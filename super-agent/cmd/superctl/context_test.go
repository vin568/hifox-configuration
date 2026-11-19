package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"super-agent/internal/config"
	"super-agent/internal/ctxmeter"
	"super-agent/internal/herdr"
	"super-agent/internal/inflight"
)

func TestContextAlertLevelAndText(t *testing.T) {
	p := ctxmeter.DefaultPolicy()
	c := controlledContext{
		Target: ctxmeter.Target{Name: "worker-1", Role: "job", Status: "idle", ResultPath: "/r.md", BriefPath: "/b.md", ProgressPath: "/p.md"},
		Snapshot: ctxmeter.Snapshot{
			Provider: "hifox", Model: "deepseek-flash", PromptTokens: 800, OutputTokens: 10,
			WindowTokens: 1000, Source: ctxmeter.SourceMeasured,
		},
	}
	if got := contextAlertLevel(c.Snapshot, p); got != "critical" {
		t.Fatalf("level=%q, want critical", got)
	}
	text := contextAlertText(c, "critical")
	for _, want := range []string{"worker-1", "810/1000", "checkpoint", "/p.md"} {
		if !strings.Contains(text, want) {
			t.Fatalf("alert text missing %q: %s", want, text)
		}
	}
	if got := contextAlertLevel(ctxmeter.Snapshot{Source: ctxmeter.SourceUnknown}, p); got != "" {
		t.Fatalf("unknown should not alert, got %q", got)
	}
}

func TestContextAlertWarnLevel(t *testing.T) {
	p := ctxmeter.DefaultPolicy()
	warn := ctxmeter.Snapshot{PromptTokens: 750000, WindowTokens: 1000000, Source: ctxmeter.SourceMeasured}
	if got := contextAlertLevel(warn, p); got != "warn" {
		t.Fatalf("750k/1M level=%q, want warn", got)
	}
	critical := ctxmeter.Snapshot{PromptTokens: 850000, WindowTokens: 1000000, Source: ctxmeter.SourceMeasured}
	if got := contextAlertLevel(critical, p); got != "critical" {
		t.Fatalf("850k/1M level=%q, want critical", got)
	}
	quiet := ctxmeter.Snapshot{PromptTokens: 699999, WindowTokens: 1000000, Source: ctxmeter.SourceMeasured}
	if got := contextAlertLevel(quiet, p); got != "" {
		t.Fatalf("below warn level=%q, want empty", got)
	}
}

