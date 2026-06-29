package runtime

import (
	"errors"
	"testing"
)

// TestThreadCreateStateAndStatus 验证 NewThread 只创建挂载状态的协程，并返回 suspended 状态。
//
// 协程初始状态用于匹配 Lua `coroutine.create` 返回值的标准观察点。
func TestThreadCreateStateAndStatus(t *testing.T) {
	state := NewState()
	thread := state.NewThread(ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
		// create 时不执行函数，返回值仅用于后续 resume 的可执行路径。
		return values, nil
	})))
	if thread == nil {
		// NewThread 失败表示 state 绑定或生命周期检查有问题。
		t.Fatalf("new thread should not be nil")
	}
	if thread.state != state {
		// 协程必须与当前 State 绑定，否则状态机无法恢复/暂停。
		t.Fatalf("thread state mismatch: got %#v want %#v", thread.state, state)
	}
	if thread.Status() != CoroutineStatusSuspended {
		// 新建协程默认可恢复，状态应为 suspended。
		t.Fatalf("new thread status mismatch: got %s", thread.Status())
	}
}

// TestThreadStatusReflectsMainThreadRunning 验证主线程总是返回 running 状态。
//
// 主线程不允许被销毁后随意切换状态，状态查询应稳定返回 running。
func TestThreadStatusReflectsMainThreadRunning(t *testing.T) {
	state := NewState()
	if got := state.mainThread.Status(); got != CoroutineStatusRunning {
		// 主线程创建后固定为 running。
		t.Fatalf("main thread status mismatch: got %s", got)
	}
}

// TestThreadResumeExecutesGoClosure 验证 Resume 能执行 Go 闭包并进入 dead。
//
// 当前阶段只支持 Go closure 直接调用，返回值和错误会原样透出。
func TestThreadResumeExecutesGoClosure(t *testing.T) {
	state := NewState()
	thread := state.NewThread(ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
		// 回传参数作为结果，验证 resume 将实参传给入口并读取返回。
		return []Value{IntegerValue(11), values[0]}, nil
	})))

	results, err := thread.Resume(IntegerValue(11))
	if err != nil {
		// 可调用 Go closure 的协程 resume 应无错误返回。
		t.Fatalf("resume returned unexpected error: %v", err)
	}
	if thread.Status() != CoroutineStatusDead {
		// 单次 resume 未涉及 yield 的协程应返回 dead。
		t.Fatalf("thread status should be dead after resume: %s", thread.Status())
	}
	if len(results) != 2 || !results[0].RawEqual(IntegerValue(11)) || !results[1].RawEqual(IntegerValue(11)) {
		// Go closure 返回值必须完整回传给调用方。
		t.Fatalf("resume result mismatch: %#v", results)
	}
}

// TestThreadResumeRejectsMainThread 验证主线程 resume 返回明确错误。
//
// Lua 协程库语义下 resume(mainthread) 不允许，避免对主执行线程产生重入破坏。
func TestThreadResumeRejectsMainThread(t *testing.T) {
	state := NewState()

	if _, err := state.mainThread.Resume(); !errors.Is(err, ErrMainThreadResume) {
		// 直接 resume 主线程应被拒绝。
		t.Fatalf("expected main thread resume error: got %v", err)
	}
}

// TestThreadYieldAndResumeTransfersValues 验证 yield 只允许 running 协程，并把值返回给下一次 resume。
//
// 该测试构造 running->suspended->yield 回传值路径，验证状态和返回值闭环。
func TestThreadYieldAndResumeTransfersValues(t *testing.T) {
	state := NewState()
	thread := state.NewThread(ReferenceValue(KindLuaClosure, "lua-function"))
	thread.status = CoroutineStatusRunning
	thread.state.runningThread = thread

	if err := thread.Yield(IntegerValue(7), IntegerValue(8)); err != nil {
		// running 协程 yield 应成功并挂起。
		t.Fatalf("yield failed: %v", err)
	}
	if thread.Status() != CoroutineStatusSuspended {
		// yield 后协程进入 suspended，下一次 resume 可接着返回数据。
		t.Fatalf("thread status should be suspended after yield: %s", thread.Status())
	}

	values, err := thread.Resume()
	if err != nil {
		// resume 应返回上次 yield 数据。
		t.Fatalf("resume after yield failed: %v", err)
	}
	if thread.Status() != CoroutineStatusNormal {
		// 在当前实现中，yield 恢复后的 thread 进入 normal。
		t.Fatalf("thread status should be normal after yield resume: %s", thread.Status())
	}
	if len(values) != 2 || !values[0].RawEqual(IntegerValue(7)) || !values[1].RawEqual(IntegerValue(8)) {
		// resume 在 yield 之后应将 yield 参数返回给调用方。
		t.Fatalf("yield resume values mismatch: %#v", values)
	}
}

