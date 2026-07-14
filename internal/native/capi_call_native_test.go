//go:build cgo

package native

import (
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
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

// TestNativeLuaCallKPushesTemporaryCFrame 验证 C API 调用会暴露临时 C 帧。
func TestNativeLuaCallKPushesTemporaryCFrame(t *testing.T) {
	// error(level) 和 debug traceback 需要看到 C 模块通过 lua_callk 进入 Lua/Go callback 的边界。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 callk。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	observedFrame := false
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// callback 执行期间，当前帧应是 native lua_call 暴露出的 Go/C 边界。
		frames := state.TracebackFrames()
		if len(frames) == 0 {
			return nil, runtime.RaiseError(runtime.StringValue("missing lua_call frame"))
		}
		if frames[0].Kind != runtime.CallFrameKindGo || frames[0].Name != "lua_call" || frames[0].NameWhat != "C" {
			return nil, runtime.RaiseError(runtime.StringValue("wrong lua_call frame"))
		}
		observedFrame = true
		return []runtime.Value{runtime.IntegerValue(1)}, nil
	})))
	nativeLuaCallK(luaState, 0, 0)

	if !observedFrame {
		// callback 必须实际执行并观察到临时 C 帧。
		t.Fatalf("native lua_call frame was not observed")
	}
	if errorObject, hasError := takeNativeStatePendingError(luaState); hasError {
		// 成功路径不应留下 pending error。
		t.Fatalf("nativeLuaCallK frame pending error = %#v", errorObject)
	}
	if frames := state.TracebackFrames(); len(frames) != 0 {
		// 调用完成后临时 C 帧必须弹出。
		t.Fatalf("native lua_call frame leaked: %#v", frames)
	}
}

// TestNativeLuaCallKCallsUnaryGoClosureShapes 验证 lua_callk 覆盖一元 Go closure 负载。
func TestNativeLuaCallKCallsUnaryGoClosureShapes(t *testing.T) {
	// native C 模块通过 lua_callk 调 Lua/Go 函数时，必须支持标准库常用的一元热路径闭包形态；
	// 该路径不能只识别 GoResultsFunction，否则 math.sin 这类函数会被误判为不可调用。
	cases := []struct {
		name     string
		function runtime.Value
	}{
		{
			name: "go-unary-function",
			function: runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoUnaryFunction(func(argument runtime.Value) (runtime.Value, error) {
				// 测试函数只消费首个实参，多余 Lua 实参必须被调用边界安全忽略。
				value, ok := argument.ToInteger()
				if !ok {
					// 非整数实参说明 native 调用桥没有按顺序传入首个参数。
					return runtime.NilValue(), runtime.RaiseError(runtime.StringValue("bad unary argument"))
				}
				return runtime.IntegerValue(value + 1), nil
			})),
		},
		{
			name: "go-fast-unary-function",
			function: runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoFastUnaryFunction{
				Function: func(argument runtime.Value) (runtime.Value, error) {
					// fast unary 在 native C API 调用桥中仍要走真实函数入口，不能依赖 VM opcode 快路径。
					value, ok := argument.ToInteger()
					if !ok {
						// 非整数实参说明 native 调用桥没有按顺序传入首个参数。
						return runtime.NilValue(), runtime.RaiseError(runtime.StringValue("bad fast unary argument"))
					}
					return runtime.IntegerValue(value + 1), nil
				},
				AcceptedKinds: runtime.UnaryKindMask(runtime.KindInteger),
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 每个子用例使用独立 State，避免 pending error 或栈状态跨用例污染。
			state := runtime.NewState()
			defer state.Close()
			handle, err := newNativeStateHandle(state)
			if err != nil {
				// handle 创建失败说明 State 映射不可用，无法验证 unary callk。
				t.Fatalf("newNativeStateHandle failed: %v", err)
			}
			defer handle.close()
			luaState := handle.pointer()

			nativeLuaPushValue(luaState, runtime.StringValue("outer-sentinel"))
			baseTop := state.StackTop()
			if !pushNativeStateCallFrame(luaState, baseTop, nil) {
				// 建立 C frame 失败时无法验证 native C 模块中的 lua_callk 边界。
				t.Fatalf("pushNativeStateCallFrame failed")
			}
			defer popNativeStateCallFrame(luaState)

			nativeLuaPushValue(luaState, tc.function)
			nativeLuaPushInteger(luaState, 41)
			nativeLuaPushInteger(luaState, 99)
			nativeLuaCallK(luaState, 2, 1)

			if got := nativeLuaStackTop(luaState); got != 1 {
				// 函数和两个参数应被消费，只留下一个一元函数返回值。
				t.Fatalf("visible top after unary call = %d, want 1", got)
			}
			if got := state.StackTop(); got != baseTop+1 {
				// 全局栈应保留外层 sentinel 加当前 C frame 的一个返回值。
				t.Fatalf("global top after unary call = %d, want %d", got, baseTop+1)
			}
			if value := state.ValueAt(1); !value.RawEqual(runtime.StringValue("outer-sentinel")) {
				// native C frame 内的调用不能覆盖外层 Go VM 栈值。
				t.Fatalf("outer sentinel after unary call = %#v", value)
			}
			if value := state.ValueAt(-1); !value.RawEqual(runtime.IntegerValue(42)) {
				// 一元闭包必须读取首个实参 41 并忽略第二个多余实参。
				t.Fatalf("unary call result = %#v, want 42", value)
			}
			if errorObject, hasError := takeNativeStatePendingError(luaState); hasError {
				// 成功路径不应留下 pending error。
				t.Fatalf("unary call pending error = %#v", errorObject)
			}
		})
	}
}

