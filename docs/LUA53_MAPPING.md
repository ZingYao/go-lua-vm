# Lua 5.3.6 源码迁移映射

## 源码基线

- 来源仓库：`https://github.com/lua/lua.git`
- 标签：`v5.3.6`
- 提交：`75ea9ccbea7c4886f30da147fb67b693b2624c26`
- 本地参考路径：`third_party/lua-5.3.6/`
- 使用规则：仅用于对照阅读、迁移映射和兼容测试参考，不参与 Go 构建，不引入 CGO。

## 迁移原则

- Go 实现必须保持纯 Go，不调用 Lua C API。
- 迁移以行为兼容为目标，不逐行翻译 C 内存模型。
- 每个 Go 方法和函数必须有中文注释，说明功能目标、入参约束、出参语义、错误语义和 Lua 5.3 兼容点。
- 每个分支逻辑必须有中文注释，提前返回必须说明退出原因和影响范围。
- 官方 C 源码中依赖宏、指针和 longjmp 的部分，需要在 Go 中改写为类型安全结构、显式错误和受保护调用边界。

## C 源码到 Go 包映射

| Lua 5.3.6 源码 | Go 目标目录 | 迁移重点 |
| --- | --- | --- |
| `lapi.c` / `lapi.h` | `lua/`、`runtime/` | 对外 API、栈访问、类型转换、protected call。 |
| `lauxlib.c` / `lauxlib.h` | `lua/`、`stdlib/` | 辅助检查、buffer、库注册、错误格式化。 |
| `lbaselib.c` | `stdlib/base` | `_G`、`load`、`pcall`、`pairs`、raw API。 |
| `lbitlib.c` | `stdlib/math`、`tests/compat` | Lua 5.2 bit32 兼容参考；Lua 5.3 主要使用原生位运算。 |
| `lcode.c` / `lcode.h` | `compiler/codegen` | 寄存器分配、常量表、跳转回填、表达式 codegen。 |
| `lcorolib.c` | `stdlib/coroutine`、`runtime/` | coroutine API、resume/yield/error 边界。 |
| `lctype.c` / `lctype.h` | `compiler/lexer` | 字符分类、空白、数字、标识符判断。 |
| `ldblib.c` | `stdlib/debug` | debug 标准库 API。 |
| `ldebug.c` / `ldebug.h` | `debug/`、`runtime/` | traceback、hook、局部变量、upvalue、行号信息。 |
| `ldo.c` / `ldo.h` | `runtime/` | 调用栈、protected call、错误恢复、yield 边界。 |
| `ldump.c` | `bytecode/` | binary chunk dump。 |
| `lfunc.c` / `lfunc.h` | `runtime/` | closure、Proto、upvalue open/close。 |
| `lgc.c` / `lgc.h` | `runtime/` | GC 生命周期、root 标记、finalizer 策略。 |
| `linit.c` | `stdlib/`、`lua/` | 标准库注册顺序。 |
| `liolib.c` | `stdlib/io` | file userdata、读写、seek、popen 策略。 |
| `llex.c` / `llex.h` | `compiler/lexer` | token、字符串、数字、注释、错误位置。 |
| `llimits.h` | `runtime/`、`bytecode/` | 栈限制、整数范围、指令限制。 |
| `lmathlib.c` | `stdlib/math` | math 函数、整数/浮点边界、随机数策略。 |
| `lmem.c` / `lmem.h` | `runtime/` | Go 内存预算、资源限制错误。 |
| `loadlib.c` | `stdlib/package` | require、searchers、path、动态库策略。 |
| `lobject.c` / `lobject.h` | `runtime/` | TValue、类型标签、转换、字符串格式。 |
| `lopcodes.c` / `lopcodes.h` | `bytecode/` | opcode 定义、编码、解码、反汇编。 |
| `loslib.c` | `stdlib/os` | 时间、环境变量、文件系统、进程策略。 |
| `lparser.c` / `lparser.h` | `compiler/parser`、`compiler/codegen` | 语法、作用域、goto、函数、表达式优先级。 |
| `lprefix.h` | `internal/cli` | 平台前缀逻辑参考。 |
| `lstate.c` / `lstate.h` | `runtime/` | State、global state、registry、main thread。 |
| `lstring.c` / `lstring.h` | `runtime/` | 字符串驻留、hash、短/长字符串策略。 |
| `lstrlib.c` | `stdlib/string` | string 函数、pattern 引擎、pack/unpack。 |
| `ltable.c` / `ltable.h` | `runtime/` | Table 数组/hash 双区、长度、next、rehash。 |
| `ltablib.c` | `stdlib/table` | concat、insert、move、pack、remove、sort、unpack。 |
| `ltests.c` / `ltests.h` | `tests/compat` | 官方内部测试参考，不进入产物。 |
| `ltm.c` / `ltm.h` | `runtime/` | tag method、metamethod 查询和缓存策略。 |
| `lua.c` | `cmd/glua`、`internal/cli` | CLI 参数、REPL、错误输出、退出码。 |
| `lua.h` / `luaconf.h` / `lualib.h` | `lua/`、`stdlib/` | 公共 API 常量、配置、标准库名称。 |
| `luac.c` | `internal/luac` | bytecode 编译、dump、反汇编工具。 |
| `lundump.c` / `lundump.h` | `bytecode/` | binary chunk load 和校验。 |
| `lutf8lib.c` | `stdlib/utf8` | utf8 API、非法编码边界。 |
| `lvm.c` / `lvm.h` | `runtime/`、`bytecode/` | VM 指令循环、算术、比较、调用、for、元方法。 |
| `lzio.c` / `lzio.h` | `compiler/lexer`、`bytecode/` | 输入流抽象、reader、buffer。 |

