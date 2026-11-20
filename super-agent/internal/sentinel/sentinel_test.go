package sentinel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"super-agent/internal/hifox"
)

func fixture(t *testing.T, watch string, snap string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	w, s := filepath.Join(dir, "inflight.json"), filepath.Join(dir, "platform-snapshot.json")
	if watch != "" {
		os.WriteFile(w, []byte(watch), 0o644)
	}
	if snap != "" {
		os.WriteFile(s, []byte(snap), 0o644)
	}
	return w, s
}

const sigDone = "completed/已发布|assigned|none"
const sigActive = "active/进行中|working|running"
const sigWaiting = "active/进行中|assigned|waiting_human_reply"

// poll 是测试用的短形态：不带旧快照兼容路径（那条分支另有专门用例）。
func poll(ctx context.Context, client *hifox.Client, inflightPath, snapPath string, inject func(string) error) (*Result, error) {
	return Poll(ctx, client, inflightPath, snapPath, "", inject)
}

func fakeClient(sigs map[string]string) *hifox.Client {
	return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		return taskJSONFor(sigs[args[2]]), nil
	}}
}

func taskJSONFor(sig string) string {
	switch sig {
	case sigDone:
		return `{"identifier":"HF-1","title":"验收","status":{"name":"已发布","category":"completed"},"delegateStatus":"assigned","agentWorkingStatus":"none"}`
	case sigWaiting:
		return `{"identifier":"HF-1","title":"验收","status":{"name":"进行中","category":"active"},"delegateStatus":"assigned","agentWorkingStatus":"waiting_human_reply"}`
	default:
		return `{"identifier":"HF-1","title":"验收","status":{"name":"进行中","category":"active"},"delegateStatus":"working","agentWorkingStatus":"running"}`
	}
}

func TestPollEmptyWatchlistNoop(t *testing.T) {
	w, s := fixture(t, "", "")
	res, err := poll(context.Background(), fakeClient(nil), w, s, func(string) error {
		t.Fatal("must not inject")
		return nil
	})
	if err != nil || res.Watched != 0 || res.Injected {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestPollFirstSightIsBaseline(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, "")
	res, err := poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error {
		t.Fatal("首见不得注入")
		return nil
	})
	if err != nil || res.Watched != 1 || res.Injected {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	b, _ := os.ReadFile(s)
	var snap map[string]string
	if json.Unmarshal(b, &snap) != nil || snap["HF-1"] != sigDone {
		t.Fatalf("snapshot=%s", b)
	}
}

func TestPollChangeInjects(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1","note":"戴宗验收"}]`, `{"HF-1":"`+sigActive+`"}`)
	var got string
	res, err := poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(text string) error { got = text; return nil })
	if err != nil || !res.Injected || len(res.Changed) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	for _, want := range []string{"HF-1", "验收", sigActive, sigDone, "无事不回复用户"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
}

func TestPollUnchangedQuiet(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, `{"HF-1":"`+sigDone+`"}`)
	res, err := poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error {
		t.Fatal("无变化不得注入")
		return nil
	})
	if err != nil || res.Injected {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// 别的平台的条目只记一条 slog、不注入也不查询：哨兵目前只有 hifox 的实现。
func TestPollSkipsOtherPlatforms(t *testing.T) {
	w, s := fixture(t, `[{"kind":"platform","platform":"sharkly","task_id":"SH-1"},{"kind":"platform","task_id":"HF-9"},{"kind":"platform","platform":"hifox","task_id":"HF-1"}]`, "")
	var viewed []string
	client := &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		viewed = append(viewed, args[2])
		return taskJSONFor(sigDone), nil
	}}
	res, err := poll(context.Background(), client, w, s, func(string) error {
		t.Fatal("首见不该注入")
		return nil
	})
	if err != nil || res.Watched != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(viewed) != 1 || viewed[0] != "HF-1" {
		t.Fatalf("只该查 hifox 平台的任务: %v", viewed)
	}
}

// 旧 watchlist 迁移出来的无 kind 条目按 hifox 平台跟踪（升级后不漏跟踪）。
func TestPollTracksLegacyKindlessEntries(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, "")
	res, err := poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error {
		t.Fatal("首见不得注入")
		return nil
	})
	if err != nil || res.Watched != 1 {
		t.Fatalf("无 kind 的旧条目必须仍被跟踪: res=%+v err=%v", res, err)
	}
}

