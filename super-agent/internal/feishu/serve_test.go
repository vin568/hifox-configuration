package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// postEvent 造一条富文本消息事件（content 直接给原始 JSON）。
func postEvent(chatType, chatID, senderID, messageID, content string) *larkim.P2MessageReceiveV1 {
	return mediaEvent(chatType, chatID, senderID, messageID, "post", content)
}

// mediaEvent 造任意 msg_type 的消息事件（纯媒体消息也走它）。
func mediaEvent(chatType, chatID, senderID, messageID, msgType, content string) *larkim.P2MessageReceiveV1 {
	return &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: new(senderID)}},
		Message: &larkim.EventMessage{
			MessageId: new(messageID), ChatId: new(chatID), ChatType: new(chatType),
			MessageType: new(msgType), Content: new(content),
		},
	}}
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

// 富文本里的图片：注入文本里必须出现本地路径（旧实现整张图静默消失，
// 主控手上没有任何线索可以派活）。
func TestHandlePostImageBecomesLocalPath(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	g := &fakeGetter{body: "JPEG", ctype: "image/jpeg"}
	dir := t.TempDir()
	h.media = newMediaStore(dir, g, 0)

	h.handle(postEvent("p2p", "oc_dm", "ou_alice", "m_img",
		`{"title":"","content":[[{"tag":"text","text":"复现步骤："}],[{"tag":"img","image_key":"img_v3_abc","width":3384,"height":1904}]]}`))

	if len(h.payloads) != 1 {
		t.Fatalf("want 1 inject, got %d", len(h.payloads))
	}
	text := h.payloads[0].Text
	want := filepath.Join(dir, "m_img", "img_v3_abc.jpg")
	if !strings.Contains(text, "[图片 "+want+"]") {
		t.Fatalf("注入文本里应是本地路径 %s:\n%s", want, text)
	}
	if strings.Contains(text, "[图片 img_v3_abc]") {
		t.Fatalf("key 占位必须被替换掉，不能与路径并存:\n%s", text)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("附件没落盘: %v", err)
	}
}

// 附件下载失败：消息照常注入，key 占位留着（可人工补），绝不静默丢弃。
func TestHandleImageDownloadFailureKeepsPlaceholder(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.media = newMediaStore(t.TempDir(), &fakeGetter{err: errors.New("网络抖动")}, 0)

	h.handle(postEvent("p2p", "oc_dm", "ou_alice", "m_img2",
		`{"content":[[{"tag":"text","text":"见图"},{"tag":"img","image_key":"img_v3_down"}]]}`))

	if len(h.payloads) != 1 {
		t.Fatalf("下载失败不该丢消息，inject=%d", len(h.payloads))
	}
	if !strings.Contains(h.payloads[0].Text, "[图片 img_v3_down]") {
		t.Fatalf("失败时应保留 key 占位:\n%s", h.payloads[0].Text)
	}
}

// 纯图片消息：content 里只有 image_key，从前只降级成 [媒体消息]（连 key 都没留）。
func TestHandlePureImageMessage(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	dir := t.TempDir()
	h.media = newMediaStore(dir, &fakeGetter{body: "PNG", ctype: "image/png"}, 0)

	h.handle(mediaEvent("p2p", "oc_dm", "ou_alice", "m_pure", "image", `{"image_key":"img_v2_pure"}`))

	if len(h.payloads) != 1 {
		t.Fatalf("want 1 inject, got %d", len(h.payloads))
	}
	if want := filepath.Join(dir, "m_pure", "img_v2_pure.png"); !strings.Contains(h.payloads[0].Text, want) {
		t.Fatalf("注入文本应带本地路径 %s:\n%s", want, h.payloads[0].Text)
	}
}

// 没配落盘目录（probe / 未配状态目录）：不下载、不 panic，key 占位照旧进注入。
func TestHandleMediaWithoutStore(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.handle(mediaEvent("p2p", "oc_dm", "ou_alice", "m_nostore", "image", `{"image_key":"img_v2_x"}`))
	if len(h.payloads) != 1 || !strings.Contains(h.payloads[0].Text, "img_v2_x") {
		t.Fatalf("无落盘目录时应保留 key 占位: %+v", h.payloads)
	}
}

// 非图片附件：文件消息落盘后注入里是 [文件 <路径>]，不是 [图片 …]（文件与图片走
// 接口的两个 type 分支，改写分支也不同）。
func TestHandleFileMessageBecomesLocalPath(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	g := &fakeGetter{body: "LOGS", ctype: "text/plain"}
	dir := t.TempDir()
	h.media = newMediaStore(dir, g, 0)

	h.handle(mediaEvent("p2p", "oc_dm", "ou_alice", "m_file", "file",
		`{"file_key":"file_v2_log","file_name":"崩溃日志.txt"}`))

	if len(h.payloads) != 1 {
		t.Fatalf("want 1 inject, got %d", len(h.payloads))
	}
	text := h.payloads[0].Text
	want := filepath.Join(dir, "m_file", "file_v2_log.txt")
	if !strings.Contains(text, "[文件 "+want+"]") {
		t.Fatalf("注入文本里应是文件路径 %s:\n%s", want, text)
	}
	if strings.Contains(text, "[文件 崩溃日志.txt file_v2_log]") {
		t.Fatalf("key 占位必须被替换掉:\n%s", text)
	}
	if len(g.calls) != 1 || g.calls[0] != "m_file|file_v2_log|file" {
		t.Fatalf("接口 type 参数应是 file: %v", g.calls)
	}
}

