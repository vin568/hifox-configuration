# 移动端专家花荣接入忠义堂：主题按「交付面」扩展为 `xwing-mobile`，两端技能降为 agent 私有的命令参考

xwing 仓库的移动端原生客户端（`apps/ios` Swift/SwiftUI、`apps/android` Kotlin/Compose）此前挂在 `xwing` 主题下，由全栈交付专家林冲（`linchong`）兼办。这条归口在两个方向上都不成立：

1. **改动面不相交**。林冲的指令与技能围绕 Go 服务端、共享前端、Desktop 与 Rust CLI 组织；移动端是模拟器 / 真机取证、金样生成器、xcodegen / Gradle 工程纪律、签名与打包管线，与桌面端规则互不相通。移动端工作塞进 `xwing` 意味着每次都要在 27k 字符的通用指令外另寻事实源。
2. **验收形态不同**。移动端完成的判据是需求方在指定模拟器或真机上亲手走查（两轮交付：开发分支功能确认 → 集成分支集成态确认），与桌面端「浏览器 / Electron 截图」不是同一套证据链。

同时，移动端的操作知识此前只存在于两位需求方各自机器的技能目录里（`ios-client-development` / `android-client-development`，各含 `SKILL.md` 与一份自包含 system prompt）。它们是仓内 `apps/ios/docs/`、`apps/android/docs/` 的转述，带着会漂的事实（工作根写成独立的 `xwing-mobile-client` / `xwing-android-client` 检出、主开发分支写成 `feature/mobile-client`、开工前要求自行安装 `swiftui-pro` / `apple-hig` / compose 系列用户级技能），且不入仓、不可审计、不可随门禁演化。

**决策：新增专职移动端 agent 花荣（`huarong`），主题闭集按交付面扩展为 `xwing-mobile`，并把两份技能重写为**该 agent 自己的**平台技能（`agents/huarong/skills/`），只保留命令与平台事实。**

### 为什么新增主题而不是加字段

移动端代码与 xwing 同仓（monorepo），按 ADR 0009 的「主要改动落在哪个仓」口径本应归 `xwing`。这里做的是**显式例外并写进契约**：切分维度从「仓库」改为「交付面」，因为承接专家、改动规则、验收设备、交付节奏四项全部独立。例外必须可见——否则下一个主题扩展会以为仓口径可以被随意绕过。

`xwing-mobile` 的判定特征是可观察的端：iOS / Android / iPhone / iPad / 安卓 / 模拟器 / 真机 / 装机 / TestFlight / APK / 安装包。裸词「App / 客户端 / 手机」是弱信号，走一条新切分问句：「是在手机上装的 App 里，还是浏览器 / 桌面客户端里？」

### 技能落地形态

| 上游（需求方机器） | 本仓 |
|---|---|
| `ios-client-development/SKILL.md`（带 frontmatter，164 行） | `config/hifox/agents/huarong/skills/ios-client/SKILL.md`（无 frontmatter，60 行） |
| `android-client-development/SKILL.md`（183 行） | `config/hifox/agents/huarong/skills/android-client/SKILL.md`（90 行） |
| 两份 `system-prompt.md` | 丢弃：与 `SKILL.md` 重叠，且与 agent 指令的职责（任务模式、门槛、任务存档、汇报格式）冲突；常驻部分收进 `config/hifox/agents/huarong/instructions.md` |
| 两份技能共有的流程内容（迭代闭环、两轮交付、反模式、完成定义、需求方触发语表） | 丢弃：这些是**一个 agent 的行为**，只在 `instructions.md` 写一次，技能不重复 |
| `ios-client-development` 里「安装三个用户级 skill」 | 删除：仓内 `apps/ios/docs/agent-development-guide.md` 已是权威，装不装外部技能不应成为开工前置 |

**为什么是两个技能、为什么放在 agent 目录下**：两端命令面互不相干（xcodebuild / simctl vs gradlew / adb），合一份会让每次只改 iOS 时把 Android 命令也读进上下文；但它们只有一个消费者（花荣），所以按 AGENTS.md 的技能目录分工落在 `agents/huarong/skills/`（归属即位置），不进根级 `skills/` 共享命名空间——那里的目录名带 `xwing-` 前缀是跨配置集合的共享约定，单消费者内容放进去会假装通用。

**技能只剩平台事实**：命令与参数、设备解析、生成器产物映射、装包诊断、打包与验签。流程、门禁、事实源裁决顺序、验证要求（含「只跑定向用例」「零漂移才算过」）、反模式、触发语表全部归 agent 指令——同一事实只有一处权威，改流程不用同时改三份文档。技能与仓内文档冲突时以仓内文档为准；视觉与交互的完整硬约束仍以 `apps/*/docs/` 为准，技能只留会直接导致返工的平台常量（品牌绿、token / 字体单一来源、金样管线）。

**事实校正**（技能重写时按仓库现状钉死）：

