# Lua C 原生模块加载 TODO

本文跟踪 `glua` 通过可选 CGO/native shim 在 Linux、macOS、Windows 上直接 `require` Lua 5.3 C 扩展模块的实施进度。每轮自动推进必须先读取本文和 `docs/NATIVE_MODULES_PLAN.md`。

## 总目标

- 默认 no-CGO 构建保持现状并通过全部门禁。
- `native_modules` 构建支持直接 `require` 按 Lua 5.3 public C API 编写并导出 `luaopen_*` 的 `.so/.dylib/.dll`。
- Linux、macOS、Windows 均有 fixture 或平台脚本验收。
- `native_modules` 允许使用 CGO；项目侧 C shim、Lua public headers、fixture、真实模块验收源码和构建脚本必须全部入仓库，不能依赖系统 Lua C 开发包或测试时联网下载。

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
- 约束：native 构建可使用 `CGO_ENABLED=1`，但交叉编译验证必须显式记录目标平台、`GOOS`、`GOARCH`、`CC` 和跳过原因。

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
- [x] 增加仓库内 C 源码自包含清单，覆盖 shim、fixture 和真实模块验收源码。
  - 详见 `docs/NATIVE_MODULES_SOURCE_INVENTORY.md`；真实第三方模块源码仍作为第七阶段独立 TODO 跟踪。

## 第二阶段：动态库加载器

- [x] Linux/macOS 实现 `dlopen` / `dlsym` / `dlclose` 封装。
- [x] Windows 实现 `LoadLibraryW` / `GetProcAddress` / `FreeLibrary` 封装。
- [x] 动态库 loader 返回 Lua 可调用 loader，接入 `PackageDynamicLibraryLoader`。
  - [x] 已新增 State-aware loader 工厂入口，后续 native shim 可绑定当前 State 后返回 Lua callable。
- [x] `native_modules` 构建下 CLI 自动注入 State-aware native loader。
- [x] `package.loadlib(path, symbol)` 在 native 构建下可加载 fixture 入口。
  - [x] 已验证 Linux/macOS 真实 fixture 可解析到 `luaopen_*`，无状态 loader 仍返回 `init` 分类。
  - [x] 已验证 State-aware loader 可返回 callable 并实际调用 fixture `luaopen_*`。
- [x] `require("mod")` 在 native 构建下可通过 `package.cpath` 命中 fixture。
- [x] 保持默认构建 `package.loadlib` 禁用说明不变。

## 第三阶段：最小 Lua C API shim

- [x] 设计 opaque `lua_State*` handle 与 Go State 映射。
- [ ] 实现 C API 栈基本操作：
  - [x] `lua_gettop`
  - [x] `lua_settop`
  - [x] `lua_checkstack`
  - [x] `lua_pushvalue`
  - [x] `lua_pushnil`
  - [x] `lua_pushboolean`
  - [x] `lua_pushinteger`
  - [x] `lua_pushnumber`
  - [x] `lua_pushlstring`
  - [x] `lua_pushstring`
  - [x] `lua_pushfstring`
    - [x] 当前通过 C wrapper 处理 varargs 和 `vsnprintf` 格式化，再由 Go helper 压入 Lua string 并返回 State 生命周期内的 C 字符串指针。
  - [x] `lua_pushvfstring`
    - [x] 与 `lua_pushfstring` 共用 C wrapper 格式化路径，供直接使用 `va_list` 的 C 模块调用。
- [ ] 实现 table 和 newlib 基础：
  - [x] `lua_createtable`
  - [x] `lua_setfield`
  - [x] `lua_getfield`
  - [x] `lua_gettable`
    - [x] 当前覆盖 table raw 查询、弹出栈顶 key 并压入结果；`__index` 元方法语义后续按真实模块需求扩展。
  - [x] `lua_rawset`
  - [x] `lua_next`
  - [x] `luaL_newlib`
    - [x] 当前通过 `luaL_setfuncs` 覆盖 Lua 5.3 头文件中的 `luaL_newlib` 宏展开路径，并支持 `nup >= 0` 的 C closure upvalue 复制。
- [ ] 实现基础参数检查：
  - [x] `luaL_checkinteger`
  - [x] `luaL_optinteger`
  - [x] `luaL_checklstring`
  - [x] `luaL_checkany`
  - [x] `luaL_checktype`
  - [x] `luaL_argerror`
  - [x] `luaL_checkoption`
  - [x] `luaL_error`
- [x] 实现 lauxlib buffer 基础：
  - [x] `luaL_buffinit`
  - [x] `luaL_prepbuffsize`
  - [x] `luaL_addlstring`
  - [x] `luaL_addstring`
  - [x] `luaL_addvalue`
  - [x] `luaL_pushresult`
  - [x] `luaL_pushresultsize`
  - [x] `luaL_buffinitsize`
