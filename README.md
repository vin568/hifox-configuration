# hifox-configuration — 宋江实例宿主 + xwing 部署配置

本仓是 **super-agent 框架的一个运行时实例（宋江）的宿主**：仓根就是实例根，`capabilities/`、`direct/`、`bin/` 都在这里；框架代码在 git 子模块 `vendor/super-agent`（[vin568/super-agent](https://github.com/vin568/super-agent)，branch `main`）。实跑在本机 herdr 上，不经 x-deploy 部署。

同一仓根还载着 **xwing 部署配置**（配置集合、环境绑定、共享技能源），全部收在 `.xwing/` 下——部署工具只认那四个目录与根 `x-deploy.yaml`，互不干扰。配置侧手册见 [`.xwing/README.md`](.xwing/README.md)。

术语：实例运营词见根 `GLOSSARY.md`「主控编排」；机制词见 `vendor/super-agent/GLOSSARY.md`。架构与边界见 `docs/adr/0016`–`0035` 与 [`docs/adr/0052`](docs/adr/0052-runtime-at-root-deployment-under-xwing.md)（提炼框架的决策是 ADR 0034）。

## 结构

```
仓根（= 实例根）
├── superctl             # 开发期入口（go run；不要作为 runtime_cmd）
├── scripts/build        # 稳定入口构建：注入 build 戳，产出 bin/superctl
├── scripts/check-registry.mjs  # 实例自检（框架不变量 + 跨仓对账）
├── bin/superctl         # 稳定入口（构建产物，gitignore；runtime_cmd 与受管 serve 都用它）
├── capabilities/        # 能力声明（目录即注册表）：agents/ skills/ projects/ scripts/ platforms/<平台>/ nodes/
│                        #   platforms/<平台>/defaults.yaml = 平台默认 org/space（不指定承接 agent 的建单用）
│                        #   nodes/<key>.yaml = 执行节点（寻址制；本机节点可选；ADR 0041/0042）
│                        #   nodes/<key>/checkouts.yaml = 该机器上各项目的绝对路径（ADR 0043）
│                        #   projects/<key>.yaml = 项目（仓库身份 + 基线；repo 缺省 = 目录型，只读）
│                        #   agents/<key>/projects.yaml = 这个能力能碰哪些项目
├── direct/              # 直连白名单脚本（ADR 0019 第 2 级；当前 worktree + server）
├── superctl.example.toml # 实例配置样例；实际配置在 ~/.config/super-agent/superctl.toml
├── x-deploy.yaml         # x-deploy 布局声明：四目录都指到 .xwing/ 下（ADR 0027/0052）
├── .xwing/               # xwing 部署配置载荷（config/ environments/ skills/ snapshots/ backups/
│                         #   .contract-snapshots/；手册见其 README）
├── .agents/skills/       # 开发 agent 用的项目级技能（含 x-deploy 受管重置的 xwing-workflows）
├── docs/                 # ADR（本仓决策）
└── vendor/               # 三个子模块：x-deploy / v-skills / super-agent
```

框架代码（命令实现、配置加载、能力注册表、飞书/herdr/平台适配、上下文治理）全在子模块，本仓没有 Go 源码。平台任务的豁免表在 `state_dir/untracked.yaml`（`superctl untracked` 维护），不进仓库。

## 首次落地 / 开发

```bash
# 1. 子模块就位（首次）
git submodule update --init vendor/super-agent

# 2. 配置：cp superctl.example.toml ~/.config/super-agent/superctl.toml 并填值
#    instance_dir 指仓根；runtime_cmd 指向构建产物 bin/superctl（先构建；包装脚本只用于调试）。

# 3. 构建稳定入口 + 自检 + 起拓扑 + 常驻
./scripts/build                                 # 产出 bin/superctl（带 version/commit/dirty/buildTime）
./bin/superctl capabilities --check            # 框架不变量
npm run check                                   # 框架 + 本实例的跨仓对账
./bin/superctl ensure
./bin/superctl server start                    # 受管 daemon；主控经 direct/server 查/启/停/重启
```

`go run` 只用于开发和临时排查：它生成的可执行文件是临时产物，框架会拒绝把它注给
agent（回退到配置的 `runtime_cmd`），也无法被 `server start/restart` 管理。

## 常驻 serve 的日常操作

- **改完代码**：先 `scripts/build`，再 `server restart`（或由主控经 `direct/server restart`）。
  入口过期（源码比 bin 新、commit 不符、无 build 戳）时 `start/restart` 会拒绝并提示重编。
- **查看状态**：`./bin/superctl server status`（默认中文文本，`--json` 给机器）；
  六态与字段见框架 README「常驻服务」。
- **停止/启动**：`server stop` / `server start`；停止前先让用户知道「停后飞书联系不上」，
  终局 drain 负责把已排队的回帖发出去（≤10s）。
- **日志**：`server logs --lines 200`（或 `--follow`）；审计在 `state_dir/serve-audit.jsonl`。
- **主控权限**：只经 `direct/server status|start|stop|restart`（actor=master）；
  构建、改配置、重启 herdr/主控都不在白名单内（ADR 0036）。
- 运行中的 `dev` 入口（wrapper / go run）与未受管实例可查可停，**不能** restart：先停掉它，再用稳定入口启动。
- **主控 blocked**：`blocked` 是 herdr 的状态名，两个成因（herdr 只报状态不报成因）：
  ① harness 停在审批/提问 UI 等人按键（omp 的 `ask` 表单）；② omp 的自动重试预算耗尽
  （provider 超时等）。两种情况下所有注入都在写输入之前被拒。
  框架处置（框架 ADR 0017）：巡检每轮写一条 `blocked:master` 发现、经 outbox 给用户发一句
  提醒（带屏上最后一行）；**是 `ask` 就不换脑**（会丢掉那个问题，只等人回答），
  非 ask 则卡满 `blocked_recover_after`（本实例 `30m`）自动换脑，24h 内至多两次。
  自查：`superctl status`（`master_blocked.since` / `recoveries` / `awaiting_answer`）、
  `superctl finding list --pending`、`state/blocked.json`（含 `screen_tail` 与 `awaiting_answer`）。
  人工兜底：去那个 pane 回答（ask）或点框，或 `superctl new` 立刻换脑。
- **主控与作业都不该用确认框提问**：ask 会把回合卡在 blocked、停掉整条注入链。
  主控要用户拍板就用 `superctl reply --admin`；作业要裁决就用回报通道给主控。
  作业真的卡在 ask 上时：主控读屏（`herdr agent read <名字>`）看它问什么，能按既有授权答就
  `superctl tell` 回去（必须在台账记一句），拿不准就问用户。

本机已部署的实例用 `~/.config/super-agent/superctl.toml`。旧配置路径与旧键已按一版期限移除（框架 ADR 0005）。

## 第二实例验收（ADR 0034）

新建实例与本地闭环（无需飞书凭证；会话与状态目录必须与生产不同）：

```bash
tmp=$(mktemp -d)
./bin/superctl init --no-input --dir "$tmp" --master-name 张三 \
  --master-key zhangsan --session smoke-second
./bin/superctl capabilities --check --root "$tmp"
herdr session attach smoke-second          # 另开终端；等 socket 起来
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./bin/superctl ensure
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./bin/superctl dispatch \
  --local --agent worker --body "把 ok 写进结果文件"
# 结果文件路径在 dispatch 输出里；确认内容后：
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./bin/superctl cancel --agent worker-<时刻>
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./bin/superctl reply --chat oc_smoke --text ok
SUPERAGENT_CONFIG="$tmp/superctl.toml" ./bin/superctl outbox list --state pending
```

完整「飞书私聊 → 主控 → 本地 job → 回帖」还需要第二个自建应用的凭证（`<prefix>_FEISHU_APP_ID/_SECRET`）；
不能与生产实例共用同一个 bot，否则两个长连接会抢消息。这个沙箱用完即删：`herdr session delete smoke-second`，
再删 `$tmp` 与 `~/.local/state/smoke-second`。

## 构建与测试

- 框架：`go -C vendor/super-agent build ./... && go -C vendor/super-agent test -race ./...`
- 实例自检：`npm run check`（需要 `yq`；有 `nodes/` 声明时还需要 `herdr` 做 profile 对账，未安装则跳过并提示；`npm run check:live` 另需 `hifox` 登录）
- 技能脚本：`npm test`（仓根）

## 命令面

`./superctl help` 是权威（命令表由框架二进制给出）。速览见框架 README。

## 边界提醒

- 主控与吴用分面（ADR 0017）：两个 bot 两套台账，互不同步。群/话题的服务边界、回复锚点、任务负责人/发起人/来源见本仓 ADR 0038–0040。平台派活员朱贵是第三面（本机 job agent，机器入口，ADR 0023）：主控不挑人、不解析平台 ref。
- 主控不自己干活：平台/产品交付 → 朱贵；本机**只读**活（调研、排查、资料搜集）→ 石秀（ADR 0027）；**改本仓文件的活** → 安道全（ADR 0035，worktree 里开发、只 push 自己的特性分支）；执行节点由主控直连 `dispatch --node`（钉死）或 `dispatch --auto`（按准入阈值自动择节点），不经朱贵/平台通道（ADR 0041/0042）。
- `.xwing/config/<集合>/agents/<key>` 声明与 `capabilities/platforms/<平台>/<key>.yaml` 双向一一对应（同步规矩，ADR 0023），由本实例的 `npm run check` 强制（框架 `capabilities --check` 只管框架不变量）。
- 密钥只走环境变量，不进配置文件、台账、结果文件（密钥卫生）。
- `direct/` 白名单按需添加，不预制占位脚本。
- 项目与检出（ADR 0043）：`capabilities/projects/<key>.yaml` 是机器无关的仓库身份，路径只写在 `nodes/<key>/checkouts.yaml`。`dispatch --project <key>` 默认只读；写作业显式 `--in-place`（原地写，脏树要 `--allow-dirty`）或 `--worktree <作业名>`（建作业工作区）。平台侧的仓库绑定仍是 `.xwing/config/` 的 `agent.yaml`，两套互不相通。
