#!/usr/bin/env bash
# Prepares a fresh SonarQube instance for the integration tests:
#   - waits until the server is UP
#   - replaces the default admin password
#   - creates an admin token
#   - analyses integration/testdata/project twice with sonar-scanner-cli
# The server should be started with SONAR_WEB_SYSTEMPASSCODE set to the value
# of $SONARQUBE_PASSCODE so that monitoring endpoints can be tested.
# Writes SONARQUBE_URL, SONARQUBE_TOKEN and SONARQUBE_ADMIN_PASSWORD and SONARQUBE_PASSCODE to the
# file named by $1 (default: .it.env), in KEY=value form.
set -euo pipefail

URL=${SONARQUBE_URL:-http://localhost:9000}
PASS=${SONARQUBE_ADMIN_PASSWORD:-It-Admin-Passw0rd!}
OUT=${1:-.it.env}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SCANNER_IMAGE=${SCANNER_IMAGE:-sonarsource/sonar-scanner-cli:11}

# poll CMD... runs CMD with exponential backoff (1s doubling, capped at 15s)
# until it succeeds or POLL_TIMEOUT seconds (default 600) have elapsed.
poll() {
  local delay=1 deadline=$((SECONDS + ${POLL_TIMEOUT:-600}))
  until "$@"; do
    if ((SECONDS + delay > deadline)); then return 1; fi
    sleep "$delay"
    delay=$((delay * 2 > 15 ? 15 : delay * 2))
  done
}

is_up() { curl -fsS "$URL/api/system/status" 2>/dev/null | grep -q '"status":"UP"'; }

echo "waiting for $URL"
poll is_up || { echo "server not UP"; exit 1; }

if curl -fsS -u admin:admin "$URL/api/authentication/validate" | grep -q '"valid":true'; then
  curl -fsS -u admin:admin -X POST "$URL/api/users/change_password" \
    --data-urlencode login=admin --data-urlencode previousPassword=admin --data-urlencode "password=$PASS"
fi

TOKEN=$(curl -fsS -u "admin:$PASS" -X POST "$URL/api/user_tokens/generate" \
  --data-urlencode "name=it-$(date +%s%N)" | sed -E 's/.*"token":"([^"]+)".*/\1/')

# curl retries transient failures (429/5xx, connection errors) with backoff
# and honours Retry-After.
api() { curl -fsS --retry 3 --retry-delay 0 -H "Authorization: Bearer $TOKEN" "$@"; }

# Built-in default profiles may not activate security-sensitive rules; use a copy that does,
# so the analysis produces security hotspots.
if ! api "$URL/api/qualityprofiles/search?language=py" | grep -q '"name":"it-python"'; then
  SW=$(api "$URL/api/qualityprofiles/search?language=py" | python3 -c 'import json,sys;print([p["key"] for p in json.load(sys.stdin)["profiles"] if p["isDefault"]][0])')
  NEW=$(api -X POST "$URL/api/qualityprofiles/copy" --data-urlencode "fromKey=$SW" --data-urlencode toName=it-python | python3 -c 'import json,sys;print(json.load(sys.stdin)["key"])')
  for r in python:S4790 python:S2068 python:S5332 python:S4721; do
    api -X POST "$URL/api/qualityprofiles/activate_rule" --data-urlencode "key=$NEW" --data-urlencode "rule=$r" || true
  done
  api -X POST "$URL/api/qualityprofiles/set_default" --data-urlencode language=py --data-urlencode qualityProfile=it-python
fi

scan() {
  docker run --rm --network host -v "$ROOT/integration/testdata/project:/usr/src" \
    -e SONAR_HOST_URL="$URL" -e SONAR_TOKEN="$TOKEN" "$SCANNER_IMAGE" \
    -Dsonar.projectVersion="$1" -Dsonar.working.directory=/tmp/scannerwork >/dev/null
}
scan 1.0
scan 2.0

queue_empty() { api "$URL/api/analysis_reports/is_queue_empty" | grep -q true; }
echo "waiting for background tasks"
poll queue_empty || { echo "background tasks did not finish"; exit 1; }

{
  echo "SONARQUBE_URL=$URL"
  echo "SONARQUBE_TOKEN=$TOKEN"
  echo "SONARQUBE_ADMIN_PASSWORD=$PASS"
  echo "SONARQUBE_PASSCODE=${SONARQUBE_PASSCODE:-}"
} > "$OUT"
echo "seeded; wrote $OUT"
