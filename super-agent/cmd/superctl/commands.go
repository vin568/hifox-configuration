package main

import (
	"context"
	"log/slog"
	"strings"

	"super-agent/internal/config"
	"super-agent/internal/halt"
	"super-agent/internal/herdr"
	"super-agent/internal/outbox"
)

// commandIndices 是运行时自处理的私聊指令（ADR 0020/0022）。
type commandIndices struct {
	cfg config.Config
	hc  *herdr.Client
	ob  *outbox.Outbox
}

// absorb 消化一条指令；false = 不是指令（交回正常注入路径）。
// 指令永不进注入队列：急停不能排在错误指令后面等主控自己读。
func (c commandIndices) absorb(ctx context.Context, chatID, text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/halt":
		reason := strings.TrimSpace(strings.TrimPrefix(text, "/halt"))
		if _, err := doHalt(ctx, c.cfg, c.hc, c.ob, chatID, reason, chatID); err != nil {
			c.notice(chatID, "我已经停下了，但收拾现场时出了点问题，先别派新活：\n"+err.Error())
			return true
		}
		return true
	case "/new":
		// /new 也是一句人话：它同样解除急停（用户下一句私聊即恢复）。
		if _, err := freshBrain(ctx, c.cfg, c.hc, c.cfg.MasterAgent, "new", ""); err != nil {
			slog.Warn("superctl: fresh brain failed", "error", err)
			c.notice(chatID, "想把手头重新理一遍，但没弄成，我再试一次。")
			return true
		}
		if st, _, _ := halt.Load(c.cfg.HaltPath()); halt.Active(c.cfg.HaltPath()) {
			if err := halt.Release(c.cfg.HaltPath(), halt.Watermark{HaltAt: st.At}); err != nil {
				slog.Warn("superctl: /new 解除急停失败（保持停住）", "error", err)
			}
		}
		c.notice(chatID, "收到，我把手头重新理一遍，有事说。")
		return true
	}
	return false
}

func (c commandIndices) notice(chatID, text string) {
	if _, err := c.ob.Append(chatID, text); err != nil {
		slog.Warn("superctl: notice append failed", "error", err)
	}
}
