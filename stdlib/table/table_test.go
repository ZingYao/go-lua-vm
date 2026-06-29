package table

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/zing/go-lua-vm/runtime"
)

// TestOpenRegistersTableLibrary 验证 Open 会注册 table 库和本阶段支持的函数。
//
// 测试通过全局表读取库对象，确认每个函数都以 Go closure 暴露，错误语义由 runtime
// 后续 CALL/bridge 层负责执行。
func TestOpenRegistersTableLibrary(t *testing.T) {
	// 测试先创建新的 State，避免污染其他标准库注册用例。
	state := runtime.NewState()
	if err := Open(state); err != nil {
		// Open 失败表示 table 标准库无法作为全局库暴露。
		t.Fatalf("Open failed: %v", err)
	}

	libraryValue := state.GetGlobal("table")
	if libraryValue.Kind != runtime.KindTable {
		// table 全局变量必须指向库表。
		t.Fatalf("global table kind mismatch: %v", libraryValue.DebugString())
	}
	library, ok := libraryValue.Ref.(*runtime.Table)
	if !ok || library == nil {
		// KindTable 的引用负载必须是 runtime.Table。
		t.Fatalf("global table payload mismatch: %#v", libraryValue.Ref)
	}

	for _, name := range []string{"concat", "insert", "move", "pack", "remove", "sort", "unpack"} {
		// 每个本阶段函数都应作为 Go closure 注册在库表上。
		functionValue := library.RawGetString(name)
		if functionValue.Kind != runtime.KindGoClosure {
			// 缺失或类型错误都会导致 VM CALL 无法进入标准库函数。
			t.Fatalf("table.%s kind mismatch: %v", name, functionValue.DebugString())
		}
	}
}

// TestOpenRejectsInvalidState 验证 Open 对 nil 和关闭 State 的生命周期错误。
//
// 该测试保证标准库不会向无效 State 写入全局表，错误链仍可用 errors.Is 判断。
func TestOpenRejectsInvalidState(t *testing.T) {
	// nil State 应返回携带 runtime.ErrNilState 的注册错误。
	if err := Open(nil); !errors.Is(err, runtime.ErrNilState) {
		// nil State 错误链必须可被宿主识别。
		t.Fatalf("Open(nil) error mismatch: %v", err)
	}

	state := runtime.NewState()
	state.Close()
	if err := Open(state); !errors.Is(err, runtime.ErrClosedState) {
		// 已关闭 State 错误链必须可被宿主识别。
		t.Fatalf("Open(closed) error mismatch: %v", err)
	}
}

// TestConcatJoinsStringAndNumberRange 验证 table.concat 的区间、分隔符和 number 转换。
//
// 用例覆盖默认序列读取、显式 i/j 边界和 Lua 5.3 integer/float 的基础字符串格式。
func TestConcatJoinsStringAndNumberRange(t *testing.T) {
	// 构造包含 string、integer 和 float number 的连续序列。
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.StringValue("a"))
	list.RawSetInteger(2, runtime.IntegerValue(2))
	list.RawSetInteger(3, runtime.NumberValue(3))
	list.RawSetInteger(4, runtime.StringValue("d"))

	results, err := Concat(runtime.ReferenceValue(runtime.KindTable, list), runtime.StringValue(","), runtime.IntegerValue(2), runtime.IntegerValue(4))
	if err != nil {
		// 合法序列拼接不应返回错误。
		t.Fatalf("Concat failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "2,3.0,d" {
		// 结果必须保持区间顺序，并按 Lua number-to-string 规则转换 number。
		t.Fatalf("Concat result mismatch: %#v", results)
	}
}

