---
name: local-agents
description: 本机 herdr 能力 agent 的派活方式与动态 agent 并发上限。何时用：要把活派给本机能力 agent，或按需拉起动态作业 agent 时。
---

# 本地派活

本地能力由能力注册表声明（`super-agent/capabilities/agents/<key>/capability.yaml`，ADR 0021）。常驻（role=master|retro）不派活；role=job 的按需拉起。

- **蒋敬（jiangjing，role: retro）**：复盘分析 + 直连记账抽查。`herdr agent prompt jiangjing "<复盘任务>"`；产物写结果目录 md 回路径。
- **石秀（shixiu，role: job，只读）**：本地调研员。**平台专家接不到的只读活都派他**——调研、排查、根因与方案对比、资料搜集、读代码/文档/台账：

  ```bash
  superctl dispatch --local --agent shixiu --body "<要查什么 + 背景 + 验收要点>"
  ```

  他的边界：**只读，不创建/修改/删除任何文件**（结果与进度文件除外），不跑会写工作树的构建/脚本，不做 git 写操作，不动运行时、不派活、不对外发消息。任务需要改文件时他会停手，写进结果文件的「需要你决定」——**你也不要去改**：专用编辑 agent 尚未接入，把要改什么如实告诉用户。干完 `superctl cancel --agent shixiu-<时刻>` 收掉。
- **分派判据**：先判是不是平台/产品交付（是 → 朱贵）；其余本机只读活一律石秀；**要改文件的本机活现在没有承接人**，上报用户。**不要因为「看起来只是查一下」就自己上**——调研也是他的活。
- **朱贵（zhugui，role: job）**：平台派活员。平台侧（hifox 等）的派活一律经他——他挑人、**写任务正文**、建单、回任务号；你只把用户原话与附件转过去（`--body` + `--attachment`），不写正文、不传 `--desc-file`。细节见 `platform-dispatch` 技能文档。
- **动态能力 agent（role=job）**：一律经同一入口，别自己拼 `herdr agent start`：

  ```bash
  superctl dispatch --local --agent <能力 key> --body "<任务 + 上下文 + 验收要点>"
  ```

  它会按注册表的配方（kind/args/workspace/并发上限）拉起 agent、把**该 agent 自己的手册**与任务一起注入、把结果路径写进任务、并登记在途清单；结果落盘后你读文件。`--attachment <路径>`（可重复）会单列成注入里的一段，交给承接 agent 原样带上。
- **派活一律用 `superctl dispatch --local`**：确认要与批准都走派活信封，你不在派活前替副手做判断。
- **回报通道（ADR 0029）**：作业中的 agent 用 `superctl report --agent <名字> --text` 主动找主控；你收到 `[触发器: agent]` 就按需回 `superctl tell --agent <名字> --text`。回合结束时运行时会发 `[触发器: event] ... done（不代表已交付，可能是在等你回话）；结果：<路径>`，读结果文件判断。**派完就收轮**，不要轮询或 `herdr agent wait`；巡检时对没消息的作业用 `tell` 问一句（同一作业每轮最多一次，两次没回音上报用户）。
- **收掉**：`superctl cancel --agent <名字>`（关 pane、移出在途清单）。干完的活不要留着占并发额度。
- 并发上限来自该能力的 `maxConcurrent`（默认 3）：满了就先收口再派。
