//go:build cgo

package native

import (
	"testing"
	"unsafe"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestNativeLuaVersionReturnsLua53Constant 验证 lua_version 返回 Lua 5.3 版本静态地址。
func TestNativeLuaVersionReturnsLua53Constant(t *testing.T) {
	// Lua 5.3 的 luaL_checkversion_ 会比较 lua_version(L) 与 lua_version(NULL) 的地址和值。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证有效 State 路径。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()

	stateVersion := nativeLuaVersion(handle.pointer())
	nilVersion := nativeLuaVersion(nil)
	if stateVersion == nil {
		// 有效 State 查询必须返回静态版本地址。
		t.Fatalf("nativeLuaVersion state returned nil")
	}
	if nilVersion == nil {
		// nil State 查询同样必须返回静态版本地址。
		t.Fatalf("nativeLuaVersion nil returned nil")
	}
	if stateVersion != nilVersion {
		// Lua 5.3 要求同一 ABI 的 lua_version 指针地址一致，供 lauxlib 版本检查使用。
		t.Fatalf("nativeLuaVersion state pointer = %p, nil pointer = %p, want same", stateVersion, nilVersion)
	}
	if got := *(*float64)(stateVersion); got != 503 {
		// Lua 5.3 public header 的 LUA_VERSION_NUM 固定为 503。
		t.Fatalf("nativeLuaVersion value = %v, want 503", got)
	}
}

// TestNativeCAPITypeAndNumberPrimitives 验证类型、truthiness 和 number 转换 shim。
func TestNativeCAPITypeAndNumberPrimitives(t *testing.T) {
	// 使用真实 State 和 opaque handle 验证 C API helper 对 Go VM 栈的读取语义。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 type shim。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushNil(luaState)
	nativeLuaPushBoolean(luaState, false)
	nativeLuaPushBoolean(luaState, true)
	nativeLuaPushInteger(luaState, 7)
	nativeLuaPushNumber(luaState, 2.5)
	nativeLuaPushString(luaState, nil)
	nativeLuaCreateTable(luaState, 0, 0)

	typeCases := []struct {
		name  string
		index int
		want  int
	}{
		{name: "nil", index: 1, want: nativeLuaTypeNil},
		{name: "false", index: 2, want: nativeLuaTypeBoolean},
		{name: "true", index: 3, want: nativeLuaTypeBoolean},
		{name: "integer", index: 4, want: nativeLuaTypeNumber},
		{name: "number", index: 5, want: nativeLuaTypeNumber},
		{name: "string nil push", index: 6, want: nativeLuaTypeNil},
		{name: "table", index: 7, want: nativeLuaTypeTable},
		{name: "top negative", index: -1, want: nativeLuaTypeTable},
		{name: "missing", index: 8, want: nativeLuaTypeNone},
		{name: "zero", index: 0, want: nativeLuaTypeNone},
	}
	for _, tc := range typeCases {
		// 每个索引都必须按 Lua C API 基础类型编号返回。
		if got := nativeLuaType(luaState, tc.index); got != tc.want {
			t.Fatalf("nativeLuaType %s = %d, want %d", tc.name, got, tc.want)
		}
	}

	booleanCases := []struct {
		name  string
		index int
		want  bool
	}{
		{name: "nil", index: 1, want: false},
		{name: "false", index: 2, want: false},
		{name: "true", index: 3, want: true},
		{name: "integer", index: 4, want: true},
		{name: "number", index: 5, want: true},
		{name: "nil from pushstring null", index: 6, want: false},
		{name: "table", index: 7, want: true},
		{name: "missing", index: 8, want: false},
	}
	for _, tc := range booleanCases {
		// Lua 5.3 只有 nil 和 false 为假，其余值为真。
		if got := nativeLuaToBoolean(luaState, tc.index); got != tc.want {
			t.Fatalf("nativeLuaToBoolean %s = %v, want %v", tc.name, got, tc.want)
		}
	}

	stringCases := []struct {
		name  string
		index int
		want  bool
	}{
		{name: "nil", index: 1, want: false},
		{name: "false", index: 2, want: false},
		{name: "integer", index: 4, want: true},
		{name: "number", index: 5, want: true},
		{name: "nil from pushstring null", index: 6, want: false},
		{name: "table", index: 7, want: false},
		{name: "missing", index: 8, want: false},
	}
	for _, tc := range stringCases {
		// lua_isstring 对 string 和 number 为真，对 nil、boolean、table、none 为假。
		if got := nativeLuaIsString(luaState, tc.index); got != tc.want {
			t.Fatalf("nativeLuaIsString %s = %v, want %v", tc.name, got, tc.want)
		}
	}
	textBytes := []byte{'l', 'p', 'e', 'g', 0}
	nativeLuaPushString(luaState, unsafe.Pointer(&textBytes[0]))
	if got := nativeLuaIsString(luaState, -1); !got {
		// 普通 string 必须可作为字符串读取。
		t.Fatalf("nativeLuaIsString string = %v, want true", got)
	}
	numericText := []byte{' ', '0', 'x', '1', 'p', '+', '2', ' ', 0}
	nativeLuaPushString(luaState, unsafe.Pointer(&numericText[0]))
	if got := nativeLuaIsString(nil, 1); got {
		// nil State 不能读取任何 string。
		t.Fatalf("nativeLuaIsString nil state = %v, want false", got)
	}

	numberCases := []struct {
		name    string
		index   int
		wantNum bool
		wantInt bool
	}{
		{name: "nil", index: 1, wantNum: false, wantInt: false},
		{name: "boolean", index: 2, wantNum: false, wantInt: false},
		{name: "integer", index: 4, wantNum: true, wantInt: true},
		{name: "float number", index: 5, wantNum: true, wantInt: false},
		{name: "table", index: 7, wantNum: false, wantInt: false},
		{name: "plain string", index: 8, wantNum: false, wantInt: false},
		{name: "numeric string", index: 9, wantNum: true, wantInt: false},
		{name: "missing", index: 10, wantNum: false, wantInt: false},
	}
	for _, tc := range numberCases {
		// lua_isnumber 使用 number 可转换性；lua_isinteger 只接受真实 integer 表示。
		if got := nativeLuaIsNumber(luaState, tc.index); got != tc.wantNum {
			t.Fatalf("nativeLuaIsNumber %s = %v, want %v", tc.name, got, tc.wantNum)
		}
		if got := nativeLuaIsInteger(luaState, tc.index); got != tc.wantInt {
			t.Fatalf("nativeLuaIsInteger %s = %v, want %v", tc.name, got, tc.wantInt)
		}
	}
	if got := nativeLuaIsNumber(nil, 1); got {
		// nil State 不能读取任何 number。
		t.Fatalf("nativeLuaIsNumber nil state = %v, want false", got)
	}
	if got := nativeLuaIsInteger(nil, 1); got {
		// nil State 不能读取任何 integer。
		t.Fatalf("nativeLuaIsInteger nil state = %v, want false", got)
	}

	if pointer := nativeLuaNewUserdata(luaState, 4); pointer == nil {
		// full userdata 构造失败时无法验证 lua_isuserdata。
		t.Fatal("nativeLuaNewUserdata returned nil")
	}
	lightMarker := 73
	nativeLuaPushLightUserdata(luaState, unsafe.Pointer(&lightMarker))
	userdataCases := []struct {
		name  string
		index int
		want  bool
	}{
		{name: "nil", index: 1, want: false},
		{name: "integer", index: 4, want: false},
		{name: "table", index: 7, want: false},
		{name: "plain string", index: 8, want: false},
		{name: "full userdata", index: 10, want: true},
		{name: "light userdata", index: 11, want: true},
		{name: "missing", index: 12, want: false},
	}
	for _, tc := range userdataCases {
		// lua_isuserdata 对 full userdata 和 lightuserdata 为真，其余类型和 none 为假。
		if got := nativeLuaIsUserdata(luaState, tc.index); got != tc.want {
			t.Fatalf("nativeLuaIsUserdata %s = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := nativeLuaIsUserdata(nil, 1); got {
		// nil State 不能读取任何 userdata。
		t.Fatalf("nativeLuaIsUserdata nil state = %v, want false", got)
	}

	nativeLuaPushCClosure(luaState, luaState, 0)
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 测试只关心值类型；该函数不会被实际调用。
		return args, nil
	})))
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindLuaClosure, &runtime.LuaClosure{}))
	callableTable := runtime.NewTable()
	callMetatable := runtime.NewTable()
	callMetatable.RawSetString("__call", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 测试只关心带 __call 的 table 不应被误判为 C function。
		return args, nil
	})))
	callableTable.SetMetatable(callMetatable)
	nativeLuaPushValue(luaState, runtime.ReferenceValue(runtime.KindTable, callableTable))
	cFunctionCases := []struct {
		name  string
		index int
		want  bool
	}{
		{name: "nil", index: 1, want: false},
		{name: "integer", index: 4, want: false},
		{name: "table", index: 7, want: false},
		{name: "full userdata", index: 10, want: false},
		{name: "light userdata", index: 11, want: false},
		{name: "native C closure", index: 12, want: true},
		{name: "host Go closure", index: 13, want: true},
		{name: "Lua closure", index: 14, want: false},
		{name: "callable table", index: 15, want: false},
		{name: "missing", index: 16, want: false},
	}
	for _, tc := range cFunctionCases {
		// lua_iscfunction 只接受宿主侧 C/Go closure，不触发 __call，也不把 Lua closure 误判为 C function。
		if got := nativeLuaIsCFunction(luaState, tc.index); got != tc.want {
			t.Fatalf("nativeLuaIsCFunction %s = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := nativeLuaIsCFunction(nil, 1); got {
		// nil State 不能读取任何 C function。
		t.Fatalf("nativeLuaIsCFunction nil state = %v, want false", got)
	}

	numberValue, ok := nativeLuaToNumber(luaState, 4)
	if !ok || numberValue != 7 {
		// integer 必须可按 number 读取。
		t.Fatalf("nativeLuaToNumber integer = value=%v ok=%v, want 7 true", numberValue, ok)
	}
	numberValue, ok = nativeLuaToNumber(luaState, 5)
	if !ok || numberValue != 2.5 {
		// float number 必须原样返回。
		t.Fatalf("nativeLuaToNumber number = value=%v ok=%v, want 2.5 true", numberValue, ok)
	}
	numberValue, ok = nativeLuaToNumber(luaState, 9)
	if !ok || numberValue != 4 {
		// Lua 5.3 C API 要求 numeric string 可按 number 读取，十六进制浮点也应复用 runtime 解析。
		t.Fatalf("nativeLuaToNumber numeric string = value=%v ok=%v, want 4 true", numberValue, ok)
	}
	if value := state.ValueAt(9); value.Kind != runtime.KindString || value.String != " 0x1p+2 " {
		// lua_tonumberx 不应改变栈中原始字符串值。
		t.Fatalf("nativeLuaToNumber numeric string stack slot = %#v, want original string", value)
	}
	numberValue, ok = nativeLuaToNumber(luaState, 7)
	if ok || numberValue != 0 {
		// table 不能转换为 number。
		t.Fatalf("nativeLuaToNumber table = value=%v ok=%v, want 0 false", numberValue, ok)
	}
	numberValue, ok = nativeLuaToNumber(nil, 1)
	if ok || numberValue != 0 {
		// nil State 不能读取任何 number。
		t.Fatalf("nativeLuaToNumber nil state = value=%v ok=%v, want 0 false", numberValue, ok)
	}
}

// TestNativeCAPITypeRegistryPseudoIndex 验证 registry pseudo-index 的类型暴露。
func TestNativeCAPITypeRegistryPseudoIndex(t *testing.T) {
	// registry 是当前唯一公开的 pseudo-index，类型查询必须返回 table。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 registry 类型。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	if got := nativeLuaType(luaState, runtime.RegistryPseudoIndex); got != nativeLuaTypeTable {
		// registry pseudo-index 在 Lua C API 中表现为 table。
		t.Fatalf("nativeLuaType registry = %d, want %d", got, nativeLuaTypeTable)
	}
	if got := nativeLuaType(luaState, runtime.RegistryPseudoIndex-1); got != nativeLuaTypeNone {
		// 未支持的 pseudo-index 不能误判为 nil。
		t.Fatalf("nativeLuaType unsupported pseudo = %d, want %d", got, nativeLuaTypeNone)
	}
}
