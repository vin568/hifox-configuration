package boot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/herdr"
)

// Expected 是幂等引导的目标拓扑（ADR 0018）：master / crew / jobs 三个 workspace
// + 主控与复盘两个常驻 agent。动态作业 agent 不在此列（jobs workspace 只建容器）。
type Expected struct {
	RepoDir          string
	MasterAgent      string
	RetroAgent       string
	Kind             string   // 底层 CLI kind（omp）
	MasterArgs       []string // agent start 透传参数（如指定快模型）
	MasterSessionDir string   // 主控本轮次会话目录（换脑/首次启动都落留档目录）
	MasterPrime      string   // 新启动主控时的首条注入（操作手册 + 能力索引 + 路径）
	RetroArgs        []string
	RetroSessionDir  string
	RetroPrime       string // 新启动复盘 agent 时的首条注入
	DynamicWS        string // 动态作业 workspace 标签（jobs）
	PrimeMarkerDir   string // prime 成功标记目录（state/primed）；空 = 不记标记
}

// Paths 是首条注入里引用的本机路径集（由 config 派生，避免六个裸参数结伴走）。
type Paths struct {
	CapabilitiesDir string
	ActiveDir       string
	ArchivedDir     string
	ResultsDir      string
	InflightPath    string
	Index           string // 能力注册表渲染出的索引（名字+一句话+何时用+文档路径）
}

type Report struct {
	WorkspacesCreated []string `json:"workspaces_created"`
	AgentsStarted     []string `json:"agents_started"`
	Primed            []string `json:"primed"`
	AlreadyRunning    []string `json:"already_running"`
}

func (r *Report) empty() bool {
	return len(r.WorkspacesCreated) == 0 && len(r.AgentsStarted) == 0
}

