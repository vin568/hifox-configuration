package feishu

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"super-agent/internal/inject"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// serveHarness 把 server.handle 的完整链路（过滤→去重→注入→重试）接到
// 可控的 InjectFunc 上；dedup 落 tmpdir。
type serveHarness struct {
	*server
	dedupPath string
	injected  chan inject.Payload
	mu        sync.Mutex
	payloads  []inject.Payload
	err       error // 每次 inject 的返回值（默认 nil）
}

func newServeHarness(t *testing.T, cfg Config, err error) *serveHarness {
	t.Helper()
	dedupPath := t.TempDir() + "/dedup.jsonl"
	dedup, derr := newDedupStore(dedupPath, time.Minute)
	if derr != nil {
		t.Fatal(derr)
	}
	h := &serveHarness{injected: make(chan inject.Payload, 16), err: err, dedupPath: dedupPath}
	h.server = &server{
		cfg:   cfg,
		dedup: dedup,
		queue: newRetryQueue(func(_ context.Context, _ Source, p inject.Payload) error {
			h.mu.Lock()
			h.payloads = append(h.payloads, p)
			h.mu.Unlock()
			h.injected <- p
			return h.err
		}),
	}
	return h
}

func textEvent(chatType, chatID, senderID, messageID, text string, mentions ...*larkim.MentionEvent) *larkim.P2MessageReceiveV1 {
	content := `{"text":"` + text + `"}`
	return &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: new(senderID)}},
		Message: &larkim.EventMessage{
			MessageId: new(messageID), ChatId: new(chatID), ChatType: new(chatType),
			MessageType: new("text"), Content: new(content), Mentions: mentions,
		},
	}}
}

func mention(key, id string) *larkim.MentionEvent {
	return &larkim.MentionEvent{Key: new(key), Id: &larkim.UserId{OpenId: new(id)}, Name: new("名字")}
}

func baseCfg() Config {
	return Config{
		AllowedUserIDs:   []string{"ou_alice", "ou_bob"},
		ProxyReplyGroups: map[string]bool{"chat_proxy": true},
		AnyMessageGroups: []string{"chat_any"},
		BotMentionOpenID: "ou_bot",
	}
}

// doneInJournal 用重开后的 state 断言 done 行真的落了盘：claim 对「已 claim
// 未 done」同样返回 false，拿 claim 当 done 的判据区分不了两者。
func doneInJournal(t *testing.T, path, id string) bool {
	t.Helper()
	s2, err := newDedupStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.close()
	rec, ok := s2.state[id]
	return ok && rec.Done
}

// 残缺事件（nil / 缺 Message）不得 panic、不得注入。
func TestHandleDegenerateEvent(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.handle(nil)
	h.handle(&larkim.P2MessageReceiveV1{})
	h.handle(&larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{}})
	if len(h.payloads) != 0 {
		t.Fatalf("degenerate events must not inject, got %d", len(h.payloads))
	}
}

// 陌生人私聊被三层准入拦下，绝不注入主控。
func TestHandleStrangerDMDropped(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.handle(textEvent("p2p", "oc_dm", "ou_stranger", "m_stranger", "hi"))
	if len(h.payloads) != 0 {
		t.Fatalf("stranger DM must be dropped, injected %+v", h.payloads)
	}
}

// 评审 F-11 收尾：过滤必须先于去重 claim——被拦下的消息不得占用去重位，
// 否则同一 message_id 换白名单身份重放会被当重复吞掉（claim 提到 allow 之前
// 时本用例变红）。
func TestHandleFilterRunsBeforeDedupClaim(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.handle(textEvent("p2p", "oc_dm", "ou_stranger", "m_shared", "hi"))
	if len(h.payloads) != 0 {
		t.Fatalf("stranger must not inject: %+v", h.payloads)
	}
	data, derr := os.ReadFile(h.dedupPath)
	if derr == nil && strings.Contains(string(data), "m_shared") {
		t.Fatal("被过滤的消息不得留下 claim 记录（过滤必须先于去重）")
	}
	// 同 message_id 由白名单身份进入：必须照常注入（未被陌生人提前占位）。
	h.handle(textEvent("p2p", "oc_dm", "ou_alice", "m_shared", "跑验收"))
	if len(h.payloads) != 1 || h.payloads[0].SenderID != "ou_alice" {
		t.Fatalf("want alice injected, got %+v", h.payloads)
	}
}

