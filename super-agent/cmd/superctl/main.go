package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"super-agent/internal/boot"
	"super-agent/internal/config"
	feishu "super-agent/internal/feishu"
	"super-agent/internal/halt"
	"super-agent/internal/herdr"
	"super-agent/internal/hifox"
	"super-agent/internal/inflight"
	"super-agent/internal/inject"
	"super-agent/internal/outbox"
	"super-agent/internal/sentinel"
	"super-agent/internal/tick"
)

const usage = `superctl — 宋江 super agent 本地运行时（super-agent/，ADR 0016–0031）

用法：superctl <命令> [参数]

命令：
  ensure       幂等引导：按能力注册表对账 herdr 拓扑（master/crew/jobs + 常驻 agent），缺则补齐
  status       概览：session / workspaces / agents / 活跃作业 / 在途清单 / 急停状态 / 受控 agent 上下文 / 待发回帖
  inject       手工注入一条 prompt 到主控（--source feishu|event|manual）
  report       能力 agent → 主控：中途回报一句（--agent 自己的名字 --text）
  tell         主控 → 本地能力 agent：补信息或追问（--agent 目标 --text）
  checkpoint   让 idle/done 的本地作业把进度写进 progress 文件（交接前置）
  handoff      交接本地作业：停旧 agent，用 brief + progress 重新拉起，结果写回原路径
  tick         立即注入一次作业巡检（无在途内容时不发）
  reply        追加一条回帖到 outbox（主控回用户的唯一通道）
  answer       把用户的回答投进平台任务对话（不是任务评论）：--platform <平台> --task <任务号> --text/--text-file
  serve        常驻：飞书长连接 + outbox 投递 + 定时巡检 + 注入闸门
  platformwatch  平台哨兵一轮：读在途清单的平台条目轮询 hifox，有状态变化才注入
  halt         急停：落闸 + 秒级回执 + 换脑（解除=用户下一句私聊；离线兜底 resume）
  resume       解除急停闸门（离线兜底，正常不用）
  new          人工换脑：起新会话、清空上下文（/new 的命令行等价物）
  dispatch     派活：--platform <hifox> 建平台任务（--agent 是能力 key，平台 ref 自动解析；正文 --desc-file、附件 --attachment）/ --local 按配方起 jobs agent；自动登记
  cancel       撤销：--task 取消平台运行 / --agent 关本地 pane，并移出在途清单
  capabilities  能力注册表：打印索引；--check 查漂移；--check --live 再对账平台真值（索引与注入主控的那份同源）
  probe        连上飞书只打印事件 ID（取 open_id / chat_id / bot_open_id 的引导工具）
  help         本帮助

配置查找：--config 路径 > $SONGJIANG_CONFIG > ./superctl.toml > ~/.config/songjiang/superctl.toml
飞书凭证走环境变量：SONGJIANG_FEISHU_APP_ID / SONGJIANG_FEISHU_APP_SECRET（不进配置文件）`

// commands 是唯一的命令注册表：usage 文案与之对齐，dispatch 测试钉死
// 「usage 列了但表里没有」这类漏注册（reply 曾因此在集成编辑中丢失）。
var commands = map[string]func(args []string) error{
	"ensure":        cmdEnsure,
	"status":        cmdStatus,
	"inject":        cmdInject,
	"report":        cmdReport,
	"tell":          cmdTell,
	"checkpoint":    cmdCheckpoint,
	"handoff":       cmdHandoff,
	"tick":          cmdTick,
	"platformwatch": cmdPlatformwatch,
	"reply":         cmdReply,
	"answer":        cmdAnswer,
	"serve":         cmdServe,
	"probe":         cmdProbe,
	"halt":          cmdHalt,
	"resume":        cmdResume,
	"new":           cmdNew,
	"dispatch":      cmdDispatch,
	"cancel":        cmdCancel,
	"capabilities":  cmdCapabilities,
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	name := os.Args[1]
	if name == "help" || name == "-h" || name == "--help" {
		fmt.Println(usage)
		return
	}
	run, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s\n", name, usage)
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "superctl:", err)
		os.Exit(1)
	}
}

