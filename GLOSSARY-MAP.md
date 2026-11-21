# Glossary Map

本仓有四个词汇上下文：本仓自己的部署配置与统筹调度、随子模块分发的 x-deploy 工具契约，以及两个运行时子模块的机制词汇（super-agent 框架、v-skills）。术语的权威宿主唯一，跨上下文不重复定义。

## Contexts

- [本仓上下文](./GLOSSARY.md)：`config/<集合>/` 的部署实例、发布流水线的判定与门禁、统筹入口的调度与工作记忆（调度台账、事项、台账状态等），以及**宋江运行时实例**的运营词（宋江、蒋敬、朱贵、石秀、安道全、作业、作业台账…）。
- [x-deploy 工具契约](./vendor/x-deploy/GLOSSARY.md)：声明式配置与部署工具自身的词汇——CLI App、Organization、Space、Environment、Crew / Agent / Skill / Workflow / Automation、编写型 / 捕获型等。
- [super-agent 框架](./vendor/super-agent/GLOSSARY.md)：本地编排运行时的机制词——框架、运行时实例、主控（角色）、能力注册表、能力技能、平台指针、直连脚本、触发器、在途清单、急停、换脑、上下文快照、受控 agent。
- v-skills：随子模块分发的工作流/技能词汇（见其 README 与 glossary，若存在）。

## Relationships

- **本仓 → 工具**：本仓声明的资源由工具调和；资源类型、字段闭集与调谐语义以工具 glossary 为准，本仓不复制、不复述。
- **工具 → 本仓**：工具 glossary 只定义机制与闭集，不含具体环境、实例、流水线与调度语义；这些运营词汇只在本仓定义。
- **本仓 ↔ super-agent 框架**：框架只定义机制（主控是角色、运行时实例是配置 + 能力注册表 + 状态目录）；「宋江」是这个实例的主控人名与人格，只在本仓定义。配置结构语义以框架 README 与 `guide` 为准，本仓不复述字段表。
- **同一概念只允许一个权威宿主**：平台词（Organization / Space / Environment / CLI App）归工具；运行时机制词归框架；实例运营词（主题闭集、同层级、完整交付、调度台账、事项、宋江/蒋敬/朱贵/石秀/安道全）归本仓。新增术语先判定归属，再写入对应 glossary。
- **决策记录随上下文**：本仓决策在 [`docs/adr/`](./docs/adr/)，工具决策在 `vendor/x-deploy/docs/adr/`，运行时机制决策在 `vendor/super-agent/docs/adr/`。