// TestNativeLuaCallKRecordsPendingError 验证 lua_callk Go helper 失败时记录非 protected pending error。
func TestNativeLuaCallKRecordsPendingError(t *testing.T) {
	// Go helper 只记录错误对象；真实 C 入口会在看到错误标记后 longjmp，避免 C 调用点继续执行。
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

// TestNativeLuaCallKFixedResultKeepsConstructedUserdataRoots 验证固定返回调用后的构造期 native 根。
func TestNativeLuaCallKFixedResultKeepsConstructedUserdataRoots(t *testing.T) {
	// 固定返回 Go closure 经 lua_callk 写回后，当前 C frame 继续创建 userdata/user value table；
	// 这些临时构造对象必须通过可见栈和 userdata 关联边参与 weak sweep 强根判断。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 fixed call 与构造期根组合。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	weakValueTable := runtime.NewTable()
	weakMetatable := runtime.NewTable()
	weakMetatable.RawSetString("__mode", runtime.StringValue("v"))
	weakValueTable.SetMetatable(weakMetatable)
	state.SetGlobal("weak", runtime.ReferenceValue(runtime.KindTable, weakValueTable))
	target := runtime.NewTable()
	weakValueTable.RawSetString("from-fixed-call-userdata", runtime.ReferenceValue(runtime.KindTable, target))

	nativeLuaPushValue(luaState, runtime.StringValue("outer-sentinel"))
	baseTop := state.StackTop()
	if !pushNativeStateCallFrame(luaState, baseTop, nil) {
		// 建立 C frame 失败时无法验证可见栈边界。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	fixedFunction := &runtime.GoFixedResultsFunction{
		MaxResults: 1,
		Function: func(results []runtime.Value, args ...runtime.Value) (int, bool, error) {
			// 该回调模拟标准库 select/rawequal 这类固定返回 Go closure 的 C API 调用路径。
			if len(args) != 2 {
				// 参数数量不符合预期时返回 Lua error，避免测试静默通过。
				return 0, false, runtime.RaiseError(runtime.StringValue("bad fixed arg count"))
			}
			if len(results) == 0 {
				// 调用方声明的 MaxResults 不足时不能写入结果槽。
				return 0, false, runtime.RaiseError(runtime.StringValue("missing result slot"))
			}
			results[0] = runtime.IntegerValue(int64(len(args)))
			return 1, true, nil
		},
	}
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, fixedFunction))
	nativeLuaPushValue(luaState, runtime.StringValue("alpha"))
	nativeLuaPushValue(luaState, runtime.StringValue("beta"))
	nativeLuaCallK(luaState, 2, 1)

	if got := nativeLuaStackTop(luaState); got != 1 {
		// 固定返回调用应只在当前 C frame 留下一个结果。
		t.Fatalf("visible top after fixed call = %d, want 1", got)
	}
	if value := state.ValueAt(-1); !value.RawEqual(runtime.IntegerValue(2)) {
		// 固定返回值必须按实参数量写回，证明 lua_callk 消费了当前 C frame 内的函数和参数。
		t.Fatalf("fixed call result = %#v, want 2", value)
	}
	if value := state.ValueAt(1); !value.RawEqual(runtime.StringValue("outer-sentinel")) {
		// 外层 Go VM 栈值不能被 fixed call 或后续构造期操作覆盖。
		t.Fatalf("outer sentinel after fixed call = %#v", value)
	}
	if errorObject, hasError := takeNativeStatePendingError(luaState); hasError {
		// 成功 fixed call 不应留下 pending error。
		t.Fatalf("fixed call pending error = %#v", errorObject)
	}

	if pointer := nativeLuaNewUserdata(luaState, 4); pointer == nil {
		// 构造期需要一个 native full userdata 承载 user value table。
		t.Fatalf("lua_newuserdata returned nil")
	}
	userdataValue := state.ValueAt(-1)
	userdata, ok := userdataValue.Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// native full userdata 必须能解析为 runtime.Userdata。
		t.Fatalf("userdata ref = %#v, want *runtime.Userdata", userdataValue.Ref)
	}
	nativeLuaCreateTable(luaState, 0, 1)
	userValueValue := state.ValueAt(-1)
	userValueTable, ok := userValueValue.Ref.(*runtime.Table)
	if !ok || userValueTable == nil {
		// lua_createtable 必须压入可挂接到 userdata 的 table。
		t.Fatalf("user value ref = %#v, want *runtime.Table", userValueValue.Ref)
	}
	userValueTable.RawSetString("target", runtime.ReferenceValue(runtime.KindTable, target))
	if got := nativeLuaSetUserValue(luaState, -2); got != 1 {
		// -2 当前指向刚创建的 userdata，栈顶 table 应挂为 user value。
		t.Fatalf("lua_setuservalue(userdata) = %d, want 1", got)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 当前 C frame 应保留 fixed call 结果和 userdata，不应残留临时 user value table。
		t.Fatalf("visible top after userdata construction = %d, want 2", got)
	}
	if got := state.StackTop(); got != baseTop+2 {
		// 全局栈应保留外层 sentinel 加当前 C frame 两个可见值。
		t.Fatalf("global top after userdata construction = %d, want %d", got, baseTop+2)
	}
	if userdata.UserValue.Kind != runtime.KindTable || userdata.UserValue.Ref != userValueTable {
		// userdata 必须持有刚构造出的 user value table identity。
		t.Fatalf("userdata user value = %#v, want %p", userdata.UserValue, userValueTable)
	}

	if removed := state.SweepWeakTables(); removed != 0 {
		// userdata 仍在当前 C frame 可见栈时，其 user value 间接引用的 weak value 不能被清理。
		t.Fatalf("weak sweep removed %d entries while constructed userdata is visible", removed)
	}
	if got := weakValueTable.RawGetString("from-fixed-call-userdata"); got.IsNil() {
		// fixed call 后构造出的 userdata/user value 关联边必须保活 target。
		t.Fatalf("weak value reachable through fixed-call userdata was removed")
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
