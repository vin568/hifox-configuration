package hifox

import (
	"context"
	"strings"
	"testing"
)

// 形状取自 hifox task view HF-11198 --output json 的实机输出（截 description）。
const taskJSON = `{
  "id": "dd416807-38b7-435d-adf2-97b562d886da",
  "identifier": "HF-11198",
  "title": "任务详情增加一个 AI 功能入口",
  "status": {"id": "s1", "name": "已发布", "category": "completed", "color": "#5e6ad2"},
  "priority": "none",
  "agentWorkingStatus": "none",
  "delegateStatus": "assigned",
  "updatedAt": "2026-11-05T10:29:27Z",
  "statusChangedAt": "2026-07-28T05:37:22Z"
}`

func TestDecodeTask(t *testing.T) {
	task, err := decodeTask(taskJSON)
	if err != nil {
		t.Fatal(err)
	}
	if task.Identifier != "HF-11198" || task.Status.Category != "completed" ||
		task.DelegateStatus != "assigned" || task.AgentWorkingStatus != "none" {
		t.Fatalf("unexpected task: %+v", task)
	}
	want := "completed/已发布|assigned|none"
	if got := task.Signature(); got != want {
		t.Fatalf("signature=%q want %q", got, want)
	}
}

func TestTaskViewPassesArgs(t *testing.T) {
	var got []string
	c := &Client{Run: func(_ context.Context, args []string) (string, error) {
		got = args
		return taskJSON, nil
	}}
	task, err := c.TaskView(context.Background(), "HF-1")
	if err != nil || task.Identifier != "HF-11198" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	if strings.Join(got, " ") != "task view HF-1 --output json" {
		t.Fatalf("args=%v", got)
	}
}
