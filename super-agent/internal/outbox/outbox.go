package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry 是一条待投递回帖（sharkly durable outbox 的文件版，ADR 0018/0021 决策）。
type Entry struct {
	ID        string     `json:"id"`
	ChatID    string     `json:"chat_id"`
	Text      string     `json:"text,omitempty"`
	Images    []string   `json:"images,omitempty"` // 本地绝对路径；投递时才上传
	CreatedAt time.Time  `json:"created_at"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"last_error,omitempty"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	// MessageID 是投递成功后飞书返回的消息 id：撤回的唯一依据。
	MessageID string `json:"message_id,omitempty"`
	// RecalledAt 记撤回时刻；非空 = 这条已经撤过，重复撤回不再发请求。
	RecalledAt *time.Time `json:"recalled_at,omitempty"`
}

// Recallable 是「哪条能撤」的唯一谓词：已发出、有 message_id、还没撤过。
// 未发送（pending）与旧版本投递（无 id）都不能撤。
func (e Entry) Recallable() bool {
	return e.SentAt != nil && e.MessageID != "" && e.RecalledAt == nil
}

// DefaultMaxAttempts 是 Drain 的默认重试上限：达到即死信（不再投递）。
// superctl serve/status 与 ADR/README 的「8 次」都应引用这一处。
const DefaultMaxAttempts = 8

type Outbox struct {
	path string
	mu   sync.Mutex // Append/Drain/Get/MarkRecalled 都是 load→save 读改写
	// drainMu 串行化 Drain 本身：投递（含图片上传）在数据锁之外进行，
	// 没有它会并发重复投递同一条；数据锁仍只覆盖 load/save。
	drainMu sync.Mutex
}

func Open(path string) (*Outbox, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return nil, err
		}
	}
	return &Outbox{path: path}, nil
}

func (o *Outbox) load() ([]Entry, error) {
	data, err := os.ReadFile(o.path)
	if err != nil {
		return nil, err
	}
	var es []Entry
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("outbox line %q: %w", truncate(string(line), 80), err)
		}
		es = append(es, e)
	}
	return es, nil
}

func (o *Outbox) save(es []Entry) error {
	tmp := o.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	for _, e := range es {
		b, err := json.Marshal(e)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, o.path)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%d-%s", time.Now().UnixMilli(), hex.EncodeToString(b))
}

// Append 追加一条待发回帖（主控经 `superctl reply` 调用）。
// text 与 images 可并存；纯文字时 images 传 nil。
func (o *Outbox) Append(chatID, text string, images []string) (Entry, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := o.load()
	if err != nil {
		return Entry{}, err
	}
	e := Entry{ID: newID(), ChatID: chatID, Text: text, Images: images, CreatedAt: time.Now().UTC()}
	es = append(es, e)
	return e, o.save(es)
}

// Pending 返回未投递且未死信的条数（status 命令用）。
func (o *Outbox) Pending(maxAttempts int) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := o.load()
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range es {
		if e.SentAt == nil && e.Attempts < maxAttempts {
			n++
		}
	}
	return n
}

// Drain 逐条投递：成功后写 sent_at 与 message_id，失败计次；达到 maxAttempts 留成死信。
//
// 投递在数据锁之外进行（图片上传最多几十秒，不能挡住 Append），因此分两段：
// 先快照待投条目并逐条投递，再回到锁内按 ID 合并结果。Append 在这期间写入的
// 新条目不会被覆盖；drainMu 保证同一时刻只有一个 Drain 在跑。
func (o *Outbox) Drain(ctx context.Context, deliver func(context.Context, Entry) (string, error), maxAttempts int) (sent, dead int, err error) {
	o.drainMu.Lock()
	defer o.drainMu.Unlock()

	o.mu.Lock()
	snapshot, err := o.load()
	o.mu.Unlock()
	if err != nil {
		return 0, 0, err
	}

	type outcome struct {
		id        string
		messageID string
		err       error
	}
	var outcomes []outcome
	for _, e := range snapshot {
		if e.SentAt != nil || e.Attempts >= maxAttempts {
			continue
		}
		id, derr := deliver(ctx, e)
		outcomes = append(outcomes, outcome{id: e.ID, messageID: id, err: derr})
	}
	if len(outcomes) == 0 {
		return 0, 0, nil
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := o.load()
	if err != nil {
		return 0, 0, err
	}
	index := make(map[string]int, len(es))
	for i := range es {
		index[es[i].ID] = i
	}
	changed := false
	for _, out := range outcomes {
		i, ok := index[out.id]
		if !ok {
			continue // 同一 Drain 期间被外部删除：不复活
		}
		e := &es[i]
		if e.SentAt != nil {
			continue
		}
		changed = true
		if out.err != nil {
			e.Attempts++
			e.LastError = out.err.Error()
			if e.Attempts >= maxAttempts {
				dead++
			}
			continue
		}
		now := time.Now().UTC()
		e.SentAt = &now
		// 缺 message_id 也照记发送成功：消息已经出去了，重发更糟；撤回时再按 id
		// 为空给出明确提示（这是「宁可丢撤回能力也不重复打扰用户」的取舍）。
		e.MessageID = out.messageID
		e.LastError = ""
		sent++
	}
	if !changed {
		return sent, dead, nil
	}
	return sent, dead, o.save(es)
}

// Get 按 entry id 取一条；不存在返回包装 os.ErrNotExist 的可读错误。
func (o *Outbox) Get(id string) (Entry, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := o.load()
	if err != nil {
		return Entry{}, err
	}
	for _, e := range es {
		if e.ID == id {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("outbox 里没有 id=%s 的回帖: %w", id, os.ErrNotExist)
}

// LastRecallable 取最新一条可撤回记录；chatID 非空时限定会话。
func (o *Outbox) LastRecallable(chatID string) (Entry, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := o.load()
	if err != nil {
		return Entry{}, false, err
	}
	var best Entry
	found := false
	for _, e := range es {
		if !e.Recallable() || (chatID != "" && e.ChatID != chatID) {
			continue
		}
		if !found || e.CreatedAt.After(best.CreatedAt) {
			best, found = e, true
		}
	}
	return best, found, nil
}

// MarkRecalled 记撤回时刻。重复标记幂等（不覆盖首次时间）；
// entry 未发送或无 message_id 时返回可读错误。
func (o *Outbox) MarkRecalled(id string, at time.Time) (Entry, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := o.load()
	if err != nil {
		return Entry{}, err
	}
	for i := range es {
		if es[i].ID != id {
			continue
		}
		if es[i].RecalledAt != nil {
			return es[i], nil
		}
		if !es[i].Recallable() {
			return Entry{}, fmt.Errorf("回帖 id=%s 还不能撤回：未发送或没有记录飞书 message_id", id)
		}
		t := at.UTC()
		es[i].RecalledAt = &t
		return es[i], o.save(es)
	}
	return Entry{}, fmt.Errorf("outbox 里没有 id=%s 的回帖: %w", id, os.ErrNotExist)
}

// MarkRecalledByMessageID 按飞书 message_id 回填撤回时刻（`recall --message` 尽力而为）。
// 找不到对应记录不报错：那条消息可能不是本 outbox 发的。
func (o *Outbox) MarkRecalledByMessageID(messageID string, at time.Time) (Entry, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := o.load()
	if err != nil {
		return Entry{}, false, err
	}
	for i := range es {
		if es[i].MessageID != messageID {
			continue
		}
		if es[i].RecalledAt != nil {
			return es[i], true, nil
		}
		t := at.UTC()
		es[i].RecalledAt = &t
		return es[i], true, o.save(es)
	}
	return Entry{}, false, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