## 重点映射明细

### `lbaselib.c` → `stdlib/base`

- 目标职责：实现 base library 的核心函数（`error`/`assert`/`dofile`/`load`/`loadfile`/`pcall`/`xpcall`/`print` 等）与 `_G`、`_VERSION` 全局语义。
- 映射切口：
  - `luaopen_base` 对齐标准库入口注册顺序。
  - `base_funcs` 对齐可导出函数列表与元信息。
  - `b_str2int`、`b_tonumber`、`b_tostring` 等类型辅助逻辑映射到 `runtime` 与 `lua` API 的可复用层。
  - `luaL_argcheck`、`luaL_check*` 系列转换与 `runtime` 检查器组合使用，统一错误文本与边界处理。
- 实施建议：
  - 先补 `_G`、`_VERSION`、`assert`、`error`、`print`，再补文件加载与错误恢复函数。
  - 对每个函数补充 success/error 两类单测，覆盖参数边界、异常栈回传语义、返回值个数。

### `lbitlib.c` → `stdlib/base`（兼容层）

- 目标职责：建立 Lua 5.3 位运算语义的兼容对照，不再引入独立 bit32 API 命名，但需保证 `& | ~ << >>` 与 metamethod fallback 的行为一致。
- 映射切口：
  - 与 `lobject` 数值分支结合，实现整数优先、浮点回退到标准转换的行为。
  - 与 `runtime/metamethod` 结合，确保 `__band/__bor/__bxor/__shl/__shr/__bnot/__idiv` 等元方法在必要路径上可命中。
  - 与 `runtime/value` 的整数溢出边界保持一致，不新增与 C 版本冲突的位序。
- 实施建议：
  - 以兼容脚本建立 `tests/compat` 的 golden（如官方 `bitwise.lua` 及扩展边界）。
  - 在 `docs/COMPATIBILITY.md` 记录与 Lua 5.3 位语义的差异和确认点。

### `lcode.c` → `compiler/codegen`

- 目标职责：对齐寄存器分配、跳转回填、局部变量与常量池构建行为，输出 Lua 5.3 `Proto` 与 `Code`。
- 映射切口：
  - `code*` 系列过程映射到 `compiler/codegen/codegen.go` 的表达式/语句编排阶段。
  - `codearith`、`codecomp`、`codebitwise` 等分支映射到 opcode 生成逻辑。
  - `for` 循环、闭包、局部变量生命周期与 Lua 的 `goto` 修复逻辑形成一致的语义边界。
- 实施建议：
  - 将 `lopcodes` 的字段语义与 `Proto.Code` 的映射关系稳定在共享测试中。
  - 增补反汇编 golden，验证 `if`/`for`/`repeat` 的 label 与回填顺序。

### `lcorolib.c` → `stdlib/coroutine` + `runtime`

- 目标职责：实现 `coroutine.create/resume/yield/status/running/wrap` API 与 Go 回调边界 yield 约束。
- 映射切口：
  - `luaB_` 前缀函数映射到公开 API。
  - `luaB_coresume`、`luaB_yield` 的返回值与错误传播对应到 `runtime.State` 异常路径。
  - `co_status` 与 `co_auxwrap` 的状态机映射到 `runtime.Thread` 与 call frame 标记。
