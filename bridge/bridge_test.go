package bridge

import (
	"errors"
	"strings"
	"testing"

	"github.com/zing/go-lua-vm/lua"
	"github.com/zing/go-lua-vm/runtime"
)

// TestRegisterFunctionReadsArgsAndPushesReturns 验证 Go bridge 函数注册、参数读取和返回值压入。
//
// bridge.Register 必须把 Go 回调注册为 Lua callable；回调通过 Context 读取参数并压入多返回值。
func TestRegisterFunctionReadsArgsAndPushesReturns(t *testing.T) {
	// 创建独立 State，避免全局环境污染其他测试。
	state := lua.NewState()
	defer state.Close()

	if err := Register(state, "sum", func(context *Context) error {
		// 读取两个 Lua integer 参数并压入 integer/string 两个返回值。
		left, leftOK := context.ToInteger(1)
		if !leftOK {
			// 第一个参数不是整数时返回 Lua error。
			return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "left integer expected"})
		}
		right, rightOK := context.ToInteger(2)
		if !rightOK {
			// 第二个参数不是整数时返回 Lua error。
			return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "right integer expected"})
		}
		context.PushInteger(left + right)
		context.PushString("ok")
		return nil
	}); err != nil {
		// 有效桥接函数注册不应失败。
		t.Fatalf("Register failed: %v", err)
	}

	functionValue, err := lua.GetGlobal(state, "sum")
	if err != nil {
		// 注册后的全局函数必须可读取。
		t.Fatalf("GetGlobal failed: %v", err)
	}
	results, err := lua.Call(state, functionValue, lua.Value{Kind: lua.KindInteger, Integer: 20}, lua.Value{Kind: lua.KindInteger, Integer: 22})
	if err != nil {
		// 桥接函数调用不应失败。
		t.Fatalf("Call failed: %v", err)
	}
	if len(results) != 2 {
		// 桥接返回值必须保留压入数量。
		t.Fatalf("result count = %d", len(results))
	}
	if results[0].Kind != lua.KindInteger || results[0].Integer != 42 {
		// 第一个返回值必须是求和结果。
		t.Fatalf("first result = %#v", results[0])
	}
	if results[1].Kind != lua.KindString || results[1].String != "ok" {
		// 第二个返回值必须是状态字符串。
		t.Fatalf("second result = %#v", results[1])
	}
}

// TestContextArgumentAndResultHelpers 验证 Context 参数读取与返回值 helper。
//
// 参数索引采用 Lua 1-based 语义，越界参数按 nil 处理；Results 返回副本避免外部篡改。
func TestContextArgumentAndResultHelpers(t *testing.T) {
	// 构造一个不依赖 State 的 Context，专注验证值转换 helper。
	context := NewContext(nil, lua.Value{Kind: lua.KindBoolean, Bool: true}, lua.Value{Kind: lua.KindInteger, Integer: 7}, lua.Value{Kind: lua.KindString, String: "name"})

	if !context.Arg(0).IsNil() {
		// 非法索引小于 1 时必须返回 nil。
		t.Fatalf("arg 0 should be nil")
	}
	if !context.Arg(4).IsNil() {
		// 超出参数数量时必须返回 nil。
		t.Fatalf("arg 4 should be nil")
	}
	if booleanValue, ok := context.ToBoolean(1); !ok || !booleanValue {
		// boolean true 参数必须按 Lua truthy 语义返回 true。
		t.Fatalf("ToBoolean = value=%v ok=%v", booleanValue, ok)
	}
	if integerValue, ok := context.ToInteger(2); !ok || integerValue != 7 {
		// integer 参数必须可读为 int64。
		t.Fatalf("ToInteger = value=%d ok=%v", integerValue, ok)
	}
	if stringValue, ok, err := context.ToString(3); err != nil || !ok || stringValue != "name" {
		// string 参数必须可读为自身内容。
		t.Fatalf("ToString = value=%q ok=%v err=%v", stringValue, ok, err)
	}

	context.PushNil()
	context.PushBoolean(false)
	context.PushInteger(8)
	context.PushNumber(1.5)
	context.PushString("done")
	results := context.Results()
	if len(results) != 5 {
		// 五次 Push 必须生成五个返回值。
		t.Fatalf("result count = %d", len(results))
	}
	results[0] = lua.Value{Kind: lua.KindString, String: "mutated"}
	if context.Results()[0].Kind != lua.KindNil {
		// Results 返回副本，外部修改不得影响 Context 内部返回值。
		t.Fatalf("Results should return a copy")
	}
}

// TestGoErrorMapsToLuaError 验证 Go error 会映射为 Lua error object。
//
// 普通 Go error 必须转换为 RuntimeError，同时保留 errors.Is 对原始错误的判断能力。
func TestGoErrorMapsToLuaError(t *testing.T) {
	// sentinel 用于验证错误链是否保留原始 Go error。
	sentinel := errors.New("go failure")
	state := lua.NewState()
	defer state.Close()

	if err := Register(state, "fail", func(context *Context) error {
		// 返回普通 Go error，桥接层应转换为 Lua RuntimeError。
		return sentinel
	}); err != nil {
		// 有效函数注册不应失败。
		t.Fatalf("Register failed: %v", err)
	}
	functionValue, err := lua.GetGlobal(state, "fail")
	if err != nil {
		// 注册后的全局函数必须可读取。
		t.Fatalf("GetGlobal failed: %v", err)
	}
	_, err = lua.Call(state, functionValue)
	if !errors.Is(err, sentinel) {
		// 错误链必须保留原始 Go error。
		t.Fatalf("Call error = %v, want sentinel", err)
	}
	errorObject := lua.ErrorObject(err)
	if errorObject.Kind != lua.KindString || errorObject.String != sentinel.Error() {
		// Lua error object 必须保存普通 Go error 文本。
		t.Fatalf("error object = %#v", errorObject)
	}
}

