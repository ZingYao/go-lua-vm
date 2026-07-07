# native_modules 构建说明

本文说明 `native_modules` 可选构建的使用方式、平台前置条件、当前阶段能力边界和后续验收命令。该构建用于逐步支持 `glua` 直接 `require` Lua 5.3 C 原生扩展模块。

## 构建模式

默认构建保持纯 Go、无 CGO，仍是项目的默认发布和测试路径：

```bash
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
```

默认构建下不会启用本机动态库加载器，`package.loadlib` 和 C searcher 继续保持现有禁用或不可用语义。

native 构建需要显式启用 CGO 和 build tag：

```bash
CGO_ENABLED=1 go build -tags native_modules -o bin/glua-native ./cmd/glua
```

`native_modules` 构建允许引入 CGO，用于平台动态库加载、Lua 5.3 C API shim 和后续 `lua53` 兼容符号层。该允许范围只适用于 native 模块能力；默认构建仍必须保持 `CGO_ENABLED=0`。

native 构建不得依赖系统已安装的 Lua C 开发包。项目侧 C shim、Lua 5.3 public headers、fixture、真实模块验收源码和构建脚本必须随仓库提交，便于在不同机器和 CI 中重复编译验证。

## 兼容目标

native 模块必须满足以下条件：

- 动态库导出 `luaopen_<module>(lua_State*)`。
- 只依赖 Lua 5.3 public C API。
- 使用 `lua.h`、`lauxlib.h`、`lualib.h` 等 public headers 编译。
- 不直接依赖 `lstate.h`、`lobject.h`、`lapi.h` 等 Lua 内部头文件。

该能力不是任意动态库 FFI。没有 `luaopen_*` 入口的 `.so`、`.dylib`、`.dll` 不能按 `require` 语义加载。

## 头文件位置

Lua 5.3.6 public headers 固定在：

```text
native/lua53/include/
```

该目录供本项目 shim、fixture 和外部模块构建说明引用。默认 Go 构建不会包含这些头文件，也不会引入 CGO。

fixture 和真实模块验收必须只引用该目录下的头文件，禁止引用 `/usr/include/lua*`、Homebrew Lua、LuaRocks 或用户本机 Lua SDK。

## 平台前置条件

Linux：

- 需要可用 C 编译器和系统动态加载能力。
- 后续动态库加载器将使用 `dlopen`、`dlsym`、`dlclose`。
- fixture 将优先验证 `.so`。

macOS：

- 需要 Xcode Command Line Tools 或等价 C 编译器。
- 后续动态库加载器将使用 `dlopen`、`dlsym`、`dlclose`。
- fixture 将验证 `.dylib`，并覆盖 Lua 生态常见的 `.so` 后缀候选。

Windows：

- 需要可用 C 编译器和 Windows 动态库加载能力。
- 后续动态库加载器将使用 `LoadLibraryW`、`GetProcAddress`、`FreeLibrary`。
- 常见 Lua C 模块可能链接 `lua53.dll`，因此 Windows 闭环需要 `lua53.dll` shim 或等价 import library 方案。

## 当前验证命令

默认构建必须保持无 CGO：

```bash
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'
```

native 构建需要通过 Go/CGO shim 全量测试：

```bash
CGO_ENABLED=1 go test -tags native_modules ./...
```

fixture loader smoke：

```bash
./scripts/test-native-modules.sh
```

真实第三方模块源码编译与运行期验收：

```bash
./scripts/build-native-cjson.sh
./scripts/test-native-cjson.sh
./scripts/build-native-lpeg.sh
./scripts/test-native-lpeg.sh
./scripts/build-native-luasocket.sh
./scripts/test-native-luasocket.sh
```

当前平台真实模块总验收入口：

```bash
./scripts/test-native-real-modules.sh
```

该入口串联 fixture、lua-cjson、LPeg 和 LuaSocket 当前平台运行期验收，便于本机或 CI 做一次性回归；它只允许在宿主同平台运行，Windows 目标和其他异平台目标会明确 `skip:`，不替代 Linux/Windows 目标平台的独立运行期闭环。

