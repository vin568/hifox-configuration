---
name: worktree-dev
description: Develop the hifox-configuration repository (root repo plus two submodules) in git worktrees. Use when creating, inspecting, or removing worktrees for this repo, preparing one for submodule development (fresh remote submodule clones, per-worktree feature branches off the declared integration branch, .env.e2e copy, npm install, doctor), merging a finished submodule feature branch back into origin/dev, or cleaning up afterwards (submodule-aware dirty check and metadata cleanup). Covers git 2.50 worktree-with-submodules mechanics that plain git commands get wrong.
---
# XWing Worktree Development

本仓（根仓 + 双子模块 `vendor/xwing-deploy` / `vendor/v-skills`）的 git worktree 开发契约与工具脚本。适用：并行特性分支、不切主检出分支的验证与修复。

## 快速通道：`scripts/worktree.sh`

```bash
bash <skill-dir>/scripts/worktree.sh add <name> [-b <branch>]   # 建 + 配齐子模块
bash <skill-dir>/scripts/worktree.sh list                       # 列出各 worktree 与子模块态
bash <skill-dir>/scripts/worktree.sh remove <name> [--force]    # 自检干净后移除（根仓分支保留）
bash <skill-dir>/scripts/worktree.sh prune                      # 清理失联元数据
```

- 只在主检出运行（linked worktree 内运行会报错）。worktree 目录是兄弟目录 `../hifox-configuration-<name>`，分支默认 `feature/<name>`（基于 main）；分支已存在则直接挂载该分支。
- `add` 自动完成：`submodule update --init`（两子模块）→ 对 `.gitmodules` 声明集成分支的子模块 `switch -c <根仓分支名> origin/<集成分支>` 建同名特性分支 → 从主检出复制机器本地的 `vendor/xwing-deploy/.env.e2e`（存在才复制）。
- **并行特性不混提交**：每个 worktree 的子模块 WIP 只落在自己的特性分支上，`origin/dev` 上只发生「已测绿特性的合并」（并回流程见下节）。
- `remove` 先自检根仓与各子模块 `status --porcelain`，脏则拒绝并列出，确认丢弃用 `--force`；随后 `git worktree remove --force`（子模块在位时无 `--force` 一律 fatal，git 2.50 实测）。两类**预期脏**：子模块特性分支离开根仓 gitlink 使根仓显示 ` M vendor/xwing-deploy`；`npm install` 在子模块生成未跟踪 `package-lock.json`（仓内无 lockfile）——均为可丢弃工作产物，`--force` 移除即可。
## 子模块在 worktree 中的机制与并回流程（git 2.50 实测）

- 每个 worktree 的子模块是**从远端 URL 的全新克隆**（需网络），git dir 位于主仓 `.git/worktrees/<dir>/modules/`；init 后 detached 在根仓 gitlink，随即切到基于 `origin/dev` tip 的同名特性分支——与根仓 gitlink 不同、根仓显示 ` M vendor/xwing-deploy` 属预期。
- **并行隔离**：worktree 内子模块开发全程在特性分支，两个未完成的特性之间零共享提交。主检出未推送的 dev 提交对 worktree 不可见的旧约束由此降级：其影响只是「并回时才汇合」，不再要求建 worktree 前先推 `origin/dev`。
- **并回 dev（特性完成时，在 worktree 子模块内）**：`npm test` 全绿 → `git fetch origin dev && git merge origin/dev`（有分叉则解冲突后重测）→ `git push origin HEAD:dev`。merge 后特性分支是 origin/dev 的超集，push 必为 fast-forward；被拒说明期间 dev 又并入了别的（同样测绿的）特性——重复 fetch → merge → 重测 → push。这是「都已可上线才混提交」的唯一汇合点。
- 回所在 worktree 分支提交指针时，以**并回后的 commit** 为准（必在 origin/dev 上，无悬空指针风险）。
- `remove --force` 会连带清理 `.git/worktrees/<dir>/modules`（实测无残留）：根仓分支保留、自行 `git branch -d feature/<name>`；**未并回 dev 的子模块特性分支随 worktree 移除而消失**——remove 前先并回，或 `git push -u origin <分支名>` 备份。

## add 之后的就绪清单

| 步骤 | 命令 | 说明 |
| --- | --- | --- |
| Node | `mise install` | `mise.toml` 钉 node 24；主检出已装则跳过 |
| 依赖 | `vendor/xwing-deploy` 内 `npm install` | 无 lockfile，不用 `npm ci`；`v-skills` 无依赖 |
| 预检 | `./xwing-deploy doctor` | 壳脚本按自身路径解析 root，在 worktree 内即 worktree root |

离线用例：子模块内 `npm test` 必须全绿。真实 API e2e：`.env.e2e`（`add` 已复制；无则 `cp .env.e2e.example .env.e2e` 并填专用测试组织变量，缺省即 skip）。

## 提交边界（与根 `AGENTS.md` 相同，worktree 内同样成立）
子模块内在**特性分支**上开发，`npm test` 全绿后按上节流程并回 `origin/dev`，再回所在 worktree 分支 `git add vendor/xwing-deploy` 更新指针（指向并回后的 commit）；根仓提交只含配置/文档/壳脚本/指针。主检出的子模块仍在 dev 上单线开发（根 `AGENTS.md` 流程），多 worktree 并行才走特性分支。`state.yaml` UUID 是已部署资源唯一权威，勿手改；变更命令默认预览，显式 `--apply` 才执行。
