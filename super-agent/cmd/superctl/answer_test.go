package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"super-agent/internal/hifox"
	"super-agent/internal/platform"
)

// fakePlatform 是回答通道替换掉的 hifox CLI：只认 `task view` 与
// `api post …/messages`。别的子命令（评论、召唤、改任务）一律报错——ADR 0031
// 的核心禁止项必须让越界调用当场炸掉，而不是被静默吞成空输出。
type fakePlatform struct {
	taskJSON string
	taskErr  error
	postJSON string
	postErr  error

	platforms []string
	orgs      []string
	posts     [][]string
}

func (f *fakePlatform) install(t *testing.T) {
	t.Helper()
	orig := answerClient
	answerClient = func(name string) (func(org string) *hifox.Client, error) {
		f.platforms = append(f.platforms, name)
		return func(org string) *hifox.Client {
			f.orgs = append(f.orgs, org)
			return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
				switch strings.Join(args[:2], " ") {
				case "task view":
					return f.taskJSON, f.taskErr
				case "api post":
					f.posts = append(f.posts, append([]string{}, args...))
					return f.postJSON, f.postErr
				}
				return "", fmt.Errorf("回答通道越界调用: %v", args)
			}}
		}, nil
	}
	t.Cleanup(func() { answerClient = orig })
}

// 命令层必须把 --platform 原样交给解析层（不是硬编码 hifox），并把平台回报给调用方。
func TestCmdAnswerPassesPlatformThrough(t *testing.T) {
	f := &fakePlatform{
		taskJSON: `{"identifier":"HF-16187","organizationId":"org-1","chatSessionId":"sess-1","agentWorkingStatus":"waiting_human_reply"}`,
		postJSON: `{"messageId":"m-1","runId":"r-1","queued":false}`,
	}
	f.install(t)
	// 抓 stdout：命令层唯一可观察的出口就是这段 JSON。
	out := captureStdout(t, func() error {
		return cmdAnswer([]string{"--platform", platform.Hifox, "--task", "HF-16187", "--text", "执行"})
	})
	if len(f.platforms) != 1 || f.platforms[0] != platform.Hifox {
		t.Fatalf("cmdAnswer 应把 --platform 传给解析层: %v", f.platforms)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("输出不是 JSON: %v\n%s", err, out)
	}
	if got["platform"] != platform.Hifox || got["session_id"] != "sess-1" || got["message_id"] != "m-1" {
		t.Fatalf("输出字段不符: %v", got)
	}
}

// 未实现的平台在命令层就停住：解析层拿到的名字还是调用方给的那个，且不触达 CLI。
func TestCmdAnswerRefusesUnimplementedPlatform(t *testing.T) {
	err := cmdAnswer([]string{"--platform", "sharkly", "--task", "HF-1", "--text", "x"})
	if err == nil || !strings.Contains(err.Error(), "还没有回答实现") {
		t.Fatalf("未知平台应如实拒绝: %v", err)
	}
}

// 回答投进任务对话：人类消息接口、正文原样、落任务自己的组织；评论与
// agent-messages 都不可能出现在调用里（fakePlatform 会让它们报错）。
func TestAnswerPostsIntoTaskConversation(t *testing.T) {
	f := &fakePlatform{
		taskJSON: `{"identifier":"HF-16187","organizationId":"org-1","chatSessionId":"sess-1","agentWorkingStatus":"waiting_human_reply"}`,
		postJSON: `{"messageId":"m-1","runId":"r-1","queued":false}`,
	}
	f.install(t)
	clientFor, err := answerClient(platform.Hifox)
	if err != nil {
		t.Fatal(err)
	}
	text := "1. 跟 web 完全对齐\n2. develop 吧\n4. 为什么要降级？是实现不了吗？"
	out, err := answerTask(context.Background(), clientFor, "HF-16187", text)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.posts) != 1 {
		t.Fatalf("应当只发一次消息，实际 %d 次: %v", len(f.posts), f.posts)
	}
	got := f.posts[0]
	joined := strings.Join(got, "\x00")
	if !strings.Contains(joined, "api\x00post\x00/rest/v1/chat/sessions/sess-1/messages") {
		t.Fatalf("必须打人类消息接口（不是 agent-messages / 评论）: %v", got)
	}
	i := indexOf(got, "--data")
	if i < 0 || i+1 >= len(got) {
		t.Fatalf("缺 --data: %v", got)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(got[i+1]), &payload); err != nil {
		t.Fatalf("--data 不是合法 JSON: %v", err)
	}
	if payload["content"] != text {
		t.Fatalf("回答未原样投递:\nwant=%q\ngot=%q", text, payload["content"])
	}
	// 投递必须钉任务自己的组织（不是 CLI 当前组织）：跨组织任务也要落到正确组织。
	if len(f.orgs) == 0 || f.orgs[len(f.orgs)-1] != "org-1" {
		t.Fatalf("投递组织应为任务自己的组织: %v", f.orgs)
	}
	if out["session_id"] != "sess-1" || out["message_id"] != "m-1" ||
		out["run_id"] != "r-1" || out["queued"] != false || out["task_id"] != "HF-16187" {
		t.Fatalf("投递结果不符: %v", out)
	}
	if out["agent_working_status"] != "waiting_human_reply" {
		t.Fatalf("应回报投递前状态: %v", out)
	}
}

