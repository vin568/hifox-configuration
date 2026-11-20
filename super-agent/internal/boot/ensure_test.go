package boot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"super-agent/internal/herdr"
)

type fakeHerdr struct {
	workspaces string
	agents     string
	snapshot   string
	created    []string
	closed     []string
	started    []string
	prompted   []string
	failPrompt int   // 前 N 次 agent prompt 报错（模拟 prime 失败）
	startErr   error // agent start 直接失败（模拟启动期 blocked）
}

func (f *fakeHerdr) run(_ context.Context, args []string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "agent list":
		// 冷启动时 f.agents 是空列表；把本次已 start 的 agent 合进去，
		// 否则 prime 送达校验找不到会话，会误判失败。
		base := strings.TrimSpace(f.agents)
		if base == "" {
			base = `{"result":{"agents":[]}}`
		}
		inner := strings.TrimSuffix(base, `]}}`)
		entries := ""
		for i, n := range f.started {
			if i > 0 {
				entries += ","
			}
			entries += `{"agent":"omp","name":"` + n + `","agent_status":"idle","pane_id":"w1:p1"}`
		}
		if entries != "" {
			if strings.HasSuffix(inner, "[") {
				inner += entries
			} else {
				inner += "," + entries
			}
		}
		return inner + `]}}`, nil
	case "workspace list":
		return f.workspaces, nil
	case "api snapshot":
		if f.snapshot == "" {
			f.snapshot = `{"result":{"snapshot":{"layouts":[]}}}`
		}
		return f.snapshot, nil
	case "workspace close":
		f.closed = append(f.closed, args[2])
		return `{"result":{"type":"ok"}}`, nil
	case "workspace create":
		label := ""
		for i, a := range args {
			if a == "--label" && i+1 < len(args) {
				label = args[i+1]
			}
		}
		f.created = append(f.created, label)
		id := "w" + string(rune('0'+len(f.created)+10))
		return `{"result":{"workspace":{"workspace_id":"` + id + `","label":"` + label + `"},"root_pane":{"pane_id":"` + id + `:p1"}}}`, nil
	case "agent start":
		if f.startErr != nil {
			return "", f.startErr
		}
		f.started = append(f.started, args[2])
		return `{"result":{"agent":{"agent":"` + args[2] + `","agent_status":"idle"}}}`, nil
	case "agent prompt":
		if f.failPrompt > 0 {
			f.failPrompt--
			return "", &herdr.CmdError{Code: "agent_blocked", Message: "blocked"}
		}
		f.prompted = append(f.prompted, args[3])
		return `{}`, nil
	}
	return "", nil
}

func client(f *fakeHerdr) *herdr.Client { return &herdr.Client{Run: f.run} }

func fullTopo() (string, string) {
	ws := `{"result":{"workspaces":[
		{"workspace_id":"w11","label":"master"},{"workspace_id":"w12","label":"crew"},{"workspace_id":"w13","label":"jobs"}]}}`
	snap := `{"result":{"snapshot":{"layouts":[
		{"workspace_id":"w11","panes":[{"pane_id":"w11:p1"}]},
		{"workspace_id":"w12","panes":[{"pane_id":"w12:p1"}]},
		{"workspace_id":"w13","panes":[{"pane_id":"w13:p1"}]}]}}}`
	return ws, snap
}

func fullAgents() string {
	return `{"result":{"agents":[{"agent":"omp","name":"songjiang","agent_status":"idle","pane_id":"w11:p1"},{"agent":"omp","name":"jiangjing","agent_status":"idle","pane_id":"w12:p1"}]}}`
}

func exp(markerDir string) Expected {
	return Expected{RepoDir: "/repo", MasterAgent: "songjiang", RetroAgent: "jiangjing",
		Kind: "omp", DynamicWS: "jobs", MasterPrime: "prime-master", RetroPrime: "prime-retro",
		PrimeMarkerDir: markerDir}
}

