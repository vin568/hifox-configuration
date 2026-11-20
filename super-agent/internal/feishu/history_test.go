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
	// pages 非空时按页返回：第 i 次调用返回 pages[i]，仅当还有下一页时才给 token。
	pages     [][]*messageView
	err       error
	byID      map[string]*messageView
	getErr    error
	gotChat   string
	gotFrom   time.Time
	gotTo     time.Time
	gotN      []int
	gotTokens []string
	// errAt 非零时第 errAt 次调用返回错误（模拟中途某页失败）。
	errAt int
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

func (f *fakeMessageReader) List(_ context.Context, chatID string, from, to time.Time, n int, token string) ([]*messageView, string, bool, error) {
	f.gotChat, f.gotFrom, f.gotTo = chatID, from, to
	f.gotN = append(f.gotN, n)
	f.gotTokens = append(f.gotTokens, token)
	if f.err != nil {
		return nil, "", false, f.err
	}
	if f.errAt > 0 && len(f.gotTokens) == f.errAt {
		return nil, "", false, errors.New("page failed")
	}
	if n < 1 || n > HistoryPageSize {
		// 真实接口只接受 1..50：假件也必须拒，否则本地钳位失效也测不出来。
		return nil, "", false, errors.New("invalid page_size")
	}
	if f.pages != nil {
		i := len(f.gotTokens) - 1
		if i >= len(f.pages) {
			return nil, "", false, nil
		}
		next := ""
		more := i+1 < len(f.pages)
		if more {
			next = "tok" + string(rune('1'+i))
		}
		page := f.pages[i]
		if n < len(page) {
			page = page[:n]
		}
		return page, next, more, nil
	}
	// hasMore 只服务「平台报有更多」的单页场景；再翻就没有内容了。
	if f.hasMore {
		if len(f.gotTokens) == 1 {
			return f.msgs, "tok-more", true, nil
		}
		return nil, "", false, nil
	}
	return f.msgs, "", false, nil
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

	t.Run("has_more footer 且点明已到 limit", func(t *testing.T) {
		// 三页：凑够 limit 2 后 platform 仍报还有更多。
		r := &fakeMessageReader{pages: [][]*messageView{{
			view(now.UnixMilli(), "ou_a", "user", "text", `{"text":"x"}`),
		}, {
			view(now.Add(-time.Hour).UnixMilli(), "ou_a", "user", "text", `{"text":"y"}`),
		}, {
			view(now.Add(-2*time.Hour).UnixMilli(), "ou_a", "user", "text", `{"text":"z"}`),
		}}}
		out, _ := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 2, Since: 24 * time.Hour, Now: now,
		})
		if !strings.Contains(out, "更早还有") || !strings.Contains(out, "已到 --limit 2") {
			t.Fatalf("页脚应点明已到 limit:\n%s", out)
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
		{"since 0", HistoryQuery{ChatID: "oc_x", Limit: 20, Since: 0, Now: now}},
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
	if r.gotChat != "oc_x" || len(r.gotN) != 1 || r.gotN[0] != 7 {
		t.Fatalf("list args = %s/%v", r.gotChat, r.gotN)
	}
	if !r.gotFrom.Equal(now.Add(-2*time.Hour)) || !r.gotTo.Equal(now) {
		t.Fatalf("window = %v ~ %v", r.gotFrom, r.gotTo)
	}
}

func TestHistoryListPaging(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.Local)
	mk := func(n int, base int64) []*messageView {
		out := make([]*messageView, 0, n)
		for i := range n {
			out = append(out, view(base+int64(i)*1000, "ou_a", "user", "text", `{"text":"x"}`))
		}
		return out
	}

	t.Run("超过单页自动翻页凑够 limit", func(t *testing.T) {
		r := &fakeMessageReader{pages: [][]*messageView{
			mk(50, now.Add(-3*time.Hour).UnixMilli()),
			mk(50, now.Add(-2*time.Hour).UnixMilli()),
			mk(50, now.Add(-time.Hour).UnixMilli()),
		}}
		out, err := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 120, Since: 30 * 24 * time.Hour, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.gotN) != 3 || r.gotN[0] != 50 || r.gotN[1] != 50 || r.gotN[2] != 20 {
			t.Fatalf("翻页 page_size = %v", r.gotN)
		}
		if r.gotTokens[0] != "" || r.gotTokens[1] != "tok1" || r.gotTokens[2] != "tok2" {
			t.Fatalf("翻页 token = %v", r.gotTokens)
		}
		if strings.Count(out, "ou_a") != 120 {
			t.Fatalf("应返回 120 行，实际 %d:\n%s", strings.Count(out, "ou_a"), out)
		}
		if strings.Contains(out, "更早还有") {
			t.Fatalf("凑够 limit 且无更多时不该提示更早还有:\n%s", out)
		}
	})

	t.Run("翻到尽头未满 limit 时不再提示更早还有", func(t *testing.T) {
		r := &fakeMessageReader{pages: [][]*messageView{
			mk(50, now.Add(-2*time.Hour).UnixMilli()),
			mk(5, now.Add(-time.Hour).UnixMilli()),
		}}
		out, _ := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 500, Since: 30 * 24 * time.Hour, Now: now,
		})
		if strings.Contains(out, "更早还有") {
			t.Fatalf("已翻到尽头不该提示更早还有:\n%s", out)
		}
		if strings.Count(out, "ou_a") != 55 {
			t.Fatalf("应返回 55 行:\n%s", out)
		}
	})

	t.Run("还有更多且本页有附件时两个提示都在", func(t *testing.T) {
		// limit 1 在第一页后即停，platform 仍报 has_more：两个提示都要出。
		r := &fakeMessageReader{pages: [][]*messageView{{
			view(now.UnixMilli(), "ou_a", "user", "image", `{"image_key":"img_v3_abc"}`),
		}, {
			view(now.Add(-time.Hour).UnixMilli(), "ou_a", "user", "text", `{"text":"y"}`),
		}}}
		out, _ := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 1, Since: 24 * time.Hour, Now: now,
		})
		if !strings.Contains(out, "更早还有") || !strings.Contains(out, "要看附件") {
			t.Fatalf("页脚缺提示:\n%s", out)
		}
	})

	t.Run("跨页整体反转成时间正序", func(t *testing.T) {
		// 平台按 ByCreateTimeDesc 返回：页内与页间都是「新→旧」。
		mkDesc := func(times ...int64) []*messageView {
			out := make([]*messageView, 0, len(times))
			for _, ms := range times {
				out = append(out, view(ms, "ou_a", "user", "text", `{"text":"x"}`))
			}
			return out
		}
		h := func(n int64) int64 { return now.Add(-time.Duration(n) * time.Hour).UnixMilli() }
		r := &fakeMessageReader{pages: [][]*messageView{
			mkDesc(h(1), h(2)),
			mkDesc(h(3), h(4)),
		}}
		out, err := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 4, Since: 30 * 24 * time.Hour, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		stamps := make([]string, 0, 4)
		for _, line := range strings.Split(out, "\n")[1:] {
			if line == "" || strings.HasPrefix(line, "（") {
				continue
			}
			stamps = append(stamps, line[:len(historyTimeLayout)])
		}
		want := []string{
			time.UnixMilli(h(4)).Local().Format(historyTimeLayout),
			time.UnixMilli(h(3)).Local().Format(historyTimeLayout),
			time.UnixMilli(h(2)).Local().Format(historyTimeLayout),
			time.UnixMilli(h(1)).Local().Format(historyTimeLayout),
		}
		if strings.Join(stamps, "|") != strings.Join(want, "|") {
			t.Fatalf("跨页必须整体正序\n got=%v\nwant=%v\n%s", stamps, want, out)
		}
	})

	t.Run("中途某页报错整单失败", func(t *testing.T) {
		r := &fakeMessageReader{
			pages: [][]*messageView{mk(50, now.Add(-2*time.Hour).UnixMilli()), mk(50, now.Add(-time.Hour).UnixMilli())},
			errAt: 2,
		}
		if _, err := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 100, Since: 30 * 24 * time.Hour, Now: now,
		}); err == nil || !strings.Contains(err.Error(), "page failed") {
			t.Fatalf("第二页失败必须整单报错，err=%v", err)
		}
	})

	t.Run("平台报还有但没给 token 时仍提示", func(t *testing.T) {
		r := &noTokenMoreReader{page: []*messageView{
			view(now.UnixMilli(), "ou_a", "user", "text", `{"text":"x"}`),
		}}
		out, _ := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 20, Since: 24 * time.Hour, Now: now,
		})
		if !strings.Contains(out, "更早还有") {
			t.Fatalf("平台报有更多时必须提示:\n%s", out)
		}
	})

	t.Run("后续页无 token 即停", func(t *testing.T) {
		// 首页给 token 能续翻，第二页平台报还有更多但没给 token：停，不空转。
		r := &secondPageNoTokenReader{}
		out, err := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 100, Since: 24 * time.Hour, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if r.calls != 2 {
			t.Fatalf("第二页无 token 应停，calls=%d", r.calls)
		}
		if !strings.Contains(out, "更早还有") {
			t.Fatalf("应提示更早还有:\n%s", out)
		}
	})

	t.Run("平台多给也不超过 limit", func(t *testing.T) {
		r := &overReturnReader{page: mk(60, now.Add(-time.Hour).UnixMilli())}
		out, err := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 20, Since: 30 * 24 * time.Hour, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(out, "ou_a") != 20 {
			t.Fatalf("应截到 20 行:\n%s", out)
		}
	})

	t.Run("空页即停，不再耗配额", func(t *testing.T) {
		r := &emptyPageReader{}
		out, err := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 100, Since: 30 * 24 * time.Hour, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if r.calls != 1 {
			t.Fatalf("空页后应停，calls=%d", r.calls)
		}
		if out != historyEmptyWindow {
			t.Fatalf("out=%q", out)
		}
	})

	t.Run("token 原地踏步即停（防死循环）", func(t *testing.T) {
		r := &stuckTokenReader{page: mk(50, now.Add(-time.Hour).UnixMilli())}
		out, err := historyText(context.Background(), r, nil, HistoryQuery{
			ChatID: "oc_x", Limit: 500, Since: 30 * 24 * time.Hour, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if r.calls != 2 {
			t.Fatalf("token 不变时应只多拉一页就停，calls=%d", r.calls)
		}
		if strings.Count(out, "ou_a") != 100 {
			t.Fatalf("应返回两页 100 行:\n%s", out)
		}
	})
}

// emptyPageReader 返回空页但仍声称还有更多、并给出新 token。
type emptyPageReader struct{ calls int }

func (e *emptyPageReader) Get(context.Context, string) (*messageView, error) {
	return nil, errors.New("not implemented")
}

func (e *emptyPageReader) List(context.Context, string, time.Time, time.Time, int, string) ([]*messageView, string, bool, error) {
	e.calls++
	return nil, "tok" + string(rune('0'+e.calls)), true, nil
}

// secondPageNoTokenReader 首页带 token，第二页 has_more 但 token 为空。
type secondPageNoTokenReader struct{ calls int }

func (s *secondPageNoTokenReader) Get(context.Context, string) (*messageView, error) {
	return nil, errors.New("not implemented")
}

func (s *secondPageNoTokenReader) List(_ context.Context, _ string, _ time.Time, _ time.Time, _ int, token string) ([]*messageView, string, bool, error) {
	s.calls++
	if token == "" {
		return []*messageView{view(2, "ou_a", "user", "text", `{"text":"a"}`)}, "next", true, nil
	}
	return []*messageView{view(1, "ou_a", "user", "text", `{"text":"b"}`)}, "", true, nil
}

// noTokenMoreReader 报 has_more 但不给 page_token（真实 SDK 可能出现）。
type noTokenMoreReader struct{ page []*messageView }

func (n *noTokenMoreReader) Get(context.Context, string) (*messageView, error) {
	return nil, errors.New("not implemented")
}

func (n *noTokenMoreReader) List(context.Context, string, time.Time, time.Time, int, string) ([]*messageView, string, bool, error) {
	return n.page, "", true, nil
}

// overReturnReader 无视 page_size 多给消息：验证本地仍按 --limit 截断。
type overReturnReader struct{ page []*messageView }

func (o *overReturnReader) Get(context.Context, string) (*messageView, error) {
	return nil, errors.New("not implemented")
}

func (o *overReturnReader) List(context.Context, string, time.Time, time.Time, int, string) ([]*messageView, string, bool, error) {
	return o.page, "next", true, nil
}

// stuckTokenReader 每页都声称还有更多，但下一页 token 永远是同一个。
type stuckTokenReader struct {
	page  []*messageView
	calls int
}

func (s *stuckTokenReader) Get(context.Context, string) (*messageView, error) {
	return nil, errors.New("not implemented")
}

func (s *stuckTokenReader) List(context.Context, string, time.Time, time.Time, int, string) ([]*messageView, string, bool, error) {
	s.calls++
	return s.page, "same", true, nil
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
