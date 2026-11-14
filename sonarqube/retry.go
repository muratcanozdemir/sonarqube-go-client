package sonarqube

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RetryPolicy controls how failed requests are retried.
//
// A request is retried when:
//   - the server answered 429 Too Many Requests (any method: the request was
//     rejected, not processed), or
//   - the server answered 502, 503 or 504, or the connection failed, and the
//     method is idempotent (GET, HEAD, PUT, DELETE, OPTIONS).
//
// Non-idempotent requests (POST, PATCH) are never replayed after a possible
// partial processing. Waits grow exponentially with full jitter, are capped by
// MaxBackoff, and a Retry-After header from the server takes precedence.
type RetryPolicy struct {
	// MaxRetries is the number of retries after the first attempt. 0 disables retries.
	MaxRetries int
	// MinBackoff is the base wait before the first retry.
	MinBackoff time.Duration
	// MaxBackoff caps any single wait, including Retry-After.
	MaxBackoff time.Duration
}

// DefaultRetryPolicy is used unless [WithRetryPolicy] overrides it.
var DefaultRetryPolicy = RetryPolicy{MaxRetries: 3, MinBackoff: 500 * time.Millisecond, MaxBackoff: 30 * time.Second}

// WithRetryPolicy sets the retry policy. Use RetryPolicy{} to disable retries.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(c *Client) error {
		if p.MaxRetries < 0 || p.MinBackoff < 0 || p.MaxBackoff < p.MinBackoff {
			return errors.New("sonarqube: invalid retry policy")
		}
		c.retry = p
		return nil
	}
}

// WithRateLimit limits the client to rps requests per second with bursts of
// up to burst requests. rps <= 0 disables client-side limiting. The default
// is 10 requests per second with a burst of 10.
func WithRateLimit(rps float64, burst int) Option {
	return func(c *Client) error {
		if rps <= 0 {
			c.limiter = nil
			return nil
		}
		if burst < 1 {
			return errors.New("sonarqube: rate limit burst must be at least 1")
		}
		c.limiter = newLimiter(rps, burst)
		return nil
	}
}

func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// shouldRetry decides whether an attempt is retried.
func shouldRetry(method string, resp *http.Response, err error) bool {
	if err != nil {
		return idempotent(method) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return true
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return idempotent(method)
	}
	return false
}

// backoff returns the wait before retry number attempt (0-based).
func (p RetryPolicy) backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if d, ok := retryAfter(resp.Header.Get("Retry-After")); ok {
			return min(d, p.MaxBackoff)
		}
	}
	ceil := p.MinBackoff << min(attempt, 20)
	if ceil <= 0 || ceil > p.MaxBackoff {
		ceil = p.MaxBackoff
	}
	if ceil <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(ceil) + 1)) //nolint:gosec // jitter, not a security value
}

// retryAfter parses a Retry-After header (delay-seconds or HTTP-date).
func retryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0), true
	}
	return 0, false
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// limiter is a token bucket shared by all requests of a client.
type limiter struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newLimiter(rps float64, burst int) *limiter {
	return &limiter{rate: rps, burst: float64(burst), tokens: float64(burst), now: time.Now}
}

// wait blocks until a token is available or ctx is done.
func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := l.now()
	if !l.last.IsZero() {
		l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	}
	l.last = now
	l.tokens-- // reserve; may go negative, which queues callers fairly
	var d time.Duration
	if l.tokens < 0 {
		d = time.Duration(-l.tokens / l.rate * float64(time.Second))
	}
	l.mu.Unlock()
	if err := sleep(ctx, d); err != nil {
		l.mu.Lock()
		l.tokens++ // give the reservation back
		l.mu.Unlock()
		return err
	}
	return nil
}
