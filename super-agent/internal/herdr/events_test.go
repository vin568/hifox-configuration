package herdr

import (
	"encoding/json"
	"strings"
	"testing"
)

// 实机抓取的推送帧（super-agent session，jiangjing pane）。
const pushedEvent = `{"data":{"agent":"omp","agent_status":"working","pane_id":"w3:p1","workspace_id":"w3"},"event":"pane.agent_status_changed"}`
const confirmFrame = `{"id":"sa_probe","result":{"type":"subscription_started"}}`
const errorFrame = `{"id":"sa_probe","error":{"code":"pane_not_found","message":"pane  not found"}}`

func TestDecodePushedEvent(t *testing.T) {
	var ev Event
	if err := json.Unmarshal([]byte(pushedEvent), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Event != "pane.agent_status_changed" || ev.Data.AgentStatus != "working" ||
		ev.Data.PaneID != "w3:p1" || ev.Data.Agent != "omp" || ev.Data.WorkspaceID != "w3" {
		t.Fatalf("event=%+v", ev)
	}
}

func TestConfirmAndErrorFramesAreNotEvents(t *testing.T) {
	var ev Event
	if json.Unmarshal([]byte(confirmFrame), &ev) == nil && ev.Event != "" {
		t.Fatalf("confirm frame must decode to empty event: %+v", ev)
	}
	var envelope struct {
		ID    string `json:"id"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(errorFrame), &envelope); err != nil || envelope.Error == nil || envelope.Error.Code != "pane_not_found" {
		t.Fatalf("error frame must surface: %+v", envelope)
	}
}

func TestSocketPath(t *testing.T) {
	if got := SocketPath("super-agent"); !strings.HasSuffix(got, "sessions/super-agent/herdr.sock") {
		t.Fatalf("named socket path=%q", got)
	}
	if got := SocketPath(""); !strings.HasSuffix(got, "herdr/herdr.sock") {
		t.Fatalf("default socket path=%q", got)
	}
}
