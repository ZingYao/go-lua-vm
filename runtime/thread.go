package runtime

import "errors"

var (
	// ErrNilThread 表示调用方操作了空的线程对象。
	ErrNilThread = errors.New("nil coroutine thread")
	// ErrNilThreadState 表示线程没有绑定 State。
	ErrNilThreadState = errors.New("thread has nil state")
	// ErrMainThreadResume 表示尝试 resume 主线程。
	ErrMainThreadResume = errors.New("cannot resume the main thread")
	// ErrDeadThread 表示 resume 已终止或执行完成的线程。
	ErrDeadThread = errors.New("cannot resume a dead coroutine")
	// ErrRunningThread 表示线程正在运行时再次 resume。
	ErrRunningThread = errors.New("cannot resume non-suspended coroutine")
	// ErrNonCallableThread 表示线程入口不是可调用对象。
	ErrNonCallableThread = errors.New("thread entry is not callable")
	// ErrYieldFromGoCallback 表示在 Go 回调边界中禁止 yield。
	ErrYieldFromGoCallback = errors.New("cannot yield across Go callback boundary")
	// ErrYieldFromNonRunning 表示仅运行中的协程才能 yield。
	ErrYieldFromNonRunning = errors.New("yield can only be called in running coroutine")
	// ErrYieldFromMainThread 表示主线程不允许 yield。
	ErrYieldFromMainThread = errors.New("main thread cannot yield")
	// ErrCoroutineYield 表示 Lua closure 协程通过 coroutine.yield 主动让出。
	ErrCoroutineYield = errors.New("coroutine yielded")
)

// CoroutineStatus 表示 coroutine 状态字符串。
//
// 与 Lua 5.3 `coroutine.status` 返回值对齐，状态语义用于状态机和可见行为校验。
type CoroutineStatus string

const (
	// CoroutineStatusSuspended 表示线程已创建或已 yield，可再次 resume。
	CoroutineStatusSuspended CoroutineStatus = "suspended"
	// CoroutineStatusRunning 表示当前正在由 VM 执行。
	CoroutineStatusRunning CoroutineStatus = "running"
	// CoroutineStatusNormal 表示线程运行过且不在当前运行路径中。
	CoroutineStatusNormal CoroutineStatus = "normal"
	// CoroutineStatusDead 表示线程执行完成或不可恢复。
	CoroutineStatusDead CoroutineStatus = "dead"
)

// Thread 表示一个 Lua 协程实例。
//
// 当前阶段 Thread 仅承载最小协程生命周期状态、入口函数引用和栈快照，后续会接入
// 实际 resume/yield 的执行栈与调用保护机制。
type Thread struct {
	// state 保存当前线程所属的 Lua State，可用于判断关闭状态和运行线程归属。
	state *State
	// function 保存 create 时记录的入口函数，当前阶段只支持 Go closure 直接执行。
	function Value
	// status 记录协程当前生命周期状态。
	status CoroutineStatus
	// isMain 标识是否为主线程。
	isMain bool
	// resumeParent 保存本次 resume 进入前的运行线程，用于嵌套 coroutine 恢复父执行上下文。
	resumeParent *Thread
	// stack 保存当前协程最近一次 resume 的参数栈快照。
	stack []Value
	// yieldedValues 保存 yield 时挂起前一次向 resume 调用方返回的返回值。
	yieldedValues []Value
	// continuationPending 表示 Lua VM 已保存 yield 后的续执行现场。
	continuationPending bool
	// lastError 保存最近一次 resume 的错误对象，便于 coroutine error 兼容。
	lastError Value
	// tracebackFrames 保存最近一次 yield 时的调用帧快照，供 debug.traceback(thread, ...) 读取。
	tracebackFrames []CallFrame
	// localRegisterSnapshots 保存最近一次 yield 时各层 Lua VM 寄存器快照，供 debug.getlocal(thread, ...) 读取。
	localRegisterSnapshots [][]Value
	// localRegisterSnapshotsDirty 表示挂起快照被 debug.setlocal(thread, ...) 修改过，需要恢复前写回 VM。
	localRegisterSnapshotsDirty bool
}

