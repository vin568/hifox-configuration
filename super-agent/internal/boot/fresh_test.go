package boot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"super-agent/internal/herdr"
)

// freshFake 模拟 herdr：agent 在册/退出、按键、关 pane、以及「每次 start 都是新会话」。
type freshFake struct {
	agentRunning bool
	working      bool
	oldSession   string
	sessionSeq   int
	sameSession  bool // true 时 start 后仍报旧会话路径（模拟没换成脑）

	started     []string
	sentKeys    []string
	closedPanes []string
	prompted    []string
	sessionDir  string
	ignoreCtrlD bool // 模拟 ctrl+d 退不回去
	ignoreExit  bool // 模拟 /exit 也不生效，只能关 pane
}

func (f *freshFake) run(_ context.Context, args []string) (string, error) {
	switch strings.Join(args[:2], " ") {
	case "agent list":
		if !f.agentRunning {
			return `{"result":{"agents":[]}}`, nil
		}
		sess := f.oldSession
		if f.sessionSeq > 0 {
			if f.sameSession {
				sess = f.oldSession
			} else {
				sess = f.sessionPath()
			}
		}
		st := "idle"
		if f.working {
			st = "working"
		}
		return `{"result":{"agents":[{"agent":"omp","name":"songjiang","agent_status":"` + st + `","pane_id":"w11:p1","agent_session":{"source":"herdr:omp","value":"` + sess + `"}}]}}`, nil
	case "workspace list":
		ws, _ := fullTopo()
		return ws, nil
	case "api snapshot":
		_, snap := fullTopo()
		return snap, nil
	case "workspace create":
		return "", nil
	case "agent start":
		f.started = append(f.started, args[2])
		f.agentRunning = true
		f.sessionSeq++
		return `{"result":{"agent":{"agent":"omp","agent_status":"idle"}}}`, nil
	case "agent send-keys":
		f.sentKeys = append(f.sentKeys, args[2]+":"+strings.Join(args[3:], ","))
		if args[3] == "esc" {
			f.working = false
			return `{"result":{"type":"ok"}}`, nil
		}
		if !f.ignoreCtrlD {
			f.agentRunning = false
		}
		return `{"result":{"type":"ok"}}`, nil
	case "pane close":
		f.closedPanes = append(f.closedPanes, args[2])
		f.agentRunning = false
		return `{"result":{"type":"ok"}}`, nil
	case "agent prompt":
		if args[3] == "/exit" {
			if !f.ignoreExit {
				f.agentRunning = false
			}
			return `{}`, nil
		}
		f.prompted = append(f.prompted, args[3])
		if f.sessionSeq > 0 && !f.sameSession {
			if err := os.WriteFile(f.sessionPath(), []byte(args[3]), 0o644); err != nil {
				return "", err
			}
		}
		return `{}`, nil
	}
	return "", nil
}

func (f *freshFake) sessionPath() string {
	if f.sessionDir == "" {
		d, err := os.MkdirTemp("", "fresh-session")
		if err != nil {
			return ""
		}
		f.sessionDir = d
	}
	return filepath.Join(f.sessionDir, fmt.Sprintf("s%d.jsonl", f.sessionSeq))
}

func freshClient(f *freshFake) *herdr.Client { return &herdr.Client{Run: f.run} }

func freshExp(t *testing.T) Expected {
	t.Helper()
	return Expected{
		RepoDir: "/repo", MasterAgent: "songjiang", RetroAgent: "jiangjing",
		Kind: "omp", DynamicWS: "jobs", MasterPrime: "prime-master", RetroPrime: "prime-retro",
		PrimeMarkerDir: t.TempDir(),
	}
}

func withFastPoll(t *testing.T) {
	t.Helper()
	oldTimeout, oldPoll := FreshTimeout, FreshPoll
	FreshTimeout, FreshPoll = 60*time.Millisecond, time.Millisecond
	t.Cleanup(func() { FreshTimeout, FreshPoll = oldTimeout, oldPoll })
}