func TestEnsureColdStart(t *testing.T) {
	f := &fakeHerdr{workspaces: `{"result":{"workspaces":[]}}`, agents: `{"result":{"agents":[]}}`}
	markers := t.TempDir()
	rep, err := Ensure(context.Background(), client(f), exp(markers))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 3 || f.created[0] != "master" || f.created[1] != "crew" || f.created[2] != "jobs" {
		t.Fatalf("workspaces created: %v", f.created)
	}
	if len(f.started) != 2 || f.started[0] != "songjiang" || f.started[1] != "jiangjing" {
		t.Fatalf("agents started: %v", f.started)
	}
	if len(f.prompted) != 2 || f.prompted[0] != "prime-master" || f.prompted[1] != "prime-retro" {
		t.Fatalf("primes: %v", f.prompted)
	}
	if rep.empty() || len(rep.Primed) != 2 {
		t.Fatalf("report: %+v", rep)
	}
	// prime 成功必须落标记。
	for _, name := range []string{"songjiang", "jiangjing"} {
		if !primeMarkerExists(markers, name) {
			t.Fatalf("marker missing for %s", name)
		}
	}
}

func TestEnsureIdempotent(t *testing.T) {
	ws, snap := fullTopo()
	markers := t.TempDir()
	markPrimed(t, markers, "songjiang", "jiangjing")
	f := &fakeHerdr{workspaces: ws, agents: fullAgents(), snapshot: snap}
	rep, err := Ensure(context.Background(), client(f), exp(markers))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 || len(f.started) != 0 || len(f.prompted) != 0 {
		t.Fatalf("expected zero side effects: created=%v started=%v prompted=%v", f.created, f.started, f.prompted)
	}
	if len(rep.AlreadyRunning) != 2 {
		t.Fatalf("report: %+v", rep)
	}
}

// 评审 P0 回归：agent 在跑但 prime 标记缺失（上次 prime 失败）→ 必须补 prime。
func TestEnsureReprimesWhenMarkerMissing(t *testing.T) {
	ws, snap := fullTopo()
	markers := t.TempDir()
	markPrimed(t, markers, "jiangjing") // 只有蒋敬有标记
	f := &fakeHerdr{workspaces: ws, agents: fullAgents(), snapshot: snap}
	rep, err := Ensure(context.Background(), client(f), exp(markers))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.started) != 0 {
		t.Fatalf("不应重启 agent: %v", f.started)
	}
	if len(f.prompted) != 1 || f.prompted[0] != "prime-master" {
		t.Fatalf("应补 prime songjiang: %v", f.prompted)
	}
	if len(rep.Primed) != 1 || len(rep.AlreadyRunning) != 1 {
		t.Fatalf("report: %+v", rep)
	}
}

// 评审 P0 回归：prime 失败后标记不落盘，下一轮 Ensure 重试 prime 而不是跳过。
func TestEnsurePrimeFailureRetriedOnNextRun(t *testing.T) {
	markers := t.TempDir()
	markPrimed(t, markers, "jiangjing")
	ws, snap := fullTopo()
	f := &fakeHerdr{workspaces: ws, agents: fullAgents(), snapshot: snap, failPrompt: 1}
	if _, err := Ensure(context.Background(), client(f), exp(markers)); err == nil {
		t.Fatal("prime 失败必须报错")
	}
	if primeMarkerExists(markers, "songjiang") {
		t.Fatal("失败的 prime 不得落标记")
	}
	f2 := &fakeHerdr{workspaces: ws, agents: fullAgents(), snapshot: snap}
	rep, err := Ensure(context.Background(), client(f2), exp(markers))
	if err != nil {
		t.Fatal(err)
	}
	if len(f2.prompted) != 1 || f2.prompted[0] != "prime-master" {
		t.Fatalf("第二轮应补 prime: %v", f2.prompted)
	}
	if len(rep.Primed) != 1 || !primeMarkerExists(markers, "songjiang") {
		t.Fatalf("report=%+v marker=%v", rep, primeMarkerExists(markers, "songjiang"))
	}
}

