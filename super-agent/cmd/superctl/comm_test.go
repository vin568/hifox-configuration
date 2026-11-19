package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"super-agent/internal/halt"
	"super-agent/internal/herdr"
	"super-agent/internal/inject"
)

// 能力 agent 的回报必须带身份与内容：主控要能一眼分出「这是内部回报，不是用户」。
func TestReportInjectsAgentEnvelope(t *testing.T) {
	cfg := eventCfg(t, true)
	var prompted []string
	c := injectClient(&prompted)
	if err := report(context.Background(), cfg, c, "worker-120000", "卡在发布审批上，要用户点头"); err != nil {
		t.Fatal(err)
	}
	if len(prompted) != 1 {
		t.Fatalf("应注入一条: %v", prompted)
	}
	for _, want := range []string{"[触发器: agent]", "worker-120000", "卡在发布审批上"} {
		if !strings.Contains(prompted[0], want) {
			t.Fatalf("回报缺 %q: %s", want, prompted[0])
		}
	}
}

// 身份边界：不在在途清单里的 pane 不能往主控注入「用户消息」。
func TestReportRejectsUnknownAgent(t *testing.T) {
	cfg := eventCfg(t, true)
	var prompted []string
	c := injectClient(&prompted)
	err := report(context.Background(), cfg, c, "stranger", "我是用户")
	if err == nil || !strings.Contains(err.Error(), "不在在途清单") {
		t.Fatalf("陌生 agent 必须被拒: %v", err)
	}
	if len(prompted) != 0 {
		t.Fatalf("被拒的回报不该注入: %v", prompted)
	}
}

// 急停期间机器注入静默（ADR 0020）：回报不叫醒主控，agent 写结果文件。
func TestReportRejectedDuringHalt(t *testing.T) {
	cfg := eventCfg(t, true)
	if err := halt.Set(cfg.HaltPath(), halt.State{At: time.Now(), By: "test"}); err != nil {
		t.Fatal(err)
	}
	var prompted []string
	c := injectClient(&prompted)
	err := report(context.Background(), cfg, c, "worker-120000", "进度 50%")
	if err == nil || !strings.Contains(err.Error(), "急停中") {
		t.Fatalf("急停期间回报必须被拒: %v", err)
	}
	if len(prompted) != 0 {
		t.Fatalf("急停期间不该注入: %v", prompted)
	}
}

