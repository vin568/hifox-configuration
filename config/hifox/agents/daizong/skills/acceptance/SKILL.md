# x-deploy Acceptance Suite

本仓（消费方）持有的 x-deploy 黑盒验收套件。断言只基于消费方可观察行为：命令退出码、stdout 契约文案、state.yaml 内容、经任务 CLI 读回的远端资源状态、终端用户在 app UI 里可见的文本。不 import 被测代码，不复用其测试助手。

## 运行前提（环境变量）

| 变量 | 含义 |
| --- | --- |
| `HIFOX_SERVER_URL` | task-local 后端地址（如 `http://localhost:20000`） |
| `HIFOX_TOKEN` | task-local session JWT（`auth.env` 的 `TASK_ENV_API_TOKEN`） |
| `HIFOX_ORGANIZATION_ID` | task-local 组织 ID（任务 CLI `organization list` 取；manifest 不含组织 ID） |
| `HIFOX_CLI_BIN` | 任务 CLI 绝对路径（编号身份）；缺省取 PATH 上的 `hifox` |

另需 `node ≥ 18`、`yq`（doctor 场景）。
任务 CLI 按产品品牌派生连接变量前缀（`PRODUCT=hifox` 起的服务读 `HIFOX_*`；缺省 sharkly 品牌不读），`APP_*` 是任何品牌都认的兼容前缀。套件内部会把 `HIFOX_SERVER_URL` / `HIFOX_TOKEN` / `HIFOX_ORGANIZATION_ID` 同值透传为 `APP_SERVER_URL` / `APP_TOKEN` / `APP_ORGANIZATION_ID`（调用方已显式设置时不覆盖），调用方无需自行导出。
fixture agents 声明运行目标（`target.computer` + `target.runtimeProvider`）：套件运行时经任务 CLI `agent target list` 解析 task-local 唯一 active computer lease（web 启动默认由 task daemon 自动注册）并统一注入所有建 agent 的场景；零个或多个 active lease → fail fast，按环境问题归因。

## 用法

```bash
# 全量验收（--keep 推迟远端清理，供截图/UI 验证；report.json 含清理所需 ID）
node scripts/run-acceptance.mjs --candidate <sha> --repo <x-deploy-worktree> --keep --out <dir>

# UI 验证 + 取证（消费套件产出的 <dir>/journey.json；随后照常清理远端资源）
node scripts/screenshot.mjs --url <frontend-origin> --out <截图目录> --verify <dir>/journey.json

# 截图完成后按报告清理远端资源
node scripts/run-acceptance.mjs --cleanup-report <dir>/report.json
```

退出码 `0` = 全部场景通过且远端清理成功；非 `0` = 存在失败、中断或清理问题（report.json 的逐场景结果、覆盖矩阵与 `cleanupProblems` 可见）。
`screenshot.mjs --verify` 退出码：`0` = UI 断言全过；`1` = UI 断言失败（门禁结果，归因 UI 层）；`2` = 浏览器基建缺失（`ui-verdict.json` 记 `verdict: skipped`，轮次降级、不翻 verdict 但必须标注）。

## 验收分层

套件按**链**编排：链内按序执行、首败即中止本链（后续场景依赖前置状态）；**链间相互独立**，一条链失败不遮蔽其余链——每轮成本一整套环境，必须产出完整故障面。执行顺序：`offline → main → workflow → journey → smoke`。

- **A 契约钉子（offline / main / workflow 链）**：手写的语义契约断言，含阴性对照（见下节编目）。这是套件的核心价值，**写死即契约**。
- **B 用户旅程（journey 链）**：模拟真实消费者的第一公里——`init` 脚手架 → 手写一份真实形状的配置（双 agent：显式 target + 环境级默认 target、avatar、仓库、skill、crew 角色、automation）→ `bind` 按名采纳 org/space → `validate`/`plan`/`create` → 用户式二次编辑（改名 + 加 automation）经 `apply` 收敛 → `backup`。同时产出 `journey.json`（UI 验证规格）。
- **C 派生冒烟 + 覆盖矩阵（smoke 链）**：命令面**运行时**解析自 candidate 自己的 `--help`（其注册表单源渲染，ADR 0013），本仓不维护命令清单。按 usage 形状分三层（无命令名知识，唯一例外是 direct 变更者拒绝表，见 `scripts/lib/surface.mjs`）：`bare`（空参调用必须干净失败或出信息，无堆栈、无副作用）、`preview`（带 `[--apply]` 门禁的命令不带 `--apply` 跑，state.yaml 必须字节不变）、`read`（只读命令对 fixture 跑必须 exit 0）。report.json 的 `coverage` 矩阵按**实际调用日志**度量每个命令被哪些钉子/旅程场景覆盖、冒烟到哪层、是否零覆盖——覆盖是量出来的，不是宣称的。
- **D UI 验证（`screenshot.mjs --verify`）**：按 `journey.json` 断言终端用户在 app 里**看得见**的东西（agents 列表出现带后缀的新 agent、详情页 instructions/skill/仓库渲染、crew 成员与角色），纯文本断言（无 CSS 选择器、无像素比对），每断言一张截图证据，产出 `ui-verdict.json` 计入门禁（基建缺失降级为 skipped 并标注）。

