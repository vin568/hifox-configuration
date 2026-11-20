package feishu

import (
	"context"
	"fmt"
	"strconv"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// messageView 是一条已发消息的扁平视图。字段来自 im/v1 消息对象，
// 与事件对象不同形：sender 是平铺的 id/id_type，mentions 的 Id 是 *string（事件里是 *UserId）。
type messageView struct {
	MessageID  string
	ChatID     string
	RootID     string
	ParentID   string
	MsgType    string
	Content    string
	SenderID   string
	SenderType string // user | app
	Deleted    bool
	CreateMS   int64 // create_time（毫秒）；解析失败为 0
	// Total 是 Get 返回的 Items 条数：合并转发会 >1，历史命令据此提示「只显示首条」。
	Total    int
	Mentions []mentionInfo
}

// messageGetter 是 im/v1/messages/:message_id 的窄接口（引用上下文用；历史单条查询共用）。
type messageGetter interface {
	Get(ctx context.Context, messageID string) (*messageView, error)
}

// messageLister 是 im/v1/messages?container_id=… 的窄接口（会话历史用）。
// pageToken 为空表示第一页；nextToken 非空时可继续翻页。单页上限由平台定（50）。
type messageLister interface {
	List(ctx context.Context, chatID string, start, end time.Time, pageSize int, pageToken string) (msgs []*messageView, nextToken string, hasMore bool, err error)
}

// messageReader 同时具备单条与列表读取（历史命令用）。
type messageReader interface {
	messageGetter
	messageLister
}

// sdkMessageReader 是 messageReader 的生产实现。
type sdkMessageReader struct {
	client *lark.Client
}

// readerFor 按配置建只读客户端：没有凭证就不建（probe / 单测路径），
// 这条装配线断了线上所有回帖会静默退回「只看当前消息」。
func readerFor(cfg Config) messageReader {
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return nil
	}
	return sdkMessageReader{client: lark.NewClient(cfg.AppID, cfg.AppSecret)}
}

func (r sdkMessageReader) Get(ctx context.Context, messageID string) (*messageView, error) {
	req := larkim.NewGetMessageReqBuilder().MessageId(messageID).Build()
	resp, err := r.client.Im.Message.Get(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("feishu get message %s: %w", messageID, err)
	}
	if !resp.Success() {
		return nil, &APIError{Op: "feishu get message", Code: resp.Code, Msg: resp.Msg, RequestID: resp.RequestId()}
	}
	if resp.Data == nil || len(resp.Data.Items) == 0 || resp.Data.Items[0] == nil {
		return nil, fmt.Errorf("feishu get message %s: 响应为空（消息不存在或对机器人不可见）", messageID)
	}
	// 合并转发会返回 1 条 merge_forward + N 条子消息，只取首条（容器本身），不展开。
	v := viewOf(resp.Data.Items[0])
	v.Total = len(resp.Data.Items)
	return v, nil
}

// normalizePageSize 把调用方的每页条数收敛到飞书契约的 1..50：
// 上界截断（平台最多 50），下界直接报错——静默放大成 50 会违背「只取 --limit」。
func normalizePageSize(n int) (int, error) {
	if n < 1 {
		return 0, fmt.Errorf("page_size 必须大于 0，当前 %d", n)
	}
	if n > HistoryPageSize {
		return HistoryPageSize, nil
	}
	return n, nil
}

func (r sdkMessageReader) List(ctx context.Context, chatID string, start, end time.Time, pageSize int, pageToken string) ([]*messageView, string, bool, error) {
	pageSize, err := normalizePageSize(pageSize)
	if err != nil {
		return nil, "", false, err
	}
	builder := larkim.NewListMessageReqBuilder().
		ContainerIdType("chat").
		ContainerId(chatID).
		StartTime(strconv.FormatInt(start.Unix(), 10)).
		EndTime(strconv.FormatInt(end.Unix(), 10)).
		SortType("ByCreateTimeDesc").
		PageSize(pageSize)
	if pageToken != "" {
		builder = builder.PageToken(pageToken)
	}
	resp, err := r.client.Im.Message.List(ctx, builder.Build())
	if err != nil {
		return nil, "", false, fmt.Errorf("feishu list messages %s: %w", chatID, err)
	}
	if !resp.Success() {
		return nil, "", false, &APIError{Op: "feishu list messages", Code: resp.Code, Msg: resp.Msg, RequestID: resp.RequestId()}
	}
	if resp.Data == nil {
		return nil, "", false, nil
	}
	out := make([]*messageView, 0, len(resp.Data.Items))
	for _, m := range resp.Data.Items {
		if m != nil {
			out = append(out, viewOf(m))
		}
	}
	next := deref(resp.Data.PageToken)
	// hasMore 如实反映平台自报；没有 token 时上层无法续翻，由调用方停并提示。
	hasMore := resp.Data.HasMore != nil && *resp.Data.HasMore
	return out, next, hasMore, nil
}

// viewOf 把 SDK 的消息对象摊平。mentions 的 Id 是 *string，直接 deref；
// 不要照抄事件的 MentionEvent.Id.OpenId。
func viewOf(m *larkim.Message) *messageView {
	v := &messageView{
		MessageID: deref(m.MessageId),
		ChatID:    deref(m.ChatId),
		RootID:    deref(m.RootId),
		ParentID:  deref(m.ParentId),
		MsgType:   deref(m.MsgType),
		Deleted:   m.Deleted != nil && *m.Deleted,
	}
	if m.Body != nil {
		v.Content = deref(m.Body.Content)
	}
	if m.Sender != nil {
		v.SenderID = deref(m.Sender.Id)
		v.SenderType = deref(m.Sender.SenderType)
	}
	if ms, err := strconv.ParseInt(deref(m.CreateTime), 10, 64); err == nil {
		v.CreateMS = ms
	}
	for _, mi := range m.Mentions {
		if mi == nil {
			continue
		}
		v.Mentions = append(v.Mentions, mentionInfo{
			Key:  deref(mi.Key),
			ID:   deref(mi.Id),
			Name: deref(mi.Name),
		})
	}
	return v
}
