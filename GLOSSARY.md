# HiFox Declarative Configuration & Deployment（声明式配置与部署）

定义并调谐跨目标 Space 与 Environment 的 HiFox 小队（Crew）、智能体（Agent）与技能（Skill）声明式配置。代码中统一使用英文术语，文档与讨论中可并用对应中文术语。

## Language

### Platform

**Organization（组织）**:
HiFox 的租户与权限边界，拥有 Space、Crew、Agent 和 Skill。
_Avoid_: Workspace（工作区）, Tenant（租户）, Account（账号）, ActiveOrganization（有效组织）

**Space（空间）**:
Organization 下承载已部署 Crew、Agent 与空间级 Skill 的平台容器。
_Avoid_: Workspace（工作区）, Project（项目）, Environment（环境）, ConfigRoot（配置根目录）

**Crew（小队）**:
部署在 Space 中的远端协作团队，由一个 leader Agent 与零或多个成员 Agent 组成。
_Avoid_: CrewConfig（小队配置）, Squad, Group（群组）

**Agent（智能体）**:
部署在 Space 中的远端自主执行体，拥有指令、运行时设置、运行目标与绑定的 Skill。
_Avoid_: AgentConfig（智能体配置）, Bot（机器人）, Worker

**Skill（技能）**:
HiFox 中的远端能力单元，分为空间级共享或 Agent 专属两类。
_Avoid_: SkillConfig（技能配置）, Plugin（插件）, Tool（工具）

**Target（运行目标）**:
Agent 执行所绑定的 Computer 租约与 Runtime 标识组合。
_Avoid_: Environment（环境）, Host（主机）

### Configuration

**ConfigurationSet（配置集合）**:
与 Organization 和 Space 解耦的一组期望 CrewConfig、AgentConfig 与 SkillConfig，是跨资源引用与命名的校验边界而非事务性聚合根。
_Avoid_: Space（空间）, Environment（环境）

**ConfigRoot（配置根目录）**:
单个 ConfigurationSet 的根位置。
_Avoid_: Space（空间）, Workspace（工作区）

**CrewConfig（小队配置）**:
期望的 Crew 声明，包含标识键、基础名称、leader 引用、成员引用、指令与可见性。
_Avoid_: Crew（小队）

**AgentConfig（智能体配置）**:
期望的 Agent 声明，包含标识键、基础名称、职务、指令、运行时设置、运行目标、权限与并发度。
_Avoid_: Agent（智能体）

**SkillConfig（技能配置）**:
期望的 Skill 声明，包含标识键、基础名称、正文内容、目标 Agent 分配关系与绑定策略。
_Avoid_: Skill（技能）

**SkillBinding（技能绑定策略）**:
将 Skill 关联到 Agent 的方式：空间级直接分配（`normal`），或通过复制源 Agent 保留专属技能所有权（`inherited-copy`）。
_Avoid_: Skill（技能）

**NameTemplate（名称模板）**:
命名模式与环境后缀的组合，用于从基础配置名称派生环境实例名称。
_Avoid_: RenderedName（渲染名称）

**RenderedName（渲染名称）**:
将 Environment 的 NameTemplate 应用于 Crew、Agent 或 Skill 后的最终部署名称。
_Avoid_: NameTemplate（名称模板）, Key（标识键）

**Visibility（可见性）**:
已部署 Crew 或 Agent 的访问范围（个人级 `personal` 或空间级 `space`）。
_Avoid_: Permission（权限）, Role（角色）

**ModelPolicy（模型策略）**:
Environment 级的模型传递规则，决定部署时是否忽略 Agent 声明的模型而交由远端默认处理。
_Avoid_: Runtime（运行时）

### Deployment

**Environment（部署环境 / 部署绑定）**:
将单个 ConfigurationSet 绑定到目标 Organization 与 Space，并附加 NameTemplate、默认 Visibility、默认 Target、ModelPolicy 及独立 DeploymentState 的部署单元。
_Avoid_: Space（空间）, ConfigRoot（配置根目录）, Stage

**DeploymentState（部署状态）**:
单个 Environment 内配置标识键到已部署远端资源身份（UUID）的唯一权威映射。
_Avoid_: Snapshot（快照）, Cache（缓存）, Manifest（备份清单）

**DeploymentPlan（部署计划）**:
在不修改远端的前提下，为使 Environment 与其 ConfigurationSet 对齐而生成的预期远端变更序列。
_Avoid_: Diff（差异对比）

**Provision（首次置备）**:
为尚无已部署 Crew 身份的 Environment 首次创建整套远端资源的过程。
_Avoid_: Reconcile（增量调谐）, Apply

**Reconcile（增量调谐）**:
依据 DeploymentState 中的已有远端身份，增量创建缺失资源、更新已有资源并校准小队成员的过程。
_Avoid_: Provision（首次置备）, Sync（同步）

**ProductionGuard（生产环境门禁）**:
对主生产环境（`current`）执行写操作前要求显式二次确认的安全保护机制。
_Avoid_: Dry-run（空跑预览）

**ActiveOrganization（有效组织）**:
本地适配器当前生效的 Organization 上下文；任何远端操作前必须与 Environment 声明的目标 Organization 一致。
_Avoid_: Organization（目标组织）

### Artifacts

**Snapshot（快照）**:
从远端拉取生成的环境状态只读本地缓存，可随时重建且不参与部署身份解析。
_Avoid_: DeploymentState（部署状态）, Backup（备份）

**Backup（备份）**:
包含 ConfigurationSet、Environment 的 DeploymentState、Snapshot 与 Manifest 的不可变时间点归档。
_Avoid_: Snapshot（快照）

**Manifest（备份清单）**:
记录 Backup 来源 Environment、目标 Organization 与 Space、ConfigRoot 及所捕获资源身份的归档元数据。
_Avoid_: DeploymentState（部署状态）, RestoreRecord（恢复记录）

**RestoreRecord（恢复记录）**:
备份恢复到新目录后记录来源 Backup 与恢复时间的溯源元数据。
_Avoid_: Manifest（备份清单）
