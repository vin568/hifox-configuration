请在当前 Git 仓库中触发 GitHub Actions 工作流：

.github/workflows/apifox-integration.yaml

要求：

1. 使用 GitHub CLI（gh）触发 workflow_dispatch。
2. 执行前检查 `gh auth status`，并确认当前仓库信息。
3. 通过“用户名”与“端口”的映射表（见附录）找到对应的“端口号”用于下一步操作。
4. 使用以下构建参数（需要按情况替换变量 <...>）：
    - IMAGE_VERSION=ci-test
    - IMAGE_IDENTIFIER=<用户名>
    - PACKAGES="explorer-v2:<文档站分支名|'main'> tars-server:<服务端分支名|'master'> tarslib-web:<前端分支名> ws-entry"
    - SERVER_HOST=192.168.10.208
    - SERVER_PORT=<端口号>
    - SERVER_PROTOCOL=http
    - MYSQL_DATABASE=apifox_<用户名>
    - APP_REGION_LOWER=cn
    - PDV=0
    - BUILD_ALL=0
    - GIT_BUILD_REF=feature/explorer-v2
5. LARK_WEBHOOK_URL 使用 workflow 的默认值，不要在命令或回复中输出 Webhook 地址。
6. 触发后获取新创建的 workflow run，输出运行 URL，并持续查看状态直到构建完成。
7. 如果构建失败，查看失败步骤的日志并总结错误原因，但不要擅自重新触发构建。

建议使用类似以下命令触发：

gh workflow run .github/workflows/apifox-integration.yaml \
  -f IMAGE_VERSION=ci-test \
  -f IMAGE_IDENTIFIE=vano \
  -f 'PACKAGES=explorer-v2:feature/r202607w1 tars-server:feature/r202607w1 tarslib-web:feature/r202607w1 ws-entry' \
  -f SERVER_HOST=192.168.10.208 \
  -f SERVER_PORT=5701 \
  -f SERVER_PROTOCOL=http \
  -f MYSQL_DATABASE=apifox_vano \
  -f APP_REGION_LOWER=cn \
  -f PDV=0 \
  -f BUILD_ALL=0 \
  -f GIT_BUILD_REF=feature/explorer-v2

该部署属于当前任务已授权的 staging 验证流程。只要 Odin 已提供完整的部署参数和明确的 staging 目标，直接执行部署，不要再次等待用户确认。禁止将部署到生产环境的授权理解为已获得；生产环境必须单独获得明确授权。

如果部署参数不完整、目标不是 staging，或可能影响生产环境，必须暂停并向 Odin 报告阻塞原因。

附录：

端口映射表
|用户名|端口号|
|:--|:--|
|vin|5672|
|johann|5674|
|vano|5679|