// TestGoPanicRecoverMapsToLuaError 验证 Go panic 会在 bridge 边界转换为 Lua RuntimeError。
//
// panic 不得穿透 lua.Call；返回错误应可通过 Lua error object 获取 panic 文本。
func TestGoPanicRecoverMapsToLuaError(t *testing.T) {
	// 创建独立 State 并注册会 panic 的桥接函数。
	state := lua.NewState()
	defer state.Close()

	if err := Register(state, "explode", func(context *Context) error {
		// panic 用于模拟宿主回调内部异常。
		panic("boom")
	}); err != nil {
		// 有效函数注册不应失败。
		t.Fatalf("Register failed: %v", err)
	}
	functionValue, err := lua.GetGlobal(state, "explode")
	if err != nil {
		// 注册后的全局函数必须可读取。
		t.Fatalf("GetGlobal failed: %v", err)
	}
	_, err = lua.Call(state, functionValue)
	if err == nil {
		// panic 必须转换为错误返回。
		t.Fatalf("Call should return panic error")
	}
	errorObject := lua.ErrorObject(err)
	if errorObject.Kind != lua.KindString || errorObject.String != "boom" {
		// Lua error object 必须保存 panic 文本。
		t.Fatalf("panic error object = %#v", errorObject)
	}
}

// TestCallableFromValueAndCallGlobal 验证 Lua 函数值可保存为 Go callable 并从全局环境调用。
//
// Go closure 当前应可直接执行；Lua closure 可保存，但调用时仍返回执行器未接入错误。
func TestCallableFromValueAndCallGlobal(t *testing.T) {
	// 创建独立 State 并注册一个可执行 Go closure。
	state := lua.NewState()
	defer state.Close()
	if err := Register(state, "double", func(context *Context) error {
		// 读取第一个整数参数并压入翻倍结果。
		integerValue, ok := context.ToInteger(1)
		if !ok {
			// 参数不是整数时返回 Lua error。
			return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "integer expected"})
		}
		context.PushInteger(integerValue * 2)
		return nil
	}); err != nil {
		// 有效函数注册不应失败。
		t.Fatalf("Register failed: %v", err)
	}

	functionValue, err := lua.GetGlobal(state, "double")
	if err != nil {
		// 注册后的全局函数必须可读取。
		t.Fatalf("GetGlobal failed: %v", err)
	}
	callable, err := FromValue(state, functionValue)
	if err != nil {
		// Go closure 必须可保存为 Callable。
		t.Fatalf("FromValue failed: %v", err)
	}
	results, err := callable.Call(lua.Value{Kind: lua.KindInteger, Integer: 21})
	if err != nil {
		// Callable 调用 Go closure 不应失败。
		t.Fatalf("Callable call failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != lua.KindInteger || results[0].Integer != 42 {
		// Callable 返回值必须保留 Go closure 结果。
		t.Fatalf("Callable results = %#v", results)
	}

	globalResults, err := CallGlobal(state, "double", lua.Value{Kind: lua.KindInteger, Integer: 5})
	if err != nil {
		// CallGlobal 调用全局 Go closure 不应失败。
		t.Fatalf("CallGlobal failed: %v", err)
	}
	if len(globalResults) != 1 || globalResults[0].Integer != 10 {
		// CallGlobal 返回值必须保留全局函数结果。
		t.Fatalf("CallGlobal results = %#v", globalResults)
	}

	if _, err := FromValue(nil, functionValue); !errors.Is(err, lua.ErrNilState) {
		// nil State 不能保存 callable。
		t.Fatalf("FromValue nil state error = %v", err)
	}
	if _, err := FromValue(state, lua.Value{Kind: lua.KindInteger, Integer: 1}); !errors.Is(err, lua.ErrExpectedCallable) {
		// 非函数值不能保存为 callable。
		t.Fatalf("FromValue non-callable error = %v", err)
	}
	if _, err := CallGlobal(state, "missing"); !errors.Is(err, lua.ErrExpectedCallable) {
		// 缺失全局函数读取为 nil，调用时应返回不可调用错误。
		t.Fatalf("CallGlobal missing error = %v", err)
	}

	if err := lua.LoadString(state, "return 1", ""); err != nil {
		// 合法 Lua closure 加载不应失败。
		t.Fatalf("LoadString failed: %v", err)
	}
	luaClosure := state.ValueAt(-1)
	luaCallable, err := FromValue(state, luaClosure)
	if err != nil {
		// Lua closure 也必须可保存为 Go callable。
		t.Fatalf("FromValue Lua closure failed: %v", err)
	}
	luaResults, err := luaCallable.Call()
	if err != nil {
		// Lua closure callable 现在应通过最小 VM 执行循环运行。
		t.Fatalf("Lua callable call failed: %v", err)
	}
	if len(luaResults) != 1 || luaResults[0].Kind != lua.KindInteger || luaResults[0].Integer != 1 {
		// return 1 必须保留单个整数返回值。
		t.Fatalf("Lua callable results = %#v", luaResults)
	}
}

