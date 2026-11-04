# ADR 0004: Snapshot 与 Backup 是支撑产物，不是领域聚合

- 状态：Accepted
- 日期：2026-11-04

## 背景

`pull` 会生成 `snapshots/<env>/`，`backup` 会生成 `backups/<timestamp>/<env>/`。两者都包含远端数据，但性质不同：

- **Snapshot**：远端状态的只读缓存，可以随时重新生成。
- **Backup**：不可变归档，包含 config、environment state、snapshot 与 manifest，用于恢复和审计。

## 决策

- Snapshot 和 Backup 建模为**支撑产物（supporting artifacts）**，不属于核心领域聚合。
- 核心身份事实仍然只来自 `DeploymentState`；Snapshot 不能替代 state。
- Backup 的 manifest 记录 environment、configRoot、organizationId、spaceId、crews、crewId，作为归档元数据，不参与部署决策。
- 保留策略、加密、异地存储属于运维/安全策略，不进入领域模型。

## 迁移策略

**不迁移历史 artifacts，也不在 CLI 中实现 `migrate` 命令。**

- **Snapshot**：不迁移。`pull` 会按当前布局重新生成；旧快照可以直接删除。CLI 不读取 Snapshot 做部署决策，因此没有兼容负担。
- **Backup**：不迁移。Backup 是不可变历史，改写会破坏审计价值。旧备份仍然可用，因为配置加载器支持：
  - 根目录 `crew.yaml`（legacy 单 crew）；
  - 旧的 `config/spaces/...` 路径（`configRoot` 只要求位于 `config/` 内）；
  - 旧 `environment.yaml` 缺少 `configRoot` 时默认 `config/`。
- **新 backup**：使用新的 `config/<config-set>/` 布局；切换目录结构后重新执行一次 `backup` 即可。
- **CLI 能力边界**：不提供 `migrate`。恢复用 `restore`，清理用 `prune`，两者都只做复制/删除，不改写备份内容。
- **Restore**：只写入不存在或为空的 `--to` root，不覆盖现有工作区；复制 `config/`、`environment/`、`snapshot/`，保留原 `configRoot` 和 state UUID；恢复后用目标 root 重新 `validate`。它不是迁移：旧备份的 `configRoot` / legacy `crew.yaml` 原样保留。
- **Prune**：`prune backups` 按时间戳保留最新 N 个；`prune snapshots` 删除孤儿快照或显式 `--environment` 的快照。两者默认预览，必须显式 `--apply` 才删除。
- **Manifest 兼容**：字段、类型和版本规则见 [hifox-dotfile README 的 Backup manifest](../../vendor/hifox-dotfile/README.md#备份-manifest)；`version: 1` 保持不变，新增字段是 additive；`configRoot` / `crews` 缺失或为 `null` 时按 legacy 处理；`crewId` 只保证单 crew 时存在，多 crew / 零 crew 以 `crews` 映射（可能为空）为准。读取方必须容忍缺失字段，不能假设新布局；只有 breaking change 才升级到 `2`，读取方遇到未知版本必须报错而不是按 `1` 猜测。

## 后果

- Snapshot 可以删除并重建，不会影响部署身份。
- Backup 的格式变化需要保持向后兼容；旧备份仍可通过 legacy 根 `crew.yaml` 或旧 `configRoot` 路径读取。
- 领域模型不引入 Snapshot/Backup 的生命周期状态机，避免过度建模。
- 备份完整性（config/state/snapshot/manifest 是否齐全）由 `backup` 命令保证，而不是聚合不变量。

## 备选方案

- **把 Snapshot/Backup 建模为领域聚合**：可以为保留策略、恢复流程建状态机，但会扩大领域范围，且当前没有对应业务规则。暂不采用。
- **在 CLI 中实现 `migrate` 重写旧 Snapshot/Backup**：Snapshot 可以直接重建，Backup 改写会破坏不可变性和审计价值；迁移历史 artifacts 不是领域需求。被否决。恢复/清理由 `restore` / `prune` 承担，不重写历史。
- **为 manifest 单独维护 JSON Schema**：`version: 1` 同时覆盖新布局和 legacy 变体，单 schema 难以既严格校验新 writer、又接受旧备份；当前由字段表、writer/reader 和契约测试锁定。暂不采用；出现外部消费方或 CI 校验需求时，按版本引入独立 schema。
