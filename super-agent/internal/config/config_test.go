package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 巡检周期必须是正 duration：time.NewTicker 对非正值 panic，serve 进程
// 会直接死（生产路径用的就是 SweepInterval，不是 tick.ParseInterval）。
func TestSweepInterval(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"10m", 10 * time.Minute, false},
		{"30s", 30 * time.Second, false},
		{"0s", 0, true},
		{"-5m", 0, true},
		{"bogus", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		c := Default()
		c.TickInterval = tt.in
		d, err := c.SweepInterval()
		if tt.wantErr {
			if err == nil {
				t.Fatalf("SweepInterval(%q) = %v, want error", tt.in, d)
			}
			continue
		}
		if err != nil || d != tt.want {
			t.Fatalf("SweepInterval(%q) = %v %v, want %v", tt.in, d, err, tt.want)
		}
	}
}

func TestCloudInterval(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"60s", 60 * time.Second, false},
		{"", 0, true},
		{"0s", 0, true},
		{"-5s", 0, true},
		{"bogus", 0, true},
	}
	for _, tt := range tests {
		c := Default()
		c.CloudPollInterval = tt.in
		d, err := c.CloudInterval()
		if tt.wantErr {
			if err == nil {
				t.Fatalf("CloudInterval(%q) = %v, want error", tt.in, d)
			}
			continue
		}
		if err != nil || d != tt.want {
			t.Fatalf("CloudInterval(%q) = %v %v, want %v", tt.in, d, err, tt.want)
		}
	}
}

func TestValidateCloudIntervalPrefixed(t *testing.T) {
	c := Default()
	c.CloudPollInterval = "bogus"
	err := c.Validate()
	if err == nil || !strings.HasPrefix(err.Error(), "cloud_poll_interval: ") {
		t.Fatalf("Validate should prefix cloud_poll_interval error, got %v", err)
	}
}

func TestExpandPath(t *testing.T) {
	t.Setenv("HOME", "/home/songjiang")
	if got := ExpandPath("~/x"); got != "/home/songjiang/x" {
		t.Fatalf("ExpandPath(~/x) = %q", got)
	}
	if got := ExpandPath("/abs/path"); got != "/abs/path" {
		t.Fatalf("ExpandPath(/abs/path) = %q", got)
	}
	if got := ExpandPath("rel/path"); got != "rel/path" {
		t.Fatalf("ExpandPath(rel/path) = %q", got)
	}
}

func TestValidateMissingFields(t *testing.T) {
	c := Default()
	c.Session = ""
	c.StateDir = ""
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "session") || !strings.Contains(err.Error(), "state_dir") {
		t.Fatalf("Validate should name the missing fields, got %v", err)
	}
}

func TestLoadTomlOverrideAndExpansion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "superctl.toml")
	toml := `
session = "s1"
master_agent = "songjiang"
retro_agent = "jiangjing"
state_dir = "` + filepath.Join(t.TempDir(), "state") + `"
tick_interval = "30s"
allowed_users = ["ou_a", "ou_b"]
`
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Session != "s1" || len(c.AllowedUsers) != 2 || c.AllowedUsers[1] != "ou_b" {
		t.Fatalf("Load toml override wrong: %+v", c)
	}
	if d, err := c.SweepInterval(); err != nil || d != 30*time.Second {
		t.Fatalf("tick_interval override lost: %v %v", d, err)
	}
	if c.OutboxPath() != filepath.Join(c.StateDir, "outbox.jsonl") || c.DedupPath() != filepath.Join(c.StateDir, "dedup.jsonl") {
		t.Fatalf("state-dir derived paths wrong: %s %s", c.OutboxPath(), c.DedupPath())
	}
}
