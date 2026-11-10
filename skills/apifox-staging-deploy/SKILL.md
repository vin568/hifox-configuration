# Apifox Integration Deploy

apifox 集成测试服（workflow `apifox-integration`）的部署契约：触发前检查、参数模板、端口映射、未改动包的处理、失败口径与汇报清单。单一来源，由 yanqing / xiaorang / skadi 以各自实例引用；各 agent 的包面与验收入口路径**不在**本技能内。

## 触发前

- 在包含 `.github/workflows/apifox-integration.yaml` 的 Git 仓库中执行；先 `gh auth status`、`gh repo view` 确认身份与仓库。
- 确认目标是 staging；生产部署必须单独获得明确授权，不得从 staging 授权推断。
- 确认要部署的包与分支；未改动的包给出其当前集成分支（见「未改动的包」）。

## 端口映射

| 用户名 | 端口 |
|---|---|
| vin | 5672 |
| johann | 5674 |
| vano | 5679 |

`SERVER_PORT`、`IMAGE_IDENTIFIER`、`MYSQL_DATABASE`（`apifox_<username>`）取同一用户名的对应值。用户名不在表中：停止并报告阻塞，不要猜端口。

## 参数模板

```bash
gh workflow run .github/workflows/apifox-integration.yaml \
  -f IMAGE_VERSION=ci-test \
  -f IMAGE_IDENTIFIER=<username> \
  -f 'PACKAGES=<build-package>:<branch> tars-server:<branch> tarslib-web:<branch> ws-entry' \
  -f SERVER_HOST=192.168.10.208 \
  -f SERVER_PORT=<port> \
  -f SERVER_PROTOCOL=http \
  -f MYSQL_DATABASE=apifox_<username> \
  -f APP_REGION_LOWER=cn \
  -f PDV=0 \
  -f BUILD_ALL=0 \
  -f GIT_BUILD_REF=feature/explorer-v2
```

| 字段 | 语义 |
|---|---|
| `PACKAGES` | 空格分隔的 `<包>:<分支>` 列表；集成脚本按列表重建这些包，未列出的包复用镜像里的旧产物。分支缺省取 `master`（`<pkg>` 等价 `<pkg>:master`），所以除 `ws-entry` 外每个包都显式写分支。包名必须落在集成脚本闭集内：`admin`、`help`、`tarslib-apidoc`、`tarslib-console`、`tarslib-web`、`tars-server`、`ws-entry`、`explorer-v2`；非法 token 会让构建直接失败（`无效的模块名称`，退出码 1）。 |
| `IMAGE_VERSION` | 测试服用 `ci-test`。 |
| `GIT_BUILD_REF` | 集成打包脚本仓库（apifox-integration）的分支，不是产品分支。 |
| `APP_REGION_LOWER` | `cn` / `global`，按要验证的发行形态取。 |
| `PDV` | `1` 只构建私有化镜像，跳过测试服部署步骤。 |
| `BUILD_ALL` | `0` 只重建 `PACKAGES` 列出的包。 |
| `SERVER_HOST` / `SERVER_PROTOCOL` | 测试服主机与协议；缺省 `192.168.10.208`，另一台是 `192.168.10.207`（只在上游指明目标实例在那台时改用）。不要改写成生产域名。 |
| `LARK_WEBHOOK_URL` | 用 workflow 默认值；禁止在命令或回复中输出 Webhook 地址。 |

### 未改动的包

- 不要留空、不要凭印象猜：用最近一次成功部署记录里的分支（`gh run list --workflow apifox-integration.yaml --limit 5`），并在汇报中说明复用来源。
- **漏列目标包的代价是静默的**：不在 `PACKAGES` 里的包不会报错，只会沿用镜像里的旧产物——部署「成功」、验收却跑的是旧代码。触发后必须确认构建步骤的「需要编译的模块」列表里出现本次要交付的包；漏了就把它补进 `PACKAGES` 重新触发，并在汇报中说明原因。
- 构建包（如文档站 `explorer-v2`）与运行包（`tars-server` / `tarslib-web` / `ws-entry`）的具体清单由消费者指令给出，本技能不预设。

## 触发后

1. 取新 run 的 URL，跟踪到结束。
2. 失败：看失败步骤日志（`gh run view <id> --log-failed`），总结原因后报告；**不擅自重跑**。
3. 汇报必须包含：测试服务器地址、各包分支、run URL 与最终状态、实际部署的包列表。
4. 测试服站点入口路径由消费者自持（文档站 `/apidoc`、主应用 `/main` 等）；本技能不规定入口。

## 失败口径

- 触发失败 / 构建失败 / 部署步骤失败要分开表述；只有 run 成功且目标地址可达才算部署完成。
- 部署成功不等于功能正确：功能结论只能来自验收，不来自构建日志。
