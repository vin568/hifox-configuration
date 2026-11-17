package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/boot"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/config"
	feishu "apifox.github.com/vin568/hifox-configuration/super-agent/internal/feishu"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/halt"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/herdr"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/hifox"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/inflight"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/inject"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/outbox"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/tick"
)

// testRepo 造一个最小仓库：能力注册表（含一个 role=job 的本地能力）+ config/ 云端指针。
func testRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("super-agent/capabilities/agents/songjiang/capability.yaml", "key: songjiang\nname: 宋江\nrole: master\nworkspace: master\nkind: omp\n")
	write("super-agent/capabilities/agents/songjiang/instructions.md", "手册")
	write("super-agent/capabilities/agents/worker/capability.yaml",
		"key: worker\nname: 工人\ndescription: 测试用本地能力\nrole: job\nworkspace: jobs\nkind: omp\nmaxConcurrent: 1\n")
	write("super-agent/capabilities/agents/worker/instructions.md", "工人手册")
	write("super-agent/capabilities/cloud/daizong.yaml", "key: daizong\nname: 戴宗\nspace: HiFox\nnote: 验收\n")
	write("config/hifox/agents/daizong/agent.yaml", "key: daizong\nname: 戴宗\n")
	return root
}

// fastFresh 把换脑等待压到毫秒级（默认 30s，失败用例会拖死测试）。
func fastFresh(t *testing.T) {
	t.Helper()
	oldT, oldP := boot.FreshTimeout, boot.FreshPoll
	boot.FreshTimeout, boot.FreshPoll = 50*time.Millisecond, time.Millisecond
	t.Cleanup(func() { boot.FreshTimeout, boot.FreshPoll = oldT, oldP })
}

func testCfg(t *testing.T, repo string) config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Session = "t"
	cfg.StateDir = dir
	cfg.RepoDir = repo
	cfg.MasterAgent = "songjiang"
	cfg.RetroAgent = ""
	cfg.AdminChatID = "oc_admin"
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// dispatchFake 覆盖 dispatch/cancel 需要的 herdr 面：workspace、snapshot、agent list/start、pane 操作。
type dispatchFake struct {
	agents   []herdr.Agent
	started  []string
	prompts  []string
	closed   []string
	paneSeq  int
	startErr error
}

func (f *dispatchFake) run(_ context.Context, args []string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "workspace list":
		return `{"result":{"workspaces":[{"workspace_id":"w1","label":"master"},{"workspace_id":"w3","label":"jobs"}]}}`, nil
	case "api snapshot":
		return `{"result":{"snapshot":{"layouts":[{"workspace_id":"w1","panes":[{"pane_id":"w1:p1"}]},{"workspace_id":"w3","panes":[{"pane_id":"w3:p1"}]}]}}}`, nil
	case "agent list":
		agents := make([]string, 0, len(f.agents))
		for _, a := range f.agents {
			agents = append(agents, `{"agent":"omp","name":"`+a.Name+`","agent_status":"`+a.AgentStatus+`","pane_id":"`+a.PaneID+`"}`)
		}
		return `{"result":{"agents":[` + strings.Join(agents, ",") + `]}}`, nil
	case "pane split":
		f.paneSeq++
		return `{"result":{"pane":{"pane_id":"w3:p` + string(rune('1'+f.paneSeq)) + `"}}}`, nil
	case "agent start":
		if f.startErr != nil {
			return "", f.startErr
		}
		f.started = append(f.started, args[2])
		name := args[2]
		pane := ""
		for i, a := range args {
			if a == "--pane" && i+1 < len(args) {
				pane = args[i+1]
			}
		}
		f.agents = append(f.agents, herdr.Agent{Name: name, AgentStatus: "idle", PaneID: pane})
		return `{"result":{"agent":{"agent":"omp","agent_status":"idle"}}}`, nil
	case "agent prompt":
		f.prompts = append(f.prompts, args[3])
		return `{}`, nil
	case "pane close":
		f.closed = append(f.closed, args[2])
		return `{"result":{"type":"ok"}}`, nil
	}
	return "", nil
}

func TestDispatchLocalStartsRegistersAndDraftsLedger(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	if _, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "worker", "跑一遍", "测试备注", nil); err != nil {
		t.Fatal(err)
	}
	if len(f.started) != 1 || !strings.HasPrefix(f.started[0], "worker-") {
		t.Fatalf("应按注册表配方起一个本地 agent: %v", f.started)
	}
	entries, err := inflight.Load(cfg.InflightPath())
	if err != nil || len(entries) != 1 {
		t.Fatalf("应自动登记在途清单: %+v err=%v", entries, err)
	}
	if entries[0].Kind != "local" || entries[0].Agent != f.started[0] {
		t.Fatalf("在途清单条目不对: %+v", entries[0])
	}
	if len(f.prompts) != 1 || !strings.Contains(f.prompts[0], cfg.ResultsDir()) {
		t.Fatalf("注入任务里应带结果路径: %v", f.prompts)
	}
	files, _ := os.ReadDir(cfg.ActiveDir())
	if len(files) != 1 {
		t.Fatalf("应建一个台账骨架: %v", files)
	}
}

