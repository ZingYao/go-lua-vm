//go:build native_modules

package native

import (
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeLuaCompareBasicValues 验证 lua_compare 的基础 EQ/LT/LE 语义。
func TestNativeLuaCompareBasicValues(t *testing.T) {
	// 使用真实 State 和 opaque handle 验证 C API 比较读取 Go VM 栈值。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 compare。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushInteger(luaState, 1)
	nativeLuaPushNumber(luaState, 1.0)
	nativeLuaPushValue(luaState, runtime.StringValue("a"))
	nativeLuaPushValue(luaState, runtime.StringValue("b"))

	if got := nativeLuaCompare(luaState, 1, 2, nativeLuaOpEq); got != 1 {
		// integer(1) 与 number(1.0) 必须按 Lua 5.3 数字相等语义成立。
		t.Fatalf("nativeLuaCompare number eq = %d, want 1", got)
	}
	if got := nativeLuaCompare(luaState, 3, 4, nativeLuaOpLt); got != 1 {
		// 字符串小于按字节字典序比较。
		t.Fatalf("nativeLuaCompare string lt = %d, want 1", got)
	}
	if got := nativeLuaCompare(luaState, 3, 3, nativeLuaOpLe); got != 1 {
		// 同一字符串必须满足小于等于。
		t.Fatalf("nativeLuaCompare string le self = %d, want 1", got)
	}
	if got := nativeLuaCompare(luaState, 4, 3, nativeLuaOpLt); got != 0 {
		// b < a 应返回 false。
		t.Fatalf("nativeLuaCompare string reverse lt = %d, want 0", got)
	}
	if got := nativeLuaCompare(luaState, 1, 99, nativeLuaOpEq); got != 0 {
		// 无效索引按 Lua C API 语义返回 0，不触发错误。
		t.Fatalf("nativeLuaCompare missing index = %d, want 0", got)
	}
	if errorObject, hasError := takeNativeStatePendingError(luaState); hasError {
		// 基础成功路径和无效索引不应留下 pending error。
		t.Fatalf("nativeLuaCompare basic pending error = %#v", errorObject)
	}
}

// TestNativeLuaCompareRecordsOrderError 验证不可有序比较时记录 pending error。
func TestNativeLuaCompareRecordsOrderError(t *testing.T) {
	// 当前 native shim 对不可比较类型记录错误，等待 C function 返回边界传播。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 compare 错误路径。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushBoolean(luaState, true)
	nativeLuaPushInteger(luaState, 1)
	if got := nativeLuaCompare(luaState, 1, 2, nativeLuaOpLt); got != 0 {
		// boolean 与 number 不支持基础小于比较。
		t.Fatalf("nativeLuaCompare boolean lt integer = %d, want 0", got)
	}
	errorObject, hasError := takeNativeStatePendingError(luaState)
	if !hasError || errorObject.Kind != runtime.KindString || errorObject.String != "attempt to compare boolean with number" {
		// 错误文本必须说明左右不可比较类型。
		t.Fatalf("nativeLuaCompare pending error = %#v has=%v, want compare boolean with number", errorObject, hasError)
	}
}
