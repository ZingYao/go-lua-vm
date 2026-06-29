# go-lua-vm Lua 5.3 迁移方案

## 1. 目标与边界

本项目目标是使用 Go 最新稳定版初始化并实现一个完整的 Lua 5.3 虚拟机，迁移 Lua 5.3 官方 C 源码的行为语义，覆盖词法、语法、字节码、运行时、标准库、Debug 能力、CLI 可执行程序与 Go 扩展调用能力。

当前基线日期为 2026-06-26，Go 官方版本接口与下载页显示最新稳定版本为 `go1.26.4`，发布于 2026-05-29。本项目初始化阶段应使用：

- `go 1.26`
- `toolchain go1.26.4`
- 本机 Go SDK 路径参考：`/Users/zing/sdk/go/go1.26.4`
- 默认模块路径待确认，临时建议为 `github.com/<owner>/go-lua-vm`

执行测试与构建时应优先使用 `go.mod` 的 `toolchain` 机制解析版本，不通过临时设置 `GOROOT`、`GOPATH`、`GOTOOLCHAIN` 或在项目脚本中硬编码绝对路径切换 Go 版本。若本机 PATH 未指向可用 Go，应先修正开发环境配置，再执行项目命令。

项目核心与默认构建必须使用纯 Go 实现，禁止在核心代码中引入 CGO。任何默认构建参与的 Go 文件不得出现 `import "C"`，所有构建、测试与基准测试默认使用 `CGO_ENABLED=0`。该规则的目标是避免跨系统编译困难，而不是禁止宿主程序或可选扩展调用外部 `lib`、`.so`、`.dylib`。允许在默认构建保持简单跨平台的前提下，通过显式可选 loader、宿主注册或平台适配层接入外部动态库；默认构建不得要求额外 C 头文件、预装动态库、Lua C API 开发包或系统链接依赖。普通 Lua C 模块直接 `require` 需要 `lua_State*` 和 Lua C ABI 兼容层，首版不把该能力纳入默认纯 Go 构建承诺。允许在 `third_party/lua-5.3.6/` 保存 Lua 5.3.6 官方源码作为对照迁移参考，但该源码不得进入 Go 构建链路。

Lua 迁移基线建议锁定为 Lua 5.3 最新补丁版本 `lua-5.3.6`，后续所有行为测试都以该版本官方源码与手册为准。用户原文中的 “Cpp 源码” 按 Lua 5.3 官方 C 源码理解；若另有 C++ 分支或二次开发源码，需要在实现前补充来源。

所有 Go 方法、函数、分支逻辑必须提供中文注释。方法或函数注释需要覆盖功能目标、入参约束、出参语义、错误语义和 Lua 5.3 兼容点；`if`、`else`、`switch case/default` 分支必须说明判断目的，分支内提前 `return`、`continue`、`break` 或 `panic` 必须说明退出原因和影响范围。

本阶段只生成方案与 TODO，不进入 VM 实现。

## 2. 交付形态

项目需要同时支持两种交付形态：

- 库模式：其他 Go 程序通过包 API 嵌入 VM，创建状态机、加载脚本、注册 Go 函数、调用 Lua 函数、读取栈与错误信息。
- CLI 模式：编译为类似 `lua` 的可执行程序，支持执行脚本文件、`-e` 执行片段、`-l` 加载库、`-i` 交互模式、`-v` 输出版本。

建议目录结构：

```text
cmd/glua/main.go              # CLI 入口，保持极薄
internal/cli/                 # 参数解析、REPL、脚本入口编排
internal/luac/                # 可选 luac 兼容工具与字节码反汇编
lua/                          # 对外嵌入 API，其他程序主要依赖此包
runtime/                      # VM 状态、栈、值、表、闭包、协程、GC
compiler/lexer/               # 词法分析
compiler/parser/              # 语法分析与 AST
compiler/codegen/             # Lua 5.3 指令生成
bytecode/                     # 指令定义、Proto、二进制 chunk 编解码
stdlib/                       # base/table/string/math/utf8/os/io/package/debug/coroutine
debug/                        # Debug Hook、栈帧、局部变量、upvalue、traceback
bridge/                       # Go <-> Lua 双向回调与类型转换
tests/compat/                 # Lua 官方测试、兼容脚本与 golden 输出
docs/                         # 设计文档、迁移映射、验收说明
```

