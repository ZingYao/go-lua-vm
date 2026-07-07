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
  - [x] `lua_settable`
    - [x] 当前覆盖普通 table 栈顶 key/value 写入，支持已有 raw 字段覆盖和 table/Go function 形式 `__newindex`。
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
  - [x] `lua_rawlen`
    - [x] 当前覆盖 string 字节长度、table raw length 和 native full userdata 分配块大小，不触发 `__len` 元方法。
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
  - [x] `lua_setuservalue`
    - [x] 当前覆盖 native full userdata 的 user value 写入；失败路径保持栈顶值不被消费。
    - [x] 导出 C ABI 保持 Lua 5.3 public header 的 `void lua_setuservalue(lua_State*, int)` 签名。
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
    - [x] 新增 `scripts/test-native-lpeg.sh`，覆盖 `require("lpeg")` 和基础 pattern/match runtime smoke。
  - [ ] `lpeg` 或等价纯 C 模块验收：覆盖 userdata、metatable、registry 和复杂 C function 行为。
    - [x] macOS arm64 `.so` 与 `.dylib` 两种后缀已通过基础 LPeg runtime smoke。
    - [ ] 完整 `third_party/lpeg/test.lua`、复杂 capture、grammar 和错误边界仍待后续验收。
      - [x] 已修复完整测试在 1084 行触发的 `string.char("98")` Lua 5.3 numeric string 转 integer 兼容断点。
      - [ ] 当前完整测试推进到 1159 行；同一 pattern 独立运行返回 18，但完整前序状态后返回 12，疑似 LPeg match-time capture / named capture 前序状态污染，需继续缩小最小复现。
        - [x] 2026-07-06 复核：替换 1159 行断言为打印后，官方测试同路径下 `c:match('[==[]]====]]]]==]===[]')` 实际返回 `12`。
        - [x] 2026-07-06 复核：前缀执行到 656 行后，若沿用官方脚本的外层 `c = ...` 赋值形态，match-time callback 只在 position 12 触发并返回 `12`；独立脚本会尝试 position 7、12、13、14、15、18 并最终返回 `18`。
        - [x] 2026-07-06 复核：`local c = ...` shadow 形态与官方外层 `c = ...` 形态表现不同，说明问题不在动态库解析，而更可能在 native C frame 与 Go VM 嵌套执行 Lua callback 时的栈/寄存器隔离、`lua_call`、`lua_remove`/`lua_rotate` 或借用 VM 清理边界。
        - [x] 2026-07-06 复核：已在 native smoke fixture 中模拟 `runtimecap` 的 `lua_call(..., LUA_MULTRET)` + `lua_remove` 旧动态 capture 清理路径，macOS `.dylib`/`.so` 均通过；基础 C frame 可见 `lua_gettop` 与 `lua_remove` 清理未单独复现 1159 行错位。
        - [x] 2026-07-06 复核：已在 native smoke fixture 中加入多次 runtime callback、lightuserdata stackbase、旧动态 capture 多轮残留和外层 Lua 变量覆盖场景，macOS `.dylib`/`.so` 均通过；通用 C API 多轮 callback 与清理路径继续被证伪为非单独根因。
        - [x] 2026-07-06 复核：修正 C frame 内过深负索引边界，`lua_type`、`lua_pushvalue`、`lua_copy`、`lua_rotate` 均不得通过 `-3` 等越界负索引读写 frame base 之前的外层 Go VM 栈；该语义边界已由 `internal/native` 定向测试覆盖，但不直接宣称解决 LPeg 1159。
        - [x] 2026-07-06 复核：新增 `scripts/probe-native-lpeg-1159.sh` 固化定位证据；前缀跑到 `test.lua:558` 与 `test.lua:641` 后 probe 均返回 `18`，跑到 `test.lua:651` 与 `test.lua:1153` 后退化为 `12`，而孤立执行 `645-651` 的 deep stack overflow/success match 组合仍返回 `18`。根因窗口进一步收敛为 `1-641` 前序状态叠加 `645-651` 后的 LPeg/native 状态污染。
        - [x] 2026-07-06 复核：扩展 `scripts/probe-native-lpeg-1159.sh` 的细分诊断；前缀跑到 `test.lua:646` 仍返回 `18`，跑到 `test.lua:647` 首次 `checkerr("stack overflow", m.match, p, ...)` 后立即退化为 `12`。`p=nil`、两次 `collectgarbage()`、重新 `require("lpeg")`、`m.setmaxstack(1000000)` 后仍返回 `12`，因此已证伪递归 pattern 构造、成功 deep match、`lpeg-maxstack` 取值、Lua 层对象可达性和普通模块 reload 是单独根因。
        - [x] 2026-07-06 复核：继续扩展 `scripts/probe-native-lpeg-1159.sh`；将 `test.lua:647` 的 `checkerr` 拆为只执行 `pcall(m.match, p, ...)` 并检查错误文本，probe 仍退化为 `12`。因此 LPeg 错误文本上的后续 `m.match({ m.P(msg) + 1 * m.V(1) }, err)` 不是根因，污染发生在 overflow protected call 本身或其恢复边界。
        - [x] 2026-07-06 复核：在前缀跑到 `test.lua:620` 后只构造 `630-635` 的 parity grammar、不执行 `637-641` 的任一匹配，再执行 overflow `pcall`，probe 仍返回 `18`；补上 `637-641` 的 parity grammar 成功/失败匹配后，再执行同一个 pcall-only overflow，probe 才退化为 `12`。新窗口收敛为 `1-620` 前序状态 + parity grammar 多轮匹配 + overflow protected call；parity grammar 构造本身不是必要充分条件。
        - [x] 2026-07-06 复核：新增 native smoke fixture 模拟 LPeg `doublestack` overflow：按 `lp_match` 的 `ptop+1` nil、`ptop+2` capture lightuserdata、`ptop+3` ktable、`ptop+4` stackbase lightuserdata 形态，循环 `lua_newuserdata` + `lua_replace(stackidx)` 后 `luaL_error`，并在 Lua `pcall` 捕获后复跑 runtimecap sequence。macOS `.dylib`/`.so` 均通过，说明通用临时栈槽替换、full userdata 创建和 `luaL_error` 恢复未单独复现 1159 污染。
        - [x] 2026-07-06 复核：继续扩展 `scripts/probe-native-lpeg-1159.sh` 的跨模块控制组；`1-620` 前序状态后直接 `require("glua_native_smoke")` 仍返回 `18`，但 `1-620` 前序状态 + parity grammar 多轮匹配后仅 `require("glua_native_smoke")` 即退化为 `12`，不需要再触发 LPeg overflow 或 smoke `doublestack_overflow_probe`。根因窗口进一步前移为 parity grammar 多轮匹配后加载第二个 native C 模块时的跨模块 native 状态/registry/lightuserdata/full userdata 交互。
        - [x] 2026-07-06 复核：继续扩展 `scripts/probe-native-lpeg-1159.sh` 的 `package.loadlib` 控制组；fresh `require("lpeg")` 后对不存在文件执行 `package.loadlib(..., "luaopen_missing")` 仍返回 `18`，`1-620` 前序状态后执行成功 open/成功符号解析的 `package.loadlib` 也返回 `18`，但 `1-620` 前序状态后同一个 missing-file open-failure 路径退化为 `12`。这排除了 `luaopen_*` 执行、第二模块初始化、真实动态库成功加载、parity grammar 多轮匹配和 smoke fixture 逻辑是必要条件；当前窗口收敛为 `1-620` 前序状态后进入 native `package.loadlib` 动态库 open-failure 尝试路径。
        - [x] 2026-07-06 复核：继续扩展 `scripts/probe-native-lpeg-1159.sh` 的非 native 错误对照；`1-620` 前序状态后执行合成的 `nil, message, "open"` 三返回和 `package.loadlib` 参数错误路径均返回 `18`，但同一前序状态后对普通文本文件执行 `package.loadlib` open-failure 退化为 `12`。这排除了普通 Lua/Go 错误返回、`pcall` 参数错误和错误三返回对象本身是必要条件，进一步收敛为 native loader 的 `dlopen` 失败边界。
        - [x] 2026-07-06 复核：新增 Unix native loader 诊断阶段开关，仅对匹配文件名生效，避免误伤前置 `require("lpeg")`；`before-cstring`、`after-cstring`、`after-clear`、`after-dlopen-no-dlerror` 四组在 `1-620` 前序状态后均退化为 `12`，而成功符号解析仍保持 `18`。因此已证伪 `C.CString`/`C.free`、`dlerror` 清理、真实 `dlopen()` 调用和 `dlerror` 字符串读取是必要条件，问题继续前移到 `package.loadlib` 调用 native loader Go 回调并返回 `DynamicLibraryError` 的错误传播边界。
        - [x] 2026-07-06 复核：新增 native loader 顶层诊断开关；`DynamicLibraryError`、普通 `error`、nil+不可调用返回值、以及成功打开/解析 `glua_native_smoke` 后人工返回 open 错误四组均退化为 `12`，而成功返回 callable 的 `package.loadlib` 仍保持 `18`。因此已证伪错误类型、Go closure 返回 error 和真实动态库打开成功/失败本身是必要条件，当前边界进一步收敛为 `package.loadlib` 已调用 native loader 回调、随后进入失败三返回构造分支。
        - [x] 2026-07-06 复核：新增 `package.loadlib` 层诊断开关；`before-loader-fixed` 在合法参数解析后、不调用 native loader、直接返回固定 `nil,message,"open"` 三返回时已经退化为 `12`，`after-loader-fixed` 同样为 `12`，而成功 callable 路径仍为 `18`。因此已证伪 native loader 回调、`loadDynamicLibrary`、`dynamicLibraryFailure` 和错误分类转换是必要条件，边界继续前移到内置 `package.loadlib` 合法参数失败三返回路径本身。
        - [x] 2026-07-06 复核：继续扩展 `package.loadlib` 层诊断开关；`before-args-fixed` 在正式 `stringArgument` 参数解析前、只读取原始字符串参数并返回固定三返回时已退化为 `12`，`after-args-one-return` 只返回单个 `nil`、`after-args-two-return` 返回 `nil,message` 也都退化为 `12`。因此已证伪正式参数解析、返回数量、message 文本和第三返回 category 是必要条件，当前边界进一步收敛为“调用内置 `package.loadlib` 诊断/失败分支本身会扰动后续 LPeg match”，而不是失败返回值形态。
        - [x] 2026-07-06 复核：新增仅诊断模式注册的 `package._glua_loadlib_diag` 等价 Go closure，不经过 `Environment.LoadLib`、不调用 native loader；在 `1-620` 前序状态后，`one-return`、`two-return`、`three-return` 三组全部退化为 `12`，而成功符号解析的 `package.loadlib` 仍保持 `18`。因此已证伪 `package.loadlib` 方法接收者、环境捕获和特定内置函数实现是必要条件，边界继续收敛为前序 LPeg 状态后普通 Go closure 正常返回 nil/失败形态会扰动后续 match。
        - [x] 2026-07-06 复核：继续扩展等价 Go closure 诊断；`empty-return`、`true-return`、`string-return`、`table-return`、`callable-return` 在 package 表字段调用下全部退化为 `12`，全局 `_glua_loadlib_diag` 的 `one-return` 和 `callable-return` 也退化为 `12`，而成功符号解析的 `package.loadlib` 仍保持 `18`。因此已证伪首返回 nil、失败形态、返回值内容、返回值数量、package 表字段调用路径是必要条件；当前边界收敛为前序 LPeg 状态后任意普通 Go closure 被 Lua 调用并正常返回都会扰动后续 match。
        - [x] 2026-07-06 复核：新增 `scripts/probe-native-lpeg-1159.sh` 的 Go/Lua closure 对照组；`1-620` 前序状态后只读取 `package._glua_loadlib_diag` 函数值、不调用时仍返回 `18`，成功 `package.loadlib` 取得 native callable 但不调用 `luaopen_*` 也仍返回 `18`；但调用纯 Lua closure 的 `empty-return`、`one-return`、`three-return`、`callable-return` 均退化为 `12`。因此污染点不再局限于 Go closure、package closure 或 native loader，而是收敛到前序 LPeg 状态后任意 Lua 函数调用/返回边界触发的 VM 栈/寄存器/调用帧隔离问题。
        - [x] 2026-07-06 复核：继续扩展 `scripts/probe-native-lpeg-1159.sh` 的 Lua 调用帧对照组；该长线性 probe 一度显示空 Lua 函数、局部变量函数和 `pcall(function() end)` 均退化为 `12`，但后续独立短矩阵复核证明该结论不够隔离，保留为历史证据，不再作为最终 CALL 边界结论。
        - [x] 2026-07-06 复核：新增 `scripts/bisect-native-lpeg-1159-prefix.sh`，将 LPeg 1159 prefix 边界改为机械二分定位；默认 `LOW=620`、`HIGH=651` 下自动验证 `620` 为 good、`651` 为 bad，并输出 `last_good_prefix_line=646`、`first_bad_prefix_line=647`。后续 prefix 边界复核优先用该脚本，避免继续靠长线性 probe 手工排查。
        - [x] 2026-07-06 复核：新增 `scripts/probe-native-lpeg-1159-call-kinds.sh`，用独立短矩阵复核 `1-620` 前序状态后的单操作影响；只定义局部函数、匿名函数赋值、table 字段读取函数、不带副作用的空 Lua 调用、局部变量 Lua 调用、`type`、`tostring`、`pcall(function() end)` 和单次 LPeg parity match 均保持 `18`，只有 `select("#", "alpha", "beta")` 退化为 `12`；`select("#")` 和 `select(1, "alpha", "beta")` 仍为 `18`。当前边界修正为 base `select` 的非空变参数量查询路径或其返回帧清理，而不是普通 Lua closure/CALL 本身。
        - [x] 2026-07-07 复核：使用 `gopls check stdlib/base/base.go lua/api.go runtime/vm.go` 确认目标包无诊断；新增 `scripts/probe-native-lpeg-select-count.sh`，把 `1-620` 前序状态后的 `baseline`、`select("#")`、`select(1, ...)`、`select("#", "alpha", "beta")` 四组单独固化为可复跑 probe。当前输出显示前三组为 good，非空 `select("#", ...)` 为 bad，进一步排除 `select` 空计数和索引返回路径。
        - [x] 2026-07-07 复核：扩展 `scripts/probe-native-lpeg-select-count.sh` 的返回形态矩阵；普通 Lua 函数返回整数 `2`、Lua vararg 函数返回整数 `2`、Lua 函数内部 `return select("#", ...)`、`assert(2)` 和 `assert(2, "ok")` 均保持 good，仅顶层直接 `select("#", "alpha", "beta")` 保持 bad。因此进一步证伪“整数 2 返回值本身”“Lua vararg 调用”“GoResultsFunction 透传整数/多返回”是必要根因，下一轮应审查该 CALL 形态的结果寄存器写回和后续寄存器/开放栈顶清理。
        - [x] 2026-07-07 复核：新增 `lua.TestDoStringSelectCountFixedResultDoesNotLeakArguments` 作为模块无关 Go 回归测试；纯 Go VM 中顶层固定单返回 `select("#", "alpha", "beta")` 后继续调用 vararg 函数、table constructor 和公开 State 栈清理均通过。该结果证伪“普通固定单返回 CALL 在无 native/LPeg 前序状态下必然泄漏实参或开放栈顶”是必要根因，后续应聚焦 native/LPeg 前序状态与 VM 调用边界叠加后的隔离差异。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的 select 计数矩阵；`select("#", "alpha", "beta")` 调用语句直接丢弃返回值保持 good，单个非空参数的 `select("#", "alpha")` / `select("#", 17)` 也保持 good，但两个数字参数、双返回左值接收和 table constructor 消费计数结果均为 bad。当前边界进一步收敛为 native/LPeg 前序状态后“两个以上非空参数的 select 计数结果被后续消费”触发的通用寄存器/返回值/GC 根隔离问题，而不是参数值类型、返回值内容或 `select` Go closure 调用本身。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的消费形态矩阵；inline `if select("#", "alpha", "beta") ~= 2`、`do ... end` 块作用域、`count = nil`、后续局部覆盖为 nil/false 均保持 bad，说明 bad 不依赖活跃 local 生命周期，也不是简单地由 CALL 后实参槽非 nil 残留导致；`select("#", nil, nil)`、literal `2` 和 `#{"alpha","beta"}` 仍为 good，说明至少两个非 nil 实参参与 `select("#", ...)` 且结果被消费是当前更小边界。
        - [x] 2026-07-07 复核：继续用 `scripts/probe-native-lpeg-select-count.sh` 细化 nil/false 混合矩阵；`select("#", "alpha", nil)`、`select("#", nil, "beta")`、`select("#", false, false)`、`select("#", false, nil)`、`select("#", nil, false)` 均保持 good，而两个数字和三字符串仍为 bad。该结果修正上一条“两个以上非 nil”表述：触发条件目前更接近两个以上 string/number 这类可参与 VM 值/根处理的实参、固定单返回写回、结果被后续消费三者叠加；false 和 nil 混合不触发，后续不能按所有非 nil 值做泛化修复。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的两实参内置函数对照；`rawequal("alpha", "beta")`、`rawequal(17, 25)`、`tonumber("17", 10)`、`rawget({key="value"}, "key")`、`setmetatable({}, {})` 均保持 good。因此已证伪“两实参 Go closure 固定单返回被消费”是充分根因，当前边界继续收窄为 `select("#", ...)` 的 vararg 计数路径、固定返回写回和 native/LPeg 前序状态叠加；生产修复仍必须解释为通用 vararg/CALL/栈隔离语义，不能为 `select` 或 LPeg 写特例。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的 truthy/falsy 对照；`select("#", true, true)`、`select("#", true, false)`、`select("#", false, true)`、`select("#", "alpha", false)`、`select("#", false, "beta")` 均保持 good，而三字符串和两个数字仍保持 bad。因此已证伪“两个 truthy 参数”是根因，当前边界继续限定为 `select("#", ...)` 的两个以上 string/number 实参组合；后续需要继续查 string/number 值类别在 vararg 参数切片、固定写回和 native/LPeg 前序状态叠加下的通用隔离问题。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的手动寄存器清理对照；`select("#", "alpha", "beta")` 后追加 `local clear1, clear2, clear3 = nil, nil, nil` 或 `false, false, false` 仍保持 bad，而直接丢弃 `select` 返回值仍为 good。因此已证伪“仅有 R1-R3 参数槽残留 string/number，手动覆盖即可恢复”是单独根因，后续应转向固定 CALL/vararg 计数路径的内部状态、结果消费和 native/LPeg 前序状态叠加，而不是简单清空后续局部槽。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加指定模式运行入口，可通过命令行参数或 `PROBE_MODES` 只跑关键 good/bad 对照组；后续出现定位分歧时，每轮先评估采用二分、短矩阵、源码审查、定向 Go 测试或指定模式 probe 中哪一种，再执行最能缩小边界的方案，避免无差别全矩阵耗时。
        - [x] 2026-07-07 复核：新增 `scripts/probe-native-lpeg-select-bytecode.sh` 固定关键对照组反汇编；`select-count-consume` 为 `CALL A=0 B=4 C=2` 后 `MOVE/EQ/TEST` 消费固定单返回，`select-count-discard` 为 `CALL A=0 B=4 C=1` 丢弃返回值，`select-count-table-constructor` 为 `CALL A=1 B=4 C=0` + `SETLIST` 消费开放返回；`rawequal`/`tonumber` good 组同样存在固定单返回 `CALL C=2`，因此下一步应继续区分 `select` vararg 计数路径和 fixed/open 返回写回，而不能把所有 `CALL C=2` 泛化为根因。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的 GC 时机对照；`select` 前 `collectgarbage()`、`select` 调用后 `collectgarbage()`、结果消费后 `collectgarbage()` 均保持 bad，而直接丢弃返回值仍为 good。因此已证伪“只要改变显式 GC 时机即可恢复”是根因，下一步仍应聚焦 `select` vararg 计数结果被消费时的寄存器/返回写回/LPeg native 状态交互，而不是直接做 GC 时机特例。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的后续 CALL 对照；bad 的 `select("#", "alpha", "beta")` 计数结果被消费后，再执行 `rawequal("alpha","beta")`、`tonumber("17",10)` 或一次丢弃式 `select("#", "alpha", "beta")` 仍保持 bad；这些 good CALL 单独执行仍为 good。因此已证伪“下一次普通 CALL 会自动清理/恢复状态”是根因，问题更接近首次被消费的 `select` vararg 计数调用留下了会被后续 LPeg native 匹配读取的状态。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的赋值/消费边界；`local count = select("#", "alpha", "beta")` 后不读取、`local mirror = count`、写入 `_G.__glua_select_count_probe`、直接丢弃返回值、单字符串参数、以及嵌套 Lua 函数内部 `return select("#", ...)` 再由外层消费均保持 good；直接比较消费和 table constructor 消费仍保持 bad，两数字参数仍 bad，`rawequal` good 组仍 good。因此已证伪“固定单返回写回本身”“普通 MOVE”“全局写入”“GoResultsFunction 返回整数 2”和“任意两实参 Go closure”是充分根因，下一轮应优先审查 `EQ`/`TEST` 条件消费、`SETLIST` 开放结果消费与 LPeg native 前序状态之间的通用隔离语义。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的消费操作矩阵；`if not count then ... end` 仅做 truthy `TEST` 即退化为 `12`，而 `local equal = count == 2` 但不分支、`local packed = {count}` 固定 table store、`sink(count)` Lua 函数实参、`rawequal(count, 2)` Go 函数实参、`count + 0` 算术消费均保持 good；直接 `local count = select(...); if count ~= 2 then ... end` 和 `{select(...)}` 仍保持 bad。因此已证伪“读取 count 值本身”“算术消费”“函数实参传递”“固定 table 构造”和“EQ 比较计算”是充分根因，下一轮应优先审查条件分支 `TEST/JMP` 后继续执行、以及开放返回 `CALL C=0` 接 `SETLIST` 的状态更新是否影响后续 LPeg native match。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 与 `scripts/probe-native-lpeg-select-bytecode.sh` 的空分支对照；`if not count then end`、`if count then end`、`if count == 2 then end` 均保持 good，反汇编分别覆盖 `NOT/TEST/JMP`、直接 `TEST/JMP`、`EQ/LOADBOOL/TEST/JMP`，而含 `error(...)` 的 `if not count then ... end` 仍 bad，`{select(...)}` 的 `CALL C=0` + `SETLIST B=0` 仍 bad。因此已证伪“普通 `TEST/JMP` 或条件布尔化本身”是充分根因，下一轮应优先区分条件分支跳过/进入后续 call block 的 PC/寄存器状态，以及开放返回构造路径对 LPeg native 前序状态的通用影响。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 与 `scripts/probe-native-lpeg-select-bytecode.sh` 的非空 body / 普通 call 对照；`if not count then local skipped = 1 end`、`if not count then local skipped = error end`、`if count == 2 then local entered = 1 end`、`if not count then type("skipped") end`、`if count == 2 then type("entered") end` 均保持 good，而 `if not count then error(...) end` 仍 bad，`{select(...)}` 仍 bad。反汇编显示 skipped `error(...)` 与 skipped `type(...)` 同为 `GETTABUP/LOADK/CALL` 且 `JMP sBx=3` 跳过；因此已证伪“跳过非空 body”“跳过或进入普通 `CALL` 指令”“读取 `error` 全局函数值”是充分根因，下一轮应优先区分 `error` 调用字节码的常量/函数值保留、错误函数特殊语义和 LPeg 前序状态叠加，以及继续独立审查开放返回 `CALL C=0` + `SETLIST B=0`。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 与 `scripts/probe-native-lpeg-select-bytecode.sh` 的顺序局部变量对照；`local skipped = error` 单独 good，`local message = "unexpected falsy select count"` 单独 good，`local skipped = type; local message = ...` good，但 `local skipped = error; local message = ...` 在无分支、无 `CALL`、无错误执行的顺序路径下已退化为 `12`。因此已证伪 `JMP`、`TEST`、错误恢复、未执行 body、普通 `CALL` 和字符串常量本身是必要条件，当前最小坏形态收敛为 `select("#", 两个以上 string/number)` 固定单返回被消费后，`error` 函数值与消息字符串同时落入连续局部槽；下一轮应审查 VM 活跃寄存器、闭包/函数值、字符串值和 native LPeg 根扫描隔离，而不是继续围绕 `base.error` 执行语义。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 与 `scripts/probe-native-lpeg-select-bytecode.sh` 的作用域/清空/顺序对照；无 `select` 时 `error+message` 保持 good，`select` 后 `message+error` 反向局部槽顺序仍 bad，`do ... end` 关闭作用域仍 bad，`skipped/message = nil` 或 `false` 后仍 bad。因此已证伪单纯 `error+message`、局部槽顺序、活跃 local 生命周期和后续显式覆盖是必要根因；当前更像 `select("#", 两个以上 string/number)` 固定单返回被消费后，后续写入 `error`/message 触发的历史寄存器、openTop 或 native 可见栈隔离缺口。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的捕获 State/重入 Lua 内置函数值对照；`type+message` 保持 good，`error+message`、`pcall+message`、`xpcall+message`、`tostring+message`、`dofile+message` 均 bad，`print+message` good。因此已证伪 `base.error` 执行语义、函数名和错误恢复是必要根因；下一步应优先审查 Go closure identity、捕获 State 的 closure 值作为活跃 local 时的 GC/弱表强根建模，以及 native LPeg match-time callback 期间可见栈/寄存器隔离是否仍有通用语义缺口。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-count.sh` 的 `collectgarbage("stop")` 对照；`select` 前、`select` 后、结果消费后停止自动 GC 均仍为 bad，`error+message` 组合在 `gc stop` 前置或后置时也仍为 bad。因此已证伪 `NoteTableAllocation` 驱动的自动 GC、自动 weak sweep 或自动 finalizer 时机是单独根因，后续应继续聚焦固定单返回/寄存器历史状态/闭包值作为 native 可见根的隔离问题，而不是通过调节 GC 时机修复。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加 `select-count-global-message-*` 通用函数值分类模式；短矩阵显示 `assert/getmetatable/ipairs/load/pairs/rawget/select/tonumber/type/print + message` 保持 good，`error/collectgarbage/loadfile/next/rawequal/rawlen/rawset/setmetatable/tostring + message` 为 bad。因此已证伪“捕获 State 的 Go closure 必然触发”和“非捕获 named Go function 必然安全”两种简单分类；当前更像特定函数值身份、历史寄存器布局、message 字符串和 native LPeg 后续根扫描/可见栈之间的组合问题，生产修复不能按函数名特例化。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `select-count-global-only-*`、`select-count-message-global-*` 和 `select-count-global-spacer-message-*` 布局对照；`error/next/rawequal` 函数值单独均为 good，但与 message 组合时无论函数在前、message 在前，或中间插入 `spacer = 0` 都为 bad；`type/assert` 在相同布局下全部 good。因此已证伪“函数值单独污染”“局部槽相邻顺序错位”是充分根因，当前更像特定函数值身份与字符串根同时处于活动寄存器/可见根集合时触发 native LPeg 后续匹配状态差异。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `select-count-global-value-<global>-<kind>` 载荷类型对照；`error/next/rawequal` 搭配 `string` 或 `number` 仍为 bad，但 `boolean`、`nil`、`table`、`function` 保持 good；`type/assert` 搭配 `string` 仍为 good。因此已证伪“任意第二个值”“任意可回收对象”“任意字符串值”是充分根因，当前边界进一步收窄为特定函数值身份与 string/number 标量载荷在 `select("#", 两个以上 string/number 变参)` 固定单返回被消费后的历史寄存器/可见根集合叠加。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `select-count-value-global-<kind>-<global>` 和 `select-count-global-spacer-value-<global>-<kind>` 布局对照；`error+number` 与 `error+string` 在函数值前后互换、或中间插入 `spacer = 0` 后仍为 bad，`error+boolean` 在同样布局下保持 good，`type+number` 在同样布局下保持 good。因此已证伪 string/number 载荷的局部槽顺序、相邻性和新增布局模式自身是充分根因，下一步应继续审查这些值落入 VM 活动寄存器/可见根集合后的通用隔离语义。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `select-count-holder-table-*`、`select-count-holder-table-clear-*` 和 `select-count-global-holder-table-*` 间接可达对照；`error+number/string` 放入 local holder table、清空 holder，或写入全局 holder table 后仍为 bad，`error+boolean` 与 `type+number` 在 holder table 中保持 good。因此已证伪“必须直接留在局部槽且保持活跃”是充分根因，当前更像特定函数值与 string/number 组合在 table 构造/可达图或历史寄存器写入后影响 LPeg 后续 native match 的通用状态隔离问题。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_SHOW_ATTEMPTS=1` 可选输出；baseline、`type+number`、`error+boolean` 的 match-time attempts 为 `7,12,13,14,15,18` 完整回溯，`error+number` 与 holder table `error+number` 只剩 position `12` 两次尝试并返回 `12`。因此已证伪“回调同样执行但真假判断不同”是根因，当前更像前序状态改变了 LPeg VM/backtrack/capture 可见状态，使后续新建 pattern 的回溯路径提前收敛。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_PREBUILD_PATTERN=1` 预构造最终 probe pattern 开关；在 `error+number`、holder table `error+number`、`type+number` 和 `error+boolean` 矩阵中，若先构造 probe pattern 再执行扰动代码，后续 match 均保持 `18` 且 attempts 完整。因此已证伪“扰动直接破坏已构造 pattern 的 match-time 运行态”是充分根因，当前边界前移到扰动后新建 LPeg pattern 的构造、ktable/capture 建立或编译阶段。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_PREBUILD_PARTS=1` 子 pattern 预构造开关；先构造 `probe_open`、`probe_close` 和 `probe_any`，扰动后再组装 grammar 并 match，`error+number`、holder table `error+number`、`type+number` 和 `error+boolean` 均保持 `18` 且 attempts 完整。因此已证伪“已构造子 pattern 在 grammar 组装或 match 阶段被扰动”是充分根因，当前边界进一步前移到扰动后构造 LPeg 基础子 pattern、写入 ktable/user value 或 capture 描述的阶段。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_PREBUILD_OPEN=1`、`PROBE_PREBUILD_CLOSE=1` 和 `PROBE_PREBUILD_ANY=1` 单子 pattern 预构造开关；只预构造 `probe_open` 时 `error+number` 仍为 `12`，只预构造 `probe_any` 时仍为 `12`，但只预构造 `probe_close = Cmt(... Cb("init") ...)` 时 `error+number`、`error+string`、holder table `error+number`、`type+number` 和 `error+boolean` 均恢复为 `18` 且 attempts 完整。因此当前边界进一步收敛为扰动后构造 match-time capture / back capture 相关子 pattern 时，LPeg 后续回溯路径被提前收敛；生产修复仍必须解释为通用 native C API/VM 调用帧/可见根隔离语义，不能围绕 LPeg 的 `Cmt` 或 `Cb` 做特向性修复。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_PREBUILD_CLOSE_HEAD=1`、`PROBE_PREBUILD_CLOSE_BACK=1` 和 `PROBE_PREBUILD_CLOSE_FUNC=1`，把 `probe_close` 拆成 `']' * C(P'='^0) * ']'`、`Cb("init")` 与 match-time 回调函数三部分；在 `error+number` 坏例中，三者任一单独预构造均恢复为 `18` 且 attempts 完整。因此当前证据不能归因到某一个 LPeg API 或某一个 callback 执行语义，更像扰动后完整重新构造 close 相关对象图时，pattern 编译/ktable/capture 描述或 native 可见根集合发生了通用隔离差异；下一轮需要加入无关 Lua closure、无关 capture、无关 back capture 等 dummy 控制组，避免把“预构造改变对象布局/分配时机”误判成 close 语义本身。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_PREBUILD_DUMMY_FUNC=1`、`PROBE_PREBUILD_DUMMY_CAPTURE=1` 和 `PROBE_PREBUILD_DUMMY_BACK=1`；这些 dummy 对象在扰动前构造但完全不参与最终 probe pattern。`error+number` 坏例在三组下均恢复为 `18` 且 attempts 完整。因此已证伪“只要预构造 close 相关子 pattern 才能恢复”以及“close 语义本身是充分根因”；当前更像特定对象种类/分配时机/可见根集合布局改变了扰动后的 LPeg pattern 构造结果。由于 `probe_open` 和 `probe_any` 预构造仍不恢复，后续不能简单归因到任意预分配对象，需继续区分 Lua closure、capture/back capture、table/string/number 等对象种类和 VM/GC 根建模的通用语义。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_PREBUILD_DUMMY_VALUE=<string|number|table|boolean|nil>` 与 `PROBE_PREBUILD_DECLS_ONLY=1`；`string`、`number`、`table`、`boolean`、`nil` 以及只声明前置局部、不构造任何对象的 `DECLS_ONLY` 均能把 `error+number` 坏例恢复为 `18`，而默认无预构造仍保持 `12`。因此上一条“对象种类/分配时机”继续被修正：最小证据已收敛为前置局部槽/寄存器布局变化本身即可改变后续 LPeg pattern 构造结果，下一步应直接审查 VM 活跃寄存器、openTop、调用帧返回写回和 native 可见栈/GC 根边界，而不是继续按 LPeg API 或对象类型做特向性推断。
        - [x] 2026-07-07 修复：使用 `gopls check runtime/vm.go runtime/state.go stdlib/base/base.go internal/native/*.go` 和 `gopls check runtime/gc.go runtime/gc_test.go runtime/state.go runtime/vm.go` 复核后，确认 `SnapshotGCRoots` 未把 `state.activeVMs` 的活动寄存器纳入 `GCRootTypeStack`，而弱表 sweep 路径已经多处扫描 active VM。已补齐 active VM 活动寄存器 root 与其中 table 的 key/value 采样，并新增模块无关测试 `TestGCRootsActiveVMRegisterRoot`。该切口面向通用 VM/GC 根语义，不针对 LPeg、测试行号或特定 pattern。
        - [x] 2026-07-07 证伪：在上述 active VM root 修复后复跑 `scripts/probe-native-lpeg-select-count.sh`，`select-count-global-value-error-number` 默认仍为 `12` bad，`select-count-global-value-error-boolean` 与 `select-count-global-value-type-number` 仍为 `18` good，`PROBE_PREBUILD_DECLS_ONLY=1` 的 `error+number` 恢复组仍为 `18` good。因此 active VM root 缺口需要修复，但不是 LPeg 1159/select-count 退化的直接充分根因。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加 `fixed-result-global-value-<source>-<global>-<kind>` 矩阵，复用同一 `error+number` 扰动但替换前置固定单返回来源。`select-count` 与 `builtin-rawequal` 仍为 `12` bad；`literal-two`、`literal-false`、`literal-nil`、`lua-return-select-count`、`lua-return-two`、`lua-return-vararg-two`、`builtin-assert-two`、`builtin-tonumber`、`builtin-rawget` 均为 `18` good。因此已证伪“任意固定单返回 CALL”“任意 vararg 返回”“静态局部槽保存 false/nil/number”是充分根因，当前窗口收窄为部分 Go 内置闭包固定返回与后续特定函数值+标量载荷组合后的寄存器/调用帧历史状态。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `fixed-result-clear3-global-value-<source>-<global>-<kind>`，在固定返回后插入 `local clear1, clear2, clear3 = nil, nil, nil` 再写入 `error+number`。`select-count` 与 `builtin-rawequal` 的 clear3 组仍为 `12` bad。因此已证伪“只要后续新增局部覆盖几个寄存器槽即可恢复”是根因，问题不只是简单未清空相邻实参槽；下一步需要直接比较这些 Go 内置 closure 的返回搬运、函数/实参槽生命周期和 active VM/native 可见状态。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加固定返回来源的实参类型和 true/false 结果对照。`select("#", "alpha")`、`select("#", "alpha", "beta")`、`select("#", 17, 25)` 均为 `12` bad，但 `select("#", true, false)` 为 `18` good；`rawequal("alpha", "beta")` 为 `12` bad，但 `rawequal(17, 25)`、`rawequal(true, false)`、`rawequal("alpha", "alpha")` 和 `rawequal(17, 17)` 均为 `18` good，`assert(2)`、`tonumber("17", 10)`、`rawget({key = 2}, "key")` 继续保持 good。因此已证伪“字符串实参独有”“数字实参独有”“rawequal 函数整体”“true/false boolean 返回整体”是充分根因；当前窗口进一步收敛为特定内置调用写回路径、结果真假和后续 `error+number` 可见状态组合，而不是某个模块、函数名或单一实参类型。
        - [x] 2026-07-07 复核：继续扩展 `scripts/probe-native-lpeg-select-bytecode.sh`，把上一轮固定返回 good/bad 最小对落到反汇编层。`select("#", "alpha")`、`select("#", 17, 25)`、`select("#", true, false)` 均使用同一固定单返回 `CALL C=2`、随后 `MOVE/EQ/LOADBOOL/TEST/JMP`、再写入 `skipped` 与 `payload` 的骨架，差异只在 `LOADK` string/integer 与 `LOADBOOL` boolean 实参装载；`rawequal("alpha","beta")`、`rawequal("alpha","alpha")`、`rawequal(17,25)` 也共享同一 `CALL B=3 C=2` 与后续局部槽骨架。因此已证伪“坏例由额外控制流、额外 CALL、不同 maxstack 或不同 local 生命周期引入”是根因；下一步应进入 VM/base CALL 写回运行期 trace，重点看 CALL 后非结果寄存器、比较临时槽和 Go closure 结果搬运是否留下 native 后续构造可见状态。
        - [x] 2026-07-07 复核：新增 `stdlib/base/base_call_trace_test.go`，该文件只在显式 `-tags call_trace` 下启用，用于同包运行期 trace `executeBaseLuaCallRequest` 与 `writeBaseLuaCallResults`。`go test -tags call_trace ./stdlib/base -run TestBaseLuaCallTraceFixedResultPairs -v` 显示固定单返回 `CALL C=2` 只覆盖 R(A) 返回区，非结果实参槽保持原值：`select("#","alpha")` 后寄存器为 `[integer(1), string("#"), string("alpha"), nil]`，`select("#",17,25)` 后为 `[integer(2), string("#"), integer(17), integer(25)]`，`select("#",true,false)` 后为 `[integer(2), string("#"), boolean(true), boolean(false)]`；`rawequal("alpha","beta")` 后为 `[boolean(false), string("alpha"), string("beta"), nil]`，`rawequal("alpha","alpha")` 后为 `[boolean(true), string("alpha"), string("alpha"), nil]`，`rawequal(17,25)` 后为 `[boolean(false), integer(17), integer(25), nil]`。因此已确认运行期 CALL 写回确实保留非结果实参槽，且 bad/good 差异落在这些保留值类型/identity 与后续局部写入叠加，而不是结果写回数量或 CALL 控制流。
        - [x] 2026-07-07 复核：继续扩展 `stdlib/base/base_call_trace_test.go` 的 `TestBaseLuaCallTraceCalleeRegisterLifetimes`，trace 输出新增活动 local 名称与寄存器号。`go test -tags call_trace ./stdlib/base -run 'TestBaseLuaCallTrace(FixedResultPairs|CalleeRegisterLifetimes)' -v` 显示 local Go callee、local Lua callee、字段调用和方法调用都会把函数值搬到临时 CALL A 槽，活跃 local 保留在更低寄存器：例如 `local f = select; local count = f(...)` 后 `locals=[f@0=ref(kind=7),count@1=integer(1)]` 且临时实参槽仍为 `string("#"), string("alpha")`；`local function f() ...; local count = f()` 后 `locals=[f@0=ref(kind=6),count@1=integer(1)]`；方法调用 `t:f()` 后 `locals=[t@0=ref(kind=5),count@1=integer(1)]` 且 self 临时槽仍残留 table。因此已初步证实当前 codegen 常见调用来源不会让固定返回 CALL A 直接覆盖仍存活的 callee local，但方法 self/参数临时槽仍会残留；后续生产修复若选择清理非结果实参槽，必须继续保护 debug local 生命周期、`CALL C=0` 开放返回、`TFORCALL`、`__call` 改写和 hook/traceback 可见性。
        - [x] 2026-07-07 门禁：新增默认构建测试 `lua.TestDoStringCallTemporaryCleanupGuardSemantics`。该测试不依赖 LPeg 或 native 模块，通过 Lua line hook + `debug.getlocal` 锁定 local Go callee、method receiver 和 `__call` receiver 在固定返回 CALL 后仍可见，同时直接验证 `{many()}` 的开放返回 `CALL C=0` + `SETLIST` 和 `ipairs` 泛型 for 的 `TFORCALL` 语义。`go test ./lua -run TestDoStringCallTemporaryCleanupGuardSemantics -v` 已通过；后续任何 CALL 临时区清理生产修复都必须先保持该模块无关门禁。
        - [x] 2026-07-07 门禁：新增默认构建测试 `lua.TestDoStringCallTemporaryCleanupTraceHookGuards`。该测试不依赖 LPeg 或 native 模块，通过固定返回 CALL 后的 count hook、return hook 与 `xpcall(..., debug.traceback)` 锁定当前 Lua 帧仍能观察 local callee、固定返回值和错误现场；`go test ./lua -run 'TestDoStringCallTemporaryCleanup(GuardSemantics|TraceHookGuards)' -v` 已通过。该门禁约束未来生产修复不得破坏 debug hook、return hook 与 traceback 对活动帧的 Lua 5.3 可见性。
        - [x] 2026-07-07 门禁：新增默认构建测试 `lua.TestDoStringCallTemporaryCleanupCallMetamethodArgumentGuards`。该测试不依赖 LPeg 或 native 模块，覆盖 fixed-result CALL 调用带 `__call` 元方法的值时，元方法帧仍能通过正索引 `debug.getlocal` 看到 `self/first/second`，通过负索引 `debug.getlocal` 看到 vararg，并验证固定单返回写回后原 callable local 仍可见；`go test ./lua -run 'TestDoStringCallTemporaryCleanup(CallMetamethodArgumentGuards|GuardSemantics|TraceHookGuards)' -v` 已通过。该门禁约束未来生产修复不得提前清理 `__call` 改写后的参数区间。
        - [x] 2026-07-07 门禁：新增默认构建测试 `lua.TestDoStringCallTemporaryCleanupTForCallResultGuards`。该测试不依赖 LPeg 或 native 模块，覆盖自定义泛型 for 迭代器的 `TFORCALL` 结果写入循环变量区、循环体内普通 fixed-result CALL 后循环变量仍通过 `debug.getlocal` 可见，并验证 `TFORLOOP` 控制变量能推进两轮；`go test ./lua -run 'TestDoStringCallTemporaryCleanup(CallMetamethodArgumentGuards|GuardSemantics|TraceHookGuards|TForCallResultGuards)' -v` 已通过。该门禁约束未来生产修复不得把普通 CALL 临时槽清理逻辑误应用到 `TFORCALL` 结果区或活跃迭代变量。
        - [x] 2026-07-07 证伪：尝试实现最小生产修复 `VM.ClearFixedCallArgumentTemporaries`，仅清理 fixed-result 普通 CALL 中未被返回值覆盖的实参槽，并排除 `CALL C=0`、`TAILCALL`、`TFORCALL`、函数槽和返回值覆盖槽；模块无关定向测试通过，但 native LPeg probe `select-count-global-value-error-number` 从基线 worktree 的 `result=12 class=bad` 变为当前修改下进程崩溃 `class=invalid`，而 good 对照 `select-count-global-value-type-number` 在当前修改下仍为 `result=18 class=good`。因此直接清理普通 CALL 实参槽能命中问题区域，但当前 native C API/LPeg 生命周期、ktable/capture 根集合或 userdata/user value 持有语义还不能安全承受该清理，生产代码已撤回，不能提交该修复。
        - [x] 2026-07-07 工具门禁：增强 `scripts/probe-native-lpeg-select-count.sh`，新增 `PROBE_CONTINUE_ON_CRASH=1`。该开关只影响诊断模式，默认失败退出行为保持不变；启用后 glua/native 子进程非零退出会输出 `class=crash exit=<status>` 并继续后续 mode，便于对 “清理后崩溃” 场景做二分、矩阵对照和最小复现定位，而不是被第一个崩溃样本打断。
        - [x] 2026-07-07 修复：使用 `gopls check runtime/gc.go runtime/gc_test.go runtime/userdata.go runtime/state.go` 复核后，补齐 GC root/weak sweep 对 userdata 关联强边的建模：`UserValue` 与 raw metatable 进入 `userdata-association-root`，并在强引用递归、weak table sweep、finalizer 前 weak value sweep 中继续扫描。新增模块无关测试 `TestGCRootsUserdataAssociationRoot` 和 `TestSweepWeakTablesKeepsUserdataAssociationValues`，锁定 native full userdata 的 ktable/capture 类关联值不会被 weak sweep 误清理。该修复是通用 Lua 5.3 full userdata 语义，不依赖 LPeg 私有行为。
        - [x] 2026-07-07 证伪：上述 userdata 关联强边修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 userdata user value/metatable 强边缺口需要修复，但不是当前 select-count 退化的直接充分根因；下一步继续审查 CALL 实参槽清理后崩溃暴露的 native 可见栈/寄存器隔离。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_call_native.go internal/native/capi_call_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，补齐 `lua_callk`/`lua_pcallk` 的当前 C 帧可见栈边界检查。此前 malformed `argumentCount` 可让 function slot 穿透 `baseTop`，误把外层 Go VM 栈值当作函数并弹掉；新增 `TestNativeLuaCallKRespectsCurrentCFrameBase` 锁定嵌套 C function 调用只消费当前 C 帧可见栈，并在可见槽不足时保留外层 sentinel。该修复是通用 Lua C API 栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 C 帧栈隔离修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 C API 嵌套 call 栈穿透需要修复，但不是当前 select-count 退化的直接充分根因。
        - [x] 2026-07-07 门禁：继续使用 `gopls check internal/native/capi_call_native.go internal/native/capi_call_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，新增 `TestNativeLuaPCallKRespectsCurrentCFrameBase`，把 protected `lua_pcallk` 的当前 C 帧隔离与 malformed 参数数量失败安全路径纳入测试。该门禁未改生产代码，用于防止后续栈隔离修复只覆盖非 protected call 而遗漏 protected call。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_table_native.go internal/native/capi_table_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，新增 `nativeLuaPopVisible` 并将 `lua_setfield` 的弹栈改为只消费当前 C 帧可见槽位。新增 `TestNativeLuaSetFieldRespectsCurrentCFrameBase`，覆盖 `lua_setfield(L, LUA_REGISTRYINDEX, key)` 在当前 C 帧没有可见 value 时不得弹掉外层 Go VM sentinel，也不得向 registry 写入错误值。该修复是通用 Lua 5.3 C API 栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `lua_setfield` 可见栈弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `lua_setfield` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续仍需继续审查其他直接 `state.Pop()` 的 C API 和 fixed-result CALL 临时槽生命周期。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_table_native.go internal/native/capi_table_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `lua_rawseti` 的弹栈改为复用 `nativeLuaPopVisible`，只消费当前 C 帧可见栈顶。新增 `TestNativeLuaRawSetIRespectsCurrentCFrameBase`，覆盖 `lua_rawseti(L, LUA_REGISTRYINDEX, ref)` 在当前 C 帧没有可见 value 时不得弹掉外层 Go VM sentinel，也不得向 registry 写入错误值。该修复是通用 Lua 5.3 raw integer table/registry 写入栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `lua_rawseti` 可见栈弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `lua_rawseti` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续按通用 C API 栈隔离和 fixed-result CALL 生命周期排查。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_table_native.go internal/native/capi_table_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `lua_gettable` 的 key 弹栈改为复用 `nativeLuaPopVisible`，只消费当前 C 帧可见栈顶。新增 `TestNativeLuaGetTableRespectsCurrentCFrameBase`，覆盖 `lua_gettable(L, LUA_REGISTRYINDEX)` 在当前 C 帧没有可见 key 时不得弹掉外层 Go VM sentinel，也不得压入伪造的 nil 查询结果。该修复是通用 Lua 5.3 table/registry 读取栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `lua_gettable` 可见栈弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `lua_gettable` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续按通用 C API 栈隔离和 fixed-result CALL 生命周期排查。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_table_native.go internal/native/capi_table_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `lua_settable` 的 key/value 数量检查和弹栈改为当前 C 帧可见栈语义。新增 `TestNativeLuaSetTableRespectsCurrentCFrameBase`，覆盖 `lua_settable(L, LUA_REGISTRYINDEX)` 在当前 C 帧没有可见 key/value 时不得弹掉外层 Go VM sentinel，也不得向 registry 写入错误值。该修复是通用 Lua 5.3 table/registry 写入栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `lua_settable` 可见栈弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `lua_settable` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续按通用 C API 栈隔离和 fixed-result CALL 生命周期排查。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_table_native.go internal/native/capi_table_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `lua_rawset` 的 key/value 数量检查和弹栈改为当前 C 帧可见栈语义。新增 `TestNativeLuaRawSetRespectsCurrentCFrameBase`，覆盖 `lua_rawset(L, LUA_REGISTRYINDEX)` 在当前 C 帧没有可见 key/value 时不得弹掉外层 Go VM sentinel，也不得向 registry 写入错误值。该修复是通用 Lua 5.3 raw table/registry 写入栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `lua_rawset` 可见栈弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `lua_rawset` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续审查 `lua_next`、`luaL_ref`、metatable/userdata 弹栈和 fixed-result CALL 生命周期。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_table_native.go internal/native/capi_table_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `lua_next` 的当前 key 弹栈改为当前 C 帧可见栈语义。新增 `TestNativeLuaNextRespectsCurrentCFrameBase`，覆盖 `lua_next(L, LUA_REGISTRYINDEX)` 在当前 C 帧没有可见 key 时不得弹掉外层 Go VM sentinel，也不得留下 pending error。该修复是通用 Lua 5.3 raw table/registry 迭代栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `lua_next` 可见栈弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `lua_next` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续审查 `luaL_ref`、metatable/userdata 弹栈和 fixed-result CALL 生命周期。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_table_native.go internal/native/capi_table_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `luaL_ref` 的待引用值弹栈改为当前 C 帧可见栈语义。新增 `TestNativeLuaLRefRespectsCurrentCFrameBase`，覆盖 `luaL_ref(L, LUA_REGISTRYINDEX)` 在当前 C 帧没有可见 value 时不得弹掉外层 Go VM sentinel，也不得分配 registry 引用槽。该修复是通用 Lua 5.3 registry ref 栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `luaL_ref` 可见栈弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `luaL_ref` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续审查 metatable/userdata 弹栈和 fixed-result CALL 生命周期。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_userdata_native.go internal/native/capi_userdata_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `lua_setuservalue` 的待写 user value 弹栈改为当前 C 帧可见栈语义。新增 `TestNativeLuaSetUserValueRespectsCurrentCFrameBase`，覆盖目标 userdata 来自 C closure upvalue、当前 C 帧没有可见 user value 时不得弹掉外层 Go VM sentinel，也不得把 sentinel 写入 userdata。该修复是通用 Lua 5.3 full userdata user value 栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `lua_setuservalue` 可见栈弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `lua_setuservalue` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续审查 `lua_setmetatable` 和 fixed-result CALL 生命周期。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_metatable_native.go internal/native/capi_metatable_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `lua_setmetatable` 成功路径弹出新元表改为当前 C 帧可见栈语义。新增 `TestNativeLuaSetMetatableUsesCurrentCFrameTop`，覆盖外层 Go VM sentinel 仍在栈上、当前 C 帧只可见目标 table 和元表时，成功设置元表后只弹出可见元表，不得穿透调用者栈。该修复是通用 Lua 5.3 raw metatable 写入栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `lua_setmetatable` 可见栈弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `lua_setmetatable` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续按 fixed-result CALL 生命周期和 native 对象可达性定位。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_buffer_native.go internal/native/capi_buffer_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `luaL_addvalue` 的栈顶读取和弹栈改为当前 C 帧可见栈语义，并把核心逻辑抽成 `nativeLuaBufferAddVisibleValue` 便于纯 Go 定向测试。新增 `TestNativeLuaBufferAddVisibleValueConsumesVisibleTop` 和 `TestNativeLuaBufferAddVisibleValueRespectsCurrentCFrameBase`，覆盖可见字符串正常追加并弹出、当前 C 帧无可见值时不得追加或弹掉外层 sentinel。该修复是通用 Lua 5.3 lauxlib buffer 栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `luaL_addvalue` 可见栈读取/弹栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `luaL_addvalue` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续按 fixed-result CALL 生命周期、native 对象可达性和剩余 C API 边界定位。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_cfunction_native.go internal/native/capi_cfunction_native_test.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `lua_pushcclosure` 的 upvalue 数量检查、捕获起点和弹栈恢复改为当前 C 帧可见栈语义。新增 `TestNativeLuaPushCClosureCapturesVisibleFrameUpvalues` 和 `TestNativeLuaPushCClosureRejectsUpvaluesOutsideCurrentCFrame`，覆盖 visible upvalue 正常捕获、当前 C 帧没有可见 upvalue 时不得捕获或弹掉外层 sentinel。该修复是通用 Lua 5.3 C closure/upvalue 栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `lua_pushcclosure` upvalue 可见栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 C closure upvalue 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续按 fixed-result CALL 生命周期、native 对象可达性和剩余 C API 边界定位。
        - [x] 2026-07-07 修复：使用 `gopls check internal/native/capi_newlib_native.go internal/native/capi_newlib_native_test.go internal/native/capi_cfunction_native.go internal/native/capi_stack_native.go internal/native/state_handle_native.go` 复核后，将 `luaL_setfuncs` 的 upvalue 数量检查、捕获起点、临时 closure 弹栈和结束恢复改为当前 C 帧可见栈语义。新增 `TestNativeLuaLSetFuncsUsesCurrentCFrameVisibleStack` 和 `TestNativeLuaLSetFuncsRejectsUpvaluesOutsideCurrentCFrame`，覆盖 visible table/upvalue 正常注册、当前 C 帧没有可见 upvalue 时不得把外层 sentinel 当作 upvalue 或弹掉。该修复是通用 Lua 5.3 `luaL_setfuncs`/`luaL_newlib` 栈隔离语义，不针对 LPeg。
        - [x] 2026-07-07 证伪：上述 `luaL_setfuncs` 可见栈修复后复跑 `PROBE_CONTINUE_ON_CRASH=1 PROBE_MODES="select-count-global-value-error-number select-count-global-value-type-number" ./scripts/probe-native-lpeg-select-count.sh`，`error-number` 仍为 `result=12 class=bad`，`type-number` 仍为 `result=18 class=good`。因此 `luaL_setfuncs` 穿透外层栈需要修复，但不是当前 select-count 退化的直接充分根因；后续继续按 fixed-result CALL 生命周期、native 对象可达性和剩余 C API 边界定位。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_PREBUILD_PADDING_LOCALS=<n>`，用可变数量前置 nil local 二分 `PROBE_PREBUILD_DECLS_ONLY=1` 的恢复原因。`padding=0/1/2/3` 时 `select-count-global-value-error-number` 均仍为 `result=12 class=bad`，`type-number` 均为 `result=18 class=good`；同一脚本下 `PROBE_PREBUILD_DECLS_ONLY=1` 仍把 `error-number` 恢复为 `result=18 class=good`。因此已证伪“任意增加少量前置 local 槽即可恢复”是根因，当前应继续区分 selected prebuild 代码路径本身、`attempts/probe_*` 局部声明形态、最终 pattern tail 生成方式与 native LPeg pattern 构造状态之间的差异，而不是简单按局部槽数量修复。
        - [x] 2026-07-07 复核：继续拆分 `PROBE_PREBUILD_DECLS_ONLY=1` 的恢复现象，新增 `PROBE_SELECTED_DECLS_DEFAULT_TAIL=1` 与 `PROBE_SELECTED_TAIL_ONLY=1`。默认矩阵中 `error-number` 仍为 `result=12 class=bad`、`type-number` 为 `result=18 class=good`；只保留 selected 前置声明但使用默认 tail 时仍保持同样结果；只使用 selected tail 且不保留前置声明时 `error-number` 与 `type-number` 都退化为 `result=12 class=bad`；完整 `PROBE_PREBUILD_DECLS_ONLY=1` 组合才把两组都恢复为 `result=18 class=good`。因此上一轮“前置局部槽/寄存器布局本身即可恢复”的解释需要收窄：恢复不是声明、少量 padding 或 tail 构造单独造成，而是前置声明形态与 selected tail 重新构造路径的组合效应；后续应优先比较这两种 Lua 代码生成出来的字节码、寄存器活跃区和 LPeg 构造时 native 可见根，而不是按某个单一开关生产修复。
        - [x] 2026-07-07 复核：扩展 `scripts/probe-native-lpeg-select-bytecode.sh`，把上一条四组落到反汇编。默认 tail 坏例为 `maxstack=10`，活跃 local 仅 `count/skipped/payload/attempts`，match-time callback 捕获 local `attempts@3`；`selected-decls-default-tail` 坏例为 `maxstack=21`，前置 `probe_*` local 长生命周期保留，但默认 tail 又创建新的 `attempts@14` 并被 callback 捕获；`selected-tail-only` 坏例为 `maxstack=9`，`attempts/probe_*` 全部经 `_ENV` 全局 `GETTABUP/SETTABUP` 读写，callback 捕获 `_ENV`；完整 `decls-only-selected-tail` 恢复组为 `maxstack=20`，`attempts/probe_*` 均为长生命周期 local，tail 赋值复用 `attempts@0`，callback 捕获 local `attempts@0`。因此恢复现象进一步收敛到“selected tail 是否复用前置 local 根/寄存器，而不是走全局或 shadow 新 local”的差异；下一步应基于这个形态构造模块无关 VM/GC/root 或 closure upvalue 可见性 probe，而不是按 LPeg API 名称修复。
        - [x] 2026-07-07 证伪：使用 `gopls check lua/api_test.go` 复核后，新增默认构建模块无关测试 `lua.TestDoStringCallTemporaryCleanupClosureRootShapeGuards`，覆盖 fixed-result `select` 后的 long-lived local upvalue、`_ENV`/global 访问和 shadow local 三种 root 形态，并在分配压力与 `collectgarbage()` 后继续调用闭包读取 captured table、函数值、数字载荷和字符串拼接结果。`CGO_ENABLED=0 go test ./lua -run TestDoStringCallTemporaryCleanupClosureRootShapeGuards -v` 已通过；同轮 LPeg probe 仍显示 `error-number result=12 class=bad`、`type-number result=18 class=good`。因此纯 Lua closure/upvalue/root 可见性不是当前退化的单独充分根因，后续应继续聚焦 native LPeg pattern 构造过程中 C API 持有 Lua 值、ktable/capture/userdata 与 fixed-result CALL 历史寄存器之间的交互。
        - [x] 2026-07-07 复核：继续拆分完整 `PROBE_PREBUILD_DECLS_ONLY=1` 的局部根来源，为 `scripts/probe-native-lpeg-select-count.sh` 新增 `PROBE_SELECTED_ATTEMPTS_DECL_ONLY=1`、`PROBE_SELECTED_PROBES_DECL_ONLY=1`、`PROBE_SELECTED_CORE_DECLS_ONLY=1`。`attempts-only` 组中 `error-number` 与 `type-number` 均为 `result=12 class=bad`；`probes-only` 组中两者均恢复为 `result=18 class=good`；`core` 组和完整 `PROBE_PREBUILD_DECLS_ONLY=1` 也均为 `result=18 class=good`。因此恢复关键不是 attempts 表或 dummy locals，而是 `probe_*` 这组 native LPeg pattern Lua 值是否在 selected tail 执行前作为长生命周期 local 保活；下一步应把生产修复收敛到通用 Lua C API/native userdata/capture/ktable 持值与 fixed-result CALL 临时槽清理之间的生命周期边界，避免任何 LPeg 名称或测试行号特向性处理。
        - [x] 2026-07-07 门禁：使用 `gopls check internal/native/capi_stack_native.go internal/native/capi_stack_native_test.go` 复核后，新增 `TestNativeLuaReplaceMacroRespectsCurrentCFrame`，模拟 Lua 5.3 public header 中 `lua_replace(L, idx)` 的 `lua_copy(L, -1, idx)` + `lua_pop(L, 1)` 宏展开，验证当前 C frame 内只替换可见参数并弹出临时栈顶，不覆盖外层 Go VM sentinel。该门禁覆盖 LPeg `getpatt` 依赖的通用 C API 栈组合语义，但不把当前 LPeg select-count 退化归因到 `lua_replace`。
        - [x] 2026-07-07 复核：继续用 `PROBE_SELECTED_PROBE_LOCALS=<list>` 二分 `probe_*` 局部根子集。`open`、`close`、`any`、`back`、`func` 单独均为 `result=12 class=bad`；`open,close,any`、`open,any`、`open,close`、`close,any` 等不含 `head` 的组合也均为 bad；仅 `head` 单独即可让 `error-number` 与 `type-number` 均恢复到 `result=18 class=good`，`head,back,func`、`close,head,back,func` 和完整 `open,close,any,head,back,func` 同样 good。因此上一条需进一步收窄：恢复关键不是所有 `probe_*` 或顶层拼装 pattern 本地化，而是 close-head 子表达式 `']' * C(P'='^0) * ']'` 是否在 `Cmt` 构造前经 local 保活；下一步应围绕通用 capture pattern/native 对 Lua 子对象引用保活、ktable/capture 引用边界和 CALL 临时槽清理交互继续定位。
        - [x] 2026-07-07 复核：继续拆分 close-head 子表达式，为 `scripts/probe-native-lpeg-select-count.sh` 新增 `PROBE_SELECTED_HEAD_LOCALS=<left|unit|capture|right,...>` 与 `PROBE_SELECTED_HEAD_SPLIT_TAIL_ONLY=1`。`left`、`unit`、`capture`、`right` 单独和多数组合均恢复到 `result=18 class=good`；关键对照 `PROBE_SELECTED_HEAD_SPLIT_TAIL_ONLY=1` 在不声明任何 head 组件 local 时也恢复到 good，而默认坏例仍保持 `error-number result=12 class=bad`、`type-number result=18 class=good`。因此上一条“local 保活”解释还需修正：恢复关键不是某个 head 组件 local，而是把 `']' * C(P'='^0) * ']'` 从单表达式改成分步构造改变了 native capture 子对象的引用/生命周期边界；下一步应比较单表达式与分步构造的 native userdata/capture/ktable 持值路径，优先寻找通用“组合表达式构造期间子 pattern 失根或悬挂引用”的修复点。
        - [x] 2026-07-07 复核：扩展 `scripts/probe-native-lpeg-select-bytecode.sh`，新增 `lpeg-split-head-tail-only-error-number` 并反汇编对照默认坏例。默认单表达式为 `maxstack=10`，close-head 的 `P/C/*` 构造全部停留在临时寄存器，只有最终 `c` 写入 `_ENV`，callback 捕获 local `attempts@3`；split-head 对照为 `maxstack=9`，`attempts`、`probe_head_left`、`probe_head_unit`、`probe_head_capture`、`probe_head_right`、`probe_close_head` 等均经 `SETTABUP` 写入 `_ENV`，callback 捕获 `_ENV` 并通过全局 `attempts` 写回。结合 `PROBE_SELECTED_HEAD_SPLIT_TAIL_ONLY=1` 运行结果 good，可知恢复来自子 pattern 进入稳定 `_ENV` 根，而不是纯粹语句拆分；下一步应审查单表达式临时 pattern 在 native 组合 API 返回后是否只被 C 内存浅拷贝引用、未被新 pattern 的 uservalue/ktable/registry 强边持有。
        - [x] 2026-07-07 门禁：使用 `gopls check internal/native/capi_userdata_native.go internal/native/capi_userdata_native_test.go internal/native/capi_stack_native.go` 复核后，新增 `TestNativeLuaUserValueCopyBetweenVisibleUserdata`，模拟 LPeg `copyktable` 依赖的通用 C API 组合 `lua_getuservalue(source)` + `lua_setuservalue(-2)`。测试验证 source/target 均在当前 C frame 可见时，source user value table 可复制到 target userdata，临时 user value 被弹出，外层 Go VM sentinel 不被覆盖。该门禁约束 ktable/user value 复制基础语义，但不把当前 LPeg select-count 退化归因到该组合。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_SELECTED_CLEAR_BEFORE_MATCH=1` 和 `PROBE_SELECTED_GC_BEFORE_MATCH=1`，在 selected tail 构造最终 pattern `c` 后清空 `probe_*`、head 组件、dummy 值并强制 GC，再执行同一 match。短矩阵结果：默认 `error-number` 仍为 `result=12 class=bad`、`type-number` 为 `result=18 class=good`；`PROBE_SELECTED_PROBE_LOCALS=head` 下两者均为 `result=18 class=good`；同组开启清根+GC 后两者仍为 `result=18 class=good`。因此已证伪“恢复依赖 match 阶段外部 local/global 持续保活”是根因，最终 pattern 构造完成后能持有所需引用；当前边界进一步收敛为构造期间单表达式临时子 pattern 缺少稳定根或 native 组合 API 构造期读取了已失根/被污染的临时对象。
        - [x] 2026-07-07 复核：继续为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_SELECTED_HEAD_WARMUP_DEFAULT_TAIL=1`、`PROBE_SELECTED_HEAD_WARMUP_GC_BEFORE_TAIL=1` 和 `PROBE_GC_BEFORE_TAIL=1`。warmup-only 会先构造并清掉 `']' * C(P'='^0) * ']'`，但真实 probe 仍使用默认单表达式 tail；结果显示默认 `error-number` 为 `12` bad、`type-number` 为 `18` good，warmup-only 下两者均恢复为 `18` good，warmup 后强制 GC 再构造默认 tail 仍均为 `18` good；而 GC-only 不构造 warmup 时 `error-number` 与 `type-number` 均退化为 `12` bad。因此已证伪“恢复必须让预构造 head 作为真实 Cmt 输入”与“单纯 GC 即可恢复”；当前边界更像构造期分配/GC/临时根状态影响后续 LPeg 组合构造，而不是 match 阶段外部保活或 LPeg API 名称本身。
        - [x] 2026-07-07 复核：继续把 warmup-only 泛化为 `PROBE_SELECTED_WARMUP_KIND=<pany|pchar|seq|capture|back|runtime|head>`，并在每种 warmup 后清空局部、强制 GC、再执行默认 tail。短矩阵显示 `pchar`、`seq`、`head` 均把 `error-number` 和 `type-number` 恢复为 `18` good；`pany`、`capture`、`back`、`runtime` 均为 `12` bad。因此已证伪“任意 LPeg pattern 分配即可恢复”和“capture/ktable/runtime capture 构造即可恢复”，恢复条件继续收窄到字符/string pattern 或包含该类构造的序列/head warmup 对后续构造期状态的影响。
        - [x] 2026-07-07 复核：继续扩展 `PROBE_SELECTED_WARMUP_KIND`，覆盖 `pempty/ptrue/pfalse/pnum0/pany/pnum2/pnumneg1/pchar/pstring2/set-empty/set-single/set-range`。短矩阵显示 `pchar` 和 `set-single` 恢复为 `18` good，`pempty/ptrue/pfalse/pnum0/pany/pnum2/pnumneg1/pstring2/set-empty/set-range` 均为 `12` bad；结合 LPeg 源码，恢复条件进一步收窄为会生成单叶 `TChar` 的 warmup，而不是 TTrue/TFalse/TAny/TNot/TSeq/TSet 或一般字符串 pattern。
        - [x] 2026-07-07 复核：继续用现有 warmup/GC 开关做时序二分；`pchar` warmup 不做 GC 时已把 `error-number` 与 `type-number` 都恢复为 `18` good，`GC-before-tail + pchar`、`pchar + GC-before-tail`、`GC-before-tail + pchar + GC-before-tail` 也均保持 good；同样位置的 `pany` 不恢复，且 `GC-before-tail + pany` 会让 `type-number` 也退化为 `12` bad。因此已证伪“warmup 必须先被 GC 才能恢复”和“GC 顺序是主因”，恢复条件继续指向单叶 `TChar` 构造本身改变了后续默认 tail 的 native pattern 构造期状态。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_SELECTED_WARMUP_MATCH=1`，让 warmup pattern 构造后先执行一次 `pcall(function() return probe_warmup:match("]]") end)` 再清空并构造默认 tail。短矩阵显示 `pchar` 构造-only 和 match 后均为 `18` good；`pany` 构造-only 仍为 `error-number=12 bad/type-number=18 good`，`pany` match 后以及 match+GC 后两组均为 `12` bad。因此已证伪“任意 pattern 只要经历一次编译/匹配即可恢复”，恢复条件继续限定为单叶 `TChar` 构造路径，而不是 LPeg VM 编译或 match side effect 本身。
        - [x] 2026-07-07 复核：继续为 `PROBE_SELECTED_WARMUP_KIND` 增加 `pchar-open/pchar-eq/pchar-a/set-single-a`，二分恢复是否来自任意单叶 `TChar`。短矩阵显示 `pchar` 即 `m.P']'` 和 `set-single` 即 `m.S']'` 恢复为 `18` good，但 `m.P'['`、`m.P'='`、`m.P'a'`、`m.S'a'`、`m.P'ab'` 和 `m.P(1)` 均保持 `error-number=12 bad/type-number=18 good`。因此上一轮“单叶 TChar”解释需继续收窄：恢复与最终 close-head 使用的同字符 `]` 构造相关，而不是 TChar tag、任意字符 pattern、任意 single-char set 或 LPeg 编译/匹配副作用。
        - [x] 2026-07-07 复核：为 `scripts/probe-native-lpeg-select-count.sh` 增加 `PROBE_SELECTED_WARMUP_BEFORE_MODE=1`，把同一 warmup 片段放到 fixed-result CALL 扰动模式之前执行，再用默认单表达式 tail 复测。短矩阵显示基线 `error-number=12 bad/type-number=18 good`，`pchar` 后置 warmup 与前置 warmup 均恢复为 `18 good`，而 `pany` 前置 warmup 仍保持 `error-number=12 bad/type-number=18 good`。因此恢复不是“在扰动后修复被污染状态”，更像是同字符 `]` 构造路径在扰动前已经建立了能持续影响后续构造的 native/VM 状态；后续应检查这类状态是否来自通用 string/char pattern 构造时的 userdata/metatable/uservalue、registry、interned string、GC root 或临时 C API 栈根，而不能按 LPeg pattern 名称特判。
        - [ ] 修复门禁：LPeg 只作为真实第三方 C 模块集成验收和问题暴露样本，禁止通过模块名、测试行号、特定 pattern、特定返回值或 LPeg 私有行为做特向性修复；任何生产代码修复都必须能解释为通用 Lua 5.3 C API、VM 调用帧、vararg、返回值写回、栈隔离、registry/metatable/userdata 或错误恢复语义，并补充模块无关的定向测试或 probe。
        - [ ] 下一步继续定位构造期生命周期缺口：优先比较单叶 `TChar` warmup 与 TTrue/TFalse/TAny/TNot/TSeq/TSet/capture/back/runtime warmup 在 native userdata 形态、metatable/uservalue 关联、临时对象 finalizer、弱表 sweep 和 C API 栈可见根上的差异；任何生产修复仍必须落到通用 Lua 5.3 C API 或 VM/GC 生命周期语义，不能按 LPeg pattern 特例处理。
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
  - [x] `scripts/test-native-lpeg.sh`
  - [x] `scripts/probe-native-lpeg-1159.sh`
  - [x] `scripts/bisect-native-lpeg-1159-prefix.sh`
  - [x] `scripts/probe-native-lpeg-1159-call-kinds.sh`
  - [x] `scripts/probe-native-lpeg-select-count.sh`
  - [x] `scripts/probe-native-lpeg-select-bytecode.sh`
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
- native 修复必须优先落在通用 Lua 5.3 public C API 和 Go VM 语义上；真实模块 probe 只能作为验收证据，禁止为单个 C 模块、单个脚本行号或单个测试样例写特向性逻辑。
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
- 2026-07-06：补齐 `lua_rawlen`；native shim 按 Lua 5.3 raw length 语义读取 string 字节长度、table 基础长度边界和 native full userdata 逻辑分配大小，不触发 `__len` 元方法，不改变栈顶。LPeg 1.1.0 运行期探针已从 `_lua_rawlen` 前移到 `_lua_settable`。
- 2026-07-06：补齐 `lua_settable`；native shim 按 Lua 5.3 普通 table 写入语义消费栈顶 key/value，已有 raw 字段直接覆盖，raw 未命中时支持 table/Go function 形式 `__newindex` 链。LPeg 1.1.0 运行期探针已从 `_lua_settable` 前移到 `_lua_setuservalue`。
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
- 2026-07-06：修正 C function frame 内负索引可见栈边界；`nativeLuaValueAt`、`lua_copy` 目标索引和 `lua_rotate` 区间均禁止通过过深负索引穿透到调用帧之前的外层 Go VM 栈。新增定向测试覆盖 `lua_type(-3)` 返回 none、`lua_pushvalue(-3)` no-op、`lua_copy` 无效 source/target no-op 和 `lua_rotate(-3)` 不改变外层栈。该切口补强通用 C API 栈隔离语义，LPeg 1159 仍需继续对比 pattern userdata / ktable / registry 状态。
- 2026-07-06：新增 `scripts/probe-native-lpeg-1159.sh`，将 LPeg 完整测试 1159 行的当前定位固化为可复跑诊断脚本；macOS arm64 输出确认 `test.lua:641` 前缀仍返回 `18`，`test.lua:651` 前缀首次退化到 `12`，`test.lua:1153` 保持退化，孤立执行 `645-651` 则仍返回 `18`。本轮未改生产 shim，证伪单独 deep stack overflow/success match 组合为根因，下一轮应继续查 `1-641` 前序状态与 `645-651` 组合后的 pattern/ktable/registry 或 C frame 清理差异。