// TestConcatHandlesMaxIntegerEnd 验证 table.concat 在 maxinteger 端点不会自增溢出。
//
// Lua 5.3 官方 strings.lua 会用 `[maxinteger-1]` 到 `[maxinteger]` 的闭区间拼接，循环必须在
// 处理 end 后退出，不能让 int64 自增回绕到 mininteger。
func TestConcatHandlesMaxIntegerEnd(t *testing.T) {
	// 构造稀疏表，只在最大整数附近放置两个连续键。
	const maxInteger int64 = 1<<63 - 1
	list := runtime.NewTable()
	list.RawSetInteger(maxInteger-1, runtime.StringValue("y"))
	list.RawSetInteger(maxInteger, runtime.StringValue("alo"))

	results, err := Concat(runtime.ReferenceValue(runtime.KindTable, list), runtime.StringValue("-"), runtime.IntegerValue(maxInteger-1), runtime.IntegerValue(maxInteger))
	if err != nil {
		// maxinteger 闭区间拼接不应访问溢出后的 mininteger。
		t.Fatalf("Concat max integer range failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "y-alo" {
		// 结果必须只包含闭区间内两个元素。
		t.Fatalf("Concat max integer range result mismatch: %#v", results)
	}
}

// TestConcatRejectsInvalidElement 验证 table.concat 遇到 nil 元素时返回 Lua error。
//
// Lua 5.3 要求 concat 区间元素为 string 或 number，nil 洞不能被静默转换。
func TestConcatRejectsInvalidElement(t *testing.T) {
	// 构造第二个元素缺失的序列，强制 concat 读取到 nil。
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.StringValue("a"))
	list.RawSetInteger(3, runtime.StringValue("c"))

	_, err := Concat(runtime.ReferenceValue(runtime.KindTable, list), runtime.StringValue(","), runtime.IntegerValue(1), runtime.IntegerValue(3))
	if err == nil {
		// nil 元素必须触发错误，避免返回不完整字符串。
		t.Fatal("Concat expected error for nil element")
	}
	if !errors.Is(err, runtime.ErrLuaError) {
		// 错误应以 Lua error 形式传播，便于 pcall 捕获。
		t.Fatalf("Concat error chain mismatch: %v", err)
	}
}

// TestInsertAppendAndPosition 验证 table.insert 的追加和指定位置插入。
//
// 用例覆盖 append 到 #list+1，以及指定位置插入时对后续元素的 raw 右移。
func TestInsertAppendAndPosition(t *testing.T) {
	// 构造初始连续序列。
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.StringValue("a"))
	list.RawSetInteger(2, runtime.StringValue("c"))

	if _, err := Insert(runtime.ReferenceValue(runtime.KindTable, list), runtime.StringValue("d")); err != nil {
		// 两参数形态应追加到当前末尾。
		t.Fatalf("Insert append failed: %v", err)
	}
	if _, err := Insert(runtime.ReferenceValue(runtime.KindTable, list), runtime.IntegerValue(2), runtime.StringValue("b")); err != nil {
		// 三参数形态应在指定位置插入并右移。
		t.Fatalf("Insert position failed: %v", err)
	}

	expectStringSlot(t, list, 1, "a")
	expectStringSlot(t, list, 2, "b")
	expectStringSlot(t, list, 3, "c")
	expectStringSlot(t, list, 4, "d")
}

// TestRemoveReturnsAndShifts 验证 table.remove 返回删除值并左移后续元素。
//
// 用例覆盖显式位置删除与旧末尾清空，确保不会残留重复元素。
func TestRemoveReturnsAndShifts(t *testing.T) {
	// 构造三元素连续序列。
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.StringValue("a"))
	list.RawSetInteger(2, runtime.StringValue("b"))
	list.RawSetInteger(3, runtime.StringValue("c"))

	results, err := Remove(runtime.ReferenceValue(runtime.KindTable, list), runtime.IntegerValue(2))
	if err != nil {
		// 合法删除不应返回错误。
		t.Fatalf("Remove failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "b" {
		// remove 必须返回被删除元素。
		t.Fatalf("Remove result mismatch: %#v", results)
	}

	expectStringSlot(t, list, 1, "a")
	expectStringSlot(t, list, 2, "c")
	if value := list.RawGetInteger(3); !value.IsNil() {
		// 旧末尾必须清空，避免序列中留下重复值。
		t.Fatalf("slot 3 should be nil: %v", value.DebugString())
	}
}

