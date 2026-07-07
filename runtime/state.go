package runtime

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/zing/go-lua-vm/bytecode"
)

const (
	// RegistryPseudoIndex 对齐 Lua 5.3 LUA_REGISTRYINDEX。
	RegistryPseudoIndex int = -DefaultMaxStackDepth - 1000
	// RegistryIndexMainThread 对齐 Lua 5.3 LUA_RIDX_MAINTHREAD。
	RegistryIndexMainThread int64 = 1
	// RegistryIndexGlobals 对齐 Lua 5.3 LUA_RIDX_GLOBALS。
	RegistryIndexGlobals int64 = 2
)

var (
	// ErrClosedState 表示调用方尝试使用已经关闭的 Lua State。
	ErrClosedState = errors.New("lua state is closed")
	// ErrStackUnderflow 表示调用方尝试从空 Lua 栈弹出值。
	ErrStackUnderflow = errors.New("stack underflow")
	// ErrCallFrameUnderflow 表示调用方尝试从空调用帧栈弹出帧。
	ErrCallFrameUnderflow = errors.New("call frame underflow")
	// ErrNilProtectedCall 表示调用方传入了空的保护调用函数。
	ErrNilProtectedCall = errors.New("nil protected call")
	// ErrNilContext 表示调用方尝试把 nil context 绑定到 State。
	ErrNilContext = errors.New("nil context")
	// ErrInterrupted 表示宿主向 VM 请求一次 Lua 级中断。
	ErrInterrupted = errors.New("interrupted!")
)

const (
	// CallFrameKindLua 表示 Lua closure 调用帧。
	CallFrameKindLua CallFrameKind = "lua"
	// CallFrameKindGo 表示 Go closure 调用帧。
	CallFrameKindGo CallFrameKind = "go"
)

// CallFrameKind 表示调用帧来源类型。
//
// 当前只区分 Lua closure 与 Go closure，后续 coroutine 和 tail call 会复用该类型。
type CallFrameKind string

// VarargSnapshot 保存 Lua 调用帧的可变参数快照。
//
// Values 按 Lua vararg 原始顺序保存；该结构通过指针挂到 CallFrame，避免破坏调用帧的可比较性。
type VarargSnapshot struct {
	// Values 保存可变参数值列表，读取方不得原地修改。
	Values []Value
}

// CallFrame 表示 Lua VM 的一个调用帧。
//
// Function 保存被调用函数，Base 保存该帧在 State stack 中的 1-based 起始位置，
// ExpectedReturns 保存调用方期望返回值数量，-1 表示多返回值。Name、NameWhat、CurrentPC、
// TailCall 和 Varargs 保存 Debug 库需要的阶段性元数据，不参与当前最小执行器调度。
type CallFrame struct {
	// Kind 表示调用帧来源类型。
	Kind CallFrameKind
	// Function 保存被调用函数值。
	Function Value
	// Base 保存当前调用帧栈基址，使用 Lua 1-based 栈索引。
	Base int
	// ExpectedReturns 保存调用方期望返回值数量，-1 表示多返回值。
	ExpectedReturns int
	// Name 保存调用点推断出的函数名，空字符串表示当前阶段无法推断。
	Name string
	// NameWhat 保存函数名来源，例如 global、local、field 或空字符串。
	NameWhat string
	// CurrentPC 保存当前帧正在执行或最近执行的 0-based 指令位置。
	CurrentPC int
	// TailCall 表示当前帧是否由尾调用替换产生。
	TailCall bool
	// Varargs 保存当前 Lua 帧的可变参数快照，供 debug.getlocal 负索引读取。
	Varargs *VarargSnapshot
}

// ProtectedCallFunc 表示 protected call 边界内执行的运行时函数。
//
// 入参 state 必须是触发 ProtectedCall 的同一个 State；返回 nil 表示执行成功，返回非 nil
// 错误表示 Lua/Go 边界内发生可恢复错误。函数内部 panic 会被 ProtectedCall 转换为错误。
type ProtectedCallFunc func(state *State) error

// RuntimeError 表示可传播到 Lua 层的运行时错误。
//
// Object 保存 Lua 侧 error object；Cause 保存 Go 侧原始错误，便于调用方继续使用
// errors.Is 和 errors.As 判断错误链。Object 为 nil 时会从 Cause 的错误文本派生 string。
type RuntimeError struct {
	// Object 保存 Lua 侧可见的错误对象。
	Object Value
	// Cause 保存 Go 侧原始错误。
	Cause error
	// TracebackFrames 保存错误发生现场的调用帧快照，顺序为当前帧到最早帧。
	TracebackFrames []CallFrame
}

// NewRuntimeError 创建带 Lua error object 的运行时错误。
//
// object 为 nil 且 cause 非 nil 时，会用 cause.Error() 生成 Lua string 错误对象；object
// 为 nil 且 cause 也为 nil 时，会生成空字符串错误对象，避免错误对象传播时丢失值槽。
func NewRuntimeError(object Value, cause error) *RuntimeError {
	if object.IsNil() && cause != nil {
		// 未显式提供 Lua 错误对象时，使用 Go 错误文本作为 Lua string error object。
		object = StringValue(cause.Error())
	}
	if object.IsNil() {
		// 没有 cause 的错误仍需要可传播对象，使用空字符串保持错误对象槽非 nil。
		object = StringValue("")
	}

	// 返回同时携带 Lua object 与 Go cause 的错误包装。
	return &RuntimeError{Object: object, Cause: cause}
}

// Error 返回运行时错误的文本展示。
//
// Cause 存在时优先返回 Cause 文本，便于日志和测试继续看到原始错误；否则返回 Lua error
// object 的调试展示。
func (runtimeErr *RuntimeError) Error() string {
	if runtimeErr == nil {
		// nil 接收者只用于防御误调用，返回稳定文本避免 panic。
		return "<nil runtime error>"
	}
	if runtimeErr.Cause != nil {
		// 保留 Go 原始错误文本，避免包装后改变 errors.Is 之外的可读输出。
		return runtimeErr.Cause.Error()
	}

	// 无 Go cause 时展示 Lua error object，后续 traceback 会在此基础上扩展。
	return runtimeErr.Object.DebugString()
}

// Unwrap 返回运行时错误的 Go 原始错误。
//
// 返回 nil 表示该 RuntimeError 只有 Lua error object，没有可展开的 Go cause。
func (runtimeErr *RuntimeError) Unwrap() error {
	if runtimeErr == nil {
		// nil 接收者没有可展开的错误链。
		return nil
	}

	// errors.Is 和 errors.As 会沿用该 cause 继续匹配。
	return runtimeErr.Cause
}

