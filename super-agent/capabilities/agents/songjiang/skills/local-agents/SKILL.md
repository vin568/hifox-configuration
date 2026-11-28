---
name: local-agents
description: 本机 herdr 与执行节点上的能力 agent 派活、动态并发上限与节点位置约束。何时用：要把活派给本机能力 agent、按需拉起动态作业 agent，或派到远端执行节点时。
---

# 派活（本机与执行节点）

本地能力由能力注册表声明（`super-agent/capabilities/agents/<key>/capability.yaml`，ADR 0021）。role=job 按需拉起新 pane；role=retro 是常驻，派活注入它已有的会话（同一套登记与回报，cancel 不关它的 pane）。

- **蒋敬（jiangjing，role: retro，常驻）**：复盘分析 + 直连记账抽查。每天 02:00 由运行时定时派发（产物由整体审视转述，不叫醒你）；你也可以临时派专项复盘：

  ```bash
  superctl dispatch --local --agent jiangjing --body "<复盘窗口 + 检查重点>"
  ```

  只投一单进它的常驻会话：不新起 pane、`cancel --agent jiangjing` 只撤单不关会话、`checkpoint`/`handoff` 不适用（上下文由轮换管）。它忙时会拒绝派活；结果路径由框架给出。`done` 事件会自动收口这一单（并发额度随之释放）；要提前撤、或它卡在确认界面时，用 `cancel --agent jiangjing` 撤单（只撤单不关会话）。
- **时迁（shiqian，role: inspector，常驻）**：巡检者。运行时每 10 分钟派一次巡检轮次（三源为空则不派），它只写发现、从不直接找你；有发现时你收到 `[触发器: sweep]`。**不要自己派它**、不要当它是可派活的执行者。
- **石秀（shixiu，role: job，只读）**：本地调研员。**平台专家接不到的只读活都派他**——调研、排查、根因与方案对比、资料搜集、读代码/文档/台账：

  ```bash
  superctl dispatch --local --agent shixiu --playbook investigate --body "<要查什么 + 背景 + 验收要点>"
  ```

  他的边界：**只读，不创建/修改/删除任何文件**（结果与进度文件除外），不跑会写工作树的构建/脚本，不做 git 写操作，不动运行时、不派活、不对外发消息。任务需要改文件时他会停手，写进结果文件的「需要你决定」——**改文件派安道全，不要派石秀**。干完 `superctl cancel --agent shixiu-<时刻>` 收掉。
- **安道全（andaoquan，role: job，读写）**：通用开发作业 agent。**要改文件（任何已声明项目里的配置、文档、技能、脚本、代码）的活都派他**，并用 `--playbook` 指定作业法（`dev-loop` 交付 / `investigate` 只读调研 / `review-diff` 评审）：

  ```bash
  superctl dispatch --local --agent andaoquan --project <项目> --worktree <作业名> --playbook dev-loop \
    --body "<改什么 + 为什么 + 验收 + 范围>"
  ```

  项目按 `--project <key> --worktree <作业名>` 指定：他建自己的工作区（由他自己跑 `direct/worktree add`，路径与分支取自派活注入的项目事实），改完跑对应测试，提交并 push **同名特性分支**（`feature/<作业名>`），工作区保留给你审。**合并由用户做**，规则以注入的项目约定为准。他不 push 共享分支、不碰运行时状态、不读凭证；`maxConcurrent: 3` 按「能力 + 位置」计，某台机器上的额度满了先收口再派。
- **分派判据**：先判是不是平台/产品交付（是 → 朱贵）；只读的本机活 → 石秀；要改文件 → 安道全（带 `--playbook`）。**不要因为「看起来只是查一下」就自己上**——调研也是石秀的活。
- **朱贵（zhugui，role: job）**：平台派活员。平台/产品交付一律经他；你只转原话与附件，不挑人、不写正文。协议与命令见 `[[skill:platform-dispatch]]`。
- **动态能力 agent（role=job）**：一律经同一入口，别自己拼 `herdr agent start`：

  ```bash
  superctl dispatch --local --agent <能力 key> --body "<任务 + 上下文 + 验收要点>"
  ```

  它会按注册表的配方（kind/args/workspace/并发上限）拉起 agent、把**该 agent 自己的手册**与任务一起注入、把结果路径写进任务、并登记在途清单；结果落盘后你读文件。`--attachment <路径>`（可重复）会单列成注入里的一段，交给承接 agent 原样带上。派活结果里的 `- key` 是 `job:<台账短名>`：回用户提到这件活时带上 `--key <那个 key>`（第一次带锚点会懒建话题），台账里也记这一行。
- **执行节点（远端 herdr，ADR 0041）**：节点声明在 `super-agent/capabilities/nodes/<key>.yaml`；agent 的 `targets` 必须包含该节点。派活与收口：

  ```bash
  superctl dispatch --node <节点> --agent <能力 key> --body "<任务 + 上下文 + 验收>"
  superctl dispatch --auto --agent <能力 key> --body "<任务 + 上下文 + 验收>"
  superctl tell --node <节点> --agent <名字> --text "<补充/追问>"
  superctl checkpoint --node <节点> --agent <名字>
  superctl handoff --node <节点> --agent <名字>
  superctl cancel --node <节点> --agent <名字>
  superctl node read --node <节点> --path <远端结果路径>
  ```

  `--auto` 只在 agent `targets` 覆盖的已声明节点里按准入阈值（内存/load/agent 数/磁盘）自动选择；能力声明 `placement: pinned` 时拒绝自动选择（位置本身就是任务语义，必须显式 `--node` / `--local`）。本机若声明为节点，`local` 与它的 key 等价。
  节点没有常驻 master/retro，只建 jobs；状态由运行时用 `agent wait` 盯，done/blocked 会注入事件。节点报告走 outbox 由运行时抽取（≤60s），结果/进度/brief 都在节点上，读用 `node read`。同名 agent 在多个位置（本机或节点）时必须带 `--node`。
  节点上**没有主控机的文件系统**，材料一律由主控随派活推过去：`--attachment` 的附件落在节点 `state_root/attachments/<run>/`，技能与已登记的直连脚本落在 `state_root/jobs/<run>/material/`（ADR 0041 / 0043，机制细节见框架 ADR 0011）；节点不回头取，也没有反向回连通道，不要为此提反向隧道。所以任务要用到主控机上已有的数据（会话历史、台账、结果/进度、serve 日志、report outbox）时不要派节点——那份数据不在那儿，除非把它打包成附件一起推。派节点前先自问「这台机器上有这份数据吗」。
- **派活用 `superctl dispatch --local`（本机）、`--node`（钉死节点）或 `--auto`（自动择节点）**：确认要与批准都走派活信封，你不在派活前替副手做判断。
- **回报通道（ADR 0029）**：作业中的 agent 用 `superctl report --agent <名字> --text` 主动找主控；你收到 `[触发器: agent]` 就按需回 `superctl tell --agent <名字> --text`。回合结束时运行时会发 `[触发器: event] ... done（不代表已交付，可能是在等你回话）；结果：<路径>`，读结果文件判断。**派完就收轮**，不要轮询或 `herdr agent wait`；巡检发现报来「在途无进度」时，用 `tell` 问一句（同一作业每轮最多一次，两次没回音上报用户）。
- **收掉**：`superctl cancel --agent <名字>`（关 pane、移出在途清单）。干完的活不要留着占并发额度。安道全的 worktree **不**自己 remove——留给你审。
- 并发上限来自该能力的 `maxConcurrent`（默认 3），按「能力 + 位置」计：同一能力在另一台机器上并行不占本机额度；某台机器上满了就先收口再派。
