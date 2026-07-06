//go:build native_modules

package native

import (
	"errors"
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestNativeLuaCallCFunctionRejectsInvalidHandle 验证失效 State 不会进入本机函数。
func TestNativeLuaCallCFunctionRejectsInvalidHandle(t *testing.T) {
	// 无效 handle 必须直接返回 runtime error，避免 C 函数在已关闭 VM 上执行。
	results, err := nativeLuaCallCFunction(nil, nil)
	if err == nil || !errors.Is(err, runtime.ErrClosedState) {
		// nil handle 没有可映射 State，应按关闭 State 分类。
		t.Fatalf("nativeLuaCallCFunction nil state err = %v, want ErrClosedState", err)
	}
	if results != nil {
		// 失败路径不能返回结果。
		t.Fatalf("nativeLuaCallCFunction nil state results = %#v, want nil", results)
	}
}

// TestNativeLuaPushCClosureRejectsUnsupportedUpvalues 验证当前 nup>0 边界保持 no-op。
func TestNativeLuaPushCClosureRejectsUnsupportedUpvalues(t *testing.T) {
	// 当前阶段还没有 C closure upvalue 模型，nup>0 不能压入半成品 closure。
	state := runtime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C closure 边界。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushInteger(luaState, 1)
	nativeLuaPushCClosure(luaState, nil, 0)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// nil 函数指针必须保持栈不变。
		t.Fatalf("nil C closure top = %d, want 1", got)
	}
	nativeLuaPushCClosure(luaState, luaState, 1)
	if got := nativeLuaStackTop(luaState); got != 1 {
		// nup>0 暂不支持，不能弹 upvalue 或压入不可正确调用的 closure。
		t.Fatalf("upvalue C closure top = %d, want 1", got)
	}
}
