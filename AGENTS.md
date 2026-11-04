# AGENTS.md

## 仓库定位

本仓库是 HiFox 的声明式配置仓库：crew / agent / skill 模板，以及每个环境的 `environment.yaml`、`state.yaml`。部署逻辑不在本仓库，位于 git 子模块 `vendor/hifox-deploy`，由根目录 `hifox-deploy` 固定以本仓库根目录作为 `--root` 调用。

## 关键原则：子模块是开发者工作副本

根 README 将 `vendor/hifox-deploy` 描述为部署 CLI 子模块，并说明普通配置使用者不需要修改。该说明只约束配置仓库的使用者：维护部署行为时：

1. 直接在 `vendor/hifox-deploy` 内编辑、测试（它是完整的 git 仓库，不是 vendored 只读代码）。
2. 在子模块仓库内提交。
3. 回到根仓库更新子模块指针：`git add vendor/hifox-deploy && git commit`。

不要把子模块当只读依赖，也不要绕开它把部署逻辑复制到根仓库或壳脚本。

## 目录与边界

- `config/<config-set>/`：与 Organization/Space 解耦的配置集合，是 crew / agent / skill 的唯一事实来源；`organizationId`、`spaceId` 只在 `environments/` 中定义。
- `environments/<env>/`：部署绑定（Organization、Space、`configRoot`、suffix、target、model policy）与部署后的 UUID；`state.yaml` 需要提交，不要手改其中的 UUID。
- `snapshots/`、`backups/`：生成物，已被 Git 忽略，不提交。
- `GLOSSARY.md`、`docs/adr/`：领域词汇表与关键架构决策；修改术语或契约前先读并同步。
- `vendor/hifox-deploy/`：CLI 源码与测试；改动走子模块自己的提交，遵守其 `AGENTS.md`。
- `hifox-deploy`：消费方契约，路径和 `--root` 语义不要改。

## 执行约定

- 变更分级：`plan` 只生成 DeploymentPlan；`create` / `clone` 不带 `--apply` 只打印 provisioning 计划；`apply` 必须显式 `--apply`；`restore` / `prune` 默认预览，`--apply` 才写盘或删除。
- 远程命令前校验 effective organization 与 `environment.organizationId`，并校验 `spaceId` 归属；不自动切换组织。`validate` 不触发该 guard。
- `restore` 不覆盖非空 root；`prune` 不改写备份内容。
- 对 `--environment current` 执行 `--apply` 必须同时传 `--production`。
- 改 CLI 后，在子模块运行 `npm test`；真实 API 测试只在专用测试组织上跑，见子模块 README 的 `HIFOX_E2E_*`。
- 提交顺序：先子模块，再根仓库指针。根仓库的提交只包含配置/文档/壳脚本/指针变化。

## 已知约束

- `hifox` CLI 没有 `organization create` / `space create`；测试组织与 Space 必须在 HiFox 应用里创建。
- 选项必须写在命令后；只有 `--root` 可以放在任意位置。文档不要声称支持其他前置选项。
