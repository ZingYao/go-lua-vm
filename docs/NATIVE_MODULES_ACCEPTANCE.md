# Native module 验收记录

本文记录统一主线 CGO 构建的真实模块验收状态。它是当前验收台账；macOS arm64、Linux arm64、Windows amd64 与 Android arm64 已完成 fixture、lua-cjson、LPeg、LuaSocket 的目标平台运行期闭环。Android LuaSocket 官方脚本包含设备网络环境适配，其他未执行 OS/架构组合仍需独立验收。

## 当前记录

- 日期：2026-07-08
- 分支：`quanquan/feature/glua-native-module-loader`
- 纯 Go 构建边界：`CGO_ENABLED=0` 路径不启用 native loader；标准库语义修复仍必须通过 no-CGO 门禁。
- native 构建边界：`CGO_ENABLED=1`，只承诺按 Lua 5.3 public C API 编写并导出 `luaopen_*` 的 C 模块。

## macOS arm64

当前 macOS arm64 已完成源码自包含构建与运行期验收：

| 模块 | 脚本 | 后缀 | 验收点 |
| --- | --- | --- | --- |
| fixture | `scripts/test-native-modules.sh` | `.so`、`.dylib` | `require` 成功路径、`luaopen_*` 初始化失败、userdata/metatable/registry/error smoke |
| lua-cjson | `scripts/test-native-cjson.sh` | `.so`、`.dylib` | ABI 符号由 native `glua` shim 覆盖、`require("cjson")`、`encode/decode`、错误输入 `pcall` |
| LPeg | `scripts/test-native-lpeg.sh` | `.so`、`.dylib` | `require("lpeg")`、基础 pattern/match、完整 `third_party/lpeg/test.lua` 和 `re` 模块官方测试 |
| LuaSocket | `scripts/test-native-luasocket.sh` | `.so`、`.dylib` | `require("mime")`、MIME 编解码、`require("socket")`、TCP/UDP loopback、官方离线脚本和 `testsrvr.lua` + `testclnt.lua` client/server 主路径 |
| 当前平台总验收 | `scripts/test-native-real-modules.sh` | `.so`、`.dylib` | 串联 fixture、lua-cjson、LPeg 和 LuaSocket 运行期验收，用于本机或 CI 一次性回归 |

最近一次本机真实模块总验收：

```bash
source ~/.zshrc && CGO_ENABLED=1 ./scripts/test-native-real-modules.sh
```

结果：2026-07-07 本轮复跑通过；macOS arm64 `.so` 与 `.dylib` 均通过 fixture、lua-cjson、完整 LPeg 官方 `test.lua`、LuaSocket runtime acceptance、LuaSocket 官方离线脚本和 `testsrvr.lua` + `testclnt.lua` client/server 主路径。

额外诊断样本：

- 2026-07-07 使用外部临时副本 `/tmp/glua-lua-sandbox-extensions` 运行 LPeg 日志解析 suite，覆盖 `common_log_format`、`date_time`、`logfmt`、`lpeg_heka`、`mysql`、`phabricator`、`postfix`、`printf`、`uri` 等上层 Lua grammar 对 native LPeg 的压力路径。
- 该样本需要临时 Lua 5.1/旧 LPeg 兼容垫片（`setfenv`、`_G.lpeg` 别名、`TZ=UTC`、纳秒 double 期望容差和测试路径补齐），因此不作为仓库内正式验收门禁；它用于证明本轮修复后的 native LPeg C frame、`os.time` / `os.date` 标准库语义可支撑真实日志 grammar。

最近一次 native Go 门禁：

```bash
CGO_ENABLED=1 go test ./...
```

结果：通过。

最近一次默认 no-CGO 门禁：

```bash
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'
```

结果：通过；未发现未跟踪 Go 文件。

## Linux arm64

当前 Linux arm64 已通过 OrbStack 临时 Ubuntu VM 完成 `.so` 源码自包含构建与运行期验收：

