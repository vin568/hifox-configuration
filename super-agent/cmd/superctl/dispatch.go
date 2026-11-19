package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"super-agent/internal/boot"
	"super-agent/internal/capability"
	"super-agent/internal/config"
	"super-agent/internal/herdr"
	"super-agent/internal/hifox"
	"super-agent/internal/inflight"
	"super-agent/internal/platform"
)

// ensureTopoFn 是幂等引导的测试接缝：本地派发必须先补拓扑，这条前置不能被静默跳过。
var ensureTopoFn = ensureTopo

// dispatchLocalFn / dispatchPlatformFn 是两条通道的测试接缝：flag 解析 → platformReq
// 的接线（尤其 --attachment）只在这一层可观测，抽成变量才能整条链测到。
var (
	dispatchLocalFn    = dispatchLocal
	dispatchPlatformFn = dispatchPlatform
)

// platformReq 是一次平台派活的输入：字段多，收成类型而不是一串并列 string 参数。
// Platform 是平台 CLI 应用实例名（目前只有 hifox）。
type platformReq struct {
	Platform, Space, Title, AgentKey, Body, Note string
	Priority, Sprint                             string
	Type                                         string // 平台任务类型（如 Bug/需求）：平台有默认值，标错类型要靠人发现
	Instance, Delegate                           string // Instance: prod|staging；Delegate: 显式平台 ref（跳过解析）
	// DescFile 是平台任务正文的 markdown 文件（由派活员写）：建单时成为任务
	// description。本地通道不收它——主控只转发原始材料，不产出文档。
	DescFile string
	// Attachments 是随任务走的本地文件（图片等）；平台建单时上传，本地派活时列进 prompt。
	Attachments []string
	// Update 是已有任务号：给了就 edit 那张单（补充需求），不再新建——用户拿任务号来
	// 的时候，「再建一张」永远是错的。
	Update string
}

// cmdDispatch 是派活的唯一入口（ADR 0021）：两条通道都在这里登记在途清单、
// 起台账骨架，主控不必再记「派完记得写哪」。
func cmdDispatch(args []string) error {
	fs := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	local := fs.Bool("local", false, "派本地活（按能力注册表起 jobs agent）")
	req := platformReq{}
	fs.StringVar(&req.Platform, "platform", "", "派平台活：平台 CLI 应用实例名（目前只支持 hifox）")
	fs.StringVar(&req.Space, "space", "", "平台：Space（名/标识）")
	fs.StringVar(&req.Title, "title", "", "平台：任务标题")
	fs.StringVar(&req.AgentKey, "agent", "", "能力注册表的 key（平台解析成平台 ref；本地按配方起 agent）")
	fs.StringVar(&req.Body, "body", "", "本地：任务正文（进注入文本；平台任务的正文用 --desc-file，不走这里）")
	fs.StringVar(&req.Note, "note", "", "一句话备注（写进在途清单与台账骨架）")
	fs.StringVar(&req.DescFile, "desc-file", "", "平台：任务正文 markdown 文件（成为 description；本地派活不接受——正文由承接的派活员自己写）")
	var attachments stringList
	fs.Var(&attachments, "attachment", "附件路径（可重复）：平台建单时随任务上传，本地派活时列进任务说明")
	fs.StringVar(&req.Type, "type", "", "平台：任务类型（Bug/需求/任务；Space 内类型名，缺失即用平台默认）")
	fs.StringVar(&req.Update, "update", "", "平台：已有任务号（如 HF-16095）：补充正文/附件/类型/优先级/迭代，不新建、不改标题与承接人")
	fs.StringVar(&req.Priority, "priority", "", "平台：优先级")
	fs.StringVar(&req.Sprint, "sprint", "", "平台：迭代")
	fs.StringVar(&req.Instance, "instance", "", "平台：平台实例后缀（prod = 无后缀那一份；staging），歧义时必填")
	fs.StringVar(&req.Delegate, "delegate", "", "平台：显式平台 agent ref（名字或 ID），跳过注册表解析")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		// flag 只会说「未定义」，不说为什么；--cloud 是刚下线的旧名，点明它免得
		// 使用者以为只是拼错。
		if strings.Contains(err.Error(), "cloud") {
			return fmt.Errorf("%w（--cloud 已下线：派活请明确写 --platform hifox）", err)
		}
		return err
	}
	if *local == (req.Platform != "") {
		return errors.New("dispatch 需要恰好一个 --platform <平台名> 或 --local")
	}
	attachments, err := resolveAttachments(attachments)
	if err != nil {
		return err
	}
	req.Attachments = attachments
	// 通道各自的必填项先判：参数错了不该先跑一遍配置加载与目录创建。
	if req.Platform != "" {
		// 平台任务的正文是给人看的 description，不是派活信封：信封（需求原话/
		// 验收要点）得由主控先写成正文文件。少写正文就当场拒绝，别把信封原文
		// 灌成 description（HF-16095 的教训）。
		if req.Body != "" {
			return errors.New("平台派活不接受 --body：任务正文请用 --desc-file <md 文件>（信封只给本地派活员读，不进平台）")
		}
		if req.DescFile, err = resolveDescFile(req.DescFile); err != nil {
			return err
		}
		if req.DescFile == "" {
			return errors.New("平台派活需要 --desc-file <md 文件>（承接人读的正文）；没有正文就是没写成任务，不建单")
		}
		if req.Update != "" {
			// 补充已有任务：不改委派、空间与标题（改派=再触发一次执行；改标题会让
			// 用户认不出原单——「补充一下」不该有这两个副作用）。
			if req.AgentKey != "" || req.Instance != "" || req.Delegate != "" || req.Space != "" || req.Title != "" {
				return errors.New("--update 只补正文/附件/类型/优先级/迭代；不改标题/承接人/Space（改标题请在平台任务上直接改，改派请让主控另派）")
			}
		} else if req.Title == "" || req.AgentKey == "" {
			return errors.New("平台派活需要 --title 与 --agent")
		}
	} else {
		// 平台专属参数在本地通道上没有含义：静默忽略等于让调用方以为生效了。
		if req.Update != "" || req.Type != "" || req.Title != "" || req.Priority != "" || req.Sprint != "" {
			return errors.New("本地派活不接受 --update / --type / --title / --priority / --sprint（这些只对平台任务有意义；备注用 --note）")
		}
		// 正文由承接的派活员从你给的原始材料里写：主控不产出文档，也不该替它准备文件。
		if req.DescFile != "" {
			return errors.New("本地派活不接受 --desc-file：把用户原话与附件交给承接 agent，正文由它写")
		}
		if req.AgentKey == "" || req.Body == "" {
			return errors.New("本地派活需要 --agent 与 --body")
		}
	}
	cfg, _, err := loadConfigWithAgents([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	if req.Platform != "" {
		out, err := dispatchPlatformFn(ctx, cfg, req, fs.Args())
		if err != nil {
			return err
		}
		return jsonPrint(out)
	}
	// 本地派活要 pane：先幂等引导，否则 cancel 关掉 jobs 最后一个 pane 后这里会报
	// 「workspace jobs 不存在」——那一步该由运行时自己补，不该让主控撞上。
	if _, err := ensureTopoFn(ctx, cfg, hc); err != nil {
		return err
	}
	out, err := dispatchLocalFn(ctx, cfg, hc, req.AgentKey, req.Body, req.Note, req.Attachments, fs.Args())
	if err != nil {
		return err
	}
	return jsonPrint(out)
}

// stringList 是可重复的字符串参数（--attachment 一次带一个路径）。
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("附件路径不能为空")
	}
	*s = append(*s, v)
	return nil
}

