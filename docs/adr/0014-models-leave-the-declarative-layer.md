# 模型生命周期退出声明面：create 播种、更新忽略，运行期由平台 Runtime 默认层管理

生产环境模型故障时，现状是三重摩擦：**批量改**——每个 `agent.yaml` 都在 Agent 层硬编码 `runtime.model`，一次故障要改 N 个文件、提交、`./x-deploy apply`；**存量会话粘连**——服务端粘性继承（sharkly HF-14130）把会话上一轮的模型升格为 run 级最高优先级，Agent 换模型后存量会话继续用旧模型，直到该轮失败；**恢复路径与故障同源**——任何「让 agent 代劳切换」的快车道（吴用 / workflow）本身跑在坏掉的模型上，模型挂了快车道也是坏的。恢复杠杆必须**不依赖任何模型**、由人执行、秒级生效。另一面，纯忽略又留一个坑：agent **创建**时若 Runtime 默认未配置，新 agent 会静默落到 runtime 本地默认（如 omp `config.yml` 的 `modelRoles.default`，可能是 `ollama/translategemma` 之类的错误模型）。

→ 模型策略取「**出生播种、生命周期放手**」：x-deploy 的 ModelPolicy 缺省为——声明的 `runtime.model` 只在 agent **create** 时传递一次（播种，保证创建时刻确定性），**edit** 永不传递（`ignoreAgentModels` 缺省 `true`；显式 `false` 恢复创建与更新全量声明式管理；不声明 `runtime.model` 则创建也不播种）。运行期批量操作 = `./x-deploy model`（子模块内置运维命令：批量写显式模型选择，按 `state.yaml` 锚定受控 agent，默认预览变更集、读当前值跳过已在目标的 agent、幂等；`default` 参数清除选择、跟随 Runtime 默认模型）——平台侧的任何模型变更都不会被下一次 apply 回滚。恢复路径 = 人 + 命令行，零提交零部署。

## Considered Options

- **环境级模型声明 / modelMap 映射**（`environment.yaml` 声明默认模型或旧→新映射）：拒。只是把「改 N 个文件」变「改 1 个文件」，发布回路（commit + apply）原样保留，应急延迟与「紧急 apply 捎带其他待发布变更」的风险都在。
- **agent 驱动的快车道**（飞书指示吴用改配置并 apply）：拒。执行者与故障同源（shared fate），模型不可用时快车道本身不可用。
- **完全忽略（创建也不传）**：拒。新 agent 的出生模型取决于 Runtime 默认是否恰好已配置，未配置时静默落到 runtime 本地默认；创建时刻的确定性应该由受评审的声明保证，而不是由运维现场状态决定。
- **维持 apifox 异构档位（opus / sol / luna）为平台侧设置**：曾按本 ADR 初版采纳；2026-11-12 收敛——出生规格统一 `hifox/glm-5.3`，远端遗留的异构选择由 clear 脚本一次性清除，apifox 集合不再保留分层。
- **sharkly 侧根治粘性继承**（会话跟随 Agent/Runtime 默认、Agent 换模型自动清除粘性）：另行推进，不阻塞本决策。粘性规则里「上一轮 failed / cancelled 不留粘性」已保证存量会话**最多失败一轮后自愈**，使本决策可以先落地。

## Consequences

- **切换工具是 model 命令**：`./x-deploy model <model> --apply` 直接批量改写显式选择，换模型 = 换参数重跑；播种 / 遗留的选择同样被它覆盖，不再需要单独的 clear 步骤。传 `default` 清除选择，agent 回到跟随 Runtime 默认（解析序：Agent 选择 > Runtime 默认）。
- **应急顺序**（全部人手执行，零模型依赖）：
  1. `./x-deploy model <可用模型> --environment <env> --apply`——对每个受影响环境批量切换，无需平台 UI；
  2. 存量会话**自愈**：粘性继承只在上一轮成功时生效，旧模型不可用会让该轮失败，下一轮自动跟随新模型；`provider_session_id` 保留，ACP 会话池原地热切，上下文不丢。等不起一次失败的单个会话，让用户发 `/new` 重开上下文；
  3. 恢复后可补一个 commit 对齐文档基线（break-glass 后记账，不阻塞恢复）。Runtime 设置页默认模型降为可选兜底层：清除选择后的跟随目标，不做也不影响上述路径。
- **既有 agent 不受影响**：播种只发生在 create；已部署 agent 的远端选择在 edit 缺省不传 `--model` 下被保留，批量改写由 `./x-deploy model` 显式执行（apifox 环境的 opus / sol / luna 遗留选择同样如此）。hifox-prod 目前 state 只绑定林冲，其余 4 个 agent 首次 apply 创建时会播种 `hifox/glm-5.3`，之后即脱离声明面。
- **批量清存量会话不可行也不必要**：会话枚举按创建者限定、写入按参与方 / 任务可见性门控（会话是私有的，运维无全局通行证）；未覆盖会话由上一条的自愈兜底。
- **审计取舍**：模型运行期变更不再有 git diff / 评审记录，代价换秒级应急与「故障切换完全不碰仓库」；出生模型仍在受评审的 `agent.yaml` 里。运行期审计面 = `./x-deploy model` 的预览 / 结果视图（默认预览、显式 --apply）+ 平台侧变更记录。
- **对 x-deploy 消费方是行为变更**：缺省策略从「创建与更新都传模型」变为「仅创建播种」——更新停止管理模型（远端既有选择保留，不会误清），需要全量声明式管理的环境显式 `ignoreAgentModels: false`。契约措辞随子模块 README / GLOSSARY 同步。
- **本仓 YAML 语义**：`runtime.model` 保留为出生规格，全配置集合统一 `hifox/glm-5.3`（create 预览的 `· model:` 行即播种声明）；`plan` 的更新比对中模型字段不再参与漂移（desired 为空即不管，无永久漂移误报）。
