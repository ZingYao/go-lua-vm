# TODO

本清单用于跟踪 Lua 5.3 VM 的完整 Go 迁移。任务粒度按“可实现、可测试、可验收”拆分；每个实现任务完成时必须同步补测试或 golden 用例。

## 0. 项目初始化与工程基线

- [x] 确认 Go module 路径，当前使用 `github.com/zing/go-lua-vm`，如需发布到其他仓库后续再调整。
- [x] 确认最终 CLI 二进制名称为 `glua`，字节码工具名称为 `gluac`。
- [x] 确认 Lua 5.3 迁移基线版本为 `lua-5.3.6`。
- [x] 确认 “Cpp 源码” 当前按 Lua 官方 C 源码理解；如后续提供 C++ 仓库，另建映射文档。
- [x] 使用 `go1.26.4` 初始化 `go.mod`，设置 `go 1.26` 与 `toolchain go1.26.4`。
- [x] 确认本机 Go SDK `/Users/zing/sdk/go/go1.26.4` 可用，项目脚本不硬编码该路径。
- [x] 创建 `cmd/glua` CLI 目录。
- [x] 创建 `lua` 对外嵌入 API 目录。
- [x] 创建 `runtime` VM 运行时目录。
- [x] 创建 `compiler/lexer` 词法目录。
- [x] 创建 `compiler/parser` 语法目录。
- [x] 创建 `compiler/codegen` 代码生成目录。
- [x] 创建 `bytecode` 指令与 chunk 目录。
- [x] 创建 `stdlib` 标准库目录。
- [x] 创建 `debug` Debug 能力目录。
- [x] 创建 `bridge` Go/Lua 桥接目录。
- [x] 创建 `tests/compat` 兼容测试目录。
- [x] 创建 `tests/golden` golden 资产目录。
- [x] 创建 `internal/cli` CLI 编排目录。
- [x] 创建 `internal/luac` 字节码工具目录。
- [x] 补充 `README.md`，说明目标、状态、构建方式、兼容范围。
- [x] 补充 `Makefile`，包含 test、fmt、build、compat、bench。
- [x] 补充基础 CI 配置。
- [x] 补充 `.gitignore`，排除构建产物、日志、临时测试输出。
- [x] 建立项目级 `AGENTS.md`，写入无 CGO、详细注释、Go 1.26.4 路径与门禁规则。
- [x] 建立基础 Go 门禁脚本 `scripts/check-go-gates.sh`。
- [x] 建立 `docs/LUA53_MAPPING.md`，逐文件映射 Lua 5.3 C 源码到 Go 包。
- [x] 建立 `docs/API.md`，记录 Go 嵌入 API 设计。
- [x] 建立 `docs/COMPATIBILITY.md`，记录与官方 Lua 5.3 的差异。
- [x] 建立 `docs/DEBUG.md`，记录 Debug API 与 hook 设计。
- [x] 建立 `docs/BRIDGE.md`，记录 Go/Lua 双向回调设计。

## 1. Lua 5.3 源码映射与迁移基线

- [x] 记录 Lua 5.3 官方源码下载地址。
- [x] 记录 Lua 5.3 官方源码 checksum。
- [x] 保存源码版本、发布时间、补丁级别。
- [x] 在 `third_party/lua-5.3.6/` 保存 Lua 5.3.6 官方源码参考副本。
- [x] 映射 `lapi.c` 到 Go 对外 API 与栈操作。
- [x] 映射 `lauxlib.c` 到 Go 辅助库 API。
- [x] 映射 `lbaselib.c` 到 base 标准库。
- [x] 映射 `lbitlib.c` 行为到 5.3 原生位运算兼容测试。
- [x] 映射 `lcode.c` 到 codegen。
- [x] 映射 `lcorolib.c` 到 coroutine 标准库。
- [x] 映射 `lctype.c` 到 lexer 字符分类。
- [x] 映射 `ldblib.c` 到 debug 标准库。
- [x] 映射 `ldebug.c` 到 Debug 元信息与 traceback。
- [x] 映射 `ldo.c` 到调用栈、保护调用、错误恢复。
- [x] 映射 `ldump.c` 到 binary chunk dump。
- [x] 映射 `lfunc.c` 到 closure 与 upvalue。
- [x] 映射 `lgc.c` 到 GC 策略。
- [x] 映射 `linit.c` 到标准库注册。
- [x] 映射 `liolib.c` 到 io 标准库。
- [x] 映射 `llex.c` 到 lexer。
- [x] 映射 `lmathlib.c` 到 math 标准库。
- [x] 映射 `lmem.c` 到 Go 内存与资源限制设计。
- [x] 映射 `loadlib.c` 到 package/dynamic loading 策略。
- [x] 映射 `lobject.c` 到值系统。
- [x] 映射 `lopcodes.c` 与 `lopcodes.h` 到 bytecode。
- [x] 映射 `loslib.c` 到 os 标准库。
- [x] 映射 `lparser.c` 到 parser。
- [x] 映射 `lstate.c` 到 State 与 registry。
- [x] 映射 `lstring.c` 到字符串驻留。
- [x] 映射 `lstrlib.c` 到 string 标准库。
- [x] 映射 `ltable.c` 到 Table 实现。
- [x] 映射 `ltablib.c` 到 table 标准库。
- [x] 映射 `ltm.c` 到 tag method / metamethod。
- [x] 映射 `lua.c` 到 CLI 行为。
- [x] 映射 `luac.c` 到可选 luac 兼容工具。
- [x] 映射 `lundump.c` 到 binary chunk load。
- [x] 映射 `lutf8lib.c` 到 utf8 标准库。
- [x] 映射 `lvm.c` 到 VM 指令执行。
- [x] 映射 `lzio.c` 到输入流抽象。

## 2. 测试资产与兼容框架

- [x] 接入 Lua 官方测试套件。
- [x] 为官方测试套件建立本地运行脚本。
- [x] 建立官方 `lua` 与本项目 `glua` 输出对比脚本。
- [x] 建立 stdout golden 对比能力。
- [x] 建立 stderr golden 对比能力。
- [x] 建立退出码 golden 对比能力。
- [x] 建立 parser 错误位置 golden。
- [x] 建立 runtime traceback golden。
- [x] 建立 bytecode roundtrip 测试框架。
- [x] 建立 fuzz 入口：lexer。
- [x] 建立 fuzz 入口：parser。
- [x] 建立 fuzz 入口：binary chunk loader。
- [x] 建立 benchmark：VM dispatch。
- [x] 建立 benchmark：Table 读写。
- [x] 建立 benchmark：函数调用。
- [x] 建立 benchmark：字符串拼接。
- [x] 建立 benchmark：Go/Lua 回调。
- [x] 建立跨平台测试矩阵：darwin。
- [x] 建立跨平台测试矩阵：linux。
- [x] 建立跨平台测试矩阵：windows。

## 3. 值系统与对象模型

- [x] 定义 `nil` 值。
- [x] 定义 boolean 值。
- [x] 定义 integer 值，按 Lua 5.3 `lua_Integer` 语义处理。
- [x] 定义 float 值，按 Lua 5.3 `lua_Number` 语义处理。
- [x] 定义 string 值。
- [x] 定义 table 值。
- [x] 定义 Lua closure 值。
- [x] 定义 Go closure 值。
- [x] 定义 userdata 值。
- [x] 定义 thread/coroutine 值。
- [x] 实现类型标签与类型判断。
- [x] 实现 Lua 值相等比较。
- [x] 实现 raw equality。
- [x] 实现 number integer/float 转换。
- [x] 实现 string 到 number 转换。
- [x] 实现 number 到 string 转换。
- [x] 实现 truthiness 语义。
- [x] 实现值的 Debug 展示。
- [x] 实现 registry 存储。
- [x] 实现全局环境 `_G`。

## 4. 字符串与内存策略

- [x] 实现短字符串驻留。
- [x] 实现长字符串存储策略。
- [x] 实现字符串 hash。
- [x] 实现字符串比较。
- [x] 实现字符串拼接。
- [x] 实现字符串长度按字节计算。
- [x] 设计 Go GC 与 Lua object 生命周期边界。
- [x] 设计资源限制选项：最大栈深度。
- [x] 设计资源限制选项：最大调用深度。
- [x] 设计资源限制选项：最大分配预算。
- [x] 设计资源限制错误类型。

## 5. Table 与元表

- [x] 实现数组区存储。
- [x] 实现 hash 区存储。
- [x] 实现 nil key 禁止策略。
- [x] 实现 NaN key 错误策略。
- [x] 实现 raw get。
- [x] 实现 raw set。
- [x] 实现普通 get raw 优先读取。
- [x] 实现普通 get table 型 `__index` 链。
- [x] 实现普通 get function 型 `__index` 调用。
- [x] 实现普通 set raw 已有字段优先写入。
- [x] 实现普通 set table 型 `__newindex` 链。
- [x] 实现普通 set function 型 `__newindex` 调用。
- [x] 实现 table 长度运算。
- [x] 实现 `next` 迭代。
- [x] 实现 `pairs` 迭代。
- [x] 实现 `ipairs` 迭代。
- [x] 实现 metatable 读取。
- [x] 实现 metatable 设置。
- [x] 实现受保护 metatable。
- [x] 实现 weak key 策略评估。
- [x] 实现 weak value 策略评估。
- [x] 建立 Table resize 测试。
- [x] 建立 Table 迭代稳定性说明。

