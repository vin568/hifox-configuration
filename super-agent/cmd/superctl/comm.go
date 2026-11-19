package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"super-agent/internal/config"
	"super-agent/internal/herdr"
	"super-agent/internal/inflight"
	"super-agent/internal/inject"
)

// report / tell 是主控与本地能力 agent 之间的双向消息通道（ADR 0029）：
//
//   - report：作业中的 agent 主动把卡点、需要裁决的事、用户可见的关键结果推给主控；
//   - tell：主控中途补信息，或在巡检时问一句进度。
//
// 身份边界是在途清单：只有经 superctl dispatch 派出的本地作业能 report，也只允许
// tell 到这些作业。否则任意一个 pane 都能往主控注入「用户消息」。

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	agentName := fs.String("agent", "", "你的 agent 名（派活注入里给出的名字）")
	text := fs.String("text", "", "报给主控的一句话")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agent, body := strings.TrimSpace(*agentName), strings.TrimSpace(*text)
	if agent == "" || body == "" {
		return errors.New("report 需要 --agent 与 --text")
	}
	cfg, _, err := loadConfigWithAgents([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	return report(context.Background(), cfg, herdr.New(cfg.Session), agent, body)
}

// report 只走注入、不做拓扑对账：能力 agent 不该顺手改主控的拓扑。
func report(ctx context.Context, cfg config.Config, hc *herdr.Client, agent, text string) error {
	if err := requireInflightLocal(cfg, agent); err != nil {
		return err
	}
	if !(gate{cfg: cfg}).allowMachine() {
		// 急停期间机器注入静默（ADR 0020）：报告不叫醒主控，落进结果文件由巡检补交。
		return errors.New("急停中：这条不注入主控；把内容写进结果文件")
	}
	if err := inject.Do(ctx, hc, cfg.MasterAgent, inject.BuildAgentReport(agent, text)); err != nil {
		if errors.Is(err, inject.ErrBlocked) {
			return fmt.Errorf("主控在等确认界面，报告没送达；把内容写进结果文件: %w", err)
		}
		return err
	}
	fmt.Println("reported")
	return nil
}

func cmdTell(args []string) error {
	fs := flag.NewFlagSet("tell", flag.ContinueOnError)
	agentName := fs.String("agent", "", "目标本地 agent 名")
	text := fs.String("text", "", "要说的话")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agent, body := strings.TrimSpace(*agentName), strings.TrimSpace(*text)
	if agent == "" || body == "" {
		return errors.New("tell 需要 --agent 与 --text")
	}
	cfg, _, err := loadConfigWithAgents([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	return tell(context.Background(), cfg, herdr.New(cfg.Session), agent, body)
}

// tell 只递交、不等回合。先看目标当前状态：working 的 agent 只递交（对它 --wait 会
// 等当前回合结束，把主控卡住并误报 timeout）；idle/done 才要求看到它动起来；
// blocked 与不存在的 agent 直接如实失败，绝不假装消息到了。
func tell(ctx context.Context, cfg config.Config, hc *herdr.Client, agent, text string) error {
	if err := requireInflightLocal(cfg, agent); err != nil {
		return err
	}
	agents, err := hc.AgentList(ctx)
	if err != nil {
		return fmt.Errorf("查 %s 的状态: %w", agent, err)
	}
	a, ok := herdr.FindAgent(agents, agent)
	if !ok {
		return fmt.Errorf("%s 不在运行（可能已退出）；这条消息没送出去", agent)
	}
	if a.AgentStatus == "blocked" {
		return fmt.Errorf("%s 卡在要确认的界面上（需要用户处理）；消息未送达", agent)
	}
	if a.AgentStatus == "working" {
		if err := hc.AgentPrompt(ctx, agent, text, false, 0); err != nil {
			return tellPromptError(agent, err)
		}
		fmt.Println("sent")
		return nil
	}
	if err := hc.AgentPromptUntil(ctx, agent, text, []string{"working", "blocked"}, 15000); err != nil {
		return tellPromptError(agent, err)
	}
	fmt.Println("sent")
	return nil
}

// tellPromptError 把 herdr 的三种拒绝翻译成主控能直接处置的话。
func tellPromptError(agent string, err error) error {
	if herdr.IsBlocked(err) {
		return fmt.Errorf("%s 卡在要确认的界面上（需要用户处理）；消息未送达: %w", agent, err)
	}
	var ce *herdr.CmdError
	if errors.As(err, &ce) {
		switch ce.Code {
		case "agent_prompt_stalled":
			return fmt.Errorf("%s 没有对消息起反应（可能卡在输入框或长任务里）；等这轮结束再问: %w", agent, err)
		case "timeout":
			return fmt.Errorf("等 %s 确认超时；消息可能已递交，下一轮巡检再核对: %w", agent, err)
		}
	}
	return err
}

// requireInflightLocal 是通道的身份边界：只认在途清单里的本地作业。
// 旧的 watchlist 迁移也走同一入口，避免升级窗口里把活当成陌生 pane。
func requireInflightLocal(cfg config.Config, agent string) error {
	entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath())
	if err != nil {
		return err
	}
	if inflight.HasLocal(entries, agent) {
		return nil
	}
	return fmt.Errorf("%s 不在在途清单里：report/tell 只用于 superctl dispatch 派出的本地作业", agent)
}
