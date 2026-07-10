//go:build !lua53 && (with_events || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package lua

import (
	"testing"

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
		{name: "negative queue", field: "queueLimit", value: runtime.IntegerValue(-1)},
		{name: "overflow enum", field: "overflow", value: runtime.StringValue("block")},
		{name: "error enum", field: "onError", value: runtime.StringValue("panic")},
		{name: "mutable type", field: "mutable", value: runtime.IntegerValue(1)},
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