// resolveAttachments 把附件路径校验成绝对路径：附件是派活的一部分，缺文件要在
// 建单前就报出来（平台任务建好了却没带上图，等于派了个瞎活）。
func resolveAttachments(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, _, err := resolveRegularFile(p, "附件")
		if err != nil {
			return nil, err
		}
		out = append(out, abs)
	}
	return out, nil
}

// resolveDescFile 校验正文文件存在且非空，返回绝对路径。
func resolveDescFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	abs, st, err := resolveRegularFile(path, "正文文件")
	if err != nil {
		return "", err
	}
	if st.Size() == 0 {
		return "", fmt.Errorf("正文文件 %s 是空的：没有正文就是没写成任务", path)
	}
	return abs, nil
}

// resolveRegularFile 是附件与正文文件共用的输入校验：绝对路径 + 必须存在 +
// 必须是普通文件（目录/设备文件传给 CLI 只会在建单路上更晚地炸）。
func resolveRegularFile(path, what string) (string, os.FileInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, fmt.Errorf("%s %q: %w", what, path, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", nil, fmt.Errorf("%s %s 读不到: %w", what, path, err)
	}
	if !st.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s %s 不是文件", what, path)
	}
	return abs, st, nil
}

// dispatchPlatform 按平台分派：平台是 CLI 应用实例，每个实例的派活实现各一份。
// 目前只有 hifox；别的平台名（将来 sharkly）如实拒绝，而不是悄悄拿 hifox 去派。
func dispatchPlatform(ctx context.Context, cfg config.Config, req platformReq, extra []string) (map[string]string, error) {
	switch req.Platform {
	case platform.Hifox:
		return dispatchHifox(ctx, cfg, hifox.NewForOrg, req, extra)
	default:
		return nil, fmt.Errorf("平台 %q 还没有派活实现", req.Platform)
	}
}

