# 主控管理常驻 serve：唯一白名单动作、显式停止、无自主拉起

`serve` 是用户唯一的飞书入口；它挂在主控之外，过去明文禁止主控停/换/重启运行时（ADR 0018），所以「服务卡死」只能等用户自己进终端。用户要求主控能在明确指示下启停/重启它，并把日常实例跑在稳定入口上；同时不能把「主控自改运行时」的口子重新打开。

**决策：**

- **唯一白名单动作**：新增 `super-agent/direct/server`（`status|start|stop|restart`），登记 `capabilities/scripts/server.yaml`；只有宋江可执行，石秀/安道全的运行时禁令不变。`direct/server` 用绝对路径调用，不依赖主控 pane 的 cwd。
- **动作边界**：
  - `status`：随时可查，只读。
  - `start` / `stop`：只在用户明确指示时执行。
  - `restart`：用户明确指示时执行；唯一自主例外是**运行中的 production 实例处于 degraded**，每个事件只重启一次，然后汇报结果。`local` 构建只接受明确指示；`dev` 实例拒绝 start/restart。
  - 永不做：改源码、构建/重编、改配置或凭证、重启 herdr/主控/其他服务。入口过期就告诉用户 `super-agent/build`，不自己 build。
- **停服话术与顺序**：停之前先告诉用户「停后飞书联系不上，需要在终端再启」；先 `reply` 再 `stop`，stop 的终局 drain 负责把这条回执发出去（机制见框架 ADR 0004）。
- **模式与构建**：日常实例跑稳定入口（production / local build），wrapper / go run 只用于临时调试；`super-agent/build` 由用户或维护员在合并后执行。主控只消费构建产物。
- **验收换脑**：当前主控会话的注入仍写旧入口；验收包含一次用户执行的 `new`，让新手册生效后再由主控执行 start/status/restart/stop。`new` 是一次性切换动作，不属于生命周期命令。
- **文档同步**：ADR 0018 的运行时禁令按本 ADR 收窄（supervisor/watchdog 禁令不变）；ADR 0019 的第 2 级新增本例外；ADR 0028 的「重启由用户执行」改为「用户或主控经 direct/server」。

**Considered Options：**

- **维持禁令，只让用户操作**：拒。服务卡死时用户必须自己进终端，主控作为「用户替身」缺一条基本运维路径。
- **授权主控直接跑 `superctl server …`**：拒。命令面比白名单宽，且没有单一入口做模式/审计/拒绝。
- **允许主控用 wrapper（go run）重启 dev**：拒。那等于允许主控触发编译，重新打开「运行时自改」的口子。
- **停服后自动拉起**：拒。停止可能是用户故意的；自动拉起会和 dev 循环、手动停服冲突。

**Consequences：**

- 主控手册、石秀/安道全边界、直连 README 与登记表同步；`npm run check` 两向校验。
- 每次动作落 `state/serve-audit.jsonl`（actor=master/user/auto）；用户与蒋敬可复盘。
- 无 supervisor：服务半夜挂了不会主动告警；主控下一次 status / 下一次 start 才能发现。自动拉起是另一个决策。
- 停服会切断飞书入口；恢复不能依赖飞书指示，必须由人在终端发起（或由已被本地事件唤醒的主控发起）。