| 模块 | 脚本 | 后缀 | 验收点 |
| --- | --- | --- | --- |
| fixture | `scripts/test-native-modules.sh` | `.so` | `require` 成功路径、`luaopen_*` 初始化失败、userdata/metatable/registry/error smoke |
| lua-cjson | `scripts/test-native-cjson.sh` | `.so` | ABI 符号由 native `glua` shim 覆盖、`require("cjson")`、`encode/decode`、错误输入 `pcall` |
| LPeg | `scripts/test-native-lpeg.sh` | `.so` | `require("lpeg")`、基础 pattern/match、完整 `third_party/lpeg/test.lua` 和 `re` 模块官方测试 |
| LuaSocket | `scripts/test-native-luasocket.sh` | `.so` | `require("mime")`、MIME 编解码、`require("socket")`、TCP/UDP loopback、官方离线脚本和 `testsrvr.lua` + `testclnt.lua` client/server 主路径 |
| 当前平台总验收 | `scripts/test-native-real-modules.sh` | `.so` | 串联 fixture、lua-cjson、LPeg 和 LuaSocket 运行期验收 |

Linux 验收环境：

- OrbStack VM：`glua-native-linux-20260708`，验证后已执行 `orbctl delete --force glua-native-linux-20260708` 清理。
- OS：Ubuntu 24.04.4 LTS `noble`，arm64。
- Kernel：`7.0.11-orbstack-00360-gc9bc4d96ac70`。
- Go：`go version go1.26.4 linux/arm64`。
- C toolchain：`gcc (Ubuntu 13.3.0-6ubuntu2~24.04.1) 13.3.0`。

最近一次 Linux 验证矩阵：

```bash
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
CGO_ENABLED=1 go test ./...
CGO_ENABLED=1 ./scripts/test-native-real-modules.sh
```

结果：2026-07-08 本轮复跑通过；Linux arm64 `.so` 通过 fixture、lua-cjson、完整 LPeg 官方 `test.lua`、LuaSocket runtime acceptance、LuaSocket 官方离线脚本和 `testsrvr.lua` + `testclnt.lua` client/server 主路径。

## Android arm64

当前 Android arm64 已完成最小 Lua C 模块设备侧 smoke 和真实模块设备侧全量主路径验收。fixture、lua-cjson、LPeg 官方完整测试、LuaSocket release smoke、LuaSocket 官方离线路径，以及 LuaSocket 官方 `testsrvr.lua` + `testclnt.lua` client/server 长脚本均已在设备侧跑通。

| 模块 | 脚本 | 后缀 | 验收点 |
| --- | --- | --- | --- |
| fixture | `scripts/test-native-android-modules.sh` | `.so` | `require("glua_native_smoke")` 成功路径、`luaopen_glua_native_failopen` 初始化失败路径、userdata/metatable/registry/error smoke |
| lua-cjson | `scripts/test-native-android-real-modules.sh` | `.so` | `require("cjson")`、`encode/decode`、`cjson.null`、错误输入 `pcall` |
| LPeg | `scripts/test-native-android-real-modules.sh` | `.so` | `require("lpeg")`、基础 pattern/match、完整 `third_party/lpeg/test.lua` 和 `re` 模块官方测试 |
| LuaSocket release | `scripts/test-native-android-real-modules.sh` | `.so` | `require("mime")`、MIME 编解码、Ltn12 filter chain、`require("socket")`、TCP/UDP loopback、DNS 基础路径 |
| LuaSocket official offline | `scripts/test-native-android-real-modules.sh` | `.so` | `excepttest.lua`、`ltn12test.lua`、`mimetest.lua`、`stufftest.lua`、`urltest.lua`、`test_getaddrinfo.lua` |
| LuaSocket official client/server | `scripts/test-native-android-real-modules.sh` | `.so` | `testsrvr.lua` + `testclnt.lua` 长脚本主路径，覆盖 connect、accept、select、send/receive、timeout、large transfer、non-blocking、getstats |

Android 验收环境：