// workspace 已在但 agent 掉了：从 snapshot 的 root pane 重启 agent，不建 workspace。
func TestEnsureRestartAgentOnExistingWorkspace(t *testing.T) {
	ws, snap := fullTopo()
	markers := t.TempDir()
	markPrimed(t, markers, "jiangjing")
	f := &fakeHerdr{workspaces: ws, agents: `{"result":{"agents":[{"agent":"omp","name":"jiangjing","agent_status":"idle","pane_id":"w12:p1"}]}}`, snapshot: snap}
	rep, err := Ensure(context.Background(), client(f), exp(markers))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 {
		t.Fatalf("should not create workspaces: %v", f.created)
	}
	if len(f.started) != 1 || f.started[0] != "songjiang" || len(f.prompted) != 1 {
		t.Fatalf("started=%v prompted=%v", f.started, f.prompted)
	}
	if len(rep.AlreadyRunning) != 1 || rep.AlreadyRunning[0] != "jiangjing" {
		t.Fatalf("report: %+v", rep)
	}
}

// herdr 的 agent start 在启动期撞审批框时返回 agent_not_ready；这要翻译成
// 「需要用户先处理」，不能当普通启动失败让调用方重派。
func TestEnsureReportsStartupBlocked(t *testing.T) {
	ws, snap := fullTopo()
	f := &fakeHerdr{workspaces: ws, agents: `{"result":{"agents":[]}}`, snapshot: snap,
		startErr: &herdr.CmdError{Code: "agent_not_ready", Message: "agent blocked during startup"}}
	_, err := Ensure(context.Background(), client(f), exp(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "审批") {
		t.Fatalf("启动期 blocked 应提示需要用户处理: %v", err)
	}
}

func TestPrimePromptIncludesPaths(t *testing.T) {
	got := PrimePrompt("手册正文", Paths{CapabilitiesDir: "/cap", ActiveDir: "/active", ArchivedDir: "/archived",
		ResultsDir: "/results", BriefsDir: "/briefs", MediaDir: "/media",
		InflightPath: "/inflight", RuntimeCmd: "/opt/bin/superctl", Index: "## 平台能力\n- 戴宗：发布验收"})
	for _, want := range []string{PrimeSentinel, "手册正文", "/cap", "/active", "/archived", "/results",
		"/briefs", "任务正文", "/media", "消息附件",
		"/inflight", "superctl reply", "/opt/bin/superctl", "运行时命令", "## 平台能力", "戴宗"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prime prompt missing %q", want)
		}
	}
}

// 两个目录为空时那两行不打印：主控手册里出现空路径只会让它去读一个不存在的位置。
func TestPrimePromptOmitsEmptyBriefsAndMedia(t *testing.T) {
	got := PrimePrompt("手册", Paths{InflightPath: "/inflight"})
	for _, unwanted := range []string{"任务正文（", "消息附件："} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("空路径不该打印 %q:\n%s", unwanted, got)
		}
	}
}

// 运行时命令路径取不到（os.Executable 失败）时那段不打印，而不是留一行空路径。
func TestPrimePromptOmitsRuntimeCmdWhenUnknown(t *testing.T) {
	if got := PrimePrompt("手册", Paths{InflightPath: "/inflight"}); strings.Contains(got, "运行时命令") {
		t.Fatalf("路径为空时不该打印运行时命令段:\n%s", got)
	}
}

func markPrimed(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := writeFileHelper(dir, n); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFileHelper(dir, name string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte("primed\n"), 0o644)
}

// prime 送达校验：模拟 omp 启动窗口吞掉回车——文本停在输入框，补一次 Enter 才提交。
func TestPrimeAgentSubmitsPendingPrompt(t *testing.T) {
	fastPrime(t)
	session := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(session, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &primeFake{session: session, status: "idle", writeOnEnter: true}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	if err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{})); err != nil {
		t.Fatal(err)
	}
	if f.enter == 0 {
		t.Fatal("没有补发 Enter，输入框里的文本不会提交")
	}
	if !primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("送达后必须写 prime 标记")
	}
	marker, _ := os.ReadFile(filepath.Join(exp.PrimeMarkerDir, "songjiang"))
	if !strings.Contains(string(marker), session) {
		t.Fatalf("标记应记下会话路径: %q", marker)
	}
}