- 实施建议：
  - 先补充 `coroutine.create`、`coroutine.wrap`、`resume/yield/status` 的行为测试，再补 `running`。
  - 明确主线程调用 yield 的错误返回路径，并写死 `thread status` 变化。

### `lctype.c` → `compiler/lexer`

- 目标职责：从 C 版字符分类移植到 Go 版 UTF-8 安全的 token 分类入口，保证关键字、数字前缀、注释/空白识别一致。
- 映射切口：
  - 分类函数（如 `lisspace`/`isdigit` 等语义）转为 `compiler/lexer/*` 内部工具。
  - 结合 `unicode` 与字节级策略，确认 Lua 5.3 接受/拒绝的边界字符集。
  - 与 `lexer.go` 的行列更新钩子保持一致的状态增量方式。
- 实施建议：
  - 将关键字符分类用例收敛到 `compiler/lexer` 的测试表，覆盖 0x00-0xFF 和高位字节映射。
  - 与 `third_party/lua-5.3.6/` 的注释/关键字用例同步更新。

### `ldblib.c` → `stdlib/debug`

- 目标职责：实现 debug 标准库函数表，向 Lua 层暴露 `debug.getinfo`、`debug.traceback`、hook、registry、metatable、local 与 upvalue 操作。
- 映射切口：
  - `dblib` 函数表映射到 `stdlib/debug` 的库注册入口。
  - `db_getinfo`、`db_traceback` 依赖 `debug/` 包读取 call frame、line info、name 信息。
  - `db_getlocal`、`db_setlocal`、`db_getupvalue`、`db_setupvalue` 依赖 `runtime` 的 closure、upvalue 与 active frame 查询能力。
  - `db_sethook`、`db_gethook` 依赖 VM 执行循环提供 call/return/line/count 事件钩子。
- 实施建议：
  - 先实现只读能力：`debug.traceback`、`debug.getinfo`、`debug.getregistry`。
  - 再实现可变能力：local/upvalue/metatable 操作，并对错误参数、关闭帧、尾调用帧建立测试。

### `ldebug.c` → `debug/` + `runtime`

- 目标职责：提供 Debug 元信息解析、traceback 拼接、行号定位、函数名推断、local/upvalue 查询与 hook 事件派发。
- 映射切口：
  - `luaG_traceexec` 映射到 VM 指令循环中的 hook 检查点。
  - `luaG_getfuncline`、`luaG_findlocal` 映射到 `bytecode.Proto` 的 line info/local var info 查询。
  - `luaG_runerror` 与 `luaG_errormsg` 映射到 runtime 错误分类与受保护调用边界。
  - `lua_getinfo` 相关语义由 `debug/` 包聚合 runtime frame 与 Proto 元信息实现。
- 实施建议：
  - 先稳定 frame 元信息结构，再补 traceback 字符串格式。
  - hook 实现必须明确重入规则：hook 回调错误要进入受保护调用错误路径，不得破坏主 VM 栈。

### `ldo.c` → `runtime`

- 目标职责：实现 Lua 调用边界、protected call、错误恢复、yield/resume 边界、Go panic 与 Lua error 的转换。
- 映射切口：
  - `luaD_call`、`luaD_precall`、`luaD_poscall` 对应 `runtime.State` 的 call frame 入栈、出栈和返回值搬运。
  - `luaD_pcall` 对应 Go `recover` + 显式 error object 的保护调用。
  - `luaD_throw`、`luaD_rawrunprotected` 对应 runtime error 分类和 traceback 保留。
  - `lua_resume`、`lua_yieldk` 对应 `runtime.Thread` 状态机和 coroutine 标准库入口。
- 实施建议：
  - 所有调用路径先统一到一个 frame 生命周期辅助层，避免 Go closure、Lua closure、tail call 三套逻辑分叉。
  - 建立 panic、runtime error、context cancel、yield across Go callback 的独立测试。

### `ldump.c` → `bytecode`

