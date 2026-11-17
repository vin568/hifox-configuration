package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSocketServer 在 HOME 下按 SocketPath 的真实推导规则起一个假 herdr socket。
type fakeSocketServer struct {
	listener *net.UnixListener
	received chan string // 收到的请求行
	script   func(c net.Conn)
}

func newFakeSocketServer(t *testing.T, session string, script func(net.Conn)) *fakeSocketServer {
	t.Helper()
	// macOS sun_path 上限 104 字符：t.TempDir() 的长后缀会 bind: invalid argument，
	// 用短基目录。
	base, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", base)
	t.Cleanup(func() { os.RemoveAll(base) })
	sock := SocketPath(session)
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	f := &fakeSocketServer{listener: l.(*net.UnixListener), received: make(chan string, 4), script: script}
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sc := bufio.NewScanner(conn)
		if sc.Scan() {
			f.received <- sc.Text()
		}
		script(conn)
	}()
	return f
}

func (f *fakeSocketServer) subscriptionFrame(t *testing.T) map[string]any {
	t.Helper()
	select {
	case line := <-f.received:
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("订阅帧不是 JSON: %v", err)
		}
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("没收到订阅帧")
		return nil
	}
}

func writeLine(t *testing.T, c net.Conn, s string) {
	t.Helper()
	if _, err := c.Write([]byte(s + "\n")); err != nil {
		t.Fatalf("write %q: %v", s, err)
	}
}

const subPushedEvent = `{"data":{"agent":"omp","agent_status":"working","pane_id":"w3:p1","workspace_id":"w3"},"event":"pane.agent_status_changed"}`

var testSubs = []Subscription{{"type": "pane.agent_status_changed", "pane_id": "w3:p1"}}

// 确认帧不当作事件；推送帧触发回调；对端干净关闭返回错误（外层走退避）。
func TestSubscribeOnceConfirmPushThenEOF(t *testing.T) {
	srv := newFakeSocketServer(t, "t1", func(c net.Conn) {
		writeLine(t, c, `{"id":"superctl_events","result":{"type":"subscription_started"}}`)
		writeLine(t, c, subPushedEvent)
		time.Sleep(50 * time.Millisecond)
		c.Close() // 干净 EOF
	})
	var events []Event
	err := subscribeOnce(context.Background(), "t1", testSubs, 5*time.Second, func(ev Event) error {
		events = append(events, ev)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("EOF 必须报错走退避, got %v", err)
	}
	if len(events) != 1 || events[0].Data.PaneID != "w3:p1" || events[0].Data.AgentStatus != "working" {
		t.Fatalf("events=%+v", events)
	}
	frame := srv.subscriptionFrame(t)
	if frame["method"] != "events.subscribe" {
		t.Fatalf("订阅帧 method=%v", frame["method"])
	}
}

// 服务端错误帧必须变成错误（订阅被拒要显式失败，不能静默空转）。
func TestSubscribeOnceErrorFrameFails(t *testing.T) {
	newFakeSocketServer(t, "t2", func(c net.Conn) {
		writeLine(t, c, `{"id":"superctl_events","error":{"code":"pane_not_found","message":"pane gone"}}`)
	})
	err := subscribeOnce(context.Background(), "t2", testSubs, 5*time.Second, func(Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "pane_not_found") {
		t.Fatalf("want pane_not_found error, got %v", err)
	}
}

// ttl 到点主动轮换返回 nil（外层重建订阅面）。
func TestSubscribeOnceTtlRotationReturnsNil(t *testing.T) {
	newFakeSocketServer(t, "t3", func(c net.Conn) {
		writeLine(t, c, `{"id":"superctl_events","result":{"type":"subscription_started"}}`)
		time.Sleep(1 * time.Second) // 比 ttl 长，保持连接开着
	})
	start := time.Now()
	err := subscribeOnce(context.Background(), "t3", testSubs, 150*time.Millisecond, func(Event) error { return nil })
	if err != nil {
		t.Fatalf("ttl 轮换应返回 nil, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("ttl 轮换不应等满服务端保持时长")
	}
}

// 回调返回错误时断开本条订阅连接（订阅循环交由外层决定）。
func TestSubscribeOnceCallbackErrorPropagates(t *testing.T) {
	newFakeSocketServer(t, "t4", func(c net.Conn) {
		writeLine(t, c, `{"id":"superctl_events","result":{"type":"subscription_started"}}`)
		writeLine(t, c, subPushedEvent)
		time.Sleep(1 * time.Second)
	})
	err := subscribeOnce(context.Background(), "t4", testSubs, 5*time.Second, func(Event) error {
		return os.ErrClosed
	})
	if err == nil {
		t.Fatal("回调错误必须传播")
	}
}
