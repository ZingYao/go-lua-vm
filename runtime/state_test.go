package runtime

import (
	"context"
	"errors"
	"testing"
)

// TestTableRawStringAccess 验证 table string key 的 raw get/set 行为。
//
// 当前 Table 阶段只承诺 raw 访问，不触发元方法。
func TestTableRawStringAccess(t *testing.T) {
	table := NewTable()
	table.RawSetString("name", StringValue("lua"))

	// 已写入 key 必须读取到原值。
	if got := table.RawGetString("name"); !got.RawEqual(StringValue("lua")) {
		t.Fatalf("string table value mismatch: got %#v", got)
	}

	table.RawSetString("name", NilValue())
	// 写入 nil 必须删除 key，后续读取返回 nil。
	if got := table.RawGetString("name"); !got.IsNil() {
		t.Fatalf("deleted string key should read nil: got %#v", got)
	}
}

// TestTableRawIntegerAccess 验证 table integer key 的 raw get/set 行为。
//
// 该能力用于 registry 预定义整数索引和后续数组区语义的最小基础。
func TestTableRawIntegerAccess(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(1, BooleanValue(true))

	// 已写入整数 key 必须读取到原值。
	if got := table.RawGetInteger(1); !got.RawEqual(BooleanValue(true)) {
		t.Fatalf("integer table value mismatch: got %#v", got)
	}

	// 未写入整数 key 必须读取 nil。
	if got := table.RawGetInteger(2); !got.IsNil() {
		t.Fatalf("missing integer key should read nil: got %#v", got)
	}
}

// TestNewStateInitializesRegistry 验证 State 初始化 registry 预定义槽位。
//
// Lua 5.3 registry[1] 是主线程，registry[2] 是全局环境表。
func TestNewStateInitializesRegistry(t *testing.T) {
	state := NewState()

	mainThread := state.Registry().RawGetInteger(RegistryIndexMainThread)
	if mainThread.Kind != KindThread || mainThread.Ref != state.mainThread {
		// registry[1] 必须指向当前 State 的主线程占位。
		t.Fatalf("main thread registry mismatch: %#v", mainThread)
	}
	if got := state.MainThread(); !got.RawEqual(mainThread) {
		// MainThread 必须读取 registry 中的同一主线程占位值。
		t.Fatalf("main thread accessor mismatch: got %#v want %#v", got, mainThread)
	}

	globals := state.Registry().RawGetInteger(RegistryIndexGlobals)
	if globals.Kind != KindTable || globals.Ref != state.Globals() {
		// registry[2] 必须指向 globals table。
		t.Fatalf("globals registry mismatch: %#v", globals)
	}
}

// TestStateGlobals 验证全局环境 `_G` 表的基础读写。
//
// 当前阶段只测试 string key 的 raw 全局变量访问。
func TestStateGlobals(t *testing.T) {
	state := NewState()
	state.SetGlobal("answer", IntegerValue(42))

	// GetGlobal 必须从 globals 表中读取先前写入的值。
	if got := state.GetGlobal("answer"); !got.RawEqual(IntegerValue(42)) {
		t.Fatalf("global value mismatch: got %#v", got)
	}

	state.SetGlobal("answer", NilValue())
	// 全局变量赋 nil 后必须删除。
	if got := state.GetGlobal("answer"); !got.IsNil() {
		t.Fatalf("deleted global should read nil: got %#v", got)
	}
}

// TestStateCloseReleasesRoots 验证 Close 会释放 State root 引用。
//
// 当前阶段 Close 只负责生命周期标记和 root 清理，后续 GC/userdata 会继续扩展关闭流程。
func TestStateCloseReleasesRoots(t *testing.T) {
	state := NewState()
	closeHookCalls := 0
	state.AddCloseHook(func() {
		// 关闭钩子执行期间保持既有的未正式关闭语义，同时必须防止 Close 重入。
		if state.IsClosed() {
			// closed 只能在 finalizer 和根引用清理完成后可见。
			t.Fatalf("state should not be marked closed while close hook is running")
		}
		state.Close()
		closeHookCalls++
	})
	if state.IsClosed() {
		// 新建 State 必须处于可用状态。
		t.Fatalf("new state should be open")
	}

	state.Close()
	if !state.IsClosed() {
		// Close 后生命周期标记必须变为已关闭。
		t.Fatalf("state should be closed")
	}
	if state.Registry() != nil {
		// Close 后 registry root 必须释放。
		t.Fatalf("closed state registry should be nil")
	}
	if state.Globals() != nil {
		// Close 后 globals root 必须释放。
		t.Fatalf("closed state globals should be nil")
	}
	if closeHookCalls != 1 {
		// 首次 Close 必须执行已登记的资源释放钩子。
		t.Fatalf("close hook calls mismatch: got %d want 1", closeHookCalls)
	}

	state.Close()
	if !state.IsClosed() {
		// 重复 Close 必须保持已关闭状态且无副作用。
		t.Fatalf("state should remain closed after second close")
	}
	if closeHookCalls != 1 {
		// 重复 Close 不得重复执行关闭钩子。
		t.Fatalf("close hook should remain single-shot: got %d", closeHookCalls)
	}
}