func TestDispatchLocalRespectsMaxConcurrent(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	for i := 0; i < 1; i++ {
		if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: "local", Agent: "worker-old"}); err != nil {
			t.Fatal(err)
		}
	}
	f := &dispatchFake{}
	_, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "worker", "再派一件", "", nil)
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("并发上限应拦住第二次派活: %v", err)
	}
	if len(f.started) != 0 {
		t.Fatalf("被拦住时不应起 agent: %v", f.started)
	}
}

func TestDispatchLocalUnknownAgent(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	_, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "nobody", "x", "", nil)
	if err == nil || !strings.Contains(err.Error(), "没有可派活的 agent") {
		t.Fatalf("未声明的能力必须拒绝: %v", err)
	}
}

// 台架：dispatch → 在途清单 → cancel 关 pane 并移出清单。
func TestCancelLocalClosesPaneAndDeregisters(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	hc := &herdr.Client{Run: f.run}
	if _, err := dispatchLocal(context.Background(), cfg, hc, "worker", "跑一遍", "", nil); err != nil {
		t.Fatal(err)
	}
	entries, _ := inflight.Load(cfg.InflightPath())
	if len(entries) != 1 {
		t.Fatalf("前置失败: %+v", entries)
	}
	agent, pane := entries[0].Agent, entries[0].Pane
	if _, err := cancelLocal(context.Background(), cfg, hc, agent); err != nil {
		t.Fatal(err)
	}
	if len(f.closed) != 1 || f.closed[0] != pane {
		t.Fatalf("应关掉该 agent 的 pane: %v (want %s)", f.closed, pane)
	}
	after, _ := inflight.Load(cfg.InflightPath())
	if len(after) != 0 {
		t.Fatalf("取消后应移出在途清单: %+v", after)
	}
}

// 闸门（ADR 0020）：急停期间机器注入全拦，用户私聊放行并解除。
// 另：落闸前就排队的旧指令（重试队列里那条）必须丢弃——它正是急停要拦的。
func TestGateBlocksMachineAndUserClearsHalt(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	g := gate{cfg: cfg}
	if !g.allowMachine() {
		t.Fatal("未急停时机器注入应放行")
	}
	if err := halt.Set(cfg.HaltPath(), halt.State{By: "ou_alice"}); err != nil {
		t.Fatal(err)
	}
	if g.allowMachine() {
		t.Fatal("急停期间机器注入必须被拦")
	}
	if !g.admit(inject.Payload{Kind: "dm", ReceivedAt: time.Now()}) {
		t.Fatal("用户私聊应放行")
	}
	if halt.Active(cfg.HaltPath()) {
		t.Fatal("用户说话后急停应解除")
	}
	if !g.allowMachine() {
		t.Fatal("解除后机器注入应恢复")
	}
}

func TestGateDropsPreHaltQueuedMessage(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	g := gate{cfg: cfg}
	haltAt := time.Now()
	if err := halt.Set(cfg.HaltPath(), halt.State{At: haltAt, By: "ou_alice"}); err != nil {
		t.Fatal(err)
	}
	stale := inject.Payload{Kind: "dm", Text: "停前那条错误指令", ReceivedAt: haltAt.Add(-2 * time.Second)}
	if g.admit(stale) {
		t.Fatal("落闸前排队的旧指令必须丢弃")
	}
	if !halt.Active(cfg.HaltPath()) {
		t.Fatal("丢弃旧指令不应解除急停")
	}
	fresh := inject.Payload{Kind: "dm", Text: "停 2 号和 3 号", ReceivedAt: haltAt.Add(time.Second)}
	if !g.admit(fresh) {
		t.Fatal("急停后用户新说的话应放行")
	}
	if halt.Active(cfg.HaltPath()) {
		t.Fatal("新消息放行后急停应解除")
	}
	// 解除之后，停前排队的另一条重试仍必须被丢（水位窗口内）。
	lateRetry := inject.Payload{Kind: "dm", Text: "停前那条", ReceivedAt: haltAt.Add(-time.Second)}
	if g.admit(lateRetry) {
		t.Fatal("解除后窗口内，落闸前排队的旧指令仍必须丢弃")
	}
}