// TestCallTableMethod 验证 Go 调 Lua table method 的第一阶段能力。
//
// method 字段使用 raw string 读取，调用时自动把 table 本身作为 self 参数传入。
func TestCallTableMethod(t *testing.T) {
	// 创建 table 并写入一个 Go closure 方法。
	state := lua.NewState()
	defer state.Close()
	tableObject := runtime.NewTable()
	tableValue := runtime.ReferenceValue(runtime.KindTable, tableObject)
	methodValue := runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 第一个参数必须是 self table，第二个参数用于返回。
		if len(args) < 2 {
			// 参数不足时返回 Lua error。
			return nil, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "missing argument"})
		}
		if args[0].Kind != runtime.KindTable || args[0].Ref != tableObject {
			// self 必须是被调用的 table。
			return nil, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "invalid self"})
		}
		return []runtime.Value{args[1], runtime.StringValue("method")}, nil
	}))
	tableObject.RawSetString("echo", methodValue)

	results, err := CallTableMethod(state, tableValue, "echo", lua.Value{Kind: lua.KindString, String: "payload"})
	if err != nil {
		// table method 调用不应失败。
		t.Fatalf("CallTableMethod failed: %v", err)
	}
	if len(results) != 2 {
		// method 必须返回两个值。
		t.Fatalf("result count = %d", len(results))
	}
	if results[0].Kind != lua.KindString || results[0].String != "payload" {
		// 第一个返回值必须是原始 payload。
		t.Fatalf("first result = %#v", results[0])
	}
	if results[1].Kind != lua.KindString || results[1].String != "method" {
		// 第二个返回值必须是方法标记。
		t.Fatalf("second result = %#v", results[1])
	}

	if _, err := CallTableMethod(nil, tableValue, "echo"); !errors.Is(err, lua.ErrNilState) {
		// nil State 必须被拒绝。
		t.Fatalf("CallTableMethod nil state error = %v", err)
	}
	if _, err := CallTableMethod(state, lua.Value{Kind: lua.KindInteger, Integer: 1}, "echo"); err == nil {
		// 非 table 值必须返回错误。
		t.Fatalf("CallTableMethod non-table should fail")
	}
	if _, err := CallTableMethod(state, tableValue, "missing"); !errors.Is(err, lua.ErrExpectedCallable) {
		// 缺失方法字段读取为 nil，调用时应返回不可调用错误。
		t.Fatalf("CallTableMethod missing method error = %v", err)
	}
}

// TestNestedGoLuaGoCallback 验证 Go -> Lua -> Go 嵌套回调的第一阶段链路。
//
// 当前阶段 Lua callable 由 Go closure 表示；外层 Go 调用全局 callable，最终回到另一个 Go 回调。
func TestNestedGoLuaGoCallback(t *testing.T) {
	// 注册 inner 和 outer 两个桥接函数，outer 通过 Context.CallGlobal 调用 inner。
	state := lua.NewState()
	defer state.Close()
	if err := Register(state, "inner", func(context *Context) error {
		// inner 读取整数参数并返回加一结果。
		value, ok := context.ToInteger(1)
		if !ok {
			// 参数不是整数时返回 Lua error。
			return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "integer expected"})
		}
		context.PushInteger(value + 1)
		return nil
	}); err != nil {
		// inner 注册不应失败。
		t.Fatalf("Register inner failed: %v", err)
	}
	if err := Register(state, "outer", func(context *Context) error {
		// outer 形成 Go -> callable -> Go 的嵌套调用链。
		results, err := context.CallGlobal("inner", lua.Value{Kind: lua.KindInteger, Integer: 41})
		if err != nil {
			// 嵌套调用失败时向外传播错误。
			return err
		}
		context.PushValue(results[0])
		context.PushString("nested")
		return nil
	}); err != nil {
		// outer 注册不应失败。
		t.Fatalf("Register outer failed: %v", err)
	}

	results, err := CallGlobal(state, "outer")
	if err != nil {
		// 外层调用不应失败。
		t.Fatalf("CallGlobal outer failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != 42 || results[1].String != "nested" {
		// 嵌套链路必须保留 inner 和 outer 的返回值。
		t.Fatalf("nested results = %#v", results)
	}
}

// TestNestedLuaGoLuaCallback 验证 Lua -> Go -> Lua 嵌套回调的第一阶段链路。
//
// 当前阶段 Lua callable 由传入的 Go closure 表示；Go 回调读取 callable 参数并再次调用它。
func TestNestedLuaGoLuaCallback(t *testing.T) {
	// 注册 caller，它从第一个参数读取 callable 并传入第二个参数调用。
	state := lua.NewState()
	defer state.Close()
	if err := Register(state, "caller", func(context *Context) error {
		// 第一个参数必须是可回调函数。
		callable, ok, err := context.ToCallable(1)
		if err != nil || !ok {
			// callable 参数无效时返回转换错误。
			return err
		}
		results, err := context.Call(callable, context.Arg(2))
		if err != nil {
			// 回调失败时向外传播错误。
			return err
		}
		for _, result := range results {
			// 透传回调返回值，模拟 Lua -> Go -> Lua 的返回传播。
			context.PushValue(result)
		}
		return nil
	}); err != nil {
		// caller 注册不应失败。
		t.Fatalf("Register caller failed: %v", err)
	}

	callback := lua.Value{Kind: lua.KindGoClosure, Ref: lua.GoResultsFunction(func(args ...lua.Value) ([]lua.Value, error) {
		// 回调读取参数并返回字符串标记。
		return []lua.Value{args[0], lua.Value{Kind: lua.KindString, String: "callback"}}, nil
	})}
	results, err := CallGlobal(state, "caller", callback, lua.Value{Kind: lua.KindInteger, Integer: 7})
	if err != nil {
		// caller 调用不应失败。
		t.Fatalf("CallGlobal caller failed: %v", err)
	}
	if len(results) != 2 || results[0].Integer != 7 || results[1].String != "callback" {
		// 嵌套回调必须透传 callback 返回值。
		t.Fatalf("callback results = %#v", results)
	}
}

