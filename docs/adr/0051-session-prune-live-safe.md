# 0051：会话留档只删过期的、在用的永不删（配套「读不到 ≠ 没送到」）

2026-11-27 11:37 一次 `/new` 换脑删掉了常驻巡检者正在写的会话目录：`pruneSessions` 只按目录名排序、`RemoveAll` 掉 keep 之外的最老目录，**不判断目录是否被存活 agent 使用**。omp 继续往已 unlink 的 inode 写，注入确认（`waitPrimeLanded` → `os.ReadFile`）每轮 ENOENT，运行时据此报「巡检失明」；蒋敬的会话目录更早被删，复盘轮同样中招。同日暴露两个连带缺陷：失败路径把在途条目移出后 `done` 事件被丢，`LastDoneAt`/`RunningSince` 停更；`rotate`/`context_alerts` 对会话文件 stat 失败静默 `continue`，上下文轮换对这两人整体失效且无日志。当时 `sessions/` 下 7 个目录 > 默认 keep=5，第 5 次换脑就会删到主控现役目录。

**决策：**

- **活会话不可删**：`pruneSessions` 先取 `hc.AgentList`，每个 agent 的 `agent_session.value` 取 `filepath.Dir` 的 basename 建 `live` 集合，本次刚新建的目录也加入；`AgentList` 出错**整轮不 prune**（fail-safe：宁可多留不可多删）。
- **按天保留为主、数量为辅**：新增 `sessions_keep_days`（默认 7），候选目录按目录名里的 UTC 时间（取不到退回 `ModTime`）判龄，超过 7 天才删；24 小时内的一律跳过（双保险），判不出龄的（两个来源都失败）也不删——「宁可多留不可多删」与整轮跳过同一取向。`sessions_keep`（默认 5）降为辅助上限：只对「已过 24h」的候选生效，防一周内频繁换脑把目录堆无界，不再作主判据；它仍可能删掉「过了 24h、未满保留天数」的最老目录，收敛值是 `max(sessions_keep, 24h 内目录数)`。
- **删除要留痕**：每删一个目录打一行 `slog.Info`（目录名 + 龄）。旧实现成功删除完全无日志，11-27 只能靠三方对齐溯源。
- **读不到 ≠ 没送到（收窄）**：`boot.SubmitPrompt` 预算耗尽后，若 agent 在册、会话路径非空，而对该路径 `ReadSessionFile` 返回 `os.ErrNotExist`（或任意读失败），返回 `Delivery{Session, Unconfirmed: true}, nil` 并 `slog.Info` 区分「会话文件不存在」与「预算内未落盘」。「读得到、没有 sentinel、agent 又不在 working」仍然是输入框卡住，照旧报错；`ErrNoSessionPath`（live 但路径为空）语义不变。
- **未确认与真失败分账**：巡检健康把 `delivery=unconfirmed` 单独记 `Unconfirmed++` 与 `LastError`，不计 `Misses`；`Misses` 只记真没派出去（拒派、pane 不在、blocked、`ErrNoSessionPath`）。`Unconfirmed` 记的是「最近连续未确认」，确认送达的那一轮归零——审视单点会原样转述这个数，终身累计会被读成「现在还没确认的轮次」。
- **失明判据拆成两个事实**：告警文案同时给「最近成功起轮」（`LastRoundAt`）与「最近确认完成」（`LastDoneAt`），两者都停在窗口（3 个 tick）之外才判失明。只看轮次计数会在「派发已提交、会话文件读不到」时误报（11-27）；只看完成时刻会漏掉「派发次次失败」。
- **健康状态只合并自己负责的字段**：`dispatchInspector` 在 tick 开头 load、dispatch 阻塞约 90 秒后写回；改为保存前重新 load，只覆盖 `Misses/BusySkips/LastError/LastRoundAt/Unconfirmed/BlindAlerted`（及 `Agent`），保留期间由 `done` 事件写入的 `LastDoneAt`。实测 17:08:43 曾被回退成 16:49:57。`RunningSince`/`BusySince` 两个写者都动（done 事件会清零），按「盘上与本轮开头读到的不一样就以盘上为准」合并：done 在窗口里清掉的时刻不能被旧值复活，否则「持续忙超过 2 个周期」会按上一轮的起点误报。`BlindAlerted` 必须整取本轮值（唯一写者除此之外只有 `markBlindAlerted`，且在本函数写盘之后才跑）：它的归零只发生在成功起轮这一处，漏掉会让 `markBlindAlerted` 写下的水位永久有效，之后每一串失明都静默。
- **读不到就出声**：`scanUnmeasurableSessions` 对 `SessionPath != ""` 且 `stat` 报 `IsNotExist` 的受控 agent 写 `context-unmeasurable:<agent>` 发现（类别 `unknown`），路径恢复（换脑/重建）、或该 agent 不再受控（job 收口、离场）后按 key 自消——发现是条件的镜像，残留下来会被永久当成仍然有效。判据是「会话目录本身还在不在」而不是 agent 状态：目录还在、只是 omp 还没写出首个 assistant 输出（刚换脑、刚派出）是正常窗口，由 supervision 的 delivery-unconfirmed 兜底；目录本身没了才是 11-27 的形态。按状态判会把「首轮读十几份台账」的长作业误报成目录被清理。测量成功即视为文件读得到，不再重复 stat（远端那是一趟 SSH）；非 `IsNotExist` 的读失败（权限、IO、节点抖动）不判「没了」，留上一轮状态。「未知不得用于自动 handoff」的行为不变。

