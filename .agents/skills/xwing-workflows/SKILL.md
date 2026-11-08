---
name: xwing-workflows
description: Create, inspect, update, trigger and delete xwing workflows entirely without the GUI — author workflows as declarative graph.yaml + shell block sources (compiled deterministically by xwing-deploy), adopt existing platform workflows via capture (graph.json or decompile to graph.yaml), discover block types and field structures via the workflow catalog, deploy with apply (workflow import, one-shot), iterate via archive-and-reimport, and operate scheduled runs. Use when authoring or operating workflows in a repository managed by xwing-deploy.
---

# xwing workflows（无 GUI 全生命周期）

本技能让你在命令行完成 workflow 的增、删、改、查、触发。权威契约：`./xwing-deploy guide workflows`（随 CLI 版本分发）；块内部字段真值：`<cli> workflow catalog`。本文只固化跨版本稳定的方法与陷阱。

## 0. 源形态怎么选（每个 workflow 二选一、互斥）

| 场景 | 源形态 | 编辑器与回路 |
|---|---|---|
| 从零创建（**默认推荐**） | `workflows/<key>/graph.yaml` + `*.sh`（编写型） | 编辑器是本仓库（PR 评审）；UI 锁定；漂移=报警 |
| 收编 Space 里已存在的 workflow，且想留在 UI 里维护 | `capture`（默认 --form graph → graph.json，捕获型） | 编辑器是 UI；capture 回收 |
| 平台配好的 workflow 想转成仓库维护 | `capture --form yaml`（语义反编译成编写型） | 之后同编写型 |
| 在 UI 里试验过、再转正 | 同上 | — |

一个 workflow 目录同时出现 `graph.json` 与 `graph.yaml` = 装载期报错。同步模型：声明式单向（config → 远端），反向是显式采纳（capture），不是自动同步。

## 1. 编写型：graph.yaml

图骨架是纯数据；function 块的命令式代码住在同目录 `*.sh`（图里只写文件引用，加载时编译内联——.sh 是代码唯一宿主）。管线（validate/plan/apply）加载时确定性编译，**不产出也不提交 graph.json**。

```yaml
# graph.yaml
schedule:
  cron: "0 */3 * * *"          # 引擎没有「每 N 小时」类型，一律 cron
  timezone: Asia/Shanghai

blocks:
  - id: check
    name: Check
    shell: check-update.sh     # 块源引用（与 graph.yaml 同目录的 .sh），不内联
    timeout: 600000            # 可选，默认 600000ms
  - id: route
    name: Route
    condition:                 # 行 id 不得带块 id 前缀（陷阱见 §4）
      - { id: test, when: "JSON.parse(<check.result>).action === 'test'" }
      # else 兜底行编译器自动补；也可显式声明
  - id: tester
    name: Tester
    agent: ci-tester           # 配置键：apply 解析为该环境 state 的 UUID；
    work: |                    # 字面 agent:<uuid> 透传（引用非本配置集合的 agent）
      xwing-deploy 发布验收轮次。Check 输出：<check.result>。
  - id: anything
    type: <engine 块类型>      # raw 逃生舱：未知/异构块原样透传
    subBlocks: { field: 值 }   # 标量 → short-input 简写；或 {id,type,value} 全形

edges:
  - { from: schedule, to: check }
  - { from: route, to: tester, branch: test }   # branch 对应 condition 行 id
```

```bash
./xwing-deploy validate --environment <env>      # 编译 + 全部校验（无需 graph.json）
./xwing-deploy plan --environment <env>
./xwing-deploy apply --environment <env> --apply # import 一次成型（无 UI）
./xwing-deploy build <key> --out /tmp/g.json     # 调试转储编译产物（别提交、别写进 config 目录）
```

## 2. 捕获型与收编

