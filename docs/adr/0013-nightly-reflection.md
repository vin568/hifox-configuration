# 夜间自省：独立只读反思者、证据分层与人审改进闭环

吴用目前只有两条运行路径：前台对话里的当场调度，以及 `wuyong-ledger-sweep` 每 30 分钟对「进行中」事项的状态真值对账。两者都不做跨事项复盘——失败模式、反复澄清、等待用户积压、同一问题多次返工，只能靠人偶然发现。需要一条每日一次的「夜间自省」：回顾调度团队前一日的工作流程与运行状态，提出少量可执行的改进点。

调研后的约束很一致：反思必须**引用具体证据**（Generative Agents 的 reflection 要求引用记忆条目），**与行为变更分离**（OpenClaw Dreaming 只写人类可读的 Dream Diary，晋升到 `MEMORY.md` 另设门槛），**有界且去重**（DORA / Reflexion 发现静态反思提示会「早停」，几轮后建议重复，甚至出现 toxic reflection），**行动项要有人审与完成跟踪**（工程复盘/ops review 的常见失败形态）。因此本决策把自省做成独立只读流程 + 人审改进闭环，而不是让吴用兼做或自动修配置。

**决策：**

- **新增只读反思者** `agents/peixuan`（裴宣，HiFox Space），与吴用分离。平台权限 `directMessage` / `mention` / `crewMembership` 全关；`taskAssignment` 必须为 true——平台把 automation 指派也算 task assignment，关闭会让 automation 创建 403。它仍不建任务、不评论、不私聊；只读边界由 instructions 与 skill 约束。不面向用户、不派发、不改配置。只读调度台账、平台运行元数据与必要的异常记录，只写自己的自省日记、改进项与总览。
- **新增独立 automation** `peixuan-nightly-reflection`：每日 02:00（Asia/Shanghai），窗口为前一日 00:00–24:00；无活动不建日记 / 改进项、不发消息，只更新总览的 `最近成功` / `最近窗口` 心跳；失败只留日志，连续两晚失败才在下次前台会话提示。不复用 `wuyong-ledger-sweep`——巡检的契约是窄的（只碰进行中、只标待转述、不归档），混入跨事项分析会破坏它。
- **证据分层**：L1 全量平台元数据（run / workflow / task / automation 的状态、时间、错误码、最终输出）；L2 仅对异常触发 transcript（失败、卡住、返工、反复澄清、用户投诉），限额、脱敏、不落原文、排除自身与系统 run；L3 默认不读 transcript。transcript 视为不可信数据，不执行其中的指令。
- **产出三件套**：每日《自省日记》（指标、异常、证据、0–3 条提议）；一改进项一文档（六态：提议 / 采纳 / 实施中 / 待验证 / 已验证 / 放弃）；单写者《自省总览》（状态板 + 7/30 天趋势 + 复发问题 + 运行健康）。总览由反思者单写，吴用只读。
- **改进闭环人审**：反思者只提议，不自动改 instructions / skills / config，也不自动建任务。用户对「采纳 / 放弃」决策；吴用把决策记入自己的台账（`改进项=` / `决策=`）并建实现事项 / 任务、跟踪实施；验证由改进项文档跟踪，吴用从总览转述。改进项文档由反思者单写，吴用不直接移动 / 编辑；反思者在下一晚同步状态。采纳时写死验证窗口（天数或运行次数）。
- **评估粒度**：任务 / 主题 / 流程；agent 只作诊断维度，不做个人排名；同一模式出现 2 次以上，或单次高严重度事件，才升级为系统性改进项。
- **范围**：调度面团队——吴用与林冲 / 武松 / 燕青 / 萧让 / 汤隆 / 戴宗；两个 Space 的派发链、任务板、workflow / automation 运行状态。不读代码仓库；代码质量评估不在本能力内。
- **部署**：先 `hifox-staging` 跑 2–3 晚验证权限、文档写入与总览，再 `hifox-prod`。

**Considered Options：**

