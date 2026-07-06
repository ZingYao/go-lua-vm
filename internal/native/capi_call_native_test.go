//go:build native_modules

package native

import (
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeLuaCallKCallsGoClosure 验证 lua_callk 能执行非 protected 调用并压回返回值。
func TestNativeLuaCallKCallsGoClosure(t *testing.T) {
	// lua_callk 成功时必须弹出函数和参数，并按 nresults 压回固定数量返回值。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 callk。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 测试函数读取两个参数并返回求和结果，便于验证实参搬运。
		if len(args) != 2 {
			// 实参数量不对时返回 Lua error，让测试能暴露调用帧问题。
			return nil, runtime.RaiseError(runtime.StringValue("bad arg count"))
		}
		left, leftOK := args[0].ToInteger()
		right, rightOK := args[1].ToInteger()
		if !leftOK || !rightOK {
			// 非整数实参说明 C API 栈复制出错。
			return nil, runtime.RaiseError(runtime.StringValue("bad arg type"))
		}
		return []runtime.Value{runtime.IntegerValue(left + right)}, nil
	})))
	nativeLuaPushInteger(luaState, 20)
	nativeLuaPushInteger(luaState, 22)
	nativeLuaCallK(luaState, 2, 1)

	if got := nativeLuaStackTop(luaState); got != 1 {
		// 函数和参数应被消费，只保留一个返回值。
		t.Fatalf("nativeLuaCallK success top = %d, want 1", got)
	}
	if value := state.ValueAt(-1); !value.RawEqual(runtime.IntegerValue(42)) {
		// 返回值必须按原顺序压栈。
		t.Fatalf("nativeLuaCallK success result = %#v, want 42", value)
	}
	if errorObject, hasError := takeNativeStatePendingError(luaState); hasError {
		// 成功路径不应留下 pending error。
		t.Fatalf("nativeLuaCallK success pending error = %#v", errorObject)
	}
}

// TestNativeLuaCallKRecordsPendingError 验证 lua_callk 失败时记录非 protected pending error。
func TestNativeLuaCallKRecordsPendingError(t *testing.T) {
	// lua_callk 没有错误码返回，错误需要等待当前 C function 返回 Go 边界后传播。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 callk 错误路径。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 返回 Lua error，lua_callk 应记录 pending error 而不是压入 error object。
		return nil, runtime.RaiseError(runtime.StringValue("boom"))
	})))
	nativeLuaCallK(luaState, 0, 1)

	if got := nativeLuaStackTop(luaState); got != 0 {
		// 非 protected 错误路径已经移除函数槽，不应压入 pcall 风格 error object。
		t.Fatalf("nativeLuaCallK error top = %d, want 0", got)
	}
	errorObject, hasError := takeNativeStatePendingError(luaState)
	if !hasError || !errorObject.RawEqual(runtime.StringValue("boom")) {
		// 错误对象必须等待 C function 返回边界统一传播。
		t.Fatalf("nativeLuaCallK pending error = %#v has=%v, want boom", errorObject, hasError)
	}
}

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
