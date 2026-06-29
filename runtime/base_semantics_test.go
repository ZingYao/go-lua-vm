package runtime

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zing/go-lua-vm/bytecode"
)

// TestVMCallMetamethod 验证 CALL 会把带 `__call` 的 table 转换为元方法调用请求。
//
// Lua 5.3 的 tryfuncTM 会把原被调对象插入为第一个参数；当前最小 VM 只生成调用请求，
// 因此测试寄存器重排和 CallRequest 字段即可。
func TestVMCallMetamethod(t *testing.T) {
	vm := NewVM(5)
	callableValue := tableValueWithMetamethod(metamethodCall, func(args ...Value) (Value, error) {
		// __call 函数体不会在 Step 阶段执行，后续调用执行器消费 CallRequest。
		return IntegerValue(int64(len(args))), nil
	})
	if err := vm.SetRegister(0, callableValue); err != nil {
		// 测试准备阶段写入被调对象必须成功。
		t.Fatalf("set callable register failed: %v", err)
	}
	if err := vm.SetRegister(1, StringValue("a")); err != nil {
		// 测试准备阶段写入第一个参数必须成功。
		t.Fatalf("set first argument failed: %v", err)
	}
	if err := vm.SetRegister(2, StringValue("b")); err != nil {
		// 测试准备阶段写入第二个参数必须成功。
		t.Fatalf("set second argument failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpCall, 0, 3, 2)); err != nil {
		// CALL 遇到带 __call 的 table 时必须生成元方法调用请求。
		t.Fatalf("call metamethod failed: %v", err)
	}
	request := vm.LastCallRequest()
	if request == nil || request.FunctionIndex != 0 || request.ArgumentCount != 3 || request.ReturnCount != 1 {
		// __call 会额外插入原被调对象，因此参数数量从 2 变为 3。
		t.Fatalf("call metamethod request mismatch: %#v", request)
	}
	functionValue, functionOK := vm.Register(0)
	selfValue, selfOK := vm.Register(1)
	firstArgument, firstOK := vm.Register(2)
	secondArgument, secondOK := vm.Register(3)
	if !functionOK || functionValue.Kind != KindGoClosure {
		// 函数槽必须替换为 __call 元方法。
		t.Fatalf("call metamethod function mismatch: value=%#v ok=%v", functionValue, functionOK)
	}
	if !selfOK || !selfValue.RawEqual(callableValue) {
		// 第一个参数必须是原被调对象。
		t.Fatalf("call metamethod self mismatch: value=%#v ok=%v", selfValue, selfOK)
	}
	if !firstOK || !firstArgument.RawEqual(StringValue("a")) || !secondOK || !secondArgument.RawEqual(StringValue("b")) {
		// 原始参数必须整体右移且保持顺序。
		t.Fatalf("call metamethod arguments mismatch: first=%#v ok=%v second=%#v ok=%v", firstArgument, firstOK, secondArgument, secondOK)
	}
}

// TestVMCallMetamethodExtendsRegisterWindow 验证 `__call` 插入 self 时会按需扩展寄存器窗口。
//
// Lua 5.3 的 `tryfuncTM` 会把原被调对象插入到函数槽后一位；当 CALL 参数刚好占满当前
// Proto.MaxStackSize 时，运行时仍需要临时栈位完成元方法调用，而不能报告寄存器越界。
func TestVMCallMetamethodExtendsRegisterWindow(t *testing.T) {
	vm := NewVM(3)
	callableValue := tableValueWithMetamethod(metamethodCall, func(args ...Value) (Value, error) {
		// __call 函数体不会在 Step 阶段执行，测试只关注调用请求和窗口扩展。
		return IntegerValue(int64(len(args))), nil
	})
	if err := vm.SetRegister(0, callableValue); err != nil {
		// 测试准备阶段写入被调对象必须成功。
		t.Fatalf("set callable register failed: %v", err)
	}
	if err := vm.SetRegister(1, StringValue("a")); err != nil {
		// 测试准备阶段写入第一个参数必须成功。
		t.Fatalf("set first argument failed: %v", err)
	}
	if err := vm.SetRegister(2, StringValue("b")); err != nil {
		// 测试准备阶段写入第二个参数必须成功。
		t.Fatalf("set second argument failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpCall, 0, 3, 2)); err != nil {
		// CALL 元方法插入 self 时应扩展寄存器窗口，而不是返回越界。
		t.Fatalf("call metamethod with tight registers failed: %v", err)
	}
	if vm.RegisterCount() != 4 {
		// 两个原始参数加一个 self 需要把窗口从 3 扩展到 4。
		t.Fatalf("register count mismatch: %d", vm.RegisterCount())
	}
	request := vm.LastCallRequest()
	if request == nil || request.FunctionIndex != 0 || request.ArgumentCount != 3 || request.ReturnCount != 1 {
		// 扩展窗口后仍必须生成与普通 `__call` 相同的调用请求。
		t.Fatalf("call metamethod request mismatch: %#v", request)
	}
}

// TestVMBasicTypeMetatableIndexAndLength 验证基础类型共享元表参与索引和长度运算。
//
// 官方 events.lua 会通过 debug.setmetatable 给 number 挂载 `__index` 与 `__len`，随后执行
// `(10)[3]` 和 `#3.45`；VM 必须在非 table 值上查找类型级元表并调用对应元方法。
func TestVMBasicTypeMetatableIndexAndLength(t *testing.T) {
	defer SetBasicTypeMetatable(IntegerValue(0), nil)

	metatable := NewTable()
	metatable.RawSetString(tableIndexMetamethodKey, ReferenceValue(KindGoClosure, GoResultsFunction(func(args ...Value) ([]Value, error) {
		// __index 接收原始 number 与 key，测试中返回两者相加的结果。
		if len(args) != 2 {
			// 参数数量错误会让测试直接暴露 VM 调用约定问题。
			return nil, fmt.Errorf("index args mismatch: %d", len(args))
		}
		return []Value{IntegerValue(args[0].Integer + args[1].Integer)}, nil
	})))
	metatable.RawSetString(metamethodLen, ReferenceValue(KindGoClosure, GoResultsFunction(func(args ...Value) ([]Value, error) {
		// __len 接收原始 number，返回 floor 后的 integer。
		if len(args) != 1 {
			// 参数数量错误会让测试直接暴露 VM 调用约定问题。
			return nil, fmt.Errorf("len args mismatch: %d", len(args))
		}
		return []Value{IntegerValue(int64(args[0].Number))}, nil
	})))
	SetBasicTypeMetatable(IntegerValue(10), metatable)

	vm := NewVM(3)
	if err := vm.SetRegister(0, IntegerValue(10)); err != nil {
		// 测试准备阶段写入 receiver 必须成功。
		t.Fatalf("set receiver failed: %v", err)
	}
	if err := vm.SetRegister(1, IntegerValue(3)); err != nil {
		// 测试准备阶段写入 key 必须成功。
		t.Fatalf("set key failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpGetTable, 2, 0, 1)); err != nil {
		// GETTABLE 必须通过 number 类型级 __index 完成读取。
		t.Fatalf("number index failed: %v", err)
	}
	value, ok := vm.Register(2)
	if !ok || !value.RawEqual(IntegerValue(13)) {
		// (10)[3] 应通过 __index 返回 13。
		t.Fatalf("number index result=%#v ok=%v", value, ok)
	}

	if err := vm.SetRegister(0, NumberValue(3.45)); err != nil {
		// 测试准备阶段写入长度操作数必须成功。
		t.Fatalf("set length operand failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpLen, 1, 0, 0)); err != nil {
		// LEN 必须通过 number 类型级 __len 完成。
		t.Fatalf("number len failed: %v", err)
	}
	value, ok = vm.Register(1)
	if !ok || !value.RawEqual(IntegerValue(3)) {
		// #3.45 应返回元方法计算出的 3。
		t.Fatalf("number len result=%#v ok=%v", value, ok)
	}
}

// TestToStringMetamethod 验证 ToString 会调用 `__tostring` 并校验返回值类型。
//
// 该 helper 是后续 base.tostring 标准库的运行时语义入口；元方法返回非 string 时必须报错。
func TestToStringMetamethod(t *testing.T) {
	tableValue := tableValueWithMetamethod(metamethodToString, func(args ...Value) (Value, error) {
		// __tostring 返回稳定字符串，验证 ToString 优先使用元方法。
		return StringValue("custom"), nil
	})
	result, err := ToString(tableValue)
	if err != nil {
		// 合法 __tostring 必须转换成功。
		t.Fatalf("tostring metamethod failed: %v", err)
	}
	if !result.RawEqual(StringValue("custom")) {
		// ToString 必须返回 __tostring 的字符串结果。
		t.Fatalf("tostring metamethod result mismatch: %#v", result)
	}

	invalidValue := tableValueWithMetamethod(metamethodToString, func(args ...Value) (Value, error) {
		// 返回非 string 用于验证 Lua 5.3 的类型错误边界。
		return IntegerValue(53), nil
	})
	if _, err := ToString(invalidValue); !errors.Is(err, ErrToStringMetamethod) {
		// __tostring 返回非 string 必须返回明确错误。
		t.Fatalf("tostring invalid result error mismatch: %v", err)
	}
}

// TestToStringReferenceValuesUseLuaPrefixes 验证引用值 tostring 使用 Lua 风格前缀。
//
// Lua 5.3 官方 strings.lua 只要求结果包含 `table:` 和 `function:`；地址部分可由宿主决定。
func TestToStringReferenceValuesUseLuaPrefixes(t *testing.T) {
	// table 默认 tostring 必须带 table 前缀，而不是内部 DebugString。
	tableResult, err := ToString(ReferenceValue(KindTable, NewTable()))
	if err != nil {
		// 普通 table tostring 不应失败。
		t.Fatalf("table tostring failed: %v", err)
	}
	if !strings.HasPrefix(tableResult.String, "table:") {
		// 官方测试通过 string.find(tostring{}, "table:") 验证该前缀。
		t.Fatalf("table tostring = %q", tableResult.String)
	}

	// Go closure 默认 tostring 必须带 function 前缀。
	functionResult, err := ToString(ReferenceValue(KindGoClosure, GoResultsFunction(func(args ...Value) ([]Value, error) {
		// 函数体不会执行，只用于提供引用负载。
		return nil, nil
	})))
	if err != nil {
		// 普通 function tostring 不应失败。
		t.Fatalf("function tostring failed: %v", err)
	}
	if !strings.HasPrefix(functionResult.String, "function:") {
		// 官方测试通过 string.find(tostring(print), "function:") 验证该前缀。
		t.Fatalf("function tostring = %q", functionResult.String)
	}
}

// TestToStringUsesMetatableName 验证 tostring 回退路径使用元表 `__name` 前缀。
//
// Lua 5.3 官方 strings.lua 会通过 string.format("%.4s", value) 验证缺少 `__tostring` 时
// 元表 `__name` 参与基础 tostring 文本。
func TestToStringUsesMetatableName(t *testing.T) {
	// 构造带 __name 的 table 元表，且不设置 __tostring。
	table := NewTable()
	metatable := NewTable()
	metatable.RawSetString("__name", StringValue("hi"))
	table.SetMetatable(metatable)

	result, err := ToString(ReferenceValue(KindTable, table))
	if err != nil {
		// 只有 __name 不应触发元方法调用错误。
		t.Fatalf("tostring with __name failed: %v", err)
	}
	if !strings.HasPrefix(result.String, "hi: ") {
		// Lua 5.3 期望 `__name` 覆盖可见类型名前缀。
		t.Fatalf("tostring with __name = %q", result.String)
	}
}

// TestLuaErrorTypeNameUsesMetatableName 验证错误类型名读取元表 `__name`。
//
// value 必须是带 metatable 的引用值；字符串型 `__name` 会覆盖基础 table/userdata 类型名，
// 无 `__name` 时仍回退到 LuaTypeName。
func TestLuaErrorTypeNameUsesMetatableName(t *testing.T) {
	// 构造带 __name 的 table，用于模拟官方 errors.lua 的 named object。
	table := NewTable()
	metatable := NewTable()
	metatable.RawSetString("__name", StringValue("My Type"))
	table.SetMetatable(metatable)
	if got := LuaErrorTypeName(ReferenceValue(KindTable, table)); got != "My Type" {
		// 错误类型名必须优先使用用户声明的 __name。
		t.Fatalf("LuaErrorTypeName named table = %q, want My Type", got)
	}

	plainTable := ReferenceValue(KindTable, NewTable())
	if got := LuaErrorTypeName(plainTable); got != "table" {
		// 无 __name 的引用值必须保留基础类型名。
		t.Fatalf("LuaErrorTypeName plain table = %q, want table", got)
	}
}

// TestPairsMetamethodAndRawIterator 验证 Pairs 支持 `__pairs` 和 raw next 回退。
//
// 有 `__pairs` 时返回元方法多返回值；无元方法时返回 raw pairs 迭代器、table 和 nil key。
func TestPairsMetamethodAndRawIterator(t *testing.T) {
	customValue := tableValueWithResultsMetamethod(metamethodPairs, func(args ...Value) ([]Value, error) {
		// __pairs 返回自定义三元组，验证多返回值元方法通道。
		return []Value{StringValue("iter"), StringValue("state"), StringValue("init")}, nil
	})
	customResults, err := Pairs(customValue)
	if err != nil {
		// 自定义 __pairs 必须执行成功。
		t.Fatalf("custom pairs failed: %v", err)
	}
	if len(customResults) != 3 || !customResults[0].RawEqual(StringValue("iter")) || !customResults[1].RawEqual(StringValue("state")) || !customResults[2].RawEqual(StringValue("init")) {
		// Pairs 必须保留 __pairs 的全部返回值。
		t.Fatalf("custom pairs results mismatch: %#v", customResults)
	}

	table := NewTable()
	table.RawSetString("name", StringValue("lua"))
	tableValue := ReferenceValue(KindTable, table)
	rawResults, err := Pairs(tableValue)
	if err != nil {
		// 无 __pairs 的 table 必须走 raw pairs 回退。
		t.Fatalf("raw pairs failed: %v", err)
	}
	if len(rawResults) != 3 || rawResults[0].Kind != KindGoClosure || !rawResults[1].RawEqual(tableValue) || !rawResults[2].IsNil() {
		// raw pairs 三元组必须是 iterator、state table、nil 初始 key。
		t.Fatalf("raw pairs triple mismatch: %#v", rawResults)
	}
	nextResults, err := callGoClosureResults(rawResults[0], rawResults[1], rawResults[2])
	if err != nil {
		// raw pairs 迭代器必须能读取第一项。
		t.Fatalf("raw pairs iterator failed: %v", err)
	}
	if len(nextResults) != 2 || !nextResults[0].RawEqual(StringValue("name")) || !nextResults[1].RawEqual(StringValue("lua")) {
		// 第一项必须返回写入的 key/value。
		t.Fatalf("raw pairs iterator result mismatch: %#v", nextResults)
	}
}

// TestPairsWithStateCallsLuaMetamethod 验证带 State 的 pairs 能调用 Lua closure 型 `__pairs`。
//
// base 标准库注册的 pairs 会捕获当前 State；当用户元表中提供 Lua closure 时，必须通过
// State runner 保留元方法多返回值，而不是误报 table expected。
func TestPairsWithStateCallsLuaMetamethod(t *testing.T) {
	state := NewState()
	defer state.Close()

	table := NewTable()
	tableValue := ReferenceValue(KindTable, table)
	luaMethod := ReferenceValue(KindLuaClosure, &LuaClosure{})
	metatable := NewTable()
	metatable.RawSetString(metamethodPairs, luaMethod)
	table.SetMetatable(metatable)

	state.SetLuaMetamethodRunner(func(method Value, name string, args ...Value) ([]Value, error) {
		if !method.RawEqual(luaMethod) || len(args) != 1 || !args[0].RawEqual(tableValue) {
			// runner 必须收到元方法本体和 pairs 参数本身。
			t.Fatalf("lua pairs runner arguments mismatch: method=%#v name=%q args=%#v", method, name, args)
		}
		return []Value{StringValue("iter"), tableValue, IntegerValue(0)}, nil
	})

	results, err := PairsWithState(state, tableValue)
	if err != nil {
		// Lua closure 型 `__pairs` 必须执行成功。
		t.Fatalf("pairs with lua metamethod failed: %v", err)
	}
	if len(results) != 3 || !results[0].RawEqual(StringValue("iter")) || !results[1].RawEqual(tableValue) || !results[2].RawEqual(IntegerValue(0)) {
		// 元方法多返回值必须原样返回给 generic for。
		t.Fatalf("pairs with lua metamethod results mismatch: %#v", results)
	}
}

// TestIPairsMetamethodAndRawIterator 验证 IPairs 支持 `__ipairs` 和 raw integer 前缀回退。
//
// Lua 5.3 保留 `__ipairs` 兼容入口；无元方法时从索引 1 开始迭代直到第一个 nil。
func TestIPairsMetamethodAndRawIterator(t *testing.T) {
	customValue := tableValueWithResultsMetamethod(metamethodIPairs, func(args ...Value) ([]Value, error) {
		// __ipairs 返回自定义三元组，验证兼容入口优先级。
		return []Value{StringValue("iiter"), StringValue("istate"), IntegerValue(7)}, nil
	})
	customResults, err := IPairs(customValue)
	if err != nil {
		// 自定义 __ipairs 必须执行成功。
		t.Fatalf("custom ipairs failed: %v", err)
	}
	if len(customResults) != 3 || !customResults[0].RawEqual(StringValue("iiter")) || !customResults[1].RawEqual(StringValue("istate")) || !customResults[2].RawEqual(IntegerValue(7)) {
		// IPairs 必须保留 __ipairs 的全部返回值。
		t.Fatalf("custom ipairs results mismatch: %#v", customResults)
	}

	table := NewTable()
	table.RawSetInteger(1, StringValue("first"))
	tableValue := ReferenceValue(KindTable, table)
	rawResults, err := IPairs(tableValue)
	if err != nil {
		// 无 __ipairs 的 table 必须走 raw ipairs 回退。
		t.Fatalf("raw ipairs failed: %v", err)
	}
	if len(rawResults) != 3 || rawResults[0].Kind != KindGoClosure || !rawResults[1].RawEqual(tableValue) || !rawResults[2].RawEqual(IntegerValue(0)) {
		// raw ipairs 三元组必须是 iterator、state table、0 初始索引。
		t.Fatalf("raw ipairs triple mismatch: %#v", rawResults)
	}
	nextResults, err := callGoClosureResults(rawResults[0], rawResults[1], rawResults[2])
	if err != nil {
		// raw ipairs 迭代器必须能读取第一项。
		t.Fatalf("raw ipairs iterator failed: %v", err)
	}
	if len(nextResults) != 2 || !nextResults[0].RawEqual(IntegerValue(1)) || !nextResults[1].RawEqual(StringValue("first")) {
		// 第一项必须返回索引 1 和对应值。
		t.Fatalf("raw ipairs iterator result mismatch: %#v", nextResults)
	}
}

// TestIPairsWithStateUsesIndexMetamethod 验证带 State 的 ipairs 迭代器会触发 `__index`。
//
// Lua 5.3 的 ipairs 辅助函数通过普通索引读取连续整数键；当数组槽位为空但 `__index`
// 提供值时，迭代必须继续并返回该值。
func TestIPairsWithStateUsesIndexMetamethod(t *testing.T) {
	state := NewState()
	defer state.Close()

	table := NewTable()
	tableValue := ReferenceValue(KindTable, table)
	luaIndex := ReferenceValue(KindLuaClosure, &LuaClosure{})
	metatable := NewTable()
	metatable.RawSetString(tableIndexMetamethodKey, luaIndex)
	table.SetMetatable(metatable)

	state.SetLuaMetamethodRunner(func(method Value, name string, args ...Value) ([]Value, error) {
		if !method.RawEqual(luaIndex) || len(args) != 2 || !args[0].RawEqual(tableValue) {
			// runner 必须收到 __index 元方法、原 table 和当前整数索引。
			t.Fatalf("lua index runner arguments mismatch: method=%#v name=%q args=%#v", method, name, args)
		}
		indexValue, ok := args[1].ToInteger()
		if !ok {
			// ipairs 传给 __index 的键必须是 integer。
			t.Fatalf("lua index key is not integer: %#v", args[1])
		}
		if indexValue <= 3 {
			// 前三个索引通过元方法合成非 nil 值。
			return []Value{IntegerValue(indexValue * 10)}, nil
		}
		return []Value{NilValue()}, nil
	})

	results, err := IPairsWithState(state, tableValue)
	if err != nil {
		// 带 State 的 ipairs 三元组生成必须成功。
		t.Fatalf("ipairs with state failed: %v", err)
	}
	if len(results) != 3 || results[0].Kind != KindGoClosure || !results[1].RawEqual(tableValue) || !results[2].RawEqual(IntegerValue(0)) {
		// 返回布局必须仍是 iterator、state、0。
		t.Fatalf("ipairs with state triple mismatch: %#v", results)
	}

	firstResults, err := callGoClosureResults(results[0], results[1], results[2])
	if err != nil {
		// 第一次迭代必须能通过 __index 读取索引 1。
		t.Fatalf("ipairs with state iterator failed: %v", err)
	}
	if len(firstResults) != 2 || !firstResults[0].RawEqual(IntegerValue(1)) || !firstResults[1].RawEqual(IntegerValue(10)) {
		// ipairs 返回的 key/value 必须来自普通索引访问结果。
		t.Fatalf("ipairs with state first result mismatch: %#v", firstResults)
	}
}

// TestRaiseErrorPreservesLuaObject 验证 RaiseError 会保留任意 Lua error object。
//
// Lua `error` 可以抛出任意值，包括 nil；错误链仍应可通过 ErrLuaError 分类识别。
func TestRaiseErrorPreservesLuaObject(t *testing.T) {
	stringErr := RaiseError(StringValue("boom"))
	if !errors.Is(stringErr, ErrLuaError) {
		// RaiseError 的错误链必须支持 ErrLuaError 分类。
		t.Fatalf("raise error classification mismatch: %v", stringErr)
	}
	if !ErrorObject(stringErr).RawEqual(StringValue("boom")) {
		// ErrorObject 必须取回原始 Lua string 错误对象。
		t.Fatalf("raise error object mismatch: %#v", ErrorObject(stringErr))
	}

	nilErr := RaiseError(NilValue())
	if !errors.Is(nilErr, ErrLuaError) {
		// nil 错误对象同样必须保留分类。
		t.Fatalf("raise nil error classification mismatch: %v", nilErr)
	}
	if !ErrorObject(nilErr).IsNil() {
		// Lua error(nil) 的错误对象必须保持 nil。
		t.Fatalf("raise nil error object mismatch: %#v", ErrorObject(nilErr))
	}
}

// TestPCallCapturesSuccessAndError 验证 PCall 会返回 Lua 风格的成功标记和错误对象。
//
// 成功路径返回 true 加新增栈值；失败路径返回 false 加错误对象，并且不把 Go error 继续上抛。
func TestPCallCapturesSuccessAndError(t *testing.T) {
	state := NewState()
	successResults, err := PCall(state, func(protectedState *State) error {
		// 成功调用向栈顶压入一个返回值，PCall 需要收集该新增值。
		return protectedState.Push(StringValue("ok"))
	})
	if err != nil {
		// pcall 自身执行成功时不应返回 Go error。
		t.Fatalf("pcall success failed: %v", err)
	}
	if len(successResults) != 2 || !successResults[0].RawEqual(BooleanValue(true)) || !successResults[1].RawEqual(StringValue("ok")) {
		// 成功返回布局必须是 true 后接函数返回值。
		t.Fatalf("pcall success results mismatch: %#v", successResults)
	}

	errorResults, err := PCall(state, func(protectedState *State) error {
		// 失败调用抛出 Lua error object，PCall 需要转换成 false/object 返回。
		return RaiseError(StringValue("boom"))
	})
	if err != nil {
		// pcall 捕获运行时错误后不应继续上抛 Go error。
		t.Fatalf("pcall error should be captured: %v", err)
	}
	if len(errorResults) != 2 || !errorResults[0].RawEqual(BooleanValue(false)) || !errorResults[1].RawEqual(StringValue("boom")) {
		// 失败返回布局必须是 false 和原始 Lua error object。
		t.Fatalf("pcall error results mismatch: %#v", errorResults)
	}
}

// TestXPCallUsesErrorHandler 验证 XPCall 会用 handler 替换错误对象。
//
// xpcall 成功路径等同 pcall；失败路径会把原始错误对象交给 handler，并返回 handler 结果。
func TestXPCallUsesErrorHandler(t *testing.T) {
	state := NewState()
	results, err := XPCall(state, func(protectedState *State) error {
		// 触发 Lua error，验证 handler 能看到该对象。
		return RaiseError(StringValue("raw"))
	}, func(object Value) (Value, error) {
		// handler 把原始错误对象包装成新的字符串。
		if !object.RawEqual(StringValue("raw")) {
			t.Fatalf("xpcall handler object mismatch: %#v", object)
		}
		return StringValue("handled"), nil
	})
	if err != nil {
		// xpcall 捕获错误后不应上抛 Go error。
		t.Fatalf("xpcall failed: %v", err)
	}
	if len(results) != 2 || !results[0].RawEqual(BooleanValue(false)) || !results[1].RawEqual(StringValue("handled")) {
		// xpcall 失败返回布局必须是 false 和 handler 结果。
		t.Fatalf("xpcall results mismatch: %#v", results)
	}
}

// TestPanicAndGoErrorConversion 验证 panic 与普通 Go error 都会转换为 Lua error object。
//
// PanicToError 服务 ProtectedCall 的 recover 分支；LuaErrorFromGo 服务 Go bridge 返回 error
// 的边界，两者都必须保留可供 Lua 侧读取的错误对象。
func TestPanicAndGoErrorConversion(t *testing.T) {
	panicErr := PanicToError("boom")
	if panicErr == nil || !ErrorObject(panicErr).RawEqual(StringValue("boom")) {
		// panic 值必须转换成 Lua string error object。
		t.Fatalf("panic error object mismatch: err=%v object=%#v", panicErr, ErrorObject(panicErr))
	}
	if PanicToError(nil) != nil {
		// nil recovered 值表示没有 panic，不应构造错误。
		t.Fatalf("nil panic should not create error")
	}

	goErr := errors.New("go failure")
	luaErr := LuaErrorFromGo(goErr)
	if !errors.Is(luaErr, goErr) {
		// 普通 Go error 转 Lua error 后仍要保留 errors.Is 链路。
		t.Fatalf("go error chain mismatch: %v", luaErr)
	}
	if !ErrorObject(luaErr).RawEqual(StringValue("go failure")) {
		// 普通 Go error 的文本必须成为 Lua error object。
		t.Fatalf("go error object mismatch: %#v", ErrorObject(luaErr))
	}
}

// TestTracebackFormatsFrames 验证 Traceback 会按调用帧顺序拼接基础 traceback。
//
// 当前阶段尚未接入源码行号，因此测试只要求错误消息、固定标题和帧类型/函数展示稳定存在。
func TestTracebackFormatsFrames(t *testing.T) {
	frames := []CallFrame{
		NewGoCallFrame(ReferenceValue(KindGoClosure, "go"), 1, 0),
		NewLuaCallFrame(ReferenceValue(KindLuaClosure, "lua"), 1, 0),
	}
	text := Traceback("boom", frames)
	if !strings.Contains(text, "boom\nstack traceback:") {
		// traceback 必须包含错误消息和固定标题。
		t.Fatalf("traceback header mismatch: %q", text)
	}
	if !strings.Contains(text, "[go]") || !strings.Contains(text, "[lua]") {
		// traceback 必须包含调用帧类型，便于后续 debug 元信息扩展。
		t.Fatalf("traceback frames mismatch: %q", text)
	}
}

// TestTracebackCompactsDeepStacks 验证 Lua 5.3 深栈 traceback 折叠规则。
//
// 官方 db.lua 要求深栈 traceback 最多展示前 10 行和后 11 行，中间用 `...` 表示省略帧。
func TestTracebackCompactsDeepStacks(t *testing.T) {
	// 构造超过 Lua 5.3 折叠阈值的调用帧列表，模拟递归深栈。
	frames := make([]CallFrame, 0, 30)
	for frameIndex := 0; frameIndex < 30; frameIndex++ {
		// 每个帧使用不同函数名，便于后续确认头尾帧保留。
		frame := NewGoCallFrame(ReferenceValue(KindGoClosure, "fn"), 1, 0)
		frame.Name = fmt.Sprintf("fn%d", frameIndex)
		frame.NameWhat = "global"
		frames = append(frames, frame)
	}

	text := Traceback("boom", frames)
	if !strings.Contains(text, "\n\t...\n") {
		// 深栈 traceback 必须包含省略号行。
		t.Fatalf("traceback should contain ellipsis line: %q", text)
	}
	if strings.Count(text, "\n\t") != 22 {
		// 期望 10 个前段帧、1 个省略号行、11 个尾段帧。
		t.Fatalf("traceback line count mismatch: %q", text)
	}
	if strings.Contains(text, "fn10") || strings.Contains(text, "fn18") {
		// 中间段帧必须被折叠，不应继续出现在输出中。
		t.Fatalf("traceback should compact middle frames: %q", text)
	}
	if !strings.Contains(text, "fn0") || !strings.Contains(text, "fn29") {
		// 前段和尾段边界帧必须保留，便于定位当前点和调用入口。
		t.Fatalf("traceback should keep head and tail frames: %q", text)
	}
}

// TestTracebackFormatsLuaFrameSource 验证匿名 Lua 帧会展示 Proto source。
//
// debug.traceback(thread) 需要在协程挂起栈中匹配脚本文件名；没有调用点名称的 Lua 帧不能只输出
// ref(kind=6)。
func TestTracebackFormatsLuaFrameSource(t *testing.T) {
	// 构造带文件 source 的 Lua closure 调用帧。
	closure := &LuaClosure{Proto: &bytecode.Proto{Source: "@/tmp/db.lua"}}
	frame := NewLuaCallFrame(ReferenceValue(KindLuaClosure, closure), 1, 0)
	text := Traceback("", []CallFrame{frame})
	if !strings.Contains(text, "/tmp/db.lua") {
		// traceback 需要包含去掉 @ 前缀后的 source。
		t.Fatalf("traceback should contain lua source: %q", text)
	}
}

// TestTracebackFormatsNamedLuaFrameSource 验证命名 Lua 帧仍保留 Proto source。
//
// 官方 db.lua 的协程 traceback 既会匹配源码文件名，也会在递归帧中匹配函数名；命名帧不能因
// 写入 Name/NameWhat 而丢失 source 展示。
func TestTracebackFormatsNamedLuaFrameSource(t *testing.T) {
	// 构造带 upvalue 调用点名称和文件 source 的 Lua closure 调用帧。
	closure := &LuaClosure{Proto: &bytecode.Proto{Source: "@/tmp/db.lua"}}
	frame := NewLuaCallFrame(ReferenceValue(KindLuaClosure, closure), 1, 0)
	frame.Name = "f"
	frame.NameWhat = "upvalue"

	text := Traceback("", []CallFrame{frame})
	if !strings.Contains(text, "/tmp/db.lua") || !strings.Contains(text, "'f'") {
		// 命名帧必须同时包含 source 与函数名，满足官方 traceback 双重匹配。
		t.Fatalf("traceback should contain source and function name: %q", text)
	}
}

// TestTracebackFormatsEmptyMessageAndNameWhat 验证空消息和调用点来源格式。
//
// debug.traceback() 无消息时不应产生前导空行；hook 调用帧需要在帧行暴露 hook 字样。
func TestTracebackFormatsEmptyMessageAndNameWhat(t *testing.T) {
	// 构造带 hook namewhat 的 Lua 调用帧。
	frame := NewLuaCallFrame(ReferenceValue(KindLuaClosure, "hookfn"), 1, 0)
	frame.NameWhat = "hook"
	text := Traceback("", []CallFrame{frame})
	if strings.HasPrefix(text, "\n") || !strings.HasPrefix(text, "stack traceback:") {
		// 空消息 traceback 必须直接以标题开头。
		t.Fatalf("empty message traceback header mismatch: %q", text)
	}
	if !strings.Contains(text, "hook") {
		// hook 调用来源必须出现在帧行，供 debug 库测试匹配。
		t.Fatalf("traceback should contain hook namewhat: %q", text)
	}
}

// tableValueWithResultsMetamethod 创建带多返回值 Go 元方法的 table 值。
//
// name 是元方法字段名；function 是当前 VM 可直接调用的 GoResultsFunction。返回值可直接
// 写入 VM 寄存器或传给 runtime 语义 helper。
func tableValueWithResultsMetamethod(name string, function GoResultsFunction) Value {
	// 构造普通 table 作为带元方法的测试对象。
	table := NewTable()
	metatable := NewTable()
	metatable.RawSetString(name, ReferenceValue(KindGoClosure, function))
	table.SetMetatable(metatable)
	return ReferenceValue(KindTable, table)
}
