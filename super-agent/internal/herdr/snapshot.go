package herdr

import (
	"context"
	"encoding/json"
	"fmt"
)

// Snapshot 对应 `herdr api snapshot` 的 `.result.snapshot`：layouts 按workspace给出 pane 拓扑。
type Snapshot struct {
	Layouts []Layout `json:"layouts"`
}

type Layout struct {
	WorkspaceID string    `json:"workspace_id"`
	Panes       []PaneRef `json:"panes"`
}

type snapshotResp struct {
	Result struct {
		Snapshot Snapshot `json:"snapshot"`
	} `json:"result"`
}

func decodeSnapshot(data string) (Snapshot, error) {
	var r snapshotResp
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	return r.Result.Snapshot, nil
}

func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	out, err := c.Run(ctx, []string{"api", "snapshot"})
	if err != nil {
		return Snapshot{}, err
	}
	return decodeSnapshot(out)
}

// RootPane 返回 workspace 的第一个 pane（root pane）。空串表示没找到。
func (s Snapshot) RootPane(workspaceID string) string {
	for _, l := range s.Layouts {
		if l.WorkspaceID == workspaceID {
			for _, p := range l.Panes {
				return p.PaneID
			}
		}
	}
	return ""
}
