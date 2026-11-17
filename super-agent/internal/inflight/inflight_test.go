package inflight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "inflight.json")
}

func TestLoadMissingIsEmpty(t *testing.T) {
	entries, err := Load(filepath.Join(t.TempDir(), "nope", "inflight.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := tmpPath(t)
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	in := []Entry{
		{Kind: "cloud", TaskID: "HF-1", Note: "派白小浅", AddedAt: at},
		{Kind: "local", Agent: "jiangjing", Pane: "w1:p2", AddedAt: at},
	}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	// 缩进 + 换行收尾，便于人读 diff。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if raw[len(raw)-1] != '\n' || len(raw) < 10 || raw[1] != '\n' {
		t.Fatalf("not indented json: %q", raw)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].TaskID != "HF-1" || out[1].Agent != "jiangjing" || out[1].Pane != "w1:p2" {
		t.Fatalf("out=%+v", out)
	}
	if !out[0].AddedAt.Equal(at) {
		t.Fatalf("added_at=%v", out[0].AddedAt)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind")
	}
}

func TestAddDedupesSameKindSameID(t *testing.T) {
	path := tmpPath(t)
	at := time.Now()
	if err := Add(path, Entry{Kind: "cloud", TaskID: "HF-1", Note: "第一版", AddedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, Entry{Kind: "cloud", TaskID: "HF-1", Note: "覆盖", AddedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, Entry{Kind: "local", Agent: "jiangjing", Note: "a", AddedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := Add(path, Entry{Kind: "local", Agent: "jiangjing", Pane: "w1:p1", AddedAt: at}); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%+v", entries)
	}
	if entries[0].Note != "覆盖" {
		t.Fatalf("cloud note=%q", entries[0].Note)
	}
	if entries[1].Pane != "w1:p1" {
		t.Fatalf("local pane=%q", entries[1].Pane)
	}
}

// 同 kind 不同标识、不同 kind 同标识都不算重复。
func TestAddDistinctKeys(t *testing.T) {
	path := tmpPath(t)
	at := time.Now()
	for _, e := range []Entry{
		{Kind: "cloud", TaskID: "HF-1", AddedAt: at},
		{Kind: "cloud", TaskID: "HF-2", AddedAt: at},
		{Kind: "local", TaskID: "HF-1", Agent: "jiangjing", AddedAt: at},
	} {
		if err := Add(path, e); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestAddStampsAddedAt(t *testing.T) {
	path := tmpPath(t)
	if err := Add(path, Entry{Kind: "cloud", TaskID: "HF-9"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := Load(path)
	if len(entries) != 1 || entries[0].AddedAt.IsZero() {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestRemove(t *testing.T) {
	path := tmpPath(t)
	at := time.Now()
	for _, e := range []Entry{
		{Kind: "cloud", TaskID: "HF-1", AddedAt: at},
		{Kind: "cloud", TaskID: "HF-2", AddedAt: at},
		{Kind: "local", Agent: "jiangjing", AddedAt: at},
	} {
		if err := Add(path, e); err != nil {
			t.Fatal(err)
		}
	}
	n, err := Remove(path, func(e Entry) bool { return e.TaskID == "HF-1" })
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, err = Remove(path, func(e Entry) bool { return e.Kind == "local" })
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, err = Remove(path, func(e Entry) bool { return true })
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	entries, _ := Load(path)
	if len(entries) != 0 {
		t.Fatalf("entries=%+v", entries)
	}
	// 无匹配不改文件、不报错。
	n, err = Remove(path, func(Entry) bool { return true })
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestLoadMigrating(t *testing.T) {
	dir := t.TempDir()
	path, legacy := filepath.Join(dir, "inflight.json"), filepath.Join(dir, "watchlist.json")
	if err := os.WriteFile(legacy, []byte(`[{"task_id":"HF-1","note":"云端活"},{"task_id":"HF-2"},{"note":"没有任务号"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadMigrating(path, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%+v", entries)
	}
	for _, e := range entries {
		if e.Kind != "cloud" || e.AddedAt.IsZero() {
			t.Fatalf("entry=%+v", e)
		}
	}
	if entries[0].TaskID != "HF-1" || entries[0].Note != "云端活" {
		t.Fatalf("entry=%+v", entries[0])
	}
	// 迁移结果已落盘，下次直接读新文件。
	saved, err := Load(path)
	if err != nil || len(saved) != 2 {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	// 旧文件保持不动，哨兵仍可读。
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMigratingPrefersInflight(t *testing.T) {
	dir := t.TempDir()
	path, legacy := filepath.Join(dir, "inflight.json"), filepath.Join(dir, "watchlist.json")
	if err := Save(path, []Entry{{Kind: "local", Agent: "jiangjing", AddedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`[{"task_id":"HF-1"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadMigrating(path, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Agent != "jiangjing" {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestLoadMigratingNoFiles(t *testing.T) {
	dir := t.TempDir()
	entries, err := LoadMigrating(filepath.Join(dir, "inflight.json"), filepath.Join(dir, "watchlist.json"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
}

func TestLoadHealsCorrupt(t *testing.T) {
	path := tmpPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"not":"an array"`), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries=%+v", entries)
	}
	// 原文件被移走，坏内容另存在 .corrupt-<ts>。
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt file not healed")
	}
	asides, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(asides) != 1 {
		t.Fatalf("asides=%v err=%v", asides, err)
	}
	data, err := os.ReadFile(asides[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"not":"an array"` {
		t.Fatalf("aside=%q", data)
	}
	// 自愈后可继续写读。
	if err := Add(path, Entry{Kind: "cloud", TaskID: "HF-1"}); err != nil {
		t.Fatal(err)
	}
	if entries, err = Load(path); err != nil || len(entries) != 1 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
}

func TestCloudLocalFilter(t *testing.T) {
	at := time.Now()
	entries := []Entry{
		{Kind: "cloud", TaskID: "HF-1", AddedAt: at},
		{Kind: "local", Agent: "jiangjing", AddedAt: at},
		{Kind: "cloud", TaskID: "HF-2", AddedAt: at},
	}
	cloud, local := Cloud(entries), Local(entries)
	if len(cloud) != 2 || len(local) != 1 || local[0].Agent != "jiangjing" {
		t.Fatalf("cloud=%+v local=%+v", cloud, local)
	}
}

func TestSaveEmpty(t *testing.T) {
	path := tmpPath(t)
	if err := Save(path, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []Entry
	if err := json.Unmarshal(data, &got); err != nil || len(got) != 0 {
		t.Fatalf("data=%q err=%v", data, err)
	}
}