// TestYieldPolicyDocumentsCurrentBoundary 验证当前 bridge yield 策略。
//
// 在 coroutine 可恢复调用帧接入前，跨 Go/Lua 边界 yield 必须显式禁止。
func TestYieldPolicyDocumentsCurrentBoundary(t *testing.T) {
	// 新建 Context 并检查默认 yield 策略。
	context := NewContext(nil)
	if context.YieldPolicy() != YieldForbidden {
		// 当前阶段不允许 yield 穿越 Go 回调边界。
		t.Fatalf("yield policy = %s", context.YieldPolicy())
	}
	if YieldAllowed == YieldForbidden {
		// 允许策略必须与禁止策略区分，便于后续扩展。
		t.Fatalf("yield policies should be distinct")
	}
}

// TestBindStructObjectProxyPropertiesAndMethods 验证 Go 对象显式绑定、userdata 代理和属性/方法转发。
//
// 绑定对象必须通过隐藏 userdata 保留 identity；Lua table 的 __index/__newindex 必须分别转发
// 显式方法、getter 和 setter，不允许未声明字段写入。
func TestBindStructObjectProxyPropertiesAndMethods(t *testing.T) {
	// sampleObject 用于验证 Go 对象 identity、字段读取和方法副作用。
	type sampleObject struct {
		// name 表示可读写字符串属性。
		name string
		// count 表示可读取并可由方法修改的计数。
		count int64
	}

	// 创建独立 State 和待绑定对象。
	state := lua.NewState()
	defer state.Close()
	object := &sampleObject{name: "lua", count: 1}

	proxyValue, err := BindStruct(state, ObjectBinding{
		Name:   "sample",
		Object: object,
		Methods: map[string]ObjectMethod{
			"inc": func(context *ObjectContext) error {
				// 断言 ObjectContext 暴露的对象 identity 与绑定对象一致。
				boundObject, ok := context.Object().(*sampleObject)
				if !ok {
					// 对象类型不匹配说明绑定配置损坏。
					return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "invalid object"})
				}
				delta, deltaOK := context.ToInteger(1)
				if !deltaOK {
					// 参数不是整数时返回 Lua error。
					return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "integer delta expected"})
				}
				boundObject.count += delta
				context.PushInteger(boundObject.count)
				return nil
			},
		},
		Getters: map[string]PropertyGetter{
			"name": func(object any) (lua.Value, error) {
				// name getter 只读取显式绑定对象。
				return lua.Value{Kind: lua.KindString, String: object.(*sampleObject).name}, nil
			},
			"count": func(object any) (lua.Value, error) {
				// count getter 用于确认方法副作用可被属性读取观察。
				return lua.Value{Kind: lua.KindInteger, Integer: object.(*sampleObject).count}, nil
			},
		},
		Setters: map[string]PropertySetter{
			"name": func(object any, value lua.Value) error {
				if value.Kind != lua.KindString {
					// name 只接受字符串写入。
					return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "string name expected"})
				}
				object.(*sampleObject).name = value.String
				return nil
			},
		},
	})
	if err != nil {
		// 有效对象绑定不应失败。
		t.Fatalf("BindStruct failed: %v", err)
	}
	if proxyValue.Kind != lua.KindTable {
		// 对 Lua 侧公开的代理必须是 table，便于属性和方法访问。
		t.Fatalf("proxy kind = %v", proxyValue.Kind)
	}

	tableObject, ok := proxyValue.Ref.(*runtime.Table)
	if !ok {
		// table 引用必须是 runtime.Table。
		t.Fatalf("proxy table ref = %#v", proxyValue.Ref)
	}
	userdataValue := tableObject.RawGetString("__userdata")
	if userdataValue.Kind != lua.KindUserdata {
		// 隐藏 userdata 字段必须保留 Go identity。
		t.Fatalf("userdata kind = %v", userdataValue.Kind)
	}
	userdataObject, ok := userdataValue.Ref.(*runtime.Userdata)
	if !ok {
		// userdata 引用必须是 runtime.Userdata。
		t.Fatalf("userdata ref = %#v", userdataValue.Ref)
	}
	proxyObject, ok := userdataObject.Data.(*ObjectProxy)
	if !ok {
		// userdata 负载必须指回 ObjectProxy。
		t.Fatalf("userdata data = %#v", userdataObject.Data)
	}
	if proxyObject.Object() != object {
		// 代理必须保留原始 Go 对象 identity。
		t.Fatalf("proxy object identity mismatch")
	}

	nameValue, err := tableObject.Get(runtime.StringValue("name"))
	if err != nil {
		// getter 读取不应失败。
		t.Fatalf("get name failed: %v", err)
	}
	if nameValue.Kind != lua.KindString || nameValue.String != "lua" {
		// name getter 必须返回初始对象字段。
		t.Fatalf("name value = %#v", nameValue)
	}

	if err := tableObject.Set(runtime.StringValue("name"), runtime.StringValue("go")); err != nil {
		// setter 写入不应失败。
		t.Fatalf("set name failed: %v", err)
	}
	if object.name != "go" {
		// setter 必须写回 Go 对象。
		t.Fatalf("object name = %q", object.name)
	}

	methodValue, err := tableObject.Get(runtime.StringValue("inc"))
	if err != nil {
		// 方法读取不应失败。
		t.Fatalf("get method failed: %v", err)
	}
	methodResults, err := lua.Call(state, methodValue, proxyValue, lua.Value{Kind: lua.KindInteger, Integer: 3})
	if err != nil {
		// method closure 调用不应失败。
		t.Fatalf("call method failed: %v", err)
	}
	if len(methodResults) != 1 || methodResults[0].Kind != lua.KindInteger || methodResults[0].Integer != 4 {
		// 方法返回值必须反映 Go 对象副作用。
		t.Fatalf("method results = %#v", methodResults)
	}
	countValue, err := tableObject.Get(runtime.StringValue("count"))
	if err != nil {
		// 方法副作用后的 getter 读取不应失败。
		t.Fatalf("get count failed: %v", err)
	}
	if countValue.Kind != lua.KindInteger || countValue.Integer != 4 {
		// count getter 必须观察到 inc 修改后的值。
		t.Fatalf("count value = %#v", countValue)
	}

	if err := tableObject.Set(runtime.StringValue("missing"), runtime.StringValue("x")); err == nil {
		// 未声明 setter 的属性写入必须失败，避免污染代理表。
		t.Fatalf("missing property write should fail")
	}
}

