# 执行节点（ADR 0041）

一个节点是一台**由本地运行时按 SSH 地址寻址**的远端 herdr server：有执行者、没有工作项面，不向任何中心注册（与 hifox Computer 的注册制不同）。节点不跑 super-agent 运行时，也没有自己的在途清单与台账。

## 声明

`capabilities/nodes/<key>.yaml`：

```yaml
key: workbox              # 目录名，稳定身份；SSH 别名换了也不动 state
name: 工作机              # 可选，人读
ssh: workbox              # 本机侧 SSH 目的：别名 / user@host；端口、密钥、ProxyJump 走 ~/.ssh/config
machine: workbox          # 本机侧 herdr saved profile 标签或 ID（herdr --machine）
session: super-agent      # 远端 herdr 命名 session
instance_root: /srv/hifox/super-agent   # 节点上的绝对路径：同一份实例布局的根
state_root: /srv/state/songjiang         # 节点上的绝对路径：结果/brief/progress/report/附件根
work_dir: /srv/hifox                     # 节点上的绝对路径：agent pane 的 cwd（仓库根）
note: 只跑只读调研
```

- 声明文件在本机，但 `instance_root` / `state_root` / `work_dir` 的**值指向节点文件系统**，必须是远端绝对路径；它们可以与本机路径完全不同。`ssh` / `machine` 才是本机侧的寻址与 profile。
- 不写 `ip` / `port` / `user` / `computer`：SSH 配置是这些事实的真源。
- `key` 与 `ssh` 分开：别名可以换，节点身份不能换（在途清单、agent `targets`、台账都按 key）。
- 路径按本地实例的相对位置映射：本机 `instance_dir` 下的相对路径落在 `instance_root`，本机 `state_dir` 下的相对路径落在 `state_root`。节点上要有同一份布局（capabilities/ 技能源、仓库）。
- 在线状态是探测值，不写进声明。

## 节点上可以没有哪些目录

- **`instance_root` 可以不存在**，只要该能力没有声明技能（`skills.yaml` 为空、也没有专属技能目录）。手册由本机读取后整段注入，不依赖节点上的 `instructions.md`；agent 仍能干活。若 agent 声明了技能，派活前会 **fail closed**：缺布局等于缺能力，不能静默派一个没有技能的人。
- **`state_root` 不需要预先存在**：派活时会 `mkdir -p` 结果、进度、brief、report、附件目录；agent 自己负责写 result/progress。
- **`work_dir` 必须存在**：`herdr agent start` 需要 cwd，否则会以 `uv_cwd` 失败。
- 只要有部分技能根存在，缺失的发现根会被过滤掉，不会把不存在的目录写进 omp 覆盖配置；完全没有布局且无技能时，`dispatch` 结果里会带 `layout: missing` 说明。

## 准入（一次性）

```bash
# 1. 远端：安装并常驻 herdr server（launchd/systemd/登录项由节点自己负责）
# 2. 本地：把 SSH 目标存成 herdr saved machine（交互式，可能提示安装/升级远端 herdr）
herdr machine add workbox --label workbox --remote-session super-agent
# 3. 自检
./super-agent/bin/superctl capabilities --check
npm run check            # profile 是否存在、targets 是否引用已声明节点
npm run check:live       # 再探 herdr machine status
```

框架与转发面**从不**安装、启动或重启远端 server；节点重启后 server 要由节点自己回来。

## agent 声明目标

agent 的 `capability.yaml` 用完整集合声明自己能去哪：

```yaml
targets:
  - local
  - workbox
```

缺省 = `[local]`，没有通配。派活时主控显式指定位置：

```bash
superctl dispatch --node workbox --agent shixiu --body "..."
```

派活前会校验 agent 的 `targets` 包含该节点，并按 `herdr machine status` 探测；不可达直接失败。节点只建 `jobs` workspace，不起主控/复盘者。

## 监督

- 状态：serve 对在途 working agent 挂 `herdr --machine agent wait`（60s），只把 done/blocked 注入主控；拓扑类靠巡检与快照差分。
- `tell` / `checkpoint` / `cancel` / `handoff` 都接受 `--node`；同名 agent 出现在多个位置（本机或节点）时必须显式指定。
- report：agent 仍执行 `superctl report`，派活时写的 shim 把内容追加到 `<state_root>/reports/<run>.md`；本地运行时按 60s 抽取并注入。工作中报告最多等一个 wait 超时，状态变化路径即时。
- 上下文测量经 ssh 读远端会话文件；只有 `measured` 可用于自动策略。