// dispatchHifox 包 `hifox task create`（或 --update 时 `<id> task edit`）：委派即触发；
// 任务号回填在途清单与台账骨架。平台 ref 由能力名解析（平台名字带实例后缀，注册表 key
// 不是平台 ref）。
func dispatchHifox(ctx context.Context, cfg config.Config, clientFor func(org string) *hifox.Client, req platformReq, extra []string) (map[string]string, error) {
	reg, err := capability.Load(cfg.RepoDir)
	if err != nil {
		return nil, err
	}
	var (
		client   *hifox.Client
		delegate = req.Delegate
		space    = req.Space
	)
	if req.Update != "" {
		// 补充已有任务：任务号全局可解析，不钉组织；也不改承接人与 Space——改派会让
		// 承接人再开一次执行，「补描述」不该有这个副作用。
		client = clientFor("")
	} else {
		entry, ok := reg.PlatformByKey(req.Platform, req.AgentKey)
		if !ok {
			return nil, fmt.Errorf("能力注册表里没有平台 %s 的能力 %q（检查 super-agent/capabilities/platforms/%s/）", req.Platform, req.AgentKey, req.Platform)
		}
		// 解析 ref 与建任务必须在同一个组织下：agent list 只列当前组织。
		client = clientFor(entry.Org)
		if delegate == "" {
			agents, err := client.AgentList(ctx)
			if err != nil {
				return nil, err
			}
			ref, rerr := hifox.ResolveDelegate(agents, entry.Name, req.Instance)
			if rerr != nil {
				return nil, rerr
			}
			if ref.ID != "" {
				delegate = ref.ID
			} else {
				delegate = ref.Name
			}
		}
		// --space 缺省时用声明里的 space：注册表已经知道这能力在哪个 Space，
		// 不该让主控每次现拼（拼错就是歧义或派到别的 Space）。
		if space == "" {
			space = entry.Space
		}
	}

	args := make([]string, 0, 10)
	if req.Update == "" {
		if space != "" {
			args = append(args, "--space", space)
		}
		args = append(args, "--title", req.Title, "--delegate-agent", delegate)
	}
	// 任务类型必须显式给：平台默认值不是「猜」出来的结果——HF-16095 标题写了 [bug]，
	// 类型却是「需求」，承接人打开就看到一个标错的单。
	if req.Type != "" {
		args = append(args, "--type", req.Type)
	}
	// 正文用 --body-file 而不是 --body：markdown 原文进 description，不过 \n 转义、
	// 不经过任何「重组」。信封（需求原话/验收要点）在主控→派活员那一层就已经用完。
	args = append(args, "--body-file", req.DescFile)
	for _, p := range req.Attachments {
		args = append(args, "--attachment", p)
	}
	if req.Priority != "" {
		args = append(args, "--priority", req.Priority)
	}
	if req.Sprint != "" {
		args = append(args, "--sprint", req.Sprint)
	}
	args = append(args, extra...)

	var (
		id    string
		title string
		url   string
	)
	if req.Update != "" {
		task, err := client.TaskEdit(ctx, req.Update, args)
		if err != nil {
			return nil, err
		}
		id, title, url = req.Update, task.Title, task.URL
		// edit 的输出形状以 CLI 为准：缺 title/url 时补读一次真值，别让结果文件里
		// 的链接为空（用户拿链接点不开等于没交付）。
		if title == "" || url == "" {
			if view, verr := client.TaskView(ctx, req.Update); verr == nil {
				title, url = orDefault(title, view.Title), orDefault(url, view.URL)
			}
		}
	} else {
		ref, err := client.TaskCreate(ctx, args)
		if err != nil {
			return nil, err
		}
		id = ref.Identifier
		if id == "" {
			id = ref.ID
		}
		title, url = ref.Title, ref.URL
	}
	if id == "" {
		return nil, errors.New("hifox task create 没回任务号，无法登记在途清单")
	}
	// 建单后补一步（失败只回报，不重试建单——重试建单会开出第二张单）：图片用任务
	// 自身的 preview.url 嵌进正文。附件区缩略图读不清复现细节，正文里嵌原尺寸图才看
	// 得出「滚动被刷到第一页」这种界面问题。
	//
	// 不订阅发起人：任务由 CLI 的成员身份创建，平台已按 reason=creator 自动订阅
	// （HF-16095 实测），飞书 sender 是 open_id、换不出平台成员身份，硬映射只会猜错人。
	var notes []string
	attachments := 0
	// --update 也要走这一段：正文是整体替换，若跳过嵌图，原单已有的「截图」小节
	// 会随旧正文一起消失（只补正文、不带新附件时必然踩到）。
	if len(req.Attachments) > 0 || req.Update != "" {
		task, terr := client.TaskView(ctx, id)
		if terr != nil {
			notes = append(notes, "取任务真值失败，正文未嵌图（任务已建成，附件不受影响）: "+terr.Error())
		} else {
			// 只数任务本身的附件：评论里挂的不算（--update 时拿含评论的总数去比，
			// 只会得到一条永远对不上的假警报）。
			for _, a := range task.Attachments {
				if a.CommentID == "" {
					attachments++
				}
			}
			embedded, eerr := embedImagePreviews(ctx, client, id, req.DescFile, task)
			switch {
			case eerr != nil:
				notes = append(notes, "正文嵌图失败: "+eerr.Error())
			case embedded > 0:
				notes = append(notes, fmt.Sprintf("正文已嵌入 %d 张图片（任务自身 preview.url）", embedded))
			}
			// 数量核对只对新建有意义：--update 时平台回的是历史附件 + 本次追加，
			// 拿它跟本次上传数比必然「对不上」，那种假警报会让主控白补一遍。
			if req.Update == "" && attachments != len(req.Attachments) {
				notes = append(notes, fmt.Sprintf("附件数对不上：上传 %d，平台回 %d", len(req.Attachments), attachments))
			}
		}
	}

	// 最终正文归档：派活员写的是临时文件，平台正文是唯一真源；主控复查、追加、
	// 改派时要能直接读到一个稳定的本地副本（<state>/briefs/<任务号>.md）。
	brief := archiveBrief(cfg, id, req.DescFile)

	note := req.Note
	if note == "" {
		note = req.Title
	}
	if note == "" {
		note = title
	}
	e := inflight.Entry{Kind: inflight.KindPlatform, Platform: req.Platform, TaskID: id, Note: note, AddedAt: time.Now()}
	if err := inflight.Add(cfg.InflightPath(), e); err != nil {
		return nil, fmt.Errorf("任务已派出（%s）但登记在途清单失败: %w", id, err)
	}
	ledger := ledgerDraft(cfg, id+"-"+orDefault(req.Title, title), "平台", orDefault(delegate, "未改"), note)
	out := map[string]string{
		"task_id": id, "title": title, "url": url, "delegate": delegate, "space": space,
		"ledger": ledger, "attachments": strconv.Itoa(attachments), "type": req.Type, "brief": brief,
	}
	if len(notes) > 0 {
		out["build_notes"] = strings.Join(notes, "；")
	}
	return out, nil
}

