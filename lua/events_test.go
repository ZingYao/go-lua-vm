//go:build !lua53 && (with_events || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package lua

import (
	"errors"
	"testing"
	"time"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestGluaEventRegistryReleasedOnStateClose 验证直接调用 State.Close 也会释放事件全局索引。
//
// 测试不接收外部入参；关闭后 sync.Map 不得再保留 State、监听器或异步上下文引用。
func TestGluaEventRegistryReleasedOnStateClose(t *testing.T) {
	// 创建注册表并填入最小监听器和队列，确保关闭覆盖全部强引用容器。
	state := NewState()
	registry := gluaRegistryForState(state)
	callback := &gluaEventCallback{id: 1, source: "@close.glua", eventName: "test.close"}
	registry.eventsByID = map[int64]*gluaEventCallback{callback.id: callback}
	registry.progressEvents = map[string]map[string][]*gluaEventCallback{
		callback.source: {callback.eventName: {callback}},
	}
	registry.queue = []gluaEventTask{{callback: callback, context: runtime.StringValue("pending")}}
	registry.roots = runtime.NewTable()

	state.Close()
	if _, ok := gluaEventRegistries.Load(state); ok {
		// 关闭后的 State 不能继续作为全局 map 强引用键。
		t.Fatalf("closed state still has an event registry")
	}
	if registry.eventsByID != nil || registry.progressEvents != nil || registry.queue != nil || registry.roots != nil {
		// 注册表内部容器必须同步释放，避免外部暂存 registry 时继续持有 Lua 对象。
		t.Fatalf("closed registry still retains event resources: %#v", registry)
	}
}

// TestGluaEventAsyncQueueOverflowPolicies 验证异步监听器队列上限和三种溢出策略。
func TestGluaEventAsyncQueueOverflowPolicies(t *testing.T) {
	// 直接测试注册表队列，避免 VM 安全点自动消费影响待处理数量。
	registry := &gluaEventRegistry{}
	context := runtime.ReferenceValue(runtime.KindTable, runtime.NewTable())
	dropNewest := &gluaEventCallback{id: 1, options: gluaEventOptions{queueLimit: 1, overflow: "drop_newest"}}
	if accepted, err := registry.enqueue(dropNewest, context); !accepted || err != nil {
		// 第一个任务必须成功入队。
		t.Fatalf("first drop_newest enqueue accepted=%v err=%v", accepted, err)
	}
	if accepted, err := registry.enqueue(dropNewest, context); accepted || err != nil {
		// 第二个任务应静默丢弃最新任务。
		t.Fatalf("overflow drop_newest accepted=%v err=%v", accepted, err)
	}
	if len(registry.queue) != 1 || registry.droppedTasks != 1 || dropNewest.droppedCount != 1 {
		// 队列和丢弃统计必须同步更新。
		t.Fatalf("drop_newest queue=%d dropped=%d callbackDropped=%d", len(registry.queue), registry.droppedTasks, dropNewest.droppedCount)
	}

	dropOldest := &gluaEventCallback{id: 2, options: gluaEventOptions{queueLimit: 1, overflow: "drop_oldest"}}
	if accepted, err := registry.enqueue(dropOldest, context); !accepted || err != nil {
		// drop_oldest 第一个任务必须成功入队。
		t.Fatalf("first drop_oldest enqueue accepted=%v err=%v", accepted, err)
	}
	replacementContext := runtime.ReferenceValue(runtime.KindTable, runtime.NewTable())
	if accepted, err := registry.enqueue(dropOldest, replacementContext); !accepted || err != nil {
		// 队列满时应删除旧任务并接受新任务。
		t.Fatalf("overflow drop_oldest accepted=%v err=%v", accepted, err)
	}
	if len(registry.queue) != 2 || registry.queue[1].context.Ref != replacementContext.Ref {
		// 另一个监听器任务不受影响，目标监听器仅保留新任务。
		t.Fatalf("drop_oldest queue mismatch: %#v", registry.queue)
	}

	errorPolicy := &gluaEventCallback{id: 3, options: gluaEventOptions{queueLimit: 1, overflow: "error"}}
	if accepted, err := registry.enqueue(errorPolicy, context); !accepted || err != nil {
		// error 策略第一个任务仍正常入队。
		t.Fatalf("first error enqueue accepted=%v err=%v", accepted, err)
	}
	if accepted, err := registry.enqueue(errorPolicy, context); accepted || err == nil {
		// 第二个任务必须返回溢出错误且保持原队列。
		t.Fatalf("overflow error accepted=%v err=%v", accepted, err)
	} else {
		// error 策略必须返回可由 Go 宿主稳定分类的队列满错误。
		var queueFullError *ProgressEventQueueFullError
		if !errors.Is(err, ErrProgressEventQueueFull) || !errors.As(err, &queueFullError) || queueFullError.EventID != errorPolicy.id || queueFullError.Limit != 1 || queueFullError.Pending != 1 {
			// 错误明细不完整会使宿主无法区分背压和脚本异常。
			t.Fatalf("overflow error detail = %#v", err)
		}
	}
	if errorPolicy.rejectedCount != 1 || registry.rejectedTasks != 1 {
		// 拒绝统计必须与错误策略次数精确同步。
		t.Fatalf("overflow rejection callback=%d registry=%d", errorPolicy.rejectedCount, registry.rejectedTasks)
	}
}

// TestGluaEventQueuedTaskManagement 验证异步任务入队后的替换、静音和删除语义。
//
// 测试直接构造单个监听器；setCallback 必须影响尚未消费的任务，静音必须暂停消费，remove 必须
// 取消队列任务并释放引用。各管理操作返回 boolean 表示监听器是否仍存在。
func TestGluaEventQueuedTaskManagement(t *testing.T) {
	// 使用同一个 callback 条目模拟队列持有的稳定事件 ID。
	registry := &gluaEventRegistry{
		eventsByID: make(map[int64]*gluaEventCallback),
		roots:      runtime.NewTable(),
	}
	replacementCalls := 0
	original := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 原始回调无需产生结果。
		return nil, nil
	}))
	replacement := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 记录待处理任务最终观察到的新回调。
		replacementCalls++
		return nil, nil
	}))
	callback := &gluaEventCallback{id: 11, source: "@queue.glua", eventName: "test.queue", value: original}
	registry.eventsByID[callback.id] = callback
	registry.progressEvents = map[string]map[string][]*gluaEventCallback{
		callback.source: {callback.eventName: {callback}},
	}
	registry.rootEvent(callback)
	if accepted, err := registry.enqueue(callback, runtime.StringValue("pending")); !accepted || err != nil {
		// 首个异步任务必须成功进入队列。
		t.Fatalf("enqueue accepted=%v err=%v", accepted, err)
	}
	if !registry.setEventCallback(callback.id, replacement) {
		// 队列保存 callback 条目指针，因此替换必须对待处理任务立即可见。
		t.Fatalf("queued callback was not replaced")
	}
	replacementFunction, ok := callback.value.Ref.(runtime.GoResultsFunction)
	if !ok {
		// 替换后的值必须仍是可调用 Go closure。
		t.Fatalf("replacement callback has unexpected type %T", callback.value.Ref)
	}
	if _, err := replacementFunction(); err != nil || replacementCalls != 1 {
		// 直接调用条目当前函数可证明旧任务引用会使用替换后的 callback.value。
		t.Fatalf("replacement callback call count=%d err=%v", replacementCalls, err)
	}
	if !registry.setEventMuted(callback.id, true) || registry.queuedCallbackActive(callback) {
		// 静音后的旧任务暂不允许执行。
		t.Fatalf("muted queued callback should be inactive")
	}
	if !registry.setEventMuted(callback.id, false) || !registry.queuedCallbackActive(callback) {
		// 取消静音后尚未删除的任务恢复可执行状态。
		t.Fatalf("unmuted queued callback should be active")
	}
	if !registry.removeEvent(callback.id) {
		// 删除已存在监听器必须返回 true。
		t.Fatalf("queued callback should be removable")
	}
	if len(registry.queue) != 0 || registry.eventsByID[callback.id] != nil || !registry.roots.RawGetInteger(callback.id).IsNil() {
		// 删除必须同时清理队列、索引和 Lua GC 根。
		t.Fatalf("remove retained queued callback resources")
	}
}