// TestBindStructRejectsInvalidInput 验证对象绑定入口的非法输入防护。
//
// nil State 和 nil Object 都必须返回稳定错误，不得构造半初始化代理。
func TestBindStructRejectsInvalidInput(t *testing.T) {
	if _, err := BindStruct(nil, ObjectBinding{Object: struct{}{}}); !errors.Is(err, lua.ErrNilState) {
		// nil State 必须被拒绝。
		t.Fatalf("BindStruct nil state error = %v", err)
	}

	state := lua.NewState()
	defer state.Close()
	if _, err := BindStruct(state, ObjectBinding{}); err == nil {
		// nil Object 必须被拒绝。
		t.Fatalf("BindStruct nil object should fail")
	}
}

// TestRegisterModuleWritesGlobalAndPackageLoaded 验证 Go API 可注册为 Lua 模块。
//
// 模块注册必须写入全局环境；当 package 标准库已打开时，还必须写入 package.loaded，使
// require 返回同一个模块表。
func TestRegisterModuleWritesGlobalAndPackageLoaded(t *testing.T) {
	// 创建 State 并打开 package 库，使 RegisterModule 可以同步 package.loaded。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	moduleValue, err := RegisterModule(state, ModuleBinding{
		Name: "gomod",
		Functions: map[string]Function{
			"add": func(context *Context) error {
				// add 读取两个整数并返回求和结果。
				left, leftOK := context.ToInteger(1)
				if !leftOK {
					// 左参数不是整数时返回 Lua error。
					return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "left integer expected"})
				}
				right, rightOK := context.ToInteger(2)
				if !rightOK {
					// 右参数不是整数时返回 Lua error。
					return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "right integer expected"})
				}
				context.PushInteger(left + right)
				return nil
			},
		},
		Objects: map[string]ObjectBinding{
			"counter": {
				Object: &struct {
					// value 表示测试对象当前计数。
					value int64
				}{value: 2},
				Methods: map[string]ObjectMethod{
					"read": func(context *ObjectContext) error {
						// read 返回绑定对象的当前计数。
						object := context.Object().(*struct{ value int64 })
						context.PushInteger(object.value)
						return nil
					},
				},
			},
		},
	})
	if err != nil {
		// 有效模块注册不应失败。
		t.Fatalf("RegisterModule failed: %v", err)
	}
	if moduleValue.Kind != lua.KindTable {
		// 模块值必须是 table。
		t.Fatalf("module kind = %v", moduleValue.Kind)
	}

	globalValue, err := lua.GetGlobal(state, "gomod")
	if err != nil {
		// 全局模块读取不应失败。
		t.Fatalf("GetGlobal failed: %v", err)
	}
	if globalValue.Ref != moduleValue.Ref {
		// 全局环境必须保存同一个模块表实例。
		t.Fatalf("global module should share table ref")
	}

	moduleTable := moduleValue.Ref.(*runtime.Table)
	addValue := moduleTable.RawGetString("add")
	results, err := lua.Call(state, addValue, runtime.IntegerValue(20), runtime.IntegerValue(22))
	if err != nil {
		// 模块函数调用不应失败。
		t.Fatalf("module add call failed: %v", err)
	}
	if len(results) != 1 || results[0].Integer != 42 {
		// 模块函数必须返回求和结果。
		t.Fatalf("module add results = %#v", results)
	}

	requireResults, err := CallGlobal(state, "require", runtime.StringValue("gomod"))
	if err != nil {
		// require 已缓存模块时不应失败。
		t.Fatalf("require gomod failed: %v", err)
	}
	if len(requireResults) != 1 || requireResults[0].Ref != moduleValue.Ref {
		// require 必须返回 package.loaded 中的同一个模块表。
		t.Fatalf("require results = %#v", requireResults)
	}

	counterValue := moduleTable.RawGetString("counter")
	counterTable := counterValue.Ref.(*runtime.Table)
	readValue, err := counterTable.Get(runtime.StringValue("read"))
	if err != nil {
		// 对象方法读取不应失败。
		t.Fatalf("counter read get failed: %v", err)
	}
	readResults, err := lua.Call(state, readValue, counterValue)
	if err != nil {
		// 对象方法调用不应失败。
		t.Fatalf("counter read call failed: %v", err)
	}
	if len(readResults) != 1 || readResults[0].Integer != 2 {
		// 对象方法必须返回绑定对象字段。
		t.Fatalf("counter read results = %#v", readResults)
	}
}

