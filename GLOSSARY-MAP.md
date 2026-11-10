# Glossary Map

本仓有两个词汇上下文：本仓自己的部署配置与统筹调度，以及随子模块分发的 x-deploy 工具契约。术语的权威宿主唯一，跨上下文不重复定义。

## Contexts

- [本仓上下文](./GLOSSARY.md)：`config/<集合>/` 的部署实例、发布流水线的判定与门禁、统筹入口的调度与工作记忆（调度台账、事项、台账状态等）。
- [x-deploy 工具契约](./vendor/x-deploy/GLOSSARY.md)：声明式配置与部署工具自身的词汇——CLI App、Organization、Space、Environment、Crew / Agent / Skill / Workflow / Automation、编写型 / 捕获型等。

## Relationships

- **本仓 → 工具**：本仓声明的资源由工具调和；资源类型、字段闭集与调谐语义以工具 glossary 为准，本仓不复制、不复述。
- **工具 → 本仓**：工具 glossary 只定义机制与闭集，不含具体环境、实例、流水线与调度语义；这些运营词汇只在本仓定义。
- **同一概念只允许一个权威宿主**：平台词（Organization / Space / Environment / CLI App）归工具；实例运营词（主题闭集、同层级、完整交付、调度台账、事项）归本仓。新增术语先判定归属，再写入对应 glossary。
- **决策记录随上下文**：本仓决策在 [`docs/adr/`](./docs/adr/)，工具决策在 `vendor/x-deploy/docs/adr/`。