// 多会话任务：回答必须投「活着」的那条会话，而不是最老的 chatSessionId。
// 投错会话不会报警（同样 200），所以只能靠这条断言拦。
func TestAnswerTargetsLiveSessionNotOldest(t *testing.T) {
	f := &fakePlatform{
		taskJSON: `{"identifier":"HF-1","organizationId":"org-1","chatSessionId":"old",` +
			`"sessionStatus":{"top":{"state":"waiting_human_reply","sessionId":"asking"}}}`,
		postJSON: `{"messageId":"m-1"}`,
	}
	f.install(t)
	clientFor, err := answerClient(platform.Hifox)
	if err != nil {
		t.Fatal(err)
	}
	out, err := answerTask(context.Background(), clientFor, "HF-1", "执行")
	if err != nil {
		t.Fatal(err)
	}
	if out["session_id"] != "asking" {
		t.Fatalf("应投等回答的会话，实际 %v", out["session_id"])
	}
	joined := strings.Join(f.posts[0], "\x00")
	if !strings.Contains(joined, "/rest/v1/chat/sessions/asking/messages") {
		t.Fatalf("投递路径应为等回答的会话: %v", f.posts[0])
	}
	if strings.Contains(joined, "/sessions/old/") {
		t.Fatalf("不得投最老的会话: %v", f.posts[0])
	}
}

// 平台解析层：只有 hifox 有实现，别的平台名如实拒绝（平台不是 hifox 的同义词）。
func TestAnswerClientRejectsUnimplementedPlatform(t *testing.T) {
	for _, name := range []string{"sharkly", "hifox-ssh", ""} {
		if _, err := answerClient(name); err == nil || !strings.Contains(err.Error(), "还没有回答实现") {
			t.Fatalf("平台 %q 应如实拒绝: %v", name, err)
		}
	}
}

// --platform 是必填：漏传时不能默认成 hifox。
func TestCmdAnswerRequiresPlatform(t *testing.T) {
	err := cmdAnswer([]string{"--task", "HF-1", "--text", "执行"})
	if err == nil || !strings.Contains(err.Error(), "--platform") {
		t.Fatalf("缺 --platform 应报错: %v", err)
	}
}

// 没有对话会话的任务（还没派给 agent）不能「凑合写评论」：当场拒绝并点名。
func TestAnswerRefusesTaskWithoutConversation(t *testing.T) {
	f := &fakePlatform{taskJSON: `{"identifier":"HF-1","organizationId":"org-1"}`}
	f.install(t)
	clientFor, err := answerClient(platform.Hifox)
	if err != nil {
		t.Fatal(err)
	}
	_, err = answerTask(context.Background(), clientFor, "HF-1", "执行")
	if err == nil || !strings.Contains(err.Error(), "任务评论") {
		t.Fatalf("无会话应拒绝并提示不要写评论: %v", err)
	}
	if len(f.posts) != 0 {
		t.Fatalf("无会话不该发任何消息: %v", f.posts)
	}
}

