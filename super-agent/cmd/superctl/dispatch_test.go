package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"super-agent/internal/boot"
	"super-agent/internal/capability"
	"super-agent/internal/config"
	"super-agent/internal/herdr"
	"super-agent/internal/hifox"
	"super-agent/internal/inflight"
	"super-agent/internal/platform"
)

// impossibleConfig 返回一份「存在但必然校验失败」的配置：校验型用例靠它兜底隔离。
// 不能用不存在的路径——config.Find 对显式缺失路径会继续回退到本机真实配置，守卫
// 一旦回归，用例就会读真实配置并真的去 edit/起 agent。这里让 Load 停在 session 为空。
func impossibleConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "invalid-superctl.toml")
	if err := os.WriteFile(path, []byte("session = \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// usage 里的命令名（行首两个空格 + 小写命令名）。排版微调（对齐空格多少）不该影响
// 这两条对账用例，所以按行解析而不是子串匹配。
var usageCmdRe = regexp.MustCompile(`(?m)^ {2}([a-z][a-z-]*)`)

func usageCommands() map[string]bool {
	names := map[string]bool{}
	for _, m := range usageCmdRe.FindAllStringSubmatch(usage, -1) {
		names[m[1]] = true
	}
	return names
}

// F-1 回归：usage 里列出的每个命令必须注册（reply 曾在集成编辑中从
// switch 丢失，主控回话唯一通道当场断掉）。
func TestUsageCommandsAllRegistered(t *testing.T) {
	names := usageCommands()
	if len(names) < 6 {
		t.Fatalf("usage 解析出 %d 个命令，文案格式可能变了: %v", len(names), names)
	}
	for name := range names {
		if name == "help" {
			continue // help 在 main 里特判，不入命令表
		}
		if _, ok := commands[name]; !ok {
			t.Errorf("usage 列出 %q 但 commands 未注册", name)
		}
	}
}

func TestReplyRegistered(t *testing.T) {
	if _, ok := commands["reply"]; !ok {
		t.Fatal("reply 必须注册：主控回用户的唯一通道")
	}
}

// 注册表里的命令都应在 usage 有说明（防幽灵命令）。
func TestRegisteredCommandsDocumented(t *testing.T) {
	names := usageCommands()
	for name := range commands {
		if !names[name] {
			t.Errorf("命令 %q 已注册但 usage 未列出", name)
		}
	}
}

func TestCommandsArentNil(t *testing.T) {
	for name, run := range commands {
		if run == nil {
			t.Errorf("命令 %q 的处理函数为 nil", name)
		}
	}
}

// 冒烟：真实的 reply 命令行应走出参数解析（而非 unknown command）。
func TestReplySmoke(t *testing.T) {
	dir := t.TempDir()
	cfg := "session = \"t\"\nstate_dir = \"" + dir + "\"\nrepo_dir = \"/repo\"\n"
	path := dir + "/superctl.toml"
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SONGJIANG_CONFIG", path)
	run := commands["reply"]
	if err := run([]string{"--chat", "oc_1", "--text", "hi"}); err != nil {
		t.Fatalf("reply 冒烟失败: %v", err)
	}
	data, err := os.ReadFile(dir + "/outbox.jsonl")
	if err != nil || !strings.Contains(string(data), "oc_1") {
		t.Fatalf("outbox 未落盘: %v %s", err, data)
	}
}

// 派活通道必须恰好一个：两个都不给或两个都给都当场拒绝，不猜。登记在途清单改由派活员
// 自己跑 `dispatch --platform hifox` 完成，所以外面的 track 登记面已下线。
func TestDispatchRequiresExactlyOneChannel(t *testing.T) {
	if err := cmdDispatch([]string{"--agent", "worker", "--body", "b"}); err == nil || !strings.Contains(err.Error(), "恰好一个 --platform") {
		t.Fatalf("两个通道都不给应报错: %v", err)
	}
	if err := cmdDispatch([]string{"--platform", "hifox", "--local", "--agent", "worker", "--body", "b"}); err == nil || !strings.Contains(err.Error(), "恰好一个 --platform") {
		t.Fatalf("两个通道都给应报错: %v", err)
	}
}

// --cloud 已下线：它曾把「云端」当 hifox 的同义词，现在派活必须点名平台。
// 报错要带 --cloud（不然用户只知道「参数不对」，不知道是哪个参数）。
func TestDispatchRejectsRemovedCloudFlag(t *testing.T) {
	err := cmdDispatch([]string{"--cloud", "--agent", "worker", "--title", "t"})
	if err == nil {
		t.Fatal("--cloud 已下线，不该再被接受")
	}
	if !strings.Contains(err.Error(), "--cloud") {
		t.Fatalf("报错应点名 --cloud: %v", err)
	}
}

// 未知平台如实拒绝：不拿 hifox 的 CLI 去猜别的平台怎么派活。
func TestDispatchPlatformUnknownPlatform(t *testing.T) {
	_, err := dispatchPlatform(context.Background(), config.Config{}, platformReq{Platform: "sharkly", Title: "t", AgentKey: "loki"}, nil)
	if err == nil || !strings.Contains(err.Error(), `平台 "sharkly" 还没有派活实现`) {
		t.Fatalf("未知平台必须拒绝: %v", err)
	}
}

// dispatchLocal 的首条 prompt 必须带上 agent 手册：role=job 的 agent 没有别的途径拿手册
// （换脑那条路只给 master/retro），只喂正文等于让它空手干活。
func TestLocalPromptInjectsManual(t *testing.T) {
	dir := t.TempDir()
	manual := filepath.Join(dir, "instructions.md")
	if err := os.WriteFile(manual, []byte("工人手册正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := localPrompt(capability.Agent{Key: "worker", Instructions: manual}, "worker-120000", "跑一遍", nil, "/results/x.md", "/progress/x.md", "/opt/bin/superctl")
	for _, want := range []string{
		"[你的手册]", "工人手册正文\n\n[本轮任务]", "跑一遍", "/results/x.md", "只回文件路径",
		"[回报通道]", "你的名字：worker-120000", "/opt/bin/superctl report --agent worker-120000",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt 缺 %q:\n%s", want, got)
		}
	}
}

// 没有稳定的 superctl 路径时 dispatch 必须在起 pane 之前失败：回报通道的命令
// 不能是一条必失败的死路径。
func TestDispatchLocalRequiresRuntimeCmd(t *testing.T) {
	repo := testRepo(t)
	if err := os.Remove(filepath.Join(repo, "super-agent", "bin", "superctl")); err != nil {
		t.Fatal(err)
	}
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	_, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "worker", "跑一遍", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "找不到稳定的 superctl 路径") {
		t.Fatalf("缺运行时路径必须失败: %v", err)
	}
	if len(f.started) != 0 {
		t.Fatalf("失败时不该起 agent: %v", f.started)
	}
}

// 手册读不到不该让派活失败：退回只给任务正文（保持原样）。
func TestLocalPromptFallsBackWithoutManual(t *testing.T) {
	got := localPrompt(capability.Agent{Key: "worker", Instructions: filepath.Join(t.TempDir(), "nope.md")}, "worker-120000", "跑一遍", nil, "/results/x.md", "/progress/x.md", "/opt/bin/superctl")
	if strings.Contains(got, "[你的手册]") {
		t.Fatalf("没有手册就不该写手册段:\n%s", got)
	}
	if !strings.Contains(got, "跑一遍") || !strings.Contains(got, "/results/x.md") {
		t.Fatalf("应退回任务正文 + 结果路径:\n%s", got)
	}
}

// 附件必须单列成段：承接的派活员要把它原样带上平台，混在正文里靠自觉一定会漏
// （HF-16095：图片丢在透传上）。正文由派活员自己写，prompt 里不再有正文文件段。
func TestLocalPromptCarriesBodyManualAndAttachments(t *testing.T) {
	got := localPrompt(capability.Agent{Key: "zhugui"}, "zhugui-120000", "信封正文",
		[]string{"/state/media/om_1/img_a.jpg", "/state/media/om_1/img_b.png"}, "/state/results/x.md", "/state/progress/x.md", "/opt/bin/superctl")
	for _, want := range []string{
		"信封正文",
		"[附件（建平台任务时逐个 --attachment 带上，一个不漏）]\n/state/media/om_1/img_a.jpg\n/state/media/om_1/img_b.png",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt 缺 %q:\n%s", want, got)
		}
	}
}

// 没有附件时不得留下空段：派活员读到空标题会来问，白耗一轮。
func TestLocalPromptOmitsEmptyExtras(t *testing.T) {
	got := localPrompt(capability.Agent{Key: "worker"}, "worker-120000", "跑一遍", nil, "/r/x.md", "/p/x.md", "/opt/bin/superctl")
	if strings.Contains(got, "附件（") {
		t.Fatalf("没有附件就不该有附件段:\n%s", got)
	}
}

// 平台派活的正文必须是 --desc-file（给人看的任务描述），不是 --body（派活信封）：
// 信封原文曾原样落成 description（HF-16095），这里把「不接受 --body」钉住。
func TestDispatchPlatformRejectsBody(t *testing.T) {
	// 必须带一份合法 --desc-file：否则「缺正文」守卫也会返回含 --desc-file 的错误，
	// body 守卫被删掉时用例仍然绿——那正是它要钉的 HF-16095 回归。
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := cmdDispatch([]string{"--platform", "hifox", "--agent", "daizong", "--title", "t",
		"--body", "需求原话：…", "--desc-file", desc, "--config", impossibleConfig(t)})
	if err == nil || !strings.Contains(err.Error(), "不接受 --body") {
		t.Fatalf("平台派活带 --body 必须被拒: %v", err)
	}
}

// 没有正文文件的平台派活直接拒绝：宁可少建一张单，也不建一张没写清的单。
func TestDispatchPlatformRequiresDescFile(t *testing.T) {
	err := cmdDispatch([]string{"--platform", "hifox", "--agent", "daizong", "--title", "t", "--config", impossibleConfig(t)})
	if err == nil || !strings.Contains(err.Error(), "--desc-file") {
		t.Fatalf("缺正文文件必须报错: %v", err)
	}
}

// 正文文件不存在 / 是空文件：建单前就报出来，别带着空 description 建单。
func TestResolveDescFile(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDescFile(empty); err == nil || !strings.Contains(err.Error(), "空") {
		t.Fatalf("空正文文件必须报错: %v", err)
	}
	if _, err := resolveDescFile(filepath.Join(dir, "nope.md")); err == nil {
		t.Fatal("缺文件必须报错")
	}
	full := filepath.Join(dir, "full.md")
	if err := os.WriteFile(full, []byte("## 背景\n…\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveDescFile(full)
	if err != nil || got != full {
		t.Fatalf("正文文件应解析成绝对路径: %q %v", got, err)
	}
	if got, err := resolveDescFile(""); err != nil || got != "" {
		t.Fatalf("空参数应原样返回空: %q %v", got, err)
	}
	// 相对路径必须绝对化：承接 agent 在别的 CWD 下干活，相对路径等于找不到文件。
	t.Chdir(dir)
	got, err = resolveDescFile("full.md")
	if err != nil || got != full {
		t.Fatalf("相对路径应绝对化成 %q: %q %v", full, got, err)
	}
}

// 附件路径必须存在且是文件：任务建好了却没带上图，等于派了个瞎活。
func TestResolveAttachments(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "shot.jpg")
	if err := os.WriteFile(file, []byte("jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveAttachments([]string{file})
	if err != nil || len(got) != 1 || got[0] != file {
		t.Fatalf("附件应解析成绝对路径: %v %v", got, err)
	}
	// 相对路径同理：注入 prompt 里必须是绝对路径。
	t.Chdir(dir)
	got, err = resolveAttachments([]string{"shot.jpg"})
	if err != nil || len(got) != 1 || got[0] != file {
		t.Fatalf("相对附件应绝对化成 %q: %v %v", file, got, err)
	}
	if _, err := resolveAttachments([]string{filepath.Join(dir, "nope.jpg")}); err == nil {
		t.Fatal("缺附件必须报错")
	}
	if _, err := resolveAttachments([]string{dir}); err == nil {
		t.Fatal("目录不是附件")
	}
}

// --attachment 可重复：每个路径进一次 args（hifox 的 --attachment 就是可重复的）。
func TestStringListCollectsRepeats(t *testing.T) {
	var l stringList
	for _, v := range []string{"a", "b"} {
		if err := l.Set(v); err != nil {
			t.Fatal(err)
		}
	}
	if len(l) != 2 || l[0] != "a" || l[1] != "b" {
		t.Fatalf("重复参数收集失败: %v", l)
	}
	if err := l.Set("  "); err == nil {
		t.Fatal("空路径必须拒绝")
	}
}

// 建单 argv 对账：正文走 --body-file（原样 markdown）、附件逐个 --attachment，
// 且不带 --body——信封原文落成 description 就是 HF-16095 那个 bug。
func TestDispatchHifoxArgsCarryDescFileAndAttachments(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("## 现象\n…\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(t.TempDir(), "shot.jpg")
	if err := os.WriteFile(img, []byte("jpg"), 0o600); err != nil {
		t.Fatal(err)
	}

	var got []string
	var edited []string
	var editedBody string
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "agent list":
				return `[{"id":"agent-1","name":"戴宗","allowTaskAssignment":true}]`, nil
			case "task create":
				got = args
				return `{"id":"1","identifier":"HF-1","title":"t","url":"u"}`, nil
			case "task view":
				// 平台回的任务附件：一张图（有 preview.url）+ 一个视频（无 preview）。
				return `{"identifier":"HF-1","attachments":[
					{"id":"a1","filename":"01-shot.jpg","mimeType":"image/jpeg","preview":{"type":"image","status":"available","url":"/rest/v1/attachments/a1/preview"}},
					{"id":"a2","filename":"02-repro.mp4","mimeType":"video/mp4","preview":{"type":"none","status":"unavailable","url":null}}]}`, nil
			case "task edit":
				edited = args
				// 正文临时文件在 TaskEdit 返回后即被清掉：在调用当下把内容抓下来。
				if i := indexOf(args, "--body-file"); i >= 0 && i+1 < len(args) {
					if data, rerr := os.ReadFile(args[i+1]); rerr == nil {
						editedBody = string(data)
					}
				}
				return `{"identifier":"HF-1"}`, nil
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	req := platformReq{
		Platform: platform.Hifox, Title: "t", AgentKey: "daizong", Type: "Bug",
		DescFile: desc, Attachments: []string{img}, Note: "一句话备注",
	}
	out, err := dispatchHifox(context.Background(), cfg, clientFor, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["task_id"] != "HF-1" {
		t.Fatalf("任务号应回填: %v", out)
	}
	joined := strings.Join(got, "\x00")
	for _, want := range []string{"--body-file\x00" + desc, "--attachment\x00" + img, "--delegate-agent\x00agent-1", "--type\x00Bug"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv 缺 %q:\n%v", want, got)
		}
	}
	if strings.Contains(joined, "--body\x00") {
		t.Fatalf("建单 argv 不该带 --body:\n%v", got)
	}
	// 图片按任务自身 preview.url 嵌进正文；视频没有 preview，不许嵌成空图。
	if edited == nil {
		t.Fatal("有图片附件时必须回写正文（task edit）")
	}
	body := editedBody
	if !strings.Contains(body, "![01-shot.jpg](/rest/v1/attachments/a1/preview)") {
		t.Fatalf("正文应嵌入任务自身 preview.url:\n%s", body)
	}
	if strings.Contains(body, "02-repro.mp4") {
		t.Fatalf("视频不该被嵌成图片:\n%s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "/preview)") {
		t.Fatalf("嵌图应在文末:\n%s", body)
	}
	// 落盘的正文文件同步回写：主控复查时读到的必须是平台上真正的内容。
	if data, err := os.ReadFile(desc); err != nil || !strings.Contains(string(data), "/rest/v1/attachments/a1/preview") {
		t.Fatalf("正文文件未回写: %v\n%s", err, data)
	}
	// build_notes 是「任务已建成但没完全长成」的唯一信号（ADR 0024），主控读结果
	// 文件时必须看见它：上一条图嵌成功、以及上传 1 张与平台回 2 条的数量偏差。
	notes := out["build_notes"]
	if !strings.Contains(notes, "正文已嵌入 1 张图片") {
		t.Fatalf("build_notes 缺嵌图结果: %q", notes)
	}
	if !strings.Contains(notes, "附件数对不上：上传 1，平台回 2") {
		t.Fatalf("build_notes 缺数量偏差: %q", notes)
	}
	// 归档必须晚于嵌图：主控复查读到的副本要与平台上真正的内容一致（含截图小节）。
	archived, err := os.ReadFile(out["brief"])
	if err != nil {
		t.Fatalf("归档读不到: %v", err)
	}
	if !strings.Contains(string(archived), imageSectionTitle) {
		t.Fatalf("归档应含运行时补的截图小节:\n%s", archived)
	}
	if !strings.Contains(string(archived), "/rest/v1/attachments/a1/preview") {
		t.Fatalf("归档应含嵌图 URL:\n%s", archived)
	}
	if out["attachments"] != "2" {
		t.Fatalf("附件数应回报: %v", out)
	}
	entries, err := inflight.Load(cfg.InflightPath())
	if err != nil || len(entries) != 1 || entries[0].TaskID != "HF-1" {
		t.Fatalf("在途清单应登记任务号: %+v %v", entries, err)
	}
}

// 平台哨兵命令：cloudwatch 曾当 hifox 的同义词，现已改名 platformwatch；
// 旧名不该再出现在注册表或 usage 里（照着旧名敲会得到 unknown command）。
func TestPlatformwatchRegisteredAndCloudwatchGone(t *testing.T) {
	if _, ok := commands["platformwatch"]; !ok {
		t.Fatal("cloudwatch 应改名 platformwatch 并注册")
	}
	if strings.Contains(usage, "cloudwatch") {
		t.Fatal("usage 里不该再出现 cloudwatch")
	}
}

// indexOf 找参数位置（--body-file 这类带值参数取值用）。
func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// 正文里已经出现任务自身 preview.url（主控手写过图片）时不得重复追加：
// --update 重跑同一份正文必须幂等。
func TestEmbedImagePreviewsIsIdempotent(t *testing.T) {
	existing := "/rest/v1/attachments/a1/preview"
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("## 现象\n![自己嵌的]("+existing+")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		calls++
		return `{"identifier":"HF-1"}`, nil
	}}
	task := hifox.Task{Attachments: []hifox.Attachment{{
		Filename: "a.jpg", MimeType: "image/jpeg",
	}}}
	task.Attachments[0].Preview.URL = existing

	n, err := embedImagePreviews(context.Background(), client, "HF-1", desc, task)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || calls != 0 {
		t.Fatalf("已引用的图片不该重复追加（n=%d calls=%d）", n, calls)
	}
}

// 用户给的是已有任务号：只能 edit 那张单，不能新建——「再建一张」永远是错的。
func TestDispatchHifoxUpdateEditsExistingTask(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "amend.md")
	if err := os.WriteFile(desc, []byte("## 补充\n新增线索\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var edited []string
	created := false
	clientFor := func(org string) *hifox.Client {
		if org != "" {
			t.Fatalf("--update 不该钉组织，传了 %q", org)
		}
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "task edit":
				edited = args
				return `{"identifier":"HF-16095","title":"t","url":"u"}`, nil
			case "task view":
				// 补正文时也要按任务现有附件重嵌截图小节。
				return `{"identifier":"HF-16095","attachments":[]}`, nil
			case "task create":
				created = true
				return `{}`, nil
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	req := platformReq{
		Platform: platform.Hifox, Update: "HF-16095", Type: "Bug",
		DescFile: desc, Note: "补充线索",
	}
	out, err := dispatchHifox(context.Background(), cfg, clientFor, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("--update 绝不能新建任务")
	}
	if out["task_id"] != "HF-16095" {
		t.Fatalf("任务号应回填: %v", out)
	}
	joined := strings.Join(edited, "\x00")
	for _, want := range []string{"task\x00edit\x00HF-16095", "--body-file\x00" + desc, "--type\x00Bug"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("edit argv 缺 %q:\n%v", want, edited)
		}
	}
	for _, unwanted := range []string{"--delegate-agent", "--space"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("补描述不该动承接人/空间，出现了 %s:\n%v", unwanted, edited)
		}
	}
}

// --update 与改派/换 Space 互斥：改派会让承接人再开一次执行，不是「补描述」。
func TestDispatchUpdateRejectsRoutingFlags(t *testing.T) {
	desc := filepath.Join(t.TempDir(), "amend.md")
	if err := os.WriteFile(desc, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{
		{"--agent", "daizong"},
		{"--delegate", "戴宗"},
		{"--instance", "staging"},
		{"--space", "HF"},
	} {
		args := append([]string{"--platform", "hifox", "--update", "HF-1", "--desc-file", desc, "--config", impossibleConfig(t)}, extra...)
		if err := cmdDispatch(args); err == nil || !strings.Contains(err.Error(), "--update") {
			t.Fatalf("--update 带 %v 应报错: %v", extra, err)
		}
	}
}

// 本地派活不接受 --desc-file：正文由承接的派活员自己写，主控只转交原始材料。
// 这条守卫是为了不让「主控又悄悄开始写文档」回潮。
func TestDispatchLocalRejectsDescFile(t *testing.T) {
	err := cmdDispatch([]string{"--local", "--agent", "zhugui", "--body", "用户原话：…", "--desc-file", "/tmp/x.md", "--config", impossibleConfig(t)})
	if err == nil || !strings.Contains(err.Error(), "--desc-file") {
		t.Fatalf("本地派活带 --desc-file 应报错: %v", err)
	}
}

// 最终正文归档到 <state>/briefs/<任务号>.md：派活员写的是临时文件，
// 主控复查/追加时要读得到稳定副本。
func TestDispatchHifoxArchivesBrief(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("## 现象\n用户说的事\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "agent list":
				return `[{"id":"agent-1","name":"戴宗","allowTaskAssignment":true}]`, nil
			case "task create":
				return `{"id":"1","identifier":"HF-77","title":"t","url":"u"}`, nil
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	out, err := dispatchHifox(context.Background(), cfg, clientFor,
		platformReq{Platform: platform.Hifox, Title: "t", AgentKey: "daizong", DescFile: desc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cfg.BriefsDir(), "HF-77.md")
	if out["brief"] != want {
		t.Fatalf("应回报归档路径 %s: %v", want, out)
	}
	if data, err := os.ReadFile(want); err != nil || !strings.Contains(string(data), "用户说的事") {
		t.Fatalf("归档内容不对: %v\n%s", err, data)
	}
}

// --update 的任务号来自用户输入：拼进 briefs/<id>.md 之前必须收敛成单层文件名，
// 否则「../../x」会写到 briefs 目录外（实测 filepath.Join 会照做）。
func TestArchiveBriefRejectsPathTraversal(t *testing.T) {
	cfg := testCfg(t, testRepo(t))
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := archiveBrief(cfg, "../../escaped", desc)
	if path == "" {
		t.Fatal("归档应成功")
	}
	if want := cfg.BriefsDir(); filepath.Dir(path) != want {
		t.Fatalf("归档必须落在 %s, got %s", want, path)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfg.BriefsDir()), "escaped.md")); err == nil {
		t.Fatal("写到了 briefs 目录外")
	}
}

// 取任务真值失败：任务已建成、附件也上传了，只是没嵌图——必须回报成 build_notes，
// 不能静默（否则主控以为一切正常，图永远补不上）。
func TestDispatchHifoxNotesOnTaskViewFailure(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("## 现象\n…\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(t.TempDir(), "shot.jpg")
	if err := os.WriteFile(img, []byte("jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "agent list":
				return `[{"id":"agent-1","name":"戴宗","allowTaskAssignment":true}]`, nil
			case "task create":
				return `{"id":"1","identifier":"HF-9","title":"t","url":"u"}`, nil
			case "task view":
				return "", errors.New("平台 502")
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	req := platformReq{Platform: platform.Hifox, Title: "t", AgentKey: "daizong", DescFile: desc, Attachments: []string{img}}
	out, err := dispatchHifox(context.Background(), cfg, clientFor, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out["build_notes"], "取任务真值失败") {
		t.Fatalf("取真值失败必须回报: %v", out)
	}
	// 建成了就得登记，即使没嵌上图。
	if out["task_id"] != "HF-9" {
		t.Fatalf("任务号应回填: %v", out)
	}
}

// 评论里挂的附件不是任务正文的材料，不许被嵌进 description（HF-11745 实测有这种条目）。
func TestEmbedImagePreviewsSkipsCommentAttachments(t *testing.T) {
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("## 现象\n…\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := hifox.Task{Attachments: []hifox.Attachment{
		{ID: "a1", Filename: "评论里的图.png", CommentID: "c-1", MimeType: "image/png", Preview: previewOf("/rest/v1/attachments/a1/preview")},
		{ID: "a2", Filename: "任务自带的图.png", MimeType: "image/png", Preview: previewOf("/rest/v1/attachments/a2/preview")},
	}}
	var edited string
	client := &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
		if i := indexOf(args, "--body-file"); i >= 0 {
			data, err := os.ReadFile(args[i+1])
			if err != nil {
				return "", err
			}
			edited = string(data)
		}
		return `{"identifier":"HF-1"}`, nil
	}}
	n, err := embedImagePreviews(context.Background(), client, "HF-1", desc, task)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("只该嵌任务自身的一张图，got %d", n)
	}
	if strings.Contains(edited, "a1/preview") {
		t.Fatalf("评论附件不该嵌进正文:\n%s", edited)
	}
	if !strings.Contains(edited, "a2/preview") {
		t.Fatalf("任务附件应嵌进正文:\n%s", edited)
	}
}

// previewOf 造一个 available 的图片预览（任务附件的真值形状）。
func previewOf(url string) (p struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	URL    string `json:"url"`
}) {
	p.Type, p.Status, p.URL = "image", "available", url
	return p
}

// --update 只补正文、不带新附件：正文是整体替换，旧单已有的截图小节不能因此
// 消失——update 也要按任务现有附件重嵌一遍（幂等）。
func TestDispatchHifoxUpdateReembedsExistingImages(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "amend.md")
	if err := os.WriteFile(desc, []byte("## 补充\n新增线索\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var editedBody string
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "task edit":
				if i := indexOf(args, "--body-file"); i >= 0 {
					data, err := os.ReadFile(args[i+1])
					if err != nil {
						return "", err
					}
					editedBody = string(data)
				}
				return `{"identifier":"HF-1","title":"t","url":"u"}`, nil
			case "task view":
				return `{"identifier":"HF-1","attachments":[
					{"id":"a1","filename":"旧图.jpg","mimeType":"image/jpeg","preview":{"type":"image","status":"available","url":"/rest/v1/attachments/a1/preview"}}]}`, nil
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	req := platformReq{Platform: platform.Hifox, Update: "HF-1", DescFile: desc}
	out, err := dispatchHifox(context.Background(), cfg, clientFor, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(editedBody, "/rest/v1/attachments/a1/preview") {
		t.Fatalf("update 替换正文时应重嵌原单图片:\n%s", editedBody)
	}
	if !strings.Contains(out["build_notes"], "正文已嵌入 1 张图片") {
		t.Fatalf("应回报嵌图结果: %v", out)
	}
	// 补正文不带新附件：平台回的是历史附件总数，拿它跟「本次上传 0」比会得到一条
	// 永远对不上的假警报，主控据此白补一遍（ADR 0024 明确要避免）。
	if strings.Contains(out["build_notes"], "附件数对不上") {
		t.Fatalf("--update 不该报附件数对不上: %v", out)
	}
	// 归档的是替换后的最终正文（含截图小节）。
	archived, err := os.ReadFile(out["brief"])
	if err != nil || !strings.Contains(string(archived), imageSectionTitle) {
		t.Fatalf("归档应含截图小节: %v\n%s", err, archived)
	}
}

// 本地通道拒绝平台专属参数：静默忽略等于让调用方以为生效了。
func TestDispatchLocalRejectsPlatformFlags(t *testing.T) {
	desc := filepath.Join(t.TempDir(), "x.md")
	if err := os.WriteFile(desc, []byte("正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgArg := impossibleConfig(t)
	for _, args := range [][]string{
		{"--local", "--agent", "worker", "--body", "跑", "--update", "HF-1", "--config", cfgArg},
		{"--local", "--agent", "worker", "--body", "跑", "--type", "Bug", "--config", cfgArg},
		{"--local", "--agent", "worker", "--body", "跑", "--title", "t", "--config", cfgArg},
		{"--local", "--agent", "worker", "--body", "跑", "--priority", "high", "--config", cfgArg},
		{"--local", "--agent", "worker", "--body", "跑", "--sprint", "S1", "--config", cfgArg},
	} {
		if err := cmdDispatch(args); err == nil || !strings.Contains(err.Error(), "本地派活不接受") {
			t.Fatalf("本地派活带 %v 应报错: %v", args, err)
		}
	}
}

// 正文文件是目录（不是普通文件）：建单前就该拒绝，而不是在建单路上更晚地炸。
func TestResolveDescFileRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolveDescFile(dir); err == nil || !strings.Contains(err.Error(), "不是文件") {
		t.Fatalf("目录应被 reject: %v", err)
	}
}

// --update 带新附件：edit 追加附件后按任务全量附件重嵌，且评论附件仍然排除。
func TestDispatchHifoxUpdateWithAttachment(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "amend.md")
	if err := os.WriteFile(desc, []byte("## 补充\n新图见下\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(t.TempDir(), "new.png")
	if err := os.WriteFile(img, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	var editArgs []string
	var editedBody string
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "task edit":
				if i := indexOf(args, "--attachment"); i >= 0 {
					editArgs = args
				}
				if i := indexOf(args, "--body-file"); i >= 0 {
					data, err := os.ReadFile(args[i+1])
					if err != nil {
						return "", err
					}
					editedBody = string(data)
				}
				return `{"identifier":"HF-5","title":"t","url":"u"}`, nil
			case "task view":
				return `{"identifier":"HF-5","attachments":[
					{"id":"a1","filename":"历史图.jpg","mimeType":"image/jpeg","preview":{"type":"image","status":"available","url":"/rest/v1/attachments/a1/preview"}},
					{"id":"a2","filename":"评论图.png","commentId":"c-1","mimeType":"image/png","preview":{"type":"image","status":"available","url":"/rest/v1/attachments/a2/preview"}}]}`, nil
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	req := platformReq{Platform: platform.Hifox, Update: "HF-5", DescFile: desc, Attachments: []string{img}}
	out, err := dispatchHifox(context.Background(), cfg, clientFor, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if indexOf(editArgs, "--attachment") < 0 {
		t.Fatalf("update 必须把新附件带上: %v", editArgs)
	}
	if !strings.Contains(editedBody, "a1/preview") {
		t.Fatalf("历史任务附件应重嵌:\n%s", editedBody)
	}
	if strings.Contains(editedBody, "a2/preview") {
		t.Fatalf("评论附件不该嵌进正文:\n%s", editedBody)
	}
	// 任务级附件计数不含评论那条：夹具里两条都带 preview，只有一条是任务自身的。
	if out["attachments"] != "1" {
		t.Fatalf("attachments 应只数任务级附件: %v", out)
	}
}

// --update 改标题是破坏性副作用：用户拿任务号来「补充一下」，原单标题必须保持不变。
func TestDispatchUpdateRejectsTitle(t *testing.T) {
	desc := filepath.Join(t.TempDir(), "amend.md")
	if err := os.WriteFile(desc, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdDispatch([]string{"--platform", "hifox", "--update", "HF-1", "--title", "新标题", "--desc-file", desc, "--config", impossibleConfig(t)}); err == nil ||
		!strings.Contains(err.Error(), "--update") {
		t.Fatalf("--update 带 --title 应报错: %v", err)
	}
}

// --update：edit 输出缺 title **或** url 时都要补读一次 task view，
// 结果文件的链接不能为空（用户点不开等于没交付）。&& 与 || 只在「只缺一个」时不同。
func TestDispatchHifoxUpdateViewFallback(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "amend.md")
	if err := os.WriteFile(desc, []byte("## 补充\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var edited []string
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "task edit":
				edited = args
				return `{"identifier":"HF-2"}`, nil // 缺 title/url
			case "task view":
				return `{"identifier":"HF-2","title":"真标题","url":"https://app.hifox.com/rayfox/task/HF-2","attachments":[]}`, nil
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	req := platformReq{Platform: platform.Hifox, Update: "HF-2", DescFile: desc}
	out, err := dispatchHifox(context.Background(), cfg, clientFor, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(edited, "\x00"), "--title") {
		t.Fatalf("update 不该改标题: %v", edited)
	}
	if out["title"] != "真标题" || out["url"] != "https://app.hifox.com/rayfox/task/HF-2" {
		t.Fatalf("title/url 应来自 task view（补读条件漏一个字段就会空）: %v", out)
	}
}

// 只缺 url：补读条件若被写成「只在 title 为空时读」，这里的 url 会是空。
func TestDispatchHifoxUpdateFallsBackWhenOnlyURLMissing(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "amend.md")
	if err := os.WriteFile(desc, []byte("## 补充\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "task edit":
				return `{"identifier":"HF-3","title":"更新后标题"}`, nil // 有 title、缺 url
			case "task view":
				return `{"identifier":"HF-3","title":"更新后标题","url":"https://app.hifox.com/rayfox/task/HF-3","attachments":[]}`, nil
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	out, err := dispatchHifox(context.Background(), cfg, clientFor,
		platformReq{Platform: platform.Hifox, Update: "HF-3", DescFile: desc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["url"] != "https://app.hifox.com/rayfox/task/HF-3" {
		t.Fatalf("只缺 url 时也要补读: %v", out)
	}
}

// 嵌图的回写 edit 失败：任务已建成、附件也上传了，只是正文没嵌上图。这条必须
// 进 build_notes——它是主控唯一能知道「图没进正文」的信号。
func TestDispatchHifoxNotesOnEmbedFailure(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("## 现象\n…\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(t.TempDir(), "shot.jpg")
	if err := os.WriteFile(img, []byte("jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "agent list":
				return `[{"id":"agent-1","name":"戴宗","allowTaskAssignment":true}]`, nil
			case "task create":
				return `{"id":"1","identifier":"HF-8","title":"t","url":"u"}`, nil
			case "task view":
				return `{"identifier":"HF-8","attachments":[
					{"id":"a1","filename":"shot.jpg","mimeType":"image/jpeg","preview":{"type":"image","status":"available","url":"/rest/v1/attachments/a1/preview"}}]}`, nil
			case "task edit":
				return "", errors.New("网络中断")
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	req := platformReq{Platform: platform.Hifox, Title: "t", AgentKey: "daizong", DescFile: desc, Attachments: []string{img}}
	out, err := dispatchHifox(context.Background(), cfg, clientFor, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["task_id"] != "HF-8" {
		t.Fatalf("嵌图失败不该丢掉任务号: %v", out)
	}
	if !strings.Contains(out["build_notes"], "正文嵌图失败") {
		t.Fatalf("嵌图失败必须回报: %v", out)
	}
	// 任务已建成，在途清单照样要有它（哨兵才盯得到）。
	entries, err := inflight.Load(cfg.InflightPath())
	if err != nil || len(entries) != 1 || entries[0].TaskID != "HF-8" {
		t.Fatalf("应登记在途清单: %+v %v", entries, err)
	}
}

// --attachment 的 CLI flag 一跳：cmdDispatch 解析出的附件必须原样进两条通道。
// 之前只测了 dispatchLocal/dispatchPlatform 的入参，删掉 flag→req 的赋值仍全绿。
func TestCmdDispatchCarriesAttachmentFromFlag(t *testing.T) {
	shot := filepath.Join(t.TempDir(), "shot.jpg")
	if err := os.WriteFile(shot, []byte("jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	cfgPath := filepath.Join(t.TempDir(), "superctl.toml")
	toml := "session = \"t\"\nstate_dir = \"" + cfg.StateDir + "\"\nrepo_dir = \"" + repo + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	var localAttachments []string
	oldLocal, oldTopo := dispatchLocalFn, ensureTopoFn
	dispatchLocalFn = func(_ context.Context, _ config.Config, _ *herdr.Client, _, _, _ string, attachments, _ []string) (map[string]string, error) {
		localAttachments = attachments
		return map[string]string{}, nil
	}
	ensureTopoFn = func(context.Context, config.Config, *herdr.Client) (*boot.Report, error) { return nil, nil }
	defer func() { dispatchLocalFn, ensureTopoFn = oldLocal, oldTopo }()
	if err := cmdDispatch([]string{"--local", "--agent", "worker", "--body", "跑", "--attachment", shot, "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if len(localAttachments) != 1 || localAttachments[0] != shot {
		t.Fatalf("本地通道的附件没有从 flag 传下来: %v", localAttachments)
	}

	var platformReqSeen platformReq
	oldPlatform := dispatchPlatformFn
	dispatchPlatformFn = func(_ context.Context, _ config.Config, req platformReq, _ []string) (map[string]string, error) {
		platformReqSeen = req
		return map[string]string{}, nil
	}
	defer func() { dispatchPlatformFn = oldPlatform }()
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdDispatch([]string{"--platform", "hifox", "--agent", "daizong", "--title", "t", "--desc-file", desc, "--attachment", shot, "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	if len(platformReqSeen.Attachments) != 1 || platformReqSeen.Attachments[0] != shot {
		t.Fatalf("平台通道的附件没有从 flag 传下来: %v", platformReqSeen.Attachments)
	}
}

// 只缺 title：补读条件若只剩 url，结果与台账会丢标题。
func TestDispatchHifoxUpdateFallsBackWhenOnlyTitleMissing(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "amend.md")
	if err := os.WriteFile(desc, []byte("## 补充\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "task edit":
				return `{"identifier":"HF-4","url":"https://app.hifox.com/rayfox/task/HF-4"}`, nil // 有 url、缺 title
			case "task view":
				return `{"identifier":"HF-4","title":"原任务标题","url":"https://app.hifox.com/rayfox/task/HF-4","attachments":[]}`, nil
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	out, err := dispatchHifox(context.Background(), cfg, clientFor,
		platformReq{Platform: platform.Hifox, Update: "HF-4", DescFile: desc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["title"] != "原任务标题" {
		t.Fatalf("只缺 title 时也要补读: %v", out)
	}
}

// 结果里必须有 space：结果模板要求写「- space：」，而 update 时它来自任务声明。
func TestDispatchHifoxReportsSpace(t *testing.T) {
	repo := testRepo(t)
	cfg := testCfg(t, repo)
	desc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(desc, []byte("## 背景\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clientFor := func(string) *hifox.Client {
		return &hifox.Client{Run: func(_ context.Context, args []string) (string, error) {
			switch strings.Join(args[:2], " ") {
			case "agent list":
				return `[{"id":"agent-1","name":"戴宗","allowTaskAssignment":true}]`, nil
			case "task create":
				return `{"identifier":"HF-6","title":"t","url":"u"}`, nil
			}
			return "", fmt.Errorf("意外的 hifox 调用: %v", args)
		}}
	}
	out, err := dispatchHifox(context.Background(), cfg, clientFor,
		platformReq{Platform: platform.Hifox, Title: "t", AgentKey: "daizong", DescFile: desc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["space"] == "" {
		t.Fatalf("结果应回报 space: %v", out)
	}
}

// 首条 prompt 被 omp 启动窗口吞掉时，dispatch 必须当场失败并收掉空转的 pane：
// 只报「提交成功」会让主控以为已开工，实际整件活没人干（本次 6 分钟事故 110 秒）。
type swallowFake struct {
	name         string
	session      string
	closed       []string
	closeErr     error
	promptErr    error
	inflightPath string
	sawInflight  bool
}

func (f *swallowFake) run(_ context.Context, args []string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "workspace list":
		return `{"result":{"workspaces":[{"workspace_id":"w1","label":"master"},{"workspace_id":"w3","label":"jobs"}]}}`, nil
	case "api snapshot":
		return `{"result":{"snapshot":{"layouts":[{"workspace_id":"w1","panes":[{"pane_id":"w1:p1"}]},{"workspace_id":"w3","panes":[{"pane_id":"w3:p1"}]}]}}}`, nil
	case "agent list":
		if f.name == "" {
			return `{"result":{"agents":[]}}`, nil
		}
		return `{"result":{"agents":[{"agent":"omp","name":"` + f.name + `","agent_status":"idle","pane_id":"w3:p1","agent_session":{"value":"` + f.session + `"}}]}}`, nil
	case "agent start":
		f.name = args[2]
		return `{"result":{"agent":{"agent":"omp","agent_status":"idle"}}}`, nil
	case "agent prompt":
		if f.inflightPath != "" {
			if _, err := os.Stat(f.inflightPath); err == nil {
				f.sawInflight = true
			}
		}
		if f.promptErr != nil {
			return "", f.promptErr
		}
		return `{}`, nil
	case "agent send-keys":
		// 模拟 omp 把粘贴折叠成 chip 后吞掉 Enter：会话文件始终不会出现任务文本。
		return `{"result":{"type":"ok"}}`, nil
	case "pane close":
		if f.closeErr != nil {
			return "", f.closeErr
		}
		f.closed = append(f.closed, args[2])
		return `{"result":{"type":"ok"}}`, nil
	}
	return "", nil
}

func TestDispatchLocalFailsWhenPromptNeverLands(t *testing.T) {
	fastFresh(t) // 复用毫秒级的注入等待
	cfg := testCfg(t, testRepo(t))
	session := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(session, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &swallowFake{session: session, inflightPath: cfg.InflightPath()}
	_, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "worker", "跑一遍", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "注入没有落地") {
		t.Fatalf("prompt 没落地必须报错: %v", err)
	}
	if len(f.closed) != 1 || f.closed[0] != "w3:p1" {
		t.Fatalf("失败后必须收掉 pane: %v", f.closed)
	}
	if !f.sawInflight {
		t.Fatal("注入前必须先在途登记：agent 一开工就可能 report/结束")
	}
	entries, lerr := inflight.Load(cfg.InflightPath())
	if lerr != nil || len(entries) != 0 {
		t.Fatalf("失败的派活不该登记在途清单: %+v %v", entries, lerr)
	}
}

// 启动期卡在审批框：重派会再撞同一框，错误必须让主控先找用户，而不是说「重派一次」。
func TestDispatchLocalBlockedStartupAsksUser(t *testing.T) {
	fastFresh(t)
	cfg := testCfg(t, testRepo(t))
	session := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(session, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &swallowFake{session: session, promptErr: &herdr.CmdError{Code: "agent_blocked", Message: "blocked"}}
	_, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "worker", "跑一遍", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "需要用户先处理") {
		t.Fatalf("启动期 blocked 必须交回用户: %v", err)
	}
	if strings.Contains(err.Error(), "重派一次") {
		t.Fatalf("启动期 blocked 不该建议直接重派: %v", err)
	}
}

// 注入没落地且 pane 也关不掉：错误必须让主控知道还有一个活着的 agent，别直接重派。
func TestDispatchLocalReportsPaneCloseFailure(t *testing.T) {
	fastFresh(t)
	cfg := testCfg(t, testRepo(t))
	session := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(session, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &swallowFake{session: session, closeErr: errors.New("pane busy")}
	_, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "worker", "跑一遍", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "也没关掉") || !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("关 pane 失败必须如实说明: %v", err)
	}
	// 关不掉 pane 时条目必须保留：主控要靠它找到还活着的 agent 并 cancel。
	entries, lerr := inflight.Load(cfg.InflightPath())
	if lerr != nil || len(entries) != 1 {
		t.Fatalf("pane 关不掉时应保留在途条目: %+v %v", entries, lerr)
	}
}

// 共享技能根必须挂到作业 agent 的启动参数上：回报纪律（agent-report）是每个 agent
// 都要有的能力，漏挂就是「技能装了但没人看得到」（与主控那条装配同一段代码）。
func TestDispatchLocalMountsSharedSkills(t *testing.T) {
	repo := testRepo(t)
	writeRepoFile(t, filepath.Join(repo, "super-agent", "capabilities", "skills", "agent-report"), "SKILL.md",
		"---\nname: agent-report\ndescription: 回报主控\n---\n\n# 回报主控\n")
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	if _, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "worker", "跑一遍", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.startArgs) != 1 {
		t.Fatalf("应起一个 agent: %v", f.startArgs)
	}
	var overlay string
	for i, a := range f.startArgs[0] {
		if a == "--config" && i+1 < len(f.startArgs[0]) {
			overlay = f.startArgs[0][i+1]
		}
	}
	if overlay == "" {
		t.Fatalf("作业 agent 启动参数缺技能覆盖: %v", f.startArgs[0])
	}
	data, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatalf("覆盖配置读不到: %v", err)
	}
	if want := filepath.Join(repo, "super-agent", "capabilities", "skills"); !strings.Contains(string(data), want) {
		t.Fatalf("覆盖配置应指向共享技能根 %s:\n%s", want, data)
	}
	if strings.Contains(string(data), "ignoredSkills") {
		t.Fatalf("已声明的共享技能不该被排除:\n%s", data)
	}
}

// 没有声明的共享技能必须挡在发现面外：共享根是仓库里的存放位置，不是「人人都有」。
func TestDispatchLocalFiltersUndeclaredSharedSkills(t *testing.T) {
	repo := testRepo(t)
	// 再放一份共享技能，但不声明给 worker。
	writeRepoFile(t, filepath.Join(repo, "super-agent", "capabilities", "skills", "other-skill"),
		"SKILL.md", "---\nname: other-skill\ndescription: 别的纪律\n---\n\n# 别的\n")
	cfg := testCfg(t, repo)
	f := &dispatchFake{}
	if _, err := dispatchLocal(context.Background(), cfg, &herdr.Client{Run: f.run}, "worker", "跑一遍", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	var overlay string
	for i, a := range f.startArgs[0] {
		if a == "--config" && i+1 < len(f.startArgs[0]) {
			overlay = f.startArgs[0][i+1]
		}
	}
	data, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatalf("覆盖配置读不到: %v", err)
	}
	if !strings.Contains(string(data), "ignoredSkills:\n    - other-skill") {
		t.Fatalf("未声明的共享技能应被排除:\n%s", data)
	}
	if strings.Contains(string(data), "    - agent-report") {
		t.Fatalf("已声明的技能不该被排除:\n%s", data)
	}
}

// 含空格的仓库路径必须被单引号包住，注入的 report 命令才不会被 shell 拆成两截。
func TestShellQuotePath(t *testing.T) {
	cases := map[string]string{
		"/opt/bin/superctl":       "/opt/bin/superctl",
		"/Users/a b/bin/superctl": "'/Users/a b/bin/superctl'",
		"/tmp/it's/superctl":      `'/tmp/it'\''s/superctl'`,
	}
	for in, want := range cases {
		if got := shellQuotePath(in); got != want {
			t.Errorf("shellQuotePath(%q) = %q, want %q", in, got, want)
		}
	}
}
