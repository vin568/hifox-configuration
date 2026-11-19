// Package ctxmeter 是受控 agent 的上下文度量层（ADR 0030）：把不同 harness 的 usage 归一成
// 同一个 Snapshot，并用模型窗口算占用比例。测量与策略分离——这里只回答
// 「用了多少、窗口多大、是否可信」，要不要轮换由调用方决定。
package ctxmeter

import (
	"context"
	"time"
)

// Source 是测量值的可信来源。unknown 不是 0：它表示「这次没测到」，调用方不能
// 据此做自动 handoff。
const (
	SourceMeasured  = "measured"  // provider 回报的 usage
	SourceEstimated = "estimated" // 本地 tokenizer/估算
	SourceUnknown   = "unknown"
)

// Target 是一个受控 agent 的静态描述；测量层不关心它由谁拉起。
type Target struct {
	Name         string
	Role         string // master | retro | job
	Kind         string // herdr 底层 agent 名（omp/opencode/...）
	Status       string // working | idle | done | blocked | unknown | absent（herdr 在册但没有该 agent）
	Pane         string
	SessionPath  string
	ResultPath   string
	BriefPath    string
	ProgressPath string
}

// Snapshot 是一次上下文测量。PromptTokens 是最近一次请求实际发出的 prompt，
// 包含该 harness 语义下的 cache；OutputTokens 是这次响应的输出；TailTokens 是
// 最后一次测量之后新增、尚未被 provider 计量的部分。
type Snapshot struct {
	Provider     string    `json:"provider,omitempty"`
	Model        string    `json:"model,omitempty"`
	PromptTokens int64     `json:"prompt_tokens"`
	OutputTokens int64     `json:"output_tokens,omitempty"`
	TailTokens   int64     `json:"tail_tokens,omitempty"`
	WindowTokens int64     `json:"window_tokens,omitempty"`
	Epoch        int       `json:"epoch,omitempty"`
	Source       string    `json:"source"`
	MeasuredAt   time.Time `json:"measured_at"`
}

// Used 是下一次请求前大致的上下文占用。最新一次 prompt 已经包含此前的 output，
// 所以只加「本次 output」和「测量之后的增量」。
func (s Snapshot) Used() int64 {
	return s.PromptTokens + s.OutputTokens + s.TailTokens
}

// Known 表示分子和分母都拿到了；unknown 不得用于自动 handoff。
func (s Snapshot) Known() bool {
	return s.Source != SourceUnknown && s.WindowTokens > 0 && s.Used() > 0
}

// Trusted 表示这个值可以被自动策略直接用于硬判定。estimated/bytes 只能用于告警
// 或人工判断：tokenizer 估算和文件字节都不是 provider 实际分词的结果。
func (s Snapshot) Trusted() bool {
	return s.Known() && s.Source == SourceMeasured
}

// Ratio 是占用比例；分母未知时返回 0，调用方必须先用 Known。
func (s Snapshot) Ratio() float64 {
	if !s.Known() {
		return 0
	}
	return float64(s.Used()) / float64(s.WindowTokens)
}

// Policy 是 handoff 的保留区策略：触发点 = window - max(window*reserve, reserveMin)。
// 用保留区而不是固定百分比，小窗口不会出现「warn 线高于 handoff 线」的倒挂。
type Policy struct {
	WarnRatio        float64
	ReserveRatio     float64
	ReserveMinTokens int64
	WarnMarginTokens int64
}

// DefaultPolicy 参考 OpenCode 的压缩保留策略：至少留 15% 或 16k token，告警线
// 在 handoff 线下方再留 8k，避免刚好在临界点报警。
func DefaultPolicy() Policy {
	return Policy{
		WarnRatio:        0.70,
		ReserveRatio:     0.15,
		ReserveMinTokens: 16000,
		WarnMarginTokens: 8000,
	}
}

// Ceiling 是 handoff 触发 token 数；0 表示无法判定。小窗口下 reserveMin 可能
// 大于整个窗口，此时退化为「保留一半」，否则 ceiling<=0 会让该 agent 永远不轮换。
func (p Policy) Ceiling(window int64) int64 {
	if window <= 0 {
		return 0
	}
	reserve := int64(float64(window) * p.ReserveRatio)
	if reserve < p.ReserveMinTokens {
		reserve = p.ReserveMinTokens
	}
	if reserve >= window {
		reserve = window / 2
	}
	ceiling := window - reserve
	if ceiling < 1 {
		return 1
	}
	return ceiling
}

// WarnAt 是告警 token 数；保证不小于 1、不大于 ceiling。margin 比 ceiling 还大
// 时退回 ceiling，避免算成 0 导致「任何用量都告警」。
func (p Policy) WarnAt(window int64) int64 {
	ceiling := p.Ceiling(window)
	if ceiling <= 0 {
		return 0
	}
	warn := int64(float64(window) * p.WarnRatio)
	if limit := ceiling - p.WarnMarginTokens; warn > limit {
		warn = limit
	}
	if warn < 1 || warn > ceiling {
		return ceiling
	}
	return warn
}

// Probe 读取某个 harness 的最新 usage。WindowTokens 可以为空：由 Measurer 用
// 模型目录补齐；harness 自己知道窗口时（Claude statusline/Codex/OpenCode）直接填。
type Probe interface {
	Measure(ctx context.Context, t Target) (Snapshot, error)
}

// Measurer 把 Probe 和模型窗口拼成完整 Snapshot。找不到 probe 时退回 Default；
// Default 为 nil 表示该 harness 没有测量实现。
type Measurer struct {
	Probes   map[string]Probe
	Default  Probe
	Resolver *Resolver
}

// Measure 返回测量结果。错误用于诊断，Snapshot 仍可带回 unknown 或已读取的部分。
func (m *Measurer) Measure(ctx context.Context, t Target) (Snapshot, error) {
	probe := m.Probes[t.Kind]
	if probe == nil {
		probe = m.Default
	}
	if probe == nil {
		return Snapshot{Source: SourceUnknown, MeasuredAt: time.Now()},
			errNoProbe(t.Kind)
	}
	s, err := probe.Measure(ctx, t)
	if s.Source == "" {
		s.Source = SourceMeasured
	}
	if s.MeasuredAt.IsZero() {
		s.MeasuredAt = time.Now()
	}
	if s.WindowTokens == 0 && m.Resolver != nil && s.Provider != "" && s.Model != "" {
		if window, ok := m.Resolver.Window(ctx, s.Provider, s.Model); ok {
			s.WindowTokens = window
		}
	}
	if s.PromptTokens <= 0 && s.OutputTokens <= 0 && s.TailTokens <= 0 {
		s.Source = SourceUnknown
	}
	return s, err
}

type probeError string

func (e probeError) Error() string { return string(e) }

func errNoProbe(kind string) error {
	if kind == "" {
		kind = "unknown"
	}
	return probeError("ctxmeter: 没有 " + kind + " 的上下文测量实现")
}