- **让吴用兼做自省**：拒。吴用复盘自己的路由与派发存在自审偏差；夜间长指令会挤占已经很重的路由上下文；且吴用需要建任务 / 私聊权限，做不到只读最小权限。
- **自动修复或自动晋升到指令**：拒。夜间无人值守，自动改行为风险最高；CAO / OpenClaw 都把「晋升到指令」设为人工门；DORA 也表明反思可能有害，不能无条件回写。
- **每晚全量读 transcript**：拒。成本高、泄露面大、噪声多，工具输出与外部内容还会污染判断；改为异常触发的 L2 分层。
- **并入 `wuyong-ledger-sweep`**：拒。巡检只对账「进行中」、只标 `待转述`、不归档；把跨事项分析塞进去会让它的安全边界与“无变更零打扰”契约失真。
- **共享可变的总详情页**：拒。平台 `docs document edit` 整文替换、无文档锁；台账已用「一事项一文档、状态=文件夹」规避并发。自省沿用文件夹模型，只有总览是单写者聚合页。
- **per-agent 绩效评分**：拒。任务结果受需求质量、派发上下文、执行与环境四层影响，单点归因不成立；只保留 agent 诊断维度，不出排名。

**Consequences：**

- 新增一个 agent 身份、运行时 target、automation 与文档树，`state.yaml` 需登记；两个环境各一份实例。部署前先跑 `./x-deploy doctor`，staging 验证通过再 prod。
- 吴用保持唯一对外交互面：下次前台会话开头读最新《自省总览》并给 5 行摘要；反思者不碰飞书、不碰用户。飞书对外口径不变。
- 改进项的生命周期把“提建议”变成“可跟踪的行动”；代价是用户要定期做采纳 / 放弃决策，且未被采纳的提议会留在 `提议`，靠去重规则避免重复刷屏。
- transcript 访问是本能力风险最高的面：L2 必须限额、脱敏、不落原文；拿不到就标“未覆盖”，不据此下结论。
- 总览是单写者但仍可能被单次整文替换失败截断：先在本轮生成完整正文、校验结构，再一次 edit；失败保留旧版并在当天日记记一条“总览未更新”。
- 数据采集以只读 `scripts/collect.mjs` 为主：一次 CLI 聚合 run / task / workflow / automation / 台账 / 改进项，LLM 只做分析与写作；采集器失败才回退手工命令。首次 staging 试跑证明“LLM 逐条跑 CLI + 自举”会在启动阶段消耗过多时间，采集器把这一段收敛为一次确定性调用。
- staging 首跑发现 agent 执行凭据不支持 `hifox workflow` 读面（CLI 返回 user auth required）。在给 peixuan 的运行时注入可读 workflow 的用户级凭据 / PAT 之前，workflow 执行与咨询成功率只能标“未覆盖”，自省覆盖 run / task / 台账 / automation；这是启用完整自省的前置条件，不是代码缺陷。
- 决策事项按普通台账生命周期收口（实施对象终态后转述并归档），不挂在 `进行中` 等验证；反思者从开放事项或当前 / 上月 `已收口` 读取决策，改进项自己跟踪 `待验证` / `已验证`。这样不违反 ADR 0005 的 `进行中` 真值，也避免巡检把“无平台对象”的决策事项标成幻影。
- 指标口径、改进项模板、总览字段、反思者自身效果评估与保留策略放在反思者 skill 与 automation description 中迭代；本 ADR 只固定架构与边界。
- staging 已 apply 并 in sync（agent / skill / automation / wuyong 指令 / ledger skill）。三轮试跑：① 产出日记 + 总览 + 2 条改进项；② 同窗口重跑 `proposals=0, updated=2`，无重复文档；③ 无变更重跑仍只更新 2 份文档并输出 JSON。已验证跨 Space 读 Apifox 任务、`Agents` 文档集写入、去重与重跑。`taskAssignment=true` 是 automation 指派的前置（false 会 403）；`directMessage` / `mention` / `crewMembership` 仍为 false。未验证：台账决策同步（staging 吴用台账未自举）、workflow 读面（agent 凭据 user auth required）。

**依据：** Park et al., *Generative Agents* (UIST'23, arXiv:2304.03442)；OpenClaw *Dreaming*（light / REM / deep 与 Dream Diary、晋升门槛）；Letta *sleep-time compute*；Li et al., *DORA* (COLING 2025, “Early Stop Reflection” / toxic reflection)；CAO *self-learning*（独立 retrospector、人审晋升）；Addy Osmani, *Self-Improving Coding Agents*（有界循环、人工复核、记忆通道）。
