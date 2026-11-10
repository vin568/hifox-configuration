---
name: XWing Branch Hall Dispatch
description: "吴用跨 Space 调度分舵的允许面与操作契约：舵登记表、同层级实例挑选、二龙山触发 / 进度查询 / 任务委派命令模板与禁区。"
---

# Branch Hall Dispatch（分舵调度）

吴用跨 Space 调度分舵的操作契约与允许面。分舵是部署在非主 Space 的专家调度中枢（与忠义堂同构：手动触发、按主题分支、零任务卡）；本技能是吴用在分舵上的**唯一操作面**，清单外的任何命令都不执行。

## 1. 舵登记表

| 舵 | key | Space（名 / identifier） | 工作流实例 | 主题 → 承接专家 |
|---|---|---|---|---|
| 二龙山 | `erlongshan` | Apifox 产研 / `AF` | 二龙山（无后缀）、二龙山 - staging | `apifox` → 燕青（`yanqing`）；`docs-site`（文档站 explorer-v2）→ 萧让（`xiaorang`） |

加舵 = 建舵 workflow + 本表加行 + 吴用 instructions 路由表加行；舵内加主题 = 舵图加分支 + 本表主题行 + 吴用路由表与任务委派映射同步（同一不变量，漏一处即「派了没人接」或反向，见 ADR 0002 / 0009）。

## 2. 允许面（六条，其余一律不做）

1. **找舵**：`hifox workflow list --space "Apifox 产研" --output json`，按**同层级**挑实例：吴用无后缀 → 「二龙山」；吴用 staging → 「二龙山 - staging」。
2. **触发**：`hifox workflow run <id> --space "Apifox 产研" --async --input '<json>'`，input 结构：
   `{"topic":"apifox|docs-site","message":"<用户原话>","context":"<会话前情、已排除的方向、用户关注点>"}`
   `--space` 必填（有 id 也要）；把返回的 execution id 与本会话请求关联。
3. **查进度**：`hifox workflow runs view <id> <execution-id> --space "Apifox 产研"`。每轮回复前主动检查挂起执行；完成则转述专家最终消息，失败说明失败点。
4. **解析委派对象**：`hifox agent list --output json`（组织级，**无** `--space`），按同层级挑对应专家——`apifox` 用「燕青」/「燕青 - staging」，`docs-site` 用「萧让」/「萧让 - staging」，取其 id，勿硬编码。
5. **建任务**（仅用户显式要求，或排查结论确认后）：`hifox task create --space "Apifox 产研" --title "[<主题>] <标题>" --delegate-agent <对应专家 id> --body "<需求原话 + 排查结论 + 验收要点>"`（`<主题>` 取 `apifox` 或 `docs-site`）。`--space` 必填，缺了任务落错板。
6. **查任务**（杂活）：`hifox task view <id>`（id 全局唯一，如 `AF-123`）；`hifox task list --space "Apifox 产研" --assignee agent:<对应专家 id>`。

## 3. 纪律

- 分舵只做排查 / 分析 / 答复：`run_agent` 不带 task id，聊天链路零任务卡；完整交付只走第 5 条。
- 禁区：Apifox 产研 Space 的其它任何资源（workflow / agent / crew / skill / automation / docs）、`state.yaml`、发布与部署类操作、一切写操作命令。
- 如实汇报：执行中就说执行中，未查询不得声称结果。
- 不打印任何 token / 密钥 / 凭据。