// Error 返回当前协程最近一次 resume 产生的错误对象。
//
// 若最近 resume 成功，返回 nil；若最近 resume 发生 error，返回标准化的 Lua error object。
// 与 `coroutine.error` 对齐，空线程返回 lua nil。
func (thread *Thread) Error() Value {
	if thread == nil {
		// nil 线程没有状态，不应抛出异常，直接返回 nil 占位。
		return NilValue()
	}

	// 返回最近一次 resume 留存的错误对象。
	return thread.lastError
}

// NewThread 为当前 State 创建一个新协程。
//
// function 为入口函数，当前阶段要求可执行对象由 caller 保证；函数本身不在 create 时校验，
// resume 时会按可调用性检查后执行。返回的协程状态初始为 suspended，非主线程默认可 resume。
func (state *State) NewThread(function Value) *Thread {
	if state == nil {
		// nil State 无法挂载线程，返回 nil 让调用方按 API 保持空值判断。
		return nil
	}
	if state.closed {
		// 已关闭 State 不再接纳新的协程实例，返回 nil 与 Close 语义一致。
		return nil
	}

	// 新建协程时记录所属 state 与入口函数，状态置为 suspended 以符合 create/状态检查。
	thread := &Thread{
		state:    state,
		function: function,
		status:   CoroutineStatusSuspended,
	}
	state.threads = append(state.threads, thread)
	return thread
}

// Function 返回当前协程入口函数值。
//
// 返回值用于 lua 包的 Lua closure 执行器读取入口；nil 或状态损坏的协程返回 Lua nil，避免
// 外层执行器直接访问 Thread 内部字段。
func (thread *Thread) Function() Value {
	if thread == nil {
		// nil 线程没有入口函数。
		return NilValue()
	}

	// 返回 create 时记录的入口函数值。
	return thread.function
}

// State 返回当前协程所属的运行时 State。
//
// 返回值可能为 nil，表示线程无效或已失去运行时绑定。该方法只暴露只读归属关系，供上层
// coroutine continuation 在 yield 后从挂起线程反查宿主 State，不允许调用方修改线程内部状态。
func (thread *Thread) State() *State {
	if thread == nil {
		// nil 线程没有所属 State。
		return nil
	}

	// 返回创建协程时绑定的 State。
	return thread.state
}

// Status 返回当前线程的可见状态。
//
// 为避免外部误读 running 状态，若线程当前由 state 正在运行，返回 running；否则返回
// 存储状态。主线程固定返回 running，以保留其唯一执行语义。
func (thread *Thread) Status() CoroutineStatus {
	if thread == nil {
		// nil 线程无法参与状态读取，返回 dead 表示不可恢复状态，便于链路兜底处理。
		return CoroutineStatusDead
	}
	if thread.state == nil {
		// 状态丢失的协程也视为不可恢复的 dead 状态。
		return CoroutineStatusDead
	}
	if thread.isMain {
		// 主线程当前总是 running，不随 resume/yield 状态机切换。
		return CoroutineStatusRunning
	}
	// 如果当前 state.runningThread 不是该线程，说明本线程未处于主动执行态。
	if thread.state.runningThread != thread {
		if thread.status == CoroutineStatusRunning {
			// 父协程正在等待子协程执行时不再是当前 running thread，Lua 5.3 对外显示为 normal。
			return CoroutineStatusNormal
		}
		return thread.status
	}

	// state.runningThread 与 thread 一致时才返回 running，供状态查询与调试对齐。
	return CoroutineStatusRunning
}

