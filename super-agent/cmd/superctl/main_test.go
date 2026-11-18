package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"super-agent/internal/config"
	"super-agent/internal/herdr"
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
			"w12:p1": {Agent: "omp", Name: "jiangjing", PaneID: "w12:p1"},
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

func TestHandleAgentEventInjectsCapabilityChange(t *testing.T) {
	cfg := config.Default()
	cfg.MasterAgent = "songjiang"
	var prompted []string
	c := injectClient(&prompted)
	ok, err := handleAgentEvent(context.Background(), cfg, c, testIdx(),
		herdr.Event{Event: "pane.agent_status_changed", Data: herdr.EventData{PaneID: "w12:p1", AgentStatus: "blocked"}})
	if err != nil || !ok || len(prompted) != 1 {
		t.Fatalf("ok=%v err=%v prompted=%v", ok, err, prompted)
	}
	for _, want := range []string{"jiangjing", "w12:p1", "blocked", "[触发器: event]"} {
		if !strings.Contains(prompted[0], want) {
			t.Fatalf("prompt missing %q: %s", want, prompted[0])
		}
	}
}

func TestHandleAgentEventBlockedMapsToErrBlocked(t *testing.T) {
	cfg := config.Default()
	cfg.MasterAgent = "songjiang"
	c := &herdr.Client{Run: func(context.Context, []string) (string, error) {
		return "", &herdr.CmdError{Code: "agent_blocked", Message: "blocked"}
	}}
	_, err := handleAgentEvent(context.Background(), cfg, c, testIdx(),
		herdr.Event{Event: "pane.agent_status_changed", Data: herdr.EventData{PaneID: "w12:p1", AgentStatus: "idle"}})
	if !errors.Is(err, inject.ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
}