// 附件落盘开关的装配：MediaDir 为空就不建 store（probe/测试路径），非空才建。
// 这条线断了，线上所有图片都退回 key 占位且没有任何报错。
func TestMediaStoreForHonorsMediaDir(t *testing.T) {
	if s := mediaStoreFor(Config{}); s != nil {
		t.Fatalf("MediaDir 为空不该落盘: %+v", s)
	}
	dir := t.TempDir()
	s := mediaStoreFor(Config{MediaDir: dir, MediaRetention: 48 * time.Hour})
	if s == nil {
		t.Fatal("MediaDir 非空必须建 mediaStore（附件落盘总开关）")
	}
	if s.dir != dir || s.retention != 48*time.Hour {
		t.Fatalf("store 配置没接上: dir=%q retention=%v", s.dir, s.retention)
	}
	if s.get == nil {
		t.Fatal("getter 不能是 nil：第一条附件就会 panic")
	}
}

// 一条消息多张图：第一张下载失败只影响它自己，第二张照常落盘并改写成路径。
// 失败分支从 continue 变成 return 时，这条用例是唯一会红的。
func TestHandleMultiImagePartialFailure(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	dir := t.TempDir()
	h.media = newMediaStore(dir, &fakeGetter{
		body:     "PNG",
		ctype:    "image/png",
		errByKey: map[string]error{"img_v3_bad": errors.New("502")},
	}, 0)

	h.handle(postEvent("p2p", "oc_dm", "ou_alice", "m_multi",
		`{"content":[[{"tag":"img","image_key":"img_v3_bad"},{"tag":"img","image_key":"img_v3_ok"}]]}`))

	if len(h.payloads) != 1 {
		t.Fatalf("want 1 inject, got %d", len(h.payloads))
	}
	text := h.payloads[0].Text
	if !strings.Contains(text, "[图片 img_v3_bad]") {
		t.Fatalf("失败那张应保留 key 占位:\n%s", text)
	}
	want := filepath.Join(dir, "m_multi", "img_v3_ok.png")
	if !strings.Contains(text, "[图片 "+want+"]") {
		t.Fatalf("成功那张应改写成路径 %s:\n%s", want, text)
	}
}

// 多张图全部成功：两条路径都要出现（顺序按文档顺序）。
func TestHandleMultiImageAllSucceed(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	dir := t.TempDir()
	h.media = newMediaStore(dir, &fakeGetter{body: "PNG", ctype: "image/png"}, 0)

	h.handle(postEvent("p2p", "oc_dm", "ou_alice", "m_two",
		`{"content":[[{"tag":"img","image_key":"img_a"},{"tag":"img","image_key":"img_b"}]]}`))

	text := h.payloads[0].Text
	for _, key := range []string{"img_a", "img_b"} {
		want := filepath.Join(dir, "m_two", key+".png")
		if !strings.Contains(text, want) {
			t.Fatalf("缺 %s 的本地路径:\n%s", key, text)
		}
	}
}

// newServer 是生产装配线：MediaDir 有值就必须挂上 mediaStore（否则附件功能整体
// 静默失效），为空时不能挂（probe 路径）。
func TestNewServerWiresMedia(t *testing.T) {
	dir := t.TempDir()
	srv := newServer(Config{MediaDir: dir, MediaRetention: time.Hour}, nil, nil)
	if srv.media == nil {
		t.Fatal("MediaDir 非空时 server 必须挂 mediaStore")
	}
	if srv.media.dir != dir || srv.media.retention != time.Hour {
		t.Fatalf("mediaStore 配置没接上: %+v", srv.media)
	}
	if s := newServer(Config{}, nil, nil); s.media != nil {
		t.Fatalf("MediaDir 为空不该挂 mediaStore: %+v", s.media)
	}
}

// Run 必须经 serverFactory 装配：若有人把 Run 改回结构体字面量，srv.media 恒为 nil，
// 线上所有附件退回 key 占位且没有报错。用已取消的 ctx 让连接立刻收起。
func TestRunBuildsServerWithMedia(t *testing.T) {
	var got Config
	old := serverFactory
	serverFactory = func(cfg Config, d *dedupStore, q *retryQueue) *server {
		got = cfg
		return &server{cfg: cfg, dedup: d, queue: q}
	}
	defer func() { serverFactory = old }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := Config{
		AppID: "app", AppSecret: "secret",
		DedupPath: filepath.Join(t.TempDir(), "dedup.jsonl"),
		MediaDir:  t.TempDir(),
	}
	_ = Run(ctx, cfg, func(context.Context, Source, inject.Payload) error { return nil })
	if got.MediaDir != cfg.MediaDir {
		t.Fatalf("Run 没有把 MediaDir 交给装配函数: %q", got.MediaDir)
	}
}
