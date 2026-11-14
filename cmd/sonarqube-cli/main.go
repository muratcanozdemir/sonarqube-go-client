// Command sonarqube-cli calls any SonarQube Web API endpoint wrapped by the
// sonarqube-go-client library.
//
//	sonarqube-cli list [service]
//	sonarqube-cli describe <service> <method>
//	sonarqube-cli <service> <method> [--param value ...] [--body JSON|@file]
//
// The server and credentials come from --url/--token or the SONARQUBE_URL and
// SONARQUBE_TOKEN environment variables. File parameters take @path. Repeat a
// flag to send several values.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	stop()
	os.Exit(exitCode(err))
}

// exitCode is 0 on success, 3 for API errors and 1 otherwise.
func exitCode(err error) int {
	var er *sq.ErrorResponse
	switch {
	case err == nil:
		return 0
	case errors.As(err, &er):
		return 3
	}
	return 1
}

const usage = `usage:
  sonarqube-cli [global flags] list [service]
  sonarqube-cli [global flags] describe <service> <method>
  sonarqube-cli [global flags] <service> <method> [--param value ...] [--body JSON|@file]
  sonarqube-cli version

global flags (before the command):
  --url URL        server URL (env SONARQUBE_URL)
  --token TOKEN    user token (env SONARQUBE_TOKEN)
  --rps N          client-side rate limit in requests/second (default 10, 0 = off)
  --timeout DUR    overall timeout (default 2m)
`

type globals struct {
	url, token string
	rps        float64
	timeout    time.Duration
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	g := globals{url: getenv("SONARQUBE_URL"), token: getenv("SONARQUBE_TOKEN"), rps: 10, timeout: 2 * time.Minute}
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		name := strings.TrimPrefix(args[0], "--")
		if name == "help" {
			fmt.Fprint(stdout, usage)
			return nil
		}
		if len(args) < 2 {
			return fmt.Errorf("flag --%s needs a value\n%s", name, usage)
		}
		val := args[1]
		args = args[2:]
		switch name {
		case "url":
			g.url = val
		case "token":
			g.token = val
		case "rps":
			if _, err := fmt.Sscan(val, &g.rps); err != nil {
				return fmt.Errorf("invalid --rps: %w", err)
			}
		case "timeout":
			d, err := time.ParseDuration(val)
			if err != nil {
				return fmt.Errorf("invalid --timeout: %w", err)
			}
			g.timeout = d
		default:
			return fmt.Errorf("unknown global flag --%s\n%s", name, usage)
		}
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("missing command")
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "sonarqube-cli %s (library %s)\n", version, sq.Version)
		return nil
	case "list":
		return list(stdout, args[1:])
	case "describe":
		if len(args) != 3 {
			return errors.New("usage: describe <service> <method>")
		}
		e, err := find(args[1], args[2])
		if err != nil {
			return err
		}
		return describe(stdout, e)
	}
	if len(args) < 2 {
		return fmt.Errorf("missing method for service %q", args[0])
	}
	e, err := find(args[0], args[1])
	if err != nil {
		return err
	}
	req, err := buildRequest(e, args[2:])
	if err != nil {
		return err
	}
	if g.url == "" {
		return errors.New("no server: set --url or SONARQUBE_URL")
	}
	opts := []sq.Option{sq.WithUserAgent("sonarqube-cli/" + version), sq.WithRateLimit(g.rps, max(1, int(g.rps)))}
	if g.token != "" {
		opts = append(opts, sq.WithToken(g.token))
	}
	c, err := sq.NewClient(g.url, opts...)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	var body []byte
	_, err = c.Do(ctx, req, &body)
	if err != nil {
		return err
	}
	if json.Valid(body) && len(body) > 0 {
		var v any
		_ = json.Unmarshal(body, &v)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	_, err = stdout.Write(body)
	return err
}

func find(service, method string) (sq.EndpointInfo, error) {
	for _, e := range sq.Endpoints() {
		if strings.EqualFold(e.Service, service) && strings.EqualFold(e.Method, method) {
			return e, nil
		}
	}
	return sq.EndpointInfo{}, fmt.Errorf("unknown endpoint %s %s (see: sonarqube-cli list)", service, method)
}

