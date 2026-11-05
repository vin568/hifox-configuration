# HiFox Agent Crew Configuration

“诸神的黄昏 Ragnarok”小队的声明式本地配置。本仓库只保存配置与运行环境：部署逻辑位于 git 子模块 `vendor/hifox-deploy`，由根目录的 `hifox-deploy` 壳脚本调用。

## 目录

- `config/<config-set>/`：与 Organization/Space 解耦的配置集合，是 crew、agents、skills 的唯一事实来源。
  - `config/apifox/`：当前 Apifox 配置集合（Ragnarok 小队）。
  - `config/hifox/`：空的配置集合模板，待填充。
- `config/<config-set>/agents/*/instructions.md`：Agent 指令，支持名称变量。
- `GLOSSARY.md`：领域词汇表（Ubiquitous Language）。
- `docs/adr/`：关键设计决策记录。
- `environments/<name>/environment.yaml`：Organization、Space、`configRoot` 和名称后缀；Organization/Space 只在这里定义。
- `environments/<name>/state.yaml`：部署后由工具记录的远端 UUID。
- `snapshots/<environment>/`：可再生成的远端状态缓存，已被 Git 忽略。
- `backups/<timestamp>/<environment>/`：带时间戳的完整本地备份，已被 Git 忽略。
- `vendor/hifox-deploy/`：部署 CLI 子模块。普通配置使用者不需要修改；维护部署逻辑时在该子模块内开发、测试并提交，再在根仓库更新子模块指针（见 `AGENTS.md`）。
- `hifox-deploy`：同步入口，固定把本仓库根目录作为 `--root` 传给 CLI。
- `mise.toml`：Node 版本。

CLI 的命令、选项以及 `environment.yaml` / `state.yaml` 字段说明见 [`vendor/hifox-deploy/README.md`](vendor/hifox-deploy/README.md)。领域词汇见 [`GLOSSARY.md`](GLOSSARY.md)，关键决策见 [`docs/adr/`](docs/adr/)。

## 文档边界

- `GLOSSARY.md`：领域词汇表，定义平台、配置、部署与产物的核心概念。
- `docs/adr/`：关键设计决策及其取舍。
- `vendor/hifox-deploy/README.md`：CLI 契约，包括命令、选项、`environment.yaml` / `state.yaml` 字段和 manifest 规则。
- 本 README：配置集合、环境绑定和日常操作说明；CLI 细节以上面的契约为准，不在本文重复维护。

## 子模块

首次克隆：

```bash
git clone --recurse-submodules git@apifox.github.com:vin568/hifox-configuration.git
```

已有克隆补齐子模块：

```bash
git submodule update --init --recursive
```

`vendor/hifox-deploy` 是独立仓库；首次克隆前需要该子模块仓库的读取权限。SSH/HTTPS 的本地 Git 配置按团队环境处理，不在本文档中固化。

升级 CLI 版本：可先执行 `./hifox-deploy update` 预览，再执行 `./hifox-deploy update --apply` 显式升级到 origin 默认分支 tip，并由升级后的 CLI 刷新物料；随后提交新子模块指针。`init` / `init.sh` 重跑不升级依赖。需要固定已验证版本时，在 `hifox-deploy` 仓库提交并推送后手动选取提交：

```bash
git -C vendor/hifox-deploy fetch
git -C vendor/hifox-deploy checkout <commit>
./hifox-deploy init
git add vendor/hifox-deploy
git commit -m "chore(deps): bump hifox-deploy"
```

开发 CLI 时不需要等上游发布：直接在 `vendor/hifox-deploy` 内修改、运行 `npm test`，在该子模块仓库提交并推送，然后按上面的方式更新本仓库指针。拆分、测试与提交边界见根 [`AGENTS.md`](AGENTS.md) 和子模块 `AGENTS.md`。

## 默认 Staging 实例

```bash
./hifox-deploy validate
./hifox-deploy plan
./hifox-deploy apply --apply
./hifox-deploy pull
```

`staging` 是默认环境，小队和全部 Agent 均为 `personal`，名称追加 ` - staging`。
`plan` 只预览 DeploymentPlan；`apply` 必须显式传 `--apply`；`create` / `clone` 默认只打印 provisioning 计划，传 `--apply` 才创建远端对象。

原有无后缀实例保留在 `current` 环境：

```bash
./hifox-deploy plan --environment current
```

对 `current` 执行 `--apply` 必须同时传入 `--production`。

## 快照与备份

`pull` 只刷新可再生成的远端缓存：

```bash
./hifox-deploy pull
```

