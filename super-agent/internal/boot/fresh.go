package boot

import (
	"context"
	"fmt"
	"time"

	"super-agent/internal/herdr"
)

// 换脑（ADR 0020/0022）：停掉主控进程 → 保证拓扑 → 同 pane 起新会话 → 灌首条 prompt。
// 同一 pane 复用而非关 pane 新建，是为了不动 ensure 的拓扑假设；新会话靠
// `--session-dir <本轮次目录>` 钉死，并对比 herdr 报出的会话路径确认真的换了脑。

// FreshTimeout 是等 agent 退出的上限；FreshPoll 是轮询间隔（测试里调小）。
var (
	FreshTimeout = 30 * time.Second
	FreshPoll    = 500 * time.Millisecond
	// FreshSettle 是「esc 之后等回合落定」的上限：落不下来就直接 ctrl+d，不拖满 FreshTimeout。
	FreshSettle = 5 * time.Second
)

// FreshReport 是一次换脑的结果，供调用方记账与告警。
type FreshReport struct {
	Agent        string `json:"agent"`
	StoppedVia   string `json:"stopped_via"` // send-keys | pane-close | absent | absent-after-pane-close
	OldSession   string `json:"old_session,omitempty"`
	NewSession   string `json:"new_session,omitempty"`
	SessionFresh bool   `json:"session_fresh"` // 新会话路径与旧的不同（旧为空时视为 fresh）
	Primed       bool   `json:"primed"`
}

// Fresh 执行一次换脑。agent 可以是主控（exp.MasterAgent）或复盘 agent（exp.RetroAgent）；
// prime 为空时用该 agent 的默认首条注入。sessionDir 是本轮次留档目录。
func Fresh(ctx context.Context, c *herdr.Client, exp Expected, agent, sessionDir, prime string) (*FreshReport, error) {
	rep := &FreshReport{Agent: agent}
	isMaster := agent == exp.MasterAgent || exp.MasterAgent == ""
	if !isMaster && agent != exp.RetroAgent {
		return nil, fmt.Errorf("fresh: %s 不在注册表派生的常驻 agent 里（%s / %s）", agent, exp.MasterAgent, exp.RetroAgent)
	}
	if prime == "" {
		if isMaster {
			prime = exp.MasterPrime
		} else {
			prime = exp.RetroPrime
		}
	}

	agents, err := c.AgentList(ctx)
	if err != nil {
		return nil, fmt.Errorf("fresh: agent list: %w", err)
	}
	if a, ok := herdr.FindAgent(agents, agent); ok {
		rep.OldSession = a.AgentSession.Value
		via, err := stopAgent(ctx, c, a)
		if err != nil {
			return nil, err
		}
		rep.StoppedVia = via
	} else {
		rep.StoppedVia = "absent"
	}

	// 拓扑可能会因关 pane 而整体消失（herdr：关掉最后一个 pane，workspace 随之消失），
	// 交给 Ensure 重建；启动参数带本轮次 session 目录。
	exp2 := exp
	if isMaster {
		exp2.MasterPrime, exp2.MasterSessionDir = prime, sessionDir
	} else {
		exp2.RetroPrime, exp2.RetroSessionDir = prime, sessionDir
	}
	if _, err := Ensure(ctx, c, exp2); err != nil {
		return nil, fmt.Errorf("fresh: ensure: %w", err)
	}
	rep.Primed = prime != ""

	agents, err = c.AgentList(ctx)
	if err != nil {
		return nil, fmt.Errorf("fresh: agent list after start: %w", err)
	}
	a, ok := herdr.FindAgent(agents, agent)
	if !ok {
		return nil, fmt.Errorf("fresh: %s 未起来（启动后 agent list 里没有它）", agent)
	}
	rep.NewSession = a.AgentSession.Value
	rep.SessionFresh = rep.OldSession == "" || rep.NewSession != rep.OldSession
	if !rep.SessionFresh {
		return rep, fmt.Errorf("fresh: 会话未更换（old=%s new=%s），换脑失败", rep.OldSession, rep.NewSession)
	}
	return rep, nil
}

// stopAgent 按阶梯停掉 agent：先中断当前回合（omp 在 working 时忽略 ctrl+d，实测），
// 再 ctrl+d 优雅退出，超时则关 pane。
func stopAgent(ctx context.Context, c *herdr.Client, a herdr.Agent) (string, error) {
	// 无条件先 esc：omp 在跑回合或缓冲区里还有排队输入时不理退出键（实机实测）。
	if err := c.AgentSendKeys(ctx, a.Name, "esc"); err != nil && a.AgentStatus != "idle" && a.AgentStatus != "done" {
		return "", fmt.Errorf("fresh: send-keys esc: %w", err)
	}
	if err := waitSettle(ctx, c, a.Name); err != nil {
		return "", err
	}
	// omp 的退出是 /exit：ctrl+d 在草稿非空时只删字符（TUI 自己会提示这一点）。
	if err := c.AgentPrompt(ctx, a.Name, "/exit", false, 0); err == nil {
		if gone, werr := waitGone(ctx, c, a.Name); werr != nil {
			return "", werr
		} else if gone {
			return "slash-exit", nil
		}
	}
	// 兜底：老版本/被占用的输入缓冲，用 ctrl+d 再试一次。
	_ = c.AgentSendKeys(ctx, a.Name, "ctrl+d")
	if gone, err := waitGone(ctx, c, a.Name); err != nil {
		return "", err
	} else if gone {
		return "send-keys", nil
	}

	if a.PaneID == "" {
		return "", fmt.Errorf("fresh: %s 未退出且没有 pane id 可关", a.Name)
	}
	if err := c.PaneClose(ctx, a.PaneID); err != nil {
		return "", fmt.Errorf("fresh: pane close %s: %w", a.PaneID, err)
	}
	if gone, err := waitGone(ctx, c, a.Name); err != nil {
		return "", err
	} else if gone {
		return "pane-close", nil
	}
	return "", fmt.Errorf("fresh: %s 在 ctrl+d 与关 pane 之后仍在册，需人工处理", a.Name)
}

// waitSettle 等 agent 从 working/blocked 落回可退出态（esc 之后通常 1–2 秒）。
func waitSettle(ctx context.Context, c *herdr.Client, name string) error {
	deadline := time.Now().Add(FreshSettle)
	for {
		agents, err := c.AgentList(ctx)
		if err != nil {
			return fmt.Errorf("fresh: agent list while interrupting: %w", err)
		}
		a, ok := herdr.FindAgent(agents, name)
		if !ok || (a.AgentStatus != "working" && a.AgentStatus != "blocked") {
			return nil
		}
		if time.Now().After(deadline) {
			return nil // 落不下来也不再拖：交给 ctrl+d / 关 pane 阶梯
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(FreshPoll):
		}
	}
}

// waitGone 轮询 agent list 直到目标名字消失（FreshTimeout 内）。
func waitGone(ctx context.Context, c *herdr.Client, name string) (bool, error) {
	deadline := time.Now().Add(FreshTimeout)
	for {
		agents, err := c.AgentList(ctx)
		if err != nil {
			return false, fmt.Errorf("fresh: agent list while stopping: %w", err)
		}
		if _, ok := herdr.FindAgent(agents, name); !ok {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(FreshPoll):
		}
	}
}
