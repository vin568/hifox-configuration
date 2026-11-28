# AGENTS.md

## 仓库定位与分工

本仓是宋江运行时实例的宿主，兼载 xwing 家族 CLI 应用（部署实例）的声明式配置（配置集合与环境绑定清单见 `.xwing/README.md`），同时是部署 CLI 的开发配置仓库：`vendor/x-deploy` 是 git 子模块，部署行为改动只在子模块内开发，本仓库跟踪指针与配置。2026-11 起仓根就是宋江 super agent 的本地运行时实例根（见「Super Agent 本地运行时」与 ADR 0016–0031）；xwing 部署配置全部收在 `.xwing/` 下，由根 `x-deploy.yaml` 声明（ADR 0052）。布局：仓根 = 实例根（`capabilities/` `direct/` `bin/` `scripts/` `superctl.example.toml`），`.xwing/` = 部署载荷（`config/` `environments/` `skills/` `snapshots/` `backups/`），`vendor/` = 三个子模块。

## 文档职责判定

新增或修改本仓文档（本文件 / `README.md`）前，先问这段内容的事实归属：

1. **只有本仓知道的**（环境清单、仓库身份、git 物流、子模块开发流程）→ 写在这里。
2. **工具行为与配置结构语义**（命令、选项、字段、闭集、guard、目录结构）→ 不写，指向 CLI 自描述面（`--help` / `doctor` / `guide` / `guide contract`）；不列举、不复制契约细节。
3. **面向全部消费仓的策略或指针声明** → 改受管章节模板（子模块 `src/commands/init.mjs`），经 `init` 分发，不手写副本。

原则与取舍见子模块 `docs/adr/0015`；受管章节内的策略句是唯一权威表述，本节只是判定方法。

## 技能目录

本仓有四处技能目录，消费者互斥；新增技能先按消费者判定放置：

