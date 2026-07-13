# Debug 能力设计

本文记录 Lua 5.3 Debug 能力的迁移范围。Debug 能力分为内部调试元信息、公开 `debug` 标准库、hook 事件和 traceback 生成四部分。

## 目标

- 支持 Lua 5.3 `debug` 标准库主要 API。
- 支持 call、return、line、count hook。
- 支持 traceback、当前栈帧、局部变量、upvalue 和 registry 访问。
- 支持 Go 嵌入 API 从错误中读取 traceback。
- 保持 Debug 逻辑不泄露 `runtime` 可变内部结构。

## 源码映射

- `ldebug.c`：栈帧查询、traceback、局部变量和 upvalue 元信息。
- `ldblib.c`：`debug` 标准库导出函数。
- `lstate.c`：CallInfo、栈帧链和 hook 状态。
- `lfunc.c`：closure 与 upvalue 访问。
- `lobject.c`：值展示与错误对象。

## 核心类型草案

```go
type HookEvent uint8

const (
    HookCall HookEvent = iota
    HookReturn
    HookLine
    HookCount
)

type FrameInfo struct {
    Name       string
    Source     string
    CurrentLine int
    What       string
}

type Hook func(context.Context, *FrameInfo) error
```

内部 runtime 保存更完整的帧结构；公开 API 只返回快照，避免宿主直接修改 VM 调用栈。

## Hook 语义

- call 事件在 Lua 函数和 Go 函数进入时触发。
- return 事件在函数正常返回和错误展开前触发。
- line 事件按 Proto line info 变化触发。
- count 事件按指令计数触发。
- hook 内错误必须转换为 Lua runtime error，并参与 traceback。
- hook 不能破坏当前栈帧寄存器窗口。

## Traceback

traceback 需要包含：

- 错误对象展示。
- chunk 名称。
- 当前行号。
- 函数名或可推断调用点。
- Lua 帧和 Go 回调帧。
- 尾调用压缩标记。

首版可以不逐字符匹配官方 Lua，但必须稳定、可测试，并保留足够上下文。

## debug 标准库范围

首批迁移目标：

- `debug.traceback`
- `debug.getinfo`
- `debug.getlocal`
- `debug.setlocal`
- `debug.getupvalue`
- `debug.setupvalue`
- `debug.upvalueid`
- `debug.upvaluejoin`
- `debug.sethook`
- `debug.gethook`
- `debug.getregistry`
- `debug.getmetatable`
- `debug.setmetatable`

暂不支持项必须返回明确错误，不允许静默返回错误数据。

## 测试策略

- 使用手写 Proto 验证 line info 和 local var info。
- 使用嵌套 Lua 调用验证 traceback 帧顺序。
- 使用 Lua 调 Go、Go 回调 Lua 验证混合栈。
- 使用 hook 计数验证 VM 检查点。
- 使用错误 hook 验证错误传播边界。

## DAP 调试器现状

`runtime/dap` 可由嵌入方复用，CLI 通过 `--glua-dap-listen=host:port` 显式启用。它支持断点、继续执行、调用栈快照和三类单步：`stepIn` 在下一条不同源码行暂停，允许进入被调函数；`next` 只在当前或更浅调用深度的下一条不同源码行暂停；`stepOut` 在离开当前调用深度后暂停。

DAP `threads` 会显示主线程与当前 State 已创建的 Lua coroutine，并在断点事件中携带实际线程 ID。`evaluate` 支持暂停态的 Locals、Upvalues、Globals 及点号 table 路径，例如 `config.timeout`；它不会编译或执行任意 Lua 表达式，因此 Hover、Watch 和 Debug Console 不会产生副作用。

变量面板当前提供当前帧的 Locals、Upvalues、运行时 Globals、table 展开与变量写入。Lua closure 存在共享 upvalue cell 时，编辑 Upvalue 会写回该 cell 并影响共享它的 closure；普通命令行执行不会启动 DAP listener。
