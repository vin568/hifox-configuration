package halt

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetLoadClearRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "halt.json")
	if Active(path) {
		t.Fatal("未落闸时 Active 必须为 false")
	}
	st := State{At: time.Now(), By: "ou_alice", Reason: "派错了", OldSession: "a.jsonl"}
	if err := Set(path, st); err != nil {
		t.Fatal(err)
	}
	if !Active(path) {
		t.Fatal("落闸后 Active 必须为 true")
	}
	got, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.By != "ou_alice" || got.Reason != "派错了" || got.OldSession != "a.jsonl" {
		t.Fatalf("状态跑偏: %+v", got)
	}
	if err := Clear(path); err != nil {
		t.Fatal(err)
	}
	if Active(path) {
		t.Fatal("解除后 Active 必须为 false")
	}
	if err := Clear(path); err != nil {
		t.Fatalf("重复解除必须幂等: %v", err)
	}
}

// 坏文件不能把闸门打开：解析失败时 Active 保持 true（fail-closed），
// 否则一次坏写就等于自动解除急停。
func TestCorruptStateStaysHalted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "halt.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Active(path) {
		t.Fatal("坏文件必须按已落闸处理")
	}
	if _, ok, err := Load(path); err == nil || !ok {
		t.Fatalf("坏文件应报错且 ok=true: ok=%v err=%v", ok, err)
	}
}

func TestSetCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "halt.json")
	if err := Set(path, State{By: "cli"}); err != nil {
		t.Fatal(err)
	}
	if !Active(path) {
		t.Fatal("嵌套父目录应被创建")
	}
}
