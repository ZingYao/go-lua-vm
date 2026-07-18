# Go 嵌入 API 设计

本文记录 `lua` 包对外 API 的第一阶段设计。`lua` 包是其他 Go 程序嵌入 VM 的运行时入口；`bridge` 包提供显式 Go/Lua 绑定能力。内部 `runtime`、`bytecode`、`compiler`、`stdlib` 和 `debug` 包不直接承诺兼容性。

## 目标

- 允许宿主程序创建 Lua 5.3 状态机并控制生命周期。
- 允许宿主程序加载字符串、文件和二进制 chunk。
- 允许宿主程序注册 Go 函数、调用 Lua 函数并读取返回值。
- 允许 Lua 调 Go、Go 回调 Lua，并在错误、panic、context 取消时保持边界清晰。
- `CGO_ENABLED=0` 保持纯 Go；`CGO_ENABLED=1` 时 CLI 自动包含 Lua C 原生模块加载能力。

## 稳定包与并发边界

第三方宿主应优先依赖 `lua`、`bridge` 和 `extensions` 包。`internal/*` 无法被外部 module 导入；`runtime`、`bytecode`、`compiler` 和 `stdlib` 虽然可以用于高级集成，但不承诺与 `lua` 包相同的长期 API 稳定性。

单个 `lua.State` 是串行执行单元，不支持多个 goroutine 同时执行 Lua、操作栈、触发 Event callback 或消费异步队列。不同 State 可以由不同 goroutine 独立运行。宿主如果需要共享一个 State，必须在自身执行器或互斥边界中串行调度全部访问。

`CallProgressEventAsync` 和 Lua 的 `setProgressAsync` 不会启动后台 goroutine；它们只把任务写入 State 队列，callback 在后续 VM 安全点或显式 `FlushProgressEvents` 的调用 goroutine 中执行。

## 核心类型草案

```go
type Options struct {
    MaxStackDepth               int
    MaxCallDepth                int
    MaxAllocationBudget         int64
    MaxGluaEventListeners       int
    MaxGluaEventQueuedTasks     int
    MaxGluaEventTasksPerDrain   int
    AllowHostFilesystem         bool
    AllowEnvironment            bool
    AllowProcess                bool
    PackageDynamicLibraryLoader func(filename string, symbol string) (Value, error)
    PackageDynamicLibraryLoaderForState func(state *State) func(filename string, symbol string) (Value, error)
    VirtualFilesystem           fs.FS
    PreferHostFilesystem        bool
}

type State = runtime.State
type Value = runtime.Value

type Function func(args ...Value) ([]Value, error)
type GoFunction func(args ...Value) (Value, error)
type GoResultsFunction func(args ...Value) ([]Value, error)
```

`Options` 使用零值表示默认限制。文件系统、环境变量和进程能力默认全部开放，三个 `Allow*` 字段仅为已有调用方保留源码兼容，规范化时始终设为 `true`。Event 默认最多注册 4096 个监听器、保留 65536 个异步待执行任务，并在单次安全点或显式 flush 中最多处理 4096 个任务；宿主可按业务负载调低或调高三个 `MaxGluaEvent*` 字段。达到 State 预算时公开 Go API 返回可通过 `errors.Is(err, lua.ErrProgressEventLimitExceeded)` 识别的错误；声明 `var limitErr *lua.ProgressEventLimitError` 后还可使用 `errors.As(err, &limitErr)` 读取具体预算类型，剩余队列任务不会被静默丢弃。单监听器设置 `Overflow: "error"` 并达到 `QueueLimit` 时，使用 `errors.Is(err, lua.ErrProgressEventQueueFull)` 和 `errors.As(err, &lua.ProgressEventQueueFullError{})` 读取事件 ID、上限和当前待处理数；它与 State 级队列预算超限是两类不同的背压信号。

完整替换监听器配置使用 `SetProgressEventOptions`。需要把某个字段明确改为 `false`、`0` 或空字符串时，使用 `PatchProgressEventOptions` 与指针字段，避免零值被理解成“未设置”：

```go
import "time"

disabled := false
unlimited := int64(0)
noThrottle := time.Duration(0)
_, err := lua.PatchProgressEventOptions(state, listenerID, lua.ProgressEventOptionsPatch{
    Once:     &disabled,
    MaxCalls: &unlimited,
    Throttle: &noThrottle,
})
```

