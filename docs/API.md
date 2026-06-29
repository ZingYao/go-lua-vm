# Go 嵌入 API 设计

本文记录 `lua` 包对外 API 的第一阶段设计。`lua` 包是其他 Go 程序嵌入本项目的唯一稳定入口，内部 `runtime`、`bytecode`、`compiler`、`stdlib`、`debug` 和 `bridge` 包不直接承诺兼容性。

## 目标

- 允许宿主程序创建 Lua 5.3 状态机并控制生命周期。
- 允许宿主程序加载字符串、文件和二进制 chunk。
- 允许宿主程序注册 Go 函数、调用 Lua 函数并读取返回值。
- 允许 Lua 调 Go、Go 回调 Lua，并在错误、panic、context 取消时保持边界清晰。
- 保持纯 Go 和无 CGO，不暴露 Lua C API 依赖。

## 核心类型草案

```go
type Options struct {
    MaxStackDepth      int
    MaxCallDepth       int
    MaxAllocationBudget int64
}

type State = runtime.State
type Value = runtime.Value

type Function func(args ...Value) ([]Value, error)
type GoFunction func(args ...Value) (Value, error)
type GoResultsFunction func(args ...Value) ([]Value, error)
```

`Options` 使用零值表示默认限制。所有限制在 `runtime` 层执行，`lua` 包只负责把宿主配置转换为内部选项。`State` 和 `Value` 当前复用 runtime 的稳定值语义，外部调用方应只依赖 `lua` 包导出的别名、常量、构造函数和后续方法，不直接耦合内部包。

## 生命周期 API

```go
func NewState() *State
func NewStateWithOptions(options Options) *State
func SetContext(state *State, ctx context.Context) error
```

`NewState` 创建主线程、registry 和全局环境。`NewStateWithOptions` 创建带资源限制的状态机。生命周期关闭当前复用 `state.Close()`，关闭后 runtime 层拒绝继续访问已释放资源。`SetContext` 绑定宿主取消信号，nil context 返回 `ErrNilContext`。

## 加载与执行 API

```go
func (state *State) DoString(ctx context.Context, source string) error
func (state *State) DoFile(ctx context.Context, path string) error
func (state *State) LoadString(ctx context.Context, source string, chunkName string) error
func (state *State) LoadFile(ctx context.Context, path string) error
func (state *State) LoadBinary(ctx context.Context, chunk []byte, chunkName string) error
func (state *State) PCall(ctx context.Context, nargs int, nresults int) error
```

加载 API 把函数压入 Lua 栈；执行 API 负责保护调用并返回 Go `error`。`context.Context` 取消时 VM 必须在检查点中断并返回可通过 `errors.Is` 判断的取消错误。

## 栈访问 API

```go
func (state *State) Top() int
func (state *State) Pop(n int) error
func (state *State) PushNil()
func (state *State) PushBoolean(value bool)
func (state *State) PushInteger(value int64)
func (state *State) PushNumber(value float64)
func (state *State) PushString(value string)
func (state *State) ToBoolean(index int) (bool, bool)
func (state *State) ToInteger(index int) (int64, bool)
func (state *State) ToNumber(index int) (float64, bool)
func (state *State) ToString(index int) (string, bool)
```

索引规则对齐 Lua 5.3：正数从栈底开始，负数从栈顶开始，pseudo-index 后续用于 registry 和 upvalue。

## 函数注册 API

```go
func (state *State) Register(name string, fn Function) error
func (state *State) SetGlobal(name string) error
func (state *State) GetGlobal(name string) error
```

`Register` 把 Go 函数包装为 Lua callable。第一阶段推荐 `Function` 多返回值签名：入参是 Lua 实参快照，返回值是 Lua 多返回值列表。Go 函数返回 error 时，bridge 层把错误转换为 Lua error。

## 错误边界

对外错误必须支持以下分类：

- 语法错误。
- 运行时错误。
- 加载错误。
- 资源限制错误。
- context 取消或超时。
- Go 回调错误。
- 内部不变量错误。

错误对象需要保留 Lua traceback、chunk 名称和行号信息；CLI 层再决定 stderr 文本和退出码。

## 兼容边界

`lua` 包不承诺与 Lua C API 函数名一一对应。优先提供 Go 语义清晰、可测试的 API；必要时再提供 `auxlib` 风格 helper。