// 坏 halt 文件按已挂闸（fail-closed），但用户私聊可将其重置并放行。
func TestGateCorruptHaltResetsOnUserMessage(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	g := gate{cfg: cfg}
	if err := os.WriteFile(cfg.HaltPath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if g.admit(inject.Payload{Kind: "group", ChatID: "oc_g"}) {
		t.Fatal("坏状态也必须继续拦机器/群注入")
	}
	if !g.admit(inject.Payload{Kind: "dm", ReceivedAt: time.Now()}) {
		t.Fatal("用户私聊应能重置坏状态")
	}
	if halt.Active(cfg.HaltPath()) {
		t.Fatal("重置后闸门应已清")
	}
}

// 急停期间群/代答消息一律静默。
func TestGateSilencesGroupDuringHalt(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	g := gate{cfg: cfg}
	if err := halt.Set(cfg.HaltPath(), halt.State{At: time.Now(), By: "cli"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []inject.Payload{
		{Kind: "group", ChatID: "oc_g"},
		{Kind: "dm", ProxyReply: true, ChatID: "oc_g"},
	} {
		if g.admit(p) {
			t.Fatalf("急停期间这些注入必须静默: %+v", p)
		}
	}
}

// 机器真值给主控：在途清单 + 台账 + jobs agent（pane 也带上）；
// 给用户的那份必须只剩人话（一体人设：不出口 pane id / 文件名）。
func TestTruthMasterVsUser(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: "cloud", TaskID: "HF-1", Note: "云端活"}); err != nil {
		t.Fatal(err)
	}
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: "local", Agent: "worker-1", Pane: "w3:p1", Note: "跑一遍"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.ActiveDir(), "20261117-活.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &dispatchFake{agents: []herdr.Agent{{Name: "worker-1", AgentStatus: "working", PaneID: "w3:p1"}}}
	hc := &herdr.Client{Run: f.run}
	tr := collectTruth(context.Background(), cfg, hc)

	master := tr.forMaster(cfg)
	for _, want := range []string{"HF-1", "云端活", "20261117-活.md", "worker-1", "w3:p1"} {
		if !strings.Contains(master, want) {
			t.Fatalf("主控视图缺 %q:\n%s", want, master)
		}
	}
	user := tr.forUser()
	for _, want := range []string{"HF-1", "云端活", "跑一遍", "活"} {
		if !strings.Contains(user, want) {
			t.Fatalf("用户视图缺 %q:\n%s", want, user)
		}
	}
	for _, leak := range []string{"w3:p1", ".md", "worker-1"} {
		if strings.Contains(user, leak) {
			t.Fatalf("用户视图泄漏内部标识 %q:\n%s", leak, user)
		}
	}
}

// 急停回执是给人看的人话：有在途就说清单，没有就说明白。
func TestHaltReceiptWording(t *testing.T) {
	if got := haltReceipt(""); !strings.Contains(got, "没有正在跑的事") {
		t.Fatalf("空清单要说清楚: %s", got)
	}
	got := haltReceipt("- 云端任务 HF-1：验收")
	if !strings.Contains(got, "HF-1") || !strings.Contains(got, "要停哪些") {
		t.Fatalf("有在途要带清单并问要不要停: %s", got)
	}
}

// doHalt 的落闸顺序：先有闸门，再换脑；换脑失败也保持停住（fail-closed）。
func TestDoHaltKeepsGateWhenFreshFails(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	// workspace 列表为空 → ensure 会创建，但 agent start 直接失败 → 换脑失败。
	f := &dispatchFake{startErr: errors.New("boom")}
	hc := &herdr.Client{Run: func(ctx context.Context, args []string) (string, error) {
		if strings.Join(args[:2], " ") == "workspace create" {
			return `{"result":{"workspace":{"workspace_id":"w9","label":"master"},"root_pane":{"pane_id":"w9:p1"}}}`, nil
		}
		return f.run(ctx, args)
	}}
	ob, err := outbox.Open(cfg.OutboxPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doHalt(context.Background(), cfg, hc, ob, "ou_alice", "测试", "oc_admin"); err == nil {
		t.Fatal("换脑失败必须报错")
	}
	if !halt.Active(cfg.HaltPath()) {
		t.Fatal("换脑失败后闸门必须仍在（fail-closed）")
	}
}

// 并发上限按「这个能力」计，不是全局面板：两个不同能力互不占额度。
func TestDispatchLocalLimitIsPerCapability(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	// 另一个能力已占掉 3 个额度（超过 worker 的 maxConcurrent:1）。
	for i := 0; i < 3; i++ {
		if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: "local", Agent: "other-" + string(rune('a'+i)), Capability: "other"}); err != nil {
			t.Fatal(err)
		}
	}
	f := &dispatchFake{}
	if _, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "worker", "跑一遍", "", nil); err != nil {
		t.Fatalf("别的能力的在途件不应占 worker 的额度: %v", err)
	}
	if len(f.started) != 1 {
		t.Fatalf("应起一个 agent: %v", f.started)
	}
}

