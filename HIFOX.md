# HiFox Declarative Configuration Guide

本项目使用 `hifox-deploy` 管理 HiFox 小队（Crew）、智能体（Agent）与技能（Skill）的声明式配置。

## 1. 目录结构

- **ProjectRoot（项目根目录）**：当前仓库根目录。
- **DeploymentRoot（部署根目录）**：`.`（未显式传 `--root` 时，CLI 自动优先探测 `.hifox/`，不存在则使用当前目录 `.`）。
  - `config/apifox/`：与 Organization / Space 解耦的配置集合（`ConfigRoot`）。
    - `crews/<key>/crew.yaml` + 指令文件
    - `agents/<key>/agent.yaml` + 指令文件
    - `skills/<key>/skill.yaml` + `SKILL.md`（及可选附属文件）：Space Skill，创建在 Space 中，可挂载给多个 Agent
    - `agents/<key>/skills/<skill-key>/skill.yaml` + `SKILL.md`：Agent Skill，归属由所在 agent 目录表达（无 `assignTo`），部署时以 `hifox skill create --agent` 创建为该 Agent 专属技能
  - `environments/staging/environment.yaml`：部署绑定（`organizationId`、`spaceId`、`configRoot`、`nameSuffix`、`target`）。
  - `environments/staging/state.yaml`：已部署远端资源的权威 UUID 映射（需提交 Git，勿手改 UUID）。
  - `snapshots/`、`backups/`：本地只读快照与时间点归档产物（已由 `.gitignore` 忽略）。

## 2. 引用宿主项目的 Skills 与 Prompts（`@/` 语法）

配置元数据（`crew.yaml`、`agent.yaml`、`skill.yaml`）始终位于 `config/<config-set>/` 下，但正文与技能目录可以直接引用项目根目录（`ProjectRoot`）下的文件：

- **默认相对路径**：不带 `@/` 前缀时，相对于当前资源目录（如 `agents/<key>/instructions.md`）。
- **`@/` 项目根路径**：以 `@/` 开头时，相对于 `ProjectRoot` 解析（禁止越出 `ProjectRoot`）：
  - **引用宿主 Prompt 文件**（在 `agent.yaml` 或 `crew.yaml` 中）：
    ```yaml
    key: reviewer
    name: Reviewer
    instructionsFile: "@/prompts/reviewer.md"
    ```
  - **直接引用整个宿主 Skill 目录**（在 `skill.yaml` 中）：
    ```yaml
    key: code-review
    name: Code Review
    description: Review changes with repository rules
    dir: "@/skills/code-review"
    assignTo:
      - reviewer
    ```
    当配置 `dir` 时：
    - `contentFile` 默认取该目录下的 `SKILL.md`（通过 `hifox skill create/edit --content` 同步）；
    - 该目录下除 `SKILL.md`、`skill.yaml`、`.gitkeep` 外的其余附属文件（如 `references/`、`templates/`、`scripts/`），会在部署时通过 `hifox skill files upsert/delete` 自动同步到远端 Skill。

## 3. 编译期模板变量

Crew 指令、Agent 指令、Skill 的 `SKILL.md` 以及 Skill 目录下的附属文本文件，在 `validate`、`plan`、`create`、`apply` 阶段均会经过编译期变量展开：

| 变量 | 含义 |
| --- | --- |
| `{{agents.<key>.name}}` | 当前环境下渲染后的 Agent 名称（含环境后缀） |
| `{{crews.<key>.name}}` | 当前环境下渲染后的 Crew 名称 |
| `{{skills.<key>.name}}` | 当前环境下渲染后的 Skill 名称（Agent Skill 的 key 为 `<agent-key>/<skill-key>`） |
| `{{crew.name}}` | 单 Crew 配置集合下的当前 Crew 名称（多 Crew 时请用 `{{crews.<key>.name}}`） |
| `{{environment}}` | 当前环境名（如 `staging`、`current`） |

- **校验规则**：任何未解析的 `{{...}}` 变量都会在 `validate` 阶段报错，防止拼写错误静默上线。
- **字面量转义**：若 Skill 或 Prompt 内容中包含非 HiFox 变量的字面量双花括号（如代码模板示例 `{{value}}`），请使用反斜杠转义写为 `\{{value}}`，编译后将输出为字面量 `{{value}}`。

## 4. 常用命令与工作流

```bash
# 1. 离线校验配置、路径边界与模板变量编译
./hifox-deploy validate

# 2. 预览首次置备或增量变更计划（Dry-run，不修改远端）
./hifox-deploy create --environment staging --suffix " - staging"
./hifox-deploy plan --environment staging

# 3. 执行远端变更（必须显式传 --apply）
./hifox-deploy create --environment staging --suffix " - staging" --apply
./hifox-deploy apply --environment staging --apply

# 4. 拉取快照、创建备份与恢复
./hifox-deploy pull --environment staging
./hifox-deploy backup --environment staging
./hifox-deploy restore --from backups/<timestamp>/staging --apply              # 原地恢复（还原配置与外部引用文件到原位置）
./hifox-deploy restore --from backups/<timestamp>/staging --to /tmp/out --apply # 异地恢复（外部引用自动物化进目标 config/）
```