## 6. 栈、调用帧与 State

- [x] 实现 State 创建。
- [x] 实现 State 关闭。
- [x] 实现主线程。
- [x] 实现 registry。
- [x] 实现 Lua stack push。
- [x] 实现 Lua stack pop。
- [x] 实现绝对索引。
- [x] 实现相对索引。
- [x] 实现 pseudo-index。
- [x] 实现栈扩容。
- [x] 实现栈溢出错误。
- [x] 实现 CallFrame。
- [x] 实现 Lua 函数调用帧。
- [x] 实现 Go 函数调用帧。
- [x] 实现 tail call 帧替换。
- [x] 实现 protected call。
- [x] 实现 error object 传播。
- [x] 实现 traceback frame 收集。
- [x] 实现 context cancel 检查点。

## 7. Bytecode 与 Proto

- [x] 定义 Lua 5.3 指令枚举。
- [x] 定义 opcode 名称表。
- [x] 定义 opcode 模式表与参数模式。
- [x] 定义 iABC 编码。
- [x] 定义 iABx 编码。
- [x] 定义 iAsBx 编码。
- [x] 定义 iAx 编码。
- [x] 实现 opcode decode。
- [x] 实现 opcode encode。
- [x] 实现 RK 常量标记。
- [x] 实现 constant pool。
- [x] 实现 Proto 结构。
- [x] 实现 Proto 子函数列表。
- [x] 实现 upvalue descriptor。
- [x] 实现 line info。
- [x] 实现 local var info。
- [x] 实现 chunk header load。
- [x] 实现 chunk header validate。
- [x] 实现 chunk constant load。
- [x] 实现 chunk Proto load。
- [x] 实现 chunk dump。
- [x] 实现 chunk load/dump roundtrip。
- [x] 实现反汇编输出，辅助调试。
- [x] 设计自定义加密 chunk encoder/decoder，目标是允许嵌入用户在发布侧生成私有加密 chunk，并在运行侧注册自己的解密/解包逻辑，同时保持 VM 只执行标准 Proto/bytecode。
- [x] 定义 chunk 加载流程：输入字节先由 loader 识别格式，标准 Lua 5.3 binary chunk 直接解析，自定义加密 chunk 命中注册 decoder 后解码为标准 binary chunk，再进入统一 chunk parser 与校验。
- [x] 定义对外注册接口，优先在 `lua` 嵌入 API 暴露稳定 `ChunkDecoder`、`ChunkEncoder`、`WithChunkDecoder` 等选项，避免用户直接依赖 `bytecode` 内部结构。
- [x] 约束 decoder 第一阶段只返回标准 Lua 5.3 binary chunk 字节，禁止直接返回 `Proto` 绕过 header、版本、指令、常量表、嵌套深度等统一校验。
- [x] 约束 encoder 输入必须是标准 Lua 5.3 binary chunk 字节，输出必须包含稳定 magic header、版本号与完整性校验，便于 decoder 精确识别和拒绝损坏数据。
- [x] 设计 decoder 匹配规则：按注册顺序执行 `Match`，首个命中后停止；标准 Lua chunk decoder 默认保留，是否允许禁用标准 chunk 需单独配置。
- [x] 设计安全边界：encoder/decoder 必须支持 `context.Context` 取消，输入与输出必须有最大字节数限制，错误信息不得泄露密钥、明文片段或宿主内部路径。
- [x] 设计异常语义：未命中任何 decoder 返回未知 chunk 格式错误，decoder 解密失败返回解码错误，解密后非法 chunk 统一返回 bytecode 校验错误。
- [x] 设计 CLI 策略：`glua` 默认兼容标准 chunk；自定义加密 chunk 优先通过 Go 嵌入注册，是否提供 CLI 内置示例 decoder 或显式开关后续评估。
- [x] 记录能力边界：该方案只提升通用工具直接反编译门槛，不承诺防止运行时调试、内存 dump 或解密后 Proto 抽取。
- [x] 补充测试计划：覆盖标准 chunk、加密 chunk、encoder/decoder roundtrip、未知格式、decoder 错误、非法解密结果、超大解密结果、context 取消和多 decoder 匹配顺序。

## 8. VM 指令执行

- [x] 实现 `MOVE`。
- [x] 实现 `LOADK`。
- [x] 实现 `LOADKX`。
- [x] 实现 `LOADBOOL`。
- [x] 实现 `LOADNIL`。
- [x] 实现 `GETUPVAL`。
- [x] 实现 `GETTABUP`。
- [x] 实现 `GETTABLE`。
- [x] 实现 `SETTABUP`。
- [x] 实现 `SETUPVAL`。
- [x] 实现 `SETTABLE`。
- [x] 实现 `NEWTABLE`。
- [x] 实现 `SELF`。
- [x] 实现 `ADD`。
- [x] 实现 `SUB`。
- [x] 实现 `MUL`。
- [x] 实现 `MOD`。
- [x] 实现 `POW`。
- [x] 实现 `DIV`。
- [x] 实现 `IDIV`。
- [x] 实现 `BAND`。
- [x] 实现 `BOR`。
- [x] 实现 `BXOR`。
- [x] 实现 `SHL`。
- [x] 实现 `SHR`。
- [x] 实现 `UNM`。
- [x] 实现 `BNOT`。
- [x] 实现 `NOT`。
- [x] 实现 `LEN`。
- [x] 实现 `CONCAT`。
- [x] 实现 `JMP`。
- [x] 实现 `EQ`。
- [x] 实现 `LT`。
- [x] 实现 `LE`。
- [x] 实现 `TEST`。
- [x] 实现 `TESTSET`。
- [x] 实现 `CALL`。
- [x] 实现 `TAILCALL`。
- [x] 实现 `RETURN`。
- [x] 实现 `FORLOOP`。
- [x] 实现 `FORPREP`。
- [x] 实现 `TFORCALL`。
- [x] 实现 `TFORLOOP`。
- [x] 实现 `SETLIST`。
- [x] 实现 `CLOSURE`。
- [x] 实现 `VARARG`。
- [x] 实现 `EXTRAARG`。
- [x] 建立每条 opcode 的单元测试。
- [x] 建立 opcode 组合 golden 测试。

## 9. Lexer

- [x] 实现输入流抽象。
- [x] 实现 UTF-8 字节读取策略。
- [x] 实现行号与列号跟踪。
- [x] 实现空白跳过。
- [x] 实现短注释。
- [x] 实现长注释。
- [x] 实现长字符串。
- [x] 实现短字符串单引号。
- [x] 实现短字符串双引号。
- [x] 实现字符串转义。
- [x] 实现十进制整数。
- [x] 实现十进制浮点数。
- [x] 实现十六进制整数。
- [x] 实现十六进制浮点数。
- [x] 实现标识符。
- [x] 实现关键字。
- [x] 实现操作符 token。
- [x] 实现 EOF token。
- [x] 实现非法 token 错误。
- [x] 建立 lexer golden。

## 10. Parser

- [x] 实现 chunk 解析。
- [x] 实现 block 解析。
- [x] 实现 empty statement。
- [x] 实现 assignment。
- [x] 实现 local assignment。
- [x] 实现 local function。
- [x] 实现 function statement。
- [x] 实现 if/elseif/else。
- [x] 实现 while。
- [x] 实现 repeat until。
- [x] 实现 numeric for。
- [x] 实现 generic for。
- [x] 实现 break。
- [x] 实现 goto。
- [x] 实现 label。
- [x] 实现 return。
- [x] 实现 function body。
- [x] 实现 vararg。
- [x] 实现 table constructor。
- [x] 实现 prefix expression。
- [x] 实现 function call。
- [x] 实现 method call。
- [x] 实现 unary expression。
- [x] 实现 binary expression precedence。
- [x] 实现 right-associative power。
- [x] 实现作用域栈。
- [x] 实现局部变量生命周期。
- [x] 实现 goto/label 合法性校验。
- [x] 实现 `continue` 语法解析，要求只允许出现在循环内，并能在嵌套循环中绑定最近一层循环。
- [x] 实现 `switch/case/default` 语法解析，支持多值 case、最多一个 default，并要求 default 第一阶段放在最后。
- [x] 实现语法扩展注册表，支持按 build tag 编译裁剪，并在 glua、gluac 和 Go API 中按参数关闭已编译扩展。
- [x] 建立 parser 错误恢复策略。
- [x] 建立 parser golden。

## 11. Codegen