- 工作根统一为 xwing 检出，不再是独立仓库名。
- 集成分支不写死：仓内文档的 `feature/mobile-client` / `feature/android-client` 已分别落后 `develop` 307 / 388 个 commit（2026-11-18 实测）。技能与指令只写**判定方法**（`git rev-list --count origin/develop..origin/<branch>`，为 0 就用 `develop`，不为 0 先问需求方），不写会立即过期的人名。
- 依赖版本不抄数字：Android 侧指向 `gradle/libs.versions.toml`，避免 AGP / Kotlin / Compose BOM 升级后技能说谎。
- 设备序列号不写死：真机以 `adb devices` 现场核对；模拟器 AVD 名与资源参数保留（那是稳定性前提，不是身份）。
- 「必装用户级 skill」删除，改为 `preflight.sh` 的 `ios` / `android` surface 报工具事实。
- 「主开发分支 `feature/mobile-client`」「两轮证据」「完成定义」等流程断言搬进 `instructions.md` 的 P8 / P8.1，技能不再持有第二份。

### 环境预检

`xwing-task-runtime` 的 `preflight.sh` 增加两个 surface：`ios`（`xcrun`、`xcodebuild` 必需，`xcodegen` 可选）与 `android`（`adb`、JDK ≥ 17 必需，`emulator` / `aapt2` / `apksigner` 可选），并让它从 `ANDROID_HOME` / `ANDROID_SDK_ROOT` / `~/Library/Android/sdk` 兜底解析 SDK 工具——Android SDK 常常不在 PATH 上；`aapt2` / `apksigner` 藏在版本化的 `build-tools/<ver>/` 下，按版本号数值取最新（macOS 的 BSD `sort` 没有 `-V`）。回归用例 `test/xwing-task-runtime/preflight.test.mjs` 用沙箱 PATH（无 SDK 工具泄漏）钉住四条：缺工具必然报 MISSING、未知 surface 退出码 2、必需工具缺失退出码 1、`ANDROID_HOME` 解析与最新 build-tools 选择。

## Considered Options

- **继续由林冲兼办移动端**：拒。两端规则集不相通，单 agent 闭环的前提是规则能在上下文里同时成立；把 27k 字符的桌面端指令与两份移动端技能叠在一起只会让每次开工都在找事实源。
- **一个 agent 只管 iOS、另一个只管 Android**：拒。两端共享同一契约（同一 API 字段、同一份文案、同一套状态机），跨端一致性是最高频的返修来源；拆成两个人要么互相等待，要么各修一半。单 agent 双端、由需求方按端排期更稳。
- **按「仓库」口径硬塞进 `xwing`，只在委派映射里分流**：拒。委派映射分流的是人，不是契约；`reject.sh`、吴用词表、台账枚举都以 topic 单键为口径，隐式分流会让「派了但没人接」重新变成可能。
- **移动端技能放根级 `skills/`（跨 agent 共享源）**：拒。只有花荣一个消费者，根级 `skills/` 是跨配置集合的共享命名空间（目录名带产品前缀的前提就是「通用」），放单消费者内容会让后来者误以为它可复用，还要多一层 `dir: @/skills/...` 间接。
- **合成一个 `mobile-client` 技能**：拒。iOS 与 Android 命令面不相交，合一份会让单端改动的上下文里混进另一端的整段命令。
- **保留上游的 `system-prompt.md` 一并入仓**：拒。它是同一事实的第二份表述，随技能演化必然漂移；agent 指令才是常驻面的唯一处。

## Consequences

- **主题闭集新增一项 `xwing-mobile`**，需同步：忠义堂 condition 分支（含 `reject.sh` 闭集文案与 `workflow.yaml` description）、吴用路由表（`instructions.md` §1.0）、任务委派映射（§4）、判定词表（`instructions.md` §1.0 + `skills/topic-resolution`）、台账主题枚举（`skills/ledger` §6）、`GLOSSARY.md`「主题闭集」定义、裴宣自省花名册（`skills/reflection` 的 SKILL.md 与 `scripts/collect.mjs` 的 team 表）。漏任一处即「派了没人接」或「新主题判不出来」。
- **平台指针**：`super-agent/capabilities/platforms/hifox/huarong.yaml` 必须与 `config/hifox/agents/huarong` 双向对应（ADR 0023）；`superctl capabilities --check` 强制。
- **部署记录**：新 agent 在 `environments/hifox-prod` / `hifox-staging` 都没有 `state.yaml` 条目，首次 `deploy --apply` 时创建；在部署前 `capabilities --check` 会报「声明了但没部署」。
- **两个移动端技能成为仓内资产**：`apps/ios/docs` / `apps/android/docs` 变更后需要回头核对技能里的命令是否失效；流程类变化只改 `instructions.md`。仓内文档与技能的优先级不写进技能（agent 指令 §2 已声明「以仓库为准，发现不一致在汇报中点名」），避免三份文件各自复述一遍。
- **林冲退出移动端交付**：`instructions.md` 共 7 处——§1 能力表（改「Rust CLI / 移动端（协同）」）、§5 P0 改动类型（移出 iOS）、§6 P1 bug 环境清单、§7.2 影响面（改为「跨端契约时另列移动端影响」）、§9.7 改名「Rust CLI 与移动端协同」并写清交付归花荣，以及 §13.4 验收清单与 §17 角色切换纪律里删掉「iOS 改动由本 agent 取证」的旧表述。技能与其余流程不动。`xwing` 主题的描述里不再保留独立的 iOS 交付项。
- **花荣不接管打包与发布授权**：签名 / TestFlight / release alpha 的执行属任务面，发布与部署仍需用户单独授权。