// Ensure 把命名 session 对账到目标拓扑：缺 workspace 就建、缺常驻 agent 就 start
// （omp 集成原生 resume 已有会话）、新启动或漏 prime 的 agent 立即注入首条 prompt。
// prime 成功以 PrimeMarkerDir 里的标记文件为准——上次 prime 失败的 agent 下轮会补。
// 全部就位时零副作用——注入器与巡检 tick 每次行动前都跑它（注入前幂等引导）。
func Ensure(ctx context.Context, c *herdr.Client, exp Expected) (*Report, error) {
	rep := &Report{}

	list, err := c.WorkspaceList(ctx)
	if err != nil {
		return nil, fmt.Errorf("workspace list: %w", err)
	}
	labelID := map[string]string{}
	for _, w := range list {
		labelID[w.Label] = w.WorkspaceID
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}

	panes := map[string]string{} // workspace label -> root pane
	for _, label := range []string{"master", "crew", exp.DynamicWS} {
		if id, ok := labelID[label]; ok {
			if p := snap.RootPane(id); p != "" {
				panes[label] = p
				continue
			}
			// workspace 在、pane 没了（换脑时关 pane 的兜底路径可能留下这种残局）：
			// 关掉空壳再按创建路径重建，避免在这里硬报错把注入路径永久卡死。
			if err := c.WorkspaceClose(ctx, id); err != nil {
				return nil, fmt.Errorf("workspace %s has no pane and close failed: %w", label, err)
			}
			delete(labelID, label)
		}
		ws, pane, err := c.WorkspaceCreate(ctx, exp.RepoDir, label)
		if err != nil {
			return nil, fmt.Errorf("create workspace %s: %w", label, err)
		}
		rep.WorkspacesCreated = append(rep.WorkspacesCreated, ws.Label)
		labelID[ws.Label] = ws.WorkspaceID
		panes[label] = pane.PaneID
	}

	agents, err := c.AgentList(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent list: %w", err)
	}
	hasAgent := func(name string) bool {
		_, ok := herdr.FindAgent(agents, name)
		return ok
	}

	ensureAgent := func(name, wsLabel, prime, sessionDir string, args []string) error {
		primed := exp.PrimeMarkerDir != "" && primeMarkerExists(exp.PrimeMarkerDir, name)
		if hasAgent(name) {
			if primed || prime == "" {
				rep.AlreadyRunning = append(rep.AlreadyRunning, name)
				return nil
			}
			// agent 在跑但从未 prime 成功（上次失败/标记丢失）：补 prime。
			// 没有这一支，hasAgent 短路会让它永远收不到操作手册。
			if err := primeAgent(ctx, c, exp, name, prime); err != nil {
				return err
			}
			rep.Primed = append(rep.Primed, name)
			return nil
		}
		pane, ok := panes[wsLabel]
		if !ok {
			return fmt.Errorf("no root pane for workspace %s", wsLabel)
		}
		if sessionDir != "" {
			args = append(append([]string{}, args...), "--session-dir", sessionDir)
		}
		if err := c.AgentStart(ctx, name, exp.Kind, pane, args); err != nil {
			return fmt.Errorf("start %s: %w", name, err)
		}
		rep.AgentsStarted = append(rep.AgentsStarted, name)
		if prime != "" {
			if err := primeAgent(ctx, c, exp, name, prime); err != nil {
				return err
			}
			rep.Primed = append(rep.Primed, name)
		}
		return nil
	}

	if err := ensureAgent(exp.MasterAgent, "master", exp.MasterPrime, exp.MasterSessionDir, exp.MasterArgs); err != nil {
		return nil, err
	}
	if exp.RetroAgent != "" {
		if err := ensureAgent(exp.RetroAgent, "crew", exp.RetroPrime, exp.RetroSessionDir, exp.RetroArgs); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// PrimePrompt 拼装主控首条注入：操作手册全文 + 本机绝对路径。
func PrimePrompt(manual string, p Paths) string {
	var b strings.Builder
	b.WriteString(manual)
	b.WriteString("\n\n---\n本机路径（重启后以这条为准）：\n")
	fmt.Fprintf(&b, "- 能力目录：%s\n", p.CapabilitiesDir)
	fmt.Fprintf(&b, "- 作业台账活跃区：%s；归档区：%s\n", p.ActiveDir, p.ArchivedDir)
	fmt.Fprintf(&b, "- 结果目录：能力 agent 的产出 markdown 放 %s，作业记录只回文件路径\n", p.ResultsDir)
	fmt.Fprintf(&b, "- 在途清单：%s（派活经 superctl dispatch 自动登记，收口自动移除；系统会替你盯云端状态变化）\n", p.InflightPath)
	b.WriteString("- 回复用户：superctl reply --chat <会话ID> --text <内容>（可多次调用分段发）\n")
	if strings.TrimSpace(p.Index) != "" {
		b.WriteString("\n---\n")
		b.WriteString(strings.TrimSpace(p.Index))
		b.WriteString("\n")
	}
	return b.String()
}

// primeAgent 注入首条 prompt 并在成功后落盘标记；标记写失败视为 prime 失败
// （宁可下轮重 prime，不可误判已就绪）。
func primeAgent(ctx context.Context, c *herdr.Client, exp Expected, name, prime string) error {
	if err := c.AgentPrompt(ctx, name, prime, false, 0); err != nil {
		return fmt.Errorf("prime %s: %w", name, err)
	}
	if exp.PrimeMarkerDir != "" {
		if err := os.MkdirAll(exp.PrimeMarkerDir, 0o755); err != nil {
			return fmt.Errorf("prime %s: marker dir: %w", name, err)
		}
		stamp := time.Now().UTC().Format(time.RFC3339) + "\n"
		if err := os.WriteFile(filepath.Join(exp.PrimeMarkerDir, name), []byte(stamp), 0o644); err != nil {
			return fmt.Errorf("prime %s: marker write: %w", name, err)
		}
	}
	return nil
}

func primeMarkerExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}