| 目录 | 消费者 | 机制与形态 |
| --- | --- | --- |
| `.xwing/skills/` | 部署到远端的 xwing agent | 技能源：由 `.xwing/config/<集合>/agents/<key>/skills/<skill-key>/skill.yaml` 以 `dir: "@/.xwing/skills/<名>"` 引用同一源，部署为各 agent 专属实例。`SKILL.md` 带最小 frontmatter（`name` = 目录名、`description` 写内容职责）：同一份目录既能被 x-deploy 部署（身份与部署侧的 `name`/`description` 仍以 `skill.yaml` 为准），也能被本地运行时以 `skills.yaml` 的 `@/.xwing/skills/<名>` 路径引用。源目录名带产品前缀（`xwing-` / `apifox-`）：`.xwing/skills/` 是跨配置集合的共享命名空间，裸名会假装通用；agent 实例 `key` 保持裸名（`<agent>/<key>` 已按 agent 命名空间化，改名会动 state 身份）。当前：`xwing-task-runtime`（linchong、daizong、huarong 引用）、`apifox-staging-deploy`（yanqing、xiaorang、skadi 引用）、`apifox-web-acceptance`（eir、yanqing、xiaorang 引用） |
| `.xwing/config/<集合>/skills/` | 部署到远端的 xwing agent（Space 内共享） | Space Skill 实例，多 agent 挂载同一份；结构语义见 `./x-deploy guide contract`，不在本仓复述 |
| `.agents/skills/` | 在本仓工作的开发 agent 与人（不部署） | 项目级工具技能（Agent Skills 开放标准位置）：`SKILL.md` 带 `name`/`description` frontmatter。CLI 自带技能（当前 `xwing-workflows`）由 `./x-deploy init` 整目录受管重置，定制须复制改名；清单外的自有技能 init 不触碰，当前自有：`worktree-dev`（本仓 git worktree 开发契约 + `worktree.sh` 助手）、`code-review`（由 `npx skills add` 装入，`skills-lock.json` 锁到 commit + content hash，供 `super-agent` 的 `review` 组合技能引用） |
| `capabilities/` | 本机宋江主控（不部署） | 本地能力注册表，目录即声明（ADR 0021）：`agents/<key>/{capability.yaml,instructions.md}` + 可选 `skills.yaml`（声明挂哪些共享技能）、`projects.yaml`（该能力能碰哪些项目，ADR 0043）与 `playbooks.yaml`（该能力可用的作业法，ADR 0049）、`playbooks/<key>.md`（作业法：派活期 `--playbook` 指定并内联，不供模型挑选）、`projects/<key>.yaml`（项目：仓库身份 + 基线）与可选 `projects/<key>.instructions.md`（项目约定的补充与覆盖）、`nodes/<key>/checkouts.yaml`（该机器上各项目的绝对路径）（含 `role: master` 的宋江、`role: retro` 的蒋敬、`role: inspector` 的时迁与 `role: job` 的朱贵/石秀/安道全；常驻 agent 的名单、启动参数与手册由它派生，job agent 按作业拉起，workspace 布局仍是固定三件套 master/crew/jobs；巡检者写发现、运行时投递、主控处置，见 ADR 0044）、`skills/<key>/SKILL.md`（共享池：按 agent 同目录 `skills.yaml` 的声明挂载，未声明不可见；当前 `dispatch-envelope`（主控与朱贵共同挂载的派活信封）与 `direct-tiers`（主控与复盘者共同挂载的三级判据）。作业 agent 的回报纪律由框架角色契约注入，不占共享池）与 `agents/<key>/skills/<key>/SKILL.md`（该 agent 专属；当前 `local-agents`、`platform-dispatch` 在宋江专属下，`direct-tiers` 已移入共享池（主控与蒋敬共同挂载））；含 `role: master` 的宋江、`role: retro` 的蒋敬、`role: inspector` 的时迁、`role: job` 的朱贵 / 石秀 / 安道全。框架代码在子模块 `vendor/super-agent/`，本目录只有实例物料——标准 Agent Skills 形态，位置即归属，与 `.xwing/config/` 的 `skills/` ↔ `agents/<key>/skills/` 分法一致；真源目录直接交给底层 CLI 的 `skills.customDirectories`，不复制（ADR 0025）、`scripts/<key>.yaml`（指向 `direct/` 实脚本）、`platforms/<平台>/<key>.yaml`（平台侧 agent 指针，平台 = 平台 CLI 应用名如 hifox；真源仍是 `.xwing/config/`）。`superctl capabilities --check` 查漂移 |

判定一句话：给部署的远端 agent 用 → `.xwing/skills/`（源 + agent 引用）或 `.xwing/config/<集合>/skills/`（Space 共享）；给在本仓干活的开发 agent 用 → `.agents/skills/<名>/`；给本机宋江主控用 → `capabilities/`。改运行时机制（不是本仓实例）→ 子模块 `vendor/super-agent/`，按子模块流程开发。

技能目录里随仓维护的脚本（如戴宗验收套件的 `screenshot.mjs`、`xwing-task-runtime` 的 `preflight.sh`）有回归测试：根目录 `npm test`（node:test，零依赖，用 `test/fixtures/` 的边界 stub）；用例按源码路径镜像存放——agent 技能为 `test/<agent>/<skill>/<脚本名>.test.mjs`，根级共享源按 `.xwing/skills/` 下目录名（如 `test/xwing-task-runtime/`）——改这些脚本必须先跑或补用例。实例对账脚本 `scripts/check-registry.mjs` 的用例在 `test/scripts/`、`direct/` 下脚本的用例在 `test/direct/`，与源码路径镜像；改它同样先跑 `npm test`。

## Super Agent 本地运行时（仓根）

