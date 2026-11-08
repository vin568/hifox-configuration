# AGENTS.md

## 仓库定位与分工

本仓库是 xwing 家族 CLI 应用（部署实例）的声明式配置仓库（配置集合与环境绑定清单见根 `README.md`），同时是部署 CLI 的开发配置仓库：`vendor/xwing-deploy` 是 git 子模块，部署行为改动只在子模块内开发，本仓库跟踪指针与配置。

## 文档职责判定

新增或修改本仓文档（本文件 / `README.md`）前，先问这段内容的事实归属：

1. **只有本仓知道的**（环境清单、仓库身份、git 物流、子模块开发流程）→ 写在这里。
2. **工具行为与配置结构语义**（命令、选项、字段、闭集、guard、目录结构）→ 不写，指向 CLI 自描述面（`--help` / `doctor` / `guide` / `guide contract`）；不列举、不复制契约细节。
3. **面向全部消费仓的策略或指针声明** → 改受管章节模板（子模块 `src/commands/init.mjs`），经 `init` 分发，不手写副本。

原则与取舍见子模块 `docs/adr/0015`；受管章节内的策略句是唯一权威表述，本节只是判定方法。

## 子模块开发（唯一允许改部署逻辑的地方）

1. `vendor/xwing-deploy` 是完整 git 仓库，不是 vendored 只读代码；`.gitmodules` 固定 `branch = dev`（`main` 为稳定分支）。detached HEAD 时先 `git -C vendor/xwing-deploy switch dev`。
2. 在子模块内编辑并运行 `npm test`；真实 API 测试按 CLI App 参数化（hifox / sharkly 双套），只在各自专用测试组织上跑，见子模块 `AGENTS.md`「测试」节的 `.env.e2e` / `<APP>_ORGANIZATION_ID` 与 `<APP>_E2E_*` 约定。
3. 在子模块仓库提交并推送 `origin/dev`，再回本仓库 `git add vendor/xwing-deploy && git commit` 更新指针。根仓库的提交只包含配置/文档/壳脚本/指针变化。
4. 拉上游更新：`git submodule update --remote --rebase vendor/xwing-deploy`，随后 `./xwing-deploy init` 刷新受管物料；`./xwing-deploy update` 对子模块布局一律拒绝（见 `./xwing-deploy guide upgrade`）。

不要把子模块当只读依赖，也不要绕开它把部署逻辑复制进根仓库或壳脚本；`xwing-deploy` 壳脚本的路径与 `--root` 语义属消费方契约，不要改。修改术语或契约前，先读子模块 `GLOSSARY.md` 与 `docs/adr/` 并同步。子模块文档拆分约束见其 `AGENTS.md`「文档职责」节：工具仓 `README.md` 是随包分发的纯消费契约（`guide contract` 原样输出）；维护者内容归子模块 `AGENTS.md`；随包文档（README 与 runbooks）不反向引用不随包的文件（`docs/adr/`、`AGENTS.md`、`GLOSSARY.md` 的编号与链接在安装形态下是死链）；部署实例名只出现在校验闭集处（依赖节、`cli.app` 字段表）；改契约措辞需同步其不变量与 runbook。

<!-- xwing-deploy:start -->

## Xwing Declarative Configuration

本项目的 Crew / Agent / Skill / Workflow 声明式配置位于 `.`（`config/<config-set>/` 配置集合 + `environments/<env>/` 部署绑定）。部署工具 xwing-deploy 自描述其操作面，使用方法与配置结构语义以 CLI 为准、不在本仓库其他文档复述：

- 不变量：`state.yaml` 的 UUID 是已部署资源的唯一权威，勿手改；变更命令默认预览，必须显式 `--apply` 才执行（各命令的变更分级见 `./xwing-deploy --help`）。
- 预检：`./xwing-deploy doctor`（node / yq / CLI 可执行 / state 盖章 / configRoot，全离线，先跑它）。
- 命令目录：`./xwing-deploy --help`；逐命令 `./xwing-deploy <command> --help`。
- 过程手册：`./xwing-deploy guide <topic>`（restore / troubleshooting / upgrade / workflows，随 CLI 版本发布）。
- 完整契约：`./xwing-deploy guide contract`（CLI 按自身位置解析随包 README，任意包管理器/安装布局可用）；copy/子模块安装亦可直接读 `vendor/xwing-deploy/README.md`。

<!-- xwing-deploy:end -->
