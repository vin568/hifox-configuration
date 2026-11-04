# ADR 0003: DeploymentPlan 的 CLI 命令是 `plan`

- 状态：Accepted
- 日期：2026-11-04

## 背景

旧 CLI 中，`diff` 不是文本差异比较，而是 `apply` 的 dry-run 别名：

```text
hifox-config diff  ==  hifox-config apply   （两者都不带 --apply）
```

这里有两个容易混淆的 “apply”：

- `apply` 是**命令名**；
- `--apply` 是**允许真正修改远端的 flag**。

`diff` 和 `apply` 共用同一实现，`apply` 不带 `--apply` 时也只是打印计划。结果是同一个 DeploymentPlan 有两条命令路径，而且 `diff` 这个名字与“文本差异”强烈相关，语义误导。

当前应用尚未发布，可以接受 breaking change。

## 决策

- 领域概念仍然叫 **DeploymentPlan**（部署计划）/ **Preview**。
- CLI 的规范命令是 **`plan`**：
  ```bash
  hifox-config plan --environment current
  ```
  它只生成 DeploymentPlan，不修改远端，也不接受 `--apply`。
- **`apply` 只负责执行**：
  ```bash
  hifox-config apply --apply --environment current
  ```
  缺少 `--apply` 时直接失败，并提示改用 `plan`。它不再静默退化为 dry-run。
- **`diff` 命令移除**，不再保留别名。应用未发布，不存在兼容负担。
- `create` / `clone` 仍保留 `--apply` 门禁：不带 `--apply` 时打印 provisioning 计划；它们是首次创建命令，不是 `apply` 的预览。
- 文档描述语义时使用 DeploymentPlan/Preview；命令示例使用 `plan` / `apply --apply`。

## 后果

- 预览与执行各有一条明确路径：`plan` 预览，`apply --apply` 执行。
- CLI 与领域语言一致，不再需要解释“apply 命令”和 “--apply flag” 的双关。
- `diff` 的旧脚本会失效；应用未发布，接受该 breaking change。
- dry-run 的输出顺序和内容仍属于可观察行为，测试继续覆盖 `plan`。

## 备选方案

- **保留 `diff` 作为 `plan` 的别名**：减少迁移成本，但会把误导性术语继续留在 CLI。被否决。
- **保留 `apply` 不带 `--apply` 时的 dry-run**：会与 `plan` 形成两条预览路径，继续产生歧义。被否决。
- **把 `apply` 改成默认执行、不再需要 `--apply`**：命令更短，但失去显式 mutation 门禁，风险更高。被否决。
