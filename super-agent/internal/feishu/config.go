// Package feishu adapts Feishu (Lark) messaging to the songjiang orchestrator:
// inbound messages arrive over the official long-connection (larkws), are
// filtered by hard rules (zero LLM), deduplicated on disk, and injected into
// the orchestrator as prompts; replies are sent back through the OpenAPI im/v1
// text endpoint on behalf of the outbox drain.
package feishu

import (
	"context"
	"fmt"
	"os"
	"time"
)

// DefaultDedupPath is the two-phase dedup journal location.
const DefaultDedupPath = "~/.local/state/songjiang/dedup.jsonl"

// DefaultDedupExpire reclaims claim lines that never got a done line after
// this duration (crash recovery).
const DefaultDedupExpire = 60 * time.Second

// Config is everything the adapter needs. AppID/AppSecret default to the
// SONGJIANG_FEISHU_* environment variables when left empty by the caller.
type Config struct {
	AppID     string
	AppSecret string

	// AllowedUserIDs is the whitelist of open_id/user_id values. DMs are only
	// admitted for these senders, and in ProxyReplyGroups a message that @-mentions
	// any of them is admitted as a proxy reply.
	AllowedUserIDs []string

	// ProxyReplyGroups maps chat_id -> enabled. In these groups a message
	// mentioning a whitelisted user is admitted with proxyReply=true.
	ProxyReplyGroups map[string]bool

	// AnyMessageGroups lists chat_ids where every message is admitted.
	AnyMessageGroups []string

	// BotMentionOpenID identifies this bot in mentions; when set, a group
	// message that @-mentions the bot is admitted. Empty = no special handling.
	BotMentionOpenID string

	// DedupPath overrides the dedup journal location (tests use a tmpdir path).
	DedupPath string

	// DedupExpire reclaims claim lines that never got a done line after this
	// duration. Zero means DefaultDedupExpire.
	DedupExpire time.Duration

	// MediaDir 是消息附件（图片/文件）的落盘根目录；空 = 不下载，注入文本里只留
	// key 占位（探针、单测与没配状态目录的调用方走这条路）。
	MediaDir string

	// MediaRetention 是落盘附件的保留期，零值用 DefaultMediaRetention。
	MediaRetention time.Duration

	// CommandFunc handles白名单私聊里以 "/" 开头的指令（/halt、/new）。返回
	// handled=true 表示已消化，绝不再作为消息注入主控——急停这类指令不能排在
	// 错误指令后面等主控自己读。
	CommandFunc func(ctx context.Context, chatID, senderID, text string) bool
}

// CredentialsFromEnv reads the Feishu app credentials from
// SONGJIANG_FEISHU_APP_ID / SONGJIANG_FEISHU_APP_SECRET — the only place
// secrets may come from（密钥卫生：不进配置文件）。
func CredentialsFromEnv() (appID, appSecret string) {
	return os.Getenv("SONGJIANG_FEISHU_APP_ID"), os.Getenv("SONGJIANG_FEISHU_APP_SECRET")
}

// LoadConfigFromEnv fills empty credential fields from
// SONGJIANG_FEISHU_APP_ID / SONGJIANG_FEISHU_APP_SECRET.
func LoadConfigFromEnv(cfg Config) (Config, error) {
	if cfg.AppID == "" || cfg.AppSecret == "" {
		id, secret := CredentialsFromEnv()
		if cfg.AppID == "" {
			cfg.AppID = id
		}
		if cfg.AppSecret == "" {
			cfg.AppSecret = secret
		}
	}
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return cfg, fmt.Errorf("feishu: missing credentials: set Config.AppID/AppSecret or SONGJIANG_FEISHU_APP_ID/SONGJIANG_FEISHU_APP_SECRET")
	}
	return cfg, nil
}

// allowedSet returns the whitelist as a set.
func (c Config) allowedSet() map[string]bool {
	m := make(map[string]bool, len(c.AllowedUserIDs))
	for _, id := range c.AllowedUserIDs {
		m[id] = true
	}
	return m
}

// allow is the pure three-layer admission rule.
//
//  1. DM (p2p) and sender whitelisted -> admit.
//  2. Group and @-mentions the bot (BotMentionOpenID set and in mentions) -> admit.
//  3. Group in ProxyReplyGroups and mentions contain a whitelisted user -> admit, proxyReply.
//  4. Group in AnyMessageGroups -> admit.
//
// Everything else is dropped. Order matters: a proxy-mention in an
// AnyMessageGroup reports proxyReply=false (the group would have admitted it
// anyway, so the mention is not the reason it arrived).
func (c Config) allow(chatType, chatID, senderID string, mentionedIDs []string) (ok bool, proxyReply bool) {
	if chatType == "p2p" {
		return c.allowedSet()[senderID], false
	}
	if chatType != "group" {
		return false, false
	}
	// Proxy check first so a message that mentions both the bot and a
	// whitelisted user reports proxyReply=true.
	if c.ProxyReplyGroups[chatID] {
		allowed := c.allowedSet()
		for _, id := range mentionedIDs {
			if allowed[id] {
				return true, true
			}
		}
	}
	if c.BotMentionOpenID != "" && contains(mentionedIDs, c.BotMentionOpenID) {
		return true, false
	}
	for _, id := range c.AnyMessageGroups {
		if id == chatID {
			return true, false
		}
	}
	return false, false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
