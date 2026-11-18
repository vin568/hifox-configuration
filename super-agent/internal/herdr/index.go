package herdr

import (
	"context"
	"fmt"
)

// Topology 是一次对账的拓扑视图：workspace 标签 → id、workspace → pane、pane → agent。
// 事件订阅、派发选 pane、急停的机器真值三处都要「按 label 找 pane」，共用这一份，
// 免得各写一套解析、各拉一遍 CLI（原先三套实现、每处 3–5 次调用）。
type Topology struct {
	Labels map[string]string   // label -> workspace_id
	Panes  map[string][]string // workspace_id -> pane ids（按快照顺序，首个是 root）
	ByPane map[string]Agent    // pane -> agent
}

// Topology 拉一次 workspace 列表 + 快照 + agent 列表。
func (c *Client) Topology(ctx context.Context) (Topology, error) {
	ws, err := c.WorkspaceList(ctx)
	if err != nil {
		return Topology{}, fmt.Errorf("workspace list: %w", err)
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return Topology{}, fmt.Errorf("snapshot: %w", err)
	}
	agents, err := c.AgentList(ctx)
	if err != nil {
		return Topology{}, fmt.Errorf("agent list: %w", err)
	}
	t := Topology{
		Labels: make(map[string]string, len(ws)),
		Panes:  make(map[string][]string, len(snap.Layouts)),
		ByPane: make(map[string]Agent, len(agents)),
	}
	for _, w := range ws {
		t.Labels[w.Label] = w.WorkspaceID
	}
	for _, l := range snap.Layouts {
		for _, p := range l.Panes {
			t.Panes[l.WorkspaceID] = append(t.Panes[l.WorkspaceID], p.PaneID)
		}
	}
	for _, a := range agents {
		if a.PaneID != "" {
			t.ByPane[a.PaneID] = a
		}
	}
	return t, nil
}

// RootPane 返回某 label 的 workspace 根 pane；空串表示 workspace 或 pane 不存在。
func (t Topology) RootPane(label string) string {
	id := t.Labels[label]
	if id == "" {
		return ""
	}
	if panes := t.Panes[id]; len(panes) > 0 {
		return panes[0]
	}
	return ""
}

// PanesOf 返回某 label 的 workspace 全部 pane。
func (t Topology) PanesOf(label string) []string {
	return t.Panes[t.Labels[label]]
}

// FindAgent 按具名或底层名在 agent 列表里找（与 boot/herdr 两处原先的各自实现同一语义）。
func FindAgent(agents []Agent, name string) (Agent, bool) {
	for _, a := range agents {
		if a.Name == name || a.Agent == name {
			return a, true
		}
	}
	return Agent{}, false
}

// PaneIndex 是一次对账后的 pane 视图：主控 pane（按 workspace 拓扑解析，
// 不依赖 agent 名字存活）+ 全部在册 agent 按 pane 的索引。buildSubs 与
// 事件处理共用，消除两处各写一套反查且判定不一致的问题。
type PaneIndex struct {
	MasterPane string
	ByPane     map[string]Agent
}

func (c *Client) PaneIndex(ctx context.Context, masterWorkspaceLabel string) (PaneIndex, error) {
	t, err := c.Topology(ctx)
	if err != nil {
		return PaneIndex{}, err
	}
	return PaneIndex{
		MasterPane: t.RootPane(masterWorkspaceLabel),
		ByPane:     t.ByPane,
	}, nil
}
