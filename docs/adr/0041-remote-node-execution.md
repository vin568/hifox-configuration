# ADR 0041：远端执行节点——寻址制执行面，不是平台，也不是第二运行时

> 状态：`instance_root` 与「节点同布局」由 ADR 0043 取代（技能与直连脚本改为随作业投递）；寻址制、outbox、事件、fail closed 仍然有效。

> 状态：部分被 ADR 0042 取代——「本地 herdr 是隐式节点、只有远端写声明」的前提不再成立；寻址制、outbox、事件与 fail closed 仍有效。

现有两条派活通道：本地 herdr 里的 job agent（可监督：状态、tell、取消、checkpoint、handoff、上下文治理）与平台任务（hifox：任务号、哨兵、问答，执行体对主控不透明）。远程执行的需求是把前者的能力延伸到一台长驻远端开发/执行机：仓库、工具链、凭据都在那台机器上，任务产物以它的文件系统为准，且本地主控要保持同一套监督语义。

调研结论（Herdr 0.9.5）：

- 官方原则是 **“Run Herdr where the work lives. Attach from wherever you are.”** 远程 server 持有 pane 与 agent；本地 client 只负责画界面。
- **本地 herdr pane 里 ssh 不可作为编排通道**：herdr 的 agent 识别基于 pane 内的本机进程，ssh / VM / 容器里的 agent 属于文档明说「看不见」的情形，只会被当成普通终端；`agent start/prompt/wait/send-keys` 全部失去语义，断线还会带走远端进程。
- **`herdr --machine` 是官方远程自动化面**，但它只转发请求/响应命令（`workspace` / `worktree` / `tab` / `pane` / `notification` / `agent` 除 attach / `api snapshot` / `status server`），不转发事件订阅、interactive attach 与 session 管理；文件读写要另走 ssh。
- 事件面本身有 26 个事件（`events.subscribe`），但跨机需要自管 SSH streamlocal 隧道；一期不引入。

约束：本仓只有一个运行时实例、一个主控；远程不引入第二主控，也不在节点上跑 super-agent 运行时。

**决策：**

