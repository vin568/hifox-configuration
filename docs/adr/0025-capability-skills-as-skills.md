# ADR 0025：能力技能用标准 SKILL.md，位置即归属

`super-agent/capabilities/skills/` 下原先有三份能力文档（`direct-tiers`、`local-agents`、`platform-dispatch`），是宋江做「亲自跑命令 / 派人 / 派平台活」时的纪律来源。它们一直是**私有形态**：`capability.yaml`（key/name/description/whenToUse）+ `doc.md`，注册表索引里给一条绝对路径，主控要自己判断什么时候去 `read` 那一篇。

实际运行确认这条路两头都不对：

- **看不见**：omp 的技能发现只扫 `.agent/skills`、`.agents/skills`（项目向上 + 家目录）、`~/.codex/skills`、`.claude/skills`、`.github/skills`、`~/.config/opencode/skills`，以及配置项 `skills.customDirectories`。`capabilities/skills/` 不在其中——实测问主控「你能看到哪些技能」，它一个都说不出；反过来它看得见 `~/.agents/skills/` 下那 80 个**开发工具**技能。排除面与需要面正好错开。
- **不通用**：`capability.yaml + doc.md` 是本仓自造的形态。omp 只认 `SKILL.md` 的 frontmatter `name`/`description`（目录名不参与，无 frontmatter 的整份跳过），`config/` 里交给 x-deploy 的技能也是 `skill.yaml + SKILL.md`。形态私有，就意味着这些文档搬不进 `config/`、也不能被别的 CLI 读。

**决策：**

- **唯一载体是标准 `SKILL.md`**（Agent Skills 开放标准）：frontmatter 只有 `name`（kebab-case）与 `description`（用途 + 「何时用」，模型不读正文也能判断要不要读）。正文即方法论文档。不再有 `capability.yaml` / `doc.md`，也没有第二份元数据文件。
- **位置即归属，与 `config/` 同一套逻辑**：
  - `capabilities/skills/<key>/SKILL.md` —— 多个 agent 共享；
  - `capabilities/agents/<key>/skills/<key>/SKILL.md` —— 只给该 agent。
  这与 `config/<集合>/skills/`（Space 共享）对 `config/<集合>/agents/<key>/skills/`（agent 专属）是同一种分法；`config/` 与这里可以互相搬运，但**正文可原样复用、frontmatter 要按目的地的规则改**：远端 `config/` 的技能用人类可读的展示名（`XWing Dispatch Ledger`）且部分没有 frontmatter，本地要求 `name` 是 kebab-case 且等于目录名。
- **不复制，直接把真源目录交给 CLI**：`capability.SkillRoots` 返回「共享根 + 该 agent 的专属根」，写成一份只含 `skills.customDirectories` 的覆盖配置（`state_dir/skills/<agent>/omp-skills.yml`），作为 `--config` 追加到 `agent start`。**不动用户的 `~/.omp`**。多根是必须的：合并两份目录就得复制。
- **归属不再靠声明字段**：原先 `capability.yaml` 里的 `skills: [...]` 取消。技能在哪个目录就是给谁的——多一个声明字段就等于多一个能和文件位置打架的真源。
- **按需拉起的 job agent 也带技能**：`dispatch --local` 起 agent 时同样拼技能参数（共享 + 它自己那份）。三条启动路径（master / retro / job）走同一段装配，谁都不能例外——否则某个 agent 专属的技能只在常驻会话里可见、派活起来就少一份。
- **只对某个 agent 说话的技能归它自己**：`platform-dispatch` 通篇是「你不写正文、你只转发」的主控纪律，朱贵读来正好相反（正文由他写）。这类文档放 `agents/songjiang/skills/`；共享根只留真正对所有 agent 成立的通用方法。判据不是「几个 agent 会看到」，而是「按它做的人是不是同一个角色」。
- **`--check` 守住六件事**：SKILL.md 缺失；frontmatter 解析不出（无 `---`、未闭合、坏 YAML）；缺 `name`/`description`（底层会整份跳过，且不报错）；技能名不是 kebab-case；同名技能在两处声明（底层按 name 注册，重名只有一个能被裸名加载）；目录 key 与 frontmatter `name` 不一致。
- **索引不再给文档路径**：`## 技能` 段只列技能名（frontmatter `name`，与底层注册名同一个）+ 归属（共享/某 agent 专属）+ description 原文（其中的「何时用」正是主控判断要不要加载的依据，不截断）。给路径会诱导模型 `read` 整个文件，绕过技能机制。**目录 key 必须等于 frontmatter `name`**（位置即身份）：名称只有一个真源，目录名不再承担别名职责（上一条的 `--check` 强制）。

**Considered Options：**

- **搬进 `.agents/skills/`（仓根）**：拒。那是**开发 agent**（在仓里干活的 omp/Claude）的技能面，消费者不同；混进去会让开发 agent 看到「宋江的工作纪律」，也让运行时真源依赖 git 工作树的布局。
- **符号链接到 `.agents/skills/<key>`**：拒。会改变开发 agent 的可见面，而 omp 的技能名是全局命名空间——重名时谁赢不由我们决定（实测：重名的那份被加目录前缀，裸名只剩一个）。
- **运行时把 `doc.md` 铺成 `SKILL.md` 副本**（第一版实现，已废弃）：拒。副本是可编辑的第二真源，还要处理托管标记与清理；既然 `SKILL.md` 本身就是标准形态，直接给目录就行。
- **`customDirectories` 只指共享根**：拒。专属技能会被所有 agent 看到，等于没有归属。
- **改用户的 `~/.omp/config.yml`**：拒。全局副作用、多 profile 下互相打脸。
- **在 `capability.yaml` 里声明 `skills:`**：拒。文件位置已经是归属，声明只会与它不一致。

**Consequences：**

- **技能可搬运（正文层面）**：一份 `capabilities/.../skills/<key>/SKILL.md` 的正文可以原样用于 `config/<集合>/skills/<key>/`，反之亦然；差别只在元数据与名称规则——远端要补 `skill.yaml`（`name`/`namingPattern`/`assignTo`，名称是展示名），本地要 frontmatter 的 kebab-case `name` 等于目录名。搬运时改的是这两样，正文不用动。
- **`state_dir/skills/<agent>/` 只剩覆盖配置**：不再是技能副本目录，删掉也不丢内容。
- **frontmatter 是硬约束**：手写技能漏 `description` 时底层静默跳过，`capabilities --check` 是唯一能看见的地方。
- **主控仍看得见 `~/.agents/skills` 的 80 个开发工具技能**：这是 omp 的发现语义（家目录层始终生效）。彻底隔离要给主控专属 HOME，代价大于收益。
- **技能名必须 kebab-case**：`direct-tiers` / `local-agents` / `platform-dispatch` 都合规；新增时用别的字符会被 `--check` 拦下，而不是在启动时静默失效。
- **共享根现在是空的**：三份能力文档全是宋江的岗位纪律，都落在他自己的 `skills/` 下。共享根保留给将来真正跨 agent 的方法（判据见上），空着比塞一份角色专属的文档好。