- [x] fixture：C 模块 `luaopen_glua_native_smoke` 返回 table，并暴露一个简单函数。

## 第四阶段：C function 调用

- [x] 实现 `lua_CFunction` 到 Go VM callable 的包装。
  - [x] 当前覆盖 `nup == 0` 的 C function；C closure upvalue 后续随 registry/upvalue 阶段补齐。
- [ ] 实现：
  - [x] `lua_pushcclosure`
    - [x] 当前覆盖 `nup >= 0`，通过 `GoClosureWithUpvalues` 保存 C closure upvalue，并支持 `lua_upvalueindex(i)` 读取。
  - [x] `lua_pushcfunction`
  - [x] `lua_type`
  - [x] `lua_typename`
  - [x] `lua_toboolean`
  - [x] `lua_tointegerx`
  - [x] `lua_tonumberx`
  - [x] `lua_tolstring`
  - [x] `lua_callk` / `lua_call`
    - [x] Lua 5.3 public header 的 `lua_call` 宏会展开到 `lua_callk`；当前实现覆盖非 yield、非 continuation 路径。
  - [x] `lua_compare`
    - [x] 当前覆盖 EQ raw equality，以及 number/string 的 LT/LE 基础比较；元方法比较后续按真实模块需求扩展。
  - [x] `lua_rawequal`
    - [x] 当前覆盖 runtime raw equality，不触发 `__eq` 元方法，无效索引返回 false。
  - [ ] `lua_rawlen`
    - [ ] LPeg 1.1.0 当前运行期探针已越过 `_lua_rawequal`，阻塞前移到 `_lua_rawlen`。
  - [x] `lua_copy`
    - [x] 当前覆盖普通栈槽复制，以及 C function 调用帧内正索引相对基址复制；pseudo-index 目标写入留到完整 API 阶段。
  - [x] `lua_getallocf`
    - [x] 当前返回 native shim 的 C heap `realloc/free` 分配器，`ud` 固定为 `NULL`；fixture 覆盖 allocate/reallocate/free roundtrip。
  - [ ] `lua_is*` 系列常用入口：
    - [x] `lua_isstring`
      - [x] 当前按 Lua 5.3 语义对 string 和 number 返回 true，对 nil、boolean、table、none 返回 false。
- [x] 支持 C function 读取 Lua 参数并返回多值。
- [x] fixture：C 模块函数 `add(a, b)`、`echo(s)`、`multi()`。

## 第五阶段：userdata、metatable、registry

- [ ] 实现 userdata：
  - [x] `lua_newuserdata`
  - [x] `lua_touserdata`
  - [x] `luaL_checkudata`
  - [x] `lua_getuservalue`
    - [x] 当前覆盖 native full userdata 的 user value 压栈和类型码返回；非 full userdata、lightuserdata、无效索引按 nil 回退。
- [ ] 实现 metatable：
  - [x] `luaL_newmetatable`
  - [x] `luaL_getmetatable`
  - [x] `lua_setmetatable`
  - [x] `lua_getmetatable`
- [ ] 实现 registry/ref：
  - [x] `luaL_ref`
  - [x] `luaL_unref`
  - [x] `lua_rawgeti`
  - [x] `lua_rawseti`
- [x] fixture：C 模块创建 userdata，方法调用后能保持状态。

## 第六阶段：错误、pcall、traceback

- [x] 将 `lua_error` / `luaL_error` 转换为 Go VM runtime error。
- [x] 在 native C function 调用 wrapper 内建立 C 层 `setjmp` 边界，使 `lua_error` / `luaL_error` / `luaL_argerror` 对 C 模块表现为不返回，并在 Go 边界转换为 VM runtime error。
- [x] 实现 `lua_pcallk` 的非 yield protected call 路径，覆盖 C 模块内部 `lua_pcall` 错误封装。
- [x] 验证 `pcall(require, "mod")` 捕获 C module 初始化错误。
- [x] 验证 C function 运行时错误包含合理 traceback。
- [x] 定义 C frame 在 `debug.traceback` 中的展示策略。
- [x] 记录暂不支持或语义有差异的 C API。

## 第七阶段：平台闭环