- 目标职责：实现 Lua 5.3 binary chunk dump，将 `Proto`、常量、upvalue、debug info 按官方格式序列化。
- 映射切口：
  - `DumpHeader` 对应 `bytecode` 的 chunk header 写出和平台特征字段。
  - `DumpFunction` 对应 `bytecode.Proto` 的递归写出。
  - `DumpConstants`、`DumpCode`、`DumpDebug` 对应常量池、指令流、line info/local/upvalue name 写出。
  - `strip` 参数对应是否保留 debug info 的 dump 选项。
- 实施建议：
  - 先保证本项目 load/dump roundtrip，再与官方 Lua 5.3 的 `string.dump`/`luac` 输出做兼容验证。
  - 文档需明确跨架构兼容范围，特别是整数宽度、浮点格式、端序字段。

### `lfunc.c` → `runtime`

- 目标职责：实现 Lua closure、Go closure、upvalue open/close、闭包生命周期和 GC 标记入口。
- 映射切口：
  - `luaF_newLclosure`、`luaF_newCclosure` 对应 runtime closure 构造。
  - `luaF_findupval` 对应 open upvalue 查找与复用。
  - `luaF_close` 对应函数返回、block 退出、tail call 替换时关闭 upvalue。
  - `luaF_getlocalname` 对应 Debug local var 查询。
- 实施建议：
  - upvalue 关闭必须与 call frame 生命周期绑定，先测闭包捕获局部变量，再测循环变量捕获。
  - GC 标记阶段要从 closure 走到 Proto、upvalue、Go callback 引用，避免 Go/Lua 互调时丢引用。

### `lgc.c` → `runtime`

- 目标职责：在纯 Go 环境下提供 Lua object 生命周期管理、root 标记、弱表语义评估、userdata finalizer 策略和资源预算检查。
- 映射切口：
  - `luaC_step`、`luaC_fullgc` 映射到 `runtime` 的显式 GC 调度入口和 `collectgarbage` 后续实现。
  - `reallymarkobject` 语义映射到 State、registry、stack、closure、upvalue、table、thread、userdata 的可达性遍历。
  - `luaC_barrier*` 在 Go 实现中先记录为增量 GC 差异点，第一阶段不逐条模拟写屏障。
  - `GCTM` 与 `__gc` 映射到 userdata finalizer 策略，并在 `docs/GC.md` 记录启用边界。
- 实施建议：
  - 第一阶段优先验证不会丢失 Go/Lua 双向引用，暂不追求 C Lua 增量步进一致。
  - 建立压力测试覆盖闭包环、表环、userdata finalizer、coroutine 栈 root。

### `linit.c` → `stdlib` + `lua`

- 目标职责：定义标准库注册顺序与 `OpenLibs` 对外入口，保持 Lua 5.3 默认打开库集合一致。
- 映射切口：
  - `loadedlibs` 表映射到 `stdlib` 内部注册清单。
  - `luaL_openlibs` 映射到 `lua.State.OpenLibs` 或等价公开方法。
  - base library 需最先注册，随后按 coroutine、table、io、os、string、utf8、math、debug、package 顺序开放。
  - package/dynamic loading 在无 CGO 约束下保留纯 Go searcher 策略，动态 C 模块默认禁用。
- 实施建议：
  - 每个标准库先提供独立 `OpenXxx`，再由 `OpenLibs` 聚合。
  - 测试需要验证 `_G`、`package.loaded`、库表名和重复打开库的幂等性。

### `liolib.c` → `stdlib/io`

- 目标职责：实现 Lua `io` 标准库、file userdata、默认输入输出、文件读写、seek、flush、close 以及 `popen` 策略。
- 映射切口：
  - `io_*` 函数表映射到 `stdlib/io` 注册入口。
  - `LStream` 映射到纯 Go file userdata，底层使用 `os.File` 与受控接口封装。
  - `io.lines`、file `:lines` 需要维护迭代闭包与关闭语义。
  - `io.popen` 在无 CGO 和安全约束下默认单独配置，未启用时返回明确错误。
- 实施建议：
  - 先实现 `io.open/read/write/close/type` 和 file 方法，再补默认 stdin/stdout/stderr。
  - 所有文件系统访问必须受 `Options` 或 sandbox 策略控制，并建立临时目录隔离测试。

### `llex.c` → `compiler/lexer`

- 目标职责：实现 Lua 5.3 token 扫描、长字符串/长注释、数字字面量、关键字、错误位置与输入流推进。
- 映射切口：
  - `luaX_next`、`luaX_lookahead` 映射到 lexer token 流接口。
  - `read_long_string` 映射到长字符串和长注释状态机。
  - `read_numeral` 映射到十进制/十六进制整数、浮点、指数解析。
  - `txtToken` 与 `lexerror` 映射到 parser 可消费的错误分类与行列信息。
