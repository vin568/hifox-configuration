# 运行时实例住仓根，xwing 部署配置收进 `.xwing/`

2026-11 重排本仓目录：宋江运行时实例的物料（`capabilities/`、`direct/`、`scripts/`、`bin/`、`untracked.yaml`、`superctl.example.toml`）从 `super-agent/` 提到仓根，仓根即实例根；xwing 部署配置（`config/`、`environments/`、`skills/`、`snapshots/`、`backups/`）收进 `.xwing/`，由根 `x-deploy.yaml` 的四个目录键声明（embedded 布局，工具 ADR 0027）。理由是框架本来就把 `super-agent/` 当旧布局兼容（`bin/superctl`、`<root>/capabilities`、`<root>/direct` 才是它的缺省），而两套 `@/` 锚（部署根 / `work_dir`）本来就都落在仓根——扁平化后「仓根 = 实例根 = 部署根」成立，根目录只剩一种身份，部署载荷有唯一容器。

## Consequences

- `vendor/super-agent` 的 `capabilities --root` 改为把 `@/` 锚在 `--root` 本身；旧布局（实例根是仓根的子目录）必须显式传 `--path-root`。不靠目录名探测。
- `scripts/check-registry.mjs` 从 `x-deploy.yaml` 读 `configDir` / `environmentsDir`，不再把 `.xwing/` 或 `config/` 抄成常量。
- 声明里的共享技能引用改成 `@/.xwing/skills/<名>`（`@/` 锚仍是仓根）。
- 运行时配置（`~/.config/super-agent/superctl.toml`）的 `instance_dir` / `runtime_cmd` 指向仓根与 `bin/superctl`；`work_dir` 不变。
- 历史 ADR 的路径按当时事实保留、不重写：0016、0021、0023、0025、0026、0029、0034、0036、0037、0043、0049、0050 里出现的 `super-agent/…` 与 `config/…` 以本文为准。

## Considered Options

- **只把 xwing 收进 `.xwing/`、保留 `super-agent/`**：两套布局同时存在，框架的兼容分支永远是主路径，根上仍有两个身份。
- **把实例也收进隐藏目录**（如 `.songjiang/`）：对称，但框架的缺省路径与所有运维脚本都要多绕一层，且实例是主角、不该藏。
- **拆成两个仓库**：运行时与部署配置共享 `@/` 锚、共享 `npm run check`（跨仓对账是本仓知识），拆开要复制一套桥接。
