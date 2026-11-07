# AGENTS.md

## 仓库定位

本仓库是 xwing 家族部署实例（国内 `hifox`、国外 `sharkly`）的声明式配置仓库：crew / agent / skill 模板，以及每个环境的 `environment.yaml`、`state.yaml`。环境经 `cli: { app, path? }` 声明目标部署实例（ADR 0011）。部署逻辑不在本仓库，位于 git 子模块 `vendor/xwing-deploy`，由根目录 `xwing-deploy` 壳脚本固定以本仓库根目录作为 `--root` 调用。

## 关键原则：子模块是开发者工作副本

根 README 将 `vendor/xwing-deploy` 描述为部署 CLI 子模块，并说明普通配置使用者不需要修改。该说明只约束配置仓库的使用者：维护部署行为时：

1. 在 `vendor/xwing-deploy` 的 `dev` 分支上直接编辑、测试（它是完整的 git 仓库，不是 vendored 只读代码）。`.gitmodules` 设置 `branch = dev`，`main` 是稳定分支。
2. 在子模块仓库内提交并推送到 `origin/dev`。
3. 回到根仓库更新子模块指针：`git add vendor/xwing-deploy && git commit`。

拉取子模块上游更新用 `git submodule update --remote --rebase vendor/xwing-deploy`。`./xwing-deploy update` 在本仓库会直接报错：CLI 只管理复制安装（copy install），对 git 子模块（开发布局）一律拒绝并提示 git 命令；升级一律走上面的 git 流程。

不要把子模块当只读依赖，也不要绕开它把部署逻辑复制到根仓库或壳脚本。

## 目录与边界

- `config/<config-set>/`：与 Organization/Space 解耦的配置集合，是 crew / agent / skill 的唯一事实来源；`organizationId`、`spaceId` 只在 `environments/` 中定义。
- `environments/<env>/`：部署绑定（目标部署实例 `cli`、Organization、Space、`configRoot`、suffix、target、model policy）与部署后的 UUID；`state.yaml` 需要提交，不要手改其中的 UUID；`cliApp` 记录该 state 所属部署实例，与 `environment.yaml` 的 `cli.app` 不一致时部署直接失败。
- `snapshots/`、`backups/`：生成物，已被 Git 忽略，不提交。
- `vendor/xwing-deploy/`：CLI 源码、测试、领域词汇表（`GLOSSARY.md`）与关键架构决策（`docs/adr/`）；修改术语或契约前先读并同步。改动走子模块自己的提交，遵守其 `AGENTS.md`。
- `xwing-deploy`：消费方契约，路径和 `--root` 语义不要改。

## 执行约定

- 每个 `environment.yaml` 必须声明 `cli.app`（闭集 `hifox` | `sharkly`，由工具内 `KNOWN_APPS` 维护）；`cli.path` 可选，指向该实例的某个具体 CLI 构建（如测试构建）。
- 变更分级：`plan` 只生成 DeploymentPlan；`create` / `clone` 不带 `--apply` 只打印 provisioning 计划；`apply` 必须显式 `--apply`；`restore` / `prune` 默认预览，`--apply` 才写盘或删除。
- 远程命令前校验 effective organization 与 `environment.organizationId`，并校验 `spaceId` 归属；不自动切换组织。组织环境变量按 `cli.app` 派生（`hifox` → `HIFOX_ORGANIZATION_ID`，`sharkly` → `SHARKLY_ORGANIZATION_ID`）。`validate` 不触发该 guard。
- `restore` 传 `--to` 时不覆盖非空目录并将外部引用物化进 `config/`，省略 `--to` 时按原位置恢复；`prune` 不改写备份内容。
- `--environment current` 管理线上正在使用的无后缀实例；对它执行 `--apply` 前先 `plan`，且只在用户明确要求时执行。
- 改 CLI 后，在子模块运行 `npm test`；真实 API 测试按 CLI App 参数化（hifox / sharkly 双套），只在各自专用测试组织上跑，见子模块 README 的 `.env.e2e` / `<APP>_ORGANIZATION_ID` 与 `<APP>_E2E_*` 约定。
- 提交顺序：先子模块，再根仓库指针。根仓库的提交只包含配置/文档/壳脚本/指针变化。

## 已知约束

- CLI 没有创建 Organization / Space / 部署实例的能力；测试组织与 Space 必须在对应实例的应用里创建。
- 选项必须写在命令后；只有 `--root` 可以放在任意位置。文档不要声称支持其他前置选项。

<!-- xwing-deploy:start -->

## Xwing Declarative Configuration

本项目的 Crew / Agent / Skill 声明式配置位于 `.`（初始配置集合：`config/apifox`，默认环境：`staging`）。
目录结构、通过 `@/` 引用项目内 Skills 与 Prompts、编译期模板变量（`{{agents.<key>.name}}`、`{{crews.<key>.name}}`、`{{skills.<key>.name}}`、`{{crew.name}}`、`{{environment}}` 及 `\{{...}}` 转义）以及部署/备份恢复工作流详见 [`XWING.md`](XWING.md)。

<!-- xwing-deploy:end -->