// loadConfig 只读配置文件，不做注册表派生：reply 这类命令不该因注册表坏掉而失联
// （主控回话是唯一对外通道，不能在「能力表读不出来」时一起哑掉）。
func loadConfig(args []string) (config.Config, string, error) {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return config.Config{}, "", err
	}
	path, err := config.Find(*cfgPath)
	if err != nil {
		return config.Config{}, "", err
	}
	cfg, err := config.Load(path)
	return cfg, path, err
}

// loadConfigWithAgents 在读配置之外，从能力注册表派生主控/复盘 agent 名（ADR 0021）。
// 需要 agent 名的命令用它；不要让「回话」「探针」这类基础通道依赖注册表。
func loadConfigWithAgents(args []string) (config.Config, string, error) {
	cfg, path, err := loadConfig(args)
	if err != nil {
		return cfg, path, err
	}
	master, retro, err := deriveAgentNames(cfg)
	if err != nil {
		return cfg, path, err
	}
	// 已下线的 master_agent/retro_agent：值一致就警告后忽略（存量部署平滑升级），
	// 值不一致就硬报错——那说明有人以为它还在生效。
	for _, k := range []struct{ key, legacy, derived string }{
		{"master_agent", cfg.LegacyMasterAgent, master},
		{"retro_agent", cfg.LegacyRetroAgent, retro},
	} {
		if k.legacy == "" || k.legacy == k.derived {
			if k.legacy != "" {
				slog.Warn("superctl: 配置里的已下线键被忽略（以能力注册表为准）", "key", k.key, "value", k.legacy)
			}
			continue
		}
		return cfg, path, fmt.Errorf("config %s 的 %s=%q 已不再生效（注册表派生为 %q）；改注册表或删掉该键", path, k.key, k.legacy, k.derived)
	}
	// cloud_poll_interval 是 platform_poll_interval 的旧名（cloud 曾当 hifox 的同义词）：
	// 值一致就当迁移期噪声略过，值不一致必须当场报错，否则「改了周期却不生效」会变成悬案。
	if legacy := cfg.LegacyCloudPollInterval; legacy != "" {
		if legacy != cfg.PlatformPollInterval {
			return cfg, path, fmt.Errorf("config %s 的 cloud_poll_interval=%q 已不再生效（新键为 platform_poll_interval=%q）；改用新键或删掉旧键", path, legacy, cfg.PlatformPollInterval)
		}
		slog.Warn("superctl: 配置里的已下线键被忽略", "key", "cloud_poll_interval", "value", legacy)
	}
	cfg.MasterAgent, cfg.RetroAgent = master, retro
	return cfg, path, nil
}

// snapshotPaths 返回（新快照, 旧名快照）：旧名只在升级窗口里当基线（ADR 0023 的
// 「升级动作」），新文件一旦生成就以它为准。两个调用点共用它，免得升级兼容被漏掉。
func snapshotPaths(cfg config.Config) (string, string) {
	return cfg.PlatformSnapPath(), cfg.LegacySnapPath()
}

// ensureTopo 注入前幂等引导（ADR 0018）：拓扑由能力注册表派生（ADR 0021），
// 缺 workspace/agent 就补，新 agent 灌首条 prompt（手册 + 索引 + 路径）。
func ensureTopo(ctx context.Context, cfg config.Config, hc *herdr.Client) (*boot.Report, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}
	exp, err := expectedTopo(cfg)
	if err != nil {
		return nil, err
	}
	return boot.Ensure(ctx, hc, exp)
}

