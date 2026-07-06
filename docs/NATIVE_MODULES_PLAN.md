# Lua C 原生模块加载方案

本文定义 `glua` 直接 `require` Lua 5.3 C 原生扩展模块的实现方案。该能力面向 `.so`、`.dylib`、`.dll`，但语义必须收窄为 **Lua 5.3 public C API 模块**：动态库需要导出 `luaopen_<module>(lua_State*)`，并通过 Lua 5.3 public API 与宿主交互。

## 目标

- 在 Linux、macOS、Windows 上支持 `glua` 直接 `require` Lua 5.3 C 扩展模块。
- 保留当前默认纯 Go、无 CGO 构建：`CGO_ENABLED=0 go test ./...` 与默认 `glua` 行为不受影响。
- 通过可选构建启用 native 模块：

```bash
CGO_ENABLED=1 go build -tags native_modules ./cmd/glua
```

- 构建模式、平台前置条件和当前限制见 [native_modules 构建说明](NATIVE_MODULES_BUILD.md)。
- 复用现有 `package.cpath`、`package.loadlib`、`package.searchers[3]`、`package.searchers[4]` 和 `luaopen_*` 符号生成规则。
- 为 Lua C 模块提供统一的 Lua 5.3 C API shim，使用户自定义模块无需针对每个模块单独写胶水。

## 非目标

- 不承诺普通任意动态库可被 `require`。没有 `luaopen_*` 入口的 `.so/.dylib/.dll` 不是 Lua C 模块，不能按 `require` 语义加载。
- 不承诺依赖 `lstate.h`、`lobject.h` 等 Lua 内部结构的模块兼容。第一阶段只承诺 public API。
- 不把 Lua 官方 C VM 源码接入本项目运行时；本项目 VM 仍是 Go 实现。
- 不在默认构建引入 CGO、C 头文件、系统动态库或 Lua C API 开发包依赖。
- 不承诺第一阶段覆盖完整 Lua 5.3 C API。兼容程度由 shim 已实现 API 决定。

## 当前基础

当前代码已经具备以下接入点：

- `runtime.Options.PackageDynamicLibraryLoader`：可选动态库 loader 回调。
- `stdlib/package.Environment.LoadLib`：实现 `package.loadlib` 三返回语义。
- `package.searchers[3]` 与 `[4]`：按 `package.cpath` 展开 C 模块候选并生成 `luaopen_*` 符号。
- `lua.openPackageWithStateCaller`：打开 package 库时从 State options 注入动态库 loader。
- `internal/cli`：CLI 创建 State 时从 `lua.DefaultOptions()` 开始组装 `lua.Options`。

因此 native 模块实现不需要重写 package 搜索逻辑，重点是提供一个可选的 `PackageDynamicLibraryLoader` 和 C API/ABI shim。

## 架构

### 构建分层

默认构建：

- build tag：无
- `CGO_ENABLED=0`
- `package.loadlib` 和 C searcher 保持当前禁用说明。
- 所有现有兼容测试必须继续通过。

native 构建：

- build tag：`native_modules`
- `CGO_ENABLED=1`
- 注入 native dynamic loader。
- 启用 Lua 5.3 public C API shim。
- 平台动态库入口按系统拆分。

建议目录：

```text
native/lua53/include/        # 复制 Lua 5.3 public headers
native/lua53/shim/           # C API shim 与 Go/C handle 桥
native/lua53/loader/         # dlopen/dlsym 或 LoadLibrary/GetProcAddress
internal/native/             # glua 内部 native loader 封装
tests/native_modules/        # C fixture、构建脚本和 Lua require 测试
```

### 头文件策略

复制并固定 Lua 5.3.6 public headers：

- `lua.h`
- `luaconf.h`
- `lauxlib.h`
- `lualib.h`

不复制或公开内部头文件：

- `lstate.h`
- `lobject.h`
- `lapi.h`

原因：public C 模块应该只依赖 Lua public API；依赖内部结构的模块无法安全映射到 Go VM。

### C API shim

实现一套统一的 Lua 5.3 C API 兼容层，而不是为每个动态库写专用桥。

调用链：

```text
用户 C 扩展模块
  -> luaopen_xxx(lua_State*)
  -> lua_* / luaL_* public API
  -> 本项目 C API shim
  -> Go handle / runtime.State / runtime.Value
  -> go-lua-vm VM
```

`lua_State*` 在本项目中是 opaque handle。C 模块不能访问其内部结构；shim 负责把 C API 栈操作映射到 Go VM 的调用帧、栈和值模型。

### 动态库加载

Linux：

- 使用 `dlopen` / `dlsym`。
- 需要处理模块中的 `lua_*` 未解析符号如何绑定到本项目 shim。
- 可选策略：
  - 主程序导出 shim 符号；
  - 或随 `glua` 提供 `liblua5.3.so` / `liblua.so` 兼容 shim。