// TestRemoveDefaultPositionZero 验证 table.remove 在空序列上的默认索引 0 特例。
//
// Lua 5.3 官方 nextvar.lua 要求 `#t == 0` 且存在 key 0 时，省略 pos 的 remove 删除 key 0。
func TestRemoveDefaultPositionZero(t *testing.T) {
	// 构造长度为 0 但 key 0 有值的 table。
	list := runtime.NewTable()
	list.RawSetInteger(0, runtime.StringValue("ban"))

	results, err := Remove(runtime.ReferenceValue(runtime.KindTable, list))
	if err != nil {
		// 默认删除 key 0 不应报越界。
		t.Fatalf("Remove default zero failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindString || results[0].String != "ban" {
		// 返回值必须是原 key 0 上的值。
		t.Fatalf("Remove default zero result mismatch: %#v", results)
	}
	if value := list.RawGetInteger(0); !value.IsNil() {
		// 删除后 key 0 必须被清空。
		t.Fatalf("slot 0 should be nil: %v", value.DebugString())
	}

	explicitOneResults, err := Remove(runtime.ReferenceValue(runtime.KindTable, list), runtime.IntegerValue(1))
	if err != nil {
		// 空序列显式 pos=1 在 Lua 5.3 中允许并返回 nil。
		t.Fatalf("Remove explicit one on empty failed: %v", err)
	}
	if len(explicitOneResults) != 1 || !explicitOneResults[0].IsNil() {
		// 空序列 pos=1 没有元素可删，返回 nil。
		t.Fatalf("Remove explicit one result mismatch: %#v", explicitOneResults)
	}

	explicitZeroResults, err := Remove(runtime.ReferenceValue(runtime.KindTable, list), runtime.IntegerValue(0))
	if err != nil {
		// 空序列显式 pos=0 也属于 Lua 5.3 允许边界。
		t.Fatalf("Remove explicit zero on empty failed: %v", err)
	}
	if len(explicitZeroResults) != 1 || !explicitZeroResults[0].IsNil() {
		// key 0 已删除后再次删除返回 nil。
		t.Fatalf("Remove explicit zero result mismatch: %#v", explicitZeroResults)
	}
}

// TestRemoveEndPlusOneReturnsNil 验证 table.remove 允许删除末尾后一位。
//
// Lua 5.3 官方 nextvar.lua 要求 pos=#t+1 返回 nil 且不修改原序列。
func TestRemoveEndPlusOneReturnsNil(t *testing.T) {
	// 构造两元素序列，显式删除第三个位置。
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.StringValue("a"))
	list.RawSetInteger(2, runtime.StringValue("b"))

	results, err := Remove(runtime.ReferenceValue(runtime.KindTable, list), runtime.IntegerValue(3))
	if err != nil {
		// 末尾后一位是 Lua 5.3 允许边界。
		t.Fatalf("Remove end plus one failed: %v", err)
	}
	if len(results) != 1 || !results[0].IsNil() {
		// 删除不存在的末尾后一位应返回 nil。
		t.Fatalf("Remove end plus one result mismatch: %#v", results)
	}
	expectStringSlot(t, list, 1, "a")
	expectStringSlot(t, list, 2, "b")
}

// TestMoveSupportsOverlapAndDestination 验证 table.move 的重叠区间和外部目标表。
//
// 用例先在同一 table 内重叠移动，再把区间移动到另一个 table，并检查返回目标 table。
func TestMoveSupportsOverlapAndDestination(t *testing.T) {
	// 构造源序列，用于验证先缓存再写入的重叠安全性。
	source := runtime.NewTable()
	source.RawSetInteger(1, runtime.StringValue("a"))
	source.RawSetInteger(2, runtime.StringValue("b"))
	source.RawSetInteger(3, runtime.StringValue("c"))

	sourceValue := runtime.ReferenceValue(runtime.KindTable, source)
	results, err := Move(sourceValue, runtime.IntegerValue(1), runtime.IntegerValue(2), runtime.IntegerValue(2))
	if err != nil {
		// 同表重叠移动不应破坏源读取。
		t.Fatalf("Move overlap failed: %v", err)
	}
	if len(results) != 1 || !results[0].RawEqual(sourceValue) {
		// 未传目标表时必须返回源表。
		t.Fatalf("Move overlap return mismatch: %#v", results)
	}
	expectStringSlot(t, source, 2, "a")
	expectStringSlot(t, source, 3, "b")

	destination := runtime.NewTable()
	destinationValue := runtime.ReferenceValue(runtime.KindTable, destination)
	results, err = Move(sourceValue, runtime.IntegerValue(2), runtime.IntegerValue(3), runtime.IntegerValue(1), destinationValue)
	if err != nil {
		// 外部目标表移动不应返回错误。
		t.Fatalf("Move destination failed: %v", err)
	}
	if len(results) != 1 || !results[0].RawEqual(destinationValue) {
		// 传入目标表时必须返回该目标表。
		t.Fatalf("Move destination return mismatch: %#v", results)
	}
	expectStringSlot(t, destination, 1, "a")
	expectStringSlot(t, destination, 2, "b")
}

