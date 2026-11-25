# 直连白名单（Direct Channel Allowlist）

直连通道的变更类动作只允许执行本目录内的脚本，参数原样透传（ADR 0019 三级制的第 2 级）。

## 规矩

- 一可执行文件一能力。文件名即能力名。
- 每个脚本开头注释四行：用途、参数、示例、副作用声明。
- 脚本自身负责参数校验与幂等；主控不做二次包装。
- 新增能力 = 本目录加脚本 + 用例 + 走 git 提交；不进 git 的脚本视为不存在。
- 禁止放置：删除类、安装类、读凭证类脚本（直连硬禁区，写了也不会被执行）。

当前清单：

| 脚本 | 能力 | 边界 |
|---|---|---|
| `worktree` | 项目作业工作区的增删查（`--repo` 指定检出；根仓 + 子模块同名特性分支） | 只建/删工作区；不 push、不合并、不切检出分支、不 commit |
| `server` | 常驻 serve 生命周期：`status` / `start` / `stop` / `restart` | 只动 serve 进程；actor=master；永不 build、不改配置/凭证、不碰主控会话 |

`server` 的其余动词（包括 `logs`、`build`）不在白名单内：日志排查是只读动作，走
`super-agent/bin/superctl server logs` 由人执行；构建只由用户或维护员跑
`super-agent/build`（ADR 0036）。
