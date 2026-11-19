package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// CmdError 携带 herdr CLI 的结构化错误码（如 agent_blocked、agent_not_ready）。
type CmdError struct {
	Code    string
	Message string
	Stderr  string
}

func (e *CmdError) Error() string {
	return fmt.Sprintf("herdr %s: %s", e.Code, e.Message)
}

// IsBlocked 判定主控/能力 agent 是否停在审批或提问 UI（prompt 会被整体拒绝）。
func IsBlocked(err error) bool {
	for err != nil {
		if ce, ok := err.(*CmdError); ok {
			return ce.Code == "agent_blocked"
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// Runner 执行一条 herdr 命令并返回 stdout；错误时返回包装的 CmdError。
type Runner func(ctx context.Context, args []string) (string, error)

// Agent.Name 是 herdr 具名（songjiang / jiangjing）；Agent 是底层可执行名（omp）。
type Agent struct {
	Agent        string       `json:"agent"`
	Name         string       `json:"name"`
	AgentStatus  string       `json:"agent_status"`
	PaneID       string       `json:"pane_id"`
	CWD          string       `json:"cwd"`
	AgentSession AgentSession `json:"agent_session"`
}

// AgentSession 是 herdr 对 agent 会话身份的识别（source=herdr:omp 时为 omp 的
// session jsonl 绝对路径）。换脑的「是否真的换了会话」靠对比前后这个值来钉死。
type AgentSession struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

func ExecRunner(session string) Runner {
	return func(ctx context.Context, args []string) (string, error) {
		cmd := exec.CommandContext(ctx, "herdr", args...)
		cmd.Env = herdrEnv(os.Environ(), session)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", wrapErr(err, errb.String())
		}
		return out.String(), nil
	}
}

func wrapErr(err error, stderr string) error {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(stderr), &payload); jerr == nil && payload.Error.Code != "" {
		return &CmdError{Code: payload.Error.Code, Message: payload.Error.Message, Stderr: stderr}
	}
	return fmt.Errorf("herdr: %w: %s", err, firstLine(stderr))
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

type Client struct {
	Run Runner
}

func New(session string) *Client { return &Client{Run: ExecRunner(session)} }

type agentListResp struct {
	Result struct {
		Agents []Agent `json:"agents"`
	} `json:"result"`
}

func decodeAgentList(data string) ([]Agent, error) {
	var r agentListResp
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return nil, fmt.Errorf("decode agent list: %w", err)
	}
	return r.Result.Agents, nil
}

func (c *Client) AgentList(ctx context.Context) ([]Agent, error) {
	out, err := c.Run(ctx, []string{"agent", "list"})
	if err != nil {
		return nil, err
	}
	return decodeAgentList(out)
}

type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

type workspaceListResp struct {
	Result struct {
		Workspaces []struct {
			WorkspaceID string `json:"workspace_id"`
			Label       string `json:"label"`
		} `json:"workspaces"`
	} `json:"result"`
}

// herdrEnv 剥掉继承的 HERDR_SOCKET_PATH / HERDR_SESSION 再设目标 session：
// herdr 的解析顺序里 HERDR_SOCKET_PATH 优先于 HERDR_SESSION，从别的 herdr
// 上下文（如事件钩子）被拉起时继承值会把请求打到错误的 session。
func herdrEnv(environ []string, session string) []string {
	filtered := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if strings.HasPrefix(kv, "HERDR_SOCKET_PATH=") || strings.HasPrefix(kv, "HERDR_SESSION=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	return append(filtered, "HERDR_SESSION="+session)
}

func decodeWorkspaceList(data string) ([]Workspace, error) {
	var r workspaceListResp
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return nil, fmt.Errorf("decode workspace list: %w", err)
	}
	ws := make([]Workspace, 0, len(r.Result.Workspaces))
	for _, w := range r.Result.Workspaces {
		ws = append(ws, Workspace{WorkspaceID: w.WorkspaceID, Label: w.Label})
	}
	return ws, nil
}

func (c *Client) WorkspaceList(ctx context.Context) ([]Workspace, error) {
	out, err := c.Run(ctx, []string{"workspace", "list"})
	if err != nil {
		return nil, err
	}
	return decodeWorkspaceList(out)
}

type PaneRef struct {
	PaneID string `json:"pane_id"`
}

type wsCreateResp struct {
	Result struct {
		Workspace struct {
			WorkspaceID string `json:"workspace_id"`
			Label       string `json:"label"`
		} `json:"workspace"`
		RootPane PaneRef `json:"root_pane"`
	} `json:"result"`
}

func decodeWsCreate(data string) (Workspace, PaneRef, error) {
	var r wsCreateResp
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return Workspace{}, PaneRef{}, fmt.Errorf("decode workspace create: %w", err)
	}
	return Workspace{WorkspaceID: r.Result.Workspace.WorkspaceID, Label: r.Result.Workspace.Label}, r.Result.RootPane, nil
}

