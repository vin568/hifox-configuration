package boot

import (
	"context"
	"testing"
)

// workspace 存在但 snapshot 里没有它的 pane（换脑时关 pane 的兜底路径可能留下这种残局）：
// 引导必须自愈——关掉空壳 workspace 再重建，而不是硬报错把注入路径永久卡死。
func TestEnsureRebuildsWorkspaceWithoutPane(t *testing.T) {
	ws := `{"result":{"workspaces":[
		{"workspace_id":"w11","label":"master"},{"workspace_id":"w12","label":"crew"},{"workspace_id":"w13","label":"jobs"}]}}`
	f := &fakeHerdr{workspaces: ws, agents: `{"result":{"agents":[]}}`, snapshot: `{"result":{"snapshot":{"layouts":[]}}}`}
	rep, err := Ensure(context.Background(), client(f), exp(t.TempDir()))
	if err != nil {
		t.Fatalf("空 pane 的 workspace 应自愈而不是报错: %v", err)
	}
	if len(rep.WorkspacesCreated) != 3 {
		t.Fatalf("三个空壳都应被重建: %+v", rep.WorkspacesCreated)
	}
	if len(f.closed) != 3 {
		t.Fatalf("重建前必须先关掉空壳: %v", f.closed)
	}
	if len(f.started) != 2 {
		t.Fatalf("重建后应起两个常驻 agent: %v", f.started)
	}
}