资源限制、VFS 和动态库 loader 策略在 `runtime`/stdlib 层执行，`lua` 包只负责把宿主配置转换为内部选项。`State` 和 `Value` 当前复用 runtime 的稳定值语义，外部调用方应只依赖 `lua` 包导出的别名、常量、构造函数和后续方法，不直接耦合内部包。

## VFS 与动态库 loader

`VirtualFilesystem` 接收只读 `fs.FS`，覆盖 `loadfile`、`dofile`、`require` Lua 文件 loader、只读 `io.open/io.lines` 和 `file:read/file:lines`。默认读取优先命中 VFS；设置 `PreferHostFilesystem` 后，同名路径优先使用宿主文件系统。

```go
state := lua.NewStateWithOptions(lua.Options{
    VirtualFilesystem: fstest.MapFS{
        "mod.lua": {Data: []byte(`return {name = "vfs"}`)},
    },
})
state.OpenLibs()
```

动态库加载在 `CGO_ENABLED=0` 构建中不启用。需要外部 `.so/.dylib/.dll` 时，宿主可以注入 `PackageDynamicLibraryLoader` / `PackageDynamicLibraryLoaderForState` 或覆盖 Lua 侧 `package.loadlib`；纯 Go 构建不需要 C 头文件、Lua C API 开发包或系统动态库。

```go
var state *lua.State
state = lua.NewStateWithOptions(lua.Options{
    PackageDynamicLibraryLoader: func(filename, symbol string) (lua.Value, error) {
        return bridge.ValueOf(state, runtime.GoResultsFunction(func(args ...lua.Value) ([]lua.Value, error) {
            return []lua.Value{{Kind: lua.KindBoolean, Bool: true}}, nil
        }))
    },
})
state.OpenLibs()
```

CGO 构建用于 Lua 5.3 public C API 原生模块，不再需要自定义 build tag。CLI 在 `CGO_ENABLED=1` 时自动注入仓库内 state-aware native loader；Go 嵌入方如果需要同类能力，必须显式注入 state-aware loader，确保 `luaopen_*` 调用绑定当前 VM state，而不是只做无状态符号解析。

```go
options := lua.DefaultOptions()
options.PackageDynamicLibraryLoaderForState = func(loaderState *lua.State) func(filename, symbol string) (lua.Value, error) {
    return mynative.LoaderForState(loaderState)
}
state := lua.NewStateWithOptions(options)
state.OpenLibs()
```

上例中的 `mynative.LoaderForState` 代表宿主自己的 native loader 适配层。当前仓库内置实现位于 `internal/native`，供本仓库 CLI 和内部验收使用；它不是外部 module 可直接 import 的公开 Go API。外部嵌入方应通过 `lua.Options` 注入自己的 loader，或等待后续公开适配包。

跨平台注意事项：Linux/macOS 运行期候选是 `.so`/`.dylib`，Windows 运行期候选是 `.dll`；`.lib`/import library 属于链接期产物，不作为 `require` 运行期候选。Native 能力只承诺按 Lua 5.3 public C API 编写并导出 `luaopen_*` 的模块，不承诺任意动态库 FFI，也不承诺访问 Lua 内部头文件或 `lua_State` 内部结构的模块兼容。

## 生命周期 API

```go
func NewState() *State
func NewStateWithOptions(options Options) *State
func SetContext(state *State, ctx context.Context) error
```

`NewState` 创建主线程、registry 和全局环境。`NewStateWithOptions` 创建带资源限制的状态机。生命周期关闭当前复用 `state.Close()`，关闭后 runtime 层拒绝继续访问已释放资源。`SetContext` 绑定宿主取消信号，nil context 返回 `ErrNilContext`。

`OpenLibs` 还会注册 `glua.json`、`glua.yaml`、`glua.xml` 和 `glua.toml` 纯 Go 序列化命名空间。Lua table 的数组/对象判定、`glua.null`、XML 映射和错误边界参见 [GLua 序列化](glua-serialization.md)。

`glua.codec`、`glua.hash`、`glua.regex`、`glua.uuid`、`glua.zip`、`glua.path` 和 `glua.schema` 的 API、安全边界及回归方式参见 [GLua 通用扩展](glua-utilities.md)。

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

## Go 封装 API

Go 封装能力位于 `bridge` 包，目标是让宿主以声明式方式把 Go 函数、模块、table、对象、常量和变量暴露给 Lua。默认推荐显式 binding；需要减少样板代码时，可以显式调用 reflection 入口生成 `bridge.Function` 或 `ObjectBinding`。该能力不引入 CGO，也不依赖 Lua C API。

