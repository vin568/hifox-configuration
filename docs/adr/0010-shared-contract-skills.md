# 跨 agent 复用契约抽为根级共享源技能（staging-deploy / web-acceptance），行为门禁保留内联

yanqing / xiaorang / skadi / eir 的指令正文里存在同一份**机械契约**的多份手写拷贝：apifox 集成测试服的部署参数（`gh workflow run` 模板、用户名到端口的映射、未改动包的分支规则、失败口径）在 yanqing / xiaorang / skadi 各一份；测试服 Web 验收取证规范（固定测试账号、`agent-browser` 操作、截图与录屏自查、附件提交、结论与回流）在 yanqing / xiaorang / eir 各一份（eir 那份还是逐条重写版，且测试账号域名带错字）。段落级比对：yanqing ↔ xiaorang 有 25% 的正文近同，其中部署命令块与端口表 0.91 / 0.99，skadi ↔ yanqing 达 39%。

→ 抽出两个**根级共享源**技能（`skills/apifox-staging-deploy/`、`skills/apifox-web-acceptance/`），各 agent 在自己的 `skills/<key>/skill.yaml` 里用 `dir: "@/skills/<源目录>"` 建**私有实例**（沿用 `xwing-task-runtime` 机制），指令正文只留「按该 skill 执行」+ 各 agent 的增量（包面、入口路径、覆盖重点）。

**抽取边界：机械契约抽出去，常驻行为契约留在指令里。** 判据是「这份内容是否需要每次任务都遵守」：

- 抽：命令 / 参数 / 端口 / 账号 / 取证步骤这类**机械且易漂移**的契约——放错一位端口、抄错一个账号域名都会造成真实损失，单一来源是对的。
- 不抽：P0–P9 门禁骨架、阶段退出条件表、汇报模板、角色切换纪律、沟通规则与红线。它们是**常驻行为契约**，写进按需加载的 skill 会降低遵守率；且各 agent 的门禁矩阵本就不同（xiaorang 没有 DB migration / 生产发布分支），抽象成「通用协议」后差异只能以覆盖条款堆回来，可读性反而更差。自审环节已由 Space Skill `architecture-fit-review` 共享，无需再抽。

## Considered Options

- **Space Skill 共享实例**（`config/<set>/skills/<key>` + `assignTo`，同 `architecture-fit-review`）：拒。单实例省事，但向该 Space 暴露、且可被后续指派给不在设计内的 agent；ADR 0004 的最小暴露倾向偏向私有实例。改用根级共享源后，单一来源的收益（一致性）保留，暴露面不扩大。
- **把门禁骨架也抽成 `delivery-gates`**：拒。见上；常驻约束不该依赖按需加载。
- **维持内联重复（每个 agent 各持一份）**：拒。协议演进要 N 处同步，已经出现 eir 版账号域名错字这种漂移；且 skadi 的整份指令与 yanqing 的部署节重复 39%。
- **让各 agent 直接读别人的指令文件**：拒。指令正文是 agent 的身份与行为契约，交叉引用会把 A 的职责描述混进 B 的自我认知。

## Consequences

- **单一来源**：部署参数与取证规范各只有一份，改一处三 / 两个 agent 同步；`dir:` 指向根级 `skills/`，部署时仍是各 agent 专属实例（单归属），不向 Space 暴露。
- **命名**：根级源目录带产品前缀（`apifox-staging-deploy` / `apifox-web-acceptance`，既有的 `task-runtime` 同步改为 `xwing-task-runtime`）——根级 `skills/` 是跨配置集合的共享命名空间，裸名会假装通用。agent 实例 `key` 保持裸名（`staging-deploy` / `web-acceptance` / `task-runtime`）：`<agent>/<key>` 已按 agent 命名空间化，改 key 会动 state 身份（已部署的 `task-runtime` 会触发 look-renamed 门禁），收益不抵迁移成本。
- **指令与 skill 的引用要显式**：指令里保留一行强制引用（`按 {{skills.<agent>/<key>.name}} skill 执行`），避免技能按需加载时被忽略。
- **职责边界写在指令里**：各 agent 的包面、验收入口路径、覆盖重点（如文档站 `/apidoc`、主应用 `/main`、服务端改动要验 API）仍留在各自指令，skill 不预设产品细节。
- **新增 skill = 新实例**：apply 时新建，不影响既有 UUID；抽取不改变任何 agent 的职责，只改变契约的宿主位置。
- **落盘状态**：本决策落在配置侧，staging / current / hifox 各环境的远端生效由后续 apply 收口。
- **账号真值与已知残留**：测试账号以 `skills/apifox-web-acceptance/SKILL.md` 为准，域名为 `apifox.com`（2026-11-10 用户确认；历史文本里的 `apfiox.com` 是错字）。`config/apifox/agents/odin/instructions.md` 仍保留一行验收职责描述与旧账号串，本轮明确不收敛（用户决定）——读到时以本技能为准。
