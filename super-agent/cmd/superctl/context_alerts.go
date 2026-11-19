package main

import (
	"context"
	"fmt"
	"log/slog"

	"super-agent/internal/config"
	"super-agent/internal/ctxmeter"
	"super-agent/internal/herdr"
	"super-agent/internal/inject"
)

// contextAlertState 是告警去重状态（ADR 0030）：按 agent 记当前会话、压缩代数和已发出的级别。
// 只有注入成功才落盘——主控 blocked 时下一轮会重试，不会静默丢掉。
type contextAlertState struct {
	Agents map[string]contextAlert `json:"agents"`
}

type contextAlert struct {
	Session string `json:"session"`
	Epoch   int    `json:"epoch"`
	Level   string `json:"level"` // warn | critical
}

func contextAlertLevel(s ctxmeter.Snapshot, p ctxmeter.Policy) string {
	if !s.Known() {
		return ""
	}
	if s.Used() >= p.Ceiling(s.WindowTokens) {
		return "critical"
	}
	if s.Used() >= p.WarnAt(s.WindowTokens) {
		return "warn"
	}
	return ""
}

func alertLevelRank(level string) int {
	switch level {
	case "critical":
		return 2
	case "warn":
		return 1
	default:
		return 0
	}
}

func loadContextAlerts(path string) contextAlertState {
	st := contextAlertState{Agents: map[string]contextAlert{}}
	readJSONFile(path, &st)
	if st.Agents == nil {
		st.Agents = map[string]contextAlert{}
	}
	return st
}

func saveContextAlerts(path string, st contextAlertState) error {
	if st.Agents == nil {
		st.Agents = map[string]contextAlert{}
	}
	return writeJSONFile(path, st)
}

func contextAlertText(c controlledContext, level string) string {
	s := c.Snapshot
	pct := s.Ratio() * 100
	action := "用 superctl checkpoint 写进度，然后 handoff 或收口"
	switch c.Target.Status {
	case "working":
		action = "等当前回合结束再处理；不要 mid-turn 打断"
	case "blocked":
		action = "先让用户处理确认界面；不 handoff"
	case "done", "idle":
		action = "用 superctl checkpoint 写进度，然后 superctl handoff 或收口"
	}
	return fmt.Sprintf(
		"[触发器: context] 本地作业 %s（%s）上下文 %d/%d（%.0f%%，%s），模型 %s/%s；结果：%s；brief：%s；progress：%s。建议：%s。",
		c.Target.Name, c.Target.Status, s.Used(), s.WindowTokens, pct, level,
		c.Snapshot.Provider, c.Snapshot.Model,
		c.Target.ResultPath, c.Target.BriefPath, c.Target.ProgressPath, action,
	)
}

// scanContextAlerts 只对在途 job 发告警；master/retro 走自动轮换。返回本轮成功
// 注入的条数。急停期间不注入也不推进状态，恢复后补交。
func scanContextAlerts(ctx context.Context, cfg config.Config, hc *herdr.Client) (int, error) {
	if !(gate{cfg: cfg}).allowMachine() {
		return 0, nil
	}
	measured, err := measureControlled(ctx, cfg, hc)
	if err != nil {
		return 0, err
	}
	policy := contextPolicy(cfg)
	statePath := cfg.ContextAlertsPath()
	st := loadContextAlerts(statePath)
	changed := false
	sent := 0
	for _, c := range measured {
		if c.Target.Role != "job" {
			continue
		}
		level := contextAlertLevel(c.Snapshot, policy)
		if level == "" {
			continue
		}
		old, ok := st.Agents[c.Target.Name]
		if ok && old.Session == c.Target.SessionPath && old.Epoch == c.Snapshot.Epoch &&
			alertLevelRank(old.Level) >= alertLevelRank(level) {
			continue
		}
		if err := inject.Do(ctx, hc, cfg.MasterAgent, contextAlertText(c, level)); err != nil {
			// 注入失败（主控 blocked/急停/拓扑问题）：不推进这条的状态，但已成功的
			// 条目要落盘，否则下一轮会重复叫醒。
			if changed {
				_ = saveContextAlerts(statePath, st)
			}
			return sent, err
		}
		st.Agents[c.Target.Name] = contextAlert{
			Session: c.Target.SessionPath,
			Epoch:   c.Snapshot.Epoch,
			Level:   level,
		}
		changed = true
		sent++
	}
	if changed {
		if err := saveContextAlerts(statePath, st); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

// pruneContextAlerts 清掉已不在受控集合里的旧条目，避免状态文件只增不减。
func pruneContextAlerts(ctx context.Context, cfg config.Config, hc *herdr.Client) {
	measured, err := measureControlled(ctx, cfg, hc)
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, c := range measured {
		live[c.Target.Name] = true
	}
	st := loadContextAlerts(cfg.ContextAlertsPath())
	changed := false
	for name := range st.Agents {
		if !live[name] {
			delete(st.Agents, name)
			changed = true
		}
	}
	if changed {
		if err := saveContextAlerts(cfg.ContextAlertsPath(), st); err != nil {
			slog.Warn("superctl: prune context alerts failed", "error", err)
		}
	}
}

// contextBriefLines 是 handoff/status 的人类可读摘要；用于 handoff.md 与日志。
func contextBriefLines(measured []controlledContext) []string {
	lines := make([]string, 0, len(measured))
	for _, c := range measured {
		lines = append(lines, "- "+c.brief())
	}
	return lines
}
