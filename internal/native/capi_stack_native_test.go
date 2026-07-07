//go:build native_modules

package native

import (
	"testing"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeCAPIStackPrimitives 验证 Lua C API 最小栈 shim 可操作 Go State 栈。
func TestNativeCAPIStackPrimitives(t *testing.T) {
	// 测试使用真实 runtime.State 和 opaque handle，确保 C 调用路径不依赖影子栈。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明上一阶段 State 映射不可用，本阶段无法继续验证。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	if got := nativeLuaStackTop(luaState); got != 0 {
		// 新 State 应该从空栈开始。
		t.Fatalf("lua_gettop empty = %d, want 0", got)
	}
	nativeLuaPushNil(luaState)
	nativeLuaPushBoolean(luaState, false)
	nativeLuaPushBoolean(luaState, true)
	nativeLuaPushInteger(luaState, 53)
	nativeLuaPushNumber(luaState, 2.5)
	cString := []byte{'l', 'u', 'a', 0}
	cStringPointer := unsafe.Pointer(&cString[0])
	if returned := nativeLuaPushString(luaState, cStringPointer); returned != cStringPointer {
		// lua_pushstring 至少应返回传入的 C 字符串指针，后续会替换成稳定内部字符串指针策略。
		t.Fatalf("lua_pushstring returned %p, want %p", returned, cStringPointer)
	}
	binary := []byte{'a', 0, 'b'}
	cBinary := unsafe.Pointer(&binary[0])
	if returned := nativeLuaPushLString(luaState, cBinary, uintptr(len(binary))); returned != cBinary {
		// lua_pushlstring 当前返回传入指针，证明 C ABI 返回路径贯通。
		t.Fatalf("lua_pushlstring returned %p, want %p", returned, cBinary)
	}

	if got := nativeLuaStackTop(luaState); got != 7 {
		// 七次压栈后栈顶必须与 Go State 栈深一致。
		t.Fatalf("lua_gettop after pushes = %d, want 7", got)
	}
	if value := state.ValueAt(1); !value.IsNil() {
		// 第一项由 lua_pushnil 压入。
		t.Fatalf("stack[1] = %#v, want nil", value)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindBoolean || value.Bool {
		// 第二项由 lua_pushboolean false 压入。
		t.Fatalf("stack[2] = %#v, want false", value)
	}
	if value := state.ValueAt(3); value.Kind != runtime.KindBoolean || !value.Bool {
		// 第三项由 lua_pushboolean true 压入。
		t.Fatalf("stack[3] = %#v, want true", value)
	}
	if value := state.ValueAt(4); value.Kind != runtime.KindInteger || value.Integer != 53 {
		// 第四项由 lua_pushinteger 压入。
		t.Fatalf("stack[4] = %#v, want integer 53", value)
	}
	if value := state.ValueAt(5); value.Kind != runtime.KindNumber || value.Number != 2.5 {
		// 第五项由 lua_pushnumber 压入。
		t.Fatalf("stack[5] = %#v, want number 2.5", value)
	}
	if value := state.ValueAt(6); value.Kind != runtime.KindString || value.String != "lua" {
		// 第六项由 lua_pushstring 压入。
		t.Fatalf("stack[6] = %#v, want string lua", value)
	}
	if value := state.ValueAt(7); value.Kind != runtime.KindString || value.String != string(binary) {
		// 第七项由 lua_pushlstring 压入，必须保留内嵌 NUL 字节。
		t.Fatalf("stack[7] = %#v, want binary string", value)
	}
	nativeLuaPushValueAt(luaState, 4)
	if got := nativeLuaStackTop(luaState); got != 8 {
		// pushvalue 必须把指定索引的值复制到栈顶。
		t.Fatalf("lua_pushvalue top = %d, want 8", got)
	}
	if value := state.ValueAt(-1); value.Kind != runtime.KindInteger || value.Integer != 53 {
		// 第四项 integer 53 应被复制到新栈顶。
		t.Fatalf("lua_pushvalue copied value = %#v, want integer 53", value)
	}
	nativeLuaPushValueAt(luaState, 99)
	if got := nativeLuaStackTop(luaState); got != 8 {
		// 无效索引复制保持 no-op，避免破坏 C 模块当前栈。
		t.Fatalf("invalid lua_pushvalue top = %d, want 8", got)
	}

	nativeLuaSetTop(luaState, 10)
	if got := nativeLuaStackTop(luaState); got != 10 {
		// 正索引扩栈必须用 nil 补齐到目标栈顶。
		t.Fatalf("lua_settop grow top = %d, want 10", got)
	}
	if value := state.ValueAt(10); !value.IsNil() {
		// 扩栈新增槽位必须是 nil。
		t.Fatalf("stack[10] = %#v, want nil", value)
	}
	nativeLuaSetTop(luaState, -2)
	if got := nativeLuaStackTop(luaState); got != 9 {
		// 负索引 -2 应弹出一个栈顶值。
		t.Fatalf("lua_settop -2 top = %d, want 9", got)
	}
	nativeLuaSetTop(luaState, 0)
	if got := nativeLuaStackTop(luaState); got != 0 {
		// settop(0) 必须清空栈。
		t.Fatalf("lua_settop clear top = %d, want 0", got)
	}
}

// TestNativeLuaPushFormattedString 验证 lua_pushfstring C wrapper 使用的 Go 压栈 helper。
func TestNativeLuaPushFormattedString(t *testing.T) {
	// C wrapper 负责 varargs 格式化；Go helper 负责压栈和返回 C 可见稳定字符串。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	formatted := []byte{'h', 'e', 'l', 'l', 'o', ' ', 'l', 'p', 'e', 'g', 0}

	result := nativeLuaPushFormattedString(luaState, unsafe.Pointer(&formatted[0]))
	if result == nil {
		// 有效 State 和格式化文本必须返回可被 C 模块读取的字符串指针。
		t.Fatalf("nativeLuaPushFormattedString returned nil")
	}
	if got := unsafe.String((*byte)(result), len("hello lpeg")); got != "hello lpeg" {
		// 返回的 C buffer 内容必须等于格式化文本。
		t.Fatalf("nativeLuaPushFormattedString result = %q, want hello lpeg", got)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 格式化字符串必须压入 Lua 栈顶。
		t.Fatalf("top after nativeLuaPushFormattedString = %d, want 1", got)
	}
	value := state.ValueAt(-1)
	if value.Kind != runtime.KindString || value.String != "hello lpeg" {
		// Go VM 栈顶必须保存相同文本值。
		t.Fatalf("stack value = %#v, want formatted string", value)
	}
	if got := nativeLuaPushFormattedString(nil, unsafe.Pointer(&formatted[0])); got != nil {
		// 无效 State 不能返回悬空 C 字符串指针。
		t.Fatalf("nativeLuaPushFormattedString nil state = %p, want nil", got)
	}
}

// TestNativeCAPILightUserdataStableIdentity 验证 lightuserdata 指针 identity 可稳定往返。
func TestNativeCAPILightUserdataStableIdentity(t *testing.T) {
	// cjson 会用 lightuserdata 表达 cjson.null 等哨兵值，同一指针必须 raw equal。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 lightuserdata。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	sentinel := 7
	pointer := unsafe.Pointer(&sentinel)
	nativeLuaPushLightUserdata(luaState, pointer)
	nativeLuaPushLightUserdata(luaState, pointer)
	first := state.ValueAt(1)
	second := state.ValueAt(2)
	if !first.RawEqual(second) {
		// 同一 State 内同一裸指针必须复用同一个 userdata identity。
		t.Fatalf("lightuserdata values are not raw equal: first=%#v second=%#v", first, second)
	}
	if got := nativeLuaType(luaState, 1); got != nativeLuaTypeLightUD {
		// Lua C API 要把 lightuserdata 与 full userdata 区分开。
		t.Fatalf("lightuserdata type = %d, want %d", got, nativeLuaTypeLightUD)
	}
	if got := nativeLuaToUserdata(luaState, 1); got != pointer {
		// lua_touserdata 对 lightuserdata 必须返回原始裸指针。
		t.Fatalf("lightuserdata pointer = %p, want %p", got, pointer)
	}
}

// TestNativeLuaCopyReplacesTargetWithoutChangingTop 验证 lua_copy 原地替换目标槽且不改变栈顶。
func TestNativeLuaCopyReplacesTargetWithoutChangingTop(t *testing.T) {
	// 测试直接使用 Go State 栈，确保 copy 不依赖额外影子存储。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 lua_copy。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'s', 'r', 'c', 0}[0]))
	nativeLuaPushInteger(luaState, 7)
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'d', 's', 't', 0}[0]))
	nativeLuaCopy(luaState, 1, 3)
	if got := nativeLuaStackTop(luaState); got != 3 {
		// lua_copy 只能替换目标槽，不能压栈或弹栈。
		t.Fatalf("lua_copy top = %d, want 3", got)
	}
	if value := state.ValueAt(3); value.Kind != runtime.KindString || value.String != "src" {
		// 第三个槽位应被第一个槽位的字符串替换。
		t.Fatalf("lua_copy target value = %#v, want src", value)
	}
	nativeLuaCopy(luaState, -1, 2)
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "src" {
		// 负索引 source 应从当前栈顶读取。
		t.Fatalf("lua_copy negative source value = %#v, want src", value)
	}
	nativeLuaCopy(luaState, 99, 1)
	nativeLuaCopy(luaState, 1, 99)
	if got := nativeLuaStackTop(luaState); got != 3 {
		// 无效索引按当前最小 shim 策略保持 no-op。
		t.Fatalf("invalid lua_copy top = %d, want 3", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "src" {
		// 无效 copy 不能破坏已有槽位。
		t.Fatalf("invalid lua_copy changed stack[1] = %#v", value)
	}
}

// TestNativeLuaCopyUsesCallFramePositiveIndexes 验证 C function 内正索引相对当前调用帧。
func TestNativeLuaCopyUsesCallFramePositiveIndexes(t *testing.T) {
	// 外层栈保留一个值，调用帧基址之后才是 C function 可见参数。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证调用帧索引。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'o', 'u', 't', 'e', 'r', 0}[0]))
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'f', 'i', 'r', 's', 't', 0}[0]))
	nativeLuaPushString(luaState, unsafe.Pointer(&[]byte{'s', 'e', 'c', 'o', 'n', 'd', 0}[0]))
	if !pushNativeStateCallFrame(luaState, 1, nil) {
		// 无法建立调用帧时，本测试不能继续验证正索引基址。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	nativeLuaCopy(luaState, 2, 1)
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 正确基址下 lua_copy(L, 2, 1) 只能修改调用帧第一个参数，不能覆盖外层槽位。
		t.Fatalf("outer stack slot = %#v, want outer", value)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "second" {
		// 调用帧内第一个可见参数应被第二个可见参数替换。
		t.Fatalf("call frame target = %#v, want second", value)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// C function 内可见栈顶仍应只包含两个参数槽。
		t.Fatalf("call frame lua_copy visible top = %d, want 2", got)
	}
}

