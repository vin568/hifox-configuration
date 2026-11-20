package feishu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"super-agent/internal/inject"
)

const (
	quoteTimeout         = 30 * time.Second // 整段引用解析（取消息 + 引用附件落盘）总预算
	quoteDownloadTimeout = 15 * time.Second // 单个引用附件；与总预算共同兜底
	quoteMaxRunes        = 2000             // 引用正文上限（rune）
	quoteMaxMedia        = 4                // 超出只保留 key 占位，防一次回复扇出十几次下载
)

// quoteTargetID 选要取的消息 id：parent 优先，其次 root；与自身相同视为无引用。
func quoteTargetID(ev *eventInfo) string {
	switch {
	case ev == nil:
		return ""
	case ev.parentID != "" && ev.parentID != ev.messageID:
		return ev.parentID
	case ev.rootID != "" && ev.rootID != ev.messageID:
		return ev.rootID
	}
	return ""
}

// resolveQuote 取被引用消息并渲染成可注入的引用块。任何失败都返回带原因的块，
// 返回 nil 只表示「本条不是回复」。
func (s *server) resolveQuote(ctx context.Context, ev *eventInfo) *inject.Quote {
	target := quoteTargetID(ev)
	if target == "" {
		return nil
	}
	if s.quote == nil {
		// 没有只读客户端（probe / 单测路径）：按装配约定完全不解析引用。
		return nil
	}
	view, err := s.quote.Get(ctx, target)
	if err != nil {
		return &inject.Quote{MessageID: target, Text: quoteFailureText(err)}
	}
	if view.Deleted {
		return &inject.Quote{MessageID: target, SenderID: view.SenderID, Text: "（引用消息已撤回）"}
	}
	body, media := extractMessage(view.MsgType, view.Content, view.Mentions)
	if s.media != nil {
		for i, m := range media {
			if i >= quoteMaxMedia {
				slog.Warn("feishu: 引用附件超过上限，保留 key 占位",
					"message_id", view.MessageID, "key", m.Key, "limit", quoteMaxMedia)
				continue
			}
			dctx, cancel := context.WithTimeout(ctx, quoteDownloadTimeout)
			path, derr := s.media.Save(dctx, view.MessageID, m)
			cancel()
			if derr != nil {
				// 与 ADR 0024 一致：附件失败只保留 key 占位，绝不丢引用正文。
				slog.Warn("feishu: 引用附件下载失败，保留 key 占位",
					"message_id", view.MessageID, "key", m.Key, "error", derr)
				continue
			}
			body = replaceMediaToken(body, m, path)
		}
	}
	return &inject.Quote{MessageID: target, SenderID: view.SenderID, Text: truncateQuote(body)}
}

// quoteFailureText 把取消息失败翻译成带原因的一行，主控据此知道「这条是回复、内容缺失」。
func quoteFailureText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "（引用消息读取超时）"
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Code == 230027 {
			return fmt.Sprintf("（引用消息读取失败：缺权限，需补 im:message:readonly；code=%d msg=%s request_id=%s）", apiErr.Code, apiErr.Msg, apiErr.RequestID)
		}
		return fmt.Sprintf("（引用消息读取失败：code=%d msg=%s request_id=%s）", apiErr.Code, apiErr.Msg, apiErr.RequestID)
	}
	return fmt.Sprintf("（引用消息读取失败：%s）", err.Error())
}

// truncateQuote 按 rune 截断：2001 个汉字不能按字节切成半截 UTF-8。
func truncateQuote(text string) string {
	r := []rune(text)
	if len(r) <= quoteMaxRunes {
		return text
	}
	return string(r[:quoteMaxRunes]) + "\n…（引用正文超过 2000 字，已截断；完整内容见飞书客户端）"
}
