# Agent key 统一用中文名拼音，已部署实例的身份以重建而非手改 state 迁移

> **部分推翻（2026-11-10，0690473）**：第 8 条否决的「手改 state 迁移键（同 UUID 换 key）」被用户决策推翻并已在 staging 执行——linchong / daizong 以原 UUID 重绑（实例身份与会话历史随改名保留，免去重建 + 归档的切换窗口）；prod 已重绑待 apply。第 13 条「旧 state 条目留存无害」同步失效：x-deploy 9b8363c 起 validate 对 agent/crew/skill 的「失绑 + 无绑定新 key」组合 fail-fast（look-renamed 门禁，改名=搬 key 保 UUID，真增删=删陈旧条目），陈旧 agent/skill 条目必须消歧后 plan 才放行；workflow/automation 仍走 prune（本 ADR 的 workflow key 改建即按此收敛：`xwing-deploy-release-gate` → `x-deploy-release-gate` 另建新实例，旧实例 `prune workflows` 归档）。

`config/hifox` 集合的 agent key 与 workflow key 统一为中文名拼音（`linchong` / `wusong` / `daizong` / `wuyong` / `zhongyitang`）：key 是跨资源引用锚点（workflow `run_agent` 的 `agent:<key>`、Space Skill `assignTo`、Agent Skill 目录与 `{{skills.<key>/…}}` 模板变量、`state.yaml` 身份表），中文名与拼音的一一对应让「显示名 ⇄ key」可互相推导，后续命名方案再变也不动 key。

## Considered Options

- **key 保持英文职能名**（luke / ci-tester / deploy-maintainer / steward）：拒（用户决策）。人名与 key 脱钩后每加一个 agent 都要发明一个稳定英文 key，且现有 luke（人名）与 ci-tester（职能名）本就无体系。
- **手改 `state.yaml` 迁移键**（同 UUID 换 key，原地保留实例）：拒。违反本仓不变量「state.yaml 的 UUID 是已部署资源的唯一权威，勿手改」；为省一次重建去开手改 state 的口子，代价是不变量失效。
- **CLI 支持重命名命令**：xwing-deploy 无此原语；为一次性迁移给工具加命令属投机通用性。

## Consequences

- 换 key = 换身份：apply 按「config 键在 state 无条目 → 新建」对账，linchong / daizong 将以**新 UUID 重建**，旧「卢克 Luke」「CI Tester」实例（两环境各一）成为孤儿，用 `hifox agent archive` 归档清理；旧 state 条目（`luke`、`ci-tester` 及其 Agent Skill 键）留存无害——plan / pull 按 config 迭代，不会读它们。
- 旧实例上的会话历史与绑定留在归档实例上；Space Skill 实例（`architecture-fit-review` 等）key 未变，`agent skill set` 直接把新实例绑上。
- release-gate 的 `agent:daizong` 引用随重建解析到新 UUID；其图变更与 HF-15237 的归档重 import 回路叠加，切换窗口内定时轮可能派给归档旧实例——apply 完成后立即归档旧实例即可收敛。
- 主题闭集三处同步不变量（ADR 0002）不受影响：引用全部走 key，key 体系稳定后命名方案（水浒）只动显示 name。
