# 主控控制面 CLI-first：无中间守护进程，注入前幂等引导恢复

宋江的操控对象是 herdr 能力 agent（本地通道）与 hifox 平台任务（云端通道）。两边命令面都已齐备：herdr 的 agent-automation 就是「外部程序注入 prompt、查询进度」的官方设计用例（具名寻址、`agent prompt` 异步提交、状态机 `agent wait`、终端读取、事件订阅）；hifox CLI 的 `task create --delegate-agent` 委派即触发、`task view` 是状态真值、`task cancel-run` 取消。

**决策：**

- **宋江的工具箱 = `herdr` / `hifox` / `superctl` / 台账与结果文件**，直接跑 CLI，不建 supervisor 守护进程：队列、重试编排、状态对账是宋江（LLM）+ 作业台账的职责，不重复 herdr 已有的机构。
- **结果落盘约定**：能力 agent 完成后把结果写成 markdown 文件、只回路径，宋江读文件——herdr 官方兜底姿势，规避「wait 只懂 lifecycle 不懂 turn」的坑。
- **重启恢复 = 注入前幂等引导**：注入器与巡检 tick 每次行动前先 `herdr agent list` 与作业台账对账——宋江缺席就 `agent start`（omp 集成原生 resume 会话）、常驻能力 agent 缺失就重建——然后才注入 prompt。不另做 watchdog 进程；herdr server 重启失忆（agent 名、进行中 wait、订阅不跨重启）由这一条路径统一兜住。
- **blocked 一等处理**：能力 agent 启动时尽量预授权（`agent start` baked 原生参数）；出现 blocked 宋江不自动按键，私聊上报用户，用户批准后才 `agent send-keys`；宋江自身 blocked 时注入器收 `agent_blocked`，转私聊通知。
- **会话拓扑**：独立命名 herdr session；宋江独占一个 workspace，常驻能力 agent 一个 crew workspace，动态作业一个 jobs workspace（每作业一 tab，同时最多 3 个动态 agent，完成后收 tab）。

**Considered Options：**

- **中间 supervisor 守护进程（宋江只对它说话）**：拒。多一个移动件重复 herdr 已有能力，还要自带 API 与状态存储。
- **能力 agent 为可对话 peer**：拒。无对应机制且引入协商延迟；委派 + 落盘结果的语义已够。

**Consequences：**

- 无内置队列：飞书消息、巡检 tick、事件唤醒经同一注入通道顺序进入宋江会话，「边聊边推进」靠底层 CLI 的输入缓冲承载；宋江被 blocked 时注入硬拒绝，注入器侧排队重试。
- 落地前置：本机 herdr 从未初始化、omp 的 herdr 集成未装（`herdr integration install omp`；注意 Pi/OMP 集成目录冲突规则）；主控底层 kind=omp + 快模型。
- 事件驱动巡检依赖 herdr plugin `[[events]]` 钩子（`pane.agent_status_changed`）唤醒宋江；云端任务无本地事件，只能定时 sweep 轮询——事件加速只覆盖本地通道。
- **落地后修正（2026-11-17）**：herdr 事件订阅按 pane 进行、不支持通配，plugin 钩子方案弃用，改为 serve 内常驻 socket 订阅（订阅面 = 除主控外全部带 agent 的 pane，周期重建收敛动态增删；载荷形状与解析见 `internal/herdr/events.go` 注释与测试，不在本 ADR 复制）。云端及时性由**云端哨兵**补齐：跟踪清单（主控维护）+ 私有快照（哨兵独占）双文件无写竞争，纯 CLI 高频轮询、首见记基线、状态真变化且注入成功才唤醒（周期见 `config.example.toml`）；巡检 tick 降为兜底。另立治理禁令：主控不得改动/重编/重启 super-agent 运行时自身（落地首日它曾自行重编二进制自救回话通道，虽成功但属运行时自改，不可接受），写入宋江操作手册直连节。评审双 P0（CLI 分发漏注册 reply、prime 失败永不补）与哨兵注入失败快照过推进等缺陷的修复与回归用例见对应提交。
