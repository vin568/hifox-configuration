package hifox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	Identifier         string       `json:"identifier"`
	OrganizationID     string       `json:"organizationId"`
	Title              string       `json:"title"`
	Status             Status       `json:"status"`
	DelegateStatus     string       `json:"delegateStatus"`
	AgentWorkingStatus string       `json:"agentWorkingStatus"`
	UpdatedAt          string       `json:"updatedAt"`
	URL                string       `json:"url"`
	Attachments        []Attachment `json:"attachments"`
	// ChatSessionID 是任务的对话会话（承接人执行所在的那条），`hifox task view`
	// 的 chatSessionId。它是「与当前执行者最老的那条」，不一定等于正在等用户回答
	// 的那条——多会话任务里答案投错会话，提问的承接人不会被唤醒，所以回答优先用
	// SessionStatus.Top 指向的会话（见 AnswerSessionID）。
	ChatSessionID string            `json:"chatSessionId"`
	SessionStatus TaskSessionStatus `json:"sessionStatus"`
}

// TaskSessionStatus 是 `task view` 里会话状态汇总的人读面（v2 起为聚合形状）。
// Top 是当前「最需要注意」的那条会话状态：承接人提问后会写 waiting_human_reply，
// 并带上真正等回答的 SessionID。
type TaskSessionStatus struct {
	Top *TaskSessionTop `json:"top"`
}

// TaskSessionTop 是聚合里排第一的会话状态事件。
type TaskSessionTop struct {
	State     string `json:"state"`
	SessionID string `json:"sessionId"`
}

// AnswerSessionID 选回答该投进哪条会话：会话状态汇总里排第一的那条（Top）优先，
// 它是当前最该被推进的会话——承接人提问时是 waiting_human_reply，正在跑时是
// working（补一句话会作为排队回合进它正在跑的上下文）。`chatSessionId` 只是
// 「与当前执行者最老的那条」，多会话任务里常常不是活的会话，只能在没有汇总时兜底。
// 两者都没有时返回空串，由调用方拒绝并说明——绝不退回任务评论。
func (t Task) AnswerSessionID() string {
	if top := t.SessionStatus.Top; top != nil {
		if id := strings.TrimSpace(top.SessionID); id != "" {
			return id
		}
	}
	return strings.TrimSpace(t.ChatSessionID)
}

// Attachment 是任务附件在 `task view` 里的人读面。preview.url 是任务自身的预览地址
// （相对路径，Web 端按同源解析）：正文里嵌图只能用它，聊天里的临时 URL 会过期。
//
// CommentID 非空表示这条附件挂在评论上，不是任务正文的资源——嵌进正文会把评论里的
// 截图（HF-11745 实测 1 条）搬到 description 里，让承接人以为那是任务本身的材料。
type Attachment struct {
	ID          string `json:"id"`
	CommentID   string `json:"commentId"`
	Filename    string `json:"filename"`
	MimeType    string `json:"mimeType"`
	DownloadURL string `json:"downloadUrl"`
	Preview     struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		URL    string `json:"url"`
	} `json:"preview"`
}