// TestGluaEventInvalidOptions 验证监听器治理配置拒绝非法枚举和类型。
//
// 每个配置 table 只包含一个非法字段；parseGluaEventOptions 必须返回 Lua error，不能回退默认值。
func TestGluaEventInvalidOptions(t *testing.T) {
	// 表驱动覆盖异步队列、错误策略和通用字段的主要非法输入。
	testCases := []struct {
		name  string
		field string
		value runtime.Value
	}{
		{name: "once type", field: "once", value: runtime.StringValue("true")},
		{name: "negative max calls", field: "maxCalls", value: runtime.IntegerValue(-1)},
		{name: "negative queue", field: "queueLimit", value: runtime.IntegerValue(-1)},
		{name: "overflow enum", field: "overflow", value: runtime.StringValue("block")},
		{name: "error enum", field: "onError", value: runtime.StringValue("panic")},
		{name: "mutable type", field: "mutable", value: runtime.IntegerValue(1)},
		{name: "negative throttle", field: "throttleMs", value: runtime.IntegerValue(-1)},
		{name: "negative debounce", field: "debounceMs", value: runtime.IntegerValue(-1)},
		{name: "sample type", field: "sampleRate", value: runtime.StringValue("half")},
		{name: "sample range", field: "sampleRate", value: runtime.NumberValue(1.1)},
	}
	for _, testCase := range testCases {
		// 每个非法字段独立构造 table，避免前一用例污染后一用例。
		t.Run(testCase.name, func(t *testing.T) {
			config := runtime.NewTable()
			config.RawSetString(testCase.field, testCase.value)
			if _, err := parseGluaEventOptions(runtime.ReferenceValue(runtime.KindTable, config)); err == nil {
				// 非法配置必须失败，避免运行期悄然采用默认策略。
				t.Fatalf("invalid %s option was accepted", testCase.field)
			}
		})
	}
}

