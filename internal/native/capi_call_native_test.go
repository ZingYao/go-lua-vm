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

// TestNativeLuaCallKRespectsCurrentCFrameBase 验证 C function 内 lua_callk 不穿透外层栈。
func TestNativeLuaCallKRespectsCurrentCFrameBase(t *testing.T) {
	// C function 调用期间，正索引和可调用区域都必须相对当前 C 帧；外层 Go VM 栈上的值
	// 不能因为 argumentCount 异常或嵌套调用而被误当作函数槽消费。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C 帧隔离。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushValue(luaState, runtime.StringValue("outer-sentinel"))
	baseTop := state.StackTop()
	if !pushNativeStateCallFrame(luaState, baseTop, nil) {
		// 建立 C 帧失败会让后续测试无意义。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 嵌套调用应只收到当前 C 帧内的一个实参。
		if len(args) != 1 || !args[0].RawEqual(runtime.IntegerValue(41)) {
			return nil, runtime.RaiseError(runtime.StringValue("bad nested args"))
		}
		return []runtime.Value{runtime.IntegerValue(42)}, nil
	})))
	nativeLuaPushInteger(luaState, 41)
	nativeLuaCallK(luaState, 1, 1)

	if got := state.StackTop(); got != baseTop+1 {
		// 全局栈应保留外层 sentinel 和当前 C 帧内的一个返回值。
		t.Fatalf("global top after nested call = %d, want %d", got, baseTop+1)
	}
	if value := state.ValueAt(1); !value.RawEqual(runtime.StringValue("outer-sentinel")) {
		// 外层栈值不能被嵌套 lua_callk 消费或覆盖。
		t.Fatalf("outer sentinel after nested call = %#v", value)
	}
	if value := state.ValueAt(-1); !value.RawEqual(runtime.IntegerValue(42)) {
		// 当前 C 帧内必须留下嵌套调用的返回值。
		t.Fatalf("nested call result = %#v, want 42", value)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// C API 视角只看到当前 C 帧的返回值。
		t.Fatalf("visible top after nested call = %d, want 1", got)
	}
	if errorObject, hasError := takeNativeStatePendingError(luaState); hasError {
		// 正常嵌套调用不应产生 pending error。
		t.Fatalf("nested call pending error = %#v", errorObject)
	}

	nativeLuaSetTop(luaState, 0)
	nativeLuaPushInteger(luaState, 7)
	nativeLuaCallK(luaState, 1, 1)
	if value := state.ValueAt(1); !value.RawEqual(runtime.StringValue("outer-sentinel")) {
		// 异常 argumentCount 不能穿透到 baseTop 之前，把外层 sentinel 当成函数槽。
		t.Fatalf("outer sentinel after malformed call = %#v", value)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 失败路径应保留当前 C 帧内的原始值，便于 C 模块继续诊断或返回。
		t.Fatalf("visible top after malformed call = %d, want 1", got)
	}
	errorObject, hasError := takeNativeStatePendingError(luaState)
	if !hasError || errorObject.Kind != runtime.KindString {
		// malformed 调用必须转为当前 C 帧的 pending error，而不是静默消费外层栈。
		t.Fatalf("malformed call pending error = %#v has=%v, want string error", errorObject, hasError)
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

// TestNativeLuaPCallKRespectsCurrentCFrameBase 验证 C function 内 lua_pcallk 不穿透外层栈。
func TestNativeLuaPCallKRespectsCurrentCFrameBase(t *testing.T) {
	// protected call 与非 protected call 使用同一 C 帧可见栈规则；失败时可以压入错误对象，
	// 但绝不能把 baseTop 之前的外层 Go VM 栈值当作函数或参数消费。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C 帧隔离。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushValue(luaState, runtime.StringValue("outer-sentinel"))
	baseTop := state.StackTop()
	if !pushNativeStateCallFrame(luaState, baseTop, nil) {
		// 建立 C 帧失败会让后续测试无意义。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 嵌套 protected call 应只收到当前 C 帧内的一个实参。
		if len(args) != 1 || !args[0].RawEqual(runtime.IntegerValue(11)) {
			return nil, runtime.RaiseError(runtime.StringValue("bad protected args"))
		}
		return []runtime.Value{runtime.IntegerValue(12)}, nil
	})))
	nativeLuaPushInteger(luaState, 11)
	if code := nativeLuaPCallK(luaState, 1, 1, 0); code != nativeLuaOK {
		// 正常嵌套 pcall 必须成功。
		t.Fatalf("nested nativeLuaPCallK code = %d, want LUA_OK", code)
	}
	if value := state.ValueAt(1); !value.RawEqual(runtime.StringValue("outer-sentinel")) {
		// 外层栈值不能被嵌套 pcall 消费或覆盖。
		t.Fatalf("outer sentinel after nested pcall = %#v", value)
	}
	if value := state.ValueAt(-1); !value.RawEqual(runtime.IntegerValue(12)) {
		// 当前 C 帧内必须留下嵌套 protected call 的返回值。
		t.Fatalf("nested pcall result = %#v, want 12", value)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// C API 视角只看到当前 C 帧的返回值。
		t.Fatalf("visible top after nested pcall = %d, want 1", got)
	}

	nativeLuaSetTop(luaState, 0)
	nativeLuaPushInteger(luaState, 7)
	if code := nativeLuaPCallK(luaState, 1, 1, 0); code != nativeLuaErrRun {
		// 当前 C 帧可见槽不足时必须返回运行期错误码。
		t.Fatalf("malformed nativeLuaPCallK code = %d, want LUA_ERRRUN", code)
	}
	if value := state.ValueAt(1); !value.RawEqual(runtime.StringValue("outer-sentinel")) {
		// malformed pcall 不能穿透到 baseTop 之前，把外层 sentinel 当成函数槽。
		t.Fatalf("outer sentinel after malformed pcall = %#v", value)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 失败安全路径保留当前 C 帧原始值，并把错误对象压在栈顶。
		t.Fatalf("visible top after malformed pcall = %d, want 2", got)
	}
	if value := state.ValueAt(-1); value.Kind != runtime.KindString {
		// 栈顶必须是 protected call 错误对象，供 C 模块读取或返回。
		t.Fatalf("malformed pcall error object = %#v, want string", value)
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
