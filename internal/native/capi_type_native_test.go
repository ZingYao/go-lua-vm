//go:build native_modules

package native

import (
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

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
