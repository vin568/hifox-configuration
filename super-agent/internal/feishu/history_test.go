package feishu

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeMessageReader 同时实现 Get/List，记录调用参数。
type fakeMessageReader struct {
	msgs    []*messageView
	hasMore bool
	err     error
	byID    map[string]*messageView
	getErr  error
	gotChat string
	gotFrom time.Time
	gotTo   time.Time
	gotN    int
}

func (f *fakeMessageReader) Get(_ context.Context, id string) (*messageView, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if v, ok := f.byID[id]; ok {
		return v, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeMessageReader) List(_ context.Context, chatID string, from, to time.Time, n int) ([]*messageView, bool, error) {
	f.gotChat, f.gotFrom, f.gotTo, f.gotN = chatID, from, to, n
	if f.err != nil {
		return nil, false, f.err
	}
	return f.msgs, f.hasMore, nil
}

func view(ms int64, sender, senderType, msgType, content string) *messageView {
	return &messageView{
		MessageID: "om_x", ChatID: "oc_x", SenderID: sender, SenderType: senderType,
		MsgType: msgType, Content: content, CreateMS: ms,
	}
}

func TestHistoryText(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.Local)
	q := HistoryQuery{ChatID: "oc_x", Limit: 20, Since: 24 * time.Hour, Now: now}

	t.Run("用户与应用两条 + 正序", func(t *testing.T) {
		r := &fakeMessageReader{msgs: []*messageView{
			view(now.Add(-1*time.Hour).UnixMilli(), "cli_9f4", "app", "text", `{"text":"收到，我安排一下"}`),
			view(now.Add(-2*time.Hour).UnixMilli(), "ou_alice", "user", "text", `{"text":"跑一次 staging 验收"}`),
		}}
		out, err := historyText(context.Background(), r, nil, q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "{") {
			t.Fatalf("不能把 JSON 抛给主控:\n%s", out)
		}
		lines := strings.Split(out, "\n")
		if !strings.Contains(lines[1], "ou_alice") || !strings.Contains(lines[2], "（应用）") {
			t.Fatalf("顺序或发送者渲染不对:\n%s", out)
		}
	})

	t.Run("多行折叠", func(t *testing.T) {
		r := &fakeMessageReader{msgs: []*messageView{
			view(now.UnixMilli(), "ou_a", "user", "text", `{"text":"第一行\n第二行"}`),
		}}
		out, _ := historyText(context.Background(), r, nil, q)
		if !strings.Contains(out, "第一行 / 第二行") {
			t.Fatalf("多行应折叠:\n%s", out)
		}
	})

	t.Run("媒体 key 占位", func(t *testing.T) {
		r := &fakeMessageReader{msgs: []*messageView{
			view(now.UnixMilli(), "ou_a", "user", "image", `{"image_key":"img_v3_abc"}`),
		}}
		out, _ := historyText(context.Background(), r, nil, q)
		if !strings.Contains(out, "img_v3_abc") {
			t.Fatalf("列表模式必须保留 key:\n%s", out)
		}
	})

	t.Run("已撤回与空正文", func(t *testing.T) {
		recalled := view(now.UnixMilli(), "ou_a", "user", "text", "")
		recalled.Deleted = true
		sticker := view(now.UnixMilli(), "ou_a", "user", "sticker", "")
		r := &fakeMessageReader{msgs: []*messageView{sticker, recalled}}
		out, _ := historyText(context.Background(), r, nil, q)
		if !strings.Contains(out, historyRecalledLabel) || !strings.Contains(out, "[消息类型 sticker]") {
			t.Fatalf("占位文案缺失:\n%s", out)
		}
	})

	t.Run("超 200 rune 截断", func(t *testing.T) {
		long := strings.Repeat("汉", historyLineMaxRunes+10)
		r := &fakeMessageReader{msgs: []*messageView{
			view(now.UnixMilli(), "ou_a", "user", "text", `{"text":"`+long+`"}`),
		}}
		out, _ := historyText(context.Background(), r, nil, q)
		if !strings.HasSuffix(out, "…") {
			t.Fatalf("超长正文应截断:\n%s", out)
		}
	})

	t.Run("空列表", func(t *testing.T) {
		out, err := historyText(context.Background(), &fakeMessageReader{}, nil, q)
		if err != nil || out != historyEmptyWindow {
			t.Fatalf("out=%q err=%v", out, err)
		}
	})

	t.Run("has_more footer", func(t *testing.T) {
		r := &fakeMessageReader{hasMore: true, msgs: []*messageView{
			view(now.UnixMilli(), "ou_a", "user", "text", `{"text":"x"}`),
		}}
		out, _ := historyText(context.Background(), r, nil, q)
		if !strings.Contains(out, "更早还有") {
			t.Fatalf("缺 has_more 提示:\n%s", out)
		}
	})
}

