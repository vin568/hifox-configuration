package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"super-agent/internal/config"
	"super-agent/internal/herdr"
	"super-agent/internal/inflight"
	"super-agent/internal/inject"
)

// 升级兼容的接线：哨兵两个调用点都要拿到「新快照 + 旧名快照」。旧名只在升级窗口里
// 当基线——把它漏成空串，升级首轮会把在途任务全部重打基线，窗口内的变化不注入。
func TestSnapshotPathsIncludeLegacyName(t *testing.T) {
	cfg := config.Config{StateDir: t.TempDir()}
	snap, legacy := snapshotPaths(cfg)
	if filepath.Base(snap) != "platform-snapshot.json" {
		t.Errorf("新快照名 = %q", snap)
	}
	if filepath.Base(legacy) != "cloud-snapshot.json" {
		t.Errorf("旧名快照 = %q", legacy)
	}
	if filepath.Dir(snap) != cfg.StateDir || filepath.Dir(legacy) != cfg.StateDir {
		t.Errorf("快照该落在状态目录: %q %q", snap, legacy)
	}
}

func injectClient(prompted *[]string) *herdr.Client {
	return &herdr.Client{Run: func(_ context.Context, args []string) (string, error) {
		if strings.Join(args[:2], " ") == "agent prompt" {
			*prompted = append(*prompted, args[3])
			return `{}`, nil
		}
		return "", nil
	}}
}

func testIdx() herdr.PaneIndex {
	return herdr.PaneIndex{
		MasterPane: "w11:p1",
		ByPane: map[string]herdr.Agent{
			"w11:p1": {Agent: "omp", Name: "songjiang", PaneID: "w11:p1"},
			"w12:p1": {Agent: "omp", Name: "worker-120000", PaneID: "w12:p1"},
		},
	}
}

func TestHandleAgentEventSkipsMasterUnknownAndWrongType(t *testing.T) {
	cfg := config.Default()
	cfg.MasterAgent = "songjiang"
	idx := testIdx()
	var prompted []string
	c := injectClient(&prompted)

	statusChange := func(pane string) herdr.Event {
		return herdr.Event{Event: "pane.agent_status_changed", Data: herdr.EventData{PaneID: pane, AgentStatus: "idle"}}
	}
	// 主控自身（按 pane 与按名字都识别得出）。
	if ok, err := handleAgentEvent(context.Background(), cfg, c, idx, statusChange("w11:p1")); ok || err != nil {
		t.Fatalf("master pane event must be skipped: %v %v", ok, err)
	}
	// 名字命中但 pane 是 master workspace（拓扑识别兜底）。
	idxOnlyByPane := idx
	idxOnlyByPane.ByPane["w11:p1"] = herdr.Agent{Agent: "omp", PaneID: "w11:p1"} // name 缺失
	if ok, _ := handleAgentEvent(context.Background(), cfg, c, idxOnlyByPane, statusChange("w11:p1")); ok {
		t.Fatal("master pane（无名）must be skipped by topology")
	}
	// 陌生 pane（其他 session）。
	if ok, err := handleAgentEvent(context.Background(), cfg, c, idx, statusChange("w99:p1")); ok || err != nil {
		t.Fatalf("unknown pane must be skipped: %v %v", ok, err)
	}
	// 非状态变化事件类型：跳过。
	if ok, _ := handleAgentEvent(context.Background(), cfg, c, idx, herdr.Event{Event: "workspace.created", Data: herdr.EventData{PaneID: "w12:p1", AgentStatus: "idle"}}); ok {
		t.Fatal("non agent-status event must be skipped")
	}
	// 空状态。
	if ok, _ := handleAgentEvent(context.Background(), cfg, c, idx, herdr.Event{Event: "pane.agent_status_changed", Data: herdr.EventData{PaneID: "w12:p1"}}); ok {
		t.Fatal("empty status must be skipped")
	}
	if len(prompted) != 0 {
		t.Fatalf("nothing should be injected: %v", prompted)
	}
}

