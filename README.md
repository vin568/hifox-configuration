# Xwing Agent Crew Configuration

“诸神的黄昏 Ragnarok”小队与 hifox 配置集合的声明式本地配置，面向 xwing 家族 CLI 应用（部署实例 `hifox` / `sharkly`）。本仓库只保存配置与环境绑定；部署逻辑位于 git 子模块 `vendor/x-deploy`——本仓库同时是该 CLI 的开发配置仓库（见 [`AGENTS.md`](AGENTS.md)）。

## 操作面

部署操作面由 CLI 自描述：`./x-deploy --help`（命令与变更分级）、`./x-deploy guide contract`（完整契约与结构语义）；agent 入口见 `AGENTS.md` 受管章节。

## 配置集合与环境清单

同 Organization（Apifox）下两个 Space：**Apifox 产研** 与 **HiFox 产研**；同一配置集合可被多个环境绑定，各环境独立 `state.yaml`。

| 环境 | configRoot | 目标 Space | 说明 |
| --- | --- | --- | --- |
| `staging`（默认） | `config/apifox` | Apifox 产研 | 后缀 ` - staging`，Ragnarok 日常迭代环境，已部署 |
| `current` | `config/apifox` | Apifox 产研 | 无后缀，线上正在使用的实例；`ignoreAgentModels: true` |
| `example` | `config/apifox` | Apifox 产研 | 后缀 ` - example`，未部署，新环境复制模板（`cp -R environments/example environments/<name>`） |
| `hifox-prod` | `config/hifox` | HiFox 产研 | 无后缀，已部署 |
| `hifox-staging` | `config/hifox` | HiFox 产研 | 后缀 ` (staging)`，已部署 |

## 根级共享技能源

`skills/` 存放跨 agent 复用、但不以 Space Skill 共享实例的技能源目录；各 agent 在自身 `skills/` 目录下用 Agent Skill 引用同一源，部署为各 agent 专属实例（结构语义见 `./x-deploy guide contract`）。源目录名带产品前缀（`xwing-` / `apifox-`），因为这里是跨配置集合的共享命名空间；agent 实例 key 保持裸名（`<agent>/<key>` 已命名空间化）。当前条目：`xwing-task-runtime`（task-local 运行时操作契约 + 环境预检 `preflight.sh`，由 linchong 与 daizong 引用）、`apifox-staging-deploy`（apifox 集成测试服部署契约：参数模板、端口映射、失败口径，由 yanqing、xiaorang、skadi 引用）、`apifox-web-acceptance`（测试服 Web 验收与取证规范：账号、截图、录屏、结论与回流，由 eir、yanqing、xiaorang 引用）。抽取边界（机械契约 vs 常驻行为契约）见 [`docs/adr/0010`](docs/adr/0010-shared-contract-skills.md)。三处技能目录的用途划分见 [`AGENTS.md`](AGENTS.md)。

## x-deploy 发布流水线

`vendor/x-deploy` dev → main 的定时发布门禁，全部资源在 `config/hifox` 集合内（与 linchong 同仓）：workflow `x-deploy-release-gate` 是**编写型**（源形态与迭代回路见工具技能）——**无 GUI 创建**，`workflow import` 一次成型。它每 3 小时用 patch 等价判定 Releasable Delta（真分叉自动 backmerge），指派 `agents/daizong` 在 task-local dev 服务上跑该 agent 自带的黑盒验收套件（消费方契约钉子），通过即合并被测 commit 到 main 并推送；结果与截图汇入常驻报告任务。因 `config/hifox` 被 hifox-prod / hifox-staging 双环境部署，workflow 会存在两份实例，check-update 块以运行标记守卫让并发轮退化为 skip。决策依据见 [`docs/adr/0001`](docs/adr/0001-x-deploy-release-gate.md)，术语见 [`GLOSSARY.md`](GLOSSARY.md)，workflow 的增删改知识见工具技能（`.agents/skills/xwing-workflows/`，init 受管安装）与 `./x-deploy guide workflows`。


## 统筹调度

