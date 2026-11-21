# super-agent — 宋江实例

本目录是 **super-agent 框架的一个运行时实例（宋江）**：框架代码在 git 子模块 `vendor/super-agent`（[vin568/super-agent](https://github.com/vin568/super-agent)，branch `main`），这里只有身份、能力声明与配置。跑在本机 herdr 上，不经 x-deploy 部署。

术语：实例运营词见根 `GLOSSARY.md`「主控编排」；机制词见 `vendor/super-agent/GLOSSARY.md`。架构与边界见 `docs/adr/0016`–`0035`（提炼框架的决策是 ADR 0034）。

## 结构

```
super-agent/
├── superctl             # 本实例的 superctl 入口（从 vendor/super-agent 跑；配置的 runtime_cmd 指向它）
├── capabilities/        # 能力声明（目录即注册表）：agents/ skills/ scripts/ platforms/<平台>/
├── direct/              # 直连白名单脚本（ADR 0019 第 2 级）
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
#    必须显式给 runtime_cmd（指向本目录的 superctl），否则派活注入的回报命令会扑空。

# 3. 自检 + 起拓扑 + 常驻
./super-agent/superctl capabilities --check     # 框架不变量
npm run check                                   # 框架 + 本实例的跨仓对账
./super-agent/superctl ensure
./super-agent/superctl serve
```

`go run` 只用于开发和临时排查：它生成的可执行文件是临时产物，框架会拒绝把它注给 agent（回退到配置的 `runtime_cmd`）。

本机已部署的实例用 `~/.config/super-agent/superctl.toml`；旧的 `~/.config/songjiang/superctl.toml` 与 `SONGJIANG_*` 凭证前缀仍被接受（一版，带告警）。

## 构建与测试

- 框架：`go -C vendor/super-agent build ./... && go -C vendor/super-agent test -race ./...`
- 实例自检：`npm run check`（需要 `yq`；`npm run check:live` 另需 `hifox` 登录）
- 技能脚本：`npm test`（仓根）

## 命令面

`./super-agent/superctl help` 是权威（命令表由框架二进制给出）。速览见框架 README。

## 边界提醒

- 主控与吴用分面（ADR 0017）：两个 bot 两套台账，互不同步。平台派活员朱贵是第三面（本机 job agent，机器入口，ADR 0023）：主控不挑人、不解析平台 ref。
- 主控不自己干活：平台/产品交付 → 朱贵；本机**只读**活（调研、排查、资料搜集）→ 石秀（ADR 0027）；**改本仓文件的活** → 安道全（ADR 0035，worktree 里开发、只 push 自己的特性分支）。
- `config/<集合>/agents/<key>` 声明与 `capabilities/platforms/<平台>/<key>.yaml` 双向一一对应（同步规矩，ADR 0023），由本实例的 `npm run check` 强制（框架 `capabilities --check` 只管框架不变量）。
- 密钥只走环境变量，不进配置文件、台账、结果文件（密钥卫生）。
- `direct/` 白名单按需添加，不预制占位脚本。