// TestMoveRejectsHugeRangesAndWrapAround 验证 table.move 的极端区间错误。
//
// 官方 sort.lua 会用 math.mininteger/math.maxinteger 组合检查 too many elements 和
// destination wrap around；这些错误必须在进入移动循环前返回。
func TestMoveRejectsHugeRangesAndWrapAround(t *testing.T) {
	sourceValue := runtime.ReferenceValue(runtime.KindTable, runtime.NewTable())
	tests := []struct {
		name string
		args []runtime.Value
		want string
	}{
		{
			name: "too many from zero to max",
			args: []runtime.Value{sourceValue, runtime.IntegerValue(0), runtime.IntegerValue(math.MaxInt64), runtime.IntegerValue(1)},
			want: "too many",
		},
		{
			name: "too many full signed range",
			args: []runtime.Value{sourceValue, runtime.IntegerValue(math.MinInt64), runtime.IntegerValue(math.MaxInt64), runtime.IntegerValue(1)},
			want: "too many",
		},
		{
			name: "destination wrap positive",
			args: []runtime.Value{sourceValue, runtime.IntegerValue(1), runtime.IntegerValue(math.MaxInt64), runtime.IntegerValue(2)},
			want: "wrap around",
		},
		{
			name: "destination wrap target",
			args: []runtime.Value{sourceValue, runtime.IntegerValue(1), runtime.IntegerValue(2), runtime.IntegerValue(math.MaxInt64)},
			want: "wrap around",
		},
	}

	for _, testCase := range tests {
		_, err := Move(testCase.args...)
		if !errors.Is(err, runtime.ErrLuaError) {
			// 极端边界应转换成 Lua error，而不是进入长循环或返回普通 Go 错误。
			t.Fatalf("%s error mismatch: %v", testCase.name, err)
		}
		if message := runtime.ErrorObject(err).String; !strings.Contains(message, testCase.want) {
			// 错误文本必须包含官方 sort.lua 匹配片段。
			t.Fatalf("%s message = %q, want %q", testCase.name, message, testCase.want)
		}
	}
}

// TestMoveIntegerFringesAndMetamethodAbort 验证 table.move 的边界索引和元方法中断位置。
//
// 官方 sort.lua 会移动 maxinteger/mininteger 边缘槽位，并用会抛错的 __newindex 中断超长移动；
// 实现必须只访问首个或最后一个必要槽位，不能为了重叠安全整段缓存。
func TestMoveIntegerFringesAndMetamethodAbort(t *testing.T) {
	source := runtime.NewTable()
	source.RawSetInteger(math.MaxInt64, runtime.IntegerValue(100))
	sourceValue := runtime.ReferenceValue(runtime.KindTable, source)

	results, err := Move(sourceValue, runtime.IntegerValue(math.MaxInt64), runtime.IntegerValue(math.MaxInt64), runtime.IntegerValue(math.MinInt64))
	if err != nil {
		// 单元素从 maxinteger 移到 mininteger 不应触发 wrap around。
		t.Fatalf("Move max to min failed: %v", err)
	}
	if len(results) != 1 || !results[0].RawEqual(sourceValue) || !source.RawGetInteger(math.MinInt64).RawEqual(runtime.IntegerValue(100)) {
		// 返回值和目标槽位必须符合官方边界移动语义。
		t.Fatalf("Move max to min result mismatch: results=%#v min=%v", results, source.RawGetInteger(math.MinInt64).DebugString())
	}

	var readIndex int64
	var writeIndex int64
	fail := runtime.RaiseError(runtime.NilValue())
	metatable := runtime.NewTable()
	metatable.RawSetString("__index", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 记录 table.move 首次读取的源索引。
		readIndex, _ = args[1].ToInteger()
		return []runtime.Value{runtime.IntegerValue(readIndex)}, nil
	})))
	metatable.RawSetString("__newindex", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 记录 table.move 首次写入的目标索引并中断，模拟官方 sort.lua 的 checkmove。
		writeIndex, _ = args[1].ToInteger()
		return nil, fail
	})))
	metaSource := runtime.NewTable()
	metaSource.SetMetatable(metatable)

	_, err = Move(runtime.ReferenceValue(runtime.KindTable, metaSource), runtime.IntegerValue(1), runtime.IntegerValue(math.MaxInt64), runtime.IntegerValue(0))
	if !errors.Is(err, runtime.ErrLuaError) || readIndex != 1 || writeIndex != 0 {
		// 超长非重叠移动应从首个源索引开始，并在首个目标写入错误后停止。
		t.Fatalf("Move metamethod abort mismatch: err=%v read=%d write=%d", err, readIndex, writeIndex)
	}
}