## 3. 核心迁移范围

必须完整迁移以下 Lua 5.3 核心模块语义：

- 词法与语法：关键字、长字符串、长注释、数字字面量、局部变量作用域、goto/label、函数定义、vararg、表达式优先级。
- 编译器：常量表、upvalue、寄存器分配、跳转回填、闭包原型、行号信息、局部变量调试信息。
- 字节码：Lua 5.3 指令集、操作数编码、RK 常量寻址、二进制 chunk dump/load。
- VM：函数调用、尾调用、栈帧、元方法、算术/位运算、比较、表访问、for 循环、闭包、upvalue open/close、错误与恢复。
- 值系统：nil、boolean、integer、float、string、table、function、userdata/thread 的 Go 表示与 Lua 语义。
- 表：数组/hash 双区行为、长度运算、迭代顺序约束、元表、弱表策略。
- 协程：create/resume/yield/status/running/wrap，支持 Go 调 Lua、Lua 调 Go 过程中的 yield 边界。
- GC：先实现正确的可达性与生命周期管理，再评估是否需要模拟 Lua 5.3 增量 GC 语义；不得暴露悬空引用。
- 标准库：base、coroutine、table、string、utf8、math、io、os、package、debug。
- Debug：`debug.getinfo`、hook、traceback、局部变量、upvalue、registry、metatable 访问等。

## 4. Go 嵌入与双向回调

对外 API 需要稳定、可测试，并避免泄露内部 VM 细节。

建议核心 API：

```go
state := lua.NewState(lua.Options{})
defer state.Close()

state.OpenLibs()
state.Register("goFunc", func(ctx context.Context, l *lua.State) (int, error) {
    return 0, nil
})

err := state.DoString(ctx, `return goFunc()`)
```

Go 调 Lua 需要支持：

- 加载字符串、文件、预编译 chunk。
- 调用全局函数、表字段函数、栈上函数。
- 传入 Go 基础类型、切片、map、结构体包装、userdata。
- 获取返回值并做显式类型转换。
- panic/recover 边界转换为 Lua 错误或 Go error。

Lua 调 Go 需要支持：

- 注册 Go 函数为 Lua callable。
- Go 函数读取 Lua 栈参数、压入返回值。
- Go 函数抛出的 error 映射为 Lua 错误。
- 支持 Go 函数回调 Lua 函数，形成 Go -> Lua -> Go 和 Lua -> Go -> Lua 链路。

“将 Go 实现转为 Lua 代码”建议定义为可选桥接能力：

- 对普通 Go 函数：通过注册表生成 Lua 包装函数，而不是反编译 Go 源码。
- 对 Go 模块与 table：通过显式 `ModuleBinding` / `TableBinding` 构造模块 table、嵌套 table、常量、变量、只读 table、`package.loaded` 和 `package.preload` 入口。
- 对 Go 结构体/对象：生成 Lua table 代理，方法通过 metatable `__index` 转发到 Go，并通过隐藏 userdata 保留 identity 与 State 关闭阶段 finalizer。
- 对导出的 Go API：可生成 Lua stub 代码，供 Lua 侧以模块方式 `require`。

如用户期望真正把 Go 源码翻译为 Lua 源码，需要单独立项；该目标与 VM 迁移是不同编译器问题。

## 5. 兼容性验收

行为验收必须以 Lua 5.3 官方行为为准，至少包含：