`backup` 会先拉取最新远端状态，再保存该环境绑定的配置集合（`configRoot`，例如 `config/apifox`）、环境 state、远端快照和 manifest。`config/` 下的其他配置集合属于别的环境，不会被打包：

```bash
./hifox-deploy backup
./hifox-deploy backup --environment current
./hifox-deploy backup --output-dir /path/to/private-backups
```

默认备份位置为 `backups/<UTC timestamp>/<environment>/`。仓库内的 `snapshots/` 和 `backups/` 都不会提交 Git；重要备份应复制到私有对象存储或其他持久化位置。`pull` 会先清空 `snapshots/<env>/`，因此远端已删除的资源不会残留在快照里。

恢复备份（支持原地恢复到原位置，或通过 `--to` 恢复到新的空目录并物化外部引用）：

```bash
./hifox-deploy restore --from backups/2026-11-04T04-06-12Z/current --apply
./hifox-deploy restore --from backups/2026-11-04T04-06-12Z/current --to /tmp/restored
./hifox-deploy restore --from backups/2026-11-04T04-06-12Z/current --to /tmp/restored --apply
```

清理旧备份和孤儿快照：

```bash
./hifox-deploy prune backups --keep 10          # 预览
./hifox-deploy prune backups --keep 10 --apply  # 删除更旧的备份
./hifox-deploy prune snapshots                  # 预览孤儿快照
./hifox-deploy prune snapshots --apply
```

`restore` 只覆盖备份记录的 `configRoot` 与环境目录（不会动 `config/` 下的其他配置集合），保留备份中的 `configRoot` 和 state UUID，恢复后仍需 `validate` / `plan` / `apply` 才能与远端对齐。`prune` 不迁移备份内容，只按保留策略删除。

## 克隆新实例

创建其他环境时复制示例配置：

```bash
cp -R environments/example environments/my-environment
```

编辑新环境的 `environment.yaml`，至少设置唯一后缀、`configRoot` 和 visibility；如果要部署到不同 Organization/Space，还要设置 `organizationId` / `spaceId`：

```yaml
organizationId: <organization-id>
spaceId: <space-id>
configRoot: config/apifox
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
./hifox-deploy clone --environment my-environment
./hifox-deploy clone --environment my-environment --apply
```

创建成功后会生成对应环境的 `state.yaml`。此后使用：

```bash
./hifox-deploy plan --environment my-environment
./hifox-deploy apply --environment my-environment --apply
./hifox-deploy pull --environment my-environment
```

也可以临时覆盖环境后缀：

```bash
./hifox-deploy clone --environment my-environment --suffix " - Vin Test"
```

## 名称变量

instructions 与 Skill 文件（`SKILL.md` 及目录内附属文本文件）支持部署时编译渲染以下变量：

```text
{{agents.thor.name}}
{{agents.baldur.name}}
{{crews.ragnarok.name}}
{{skills.maintainer-review.name}}
{{crew.name}}
{{environment}}
```

`{{crew.name}}` 仅在只有一个 crew 时可用；多个 crew 时使用 `{{crews.<key>.name}}`，避免歧义。需要保留字面量双花括号时使用 `\{{...}}` 转义。

例如 `@{{agents.thor.name}}` 在 staging 环境会渲染为 `@托尔 Thor - staging`。

## 忽略 Agent 模型

默认情况下，`apply` 会把 `configRoot` 下 `agents/*/agent.yaml` 中的 `runtime.model` 同步到远端。若某个环境不希望配置覆盖线上模型，在该环境的 `environment.yaml` 中设置：

```yaml
ignoreAgentModels: true
```

设置后，CLI 不再向 `hifox agent create/edit` 传递 `--model`，已有 Agent 的模型保持不变，新建 Agent 使用 runtime 默认模型。该开关只影响 `runtime.model`；`runtime.customArguments` 仍按配置传递。

## 多配置集合与多 Space

`configRoot` 指向一个与 Organization/Space 解耦的 **ConfigurationSet**。Organization 和 Space 只出现在 `environment.yaml`；同一个 ConfigurationSet 可以被多个 Environment 绑定到不同 Organization/Space。

```text
config/
  apifox/
    crews/
      ragnarok/
        crew.yaml
        instructions.md
    agents/
    skills/
  hifox/
    crews/
    agents/
    skills/
environments/
  apifox-current/
    environment.yaml   # organizationId + spaceId + configRoot: config/apifox
    state.yaml
  apifox-staging/
    environment.yaml   # same configRoot, different suffix
    state.yaml
```