// 旧条目没有 capability 字段时用 agent 名前缀兜底认领，升级后额度不会突然清零。
func TestCountActiveLocalFallsBackToPrefix(t *testing.T) {
	entries := []inflight.Entry{
		{Kind: "local", Agent: "worker-101500"},
		{Kind: "local", Agent: "worker-101600", Capability: "worker"},
		{Kind: "local", Agent: "other-101700", Capability: "other"},
		{Kind: "cloud", TaskID: "HF-1"},
	}
	if got := countActiveLocal(entries, "worker"); got != 2 {
		t.Fatalf("countActiveLocal(worker) = %d, want 2", got)
	}
}

// 台账骨架只在文件不存在时建：主控已经写过内容，不能被派发覆盖。
func TestLedgerDraftNeverOverwrites(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	slug := "20261117-test-job"
	path := ledgerDraft(cfg, slug, "本地", "worker-1", "第一次")
	if path == "" {
		t.Fatal("首建失败")
	}
	if err := os.WriteFile(path, []byte("主控写的现场记录\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := ledgerDraft(cfg, slug, "本地", "worker-1", "第二次")
	if again != path {
		t.Fatalf("应返回同一路径: %q vs %q", again, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "主控写的现场记录\n" {
		t.Fatalf("已存在的台账被覆盖:\n%s", data)
	}
}

// /new 必须换脑并解除急停（用户下一句私聊即恢复，/new 也是一句话）。
func TestNewAbsorbsAndClearsHalt(t *testing.T) {
	fastFresh(t)
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	cfg.MasterAgent = "songjiang"
	if err := halt.Set(cfg.HaltPath(), halt.State{By: "cli"}); err != nil {
		t.Fatal(err)
	}
	f := &freshFakeForCommands{}
	ob, err := outbox.Open(cfg.OutboxPath())
	if err != nil {
		t.Fatal(err)
	}
	idx := commandIndices{cfg: cfg, hc: &herdr.Client{Run: f.run}, ob: ob}
	if !idx.absorb(context.Background(), "oc_dm", "/new") {
		t.Fatal("/new 应被消化")
	}
	if halt.Active(cfg.HaltPath()) {
		t.Fatal("/new 之后急停应解除")
	}
	if len(f.started) == 0 {
		t.Fatal("/new 应真的起了新会话")
	}
	if !strings.Contains(f.prompted, "手册") {
		t.Fatalf("新会话首条注入应是主控手册: %q", f.prompted)
	}
}

// 未识别的斜杠指令交回正常注入路径。
func TestCommandUnknownFallsThrough(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	ob, err := outbox.Open(cfg.OutboxPath())
	if err != nil {
		t.Fatal(err)
	}
	idx := commandIndices{cfg: cfg, hc: &herdr.Client{Run: (&freshFakeForCommands{}).run}, ob: ob}
	if idx.absorb(context.Background(), "oc_dm", "/deploy 一下") {
		t.Fatal("未知指令不该被消化")
	}
}

// freshFakeForCommands 是命令测试用的最小 herdr：拓扑齐全、每次 start 换新会话、prompt 记账。
type freshFakeForCommands struct {
	started  []string
	prompted string
}

func (f *freshFakeForCommands) startedNamed(name string) bool {
	for _, n := range f.started {
		if n == name {
			return true
		}
	}
	return false
}

func (f *freshFakeForCommands) run(_ context.Context, args []string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "workspace list":
		return `{"result":{"workspaces":[{"workspace_id":"w1","label":"master"},{"workspace_id":"w2","label":"crew"},{"workspace_id":"w3","label":"jobs"}]}}`, nil
	case "api snapshot":
		return `{"result":{"snapshot":{"layouts":[{"workspace_id":"w1","panes":[{"pane_id":"w1:p1"}]},{"workspace_id":"w2","panes":[{"pane_id":"w2:p1"}]},{"workspace_id":"w3","panes":[{"pane_id":"w3:p1"}]}]}}}`, nil
	case "agent list":
		var parts []string
		if f.startedNamed("songjiang") {
			parts = append(parts, `{"agent":"omp","name":"songjiang","agent_status":"idle","pane_id":"w1:p1","agent_session":{"value":"/sessions/master-after.jsonl"}}`)
		}
		if f.startedNamed("jiangjing") {
			parts = append(parts, `{"agent":"omp","name":"jiangjing","agent_status":"idle","pane_id":"w2:p1","agent_session":{"value":"/sessions/retro-after.jsonl"}}`)
		}
		return `{"result":{"agents":[` + strings.Join(parts, ",") + `]}}`, nil
	case "workspace create":
		return "", nil
	case "agent start":
		f.started = append(f.started, args[2])
		return `{"result":{"agent":{"agent":"omp","agent_status":"idle"}}}`, nil
	case "agent prompt":
		f.prompted = args[3]
		return `{}`, nil
	case "agent send-keys":
		return `{"result":{"type":"ok"}}`, nil
	}
	return "", nil
}

// 蒋敬换脑必须拿自己的手册，不能拿主控的（否则它按「编排者」行事）。
func TestFreshBrainUsesRetroManual(t *testing.T) {
	fastFresh(t)
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	// testRepo 只有一个 role=master；补一个 retro 条目。
	writeCap(t, repo, "super-agent/capabilities/agents/jiangjing/capability.yaml",
		"key: jiangjing\nname: 蒋敬\nrole: retro\nworkspace: crew\nkind: omp\n")
	writeCap(t, repo, "super-agent/capabilities/agents/jiangjing/instructions.md", "蒋敬手册正文")
	cfg.RetroAgent = "jiangjing"

	f := &freshFakeForCommands{}
	if _, err := freshBrain(context.Background(), cfg, &herdr.Client{Run: f.run}, "jiangjing", "rotate", "复盘意图"); err != nil {
		t.Fatalf("freshBrain: %v", err)
	}
	if !strings.Contains(f.prompted, "蒋敬手册正文") {
		t.Fatalf("蒋敬换脑应拿自己的手册: %q", f.prompted)
	}
	if strings.Contains(f.prompted, "宋江") {
		t.Fatalf("蒋敬换脑不该拿到主控手册: %q", f.prompted)
	}
	if !strings.Contains(f.prompted, "复盘意图") {
		t.Fatalf("附加意图应保留: %q", f.prompted)
	}
}

func writeCap(t *testing.T, repo, rel, body string) {
	t.Helper()
	p := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 轮换：阈值以下不换；阈值以上且 idle 才换；working 时不动；阈值 0 关闭。
func TestMaybeRotateMatrix(t *testing.T) {
	cases := []struct {
		name      string
		maxBytes  int64
		status    string
		sessionSz int
		wantFresh bool
	}{
		{"阈值 0 关闭", 0, "idle", 99999, false},
		{"阈值以下", 1 << 20, "idle", 10, false},
		{"超阈值但 working", 10, "working", 999, false},
		{"超阈值且 idle", 10, "idle", 999, true},
		{"超阈值且 done", 10, "done", 999, true},
	}
	fastFresh(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := testRepo(t)
			cfg := testCfg(t, repo)
			cfg.SessionMaxBytes = c.maxBytes
			sess := filepath.Join(t.TempDir(), "s.jsonl")
			if err := os.WriteFile(sess, make([]byte, c.sessionSz), 0o644); err != nil {
				t.Fatal(err)
			}
			f := &rotateFake{status: c.status, session: sess}
			if err := maybeRotate(context.Background(), cfg, &herdr.Client{Run: f.run}); err != nil {
				t.Fatalf("maybeRotate: %v", err)
			}
			if f.freshCalled != c.wantFresh {
				t.Fatalf("freshCalled=%v, want %v", f.freshCalled, c.wantFresh)
			}
		})
	}
}

// rotateFake 记录是否发生了换脑（agent start）。
type rotateFake struct {
	status      string
	session     string
	freshCalled bool
	gone        bool
	started     []string
}

func (f *rotateFake) run(_ context.Context, args []string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "workspace list":
		return `{"result":{"workspaces":[{"workspace_id":"w1","label":"master"},{"workspace_id":"w2","label":"crew"},{"workspace_id":"w3","label":"jobs"}]}}`, nil
	case "api snapshot":
		return `{"result":{"snapshot":{"layouts":[{"workspace_id":"w1","panes":[{"pane_id":"w1:p1"}]},{"workspace_id":"w2","panes":[{"pane_id":"w2:p1"}]},{"workspace_id":"w3","panes":[{"pane_id":"w3:p1"}]}]}}}`, nil
	case "agent list":
		if f.gone {
			return `{"result":{"agents":[]}}`, nil
		}
		sess := f.session
		if f.freshCalled {
			sess = "/sessions/after.jsonl"
		}
		return `{"result":{"agents":[{"agent":"omp","name":"songjiang","agent_status":"` + f.status + `","pane_id":"w1:p1","agent_session":{"value":"` + sess + `"}}]}}`, nil
	case "workspace create":
		return "", nil
	case "agent start":
		f.started = append(f.started, args[2])
		f.freshCalled = true
		f.gone = false
		return `{"result":{"agent":{"agent":"omp","agent_status":"idle"}}}`, nil
	case "agent prompt":
		return `{}`, nil
	case "agent send-keys":
		f.gone = true
		return `{"result":{"type":"ok"}}`, nil
	case "pane close":
		f.gone = true
		return `{"result":{"type":"ok"}}`, nil
	}
	return "", nil
}

// 云端派活：注册表 key → 平台 ref 的解析（歧义拒绝、实例可指定、显式 delegate 直通）。
func TestDispatchCloudResolvesPlatformRef(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	agentsJSON := `[{"id":"id-prod","name":"戴宗 (prod)"},{"id":"id-staging","name":"戴宗 (staging)"}]`
	var createdArgs []string
	stub := &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		switch strings.Join(args[:2], " ") {
		case "agent list":
			return agentsJSON, nil
		case "task create":
			createdArgs = args
			return `{"id":"uuid-1","identifier":"HF-9","title":"验收"}`, nil
		}
		return "", nil
	}}

	if _, err := dispatchCloud(context.Background(), cfg, func(string) *hifox.Client { return stub }, cloudReq{Title: "验收", AgentKey: "daizong"}, nil); err == nil {
		t.Fatal("实例歧义时必须拒绝，而不是随便挑一个")
	}

	out, err := dispatchCloud(context.Background(), cfg, func(string) *hifox.Client { return stub }, cloudReq{Title: "验收", AgentKey: "daizong", Instance: "staging"}, nil)
	if err != nil {
		t.Fatalf("指定实例后应成功: %v", err)
	}
	if out["task_id"] != "HF-9" || out["delegate"] != "id-staging" {
		t.Fatalf("派活输出不对: %+v", out)
	}
	if !strings.Contains(strings.Join(createdArgs, " "), "--delegate-agent id-staging") {
		t.Fatalf("hifox 调用应带解析后的平台 ref: %v", createdArgs)
	}
	entries, _ := inflight.Load(cfg.InflightPath())
	if len(entries) != 1 || entries[0].TaskID != "HF-9" {
		t.Fatalf("应自动登记在途清单: %+v", entries)
	}

	// 显式 --delegate 跳过解析（连 agent list 都不该查）。
	noList := &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		if strings.Join(args[:2], " ") == "agent list" {
			t.Fatal("显式 delegate 时不应查 agent list")
		}
		return `{"id":"uuid-2","identifier":"HF-10"}`, nil
	}}
	if _, err := dispatchCloud(context.Background(), cfg, func(string) *hifox.Client { return noList }, cloudReq{Title: "t", AgentKey: "daizong", Delegate: "戴宗 (staging)"}, nil); err != nil {
		t.Fatalf("显式 delegate 应直通: %v", err)
	}
}

