# 文档站专家萧让接入二龙山：主题按「交付物」粒度扩展为 `docs-site`，跨仓归口 `apifox`

Apifox 文档站（仓库 `explorer-v2`）此前由全栈交付专家燕青（`yanqing`）兼办，与 server-apps / tars-app / tars-cli 共用 `apifox` 主题。文档站的工程面自成一套（Remix SSR 渲染管线、URL / 重定向 / SEO 语义、api-hub、公开 API hub、私有化形态），与后端 DDD、桌面端 Electron 的规则互不相通 → 新增专职研发 agent 萧让（`xiaorang`，圣手书生，沿用 ADR 0004 的「入伙统筹调度面取水浒名」），在**同一座分舵**（二龙山 `erlongshan`）加一条主题分支，不新开舵：`docs-site` → 萧让，`apifox` → 燕青。

主题粒度取「交付物 / 产品面」：既有 `xwing` / `x-deploy` / `apifox` 都已按交付物划分，文档站是与它们并列的一个交付物，不是 `apifox` 内部的二级分类。

**归口规则**：以「主要改动落在哪个仓」为准——只涉及 `explorer-v2` → `docs-site`；涉及 server-apps / tars-app / tars-cli，或文档站改动必须与后端契约一起改而无法切分 → `apifox`（燕青，全栈闭环）。

## Considered Options

- **在 `apifox` 主题内加二级字段**（如 `{"topic":"apifox","surface":"docs-site"}`）：拒。主题闭集的三处同步不变量（舵分支 / 吴用路由表 / 委派映射）以 topic 单键为口径；二级字段要么让三处都懂二级结构，要么把两个维度压成一个隐式枚举——同步面不降反升，`reject.sh` 的拒绝语义也变模糊。
- **每专家一舵**：拒。舵的维度是 Space（ADR 0004：一座 Space 一舵），两位专家同在 Apifox 产研 Space，没有跨 Space 的理由；实例数随专家数线性增长，舵登记表与路由表放大。
- **把燕青收窄为只做 server-apps / tars-app / tars-cli**：拒。燕青的价值是跨端闭环（契约变更同时改两端），收窄会切断该能力；范围重叠由归口规则消解，不靠删减指令。
- **新专家沿用北欧命名**：拒。ADR 0004 已定「入伙统筹调度面的 agent 取水浒名」。
- **吴用直连萧让（不走舵）**：拒。同 ADR 0004——平台无 agent 间 DM 原语，会话协作是任务作用域，会产生任务噪音。

## Consequences

- **主题闭集新增一项**：`docs-site` 需三处同步——二龙山 condition 分支、吴用路由表（`config/hifox/agents/wuyong/instructions.md`）、任务委派映射（同文件 §4）；`branch-hall-dispatch` skill 的舵登记表与 `GLOSSARY.md` 的「主题闭集」定义随之更新。
- **一舵两专家**：二龙山图现为 start → condition（apifox / docs-site / else）→ `run_agent` × 2 + reject；两分支的 work 都显式收窄为「单条消息的排查答复」，聊天链路零任务增量不变，完整交付仍只走 `task create --space "Apifox 产研"`。
- **图迭代受 HF-15237 约束**：二龙山是 `workflow import` 建立的实例，改图走「归档旧实例 + 重新 import」（同 ADR 0004）；配置未变时 plan 显示 in sync 不代表图可增量推送。
- **双实例**：二龙山与萧让都随 apifox 环境双份（无后缀 / ` - staging`），吴用按同层级挑选中枢与委派对象。
- **萧让的仓库与技能归属**：`agent.yaml` 的 `repositories` 只声明 `explorer-v2`；自审所需的 Space Skill `architecture-fit-review` 的 `assignTo` 增 `xiaorang`（指令要求该 skill）。
- **未 apply**：本决策落在配置侧，staging / current 的远端生效由后续 apply 收口（含二龙山图的归档 + 重新 import）。
