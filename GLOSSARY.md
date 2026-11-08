# hifox-configuration

xwing 家族部署实例的声明式配置仓库：配置集合、环境绑定，以及 xwing-deploy 的发布流水线（门禁、判定与发布语义都在本仓定义）。

## Language

### 发布流水线（Release Pipeline）

**Releasable Delta（可发布增量）**:
dev 分支上存在、main 上没有 patch 等价物的内容提交集合。为空即无事可发布。
_Avoid_: 新提交、待发布内容（未定义 patch 等价语义）

**发布（Release）**:
把通过验收的 dev commit 以 `--no-ff` 合并进 xwing-deploy 的 main 分支这一动作。发布不更新本仓的子模块指针。
_Avoid_: 上线、部署（指别的动作）

**Backmerge（回并）**:
流水线检测到 main 独有内容后，自动把 main 合并进 dev 并推送的动作。目的：让被验收的树永远是 main 的超集。
_Avoid_: 反向合并、同步分支

**验收钉子（Acceptance Pin）**:
本仓持有的 xwing-deploy 黑盒验收套件：断言只基于消费方可观察行为，CLI 契约演进必须在本仓显式更新才能通过。
_Avoid_: CI 测试、回归测试（不表达契约钉子语义）

**作者套件（Author Suite）**:
随被测代码同仓维护的测试（如 xwing-deploy 的 e2e）。可作开发期工具，不作发布门禁——写改动的人可以顺手改测试。
_Avoid_: 白盒测试（技术上仍是黑盒，问题在 oracle 独立性）

**Superseded（被取代轮）**:
验收通过但发布校验时 dev 或 main 已移动，本轮放弃发布、留给下一轮重新验收的运行结果。
_Avoid_: 失败、跳过（语义不同：验收是通过的）

**常驻报告任务（Standing Report Task）**:
流水线每轮运行结果（评论 + 截图附件）的汇聚任务；评论按「被测 commit 变化或 verdict 翻转」去重。
_Avoid_: CI 任务（不是执行载体，是报告载体）

**Candidate（被测提交）**:
一轮流水线开头定下的 dev HEAD 快照；验收与发布校验都只认它。
_Avoid_: 最新提交（会漂移）

### 编写与分发（工具侧）

工具侧术语（工具技能、编写型/捕获型、图骨架、块源、块目录等）的权威宿主是子模块 `vendor/xwing-deploy/GLOSSARY.md`（部署工具的词汇随工具仓演化），此处不重复定义。