// archiveBrief 把任务最终正文（Task Brief）存进归档（<state>/briefs/<任务号>.md）。
// 派活员写的是临时文件，平台侧才是真源；归档给出一个稳定的本地副本供主控复查，
// 也避免「正文文件被 tmp 清理后没人知道当时写了什么」。
func archiveBrief(cfg config.Config, id, descFile string) string {
	body, err := os.ReadFile(descFile)
	if err != nil {
		slog.Warn("superctl: 归档任务正文失败（读正文文件）", "task", id, "error", err)
		return ""
	}
	path := filepath.Join(cfg.BriefsDir(), safeTaskID(id)+".md")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		slog.Warn("superctl: 归档任务正文失败", "task", id, "path", path, "error", err)
		return ""
	}
	return path
}

// embedImagePreviews 把已上传的图片附件以任务自身的 preview.url 嵌进正文，再以
// --body-file 回写任务。视频/文件不嵌：平台不给 preview，正文里嵌空图只会坏版。
//
// 位置固定为文末「截图（按上传顺序）」小节，不猜正文里的语义位置：编号与上传顺序
// 一致，正文的「附件」小节仍由主控写用途说明。正文里已经出现该 preview.url 就不重复
// 追加（--update 重跑同一份正文时保持幂等）。
func embedImagePreviews(ctx context.Context, client *hifox.Client, id, descFile string, task hifox.Task) (int, error) {
	body, err := os.ReadFile(descFile)
	if err != nil {
		return 0, fmt.Errorf("读正文文件: %w", err)
	}
	text := strings.TrimRight(string(body), "\n")
	var additions []string
	for _, a := range task.Attachments {
		// 评论里挂的附件不是任务正文的材料：嵌进 description 会让承接人误以为
		// 那是任务自带的截图（HF-11745 实测有 commentId 非空的一条）。
		if a.CommentID != "" || !a.IsImage() || a.Preview.URL == "" || strings.Contains(text, a.Preview.URL) {
			continue
		}
		// alt 只写文件名：编号由 markdown 的有序呈现与上传顺序决定，别在 alt 里再
		// 拼一遍（派活员写的文件名本身可能已带 01- 前缀，拼了就成 01-01-x.jpg）。
		additions = append(additions, fmt.Sprintf("![%s](%s)", a.Filename, a.Preview.URL))
	}
	if len(additions) == 0 {
		return 0, nil
	}
	if !strings.Contains(text, imageSectionTitle) {
		text += "\n\n" + imageSectionTitle
	}
	text += "\n\n" + strings.Join(additions, "\n\n") + "\n"

	tmp, err := os.CreateTemp(filepath.Dir(descFile), "brief-*.md")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if _, err := client.TaskEdit(ctx, id, []string{"--body-file", tmp.Name()}); err != nil {
		return 0, err
	}
	// 正文文件同步回写：主控之后复查/改派时读到的就是平台上真正的内容。
	if err := os.WriteFile(descFile, []byte(text), 0o644); err != nil {
		return len(additions), fmt.Errorf("正文已回写平台但落盘失败: %w", err)
	}
	return len(additions), nil
}

