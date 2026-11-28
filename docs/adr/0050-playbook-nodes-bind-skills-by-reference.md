# 0050：作业法的节点名即协议，实现由同名技能提供

ADR 0049 把「怎么干」从手册里抽成作业法，但它没回答作业法里的每个节点由什么承载具体操作。盘点下来，节点名（`## 阶段` 里那条粗体小标题）只是散文，作业法引用技能是 `[[skill:]]` 纯文本——**没有任何一层校验，也没有任何一层注入**。于是「自审」这个节点究竟用哪套评审方法，取决于模型自己在技能目录里按 description 挑，这正是框架 ADR 0024 否掉「用技能承载流程」的同一个理由，只是下沉了一层。

同时存在三个互相冲突的事实：

1. `config/` 侧的部署 agent 早就在用 `skill.yaml` 的 `dir:` 做技能引用（`@/vendor/v-skills/skills/architecture-fit-review`），本地运行时侧没有对应物。
2. omp 的技能发现是**目录级**的（`customDirectories` 收技能根的父目录），挂单个技能目录会被 `NONE` 掉——实测。所以「按路径取一份」在写法上能照搬 x-deploy，在**收敛**上必须多一步 `includeSkills`。
3. `~/.agents/skills` 对作业 agent 是全量可见的（`skills.enableAgentsUser` 默认 `true`），60+ 个人工具技能都在发现面上；而本仓项目约定写着「`worktree-dev` 不对作业 agent 开放」——规则靠文档，机制没拦。

**决策：**

- **节点名即协议；技能是可选补充**。作业法的每个阶段标一个机器可读锚点（`{#review}`），注入时渲染成行内代码里的协议名。**节点正文自己必须够用**——多数阶段（目标、计划、实现、自验、提交）本来就不需要技能；存在同名技能时它才是这一节点的补充实现，**不存在不报错、不影响派活**。判据：技能提供「怎么做」的额外细节，不承担「这一步存在」的责任。
- **作业法正文不写技能名**。写进去就把实现钉死了，「换一份实现」得改作业法，正好破坏这条不变量；按协议名让模型在同名技能里找，换技能即可，作业法一个字不动。
- **回流用 `[[node:<id>]]`**，注入时渲染成节点标题（「实现」）。这修掉一个实测缺陷：`dev-loop` 引用 `investigate` 后，一份注入里有多份作业法正文、各自从 1 开始编号，「回第 4 步」根本指不明白。
- **检查器只校验语法与落点**：锚点 id 不重复、`[[node:…]]` 有对应的 `{#…}`。**不校验同名技能是否存在**——那会把「节点正文已经够用」判成错误，逼每个阶段凭空造一个技能。两份实现（`checkPlaybooks` 与 `check-registry.mjs`）跨语言重复是刻意的：检查器零依赖，不能 import Go 包。`[[skill:]]` 若出现在作业法里，仍校验存在性与挂载。
- **组合是技能引技能**：`review` 的正文用 `[[skill:code-review]]` 与 `[[skill:test-review]]` 指向两轴，产物形态（四档结论、字段、回流方向）留在作业法里——技能只提供「怎么审」，不定义「交什么」。
- **`skills.yaml` 增「技能引用」形态**：`@/` 锚在 **`work_dir`**（agent pane 的 cwd，本实例 = `hifox-configuration/`，与 x-deploy 的 `@/` 指向同一个目录）；裸名仍指 `capabilities/skills/` 共享池，语义不变、零迁移。不另设配置键——技能引用写的就是「相对 agent 干活的那棵树」的路径，与它手上的相对路径直觉一致。路径不许越出 `work_dir`，`..` 一律视为错误。
- **`instance_dir` 不动**。它是运行时身份（`StableBin` = `<root>/bin/superctl` 是受管 serve 的唯一判据），不是路径锚。两件事绑在一起会让「改技能路径 → 实例被判 dev → serve 起不来」。
- **收敛靠 `includeSkills`，且由声明派生**。挂外部根时，`includeSkills` = 该 agent 声明的技能名集合；**声明为空却挂了根 = 报红**（omp 的空名单是「全部可见」，这个默认必须由生成侧兜住）。`skillfs.go:16` 的注释「不用 includeSkills」是旧结论，按需改写实现而非维持。
- **外部技能必须能派到执行节点**。`pushMaterial` / `skillArgs` 共用一份映射 `materialRel`：实例内的根沿用 `capabilities/<rel>`，实例外的按 `work_dir` 映射成 `<rel>`；两侧都不允许 `..` 进 `Join`（原来的 `Rel(capabilitiesDir, …)` 会让外部根静默写到物料目录之外，实测落成 `/state/jobs/<run>/.agents/skills`）。
- **可见性边界**：`enableAgentsUser: false`（机器上的个人技能不可见），`enableAgentsProject: false`（被开发仓库自己的 `.agents/skills` 也不自动可见，`worktree-dev` 那条约定由此第一次成为机制）。**`includeSkills` 是全局按名过滤**：启用后 `.claude/skills` 里的技能同样必须把名字写进 `skills.yaml` 才可见——源开关保持默认 `true` 也不够。实测：清单只写 `review` → 目录只有 `review`；把 `migration-safety` 也写进去 → 两者都在。
- **外部技能只校验不注册**：扫描结果进独立的 `ExternalSkills`，跑 frontmatter / kebab-case / 重名四道检查，但不进 `reg.Skills`、不上 `--roster`、不能被裸名引用。重名必须硬报红——实测两个根各一份同名技能时 omp 静默生成 `dupB/test-review` 与 `test-review`，而带前缀的名字在 `[[skill:]]` 里写不出来。

