package tick

import (
	"strings"
	"testing"
)

func TestSweepPrompt(t *testing.T) {
	got := SweepPrompt()
	// 注入卫生（ADR 0022）：样板与路径只在首条 prime 出现，注入文本只带增量。
	for _, want := range []string{"[触发器: sweep]", "活跃区台账", "一行带过"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sweep prompt missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "/") {
		t.Fatalf("巡检注入不应再带路径样板（路径归首条 prime）: %s", got)
	}
}

func TestNeeded(t *testing.T) {
	cases := []struct {
		name string
		s    Signals
		want bool
	}{
		{"全空不注入", Signals{}, false},
		{"有在途作业台账", Signals{ActiveJobs: 1}, true},
		{"有在途清单条目", Signals{Inflight: 2}, true},
		{"有活动 jobs agent", Signals{JobsAgents: 1}, true},
	}
	for _, c := range cases {
		if got := Needed(c.s); got != c.want {
			t.Errorf("%s: Needed(%+v)=%v，want %v", c.name, c.s, got, c.want)
		}
	}
}
