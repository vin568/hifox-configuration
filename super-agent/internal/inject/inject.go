package inject

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"super-agent/internal/herdr"
)

// ErrBlocked：主控停在审批/提问 UI，prompt 被整体拒绝——调用方应通知用户，不要原样重试。
var ErrBlocked = errors.New("master blocked at approval prompt")

// Quote 是被回复消息的引用块（空 = 本条不是回复，输出与旧版逐字节一致）。
type Quote struct {
	MessageID string
	SenderID  string
	Text      string // 已提取的正文；附件占位已改写成本地路径
}

// Payload 是一次消息/事件注入的载荷。ProxyReply=true 表示代答场景（群内 @ 用户本人）。
type Payload struct {
	ChatID     string
	SenderID   string
	Text       string
	Kind       string // dm | group
	ProxyReply bool
	// Quote 是用户「回复」的那条消息（nil = 普通消息）。渲染在 <current_message> 之前：
	// 主控必须知道这条是回复，否则会把「这个再改一下」当成没有指代的新需求。
	Quote *Quote
	// ReceivedAt 是事件被接纳的时间（去重 claim 时刻）。重试队列原样携带它，
	// 于是急停可以丢弃「落闸之前就排队」的旧指令（ADR 0020）。
	ReceivedAt time.Time
}

// BuildFeishu 把飞书消息拼成注入主控的信封：来源会话 + 原话。
// 注入卫生（ADR 0022）：纪律与路径只在首条 prime 出现，这里只带本条消息的增量。
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
	if p.Quote != nil {
		writeQuote(&b, p.Quote)
	}
	fmt.Fprintf(&b, "<current_message>\n%s\n</current_message>", p.Text)
	return b.String()
}

// writeQuote 渲染被回复消息的引用块：message_id 恒输出，from 有值才输出。
// 块内正文为空时给占位，主控不会把空块当成「父消息就是空的」。
func writeQuote(b *strings.Builder, q *Quote) {
	if q.SenderID != "" {
		fmt.Fprintf(b, "<quoted_message message_id=%q from=%q>\n", q.MessageID, q.SenderID)
	} else {
		fmt.Fprintf(b, "<quoted_message message_id=%q>\n", q.MessageID)
	}
	text := q.Text
	if strings.TrimSpace(text) == "" {
		text = "（引用消息正文为空）"
	}
	fmt.Fprintf(b, "%s\n</quoted_message>\n", text)
}

// BuildEvent 拼装本地能力 agent 状态变化事件信封（serve 的事件订阅用）。
func BuildEvent(detail string) string {
	return fmt.Sprintf("[触发器: event] 能力 agent 状态变化：%s", detail)
}

// BuildAgentReport 拼装能力 agent 主动回报的信封（ADR 0029）：与 feishu/event/sweep
// 并列的触发器，主控据此知道这是内部回报，不是用户说话。
func BuildAgentReport(agent, text string) string {
	return fmt.Sprintf("[触发器: agent] %s：%s", agent, text)
}

// BuildHaltIntent 拼装换脑后的首条注入：用户按了急停，当前会话是新起的，
// 手上有什么由运行时给出的机器真值清单回答。要求主控逐件请示是否停，
// 用户回话前不自动继续（ADR 0020）。
func BuildHaltIntent(receipt, oldSession string) string {
	var b strings.Builder
	b.WriteString("[触发器: halt] 用户刚按了急停。你原来那个会话已经被停掉，现在这个是新会话：\n")
	b.WriteString(receipt)
	if oldSession != "" {
		fmt.Fprintf(&b, "\n旧会话留档：%s（需要时读尾部几行了解被打断的是什么，不必重放）\n", oldSession)
	}
	b.WriteString("现在做三件事：把手上这些按业务说清楚（哪件其实跑完了、哪件可以先放着）、" +
		"逐件问用户要不要停、等用户回话。在用户回话之前不要继续任何作业，也不要自己启动新活。")
	return b.String()
}

// Do 把任意文本注入主控（不 --wait）。blocked 时返回 ErrBlocked。
func Do(ctx context.Context, c *herdr.Client, target, text string) error {
	err := c.AgentPrompt(ctx, target, text, false, 0)
	if herdr.IsBlocked(err) {
		return fmt.Errorf("%w: %v", ErrBlocked, err)
	}
	return err
}