// 取任务真值失败必须原样上抛：吞掉它会被误报成「没有会话」，主控据此走错处置。
func TestAnswerSurfacesTaskViewError(t *testing.T) {
	f := &fakePlatform{taskErr: errors.New("organization not found")}
	f.install(t)
	clientFor, err := answerClient(platform.Hifox)
	if err != nil {
		t.Fatal(err)
	}
	_, err = answerTask(context.Background(), clientFor, "HF-1", "执行")
	if err == nil || !strings.Contains(err.Error(), "organization not found") {
		t.Fatalf("task view 失败应原样上抛: %v", err)
	}
	if strings.Contains(err.Error(), "chatSessionId") {
		t.Fatalf("不该把失败改写成「没有会话」: %v", err)
	}
	if len(f.posts) != 0 {
		t.Fatalf("失败不该发消息: %v", f.posts)
	}
}

func TestAnswerRequiresExactlyOneBody(t *testing.T) {
	if _, err := answerBody("", ""); err == nil {
		t.Fatal("--text 与 --text-file 都不给应报错")
	}
	if _, err := answerBody("a", "/tmp/b"); err == nil {
		t.Fatal("--text 与 --text-file 同时给应报错")
	}
	if body, err := answerBody("  执行\n", ""); err != nil || body != "执行" {
		t.Fatalf("单行回答应去首尾空白: %q %v", body, err)
	}
}

// 多行回答走 --text-file：文件末尾换行不进消息，空文件与读不到的文件当场拒绝。
func TestAnswerBodyFromFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "reply.md")
	content := "1. 跟 web 完全对齐\n2. develop 吧\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := answerBody("", file)
	if err != nil {
		t.Fatal(err)
	}
	if body != strings.TrimSpace(content) {
		t.Fatalf("文件回答应保留多行、去掉首尾空白:\nwant=%q\ngot=%q", strings.TrimSpace(content), body)
	}

	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := answerBody("", empty); err == nil || !strings.Contains(err.Error(), "空") {
		t.Fatalf("空文件必须拒绝: %v", err)
	}
	if _, err := answerBody("", filepath.Join(dir, "nope.md")); err == nil || !strings.Contains(err.Error(), "读回答文件") {
		t.Fatalf("读不到的文件必须报读失败: %v", err)
	}
}

func TestAnswerPostsSurfaceRunnerError(t *testing.T) {
	f := &fakePlatform{
		taskJSON: `{"identifier":"HF-1","organizationId":"org-1","chatSessionId":"sess-1"}`,
		postErr:  errors.New("boom"),
	}
	f.install(t)
	clientFor, err := answerClient(platform.Hifox)
	if err != nil {
		t.Fatal(err)
	}
	_, err = answerTask(context.Background(), clientFor, "HF-1", "执行")
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "sess-1") {
		t.Fatalf("发送失败要带会话与原因: %v", err)
	}
}

// captureStdout 取一段命令的 stdout（命令层用 jsonPrint 输出）。
func captureStdout(t *testing.T, run func() error) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = f
	runErr := run()
	os.Stdout = orig
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("命令失败: %v", runErr)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// 排队语义必须如实透传：queued=true 是「排在承接人当前回合之后」的唯一信号，
// 硬编码成 false 会让主控对用户承诺「立刻生效」。
func TestAnswerReportsQueued(t *testing.T) {
	f := &fakePlatform{
		taskJSON: `{"identifier":"HF-1","organizationId":"org-1","chatSessionId":"sess-1"}`,
		postJSON: `{"messageId":"m-1","runId":"","queued":true}`,
	}
	f.install(t)
	clientFor, err := answerClient(platform.Hifox)
	if err != nil {
		t.Fatal(err)
	}
	out, err := answerTask(context.Background(), clientFor, "HF-1", "执行")
	if err != nil {
		t.Fatal(err)
	}
	if out["queued"] != true {
		t.Fatalf("排队状态未透传: %v", out)
	}
}

// --task 是必填；缺了不能拿空串去查任务。
func TestCmdAnswerRequiresTask(t *testing.T) {
	err := cmdAnswer([]string{"--platform", platform.Hifox, "--text", "执行"})
	if err == nil || !strings.Contains(err.Error(), "--task") {
		t.Fatalf("缺 --task 应报错: %v", err)
	}
}