func list(w io.Writer, args []string) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	eps := sq.Endpoints()
	sort.Slice(eps, func(i, j int) bool {
		if eps[i].Service != eps[j].Service {
			return eps[i].Service < eps[j].Service
		}
		return eps[i].Method < eps[j].Method
	})
	n := 0
	for _, e := range eps {
		if len(args) > 0 && !strings.EqualFold(args[0], e.Service) {
			continue
		}
		flags := ""
		if e.Internal {
			flags += " internal"
		}
		if e.Deprecated {
			flags += " deprecated"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s %s\t%s\n", e.Service, e.Method, e.HTTPMethod, e.Path, strings.TrimSpace(flags))
		n++
	}
	if n == 0 {
		return fmt.Errorf("unknown service %q", args[0])
	}
	return tw.Flush()
}

func describe(w io.Writer, e sq.EndpointInfo) error {
	fmt.Fprintf(w, "%s %s\n%s %s (API %s)\n", e.Service, e.Method, e.HTTPMethod, e.Path, e.API)
	var avail []string
	for _, r := range sq.Releases() {
		if e.AvailableOn(r).Exists() {
			avail = append(avail, r)
		}
	}
	fmt.Fprintf(w, "available on: %s\nparameters:\n", strings.Join(avail, ", "))
	for _, p := range e.Params {
		req := ""
		if p.Required {
			req = " (required)"
		}
		fmt.Fprintf(w, "  --%s\t[%s]%s\n", p.Name, p.In, req)
	}
	return nil
}

// buildRequest maps --name value pairs onto the endpoint's parameters.
func buildRequest(e sq.EndpointInfo, args []string) (*sq.Request, error) {
	params := map[string]sq.ParamInfo{}
	for _, p := range e.Params {
		params[p.Name] = p
	}
	r := &sq.Request{Method: e.HTTPMethod, Path: e.Path, PathParams: map[string]string{}, Header: http.Header{}}
	values := url.Values{}
	for len(args) > 0 {
		name, ok := strings.CutPrefix(args[0], "--")
		if !ok || len(args) < 2 {
			return nil, fmt.Errorf("expected --param value, got %q", args[0])
		}
		val := args[1]
		args = args[2:]
		p, known := params[name]
		if !known {
			return nil, fmt.Errorf("%s %s has no parameter %q", e.Service, e.Method, name)
		}
		switch p.In {
		case "path":
			r.PathParams[name] = val
		case "header":
			r.Header.Set(name, val)
		case "body":
			b, err := readArg(val)
			if err != nil {
				return nil, err
			}
			if !json.Valid(b) {
				return nil, errors.New("--body must be valid JSON")
			}
			r.JSON = json.RawMessage(b)
		case "file":
			path, ok := strings.CutPrefix(val, "@")
			if !ok {
				return nil, fmt.Errorf("--%s takes @path", name)
			}
			// Read into memory so the upload can be replayed on retry.
			b, err := os.ReadFile(path) //nolint:gosec // the user names the file to upload
			if err != nil {
				return nil, err
			}
			if r.Files == nil {
				r.Files = map[string]sq.File{}
			}
			r.Files[name] = sq.File{Name: filepath.Base(path), Content: bytes.NewReader(b)}
		default:
			values.Add(name, val)
		}
	}
	for _, p := range e.Params {
		if p.In == "path" && r.PathParams[p.Name] == "" {
			return nil, fmt.Errorf("missing path parameter --%s", p.Name)
		}
	}
	if e.Multipart || r.Files != nil || (e.API == "v1" && e.HTTPMethod != http.MethodGet && r.JSON == nil) {
		r.Form = values
	} else {
		r.Query = values
	}
	r.ContentType = e.ContentType
	r.Accept = "*/*"
	return r, nil
}

func readArg(v string) ([]byte, error) {
	if path, ok := strings.CutPrefix(v, "@"); ok {
		return os.ReadFile(path) //nolint:gosec // the user names the body file
	}
	return []byte(v), nil
}