// TestNativeLuaReplaceMacroRespectsCurrentCFrame 验证 lua_replace 宏展开不会穿透当前 C 调用帧。
func TestNativeLuaReplaceMacroRespectsCurrentCFrame(t *testing.T) {
	// Lua 5.3 头文件将 lua_replace 展开为 lua_copy(L, -1, idx) 与 lua_pop(L, 1)，LPeg 的 getpatt 路径依赖该组合语义。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 lua_replace 宏语义。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	pushText := func(text string) {
		// 测试字符串只在本次调用内使用；native helper 会立即复制为 Go VM string。
		buffer := append([]byte(text), 0)
		nativeLuaPushString(luaState, unsafe.Pointer(&buffer[0]))
	}

	pushText("outer")
	pushText("first")
	pushText("second")
	if !pushNativeStateCallFrame(luaState, 1, nil) {
		// 无法建立调用帧时，本测试不能继续验证 C frame 内 lua_replace 宏展开。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	pushText("replacement")
	if got := nativeLuaStackTop(luaState); got != 3 {
		// C function 内两个入参加一个临时 replacement，宏展开前可见栈顶应为 3。
		t.Fatalf("before lua_replace macro visible top = %d, want 3", got)
	}
	nativeLuaCopy(luaState, -1, 1)
	nativeLuaSetTop(luaState, -2)

	if got := nativeLuaStackTop(luaState); got != 2 {
		// lua_pop(L, 1) 必须只弹出当前 C frame 内的临时栈顶。
		t.Fatalf("after lua_replace macro visible top = %d, want 2", got)
	}
	if got := state.StackTop(); got != 3 {
		// 全局栈仍保留 outer 加两个 C frame 可见槽，不能留下 replacement 临时值。
		t.Fatalf("after lua_replace macro global top = %d, want 3", got)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 外层调用者栈不属于当前 C frame，lua_replace(L, 1) 不能覆盖它。
		t.Fatalf("outer stack slot = %#v, want outer", value)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "replacement" {
		// 当前 C frame 第一个参数应被原栈顶临时值替换。
		t.Fatalf("call frame target = %#v, want replacement", value)
	}
	if value := state.ValueAt(3); value.Kind != runtime.KindString || value.String != "second" {
		// 当前 C frame 第二个参数不是目标槽，必须保持原值。
		t.Fatalf("call frame untouched slot = %#v, want second", value)
	}
}

// TestNativeCAPIFrameNegativeIndexesDoNotCrossBase 验证 C frame 内过深负索引不能穿透外层栈。
func TestNativeCAPIFrameNegativeIndexesDoNotCrossBase(t *testing.T) {
	// 外层栈槽不属于当前 C function 可见栈，负索引越过 frame base 时必须按 none/no-op 处理。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证调用帧负索引边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	pushText := func(text string) {
		// 测试字符串只在本次调用内使用；native helper 会立即复制为 Go VM string。
		buffer := append([]byte(text), 0)
		nativeLuaPushString(luaState, unsafe.Pointer(&buffer[0]))
	}

	pushText("outer")
	pushText("first")
	pushText("second")
	if !pushNativeStateCallFrame(luaState, 1, nil) {
		// 无法建立调用帧时，本测试不能继续验证负索引基址。
		t.Fatalf("pushNativeStateCallFrame failed")
	}
	defer popNativeStateCallFrame(luaState)

	if got := nativeLuaStackTop(luaState); got != 2 {
		// C function 内只能看到两个参数槽。
		t.Fatalf("call frame visible top = %d, want 2", got)
	}
	if got := nativeLuaType(luaState, -1); got != nativeLuaTypeString {
		// -1 指向当前 C frame 的栈顶 second。
		t.Fatalf("lua_type(-1) = %d, want %d", got, nativeLuaTypeString)
	}
	if got := nativeLuaType(luaState, -2); got != nativeLuaTypeString {
		// -2 指向当前 C frame 的第一个参数 first。
		t.Fatalf("lua_type(-2) = %d, want %d", got, nativeLuaTypeString)
	}
	if got := nativeLuaType(luaState, -3); got != nativeLuaTypeNone {
		// -3 会越过 frame base，不能读到外层 outer。
		t.Fatalf("lua_type(-3) = %d, want %d", got, nativeLuaTypeNone)
	}

	nativeLuaPushValueAt(luaState, -3)
	if got := nativeLuaStackTop(luaState); got != 2 {
		// 无效负索引 pushvalue 必须保持 no-op，避免把外层栈值暴露给 C 模块。
		t.Fatalf("lua_pushvalue(-3) top = %d, want 2", got)
	}
	nativeLuaCopy(luaState, -3, 1)
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "first" {
		// 无效负索引 source 不能覆盖当前 frame 第一个参数。
		t.Fatalf("lua_copy invalid source changed frame[1] = %#v, want first", value)
	}
	nativeLuaCopy(luaState, -1, -3)
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 无效负索引 target 不能覆盖调用帧之前的外层槽位。
		t.Fatalf("lua_copy invalid target changed outer = %#v, want outer", value)
	}
	nativeLuaRotate(luaState, -3, 1)
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// 无效旋转区间不能把外层栈纳入 C frame 操作范围。
		t.Fatalf("lua_rotate invalid index changed outer = %#v, want outer", value)
	}
	if value := state.ValueAt(2); value.Kind != runtime.KindString || value.String != "first" {
		// 当前 frame 参数顺序也必须保持不变。
		t.Fatalf("lua_rotate invalid index changed frame[1] = %#v, want first", value)
	}
	if value := state.ValueAt(3); value.Kind != runtime.KindString || value.String != "second" {
		// 当前 frame 栈顶参数也必须保持不变。
		t.Fatalf("lua_rotate invalid index changed frame[2] = %#v, want second", value)
	}
}