仓根是**宋江运行时实例**（本仓是 super-agent 框架的 dogfood 实例）：身份、能力声明与配置都在根上（`capabilities/` `direct/` `superctl.example.toml`，稳定入口 `bin/superctl` 由 `scripts/build` 产出）；Go 运行时是子模块 `vendor/super-agent`（ADR 0034，框架仓 `vin568/super-agent`，branch `main`）。框架只提供机制，机制词见 `vendor/super-agent/GLOSSARY.md`；实例运营词见 `GLOSSARY.md`「主控编排」节。它跑在本机 herdr 上，不经 x-deploy 部署、不进远端环境与 `state.yaml`。平台能力口径 = `.xwing/config/` 已声明的 agent（未声明即不存在；平台 = hifox / 将来的 sharkly）；主控提示词与能力注册表属运行时物料，不进 `.xwing/config/`。运行时面（`state_dir`、`env_prefix`、受管服务名）按实例名 `super-agent` 取，主控人格名（`songjiang`）只是能力内容，不写进路径与凭证命名空间（ADR 0053）。平台任务的人工豁免表是运行时状态：`state_dir/untracked.yaml`，由 `superctl untracked list|add|remove` 读写，不进仓库（ADR 0054）。框架测试在子模块内 `go test -race ./...` 自管；本仓的 `npm run check` 跑实例对账（框架 `--check` + 本仓跨仓规则），`npm test` 仍只管技能脚本与检查脚本的用例。上下文测量与 handoff 见 ADR 0030：受控面是 master/retro 与 `inflight.kind=local` 的 job agent（本机或执行节点）；平台任务与非在途 herdr agent 不测、不交接；所有受控 agent 共用 `internal/ctxmeter` 的 Snapshot 公式，harness 差异只在 adapter/`context_probes`。

**同步规矩（ADR 0023）**：`.xwing/config/<集合>/agents/<key>` 与 `capabilities/platforms/<平台>/<key>.yaml` 双向一一对应（平台↔集合的映射读 `.xwing/environments/*/environment.yaml` 的 `cli.app` + `configSet`）——config 里加 / 删 / 改 agent 必须同步平台目录（name 指同一身份、任一 `.xwing/environments/*/state.yaml` 有部署记录）；**新加配置集合而没在任何 `.xwing/environments/*/environment.yaml` 里写明 `cli.app` + `configSet` 时，`npm run check` 会直接报红**（平台归属推不出就没法对账）。由 `npm run check`（`scripts/check-registry.mjs`，部署目录读根 `x-deploy.yaml`）强制、`npm run check:live` 再对平台核一遍；框架的 `capabilities --check` 只管框架不变量（ADR 0002）。平台派活只经平台派活员朱贵（`zhugui`，本机 job agent），主控不挑人、不解析平台 ref。执行节点是另一条通道：主控直连派活（`--node` 钉死或 `--auto` 按准入阈值自动择节点），不经平台派活员（实例 ADR 0041/0042 / 框架 ADR 0008；命令与目录结构以 CLI 自描述为准）。

## 子模块开发（唯一允许改部署逻辑的地方）

