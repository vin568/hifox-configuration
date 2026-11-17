# super-agent — 宋江本地运行时

宋江（主控）super agent 的 Go 单二进制运行时：飞书注入器、`superctl` CLI、作业巡检 tick、重启引导。术语见根 `GLOSSARY.md`「主控编排」节；架构与边界见 `docs/adr/0016`–`0019`。跑在本机 herdr 上，不经 x-deploy 部署。

## 结构

```
super-agent/
├── cmd/superctl/        # 单二进制入口（serve/ensure/status/inject/tick/reply）
├── internal/config/     # superctl.toml 加载与路径约定
├── internal/herdr/      # herdr CLI 薄封装 + socket 事件订阅（按 pane 订阅状态变化）
├── internal/boot/       # 注入前幂等引导（拓扑对账 + 首条 prompt）
├── internal/inject/     # 注入信封（feishu/event）与 blocked 语义
├── internal/feishu/     # 飞书长连接接入：三层硬过滤 + 去重 + 重试 + 发送
├── internal/hifox/      # hifox CLI 薄封装（task view 真值与变化签名）
├── internal/sentinel/   # 云端哨兵：跟踪清单轮询 + 快照对比 + 有变化才注入
├── internal/tick/      # 作业巡检注入文本（周期校验在 internal/config）
├── prompts/             # 宋江操作手册、能力目录、蒋敬手册（运行时物料，不进 config/）
└── direct/              # 直连白名单脚本目录（ADR 0019 第 2 级）
```

状态目录默认 `~/.local/state/songjiang/`：`ledger/active|archived`（主控维护）、`results/`、`outbox.jsonl`、`dedup.jsonl`、`watchlist.json`（主控维护的云端跟踪清单）、`cloud-snapshot.json`（哨兵私有快照）、`primed/`（prime 成功标记）。注意：`cloudwatch` 子命令与 `serve` 并跑会对同一变化双注入（哨兵无跨进程锁），它只在 serve 停机时手动/cron 顶班用。

## 构建与测试

```bash
cd super-agent
go build ./... && go vet ./... && go test ./...
```

（仓 mise 已固定 go 1.26；PATH 里的老 go 不用时 `mise exec go -- go test ./...`。）

## 首次落地

1. **herdr 命名 session**：`herdr session attach super-agent`（需要终端；常驻机器上可用 tmux/pty 服务方式挂着）。本仓已验证：session 起来后 `HERDR_SESSION=super-agent herdr api snapshot` 可达。
2. **omp 集成**：`herdr integration install omp`（当前机器已是 current v10，无需动作）。
3. **配置**：`cp super-agent/config.example.toml superctl.toml`，填 `allowed_users`（你的 open_id）；代答群/全收群按需。
4. **飞书自建应用**（宋江身份，与吴用的 bot 分开，ADR 0017）：开发者后台建应用、开长连接事件订阅、拿 app_id/secret，导出：
   ```bash
   export SONGJIANG_FEISHU_APP_ID=cli_xxx
   export SONGJIANG_FEISHU_APP_SECRET=xxx
   ```
5. **取 ID（probe 引导）**：`./bin/superctl probe`——启动即打出 `bot_open_id`；给 bot 发条私聊（或在群里 @ 它），每条事件一行打出 `chat_id / 发送者 open_id / mentions`，照抄进 superctl.toml：你的 open_id → `allowed_users`，与 bot 的私聊 chat_id → `admin_chat_id`，群 chat_id → `proxy_reply_groups` / `any_message_groups`。
6. **引导 + 常驻**：
   ```bash
   go build -o bin/superctl ./cmd/superctl
   ./bin/superctl ensure     # 建 master/crew/jobs workspace，起 songjiang/jiangjing，灌手册
   ./bin/superctl serve      # 飞书长连接 + outbox 投递 + 定时巡检
   ```

常驻方式任选：launchd/tmux/nohup。serve 内置两路加速：**本地通道**按 pane 订阅 herdr 状态变化事件（能力 agent 一变即唤醒主控，订阅面 = 除主控外所有带 agent 的 pane，60s 重建收敛动态增删）；**云端通道**由哨兵每 `cloud_poll_interval` 轮询 watchlist 里的任务，状态真变化才注入（首见只记基线）。巡检 tick（默认 10m）仅作兜底。

## 命令面

| 命令 | 作用 |
|---|---|
| `ensure` | 幂等引导：对账拓扑，缺则补（注入前自动跑） |
| `status` | session/workspaces/agents/活跃作业/待发回帖 |
| `inject` | 手工注入（`--source feishu\|event\|manual`，测试与事件钩子用） |
| `tick` | 立即注入一次作业巡检（cron 兜底用） |
| `reply` | 追加回帖到 outbox（**主控回用户的唯一通道**） |
| `cloudwatch` | 云端哨兵一轮：读跟踪清单轮询 hifox，有状态变化才注入 |
| `serve` | 常驻：飞书长连接 + outbox 投递 + 定时巡检 |
| `probe` | 只打印事件 ID 的引导工具（bot_open_id / open_id / chat_id） |

命令与参数以 `superctl help` 输出为权威；上表是速览。

## 验收线（ADR/设计定稿 Q27）

飞书私聊发「让戴宗跑一次 staging 验收」→ 10 秒内受理回音 → 主控 `hifox task create --delegate-agent <daizong>` → 巡检跟踪到完成 → 结论回私聊。全程一体人设，无内部术语出口。

## 边界提醒

- 主控与吴用分面（ADR 0017）：两个 bot 两套台账，互不同步。
- 密钥只走环境变量，不进配置文件、台账、结果文件（密钥卫生）。
- `direct/` 白名单按需添加，不预制占位脚本。
