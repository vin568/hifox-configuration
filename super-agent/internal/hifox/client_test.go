package hifox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubClient 记录命令并回固定 stdout。
func stubClient(t *testing.T, out string, err error) (*Client, *[]string) {
	t.Helper()
	var got []string
	c := &Client{Run: func(_ context.Context, args []string) (string, error) {
		got = append([]string{}, args...)
		return out, err
	}}
	return c, &got
}

const createdJSON = `{
  "id": "dd416807-38b7-435d-adf2-97b562d886da",
  "identifier": "HF-12000",
  "title": "[apifox] 修一个 bug",
  "url": "https://app.hifox.com/rayfox/task/HF-12000"
}`

func TestTaskCreatePassesArgsAndParses(t *testing.T) {
	c, got := stubClient(t, createdJSON, nil)
	args := []string{"--space", "Apifox 产研", "--title", "[apifox] 修一个 bug", "--delegate-agent", "wuyong"}
	ref, err := c.TaskCreate(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	want := "task create --space Apifox 产研 --title [apifox] 修一个 bug --delegate-agent wuyong --output json"
	if strings.Join(*got, " ") != want {
		t.Fatalf("args=%v", *got)
	}
	if ref.Identifier != "HF-12000" || ref.URL != "https://app.hifox.com/rayfox/task/HF-12000" || ref.Title == "" {
		t.Fatalf("ref=%+v", ref)
	}
}

// 字段缺失不能算失败：任务已经建成，少 url 也得让派活流程继续。
func TestTaskCreateToleratesMissingFields(t *testing.T) {
	c, _ := stubClient(t, `{"identifier":"HF-1"}`, nil)
	ref, err := c.TaskCreate(context.Background(), []string{"--title", "t"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Identifier != "HF-1" || ref.ID != "" || ref.URL != "" {
		t.Fatalf("ref=%+v", ref)
	}
}

func TestTaskCreateSurfacesRunnerError(t *testing.T) {
	c, _ := stubClient(t, "", errors.New("boom"))
	if _, err := c.TaskCreate(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "task create") {
		t.Fatalf("err=%v", err)
	}
}

func TestTaskCreateRejectsNonJSON(t *testing.T) {
	c, _ := stubClient(t, "Error: space not found", nil)
	if _, err := c.TaskCreate(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "decode created task") {
		t.Fatalf("err=%v", err)
	}
}

// 形状取自 `hifox run list --task HF-11198 --output json` 的实机输出。
const runsJSON = `[
  {"id":"66a3abc3-8a21-4a07-af7a-74608928393a","taskId":"t1","status":"running","agentId":"a1"},
  {"id":"26c06ddb-9822-4d52-9ded-0c5856c21894","taskId":"t1","status":"completed"}
]`

func TestRunListPassesArgsAndParses(t *testing.T) {
	c, got := stubClient(t, runsJSON, nil)
	runs, err := c.RunList(context.Background(), "HF-11198")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*got, " ") != "run list --task HF-11198 --output json" {
		t.Fatalf("args=%v", *got)
	}
	if len(runs) != 2 || runs[0].ID != "66a3abc3-8a21-4a07-af7a-74608928393a" || runs[0].Status != "running" {
		t.Fatalf("runs=%+v", runs)
	}
	// completed 归一到对外词汇。
	if runs[1].Status != "succeeded" {
		t.Fatalf("status=%q", runs[1].Status)
	}
}

func TestRunListEmptyOutput(t *testing.T) {
	c, _ := stubClient(t, "  \n", nil)
	runs, err := c.RunList(context.Background(), "HF-1")
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
}

func TestRunListRejectsNonArray(t *testing.T) {
	c, _ := stubClient(t, `{"runs":[]}`, nil)
	if _, err := c.RunList(context.Background(), "HF-1"); err == nil || !strings.Contains(err.Error(), "decode runs") {
		t.Fatalf("err=%v", err)
	}
}

func TestCancelRunPassesYes(t *testing.T) {
	c, got := stubClient(t, "", nil)
	if err := c.CancelRun(context.Background(), "HF-1", "run-9"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*got, " ") != "task cancel-run HF-1 run-9 -y" {
		t.Fatalf("args=%v", *got)
	}
}

func TestActiveRun(t *testing.T) {
	cases := []struct {
		runs []Run
		want string
		ok   bool
	}{
		{[]Run{{ID: "a", Status: "succeeded"}, {ID: "b", Status: "running"}}, "b", true},
		{[]Run{{ID: "a", Status: "queued"}, {ID: "b", Status: "running"}}, "b", true},
		{[]Run{{ID: "a", Status: "queued"}, {ID: "b", Status: "succeeded"}}, "a", true},
		{[]Run{{ID: "a", Status: "succeeded"}, {ID: "b", Status: "failed"}}, "", false},
		{nil, "", false},
	}
	for _, tc := range cases {
		got, ok := ActiveRun(tc.runs)
		if ok != tc.ok || got.ID != tc.want {
			t.Fatalf("runs=%+v got=%+v ok=%v", tc.runs, got, ok)
		}
	}
}

// 指定实例时，裸名「戴宗」不算命中——否则 --instance 会既报歧义又给不出解法。
func TestResolveDelegateInstanceExcludesBareName(t *testing.T) {
	agents := []AgentRef{{ID: "a", Name: "戴宗"}, {ID: "b", Name: "戴宗 (staging)"}}
	if _, err := ResolveDelegate(agents, "戴宗", "staging"); err != nil {
		t.Fatalf("指定 staging 应唯一命中: %v", err)
	}
	if _, err := ResolveDelegate(agents, "戴宗", ""); err == nil {
		t.Fatal("不指定实例且有裸名+实例名时应报歧义")
	}
	if _, err := ResolveDelegate(agents, "戴宗", "prod"); err == nil {
		t.Fatal("没有 prod 实例时应报找不到")
	}
}