// 输入框卡住且补 Enter 也无效：不许写标记，否则下一轮永远不会重试。
func TestPrimeAgentFailsWhenPromptNeverLands(t *testing.T) {
	fastPrime(t)
	// 会话文件先不存在（新会话要等首次提交才落盘），且补 Enter 也不生效。
	session := filepath.Join(t.TempDir(), "session.jsonl")
	f := &primeFake{session: session, status: "idle", writeOnEnter: false}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	if err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{})); err == nil {
		t.Fatal("首条注入没进会话必须报错")
	}
	if primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("失败不得写 prime 标记")
	}
}

// omp 冷启动时 agent 会先出现在 agent list 里、prompt 还不能写：这类拒绝发生在
// 写输入之前，必须等它就绪后重试，而不是把首条注入丢掉。
func TestSubmitPromptRetriesWhileAgentStarts(t *testing.T) {
	fastPrime(t)
	session := filepath.Join(t.TempDir(), "session.jsonl")
	f := &primeFake{session: session, status: "idle", writeOnEnter: true, notReady: 3}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	if err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{})); err != nil {
		t.Fatal(err)
	}
	if f.notReady != 0 {
		t.Fatalf("应重试到 agent 接输入，剩余 notReady=%d", f.notReady)
	}
	if f.prompts != 4 {
		t.Fatalf("应 3 次就绪拒绝 + 1 次成功，实际 prompt %d 次", f.prompts)
	}
	if !primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("就绪后送达必须落标记")
	}
}

// agent 不在 agent list 里（名字错/根本没起）时不能白等满 90s 再报同一句错。
func TestSubmitPromptFailsFastWhenAgentAbsent(t *testing.T) {
	fastPrime(t)
	f := &primeFake{status: "absent", notReady: 1 << 30}
	_, err := SubmitPrompt(context.Background(), clientPrime(f), "songjiang", "文本", "SENTINEL")
	if err == nil {
		t.Fatal("agent 不存在必须报错")
	}
	if !herdr.IsNotReady(err) {
		t.Fatalf("应把原始拒绝透出给调用方: %v", err)
	}
	if f.prompts != 1 {
		t.Fatalf("agent 不在列表应立即失败，实际 prompt %d 次", f.prompts)
	}
}

// 已 working 说明文本多半进了输入框；omp 在首个 assistant 消息出现前不落会话文件。
// 落盘后必须用真会话路径写标记，且不许补 Enter。
func TestSubmitPromptWaitsForWorkingFlush(t *testing.T) {
	fastPrime(t)
	PrimeFirstWait = 0 // 第一次检查只读一次列表，保证落盘发生在 working 长等里
	session := filepath.Join(t.TempDir(), "session.jsonl")
	f := &primeFake{session: session, status: "working", flushText: PrimeSentinel + " 手册", flushAfterList: 3}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	if err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{})); err != nil {
		t.Fatal(err)
	}
	if f.flushText != "" {
		t.Fatal("expected the fake to flush the session file")
	}
	if f.enter != 0 {
		t.Fatalf("working 时不该补 Enter，实际 %d 次", f.enter)
	}
	marker, _ := os.ReadFile(filepath.Join(exp.PrimeMarkerDir, "songjiang"))
	parts := strings.Split(strings.TrimSpace(string(marker)), "\n")
	if len(parts) < 2 || parts[1] != session {
		t.Fatalf("标记必须记下真落盘的会话路径: %q", marker)
	}
}

// working 但会话永远不落盘：不能把 working 当送达（ADR 明确拒绝），必须报错且不写标记。
// 落盘等空后仍在 working = 真回合在跑，绝不能再塞 Enter 干扰它。
func TestSubmitPromptFailsWhenWorkingNeverFlushes(t *testing.T) {
	fastPrime(t)
	PrimeWorkingWait = 50 * time.Millisecond
	f := &primeFake{session: filepath.Join(t.TempDir(), "session.jsonl"), status: "working"}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	if err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{})); err == nil {
		t.Fatal("working 但永不落盘必须报错")
	}
	if primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("失败不得写 prime 标记")
	}
	if f.enter != 0 {
		t.Fatalf("仍在 working 时不许补 Enter（会打断在跑的回合），实际 %d 次", f.enter)
	}
}

