// Package inflight 承载「正在办」清单：一个机器可读的 JSON 数组（ADR 0021）。
// 清单代替旧 watchlist.json：云端任务与本地 agent 作业同列一处，主控派活即登记、
// 收口即移除。哨兵/巡检读它取云端条目做状态跟踪，故写入必须原子，读必须自愈。
package inflight

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Entry 是清单一项。Kind 区分两条通道：cloud 用 TaskID 做标识，local 用 Agent。
type Entry struct {
	Kind       string    `json:"kind"` // cloud | local
	TaskID     string    `json:"task_id,omitempty"`
	Agent      string    `json:"agent,omitempty"`
	Pane       string    `json:"pane,omitempty"`
	Capability string    `json:"capability,omitempty"` // 本地：派给哪个能力（并发上限按它计）
	Note       string    `json:"note,omitempty"`
	AddedAt    time.Time `json:"added_at"`
}

// legacyEntry 是旧 watchlist.json 的形状（[{task_id, note}]），只为迁移而读。
type legacyEntry struct {
	TaskID string `json:"task_id"`
	Note   string `json:"note,omitempty"`
}

// Load 读清单。文件不存在视为空清单；损坏则把坏文件另存 `.corrupt-<ts>` 后按空处理
// （清单不能因一次坏写让主控永久起不来），因此调用方拿到的错误只来自读/改名失败。
func Load(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read inflight %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		healCorrupt(path, data)
		return nil, nil
	}
	return entries, nil
}

// LoadMigrating 是迁移期的读入口：inflight.json 不存在而旧 watchlist.json 还在时，
// 把旧形状转成 kind=cloud 的条目并落盘（旧文件保持不动，哨兵仍可读），之后与 Load 同。
// 迁移落盘失败即返回错误——静默吞掉会让清单每轮重算、主控以为登记成功。
func LoadMigrating(path, legacyPath string) ([]Entry, error) {
	if _, err := os.Stat(path); err == nil {
		return Load(path)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat inflight %s: %w", path, err)
	}

	data, err := os.ReadFile(legacyPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read legacy watchlist %s: %w", legacyPath, err)
	}
	at := time.Now()
	if fi, err := os.Stat(legacyPath); err == nil {
		at = fi.ModTime()
	}
	var legacy []legacyEntry
	if len(data) > 0 {
		if err := json.Unmarshal(data, &legacy); err != nil {
			// 旧文件损坏不在这里改名：它是哨兵的输入，留给哨兵自己的自愈逻辑。
			slog.Warn("inflight: 旧 watchlist.json 损坏，迁移按空处理", "file", legacyPath, "error", err)
			return nil, nil
		}
	}
	entries := make([]Entry, 0, len(legacy))
	for _, e := range legacy {
		if e.TaskID == "" {
			continue
		}
		entries = append(entries, Entry{Kind: "cloud", TaskID: e.TaskID, Note: e.Note, AddedAt: at})
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if err := Save(path, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// Save 覆盖写清单：缩进 JSON + 原子写（临时文件 + rename），避免半截文件。
func Save(path string, entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir inflight dir: %w", err)
	}
	if entries == nil {
		entries = []Entry{}
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode inflight: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write inflight %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename inflight %s: %w", tmp, err)
	}
	return nil
}

// Add 追加一项；同 kind 且同标识（cloud 比 TaskID、local 比 Agent）时原位覆盖，不重复。
// 同 kind 但双方都没有标识时无从去重，按追加处理。AddedAt 为零则补当前时间。
func Add(path string, e Entry) error {
	entries, err := Load(path)
	if err != nil {
		return err
	}
	if e.AddedAt.IsZero() {
		e.AddedAt = time.Now()
	}
	for i, old := range entries {
		if old.Kind != e.Kind {
			continue
		}
		if (e.TaskID != "" && old.TaskID == e.TaskID) || (e.Agent != "" && old.Agent == e.Agent) {
			entries[i] = e
			return Save(path, entries)
		}
	}
	return Save(path, append(entries, e))
}

// Remove 删除所有匹配项，返回删除条数；无匹配时不落盘。
func Remove(path string, match func(Entry) bool) (int, error) {
	entries, err := Load(path)
	if err != nil {
		return 0, err
	}
	kept := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if match(e) {
			continue
		}
		kept = append(kept, e)
	}
	removed := len(entries) - len(kept)
	if removed == 0 {
		return 0, nil
	}
	if err := Save(path, kept); err != nil {
		return 0, err
	}
	return removed, nil
}

// Cloud 取云端通道条目（哨兵跟踪对象）。kind 缺失按云端处理：旧 watchlist 条目
// 与手写条目都只有 task_id，把它们当本地条目会让哨兵漏跟踪。
func Cloud(entries []Entry) []Entry {
	return filter(entries, "cloud", "")
}

// Local 取本地通道条目（herdr 能力 agent 作业）。
func Local(entries []Entry) []Entry {
	return filter(entries, "local")
}

func filter(entries []Entry, kinds ...string) []Entry {
	var out []Entry
	for _, e := range entries {
		for _, k := range kinds {
			if e.Kind == k {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// healCorrupt 把坏文件另存 `.corrupt-<ts>` 再删原文件，让下一轮从空清单继续。
func healCorrupt(path string, original []byte) {
	aside := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
	if werr := os.WriteFile(aside, original, 0o644); werr != nil {
		slog.Error("inflight: 清单损坏且另存失败", "file", path, "error", werr)
		return
	}
	_ = os.Remove(path)
	slog.Warn("inflight: 清单损坏，已另存并按空重置", "file", path, "saved_as", aside)
}