// Resume 恢复线程执行。
//
// 入参 args 会作为本次 resume 的实参快照写入 thread stack。返回值为 resume 成功路径的
// Lua 值列表；若线程不可恢复返回错误且不做执行。执行成功后线程会变为 dead 或 suspended。
func (thread *Thread) Resume(args ...Value) ([]Value, error) {
	if thread == nil {
		// 空线程无入口不可恢复。
		return nil, ErrNilThread
	}
	if thread.state == nil {
		// 丢失状态的线程无法进入 state 运行上下文。
		return nil, ErrNilThreadState
	}
	if thread.state.closed {
		// 关闭 State 后协程不得继续执行。
		return nil, ErrClosedState
	}
	if thread.isMain {
		// 协程库语义下主线程不可 resume，返回显式错误避免破坏 main thread 约束。
		return nil, ErrMainThreadResume
	}
	if thread.status == CoroutineStatusDead {
		// dead 协程不再可恢复，重试 resume 必须失败。
		return nil, ErrDeadThread
	}
	if thread.status == CoroutineStatusRunning {
		// 同一协程运行中再次 resume 说明调用方违反调用契约。
		return nil, ErrRunningThread
	}

	// 切换为 running，保证 Status 在 resume 期间可被外部观察为执行态。
	previousRunningThread := thread.state.runningThread
	if previousRunningThread == nil {
		// runningThread 异常缺失时退回主线程，避免后续恢复为 nil 破坏 State 运行态。
		previousRunningThread = thread.state.mainThread
	}
	thread.resumeParent = previousRunningThread
	thread.status = CoroutineStatusRunning
	thread.state.runningThread = thread
	thread.stack = append([]Value(nil), args...)
	thread.lastError = NilValue()
	thread.tracebackFrames = nil
	if !thread.continuationPending {
		// 非 continuation 恢复路径才清空局部快照，避免 debug.setlocal(thread, ...) 写入丢失。
		thread.localRegisterSnapshots = nil
		thread.localRegisterSnapshotsDirty = false
	}

	// 若之前有未消费的 yield 返回值，优先在本次 resume 返回这些值并保持 suspended。
	if len(thread.yieldedValues) > 0 && !thread.continuationPending {
		// Yield 场景下，resume 首先向调用方返回上次 yield 数据。
		results := append([]Value(nil), thread.yieldedValues...)
		thread.yieldedValues = nil
		thread.lastError = NilValue()
		thread.status = CoroutineStatusNormal
		thread.state.runningThread = thread.resumeParentOrMain()
		thread.resumeParent = nil
		return results, nil
	}

	// 非主线程 resume 时不再支持空入口，返回不可调用错误并将协程标记为 dead。
	if thread.function.IsNil() {
		// 空入口不能执行，resume 应返回错误并终止该 coroutine。
		thread.lastError = ErrorObject(ErrNonCallableThread)
		thread.status = CoroutineStatusDead
		thread.state.runningThread = thread.resumeParentOrMain()
		thread.resumeParent = nil
		return nil, ErrNonCallableThread
	}

	// 当前阶段仅支持 KindGoClosure 作为可执行入口，其余类型先返回不可恢复错误。
	if thread.function.Kind == KindGoClosure {
		var results []Value
		var callErr error
		allowYield := goClosureAllowsYield(thread.function)
		if allowYield && thread.continuationPending && thread.state.luaThreadRunner != nil {
			// 可 yield Go trampoline 首次进入后会在内部 Lua chunk 保存 continuation；后续 resume
			// 必须恢复该 Lua continuation，而不是重新调用 trampoline 本身。
			thread.yieldedValues = nil
			results, callErr = thread.state.luaThreadRunner(thread, args...)
		} else if allowYield {
			// 部分标准库入口只是把控制权转交给 Lua closure，例如 dofile；这类薄包装必须允许内部
			// Lua chunk yield，不能把整段执行归类为不可让出的 Go/C 回调。
			results, callErr = callGoClosureResults(thread.function, args...)
		} else {
			thread.state.enterGoCallback()
			results, callErr = func() ([]Value, error) {
				// 使用延迟退出确保 Go 回调边界不泄漏。
				defer thread.state.exitGoCallback()
				return callGoClosureResults(thread.function, args...)
			}()
		}
		if allowYield && errors.Is(callErr, ErrCoroutineYield) {
			// 内部 Lua chunk 已由 Thread.Yield 切到 suspended，并把 yield 值写入 yieldedValues。
			thread.lastError = NilValue()
			return append([]Value(nil), thread.yieldedValues...), nil
		}
		// 可执行结束后，当前线程不再保持 running，先置为 normal 再根据执行结果收敛。
		thread.status = CoroutineStatusNormal
		thread.state.runningThread = thread.resumeParentOrMain()
		thread.resumeParent = nil
		if callErr != nil {
			thread.lastError = ErrorObject(callErr)
			// Go closure 执行错误后视为死协程，resume 再次尝试时返回 dead。
			thread.status = CoroutineStatusDead
			return nil, callErr
		}
		thread.lastError = NilValue()

		// 若 Go closure 调用成功，按 Lua 风格保持 dead。当前版本不支持协程内再次挂起。
		thread.status = CoroutineStatusDead
		return append([]Value(nil), results...), nil
	}
	if thread.function.Kind == KindLuaClosure && thread.state.luaThreadRunner != nil {
		// Lua closure 协程通过外部注入执行器运行，避免 runtime 包反向依赖 lua 执行循环。
		if thread.continuationPending {
			// continuation 路径中旧 yield 值已返回给上一轮 coroutine.resume，本轮实参会作为 yield 返回值。
			thread.yieldedValues = nil
		}
		baseCallDepth := len(thread.state.callFrames)
		results, callErr := thread.state.luaThreadRunner(thread, args...)
		if errors.Is(callErr, ErrCoroutineYield) {
			// yield 已经把线程置为 suspended 并切回主线程，resume 返回 true 与 yield 值。
			thread.lastError = NilValue()
			return append([]Value(nil), thread.yieldedValues...), nil
		}
		thread.status = CoroutineStatusNormal
		thread.state.runningThread = thread.resumeParentOrMain()
		thread.resumeParent = nil
		if callErr != nil {
			thread.lastError = ErrorObject(callErr)
			thread.tracebackFrames = thread.state.TracebackFrames()
			thread.state.popCallFramesAbove(baseCallDepth)
			// Lua closure 执行错误后视为 dead，错误对象留给 coroutine.resume 包装。
			thread.status = CoroutineStatusDead
			return nil, callErr
		}
		thread.lastError = NilValue()
		thread.continuationPending = false
		thread.status = CoroutineStatusDead
		return append([]Value(nil), results...), nil
	}

	// 除 Go closure外的入口不进入执行阶段，当前实现不模拟 VM 调用链。
	thread.lastError = ErrorObject(ErrNonCallableThread)
	thread.status = CoroutineStatusDead
	thread.state.runningThread = thread.resumeParentOrMain()
	thread.resumeParent = nil
	return nil, ErrNonCallableThread
}

