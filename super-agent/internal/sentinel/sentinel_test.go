package sentinel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/hifox"
)

func fixture(t *testing.T, watch string, snap string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	w, s := filepath.Join(dir, "inflight.json"), filepath.Join(dir, "cloud-snapshot.json")
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

func fakeClient(sigs map[string]string) *hifox.Client {
	return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		return taskJSONFor(sigs[args[2]]), nil
	}}
}

func taskJSONFor(sig string) string {
	if sig == sigDone {
		return `{"identifier":"HF-1","title":"验收","status":{"name":"已发布","category":"completed"},"delegateStatus":"assigned","agentWorkingStatus":"none"}`
	}
	return `{"identifier":"HF-1","title":"验收","status":{"name":"进行中","category":"active"},"delegateStatus":"working","agentWorkingStatus":"running"}`
}

func TestPollEmptyWatchlistNoop(t *testing.T) {
	w, s := fixture(t, "", "")
	res, err := Poll(context.Background(), fakeClient(nil), w, s, func(string) error {
		t.Fatal("must not inject")
		return nil
	})
	if err != nil || res.Watched != 0 || res.Injected {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestPollFirstSightIsBaseline(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, "")
	res, err := Poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error {
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
	res, err := Poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(text string) error { got = text; return nil })
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
	res, err := Poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error {
		t.Fatal("无变化不得注入")
		return nil
	})
	if err != nil || res.Injected {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// 评审 P1 回归（物理探针实证过）：注入失败时快照不得推进，下一轮必须重见并重试。
func TestPollInjectFailureRollsBackSnapshot(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, `{"HF-1":"`+sigActive+`"}`)
	client := fakeClient(map[string]string{"HF-1": sigDone})
	injected := 0
	res1, err1 := Poll(context.Background(), client, w, s, func(string) error {
		injected++
		return errors.New("master blocked")
	})
	if err1 == nil || res1.Injected {
		t.Fatalf("注入失败必须报错且 Injected=false: %+v %v", res1, err1)
	}
	// 第二轮（注入恢复正常）必须重见同一变化。
	res2, err2 := Poll(context.Background(), client, w, s, func(string) error { return nil })
	if err2 != nil || !res2.Injected || len(res2.Changed) != 1 {
		t.Fatalf("变化被丢失！res2=%+v err2=%v", res2, err2)
	}
}

// 评审发现：移出 watchlist 的任务必须从快照清掉，否则同任务重派撞旧签名。
func TestPollPrunesRemovedEntries(t *testing.T) {
	w, s := fixture(t, `[{"task_id":"HF-1"}]`, `{"HF-1":"x","HF-OLD":"stale"}`)
	res, err := Poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error { return nil })
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
	res, err := Poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error { return nil })
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
	res, err := Poll(context.Background(), fakeClient(map[string]string{"HF-1": sigDone}), w, s, func(string) error { return nil })
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
