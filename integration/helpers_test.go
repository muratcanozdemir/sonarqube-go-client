//go:build integration

// Package integration exercises every endpoint the client wraps against a
// real SonarQube Community Edition server. See scripts/it-seed.sh for the
// required server preparation and README.md for how to run it.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

var (
	ctx      = context.Background()
	c        *sq.Client // admin token
	baseURL  string
	token    string
	adminPwd string
	passcode string
	release  string // e.g. "26.11 CE"
	rec      = &recorder{}
	runID    = fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
)

// scanned is the project analysed by scripts/it-seed.sh.
const scanned = "it-scan"

// call is one observed HTTP exchange.
type call struct {
	method, path string
	status       int
	accepted     bool
	body         string
}

// recorder is an http.RoundTripper that records every exchange so the
// coverage check can prove each endpoint was reached.
type recorder struct {
	mu    sync.Mutex
	calls []*call
	next  http.RoundTripper
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	snippet := string(b)
	if len(snippet) > 300 {
		snippet = snippet[:300]
	}
	r.mu.Lock()
	r.calls = append(r.calls, &call{method: req.Method, path: req.URL.Path, status: resp.StatusCode, body: snippet,
		accepted: resp.StatusCode/100 == 2})
	r.mu.Unlock()
	return resp, nil
}

func (r *recorder) last() *call {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[len(r.calls)-1]
}

func newClient(opts ...sq.Option) *sq.Client {
	hc := &http.Client{Timeout: 2 * time.Minute, Transport: rec}
	cl, err := sq.NewClient(baseURL, append([]sq.Option{sq.WithHTTPClient(hc)}, opts...)...)
	if err != nil {
		panic(err)
	}
	return cl
}

func TestMain(m *testing.M) {
	baseURL = env("SONARQUBE_URL", "http://localhost:9000")
	token = os.Getenv("SONARQUBE_TOKEN")
	adminPwd = os.Getenv("SONARQUBE_ADMIN_PASSWORD")
	passcode = os.Getenv("SONARQUBE_PASSCODE")
	if token == "" || adminPwd == "" || passcode == "" {
		fmt.Println("SONARQUBE_TOKEN, SONARQUBE_ADMIN_PASSWORD and SONARQUBE_PASSCODE are required; run scripts/it-seed.sh")
		os.Exit(2)
	}
	rec.next = http.DefaultTransport
	c = newClient(sq.WithToken(token))
	v, _, err := c.Server.Version(ctx)
	if err != nil {
		fmt.Println("server version:", err)
		os.Exit(2)
	}
	release = sq.ReleaseLabel(strings.TrimSpace(string(v)), "community")
	if release == "" {
		fmt.Printf("server version %q is not in the availability matrix\n", v)
		os.Exit(2)
	}
	fmt.Printf("testing against SonarQube %s (%s)\n", strings.TrimSpace(string(v)), release)
	code := m.Run()
	if code == 0 && os.Getenv("IT_SKIP_COVERAGE") == "" {
		code = checkCoverage()
	}
	os.Exit(code)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// available reports whether the endpoint exists on the server under test.
func available(name string) bool {
	for _, e := range sq.Endpoints() {
		if e.Service+"."+e.Method == name {
			return e.AvailableOn(release).Exists()
		}
	}
	panic("unknown endpoint " + name)
}

// result carries the return values of a generated method.
type result[T any] struct {
	v    T
	resp *sq.Response
	err  error
}

func ok[T any](v T, resp *sq.Response, err error) result[T] { return result[T]{v, resp, err} }

func done(resp *sq.Response, err error) result[struct{}] {
	return result[struct{}]{struct{}{}, resp, err}
}

// must fails the test unless the call succeeded.
func (r result[T]) must(t *testing.T) T {
	t.Helper()
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}
	return r.v
}

// allow accepts success or one of the listed API error statuses. It is used
// for endpoints whose success depends on infrastructure the test server does
// not have (DevOps platforms, SMTP, licences, the marketplace); reaching the
// endpoint's validation still proves the request is well formed and routed.
func (r result[T]) allow(t *testing.T, statuses ...int) T {
	t.Helper()
	if r.err == nil {
		return r.v
	}
	code := sq.StatusCode(r.err)
	for _, s := range statuses {
		if code == s {
			if l := rec.last(); l != nil {
				if routeMissing(l) {
					t.Fatalf("endpoint not routed: %v", r.err)
				}
				l.accepted = true
			}
			t.Logf("accepted %d: %v", code, r.err)
			return r.v
		}
	}
	t.Fatalf("unexpected error (allowed %v): %v", statuses, r.err)
	return r.v
}

// routeMissing detects "no such endpoint" answers, as opposed to API errors.
func routeMissing(l *call) bool {
	return l.status == http.StatusNotFound && (strings.Contains(l.body, "Unknown url") || l.body == "" ||
		strings.Contains(l.body, "No static resource") || strings.Contains(l.body, `"error":"Not Found"`))
}

func uniq(prefix string) string { return prefix + "-" + runID }

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// checkCoverage fails unless every endpoint available on the server was
// reached with an accepted response.
func checkCoverage() int {
	type key struct{ method, path string }
	seen := map[key]bool{}
	for _, cl := range rec.calls {
		if cl.accepted {
			seen[key{cl.method, cl.path}] = true
		}
	}
	var missing, covered, skipped []string
	total := 0
	for _, e := range sq.Endpoints() {
		if !e.AvailableOn(release).Exists() && !strings.HasPrefix(e.Path, "/batch/") {
			continue
		}
		if testing.Short() && e.Service+"."+e.Method == "System.Restart" {
			skipped = append(skipped, "System.Restart (-short)")
			continue
		}
		total++
		re := regexp.MustCompile("^" + regexp.MustCompile(`\\\{[^}]*\\\}`).ReplaceAllString(regexp.QuoteMeta(e.Path), `[^/]+`) + "$")
		hit := false
		for k := range seen {
			if k.method == e.HTTPMethod && re.MatchString(k.path) {
				hit = true
				break
			}
		}
		name := e.Service + "." + e.Method
		if hit {
			covered = append(covered, name)
		} else {
			missing = append(missing, fmt.Sprintf("%s (%s %s)", name, e.HTTPMethod, e.Path))
		}
	}
	sort.Strings(missing)
	if p := os.Getenv("IT_REPORT"); p != "" {
		_ = os.WriteFile(p, []byte(jsonOf(map[string]any{
			"release": release, "endpoints": total, "covered": len(covered), "missing": missing, "skipped": skipped,
		})), 0o644)
	}
	fmt.Printf("endpoint coverage on %s: %d/%d", release, len(covered), total)
	if len(skipped) > 0 {
		fmt.Printf(" (skipped: %s)", strings.Join(skipped, ", "))
	}
	fmt.Println()
	if len(missing) > 0 {
		fmt.Println("endpoints not exercised:\n  " + strings.Join(missing, "\n  "))
		return 1
	}
	return 0
}
