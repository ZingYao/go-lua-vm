package lua

import (
	"errors"
	"testing"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestOpenLibsRejectsNonTableGluaNamespace 验证宿主命名空间冲突不会静默跳过扩展。
func TestOpenLibsRejectsNonTableGluaNamespace(t *testing.T) {
	// 在打开标准库前模拟宿主占用 glua 全局名称。
	state := NewState()
	defer state.Close()
	state.Globals().RawSetString("glua", runtime.StringValue("occupied"))
	err := OpenLibs(state)
	if !errors.Is(err, ErrGluaNamespaceConflict) {
		// 调用方必须能使用 errors.Is 稳定识别命名空间冲突。
		t.Fatalf("OpenLibs error = %v, want ErrGluaNamespaceConflict", err)
	}
}

// TestOpenLibsPreservesTableGluaNamespace 验证宿主预设 table 可与 GLua 扩展共存。
func TestOpenLibsPreservesTableGluaNamespace(t *testing.T) {
	// 宿主预设字段应保留，扩展方法追加到同一个 table。
	state := NewState()
	defer state.Close()
	namespace := runtime.NewTable()
	namespace.RawSetString("host", runtime.StringValue("ready"))
	state.Globals().RawSetString("glua", runtime.ReferenceValue(runtime.KindTable, namespace))
	if err := OpenLibs(state); err != nil {
		// table 命名空间属于合法扩展目标。
		t.Fatalf("OpenLibs failed: %v", err)
	}
	if namespace.RawGetString("host").String != "ready" || namespace.RawGetString("json").Kind != runtime.KindTable {
		// 宿主字段和 GLua 扩展必须同时存在。
		t.Fatalf("glua namespace was not merged: %#v", namespace)
	}
}