func TestFreshStopsAndStartsNewSession(t *testing.T) {
	withFastPoll(t)
	f := &freshFake{agentRunning: true, working: true, oldSession: "/sessions/old.jsonl"}
	rep, err := Fresh(context.Background(), freshClient(f), freshExp(t), "songjiang", "/state/sessions/round-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.StoppedVia != "slash-exit" {
		t.Fatalf("应走 /exit 优雅退出，得到 %q", rep.StoppedVia)
	}
	if rep.OldSession != "/sessions/old.jsonl" || rep.NewSession == "" || rep.NewSession == rep.OldSession {
		t.Fatalf("会话应更换: %+v", rep)
	}
	if !rep.SessionFresh || !rep.Primed {
		t.Fatalf("报告应标明换脑成功且已 prime: %+v", rep)
	}
	if len(f.prompted) == 0 || f.prompted[0] != "prime-master" {
		t.Fatalf("首条注入必须是 prime: %v", f.prompted)
	}
	// working 时必须先 esc 中断，再用 /exit 退出：omp 在跑的时候不理退出键/快捷键。
	if len(f.sentKeys) == 0 || f.sentKeys[0] != "songjiang:esc" {
		t.Fatalf("必须先 esc 中断: %v", f.sentKeys)
	}
}

func TestFreshFallsBackToPaneClose(t *testing.T) {
	withFastPoll(t)
	f := &freshFake{agentRunning: true, oldSession: "/sessions/old.jsonl", ignoreCtrlD: true, ignoreExit: true}
	rep, err := Fresh(context.Background(), freshClient(f), freshExp(t), "songjiang", "/state/sessions/round-2", "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.StoppedVia != "pane-close" {
		t.Fatalf("退不回去应关 pane，得到 %q", rep.StoppedVia)
	}
	if len(f.closedPanes) != 1 || f.closedPanes[0] != "w11:p1" {
		t.Fatalf("关掉的 pane 不对: %v", f.closedPanes)
	}
}

func TestFreshWithoutRunningAgentJustStarts(t *testing.T) {
	withFastPoll(t)
	f := &freshFake{}
	rep, err := Fresh(context.Background(), freshClient(f), freshExp(t), "songjiang", "/state/sessions/round-3", "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.StoppedVia != "absent" {
		t.Fatalf("无 agent 时应记 absent，得到 %q", rep.StoppedVia)
	}
	if len(f.sentKeys) != 0 {
		t.Fatalf("无 agent 时不该按键: %v", f.sentKeys)
	}
}

// 会话没换 = 换脑失败：绝不能静默当成成功（否则急停等于没停）。
func TestFreshFailsWhenSessionNotChanged(t *testing.T) {
	withFastPoll(t)
	f := &freshFake{agentRunning: true, oldSession: "/sessions/old.jsonl", sameSession: true}
	rep, err := Fresh(context.Background(), freshClient(f), freshExp(t), "songjiang", "/state/sessions/round-4", "")
	if err == nil {
		t.Fatalf("会话未更换必须报错，rep=%+v", rep)
	}
	if rep.SessionFresh {
		t.Fatal("报告不应标记为 fresh")
	}
}

// 自定义 prime 走附加意图（急停/轮换），不再用默认手册。
func TestFreshUsesGivenPrime(t *testing.T) {
	withFastPoll(t)
	f := &freshFake{agentRunning: true, oldSession: "/sessions/old.jsonl"}
	if _, err := Fresh(context.Background(), freshClient(f), freshExp(t), "songjiang", "/state/sessions/round-5", "急停意图正文"); err != nil {
		t.Fatal(err)
	}
	if len(f.prompted) == 0 || f.prompted[0] != "急停意图正文" {
		t.Fatalf("应使用传入的 prime: %v", f.prompted)
	}
}
