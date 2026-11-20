# super-agent — 宋江本地运行时

宋江（主控）super agent 的 Go 单二进制运行时：飞书注入器、`superctl` CLI、作业巡检 tick、重启引导。术语见根 `GLOSSARY.md`「主控编排」节；架构与边界见 `docs/adr/0016`–`0032`。跑在本机 herdr 上，不经 x-deploy 部署。

## 结构

```
super-agent/
├── cmd/superctl/        # 单二进制入口（serve/ensure/status/inject/report/tell/tick/reply/history/recall/answer/halt/resume/new/dispatch/cancel/capabilities）
├── capabilities/        # 能力声明（目录即注册表，ADR 0021）：agents/ skills/ scripts/ platforms/<平台>/；技能是标准 SKILL.md，位置即归属（ADR 0025）；共享技能按 agent 的 skills.yaml 声明挂载
├── internal/config/     # superctl.toml 加载与路径约定
├── internal/capability/ # 能力注册表：目录即声明 + 索引生成 + 漂移检查（ADR 0021）
├── internal/halt/       # 急停闸门状态文件（ADR 0020）
├── internal/inflight/   # 在途清单 inflight.json（platform/local 两通道）
├── internal/herdr/      # herdr CLI 薄封装 + socket 事件订阅（按 pane 订阅状态变化）
├── internal/boot/       # 注入前幂等引导（拓扑对账 + 首条 prompt）+ 换脑（Fresh）
├── internal/inject/     # 注入信封（feishu/event）与 blocked 语义
├── internal/feishu/     # 飞书长连接接入：三层硬过滤 + 去重 + 重试 + 发送
├── internal/hifox/      # hifox CLI 薄封装（task view 真值、变化签名、agent 解析）
├── internal/outbox/     # 发件箱：主控回帖的唯一投递通道（重试/死信）
├── internal/sentinel/   # 平台哨兵：在途清单 platform 条目轮询 + 快照对比 + 有变化才注入
├── internal/tick/      # 作业巡检注入文本（周期校验在 internal/config）
└── direct/              # 直连白名单脚本目录（ADR 0019 第 2 级）
```

主控操作手册、能力用途与复盘者手册随能力注册表走（`capabilities/agents/<key>/instructions.md`，ADR 0021），独立的提示词目录不再是真源。

状态目录默认 `~/.local/state/songjiang/`：`ledger/active|archived`（主控维护）、`results/`、`briefs/`（平台任务正文的归档副本 `<任务号>.md`：派活员写、运行时归档，ADR 0024；本地作业的 `<agent>.md` 由运行时在起 agent 前落盘）、`progress/<agent>.md`（job 的交接进度）、`handoffs/`（交接记录）、`skills/<agent>/omp-skills.yml`（该 agent 的技能发现覆盖配置：指向 capabilities 下的真源目录，不复制技能本体，ADR 0025）、`media/`（飞书消息附件落盘：注入文本只带路径，目录 0700/文件 0600，默认保留 30 天）、`outbox.jsonl`、`dedup.jsonl`、`inflight.json`（在途清单，原 `watchlist.json` 改名扩义：kind=platform|local，哨兵只读 platform 条目）、`platform-snapshot.json`（哨兵私有快照）、`halt.json`（急停闸门与急停状态）、`context-alerts.json`（上下文告警去重）、`supervision.json`（僵尸/停滞告警去重）、`handoff.md`（轮换交接的机器真值）、`sessions/`（每轮次会话留档）、`primed/`（prime 成功标记）。注意：`platformwatch` 子命令与 `serve` 并跑会对同一变化双注入（哨兵无跨进程锁），它只在 serve 停机时手动/cron 顶班用。

## 构建与测试

```bash
cd super-agent
go build ./... && go vet ./... && go test -race ./...
```

`-race` 不是可选：飞书 SDK 每条消息起一个 goroutine，附件落盘的并发共享状态只有它在看
（ADR 0024）。不带 `-race` 跑，`TestMediaStoreConcurrentSave` 这类用例没有检出能力。