// WithTracebackFrames 返回携带失败现场调用帧的运行时错误。
//
// frames 必须按 TracebackFrames 返回的当前帧到最早帧顺序传入；非 RuntimeError 错误会原样返回。
// 已有 traceback 或空 frames 不会覆盖，避免更外层 protected call 污染更内层失败现场。
func WithTracebackFrames(err error, frames []CallFrame) error {
	if err == nil || len(frames) == 0 {
		// 没有错误或没有现场帧时无需修改错误链。
		return err
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr == nil {
		// 非 RuntimeError 不强行包装，避免改变调用方原本错误分类。
		return err
	}
	if len(runtimeErr.TracebackFrames) > 0 {
		// 已保存更内层现场时保持原值。
		return err
	}
	runtimeErr.TracebackFrames = append([]CallFrame(nil), frames...)
	return err
}

// ErrorObject 从错误链中提取 Lua error object。
//
// err 为 nil 时返回 Lua nil；err 链中包含 RuntimeError 时返回其 Object；普通 Go error
// 会转换为 Lua string，保证 protected call 调用方总能取得 Lua 侧错误对象。
func ErrorObject(err error) Value {
	if err == nil {
		// 没有错误时不存在错误对象。
		return NilValue()
	}

	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		// RuntimeError 已经携带 Lua 侧对象，直接返回供 pcall/xpcall 压栈。
		return runtimeErr.Object
	}

	// 普通 Go 错误按 Lua 5.3 常见 error string 语义转换为字符串对象。
	return StringValue(err.Error())
}

// ensureRuntimeError 确保错误链带有 Lua error object。
//
// err 为 nil 时返回 nil；已有 RuntimeError 时保持原错误链；普通 Go error 会被包装为
// RuntimeError，并用错误文本生成 Lua string 对象。
func ensureRuntimeError(err error) error {
	if err == nil {
		// nil 错误不需要包装。
		return nil
	}

	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		// 已经具备 Lua error object 的错误不能重复包装，避免改变错误链结构。
		return err
	}

	// 普通 Go error 包装为 RuntimeError，供 protected call 向 Lua 层传播。
	return NewRuntimeError(StringValue(err.Error()), err)
}

// NewLuaCallFrame 构造 Lua closure 调用帧。
//
// function 必须是 Lua closure 值；base 使用 Lua 1-based 栈索引，expectedReturns 为 -1
// 表示多返回值。该函数只构造帧元数据，不执行函数体。
func NewLuaCallFrame(function Value, base int, expectedReturns int) CallFrame {
	// 构造 Lua 调用帧，后续 VM 执行器会补充 pc、Proto 和寄存器窗口。
	return CallFrame{
		Kind:            CallFrameKindLua,
		Function:        function,
		Base:            base,
		ExpectedReturns: expectedReturns,
	}
}

// NewGoCallFrame 构造 Go closure 调用帧。
//
// function 必须是 Go closure 值；base 使用 Lua 1-based 栈索引，expectedReturns 为 -1
// 表示多返回值。该函数只构造帧元数据，不执行 Go 函数体，也不转换 Go panic。
func NewGoCallFrame(function Value, base int, expectedReturns int) CallFrame {
	// 构造 Go 调用帧，后续 bridge 执行器会补充 Go 函数调用和错误转换。
	return CallFrame{
		Kind:            CallFrameKindGo,
		Function:        function,
		Base:            base,
		ExpectedReturns: expectedReturns,
	}
}

// TableFinalizerRunner 表示 State 执行 table `__gc` 元方法的回调。
//
// tableValue 是待终结 table，finalizerValue 是其当前元表上的 `__gc` 字段。返回 error 时会
// 传播到 collectgarbage 调用方，供 pcall 捕获。
type TableFinalizerRunner func(tableValue Value, finalizerValue Value) error

// LuaThreadRunner 表示 State 执行 Lua closure 协程入口的回调。
//
// thread 是当前被 resume 的协程；args 是本次 resume 实参。返回值会按 coroutine.resume
// 成功路径透传，返回 error 时由 Thread.Resume 收敛为协程错误或 yield 状态。
type LuaThreadRunner func(thread *Thread, args ...Value) ([]Value, error)

// LuaMetamethodRunner 表示 State 执行 Lua closure 元方法的回调。
//
// method 是元表中取得的 Lua closure；name 是 `__add`、`__index` 等元方法名，空字符串表示
// 普通 Lua closure 回调；args 是按 Lua 调用约定排列的实参。返回值保留完整多返回值列表。
type LuaMetamethodRunner func(method Value, name string, args ...Value) ([]Value, error)

