package hifox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// Runner 执行一条 hifox 命令返回 stdout（与 internal/herdr 同模式）。
type Runner func(ctx context.Context, args []string) (string, error)

func ExecRunner() Runner {
	return func(ctx context.Context, args []string) (string, error) {
		var out, errb bytes.Buffer
		cmd := exec.CommandContext(ctx, "hifox", args...)
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

func New() *Client { return &Client{Run: ExecRunner()} }

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