## 钉子场景编目（语义契约，写死即契约）

场景名与 `scripts/run-acceptance.mjs` 注册表一一对应；编号为链内执行顺序：

**offline 链**（离线负例，不依赖远端）：
1. `validate`：fixture 根离线校验通过；含未知模板变量的根被拒（负例）。
2. `doctor`：自检通过；未知 `cli.app` 的环境失败（负例）。

**main 链**（主 fixture 生命周期，按序）：
1. `create`：`create --apply` 落地 crew / 两 agent / Space skill / Agent skill，state.yaml 注册 ID，远端名称带后缀（含 Agent Skill 部署名），模板变量已渲染（含 crew instructions），仓库清单 replace-all 语义成立。agents 声明运行目标（computer + runtimeProvider，运行时解析注入；平台契约——新 agent 必须带成对运行目标，后续建 agent 场景同此）。
2. `plan`：变更集视图预览（`+ agent <key> "<name>" (id known after apply)`）且不写 state。
3. `apply-drift`：新增 agent（名称与指令读回）、crew 角色变更、skill 附属文件删除、仓库清单收缩后 `apply --apply` 一次收敛；同轮断言 automation 已注册（同轮首发）。
4. `apply-idempotent`：二次 apply 幂等（ID 不变、无 mutation 回显——ADR 0021 漂移门控、远端名称不漂移）。
5. `pull`：快照文件落盘；陈旧快照条目先清空（负例）。
6. `backup-restore`：备份目录成形；原地恢复还原被破坏的 prompt；`--to` 异地恢复物化外部引用。
7. `automation-lifecycle`：主 fixture 自带 automation（`deployed` 缺省，钉缺省 true 契约）——apply 与 agent 同轮首发（state 注册 + 引擎读回 title/status/assignee/trigger）→ 二次 apply 幂等（apply 输出契约：in-sync 资源零动作行——断言无 `~`/`+ automation` 行 + 收口计数收敛 + title/assignee 复验）→ cron 漂移经 trigger edit 收敛（trigger id 稳定，非删+重建）→ 删除配置后 `prune automations` **硬删**并注销（事后 `automation view` 必须失败）。

**workflow 链**（独立 root，与主链互不遮蔽）：
1. `workflow-lifecycle`：捕获型（graph.json）与编写型（graph.yaml）双形态同轮一次成型（各自 `+ workflow` 落地行）→ 引擎读回名称 / deployed / lock → 编写型图的 run_agent executor 解析为 state UUID（export 读回）→ 二次 apply 幂等（无 `+`/`~` 漂移标记 + 收口计数收敛）→ 已上锁实例改元数据与 `deployed: false` 经 apply 解锁更新并恢复上锁 → 删除配置后 `prune workflows` 归档并注销（active 列表消失、`--status archived` 可见）。

**journey / smoke 链**：见「验收分层」B 与 C。

## 维护规则

- 场景与断言钉在本仓：CLI 契约合法演进时，在此处显式更新（这是有意的契约评审点），不要为了“让 dev 过门禁”悄悄放宽。
- 新增场景必须保持消费方视角：只准断言 README / guide contract 声明的可观察行为。
- **命令清单禁止手维护**：冒烟覆盖来自 candidate 自身的 `--help` 解析（`scripts/lib/surface.mjs`），x-deploy 加减命令自动反映在覆盖矩阵里。把某命令升级为钉子场景是显式契约评审决策，依据是矩阵里它的覆盖现状与本仓是否开始依赖其语义。
- UI 断言规格由 journey 场景产出（`journey.json`），不在 `screenshot.mjs` 里硬编码业务内容；选择器级漂移用环境变量（`LOGIN_*_SELECTOR` 等）临场适配。
