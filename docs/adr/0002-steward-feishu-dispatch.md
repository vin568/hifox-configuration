# 统筹入口吴用经飞书原生会话接入，专家调度收敛在单一手动 workflow（忠义堂）

用户的统一入口是 `config/hifox` 集合内的 `agents/wuyong`（吴用）：飞书私聊或群 @ 进入**平台原生的持久多轮会话**（sharkly 服务端 `feishu_bots` 每 agent 一 bot、outbound WebSocket 长连接、按 `(bot, chat_id+话题)` 关联 conversation，GA 无 feature flag），吴用在会话内判定主题；专家主题经**手动触发**的单一 workflow `workflows/zhongyitang`（忠义堂，`start_trigger` + `--input {topic, message, context}`）按 condition 分支派给林冲（`agent: linchong`）或武松（`agent: wusong`）做单条消息的排查答复。`run_agent` 不带 task id → 不产生任务卡，聊天链路零任务增量；吴用异步触发（`--async`）后按 execution id 查询进度并在会话内转述结果。完整交付（正式开发）保留给任务委派，且仅经用户显式要求或排查后确认触发——「不想因为路由产生一堆任务」是本决策的起点。

## Considered Options

- **任务委派作路由原语**（`task create --delegate-agent` 直发专家）：拒。每次互动产生任务卡，任务板被聊天噪音淹没；委派语义保留给完整交付。
- **HiFox DM + summon 会话内协作**：拒。平台无多方会话原语——`task summon` 是任务作用域、agent 间不能互发 DM（CLI 与服务端双面核实），纯 DM 无法把专家带进会话。
- **webhook 触发的 workflow 承载入口**：拒。workflow 触发器闭集没有飞书事件（只能 generic_webhook 外接桥），且每事件一次执行、无多轮上下文；飞书订阅 URL 的 challenge 握手与加密不可控。飞书原生 agent 会话上线后，此路径全面劣势。
- **crew（队长编排）**：拒。与 workflow 调度职能重叠，crew 会话同样是任务作用域；吴用单 agent 足够承载路由与杂活。
- **每主题一个 workflow**：拒（用户决策）。实例数随主题线性增长、路由表散落多处；单一 workflow「能力聚义」——加主题 = 加分支，演进面一处。

## Consequences

- **声明式边界**：飞书 bot 绑定（设备流安装、私聊 / 群 @ 触发设置）与 `allowWorkflowInvocation`（平台默认开）都在平台侧，不在本仓 agent.yaml 表达面内；环境重建后需手动重装 bot。只给 hifox-prod 实例装（吴用 / 忠义堂各留一份 `(staging)` 实例用于联调）。
- **主题闭集是三处共同不变量**：忠义堂 condition 分支、吴用路由表（wuyong instructions）、任务委派映射必须同步演化；漏一处即「吴用派了但堂里没人接」或反向。
- **专家块 work 显式收窄**为「单条消息的排查 / 分析 / 答复」：linchong / wusong 的 instructions 是任务模式中心（阶段门禁、【执行】/【推送】门槛），不收窄会把聊天当需求跑完整交付流程。
- `start.input` 的运行时形态（字符串 / 对象）未在引擎源码面唯一钉死，路由条件用 `typeof` 三元式双态兼容；staging 实例首跑联调验证。
- 双环境 = 同 Space 两份实例（`忠义堂` / `忠义堂 (staging)`、`吴用` / `吴用 (staging)`）；吴用按**无后缀名**挑生产实例，其 instructions 已写明。
- workflow 图迭代受 HF-15237 约束（同 ADR 0001）：改图走「归档 + 重新 import」。
- 吴用不绑 repositories：代码动手永远走忠义堂（专家有仓库）或任务委派；「全能兜底」降格为「杂活自理 + 求援说明」。
