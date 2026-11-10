# 跨 Space 专家调度收敛为分舵（二龙山）：吴用走命令面，操作面由专属技能收窄

Apifox 产研 Space 的交付专家燕青（`yanqing`，原 ymir）不在 HiFox 产研 Space，忠义堂的 `agent:` 块不能跨 Space 引用 → 在 `config/apifox` 建同构的单一手动 workflow「二龙山」（`erlongshan`：`start_trigger` + condition + `run_agent` 不挂任务 + else reject），作为该 Space 的专家调度中枢（**分舵**，Branch Hall）。吴用（HiFox 产研）用 `hifox` CLI 跨 Space 触发（`workflow run --space "Apifox 产研"`），按 execution id 查询并转述；完整交付经用户确认后以 `task create --space "Apifox 产研" --delegate-agent <燕青>` 建卡，任务落 Apifox 产研任务板。为防止误操作，吴用的跨 Space 操作面收敛在专属 Agent Skill `branch-hall-dispatch`（舵登记表 + 允许命令清单 + 禁区），随吴用实例部署，不进 Space 共享（单一消费者，源内联在 `config/hifox/agents/wuyong/skills/`，不建根级共享源）。

## Considered Options

- **把燕青迁入 HiFox 产研 Space**：拒。燕青绑 Apifox 的 Computer / 仓库 / 环境（`environments/{current,staging}` 各自 target），迁移破坏 `state.yaml` 归属与可见性；Apifox 产研任务板才是其交付工作流的落点。
- **吴用直连燕青（DM / summon）**：拒。平台无 agent 间 DM 原语（ADR 0002 已核实），会话协作是任务作用域，会产生任务噪音。
- **不外挂入口、用户直接找燕青**：拒。用户入口唯一性被破坏，主题判定与转述职责落回用户。
- **webhook 外接桥**：拒。工作流触发器闭集无飞书事件，且每事件一次执行、无多轮上下文（同 ADR 0002）。
- **每舵一个约束技能**：拒。规则重复 N 份、修订要 N 处同步；单一 `branch-hall-dispatch` skill 用舵登记表承载扩展。
- **约束技能做成 Space 共享 Skill**（`config/hifox/skills/`）：拒。会暴露给 HiFox 产研全部 agent，违背最小暴露；单消费者技能内联在吴用技能目录（`contentFile`），不进根级共享源。

## Consequences

- **主题闭集同步面扩大**：apifox 主题需在多处同步——堂 / 舵分支、吴用路由表、任务委派映射；漏一处即「派了没人接」或反向。GLOSSARY 的术语定义随之更新。
- **同层级匹配**：两个 Space 的 staging 后缀文本不同（HiFox `" (staging)"`、Apifox `" - staging"`），挑选中枢实例与委派对象一律按层级（无后缀↔无后缀、staging↔staging），不按字面后缀。
- **零任务增量保持**：分舵分支末块输出即结果，聊天链路不产生任务卡；任务卡只出现在完整交付（Apifox 产研任务板）。
- **skill 是纪律不是权限系统**：平台无 per-Space 工具白名单，允许面靠 instructions + skill 约束；硬边界仍在吴用 instructions 纪律节。
- **双实例**：二龙山随 apifox 环境双份（`二龙山` / `二龙山 - staging`），联调走 staging 后由吴用同层级触发。
- **落盘状态**：staging 双环境已 apply（二龙山实例、燕青改名、吴用技能）；`environments/current`（prod）只重绑 `yanqing`、**未 apply**，生产实例仍显示「尤弥尔 Ymir」——prod apply 与 `hifox-prod` 缺 wuyong / zhongyitang 条目的收口另开任务。
- **图迭代受 HF-15237 约束**：改图走「归档 + 重新 import」。
- **命名**：分舵是类别词，用英文术语 Branch Hall；实例名（二龙山）与人物名（燕青）沿用专名拼音 key（ADR 0003）。**入伙统筹调度面的 agent 取水浒名**，故 apifox 集合内统筹调度面的专家（燕青、萧让，见 ADR 0009；汤隆，见 ADR 0011）非北欧名，其余 agent 改名不动。
- **验证**：CLI help 已确认 `workflow run --space` / `task create --space` 支持；staging 首跑与 else 分支被平台执行器阻断（`sim-runner.cjs bundle` 缺失，计算机 `workflowExecutionEnabled=false`，未改动的忠义堂同错），待环境修复后补验；`task create --space` 尚无真实建卡验证。
