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

## 安全约束

- 新建实例必须使用非空后缀。
- 创建前会检查远端小队与 Agent 是否重名。
- 已存在小队 UUID 的环境不能再次执行 create/clone；如果首次创建中断，会跳过 state 中已成功创建的 Agent 并继续。
- 更新始终依据 state 中的 UUID，不通过名称猜测目标。
- 中途创建失败时会保留已成功创建的 UUID，便于排查并避免重复创建。
- generated avatar、在线状态、时间戳等服务端字段不会写回模板。
- Skill ID 必须在目标组织中有效；跨组织克隆前需要确认 Skill 可见性。
- Agent 专属 Skill 使用 `inherited-copy`：新环境通过复制源 Agent 保留绑定，Skill 正文由 `config/skills/` 统一维护。
- 新建 Agent 需要成对配置 Computer Lease ID 与 Runtime ID，可通过 `hifox agent target list` 查询。

## 依赖

- `hifox`
- `node >= 18`
- `yq`