- [x] 实现寄存器分配。
- [x] 实现寄存器释放。
- [x] 实现常量去重。
- [x] 实现表达式求值。
- [x] 实现短路逻辑。
- [x] 实现跳转回填。
- [x] 实现局部变量 debug info。
- [x] 实现 upvalue 捕获。
- [x] 实现 nested function Proto。
- [x] 实现 table constructor codegen。
- [x] 实现 function call codegen。
- [x] 实现 tail call codegen。
- [x] 实现 numeric for codegen。
- [x] 实现 generic for codegen。
- [x] 实现 `continue` codegen，将 continue 编译为现有 `JMP`，分别跳到 while 条件、repeat-until 条件、numeric for `FORLOOP`、generic for `TFORCALL`。
- [x] 实现 `switch/case/default` codegen，不新增 VM opcode，通过临时寄存器、`EQ` 与 `JMP` 生成非贯穿多分支控制流。
- [x] 实现 vararg codegen。
- [x] 实现 return codegen。
- [x] 对齐官方 Lua 关键样例反汇编。

## 12. 核心语义与错误恢复

- [x] 实现算术元方法。
- [x] 实现位运算元方法。
- [x] 实现比较元方法。
- [x] 实现 `__len`。
- [x] 实现 `__concat`。
- [x] 实现 `__call`。
- [x] 实现 `__tostring`。
- [x] 实现 `__pairs`。
- [x] 实现 `__ipairs` 兼容策略。
- [x] 实现 `error`。
- [x] 实现 `pcall`。
- [x] 实现 `xpcall`。
- [x] 实现 panic 到 Go error 转换。
- [x] 实现 Go error 到 Lua error 转换。
- [x] 实现 traceback 拼接。
- [x] 实现 runtime error 分类。
- [x] 实现 syntax error 分类。
- [x] 实现 resource limit error 分类。

## 13. Coroutine

- [x] 实现 thread 状态。
- [x] 实现 coroutine create。
- [x] 实现 coroutine resume。
- [x] 实现 coroutine yield。
- [x] 实现 coroutine status。
- [x] 实现 coroutine running。
- [x] 实现 coroutine wrap。
- [x] 实现 main thread yield 禁止。
- [x] 实现跨 Go callback 的 yield 边界规则。
- [x] 实现 coroutine error 传播。
- [x] 建立 coroutine 官方兼容测试。

## 14. GC 与生命周期

- [x] 设计第一阶段 GC 策略。
- [x] 标记 State root。
- [x] 标记 registry root。
- [x] 标记 stack root。
- [x] 标记 closure/upvalue root。
- [x] 标记 table key/value。
- [x] 标记 coroutine stack。
- [x] 处理 userdata finalizer 策略。
- [x] 评估 `__gc` 支持范围。
- [x] 评估增量 GC 与官方 Lua 差异。
- [x] 增加生命周期压力测试。

## 15. Base 标准库

- [x] 实现 `_G`。
- [x] 实现 `_VERSION`。
- [x] 实现 `assert`。
- [x] 实现 `collectgarbage`。
- [x] 实现 `dofile`。
- [x] 实现 `error`。
- [x] 实现 `getmetatable`。
- [x] 实现 `ipairs`。
- [x] 实现 `load`。
- [x] 实现 `loadfile`。
- [x] 实现 `next`。
- [x] 实现 `pairs`。
- [x] 实现 `pcall`。
- [x] 实现 `print`。
- [x] 实现 `rawequal`。
- [x] 实现 `rawget`。
- [x] 实现 `rawlen`。
- [x] 实现 `rawset`。
- [x] 实现 `select`。
- [x] 实现 `setmetatable`。
- [x] 实现 `tonumber`。
- [x] 实现 `tostring`。
- [x] 实现 `type`。
- [x] 实现 `xpcall`。

## 16. Table 标准库

- [x] 实现 `table.concat`。
- [x] 实现 `table.insert`。
- [x] 实现 `table.move`。
- [x] 实现 `table.pack`。
- [x] 实现 `table.remove`。
- [x] 实现 `table.sort`。
- [x] 实现 `table.unpack`。
- [x] 覆盖 sparse table 边界。
- [x] 覆盖 comparator error 边界。

## 17. String 标准库

- [x] 实现 `string.byte`。
- [x] 实现 `string.char`。
- [x] 实现 `string.dump`。
- [x] 实现 `string.find`。
- [x] 实现 `string.format`。
- [x] 实现 `string.gmatch`。
- [x] 实现 `string.gsub`。
- [x] 实现 `string.len`。
- [x] 实现 `string.lower`。
- [x] 实现 `string.match`。
- [x] 实现 `string.pack`。
- [x] 实现 `string.packsize`。
- [x] 实现 `string.rep`。
- [x] 实现 `string.reverse`。
- [x] 实现 `string.sub`。
- [x] 实现 `string.unpack`。
- [x] 实现 `string.upper`。
- [x] 实现 Lua pattern 引擎。
- [x] 实现 capture。
- [x] 实现 balanced match。
- [x] 实现 frontier pattern。

## 18. Math 标准库

- [x] 实现 `math.abs`。
- [x] 实现 `math.acos`。
- [x] 实现 `math.asin`。
- [x] 实现 `math.atan`。
- [x] 实现 `math.ceil`。
- [x] 实现 `math.cos`。
- [x] 实现 `math.deg`。
- [x] 实现 `math.exp`。
- [x] 实现 `math.floor`。
- [x] 实现 `math.fmod`。
- [x] 实现 `math.huge`。
- [x] 实现 `math.log`。
- [x] 实现 `math.max`。
- [x] 实现 `math.maxinteger`。
- [x] 实现 `math.min`。
- [x] 实现 `math.mininteger`。
- [x] 实现 `math.modf`。
- [x] 实现 `math.pi`。
- [x] 实现 `math.rad`。
- [x] 实现 `math.random`。
- [x] 实现 `math.randomseed`。
- [x] 实现 `math.sin`。
- [x] 实现 `math.sqrt`。
- [x] 实现 `math.tan`。
- [x] 实现 `math.tointeger`。
- [x] 实现 `math.type`。
- [x] 实现 `math.ult`。

## 19. UTF-8 标准库

- [x] 实现 `utf8.char`。
- [x] 实现 `utf8.charpattern`。
- [x] 实现 `utf8.codes`。
- [x] 实现 `utf8.codepoint`。
- [x] 实现 `utf8.len`。
- [x] 实现 `utf8.offset`。
- [x] 覆盖非法 UTF-8 边界。

## 20. IO 与 OS 标准库

- [x] 确认默认是否允许访问宿主文件系统。
- [x] 设计 sandbox 选项。
- [x] 实现 file userdata。
- [x] 实现 `io.close`。
- [x] 实现 `io.flush`。
- [x] 实现 `io.input`。
- [x] 实现 `io.lines`。
- [x] 实现 `io.open`。
- [x] 实现 `io.output`。
- [x] 实现 `io.popen` 策略。
- [x] 实现 `io.read`。
- [x] 实现 `io.tmpfile`。
- [x] 实现 `io.type`。
- [x] 实现 `io.write`。
- [x] 实现 file `:close`。
- [x] 实现 file `:flush`。
- [x] 实现 file `:lines`。
- [x] 实现 file `:read`。
- [x] 实现 file `:seek`。
- [x] 实现 file `:setvbuf`。
- [x] 实现 file `:write`。
- [x] 实现 `os.clock`。
- [x] 实现 `os.date`。
- [x] 实现 `os.difftime`。
- [x] 实现 `os.execute` 策略。
- [x] 实现 `os.exit`。
- [x] 实现 `os.getenv`。
- [x] 实现 `os.remove`。
- [x] 实现 `os.rename`。
- [x] 实现 `os.setlocale` 策略。
- [x] 实现 `os.time`。
- [x] 实现 `os.tmpname`。

## 21. Package 标准库

- [x] 实现 `require`。
- [x] 实现 `package.config`。
- [x] 实现 `package.cpath` 策略。
- [x] 实现 `package.loaded`。
- [x] 实现 `package.loadlib` 策略。
- [x] 实现 `package.path`。
- [x] 实现 `package.preload`。
- [x] 实现 `package.searchers`。
- [x] 实现 `package.searchpath`。
- [x] 实现 Lua 文件 loader。
- [x] 实现预加载模块 loader。
- [x] 设计 Go 模块 loader。
- [x] 评估 C 动态库 loader 是否支持或明确不支持。

## 22. Debug 标准库与调试信息