// imageSectionTitle 是运行时追加截图的固定小节标题（与正文的「附件」清单分开：
// 一个是人写的用途说明，一个是平台生成的原图）。
const imageSectionTitle = "## 截图（按上传顺序）"

// dispatchLocal 按注册表配方起一个 jobs agent，把任务注入进去并登记在途清单。
func dispatchLocal(ctx context.Context, cfg config.Config, hc *herdr.Client, agentKey, body, note string, attachments, extra []string) (map[string]string, error) {
	reg, err := capability.Load(cfg.RepoDir)
	if err != nil {
		return nil, err
	}
	recipe, ok := reg.AgentByKey(agentKey)
	if !ok || recipe.Role == "master" || recipe.Role == "retro" {
		return nil, fmt.Errorf("能力注册表里没有可派活的 agent %q（检查 super-agent/capabilities/agents/）", agentKey)
	}

	entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath())
	if err != nil {
		return nil, err
	}
	idemKey := localIdempotencyKey(recipe.Key, body, attachments)
	for _, e := range inflight.Local(entries) {
		if e.IdempotencyKey == idemKey {
			return nil, fmt.Errorf("%s 已有同一任务的活跃作业（agent %s）；先收口或 cancel，避免重复副作用", recipe.Key, e.Agent)
		}
	}
	limit := recipe.MaxConcurrent
	if limit <= 0 {
		limit = 3
	}
	// 上限按「这个能力」的并发计，不是全局面板：两个能力各有各的额度。
	if active := countActiveLocal(entries, recipe.Key); active >= limit {
		return nil, fmt.Errorf("%s 并发已达上限 %d，等一个收口再派", recipe.Key, limit)
	}

	// 回报通道要一个可执行的 superctl：没有稳定路径就先失败，别把一条必失败的命令
	// 注给 agent。这里在建 pane 之前判，避免留下空转 agent。
	runtimeCmd := runtimeCmdPath(cfg)
	if runtimeCmd == "" {
		return nil, errors.New("找不到稳定的 superctl 路径（先 go build -o super-agent/bin/superctl ./cmd/superctl）：回报通道没有可执行的命令")
	}
	name := fmt.Sprintf("%s-%s", agentKey, time.Now().UTC().Format("150405"))
	resultPath := filepath.Join(cfg.ResultsDir(), name+".md")
	progressPath := filepath.Join(cfg.ProgressDir(), name+".md")
	briefPath := filepath.Join(cfg.BriefsDir(), name+".md")
	if err := writeLocalBrief(cfg, recipe, name, body, attachments, resultPath, progressPath, briefPath); err != nil {
		return nil, fmt.Errorf("写本地作业 brief 失败（未起 agent）: %w", err)
	}
	pane, err := acquirePane(ctx, cfg, hc, recipe.Workspace)
	if err != nil {
		return nil, err
	}
	if note == "" {
		note = noteLine(body)
	}
	if err := startLocal(ctx, cfg, hc, recipe, name, pane, extra); err != nil {
		return nil, err
	}
	// 先登记再注入：注入一旦落地，agent 可能立刻 report 或结束回合；在途清单晚一步
	// 写，这些信号会按「陌生 agent」被静默丢掉。失败时再把条目撤掉。
	e := inflight.Entry{Kind: inflight.KindLocal, Agent: name, Pane: pane, Capability: recipe.Key, Note: note,
		Brief: briefPath, Progress: progressPath, Result: resultPath, IdempotencyKey: idemKey,
		Args: append([]string{}, extra...), AddedAt: time.Now()}
	if err := inflight.Add(cfg.InflightPath(), e); err != nil {
		if cerr := hc.PaneClose(ctx, pane); cerr != nil {
			return nil, fmt.Errorf("起 %s 后登记在途清单失败，pane %s 也没关掉（用 superctl cancel --agent %s 收掉）: %w", name, pane, name, err)
		}
		return nil, fmt.Errorf("起 %s 后登记在途清单失败（已关掉 pane）: %w", name, err)
	}
	prompt := localPrompt(recipe, name, body, attachments, resultPath, progressPath, shellQuotePath(runtimeCmd))
	// 注入必须确认落地：omp 启动窗口会吞掉大段粘贴的 Enter，agent 会空转在输入框上。
	// 只报「提交成功」等于把空 pane 当成已开工——本次 6 分钟事故里 110 秒就丢在这里。
	if _, err := boot.SubmitPrompt(ctx, hc, name, prompt, resultPath); err != nil {
		reason := "重派一次"
		if herdr.IsBlocked(err) {
			// 启动期卡在信任/审批框：重派会再撞同一框，必须交回用户先处理。
			reason = "它卡在要确认的界面上，需要用户先处理，处理完再派"
		}
		if cerr := hc.PaneClose(ctx, pane); cerr != nil {
			// pane 还在：保留在途条目，主控可以用 cancel 找到它。
			return nil, fmt.Errorf("任务已起到 %s 但注入没有落地，pane %s 也没关掉（用 superctl cancel --agent %s 收掉后%s）: %w", name, pane, name, reason, err)
		}
		if _, rerr := inflight.Remove(cfg.InflightPath(), func(e inflight.Entry) bool { return e.Agent == name }); rerr != nil {
			return nil, fmt.Errorf("任务已起到 %s 但注入没有落地；pane 已关掉，但移出在途清单失败（用 cancel 收口）: %w", name, rerr)
		}
		return nil, fmt.Errorf("任务已起到 %s 但注入没有落地（已关掉该 pane，%s）: %w", name, reason, err)
	}
	ledger := ledgerDraft(cfg, name, "本地", name, note)
	e.Ledger = ledger
	if err := inflight.Add(cfg.InflightPath(), e); err != nil {
		// 任务已经在跑，台账元数据写失败不该让派活失败；下一轮 handoff 会退回按 slug 找。
		slog.Warn("superctl: 回填台账路径失败", "agent", name, "error", err)
	}
	return map[string]string{"agent": name, "pane": pane, "result": resultPath, "brief": briefPath, "progress": progressPath, "ledger": ledger}, nil
}

