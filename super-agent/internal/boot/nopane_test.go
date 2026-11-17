package boot

import (
	"context"
	"strings"
	"testing"
)

// workspace 存在但 snapshot 里没有它的 pane（实机空 workspace 常见态）：
// 引导必须报错而不是拿空 pane 去 start agent。
func TestEnsureWorkspaceWithoutPaneFails(t *testing.T) {
	ws := `{"result":{"workspaces":[
		{"workspace_id":"w11","label":"master"},{"workspace_id":"w12","label":"crew"},{"workspace_id":"w13","label":"jobs"}]}}`
	f := &fakeHerdr{workspaces: ws, agents: fullAgents(), snapshot: `{"result":{"snapshot":{"layouts":[]}}}`}
	_, err := Ensure(context.Background(), client(f), exp(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "no pane") {
		t.Fatalf("want no-pane error, got %v", err)
	}
	if len(f.created) != 0 || len(f.started) != 0 {
		t.Fatalf("failed reconcile must not create/start anything: created=%v started=%v", f.created, f.started)
	}
}