- [x] 实现 `debug.debug` 策略。
- [x] 实现 `debug.gethook`。
- [x] 实现 `debug.getinfo`。
- [x] 实现 `debug.getlocal`。
- [x] 实现 `debug.getmetatable`。
- [x] 实现 `debug.getregistry`。
- [x] 实现 `debug.getupvalue`。
- [x] 实现 `debug.getuservalue`。
- [x] 实现 `debug.sethook`。
- [x] 实现 `debug.setlocal`。
- [x] 实现 `debug.setmetatable`。
- [x] 实现 `debug.setupvalue`。
- [x] 实现 `debug.setuservalue`。
- [x] 实现 `debug.traceback`。
- [x] 实现 `debug.upvalueid`。
- [x] 实现 `debug.upvaluejoin`。
- [x] 实现 call hook。
- [x] 实现 return hook。
- [x] 实现 line hook。
- [x] 实现 count hook。
- [x] 实现 hook 重入保护。
- [x] 实现 hook 中错误传播。
- [x] 实现 tail call 调试信息。
- [x] 实现局部变量可见范围。
- [x] 实现 vararg 调试读取。

## 23. Go 嵌入 API

- [x] 设计 `lua.Options`。
- [x] 设计 `lua.State` 生命周期。
- [x] 设计 `lua.Value` 对外表示。
- [x] 设计 `lua.Function` 回调签名。
- [x] 实现 `NewState`。
- [x] 实现 `Close`。
- [x] 实现 `OpenLibs`。
- [x] 实现 `OpenBase` 等按库加载 API。
- [x] 实现 `DoString`。
- [x] 实现 `DoFile`。
- [x] 实现 `LoadString`。
- [x] 实现 `LoadFile`。
- [x] 实现 `Call`。
- [x] 实现 `ProtectedCall`。
- [x] 实现 `GetGlobal`。
- [x] 实现 `SetGlobal`。
- [x] 实现 `Register`。
- [x] 实现 `Push` 系列 API。
- [x] 实现 `To` 系列类型转换 API。
- [x] 实现错误类型导出。
- [x] 实现 context 取消。
- [x] 实现资源限制配置。
- [x] 编写嵌入 API 示例。

## 24. Go 与 Lua 双向桥接

- [x] 实现 Go 函数注册为 Lua function。
- [x] 实现 Go function 读取 Lua 参数。
- [x] 实现 Go function 压入 Lua 返回值。
- [x] 实现 Go error 映射 Lua error。
- [x] 实现 Go panic recover。
- [x] 实现 Lua 函数保存为 Go callable。
- [x] 实现 Go 调 Lua 全局函数。
- [x] 实现 Go 调 Lua table method。
- [x] 实现 Go -> Lua -> Go 嵌套回调。
- [x] 实现 Lua -> Go -> Lua 嵌套回调。
- [x] 设计跨边界 yield 支持范围。
- [x] 实现 Go struct 显式绑定。
- [x] 实现 Go object userdata 代理。
- [x] 实现 method metatable `__index` 转发。
- [x] 实现 property get/set 策略。
- [x] 实现 Lua stub 生成。
- [x] 实现 Go API 到 Lua module 注册。
- [x] 编写 bridge 兼容测试。
- [x] 编写 bridge 文档示例。

## 25. CLI 与 REPL

- [x] 实现 `cmd/glua/main.go` 极薄入口。
- [x] 实现 CLI 参数解析。
- [x] 支持 `-e stat`。
- [x] 支持 `-l mod`。
- [x] 支持 `-i`。
- [x] 支持 `-v`。
- [x] 支持 `--`。
- [x] 支持脚本文件路径。
- [x] 支持脚本参数 `arg`。
- [x] 支持 stdin 执行。
- [x] 支持 REPL 单行输入。
- [x] 支持 REPL 多行补全。
- [x] 支持 REPL 错误恢复。
- [x] 支持 stdout/stderr 分离。
- [x] 对齐 Lua CLI 退出码。
- [x] 对齐 Lua CLI 版本输出。
- [x] 增加 `glua` 构建脚本。
- [x] 增加跨平台 release 脚本。

## 26. Luac 与开发调试工具

- [x] 实现可选 `glua -l` 行为与官方参数冲突检查。
- [x] 设计独立 `gluac` 或 `glua-bytecode` 工具。
- [x] 支持源码编译为 binary chunk。
- [x] 支持 binary chunk 反汇编。
- [x] 支持 Proto debug dump。
- [x] 支持 opcode trace 模式。
- [x] 支持 VM step trace 模式。
- [x] 支持测试失败时输出最小反汇编。

## 27. 验收门禁

