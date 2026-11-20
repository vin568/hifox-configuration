package hifox

import (
	"context"
	"encoding/json"
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

// --instance 的语义：prod 是「无后缀」那份（平台上的裸名），staging 是带后缀那份；
// 不指定时多实例必须报歧义，别替人猜。
func TestResolveDelegateInstanceSemantics(t *testing.T) {
	agents := []AgentRef{{ID: "a", Name: "戴宗"}, {ID: "b", Name: "戴宗 (staging)"}}
	if ref, err := ResolveDelegate(agents, "戴宗", "staging"); err != nil || ref.ID != "b" {
		t.Fatalf("指定 staging 应命中带后缀那份: %+v %v", ref, err)
	}
	if ref, err := ResolveDelegate(agents, "戴宗", "prod"); err != nil || ref.ID != "a" {
		t.Fatalf("指定 prod 应命中无后缀那份: %+v %v", ref, err)
	}
	if _, err := ResolveDelegate(agents, "戴宗", ""); err == nil {
		t.Fatal("不指定实例且有裸名+实例名时应报歧义")
	}
}

// 只有 staging 那一份时，--instance prod 必须如实报找不到：prod 指的是「无后缀」这个
// 具体实例，不是「随便哪一份」（旧实现把两者混为一谈，prod 反而恒零命中）。
func TestResolveDelegateProdMissingOnStagingOnly(t *testing.T) {
	agents := []AgentRef{{ID: "b", Name: "戴宗 (staging)"}}
	if _, err := ResolveDelegate(agents, "戴宗", "prod"); err == nil || !strings.Contains(err.Error(), "找不到") {
		t.Fatalf("只有 staging 时指定 prod 应报找不到: %v", err)
	}
	if ref, err := ResolveDelegate(agents, "戴宗", "staging"); err != nil || ref.ID != "b" {
		t.Fatalf("staging 仍应命中: %+v %v", ref, err)
	}
}

// 注册表对账（capability.Check）与派活解析共用这一套「同名」判定，边界行为要有表。
func TestNameMatches(t *testing.T) {
	cases := []struct {
		declared, candidate string
		want                bool
	}{
		{"巴德尔", "巴德尔 Baldur", true},
		{"萧", "萧让", false},
		{"吴用", "吴用改", false},
		{"吴用", "吴用", true},
		{"吴用", "吴", false},
	}
	for _, c := range cases {
		if got := NameMatches(c.declared, c.candidate); got != c.want {
			t.Errorf("NameMatches(%q, %q) = %v, want %v", c.declared, c.candidate, got, c.want)
		}
	}
}

// 平台名字带实例后缀（两种写法）时先剥后缀再比——线上对账用这条。
func TestPlatformNameMatches(t *testing.T) {
	cases := []struct {
		declared, platformName string
		want                   bool
	}{
		{"吴用", "吴用 (staging)", true},
		{"燕青", "燕青 - staging", true},
		{"巴德尔", "巴德尔 Baldur - staging", true},
		{"萧", "萧让 (staging)", false},
		{"吴用", "卢克 Luke", false},
	}
	for _, c := range cases {
		if got := PlatformNameMatches(c.declared, c.platformName); got != c.want {
			t.Errorf("PlatformNameMatches(%q, %q) = %v, want %v", c.declared, c.platformName, got, c.want)
		}
	}
}

// AgentListAll 走 --include-archived（对账要看归档状态），并解析出对账用的字段。
func TestAgentListAllIncludesArchivedAndParsesFields(t *testing.T) {
	out := `[{"id":"a1","name":"戴宗 (staging)","jobTitle":"验收","spaceIds":["s1","s2"],` +
		`"archivedAt":"2026-01-01T00:00:00Z","allowTaskAssignment":true,"canManage":false,"status":"active"}]`
	c, got := stubClient(t, out, nil)
	refs, err := c.AgentListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "agent list --include-archived --output json"; strings.Join(*got, " ") != want {
		t.Fatalf("args=%v", *got)
	}
	if len(refs) != 1 {
		t.Fatalf("refs=%+v", refs)
	}
	r := refs[0]
	if r.ID != "a1" || r.Name != "戴宗 (staging)" || len(r.SpaceIDs) != 2 || r.ArchivedAt == "" ||
		!r.AllowTaskAssignment || r.CanManage || r.Status != "active" {
		t.Fatalf("ref=%+v", r)
	}
}

// AgentList 不带 --include-archived：派活只认在册项。
func TestAgentListStaysWithoutArchivedFlag(t *testing.T) {
	c, got := stubClient(t, `[]`, nil)
	if _, err := c.AgentList(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := "agent list --output json"; strings.Join(*got, " ") != want {
		t.Fatalf("args=%v", *got)
	}
}

// JSON null 解成零值即可：平台上没归档/没配空间不该让对账炸掉。
func TestAgentListToleratesNullFields(t *testing.T) {
	out := `[{"id":"a1","name":"奥丁","jobTitle":null,"spaceIds":null,"archivedAt":null,` +
		`"allowTaskAssignment":false,"canManage":null,"status":null}]`
	c, _ := stubClient(t, out, nil)
	refs, err := c.AgentList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Name != "奥丁" || refs[0].ArchivedAt != "" || refs[0].SpaceIDs != nil {
		t.Fatalf("refs=%+v", refs)
	}
}

// 回答走人工消息接口：`hifox api post /rest/v1/chat/sessions/<id>/messages`。
// 不是 `chat send`（agent-messages 只认执行身份，人工令牌 403），也不是任务评论。
func TestPostUserMessageUsesHumanMessagesEndpoint(t *testing.T) {
	c, got := stubClient(t, `{"messageId":"m-1","runId":"r-1","queued":true}`, nil)
	msg, err := c.PostUserMessage(context.Background(), "sess-1", "跟 web 完全对齐\ndevelop 吧")
	if err != nil {
		t.Fatal(err)
	}
	want := `api post /rest/v1/chat/sessions/sess-1/messages --data {"content":"跟 web 完全对齐\ndevelop 吧"} --output json`
	if strings.Join(*got, " ") != want {
		t.Fatalf("args=%v", *got)
	}
	if msg.MessageID != "m-1" || msg.RunID != "r-1" || !msg.Queued {
		t.Fatalf("msg=%+v", msg)
	}
}

// 没有会话或空正文是调用错误，不该发一个空 JSON 出去。
func TestPostUserMessageRejectsEmptyInputs(t *testing.T) {
	c, got := stubClient(t, `{}`, nil)
	if _, err := c.PostUserMessage(context.Background(), " ", "x"); err == nil || !strings.Contains(err.Error(), "没有可投的对话会话") {
		t.Fatalf("空会话必须拒绝: %v", err)
	}
	if _, err := c.PostUserMessage(context.Background(), "sess-1", "  "); err == nil {
		t.Fatal("空正文必须拒绝")
	}
	if len(*got) != 0 {
		t.Fatalf("被拒绝的调用不该触达 CLI: %v", *got)
	}
}

// 多会话任务：chatSessionId 是「与当前执行者最老的那条」，活着的会话可能是另一条
// （sessionStatus.top.sessionId）。投错会话，承接人不会被唤醒（等回答时）或补不进
// 正在跑的上下文（working 时）。
func TestAnswerSessionIDPrefersWaitingSession(t *testing.T) {
	cases := []struct {
		name, json, want string
	}{
		{
			name: "等回答的会话优先于主会话",
			json: `{"chatSessionId":"main","sessionStatus":{"top":{"state":"waiting_human_reply","sessionId":"asking"}}}`,
			want: "asking",
		},
		{
			name: "评审态同样算等回答",
			json: `{"chatSessionId":"main","sessionStatus":{"top":{"state":"waiting_human_review","sessionId":"asking"}}}`,
			want: "asking",
		},
		{
			name: "执行中也投活着的会话",
			json: `{"chatSessionId":"main","sessionStatus":{"top":{"state":"working","sessionId":"other"}}}`,
			want: "other",
		},
		{
			name: "没有聚合状态（老任务）回落到主会话",
			json: `{"chatSessionId":"main"}`,
			want: "main",
		},
		{
			name: "top 有状态没会话 id",
			json: `{"chatSessionId":"main","sessionStatus":{"top":{"state":"waiting_human_reply"}}}`,
			want: "main",
		},
		{
			name: "两者都没有",
			json: `{"identifier":"HF-1"}`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var task Task
			if err := json.Unmarshal([]byte(tc.json), &task); err != nil {
				t.Fatal(err)
			}
			if got := task.AnswerSessionID(); got != tc.want {
				t.Fatalf("AnswerSessionID()=%q, want %q", got, tc.want)
			}
		})
	}
}
