# Xwing Agent Crew Configuration

“诸神的黄昏 Ragnarok”小队与 hifox 配置集合的声明式本地配置，面向 xwing 家族 CLI 应用（部署实例 `hifox` / `sharkly`）。本仓库只保存配置与环境绑定；部署逻辑位于 git 子模块 `vendor/x-deploy`——本仓库同时是该 CLI 的开发配置仓库（见 [`AGENTS.md`](AGENTS.md)）。

## 操作面

部署操作面由 CLI 自描述：`./x-deploy --help`（命令与变更分级）、`./x-deploy guide contract`（完整契约与结构语义）；agent 入口见 `AGENTS.md` 受管章节。

## 配置集合与环境清单

同 Organization（Apifox）下两个 Space：**Apifox 产研** 与 **HiFox 产研**；同一配置集合可被多个环境绑定，各环境独立 `state.yaml`。

| 环境 | configRoot | 目标 Space | 说明 |
| --- | --- | --- | --- |
| `staging`（默认） | `config/apifox` | Apifox 产研 | 后缀 ` - staging`，Ragnarok 日常迭代环境，已部署 |
| `current` | `config/apifox` | Apifox 产研 | 无后缀，线上正在使用的实例；`ignoreAgentModels: true` |
| `example` | `config/apifox` | Apifox 产研 | 后缀 ` - example`，未部署，新环境复制模板（`cp -R environments/example environments/<name>`） |
| `hifox-prod` | `config/hifox` | HiFox 产研 | 无后缀，已部署 |
| `hifox-staging` | `config/hifox` | HiFox 产研 | 后缀 ` (staging)`，已部署 |

## 根级共享技能源

`skills/` 存放跨 agent 复用、但不以 Space Skill 共享实例的技能源目录；各 agent 在自身 `skills/` 目录下用 Agent Skill 引用同一源，部署为各 agent 专属实例（结构语义见 `./x-deploy guide contract`）。当前条目：`task-runtime`（task-local 运行时操作契约 + 环境预检 `preflight.sh`，由 linchong 与 daizong 引用）。三处技能目录的用途划分见 [`AGENTS.md`](AGENTS.md)。

## x-deploy 发布流水线

`vendor/x-deploy` dev → main 的定时发布门禁，全部资源在 `config/hifox` 集合内（与 linchong 同仓）：workflow `x-deploy-release-gate` 是**编写型**（源形态与迭代回路见工具技能）——**无 GUI 创建**，`workflow import` 一次成型。它每 3 小时用 patch 等价判定 Releasable Delta（真分叉自动 backmerge），指派 `agents/daizong` 在 task-local dev 服务上跑该 agent 自带的黑盒验收套件（消费方契约钉子），通过即合并被测 commit 到 main 并推送；结果与截图汇入常驻报告任务。因 `config/hifox` 被 hifox-prod / hifox-staging 双环境部署，workflow 会存在两份实例，check-update 块以运行标记守卫让并发轮退化为 skip。决策依据见 [`docs/adr/0001`](docs/adr/0001-x-deploy-release-gate.md)，术语见 [`GLOSSARY.md`](GLOSSARY.md)，workflow 的增删改知识见工具技能（`.agents/skills/xwing-workflows/`，init 受管安装）与 `./x-deploy guide workflows`。


## 统筹调度

用户统一入口是 `agents/wuyong`（吴用）：飞书私聊或群 @ 进入平台原生持久多轮会话，判定主题后调度——专家主题经手动触发的调度中枢单图路由执行：xwing 产品 → `workflows/zhongyitang`（忠义堂，HiFox 产研）派林冲，x-deploy → 忠义堂派武松，apifox 产品 → Apifox 产研 Space 的分舵 `workflows/erlongshan`（二龙山）派燕青（吴用经 `hifox workflow run --space` 跨 Space 触发，操作面收窄在 `branch-hall-dispatch` skill）；`run_agent` 不挂任务、结果由吴用查询执行进度后转述；杂活自理；完整交付经用户显式要求或确认后建任务委派。key 命名与身份重建决策见 [`docs/adr/0002`](docs/adr/0002-steward-feishu-dispatch.md) / [`0003`](docs/adr/0003-agent-key-pinyin-identity-rebuild.md)，跨 Space 分舵决策见 [`0004`](docs/adr/0004-cross-space-branch-hall.md)，术语见 [`GLOSSARY.md`](GLOSSARY.md)。

## 子模块（x-deploy）

首次克隆与补齐：

```bash
git clone --recurse-submodules git@apifox.github.com:vin568/hifox-configuration.git
git submodule update --init --recursive
```

`vendor/x-deploy` 是独立仓库，需要其读取权限；SSH/HTTPS 的本地 Git 配置按团队环境处理。本仓库跟踪 `dev` 开发分支（`main` 为稳定）。升级 `dev` 最新并刷新受管物料：

```bash
git submodule update --remote --rebase vendor/x-deploy
./x-deploy init
git add vendor/x-deploy
git commit -m "chore(deps): bump x-deploy"
```

固定已验证版本用 `git -C vendor/x-deploy checkout <commit>` 代替第一步。开发、测试与提交边界见根 [`AGENTS.md`](AGENTS.md) 与子模块 `AGENTS.md`；各安装形态的升级语义见 `./x-deploy guide upgrade`。

## 依赖

- `./x-deploy doctor` 一次校验全部工具依赖（node ≥ 18、`yq`、目标 CLI 可执行与 state 盖章）。
- 使用 mise 时 `mise install` 按 `mise.toml` 安装固定 Node 版本。