// 云端派活：未在注册表声明的能力 key 必须拒绝（避免拼错 key 静默派错）。
func TestDispatchCloudUnknownCapability(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	_, err := dispatchCloud(context.Background(), cfg, func(string) *hifox.Client {
		return &hifox.Client{Run: func(context.Context, []string) (string, error) { return "", nil }}
	}, cloudReq{Title: "t", AgentKey: "nobody"}, nil)
	if err == nil || !strings.Contains(err.Error(), "没有云端能力") {
		t.Fatalf("未声明的云端能力必须拒绝: %v", err)
	}
}

// 云端撤销：有在跑/排队的 run 就取消并移出清单；没有就只移出并如实说明。
func TestCancelCloud(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: "cloud", TaskID: "HF-9"}); err != nil {
		t.Fatal(err)
	}
	var cancelled string
	stub := &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		switch strings.Join(args[:2], " ") {
		case "run list":
			return `[{"id":"run-1","status":"running"}]`, nil
		case "task cancel-run":
			cancelled = args[2] + "/" + args[3]
			return `{}`, nil
		}
		return "", nil
	}}
	out, err := cancelCloud(context.Background(), cfg, stub, "HF-9")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled != "HF-9/run-1" || out["cancelled"] != true {
		t.Fatalf("应取消在跑的 run: cancelled=%q out=%+v", cancelled, out)
	}
	if entries, _ := inflight.Load(cfg.InflightPath()); len(entries) != 0 {
		t.Fatalf("取消后应移出在途清单: %+v", entries)
	}

	// 无活跃 run：不调 cancel-run，只移出并说明。
	quiet := &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		if strings.Join(args[:2], " ") == "run list" {
			return `[{"id":"run-0","status":"succeeded"}]`, nil
		}
		t.Fatalf("无活跃 run 不该调 %v", args)
		return "", nil
	}}
	out2, err := cancelCloud(context.Background(), cfg, quiet, "HF-404")
	if err != nil {
		t.Fatal(err)
	}
	if out2["cancelled"] != false {
		t.Fatalf("无活跃 run 不应谎报取消: %+v", out2)
	}
}

