# 0047：送达是状态不是布尔——未确认落盘按已派出处理

复盘 agent 在 2026-11-27 发现一个会自我复制的故障：定时复盘派出去后，程序判断「话没送到」，其实送到了、活也干完了。按失败回滚的结果是**每隔 10 分钟把同一天再派一遍**，同一窗口跑了两轮，每轮产出两万字互相矛盾的复盘。

根因不在超时长短，在判据的形状：`boot.SubmitPrompt` 用「会话文件里出现这段文本」判送达，超时即 `error`。而 omp 在首个 assistant 输出之前不写会话文件，日度复盘又要先读十几份台账才开始动手——90 秒预算必然不够。这是「先重读、再输出」这类作业的稳定失败模式，不是偶发抖动。

**决策：**

- **送达三态**：`boot.SubmitPrompt` 返回 `Delivery{Session, Unconfirmed}`。`Unconfirmed=true` 表示提交被接受、agent 活着、但预算内没看到文本落进会话。判据：agent 状态为 `working` 且会话路径已知。
- **未确认不得回滚**：`dispatchLocalEx` / 节点派发 / `handoff` 在 `Unconfirmed` 时照常登记在途条目、照常建台账，只在条目上写 `delivery=unconfirmed` 并在 CLI 输出里带 `delivery` 字段。回滚会把正在干活的作业变成孤儿，并诱使调用方重派同一单。
- **真正的失败仍然失败**：agent 闲着而文本没进会话（文本卡在输入框，11-25 事故）、拿不到会话路径（缺 herdr 集成）、`blocked`（审批框）一律维持原错误语义。放宽不能把老故障重新放进来。
- **prime 是例外**：主控首条注入必须逐字确认落盘（岗位材料不能靠猜），`Unconfirmed` 在 `primeAgent` 里仍报错，pending 留待下一轮只补 Enter。
- **复盘去重键改成「本窗口已尝试」**：`retro.json` 的 `last_date` 在派发**之前**写，新增 `last_attempt_error` 记失败或未确认的原因。判据出错时宁可少跑一轮复盘，也不能重复派发。
- **未确认由巡检兜底**：在途条目 `delivery=unconfirmed`、结果文件未就绪、会话文件在派发后也没被写过时，报一条 `delivery-unconfirmed` 发现（类别 `unknown`，即「派发结果未知」）。只是告警，**不自动 cancel**——ADR 0030 已定过「只告警，不自动 cancel」。会话文件在派发后被写过即视为自证送达。
- **兜底分两档，因为「没落盘」有两个成因**：agent 已不在跑（`idle`/`done`）时 15 分钟就报——那才是「文本可能停在输入框」的形状；agent 仍 `working` 时用更长的上界（`local_stall_after`，默认 45 分钟，且不小于 15 分钟）。理由：omp 在首个 assistant 输出之前不落盘，先重读再动手的作业（复盘本身）必然有一段时间既无结果也无写入，15 分钟会误报；但**永久沉默也不行**——那等于把本文档 Considered Options 里拒绝过的「working 就算送达」换个说法，卡在输入框的 working 外壳会一直没人管。到上界仍未写入就报，兼作停滞告警的兜底。
- **交接同样要写回状态**：本机 `handoff` 与节点 `handoff` 在 `Unconfirmed` 时把 `delivery=unconfirmed` 写回新条目，并在命令输出里带 `delivery` 字段——只打日志会让巡检把它当已确认，文本停在输入框的作业从此沉默。
- **审视面暴露失败**：`reviewPrompt` 带上本轮派发失败/未确认的原因，`superctl retro status` 增加 `last_attempt_error`。否则「这一轮为什么没复盘」只能靠日志考古。

**Considered Options：**

- **放宽成「agent 处于 working/done 就算送达」**：拒。一行改动，但会漏掉「文本真停在输入框」的老问题——那正是 11-25 事故，而且用户手按回车才救回来。
- **只修复盘去重**：拒。止住重复，但孤儿 brief / 无台账那条继续存在（派发失败回滚 + 台账要等注入成功才建）。
- **把超时预算调大**：拒。按作业长短调预算，等于给每类作业配一个魔法数字；判据形状错了，调参数只是把故障往后推。
- **未确认自动 cancel**：拒。`unconfirmed` 的真实语义就是「不知道」；自动 cancel 会砍掉正在跑的作业。

**Consequences：**

- `SubmitPrompt` 的调用方全部要处理返回结构：`dispatchLocalEx`、节点派发、本机与节点 `handoff`、`primeAgent`。前者们按已派出登记，后者仍要求确认。
- 在途清单多一个可选字段 `delivery`；旧条目没有该字段，按已确认对待，不需要迁移。
- 派发命令的输出多一个可选字段 `delivery=unconfirmed`：主控据此如实说「已派出、尚待确认」，而不是「失败」。
- 巡检多一类发现（`delivery-unconfirmed`），去重走现有 `health:<节点><agent>:<条件>` 通道，条件消失即消解。
- 判据仍然依赖 herdr 上报的 `agent_status`：节点没装 omp 集成时拿不到会话路径，那条路径按 `ErrNoSessionPath` 失败（既有的节点准入约束不变）。

**验证：**

- `internal/boot`：`TestSubmitPromptReportsUnconfirmedWhileWorking`（working + 不落盘 → Unconfirmed，且不补 Enter）、`TestSubmitPromptStillFailsWhenIdleAndUnflushed`（idle + 不落盘 → 仍失败）；原 `TestSubmitPromptFailsWhenWorkingNeverFlushes` 保留，钉住 prime 仍要求确认。
- `cmd/superctl`：`TestDispatchUnconfirmedDoesNotRollback`（条目与台账都保留）、`TestDispatchConfirmedHasNoDeliveryLabel`、`TestRetroAttemptIsRecordedEvenWhenDispatchFails`、`TestRetroUnconfirmedRecordsResultAndDoesNotRedispatch`、`TestLocalHealthFlagsUnconfirmedAfterGrace`、`TestLocalHealthUnconfirmedSilentWhenSessionAdvanced`。
