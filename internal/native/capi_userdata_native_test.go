//go:build native_modules

package native

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeLuaNewUserdata 验证 lua_newuserdata 创建 full userdata 并返回稳定 C 指针。
func TestNativeLuaNewUserdata(t *testing.T) {
	// 使用真实 State 与 opaque handle，确保 userdata 进入 Go VM 栈和 State 关闭路径。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用，无法验证 userdata API。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	pointer := nativeLuaNewUserdata(luaState, 8)
	if pointer == nil {
		// 有效 State 和小内存分配必须返回非 nil C 数据区。
		t.Fatalf("lua_newuserdata returned nil")
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// lua_newuserdata 必须把 full userdata 压到栈顶。
		t.Fatalf("lua_newuserdata top = %d, want 1", got)
	}
	value := state.ValueAt(-1)
	if value.Kind != runtime.KindUserdata {
		// 栈顶值必须是 runtime userdata。
		t.Fatalf("lua_newuserdata value = %#v, want userdata", value)
	}
	userdata, ok := value.Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// userdata 引用负载必须是 *runtime.Userdata。
		t.Fatalf("lua_newuserdata ref = %#v, want *runtime.Userdata", value.Ref)
	}
	block, ok := userdata.Data.(*nativeUserdataBlock)
	if !ok || block == nil {
		// native userdata 必须保存 C 内存 block，供 lua_touserdata 返回。
		t.Fatalf("lua_newuserdata data = %#v, want *nativeUserdataBlock", userdata.Data)
	}
	if block.size != 8 {
		// 逻辑长度需要保留，后续 luaL_checkudata 和调试边界会依赖该元信息。
		t.Fatalf("userdata block size = %d, want 8", block.size)
	}
	if got := nativeLuaToUserdata(luaState, -1); got != pointer {
		// touserdata 必须返回创建时同一 C 数据区地址。
		t.Fatalf("lua_touserdata = %p, want %p", got, pointer)
	}

	bytes := unsafe.Slice((*byte)(pointer), 8)
	bytes[0] = 0x53
	bytes[7] = 0x36
	if bytes[0] != 0x53 || bytes[7] != 0x36 {
		// C 可见内存必须可读写，后续 C 模块才能保存原生对象状态。
		t.Fatalf("userdata memory write/read mismatch")
	}

	state.Close()
	if block.data() != nil {
		// State.Close 必须触发 native userdata finalizer，释放 C 内存并清空指针。
		t.Fatalf("userdata block after State.Close = %p, want nil", block.data())
	}
}

// TestNativeLuaNewZeroSizeUserdata 验证 0 字节 full userdata 仍有稳定 identity。
func TestNativeLuaNewZeroSizeUserdata(t *testing.T) {
	// Lua 允许 0 字节 userdata；shim 需要返回可比较的非 nil 指针。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	pointer := nativeLuaNewUserdata(luaState, 0)
	if pointer == nil {
		// 0 字节 userdata 也需要稳定 C identity。
		t.Fatalf("zero-size lua_newuserdata returned nil")
	}
	if got := nativeLuaToUserdata(luaState, -1); got != pointer {
		// 0 字节 userdata 的 touserdata 仍必须返回同一地址。
		t.Fatalf("zero-size lua_touserdata = %p, want %p", got, pointer)
	}
}

