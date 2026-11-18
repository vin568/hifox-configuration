package boot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		return f.agents, nil
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
	got := PrimePrompt("手册正文", Paths{CapabilitiesDir: "/cap", ActiveDir: "/active", ArchivedDir: "/archived", ResultsDir: "/results", InflightPath: "/inflight", RuntimeCmd: "/opt/bin/superctl", Index: "## 平台能力\n- 戴宗：发布验收"})
	for _, want := range []string{"手册正文", "/cap", "/active", "/archived", "/results", "/inflight", "superctl reply", "/opt/bin/superctl", "运行时命令", "## 平台能力", "戴宗"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prime prompt missing %q", want)
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
