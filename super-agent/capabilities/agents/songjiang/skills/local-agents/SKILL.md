---
name: local-agents
description: 本机 herdr 能力 agent 的派活方式与动态 agent 并发上限。何时用：要把活派给本机能力 agent，或按需拉起动态作业 agent 时。
---

# 本地派活

本地能力由能力注册表声明（`super-agent/capabilities/agents/<key>/capability.yaml`，ADR 0021）。常驻（role=master|retro）不派活；role=job 的按需拉起。

- **蒋敬（jiangjing，role: retro）**：复盘分析 + 直连记账抽查。`herdr agent prompt jiangjing "<复盘任务>"`；产物写结果目录 md 回路径。
- **石秀（shixiu，role: job）**：本地副手。**平台专家接不到的本机活全派他**——调研、排查、根因与方案、写脚本、改本仓配置/文档/技能/运行时代码、跑构建测试、整理数据与文件：

  ```bash
  superctl dispatch --local --agent shixiu --body "<要干什么 + 背景 + 验收要点>"
  ```

  他的边界：改动留在工作树不自行提交、不动运行时、不派活、不对外发消息、不做产品交付；删文件/装软件/改全局配置/本仓外变更这类需批准的动作会停手，写进结果文件的「需要你决定」，你读出来问用户，批准后另派一次。干完 `superctl cancel --agent shixiu-<时刻>` 收掉。
- **分派判据**：先判是不是平台/产品交付（是 → 朱贵）；其余本机活不分类，一律石秀。**不要因为「看起来只是查一下」就自己上**——调研也是他的活。
- **朱贵（zhugui，role: job）**：平台派活员。平台侧（hifox 等）的派活一律经他——他挑人、**写任务正文**、建单、回任务号；你只把用户原话与附件转过去（`--body` + `--attachment`），不写正文、不传 `--desc-file`。细节见 `platform-dispatch` 技能文档。
- **动态能力 agent（role=job）**：一律经同一入口，别自己拼 `herdr agent start`：

  ```bash
  superctl dispatch --local --agent <能力 key> --body "<任务 + 上下文 + 验收要点>"
  ```

  它会按注册表的配方（kind/args/workspace/并发上限）拉起 agent、把**该 agent 自己的手册**与任务一起注入、把结果路径写进任务、并登记在途清单；结果落盘后你读文件。`--attachment <路径>`（可重复）会单列成注入里的一段，交给承接 agent 原样带上。
- **派活一律用 `superctl dispatch --local`**：确认要与批准都走派活信封，你不在派活前替副手做判断。
- **收掉**：`superctl cancel --agent <名字>`（关 pane、移出在途清单）。干完的活不要留着占并发额度。
- 并发上限来自该能力的 `maxConcurrent`（默认 3）：满了就先收口再派。
