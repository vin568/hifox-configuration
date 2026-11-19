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
	// LegacyCloudPollInterval 是 platform_poll_interval 的旧名（cloud 曾当 hifox 的同义词）；
	// 由 loadConfigWithAgents 按「值是否等于新键」决定警告还是报错。
	LegacyCloudPollInterval string `toml:"cloud_poll_interval"`

	TickInterval         string   `toml:"tick_interval"`
	AllowedUsers         []string `toml:"allowed_users"`
	ProxyReplyGroups     []string `toml:"proxy_reply_groups"`
	AnyMessageGroups     []string `toml:"any_message_groups"`
	BotMentionOpenID     string   `toml:"bot_open_id"`
	AdminChatID          string   `toml:"admin_chat_id"`
	PlatformPollInterval string   `toml:"platform_poll_interval"`
	MasterAgentArgs      []string `toml:"master_agent_args"`
	SessionMaxBytes      int64    `toml:"session_max_bytes"` // unknown 时的字节兜底（0 = 关）；主判定改用 context_* 比例
	SessionsKeep         int      `toml:"sessions_keep"`     // 会话留档目录保留轮数（0 = 默认 5）
	MediaRetentionRaw    string   `toml:"media_retention"`   // 飞书落盘附件保留期（空 = 适配层默认 30 天）

	// 上下文度量（ADR 0030）：主判定用 provider 回报的 token + 模型窗口；字节数只
	// 在测量不可用时兜底。保留区策略见 ctxmeter.Policy。
	ContextWarnRatio        float64           `toml:"context_warn_ratio"`
	ContextReserveRatio     float64           `toml:"context_reserve_ratio"`
	ContextReserveMinTokens int64             `toml:"context_reserve_min_tokens"`
	ContextWarnMarginTokens int64             `toml:"context_warn_margin_tokens"`
	ContextProbes           map[string]string `toml:"context_probes"`    // kind -> 通用探针命令（omp 内置）
	ContextWindows          map[string]int64  `toml:"context_windows"`   // "provider/model" 或 "model" -> contextWindow
	ContextModelDB          string            `toml:"context_model_db"`  // omp models.db；空 = ~/.omp/agent/models.db
	LocalStallAfterRaw      string            `toml:"local_stall_after"` // working 但会话文件多久没写入算卡死（0 = 关）
}