- [ ] 真实第三方模块验收：
  - [x] 明确自编 fixture 只作为 loader smoke，不作为最终兼容性依据。
  - [x] 固定 `lua-cjson` 源码到仓库或 `third_party/`，记录来源、版本和许可证，构建不得联网下载。
  - [x] `lua-cjson` 源码编译验收：`require("cjson")`、`encode/decode`、错误输入 `pcall`。
    - [x] 新增 `scripts/build-native-cjson.sh`，使用仓库内 Lua 5.3 public headers 和固定源码编译当前平台 `cjson` 动态模块。
    - [x] 运行期 `require("cjson")`、`encode/decode` 和错误输入 `pcall` 验收。
      - [x] macOS `.so` 与 `.dylib` 两种后缀分别独立验收。
    - [x] 新增 `scripts/test-native-cjson.sh`，把真实模块运行期验收固化为可重复 CLI 脚本。
  - [ ] `lua-cjson` 官方 Lua 5.3 ABI 二进制模块验收：验证 `lua_*` / `luaL_*` 符号由本项目 shim 满足。
  - [x] 固定 `lpeg` 或等价纯 C 模块源码到仓库或 `third_party/`，记录来源、版本和许可证。
    - [x] 新增 `scripts/build-native-lpeg.sh`，使用仓库内 Lua 5.3 public headers 和固定源码编译当前平台 `lpeg` 动态模块。
  - [ ] `lpeg` 或等价纯 C 模块验收：覆盖 userdata、metatable、registry 和复杂 C function 行为。
  - [ ] LuaSocket 或等价网络库验收：仅在 userdata/metatable/registry/错误边界稳定后进入平台闭环。
- [ ] 增加交叉编译验证脚本：
  - [x] `scripts/check-native-cross-compile.sh`：显式输出 `GOOS`、`GOARCH`、`CC`、产物路径和缺失 toolchain 时的 skip 原因。
    - [x] 支持 `NATIVE_CC_*` / `CC` 使用带参数的编译器命令，便于 CI 传入 `zig cc -target ...` 或等价 cross toolchain。
  - [ ] Linux native build/test 编译验证。
  - [x] macOS native build/test 编译验证。
  - [ ] Windows native build/test 编译验证。
- [ ] Linux `.so` fixture 构建和 require。
- [x] macOS `.dylib` fixture 构建和 require。
- [x] macOS `.so` 后缀候选验证。
- [ ] Windows `.dll` fixture 构建和 require。
- [ ] Windows `lua53.dll` shim 或等价 import library 方案落地。
- [ ] 在平台不可用时提供可跳过但明确原因的测试。

## 第八阶段：文档与发布边界

