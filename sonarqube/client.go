// Package sonarqube is a Go client for the SonarQube Server Web API (v1) and
// REST API (v2).
//
// Every endpoint is exposed as a method on a service attached to [Client], for
// example:
//
//	c, err := sonarqube.NewClient("https://sonar.example.com", sonarqube.WithToken(token))
//	if err != nil { ... }
//	issues, _, err := c.Issues.Search(ctx, &sonarqube.IssuesSearchOptions{Projects: []string{"my-project"}})
//
// Endpoints that are not wrapped by a typed method can be called with
// [Client.Do].
package sonarqube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultUserAgent = "sonarqube-go-client/" + Version

// Client talks to one SonarQube Server instance.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	userAgent  string

	token    string
	username string
	password string
	passcode string

	retry   RetryPolicy
	limiter *limiter

	services
}

// Option configures a [Client].
type Option func(*Client) error

// WithToken authenticates with a user, project analysis or global analysis
// token, sent as a bearer token.
func WithToken(token string) Option {
	return func(c *Client) error {
		c.token = token
		return nil
	}
}

// WithBasicAuth authenticates with a login and password.
func WithBasicAuth(username, password string) Option {
	return func(c *Client) error {
		c.username, c.password = username, password
		return nil
	}
}

// WithSystemPasscode sends the X-Sonar-Passcode header, accepted by
// monitoring endpoints such as system/health and monitoring/metrics.
func WithSystemPasscode(passcode string) Option {
	return func(c *Client) error {
		c.passcode = passcode
		return nil
	}
}

// WithHTTPClient replaces the default HTTP client (30s timeout).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) error {
		if hc == nil {
			return errors.New("sonarqube: nil http client")
		}
		c.httpClient = hc
		return nil
	}
}

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) error {
		c.userAgent = ua
		return nil
	}
}

// NewClient returns a client for the SonarQube instance at baseURL, for
// example "https://sonar.example.com" or "http://localhost:9000/sonar".
func NewClient(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("sonarqube: invalid base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("sonarqube: base URL %q must use http or https", baseURL)
	}
	c := &Client{
		baseURL:    u,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		userAgent:  defaultUserAgent,
		retry:      DefaultRetryPolicy,
		limiter:    newLimiter(10, 10),
	}
	for _, o := range opts {
		if err := o(c); err != nil {
			return nil, err
		}
	}
	c.initServices()
	return c, nil
}

// BaseURL returns the instance URL the client was created with.
func (c *Client) BaseURL() string { return c.baseURL.String() }

// Response wraps the HTTP response of a call. The body has already been
// read and closed; it is available in Body.
type Response struct {
	*http.Response
	Body []byte
}

// Request describes a raw API call for [Client.Do].
type Request struct {
	// Method is the HTTP method, e.g. http.MethodGet.
	Method string
	// Path is the API path, e.g. "/api/issues/search" or "/api/v2/users-management/users/{id}".
	// Path parameters in braces are substituted from PathParams.
	Path string
	// PathParams fill "{name}" placeholders in Path.
	PathParams map[string]string
	// Query is sent as the URL query string.
	Query url.Values
	// Form is sent as an application/x-www-form-urlencoded body (v1 POST).
	Form url.Values
	// Files are sent with Form as a multipart/form-data body.
	Files map[string]File
	// JSON is marshalled as the request body (v2).
	JSON any
	// RawBody is sent verbatim; ContentType should be set alongside it.
	RawBody []byte
	// Header holds extra request headers.
	Header http.Header
	// ContentType overrides the request Content-Type for a JSON body,
	// e.g. "application/merge-patch+json".
	ContentType string
	// Accept sets the Accept header. Defaults to application/json.
	Accept string
}

// File is a file upload for a multipart request.
type File struct {
	Name    string
	Content io.Reader
}