// TestPackStoresArgumentsAndCount 验证 table.pack 保存入参和 n 字段。
//
// 用例覆盖普通值、nil 值和尾部数量记录，确保 n 不依赖 table.Len。
func TestPackStoresArgumentsAndCount(t *testing.T) {
	// pack 的第三个参数为 nil，用于验证 n 仍记录完整入参数量。
	results, err := Pack(runtime.StringValue("a"), runtime.IntegerValue(2), runtime.NilValue())
	if err != nil {
		// pack 不依赖外部状态，合法调用不应失败。
		t.Fatalf("Pack failed: %v", err)
	}
	if len(results) != 1 || results[0].Kind != runtime.KindTable {
		// pack 必须返回一个新 table。
		t.Fatalf("Pack result mismatch: %#v", results)
	}

	packed, ok := results[0].Ref.(*runtime.Table)
	if !ok || packed == nil {
		// KindTable 的引用负载必须是 runtime.Table。
		t.Fatalf("Pack payload mismatch: %#v", results[0].Ref)
	}
	expectStringSlot(t, packed, 1, "a")
	if value := packed.RawGetInteger(2); value.Kind != runtime.KindInteger || value.Integer != 2 {
		// 第二个槽位必须保存原始 integer 参数。
		t.Fatalf("slot 2 mismatch: %v", value.DebugString())
	}
	if value := packed.RawGetInteger(3); !value.IsNil() {
		// nil 参数按 table raw 存储语义不会保留槽位。
		t.Fatalf("slot 3 should be nil: %v", value.DebugString())
	}
	if value := packed.RawGetString("n"); value.Kind != runtime.KindInteger || value.Integer != 3 {
		// n 字段必须记录原始参数数量，而不是 table 长度。
		t.Fatalf("n field mismatch: %v", value.DebugString())
	}
}

// TestSortOrdersNumbersAndStrings 验证 table.sort 的默认 number/string 基础排序。
//
// 用例分别覆盖 number 混合 integer/float 的数值比较，以及 string 的字节序比较。
func TestSortOrdersNumbersAndStrings(t *testing.T) {
	// 构造 number 序列，覆盖 integer 与 float number 混排。
	numbers := runtime.NewTable()
	numbers.RawSetInteger(1, runtime.IntegerValue(3))
	numbers.RawSetInteger(2, runtime.NumberValue(1.5))
	numbers.RawSetInteger(3, runtime.IntegerValue(2))
	if _, err := Sort(runtime.ReferenceValue(runtime.KindTable, numbers)); err != nil {
		// number 序列默认排序不应失败。
		t.Fatalf("Sort numbers failed: %v", err)
	}
	if value := numbers.RawGetInteger(1); value.Kind != runtime.KindNumber || value.Number != 1.5 {
		// 第一项应为最小浮点数。
		t.Fatalf("number slot 1 mismatch: %v", value.DebugString())
	}
	if value := numbers.RawGetInteger(2); value.Kind != runtime.KindInteger || value.Integer != 2 {
		// 第二项应为 integer 2。
		t.Fatalf("number slot 2 mismatch: %v", value.DebugString())
	}
	if value := numbers.RawGetInteger(3); value.Kind != runtime.KindInteger || value.Integer != 3 {
		// 第三项应为 integer 3。
		t.Fatalf("number slot 3 mismatch: %v", value.DebugString())
	}

	// 构造 string 序列，覆盖字节序排序。
	stringsTable := runtime.NewTable()
	stringsTable.RawSetInteger(1, runtime.StringValue("b"))
	stringsTable.RawSetInteger(2, runtime.StringValue("a"))
	if _, err := Sort(runtime.ReferenceValue(runtime.KindTable, stringsTable)); err != nil {
		// string 序列默认排序不应失败。
		t.Fatalf("Sort strings failed: %v", err)
	}
	expectStringSlot(t, stringsTable, 1, "a")
	expectStringSlot(t, stringsTable, 2, "b")
}

// TestSortUsesComparator 验证 table.sort 会调用 Go comparator 并按 truthiness 排序。
//
// 当前阶段 comparator 先支持 Go closure，Lua closure comparator 会在完整调用栈接入后补齐。
func TestSortUsesComparator(t *testing.T) {
	// 构造升序输入，用自定义 comparator 排成降序。
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.IntegerValue(1))
	list.RawSetInteger(2, runtime.IntegerValue(2))
	list.RawSetInteger(3, runtime.IntegerValue(3))

	comparator := runtime.GoResultsFunction(func(values ...runtime.Value) ([]runtime.Value, error) {
		// comparator 入参必须是待比较的两个元素。
		if len(values) != 2 {
			// 参数数量错误表示 sort 调用协议损坏。
			t.Fatalf("comparator arg count mismatch: %d", len(values))
		}
		left, _ := values[0].ToNumber()
		right, _ := values[1].ToNumber()
		return []runtime.Value{runtime.BooleanValue(left > right)}, nil
	})

	if _, err := Sort(runtime.ReferenceValue(runtime.KindTable, list), runtime.ReferenceValue(runtime.KindGoClosure, comparator)); err != nil {
		// 合法 comparator 不应返回错误。
		t.Fatalf("Sort comparator failed: %v", err)
	}
	if value := list.RawGetInteger(1); value.Kind != runtime.KindInteger || value.Integer != 3 {
		// 降序排序后第一项应为最大值。
		t.Fatalf("slot 1 mismatch: %v", value.DebugString())
	}
	if value := list.RawGetInteger(2); value.Kind != runtime.KindInteger || value.Integer != 2 {
		// 降序排序后第二项应为中间值。
		t.Fatalf("slot 2 mismatch: %v", value.DebugString())
	}
	if value := list.RawGetInteger(3); value.Kind != runtime.KindInteger || value.Integer != 1 {
		// 降序排序后第三项应为最小值。
		t.Fatalf("slot 3 mismatch: %v", value.DebugString())
	}
}