// TestNativeCAPIUpvaluePseudoIndexesUseCurrentCFrame 验证 lua_upvalueindex 只读取当前 C closure 调用帧。
func TestNativeCAPIUpvaluePseudoIndexesUseCurrentCFrame(t *testing.T) {
	// C closure upvalue 不是普通 Lua 栈槽，读取时必须从当前 C frame 的 upvalue 快照解析。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 upvalue pseudo-index。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	upvalueIndex := func(index int) int {
		// Lua 5.3 头文件把 lua_upvalueindex(i) 定义为 LUA_REGISTRYINDEX - i。
		return runtime.RegistryPseudoIndex - index
	}

	if err := state.Push(runtime.StringValue("outer")); err != nil {
		// 外层栈值用于证明 upvalue 读取不会穿透或改写调用者栈。
		t.Fatalf("push outer stack value failed: %v", err)
	}
	outerBaseTop := state.StackTop()
	outerUpvalues := []runtime.Value{runtime.StringValue("outer-up"), runtime.IntegerValue(11)}
	if !pushNativeStateCallFrame(luaState, outerBaseTop, outerUpvalues) {
		// 无法建立 C 调用帧时，本测试不能继续验证 upvalue pseudo-index。
		t.Fatalf("pushNativeStateCallFrame outer failed")
	}
	defer popNativeStateCallFrame(luaState)

	value, ok := nativeLuaValueAt(luaState, upvalueIndex(1))
	if !ok || value.Kind != runtime.KindString || value.String != "outer-up" {
		// lua_upvalueindex(1) 必须读取当前 C closure 的第一个 upvalue。
		t.Fatalf("outer upvalue[1] = %#v ok=%v, want outer-up", value, ok)
	}
	nativeLuaPushValueAt(luaState, upvalueIndex(2))
	if got := nativeLuaStackTop(luaState); got != 1 {
		// pushvalue 读取 upvalue 时只能在当前 C frame 可见栈压入一个副本。
		t.Fatalf("lua_pushvalue(upvalue[2]) visible top = %d, want 1", got)
	}
	if copied := state.ValueAt(-1); copied.Kind != runtime.KindInteger || copied.Integer != 11 {
		// 第二个 upvalue 被复制到全局栈顶，但不改变原 upvalue 快照。
		t.Fatalf("copied upvalue[2] = %#v, want integer 11", copied)
	}
	if missing, ok := nativeLuaValueAt(luaState, upvalueIndex(3)); ok || !missing.IsNil() {
		// 超出当前 closure 捕获数量的 upvalue 必须表现为 none，不能误读普通栈槽。
		t.Fatalf("missing outer upvalue[3] = %#v ok=%v, want none", missing, ok)
	}

	innerBaseTop := state.StackTop()
	innerUpvalues := []runtime.Value{runtime.StringValue("inner-up")}
	if !pushNativeStateCallFrame(luaState, innerBaseTop, innerUpvalues) {
		// 无法建立嵌套 C 调用帧时，本测试不能继续验证 frame 隔离。
		t.Fatalf("pushNativeStateCallFrame inner failed")
	}
	innerValue, ok := nativeLuaValueAt(luaState, upvalueIndex(1))
	if !ok || innerValue.Kind != runtime.KindString || innerValue.String != "inner-up" {
		// 嵌套 C frame 内的 lua_upvalueindex(1) 必须读取内层 closure 的 upvalue。
		t.Fatalf("inner upvalue[1] = %#v ok=%v, want inner-up", innerValue, ok)
	}
	popNativeStateCallFrame(luaState)

	restoredValue, ok := nativeLuaValueAt(luaState, upvalueIndex(1))
	if !ok || restoredValue.Kind != runtime.KindString || restoredValue.String != "outer-up" {
		// 内层 frame 退出后，外层 C closure upvalue 快照必须恢复可见。
		t.Fatalf("restored outer upvalue[1] = %#v ok=%v, want outer-up", restoredValue, ok)
	}
	if value := state.ValueAt(1); value.Kind != runtime.KindString || value.String != "outer" {
		// upvalue pseudo-index 读取和复制不能覆盖当前 C frame 之前的外层栈槽。
		t.Fatalf("outer stack slot = %#v, want outer", value)
	}
}

// TestNativeCAPIStackPrimitivesRejectInvalidState 验证失效 State handle 的最小安全边界。
func TestNativeCAPIStackPrimitivesRejectInvalidState(t *testing.T) {
	// nil lua_State* 没有可映射 State，所有操作必须保持失败安全。
	if got := nativeLuaStackTop(nil); got != 0 {
		// 无效 State 查询栈顶固定为 0。
		t.Fatalf("lua_gettop nil = %d, want 0", got)
	}
	nativeLuaSetTop(nil, 3)
	nativeLuaPushNil(nil)
	nativeLuaPushBoolean(nil, true)
	nativeLuaPushInteger(nil, 1)
	nativeLuaPushNumber(nil, 1)
	nativeLuaPushString(nil, nil)
	nativeLuaPushLString(nil, nil, 0)

	state := runtime.NewState()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明测试无法建立关闭 State 场景。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	luaState := handle.pointer()
	state.Close()
	nativeLuaPushInteger(luaState, 7)
	if got := nativeLuaStackTop(luaState); got != 0 {
		// State 关闭后 lookup 必须拒绝，C API 不能继续写入栈。
		t.Fatalf("lua_gettop closed = %d, want 0", got)
	}
	handle.close()
}
