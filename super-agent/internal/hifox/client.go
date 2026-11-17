package hifox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner 执行一条 hifox 命令返回 stdout（与 internal/herdr 同模式）。
type Runner func(ctx context.Context, args []string) (string, error)

// ExecRunner 跑 hifox CLI。org 非空时以 HIFOX_ORGANIZATION_ID 指定组织——
// `hifox agent list` 只列当前组织，跨组织派活必须显式切。
func ExecRunner(org string) Runner {
	return func(ctx context.Context, args []string) (string, error) {
		var out, errb bytes.Buffer
		cmd := exec.CommandContext(ctx, "hifox", args...)
		if org != "" {
			cmd.Env = append(os.Environ(), "HIFOX_ORGANIZATION_ID="+org)
		}
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("hifox %s: %w: %s", args[0], err, firstLine(errb.String()))
		}
		return out.String(), nil
	}
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

type Client struct{ Run Runner }

func New() *Client { return &Client{Run: ExecRunner("")} }

// NewForOrg 指定平台组织（跨组织派活用）。
func NewForOrg(org string) *Client { return &Client{Run: ExecRunner(org)} }

// Task 是哨兵关心的任务真值子集（`hifox task view <id> --output json` 的形状）。
type Task struct {
	Identifier         string `json:"identifier"`
	Title              string `json:"title"`
	Status             Status `json:"status"`
	DelegateStatus     string `json:"delegateStatus"`
	AgentWorkingStatus string `json:"agentWorkingStatus"`
	UpdatedAt          string `json:"updatedAt"`
}

type Status struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

func decodeTask(data string) (Task, error) {
	var t Task
	if err := json.Unmarshal([]byte(data), &t); err != nil {
		return Task{}, fmt.Errorf("decode task: %w", err)
	}
	return t, nil
}

// TaskView 按任务号（如 HF-11198，全局唯一）取任务真值。
func (c *Client) TaskView(ctx context.Context, id string) (Task, error) {
	out, err := c.Run(ctx, []string{"task", "view", id, "--output", "json"})
	if err != nil {
		return Task{}, fmt.Errorf("hifox task view %s: %w", id, err)
	}
	return decodeTask(out)
}

// Signature 是变化检测用的三元组快照：状态类别/名 | 委派状态 | agent 工作状态。
func (t Task) Signature() string {
	return t.Status.Category + "/" + t.Status.Name + "|" + t.DelegateStatus + "|" + t.AgentWorkingStatus
}

// TaskRef 是 `task create` 回给调用方的定位子集；字段缺失也算成功（任务已建成，
// 少个 url 不该让派活流程报错）。
type TaskRef struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	URL        string `json:"url"`
}

// TaskCreate 建任务。args 原样透传给 CLI（--space/--title/--delegate-agent/--body 等），
// 只补 --output json 以便解析；不管调用方传没传 --output，末次生效的必是 json。
func (c *Client) TaskCreate(ctx context.Context, args []string) (TaskRef, error) {
	full := append(append([]string{"task", "create"}, args...), "--output", "json")
	out, err := c.Run(ctx, full)
	if err != nil {
		return TaskRef{}, fmt.Errorf("hifox task create: %w", err)
	}
	var ref TaskRef
	if err := json.Unmarshal([]byte(out), &ref); err != nil {
		return TaskRef{}, fmt.Errorf("decode created task: %w", err)
	}
	return ref, nil
}

// Run 是一次执行会话的状态子集；Status 归一到 queued|running|succeeded|failed|cancelled
// （平台原始值是 completed/canceled 一类）。
type Run struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// RunList 列某任务的执行会话（`run list --task`）。
func (c *Client) RunList(ctx context.Context, taskID string) ([]Run, error) {
	out, err := c.Run(ctx, []string{"run", "list", "--task", taskID, "--output", "json"})
	if err != nil {
		return nil, fmt.Errorf("hifox run list --task %s: %w", taskID, err)
	}
	var runs []Run
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return nil, fmt.Errorf("decode runs for %s: %w", taskID, err)
	}
	for i := range runs {
		runs[i].Status = normalizeRunStatus(runs[i].Status)
	}
	return runs, nil
}

// CancelRun 取消任务下的一次执行；-y 跳过交互确认（调用方已负责确认）。
func (c *Client) CancelRun(ctx context.Context, taskID, runID string) error {
	if _, err := c.Run(ctx, []string{"task", "cancel-run", taskID, runID, "-y"}); err != nil {
		return fmt.Errorf("hifox task cancel-run %s %s: %w", taskID, runID, err)
	}
	return nil
}