- Lua 官方测试套件通过。
- 对每个标准库建立 Go 单测与 Lua golden 脚本。
- `lua` CLI 关键参数行为兼容。
- 二进制 chunk dump/load 与 Lua 5.3 语义对齐；跨架构二进制兼容性需单独确认。
- Debug hook 对 call/return/line/count 事件可用，traceback 与局部变量信息符合预期。
- Go 嵌入模式覆盖 Go 调 Lua、Lua 调 Go、双向嵌套回调、错误传播、context 取消。

性能验收建议分层：

- 正确性优先于性能，第一阶段不追求超过 C Lua。
- VM 指令分发、表访问、字符串驻留、闭包调用建立基准测试。
- 禁止为了微优化破坏可读性与兼容性。

## 6. 异常边界

必须显式处理以下异常：

- 语法错误、加载错误、运行时错误、内存/资源限制错误。
- Go panic 与 Lua error 的边界转换。
- context 取消或超时导致的 VM 中断。
- CLI 文件读取、stdin、stdout、stderr、退出码。
- `os`、`io`、`package` 标准库在不同平台上的权限和路径差异。
- Debug hook 中再次触发错误或重入 VM。

错误模型建议：

- 内部保留 Lua error 对象与 traceback。
- 对外 Go API 返回 `error`，支持 `errors.Is` / `errors.As` 判断语法、运行时、取消、资源限制等分类。
- CLI 将错误打印到 stderr，并按 Lua 兼容语义返回非 0 退出码。

## 7. 回滚与演进策略

这是新仓库，回滚策略以阶段性可运行版本为单位：

- 每个阶段必须保持 `go test ./...` 可运行。
- 每个迁移模块必须有独立测试与 golden 用例。
- 标准库按模块逐个启用，默认未完成模块返回明确错误。
- CLI 初期只开放已稳定能力；实验能力通过 feature flag 或内部包控制。
- 字节码、Debug、FFI/bridge API 一旦公开，需要版本化或保留兼容层。

## 8. 依赖策略

首选 Go 标准库实现核心 VM，避免核心运行时依赖重型第三方库。

允许依赖：

- 测试断言、golden 对比、模糊测试辅助库。
- CLI 辅助库仅在必要时引入；优先使用 `flag`。
- Lua 5.3.6 官方源码可存放在 `third_party/lua-5.3.6/`，仅作为迁移对照与测试参考。

暂不涉及：

- 数据库、Redis、MQ、外部 HTTP 服务。
- go-zero HTTP 服务结构。
- Redis SCAN、Lua 脚本缓存、线上中间件。
- 默认构建中的 CGO、Lua C API 绑定、C 动态库接入核心 VM。

## 9. 实施阶段

### 阶段 0：项目初始化

- 初始化 `go.mod`，写入 Go 1.26 与 toolchain go1.26.4。
- 确认本机 `/Users/zing/sdk/go/go1.26.4` 可用，但项目命令不硬编码该路径。
- 建立项目级 `AGENTS.md`，固化 Go 版本、无 CGO、注释和门禁规则。
- 建立基础 Go 门禁脚本，检查 Go 版本、CGO 禁用、未跟踪 Go 文件与测试。
- 建立基础目录、README、Makefile、CI、lint/test 命令。
- 建立 Lua 5.3 迁移映射文档。

### 阶段 1：最小可运行 VM

- 实现值系统、栈、函数原型、基础指令分发。
- 支持手写 Proto 执行最小表达式。
- 建立 opcode 单测与 golden。

### 阶段 2：编译器与脚本加载

- 实现 lexer、parser、codegen。
- 支持 `DoString`、`DoFile`、语法错误定位。
- 覆盖表达式、语句、函数、闭包、循环、表构造。

### 阶段 3：运行时完整语义

- 补齐元方法、upvalue、tail call、vararg、coroutine、error/recover。
- 支持二进制 chunk dump/load。
- 建立官方测试套件接入。

