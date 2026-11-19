# ADR 0030：受控 agent 的上下文测量与 handoff

上下文治理从「主控会话文件过了 8 MiB 就换脑」扩展为**所有受控 agent 共用同一套度量**，
并在合适的时候交接。触发问题的是一组实测事实：

- 同一个 1.9 MB 的 omp 会话，provider 回报的 prompt 只有 119,797 token，模型窗口
  1,000,000；另一个 777 KB 的会话约 69,781 token。bytes/token 在 11–16 之间浮动，
  字节数既不能跨模型比较，也不能稳定换算。
- omp 自己已经记录 `contextSnapshot.promptTokens`/`usage`/`compactionEpoch`；Claude
  Code 的 statusline、Codex 的 `token_count`、OpenCode 的 `Estimate{measured,estimated}`
  也都走「provider 回报 + 模型窗口」这条路。市场共同点不是统一存储，而是统一公式。
- 平台任务不在本机 herdr，没有会话、没有 `tell/cancel` 身份；它们只能按任务状态处理。

**决策：**

- **统一 Snapshot**：`internal/ctxmeter` 把所有 harness 归一成
  `PromptTokens + OutputTokens + TailTokens / WindowTokens`。分子来自 provider 回报
  的最近一次请求，分母优先 harness 自报、其次本地模型目录、最后配置覆盖；测量不可用是 `unknown`，不是 0。
- **受控范围**：master、retro、`inflight.kind=local` 的 job agent。平台任务与非在途
  herdr agent 不测、不 handoff；非在途 agent 没有 `report/tell/cancel` 身份。
- **统一触发**：`ceiling = window - max(window*15%, 16k)`，告警线在 ceiling 下方再留
  8k。小窗口（保留区 >= 窗口）退化为保留一半，ceiling 至少 1，避免算出 0 让 agent
  永不轮换/任何用量都告警。所有 agent 用同一套公式，差异只在 handoff 执行方式。
- **master/retro 自动换脑**：达到 ceiling 且 `idle/done` 时走现有 `Fresh` 管道；
  `working/blocked/unknown` 不动。`handoff.md` 增补受控 agent 的上下文、brief、
  progress、结果路径。
- **job 默认 assisted**：达 ceiling 只告警；主控先 `checkpoint` 把进度写进
  `progress/<agent>.md`，再 `handoff`。`done` 可能是在等用户，自动重启会变成循环
  提问，所以默认不自动交接。
- **交接物是机器真值**：`dispatch --local` 在起 agent 前归档完整
  `briefs/<agent>.md`；progress 固定小节；结果路径稳定。`handoff` 停旧 agent 后用
  brief + progress 重新拉起（新 agent、新会话），结果仍写回原路径；旧条目用一次 Replace 原子替换，
  台账路径随在途条目携带（跨午夜/多次交接不新建第二份），每次交接在台账记一笔。当前实现统一走「停旧起新」的通用路径，不依赖任何 harness 的同名会话能力。
- **拒绝条件**：working（会砍回合）、blocked（先找用户）、无 brief、无 progress、
  结果文件已就绪（直接收口）、window unknown（不自动判定）。
- **监督兜底**：tick 对账在途条目 × agent list × 结果文件；agent 消失但结果就绪 →
  提醒收口；消失且无结果 → 提醒重派；working 但会话文件长时间无写入 → 停滞告警。
  只告警，不自动 cancel。派活加幂等键，同一能力 + 正文 + 附件组有活跃条目时拒绝重复派。
- **压缩对齐**：omp 默认开启自动压缩（`compaction.enabled/midTurnEnabled`）。运行时
  ceiling 必须压在 omp 阈值之前，或对 master/retro 关闭 omp 自动压缩；否则会在
  runtime 还没 handoff 时被 harness 悄悄压缩。`compactionEpoch` 变化时重置告警状态。

**Considered Options：**

- **继续用会话字节数**：拒。与模型窗口、cache、JSON 开销、压缩全部无关；1.9 MB 的
 会话可能只用 12%，也可能在 200k 窗口上已接近上限。
- **让每个 agent 自报「我快满了」**：拒。agent 看不到可靠 token 数，也没有安全停/起
  会话的手段；handoff 必须由运行时执行。
- **job 达阈值自动换脑**：拒。`done` 不代表任务可续，可能在等用户；没有 progress
  契约时自动重启只会重复任务并制造双份副作用。
- **只做 per-harness 阈值**：拒。阈值、告警、交接、状态应该共用；每个 harness 只
  贡献一个读取 usage 的薄 adapter/通用 command probe。
- **平台任务也监控上下文**：拒。本机看不到远端会话；平台运行时自己负责重试/压缩，
  主控只按任务状态、run 状态和任务号处理。

**Consequences：**

- `internal/ctxmeter` 成为唯一度量入口；`status`/tick/`done` 事件/`handoff.md` 都消费
  同一 Snapshot，不再各自算字节。
- 换 harness（如 opencode）只需新增 adapter 或配置 `context_probes.<kind>` 命令，
  阈值、告警、checkpoint、handoff、验收用例不变。
- 本地作业多两份机器真值（brief/progress）和一个稳定结果路径；崩溃或交接后不再依赖
  主控的对话记忆。
- 巡检会多两类告警（上下文、僵尸/停滞），可能增加主控唤醒；因此全部按 agent +
  会话 + 级别去重，且急停期间延后补交。
- omp 的自动压缩与运行时轮换存在阈值竞争；部署时必须显式对齐，不能假设两者互不干扰。
- 非在途 agent 仍不在治理范围。用户要管它们，先把它们纳入 `superctl dispatch --local`
  的在途身份，而不是让主控凭 pane 名猜。