**Considered Options：**

- **只加 keep 数量、不判 live**：拒。11-27 的根因是「谁在用」不可见，不是阈值太小；只把 5 调大只是把下一次事故推后。
- **按数量保留 + 保护在用的**：拒。会话数由换脑次数决定，忙日与闲日差一个量级；数量作主判据会在忙日删掉当天目录。用户定的是「按天留 7 天 + 在用的永不删」。
- **文件读不到时保持报错**：拒。报错会触发回滚/重派，把正在跑的巡检轮次变成孤儿；用户接受「读不到时改由可见发现兜底」的收窄代价（方案 D 同时落地）。
- **用 inode 探测（`lsof`/写测试）判断目录是否在用**：拒。需要额外特权与平台假设；herdr 已上报 `agent_session`，那是机器真值。
- **`sweep_wiring` 整份 save 前加锁**：拒。跨 goroutine 锁会扩大变更面；读-改-写只需保留一个字段，按字段合并更简单。

**Consequences：**

- `state/sessions/` 的目录数由「换脑次数」变成「约 7 天 × 日换脑次数」，短期不再被砍；磁盘占用上升，但单目录只是 omp 的会话工作副本，量级可接受。
- `sessions_keep` 的语义从「保留轮数」变成「辅助上限」；实例里显式写了 `sessions_keep = 5` 的配置不需要迁移，行为改为天数优先。
- 「会话文件不存在」不再以派活失败的形式出现，改由 `context-unmeasurable:<agent>` 发现与巡检健康面暴露；`unknown` 仍然不参与自动轮换/handoff。
- `dispatchInspector` 的写回窗口从「整份覆盖」变成「字段合并」，`LastDoneAt` 的并发写入不再被吞。

**验证：**

- `cmd/superctl`：`TestPruneSessionsKeepsLiveAgentDir`、`TestPruneSessionsSkipsOnAgentListError`、`TestPruneSessionsKeepsRecentByDays`、`TestPruneSessionsKeepsJustCreatedDir`、`TestExpirySetCriteria`（四条判据逐条 + 24h/天数边界）、`TestDispatchInspectorPreservesLastDoneAt`、`TestPreserveRoundDoesNotResurrectClearedMoments`、`TestInspectorBlindAlertRearmsAfterRecovery`、`TestInspectorMissesIgnoresUnconfirmed`、`TestInspectorAliveRequiresBothFactsStopped`、`TestUnmeasurableSessionBecomesVisibleFinding`、`TestUnmeasurableFindingResolvesWhenAgentLeavesScope`、`TestUnmeasurableFindingResolvesWhenAgentAbsent`、`TestUnmeasurableOnlyWhenArchiveDirIsGone`、`TestUnmeasurableFindingSurvivesTransientEmptyPath`、`TestInspectorUnconfirmedResetsOnConfirmedDelivery`、`TestWithinWindowBoundary`、`TestReviewPromptShowsInspectorFacts`、`TestSessionDirTimeFallsBackToModTime`、`TestPruneSessionsLeavesRegularFilesAlone`、`TestPruneSessionsIgnoresNonPathSessionKinds`。
- `internal/boot`：`TestSubmitPromptUnconfirmedWhenSessionFileMissing`、`TestSubmitPromptStillFailsWhenSessionReadableButTextMissing`（idle/done 两种收尾态都钉住 11-25 输入框卡住）。
- `internal/config`：`SessionKeptDays` 默认 7、`SessionKept` 语义变为辅助上限。
