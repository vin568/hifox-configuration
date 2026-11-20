package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"super-agent/internal/config"
	feishu "super-agent/internal/feishu"
	"super-agent/internal/outbox"
)

func writeTemp(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReplyImageValidation(t *testing.T) {
	dir := t.TempDir()
	ok := writeTemp(t, dir, "a.png", 16)
	if got, err := validateReplyImages([]string{ok}); err != nil || len(got) != 1 || !filepath.IsAbs(got[0]) {
		t.Fatalf("valid image rejected: %v %v", got, err)
	}
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"不存在", []string{filepath.Join(dir, "nope.png")}, "读不到"},
		{"目录", []string{dir}, "不是文件"},
		{"空文件", []string{writeTemp(t, dir, "empty.png", 0)}, "空文件"},
		{"11MiB", []string{writeTemp(t, dir, "big.png", 11<<20)}, "10MB"},
		{"扩展名", []string{writeTemp(t, dir, "x.txt", 8)}, "格式不支持"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := validateReplyImages(c.paths)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	t.Run("10 张", func(t *testing.T) {
		paths := make([]string, 10)
		for i := range paths {
			paths[i] = writeTemp(t, dir, "n"+string(rune('a'+i))+".png", 8)
		}
		if _, err := validateReplyImages(paths); err == nil || !strings.Contains(err.Error(), "最多") {
			t.Fatalf("err = %v", err)
		}
	})
}

// 参数错要在读配置/建客户端之前返回（不会碰本机真实配置）。
func TestHistoryFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"都缺", nil, "必须且只能给一个"},
		{"两个都给", []string{"--chat", "oc_x", "--message", "om_x"}, "必须且只能给一个"},
		{"limit 0", []string{"--chat", "oc_x", "--limit", "0"}, "--limit 必须大于 0"},
		{"since 0", []string{"--chat", "oc_x", "--since", "0s"}, "--since 必须大于 0"},
		{"message+limit", []string{"--message", "om_x", "--limit", "5"}, "不能与 --limit"},
		{"message+since", []string{"--message", "om_x", "--since", "1h"}, "不能与 --since"},
		{"list+download", []string{"--chat", "oc_x", "--download"}, "--download 只能"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := cmdHistory(c.args)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestRecallFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"无选择器", nil, "必须且只能给一个"},
		{"两个选择器", []string{"--id", "a", "--message", "om_x"}, "必须且只能给一个"},
		{"chat 无 last", []string{"--id", "a", "--chat", "oc_x"}, "--chat 只能"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := cmdRecall(c.args)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// recall --id 的路径：pending / 旧行 / 已撤回 / 230110 按成功处理。
func TestRecallEntryPaths(t *testing.T) {
	api := &fakeRecallAPI{}
	old := newRecaller
	newRecaller = func(config.Config, string, string) (recaller, error) { return api, nil }
	t.Cleanup(func() { newRecaller = old })

	stateDir := t.TempDir()
	corpus := filepath.Join(t.TempDir(), "cfg.toml")
	cfgBody := "session = \"x\"\nstate_dir = \"" + stateDir + "\"\n"
	if err := os.WriteFile(corpus, []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SONGJIANG_FEISHU_APP_ID", "cli_x")
	t.Setenv("SONGJIANG_FEISHU_APP_SECRET", "s")

	ob, err := outbox.Open(filepath.Join(stateDir, "outbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := ob.Append("oc_x", "还没发", nil)
	sent, _ := ob.Append("oc_x", "已发", nil)
	legacy, _ := ob.Append("oc_x", "旧版本", nil)
	done, _ := ob.Append("oc_x", "已撤回", nil)
	_, _, _ = ob.Drain(context.Background(), func(_ context.Context, e outbox.Entry) (string, error) {
		if e.ID == legacy.ID {
			return "", nil // 旧版本投递：无 message_id
		}
		return "om_" + e.ID, nil
	}, 8)
	if _, err := ob.MarkRecalled(done.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	run := func(id string) error { return cmdRecall([]string{"--id", id, "--config", corpus}) }

	if err := run(pending.ID + "-missing"); err == nil || !strings.Contains(err.Error(), "没有 id=") {
		t.Fatalf("missing id err = %v", err)
	}
	if err := run(legacy.ID); err == nil || !strings.Contains(err.Error(), "没有记录飞书 message_id") {
		t.Fatalf("legacy err = %v", err)
	}
	if err := run(done.ID); err != nil {
		t.Fatalf("already recalled must succeed: %v", err)
	}
	if len(api.recalls) != 0 {
		t.Fatalf("already recalled must not call API: %v", api.recalls)
	}
	if err := run(sent.ID); err != nil {
		t.Fatalf("recall sent: %v", err)
	}
	if len(api.recalls) != 1 || api.recalls[0] != "om_"+sent.ID {
		t.Fatalf("recalls = %v", api.recalls)
	}
	if e, err := ob.Get(sent.ID); err != nil || e.RecalledAt == nil {
		t.Fatalf("recalled_at not recorded: %+v %v", e, err)
	}

	// 230110 按成功处理并回填。
	api.err = &feishu.APIError{Op: "feishu recall", Code: 230110, Msg: "deleted", RequestID: "r"}
	again, _ := ob.Append("oc_x", "又一条", nil)
	_, _, _ = ob.Drain(context.Background(), func(_ context.Context, e outbox.Entry) (string, error) {
		return "om_" + e.ID, nil
	}, 8)
	if err := run(again.ID); err != nil {
		t.Fatalf("230110 must be success: %v", err)
	}
	if e, _ := ob.Get(again.ID); e.RecalledAt == nil {
		t.Fatal("230110 must still be marked recalled")
	}
}

// 230009 要给出中文时限说明。
func TestRecallLimitHint(t *testing.T) {
	err := recallErrHint(&feishu.APIError{Op: "feishu recall", Code: 230009, Msg: "expired", RequestID: "r"})
	if err == nil || !strings.Contains(err.Error(), "撤回时限") || !strings.Contains(err.Error(), "230009") {
		t.Fatalf("err = %v", err)
	}
	err = recallErrHint(&feishu.APIError{Op: "feishu recall", Code: 230026, Msg: "x", RequestID: "r"})
	if err == nil || !strings.Contains(err.Error(), "本应用自己发的") {
		t.Fatalf("err = %v", err)
	}
}

type fakeRecallAPI struct {
	recalls []string
	err     error
}

func (f *fakeRecallAPI) Recall(_ context.Context, messageID string) error {
	f.recalls = append(f.recalls, messageID)
	return f.err
}

// tempConfig 写一份指向临时 state 目录的配置；凭证走环境变量。
func tempConfig(t *testing.T) string {
	t.Helper()
	stateDir := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "cfg.toml")
	body := "session = \"x\"\nstate_dir = \"" + stateDir + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SONGJIANG_FEISHU_APP_ID", "cli_x")
	t.Setenv("SONGJIANG_FEISHU_APP_SECRET", "s")
	return cfgPath
}

// flag → HistoryQuery 的映射（historyTextFn 接缝），以及列表/单条的互斥分支。
func TestCmdHistoryFlags(t *testing.T) {
	cfgPath := tempConfig(t)
	var got feishu.HistoryQuery
	old := historyTextFn
	historyTextFn = func(_ context.Context, _ feishu.Config, q feishu.HistoryQuery) (string, error) {
		got = q
		return "ok", nil
	}
	t.Cleanup(func() { historyTextFn = old })

	if err := cmdHistory([]string{"--chat", "oc_x", "--limit", "5", "--since", "2h", "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if got.ChatID != "oc_x" || got.Limit != 5 || got.Since != 2*time.Hour || got.Download {
		t.Fatalf("list query = %+v", got)
	}

	if err := cmdHistory([]string{"--message", "om_1", "--download", "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if got.MessageID != "om_1" || !got.Download || got.Limit != 0 || got.Since != 0 {
		t.Fatalf("message query = %+v", got)
	}
}

// reply 存绝对路径与 images；纯文字条目不落地 images 字段。
func TestReplyStoresAbsoluteImage(t *testing.T) {
	cfgPath := tempConfig(t)
	dir := t.TempDir()
	abs := writeTemp(t, dir, "a.png", 8)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmdReply([]string{"--chat", "oc_x", "--text", "结论", "--image", rel, "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if err := cmdReply([]string{"--chat", "oc_x", "--text", "纯文字", "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(filepath.Dir(cfgPath), "state", "outbox.jsonl"))
	if err == nil {
		t.Fatalf("state 路径不该来自 cfg 同目录: %s", data)
	}
	// state_dir 是 tempConfig 里的独立临时目录，从配置里读回来定位 outbox。
	cfgBody, _ := os.ReadFile(cfgPath)
	stateDir := strings.TrimSpace(strings.Split(strings.Split(string(cfgBody), "state_dir = \"")[1], "\"")[0])
	data, err = os.ReadFile(filepath.Join(stateDir, "outbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("outbox lines = %d", len(lines))
	}
	var withImage, textOnly outbox.Entry
	if err := json.Unmarshal([]byte(lines[0]), &withImage); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &textOnly); err != nil {
		t.Fatal(err)
	}
	if len(withImage.Images) != 1 || !filepath.IsAbs(withImage.Images[0]) || withImage.Images[0] != abs {
		t.Fatalf("images = %v, want [%s]", withImage.Images, abs)
	}
	if len(textOnly.Images) != 0 {
		t.Fatalf("纯文字条目不该有 images: %+v", textOnly)
	}
}

// 带图正文超 30KiB：reply 当场拒绝，不落 outbox。
func TestReplyRejectsLongTextWithImages(t *testing.T) {
	dir := t.TempDir()
	img := writeTemp(t, dir, "a.png", 8)
	err := cmdReply([]string{"--chat", "oc_x", "--text", strings.Repeat("x", (30<<10)+1), "--image", img})
	if err == nil || !strings.Contains(err.Error(), "30KiB") {
		t.Fatalf("err = %v", err)
	}
}

// 三个选择器各自走到对应路径：--last 选最新可撤、--message 按 id 撤、pending 拒绝。
func TestRecallSelectors(t *testing.T) {
	cfgPath := tempConfig(t)
	api := &fakeRecallAPI{}
	old := newRecaller
	newRecaller = func(config.Config, string, string) (recaller, error) { return api, nil }
	t.Cleanup(func() { newRecaller = old })

	cfgBody, _ := os.ReadFile(cfgPath)
	stateDir := strings.TrimSpace(strings.Split(strings.Split(string(cfgBody), "state_dir = \"")[1], "\"")[0])
	ob, err := outbox.Open(filepath.Join(stateDir, "outbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := ob.Append("oc_a", "第一条", nil)
	second, _ := ob.Append("oc_b", "第二条", nil)
	if _, _, err := ob.Drain(context.Background(), func(_ context.Context, e outbox.Entry) (string, error) {
		return "om_" + e.ID, nil
	}, 8); err != nil {
		t.Fatal(err)
	}
	// 投递之后再加一条，保持 pending 状态。
	pending, _ := ob.Append("oc_x", "还没发", nil)

	// --last --chat：选 oc_b 的最新一条。
	if err := cmdRecall([]string{"--last", "--chat", "oc_b", "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if len(api.recalls) != 1 || api.recalls[0] != "om_"+second.ID {
		t.Fatalf("recalls = %v", api.recalls)
	}

	// --message：直接按飞书 id 撤，并回填 outbox。
	if err := cmdRecall([]string{"--message", "om_" + first.ID, "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if len(api.recalls) != 2 || api.recalls[1] != "om_"+first.ID {
		t.Fatalf("recalls = %v", api.recalls)
	}
	if e, _ := ob.Get(first.ID); e.RecalledAt == nil {
		t.Fatal("--message 应回填 recalled_at")
	}

	// pending：不发请求，报可读错误。
	if err := cmdRecall([]string{"--id", pending.ID, "--config", cfgPath}); err == nil || !strings.Contains(err.Error(), "还没发出去") {
		t.Fatalf("pending err = %v", err)
	}
	if len(api.recalls) != 2 {
		t.Fatalf("pending 不该发请求: %v", api.recalls)
	}
}

// 放宽之后 --limit 200 / --since 720h 必须能通过参数校验：卡在校验层之后的
// 「读配置/建客户端」错误说明这两个 flag 没被自己拒绝，不能因为机器没配凭证而误判。
func TestHistoryFlagsAcceptLargeValues(t *testing.T) {
	old := historyTextFn
	t.Cleanup(func() { historyTextFn = old })
	var got feishu.HistoryQuery
	historyTextFn = func(_ context.Context, _ feishu.Config, q feishu.HistoryQuery) (string, error) {
		got = q
		return "ok", nil
	}
	t.Setenv("SONGJIANG_FEISHU_APP_ID", "cli_test")
	t.Setenv("SONGJIANG_FEISHU_APP_SECRET", "secret_test")
	cfgPath := filepath.Join(t.TempDir(), "superctl.toml")
	if err := os.WriteFile(cfgPath, []byte("state_dir = \""+t.TempDir()+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdHistory([]string{"--config", cfgPath, "--chat", "oc_x", "--limit", "200", "--since", "720h"}); err != nil {
		t.Fatalf("大 limit / 长窗口应被接受: %v", err)
	}
	if got.Limit != 200 || got.Since != 720*time.Hour {
		t.Fatalf("参数没透传到历史查询: %+v", got)
	}
}