// MarkContinuationPending 标记当前协程持有 Lua VM continuation。
//
// pending 为 true 时，下一次 Resume 会跳过旧 yield 值的直接消费，改由 lua 包注入的执行器
// 使用 resume 实参恢复上次 coroutine.yield 调用。该方法只暴露布尔状态，不泄露 VM 细节。
func (thread *Thread) MarkContinuationPending(pending bool) {
	if thread == nil {
		// nil 线程没有可标记的 continuation。
		return
	}

	// 记录 continuation 是否存在，供 Resume 与 lua 执行器协同判断恢复路径。
	thread.continuationPending = pending
}

// HasContinuationPending 返回当前协程是否持有 Lua VM continuation。
//
// 返回 true 表示 lua 包保存了可续执行现场；runtime 仅据此调整 Resume 的 yield 值消费策略。
func (thread *Thread) HasContinuationPending() bool {
	if thread == nil {
		// nil 线程没有 continuation。
		return false
	}

	// 返回当前 continuation 标记。
	return thread.continuationPending
}

// Wrap 根据 function 创建 coroutine.wrap 兼容闭包。
//
// wrapper 持有同一个 thread 句柄，每次调用都会执行一次 resume；resume 成功返回值
// 则透传，resume error 直接返回给上层，以便让调用链在 Go/Lua 互调时模拟抛错行为。
func (state *State) Wrap(function Value) Value {
	thread := state.NewThread(function)
	wrapper := &GoClosureWithUpvalues{Function: GoResultsFunction(func(args ...Value) ([]Value, error) {
		if thread == nil {
			// 只要 state 为空、关闭或异常，先构造 nil-state 的非调用错误。
			return nil, ErrNilThreadState
		}

		// wrap 的语义是直接转交给 thread.Resume，避免多一次状态转换。
		return thread.Resume(args...)
	})}
	return ReferenceValue(KindGoClosure, wrapper)
}