// TestNativeLuaGetUserValue 验证 lua_getuservalue 读取 full userdata 关联值。
func TestNativeLuaGetUserValue(t *testing.T) {
	// 使用 native full userdata 覆盖默认 nil user value 和显式 table user value。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	if pointer := nativeLuaNewUserdata(luaState, 4); pointer == nil {
		// 测试需要一个 native full userdata。
		t.Fatalf("lua_newuserdata returned nil")
	}
	value := state.ValueAt(1)
	userdata, ok := value.Ref.(*runtime.Userdata)
	if !ok || userdata == nil {
		// userdata 引用负载必须是 *runtime.Userdata。
		t.Fatalf("userdata ref = %#v, want *runtime.Userdata", value.Ref)
	}

	if gotType := nativeLuaGetUserValue(luaState, 1); gotType != nativeLuaTypeNil {
		// 新创建 userdata 的 user value 零值等价于 Lua nil。
		t.Fatalf("lua_getuservalue default type = %d, want nil", gotType)
	}
	if got := nativeLuaStackTop(luaState); got != 2 {
		// lua_getuservalue 必须把读取到的 user value 压到栈顶。
		t.Fatalf("top after default lua_getuservalue = %d, want 2", got)
	}
	if got := state.ValueAt(-1); got.Kind != runtime.KindNil {
		// 默认 user value 必须作为 nil 栈值可见。
		t.Fatalf("default user value = %#v, want nil", got)
	}

	nativeLuaSetTop(luaState, 1)
	userValueTable := runtime.NewTable()
	userdata.UserValue = runtime.ReferenceValue(runtime.KindTable, userValueTable)
	if gotType := nativeLuaGetUserValue(luaState, 1); gotType != nativeLuaTypeTable {
		// 显式 user value 为 table 时必须返回 table 类型码。
		t.Fatalf("lua_getuservalue table type = %d, want table", gotType)
	}
	gotValue := state.ValueAt(-1)
	if gotValue.Kind != runtime.KindTable || gotValue.Ref != userValueTable {
		// 栈顶必须保留同一个 table identity，供 LPeg 等模块保存 ktable。
		t.Fatalf("table user value = %#v, want %p", gotValue, userValueTable)
	}
}

// TestNativeLuaGetUserValueRejectsNonFullUserdata 验证非 full userdata 路径回退为 nil。
func TestNativeLuaGetUserValueRejectsNonFullUserdata(t *testing.T) {
	// 构造 lightuserdata、普通值和无效 State，覆盖 user value 不存在的边界。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	var marker byte

	nativeLuaPushLightUserdata(luaState, unsafe.Pointer(&marker))
	if gotType := nativeLuaGetUserValue(luaState, 1); gotType != nativeLuaTypeNil {
		// lightuserdata 没有 full userdata 的 user value 槽。
		t.Fatalf("lua_getuservalue(lightuserdata) = %d, want nil", gotType)
	}
	if got := state.ValueAt(-1); got.Kind != runtime.KindNil {
		// lightuserdata 路径仍应压入 nil，避免 C 模块读到旧栈顶。
		t.Fatalf("lightuserdata user value = %#v, want nil", got)
	}

	nativeLuaSetTop(luaState, 0)
	nativeLuaPushInteger(luaState, 7)
	if gotType := nativeLuaGetUserValue(luaState, 1); gotType != nativeLuaTypeNil {
		// 非 userdata 没有关联 user value。
		t.Fatalf("lua_getuservalue(integer) = %d, want nil", gotType)
	}
	if gotType := nativeLuaGetUserValue(nil, 1); gotType != nativeLuaTypeNil {
		// 无效 State 也按 nil 回退，保持 C API shim 失败安全策略。
		t.Fatalf("lua_getuservalue(nil state) = %d, want nil", gotType)
	}
}

// TestNativeLuaToUserdataRejectsNonUserdata 验证非 native userdata 不会被误暴露为 C 指针。
func TestNativeLuaToUserdataRejectsNonUserdata(t *testing.T) {
	// 构造普通栈值和纯 Go userdata，覆盖转换失败边界。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushInteger(luaState, 1)
	if got := nativeLuaToUserdata(luaState, -1); got != nil {
		// integer 不能转换为 userdata。
		t.Fatalf("lua_touserdata(integer) = %p, want nil", got)
	}

	goUserdata := runtime.NewUserdata("go-only")
	if err := state.Push(goUserdata.Value()); err != nil {
		// 测试需要把纯 Go userdata 放入栈顶。
		t.Fatalf("push go userdata failed: %v", err)
	}
	if got := nativeLuaToUserdata(luaState, -1); got != nil {
		// 非 native 创建的 userdata 没有 C full userdata 数据区，必须返回 nil。
		t.Fatalf("lua_touserdata(go userdata) = %p, want nil", got)
	}
	if got := nativeLuaToUserdata(nil, -1); got != nil {
		// nil State 不能读取任何 userdata。
		t.Fatalf("lua_touserdata(nil state) = %p, want nil", got)
	}
}