**Considered Options：**

- **节点用 frontmatter 声明**：拒。撞 ADR 0024 写死的「没有元数据面」，且正文与机器声明会各自漂移。
- **作业法里显式写 `[[skill:<key>]]` 绑定实现**：拒（这是最初实现过的版本，已撤回）。显式写实现名等于把 playbook 钉在某一份技能上，「同一 playbook 换实现」必须改 playbook。
- **检查器强制每个锚点都有同名技能**：拒。实测本仓 10 个锚点里只有 1 个（`review`）有同名技能；强制会让另外 9 个简单阶段（目标、计划、实现、自验、提交）被迫造一个空技能。**技能是节点的补充，不是节点的前提**——节点正文自己就得能完成任务。
- **`@/` 锚在实例根 + 允许 `..`**：拒。实例根里没有技能，于是**每一处**引用都要 `..`——一个边界被 100% 的引用穿过就不提供任何保证。而且 x-deploy 的 `@/` = 仓库根（`artifacts.mjs:73-80` 的 `assertWithinRoot`），同前缀两义会分裂心智模型。
- **`@/` 锚在实例根 + 第二个前缀表示仓库根**：拒。两个前缀与「一个根」的诉求相反。
- **把 `.agents/skills` 整个当发现根、用 `ignoredSkills` 逐个排除**：拒。`npx skills` 之后往目录里再装十个就漏了——收敛应该是「声明即可见」，不是「列出不许见的」。
- **外部技能不进节点物料**：拒。用户明确要求可派到执行节点；`Rel(仓库根)` 映射让这件事零额外语法。
- **`instance_dir` 改到仓库根**：拒。见上，会动受管 serve 的判据，且 `check-registry.mjs` 里 8 处硬编码 `join(root, "super-agent", ...)`。

**Consequences：**

- 框架侧要改：`skills.yaml` 双形态解析、`SkillMounts.Include`、`OverlayContent` 输出 `includeSkills`、`pushMaterial` / `skillArgs` 共用映射函数、外部技能扫描与检查。属 `vendor/super-agent`，走子模块流程。
- 实例侧要改：安道全 `skills.yaml` 声明 `review` / `code-review` / `test-review`；新建 `capabilities/skills/review/SKILL.md`（只写仲裁与合并规则，不复述两方维度名——重复正文门禁拦 ≥24 字相同行）；`code-review` 需进仓库（它不在 `vendor/v-skills`，来源 `mattpocock/skills`）；`check-registry.mjs` 扩扫 `playbooks/` 并与 Go 侧同步一份跨根重名检查（零依赖，跨语言重复是有意的）。
- 作业法的「薄」不是缺陷：命令与项目事实归项目约定与仓库 `AGENTS.md`，抄进作业法就是第二份会腐烂的真值。`dev-loop` 2095 字节、7 阶段、1 个分支 + 1 个回流是完整拓扑；**不需要流程图**（判据：分支点 ≥3 或存在跳过路径才画，如 linchong 的 11 阶段 2 门槛 2 回流）。
- 已识别的组合作用域缺陷留待下一轮：`dev-loop` 引用 `investigate` 后，后者的停损原文「任务实际要求动手改文件 → 交回主控另派」与 `dev-loop` 的作业目的字面冲突；展开后三段各有从 1 开始的编号，「回第 4 步」只能靠位置推断。
