# Xwing Agent Crew Configuration

“诸神的黄昏 Ragnarok”小队与 hifox 配置集合的声明式本地配置，面向 xwing 家族 CLI 应用（部署实例 `hifox` / `sharkly`）。本仓库只保存配置与环境绑定；部署逻辑位于 git 子模块 `vendor/xwing-deploy`——本仓库同时是该 CLI 的开发配置仓库（见 [`AGENTS.md`](AGENTS.md)）。

## 操作面

部署操作面由 CLI 自描述：`./xwing-deploy --help`（命令与变更分级）、`./xwing-deploy guide contract`（完整契约与结构语义）；agent 入口见 `AGENTS.md` 受管章节。

## 配置集合与环境清单

同 Organization（Apifox）下两个 Space：**Apifox 产研** 与 **HiFox 产研**；同一配置集合可被多个环境绑定，各环境独立 `state.yaml`。

| 环境 | configRoot | 目标 Space | 说明 |
| --- | --- | --- | --- |
| `staging`（默认） | `config/apifox` | Apifox 产研 | 后缀 ` - staging`，Ragnarok 日常迭代环境，已部署 |
| `current` | `config/apifox` | Apifox 产研 | 无后缀，线上正在使用的实例；`ignoreAgentModels: true` |
| `example` | `config/apifox` | Apifox 产研 | 后缀 ` - example`，未部署，新环境复制模板（`cp -R environments/example environments/<name>`） |
| `hifox-prod` | `config/hifox` | HiFox 产研 | 无后缀，已部署 |
| `hifox-staging` | `config/hifox` | HiFox 产研 | 后缀 ` (staging)`，已部署 |

`config/apifox` 为 Ragnarok 小队主集合（crews / agents / skills）；`config/hifox` 为 HiFox 集合：`luke` agent（含 `xwing-task-kit` Agent Skill）、`autopilot-grilling` 与 `architecture-fit-review` Space Skill、`webhook-echo` 工作流（HF-15237 纳管实例，迭代约束见 `./xwing-deploy guide workflows`）。环境绑定明细以各 `environments/<env>/environment.yaml` 为准。

## 子模块（xwing-deploy）

首次克隆与补齐：

```bash
git clone --recurse-submodules git@apifox.github.com:vin568/hifox-configuration.git
git submodule update --init --recursive
```

`vendor/xwing-deploy` 是独立仓库，需要其读取权限；SSH/HTTPS 的本地 Git 配置按团队环境处理。本仓库跟踪 `dev` 开发分支（`main` 为稳定）。升级 `dev` 最新并刷新受管物料：

```bash
git submodule update --remote --rebase vendor/xwing-deploy
./xwing-deploy init
git add vendor/xwing-deploy
git commit -m "chore(deps): bump xwing-deploy"
```

固定已验证版本用 `git -C vendor/xwing-deploy checkout <commit>` 代替第一步。开发、测试与提交边界见根 [`AGENTS.md`](AGENTS.md) 与子模块 `AGENTS.md`；各安装形态的升级语义见 `./xwing-deploy guide upgrade`。

## 依赖

- `./xwing-deploy doctor` 一次校验全部工具依赖（node ≥ 18、`yq`、目标 CLI 可执行与 state 盖章）。
- 使用 mise 时 `mise install` 按 `mise.toml` 安装固定 Node 版本。