// State 表示 Lua 5.3 VM 的运行状态。
//
// 当前阶段只初始化 registry 和全局环境；后续会继续承载栈、调用帧、GC、协程和错误恢复。
type State struct {
	// registry 保存 Lua 5.3 registry table。
	registry *Table
	// globals 保存 registry[LUA_RIDX_GLOBALS] 指向的全局环境表。
	globals *Table
	// stack 保存当前主线程 Lua 栈值。
	stack []Value
	// callFrames 保存当前主线程调用帧栈。
	callFrames []CallFrame
	// pendingErrorTracebackFrames 保存 xpcall 错误处理器可见的失败现场快照。
	pendingErrorTracebackFrames []CallFrame
	// mainThread 保存当前 State 的主协程对象。
	mainThread *Thread
	// runningThread 记录当前正在运行的协程，用于 status 跟踪。
	runningThread *Thread
	// threads 保存当前 State 创建的所有协程对象，用于生命周期与 root 标记扫描。
	threads []*Thread
	// activeVMs 保存当前正在执行的 Lua VM 寄存器窗口，用于 GC 扫描活动 local。
	activeVMs []*VM
	// externalGCRootFrames 保存宿主桥接层显式压入的临时 GC 根帧。
	externalGCRootFrames [][]Value
	// pooledLuaVMCached 按寄存器窗口大小缓存可复用的 Lua VM。
	pooledLuaVMCached map[int][]*VM
	// pooledLuaVMFast 保存最近归还的 VM，命中同窗口热调用时绕过 map 池。
	pooledLuaVMFast *VM
	// callArgumentScratch 保存 Lua closure 调用参数临时区，避免热路径每次 CALL 分配小切片。
	callArgumentScratch []Value
	// userdatas 保存当前 State 注册的 userdata 实例，Close 时触发显式 finalizer。
	userdatas []*Userdata
	// finalizableTables 保存已登记 `__gc` 元方法的 table，完整 GC 时按逆序尝试终结。
	finalizableTables []*Table
	// finalizerInsertIndex 保存上次 finalizer 错误后新登记对象的插入位置，-1 表示正常追加。
	finalizerInsertIndex int
	// finalizedTables 记录已经终结过的 table，避免同一对象重复执行 `__gc`。
	finalizedTables map[*Table]bool
	// deferredThreadFinalizers 记录因 suspended thread 图仍引用而延迟过一轮的 table。
	deferredThreadFinalizers map[*Table]bool
	// coroutineBornFinalizers 记录在非主协程执行期间登记的 table finalizer。
	coroutineBornFinalizers map[*Table]bool
	// tableFinalizerRunner 执行 Lua table `__gc` 元方法，由 lua 包在打开 base 库时注入。
	tableFinalizerRunner TableFinalizerRunner
	// luaThreadRunner 执行 Lua closure 协程入口，由 lua 包注入以避免 runtime 反向依赖。
	luaThreadRunner LuaThreadRunner
	// luaMetamethodRunner 执行 Lua closure 元方法，由 lua 包注入以避免 runtime 反向依赖。
	luaMetamethodRunner LuaMetamethodRunner
	// debugEnvironment 保存 debug 标准库运行环境，由 stdlib/debug 注入以避免每次 VM 调用查 sync.Map。
	debugEnvironment any
	// autoGCAllocations 记录自动 GC 运行态下的分配节拍，用于模拟分配压力触发 finalizer。
	autoGCAllocations int64
	// hasWeakTables 标记当前 State 曾经登记过弱表；为 true 时自动 GC 才需要周期性 weak sweep。
	hasWeakTables bool
	// gcRunning 标记 Lua 视角的自动 GC 是否运行。
	gcRunning bool
	// gcPause 保存 collectgarbage("setpause") 最近配置的暂停比例。
	gcPause int64
	// gcStepMultiplier 保存 collectgarbage("setstepmul") 最近配置的步进倍率。
	gcStepMultiplier int64
	// gcCountMetric 保存 Lua 视角 collectgarbage("count") 的阶段性计数。
	gcCountMetric int64
	// gcStepProgress 保存 Lua 视角 collectgarbage("step") 的当前周期进度。
	gcStepProgress int64
	// gcSuppressStoppedCountOnce 表示 step 完成后下一次 count 不模拟停止状态增长。
	gcSuppressStoppedCountOnce bool
	// goCallbackDepth 记录当前 Go 回调进入层级。
	// 值大于 0 表示当前执行处于 Go 调用边界内，不允许协程在边界中 yield。
	goCallbackDepth int
	// errorHandlerDepth 记录当前是否正在 xpcall 错误处理器内。
	errorHandlerDepth int
	// options 保存 State 的资源限制配置。
	options Options
	// ctx 保存宿主注入的取消信号，供 VM 指令循环和 Go/Lua 回调边界检查。
	ctx context.Context
	// ctxCanCancel 表示 ctx 可能产生取消错误；默认 background 路径可跳过 ctx.Err 热点调用。
	ctxCanCancel bool
	// interruptCount 保存宿主请求的 Lua 级中断次数；每次 CheckContext 消费一次。
	interruptCount atomic.Int64
	// closed 标记 State 是否已经关闭。
	closed bool
}

// NewState 创建一个最小 Lua 运行状态。
//
// 返回的 State 会初始化 registry，并写入 registry[1] 主线程占位与 registry[2] 全局表。
func NewState() *State {
	// 使用默认资源限制创建 State，兼容最简单的调用方式。
	return NewStateWithOptions(Options{})
}

// NewStateWithOptions 使用指定资源限制创建最小 Lua 运行状态。
//
// options 会先被 NormalizeOptions 规范化，再写入 State。
func NewStateWithOptions(options Options) *State {
	// 先创建 State 主体，保持 registry 和 globals 的初始化顺序与关闭路径一致。
	state := &State{
		registry:             NewTable(),
		globals:              NewTable(),
		finalizerInsertIndex: -1,
		finalizedTables:      make(map[*Table]bool),
		gcRunning:            true,
		gcPause:              200,
		gcStepMultiplier:     200,
		options:              NormalizeOptions(options),
		ctx:                  context.Background(),
	}
	state.mainThread = &Thread{
		state:  state,
		isMain: true,
		status: CoroutineStatusRunning,
	}
	state.threads = []*Thread{state.mainThread}
	state.runningThread = state.mainThread
	state.registry.RawSetInteger(RegistryIndexMainThread, ReferenceValue(KindThread, state.mainThread))
	state.registry.RawSetInteger(RegistryIndexGlobals, ReferenceValue(KindTable, state.globals))
	return state
}

// BorrowLuaVMAfterReset 返回一个可复用的 Lua closure VM，优先命中 state 内部缓存。
//
// 缓存条目按寄存器窗口长度命中；无可用缓存时按借用 Proto 数据创建新 VM。返回值始终可直接
// 复用于当前函数调用，调用方无需再执行二次构造。
func (state *State) BorrowLuaVMAfterReset(registerCount int, constants []bytecode.Constant, upvalues []Value, protos []*bytecode.Proto, varargs []Value) *VM {
	return state.BorrowLuaVMAfterResetSkippingClearPrefix(registerCount, constants, upvalues, protos, varargs, 0)
}

// BorrowLuaVMAfterResetSkippingClearPrefix 返回一个可复用的 Lua closure VM，并跳过即将覆盖的参数前缀清零。
//
// skipClearPrefix 必须只覆盖调用入口马上写入的固定参数槽；无可用缓存时仍按普通借用 Proto
// 构造新 VM，新建 VM 本身已是 nil 初始化，不需要额外清零。
func (state *State) BorrowLuaVMAfterResetSkippingClearPrefix(registerCount int, constants []bytecode.Constant, upvalues []Value, protos []*bytecode.Proto, varargs []Value, skipClearPrefix int) *VM {
	if state == nil || state.closed {
		// state 未初始化或关闭时不复用缓存，直接按纯构造语义返回新 VM。
		return NewVMWithBorrowedPrototypeData(registerCount, constants, upvalues, protos, varargs)
	}

	if fastVM := state.pooledLuaVMFast; fastVM != nil && len(fastVM.registers) == registerCount {
		// 最近归还的同窗口 VM 优先复用，避开热路径 map 查询。
		state.pooledLuaVMFast = nil
		if fastVM.ResetForBorrowedPrototypeDataSkippingClearPrefix(registerCount, constants, upvalues, protos, varargs, skipClearPrefix) {
			return fastVM
		}
	}

	if state.pooledLuaVMCached != nil {
		if pooledVMSlice, ok := state.pooledLuaVMCached[registerCount]; ok && len(pooledVMSlice) > 0 {
			vm := pooledVMSlice[len(pooledVMSlice)-1]
			state.pooledLuaVMCached[registerCount] = pooledVMSlice[:len(pooledVMSlice)-1]
			if vm == nil {
				// 污染条目直接丢弃并兜底创建新 VM，避免 nil deref。
				return NewVMWithBorrowedPrototypeData(registerCount, constants, upvalues, protos, varargs)
			}
			if vm.ResetForBorrowedPrototypeDataSkippingClearPrefix(registerCount, constants, upvalues, protos, varargs, skipClearPrefix) {
				return vm
			}
		}
	}

	// 兜底返回新 VM，确保异常恢复时不改变调用路径。
	return NewVMWithBorrowedPrototypeData(registerCount, constants, upvalues, protos, varargs)
}