// startLocal 是「按配方起一个 job agent」的共享装配：dispatch 与 handoff 都必须
// 走同一条路径，否则技能与启动参数会分叉。注入必须等登记之后再做（agent 一开工
// 就可能 report/结束回合），所以启动与注入分成两步。
func startLocal(ctx context.Context, cfg config.Config, hc *herdr.Client, recipe capability.Agent, name, pane string, extra []string) error {
	skillOpts, err := skillArgs(cfg, recipe)
	if err != nil {
		return err
	}
	args := append(append(append([]string{}, recipe.Args...), skillOpts...), extra...)
	if err := hc.AgentStart(ctx, name, orDefault(recipe.Kind, "omp"), pane, args); err != nil {
		return fmt.Errorf("起 %s: %w", name, err)
	}
	return nil
}

// localIdempotencyKey 是本地作业的防重复键：同一能力 + 同一正文 + 同一附件组，
// 有活跃条目未收口时拒绝再派。重派要先 cancel，避免两个 agent 对同一件事产生双份副作用。
func localIdempotencyKey(agentKey, body string, attachments []string) string {
	// 正文去首尾空白、附件排序：同一件事换个换行/附件顺序仍是同一件事，不能因为
	// 封装差异再派一个 agent 产生双份副作用。
	atts := append([]string{}, attachments...)
	sort.Strings(atts)
	h := sha256.New()
	h.Write([]byte(agentKey))
	h.Write([]byte{0})
	h.Write([]byte(strings.TrimSpace(body)))
	for _, a := range atts {
		h.Write([]byte{0})
		h.Write([]byte(a))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// writeLocalBrief 在起 agent 前落盘完整任务正文。worker 崩溃或 handoff 时，运行时
// 必须有一份不依赖主控对话记忆的机器真值；写不进去就不派。
func writeLocalBrief(cfg config.Config, recipe capability.Agent, name, body string, attachments []string, resultPath, progressPath, briefPath string) error {
	if err := os.MkdirAll(cfg.BriefsDir(), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# 本地作业 Brief\n\n")
	fmt.Fprintf(&b, "- 能力：%s（%s）\n", recipe.Key, orDefault(recipe.Kind, "omp"))
	fmt.Fprintf(&b, "- agent：%s\n", name)
	fmt.Fprintf(&b, "- 创建：%s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- 结果：%s\n", resultPath)
	fmt.Fprintf(&b, "- 进度：%s\n\n", progressPath)
	b.WriteString("## 任务正文\n\n")
	b.WriteString(strings.TrimSpace(body))
	if len(attachments) > 0 {
		b.WriteString("\n\n## 附件\n\n")
		for _, p := range attachments {
			b.WriteString("- " + p + "\n")
		}
	}
	return os.WriteFile(briefPath, []byte(b.String()), 0o644)
}

// localPrompt 拼注入本地 agent 的首条 prompt：recipe.Instructions 是扫描时解析好的
// 绝对路径，声明了手册就把全文带上——role=job 的 agent 没有别的途径拿手册（换脑那条路
// 只给 master/retro），只喂任务正文等于让它空手干活。手册读不到就退回只给任务正文：
// 派活不该因为一个文件缺失而失败。
//
// 附件单列成一段：承接的派活员要把它们原样带上平台，混在正文里靠自觉一定会漏。
//
// 回报通道（ADR 0029）：agent 知道自己的名字，就能在主控没轮询、平台也没有任务变化
// 的时候主动把卡点与关键结果推回去；完成仍以结果文件 + 运行时的状态事件为准，不要求
// 再发一条收尾报告。
func localPrompt(recipe capability.Agent, name, body string, attachments []string, resultPath, progressPath, runtimeCmd string) string {
	var b strings.Builder
	if manual, err := os.ReadFile(recipe.Instructions); err == nil {
		b.WriteString("[你的手册]\n")
		b.WriteString(strings.TrimRight(string(manual), "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString("[本轮任务]\n")
	b.WriteString(body)
	if len(attachments) > 0 {
		b.WriteString("\n\n[附件（建平台任务时逐个 --attachment 带上，一个不漏）]")
		for _, p := range attachments {
			b.WriteString("\n")
			b.WriteString(p)
		}
	}
	b.WriteString("\n\n[进度文件]\n")
	fmt.Fprintf(&b, "长任务或上下文变大时，把「状态 / 已完成 / 未完成 / 下一步 / 改过的文件 / 验证 / 待决问题 / 可续」写进 %s。\n", progressPath)
	b.WriteString("收到 checkpoint 指令时，先把进度写进这个文件，再结束回合；不要继续任务。")
	b.WriteString("\n\n[回报通道]（纪律见 agent-report 技能；注入只给你名字与命令）")
	fmt.Fprintf(&b, "\n你的名字：%s（主控用这个名字找你）", name)
	fmt.Fprintf(&b, "\n中途要主控知道的事（被卡住、需要裁决、拿到了用户可见的关键结果），运行：\n  %s report --agent %s --text \"<一句话>\"", runtimeCmd, name)
	fmt.Fprintf(&b, "\n主控也可能用 tell 给你补信息；按它说的调整。")
	fmt.Fprintf(&b, "\n完成后把结果写成 markdown 存到 %s，只回文件路径（运行时会发完成事件，不要再用 report 发收尾报告）。", resultPath)
	return b.String()
}

// shellQuotePath 只在路径含 shell 特殊字符时加单引号：正常路径保持可读，含空格的
// 仓库路径也不会让注入的 report 命令断成两截。
func shellQuotePath(p string) string {
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '.', r == '-', r == '_', r == '~':
		default:
			return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		}
	}
	return p
}

// countActiveLocal 数某能力名下还没收口的本地作业。旧条目没有 capability 字段，
// 用 agent 名前缀（<key>-HHMMSS）兜底认领，避免升级后额度突然清零。
func countActiveLocal(entries []inflight.Entry, key string) int {
	n := 0
	for _, e := range inflight.Local(entries) {
		if e.Capability == key || (e.Capability == "" && strings.HasPrefix(e.Agent, key+"-")) {
			n++
		}
	}
	return n
}

// acquirePane 在目标 workspace（默认 jobs）里找一个空闲 pane；根 pane 被占就往下切一个。
// herdr 的 agent start 必须在现成 shell pane 上，所以这里的获得策略是「先占根、再切分」。
func acquirePane(ctx context.Context, cfg config.Config, hc *herdr.Client, workspace string) (string, error) {
	if workspace == "" {
		workspace = "jobs"
	}
	t, err := hc.Topology(ctx)
	if err != nil {
		return "", err
	}
	root := t.RootPane(workspace)
	if root == "" {
		return "", fmt.Errorf("workspace %s 不存在（先 superctl ensure）", workspace)
	}
	if _, busy := t.ByPane[root]; !busy {
		return root, nil
	}
	return hc.WorkspaceCreatePane(ctx, root, "down", cfg.RepoDir)
}

// ledgerDraft 在台账活跃区建骨架（仅当同名文件不存在；之后归主控维护）。
func ledgerDraft(cfg config.Config, slug, channel, who, note string) string {
	name := time.Now().Format("20060102") + "-" + sanitizeSlug(slug) + ".md"
	path := filepath.Join(cfg.ActiveDir(), name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	body := fmt.Sprintf("# %s\n\n- 通道：%s\n- 派给：%s\n- 状态：在途\n- 重试：0\n- 最近核对：%s\n\n## 说明\n%s\n",
		slug, channel, who, time.Now().Format("2006-01-02 15:04"), note)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return ""
	}
	return path
}

// safeTaskID 把任务号收敛成单个安全文件名：--update 的任务号来自用户/主控输入，
// 直接拼进 briefs/<id>.md 时 "../../etc/passwd" 会写到目录外。
func safeTaskID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		out = "task"
	}
	return out
}

func sanitizeSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "job"
	}
	return out
}