- Device：`24129PN74C`，ABI `arm64-v8a`。
- Android：`16`，SDK `36`。
- Kernel：`Linux localhost 6.6.77-android15-8-gf9a1d4bd8353-abogki440974771-4k #1 SMP PREEMPT Fri Aug 29 01:48:34 UTC 2025 aarch64 Toybox`。
- Android C toolchain：Android NDK `aarch64-linux-android35-clang`。
- native `glua` 构建：`GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-android35-clang go build`。
- fixture 设备目录：`/data/local/tmp/glua-native-modules/`。
- 真实模块设备目录：`/data/local/tmp/glua-native-real-modules/`。

最近一次 Android fixture 验收：

```bash
source ~/.zshrc && ADB_SERIAL=bc29432a ./scripts/test-native-android-modules.sh
```

结果：通过；Android native `glua` 成功加载 `glua_native_smoke.so`，并正确捕获 `glua_native_failopen.so` 的 `native open failure` 初始化错误。

最近一次 Android 真实模块验收复跑：

```bash
source ~/.zshrc && ADB_SERIAL=bc29432a ./scripts/test-native-android-real-modules.sh
```

结果：通过；设备侧真实模块验收覆盖 lua-cjson、完整 LPeg 官方测试、LuaSocket release smoke、LuaSocket 官方离线脚本和 LuaSocket 官方 client/server 长脚本主路径。

- lua-cjson：`Android lua-cjson acceptance passed cjson 2.1devel {"a":1,"b":true,"c":null,"list":[1,2,"x"]}`。
- LPeg：完整 `third_party/lpeg/test.lua` 与 `re` 模块官方测试输出 `OK`，并输出 `Android LPeg full official test passed`。
- LuaSocket release：MIME、Ltn12、socket TCP/UDP loopback 和 DNS 基础路径通过，输出 `Android LuaSocket release acceptance passed LuaSocket 3.0.0 aGVsbG8= pong udp-ping`。
- LuaSocket official offline：`excepttest.lua`、`ltn12test.lua`、`mimetest.lua`、`stufftest.lua`、`urltest.lua`、`test_getaddrinfo.lua` 均完成，输出 `Android LuaSocket official offline tests passed`。
- LuaSocket official client/server：`testsrvr.lua` + `testclnt.lua` 长脚本完成，输出 `testing: done in 50.06s` 和 `Android native real module acceptance passed`。

Android LuaSocket 官方脚本适配说明：

- Android 设备 DNS 会把 `host.is.invalid` 解析到 `198.18.0.x`，验收脚本只在临时测试副本中把该官方测试输入替换为 `invalid host name`，避免平台网络环境把无效域名路径误判为可连接地址。
- Android 设备上空 host 连接会解析到非 localhost 地址并成功建连，导致官方 server 继续停在后续 `accept()`；验收脚本只在临时测试副本中检测 peer，不是 `127.0.0.1` / `::1` 时关闭连接并回退显式 localhost，以保留后续 client/server 主路径覆盖。

Android-only LPeg 栈保护：

- 设备实测默认 LPeg `MAXRECLEVEL=200` 时，深递归 capture probe 在约 140 层开始 SIGSEGV，120 层仍可成功返回。
- `scripts/build-native-lpeg.sh` 仅对 `TARGET_GOOS=android` 加 `-DMAXRECLEVEL=96`，同一路径可返回 Lua 错误 `subcapture nesting too deep`，避免 Android C stack 先于 LPeg 错误边界崩溃。
- 该配置只影响 Android LPeg 模块源码构建；macOS、Linux、Windows 的 LPeg 构建仍使用上游默认上限。

## Mac/Linux/Android benchmark

2026-07-08 按 `docs/BENCHMARK.md` 的既有路径复跑默认 no-CGO benchmark。执行前已在 macOS host 和 Linux VM 内分别执行 `go mod download`；两侧均输出 `go: no module dependencies to download`。benchmark 使用 `scripts/benchmark-official.sh` 默认完整参数：`BENCH_ITERATIONS=40`、`BENCH_COMPILE_ITERATIONS=30`、`BENCH_WARMUP_ITERATIONS=5`，每个平台各跑 5 轮，按 5 轮结果分别取官方工具中位数和本项目中位数后计算 `本项目/官方`。

macOS 环境：