- **执行面三分，词各有界**：平台 = 有工作项面且有执行者（hifox，将来 sharkly）；执行节点 = 有执行者、无工作项面（远端 herdr server）；跟踪面 = 有工作项面、无执行者（Linear 单独接时是跟踪面，不是平台）。ADR 0023 里「远程 ssh 主机属于平台」的说法由本 ADR 取代。
- **节点是寻址制，不是注册制**：节点由本地运行时按 SSH 地址寻址，不向任何中心注册。这是它与 hifox Computer 的分界——Computer 要在机器上执行注册命令进平台，节点只存在于本机声明与 SSH 可达性里。同一台物理机可以既是某平台的 Computer 又是本实例的节点，两个注册面互不替代。
- **本地 herdr 是隐式节点**：不单独声明；只有远端节点写 `capabilities/nodes/<key>.yaml`。字段：`key`（稳定身份）、`name`（可选，人读）、`ssh`（别名 / `user@host`，端口与密钥走 `~/.ssh/config`）、`machine`（herdr saved profile 标签或 ID）、`session`（远端命名 session）、`instance_root`、`state_root`、`work_dir`、`note`。不写 `ip` / `port` / `user` / `computer`。
- **传输层**：控制命令走 `herdr --machine <profile>`；文件读写走 `ssh <target>`。`herdr machine add` 是一次性的本机客户端配置，不是中心注册；它带来参数不做 shell 插值、版本兼容检查与受管连接复用。不采用「本地 herdr pane 里 ssh」，也不以直接 `ssh <host> herdr …` 作为主路径（要自担引用安全与版本漂移；仅在 profile 无法建立时作为人工兜底）。
- **agent 声明目标**：`capability.yaml` 新增 `targets: [local, workbox]`，完整集合，缺省 `[local]`，不允许通配。节点专属 agent 写 `targets: [workbox]`；派活前校验，不匹配直接失败。加节点不需要改节点侧名单，需要改的是愿意去该节点的 agent（评审面在 PR）。
- **同一布局，不同根**：节点上是同一份实例布局（技能、手册、agent 配方、仓库），路径按 `instance_root` / `state_root` / `work_dir` 映射；这三个值都指向**节点文件系统**（远端绝对路径），声明文件本身在本机。`work_dir` 必须存在（agent cwd）；`state_root` 由派活方按需创建结果/进度/brief/report/附件目录；`instance_root` 可以不存在——手册按本机文本注入，只有声明了技能的 agent 才要求布局，缺它即 fail closed。节点不复制配方与技能源，也不持有运行时状态。节点需要自行常驻 herdr server（launchd / systemd / 登录项），本地不负责启动、安装或重启它。
- **派活入口**：主控直连 `superctl dispatch --node <key> --agent <能力 key>`，与 `--local` 同形、与 `--platform` / `--local` 互斥；不经平台派活员朱贵。节点上的拓扑只按需建 `jobs` workspace 与其根 pane（`ensureNode`），不建 master / crew，不起常驻 agent。附件在起 agent 前复制到节点 `state_root/attachments/<run>/`，prompt 与 brief 只出现远端路径；读远端结果/brief/进度用 `superctl node read --node <key> --path <远端路径>`。
- **在途清单**：沿用 `kind: local`，新增 `node` 字段（空 = 本机）。`kind` 区分的仍是执行契约（任务板 vs agent 生命周期），节点只是位置维度；agent 名按节点作用域，`tell` / `cancel` 在名字不唯一时要求 `--node`，触发载荷写 `<节点>/<agent>`。
- **管控与本地对齐**：状态靠 `agent wait`（服务端长等待）+ `agent list`；`tell`、`pane close` 取消、checkpoint、人工 handoff 全部跨机支持。取消 = 按在途条目的远端 pane 关掉、确认 agent 消失、再移出清单；关不掉保留条目报错。上下文测量经 ssh 读远端 session 文件（路径来自远端 `agent list` 的 `agent_session.value`），复用现有 OMP 解析与公式；只有 `measured` 允许自动轮换，`estimated` / `unknown` 只告警。
- **远程 report 走节点本地 outbox**：派活时为该 run 写一个 report shim，`RuntimeCmd` 指向它；agent 仍执行 `superctl report --agent … --text …`，shim 只把内容追加到 `<state_root>/reports/<runName>.md`。本地运行时在 `agent wait` 返回/超时、tick、`cancel` / `handoff` 前经 ssh 抽取新内容，注入 `[触发器: agent] <节点>/<agent>：<内容>`。抽取按节点合并、按偏移/已读标记去重；急停期间不注入，只给未交付的 outbox 打积压标记（不推进偏移），恢复后补交并标「积压」。不采用反向 SSH（节点→本地），也不采用终端哨兵作为契约。
- **事件一期只做两类**：agent 状态转换（`agent wait`）与 pane 输出匹配（`pane wait-output`，转发能力需实机验证，失败则退回 `pane read` 轮询）；拓扑/焦点/布局/worktree 事件靠 tick 与 `api snapshot` 差分。SSH streamlocal 隧道留到确实需要亚分钟拓扑事件时再评估。
- **失败语义 fail closed**：派活前 `herdr machine status --json` 探测；不可达直接失败、不建 pane、不登记。在途作业不可达 = 保留条目、标 degraded、告警，不自动重派、不自动改派平台。
- **首批负载**：只读调研/核对（石秀）→ 本仓改动（安道全，worktree + push 特性分支）；部署/发布不走节点（有自己的流水线门禁）。
- **对账**：离线 `npm run check` 校验 key 与目录一致、`ssh` 非空、`machine` 在 `herdr machine list --json` 存在且 target 与 `ssh` 一致、`session` 与 profile 一致、三个根为绝对路径、每个 `targets:` 值指向存在的节点 key；`check:live` 用 `machine status` 探可达与兼容。在线状态永不写进声明，不碰 `config/`。

**Considered Options：**