// TestStateAddCloseHookAfterClose 验证已关闭 State 不会继续持有新钩子。
//
// 入参由测试构造；AddCloseHook 没有返回值，已关闭状态下必须立即执行回调且不影响重复 Close。
func TestStateAddCloseHookAfterClose(t *testing.T) {
	// 先关闭 State，再登记钩子，覆盖延迟清理调用方的兜底路径。
	state := NewState()
	state.Close()
	calls := 0
	state.AddCloseHook(func() {
		// 记录关闭后登记的钩子是否立即执行。
		calls++
	})
	if calls != 1 {
		// 已关闭 State 必须立即释放调用方资源。
		t.Fatalf("late close hook calls mismatch: got %d want 1", calls)
	}
}

// TestStateGlobalsAfterClose 验证关闭后全局读写不会访问已释放 root。
//
// 当前内部 runtime API 选择关闭后写入忽略、读取返回 nil，公开 lua 包后续会返回明确错误。
func TestStateGlobalsAfterClose(t *testing.T) {
	state := NewState()
	state.SetGlobal("answer", IntegerValue(42))
	state.Close()

	state.SetGlobal("answer", IntegerValue(100))
	if got := state.GetGlobal("answer"); !got.IsNil() {
		// 关闭后读取全局变量应表现为缺失，避免访问已释放 globals。
		t.Fatalf("closed state global should read nil: got %#v", got)
	}
}

// TestStateMainThreadAfterClose 验证关闭后主线程占位不再暴露。
//
// Close 释放 registry root 后，MainThread 应返回 nil，避免保留已关闭 State 引用。
func TestStateMainThreadAfterClose(t *testing.T) {
	state := NewState()
	state.Close()

	if got := state.MainThread(); !got.IsNil() {
		// 关闭后的 State 不应继续暴露主线程占位。
		t.Fatalf("closed state main thread should be nil: %#v", got)
	}
}

// TestStateContextDefaultAndSet 验证 State 默认上下文与宿主上下文绑定。
//
// 默认上下文应可直接通过检查点；宿主绑定的新上下文应在 Context 中原样可见。
func TestStateContextDefaultAndSet(t *testing.T) {
	state := NewState()
	if state.Context() == nil {
		// 新建 State 必须带有默认 context，避免 VM 检查点出现 nil。
		t.Fatalf("new state context should not be nil")
	}
	if err := state.CheckContext(); err != nil {
		// 默认 background context 不应产生取消错误。
		t.Fatalf("default context check failed: %v", err)
	}

	ctx := context.Background()
	if err := state.SetContext(ctx); err != nil {
		// 合法 context 绑定必须成功。
		t.Fatalf("set context failed: %v", err)
	}
	if state.Context() != ctx {
		// Context 必须返回宿主刚绑定的同一个上下文。
		t.Fatalf("state context mismatch")
	}
}

// TestStateCheckContextCanceled 验证取消上下文会转换为可传播错误对象。
//
// VM 指令循环后续会在长循环边界调用该检查点，及时响应宿主取消信号。
func TestStateCheckContextCanceled(t *testing.T) {
	state := NewState()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := state.SetContext(ctx); err != nil {
		// 已取消的 context 仍允许绑定，取消状态由 CheckContext 统一报告。
		t.Fatalf("set canceled context failed: %v", err)
	}

	err := state.CheckContext()
	if !errors.Is(err, context.Canceled) {
		// 取消错误必须保留 context.Canceled 错误链，便于宿主识别。
		t.Fatalf("context canceled error mismatch: %v", err)
	}
	if got := ErrorObject(err); !got.RawEqual(StringValue(context.Canceled.Error())) {
		// 取消错误必须转换为 Lua string error object，供 protected call 继续传播。
		t.Fatalf("context canceled object mismatch: %#v", got)
	}
}

// TestStateInterruptRequestConsumed 验证宿主中断请求会转换为一次性 Lua 错误。
//
// Ctrl-C 兼容路径需要让 pcall 捕获 `interrupted!` 后继续执行，因此中断不能像 context cancel
// 一样永久污染后续检查点。
func TestStateInterruptRequestConsumed(t *testing.T) {
	state := NewState()
	state.RequestInterrupt()

	err := state.CheckContext()
	if !errors.Is(err, ErrInterrupted) {
		// 第一次检查必须消费中断并保留 ErrInterrupted 错误链。
		t.Fatalf("interrupt error mismatch: %v", err)
	}
	if got := ErrorObject(err); !got.RawEqual(StringValue("interrupted!")) {
		// Lua 侧错误对象必须对齐官方 Ctrl-C 文本。
		t.Fatalf("interrupt object mismatch: %#v", got)
	}
	if err := state.CheckContext(); err != nil {
		// 中断只消费一次，后续检查点应允许脚本继续运行。
		t.Fatalf("interrupt should be consumed, got %v", err)
	}
}

// TestStateContextInvalidState 验证 nil context 和关闭 State 的错误语义。
//
// 这两类错误都发生在 VM 执行前，不能静默降级为 background context。
func TestStateContextInvalidState(t *testing.T) {
	state := NewState()
	var nilContext context.Context
	if err := state.SetContext(nilContext); !errors.Is(err, ErrNilContext) {
		// nil context 必须返回 ErrNilContext。
		t.Fatalf("nil context error mismatch: %v", err)
	}

	state.Close()
	if state.Context() != nil {
		// 关闭后的 State 应释放 context 引用。
		t.Fatalf("closed state context should be nil")
	}
	if err := state.SetContext(context.Background()); !errors.Is(err, ErrClosedState) {
		// 关闭后绑定 context 必须返回 ErrClosedState。
		t.Fatalf("closed state set context error mismatch: %v", err)
	}
	if err := state.CheckContext(); !errors.Is(err, ErrClosedState) {
		// 关闭后检查 context 必须返回 ErrClosedState。
		t.Fatalf("closed state check context error mismatch: %v", err)
	}
}