// TestGluaEventReliabilityAdmission 验证限次、确定性采样和节流在派发前的额度行为。
//
// 测试显式传入纳秒时间，不依赖真实时钟漂移；每个监听器独立验证计数和释放语义。
func TestGluaEventReliabilityAdmission(t *testing.T) {
	// 辅助注册表只包含当前测试监听器，模拟正常注册索引。
	registry := &gluaEventRegistry{eventsByID: make(map[int64]*gluaEventCallback)}

	limited := &gluaEventCallback{id: 1, options: gluaEventOptions{maxCalls: 2, sampleRate: 1}}
	registry.eventsByID[limited.id] = limited
	if !registry.canDispatchAt(limited, nil, runtime.NilValue(), false, time.Unix(0, 1)) || !registry.canDispatchAt(limited, nil, runtime.NilValue(), false, time.Unix(0, 2)) {
		// 两次额度都必须允许占用。
		t.Fatalf("maxCalls should admit first two dispatches")
	}
	if registry.canDispatchAt(limited, nil, runtime.NilValue(), false, time.Unix(0, 3)) {
		// 两个待执行额度已经耗尽上限，第三次必须被拒绝。
		t.Fatalf("maxCalls admitted an excess dispatch")
	}
	registry.releaseDispatchClaim(limited)
	if !registry.canDispatchAt(limited, nil, runtime.NilValue(), false, time.Unix(0, 4)) {
		// 释放一个未执行额度后应允许再次占用。
		t.Fatalf("released maxCalls claim was not reusable")
	}

	sampled := &gluaEventCallback{id: 2, options: gluaEventOptions{sampleRate: 0.5}}
	registry.eventsByID[sampled.id] = sampled
	admitted := 0
	for index := int64(1); index <= 4; index++ {
		// 0.5 累加采样应稳定拒绝、接受、拒绝、接受。
		if registry.canDispatchAt(sampled, nil, runtime.NilValue(), false, time.Unix(0, index)) {
			// 接受后立即释放，避免额度影响下一次采样。
			admitted++
			registry.releaseDispatchClaim(sampled)
		}
	}
	if admitted != 2 || sampled.sampledOutCount != 2 {
		// 采样结果和抑制统计必须完全确定。
		t.Fatalf("sample admission=%d sampledOut=%d", admitted, sampled.sampledOutCount)
	}

	throttled := &gluaEventCallback{id: 3, options: gluaEventOptions{sampleRate: 1, throttleNs: 100}}
	registry.eventsByID[throttled.id] = throttled
	if !registry.canDispatchAt(throttled, nil, runtime.NilValue(), false, time.Unix(0, 1000)) {
		// 第一次触发必须通过节流。
		t.Fatalf("first throttled dispatch was rejected")
	}
	registry.releaseDispatchClaim(throttled)
	if registry.canDispatchAt(throttled, nil, runtime.NilValue(), false, time.Unix(0, 1050)) {
		// 时间窗内触发必须被抑制。
		t.Fatalf("throttle admitted an event inside its window")
	}
	if !registry.canDispatchAt(throttled, nil, runtime.NilValue(), false, time.Unix(0, 1100)) {
		// 到达边界时必须允许触发。
		t.Fatalf("throttle rejected an event at its boundary")
	}
	if throttled.throttledCount != 1 {
		// 节流统计只记录被抑制的一次。
		t.Fatalf("throttledCount=%d", throttled.throttledCount)
	}
}

