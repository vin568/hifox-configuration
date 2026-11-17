package inject

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/herdr"
)

// ErrBlocked：主控停在审批/提问 UI，prompt 被整体拒绝——调用方应通知用户，不要原样重试。
var ErrBlocked = errors.New("master blocked at approval prompt")

// Payload 是一次消息/事件注入的载荷。ProxyReply=true 表示代答场景（群内 @ 用户本人）。
type Payload struct {
	ChatID     string
	SenderID   string
	Text       string
	Kind       string // dm | group
	ProxyReply bool
}

// BuildFeishu 把飞书消息拼成注入主控的信封：来源会话 + 原话。
// 主控的操作手册在会话常驻，这里不重复纪律。
func BuildFeishu(p Payload) string {
	label := "私聊"
	switch {
	case p.ProxyReply:
		label = "群聊（代答：对方 @ 的是用户本人，以用户口吻回复，不暴露身份）"
	case p.Kind == "group":
		label = "群聊"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[触发器: feishu] 来源会话 %s（%s）\n", p.ChatID, label)
	if p.SenderID != "" {
		fmt.Fprintf(&b, "发送者 %s\n", p.SenderID)
	}
	fmt.Fprintf(&b, "<current_message>\n%s\n</current_message>\n", p.Text)
	b.WriteString("按操作手册处理；需要回复用户时用 superctl reply。")
	return b.String()
}

// BuildEvent 拼装状态变化事件信封（herdr plugin 钩子用）。
func BuildEvent(detail string) string {
	return fmt.Sprintf("[触发器: event] 能力 agent 状态变化：%s。对照作业台账判断是否需要动作；无事不回复用户。", detail)
}

// BuildEvent 拼装本地能力 agent 状态变化事件信封（serve 的事件订阅用）。
// Do 把任意文本注入主控（不 --wait）。blocked 时返回 ErrBlocked。
func Do(ctx context.Context, c *herdr.Client, target, text string) error {
	err := c.AgentPrompt(ctx, target, text, false, 0)
	if herdr.IsBlocked(err) {
		return fmt.Errorf("%w: %v", ErrBlocked, err)
	}
	return err
}
