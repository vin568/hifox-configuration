# xwing-deploy 的发布门禁钉在本仓，可发布增量用 patch 等价判定，真分叉自动 backmerge

`vendor/xwing-deploy` 的 dev → main 合并（即发布）由 `config/hifox` 集合内的定时 workflow `xwing-deploy-release-gate`（配 `agents/daizong`）把门：门禁套件（验收钉子）钉在本仓而非被测仓，因为写改动的人与写测试的人同体时 oracle 不独立——契约演进必须在本仓显式更新，这本身就是要的评审点。`config/hifox` 被 hifox-prod / hifox-staging 双环境部署，workflow 因此存在两份实例、两份 schedule；不在配置上分环境（刻意不分出 `*-ci` 集合，与 linchong 同仓），由 check-update 块的运行标记（noclobber 原子创建 + 带 TTL 的 running 标记，release-merge 退出时经 trap 清除）把并发轮退化为 skip。触发判定刻意无状态：右集（`git rev-list --right-only --cherry-pick --no-merges origin/main...origin/dev`）非空即有可发布内容；`--no-merges` 是必须的，否则每次 `--no-ff` 发布后留在 main 上的 merge commit 会被误判成分叉，`--cherry-pick` 让 dev 被 rebase/amend 后与 main 上的旧 SHA patch 等价对消、不误报。左集非空（真分叉：直推 main、main 上 revert、内容级改写）不停车报警，而是自动把 main backmerge 进 dev 并推送（通知常驻报告任务），让验收永远测「main 的超集」；backmerge 冲突才 halt 交人。发布只合并被测 commit：merge 前校验 `origin/dev == candidate && origin/main == 分类时 main`，任一移动即 superseded 留给下一轮。

## Considered Options

- 门禁跑 xwing-deploy 自带 e2e：被拒。e2e 是作者套件（技术上是黑盒，但 oracle 随被测代码同源演化），CI 只增量得到「新环境 × 新 xwing rev × 必然执行」三点；作为发布门禁不成立。
- Automation（cron 直接派发 agent run）而非 workflow：能力上更省（无人值守 YOLO、无图迭代摩擦），被弃是因为 merge 判定会落进 LLM 会话内，且本仓要顺带实践 xwing-deploy 的 workflow 纳管能力（config 聚合只有 workflow 一种调度形态）。
- 分叉一律 halt 交人工：被弃——dev 被 rebase 的良性场景会被误伤停车，且 hotfix 上 main 后流水线失去自愈能力。

## Consequences

- run_agent 的 executor 写配置键 `agent:daizong`：xwing-deploy 在 import/推送前解析为各环境 state.yaml 的实例 UUID（子模块 ADR 0016），跨环境稳定、一次 apply 即可与 agent 同仓首发；远端已部署的实例（存 UUID）与声明侧（存键）对比为 in sync。
- 手写图的 condition 行 id 必须不带块 id 前缀：带前缀的行 id 会被引擎 import 时重映射为新 uuid，而 xwing-deploy 的块 id 翻译映射不回「块 id 派生串」，形成恒假漂移 → 推送触发 HF-15237 → apply 中止在 deploy 之前。无前缀行 id 原样幸存、首轮 apply 即 in sync（2026-11-08 真实环境双向实证）。

- 契约合法演进时，本仓验收套件先红，需要在本仓显式更新场景（有意识的契约变更，不是回归）。
- backmerge 会以 `xwing-ci` 身份向 dev 推送合并 commit；dev 历史出现自动化合并节点是设计内行为。
- workflow 图迭代受 HF-15237 约束：改图走「归档旧实例 + 重新 import」（HF-15237 修复后增量推送恢复）；含 function 块且定向 personal 可见性的 computer 时，函数代码改动需 owner 重新授权（把 runner 的 computer 可见性翻成 organization/space 可豁免）。
