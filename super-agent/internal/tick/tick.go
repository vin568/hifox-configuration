// Package tick 拼作业巡检的注入文本与放行判据（注入卫生，ADR 0022）。
// 样板（路径、命令、回复规则）只在首条 prime 出现一次，这里只带增量。
package tick

// Signals 是「巡检有没有内容」的三个机器信号：活跃区台账文件数、在途清单条数、
// jobs workspace 里的活动 agent 数。三者皆空说明无事可巡检——不必叫醒主控。
type Signals struct {
	ActiveJobs int
	Inflight   int
	JobsAgents int
}

// Needed 判定本轮是否值得注入。任一非空即注入：宁可让主控自己判断无事，
// 也不能让在途作业无人对账。
func Needed(s Signals) bool {
	return s.ActiveJobs > 0 || s.Inflight > 0 || s.JobsAgents > 0
}

// SweepPrompt 是作业巡检的注入文本（巡检触发器；路径与纪律见首条 prime）。
func SweepPrompt() string {
	return "[触发器: sweep] 作业巡检：按活跃区台账逐条核对在途作业，机械失败按策略重试，" +
		"语义异常上报；已收口的收口归档；无事一行带过，不展开。"
}
