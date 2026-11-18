package feishu

import (
	"context"
	"log/slog"
	"time"

	"super-agent/internal/inject"
)

// retryMaxAttempts is the total attempt budget per message (1 immediate + 7
// queued retries) before the message is logged and dropped.
const retryMaxAttempts = 8

// retryBase is the exponential backoff base; capped at retryCap.
const (
	retryBase = time.Second
	retryCap  = 5 * time.Minute
)

// injectTask is one injection attempt (immediate or queued retry).
type injectTask struct {
	src      Source
	p        inject.Payload
	attempts int
	done     func() // called when the message reaches a terminal state: injected, or dropped after the budget
}

// retryQueue re-delivers failed injections to the InjectFunc without blocking
// the long-connection receive loop. One goroutine, unbounded channel: the only
// producer is the live message path and inflight failures are bounded by
// attempt count, so a bounded queue would add a drop mode for no benefit.
type retryQueue struct {
	inject  InjectFunc
	tasks   chan injectTask
	backoff func(attempt int) time.Duration // injectable so tests run without real delays
}

func newRetryQueue(inject InjectFunc) *retryQueue {
	return &retryQueue{inject: inject, tasks: make(chan injectTask), backoff: backoff}
}

// injectNow performs one immediate attempt. The caller handles failure.
func (q *retryQueue) injectNow(ctx context.Context, t injectTask) error {
	return q.inject(ctx, t.src, t.p)
}

// run drains the queue until ctx is cancelled. Any failure — transient errors
// and a blocked master alike — is re-queued with exponential backoff until
// retryMaxAttempts (ADR 0018「注入器侧排队重试」: a blocked master is unblocked
// by the user, and the queued message is then delivered within the budget).
// When the budget is exhausted the task is logged, dropped, and marked done:
// the claim closes so an expired redelivery cannot re-inject it.
func (q *retryQueue) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-q.tasks:
			timer := time.NewTimer(q.backoff(t.attempts))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if err := q.inject(ctx, t.src, t.p); err != nil {
				t.attempts++
				if t.attempts >= retryMaxAttempts {
					slog.Error("feishu: injection failed past retry budget, dropping",
						"chat_id", t.p.ChatID, "attempts", t.attempts, "error", err)
					if t.done != nil {
						t.done()
					}
					continue
				}
				q.enqueue(t)
				continue
			}
			if t.done != nil {
				t.done()
			}
		}
	}
}

// enqueue adds a task without blocking the caller.
func (q *retryQueue) enqueue(t injectTask) {
	select {
	case q.tasks <- t:
	default:
		go func() { q.tasks <- t }()
	}
}

// backoff returns base * 2^attempt capped at cap.
func backoff(attempt int) time.Duration {
	d := retryBase
	for i := 0; i < attempt && d < retryCap; i++ {
		d *= 2
	}
	if d > retryCap {
		d = retryCap
	}
	return d
}
