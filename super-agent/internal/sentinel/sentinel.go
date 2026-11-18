package sentinel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"super-agent/internal/hifox"
	"super-agent/internal/inflight"
	"super-agent/internal/platform"
)

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
	// 急停期间注入被闸门拦下走的也是这条路径：变化攒着，解除后一次性补交。
	Injected bool `json:"injected"`
}

// Poll 读在途清单的平台条目，逐个 `hifox task view` 对照快照：首见只记基线（防启动噪声），
// 之后签名变化才汇总成一条事件注入主控，**注入成功才落盘新快照**。
// 快照只保留清单在册任务（移出的随下一轮清掉，避免同任务重派撞旧签名）。
// legacySnapPath 是改名前的快照路径（空 = 无兼容）：新快照还不存在而旧文件在时，
// 拿旧文件当基线，升级后首轮才不会把在途任务全当首见重打、丢掉窗口内的变化。
// 清单/快照损坏时不让哨兵永久死掉：把坏文件改名 `.corrupt-<ts>` 另存、按空处理、告警。
func Poll(ctx context.Context, client *hifox.Client, inflightPath, snapPath, legacySnapPath string, inject func(text string) error) (*Result, error) {
	res := &Result{}
	entries, err := readPlatformEntries(inflightPath)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return res, nil
	}
	snap, err := loadSnapshot(snapPath, legacySnapPath)
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

// ChangesPrompt 拼注入主控的平台状态变化事件文本。
func ChangesPrompt(changes []Change) string {
	var b strings.Builder
	b.WriteString("[触发器: event] 平台任务状态变化（状态类别/名 | 委派 | agent工作）：\n")
	for _, c := range changes {
		fmt.Fprintf(&b, "- %s「%s」：%s → %s\n", c.TaskID, c.Title, c.From, c.To)
	}
	b.WriteString("对照作业台账处置：该收口收口、该转告用户转告；无事不回复用户。")
	return b.String()
}

// readPlatformEntries 读在途清单里的平台条目；旧 watchlist.json 只在 inflight.json
// 尚未生成时作迁移源（sentinel 与主控共用同一份清单，平台与本地同列一处）。
// 哨兵目前只有 hifox 的实现：别的平台的条目只记一条 slog，不注入。
func readPlatformEntries(inflightPath string) ([]inflight.Entry, error) {
	legacy := filepath.Join(filepath.Dir(inflightPath), "watchlist.json")
	entries, err := inflight.LoadMigrating(inflightPath, legacy)
	if err != nil {
		return nil, err
	}
	others := map[string]bool{}
	var blankTasks []string
	for _, e := range entries {
		if e.Kind != inflight.KindPlatform || e.Platform == platform.Hifox {
			continue
		}
		if e.Platform == "" {
			blankTasks = append(blankTasks, e.TaskID)
			continue
		}
		others[e.Platform] = true
	}
	// 缺 platform 的平台条目是写坏的清单项（不是「别的平台」）：点名它，别让它静默
	// 变成没人盯的活。
	if len(blankTasks) > 0 {
		slog.Warn("sentinel: 有平台条目缺 platform 字段，本轮不跟踪（清单被写坏了）", "count", len(blankTasks), "tasks", strings.Join(blankTasks, ", "))
	}
	if len(others) > 0 {
		names := make([]string, 0, len(others))
		for p := range others {
			names = append(names, p)
		}
		sort.Strings(names)
		slog.Warn("sentinel: 这些平台的在途条目尚无哨兵实现，本轮不跟踪", "platforms", strings.Join(names, ", "))
	}
	return inflight.PlatformEntries(entries, platform.Hifox), nil
}

// loadSnapshot 读快照基线。当前文件不存在而旧文件（legacyPath 非空时的改名前的
// 快照）在时，用旧文件当基线——升级后首轮若按空基线重打，窗口内的变化会被当成首见
// 丢掉。旧文件不删也不回写：下次落盘写的是新文件，两边并存到人自己收拾。
// 坏内容按谁坏 heal 谁（另存 .corrupt-<ts> 后按空处理），新文件仍在原位。
func loadSnapshot(path, legacyPath string) (map[string]string, error) {
	data, err := readFileOrHeal(path)
	if err != nil {
		return map[string]string{}, err
	}
	src := path
	if len(data) == 0 && legacyPath != "" {
		if data, err = readFileOrHeal(legacyPath); err != nil {
			return map[string]string{}, err
		}
		src = legacyPath
	}
	if len(data) == 0 {
		return map[string]string{}, nil
	}
	m := map[string]string{}
	if json.Unmarshal(data, &m) != nil {
		healCorrupt(src, data)
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
	// 原子写：快照是「上次看到的状态」的唯一记录，半截文件会被当空快照 → 重打基线
	// → 那段时间的变化不再注入。临时文件 + rename，与在途清单同口径。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
