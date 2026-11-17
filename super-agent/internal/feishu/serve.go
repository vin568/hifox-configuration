package feishu

import (
	"context"
	"log/slog"
	"time"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/config"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/inject"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// Source identifies the inbound channel for the orchestrator's InjectFunc.
type Source string

// SourceFeishu is the only source this adapter speaks.
const SourceFeishu Source = "feishu"

// InjectFunc hands an admitted, deduplicated message to the orchestrator.
// The payload carries the full envelope semantics (kind, proxy, sender), so
// the receiver builds the prompt from it directly instead of guessing.
type InjectFunc func(ctx context.Context, src Source, p inject.Payload) error

// reconnectDelay paces the outer loop after larkws itself gives up; the SDK
// handles ordinary disconnects internally, so reaching here already means it
// returned an error from Start.
const reconnectDelay = 5 * time.Second

// Run opens the Feishu long-connection and serves messages until ctx is
// cancelled. It never returns on its own: connection errors are logged and the
// connection is re-established.
func Run(ctx context.Context, cfg Config, inject InjectFunc) error {
	cfg, err := LoadConfigFromEnv(cfg)
	if err != nil {
		return err
	}

	dedupPath := cfg.DedupPath
	if dedupPath == "" {
		dedupPath = DefaultDedupPath
	}
	dedup, err := newDedupStore(config.ExpandPath(dedupPath), cfg.DedupExpire)
	if err != nil {
		return err
	}
	defer dedup.close()

	queue := newRetryQueue(inject)
	go queue.run(ctx)

	srv := &server{cfg: cfg, dedup: dedup, queue: queue}
	d := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
			// The handler must not return an error: the SDK turns that into a
			// frame-level NACK, and delivery failures are the retry queue's
			// job, not Feishu's.
			srv.handle(event)
			return nil
		})

	cli := larkws.NewClient(cfg.AppID, cfg.AppSecret,
		larkws.WithEventHandler(d),
		larkws.WithLogLevel(larkcore.LogLevelWarn),
	)

	keepConnected(ctx, cli, "feishu")
	return ctx.Err()
}

// keepConnected runs cli.Start in a loop with reconnect pacing until ctx is
// cancelled. label prefixes the log lines (serve / probe).
func keepConnected(ctx context.Context, cli *larkws.Client, label string) {
	for ctx.Err() == nil {
		if err := startUntilDone(ctx, cli); err != nil && ctx.Err() == nil {
			slog.Warn(label+": long connection ended, reconnecting", "error", err)
			select {
			case <-ctx.Done():
			case <-time.After(reconnectDelay):
			}
		}
	}
}

// startUntilDone runs cli.Start until it returns or ctx is cancelled.
func startUntilDone(ctx context.Context, cli *larkws.Client) error {
	done := make(chan error, 1)
	go func() { done <- cli.Start(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		cli.Close()
		return ctx.Err()
	}
}

// server carries the per-connection message path dependencies.
type server struct {
	cfg   Config
	dedup *dedupStore
	queue *retryQueue
}

// eventInfo is the flattened view of one inbound message event, shared by the
// serve path and the probe tool.
type eventInfo struct {
	messageID    string
	chatID       string
	chatType     string
	msgType      string
	content      string
	senderID     string
	text         string
	mentions     []mentionInfo
	mentionedIDs []string
}

// parseEvent dereferences the SDK pointers into eventInfo (open_id first,
// user_id fallback everywhere an id is read); nil for absent or残缺 events.
func parseEvent(event *larkim.P2MessageReceiveV1) *eventInfo {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}
	msg := event.Event.Message
	info := &eventInfo{
		messageID: deref(msg.MessageId),
		chatID:    deref(msg.ChatId),
		chatType:  deref(msg.ChatType),
		msgType:   deref(msg.MessageType),
		content:   deref(msg.Content),
	}
	if event.Event.Sender != nil && event.Event.Sender.SenderId != nil {
		info.senderID = deref(event.Event.Sender.SenderId.OpenId)
		if info.senderID == "" {
			info.senderID = deref(event.Event.Sender.SenderId.UserId)
		}
	}
	for _, m := range msg.Mentions {
		mi := mentionInfo{Key: deref(m.Key), Name: deref(m.Name)}
		if m.Id != nil {
			mi.ID = deref(m.Id.OpenId)
			if mi.ID == "" {
				mi.ID = deref(m.Id.UserId)
			}
		}
		info.mentions = append(info.mentions, mi)
		if mi.ID != "" {
			info.mentionedIDs = append(info.mentionedIDs, mi.ID)
		}
	}
	info.text = extractText(info.msgType, info.content, info.mentions)
	return info
}

// handle runs filter -> dedup -> inject for one event, synchronously (the SDK
// already dispatches events on its own goroutines).
func (s *server) handle(event *larkim.P2MessageReceiveV1) {
	ev := parseEvent(event)
	if ev == nil {
		return
	}

	ok, proxyReply := s.cfg.allow(ev.chatType, ev.chatID, ev.senderID, ev.mentionedIDs)
	if !ok {
		slog.Info("feishu: 消息被过滤丢弃（私聊需 sender 在 allowed_users；群需 @bot、代答群或全收群；引导期用 superctl probe 核对 ID）",
			"chat_id", ev.chatID, "chat_type", ev.chatType, "sender", ev.senderID, "message_id", ev.messageID)
		return
	}

	if !s.dedup.claim(ev.messageID) {
		return
	}
	task := injectTask{
		src:      SourceFeishu,
		p:        inject.Payload{ChatID: ev.chatID, SenderID: ev.senderID, Text: ev.text, Kind: kindOf(ev.chatType), ProxyReply: proxyReply},
		attempts: 1,
		done:     func() { s.dedup.markDone(ev.messageID) },
	}
	if err := s.queue.injectNow(context.Background(), task); err != nil {
		// 瞬时失败与主控 blocked 同梯排队重试（ADR 0018「注入器侧排队重试」）；
		// blocked 的用户提醒与限频在 InjectFunc 侧（superctl serve）。
		task.attempts++
		s.queue.enqueue(task)
		return
	}
	s.dedup.markDone(ev.messageID)
}

// kindOf maps the Feishu chat_type onto the envelope's dm/group kind.
func kindOf(chatType string) string {
	if chatType == "group" {
		return "group"
	}
	return "dm"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
