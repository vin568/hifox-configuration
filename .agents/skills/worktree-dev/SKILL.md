---
name: worktree-dev
description: Develop the hifox-configuration repository (root repo plus two submodules) in git worktrees. Use when creating, inspecting, or removing worktrees for this repo, preparing one for submodule development (fresh remote submodule clones, switch dev, .env.e2e copy, npm install, doctor), or cleaning up afterwards (submodule-aware dirty check and metadata cleanup). Covers git 2.50 worktree-with-submodules mechanics that plain git commands get wrong.
---
# XWing Worktree Development

本仓（根仓 + 双子模块 `vendor/xwing-deploy` / `vendor/v-skills`）的 git worktree 开发契约与工具脚本。适用：并行特性分支、不切主检出分支的验证与修复。

## 快速通道：`scripts/worktree.sh`

```bash
bash <skill-dir>/scripts/worktree.sh add <name> [-b <branch>]   # 建 + 配齐子模块
bash <skill-dir>/scripts/worktree.sh list                       # 列出各 worktree 与子模块态
bash <skill-dir>/scripts/worktree.sh remove <name> [--force]    # 自检干净后移除（分支保留）
bash <skill-dir>/scripts/worktree.sh prune                      # 清理失联元数据
```

- 只在主检出运行（linked worktree 内运行会报错）。worktree 目录是兄弟目录 `../hifox-configuration-<name>`，分支默认 `feature/<name>`（基于 main）；分支已存在则直接挂载该分支。
- `add` 自动完成：`submodule update --init`（两子模块）→ 对 `.gitmodules` 声明 `branch = dev` 的子模块 `switch dev` → 从主检出复制机器本地的 `vendor/xwing-deploy/.env.e2e`（存在才复制）。
- `remove` 先自检根仓与各子模块 `status --porcelain`，脏则拒绝并列出，确认丢弃用 `--force`；随后 `git worktree remove --force`（子模块在位时无 `--force` 一律 fatal，git 2.50 实测）。两类**预期脏**：`switch dev` 使根仓 gitlink 显示 ` M vendor/xwing-deploy`；`npm install` 在子模块生成未跟踪 `package-lock.json`（仓内无 lockfile）——均为可丢弃工作产物，`--force` 移除即可。

## 子模块在 worktree 中的机制（git 2.50 实测）

- 每个 worktree 的子模块是**从远端 URL 的全新克隆**（需网络），git dir 位于主仓 `.git/worktrees/<dir>/modules/`；init 后 detached 在根仓 gitlink。
- 主检出里**未推送**的子模块提交对 worktree 不可见：建 worktree 前先推 `origin/dev`，或 `git -C vendor/xwing-deploy fetch <主检出>/vendor/xwing-deploy dev` 补齐。
- `switch dev` 后子模块位于 `origin/dev` tip，与根仓 gitlink 可能不同（根仓显示 ` M vendor/xwing-deploy` 属预期）；回根仓提交指针时以实际验证过的 commit 为准。
- `remove --force` 会连带清理 `.git/worktrees/<dir>/modules`（实测无残留）；分支不删，自行 `git branch -d feature/<name>`。

## add 之后的就绪清单

| 步骤 | 命令 | 说明 |
| --- | --- | --- |
| Node | `mise install` | `mise.toml` 钉 node 24；主检出已装则跳过 |
| 依赖 | `vendor/xwing-deploy` 内 `npm install` | 无 lockfile，不用 `npm ci`；`v-skills` 无依赖 |
| 预检 | `./xwing-deploy doctor` | 壳脚本按自身路径解析 root，在 worktree 内即 worktree root |

离线用例：子模块内 `npm test` 必须全绿。真实 API e2e：`.env.e2e`（`add` 已复制；无则 `cp .env.e2e.example .env.e2e` 并填专用测试组织变量，缺省即 skip）。

## 提交边界（与根 `AGENTS.md` 相同，worktree 内同样成立）

子模块内开发、提交、推 `origin/dev`，再回所在 worktree 分支 `git add vendor/xwing-deploy` 更新指针；根仓提交只含配置/文档/壳脚本/指针。`state.yaml` UUID 是已部署资源唯一权威，勿手改；变更命令默认预览，显式 `--apply` 才执行。
