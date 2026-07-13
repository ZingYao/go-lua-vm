//go:build !lua53 && (with_events || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package lua

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// runNamedEventChunk 编译并执行带明确 Proto.Source 的测试 chunk。
func runNamedEventChunk(t *testing.T, state *State, source string, chunkName string) {
	// LoadString 负责生成带来源的 closure，Call 负责同步执行并传播 Lua 错误。
	t.Helper()
	if err := LoadString(state, source, chunkName); err != nil {
		// 编译失败说明测试脚本或扩展语法回归。
		t.Fatalf("LoadString %s failed: %v", chunkName, err)
	}
	closure := state.ValueAt(-1)
	if _, err := Call(state, closure); err != nil {
		// 执行失败时输出 Lua 错误对象，便于定位断言。
		t.Fatalf("Call %s failed: %v object=%s", chunkName, err, runtime.ErrorObject(err).DebugString())
	}
}

// TestGluaProgressEventScopes 验证默认 runtime 范围、file 范围和动态配置切换。
func TestGluaProgressEventScopes(t *testing.T) {
	// 两个命名 chunk 共用一个 State，模拟 require 后同一运行时中的跨文件调用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// Event 命名空间必须在标准库打开后可用。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	runNamedEventChunk(t, state, `
runtimeHits = 0
fileHits = 0
lastSource = nil
order = {}
runtimeID = glua.event.setProgress("scope.event", function(ctx)
  runtimeHits = runtimeHits + 1
  lastSource = ctx.source
  assert(ctx.scope == "runtime")
  order[#order + 1] = "runtime"
end, { priority = 1 })
fileID = glua.event.setProgress("scope.event", function(ctx)
  fileHits = fileHits + 1
  order[#order + 1] = "file"
end, { scope = "file", priority = 10 })
assert(glua.event.get(fileID).scope == "file")
`, "@scope-a.glua")

	runNamedEventChunk(t, state, `
glua.event.callProgress("scope.event")
assert(runtimeHits == 1 and fileHits == 0)
assert(lastSource == "@scope-b.glua", lastSource)
local list = glua.event.eventList()
assert(list.totalListeners == 1, list.totalListeners)
assert(glua.event.setConfig(fileID, { scope = "runtime", priority = 10 }))
assert(glua.event.get(fileID).scope == "runtime")
glua.event.callProgress("scope.event")
assert(runtimeHits == 2 and fileHits == 1)
assert(order[#order - 1] == "file" and order[#order] == "runtime")
`, "@scope-b.glua")

	runNamedEventChunk(t, state, `
local list = glua.event.eventList()
assert(list.totalListeners == 2, list.totalListeners)
assert(glua.event.setConfig(fileID, { scope = "file", priority = 10 }))
assert(glua.event.get(fileID).scope == "file")
glua.event.callProgress("scope.event")
assert(runtimeHits == 3 and fileHits == 2)
assert(order[#order - 1] == "file" and order[#order] == "runtime")
`, "@scope-a.glua")
}

// TestProgressEventGoAPI 验证 Go 注册、Go 触发、Lua 回调和异步 flush 的双向交互。
func TestProgressEventGoAPI(t *testing.T) {
	// 使用一个完整 State 验证公开 API 不需要调用方接触内部 registry。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// Event GC 根和 glua.event 必须先初始化。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	goCalls := 0
	eventID, err := SetProgressEvent(state, "@go-owner.glua", "go.bridge", func(context ProgressEventContext) error {
		// 类型化回调必须收到实际触发来源、payload 和默认 runtime scope。
		goCalls++
		if context.Source != "@go-trigger.glua" || context.Payload.String != "payload" || context.Scope != ProgressEventScopeRuntime {
			// 返回错误可验证 Go callback 的错误传播链。
			return errors.New("unexpected Go progress event context")
		}
		return nil
	})
	if err != nil || eventID <= 0 {
		// 注册成功必须返回稳定正整数 ID。
		t.Fatalf("SetProgressEvent = id=%d err=%v", eventID, err)
	}
	if err := CallProgressEvent(state, "@go-trigger.glua", "go.bridge", runtime.StringValue("payload")); err != nil {
		// runtime scope 的 Go listener 必须跨 source 触发。
		t.Fatalf("CallProgressEvent failed: %v", err)
	}
	if goCalls != 1 {
		// 同步调用返回前 callback 必须完成。
		t.Fatalf("Go callback count = %d, want 1", goCalls)
	}

	runNamedEventChunk(t, state, `
luaBridgePayload = nil
glua.event.setProgress("lua.bridge", function(ctx)
  luaBridgePayload = ctx.payload
end)
`, "@lua-listener.glua")
	if err := CallProgressEvent(state, "@go-trigger.glua", "lua.bridge", runtime.IntegerValue(42)); err != nil {
		// Go 触发必须能够调用 Lua 注册的 listener。
		t.Fatalf("CallProgressEvent Lua listener failed: %v", err)
	}
	runNamedEventChunk(t, state, `assert(luaBridgePayload == 42)`, "@lua-assert.glua")

	asyncCalls := 0
	_, err = SetProgressEvent(state, "@go-owner.glua", "go.async", func(context ProgressEventContext) error {
		// 异步 callback 只在 flush 后执行，并带 async=true。
		if !context.Async {
			// 错误进入 flush 返回值，避免测试静默通过。
			return errors.New("async progress event context is not marked async")
		}
		asyncCalls++
		return nil
	}, ProgressEventOptions{Async: true})
	if err != nil {
		// 合法异步监听器必须注册成功。
		t.Fatalf("SetProgressEvent async failed: %v", err)
	}
	if err := CallProgressEventAsync(state, "@go-trigger.glua", "go.async"); err != nil {
		// 异步触发只负责可靠入队。
		t.Fatalf("CallProgressEventAsync failed: %v", err)
	}
	if asyncCalls != 0 {
		// flush 前不得在当前 Go 调用栈执行 callback。
		t.Fatalf("async callback executed before flush: %d", asyncCalls)
	}
	executed, err := FlushProgressEvents(state)
	if err != nil || executed != 1 || asyncCalls != 1 {
		// flush 必须准确报告执行任务数量。
		t.Fatalf("FlushProgressEvents = executed=%d calls=%d err=%v", executed, asyncCalls, err)
	}
}

// TestProgressEventDiagnostics 验证异步队列与 callback 错误诊断可通过公开 API 读取。
func TestProgressEventDiagnostics(t *testing.T) {
	// 一个异步失败回调同时覆盖队列等待、drain 时间和最后错误快照。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// Event 命名空间未初始化时无法注册异步监听器。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	_, err := SetProgressEvent(state, "@diagnostic-owner.glua", "diagnostic.async", func(ProgressEventContext) error {
		// ignore 策略应保留错误诊断但不让 flush 失败。
		return errors.New("diagnostic callback failure")
	}, ProgressEventOptions{Async: true, OnError: "ignore"})
	if err != nil {
		// 合法的异步错误治理配置必须注册成功。
		t.Fatalf("SetProgressEvent failed: %v", err)
	}
	if err := CallProgressEventAsync(state, "@diagnostic-trigger.glua", "diagnostic.async"); err != nil {
		// 触发阶段只应入队，不应执行 callback。
		t.Fatalf("CallProgressEventAsync failed: %v", err)
	}
	if _, err := FlushProgressEvents(state); err != nil {
		// ignore 策略处理 callback 错误后 flush 应继续成功。
		t.Fatalf("FlushProgressEvents failed: %v", err)
	}
	summary, err := ListProgressEvents(state, "@diagnostic-trigger.glua")
	if err != nil {
		// 公开摘要读取必须与 Lua eventList 保持一致。
		t.Fatalf("ListProgressEvents failed: %v", err)
	}
	if summary.DrainedTasks != 1 || summary.QueuedTaskDuration < 0 || summary.MaxQueuedTaskDuration < 0 || summary.LastDrainAt.IsZero() {
		// 已 drain 任务必须携带非负排队耗时和最近 drain 时间。
		t.Fatalf("queue diagnostics = %+v", summary)
	}
	if summary.CallbackErrors != 1 || summary.LastCallbackError != "diagnostic callback failure" || summary.LastCallbackErrorAt.IsZero() {
		// callback 错误统计和最近错误快照必须同时可观察。
		t.Fatalf("callback diagnostics = %+v", summary)
	}
}

// TestProgressEventTraceHook 验证宿主能观测 callback 完成记录而不改变事件执行结果。
func TestProgressEventTraceHook(t *testing.T) {
	// 同步 listener 便于在触发返回后直接断言 trace 内容。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 未初始化 Event 命名空间时不允许设置 trace hook。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	var traces []ProgressEventTrace
	if err := SetProgressEventTraceHook(state, func(trace ProgressEventTrace) {
		// hook 只收集记录，不回调 Lua API。
		traces = append(traces, trace)
	}); err != nil {
		// 有效 State 必须允许设置观测 hook。
		t.Fatalf("SetProgressEventTraceHook failed: %v", err)
	}
	if _, err := SetProgressEvent(state, "@trace-owner.glua", "trace.event", func(ProgressEventContext) error {
		// 成功 callback 应生成无错误 trace。
		return nil
	}); err != nil {
		// 合法 listener 必须注册成功。
		t.Fatalf("SetProgressEvent failed: %v", err)
	}
	if err := CallProgressEvent(state, "@trace-trigger.glua", "trace.event"); err != nil {
		// trace hook 不得改变同步触发结果。
		t.Fatalf("CallProgressEvent failed: %v", err)
	}
	if len(traces) != 1 || traces[0].Event != "trace.event" || traces[0].Source != "@trace-trigger.glua" || traces[0].Async || traces[0].Error != "" || traces[0].Timestamp <= 0 || traces[0].Duration < 0 {
		// 观测记录必须保留触发 source、执行模式、耗时和成功状态。
		t.Fatalf("traces = %#v", traces)
	}
}

// TestProgressEventGoAPIErrors 验证公开 Go API 的初始化、配置和参数错误边界。
func TestProgressEventGoAPIErrors(t *testing.T) {
	// 未调用 OpenLibs 的 State 不具备 callback GC 根。
	unopened := NewState()
	defer unopened.Close()
	if _, err := SetProgressEvent(unopened, "@owner.glua", "test", func(ProgressEventContext) error { return nil }); !errors.Is(err, ErrGluaEventsNotOpen) {
		// 未初始化错误必须可用 errors.Is 判断。
		t.Fatalf("unopened SetProgressEvent error = %v", err)
	}

	options := WithGluaEvents(DefaultOptions(), false)
	disabled := NewStateWithOptions(options)
	defer disabled.Close()
	if err := OpenLibs(disabled); err != nil {
		// 关闭 Event 不影响其他标准库初始化。
		t.Fatalf("OpenLibs disabled failed: %v", err)
	}
	if err := CallProgressEvent(disabled, "@source.glua", "test"); !errors.Is(err, ErrGluaEventsUnavailable) {
		// 显式关闭 Event 应返回稳定 sentinel。
		t.Fatalf("disabled CallProgressEvent error = %v", err)
	}

	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 后续参数测试需要已初始化 Event。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	_, err := SetProgressEvent(state, "@owner.glua", "test", func(ProgressEventContext) error { return nil }, ProgressEventOptions{Scope: "unknown"})
	if err == nil {
		// 未知 scope 不能静默扩大到 runtime。
		t.Fatal("unknown progress event scope should fail")
	}
	if err := CallProgressEvent(state, "", "test"); !errors.Is(err, ErrProgressEventInvalidArgument) {
		// 缺少 source 无法执行 file 匹配或填充上下文，并且必须返回稳定参数分类。
		t.Fatalf("empty progress event source error = %v", err)
	}
}

// TestProgressEventGoManagementAPI 验证第三方宿主可完整管理监听器生命周期和类型化配置。
func TestProgressEventGoManagementAPI(t *testing.T) {
	// 使用完整 State 覆盖注册、查询、静音、替换、配置、分组和删除路径。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// Event 命名空间和 GC 根必须成功初始化。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	sampleRate := 1.0
	initialCalls := 0
	eventID, err := SetProgressEvent(state, "@owner.glua", "host.manage", func(ProgressEventContext) error {
		// 初始 callback 只记录实际执行次数。
		initialCalls++
		return nil
	}, ProgressEventOptions{
		Priority: 7, Group: "host", MaxCalls: 10, QueueLimit: 4,
		OnError: "ignore", Throttle: time.Millisecond, SampleRate: &sampleRate,
	})
	if err != nil {
		// 合法类型化配置必须注册成功。
		t.Fatalf("SetProgressEvent failed: %v", err)
	}
	listener, found, err := GetProgressEvent(state, eventID)
	if err != nil || !found {
		// 新注册监听器必须立即可查询。
		t.Fatalf("GetProgressEvent = listener=%#v found=%v err=%v", listener, found, err)
	}
	if listener.Priority != 7 || listener.Group != "host" || listener.Scope != ProgressEventScopeRuntime {
		// 类型化配置必须进入公开监听器快照。
		t.Fatalf("listener configuration mismatch: %#v", listener)
	}

	if err := CallProgressEvent(state, "@other.glua", "host.manage"); err != nil || initialCalls != 1 {
		// runtime scope 必须跨来源同步触发。
		t.Fatalf("initial CallProgressEvent calls=%d err=%v", initialCalls, err)
	}
	updated, err := SetProgressEventMuted(state, eventID, true)
	if err != nil || !updated {
		// 已存在监听器必须可以静音。
		t.Fatalf("SetProgressEventMuted = updated=%v err=%v", updated, err)
	}
	if err := CallProgressEvent(state, "@other.glua", "host.manage"); err != nil || initialCalls != 1 {
		// 静音监听器不能继续执行。
		t.Fatalf("muted CallProgressEvent calls=%d err=%v", initialCalls, err)
	}

	replacementCalls := 0
	updated, err = SetProgressEventCallback(state, eventID, func(ProgressEventContext) error {
		// 替换 callback 独立记录执行次数。
		replacementCalls++
		return nil
	})
	if err != nil || !updated {
		// callback 替换必须保留原事件 ID。
		t.Fatalf("SetProgressEventCallback = updated=%v err=%v", updated, err)
	}
	if updated, err = SetProgressEventMuted(state, eventID, false); err != nil || !updated {
		// 恢复静音后新 callback 应参与派发。
		t.Fatalf("restore muted = updated=%v err=%v", updated, err)
	}
	updated, err = SetProgressEventOptions(state, eventID, ProgressEventOptions{
		Scope: ProgressEventScopeFile, Priority: 9, Group: "replacement", MaxCalls: 3,
	})
	if err != nil || !updated {
		// 类型化配置更新必须成功。
		t.Fatalf("SetProgressEventOptions = updated=%v err=%v", updated, err)
	}
	if err := CallProgressEvent(state, "@other.glua", "host.manage"); err != nil || replacementCalls != 0 {
		// file scope 不匹配其他来源。
		t.Fatalf("foreign file call replacement=%d err=%v", replacementCalls, err)
	}
	if err := CallProgressEvent(state, "@owner.glua", "host.manage"); err != nil || replacementCalls != 1 {
		// 注册来源必须匹配 file scope。
		t.Fatalf("owner file call replacement=%d err=%v", replacementCalls, err)
	}

	summary, err := ListProgressEvents(state, "@owner.glua")
	if err != nil {
		// Go 侧列表必须解码为稳定类型化统计。
		t.Fatalf("ListProgressEvents = %#v err=%v", summary, err)
	}
	if summary.TotalListeners != 1 || len(summary.Events) != 1 || summary.Events[0].Event != "host.manage" || summary.Raw.Kind != KindTable {
		// 当前来源应只看到一个监听器。
		t.Fatalf("ListProgressEvents summary = %#v", summary)
	}
	if summary.ListenerLimit != runtime.DefaultMaxGluaEventListeners || summary.QueuedTaskLimit != runtime.DefaultMaxGluaEventQueuedTasks || summary.TasksPerDrainLimit != runtime.DefaultMaxGluaEventTasksPerDrain {
		// 类型化统计必须公开 State 当前预算，便于宿主监控容量。
		t.Fatalf("ListProgressEvents limits = %#v", summary)
	}
	rawSummary, err := ListProgressEventsRaw(state, "@owner.glua")
	if err != nil || rawSummary.Kind != KindTable {
		// 高级调用方仍可读取与 Lua eventList 相同的原始 table。
		t.Fatalf("ListProgressEventsRaw = %#v err=%v", rawSummary, err)
	}
	count, err := SetProgressEventGroupMuted(state, "@owner.glua", "replacement", true)
	if err != nil || count != 1 {
		// 分组静音按注册来源更新一个监听器。
		t.Fatalf("SetProgressEventGroupMuted = count=%d err=%v", count, err)
	}
	count, err = RemoveProgressEventGroup(state, "@owner.glua", "replacement")
	if err != nil || count != 1 {
		// 删除分组必须同步释放监听器。
		t.Fatalf("RemoveProgressEventGroup = count=%d err=%v", count, err)
	}
	if _, found, err := GetProgressEvent(state, eventID); err != nil || found {
		// 删除后的监听器不能继续查询到。
		t.Fatalf("GetProgressEvent after group removal found=%v err=%v", found, err)
	}

	secondID, err := SetProgressEvent(state, "@owner.glua", "host.remove", func(ProgressEventContext) error { return nil })
	if err != nil {
		// 单项删除测试需要第二个有效监听器。
		t.Fatalf("SetProgressEvent second failed: %v", err)
	}
	removed, err := RemoveProgressEvent(state, secondID)
	if err != nil || !removed {
		// 单项删除必须返回 true。
		t.Fatalf("RemoveProgressEvent = removed=%v err=%v", removed, err)
	}
}

// TestProgressEventTypedOptionsRejectInvalidValues 验证类型化配置拒绝截断时长和错误过滤值。
func TestProgressEventTypedOptionsRejectInvalidValues(t *testing.T) {
	// 参数校验发生在访问 State 前，因此可使用 nil State 验证构造错误。
	_, err := SetProgressEvent(nil, "@owner.glua", "test", func(ProgressEventContext) error { return nil }, ProgressEventOptions{Throttle: time.Microsecond})
	if err == nil {
		// 子毫秒时长不能静默截断为关闭节流。
		t.Fatal("sub-millisecond throttle should fail")
	}
	_, err = SetProgressEvent(nil, "@owner.glua", "test", func(ProgressEventContext) error { return nil }, ProgressEventOptions{WhitelistNames: []string{""}})
	if err == nil {
		// 空函数名不能形成有效过滤器。
		t.Fatal("empty whitelist name should fail")
	}
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// debounce 的同步监听器校验需要已初始化 State。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	_, err = SetProgressEvent(state, "@owner.glua", "test", func(ProgressEventContext) error { return nil }, ProgressEventOptions{Debounce: time.Millisecond})
	if !errors.Is(err, ErrProgressEventInvalidArgument) {
		// 同步监听器不能配置防抖，并应返回稳定参数分类。
		t.Fatalf("sync debounce error = %v", err)
	}
}

// TestProgressEventSourceHelpers 验证文件、内存 chunk 和既有 Lua Source 的规范化规则。
func TestProgressEventSourceHelpers(t *testing.T) {
	// 文件 helper 只做当前平台词法清理，并添加 Lua 文件 Source 前缀。
	fileSource, err := FileProgressEventSource(filepath.Join("scripts", "..", "main.glua"))
	if err != nil || fileSource != "@main.glua" {
		// helper 不访问文件系统，因此不存在的相对路径也应稳定规范化。
		t.Fatalf("FileProgressEventSource = %q err=%v", fileSource, err)
	}
	chunkSource, err := ChunkProgressEventSource("worker")
	if err != nil || chunkSource != "=worker" {
		// 内存 chunk 名必须使用等号前缀并保持名称不变。
		t.Fatalf("ChunkProgressEventSource = %q err=%v", chunkSource, err)
	}
	for _, source := range []string{"@vfs/module.glua", "=generated"} {
		// 已符合 Lua 约定的 Source 不应被二次清理。
		normalized, normalizeErr := NormalizeProgressEventSource(source)
		if normalizeErr != nil || normalized != source {
			// 前缀 Source 必须保持字节一致，才能精确匹配 Proto.Source。
			t.Fatalf("NormalizeProgressEventSource(%q) = %q err=%v", source, normalized, normalizeErr)
		}
	}
	if _, err := NormalizeProgressEventSource(""); !errors.Is(err, ErrProgressEventInvalidArgument) {
		// 空 Source 必须返回可分类参数错误。
		t.Fatalf("empty source error = %v", err)
	}
	var argumentError *ProgressEventArgumentError
	if _, err := ChunkProgressEventSource("bad\x00name"); !errors.As(err, &argumentError) || argumentError.Field != "source" {
		// NUL 错误必须保留具体字段，便于宿主生成参数诊断。
		t.Fatalf("NUL source error = %#v", err)
	}
}

// TestProgressEventConfigConflict 验证原始 Config 与类型化字段不会按隐式顺序相互覆盖。
func TestProgressEventConfigConflict(t *testing.T) {
	// 构造同时包含 priority 和宿主自定义 metadata 的原始配置。
	configTable := runtime.NewTable()
	configTable.RawSetString("priority", runtime.IntegerValue(3))
	configTable.RawSetString("metadata", runtime.StringValue("host"))
	configValue := runtime.ReferenceValue(runtime.KindTable, configTable)
	_, err := prepareProgressEventOptions([]ProgressEventOptions{{Config: configValue, Priority: 7}})
	if !errors.Is(err, ErrProgressEventConfigConflict) {
		// 同一字段使用两种入口必须明确失败，不能静默选择其中一侧。
		t.Fatalf("config conflict error = %v", err)
	}
	var conflictError *ProgressEventConfigConflictError
	if !errors.As(err, &conflictError) || conflictError.Field != "priority" {
		// 结构化错误必须指出冲突字段。
		t.Fatalf("config conflict detail = %#v", conflictError)
	}
	prepared, err := prepareProgressEventOptions([]ProgressEventOptions{{Config: configValue}})
	if err != nil {
		// 只使用原始 Config 时继续兼容高级 metadata 和 legacy 配置。
		t.Fatalf("raw Config failed: %v", err)
	}
	preparedTable, _ := prepared.Config.Ref.(*runtime.Table)
	if preparedTable == nil || preparedTable.RawGetString("priority").Integer != 3 || preparedTable.RawGetString("metadata").String != "host" {
		// 复制后的配置必须保留原字段且不能修改调用方 table。
		t.Fatalf("prepared Config = %#v", prepared.Config)
	}
}

// TestProgressEventStateLimits 验证监听器总数和异步队列总数使用 State 级硬预算。
func TestProgressEventStateLimits(t *testing.T) {
	// 一个监听器和一个排队任务足以分别触发两类容量错误。
	options := DefaultOptions()
	options.MaxGluaEventListeners = 1
	options.MaxGluaEventQueuedTasks = 1
	state := NewStateWithOptions(options)
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// Event 根表必须先初始化。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	_, err := SetProgressEvent(state, "@owner.glua", "limited", func(ProgressEventContext) error { return nil }, ProgressEventOptions{Async: true})
	if err != nil {
		// 预算内第一个监听器必须注册成功。
		t.Fatalf("first listener failed: %v", err)
	}
	_, err = SetProgressEvent(state, "@owner.glua", "second", func(ProgressEventContext) error { return nil })
	var limitError *ProgressEventLimitError
	if !errors.Is(err, ErrProgressEventLimitExceeded) || !errors.As(err, &limitError) || limitError.Kind != ProgressEventLimitListeners {
		// 第二个监听器必须命中 listeners 分类且不污染注册表。
		t.Fatalf("listener limit error = %#v", err)
	}
	if err := CallProgressEventAsync(state, "@worker.glua", "limited"); err != nil {
		// 第一个异步任务在队列预算内。
		t.Fatalf("first async call failed: %v", err)
	}
	err = CallProgressEventAsync(state, "@worker.glua", "limited")
	limitError = nil
	if !errors.Is(err, ErrProgressEventLimitExceeded) || !errors.As(err, &limitError) || limitError.Kind != ProgressEventLimitQueuedTasks {
		// 第二个任务必须命中 queued_tasks 分类。
		t.Fatalf("queue limit error = %#v", err)
	}
}

// TestProgressEventQueueFullError 验证单监听器 error 溢出策略向 Go 宿主暴露稳定错误和统计。
func TestProgressEventQueueFullError(t *testing.T) {
	// 建立可容纳多个 State 任务、但单监听器只容纳一个任务的异步监听器。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 公开 Event API 需要 OpenLibs 建立全局命名空间。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	listenerID, err := SetProgressEvent(state, "@owner.glua", "queue.full", func(ProgressEventContext) error {
		// 本用例只验证入队背压，回调本体无需副作用。
		return nil
	}, ProgressEventOptions{Async: true, QueueLimit: 1, Overflow: "error"})
	if err != nil {
		// 首次注册必须成功，才能验证后续队列上限。
		t.Fatalf("SetProgressEvent failed: %v", err)
	}
	if err := CallProgressEventAsync(state, "@worker.glua", "queue.full"); err != nil {
		// 第一个异步任务位于监听器预算内。
		t.Fatalf("first CallProgressEventAsync failed: %v", err)
	}
	err = CallProgressEventAsync(state, "@worker.glua", "queue.full")
	var queueFullError *ProgressEventQueueFullError
	if !errors.Is(err, ErrProgressEventQueueFull) || !errors.As(err, &queueFullError) || queueFullError.EventID != listenerID || queueFullError.Limit != 1 || queueFullError.Pending != 1 {
		// 第二个任务必须返回可分类的单监听器背压错误。
		t.Fatalf("queue full error = %#v", err)
	}
	listener, found, err := GetProgressEvent(state, listenerID)
	if err != nil || !found || listener.RejectedCount != 1 {
		// 单监听器快照必须暴露准确的拒绝次数。
		t.Fatalf("listener=%#v found=%v err=%v", listener, found, err)
	}
	summary, err := ListProgressEvents(state, "@worker.glua")
	if err != nil || summary.RejectedTasks != 1 || len(summary.Events) != 1 || summary.Events[0].RejectedCount != 1 {
		// 聚合统计必须区分主动丢弃和 error 策略拒绝。
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}

// TestPatchProgressEventOptionsAcceptsExplicitZeroValues 验证 Patch API 能区分零值覆盖和保持原配置。
func TestPatchProgressEventOptionsAcceptsExplicitZeroValues(t *testing.T) {
	// 创建包含非零可靠性配置的异步监听器，确保 Patch 有旧值可覆盖。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// Event API 需要 OpenLibs 初始化命名空间和根表。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	sampleRate := 0.5
	listenerID, err := SetProgressEvent(state, "@owner.glua", "patch.zero", func(ProgressEventContext) error {
		// 本用例只读取更新后的快照，不需要执行 callback。
		return nil
	}, ProgressEventOptions{
		Async: true, Once: true, MaxCalls: 1, Priority: 9, Group: "workers", QueueLimit: 3,
		Overflow: "drop_oldest", OnError: "ignore", Mutable: true, Throttle: time.Second, Debounce: time.Second,
		SampleRate: &sampleRate,
	})
	if err != nil {
		// 初始监听器必须成功注册，才能验证部分更新。
		t.Fatalf("SetProgressEvent failed: %v", err)
	}
	falseValue := false
	zeroInt64 := int64(0)
	zeroInt := 0
	emptyString := ""
	zeroDuration := time.Duration(0)
	zeroSampleRate := 0.0
	if updated, err := PatchProgressEventOptions(state, listenerID, ProgressEventOptionsPatch{
		Once: &falseValue, MaxCalls: &zeroInt64, Priority: &zeroInt64, Group: &emptyString, QueueLimit: &zeroInt,
		Mutable: &falseValue, Throttle: &zeroDuration, Debounce: &zeroDuration, SampleRate: &zeroSampleRate,
	}); err != nil || !updated {
		// 全部零值覆盖必须一次成功完成，不能退化为保持旧配置。
		t.Fatalf("PatchProgressEventOptions updated=%v err=%v", updated, err)
	}
	listener, found, err := GetProgressEvent(state, listenerID)
	if err != nil || !found {
		// 更新后的监听器必须仍存在且可读取。
		t.Fatalf("GetProgressEvent listener=%#v found=%v err=%v", listener, found, err)
	}
	if listener.Once || listener.MaxCalls != 0 || listener.Priority != 0 || listener.Group != "" || listener.Throttle != 0 || listener.Debounce != 0 || listener.SampleRate != 0 {
		// 每个零值字段都必须覆盖旧配置，而不是被当成未设置。
		t.Fatalf("patched listener=%#v", listener)
	}
}

// TestProgressEventDrainLimitPreservesQueue 验证单次 flush 预算不会丢失剩余异步任务。
func TestProgressEventDrainLimitPreservesQueue(t *testing.T) {
	// 队列允许三个任务，但每次 flush 只处理一个。
	options := DefaultOptions()
	options.MaxGluaEventQueuedTasks = 3
	options.MaxGluaEventTasksPerDrain = 1
	state := NewStateWithOptions(options)
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// Event 根表必须先初始化。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	calls := 0
	_, err := SetProgressEvent(state, "@owner.glua", "drain", func(ProgressEventContext) error {
		// 每次实际消费任务时精确增加一次计数。
		calls++
		return nil
	}, ProgressEventOptions{Async: true})
	if err != nil {
		// 异步监听器注册必须成功。
		t.Fatalf("SetProgressEvent failed: %v", err)
	}
	for index := 0; index < 2; index++ {
		// 连续排入两个任务，确保第一次 flush 后仍有剩余。
		if err := CallProgressEventAsync(state, "@worker.glua", "drain"); err != nil {
			t.Fatalf("CallProgressEventAsync #%d failed: %v", index, err)
		}
	}
	executed, err := FlushProgressEvents(state)
	var limitError *ProgressEventLimitError
	if executed != 1 || calls != 1 || !errors.As(err, &limitError) || limitError.Kind != ProgressEventLimitDrainTasks {
		// 第一次 flush 应执行一个任务、报告 drain 限额并保留第二个任务。
		t.Fatalf("first flush executed=%d calls=%d err=%#v", executed, calls, err)
	}
	executed, err = FlushProgressEvents(state)
	if err != nil || executed != 1 || calls != 2 {
		// 第二次 flush 必须正常消费保留任务并清空队列。
		t.Fatalf("second flush executed=%d calls=%d err=%v", executed, calls, err)
	}
}
