package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"super-agent/internal/boot"

	"super-agent/internal/capability"
	"super-agent/internal/config"
	"super-agent/internal/herdr"
	"super-agent/internal/inflight"
)

// cmdCheckpoint 让一个已 idle/done 的本地作业（ADR 0030）把当前进度写进 progress 文件。
// 它只负责「递交 checkpoint 指令」；是否可交接由 handoff 再校验文件。
func cmdCheckpoint(args []string) error {
	fs := flag.NewFlagSet("checkpoint", flag.ContinueOnError)
	agentName := fs.String("agent", "", "本地 agent 名")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agent := strings.TrimSpace(*agentName)
	if agent == "" {
		return errors.New("checkpoint 需要 --agent")
	}
	cfg, _, err := loadConfigWithAgents([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)
	return checkpointLocal(ctx, cfg, hc, agent)
}

// checkpointLocal 是可测试的实现：只注入 checkpoint 指令，不替 agent 写文件。
func checkpointLocal(ctx context.Context, cfg config.Config, hc *herdr.Client, agent string) error {
	entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath())
	if err != nil {
		return err
	}
	e, ok := inflight.FindLocal(entries, agent)
	if !ok {
		return fmt.Errorf("%s 不在在途清单里：checkpoint 只用于 superctl dispatch 派出的本地作业", agent)
	}
	agents, err := hc.AgentList(ctx)
	if err != nil {
		return err
	}
	a, ok := herdr.FindAgent(agents, agent)
	if !ok {
		return fmt.Errorf("%s 不在运行（可能已退出）；先收口或按 brief 重派", agent)
	}
	if a.AgentStatus == "working" {
		return fmt.Errorf("%s 正在跑回合；等 idle/done 再 checkpoint（tell 不会抢断）", agent)
	}
	if a.AgentStatus == "blocked" {
		return fmt.Errorf("%s 卡在确认界面；先让用户处理，再 checkpoint", agent)
	}
	if a.AgentStatus != "idle" && a.AgentStatus != "done" {
		return fmt.Errorf("%s 当前 %s；checkpoint 只在 idle/done 时生效", agent, a.AgentStatus)
	}
	progress := orDefault(e.Progress, filepath.Join(cfg.ProgressDir(), agent+".md"))
	if err := os.MkdirAll(filepath.Dir(progress), 0o755); err != nil {
		return err
	}
	text := fmt.Sprintf(
		"[checkpoint] 上下文接近上限。把当前进度写进 %s：状态 / 已完成 / 未完成 / 下一步 / 改过的文件 / 验证 / 待决问题 / 可续。写完结束回合，不要继续任务，不要调用其他工具。",
		progress,
	)
	if err := hc.AgentPromptUntil(ctx, agent, text, []string{"working", "blocked"}, 15000); err != nil {
		return tellPromptError(agent, err)
	}
	return jsonPrint(map[string]any{"agent": agent, "progress": progress, "sent": true})
}

