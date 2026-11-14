package sonarqube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// generatedCase exercises one generated method against a fake server.
type generatedCase struct {
	name     string
	method   string
	path     string
	kind     string
	body     bool
	params   map[string][]string // wire name -> values; "header:X" for headers
	files    []string
	response string
	call     func(ctx context.Context, c *Client) (any, *Response, error)
}

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, WithToken("tkn"), WithRateLimit(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// requestParams collects query, form and multipart fields of r.
func requestParams(t *testing.T, r *http.Request) (url.Values, map[string]string, []byte) {
	t.Helper()
	vals := url.Values{}
	for k, v := range r.URL.Query() {
		vals[k] = append(vals[k], v...)
	}
	files := map[string]string{}
	raw, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		for k, v := range r.PostForm {
			vals[k] = append(vals[k], v...)
		}
	case "multipart/form-data":
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		for k, v := range r.MultipartForm.Value {
			vals[k] = append(vals[k], v...)
		}
		for k, fhs := range r.MultipartForm.File {
			f, err := fhs[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(f)
			_ = f.Close()
			files[k] = string(b)
		}
	}
	return vals, files, raw
}

func TestGeneratedEndpoints(t *testing.T) {
	seen := map[string]bool{}
	for _, tc := range generatedCases {
		if seen[tc.name] {
			t.Errorf("duplicate generated method %s", tc.name)
		}
		seen[tc.name] = true
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method {
					t.Errorf("method = %s, want %s", r.Method, tc.method)
				}
				if r.URL.Path != tc.path {
					t.Errorf("path = %s, want %s", r.URL.Path, tc.path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer tkn" {
					t.Errorf("Authorization = %q", got)
				}
				vals, files, body := requestParams(t, r)
				for name, want := range tc.params {
					if h, ok := strings.CutPrefix(name, "header:"); ok {
						if got := r.Header.Get(h); got != want[0] {
							t.Errorf("header %s = %q, want %q", h, got, want[0])
						}
						continue
					}
					if got := vals[name]; !reflect.DeepEqual(got, want) {
						t.Errorf("param %s = %q, want %q", name, got, want)
					}
				}
				for _, f := range tc.files {
					if files[f] != "data" {
						t.Errorf("file %s = %q", f, files[f])
					}
				}
				if tc.body && len(body) == 0 {
					t.Errorf("expected a request body")
				}
				if tc.kind == "none" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_, _ = io.WriteString(w, tc.response)
			})
			got, resp, err := tc.call(ctx, c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if resp == nil || resp.StatusCode/100 != 2 {
				t.Fatalf("unexpected response %+v", resp)
			}
			if tc.kind != "none" && reflect.ValueOf(got).IsZero() {
				t.Errorf("empty result %#v", got)
			}
			if tc.kind == "json" {
				// The typed result must round-trip the example's keys.
				assertKeysPreserved(t, tc.response, got)
			}
		})
		t.Run(tc.name+"/error", func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"errors":[{"msg":"boom"}]}`)
			})
			_, _, err := tc.call(context.Background(), c)
			var er *ErrorResponse
			if !errors.As(err, &er) || er.StatusCode != http.StatusBadRequest || er.Messages[0] != "boom" {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if len(generatedCases) != len(endpointRegistry) {
		t.Errorf("%d cases for %d endpoints", len(generatedCases), len(endpointRegistry))
	}
}

// assertKeysPreserved checks that marshalling the decoded value keeps every
// top-level key of the example, i.e. the generated type models it.
func assertKeysPreserved(t *testing.T, example string, got any) {
	t.Helper()
	var want map[string]json.RawMessage
	if json.Unmarshal([]byte(example), &want) != nil {
		return
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var have map[string]json.RawMessage
	if err := json.Unmarshal(b, &have); err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if _, ok := have[k]; !ok && !isEmptyJSON(v) {
			t.Errorf("key %q of the example was dropped by the generated type", k)
		}
	}
}

func isEmptyJSON(v json.RawMessage) bool {
	var buf bytes.Buffer
	_ = json.Compact(&buf, v)
	switch buf.String() {
	case "null", `""`, "0", "false", "[]", "{}":
		return true
	}
	return false
}