func TestBriefArchivedAndIdempotency(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "完整任务正文\n第二行", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	brief := out["brief"]
	data, err := os.ReadFile(brief)
	if err != nil || !strings.Contains(string(data), "完整任务正文") {
		t.Fatalf("brief 未落盘: %v %s", err, data)
	}
	entries, _ := inflight.Load(cfg.InflightPath())
	if len(entries) != 1 || entries[0].Brief != brief || entries[0].Progress == "" || entries[0].Result == "" {
		t.Fatalf("在途条目缺交接字段: %+v", entries)
	}
	if entries[0].IdempotencyKey == "" {
		t.Fatal("缺少幂等键")
	}
	// 同一能力 + 同一正文 + 同一附件组必须拒绝；这里把并发额度让开，确保命中的是幂等守卫。
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{
		Kind: inflight.KindLocal, Agent: "other-1", Capability: "other", IdempotencyKey: localIdempotencyKey("worker", "完整任务正文\n第二行", nil),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = dispatchLocal(context.Background(), cfg, hc, "worker", "完整任务正文\n第二行", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "同一任务") {
		t.Fatalf("幂等守卫未生效: %v", err)
	}
}

func TestLocalHealth(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	// 有结果、agent 不在 → 已完成未收口。
	result := filepath.Join(cfg.ResultsDir(), "worker-1.md")
	if err := os.WriteFile(result, []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	cond, _ := localHealth(cfg, inflight.Entry{Agent: "worker-1", Result: result}, herdr.Agent{}, false, 0)
	if cond != "exited-with-result" {
		t.Fatalf("cond=%q", cond)
	}
	// 无结果、agent 不在 → lost。
	if cond, _ := localHealth(cfg, inflight.Entry{Agent: "worker-2"}, herdr.Agent{}, false, 0); cond != "lost" {
		t.Fatalf("cond=%q", cond)
	}
	// working + 会话文件很旧 → stalled。
	sess := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(sess, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(sess, old, old); err != nil {
		t.Fatal(err)
	}
	cond, _ = localHealth(cfg, inflight.Entry{Agent: "worker-3"}, herdr.Agent{AgentStatus: "working", AgentSession: herdr.AgentSession{Value: sess}}, true, time.Minute)
	if cond != "stalled" {
		t.Fatalf("cond=%q", cond)
	}
}

func TestHandoffBodyAndUniqueName(t *testing.T) {
	body := handoffBody("worker-1", "/b.md", "/p.md", "/r.md")
	for _, want := range []string{"worker-1", "/b.md", "/p.md", "/r.md", "不要重复"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	agents := []herdr.Agent{{Name: "worker-120000"}}
	name := uniqueLocalName(agents, "worker")
	if name == "" || strings.HasPrefix(name, "worker-120000") {
		t.Fatalf("unexpected name %q", name)
	}
}

func TestContextConfigDefaults(t *testing.T) {
	c := config.Default()
	if c.ContextWarnRatio <= 0 || c.ContextReserveRatio <= 0 || c.ContextReserveMinTokens <= 0 {
		t.Fatalf("context defaults missing: %+v", c)
	}
}

// handoff 的三条拒绝线：没有 brief、没有 progress、结果文件已就绪。拒绝必须在
// 停旧 agent 之前发生。
func TestHandoffRejectsWithoutMachineTruth(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	if _, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	entries, _ := inflight.Load(cfg.InflightPath())
	agent := entries[0].Agent
	if err := os.Remove(entries[0].Brief); err != nil {
		t.Fatal(err)
	}
	if _, err := handoffLocal(context.Background(), cfg, hc, agent); err == nil || !strings.Contains(err.Error(), "brief") {
		t.Fatalf("缺 brief 必须拒绝: %v", err)
	}
	// 补回 brief，缺 progress。
	if err := os.WriteFile(entries[0].Brief, []byte("正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := handoffLocal(context.Background(), cfg, hc, agent); err == nil || !strings.Contains(err.Error(), "progress") {
		t.Fatalf("缺 progress 必须拒绝: %v", err)
	}
	// progress 有了，结果文件已就绪。
	if err := os.WriteFile(entries[0].Progress, []byte("状态：完成"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entries[0].Result, []byte("已交付"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := handoffLocal(context.Background(), cfg, hc, agent); err == nil || !strings.Contains(err.Error(), "结果文件") {
		t.Fatalf("结果已就绪必须拒绝: %v", err)
	}
	if len(f.closed) != 0 {
		t.Fatalf("拒绝时不该停旧 agent: %v", f.closed)
	}
}

func TestHandoffReplacesAgentAndKeepsResult(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	old := out["agent"]
	if err := os.WriteFile(out["progress"], []byte("状态：做了一半\n下一步：继续"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := handoffLocal(context.Background(), cfg, hc, old)
	if err != nil {
		t.Fatal(err)
	}
	if got["agent"] == "" || got["agent"] == old {
		t.Fatalf("应起新 agent: %v", got)
	}
	if got["result"] != out["result"] {
		t.Fatalf("结果路径应保持稳定: %v vs %v", got["result"], out["result"])
	}
	entries, _ := inflight.Load(cfg.InflightPath())
	if len(entries) != 1 || entries[0].Agent != got["agent"] {
		t.Fatalf("在途清单应只剩新 agent: %+v", entries)
	}
	if entries[0].Brief == "" || entries[0].Progress == "" {
		t.Fatalf("交接条目应带回 brief/progress: %+v", entries[0])
	}
	if entries[0].IdempotencyKey == "" || entries[0].RetryCount != 1 {
		t.Fatalf("交接必须继承幂等键并递增重试次数: %+v", entries[0])
	}
	if len(f.prompts) == 0 || !strings.Contains(f.prompts[len(f.prompts)-1], "交接续跑") {
		t.Fatalf("首条 prompt 应是交接意图: %v", f.prompts)
	}
	if _, still := herdr.FindAgent(f.agents, old); still {
		t.Fatalf("旧 agent 应已停止: %+v", f.agents)
	}
	ledgers, _ := os.ReadDir(cfg.ActiveDir())
	if len(ledgers) != 1 {
		t.Fatalf("交接应复用旧台账，不应新增第二份: %v", ledgers)
	}
	data, _ := os.ReadFile(filepath.Join(cfg.ActiveDir(), ledgers[0].Name()))
	if !strings.Contains(string(data), "交接：") {
		t.Fatalf("台账应记一笔交接: %s", data)
	}
}

func TestControlledTargetsIncludesOnlyMasterAndInflight(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{
		Kind: inflight.KindLocal, Agent: "worker-1", Capability: "worker", Note: "跑一遍",
	}); err != nil {
		t.Fatal(err)
	}
	f := &dispatchFake{agents: []herdr.Agent{
		{Name: "songjiang", AgentStatus: "idle", PaneID: "w1:p1"},
		{Name: "worker-1", AgentStatus: "working", PaneID: "w3:p1"},
		{Name: "stranger", AgentStatus: "working", PaneID: "w3:p2"},
	}}
	targets, err := controlledTargets(context.Background(), cfg, &herdr.Client{Run: f.run})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets=%+v, want master + inflight worker only", targets)
	}
	if targets[0].Role != "master" || targets[1].Name != "worker-1" {
		t.Fatalf("unexpected targets: %+v", targets)
	}
}

func TestMaybeRotateUsesTokenRatio(t *testing.T) {
	fastFresh(t)
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	cfg.SessionMaxBytes = 0 // 关闭字节兜底，必须走 token 判定
	cfg.ContextWindows = map[string]int64{"hifox/deepseek-flash": 1000}
	cfg.ContextModelDB = filepath.Join(t.TempDir(), "missing.db")
	cfg.ContextReserveMinTokens = 0
	cfg.ContextReserveRatio = 0.15
	sess := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"type":"message","message":{"role":"assistant","model":"deepseek-flash","provider":"hifox","contextSnapshot":{"promptTokens":900},"usage":{"output":0}}}`
	if err := os.WriteFile(sess, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &rotateFake{status: "idle", session: sess, after: filepath.Join(t.TempDir(), "after.jsonl")}
	if err := maybeRotate(context.Background(), cfg, &herdr.Client{Run: f.run}); err != nil {
		t.Fatalf("maybeRotate: %v", err)
	}
	if !f.freshCalled {
		t.Fatal("900/1000 tokens should cross the 850 ceiling")
	}
}

func TestMaybeRotateTokenRatioRespectsWorking(t *testing.T) {
	fastFresh(t)
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	cfg.SessionMaxBytes = 0
	cfg.ContextWindows = map[string]int64{"hifox/deepseek-flash": 1000}
	cfg.ContextModelDB = filepath.Join(t.TempDir(), "missing.db")
	cfg.ContextReserveMinTokens = 0
	sess := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"type":"message","message":{"role":"assistant","model":"deepseek-flash","provider":"hifox","contextSnapshot":{"promptTokens":900}}}`
	if err := os.WriteFile(sess, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &rotateFake{status: "working", session: sess, after: filepath.Join(t.TempDir(), "after.jsonl")}
	if err := maybeRotate(context.Background(), cfg, &herdr.Client{Run: f.run}); err != nil {
		t.Fatalf("maybeRotate: %v", err)
	}
	if f.freshCalled {
		t.Fatal("working agent must not be rotated")
	}
}

func TestCheckpointInjectsAndRefusesWorking(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.prompts = nil
	if err := checkpointLocal(context.Background(), cfg, hc, out["agent"]); err != nil {
		t.Fatal(err)
	}
	if len(f.prompts) != 1 || !strings.Contains(f.prompts[0], "[checkpoint]") || !strings.Contains(f.prompts[0], out["progress"]) {
		t.Fatalf("checkpoint prompt 不对: %v", f.prompts)
	}
	f.agents[0].AgentStatus = "working"
	f.prompts = nil
	if err := checkpointLocal(context.Background(), cfg, hc, out["agent"]); err == nil || !strings.Contains(err.Error(), "正在跑回合") {
		t.Fatalf("working 必须拒绝: %v", err)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("拒绝时不该注入: %v", f.prompts)
	}
	f.agents[0].AgentStatus = "unknown"
	if err := checkpointLocal(context.Background(), cfg, hc, out["agent"]); err == nil || !strings.Contains(err.Error(), "只在 idle/done") {
		t.Fatalf("unknown 必须拒绝: %v", err)
	}
	f.agents[0].AgentStatus = "idle"
	f.promptErr = &herdr.CmdError{Code: "agent_prompt_stalled", Message: "stalled"}
	if err := checkpointLocal(context.Background(), cfg, hc, out["agent"]); err == nil || !strings.Contains(err.Error(), "没有对消息起反应") {
		t.Fatalf("stalled 必须如实失败: %v", err)
	}
}

func TestHandoffRejectsReusedSession(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	sess := filepath.Join(t.TempDir(), "old-session.jsonl")
	f := &dispatchFake{session: sess}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out["progress"], []byte("状态：做了一半\n下一步：继续"), 0o644); err != nil {
		t.Fatal(err)
	}
	// fake 的 agent list 对新旧 agent 都报同一个 session，模拟底层 CLI 复用旧会话。
	if _, err := handoffLocal(context.Background(), cfg, hc, out["agent"]); err == nil || !strings.Contains(err.Error(), "旧会话") {
		t.Fatalf("复用旧会话必须判失败: %v", err)
	}
	entries, _ := inflight.Load(cfg.InflightPath())
	if len(entries) != 1 || entries[0].Agent != out["agent"] {
		t.Fatalf("失败后应恢复旧在途条目: %+v", entries)
	}
}

func TestHandoffPreservesDispatchArgs(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, []string{"--model", "deepseek-flash"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out["progress"], []byte("状态：一半"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := handoffLocal(context.Background(), cfg, hc, out["agent"]); err != nil {
		t.Fatal(err)
	}
	last := f.startArgs[len(f.startArgs)-1]
	if !strings.Contains(strings.Join(last, " "), "--model deepseek-flash") {
		t.Fatalf("handoff 必须继承派活参数: %v", last)
	}
}

func writeContextSession(t *testing.T, path string, prompt int64, epoch int) {
	t.Helper()
	line := fmt.Sprintf(`{"type":"message","message":{"role":"assistant","model":"deepseek-flash","provider":"hifox","contextSnapshot":{"promptTokens":%d,"compactionEpoch":%d},"usage":{"output":0}}}`, prompt, epoch)
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contextCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := testCfg(t, testRepo(t))
	cfg.SessionMaxBytes = 0
	cfg.ContextWindows = map[string]int64{"hifox/deepseek-flash": 1000}
	cfg.ContextModelDB = filepath.Join(t.TempDir(), "missing.db")
	cfg.ContextReserveMinTokens = 0
	cfg.ContextReserveRatio = 0.15
	cfg.LocalStallAfterRaw = "1m"
	return cfg
}

func TestScanContextAlertsDedupeEscalationAndFailure(t *testing.T) {
	cfg := contextCfg(t)
	cfg.ContextWindows = map[string]int64{"hifox/deepseek-flash": 100000}
	sess := filepath.Join(t.TempDir(), "worker.jsonl")
	writeContextSession(t, sess, 75000, 0) // warn：默认 70% 线
	f := &dispatchFake{agents: []herdr.Agent{
		// master 也高上下文，但告警面只应包含 job；这行是角色过滤的负向锚点。
		{Name: "songjiang", AgentStatus: "idle", PaneID: "w1:p1", AgentSession: herdr.AgentSession{Value: sess}},
		{Name: "worker-1", AgentStatus: "idle", PaneID: "w3:p1", AgentSession: herdr.AgentSession{Value: sess}},
	}}
	hc := &herdr.Client{Run: f.run}
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: inflight.KindLocal, Agent: "worker-1", Capability: "worker"}); err != nil {
		t.Fatal(err)
	}
	sent, err := scanContextAlerts(context.Background(), cfg, hc)
	if err != nil || sent != 1 {
		t.Fatalf("first scan sent=%d err=%v", sent, err)
	}
	if len(f.prompts) == 0 || !strings.Contains(f.prompts[len(f.prompts)-1], "[触发器: context]") {
		t.Fatalf("alert prompt missing: %v", f.prompts)
	}
	if strings.Contains(f.prompts[len(f.prompts)-1], "songjiang") {
		t.Fatalf("master must not get job context alerts: %v", f.prompts)
	}
	if sent, _ := scanContextAlerts(context.Background(), cfg, hc); sent != 0 {
		t.Fatalf("same session/epoch/level must be deduped, sent=%d", sent)
	}
	// 同一 epoch 从 warn 升到 critical 必须再告警一次。
	writeContextSession(t, sess, 85000, 0)
	if sent, _ := scanContextAlerts(context.Background(), cfg, hc); sent != 1 {
		t.Fatalf("warn->critical escalation must alert, sent=%d", sent)
	}
	if sent, _ := scanContextAlerts(context.Background(), cfg, hc); sent != 0 {
		t.Fatalf("same critical must be deduped, sent=%d", sent)
	}
	writeContextSession(t, sess, 85000, 1)
	if sent, _ := scanContextAlerts(context.Background(), cfg, hc); sent != 1 {
		t.Fatalf("epoch change must re-alert, sent=%d", sent)
	}
	// 注入失败不推进状态：主控 blocked 时下一轮还会重试。
	writeContextSession(t, sess, 85000, 2)
	f.promptErr = &herdr.CmdError{Code: "agent_blocked", Message: "blocked"}
	if _, err := scanContextAlerts(context.Background(), cfg, hc); err == nil {
		t.Fatal("blocked injection should surface an error")
	}
	st := loadContextAlerts(cfg.ContextAlertsPath())
	if st.Agents["worker-1"].Epoch != 1 {
		t.Fatalf("failed injection must not advance state: %+v", st.Agents["worker-1"])
	}
}

func TestScanLocalHealthDedupeAndClear(t *testing.T) {
	cfg := contextCfg(t)
	sess := filepath.Join(t.TempDir(), "worker.jsonl")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.WriteFile(sess, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sess, old, old); err != nil {
		t.Fatal(err)
	}
	f := &dispatchFake{agents: []herdr.Agent{
		{Name: "songjiang", AgentStatus: "idle", PaneID: "w1:p1"},
		{Name: "worker-1", AgentStatus: "idle", PaneID: "w3:p1", AgentSession: herdr.AgentSession{Value: sess}},
	}}
	hc := &herdr.Client{Run: f.run}
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: inflight.KindLocal, Agent: "worker-1", Capability: "worker"}); err != nil {
		t.Fatal(err)
	}
	// idle 的旧会话文件不是停滞：只有 working 才判。
	if sent, _ := scanLocalHealth(context.Background(), cfg, hc); sent != 0 {
		t.Fatalf("idle old session should not alert, sent=%d", sent)
	}
	f.agents[1].AgentStatus = "working"
	if sent, _ := scanLocalHealth(context.Background(), cfg, hc); sent != 1 {
		t.Fatalf("working stale session must alert, sent=%d", sent)
	}
	if sent, _ := scanLocalHealth(context.Background(), cfg, hc); sent != 0 {
		t.Fatalf("same condition must be deduped, sent=%d", sent)
	}
	// 注入失败不能推进状态：否则主控 blocked 时会永久吞掉后续告警。
	f.agents = f.agents[:1] // worker 消失 -> lost（与之前的 stalled 不同）
	f.promptErr = &herdr.CmdError{Code: "agent_blocked", Message: "blocked"}
	if _, err := scanLocalHealth(context.Background(), cfg, hc); err == nil {
		t.Fatal("blocked injection should surface an error")
	}
	if st := loadSupervision(cfg.SupervisionPath()); st.Agents["worker-1"] != "stalled" {
		t.Fatalf("failed injection must not advance state: %+v", st.Agents)
	}
	f.promptErr = nil
	if sent, _ := scanLocalHealth(context.Background(), cfg, hc); sent != 1 {
		t.Fatalf("retry after blocked should alert, sent=%d", sent)
	}
	// 恢复：agent 回来后会话文件变新。
	f.agents = append(f.agents, herdr.Agent{Name: "worker-1", AgentStatus: "working", PaneID: "w3:p1", AgentSession: herdr.AgentSession{Value: sess}})
	now := time.Now()
	if err := os.Chtimes(sess, now, now); err != nil {
		t.Fatal(err)
	}
	if sent, _ := scanLocalHealth(context.Background(), cfg, hc); sent != 0 {
		t.Fatalf("recovery should not alert, sent=%d", sent)
	}
	if st := loadSupervision(cfg.SupervisionPath()); len(st.Agents) != 0 {
		t.Fatalf("recovery must clear state: %+v", st.Agents)
	}
	if len(f.closed) != 0 {
		t.Fatalf("health scan must never close panes: %v", f.closed)
	}
}

func TestHandoffRejectsWorkingAgent(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out["progress"], []byte("状态：一半"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.agents[0].AgentStatus = "working"
	if _, err := handoffLocal(context.Background(), cfg, hc, out["agent"]); err == nil || !strings.Contains(err.Error(), "只在 idle/done") {
		t.Fatalf("working must be refused: %v", err)
	}
	if len(f.closed) != 0 {
		t.Fatalf("refusal must not close the pane: %v", f.closed)
	}
	entries, _ := inflight.Load(cfg.InflightPath())
	if len(entries) != 1 || entries[0].Agent != out["agent"] {
		t.Fatalf("refusal must keep the entry: %+v", entries)
	}
}

func TestMaybeRotateJobRoleAndBytePrecedence(t *testing.T) {
	fastFresh(t)
	cfg := contextCfg(t)
	cfg.SessionMaxBytes = 10 // 字节兜底本来会触发；Trusted token 判定必须优先
	masterSess := filepath.Join(t.TempDir(), "master.jsonl")
	jobSess := filepath.Join(t.TempDir(), "job.jsonl")
	writeContextSession(t, masterSess, 100, 0)
	writeContextSession(t, jobSess, 900, 0)
	f := &dispatchFake{agents: []herdr.Agent{
		{Name: "songjiang", AgentStatus: "idle", PaneID: "w1:p1", AgentSession: herdr.AgentSession{Value: masterSess}},
		{Name: "worker-1", AgentStatus: "idle", PaneID: "w3:p1", AgentSession: herdr.AgentSession{Value: jobSess}},
	}}
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: inflight.KindLocal, Agent: "worker-1", Capability: "worker"}); err != nil {
		t.Fatal(err)
	}
	if err := maybeRotate(context.Background(), cfg, &herdr.Client{Run: f.run}); err != nil {
		t.Fatal(err)
	}
	if len(f.started) != 0 {
		t.Fatalf("job must not rotate and known-below-ceiling master must ignore bytes: %v", f.started)
	}
}

func TestAgentContextJSONUnknownAndRatio(t *testing.T) {
	unknown := controlledContext{Target: ctxmeter.Target{Name: "w", Role: "job"}, Snapshot: ctxmeter.Snapshot{
		Source: ctxmeter.SourceUnknown, PromptTokens: 100, WindowTokens: 0,
	}}.json()
	if unknown.Known || unknown.Ratio != 0 {
		t.Fatalf("unknown must not fake a ratio: %+v", unknown)
	}
	known := controlledContext{Target: ctxmeter.Target{Name: "w", Role: "job"}, Snapshot: ctxmeter.Snapshot{
		PromptTokens: 100, WindowTokens: 1000, Source: ctxmeter.SourceMeasured,
	}}.json()
	if !known.Known || known.Ratio != 0.1 {
		t.Fatalf("known ratio wrong: %+v", known)
	}
}

func TestLocalResultPathUsesEntryResult(t *testing.T) {
	cfg := testCfg(t, testRepo(t))
	custom := filepath.Join(cfg.ResultsDir(), "stable.md")
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: inflight.KindLocal, Agent: "worker-1", Result: custom}); err != nil {
		t.Fatal(err)
	}
	if got := localResultPath(cfg, "worker-1"); got != custom {
		t.Fatalf("localResultPath=%q, want %q", got, custom)
	}
}

func TestIdempotencyKeyDependsOnBodyAndAttachments(t *testing.T) {
	base := localIdempotencyKey("worker", "body-a", nil)
	if base == localIdempotencyKey("worker", "body-b", nil) {
		t.Fatal("different bodies must produce different keys")
	}
	if base == localIdempotencyKey("worker", "body-a", []string{"/a.png"}) {
		t.Fatal("different attachments must produce different keys")
	}
	if base != localIdempotencyKey("worker", " body-a\n", nil) {
		t.Fatal("whitespace-only body changes must not alter the key")
	}
	one := localIdempotencyKey("worker", "body", []string{"/b", "/a"})
	two := localIdempotencyKey("worker", "body", []string{"/a", "/b"})
	if one != two {
		t.Fatal("attachment order must not alter the key")
	}
}

func TestHandoffDocIncludesJobPathsAndNoMasterDefaults(t *testing.T) {
	cfg := contextCfg(t)
	jobSess := filepath.Join(t.TempDir(), "job.jsonl")
	writeContextSession(t, jobSess, 100, 0)
	f := &dispatchFake{agents: []herdr.Agent{
		{Name: "songjiang", AgentStatus: "idle", PaneID: "w1:p1"},
		{Name: "worker-1", AgentStatus: "idle", PaneID: "w3:p1", AgentSession: herdr.AgentSession{Value: jobSess}},
	}}
	e := inflight.Entry{Kind: inflight.KindLocal, Agent: "worker-1", Capability: "worker",
		Brief: "/b.md", Progress: "/p.md", Result: "/r.md"}
	if err := inflight.Add(cfg.InflightPath(), e); err != nil {
		t.Fatal(err)
	}
	path := handoffPath(context.Background(), cfg, &herdr.Client{Run: f.run})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"worker-1", "brief=/b.md", "progress=/p.md", "result=/r.md"} {
		if !strings.Contains(text, want) {
			t.Fatalf("handoff.md missing %q:\n%s", want, text)
		}
	}
	// master/retro 不该出现按名字猜出来的 brief/result 默认路径。
	if strings.Contains(text, "songjiang") && (strings.Contains(text, "brief="+cfg.BriefsDir()) || strings.Contains(text, "result="+cfg.ResultsDir())) {
		t.Fatalf("master should not get job default paths:\n%s", text)
	}
}

func TestHandoffReusesLedgerFromEarlierDay(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out["progress"], []byte("状态：一半"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 模拟派活发生在昨天：删掉今天的台账，只留旧日期的同名台账。
	if err := os.Remove(out["ledger"]); err != nil {
		t.Fatal(err)
	}
	oldLedger := filepath.Join(cfg.ActiveDir(), "20200101-"+sanitizeSlug(out["agent"])+".md")
	if err := os.WriteFile(oldLedger, []byte("# 旧台账\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := handoffLocal(context.Background(), cfg, hc, out["agent"]); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cfg.ActiveDir())
	if len(entries) != 1 {
		t.Fatalf("handoff 应复用旧台账，实际活跃区: %v", entries)
	}
	data, _ := os.ReadFile(oldLedger)
	if !strings.Contains(string(data), "交接：") {
		t.Fatalf("旧台账应记一笔交接: %s", data)
	}
}

func TestLocalHealthSkipsHandoffWindow(t *testing.T) {
	cfg := testCfg(t, testRepo(t))
	e := inflight.Entry{Agent: "worker-1", HandoffAt: time.Now()}
	if cond, _ := localHealth(cfg, e, herdr.Agent{}, false, time.Minute); cond != "" {
		t.Fatalf("handoff 进行中不该报丢失: %q", cond)
	}
	e.HandoffAt = time.Now().Add(-30 * time.Minute)
	if cond, _ := localHealth(cfg, e, herdr.Agent{}, false, time.Minute); cond != "lost" {
		t.Fatalf("超过宽限期应恢复判定: %q", cond)
	}
}

// 新 agent 一起来就必须在在途清单里：注入窗口里它可能立刻 report/done，
// 身份晚写会让这些信号按陌生 agent 被丢掉。
func TestHandoffRegistersBeforePrompt(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	sess := filepath.Join(t.TempDir(), "same.jsonl")
	f := &dispatchFake{session: sess}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out["progress"], []byte("状态：一半"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 记录每次 agent prompt 时在途清单是否已包含被注入的 agent。
	var missing []string
	f.onPrompt = func(args []string) {
		if len(args) > 2 && args[2] != cfg.MasterAgent {
			entries, _ := inflight.Load(cfg.InflightPath())
			if !inflight.HasLocal(entries, args[2]) {
				missing = append(missing, args[2])
			}
		}
	}
	// 只给旧 agent 一个会话；新 agent 由 fake 报空会话（跳过「复用旧会话」判定，
	// 本条用例只关心登记顺序）。
	f.agents[0].AgentSession = herdr.AgentSession{Value: sess}
	f.session = ""
	if _, err := handoffLocal(context.Background(), cfg, hc, out["agent"]); err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("注入时 agent 尚未登记在途清单: %v", missing)
	}
}

func TestRunSupervisionScansWiresBothScans(t *testing.T) {
	cfg := contextCfg(t)
	cfg.ContextWindows = map[string]int64{"hifox/deepseek-flash": 100000}
	critSess := filepath.Join(t.TempDir(), "crit.jsonl")
	stallSess := filepath.Join(t.TempDir(), "stall.jsonl")
	writeContextSession(t, critSess, 85000, 0)
	if err := os.WriteFile(stallSess, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stallSess, old, old); err != nil {
		t.Fatal(err)
	}
	f := &dispatchFake{agents: []herdr.Agent{
		{Name: "songjiang", AgentStatus: "idle", PaneID: "w1:p1"},
		{Name: "worker-crit", AgentStatus: "idle", PaneID: "w3:p1", AgentSession: herdr.AgentSession{Value: critSess}},
		{Name: "worker-stall", AgentStatus: "working", PaneID: "w3:p2", AgentSession: herdr.AgentSession{Value: stallSess}},
	}}
	for _, name := range []string{"worker-crit", "worker-stall"} {
		if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: inflight.KindLocal, Agent: name, Capability: "worker"}); err != nil {
			t.Fatal(err)
		}
	}
	alerts, health := runSupervisionScans(context.Background(), cfg, &herdr.Client{Run: f.run})
	if alerts != 1 || health != 1 {
		t.Fatalf("scans alerts=%d health=%d, want 1/1", alerts, health)
	}
	joined := strings.Join(f.prompts, "\n")
	if !strings.Contains(joined, "[触发器: context]") || !strings.Contains(joined, "stalled") {
		t.Fatalf("both scans should inject:\n%s", joined)
	}
}

func TestContextPolicyReadsConfigOverrides(t *testing.T) {
	cfg := contextCfg(t)
	cfg.ContextWindows = map[string]int64{"hifox/deepseek-flash": 100000}
	cfg.ContextWarnRatio = 0.5
	cfg.ContextWarnMarginTokens = 8000
	cfg.ContextReserveRatio = 0.15
	cfg.ContextReserveMinTokens = 0
	p := contextPolicy(cfg)
	s := ctxmeter.Snapshot{PromptTokens: 60000, WindowTokens: 100000, Source: ctxmeter.SourceMeasured}
	if got := contextAlertLevel(s, p); got != "warn" {
		t.Fatalf("custom warn ratio: level=%q, want warn", got)
	}
	cfg.ContextReserveRatio = 0.5
	p = contextPolicy(cfg)
	if got := contextAlertLevel(s, p); got != "critical" {
		t.Fatalf("custom reserve ratio: level=%q, want critical", got)
	}
}

// 交接过程中健康对账必须容忍「旧 agent 已停、新 agent 未登记」的短暂空窗：
// HandoffAt 是宽限期标记，不置就绪会误报 lost。
func TestHandoffMarksInflightDuringWindow(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out["progress"], []byte("状态：一半"), 0o644); err != nil {
		t.Fatal(err)
	}
	var seenMarked bool
	f.onPaneClose = func(args []string) {
		entries, _ := inflight.Load(cfg.InflightPath())
		for _, e := range entries {
			if e.Kind == inflight.KindLocal && e.Agent == out["agent"] && !e.HandoffAt.IsZero() {
				seenMarked = true
			}
		}
	}
	if _, err := handoffLocal(context.Background(), cfg, hc, out["agent"]); err != nil {
		t.Fatal(err)
	}
	if !seenMarked {
		t.Fatal("交接窗口内在途条目必须带 HandoffAt 标记")
	}
	entries, _ := inflight.Load(cfg.InflightPath())
	if len(entries) != 1 || !entries[0].HandoffAt.IsZero() {
		t.Fatalf("交接完成后新条目不应保留标记: %+v", entries)
	}
}

func TestHandoffKeepsLedgerAcrossMultipleHandoffs(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	out, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ledger := out["ledger"]
	if err := os.WriteFile(out["progress"], []byte("状态：一半"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := handoffLocal(context.Background(), cfg, hc, out["agent"])
	if err != nil {
		t.Fatal(err)
	}
	if first["ledger"] != ledger {
		t.Fatalf("first handoff ledger=%q, want %q", first["ledger"], ledger)
	}
	if _, err := handoffLocal(context.Background(), cfg, hc, first["agent"]); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cfg.ActiveDir())
	if len(entries) != 1 {
		t.Fatalf("连续 handoff 不该新增台账: %v", entries)
	}
	data, _ := os.ReadFile(ledger)
	if strings.Count(string(data), "交接：") != 2 {
		t.Fatalf("台账应有两笔交接记录:\n%s", data)
	}
	inflightEntries, _ := inflight.Load(cfg.InflightPath())
	if len(inflightEntries) != 1 || inflightEntries[0].Ledger != ledger {
		t.Fatalf("在途条目应沿用台账路径: %+v", inflightEntries)
	}
}
