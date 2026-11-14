package sonarqube

import (
	"context"
	"math/rand/v2"
	"strings"
	"time"
)

// PollPolicy controls the wait helpers. Intervals start at Initial, double
// after every check (with jitter) and are capped at Max.
type PollPolicy struct {
	Initial time.Duration
	Max     time.Duration
}

// DefaultPollPolicy starts at 1s and backs off to at most 15s between checks.
var DefaultPollPolicy = PollPolicy{Initial: time.Second, Max: 15 * time.Second}

// poll calls check until it reports done, returns an error, or ctx ends.
func (p PollPolicy) poll(ctx context.Context, check func() (bool, error)) error {
	interval := p.Initial
	for {
		done, err := check()
		if err != nil || done {
			return err
		}
		// Jitter in [interval/2, interval] spreads concurrent pollers.
		wait := interval/2 + time.Duration(rand.Int64N(int64(interval/2)+1)) //nolint:gosec // jitter, not a security value
		if err := sleep(ctx, wait); err != nil {
			return err
		}
		interval = min(interval*2, p.Max)
	}
}

// WaitForStatus polls api/system/status until the server reports want
// (e.g. "UP"). Connection errors and other statuses are treated as transient,
// which makes it suitable for waiting out a restart. Bound it with ctx.
func (c *Client) WaitForStatus(ctx context.Context, want string, p PollPolicy) error {
	return p.poll(ctx, func() (bool, error) {
		st, _, err := c.System.Status(ctx)
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return err == nil && st.Status == want, nil
	})
}

// WaitForTask polls api/ce/task until the background task reaches a final
// state (SUCCESS, FAILED or CANCELED) and returns it. API errors abort.
func (c *Client) WaitForTask(ctx context.Context, taskID string, p PollPolicy) (*CeTaskResponseTask, error) {
	var task *CeTaskResponseTask
	err := p.poll(ctx, func() (bool, error) {
		res, _, err := c.Ce.Task(ctx, &CeTaskOptions{ID: taskID})
		if err != nil {
			return false, err
		}
		task = res.Task
		switch task.Status {
		case "SUCCESS", "FAILED", "CANCELED":
			return true, nil
		}
		return false, nil
	})
	return task, err
}

// WaitForEmptyQueue polls api/analysis_reports/is_queue_empty until no
// background task is pending or running. API errors abort.
func (c *Client) WaitForEmptyQueue(ctx context.Context, p PollPolicy) error {
	return p.poll(ctx, func() (bool, error) {
		b, _, err := c.AnalysisReports.QueueStatus(ctx)
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(string(b)) == "true", nil
	})
}
