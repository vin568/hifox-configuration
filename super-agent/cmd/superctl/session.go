package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"super-agent/internal/boot"
	"super-agent/internal/config"
	"super-agent/internal/herdr"
)

// freshBrain 执行一次换脑：首条注入 = 该 agent 自己的手册（+ 附加意图），
// 主控另带能力索引与路径（那是 exp.MasterPrime 的内容）。会话留档按轮次目录。
func freshBrain(ctx context.Context, cfg config.Config, hc *herdr.Client, agent, reason, extraIntent string) (*boot.FreshReport, error) {
	exp, err := expectedTopo(cfg)
	if err != nil {
		return nil, err
	}
	prime := ""
	if extraIntent != "" {
		base := exp.MasterPrime
		if agent != cfg.MasterAgent {
			// 复盘 agent 换脑必须拿自己的手册：拿主控手册会让它按「编排者」行事。
			base = exp.RetroPrime
		}
		prime = base + "\n\n---\n" + extraIntent
	}
	rep, err := boot.Fresh(ctx, hc, exp, agent, newSessionDir(cfg, reason), prime)
	pruneSessions(cfg)
	return rep, err
}

// newSessionDir 生成一个轮次留档目录：state/sessions/<UTC 时间>-<原因>。
func newSessionDir(cfg config.Config, reason string) string {
	return filepath.Join(cfg.SessionsDir(), time.Now().UTC().Format("20060102T150405Z")+"-"+reason)
}

// pruneSessions 只保留最近 cfg.SessionKept() 个留档目录（目录名以 UTC 时间开头，字典序即时间序）。
func pruneSessions(cfg config.Config) {
	entries, err := os.ReadDir(cfg.SessionsDir())
	if err != nil {
		return
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	keep := cfg.SessionKept()
	for i := 0; i+keep < len(dirs); i++ {
		if err := os.RemoveAll(filepath.Join(cfg.SessionsDir(), dirs[i])); err != nil {
			slog.Warn("superctl: prune session archive failed", "dir", dirs[i], "error", err)
		}
	}
}