核心入口：

```go
func RegisterFunction(state *lua.State, name string, fn bridge.Function) error
func RegisterModule(state *lua.State, module bridge.ModuleBinding) (lua.Value, error)
func RegisterModulePreload(state *lua.State, module bridge.ModuleBinding) error
func BuildModule(state *lua.State, module bridge.ModuleBinding) (lua.Value, error)
func BuildTable(state *lua.State, table bridge.TableBinding) (lua.Value, error)
func ValueOf(state *lua.State, value any) (lua.Value, error)
func ReflectFunction(fn any) (bridge.Function, error)
func ReflectedFunctions(functions map[string]any) (map[string]bridge.Function, error)
func ReflectStruct(object any) (bridge.ObjectBinding, error)
```

`RegisterFunction` 写入全局环境。`RegisterModule` 构造模块 table，写入全局环境，并在 `package.loaded` 可用时写入同一模块实例。`RegisterModulePreload` 要求已打开 `package` 标准库，只写入 `package.preload`，让 Lua 侧 `require` 延迟加载模块。

`ModuleBinding` 和 `TableBinding` 支持：

- `Fields` / `Variables`：普通可变字段。
- `Constants`：只读字段，Lua 侧写入同名 key 返回错误。
- `Functions`：Go 函数字段。
- `Tables`：嵌套 table。
- `Objects`：显式 object proxy。
- `ReadOnly`：整体只读 table。
- `Metatable`：table 级宿主元表字段。

对象代理通过 `ObjectBinding` 显式声明可见方法、getter、setter 和可选 finalizer。Lua 侧看到的是 table，内部隐藏 userdata 保留 Go identity；`object:method(...)` 会自动剥离 self 并把剩余实参交给 `ObjectContext`。State 关闭时，已注册 userdata 的 finalizer 会统一执行。

reflection 入口的当前边界：

- `ReflectFunction` 支持 bool、整数、浮点、string、`lua.Value`、`*lua.State`、`context.Context` 参数；支持基础类型、`lua.Value`、`*runtime.Table`、`*runtime.Userdata`、bridge/runtime callable 返回；最后一位 `error` 会映射为 Lua error。
- `ReflectedFunctions` 把函数 map 转为 `map[string]bridge.Function`，适合放入 `ModuleBinding.Functions` 或 `TableBinding.Functions`。
- `ReflectStruct` 接收非 nil struct 或 struct 指针，扫描导出字段、导出方法、匿名嵌入 struct 字段和 `glua` tag；生成的 `ObjectBinding` 仍需通过 `BindStruct` 或模块/table binding 暴露给 Lua。
- `ReflectStruct` 只支持可稳定转换的基础字段、`lua.Value`、`*runtime.Table` 和 `*runtime.Userdata`；slice、map、任意 struct 指针字段和循环引用不会被隐式递归代理。

示例：

```go
type Counter struct {
    Value int64
}

counter := &Counter{Value: 1}

_, err := bridge.RegisterModule(state, bridge.ModuleBinding{
    Name: "gomod",
    Constants: map[string]any{
        "version": "1.0.0",
    },
    Variables: map[string]any{
        "enabled": true,
    },
    Functions: map[string]bridge.Function{
        "add": func(ctx *bridge.Context) error {
            left, _ := ctx.ToInteger(1)
            right, _ := ctx.ToInteger(2)
            ctx.PushInteger(left + right)
            return nil
        },
    },
    Objects: map[string]bridge.ObjectBinding{
        "counter": {
            Object: counter,
            Getters: map[string]bridge.PropertyGetter{
                "value": func(object any) (lua.Value, error) {
                    return lua.Value{Kind: lua.KindInteger, Integer: object.(*Counter).Value}, nil
                },
            },
            Methods: map[string]bridge.ObjectMethod{
                "inc": func(ctx *bridge.ObjectContext) error {
                    counter := ctx.Object().(*Counter)
                    counter.Value++
                    ctx.PushInteger(counter.Value)
                    return nil
                },
            },
        },
    },
})
```

Lua 侧：

```lua
local gomod = require("gomod")
print(gomod.add(20, 22))
print(gomod.version)
print(gomod.counter:inc())
```

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

Go 封装 API 不承诺 Go 包级自动扫描、Go 源码到 Lua 源码翻译、默认 C 动态库加载或绕过 raw table 写入的强只读沙箱。reflection 自动绑定是显式 opt-in 能力，只覆盖当前文档列出的类型和错误语义。
