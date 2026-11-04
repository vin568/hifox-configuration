# 领域模型

本目录记录 `hifox-configuration` / `hifox-dotfile` 的领域语言与边界。

核心边界：**ConfigurationSet / ConfigRoot 与 Organization/Space 解耦**；Organization 和 Space 只在 `environment.yaml` 中定义，由 Environment 把配置集合绑定到目标 Space。

- [领域词汇表](glossary.md)
- [上下文地图](context-map.md)

## ADR

- [ADR 0001: Environment 是部署绑定，不是 Space 的子对象](../adr/0001-environment-as-deployment-binding.md)
- [ADR 0002: ConfigurationSet 是校验边界，不是聚合根](../adr/0002-configuration-aggregates.md)
- [ADR 0003: DeploymentPlan 的 CLI 命令是 `plan`](../adr/0003-deployment-plan-command.md)
- [ADR 0004: Snapshot 与 Backup 是支撑产物，不是领域聚合](../adr/0004-snapshot-backup-as-artifacts.md)

## 当前实现缺口

无已知实现缺口。新增模型/实现不一致时，在本节记录，并注明是待实现还是有意降级。
