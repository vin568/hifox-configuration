# ADR 0028：首条注入必须校验送达，初始化回合保持静默

`herdr agent prompt` 的成功只代表「文本 + 回车已写进终端」，不代表 agent 开了回合（herdr 文档明确写了这一点）。omp 启动窗口里这次回车会被吞，整段手册停在输入框；而 `primeAgent` 只看到提交成功就写了 `primed/<agent>` 标记。结果：**标记说已 prime，会话里却没有手册**——2026-11-18 05:31 之后的四个主控会话全部如此，宋江连续一天多按「一个带全套工具的 coding assistant」行事，而不是按手册派活。用户后来在 `superctl new` 之后亲眼看到输入框里那坨未提交的手册文本。

**决策：**

- **首条注入带固定 banner**：`[初始化注入] ...`。它同时是送达校验的锚点与初始化回合的静默指令：这条注入本身没有任务，读完即止，不回复用户、不调工具；换脑/急停追加的指令在后面，按「以那段为准」执行。
- **`primeAgent` 先校验再落标记**：
  1. 提交前去掉尾部换行（避免 TUI 把末尾换行当成粘贴内容）；
  2. 轮询该 agent 的会话文件，确认 banner/首行真的落进去；
  3. 没落进去就补 Enter（最多三次，每次等 `PrimeWait/3`；不按的情况：`blocked`——那是在替用户答审批框；长等落盘后仍 `working` 的真回合；复检时状态未知（list 抖动）。见 2026-11-20 修订。启动窗口 herdr 可能报 `working`，此时输入框里就是我们刚提交的文本，不能因为非 idle 就放弃）；
  4. 仍没落进去就报错、**不写标记**，下一轮 `ensure` 重试。
- **标记记录会话路径**：`primed/<agent>` 写成「时间戳 + 会话文件路径」。`ensure` 只在标记与 agent 当前会话一致时才认为已 prime；老格式（只有时间戳）视为不属于当前会话，在 agent `idle`/`done` 时补一次手册——这样历史上丢过手册的会话会被自动修复。
- **静默不靠屏蔽输出**：用户只从 `outbox`（`superctl reply`）收到消息，agent 在 pane 里的聊天输出本来就不面向用户。banner 的作用是让初始化回合不空转、不误触发工作，不是遮挡输出。

**2026-11-19 现场修订：** 首版把「会话文件尚不存在」当成已送达（`os.ReadFile` 出错直接放行），只补一次 Enter 且要求 `idle`/`done`；13:43 的 `new` 现场证明 omp 的 paste chip 会吞掉紧随的 Enter，而 herdr 此时可能报 `working`。改为文件不存在继续等、最多三次 Enter、只跳过 `blocked`；13:52 用同一命令验证通过（会话文件里出现 banner，agent 进入「已读，待命」）。

**2026-11-20 现场修订（启动慢与落盘延迟）：**

1. **落盘延迟不是卡住**：omp 在首个 assistant 消息出现前不写会话文件（实测 `new` 在 04:25:05 提交、04:25:09 文本已进会话、04:25:24 才落盘，18.7s 晚于旧的 18s 预算）。现在 `SubmitPrompt` 若在等 Enter 时看到 `working`，先判「文本多半已进输入框、只差落盘」，用 `PrimeWorkingWait`（90s）继续等文件，期间不补 Enter（往在跑的回合里塞键只会干扰它）。等不到后**复检状态**：仍在 `working` 说明是真回合、`blocked` 说明弹了审批框、list 抖动说明状态未知——这三种都不塞键，报错交给下一轮；确认已不在 `working` 才回到补 Enter（启动窗口的 `working` 可能是文本停在输入框的假象）。长等只消费一次，补 Enter 救回真回合后再次看到 `working` 直接报错，不再塞键。**绝不把 working 当送达**。
2. **冷启动拒绝要重试**：omp 起得慢时 herdr 已把 agent 列进 `agent list`（`interactive_ready=false`），但 `agent prompt` 仍以 `agent_not_found` / `agent_not_ready` 整体拒绝（实测复现）。herdr 的提交语义是「文本 + 回车都写成功才报成功」，`agent_blocked` 明确在写输入前拒绝；`agent_not_found` 是目标解析阶段拒绝，`agent_not_ready` 是就绪检查阶段拒绝，都早于写输入（herdr `validate_agent_prompt` 在 `queue_user_input_submission` 之前返回）。`submitPromptText` 重试到 `SubmitReadyWait`（90s）：`agent list` 瞬时失败继续重试，只有 agent 不在列表里才立刻失败；重试期间转 `blocked` 时再提交一次，让 herdr 给出真实的 `agent_blocked`，`dispatch` 才能按 `IsBlocked` 把审批框交回用户。
3. **`agent start` 超时只对 omp 放宽到 120s**（herdr 默认 30s）：一条成功 `agent start` 只在 herdr 检测到该 agent 可交互输入后返回；omp 冷启动（加载配置/模型）偶尔超过 30s。别的 kind 仍按默认 30s 失败，避免起不来时把 dispatch/handoff 白拖两分钟。另：start 期的 `agent_not_ready` 是「启动时卡在审批/提问界面」（herdr 原文），与 prompt 的未就绪同名不同义；`Ensure`/`startLocal` 把它翻译成「需要用户先处理」的错误，而不是普通启动失败。

