# ADR 0040：任务建单的负责人、承接 agent、发起人与来源

平台任务原先只有「指派给能力 agent」一条路：`superctl dispatch` 新建时强制 `--agent`，平台把 CLI 登录身份（Vin）自动设为 assignee，派活员再把选中的能力 agent 设为 delegate。用户点名「这件事交给某某人」时无路可走；参考的 Hifox 任务创建 agent 里「先不指派 / 指定负责人 / 订阅发起人」三项因此漂移。2026-11-22 把三个身份拆开。

**决策：**

- **负责人（assignee）与承接人（delegate agent）分开**。默认负责人是实例主人（配置 `owner_member`，hifox 成员邮箱或 `member:<id>`），承接人仍由朱贵按能力索引挑并传 `--delegate-agent`。任务创建后两者同时存在。
- **点名其他负责人 = 只建单，不派 agent**。`dispatch --assignee <name|email>` 时禁止 `--agent`/`--instance`/`--delegate`；由该负责人自己指派 agent。组织与 Space 取 `platforms/<平台>/defaults.yaml`（当前 hifox：Rayfox Labs / `HF`）。
- **不提供无负责人建单**。没有执行者的 board-only 任务不属于派活入口；缺 `owner_member` 又没点名时当场拒绝，不悄悄落到 CLI 登录身份。
- **发起人 = 建单消息所回复的那条消息的发送者**；没有回复时是当前发送者。创建者已被平台按 `reason=creator` 自动订阅；发起人与创建者不同时才显式 `task subscriber add`，再用 `subscriber list` 校验。重名、查无此人、读目录失败、**创建者身份读不到**都不猜，写进 `build_notes`。**私聊建单不订阅**：创建者就是唯一发送者，自动订阅已覆盖。
- **名字是两边的桥**：飞书事件给 open_id，IM 成员列表给显示名；hifox `organization people list` 给同名成员的 email/userId。当前实例约定两边人名一致；反查不做。
- **来源入账**：serve 在注入成功（闸门放行）之后按 `(chat, thread)` 记最后一条触发（消息 id、发送者、被回复者）到 `origins.json`；`dispatch --origin-chat/--origin-thread` 读它补发起人，并写进 `inflight.Entry` 与台账骨架。成员问「本群任务进度」时，运行时只列出 origin 匹配的在途任务。
- **派活员仍是唯一建单入口**：主控不直接跑 `hifox task create`；类型闭集不变（不按标题猜），图片嵌入数少于图片附件数时写 `build_notes`。

**Considered Options：**

- **依赖平台把创建者自动设为 assignee**：拒。行为没有契约文档，变了就是静默错派；显式 `--assignee` 可测。
- **负责人和 delegate 同时由用户点名**：拒。与「负责人自己指派 agent」冲突，两个执行者会互相踩。
- **支持无负责人建单**：拒（本批）。无执行者、无人订阅、哨兵也不知道盯谁，语义不属于派活。
- **每次建单都显式订阅发起人**：拒。创建者自动订阅已覆盖单人触发；重复 add 只是多一次 API 与校验。
- **让派活员自己解析 open_id → 成员**：拒。派活员没有飞书凭证，也不该拿；解析在运行时完成，派活员只读结果。

**Consequences：**

- `config.Config` 新增 `owner_member`；`platforms/<平台>/defaults.yaml` 新增；`dispatch` 新增 `--assignee`/`--origin-chat`/`--origin-thread`。
- `inflight.Entry` 新增 `origin_chat`/`origin_thread`；`origins.json` 是新的实例状态文件。
- 台账骨架多一行「来源」；成员消息注入带本群在途任务清单。
- 主控硬禁区补「不直接建单/改单」，只读 `task view` 仍允许。
- 新增 `superctl member find --feishu <open_id> --chat <chat_id>` 供人工/主控做身份互查。

## 2026-11-27 修订：绑定话题与 url

- `inflight.Entry` 新增 `url`：`dispatch` 写平台返回的链接，`track` 新增可选 `--url`（不联网取真值）。
- 作业话题绑定（ADR 0046）按话题 id 匹配作业：成员在绑定话题里问进度时列出该话题绑定的作业；主区消息仍按 `origin_chat` / `origin_thread` 匹配。origin 仍是建单来源，不因绑定话题而改写。
- `<group_tasks>` 渲染短 id + 链接 + 备注；成员进度回答可带这两者（ADR 0038 修订）。
