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
	// Platform 是条目所属平台（平台 CLI 应用名）：注入主控的回答命令要带它，
	// 平台通用路径不写死 hifox（ADR 0023）。
	Platform string `json:"platform,omitempty"`
	Title    string `json:"title"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type Result struct {
	Watched int      `json:"watched"`
	Changed []Change `json:"changed,omitempty"`
	// Injected 仅在「确有变化且注入成功」时为 true；注入失败时 false 且带 err，
	// 快照不推进，下一轮重见同一变化并重试（评审 P1：变化不可静默丢失）。
	// 急停期间注入被闸门拦下走的也是这条路径：变化攒着，解除后一次性补交。
	Injected bool `json:"injected"`
}

// Poll 读在途清单的平台条目，逐个 `hifox task view` 对照快照：首见只记基线（防启动噪声，
// 等用户回话的两态除外——首见即注入，见 ADR 0031），之后签名变化才汇总成一条事件注入主控，
// **注入成功才落盘新快照**。
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
		prev, seen := snap[e.TaskID]
		switch {
		case seen && prev != sig:
			res.Changed = append(res.Changed, Change{TaskID: e.TaskID, Platform: e.Platform, Title: task.Title, From: prev, To: sig})
		case !seen && waitingSignature(sig):
			// 首见只记基线是为了防启动噪声；但「等用户回答」是当下就要用户行动的
			// 状态，静默记基线会让部署前就在等、或派活时已在等的任务永远拿不到
			// 回答通道提示（ADR 0031）。只对这一种签名破例。
			res.Changed = append(res.Changed, Change{TaskID: e.TaskID, Platform: e.Platform, Title: task.Title, From: "首次跟踪", To: sig})
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
	// waiting_human_reply 是「承接人在任务对话里提问、这一轮结束在等用户」的状态；
	// waiting_human_review 是交付完等验收。回答都只能投进那条对话（评论不产生
	// 承接人的回合）；把通道写在这里，主控不必依赖用户每句话再强调一次（ADR 0031）。
	if waiting := waitingChanges(changes); len(waiting) > 0 {
		b.WriteString("\n在等用户回话：\n")
		for _, c := range waiting {
			label, lead := "等你回答", "把承接人的问题转给用户"
			if changeState(c) == "waiting_human_review" {
				label, lead = "等你验收", "把承接人交的东西与待确认点转给用户"
			}
			fmt.Fprintf(&b, "- %s（平台 %s，%s）：%s；用户回话后用 `superctl answer --platform %s --task %s --text \"<用户原话>\"` 投进任务对话——不是任务评论，也不要改任务正文。\n",
				c.TaskID, changePlatform(c), label, lead, changePlatform(c), c.TaskID)
		}
	}
	return b.String()
}

// waitingChanges 列出本轮变化里「在等用户回话」的任务。只看变化后的
// agentWorkingStatus（签名的第三段）：waiting_human_reply 与 waiting_human_review
// 都是承接人结束回合、等用户回话的状态，但前者是提问、后者是等验收。
func waitingChanges(changes []Change) []Change {
	var tasks []Change
	for _, c := range changes {
		switch changeState(c) {
		case "waiting_human_reply", "waiting_human_review":
			tasks = append(tasks, c)
		}
	}
	return tasks
}

// stateOf 取任务签名里的 agent 工作状态（第三段）；签名形状是
// `状态类别/名|委派状态|agent工作状态`。
func stateOf(sig string) string {
	parts := strings.Split(sig, "|")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

// waitingState 判定 agent 工作状态是否在等用户回话（回答或验收）。
func waitingState(state string) bool {
	return state == "waiting_human_reply" || state == "waiting_human_review"
}

// waitingSignature 判定一条任务签名是否正处于等用户回话的状态（首见提示用）。
func waitingSignature(sig string) bool { return waitingState(stateOf(sig)) }

// changeState 取变化后的 agent 工作状态。
func changeState(c Change) string { return stateOf(c.To) }

// changePlatform 取条目平台，空值时如实留空：哨兵的读取面目前只有 hifox 条目
// （readPlatformEntries 只取 hifox），但提示文本不该替一个没写平台的条目不猜。
func changePlatform(c Change) string { return c.Platform }

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
