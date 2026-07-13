//go:build !lua53 && (with_events || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package lua

import "testing"

// BenchmarkProgressEventSyncDispatch 测量一个同步 Go Event 监听器的触发与上下文构造成本。
//
// 基准使用固定 State、Source 和空 payload；注册及标准库初始化不计入计时，错误会立即终止基准。
func BenchmarkProgressEventSyncDispatch(benchmark *testing.B) {
	// 初始化完整 Event 命名空间并注册无副作用 callback。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 初始化失败时基准没有可比较结果。
		benchmark.Fatal(err)
	}
	if _, err := SetProgressEvent(state, "@benchmark-owner.glua", "benchmark.sync", func(ProgressEventContext) error { return nil }); err != nil {
		// 注册失败时不进入热路径计时。
		benchmark.Fatal(err)
	}
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for index := 0; index < benchmark.N; index++ {
		// 每轮同步触发一次，callback 在返回前完成。
		if err := CallProgressEvent(state, "@benchmark-worker.glua", "benchmark.sync"); err != nil {
			// 热路径错误表示 Event 行为回归，立即停止基准。
			benchmark.Fatal(err)
		}
	}
}

// BenchmarkProgressEventAsyncQueueFlush 测量异步任务入队和逐任务 flush 的组合成本。
//
// 每轮只排入一个任务并立即消费，避免基准结果包含队列持续扩容或 State 预算超限。
func BenchmarkProgressEventAsyncQueueFlush(benchmark *testing.B) {
	// 初始化异步监听器，callback 保持无副作用。
	state := NewState()
	defer state.Close()
	if err := OpenLibs(state); err != nil {
		// 初始化失败时基准没有可比较结果。
		benchmark.Fatal(err)
	}
	if _, err := SetProgressEvent(state, "@benchmark-owner.glua", "benchmark.async", func(ProgressEventContext) error { return nil }, ProgressEventOptions{Async: true}); err != nil {
		// 注册失败时不进入热路径计时。
		benchmark.Fatal(err)
	}
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for index := 0; index < benchmark.N; index++ {
		// 入队后立即 flush，保证每轮处理一个独立任务。
		if err := CallProgressEventAsync(state, "@benchmark-worker.glua", "benchmark.async"); err != nil {
			// 入队错误会让本轮数据失真。
			benchmark.Fatal(err)
		}
		if executed, err := FlushProgressEvents(state); err != nil || executed != 1 {
			// flush 必须精确执行当前轮任务。
			benchmark.Fatalf("flush executed=%d err=%v", executed, err)
		}
	}
}
