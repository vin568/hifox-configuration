# AGENTS.md

## 仓库定位与分工

本仓库是 xwing 家族 CLI 应用（部署实例）的声明式配置仓库（配置集合与环境绑定清单见根 `README.md`），同时是部署 CLI 的开发配置仓库：`vendor/x-deploy` 是 git 子模块，部署行为改动只在子模块内开发，本仓库跟踪指针与配置。

## 文档职责判定

新增或修改本仓文档（本文件 / `README.md`）前，先问这段内容的事实归属：

1. **只有本仓知道的**（环境清单、仓库身份、git 物流、子模块开发流程）→ 写在这里。
2. **工具行为与配置结构语义**（命令、选项、字段、闭集、guard、目录结构）→ 不写，指向 CLI 自描述面（`--help` / `doctor` / `guide` / `guide contract`）；不列举、不复制契约细节。
3. **面向全部消费仓的策略或指针声明** → 改受管章节模板（子模块 `src/commands/init.mjs`），经 `init` 分发，不手写副本。

原则与取舍见子模块 `docs/adr/0015`；受管章节内的策略句是唯一权威表述，本节只是判定方法。

## 技能目录

本仓有三处技能目录，消费者互斥；新增技能先按消费者判定放置：

| 目录 | 消费者 | 机制与形态 |
| --- | --- | --- |
| `skills/` | 部署到远端的 xwing agent | 技能源：由 `config/<集合>/agents/<key>/skills/<skill-key>/skill.yaml` 以 `dir: "@/skills/<名>"` 引用同一源，部署为各 agent 专属实例。`SKILL.md` 是内容体、无 frontmatter（身份在 `skill.yaml`）。当前：`task-runtime`（linchong、daizong 引用） |
| `config/<集合>/skills/` | 部署到远端的 xwing agent（Space 内共享） | Space Skill 实例，多 agent 挂载同一份；结构语义见 `./x-deploy guide contract`，不在本仓复述 |
| `.agents/skills/` | 在本仓工作的开发 agent 与人（不部署） | 项目级工具技能（Agent Skills 开放标准位置）：`SKILL.md` 带 `name`/`description` frontmatter。CLI 自带技能（当前 `xwing-workflows`）由 `./x-deploy init` 整目录受管重置，定制须复制改名；清单外的自有技能 init 不触碰，当前自有：`worktree-dev`（本仓 git worktree 开发契约 + `worktree.sh` 助手） |

判定一句话：给部署的远端 agent 用 → `skills/`（源 + agent 引用）或 `config/<集合>/skills/`（Space 共享）；给在本仓干活的开发 agent 用 → `.agents/skills/<名>/`。

## 子模块开发（唯一允许改部署逻辑的地方）

1. `vendor/x-deploy` 是完整 git 仓库，不是 vendored 只读代码；`.gitmodules` 固定 `branch = dev`（`main` 为稳定分支）。detached HEAD 时先 `git -C vendor/x-deploy switch dev`。
2. 在子模块内编辑并运行 `npm test`；真实 API 测试按 CLI App 参数化（hifox / sharkly 双套），只在各自专用测试组织上跑，见子模块 `AGENTS.md`「测试」节的 `.env.e2e` / `<APP>_ORGANIZATION_ID` 与 `<APP>_E2E_*` 约定。
3. 在子模块仓库提交并推送 `origin/dev`，再回本仓库 `git add vendor/x-deploy && git commit` 更新指针。根仓库的提交只包含配置/文档/壳脚本/指针变化。
4. 拉上游更新：`git submodule update --remote --rebase vendor/x-deploy`，随后 `./x-deploy init` 刷新受管物料；`./x-deploy update` 对子模块布局一律拒绝（见 `./x-deploy guide upgrade`）。

不要把子模块当只读依赖，也不要绕开它把部署逻辑复制进根仓库或壳脚本；`x-deploy` 壳脚本的路径与 `--root` 语义属消费方契约，不要改。修改术语或契约前，先读子模块 `GLOSSARY.md` 与 `docs/adr/` 并同步。子模块文档拆分约束见其 `AGENTS.md`「文档职责」节：工具仓 `README.md` 是随包分发的纯消费契约（`guide contract` 原样输出）；维护者内容归子模块 `AGENTS.md`；随包文档（README 与 runbooks）不反向引用不随包的文件（`docs/adr/`、`AGENTS.md`、`GLOSSARY.md` 的编号与链接在安装形态下是死链）；部署实例名只出现在校验闭集处（依赖节、`cli.app` 字段表）；改契约措辞需同步其不变量与 runbook。

<!-- x-deploy:start -->

## Xwing Declarative Configuration

本项目的 Crew / Agent / Skill / Workflow / Automation 声明式配置位于 `.`（`config/<config-set>/` 配置集合 + `environments/<env>/` 部署绑定）。部署工具 x-deploy 自描述其操作面，使用方法与配置结构语义以 CLI 为准、不在本仓库其他文档复述：

- 不变量：`state.yaml` 的 UUID 是已部署资源的唯一权威，勿手改；key 改名时把对应条目搬到新 key（UUID 不变），validate 的 look-renamed 门禁会拦下未重绑的改名；变更命令默认预览，必须显式 `--apply` 才执行（各命令的变更分级见 `./x-deploy --help`）。
- 预检：`./x-deploy doctor`（node / yq / CLI 可执行 / state 盖章 / configRoot，全离线，先跑它）。
- 命令目录：`./x-deploy --help`；逐命令 `./x-deploy <command> --help`。
- 过程手册：`./x-deploy guide <topic>`（automations / restore / troubleshooting / upgrade / workflows，随 CLI 版本发布）。
- 完整契约：`./x-deploy guide contract`（CLI 按自身位置解析随包 README，任意包管理器/安装布局可用）；copy/子模块安装亦可直接读 `vendor/x-deploy/README.md`。

<!-- x-deploy:end -->