// 评审 P1 回归（物理探针实证过）：注入失败时快照不得推进，下一轮必须重见并重试。
func TestPollInjectFailureRollsBackSnapshot(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, `{"HF-1":"`+sigActive+`"}`)
	client := fakeClient(map[string]string{"HF-1": sigDone})
	injected := 0
	res1, err1 := poll(context.Background(), client, w, s, func(string) error {
		injected++
		return errors.New("master blocked")
	})
	if err1 == nil || res1.Injected {
		t.Fatalf("注入失败必须报错且 Injected=false: %+v %v", res1, err1)
	}
	// 第二轮（注入恢复正常）必须重见同一变化。
	res2, err2 := poll(context.Background(), client, w, s, func(string) error { return nil })
	if err2 != nil || !res2.Injected || len(res2.Changed) != 1 {
		t.Fatalf("变化被丢失！res2=%+v err2=%v", res2, err2)
	}
}

// 评审发现：移出 watchlist 的任务必须从快照清掉，否则同任务重派撞旧签名。
func TestPollPrunesRemovedEntries(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, `{"HF-1":"x","HF-OLD":"stale"}`)
	res, err := poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s)
	var snap map[string]string
	if json.Unmarshal(b, &snap) != nil {
		t.Fatalf("snapshot=%s", b)
	}
	if _, exists := snap["HF-OLD"]; exists {
		t.Fatalf("已移出清单的任务必须清理: %s", b)
	}
	if snap["HF-1"] != sigDone || res.Watched != 1 {
		t.Fatalf("snap=%s res=%+v", b, res)
	}
}

func TestPollCorruptWatchlistSelfHeals(t *testing.T) {
	w, s := fixture(t, `{not json`, "")
	res, err := poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error { return nil })
	if err != nil || res.Watched != 0 {
		t.Fatalf("损坏 watchlist 按空处理: res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(w); !os.IsNotExist(err) {
		t.Fatalf("坏 watchlist 应被改名移走: %v", err)
	}
	if matches, _ := filepath.Glob(w + ".corrupt-*"); len(matches) == 0 {
		t.Fatal("坏 watchlist 应留 .corrupt-* 另存")
	}
}

func TestPollCorruptSnapshotSelfHeals(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, `also not json`)
	res, err := poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error { return nil })
	if err != nil || res.Watched != 1 || res.Injected {
		t.Fatalf("损坏快照按空（首见基线）处理: res=%+v err=%v", res, err)
	}
	// 愈合后本轮会用新基线重建同名快照：断言当前文件是合法 JSON 基线。
	b, rerr := os.ReadFile(s)
	if rerr != nil {
		t.Fatal(rerr)
	}
	var fresh map[string]string
	if json.Unmarshal(b, &fresh) != nil || fresh["HF-1"] != sigDone {
		t.Fatalf("重建的快照应为合法基线: %s", b)
	}
	if matches, _ := filepath.Glob(s + ".corrupt-*"); len(matches) == 0 {
		t.Fatal("坏快照应留 .corrupt-* 另存")
	}
}

// 升级后首轮：新快照还没生成、旧快照（cloud-snapshot.json）在时，必须拿旧文件当基线。
// 否则在途任务全被当成首见重打基线，切换那一刻窗口内的变化就这么丢了。
func TestPollFallsBackToLegacySnapshot(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, "")
	legacy := filepath.Join(filepath.Dir(s), "cloud-snapshot.json")
	if err := os.WriteFile(legacy, []byte(`{"HF-1":"`+sigActive+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var got string
	res, err := Poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, legacy, func(text string) error {
		got = text
		return nil
	})
	if err != nil || !res.Injected || len(res.Changed) != 1 {
		t.Fatalf("旧快照必须当基线，变化不能被吞掉: res=%+v err=%v", res, err)
	}
	if !strings.Contains(got, sigActive) {
		t.Fatalf("变化应基于旧快照的签名: %s", got)
	}
	// 新基线写新文件，旧文件保持不动（迁移期两边并存，不引入清理逻辑）。
	if b, rerr := os.ReadFile(s); rerr != nil || !strings.Contains(string(b), sigDone) {
		t.Fatalf("新快照应已落盘: %v %s", rerr, b)
	}
	if _, serr := os.Stat(legacy); serr != nil {
		t.Fatalf("旧快照不该被删: %v", serr)
	}
}

