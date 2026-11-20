package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"time"

	"super-agent/internal/config"
	feishu "super-agent/internal/feishu"
	"super-agent/internal/outbox"
)

// recaller 是撤回动作的窄接口：命令层只依赖它，测试给假件。
type recaller interface {
	Recall(ctx context.Context, messageID string) error
}

// newRecaller 是接缝：生产走 feishu.NewSender，测试注入假件。
var newRecaller = func(cfg config.Config, appID, secret string) (recaller, error) {
	return feishu.NewSender(feishuConfigFrom(cfg, appID, secret))
}

// recallResult 是 recall 的 JSON 输出（与 answer/dispatch 一致走 jsonPrint）。
type recallResult struct {
	OutboxID        string `json:"outbox_id,omitempty"`
	MessageID       string `json:"message_id"`
	RecalledAt      string `json:"recalled_at,omitempty"`
	AlreadyRecalled bool   `json:"already_recalled,omitempty"`
	AlreadyDeleted  bool   `json:"already_deleted,omitempty"`
}

// cmdRecall 撤回本应用自己发的消息。撤回时限不在本地预判：
// 企业管理员可调，230009 是唯一权威。
func cmdRecall(args []string) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	entryID := fs.String("id", "", "outbox 回帖 ID（superctl reply 打印的那个）")
	messageID := fs.String("message", "", "飞书 message_id（om_...）")
	last := fs.Bool("last", false, "撤回最新一条可撤回的回帖")
	chatID := fs.String("chat", "", "只与 --last 连用：限定会话")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	picked := 0
	for _, on := range []bool{*entryID != "", *messageID != "", *last} {
		if on {
			picked++
		}
	}
	if picked != 1 {
		return errors.New("--id / --message / --last 必须且只能给一个")
	}
	if *chatID != "" && !*last {
		return errors.New("--chat 只能与 --last 一起用")
	}

	cfg, _, err := loadConfig([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	appID, secret, err := requireFeishuCredentials()
	if err != nil {
		return err
	}
	sender, err := newRecaller(cfg, appID, secret)
	if err != nil {
		return err
	}
	ob, err := outbox.Open(cfg.OutboxPath())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch {
	case *messageID != "":
		return recallByMessage(ctx, sender, ob, *messageID)
	case *entryID != "":
		e, err := ob.Get(*entryID)
		if err != nil {
			return fmt.Errorf("outbox 里没有 id=%s 的回帖（superctl reply 会打印 id）: %w", *entryID, err)
		}
		return recallEntry(ctx, sender, ob, e)
	default:
		e, ok, err := ob.LastRecallable(*chatID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("没有可撤回的回帖（都已撤回或没有 message_id）")
		}
		return recallEntry(ctx, sender, ob, e)
	}
}

func recallEntry(ctx context.Context, sender recaller, ob *outbox.Outbox, e outbox.Entry) error {
	if e.SentAt == nil {
		return errors.New("这条还没发出去（pending），没有可撤回的消息")
	}
	if e.MessageID == "" {
		return errors.New("这条没有记录飞书 message_id（旧版本投递），只能用 --message <om_...> 撤，或去客户端撤")
	}
	if e.RecalledAt != nil {
		return jsonPrint(recallResult{
			OutboxID:        e.ID,
			MessageID:       e.MessageID,
			AlreadyRecalled: true,
		})
	}
	err := sender.Recall(ctx, e.MessageID)
	deleted := false
	if err != nil {
		if !isRecallAlreadyDeleted(err) {
			return recallErrHint(err)
		}
		deleted = true
	}
	at := time.Now()
	if _, markErr := ob.MarkRecalled(e.ID, at); markErr != nil {
		return markErr
	}
	res := recallResult{OutboxID: e.ID, MessageID: e.MessageID, RecalledAt: at.UTC().Format(time.RFC3339)}
	res.AlreadyDeleted = deleted
	return jsonPrint(res)
}

func recallByMessage(ctx context.Context, sender recaller, ob *outbox.Outbox, messageID string) error {
	deleted := false
	if err := sender.Recall(ctx, messageID); err != nil {
		if !isRecallAlreadyDeleted(err) {
			return recallErrHint(err)
		}
		deleted = true
	}
	at := time.Now()
	res := recallResult{MessageID: messageID, RecalledAt: at.UTC().Format(time.RFC3339)}
	// outbox 里有同一 message_id 的记录就顺带标记；找不到不报错（可能不是本 outbox 发的），
	// 但写盘失败必须留痕：撤回已经生效，本地状态跟不上会让后续 --last 选错。
	if e, ok, err := ob.MarkRecalledByMessageID(messageID, at); err != nil {
		slog.Warn("superctl: 回填 recalled_at 失败", "message_id", messageID, "error", err)
	} else if ok {
		res.OutboxID = e.ID
	}
	res.AlreadyDeleted = deleted
	return jsonPrint(res)
}

// isRecallAlreadyDeleted: 230110 = 消息已不存在。目标状态（消息不可见）已达成，按成功处理。
func isRecallAlreadyDeleted(err error) bool {
	var apiErr *feishu.APIError
	return errors.As(err, &apiErr) && apiErr.Code == 230110
}

func recallErrHint(err error) error {
	var apiErr *feishu.APIError
	if !errors.As(err, &apiErr) {
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("撤回请求超时，结果未知；先看客户端是否已撤回，再决定是否重试")
		}
		return err
	}
	switch apiErr.Code {
	case 230009:
		return errors.New("超过企业管理员设置的撤回时限（管理后台「撤回和编辑消息权限」）；code=230009")
	case 230026:
		return errors.New("只能撤回本应用自己发的消息；code=230026")
	case 230002:
		return errors.New("机器人不在这个会话里；code=230002")
	case 230013:
		return errors.New("用户不在应用可用范围内；code=230013")
	case 230050:
		return errors.New("消息对机器人不可见；code=230050")
	case 230054:
		return errors.New("该消息类型不支持撤回；code=230054")
	case 232009:
		return errors.New("会话已解散；code=232009")
	}
	return err
}