用户统一入口是 `agents/wuyong`（吴用）：飞书私聊或群 @ 进入平台原生持久多轮会话，判定主题后调度——专家主题经手动触发的调度中枢单图路由执行：xwing 产品 → `workflows/zhongyitang`（忠义堂，HiFox 产研）派林冲，x-deploy → 忠义堂派武松，apifox 产品 → Apifox 产研 Space 的分舵 `workflows/erlongshan`（二龙山）派燕青、文档站（`docs-site`，仓库 explorer-v2）→ 二龙山派萧让、Apifox CLI（`apifox-cli`，仓库 apifox-cli）→ 二龙山派汤隆（吴用经 `hifox workflow run --space` 跨 Space 触发，操作面收窄在 `branch-hall-dispatch` skill）；`run_agent` 不挂任务、结果由吴用查询执行进度后转述；咨询 / 交付的判定**默认咨询**（明确动作指令或对具体提议的确认才建任务，见 `instructions.md` §1.1）；飞书侧一律**对外口径**（吴用是唯一接口，内部 agent / workflow / Space / 编号 / 承接人不外露，见 `instructions.md` §5 / ADR 0007）；密钥 / 凭据 / 环境变量值与机器标识不进入任何输出、台账或文档（见 `instructions.md` §6 / ADR 0008）；杂活自理；完整交付经判定后建任务委派。跨会话工作记忆是 Space 共享文档里的**文件夹式调度台账**（`Agents / <部署名>/` 下 `进行中` / `等待用户` / `计划` / `已收口`，一事项一文档、状态=文件夹、索引=列表；不维护共享总台账文档，常驻只读未完结事项，历史按需检索，状态回平台现查），读写与并发协议是吴用的 agent skill `wuyong/ledger`。后台巡检 automation `automations/wuyong-ledger-sweep` 每 30 分钟按同一套真值规则对账「进行中」事项（终态标 `待转述`，转述与收口由对话完成；异常私聊上报；不重派、不归档、不碰等待用户 / 计划），仅作加速器，回答仍以对话回合查询为准。key 命名与身份重建决策见 [`docs/adr/0002`](docs/adr/0002-steward-feishu-dispatch.md) / [`0003`](docs/adr/0003-agent-key-pinyin-identity-rebuild.md)，跨 Space 分舵决策见 [`0004`](docs/adr/0004-cross-space-branch-hall.md)，调度台账决策见 [`0005`](docs/adr/0005-steward-dispatch-ledger.md)，意图判定决策见 [`0006`](docs/adr/0006-steward-intent-routing.md)，对外口径决策见 [`0007`](docs/adr/0007-steward-external-voice.md)，密钥与机器标识决策见 [`0008`](docs/adr/0008-secret-and-machine-identifiers.md)，文档站专家与主题粒度决策见 [`0009`](docs/adr/0009-docs-site-expert-topic.md)，Apifox CLI 专家与主题粒度决策见 [`0011`](docs/adr/0011-apifox-cli-expert-topic.md)，术语见 [`GLOSSARY.md`](GLOSSARY.md)。

夜间自省由独立只读 agent 裴宣（`agents/peixuan`）执行：每日 02:00 复盘前一日调度团队的工作流程与运行状态，产出《自省日记》《改进项》与《自省总览》；吴用在下次前台会话开头转述摘要，用户采纳后由吴用建实现事项。设计见 [`docs/adr/0013`](docs/adr/0013-nightly-reflection.md)，术语见 [`GLOSSARY.md`](GLOSSARY.md)。已知前置：agent 执行凭据不支持 workflow 读面；未注入用户级凭据 / PAT 前，workflow 执行与咨询成功率在日记中标“未覆盖”。

## 子模块（x-deploy）

首次克隆与补齐：

```bash
git clone --recurse-submodules git@apifox.github.com:vin568/hifox-configuration.git
git submodule update --init --recursive
```

`vendor/x-deploy` 是独立仓库，需要其读取权限；SSH/HTTPS 的本地 Git 配置按团队环境处理。本仓库跟踪 `dev` 开发分支（`main` 为稳定）。升级 `dev` 最新并刷新受管物料：

```bash
git submodule update --remote --rebase vendor/x-deploy
./x-deploy init
git add vendor/x-deploy
git commit -m "chore(deps): bump x-deploy"
```

固定已验证版本用 `git -C vendor/x-deploy checkout <commit>` 代替第一步。开发、测试与提交边界见根 [`AGENTS.md`](AGENTS.md) 与子模块 `AGENTS.md`；各安装形态的升级语义见 `./x-deploy guide upgrade`。

## 依赖

- `./x-deploy doctor` 一次校验全部工具依赖（node ≥ 18、`yq`、目标 CLI 可执行与 state 盖章）。
- 使用 mise 时 `mise install` 按 `mise.toml` 安装固定 Node 版本。
