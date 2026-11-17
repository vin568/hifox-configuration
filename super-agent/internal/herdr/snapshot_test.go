package herdr

import "testing"

// 形状取自 0.9.5 实机输出（super-agent 命名 session 首次 snapshot）。
const snapshotJSON = `{"id":"cli:api:snapshot","result":{"snapshot":{
 "agents":[],"focused_pane_id":"w1:p1","focused_tab_id":"w1:t1","focused_workspace_id":"w1",
 "layouts":[{"area":{"height":39,"width":94,"x":0,"y":0},"focused_pane_id":"w1:p1",
   "panes":[{"focused":true,"pane_id":"w1:p1","rect":{"height":39,"width":94,"x":0,"y":0}}],
   "splits":[],"tab_id":"w1:t1","workspace_id":"w1","zoomed":false}],
 "panes":[{"agent_status":"unknown","cwd":"/repo","focused":true,"pane_id":"w1:p1"}]}}}`

func TestDecodeSnapshotRootPane(t *testing.T) {
	s, err := decodeSnapshot(snapshotJSON)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.RootPane("w1"); got != "w1:p1" {
		t.Fatalf("RootPane(w1)=%q", got)
	}
	if got := s.RootPane("w9"); got != "" {
		t.Fatalf("RootPane(missing)=%q, want empty", got)
	}
}
