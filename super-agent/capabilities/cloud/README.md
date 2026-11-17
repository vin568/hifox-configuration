# 云端能力指针

一项一文件：`<key>.yaml` 指向本仓 `config/` 里已声明的云端 agent。**真源仍是 `config/`**，这里只放主控需要的「人读索引 + 派活参数」。

```yaml
key: daizong      # 必须等于文件名主干
name: 戴宗        # 声明名；可以是平台基名的前缀（巴德尔 → 巴德尔 Baldur））
space: HF         # 派活默认 Space（Space 标识符，名字前缀歧义时用标识）
org: rayfox       # 平台组织 slug 或完整 UUID；省略 = 用 CLI 当前组织
note: 发布验收（按被测 commit 跑黑盒验收套件）
```

规矩：

- `space` 用 Space **标识符**（`HF` / `AF`），不要写展示名——`HiFox` 同时匹配「HiFox 产研」「HiFox 运营」，CLI 会判歧义。
- `org` 只在跨组织派活时需要；`hifox agent list` 只列当前组织，所以解析平台 ref 前要切到该组织（`HIFOX_ORGANIZATION_ID`）。
- `--check` 校验每个 key 在 `config/hifox/agents/<key>/agent.yaml` 或 `config/apifox/agents/<key>/agent.yaml` 有声明。