func (c *Client) WorkspaceCreate(ctx context.Context, cwd, label string) (Workspace, PaneRef, error) {
	out, err := c.Run(ctx, workspaceCreateArgs(cwd, label))
	if err != nil {
		return Workspace{}, PaneRef{}, err
	}
	return decodeWsCreate(out)
}

// workspaceCreateArgs / agentStartArgs / agentPromptArgs 是 herdr CLI 的参数
// 契约（纯函数，供直测钉住 flag 名与顺序）。
func workspaceCreateArgs(cwd, label string) []string {
	return []string{"workspace", "create", "--cwd", cwd, "--label", label, "--no-focus"}
}

func agentStartArgs(name, kind, pane string, extra []string) []string {
	args := []string{"agent", "start", name, "--kind", kind, "--pane", pane}
	if len(extra) > 0 {
		args = append(args, "--")
		args = append(args, extra...)
	}
	return args
}

func agentPromptArgs(target, text string, wait bool, timeoutMS int) []string {
	args := []string{"agent", "prompt", target, text}
	if wait {
		args = append(args, "--wait")
	}
	if timeoutMS > 0 {
		args = append(args, "--timeout", strconv.Itoa(timeoutMS))
	}
	return args
}

func agentPromptUntilArgs(target, text string, untils []string, timeoutMS int) []string {
	args := []string{"agent", "prompt", target, text, "--wait"}
	for _, u := range untils {
		args = append(args, "--until", u)
	}
	if timeoutMS > 0 {
		args = append(args, "--timeout", strconv.Itoa(timeoutMS))
	}
	return args
}

// AgentStart 在既有空闲 shell pane 上启动具名 agent；extra 原样透传给底层 CLI。
func (c *Client) AgentStart(ctx context.Context, name, kind, pane string, extra []string) error {
	_, err := c.Run(ctx, agentStartArgs(name, kind, pane, extra))
	return err
}

// AgentPrompt 注入一条 prompt。wait=true 时阻塞到 settled（lifecycle，不懂 turn）。
func (c *Client) AgentPrompt(ctx context.Context, target, text string, wait bool, timeoutMS int) error {
	_, err := c.Run(ctx, agentPromptArgs(target, text, wait, timeoutMS))
	return err
}

// AgentPromptUntil 注入 prompt 并等一个指定状态出现（--wait --until ...）。
// 用于「消息已递交且 agent 真的动了起来」的确认：blocked 会在写输入前被拒，
// 从非工作态提交后 5 秒内没有任何活动则返回 agent_prompt_stalled。
func (c *Client) AgentPromptUntil(ctx context.Context, target, text string, untils []string, timeoutMS int) error {
	_, err := c.Run(ctx, agentPromptUntilArgs(target, text, untils, timeoutMS))
	return err
}

func agentSendKeysArgs(target string, keys []string) []string {
	return append([]string{"agent", "send-keys", target}, keys...)
}

// AgentSendKeys 向 agent 所在 pane 送按键（换脑用 ctrl+d；esc 是中断键名）。
func (c *Client) AgentSendKeys(ctx context.Context, target string, keys ...string) error {
	if len(keys) == 0 {
		return fmt.Errorf("herdr: no keys given for %s", target)
	}
	_, err := c.Run(ctx, agentSendKeysArgs(target, keys))
	return err
}

// PaneClose 关掉一个 pane（herdr 语义：关掉 workspace 的最后一个 pane，workspace 随之消失）。
func (c *Client) PaneClose(ctx context.Context, paneID string) error {
	_, err := c.Run(ctx, []string{"pane", "close", paneID})
	return err
}

// WorkspaceClose 关掉一个 workspace（用于「workspace 在、pane 没了」的残局重建）。
func (c *Client) WorkspaceClose(ctx context.Context, workspaceID string) error {
	_, err := c.Run(ctx, []string{"workspace", "close", workspaceID})
	return err
}

// WorkspaceCreatePane 在既有 workspace 里新开一个 pane（`pane split` 需要现成的 pane 当锚）。
func (c *Client) WorkspaceCreatePane(ctx context.Context, anchorPane, direction, cwd string) (string, error) {
	out, err := c.Run(ctx, []string{"pane", "split", "--pane", anchorPane, "--direction", direction, "--cwd", cwd, "--no-focus"})
	if err != nil {
		return "", err
	}
	var r struct {
		Result struct {
			Pane PaneRef `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return "", fmt.Errorf("decode pane split: %w", err)
	}
	if r.Result.Pane.PaneID == "" {
		return "", fmt.Errorf("pane split returned no pane id")
	}
	return r.Result.Pane.PaneID, nil
}
