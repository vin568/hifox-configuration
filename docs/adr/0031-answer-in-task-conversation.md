# ADR 0031：承接人提问的回答位置是任务对话，由 `superctl answer` 投递

平台任务执行 agent（承接人）在任务对话里提问、把会话置成 `waiting_human_reply` 后结束回合。
用户回话在飞书里发生，主控要把这段原话送回**承接人正在跑的那条任务对话**——回答落在
任务评论里，承接人不会被唤醒，等于没答。

这条规则此前只靠人在每条回答前手写「在对话中回复，不要在任务评论中回复：」。运行记录
（HF-16187）显示后果：

- 前缀一旦漏写，主控手上的通道清单里只有 `dispatch --update`（改正文）、`task comment`
  与 `task summon`，没有一条能「以用户身份说一句话」——选哪条都是错的。
- 2026-11-20 两次真实投递（02:53、03:04）是派活员临场拼的：从 `~/.hifox/config.yml`
  grep 出人工 token，`curl POST /rest/v1/chat/sessions/<id>/messages`。这既是读凭证
  （ADR 0019 硬禁区），也是「绕过 superctl 对外发消息」，而且依赖派活员临时被拉起来。
- `hifox chat send` 不能替代：它打 `/rest/v1/chat/sessions/<id>/agent-messages`，只接受
  会话绑定的 agent 执行身份，人工令牌 403 `FORBIDDEN: not your chat session`（同一实测）。
- 派活员手册原写「补充信息只写任务评论」，把「回答」与「补需求」混成同一格，助长了错位。

**决策：**

- **任务对话是回答的唯一位置，且必须是「活着」的那条会话**。`task view` 的
  `chatSessionId` 是与当前执行者最老的会话，多会话任务里活着的可能是别的会话
  （`sessionStatus.top.sessionId`：等回答、等验收、或正在跑）；投错会话，承接人不会被
  唤醒，补的话也进不了它当前的上下文。没有会话汇总时才回落 `chatSessionId`。任务评论不产生
  承接人的回合（不唤醒、不进它的上下文）；正文是需求不是对话；`task summon` 会并开第二份
  执行（HF-15793）。**没有「看情况写评论」这一档。**
- **命令面只提供对话通道，平台是显式参数**：`superctl answer --platform <平台> --task <任务号>
  --text "<原话>"`（多行用 `--text-file`）。平台名与 `dispatch --platform` 同一口径（平台 CLI
  应用名），目前只有 hifox 有实现；别的平台名如实拒绝，不拿 hifox 顶替。回答实现读
  `hifox task view` 的 `sessionStatus.top.sessionId`（最该推进的会话，缺失时回落
  `chatSessionId`）与 `organizationId`，经
  `hifox api post /rest/v1/chat/sessions/<id>/messages` 以 CLI 自己的登录身份写入，返回
  `message_id` / `run_id` / `queued` 与投递前的 `agent_working_status`。命令**没有**评论
  选项、没有「是否改写」选项——原话原样投，位置没有第二选。
- **身份不靠抓 token**：走 CLI 已登录的人工身份，主控与派活员都不读 `~/.hifox/config.yml`。
  `superctl reply`（回用户）与 `superctl answer`（回承接人）是仅有的两条对外/对平台发消息
  通道，硬禁区闭集随之更新（ADR 0019 的措辞按这两条收紧）。
- **编排者自己知道位置**：任务进入 `waiting_human_reply` / `waiting_human_review` 时，平台
  哨兵的注入文本点名「<任务号>（平台 <名>，等你回答/验收）」并给出带平台的 `superctl answer`
  命令，主控不必依赖用户每条消息再强调一遍。等待态在**首见**时也要报（部署前就在等、或派活
  时已在等）：首见记基线只适用于非等待态，否则最需要提示的时刻恰好人静默。用户仍带「在对话
  中回复」这类前缀时，主控剥掉前缀再投——那是位置说明，不是回答内容。
- **规则写进两份手册**：宋江手册新增「平台任务的问答（回答位置）」节（何时读问题、剥前缀、
  口误只投最后一条、`queued=true` 要照实转述、带图请用户直接在平台补），`platform-dispatch`
  技能补通道与禁止项；朱贵手册删掉「补充信息只写任务评论」的默认，改为「回答不是补充信息、
  不走 `--update`、不写评论」。用户因此不需要在每次回答前加前缀。
- **没有会话就报错，不退回评论**：会话汇总没有 top、`chatSessionId` 也为空时
  `answer` 当场拒绝并说明（不臆测「承接人没跑过」）；绝不「凑合写评论」。
- **携带附件仍不在这条通道**：`answer` 只投文字。用户要附图时在平台对话里直接补（或走正常
  派活附件路径），命令不提供第二形态。

**Considered Options：**

- **只用任务评论，让承接人下次读评论**：拒。评论不触发回合，承接人不会主动回读；用户看到
  的「已评论」与承接人的「未收到」之间没有任何信号。
- **`dispatch --update <任务号>` 把回答写进正文**：拒。正文是需求真源，把「回答第二轮问题」
  写进去会让需求随对话漂移，也丢掉「谁在什么时候答了什么」的轮次结构。
- **`task summon` 带一句回答另开会话**：拒。HF-15793 的双会话事故同源：同一任务两份执行。
- **把 `chat send` 当答案通道**：拒。它只认会话绑定的执行身份，人工令牌 403；为它造一个
  「用户身份的 agent 令牌」等于新造一个身份面，收益为零。
- **由派活员临场拼 curl（现状）**：拒。读凭证 + 绕过 superctl + 依赖临时拉起派活员，三重代价
  换一句消息。

**Consequences：**

- 回答有了机器入口，位置不再由提示词运气决定；主控的受理路径仍是「回一句收到 → 一条
  `superctl answer` → 记台账」，没有多一次派活。
- `internal/hifox` 多一个 `PostUserMessage`（只打人类消息接口），`task view` 的解析多
  `chatSessionId` / `organizationId` / `sessionStatus.top` 三处，回答的会话由
  `Task.AnswerSessionID()` 一处选定；哨兵的注入文本多一段条件提示，首见等待态也注入。
- 只有文字：带图回答需要在平台侧补，这条限制写在手册里，不静默降级。
- 回答是一条新的用户消息，会像用户手动发送一样唤醒承接人并产生 run；`queued=true` 时必须
  如实告诉用户「排在他这轮后面」，不要承诺立即生效。
- 尚未覆盖平台侧「用户直接在网页回答」：那条路本来就不经主控，无需改。
