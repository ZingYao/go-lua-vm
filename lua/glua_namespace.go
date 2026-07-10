package lua

import "github.com/ZingYao/go-lua-vm/runtime"

// gluaNamespaceTable 返回可挂载扩展能力的 glua 全局命名空间。
//
// globals 必须是有效全局表；返回已有或新建的 glua table。若宿主已把 glua 定义为非 table，
// 返回 nil 且不覆盖宿主值，调用方应跳过注册以保持兼容性。
func gluaNamespaceTable(globals *runtime.Table) *runtime.Table {
	// 已有 glua table 时保留其字段；缺失时创建新的保留命名空间。
	if globals == nil {
		// nil 全局表无法保存命名空间。
		return nil
	}
	existing := globals.RawGetString("glua")
	if existing.IsNil() {
		// 首次使用时创建全局 glua table。
		created := runtime.NewTable()
		globals.RawSetString("glua", runtime.ReferenceValue(runtime.KindTable, created))
		return created
	}
	if existing.Kind != runtime.KindTable {
		// 非 table 的宿主全局值不能安全扩展。
		return nil
	}
	table, _ := existing.Ref.(*runtime.Table)
	return table
}
