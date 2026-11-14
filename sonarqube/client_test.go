package sonarqube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewClientValidation(t *testing.T) {
	for _, u := range []string{"ftp://x", "://bad", "localhost:9000"} {
		if _, err := NewClient(u); err == nil {
			t.Errorf("NewClient(%q) succeeded", u)
		}
	}
	if _, err := NewClient("http://x", WithHTTPClient(nil)); err == nil {
		t.Error("nil http client accepted")
	}
	c, err := NewClient("https://sonar.example.com/sub/")
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL() != "https://sonar.example.com/sub" {
		t.Errorf("BaseURL = %s", c.BaseURL())
	}
}

func TestClientOptionsAndHeaders(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	hc := &http.Client{Timeout: time.Second}
	c, err := NewClient(srv.URL+"/sonar", WithBasicAuth("u", "p"), WithSystemPasscode("pc"), WithHTTPClient(hc), WithUserAgent("ua/1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/api/x/{k}", PathParams: map[string]string{"k": "a b"}}, nil); err != nil {
		t.Fatal(err)
	}
	if u, p, ok := got.BasicAuth(); !ok || u != "u" || p != "p" {
		t.Errorf("basic auth = %q %q %v", u, p, ok)
	}
	if got.Header.Get("X-Sonar-Passcode") != "pc" || got.Header.Get("User-Agent") != "ua/1" {
		t.Errorf("headers = %v", got.Header)
	}
	if got.URL.Path != "/sonar/api/x/a b" {
		t.Errorf("path = %q", got.URL.Path)
	}
}

func TestDoErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2err":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"nope"}`)
		case "/plain":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `forbidden`)
		case "/badjson":
			_, _ = io.WriteString(w, `{`)
		case "/short":
			w.Header().Set("Content-Length", "10")
			_, _ = io.WriteString(w, `{`)
		}
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	ctx := context.Background()

	_, err := c.Do(ctx, &Request{Method: "GET", Path: "/v2err"}, nil)
	if !IsNotFound(err) || IsForbidden(err) || IsUnauthorized(err) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "404: nope") {
		t.Errorf("Error() = %q", err.Error())
	}
	_, err = c.Do(ctx, &Request{Method: "GET", Path: "/plain"}, nil)
	if !IsForbidden(err) || !strings.HasSuffix(err.Error(), "Forbidden") {
		t.Errorf("err = %v", err)
	}
	if StatusCode(errors.New("x")) != 0 {
		t.Error("StatusCode of plain error")
	}
	var out map[string]any
	if _, err = c.Do(ctx, &Request{Method: "GET", Path: "/badjson"}, &out); err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("err = %v", err)
	}
	if _, err = c.Do(ctx, &Request{Method: "GET", Path: "/short"}, &out); err == nil {
		t.Error("short body accepted")
	}
	if _, err = c.Do(ctx, &Request{Method: "GET", Path: "/x/{missing}"}, nil); err == nil {
		t.Error("unresolved path param accepted")
	}
	if _, err = c.Do(ctx, &Request{Method: "GET", Path: "/x", JSON: make(chan int)}, nil); err == nil {
		t.Error("unmarshalable JSON accepted")
	}
	if _, err = c.Do(ctx, &Request{Method: "bad method", Path: "/x"}, nil); err == nil {
		t.Error("invalid method accepted")
	}
	srv.Close()
	if _, err = c.Do(ctx, &Request{Method: "GET", Path: "/x"}, nil); err == nil {
		t.Error("closed server accepted")
	}
	if _, err = c.call(ctx, endpoint{method: "GET", path: "/x"}, 42, nil); err == nil {
		t.Error("non-struct options accepted")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestMultipartAndEncoding(t *testing.T) {
	var vals url.Values
	var file string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		vals = r.MultipartForm.Value
		f, _ := r.MultipartForm.File["f"][0].Open()
		b, _ := io.ReadAll(f)
		file = string(b)
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	ctx := context.Background()
	_, err := c.Do(ctx, &Request{Method: "POST", Path: "/up", Form: url.Values{"a": {"1"}}, Files: map[string]File{"f": {Name: "x", Content: strings.NewReader("hello")}, "empty": {Name: "e"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if vals["a"][0] != "1" || file != "hello" {
		t.Errorf("vals=%v file=%q", vals, file)
	}
	if _, err := c.Do(ctx, &Request{Method: "POST", Path: "/up", Files: map[string]File{"f": {Name: "x", Content: errReader{}}}}, nil); err == nil {
		t.Error("file read error ignored")
	}

	type opts struct {
		Path   string   `path:"id"`
		I      int      `url:"i"`
		F      float32  `url:"f"`
		Ints   []int    `url:"n,comma"`
		Empty  string   `url:"e"`
		Other  struct{} `url:"o"`
		NoTag  string
		Body   string  `body:""`
		Header *string `header:"X-H"`
	}
	enc, err := encodeOptions(&opts{Path: "p", I: 3, F: 1.5, Ints: []int{1, 2}, Body: ""})
	if err != nil {
		t.Fatal(err)
	}
	if enc.values.Encode() != "f=1.5&i=3&n=1%2C2" || enc.body != nil || enc.header != nil {
		t.Errorf("encoded = %v body=%v", enc.values, enc.body)
	}
	type nils struct {
		F *File    `file:"f"`
		B *opts    `body:""`
		S []string `url:"s"`
	}
	if e, err := encodeOptions(&nils{S: []string{}}); err != nil || e.files != nil || e.body != nil || len(e.values) != 0 {
		t.Errorf("nil fields encoded: %+v", e)
	}
	if _, err := encodeOptions(&opts{}); err == nil {
		t.Error("missing path param accepted")
	}
	if e, err := encodeOptions((*opts)(nil)); err != nil || len(e.values) != 0 {
		t.Error("nil options")
	}
	if Ptr(3) == nil {
		t.Error("Ptr")
	}
}

func TestEndpointRegistry(t *testing.T) {
	eps := Endpoints()
	if len(eps) == 0 {
		t.Fatal("no endpoints")
	}
	rel := Releases()
	if len(rel) == 0 || rel[0] != "25.1 CE" {
		t.Fatalf("releases = %v", rel)
	}
	var search EndpointInfo
	for _, e := range eps {
		if e.Service == "Issues" && e.Method == "Search" {
			search = e
		}
	}
	if a := search.AvailableOn("26.11 CE"); !a.Exists() || a != Public {
		t.Errorf("issues/search on 26.11 = %c", a)
	}
	if search.AvailableOn("1.0 CE") != Unavailable || Unavailable.Exists() {
		t.Error("unknown release")
	}
	cases := map[[2]string]string{
		{"26.11.0.134175", "community"}:   "26.11 CE",
		{"25.1.0.102122", ""}:             "25.1 CE",
		{"2026.5.5.134787", "enterprise"}: "2026.5.5 EE",
		{"9.9.1.1", "community"}:          "",
		{"x", "enterprise"}:               "",
	}
	for in, want := range cases {
		if got := ReleaseLabel(in[0], in[1]); got != want {
			t.Errorf("ReleaseLabel(%v) = %q, want %q", in, got, want)
		}
	}
}
