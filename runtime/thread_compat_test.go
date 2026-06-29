package runtime

import (
	"errors"
	"testing"
)

// emulateLuaResume 模拟 Lua `coroutine.resume` 的返回形态。
//
// runtime API 返回 (values, error)，Lua API 返回 (true, values...) 或 (false, errObject...)；
// 为保持兼容性测试可读性，这里统一转换为 bool+values+err 的形态。
func emulateLuaResume(thread *Thread, args ...Value) (bool, []Value, error) {
	// 每次调用都复用 runtime.Resume 的行为，并把错误映射为 Lua bool+err 语义。
	results, resumeErr := thread.Resume(args...)
	if resumeErr != nil {
		// 调用失败时，Lua 风格返回 false 与错误对象。
		return false, []Value{ErrorObject(resumeErr)}, resumeErr
	}

	// 调用成功时，Lua 风格返回 true 与返回值列表。
	return true, results, nil
}

// TestCoroutineOfficialCompatibilitySuite 建立 Coroutine 官方行为向量的最小兼容测试。
//
// 覆盖点来源于 Lua 5.3 手册：coroutine.create/resume/wrap/running/status/error。
// 若后续实现更多 coroutine API，可继续在此添加官方脚本向量。
func TestCoroutineOfficialCompatibilitySuite(t *testing.T) {
	// case1: coroutine.create 后的默认状态应为 suspended。
	// 对应 Lua 中新建协程可立即被 resume 的状态观察。
	t.Run("coroutine.create_default_status", func(t *testing.T) {
		state := NewState()
		thread := state.NewThread(ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
			// create 阶段只记录入口，不执行逻辑，返回值在 resume 时验证。
			return values, nil
		})))
		if thread.Status() != CoroutineStatusSuspended {
			// 非 suspended 会导致 resume 调度前置判断偏离官方协程规范。
			t.Fatalf("expected suspended, got %s", thread.Status())
		}
	})

	// case2: coroutine.resume 成功时应返回 true + 返回值列表。
	// 这与 runtime.Resume 的签名不同，测试层通过 emulateLuaResume 对齐验证行为。
	t.Run("coroutine.resume_success_path", func(t *testing.T) {
		state := NewState()
		thread := state.NewThread(ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
			// 成功场景返回多个值：调用参数 + 固定补充值。
			return append([]Value{IntegerValue(1), IntegerValue(2)}, values...), nil
		})))

		ok, results, err := emulateLuaResume(thread)
		if err != nil {
			// resume 无错误时 should keep ok true and propagate所有返回值。
			t.Fatalf("expected resume success, got err=%v", err)
		}
		if !ok {
			// ok=false 表示错误路径，应被映射为 false+errObject。
			t.Fatalf("expected ok=true, got false")
		}
		if thread.Status() != CoroutineStatusDead {
			// 完成运行后未返回 yielded 状态，线程应死（dead）。
			t.Fatalf("expected dead after successful go-closure coroutine, got %s", thread.Status())
		}
		if len(results) != 2 || !results[0].RawEqual(IntegerValue(1)) || !results[1].RawEqual(IntegerValue(2)) {
			// Lua 风格 resume 成功应透传多返回值。
			t.Fatalf("unexpected resume results: %#v", results)
		}
	})

	// case3: coroutine.resume 对不可调用入口返回 false + error object。
	// 这对应官方语义中的执行失败分支。
	t.Run("coroutine.resume_non_callable_entry", func(t *testing.T) {
		state := NewState()
		thread := state.NewThread(NilValue())
		ok, results, err := emulateLuaResume(thread)
		if err == nil {
			// 非可调用入口应失败，并返回明确错误。
			t.Fatalf("expected resume error, got nil")
		}
		if ok {
			// ok=false 时才与 Lua 约定一致。
			t.Fatalf("expected ok=false, got true")
		}
		if len(results) != 1 || !results[0].RawEqual(ErrorObject(ErrNonCallableThread)) {
			// 结果列表首项应是标准化后的 Lua 错误对象。
			t.Fatalf("unexpected resume error payload: %#v", results)
		}
		if !errors.Is(err, ErrNonCallableThread) {
			// 协程错误对象应可通过 Go error 语义追踪到根错误。
			t.Fatalf("expected ErrNonCallableThread, got %v", err)
		}
		if thread.Error().RawEqual(NilValue()) {
			// 失败后 coroutine.error 等价值应可读到最近一次错误对象。
			t.Fatalf("expected thread last error to be set")
		}
	})

	// case4: coroutine.running 行为映射。
	// 主线程 running 标记为 true，普通线程在非运行态返回 false。
	t.Run("coroutine.running_behavior", func(t *testing.T) {
		state := NewState()
		runningThread, isMain := state.Running()
		if !isMain {
			// 初始运行态必须是主线程。
			t.Fatalf("expected main thread as running thread")
		}
		if runningThread != state.mainThread {
			// running 与 state.mainThread 引用应一致。
			t.Fatalf("unexpected running thread: got %p want %p", runningThread, state.mainThread)
		}

		thread := state.NewThread(ReferenceValue(KindGoClosure, GoResultsFunction(func(values ...Value) ([]Value, error) {
			return values, nil
		})))
		thread.status = CoroutineStatusRunning
		thread.state.runningThread = thread
		runningThread, isMain = state.Running()
		if isMain {
			// user 协程运行时不应被标记为主线程。
			t.Fatalf("non-main thread should not be marked as main")
		}
		if runningThread != thread {
			// running 返回值应切换为当前运行协程。
			t.Fatalf("expected running to point to current user thread")
		}
	})

	// case5: coroutine.wrap 后包装调用继续透传 resume 错误。
	// 该语义在 Lua 中表现为 wrapper 抛出错误，Go 侧等价为返回 error。
	t.Run("coroutine.wrap_error_path", func(t *testing.T) {
		state := NewState()
		expected := errors.New("wrap failure")
		goClosure := GoResultsFunction(func(values ...Value) ([]Value, error) {
			return nil, expected
		})
		wrapped := state.Wrap(ReferenceValue(KindGoClosure, goClosure))
		_, err := callGoClosureResults(wrapped)
		if !errors.Is(err, expected) {
			// 包装调用应透传底层 resume 错误。
			t.Fatalf("expected wrapped error propagation, got %v", err)
		}
	})
}
