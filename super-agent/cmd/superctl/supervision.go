package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"super-agent/internal/config"
	"super-agent/internal/herdr"
	"super-agent/internal/inflight"
	"super-agent/internal/inject"
)

// supervisionState 记住每个本地作业已经上报过的健康问题（ADR 0030 的监督对账），避免巡检每轮重复叫醒
// 主控。问题消失后条目被清掉，下次复发可以再报。
type supervisionState struct {
	Agents map[string]string `json:"agents"`
}

func loadSupervision(path string) supervisionState {
	st := supervisionState{Agents: map[string]string{}}
	readJSONFile(path, &st)
	if st.Agents == nil {
		st.Agents = map[string]string{}
	}
	return st
}

func saveSupervision(path string, st supervisionState) error {
	if st.Agents == nil {
		st.Agents = map[string]string{}
	}
	return writeJSONFile(path, st)
}

// localHealth 判定一个在途本地作业的机器可见健康状态。只看三样真值：agent 是否
// 在册、结果文件、会话文件最后写入时间；不读 transcript。
//
// 返回的 condition 为空表示健康；否则是一条稳定的状态键（用于去重）。
func localHealth(cfg config.Config, e inflight.Entry, agent herdr.Agent, alive bool, stallAfter time.Duration) (string, string) {
	// 交接进行中：旧 agent 已被停掉、旧条目还没被 Replace，短暂的空窗不是「丢失」。
	if !e.HandoffAt.IsZero() && time.Since(e.HandoffAt) < 10*time.Minute {
		return "", ""
	}
	result := e.Result
	if result == "" {
		result = filepath.Join(cfg.ResultsDir(), e.Agent+".md")
	}
	resultReady := fileNonEmpty(result)
	if !alive {
		if resultReady {
			return "exited-with-result", "agent 已退出，但结果文件已就绪；读结果后收口并移出在途清单"
		}
		return "lost", "agent 已退出且没有结果文件；按 brief 重派，或先问用户是否还要这件活"
	}
	if agent.AgentStatus == "working" && stallAfter > 0 {
		if fi, err := os.Stat(agent.AgentSession.Value); err == nil && time.Since(fi.ModTime()) > stallAfter {
			return "stalled", fmt.Sprintf("agent 在 working 但会话 %s 已 %s 没有写入；可能卡死。先 tell 问一句，或 cancel 后按 brief 重派",
				filepath.Base(agent.AgentSession.Value), time.Since(fi.ModTime()).Round(time.Minute))
		}
	}
	return "", ""
}

// scanLocalHealth 是巡检的监督对账：僵尸在途、崩溃丢结果、长时间无活动。
// 只告警，不自动 cancel——破坏性动作必须由主控或用户决定。
func scanLocalHealth(ctx context.Context, cfg config.Config, hc *herdr.Client) (int, error) {
	if !(gate{cfg: cfg}).allowMachine() {
		return 0, nil
	}
	entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath())
	if err != nil {
		return 0, err
	}
	locals := inflight.Local(entries)
	if len(locals) == 0 {
		return 0, nil
	}
	agents, err := hc.AgentList(ctx)
	if err != nil {
		return 0, err
	}
	stallAfter, _ := cfg.LocalStallAfter()
	statePath := cfg.SupervisionPath()
	st := loadSupervision(statePath)
	changed := false
	sent := 0
	seen := make(map[string]bool, len(locals))
	for _, e := range locals {
		if e.Agent == "" {
			continue
		}
		seen[e.Agent] = true
		a, alive := herdr.FindAgent(agents, e.Agent)
		condition, text := localHealth(cfg, e, a, alive, stallAfter)
		if condition == "" {
			if _, ok := st.Agents[e.Agent]; ok {
				delete(st.Agents, e.Agent)
				changed = true
			}
			continue
		}
		if st.Agents[e.Agent] == condition {
			continue
		}
		msg := fmt.Sprintf("本地作业 %s（%s）：%s", e.Agent, condition, text)
		if err := inject.Do(ctx, hc, cfg.MasterAgent, inject.BuildEvent(msg)); err != nil {
			if changed {
				_ = saveSupervision(statePath, st)
			}
			return sent, err
		}
		st.Agents[e.Agent] = condition
		changed = true
		sent++
	}
	for name := range st.Agents {
		if !seen[name] {
			delete(st.Agents, name)
			changed = true
		}
	}
	if changed {
		if err := saveSupervision(statePath, st); err != nil {
			return sent, err
		}
	}
	return sent, nil
}
