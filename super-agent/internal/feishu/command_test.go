package feishu

import (
	"context"
	"testing"
	"time"
)

// 指令面（ADR 0020）：白名单私聊里的 "/" 指令由运行时消化，绝不当消息注入，
// 也绝不能被重试队列排队——急停不能排在错误指令后面。
func TestSlashCommandInterceptedBeforeInject(t *testing.T) {
	var got []string
	cfg := Config{AllowedUserIDs: []string{"ou_alice"}}
	cfg.CommandFunc = func(_ context.Context, chatID, senderID, text string) bool {
		got = append(got, chatID+"|"+senderID+"|"+text)
		return true
	}
	h := newServeHarness(t, cfg, nil)
	h.handle(textEvent("p2p", "oc_dm", "ou_alice", "om_halt", "/halt 别乱派"))
	if len(got) != 1 || got[0] != "oc_dm|ou_alice|/halt 别乱派" {
		t.Fatalf("指令未交给运行时消化: %v", got)
	}
	if len(h.payloads) != 0 {
		t.Fatalf("指令绝不能进注入队列: %v", h.payloads)
	}
}

// 指令面只对私聊生效：群里 "/halt" 是普通消息（可能被判代答/放行）。
func TestSlashCommandIgnoredInGroup(t *testing.T) {
	called := false
	cfg := Config{
		AllowedUserIDs:   []string{"ou_alice"},
		AnyMessageGroups: []string{"oc_group"},
	}
	cfg.CommandFunc = func(context.Context, string, string, string) bool { called = true; return true }
	h := newServeHarness(t, cfg, nil)
	h.handle(textEvent("group", "oc_group", "ou_bob", "om_group", "/halt"))
	if called {
		t.Fatal("群里的斜杠命令不应被运行时消化")
	}
}

// 不认识的 "/" 指令返回 false，仍按普通消息注入。
func TestUnknownSlashFallsThrough(t *testing.T) {
	cfg := Config{AllowedUserIDs: []string{"ou_alice"}}
	cfg.CommandFunc = func(context.Context, string, string, string) bool { return false }
	h := newServeHarness(t, cfg, nil)
	h.handle(textEvent("p2p", "oc_dm", "ou_alice", "om_x", "/deploy 一下"))
	select {
	case p := <-h.injected:
		if p.Text != "/deploy 一下" {
			t.Fatalf("正文被改写: %+v", p)
		}
	case <-time.After(time.Second):
		t.Fatal("未识别指令应走普通注入")
	}
}

// 重复投递同一条指令只消化一次（去重在指令面之前）。
func TestSlashCommandDeduped(t *testing.T) {
	n := 0
	cfg := Config{AllowedUserIDs: []string{"ou_alice"}}
	cfg.CommandFunc = func(context.Context, string, string, string) bool { n++; return true }
	h := newServeHarness(t, cfg, nil)
	ev := textEvent("p2p", "oc_dm", "ou_alice", "om_same", "/halt")
	h.handle(ev)
	h.handle(ev)
	if n != 1 {
		t.Fatalf("同一 message_id 重复执行了 %d 次", n)
	}
}
