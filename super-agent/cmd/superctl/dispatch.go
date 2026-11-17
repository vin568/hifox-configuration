package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/capability"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/config"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/herdr"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/hifox"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/inflight"
)

// ensureTopoFn 是幂等引导的测试接缝：本地派发必须先补拓扑，这条前置不能被静默跳过。
var ensureTopoFn = ensureTopo

// cloudReq 是一次云端派活的输入：字段多，收成类型而不是一串并列 string 参数。
type cloudReq struct {
	Space, Title, AgentKey, Body, Note string
	Priority, Sprint                   string
	Instance, Delegate                 string // Instance: prod|staging；Delegate: 显式平台 ref（跳过解析）
}

// cmdDispatch 是派活的唯一入口（ADR 0021）：两条通道都在这里登记在途清单、
// 起台账骨架，主控不必再记「派完记得写哪」。
func cmdDispatch(args []string) error {
	fs := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	cloud := fs.Bool("cloud", false, "派云端活（包 hifox task create）")
	local := fs.Bool("local", false, "派本地活（按能力注册表起 jobs agent）")
	req := cloudReq{}
	fs.StringVar(&req.Space, "space", "", "云端：Space（名/标识）")
	fs.StringVar(&req.Title, "title", "", "云端：任务标题")
	fs.StringVar(&req.AgentKey, "agent", "", "能力注册表的 key（云端解析成平台 ref；本地按配方起 agent）")
	fs.StringVar(&req.Body, "body", "", "任务正文")
	fs.StringVar(&req.Note, "note", "", "一句话备注（写进在途清单与台账骨架）")
	fs.StringVar(&req.Priority, "priority", "", "云端：优先级")
	fs.StringVar(&req.Sprint, "sprint", "", "云端：迭代")
	fs.StringVar(&req.Instance, "instance", "", "云端：平台实例后缀（prod|staging），歧义时必填")
	fs.StringVar(&req.Delegate, "delegate", "", "云端：显式平台 agent ref（名字或 ID），跳过注册表解析")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cloud == *local {
		return errors.New("dispatch 需要恰好一个 --cloud 或 --local")
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

	if *cloud {
		if req.Title == "" || req.AgentKey == "" {
			return errors.New("云端派活需要 --title 与 --agent")
		}
		out, err := dispatchCloud(ctx, cfg, hifox.NewForOrg, req, fs.Args())
		if err != nil {
			return err
		}
		return jsonPrint(out)
	}
	if req.AgentKey == "" || req.Body == "" {
		return errors.New("本地派活需要 --agent 与 --body")
	}
	// 本地派活要 pane：先幂等引导，否则 cancel 关掉 jobs 最后一个 pane 后这里会报
	// 「workspace jobs 不存在」——那一步该由运行时自己补，不该让主控撞上。
	if _, err := ensureTopoFn(ctx, cfg, hc); err != nil {
		return err
	}
	out, err := dispatchLocal(ctx, cfg, hc, req.AgentKey, req.Body, req.Note, fs.Args())
	if err != nil {
		return err
	}
	return jsonPrint(out)
}

// dispatchCloud 包 `hifox task create`：委派即触发；任务号回填在途清单与台账骨架。
// 平台 ref 由能力名解析（平台名字带实例后缀，注册表 key 不是平台 ref）。
func dispatchCloud(ctx context.Context, cfg config.Config, clientFor func(org string) *hifox.Client, req cloudReq, extra []string) (map[string]string, error) {
	reg, err := capability.Load(cfg.RepoDir)
	if err != nil {
		return nil, err
	}
	entry, ok := reg.CloudByKey(req.AgentKey)
	if !ok {
		return nil, fmt.Errorf("能力注册表里没有云端能力 %q（检查 super-agent/capabilities/cloud/）", req.AgentKey)
	}
	// 解析 ref 与建任务必须在同一个组织下：agent list 只列当前组织。
	client := clientFor(entry.Org)
	delegate := req.Delegate
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
	space := req.Space
	if space == "" {
		space = entry.Space
	}
	args := make([]string, 0, 10)
	if space != "" {
		args = append(args, "--space", space)
	}
	args = append(args, "--title", req.Title, "--delegate-agent", delegate)
	if req.Body != "" {
		args = append(args, "--body", req.Body)
	}
	if req.Priority != "" {
		args = append(args, "--priority", req.Priority)
	}
	if req.Sprint != "" {
		args = append(args, "--sprint", req.Sprint)
	}
	args = append(args, extra...)

	ref, err := client.TaskCreate(ctx, args)
	if err != nil {
		return nil, err
	}
	id := ref.Identifier
	if id == "" {
		id = ref.ID
	}
	if id == "" {
		return nil, errors.New("hifox task create 没回任务号，无法登记在途清单")
	}
	note := req.Note
	if note == "" {
		note = req.Title
	}
	e := inflight.Entry{Kind: "cloud", TaskID: id, Note: note, AddedAt: time.Now()}
	if err := inflight.Add(cfg.InflightPath(), e); err != nil {
		return nil, fmt.Errorf("任务已派出（%s）但登记在途清单失败: %w", id, err)
	}
	ledger := ledgerDraft(cfg, id+"-"+req.Title, "云端", delegate, note)
	return map[string]string{"task_id": id, "title": ref.Title, "url": ref.URL, "delegate": delegate, "ledger": ledger}, nil
}

// dispatchLocal 按注册表配方起一个 jobs agent，把任务注入进去并登记在途清单。
func dispatchLocal(ctx context.Context, cfg config.Config, hc *herdr.Client, agentKey, body, note string, extra []string) (map[string]string, error) {
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
	limit := recipe.MaxConcurrent
	if limit <= 0 {
		limit = 3
	}
	// 上限按「这个能力」的并发计，不是全局面板：两个能力各有各的额度。
	if active := countActiveLocal(entries, recipe.Key); active >= limit {
		return nil, fmt.Errorf("%s 并发已达上限 %d，等一个收口再派", recipe.Key, limit)
	}

	pane, err := acquirePane(ctx, cfg, hc, recipe.Workspace)
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%s-%s", agentKey, time.Now().UTC().Format("150405"))
	args := append(append([]string{}, recipe.Args...), extra...)
	if err := hc.AgentStart(ctx, name, orDefault(recipe.Kind, "omp"), pane, args); err != nil {
		return nil, fmt.Errorf("起 %s: %w", name, err)
	}
	resultPath := filepath.Join(cfg.ResultsDir(), name+".md")
	prompt := fmt.Sprintf("[任务] %s\n\n完成后把结果写成 markdown 存到 %s，只回文件路径。", body, resultPath)
	if err := hc.AgentPrompt(ctx, name, prompt, false, 0); err != nil {
		return nil, fmt.Errorf("任务已起到 %s 但注入失败（agent 还在跑，用 superctl cancel --agent %s 收掉）: %w", name, name, err)
	}
	if note == "" {
		note = noteLine(body)
	}
	e := inflight.Entry{Kind: "local", Agent: name, Pane: pane, Capability: recipe.Key, Note: note, AddedAt: time.Now()}
	if err := inflight.Add(cfg.InflightPath(), e); err != nil {
		return nil, fmt.Errorf("本地活已派出（%s）但登记在途清单失败: %w", name, err)
	}
	ledger := ledgerDraft(cfg, name, "本地", name, note)
	return map[string]string{"agent": name, "pane": pane, "result": resultPath, "ledger": ledger}, nil
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

// cmdCancel 是撤销（ADR 0020）：云端查在跑/排队的 run 再 cancel-run；本地关 pane。
func cmdCancel(args []string) error {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	task := fs.String("task", "", "云端任务号（HF-xxx）")
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
		out, err := cancelCloud(ctx, cfg, hifox.New(), *task)
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

// cancelCloud 取消任务下正在跑/排队的 run；没有可取消的 run 时只移出在途清单并说明。
func cancelCloud(ctx context.Context, cfg config.Config, client *hifox.Client, taskID string) (map[string]any, error) {
	runs, err := client.RunList(ctx, taskID)
	if err != nil {
		return nil, err
	}
	run, ok := hifox.ActiveRun(runs)
	if !ok {
		n, rerr := inflight.Remove(cfg.InflightPath(), func(e inflight.Entry) bool { return e.TaskID == taskID })
		if rerr != nil {
			return nil, rerr
		}
		return map[string]any{"task_id": taskID, "cancelled": false, "removed": n, "note": "该任务没有排队中/运行中的 run"}, nil
	}
	if err := client.CancelRun(ctx, taskID, run.ID); err != nil {
		return nil, err
	}
	if _, err := inflight.Remove(cfg.InflightPath(), func(e inflight.Entry) bool { return e.TaskID == taskID }); err != nil {
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
