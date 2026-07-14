# native_modules Windows 功能验收手册

本文用于在 Windows 目标平台手动验收 `native_modules` 功能闭环。它只回答一个问题：Windows 上是否能构建并运行 Lua 5.3 public C API 原生模块。最终性能结果另见 `docs/NATIVE_MODULES_WINDOWS_BENCHMARK.md`。

## 最近一次验收结果

2026-07-08 已在 Windows amd64 目标平台完成严格功能验收：

```powershell
.\scripts\test-native-windows-manual.ps1 -GoArch amd64 -Bash "C:\Program Files\Git\bin\bash.exe" -StrictRuntime
```

结果：通过，脚本退出码为 0，`-StrictRuntime` 未发现运行期 `skip:`。本轮覆盖默认 no-CGO Go tests、`./scripts/check-go-gates.sh`、`CGO_ENABLED=1 go test ./...`、Windows `lua53.def` drift check、`lua53.dll` runtime shim/import library 构建、fixture/lua-cjson/LPeg/LuaSocket DLL 构建、Windows source build strict aggregate，以及 fixture、lua-cjson、LPeg、LuaSocket 和 real modules 运行期验收。

验收环境摘要：

- OS：Microsoft Windows 11 企业版 10.0.26200，64 位。
- Go：`go version go1.26.4 windows/amd64`。
- C toolchain：MSYS2 MinGW GCC 16.1.0。
- Import library 工具：GNU dlltool 2.46.1。
- 临时目录：`C:\tmp\go-lua-vm`。

## 验收范围

Windows 功能验收必须覆盖以下路径：

- 默认 no-CGO 构建：`CGO_ENABLED=0 go test ./...` 和 `./scripts/check-go-gates.sh`。
- native Go 测试：`CGO_ENABLED=1 go test ./...`。
- Windows ABI 清单：`native/lua53/windows/lua53.def` 与 native 源码导出符号一致。
- Windows import library：从 `lua53.def` 生成 `liblua53.dll.a` 或 `lua53.lib`。
- fixture DLL：`glua_native_smoke.dll` 和 `glua_native_failopen.dll` 构建与 `require`。
- lua-cjson DLL：`require("cjson")`、`encode/decode`、错误输入 `pcall`。
- LPeg DLL：`require("lpeg")`、基础 pattern/match、完整 `third_party/lpeg/test.lua` 和 `re` 模块路径。
- LuaSocket DLL：`require("socket")`、`require("mime")`、MIME 编解码、TCP/UDP loopback、官方离线脚本和 `testsrvr.lua` + `testclnt.lua` client/server 主路径。

## 环境要求

- Windows 目标平台真实机器或 VM。
- PowerShell。
- Go `go1.26.4` 位于 `PATH` 第一优先级。
- Git Bash 或 MSYS2 bash，可执行仓库内 bash 脚本。
- Python 3，可由 bash 环境访问。
- Windows C 编译器，需能构建当前 `GOARCH` 的 DLL。
- Windows import library 工具，或预先提供 `LUA53_IMPORT_LIB`。

不要通过临时设置 `GOROOT`、`GOPATH`、`GOTOOLCHAIN` 或 Go SDK 绝对路径绕过项目门禁。

## 关键环境变量

按实际工具链选择其中一种配置。

MinGW 或 clang/LLVM 常见配置：

```powershell
$env:NATIVE_CC_WINDOWS_AMD64 = "clang"
$env:NATIVE_WINDOWS_IMPORT_TOOL = "llvm-dlltool"
$env:NATIVE_WINDOWS_IMPORT_TOOL_KIND = "dlltool"
```

如果已经生成 import library：

```powershell
$env:LUA53_IMPORT_LIB = "C:\path\to\liblua53.dll.a"
```

如果 Git Bash 不在 `PATH`，运行脚本时显式传入：

```powershell
-Bash "C:\Program Files\Git\bin\bash.exe"
```

`arm64` 平台把 `NATIVE_CC_WINDOWS_AMD64` 换成 `NATIVE_CC_WINDOWS_ARM64`，并在脚本参数中传入 `-GoArch arm64`。

## 推荐执行命令

在仓库根目录打开 PowerShell，先确认分支和 Go 版本：

```powershell
git status --short --branch
go version
```

执行严格功能验收：

```powershell
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
.\scripts\test-native-windows-manual.ps1 -StrictRuntime 2>&1 |
  Tee-Object "native-windows-functional-$stamp.log"
```

指定架构和 bash 路径：

```powershell
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
.\scripts\test-native-windows-manual.ps1 `
  -GoArch amd64 `
  -Bash "C:\Program Files\Git\bin\bash.exe" `
  -StrictRuntime 2>&1 |
  Tee-Object "native-windows-functional-$stamp.log"
```

## 脚本执行内容

`scripts/test-native-windows-manual.ps1` 会依次执行：

```text
default no-CGO Go tests
default gate script
native_modules Go tests
Windows lua53.def drift check
Windows lua53 import library build
Windows fixture DLL build
Windows lua-cjson DLL build
Windows LPeg DLL build
Windows LuaSocket DLL build
Windows source build strict aggregate
runtime attempt: scripts/test-native-modules.sh
runtime attempt: scripts/test-native-cjson.sh
runtime attempt: scripts/test-native-lpeg.sh
runtime attempt: scripts/test-native-luasocket.sh
runtime attempt: scripts/test-native-real-modules.sh
```

`-StrictRuntime` 会把任何运行期 `skip:` 视为失败。Windows 最终功能验收必须使用 `-StrictRuntime` 通过；不带 `-StrictRuntime` 的运行只能作为诊断，不应记为 Windows 闭环完成。

## 通过判定

满足以下条件才能把 Windows 功能验收记为通过：

- PowerShell 脚本退出码为 0。
- `go version` 输出 `go1.26.4 windows/<arch>`。
- 日志中没有 `skip:`。
- 日志中没有 `failed`、`panic`、`undefined symbol`、`procedure not found`、`LoadLibrary` 失败或 import library 生成失败。
- `scripts/test-native-real-modules.sh` 在 Windows 目标上真实运行，而不是跳过。

## 需要反馈的结果

请把以下信息反馈回本线程：

- Windows 版本和架构。
- `go version`。
- C 编译器版本。
- import library 工具版本，或 `LUA53_IMPORT_LIB` 路径。
- `native-windows-functional-*.log` 的最终摘要。
- 如果失败，保留第一处失败附近 80 行日志。

功能验收通过后，再执行 `docs/NATIVE_MODULES_WINDOWS_BENCHMARK.md` 中的最终 benchmark。
