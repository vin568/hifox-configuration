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
	Text      string     `json:"text"`
	CreatedAt time.Time  `json:"created_at"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"last_error,omitempty"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
}

// DefaultMaxAttempts 是 Drain 的默认重试上限：达到即死信（不再投递）。
// superctl serve/status 与 ADR/README 的「8 次」都应引用这一处。
const DefaultMaxAttempts = 8

type Outbox struct {
	path string
	mu   sync.Mutex // Append/Drain/Pending 都是 load→save 读改写；serve 里注入协程与投递协程并发调用
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
func (o *Outbox) Append(chatID, text string) (Entry, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := o.load()
	if err != nil {
		return Entry{}, err
	}
	e := Entry{ID: newID(), ChatID: chatID, Text: text, CreatedAt: time.Now().UTC()}
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

// Drain 逐条投递：成功标 sent，失败计次；达到 maxAttempts 留在文件里成死信。
func (o *Outbox) Drain(ctx context.Context, send func(context.Context, string, string) error, maxAttempts int) (sent, dead int, err error) {
	// 整段持锁（含 send）：Append/Drain 都是 load→save 读改写，与注入协程
	// 并发时不持锁会丢条目；单管理员规模下 send 时延可接受。
	o.mu.Lock()
	defer o.mu.Unlock()
	es, err := o.load()
	if err != nil {
		return 0, 0, err
	}
	changed := false
	for i := range es {
		e := &es[i]
		if e.SentAt != nil || e.Attempts >= maxAttempts {
			continue
		}
		if serr := send(ctx, e.ChatID, e.Text); serr != nil {
			e.Attempts++
			e.LastError = serr.Error()
			if e.Attempts >= maxAttempts {
				dead++
			}
			changed = true
			continue
		}
		now := time.Now().UTC()
		e.SentAt = &now
		e.LastError = ""
		sent++
		changed = true
	}
	if changed {
		if err := o.save(es); err != nil {
			return sent, dead, err
		}
	}
	return sent, dead, nil
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