- **本地 herdr pane 里 ssh 到远端跑 agent**：拒。agent 进程不在本机，herdr 看不见；`agent start/prompt/wait` 没有真值，断线即死。只保留为人工排障手段。
- **远端跑第二份运行时/第二主控**：拒。两份在途清单、两套台账，且违反「一个实例恰好一个主控」。本地主控若要读远端结果，仍要跨机，收益为零。
- **把远端 herdr 当作 platform**：拒。节点没有工作项面，`--platform` 的建单/哨兵/任务号语义无处落地。
- **用 hifox Computer 作节点真源**：拒。Computer 是注册制、需要机器侧命令与中心服务器；本实例没有中心，能力索引也必须离线可用。两者可以在同一台物理机上并存，但注册面不共享。
- **节点声明 agent 白名单（反向绑定）**：拒。hifox 的 `target.computer` 在 agent 侧；单向真源避免两边各改一边。代价是新增节点要改愿意去它的 agent，靠 PR 评审而非机器强制。
- **直接 `ssh <host> herdr --session …` 作为主传输**：拒为主路径。prompt 正文含引号/换行时是新的注入面，版本漂移与连接复用也要自担；`--machine` 的官方转发面更窄也更安全。
- **反向 SSH 让节点调用本地 `superctl report`**：拒。新的信任方向（节点→本地）、节点可能不可达本地、本地还要认证节点身份；outbox 不需要任何反向连通性。
- **一期上 SSH streamlocal 隧道拿全量事件**：暂拒。要自管隧道生命周期、残留 socket 与远端 socket 路径；一期需要的信号（在途 agent 状态变化）`agent wait` 已覆盖。
- **不支持远程 report**：拒（Q12 原案撤回）。outbox 能在不引入反向信任的前提下恢复语义；只有「工作中顺带报的关键结果」有 ≤ 一个 wait 超时（60s）的延迟。
- **远程自动轮换与本地同样开启**：拒。测量来源不是 `measured` 时不能自动换脑；与本地同一门槛。

**Consequences：**

- 节点准入是运维前置：远端有 herdr server 在跑、装了 omp 与同一份实例布局、完成过一次 `herdr machine add`；没有自动安装/启动/升级。
- 跨机监督有可观测的延迟项：event 流不转发，拓扑靠轮询；工作中 report 最多等一个 `agent wait` 超时（默认 60s），状态变化路径即时。
- `ctxmeter` 需要一条远端读缝（本地文件与 ssh 读共用解析与公式）；远程自动轮换在测量稳定前只告警。
- `targets` 无通配：新增节点时，愿意去该节点的 agent 必须显式加值；这是有意的评审面。
- 节点与本地是两份 checkout：技能/手册/配方可能漂移。`npm run check` 只校验声明与 profile，不负责同步；同步是节点运维责任。
- 节点声明含机器标识（SSH 目标、路径、session），按 ADR 0008 只在内部面存在，不进入对外输出、台账正文或平台任务。
- 框架落点：machine 前缀的 herdr 客户端、`runtime.HerdrNode`、`capability.Agent.Targets`、节点注册表加载/校验、`inflight.Entry.Node`、`dispatch --node`、`ensureNode`、`ctxmeter` 远端读、report outbox 抽取，以及 health/context/handoff/cancel/tell 的节点分支，全部带测试。框架侧机制细节在实现时补一份子模块 ADR，并同步 `vendor/super-agent/GLOSSARY.md` 的「受控 agent」定义（纳入节点作业）。
- 实例文档同步：`platform-dispatch` 与朱贵手册里「远程 ssh 主机属于平台」的措辞改为「执行节点、由主控直连」；`GLOSSARY.md` 已按本 ADR 更新平台/节点两条。
- 首条验收：在一个节点上派石秀做只读调研，走完 status → tell → checkpoint → cancel/handoff → 结果读取 → 上下文测量；同时验证 `--machine` 对 `agent start` / `agent prompt` / `agent wait` / `pane close` 的转发与远端 `agent_session.value` 路径。`pane wait-output` 的转发能力单独验证，失败不阻塞主路径。
