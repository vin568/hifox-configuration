# 云端派活

云端能力的口径：本仓 `config/` 已声明的 agent 即全部，未声明即不存在（ADR 0017）。要新云端能力 → 提案 → 用户批准 → 加 config 声明。

清单会演进：派活前 `hifox agent list --output json` 现查（或在能力注册表里核对 key —— 注册表是这些指针的索引），按同层级挑 prod/staging 实例，不要凭记忆硬编码。

命令面：

```bash
# 派（唯一入口：自动建单 + 登记在途清单；绝不追加 task summon）
superctl dispatch --cloud --title "<标题>" --agent <能力 key> --space "<Space>" \
  --body "<需求原话 + 实例 + 排查结论 + 验收要点>"

# 查（真值三件套：status / delegateStatus / agentWorkingStatus）
hifox task view <任务号> --output json
hifox run list --task <任务号>

# 停（撤销一件工作；不急停）
superctl cancel --task <任务号>

# 勘误用的底层命令（正常不用自己拼）
hifox task create --space "<Space>" --title "<标题>" --delegate-agent <agent-id> --body "<正文>"
hifox task cancel-run <任务号> <run-id>
```

登记与跟踪：`superctl dispatch` 会把任务号写进在途清单（`inflight.json`，路径见首条注入）；哨兵高频轮询清单里的云端条目，状态真变化才以事件唤醒主控（ADR 0018）。收口时用 `superctl cancel`（或直接改清单）移出。