- [x] `gofmt` 全量通过。
- [x] `go test ./...` 全量通过。
- [x] 官方 Lua 5.3 测试套件通过。
  - 拆分验收路径：官方套件不再按一个粗粒度断点推进；后续每轮优先选择一个可单独验证的小段，修复后用对应官方脚本或最小 Lua 片段回归。
  - [x] 官方 `gc.lua` 单文件通过。
  - [x] 官方 `api.lua` 在无 `testC` 环境下通过。
  - [x] 官方 `attrib.lua` 单文件通过。
  - [x] 官方 `locals.lua` 单文件通过。
  - [x] 官方 `constructs.lua` 全量 level=4 模式通过。
  - [x] 官方 `strings.lua` 单文件通过。
  - [x] 官方 `bitwise.lua` 单文件通过。
  - [x] 官方 `bwcoercion.lua` 单文件零退出通过。
  - [x] 官方 `math.lua` 单文件通过。
  - [x] 官方 `db.lua`：补齐 `crl` hook 中 call/return 事件触发与 hook 中 `debug.getinfo(2, "f")` 元数据。
  - [x] 官方 `db.lua`：补齐跨层 `debug.setlocal/getlocal` 对外层活动 local、temporary local 与 non-registered local 的读写。
  - [x] 官方 `db.lua`：补齐 `debug.setupvalue/getupvalue` 对共享 upvalue cell 的读写，并让 `string.gmatch` Go iterator 暴露匿名 debug upvalue 名称。
  - [x] 官方 `db.lua`：补齐 count hook numeric for 计数、`debug.sethook` 浮点整数 count、traceback level=0/非字符串 message/动态 `pcall` 名称、Go/C 函数 `debug.getinfo(..., "u")` vararg 语义，以及 `debug.traceback(thread, nil, level)` 的 thread 重载解析。
  - [x] 官方 `db.lua`：补齐 coroutine 挂起栈 traceback、`debug.sethook/gethook(thread, ...)` 状态隔离、thread hook 派发，以及 `debug.getinfo(thread, level, "lfLS")` 对挂起帧的元信息读取；完整脚本已推进到 `debug.getlocal(co, 1, 1)`。
  - [x] 官方 `db.lua`：补齐 `debug.getlocal/setlocal(thread, level, index, ...)` 对挂起协程寄存器窗口快照的读取与写回，并修复 call/return hook 对 Lua 回调传入 `0` 行号导致 `if l then` 误判的问题；完整脚本已推进到第二次 `coroutine.resume(co)` continuation 断点。
  - [x] 官方 `db.lua`：补齐 Lua coroutine yield 后的真实 continuation 恢复；第二次 `coroutine.resume(co)` 已能继续执行到下一次 `coroutine.yield(debug.getinfo(1, "l").currentline)`，最终返回被 `debug.setlocal(co, 1, 2, "hi")` 修改后的 local，并避免嵌套 Lua 调用外层 continuation 覆盖内层真实挂起点。
  - [x] 官方 `db.lua`：补齐递归协程 traceback 帧数与边界裁剪；递归 `f` 多次 yield 时已能保留外层调用帧，dead-with-error coroutine 也会保存错误现场，并裁掉 `pcall(coroutine.resume, co)` 混入的主线程边界。
  - [x] 官方 `db.lua`：补齐 continuation 恢复后递归协程 traceback 的函数名 identity；`GETUPVAL` 调用点已能恢复递归 `f` 名称，同时命名 Lua traceback 帧保留 source，避免前序挂起协程帧丢失 `db.lua` 匹配。
  - [x] 官方 `db.lua`：补齐 tagmethod 小节 Lua closure 元方法调用；`__index`、算术、拼接、长度、一元、位运算与比较元方法已可执行 Lua closure，并在元方法内部暴露 `debug.getinfo(1).namewhat == "metamethod"` 和对应元方法名。
  - [x] 官方 `db.lua`：补齐泛型 for 迭代器调试名称；迭代器函数内部 `debug.getinfo(1).name` 已按 Lua 5.3 返回固定 `"for iterator"`。
  - [x] 官方 `db.lua`：补齐 finalizer 小节 table `__gc` 自动触发与调用方 debug 名称；finalizer 内 `debug.getinfo(2, "n")` 已能观察到 `namewhat == "metamethod"` 与 `name == "__gc"`。
  - [x] 官方 `db.lua`：补齐 `testing traceback sizes` 深栈 traceback 折叠与当前协程 traceback 边界裁剪；递归协程中 `debug.traceback("message", level)` 已按前 10 行、后 11 行与 `...` 规则压缩。
  - [x] 官方 `db.lua`：补齐 stripped chunk 调试信息剥离与 binary/source dump 闭包捕获语义；`string.dump(fn, true)` 已清空 local/upvalue 名称、source、行号与 activelines，完整 `db.lua` 已输出 `OK`。
  - [x] 官方 `big.lua`：补齐遍历期间删除当前 key 的 `next` 继续迭代、Lua closure `__index/__newindex` 在协程中 yield 后的链式 continuation 恢复、base `pcall/xpcall` 内 Lua 元方法 runner，以及大常量 `LOADK/LOADKX` 全局名错误文本；大表断言与元方法错误断言已通过，脚本推进到末尾 `coroutine.yield'b'` 边界。
  - [x] 官方 `big.lua`：修复 `coroutine.wrap(assert(loadfile("big.lua")))` 执行到文件末尾时仍报 `main thread cannot yield` 的嵌套协程父运行态恢复问题，使 `all.lua` 的 `big.lua` 小节通过。
  - [x] 官方 `main.lua`：补齐 CLI standalone 错误对象 `__tostring` 中 `debug.getinfo(4).currentline` 的错误处理帧层级；`main.lua` 单文件已通过并输出 `OK`。
  - [x] 官方 `all.lua -> gc.lua`：修复顺序执行 `main.lua` 后进入 `gc.lua` 的 `self-referenced threads` 附近报 `C stack overflow`，保持 `gc.lua` 单文件与 all.lua 顺序执行一致。
  - [x] 官方 `all.lua -> db.lua`：修复顺序执行进入 `db.lua` 后的 dump/load 调试名断点；补齐 binary chunk 读回后 locvar 寄存器重建对 `local function` 与 `local f = function()` 闭包的 `CLOSURE A` 反推，并修复 return hook 场景下 hook 包装帧过滤与 pcall 内 tail-call debug 标记。`all.lua` 已顺序通过 `main.lua`、`gc.lua`、`db.lua`，`db.lua` 输出 `OK`。
  - [x] 官方 `all.lua -> calls.lua`：已从第 21 行 `assert(not pcall(type))` 推进并通过完整 `calls.lua` 小节；补齐 `type()` 缺参错误、`print` 走全局 `tostring`、method tail call、Lua comparator `table.sort`、`load(reader/mode)`、dump/load upvalue 可变 cell、长 upvalue 求和寄存器复用、`string.pack` 的 `n` 格式、binary chunk header 严格校验，以及截断 binary chunk 的 `truncated` 错误文本。`all.lua` 已顺序通过 `main.lua`、`gc.lua`、`db.lua`、`calls.lua`，当前推进到下一段 `olddofile('strings.lua')`。
  - [x] 官方 `all.lua -> strings.lua`：已补齐 Open 注册的全局 `dofile` 文件 chunk 执行链路，并让 base 内部执行器支持调用 `string.gmatch` 返回的 `GoClosureWithUpvalues` iterator；`all.lua` 已顺序通过 `main.lua`、`gc.lua`、`db.lua`、`calls.lua` 与 `strings.lua`，`strings.lua` 输出 `OK`。
  - [x] 官方 `all.lua -> literals.lua`：补齐 return 列表跨行表达式 debug 行号、长字符串 CR/CRLF/LFCR 归一化、非法 escape 的 `near` 错误片段，以及文件加载首行 `#` shebang 与 `load(string)` 普通 `#` 的上下文差异；`literals.lua` 单文件已输出 `OK`，`all.lua` 已顺序通过 `strings.lua` 与 `literals.lua` 并推进到 `tpack.lua`。
  - [x] 官方 `all.lua -> tpack.lua`：补齐超大十六进制整数字面量低 64 位回绕、`string.pack/unpack/packsize` 的 `!n` 对齐、`Xop` 空对齐项、1..16 字节整数与符号扩展、packsize 尺寸溢出、`cN` 超长错误、`unpack` 负起始位置和 `pairs/ipairs` 缺参错误文本；`tpack.lua` 单文件已输出 `OK`，`all.lua` 已顺序通过 `tpack.lua` 并推进到 `nextvar.lua`。
  - [x] 官方 `all.lua -> nextvar.lua`：补齐 `table.remove` 默认位置 0 与 `#t+1` 边界、`table.insert/remove/concat/unpack/sort` 对 `__len`/`__index`/`__newindex` 的访问、integer numeric for 对 `math.maxinteger`、float/string limit、`math.huge` 的 Lua 5.3 折算语义，以及 `pairs/__pairs`、`ipairs/__index` 的 Lua closure 元方法通道；`nextvar.lua` 单文件已输出 `OK`，`all.lua` 已顺序通过 `nextvar.lua` 并推进到 `pm.lua`。
  - [x] 官方 `all.lua -> pm.lua`：补齐 raw high byte 字符串字面量保留、字符集 `]`/转义字面量解析、位置捕获、嵌套 capture 返回顺序、`%1..%9` back reference、gsub 无显式 capture 的 `%1` 折算、Lua 5.3.3 空匹配推进规则、同字符 `%b''` balanced match、gsub 非法 replacement value/capture index 错误文本、超长 pattern 的 `too complex` 快速失败、`^a*.?$`/`^a*.?b$`/`^a-.?$` 大字符串锚定 pattern 线性快路径、table replacement 的 false 保留原文、位置捕获数字 key、`__index` 普通读取，以及 frontier 边界 NUL 语义；`pm.lua` 单文件已输出 `OK`，`all.lua` 已顺序穿过该阶段并继续进入后续脚本。
  - [x] 官方 `all.lua -> utf8.lua`：补齐 `utf8.offset` 正向查找尾后哨兵位置 `#s+1` 与空字符串 `utf8.offset("", 0) == 1` 的 Lua 5.3 语义；`utf8.lua` 单文件已输出 `ok`。
  - [x] 官方 `all.lua -> events.lua`：补齐 `__call` 元方法插入 self 时的寄存器窗口扩展、number/boolean/nil/string 基础类型级 raw 元表、基础类型 `__index` 与 `__len` 元方法通道；`events.lua` 单文件已输出 `OK`。
  - [x] 官方 `all.lua -> vararg.lua`：补齐 `table.unpack(list, i, nil)` 与显式 nil 起止边界按默认值折算的 Lua 5.3 语义；`vararg.lua` 单文件已输出 `OK`。
  - [x] 官方 `all.lua -> closure.lua`：补齐 numeric for 与 repeat-until 循环体 local/upvalue 每轮闭合、goto 跳出 local 作用域时关闭 upvalue、自动 GC 弱表清理、Lua closure 相等和 debug.upvalueid Go closure 元数据；`closure.lua` 单文件已输出 `OK`。
  - [x] 官方 `all.lua -> coroutine.lua` 前半段：补齐 `coroutine.isyieldable` 注册与主线程/协程/Go 回调边界判断，修复 tail-call `coroutine.yield` 恢复时误用内层 VM local 快照污染外层 numeric-for 寄存器，截断 nested `coroutine.wrap` continuation 的 Go 边界外父协程帧，并让 `table.sort` comparator 内 yield 按不可跨 Go/C 边界错误处理；`coroutine.lua` 已推进到 `xpcall(pcall, ...)` 跨 yield protected-call continuation 断点。
  - [x] 官方 `all.lua -> coroutine.lua`：补齐 `pcall/xpcall` 跨 `coroutine.yield` 的 protected-call continuation，使 yield 恢复后仍回到内层 pcall 并捕获最终 Lua error object；同步修复 `string.gsub` Lua replacement 不可跨 Go/C 边界 yield，以及 `coroutine.resume` 失败时保留原始 Lua error object。`coroutine.lua` 已推进到递归 Lua 调用 `all({}, 5, 4)` 跨 yield 只恢复一层调用栈的断点。
  - [x] 官方 `all.lua -> coroutine.lua`：补齐递归 Lua 调用跨 `coroutine.yield` 的完整调用栈 continuation，使 `for t in coroutine.wrap(function () all({}, 5, 4) end)` 产出 `5^4` 个组合而不是只恢复最外层循环；同时让挂起寄存器快照仅在 `debug.setlocal(thread, ...)` 修改后回灌，避免普通 resume 覆盖父级 numeric-for 控制寄存器。`coroutine.lua` 已推进到 `access to locals of collected corroutines` 弱表清理断点。
  - [x] 官方 `all.lua -> coroutine.lua`：修复前置 coroutine continuation/线程状态影响弱表清理的问题，使 `x=nil; collectgarbage(); assert(C[1] == nil)` 在完整前缀执行后通过；同步修复 `coroutine.wrap` 返回的 Go closure identity，避免不同 wrap 因共享 Go 函数代码指针被 raw equality/table key 误判为同一函数，并让 GC 活动寄存器扫描跳过 `(*temporary)` 历史临时槽。
  - [x] 官方 `all.lua -> coroutine.lua`：修复 `other old bug when attempting to resume itself` 小节中 `coroutine.wrap(function() return pcall(A, 1) end)` 的 normal/non-suspended 状态与错误文本断点；同时修复父协程调用子 `coroutine.wrap` 期间 `coroutine.status(parent)` 应显示 `normal` 的语义，当前 `coroutine.lua` 已推进到 `bug (stack overflow)` 超大 `table.unpack` 返回量断点。
  - [x] 官方 `all.lua -> coroutine.lua`：修复 `bug (stack overflow)` 小节中协程返回 `table.unpack(t)` 超大结果时仍成功的问题；`table.unpack` 会在展开近 `MaxStackDepth` 数量级结果前按 Lua 栈预算返回 `stack overflow`，`coroutine.resume(co)` 已返回失败。
  - [x] 官方 `all.lua -> coroutine.lua`：修复 `testing yields inside metamethods` 小节中比较、算术、位运算等元方法 yield 后的指令级 continuation 恢复；比较元方法会恢复 OP_EQ/OP_LT/OP_LE 的 skipNext 语义，运算型元方法会把返回值写回 A 寄存器，`coroutine.lua` 单文件已顺序通过并进入后续官方脚本。
  - [x] 官方 `all.lua -> goto.lua`：修复同名 label 在不相交 block 中的可见性解析、跨 block goto 越过外层 local 的校验、repeat-until 尾部条件仍可见 local 的生命周期边界，以及 goto/label/local 相关 parser 错误文本的 Lua 5.3 单引号兼容；`goto.lua` 单文件已输出 `OK`。
  - [x] 官方 `all.lua -> errors.lua` 前置 common errors：修复 `tonumber()` 与 `tostring()` 缺少第一个参数时未按 Lua 5.3 抛出 bad argument 的问题，`errors.lua` 已推进到 better error messages 小节。
  - [x] 官方 `all.lua -> errors.lua`：修复 better error messages 小节中位运算、比较、函数调用、拼接和长度运算的错误文本兼容；已覆盖 `{ } | 1`、`{ } < 1`、`bbbb(3)`、`a.bbbb(3)`、`a=(1)..{}`、`#print`、`#3`，并补齐 upvalue/local/global 操作数来源、短路表达式匿名调用、float->integer 转换和 `% 0` 取模零除文本，当前 `errors.lua` 已推进到 `(io.write or print){}` 标准库 bad argument 名称断点。
  - [x] 官方 `all.lua -> errors.lua`：修复 better error messages 后续标准库/全局函数 bad argument 名称文本；已通过 Go closure identity 从标准库表反查 `io.write`/`table.sort` 等完整名称，并用聚焦脚本确认 `(io.write or print){}`、`(collectgarbage or print){}`、`table.sort({1,2,3}, table.sort)`、`string.gsub('s', 's', setmetatable)` 均包含官方期望函数名。
  - [x] 官方 `all.lua -> errors.lua`：修复完整 `errors.lua` 前置 `checksyntax` 语法错误格式兼容；`load` 失败文本已转换为 `[string "..."]:line: ... near token` 形态，覆盖 `<eof>`、短/长字符串、非法字符和长 source 截断，并修复短路表达式 `(aaa or aaa)` 作为算术/调用临时值时不应泄露 `global 'aaa'` 的错误名称。当前 `errors.lua` 已推进到 float->integer conversions 小节的 `string.sub('a', 2.0^100)` 参数错误文本断点。
  - [x] 官方 `all.lua -> errors.lua`：修复 float->integer conversions 小节中标准库整数参数错误文本；`string.sub('a', 2.0^100)` 与 `string.rep('a', 3.3)` 已在 number 无法整数化时返回包含 `has no integer representation` 的错误文本，并补齐 `debug.setuservalue(debug.upvalueid(...), {})` 对当前 light userdata surrogate 的 `light userdata` 错误文本。当前 `errors.lua` 已推进到 named objects `__name` 小节。
  - [x] 官方 `all.lua -> errors.lua`：修复 named objects `__name` 小节中类型名传播；`math.sin(io.input())`、`io.input(XX)`、`XX + 1`、`~io.stdin`、`XX < XX`、`{} < XX` 与 `XX < io.stdin` 已分别使用 `FILE*`、`My Type` 等元表类型名，匹配官方 `number expected, got FILE*`、`FILE* expected, got My Type`、`on a My Type value` 等片段。
  - [x] 官方 `all.lua -> errors.lua`：修复 stripped binary chunk 运行期错误位置前缀；`load(string.dump(f, true))` 后触发算术错误时已输出 `?:-1:` 前缀，并在无 local debug 名称时仍保留 `table value` 操作数类型。
  - [x] 官方 `all.lua -> errors.lua`：修复 field accesses after RK limit 小节中的长表达式名称推断；1000 个 `a = xN` 之后执行 `a = bbb + 1`、`local _ENV=_ENV; ... bbb + 1`、`t.bbb + 1` 与 `t:bbb()` 已分别恢复 `global 'bbb'`、`field 'bbb'` 与 `method 'bbb'`。
  - [x] 官方 `all.lua -> errors.lua`：修复 field accesses 后续复杂 table constructor 内短路分支索引接收者名称；`x and aaa[x or y]` 触发非 table 索引时已保留 `global 'aaa'`，同时普通 `(aaa or aaa)` 短路算术仍不泄露变量名。
  - [x] 官方 `all.lua -> errors.lua`：修复 tail-call bad argument 函数名文本；`return math.sin("a")` 现在输出 `bad argument #1 to 'math.sin' (function 'sin')`，同时满足完整全局名与官方短函数名片段 `'sin'` 断言。
  - [x] 官方 `all.lua -> errors.lua`：修复 file userdata 可见元表与 `__gc` 缺参错误文本；`getmetatable(io.stdin).__gc()` 已通过真实 userdata metatable 暴露 `__gc`，缺少 self 时返回包含 `no value` 的 Lua 5.3 兼容 bad argument。
  - [x] 官方 `all.lua -> errors.lua`：修复 string 方法调用 bad self 与隐式 self 参数编号；`a:sub()` 现在包含 `bad self`，`('a'):sub{}` 的 visible 参数编号从隐式 self 后移回官方期望的 `#1`。
  - [x] 官方 `all.lua -> errors.lua`：修复 stack overflow 运行期错误位置文本；Lua 调用深度溢出现在按当前 CALL/TAILCALL 调用点输出 `source:line: stack overflow`，同时保留 Go 侧 `ResourceLimitError` cause，官方脚本已越过 repeated stack overflow 前置断点并进入 line error 小节。
  - [x] 官方 `all.lua -> errors.lua`：修复 line error 小节的运行期错误行号归因；`for k,v in 3 do ... end`、多行算术表达式、方法实参表达式和 `error('a', level)` 已按 Lua 5.3 的 `LineInfo`/调用栈 level 返回官方期望行号，同时协程递归仍保留 `C stack overflow`，主线程 `coroutine.yield()` 返回包含 `outside a coroutine` 的错误文本。
  - [x] 官方 `all.lua -> errors.lua`：修复 stack overflow traceback 行号排序；`xpcall(g, debug.traceback, 1)` 现在通过失败现场快照保留深递归 `y` 帧，traceback 会先重复调用行 `l`，随后出现 `debug.getinfo(x, "l").currentline` 记录的 `l1`，且折叠/恢复后 `i > 15`；同时修复 `xpcall(error, error)` 中 handler 抛 nil 时返回 string 错误对象。
  - [x] 官方 `all.lua -> errors.lua`：修复 error handler 内再次触发栈溢出的错误文本；`xpcall(loop, function(m) ... checkerr("error handling", loop) ... end)` 中 handler 内部 `pcall(loop)` 现在返回包含 `error handling` 的错误对象，同时保持外层 handler 返回值 `15`；并补齐默认栈上限下 `table.unpack({}, 1, i)` 的 `too many results` 文本、`assert(false)` 默认错误位置前缀和 `assert()` 无参 `value expected` 文本。
  - [x] 官方 `all.lua -> errors.lua`：修复 `testing tokens in error messages` 小节的语法错误 token 格式；`load` 现在能对普通 token、短/长字符串、`<<`/`>>`、控制字符 `a\1a = 1` 与首字节 `\255a = 1` 输出官方期望的 near 片段，脚本已越过该小节并打印 `+`。
  - [x] 官方 `all.lua -> errors.lua`：修复 syntax limits 小节的过深递归与编译限制错误文本；`load` 已对 201 层 parser 结构返回 `too many C levels`，对超长调用实参返回 `too many registers`，对超量 upvalue/local 分别返回包含 `line 5`/`line 2` 的 `too many upvalues`/`too many local variables`，`errors.lua` 单文件已输出 `OK`。
  - [x] 官方测试套件剩余脚本逐个运行、记录断点并拆分为同级小项；已确认 `errors.lua` 后剩余脚本为 `math.lua`、`sort.lua`、`bitwise.lua`、`verybig.lua`、`files.lua`，其中 `files.lua` 当前受宿主权限策略阻断。
  - [x] 官方 `all.lua -> math.lua`：修复 `math.huge << 1` 的 float-to-integer 错误文本来源名格式，运行期错误重写现在保留 `field 'huge'`，`math.lua` 单文件已输出 `OK`。
  - [x] 官方 `all.lua -> bitwise.lua/verybig.lua`：确认 `bitwise.lua` 与 `verybig.lua` 单文件分别输出 `OK`，当前无需新增兼容修复。
  - [x] 官方 `all.lua -> sort.lua` unpack 小节：修复 `table.unpack` 极端范围在 `math.mininteger..math.maxinteger` 上的返回数量溢出和 `maxinteger` 终点索引回绕问题；`sort.lua` 已越过 unpack 并进入 pack 后续小节。
  - [x] 官方 `all.lua -> sort.lua` pack 后续小节：修复 `table.move` 极端边界的 `too many elements to move`、`destination wrap around`、`maxinteger/mininteger` 端点移动和元方法中断顺序；修复 `table.sort` 负长度虚拟序列、`array too big`、显式 nil comparator、非法 comparator 检测、50k 元素性能路径和默认 `__lt` 元方法排序，`sort.lua` 单文件已输出 `OK`。
  - [x] 官方 `all.lua -> files.lua` 宿主访问与 I/O 前段：为 State 增加显式宿主文件系统、环境变量与进程权限，CLI 普通模式开启官方测试所需权限且 `-E` 保持环境屏蔽；修复 `io.input/io.output` 标准流 userdata identity、标准流 close falsy、`io.open` 失败三返回与 mode 顺序校验、`file:seek` 普通失败返回、`io.write` 返回当前输出 file、`os.rename` 普通失败返回、`file:read("n")` 数字扫描和 local 赋值尾部 method call 多返回展开；`files.lua` 已越过 `testing i/o` 前段并打印 `+`。
  - [x] 官方 `all.lua -> files.lua` coroutine/dofile 与 I/O 中段：修复 `coroutine.wrap(dofile)` 中 Lua chunk yield 不能跨 Go 回调的问题；补齐 `file:read("n")` 数字终止、超长数字失败保留剩余行、`L/*L`、EOF `read(0)`、`io.lines(path)` EOF 自动关闭、写-only `file:read/file:write` 普通失败、file `__tostring`、显式重复 close 错误、`file:lines/io.lines` 多格式读取与 250 参数上限、默认输入输出关闭错误文本、`seek("cur")` 行缓冲位置修正、generic for 迭代表达式调用展开，以及 UTF-8 BOM/shebang 文件加载兼容；`files.lua` 已推进到 binary chunk `loadfile` 小节。
  - [x] 官方 `all.lua -> files.lua` binary `loadfile`、格式别名与缓冲小节：补齐 `loadfile` 对 binary chunk、首行注释后 binary chunk、mode/env 参数的处理；补齐 `file:read("all")` 兼容别名；实现 `file:setvbuf("full"/"line"/"no")` 的最小写缓冲语义，`files.lua` 已越过 buffer 小节。
  - [x] 官方 `all.lua -> files.lua` popen/pclose、execute 与 date/time 小节：补齐 `io.popen(...):close()` 与 `os.execute(...)` 的 Lua 5.3 `ok, what, code` 三元组，区分普通退出和信号终止；同步修复外层 `/bin/sh` 对嵌套 shell 信号退出的状态折算、`os.date` 的 `%w/%j/%x/%y` 与 E/O 修饰符、非法转换错误、超大时间戳不可表示错误，以及 `os.time(table)` 对越界秒数字段的归一化回写。`files.lua` 单文件已输出 `Lua 5.3` 并完成。
  - [x] 官方 `all.lua -> db.lua` debug 名称小节：修复 `if ... then break end; f()`、`if/else ...; f()`、`while ... break ...; f()` 与嵌套 `repeat` 分支后的 local 函数调用名推断，避免 close-only/控制流 JMP 被误判为短路表达式末端；`db.lua` 单文件已输出 `OK`，`all.lua` 顺序回归已越过 `db.lua` 并推进到 `constructs.lua`。
  - [x] 官方 `all.lua -> code.lua` if-goto 连续 label 小节：修复函数表达式内 `if/elseif/else` 子 block 的 goto 跳到外层连续 `::l1:: ::l2:: ::l3::` 时被 codegen 误报 `undefined label` 的问题；缺失 ScopeInfo 时仅在同名 label 唯一时回填，`code.lua` 单文件已零退出，`_soft=true all.lua` 已越过 `code.lua`。
  - [x] 官方 `all.lua -> constructs.lua` full level=4 顺序性能卡点：完整 `all.lua` 已越过 `db.lua`，且 full level=4 模式已通过 `constructs.lua` 并输出 `OK`；保留该项作为性能卡点回归记录。
  - [x] 官方 `_soft=true all.lua -> closure.lua` 顺序长耗时断点：软模式已顺序越过 `code.lua`、`nextvar.lua`、`pm.lua`、`utf8.lua`、`api.lua`、`events.lua`、`vararg.lua` 与 `closure.lua`；本轮修复同作用域重名 local 调试生命周期，以及 binary chunk 读回后共享 StartPC local 误用后续 `CLOSURE A` 的寄存器重建问题。
  - [x] 官方 `_soft=true all.lua -> coroutine.lua:238` 顺序断点：软模式已越过 `closure.lua` 并进入 `coroutine.lua`，已修复 binary chunk 读回后初始化表达式 local 寄存器重建错误，`coroutine.lua` 弱表清理断点通过。
  - [x] 官方 `_soft=true all.lua -> errors.lua` 顺序收尾：修复 `events.lua` 留下 nil 空元表后 `aaa.bbb:ddd(9)` 未在 nil 全局索引处报错的问题，并修复默认输出关闭时 `io.write({})` 参数错误优先级；当前 `_soft=true all.lua` 已完整输出 `final OK !!!`。
  - 当前 `gc.lua` 已单文件通过并输出 `OK`，覆盖 `collectgarbage` 选项、`gsub(string.upper)`、多返回赋值、numeric for 初始化、GC step 节奏、pcall 错误帧回收、泛型 for 迭代三元组、`clearing tables`、`weak tables`、ephemeron 弱 key 固定点传播、table `__gc` finalizer 错误顺序、`__gc x weak tables`、`self-referenced threads` 与 closure/upvalue/thread cycle 两轮回收。
  - 当前 `api.lua` 已按无 `testC` 环境通过并输出 `testC not active: skipping API tests`；已修复 RK 常量索引超过 255 的字段访问降级、无返回值 `RETURN` 终止、`package.path/searchers` 类型错误和 `package.loadlib` 无 CGO 跳过分类。
  - 当前 `attrib.lua` 已单文件通过并输出 `OK`；本轮修复了 `package.preload` Lua closure loader、显式 `_ENV` 环境替换、require 语法错误包装、C loader 禁用时的 `package.cpath` 候选错误文本、Go closure raw equality、大整数 table key hash 存储、多重赋值 RHS 先求值、裸 table constructor 不跨行粘连 IIFE、以及 `return upvalue, local` 寄存器覆盖问题。
  - 当前 `locals.lua` 已单文件通过并输出 `OK`；本轮修复了 `repeat-until` 条件可见循环体局部变量、`load(chunk, nil, nil, env)` 显式环境绑定，以及 block 结束时关闭被闭包捕获的 local `_ENV` upvalue。
  - 当前 `constructs.lua` 已在全量 level=4 模式下通过并输出 `OK`；本轮补齐了 table 长度缓存、CONCAT 连续字符串快路径、Lua closure debug 名称正/负缓存与全局表版本失效、RawNext 迭代快照缓存，以及超长控制结构 sBx 范围错误，解决高组合动态 `load` 长耗时和 `control structure too long` 兼容断点。此前已修复 `_soft=true` 软模式断点：`return;` 空返回分号解析、table constructor 末尾函数调用展开、开放 `return` 列表、开放返回寄存器扩展、numeric for 初始表达式读取外层变量、`debug.getinfo(1, "n")` 全局函数名推断，以及函数调用末尾实参多返回值展开。
  - 当前 `db.lua` 已单文件通过并输出 `OK`；此前已修复 `debug.getinfo(function)` 函数值查询、非法 `what` 选项拒绝、Lua/Go closure 的 `what/source/short_src/activelines`、函数 Proto 的 `LineDefined/LastLineDefined/LineInfo` 写入、隐式 `RETURN` 的 `end` 行号、`load(string)` 默认 chunk name、空字符串/首行为空/长路径 source 的 `short_src` 兼容规则、local/field/global/upvalue 调用点的 `debug.getinfo(..., "n")` 名称推断、命名 Lua traceback 帧 source 保留、Lua closure line hook 调用通路、if/repeat/while/numeric for/generic for/单行 for 的 line hook 轨迹、`debug.getlocal/setlocal` 非法 level 语义、函数形参名查询、vararg 负索引读写、hook 回调 `namewhat=hook`、`debug.traceback()` 帧行展示、正索引活动局部变量写回、tail call 调试帧折叠、tail call hook 派发、自尾递归 VM 帧复用、共享 upvalue debug 读写、`string.gmatch` iterator 匿名 upvalue、count hook numeric for 计数、`debug.sethook` 浮点整数 count、traceback level=0/非字符串 message/动态 `pcall` 名称、Go/C 函数 `debug.getinfo(..., "u")` vararg 语义、traceback thread 重载解析、coroutine 挂起栈快照、thread hook 状态隔离、`debug.getinfo(thread, level, "lfLS")` 挂起帧元信息、挂起协程 `debug.getlocal/setlocal(thread, ...)` 寄存器快照读写、非 line hook 的 Lua 回调行号 nil 语义、Lua coroutine yield 后的 continuation 恢复、递归 coroutine traceback 的外层帧恢复、dead error 快照、pcall 边界裁剪、continuation 恢复后的递归 `f` identity、tagmethod Lua closure 元方法调用、泛型 for 迭代器调试名称、table `__gc` finalizer debug 名称、深栈 traceback sizes 折叠、stripped chunk 调试信息剥离、dump/load 后缺失 source 的 `=?` 回填、dump/load 后 stripped Lua closure upvalue `(*no name)` 展示、dump/load 后 locvar 寄存器重建与局部函数调用名反查，以及固定 return 直接从临时返回区返回以避免覆盖被闭包捕获的形参。
  - 当前 `strings.lua` 已单文件通过并输出 `OK`；本轮修复了十六进制 `0x8000000000000000` 整数字面量补码解析、`string.rep` 超大结果提前拒绝、Lua 风格引用值 `tostring`、Lua closure `__tostring` 调用、元表 `__name` 前缀、`string.format` 的 `%q/%c/%x/%X/%o/%u/%a/%A` 兼容语义、format 错误文本、float 负零保留，以及 `table.concat` 在 `maxinteger` 端点的自增溢出问题。
  - 当前 `bitwise.lua` 已单文件通过并输出 `OK`；本轮修复了十六进制浮点字面量省略 `p` 指数的解析、字符串到 integer 的 64 位补码转换、float integer 到 `lua_Integer` 的无符号回绕，以及空开放 vararg table constructor 不能把旧寄存器值写入 SETLIST 的问题。
  - 当前 `bwcoercion.lua` 已单文件零退出通过；本轮补齐了 string 类型级共享元表、`getmetatable("")` 可见性、字符串位运算元方法查找，以及 VM 指令对 Lua closure 元方法的 State runner 调用路径。
  - 当前 `math.lua` 已单文件通过并输出 `OK`；本轮修复了 `string.gsub` Lua closure 替换函数调用、超长十六进制整数字符串 64 位回绕、合法浮点文本范围溢出、浮点 `% ±math.huge` 边界、`math.tointeger` 字符串解析与有符号范围、`math.max/min` 缺参错误文本，以及 `math.random` 超大区间拒绝语义。此前已修复点开头十进制浮点字面量、超大十进制整数字面量回退 float、goto/label codegen 回填、`math.modf(±inf)` 小数部分、integer 精确比较、integer/float 混合相等与有序比较、位运算严格 float-to-integer 转换、以及 `divide by zero`/`math.huge` 位运算错误文本兼容。