// noteLine 从正文里取一句可读备注（用于在途清单与台账）。
func noteLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len([]rune(s)) > 60 {
		s = string([]rune(s)[:60])
	}
	return s
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// taskClient 是撤销云端任务时用的平台客户端，测试接缝：撤销的平台守卫要能单测，
// 不能因为「守卫放行」就去 shell 一把真 CLI。
var taskClient = func() *hifox.Client { return hifox.New() }

// cmdCancel 是撤销（ADR 0020）：平台任务查在跑/排队的 run 再 cancel-run；本地关 pane。
func cmdCancel(args []string) error {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	task := fs.String("task", "", "平台任务号（HF-xxx）")
	agent := fs.String("agent", "", "本地动态 agent 名")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*task == "") == (*agent == "") {
		return errors.New("cancel 需要恰好一个 --task 或 --agent")
	}
	cfg, _, err := loadConfigWithAgents([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)

	if *task != "" {
		// 撤销必须看条目上的平台：拿 hifox 的 CLI 去撤别的平台的任务，撤不掉还会
		// 把在途清单里的条目按错误的理解清掉。任务号全局唯一，正常只有一条；条目
		// 被写坏/重复时以 hifox 为准（别让一条孤儿条目挡住正常的撤销）。
		entries, err := inflight.Load(cfg.InflightPath())
		if err != nil {
			return err
		}
		foreign := ""
		for _, e := range entries {
			if e.TaskID != *task {
				continue
			}
			if e.Platform == "" || e.Platform == platform.Hifox {
				foreign = ""
				break
			}
			foreign = e.Platform
		}
		if foreign != "" {
			return fmt.Errorf("平台 %q 的撤销还没有实现（任务 %s）；cancel 目前没有平台参数，只能撤 hifox 的活", foreign, *task)
		}
		out, err := cancelPlatformTask(ctx, cfg, taskClient(), *task)
		if err != nil {
			return err
		}
		return jsonPrint(out)
	}
	pane, err := cancelLocal(ctx, cfg, hc, *agent)
	if err != nil {
		return err
	}
	return jsonPrint(map[string]any{"agent": *agent, "pane": pane, "cancelled": true})
}