// ReturnLuaVMToPool 归还 VM 到 state 缓存，供后续同寄存器窗口大小复用。
//
// 返回 nil 或已关闭 State 时不参与缓存。当前实现不跨 State 共享缓存，避免协程与
// 生命周期状态混用。
func (state *State) ReturnLuaVMToPool(vm *VM) {
	if state == nil || state.closed || vm == nil {
		// 关闭或无效 State 不复用 VM，交给 GC 回收。
		return
	}

	registerCount := len(vm.registers)
	if registerCount <= 0 {
		// 空寄存器 VM 无复用价值，避免将异常长度对象放入固定池。
		return
	}
	if state.pooledLuaVMFast == nil {
		// 单槽缓存优先承接最近 VM，优化同一函数反复 direct CALL 的场景。
		state.pooledLuaVMFast = vm
		return
	}
	if state.pooledLuaVMCached == nil {
		state.pooledLuaVMCached = make(map[int][]*VM)
	}
	// 限制池容量避免异常调用场景将少见的极大窗口长期持有；64 可覆盖当前递归基准的活跃深度。
	const maxPooledVMSPerBucket = 64
	pooledVMSlice, bucketExists := state.pooledLuaVMCached[registerCount]
	if !bucketExists {
		// 首次创建同窗口 bucket 时直接预留上限容量，避免递归回收路径反复扩容 slice。
		pooledVMSlice = make([]*VM, 0, maxPooledVMSPerBucket)
	}
	if len(pooledVMSlice) >= maxPooledVMSPerBucket {
		// 超过配额的条目直接丢弃，保持内存上限。
		return
	}
	state.pooledLuaVMCached[registerCount] = append(pooledVMSlice, vm)
}

// BorrowCallArgumentScratch 返回 State 级 Lua closure 调用参数临时区。
//
// 返回切片只允许调用方在进入被调 Lua closure 前短暂使用；被调 closure 初始化寄存器后不得继续持有。
// 该方法不适用于 Go closure，因为 Go 回调可能在调用期间保留参数切片。
func (state *State) BorrowCallArgumentScratch(count int) []Value {
	if state == nil || state.closed || count <= 0 {
		// 无效 State 或空参数时不需要临时区。
		return nil
	}
	if cap(state.callArgumentScratch) < count {
		// 参数数量超过已有容量时扩容一次，后续同等或更小调用复用。
		state.callArgumentScratch = make([]Value, count)
	}
	return state.callArgumentScratch[:count]
}

// Close 关闭当前 Lua State 并释放可达根引用。
//
// 当前阶段引入显式 userdata 关闭协议，Close 会触发注册表内的 userdata finalizer，
// 然后清空 registry/globals 引用并设置 closed 标记。重复调用允许且无副作用，
// 后续 GC 和 coroutine 关闭路径会在此衔接。
func (state *State) Close() {
	if state.closed {
		// 已关闭 State 再次关闭不产生副作用，方便 defer state.Close() 重入。
		return
	}

	// 先执行 userdata finalizer，保证 Go 侧资源可在 State 生命周期结束时回收。
	state.closeUserdatas()

	// 清空 root 引用，允许 Go GC 回收 registry 与 globals。
	state.registry = nil
	state.globals = nil
	state.stack = nil
	state.callFrames = nil
	state.externalGCRootFrames = nil
	state.pooledLuaVMCached = nil
	state.callArgumentScratch = nil
	state.mainThread = nil
	state.threads = nil
	state.userdatas = nil
	state.finalizableTables = nil
	state.finalizedTables = nil
	state.tableFinalizerRunner = nil
	state.debugEnvironment = nil
	state.runningThread = nil
	state.ctx = nil
	state.closed = true
}

// IsClosed 返回当前 State 是否已经关闭。
//
// 返回 true 表示 Close 已执行，后续公开 API 应拒绝继续使用该 State。
func (state *State) IsClosed() bool {
	// closed 只由 Close 写入，直接返回当前生命周期状态。
	return state.closed
}

// MainThread 返回当前 State 的主线程值。
//
// 当前阶段主线程由 state.mainThread 记录并保持独立状态。State 已关闭时返回 nil，避免返回已
// 释放的协程引用。
func (state *State) MainThread() Value {
	if state.closed {
		// State 关闭后不再持有 registry 与协程对象，返回 nil 保持对外行为可预测。
		return NilValue()
	}

	// 主线程占位存放在 registry[RegistryIndexMainThread]，保持与 Lua 5.3 registry 布局一致。
	return state.registry.RawGetInteger(RegistryIndexMainThread)
}

// Options 返回 State 当前使用的资源限制配置。
//
// 返回值是副本，调用方修改该值不会影响已有 State。
func (state *State) Options() Options {
	// options 在 State 创建时已经规范化，直接返回副本。
	return state.options
}

// SetDebugEnvironment 记录 debug 标准库绑定到当前 State 的运行环境。
//
// environment 由 stdlib/debug 包创建，runtime 仅保存不透明引用，避免产生包反向依赖；State 已关闭时忽略写入。
func (state *State) SetDebugEnvironment(environment any) {
	if state == nil || state.closed {
		// nil 或已关闭 State 不再保存 debug 环境。
		return
	}

	// 保存不透明 debug 环境，VM 层可通过 debuglib.EnvironmentForState 类型断言后复用。
	state.debugEnvironment = environment
}

// DebugEnvironment 返回当前 State 绑定的 debug 标准库运行环境。
//
// 返回值是不透明引用；未打开 debug 库、State 为 nil 或已关闭时返回 nil。
func (state *State) DebugEnvironment() any {
	if state == nil || state.closed {
		// nil 或已关闭 State 没有可用 debug 环境。
		return nil
	}

	// 返回 stdlib/debug 注入的不透明环境引用。
	return state.debugEnvironment
}

// enterGoCallback 标记进入 Go 回调边界。
//
// 每次 Go 回调进入时递增深度计数，支持嵌套回调场景；边界退出时必须调用
// exitGoCallback 与计数保持匹配，否则将导致 yield 边界异常放行。
func (state *State) enterGoCallback() {
	if state == nil {
		// nil state 不能记录回调边界，静默返回避免空指针扩散。
		return
	}
	if state.closed {
		// 关闭 State 已不再参与执行，边界标记不允许继续调整。
		return
	}

	state.goCallbackDepth++
}

