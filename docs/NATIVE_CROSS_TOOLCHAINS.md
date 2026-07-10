# native_modules 交叉编译工具链

本文记录仓库管理的可选 `native_modules` 交叉编译范围。默认发布仍使用 no-CGO 构建；以下设置仅用于：

```bash
CGO_ENABLED=1 go build -tags native_modules ./cmd/glua
```

## 目标矩阵

原生编译矩阵与 `.github/workflows/release.yml` 保持一致：

| 目标 | Go 环境 |
| --- | --- |
| linux-amd64 | `GOOS=linux GOARCH=amd64` |
| linux-386 | `GOOS=linux GOARCH=386` |
| linux-arm64 | `GOOS=linux GOARCH=arm64` |
| linux-armv6 | `GOOS=linux GOARCH=arm GOARM=6` |
| linux-armv7 | `GOOS=linux GOARCH=arm GOARM=7` |
| windows-amd64 | `GOOS=windows GOARCH=amd64` |
| windows-386 | `GOOS=windows GOARCH=386` |
| windows-arm64 | `GOOS=windows GOARCH=arm64` |
| darwin-amd64 | `GOOS=darwin GOARCH=amd64` |
| darwin-arm64 | `GOOS=darwin GOARCH=arm64` |
| android-arm64 | `GOOS=android GOARCH=arm64` |

标准目标列表位于 `scripts/native-cross-targets.sh`。其他脚本应加载该文件，不应重复定义矩阵。

## 托管工具

`.mise.toml` 固定 CI 与本地初始化使用的工具版本：

```toml
[tools]
go = "1.26.4"
zig = "0.15.1"
```

机器需要安装托管工具时执行：

```bash
mise install
```

也可以让仓库初始化脚本完成安装：

```bash
./scripts/bootstrap-native-toolchains.sh --install
```

在 Windows 上，可以直接使用 Git Bash，也可以使用 PowerShell wrapper：

```powershell
.\scripts\bootstrap-native-toolchains.ps1 -Install -Bash "C:\Program Files\Git\bin\bash.exe"
```

## 编译器变量

`scripts/bootstrap-native-toolchains.sh --emit-env` 会导出目标列表，以及原生脚本使用的 `NATIVE_CC_*` 变量。

Linux 和 Windows 默认使用 Zig 交叉编译目标：

| 变量 | 默认值 |
| --- | --- |
| `NATIVE_CC_LINUX_AMD64` | `zig cc -target x86_64-linux-gnu` |
| `NATIVE_CC_LINUX_386` | `zig cc -target x86-linux-gnu` |
| `NATIVE_CC_LINUX_ARM64` | `zig cc -target aarch64-linux-gnu` |
| `NATIVE_CC_LINUX_ARMV6` | `zig cc -target arm-linux-gnueabihf -mcpu=arm1176jzf_s` |
| `NATIVE_CC_LINUX_ARMV7` | `zig cc -target arm-linux-gnueabihf -mcpu=cortex_a7` |
| `NATIVE_CC_WINDOWS_AMD64` | `zig cc -target x86_64-windows-gnu` |
| `NATIVE_CC_WINDOWS_386` | `zig cc -target x86-windows-gnu` |
| `NATIVE_CC_WINDOWS_ARM64` | `zig cc -target aarch64-windows-gnu` |
Darwin 目标在 macOS runner 上使用 Apple clang：

| 变量 | macOS 默认值 |
| --- | --- |
| `NATIVE_CC_DARWIN_AMD64` | `xcrun clang -arch x86_64` |
| `NATIVE_CC_DARWIN_ARM64` | `xcrun clang -arch arm64` |

在 macOS 之外的环境中，如果存在 Apple SDK，也提供 Zig 默认配置：

| 变量 | 回退值 |
| --- | --- |
| `NATIVE_CC_DARWIN_AMD64` | `zig cc -target x86_64-macos` |
| `NATIVE_CC_DARWIN_ARM64` | `zig cc -target aarch64-macos` |

自定义值始终优先。如果目标需要其他编译器或 SDK，请在调用脚本前设置对应的 `NATIVE_CC_*` 变量。

Android 使用 Android NDK clang，因为 CGO 需要 `android/log.h`、`pthread.h` 等 NDK 头文件：

| 变量 | 设置 `ANDROID_NDK_HOME` 或 `ANDROID_NDK_ROOT` 后的默认值 |
| --- | --- |
| `NATIVE_CC_ANDROID_ARM64` | `<ndk>/toolchains/llvm/prebuilt/<host>/bin/aarch64-linux-android24-clang` |

CI 工作流会为 Ubuntu 原生构建任务安装 NDK `r27c`，并在运行交叉编译检查前导出 `ANDROID_NDK_HOME`。

## CI 验证

`ci.yml` 会对每个发布目标执行编译级原生检查：

- Ubuntu runner：通过 Zig 构建 Linux、Windows 和 Android 目标。
- macOS runner：通过 `xcrun clang` 构建 Darwin amd64 和 arm64 目标。

该检查刻意限定在编译级别：

```bash
eval "$(./scripts/bootstrap-native-toolchains.sh --emit-env)"
NATIVE_CROSS_REQUIRE_ALL=1 ./scripts/check-native-cross-compile.sh
```

它会把 `./internal/native` 构建为测试二进制，并使用 `-tags native_modules` 构建 `./cmd/glua`。它不会运行其他平台的二进制，也不能替代各平台的运行时验收脚本。

运行时验收仍必须在目标操作系统上执行：

```bash
CGO_ENABLED=1 ./scripts/test-native-real-modules.sh
```

Windows 运行时验收还会通过 `scripts/build-native-windows-lua53-shim.sh` 构建 `lua53.dll` shim 和导入库。
