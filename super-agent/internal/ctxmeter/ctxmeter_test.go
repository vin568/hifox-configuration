package ctxmeter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotFormulaAndPolicy(t *testing.T) {
	p := DefaultPolicy()
	if got := p.Ceiling(1000000); got != 850000 {
		t.Fatalf("ceiling(1M)=%d, want 850000", got)
	}
	if got := p.Ceiling(200000); got != 170000 {
		t.Fatalf("ceiling(200k)=%d, want 170000", got)
	}
	if got := p.WarnAt(1000000); got != 700000 {
		t.Fatalf("warn(1M)=%d, want 700000", got)
	}
	s := Snapshot{PromptTokens: 100, OutputTokens: 20, TailTokens: 5, WindowTokens: 1000, Source: SourceMeasured}
	if s.Used() != 125 || !s.Known() || s.Ratio() != 0.125 {
		t.Fatalf("snapshot=%+v used=%d ratio=%v", s, s.Used(), s.Ratio())
	}
	if (Snapshot{Source: SourceUnknown, PromptTokens: 10, WindowTokens: 100}).Known() {
		t.Fatal("unknown snapshot must not be known")
	}
}

func TestOMPProbeLatestSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"message","timestamp":"2026-11-19T10:00:00Z","message":{"role":"user","content":[]}}`,
		`{"type":"message","timestamp":"2026-11-19T10:00:01Z","message":{"role":"assistant","model":"old","provider":"hifox","usage":{"input":1,"output":2,"cacheRead":3}}}`,
		`{"type":"message","timestamp":"2026-11-19T10:00:02Z","message":{"role":"assistant","model":"deepseek-flash","provider":"hifox","contextSnapshot":{"promptTokens":100000,"compactionEpoch":2},"usage":{"input":1,"output":30,"cacheRead":2000,"cacheWrite":0,"totalTokens":2031}}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := (OMPProbe{}).Measure(context.Background(), Target{SessionPath: path})
	if err != nil {
		t.Fatal(err)
	}
	// promptTokens 与 usage 的 input+cache 故意不同：必须优先用 contextSnapshot，
	// 否则「先看 contextSnapshot、没有才求和」这条核心分支不可测。
	if s.PromptTokens != 100000 || s.OutputTokens != 30 || s.Epoch != 2 || s.Used() != 100030 {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	if s.Model != "deepseek-flash" || s.Provider != "hifox" || s.MeasuredAt.IsZero() {
		t.Fatalf("metadata missing: %+v", s)
	}
	if s.Known() { // window is filled by resolver; raw probe is not known
		t.Fatal("raw probe should not be known without window")
	}
}

func TestOMPProbeFallbackUsageAndTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	prefix := strings.Repeat(`{"type":"message","message":{"role":"user","content":"`+strings.Repeat("x", 1000)+`"}}`+"\n", 20)
	last := `{"type":"message","message":{"role":"assistant","model":"hifox/deepseek-flash","usage":{"input":100,"output":7,"cacheRead":20,"cacheWrite":3}}}` + "\n"
	if err := os.WriteFile(path, []byte(prefix+last), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := (OMPProbe{MaxTailBytes: 64 << 10}).Measure(context.Background(), Target{SessionPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if s.PromptTokens != 123 || s.OutputTokens != 7 || s.Provider != "hifox" || s.Model != "deepseek-flash" {
		t.Fatalf("unexpected fallback snapshot: %+v", s)
	}
}

func TestOMPProbeMissingIsUnknown(t *testing.T) {
	_, err := (OMPProbe{}).Measure(context.Background(), Target{SessionPath: filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Fatal("expected error")
	}
	if s, _ := (OMPProbe{}).Measure(context.Background(), Target{}); s.Source != SourceUnknown {
		t.Fatalf("empty target source=%q, want unknown", s.Source)
	}
}

func TestResolverOverridesAndModelDB(t *testing.T) {
	r := &Resolver{Overrides: map[string]int64{"hifox/deepseek-flash": 1000000}}
	if w, ok := r.Window(context.Background(), "hifox", "deepseek-flash"); !ok || w != 1000000 {
		t.Fatalf("override=%d ok=%v", w, ok)
	}
	db := &ModelDB{Run: func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 0 || !strings.Contains(args[0], "hifox") {
			t.Fatalf("unexpected args: %v", args)
		}
		return []byte(`[{"id":"deepseek-flash","provider":"hifox","contextWindow":1000000}]` + "\n"), nil
	}}
	r = &Resolver{DB: db}
	if w, ok := r.Window(context.Background(), "hifox", "deepseek-flash"); !ok || w != 1000000 {
		t.Fatalf("db window=%d ok=%v", w, ok)
	}
	// 模型目录优先于配置覆盖：覆盖只补目录查不到的模型。
	r = &Resolver{DB: db, Overrides: map[string]int64{"hifox/deepseek-flash": 500}}
	if w, _ := r.Window(context.Background(), "hifox", "deepseek-flash"); w != 1000000 {
		t.Fatalf("db must win over override, got %d", w)
	}
}

func TestCommandProbe(t *testing.T) {
	p := CommandProbe{Command: `printf '{"provider":"hifox","model":"deepseek-flash","prompt_tokens":100,"output_tokens":5,"window_tokens":1000}'`}
	s, err := p.Measure(context.Background(), Target{Name: "worker-1", Role: "job", Kind: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if s.PromptTokens != 100 || s.OutputTokens != 5 || s.WindowTokens != 1000 || !s.Known() {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
}

func TestMeasurerFillsWindowAndUnknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	line := `{"type":"message","message":{"role":"assistant","model":"deepseek-flash","provider":"hifox","contextSnapshot":{"promptTokens":100},"usage":{"output":5}}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Measurer{
		Probes:   map[string]Probe{"omp": OMPProbe{}},
		Resolver: &Resolver{Overrides: map[string]int64{"hifox/deepseek-flash": 1000}},
	}
	s, err := m.Measure(context.Background(), Target{Kind: "omp", SessionPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if s.WindowTokens != 1000 || !s.Known() || s.Used() != 105 {
		t.Fatalf("unexpected measured snapshot: %+v", s)
	}
	if _, err := m.Measure(context.Background(), Target{Kind: "opencode"}); err == nil {
		t.Fatal("expected no-probe error")
	}
}

func TestReadTailDropsPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("0123456789\nabcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := readTail(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "abcdef\n" {
		t.Fatalf("tail=%q", data)
	}
}

func TestPolicyDegenerateWindows(t *testing.T) {
	p := DefaultPolicy()
	// 窗口比保留区还小：不能算出 0 让 agent 永远不触发。
	for _, window := range []int64{1, 100, 1000, 20000} {
		ceiling := p.Ceiling(window)
		if ceiling < 1 || ceiling > window {
			t.Fatalf("window=%d ceiling=%d out of range", window, ceiling)
		}
		warn := p.WarnAt(window)
		if warn < 1 || warn > ceiling {
			t.Fatalf("window=%d warn=%d ceiling=%d out of range", window, warn, ceiling)
		}
	}
	if got := p.Ceiling(0); got != 0 {
		t.Fatalf("unknown window must yield 0, got %d", got)
	}
	// margin 大于整个 ceiling 时也不能算成 0。
	tight := Policy{WarnRatio: 0.9, ReserveRatio: 0.1, ReserveMinTokens: 100, WarnMarginTokens: 1 << 30}
	if w := tight.WarnAt(1000); w < 1 {
		t.Fatalf("tight policy warn=%d, want >=1", w)
	}
}

func TestOMPTailWindowsCoversWholeFileWhenSmallerThanMax(t *testing.T) {
	// 文件比最小窗口还小：应读整个文件，而不是截成 max。
	got := ompTailWindows(1000, 8<<20)
	if len(got) != 1 || got[0] != 1000 {
		t.Fatalf("windows=%v, want [1000]", got)
	}
	got = ompTailWindows(5<<20, 8<<20)
	if got[len(got)-1] != 5<<20 {
		t.Fatalf("last window=%d, want whole file %d", got[len(got)-1], 5<<20)
	}
}
