package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// F-1 回归：usage 里列出的每个命令必须注册（reply 曾在集成编辑中从
// switch 丢失，主控回话唯一通道当场断掉）。
func TestUsageCommandsAllRegistered(t *testing.T) {
	re := regexp.MustCompile(`(?m)^  ([a-z]+)  `)
	matches := re.FindAllStringSubmatch(usage, -1)
	if len(matches) < 6 {
		t.Fatalf("usage 解析出 %d 个命令，文案格式可能变了", len(matches))
	}
	for _, m := range matches {
		name := m[1]
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
	for name := range commands {
		if !strings.Contains(usage, "  "+name+"  ") {
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
	cfg := "session = \"t\"\nmaster_agent = \"m\"\nretro_agent = \"r\"\nstate_dir = \"" + dir + "\"\n"
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