// TestStateStackPushPop 验证 State 栈的基础 LIFO 行为。
//
// 当前阶段只覆盖主线程线性栈，调用帧和索引体系会在后续任务中补齐。
func TestStateStackPushPop(t *testing.T) {
	state := NewState()
	if state.StackTop() != 0 {
		// 新建 State 栈必须为空。
		t.Fatalf("new state stack top mismatch: got %d", state.StackTop())
	}

	if err := state.Push(IntegerValue(1)); err != nil {
		// 第一次压栈必须成功。
		t.Fatalf("push first value failed: %v", err)
	}
	if err := state.Push(StringValue("top")); err != nil {
		// 第二次压栈必须成功。
		t.Fatalf("push second value failed: %v", err)
	}
	if state.StackTop() != 2 {
		// 两次压栈后栈顶位置必须为 2。
		t.Fatalf("stack top after push mismatch: got %d", state.StackTop())
	}

	value, err := state.Pop()
	if err != nil || !value.RawEqual(StringValue("top")) {
		// 第一次弹栈必须返回最后压入的值。
		t.Fatalf("first pop mismatch: value=%#v err=%v", value, err)
	}
	value, err = state.Pop()
	if err != nil || !value.RawEqual(IntegerValue(1)) {
		// 第二次弹栈必须返回第一个压入的值。
		t.Fatalf("second pop mismatch: value=%#v err=%v", value, err)
	}
	if state.StackTop() != 0 {
		// 全部弹出后栈顶位置必须归零。
		t.Fatalf("stack top after pop mismatch: got %d", state.StackTop())
	}
}

// TestStateStackPopUnderflow 验证空栈弹出会返回下溢错误。
//
// 空栈 Pop 不应 panic，也不应修改 State 生命周期状态。
func TestStateStackPopUnderflow(t *testing.T) {
	state := NewState()

	value, err := state.Pop()
	if !value.IsNil() || !errors.Is(err, ErrStackUnderflow) {
		// 空栈弹出必须返回 nil 和 ErrStackUnderflow。
		t.Fatalf("stack underflow mismatch: value=%#v err=%v", value, err)
	}
	if state.IsClosed() {
		// 下溢错误不应关闭 State。
		t.Fatalf("stack underflow should not close state")
	}
}

// TestStateStackOverflow 验证 Push 会遵守 MaxStackDepth。
//
// 超过栈深度限制时必须返回资源限制错误，并保持原栈不变。
func TestStateStackOverflow(t *testing.T) {
	state := NewStateWithOptions(Options{MaxStackDepth: 1})
	if err := state.Push(IntegerValue(1)); err != nil {
		// 第一项在限制内，应压栈成功。
		t.Fatalf("push within stack limit failed: %v", err)
	}

	err := state.Push(IntegerValue(2))
	if err == nil || err.Error() != "stack overflow" {
		// 第二项超过 MaxStackDepth，必须返回 stack overflow。
		t.Fatalf("stack overflow error mismatch: %v", err)
	}
	if state.StackTop() != 1 {
		// 溢出失败后原栈深度必须保持不变。
		t.Fatalf("stack top after overflow mismatch: got %d", state.StackTop())
	}
}

// TestStateStackAfterClose 验证关闭后的栈操作返回明确错误。
//
// Close 会释放栈 root，后续 Push/Pop 不应重新创建或访问栈。
func TestStateStackAfterClose(t *testing.T) {
	state := NewState()
	if err := state.Push(IntegerValue(1)); err != nil {
		// 关闭前压栈必须成功。
		t.Fatalf("push before close failed: %v", err)
	}
	state.Close()

	if state.StackTop() != 0 {
		// 关闭后栈顶必须归零。
		t.Fatalf("closed state stack top mismatch: got %d", state.StackTop())
	}
	if err := state.Push(IntegerValue(2)); !errors.Is(err, ErrClosedState) {
		// 关闭后 Push 必须返回 ErrClosedState。
		t.Fatalf("closed state push error mismatch: %v", err)
	}
	value, err := state.Pop()
	if !value.IsNil() || !errors.Is(err, ErrClosedState) {
		// 关闭后 Pop 必须返回 ErrClosedState。
		t.Fatalf("closed state pop mismatch: value=%#v err=%v", value, err)
	}
}

// TestStateAbsIndex 验证 Lua 栈绝对索引转换。
//
// 正索引保持不变，负索引从栈顶向下换算，0 是无效索引。
func TestStateAbsIndex(t *testing.T) {
	state := NewState()
	_ = state.Push(StringValue("bottom"))
	_ = state.Push(StringValue("top"))

	if got := state.AbsIndex(1); got != 1 {
		// 正索引 1 必须保持为 1。
		t.Fatalf("positive abs index mismatch: got %d", got)
	}
	if got := state.AbsIndex(-1); got != 2 {
		// -1 必须指向当前栈顶，也就是绝对索引 2。
		t.Fatalf("top abs index mismatch: got %d", got)
	}
	if got := state.AbsIndex(-2); got != 1 {
		// -2 必须指向栈顶下方一格，也就是绝对索引 1。
		t.Fatalf("bottom abs index mismatch: got %d", got)
	}
	if got := state.AbsIndex(0); got != 0 {
		// 0 是无效 Lua 栈索引。
		t.Fatalf("zero abs index mismatch: got %d", got)
	}
	if got := state.AbsIndex(-3); got != 0 {
		// 超出当前栈深度的负索引会换算到 0，表示无效。
		t.Fatalf("out of range negative abs index mismatch: got %d", got)
	}
}

