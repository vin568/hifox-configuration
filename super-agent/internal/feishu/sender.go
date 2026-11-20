package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// sendMessageAPI 是 Sender 对 SDK 的窄接口：发送、上传、撤回都走它，测试给同一个假件。
type sendMessageAPI interface {
	send(ctx context.Context, chatID, msgType, content string) (messageID string, err error)
	uploadImage(ctx context.Context, path string) (imageKey string, err error)
	recall(ctx context.Context, messageID string) error
}

// sdkMessageAPI implements sendMessageAPI with the official client, which owns
// tenant_access_token lifecycle (fetch, cache, refresh) automatically.
type sdkMessageAPI struct {
	client *lark.Client
}

func (a sdkMessageAPI) send(ctx context.Context, chatID, msgType, content string) (string, error) {
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build()
	resp, err := a.client.Im.Message.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("feishu send: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("feishu send: code=%d msg=%s request_id=%s",
			resp.Code, resp.Msg, resp.RequestId())
	}
	if resp.Data == nil || resp.Data.MessageId == nil || *resp.Data.MessageId == "" {
		// 消息已经发出去了（code=0），但拿不到 id。判失败会让 Drain 重发同一条，
		// 用户会收到重复消息——宁可丢撤回能力，也不能重复打扰。
		slog.Warn("feishu send: 响应缺 message_id，消息已发出但无法撤回", "chat_id", chatID)
		return "", nil
	}
	return *resp.Data.MessageId, nil
}

func (a sdkMessageAPI) uploadImage(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("feishu upload image %s: %w", path, err)
	}
	defer f.Close()
	req := larkim.NewCreateImageReqBuilder().
		Body(larkim.NewCreateImageReqBodyBuilder().
			ImageType("message").
			Image(f).
			Build()).
		Build()
	resp, err := a.client.Im.Image.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("feishu upload image %s: %w", path, err)
	}
	if !resp.Success() {
		return "", &APIError{Op: "feishu upload image", Code: resp.Code, Msg: resp.Msg, RequestID: resp.RequestId()}
	}
	if resp.Data == nil || resp.Data.ImageKey == nil || *resp.Data.ImageKey == "" {
		return "", fmt.Errorf("feishu upload image %s: 响应缺 image_key", path)
	}
	return *resp.Data.ImageKey, nil
}

func (a sdkMessageAPI) recall(ctx context.Context, messageID string) error {
	resp, err := a.client.Im.Message.Delete(ctx, larkim.NewDeleteMessageReqBuilder().MessageId(messageID).Build())
	if err != nil {
		return fmt.Errorf("feishu recall %s: %w", messageID, err)
	}
	if !resp.Success() {
		return &APIError{Op: "feishu recall", Code: resp.Code, Msg: resp.Msg, RequestID: resp.RequestId()}
	}
	return nil
}

// Sender posts replies into Feishu chats. It is the outbox drain's delivery side:
// one Deliver per outbox line, receive_id_type=chat_id.
type Sender struct {
	api sendMessageAPI
	// timeout 是单条投递（上传 + 发送）的总预算；零值不包 ctx（测试字面量）。
	timeout time.Duration
}

// NewSender builds a Sender with its own official SDK client (tenant token is
// managed by the SDK). Credentials come from cfg (or the env fallback).
func NewSender(cfg Config) (*Sender, error) {
	cfg, err := LoadConfigFromEnv(cfg)
	if err != nil {
		return nil, err
	}
	return &Sender{
		api:     sdkMessageAPI{client: lark.NewClient(cfg.AppID, cfg.AppSecret)},
		timeout: 60 * time.Second,
	}, nil
}

// Deliver posts one outbox entry: text only keeps the exact text payload
// (byte-identical to the old Send), one image with no text becomes an image
// bubble, everything else becomes a single post message (text paragraph then
// one paragraph per image). All images are uploaded first: any upload failure
// sends nothing, so a retry never produces half a message.
func (s *Sender) Deliver(ctx context.Context, chatID, text string, images []string) (string, error) {
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	if len(images) == 0 {
		return s.api.send(ctx, chatID, "text", buildTextContent(text))
	}
	keys := make([]string, 0, len(images))
	for _, path := range images {
		key, err := s.api.uploadImage(ctx, path)
		if err != nil {
			return "", err
		}
		keys = append(keys, key)
	}
	if text == "" && len(keys) == 1 {
		return s.api.send(ctx, chatID, "image", buildImageContent(keys[0]))
	}
	content, err := buildPostContent(text, keys)
	if err != nil {
		return "", err
	}
	return s.api.send(ctx, chatID, "post", content)
}

// Recall withdraws one message sent by this app. No internal retry: recall is
// a user action, and a repeated call converges via 230110.
func (s *Sender) Recall(ctx context.Context, messageID string) error {
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	return s.api.recall(ctx, messageID)
}