- [x] CLI golden 测试通过。
- [x] 标准库 golden 测试通过。
- [x] Debug golden 测试通过。
- [x] Bridge 测试通过。
- [x] Binary chunk roundtrip 通过。
- [x] Fuzz smoke 通过。
- [x] Benchmark 基线记录完成。
- [x] 完成所有开发后，与官方 `lua` 和 `luac` 跑 benchmark 基准测试，并将对比结论输出到文档中。
- [x] 文档更新完成。
- [x] `git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'` 无未 add Go 文件。

## 28. 发布前待确认

- [x] 确认是否承诺官方 Lua 5.3 二进制 chunk 跨平台完全兼容。
- [x] 确认 `io`、`os`、`package` 默认权限策略。
- [x] 确认 C 动态库加载是否支持。
- [x] 补充第三方 C 动态库由宿主自定义 `package.loadlib` 接入的无 CGO 扩展链路测试。
- [x] 明确 no-CGO 规则目标是保持默认跨系统编译简单，并允许宿主或可选扩展接入外部 lib/so/dylib。
- [x] 确认 Go reflection 自动绑定是否进入首版。
- [x] 确认 Lua stub 生成是否满足“Go 实现转为 Lua 代码”的需求。
- [x] 确认首个 release 的已知限制清单。

