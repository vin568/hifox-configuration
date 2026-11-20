package feishu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// fakeQuoteGetter 按 id 返回预设消息或错误，并记录取过哪些 id。
type fakeQuoteGetter struct {
	calls []string
	byID  map[string]*messageView
	errs  map[string]error
}

func (f *fakeQuoteGetter) Get(_ context.Context, messageID string) (*messageView, error) {
	f.calls = append(f.calls, messageID)
	if err, ok := f.errs[messageID]; ok {
		return nil, err
	}
	if v, ok := f.byID[messageID]; ok {
		return v, nil
	}
	return nil, errors.New("not found")
}

// replyEvent 造一条「回复 parent」的事件。
func replyEvent(chatType, chatID, senderID, messageID, parentID, text string) *larkim.P2MessageReceiveV1 {
	ev := textEvent(chatType, chatID, senderID, messageID, text)
	ev.Event.Message.ParentId = new(parentID)
	ev.Event.Message.RootId = new(parentID)
	return ev
}

func TestQuoteTargetID(t *testing.T) {
	cases := []struct {
		name          string
		parent, root  string
		message, want string
	}{
		{"parent 优先", "om_p", "om_r", "om_cur", "om_p"},
		{"无 parent 用 root", "", "om_r", "om_cur", "om_r"},
		{"自指跳过", "om_cur", "om_cur", "om_cur", ""},
		{"都空", "", "", "om_cur", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := &eventInfo{parentID: c.parent, rootID: c.root, messageID: c.message}
			if got := quoteTargetID(ev); got != c.want {
				t.Fatalf("quoteTargetID = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveQuote(t *testing.T) {
	base := func() *server { return &server{} }

	t.Run("文本引用", func(t *testing.T) {
		s := base()
		s.quote = &fakeQuoteGetter{byID: map[string]*messageView{
			"om_p": {MessageID: "om_p", MsgType: "text", Content: `{"text":"复现步骤"}`, SenderID: "ou_bob"},
		}}
		q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
		if q == nil || q.MessageID != "om_p" || q.SenderID != "ou_bob" || q.Text != "复现步骤" {
			t.Fatalf("quote = %+v", q)
		}
	})

	t.Run("post 带图按父消息 id 下载", func(t *testing.T) {
		s := base()
		g := &fakeGetter{body: "JPEG", ctype: "image/jpeg"}
		dir := t.TempDir()
		s.media = newMediaStore(dir, g, 0)
		s.quote = &fakeQuoteGetter{byID: map[string]*messageView{
			"om_p": {MessageID: "om_p", MsgType: "post", SenderID: "ou_bob",
				Content: `{"content":[[{"tag":"text","text":"看图"}],[{"tag":"img","image_key":"img_v3_abc"}]]}`},
		}}
		q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
		if q == nil {
			t.Fatal("nil quote")
		}
		if len(g.calls) != 1 || g.calls[0] != "om_p|img_v3_abc|image" {
			t.Fatalf("资源接口必须用父消息 id 下载，calls = %v", g.calls)
		}
		want := filepath.Join(dir, "om_p", "img_v3_abc.jpg")
		if q.Text == "" || !strings.Contains(q.Text, want) {
			t.Fatalf("引用正文应含本地路径 %s: %s", want, q.Text)
		}
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("引用附件没落盘: %v", err)
		}
	})

	t.Run("已撤回", func(t *testing.T) {
		s := base()
		s.quote = &fakeQuoteGetter{byID: map[string]*messageView{
			"om_p": {MessageID: "om_p", Deleted: true},
		}}
		q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
		if q == nil || !strings.Contains(q.Text, "已撤回") {
			t.Fatalf("quote = %+v", q)
		}
	})

	t.Run("APIError 带码", func(t *testing.T) {
		s := base()
		s.quote = &fakeQuoteGetter{errs: map[string]error{
			"om_p": &APIError{Op: "feishu get message", Code: 230050, Msg: "invisible", RequestID: "r1"},
		}}
		q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
		if q == nil || !strings.Contains(q.Text, "230050") || !strings.Contains(q.Text, "r1") {
			t.Fatalf("quote = %+v", q)
		}
	})

	t.Run("超时", func(t *testing.T) {
		s := base()
		s.quote = &fakeQuoteGetter{errs: map[string]error{"om_p": context.DeadlineExceeded}}
		q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
		if q == nil || !strings.Contains(q.Text, "超时") {
			t.Fatalf("quote = %+v", q)
		}
	})

	t.Run("截断", func(t *testing.T) {
		s := base()
		long := ""
		for range 2001 {
			long += "汉"
		}
		s.quote = &fakeQuoteGetter{byID: map[string]*messageView{
			"om_p": {MessageID: "om_p", MsgType: "text", Content: `{"text":"` + long + `"}`},
		}}
		q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
		if q == nil || len([]rune(q.Text)) <= quoteMaxRunes || !strings.Contains(q.Text, "已截断") {
			t.Fatalf("truncated len=%d", len([]rune(q.Text)))
		}
	})

	t.Run("无引用零调用", func(t *testing.T) {
		s := base()
		g := &fakeQuoteGetter{}
		s.quote = g
		if q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur"}); q != nil {
			t.Fatalf("普通消息不该产出引用块: %+v", q)
		}
		if len(g.calls) != 0 {
			t.Fatalf("普通消息不该取消息: %v", g.calls)
		}
	})
}

// 回复事件必须带引用块进注入队列；普通消息不带。
func TestHandleReplyInjectsQuote(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.quote = &fakeQuoteGetter{byID: map[string]*messageView{
		"om_p": {MessageID: "om_p", MsgType: "text", Content: `{"text":"这个再改一下"}`, SenderID: "ou_alice"},
	}}

	h.handle(replyEvent("p2p", "oc_dm", "ou_alice", "om_cur", "om_p", "图里这里错了"))

	if len(h.payloads) != 1 {
		t.Fatalf("want 1 inject, got %d", len(h.payloads))
	}
	q := h.payloads[0].Quote
	if q == nil || q.MessageID != "om_p" || q.Text != "这个再改一下" {
		t.Fatalf("payload quote = %+v", q)
	}

	// 重投同一事件（去重）不该再注入。
	h.handle(replyEvent("p2p", "oc_dm", "ou_alice", "om_cur", "om_p", "图里这里错了"))
	if len(h.payloads) != 1 {
		t.Fatalf("duplicate delivery must not re-inject, got %d", len(h.payloads))
	}
}

func TestNewServerWiresQuote(t *testing.T) {
	dedup, err := newDedupStore(t.TempDir()+"/dedup.jsonl", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dedup.close() })
	srv := newServer(Config{AppID: "cli_x", AppSecret: "s"}, dedup, nil)
	if srv.quote == nil {
		t.Fatal("有凭证时 newServer 必须接上只读客户端（装配线断了线上所有回帖退回只看当前消息）")
	}
	if srv := newServer(Config{}, dedup, nil); srv.quote != nil {
		t.Fatal("空凭证不该建客户端")
	}
}

func TestParseEventParentRoot(t *testing.T) {
	ev := parseEvent(replyEvent("p2p", "oc_dm", "ou_alice", "om_cur", "om_p", "hi"))
	if ev == nil || ev.parentID != "om_p" || ev.rootID != "om_p" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestResolveQuoteMediaEdges(t *testing.T) {
	t.Run("纯图父消息", func(t *testing.T) {
		s := &server{}
		dir := t.TempDir()
		s.media = newMediaStore(dir, &fakeGetter{body: "PNG", ctype: "image/png"}, 0)
		s.quote = &fakeQuoteGetter{byID: map[string]*messageView{
			"om_p": {MessageID: "om_p", MsgType: "image", Content: `{"image_key":"img_v3_pure"}`},
		}}
		q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
		if q == nil || !strings.Contains(q.Text, filepath.Join(dir, "om_p", "img_v3_pure.png")) {
			t.Fatalf("quote = %+v", q)
		}
	})
	t.Run("响应为空", func(t *testing.T) {
		s := &server{}
		s.quote = &fakeQuoteGetter{errs: map[string]error{
			"om_p": errors.New("feishu get message om_p: 响应为空（消息不存在或对机器人不可见）"),
		}}
		q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
		if q == nil || !strings.Contains(q.Text, "响应为空") {
			t.Fatalf("quote = %+v", q)
		}
	})
	t.Run("超过 4 个媒体只下载 4 个", func(t *testing.T) {
		s := &server{}
		g := &fakeGetter{body: "JPEG", ctype: "image/jpeg"}
		s.media = newMediaStore(t.TempDir(), g, 0)
		var paras []string
		for i := range 5 {
			paras = append(paras, `[{"tag":"img","image_key":"img_v3_`+string(rune('a'+i))+`"}]`)
		}
		s.quote = &fakeQuoteGetter{byID: map[string]*messageView{
			"om_p": {MessageID: "om_p", MsgType: "post", Content: `{"content":[` + strings.Join(paras, ",") + `]}`},
		}}
		q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
		if q == nil || len(g.calls) != quoteMaxMedia {
			t.Fatalf("downloads = %d, want %d", len(g.calls), quoteMaxMedia)
		}
		if !strings.Contains(q.Text, "img_v3_e") {
			t.Fatalf("第 5 个媒体必须保留 key 占位: %s", q.Text)
		}
	})
}

// nil 只读客户端按装配约定完全不解析引用（probe/单测路径）。
func TestResolveQuoteNilClient(t *testing.T) {
	s := &server{}
	if q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"}); q != nil {
		t.Fatalf("nil client must not resolve, got %+v", q)
	}
}

func TestResolveQuotePermissionHint(t *testing.T) {
	s := &server{}
	s.quote = &fakeQuoteGetter{errs: map[string]error{
		"om_p": &APIError{Op: "feishu get message", Code: 230027, Msg: "denied", RequestID: "r1"},
	}}
	q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
	if q == nil || !strings.Contains(q.Text, "im:message:readonly") {
		t.Fatalf("缺权限提示缺失: %+v", q)
	}
}

// 真实错误会以 %w 包装 DeadlineExceeded；只比 == 的写法会漏掉。
func TestQuoteFailureWrappedDeadline(t *testing.T) {
	s := &server{}
	s.quote = &fakeQuoteGetter{errs: map[string]error{
		"om_p": fmt.Errorf("feishu get message om_p: %w", context.DeadlineExceeded),
	}}
	q := s.resolveQuote(context.Background(), &eventInfo{messageID: "om_cur", parentID: "om_p"})
	if q == nil || !strings.Contains(q.Text, "超时") {
		t.Fatalf("wrapped deadline = %+v", q)
	}
}

// 引用解析失败不得阻断本条注入：主控照常收到消息，只是带上降级块。
func TestHandleQuoteFailureStillInjects(t *testing.T) {
	h := newServeHarness(t, baseCfg(), nil)
	h.quote = &fakeQuoteGetter{errs: map[string]error{
		"om_p": &APIError{Op: "feishu get message", Code: 230050, Msg: "invisible", RequestID: "r1"},
	}}
	h.handle(replyEvent("p2p", "oc_dm", "ou_alice", "om_cur", "om_p", "这个再改一下"))
	if len(h.payloads) != 1 {
		t.Fatalf("引用失败必须照常注入, inject=%d", len(h.payloads))
	}
	if q := h.payloads[0].Quote; q == nil || !strings.Contains(q.Text, "230050") {
		t.Fatalf("降级块应在 payload 里: %+v", q)
	}
}