交叉编译验证入口：

```bash
./scripts/check-native-cross-compile.sh
```

该脚本需要显式输出每个目标的 `GOOS`、`GOARCH`、`CC`、`CGO_ENABLED`、产物路径和最终 `compiled/skipped/targets` 汇总。缺少目标 C toolchain 时可以跳过对应目标，但必须打印明确 skip 原因，不能静默视为通过。目标编译器可通过 `NATIVE_CC_<GOOS>_<GOARCH>` 指定，值可以包含编译器参数，例如 `NATIVE_CC_LINUX_ARM64="zig cc -target aarch64-linux-musl"`；脚本只用第一个命令词做存在性检查，完整字符串会原样传给 Go/cgo 的 `CC`。

如果某次验收必须证明列出的目标全部完成编译，可以启用严格模式：

```bash
NATIVE_CROSS_REQUIRE_ALL=1 NATIVE_CROSS_TARGETS="linux/arm64 windows/arm64" ./scripts/check-native-cross-compile.sh
```

严格模式下，缺少目标 C toolchain 仍会输出明确 `skip:` 原因，但脚本会返回失败，避免把未配置工具链的目标误记为编译验证已完成。

平台不可用时的 skip 原因门禁：

```bash
./scripts/check-native-skip-reasons.sh
```

该脚本不替代 Linux/Windows 真实运行期验收，只验证 Windows shim 未落地、缺失 cross C compiler、交叉编译严格模式缺失 toolchain、当前平台总验收被误用于 Windows 或非 Windows 异平台等不可用场景会输出明确 `skip:` 原因。

当前平台 Lua 5.3 ABI 符号覆盖门禁：

```bash
./scripts/check-native-lua-abi-symbols.sh
```

该脚本构建 native `glua`、fixture、lua-cjson、LPeg 和 LuaSocket，收集真实模块未解析的 `lua_*` / `luaL_*` 符号，并确认这些符号同时存在于 native 源码声明（Go `//export` 加 C wrapper 定义）和当前 native `glua` 二进制导出中。它为后续 Windows `lua53.dll` shim 或 import library 提供可复用的符号覆盖门禁，但不替代 Windows 目标平台运行期验收。

Windows `lua53.dll` / import library 导出定义门禁：

```bash
./scripts/check-native-windows-def.sh
```

该脚本从 native 源码声明重新生成 Lua 5.3 ABI 导出列表，并比对 `native/lua53/windows/lua53.def`。该 `.def` 文件是后续 Windows import library 或 `lua53.dll` shim 的链接期输入，不代表 Windows `.dll` 运行期验收已完成。

fixture 只验证 loader smoke，不作为最终兼容结论。真实兼容验收必须包含：

- `lua-cjson`：第一真实模块，覆盖 `require`、`encode/decode` 和错误输入 `pcall`。
- `lpeg` 或等价纯 C 模块：覆盖 userdata、metatable、registry 和复杂 C function。
- LuaSocket 或等价网络库：放在平台闭环后段，用于验证系统依赖和 socket 行为；当前提供源码编译入口和当前平台运行期 loopback 验收脚本。

真实模块验收需要同时覆盖源码编译模块和按官方 Lua 5.3 ABI 构建的现成二进制模块。源码编译模块必须使用仓库内固定源码和仓库内 Lua 5.3 public headers，不允许测试时联网下载；后者要求 Linux/macOS 提供可解析的 `lua_*` / `luaL_*` 符号，Windows 提供 `lua53.dll` 或等价 import library 方案。

## 当前支持面与限制

