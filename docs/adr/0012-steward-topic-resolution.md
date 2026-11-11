# 吴用主题判定：主题是路由键、实例不是；判定收在有序阶梯与一处长尾 skill

吴用（`agents/wuyong`）原来只判两件事：主题（`instructions.md` §1 的闭集与「判定特征」列）与意图（§1.1 / ADR 0006）。缺的是**指代消解**——飞书是持久多轮会话，用户下一句常是「那个 500 再看下」「就按你说的建吧」，本条消息里没有任何项目词；而 §1 表只描述「消息里出现什么词」，没有「没词时按什么顺序回退」的规则。三个后果：

1. **多轮指代无解**。§1.1 第 5 行要求「对具体提议的确认」才交付，但提议本身不落台账，`/new` 或几轮之后无从判定指代的是哪个主题。
2. **澄清无规格**。§1.1 第 6 行只写「最多问一个能改变动作的问题」，没写问什么、给不给选项、选项从哪来；`/new` 之后连「上一轮问过什么」都没有。
3. **混轴**。品牌 / 区域与主题被当成一件事：现行 §1 把「apifox」「Apidog」当 `apifox` 主题的信号、「apidog 文档页」当 `docs-site` 的信号，而 apifox / apidog / apidog-eu 是同一项目的三个部署实例（跨主题共用），品牌词定不了主题。更糟的是 §1 的 docs-site 行把「站点配置」列为判定特征——把**应用内的文档站设置**吸到了文档站本身。

**决策：把判定拆成三轴，主题与实例分开判，并给判定配一条有序阶梯与一处长尾 skill。**

| 轴 | 值域 | 路由键 | 消费者 |
|---|---|---|---|
| 主题 | `xwing` / `x-deploy` / `apifox` / `docs-site` / `apifox-cli` | **是** | 堂 / 舵 condition、任务承接人、任务落哪块板 |
| 意图 | 咨询 / 交付 | 是 | §1.1 / ADR 0006（不变） |
| 实例 | 品牌 × 区域；私有化 `PDV/<产品线>` | **否** | `context` 前缀、任务正文、台账字段、澄清问句 |

### 判定阶梯（有序，命中即停）

`instructions.md` §1.0 的五行表：① 本条消息强信号词唯一命中 → 依据=显式词；② 指代台账对象 → 依据=指代事项；③ 本会话焦点（**仅限咨询**）；④ 台账未完结事项主题唯一（**仅限咨询**）；⑤ 其余 → 一次澄清（§1.2 问句闭集）。三条纪律：显式纠偏最高优先；**交付只能来自 ① / ②**（或一个带 `主题` 字段的 `等待用户` / `进行中` 事项）；判定依据分轴入账（`主题=` / `实例=`），误判可复盘——沿用 ADR 0006 对「模型自报置信度」的同一立场。

### 术语与位置

- **主题判定（Topic Resolution）**：主题与实例判定的合称；细则收在**新的 Agent Skill** `wuyong/topic-resolution`（弱信号与易混反例、品牌别名、切分问句库、判定用例表）。
- **常驻骨架在指令**：阶梯与强信号词留在 `instructions.md` §1.0（每条消息都要走的问题不能靠「记得去读」），skill 只在**步骤 ① 未命中**时读——读取成本与歧义量成正比，且两侧**零重叠**（skill 不重复任何强信号词）。
- **结果落台账**：`ledger` skill §6 事项模板新增 `实例` 与 `判定依据`，`主题` 字段沿用；`归属会话` 由「本会话」改为真实 `chatSessionId`。

### 实例的证据分级

