# ADR 0023：平台派活经本地派活员，平台指针目录与 `config/` 强制同步

ADR 0017 定了「宋江直连 hifox 派活、不经吴用转述」，理由是少一跳、少一次失真。运行一段时间后暴露出这条路径的两个实际代价：

- **宋江要持一份平台能力清单才能派活**，而清单是 `config/` 声明的副本，不是任何一处真值：既不是平台真值（`hifox agent list` 里 rayfox 组织 452 个 agent、`canManage` 250 个），也不是部署真值（`environments/*/state.yaml`）。两张真值任一处偏离，清单就是错的。
- **「谁适合这个活」进了主控的上下文**：主控是长会话的编排者（ADR 0022 上下文卫生），路由知识该常驻在一个专职工身上，而不是编排者脑子里。

同时定一条词汇纪律：**「云端」不再是「hifox」的同义词**。派活目标通称**平台**（platform）——今天只有 hifox，接着会有 sharkly，还会有远程 ssh 主机上的 agent；用一个词把 hifox 说成「云端」，等第二、第三个执行面进来就会立刻说不清。平台目录按平台名分目录，命令按 `--platform <名>` 走。

**决策：**

- **平台派活员是本地能力 agent，不是平台上的 agent**：朱贵（`zhugui`，`super-agent/capabilities/agents/zhugui/`，`role: job`、`workspace: jobs`）。主控经 `superctl dispatch --local --agent zhugui` 把请求派给他，他挑人、写单、建单，把任务号写进结果文件。**不在平台上新增派活 agent**：平台的 roster/权限面不该为编排者的便利多养一个身份（人要看的是「谁在干活」，多一个只派活的身份只会污染任务板与权限面），而本地 agent 天生拿得到 `superctl`（派活面收敛在运行时一处，不散落到平台提示词里）。
- **主控不挑人**：主控只写「用户原话 + 层级（prod/staging）+ 类型 + 备注」，朱贵持有主题 → 承接人的路由表并据此选人。主控不解析平台 ref、不拼 Space、不判实例。（**信封字段按 ADR 0024 修订**：验收要点与任务正文都由朱贵整理产出，主控只转发原话 + 判类型——多写一层就等于多占一次受理时延。）
- **平台指针目录按平台分目**：`super-agent/capabilities/platforms/<平台>/<key>.yaml`（`<平台>` = 平台 CLI 应用名，取自 `environments/*/environment.yaml` 的 `cli.app`）。条目只放主控与派活员需要的人读信息（key/name/space/org/note），**真源仍是 `config/`**。
- **派活入口**：`superctl dispatch --platform <平台> --agent <能力 key>`（原 `--cloud` 改名）。它把注册表 key 解析成唯一平台 ref（歧义要 `--instance prod|staging` 消歧）、建任务、登记在途清单、建台账骨架——派活员直接用这一条，不自己拼平台命令。**登记的活必须走这条路**：哨兵只盯在途清单。
- **同步规矩（机器强制）**：`config/<集合>/agents/<key>` 与 `capabilities/platforms/<平台>/<key>.yaml` 双向一一对应——config 里加 / 删 / 改 agent 必须同步平台目录，否则 `superctl capabilities --check` 报漂移并非零退出。同一检查还要求：指针的 `name` 与 config 声明名指同一身份（相等，或按词边界为声明名前缀——apifox 集合的 config 名带英文后缀 `巴德尔 Baldur`，指针写基名 `巴德尔`，两者是同一个身份）；每个条目在任一 `environments/*/state.yaml` 有部署记录；条目的 `org` 必须是平台组织**完整 UUID**。
- **线上校验 `superctl capabilities --check --live`**：按 `environments/*/state.yaml` 的 id 逐个对平台核（存在性、名字与 `deployedName`、`spaceIds` 含环境 spaceId、未归档、`allowTaskAssignment`），并核对已部署实例的线上名字与平台目录声明的名字仍是同一身份。**不断言「条目名在平台上唯一可解析」**——同层级双实例（prod/staging）本来就命中多个，`--instance` 消歧是常规路径，不是漂移。离线快、线上全，派活前的门禁默认走离线。

**术语沿革**：ADR 0018–0022 里写作「云端哨兵 / 云端条目 / `cloud_poll_interval` / `cloudwatch`」的部件，本 ADR 后统一改名为「平台哨兵 / platform 条目 / `platform_poll_interval` / `platformwatch`」（旧文件里 `kind: cloud` 的在途条目读入时按 platform+hifox 归一）。历史 ADR 保留原文，以本条为准。

**与既有 ADR 的关系**：本 ADR 取代 ADR 0021 里的两处——平台指针目录 `cloud/` 改为 `capabilities/platforms/<平台>/`，派发入口 `--cloud` 改为 `--platform <平台>`；ADR 0021 的「目录即声明」「派发统一入口」「指针只写真源在 config」三条原则不变。ADR 0017 的「宋江不经吴用人机入口转述」不变，变的是「宋江自己挑人」这一层。