// Yield 在当前线程执行中主动让出控制。
//
// values 会被记录为本次 yield 的返回值，调用 resume 时返回给调用方；仅 running 状态线程可
// 进行 yield，且主线程禁止 yield。当前阶段不接入真实调用栈，仅完成状态切换与值带回。
func (thread *Thread) Yield(values ...Value) error {
	if thread == nil {
		// 空线程不能让出执行。
		return ErrNilThread
	}
	if thread.state == nil {
		// 失去 State 绑定的线程不再可恢复 yield。
		return ErrNilThreadState
	}
	if thread.state.closed {
		// 关闭的 State 不允许继续执行路径。
		return ErrClosedState
	}
	if thread.isMain {
		// Lua 语义中 main thread 不可 yield。
		return ErrYieldFromMainThread
	}
	if thread.state.goCallbackDepth > 0 {
		// Go 回调边界内禁止 yield，避免跨宿主栈让出。
		return ErrYieldFromGoCallback
	}
	if thread.status != CoroutineStatusRunning {
		// 非 running 协程不处于可让出执行控制的状态。
		return ErrYieldFromNonRunning
	}

	// 当前 running 协程进入 suspended，保存值供下一次 resume 返回。
	thread.yieldedValues = append([]Value(nil), values...)
	thread.tracebackFrames = thread.state.TracebackFrames()
	thread.localRegisterSnapshots = thread.state.activeVMRegisterSnapshots()
	thread.localRegisterSnapshotsDirty = false
	thread.status = CoroutineStatusSuspended
	thread.state.runningThread = thread.resumeParentOrMain()
	return nil
}

// resumeParentOrMain 返回当前 resume 入口前的父运行线程。
//
// 嵌套 coroutine.wrap/resume 中，内层 yield 后必须恢复外层 coroutine 的运行态；没有父线程
// 或父线程已经失效时退回主线程，保持非嵌套调用的既有行为。
func (thread *Thread) resumeParentOrMain() *Thread {
	if thread == nil || thread.state == nil {
		// 缺少线程或 State 时无法推断父运行态。
		return nil
	}
	if thread.resumeParent != nil && thread.resumeParent.state != nil && !thread.resumeParent.state.closed {
		// 有父运行线程时优先恢复，覆盖外层协程调用内层协程的 Lua 5.3 语义。
		return thread.resumeParent
	}

	// 默认恢复到主线程，兼容普通 coroutine.resume 从主线程进入的路径。
	return thread.state.mainThread
}

// goClosureAllowsYield 判断 Go closure 是否声明为可跨协程 yield。
//
// 默认 GoResultsFunction/GoFunction 仍视为不可 yield 的 Go/C 边界；只有显式包装为
// GoClosureWithUpvalues 且 AllowYield=true 的薄入口会放行，用于 dofile 这类 Lua chunk trampoline。
func goClosureAllowsYield(function Value) bool {
	if function.Kind != KindGoClosure {
		// 非 Go closure 不走该边界判断。
		return false
	}
	closure, ok := function.Ref.(*GoClosureWithUpvalues)
	if !ok || closure == nil {
		// 普通 Go 函数没有显式声明，保持不可 yield。
		return false
	}
	return closure.AllowYield
}