// TestGluaEventDebounceQueue 验证异步防抖只保留最新上下文并遵守到期时间。
//
// 测试直接检查队列 readyAt，不休眠、不启动 goroutine，也不依赖 VM 指令执行速度。
func TestGluaEventDebounceQueue(t *testing.T) {
	// 注册表和监听器模拟一个已占用两次派发额度的异步防抖回调。
	registry := &gluaEventRegistry{}
	callback := &gluaEventCallback{id: 1, reservedCount: 2, options: gluaEventOptions{sampleRate: 1, debounceNs: int64(time.Second)}}
	first := runtime.StringValue("first")
	if accepted, err := registry.enqueue(callback, first); !accepted || err != nil {
		// 第一个上下文必须创建独立任务。
		t.Fatalf("first debounce enqueue accepted=%v err=%v", accepted, err)
	}
	latest := runtime.StringValue("latest")
	if accepted, err := registry.enqueue(callback, latest); accepted || err != nil {
		// 第二个上下文应合并而不是新增任务。
		t.Fatalf("second debounce enqueue accepted=%v err=%v", accepted, err)
	}
	registry.eventsByID = map[int64]*gluaEventCallback{callback.id: callback}
	registry.releaseDispatchClaim(callback)
	if len(registry.queue) != 1 || registry.queue[0].context.String != "latest" || callback.debouncedCount != 1 {
		// 队列必须只保留最新上下文和一次合并统计。
		t.Fatalf("debounce queue=%#v count=%d", registry.queue, callback.debouncedCount)
	}
	if callback.reservedCount != 1 {
		// 合并触发必须释放自己的额度，只保留最终任务的一次占用。
		t.Fatalf("debounce reservedCount=%d", callback.reservedCount)
	}
	readyAt := registry.queue[0].readyAt
	if tasks := registry.takeQueuedTasks(readyAt.Add(-time.Nanosecond), false, 1); len(tasks) != 0 {
		// 到期前普通安全点不能执行任务。
		t.Fatalf("debounce task executed before ready time")
	}
	tasks := registry.takeQueuedTasks(readyAt, false, 1)
	if len(tasks) != 1 || tasks[0].context.String != "latest" {
		// 到期时应取出唯一最新任务。
		t.Fatalf("debounce ready tasks=%#v", tasks)
	}
}

