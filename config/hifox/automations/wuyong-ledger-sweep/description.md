# 吴用 · 调度台账巡检（后台对账）

Prompt version: 2026-11-10.1
Agent: {{agents.wuyong.name}}
Space: HiFox 产研（HF）
Schedule: `*/30 * * * *`（Asia/Shanghai）

## Objective

在没有用户对话的情况下，对账台账里**所有「进行中」事项**的真实状态：终态标 `待转述`、异常标注并上报。不改动「等待用户」「计划」的状态，不重派、不取消、不建任务、不归档。

## 执行步骤（只读平台 + 只写台账）

1. 加载并遵守 agent skill `{{skills.wuyong/ledger.name}}`（§2 状态真值与异常处置、§7 写入、§8 并发、§9 容量）。
2. 解析台账：文档集 `Agents`（`hifox docs shared list --space "HiFox 产研"`）→ 实例文件夹 `{{agents.wuyong.name}}` → `进行中`。任一层不存在 → 直接结束（`{"swept":0,"reason":"no-ledger"}`），**不创建任何东西**。
3. 列 `进行中` 并逐项 `hifox docs document view <事项 id> --shared <文档集 id> --output text`；对每项按类型查真值（**Space 取正文 `关联键` 的 `space=<Space 名>`**，跨 Space 事项必须带对应 Space）：
   - workflow：`hifox workflow runs view <workflow-id> <execution-id> --space "<Space>" --output json`
   - task：`hifox task view <任务号> --output json`
4. 处置（与 skill §2 异常表一致）：
   - **跳过刚被前台处理的事项**：正文 `最近核对` 距今 < 5 分钟 → 不动。
   - 写前重读；正文 `最近核对` 或时间线末行与读取时不一致 → 跳过该事项，下一轮再处理。
   - 终态成功 / 取消 / 任务完成 / 发布 → 若状态已是 `待转述（同 verdict）` 且已有结论行，只更新 `最近核对`；否则时间线追加 `结论：<verdict> · <一句话摘要>`，正文状态改 `待转述（<verdict>）`，**留在 `进行中`，不改名、不移动、不归档**；转述由下一次前台对话完成。
   - 任务关闭但语义不明（仅「关闭」、取消 / 拒绝无法区分）→ 标 `等待用户` 并在时间线写「语义不明」，不要猜 verdict。
   - 终态失败 → 若已是 `待转述（失败）` 只更新 `最近核对`；否则时间线追加失败原因，正文状态改 `待转述（失败）`，留在 `进行中`；`下一步：转述后移入 等待用户`；**不自动重试**。
   - 幻影（`runs view` 404 `Execution not found` 且 `logs list --space "<Space>" --filter '{"executionId":"…"}'` 为空）/ 派发结果未知 / 疑似卡住（`pending`>10m、`running`>120m）/ 暂停待输入 / 任务无执行者 → 按 skill 标注 `状态：…`，能判定无执行者的移到 `等待用户`，疑似卡住 / 暂停的保留 `进行中` 并在时间线写诊断，等待下轮或用户决定。
   - `runs view` 其它非零退出（网络 / 5xx）按查询失败处理：重试一次，不判幻影、不标终态。
   - 每项更新 `最近核对：<ISO>（<status>）`。
5. 只统计不移动：`等待用户` 超过 7 天、`计划` 超过 90 天的项，列入摘要提醒（**只随本条摘要附带，不单独触发消息**）。
6. 汇总与通知（**状态迁移 / 新终态 / 异常变化**才发，最多一条；仅 `最近核对` 刷新不算变更，保持零打扰）：
   收件人按顺序解析：`hifox auth status --output json` 的 `email` → `hifox run view $HIFOX_RUN_ID --output json` 的 `triggerHuman.id`（非空且 type 为 human/member）→ 都没有则跳过。

   ```bash
   BODY="$(mktemp /tmp/wuyong-sweep.XXXXXX.md)"   # 并发 run 共享 /tmp，必须独立文件
   # 写入摘要到 "$BODY"
   hifox chat message send --to <收件人> --body-file "$BODY"
   ```
   摘要格式：

   ```text
   巡检 <HH:MM>：核对 N 项，待转述 a 项，异常 b 项
   - [成功] D-… · <标题>：<一句话结论>
   - [异常] D-… · <标题>：<原因>（最近核对 HH:MM）
   - [提醒] 等待用户 X 项 >7 天；计划 Y 项 >90 天
   ```

   无变更且无异常 → **不发消息（零打扰）**。
   `email` / `triggerHuman.id` 都为空或私聊发送失败 → `notified=false`，不重试：异常已写入台账与最终输出，下一次巡检或用户对话会再次暴露。
7. 最终输出单行 JSON：`{"swept":N,"pendingRelay":N,"anomalies":N,"notified":true|false}`。

## Hard Safety Boundaries

- 只处理 `进行中`；`等待用户` / `计划` 只统计，绝不移动。**巡检不归档案**（终态只标 `待转述`，转述与归档由前台对话完成）。
- 不重派、不取消、不建任务、不改配置、不碰其它 Space。
- 未完结事项 >20 时只处理 `进行中`，摘要里报积压数量。
- 不打印 / 不写入任何 token、凭据、环境变量值、机器标识（Computer / Lease / Runtime ID、主机名、IP、路径）；上游报错先脱敏。
- 不删除任何文档或文件夹（删除会级联）。
- 平台查询失败先重试一次；查询失败 ≠ 对象不存在，不得据此判定终态。