// 落盘等空但状态已不是 working：说明之前的 working 是启动窗口假象，文本还在输入框，
// 这时必须回到补 Enter，否则首条注入永远进不去。
func TestSubmitPromptPressesEnterWhenWorkingEndsUnflushed(t *testing.T) {
	fastPrime(t)
	PrimeFirstWait = 0 // 第一次检查只读一次列表，保证状态翻转发生在 working 长等里
	PrimeWorkingWait = 50 * time.Millisecond
	session := filepath.Join(t.TempDir(), "session.jsonl")
	f := &primeFake{session: session, status: "working", idleAfterList: 3, writeOnEnter: true}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	if err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{})); err != nil {
		t.Fatal(err)
	}
	if f.enter == 0 {
		t.Fatal("状态落回 idle 后应补 Enter 把输入框里的文本提交")
	}
	if !primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("补 Enter 送达后必须落标记")
	}
}

// 补 Enter 真把文本提交后，回合启动、omp 仍要十几秒才落盘。这时第二次看到 working
// 不能再按 Enter，也不能再吃一整轮 PrimeWorkingWait：长等只消费一次，之后仍在
// working 说明是真回合，应报错留给下一轮。用 list 调用次数断言，避免墙钟。
func TestSubmitPromptDoesNotEnterAfterWorkingResume(t *testing.T) {
	fastPrime(t)
	PrimeFirstWait = 0
	PrimeWorkingWait = 150 * time.Millisecond
	PrimeWait = 5 * time.Millisecond
	PrimePoll = 5 * time.Millisecond
	f := &primeFake{session: filepath.Join(t.TempDir(), "session.jsonl"),
		status: "working", idleAfterList: 4, workingOnEnter: true}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	if err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{})); err == nil {
		t.Fatal("回合重新跑起来但没落盘时必须报错")
	}
	if f.enter != 1 {
		t.Fatalf("只允许补一次 Enter（救回假 working），实际 %d 次", f.enter)
	}
	// 一轮 150ms 长等 ≈ 30 次 list；多一轮就会翻倍。
	if f.listCalls > 50 {
		t.Fatalf("长等被重复消费（list 调用 %d 次），说明 working 守卫只在第一轮生效", f.listCalls)
	}
	if primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("失败不得写 prime 标记")
	}
}

// 长等落盘期间 agent 弹审批框（working→blocked）：绝不能补 Enter 替用户点确认，
// 必须把真实 agent_blocked 透给 dispatch。
func TestSubmitPromptDoesNotEnterWhenBlockedDuringWorkingWait(t *testing.T) {
	fastPrime(t)
	PrimeFirstWait = 0
	PrimeWorkingWait = 50 * time.Millisecond
	f := &primeFake{session: filepath.Join(t.TempDir(), "session.jsonl"),
		status: "working", blockedAfterList: 3}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{}))
	if err == nil || !herdr.IsBlocked(err) {
		t.Fatalf("应透出 agent_blocked: %v", err)
	}
	if f.enter != 0 {
		t.Fatalf("blocked 时不许补 Enter（会替用户答审批框），实际 %d 次", f.enter)
	}
	if primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("blocked 不得写 prime 标记")
	}
}

// agent list 瞬时失败不能当成「agent 缺席」放弃重试：冷启动窗口里一次抖动就会
// 让 dispatch 关掉 pane、丢掉任务。
func TestSubmitPromptRetriesThroughListErrors(t *testing.T) {
	fastPrime(t)
	session := filepath.Join(t.TempDir(), "session.jsonl")
	f := &primeFake{session: session, status: "idle", writeOnEnter: true, notReady: 2, listErrAt: 1}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	if err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{})); err != nil {
		t.Fatal(err)
	}
	if !primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("list 抖动后仍应重试到送达")
	}
}