- 实施建议：
  - 用官方测试中的 `literals.lua`、`strings.lua` 补充 golden。
  - 对非法长括号、未闭合字符串、非法数字、EOF 边界建立定向测试。

### `lmathlib.c` → `stdlib/math`

- 目标职责：实现 Lua 5.3 `math` 标准库，覆盖整数/浮点分支、随机数、常量、类型判断和无符号比较。
- 映射切口：
  - `mathlib` 函数表映射到 `stdlib/math` 注册入口。
  - `math_random`、`math_randomseed` 需要可控随机源，避免测试依赖真实随机顺序。
  - `math_type`、`math_tointeger` 依赖 runtime number conversion，与 Lua 5.3 整数/浮点边界一致。
  - `math_ult` 需要按 `uint64` 语义比较 Lua integer，避免 Go 有符号比较误差。
- 实施建议：
  - 先实现无状态函数和常量：`abs/floor/ceil/min/max/pi/huge/maxinteger/mininteger`。
  - 随机数实现需支持注入种子，测试固定序列，并记录与 C Lua `rand` 序列的兼容差异。

### `lmem.c` → `runtime`

- 目标职责：将 C Lua 的内存分配失败语义迁移为 Go 资源预算、对象计数、栈限制和可分类错误。
- 映射切口：
  - `luaM_realloc_` 映射到 runtime 的分配预算检查，不直接模拟 C realloc。
  - `luaM_growaux_` 映射到 stack、Proto、table 等结构扩容前的容量上限检查。
  - `luaM_toobig` 映射到 resource limit error，支持 `errors.Is/As` 分类。
  - Go GC 负责物理内存释放，Lua 语义层只维护预算和生命周期引用关系。
- 实施建议：
  - 所有可增长结构需走统一预算检查，避免某些路径绕过最大栈深度或最大分配预算。
  - 测试覆盖栈增长、常量池增长、table 扩容、binary chunk 读入超限。

### `loadlib.c` → `stdlib/package`

- 目标职责：实现 `package` 标准库、`require`、searchers、path/cpath、`package.loaded` 和无 CGO 约束下的动态加载策略。
- 映射切口：
  - `ll_require` 映射到纯 Go module loader，先查 `package.loaded`，再按 searchers 顺序加载。
  - `searcher_Lua` 映射到 Lua 文件搜索和源码加载。
  - `searcher_C`、`loadfunc` 在本项目中默认禁用 C 动态库加载，返回明确不支持错误。
  - `package.path`、`package.searchpath` 需要兼容 Lua 5.3 模板替换、分隔符和错误聚合文本。
- 实施建议：
  - 先实现纯 Lua 文件 searcher 与 preload searcher，再补 `package.searchpath`。
  - `cpath` 保留字段但不接入动态库；该差异必须写入 `docs/COMPATIBILITY.md`。

### `lobject.c` → `runtime`

- 目标职责：实现 Lua TValue 语义在 Go 中的类型标签、数值转换、字符串转换、比较、Debug 展示和错误文本格式化。
- 映射切口：
  - `ttype`、`ttis*` 宏映射到 `runtime.ValueKind` 和类型判断方法。
  - `luaO_str2num` 映射到 string 到 integer/float 的 Lua 5.3 兼容转换。
  - `luaO_tostring` 映射到 number 到 string 的规范格式化。
  - `luaO_pushvfstring`、`luaO_chunkid` 语义映射到错误消息和 traceback 文本辅助函数。
- 实施建议：
  - 数值转换测试必须覆盖十进制、十六进制、指数、边界整数、NaN/Inf 拒绝路径。
  - 错误文本格式化要与 parser/runtime/debug 共享，避免不同包生成不一致消息。

### `lopcodes.c` / `lopcodes.h` → `bytecode`

- 目标职责：固化 Lua 5.3 opcode 枚举、参数模式、编码/解码、RK 常量寻址和反汇编名称。
- 映射切口：
  - `OpCode` 枚举映射到 `bytecode` 的 opcode 常量，顺序必须与官方一致。
  - `getOpMode`、`getBMode`、`getCMode` 映射到 opcode 模式表。
  - `CREATE_ABC`、`CREATE_ABx`、`GETARG_*`、`SETARG_*` 映射到指令字段编解码。
  - `luaP_opnames` 映射到反汇编输出和测试 golden。