// 注入卫生的判据接线（F-5）：三源计数都来自真实状态，任一非空才注入。
func TestSweepSignalsCountsThreeSources(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	f := &dispatchFake{agents: []herdr.Agent{{Name: "worker-1", AgentStatus: "working", PaneID: "w3:p1"}}}
	hc := &herdr.Client{Run: f.run}
	if sig := sweepSignals(context.Background(), cfg, hc); sig.ActiveJobs != 0 || sig.Inflight != 0 || sig.JobsAgents != 1 {
		t.Fatalf("初始信号不对: %+v", sig)
	}
	if err := os.WriteFile(filepath.Join(cfg.ActiveDir(), "20261117-a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := inflight.Add(cfg.InflightPath(), inflight.Entry{Kind: "cloud", TaskID: "HF-1"}); err != nil {
		t.Fatal(err)
	}
	sig := sweepSignals(context.Background(), cfg, hc)
	if sig.ActiveJobs != 1 || sig.Inflight != 1 || sig.JobsAgents != 1 {
		t.Fatalf("三源都应被计数: %+v", sig)
	}
	if !tick.Needed(sig) {
		t.Fatal("有内容时必须巡检")
	}
}

// serve 层的注入分流（F-1）：急停时群消息与 stale 私聊都不进主控，新私聊进并解闸；
// 哨兵在急停期只延后、不注入。
func TestFeishuAndSentinelInjectorsRespectGate(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	cfg.MasterAgent = "songjiang"
	f := &freshFakeForCommands{}
	hc := &herdr.Client{Run: f.run}
	g := gate{cfg: cfg}
	haltAt := time.Now()
	if err := halt.Set(cfg.HaltPath(), halt.State{At: haltAt, By: "ou_alice"}); err != nil {
		t.Fatal(err)
	}

	inj := feishuInjector(cfg, hc, g, nil)
	if err := inj(context.Background(), feishu.SourceFeishu, inject.Payload{Kind: "group", ChatID: "oc_g", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if f.prompted != "" {
		t.Fatalf("急停期间群消息不该进主控: %q", f.prompted)
	}
	stale := inject.Payload{Kind: "dm", ChatID: "oc_dm", Text: "停前那条", ReceivedAt: haltAt.Add(-time.Second)}
	if err := inj(context.Background(), feishu.SourceFeishu, stale); err != nil {
		t.Fatal(err)
	}
	if f.prompted != "" || !halt.Active(cfg.HaltPath()) {
		t.Fatalf("停前重试不该进主控也不该解闸: prompted=%q active=%v", f.prompted, halt.Active(cfg.HaltPath()))
	}
	fresh := inject.Payload{Kind: "dm", ChatID: "oc_dm", Text: "停 2 号", ReceivedAt: haltAt.Add(time.Second)}
	if err := inj(context.Background(), feishu.SourceFeishu, fresh); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.prompted, "停 2 号") || halt.Active(cfg.HaltPath()) {
		t.Fatalf("新私聊应进主控并解闸: prompted=%q active=%v", f.prompted, halt.Active(cfg.HaltPath()))
	}

	// 哨兵：急停期返回 errDeferredInject，恢复后正常注入。
	if err := halt.Set(cfg.HaltPath(), halt.State{At: time.Now(), By: "cli"}); err != nil {
		t.Fatal(err)
	}
	sen := sentinelInjector(cfg, hc, g, nil)
	if err := sen("变化"); !errors.Is(err, errDeferredInject) {
		t.Fatalf("急停期间哨兵应延迟注入，得到 %v", err)
	}
	if err := halt.Release(cfg.HaltPath(), halt.Watermark{HaltAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	f.prompted = ""
	if err := sen("变化"); err != nil {
		t.Fatalf("恢复后哨兵应正常注入: %v", err)
	}
	if !strings.Contains(f.prompted, "变化") {
		t.Fatalf("哨兵注入未到达: %q", f.prompted)
	}
}

// /halt 指令走的是同一条急停实现：落闸 + 换脑 + 回执。
func TestSlashHaltAbsorbs(t *testing.T) {
	fastFresh(t)
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	cfg.MasterAgent = "songjiang"
	f := &freshFakeForCommands{}
	ob, err := outbox.Open(cfg.OutboxPath())
	if err != nil {
		t.Fatal(err)
	}
	idx := commandIndices{cfg: cfg, hc: &herdr.Client{Run: f.run}, ob: ob}
	if !idx.absorb(context.Background(), "oc_dm", "/halt 派错了") {
		t.Fatal("/halt 应被消化")
	}
	if !halt.Active(cfg.HaltPath()) {
		t.Fatal("/halt 之后闸门必须挂上")
	}
	st, _, err := halt.Load(cfg.HaltPath())
	if err != nil || st.Reason != "派错了" {
		t.Fatalf("原因应写进状态: %+v err=%v", st, err)
	}
	if len(f.started) == 0 || !strings.Contains(f.prompted, "急停") {
		t.Fatalf("应换脑并注入急停意图: started=%v prompted=%q", f.started, f.prompted)
	}
}

// 本地派发先补拓扑（F-12）：cancel 关掉 jobs 最后 pane 后，下一步派发必须自己把容器补回来。
func TestDispatchLocalRunsEnsureFirst(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "superctl.toml")
	toml := "session = \"t\"\nstate_dir = \"" + cfg.StateDir + "\"\nrepo_dir = \"" + repo + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	old := ensureTopoFn
	ensureTopoFn = func(context.Context, config.Config, *herdr.Client) (*boot.Report, error) {
		called = true
		return nil, errors.New("ensure called")
	}
	defer func() { ensureTopoFn = old }()

	err := cmdDispatch([]string{"--local", "--agent", "worker", "--body", "跑一遍", "--config", cfgPath})
	if !called || err == nil || !strings.Contains(err.Error(), "ensure called") {
		t.Fatalf("本地派发必须先走 ensure：called=%v err=%v", called, err)
	}
}

// --space 缺省时用声明里的 space（注册表已经知道它在哪个 Space）。
func TestDispatchCloudDefaultsSpaceFromDeclaration(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	// testRepo 的 cloud 指针没有 space 字段，补一个。
	writeCap(t, repo, "super-agent/capabilities/cloud/daizong.yaml",
		"key: daizong\nname: 戴宗\nspace: HF\nnote: 验收\n")
	var created []string
	stub := &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		switch strings.Join(args[:2], " ") {
		case "agent list":
			return `[{"id":"id-s","name":"戴宗 (staging)"}]`, nil
		case "task create":
			created = args
			return `{"identifier":"HF-1"}`, nil
		}
		return "", nil
	}}
	if _, err := dispatchCloud(context.Background(), cfg, func(string) *hifox.Client { return stub }, cloudReq{Title: "t", AgentKey: "daizong"}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(created, " "), "--space HF") {
		t.Fatalf("应默认带上声明的 space: %v", created)
	}
}

// CLI 面：位置参数不再被静默忽略（`capabilities check` 曾打印索引、退出 0）。
func TestCapabilitiesRejectsPositionalArgs(t *testing.T) {
	repo := testRepo(t)
	cfg0 := testCfg(t, repo)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "superctl.toml")
	if err := os.WriteFile(cfgPath, []byte("session = \"t\"\nstate_dir = \""+cfg0.StateDir+"\"\nrepo_dir = \""+repo+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdCapabilities([]string{"check", "--config", cfgPath}); err == nil || !strings.Contains(err.Error(), "不接受位置参数") {
		t.Fatalf("位置参数必须报错: %v", err)
	}
	if err := cmdCapabilities([]string{"--check", "--config", cfgPath}); err != nil {
		t.Fatalf("--check 应正常: %v", err)
	}
}

// 已下线键：值一致 → 放行（存量配置平滑升级）；值不一致 → 硬报错（有人以为它还在生效）。
func TestLegacyAgentKeysMustMatchRegistry(t *testing.T) {
	repo := testRepo(t)
	dir := t.TempDir()
	writeCfg := func(extra string) string {
		p := filepath.Join(dir, "superctl.toml")
		body := "session = \"t\"\nstate_dir = \"" + dir + "/state\"\nrepo_dir = \"" + repo + "\"\n" + extra
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, _, err := loadConfigWithAgents([]string{"--config", writeCfg("master_agent = \"songjiang\"\n")}); err != nil {
		t.Fatalf("值一致应放行: %v", err)
	}
	if _, _, err := loadConfigWithAgents([]string{"--config", writeCfg("master_agent = \"oldname\"\n")}); err == nil || !strings.Contains(err.Error(), "不再生效") {
		t.Fatalf("值不一致必须报错: %v", err)
	}
}