// Do performs a raw API call. If out is non-nil and the response has a body,
// the body is decoded into out as JSON (or copied verbatim when out is a
// *[]byte). Non-2xx responses return an *[ErrorResponse].
//
// Requests are paced by the client's rate limiter and retried according to
// its [RetryPolicy].
func (c *Client) Do(ctx context.Context, r *Request, out any) (*Response, error) {
	req, err := c.newRequest(ctx, r)
	if err != nil {
		return nil, err
	}
	res, err := c.send(ctx, req)
	if err != nil {
		return res, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return res, newErrorResponse(res)
	}
	if out == nil || len(res.Body) == 0 {
		return res, nil
	}
	if b, ok := out.(*[]byte); ok {
		*b = res.Body
		return res, nil
	}
	if err := json.Unmarshal(res.Body, out); err != nil {
		return res, fmt.Errorf("sonarqube: decoding %s %s response: %w", r.Method, r.Path, err)
	}
	return res, nil
}

// send performs req with rate limiting and retries, returning the last
// response (any status) with its body read.
func (c *Client) send(ctx context.Context, req *http.Request) (*Response, error) {
	for attempt := 0; ; attempt++ {
		if c.limiter != nil {
			if err := c.limiter.wait(ctx); err != nil {
				return nil, err
			}
		}
		try := req.Clone(ctx)
		if req.GetBody != nil {
			try.Body, _ = req.GetBody() // bodies are in-memory readers
		}
		resp, err := c.httpClient.Do(try)
		var res *Response
		if err == nil {
			body, rerr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if rerr != nil {
				err = rerr
			} else {
				res = &Response{Response: resp, Body: body}
			}
		}
		if attempt >= c.retry.MaxRetries || !shouldRetry(req.Method, resp, err) {
			return res, err
		}
		var hint *http.Response
		if res != nil {
			hint = res.Response
		}
		if serr := sleep(ctx, c.retry.backoff(attempt, hint)); serr != nil {
			if err == nil {
				return res, nil
			}
			return nil, err
		}
	}
}

func (c *Client) newRequest(ctx context.Context, r *Request) (*http.Request, error) {
	path := r.Path
	for k, v := range r.PathParams {
		path = strings.ReplaceAll(path, "{"+k+"}", url.PathEscape(v))
	}
	if strings.Contains(path, "{") {
		return nil, fmt.Errorf("sonarqube: unresolved path parameter in %q", path)
	}
	u := *c.baseURL
	raw := strings.TrimSuffix(c.baseURL.EscapedPath(), "/") + path
	u.Path, _ = url.PathUnescape(raw) // raw was built from escaped parts
	u.RawPath = raw
	if len(r.Query) > 0 {
		u.RawQuery = r.Query.Encode()
	}

	var body io.Reader
	contentType := ""
	switch {
	case len(r.Files) > 0:
		buf := &bytes.Buffer{}
		mw := multipart.NewWriter(buf)
		// Writes to a bytes.Buffer cannot fail; only file readers can.
		for k, vs := range r.Form {
			for _, v := range vs {
				_ = mw.WriteField(k, v)
			}
		}
		for field, f := range r.Files {
			w, _ := mw.CreateFormFile(field, f.Name)
			if f.Content != nil {
				if _, err := io.Copy(w, f.Content); err != nil {
					return nil, err
				}
			}
		}
		_ = mw.Close()
		body, contentType = buf, mw.FormDataContentType()
	case r.Form != nil:
		body, contentType = strings.NewReader(r.Form.Encode()), "application/x-www-form-urlencoded"
	case r.RawBody != nil:
		body, contentType = bytes.NewReader(r.RawBody), r.ContentType
	case r.JSON != nil:
		b, err := json.Marshal(r.JSON)
		if err != nil {
			return nil, err
		}
		contentType = r.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), body)
	if err != nil {
		return nil, err
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if req.Header.Get("Accept") == "" {
		accept := r.Accept
		if accept == "" {
			accept = "application/json"
		}
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", c.userAgent)
	switch {
	case c.token != "":
		req.Header.Set("Authorization", "Bearer "+c.token)
	case c.username != "":
		req.SetBasicAuth(c.username, c.password)
	}
	if c.passcode != "" {
		req.Header.Set("X-Sonar-Passcode", c.passcode)
	}
	return req, nil
}