- 实施建议：
  - opcode 顺序、模式表、指令字段位宽必须有独立单测保护。
  - 反汇编 golden 需要覆盖 ABC、ABx、AsBx、Ax 与 RK 常量标记。

### `loslib.c` → `stdlib/os`

- 目标职责：实现 Lua `os` 标准库中时间、环境变量、文件系统和进程相关能力，并定义宿主访问策略。
- 映射切口：
  - `os_clock` 映射到单调或进程时间策略，需在兼容文档说明与 C Lua 差异。
  - `os_date`、`os_time`、`os_difftime` 映射到 Go `time`，保留 Lua 5.3 格式化边界。
  - `os_getenv`、`os_remove`、`os_rename`、`os_tmpname` 需受 sandbox/Options 控制。
  - `os_execute`、`os_exit` 在嵌入模式和 CLI 模式下语义不同，需要通过配置明确允许范围。
- 实施建议：
  - 先实现纯时间函数，后实现文件系统函数，最后实现进程相关函数。
  - 测试不得依赖真实本地时区漂移；时间、环境变量和临时文件都要可控隔离。

### `lparser.c` → `compiler/parser` + `compiler/codegen`

- 目标职责：实现 Lua 5.3 语法结构、作用域、局部变量生命周期、goto/label 校验、表达式优先级和函数体解析。
- 映射切口：
  - `luaY_parser` 映射到 parser 入口，输出 AST 或直接交给 codegen 的中间结构。
  - `statlist`、`statement`、`expr`、`subexpr` 映射到递归下降解析函数。
  - `enterblock`、`leaveblock`、`new_localvar` 映射到作用域栈和局部变量生命周期。
  - `labelstat`、`gotostat`、`movegotosout` 映射到 goto/label 合法性检查。
- 实施建议：
  - parser 先保持 AST 可测试，再逐步与 codegen 合流，避免语法错误定位和寄存器分配互相耦合。
  - golden 覆盖函数、闭包、局部作用域、goto 跨作用域错误、表达式优先级。

### `lstate.c` → `runtime`

- 目标职责：实现 Lua State、global state、registry、main thread、stack、call frame、panic/error 边界和关闭语义。
- 映射切口：
  - `lua_newstate`、`lua_close` 映射到 `runtime.NewState` 与 `State.Close`。
  - `stack_init`、`luaE_extendCI` 映射到 stack 和 call frame 管理。
  - `registry` 初始化映射到 `RegistryMainThread`、`RegistryGlobals` 等保留索引。
  - `lua_newthread` 映射到 coroutine/thread 对象创建，并共享 global state。
- 实施建议：
  - State 关闭后所有公开方法需要返回稳定错误，不允许继续修改 stack。
  - 测试覆盖 main thread、registry、pseudo-index、关闭后调用、context cancel 检查点。

### `lstring.c` → `runtime`

- 目标职责：实现 Lua 字符串驻留、短字符串复用、长字符串存储、hash、比较和 GC 标记边界。
- 映射切口：
  - `luaS_newlstr`、`internshrstr` 映射到短字符串 intern table。
  - `luaS_hash` 映射到稳定 hash，避免不同进程随机性影响测试。
  - `luaS_eqlngstr` 映射到长字符串字节级比较。
  - 字符串对象的 GC 标记映射到 runtime 生命周期管理。
- 实施建议：
  - 明确短字符串长度阈值，并在兼容文档中说明是否完全匹配 C Lua。
  - 测试覆盖短字符串同一性、长字符串非驻留、hash 稳定、字节长度而非 rune 长度。

### `lstrlib.c` → `stdlib/string`

- 目标职责：实现 Lua `string` 标准库、pattern 引擎、format、pack/unpack、dump 和大小写/截取等基础函数。
- 映射切口：
  - `strlib` 函数表映射到 `stdlib/string` 注册入口。
  - `str_find_aux`、`match`、`matchbalance` 映射到 Lua pattern 引擎，而不是 Go regexp。
  - `str_format` 映射到 Lua 5.3 格式化规则，不能直接等同 `fmt.Sprintf`。
  - `str_pack`、`str_unpack`、`str_packsize` 映射到 binary packing 规则，并与 bytecode endian 处理分离。