// TestSortNilComparatorUsesDefaultOrder 验证显式 nil comparator 等价于省略。
//
// 官方 sort.lua 的 timesort 会调用 `table.sort(a, nil)`；第二参数为 nil 时必须使用默认 `<`
// 排序，而不是返回 function expected。
func TestSortNilComparatorUsesDefaultOrder(t *testing.T) {
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.IntegerValue(3))
	list.RawSetInteger(2, runtime.IntegerValue(1))
	list.RawSetInteger(3, runtime.IntegerValue(2))

	if _, err := Sort(runtime.ReferenceValue(runtime.KindTable, list), runtime.NilValue()); err != nil {
		// nil comparator 必须等价于未传 comparator。
		t.Fatalf("Sort nil comparator failed: %v", err)
	}
	if value := list.RawGetInteger(1); value.Kind != runtime.KindInteger || value.Integer != 1 {
		// 默认排序后第一项应为最小值。
		t.Fatalf("slot 1 mismatch: %v", value.DebugString())
	}
}

// TestSortDefaultOrderUsesTableLtMetamethod 验证默认 table.sort 会调用元素 `__lt` 元方法。
//
// 官方 sort.lua 会给每个元素 table 设置同一个 `__lt`，然后直接 `table.sort(a)`；默认比较
// 必须通过元方法读取对象字段，而不是把 table 值判为不可比较。
func TestSortDefaultOrderUsesTableLtMetamethod(t *testing.T) {
	state := runtime.NewState()
	metatable := runtime.NewTable()
	metatable.RawSetString("__lt", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 比较两个对象的 val 字段。
		leftTable := args[0].Ref.(*runtime.Table)
		rightTable := args[1].Ref.(*runtime.Table)
		leftValue, _ := leftTable.RawGetString("val").ToInteger()
		rightValue, _ := rightTable.RawGetString("val").ToInteger()
		return []runtime.Value{runtime.BooleanValue(leftValue < rightValue)}, nil
	})))

	list := runtime.NewTable()
	for index, value := range []int64{3, 1, 2} {
		// 每个元素都是带 __lt 元表的 table。
		item := runtime.NewTable()
		item.RawSetString("val", runtime.IntegerValue(value))
		item.SetMetatable(metatable)
		list.RawSetInteger(int64(index+1), runtime.ReferenceValue(runtime.KindTable, item))
	}

	if _, err := sortWithState(state, runtime.ReferenceValue(runtime.KindTable, list)); err != nil {
		// 默认比较必须能调用 __lt 元方法。
		t.Fatalf("Sort table __lt failed: %v", err)
	}
	first := list.RawGetInteger(1).Ref.(*runtime.Table).RawGetString("val")
	if first.Kind != runtime.KindInteger || first.Integer != 1 {
		// 排序后第一个对象应持有最小 val。
		t.Fatalf("Sort table __lt first value mismatch: %v", first.DebugString())
	}
}

// TestSortPropagatesComparatorError 验证 table.sort 的 comparator error 边界。
//
// comparator 返回 Lua error 时，sort 必须立即停止并把错误原样传播给调用方。
func TestSortPropagatesComparatorError(t *testing.T) {
	// 构造至少两个元素，确保排序过程中会调用 comparator。
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.IntegerValue(2))
	list.RawSetInteger(2, runtime.IntegerValue(1))
	expectedErr := runtime.RaiseError(runtime.StringValue("comparator failed"))
	comparator := runtime.GoResultsFunction(func(values ...runtime.Value) ([]runtime.Value, error) {
		// comparator 直接返回预期错误，验证 sort 不吞错。
		return nil, expectedErr
	})

	_, err := Sort(runtime.ReferenceValue(runtime.KindTable, list), runtime.ReferenceValue(runtime.KindGoClosure, comparator))
	if !errors.Is(err, runtime.ErrLuaError) {
		// comparator 的 Lua error 必须保持在错误链中。
		t.Fatalf("Sort comparator error mismatch: %v", err)
	}
}