// cmdHandoff 是 job agent 的通用交接：把旧 agent 停掉，用归档 brief + progress
// 重新拉起一个新 agent，结果仍写回原路径。它不依赖 harness 内部会话格式；换
// opencode 等 harness 时，只要 herdr 能停/起，交接路径不变。
func cmdHandoff(args []string) error {
	fs := flag.NewFlagSet("handoff", flag.ContinueOnError)
	agentName := fs.String("agent", "", "要交接的本地 agent 名")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agent := strings.TrimSpace(*agentName)
	if agent == "" {
		return errors.New("handoff 需要 --agent")
	}
	cfg, _, err := loadConfigWithAgents([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)
	out, err := handoffLocal(ctx, cfg, hc, agent)
	if err != nil {
		return err
	}
	return jsonPrint(out)
}

// handoffLocal 是可测试的实现：cmdHandoff 只负责解析参数与选 session。
func handoffLocal(ctx context.Context, cfg config.Config, hc *herdr.Client, agent string) (map[string]string, error) {
	if _, err := ensureTopo(ctx, cfg, hc); err != nil {
		return nil, err
	}
	entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath())
	if err != nil {
		return nil, err
	}
	old, ok := inflight.FindLocal(entries, agent)
	if !ok {
		return nil, fmt.Errorf("%s 不在在途清单里：handoff 只用于 superctl dispatch 派出的本地作业", agent)
	}
	reg, err := capability.Load(cfg.RepoDir)
	if err != nil {
		return nil, err
	}
	recipe, ok := reg.AgentByKey(old.Capability)
	if !ok {
		return nil, fmt.Errorf("能力注册表里找不到 %s 的配方", old.Capability)
	}
	brief := orDefault(old.Brief, filepath.Join(cfg.BriefsDir(), agent+".md"))
	progress := orDefault(old.Progress, filepath.Join(cfg.ProgressDir(), agent+".md"))
	result := orDefault(old.Result, filepath.Join(cfg.ResultsDir(), agent+".md"))
	if !fileNonEmpty(brief) {
		return nil, fmt.Errorf("handoff 拒绝：brief %s 不存在或为空（不能用对话记忆代替机器真值）", brief)
	}
	if !fileNonEmpty(progress) {
		return nil, fmt.Errorf("handoff 拒绝：progress %s 不存在或为空；先 superctl checkpoint", progress)
	}
	if fileNonEmpty(result) {
		return nil, fmt.Errorf("handoff 拒绝：结果文件 %s 已有内容；先按结果收口，不要重开一轮", result)
	}
	agents, err := hc.AgentList(ctx)
	if err != nil {
		return nil, err
	}
	a, ok := herdr.FindAgent(agents, agent)
	if !ok {
		return nil, fmt.Errorf("%s 不在运行；直接按 brief 重派，或先 cancel 清掉僵尸条目", agent)
	}
	if a.AgentStatus != "idle" && a.AgentStatus != "done" {
		return nil, fmt.Errorf("%s 当前 %s；只在 idle/done 时 handoff（working 会砍掉回合，blocked 先找用户）", agent, a.AgentStatus)
	}
	oldSession := a.AgentSession.Value
	runtimeCmd := runtimeCmdPath(cfg)
	if runtimeCmd == "" {
		return nil, errors.New("找不到稳定的 superctl 路径；handoff 的新 agent 没有回报通道，先构建 super-agent/bin/superctl")
	}
	newName := uniqueLocalName(agents, old.Capability)
	// 先解析旧 pane 并标记「交接中」，再申请新 pane：任何一步失败都不该留下一个
	// 没人使用的空 pane。标记后健康对账在宽限期内不会把空窗误报成「丢失」。
	oldPane := a.PaneID
	if oldPane == "" {
		oldPane = old.Pane
	}
	if oldPane == "" {
		return nil, fmt.Errorf("找不到 %s 的 pane；先用 cancel 清掉旧条目再重派", agent)
	}
	old.HandoffAt = time.Now()
	if err := inflight.Add(cfg.InflightPath(), old); err != nil {
		return nil, fmt.Errorf("标记 %s 交接中失败: %w", agent, err)
	}
	newPane, err := acquirePane(ctx, cfg, hc, recipe.Workspace)
	if err != nil {
		restoreFailedHandoff(cfg, old, newName, "申请新 pane 失败")
		return nil, err
	}
	if newPane == oldPane {
		// 拓扑错位时 acquirePane 可能把旧 agent 的 pane 当成空闲；先关它就等于把
		// 新旧 pane 一起带走。直接失败，交给主控 cancel 后重派。
		restoreFailedHandoff(cfg, old, newName, "无法申请独立 pane")
		return nil, fmt.Errorf("交接需要独立 pane：%s 仍指向旧 pane %s", newName, oldPane)
	}
	if err := hc.PaneClose(ctx, oldPane); err != nil {
		_ = hc.PaneClose(ctx, newPane)
		restoreFailedHandoff(cfg, old, newName, "停旧 agent 失败")
		return nil, fmt.Errorf("停旧 agent %s（pane %s）: %w", agent, oldPane, err)
	}
	// 台账路径跟着在途条目走：连续 handoff 时不能再用当前 agent 名的 slug 找，
	// 否则第二次交接会新建一份、活跃区出现两条同一件事的记录。
	ledger := old.Ledger
	if ledger == "" || !fileNonEmpty(ledger) {
		ledger = findActiveLedger(cfg, agent)
	}
	if ledger == "" {
		ledger = ledgerDraft(cfg, agent, "本地", newName, "交接自 "+agent)
	}
	newEntry := inflight.Entry{
		Kind: inflight.KindLocal, Agent: newName, Pane: newPane, Capability: recipe.Key,
		Note: "交接自 " + agent, Brief: brief, Progress: progress, Result: result, Ledger: ledger,
		IdempotencyKey: old.IdempotencyKey, RetryCount: old.RetryCount + 1, Args: append([]string{}, old.Args...), AddedAt: time.Now(),
	}
	body := handoffBody(agent, brief, progress, result)
	prompt := localPrompt(recipe, newName, body, nil, result, progress, shellQuotePath(runtimeCmd))
	if err := startLocal(ctx, cfg, hc, recipe, newName, newPane, old.Args); err != nil {
		_ = hc.PaneClose(ctx, newPane)
		restoreFailedHandoff(cfg, old, newName, "新 agent 启动失败")
		if herdr.IsStartBlocked(err) {
			// handoff 会回收新 pane，审批框也一起没了：不能只告诉用户「先处理」，
			// 得说明处理位置（重新手动起一次 omp 或在 herdr 里确认后再 handoff）。
			return nil, fmt.Errorf("起交接后的 %s: %w（审批框随 pane 一起回收了；先手动处理 omp 启动确认再 handoff）", newName, err)
		}
		return nil, fmt.Errorf("起交接后的 %s: %w", newName, err)
	}
	// 新 agent 一起来就换上在途身份：注入窗口里它可能立刻 report 或结束回合，
	// 身份晚写会让这些信号按「陌生 agent」被丢掉（ADR 0029 同样的窗口）。
	if err := inflight.Replace(cfg.InflightPath(), agent, newEntry); err != nil {
		_ = hc.PaneClose(ctx, newPane)
		restoreFailedHandoff(cfg, old, newName, "登记交接条目失败")
		return nil, fmt.Errorf("交接 agent 已起但登记在途清单失败: %w", err)
	}
	sess, err := boot.SubmitPrompt(ctx, hc, newName, prompt, result)
	if err != nil {
		_ = hc.PaneClose(ctx, newPane)
		_, _ = inflight.Remove(cfg.InflightPath(), func(e inflight.Entry) bool { return e.Agent == newName })
		restoreFailedHandoff(cfg, old, newName, "首条交接 prompt 未落地")
		return nil, fmt.Errorf("交接 agent 已起但首条 prompt 未落地（已关 pane）: %w", err)
	}
	// 新 agent 必须是新会话；若 herdr 报出同一个 session，说明底层 CLI 复用了旧上下文，
	// 这次交接等于没换脑，必须当失败处理。
	if oldSession != "" && sess != "" && oldSession == sess {
		_ = hc.PaneClose(ctx, newPane)
		_, _ = inflight.Remove(cfg.InflightPath(), func(e inflight.Entry) bool { return e.Agent == newName })
		restoreFailedHandoff(cfg, old, newName, "交接后仍是旧会话")
		return nil, fmt.Errorf("交接失败：%s 仍复用旧会话 %s（没有真正换上下文）", newName, sess)
	}
	recordHandoff(cfg, old, newEntry)
	appendLedgerNote(ledger, fmt.Sprintf("- 交接：%s → %s（第 %d 次；brief %s；progress %s）", agent, newName, newEntry.RetryCount, brief, progress))
	return map[string]string{
		"old_agent": agent, "agent": newName, "pane": newPane,
		"result": result, "brief": brief, "progress": progress, "ledger": ledger,
	}, nil
}

