package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

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

// maybeRotate 是轮换（ADR 0022/0030）：常驻 agent 的上下文占用超过保留区且它
// idle 时换脑。主判定用 provider 回报的 token / 模型窗口；只有测量不可用（旧会话、
// 非 path 形态、模型窗口未知）时才退回 session_max_bytes 字节兜底。
func maybeRotate(ctx context.Context, cfg config.Config, hc *herdr.Client) error {
	policy := contextPolicy(cfg)
	measured, err := measureControlled(ctx, cfg, hc)
	if err != nil {
		return err
	}
	for _, c := range measured {
		name := c.Target.Name
		if c.Target.Role != "master" && c.Target.Role != "retro" {
			continue
		}
		if !isIdle(c.Target.Status) {
			continue
		}
		reason := ""
		if c.Snapshot.Trusted() {
			used := c.Snapshot.Used()
			ceiling := policy.Ceiling(c.Snapshot.WindowTokens)
			if ceiling <= 0 || used < ceiling {
				continue
			}
			reason = fmt.Sprintf("context %d/%d (%.0f%%)", used, c.Snapshot.WindowTokens, c.Snapshot.Ratio()*100)
		} else {
			// 测量不可用：保留旧的字节口径作安全网；0 = 关闭。
			if cfg.SessionMaxBytes <= 0 {
				continue
			}
			size := sessionBytes(c.Target.SessionPath)
			if size < cfg.SessionMaxBytes {
				continue
			}
			reason = fmt.Sprintf("session bytes %d/%d", size, cfg.SessionMaxBytes)
		}
		intent := "本会话是新的一轮（上一轮的上下文已按体积归档）。先把在途状态读回来：" + handoffPath(ctx, cfg, hc) +
			"；再按活跃区台账继续手上的事，不要重复已经做完的。"
		if c.Target.Role == "retro" {
			intent = "本会话是新的一轮。先读活跃区台账与结果目录，准备下一轮复盘；不要面向用户说话。"
		}
		if _, err := freshBrain(ctx, cfg, hc, name, "rotate", intent); err != nil {
			return err
		}
		slog.Info("superctl: 上下文超阈值已换脑", "agent", name, "reason", reason)
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
	if measured, err := measureControlled(ctx, cfg, hc); err == nil {
		if lines := contextBriefLines(measured); len(lines) > 0 {
			body += "\n\n## 受控 agent 上下文\n\n" + strings.Join(lines, "\n")
		}
	}
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
