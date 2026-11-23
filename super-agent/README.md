# super-agent — 宋江实例

本目录是 **super-agent 框架的一个运行时实例（宋江）**：框架代码在 git 子模块 `vendor/super-agent`（[vin568/super-agent](https://github.com/vin568/super-agent)，branch `main`），这里只有身份、能力声明与配置。跑在本机 herdr 上，不经 x-deploy 部署。

术语：实例运营词见根 `GLOSSARY.md`「主控编排」；机制词见 `vendor/super-agent/GLOSSARY.md`。架构与边界见 `docs/adr/0016`–`0035`（提炼框架的决策是 ADR 0034）。

## 结构

```
super-agent/
├── superctl             # 开发期入口（go run；不要作为 runtime_cmd）
├── build                # 稳定入口构建：注入 build 戳，产出 bin/superctl
├── bin/superctl         # 稳定入口（构建产物，gitignore；runtime_cmd 与受管 serve 都用它）
├── capabilities/        # 能力声明（目录即注册表）：agents/ skills/ scripts/ platforms/<平台>/ nodes/
│                        #   platforms/<平台>/defaults.yaml = 平台默认 org/space（不指定承接 agent 的建单用）
│                        #   nodes/<key>.yaml = 远端执行节点（寻址制；见 ADR 0041）
├── direct/              # 直连白名单脚本（ADR 0019 第 2 级；当前 worktree + server）
├── scripts/             # 本实例自检（框架不认识 config/ 与 environments/，跨仓对账在这里）
├── config.example.toml  # 实例配置样例；实际配置在 ~/.config/super-agent/superctl.toml
└── README.md
```

框架代码（命令实现、配置加载、能力注册表、飞书/herdr/平台适配、上下文治理）全在子模块，本目录没有 Go 源码。

## 首次落地 / 开发

```bash
# 1. 子模块就位（首次）
git submodule update --init vendor/super-agent

# 2. 配置：cp super-agent/config.example.toml ~/.config/super-agent/superctl.toml 并填值
#    runtime_cmd 指向构建产物 super-agent/bin/superctl（先构建；包装脚本只用于调试）。

# 3. 构建稳定入口 + 自检 + 起拓扑 + 常驻
./super-agent/build                             # 产出 super-agent/bin/superctl（带 version/commit/dirty/buildTime）
./super-agent/bin/superctl capabilities --check # 框架不变量
npm run check                                   # 框架 + 本实例的跨仓对账
./super-agent/bin/superctl ensure
./super-agent/bin/superctl server start         # 受管 daemon；主控经 direct/server 查/启/停/重启
```

`go run` 只用于开发和临时排查：它生成的可执行文件是临时产物，框架会拒绝把它注给
agent（回退到配置的 `runtime_cmd`），也无法被 `server start/restart` 管理。

## 常驻 serve 的日常操作

- **改完代码**：先 `super-agent/build`，再 `server restart`（或由主控经 `direct/server restart`）。
  入口过期（源码比 bin 新、commit 不符、无 build 戳）时 `start/restart` 会拒绝并提示重编。
- **查看状态**：`./super-agent/bin/superctl server status`（默认中文文本，`--json` 给机器）；
  六态与字段见框架 README「常驻服务」。
- **停止/启动**：`server stop` / `server start`；停止前先让用户知道「停后飞书联系不上」，
  终局 drain 负责把已排队的回帖发出去（≤10s）。
- **日志**：`server logs --lines 200`（或 `--follow`）；审计在 `state_dir/serve-audit.jsonl`。
- **主控权限**：只经 `super-agent/direct/server status|start|stop|restart`（actor=master）；
  构建、改配置、重启 herdr/主控都不在白名单内（ADR 0036）。
- 运行中的 `dev` 入口（wrapper / go run）与未受管实例可查可停，**不能** restart：先停掉它，再用稳定入口启动。

本机已部署的实例用 `~/.config/super-agent/superctl.toml`。旧配置路径与旧键已按一版期限移除（框架 ADR 0005）。

## 第二实例验收（ADR 0034）

新建实例与本地闭环（无需飞书凭证；会话与状态目录必须与生产不同）：

```bash
tmp=$(mktemp -d)
./super-agent/bin/superctl init --no-input --dir "$tmp" --master-name 张三 \
  --master-key zhangsan --session smoke-second
./super-agent/bin/superctl capabilities --check --root "$tmp"
herdr session attach smoke-second          # 另开终端；等 socket 起来
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./super-agent/bin/superctl ensure
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./super-agent/bin/superctl dispatch \
  --local --agent worker --body "把 ok 写进结果文件"
# 结果文件路径在 dispatch 输出里；确认内容后：
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./super-agent/bin/superctl cancel --agent worker-<时刻>
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./super-agent/bin/superctl reply --chat oc_smoke --text ok
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./super-agent/bin/superctl outbox list --state pending
```

完整「飞书私聊 → 主控 → 本地 job → 回帖」还需要第二个自建应用的凭证（`<prefix>_FEISHU_APP_ID/_SECRET`）；
不能与生产实例共用同一个 bot，否则两个长连接会抢消息。这个沙箱用完即删：`herdr session delete smoke-second`，
再删 `$tmp` 与 `~/.local/state/smoke-second`。

## 构建与测试

- 框架：`go -C vendor/super-agent build ./... && go -C vendor/super-agent test -race ./...`
- 实例自检：`npm run check`（需要 `yq`；有 `nodes/` 声明时还需要 `herdr` 做 profile 对账，未安装则跳过并提示；`npm run check:live` 另需 `hifox` 登录）
- 技能脚本：`npm test`（仓根）

## 命令面

`./super-agent/superctl help` 是权威（命令表由框架二进制给出）。速览见框架 README。

## 边界提醒

- 主控与吴用分面（ADR 0017）：两个 bot 两套台账，互不同步。群/话题的服务边界、回复锚点、任务负责人/发起人/来源见本仓 ADR 0038–0040。平台派活员朱贵是第三面（本机 job agent，机器入口，ADR 0023）：主控不挑人、不解析平台 ref。
- 主控不自己干活：平台/产品交付 → 朱贵；本机**只读**活（调研、排查、资料搜集）→ 石秀（ADR 0027）；**改本仓文件的活** → 安道全（ADR 0035，worktree 里开发、只 push 自己的特性分支）；远端执行节点由主控直连 `dispatch --node`，不经朱贵/平台通道（ADR 0041）。
- `config/<集合>/agents/<key>` 声明与 `capabilities/platforms/<平台>/<key>.yaml` 双向一一对应（同步规矩，ADR 0023），由本实例的 `npm run check` 强制（框架 `capabilities --check` 只管框架不变量）。
- 密钥只走环境变量，不进配置文件、台账、结果文件（密钥卫生）。
- `direct/` 白名单按需添加，不预制占位脚本。