- [x] 更新 `docs/COMPATIBILITY.md`，说明 native_modules 能力与默认 no-CGO 差异。
- [x] 更新 `docs/RELEASE_LIMITS.md`，说明 native 模块安全风险。
- [x] 更新 `docs/API.md`，展示嵌入方如何启用 native loader。
- [x] 更新 README，对外链接 native module 文档。
- [ ] 增加脚本：
  - [x] `scripts/build-native-fixtures.sh`
  - [x] `scripts/test-native-modules.sh`
  - [x] `scripts/build-native-cjson.sh`
  - [x] `scripts/test-native-cjson.sh`
  - [x] `scripts/build-native-lpeg.sh`
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
- 2026-07-06：新增最小 C API 栈 shim：`lua_gettop`、`lua_settop` 和基础 `lua_push*` 可通过 opaque handle 操作 Go State 栈；当前对无效/关闭 State 采取 no-op/0 的失败安全策略，`lua_error`/longjmp 错误边界后续阶段补齐。
- 2026-07-06：更正 CGO 边界：默认构建继续禁用 CGO；`native_modules` 为 Lua C 模块加载允许使用 CGO，但项目侧 C shim、fixture、真实模块验收源码和构建脚本必须全部随仓库提交，并补充跨平台交叉编译验证 TODO。
- 2026-07-06：新增最小 table 字段 C API shim：`lua_createtable`、`lua_setfield`、`lua_getfield` 可创建 Go table 并按 string key 读写字段；当前字段路径使用 raw table 语义，元方法和错误 longjmp 留到后续阶段。
- 2026-07-06：新增 integer 参数检查/转换 shim：`lua_tointegerx` 与 `luaL_checkinteger` 可读取 number/integer 栈值；当前不做字符串转数字，也不在失败时 longjmp，后续与 `luaL_error` 一并补齐。
- 2026-07-06：新增字符串转换/检查 shim：`lua_tolstring` 与 `luaL_checklstring` 返回绑定到 native State handle 生命周期的 C 分配 buffer，支持 string 和 number-to-string；当前不回写 number 栈槽，失败时也暂不 longjmp，后续与 `luaL_error` 一并补齐。
- 2026-07-06：新增类型、truthiness 和 number 转换 shim：`lua_type`、`lua_typename`、`lua_toboolean`、`lua_tonumberx` 可区分 `LUA_TNONE` 与 `nil`，并按 Lua 5.3 规则读取 boolean/number；当前 `lua_tonumberx` 只覆盖 integer/float number，不做字符串转数字，错误 longjmp 留到 `luaL_error` 阶段。
- 2026-07-06：新增 `lua_CFunction` 最小包装：`lua_pushcclosure`/`lua_pushcfunction` 可把 `nup==0` 的 C 函数指针压为 Go VM 可调用 closure；调用时临时把 Go 参数压入 native State 栈，执行 C 函数后按返回数量取结果并恢复调用前栈顶。当前不支持 C closure upvalue，`nup>0` 保持 no-op，`lua_error`/`luaL_error` 和 C frame traceback 留到错误阶段。
- 2026-07-06：新增 `luaL_setfuncs` 与兼容 `luaL_newlib` 符号；Lua 5.3 public header 的 `luaL_newlib` 宏会展开为 `lua_createtable` + `luaL_setfuncs`，因此当前 C 模块可把 `nup==0` 的 `luaL_Reg` 函数表注册到 table。带 upvalue 的 `luaL_setfuncs` 仍保持 no-op，等待 C closure upvalue/registry 阶段补齐。
- 2026-07-06：新增 `LoaderForState`，动态库符号解析后会保留库句柄并返回可调用 Go closure，调用时通过当前 State 的 opaque `lua_State*` 执行 `luaopen_*`。Unix fixture 已验证 `package.loadlib(path, "luaopen_glua_native_smoke")` 可返回 callable 并实际调用入口；无状态 `Loader()` 仍只做解析验证并返回 `init` 边界，防止误用错误 State。
- 2026-07-06：扩展 Unix native smoke fixture；C 模块现在通过真实 Lua 5.3 public header 的 `luaL_newlib` 宏返回模块 table，并暴露 `add(a,b)`、`echo(s)`、`multi()` 三个 C function，覆盖 integer/string 参数读取、`lua_push*` 返回值和 C function 多返回值搬运。为支持 `luaL_newlib` 宏补充 `luaL_checkversion_` 最小 no-op shim；版本不匹配错误与 longjmp 仍留到错误边界阶段。
- 2026-07-06：补齐 C function 调用基址和 Unix `require` 端到端测试；native shim 现在会在进入 C function 时记录 Go State 栈基址，使 `luaL_checkinteger(L, 1)` 等正索引相对当前 C 调用帧，而不是误读外层 Lua 栈。Lua 源码层已验证 `package.cpath` 命中 `glua_native_smoke` fixture、`require` 返回模块 table、`add/echo/multi` 可调用且二次 `require` 命中缓存。
- 2026-07-06：补齐 `lua_getuservalue`；native full userdata 可按 Lua 5.3 C API 把 user value 压栈并返回类型码，默认零值为 nil，lightuserdata/非 userdata/无效 State 回退 nil。LPeg 1.1.0 运行期探针已从 `_lua_getuservalue` 前移到 `_lua_isstring`。
- 2026-07-06：补齐 `lua_isstring`；native shim 按 Lua 5.3 C API 可转换性语义判断 string 和 number 为真，nil、boolean、table、none 为假。LPeg 1.1.0 运行期探针已从 `_lua_isstring` 前移到 `_lua_pushfstring`。
- 2026-07-06：补齐 `lua_pushfstring` / `lua_pushvfstring`；C wrapper 负责 varargs / `va_list` 格式化，Go helper 负责压入格式化后 Lua string 并返回绑定到 State 生命周期的 C 字符串指针。LPeg 1.1.0 运行期探针已从 `_lua_pushfstring` 前移到 `_lua_rawequal`。
- 2026-07-06：补齐 `lua_rawequal`；native shim 按 Lua 5.3 raw equality 语义比较两个栈值，不触发元方法，无效索引返回 false 且不留下 pending error。LPeg 1.1.0 运行期探针已从 `_lua_rawequal` 前移到 `_lua_rawlen`。
- 2026-07-06：接入 CLI native loader 注入点；默认构建下 `applyNativeModuleOptions` 保持 no-op，`native_modules` 构建下 CLI 创建 State 时自动设置 `PackageDynamicLibraryLoaderForState = native.LoaderForState`，并保留无状态 loader 作为 `package.loadlib` 解析诊断路径。该切口只影响 native tag 构建，默认 no-CGO 行为由专门测试锁定。
- 2026-07-06：新增 `lua_error` 与 `luaL_error` 最小错误传播；native shim 不跨 Go/C 边界 longjmp，而是在 opaque `lua_State*` handle 上暂存 Lua error object，C function 返回 Go 边界后转换为 `runtime.RaiseError`。Unix fixture 已验证 `pcall(mod.fail, "boom")` 捕获 `luaL_error` 文本，`pcall(mod.raise)` 捕获 `lua_error` 栈顶对象。C module 初始化期 `pcall(require, "mod")` 与 C frame traceback 展示仍在后续错误阶段补齐。
- 2026-07-06：新增 C module 初始化错误验收；同一 Unix fixture 额外导出 `luaopen_glua_native_failopen`，并以模块名匹配的动态库文件走标准 `package.cpath` 和 `require` 路径。测试已验证 `pcall(require, "glua_native_failopen")` 捕获 luaopen 阶段的 `luaL_error("native open failure")`，且失败后不污染 `package.loaded`。
- 2026-07-06：新增 C function 运行时错误 traceback 验收；Unix require fixture 通过 `xpcall(function() mod.fail("trace") end, debug.traceback)` 验证 C function 中的 `luaL_error` 会进入现有 protected-call traceback 链，返回文本同时包含原始错误文本和 `stack traceback:`。C frame 具体如何在 traceback 中命名或展示仍保持独立策略 TODO，不在本切口伪造帧信息。
- 2026-07-06：定义 C frame traceback 展示策略；native C function 继续复用 Go closure 调试帧，函数名来自 Lua 调用点 `name/namewhat` 推断，不伪造 C 源码文件、C 行号、C 栈地址或动态库内部调用栈。该策略保持 `pcall`/`xpcall` 错误对象和默认 no-CGO 行为不变，后续若增加专用 native frame 元信息必须作为附加展示。
- 2026-07-06：新增 native full userdata 基础 API；`lua_newuserdata` 会在 C heap 分配可读写数据区、压入 `runtime.Userdata`，并把释放动作绑定到 `State.Close`，`lua_touserdata` 只对 native shim 创建的 full userdata 返回同一 C 指针。当前尚未支持 `luaL_checkudata`、metatable 与 registry，因此真实第三方模块仍需等后续阶段闭环。
- 2026-07-06：新增 native raw metatable 基础 API；`lua_setmetatable` / `lua_getmetatable` 已支持 table、native userdata 和 runtime 已有基础类型共享元表槽，成功设置时按 Lua C API 弹出栈顶 metatable。当前 `luaL_newmetatable` / `luaL_getmetatable` 的 registry 命名元表和 `luaL_checkudata` 类型检查仍待后续接入。
- 2026-07-06：新增 registry 命名元表基础 API；`luaL_newmetatable` 可在 registry 中按类型名创建/复用元表并保持 Lua C API 返回值语义，`luaL_getmetatable` 复用 `lua_getfield(L, LUA_REGISTRYINDEX, name)` 语义并额外导出兼容符号。当前 `luaL_checkudata` 还未比较 userdata raw 元表与命名元表，registry integer ref 仍待 `luaL_ref` / `lua_rawgeti` 阶段接入。
- 2026-07-06：新增 `luaL_checkudata` 最小类型检查；成功路径按 Lua 5.3 规则比较 userdata raw metatable 与 `registry[tname]` identity，并返回 native full userdata 的 C 数据区指针。失败路径当前记录 pending error 并返回 nil，等待 C function 返回边界传播；尚未实现 C 层 longjmp，因此不应把失败返回 nil 的行为视为完整 lauxlib 错误语义。
- 2026-07-06：新增 `lua_rawgeti` / `lua_rawseti` 最小 raw integer API；支持普通 table 和 registry pseudo-index，按 integer key 直接读写且不触发元方法，`rawseti` 成功时弹出栈顶 value。当前无效目标仍保持 no-op/none，后续 api_check/错误边界统一收口。
- 2026-07-06：新增 `luaL_ref` / `luaL_unref` 最小 registry 引用 API；按 Lua 5.3 lauxlib 语义处理 `LUA_REFNIL` / `LUA_NOREF`、`t[0]` freelist 复用和 `#t+1` 追加分配。当前非法 table 目标返回 no-ref/no-op，后续 api_check/longjmp 阶段统一收口。
- 2026-07-06：补齐 `lua_pushvalue` 最小栈复制 API，并扩展 Unix smoke fixture 的 `glua_native_counter` userdata；fixture 通过 `luaL_newmetatable`、`lua_pushvalue`、`lua_setfield("__index")`、`luaL_setfuncs` 和 `luaL_checkudata` 验证 `counter:add()` / `counter:get()` 方法调用后状态可持续保存。
- 2026-07-06：新增 `docs/NATIVE_MODULES_SOURCE_INVENTORY.md`，明确 Lua public headers、Go/CGO shim、loader、内嵌 fixture 的已入仓状态，并列出独立 fixture C/Lua 文件、构建脚本、交叉编译脚本、`lua-cjson`、`lpeg` 和 Windows shim 等尚未入仓项。该切口只补自包含边界文档，不改变 Go/native 运行时代码。
- 2026-07-06：将 Unix smoke fixture 的 C 源码从 Go 测试内嵌字符串迁移到 `tests/native_modules/fixtures/glua_native_smoke.c`；`internal/native/loadlib_fixture_unix_test.go` 现在直接编译仓库内固定 C 文件，减少后续脚本和跨平台 fixture 复用前的重复源码入口。
- 2026-07-06：将 Unix require smoke 的 Lua 脚本从 Go 测试内嵌字符串迁移到 `tests/native_modules/fixtures/glua_native_smoke.lua`；Go 测试通过占位符注入 `package.path` / `package.cpath`，后续 CLI smoke 和跨平台脚本可复用同一份 Lua 验收逻辑。
- 2026-07-06：新增 `scripts/build-native-fixtures.sh`，使用仓库内 `native/lua53/include/` 和 `tests/native_modules/fixtures/glua_native_smoke.c` 构建当前平台 `glua_native_smoke` / `glua_native_failopen` 动态库，并显式输出 `GOOS`、`GOARCH`、`CC`、`CGO_ENABLED`、源码路径和产物路径；Windows 目标在 `lua53.dll` shim/import library 落地前明确 skip。
- 2026-07-06：新增 `scripts/test-native-modules.sh`，默认构建 native tag 的 `glua`，调用 `scripts/build-native-fixtures.sh` 生成当前平台 fixture，并实际执行 `glua_native_smoke.lua` require 成功路径和 `glua_native_failopen` 初始化失败路径；Windows CLI smoke 在 `lua53.dll` shim/import library 落地前明确 skip。
- 2026-07-06：新增 `scripts/check-native-cross-compile.sh`，按 `NATIVE_CROSS_TARGETS` 或默认当前架构的 Linux/macOS/Windows 目标编译 `internal/native` 测试二进制和 native tag `cmd/glua`，并为缺失 C toolchain 的目标显式输出 skip 原因；脚本只做编译级闭环，不运行异平台产物。
- 2026-07-06：固定 `lua-cjson` 真实模块源码到 `third_party/lua-cjson/`，来源为 upstream `https://github.com/mpx/lua-cjson` tag `2.1.0`、commit `4bc5e917c8cd5fc2f6b217512ef530007529322f`，保留 MIT-style `LICENSE` 并新增 `GLUA_VENDOR.md` 记录本项目未做源码修改；源码编译与 `require("cjson")` 验收仍作为后续独立切口。
- 2026-07-06：新增 `scripts/build-native-cjson.sh`，直接使用仓库内 `native/lua53/include/` 与 `third_party/lua-cjson/` 固定源码编译当前平台 `cjson` 动态模块；macOS 同时产出 `.so` 与 `.dylib`，Linux 产出 `.so`，Windows 在 `lua53.dll` shim/import library 落地前明确 skip。该切口只证明真实模块源码可自包含编译，`require("cjson")`、`encode/decode` 和错误输入 `pcall` 仍等待 Lua C API shim 覆盖后验收。
- 2026-07-06：补齐 `lua-cjson` 运行期所需的最小 Lua 5.3 public C API：C closure upvalue、`lua_upvalueindex` 读取、`luaL_setfuncs(nup>=0)`、`lua_checkstack`、`lua_rotate`、`lua_pushlightuserdata`、`lua_rawset`、`lua_next`、`luaL_argerror`、`luaL_checkoption`、`lua_pcallk` 非 yield 路径，并把 `lua_error` / `luaL_error` / `luaL_argerror` 改为 C 层 `setjmp` 边界内不返回。当前 macOS arm64 已用仓库内 `lua-cjson` 源码完成 `require("cjson")`、`encode/decode` 和错误输入 `pcall(cjson.decode, "{")` 验收；默认 no-CGO 构建仍需本轮完整门禁确认。
- 2026-07-06：新增 `scripts/test-native-cjson.sh`，把 `lua-cjson` 真实模块运行期验收固化为脚本：脚本先构建 native tag `glua` 和仓库内 `third_party/lua-cjson` 动态模块，再执行 `require("cjson")`、对象/数组/标量 `encode/decode`、`cjson.null` identity、非法 JSON `pcall` 和不可序列化 function `pcall`。当前 macOS arm64 验收通过；Windows 仍在 `lua53.dll` shim/import library 落地前明确 skip。
- 2026-07-06：扩展 fixture 构建与 CLI smoke 脚本；macOS 现在同时产出 `glua_native_smoke` / `glua_native_failopen` 的 `.dylib` 和 `.so` 两种后缀，并分别通过 `package.cpath` 执行 require 成功路径与 luaopen 初始化失败路径，覆盖 Lua 生态在 macOS 上常见的双后缀候选。
- 2026-07-06：执行 `scripts/check-native-cross-compile.sh`；当前 macOS arm64 已完成 `internal/native` 测试二进制和 native tag `cmd/glua` 编译验证，Linux arm64 与 Windows arm64 因未配置 `NATIVE_CC_LINUX_ARM64` / `NATIVE_CC_WINDOWS_ARM64` 或 `CC` 明确 skip，未冒充跨平台通过。
- 2026-07-06：扩展 `scripts/test-native-cjson.sh`；macOS 上不再用合并 `package.cpath` 只验证搜索顺序首个候选，而是分别用 `?.so` 和 `?.dylib` 独立执行 `require("cjson")`、对象/数组/标量 `encode/decode`、`cjson.null`、非法 JSON `pcall` 和不可序列化 function `pcall`，真实第三方模块双后缀运行期验收均通过。
- 2026-07-06：更新 `docs/NATIVE_MODULES_BUILD.md` 的当前支持面与限制；明确已覆盖的 Lua 5.3 public C API、非 yield `lua_pcallk` 边界、未支持的 C continuation/debug hook/全局表/加载与 GC API、`luaL_checkversion_` 的当前语义，以及官方 Lua 5.3 ABI 二进制模块和 Linux/Windows 运行期验收仍未闭环。
- 2026-07-06：更新 `docs/COMPATIBILITY.md`；将默认纯 Go/no-CGO 兼容口径与 `native_modules` 可选构建口径分离，记录 macOS arm64 `lua-cjson` `.so/.dylib` 真实模块验收状态、public C API 边界、Linux/Windows 未闭环项和 native 本机代码执行风险。
- 2026-07-06：更新 `docs/API.md`；补充 `PackageDynamicLibraryLoaderForState` 对外配置口径，说明默认 no-CGO 嵌入保持纯 Go，`native_modules` 下 Lua C 模块必须通过 state-aware loader 绑定当前 VM state，并明确仓库内 `internal/native` 不是外部 module 可直接 import 的公开 Go API。
- 2026-07-06：更新 `docs/RELEASE_LIMITS.md`；将 Lua C 原生模块从未立项旧口径改为显式 `native_modules` 可选构建口径，补充 macOS arm64 `lua-cjson` 验收状态、Linux/Windows/官方 ABI 未闭环限制，并新增 native 模块本机代码执行、`package.cpath` 搜索路径和 C 级崩溃不可由 `pcall` 隔离的安全边界。
- 2026-07-06：更新 `README.md`；将动态库与 `require` 边界改为默认 no-CGO 与显式 `native_modules` 可选构建并列口径，并在对外文档列表中加入 `NATIVE_MODULES_PLAN.md`、`NATIVE_MODULES_BUILD.md` 和 `NATIVE_MODULES_SOURCE_INVENTORY.md`。
- 2026-07-06：收敛 fixture 兼容边界 TODO；`docs/NATIVE_MODULES_BUILD.md` 已说明 fixture 只验证 loader smoke、不作为最终兼容结论，`docs/NATIVE_MODULES_SOURCE_INVENTORY.md` 已说明自编 fixture 只证明 loader、opaque `lua_State*`、基础 C API shim 和 require 链路贯通，不能解释为任意第三方 Lua C 模块兼容。
- 2026-07-06：增强 `scripts/check-native-cross-compile.sh`；`NATIVE_CC_*` / `CC` 现在可传入带参数的编译器命令，脚本只校验第一个命令词是否存在，完整命令仍原样传给 Go/cgo，方便后续 Linux/Windows CI 使用 `zig cc -target ...` 或等价 cross toolchain。
- 2026-07-06：固定第二真实模块 LPeg 1.1.0 到 `third_party/lpeg/`，新增 `GLUA_VENDOR.md` 记录官方源码包 URL、版本、许可位置和本项目未改源码；该切口只完成源码自包含门禁，尚未声明 `require("lpeg")` 运行期验收通过。
- 2026-07-06：新增 `scripts/build-native-lpeg.sh`，直接使用仓库内 `native/lua53/include/` 与 `third_party/lpeg/` 固定源码编译当前平台 `lpeg` 动态模块；macOS 同时产出 `.so` 与 `.dylib`，Linux 产出 `.so`，Windows 在 `lua53.dll` shim/import library 落地前明确 skip。该切口只证明 LPeg 源码可自包含编译，`require("lpeg")` 运行期验收仍等待独立脚本闭环。
- 2026-07-06：复核 `package.loadlib` 门禁并同步 TODO；`go test ./stdlib/package -run 'TestLoadLibDisabled|TestCLoadingPolicyDocumentsUnsupportedDynamicLibraries|TestLoadLibUsesDynamicLibraryLoader'` 确认默认 no-CGO 禁用说明和宿主 loader 覆盖稳定，`CGO_ENABLED=1 go test -tags native_modules ./internal/native -run 'TestUnixPackageLoadLibResolvesNativeFixture|TestUnixPackageLoadLibReturnsCallableNativeFixture'` 确认 native fixture 可通过 `package.loadlib` 解析并在 state-aware loader 下调用。LPeg 运行期探测已进入动态库打开阶段，但当前被 `_luaL_addlstring` 等尚未导出的 public C API 阻塞，后续需按 `luaL_Buffer`、table/user value、raw length/equality/call 等 API 组分批补齐。
- 2026-07-06：新增 `luaL_Buffer` 基础 API 组：`luaL_buffinit`、`luaL_prepbuffsize`、`luaL_addlstring`、`luaL_addstring`、`luaL_addvalue`、`luaL_pushresult`、`luaL_pushresultsize`、`luaL_buffinitsize`。本轮用仓库内 LPeg 1.1.0 源码重新编译 macOS arm64 `.so/.dylib` 并执行 `require("lpeg")` 运行期探针，确认阻塞点已从 `_luaL_addlstring` 前移到 `_luaL_checkany`；说明 buffer 符号组已由本项目 shim 满足，LPeg 完整验收仍待参数检查、table/user value、raw length/equality/call 等剩余 API 组继续补齐。
- 2026-07-06：新增 `luaL_checkany` 参数存在性检查；`nil` 参数按 Lua 5.3 语义视为存在，只有缺失参数记录 `bad argument #n (value expected)` pending error。LPeg 1.1.0 macOS arm64 运行期探针确认阻塞点已从 `_luaL_checkany` 前移到 `_luaL_checktype`，下一轮应继续补齐类型检查 API。
- 2026-07-06：新增 `luaL_checktype` 基础类型检查；成功路径按 `lua_type` 类型编号比较，失败路径记录 `bad argument #n (<expected> expected, got <actual>)` pending error。LPeg 1.1.0 macOS arm64 运行期探针确认阻塞点已从 `_luaL_checktype` 前移到 `_luaL_optinteger`，下一轮应继续补齐 optional integer 参数 API。
- 2026-07-06：新增 `luaL_optinteger` 可选整数参数读取；缺失参数和 `nil` 返回调用方默认值，存在但不可转整数的参数记录 `bad argument #n (integer expected)` pending error。LPeg 1.1.0 macOS arm64 运行期探针确认阻塞点已从 `_luaL_optinteger` 前移到 `_lua_callk`，下一轮应优先补齐非 yield `lua_callk` / `lua_call` 调用路径。
- 2026-07-06：新增 `lua_callk` / `lua_call` 非 protected 调用路径；成功时按 `nresults` 搬运返回值，失败时记录 pending error 并等待当前 C function 返回边界传播，不实现 yield continuation。LPeg 1.1.0 macOS arm64 运行期探针确认阻塞点已从 `_lua_callk` 前移到 `_lua_compare`，下一轮应补齐 Lua 5.3 compare API。
- 2026-07-06：新增 `lua_compare` 基础比较 API；`LUA_OPEQ` 覆盖 raw equality，`LUA_OPLT` / `LUA_OPLE` 覆盖 number 与 string 基础比较，不可比较类型记录 pending error。LPeg 1.1.0 macOS arm64 运行期探针确认阻塞点已从 `_lua_compare` 前移到 `_lua_copy`，下一轮应补齐栈槽复制 API。
- 2026-07-06：新增 `lua_copy` 栈槽复制 API；覆盖普通栈目标替换、不改变栈顶、无效索引 no-op，以及 C function 调用帧内正索引相对基址的复制语义。LPeg 1.1.0 macOS arm64 运行期探针确认阻塞点已从 `_lua_copy` 前移到 `_lua_getallocf`，下一轮应补齐 allocator 查询 API。
- 2026-07-06：新增 `lua_getallocf` allocator 查询 API；返回 native shim 的 C heap `realloc/free` 分配器且 `ud == NULL`，native smoke fixture 新增 `alloc_roundtrip()` 覆盖 C 模块侧分配、扩容、释放。LPeg 1.1.0 macOS arm64 运行期探针确认阻塞点已从 `_lua_getallocf` 前移到 `_lua_gettable`，下一轮应补齐通用表读取 API。
- 2026-07-06：新增 `lua_gettable` 通用 table 读取 API；覆盖栈顶 key 查询、弹 key、压结果、返回 Lua C API 类型编号，以及无效目标 no-op 的当前最小 shim 边界。LPeg 1.1.0 macOS arm64 运行期探针确认阻塞点已从 `_lua_gettable` 前移到 `_lua_getuservalue`，下一轮应补齐 userdata user value API。