（仓 mise 已固定 go 1.26；PATH 里的老 go 不用时 `mise exec go -- go test -race ./...`。）

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
   ./bin/superctl ensure     # 按能力注册表建 master/crew/jobs 拓扑，起 songjiang/jiangjing，灌手册
   ./bin/superctl serve      # 飞书长连接 + outbox 投递 + 定时巡检
   ```

常驻方式任选：launchd/tmux/nohup。serve 内置两路加速：**本地通道**按 pane 订阅 herdr 状态变化事件（只有**在途本地作业**的 done/blocked 才唤醒主控；working/idle 与常驻 agent 不叫醒。订阅面 = 除主控外所有带 agent 的 pane，60s 重建收敛动态增删）；**平台通道**由哨兵每 `platform_poll_interval` 轮询在途清单里的平台条目，状态真变化才注入（首见只记基线；等用户回话的两态首见即注入）。巡检 tick（默认 10m）仅作兜底。

注入卫生与轮换（ADR 0022/0030）：巡检注入只在三源任一非空时发（活跃区台账文件数、在途清单条数、jobs workspace 活动 agent 数），全空只记日志；每条注入只带增量，样板只在首条 prime。上下文治理用统一公式（最近一次 provider prompt token + output + 未测量增量，除以模型窗口；小窗口保留一半，避免 ceiling 算成 0）；master/retro 达保留区且 idle/done 时自动换脑，job 达阈值只告警，由主控 checkpoint + handoff。会话字节数只在测量不可用时兜底。急停期间按来源分流静默自动注入，白名单私聊照常通行（ADR 0020）。

上下文测量（ADR 0030）：`internal/ctxmeter` 把 omp 会话尾部的 `contextSnapshot.promptTokens`/`usage`/`compactionEpoch` 归一成 Snapshot；模型窗口优先 harness 自报，其次 omp 的 models.db，最后用 `context_windows` 覆盖补目录查不到的模型。新 harness（opencode 等）用 `[context_probes]` 配置一条输出统一 JSON 的命令即可接入，阈值、告警、交接逻辑不变。探针命令可读 `AGENT_NAME/AGENT_ROLE/AGENT_KIND/AGENT_STATUS/SESSION_PATH/RESULT_PATH/BRIEF_PATH/PROGRESS_PATH`，输出：
```json
{"provider":"hifox","model":"deepseek-flash","prompt_tokens":119797,"output_tokens":30,"tail_tokens":0,"window_tokens":1000000,"epoch":0,"source":"measured"}
```

## 命令面

| 命令 | 作用 |
|---|---|
| `ensure` | 幂等引导：按能力注册表对账拓扑，缺则补（注入前自动跑） |
| `status` | session/workspaces/agents/活跃作业/在途清单条数/急停状态/主控会话体积/待发回帖/每个受控 agent 的 `agent_contexts`（token、窗口、比例、来源、brief/progress） |
| `inject` | 手工注入（`--source feishu\|event\|manual`，测试与事件钩子用） |
| `report` | 能力 agent → 主控：中途回报一句（`--agent` 自己的名字 + `--text`）；身份以在途清单为界，急停期间静默（ADR 0029） |
| `tell` | 主控 → 本地能力 agent：补信息或追问进度（`--agent` 目标 + `--text`）；目标 blocked 时如实失败，不替它按键 |
| `checkpoint` | 让 idle/done 的本地作业把进度写进 `progress/<agent>.md`（交接前置；working 时拒绝，不抢断） |
| `handoff` | 交接本地作业：校验 brief/progress/结果为空/状态 idle，停旧 agent 后重新拉起，结果写回原路径 |
| `tick` | 立即注入一次作业巡检（无在途内容时不发；cron 兜底用） |
| `dispatch` | 派活统一入口：`--platform <hifox>` 先把能力 key 解析成唯一平台 ref（`--instance prod\|staging` 消歧，`--delegate` 可直通），再包 `hifox task create`；`--local --agent <能力 key>` 按配方起 jobs agent、把该 agent 的手册与任务一起注入。两条通道都自动登记在途清单与台账骨架（仅文件不存在时建）。**主控的平台派活只走 `--local --agent zhugui`（平台派活员，ADR 0023）**：主控不挑人、不解析 ref |
| `cancel` | 撤销一件工作（平台任务：run 列表 → 取消 run；本地：关对应 pane）——撤销不走急停 |
| `halt` | 急停：落闸 + 秒级回执 + 换脑（同 pane 停主控、起新会话、注入急停意图）；飞书私聊 `/halt` 是等价入口 |
| `resume` | 解除急停闸门（离线兜底；在线时用户下一句私聊即自动恢复） |
| `new` | 人工清空：立刻换脑，不等上下文阈值（飞书私聊 `/new` 等价入口） |
| `capabilities` | 能力注册表：生成主控索引 / `--check` 查漂移（key 与目录名、master 唯一性、文档、脚本可执行、`direct/` 未登记脚本、平台指针 ↔ `config/` 双向同步与部署记录）/ `--check --live` 再按 `environments/*/state.yaml` 对平台核一遍（ADR 0023） |
| `reply` | 追加回帖到 outbox（**主控回用户的唯一通道**；`--image` 可重复，最多 9 张，纯文字路径逐字节不变） |
| `history` | 只读历史：`--chat <会话ID> [--limit 20] [--since 24h]` 或 `--message <om_...> [--download]`；输出纯文本，不注入、不写状态 |
| `recall` | 撤回本应用发的消息：`--id <回帖ID>` / `--message <om_...>` / `--last [--chat <会话ID>]`；`230110` 按成功处理 |
| `answer` | 把用户的回答投进平台任务对话（`--platform <平台>` + `--task` + `--text`/`--text-file`；目前只有 hifox）：承接人 `waiting_human_reply`（等回答）或 `waiting_human_review`（等验收）时用；任务评论不是回答通道，命令面不提供评论路径（ADR 0031） |
| `platformwatch` | 平台哨兵一轮：读在途清单的 platform 条目轮询平台，有状态变化才注入（等用户回话的两态首见也注入） |
| `serve` | 常驻：飞书长连接 + outbox 投递 + 定时巡检 |
| `probe` | 只打印事件 ID 的引导工具（bot_open_id / open_id / chat_id） |

命令与参数以 `superctl help` 输出为权威；上表是速览。

## 验收线（ADR/设计定稿 Q27）

飞书私聊发「让戴宗跑一次 staging 验收」→ 10 秒内受理回音 → 主控把请求派给本地平台派活员（`superctl dispatch --local --agent zhugui`，正文带原话 + 层级 + 验收）→ 主控写完台账即收轮（不轮询）→ 朱贵选人、调 `superctl dispatch --platform hifox` 建单，先 `report` 一句任务号与链接，再把结果写进结果文件 → 主控收到回报即回用户、收掉朱贵 → 平台哨兵跟踪到完成 → 结论回私聊。全程一体人设，无内部术语出口。

## 边界提醒

- 主控与吴用分面（ADR 0017）：两个 bot 两套台账，互不同步。平台派活员朱贵是第三面（本机 job agent，机器入口，ADR 0023）：主控不挑人、不解析平台 ref。
- 主控不自己干活：平台/产品交付 → 朱贵；其余本机活（调研、排查、改本仓、写脚本）→ 本地副手石秀（ADR 0027）。直连三级的「只读自由跑」只覆盖编排核对，不覆盖业务调研。
- `config/<集合>/agents/<key>` 声明与 `capabilities/platforms/<平台>/<key>.yaml` 双向一一对应（同步规矩，ADR 0023），由 `capabilities --check` 强制。
- 密钥只走环境变量，不进配置文件、台账、结果文件（密钥卫生）。
- `direct/` 白名单按需添加，不预制占位脚本。
