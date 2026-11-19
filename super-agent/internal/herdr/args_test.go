package herdr

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// herdr CLI 的参数契约：flag 名、顺序、`--` 透传三态。flag 改名/换位的
// 变异必须被这些用例拦下（INV-9 的命令面保真）。
func TestWorkspaceCreateArgs(t *testing.T) {
	want := []string{"workspace", "create", "--cwd", "/repo", "--label", "crew", "--no-focus"}
	if got := workspaceCreateArgs("/repo", "crew"); !reflect.DeepEqual(got, want) {
		t.Fatalf("workspaceCreateArgs = %v, want %v", got, want)
	}
}

func TestAgentStartArgs(t *testing.T) {
	base := []string{"agent", "start", "songjiang", "--kind", "omp", "--pane", "w11:p1"}
	if got := agentStartArgs("songjiang", "omp", "w11:p1", nil); !reflect.DeepEqual(got, base) {
		t.Fatalf("agentStartArgs no extra = %v", got)
	}
	withExtra := append(append([]string{}, base...), "--", "--model", "fast")
	if got := agentStartArgs("songjiang", "omp", "w11:p1", []string{"--model", "fast"}); !reflect.DeepEqual(got, withExtra) {
		t.Fatalf("agentStartArgs extra passthrough = %v, want %v", got, withExtra)
	}
}

func TestAgentPromptArgs(t *testing.T) {
	if got := agentPromptArgs("songjiang", "hi", false, 0); !reflect.DeepEqual(got, []string{"agent", "prompt", "songjiang", "hi"}) {
		t.Fatalf("agentPromptArgs plain = %v", got)
	}
	want := []string{"agent", "prompt", "songjiang", "hi", "--wait", "--timeout", "5000"}
	if got := agentPromptArgs("songjiang", "hi", true, 5000); !reflect.DeepEqual(got, want) {
		t.Fatalf("agentPromptArgs wait+timeout = %v, want %v", got, want)
	}
}

// ExecRunner 必须经 HERDR_SESSION 环境变量定位命名 session，且把原样
// 参数交给真实 herdr 二进制——用 PATH 注入假 herdr 验证。
func TestExecRunnerSessionEnv(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "herdr")
	payload := `#!/bin/sh
echo "session=$HERDR_SESSION args=$*"
`
	if err := os.WriteFile(script, []byte(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	out, err := ExecRunner("super-agent")(context.Background(), []string{"agent", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "session=super-agent") || !strings.Contains(out, "args=agent list") {
		t.Fatalf("ExecRunner output wrong: %q", out)
	}
}

func TestAgentPromptUntilArgs(t *testing.T) {
	want := []string{"agent", "prompt", "worker-1", "hi", "--wait", "--until", "working", "--until", "blocked", "--timeout", "15000"}
	if got := agentPromptUntilArgs("worker-1", "hi", []string{"working", "blocked"}, 15000); !reflect.DeepEqual(got, want) {
		t.Fatalf("agentPromptUntilArgs = %v, want %v", got, want)
	}
}
