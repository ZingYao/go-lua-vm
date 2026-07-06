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
	results, err := nativeLuaCallCFunction(nil, nil, nil)
	if err == nil || !errors.Is(err, runtime.ErrClosedState) {
		// nil handle 没有可映射 State，应按关闭 State 分类。
		t.Fatalf("nativeLuaCallCFunction nil state err = %v, want ErrClosedState", err)
	}
	if results != nil {
		// 失败路径不能返回结果。
		t.Fatalf("nativeLuaCallCFunction nil state results = %#v, want nil", results)
	}
}

// TestNativeLuaPushCClosureCapturesUpvalues 验证 C closure 能捕获栈顶 upvalue。
func TestNativeLuaPushCClosureCapturesUpvalues(t *testing.T) {
	// cjson 通过 luaL_setfuncs(..., nup=1) 给多个 C 函数共享配置 userdata。
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
		// nup>0 应弹出原始 upvalue 并压入一个 closure。
		t.Fatalf("upvalue C closure top = %d, want 1", got)
	}
	closureValue := state.ValueAt(-1)
	closure, ok := closureValue.Ref.(*runtime.GoClosureWithUpvalues)
	if closureValue.Kind != runtime.KindGoClosure || !ok || closure == nil {
		// 栈顶必须是带 upvalue 元数据的 Go closure。
		t.Fatalf("upvalue C closure value = %#v, want GoClosureWithUpvalues", closureValue)
	}
	if len(closure.Upvalues) != 1 || !closure.Upvalues[0].RawEqual(runtime.IntegerValue(1)) {
		// 第一个 upvalue 必须保留原始栈值。
		t.Fatalf("upvalue C closure upvalues = %#v, want [1]", closure.Upvalues)
	}
}