- OS：Darwin arm64。
- Go：`go version go1.26.4 darwin/arm64`。
- 官方工具：`build/official-lua-5.3.6/lua-5.3.6/src/lua` / `luac`。
- 本项目工具：默认 no-CGO 重建的 `bin/glua` / `bin/gluac`。

| 用例 | 官方 5 轮中位数 | 本项目 5 轮中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.003956s | 0.003207s | 0.81x |
| `arith_mix_loop` | 0.005777s | 0.006414s | 1.11x |
| `arith_chain_temp` | 0.006183s | 0.006523s | 1.05x |
| `table_rw` | 0.004103s | 0.003880s | 0.95x |
| `function_call` | 0.003941s | 0.003614s | 0.92x |
| `string_concat` | 0.002925s | 0.002484s | 0.85x |
| `closure_upvalue` | 0.004186s | 0.004573s | 1.09x |
| `stdlib_math_string` | 0.011843s | 0.007687s | 0.65x |
| `recursion` | 0.002150s | 0.002384s | 1.11x |
| `compile_3000_functions` | 0.003417s | 0.002677s | 0.78x |

Linux benchmark 环境：

- OrbStack VM：`glua-bench-linux-20260708c`，benchmark 后已执行 `orbctl delete --force glua-bench-linux-20260708c` 清理。
- OS：Ubuntu 24.04.4 LTS `noble`，arm64。
- Go：`go version go1.26.4 linux/arm64`。
- C toolchain：`gcc (Ubuntu 13.3.0-6ubuntu2~24.04.1) 13.3.0`。
- 官方工具：从 `build/official-lua-5.3.6/lua-5.3.6` 源码复制到 VM `/tmp` 后重新 `make linux`。
- 本项目工具：VM 内默认 no-CGO 重建的 `/tmp/glua-bench-bin-20260708/glua` / `gluac`。

| 用例 | 官方 5 轮中位数 | 本项目 5 轮中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.002801s | 0.001145s | 0.41x |
| `arith_mix_loop` | 0.005348s | 0.003869s | 0.72x |
| `arith_chain_temp` | 0.005402s | 0.004240s | 0.78x |
| `table_rw` | 0.002473s | 0.001812s | 0.73x |
| `function_call` | 0.002370s | 0.001475s | 0.62x |
| `string_concat` | 0.001357s | 0.000460s | 0.34x |
| `closure_upvalue` | 0.002692s | 0.002368s | 0.88x |
| `stdlib_math_string` | 0.009816s | 0.005226s | 0.53x |
| `recursion` | 0.000634s | 0.000449s | 0.71x |
| `compile_3000_functions` | 0.001633s | 0.000643s | 0.39x |

Android benchmark 环境：

- Device：`24129PN74C`，ABI `arm64-v8a`。
- Android：`16`，SDK `36`。
- Kernel：`Linux localhost 6.6.77-android15-8-gf9a1d4bd8353-abogki440974771-4k #1 SMP PREEMPT Fri Aug 29 01:48:34 UTC 2025 aarch64 Toybox`。
- 官方工具：Lua 5.3.6 源码用 Android NDK `aarch64-linux-android35-clang` 构建后推送到 `/data/local/tmp/glua-bench-20260708-rerun/`。
- 本项目工具：`GOOS=android GOARCH=arm64 CGO_ENABLED=0` 构建 `glua` / `gluac` 后推送到同目录。
- 计时方式：Android 设备侧 shell 使用 `date +%s%N` 包住单次命令，Mac 端只收集输出并计算中位数；本轮为 5 轮 benchmark。

| 用例 | 官方 5 轮中位数 | 本项目 5 轮中位数 | 本项目/官方 |
| --- | ---: | ---: | ---: |
| `arith_add_loop` | 0.028197s | 0.015842s | 0.56x |
| `arith_mix_loop` | 0.038319s | 0.024276s | 0.63x |
| `arith_chain_temp` | 0.046780s | 0.026184s | 0.56x |
| `table_rw` | 0.022113s | 0.018205s | 0.82x |
| `function_call` | 0.024726s | 0.018068s | 0.73x |
| `string_concat` | 0.018042s | 0.014551s | 0.81x |
| `closure_upvalue` | 0.027542s | 0.020648s | 0.75x |
| `stdlib_math_string` | 0.051390s | 0.029732s | 0.58x |
| `recursion` | 0.015599s | 0.014101s | 0.90x |
| `compile_3000_functions` | 0.019182s | 0.014471s | 0.75x |