1. `vendor/x-deploy` 是完整 git 仓库，不是 vendored 只读代码；`.gitmodules` 固定 `branch = dev`（`main` 为稳定分支）。detached HEAD 时先 `git -C vendor/x-deploy switch dev`。
2. **同频分支（根仓与子模块同名）**：一个需求从开工到合并只用一个分支名 `feature/<作业名>`，根仓与**每个要动的子模块**都用这个名字——不允许「根仓在 `main`、子模块在 `dev`」或「子模块在特性分支、根仓在 `main`」的错位。起点一律钉远端：根仓从 `origin/main` 切，子模块从各自 `.gitmodules` 声明的集成分支切（`x-deploy` / `v-skills` 是 `dev`，`super-agent` 是 `main`）。开工前根仓与要动的子模块各 `git fetch --prune origin`，把基底 commit 与 behind 数写进结果第 1 行。
3. 在子模块内编辑并运行测试（`x-deploy` / `v-skills` 是 `npm test`，`super-agent` 是 `go test -race ./...`）；真实 API 测试按 CLI App 参数化（hifox / sharkly 双套），只在各自专用测试组织上跑，见子模块 `AGENTS.md`「测试」节的 `.env.e2e` / `<APP>_ORGANIZATION_ID` 与 `<APP>_E2E_*` 约定。
4. **提交边界**：子模块的提交落在自己的 `feature/<名>` 上；根仓的提交落在根仓的 `feature/<名>` 上，只包含配置/文档/壳脚本/测试/指针/super-agent 实例物料变化（框架代码在 `vendor/super-agent`）。指针可以先指向子模块**特性分支**上的 commit（同频开发的中间态，此时尚未合并）。
5. **一次合并，两侧同时到位**：先把子模块 `feature/<名>` 并入它的集成分支并推送，再把根仓分支的 gitlink 更新到**集成分支上的 commit**（快进则 sha 不变；产生了 merge commit 就补一次指针提交），最后把根仓 `feature/<名>` 并入 `main`。只并一侧不算完成：根仓 `main` 会指向一个子模块集成分支上不存在的 commit，或整批子模块改动没进根仓。合并后校验：`git -C vendor/<sub> rev-parse <集成分支>` 必须等于 `git ls-tree main vendor/<sub>`，且两侧集成分支都已推送到 origin。
6. 拉上游更新：`git submodule update --remote --rebase vendor/x-deploy`，随后 `./x-deploy init` 刷新受管物料；`./x-deploy update` 对子模块布局一律拒绝（见 `./x-deploy guide upgrade`）。`vendor/super-agent` 同样按子模块流程更新（框架仓 `main`），更新后跑 `npm run check` 与 `go -C vendor/super-agent test -race ./...`。
7. **维护员安道全**（`role: job`，ADR 0035）承接本仓文件改动：每作业一个 git worktree（派活时按注入的项目事实跑 `direct/worktree add --repo <检出> --name <作业名>`；`.agents/skills/worktree-dev/scripts/worktree.sh` 只给本仓开发 agent 用，含子模块初始化），提交并 push **同名特性分支**（`feature/<作业名>`，根仓与子模块同名），合并与 worktree 清理由用户按第 5 条一次做完两侧。

不要把子模块当只读依赖，也不要绕开它把部署逻辑复制进根仓库或壳脚本；`x-deploy` 壳脚本的路径与 `--root` 语义属消费方契约，不要改。修改术语或契约前，先读子模块 `GLOSSARY.md` 与 `docs/adr/` 并同步。子模块文档拆分约束见其 `AGENTS.md`「文档职责」节：工具仓 `README.md` 是随包分发的纯消费契约（`guide contract` 原样输出）；维护者内容归子模块 `AGENTS.md`；随包文档（README 与 runbooks）不反向引用不随包的文件（`docs/adr/`、`AGENTS.md`、`GLOSSARY.md` 的编号与链接在安装形态下是死链）；部署实例名只出现在校验闭集处（依赖节、`cli.app` 字段表）；改契约措辞需同步其不变量与 runbook。

<!-- x-deploy:start -->

## Xwing Declarative Configuration

本项目的 Crew / Agent / Skill / Workflow / Automation 声明式配置位于仓库根（默认 `config/<config-set>/` 配置集合 + `environments/<env>/` 部署绑定；目录位置与默认 environment 可用根目录 `x-deploy.yaml` 配置，见 `./x-deploy guide contract`）。部署工具 x-deploy 自描述其操作面，使用方法与配置结构语义以 CLI 为准、不在本仓库其他文档复述：

- 不变量：`state.yaml` 的 UUID 是已部署资源的唯一权威，勿手改；key 改名时把对应条目搬到新 key（UUID 不变），validate 的 look-renamed 门禁会拦下未重绑的改名；变更命令默认预览，必须显式 `--apply` 才执行（各命令的变更分级见 `./x-deploy --help`）。
- 预检：`./x-deploy doctor`（node / yq / CLI 可执行 / state 盖章 / settings 与 configSet，全离线，先跑它）。
- 命令目录：`./x-deploy --help`；逐命令 `./x-deploy <command> --help`。
- 过程手册：`./x-deploy guide <topic>`（authoring / automations / maintenance / restore / troubleshooting / upgrade / workflows，随 CLI 版本发布）。
- 完整契约：`./x-deploy guide contract`（CLI 按自身位置解析随包 README，任意包管理器/安装布局可用）；copy/子模块安装亦可直接读 `vendor/x-deploy/README.md`。

<!-- x-deploy:end -->