// waiting_human_reply 的变化必须在注入文本里点名回答通道：承接人在任务对话里
// 提问，回答只投对话（superctl answer），评论与正文都不是回答通道（ADR 0031）。
func TestChangesPromptNamesAnswerChannelOnlyWhenWaiting(t *testing.T) {
	waiting := ChangesPrompt([]Change{{TaskID: "HF-1", Platform: "hifox", Title: "t", From: "a/b|assigned|working", To: "a/b|assigned|waiting_human_reply"}})
	if !strings.Contains(waiting, "- HF-1（平台 hifox，等你回答）") || !strings.Contains(waiting, "superctl answer --platform hifox --task HF-1") {
		t.Fatalf("等回答的变化应点名任务、平台与通道:\n%s", waiting)
	}
	if !strings.Contains(waiting, "不是任务评论") {
		t.Fatalf("必须点破评论不是回答通道:\n%s", waiting)
	}
	other := ChangesPrompt([]Change{{TaskID: "HF-1", Title: "t", From: "a/b|assigned|working", To: "c/d|completed|none"}})
	if strings.Contains(other, "superctl answer") {
		t.Fatalf("非等回答的变化不该带通道提示:\n%s", other)
	}
}

// 混合变化：只点名在等回答的两态（reply / review），已完成任务不得被写成
// 「在等用户回答」（否则主控会把一条已收口的单当成还在等人）。
func TestChangesPromptWaitingTasksFiltersMixedStates(t *testing.T) {
	prompt := ChangesPrompt([]Change{
		{TaskID: "HF-1", Platform: "hifox", Title: "回复态", From: "a/b|assigned|working", To: "a/b|assigned|waiting_human_reply"},
		{TaskID: "HF-2", Platform: "hifox", Title: "已完成", From: "a/b|assigned|working", To: "c/d|completed|none"},
		{TaskID: "HF-3", Platform: "hifox", Title: "评审态", From: "a/b|assigned|working", To: "a/b|assigned|waiting_human_review"},
	})
	if !strings.Contains(prompt, "- HF-1（平台 hifox，等你回答）") || !strings.Contains(prompt, "- HF-3（平台 hifox，等你验收）") {
		t.Fatalf("只应点名两态任务:\n%s", prompt)
	}
	if strings.Contains(prompt, "- HF-2（平台 hifox") {
		t.Fatalf("已完成任务不得写成等回答:\n%s", prompt)
	}
}

// 平台通用路径不得写死 hifox：条目带什么平台，注入的命令就带什么平台。
func TestChangesPromptUsesEntryPlatform(t *testing.T) {
	prompt := ChangesPrompt([]Change{{TaskID: "T-1", Platform: "sharkly", From: "a/b|x|working", To: "a/b|x|waiting_human_reply"}})
	if !strings.Contains(prompt, "--platform sharkly --task T-1") {
		t.Fatalf("应使用条目自己的平台:\n%s", prompt)
	}
	if strings.Contains(prompt, "--platform hifox") {
		t.Fatalf("不该把 hifox 写死:\n%s", prompt)
	}
	// 缺平台不猜：宁可露出空值让人补，也不替条目编一个平台名。
	blank := ChangesPrompt([]Change{{TaskID: "T-2", From: "a/b|x|working", To: "a/b|x|waiting_human_reply"}})
	if strings.Contains(blank, "hifox") {
		t.Fatalf("缺平台时不该补 hifox:\n%s", blank)
	}
}

// 首见只记基线是为了防启动噪声，但「在等用户回话」不能静默：部署前就在等、或
// 派活时就已在等的任务，首见必须立刻注入通道提示，否则主控不知道裸回答属于哪张单。
func TestPollFirstSightWaitingIsNotSilent(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1","platform":"hifox","note":"在等回答"}]`, "")
	var got string
	res, err := poll(context.Background(), fakeClient(map[string]string{"HF-1": sigWaiting}), w, s, func(text string) error {
		got = text
		return nil
	})
	if err != nil || !res.Injected || len(res.Changed) != 1 {
		t.Fatalf("首见等回答必须注入: res=%+v err=%v", res, err)
	}
	if !strings.Contains(got, "HF-1（平台 hifox，等你回答）") {
		t.Fatalf("注入应点名等回答的任务:\n%s", got)
	}
}

// 首见不是等回答的任务仍然只记基线：启动噪声不能把在途任务全打一遍。
func TestPollFirstSightActiveStaysBaseline(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1","platform":"hifox"}]`, "")
	res, err := poll(context.Background(), fakeClient(map[string]string{"HF-1": sigActive}), w, s, func(string) error {
		t.Fatal("首见 working 不该注入")
		return nil
	})
	if err != nil || res.Injected {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