// TestStateValueAt 验证按正负索引读取栈值。
//
// ValueAt 不弹出栈值，越界或无效索引返回 nil。
func TestStateValueAt(t *testing.T) {
	state := NewState()
	_ = state.Push(StringValue("bottom"))
	_ = state.Push(StringValue("top"))

	if got := state.ValueAt(1); !got.RawEqual(StringValue("bottom")) {
		// 正索引 1 必须读取栈底第一个值。
		t.Fatalf("value at positive index mismatch: %#v", got)
	}
	if got := state.ValueAt(-1); !got.RawEqual(StringValue("top")) {
		// 负索引 -1 必须读取栈顶值。
		t.Fatalf("value at negative index mismatch: %#v", got)
	}
	if got := state.ValueAt(3); !got.IsNil() {
		// 超出栈顶的正索引返回 nil。
		t.Fatalf("out of range positive value should be nil: %#v", got)
	}
	if got := state.ValueAt(0); !got.IsNil() {
		// 0 是无效索引，返回 nil。
		t.Fatalf("zero index value should be nil: %#v", got)
	}
	if state.StackTop() != 2 {
		// ValueAt 只读取不弹栈，栈深度必须保持不变。
		t.Fatalf("value at should not pop stack: top=%d", state.StackTop())
	}
}

// TestStateIndexAfterClose 验证关闭后的索引读取行为。
//
// Close 释放栈 root 后，索引转换和读取都应表现为无效。
func TestStateIndexAfterClose(t *testing.T) {
	state := NewState()
	_ = state.Push(StringValue("value"))
	state.Close()

	if got := state.AbsIndex(1); got != 0 {
		// 关闭后的 State 任何索引都无效。
		t.Fatalf("closed abs index mismatch: got %d", got)
	}
	if got := state.ValueAt(1); !got.IsNil() {
		// 关闭后的 State 读取任何栈索引都返回 nil。
		t.Fatalf("closed value at should be nil: %#v", got)
	}
}

// TestStateRegistryPseudoIndex 验证 registry pseudo-index 的基础访问。
//
// Lua 5.3 LUA_REGISTRYINDEX 定义为 -LUAI_MAXSTACK - 1000，本项目用默认栈上限对齐。
func TestStateRegistryPseudoIndex(t *testing.T) {
	state := NewState()

	if RegistryPseudoIndex != -DefaultMaxStackDepth-1000 {
		// registry pseudo-index 常量必须按 Lua 5.3 公式计算。
		t.Fatalf("registry pseudo index mismatch: got %d", RegistryPseudoIndex)
	}
	if got := state.AbsIndex(RegistryPseudoIndex); got != RegistryPseudoIndex {
		// pseudo-index 不应被转换为普通栈绝对索引。
		t.Fatalf("registry pseudo abs index mismatch: got %d", got)
	}

	value := state.ValueAt(RegistryPseudoIndex)
	if value.Kind != KindTable || value.Ref != state.Registry() {
		// registry pseudo-index 必须读取 registry table 引用。
		t.Fatalf("registry pseudo value mismatch: %#v", value)
	}
}

// TestStateRegistryPseudoIndexAfterClose 验证关闭后 registry pseudo-index 不再暴露 root。
//
// Close 释放 registry root 后，ValueAt 必须返回 nil。
func TestStateRegistryPseudoIndexAfterClose(t *testing.T) {
	state := NewState()
	state.Close()

	if got := state.AbsIndex(RegistryPseudoIndex); got != 0 {
		// 关闭后的 State 不再保留 pseudo-index 访问能力。
		t.Fatalf("closed registry pseudo abs index mismatch: got %d", got)
	}
	if got := state.ValueAt(RegistryPseudoIndex); !got.IsNil() {
		// 关闭后 registry root 已释放，读取应返回 nil。
		t.Fatalf("closed registry pseudo value should be nil: %#v", got)
	}
}

// TestStateCallFramePushPop 验证调用帧栈的基础 LIFO 行为。
//
// 当前阶段只保存帧元数据，不执行 Lua 或 Go 函数。
func TestStateCallFramePushPop(t *testing.T) {
	state := NewState()
	frame := CallFrame{
		Kind:            CallFrameKindLua,
		Function:        ReferenceValue(KindLuaClosure, "proto"),
		Base:            1,
		ExpectedReturns: 1,
	}

	if err := state.PushCallFrame(frame); err != nil {
		// 第一帧压入必须成功。
		t.Fatalf("push call frame failed: %v", err)
	}
	if state.CallDepth() != 1 {
		// 压入一帧后调用深度必须为 1。
		t.Fatalf("call depth mismatch: got %d", state.CallDepth())
	}
	currentFrame, ok := state.CurrentCallFrame()
	if !ok || currentFrame != frame {
		// 当前帧必须是刚压入的帧。
		t.Fatalf("current call frame mismatch: frame=%#v ok=%v", currentFrame, ok)
	}

	poppedFrame, err := state.PopCallFrame()
	if err != nil || poppedFrame != frame {
		// 弹出帧必须返回原帧。
		t.Fatalf("pop call frame mismatch: frame=%#v err=%v", poppedFrame, err)
	}
	if state.CallDepth() != 0 {
		// 弹出后调用深度必须归零。
		t.Fatalf("call depth after pop mismatch: got %d", state.CallDepth())
	}
}