| 主题 | 实例 | 证据 |
|---|---|---|
| xwing | `hifox` / `sharkly` | `README.md` 部署实例清单；`config/hifox/agents/linchong/instructions.md:379`（`PRODUCT=hifox` 构建期变体）、`:347`（新语言只在 Sharkly 上线） |
| docs-site | CN / GLOBAL / EU / **PDV** | `config/apifox/agents/xiaorang/instructions.md:77`（`APP_REGION` CN / GLOBAL、`APP_DATA_CENTER_LOCATION` EU、`IS_PDV`、`IS_PRIVATIZATION_DEPLOYMENT`）、`:185`（区域变体 CN / GLOBAL / EU / PDV） |
| apifox-cli | `apifox-cli`(CN) / `apidog-cli`(GLOBAL) | `config/apifox/agents/tanglong/instructions.md:41`（双发行包与站点）、`:210`（bug 字段叫「发行形态」）、`:189`（未指定产品时按语言默认 → 交付留空会被静默猜形态） |
| apifox 产品 | `apifox` / `apidog` / `apidog-eu` / **PDV** | 用户口述；本仓仅有范围痕迹（`config/apifox/agents/yanqing/instructions.md:148`、`loki:189` 的私有化影响面）→ 落 GLOSSARY 时标注来源 |

品牌与区域是同一个东西的两种说法（`apifox`↔CN、`apidog`↔GLOBAL、`apidog-eu`↔EU），不设两轴，否则澄清问句会重复问同一件事。实例进 `context` 首行前缀，**不新增 `input` 键**——两个中枢图里没有任何 condition 消费实例，加键只会把「归档 + 重新 import」（HF-15237）的成本摊到收益为零的改动上。

### `/new` 的平台事实（决定「提议必须落账」）

`/new` **轮换会话**（HF-12055，`server/internal/handler/feishu_new_conversation_test.go`：前缀会话的 IM 绑定被标记 `superseded`、后继会话 transcript 为空、水位时间随之推进）。所以：

- 阶梯级 ③（会话焦点）在 `/new` 之后**不存在** → 落回级 ④ / ⑤；「跨 `/new` 恢复焦点」不能靠会话身份。
- 会话 id（`chatSessionId`，即 `hifox chat view` 的 `id`）只作**来源追溯**：agent run 只能读自己绑定的那个会话（`server/internal/handler/chat.go` 的 `agentRunMayReadChatSession`，run 以 Agent 身份、而非创建者身份授权），所以 `/new` 之后旧会话对吴用**不可读**，不构成恢复路径。
- 由此「咨询结论里的建任务提议」必须物化为 `等待用户` 事项（`ledger` skill §1）——这是「建吧」可判定的唯一承载。

## Considered Options

- **判定细则并入 `ledger` skill**：拒。`ledger` 是前台与后台巡检**共用**的协议（`automations/wuyong-ledger-sweep/description.md` 第一步就要求「加载并遵守 agent skill `ledger`」），而巡检明确不派发、不建任务——给它词表是噪声加越权诱因；且 `ledger` 每轮必读，判定长尾只在歧义轮需要，合并会把最热路径的常驻成本抬高。
- **判定细则全留 `instructions.md`**：拒。长尾（弱信号 / 别名 / 问句库 / 用例表）会把指令推过预算，且判定细则与执行纪律混在一处、diff 变噪；反过来，只要词表能压到几十行，这一选项是合理备选（本决策不禁止将来回并）。
- **实例做成路由键或新增 `input` 键**：拒。无 condition 消费者；两座中枢图改动受 HF-15237 约束（归档 + 重新 import），收益为零。
- **填 `topic` 二级字段表达实例**（`{"topic":"apifox","instance":"apidog"}`）：拒（同 ADR 0009 对 `surface` 的拒绝）。主题闭集是**多同步面**不变量，二级结构要求每一处都懂它；实例只需自由文本。
- **主题粒度细到仓库**（每仓一个键）：拒。仓库数 × 变换会让闭集膨胀；且吴用按纪律不读代码仓库（`instructions.md` §7），「主要改动落在哪个仓」对它是不可观测量——归口只能靠用户词汇代理，见 §1.2 的切分问句。
- **每会话一份「焦点」文档**：拒。违反 ADR 0005 的「禁止共享『当前状态』文档」，且每轮一次整文替换的写放大换不来信息量（焦点可从台账事项派生）。
- **靠模型自报置信度决定是否澄清**：拒（同 ADR 0006）：不可审计、不可回归；阶梯可逐条核对，并配 `topic-resolution` skill §7 的判定用例表。
- **把「部署」保留为 `x-deploy` 的判定特征**：拒。裸「部署」会让「私有化部署的接口站挂了」这类描述误命中——判定特征收紧为部署工具 / 配置仓库 / `apply` / `state` / 环境绑定 / 子模块指针。

