//go:build !lua53 && (with_events || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package lua

import (
	"errors"
	"testing"

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
	if err := CallProgressEvent(state, "", "test"); err == nil {
		// 缺少 source 无法执行 file 匹配或填充上下文。
		t.Fatal("empty progress event source should fail")
	}
}