```bash
# 捕获型（graph.json 为源，编辑器是 UI）
./xwing-deploy capture --kind workflow --id <uuid> --key <key>
# 语义反编译为编写型：schedule→cron、code→*.sh、行 id 净化、executor 反解配置键、未知块 raw
./xwing-deploy capture --kind workflow --id <uuid> --key <key> --form yaml
```

捕获型要 UI 可编辑：`workflow.yaml` 写 `lockUiEdits: false`（per-workflow 覆盖环境默认；UI 建的实例不受 HF-15237 影响，apply 可增量推送）。

## 3. 块目录（引擎全量块类型的字段真值）

```
node scripts/catalog-doc.mjs list [过滤词]        # 块类型清单（type/name/category）
node scripts/catalog-doc.mjs view <blockType>     # 单块全量：字段/控件类型/必填/默认值/选项/bestPractices
node scripts/catalog-doc.mjs options <type> <字段> # 动态选项
```

脚本自动从 `environments/*/environment.yaml` 解析 cli.app 与 spaceId（可用 `--cli`/`--space` 覆盖）。**不要凭记忆写块字段**——先 `view`。

## 4. 关键不变式（踩过坑的实证）

- **canonical 块 id**：graph 里的块 id 是规范身份，import 在每个环境重新分配；部署器按 `(type, name)` 翻译。块名不要重复（重名会被自动改名 N）。
- **condition 行 id 不得带块 id 前缀**（如块 `b-route` 的行 `b-route-test`）：引擎 import 重映射 `{blockId}-` 前缀行 id（连同 edge 句柄），部署器翻译不回 → 恒假漂移。无前缀行 id 原样幸存；`capture --form yaml` 会自动净化。
- **条件路由三件套**：行数组 + 分支边句柄 `condition-<行id>` 严格配对；兜底行 `title: else` 空值。
- **function(Shell) 输出语义**：块 `result` = 整个 stdout。脚本做 stdout 卫生：`exec 3>&1 1>&2` 后诊断走 stderr、结构化 JSON `printf … >&3`。
- **引用语法**：`{{VAR}}` = workspace 变量（执行期绑定，graph 只存占位符，密钥不落仓）；`<block.field>` = 上游块输出（块名 normalize：小写去空白）。secret 类字段只准整串 `{{VAR}}` 引用。
- **引擎自管字段**（`webhookId`/`triggerPath`/`triggerConfig`/`triggerId`、`webhookUrlDisplay_*`、`*-tool-N-*`）不参与漂移对比，声明侧写值无效（raw 块里会被剥除）。

## 5. 生命周期操作

```bash
./xwing-deploy validate --environment <env>          # 离线校验（含密钥扫描；编写型含编译）
./xwing-deploy plan --environment <env>              # 漂移预览
./xwing-deploy apply --environment <env> --apply     # 创建/收敛（import 一次成型 + deploy + lock）
<cli> workflow run <id> --space <space> --async      # 无 GUI 手动触发（id 见 state.yaml）
<cli> workflow runs view <wf-id> <execution-id> --space <space>   # 执行状态
<cli> workflow logs list --space <space> --workflow <wf-id>       # 运行日志
./xwing-deploy capture …                             # 收编（§2）
./xwing-deploy prune workflows --environment <env> --apply        # 归档配置中已删除的 workflow
```

**改图迭代（HF-15237 修复前，针对 API 创建的实例）**：移出 workflow 目录 → `prune workflows --apply`（归档+注销）→ 改源 → 移回 → `apply --apply`（重新 import）。UI 创建的历史实例可直接增量推送。修复后统一为「改源 → apply」。

## 6. 运行时事实

- schedule 触发在 runner 离线时排队（命令 TTL 24h），上线后自动执行；手动 `--async` 同机制。
- 含 function 块的 workflow 定向 personal 可见性 computer 时，函数代码改动需 owner 重新授权（把 computer 可见性翻成 organization/space 豁免）。
- 同一 workflow 部署到多环境 = 多实例多 schedule；需要防并发时在首个 function 块做原子标记守卫。
