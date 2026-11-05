# ADR 0005: 双项目布局（Dedicated 与 Embedded `.hifox/`）、宿主项目文件引用与备份恢复策略

- 状态：Accepted
- 日期：2026-11-04

## 背景

`hifox-deploy` 需要服务两类使用场景：

1. **专用配置仓库（Dedicated）**：仓库本身只维护 HiFox 声明式配置（如 `hifox-configuration`），不包含业务应用代码。
2. **宿主项目内嵌（Embedded）**：仓库主体是业务项目或共享 Skill/Prompt 仓库，HiFox 配置只是项目的附加基础设施，且配置中的 Agent 指令或 Skill 需要直接复用宿主项目已有的 `prompts/` 或 `skills/` 目录。

同时，使用者在不同项目中存在两类安装偏好：无 `package.json` 或需要联调 CLI 源码时使用 `vendor/hifox-deploy` Git 子模块；已有 Node 工具链的宿主项目则希望通过 `node_modules`（`npm`/`pnpm`/`yarn`）安装而不污染仓库根目录。

## 决策

### 1. `DeploymentRoot` 与 `ProjectRoot` 双布局模型

- **`Dedicated` 布局**：`DeploymentRoot` 与 `ProjectRoot` 重合（均为仓库根目录 `.`），`config/`、`environments/`、`snapshots/`、`backups/` 位于顶层。
- **`Embedded` 布局**：`DeploymentRoot` 位于 `<ProjectRoot>/.hifox/`，`config/`、`environments/`、`snapshots/`、`backups/` 全部收敛在 `.hifox/` 内，不污染宿主工作区。
- **自动探测规则**：未显式传入 `--root` 时，CLI 优先检查 `<cwd>/.hifox` 是否为目录；若是则以 `<cwd>/.hifox` 作为 `DeploymentRoot`（`ProjectRoot` 为 `<cwd>`），否则回退到 `<cwd>`。显式传入 `--root` 时，若 `basename(root) === ".hifox"`，则 `ProjectRoot` 为其父目录，否则 `ProjectRoot` 等于 `DeploymentRoot`。

### 2. 双安装方式与 `init` 文档注入

- **`vendor/hifox-deploy` 子模块模式**：面向专用配置仓库或非 Node 项目，通过根 `./hifox-deploy` 壳脚本或 `init.sh --install submodule` 驱动。
- **`node_modules` 包模式**：面向带 `package.json` 的宿主项目，通过包管理器安装并由 `npx hifox-deploy` 调用，不生成顶层壳脚本。
- **`hifox-deploy init` 与 `HIFOX.md`**：内置 `init` 子命令，在 `DeploymentRoot` 生成 `HIFOX.md`（避免与项目 `README.md` 冲突），并在 `ProjectRoot/AGENTS.md` 中幂等追加指向 `HIFOX.md` 的章节，向项目开发者与编码 Agent 暴露目录规范、引用语法与编译期变量能力。

### 3. `@/` 宿主引用、整个 Skill 目录同步与编译期模板展开

- **路径解析与安全边界**：
  - 普通相对路径相对于当前资源目录（`crews/<key>/`、`agents/<key>/`、`skills/<key>/`）解析；
  - `@/<path>` 相对于 `ProjectRoot` 解析；
  - 所有被引用文件与目录（含 symlink 解析后真实路径）必须位于 `ProjectRoot` 内部，严禁越界访问宿主机外部文件。
- **整个 Skill 目录引用与多文件同步**：
  - `skill.yaml` 支持 `dir`（默认当前 `skills/<key>/` 目录，可设为 `@/...` 指向宿主 Skill 目录），`contentFile` 默认取 `SKILL.md`；
  - 目录内除 `SKILL.md`、`skill.yaml` 及隐藏占位文件（`.gitkeep`、`.DS_Store`）外的附属文件，通过 `hifox skill files list/upsert/delete` 同步到远端 Skill。
- **统一编译期变量展开**：
  - Crew 指令、Agent 指令、Skill 主正文（`SKILL.md`）及 Skill 附属文本文件统一在校验与部署阶段展开模板变量：`{{agents.<key>.name}}`、`{{crews.<key>.name}}`、`{{skills.<key>.name}}`、`{{crew.name}}`（单 crew 时）、`{{environment}}`。
  - 支持 `\{{...}}` 转义输出字面量 `{{...}}`；未转义且未解析的 `{{...}}` 在 `validate` 阶段报错。

### 4. 备份归档与原地 / 异地恢复的双模态契约

- **`backup`（保留原引用 + 外部文件按原路径归档）**：
  - `backup/config/` 原样保存 `config/`（不篡改 YAML 中的 `@/` 引用）；
  - 所有被 `@/` 引用的外部文件或目录，按其相对于 `ProjectRoot` 的原始路径存入 `backup/external/<relPath>`，并在 `manifest.json` 中记录 `layout` 与 `externalPaths`。
- **`restore` 原地恢复（省略 `--to`）**：
  - 将 `backup/config/`、`environment/`、`snapshot/` 写回当前 `DeploymentRoot`，并将 `backup/external/` 下的文件按原路径写回 `ProjectRoot` 对应位置（完整恢复内部配置与外部被引用文件，不触碰无关业务文件；默认预览，传 `--apply` 执行）。
- **`restore` 异地恢复（传 `--to <空目录>`）**：
  - 目标为脱离原宿主项目的全新空目录；恢复时自动将 `backup/external/` 中的外部文件物化（内联）进 `<to>/config/<set>/{crews,agents,skills}/<key>/` 内部，并将 `<to>/config/` 副本中的 `@/` 路径改写为内部相对路径，生成零外部依赖的自包含配置目录。

## 后果

- 宿主项目接入 HiFox 时顶层只增加 `.hifox/`（以及 `AGENTS.md` 中的文档链接），并可直接复用项目内已有的 `skills/` 与 `prompts/`。
- `Backup` 同时保留了原始 `@/` 引用结构与外部文件快照：原地恢复能 100% 还原原项目引用关系与外部文件内容；`--to <空目录>` 异地恢复则能产出独立可校验的自包含配置包。
