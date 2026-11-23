---
name: local-agents
description: 本机 herdr 与执行节点上的能力 agent 派活、动态并发上限与节点位置约束。何时用：要把活派给本机能力 agent、按需拉起动态作业 agent，或派到远端执行节点时。
---

# 派活（本机与执行节点）

本地能力由能力注册表声明（`super-agent/capabilities/agents/<key>/capability.yaml`，ADR 0021）。role=job 按需拉起新 pane；role=retro 是常驻，派活注入它已有的会话（同一套登记与回报，cancel 不关它的 pane）。

- **蒋敬（jiangjing，role: retro，常驻）**：复盘分析 + 直连记账抽查。派活与 job 同一个入口：

  ```bash
  superctl dispatch --local --agent jiangjing --body "<复盘窗口 + 检查重点>"
  ```

  只投一单进它的常驻会话：不新起 pane、`cancel --agent jiangjing` 只撤单不关会话、`checkpoint`/`handoff` 不适用（上下文由轮换管）。它忙时会拒绝派活；结果路径由框架给出。`done` 事件会自动收口这一单（并发额度随之释放）；要提前撤、或它卡在确认界面时，用 `cancel --agent jiangjing` 撤单（只撤单不关会话）。
- **石秀（shixiu，role: job，只读）**：本地调研员。**平台专家接不到的只读活都派他**——调研、排查、根因与方案对比、资料搜集、读代码/文档/台账：

  ```bash
  superctl dispatch --local --agent shixiu --body "<要查什么 + 背景 + 验收要点>"
  ```

  他的边界：**只读，不创建/修改/删除任何文件**（结果与进度文件除外），不跑会写工作树的构建/脚本，不做 git 写操作，不动运行时、不派活、不对外发消息。任务需要改文件时他会停手，写进结果文件的「需要你决定」——**改文件派安道全，不要派石秀**。干完 `superctl cancel --agent shixiu-<时刻>` 收掉。
- **安道全（andaoquan，role: job，读写）**：本机维护员。**要改本仓文件（配置、文档、技能、脚本、框架子模块代码）的活都派他**：

  ```bash
  superctl dispatch --local --agent andaoquan --body "<改什么 + 为什么 + 验收 + 范围>"
  ```

  他每次作业建自己的 worktree（`super-agent/direct/worktree add <作业名>`），改完跑对应测试，提交并 push **同名特性分支**（`feature/<作业名>`），worktree 保留给你审。**合并由用户做**：先子模块特性分支 → `dev`，再把根仓分支的 gitlink 指到 `dev` 上的 commit，最后根仓分支 → `main`。他不 push `main`/`dev`、不碰运行时状态、不读凭证；`maxConcurrent: 2`，满了先收口再派。
- **分派判据**：先判是不是平台/产品交付（是 → 朱贵）；只读的本机活 → 石秀；要改本仓文件 → 安道全。**不要因为「看起来只是查一下」就自己上**——调研也是石秀的活。
- **朱贵（zhugui，role: job）**：平台派活员。平台/产品交付一律经他；你只转原话与附件，不挑人、不写正文。协议与命令见 `[[skill:platform-dispatch]]`。
- **动态能力 agent（role=job）**：一律经同一入口，别自己拼 `herdr agent start`：

  ```bash
  superctl dispatch --local --agent <能力 key> --body "<任务 + 上下文 + 验收要点>"
  ```

  它会按注册表的配方（kind/args/workspace/并发上限）拉起 agent、把**该 agent 自己的手册**与任务一起注入、把结果路径写进任务、并登记在途清单；结果落盘后你读文件。`--attachment <路径>`（可重复）会单列成注入里的一段，交给承接 agent 原样带上。
- **执行节点（远端 herdr，ADR 0041）**：节点声明在 `super-agent/capabilities/nodes/<key>.yaml`；agent 的 `targets` 必须包含该节点。派活与收口：

  ```bash
  superctl dispatch --node <节点> --agent <能力 key> --body "<任务 + 上下文 + 验收>"
  superctl tell --node <节点> --agent <名字> --text "<补充/追问>"
  superctl checkpoint --node <节点> --agent <名字>
  superctl handoff --node <节点> --agent <名字>
  superctl cancel --node <节点> --agent <名字>
  superctl node read --node <节点> --path <远端结果路径>
  ```

  节点没有常驻 master/retro，只建 jobs；状态由运行时用 `agent wait` 盯，done/blocked 会注入事件。节点报告走 outbox 由运行时抽取（≤60s），结果/进度/brief 都在节点上，读用 `node read`。同名 agent 在多个位置（本机或节点）时必须带 `--node`。
- **派活一律用 `superctl dispatch --local`（本机）或 `--node`（执行节点）**：确认要与批准都走派活信封，你不在派活前替副手做判断。
- **回报通道（ADR 0029）**：作业中的 agent 用 `superctl report --agent <名字> --text` 主动找主控；你收到 `[触发器: agent]` 就按需回 `superctl tell --agent <名字> --text`。回合结束时运行时会发 `[触发器: event] ... done（不代表已交付，可能是在等你回话）；结果：<路径>`，读结果文件判断。**派完就收轮**，不要轮询或 `herdr agent wait`；巡检时对没消息的作业用 `tell` 问一句（同一作业每轮最多一次，两次没回音上报用户）。
- **收掉**：`superctl cancel --agent <名字>`（关 pane、移出在途清单）。干完的活不要留着占并发额度。安道全的 worktree **不**自己 remove——留给你审。
- 并发上限来自该能力的 `maxConcurrent`（默认 3）：满了就先收口再派。
