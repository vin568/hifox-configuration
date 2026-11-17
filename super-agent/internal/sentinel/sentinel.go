package sentinel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/hifox"
)

// WatchEntry 是云端跟踪清单（watchlist.json）的一项。清单由主控维护：
// 派云端活时登记，收口时移除；哨兵只读。
type WatchEntry struct {
	TaskID string `json:"task_id"`
	Note   string `json:"note,omitempty"`
}

// Change 是一次探测到的状态变化。
type Change struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	From   string `json:"from"`
	To     string `json:"to"`
}

type Result struct {
	Watched int      `json:"watched"`
	Changed []Change `json:"changed,omitempty"`
	// Injected 仅在「确有变化且注入成功」时为 true；注入失败时 false 且带 err，
	// 快照不推进，下一轮重见同一变化并重试（评审 P1：变化不可静默丢失）。
	Injected bool `json:"injected"`
}

// Poll 读跟踪清单，逐个 `hifox task view` 对照快照：首见只记基线（防启动噪声），
// 之后签名变化才汇总成一条事件注入主控，**注入成功才落盘新快照**。
// 快照只保留清单在册任务（移出的随下一轮清掉，避免同任务重派撞旧签名）。
// watchlist/快照损坏时不让哨兵永久死掉：把坏文件改名 `.corrupt-<ts>` 另存、按空处理、告警。
func Poll(ctx context.Context, client *hifox.Client, watchPath, snapPath string, inject func(text string) error) (*Result, error) {
	res := &Result{}
	entries, err := readWatchlist(watchPath)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return res, nil
	}
	snap, err := loadSnapshot(snapPath)
	if err != nil {
		return nil, err
	}

	next := make(map[string]string, len(entries)) // 只保留在册任务
	for _, e := range entries {
		task, err := client.TaskView(ctx, e.TaskID)
		if err != nil {
			// 单个任务查询失败不拖垮整轮：跳过，下一轮再看。
			if prev, seen := snap[e.TaskID]; seen {
				next[e.TaskID] = prev
			}
			continue
		}
		sig := task.Signature()
		res.Watched++
		if prev, seen := snap[e.TaskID]; seen && prev != sig {
			res.Changed = append(res.Changed, Change{TaskID: e.TaskID, Title: task.Title, From: prev, To: sig})
		}
		next[e.TaskID] = sig
	}

	if len(res.Changed) > 0 {
		if err := inject(ChangesPrompt(res.Changed)); err != nil {
			// 不推进快照：下一轮重见同一变化并重试。
			return res, err
		}
		res.Injected = true
	}
	if err := saveSnapshot(snapPath, next); err != nil {
		return nil, err
	}
	return res, nil
}

// ChangesPrompt 拼注入主控的云端状态变化事件文本。
func ChangesPrompt(changes []Change) string {
	var b strings.Builder
	b.WriteString("[触发器: event] 云端任务状态变化（状态类别/名 | 委派 | agent工作）：\n")
	for _, c := range changes {
		fmt.Fprintf(&b, "- %s「%s」：%s → %s\n", c.TaskID, c.Title, c.From, c.To)
	}
	b.WriteString("对照作业台账处置：该收口收口、该转告用户转告；无事不回复用户。")
	return b.String()
}

func readWatchlist(path string) ([]WatchEntry, error) {
	data, err := readFileOrHeal(path)
	if err != nil || len(data) == 0 {
		return nil, err
	}
	var entries []WatchEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		healCorrupt(path, data)
		return nil, nil
	}
	return entries, nil
}

func loadSnapshot(path string) (map[string]string, error) {
	data, err := readFileOrHeal(path)
	if err != nil || len(data) == 0 {
		return map[string]string{}, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(data, &m); err != nil {
		healCorrupt(path, data)
	}
	return m, nil
}

// readFileOrHeal 读文件；损坏（非 JSON）由调用方 heal 后按空处理。
func readFileOrHeal(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

func healCorrupt(path string, original []byte) {
	aside := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
	if werr := os.WriteFile(aside, original, 0o644); werr == nil {
		_ = os.Remove(path)
		slog.Warn("sentinel: 状态文件损坏，已另存并按空重置", "file", path, "saved_as", aside)
	} else {
		slog.Error("sentinel: 状态文件损坏且另存失败，跳过本轮", "file", path, "error", werr)
	}
}

func saveSnapshot(path string, m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
