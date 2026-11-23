# hifox 平台能力指针

一项一文件：`<key>.yaml` 指向本仓 `config/` 里已声明的、部署在 **hifox 平台**上的 agent。**真源仍是 `config/`**，这里只放主控需要的「人读索引 + 派活参数」。

平台目录名 = 平台 CLI 应用名（`environments/*/environment.yaml` 的 `cli.app`）。当前仓只有 `hifox` 一个平台：`config/hifox` 与 `config/apifox` 两个配置集合都部署在它上面，靠 Space 区分（`HF` = HiFox 产研、`AF` = Apifox 产研）。将来会有 `platforms/sharkly/`；远程 ssh 主机不属于平台，是执行节点（ADR 0041）——**别把 hifox 说成「云端」，也别把远程主机说成平台**。

```yaml
key: daizong                                 # 必须等于文件名主干
name: 戴宗                                    # 声明名；可以是 config 声明名的词边界前缀（巴德尔 → 巴德尔 Baldur）
space: HF                                    # 派活默认 Space（Space 标识符，名字前缀歧义时用标识）
org: 86555f76-3f5b-4f7a-93c6-406074110a59    # 平台组织**完整 UUID**；省略 = 用 CLI 当前组织
note: 发布验收（按被测 commit 跑黑盒验收套件）
```

规矩：

- 一项一条，**与 `config/` 双向一一对应**：该平台覆盖的每个配置集合里加 / 删 / 改 agent，必须同步本目录（`name` 指同一身份、任一 `environments/*/state.yaml` 有部署记录）——`npm run check` 强制，`npm run check:live` 再对平台核一遍（ADR 0023；框架的 `capabilities --check` 只管框架不变量，ADR 0002 / 0034）。
- `space` 用 Space **标识符**（`HF` / `AF`），不要写展示名——`HiFox` 同时匹配「HiFox 产研」「HiFox 运营」，CLI 会判歧义。
- `org` 只在跨组织派活时需要；`hifox agent list` 只列当前组织，所以解析平台 ref 前要切到该组织（`HIFOX_ORGANIZATION_ID`）。**写完整 UUID**：`HIFOX_ORGANIZATION_ID=rayfox` 这类 slug 会被平台判 404 `ORGANIZATION_NOT_FOUND`（2026-11 实测），slug 只用于人读——别写进这里。
- `defaults.yaml` 是本平台的**默认派活参数**（`org` + `space`），给「点名了负责人、没派 agent」的建单用；它不是能力指针，`npm run check` 不对它做 agent 对应校验，`capabilities --check` 也不会把它当条目。
- 本目录只放「人读索引 + 派活参数」，不复制平台的声明。
