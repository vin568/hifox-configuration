# XWing Task Runtime

跨 agent 共享的 xwing task-local 运行时操作契约与环境预检脚本（单一来源，被多个 agent 以各自实例引用）。

## 环境预检：`scripts/preflight.sh`

任务开始前运行一次，把任务涉及的改动面全部传入：

```bash
bash <skill-dir>/scripts/preflight.sh base hifox server db runtime
```

| Surface | 必需工具 |
|---|---|
| `base`（总是检查） | `git`、`node`、`pnpm`、`make` |
| `hifox` | `hifox` |
| `server` | `go` |
| `db` | `psql`、`pg_isready`；PostgreSQL 可达性只报 WARN，因为 `scripts/ensure-postgres.sh` 可能启动共享容器 |
| `runtime` | Google Chrome（或 `TASK_ENV_CHROME_EXECUTABLE` / `CHROME_PATH`）加 `db` 全部 |
| `cli` | `cargo` |
| `ci` | `yq`、`lsof`、`python3`，且 node 主版本 ≥ 18 |

`agent-browser`（自定义浏览器交互）与 `ffmpeg`（录屏抽帧回看）恒为可选项，总是报告；`ci` surface 额外把 `playwright` 报告为可选项（缺失时跳过截图取证并在报告标注）。

退出码 `1` = 有必需工具缺失。不自行安装：把缺失工具作为阻塞项汇报。可选工具缺失时按表内降级方式处理并在汇报中写明。

## task-local 运行时操作契约

权威细节以 xwing 仓库文档为准（`docs/agents/task-local-environment.md`）；以下是稳定操作面：

- 生命周期：`make agent-task-prepare TASK_ID=<id>` → `make agent-task-start-web` / `agent-task-start-desktop` → 验收完成后 `make agent-task-stop TASK_ID=<id>`（释放运行时，保留数据库与证据）。一任务一个 `TASK_ID`；端口布局、编号 CLI 身份与 config root、daemon 不跨任务复用。
- `.task-env/<TASK_ID>/` 布局：
  - `manifest.json`：分配的 `ports`、能力状态与 daemon 记录。
  - `.env.task`：`TASK_ENV_CLI_BINARY`（端口编号 CLI 的绝对路径）。
  - `runtime/auth.env`：`TASK_ENV_API_URL` / `TASK_ENV_API_TOKEN`。调本地 API 时 source 使用；绝不打印 token、绝不作为命令行参数传递、绝不复制。install token 是机器凭据，同规则。
- 不手改 task env 文件、manifest 或 registry；不用无端口后缀的默认 config root（`~/.hifox*` / `~/.sharkly*`）做 computer 注册或 daemon 启动；绝不把本机注册到共享或远端环境。
