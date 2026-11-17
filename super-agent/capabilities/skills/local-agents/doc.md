# 本地派活

本地能力由能力注册表声明（`super-agent/capabilities/agents/<key>/capability.yaml`，ADR 0021）。常驻（role=master|retro）不派活；role=job 的按需拉起。

- **蒋敬（jiangjing，role=retro）**：复盘分析 + 直连记账抽查。`herdr agent prompt jiangjing "<复盘任务>"`；产物写结果目录 md 回路径。
- **动态能力 agent（role=job）**：一律经同一入口，别自己拼 `herdr agent start`：

  ```bash
  superctl dispatch --local --agent <能力 key> --body "<任务 + 上下文 + 验收要点>"
  ```

  它会按注册表的配方（kind/args/workspace/并发上限）拉起 agent、把任务注入进去、把结果路径写进任务、并登记在途清单；结果落盘后你读文件。
- **收掉**：`superctl cancel --agent <名字>`（关 pane、移出在途清单）。干完的活不要留着占并发额度。
- 并发上限来自该能力的 `maxConcurrent`（默认 3）：满了就先收口再派。
