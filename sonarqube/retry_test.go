package sonarqube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var fastRetry = RetryPolicy{MaxRetries: 3, MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}

func retryServer(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32, *[]string) {
	t.Helper()
	var n atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		i := int(n.Add(1)) - 1
		if i < len(statuses) {
			if statuses[i] == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "0")
			}
			w.WriteHeader(statuses[i])
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &n, &bodies
}

func TestRetryIdempotentAndThrottled(t *testing.T) {
	ctx := context.Background()

	srv, n, _ := retryServer(t, 503, 502, 504)
	c, _ := NewClient(srv.URL, WithRetryPolicy(fastRetry))
	var out map[string]bool
	if _, err := c.Do(ctx, &Request{Method: "GET", Path: "/x"}, &out); err != nil || !out["ok"] || n.Load() != 4 {
		t.Fatalf("GET after 3 transient failures: err=%v calls=%d", err, n.Load())
	}

	// POST is not replayed on 503 (the server may have processed it)...
	srv, n, _ = retryServer(t, 503)
	c, _ = NewClient(srv.URL, WithRetryPolicy(fastRetry))
	if _, err := c.Do(ctx, &Request{Method: "POST", Path: "/x", Form: map[string][]string{"a": {"1"}}}, nil); StatusCode(err) != 503 || n.Load() != 1 {
		t.Fatalf("POST 503: err=%v calls=%d", err, n.Load())
	}
	// ...but is replayed, with an identical body, after 429.
	srv, n, bodies := retryServer(t, 429, 429)
	c, _ = NewClient(srv.URL, WithRetryPolicy(fastRetry))
	if _, err := c.Do(ctx, &Request{Method: "POST", Path: "/x", Form: map[string][]string{"a": {"1"}}}, nil); err != nil || n.Load() != 3 {
		t.Fatalf("POST 429: err=%v calls=%d", err, n.Load())
	}
	for _, b := range *bodies {
		if b != "a=1" {
			t.Errorf("replayed body = %q", b)
		}
	}

	// Retries are bounded.
	srv, n, _ = retryServer(t, 503, 503, 503, 503, 503)
	c, _ = NewClient(srv.URL, WithRetryPolicy(fastRetry))
	if _, err := c.Do(ctx, &Request{Method: "GET", Path: "/x"}, nil); StatusCode(err) != 503 || n.Load() != 4 {
		t.Fatalf("exhausted: err=%v calls=%d", err, n.Load())
	}
	// Disabled retries.
	srv, n, _ = retryServer(t, 503)
	c, _ = NewClient(srv.URL, WithRetryPolicy(RetryPolicy{}))
	if _, err := c.Do(ctx, &Request{Method: "GET", Path: "/x"}, nil); StatusCode(err) != 503 || n.Load() != 1 {
		t.Fatalf("disabled: err=%v calls=%d", err, n.Load())
	}
}

func TestRetryNetworkErrorsAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := NewClient(url, WithRetryPolicy(fastRetry))
	if _, err := c.Do(context.Background(), &Request{Method: "GET", Path: "/x"}, nil); err == nil {
		t.Fatal("expected connection error")
	}

	// Cancellation during a backoff wait stops retrying.
	srv2, n, _ := retryServer(t, 503, 503, 503, 503)
	c, _ = NewClient(srv2.URL, WithRetryPolicy(RetryPolicy{MaxRetries: 3, MinBackoff: time.Hour, MaxBackoff: time.Hour}))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Do(ctx, &Request{Method: "GET", Path: "/x"}, nil); StatusCode(err) != 503 || n.Load() != 1 {
		t.Fatalf("cancelled backoff: err=%v calls=%d", err, n.Load())
	}
	// Connection error during a cancelled backoff returns that error.
	c, _ = NewClient(url, WithRetryPolicy(RetryPolicy{MaxRetries: 3, MinBackoff: time.Hour, MaxBackoff: time.Hour}))
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	if _, err := c.Do(ctx2, &Request{Method: "GET", Path: "/x"}, nil); err == nil {
		t.Fatal("expected error")
	}
	if shouldRetry("GET", nil, context.Canceled) {
		t.Error("cancelled requests must not be retried")
	}
}

func TestBackoffAndRetryAfter(t *testing.T) {
	p := RetryPolicy{MaxRetries: 5, MinBackoff: 100 * time.Millisecond, MaxBackoff: time.Second}
	for attempt := range 10 {
		if d := p.backoff(attempt, nil); d < 0 || d > time.Second {
			t.Errorf("backoff(%d) = %v", attempt, d)
		}
	}
	h := &http.Response{Header: http.Header{"Retry-After": {"7"}}}
	if d := p.backoff(0, h); d != time.Second {
		t.Errorf("Retry-After capped = %v", d)
	}
	h.Header.Set("Retry-After", time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat))
	if d := p.backoff(0, h); d != 0 {
		t.Errorf("past date = %v", d)
	}
	for _, v := range []string{"", "soon", "-1"} {
		if _, ok := retryAfter(v); ok {
			t.Errorf("retryAfter(%q) parsed", v)
		}
	}
	if d := (RetryPolicy{}).backoff(3, nil); d != 0 {
		t.Errorf("zero policy backoff = %v", d)
	}
	if err := sleep(context.Background(), 0); err != nil {
		t.Error(err)
	}
}