func handoffBody(oldAgent, brief, progress, result string) string {
	return fmt.Sprintf(`[交接续跑]
这是从 %s 交接过来的同一任务。先读：
- brief: %s
- progress: %s

不要重复已完成步骤；按 progress 的「下一步」继续，最终结果写回 %s。
如果 progress 的「待决问题」没有解决，先用 report 上报主控，不要猜。`, oldAgent, brief, progress, result)
}

// uniqueLocalName 给交接出来的新 agent 起名：占用源是 herdr 里在跑的 agent 名。
// 后缀规则与 dispatch 共用 uniqueAgentName（-2、-3），一条概念一套规则。
func uniqueLocalName(agents []herdr.Agent, key string) string {
	used := map[string]bool{}
	for _, a := range agents {
		used[a.Name] = true
	}
	base := fmt.Sprintf("%s-%s", key, localNameNow().UTC().Format("150405"))
	return uniqueAgentName(base, func(name string) bool { return used[name] })
}

// restoreFailedHandoff 在旧 agent 已停、新 agent 没起来/没落地时把旧条目放回
// 在途清单（标注失败），让主控看得见这件活，而不是静默消失。
func restoreFailedHandoff(cfg config.Config, old inflight.Entry, newName, reason string) {
	old.Note = strings.TrimSpace(old.Note + "（" + reason + "，交接目标 " + newName + "）")
	old.Pane = ""
	if err := inflight.Add(cfg.InflightPath(), old); err != nil {
		// 恢复失败只能靠 cancel/巡检兜底，不掩盖原始错误。
		return
	}
}

// findActiveLedger 按 agent slug 在活跃区找既有台账（不依赖当天日期）。
func findActiveLedger(cfg config.Config, agent string) string {
	suffix := "-" + sanitizeSlug(agent) + ".md"
	entries, err := os.ReadDir(cfg.ActiveDir())
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			return filepath.Join(cfg.ActiveDir(), e.Name())
		}
	}
	return ""
}

// appendLedgerNote 把交接事实追加到主控台账。失败不阻断任务——交接已发生，
// 台账缺失由主控下一轮对账补上。
func appendLedgerNote(path, note string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(note + "\n")
}

// recordHandoff 留一份机器可查的交接记录；旧会话全文仍按轮次目录归档。
func recordHandoff(cfg config.Config, old, newEntry inflight.Entry) {
	path := filepath.Join(cfg.HandoffsDir(), newEntry.Agent+".md")
	body := fmt.Sprintf(`# 本地作业交接

- 旧 agent：%s
- 新 agent：%s
- 能力：%s
- brief：%s
- progress：%s
- 结果：%s
- 时间：%s
`, old.Agent, newEntry.Agent, newEntry.Capability, newEntry.Brief, newEntry.Progress, newEntry.Result, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		// 交接记录失败不影响任务继续；主控台账仍会记一笔。
		return
	}
}
