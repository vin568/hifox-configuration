# hifox-configuration

事实源是仓库根 `AGENTS.md`（含 `<!-- x-deploy:start -->` 受管章节）与三个子模块各自的 `AGENTS.md`。本文件只补它没有的机器事实。

## 补充

- **工作区**：用派活注入的直连脚本目录里的 `worktree add --repo <检出> --name <作业名>` 建（兄弟目录 `<检出>-<作业名>`，根仓与三个子模块各建同名分支 `feature/<作业名>`，子模块从各自集成分支切）。`.agents/skills/worktree-dev/scripts/worktree.sh` 只给在本仓干活的开发 agent 与人用，不对作业 agent 开放。
- **Go 工具链**：一律 `mise exec -- go …`。系统自带的 go 1.18 编不过本仓（缺 `log/slog`）。
- **实例自检**：`npm run check` 需要 `yq`（`mise exec -- npm run check`）。检查器用 `--root` 检查当前检出，不受本机配置影响。
- **分支与合并**：安道全的终点是 push 同名特性分支；合并（子模块先、指针后、根仓最后）由用户做。

## 覆盖

无。发现本文件与仓库文档冲突时，以仓库文档为准并上报主控。
