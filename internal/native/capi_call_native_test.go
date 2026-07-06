//go:build native_modules

package native

import (
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeLuaPCallKCallsGoClosure 验证 lua_pcallk 能调用 native shim 包装的 Go closure。
func TestNativeLuaPCallKCallsGoClosure(t *testing.T) {
	// pcall 成功时必须弹出函数和参数，并按 nresults 压回返回值。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 pcall。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 测试函数返回一个固定结果，便于验证 nresults 搬运。
		return []runtime.Value{runtime.IntegerValue(42)}, nil
	})))
	if code := nativeLuaPCallK(luaState, 0, 1, 0); code != nativeLuaOK {
		// 成功调用必须返回 LUA_OK。
		t.Fatalf("nativeLuaPCallK code = %d, want LUA_OK", code)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 函数值应被消费，只保留一个返回值。
		t.Fatalf("nativeLuaPCallK success top = %d, want 1", got)
	}
	if value := state.ValueAt(-1); !value.RawEqual(runtime.IntegerValue(42)) {
		// 返回值必须按原顺序压栈。
		t.Fatalf("nativeLuaPCallK success result = %#v, want 42", value)
	}
}

// TestNativeLuaPCallKPushesErrorObject 验证 lua_pcallk 失败时压入 error object。
func TestNativeLuaPCallKPushesErrorObject(t *testing.T) {
	// pcall 失败时必须返回 LUA_ERRRUN，并把错误对象放到栈顶供 C 模块读取。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 pcall 错误路径。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 返回 Lua error，ProtectedCall 应把它转换为栈顶 error object。
		return nil, runtime.RaiseError(runtime.StringValue("boom"))
	})))
	if code := nativeLuaPCallK(luaState, 0, 1, 0); code != nativeLuaErrRun {
		// 运行期错误必须返回 LUA_ERRRUN。
		t.Fatalf("nativeLuaPCallK error code = %d, want LUA_ERRRUN", code)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 错误路径应只压入一个 error object。
		t.Fatalf("nativeLuaPCallK error top = %d, want 1", got)
	}
	if value := state.ValueAt(-1); !value.RawEqual(runtime.StringValue("boom")) {
		// 错误对象必须保留原始 Lua error object。
		t.Fatalf("nativeLuaPCallK error object = %#v, want boom", value)
	}
}
