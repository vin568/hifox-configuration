# xwing-deploy Acceptance Suite

本仓（消费方）持有的 xwing-deploy 黑盒验收套件。断言只基于消费方可观察行为：命令退出码、stdout 契约文案、state.yaml 内容、经任务 CLI 读回的远端资源状态。不 import 被测代码，不复用其测试助手。

## 运行前提（环境变量）

| 变量 | 含义 |
| --- | --- |
| `HIFOX_SERVER_URL` | task-local 后端地址（如 `http://localhost:20000`） |
| `HIFOX_TOKEN` | task-local session JWT（`auth.env` 的 `TASK_ENV_API_TOKEN`） |
| `HIFOX_ORGANIZATION_ID` | task-local 组织 ID（manifest） |
| `HIFOX_E2E_SPACE_ID` | 验收用 Space（General） |
| `HIFOX_CLI_BIN` | 任务 CLI 绝对路径（编号身份）；缺省取 PATH 上的 `hifox` |

另需 `node ≥ 18`、`yq`（doctor 场景）。

## 用法

```bash
# 全量验收（--keep 推迟远端清理，供截图；report.json 含清理所需 ID）
node scripts/run-acceptance.mjs --candidate <sha> --repo <xwing-deploy-worktree> --keep --out <dir>

# 截图完成后按报告清理远端资源
node scripts/run-acceptance.mjs --cleanup-report <dir>/report.json
```

退出码 `0` = 全部场景通过且远端清理成功；非 `0` = 存在失败、中断或清理问题（report.json 的逐场景结果与 `cleanupProblems` 可见）。

## 场景清单（消费方契约）

场景与 `scripts/run-acceptance.mjs` 的注册表一一对应（编号即执行顺序）：

1. `validate`：fixture 根离线校验通过。
2. `create`：`create --apply` 落地 crew / 两 agent / Space skill / Agent skill，state.yaml 注册 ID，远端名称带后缀，模板变量已渲染，仓库清单 replace-all 语义成立。
3. `doctor`：自检通过。
4. `plan-no-mutation`：`plan` 只预览不写 state。
5. `apply-drift`：新增 agent、crew 角色变更、skill 附属文件删除、仓库清单收缩后 `apply --apply` 一次收敛。
6. `apply-idempotent`：二次 apply 幂等（ID 不变、无漂移输出）。
7. `pull`：快照文件落盘。
8. `backup-restore`：备份目录成形；原地恢复还原被破坏的 prompt；`--to` 异地恢复物化外部引用。
9. `workflow-lifecycle`：workflows-only 根上 apply 一次成型（in sync）→ 引擎读回名称 / deployed / lock → 二次 apply 幂等 → 删除配置后 `prune workflows` 归档并注销。

10. `automation-lifecycle`：主 fixture 自带 automation——apply 与 agent 同轮首发（state 注册 + 引擎读回 title/status/assignee/trigger）→ 二次 apply 幂等 → cron 漂移经 trigger edit 收敛 → 删除配置后 `prune automations` **硬删**并注销。

## 维护规则

- 场景与断言钉在本仓：CLI 契约合法演进时，在此处显式更新（这是有意的契约评审点），不要为了“让 dev 过门禁”悄悄放宽。
- 新增场景必须保持消费方视角：只准断言 README / guide contract 声明的可观察行为。
