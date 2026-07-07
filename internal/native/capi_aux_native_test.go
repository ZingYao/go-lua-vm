//go:build native_modules

package native

import (
	"testing"
	"unsafe"

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

// TestNativeCAPIOptInteger 验证 luaL_optinteger 的默认值与参数错误边界。
func TestNativeCAPIOptInteger(t *testing.T) {
	// 使用真实 State 和 opaque handle 验证 none/nil 默认值以及非整数错误记录。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 optinteger。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	text := []byte{'x', 0}
	nativeLuaPushNil(luaState)
	nativeLuaPushInteger(luaState, 7)
	nativeLuaPushString(luaState, unsafe.Pointer(&text[0]))

	if integerValue := nativeLuaOptInteger(luaState, 1, 99); integerValue != 99 {
		// nil 参数必须按 lauxlib 可选参数语义返回默认值。
		t.Fatalf("nativeLuaOptInteger nil = %d, want 99", integerValue)
	}
	if integerValue := nativeLuaOptInteger(luaState, 2, 99); integerValue != 7 {
		// 存在的整数参数必须返回实际值。
		t.Fatalf("nativeLuaOptInteger integer = %d, want 7", integerValue)
	}
	if integerValue := nativeLuaOptInteger(luaState, 4, 88); integerValue != 88 {
		// 缺失参数必须按 none 语义返回默认值。
		t.Fatalf("nativeLuaOptInteger missing = %d, want 88", integerValue)
	}
	if integerValue := nativeLuaOptInteger(luaState, 3, 99); integerValue != 0 {
		// 非整数参数错误路径返回 0，真正错误对象通过 pending error 暂存。
		t.Fatalf("nativeLuaOptInteger string = %d, want 0", integerValue)
	}
	errorObject, ok := takeNativeStatePendingError(luaState)
	if !ok || errorObject.Kind != runtime.KindString || errorObject.String != "bad argument #3 (integer expected)" {
		// 非整数参数必须记录 lauxlib 兼容的 integer expected 错误。
		t.Fatalf("nativeLuaOptInteger pending error = %#v ok=%v, want integer expected", errorObject, ok)
	}
}

// TestNativeCAPICheckLString 验证字符串参数检查返回 C 分配 buffer。
func TestNativeCAPICheckLString(t *testing.T) {
	// 使用真实 State 和 opaque handle 验证 luaL_checklstring 的 C buffer 生命周期。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证字符串 shim。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	luaState := handle.pointer()
	token := uintptr(luaState)

	binary := []byte{'a', 0, 'b'}
	nativeLuaPushLString(luaState, unsafe.Pointer(&binary[0]), uintptr(len(binary)))
	nativeLuaPushInteger(luaState, 42)
	nativeLuaPushNil(luaState)

	buffer, length, ok := nativeLuaCheckLString(luaState, 1)
	if !ok || length != uintptr(len(binary)) {
		// 字符串参数必须返回原始字节长度。
		t.Fatalf("nativeLuaCheckLString binary = len=%d ok=%v, want %d true", length, ok, len(binary))
	}
	if got := unsafe.String((*byte)(buffer), int(length)); got != string(binary) {
		// C buffer 必须保留内嵌 NUL 字节。
		t.Fatalf("nativeLuaCheckLString binary = %q, want %q", got, string(binary))
	}

	buffer, length, ok = nativeLuaToLString(luaState, 2)
	if !ok || length != 2 {
		// integer 参数按当前 number-to-string 语义转换为十进制字符串。
		t.Fatalf("nativeLuaToLString integer = len=%d ok=%v, want 2 true", length, ok)
	}
	if got := unsafe.String((*byte)(buffer), int(length)); got != "42" {
		// number-to-string 结果必须来自 runtime 的 Lua 5.3 基础格式。
		t.Fatalf("nativeLuaToLString integer = %q, want 42", got)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "42" {
		// lua_tolstring 对 number 的转换必须把真实栈槽回写为 Lua string。
		t.Fatalf("nativeLuaToLString integer stack slot = %#v, want string 42", value)
	}

	buffer, length, ok = nativeLuaCheckLString(luaState, 3)
	if ok || buffer != nil || length != 0 {
		// luaL_error 尚未实现前，nil 参数检查失败返回 nil/0/false。
		t.Fatalf("nativeLuaCheckLString nil = buffer=%p len=%d ok=%v, want nil 0 false", buffer, length, ok)
	}

	nativeStateHandlesMu.Lock()
	bufferCount := len(nativeStateBuffers[token])
	nativeStateHandlesMu.Unlock()
	if bufferCount < 2 {
		// 成功返回给 C 的字符串 buffer 必须绑定到 handle 生命周期。
		t.Fatalf("nativeStateBuffers count = %d, want at least 2", bufferCount)
	}
	handle.close()
	nativeStateHandlesMu.Lock()
	_, stillTracked := nativeStateBuffers[token]
	nativeStateHandlesMu.Unlock()
	if stillTracked {
		// handle 关闭后必须解除 buffer 追踪并释放 C 内存。
		t.Fatalf("nativeStateBuffers still tracks closed token")
	}
}

// TestNativeLuaToLStringNumberConversionWritesCurrentCFrame 验证 lua_tolstring 数字转换只回写当前 C frame 可见槽。
func TestNativeLuaToLStringNumberConversionWritesCurrentCFrame(t *testing.T) {
	// 构造外层栈槽和 C frame 参数槽，确认正索引映射不会穿透到外层调用者。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C frame 写回。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	outer := []byte{'o', 'u', 't', 'e', 'r', 0}
	nativeLuaPushString(luaState, unsafe.Pointer(&outer[0]))
	nativeLuaPushInteger(luaState, 7)
	if !pushNativeStateCallFrame(luaState, 1, nil) {
		// 无法建立调用帧时，本测试不能继续验证正索引回写边界。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	buffer, length, ok := nativeLuaToLString(luaState, 1)
	if !ok || length != 1 {
		// C frame 正索引 1 应命中 integer 参数并转换成单字节字符串。
		t.Fatalf("nativeLuaToLString frame integer = len=%d ok=%v, want 1 true", length, ok)
	}
	if got := unsafe.String((*byte)(buffer), int(length)); got != "7" {
		// 返回给 C 模块的 buffer 必须包含转换后的数字文本。
		t.Fatalf("nativeLuaToLString frame integer = %q, want 7", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层栈槽不属于当前 C frame，正索引回写不能覆盖它。
		t.Fatalf("outer stack slot = %#v, want outer", value)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "7" {
		// 当前 C frame 第一个可见槽位必须从 integer 回写为 string。
		t.Fatalf("call frame stack slot = %#v, want string 7", value)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 回写不能改变当前 C frame 的可见栈顶。
		t.Fatalf("nativeLuaToLString frame visible top = %d, want 1", got)
	}
}

// TestNativeCAPICheckAny 验证 luaL_checkany 只拒绝缺失参数，允许 nil 参数存在。
func TestNativeCAPICheckAny(t *testing.T) {
	// 使用真实 State 和 opaque handle 验证参数存在性检查。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 checkany。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushNil(luaState)
	if !nativeLuaCheckAny(luaState, 1) {
		// nil 是存在的 Lua 值，luaL_checkany 必须允许。
		t.Fatalf("nativeLuaCheckAny nil = false, want true")
	}
	if nativeLuaCheckAny(luaState, 2) {
		// 第二个参数不存在，应按 none 触发参数错误。
		t.Fatalf("nativeLuaCheckAny missing = true, want false")
	}
	errorObject, ok := takeNativeStatePendingError(luaState)
	if !ok || errorObject.Kind != runtime.KindString || errorObject.String != "bad argument #2 (value expected)" {
		// 缺失参数必须记录 lauxlib 兼容的 value expected 错误。
		t.Fatalf("nativeLuaCheckAny pending error = %#v ok=%v, want value expected", errorObject, ok)
	}
}

// TestNativeCAPICheckType 验证 luaL_checktype 基础类型检查和错误记录。
func TestNativeCAPICheckType(t *testing.T) {
	// 使用真实 State 和 opaque handle 验证类型编号检查。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 checktype。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	text := []byte{'x', 0}
	nativeLuaPushString(luaState, unsafe.Pointer(&text[0]))
	if !nativeLuaCheckType(luaState, 1, nativeLuaTypeString) {
		// 字符串值按 Lua C API 必须报告为 LUA_TSTRING。
		t.Fatalf("nativeLuaCheckType string = false, want true")
	}
	if nativeLuaCheckType(luaState, 1, nativeLuaTypeTable) {
		// 字符串不能通过 table 类型检查。
		t.Fatalf("nativeLuaCheckType string-as-table = true, want false")
	}
	errorObject, ok := takeNativeStatePendingError(luaState)
	if !ok || errorObject.Kind != runtime.KindString || errorObject.String != "bad argument #1 (table expected, got string)" {
		// 类型不匹配必须记录 expected/got 错误。
		t.Fatalf("nativeLuaCheckType pending error = %#v ok=%v, want table expected", errorObject, ok)
	}
}

// TestNativeCAPICheckOption 验证 luaL_checkoption 的默认值、匹配和错误边界。
func TestNativeCAPICheckOption(t *testing.T) {
	// cjson 使用 checkoption 读取 encode/decode 配置项，必须支持 NULL 终止选项数组。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证选项检查。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	first := []byte{'o', 'n', 'e', 0}
	second := []byte{'t', 'w', 'o', 0}
	defaultValue := []byte{'o', 'n', 'e', 0}
	options := []unsafe.Pointer{unsafe.Pointer(&first[0]), unsafe.Pointer(&second[0]), nil}

	nativeLuaPushString(luaState, unsafe.Pointer(&second[0]))
	if got := nativeLuaCheckOption(luaState, 1, nil, unsafe.Pointer(&options[0])); got != 1 {
		// 栈上字符串 two 应匹配第二个选项，返回 0-based 下标 1。
		t.Fatalf("nativeLuaCheckOption explicit = %d, want 1", got)
	}

	nativeLuaSetTop(luaState, 0)
	nativeLuaPushNil(luaState)
	if got := nativeLuaCheckOption(luaState, 1, unsafe.Pointer(&defaultValue[0]), unsafe.Pointer(&options[0])); got != 0 {
		// nil 参数带默认值时应使用默认字符串 one。
		t.Fatalf("nativeLuaCheckOption default = %d, want 0", got)
	}

	nativeLuaSetTop(luaState, 0)
	invalid := []byte{'b', 'a', 'd', 0}
	nativeLuaPushString(luaState, unsafe.Pointer(&invalid[0]))
	if got := nativeLuaCheckOption(luaState, 1, nil, unsafe.Pointer(&options[0])); got != 0 {
		// 错误路径返回 0，真正错误对象通过 pending error 暂存。
		t.Fatalf("nativeLuaCheckOption invalid = %d, want 0", got)
	}
	errorObject, ok := takeNativeStatePendingError(luaState)
	if !ok || errorObject.Kind != runtime.KindString || errorObject.String == "" {
		// 非法选项必须挂起一个错误对象，供 C function 返回边界传播。
		t.Fatalf("nativeLuaCheckOption pending error = %#v ok=%v, want string error", errorObject, ok)
	}
}