**Considered Options：**

- **在平台上新增一个派活 agent**（本 ADR 起草时的第一版）：拒。平台 roster 与权限面不该为编排者便利多一个只派活的身份；派活员还得依赖平台的「被委派即开工」触发语义，而本地 agent 直接调 `superctl` 就把解析、建单、登记一次做完，少一层回执（原先要靠任务评论回传任务号）。
- **宋江按能力目录自己挑人派活（原状）**：拒。主控持 config 副本 + 路由知识进主控上下文；两者都是 ADR 0022 要收敛的东西。
- **派活全交吴用**（0017 已拒的原案）：再拒。吴用是人对平台的对话入口，对外口径要求脱敏（不报任务号、不报承接人）；机器要的恰是编号与可撤销。且它的活是主题判定与转述，不是机器派活。
- **不写目录、派活前现查 `hifox agent list`**（ADR 0021 已拒一次）：再拒。现查只有平台字段，丢掉「谁适合什么」的人类知识；250 条可管理 agent 的活列表也进不了主控上下文。
- **只用线上校验**：拒。派活前的快速门禁不该要求网络；线上的真值核对是加固，不是替代。
- **把路由表做成平台侧字段**（用 jobTitle / description 自动匹配）：暂拒。平台字段是为「展示」写的，不是为「路由」写的；路由表的评审面应与 config 声明同仓同 PR。

**Consequences：**

- **多一跳**：多一个本地 agent 起停与一次路由决策（本地开销，无网络往返），换来主控不再持路由知识、清单与 config 强制同源。
- **派活员自己是本地活**：它的起停、并发与结果文件走既有本地派活机制（`dispatch --local` / `cancel --agent`）；本期还给 `dispatch --local` 补了「把该 agent 自己的手册一并注入」——此前 role=job 的 agent 拿不到任何引导，只有任务正文。
- **派活面收敛在 `superctl dispatch --platform`**：平台 ref 解析、space 默认值、在途登记、台账骨架都只此一处实现，派活员与主控走同一条路；**谁建的活都自动进哨兵的视野**（不需要额外的登记命令）。
- **平台目录与 `config/` 的双写由 `--check` 强制**（ADR 0021 承认的双写缺口在这里补齐）；`--live` 是可选真值核对，离线仍可独立跑。
- **加一个平台能力只改两处**：`config/` 的声明 + `capabilities/platforms/<平台>/<key>.yaml` 指针（`--check` 拦）。派活员不另持清单——它按索引里的**用途**（`note`）挑人，所以「谁适合什么」只有 `config/` 与指针目录这两个同源面；note 写得含糊就是路由事故，评审要盯这一格。
- **层级落在 `--instance` 上，不是只写在正文里**：`prod` = 远端名不带后缀的那一份，`staging` = 带实例后缀的那一份（本仓 `nameSuffix: ""` 就是 prod 的由来）。派活员把层级转成 `--instance` 传下去，结果文件里写的是**实际建到**的那一层；只写正文不传参会出现「正文 prod、单建到 staging」的静默错派（评审 F-1）。
- **升级动作（本 ADR 定的改名带来的）**：`platform-snapshot.json` 取代 `cloud-snapshot.json`——新文件不存在时先拿旧文件当基线，否则升级后首轮会把所有在途任务重打基线、窗口内的状态变化不注入（10 分钟巡检会兜住，但会多一轮「用户等不到回音」）。旧文件保留不删。在途清单的旧 `kind: cloud` 条目读入时按 platform+hifox 归一（写盘即新形状）。
- **尚未接入的执行面要如实说**：sharkly 与远程 ssh 主机上的 agent 目前没有派活实现，`--platform` 只认 hifox；派活员对这类请求返回「未建单 + 平台未接入」，主控上报用户，不许自己拼命令绕过去。
- **顺带修掉的两个既有缺陷**（这次对账第一次照出来的）：
  - 平台指针里的 `org: rayfox` 是 slug，而 `HIFOX_ORGANIZATION_ID` 只认完整 UUID（`404 ORGANIZATION_NOT_FOUND`，2026-11 实测）——即 `dispatch` 的 ref 解析对全部 15 条都是必然 404。已统一改成组织 UUID，并加进离线检查（写 slug 即报漂移）。
  - 线上真值与部署记录已有两处偏离：`environments/current`（apifox prod）的 `yanqing` 平台名是「尤弥尔 Ymir」、`environments/hifox-prod` 的 `linchong` 平台名是「卢克 Luke」，都不是声明名——`--check --live` 的真实产出，处置（回滚重命名或 re-capture）归用户裁决。
