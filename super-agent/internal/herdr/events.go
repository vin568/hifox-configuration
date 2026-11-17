package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"
)

// Event 是 events.subscribe 推送的一行事件（实机形状：
// {"event":"pane.agent_status_changed","data":{"agent":"omp","agent_status":"working","pane_id":"w3:p1","workspace_id":"w3"}}）。
type Event struct {
	Event string    `json:"event"`
	Data  EventData `json:"data"`
}

type EventData struct {
	Agent       string `json:"agent"` // 底层可执行名（omp）；具名要靠 agent list 按 pane 反查
	AgentStatus string `json:"agent_status"`
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
}

// Subscription 是一条订阅项。herdr 契约：pane.agent_status_changed 必须
// 指定 pane_id（不支持通配），且引用不存在的 pane 会让整个订阅请求被拒。
type Subscription map[string]string

// SocketPath 返回 session 的 herdr socket 路径（默认 session 名为空）。
func SocketPath(session string) string {
	if session == "" {
		return expandTilde("~/.config/herdr/herdr.sock")
	}
	return expandTilde("~/.config/herdr/sessions/" + session + "/herdr.sock")
}

func expandTilde(p string) string {
	home, err := userHomeDir()
	if err != nil {
		return p
	}
	if p == "~" || len(p) >= 2 && p[:2] == "~/" {
		return home + p[1:]
	}
	return p
}

// Subscribe 持有 session 的事件订阅直到 ctx 取消。buildSubs 每轮连接前重建
// 订阅面（调用方给「除主控外所有带 agent 的 pane」），连接最长 refreshTtl 后
// 主动重连——pane 增删由此收敛，掉线期间的状态由巡检/哨兵兜底（事件不持久）。
func Subscribe(ctx context.Context, session string, buildSubs func() []Subscription, onEvent func(Event) error) error {
	const refreshTtl = 60 * time.Second
	for ctx.Err() == nil {
		subs := buildSubs()
		if len(subs) == 0 {
			select {
			case <-ctx.Done():
			case <-time.After(30 * time.Second):
			}
			continue
		}
		err := subscribeOnce(ctx, session, subs, refreshTtl, onEvent)
		if err != nil && ctx.Err() == nil {
			slog.Warn("herdr: event subscription ended, reconnecting", "error", err, "panes", len(subs))
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}
	return ctx.Err()
}

func subscribeOnce(ctx context.Context, session string, subs []Subscription, ttl time.Duration, onEvent func(Event) error) error {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", SocketPath(session))
	if err != nil {
		return fmt.Errorf("dial %s: %w", SocketPath(session), err)
	}
	defer conn.Close()

	type subFrame struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params struct {
			Subscriptions []Subscription `json:"subscriptions"`
		} `json:"params"`
	}
	frame := subFrame{ID: "superctl_events", Method: "events.subscribe"}
	frame.Params.Subscriptions = subs
	b, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return err
	}

	// ttl 到点主动断开重连（重建订阅面）；确认帧/错误帧/推送帧都按行处理。
	deadline := time.Now().Add(ttl)
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		_ = conn.SetReadDeadline(deadline)
		if !sc.Scan() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(sc.Err(), os.ErrDeadlineExceeded) {
				return nil // ttl 正常轮换，外层重建订阅面
			}
			if sc.Err() == nil {
				// 干净 EOF（对端关连接）：当错误处理走退避，否则热循环重连。
				return errors.New("herdr: event connection closed unexpectedly")
			}
			return sc.Err()
		}
		line := sc.Bytes()
		var envelope struct {
			ID    string `json:"id"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}
		if envelope.Error != nil {
			return fmt.Errorf("subscribe error %s: %s", envelope.Error.Code, envelope.Error.Message)
		}
		if envelope.ID != "" {
			continue // 响应帧（订阅确认等），非推送
		}
		var ev Event
		if json.Unmarshal(line, &ev) == nil && ev.Event != "" {
			if err := onEvent(ev); err != nil {
				return err
			}
		}
	}
}

func userHomeDir() (string, error) { return os.UserHomeDir() }