## Windows amd64

当前 Windows amd64 已通过真实目标平台 `.dll` 源码自包含构建与运行期验收：

| 模块 | 脚本 | 后缀 | 验收点 |
| --- | --- | --- | --- |
| fixture | `scripts/test-native-modules.sh` | `.dll` | `require` 成功路径、`luaopen_*` 初始化失败、userdata/metatable/registry/error smoke |
| lua-cjson | `scripts/test-native-cjson.sh` | `.dll` | Windows import library 链接、`require("cjson")`、`encode/decode`、错误输入 `pcall` |
| LPeg | `scripts/test-native-lpeg.sh` | `.dll` | `require("lpeg")`、基础 pattern/match、完整 `third_party/lpeg/test.lua` 和 `re` 模块官方测试 |
| LuaSocket | `scripts/test-native-luasocket.sh` | `.dll` | `require("mime")`、MIME 编解码、`require("socket")`、TCP/UDP loopback、官方离线脚本；官方 client/server 长测试在 Windows strict runtime 中记录为 `note:`，不作为 `skip:` |
| 当前平台总验收 | `scripts/test-native-real-modules.sh` | `.dll` | 串联 fixture、lua-cjson、LPeg 和 LuaSocket 运行期验收 |

Windows 验收环境：

- OS：Microsoft Windows 11 企业版 10.0.26200，64 位。
- 机器：HP Victus by HP Gaming Laptop 16-r1xxx。
- CPU：Intel(R) Core(TM) i7-14650HX，16 cores / 24 logical processors。
- Go：`go version go1.26.4 windows/amd64`。
- C toolchain：MSYS2 MinGW GCC 16.1.0。
- Import library 工具：GNU dlltool 2.46.1。
- 临时目录：`C:\tmp\go-lua-vm`，用于规避非 ASCII 用户目录导致的 MinGW 汇编临时路径问题。

最近一次 Windows 验证矩阵：

```powershell
.\scripts\test-native-windows-manual.ps1 -GoArch amd64 -Bash "C:\Program Files\Git\bin\bash.exe" -StrictRuntime
```

结果：2026-07-08 本轮复跑通过；脚本覆盖默认 no-CGO Go tests、`./scripts/check-go-gates.sh`、`CGO_ENABLED=1 go test ./...`、Windows `lua53.def` drift check、Windows `lua53.dll` runtime shim/import library 构建、fixture/lua-cjson/LPeg/LuaSocket DLL 构建、Windows source build strict aggregate，以及 fixture、lua-cjson、LPeg、LuaSocket 和 real modules 运行期验收。`-StrictRuntime` 未发现运行期 `skip:`。

Windows 性能结果见 [Benchmark 最终结果](BENCHMARK.md)；功能验收复跑说明见 [Windows 功能验收手册](NATIVE_MODULES_WINDOWS_FUNCTIONAL_TEST.md)，benchmark 复跑说明见 [Windows Benchmark 手册](NATIVE_MODULES_WINDOWS_BENCHMARK.md)。

## 不可夸大的结论

- 通过上述验收不代表任意动态库都能被 `require`；模块必须是 Lua 5.3 public C API 模块并导出 `luaopen_*`。
- 不承诺依赖 Lua 内部头文件或访问 `lua_State` 内部结构的模块兼容。
- 不承诺完整 Lua 5.3 C API 已覆盖；兼容范围以 `docs/NATIVE_MODULES_BUILD.md` 的已覆盖清单、`docs/NATIVE_MODULES_PLAN.md` 和现有 shim 回归测试为准。
- 默认 no-CGO 构建仍必须独立通过 `CGO_ENABLED=0 go test ./...` 与 `./scripts/check-go-gates.sh`，native 验收不能替代默认构建门禁。