// exitGoCallback 标记退出 Go 回调边界。
//
// 与 enterGoCallback 成对出现，按深度减 1；若出现不匹配调用，保持 0 防止计数下溢。
func (state *State) exitGoCallback() {
	if state == nil {
		// nil state 无边界状态可恢复，直接返回。
		return
	}
	if state.goCallbackDepth <= 0 {
		// 保护边界退出的计数一致性，不允许出现负值。
		state.goCallbackDepth = 0
		return
	}

	state.goCallbackDepth--
}

// EnterGoCallbackBoundary 进入一个不可 yield 的 Go/C 回调边界。
//
// 返回的函数必须由调用方 defer 执行，用于恢复边界深度。该方法供标准库中模拟 Lua C API
// 不可 yield 的回调场景使用，例如 table.sort comparator；nil 或已关闭 State 会返回空恢复函数。
func (state *State) EnterGoCallbackBoundary() func() {
	if state == nil {
		// nil State 没有边界可记录，返回空恢复函数便于调用方统一 defer。
		return func() {}
	}
	if state.closed {
		// 已关闭 State 不参与执行，返回空恢复函数避免改变状态。
		return func() {}
	}

	// 复用内部计数逻辑，保持与 Thread.Yield 的 goCallbackDepth 判断一致。
	state.enterGoCallback()
	return func() {
		// 恢复函数只负责退出本次边界，允许调用方安全 defer。
		state.exitGoCallback()
	}
}

// Running 返回当前执行协程与主线程标记。
//
// 返回值 thread 表示 state 当前正在运行的协程；isMain 为 true 表示当前主线程。
// State 关闭或运行状态不完整时返回 nil/false。
func (state *State) Running() (*Thread, bool) {
	if state == nil {
		// nil State 没有运行时上下文，无法返回有效协程。
		return nil, false
	}
	if state.closed {
		// 关闭 State 已结束执行生存期，不继续返回运行协程。
		return nil, false
	}
	if state.runningThread == nil {
		// runningThread 未初始化表示运行态未建立。
		return nil, false
	}

	// 判断当前运行对象是否主线程。
	return state.runningThread, state.runningThread.isMain
}

// HasCreatedCoroutines 返回当前 State 是否创建过主线程之外的 coroutine。
//
// 返回 true 表示调用现场可能需要保留 coroutine continuation 链，Lua 调用热路径应避免使用会裁剪
// 调用现场的优化路径。State 关闭或 nil 时返回 false。
func (state *State) HasCreatedCoroutines() bool {
	if state == nil || state.closed {
		// 无效或关闭 State 没有可恢复的 coroutine 图。
		return false
	}
	return len(state.threads) > 1
}

// IsYieldable 返回当前运行上下文是否允许 coroutine.yield。
//
// 返回 true 表示当前 running thread 是非主协程，且没有处在 Go 回调边界中；这与 Lua 5.3
// `coroutine.isyieldable` 的可观察语义对齐。State 关闭、运行态缺失、主线程或 Go 回调边界
// 均返回 false，不抛出错误。
func (state *State) IsYieldable() bool {
	if state == nil {
		// nil State 没有运行上下文，不允许 yield。
		return false
	}
	if state.closed {
		// 已关闭 State 不再参与执行，不允许 yield。
		return false
	}
	if state.runningThread == nil {
		// 没有 running thread 时无法定位 yield 目标。
		return false
	}
	if state.runningThread.isMain {
		// 主线程在 Lua 5.3 中不可 yield。
		return false
	}
	if state.goCallbackDepth > 0 {
		// Go 回调边界内禁止跨宿主栈 yield。
		return false
	}

	// 当前运行线程是普通协程且不在 Go 回调边界内，可以 yield。
	return true
}

// SetContext 绑定宿主传入的取消上下文。
//
// ctx 不能为 nil；成功后 VM 指令循环、Go/Lua 回调边界和长时间运行标准库应通过
// CheckContext 观察取消信号。State 已关闭时返回 ErrClosedState，避免重新绑定已释放对象。
func (state *State) SetContext(ctx context.Context) error {
	if state.closed {
		// 关闭后的 State 不允许重新绑定取消上下文。
		return ErrClosedState
	}
	if ctx == nil {
		// nil context 无法提供稳定取消语义，显式拒绝。
		return ErrNilContext
	}

	// 保存宿主上下文；调用方负责控制 context 生命周期。
	state.ctx = ctx
	state.ctxCanCancel = true
	return nil
}

// Context 返回当前 State 绑定的上下文。
//
// 返回值用于宿主检查当前绑定关系；State 已关闭时返回 nil。调用方不得假设该 context 一定
// 未取消，应通过 CheckContext 获取 VM 兼容的错误包装。
func (state *State) Context() context.Context {
	if state.closed {
		// 关闭后的 State 已释放上下文引用。
		return nil
	}

	// NewStateWithOptions 会保证默认上下文为 context.Background。
	return state.ctx
}

// RequestInterrupt 请求下一次 VM 检查点抛出 Lua 级中断错误。
//
// 该方法可由 CLI 信号处理 goroutine 调用；中断请求会被 CheckContext 消费一次，转换为
// `interrupted!` Lua error object。State 关闭后请求会被忽略，避免唤醒已释放对象。
func (state *State) RequestInterrupt() {
	if state == nil {
		// nil State 没有可写入的中断槽，调用方无需额外处理。
		return
	}
	if state.closed {
		// 关闭后的 State 不再执行 VM，忽略迟到信号。
		return
	}

	// 记录一次待消费中断；多个信号会保留为多次检查点错误。
	state.interruptCount.Add(1)
}

// CheckContext 检查当前 State 的取消信号。
//
// 未取消时返回 nil；State 已关闭时返回 ErrClosedState；上下文已取消或超时时返回携带 Lua
// string error object 的 RuntimeError，且错误链保留 context.Canceled 或 context.DeadlineExceeded。
// 宿主请求的中断会被消费一次并返回 `interrupted!` Lua error，便于 pcall 捕获后继续执行。
func (state *State) CheckContext() error {
	if state.closed {
		// 关闭后的 State 不再执行 VM 检查点。
		return ErrClosedState
	}
	if state.ctx == nil {
		// 理论上只有非法构造或关闭遗漏会出现 nil context，这里返回明确错误便于诊断。
		return ErrNilContext
	}

	for pendingInterrupts := state.interruptCount.Load(); pendingInterrupts > 0; pendingInterrupts = state.interruptCount.Load() {
		// SIGINT 类中断对齐 Lua 5.3 laction/lstop 语义，抛出一次可捕获 Lua 错误。
		if state.interruptCount.CompareAndSwap(pendingInterrupts, pendingInterrupts-1) {
			// 成功消费一次中断后立即返回 Lua 级错误对象。
			return NewRuntimeError(StringValue(ErrInterrupted.Error()), ErrInterrupted)
		}
	}

	if !state.ctxCanCancel {
		// 默认 background context 不会取消，跳过每条指令的 ctx.Err 接口调用。
		return nil
	}
	ctxErr := state.ctx.Err()
	if ctxErr != nil {
		// 取消或超时需要转换为可传播到 Lua 层的运行时错误对象。
		return NewRuntimeError(StringValue(ctxErr.Error()), ctxErr)
	}

	// 上下文仍可用，VM 可以继续执行。
	return nil
}