func TestOptionValidation(t *testing.T) {
	bad := []Option{
		WithRetryPolicy(RetryPolicy{MaxRetries: -1}),
		WithRetryPolicy(RetryPolicy{MinBackoff: time.Second, MaxBackoff: time.Millisecond}),
		WithRateLimit(5, 0),
	}
	for i, o := range bad {
		if _, err := NewClient("http://x", o); err == nil {
			t.Errorf("option %d accepted", i)
		}
	}
	c, err := NewClient("http://x", WithRateLimit(0, 0))
	if err != nil || c.limiter != nil {
		t.Error("rate limit not disabled")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(10, 2)
	l.now = func() time.Time { return now }
	ctx := context.Background()
	// Burst passes immediately.
	for range 2 {
		start := time.Now()
		if err := l.wait(ctx); err != nil || time.Since(start) > 50*time.Millisecond {
			t.Fatalf("burst request delayed: %v", err)
		}
	}
	// The next one must wait ~100ms (10 rps).
	start := time.Now()
	if err := l.wait(ctx); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Errorf("throttled request waited only %v", d)
	}
	// Refill after time passes.
	now = now.Add(time.Second)
	start = time.Now()
	if err := l.wait(ctx); err != nil || time.Since(start) > 50*time.Millisecond {
		t.Error("refilled bucket delayed")
	}
	// A cancelled wait returns the reservation.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	l.tokens = -5
	if err := l.wait(cctx); !errors.Is(err, context.Canceled) || l.tokens != -5 {
		t.Errorf("cancel: err=%v tokens=%v", err, l.tokens)
	}

	// Through the client.
	srv, _, _ := retryServer(t)
	c, _ := NewClient(srv.URL, WithRateLimit(1, 1))
	cctx2, cancel2 := context.WithCancel(ctx)
	if _, err := c.Do(cctx2, &Request{Method: "GET", Path: "/x"}, nil); err != nil {
		t.Fatal(err)
	}
	cancel2()
	if _, err := c.Do(cctx2, &Request{Method: "GET", Path: "/x"}, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("limited cancelled call: %v", err)
	}
}

func TestReadBodyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, WithRetryPolicy(fastRetry))
	if _, err := c.Do(context.Background(), &Request{Method: "GET", Path: "/x"}, nil); err == nil || !strings.Contains(err.Error(), "EOF") {
		t.Errorf("err = %v", err)
	}
}

func TestWaiters(t *testing.T) {
	var statusCalls, taskCalls, queueCalls atomic.Int32
	fail := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/system/status":
			if statusCalls.Add(1) < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"status":"UP"}`)
		case "/api/ce/task":
			if taskCalls.Add(1) < 3 {
				_, _ = io.WriteString(w, `{"task":{"status":"IN_PROGRESS"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"task":{"status":"SUCCESS"}}`)
		case "/api/analysis_reports/is_queue_empty":
			if queueCalls.Add(1) < 2 {
				_, _ = io.WriteString(w, "false")
				return
			}
			_, _ = io.WriteString(w, "true")
		}
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, WithRetryPolicy(RetryPolicy{}))
	ctx := context.Background()
	p := PollPolicy{Initial: 2 * time.Millisecond, Max: 4 * time.Millisecond}
	if err := c.WaitForStatus(ctx, "UP", p); err != nil {
		t.Fatal(err)
	}
	task, err := c.WaitForTask(ctx, "t1", p)
	if err != nil || task.Status != "SUCCESS" {
		t.Fatalf("task = %+v, %v", task, err)
	}
	if err := c.WaitForEmptyQueue(ctx, p); err != nil {
		t.Fatal(err)
	}

	// API errors abort task and queue waits.
	fail.Store(true)
	if _, err := c.WaitForTask(ctx, "t1", p); !IsForbidden(err) {
		t.Errorf("task err = %v", err)
	}
	if err := c.WaitForEmptyQueue(ctx, p); !IsForbidden(err) {
		t.Errorf("queue err = %v", err)
	}
	// Status waits treat errors as transient and stop at the deadline.
	tctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := c.WaitForStatus(tctx, "UP", p); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("status err = %v", err)
	}
}

func TestPollCancelledWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := PollPolicy{Initial: time.Hour, Max: time.Hour}.poll(ctx, func() (bool, error) { return false, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

func TestIDDecoding(t *testing.T) {
	var v struct{ A, B, C ID }
	if err := json.Unmarshal([]byte(`{"A":"uuid-1","B":42,"C":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.String() != "uuid-1" || v.B != "42" || v.C != "" {
		t.Errorf("decoded %+v", v)
	}
	for _, bad := range []string{`{"A":"x}`, `{"A":true}`} {
		if json.Unmarshal([]byte(bad), &v) == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	var id ID
	if id.UnmarshalJSON([]byte(`"unterminated`)) == nil || id.UnmarshalJSON([]byte(`tru`)) == nil {
		t.Error("invalid JSON accepted")
	}
}