func cmdEnsure(args []string) error {
	cfg, path, err := loadConfigWithAgents(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	rep, err := ensureTopo(ctx, cfg, herdr.New(cfg.Session))
	if err != nil {
		return err
	}
	fmt.Printf("config=%s\n", path)
	return jsonPrint(rep)
}

func cmdStatus(args []string) error {
	cfg, _, err := loadConfigWithAgents(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)
	ws, err := hc.WorkspaceList(ctx)
	if err != nil {
		return fmt.Errorf("herdr session %s 不可达（先 herdr session attach %s）: %w", cfg.Session, cfg.Session, err)
	}
	agents, err := hc.AgentList(ctx)
	if err != nil {
		return err
	}
	active := countFiles(cfg.ActiveDir())
	ob, err := outbox.Open(cfg.OutboxPath())
	if err != nil {
		return err
	}
	type status struct {
		Session        string             `json:"session"`
		Workspaces     []string           `json:"workspaces"`
		Agents         []string           `json:"agents"`
		ActiveJobs     int                `json:"active_jobs"`
		InFlight       int                `json:"inflight"`
		Halted         bool               `json:"halted"`
		MasterSession  string             `json:"master_session,omitempty"`
		SessionBytes   int64              `json:"master_session_bytes,omitempty"`
		AgentContexts  []agentContextJSON `json:"agent_contexts,omitempty"`
		PendingReplies int                `json:"pending_replies"`
	}
	labels := make([]string, 0, len(ws))
	for _, w := range ws {
		labels = append(labels, w.Label)
	}
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		n := a.Name
		if n == "" {
			n = a.Agent
		}
		names = append(names, n+"("+a.AgentStatus+")")
	}
	entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath())
	if err != nil {
		slog.Warn("superctl: read inflight failed", "error", err)
	}
	st := status{
		Session:        cfg.Session,
		Workspaces:     labels,
		Agents:         names,
		ActiveJobs:     active,
		InFlight:       len(entries),
		Halted:         halt.Active(cfg.HaltPath()),
		PendingReplies: ob.Pending(outbox.DefaultMaxAttempts),
	}
	if master, ok := findAgentByName(agents, cfg.MasterAgent); ok {
		st.MasterSession = master.AgentSession.Value
		if fi, err := os.Stat(master.AgentSession.Value); err == nil {
			st.SessionBytes = fi.Size()
		}
	}
	if measured, merr := measureControlled(ctx, cfg, hc); merr == nil {
		for _, c := range measured {
			st.AgentContexts = append(st.AgentContexts, c.json())
		}
	} else {
		slog.Warn("superctl: measure agent contexts failed", "error", merr)
	}
	return jsonPrint(st)
}

// findAgentByName 按具名或底层名找 agent（注册表派生后主控名来自注册表，不再硬编码）。
func findAgentByName(agents []herdr.Agent, name string) (herdr.Agent, bool) {
	for _, a := range agents {
		if a.Name == name || a.Agent == name {
			return a, true
		}
	}
	return herdr.Agent{}, false
}

func cmdInject(args []string) error {
	fs := flag.NewFlagSet("inject", flag.ContinueOnError)
	source := fs.String("source", "manual", "feishu|event|manual")
	chatID := fs.String("chat", "", "来源会话 ID")
	kind := fs.String("kind", "dm", "dm|group")
	sender := fs.String("sender", "", "发送者 ID")
	proxy := fs.Bool("proxy-reply", false, "代答场景")
	text := fs.String("text", "", "注入文本（event=状态变化描述）")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*text) == "" {
		return errors.New("--text required")
	}
	cfg, _, err := loadConfigWithAgents([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)
	if _, err := ensureTopo(ctx, cfg, hc); err != nil {
		return err
	}
	prompt, err := buildInjectPrompt(*source, inject.Payload{ChatID: *chatID, SenderID: *sender, Text: *text, Kind: *kind, ProxyReply: *proxy})
	if err != nil {
		return err
	}
	if err := inject.Do(ctx, hc, cfg.MasterAgent, prompt); err != nil {
		if errors.Is(err, inject.ErrBlocked) {
			return fmt.Errorf("主控在等确认界面，注入被拒；先在 herdr 里处理主控的 blocked 状态: %w", err)
		}
		return err
	}
	fmt.Println("injected")
	return nil
}