// ActiveRun 取仍在跑的 run（running 优先于 queued）。判「该任务还有活在跑」用它，
// 免得重复派活或误判已收口。
func ActiveRun(runs []Run) (Run, bool) {
	var queued *Run
	for i, r := range runs {
		switch r.Status {
		case "running":
			return r, true
		case "queued":
			if queued == nil {
				queued = &runs[i]
			}
		}
	}
	if queued != nil {
		return *queued, true
	}
	return Run{}, false
}

// normalizeRunStatus 把平台原始状态折进对外词汇；未知状态原样保留，便于排查。
func normalizeRunStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "completed", "success", "succeeded", "done":
		return "succeeded"
	case "canceled", "cancelled":
		return "cancelled"
	case "pending", "waiting", "dispatched":
		return "queued"
	case "running", "failed", "queued":
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return s
	}
}

// AgentRef 是平台 agent 的最小可引用面（识别名、名字、岗位）。
// 派活需要的是平台 ref（名字或 ID），不是能力注册表的 key——两者不同名。
type AgentRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	JobTitle string `json:"jobTitle"`
}

// AgentList 列组织内 agent（`agent list --output json`）。
func (c *Client) AgentList(ctx context.Context) ([]AgentRef, error) {
	out, err := c.Run(ctx, []string{"agent", "list", "--output", "json"})
	if err != nil {
		return nil, fmt.Errorf("hifox agent list: %w", err)
	}
	var refs []AgentRef
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(out), &refs); err != nil {
		return nil, fmt.Errorf("decode agent list: %w", err)
	}
	return refs, nil
}

// ErrDelegateAmbiguous 表示注册表里的名字在平台上对应多个实例（prod/staging 等）。
var ErrDelegateAmbiguous = errors.New("delegate 名字对应多个平台实例")

// splitInstance 把平台名字拆成「基名 + 实例后缀」。平台上有两种后缀写法：
// `戴宗 (staging)`（hifox 集合）与 `燕青 - staging`（apifox 集合），都要认。
func splitInstance(platformName string) (base, instance string) {
	if i := strings.LastIndex(platformName, " ("); i > 0 && strings.HasSuffix(platformName, ")") {
		return platformName[:i], strings.TrimSuffix(platformName[i+2:], ")")
	}
	if i := strings.LastIndex(platformName, " - "); i > 0 {
		return platformName[:i], platformName[i+3:]
	}
	return platformName, ""
}

// nameMatches 判定平台基名是否就是声明的能力名。声明名可以是平台基名的前缀
// （apifox 集合的平台名带英文后缀，如声明「巴德尔」而平台叫「巴德尔 Baldur」），
// 但前缀之后必须是词边界，免得「萧」误配「萧让」。
func nameMatches(declared, base string) bool {
	if base == declared {
		return true
	}
	if !strings.HasPrefix(base, declared) {
		return false
	}
	// 按 rune 取边界字符：中文全角符号是多字节，按 byte 取会切错。
	rest := []rune(base[len(declared):])
	if len(rest) == 0 {
		return false
	}
	switch rest[0] {
	case ' ', '(', '-', '（', '·', '　':
		return true
	}
	return false
}

// ResolveDelegate 把「能力名字」（如 戴宗）解析成唯一平台 agent ref。
// 平台名字带实例后缀，两种写法都剥掉再比；instance 非空时只认该实例。
// 0 个匹配与多个匹配都报错并列出候选——歧义时宁可停手，不猜实例。
func ResolveDelegate(agents []AgentRef, base, instance string) (AgentRef, error) {
	if strings.TrimSpace(base) == "" {
		return AgentRef{}, errors.New("delegate 名字为空")
	}
	var hits []AgentRef
	for _, a := range agents {
		pbase, pinst := splitInstance(a.Name)
		if !nameMatches(base, pbase) {
			continue
		}
		if instance != "" && pinst != instance {
			continue
		}
		hits = append(hits, a)
	}
	switch len(hits) {
	case 0:
		return AgentRef{}, fmt.Errorf("平台上找不到名字 %q%s 的 agent（用 hifox agent list 核对）", base, instanceSuffix(instance))
	case 1:
		return hits[0], nil
	default:
		return AgentRef{}, fmt.Errorf("%w：%q 命中 %d 个（%s），用 --instance prod|staging 指定", ErrDelegateAmbiguous, base, len(hits), refNames(hits))
	}
}

func instanceSuffix(instance string) string {
	if instance == "" {
		return ""
	}
	return " (实例 " + instance + ")"
}

func refNames(refs []AgentRef) string {
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, r.Name)
	}
	return strings.Join(names, " / ")
}
