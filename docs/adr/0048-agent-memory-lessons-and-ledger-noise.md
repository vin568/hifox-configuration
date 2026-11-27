# 0048：一份经验文件、发现留痕、台账检查行原位更新

记忆这件事在本仓的实际缺口不是「没有存储」，是**记号所在的几处地方都在自我淹没**。三条实测：

- 活跃区 14 份台账共 2776 行，其中 **2352 行是巡检每轮追加的 `- 最近核对：`**（85%），有效内容只有 424 行；单份最多 201 行全是检查行。
- `state/results/` 265 份文件里 201 份是巡检每轮的日志（808 KB），复盘的输入面被自己的核对记录占满。
- `findings.Resolve` 直接 `os.Remove`，所以「同一作业反复失败」「同类坑周期性复发」在数据上不可见——复盘只能靠人工比对台账。

对照 Anthropic《The components of agent memory》的分层（transcript → notes → representations → injected），我们这边的映射是：**transcript 已有但无消费者，notes 完全没有，representations 不需要，injection 只有出生点**。所以本决策只做「让笔记这一层存在且保持很小」，不建目录、不建检索、不动注入拓扑。

**决策：**

- **一份经验文件** `state/lessons.md`，三段作用域 `全局` / `仓库/<名>` / `agent/<key>`，一行一条：`- [已验证|待确认] (YYYY-MM-DD) 规则 — 证据：…`。硬上限 **100 行**，写入侧超限必须合并或晋升（`npm run check` 会红）；读侧另有单次注入上限 **60 行**。
- **唯一维护者是复盘者（蒋敬）**，唯一写入时机是它的日度复盘。作业 agent（朱贵、石秀、安道全）只在自己的结果文件里写「经验」节（0–3 条，可空），一手证据由此进入系统；主控不写经验。
- **不建 per-agent 目录**：一文件一索引的上限才有意义，按 agent 拆开就会各自膨胀、没人合并，最后变成第二个 `reviews/`——写的人很认真，用的人不存在。
- **注入只发生在 job 的 brief**：`localPrompt` 在角色契约与手册之间插入 `[经验]` 段（全局 + 本作业的项目 + 本 agent，已验证优先）。**文件缺失或没有匹配项时一个字都不加**——老实例的 prompt 逐字节维持原样。节点作业的 prompt 在主节点拼好再送过去，经验文件不需要随物料推送。
- **主控 prime 不注入经验**：主控几乎不换脑（现场 `sessions/` 全是 `-start` / `-new`，没有 `-rotate`），注入等于写进一个不会被读的地方。
- **发现消解留痕**：`Resolve` 删除前追加一行到 `state/findings-journal.jsonl`（`{id,key,kind,target,summary,since,updated,state,resolved_at}`）。流水只追加、运行逻辑不读，只给复盘做模式分析；写流水失败不回滚删除（删除是对的语义，留痕是旁路）。
- **台账检查行原位更新**：新增 `superctl ledger check --path <台账> [--note <附注>]` 重写那一行而不是追加；`superctl ledger compact --all` 把历史的重复检查行折叠成最新一条，被折叠的行写进 `state/ledger-checks.log`。巡检手册从「手工更新」改为调用命令——机器保证形态，不靠自觉。
- **晋升分两类**：行为类（怎么写、怎么问、怎么回）必须用户拍板；机器类（某个命令的坑、某个路径不存在）由复盘者给出可直接落地的判据、安道全执行。复盘者自己不改手册、不改脚本。

**Considered Options：**

- **per-agent 记忆目录（`state/memory/<agent>/`）**：拒。没有消费者；文章里「10 个文件 43 KB 胜过 413 个文件 978 KB」说的是同一件事——存储结构越细，合并越少发生。
- **转录归档 + 全文检索**：拒。55 MB / 5 轮，无人读；复盘不读转录，只读台账 + 结果 + 日志。做了要先解决跨 harness 格式，收益不成立。
- **把经验注进主控 prime**：拒。主控极少出生，注入的收益接近零；且 prime 已经 16 KB 无上限，再加一层只会稀释手册。
- **新增 dreaming 调度任务**：拒。蒋敬 02:00 的复盘已经在做「回头看产物」，缺的是输入过滤与产出格式，不是第五个时钟。
- **`Resolve` 改为移入归档目录而不是删除**：拒。发现面必须只回答「现在有什么要处理」；留成文件会让去重面变脏、投递面被过期状态占住（ADR 0014 的教训）。旁路流水满足「可分析」而不污染语义。
- **`ledger check` 只改手册、不改代码**：拒。手册原本就写的是「更新成本轮时间」，实际却在追加——形态不能靠自觉，必须由命令保证。

**Consequences：**

- 复盘的输入面变小：折叠后活跃区预计从 2776 行降到 ~450 行；`ledger compact` 是显式动作，不自动跑（改写历史台账要人决定）。
- 派活的 prompt 会长一段（≤60 行 ≈ 3 KB），只对 job 生效；没有经验文件时零影响。
- 经验文件是单写者（复盘者）+ 多读者（所有 job）：写坏一次会影响所有后续作业，所以格式校验放在 `npm run check`，不放在读侧静默吞掉。
- 发现流水无界增长，`npm run check` 只做容量提示（超 2 MiB 在 stderr 提示归档），不自动删。
- 晋升仍需人拍板，所以「经验 → 规则」的闭环速度取决于用户回话频率；这是刻意保留的闸门（ADR 0013 的「自动改行为风险最高」）。

**验证：**

- `internal/lessons`：缺文件不报错且渲染为空、按作用域选、已验证优先、坏行忽略、超上限报错。
- `cmd/superctl`：`TestLocalPromptInjectsLessons`（顺序为契约 → 经验 → 手册，且缺文件时 prompt 逐字节等于旧行为）、`TestLedgerCheckUpdatesInPlace`、`TestLedgerCheckInsertsAfterStatus`、`TestLedgerCompactFoldsOnlyCheckLines`、`TestResolveLedgerPathRejectsOutside`。
- `internal/findings`：`TestResolveAppendsJournal`、`TestResolveMissingKeyWritesNoJournal`、`TestResolvePrefixJournalsEveryEntry`。
- 实例：`npm run check` 校验经验文件的段闭集、行格式与 100 行上限；`npm test` 全绿。