- 实施建议：
  - 先实现 `len/sub/byte/char/lower/upper/reverse/rep`，再实现 pattern 和 pack 系列。
  - pattern 测试需要覆盖 capture、balanced match、frontier、空匹配推进和错误 pattern。

### `ltable.c` → `runtime`

- 目标职责：实现 Lua Table 的数组区/hash 区、raw get/set、rehash、长度运算、next 迭代、弱表和元表配合。
- 映射切口：
  - `luaH_new`、`luaH_free` 映射到 table 生命周期和 GC root 关系。
  - `luaH_getint`、`luaH_getstr`、`luaH_get` 映射到整数键、字符串键和通用键查询。
  - `luaH_set`、`luaH_newkey` 映射到 nil/NaN key 校验与写入。
  - `rehash`、`computesizes` 映射到数组区/hash 区调整策略。
- 实施建议：
  - 第一阶段优先行为正确，不强行复制 C 版桶结构；但 length、next、nil key、NaN key 必须兼容。
  - 测试覆盖 sparse table、删除后 next、数组/hash 边界迁移、元方法 raw/非 raw 差异。

### `ltablib.c` → `stdlib/table`

- 目标职责：实现 Lua `table` 标准库，覆盖 concat、insert、move、pack、remove、sort、unpack 及稀疏数组边界。
- 映射切口：
  - `tab_funcs` 映射到 `stdlib/table` 注册入口。
  - `tinsert`、`tremove` 映射到 1-based 顺序表操作，并通过 runtime table API 读写。
  - `tmove` 需处理源区间和目标区间重叠，按 Lua 5.3 语义选择复制方向。
  - `auxsort` 映射到稳定错误传播的排序实现，比较函数错误必须中断并保留 Lua error。
- 实施建议：
  - 先实现不需要回调的 `pack/unpack/concat/insert/remove/move`，再实现 `sort`。
  - 测试覆盖空表、稀疏表、越界位置、重叠 move、比较函数返回非 boolean。

### `ltm.c` / `ltm.h` → `runtime`

- 目标职责：实现 Lua tag method / metamethod 名称表、查找缓存、二元/一元运算 fallback、表访问 fallback 和 `__call`。
- 映射切口：
  - `luaT_init` 映射到 metamethod 名称初始化，名称必须与 Lua 5.3 完全一致。
  - `luaT_gettm`、`luaT_gettmbyobj` 映射到 table/userdata/metatable 的元方法查找。
  - `luaT_callTM`、`luaT_trybinTM` 映射到 runtime 统一调用路径。
  - `fasttm` 的缓存思想可在 Go 中保留为可选优化，第一阶段优先正确性。
- 实施建议：
  - 所有算术、比较、len、concat、index/newindex、call 指令必须通过统一 metamethod 查询入口。
  - 测试覆盖左右操作数查找顺序、同名比较元方法要求、递归 `__index/__newindex` 限制。

### `lua.c` → `cmd/glua` + `internal/cli`

- 目标职责：实现类似官方 `lua` 的命令行入口，覆盖脚本文件、`-e`、`-l`、`-i`、`-v`、stdin、错误输出和退出码。
- 映射切口：
  - `pmain` 映射到 `internal/cli` 的顶层编排，负责创建 State、打开标准库、处理参数。
  - `collectargs` 映射到 CLI 参数解析，保持官方选项优先级。
  - `handle_script`、`dostring`、`dolibrary` 映射到文件执行、片段执行和库加载。
  - `doREPL` 映射到交互模式，需保留多行输入和表达式补全策略。
- 实施建议：
  - CLI 初期先支持 `-v`、脚本文件、`-e`，REPL 和 `-l` 随标准库稳定后启用。
  - golden 测试需要覆盖 stdout、stderr、退出码和 `arg` 表构造。

### `luac.c` → `internal/luac`

- 目标职责：实现 `gluac` 字节码工具，覆盖编译、dump、反汇编、strip debug info 和多文件输入策略。
- 映射切口：
  - `main` 参数处理映射到 `internal/luac` 命令编排。
  - `combine` 语义映射到多 chunk 合并策略，需确认是否与官方 `luac` 完全兼容。
  - `listing` 映射到 `bytecode` 反汇编输出。
  - `dumping` 映射到 `bytecode` binary chunk dump，`-s` 控制 debug info strip。
