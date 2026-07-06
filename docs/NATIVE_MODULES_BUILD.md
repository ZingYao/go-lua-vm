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

当前骨架阶段可验证默认构建不受影响：

```bash
CGO_ENABLED=0 go test ./internal/native
```

可验证 native build tag 下 loader 骨架可编译：

```bash
CGO_ENABLED=1 go test -tags native_modules ./internal/native
```

默认门禁仍按项目要求执行：

```bash
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'
```

后续实现动态库和 C API shim 后，新增 fixture 验收：

```bash
./scripts/build-native-fixtures.sh
GLUA_BIN=./bin/glua-native ./scripts/test-native-modules.sh
```

后续必须增加交叉编译验证入口：

```bash
./scripts/check-native-cross-compile.sh
```

该脚本需要显式输出每个目标的 `GOOS`、`GOARCH`、`CC`、`CGO_ENABLED` 和产物路径。缺少目标 C toolchain 时可以跳过对应目标，但必须打印明确 skip 原因，不能静默视为通过。

fixture 只验证 loader smoke，不作为最终兼容结论。真实兼容验收必须包含：

- `lua-cjson`：第一真实模块，覆盖 `require`、`encode/decode` 和错误输入 `pcall`。
- `lpeg` 或等价纯 C 模块：覆盖 userdata、metatable、registry 和复杂 C function。
- LuaSocket 或等价网络库：放在平台闭环后段，用于验证系统依赖和 socket 行为。

真实模块验收需要同时覆盖源码编译模块和按官方 Lua 5.3 ABI 构建的现成二进制模块。源码编译模块必须使用仓库内固定源码和仓库内 Lua 5.3 public headers，不允许测试时联网下载；后者要求 Linux/macOS 提供可解析的 `lua_*` / `luaL_*` 符号，Windows 提供 `lua53.dll` 或等价 import library 方案。

## 当前限制

- `luaopen_*` 入口尚未真正调用，动态库 loader 仍停在符号解析和 shim 边界。
- Lua 5.3 public C API shim 只完成部分栈基础 API，table、newlib、C function、userdata、registry 和错误边界仍在 TODO 中。
- 尚未接入 CLI 自动注入 native loader。
- 尚未提供完整 C fixture、真实第三方模块源码验收和跨平台交叉编译脚本。