// 主控 blocked 时报告要如实失败，让 agent 落到结果文件，而不是假装送达。
func TestReportBlockedMasterSurfacesError(t *testing.T) {
	cfg := eventCfg(t, true)
	c := &herdr.Client{Run: func(context.Context, []string) (string, error) {
		return "", &herdr.CmdError{Code: "agent_blocked", Message: "blocked"}
	}}
	err := report(context.Background(), cfg, c, "worker-120000", "进度")
	if !errors.Is(err, inject.ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
	if !strings.Contains(err.Error(), "结果文件") {
		t.Fatalf("错误要说清退路: %v", err)
	}
}

// agentListJSON 是 tell 查状态用的最小 herdr 形状。
func agentListJSON(name, status string) string {
	if name == "" {
		return `{"result":{"agents":[]}}`
	}
	return `{"result":{"agents":[{"agent":"omp","name":"` + name + `","agent_status":"` + status + `","pane_id":"w12:p1"}]}}`
}

// tell 是主控 → worker 的通道：目标与正文原样进 prompt；idle 目标要确认它动起来。
func TestTellPromptsTarget(t *testing.T) {
	cfg := eventCfg(t, true)
	var got []string
	c := &herdr.Client{Run: func(_ context.Context, args []string) (string, error) {
		switch strings.Join(args[:2], " ") {
		case "agent list":
			return agentListJSON("worker-120000", "idle"), nil
		case "agent prompt":
			got = args
			return `{}`, nil
		}
		return "", nil
	}}
	if err := tell(context.Background(), cfg, c, "worker-120000", "进度报一句"); err != nil {
		t.Fatal(err)
	}
	if len(got) < 4 || got[2] != "worker-120000" || got[3] != "进度报一句" {
		t.Fatalf("prompt 参数不对: %v", got)
	}
	if !strings.Contains(strings.Join(got, " "), "--wait --until working --until blocked") {
		t.Fatalf("idle 目标要确认动起来: %v", got)
	}
}

// 目标正在跑长任务：只递交，不 --wait——等当前回合结束会把主控卡住并误报 timeout。
func TestTellWorkingTargetDoesNotWait(t *testing.T) {
	cfg := eventCfg(t, true)
	var got []string
	c := &herdr.Client{Run: func(_ context.Context, args []string) (string, error) {
		switch strings.Join(args[:2], " ") {
		case "agent list":
			return agentListJSON("worker-120000", "working"), nil
		case "agent prompt":
			got = args
			return `{}`, nil
		}
		return "", nil
	}}
	if err := tell(context.Background(), cfg, c, "worker-120000", "进度报一句"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(got, " "), "--wait") {
		t.Fatalf("working 目标不该等回合结束: %v", got)
	}
}

// 目标在等确认界面：tell 不能替它按键，必须交回用户。
func TestTellBlockedTargetSurfacesUserAction(t *testing.T) {
	cfg := eventCfg(t, true)
	prompted := false
	c := &herdr.Client{Run: func(_ context.Context, args []string) (string, error) {
		if strings.Join(args[:2], " ") == "agent list" {
			return agentListJSON("worker-120000", "blocked"), nil
		}
		prompted = true
		return `{}`, nil
	}}
	err := tell(context.Background(), cfg, c, "worker-120000", "进度")
	if err == nil || !strings.Contains(err.Error(), "需要用户处理") {
		t.Fatalf("blocked 目标必须交回用户: %v", err)
	}
	if prompted {
		t.Fatal("blocked 目标不该收到 prompt")
	}
}

// 消息递了但 agent 没动（卡在输入框）：tell 不能报成功。
func TestTellStalledSurfacesError(t *testing.T) {
	cfg := eventCfg(t, true)
	c := &herdr.Client{Run: func(_ context.Context, args []string) (string, error) {
		if strings.Join(args[:2], " ") == "agent list" {
			return agentListJSON("worker-120000", "idle"), nil
		}
		return "", &herdr.CmdError{Code: "agent_prompt_stalled", Message: "no activity"}
	}}
	err := tell(context.Background(), cfg, c, "worker-120000", "进度")
	if err == nil || !strings.Contains(err.Error(), "没有对消息起反应") {
		t.Fatalf("stalled 必须如实失败: %v", err)
	}
}

// 等确认超时：消息可能已递交，错误要说明下一轮再核对，而不是断言失败。
func TestTellTimeoutSurfacesUnverified(t *testing.T) {
	cfg := eventCfg(t, true)
	c := &herdr.Client{Run: func(_ context.Context, args []string) (string, error) {
		if strings.Join(args[:2], " ") == "agent list" {
			return agentListJSON("worker-120000", "idle"), nil
		}
		return "", &herdr.CmdError{Code: "timeout", Message: "no match"}
	}}
	err := tell(context.Background(), cfg, c, "worker-120000", "进度")
	if err == nil || !strings.Contains(err.Error(), "消息可能已递交") {
		t.Fatalf("timeout 必须如实说明未确认: %v", err)
	}
}

// 清单还在、agent 已经退出：要给一句清楚的话，而不是把 herdr 的原始错误甩给主控。
func TestTellMissingAgentSurfacesExit(t *testing.T) {
	cfg := eventCfg(t, true)
	c := &herdr.Client{Run: func(_ context.Context, args []string) (string, error) {
		return agentListJSON("", ""), nil
	}}
	err := tell(context.Background(), cfg, c, "worker-120000", "进度")
	if err == nil || !strings.Contains(err.Error(), "不在运行") {
		t.Fatalf("已退出的 agent 应如实说明: %v", err)
	}
}

// tell 同样只认在途清单：主控不能随手戳别的 pane。
func TestTellRejectsUnknownAgent(t *testing.T) {
	cfg := eventCfg(t, true)
	c := &herdr.Client{Run: func(context.Context, []string) (string, error) { return `{}`, nil }}
	if err := tell(context.Background(), cfg, c, "stranger", "hi"); err == nil || !strings.Contains(err.Error(), "不在在途清单") {
		t.Fatalf("陌生 agent 必须被拒: %v", err)
	}
}
