// Package main 演示第三方 Go 宿主使用 GLua 扩展和 Event 管理 API。
package main

import (
	"fmt"
	"log"

	"github.com/ZingYao/go-lua-vm/lua"
)

// main 创建 GLua State，由 Go 注册监听器，再演示 Lua/Go 触发和监听器完整生命周期。
func main() {
	// 初始化完整标准库和 glua 命名空间，退出时统一释放监听器与队列。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 初始化失败时不能继续执行脚本。
		log.Fatal(err)
	}
	ownerSource, err := lua.ChunkProgressEventSource("host")
	if err != nil {
		// 内存宿主来源必须能转换为稳定的 =chunk Source。
		log.Fatal(err)
	}
	workerSource, err := lua.FileProgressEventSource("worker.glua")
	if err != nil {
		// 文件来源只做词法清理，不要求文件真实存在。
		log.Fatal(err)
	}

	callbackCount := 0
	listenerID, err := lua.SetProgressEvent(state, ownerSource, "order.ready", func(context lua.ProgressEventContext) error {
		// callback 在同步触发点读取来源、payload 和调用链信息。
		callbackCount++
		fmt.Printf("event=%s source=%s payload=%s trace=%d\n", context.Event, context.Source, context.Payload.DebugString(), context.TraceID)
		return nil
	}, lua.ProgressEventOptions{Priority: 10, Group: "orders", Scope: lua.ProgressEventScopeRuntime})
	if err != nil {
		// 注册失败时不执行依赖监听器的脚本。
		log.Fatal(err)
	}

	if err := lua.LoadString(state, `
local payload = glua.json.encode({ id = "A-100", ready = true })
glua.event.callProgress("order.ready", payload)
`, workerSource); err != nil {
		// 编译错误保留命名 chunk 的源码位置。
		log.Fatal(err)
	}
	if _, err := lua.Call(state, state.ValueAt(-1)); err != nil {
		// Lua 或同步 callback 错误会从 Call 返回。
		log.Fatal(err)
	}
	if callbackCount != 1 {
		// Lua 同步触发必须在脚本返回前执行一次 Go callback。
		log.Fatalf("unexpected callback count after Lua call: %d", callbackCount)
	}

	muted, err := lua.SetProgressEventMuted(state, listenerID, true)
	if err != nil || !muted {
		// 静音失败表示监听器生命周期状态异常。
		log.Fatalf("mute listener: changed=%t err=%v", muted, err)
	}
	if err := lua.CallProgressEvent(state, workerSource, "order.ready", lua.Value{Kind: lua.KindString, String: "muted"}); err != nil {
		// 静音监听器不执行 callback，但事件触发本身仍应成功。
		log.Fatal(err)
	}
	if callbackCount != 1 {
		// 静音期间 callback 计数不能增长。
		log.Fatalf("muted listener was dispatched: %d", callbackCount)
	}

	unmuted, err := lua.SetProgressEventMuted(state, listenerID, false)
	if err != nil || !unmuted {
		// 恢复失败时后续 callback 替换没有演示意义。
		log.Fatalf("unmute listener: changed=%t err=%v", unmuted, err)
	}
	replacedCount := 0
	replaced, err := lua.SetProgressEventCallback(state, listenerID, func(context lua.ProgressEventContext) error {
		// 替换 callback 保留事件 ID、配置和既有统计。
		replacedCount++
		fmt.Printf("replacement event=%s sequence=%d async=%t\n", context.Event, context.Sequence, context.Async)
		return nil
	})
	if err != nil || !replaced {
		// callback 替换失败时终止示例，避免继续展示错误状态。
		log.Fatalf("replace callback: changed=%t err=%v", replaced, err)
	}

	if err := lua.CallProgressEventAsync(state, workerSource, "order.ready", lua.Value{Kind: lua.KindString, String: "queued"}); err != nil {
		// 异步触发只负责把任务加入当前 State 队列。
		log.Fatal(err)
	}
	if replacedCount != 0 {
		// flush 前执行 callback 说明异步边界已经失效。
		log.Fatalf("async callback ran before flush: %d", replacedCount)
	}
	flushed, err := lua.FlushProgressEvents(state)
	if err != nil {
		// flush 会在当前 goroutine 串行执行已接受任务。
		log.Fatal(err)
	}
	if flushed != 1 || replacedCount != 1 {
		// 当前示例只排入一个任务，因此执行数必须精确为一。
		log.Fatalf("unexpected flush result: flushed=%d callbacks=%d", flushed, replacedCount)
	}

	updated, err := lua.SetProgressEventOptions(state, listenerID, lua.ProgressEventOptions{
		Scope:    lua.ProgressEventScopeRuntime,
		Priority: 20,
		Group:    "orders-v2",
		MaxCalls: 10,
	})
	if err != nil || !updated {
		// 配置更新失败时不能继续断言新的监听器快照。
		log.Fatalf("update listener options: changed=%t err=%v", updated, err)
	}

	listener, found, err := lua.GetProgressEvent(state, listenerID)
	if err != nil {
		// 查询错误表示 State 或 Event 生命周期异常。
		log.Fatal(err)
	}
	if !found {
		// 正常监听器在主动删除前必须存在。
		log.Fatal("listener disappeared")
	}
	fmt.Printf("listener=%d calls=%d group=%s priority=%d\n", listener.ID, listener.DispatchCount, listener.Group, listener.Priority)
	summary, err := lua.ListProgressEvents(state, workerSource)
	if err != nil {
		// 类型化列表应直接提供监听器、队列和 State 预算统计。
		log.Fatal(err)
	}
	fmt.Printf("events=%d listeners=%d queued=%d limits=%d/%d/%d\n",
		summary.TotalEvents, summary.TotalListeners, summary.QueuedTasks,
		summary.ListenerLimit, summary.QueuedTaskLimit, summary.TasksPerDrainLimit,
	)

	removed, err := lua.RemoveProgressEvent(state, listenerID)
	if err != nil {
		// 删除错误直接报告给宿主。
		log.Fatal(err)
	}
	if !removed {
		// false 表示事件 ID 已经不存在。
		log.Fatal("listener was not removed")
	}
}
