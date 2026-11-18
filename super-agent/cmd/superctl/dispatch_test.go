package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"super-agent/internal/capability"
	"super-agent/internal/config"
)

// usage 里的命令名（行首两个空格 + 小写命令名）。排版微调（对齐空格多少）不该影响
// 这两条对账用例，所以按行解析而不是子串匹配。
var usageCmdRe = regexp.MustCompile(`(?m)^ {2}([a-z][a-z-]*)`)

func usageCommands() map[string]bool {
	names := map[string]bool{}
	for _, m := range usageCmdRe.FindAllStringSubmatch(usage, -1) {
		names[m[1]] = true
	}
	return names
}

// F-1 回归：usage 里列出的每个命令必须注册（reply 曾在集成编辑中从
// switch 丢失，主控回话唯一通道当场断掉）。
func TestUsageCommandsAllRegistered(t *testing.T) {
	names := usageCommands()
	if len(names) < 6 {
		t.Fatalf("usage 解析出 %d 个命令，文案格式可能变了: %v", len(names), names)
	}
	for name := range names {
		if name == "help" {
			continue // help 在 main 里特判，不入命令表
		}
		if _, ok := commands[name]; !ok {
			t.Errorf("usage 列出 %q 但 commands 未注册", name)
		}
	}
}

func TestReplyRegistered(t *testing.T) {
	if _, ok := commands["reply"]; !ok {
		t.Fatal("reply 必须注册：主控回用户的唯一通道")
	}
}

// 注册表里的命令都应在 usage 有说明（防幽灵命令）。
func TestRegisteredCommandsDocumented(t *testing.T) {
	names := usageCommands()
	for name := range commands {
		if !names[name] {
			t.Errorf("命令 %q 已注册但 usage 未列出", name)
		}
	}
}

func TestCommandsArentNil(t *testing.T) {
	for name, run := range commands {
		if run == nil {
			t.Errorf("命令 %q 的处理函数为 nil", name)
		}
	}
}

// 冒烟：真实的 reply 命令行应走出参数解析（而非 unknown command）。
func TestReplySmoke(t *testing.T) {
	dir := t.TempDir()
	cfg := "session = \"t\"\nstate_dir = \"" + dir + "\"\nrepo_dir = \"/repo\"\n"
	path := dir + "/superctl.toml"
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SONGJIANG_CONFIG", path)
	run := commands["reply"]
	if err := run([]string{"--chat", "oc_1", "--text", "hi"}); err != nil {
		t.Fatalf("reply 冒烟失败: %v", err)
	}
	data, err := os.ReadFile(dir + "/outbox.jsonl")
	if err != nil || !strings.Contains(string(data), "oc_1") {
		t.Fatalf("outbox 未落盘: %v %s", err, data)
	}
}

// 派活通道必须恰好一个：两个都不给或两个都给都当场拒绝，不猜。登记在途清单改由派活员
// 自己跑 `dispatch --platform hifox` 完成，所以外面的 track 登记面已下线。
func TestDispatchRequiresExactlyOneChannel(t *testing.T) {
	if err := cmdDispatch([]string{"--agent", "worker", "--body", "b"}); err == nil || !strings.Contains(err.Error(), "恰好一个 --platform") {
		t.Fatalf("两个通道都不给应报错: %v", err)
	}
	if err := cmdDispatch([]string{"--platform", "hifox", "--local", "--agent", "worker", "--body", "b"}); err == nil || !strings.Contains(err.Error(), "恰好一个 --platform") {
		t.Fatalf("两个通道都给应报错: %v", err)
	}
}

// --cloud 已下线：它曾把「云端」当 hifox 的同义词，现在派活必须点名平台。
// 报错要带 --cloud（不然用户只知道「参数不对」，不知道是哪个参数）。
func TestDispatchRejectsRemovedCloudFlag(t *testing.T) {
	err := cmdDispatch([]string{"--cloud", "--agent", "worker", "--title", "t"})
	if err == nil {
		t.Fatal("--cloud 已下线，不该再被接受")
	}
	if !strings.Contains(err.Error(), "--cloud") {
		t.Fatalf("报错应点名 --cloud: %v", err)
	}
}

// 未知平台如实拒绝：不拿 hifox 的 CLI 去猜别的平台怎么派活。
func TestDispatchPlatformUnknownPlatform(t *testing.T) {
	_, err := dispatchPlatform(context.Background(), config.Config{}, platformReq{Platform: "sharkly", Title: "t", AgentKey: "loki"}, nil)
	if err == nil || !strings.Contains(err.Error(), `平台 "sharkly" 还没有派活实现`) {
		t.Fatalf("未知平台必须拒绝: %v", err)
	}
}

// dispatchLocal 的首条 prompt 必须带上 agent 手册：role=job 的 agent 没有别的途径拿手册
// （换脑那条路只给 master/retro），只喂正文等于让它空手干活。
func TestLocalPromptInjectsManual(t *testing.T) {
	dir := t.TempDir()
	manual := filepath.Join(dir, "instructions.md")
	if err := os.WriteFile(manual, []byte("工人手册正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := localPrompt(capability.Agent{Key: "worker", Instructions: manual}, "跑一遍", "/results/x.md")
	for _, want := range []string{"[你的手册]", "工人手册正文\n\n[本轮任务]", "跑一遍", "/results/x.md", "只回文件路径"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt 缺 %q:\n%s", want, got)
		}
	}
}

// 手册读不到不该让派活失败：退回只给任务正文（保持原样）。
func TestLocalPromptFallsBackWithoutManual(t *testing.T) {
	got := localPrompt(capability.Agent{Key: "worker", Instructions: filepath.Join(t.TempDir(), "nope.md")}, "跑一遍", "/results/x.md")
	if strings.Contains(got, "[你的手册]") {
		t.Fatalf("没有手册就不该写手册段:\n%s", got)
	}
	if !strings.Contains(got, "[任务] 跑一遍") || !strings.Contains(got, "/results/x.md") {
		t.Fatalf("应退回任务正文 + 结果路径:\n%s", got)
	}
}

// 平台哨兵命令：cloudwatch 曾当 hifox 的同义词，现已改名 platformwatch；
// 旧名不该再出现在注册表或 usage 里（照着旧名敲会得到 unknown command）。
func TestPlatformwatchRegisteredAndCloudwatchGone(t *testing.T) {
	if _, ok := commands["platformwatch"]; !ok {
		t.Fatal("cloudwatch 应改名 platformwatch 并注册")
	}
	if strings.Contains(usage, "cloudwatch") {
		t.Fatal("usage 里不该再出现 cloudwatch")
	}
}