**Considered Options（修订）：**

- **用 `agent prompt --wait` 代替校验**：拒。它等的是 lifecycle 状态落定，可能等一整轮，而且仍然不证明文本进了会话；herdr 的 `agent_prompt_stalled` 只证明「没观察到 working」，不能区分「输入框卡住」与「已提交但很快结束」。
- **无条件补发 Enter**：拒。输入框里可能正好有用户自己的草稿，盲按回车会替用户提交。只在刚提交首条注入、且状态不是 `blocked` 的启动窗口里补，范围最小；启动窗口报 `working` 不构成拒绝理由（见现场修订）。长等落盘失败后若仍在 `working` 则不再补（真回合），避免干扰在跑的任务。
- **失败后原地重发 prompt**：拒。提交成功但没送达时，文本若还停在输入框，重发会变成两份；交给下一轮 `ensure` 重试更干净。仅当 herdr 在写输入前整体拒绝（`agent_not_found` / `agent_not_ready`）时允许原地重试——那不会产生第二份文本。
- **对话输出层面做静默**：拒。pane 输出不是用户通道，真正要防的是初始化回合调用工具或回复，banner 已覆盖。
- **不记录会话路径，只校验 banner**：拒。同一 agent 换会话后标记仍会骗过 `ensure`；会话路径是唯一能把「哪一轮已 prime」钉死的东西。
- **把「working」一律当成已送达**：拒。working 也可能来自 agent 启动时的其他活动，落盘才是唯一证据；这里只是把预算从 18s 放宽到 90s，没有放弃校验。
- **在 `agent start` 后用 `interactive_ready` 轮询代替 prompt 重试**：拒。`interactive_ready=true` 与「prompt 可写」之间有观测不到的间隙（实测 start 返回后立刻 prompt 仍被 `agent_not_found` 拒绝），只有真正写成功才算数。

**Consequences：**

- `ensure` 最坏会在一个卡住的 pane 上多等 `PrimeFirstWait + PrimeWait`（默认 3s + 15s）；若是 `working` 等落盘，最坏 `PrimeFirstWait + PrimeWorkingWait + 3 × PrimeWait/3`（默认 3s + 90s + 15s）；若是冷启动，`agent start` 最多 120s、prompt 最多再等 `SubmitReadyWait`（90s）。换来的是「没送达就不写标记、下一轮必重试」，不会再把主控放进无手册状态，也不会把「起得慢」或「落盘慢」误报成「输入框卡住」。
- 校验依赖 herdr 报出的 `agent_session.value`（omp 的会话文件）。非 path 形态或拿不到路径时按已送达放行，退化为旧行为——这是能力边界，不是静默容忍。
- 历史会话会被补一次手册：banner 让它读完即止，不会额外执行工作。
- 已经跑着的 `superctl serve` 是旧二进制，必须重启后才用上新的 prime 路径；重启由用户执行。