// 白名单私聊注入一次：kind=dm、sender 携带；同一 message_id 重投不二注，
// 且 done 落盘（重开 journal 后 claim 仍被拒）。
func TestHandleWhitelistedDMInjectedOnce(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.handle(textEvent("p2p", "oc_dm", "ou_alice", "m1", "跑一次验收"))
	if len(h.payloads) != 1 {
		t.Fatalf("want 1 inject, got %d", len(h.payloads))
	}
	p := h.payloads[0]
	if p.Kind != "dm" || p.SenderID != "ou_alice" || p.ProxyReply {
		t.Fatalf("payload semantics wrong: %+v", p)
	}
	if !strings.Contains(p.Text, "跑一次验收") {
		t.Fatalf("text lost: %+v", p)
	}
	h.handle(textEvent("p2p", "oc_dm", "ou_alice", "m1", "跑一次验收"))
	if len(h.payloads) != 1 {
		t.Fatalf("redelivery must not re-inject, got %d", len(h.payloads))
	}
	if !doneInJournal(t, h.dedup.path, "m1") {
		t.Fatal("done must persist across reopen")
	}
}

// 全收群消息：非代答的群消息信封必须是群聊语义（kind=group），不能标成私聊。
func TestHandleAnyGroupKindIsGroup(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.handle(textEvent("group", "chat_any", "ou_stranger", "m_g1", "群里说事"))
	if len(h.payloads) != 1 || h.payloads[0].Kind != "group" || h.payloads[0].ProxyReply {
		t.Fatalf("any-group message must carry kind=group, proxy=false: %+v", h.payloads)
	}
}

// 代答群里他人 @ 白名单用户：proxyReply=true 且 kind=group；@bot 不算代答。
func TestHandleProxyReplySemantics(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.handle(textEvent("group", "chat_proxy", "ou_stranger", "m_p1", "@你 看下", mention("@_user_1", "ou_alice")))
	if len(h.payloads) != 1 || !h.payloads[0].ProxyReply || h.payloads[0].Kind != "group" {
		t.Fatalf("proxy mention must inject with proxy=true kind=group: %+v", h.payloads)
	}

	h2 := newServeHarness(t, baseCfg(), nil)
	h2.handle(textEvent("group", "chat_proxy", "ou_stranger", "m_p2", "hi @bot", mention("@_user_1", "ou_bot")))
	if len(h2.payloads) != 1 || h2.payloads[0].ProxyReply {
		t.Fatalf("@bot in proxy group is not a proxy reply: %+v", h2.payloads)
	}
}

// 注入瞬时失败：消息进重试队列，重试成功后 done 被调（dedup 闭环）。
func TestHandleTransientFailureRetriedAndDone(t *testing.T) {
	h := newServeHarness(t, baseCfg(), errTransient)
	h.queue.tasks = make(chan injectTask, 8)
	h.queue.backoff = fastBackoff
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.queue.run(ctx)

	h.handle(textEvent("p2p", "oc_dm", "ou_alice", "m_retry", "重试我"))
	select {
	case <-h.injected:
	case <-time.After(2 * time.Second):
		t.Fatal("first attempt never happened")
	}

	// 恢复注入成功率，等重试成功；重试真的发生（≥2 次注入）且 done 落盘。
	h.mu.Lock()
	h.err = nil
	h.mu.Unlock()
	deadline := time.After(5 * time.Second)
	for {
		if doneInJournal(t, h.dedup.path, "m_retry") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("retry never completed (done not marked)")
		case <-time.After(20 * time.Millisecond):
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.payloads) < 2 {
		t.Fatalf("retry must re-attempt the inject, attempts=%d", len(h.payloads))
	}
}

// 主控 blocked：消息按预算排队重试（ADR 0018「注入器侧排队重试」）——
// handle 之后的重入队可观测（run 未启动，任务留在缓冲通道里）。
func TestHandleBlockedEnqueuedForRetry(t *testing.T) {
	h := newServeHarness(t, baseCfg(), errors.Join(inject.ErrBlocked, errors.New("refused")))
	h.queue.tasks = make(chan injectTask, 4) // 不启动 run：重入队会留在通道里
	h.handle(textEvent("p2p", "oc_dm", "ou_alice", "m_block", "卡住了"))
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.payloads) != 1 {
		t.Fatalf("want 1 inject attempt, got %d", len(h.payloads))
	}
	if n := len(h.queue.tasks); n != 1 {
		t.Fatalf("blocked message must be queued for retry, queue depth=%d", n)
	}
}
