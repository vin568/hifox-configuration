package tick

import (
	"fmt"
)

// SweepPrompt 是作业巡检的注入文本（巡检触发器，ADR 0018：云端无事件，定时兜底）。
func SweepPrompt(activeDir, archivedDir string) string {
	return fmt.Sprintf(
		"[触发器: sweep] 作业巡检：读 %s 的全部在途作业，逐条核对进度（云端 hifox task view / 本地 herdr agent 状态 / 直连结果文件）；"+
			"机械失败按策略重试，语义异常上报用户；已收口的作业移入 %s；无事则一行带过，不要展开。",
		activeDir, archivedDir)
}