func TestHistoryQueryValidation(t *testing.T) {
	r := &fakeMessageReader{}
	now := time.Now()
	cases := []struct {
		name string
		q    HistoryQuery
	}{
		{"limit 0", HistoryQuery{ChatID: "oc_x", Limit: 0, Since: time.Hour, Now: now}},
		{"limit 51", HistoryQuery{ChatID: "oc_x", Limit: 51, Since: time.Hour, Now: now}},
		{"since 0", HistoryQuery{ChatID: "oc_x", Limit: 20, Since: 0, Now: now}},
		{"since 169h", HistoryQuery{ChatID: "oc_x", Limit: 20, Since: 169 * time.Hour, Now: now}},
		{"both", HistoryQuery{ChatID: "oc_x", MessageID: "om_x", Now: now}},
		{"neither", HistoryQuery{Now: now}},
		{"message+limit", HistoryQuery{MessageID: "om_x", Limit: 5, Now: now}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := historyText(context.Background(), r, nil, c.q); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestHistoryListWindow(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.Local)
	r := &fakeMessageReader{msgs: []*messageView{
		view(now.UnixMilli(), "ou_a", "user", "text", `{"text":"x"}`),
	}}
	_, err := historyText(context.Background(), r, nil, HistoryQuery{
		ChatID: "oc_x", Limit: 7, Since: 2 * time.Hour, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.gotChat != "oc_x" || r.gotN != 7 {
		t.Fatalf("list args = %s/%d", r.gotChat, r.gotN)
	}
	if !r.gotFrom.Equal(now.Add(-2*time.Hour)) || !r.gotTo.Equal(now) {
		t.Fatalf("window = %v ~ %v", r.gotFrom, r.gotTo)
	}
}

func TestHistoryMessageDownload(t *testing.T) {
	dir := t.TempDir()
	g := &fakeGetter{body: "JPEG", ctype: "image/jpeg"}
	store := newMediaStore(dir, g, 0)
	r := &fakeMessageReader{byID: map[string]*messageView{
		"om_x": {MessageID: "om_x", ChatID: "oc_x", MsgType: "post", SenderID: "ou_bob",
			Content: `{"content":[[{"tag":"text","text":"复现"}],[{"tag":"img","image_key":"img_v3_abc"}]]}`},
	}}
	out, err := historyText(context.Background(), r, store, HistoryQuery{MessageID: "om_x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.calls) != 1 {
		t.Fatalf("download calls = %v", g.calls)
	}
	if !strings.Contains(out, "复现") || !strings.Contains(out, "/om_x/img_v3_abc.jpg") {
		t.Fatalf("单条下载输出:\n%s", out)
	}
	if _, err := historyText(context.Background(), r, nil, HistoryQuery{MessageID: "om_x"}); err != nil {
		t.Fatal(err)
	}
	if len(g.calls) != 1 {
		t.Fatalf("不带 --download 时不该下载: %v", g.calls)
	}
}

func TestHistoryErrorHints(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{230002, "机器人不在这个会话里"},
		{230050, "对机器人不可见"},
		{230073, "这个话题"},
		{230110, "已撤回或不存在"},
		{230027, "im:message:readonly"},
		{231203, "不支持读历史"},
	}
	for _, c := range cases {
		r := &fakeMessageReader{err: &APIError{Op: "feishu list messages", Code: c.code, Msg: "x", RequestID: "r"}}
		_, err := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 5, Since: time.Hour, Now: time.Now(),
		})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("code %d → %v, want %q", c.code, err, c.want)
		}
	}
}

func TestHistoryFooterAndDeletedHint(t *testing.T) {
	now := time.Now()
	r := &fakeMessageReader{hasMore: true, msgs: []*messageView{
		view(now.UnixMilli(), "ou_a", "user", "image", `{"image_key":"img_v3_a"}`),
	}}
	out, _ := historyText(context.Background(), r, nil, HistoryQuery{ChatID: "oc_x", Limit: 1, Since: time.Hour, Now: now})
	if !strings.Contains(out, "更早还有") || !strings.Contains(out, "--message <消息ID> --download") {
		t.Fatalf("footer 缺附件提示:\n%s", out)
	}

	// 没有 has_more 但这一页有附件：仍要给下载入口。
	r = &fakeMessageReader{msgs: []*messageView{
		view(now.UnixMilli(), "ou_a", "user", "image", `{"image_key":"img_v3_a"}`),
	}}
	out, _ = historyText(context.Background(), r, nil, HistoryQuery{ChatID: "oc_x", Limit: 5, Since: time.Hour, Now: now})
	if !strings.Contains(out, "--message <消息ID> --download") {
		t.Fatalf("单页附件缺下载提示:\n%s", out)
	}

	r = &fakeMessageReader{getErr: &APIError{Op: "feishu get message", Code: 230110, Msg: "deleted", RequestID: "r"}}
	_, err := historyText(context.Background(), r, nil, HistoryQuery{MessageID: "om_gone"})
	if err == nil || !strings.Contains(err.Error(), "om_gone") {
		t.Fatalf("单条 230110 必须带消息 id: %v", err)
	}
}
