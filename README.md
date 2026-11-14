# sonarqube-go-client

A Go client and CLI for the SonarQube Server Web API (v1) and REST API (v2).

- **659 endpoints** wrapped as typed methods: every endpoint in
  [`docs/`](docs/) (the endpoint × version and parameter matrices), across
  Community and Enterprise editions.
- **Tested against every Community Edition release from 25.1 to 26.11**: the
  integration suite calls each endpoint that exists on the release under test
  and fails if any is left unexercised.
- **Polite by default**: client-side rate limiting, bounded retries with
  exponential backoff and jitter that honour `Retry-After`, and backoff-based
  wait helpers.
- No dependencies outside the Go standard library.

## Install

```sh
go get github.com/muratcanozdemir/sonarqube-go-client/sonarqube
```

## Usage

```go
import "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"

c, err := sonarqube.NewClient("https://sonar.example.com", sonarqube.WithToken(os.Getenv("SONAR_TOKEN")))
if err != nil {
	log.Fatal(err)
}

res, _, err := c.Issues.Search(ctx, &sonarqube.IssuesSearchOptions{
	Components: []string{"my-project"},
	Ps:         sonarqube.Ptr(100),
})
if err != nil {
	log.Fatal(err)
}
for _, is := range res.Issues {
	fmt.Println(is.Key, is.Rule, is.Message)
}

gate, _, err := c.Qualitygates.ProjectStatus(ctx, &sonarqube.QualitygatesProjectStatusOptions{ProjectKey: "my-project"})
```

Services mirror the API controllers (`c.Projects`, `c.Qualitygates`,
`c.UsersManagement`, `c.SystemV2`, …). Every method takes a context and,
where the endpoint has parameters, an options struct. Optional scalars are
pointers; use `sonarqube.Ptr`. Methods return the decoded result, the raw
`*Response` (status, headers, body) and an error.

### Errors

Non-2xx responses return `*sonarqube.ErrorResponse`, which carries the status
code and SonarQube's error messages:

```go
if sonarqube.IsNotFound(err) { ... }
var apiErr *sonarqube.ErrorResponse
if errors.As(err, &apiErr) { log.Println(apiErr.StatusCode, apiErr.Messages) }
```

### Authentication

| Option | Use |
| --- | --- |
| `WithToken(t)` | user, project or global analysis token (Bearer) |
| `WithBasicAuth(login, password)` | login/password |
| `WithSystemPasscode(p)` | `X-Sonar-Passcode` for `system/liveness`, `monitoring/metrics` |

### Rate limiting, retries and waiting

The client never floods a server:

- **Rate limit**: a token bucket of 10 requests/second with a burst of 10, shared by all
  calls on the client. Change it with `WithRateLimit(rps, burst)`; `WithRateLimit(0, 0)` disables it.
- **Retries** (`DefaultRetryPolicy`: 3 retries, 0.5s base, 30s cap): a `429` is retried
  for any method because the request was rejected unprocessed. `502/503/504` and
  connection errors are retried only for idempotent methods (GET, PUT, DELETE), so a
  POST is never replayed after it may have been processed. Waits use exponential
  backoff with full jitter, and `Retry-After` wins when present. Request bodies,
  including file uploads, are buffered so retries resend identical bytes. Use
  `WithRetryPolicy(sonarqube.RetryPolicy{})` to disable retries.
- **Waiting**: `WaitForTask` (compute engine task), `WaitForEmptyQueue` and
  `WaitForStatus` (e.g. after a restart) poll with backoff from 1s to 15s by default.
  Bound them with a context deadline.

```go
ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
defer cancel()
task, err := c.WaitForTask(ctx, taskID, sonarqube.DefaultPollPolicy)
```

### Raw calls

For anything not covered, use `Client.Do`. It goes through the same limiter
and retry policy:

```go
var out json.RawMessage
_, err := c.Do(ctx, &sonarqube.Request{Method: "GET", Path: "/api/foo", Query: url.Values{"a": {"1"}}}, &out)
```

### Version awareness

`sonarqube.Endpoints()` lists every wrapped endpoint with its parameters, and
`AvailableOn(release)` reports whether it exists on a given release (data from
`docs/`):

```go
v, _, _ := c.Server.Version(ctx)
release := sonarqube.ReleaseLabel(string(v), "community") // "26.11 CE"
```

Method and parameter docs note version-specific availability and changes,
for example "Community Edition: 26.10–26.11." or "Changes: added (optional) in 26.10."

## CLI

Tagged releases ship a Linux x86-64 binary
(`sonarqube-cli_<version>_linux_amd64.tar.gz`), plus SPDX and CycloneDX SBOMs and
`SHA256SUMS`.

