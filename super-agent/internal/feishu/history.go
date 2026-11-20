package feishu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const (
	historyListTimeout   = 60 * time.Second
	historyOneTimeout    = 10 * time.Second
	historyLineMaxRunes  = 200
	historyTimeLayout    = "2006-01-02 15:04:05"
	historyEmptyWindow   = "（窗口内没有消息）"
	historyUnknownTime   = "未知时间"
	historyRecalledLabel = "[消息已撤回]"
)

// HistoryPageSize 是飞书单页上限：超过它的 --limit 由 historyList 自动翻页凑齐。
const HistoryPageSize = 50

// HistoryQuery 是一次历史读取的输入。Now 为零时用 time.Now()（测试注入）。
type HistoryQuery struct {
	ChatID    string
	MessageID string
	Limit     int
	Since     time.Duration
	Download  bool
	Now       time.Time
}

// HistoryText 返回可直接打印/注入的纯文本摘要。
func HistoryText(ctx context.Context, cfg Config, q HistoryQuery) (string, error) {
	cfg, err := LoadConfigFromEnv(cfg)
	if err != nil {
		return "", err
	}
	reader := readerFor(cfg)
	if reader == nil {
		return "", errors.New("缺少飞书凭证：设置 SONGJIANG_FEISHU_APP_ID / SONGJIANG_FEISHU_APP_SECRET")
	}
	var media *mediaStore
	if q.Download {
		if cfg.MediaDir == "" {
			return "", errors.New("--download 需要配置 media_dir（没有落盘目录就下不了附件）")
		}
		media = mediaStoreFor(cfg)
	}
	timeout := historyListTimeout
	if q.MessageID != "" {
		// 单条/下载没有被翻页放大，维持原来的短预算。
		timeout = historyOneTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return historyText(ctx, reader, media, q)
}

// historyText 是测试接缝：reader 与 media 由调用方注入。
func historyText(ctx context.Context, reader messageReader, media *mediaStore, q HistoryQuery) (string, error) {
	now := q.Now
	if now.IsZero() {
		now = time.Now()
	}
	switch {
	case q.MessageID != "" && q.ChatID != "":
		return "", errors.New("--chat 与 --message 只能给一个")
	case q.MessageID == "" && q.ChatID == "":
		return "", errors.New("--chat 与 --message 必须给一个")
	case q.MessageID != "":
		if q.Limit != 0 || q.Since != 0 {
			return "", errors.New("--message 不能与 --limit/--since 同时使用")
		}
		return historyOne(ctx, reader, media, q)
	default:
		if q.Limit < 1 {
			return "", errors.New("--limit 必须大于 0")
		}
		if q.Since <= 0 {
			return "", errors.New("--since 必须大于 0")
		}
		return historyList(ctx, reader, q, now)
	}
}

func historyList(ctx context.Context, reader messageReader, q HistoryQuery, now time.Time) (string, error) {
	start := now.Add(-q.Since)
	// 平台单页最多 50 条：超过就按页翻，直到凑够 --limit 或没有更早的了。
	var msgs []*messageView
	token := ""
	hasMore := false
	for len(msgs) < q.Limit {
		size := q.Limit - len(msgs)
		if size > HistoryPageSize {
			size = HistoryPageSize
		}
		page, next, more, err := reader.List(ctx, q.ChatID, start, now, size, token)
		if err != nil {
			return "", historyErrHint(err)
		}
		msgs = append(msgs, page...)
		if len(msgs) > q.Limit {
			// 平台若无视 page_size 多给了，也不许超过调用方给的预算。
			msgs = msgs[:q.Limit]
		}
		hasMore = more
		// 空页不再翻：token 再怎么变也不会有内容，继续请求只是白花配额。
		if len(page) == 0 {
			break
		}
		// 防脏数据/防死循环：没有下一页 token 或 token 原地踏步就停；
		// 此时仍保留平台自报的 hasMore 文案（模型可能说还有但没给 token）。
		if !more || next == "" || next == token {
			break
		}
		token = next
	}
	if len(msgs) == 0 {
		return historyEmptyWindow, nil
	}
	// 平台按 ByCreateTimeDesc 返回；渲染前整体反转成时间正序，不做本地重排。
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[会话历史] chat=%s 最近 %d 条（窗口 %s ~ %s，按时间正序）\n",
		q.ChatID, len(msgs), start.Local().Format(historyTimeLayout), now.Local().Format(historyTimeLayout))
	hasMedia := false
	for _, v := range msgs {
		if historyHasMedia(v) {
			hasMedia = true
		}
		fmt.Fprintf(&b, "%s %s %s\n", historyStamp(v.CreateMS), historySender(v), historyBody(ctx, v, nil))
	}
	switch {
	case hasMore:
		b.WriteString("（更早还有")
		if len(msgs) >= q.Limit {
			fmt.Fprintf(&b, "；已到 --limit %d", q.Limit)
		}
		if hasMedia {
			b.WriteString("；要看附件用 --message <消息ID> --download")
		}
		b.WriteString("）\n")
	case hasMedia:
		b.WriteString("（要看附件用 --message <消息ID> --download）\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func historyOne(ctx context.Context, reader messageReader, media *mediaStore, q HistoryQuery) (string, error) {
	v, err := reader.Get(ctx, q.MessageID)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == 230110 {
			return "", fmt.Errorf("消息已撤回或不存在（%s）；code=230110", q.MessageID)
		}
		return "", historyErrHint(err)
	}
	body := historyBody(ctx, v, media)
	var b strings.Builder
	fmt.Fprintf(&b, "[消息] %s chat=%s 发送者 %s 类型 %s 时间 %s\n",
		v.MessageID, v.ChatID, historySender(v), v.MsgType, historyStamp(v.CreateMS))
	b.WriteString(body)
	if v.Total > 1 {
		fmt.Fprintf(&b, "\n（合并转发，共 %d 条，只显示首条）", v.Total)
	}
	return b.String(), nil
}

// historyHasMedia 判断这一页里有没有附件：只有出现附件时才提示下载入口。
func historyHasMedia(v *messageView) bool {
	if v.Deleted || strings.TrimSpace(v.Content) == "" {
		return false
	}
	_, refs := extractMessage(v.MsgType, v.Content, v.Mentions)
	return len(refs) > 0
}

// historyBody 把一条消息渲染成单行正文：多行折叠、超长截断；
// media 非空时下载附件并把 key 占位改成本地路径（仅 --message --download）。
func historyBody(ctx context.Context, v *messageView, media *mediaStore) string {
	if v.Deleted {
		return historyRecalledLabel
	}
	// 空正文（sticker、系统消息等）没有可提取的内容：直接点名类型，比「[媒体消息]」有用。
	if strings.TrimSpace(v.Content) == "" || strings.TrimSpace(v.Content) == "{}" {
		return "[消息类型 " + v.MsgType + "]"
	}
	text, refs := extractMessage(v.MsgType, v.Content, v.Mentions)
	if media != nil {
		for _, m := range refs {
			path, err := media.Save(ctx, v.MessageID, m)
			if err != nil {
				slog.Warn("feishu: 历史附件下载失败，保留 key 占位",
					"message_id", v.MessageID, "key", m.Key, "error", err)
				continue
			}
			text = replaceMediaToken(text, m, path)
		}
	}
	text = collapseLines(text)
	if strings.TrimSpace(text) == "" {
		return "[消息类型 " + v.MsgType + "]"
	}
	return truncateRunes(text, historyLineMaxRunes)
}

// historySender 不做姓名解析（要 contact scope，且多一次调用）。
func historySender(v *messageView) string {
	if v.SenderType == "app" {
		return v.SenderID + "（应用）"
	}
	if v.SenderID == "" {
		return "未知发送者"
	}
	return v.SenderID
}

func historyStamp(ms int64) string {
	if ms == 0 {
		return historyUnknownTime
	}
	return time.UnixMilli(ms).Local().Format(historyTimeLayout)
}

// collapseLines 把正文压成一行：一条消息一行，方便主控和人扫读。
func collapseLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", " / ")
	return s
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// historyErrHint 把飞书错误码翻译成中文下一步；未列出的码保持原文（含 request_id）。
func historyErrHint(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Code {
	case 230002:
		return fmt.Errorf("机器人不在这个会话里（群聊要先拉应用机器人进群）；code=230002")
	case 230050:
		return fmt.Errorf("这批消息对机器人不可见（群关了「新成员可见历史」，或消息早于入群）；code=230050")
	case 230073:
		return fmt.Errorf("这个话题对机器人不可见；code=230073")
	case 230110:
		return fmt.Errorf("消息已撤回或不存在；code=230110")
	case 230027:
		return fmt.Errorf("缺权限：需要 im:message:readonly（群聊另需 im:message.group_msg）；code=230027")
	case 231203:
		return fmt.Errorf("该会话类型不支持读历史（如密聊开启禁止复制）；code=231203")
	}
	return err
}
