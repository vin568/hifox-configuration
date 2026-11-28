# Android 客户端：命令与平台事实

`apps/android` 的命令面、设备面与产物清单。流程、门禁、裁决顺序、验证要求、反模式在 agent 指令里，不在本文件。依赖版本号以 `apps/android/gradle/libs.versions.toml` 为准，不在本文件复述；下面只列环境前提（SDK 平台 / 构建工具 / JDK）。

## 工程

- 工具链：Android SDK `platforms;android-37.0` + `build-tools;36.0.0`，JDK 17+，Gradle wrapper（`apps/android/gradlew`）。
- `adb` / `emulator` 不在 PATH 时：`~/Library/Android/sdk/platform-tools/adb`、`~/Library/Android/sdk/emulator/emulator`。
- 契约文档 `apps/android/docs/parity.md`（§2 硬约束 / §4 分歧台账 / §5 已落地待实现 / §6 已实测）、`parity-audit.md`、`parity-notes/`、`asset-pipeline.md`；工具链与命令总表在 `apps/android/README.md`。
- 颜色只出自 `design/GeneratedTokens.kt`，字号 / 行高只出自 `design/HifoxTypography.kt`；边到边用 `enableEdgeToEdge()`；推送不接 FCM/GMS（走国内厂商通道）；figure-host 由 `packages/ui/visual-host` 构建并经 `node scripts/sync-figure-host.mjs <worktree>/apps/android` 同步。

## 构建与测试

```bash
cd apps/android
./gradlew :app:compileDebugKotlin                        # 秒级快检，--offline -q 可用
./gradlew :app:testDebugUnitTest --tests "cn.hifox.mobile.data.<X>" [--rerun-tasks]
./gradlew :app:assembleDebug -PapiBaseUrl=http://127.0.0.1:5697
./gradlew :app:connectedDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArgs.class=<类>
```

- 单测结果从 `build/test-results/testDebugUnitTest/TEST-*.xml` 聚合（tests / failures 计数）；不信 `UP-TO-DATE` 与静默退出码。
- 仪器化测试出 0 tests 时跑原始 `am instrument` 定位（曾揪出 SDK 启动即崩）。
- debug 变体默认连生产 `api.hifox.com`；release 恒正式域名、明文 HTTP 只在 debug manifest。
- 环境实证：解包 dex `grep api.hifox.com`，或 `aapt2 dump badging` / `aapt2 dump resources`。生成的 `BuildConfig.java` ≠ 包内容。

## 模拟器与真机

```bash
emulator -avd Medium_Phone_API_37.0 -cores 4 -memory 4096 -gpu swiftshader_indirect
adb devices                                  # 多设备一律 adb -s <serial>
adb reverse tcp:5697 tcp:5697                # 本地栈；设备重连后要重打
adb install -r <apk>
am start -n cn.hifox.mobile/.MainActivity
```

- 资源参数是硬要求：默认 1 核 / 2 GB 必系统级 ANR。同 AVD 再起会 FATAL，已在跑就直接复用。`-no-window` 无法人工走查。
- `10.0.2.2` 实测超时，别用。`apps/android/scripts/worktree-emu.sh` 是多 worktree 下的模拟器助手。

## 取证

```bash
adb exec-out screencap -p > /tmp/<name>.png
adb shell screenrecord /sdcard/<name>.mp4    # 动效；导出后 ffmpeg 抽帧回看
adb shell uiautomator dump && adb pull /sdcard/window_dump.xml   # 点击坐标取 bounds，禁凭截图估算
adb shell cmd uimode night yes|no
adb shell cmd locale set-app-locales cn.hifox.mobile --locales zh-Hans|en
adb shell dumpsys input_method | grep mInputShown
adb shell logcat -b crash
adb shell dumpsys activity activities | grep mResumedActivity   # 或 pidof cn.hifox.mobile
```

- 金样截图走 `apps/android/scripts/pull-screenshots.sh`（`record` 重录）；金样在 `app/src/androidTest/assets/golden/`。
- 仪器化结论可疑时先疑 harness：Compose ui-test 冻结 `mainClock` 会造成 settle 假阴性，以真机 adb 手势定论。

## 金样生成器守卫

```bash
node scripts/extract-android-{tokens,locales,visuals,file-icons,app-icon}.mjs --check
vitest run scripts/extract-android-*.test.ts
```

文案的改法：改 `packages/views/locales/*` 或 `extract-ios-locales-additions.json`，登记 `extract-android-locales.mjs` 的 MANIFEST（端内专属键进 `CLIENT_ONLY_COPY`）再生成。生成器脚本内不写产品字面量（从 `build.gradle.kts` 读 namespace / productName）。

## 装包冲突

先查证再决定，不盲卸：

```bash
adb shell dumpsys package cn.hifox.mobile | grep -E "versionName|versionCode|signatures"
adb shell pm path cn.hifox.mobile
```

- `INSTALL_FAILED_VERSION_DOWNGRADE`：提高 `buildNumber` 重打，保持数据。
- 换签名 = 换身份，旧包必然 `INSTALL_FAILED_UPDATE_INCOMPATIBLE`，须卸载重装（清数据，先征得同意）。覆盖前可 pull 旧包用 `apksigner verify --print-certs` 比对证书。

## 打包

```bash
./gradlew :app:assembleRelease -PversionName=<x.y.z> \
  -PbuildNumber=$(date +%Y%m%d%H%M) -Pchannel=alpha
```

- 产物名 `<产品>-android-<versionName>-<build号>-<channel>.apk`（`-PproductName` 控制）。**alpha 也是 release 构建（R8 混淆）**，不是 debug。
- versionCode 从 buildNumber 截掉分钟两位成 10 位（Play Int32 上限）；同 key 同 applicationId 递增即可覆盖安装。
- 签名素材 `apps/android/keystore.properties` + `keystore/hifox-release.jks`，gitignored、不读取不复制不打印。缺文件时 assembleRelease 仍出未签名 APK。
- `mapping.txt`（`app/build/outputs/mapping/release/`）是线上崩溃回溯唯一依据，发布必归档。
- 验签：`apksigner verify --print-certs <apk>`（v2/v3-only 包 `keytool` 不适用）+ `aapt2 dump badging` 核对包名 / versionCode / versionName。
- changelog 遵循仓内 `docs/operations/product-changelog.md`（按日组织、不带版本号、节序 新功能→改进→修复）。