// cancelPlatformTask 取消平台任务下正在跑/排队的 run；没有可取消的 run 时只移出在途
// 清单并说明。移出按「任务号 + hifox 归属」匹配：任务号本该全局唯一，带平台条件是
// 防御写坏的清单——撤销一件 hifox 的活，不该顺手删掉别的平台的条目。
func cancelPlatformTask(ctx context.Context, cfg config.Config, client *hifox.Client, taskID string) (map[string]any, error) {
	runs, err := client.RunList(ctx, taskID)
	if err != nil {
		return nil, err
	}
	run, ok := hifox.ActiveRun(runs)
	if !ok {
		n, rerr := inflight.Remove(cfg.InflightPath(), func(e inflight.Entry) bool {
			return e.TaskID == taskID && (e.Platform == "" || e.Platform == platform.Hifox)
		})
		if rerr != nil {
			return nil, rerr
		}
		return map[string]any{"task_id": taskID, "cancelled": false, "removed": n, "note": "该任务没有排队中/运行中的 run"}, nil
	}
	if err := client.CancelRun(ctx, taskID, run.ID); err != nil {
		return nil, err
	}
	if _, err := inflight.Remove(cfg.InflightPath(), func(e inflight.Entry) bool {
		return e.TaskID == taskID && (e.Platform == "" || e.Platform == platform.Hifox)
	}); err != nil {
		return nil, err
	}
	return map[string]any{"task_id": taskID, "run_id": run.ID, "cancelled": true}, nil
}

// cancelLocal 关掉本地动态 agent 的 pane 并移出在途清单，返回实际关掉的 pane。
// 只解析一次 pane：调用方回报的必须是真正关掉的那个。
func cancelLocal(ctx context.Context, cfg config.Config, hc *herdr.Client, agent string) (string, error) {
	pane := ""
	if entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath()); err == nil {
		for _, e := range entries {
			if e.Agent == agent {
				pane = e.Pane
			}
		}
	}
	if pane == "" {
		agents, err := hc.AgentList(ctx)
		if err != nil {
			return "", err
		}
		if a, ok := herdr.FindAgent(agents, agent); ok {
			pane = a.PaneID
		}
	}
	if pane == "" {
		return "", fmt.Errorf("没找到 %s 对应的 pane（可能已经退出）", agent)
	}
	if err := hc.PaneClose(ctx, pane); err != nil {
		return "", fmt.Errorf("关 pane %s: %w", pane, err)
	}
	if _, err := inflight.Remove(cfg.InflightPath(), func(e inflight.Entry) bool { return e.Agent == agent }); err != nil {
		return pane, err
	}
	return pane, nil
}
