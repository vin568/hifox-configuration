# 能力目录

云端能力的口径：本仓 `config/` 已声明的 agent 即全部，未声明即不存在（ADR 0017）。要新云端能力 → 提案 → 用户批准 → 加 config 声明。

## 云端通道（hifox 平台任务）

清单 = 本仓 config 已声明的全部 agent（ADR 0017：未声明即不存在）：

| key | 名 | 面 | 用途 |
|---|---|---|---|
| wuyong | 吴用 | HiFox | 统筹入口（飞书对话与云端调度是他的面，不归你直接派） |
| linchong | 林冲 | HiFox | xwing 产品全栈（服务端 / 共享前端 / Desktop / CLI） |
| wusong | 武松 | HiFox | x-deploy CLI |
| daizong | 戴宗 | HiFox | 发布验收（按被测 commit 跑黑盒验收套件） |
| peixuan | 裴宣 | HiFox | 云端侧只读采集 |
| odin | 奥丁 | Apifox | Space 内协调：拆任务、分配、验收 |
| yanqing | 燕青 | Apifox | Apifox 全栈（tars-app 前端 + server-apps 后端） |
| xiaorang | 萧让 | Apifox | 文档站 explorer-v2 |
| tanglong | 汤隆 | Apifox | apifox-cli |
| baldur | 巴德尔 | Apifox | server-apps 后端 |
| thor | 托尔 | Apifox | tars-app / Electron |
| loki | 洛基 | Apifox | 代码分析 |
| eir | 埃尔 | Apifox | Web 验收（浏览器截图取证） |
| skadi | 斯卡蒂 | Apifox | 部署到测试服务器 |
| maintainer-reviewer | 伊登 | Apifox | 开发完成后的架构复审 |

清单会演进：派活前 `hifox agent list --output json` 现查，按同层级挑 prod/staging 实例，不要凭记忆硬编码。

命令面：

```bash
# 派（委派即触发；绝不追加 task summon）
hifox task create --space "<Space>" --title "<标题>" --delegate-agent <agent-id> \
  --body "<需求原话 + 实例 + 排查结论 + 验收要点>"

# 查（真值三件套：status / delegateStatus / agentWorkingStatus）
hifox task view <任务号> --output json
hifox run list --task <任务号>

# 取消执行
hifox task cancel-run <任务号> <run-id>

# 派出即登记：任务号进云端跟踪清单（watchlist.json，路径见首条注入），收口移除；
# 系统高频轮询这些任务，状态变化会以事件唤醒主控（哨兵，ADR 0018）。
```

## 本地通道（本机 herdr 能力 agent）

- **蒋敬（jiangjing）**：复盘分析 + 直连记账抽查。`herdr agent prompt jiangjing "<复盘任务>"`；产物写结果目录 md 回路径。
- **动态能力 agent**：jobs workspace 按需拉起（`herdr workspace/tab/pane --help` 自查命令面），同时最多 3 个；完成后读结果、台账收口、让 agent 退出。
- 派活模板：`herdr agent prompt <名> "<任务 + 上下文 + 结果写到 <结果目录>/<作业名>.md 只回路径>"`。

## 直连通道（受控命令面，ADR 0019）

1. 只读命令（查文件、`git log`/`git status`、进程、只读查询）：自由跑，逐条记台账；拿不准是否有副作用就降级问用户。
2. 变更动作：只跑 `super-agent/direct/` 白名单目录里的脚本，参数透传。
3. 白名单外的变更：先问用户，批准后执行并记账。

硬禁区（任何情况不碰）：删除类命令、安装软件、动本仓以外的 git、读取任何凭证/密钥、绕开 `superctl reply` 对外发消息。
