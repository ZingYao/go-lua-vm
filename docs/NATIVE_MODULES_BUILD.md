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

当前阶段 `native_modules` 只提供 loader 入口骨架，尚未实现平台动态库打开、`luaopen_*` 调用和 Lua 5.3 C API shim。因此真实 C 模块加载仍会返回明确的未实现错误；后续阶段会按 `docs/NATIVE_MODULES_TODO.md` 逐步打开能力。

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

## 当前限制

- 尚未实现 Linux/macOS 的 `dlopen` / `dlsym`。
- 尚未实现 Windows 的 `LoadLibraryW` / `GetProcAddress`。
- 尚未实现 `lua_State*` opaque handle。
- 尚未实现 Lua 5.3 public C API shim。
- 尚未接入 CLI 自动注入 native loader。
- 尚未提供 C fixture 和跨平台验收脚本。
