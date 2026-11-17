package tick

import (
	"strings"
	"testing"
)

func TestSweepPrompt(t *testing.T) {
	got := SweepPrompt("/active", "/archived")
	for _, want := range []string{"/active", "/archived", "hifox task view", "一行带过"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sweep prompt missing %q: %s", want, got)
		}
	}
	// 「读 /active」必须先于「移入 /archived」出现，顺序反了会读不到任何待归档条目。
	ia := strings.Index(got, "读 /active")
	ib := strings.Index(got, "移入 /archived")
	if ia < 0 || ib < 0 || ia >= ib {
		t.Fatalf("sweep prompt order wrong (read=%d archive=%d):\n%s", ia, ib, got)
	}
}