macOS：

- 使用 `dlopen` / `dlsym`。
- 常见 Lua C 模块可能使用 `-undefined dynamic_lookup`，可由宿主符号解析。
- 需要验证 `.dylib` 与 `.so` 两类后缀候选。

Windows：

- 使用 `LoadLibraryW` / `GetProcAddress`。
- 常见模块会链接 `lua53.dll` 或 import library。
- 需要提供 `lua53.dll` 兼容 shim，或在 fixture 中验证模块可链接到本项目 shim。

### CLI 与嵌入 API

CLI：

- `native_modules` 构建下，`internal/cli` 创建 State 时把 native loader 写入 `lua.Options.PackageDynamicLibraryLoader`。
- 默认构建不改变 `internal/cli` 行为。

Go 嵌入：

- 提供显式启用入口，避免库模式默认加载任意本机代码。
- 建议命名：

```go
options := lua.DefaultOptions()
options.PackageDynamicLibraryLoader = native.Loader()
state := lua.NewStateWithOptions(options)
```

后续也可以提供 `lua.WithNativeModules(options)`，但第一阶段先保留清晰依赖边界。

## API 覆盖分期

### Phase 1：最小可加载模块

目标：C 模块可导出 `luaopen_xxx` 并返回 table。

覆盖 API：

- `lua_gettop`
- `lua_settop`
- `lua_pushnil`
- `lua_pushboolean`
- `lua_pushinteger`
- `lua_pushnumber`
- `lua_pushlstring`
- `lua_pushstring`
- `lua_createtable`
- `lua_setfield`
- `lua_getfield`
- `luaL_newlib`
- `luaL_checkinteger`
- `luaL_checklstring`
- `luaL_error`

### Phase 2：C function 与多返回

目标：C 模块 table 中的 C function 可被 Lua 调用，并可读取参数、返回多值。

覆盖 API：

- `lua_pushcclosure`
- `lua_pushcfunction`
- `lua_call`
- `lua_pcall`
- `lua_type`
- `lua_typename`
- `lua_toboolean`
- `lua_tointegerx`
- `lua_tonumberx`
- `lua_tolstring`
- `lua_is*` 系列常用入口

### Phase 3：userdata、metatable、registry

目标：常见 C 模块可保存 native 对象、设置 metatable，并使用 registry 引用。

覆盖 API：

- `lua_newuserdata`
- `lua_touserdata`
- `luaL_newmetatable`
- `luaL_getmetatable`
- `lua_setmetatable`
- `lua_getmetatable`
- `luaL_checkudata`
- `luaL_ref`
- `luaL_unref`
- `lua_rawgeti`
- `lua_rawseti`

### Phase 4：错误、longjmp 与调试边界

目标：C 模块错误能转换为 Lua runtime error，并保留 traceback 与 protected call 边界。

覆盖 API：

- `lua_error`
- `luaL_error`
- `lua_atpanic`
- protected call 边界
- C frame traceback 展示

### Phase 5：平台完整性

目标：Linux、macOS、Windows 均可通过 fixture 和至少一个外部示例模块验收。

- Linux：`.so` require。
- macOS：`.dylib` / `.so` require。
- Windows：`.dll` require，验证 `lua53.dll` shim 或等价符号方案。

## 安全与风险

- native 模块执行的是本机机器码，拥有进程权限；库模式默认必须关闭。
- 加载错误必须返回 Lua 5.3 `package.loadlib` 兼容三返回，不能 panic。
- `lua_State*` opaque handle 必须有生命周期校验，禁止 State 关闭后继续使用。
- C callback 进入 Go VM 时必须建立可恢复边界，避免 C panic/longjmp 破坏 Go 栈。
- C 模块中的全局状态、线程安全、重复加载和卸载顺序必须记录在测试中。

## 验收门禁

默认构建：

```bash
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'
```

native 构建：

```bash
CGO_ENABLED=1 go test -tags native_modules ./...
CGO_ENABLED=1 go build -tags native_modules -o bin/glua-native ./cmd/glua
```

fixture 验收：

```bash
./scripts/build-native-fixtures.sh
GLUA_BIN=./bin/glua-native ./scripts/test-native-modules.sh
```

平台验收：

- Linux CI 或本机：`.so`
- macOS CI 或本机：`.dylib` 和 `.so`
- Windows CI 或本机：`.dll`

## 回滚策略

- 默认构建与现有 `PackageDynamicLibraryLoader` 抽象必须保持独立；native 失败时可以整体移除 `native_modules` build tag，不影响 no-CGO 发布。
- 每个 API shim 函数分批提交并配 fixture；出现语义不确定时保留未实现错误，不做错误兼容伪装。
- Windows `lua53.dll` shim 若阻塞，不影响 Linux/macOS 阶段验收，但 TODO 必须明确未完成平台。
