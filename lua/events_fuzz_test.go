//go:build !lua53 && (with_events || with_all || (!with_switch && !with_continue && !with_const && !with_events && !with_all))

package lua

import (
	"math"
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// FuzzGluaEventOptions 验证事件配置组合只返回稳定配置或错误，不发生 panic。
func FuzzGluaEventOptions(fuzzer *testing.F) {
	// 种子覆盖默认值、合法可靠性配置和明显非法边界。
	fuzzer.Add(int64(0), int64(0), uint8(0), uint8(0), float64(1))
	fuzzer.Add(int64(10), int64(32), uint8(1), uint8(1), float64(0.5))
	fuzzer.Add(int64(-1), int64(-1), uint8(4), uint8(4), math.NaN())
	fuzzer.Fuzz(func(t *testing.T, maxCalls int64, queueLimit int64, overflowIndex uint8, errorIndex uint8, sampleRate float64) {
		// 使用公开配置字段构造 table，统一解析器负责拒绝无效组合。
		config := runtime.NewTable()
		config.RawSetString("maxCalls", runtime.IntegerValue(maxCalls))
		config.RawSetString("queueLimit", runtime.IntegerValue(queueLimit))
		overflowValues := []string{"drop_oldest", "drop_newest", "error", "invalid"}
		config.RawSetString("overflow", runtime.StringValue(overflowValues[int(overflowIndex)%len(overflowValues)]))
		errorValues := []string{"propagate", "ignore", "mute", "remove", "invalid"}
		config.RawSetString("onError", runtime.StringValue(errorValues[int(errorIndex)%len(errorValues)]))
		config.RawSetString("sampleRate", runtime.NumberValue(sampleRate))
		_, _ = parseGluaEventOptions(runtime.ReferenceValue(runtime.KindTable, config))
	})
}
