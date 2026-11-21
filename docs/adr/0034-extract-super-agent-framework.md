# 提炼 super-agent 框架：运行时搬进子模块，本仓只留实例

ADR 0016 把运行时落进本仓 `super-agent/`，理由是「运行时与能力声明同仓演进，加能力只在一处闭环」。2026-11 用户决定让其他人也能用这套编排：他们只需要自定义 agent 配置与能力配置，就能得到一个自己的 super agent。运行时留在这里，就意味着每个 adopters 都要 fork 一份本仓，或者看懂本仓的部署世界。

**决策：**

- **运行时提炼为独立框架仓**（私有 `vin568/super-agent`，module path `github.com/vin568/super-agent`），以子模块 `vendor/super-agent` 引入本仓，branch 固定 `main`，指针锁版本。
- **框架只提供机制**：拓扑引导、注入、在途清单、平台哨兵、上下文治理、派活与回帖通道。身份、人设、能力内容、台账格式全归实例。`master/retro/job` 与 workspace 标签是机制词，保留。
- **本仓退化为运行时实例（宋江）**：`super-agent/` 只留 `capabilities/`、`direct/`、`scripts/`、`config.example.toml` 与一个 `superctl` 入口脚本；Go 代码、框架文档、框架 ADR 全部随框架仓走。
- **跨仓对账移回实例**：`config/` ↔ `platforms/` ↔ `environments/*` 的双向同步、部署记录、线上真值由本仓的 `super-agent/scripts/check-registry.mjs`（`npm run check`）负责。框架的 `capabilities --check` 只管框架不变量。
- **零迁移承诺收窄为「状态不搬家」**：`~/.local/state/songjiang/`、`SONGJIANG_*` 凭证前缀、`~/.config/songjiang/superctl.toml` 继续可用（后两者带告警，一版期限）；新配置在 `~/.config/super-agent/superctl.toml`，实例根显式写出。
- **ADR 0016 的归属决定被取代**，其余部分（Go 单二进制、不经 x-deploy、测试自管）继续成立。框架自身的机制决策在框架仓 `docs/adr/0001`–`0003`。

**Considered Options：**

- **不拆仓，原地做成可复制模板**：拒。模板会分叉，本仓的实例改动与框架改进混在一个历史里，升级 = 手工 cherry-pick。
- **Go module `require` 而不是子模块**：拒。私有 module 没有 proxy，adopters 与 CI 都要配 `GOPRIVATE`；子模块的 commit 指针就是版本锁，与 `vendor/x-deploy` 同构。
- **框架自带 release 二进制**：暂缓（用户决定先用 `go run`）。开发期用仓库里的 `superctl` 包装脚本提供稳定入口；派活的回报命令不能指向 `go run` 的临时产物。

**Consequences：**

- 本仓不再有 Go 测试面；根 `npm test` 管技能脚本与实例检查脚本的用例，`npm run check` 管实例对账。
- 框架改动要进子模块仓（流程与 `vendor/x-deploy` 相同：子模块内开发、测试、提交、推送，再回根仓更新指针）。
- 验收线（用户定）：现有实例行为零变化；`superctl init --no-input` 生成的第二个实例跑通「私聊 → 主控 → 本地 job → 回帖」；新增维护 agent 只需改配置与能力声明；框架 `go test -race ./...` 与实例 `npm run check` 全绿。
- 术语分工：框架机制词归 `vendor/super-agent/GLOSSARY.md`，实例运营词留根 `GLOSSARY.md`（`GLOSSARY-MAP.md` 增列第三个上下文）。
