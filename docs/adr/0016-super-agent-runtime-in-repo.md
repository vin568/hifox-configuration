# 宋江 super agent 本地运行时落本仓，Go 单二进制

用户以 `feature/super-agent` worktree 开启 super agent 工作：主控（宋江）+ 能力 agent 的编排体系跑在本机 herdr 上，信号来自飞书消息与定时巡检（术语见 `GLOSSARY.md`「主控编排」节）。运行时物料——飞书注入器、`superctl` CLI、巡检 tick 调度、重启引导——需要一个归属。

**决策：**

- **运行时落本仓新目录 `super-agent/`，不另开仓**：能力目录的云端侧本来就是本仓 config 的声明，运行时与声明同仓演进，「加能力 = 加 config 声明」这一个动作只在一处闭环。
- **整个运行时是一个 Go 单二进制**：daemon 模式跑飞书注入器 / 巡检调度 / 重启引导，子命令模式即 `superctl`。首期跑在个人 Mac，后续迁内网 Linux 机器（ADR 决策时的部署计划），静态二进制零运行时依赖。飞书接入用官方 oapi-sdk-go 长连接；sharkly 的接入层（WS hub、两阶段去重 claim、durable outbox）只读借鉴机制，不拷代码——其后端（Postgres 状态机、多租户、run 调度）与 sharkly 平台强绑定。
- **测试约定**：`super-agent/` 内 `go test` 自管；根 `npm test` 仍只管技能脚本，互不接管。
- **不经 x-deploy 部署**：super-agent 是本地物料，不进 `environments/` 与 `state.yaml`；宋江也不在 hifox 平台注册 agent 身份——空壳身份会造成「平台有 agent 却永无 run」的错觉（与吴用的分界见 ADR 0017）。

**Considered Options：**

- **另开运行时仓**：拒。用户开本 worktree 即为此事；运行时与能力声明分家会让「加能力」跨两仓协作，分界文档也要双份。
- **全 NodeJS/TS**：拒。内网机器部署要管 node 环境与依赖安装；守护进程 + 静态分发是 Go 的主场，且飞书官方长连接 SDK 在 Go 侧最成熟。
- **运行时进 `vendor/x-deploy` 或根仓壳脚本**：拒。子模块是部署工具的家；根仓壳脚本按 `AGENTS.md` 不承载逻辑。

**Consequences：**

- `AGENTS.md` 的仓库定位、Super Agent 章节、子模块开发提交范围三处已同步增补。
- 本 ADR 只定归属与形态；落地前置：本机 herdr 初始化、`herdr integration install omp`、新建飞书自建应用（见 ADR 0018 的控制面前置）。