// TestGluaEventContextIdentity 验证调用链编号、父事件关系和根退出后的链切换。
//
// 测试使用固定调用深度，不依赖 VM 栈布局；返回编号必须单调且同一链共享 traceId。
func TestGluaEventContextIdentity(t *testing.T) {
	// 根事件建立 trace，较深事件应指向最近的较浅事件。
	registry := &gluaEventRegistry{}
	rootID, rootTraceID, rootParentID := registry.nextContextIdentity(GluaEventProgressStart, 1)
	if rootID <= 0 || rootTraceID <= 0 || rootParentID != 0 {
		// 根事件必须有有效身份且没有父事件。
		t.Fatalf("root identity event=%d trace=%d parent=%d", rootID, rootTraceID, rootParentID)
	}
	childID, childTraceID, childParentID := registry.nextContextIdentity(GluaEventProgressFunctionCall, 2)
	if childID <= rootID || childTraceID != rootTraceID || childParentID != rootID {
		// 子事件必须共享 trace 并指向根深度最近事件。
		t.Fatalf("child identity event=%d trace=%d parent=%d", childID, childTraceID, childParentID)
	}
	exitID, exitTraceID, exitParentID := registry.nextContextIdentity(GluaEventProgressExit, 1)
	if exitID <= childID || exitTraceID != rootTraceID || exitParentID != 0 {
		// 根退出仍属于旧链，且同深度事件没有较浅父节点。
		t.Fatalf("exit identity event=%d trace=%d parent=%d", exitID, exitTraceID, exitParentID)
	}
	nextID, nextTraceID, nextParentID := registry.nextContextIdentity("custom.next", 1)
	if nextID <= exitID || nextTraceID == rootTraceID || nextParentID != 0 {
		// 根退出后的独立事件必须建立新链。
		t.Fatalf("next identity event=%d trace=%d parent=%d", nextID, nextTraceID, nextParentID)
	}
}

// TestGluaEventTrimQueueOnLimitUpdate 验证收紧 maxCalls 会取消最新的超额异步任务。
//
// callback 已占用三个队列额度；收紧到一个后必须保留最早任务并同步更新抑制统计。
func TestGluaEventTrimQueueOnLimitUpdate(t *testing.T) {
	// 构造三个可区分上下文，确保裁剪方向稳定。
	callback := &gluaEventCallback{id: 1, reservedCount: 3, options: gluaEventOptions{maxCalls: 1, sampleRate: 1}}
	registry := &gluaEventRegistry{
		queue: []gluaEventTask{
			{callback: callback, context: runtime.StringValue("first")},
			{callback: callback, context: runtime.StringValue("second")},
			{callback: callback, context: runtime.StringValue("third")},
		},
	}
	registry.mu.Lock()
	registry.trimCallbackQueueLocked(callback, 1)
	registry.mu.Unlock()
	if len(registry.queue) != 1 || registry.queue[0].context.String != "first" {
		// 最早任务必须保留，最新两个任务被取消。
		t.Fatalf("trimmed queue=%#v", registry.queue)
	}
	if callback.reservedCount != 1 || callback.suppressedCount != 2 || registry.suppressedEvents != 2 {
		// 额度和两级统计必须同步减少或增加。
		t.Fatalf("reserved=%d callbackSuppressed=%d registrySuppressed=%d", callback.reservedCount, callback.suppressedCount, registry.suppressedEvents)
	}
}