// eventCfg 造一份带临时状态目录的主控配置；tracked=true 时把 w12:p1 上的 job agent
// 登记为在途本地作业（新契约只认这条身份边界）。
func eventCfg(t *testing.T, tracked bool) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.MasterAgent = "songjiang"
	cfg.StateDir = t.TempDir()
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if tracked {
		if err := inflight.Add(cfg.InflightPath(), inflight.Entry{
			Kind: inflight.KindLocal, Agent: "worker-120000", Pane: "w12:p1", Capability: "worker",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

// 在途本地作业的 blocked 是主控必须知道的信号（它在等用户处理）。
func TestHandleAgentEventInjectsInFlightLocalJob(t *testing.T) {
	cfg := eventCfg(t, true)
	var prompted []string
	c := injectClient(&prompted)
	ok, err := handleAgentEvent(context.Background(), cfg, c, testIdx(),
		herdr.Event{Event: "pane.agent_status_changed", Data: herdr.EventData{PaneID: "w12:p1", AgentStatus: "blocked"}})
	if err != nil || !ok || len(prompted) != 1 {
		t.Fatalf("ok=%v err=%v prompted=%v", ok, err, prompted)
	}
	for _, want := range []string{"worker-120000", "w12:p1", "blocked", "[触发器: event]"} {
		if !strings.Contains(prompted[0], want) {
			t.Fatalf("prompt missing %q: %s", want, prompted[0])
		}
	}
}

// 完成事件必须带结果文件路径，主控不必再猜它写哪了。
func TestHandleAgentEventDoneCarriesResultPath(t *testing.T) {
	cfg := eventCfg(t, true)
	var prompted []string
	c := injectClient(&prompted)
	ok, err := handleAgentEvent(context.Background(), cfg, c, testIdx(),
		herdr.Event{Event: "pane.agent_status_changed", Data: herdr.EventData{PaneID: "w12:p1", AgentStatus: "done"}})
	if err != nil || !ok || len(prompted) != 1 {
		t.Fatalf("ok=%v err=%v prompted=%v", ok, err, prompted)
	}
	want := filepath.Join(cfg.ResultsDir(), "worker-120000.md")
	if !strings.Contains(prompted[0], want) {
		t.Fatalf("done 事件缺结果路径 %s: %s", want, prompted[0])
	}
	// done 不等于交付：它也可能是在等用户回话，措辞必须点破。
	if !strings.Contains(prompted[0], "不代表已交付") {
		t.Fatalf("done 事件要说明回合结束 ≠ 已交付: %s", prompted[0])
	}
}

// working/idle 与不在在途清单里的 agent 一律不叫醒主控：前者是噪声，后者是别人
// （常驻 agent、别处起的 pane）的状态，不该插进主控回合。
func TestHandleAgentEventSkipsNoise(t *testing.T) {
	cfg := eventCfg(t, true)
	var prompted []string
	c := injectClient(&prompted)
	for _, status := range []string{"working", "idle"} {
		ok, err := handleAgentEvent(context.Background(), cfg, c, testIdx(),
			herdr.Event{Event: "pane.agent_status_changed", Data: herdr.EventData{PaneID: "w12:p1", AgentStatus: status}})
		if ok || err != nil {
			t.Fatalf("%s 不该注入: ok=%v err=%v", status, ok, err)
		}
	}
	untracked := eventCfg(t, false)
	ok, err := handleAgentEvent(context.Background(), untracked, c, testIdx(),
		herdr.Event{Event: "pane.agent_status_changed", Data: herdr.EventData{PaneID: "w12:p1", AgentStatus: "done"}})
	if ok || err != nil {
		t.Fatalf("不在在途清单的 agent 不该注入: ok=%v err=%v", ok, err)
	}
	if len(prompted) != 0 {
		t.Fatalf("nothing should be injected: %v", prompted)
	}
}

func TestHandleAgentEventBlockedMapsToErrBlocked(t *testing.T) {
	cfg := eventCfg(t, true)
	c := &herdr.Client{Run: func(context.Context, []string) (string, error) {
		return "", &herdr.CmdError{Code: "agent_blocked", Message: "blocked"}
	}}
	_, err := handleAgentEvent(context.Background(), cfg, c, testIdx(),
		herdr.Event{Event: "pane.agent_status_changed", Data: herdr.EventData{PaneID: "w12:p1", AgentStatus: "blocked"}})
	if !errors.Is(err, inject.ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
}

// 主控启动参数必须带上技能覆盖配置：不带它，omp 只会发现 ~/.agents/skills 里那堆
// 开发工具技能，而宋江自己的技能一个都看不到（实测）。
func TestExpectedTopoMountsSkills(t *testing.T) {
	repo := testRepo(t)
	skillsRoot := filepath.Join(repo, "super-agent", "capabilities", "skills")
	writeRepoFile(t, filepath.Join(skillsRoot, "platform-dispatch"), "SKILL.md",
		"---\nname: platform-dispatch\ndescription: 派活纪律\n---\n\n# 平台派活\n")
	ownRoot := filepath.Join(repo, "super-agent", "capabilities", "agents", "songjiang", "skills")
	writeRepoFile(t, filepath.Join(ownRoot, "direct-tiers"), "SKILL.md",
		"---\nname: direct-tiers\ndescription: 三级授权\n---\n\n# 直连三级\n")

	cfg := testCfg(t, repo)
	// 原有 args 必须在：加技能不能把用户配置的 omp 参数（模型等）挤掉。
	cfg.MasterAgentArgs = []string{"--slow", "y"}
	writeRepoFile(t, filepath.Join(repo, "super-agent", "capabilities", "agents", "songjiang"), "capability.yaml",
		"key: songjiang\nname: 宋江\nrole: master\nworkspace: master\nkind: omp\nargs: [\"--model\", \"x\"]\n")
	writeRepoFile(t, filepath.Join(repo, "super-agent", "capabilities", "agents", "songjiang"), "skills.yaml",
		"- platform-dispatch\n")
	exp, err := expectedTopo(cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(exp.MasterArgs, "\x00")
	for _, want := range []string{"--model\x00x", "--slow\x00y"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("启动参数丢了原有 args %q: %v", want, exp.MasterArgs)
		}
	}
	var overlay string
	for i, a := range exp.MasterArgs {
		if a == "--config" && i+1 < len(exp.MasterArgs) {
			overlay = exp.MasterArgs[i+1]
		}
	}
	if overlay == "" {
		t.Fatalf("启动参数缺 --config <技能覆盖>: %v", exp.MasterArgs)
	}
	data, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatalf("覆盖配置读不到: %v", err)
	}
	// 两个根都必须在：共享的 + 他专属的。少一个就是「技能装而不用」。
	for _, want := range []string{"customDirectories", skillsRoot, ownRoot} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("覆盖配置缺 %q:\n%s", want, data)
		}
	}
	// 真源目录直接交给 CLI，不生成副本。
	if _, err := os.Stat(filepath.Join(skillsRoot, "platform-dispatch", "SKILL.md")); err != nil {
		t.Fatalf("真源 SKILL.md 不该被搬走: %v", err)
	}
	// 只剩 agent-report 没被声明：它必须出现在排除清单里。
	if !strings.Contains(string(data), "ignoredSkills:") || !strings.Contains(string(data), "    - agent-report") {
		t.Fatalf("未声明的共享技能应被排除:\n%s", data)
	}
}

// writeRepoFile 在 repoDir 下按相对路径写文件（造注册表桩用）。
func writeRepoFile(t *testing.T, repoDir, rel, body string) {
	t.Helper()
	p := filepath.Join(repoDir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// retro 也是常驻 agent：它的技能参数同样要挂上（三条启动路径同一段装配）。
func TestExpectedTopoMountsRetroSkills(t *testing.T) {
	repo := testRepo(t)
	writeRepoFile(t, filepath.Join(repo, "super-agent", "capabilities", "agents", "retro"), "capability.yaml",
		"key: retro\nname: 复盘\nrole: retro\nworkspace: crew\nkind: omp\n")
	writeRepoFile(t, filepath.Join(repo, "super-agent", "capabilities", "agents", "retro"), "instructions.md", "复盘手册")
	writeRepoFile(t, filepath.Join(repo, "super-agent", "capabilities", "agents", "retro", "skills", "retro-skill"),
		"SKILL.md", "---\nname: retro-skill\ndescription: 复盘纪律\n---\n\n# 复盘\n")

	cfg := testCfg(t, repo)
	exp, err := expectedTopo(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if exp.RetroAgent != "retro" {
		t.Fatalf("retro 应被装配: %q", exp.RetroAgent)
	}
	var overlay string
	for i, a := range exp.RetroArgs {
		if a == "--config" && i+1 < len(exp.RetroArgs) {
			overlay = exp.RetroArgs[i+1]
		}
	}
	if overlay == "" {
		t.Fatalf("retro 启动参数缺 --config <技能覆盖>: %v", exp.RetroArgs)
	}
	data, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatalf("retro 覆盖配置读不到: %v", err)
	}
	want := filepath.Join(repo, "super-agent", "capabilities", "agents", "retro", "skills")
	if !strings.Contains(string(data), want) {
		t.Fatalf("retro 覆盖配置应指向它的专属根 %s:\n%s", want, data)
	}
}

// 落盘开关的配置侧接线：MediaDir 与保留期必须从运行时配置进到飞书适配层。
// 这一行断了，线上图片/文件全部退回 key 占位（HF-16095 会以「只剩 key」复发）。
func TestFeishuConfigFromWiresMedia(t *testing.T) {
	cfg := testCfg(t, testRepo(t))
	cfg.StateDir = "/state"
	cfg.MediaRetentionRaw = "48h"
	cfg.AllowedUsers = []string{"ou_a"}
	cfg.ProxyReplyGroups = []string{"oc_proxy"}
	cfg.AnyMessageGroups = []string{"oc_any"}
	cfg.BotMentionOpenID = "ou_bot"
	fc := feishuConfigFrom(cfg, "app", "secret")
	if fc.MediaDir != filepath.Join("/state", "media") {
		t.Fatalf("MediaDir 没接上: %q", fc.MediaDir)
	}
	if fc.MediaRetention != 48*time.Hour {
		t.Fatalf("MediaRetention 没接上: %v", fc.MediaRetention)
	}
	// 准入字段漏接一处，线上对应来源的消息会被静默丢掉——这些也是装配，不是业务。
	if len(fc.AllowedUserIDs) != 1 || fc.AllowedUserIDs[0] != "ou_a" {
		t.Fatalf("AllowedUserIDs 没接上: %v", fc.AllowedUserIDs)
	}
	if len(fc.ProxyReplyGroups) != 1 || !fc.ProxyReplyGroups["oc_proxy"] {
		t.Fatalf("ProxyReplyGroups 没接上: %v", fc.ProxyReplyGroups)
	}
	if len(fc.AnyMessageGroups) != 1 || fc.AnyMessageGroups[0] != "oc_any" {
		t.Fatalf("AnyMessageGroups 没接上: %v", fc.AnyMessageGroups)
	}
	if fc.BotMentionOpenID != "ou_bot" {
		t.Fatalf("BotMentionOpenID 没接上: %q", fc.BotMentionOpenID)
	}
	if fc.DedupPath != cfg.DedupPath() {
		t.Fatalf("DedupPath 没接上: %q", fc.DedupPath)
	}
}

// go run / go test 的临时构建产物不能写进跨会话注入：进程一换路径就消失，
// 主控第一次 reply 就会撞上 No such file（实测）。
func TestIsEphemeralBuildPath(t *testing.T) {
	cases := map[string]bool{
		"/Users/vin/Library/Caches/go-build/51/xx/superctl":                   true,
		"/var/folders/7l/T/go-build4141458898/b001/exe/superctl":              true,
		filepath.Join(os.TempDir(), "superctl"):                               true,
		"/Users/vin/code/apifox/hifox-configuration/super-agent/bin/superctl": false,
		"": true,
	}
	for p, want := range cases {
		if got := isEphemeralBuildPath(p); got != want {
			t.Errorf("isEphemeralBuildPath(%q)=%v, want %v", p, got, want)
		}
	}
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// 临时构建回退到仓库构建好的 bin；没有就留空（prime 不打印），绝不灌死路径。
func TestStableRuntimeCmd(t *testing.T) {
	repo := t.TempDir()
	if got := stableRuntimeCmd(repo); got != "" {
		t.Fatalf("没有 bin 时应留空: %q", got)
	}
	bin := filepath.Join(repo, "super-agent", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, "superctl")
	if err := os.WriteFile(exe, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := stableRuntimeCmd(repo); got != exe {
		t.Fatalf("应回退到 %q, got %q", exe, got)
	}
}
