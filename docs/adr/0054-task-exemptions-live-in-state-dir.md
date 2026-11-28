# 平台任务豁免表移入状态目录，由命令读写

`untracked.yaml` 原先住在仓根、用配置键 `untracked_path` 指路。它其实是运行时决策：用户说「这张单别管了」时产生，不是需要代码评审的仓库配置；放在仓库意味着每加一行都要走一次提交，而且哨兵与 `check:live` 各自解析路径、可能各看一份。现改为框架拥有的位置 `<state_dir>/untracked.yaml`，读写收口到 `superctl untracked list|add|remove`（框架 ADR 0025）；本仓的文件已原样迁入，`untracked_path` 键已删。

## Consequences

- 豁免表随状态目录走：换机器带状态，不进 git 历史，也没有 diff 评审。
- `npm run check:live` 从实例配置的 `state_dir` 读取豁免表（`scripts/check-registry.mjs:readUntrackedFile`）；旧检出里的 `untracked.yaml` 不再被读。
- 本条取代 ADR 0052 把 `untracked.yaml` 列为仓根物料的表述；`untracked_path` 不再是合法配置键。
