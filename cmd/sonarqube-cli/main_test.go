package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, env map[string]string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(context.Background(), args, &out, &errOut, func(k string) string { return env[k] })
	return out.String() + errOut.String(), err
}

func TestCLIOffline(t *testing.T) {
	if out, err := runCLI(t, nil, "version"); err != nil || !strings.Contains(out, "sonarqube-cli dev") {
		t.Errorf("version: %q %v", out, err)
	}
	if out, err := runCLI(t, nil, "--help"); err != nil || !strings.Contains(out, "usage") {
		t.Errorf("help: %q %v", out, err)
	}
	if out, err := runCLI(t, nil, "list", "issues"); err != nil || !strings.Contains(out, "/api/issues/search") {
		t.Errorf("list: %v", err)
	}
	if out, err := runCLI(t, nil, "list"); err != nil || strings.Count(out, "\n") < 600 {
		t.Errorf("list all: %v", err)
	}
	if out, err := runCLI(t, nil, "describe", "Issues", "Search"); err != nil || !strings.Contains(out, "--components") || !strings.Contains(out, "26.11 CE") {
		t.Errorf("describe: %q %v", out, err)
	}
	if out, err := runCLI(t, nil, "describe", "projects", "create"); err != nil || !strings.Contains(out, "(required)") {
		t.Errorf("describe required: %q %v", out, err)
	}
	if exitCode(nil) != 0 || exitCode(io.EOF) != 1 {
		t.Error("exit codes")
	}
	if out, err := runCLI(t, nil, "list", "dashboards"); err != nil || !strings.Contains(out, "internal") && !strings.Contains(out, "Dashboards") {
		t.Errorf("list dashboards: %q %v", out, err)
	}
	bad := [][]string{
		{},
		{"--url"},
		{"--bogus", "x", "list"},
		{"--rps", "x", "list"},
		{"--timeout", "x", "list"},
		{"list", "nosuch"},
		{"describe", "Issues"},
		{"describe", "No", "Such"},
		{"issues"},
		{"issues", "nosuch"},
		{"issues", "search", "--nosuch", "1"},
		{"issues", "search", "dangling"},
		{"issues", "search"}, // no server configured
		{"usersmanagement", "get"},
		{"usersmanagement", "update", "--id", "1", "--body", "{bad"},
		{"usersmanagement", "update", "--id", "1", "--body", "@/no/such/file"},
		{"qualityprofiles", "restore", "--backup", "nofile"},
		{"qualityprofiles", "restore", "--backup", "@/no/such/file"},
		{"--url", "ftp://x", "issues", "search"},
	}
	for _, a := range bad {
		if _, err := runCLI(t, nil, a...); err == nil {
			t.Errorf("%v succeeded", a)
		}
	}
}

func TestCLICalls(t *testing.T) {
	var got []*http.Request
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r)
		bodies = append(bodies, string(b))
		switch r.URL.Path {
		case "/api/server/version":
			_, _ = io.WriteString(w, "26.11.0.1")
		case "/api/issues/search":
			_, _ = io.WriteString(w, `{"total":0}`)
		case "/api/projects/delete":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"errors":[{"msg":"nope"}]}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer srv.Close()
	env := map[string]string{"SONARQUBE_URL": srv.URL, "SONARQUBE_TOKEN": "tkn"}

	out, err := runCLI(t, env, "issues", "search", "--components", "a", "--components", "b", "--ps", "5")
	if err != nil || !strings.Contains(out, `"total": 0`) {
		t.Fatalf("search: %q %v", out, err)
	}
	if q := got[0].URL.Query(); q["components"][1] != "b" || q.Get("ps") != "5" || got[0].Header.Get("Authorization") != "Bearer tkn" {
		t.Errorf("query = %v", q)
	}
	if out, err := runCLI(t, env, "--rps", "0", "--timeout", "5s", "server", "version"); err != nil || out != "26.11.0.1" {
		t.Errorf("version: %q %v", out, err)
	}
	if _, err := runCLI(t, env, "projects", "create", "--project", "p", "--name", "n"); err != nil || bodies[2] != "name=n&project=p" {
		t.Errorf("form post: %q %v", bodies[2], err)
	}
	dir := t.TempDir()
	body := filepath.Join(dir, "b.json")
	_ = os.WriteFile(body, []byte(`{"name":"x"}`), 0o644)
	if _, err := runCLI(t, env, "--token", "t2", "usersmanagement", "update", "--id", "u1", "--body", "@"+body); err != nil {
		t.Fatal(err)
	}
	last := got[len(got)-1]
	if last.URL.Path != "/api/v2/users-management/users/u1" || last.Header.Get("Content-Type") != "application/merge-patch+json" || bodies[len(bodies)-1] != `{"name":"x"}` {
		t.Errorf("patch: %s %s %q", last.URL.Path, last.Header.Get("Content-Type"), bodies[len(bodies)-1])
	}
	backup := filepath.Join(dir, "p.xml")
	_ = os.WriteFile(backup, []byte("<profile/>"), 0o644)
	if _, err := runCLI(t, env, "qualityprofiles", "restore", "--backup", "@"+backup); err != nil {
		t.Fatal(err)
	}
	if ct := got[len(got)-1].Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
		t.Errorf("restore content type %q", ct)
	}
	if _, err := runCLI(t, env, "sca", "getsbomreport", "--component", "p", "--Accept", "text/csv"); err != nil || got[len(got)-1].Header.Get("Accept") != "text/csv" {
		t.Errorf("header param: %v", err)
	}
	if _, err := runCLI(t, env, "projects", "delete", "--project", "p"); exitCode(err) != 3 || !strings.Contains(err.Error(), "nope") {
		t.Errorf("api error: %v", err)
	}
	if _, err := runCLI(t, env, "--timeout", "1ns", "issues", "search"); err == nil {
		t.Error("timeout ignored")
	}
}
