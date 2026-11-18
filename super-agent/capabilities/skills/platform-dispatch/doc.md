# 平台派活

远端派活不自己挑人：**一律交平台派活员朱贵（能力 key `zhugui`，本地 job）**。他读平台能力索引（key / 名 / space / 用途）挑承接人、把层级转成 `--instance prod|staging`、经 `superctl dispatch --platform` 建单，再把任务号写进结果文件。你的活是：把需求写成「原话 + 层级 + 验收」、读结果、盯进度、按用户裁决撤销。

**平台 ≠ hifox**：platform 指一个远端执行面——今天是 hifox（将来 sharkly、远程 ssh 主机上的 agent）。命令与目录都按 `--platform <名>` / `capabilities/platforms/<平台>/` 命名，别把 hifox 当同义词。

口径：本仓 `config/` 已声明的 agent 即全部平台能力，未声明即不存在（ADR 0017）。要新平台能力 → 提案 → 用户批准 → 加 config 声明；**声明了必须同步到 `super-agent/capabilities/platforms/<平台>/`**（ADR 0023，`superctl capabilities --check` 强制，`--check --live` 再对平台核一遍）。能力索引是你的「知道有谁」，不是派活路径——挑人是朱贵的职责。

命令面：

```bash
# 派（唯一入口：起本地派活员 → 他建单并登记在途清单）
superctl dispatch --local --agent zhugui \
  --body "需求原话：<原话>；层级：prod|staging|未指定；验收要点：<怎么算干完>；备注：<结论/已排除方向>"

# 读结果（结果文件里是 - 任务号：HF-xxxxx / - 承接：<key> / - 层级）
# 路径由 dispatch 的 JSON 返回（result 字段）

# 查（真值三件套：status / delegateStatus / agentWorkingStatus）
hifox task view <任务号> --output json
hifox run list --task <任务号>

# 停（撤销一件工作；不急停）
superctl cancel --task <任务号>
```

结果文件里 `结果：未建单` 时看 `原因`：缺料就补料再派一次，平台未接入就上报用户。

层级语义：`prod` = 远端名不带后缀的那一份，`staging` = 带实例后缀的那一份；层级由你写在正文里，朱贵负责把它落到 `--instance`，结果文件里的 `层级` 是他**实际建到**的那一层。

登记与跟踪：任务号由派活员经 `superctl dispatch --platform` 建单时**自动**写进在途清单；哨兵高频轮询清单里的平台条目，状态真变化才唤醒你（ADR 0018）。收口时用 `superctl cancel`（或直接改清单）移出。

**派活员自己也是本地的活**：他起来、跑完、写结果——你读文件，别让他常驻（干完 `superctl cancel --agent zhugui-<时刻>`，或让他自己退出）。
