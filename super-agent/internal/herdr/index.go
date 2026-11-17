package herdr

import (
	"context"
	"fmt"
)

// PaneIndex 是一次对账后的 pane 视图：主控 pane（按 workspace 拓扑解析，
// 不依赖 agent 名字存活）+ 全部在册 agent 按 pane 的索引。buildSubs 与
// 事件处理共用，消除两处各写一套反查且判定不一致的问题。
type PaneIndex struct {
	MasterPane string
	ByPane     map[string]Agent
}

func (c *Client) PaneIndex(ctx context.Context, masterWorkspaceLabel string) (PaneIndex, error) {
	ws, err := c.WorkspaceList(ctx)
	if err != nil {
		return PaneIndex{}, fmt.Errorf("workspace list: %w", err)
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return PaneIndex{}, fmt.Errorf("snapshot: %w", err)
	}
	agents, err := c.AgentList(ctx)
	if err != nil {
		return PaneIndex{}, fmt.Errorf("agent list: %w", err)
	}
	idx := PaneIndex{ByPane: make(map[string]Agent, len(agents))}
	for _, w := range ws {
		if w.Label == masterWorkspaceLabel {
			idx.MasterPane = snap.RootPane(w.WorkspaceID)
		}
	}
	for _, a := range agents {
		if a.PaneID != "" {
			idx.ByPane[a.PaneID] = a
		}
	}
	return idx, nil
}
