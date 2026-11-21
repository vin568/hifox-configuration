# 直连脚本能力声明

一脚本一文件：`<key>.yaml` 指实例根下 `direct/<key>` 白名单脚本（ADR 0019 第 2 级、ADR 0021）。`path` 相对实例根（= `super-agent/`）。

```yaml
key: my-script          # 必须等于文件名主干
name: 人读的名字
description: 一句话用途
path: direct/my-script
```

- 实脚本在 `direct/`，本目录只登记「这个脚本是干什么的、何时用」。
- `npm run check` 两向校验：声明的 `path` 必须存在且可执行；`direct/` 里的脚本必须在本目录有登记——登记表不能落后于目录（框架的 `capabilities --check` 也查这两条，`npm run check` 会先跑它）。
- 白名单本身仍是提示词纪律（ADR 0019 第 2 级），登记表负责让主控知道它、让漂移可发现。
- 目录为空是正常态：不预制占位脚本。
