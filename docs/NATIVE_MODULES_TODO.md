# Lua C 原生模块加载 TODO

本文跟踪 `glua` 通过可选 CGO/native shim 在 Linux、macOS、Windows 上直接 `require` Lua 5.3 C 扩展模块的实施进度。每轮自动推进必须先读取本文和 `docs/NATIVE_MODULES_PLAN.md`。

## 总目标

- 默认 no-CGO 构建保持现状并通过全部门禁。
- `native_modules` 构建支持直接 `require` 按 Lua 5.3 public C API 编写并导出 `luaopen_*` 的 `.so/.dylib/.dll`。
- Linux、macOS、Windows 均有 fixture 或平台脚本验收。

## 当前状态

- 分支：`quanquan/feature/glua-native-module-loader`
- 基线提交：`bfcc272`
- 已有接入点：
  - `runtime.Options.PackageDynamicLibraryLoader`
  - `stdlib/package.Environment.LoadLib`
  - `package.searchers[3]` / `[4]`
  - `lua.openPackageWithStateCaller`
  - `internal/cli` 中 `lua.DefaultOptions()` 创建入口
- 约束：默认构建仍必须 `CGO_ENABLED=0`。

## 第一阶段：方案与骨架

- [x] 创建 native 模块方案文档。
- [x] 创建 native 模块 TODO 文档。
- [x] 复制 Lua 5.3.6 public headers 到稳定路径：
  - `native/lua53/include/lua.h`
  - `native/lua53/include/luaconf.h`
  - `native/lua53/include/lauxlib.h`
  - `native/lua53/include/lualib.h`
- [x] 增加复制来源说明，确保这些头文件来自 `third_party/lua-5.3.6/`。
- [x] 新增 `native_modules` build tag 下的 native loader 包骨架。
- [x] 新增非 `native_modules` build tag 下的 no-op 包，保证默认构建不受影响。
- [x] 增加 `native_modules` 构建说明文档。

## 第二阶段：动态库加载器

- [x] Linux/macOS 实现 `dlopen` / `dlsym` / `dlclose` 封装。
- [x] Windows 实现 `LoadLibraryW` / `GetProcAddress` / `FreeLibrary` 封装。
- [ ] 动态库 loader 返回 Lua 可调用 loader，接入 `PackageDynamicLibraryLoader`。
  - [x] 已新增 State-aware loader 工厂入口，后续 native shim 可绑定当前 State 后返回 Lua callable。
- [ ] `package.loadlib(path, symbol)` 在 native 构建下可加载 fixture 入口。
  - [x] 已验证 Linux/macOS 真实 fixture 可解析到 `luaopen_*`，并在 C API shim 未实现时返回 `init` 分类。
- [ ] `require("mod")` 在 native 构建下可通过 `package.cpath` 命中 fixture。
- [ ] 保持默认构建 `package.loadlib` 禁用说明不变。

## 第三阶段：最小 Lua C API shim

- [x] 设计 opaque `lua_State*` handle 与 Go State 映射。
- [ ] 实现 C API 栈基本操作：
  - [ ] `lua_gettop`
  - [ ] `lua_settop`
  - [ ] `lua_pushnil`
  - [ ] `lua_pushboolean`
  - [ ] `lua_pushinteger`
  - [ ] `lua_pushnumber`
  - [ ] `lua_pushlstring`
  - [ ] `lua_pushstring`
- [ ] 实现 table 和 newlib 基础：
  - [ ] `lua_createtable`
  - [ ] `lua_setfield`
  - [ ] `lua_getfield`
  - [ ] `luaL_newlib`
- [ ] 实现基础参数检查：
  - [ ] `luaL_checkinteger`
  - [ ] `luaL_checklstring`
  - [ ] `luaL_error`
- [ ] fixture：C 模块 `luaopen_glua_native_smoke` 返回 table，并暴露一个简单函数。

## 第四阶段：C function 调用

- [ ] 实现 `lua_CFunction` 到 Go VM callable 的包装。
- [ ] 实现：
  - [ ] `lua_pushcclosure`
  - [ ] `lua_pushcfunction`
  - [ ] `lua_type`
  - [ ] `lua_typename`
  - [ ] `lua_toboolean`
  - [ ] `lua_tointegerx`
  - [ ] `lua_tonumberx`
  - [ ] `lua_tolstring`
- [ ] 支持 C function 读取 Lua 参数并返回多值。
- [ ] fixture：C 模块函数 `add(a, b)`、`echo(s)`、`multi()`。

## 第五阶段：userdata、metatable、registry

- [ ] 实现 userdata：
  - [ ] `lua_newuserdata`
  - [ ] `lua_touserdata`
  - [ ] `luaL_checkudata`
- [ ] 实现 metatable：
  - [ ] `luaL_newmetatable`
  - [ ] `luaL_getmetatable`
  - [ ] `lua_setmetatable`
  - [ ] `lua_getmetatable`
- [ ] 实现 registry/ref：
  - [ ] `luaL_ref`
  - [ ] `luaL_unref`
  - [ ] `lua_rawgeti`
  - [ ] `lua_rawseti`
- [ ] fixture：C 模块创建 userdata，方法调用后能保持状态。

## 第六阶段：错误、pcall、traceback

- [ ] 将 `lua_error` / `luaL_error` 转换为 Go VM runtime error。
- [ ] 验证 `pcall(require, "mod")` 捕获 C module 初始化错误。
- [ ] 验证 C function 运行时错误包含合理 traceback。
- [ ] 定义 C frame 在 `debug.traceback` 中的展示策略。
- [ ] 记录暂不支持或语义有差异的 C API。

