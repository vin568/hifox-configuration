package feishu

import (
	"context"
	"errors"
	"testing"
	"time"

	"super-agent/internal/inject"
)

var errTransient = errors.New("transient")

// fastBackoff 让重试用例不真睡（backoff 数学另有 TestBackoffExponentialCapped 钉）。
func fastBackoff(int) time.Duration { return 0 }

// TestRetryQueueRetriesThenSucceeds 钉住两件事：瞬时失败经退避重试最终成功、
// 且成功那一次会调 done 回调（dedup markDone 的唯一焊点——删掉回调调用，
// 走过重试的消息永不 markDone，60s 过期后飞书重投即重复注入）。
func TestRetryQueueRetriesThenSucceeds(t *testing.T) {
	failures := 2
	success := make(chan struct{}, 1)
	q := newRetryQueue(func(_ context.Context, _ Source, p inject.Payload) error {
		if failures > 0 {
			failures--
			return errTransient
		}
		success <- struct{}{}
		return nil
	})
	q.tasks = make(chan injectTask, 8)
	q.backoff = fastBackoff
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.run(ctx)

	task := injectTask{src: SourceFeishu, p: inject.Payload{ChatID: "c", Text: "payload"}, attempts: 1}
	if err := q.injectNow(context.Background(), task); err == nil {
		t.Fatal("first attempt should fail")
	}
	q.enqueue(task)

	select {
	case <-success:
	case <-time.After(5 * time.Second):
		t.Fatal("retry never succeeded")
	}

	// done 回调在重试成功后被调用。
	doneCalled := make(chan struct{}, 1)
	failures = 0
	q.enqueue(injectTask{
		src:      SourceFeishu,
		p:        inject.Payload{ChatID: "c2", Text: "payload2"},
		attempts: 1,
		done:     func() { doneCalled <- struct{}{} },
	})
	select {
	case <-doneCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("done callback never fired after successful retry")
	}
	cancel()
}

// TestRetryQueueBlockedRetriedUntilBudgetThenDropped：主控 blocked 与瞬时
// 失败同梯排队重试（ADR 0018「注入器侧排队重试」——用户解锁后预算内的
// 消息还能送达）；预算耗尽终局丢弃，且 done 必须被调（闭合 claim，否则
// 过期重投会重复注入一条已被放弃的消息）。
func TestRetryQueueBlockedRetriedUntilBudgetThenDropped(t *testing.T) {
	calls := make(chan struct{}, retryMaxAttempts)
	done := make(chan struct{}, 1)
	q := newRetryQueue(func(_ context.Context, _ Source, p inject.Payload) error {
		calls <- struct{}{}
		return inject.ErrBlocked
	})
	q.tasks = make(chan injectTask, 8)
	q.backoff = fastBackoff
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.run(ctx)

	q.enqueue(injectTask{
		src:      SourceFeishu,
		p:        inject.Payload{ChatID: "c", Text: "x"},
		attempts: 1,
		done:     func() { done <- struct{}{} },
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("budget drop must close the claim (done)")
	}
	// 队列路径共 retryMaxAttempts-1 次调用（第 1 次是 handle 的立即注入）。
	if got := len(calls); got != retryMaxAttempts-1 {
		t.Fatalf("inject calls = %d, want %d", got, retryMaxAttempts-1)
	}
	select {
	case got := <-q.tasks:
		t.Fatalf("task re-queued after budget: %+v", got)
	default:
	}
}

func TestBackoffExponentialCapped(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second},
		{1, 2 * time.Second},
		{3, 8 * time.Second},
		{30, retryCap},
	}
	for _, tt := range tests {
		if got := backoff(tt.attempt); got != tt.want {
			t.Fatalf("backoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}
