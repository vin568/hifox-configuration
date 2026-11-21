# 维护者安道全：worktree 里开发，只 push 自己的特性分支

「改本仓文件的活没有承接人」从 ADR 0027 修订起一直是结构缺口：主控只能上报用户，用户只能自己动手。ADR 0034 把运行时提炼成框架后，这个缺口更容易补——维护 agent 需要的一切机制（`role: job`、`maxConcurrent`、worktree 契约、测试面）都已存在，不需要新角色、也不需要框架改动。

**决策：**

- 新增本地 job 能力 agent **安道全（andaoquan）**，`maxConcurrent: 2`，可改根仓与三个子模块（`x-deploy` / `v-skills` / `super-agent`）的 tracked 文件；权限档位是 ADR 0019 的第 2、3 级执行侧。
- **每作业一个 worktree**：用 `.agents/skills/worktree-dev/scripts/worktree.sh add <作业名>` 建，根仓分支 `feature/<作业名>`（基于 `main`）、子模块同名分支（基于 `dev`）。运行时不管 git 生命周期，contract 写在它的手册里。
- **终点是 push 同名特性分支**，不是合并：`feature/<作业名>` 推到 origin（子模块与根仓各一条），合并顺序固定为「先子模块 → 它的集成分支（`x-deploy`/`v-skills` 是 `dev`，`super-agent` 是 `main`，以 `.gitmodules` 的 `branch` 为准），再把根仓 gitlink 指到该 commit，最后根仓 → `main`」。共享分支是用户的，不给自动化 agent push。
- **worktree 不自动清理**：作业完成后保留，由用户审阅后 `worktree.sh remove`。
- 它填的正是 `direct-tiers` / `local-agents` 里那个空缺；三份文档与 GLOSSARY 的直连三级、石秀边界同步改写。

**Considered Options：**

- **只改工作树、不提交**：拒。没有 commit 就没有 diff 的稳定边界，用户审阅也拿不到可复现的分支。
- **允许 agent merge / push `dev`**：拒。`origin/dev` 是全仓共享的受保护分支，代价不对等（省下的只是一次 `git push`）。
- **给 `capability.yaml` 加 `worktree: true` 字段由运行时建 worktree**：拒。运行时管 pane 与状态目录已经够多；git 物流属方法论，归技能正文与手册。

**Consequences：**

- `direct-tiers` 的第 2、3 级不再「执行侧空缺」，主控的处置路径变成「问用户 → 派安道全 → 用户合并」。
- 并发 2 意味着同时最多两个 worktree；`npm install` 与 `go` 构建的磁盘开销是它的实际约束。
- 验收（ADR 0034 第 5 条）：加一份 agent 声明与一个白名单脚本、让它真实改一处本仓文件、跑测试、提交并回报 diff——全程零框架代码改动。
