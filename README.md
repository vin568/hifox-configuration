# HiFox Agent Crew Configuration

“诸神的黄昏 Ragnarok”小队的声明式本地配置，可用于维护现有实例，或使用名称后缀克隆一套全新的小队与 Agent。

## 目录

- `config/`：可复用模板，是人工维护的唯一事实来源。
- `config/agents/*/instructions.md`：Agent 指令，支持名称变量。
- `environments/<name>/environment.yaml`：组织、Space 和名称后缀。
- `environments/<name>/state.yaml`：部署后由工具记录的远端 UUID。
- `snapshots/<environment>/`：可再生成的远端状态缓存，已被 Git 忽略。
- `backups/<timestamp>/<environment>/`：带时间戳的完整本地备份，已被 Git 忽略。
- `hifox-config`：同步入口。

## 默认 Staging 实例

```bash
./hifox-config validate
./hifox-config diff
./hifox-config apply --apply
./hifox-config pull
```

`staging` 是默认环境，小队和全部 Agent 均为 `personal`，名称追加 ` - staging`。
`apply` 和 `create` 默认只预览命令，必须显式传入 `--apply` 才会修改远端。

原有无后缀实例保留在 `current` 环境：

```bash
./hifox-config diff --environment current
```

## 快照与备份

`pull` 只刷新可再生成的远端缓存：

```bash
./hifox-config pull
```

`backup` 会先拉取最新远端状态，再保存配置模板、环境 state、远端快照和 manifest：

```bash
./hifox-config backup
./hifox-config backup --environment current
./hifox-config backup --output-dir /path/to/private-backups
```

默认备份位置为 `backups/<UTC timestamp>/<environment>/`。仓库内的 `snapshots/` 和 `backups/` 都不会提交 Git；重要备份应复制到私有对象存储或其他持久化位置。

## 克隆新实例

创建其他环境时复制示例配置：

```bash
cp -R environments/example environments/my-environment
```

编辑新环境的 `environment.yaml`，至少设置唯一后缀和 visibility：

```yaml
organizationId: 86555f76-3f5b-4f7a-93c6-406074110a59
spaceId: fcb74b8e-d801-42f5-b3e9-787c13577112
nameSuffix: " - staging"
namingPattern: "{{name}}{{suffix}}"
visibility:
  crew: personal
  agents: personal
target:
  computer: <computer-lease-id>
  runtime: <runtime-id>
```

预览并创建：

```bash
./hifox-config clone --environment my-environment
./hifox-config clone --environment my-environment --apply
```

创建成功后会生成对应环境的 `state.yaml`。此后使用：

```bash
./hifox-config diff --environment my-environment
./hifox-config apply --environment my-environment --apply
./hifox-config pull --environment my-environment
```

也可以临时覆盖环境后缀：

```bash
./hifox-config clone --environment my-environment --suffix " - Vin Test"
```

## 名称变量

instructions 支持部署时渲染以下变量：

```text
{{agents.thor.name}}
{{agents.baldur.name}}
{{crew.name}}
{{environment}}
```

例如 `@{{agents.thor.name}}` 在 staging 环境会渲染为 `@托尔 Thor - staging`。

## 忽略 Agent 模型

默认情况下，`apply` 会把 `config/agents/*/agent.yaml` 中的 `runtime.model` 同步到远端。若某个环境不希望配置覆盖线上模型，在该环境的 `environment.yaml` 中设置：

```yaml
ignoreAgentModels: true
```

设置后，`hifox-config` 不再向 `hifox agent create/edit` 传递 `--model`，已有 Agent 的模型保持不变，新建 Agent 使用 runtime 默认模型。该开关只影响 `runtime.model`；`runtime.customArguments` 仍按配置传递。

## 多空间维护

默认所有环境共用 `config/`。需要维护多个 Space 时，为每个 Space 准备独立的 crew/agents/skills 目录，并在环境配置中用 `configRoot` 指向它：

```text
config/
  spaces/
    apifox/
      crew.yaml
      agents/
      skills/
    apidog/
      crew.yaml
      agents/
      skills/
environments/
  apifox-current/
    environment.yaml
    state.yaml
  apidog-current/
    environment.yaml
    state.yaml
```

`configRoot` 相对于仓库根目录，且必须位于 `config/` 内：

```yaml
organizationId: 86555f76-3f5b-4f7a-93c6-406074110a59
spaceId: <目标 Space ID>
configRoot: config/spaces/apifox
nameSuffix: " - apifox"
```

新建环境时 `nameSuffix` 必须非空；如果希望 Agent 名称不带后缀，把 `namingPattern` 设为 `{{name}}`。

两种常见用法：

- **同一套 Agent/Skill 镜像到多个 Space**：多个环境使用相同的 `configRoot`，但 `spaceId` 不同；每个环境有自己的 `state.yaml`，Skill 会在各自 Space 创建。
- **不同 Space 维护不同 Agent/Skill**：每个 Space 使用独立 `configRoot`，互不影响。

`configRoot` 内的 `crew.yaml`、`agents/`、`skills/` 结构与原 `config/` 完全一致。复制现有配置：

```bash
mkdir -p config/spaces/apifox
cp config/crew.yaml config/spaces/apifox/
cp -R config/agents config/skills config/spaces/apifox/
```

注意事项：

- `state.yaml` 必须按环境分开；不要把同一份 state 用于不同 Space。
- 不要在已有环境上直接修改 `spaceId`；`state.yaml` 中的 Agent/Skill ID 属于原 Space，应该新建环境并重新 clone 一套。
- 空间级 Skill 由 `ensureSkills` 在 `spaceId` 指定的 Space 创建，并在该环境 state 中记录 ID。`skill edit` 不能把 Skill 移到另一个 Space；更换 `spaceId` 后需要删除旧 Skill 并重新创建。
- `inherited-copy` 依赖 `agent copy`，`agent copy` 没有 `--space` 参数，只适合同一 Space 内的环境克隆。跨 Space 时请在目标 Space 准备源 Agent/Skill，或改用不带 `binding` 的普通 Skill 配置，让 `ensureSkills` 在目标 Space 创建并分配给 Agent。
- 每个环境的 `state.yaml` 仍然只记录该环境的 UUID；`apply`/`diff`/`pull` 都要带 `--environment`。

## 安全约束

- 新建实例必须使用非空后缀。
- 创建前会检查远端小队与 Agent 是否重名。
- 已存在小队 UUID 的环境不能再次执行 create/clone；如果首次创建中断，会跳过 state 中已成功创建的 Agent 并继续。
- 更新始终依据 state 中的 UUID，不通过名称猜测目标。
- 中途创建失败时会保留已成功创建的 UUID，便于排查并避免重复创建。
- generated avatar、在线状态、时间戳等服务端字段不会写回模板。
- Skill ID 必须在目标组织中有效；跨组织克隆前需要确认 Skill 可见性。
- Agent 专属 Skill 使用 `inherited-copy`：同一 Space 内通过复制源 Agent 保留绑定，Skill 正文由 `config/skills/` 统一维护；跨 Space 见“多空间维护”。
- 新建 Agent 需要成对配置 Computer Lease ID 与 Runtime ID，可通过 `hifox agent target list` 查询。

## 依赖

- `hifox`
- `node >= 18`（使用 mise 时执行 `mise install` 按 `mise.toml` 安装固定版本）
- `yq`
