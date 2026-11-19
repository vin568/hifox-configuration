package ctxmeter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CommandProbe 是通用逃生口（ADR 0030）：任何 harness 只要有一个命令能输出统一 JSON，就能
// 接入，不必改运行时。命令通过环境变量拿到 agent 与会话信息。
type CommandProbe struct {
	Command string
	Timeout time.Duration
}

func (p CommandProbe) Measure(ctx context.Context, t Target) (Snapshot, error) {
	cmdline := strings.TrimSpace(p.Command)
	if cmdline == "" {
		return Snapshot{Source: SourceUnknown}, errors.New("ctxmeter: context probe command 为空")
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", cmdline)
	cmd.Env = append(os.Environ(),
		"AGENT_NAME="+t.Name,
		"AGENT_ROLE="+t.Role,
		"AGENT_KIND="+t.Kind,
		"AGENT_STATUS="+t.Status,
		"SESSION_PATH="+t.SessionPath,
		"RESULT_PATH="+t.ResultPath,
		"BRIEF_PATH="+t.BriefPath,
		"PROGRESS_PATH="+t.ProgressPath,
	)
	out, err := cmd.Output()
	if err != nil {
		if cctx.Err() != nil {
			return Snapshot{Source: SourceUnknown}, fmt.Errorf("ctxmeter: probe 超时: %w", cctx.Err())
		}
		return Snapshot{Source: SourceUnknown}, fmt.Errorf("ctxmeter: probe 失败: %w", err)
	}
	return decodeCommandSnapshot(out)
}

type commandSnapshot struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	PromptTokens int64  `json:"prompt_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TailTokens   int64  `json:"tail_tokens"`
	WindowTokens int64  `json:"window_tokens"`
	Epoch        int    `json:"epoch"`
	Source       string `json:"source"`
}

func decodeCommandSnapshot(out []byte) (Snapshot, error) {
	var c commandSnapshot
	if err := json.Unmarshal(out, &c); err != nil {
		return Snapshot{Source: SourceUnknown}, fmt.Errorf("ctxmeter: 解析 probe 输出: %w", err)
	}
	s := Snapshot{
		Provider:     c.Provider,
		Model:        c.Model,
		PromptTokens: c.PromptTokens,
		OutputTokens: c.OutputTokens,
		TailTokens:   c.TailTokens,
		WindowTokens: c.WindowTokens,
		Epoch:        c.Epoch,
		Source:       c.Source,
		MeasuredAt:   time.Now(),
	}
	if s.Source == "" {
		s.Source = SourceMeasured
	}
	if s.PromptTokens <= 0 && s.OutputTokens <= 0 {
		s.Source = SourceUnknown
		return s, errors.New("ctxmeter: probe 没有返回可用 token 数")
	}
	return s, nil
}