// StackTop 返回当前 Lua 栈的槽位数量。
//
// 返回值等价于当前栈顶位置；关闭后的 State 栈已释放，返回 0。
func (state *State) StackTop() int {
	if state.closed {
		// 关闭后的 State 不再持有栈，栈顶固定为 0。
		return 0
	}

	// 栈顶位置等于当前切片长度。
	return len(state.stack)
}

// AbsIndex 把 Lua 栈索引转换为绝对正索引。
//
// index 为正数时表示从栈底开始的 1-based 位置；index 为负数时表示从栈顶反向定位。
// index 为 0 在 Lua API 中无效，registry pseudo-index 保持原值，便于调用方继续识别。
func (state *State) AbsIndex(index int) int {
	if state.closed {
		// 关闭后的 State 不再持有栈，任何索引都视为无效。
		return 0
	}
	if index <= RegistryPseudoIndex {
		// pseudo-index 不属于当前栈帧，保持原值而不是转换为栈绝对索引。
		return index
	}
	if index > 0 {
		// 正索引已经是绝对索引，直接返回。
		return index
	}
	if index == 0 {
		// Lua 栈索引 0 无效，返回 0 供调用方识别。
		return 0
	}

	// 负索引从栈顶向下计数，-1 表示当前栈顶。
	return len(state.stack) + index + 1
}

// ValueAt 读取 Lua 栈上指定索引的值。
//
// index 支持正索引和负索引；索引无效、越界或 State 已关闭时返回 nil。该方法不弹出栈值。
func (state *State) ValueAt(index int) Value {
	if state.closed {
		// 关闭后的 State 不再持有栈或 registry，读取任何索引都返回 nil。
		return NilValue()
	}
	if index == RegistryPseudoIndex {
		// registry pseudo-index 直接返回 registry table 引用。
		return ReferenceValue(KindTable, state.registry)
	}

	// 先把相对索引转换为绝对 1-based 索引。
	absoluteIndex := state.AbsIndex(index)
	if absoluteIndex <= 0 || absoluteIndex > len(state.stack) {
		// 无效或越界索引按 Lua API 常见读取语义返回 nil。
		return NilValue()
	}

	// 转换为 Go 0-based index 后读取栈值。
	return state.stack[absoluteIndex-1]
}

// Push 向当前 Lua 栈顶压入一个值。
//
// value 可以是任意 Lua Value；若 State 已关闭返回 ErrClosedState，若压入后超过
// MaxStackDepth 返回资源限制错误并保持原栈不变。
func (state *State) Push(value Value) error {
	if state.closed {
		// 关闭后的 State 不允许继续修改栈。
		return ErrClosedState
	}

	nextDepth := len(state.stack) + 1
	if err := state.options.CheckStackDepth(nextDepth); err != nil {
		// 超过栈深度上限时直接返回错误，不修改原栈。
		return err
	}

	// 压入值到栈顶，后续索引体系会基于该切片实现。
	state.stack = append(state.stack, value)
	return nil
}

// Pop 从当前 Lua 栈顶弹出一个值。
//
// 返回值是原栈顶 Value；若 State 已关闭返回 ErrClosedState，若栈为空返回
// ErrStackUnderflow。
func (state *State) Pop() (Value, error) {
	if state.closed {
		// 关闭后的 State 不允许继续读取或修改栈。
		return NilValue(), ErrClosedState
	}
	if len(state.stack) == 0 {
		// 空栈没有可弹出的值，返回明确下溢错误。
		return NilValue(), ErrStackUnderflow
	}

	topIndex := len(state.stack) - 1
	value := state.stack[topIndex]
	state.stack[topIndex] = NilValue()
	state.stack = state.stack[:topIndex]
	return value, nil
}

// CallDepth 返回当前调用帧深度。
//
// 关闭后的 State 调用帧已释放，返回 0。
func (state *State) CallDepth() int {
	if state.closed {
		// 关闭后的 State 不再持有调用帧。
		return 0
	}

	// 调用深度等于调用帧切片长度。
	return len(state.callFrames)
}

// PushCallFrame 压入一个新的调用帧。
//
// frame 描述即将执行的 Lua 或 Go 函数；若 State 已关闭返回 ErrClosedState，若压入后超过
// MaxCallDepth 返回资源限制错误并保持原调用帧栈不变。
func (state *State) PushCallFrame(frame CallFrame) error {
	if state.closed {
		// 关闭后的 State 不允许继续修改调用帧栈。
		return ErrClosedState
	}

	nextDepth := len(state.callFrames) + 1
	if err := state.options.CheckCallDepth(nextDepth); err != nil {
		// 超过调用深度上限时直接返回错误，不修改原调用帧栈。
		return err
	}

	// 压入调用帧，后续 Lua/Go 调用执行器会扩展帧字段。
	state.callFrames = append(state.callFrames, frame)
	return nil
}

// PopCallFrame 弹出当前调用帧。
//
// 返回值是原当前调用帧；若 State 已关闭返回 ErrClosedState，若没有调用帧返回
// ErrCallFrameUnderflow。
func (state *State) PopCallFrame() (CallFrame, error) {
	if state.closed {
		// 关闭后的 State 不允许继续读取或修改调用帧栈。
		return CallFrame{}, ErrClosedState
	}
	if len(state.callFrames) == 0 {
		// 空调用帧栈没有可弹出的帧，返回明确下溢错误。
		return CallFrame{}, ErrCallFrameUnderflow
	}

	topIndex := len(state.callFrames) - 1
	frame := state.callFrames[topIndex]
	state.callFrames[topIndex] = CallFrame{}
	state.callFrames = state.callFrames[:topIndex]
	return frame, nil
}

// popCallFramesAbove 弹出调用帧栈中超过指定深度的帧。
//
// depth 使用调用前的调用栈长度；该 helper 仅用于 coroutine 错误恢复，确保错误 traceback
// 已快照后不把失败协程的帧泄漏到主线程。
func (state *State) popCallFramesAbove(depth int) {
	if state == nil || state.closed {
		// nil 或关闭 State 没有可清理栈。
		return
	}
	if depth < 0 {
		// 非法深度按清空处理，避免保留污染帧。
		depth = 0
	}
	for len(state.callFrames) > depth {
		// 逐帧弹出以复用 PopCallFrame 的清零逻辑。
		_, _ = state.PopCallFrame()
	}
}

