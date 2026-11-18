# iOS 客户端：命令与平台事实

`apps/ios` 的命令面、设备面与产物清单。流程、门禁、裁决顺序、验证要求、反模式在 agent 指令里，不在本文件。

## 工程

- `apps/ios/Hifox/project.yml` 是工程源：改 sources / settings / 资源清单后 `xcodegen` 重生成 `.xcodeproj`；`GENERATE_INFOPLIST_FILE: YES` 叠加手写 `Info.plist`（改成 NO 会让 Release archive 丢 `CFBundleIdentifier`）。
- `project.yml` 显式列举 sources：新增资源目录必须加进去，否则不进 bundle。
- `MARKETING_VERSION` / `DEVELOPMENT_TEAM: 4RSTJKUL57` / `PRODUCT_BUNDLE_IDENTIFIER: cn.hifox.mobile` 都在这一个文件里。
- 品牌绿 `oklch(0.723 0.219 149.579)`（≈ `#00C65A`，定义在 `packages/ui/styles/color-presets.ts`）落共享 token，亮暗两态都验；不散写色值。
- 视觉与交互的完整硬约束（系统组件优先、列表去分割线、错误提示不用大红块、深色 / Dynamic Type / 键盘遮挡）在 `apps/ios/docs/agent-development-guide.md` §6，改 UI 前先读。

## 构建与测试

```bash
swiftc -parse <file>                                              # 语法快检
cd apps/ios/Hifox && xcodebuild -project Hifox.xcodeproj -scheme Hifox \
  -destination 'platform=iOS Simulator,id=<UDID>' build
xcodebuild test -project Hifox.xcodeproj -scheme Hifox \
  -destination 'platform=iOS Simulator,id=<UDID>' \
  -only-testing:HifoxTests/<X> -resultBundlePath /tmp/<name>.xcresult
```

- 代码没变时可用 `test-without-building`；**改过测试文件必须先 `test` 重编**——否则跑的是旧二进制。
- UI 测试截图用 `xcrun xcresulttool export attachments` 导出，不要另写截图逻辑。

## 设备与取证

```bash
xcrun simctl list devices | grep Booted                        # 先看在跑的，复用热的
xcrun simctl install <UDID> <app>
xcrun simctl launch <UDID> cn.hifox.mobile
xcrun simctl io <UDID> screenshot /tmp/<name>.png
```

- 绝不新建设备；多台 booted 时 10 分钟级墙钟属正常，先查争用再怀疑测试挂死（`apps/ios/docs/simulator-test-contention-notes.md`）。
- 真机调试：局域网 IP + 启动参数 `-HIFOX-LOCALTEST-BASE-URL <url>` 与 `-cn.hifox.mobile.session.server-environment localTest`（`apps/ios/docs/device-debug-guide.md`）。UI 测试 runner 受 ATS 限制，不要在 runner 里预检 http 连通性。
- 临时 UI 探针：文件头写 THROWAWAY → 注册进 `project.yml` → `xcodegen` → 跑完删除探针并还原 `project.yml`。不要为截图改已入库的冒烟测试。

## 本地测试栈

```bash
make ios-setup     # xcodegen + SPM 解析 + 复用/预热模拟器 + 探测 :5697
make test-seed     # 幂等造数
make test-reset    # 重建栈与数据
```

docker 栈在 `:5697`；UI 冒烟账号 `seed-admin@example.com` / `888888`。

## 金样与文案管线

| 生成器 | 产物 |
|---|---|
| `scripts/extract-ios-visual-assets.mjs` | `FileTypeIconAssets` / `IconLibraryAssets` |
| `scripts/extract-ios-locales.mjs`（+ `extract-ios-locales-additions.json` 的 iOS 专属键） | `Localizable.xcstrings` |


## 打包

已知分发踩坑，每次出包逐条复验：图标 alpha 通道、iPad 方向声明、出口合规键、zh-Hans 登记、Sentry dSYM stub（三条构建路径都要验静态链接完好）。