// buildInjectPrompt 按 --source 拼注入文本；未知 source 拒绝（闭集 feishu|event|manual）。
func buildInjectPrompt(source string, p inject.Payload) (string, error) {
	switch source {
	case "feishu":
		return inject.BuildFeishu(p), nil
	case "event":
		return inject.BuildEvent(p.Text), nil
	case "manual":
		return p.Text, nil
	default:
		return "", fmt.Errorf("unknown --source %q", source)
	}
}

func cmdTick(args []string) error {
	cfg, _, err := loadConfigWithAgents(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)
	if _, err := ensureTopo(ctx, cfg, hc); err != nil {
		return err
	}
	if !(gate{cfg: cfg}).allowMachine() {
		return errors.New("急停期间巡检不注入（变化攒着，解除后补交）")
	}
	// 轮换先于巡检：主控刚被巡检叫起来就是 working，轮换会永远撞不上窗口。
	if err := maybeRotate(ctx, cfg, hc); err != nil {
		slog.Warn("superctl: rotate failed", "error", err)
	}
	if alerts, health := runSupervisionScans(ctx, cfg, hc); alerts+health > 0 {
		fmt.Printf("supervision alerts injected: context=%d health=%d\n", alerts, health)
	}
	if !tick.Needed(sweepSignals(ctx, cfg, hc)) {
		fmt.Println("sweep skipped (no in-flight work)")
		return nil
	}
	if err := inject.Do(ctx, hc, cfg.MasterAgent, tick.SweepPrompt()); err != nil {
		if errors.Is(err, inject.ErrBlocked) {
			return fmt.Errorf("主控 blocked，本轮巡检跳过: %w", err)
		}
		return err
	}
	fmt.Println("sweep injected")
	return nil
}

// sweepSignals 是注入卫生的判据来源（ADR 0022）：活跃区台账文件数、在途清单条数、
// jobs workspace 活动 agent 数。三者皆空就不必叫醒主控。
func sweepSignals(ctx context.Context, cfg config.Config, hc *herdr.Client) tick.Signals {
	s := tick.Signals{ActiveJobs: countFiles(cfg.ActiveDir())}
	if entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath()); err != nil {
		slog.Warn("superctl: read inflight for sweep failed", "error", err)
	} else {
		s.Inflight = len(entries)
	}
	live, _ := jobsAgents(ctx, hc)
	s.JobsAgents = len(live)
	return s
}

func cmdPlatformwatch(args []string) error {
	cfg, _, err := loadConfigWithAgents(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)
	if _, err := ensureTopo(ctx, cfg, hc); err != nil {
		return err
	}
	if !(gate{cfg: cfg}).allowMachine() {
		return errors.New("急停期间哨兵不注入（变化攒着，解除后补交）")
	}
	snap, legacySnap := snapshotPaths(cfg)
	res, err := sentinel.Poll(ctx, hifox.New(), cfg.InflightPath(), snap, legacySnap, func(text string) error {
		return inject.Do(ctx, hc, cfg.MasterAgent, text)
	})
	if err != nil {
		return err
	}
	return jsonPrint(res)
}