`configRoot` 相对于仓库根目录，且必须位于 `config/` 内：

```yaml
organizationId: <organization-id>
spaceId: <目标 Space ID>
configRoot: config/apifox
nameSuffix: " - apifox"
```

新建环境时 `nameSuffix` 必须非空；如果希望 Agent 名称不带后缀，把 `namingPattern` 设为 `{{name}}`。

两种常见用法：

- **同一个 ConfigurationSet 部署到多个 Space**：多个环境使用相同的 `configRoot`，但 `organizationId` / `spaceId` 不同；每个环境有自己的 `state.yaml`，Skill 会在各自 Space 创建。
- **不同配置集合**：不同 crew / agent / skill 组合使用独立 `configRoot`，互不影响；Space 只是绑定目标。

`configRoot` 内的 `crews/`、`agents/`、`skills/` 结构与 `config/apifox/` 完全一致。复制现有配置：

```bash
mkdir -p config/<config-set>
cp -R config/apifox/. config/<config-set>/
```

注意事项：

- `state.yaml` 必须按环境分开；不要把同一份 state 用于不同 Space。
- 不要在已有环境上直接修改 `spaceId`；`state.yaml` 中的 Agent/Skill ID 属于原 Space，应该新建环境并重新 clone 一套。
- 空间级 Skill 由 `ensureSkills` 在 `spaceId` 指定的 Space 创建，并在该环境 state 中记录 ID。`skill edit` 不能把 Skill 移到另一个 Space；更换 `spaceId` 后需要删除旧 Skill 并重新创建。
- `inherited-copy` 依赖 `agent copy`，`agent copy` 没有 `--space` 参数，只适合同一 Space 内的环境克隆。跨 Space 时请在目标 Space 准备源 Agent/Skill，或改用不带 `binding` 的普通 Skill 配置，让 `ensureSkills` 在目标 Space 创建并分配给 Agent。
- 每个环境的 `state.yaml` 仍然只记录该环境的 UUID；`plan`/`apply`/`pull` 都要带 `--environment`。

## 多 Crew

每个 `configRoot` 可以维护多个 crew，目录结构与 agents 类似：

```text
config/apifox/crews/
  ragnarok/
    crew.yaml
    instructions.md
  another-crew/
    crew.yaml
    instructions.md
```

- `crew.yaml` 的 `key` 必须与目录名一致。
- `leader` 和 `members[].agent` 引用同一 `configRoot` 下的 agent key。
- `instructionsFile` 相对于该 crew 目录，示例为 `instructions.md`。
- `state.yaml` 的 `crew:` 节点按 crew key 记录；`apply` 会创建/更新所有 crew，`pull` 输出到 `snapshots/<env>/crews/<key>/`。
- 只有一个 crew 时，`{{crew.name}}` 仍可用；多个 crew 时使用 `{{crews.<key>.name}}`。
- 新增 crew 后先 `validate`，再 `plan`/`apply`；`plan` 会显示 `crew create` 和后续 edit 占位。

## 安全约束

- 新建实例必须使用非空后缀。
- 远程命令要求 `environment.organizationId`；effective organization 优先级为 `--organization-id` > `HIFOX_ORGANIZATION_ID` > profile 默认组织。不一致、缺少组织或 Space 不属于该组织时立即失败，不自动切换组织；`validate` 保持离线。
- 创建前会检查远端 crew 与 Agent 是否重名。
- 已存在 crew UUID 的环境不能再次执行 create/clone；首次创建中断后用 `apply` 继续，state 中已成功的 Agent/Crew 会保留，缺失的会补建。
- 更新始终依据 state 中的 UUID，不通过名称猜测目标。
- 中途创建失败时会保留已成功创建的 UUID，便于排查并避免重复创建。
- generated avatar、在线状态、时间戳等服务端字段不会写回模板。
- Skill ID 必须在目标组织中有效；跨组织克隆前需要确认 Skill 可见性。不在 `config/<config-set>/skills/` 中的外部 skill 不会被创建或校验，必须由目标 Space 预先提供。
- Agent 专属 Skill 使用 `inherited-copy`：同一 Space 内通过复制源 Agent 保留绑定，Skill 正文由 `config/<config-set>/skills/` 统一维护；跨 Space 见“多空间维护”。
- 新建 Agent 需要成对配置 Computer Lease ID 与 Runtime ID，可通过 `hifox agent target list` 查询。

## 依赖

- `hifox`
- `node >= 18`（使用 mise 时执行 `mise install` 按 `mise.toml` 安装固定版本）
- `yq`
- `hifox-deploy` 子模块的读取权限
