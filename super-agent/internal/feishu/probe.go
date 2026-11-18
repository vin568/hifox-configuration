package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// BotOpenID 取 bot 自己的 open_id（GET /open-apis/bot/v3/info），
// 即配置项 bot_open_id 要填的值（识别群内 @bot）。
func BotOpenID(ctx context.Context, cfg Config) (string, error) {
	cfg, err := LoadConfigFromEnv(cfg)
	if err != nil {
		return "", err
	}
	client := lark.NewClient(cfg.AppID, cfg.AppSecret)
	resp, err := client.Get(ctx, "/open-apis/bot/v3/info", nil, larkcore.AccessTokenTypeTenant)
	if err != nil {
		return "", fmt.Errorf("feishu bot info: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("feishu bot info: http %d: %s", resp.StatusCode, truncateStr(string(resp.RawBody), 200))
	}
	var payload struct {
		Code int `json:"code"`
		Bot  struct {
			OpenID string `json:"open_id"`
		} `json:"bot"`
	}
	if err := json.Unmarshal(resp.RawBody, &payload); err != nil {
		return "", fmt.Errorf("feishu bot info: decode: %w", err)
	}
	if payload.Code != 0 {
		return "", fmt.Errorf("feishu bot info: code=%d: %s", payload.Code, truncateStr(string(resp.RawBody), 200))
	}
	if payload.Bot.OpenID == "" {
		return "", fmt.Errorf("feishu bot info: empty open_id")
	}
	return payload.Bot.OpenID, nil
}

// Probe 像 Run 一样保持长连接，但只打印：每条入站事件一行，带全部配置需要的
// ID（chat_id / 发送者 / mentions / message_id）与文本预览。这是填
// allowed_users、*_groups、admin_chat_id 的引导工具——不过滤、不去重、
// 不注入、不回帖。
//
// verbose=true 时 SDK 日志开到 Debug（能看到连接与每一条 frame），
// 默认 Info（能看到连接生命周期）。
func Probe(ctx context.Context, cfg Config, verbose bool) error {
	cfg, err := LoadConfigFromEnv(cfg)
	if err != nil {
		return err
	}
	level := larkcore.LogLevelInfo
	if verbose {
		level = larkcore.LogLevelDebug
	}
	d := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
			printProbeEvent(event)
			return nil
		})
	cli := larkws.NewClient(cfg.AppID, cfg.AppSecret,
		larkws.WithEventHandler(d),
		larkws.WithLogLevel(level))
	slog.Info("feishu: probe connecting (long connection); if nothing follows, the connection never came up")
	keepConnected(ctx, cli, "feishu: probe")
	return ctx.Err()
}

func printProbeEvent(event *larkim.P2MessageReceiveV1) {
	ev := parseEvent(event)
	if ev == nil {
		return
	}
	var mentions []string
	for _, m := range ev.mentions {
		mentions = append(mentions, m.Name+"="+m.ID)
	}
	fmt.Printf("chat_id=%s chat_type=%s sender=%s message_id=%s mentions=[%s] text=%q\n",
		ev.chatID, ev.chatType, ev.senderID, ev.messageID,
		strings.Join(mentions, ","), truncateStr(ev.text, 60))
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