func Default() Config {
	return Config{
		Session:              "super-agent",
		StateDir:             "~/.local/state/songjiang",
		RepoDir:              "~/code/apifox/hifox-configuration-super-agent",
		TickInterval:         "10m",
		PlatformPollInterval: "60s",
		// 不设 media_retention：空 = 走 feishu.DefaultMediaRetention（30 天），
		// 30 天只在一处定义，改一处不会分叉。
		SessionMaxBytes: 8 << 20, // unknown 时的兜底；主判定见 context_* 比例
		SessionsKeep:    5,

		ContextWarnRatio:        0.70,
		ContextReserveRatio:     0.15,
		ContextReserveMinTokens: 16000,
		ContextWarnMarginTokens: 8000,
		LocalStallAfterRaw:      "45m",
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
	c.ContextModelDB = ExpandPath(c.ContextModelDB)
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
	if _, err := c.PlatformInterval(); err != nil {
		return fmt.Errorf("platform_poll_interval: %w", err)
	}
	if _, err := c.SweepInterval(); err != nil {
		return fmt.Errorf("tick_interval: %w", err)
	}
	if _, err := c.MediaRetention(); err != nil {
		return fmt.Errorf("media_retention: %w", err)
	}
	if _, err := c.LocalStallAfter(); err != nil {
		return fmt.Errorf("local_stall_after: %w", err)
	}
	if c.ContextWarnRatio <= 0 || c.ContextWarnRatio >= 1 {
		return fmt.Errorf("context_warn_ratio 必须在 (0,1) 内")
	}
	if c.ContextReserveRatio < 0 || c.ContextReserveRatio >= 1 {
		return fmt.Errorf("context_reserve_ratio 必须在 [0,1) 内")
	}
	if c.ContextReserveMinTokens < 0 {
		return fmt.Errorf("context_reserve_min_tokens 不能为负")
	}
	return nil
}

// MediaRetention 是飞书落盘附件的保留期（默认 30 天，见 ADR 0024）。
func (c Config) MediaRetention() (time.Duration, error) {
	if strings.TrimSpace(c.MediaRetentionRaw) == "" {
		return 0, nil // 适配层按默认 30 天处理
	}
	return parsePositiveDuration("media_retention", c.MediaRetentionRaw)
}

// LocalStallAfter 解析本地作业的停滞判定阈值；空或 0 = 关闭。
func (c Config) LocalStallAfter() (time.Duration, error) {
	if strings.TrimSpace(c.LocalStallAfterRaw) == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(c.LocalStallAfterRaw)
	if err != nil {
		return 0, fmt.Errorf("local_stall_after %q: %w", c.LocalStallAfterRaw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("local_stall_after %q must not be negative", c.LocalStallAfterRaw)
	}
	return d, nil
}

// SweepInterval 解析巡检周期。
func (c Config) SweepInterval() (time.Duration, error) {
	return parsePositiveDuration("tick_interval", c.TickInterval)
}

// PlatformInterval 解析平台哨兵轮询周期（纯 CLI 轮询，零 LLM 成本）。
func (c Config) PlatformInterval() (time.Duration, error) {
	return parsePositiveDuration("platform_poll_interval", c.PlatformPollInterval)
}

func (c Config) InflightPath() string { return filepath.Join(c.StateDir, "inflight.json") }

// LegacyWatchlistPath 是改名前的平台跟踪清单；只在 inflight.json 尚未生成时用作迁移源。
func (c Config) LegacyWatchlistPath() string { return filepath.Join(c.StateDir, "watchlist.json") }
func (c Config) HaltPath() string            { return filepath.Join(c.StateDir, "halt.json") }
func (c Config) SessionsDir() string         { return filepath.Join(c.StateDir, "sessions") }
func (c Config) HandoffPath() string         { return filepath.Join(c.StateDir, "handoff.md") }
func (c Config) PlatformSnapPath() string    { return filepath.Join(c.StateDir, "platform-snapshot.json") }
func (c Config) ProgressDir() string         { return filepath.Join(c.StateDir, "progress") }
func (c Config) HandoffsDir() string         { return filepath.Join(c.StateDir, "handoffs") }
func (c Config) ContextAlertsPath() string   { return filepath.Join(c.StateDir, "context-alerts.json") }
func (c Config) SupervisionPath() string     { return filepath.Join(c.StateDir, "supervision.json") }

// LegacySnapPath 是改名前的哨兵快照（cloud 曾当 hifox 的同义词）。只在新的
// platform-snapshot.json 还没生成时作迁移基线：否则升级后首轮会重打基线、丢掉变化。
func (c Config) LegacySnapPath() string { return filepath.Join(c.StateDir, "cloud-snapshot.json") }
func (c Config) PrimeMarkerDir() string { return filepath.Join(c.StateDir, "primed") }
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

// BriefsDir 存平台任务正文的归档副本（<任务号>.md）：正文由派活员整理、运行时在建单
// 后归档——主控复查「这单到底写了什么」读这里，不依赖平台的 web 界面（ADR 0024）。
func (c Config) BriefsDir() string { return filepath.Join(c.StateDir, "briefs") }

// SkillsDir 存「技能发现覆盖配置」的落点（每 agent 一份 omp-skills.yml）。
// 技能本体不在这里：真源就是 capabilities/ 下的 SKILL.md，直接交给底层 CLI 扫。
func (c Config) SkillsDir() string { return filepath.Join(c.StateDir, "skills") }

// MediaDir 存飞书消息里的图片/文件（注入文本里只出现路径，主控据此带附件派活）。
func (c Config) MediaDir() string   { return filepath.Join(c.StateDir, "media") }
func (c Config) OutboxPath() string { return filepath.Join(c.StateDir, "outbox.jsonl") }
func (c Config) DedupPath() string  { return filepath.Join(c.StateDir, "dedup.jsonl") }
func (c Config) DirectDir() string  { return filepath.Join(c.RepoDir, "super-agent", "direct") }

// EnsureDirs 建好运行时状态目录（台账 active/archived、结果、正文、会话留档、父目录）。
// 附件目录不在这里建：它由 mediaStore 自己按 0700 建（用户原图不该混进 0755 的目录树）。
func (c Config) EnsureDirs() error {
	for _, d := range []string{c.ActiveDir(), c.ArchivedDir(), c.ResultsDir(), c.BriefsDir(), c.ProgressDir(), c.HandoffsDir(), c.SkillsDir(), c.SessionsDir(), filepath.Dir(c.OutboxPath())} {
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