### 阶段 4：标准库与 Debug

- 按标准库逐个迁移并建立兼容测试。
- 完整实现 Debug API、hook、traceback、局部变量与 upvalue 操作。

### 阶段 5：Go 扩展桥接

- 稳定对外 `lua` 包 API。
- 实现 Go 函数注册、Go table/module 封装、Go 对象代理、Lua stub 生成、双向回调。
- 补齐 context、错误、panic、yield 边界测试。

### 阶段 6：CLI 与发布

- 实现 `glua` 可执行程序。
- 支持 Lua CLI 关键参数和 REPL。
- 建立跨平台 build、版本信息、发布包。

## 10. 待确认问题

以下问题是发布前仍需用户确认的策略项。推荐先按保守首版发布口径确认，后续再按版本演进扩展。

| 待确认项 | 推荐首版口径 | 原因与影响 |
| --- | --- | --- |
| 是否承诺官方 Lua 5.3 二进制 chunk 跨平台完全兼容 | 根据当前测试结果，首版不承诺跨端序、跨字长完全互通；承诺本项目 load/dump roundtrip、当前平台标准 chunk 读写和 Lua 5.3 语义字段兼容 | 现有测试已覆盖 roundtrip、反汇编和官方套件，但未做跨架构矩阵验证 |
| `io`、`os`、`package` 默认权限策略 | Go 嵌入模式默认关闭宿主文件系统、环境变量和进程访问；CLI 普通模式开启，`-E` 继续屏蔽环境变量读取；Go `fs.FS` 虚拟文件系统首版按只读能力承诺，覆盖 `loadfile`、`dofile`、`require` Lua 文件 loader 和只读 `io.open/io.lines` | 代码已实现 `AllowHostFilesystem`、`AllowEnvironment`、`AllowProcess`；VFS 通过 `lua.Options.VirtualFilesystem` 接入，默认 VFS 优先，`PreferHostFilesystem` 可在宿主授权后切换优先级 |
| C 动态库加载是否支持 | 默认构建不绑定外部动态库，首版内置 `package.loadlib`、C searcher 和 C root searcher 不直接打开动态库；但允许嵌入方在自己的程序中通过 CGO、插件、系统动态库机制或后续可选 loader 接入外部 `lib`、`.so`、`.dylib`，并覆盖 `package.loadlib` 或写入 `package.preload/searchers` 接入 | no-CGO 规则的原意是保持默认构建易跨系统编译；只要默认 `CGO_ENABLED=0` 构建和测试不受影响，外部动态库加载可作为宿主或可选扩展能力存在 |
| Go reflection 自动绑定是否进入首版 | 进入首版，但仅作为显式 opt-in 扩展：支持 `ReflectFunction`、`ReflectedFunctions` 和 `ReflectStruct`，覆盖基础参数、多返回值、`error`、panic 恢复、导出字段读写、导出方法、指针和值 receiver、嵌入字段和 `glua` tag 重命名 | 默认 `ValueOf` 仍不隐式展开 struct/map/slice；不做包级自动扫描、循环引用递归代理或任意 table 到 Go 结构深拷贝，权限边界由调用方显式选择 |
| Lua stub 生成是否满足“Go 实现转为 Lua 代码”的需求 | 根据当前 bridge 实现，首版只承诺 Lua stub/代理层生成满足该需求，不承诺 Go 源码到 Lua 源码的语义翻译 | `bridge.GenerateLuaStub` 已实现并有测试；源码翻译属于独立编译器问题 |
| 首个 release 的已知限制清单 | 已知限制包括：跨平台 binary chunk 完全互通未承诺、Go `fs.FS` VFS 仅承诺只读路径、默认内置 C 动态库加载器不提供、reflection 仅支持显式 opt-in 和有限类型、强 DRM/防破解不承诺 | 明确首版能力边界，避免发布说明过度承诺 |