// 就绪预算耗尽：agent 一直在列表里但 prompt 永远拒绝，必须在上限后返回 not-ready。
func TestSubmitPromptGivesUpWhenNeverReady(t *testing.T) {
	fastPrime(t)
	SubmitReadyWait = 25 * time.Millisecond
	SubmitPoll = 5 * time.Millisecond
	f := &primeFake{status: "idle", notReady: 1 << 30}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	done := make(chan error, 1)
	go func() {
		done <- primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{}))
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("就绪预算没有生效：agent 永远拒绝时循环不退出")
	}
	if err == nil || !herdr.IsNotReady(err) {
		t.Fatalf("预算耗尽应透出 not-ready 错误: %v", err)
	}
	if f.prompts < 2 {
		t.Fatalf("应重试多次后才放弃，实际 prompt %d 次", f.prompts)
	}
	if primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("未就绪不得写 prime 标记")
	}
}

// 重试期间 agent 转 blocked：必须返回真实 agent_blocked，而不是把上一次 not-ready
// 透给调用方——dispatch 靠 IsBlocked 决定要不要把审批框交回用户。
func TestSubmitPromptStopsWhenAgentBecomesBlocked(t *testing.T) {
	fastPrime(t)
	f := &primeFake{status: "idle", notReady: 1, blocked: true}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{}))
	if err == nil || !herdr.IsBlocked(err) {
		t.Fatalf("应透出 agent_blocked: %v", err)
	}
	if f.prompts != 2 {
		t.Fatalf("应一次未就绪 + 一次 blocked，实际 prompt %d 次", f.prompts)
	}
	if primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("blocked 不得写 prime 标记")
	}
}

// 落盘等待必须可被 ctx 取消；否则 Ctrl+C 也要拖满 PrimeWorkingWait。
func TestSubmitPromptHonorsContextCancel(t *testing.T) {
	fastPrime(t)
	PrimeFirstWait = 500 * time.Millisecond
	PrimePoll = 5 * time.Millisecond
	f := &primeFake{session: filepath.Join(t.TempDir(), "nope.jsonl"), status: "idle"} // prompt 接受，但会话永不落盘
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	_, err := SubmitPrompt(ctx, clientPrime(f), "songjiang", "文本", "SENTINEL")
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消必须透出 context.Canceled: %v", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("取消后仍拖了 %s（等待未响应 ctx）", elapsed)
	}
}

// 冷启动重试期间取消也要透出 context.Canceled，而不是上一次的 not-ready：
// 否则调用方无法区分「用户中断」和「agent 起不来」。
func TestSubmitPromptCancelDuringReadinessRetry(t *testing.T) {
	fastPrime(t)
	SubmitReadyWait = 5 * time.Second
	SubmitPoll = 5 * time.Millisecond
	f := &primeFake{status: "idle", notReady: 1 << 30}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	_, err := SubmitPrompt(ctx, clientPrime(f), "songjiang", "文本", "SENTINEL")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("就绪重试被取消时应透出 context.Canceled: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("取消后仍拖了 %s", elapsed)
	}
}

func TestPrimeMarkerFresh(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "songjiang"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("2026-11-19T03:36:04Z\n")
	if primeMarkerFresh(dir, "songjiang", "/sessions/new.jsonl") {
		t.Fatal("老标记没有会话路径，不能算作当前会话已 prime")
	}
	write("2026-11-19T03:36:04Z\n/sessions/old.jsonl\n")
	if primeMarkerFresh(dir, "songjiang", "/sessions/new.jsonl") {
		t.Fatal("会话已更换，必须重 prime")
	}
	write("2026-11-19T03:36:04Z\n/sessions/new.jsonl\n")
	if !primeMarkerFresh(dir, "songjiang", "/sessions/new.jsonl") {
		t.Fatal("同一会话应视为已 prime")
	}
}

