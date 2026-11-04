# ADR 0002: ConfigurationSet 是校验边界，不是聚合根

- 状态：Accepted
- 日期：2026-11-04

## 背景

一个 ConfigRoot 下同时存在 crew、agent、skill 三类配置，并且存在跨文件引用：

- `CrewConfig.leader` / `members[].agent` 引用 AgentConfig 的 key。
- `SkillConfig.assignTo` 引用 AgentConfig 的 key。
- 渲染后的 crew 名称与 agent 名称必须全局唯一。
- 目录名必须等于配置的 key。

如果把整个 ConfigRoot 做成一个事务性聚合，会带来两个问题：

1. 文件是独立编辑的，没有事务边界；聚合的“原子一致性”无法真正保证。
2. 一个巨大的聚合会限制独立演进，尤其是 crew 与 agent 可能由不同人维护。

## 决策

- `ConfigurationSet` 是**校验边界（validation boundary）**，不是 DDD 事务性聚合根。它与 Organization/Space 解耦；Organization/Space 由 Environment 绑定。
- `CrewConfig`、`AgentConfig`、`SkillConfig` 各自是独立聚合，按 key 相互引用。
- `validate` 在部署前统一检查跨聚合不变量：
  - key 与目录名一致；
  - crew leader/members 与 skill assignTo 引用存在的 agent；
  - 渲染后的 crew/agent 名称唯一；
  - target computer/runtime 成对；
  - configRoot 位于 `config/` 内。

`ConfigurationSet` 可以没有 crew，只包含 agents / skills；此时 `{{crew.name}}` 不可解析，应使用具体 agent/skill 配置或 `{{crews.<key>.name}}`（如果有 crew）。

## 后果

- 三类配置可以独立编辑、独立评审；跨文件一致性由部署前校验保证。
- 不存在“保存即一致”的事务保证；错误配置会在 `validate` 阶段失败。
- 重命名 key 时，需要同步更新所有引用（crew members、skill assignTo、state key）。
- 部署计划以整个 ConfigurationSet 为输入，读取时不做部分聚合加载。

## 备选方案

- **Crew 作为聚合根，包含成员 Agent 与 Skill**：强一致性更好，但共享 Agent/Skill 会变复杂，且不符合文件独立编辑方式。被否决。
- **不建模聚合，只把文件当配置**：简单，但无法表达跨文件不变量和引用完整性。被否决。