```sh
export SONARQUBE_URL=https://sonar.example.com SONARQUBE_TOKEN=...
sonarqube-cli list issues
sonarqube-cli describe qualitygates projectstatus
sonarqube-cli issues search --components my-project --ps 5
sonarqube-cli qualityprofiles restore --backup @profile.xml
sonarqube-cli usersmanagement update --id <uuid> --body '{"name":"New"}'
```

Exit codes: 0 success, 3 API error, 1 other errors. `--rps` sets the client
rate limit.

### Verifying a release

Each archive has a signed SLSA build provenance attestation and an SBOM
attestation:

```sh
gh attestation verify sonarqube-cli_1.2.3_linux_amd64.tar.gz --repo muratcanozdemir/sonarqube-go-client
gh attestation verify sonarqube-cli_1.2.3_linux_amd64.tar.gz --repo muratcanozdemir/sonarqube-go-client \
  --predicate-type https://spdx.dev/Document/v2.3
sha256sum -c SHA256SUMS --ignore-missing
```

## How the client is built

The `*_gen.go` files in `sonarqube/` are generated by [`tools/codegen`](tools/codegen) from:

- `docs/*.xlsx`: the endpoint list, client method names, per-version
  availability, and the union of parameters across versions;
- `spec/harvest/`: `api/webservices/list`, response examples and
  `api/v2/api-docs`, harvested from the CE 26.11 and EE 2026.5.5 images. These
  supply descriptions, parameter types, request bodies and response models;
- `spec/type-overrides.json`: corrections where a published example disagrees
  with real responses (found by the integration suite).

Run `make generate` after changing any of them. CI fails if the generated code is
stale.

Deviations from the matrices: the scanner endpoints are served at `/batch/*`, not
`/api/batch/*`, and the client uses the working paths. v1 identifiers are typed
`sonarqube.ID`, which decodes from either a number or a string, because
documentation examples show numbers while servers return UUIDs.

## Repository layout

```
sonarqube/          the library (package sonarqube)
  client.go …       hand-written core: transport, auth, encoding, errors,
                    rate limiting/retries, wait helpers, endpoint registry API
  api_*_gen.go      one file per API service: options types and methods
  models_gen.go     request/response types
  services_gen.go   service wiring;  registry_gen.go  endpoint metadata
cmd/sonarqube-cli/  the CLI
integration/        integration suite (build tag "integration") and fixture project
tools/codegen/      code generator (separate module)
docs/               endpoint and parameter matrices (generator input)
spec/               harvested API metadata and type overrides (generator input)
scripts/            integration server seeding
```

## Testing

```sh
make test          # unit tests (race detector); library coverage is 100%
make integration-up SQ_VERSION=26.11.0.134175   # start + seed a CE server
make integration   # every endpoint on that release; ~1 min, plus up to ~5 min for the restart test
make integration SHORT=1   # skip the restart test
```

The seed script analyses `integration/testdata/project` twice with
`sonar-scanner-cli` so that issues, measures, sources, duplications and
analyses exist. Endpoints that need infrastructure a test server cannot have
(DevOps platforms, SMTP, the marketplace, licences) are still called. The test
then asserts the specific rejection SonarQube gives, which proves routing and
request shape. Each such case is documented inline.

## CI/CD

- **CI** (`.github/workflows/ci.yml`, every push/PR, a few minutes): generated-code
  drift check, gofmt, vet, golangci-lint, unit tests with race detector and a
  100% library-coverage gate, and a SonarQube scan. The scan needs the
  `SONAR_TOKEN` secret and the `SONAR_HOST_URL` variable; it is skipped with a
  warning when they are absent.
- **Integration** (`.github/workflows/integration.yml`): each job boots a real
  server, so runs are tiered by cost:

  | Trigger | Releases | Restart test |
  | --- | --- | --- |
  | PR / push to main touching `sonarqube/`, `integration/`, `spec/`, `docs/`, `go.mod` | 25.1 and 26.11 (oldest, newest) | skipped (`-short`) |
  | release-please PR | all 23 CE releases | yes |
  | weekly (Mon 03:17 UTC) | all 23 | yes |
  | manual dispatch | chosen builds or `all` | optional |

  The 23 releases have 15 distinct API surfaces, so testing only those would
  save little; the full matrix runs once a week and before every release.
- **Release** (`.github/workflows/release.yml`): release-please maintains a
  release PR from Conventional Commits. Merging it tags `vX.Y.Z` and creates the
  GitHub release; the workflow then builds the Linux x86-64 binary
  reproducibly, generates SBOMs, attests SLSA provenance and the SBOM via
  Sigstore, and uploads the assets. Set `RELEASE_PLEASE_TOKEN` (PAT or GitHub
  App token) if CI should run on release PRs.
- Actions are pinned to commit SHAs; Dependabot keeps them and Go modules
  current.

## License

[MIT](LICENSE)
