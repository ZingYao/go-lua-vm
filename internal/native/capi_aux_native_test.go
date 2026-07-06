//go:build native_modules

package native

import (
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeCAPICheckInteger 验证 integer 参数检查的最小 native shim。
func TestNativeCAPICheckInteger(t *testing.T) {
	// 使用真实 State 和 opaque handle 验证 luaL_checkinteger 读取 Go VM 栈。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 aux shim。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushInteger(luaState, 53)
	nativeLuaPushNumber(luaState, 42)
	nativeLuaPushNumber(luaState, 2.5)
	nativeLuaPushString(luaState, nil)

	if integerValue, ok := nativeLuaToInteger(luaState, 1); !ok || integerValue != 53 {
		// integer 栈值必须原样返回。
		t.Fatalf("nativeLuaToInteger integer = value=%d ok=%v, want 53 true", integerValue, ok)
	}
	if integerValue, ok := nativeLuaToInteger(luaState, 2); !ok || integerValue != 42 {
		// 无小数 float number 必须可转换为 integer。
		t.Fatalf("nativeLuaToInteger number = value=%d ok=%v, want 42 true", integerValue, ok)
	}
	if integerValue, ok := nativeLuaToInteger(luaState, 3); ok || integerValue != 0 {
		// 有小数部分的 float number 不能转换为 integer。
		t.Fatalf("nativeLuaToInteger fractional = value=%d ok=%v, want 0 false", integerValue, ok)
	}
	if integerValue := nativeLuaCheckInteger(luaState, 3); integerValue != 0 {
		// luaL_error 尚未实现前，checkinteger 失败暂时返回 0。
		t.Fatalf("nativeLuaCheckInteger fractional = %d, want 0", integerValue)
	}
	if integerValue := nativeLuaCheckInteger(luaState, 2); integerValue != 42 {
		// checkinteger 成功路径必须返回整数值。
		t.Fatalf("nativeLuaCheckInteger number = %d, want 42", integerValue)
	}
	if integerValue, ok := nativeLuaToInteger(nil, 1); ok || integerValue != 0 {
		// nil State 不能读取任何整数。
		t.Fatalf("nativeLuaToInteger nil state = value=%d ok=%v, want 0 false", integerValue, ok)
	}
}
