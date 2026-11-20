package boot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"super-agent/internal/herdr"
)

// PrimeSentinel 是首条注入的固定锚点：送达校验按它判定，初始化回合按它保持沉默。
const PrimeSentinel = "[初始化注入]"

// PrimeBanner 是首条注入的开头：初始化阶段没有明确任务时读完即止，不回复、不调工具。
// 换脑/急停会在后面追加自己的指令，按「以那段为准」执行。
const PrimeBanner = PrimeSentinel + " 以下是你的岗位手册与本机路径。这条注入本身没有任务：读完即止，不要回复用户、不要调用工具，等真实触发器进来再开始工作；如果后面另有指令，以那段为准。"

// 注入的送达校验：prompt 提交后轮询 agent 的会话文件，确认锚点真的落进去。
// 只提交成功不代表送达——omp 启动窗口里回车可能被吞、大段粘贴会折叠成 chip，
// 文本停在输入框。主控首条注入与本地派活的首条任务共用这套校验。
var (
	PrimeFirstWait = 3 * time.Second        // 先等这么久；没送达就补一次 Enter
	PrimeWait      = 15 * time.Second       // 补 Enter 后的总等待
	PrimePoll      = 250 * time.Millisecond // 轮询间隔（测试里调小）
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
	BriefsDir       string // 平台任务正文的归档副本（派活员写、运行时归档，ADR 0024）
	MediaDir        string // 飞书消息附件落盘目录（ADR 0024）
	InflightPath    string
	RuntimeCmd      string // 本机 superctl 的绝对路径（手册里的裸 superctl 指它）；空 = 不打印
	Index           string // 能力注册表渲染出的索引（名字+归属+description 原文）
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
	ensureAgent := func(name, wsLabel, prime, sessionDir string, args []string) error {
		a, running := herdr.FindAgent(agents, name)
		if running {
			primed := prime == "" || primeMarkerFresh(exp.PrimeMarkerDir, name, a.AgentSession.Value)
			if primed {
				rep.AlreadyRunning = append(rep.AlreadyRunning, name)
				return nil
			}
			// 在跑但没有为「这个会话」prime 过（启动窗口丢过手册 / 换脑后的新会话 /
			// 标记缺失）：补 prime。正在工作时先不动，避免打断在途回合，下一轮再补。
			if a.AgentStatus == "working" || a.AgentStatus == "blocked" {
				rep.AlreadyRunning = append(rep.AlreadyRunning, name)
				return nil
			}
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
	b.WriteString(PrimeBanner)
	b.WriteString("\n\n")
	b.WriteString(manual)
	b.WriteString("\n\n---\n本机路径（重启后以这条为准）：\n")
	fmt.Fprintf(&b, "- 能力目录：%s\n", p.CapabilitiesDir)
	fmt.Fprintf(&b, "- 作业台账活跃区：%s；归档区：%s\n", p.ActiveDir, p.ArchivedDir)
	fmt.Fprintf(&b, "- 结果目录：能力 agent 的产出 markdown 放 %s，作业记录只回文件路径\n", p.ResultsDir)
	if p.BriefsDir != "" {
		fmt.Fprintf(&b, "- 任务正文（Task Brief）：平台任务 description 的归档副本放 %s（派活员写、运行时自动归档；复查任务写了什么就读它）\n", p.BriefsDir)
	}
	if p.MediaDir != "" {
		fmt.Fprintf(&b, "- 消息附件：飞书发来的图片/文件已落盘在 %s，注入文本里以路径出现（派活时逐个 --attachment 带上）\n", p.MediaDir)
	}
	fmt.Fprintf(&b, "- 在途清单：%s（派活经 superctl dispatch 自动登记，收口自动移除；系统会替你盯平台状态变化）\n", p.InflightPath)
	b.WriteString("- 回复用户：superctl reply --chat <会话ID> --text <内容>（可多次调用分段发）\n")
	b.WriteString("- 回答承接人：superctl answer --platform <平台> --task <任务号> --text <用户原话>（平台任务的平台；投进任务对话，不是任务评论）\n")
	if p.RuntimeCmd != "" {
		fmt.Fprintf(&b, "- 运行时命令：%s（手册里的 superctl 指它；PATH 上没有时用绝对路径）\n", p.RuntimeCmd)
	}
	if strings.TrimSpace(p.Index) != "" {
		b.WriteString("\n---\n")
		b.WriteString(strings.TrimSpace(p.Index))
		b.WriteString("\n")
	}
	return b.String()
}

// SubmitPrompt 提交一条 prompt，并确认它真的进了 agent 当前会话（sentinel 已出现在
// 会话文件里）才返回。只提交成功不代表送达：omp 启动窗口里回车会被吞，大段粘贴又会
// 折叠成 paste chip 并把紧随的 Enter 一起吃掉。补 Enter 最多三次，blocked 时绝不按
// （那是在替用户答审批框）。主控首条注入与本地派活的首条任务共用这一条校验。
func SubmitPrompt(ctx context.Context, c *herdr.Client, name, text, sentinel string) (string, error) {
	text = strings.TrimRight(text, "\n")
	if err := c.AgentPrompt(ctx, name, text, false, 0); err != nil {
		return "", err
	}
	delivered, session := waitPrimeLanded(ctx, c, name, sentinel, PrimeFirstWait)
	// 首条注入恰好是大文本，所以「补 Enter」不是边角情况，而是主路径。给三次机会，
	// 每次之间留出 TUI 消化粘贴的时间。
	for attempt := 0; !delivered && attempt < 3; attempt++ {
		if st, ok := agentStatus(ctx, c, name); ok && st == "blocked" {
			break
		}
		if err := c.AgentSendKeys(ctx, name, "enter"); err != nil {
			return "", fmt.Errorf("补交 Enter: %w", err)
		}
		delivered, session = waitPrimeLanded(ctx, c, name, sentinel, PrimeWait/3)
	}
	if !delivered {
		return "", errors.New("prompt 没有进入会话（输入框可能卡住）")
	}
	return session, nil
}

// primeAgent 注入首条 prompt，确认它真的进了当前会话后再落盘标记。
func primeAgent(ctx context.Context, c *herdr.Client, exp Expected, name, prime string) error {
	text := strings.TrimRight(prime, "\n")
	sentinel := PrimeSentinel
	if !strings.Contains(prime, PrimeSentinel) {
		// 换脑/急停的自定义首条注入不一定带 banner：用首行当锚点，有 banner 时用固定锚点。
		sentinel = firstLine(text)
	}
	session, err := SubmitPrompt(ctx, c, name, text, sentinel)
	if err != nil {
		return fmt.Errorf("prime %s: %w", name, err)
	}
	if exp.PrimeMarkerDir != "" {
		if err := os.MkdirAll(exp.PrimeMarkerDir, 0o755); err != nil {
			return fmt.Errorf("prime %s: marker dir: %w", name, err)
		}
		stamp := time.Now().UTC().Format(time.RFC3339) + "\n" + session + "\n"
		if err := os.WriteFile(filepath.Join(exp.PrimeMarkerDir, name), []byte(stamp), 0o644); err != nil {
			return fmt.Errorf("prime %s: marker write: %w", name, err)
		}
	}
	return nil
}

// waitPrimeLanded 轮询 agent 的会话文件，确认首条注入确实落进去了。
// 只有「拿不到会话路径」（非 path 形态）才按已送达放行；agent 尚未在册、文件尚未
// 出现都继续等——新会话的 jsonl 往往要等首次提交才落盘。真正的失败是等满超时仍
// 没有这段文本（停在输入框）。
func waitPrimeLanded(ctx context.Context, c *herdr.Client, name, sentinel string, timeout time.Duration) (bool, string) {
	if sentinel == "" {
		return true, ""
	}
	deadline := time.Now().Add(timeout)
	for {
		if agents, err := c.AgentList(ctx); err == nil {
			if a, ok := herdr.FindAgent(agents, name); ok {
				session := a.AgentSession.Value
				if session == "" {
					return true, ""
				}
				if data, err := os.ReadFile(session); err == nil && strings.Contains(string(data), sentinel) {
					return true, session
				}
			}
		}
		if time.Now().After(deadline) {
			return false, ""
		}
		time.Sleep(PrimePoll)
	}
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

func agentStatus(ctx context.Context, c *herdr.Client, name string) (string, bool) {
	agents, err := c.AgentList(ctx)
	if err != nil {
		return "", false
	}
	a, ok := herdr.FindAgent(agents, name)
	if !ok {
		return "", false
	}
	return a.AgentStatus, true
}

// primeMarkerFresh 判断标记是否属于当前会话。老格式只有时间戳：拿得到会话路径时
// 视为不匹配（补一次手册），拿不到时按已 prime 处理，避免每轮巡检重复灌手册。
func primeMarkerFresh(dir, name, session string) bool {
	if dir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	parts := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(parts) < 2 {
		return session == ""
	}
	return parts[1] == session
}

func primeMarkerExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}