// TestNewLuaCallFrame 验证 Lua closure 调用帧构造。
//
// 当前构造函数只记录帧元数据，不执行 Proto 或修改寄存器。
func TestNewLuaCallFrame(t *testing.T) {
	function := ReferenceValue(KindLuaClosure, "proto")
	frame := NewLuaCallFrame(function, 2, -1)

	if frame.Kind != CallFrameKindLua {
		// Lua 调用帧 Kind 必须标记为 lua。
		t.Fatalf("lua call frame kind mismatch: %s", frame.Kind)
	}
	if !frame.Function.RawEqual(function) {
		// Lua 调用帧必须保留传入函数值。
		t.Fatalf("lua call frame function mismatch: %#v", frame.Function)
	}
	if frame.Base != 2 {
		// Lua 调用帧必须保留 1-based 栈基址。
		t.Fatalf("lua call frame base mismatch: %d", frame.Base)
	}
	if frame.ExpectedReturns != -1 {
		// -1 表示多返回值，必须原样保留。
		t.Fatalf("lua call frame returns mismatch: %d", frame.ExpectedReturns)
	}

	state := NewState()
	if err := state.PushCallFrame(frame); err != nil {
		// 构造出的 Lua 调用帧必须能压入 State 调用帧栈。
		t.Fatalf("push lua call frame failed: %v", err)
	}
	currentFrame, ok := state.CurrentCallFrame()
	if !ok || currentFrame != frame {
		// 当前调用帧必须等于刚压入的 Lua 调用帧。
		t.Fatalf("current lua call frame mismatch: frame=%#v ok=%v", currentFrame, ok)
	}
}

// TestNewGoCallFrame 验证 Go closure 调用帧构造。
//
// 当前构造函数只记录帧元数据，不执行 Go 函数体或处理 Go/Lua 边界错误。
func TestNewGoCallFrame(t *testing.T) {
	function := ReferenceValue(KindGoClosure, "go")
	frame := NewGoCallFrame(function, 3, 2)

	if frame.Kind != CallFrameKindGo {
		// Go 调用帧 Kind 必须标记为 go。
		t.Fatalf("go call frame kind mismatch: %s", frame.Kind)
	}
	if !frame.Function.RawEqual(function) {
		// Go 调用帧必须保留传入函数值，后续 bridge 执行器依赖该值定位宿主函数。
		t.Fatalf("go call frame function mismatch: %#v", frame.Function)
	}
	if frame.Base != 3 {
		// Go 调用帧必须保留 1-based 栈基址，便于后续读取参数窗口。
		t.Fatalf("go call frame base mismatch: %d", frame.Base)
	}
	if frame.ExpectedReturns != 2 {
		// 固定返回值数量必须原样保留，后续调用收尾阶段会按该数量调整栈。
		t.Fatalf("go call frame returns mismatch: %d", frame.ExpectedReturns)
	}

	state := NewState()
	if err := state.PushCallFrame(frame); err != nil {
		// 构造出的 Go 调用帧必须能压入 State 调用帧栈。
		t.Fatalf("push go call frame failed: %v", err)
	}
	currentFrame, ok := state.CurrentCallFrame()
	if !ok || currentFrame != frame {
		// 当前调用帧必须等于刚压入的 Go 调用帧。
		t.Fatalf("current go call frame mismatch: frame=%#v ok=%v", currentFrame, ok)
	}
}

// TestStateReplaceCurrentCallFrame 验证尾调用帧替换不增加调用深度。
//
// 当前阶段只验证帧元数据原地替换，寄存器窗口复用和返回值收尾留给 VM 执行器实现。
func TestStateReplaceCurrentCallFrame(t *testing.T) {
	state := NewState()
	originalFrame := NewLuaCallFrame(ReferenceValue(KindLuaClosure, "caller"), 1, 1)
	tailFrame := NewLuaCallFrame(ReferenceValue(KindLuaClosure, "callee"), 1, -1)

	if err := state.PushCallFrame(originalFrame); err != nil {
		// 初始调用帧必须能压入 State，后续才存在可替换的当前帧。
		t.Fatalf("push original call frame failed: %v", err)
	}
	if err := state.ReplaceCurrentCallFrame(tailFrame); err != nil {
		// 尾调用替换已有当前帧必须成功。
		t.Fatalf("replace current call frame failed: %v", err)
	}
	if state.CallDepth() != 1 {
		// 替换当前帧不得增加调用深度，这是 tail call 帧复用的核心约束。
		t.Fatalf("call depth after replace mismatch: got %d", state.CallDepth())
	}

	currentFrame, ok := state.CurrentCallFrame()
	if !ok || currentFrame != tailFrame {
		// 当前帧必须变为尾调用目标帧。
		t.Fatalf("current tail call frame mismatch: frame=%#v ok=%v", currentFrame, ok)
	}
}

// TestStateReplaceCurrentCallFrameUnderflow 验证空调用帧栈无法执行尾调用替换。
//
// 空帧栈替换代表 VM 调用状态不一致，应返回明确下溢错误而不是隐式压入新帧。
func TestStateReplaceCurrentCallFrameUnderflow(t *testing.T) {
	state := NewState()
	tailFrame := NewLuaCallFrame(ReferenceValue(KindLuaClosure, "callee"), 1, -1)

	if err := state.ReplaceCurrentCallFrame(tailFrame); !errors.Is(err, ErrCallFrameUnderflow) {
		// 没有当前帧时必须返回 ErrCallFrameUnderflow，避免 tail call 被误当成普通 call。
		t.Fatalf("replace empty call frame error mismatch: %v", err)
	}
	if state.CallDepth() != 0 {
		// 替换失败不能改变调用帧深度。
		t.Fatalf("call depth after failed replace mismatch: got %d", state.CallDepth())
	}
}