## Consequences

- **主题闭集同步面（当前权威清单）**：堂 / 舵 condition（含 `reject.sh` 闭集文案与 workflow description）、吴用路由表 + §4 委派映射、**判定词表**（`instructions.md` §1.0 + `topic-resolution` skill）、舵登记表（`branch-hall-dispatch` §1）、`ledger` 事项模板的主题枚举、`GLOSSARY` 定义。相对 ADR 0002 的「三处」与 ADR 0011 的「四处」，本次新增判定词表并统一了口径；历史 ADR 不改写。加主题而漏改词表的失败形态不是「派了没人接」，而是**新主题永远判不出来**。
- **新增平台资源**：Agent Skill `wuyong/topic-resolution`（`config/hifox/agents/wuyong/skills/topic-resolution/`，单归属），`state.yaml` 已登记 UUID。判定细则缺失时退化为「阶梯 + 强信号词」仍然可用（词表命中即不必读 skill）。
- **`/new` 不再是「焦点还在但没读」**：级 ③ 失效、级 ④ / ⑤ 兜底；「提议必须落账」成为硬规则，咨询结论末尾的提议一律进 `等待用户` 文件夹。
- **交付门槛变硬**：建任务自检增加「主题来源可追溯」与「实例已显式声明」（`instructions.md` §1.1 / §4）；CLI 类交付缺实例会被承接人按语言默认猜发行形态，故 `未指定` 也必须显式写出。
- **实例只进三层**：`context` 首行前缀、任务正文、台账字段；**不改** Space、承接人、落板（apifox 系任务仍恒落 Apifox 产研任务板）。
- **私有化客户信息**：台账与任务正文默认写客户代号或版本，真实客户名仅在用户明确要求时写入（`ledger` skill §6 的「永不入账」条目下追加）。
- **巡检不参与判定**：`wuyong-ledger-sweep` 不读 `topic-resolution`、不填不改 `实例` / `判定依据`（`ledger` skill §6）。
- **已验证（原「未验证项」收口）**：agent 运行时读不到退役会话——`agentRunMayReadChatSession` 只允许 run 读 `run.ConversationID == session.ID` 的那个会话（HF-12918 注释：run 以 Agent 身份授权，永不以创建者身份）。因此不保留任何「回读旧会话」兜底，跨会话恢复只靠台账级 ④ / ⑤。
- **词表的权威宿主**：apifox 系实例（`apifox` / `apidog` / `apidog-eu` / `PDV/<产品线>`）本仓无明文，本次按用户口述落 `GLOSSARY.md` 并标注来源；xwing 侧实例与工具仓 `cli.app` 校验闭集同源（GLOSSARY-MAP 的归属判定不变）。
- **部署状态**：`hifox-staging` **已 apply 并 in sync**（`validate` 通过；`plan` 为 `no changes · 19 in sync`，含新 skill 与巡检 automation；`state.yaml` 已登记 UUID）。`hifox-prod` **未 apply**，需显式 `./x-deploy apply --apply --environment hifox-prod`。
- **会话 id 的暴露面（已核）**：`归属会话` 会把 `chatSessionId` 写进 Space 共享台账，但 transcript 另受门槛保护——普通读取只认创建者 / 所有者 / `in_list` 参与者 / DM 对端 / 任务可见者（`callerMayUseChatSession`），agent 只认自己绑定的会话（`agentRunMayReadChatSession`），人类只读旁路还要求 agent 可访问 + 个人 run 记录可见、私有会话需 `manage`（`agentRunRecordReaderAgentID`，HF-15134 / 15360）。结论：台账里只有无内容的句柄；仍按内部句柄对待（只记 id、不记会话内容）。