// TestRegisterFunctionAndValueOfUserdata 验证统一封装 API 的全局函数和 userdata 注入。
//
// RegisterFunction 必须把 bridge.Function 注册到全局环境；ValueOf 注入 userdata 时必须纳入
// State.Close 生命周期，保证宿主资源关闭路径可被统一触发。
func TestRegisterFunctionAndValueOfUserdata(t *testing.T) {
	// 创建独立 State，避免全局函数和 userdata 生命周期影响其他测试。
	state := lua.NewState()
	if err := RegisterFunction(state, "triple", func(context *Context) error {
		// triple 读取整数参数并返回三倍结果。
		value, ok := context.ToInteger(1)
		if !ok {
			// 参数不是整数时返回 Lua error。
			return lua.RaiseError(runtime.StringValue("integer expected"))
		}
		context.PushInteger(value * 3)
		return nil
	}); err != nil {
		// 有效全局函数注册不应失败。
		t.Fatalf("RegisterFunction failed: %v", err)
	}
	results, err := CallGlobal(state, "triple", runtime.IntegerValue(14))
	if err != nil {
		// 全局函数调用不应失败。
		t.Fatalf("CallGlobal triple failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != lua.KindInteger || results[0].Integer != 42 {
		// 全局函数必须返回 Go 回调压入值。
		t.Fatalf("triple results = %#v", results)
	}

	closed := false
	userdata := runtime.NewUserdataWithFinalizer("resource", func(payload any) error {
		// finalizer 必须在 State.Close 时被触发。
		if payload != "resource" {
			// payload 不匹配时返回错误；runtime 关闭路径会隔离该错误。
			return errors.New("unexpected payload")
		}
		closed = true
		return nil
	})
	userdataValue, err := ValueOf(state, userdata)
	if err != nil {
		// userdata 转换不应失败。
		t.Fatalf("ValueOf userdata failed: %v", err)
	}
	if userdataValue.Kind != lua.KindUserdata || userdataValue.Ref != userdata {
		// ValueOf 必须保留 userdata identity。
		t.Fatalf("userdata value = %#v", userdataValue)
	}
	state.Close()
	if !closed {
		// ValueOf 注入的 userdata 必须被 State.Close 统一关闭。
		t.Fatalf("userdata finalizer was not called")
	}
}

// TestRegisterModulePreloadAndInjectedValues 验证统一封装 API 的 preload、常量、变量和嵌套 table。
//
// Go 模块通过 package.preload 延迟加载；模块 table 支持函数、常量、变量、嵌套 table 和 package.loaded
// 缓存，常量写入必须失败，变量写入必须成功。
func TestRegisterModulePreloadAndInjectedValues(t *testing.T) {
	// 创建 State 并打开标准库，使 package.preload 和 require 可用。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库打开不应失败。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	if err := RegisterModulePreload(state, ModuleBinding{
		Name: "gomod.preload",
		Constants: map[string]any{
			"version": "1.0.0",
		},
		Variables: map[string]any{
			"enabled": true,
		},
		Functions: map[string]Function{
			"add": func(context *Context) error {
				// add 读取两个整数并返回求和结果。
				left, leftOK := context.ToInteger(1)
				if !leftOK {
					// 左参数不是整数时返回 Lua error。
					return lua.RaiseError(runtime.StringValue("left integer expected"))
				}
				right, rightOK := context.ToInteger(2)
				if !rightOK {
					// 右参数不是整数时返回 Lua error。
					return lua.RaiseError(runtime.StringValue("right integer expected"))
				}
				context.PushInteger(left + right)
				return nil
			},
		},
		Tables: map[string]TableBinding{
			"nested": {
				Fields: map[string]any{
					"name": "child",
				},
			},
		},
	}); err != nil {
		// preload 注册不应失败。
		t.Fatalf("RegisterModulePreload failed: %v", err)
	}

	packageValue, err := lua.GetGlobal(state, "package")
	if err != nil {
		// package 全局读取不应失败。
		t.Fatalf("GetGlobal package failed: %v", err)
	}
	packageTable := packageValue.Ref.(*runtime.Table)
	preloadTable := packageTable.RawGetString("preload").Ref.(*runtime.Table)
	if got := preloadTable.RawGetString("gomod.preload"); got.Kind != lua.KindGoClosure {
		// package.preload 必须保存 Go loader。
		t.Fatalf("package.preload loader = %#v", got)
	}

	requireResults, err := CallGlobal(state, "require", runtime.StringValue("gomod.preload"))
	if err != nil {
		// require preload 模块不应失败。
		t.Fatalf("require preload failed: %v", err)
	}
	if len(requireResults) != 1 || requireResults[0].Kind != lua.KindTable {
		// require 必须返回模块 table。
		t.Fatalf("require results = %#v", requireResults)
	}
	moduleTable := requireResults[0].Ref.(*runtime.Table)

	versionValue, err := moduleTable.Get(runtime.StringValue("version"))
	if err != nil {
		// 常量读取不应失败。
		t.Fatalf("get version failed: %v", err)
	}
	if versionValue.Kind != lua.KindString || versionValue.String != "1.0.0" {
		// 常量值必须通过 __index 可见。
		t.Fatalf("version value = %#v", versionValue)
	}
	if err := moduleTable.Set(runtime.StringValue("version"), runtime.StringValue("2.0.0")); err == nil {
		// 常量字段必须拒绝 Lua 侧覆盖。
		t.Fatalf("constant write should fail")
	}
	if err := moduleTable.Set(runtime.StringValue("enabled"), runtime.BooleanValue(false)); err != nil {
		// 变量字段必须允许覆盖。
		t.Fatalf("variable write failed: %v", err)
	}
	if enabledValue := moduleTable.RawGetString("enabled"); enabledValue.Kind != lua.KindBoolean || enabledValue.Bool {
		// 变量覆盖后必须保存新值。
		t.Fatalf("enabled value = %#v", enabledValue)
	}

	addValue := moduleTable.RawGetString("add")
	addResults, err := lua.Call(state, addValue, runtime.IntegerValue(2), runtime.IntegerValue(5))
	if err != nil {
		// 模块函数调用不应失败。
		t.Fatalf("add call failed: %v", err)
	}
	if len(addResults) != 1 || addResults[0].Integer != 7 {
		// 函数返回值必须保留 Go 回调结果。
		t.Fatalf("add results = %#v", addResults)
	}

	nestedValue := moduleTable.RawGetString("nested")
	nestedTable := nestedValue.Ref.(*runtime.Table)
	if nameValue := nestedTable.RawGetString("name"); nameValue.Kind != lua.KindString || nameValue.String != "child" {
		// 嵌套 table 字段必须可读取。
		t.Fatalf("nested name = %#v", nameValue)
	}
	loadedTable := packageTable.RawGetString("loaded").Ref.(*runtime.Table)
	if loadedValue := loadedTable.RawGetString("gomod.preload"); loadedValue.Ref != requireResults[0].Ref {
		// require 必须把 loader 返回值写入 package.loaded。
		t.Fatalf("package.loaded module = %#v", loadedValue)
	}
}

