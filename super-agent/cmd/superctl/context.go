package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"super-agent/internal/capability"
	"super-agent/internal/config"
	"super-agent/internal/ctxmeter"
	"super-agent/internal/herdr"
	"super-agent/internal/inflight"
)

// controlledContext 是一个受控 agent 的测量结果（ADR 0030）。Err 只用于 status/日志，
// 不参与自动 handoff 判定。
type controlledContext struct {
	Target   ctxmeter.Target
	Snapshot ctxmeter.Snapshot
	Err      string
}

func contextPolicy(cfg config.Config) ctxmeter.Policy {
	p := ctxmeter.DefaultPolicy()
	if cfg.ContextWarnRatio > 0 {
		p.WarnRatio = cfg.ContextWarnRatio
	}
	if cfg.ContextReserveRatio >= 0 {
		p.ReserveRatio = cfg.ContextReserveRatio
	}
	if cfg.ContextReserveMinTokens >= 0 {
		p.ReserveMinTokens = cfg.ContextReserveMinTokens
	}
	if cfg.ContextWarnMarginTokens >= 0 {
		p.WarnMarginTokens = cfg.ContextWarnMarginTokens
	}
	return p
}

func newContextMeasurer(cfg config.Config) *ctxmeter.Measurer {
	dbPath := cfg.ContextModelDB
	if dbPath == "" {
		dbPath = ctxmeter.DefaultModelDBPath()
	}
	resolver := &ctxmeter.Resolver{
		Overrides: cfg.ContextWindows,
		DB:        &ctxmeter.ModelDB{Path: dbPath},
	}
	probes := map[string]ctxmeter.Probe{
		"omp": ctxmeter.OMPProbe{},
	}
	for kind, cmd := range cfg.ContextProbes {
		if kind != "" && cmd != "" {
			probes[kind] = ctxmeter.CommandProbe{Command: cmd}
		}
	}
	return &ctxmeter.Measurer{Probes: probes, Default: ctxmeter.OMPProbe{}, Resolver: resolver}
}

// controlledTargets 是「受控 agent」的唯一枚举：常驻 master/retro + 在途本地 job。
// 非在途 herdr agent 不在这里——它们没有 report/tell/cancel 身份，不能被自动 handoff。
func controlledTargets(ctx context.Context, cfg config.Config, hc *herdr.Client) ([]ctxmeter.Target, error) {
	agents, err := hc.AgentList(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath())
	if err != nil {
		return nil, err
	}
	byName := make(map[string]herdr.Agent, len(agents))
	for _, a := range agents {
		name := a.Name
		if name == "" {
			name = a.Agent
		}
		if name != "" {
			byName[name] = a
		}
	}

	var reg *capability.Registry
	if reg, err = capability.Load(cfg.RepoDir); err != nil {
		reg = nil // 测量不应因为注册表漂移整体失败；缺失的 kind 走默认 probe
	}
	kindFor := func(key string) string {
		if reg != nil {
			if a, ok := reg.AgentByKey(key); ok && a.Kind != "" {
				return a.Kind
			}
		}
		return ""
	}

	target := func(name, role, capKey string, e *inflight.Entry) ctxmeter.Target {
		t := ctxmeter.Target{Name: name, Role: role}
		if a, ok := byName[name]; ok {
			t.Status = a.AgentStatus
			t.Pane = a.PaneID
			t.SessionPath = a.AgentSession.Value
			t.Kind = a.Agent
		} else {
			t.Status = "absent"
		}
		if t.Kind == "" {
			t.Kind = kindFor(capKey)
		}
		if t.Kind == "" {
			t.Kind = "omp"
		}
		if e != nil {
			t.BriefPath = e.Brief
			t.ProgressPath = e.Progress
			t.ResultPath = e.Result
		}
		if role == "job" {
			if t.BriefPath == "" && name != "" {
				t.BriefPath = filepath.Join(cfg.BriefsDir(), name+".md")
			}
			if t.ProgressPath == "" && name != "" {
				t.ProgressPath = filepath.Join(cfg.ProgressDir(), name+".md")
			}
			if t.ResultPath == "" && name != "" {
				t.ResultPath = filepath.Join(cfg.ResultsDir(), name+".md")
			}
		}
		return t
	}

	targets := make([]ctxmeter.Target, 0, 2+len(entries))
	if cfg.MasterAgent != "" {
		targets = append(targets, target(cfg.MasterAgent, "master", cfg.MasterAgent, nil))
	}
	if cfg.RetroAgent != "" {
		targets = append(targets, target(cfg.RetroAgent, "retro", cfg.RetroAgent, nil))
	}
	seen := map[string]bool{cfg.MasterAgent: true, cfg.RetroAgent: true}
	for _, e := range inflight.Local(entries) {
		if e.Agent == "" || seen[e.Agent] {
			continue
		}
		ee := e
		targets = append(targets, target(e.Agent, "job", e.Capability, &ee))
		seen[e.Agent] = true
	}
	sort.SliceStable(targets, func(i, j int) bool {
		rank := func(role string) int {
			switch role {
			case "master":
				return 0
			case "retro":
				return 1
			default:
				return 2
			}
		}
		if ri, rj := rank(targets[i].Role), rank(targets[j].Role); ri != rj {
			return ri < rj
		}
		return targets[i].Name < targets[j].Name
	})
	return targets, nil
}