// TestSortRejectsOfficialTooBigLength 验证 table.sort 在 INT_MAX 级长度前提前失败。
//
// 官方 sort.lua 会用 `__len = math.maxinteger` 的虚拟 table 检查 `array too big`；实现必须在
// 创建待排序切片前拒绝，避免 Go 运行时 panic。
func TestSortRejectsOfficialTooBigLength(t *testing.T) {
	state := runtime.NewState()
	list := runtime.NewTable()
	metatable := runtime.NewTable()
	metatable.RawSetString("__len", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 返回远超 Lua 5.3 sort C int 上限的虚拟长度。
		return []runtime.Value{runtime.IntegerValue(math.MaxInt64)}, nil
	})))
	list.SetMetatable(metatable)

	_, err := sortWithState(state, runtime.ReferenceValue(runtime.KindTable, list))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 过大长度必须转换成 Lua error object。
		t.Fatalf("Sort too-big length error mismatch: %v", err)
	}
	if message := runtime.ErrorObject(err).String; !strings.Contains(message, "too big") {
		// 错误文本需要匹配官方 sort.lua 的 checkerror("too big", ...)。
		t.Fatalf("Sort too-big length message = %q", message)
	}
}

// TestSortAcceptsNegativeVirtualLength 验证 table.sort 对负虚拟长度不比较也不分配。
//
// 官方 sort.lua 使用 `__len = -1` 的 table 调用 `table.sort(a, error)`；此时没有非平凡区间，
// comparator 不应被调用，函数应直接成功。
func TestSortAcceptsNegativeVirtualLength(t *testing.T) {
	state := runtime.NewState()
	list := runtime.NewTable()
	metatable := runtime.NewTable()
	metatable.RawSetString("__len", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// 返回负长度，模拟官方 strange lengths 小节。
		return []runtime.Value{runtime.IntegerValue(-1)}, nil
	})))
	list.SetMetatable(metatable)
	comparator := runtime.GoResultsFunction(func(values ...runtime.Value) ([]runtime.Value, error) {
		// 如果 comparator 被调用，说明 sort 没有正确识别平凡区间。
		return nil, runtime.RaiseError(runtime.StringValue("unexpected comparator call"))
	})

	if _, err := sortWithState(state, runtime.ReferenceValue(runtime.KindTable, list), runtime.ReferenceValue(runtime.KindGoClosure, comparator)); err != nil {
		// 负长度虚拟序列必须直接成功。
		t.Fatalf("Sort negative virtual length failed: %v", err)
	}
}

// TestSortRejectsAlwaysTrueComparator 验证 table.sort 拒绝明显非法比较函数。
//
// 官方 sort.lua 用恒 true comparator 检查 `invalid order function`；实现需在相邻元素比较中
// 发现 a<b 与 b<a 同时为真并返回 Lua error。
func TestSortRejectsAlwaysTrueComparator(t *testing.T) {
	list := runtime.NewTable()
	for index := int64(1); index <= 4; index++ {
		// 构造四个元素，确保排序过程中触发相邻比较。
		list.RawSetInteger(index, runtime.IntegerValue(index))
	}
	comparator := runtime.GoResultsFunction(func(values ...runtime.Value) ([]runtime.Value, error) {
		// 恒 true comparator 违反严格弱序。
		return []runtime.Value{runtime.BooleanValue(true)}, nil
	})

	_, err := Sort(runtime.ReferenceValue(runtime.KindTable, list), runtime.ReferenceValue(runtime.KindGoClosure, comparator))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 非法 comparator 应返回 Lua error object。
		t.Fatalf("Sort invalid comparator error mismatch: %v", err)
	}
	if message := runtime.ErrorObject(err).String; !strings.Contains(message, "invalid order function") {
		// 错误文本必须匹配官方 sort.lua 的 checkerror。
		t.Fatalf("Sort invalid comparator message = %q", message)
	}
}

// TestUnpackReturnsRangeAndSparseNil 验证 table.unpack 的区间和 sparse 洞语义。
//
// 用例显式传入 i/j，确保中间缺失槽位作为 nil 返回值保留，而不是被跳过。
func TestUnpackReturnsRangeAndSparseNil(t *testing.T) {
	// 构造 sparse 序列：第二个槽位缺失，但 j 显式指定为 3。
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.StringValue("a"))
	list.RawSetInteger(3, runtime.StringValue("c"))

	results, err := Unpack(runtime.ReferenceValue(runtime.KindTable, list), runtime.IntegerValue(1), runtime.IntegerValue(3))
	if err != nil {
		// 显式范围 unpack 不应因 sparse 洞失败。
		t.Fatalf("Unpack failed: %v", err)
	}
	if len(results) != 3 {
		// 返回值数量必须等于显式区间长度。
		t.Fatalf("Unpack result length mismatch: %d", len(results))
	}
	if results[0].Kind != runtime.KindString || results[0].String != "a" {
		// 第一项应保留原始字符串。
		t.Fatalf("Unpack first result mismatch: %v", results[0].DebugString())
	}
	if !results[1].IsNil() {
		// sparse 洞必须作为 nil 返回值保留。
		t.Fatalf("Unpack sparse result should be nil: %v", results[1].DebugString())
	}
	if results[2].Kind != runtime.KindString || results[2].String != "c" {
		// 第三项应保留原始字符串。
		t.Fatalf("Unpack third result mismatch: %v", results[2].DebugString())
	}
}

