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
- C 源码、headers、fixture、真实模块源码和脚本的自包含状态见 [native_modules C 源码自包含清单](NATIVE_MODULES_SOURCE_INVENTORY.md)。
- 复用现有 `package.cpath`、`package.loadlib`、`package.searchers[3]`、`package.searchers[4]` 和 `luaopen_*` 符号生成规则。
- 为 Lua C 模块提供统一的 Lua 5.3 C API shim，使用户自定义模块无需针对每个模块单独写胶水。
- `native_modules` 路径允许引入 CGO；但所有项目侧 C shim、Lua 5.3 public headers、fixture、真实模块验收源码和构建脚本必须随仓库提交，不能依赖系统已安装的 Lua C 开发包或临时下载源码。

## 非目标

- 不承诺普通任意动态库可被 `require`。没有 `luaopen_*` 入口的 `.so/.dylib/.dll` 不是 Lua C 模块，不能按 `require` 语义加载。
- 不承诺依赖 `lstate.h`、`lobject.h` 等 Lua 内部结构的模块兼容。第一阶段只承诺 public API。
- 不把 Lua 官方 C VM 源码接入本项目运行时；本项目 VM 仍是 Go 实现。
- 不在默认构建引入 CGO、C 头文件、系统动态库或 Lua C API 开发包依赖。
- 不要求默认构建支持 native 模块；默认构建仍必须可在 `CGO_ENABLED=0` 下完整通过门禁。
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
- 允许使用 CGO、平台动态库 API 和随仓库提交的 C 源码；禁止依赖机器上预装的 Lua 头文件、Lua C 静态库、Lua C 动态库或未纳入仓库的第三方 C 源码。

建议目录：

```text
native/lua53/include/        # 复制 Lua 5.3 public headers
native/lua53/shim/           # C API shim 与 Go/C handle 桥
native/lua53/loader/         # dlopen/dlsym 或 LoadLibrary/GetProcAddress
internal/native/             # glua 内部 native loader 封装
tests/native_modules/        # C fixture、构建脚本和 Lua require 测试
```

### C 源码与交叉编译策略

`native_modules` 的可移植性要求是 **源码和构建入口自包含**：

- 项目实现需要的 C shim、兼容 `lua53` 符号导出层、fixture C 文件、fixture Lua 脚本和构建脚本必须提交到仓库。
- 验收使用的真实第三方 C 模块源码也必须固定到仓库或 `third_party/` 下，并记录来源、版本和许可证；CI/本地验收不得在测试时联网下载。
- 构建脚本必须只引用仓库内的 Lua 5.3 public headers，不读取系统 `/usr/include/lua*`、Homebrew Lua、LuaRocks 安装目录或用户自定义 Lua SDK。
- 交叉编译验证脚本需要显式输出目标平台、`GOOS`、`GOARCH`、`CC`、`CGO_ENABLED` 和产物路径；缺少目标 C toolchain 时必须跳过并说明原因，而不是静默通过。
- Linux/macOS/Windows 的 shim 和 fixture 都应能通过同一套脚本入口做 `go test -c` 或 `go build` 级别验证；运行时加载测试只在对应平台执行。

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
- `lua_copy`
- `lua_pushvalue`
- `lua_pushnil`
- `lua_pushboolean`
- `lua_pushinteger`
- `lua_pushnumber`
- `lua_pushlstring`
- `lua_pushstring`
- `lua_pushfstring`
- `lua_createtable`
- `lua_gettable`
- `lua_setfield`
- `lua_getfield`
- `luaL_newlib`
- `luaL_checkinteger`
- `luaL_optinteger`
- `luaL_checklstring`
- `luaL_checkany`
- `luaL_checktype`
- `luaL_error`
- `lua_getallocf`

### Phase 2：C function 与多返回

目标：C 模块 table 中的 C function 可被 Lua 调用，并可读取参数、返回多值。

覆盖 API：

- `lua_pushcclosure`
- `lua_pushcfunction`
- `lua_callk`
- `lua_call`
- `lua_pcall`
- `lua_type`
- `lua_typename`
- `lua_toboolean`
- `lua_tointegerx`
- `lua_tonumberx`
- `lua_tolstring`
- `lua_compare`
- `lua_is*` 系列常用入口
  - `lua_isstring` 已覆盖 Lua 5.3 对 string 和 number 的可转换性判断；LPeg 1.1.0 下一阻塞点已前移到 `lua_pushfstring`。