## 第七阶段：平台闭环

- [ ] 真实第三方模块验收：
  - [ ] 明确自编 fixture 只作为 loader smoke，不作为最终兼容性依据。
  - [ ] `lua-cjson` 源码编译验收：`require("cjson")`、`encode/decode`、错误输入 `pcall`。
  - [ ] `lua-cjson` 官方 Lua 5.3 ABI 二进制模块验收：验证 `lua_*` / `luaL_*` 符号由本项目 shim 满足。
  - [ ] `lpeg` 或等价纯 C 模块验收：覆盖 userdata、metatable、registry 和复杂 C function 行为。
  - [ ] LuaSocket 或等价网络库验收：仅在 userdata/metatable/registry/错误边界稳定后进入平台闭环。
- [ ] Linux `.so` fixture 构建和 require。
- [ ] macOS `.dylib` fixture 构建和 require。
- [ ] macOS `.so` 后缀候选验证。
- [ ] Windows `.dll` fixture 构建和 require。
- [ ] Windows `lua53.dll` shim 或等价 import library 方案落地。
- [ ] 在平台不可用时提供可跳过但明确原因的测试。

## 第八阶段：文档与发布边界

- [ ] 更新 `docs/COMPATIBILITY.md`，说明 native_modules 能力与默认 no-CGO 差异。
- [ ] 更新 `docs/RELEASE_LIMITS.md`，说明 native 模块安全风险。
- [ ] 更新 `docs/API.md`，展示嵌入方如何启用 native loader。
- [ ] 更新 README，对外链接 native module 文档。
- [ ] 增加脚本：
  - [ ] `scripts/build-native-fixtures.sh`
  - [ ] `scripts/test-native-modules.sh`
- [ ] 增加最终验收记录。

## 每轮推进规则

- 每轮先执行：

```bash
git status --short --branch
```

- 每轮先读取：
  - `docs/NATIVE_MODULES_PLAN.md`
  - `docs/NATIVE_MODULES_TODO.md`

- 每轮只做一个可验证小切口。
- 修改 Go 代码前后使用 `gopls check` 或命令行 `gopls`。
- 新建或修改 Go 文件后执行 `gofmt` 并立即 `git add`。
- 默认构建相关变更必须执行：

```bash
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'
```

- native 相关变更至少执行对应定向测试：

```bash
CGO_ENABLED=1 go test -tags native_modules ./...
```

- 涉及 CLI、bytecode、VM、stdlib、compiler 或官方兼容行为时，重建 `bin/glua` / `bin/gluac` 并跑官方兼容脚本。

## 自动任务输出要求

每次自动任务执行完成后必须主动输出：

- 本轮实现点或证伪点。
- 涉及文件。
- 支持的平台范围。
- 默认 no-CGO 是否受影响。
- native 构建验证结果。
- 剩余 TODO。
- 如提交，报告 commit hash。

## 第一轮执行记录

- 2026-07-06：创建方案文档和 TODO 文档；确认现有 `PackageDynamicLibraryLoader`、`package.searchers` 和 CLI `lua.DefaultOptions()` 是后续接入点。本轮未修改 Go 代码，默认 no-CGO 行为不变。
- 2026-07-06：复制 Lua 5.3.6 public headers 到 `native/lua53/include/`，并补充来源说明。本轮未修改 Go 代码，默认 no-CGO 行为不变。
- 2026-07-06：新增 `internal/native.Loader()` 包骨架；默认构建返回 nil，`native_modules` 构建返回明确未实现错误分类。已补默认与 native tag 定向测试，尚未接入 CLI。
- 2026-07-06：新增 `docs/NATIVE_MODULES_BUILD.md`，说明默认构建、native 构建、平台前置条件、当前限制和后续 fixture 验收命令。本轮未修改 Go 代码。
- 2026-07-06：新增 Linux/macOS `dlopen` / `dlsym` / `dlclose` 封装和系统库 smoke 测试；`Loader()` 现在能区分打开失败、符号缺失和已解析但 shim 未实现三类边界。默认门禁脚本改为只允许 `native_modules` build tag 文件使用 CGO。
- 2026-07-06：新增 Windows `LoadLibraryW` / `GetProcAddress` / `FreeLibrary` 封装和系统 DLL smoke 测试；本机非 Windows 环境通过 `GOOS=windows go test -c -tags native_modules ./internal/native` 做交叉编译验证，运行时 fixture 留到 Windows 平台闭环阶段。
- 2026-07-06：新增 Unix `package.loadlib` 真实 Lua C fixture 测试；fixture 使用 Lua 5.3 public `lua.h` 编译并导出 `luaopen_glua_native_smoke`，当前可证明动态库与符号解析已贯通到 package.loadlib，返回值停在 C API shim 未实现的 `init` 分类。
- 2026-07-06：补充真实第三方模块验收门禁；明确自编 fixture 只作为 loader smoke，最终兼容验收以 `lua-cjson` 为第一真实模块，并区分源码编译模块和官方 Lua 5.3 ABI 二进制模块。
- 2026-07-06：新增 `PackageDynamicLibraryLoaderForState` 状态感知 loader 工厂；`lua.OpenLibs` 注册 package 库时会优先绑定当前 State，为后续 `lua_State*` opaque handle 和 `luaopen_*` 调用提供正确 VM 上下文。
- 2026-07-06：新增 native opaque `lua_State*` handle 注册表；handle 使用 C 分配 token 作为 C 可见身份，Go 侧只保存 token 到 `runtime.State` 的映射，避免把 Go 指针传入 C，并覆盖 nil/closed State、查找和重复关闭测试。