// TestStateReplaceCurrentCallFrameAfterClose 验证关闭后的尾调用替换返回明确错误。
//
// Close 会释放调用帧 root，替换操作不能重新创建或访问已释放帧栈。
func TestStateReplaceCurrentCallFrameAfterClose(t *testing.T) {
	state := NewState()
	originalFrame := NewLuaCallFrame(ReferenceValue(KindLuaClosure, "caller"), 1, 1)
	if err := state.PushCallFrame(originalFrame); err != nil {
		// 关闭前压入调用帧必须成功，确保测试覆盖的是关闭后的替换行为。
		t.Fatalf("push call frame before close failed: %v", err)
	}
	state.Close()

	tailFrame := NewLuaCallFrame(ReferenceValue(KindLuaClosure, "callee"), 1, -1)
	if err := state.ReplaceCurrentCallFrame(tailFrame); !errors.Is(err, ErrClosedState) {
		// 关闭后替换当前帧必须返回 ErrClosedState。
		t.Fatalf("closed state replace call frame error mismatch: %v", err)
	}
	if state.CallDepth() != 0 {
		// 关闭后的调用帧深度必须保持为 0。
		t.Fatalf("closed state call depth after replace mismatch: got %d", state.CallDepth())
	}
}

// TestStateTracebackFrames 验证 traceback 调用帧快照顺序与隔离性。
//
// 当前阶段只收集调用帧元数据；源码行号、tail call 标记和局部变量留给 debug 模块补充。
func TestStateTracebackFrames(t *testing.T) {
	state := NewState()
	firstFrame := NewLuaCallFrame(ReferenceValue(KindLuaClosure, "first"), 1, 1)
	secondFrame := NewGoCallFrame(ReferenceValue(KindGoClosure, "second"), 2, 0)
	if err := state.PushCallFrame(firstFrame); err != nil {
		// 第一帧必须压入成功，作为 traceback 的最早帧。
		t.Fatalf("push first traceback frame failed: %v", err)
	}
	if err := state.PushCallFrame(secondFrame); err != nil {
		// 第二帧必须压入成功，作为 traceback 的当前帧。
		t.Fatalf("push second traceback frame failed: %v", err)
	}

	frames := state.TracebackFrames()
	if len(frames) != 2 {
		// traceback 快照必须包含当前全部调用帧。
		t.Fatalf("traceback frame count mismatch: got %d", len(frames))
	}
	if frames[0] != secondFrame || frames[1] != firstFrame {
		// traceback 顺序必须从当前帧向外层调用帧展开。
		t.Fatalf("traceback frame order mismatch: %#v", frames)
	}

	frames[0] = firstFrame
	currentFrame, ok := state.CurrentCallFrame()
	if !ok || currentFrame != secondFrame {
		// 修改返回快照不能影响 State 内部当前帧。
		t.Fatalf("traceback snapshot should not mutate state: frame=%#v ok=%v", currentFrame, ok)
	}
}

// TestStateActiveVMAtLevel 验证活动 VM 按 debug level 读取。
//
// level 1 必须对应最近压入的 VM，level 2 对应外层 VM；该顺序用于 debug.setlocal 写回外层
// 函数的 vararg 快照。
func TestStateActiveVMAtLevel(t *testing.T) {
	// 构造两个嵌套活动 VM。
	state := NewState()
	outerVM := NewVM(1)
	innerVM := NewVM(1)
	state.PushActiveVM(outerVM)
	state.PushActiveVM(innerVM)

	first, firstOK := state.ActiveVMAtLevel(1)
	second, secondOK := state.ActiveVMAtLevel(2)
	missing, missingOK := state.ActiveVMAtLevel(3)
	if !firstOK || first != innerVM || !secondOK || second != outerVM {
		// debug level 必须从当前 VM 向外层 VM 映射。
		t.Fatalf("active VM level mismatch: level1=%p/%v level2=%p/%v", first, firstOK, second, secondOK)
	}
	if missingOK || missing != nil {
		// 越界层级不应返回 VM。
		t.Fatalf("missing active VM = %p/%v", missing, missingOK)
	}
}

// TestStateTracebackFramesEmptyAndClosed 验证空调用栈和关闭 State 的 traceback 行为。
//
// 这两类场景都没有可收集帧，必须返回空切片而不是伪造帧。
func TestStateTracebackFramesEmptyAndClosed(t *testing.T) {
	state := NewState()
	if frames := state.TracebackFrames(); len(frames) != 0 {
		// 空调用帧栈不应返回 traceback 帧。
		t.Fatalf("empty traceback frames mismatch: %#v", frames)
	}

	if err := state.PushCallFrame(NewLuaCallFrame(ReferenceValue(KindLuaClosure, "frame"), 1, 1)); err != nil {
		// 关闭前压入调用帧必须成功，确保后续覆盖关闭释放场景。
		t.Fatalf("push frame before close failed: %v", err)
	}
	state.Close()
	if frames := state.TracebackFrames(); len(frames) != 0 {
		// 关闭后的 State 已释放调用帧 root，不应返回旧帧快照。
		t.Fatalf("closed traceback frames mismatch: %#v", frames)
	}
}