当前 `native_modules` 已经能在 macOS arm64 上用仓库内 `lua-cjson` 源码完成 `.so` 与 `.dylib` 两种后缀的 Lua 5.3 ABI 符号验收和运行期验收。ABI 验收要求 `cjson.so` / `cjson.dylib` 保留未解析 `lua_*` / `luaL_*` 符号、不链接外部 Lua runtime，并确认这些符号由 native `glua` 导出的 shim 覆盖；运行期验收覆盖 `require("cjson")`、`encode/decode`、`cjson.null`、非法 JSON `pcall` 和不可序列化 function `pcall`。

当前 `scripts/build-native-luasocket.sh` 可在 macOS arm64 上使用仓库内 LuaSocket v3.1.0 源码和 `native/lua53/include/` 编译 `socket/core.so`、`socket/core.dylib`、`mime/core.so` 和 `mime/core.dylib`。`scripts/test-native-luasocket.sh` 会构建 native `glua` 与 LuaSocket 模块，并分别按当前平台后缀验证 `require("mime")`、基础 MIME 编解码、`require("socket")`、`socket.tcp()` / `socket.udp()` 以及本机 TCP/UDP loopback。

当前真实模块验收状态和未闭环平台见 [native_modules 验收记录](NATIVE_MODULES_ACCEPTANCE.md)。

已覆盖的 Lua 5.3 public C API 主要包括：

- 栈和值：`lua_gettop`、`lua_settop`、`lua_checkstack`、`lua_rotate`、`lua_pushvalue`、`lua_pushnil`、`lua_pushboolean`、`lua_pushinteger`、`lua_pushnumber`、`lua_pushlstring`、`lua_pushstring`、`lua_pushlightuserdata`。
- 类型与转换：`lua_type`、`lua_typename`、`lua_toboolean`、`lua_tointegerx`、`lua_tonumberx`、`lua_tolstring`、`luaL_checkinteger`、`luaL_checklstring`、`luaL_checkoption`、`luaL_checkstack`。
- 表与 registry：`lua_createtable`、`lua_setfield`、`lua_getfield`、`lua_rawgeti`、`lua_rawseti`、`lua_rawset`、`lua_next`、`luaL_ref`、`luaL_unref`。
- C function 与 newlib：`lua_pushcclosure`、`lua_pushcfunction`、`luaL_setfuncs`、`luaL_newlib`、`luaL_checkversion_`。
- userdata 与 metatable：`lua_newuserdata`、`lua_touserdata`、`luaL_checkudata`、`lua_setmetatable`、`lua_getmetatable`、`luaL_newmetatable`、`luaL_getmetatable`。
- 错误与 protected call：`lua_error`、`luaL_error`、`luaL_argerror`、`lua_pcallk` 的非 yield 路径。

仍不承诺或存在语义差异的边界：

- 只支持 Lua 5.3 public C API 模块；依赖 Lua 内部头文件或访问 `lua_State` 内部结构的模块不兼容。
- `lua_pcallk` 只覆盖非 yield 场景；`lua_yieldk`、C continuation、coroutine/yield 穿越 C frame 仍未实现。
- `lua_atpanic`、debug hook C API、C 源码文件名/行号、动态库内部 C 调用栈不暴露；traceback 使用 Go VM callable frame 承载。
- `lua_call`、`lua_gettable`、`lua_settable`、`lua_getglobal`、`lua_setglobal`、`lua_getuservalue`、`lua_setuservalue`、`lua_gc`、`lua_dump`、`lua_load` 等 API 尚未作为兼容承诺。
- `luaL_checkversion_` 当前用于匹配 Lua 5.3 public header 宏展开路径，不做完整 ABI size/version 拒绝。
- 官方 Lua 5.3 ABI 二进制模块已在 macOS arm64 上通过 `lua-cjson` `.so` / `.dylib` 符号验收；Linux 还需目标平台确认宿主符号导出或 `liblua5.3` / `liblua` 兼容 shim，Windows 还需 `lua53.dll` shim 或等价 import library。
- Linux `.so` 和 Windows `.dll` 运行期验收仍受目标平台与 C toolchain 限制；当前 macOS 可运行验收不能替代 Linux/Windows 真机或 CI 验收。
