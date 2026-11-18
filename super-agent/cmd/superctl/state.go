package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"super-agent/internal/config"
	feishu "super-agent/internal/feishu"
	"super-agent/internal/herdr"
	"super-agent/internal/inflight"
	"super-agent/internal/inject"
)

// errDeferredInject 表示「注入被急停闸门延后」：哨兵据此不推进快照，变化留到解除后补交。
var errDeferredInject = errors.New("halt: 注入延后")

// liveAgent 是 jobs workspace 里还挂着的动态 agent（名字 + pane）。
type liveAgent struct {
	Name string
	Pane string
}

// truth 是在途面的两种视图：给主控的（含 pane/路径，用于处置）与给用户的（只留人话，
// 一体人设：不出口 agent 名、pane id、文件名）。两者同源，避免一处泄漏、一处缺失。
type truth struct {
	cloud        []string // "HF-1：note"
	local        []string // note
	ledger       []string // 人读的台账标签（去掉日期前缀与 .md）
	live         []liveAgent
	topologyErr  error
	notes        map[string]string // agent -> 清单里登记的说明（两视图共用，不重读盘）
	unregistered int
}

// collectTruth 汇总机器可见的在途面：在途清单 + 活跃区台账 + jobs workspace 活动 agent。
func collectTruth(ctx context.Context, cfg config.Config, hc *herdr.Client) truth {
	var t truth
	entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath())
	if err != nil {
		slog.Warn("superctl: read inflight failed", "error", err)
	}
	t.notes = make(map[string]string, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		switch e.Kind {
		case "local":
			t.local = append(t.local, noteOr(e.Note, "一件本机活"))
			t.notes[e.Agent] = noteOr(e.Note, "已登记")
			seen[e.Agent] = true
		default:
			t.cloud = append(t.cloud, fmt.Sprintf("%s：%s", e.TaskID, noteOr(e.Note, "云端任务")))
		}
	}
	for _, name := range ledgerFiles(cfg.ActiveDir()) {
		t.ledger = append(t.ledger, humanizeLedger(name))
	}
	t.live, t.topologyErr = jobsAgents(ctx, hc)
	for _, a := range t.live {
		if !seen[a.Name] {
			t.unregistered++
		}
	}
	return t
}

// forUser 是回执正文：只留人能懂的东西（任务号、一句话说明、在办的标签）。
func (t truth) forUser() string {
	var b strings.Builder
	for _, line := range t.cloud {
		fmt.Fprintf(&b, "- 云端任务 %s\n", line)
	}
	for _, line := range t.local {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	for _, line := range t.ledger {
		fmt.Fprintf(&b, "- 在办：%s\n", line)
	}
	if t.unregistered > 0 {
		fmt.Fprintf(&b, "- 另有 %d 件在本机跑着、还没登记的活\n", t.unregistered)
	}
	if t.topologyErr != nil {
		b.WriteString("- 本机那部分没查干净（盘点出错），可能还有遗漏\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// forMaster 是机器真值：pane、agent、文件路径全给，主控据此处置与取消。
func (t truth) forMaster(cfg config.Config) string {
	var b strings.Builder
	for _, line := range t.cloud {
		fmt.Fprintf(&b, "- 云端任务 %s（在途清单在册）\n", line)
	}
	for _, a := range t.live {
		note := ""
		if n, ok := t.notes[a.Name]; ok {
			note = "：" + n
		}
		fmt.Fprintf(&b, "- 本机动态 agent %s（pane %s）%s\n", a.Name, a.Pane, note)
	}
	for _, name := range ledgerFiles(cfg.ActiveDir()) {
		fmt.Fprintf(&b, "- 作业台账在途：%s\n", name)
	}
	return strings.TrimRight(b.String(), "\n")
}

func noteOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// humanizeLedger 把 `20261117-Windows问题跟进.md` 变成 `Windows问题跟进`。
func humanizeLedger(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if i := strings.IndexByte(name, '-'); i == 8 && len(name) > 9 {
		if _, err := time.Parse("20060102", name[:8]); err == nil {
			name = name[9:]
		}
	}
	if name == "" {
		return "一件事"
	}
	return name
}

func ledgerFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// jobsAgents 列出 jobs workspace 里还挂着的 agent（按 workspace 拓扑认，不靠命名）。
func jobsAgents(ctx context.Context, hc *herdr.Client) ([]liveAgent, error) {
	t, err := hc.Topology(ctx)
	if err != nil {
		return nil, err
	}
	var out []liveAgent
	for _, pane := range t.PanesOf("jobs") {
		a, ok := t.ByPane[pane]
		if !ok {
			continue
		}
		name := a.Name
		if name == "" {
			name = a.Agent
		}
		out = append(out, liveAgent{Name: name, Pane: pane})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pane < out[j].Pane })
	return out, nil
}

func currentSession(ctx context.Context, hc *herdr.Client, agent string) string {
	agents, err := hc.AgentList(ctx)
	if err != nil {
		return ""
	}
	if a, ok := herdr.FindAgent(agents, agent); ok {
		return a.AgentSession.Value
	}
	return ""
}

// feishuInjector 是 serve 的飞书注入回调：闸门判定 → 幂等引导 → 注入 → blocked 提醒。
// 抽成函数是为了让「急停期间哪类消息能进」有可测的入口（原先它埋在 serve 闭包里）。
func feishuInjector(cfg config.Config, hc *herdr.Client, g gate, notifier *blockedNotifier) feishu.InjectFunc {
	return func(ictx context.Context, _ feishu.Source, p inject.Payload) error {
		if !g.admit(p) {
			slog.Info("superctl: 注入被闸门拦下", "chat_id", p.ChatID, "proxy", p.ProxyReply)
			return nil
		}
		if _, err := ensureTopo(ictx, cfg, hc); err != nil {
			return err
		}
		if err := inject.Do(ictx, hc, cfg.MasterAgent, inject.BuildFeishu(p)); err != nil {
			if errors.Is(err, inject.ErrBlocked) && notifier != nil {
				notifier.Notify()
			}
			return err
		}
		return nil
	}
}

// sentinelInjector 是云端哨兵的注入回调：急停期间只延迟（不推进快照），解除后补交。
func sentinelInjector(cfg config.Config, hc *herdr.Client, g gate, notifier *blockedNotifier) func(text string) error {
	return func(text string) error {
		if !g.allowMachine() {
			return errDeferredInject
		}
		if err := inject.Do(context.Background(), hc, cfg.MasterAgent, text); err != nil {
			if errors.Is(err, inject.ErrBlocked) && notifier != nil {
				notifier.Notify()
			}
			return err
		}
		return nil
	}
}
