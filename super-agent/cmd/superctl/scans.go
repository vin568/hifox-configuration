package main

import (
	"context"
	"log/slog"

	"super-agent/internal/config"
	"super-agent/internal/herdr"
)

// runSupervisionScans 是 tick/serve 共用的机器侧巡检序列：上下文告警、状态清理、
// 本地作业健康对账。三件事都只注入/告警，不做破坏性动作；顺序固定，避免两处
// 各自复制一段而逐渐分叉（ADR 0030）。
func runSupervisionScans(ctx context.Context, cfg config.Config, hc *herdr.Client) (contextAlerts, healthAlerts int) {
	if sent, err := scanContextAlerts(ctx, cfg, hc); err != nil {
		slog.Warn("superctl: context alerts failed", "error", err)
	} else {
		contextAlerts = sent
	}
	pruneContextAlerts(ctx, cfg, hc)
	if sent, err := scanLocalHealth(ctx, cfg, hc); err != nil {
		slog.Warn("superctl: local health scan failed", "error", err)
	} else {
		healthAlerts = sent
	}
	return contextAlerts, healthAlerts
}