// IsImage 判定附件是不是图片（markdown 嵌图只对图片有意义）。
func (a Attachment) IsImage() bool {
	return strings.HasPrefix(strings.ToLower(a.MimeType), "image/")
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

// UserMessage 是以用户身份写进任务对话的一条消息（POST
// /rest/v1/chat/sessions/<id>/messages 的返回）。Queued 表示承接人当前回合还在跑，
// 这条消息排在它后面；RunID 是被这条消息唤醒的那次执行。
type UserMessage struct {
	MessageID string `json:"messageId"`
	RunID     string `json:"runId"`
	Queued    bool   `json:"queued"`
}

// PostUserMessage 把一段话以 CLI 的人工身份写进任务对话（网页里用户打字那条路）。
//
// 不走 `hifox chat send`：它打的是 `/agent-messages`，只接受会话绑定的 agent 执行
// 身份，人工令牌必然 403（HF-16187 实测）。任务评论是另一条面，承接人不会被评论
// 唤醒，所以这里只提供对话通道——没有写评论的第二个入口（ADR 0031）。
func (c *Client) PostUserMessage(ctx context.Context, sessionID, text string) (UserMessage, error) {
	// 边界守卫：没有任何会话就不该打出一条空路径请求。选哪条会话是调用方
	// （Task.AnswerSessionID）的事，这里只说「没有可投的会话」。
	if strings.TrimSpace(sessionID) == "" {
		return UserMessage{}, errors.New("没有可投的对话会话")
	}
	if strings.TrimSpace(text) == "" {
		return UserMessage{}, errors.New("回答正文为空")
	}
	payload, err := json.Marshal(map[string]string{"content": text})
	if err != nil {
		return UserMessage{}, fmt.Errorf("encode answer: %w", err)
	}
	out, err := c.Run(ctx, []string{
		"api", "post", "/rest/v1/chat/sessions/" + url.PathEscape(sessionID) + "/messages",
		"--data", string(payload), "--output", "json",
	})
	if err != nil {
		return UserMessage{}, fmt.Errorf("把回答发进任务对话 %s: %w", sessionID, err)
	}
	var msg UserMessage
	if strings.TrimSpace(out) == "" {
		return msg, nil
	}
	if err := json.Unmarshal([]byte(out), &msg); err != nil {
		return UserMessage{}, fmt.Errorf("decode chat message: %w", err)
	}
	return msg, nil
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

// TaskEdit 编辑任务（`task edit <id>`）：args 原样透传（--body-file/--attachment/--type），
// 只补 --output json。返回编辑后的任务真值（含 description 与 attachments）。
func (c *Client) TaskEdit(ctx context.Context, id string, args []string) (Task, error) {
	full := append(append([]string{"task", "edit", id}, args...), "--output", "json")
	out, err := c.Run(ctx, full)
	if err != nil {
		return Task{}, fmt.Errorf("hifox task edit %s: %w", id, err)
	}
	return decodeTask(out)
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

// AgentRef 是平台 agent 的可引用面与对账面：识别名、名字、岗位，加上
// 空间归属/归档/派活许可——能力注册表与 environments/ 的对账要这几项当真相。
// 派活需要的是平台 ref（名字或 ID），不是能力注册表的 key——两者不同名。
// JSON 里的 null 解成零值即可，不必用指针。
type AgentRef struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	JobTitle            string   `json:"jobTitle"`
	SpaceIDs            []string `json:"spaceIds"`
	ArchivedAt          string   `json:"archivedAt"`
	AllowTaskAssignment bool     `json:"allowTaskAssignment"`
	CanManage           bool     `json:"canManage"`
	Status              string   `json:"status"`
}

// AgentList 列组织内 agent（`agent list --output json`，不含归档）。派活只用它。
func (c *Client) AgentList(ctx context.Context) ([]AgentRef, error) {
	return c.agentList(ctx, "hifox agent list", "agent", "list", "--output", "json")
}

// AgentListAll 列组织内 agent，含已归档（`agent list --include-archived`）。
// 对账要看归档状态，所以不能复用只列在册项的 AgentList。
func (c *Client) AgentListAll(ctx context.Context) ([]AgentRef, error) {
	return c.agentList(ctx, "hifox agent list --include-archived", "agent", "list", "--include-archived", "--output", "json")
}

func (c *Client) agentList(ctx context.Context, what string, args ...string) ([]AgentRef, error) {
	out, err := c.Run(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
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

// PlatformNameMatches 判定平台名字（`戴宗 (staging)` / `燕青 - staging` 这类写法先剥
// 实例后缀）是不是声明的能力名。--check --live 用它核对「已部署实例的线上名字与
// 平台指针目录里声明的名字还是同一个」——与派活时 ResolveDelegate 用的是同一套匹配规则。
func PlatformNameMatches(declared, platformName string) bool {
	base, _ := splitInstance(platformName)
	return NameMatches(declared, base)
}

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

// NameMatches 判定一个平台名字的基名是否就是声明的能力名。声明名可以是平台基名的
// 前缀（apifox 集合的平台名带英文后缀，如声明「巴德尔」而平台叫「巴德尔 Baldur」），
// 但前缀之后必须是词边界，免得「萧」误配「萧让」。
//
// 导出是因为能力注册表的 config 对账（capability.Check）要用同一套判定——两处各写
// 一份「同名」实现，会让「注册表对账」与「派活解析」对同一个名字给出两种结论。
func NameMatches(declared, base string) bool {
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

// instanceProd 是承接层级的默认实例：它在平台名字上没有后缀（prod 那一份就叫裸名），
// 所以请求 prod 时既认裸名也认显式写的 prod 后缀。
const instanceProd = "prod"

// instanceMatches 判定平台名字上的实例后缀是否命中被请求的实例。空请求全匹配；
// 请求 prod 时认「无后缀」与「显式 prod」两种写法，其余按字面比。
func instanceMatches(pinst, want string) bool {
	switch want {
	case "":
		return true
	case instanceProd:
		return pinst == "" || pinst == instanceProd
	default:
		return pinst == want
	}
}

// ResolveDelegate 把「能力名字」（如 戴宗）解析成唯一平台 agent ref。
// 平台名字带实例后缀，两种写法都剥掉再比；instance 非空时只认该实例
// （prod = 没有后缀的那一份）。0 个匹配与多个匹配都报错并列出候选——歧义时宁可停手，
// 不猜实例。
func ResolveDelegate(agents []AgentRef, base, instance string) (AgentRef, error) {
	if strings.TrimSpace(base) == "" {
		return AgentRef{}, errors.New("delegate 名字为空")
	}
	var hits []AgentRef
	for _, a := range agents {
		pbase, pinst := splitInstance(a.Name)
		if !NameMatches(base, pbase) {
			continue
		}
		if !instanceMatches(pinst, instance) {
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
