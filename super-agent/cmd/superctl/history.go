package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	feishu "super-agent/internal/feishu"
)

// historyTextFn 是命令层接缝：测试注入假件，避免真连飞书。
var historyTextFn = feishu.HistoryText

// cmdHistory 是只读查询面：产物是渲染好的纯文本，不注入、不写状态、不进 outbox。
func cmdHistory(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	chatID := fs.String("chat", "", "会话 ID（与 --message 二选一）")
	messageID := fs.String("message", "", "消息 ID（与 --chat 二选一）")
	limit := fs.Int("limit", 20, "最多返回条数（1..50）")
	since := fs.Duration("since", 24*time.Hour, "时间窗口（>0，≤168h）")
	download := fs.Bool("download", false, "仅 --message 有效：下载该条附件并打印本地路径")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 参数错不该等网络：本地校验全部在建客户端之前完成。
	if (*chatID == "") == (*messageID == "") {
		return errors.New("--chat 与 --message 必须且只能给一个")
	}
	var q feishu.HistoryQuery
	if *messageID != "" {
		// --limit/--since 有默认值，只有用户显式给了才算冲突。
		if flagWasSet(fs, "limit") {
			return errors.New("--message 不能与 --limit 同时使用")
		}
		if flagWasSet(fs, "since") {
			return errors.New("--message 不能与 --since 同时使用")
		}
		if *download {
			q.Download = true
		}
		q.MessageID = *messageID
	} else {
		if *download {
			return errors.New("--download 只能与 --message 一起用（列表模式不下载附件）")
		}
		if *limit < 1 || *limit > feishu.HistoryMaxLimit {
			return fmt.Errorf("--limit 必须在 1..%d 之间（飞书 page_size 上限 %d），当前 %d", feishu.HistoryMaxLimit, feishu.HistoryMaxLimit, *limit)
		}
		if *since <= 0 || *since > feishu.HistoryMaxSince {
			return fmt.Errorf("--since 必须大于 0 且不超过 %s，当前 %s", feishu.HistoryMaxSince, *since)
		}
		q.ChatID = *chatID
		q.Limit = *limit
		q.Since = *since
	}

	cfg, _, err := loadConfig([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	appID, secret, err := requireFeishuCredentials()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	text, err := historyTextFn(ctx, feishuConfigFrom(cfg, appID, secret), q)
	if err != nil {
		return err
	}
	fmt.Println(text)
	return nil
}

// flagWasSet 判断用户是否显式给过某个 flag（用来区分默认值与"同时给了"）。
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}
