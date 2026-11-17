package inject

import (
	"context"
	"errors"
	"strings"
	"testing"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/herdr"
)

func TestBuildFeishuDM(t *testing.T) {
	got := BuildFeishu(Payload{ChatID: "oc_1", SenderID: "ou_alice", Kind: "dm", Text: "让戴宗跑一次 staging 验收"})
	for _, want := range []string{"oc_1", "私聊", "ou_alice", "让戴宗跑一次 staging 验收"} {
		if !strings.Contains(got, want) {
			t.Fatalf("envelope missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "发送者 \n") {
		t.Fatalf("sender line must not render empty: %s", got)
	}
	// 注入卫生（ADR 0022）：命令样本只在首条 prime，注入信封不重复。
	if strings.Contains(got, "superctl reply") {
		t.Fatalf("注入信封不应再带命令样板:\n%s", got)
	}
}

func TestBuildFeishuGroupAndProxy(t *testing.T) {
	plain := BuildFeishu(Payload{ChatID: "oc_2", Kind: "group", Text: "hi"})
	if !strings.Contains(plain, "群聊") || strings.Contains(plain, "代答") {
		t.Fatalf("group envelope wrong:\n%s", plain)
	}
	proxy := BuildFeishu(Payload{ChatID: "oc_2", Kind: "group", ProxyReply: true, Text: "@你 看下这个"})
	if !strings.Contains(proxy, "代答") || !strings.Contains(proxy, "不暴露身份") {
		t.Fatalf("proxy envelope wrong:\n%s", proxy)
	}
}

func TestBuildFeishuNoSenderOmitsLabel(t *testing.T) {
	got := BuildFeishu(Payload{ChatID: "oc_1", SenderID: "", Kind: "dm", Text: "hi"})
	if strings.Contains(got, "发送者") {
		t.Fatalf("empty sender must not render label:\n%s", got)
	}
}

func TestBuildEvent(t *testing.T) {
	got := BuildEvent("w12:p1 jiangjing → blocked")
	if !strings.Contains(got, "jiangjing") || !strings.Contains(got, "[触发器: event]") {
		t.Fatalf("event envelope wrong:\n%s", got)
	}
}

func TestBuildHaltIntent(t *testing.T) {
	got := BuildHaltIntent("在途：\n- HF-1「x」\n", "/tmp/old.jsonl")
	for _, want := range []string{"急停", "HF-1", "/tmp/old.jsonl", "要不要停", "不要继续任何作业"} {
		if !strings.Contains(got, want) {
			t.Fatalf("halt intent missing %q:\n%s", want, got)
		}
	}
}

func TestDoMapsBlocked(t *testing.T) {
	c := &herdr.Client{Run: func(context.Context, []string) (string, error) {
		return "", &herdr.CmdError{Code: "agent_blocked", Message: "blocked"}
	}}
	err := Do(context.Background(), c, "songjiang", "hi")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
}

func TestDoPassesThrough(t *testing.T) {
	c := &herdr.Client{Run: func(context.Context, []string) (string, error) { return "{}", nil }}
	if err := Do(context.Background(), c, "songjiang", "hi"); err != nil {
		t.Fatal(err)
	}
}

func TestDoPassesThroughPlainError(t *testing.T) {
	boom := errors.New("socket gone")
	c := &herdr.Client{Run: func(context.Context, []string) (string, error) {
		return "", boom
	}}
	err := Do(context.Background(), c, "songjiang", "hi")
	if err == nil || errors.Is(err, ErrBlocked) || err.Error() != boom.Error() {
		t.Fatalf("want plain error passthrough, got %v", err)
	}
}