// TracebackFrames 返回协程挂起时保存的调用帧快照。
//
// 返回顺序从挂起点当前帧到最早帧；若协程未挂起或没有快照，返回 nil。返回切片为副本，
// 调用方修改不会影响线程内部保存的调试状态。
func (thread *Thread) TracebackFrames() []CallFrame {
	if thread == nil {
		// nil 线程没有可读调用栈。
		return nil
	}
	if len(thread.tracebackFrames) == 0 {
		// 未保存挂起快照时返回空。
		return nil
	}
	frames := make([]CallFrame, len(thread.tracebackFrames))
	copy(frames, thread.tracebackFrames)
	return frames
}

// LocalRegisterAtLevel 返回挂起协程指定 debug level 与寄存器的值。
//
// level 使用 Lua debug 1-based 层级，1 表示协程挂起点所在 Lua 帧；register 使用 VM 0-based
// 寄存器编号。未命中返回 nil/false，调用方按没有局部变量处理。
func (thread *Thread) LocalRegisterAtLevel(level int64, register int) (Value, bool) {
	if thread == nil || level <= 0 || register < 0 {
		// nil 线程、非法层级或非法寄存器都不能读取。
		return NilValue(), false
	}
	snapshotIndex := int(level - 1)
	if snapshotIndex < 0 || snapshotIndex >= len(thread.localRegisterSnapshots) {
		// 层级超过挂起时保存的 VM 栈。
		return NilValue(), false
	}
	registers := thread.localRegisterSnapshots[snapshotIndex]
	if register >= len(registers) {
		// 寄存器越界时按未命中处理。
		return NilValue(), false
	}
	return registers[register], true
}

// SetLocalRegisterAtLevel 写入挂起协程指定 debug level 与寄存器的值。
//
// level 和 register 语义同 LocalRegisterAtLevel。返回 true 表示写入快照成功；该快照供
// debug.getlocal(thread, ...) 后续读取，并为未来协程 continuation 恢复提供数据来源。
func (thread *Thread) SetLocalRegisterAtLevel(level int64, register int, value Value) bool {
	if thread == nil || level <= 0 || register < 0 {
		// nil 线程、非法层级或非法寄存器都不能写入。
		return false
	}
	snapshotIndex := int(level - 1)
	if snapshotIndex < 0 || snapshotIndex >= len(thread.localRegisterSnapshots) {
		// 层级超过挂起时保存的 VM 栈。
		return false
	}
	if register >= len(thread.localRegisterSnapshots[snapshotIndex]) {
		// 寄存器越界时按未命中处理。
		return false
	}
	thread.localRegisterSnapshots[snapshotIndex][register] = value
	thread.localRegisterSnapshotsDirty = true
	return true
}

// HasLocalRegisterSnapshotUpdates 判断挂起局部寄存器快照是否被 debug.setlocal 修改。
//
// 返回 true 表示恢复 coroutine continuation 前需要把 localRegisterSnapshots 写回对应 VM；普通
// coroutine.yield 只保存快照供读取，不应每次恢复都覆盖 VM 当前循环控制寄存器。
func (thread *Thread) HasLocalRegisterSnapshotUpdates() bool {
	if thread == nil {
		// nil 线程没有可写回快照。
		return false
	}
	return thread.localRegisterSnapshotsDirty
}

// activeVMRegisterSnapshots 复制 State 当前活动 Lua VM 的寄存器窗口。
//
// 返回顺序与 debug level 对齐：第一个元素是当前最内层 Lua VM，后续依次向外。该方法仅在
// runtime 包内部用于 coroutine yield 快照，避免 debug 包直接接触 activeVMs。
func (state *State) activeVMRegisterSnapshots() [][]Value {
	if state == nil || len(state.activeVMs) == 0 {
		// 没有活动 VM 时没有局部寄存器可保存。
		return nil
	}
	snapshots := make([][]Value, 0, len(state.activeVMs))
	for vmIndex := len(state.activeVMs) - 1; vmIndex >= 0; vmIndex-- {
		vm := state.activeVMs[vmIndex]
		if vm == nil {
			// 空槽位不对应可见 Lua debug level。
			continue
		}
		snapshots = append(snapshots, vm.RegistersSnapshot())
	}
	return snapshots
}