// TestBuildTableMetatableFallbackAndErrorBoundaries 验证 table 元表 fallback 与封装 API 错误边界。
//
// 常量保护应与宿主提供的 __index fallback 共存；非法注册输入和不支持的 Go 值类型必须返回错误。
func TestBuildTableMetatableFallbackAndErrorBoundaries(t *testing.T) {
	// 创建 State 和宿主 fallback table，用于验证 __index 合并策略。
	state := lua.NewState()
	defer state.Close()
	fallbackTable := runtime.NewTable()
	fallbackTable.RawSetString("fallback", runtime.StringValue("from-metatable"))

	tableValue, err := BuildTable(state, TableBinding{
		Name: "with-fallback",
		Constants: map[string]any{
			"answer": int64(42),
		},
		Metatable: map[string]any{
			"__index": runtime.ReferenceValue(runtime.KindTable, fallbackTable),
		},
	})
	if err != nil {
		// 带 fallback 的 table 构造不应失败。
		t.Fatalf("BuildTable fallback failed: %v", err)
	}
	tableObject := tableValue.Ref.(*runtime.Table)
	answerValue, err := tableObject.Get(runtime.StringValue("answer"))
	if err != nil {
		// 常量读取不应失败。
		t.Fatalf("get answer failed: %v", err)
	}
	if answerValue.Kind != lua.KindInteger || answerValue.Integer != 42 {
		// 常量读取必须优先于 fallback。
		t.Fatalf("answer value = %#v", answerValue)
	}
	fallbackValue, err := tableObject.Get(runtime.StringValue("fallback"))
	if err != nil {
		// fallback 读取不应失败。
		t.Fatalf("get fallback failed: %v", err)
	}
	if fallbackValue.Kind != lua.KindString || fallbackValue.String != "from-metatable" {
		// 未命中常量时必须继续走宿主 __index fallback。
		t.Fatalf("fallback value = %#v", fallbackValue)
	}

	if err := RegisterFunction(state, "", func(context *Context) error {
		// 该回调不会执行，仅用于构造非 nil 函数。
		return nil
	}); err == nil {
		// 空全局函数名必须被拒绝。
		t.Fatalf("RegisterFunction empty name should fail")
	}
	noPackageState := lua.NewState()
	defer noPackageState.Close()
	if err := RegisterModulePreload(noPackageState, ModuleBinding{Name: "no.package"}); err == nil {
		// 未打开 package 库时不能注册 package.preload。
		t.Fatalf("RegisterModulePreload without package should fail")
	}
	if _, err := ValueOf(state, struct{}{}); err == nil {
		// 不支持的 Go 类型必须显式拒绝，避免隐式反射绑定扩散。
		t.Fatalf("ValueOf unsupported type should fail")
	}
}

