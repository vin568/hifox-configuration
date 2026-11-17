// Package halt 承载急停闸门：一个机器可读状态文件（ADR 0020）。serve 每次注入前
// 检查它；只有人的私聊与运行时的急停意图能穿过闸门。台账只记叙事，当不了闸门——
// serve 从不读台账，而且要被拦住的正是被停的主控自己。
package halt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State 是一次急停的记录：谁按的、什么时候、为什么（可选）、当时在哪两个会话之间换脑。
type State struct {
	At         time.Time `json:"at"`
	By         string    `json:"by"`               // 触发者（飞书 chat/open id 或 "cli"）
	Reason     string    `json:"reason,omitempty"` // 口令里带的原因，可为空
	OldSession string    `json:"old_session,omitempty"`
	NewSession string    `json:"new_session,omitempty"`
	Receipt    string    `json:"receipt,omitempty"` // 秒级回执正文（机器真值清单），便于排查
}

// Active 判定闸门是否挂上：文件存在即已挂闸（损坏也按已挂闸处理——fail-closed，
// 宁可停住也不让一次坏写自动解除急停）。
func Active(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Load 读状态；文件不存在返回 ok=false。
func Load(path string) (State, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("read halt state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// 坏文件不能让闸门失效：宁可当作已挂闸（fail-closed），并保留原文件供排查。
		return State{}, true, fmt.Errorf("parse halt state %s: %w", path, err)
	}
	return s, true, nil
}

// Set 落闸（覆盖旧状态）；原子写：先写临时文件再 rename，避免半截文件。
func Set(path string, s State) error {
	if s.At.IsZero() {
		s.At = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Clear 解除闸门；文件不存在也算成功（幂等）。
func Clear(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Grace 是「解除之后仍然丢弃更早排队指令」的窗口：飞书重试的最长退避是 5 分钟、
// 8 次尝试的总窗口约半小时，所以解除后这段时间内，落闸前就排队的消息仍必须丢。
const Grace = time.Hour

// Watermark 记录最近一次急停的落闸时刻与解除时刻。没有它，解除闸门的那一刻
// 重试队列里「停前那条」就会畅通无阻——闸门判定不能只看「文件在不在」。
type Watermark struct {
	HaltAt     time.Time `json:"halt_at"`
	ReleasedAt time.Time `json:"released_at"`
}

func watermarkPath(haltPath string) string { return haltPath + ".released" }

// Release 解除闸门并记录水位；解除动作本身要能失败（失败就不解除）。
func Release(haltPath string, w Watermark) error {
	if w.ReleasedAt.IsZero() {
		w.ReleasedAt = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(haltPath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(watermarkPath(haltPath), append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Remove(haltPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Stale 判定一条注入是否属于「落闸前就排队」的旧指令：必须在解除后的 Grace 窗口内
// 才算，窗口外不追溯（否则一次急停会让很久以前的重新投递永远失效）。
func Stale(haltPath string, receivedAt, now time.Time) bool {
	if receivedAt.IsZero() {
		return false
	}
	data, err := os.ReadFile(watermarkPath(haltPath))
	if err != nil {
		return false
	}
	var w Watermark
	if err := json.Unmarshal(data, &w); err != nil {
		return false
	}
	return receivedAt.Before(w.HaltAt) && now.Sub(w.ReleasedAt) < Grace
}
