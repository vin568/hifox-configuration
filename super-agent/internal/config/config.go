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
	Session           string   `toml:"session"`
	MasterAgent       string   `toml:"master_agent"`
	RetroAgent        string   `toml:"retro_agent"`
	StateDir          string   `toml:"state_dir"`
	RepoDir           string   `toml:"repo_dir"`
	TickInterval      string   `toml:"tick_interval"`
	AllowedUsers      []string `toml:"allowed_users"`
	ProxyReplyGroups  []string `toml:"proxy_reply_groups"`
	AnyMessageGroups  []string `toml:"any_message_groups"`
	BotMentionOpenID  string   `toml:"bot_open_id"`
	AdminChatID       string   `toml:"admin_chat_id"`
	CloudPollInterval string   `toml:"cloud_poll_interval"`
	MasterAgentArgs   []string `toml:"master_agent_args"`
}

func Default() Config {
	return Config{
		Session:           "super-agent",
		MasterAgent:       "songjiang",
		RetroAgent:        "jiangjing",
		StateDir:          "~/.local/state/songjiang",
		RepoDir:           "~/code/apifox/hifox-configuration-super-agent",
		TickInterval:      "10m",
		CloudPollInterval: "60s",
	}
}

// Load 用 TOML 覆盖默认值（缺键保留默认），展开 ~，校验必填项。
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := toml.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
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
	if c.MasterAgent == "" {
		missing = append(missing, "master_agent")
	}
	if c.RetroAgent == "" {
		missing = append(missing, "retro_agent")
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

func (c Config) WatchlistPath() string  { return filepath.Join(c.StateDir, "watchlist.json") }
func (c Config) CloudSnapPath() string  { return filepath.Join(c.StateDir, "cloud-snapshot.json") }
func (c Config) PrimeMarkerDir() string { return filepath.Join(c.StateDir, "primed") }

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
func (c Config) PromptsDir() string  { return filepath.Join(c.RepoDir, "super-agent", "prompts") }
func (c Config) DirectDir() string   { return filepath.Join(c.RepoDir, "super-agent", "direct") }

// EnsureDirs 建好运行时状态目录（台账 active/archived、结果、父目录）。
func (c Config) EnsureDirs() error {
	for _, d := range []string{c.ActiveDir(), c.ArchivedDir(), c.ResultsDir(), filepath.Dir(c.OutboxPath())} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
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