// TestBuildReadOnlyTableAndObjectFinalizer 验证只读 table、对象冒号调用和 State.Close 生命周期关闭。
//
// 只读 table 的字段通过 __index 暴露，任何 Lua 侧写入都会失败；对象代理通过 hidden userdata 保留
// identity，并在 State.Close 时执行显式 finalizer。
func TestBuildReadOnlyTableAndObjectFinalizer(t *testing.T) {
	// sampleObject 用于验证对象方法副作用和关闭回调。
	type sampleObject struct {
		// closed 标记 finalizer 是否已执行。
		closed bool
		// value 保存对象方法返回的数值。
		value int64
	}

	state := lua.NewState()
	object := &sampleObject{value: 9}
	tableValue, err := BuildTable(state, TableBinding{
		Name:     "readonly",
		ReadOnly: true,
		Fields: map[string]any{
			"label": "locked",
		},
		Objects: map[string]ObjectBinding{
			"counter": {
				Object: object,
				Methods: map[string]ObjectMethod{
					"read": func(context *ObjectContext) error {
						// read 返回绑定对象当前值，验证冒号调用 self 会被包装器移除。
						context.PushInteger(context.Object().(*sampleObject).value)
						return nil
					},
				},
				Finalizer: func(object any) error {
					// finalizer 在 State.Close 阶段标记对象已关闭。
					object.(*sampleObject).closed = true
					return nil
				},
			},
		},
	})
	if err != nil {
		// 只读 table 构造不应失败。
		t.Fatalf("BuildTable failed: %v", err)
	}
	tableObject := tableValue.Ref.(*runtime.Table)
	labelValue, err := tableObject.Get(runtime.StringValue("label"))
	if err != nil {
		// 只读 backing storage 读取不应失败。
		t.Fatalf("get label failed: %v", err)
	}
	if labelValue.Kind != lua.KindString || labelValue.String != "locked" {
		// 只读字段必须通过 __index 可见。
		t.Fatalf("label value = %#v", labelValue)
	}
	if err := tableObject.Set(runtime.StringValue("label"), runtime.StringValue("open")); err == nil {
		// 只读 table 必须拒绝覆盖已有字段。
		t.Fatalf("readonly existing write should fail")
	}
	if err := tableObject.Set(runtime.StringValue("newField"), runtime.StringValue("open")); err == nil {
		// 只读 table 必须拒绝新增字段。
		t.Fatalf("readonly new field write should fail")
	}

	counterValue, err := tableObject.Get(runtime.StringValue("counter"))
	if err != nil {
		// 嵌套对象读取不应失败。
		t.Fatalf("get counter failed: %v", err)
	}
	counterTable := counterValue.Ref.(*runtime.Table)
	readValue, err := counterTable.Get(runtime.StringValue("read"))
	if err != nil {
		// 对象方法读取不应失败。
		t.Fatalf("get read failed: %v", err)
	}
	readResults, err := lua.Call(state, readValue, counterValue)
	if err != nil {
		// 冒号调用形式的方法不应失败。
		t.Fatalf("read call failed: %v", err)
	}
	if len(readResults) != 1 || readResults[0].Kind != lua.KindInteger || readResults[0].Integer != 9 {
		// 对象方法必须返回绑定对象值。
		t.Fatalf("read results = %#v", readResults)
	}

	state.Close()
	if !object.closed {
		// State.Close 必须触发对象 finalizer。
		t.Fatalf("object finalizer was not called")
	}
}

// TestGenerateLuaStub 验证 Lua stub 生成的代理结构。
//
// stub 必须包含模块函数、对象属性代理和对象方法代理，并保持函数名排序稳定。
func TestGenerateLuaStub(t *testing.T) {
	stubSource, err := GenerateLuaStub(ModuleBinding{
		Name: "gomod",
		Functions: map[string]Function{
			"zeta":  func(context *Context) error { return nil },
			"alpha": func(context *Context) error { return nil },
		},
		Objects: map[string]ObjectBinding{
			"counter": {
				Methods: map[string]ObjectMethod{
					"read": func(context *ObjectContext) error { return nil },
				},
				Getters: map[string]PropertyGetter{
					"value": func(object any) (lua.Value, error) { return runtime.IntegerValue(0), nil },
				},
				Setters: map[string]PropertySetter{
					"value": func(object any, value lua.Value) error { return nil },
				},
			},
		},
	})
	if err != nil {
		// 有效模块 stub 生成不应失败。
		t.Fatalf("GenerateLuaStub failed: %v", err)
	}
	if !strings.Contains(stubSource, "function M.alpha(...)") {
		// stub 必须包含 alpha 函数代理。
		t.Fatalf("stub missing alpha function:\n%s", stubSource)
	}
	if !strings.Contains(stubSource, "function M.zeta(...)") {
		// stub 必须包含 zeta 函数代理。
		t.Fatalf("stub missing zeta function:\n%s", stubSource)
	}
	if strings.Index(stubSource, "function M.alpha(...)") > strings.Index(stubSource, "function M.zeta(...)") {
		// 函数输出必须按名称排序，保证 golden 稳定。
		t.Fatalf("stub functions are not sorted:\n%s", stubSource)
	}
	if !strings.Contains(stubSource, "__go_bridge_property(\"gomod.counter\", key)") {
		// 对象属性读取代理必须生成。
		t.Fatalf("stub missing property proxy:\n%s", stubSource)
	}
	if !strings.Contains(stubSource, "__go_bridge_set_property(\"gomod.counter\", key, value)") {
		// 对象属性写入代理必须生成。
		t.Fatalf("stub missing property setter proxy:\n%s", stubSource)
	}
	if !strings.Contains(stubSource, "function M.counter:read(...)") {
		// 对象方法代理必须生成冒号调用形式。
		t.Fatalf("stub missing object method:\n%s", stubSource)
	}
	if !strings.HasSuffix(stubSource, "return M\n") {
		// stub 必须返回模块表。
		t.Fatalf("stub should return module table:\n%s", stubSource)
	}

	if _, err := GenerateLuaStub(ModuleBinding{}); err == nil {
		// 空模块名不能生成 stub。
		t.Fatalf("GenerateLuaStub empty module should fail")
	}
}

// TestRegisterRejectsInvalidInput 验证 Register 对非法输入的防护。
//
// nil State 和 nil Function 都必须返回稳定错误，不得产生 panic。
func TestRegisterRejectsInvalidInput(t *testing.T) {
	if err := Register(nil, "bad", func(context *Context) error {
		// 该回调不会执行，仅用于构造非 nil 函数。
		return nil
	}); !errors.Is(err, lua.ErrNilState) {
		// nil State 必须被拒绝。
		t.Fatalf("Register nil state error = %v", err)
	}

	state := lua.NewState()
	defer state.Close()
	if err := Register(state, "bad", nil); !errors.Is(err, lua.ErrExpectedCallable) {
		// nil Function 必须被拒绝。
		t.Fatalf("Register nil function error = %v", err)
	}
}
