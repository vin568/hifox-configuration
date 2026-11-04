# ADR 0001: Environment 是部署绑定，不是 Space 的子对象

- 状态：Accepted（Environment 绑定模型 + ActiveOrganization fail-fast 校验已实现）
- 日期：2026-11-04

## 背景

仓库同时存在三个容易混淆的概念：

- `Space`：HiFox 的外部容器，代码中为 `spaceId`。
- `ConfigRoot`：本地 `config/<config-set>/` 下的期望配置；与 Organization/Space 解耦。
- `Environment`：`environments/<env>/environment.yaml` + `state.yaml`。

现状中，`current` 与 `staging` 共享同一个 `spaceId`，但名称后缀和 state 不同。`configRoot` 是一个与 Space 无关的配置集合，`environment.yaml` 才声明任意 `organizationId` / `spaceId` 并把它绑定到目标 Space。因此 Environment 不能被建模为 Space 的子对象，ConfigRoot 也不能被建模为“某个 Space 的配置”。

## 决策

**Environment 是一次部署绑定（deployment binding），不是 Space 的从属对象。**

一个 Environment 绑定：

```text
(organizationId, spaceId, configRoot, nameSuffix, namingPattern,
 visibility, target, modelPolicy) -> DeploymentState
```

- `Space` 是外部容器，不拥有 Environment。
- `ConfigRoot` 是与 Space 解耦的 ConfigurationSet，可以被多个 Environment 复用（例如部署到多个 Space）。
- `Environment` 拥有自己的 `DeploymentState`；state 不跨 Environment 共享。
- 同一个 Space 可以有多个 Environment（例如 `current` / `staging`），通过不同 suffix 区分实例。
- 同一个 ConfigurationSet 可以部署到多个 Space；反之，一个 Space 也可以承接多个 ConfigurationSet 的部署。

## ActiveOrganization 约束

`environment.yaml` 里的 `organizationId` 是**声明的目标组织**。远程命令执行前，CLI 会解析有效组织（effective organization）：

1. `--organization-id` 参数；CLI 会把它透传给每一次 `hifox` 调用；
2. `HIFOX_ORGANIZATION_ID` 环境变量；
3. profile 默认组织（`hifox config show` / `hifox organization switch`）。

解析结果会与 `environment.organizationId` 比较。`environment.organizationId` 缺失、有效组织不匹配、或 `spaceId` 不属于声明组织时，远程命令立即失败；`validate` 仍保持离线，不触发该 guard。e2e 测试仍先要求有效组织等于 `HIFOX_E2E_ORG_ID`，避免测试误指向其他组织。

**决策：Fail fast，不自动切换组织。**

在任何会访问远端或修改远端的命令（`create`、`clone`、`plan` 的远端读取、`apply`、`pull`、`backup`）执行前：

1. 解析有效组织（按上面的优先级）；
2. 与 `environment.organizationId` 比较；
3. 不一致时立即失败，错误信息说明当前组织、目标组织，以及修复方式；
4. 不自动调用 `hifox organization switch`，也不修改全局 CLI 配置；
5. 校验 `spaceId` 属于该组织（`hifox space view`），失败则停止。

修复方式由用户显式执行：

```bash
hifox organization switch <organization-id>
# 或
export HIFOX_ORGANIZATION_ID=<organization-id>
```

`hifox organization switch` 会先验证访问权限；`hifox config set organizationId` 是未校验的底层写法，不作为推荐修复方式。

离线命令 `validate` 不访问远端，不需要该 guard。

在 guard 之前，跨组织部署是**不安全**的：工具可能用 A 组织的凭据去操作 B 组织的 Space ID。现在 guard 在远程命令前阻止这种不匹配。

## 后果

- 支持“任意组织下的任意 Space”这一能力，但跨组织部署必须由用户显式切换有效组织；工具不会隐式修改全局 CLI 状态。
- Environment 成为部署一致性的边界：state、suffix、target、model policy 都以 Environment 为单位。
- 允许多个 Environment 共享 ConfigRoot，也允许多个 Environment 指向同一个 Space；是否冲突由部署前校验和名称唯一性负责。
- `organizationId` 参与部署前置校验，不再只是 manifest 元数据；缺失时远程命令直接失败。
- 失败信息必须可操作：告诉用户当前有效组织、目标组织，以及 `hifox organization switch` 或 `HIFOX_ORGANIZATION_ID` 的修复方式。

## 备选方案

- **Environment = Space 级配置**：一个 Environment 对应一个 Space。被否决，因为 `current` / `staging` 共享同一个 `spaceId`，且无法表达多实例。
- **Space 是聚合，Environment 是部署槽**：更形式化，但会把外部 Space 和本地 state 混在一个聚合里。被否决，因为 Space 是外部系统，不应该拥有本地部署状态。
- **自动切换组织**：执行前自动调用 `hifox organization switch`。被否决，因为会隐式修改全局 CLI 状态，影响同一进程外的其他命令，风险不可控。
- **提供 `--switch-organization` 显式切换**：可作为未来的可选增强，但默认仍是 fail fast；当前不引入额外选项。
- **CLI 选项使用 `--organization-id`**：只覆盖当前进程的远程调用，不修改 profile；这是当前实现。
