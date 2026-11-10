# Apifox CLI 研发专家汤隆接入二龙山：主题闭集扩展为 `apifox-cli`，过时标签 `tars-cli` 统一为仓库名

Apifox CLI（仓库 `apifox-cli`，产品包 `apifox-cli` / `apidog-cli`）此前以过时标签 `tars-cli` 归在全栈交付专家燕青（`yanqing`）的 `apifox` 主题下。CLI 的工程面自成一套：webpack 双发行包（`src/package-fox.json` / `src/package-dog.json`）与构建期常量注入、命令面与 `cli-schema` 的对外契约、i18n（zh / en）、真实项目端到端回归（`scripts/test.sh`）、以及 `ci-build` 的 CLI CI 与 `scripts/publish.sh` 发布门——与后端 DDD、桌面端 Electron 的规则互不相通。→ 新增专职研发 agent 汤隆（`tanglong`，金钱豹子 / 军器监，沿用 ADR 0004 的「入伙统筹调度面取水浒名」——军器监为「兵器」命名，对应 CLI 这类交付物），在**同一座分舵**（二龙山 `erlongshan`）加一条主题分支，不新开舵：`apifox-cli` → 汤隆。

汤隆是单 Agent 闭环：需求澄清、只读事实与影响面、实现、自验、自审（Space Skill `architecture-fit-review`）、提交、CLI CI 构建与**提测留包**、提测包回归（安装 pkg 实跑）全部自己完成。**提测 = 出 pkg，不发布**：交付物是 CI 打出的 `apifox-cli-<version>.tgz` / `apidog-cli-<version>.tgz`（`SAVE_PKG=true` 或提交信息含 `#save-pkg`，上传内部 FTP 的 `app-test/cli/`）。CLI 没有测试服 Web 面，因此**不接** `skadi` 部署与 `eir` Web 验收；发布（`TAG_NAME` + `scripts/publish.sh` + `release-*` tag）不在交付路径，汤隆不触发也不执行。

**归口规则**：以「主要改动落在哪个仓」为准——只涉及 `apifox-cli` → `apifox-cli`；涉及 server-apps / tars-app，或 CLI 改动必须与后端契约一起改而无法切分 → `apifox`（燕青，全栈闭环）。同时把集合内过时标签 `tars-cli` 统一为 `apifox-cli`（吴用路由表与主题切分、舵图 work 文本、燕青与 loki 的项目清单；`docs/adr/0009` 只改「归口规则」一行（`tars-cli` → `apifox-cli`）并指回本 ADR，其余历史措辞保留原样；`0004` / `0007` 中因新专家失真的专家清单同步补 `汤隆`）。

## Considered Options

- **加入 ragnarok 小队作为北欧名开发专家**（与 thor / baldur 同构）：拒。ragnarok 的完成门禁是「开发 → 维护者 Review → skadi 部署 staging → eir 从 `/main` Web 验收」，CLI 没有测试服 Web 面，末端环节无法落地；且 CLI 的调度入口是吴用 / 二龙山，不是 odin。
- **主题沿用 `apifox`、由燕青兼办 CLI**：拒（同 ADR 0009 对文档站的处理）。兼办会让「改哪个仓」的归口只能靠指令内约定维持，而 CLI 的构建、打包与发布纪律与后端 / 桌面端没有共享面。
- **把燕青收窄为只做 server-apps / tars-app**：拒。燕青的价值是跨端闭环（契约变更同时改两端），范围重叠由归口规则消解，不靠删减指令。
- **为 CLI 新开一座舵**：拒（同 ADR 0004 / 0009）。一座 Space 一舵；三位专家同在 Apifox 产研 Space，没有跨 Space 的理由。
- **只投 agent 配置、不接调度面**：拒（用户决策）。吴用是唯一用户入口；不进路由表与舵图，专家不可达，等于没接入。
- **沿用 `tars-cli` 作为主题键**：拒。该标签在本仓无对应仓库，与产品包名（`apifox-cli` / `apidog-cli`）也不一致；主题键取交付物名（同 `docs-site` 取交付物而非仓库 `explorer-v2`）。

## Consequences

- **主题闭集新增 `apifox-cli`**，同步面四处：二龙山 condition 分支与 `run_agent` 块（`config/apifox/workflows/erlongshan/`，含 `reject.sh` 的闭集文案与 workflow 描述）、吴用路由表 + 主题切分 + §4 委派映射 + §5 对外口径名字清单（`config/hifox/agents/wuyong/instructions.md`）、`branch-hall-dispatch` skill 的舵登记表与允许面、`GLOSSARY.md` 的「主题闭集」定义（`ledger` skill 的事项模板主题枚举随之补全）。
- **Space Skill `architecture-fit-review` 的 `assignTo` 增 `tanglong`**：汤隆的自审环节要求该 skill。
- **汤隆的仓库与目标**：`agent.yaml` 的 `repositories` 只声明 `https://cnb.cool/apifox/apifox-cli`；`target` 与燕青 / 萧让同机同 runtime，模型 `claude-opus-4-7`（与另两位调度面专家一致）。
- **头像**：`avatar.png`（512×512、256 色 PNG、约 190 KB）取自歌川国芳「通俗水滸伝豪傑百八人一個・金銭豹子湯隆」（1798–1861，约 1845–1850；Wikimedia Commons 标注公有领域，原件藏大英博物馆）的局部裁切，与燕青 / 萧让同属该国芳水浒系列，风格一致；出处与许可同时写入 PNG 元数据。
- **图迭代受 HF-15237 约束**：二龙山是 `workflow import` 建立的实例，改图走「归档旧实例 + 重新 import」；配置未变时 plan 显示 in sync 不代表图可增量推送。
- **双实例**：二龙山与汤隆随 apifox 环境双份（无后缀 / ` - staging`），吴用按同层级挑选中枢实例与委派对象。
- **未 apply**：本决策落在配置侧——staging / current 的远端生效（新建汤隆实例、二龙山图归档 + 重新 import、`assignTo` 变更）由后续 apply 收口。