// TestStateProtectedCallSuccessKeepsChanges 验证保护调用成功时保留栈和调用帧变化。
//
// 成功路径代表被保护的 Lua/Go 调用正常返回，返回值和调用收尾状态不能被回滚。
func TestStateProtectedCallSuccessKeepsChanges(t *testing.T) {
	state := NewState()

	err := state.ProtectedCall(func(protectedState *State) error {
		if protectedState != state {
			// ProtectedCall 必须把原 State 传入回调，避免回调误操作其他 VM 实例。
			t.Fatalf("protected call state mismatch")
		}
		if pushErr := protectedState.Push(StringValue("result")); pushErr != nil {
			// 成功路径测试依赖压栈成功，失败时直接返回错误给 ProtectedCall。
			return pushErr
		}
		return protectedState.PushCallFrame(NewLuaCallFrame(ReferenceValue(KindLuaClosure, "callee"), 1, 1))
	})
	if err != nil {
		// 回调没有返回错误，也没有 panic，ProtectedCall 必须返回 nil。
		t.Fatalf("protected call success failed: %v", err)
	}
	if got := state.ValueAt(-1); !got.RawEqual(StringValue("result")) {
		// 成功调用产生的栈顶结果必须保留。
		t.Fatalf("protected call result mismatch: %#v", got)
	}
	if state.CallDepth() != 1 {
		// 成功调用产生的调用帧必须保留，后续 VM 收尾逻辑会处理弹帧。
		t.Fatalf("protected call depth mismatch: got %d", state.CallDepth())
	}
}

