# 领域词汇表

本表定义 `hifox-configuration` 与 `hifox-dotfile` 的 ubiquitous language。术语以代码和配置中的实际用法为准；中文解释用于讨论，不替代英文术语。

## 外部平台

| 术语 | 定义 | 不要混淆 |
|---|---|---|
| **Organization** | HiFox 的组织边界。Space、Crew、Agent、Skill 都属于某个 Organization。 | `ActiveOrganization` |
| **Space** | HiFox 的官方容器术语，代码中为 `spaceId`，CLI 命令为 `hifox space`。一个 Space 属于一个 Organization。 | `ConfigRoot`、`Environment` |
| **Crew** | HiFox 的远端小队，包含 leader 与成员 Agent。 | `CrewConfig` |
| **Agent** | HiFox 的远端 Agent。 | `AgentConfig` |
| **Skill** | HiFox 的远端 Skill，可以是空间级或 Agent 专属。 | `SkillConfig` |
| **Computer / Runtime** | Agent 的运行目标。`Target` 由 Computer Lease ID 与 Runtime ID 组成。 | `Environment` |

## 配置

| 术语 | 定义 | 不要混淆 |
|---|---|---|
| **ConfigRoot** | 一个与 Organization/Space 解耦的配置集合目录，位于 `config/<config-set>/`。 | `Space` |
| **ConfigurationSet** | 一个 ConfigRoot 下的 crew / agent / skill 集合；crew 可为 0..*，agents/skills 可以独立存在。它是**校验边界**，不是事务性聚合。 | `Space` |
| **CrewConfig** | 期望的 crew：key、name、leader、members、instructions、visibility、sharedWorkdir。 | `Crew` |
| **AgentConfig** | 期望的 agent：key、name、jobTitle、instructions、runtime、target、permissions、concurrency。Agent 与 Skill 的绑定只由 `SkillConfig.assignTo` 表达，不在 AgentConfig 中重复声明。历史文件中的 `agent.skills` 字段会被忽略，以兼容旧备份。 | `Agent` |
| **SkillConfig** | 期望的 skill：key、name、content、config、assignTo、binding。 | `Skill` |
| **NameTemplate** | `namingPattern` 与 `nameSuffix` 的组合，用于渲染 crew/agent 名称。 | `RenderedName` |
| **RenderedName** | 应用 NameTemplate 后的实际部署名称。 | `NameTemplate` |
| **Visibility** | `personal` 或 `space`；可被单个资源覆盖。 | — |
| **Target** | `computer` 与 `runtime` 的值对象；两者应成对出现。 | Computer / Runtime |
| **ModelPolicy** | 环境级 `ignoreAgentModels`。为 true 时不传 `--model`，只影响 `runtime.model`。 | Runtime |
| **SkillBinding** | `normal`（空间级，`assignTo` 绑定）或 `inherited-copy`（复制源 Agent 以保留专属 skill）。 | `Skill` |

外部/空间级 skill 可以不在 ConfigurationSet 中建模；如果 agent instructions 引用它，目标 Space 必须已存在该 skill。`validate` 只校验 ConfigurationSet 内的 SkillConfig，不检查外部 skill 是否存在。

## 部署

| 术语 | 定义 | 不要混淆 |
|---|---|---|
| **Environment** | 一次部署绑定：把一个 ConfigurationSet（`configRoot`）绑定到 `organizationId` + `spaceId`，并附加 `nameSuffix` + `namingPattern` + `visibility` + `target` + `modelPolicy`；拥有一个 `DeploymentState`。它可以指向任意 Organization 下的任意 Space（取决于权限）。 | `Space`、`ConfigRoot` |
| **DeploymentState** | 每个 Environment 维护的 key → 远端 UUID 映射，是远端身份的唯一事实来源。 | `Snapshot` |
| **DeploymentPlan** | 一次部署将要执行的 `hifox` 命令集合，是 `plan` 命令的输出。 | CLI `plan` |
| **Provision** | 首次创建：`create` / `clone`。要求环境尚无已部署 crew，且 suffix 非空。 | `Reconcile` |
| **Reconcile** | `apply`：按 state 增量创建或编辑，补建缺失对象，并校准 crew 成员。 | `Provision` |
| **ProductionGuard** | 对名为 `current` 的环境执行 `--apply` 时必须显式传 `--production`。 | — |
| **ActiveOrganization** | `hifox` CLI 的**有效组织**。解析优先级：`--organization-id` > `HIFOX_ORGANIZATION_ID` > profile 默认组织（`hifox config show` / `hifox organization switch`）。它是适配器的环境依赖，不是 Environment 的字段。 | `Organization` |

## 产物

| 术语 | 定义 | 不要混淆 |
|---|---|---|
| **Snapshot** | 远端状态的只读缓存，由 `pull` 生成，可重建。 | `DeploymentState` |
| **Backup** | 不可变归档：`config/` + environment state + snapshot + manifest。 | `Snapshot` |
| **Manifest** | `<backup>/<env>/manifest.json`，备份元数据；字段、类型和版本规则见 [hifox-dotfile README](../../vendor/hifox-dotfile/README.md#备份-manifest)。 | `DeploymentState` |
| **RestoreRecord** | 恢复目标中的 `environments/<env>/restore.json`，记录来源备份、恢复时间和原 manifest；不是 backup manifest。 | `Manifest` |

## 命名规则

- Organization 和 Space 只出现在 `environment.yaml`；ConfigurationSet / ConfigRoot 与它们解耦。
- 英文术语在代码、类型、ADR 中使用；中文只用于解释。
- CLI 命令名不是领域术语：`plan` 对应 `DeploymentPlan`；`clone` 是 `Provision` 的 CLI 别名。
- 不用 “workspace” 作为独立领域术语；HiFox 的平台实体是 **Space**（`hifox space`）。如果某人说 workspace 指的是 Space，统一改写为 Space。
- Organization 名称里可能出现 “Workspace” 字样（例如某个组织叫 “vin's Workspace”），那只是组织名，不是平台实体。
