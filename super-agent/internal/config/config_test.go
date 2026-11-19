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

func TestPlatformInterval(t *testing.T) {
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
		c.PlatformPollInterval = tt.in
		d, err := c.PlatformInterval()
		if tt.wantErr {
			if err == nil {
				t.Fatalf("PlatformInterval(%q) = %v, want error", tt.in, d)
			}
			continue
		}
		if err != nil || d != tt.want {
			t.Fatalf("PlatformInterval(%q) = %v %v, want %v", tt.in, d, err, tt.want)
		}
	}
}

func TestValidatePlatformIntervalPrefixed(t *testing.T) {
	c := Default()
	c.PlatformPollInterval = "bogus"
	err := c.Validate()
	if err == nil || !strings.HasPrefix(err.Error(), "platform_poll_interval: ") {
		t.Fatalf("Validate should prefix platform_poll_interval error, got %v", err)
	}
}

// 平台哨兵快照换了名字：cloud 不再当 hifox 的同义词。
func TestPlatformSnapPath(t *testing.T) {
	c := Default()
	c.StateDir = "/state"
	if got := c.PlatformSnapPath(); got != filepath.Join("/state", "platform-snapshot.json") {
		t.Fatalf("PlatformSnapPath() = %q", got)
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

// 未知键必须报错（静默忽略会让「改了不生效」长期悬案）；两个已下线键单独处理，
// 由 loadConfigWithAgents 按「值是否等于注册表派生值」决定警告还是报错。
func TestLoadRejectsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/superctl.toml"
	body := "session = \"t\"\nstate_dir = \"" + dir + "\"\nunknown_knob = 1\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown_knob") {
		t.Fatalf("未知键必须报错并点名: %v", err)
	}
}

// 已下线的两个键仍可读（供调用方比对），不会让存量配置在 Load 阶段炸掉。
func TestLoadAcceptsLegacyAgentKeys(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/superctl.toml"
	body := "session = \"t\"\nstate_dir = \"" + dir + "\"\nmaster_agent = \"songjiang\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("旧键不应让 Load 失败: %v", err)
	}
	if c.LegacyMasterAgent != "songjiang" {
		t.Fatalf("旧键应可读: %+v", c)
	}
}

// cloud_poll_interval 是 platform_poll_interval 的旧名：旧键也得能在 Load 阶段读出来，
// 供调用方按「值是否等于新键」决定警告还是硬报错。
func TestLoadAcceptsLegacyCloudPollInterval(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/superctl.toml"
	body := "session = \"t\"\nstate_dir = \"" + dir + "\"\nplatform_poll_interval = \"30s\"\ncloud_poll_interval = \"30s\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("旧键不应让 Load 失败: %v", err)
	}
	if c.LegacyCloudPollInterval != "30s" || c.PlatformPollInterval != "30s" {
		t.Fatalf("新旧键都该可读: %+v", c)
	}
	if d, err := c.PlatformInterval(); err != nil || d != 30*time.Second {
		t.Fatalf("新键应生效: %v %v", d, err)
	}
}

// media_retention 是飞书落盘附件的保留期：默认 30 天，可覆盖，非法值 Validate 报错。
func TestMediaRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "superctl.toml")
	dir := filepath.Join(t.TempDir(), "state")
	toml := "session = \"s\"\nstate_dir = \"" + dir + "\"\nmedia_retention = \"48h\"\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := c.MediaRetention(); err != nil || d != 48*time.Hour {
		t.Fatalf("media_retention override lost: %v %v", d, err)
	}

	// 缺省 = 零值，交给适配层的 DefaultMediaRetention（30 天只在一处定义）。
	def := Default()
	if d, err := def.MediaRetention(); err != nil || d != 0 {
		t.Fatalf("默认应为零值（适配层兜底）: %v %v", d, err)
	}

	bad := Default()
	bad.MediaRetentionRaw = "nope"
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "media_retention") {
		t.Fatalf("非法 media_retention 应报错: %v", err)
	}
}

func TestLoadContextMapsAndLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "superctl.toml")
	body := `session = "t"
state_dir = "` + filepath.Join(dir, "state") + `"
repo_dir = "` + dir + `"
context_model_db = "~/models.db"
context_warn_ratio = 0.6
[context_windows]
"hifox/deepseek-flash" = 1000000
[context_probes]
opencode = "probe --json"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.ContextWarnRatio != 0.6 || c.ContextWindows["hifox/deepseek-flash"] != 1000000 {
		t.Fatalf("context windows not loaded: %+v", c)
	}
	if c.ContextProbes["opencode"] != "probe --json" {
		t.Fatalf("context probes not loaded: %+v", c.ContextProbes)
	}
	if c.ContextModelDB == "~/models.db" || !strings.HasSuffix(c.ContextModelDB, "/models.db") {
		t.Fatalf("context_model_db should expand ~: %q", c.ContextModelDB)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	if _, err := Load("../../config.example.toml"); err != nil {
		t.Fatalf("config.example.toml 必须能被解析: %v", err)
	}
}

func TestContextConfigRejectsInvalidValues(t *testing.T) {
	base := "session = \"t\"\nstate_dir = \"/tmp/state\"\nrepo_dir = \"/tmp/repo\"\n"
	cases := []struct{ name, extra string }{
		{"warn zero", "context_warn_ratio = 0\n"},
		{"warn one", "context_warn_ratio = 1\n"},
		{"reserve negative", "context_reserve_ratio = -0.1\n"},
		{"reserve one", "context_reserve_ratio = 1\n"},
		{"reserve min negative", "context_reserve_min_tokens = -1\n"},
		{"stall negative", "local_stall_after = \"-1m\"\n"},
		{"stall invalid", "local_stall_after = \"nope\"\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "superctl.toml")
			if err := os.WriteFile(path, []byte(base+c.extra), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("expected validation error for %s", c.name)
			}
		})
	}
}