// TestStateProtectedCallErrorRestoresBoundary 验证保护调用返回错误时回滚边界。
//
// 错误路径应清理回调期间新增的栈槽和调用帧，并原样返回回调错误。
func TestStateProtectedCallErrorRestoresBoundary(t *testing.T) {
	state := NewState()
	_ = state.Push(StringValue("base"))
	expectedErr := errors.New("runtime error")

	err := state.ProtectedCall(func(protectedState *State) error {
		if pushErr := protectedState.Push(StringValue("temporary")); pushErr != nil {
			// 压栈失败时返回原错误，避免测试继续建立错误边界。
			return pushErr
		}
		if frameErr := protectedState.PushCallFrame(NewLuaCallFrame(ReferenceValue(KindLuaClosure, "callee"), 2, 1)); frameErr != nil {
			// 压入调用帧失败时返回原错误，保护调用应同样回滚边界。
			return frameErr
		}
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		// ProtectedCall 必须原样返回回调错误，便于上层做 errors.Is/As 判断。
		t.Fatalf("protected call error mismatch: %v", err)
	}
	if got := ErrorObject(err); !got.RawEqual(StringValue("runtime error")) {
		// 普通 Go error 必须转换为 Lua string error object，供 pcall/xpcall 后续压栈。
		t.Fatalf("protected call error object mismatch: %#v", got)
	}
	if state.StackTop() != 1 || !state.ValueAt(-1).RawEqual(StringValue("base")) {
		// 错误路径必须回滚新增栈槽，只保留进入前的栈内容。
		t.Fatalf("protected call stack rollback mismatch: top=%d value=%#v", state.StackTop(), state.ValueAt(-1))
	}
	if state.CallDepth() != 0 {
		// 错误路径必须回滚新增调用帧。
		t.Fatalf("protected call frame rollback mismatch: got %d", state.CallDepth())
	}
}

// TestStateProtectedCallPanicRestoresBoundary 验证保护调用会把 panic 转为错误并回滚边界。
//
// 该能力用于 Go callback 或 VM 执行器出现 panic 时维持 State 边界一致性。
func TestStateProtectedCallPanicRestoresBoundary(t *testing.T) {
	state := NewState()
	_ = state.Push(StringValue("base"))

	err := state.ProtectedCall(func(protectedState *State) error {
		if pushErr := protectedState.Push(StringValue("temporary")); pushErr != nil {
			// 压栈失败时返回原错误，避免测试使用 panic 掩盖资源限制错误。
			return pushErr
		}
		if frameErr := protectedState.PushCallFrame(NewGoCallFrame(ReferenceValue(KindGoClosure, "callback"), 2, 0)); frameErr != nil {
			// 压入调用帧失败时返回原错误，保护调用应同样回滚边界。
			return frameErr
		}
		panic("boom")
	})
	if err == nil || err.Error() != "protected call panic: boom" {
		// panic 必须被转换为明确错误，不能越过保护调用边界。
		t.Fatalf("protected call panic error mismatch: %v", err)
	}
	if got := ErrorObject(err); !got.RawEqual(StringValue("boom")) {
		// panic 值必须转换为 Lua string error object，供保护调用调用方继续传播。
		t.Fatalf("protected call panic object mismatch: %#v", got)
	}
	if state.StackTop() != 1 || !state.ValueAt(-1).RawEqual(StringValue("base")) {
		// panic 路径必须回滚新增栈槽，只保留进入前的栈内容。
		t.Fatalf("protected call panic stack rollback mismatch: top=%d value=%#v", state.StackTop(), state.ValueAt(-1))
	}
	if state.CallDepth() != 0 {
		// panic 路径必须回滚新增调用帧。
		t.Fatalf("protected call panic frame rollback mismatch: got %d", state.CallDepth())
	}
}

// TestStateProtectedCallKeepsRuntimeErrorObject 验证已有 RuntimeError 的错误对象会原样传播。
//
// Lua `error` 可以抛出任意非 nil 值；已有 RuntimeError 表示上游已经构造了 Lua 错误对象。
func TestStateProtectedCallKeepsRuntimeErrorObject(t *testing.T) {
	state := NewState()
	expectedObject := IntegerValue(53)
	expectedErr := NewRuntimeError(expectedObject, errors.New("lua error object"))

	err := state.ProtectedCall(func(protectedState *State) error {
		if pushErr := protectedState.Push(StringValue("temporary")); pushErr != nil {
			// 压栈失败时返回原错误，避免测试继续建立错误对象传播场景。
			return pushErr
		}
		return expectedErr
	})
	if !errors.Is(err, expectedErr.Cause) {
		// RuntimeError 的 Cause 必须保留在错误链中。
		t.Fatalf("runtime error cause mismatch: %v", err)
	}
	if got := ErrorObject(err); !got.RawEqual(expectedObject) {
		// 已有 RuntimeError 的 Lua 错误对象必须原样传播，不应被替换为错误文本。
		t.Fatalf("runtime error object mismatch: %#v", got)
	}
	if state.StackTop() != 0 {
		// 错误对象传播失败时仍应回滚 protected call 内新增栈槽。
		t.Fatalf("runtime error stack rollback mismatch: got %d", state.StackTop())
	}
}

// TestStateProtectedCallInvalidState 验证关闭 State 和空回调的错误语义。
//
// 这两类错误都发生在执行回调前，不应修改栈或调用帧。
func TestStateProtectedCallInvalidState(t *testing.T) {
	state := NewState()
	if err := state.ProtectedCall(nil); !errors.Is(err, ErrNilProtectedCall) {
		// 空回调必须返回 ErrNilProtectedCall。
		t.Fatalf("nil protected call error mismatch: %v", err)
	}

	state.Close()
	err := state.ProtectedCall(func(protectedState *State) error {
		// 关闭后的 State 不应执行回调；若执行到这里说明边界检查失效。
		t.Fatalf("closed state should not run protected call")
		return nil
	})
	if !errors.Is(err, ErrClosedState) {
		// 关闭后的 State 必须返回 ErrClosedState。
		t.Fatalf("closed protected call error mismatch: %v", err)
	}
}

// TestStateCallFrameUnderflow 验证空调用帧栈弹出返回下溢错误。
//
// 空调用帧栈 Pop 不应 panic，也不应关闭 State。
func TestStateCallFrameUnderflow(t *testing.T) {
	state := NewState()

	frame, err := state.PopCallFrame()
	if frame != (CallFrame{}) || !errors.Is(err, ErrCallFrameUnderflow) {
		// 空调用帧栈弹出必须返回零值帧和 ErrCallFrameUnderflow。
		t.Fatalf("call frame underflow mismatch: frame=%#v err=%v", frame, err)
	}
	if _, ok := state.CurrentCallFrame(); ok {
		// 空调用帧栈不应存在当前帧。
		t.Fatalf("empty call frame stack should not have current frame")
	}
}

// TestStateCallFrameOverflow 验证调用帧深度限制。
//
// 超过 MaxCallDepth 时必须返回资源限制错误，并保持原调用帧栈不变。
func TestStateCallFrameOverflow(t *testing.T) {
	state := NewStateWithOptions(Options{MaxCallDepth: 1})
	frame := CallFrame{Kind: CallFrameKindGo, Function: ReferenceValue(KindGoClosure, "go"), Base: 1}
	if err := state.PushCallFrame(frame); err != nil {
		// 第一帧在限制内，应压入成功。
		t.Fatalf("push call frame within limit failed: %v", err)
	}

	err := state.PushCallFrame(frame)
	if err == nil || err.Error() != "C stack overflow" {
		// 第二帧超过 MaxCallDepth，必须返回 C stack overflow。
		t.Fatalf("call frame overflow error mismatch: %v", err)
	}
	if state.CallDepth() != 1 {
		// 溢出失败后原调用帧深度必须保持不变。
		t.Fatalf("call depth after overflow mismatch: got %d", state.CallDepth())
	}
}

// TestStateCallFrameAfterClose 验证关闭后的调用帧操作返回明确错误。
//
// Close 会释放调用帧 root，后续调用帧 API 不应重新创建或访问帧栈。
func TestStateCallFrameAfterClose(t *testing.T) {
	state := NewState()
	frame := CallFrame{Kind: CallFrameKindLua, Function: ReferenceValue(KindLuaClosure, "proto"), Base: 1}
	if err := state.PushCallFrame(frame); err != nil {
		// 关闭前压入调用帧必须成功。
		t.Fatalf("push call frame before close failed: %v", err)
	}
	state.Close()

	if state.CallDepth() != 0 {
		// 关闭后调用深度必须归零。
		t.Fatalf("closed state call depth mismatch: got %d", state.CallDepth())
	}
	if err := state.PushCallFrame(frame); !errors.Is(err, ErrClosedState) {
		// 关闭后 PushCallFrame 必须返回 ErrClosedState。
		t.Fatalf("closed state push call frame error mismatch: %v", err)
	}
	poppedFrame, err := state.PopCallFrame()
	if poppedFrame != (CallFrame{}) || !errors.Is(err, ErrClosedState) {
		// 关闭后 PopCallFrame 必须返回 ErrClosedState。
		t.Fatalf("closed state pop call frame mismatch: frame=%#v err=%v", poppedFrame, err)
	}
	if _, ok := state.CurrentCallFrame(); ok {
		// 关闭后不应存在当前调用帧。
		t.Fatalf("closed state should not have current call frame")
	}
}