## 29. 发布增强：VFS、动态库、反射绑定与 Go 封装 API

- [ ] 生成完整验证 TODO list：覆盖 Go 嵌入 API、CLI、VFS、require、动态库 loader、reflection 自动绑定、Go table/object 封装、常量变量注入、跨平台构建、官方兼容回归与发布文档。
- [ ] 生成完整验证 TODO list：为每个验证项明确命令、输入夹具、期望输出、失败诊断口径和是否需要 Linux/macOS/Windows 分平台执行。
- [ ] 生成完整验证 TODO list：补齐 `CGO_ENABLED=0 go test ./...`、全仓 `gopls check`、`./scripts/check-go-gates.sh`、未跟踪 Go 文件检查、官方 Lua 5.3 套件回归和 benchmark 文档回归。
- [x] 设计 Go `fs.FS` 虚拟文件系统接入方案：明确 `lua.Options` API、只读/可写边界、路径清洗、权限策略、错误文本和与宿主文件系统的优先级。
- [x] 实现 Go `fs.FS` 虚拟文件系统接入：支持 `loadfile`、`dofile`、`require` Lua 文件 loader 读取虚拟文件。
- [x] 实现 Go `fs.FS` 虚拟文件系统接入：支持只读 `io.open`、`io.lines`、`file:read`、`file:lines` 的虚拟文件读取路径。
- [x] 为 Go `fs.FS` 虚拟文件系统补测试：覆盖嵌入 FS、子目录模块、路径穿越拒绝、宿主权限关闭、虚拟文件优先级和错误文本。
- [ ] 设计 `require` 动态库 loader 跨平台策略：Linux/macOS 支持 `.so`/`.dylib` 候选，Windows 明确 `.dll` 运行期加载与 `.lib` 链接期/import library 的支持边界。
- [ ] 实现可选动态库 loader 接入点：默认 `CGO_ENABLED=0` 构建不绑定外部动态库，平台相关实现必须通过显式 build tag、插件或宿主适配层启用。
- [ ] 实现 `package.loadlib` 可选动态库 loader：支持按 filename/symbol 返回 Lua 可调用 loader，并保持默认无 loader 时的兼容错误三返回。
- [ ] 实现 `package.searchers` 动态库搜索器可选接入：按 `package.cpath` 展开候选路径，Linux/macOS/Windows 分平台生成诊断文本。
- [ ] 为动态库 loader 补测试：默认无 CGO 构建下确认不启用；宿主覆盖 loader 可执行；平台候选扩展名和错误文本稳定。
- [ ] 生成 Go reflection 自动绑定方案：定义可见性规则、命名规则、tag 规则、方法 receiver 支持、字段读写权限、错误语义和性能边界。
- [ ] 实现 Go reflection 自动扫描函数：支持导出函数自动转 Lua callable，覆盖参数转换、多返回值、error 返回和 panic 恢复。
- [ ] 实现 Go reflection 自动扫描 struct：支持导出字段读写、导出方法调用、指针和值 receiver、嵌入字段和 tag 重命名。
- [ ] 为 Go reflection 自动绑定补测试：覆盖函数、struct 字段、方法、错误返回、panic 恢复、不可导出字段拒绝、nil receiver 和循环引用。
- [ ] 设计 Go 封装方法给 Lua 调用的统一 API：明确注册函数、注册 table、注册 object、注册常量、注册变量和覆盖策略。
- [ ] 实现 Go 函数封装 API：支持直接注册到全局、模块 table、package.loaded 和 package.preload。
- [ ] 实现 Go table 对象封装 API：支持构造 Lua table，注入字段、方法、嵌套 table、metatable 和只读 table。
- [ ] 实现 Go object 方法封装 API：支持 userdata/object proxy、方法冒号调用、字段访问、生命周期关闭和错误传播。
- [ ] 实现常量与变量注入 API：支持 string、bool、integer、number、nil、table、function、userdata，并区分只读常量与可变变量。
- [ ] 为 Go 封装 API 补测试：覆盖 Lua 调 Go 函数、table 方法、object 方法、常量读取、变量更新、模块 require 和错误边界。
- [ ] 更新文档：补齐 VFS、动态库 loader、reflection 自动绑定、Go 封装 API 的使用示例、限制说明和跨平台注意事项。
- [ ] 完成前五项后重新输出发布限制清单，并同步更新 `docs/PLAN.md`、`docs/RELEASE_LIMITS.md`、`README.md` 与 benchmark/验证结论。