// CurrentCallFrame 返回当前调用帧。
//
// 第二个返回值为 false 表示 State 已关闭或当前没有调用帧。
func (state *State) CurrentCallFrame() (CallFrame, bool) {
	if state.closed {
		// 关闭后的 State 不再持有调用帧。
		return CallFrame{}, false
	}
	if len(state.callFrames) == 0 {
		// 当前没有调用帧。
		return CallFrame{}, false
	}

	// 返回调用帧栈顶帧。
	return state.callFrames[len(state.callFrames)-1], true
}

// TracebackFrames 收集当前调用栈快照。
//
// 返回顺序从当前帧到最早帧，便于 debug.traceback 后续按 Lua 调用链向外展开。返回切片是
// 独立副本，调用方修改不会影响 State 内部调用帧；State 已关闭或当前没有调用帧时返回空切片。
func (state *State) TracebackFrames() []CallFrame {
	if state.closed {
		// 关闭后的 State 已释放调用帧 root，traceback 只能返回空快照。
		return nil
	}
	if len(state.callFrames) == 0 {
		// 没有调用帧时表示当前不在 Lua/Go 调用链中。
		return nil
	}

	frames := make([]CallFrame, 0, len(state.callFrames))
	for frameIndex := len(state.callFrames) - 1; frameIndex >= 0; frameIndex-- {
		// 从栈顶向栈底复制，保证第一个元素是当前正在执行的帧。
		frames = append(frames, state.callFrames[frameIndex])
	}
	return frames
}

// SetPendingErrorTracebackFrames 保存错误处理器可见的 traceback 快照。
//
// frames 必须按 TracebackFrames 的当前帧到最早帧顺序传入；该方法会复制切片，确保 xpcall 在
// 裁剪真实调用帧后，debug.traceback 仍能读取失败函数的 Lua 现场。传入空切片会清空快照。
func (state *State) SetPendingErrorTracebackFrames(frames []CallFrame) {
	if state == nil || state.closed || len(frames) == 0 {
		// 无效 State、关闭 State 或空快照都按清空处理，避免保留旧错误现场。
		if state != nil {
			state.pendingErrorTracebackFrames = nil
		}
		return
	}
	state.pendingErrorTracebackFrames = append([]CallFrame(nil), frames...)
}

// PendingErrorTracebackFrames 返回错误处理器可见的 traceback 快照。
//
// 返回值是独立副本，供 debug.traceback 在 xpcall handler 中优先展示失败现场；没有快照时返回
// nil，调用方应回退到当前 State 调用帧。
func (state *State) PendingErrorTracebackFrames() []CallFrame {
	if state == nil || state.closed || len(state.pendingErrorTracebackFrames) == 0 {
		// 无快照时返回 nil，避免 debug API 把空快照误认为真实调用栈。
		return nil
	}
	return append([]CallFrame(nil), state.pendingErrorTracebackFrames...)
}

// ClearPendingErrorTracebackFrames 清除错误处理器 traceback 快照。
//
// xpcall handler 完成或失败后必须调用该方法，避免后续 debug.traceback 误用旧错误现场。
func (state *State) ClearPendingErrorTracebackFrames() {
	if state == nil {
		// nil State 没有可清理内容。
		return
	}
	state.pendingErrorTracebackFrames = nil
}

// EnterErrorHandler 标记进入 xpcall 错误处理器。
//
// 该状态用于区分普通 Lua 递归栈溢出与错误处理器内部再次失败；后者按 Lua 5.3 语义应返回
// 包含 `error handling` 的错误对象。nil 或关闭 State 直接忽略。
func (state *State) EnterErrorHandler() {
	if state == nil || state.closed {
		// 无效 State 没有可更新的错误处理器状态。
		return
	}
	state.errorHandlerDepth++
}

// ExitErrorHandler 标记离开 xpcall 错误处理器。
//
// 调用方必须与 EnterErrorHandler 成对使用；若深度已为 0 则保持 0，避免异常路径造成负数。
func (state *State) ExitErrorHandler() {
	if state == nil || state.closed {
		// 无效 State 没有可更新的错误处理器状态。
		return
	}
	if state.errorHandlerDepth > 0 {
		// 只在正深度时递减，避免异常重复退出破坏状态。
		state.errorHandlerDepth--
	}
}

// InErrorHandler 返回当前是否正在 xpcall 错误处理器内。
//
// 返回 true 表示错误处理器正在运行；资源限制等二次错误可据此改写为 Lua 官方的 error handling
// 语义。nil 或关闭 State 返回 false。
func (state *State) InErrorHandler() bool {
	if state == nil || state.closed {
		// 无效 State 不处于错误处理器内。
		return false
	}
	return state.errorHandlerDepth > 0
}

// ReplaceCurrentCallFrame 原地替换当前调用帧。
//
// frame 描述尾调用进入的新函数帧；该方法用于 Lua 5.3 tail call 基础语义，替换栈顶帧
// 而不增加调用深度。若 State 已关闭返回 ErrClosedState，若当前没有调用帧返回
// ErrCallFrameUnderflow。
func (state *State) ReplaceCurrentCallFrame(frame CallFrame) error {
	if state.closed {
		// 关闭后的 State 不允许继续修改调用帧栈，避免重新建立已释放 root。
		return ErrClosedState
	}
	if len(state.callFrames) == 0 {
		// 空调用帧栈没有可替换的当前帧，尾调用执行器应把该错误视为 VM 状态不一致。
		return ErrCallFrameUnderflow
	}

	// 原地覆盖栈顶帧，保持调用深度不变以模拟 Lua tail call 的帧复用。
	state.callFrames[len(state.callFrames)-1] = frame
	return nil
}

// UpdateCurrentFramePC 仅更新当前调用帧的执行 PC。
//
// pc 是当前 Lua Proto.Code 的 0-based 指令下标。该方法用于 VM 热路径同步 debug/traceback
// 可见的当前位置，避免每条指令替换完整 CallFrame。State 已关闭返回 ErrClosedState，当前
// 没有调用帧返回 ErrCallFrameUnderflow。
func (state *State) UpdateCurrentFramePC(pc int) error {
	if state.closed {
		// 关闭后的 State 不允许继续修改调用帧栈。
		return ErrClosedState
	}
	if len(state.callFrames) == 0 {
		// 空调用帧栈没有可更新的当前帧。
		return ErrCallFrameUnderflow
	}

	// 只写入 PC 字段，保留 Name、NameWhat、TailCall、Varargs 等帧元数据。
	state.callFrames[len(state.callFrames)-1].CurrentPC = pc
	return nil
}

// PushActiveVM 记录正在执行的 Lua VM。
//
// vm 必须来自当前 State 的 Lua closure 执行循环；该记录让 collectgarbage 能扫描活动寄存器中的 local。
func (state *State) PushActiveVM(vm *VM) {
	if state == nil || state.closed || vm == nil {
		// 无效 State 或 nil VM 没有可记录内容，直接忽略保持调用方 defer 安全。
		return
	}

	// 追加到栈尾，支持 Lua 调 Lua 的嵌套执行。
	state.activeVMs = append(state.activeVMs, vm)
}

