//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	sq "github.com/muratcanozdemir/sonarqube-go-client/sonarqube"
)

// TestZZRestart runs last (test order follows file names): it restarts the
// server and waits for it to come back.
func TestZZRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("restart skipped in -short mode")
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	// A restart waits for running background tasks; let the queue drain.
	if err := c.WaitForEmptyQueue(wctx, sq.DefaultPollPolicy); err != nil {
		t.Fatal(err)
	}
	// Idle keep-alive connections can hold the web server's graceful stop.
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	done(c.System.Restart(ctx)).must(t)
	if err := c.WaitForStatus(wctx, "UP", sq.DefaultPollPolicy); err != nil {
		t.Fatalf("server did not come back after restart: %v", err)
	}
}
