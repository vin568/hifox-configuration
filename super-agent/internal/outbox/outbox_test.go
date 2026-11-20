package outbox

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T) *Outbox {
	t.Helper()
	o, err := Open(filepath.Join(t.TempDir(), "out", "outbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func okSend(context.Context, Entry) (string, error) { return "", nil }

type failSend struct{ n int }

func (f *failSend) send(context.Context, Entry) (string, error) {
	f.n++
	return "", errors.New("feishu down")
}

func TestAppendDrainSent(t *testing.T) {
	o := open(t)
	if _, err := o.Append("oc_1", "收到，我来安排", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Append("oc_1", "结论：验收通过", nil); err != nil {
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
	if _, err := o.Append("oc_2", "重试我", nil); err != nil {
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
	if _, err := o.Append("oc_2", "还活着", nil); err != nil {
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
	if _, err := o.Append("oc_bad", "发不出的", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Append("oc_ok", "能发的", nil); err != nil {
		t.Fatal(err)
	}
	fail := &failSend{}
	fail.n = 0
	sent, dead, err := o.Drain(context.Background(), func(ctx context.Context, e Entry) (string, error) {
		if e.ChatID == "oc_bad" {
			fail.n++
			return "", errors.New("feishu down")
		}
		return "", nil
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
			_, _ = o.Append("oc_c", strconv.Itoa(i), nil)
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, _ = o.Drain(context.Background(), func(context.Context, Entry) (string, error) { return "", nil }, 8)
	}()
	wg.Wait()
	es, _ := o.load()
	if len(es) != 50 {
		t.Fatalf("lost entries under concurrency: %d/50", len(es))
	}
}
func TestPendingCountsOnlyAlive(t *testing.T) {
	o := open(t)
	o.Append("oc_3", "a", nil)
	o.Append("oc_3", "b", nil)
	if _, _, err := o.Drain(context.Background(), okSend, 8); err != nil {
		t.Fatal(err)
	}
	o.Append("oc_3", "c", nil)
	if got := o.Pending(8); got != 1 {
		t.Fatalf("pending=%d, want 1", got)
	}
}

// 投递成功后必须回填 message_id；重开文件仍在。
func TestOutboxDrainStoresMessageID(t *testing.T) {
	o := open(t)
	if _, err := o.Append("oc_1", "hi", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := o.Drain(context.Background(), func(context.Context, Entry) (string, error) {
		return "om_x", nil
	}, 8); err != nil {
		t.Fatal(err)
	}
	es, _ := o.load()
	if len(es) != 1 || es[0].MessageID != "om_x" || es[0].SentAt == nil {
		t.Fatalf("entry = %+v", es[0])
	}
	if !es[0].Recallable() {
		t.Fatal("sent entry with id must be recallable")
	}
}

func TestOutboxRecallable(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		e    Entry
		want bool
	}{
		{"未发送", Entry{}, false},
		{"已发送无 id", Entry{SentAt: &now}, false},
		{"已发送有 id", Entry{SentAt: &now, MessageID: "om_x"}, true},
		{"已撤回", Entry{SentAt: &now, MessageID: "om_x", RecalledAt: &now}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.e.Recallable(); got != c.want {
				t.Fatalf("Recallable = %v, want %v", got, c.want)
			}
		})
	}
}

func TestOutboxMarkRecalled(t *testing.T) {
	o := open(t)
	e, _ := o.Append("oc_1", "hi", nil)
	// 未发送：报错。
	if _, err := o.MarkRecalled(e.ID, time.Now()); err == nil {
		t.Fatal("pending entry must not be markable")
	}
	_, _, _ = o.Drain(context.Background(), func(context.Context, Entry) (string, error) { return "om_x", nil }, 8)

	at := time.Now().UTC()
	got, err := o.MarkRecalled(e.ID, at)
	if err != nil || got.RecalledAt == nil {
		t.Fatalf("mark = %+v, %v", got, err)
	}
	first := *got.RecalledAt
	// 重复标记幂等：不覆盖首次时间。
	got2, err := o.MarkRecalled(e.ID, at.Add(time.Hour))
	if err != nil || !got2.RecalledAt.Equal(first) {
		t.Fatalf("idempotent = %+v, %v", got2, err)
	}
	// 落盘。
	es, _ := o.load()
	if es[0].RecalledAt == nil || !es[0].RecalledAt.Equal(first) {
		t.Fatalf("not persisted: %+v", es[0])
	}
}

func TestOutboxLastRecallable(t *testing.T) {
	o := open(t)
	// 已撤回与其它会话的记录都要跳过；--chat 要过滤。
	e1, _ := o.Append("oc_a", "first", nil)
	e2, _ := o.Append("oc_b", "second", nil)
	_, _, _ = o.Drain(context.Background(), func(_ context.Context, e Entry) (string, error) {
		return "om_" + e.ID, nil
	}, 8)
	if _, err := o.MarkRecalled(e1.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := o.LastRecallable("")
	if err != nil || !ok || got.ID != e2.ID {
		t.Fatalf("last = %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, _ := o.LastRecallable("oc_a"); ok {
		t.Fatal("oc_a 已无可撤回记录")
	}
	// 按 message_id 回填。
	if e, ok, err := o.MarkRecalledByMessageID("om_"+e2.ID, time.Now()); err != nil || !ok || e.ID != e2.ID {
		t.Fatalf("mark by message = %+v ok=%v err=%v", e, ok, err)
	}
	if _, ok, _ := o.LastRecallable(""); ok {
		t.Fatal("全部撤回后不应还有可撤回记录")
	}
}

// 图片路径必须原样往返（不在 outbox 里复制或改写）。
func TestOutboxImagesRoundTrip(t *testing.T) {
	o := open(t)
	abs := "/tmp/a.png"
	if _, err := o.Append("oc_1", "", []string{abs}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(o.path)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if _, _, err := reopened.Drain(context.Background(), func(_ context.Context, e Entry) (string, error) {
		got = append(got, e.Images...)
		return "om_x", nil
	}, 8); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != abs {
		t.Fatalf("images = %v", got)
	}
}

// 失败必须写 LastError（运维/status 定位用），且不因失败丢条目。
func TestOutboxDrainStoresLastError(t *testing.T) {
	o := open(t)
	if _, err := o.Append("oc_1", "会失败", nil); err != nil {
		t.Fatal(err)
	}
	f := &failSend{}
	if _, _, err := o.Drain(context.Background(), f.send, 8); err != nil {
		t.Fatal(err)
	}
	es, _ := o.load()
	if len(es) != 1 || es[0].LastError == "" || es[0].Attempts != 1 || es[0].SentAt != nil {
		t.Fatalf("entry = %+v", es[0])
	}
}
