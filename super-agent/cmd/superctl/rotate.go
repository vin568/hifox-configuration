package main

import (
	"context"
	"log/slog"
	"os"

	"super-agent/internal/config"
	"super-agent/internal/herdr"
)

// cmdNew 人工换脑：不比阈值，立刻换新会话（/new 的命令行等价物）。
func cmdNew(args []string) error {
	cfg, _, err := loadConfigWithAgents(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)
	rep, err := freshBrain(ctx, cfg, hc, cfg.MasterAgent, "new", "")
	if err != nil {
		return err
	}
	return jsonPrint(rep)
}

// maybeRotate 是轮换（ADR 0022）：常驻 agent 的会话文件超过阈值且它 idle 时换脑。
// 交接材料由机器真值生成（在途清单 + 活跃区台账），新会话读它恢复状态——
// 跨会话记忆只有这两处，所以轮换不依赖任何 LLM 自述。
func maybeRotate(ctx context.Context, cfg config.Config, hc *herdr.Client) error {
	if cfg.SessionMaxBytes <= 0 {
		return nil
	}
	exp, err := expectedTopo(cfg)
	if err != nil {
		return err
	}
	agents, err := hc.AgentList(ctx)
	if err != nil {
		return err
	}
	names := []string{exp.MasterAgent}
	if exp.RetroAgent != "" {
		names = append(names, exp.RetroAgent)
	}
	for _, name := range names {
		a, ok := herdr.FindAgent(agents, name)
		if !ok || !isIdle(a.AgentStatus) {
			continue
		}
		size := sessionBytes(a.AgentSession.Value)
		if size < cfg.SessionMaxBytes {
			continue
		}
		intent := "本会话是新的一轮（上一轮的上下文已按体积归档）。先把在途状态读回来：" + handoffPath(ctx, cfg, hc) +
			"；再按活跃区台账继续手上的事，不要重复已经做完的。"
		if name == exp.RetroAgent {
			intent = "本会话是新的一轮。先读活跃区台账与结果目录，准备下一轮复盘；不要面向用户说话。"
		}
		if _, err := freshBrain(ctx, cfg, hc, name, "rotate", intent); err != nil {
			return err
		}
		slog.Info("superctl: 会话超阈值已换脑", "agent", name, "bytes", size, "threshold", cfg.SessionMaxBytes)
	}
	return nil
}

// isIdle 判定「此刻换脑不会砍掉正在干的工作」。done 是 settled 态（回合结束），
// unknown 不算——宁可这一轮不换，也不在半途换。
func isIdle(status string) bool {
	return status == "idle" || status == "done"
}

// handoffPath 写一份机器真值交接文件并返回它的路径（轮换的新会话先读它）。
func handoffPath(ctx context.Context, cfg config.Config, hc *herdr.Client) string {
	path := cfg.HandoffPath()
	body := "# 换脑交接（机器真值）\n\n" + collectTruth(ctx, cfg, hc).forMaster(cfg)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		slog.Warn("superctl: write handoff failed", "error", err)
	}
	return path
}

func sessionBytes(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