// PopActiveVM 移除最近记录的 Lua VM。
//
// vm 为 nil 时弹出栈顶；非 nil 时优先移除最后一个相同指针，避免错误路径泄漏活动寄存器。
func (state *State) PopActiveVM(vm *VM) {
	if state == nil || len(state.activeVMs) == 0 {
		// 没有活动 VM 时直接返回，保持错误路径幂等。
		return
	}
	if vm == nil {
		// 调用方不关心具体 VM 时弹出栈顶。
		state.activeVMs[len(state.activeVMs)-1] = nil
		state.activeVMs = state.activeVMs[:len(state.activeVMs)-1]
		return
	}
	for index := len(state.activeVMs) - 1; index >= 0; index-- {
		if state.activeVMs[index] == vm {
			// 找到匹配 VM 后移除并清空槽位，避免保留寄存器引用。
			copy(state.activeVMs[index:], state.activeVMs[index+1:])
			state.activeVMs[len(state.activeVMs)-1] = nil
			state.activeVMs = state.activeVMs[:len(state.activeVMs)-1]
			return
		}
	}
}

// ActiveVMAtLevel 按 Lua debug level 返回活动 VM。
//
// level 使用 Lua 1-based 层级，1 表示当前正在执行的 Lua VM，2 表示上一层 Lua VM。返回 false
// 表示 State 无效、level 越界或对应层级没有活动 VM。该方法用于 debug.setlocal 同步 vararg。
func (state *State) ActiveVMAtLevel(level int64) (*VM, bool) {
	if state == nil || state.closed {
		// 无效或已关闭 State 不暴露活动 VM。
		return nil, false
	}
	if level <= 0 {
		// 非正层级没有对应的 Lua debug 帧。
		return nil, false
	}
	vmIndex := len(state.activeVMs) - int(level)
	if vmIndex < 0 || vmIndex >= len(state.activeVMs) {
		// 层级超过活动 VM 栈深度时视为未命中。
		return nil, false
	}
	if state.activeVMs[vmIndex] == nil {
		// 空槽位表示对应 VM 已被移除。
		return nil, false
	}

	// 返回与 debug level 对齐的活动 VM。
	return state.activeVMs[vmIndex], true
}

// ProtectedCall 在可恢复边界内执行运行时函数。
//
// call 负责执行一段 Lua/Go 调用逻辑；执行成功时保留 call 对栈和调用帧的修改，返回错误或
// panic 时恢复到进入 ProtectedCall 前的栈顶和调用深度。该方法对齐 Lua 5.3 pcall/xpcall
// 的基础保护语义，但暂不负责 error handler、traceback 或错误对象入栈。
func (state *State) ProtectedCall(call ProtectedCallFunc) (err error) {
	if state.closed {
		// 关闭后的 State 不允许建立新的保护调用边界。
		return ErrClosedState
	}
	if call == nil {
		// 空回调无法执行，也不能作为成功调用处理。
		return ErrNilProtectedCall
	}

	stackTop := len(state.stack)
	callDepth := len(state.callFrames)
	defer func() {
		if recovered := recover(); recovered != nil {
			// panic 表示运行时边界内出现不可直接返回的异常，保护调用需要恢复栈并转为错误。
			state.restoreProtectedCallBoundary(stackTop, callDepth)
			err = PanicToError(recovered)
		}
	}()

	if callErr := call(state); callErr != nil {
		// 回调显式返回错误时恢复进入前边界，把错误交给调用方继续包装或传播。
		tracebackFrames := state.TracebackFrames()
		state.restoreProtectedCallBoundary(stackTop, callDepth)
		return WithTracebackFrames(ensureRuntimeError(callErr), tracebackFrames)
	}

	// 成功路径保留回调产生的栈和调用帧变化，供后续调用收尾逻辑处理返回值。
	return nil
}

// restoreProtectedCallBoundary 恢复 protected call 进入前的栈和调用帧边界。
//
// stackTop 与 callDepth 必须来自同一个 State 的 ProtectedCall 入口快照；该方法只裁剪多出的
// 栈槽和调用帧，并把被裁剪槽位置为 nil/零值，便于 Go GC 释放引用。
func (state *State) restoreProtectedCallBoundary(stackTop int, callDepth int) {
	if len(state.stack) > stackTop {
		// 错误路径需要清空新增栈槽，避免失败调用泄漏临时值或返回值。
		for stackIndex := stackTop; stackIndex < len(state.stack); stackIndex++ {
			state.stack[stackIndex] = NilValue()
		}
		state.stack = state.stack[:stackTop]
	}
	if len(state.callFrames) > callDepth {
		// 错误路径需要弹出新增调用帧，恢复到 protected call 入口的调用深度。
		for frameIndex := callDepth; frameIndex < len(state.callFrames); frameIndex++ {
			state.callFrames[frameIndex] = CallFrame{}
		}
		state.callFrames = state.callFrames[:callDepth]
	}
}

// Registry 返回 State 的 registry table。
//
// 调用方可通过 raw get/set 访问 registry；后续对外 API 会封装更安全的访问方式。
func (state *State) Registry() *Table {
	if state.closed {
		// 关闭后的 State 已释放 registry root，对外返回 nil 表示不可继续访问。
		return nil
	}

	// registry 在 NewState 中初始化，生命周期与 State 一致。
	return state.registry
}

// Globals 返回 State 的全局环境表。
//
// 该表与 registry[RegistryIndexGlobals] 指向同一个对象。
func (state *State) Globals() *Table {
	if state.closed {
		// 关闭后的 State 已释放 globals root，对外返回 nil 表示不可继续访问。
		return nil
	}

	// globals 在 NewState 中初始化，并写入 registry[2]。
	return state.globals
}

// SetGlobal 设置全局环境中的一个 string 字段。
//
// value 为 nil 时删除对应全局变量，保持 Lua table 赋 nil 删除字段的基础行为。
func (state *State) SetGlobal(name string, value Value) {
	if state.closed {
		// 关闭后的 State 不再持有全局表，写入请求直接忽略。
		return
	}

	// 全局变量底层存储在 globals table 的 string key 中。
	state.globals.RawSetString(name, value)
}

// GetGlobal 读取全局环境中的一个 string 字段。
//
// name 不存在时返回 Lua nil。
func (state *State) GetGlobal(name string) Value {
	if state.closed {
		// 关闭后的 State 不再持有全局表，读取按缺失全局变量返回 nil。
		return NilValue()
	}

	// 全局变量底层从 globals table 的 string key 读取。
	return state.globals.RawGetString(name)
}