func cmdReply(args []string) error {
	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	chatID := fs.String("chat", "", "目标会话 ID")
	text := fs.String("text", "", "回帖内容")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *chatID == "" || *text == "" {
		return errors.New("--chat and --text required")
	}
	cfg, _, err := loadConfig([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	ob, err := outbox.Open(cfg.OutboxPath())
	if err != nil {
		return err
	}
	e, err := ob.Append(*chatID, *text)
	if err != nil {
		return err
	}
	fmt.Println(e.ID)
	return nil
}

// blockedNote 是主控卡在确认界面时发给用户的私聊提醒（一体人设：第一人称、
// 口语、不出口内部术语）。
const blockedNote = "我卡在一个要确认的界面上了，等你来点一下我才能继续。"

// blockedNotifier 限频发送 blockedNote：feishu 重试与巡检 tick 都会撞
// blocked，同一窗口内只发一条，避免刷屏（窗口取 30 分钟）。
type blockedNotifier struct {
	ob     *outbox.Outbox
	chatID string
	window time.Duration

	mu   sync.Mutex
	last time.Time
}

func (n *blockedNotifier) Notify() {
	if n.chatID == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if time.Since(n.last) < n.window {
		return
	}
	if _, err := n.ob.Append(n.chatID, blockedNote); err != nil {
		slog.Warn("superctl: append blocked note failed", "error", err)
		return
	}
	n.last = time.Now()
}

// feishuConfigFrom 把运行时配置装配成飞书适配层配置。抽成纯函数是为了让
// 「附件落盘开关」这一行有测试把守——它断了，线上所有图片/文件都退回 key 占位，
// 没有任何报错（HF-16095 的丢图会以「只剩 key」的形式复发）。
func feishuConfigFrom(cfg config.Config, appID, secret string) feishu.Config {
	proxy := map[string]bool{}
	for _, g := range cfg.ProxyReplyGroups {
		proxy[g] = true
	}
	retention, _ := cfg.MediaRetention() // 非法值已在 config.Validate 拦截
	return feishu.Config{
		AppID:            appID,
		AppSecret:        secret,
		AllowedUserIDs:   cfg.AllowedUsers,
		ProxyReplyGroups: proxy,
		AnyMessageGroups: cfg.AnyMessageGroups,
		BotMentionOpenID: cfg.BotMentionOpenID,
		DedupPath:        cfg.DedupPath(),
		MediaDir:         cfg.MediaDir(),
		MediaRetention:   retention,
	}
}

// requireFeishuCredentials 是 serve/probe 共用的凭证门：缺凭证硬报错
// （密钥卫生：凭证只走环境变量）。
func requireFeishuCredentials() (string, string, error) {
	id, secret := feishu.CredentialsFromEnv()
	if id == "" || secret == "" {
		return "", "", errors.New("缺少飞书凭证：设置 SONGJIANG_FEISHU_APP_ID / SONGJIANG_FEISHU_APP_SECRET（自建应用，见 super-agent/README.md）")
	}
	return id, secret, nil
}

func cmdServe(args []string) error {
	cfg, cfgPath, err := loadConfigWithAgents(args)
	if err != nil {
		return err
	}
	fmt.Printf("config=%s\nallowed_users=%v\nproxy_reply_groups=%v any_message_groups=%v\nadmin_chat_id=%q bot_open_id_set=%t\n",
		cfgPath, cfg.AllowedUsers, cfg.ProxyReplyGroups, cfg.AnyMessageGroups, cfg.AdminChatID, cfg.BotMentionOpenID != "")
	for _, u := range cfg.AllowedUsers {
		if strings.Contains(u, "pending") {
			fmt.Println("警告：allowed_users 仍是占位符，所有私聊都会被过滤丢弃——用 superctl probe 取真实 open_id 后替换")
			break
		}
	}
	appID, secret, err := requireFeishuCredentials()
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hc := herdr.New(cfg.Session)
	if _, err := ensureTopo(ctx, cfg, hc); err != nil {
		return err
	}
	ob, err := outbox.Open(cfg.OutboxPath())
	if err != nil {
		return err
	}

	fcfg := feishuConfigFrom(cfg, appID, secret)
	// 指令面：白名单私聊里的 /halt 与 /new 由运行时消化（ADR 0020/0022）。
	idx := commandIndices{cfg: cfg, hc: hc, ob: ob}
	fcfg.CommandFunc = func(cctx context.Context, chatID, _ /*sender*/ string, text string) bool {
		return idx.absorb(cctx, chatID, text)
	}
	sender, err := feishu.NewSender(fcfg)
	if err != nil {
		return err
	}

	notifier := &blockedNotifier{ob: ob, chatID: cfg.AdminChatID, window: 30 * time.Minute}
	g := gate{cfg: cfg}

	injectFn := feishuInjector(cfg, hc, g, notifier)

	errCh := make(chan error, 5)
	go func() { errCh <- feishu.Run(ctx, fcfg, injectFn) }()

	interval, err := cfg.SweepInterval()
	if err != nil {
		return err
	}
	go func() {
		tk := time.NewTicker(interval)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				errCh <- nil
				return
			case <-tk.C:
				if !g.allowMachine() {
					slog.Info("superctl: 急停期间跳过巡检")
					continue
				}
				if _, err := ensureTopo(ctx, cfg, hc); err != nil {
					slog.Warn("superctl: sweep ensure failed", "error", err)
					continue
				}
				// 轮换窗口：先换脑再巡检——换完的新会话正好接着做这一轮（顺序反了
				// 会永远撞上「主控刚被巡检叫起来 = working」，轮换永远不触发）。
				if err := maybeRotate(ctx, cfg, hc); err != nil {
					slog.Warn("superctl: rotate failed", "error", err)
				}
				if alerts, health := runSupervisionScans(ctx, cfg, hc); alerts+health > 0 {
					slog.Info("superctl: supervision alerts injected", "context", alerts, "health", health)
				}
				// 注入卫生（ADR 0022）：三源皆空就不叫醒主控。
				if sig := sweepSignals(ctx, cfg, hc); !tick.Needed(sig) {
					slog.Debug("superctl: sweep skipped (no in-flight work)", "signals", sig)
				} else if err := inject.Do(ctx, hc, cfg.MasterAgent, tick.SweepPrompt()); err != nil {
					if errors.Is(err, inject.ErrBlocked) {
						// 主控 blocked：本轮巡检跳过并转私聊提醒（限频，ADR 0018）。
						slog.Warn("superctl: master blocked, sweep skipped", "error", err)
						notifier.Notify()
					} else {
						slog.Warn("superctl: sweep inject failed", "error", err)
					}
				}
			}
		}
	}()
	go func() {
		tk := time.NewTicker(5 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				errCh <- nil
				return
			case <-tk.C:
				_, _, _ = ob.Drain(ctx, sender.Send, outbox.DefaultMaxAttempts)
			}
		}
	}()
	go func() {
		platformInterval, err := cfg.PlatformInterval()
		if err != nil {
			errCh <- err
			return
		}
		tk := time.NewTicker(platformInterval)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				errCh <- nil
				return
			case <-tk.C:
				// 急停期间哨兵照跑（纯 CLI、零 LLM 成本）：继续轮询才能保持首见基线新鲜；
				// 注入被闸门延后——Poll 不推进快照，变化就攒着，解除后一次性补交。
				if _, err := ensureTopo(ctx, cfg, hc); err != nil { // 注入前幂等引导（ADR 0018）
					slog.Warn("superctl: platformwatch ensure failed", "error", err)
					continue
				}
				pollSnap, pollLegacySnap := snapshotPaths(cfg)
				if _, err := sentinel.Poll(ctx, hifox.New(), cfg.InflightPath(), pollSnap, pollLegacySnap, sentinelInjector(cfg, hc, g, notifier)); err != nil && !errors.Is(err, errDeferredInject) {
					slog.Warn("superctl: platformwatch poll failed", "error", err)
				}
			}
		}
	}()
	go func() {
		err := herdr.Subscribe(ctx, cfg.Session, func() []herdr.Subscription {
			// 订阅面 = 除主控外所有带 agent 的 pane（herdr 按 pane 订阅，不通配；
			// 主控 pane 按 master workspace 拓扑解析，不依赖 agent 名字存活）。
			idx, err := hc.PaneIndex(ctx, "master")
			if err != nil {
				return nil
			}
			subs := make([]herdr.Subscription, 0, len(idx.ByPane))
			for pane, a := range idx.ByPane {
				if pane == idx.MasterPane || a.Name == cfg.MasterAgent {
					continue
				}
				subs = append(subs, herdr.Subscription{"type": "pane.agent_status_changed", "pane_id": pane})
			}
			return subs
		}, func(ev herdr.Event) error {
			if !g.allowMachine() {
				return nil // 急停期间事件唤醒静默
			}
			if _, err := ensureTopo(ctx, cfg, hc); err != nil { // 注入前幂等引导（ADR 0018）
				slog.Warn("superctl: event ensure failed", "error", err)
				return nil
			}
			idx, err := hc.PaneIndex(ctx, "master")
			if err != nil {
				slog.Warn("superctl: event pane index failed", "error", err)
				return nil
			}
			if _, err := handleAgentEvent(ctx, cfg, hc, idx, ev); err != nil {
				if errors.Is(err, inject.ErrBlocked) {
					notifier.Notify()
				}
				slog.Warn("superctl: event inject failed", "error", err)
			}
			return nil // 订阅循环不因单条注入失败而断
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	fmt.Printf("superctl serve: session=%s master=%s tick=%s\n", cfg.Session, cfg.MasterAgent, cfg.TickInterval)
	return <-errCh
}

func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}

func jsonPrint(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	debug := fs.Bool("debug", false, "SDK 日志开到 Debug（打印连接与每条 frame）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	appID, secret, err := requireFeishuCredentials()
	if err != nil {
		return err
	}
	fcfg := feishu.Config{AppID: appID, AppSecret: secret}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if oid, berr := feishu.BotOpenID(ctx, fcfg); berr != nil {
		fmt.Printf("bot_open_id 获取失败：%v（继续监听事件）\n", berr)
	} else {
		fmt.Printf("bot_open_id = %s  ← 填进 superctl.toml 的 bot_open_id\n", oid)
	}
	fmt.Println("监听中：给 bot 发条私聊（或在群里 @ 它 / @ 你），每条事件一行 ID，照抄进配置；Ctrl-C 退出。")
	return feishu.Probe(ctx, fcfg, *debug)
}

// handleAgentEvent 处理一条本地能力 agent 状态变化事件：只认
// pane.agent_status_changed；主控 pane（按 master workspace 拓扑解析）与
// 主控大名的翻转是常态不是信号；陌生 pane（其他 session）跳过。
//
// 只有「经 superctl dispatch 派出的在途本地作业」的 done/blocked 才叫醒主控：
// working/idle 是噪声（本次 6 分钟事故里 working 事件插进主控回合，什么也没带来），
// 常驻/别处起的 agent（蒋敬的日常起动）也不该吵主控。done 附结果文件路径，
// 主控不必再猜「它把结果写哪了」。
func handleAgentEvent(ctx context.Context, cfg config.Config, hc *herdr.Client, idx herdr.PaneIndex, ev herdr.Event) (bool, error) {
	if ev.Event != "pane.agent_status_changed" || ev.Data.AgentStatus == "" || ev.Data.PaneID == "" {
		return false, nil
	}
	if ev.Data.PaneID == idx.MasterPane {
		return false, nil
	}
	a, known := idx.ByPane[ev.Data.PaneID]
	if !known {
		return false, nil
	}
	name := a.Name
	if name == "" {
		name = a.Agent
	}
	if name == cfg.MasterAgent {
		return false, nil
	}
	switch ev.Data.AgentStatus {
	case "done", "blocked":
	default:
		return false, nil
	}
	entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath())
	if err != nil {
		return false, err
	}
	if !inflight.HasLocal(entries, name) {
		return false, nil
	}
	detail := fmt.Sprintf("%s（%s）状态 → %s", name, ev.Data.PaneID, ev.Data.AgentStatus)
	if ev.Data.AgentStatus == "done" {
		// done 只是「这一回合结束了」：它可能是真做完，也可能是跑完 report 在等你回话。
		// 措辞必须点破这一点，否则主控会把「等用户拍板」当成收口。
		detail += "（回合结束，不代表已交付——可能是在等用户回话）；结果：" + localResultPath(cfg, name)
		if snap, ok := measureAgent(ctx, cfg, hc, name); ok && snap.Known() {
			detail += fmt.Sprintf("；上下文 %d/%d（%.0f%%）", snap.Used(), snap.WindowTokens, snap.Ratio()*100)
		}
	}
	if err := inject.Do(ctx, hc, cfg.MasterAgent, inject.BuildEvent(detail)); err != nil {
		return false, err
	}
	return true, nil
}