// primeFake 只覆盖 primeAgent 需要的 herdr 面：agent list / prompt / send-keys。
type primeFake struct {
	session          string
	status           string
	writeOnEnter     bool
	notReady         int    // 前 N 次 agent prompt 以 agent_not_found 拒绝（模拟冷启动）
	blocked          bool   // agent 列表报 blocked，prompt 以 agent_blocked 拒绝
	prompts          int    // agent prompt 总尝试次数（含被拒）
	flushText        string // 第 flushAfterList 次 agent list 后写进会话文件（模拟 omp 落盘）
	flushAfterList   int
	idleAfterList    int  // 第 N 次 agent list 后状态从 working 落回 idle（启动窗口假象）
	blockedAfterList int  // 第 N 次 agent list 后状态转 blocked（长等期间弹审批框）
	listErrAt        int  // 第 N 次 agent list 返回拒绝（模拟 list 瞬时失败）
	workingOnEnter   bool // send-keys 后状态回到 working（模拟 Enter 真把文本提交了）
	listCalls        int
	composer         string
	enter            int
}

func clientPrime(f *primeFake) *herdr.Client { return &herdr.Client{Run: f.run} }

func (f *primeFake) run(_ context.Context, args []string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "agent list":
		if f.status == "absent" {
			return `{"result":{"agents":[]}}`, nil
		}
		f.listCalls++
		if f.listErrAt > 0 && f.listCalls == f.listErrAt {
			return "", &herdr.CmdError{Code: "server_unavailable", Message: "transient"}
		}
		if f.idleAfterList > 0 && f.listCalls >= f.idleAfterList {
			f.status = "idle"
			f.idleAfterList = 0
		}
		if f.blockedAfterList > 0 && f.listCalls >= f.blockedAfterList {
			f.blocked = true
			f.blockedAfterList = 0
		}
		if f.flushText != "" && f.flushAfterList > 0 && f.listCalls >= f.flushAfterList {
			if err := os.WriteFile(f.session, []byte(f.flushText), 0o644); err != nil {
				return "", err
			}
			f.flushText = ""
		}
		status := f.status
		if f.blocked {
			status = "blocked"
		}
		if status == "" {
			status = "idle"
		}
		sess := ""
		if f.session != "" {
			sess = `,"agent_session":{"value":"` + f.session + `"}`
		}
		return `{"result":{"agents":[{"agent":"omp","name":"songjiang","agent_status":"` + status + `"` + sess + `}]}}`, nil
	case "agent prompt":
		f.prompts++
		if f.notReady > 0 {
			f.notReady--
			return "", &herdr.CmdError{Code: "agent_not_found", Message: "agent still starting"}
		}
		if f.blocked {
			return "", &herdr.CmdError{Code: "agent_blocked", Message: "agent is blocked"}
		}
		f.composer = args[3]
		return `{}`, nil
	case "agent send-keys":
		f.enter++
		if f.workingOnEnter {
			f.status = "working"
		}
		if f.writeOnEnter && f.composer != "" {
			if err := os.WriteFile(f.session, []byte(f.composer), 0o644); err != nil {
				return "", err
			}
			f.composer = ""
		}
		return `{"result":{"type":"ok"}}`, nil
	}
	return "", fmt.Errorf("primeFake: unexpected herdr command %q", strings.Join(args, " "))
}

func fastPrime(t *testing.T) {
	t.Helper()
	oldFirst, oldWait, oldPoll := PrimeFirstWait, PrimeWait, PrimePoll
	oldReady, oldSubmitPoll, oldWorking := SubmitReadyWait, SubmitPoll, PrimeWorkingWait
	PrimeFirstWait, PrimeWait, PrimePoll = 20*time.Millisecond, 100*time.Millisecond, 5*time.Millisecond
	SubmitReadyWait, SubmitPoll, PrimeWorkingWait = 100*time.Millisecond, 5*time.Millisecond, 500*time.Millisecond
	t.Cleanup(func() {
		PrimeFirstWait, PrimeWait, PrimePoll = oldFirst, oldWait, oldPoll
		SubmitReadyWait, SubmitPoll, PrimeWorkingWait = oldReady, oldSubmitPoll, oldWorking
	})
}