- 实施建议：
  - 先实现单文件编译与反汇编，再实现输出文件、strip、多文件。
  - 与 `docs/LUAC.md` 保持同步，明确 `gluac` 与官方 `luac` 的兼容范围。

### `lundump.c` / `lundump.h` → `bytecode`

- 目标职责：实现 Lua 5.3 binary chunk load、header 校验、常量读取、Proto 递归读取和 debug info 读取。
- 映射切口：
  - `luaU_undump` 映射到 bytecode loader 入口。
  - `checkHeader` 映射到签名、版本、格式、endianness、int/size_t/instruction/number 宽度校验。
  - `LoadFunction`、`LoadConstants`、`LoadCode`、`LoadDebug` 映射到 `bytecode.Proto` 构建。
  - `LoadUpvalues` 映射到 upvalue descriptor 读取，并与 closure 构造衔接。
- 实施建议：
  - 先保证本项目 dump/load roundtrip，再读取官方 `luac` 产物做兼容测试。
  - 错误路径需区分签名错误、版本不匹配、截断输入、非法常量类型、递归深度超限。

### `lutf8lib.c` → `stdlib/utf8`

- 目标职责：实现 Lua 5.3 `utf8` 标准库，覆盖 char、codes、codepoint、len、offset、charpattern 和非法 UTF-8 边界。
- 映射切口：
  - `utflib` 函数表映射到 `stdlib/utf8` 注册入口。
  - `utf8_decode` 映射到严格 UTF-8 解码，必须识别过长编码、代理区、超出 Unicode 范围等错误。
  - `iter_aux` 映射到 `utf8.codes` 迭代闭包，错误位置需按 Lua 5.3 语义返回。
  - `byteoffset` 映射到 `utf8.offset`，按字节索引处理正负偏移。
- 实施建议：
  - 先实现 `char/codepoint/len/offset`，再实现 `codes` 迭代器。
  - 测试覆盖非法编码、边界 codepoint、负索引、空字符串、截断多字节序列。

### `lvm.c` / `lvm.h` → `runtime` + `bytecode`

- 目标职责：实现 Lua 5.3 VM 指令循环、算术/位运算、比较、表访问、函数调用、闭包、循环、vararg、错误和元方法分派。
- 映射切口：
  - `luaV_execute` 映射到 runtime VM dispatch loop，指令解码来自 `bytecode`。
  - `luaV_gettable`、`luaV_settable` 映射到 raw 表访问与 `__index/__newindex` fallback。
  - `luaV_arith`、`luaV_equalobj`、`luaV_lessthan`、`luaV_lessequal` 映射到值系统和 metamethod。
  - `forlimit`、`luaV_tonumber_`、`luaV_tointeger` 映射到 numeric for 与数值转换边界。
- 实施建议：
  - 指令测试必须覆盖每条 opcode 的直接行为和组合 golden。
  - VM 执行循环需要 hook 检查点、context cancel 检查点、call depth/stack depth 资源限制。

### `lzio.c` / `lzio.h` → `compiler/lexer` + `bytecode`

- 目标职责：实现输入流抽象，支持从字符串、文件、reader 加载 Lua 源码或二进制 chunk，并保持行列与错误位置可追踪。
- 映射切口：
  - `ZIO` 映射到 Go `io.Reader` 包装和可回退的 lexer source。
  - `luaZ_fill` 映射到缓冲读取，处理 EOF、短读、读取错误。
  - `Mbuffer` 映射到 lexer 字符串/长注释暂存 buffer，避免无限增长绕过资源限制。
  - 与 binary chunk loader 共享 reader，但错误分类需要区分源码词法错误和 chunk 读取错误。
- 实施建议：
  - 先统一 `LoadString`、`LoadFile`、`LoadReader` 的输入路径，再接 compiler 与 bytecode。
  - 测试覆盖 reader 短读、读取错误、EOF 边界、超大 token/字符串资源限制。

## 首轮迁移顺序

1. `lopcodes.*`：先固化指令定义和编码，作为 VM、codegen、反汇编共同基础。
2. `lobject.*`、`lstate.*`、`lfunc.*`：建立值、State、Proto、Closure 和 Upvalue。
3. `lvm.*`：实现最小指令执行循环。
4. `llex.*`、`lparser.*`、`lcode.*`：接入源码编译。
5. `lbaselib.c`、`ltablib.c`、`lstrlib.c`：逐步补标准库。
6. `ldebug.*`、`ldblib.c`：补齐 Debug 能力。