// TestUnpackNilBoundsUseDefaults 验证 table.unpack 的 nil 边界参数沿用默认值。
//
// Lua 5.3 中显式传入 nil 的 i/j 等价于省略参数；官方 vararg.lua 使用 args.n=nil 作为
// 第三个参数，必须按表长度计算结束位置。
func TestUnpackNilBoundsUseDefaults(t *testing.T) {
	list := runtime.NewTable()
	list.RawSetInteger(1, runtime.StringValue("a"))
	list.RawSetInteger(2, runtime.StringValue("b"))

	results, err := Unpack(runtime.ReferenceValue(runtime.KindTable, list), runtime.NilValue(), runtime.NilValue())
	if err != nil {
		// nil i/j 应走默认范围，不应触发 integer expected。
		t.Fatalf("Unpack nil bounds failed: %v", err)
	}
	if len(results) != 2 || !results[0].RawEqual(runtime.StringValue("a")) || !results[1].RawEqual(runtime.StringValue("b")) {
		// 默认范围必须等价于 unpack(list, 1, #list)。
		t.Fatalf("Unpack nil bounds result mismatch: %#v", results)
	}
}

// TestUnpackHugeRangeFailsBeforeAllocation 验证极端整数区间在分配结果切片前失败。
//
// 官方 sort.lua 会调用 `table.unpack({}, math.mininteger, math.maxinteger)` 检查 `too many
// results`；该区间长度超过 int64 可表达范围，必须先返回 Lua error，不能发生 Go panic。
func TestUnpackHugeRangeFailsBeforeAllocation(t *testing.T) {
	_, err := Unpack(runtime.ReferenceValue(runtime.KindTable, runtime.NewTable()), runtime.IntegerValue(math.MinInt64), runtime.IntegerValue(math.MaxInt64))
	if !errors.Is(err, runtime.ErrLuaError) {
		// 超大返回范围必须转换成 Lua error object。
		t.Fatalf("Unpack huge range error mismatch: %v", err)
	}
	if message := runtime.ErrorObject(err).String; !strings.Contains(message, "too many results") {
		// 错误文本必须包含官方 sort.lua 匹配的关键片段。
		t.Fatalf("Unpack huge range message = %q", message)
	}
}

// TestUnpackMaxIntegerBoundaryDoesNotWrap 验证 unpack 处理 maxinteger 作为闭区间终点时不回绕。
//
// 官方 sort.lua 会读取 `{[maxinteger - 1] = 12, [maxinteger] = 23}` 的最后两个槽位；
// 循环必须在处理 maxinteger 后退出，不能让索引自增溢出为 mininteger。
func TestUnpackMaxIntegerBoundaryDoesNotWrap(t *testing.T) {
	list := runtime.NewTable()
	list.RawSetInteger(math.MaxInt64-1, runtime.IntegerValue(12))
	list.RawSetInteger(math.MaxInt64, runtime.IntegerValue(23))

	results, err := Unpack(runtime.ReferenceValue(runtime.KindTable, list), runtime.IntegerValue(math.MaxInt64-1), runtime.IntegerValue(math.MaxInt64))
	if err != nil {
		// 合法的两元素边界区间应正常返回。
		t.Fatalf("Unpack max boundary failed: %v", err)
	}
	if len(results) != 2 || !results[0].RawEqual(runtime.IntegerValue(12)) || !results[1].RawEqual(runtime.IntegerValue(23)) {
		// 返回值顺序必须与索引升序一致。
		t.Fatalf("Unpack max boundary results mismatch: %#v", results)
	}
}

// expectStringSlot 校验 table 指定整数槽位上的字符串值。
//
// target 必须非 nil；index 是 Lua 1-based integer key；expected 是期望字符串内容。失败时
// 直接终止当前测试，减少重复断言代码。
func expectStringSlot(t *testing.T, target *runtime.Table, index int64, expected string) {
	// helper 必须标记为测试辅助函数，失败行号指向调用处。
	t.Helper()

	value := target.RawGetInteger(index)
	if value.Kind != runtime.KindString || value.String != expected {
		// 槽位缺失或字符串不一致都表示 table 序列移动语义错误。
		t.Fatalf("slot %d mismatch: %v, want %q", index, value.DebugString(), expected)
	}
}
