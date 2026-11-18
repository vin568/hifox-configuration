package outbox

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func open(t *testing.T) *Outbox {
	t.Helper()
	o, err := Open(filepath.Join(t.TempDir(), "out", "outbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func okSend(context.Context, string, string) error { return nil }

type failSend struct{ n int }

func (f *failSend) send(context.Context, string, string) error {
	f.n++
	return errors.New("feishu down")
}

func TestAppendDrainSent(t *testing.T) {
	o := open(t)
	if _, err := o.Append("oc_1", "收到，我来安排"); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Append("oc_1", "结论：验收通过"); err != nil {
		t.Fatal(err)
	}
	sent, dead, err := o.Drain(context.Background(), okSend, 8)
	if err != nil || sent != 2 || dead != 0 {
		t.Fatalf("sent=%d dead=%d err=%v", sent, dead, err)
	}
	if o.Pending(8) != 0 {
		t.Fatal("pending should be 0 after successful drain")
	}
	// 再 drain：已投递的不重发。
	sent, _, err = o.Drain(context.Background(), okSend, 8)
	if err != nil || sent != 0 {
		t.Fatalf("re-drain sent=%d err=%v", sent, err)
	}
}

func TestDrainRetryThenDead(t *testing.T) {
	o := open(t)
	if _, err := o.Append("oc_2", "重试我"); err != nil {
		t.Fatal(err)
	}
	f := &failSend{}
	var crossingDead int
	for range 8 {
		_, dead, err := o.Drain(context.Background(), f.send, 8)
		if err != nil {
			t.Fatal(err)
		}
		crossingDead += dead
	}
	// 死信计数恰在第 8 次从 0 变 1（跨界那一次 Drain 就要报 dead）。
	if crossingDead != 1 {
		t.Fatalf("dead count across threshold = %d, want 1", crossingDead)
	}
	if f.n != 8 {
		t.Fatalf("send attempts=%d, want 8", f.n)
	}
	// 死信后不再尝试。
	sent, dead, err := o.Drain(context.Background(), f.send, 8)
	if err != nil || sent != 0 || dead != 0 || f.n != 8 {
		t.Fatalf("dead-letter drain: sent=%d dead=%d attempts=%d", sent, dead, f.n)
	}
	es, _ := o.load()
	if len(es) != 1 || es[0].Attempts != 8 || es[0].SentAt != nil {
		t.Fatalf("entry after death: %+v", es[0])
	}
	if o.Pending(8) != 0 {
		t.Fatalf("pending=%d after dead-letter, want 0", o.Pending(8))
	}
	// 死信条目不算 pending；再补一条只试过 7 次的活条目仍要计数。
	if _, err := o.Append("oc_2", "还活着"); err != nil {
		t.Fatal(err)
	}
	for range 7 {
		if _, _, err := o.Drain(context.Background(), f.send, 8); err != nil {
			t.Fatal(err)
		}
	}
	if got := o.Pending(8); got != 1 {
		t.Fatalf("pending=%d, want 1 (dead entry excluded, alive entry counted)", got)
	}
}

// 混合批次：一条失败不得阻断后续条目投递（逐条独立计次）。
func TestDrainMixedBatch(t *testing.T) {
	o := open(t)
	// 失败条目放前面：若 Drain 在首错处 break，后面的 oc_ok 就不会被投递。
	if _, err := o.Append("oc_bad", "发不出的"); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Append("oc_ok", "能发的"); err != nil {
		t.Fatal(err)
	}
	fail := &failSend{}
	fail.n = 0
	sent, dead, err := o.Drain(context.Background(), func(ctx context.Context, chatID, text string) error {
		if chatID == "oc_bad" {
			fail.n++
			return errors.New("feishu down")
		}
		return nil
	}, 8)
	if err != nil || sent != 1 || dead != 0 {
		t.Fatalf("mixed drain: sent=%d dead=%d err=%v", sent, dead, err)
	}
	es, _ := o.load()
	if len(es) != 2 {
		t.Fatalf("want both entries kept, got %d", len(es))
	}
	byChat := map[string]Entry{}
	for _, e := range es {
		byChat[e.ChatID] = e
	}
	if byChat["oc_ok"].SentAt == nil || byChat["oc_bad"].Attempts != 1 || byChat["oc_bad"].SentAt != nil {
		t.Fatalf("mixed batch result wrong: %+v", byChat)
	}
}

// Append 与 Drain 并发（serve 的注入协程 vs 投递协程）：读改写必须串行化，
// 否则互相覆盖丢条目（-race 下验证）。
func TestOutboxConcurrentAppendDrain(t *testing.T) {
	o := open(t)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = o.Append("oc_c", strconv.Itoa(i))
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, _ = o.Drain(context.Background(), func(context.Context, string, string) error { return nil }, 8)
	}()
	wg.Wait()
	es, _ := o.load()
	if len(es) != 50 {
		t.Fatalf("lost entries under concurrency: %d/50", len(es))
	}
}
func TestPendingCountsOnlyAlive(t *testing.T) {
	o := open(t)
	o.Append("oc_3", "a")
	o.Append("oc_3", "b")
	if _, _, err := o.Drain(context.Background(), okSend, 8); err != nil {
		t.Fatal(err)
	}
	o.Append("oc_3", "c")
	if got := o.Pending(8); got != 1 {
		t.Fatalf("pending=%d, want 1", got)
	}
}
