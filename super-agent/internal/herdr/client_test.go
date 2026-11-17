package herdr

import (
	"strings"
	"testing"
)

const agentListJSON = `{"id":"cli:agent:list","result":{"agents":[
 {"agent":"omp","name":"songjiang","agent_status":"idle","pane_id":"w2:p1","cwd":"/repo"},
 {"agent":"omp","name":"jiangjing","agent_status":"working","pane_id":"w1:p1","cwd":"/other"}
]}}`

const workspaceListJSON = `{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[
 {"workspace_id":"w1","label":"hifox-configuration-super-agent","pane_count":1,"tab_count":1},
 {"workspace_id":"w2","label":"master","pane_count":1,"tab_count":1}
]}}`

const wsCreateJSON = `{"id":"cli:workspace:create","result":{
 "workspace":{"workspace_id":"w3","label":"crew"},
 "root_pane":{"pane_id":"w3:p1"}}}`

func TestDecodeAgentList(t *testing.T) {
	agents, err := decodeAgentList(agentListJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 || agents[0].Name != "songjiang" || agents[0].Agent != "omp" || agents[0].AgentStatus != "idle" {
		t.Fatalf("unexpected agents: %+v", agents)
	}
}

func TestDecodeWorkspaceList(t *testing.T) {
	ws, err := decodeWorkspaceList(workspaceListJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[1].Label != "master" || ws[1].WorkspaceID != "w2" {
		t.Fatalf("unexpected workspaces: %+v", ws)
	}
}

func TestDecodeWsCreate(t *testing.T) {
	w, p, err := decodeWsCreate(wsCreateJSON)
	if err != nil {
		t.Fatal(err)
	}
	if w.WorkspaceID != "w3" || w.Label != "crew" || p.PaneID != "w3:p1" {
		t.Fatalf("unexpected: %+v %+v", w, p)
	}
}

func TestWrapErrStructured(t *testing.T) {
	err := wrapErr(nilErr(), `{"id":"x","error":{"code":"agent_blocked","message":"agent is blocked"}}`)
	ce, ok := err.(*CmdError)
	if !ok || ce.Code != "agent_blocked" {
		t.Fatalf("expected CmdError agent_blocked, got %#v", err)
	}
	if !IsBlocked(err) {
		t.Fatal("IsBlocked should be true for agent_blocked")
	}
}

func TestWrapErrUnstructured(t *testing.T) {
	err := wrapErr(nilErr(), "plain failure\nsecond line")
	if IsBlocked(err) {
		t.Fatal("IsBlocked should be false for unstructured error")
	}
}

func TestIsBlockedNil(t *testing.T) {
	if IsBlocked(nil) {
		t.Fatal("IsBlocked(nil) must be false")
	}
}

func TestHerdrEnvStripsInherited(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin",
		"HERDR_SOCKET_PATH=/wrong/sock",
		"HERDR_SESSION=other",
	}
	got := herdrEnv(environ, "super-agent")
	joined := "\n" + strings.Join(got, "\n") + "\n"
	if strings.Contains(joined, "HERDR_SOCKET_PATH=") || strings.Contains(joined, "HERDR_SESSION=other") {
		t.Fatalf("inherited herdr vars must be stripped: %v", got)
	}
	if !strings.Contains(joined, "\nHERDR_SESSION=super-agent\n") {
		t.Fatalf("target session missing: %v", got)
	}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "exit 1" }

func nilErr() error { return fakeErr{} }
