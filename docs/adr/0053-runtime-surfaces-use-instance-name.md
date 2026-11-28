# 运行时面用实例名，主控人格名只留在能力内容里

本实例的状态目录、凭证环境变量前缀与服务名历史上都带着主控人格名（`~/.local/state/songjiang`、`SONGJIANG_*`、`songjiang-serve`），那是框架化之前的残留：人格名是用户自定义的实例内容（`capabilities/agents/songjiang/`），可以改名、可以同机并存第二个实例，不该成为运维路径与凭证命名空间的组成部分。框架本身不含这个身份——`scaffold` 的状态目录按 herdr session 派生（`~/.local/state/<session>`），凭证前缀由 init 按主控 key 派生且允许实例覆盖；本实例此前沿用了旧值。现改为：`state_dir = ~/.local/state/super-agent`、`env_prefix = SUPERAGENT`、本机节点 `state_root` 同步，示例配置与节点文档只写通用名，运行服务登记为 `super-agent-serve`。本条取代 ADR 0034 里「状态目录与凭证前缀沿用旧值」的实例选择。

## Consequences

- 状态目录整体搬移（停 serve → `mv` → 改配置 → `mise exec` 起服务）；`retro.json` 里指向旧目录的绝对路径一并改写。存量 ledger / session 记录里的旧路径是历史事实，不重写。
- 凭证环境变量改名（`SUPERAGENT_FEISHU_APP_ID/_SECRET`），值不变；`mise.local.toml` 是本机密钥面。
- 能力注册表里的 `songjiang` key、姓名与手册保持原样：那是人格内容，不是运行时面。
- 旧配置目录 `~/.config/songjiang/` 删除（框架 ADR 0005 已移除该兜底）。

## Considered Options

- **保持存量命名**：状态不搬、前缀不改最省事，但运维面永远混着一个人的名字，第二个实例或换人格时会再遇到同一问题。
- **改框架按 session 派生凭证前缀**：更彻底，但前缀是凭证命名空间、init 已允许覆盖；框架没有写死身份，问题在实例取值，不动框架。
