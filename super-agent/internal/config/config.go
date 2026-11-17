package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config 是 superctl 的全部配置。飞书凭证不在此：读环境变量
// SONGJIANG_FEISHU_APP_ID / SONGJIANG_FEISHU_APP_SECRET（密钥卫生）。
type Config struct {
	Session  string `toml:"session"`
	StateDir string `toml:"state_dir"`
	RepoDir  string `toml:"repo_dir"`

	// MasterAgent / RetroAgent 不来自配置文件：由能力注册表（role=master|retro）派生，
	// 在 loadConfig 里填好，供各命令统一使用（ADR 0021）。
	MasterAgent string `toml:"-"`
	RetroAgent  string `toml:"-"`

	// 已下线键：只为了给存量配置一个可解释的过渡（值不一致时由调用方硬报错，
	// 一致时警告后忽略），不让「改了不生效」变成静默悬案，也不让升级直接把人打死。
	LegacyMasterAgent string `toml:"master_agent"`
	LegacyRetroAgent  string `toml:"retro_agent"`

	TickInterval      string   `toml:"tick_interval"`
	AllowedUsers      []string `toml:"allowed_users"`
	ProxyReplyGroups  []string `toml:"proxy_reply_groups"`
	AnyMessageGroups  []string `toml:"any_message_groups"`
	BotMentionOpenID  string   `toml:"bot_open_id"`
	AdminChatID       string   `toml:"admin_chat_id"`
	CloudPollInterval string   `toml:"cloud_poll_interval"`
	MasterAgentArgs   []string `toml:"master_agent_args"`
	SessionMaxBytes   int64    `toml:"session_max_bytes"` // 会话文件超过它且主控 idle 时换脑（0 = 关）
	SessionsKeep      int      `toml:"sessions_keep"`     // 会话留档目录保留轮数（0 = 默认 5）
}

func Default() Config {
	return Config{
		Session:           "super-agent",
		StateDir:          "~/.local/state/songjiang",
		RepoDir:           "~/code/apifox/hifox-configuration-super-agent",
		TickInterval:      "10m",
		CloudPollInterval: "60s",
		SessionMaxBytes:   8 << 20, // 8 MiB：约两千条消息；超过它就换脑重置上下文
		SessionsKeep:      5,
	}
}

// Load 用 TOML 覆盖默认值（缺键保留默认），展开 ~，校验必填项。
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	md, err := toml.Decode(string(data), &c)
	if err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		// 静默忽略未知键会让「改了配置却不生效」变成长期悬案；agent 名已改由能力注册表派生，
		// 旧键必须当场报出来。
		names := make([]string, 0, len(keys))
		for _, k := range keys {
			names = append(names, k.String())
		}
		return c, fmt.Errorf("config %s 含未知键 %s（可用键见 config.example.toml；agent 名已由能力注册表派生）",
			path, strings.Join(names, ", "))
	}
	c.StateDir = ExpandPath(c.StateDir)
	c.RepoDir = ExpandPath(c.RepoDir)
	return c, c.Validate()
}

func (c Config) Validate() error {
	var missing []string
	if c.Session == "" {
		missing = append(missing, "session")
	}
	if c.StateDir == "" {
		missing = append(missing, "state_dir")
	}
	if len(missing) > 0 {
		return fmt.Errorf("config missing: %s", strings.Join(missing, ", "))
	}
	if _, err := c.CloudInterval(); err != nil {
		return fmt.Errorf("cloud_poll_interval: %w", err)
	}
	if _, err := c.SweepInterval(); err != nil {
		return fmt.Errorf("tick_interval: %w", err)
	}
	return nil
}

// SweepInterval 解析巡检周期。
func (c Config) SweepInterval() (time.Duration, error) {
	return parsePositiveDuration("tick_interval", c.TickInterval)
}

// CloudInterval 解析云端哨兵轮询周期（纯 CLI 轮询，零 LLM 成本）。
func (c Config) CloudInterval() (time.Duration, error) {
	return parsePositiveDuration("cloud_poll_interval", c.CloudPollInterval)
}

func (c Config) InflightPath() string { return filepath.Join(c.StateDir, "inflight.json") }

// LegacyWatchlistPath 是改名前的云端跟踪清单；只在 inflight.json 尚未生成时用作迁移源。
func (c Config) LegacyWatchlistPath() string { return filepath.Join(c.StateDir, "watchlist.json") }
func (c Config) HaltPath() string            { return filepath.Join(c.StateDir, "halt.json") }
func (c Config) SessionsDir() string         { return filepath.Join(c.StateDir, "sessions") }
func (c Config) HandoffPath() string         { return filepath.Join(c.StateDir, "handoff.md") }
func (c Config) CloudSnapPath() string       { return filepath.Join(c.StateDir, "cloud-snapshot.json") }
func (c Config) PrimeMarkerDir() string      { return filepath.Join(c.StateDir, "primed") }
func (c Config) CapabilitiesDir() string {
	return filepath.Join(c.RepoDir, "super-agent", "capabilities")
}

// parsePositiveDuration 必须为正 duration：time.NewTicker 对非正值 panic、
// serve 进程会直接死，所以在这里拦下。
func parsePositiveDuration(field, val string) (time.Duration, error) {
	d, err := time.ParseDuration(val)
	if err != nil {
		return 0, fmt.Errorf("%s %q: %w", field, val, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s %q must be positive", field, val)
	}
	return d, nil
}

// Find 按优先级定位配置文件：显式路径 > 环境变量 > ./superctl.toml > ~/.config/songjiang/superctl.toml。
func Find(explicit string) (string, error) {
	paths := []string{}
	if explicit != "" {
		paths = append(paths, explicit)
	}
	if v := os.Getenv("SONGJIANG_CONFIG"); v != "" {
		paths = append(paths, v)
	}
	paths = append(paths, "superctl.toml", ExpandPath("~/.config/songjiang/superctl.toml"))
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("no config found (tried: %s); copy config.example.toml first", strings.Join(paths, ", "))
}

func (c Config) LedgerDir() string   { return filepath.Join(c.StateDir, "ledger") }
func (c Config) ActiveDir() string   { return filepath.Join(c.LedgerDir(), "active") }
func (c Config) ArchivedDir() string { return filepath.Join(c.LedgerDir(), "archived") }
func (c Config) ResultsDir() string  { return filepath.Join(c.StateDir, "results") }
func (c Config) OutboxPath() string  { return filepath.Join(c.StateDir, "outbox.jsonl") }
func (c Config) DedupPath() string   { return filepath.Join(c.StateDir, "dedup.jsonl") }
func (c Config) DirectDir() string   { return filepath.Join(c.RepoDir, "super-agent", "direct") }

// EnsureDirs 建好运行时状态目录（台账 active/archived、结果、会话留档、父目录）。
func (c Config) EnsureDirs() error {
	for _, d := range []string{c.ActiveDir(), c.ArchivedDir(), c.ResultsDir(), c.SessionsDir(), filepath.Dir(c.OutboxPath())} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// SessionKept 是留档轮数（0 或负值取默认 5）。
func (c Config) SessionKept() int {
	if c.SessionsKeep > 0 {
		return c.SessionsKeep
	}
	return 5
}

func ExpandPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}