// measureControlled 测量所有受控 agent。单个 probe 失败只影响该项，不拖垮整轮。
func measureControlled(ctx context.Context, cfg config.Config, hc *herdr.Client) ([]controlledContext, error) {
	targets, err := controlledTargets(ctx, cfg, hc)
	if err != nil {
		return nil, err
	}
	m := newContextMeasurer(cfg)
	out := make([]controlledContext, 0, len(targets))
	for _, t := range targets {
		s, err := m.Measure(ctx, t)
		c := controlledContext{Target: t, Snapshot: s}
		if err != nil {
			c.Err = err.Error()
		}
		out = append(out, c)
	}
	return out, nil
}

// measureAgent 是事件路径的单 agent 测量；找不到受控目标时 ok=false。
func measureAgent(ctx context.Context, cfg config.Config, hc *herdr.Client, name string) (ctxmeter.Snapshot, bool) {
	targets, err := controlledTargets(ctx, cfg, hc)
	if err != nil {
		return ctxmeter.Snapshot{Source: ctxmeter.SourceUnknown}, false
	}
	for _, t := range targets {
		if t.Name == name {
			s, _ := newContextMeasurer(cfg).Measure(ctx, t)
			return s, true
		}
	}
	return ctxmeter.Snapshot{Source: ctxmeter.SourceUnknown}, false
}

// localResultPath 取本地作业的稳定结果路径；handoff 后新 agent 名不同，但结果
// 仍写回原路径，事件和主控读文件都不需要改。旧条目没有 Result 时退回名字推导。
func localResultPath(cfg config.Config, name string) string {
	if entries, err := inflight.LoadMigrating(cfg.InflightPath(), cfg.LegacyWatchlistPath()); err == nil {
		if e, ok := inflight.FindLocal(entries, name); ok && e.Result != "" {
			return e.Result
		}
	}
	return filepath.Join(cfg.ResultsDir(), name+".md")
}

// fileNonEmpty 是 handoff 的交接材料校验：路径存在且不是空文件。
func fileNonEmpty(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

// agentContextJSON 是 status/事件共用的稳定形状；ratio 只在 Known 时有意义。
type agentContextJSON struct {
	Name         string  `json:"name"`
	Role         string  `json:"role"`
	Status       string  `json:"status"`
	Kind         string  `json:"kind,omitempty"`
	Pane         string  `json:"pane,omitempty"`
	Model        string  `json:"model,omitempty"`
	Provider     string  `json:"provider,omitempty"`
	PromptTokens int64   `json:"prompt_tokens"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	TailTokens   int64   `json:"tail_tokens,omitempty"`
	WindowTokens int64   `json:"window_tokens,omitempty"`
	UsedTokens   int64   `json:"used_tokens"`
	Ratio        float64 `json:"ratio,omitempty"`
	Known        bool    `json:"known"`
	Source       string  `json:"source"`
	Epoch        int     `json:"epoch,omitempty"`
	Session      string  `json:"session,omitempty"`
	Result       string  `json:"result,omitempty"`
	Brief        string  `json:"brief,omitempty"`
	Progress     string  `json:"progress,omitempty"`
	MeasuredAt   string  `json:"measured_at,omitempty"`
	Error        string  `json:"error,omitempty"`
}

func (c controlledContext) json() agentContextJSON {
	s := c.Snapshot
	out := agentContextJSON{
		Name:         c.Target.Name,
		Role:         c.Target.Role,
		Status:       c.Target.Status,
		Kind:         c.Target.Kind,
		Pane:         c.Target.Pane,
		Model:        s.Model,
		Provider:     s.Provider,
		PromptTokens: s.PromptTokens,
		OutputTokens: s.OutputTokens,
		TailTokens:   s.TailTokens,
		WindowTokens: s.WindowTokens,
		UsedTokens:   s.Used(),
		Known:        s.Known(),
		Source:       s.Source,
		Epoch:        s.Epoch,
		Session:      c.Target.SessionPath,
		Result:       c.Target.ResultPath,
		Brief:        c.Target.BriefPath,
		Progress:     c.Target.ProgressPath,
		Error:        c.Err,
	}
	if s.Known() {
		out.Ratio = s.Ratio()
	}
	if !s.MeasuredAt.IsZero() {
		out.MeasuredAt = s.MeasuredAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

func (c controlledContext) brief() string {
	ctx := c.json()
	var b string
	if ctx.Known {
		b = fmt.Sprintf("%s %s/%s：%d/%d（%.0f%%）", ctx.Name, ctx.Provider, ctx.Model, ctx.UsedTokens, ctx.WindowTokens, ctx.Ratio*100)
	} else {
		b = fmt.Sprintf("%s：上下文 unknown（%s）", ctx.Name, ctx.Error)
	}
	if c.Target.BriefPath != "" {
		b += "；brief=" + c.Target.BriefPath
	}
	if c.Target.ProgressPath != "" {
		b += "；progress=" + c.Target.ProgressPath
	}
	if c.Target.ResultPath != "" {
		b += "；result=" + c.Target.ResultPath
	}
	return b
}