// TestThreadYieldFromWrongState 返回错误。
//
// suspended 或 dead 的协程不能直接 yield，必须先进入 running。
func TestThreadYieldFromWrongState(t *testing.T) {
	state := NewState()
	thread := state.NewThread(NilValue())

	if err := thread.Yield(IntegerValue(1)); !errors.Is(err, ErrYieldFromNonRunning) {
		// 非 running 状态 yield 违反执行上下文约束。
		t.Fatalf("expected yield state error: got %v", err)
	}
}

// TestMainThreadYieldForbidsYielding 验证主线程不能直接 yield。
//
// 该分支与 coroutine running 行为一致：yield 只在非 main thread 协程中允许。
func TestMainThreadYieldForbidsYielding(t *testing.T) {
	state := NewState()
	if err := state.mainThread.Yield(); !errors.Is(err, ErrYieldFromMainThread) {
		// main thread yield 必须显式失败。
		t.Fatalf("expected main thread yield error: got %v", err)
	}
}

// TestStateRunningThread 验证 Running API 返回当前运行协程和主线程标记。
//
// 主线程初始状态应为 running 且 isMain 标记为 true。
func TestStateRunningThread(t *testing.T) {
	state := NewState()

	running, isMain := state.Running()
	if running == nil {
		// 新建 State 必须有可追踪的运行协程。
		t.Fatalf("running thread should not be nil")
	}
	if running != state.mainThread {
		// 返回对象应等于 state 主线程对象。
		t.Fatalf("running thread mismatch: got %#v want %#v", running, state.mainThread)
	}
	if !isMain {
		// 主线程场景应返回 isMain true。
		t.Fatalf("main thread should be marked as main")
	}
}

// TestThreadWrapCallsResume 验证 Wrap 创建的闭包可直接驱动同一协程 resume。
//
// 与 coroutine.wrap 对齐，wrap 后调用应返回 resume 的返回值。
func TestThreadWrapCallsResume(t *testing.T) {
	state := NewState()
	wrapped := state.Wrap(ReferenceValue(KindGoClosure, GoResultsFunction(func(args ...Value) ([]Value, error) {
		// 将首参回传，验证 wrap 和 thread.Resume 参数传递。
		return args, nil
	})))

	results, err := callGoClosureResults(wrapped, IntegerValue(11))
	if err != nil {
		// wrap 的底层调用应当像 resume 一样透传 Go closure 错误。
		t.Fatalf("wrap call should succeed: %v", err)
	}
	if len(results) != 1 || !results[0].RawEqual(IntegerValue(11)) {
		// 返回值应包含原始参数，表示调用成功。
		t.Fatalf("wrap result mismatch: %#v", results)
	}
}

// TestThreadYieldFromGoCallbackBoundary 验证 Go 回调边界内禁止 yield。
//
// 这条规则用于对齐 coroutine 在 Go/Lua 嵌套回调中的边界行为。
func TestThreadYieldFromGoCallbackBoundary(t *testing.T) {
	state := NewState()
	thread := state.NewThread(ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
		return values, nil
	})))
	thread.status = CoroutineStatusRunning
	thread.state.runningThread = thread

	state.enterGoCallback()
	defer state.exitGoCallback()

	if err := thread.Yield(); !errors.Is(err, ErrYieldFromGoCallback) {
		// Go 回调期间 yield 应返回明确边界错误。
		t.Fatalf("expected go callback yield error: got %v", err)
	}
}

// TestThreadResumeErrorPropagatesToErrorField 验证 resume 错误会记录到 coroutine error。
//
// 该行为用于后续 coroutine.error 查询能力。
func TestThreadResumeErrorPropagatesToErrorField(t *testing.T) {
	state := NewState()
	expected := errors.New("resume failed")
	thread := state.NewThread(ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
		// 触发一次可预测错误，便于断言 ErrorObject 写入。
		return nil, expected
	})))

	if _, err := thread.Resume(); !errors.Is(err, expected) {
		// resume 调用应把下层错误透传。
		t.Fatalf("expected wrapped go error: got %v", err)
	}
	if !thread.Error().RawEqual(ErrorObject(expected)) {
		// Error() 应返回与 Lua 传播兼容的错误对象。
		t.Fatalf("thread error mismatch: %#v", thread.Error())
	}

	if _, err := thread.Resume(); !errors.Is(err, ErrDeadThread) {
		// 已出错协程应进入 dead，不再可恢复。
		t.Fatalf("expected dead thread error: got %v", err)
	}
}