// TestNativeLuaCheckUDataMatchesNamedMetatable 验证 luaL_checkudata 按 registry 命名元表匹配 userdata。
func TestNativeLuaCheckUDataMatchesNamedMetatable(t *testing.T) {
	// 通过 luaL_newmetatable 建立 registry[typeName]，再绑定到 native userdata。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	typeNameBytes := []byte{'g', 'l', 'u', 'a', '.', 'u', 'd', 0}
	typeNamePointer := unsafe.Pointer(&typeNameBytes[0])

	userdataPointer := nativeLuaNewUserdata(luaState, 16)
	if userdataPointer == nil {
		// 测试需要一个 native full userdata。
		t.Fatalf("lua_newuserdata returned nil")
	}
	if got := nativeLuaLNewMetatable(luaState, typeNamePointer); got != 1 {
		// 首次创建命名元表必须成功。
		t.Fatalf("luaL_newmetatable = %d, want 1", got)
	}
	if got := nativeLuaSetMetatable(luaState, 1); got != 1 {
		// 将命名元表绑定到 userdata。
		t.Fatalf("lua_setmetatable(userdata) = %d, want 1", got)
	}
	if got := nativeLuaCheckUData(luaState, 1, typeNamePointer); got != userdataPointer {
		// 类型名匹配时必须返回同一 C full userdata 数据区指针。
		t.Fatalf("luaL_checkudata = %p, want %p", got, userdataPointer)
	}
	if _, hasError := takeNativeStatePendingError(luaState); hasError {
		// 成功检查不应留下 pending error。
		t.Fatalf("luaL_checkudata success left pending error")
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// luaL_checkudata 不应改变栈。
		t.Fatalf("top after luaL_checkudata = %d, want 1", got)
	}
}

// TestNativeLuaCheckUDataRejectsMismatchedType 验证 luaL_checkudata 失败时记录 pending error。
func TestNativeLuaCheckUDataRejectsMismatchedType(t *testing.T) {
	// 构造一个有元表但 registry 类型名不匹配的 native userdata。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 native State 映射不可用。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()
	typeNameBytes := []byte{'g', 'l', 'u', 'a', '.', 'u', 'd', 0}
	typeNamePointer := unsafe.Pointer(&typeNameBytes[0])
	otherTypeNameBytes := []byte{'o', 't', 'h', 'e', 'r', '.', 'u', 'd', 0}
	otherTypeNamePointer := unsafe.Pointer(&otherTypeNameBytes[0])

	if pointer := nativeLuaNewUserdata(luaState, 8); pointer == nil {
		// 测试需要一个 native full userdata。
		t.Fatalf("lua_newuserdata returned nil")
	}
	if got := nativeLuaLNewMetatable(luaState, typeNamePointer); got != 1 {
		// 创建真实绑定的命名元表。
		t.Fatalf("luaL_newmetatable = %d, want 1", got)
	}
	if got := nativeLuaSetMetatable(luaState, 1); got != 1 {
		// 将真实命名元表绑定到 userdata。
		t.Fatalf("lua_setmetatable(userdata) = %d, want 1", got)
	}
	if got := nativeLuaLNewMetatable(luaState, otherTypeNamePointer); got != 1 {
		// 创建另一个 registry 命名元表用于制造类型不匹配。
		t.Fatalf("luaL_newmetatable other = %d, want 1", got)
	}
	nativeLuaSetTop(luaState, 1)

	if got := nativeLuaCheckUData(luaState, 1, otherTypeNamePointer); got != nil {
		// registry[typeName] 与 userdata raw metatable 不同，必须失败。
		t.Fatalf("luaL_checkudata mismatch = %p, want nil", got)
	}
	errorObject, hasError := takeNativeStatePendingError(luaState)
	if !hasError || errorObject.Kind != runtime.KindString || !strings.Contains(errorObject.String, "other.ud expected") {
		// 失败路径需要留下可由 C function 返回边界传播的错误对象。
		t.Fatalf("pending error = %#v has=%v, want other.ud expected", errorObject, hasError)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 失败检查也不应改栈。
		t.Fatalf("top after failed luaL_checkudata = %d, want 1", got)
	}
}