### Phase 3：userdata、metatable、registry

目标：常见 C 模块可保存 native 对象、设置 metatable，并使用 registry 引用。

覆盖 API：

- `lua_newuserdata`
- `lua_touserdata`
- `luaL_newmetatable`
- `luaL_getmetatable`
- `lua_setmetatable`
- `lua_getmetatable`
- `lua_getuservalue`
- `luaL_checkudata`
- `luaL_ref`
- `luaL_unref`
- `lua_rawgeti`
- `lua_rawseti`
- `luaL_buffinit`
- `luaL_prepbuffsize`
- `luaL_addlstring`
- `luaL_addstring`
- `luaL_addvalue`
- `luaL_pushresult`
- `luaL_pushresultsize`
- `luaL_buffinitsize`

### Phase 4：错误、longjmp 与调试边界

目标：C 模块错误能转换为 Lua runtime error，并保留 traceback 与 protected call 边界。

覆盖 API：

- `lua_error`
- `luaL_error`
- `lua_atpanic`
- protected call 边界
- C frame traceback 展示

### C frame traceback 策略

`native_modules` 不伪造 Lua 官方 C VM 的 `CallInfo` 或 C 源码位置。C 模块函数在本项目中会被包装为 Go VM callable，因此 traceback 中的 C frame 展示遵循以下策略：

- C function 调用帧使用现有 Go closure 调试帧承载，帧类型仍显示为 Go/C 边界的 `[go]`。
- 函数名优先来自 Lua 调用点推断的 `name` / `namewhat`，例如 `mod.fail()` 展示为 field `fail`，全局或局部调用按既有 Lua 调用点规则展示。
- `luaopen_*` 初始化错误通过 require/package loader 的 protected call 边界传播，错误对象和 traceback 由现有 `runtime.RaiseError` 与 debug 库处理。
- 不展示 C 源码文件名、C 行号、C 栈地址或动态库内部调用栈；这些信息不属于 Lua 5.3 public C API 可移植语义，也会在不同平台和编译器下不稳定。
- 后续若引入专用 native frame 名称，只能作为附加调试元信息，不能改变默认 Lua 层错误对象、`pcall`/`xpcall` 捕获语义或默认 no-CGO 行为。

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

真实第三方模块验收：

- 自编 fixture 只作为 loader smoke：它只能证明 `package.loadlib`、`dlopen` / `dlsym` / `LoadLibraryW` / `GetProcAddress` 和错误分类链路贯通，不能作为 Lua C 扩展兼容性的最终依据。
- 真实第三方模块验收也必须使用仓库内固定源码构建；允许再追加“现成二进制模块”验证，但不能替代仓库内源码编译验收。
- 第一真实模块门禁使用 `lua-cjson`：必须验证 `require("cjson")`、`cjson.encode`、`cjson.decode`、错误输入下的 `pcall` 行为和 Lua 5.3 public C API shim 覆盖度。
- 第二层真实模块建议使用 `lpeg` 或等价纯 C 模块：用于覆盖更复杂的 userdata、metatable、registry 和 C function 行为。
- 网络库如 LuaSocket 放在后续平台闭环：它能验证更真实的系统依赖和 socket 行为，但只有在 userdata、metatable、registry 和错误边界稳定后才进入必过门禁。
- 验收必须区分两类 ABI 场景：
  - 源码编译验证：使用本项目提供的 Lua 5.3 public headers 和 shim 链接方式构建第三方模块。
  - 现成二进制模块验证：验证按官方 Lua 5.3 ABI 构建、期望 `lua_*` / `luaL_*` 符号的 `.so`、`.dylib`、`.dll`；Linux/macOS 需要主程序导出符号或提供 `liblua5.3` / `liblua` 兼容 shim，Windows 需要 `lua53.dll` 或等价 import library 方案。

平台验收：

- Linux CI 或本机：`.so`
- macOS CI 或本机：`.dylib` 和 `.so`
- Windows CI 或本机：`.dll`

## 回滚策略

- 默认构建与现有 `PackageDynamicLibraryLoader` 抽象必须保持独立；native 失败时可以整体移除 `native_modules` build tag，不影响 no-CGO 发布。
- 每个 API shim 函数分批提交并配 fixture；出现语义不确定时保留未实现错误，不做错误兼容伪装。
- Windows `lua53.dll` shim 若阻塞，不影响 Linux/macOS 阶段验收，但 TODO 必须明确未完成平台。
