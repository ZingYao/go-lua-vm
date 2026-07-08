# native_modules cross toolchains

This document records the repository-managed cross compile surface for the
optional `native_modules` build. The default release remains no-CGO; these
settings are only for:

```bash
CGO_ENABLED=1 go build -tags native_modules ./cmd/glua
```

## Target matrix

The native compile matrix mirrors `.github/workflows/release.yml`:

| Target | Go env |
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

The canonical list lives in `scripts/native-cross-targets.sh`. Scripts should
source that file instead of duplicating the matrix.

## Managed tools

`.mise.toml` pins the managed tools used by CI and local bootstrap:

```toml
[tools]
go = "1.26.4"
zig = "0.15.1"
```

Run this when a machine needs the managed tools:

```bash
mise install
```

or let the repository bootstrap script do it:

```bash
./scripts/bootstrap-native-toolchains.sh --install
```

On Windows, use Git Bash directly or the PowerShell wrapper:

```powershell
.\scripts\bootstrap-native-toolchains.ps1 -Install -Bash "C:\Program Files\Git\bin\bash.exe"
```

## Compiler variables

`scripts/bootstrap-native-toolchains.sh --emit-env` exports the target list and
the `NATIVE_CC_*` variables consumed by the native scripts.

Linux and Windows use Zig cross targets by default:

| Variable | Default |
| --- | --- |
| `NATIVE_CC_LINUX_AMD64` | `zig cc -target x86_64-linux-gnu` |
| `NATIVE_CC_LINUX_386` | `zig cc -target x86-linux-gnu` |
| `NATIVE_CC_LINUX_ARM64` | `zig cc -target aarch64-linux-gnu` |
| `NATIVE_CC_LINUX_ARMV6` | `zig cc -target arm-linux-gnueabihf -mcpu=arm1176jzf_s` |
| `NATIVE_CC_LINUX_ARMV7` | `zig cc -target arm-linux-gnueabihf -mcpu=cortex_a7` |
| `NATIVE_CC_WINDOWS_AMD64` | `zig cc -target x86_64-windows-gnu` |
| `NATIVE_CC_WINDOWS_386` | `zig cc -target x86-windows-gnu` |
| `NATIVE_CC_WINDOWS_ARM64` | `zig cc -target aarch64-windows-gnu` |
Darwin targets use Apple clang on macOS runners:

| Variable | Default on macOS |
| --- | --- |
| `NATIVE_CC_DARWIN_AMD64` | `xcrun clang -arch x86_64` |
| `NATIVE_CC_DARWIN_ARM64` | `xcrun clang -arch arm64` |

When an Apple SDK is available outside macOS, Zig defaults are also provided:

| Variable | Fallback |
| --- | --- |
| `NATIVE_CC_DARWIN_AMD64` | `zig cc -target x86_64-macos` |
| `NATIVE_CC_DARWIN_ARM64` | `zig cc -target aarch64-macos` |

Custom values always win. Set the appropriate `NATIVE_CC_*` variable before
calling the scripts if a target needs a different compiler or SDK.

Android uses Android NDK clang because CGO needs NDK headers such as
`android/log.h` and `pthread.h`:

| Variable | Default when `ANDROID_NDK_HOME` or `ANDROID_NDK_ROOT` is set |
| --- | --- |
| `NATIVE_CC_ANDROID_ARM64` | `<ndk>/toolchains/llvm/prebuilt/<host>/bin/aarch64-linux-android24-clang` |

The CI workflow installs NDK `r27c` for the Ubuntu native job and exports
`ANDROID_NDK_HOME` before running the cross compile check.

## CI validation

`ci.yml` runs compile-level native checks for every release target:

- Ubuntu runner: Linux, Windows, and Android targets through Zig.
- macOS runner: Darwin amd64 and arm64 through `xcrun clang`.

The check is intentionally compile-level:

```bash
eval "$(./scripts/bootstrap-native-toolchains.sh --emit-env)"
NATIVE_CROSS_REQUIRE_ALL=1 ./scripts/check-native-cross-compile.sh
```

It builds `./internal/native` as a test binary and `./cmd/glua` with
`-tags native_modules`. It does not run foreign binaries and does not replace
the per-platform runtime acceptance scripts.

Runtime acceptance still has to run on the target OS:

```bash
CGO_ENABLED=1 ./scripts/test-native-real-modules.sh
```

Windows runtime acceptance additionally builds the `lua53.dll` shim and import
library through `scripts/build-native-windows-lua53-shim.sh`.
