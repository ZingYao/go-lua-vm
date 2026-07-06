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
