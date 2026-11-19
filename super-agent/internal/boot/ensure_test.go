package boot

import (
	"context"
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
	failPrompt int // 前 N 次 agent prompt 报错（模拟 prime 失败）
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
	if !f.enter {
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
	session := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(session, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &primeFake{session: session, status: "idle", writeOnEnter: false}
	exp := Expected{MasterAgent: "songjiang", PrimeMarkerDir: t.TempDir()}
	if err := primeAgent(context.Background(), clientPrime(f), exp, "songjiang", PrimePrompt("手册", Paths{})); err == nil {
		t.Fatal("首条注入没进会话必须报错")
	}
	if primeMarkerExists(exp.PrimeMarkerDir, "songjiang") {
		t.Fatal("失败不得写 prime 标记")
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
	session      string
	status       string
	writeOnEnter bool
	composer     string
	enter        bool
}

func clientPrime(f *primeFake) *herdr.Client { return &herdr.Client{Run: f.run} }

func (f *primeFake) run(_ context.Context, args []string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "agent list":
		status := f.status
		if status == "" {
			status = "idle"
		}
		sess := ""
		if f.session != "" {
			sess = `,"agent_session":{"value":"` + f.session + `"}`
		}
		return `{"result":{"agents":[{"agent":"omp","name":"songjiang","agent_status":"` + status + `"` + sess + `}]}}`, nil
	case "agent prompt":
		f.composer = args[3]
		return `{}`, nil
	case "agent send-keys":
		f.enter = true
		if f.writeOnEnter && f.composer != "" {
			if err := os.WriteFile(f.session, []byte(f.composer), 0o644); err != nil {
				return "", err
			}
			f.composer = ""
		}
		return `{"result":{"type":"ok"}}`, nil
	}
	return "", nil
}

func fastPrime(t *testing.T) {
	t.Helper()
	oldFirst, oldWait, oldPoll := PrimeFirstWait, PrimeWait, PrimePoll
	PrimeFirstWait, PrimeWait, PrimePoll = 20*time.Millisecond, 100*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { PrimeFirstWait, PrimeWait, PrimePoll = oldFirst, oldWait, oldPoll })
}
