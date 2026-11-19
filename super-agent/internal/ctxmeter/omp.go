package ctxmeter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// OMPProbe 从 omp 的 session JSONL 尾部（ADR 0030）读最近一条 assistant 消息：
//   - contextSnapshot.promptTokens 是 provider 回报的完整 prompt（含 cache）
//   - contextSnapshot.compactionEpoch 标记 omp 是否已经自行压缩
//   - 没有 contextSnapshot 时退回 usage.input + cacheRead + cacheWrite
//
// 只读尾部，绝不整文件扫描；文件很大时按 64KiB → 8MiB 逐步扩大。
type OMPProbe struct {
	MaxTailBytes int64
}

func (p OMPProbe) Measure(ctx context.Context, t Target) (Snapshot, error) {
	if strings.TrimSpace(t.SessionPath) == "" {
		return Snapshot{Source: SourceUnknown}, errors.New("ctxmeter: omp session 路径为空")
	}
	select {
	case <-ctx.Done():
		return Snapshot{Source: SourceUnknown}, ctx.Err()
	default:
	}
	st, err := os.Stat(t.SessionPath)
	if err != nil {
		return Snapshot{Source: SourceUnknown}, fmt.Errorf("ctxmeter: stat %s: %w", t.SessionPath, err)
	}
	max := p.MaxTailBytes
	if max <= 0 {
		max = 8 << 20
	}
	for _, window := range ompTailWindows(st.Size(), max) {
		data, err := readTail(t.SessionPath, window)
		if err != nil {
			return Snapshot{Source: SourceUnknown}, err
		}
		if snap, ok := latestOMPAssistant(data, st.ModTime()); ok {
			return snap, nil
		}
	}
	return Snapshot{Source: SourceUnknown}, errors.New("ctxmeter: session 尾部没有可解析的 assistant usage")
}

func ompTailWindows(size, max int64) []int64 {
	if size <= 0 {
		return []int64{max}
	}
	candidates := []int64{64 << 10, 256 << 10, 1 << 20, 4 << 20, max}
	out := make([]int64, 0, len(candidates))
	last := int64(0)
	for _, c := range candidates {
		if c > max {
			c = max
		}
		if c < size && c > last {
			out = append(out, c)
			last = c
		}
	}
	want := size
	if max < size {
		want = max
	}
	if len(out) == 0 || out[len(out)-1] < want {
		out = append(out, want)
	}
	return out
}

// readTail 读文件最后 n 字节；如果从行中间开始，丢掉第一段残行。
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() <= 0 {
		return nil, nil
	}
	if n <= 0 || n > st.Size() {
		n = st.Size()
	}
	off := st.Size() - n
	if _, err := f.Seek(off, 0); err != nil {
		return nil, err
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, err
	}
	if off > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	return data, nil
}

type ompSessionLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role            string      `json:"role"`
		Model           string      `json:"model"`
		Provider        string      `json:"provider"`
		Usage           *ompUsage   `json:"usage"`
		ContextSnapshot *ompContext `json:"contextSnapshot"`
	} `json:"message"`
}

type ompUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

type ompContext struct {
	PromptTokens    int64 `json:"promptTokens"`
	CompactionEpoch int   `json:"compactionEpoch"`
}

func latestOMPAssistant(data []byte, mod time.Time) (Snapshot, bool) {
	lines := bytes.Split(data, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || !bytes.Contains(line, []byte(`"type":"message"`)) {
			continue
		}
		var l ompSessionLine
		if json.Unmarshal(line, &l) != nil || l.Type != "message" || l.Message.Role != "assistant" {
			continue
		}
		if l.Message.ContextSnapshot == nil && l.Message.Usage == nil {
			continue
		}
		s := Snapshot{
			Provider:   l.Message.Provider,
			Model:      l.Message.Model,
			Source:     SourceMeasured,
			MeasuredAt: mod,
		}
		if l.Message.ContextSnapshot != nil {
			s.PromptTokens = l.Message.ContextSnapshot.PromptTokens
			s.Epoch = l.Message.ContextSnapshot.CompactionEpoch
		}
		if u := l.Message.Usage; u != nil {
			if s.PromptTokens == 0 {
				s.PromptTokens = u.Input + u.CacheRead + u.CacheWrite
			}
			s.OutputTokens = u.Output
		}
		if t, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
			s.MeasuredAt = t
		}
		if s.Model == "" {
			continue
		}
		if s.Provider == "" {
			if i := strings.IndexByte(s.Model, '/'); i > 0 {
				s.Provider, s.Model = s.Model[:i], s.Model[i+1:]
			}
		}
		if s.PromptTokens <= 0 && s.OutputTokens <= 0 {
			continue
		}
		return s, true
	}
	return Snapshot{}, false
}
