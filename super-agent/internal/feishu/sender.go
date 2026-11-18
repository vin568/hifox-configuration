package feishu

import (
	"context"
	"fmt"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// sendMessageAPI is the thin seam over the SDK: everything Sender does besides
// building the payload. Production wires the real im/v1 call; tests wire a fake.
type sendMessageAPI interface {
	send(ctx context.Context, chatID, msgType, content string) error
}

// sdkMessageAPI implements sendMessageAPI with the official client, which owns
// tenant_access_token lifecycle (fetch, cache, refresh) automatically.
type sdkMessageAPI struct {
	client *lark.Client
}

func (a sdkMessageAPI) send(ctx context.Context, chatID, msgType, content string) error {
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
		return fmt.Errorf("feishu send: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu send: code=%d msg=%s request_id=%s",
			resp.Code, resp.Msg, resp.RequestId())
	}
	return nil
}

// Sender posts plain-text replies into Feishu chats. It is the outbox drain's
// delivery side: one Send per outbox line, receive_id_type=chat_id.
type Sender struct {
	api sendMessageAPI
}

// NewSender builds a Sender with its own official SDK client (tenant token is
// managed by the SDK). Credentials come from cfg (or the env fallback).
func NewSender(cfg Config) (*Sender, error) {
	cfg, err := LoadConfigFromEnv(cfg)
	if err != nil {
		return nil, err
	}
	return &Sender{api: sdkMessageAPI{client: lark.NewClient(cfg.AppID, cfg.AppSecret)}}, nil
}

// Send posts text into the given chat.
func (s *Sender) Send(ctx context.Context, chatID, text string) error {
	return s.api.send(ctx, chatID, "text", buildTextContent(text))
}
