package runtime

import (
	"errors"
	"math"
	"testing"
)

// TestTableArrayPart 验证正整数 key 会进入数组区。
//
// Lua table 的数组区使用从 1 开始的正整数 key，本测试覆盖基础扩展和读取。
func TestTableArrayPart(t *testing.T) {
	table := NewTable()

	if err := table.RawSet(IntegerValue(2), StringValue("two")); err != nil {
		// 合法正整数 key 不应返回错误。
		t.Fatalf("raw set integer key failed: %v", err)
	}
	if table.ArraySize() != 2 {
		// 写入 key=2 后数组区至少应扩展到 2。
		t.Fatalf("array size mismatch: got %d", table.ArraySize())
	}

	value, err := table.RawGet(IntegerValue(2))
	if err != nil || !value.RawEqual(StringValue("two")) {
		// 正整数 key 必须从数组区读回原值。
		t.Fatalf("array get mismatch: value=%#v err=%v", value, err)
	}

	if got := table.RawGetInteger(1); !got.IsNil() {
		// 未写入的数组槽位必须保持 nil。
		t.Fatalf("empty array slot should be nil: %#v", got)
	}
}

// TestTableResizeBehavior 验证当前 table 数组区扩容与 hash 区增删行为。
//
// 当前实现只做单向数组区扩容，不在删除时收缩；hash 区通过 map 增删非数组 key。
func TestTableResizeBehavior(t *testing.T) {
	table := NewTable()

	table.RawSetInteger(4, StringValue("four"))
	if table.ArraySize() != 4 {
		// 写入较大正整数 key 必须扩展数组区到对应槽位。
		t.Fatalf("array size after grow mismatch: got %d", table.ArraySize())
	}
	if got := table.RawGetInteger(3); !got.IsNil() {
		// 扩容产生的中间槽位必须显式为 nil。
		t.Fatalf("new array gap should be nil: %#v", got)
	}

	table.RawSetInteger(4, NilValue())
	if table.ArraySize() != 4 {
		// 删除数组区末尾值不应收缩底层数组区，避免频繁伸缩影响后续写入。
		t.Fatalf("array size after delete mismatch: got %d", table.ArraySize())
	}
	if got := table.RawGetInteger(4); !got.IsNil() {
		// 删除后的槽位读取必须返回 nil。
		t.Fatalf("deleted array slot should be nil: %#v", got)
	}

	if err := table.RawSet(StringValue("name"), StringValue("lua")); err != nil {
		// hash 区 string key 写入必须成功。
		t.Fatalf("hash set name failed: %v", err)
	}
	if err := table.RawSet(BooleanValue(false), StringValue("false")); err != nil {
		// hash 区 boolean key 写入必须成功。
		t.Fatalf("hash set false failed: %v", err)
	}
	if table.HashSize() != 2 {
		// 两个非数组 key 必须都进入 hash 区。
		t.Fatalf("hash size after set mismatch: got %d", table.HashSize())
	}

	if err := table.RawSet(StringValue("name"), NilValue()); err != nil {
		// hash 区写入 nil 必须删除 key。
		t.Fatalf("hash delete name failed: %v", err)
	}
	if table.HashSize() != 1 {
		// 删除一个 hash key 后 hash 区数量必须减少。
		t.Fatalf("hash size after delete mismatch: got %d", table.HashSize())
	}
}

// TestTableArrayGrowthKeepsVisibleSize 验证数组区扩容只预留容量不暴露额外长度。
//
// 连续整数写入应按几何容量增长以降低分配次数，但 len(arrayValues) 仍必须等于最高可见数组槽位，
// 预留容量不能影响 RawGet、RawNext 或 Len 的 Lua 可观察语义。
func TestTableArrayGrowthKeepsVisibleSize(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(1, StringValue("first"))

	if table.ArraySize() != 1 {
		// 可见数组区长度必须只到已写入的最高 key。
		t.Fatalf("array visible size mismatch: got %d", table.ArraySize())
	}
	if cap(table.arrayValues) <= table.ArraySize() {
		// 首次写入应预留后续连续写入容量。
		t.Fatalf("array capacity was not reserved: len=%d cap=%d", table.ArraySize(), cap(table.arrayValues))
	}
	if got := table.RawGetInteger(2); !got.IsNil() {
		// 未进入可见长度的预留槽位必须按 nil 读取。
		t.Fatalf("reserved slot should read nil: %#v", got)
	}
	if length := table.Len(); length != 1 {
		// 长度边界不能被预留容量影响。
		t.Fatalf("reserved capacity affected length: got %d", length)
	}
	nextKey, nextValue, ok, err := table.RawNext(NilValue())
	if err != nil || !ok || !nextKey.RawEqual(IntegerValue(1)) || !nextValue.RawEqual(StringValue("first")) {
		// RawNext 只能遍历可见数组区的真实元素。
		t.Fatalf("first raw next mismatch: key=%#v value=%#v ok=%v err=%v", nextKey, nextValue, ok, err)
	}
	nextKey, nextValue, ok, err = table.RawNext(IntegerValue(1))
	if err != nil || ok || !nextKey.IsNil() || !nextValue.IsNil() {
		// 预留容量中的 nil 槽位不能产生迭代项。
		t.Fatalf("reserved capacity leaked into raw next: key=%#v value=%#v ok=%v err=%v", nextKey, nextValue, ok, err)
	}
}

// TestTableHashPart 验证非数组 key 会进入 hash 区。
//
// 当前 hash 区支持 string、boolean、非正 integer 和非整数 number。
func TestTableHashPart(t *testing.T) {
	table := NewTable()

	if err := table.RawSet(StringValue("name"), StringValue("lua")); err != nil {
		// string key 必须能写入 hash 区。
		t.Fatalf("raw set string key failed: %v", err)
	}
	if err := table.RawSet(BooleanValue(true), IntegerValue(1)); err != nil {
		// boolean key 必须能写入 hash 区。
		t.Fatalf("raw set boolean key failed: %v", err)
	}
	if err := table.RawSet(NumberValue(1.5), StringValue("half")); err != nil {
		// 非整数 number key 必须能写入 hash 区。
		t.Fatalf("raw set number key failed: %v", err)
	}

	if table.HashSize() != 3 {
		// 三个非数组 key 应全部保存在 hash 区。
		t.Fatalf("hash size mismatch: got %d", table.HashSize())
	}
}

// TestTableReferenceKeys 验证 table 等引用值可作为 hash key。
//
// Lua 5.3 允许 table/function/userdata/thread 作为 table key；key 匹配必须基于对象 identity。
func TestTableReferenceKeys(t *testing.T) {
	table := NewTable()
	keyTable := NewTable()
	otherKeyTable := NewTable()
	keyValue := ReferenceValue(KindTable, keyTable)

	if err := table.RawSet(keyValue, StringValue("kept")); err != nil {
		// table 引用 key 必须可写入 hash 区。
		t.Fatalf("raw set table key failed: %v", err)
	}
	value, err := table.RawGet(keyValue)
	if err != nil {
		// 同一 table 引用 key 必须可读取。
		t.Fatalf("raw get table key failed: %v", err)
	}
	if !value.RawEqual(StringValue("kept")) {
		// 同一 identity 应命中原值。
		t.Fatalf("table key value mismatch: %#v", value)
	}
	otherValue, err := table.RawGet(ReferenceValue(KindTable, otherKeyTable))
	if err != nil {
		// 不同 table 引用 key 也应可编码，但不应命中。
		t.Fatalf("raw get other table key failed: %v", err)
	}
	if !otherValue.IsNil() {
		// 不同 identity 不得串到同一 hash key。
		t.Fatalf("other table key = %#v, want nil", otherValue)
	}

	nextKey, nextValue, ok, err := table.RawNext(NilValue())
	if err != nil || !ok {
		// table 引用 key 写入后必须能被 next 迭代出来。
		t.Fatalf("raw next table key failed: ok=%v err=%v", ok, err)
	}
	if nextKey.Kind != KindTable || nextKey.Ref != keyTable || !nextValue.RawEqual(StringValue("kept")) {
		// next 必须返回原始 table key identity，不能退化成 nil 或只返回地址。
		t.Fatalf("raw next table key mismatch: key=%#v value=%#v", nextKey, nextValue)
	}

	if err := table.RawSet(keyValue, NilValue()); err != nil {
		// 删除 table 引用 key 必须同时清理 hash 值和原始 key 缓存。
		t.Fatalf("raw delete table key failed: %v", err)
	}
	nextKey, nextValue, ok, err = table.RawNext(NilValue())
	if err != nil || ok || !nextKey.IsNil() || !nextValue.IsNil() {
		// 删除后 table 应为空，避免 stale key 缓存污染后续 next。
		t.Fatalf("raw next after table key delete mismatch: key=%#v value=%#v ok=%v err=%v", nextKey, nextValue, ok, err)
	}
}

// TestTableReferenceKeyRawNextBatch 验证多个 table key 可被 RawNext 完整遍历。
//
// Lua 5.3 官方 gc.lua 会连续写入 table 引用 key，并依赖 next/pairs 不重复、不丢失 key。
func TestTableReferenceKeyRawNextBatch(t *testing.T) {
	table := NewTable()
	keys := make([]*Table, 15)
	for index := range keys {
		// 为每个索引创建独立 table identity，模拟 Lua 的 a[{}] = i。
		keys[index] = NewTable()
		if err := table.RawSet(ReferenceValue(KindTable, keys[index]), IntegerValue(int64(index+1))); err != nil {
			// 引用 key 写入不应失败。
			t.Fatalf("raw set table key[%d] failed: %v", index, err)
		}
	}

	seen := make(map[*Table]bool, len(keys))
	currentKey := NilValue()
	for {
		nextKey, nextValue, ok, err := table.RawNext(currentKey)
		if err != nil {
			// 合法迭代链不应返回 invalid key。
			t.Fatalf("raw next batch failed: %v", err)
		}
		if !ok {
			// ok=false 表示迭代结束。
			break
		}
		if nextKey.Kind != KindTable {
			// RawNext 必须返回 table key 本身。
			t.Fatalf("raw next key kind = %v, want table", nextKey.Kind)
		}
		keyRef, ok := nextKey.Ref.(*Table)
		if !ok || keyRef == nil {
			// table key 的引用负载必须保持为 *Table。
			t.Fatalf("raw next key ref mismatch: %#v", nextKey.Ref)
		}
		if seen[keyRef] {
			// 同一 table key 不能在一次迭代中重复出现。
			t.Fatalf("raw next duplicated table key: %#v", keyRef)
		}
		if nextValue.IsNil() {
			// 写入的 value 都是整数，迭代结果不能是 nil。
			t.Fatalf("raw next nil value for key %#v", nextKey)
		}
		seen[keyRef] = true
		currentKey = nextKey
	}
	if len(seen) != len(keys) {
		// RawNext 必须完整返回所有 table key。
		t.Fatalf("raw next table key count = %d, want %d", len(seen), len(keys))
	}
}

// TestTableSweepWeakEntries 验证基础弱 key/value 清理。
//
// `__mode="k"` 应删除 table key 项；`__mode="v"` 应删除引用 value，但保留 key==value 的自保留项。
func TestTableSweepWeakEntries(t *testing.T) {
	weakKeyTable := NewTable()
	weakKeyMetatable := NewTable()
	weakKeyMetatable.RawSetString("__mode", StringValue("k"))
	weakKeyTable.SetMetatable(weakKeyMetatable)
	if err := weakKeyTable.RawSet(ReferenceValue(KindTable, NewTable()), IntegerValue(1)); err != nil {
		// 弱 key table 写入 table key 不应失败。
		t.Fatalf("raw set weak key failed: %v", err)
	}
	if err := weakKeyTable.RawSet(IntegerValue(1), IntegerValue(1)); err != nil {
		// 非可收集 key 应保留。
		t.Fatalf("raw set strong integer key failed: %v", err)
	}
	if !weakKeyTable.SweepWeakEntries(nil) {
		// 弱 table key 应在 sweep 中被删除。
		t.Fatalf("weak key sweep should remove table key")
	}
	if value, err := weakKeyTable.RawGet(IntegerValue(1)); err != nil || !value.RawEqual(IntegerValue(1)) {
		// 非可收集 integer key 必须保留。
		t.Fatalf("weak key table kept integer mismatch: value=%#v err=%v", value, err)
	}

	weakValueTable := NewTable()
	weakValueMetatable := NewTable()
	weakValueMetatable.RawSetString("__mode", StringValue("v"))
	weakValueTable.SetMetatable(weakValueMetatable)
	selfKey := ReferenceValue(KindTable, NewTable())
	if err := weakValueTable.RawSet(IntegerValue(1), ReferenceValue(KindTable, NewTable())); err != nil {
		// 弱 value table 写入可收集 value 不应失败。
		t.Fatalf("raw set weak value failed: %v", err)
	}
	if err := weakValueTable.RawSet(selfKey, selfKey); err != nil {
		// key==value 的自保留项用于模拟强 key 保留同一对象。
		t.Fatalf("raw set self retained value failed: %v", err)
	}
	if !weakValueTable.SweepWeakEntries(nil) {
		// 弱 value 应在 sweep 中被删除。
		t.Fatalf("weak value sweep should remove collectable value")
	}
	if value, err := weakValueTable.RawGet(IntegerValue(1)); err != nil || !value.IsNil() {
		// 可收集 value 应被清成 nil。
		t.Fatalf("weak value table removed value mismatch: value=%#v err=%v", value, err)
	}
	if value, err := weakValueTable.RawGet(selfKey); err != nil || !value.RawEqual(selfKey) {
		// key==value 项应保留。
		t.Fatalf("weak value self retained mismatch: value=%#v err=%v", value, err)
	}
}

// TestTableRejectsInvalidSetKeys 验证 nil key 和 NaN key 写入会返回错误。
//
// 该行为对齐 Lua 5.3 `table index is nil` 与 `table index is NaN` 错误边界。
func TestTableRejectsInvalidSetKeys(t *testing.T) {
	table := NewTable()

	if err := table.RawSet(NilValue(), StringValue("bad")); !errors.Is(err, ErrNilTableKey) {
		// nil key 写入必须返回 ErrNilTableKey。
		t.Fatalf("nil key error mismatch: %v", err)
	}
	if err := table.RawSet(NumberValue(math.NaN()), StringValue("bad")); !errors.Is(err, ErrNaNTableKey) {
		// NaN key 写入必须返回 ErrNaNTableKey。
		t.Fatalf("NaN key error mismatch: %v", err)
	}
}

// TestTableFloatIntegerKeyMatchesIntegerKey 验证可整数化 float key 与 integer key 归并。
//
// Lua 5.3 对 table get 会把整数值 float key 转成 integer key 查询。
func TestTableFloatIntegerKeyMatchesIntegerKey(t *testing.T) {
	table := NewTable()

	if err := table.RawSet(NumberValue(3.0), StringValue("three")); err != nil {
		// 可整数化 float key 应写入数组区。
		t.Fatalf("raw set float integer key failed: %v", err)
	}

	value, err := table.RawGet(IntegerValue(3))
	if err != nil || !value.RawEqual(StringValue("three")) {
		// integer key 必须能读到 float integer key 写入的值。
		t.Fatalf("integer lookup mismatch: value=%#v err=%v", value, err)
	}
}

// TestTableLargeIntegerKeyStaysInHash 验证超大正整数 key 不触发数组区巨量扩容。
//
// Lua 5.3 官方 attrib.lua 会使用接近 float 精确整数上限的 key；这类稀疏 key 必须进入
// hash 区，同时 integer key 与整数化 float key 仍要命中同一位置。
func TestTableLargeIntegerKeyStaysInHash(t *testing.T) {
	table := NewTable()
	largeKey := int64(1 << 40)

	if err := table.RawSet(IntegerValue(largeKey), StringValue("large")); err != nil {
		// 超大正整数 key 仍是合法 table key。
		t.Fatalf("raw set large integer key failed: %v", err)
	}
	if table.ArraySize() != 0 {
		// 稀疏大 key 不应扩展数组区，避免不可控内存分配。
		t.Fatalf("large integer key grew array size=%d", table.ArraySize())
	}
	if table.HashSize() != 1 {
		// 大整数 key 应落入 hash 区。
		t.Fatalf("large integer key hash size=%d", table.HashSize())
	}

	integerValue, integerErr := table.RawGet(IntegerValue(largeKey))
	floatValue, floatErr := table.RawGet(NumberValue(float64(largeKey)))
	if integerErr != nil || floatErr != nil || !integerValue.RawEqual(StringValue("large")) || !floatValue.RawEqual(StringValue("large")) {
		// integer 和整数化 float 读取都必须命中同一 hash key。
		t.Fatalf("large key lookup mismatch: int=%#v intErr=%v float=%#v floatErr=%v", integerValue, integerErr, floatValue, floatErr)
	}
}

// TestTableSetNilDeletesKey 验证通过通用 RawSet 写入 nil 会删除 key。
//
// 删除语义必须同时适用于数组区和 hash 区。
func TestTableSetNilDeletesKey(t *testing.T) {
	table := NewTable()
	if err := table.RawSet(IntegerValue(1), StringValue("one")); err != nil {
		// 数组区写入必须成功。
		t.Fatalf("raw set array key failed: %v", err)
	}
	if err := table.RawSet(StringValue("name"), StringValue("lua")); err != nil {
		// hash 区写入必须成功。
		t.Fatalf("raw set hash key failed: %v", err)
	}

	if err := table.RawSet(IntegerValue(1), NilValue()); err != nil {
		// 数组区删除必须成功。
		t.Fatalf("raw delete array key failed: %v", err)
	}
	if err := table.RawSet(StringValue("name"), NilValue()); err != nil {
		// hash 区删除必须成功。
		t.Fatalf("raw delete hash key failed: %v", err)
	}

	arrayValue, arrayErr := table.RawGet(IntegerValue(1))
	hashValue, hashErr := table.RawGet(StringValue("name"))
	if arrayErr != nil || hashErr != nil || !arrayValue.IsNil() || !hashValue.IsNil() {
		// 删除后两个 key 都必须读取 nil。
		t.Fatalf("deleted key mismatch: array=%#v arrayErr=%v hash=%#v hashErr=%v", arrayValue, arrayErr, hashValue, hashErr)
	}
}

// TestTableLenArrayBoundary 验证 table 长度会返回连续数组区边界。
//
// 该测试覆盖连续正整数 key、末尾删除和首位 nil 的基础 `luaH_getn` 行为。
func TestTableLenArrayBoundary(t *testing.T) {
	table := NewTable()
	for index := int64(1); index <= 3; index++ {
		// 连续写入 1..3，构造明确的数组区边界。
		table.RawSetInteger(index, StringValue("value"))
	}

	if length := table.Len(); length != 3 {
		// 连续数组区的长度必须落在最后一个非 nil 正整数 key。
		t.Fatalf("contiguous length mismatch: got %d", length)
	}

	table.RawSetInteger(3, NilValue())
	if length := table.Len(); length != 2 {
		// 删除末尾元素后，长度边界必须回退到 key=2。
		t.Fatalf("tail delete length mismatch: got %d", length)
	}

	table.RawSetInteger(1, NilValue())
	if length := table.Len(); length != 0 {
		// key=1 为 nil 时，当前实现按 Lua 5.3 边界搜索返回 0。
		t.Fatalf("leading nil length mismatch: got %d", length)
	}
}

// TestTableLenSparseArrayBoundary 验证稀疏数组区长度边界。
//
// Lua 对带空洞 table 的长度结果只保证返回某个边界；当前实现对数组区末尾非 nil
// 且 key=1 为 nil 的场景会返回数组区末尾边界，保持与当前搜索策略一致。
func TestTableLenSparseArrayBoundary(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(2, StringValue("two"))

	if length := table.Len(); length != 2 {
		// key=2 非 nil 且 key=3 nil，当前数组区边界为 2。
		t.Fatalf("sparse array length mismatch: got %d", length)
	}
}

// TestTableLenCacheTracksDenseAppend 验证 table 长度缓存覆盖连续追加热路径。
//
// 官方 constructs.lua 会大量执行 `res[#res + 1] = value`；该场景必须保持 O(1) 推进边界，
// 同时在删除边界内元素后失效并回退到正常搜索。
func TestTableLenCacheTracksDenseAppend(t *testing.T) {
	table := NewTable()
	if length := table.Len(); length != 0 {
		// 空表首次 Len 会建立 0 边界缓存。
		t.Fatalf("empty cached length mismatch: got %d", length)
	}

	for index := int64(1); index <= 1024; index++ {
		// 连续按 #t+1 追加，长度缓存应持续向后推进。
		table.RawSetInteger(index, StringValue("value"))
		if length := table.Len(); length != index {
			// 每次追加后 Len 必须立刻返回最新边界。
			t.Fatalf("append length[%d] mismatch: got %d", index, length)
		}
	}

	table.RawSetInteger(1024, NilValue())
	if length := table.Len(); length != 1023 {
		// 删除边界元素后缓存必须失效，并由边界搜索得到新长度。
		t.Fatalf("delete cached boundary mismatch: got %d", length)
	}
}

// TestTableLenHashBoundary 验证长度搜索会跨过 hash 区中的整数 key。
//
// 正常 RawSetInteger 会把正整数 key 放入数组区；这里直接放入 hash 区，是为了模拟后续
// resize 策略可能把整数 key 保留在 hash 区时的 `luaH_getn` 搜索路径。
func TestTableLenHashBoundary(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(1, StringValue("one"))
	table.RawSetInteger(2, StringValue("two"))
	table.ensureHashStorage()
	table.hashValues[tableKey{kind: KindInteger, integerValue: 3}] = StringValue("three")

	if length := table.Len(); length != 3 {
		// 数组区末尾非 nil 时，长度搜索必须继续检查 hash 区整数 key。
		t.Fatalf("hash boundary length mismatch: got %d", length)
	}
}

// TestTableMetatableAccess 验证 table raw 元表读取与设置。
//
// 当前方法服务 VM 内部元方法查找，不处理 `__metatable` 保护语义。
func TestTableMetatableAccess(t *testing.T) {
	table := NewTable()
	metatable := NewTable()
	metatable.RawSetString("__name", StringValue("meta"))

	table.SetMetatable(metatable)
	if got := table.GetMetatable(); got != metatable {
		// 设置后必须返回同一个元表引用，保证元方法查找使用同一状态。
		t.Fatalf("metatable reference mismatch")
	}

	table.SetMetatable(nil)
	if got := table.GetMetatable(); got != nil {
		// 传入 nil 必须移除元表，后续普通 get/set 不应再触发旧元方法。
		t.Fatalf("metatable should be removed")
	}
}

// TestTableProtectedMetatable 验证 `__metatable` 对公开元表访问的保护语义。
//
// Lua 5.3 中 `getmetatable` 会返回保护字段，`setmetatable` 会拒绝修改受保护元表。
func TestTableProtectedMetatable(t *testing.T) {
	table := NewTable()
	metatable := NewTable()

	if err := table.SetMetatableChecked(metatable); err != nil {
		// 当前 table 没有旧元表时，公开设置应成功。
		t.Fatalf("set initial metatable failed: %v", err)
	}
	if got := table.MetatableValue(); got.Kind != KindTable || got.Ref != metatable {
		// 未受保护时，公开读取必须返回真实元表引用。
		t.Fatalf("public metatable mismatch: %#v", got)
	}

	metatable.RawSetString("__metatable", StringValue("locked"))
	if got := table.MetatableValue(); !got.RawEqual(StringValue("locked")) {
		// `__metatable` 非 nil 时，公开读取必须隐藏真实元表。
		t.Fatalf("protected metatable value mismatch: %#v", got)
	}

	nextMetatable := NewTable()
	if err := table.SetMetatableChecked(nextMetatable); !errors.Is(err, ErrProtectedMetatable) {
		// 受保护元表不能通过公开设置路径替换。
		t.Fatalf("protected set error mismatch: %v", err)
	}
	if got := table.GetMetatable(); got != metatable {
		// 设置失败后必须保留原始元表引用。
		t.Fatalf("protected metatable should keep original reference")
	}
}

// TestTableGetPrefersRawValue 验证普通 get 会优先返回 raw 命中值。
//
// Lua 5.3 中 table 已有字段不会触发 `__index` 元方法。
func TestTableGetPrefersRawValue(t *testing.T) {
	table := NewTable()
	metatable := NewTable()
	indexTable := NewTable()
	table.RawSetString("name", StringValue("raw"))
	indexTable.RawSetString("name", StringValue("meta"))
	metatable.RawSetString("__index", ReferenceValue(KindTable, indexTable))
	table.SetMetatable(metatable)

	value, err := table.Get(StringValue("name"))
	if err != nil || !value.RawEqual(StringValue("raw")) {
		// raw 命中时必须返回原表字段，不读取 `__index` table。
		t.Fatalf("raw value mismatch: value=%#v err=%v", value, err)
	}
}

// TestTableGetUsesTableIndexMetamethod 验证普通 get 会使用 table 形式 `__index`。
//
// 当前阶段只支持 `__index` 为 table；函数形式会在调用栈实现后补齐。
func TestTableGetUsesTableIndexMetamethod(t *testing.T) {
	table := NewTable()
	metatable := NewTable()
	indexTable := NewTable()
	indexTable.RawSetString("name", StringValue("meta"))
	metatable.RawSetString("__index", ReferenceValue(KindTable, indexTable))
	table.SetMetatable(metatable)

	value, err := table.Get(StringValue("name"))
	if err != nil || !value.RawEqual(StringValue("meta")) {
		// raw 未命中时必须从 `__index` table 读取字段。
		t.Fatalf("index value mismatch: value=%#v err=%v", value, err)
	}
}

// TestTableGetFollowsIndexTableChain 验证普通 get 会继续跟随 `__index` table 的元表链。
//
// 该行为对齐 Lua 5.3 luaV_finishget 在 `__index` 为 table 时继续取表字段的流程。
func TestTableGetFollowsIndexTableChain(t *testing.T) {
	table := NewTable()
	firstMetatable := NewTable()
	firstIndexTable := NewTable()
	secondMetatable := NewTable()
	secondIndexTable := NewTable()
	secondIndexTable.RawSetString("name", StringValue("deep"))
	secondMetatable.RawSetString("__index", ReferenceValue(KindTable, secondIndexTable))
	firstIndexTable.SetMetatable(secondMetatable)
	firstMetatable.RawSetString("__index", ReferenceValue(KindTable, firstIndexTable))
	table.SetMetatable(firstMetatable)

	value, err := table.Get(StringValue("name"))
	if err != nil || !value.RawEqual(StringValue("deep")) {
		// 第一层 `__index` table 未命中时，应继续检查它自己的元表链。
		t.Fatalf("chained index value mismatch: value=%#v err=%v", value, err)
	}
}

// TestTableGetUsesFunctionIndexMetamethod 验证普通 get 会调用 Go function 形式 `__index`。
//
// __index 函数会收到当前 lookup table 与 key 两个参数，返回第一返回值作为最终读取结果。
func TestTableGetUsesFunctionIndexMetamethod(t *testing.T) {
	table := NewTable()
	var called bool
	var gotReceiverKind ValueKind
	var gotKey Value
	metatable := NewTable()
	indexMetamethod := GoFunction(func(args ...Value) (Value, error) {
		// `__index` 元方法接收调用者与 key 两个参数。
		called = true
		if len(args) != 2 {
			// Lua 调用约定要求参数长度固定为 2，长度错误直接返回 nil 触发测试。
			return NilValue(), nil
		}
		gotReceiverKind = args[0].Kind
		gotKey = args[1]
		// 返回一个可区分的字符串用于校验最终读取结果。
		return StringValue("from-index"), nil
	})
	metatable.RawSetString("__index", ReferenceValue(KindGoClosure, indexMetamethod))
	table.SetMetatable(metatable)

	value, err := table.Get(StringValue("name"))
	if err != nil {
		// `__index` 函数必须可执行，返回错误表示函数路径未接通。
		t.Fatalf("function index failed: %v", err)
	}
	if !called {
		// 没有触发 __index 分支表示 table lookup 未走函数路径。
		t.Fatalf("index function should be called")
	}
	if gotReceiverKind != KindTable {
		// 第一个参数必须是查找表对象，保证 Go 侧调用约定成立。
		t.Fatalf("index function receiver kind mismatch: got=%v", gotReceiverKind)
	}
	if !gotKey.RawEqual(StringValue("name")) {
		// 第二参数必须是查找 key 本身。
		t.Fatalf("index function key mismatch: got=%#v", gotKey)
	}
	if !value.RawEqual(StringValue("from-index")) {
		// __index 函数返回值必须写回 GETTABLE 读取结果。
		t.Fatalf("index function result mismatch: %#v", value)
	}
}

// TestTableSetPrefersRawExistingValue 验证普通 set 会优先覆盖已有 raw 字段。
//
// Lua 5.3 中 table 已有字段写入不会触发 `__newindex` 元方法。
func TestTableSetPrefersRawExistingValue(t *testing.T) {
	table := NewTable()
	metatable := NewTable()
	newIndexTable := NewTable()
	table.RawSetString("name", StringValue("raw"))
	metatable.RawSetString("__newindex", ReferenceValue(KindTable, newIndexTable))
	table.SetMetatable(metatable)

	if err := table.Set(StringValue("name"), StringValue("updated")); err != nil {
		// raw 已有字段的普通写入必须成功。
		t.Fatalf("set raw existing value failed: %v", err)
	}
	if got := table.RawGetString("name"); !got.RawEqual(StringValue("updated")) {
		// 写入必须落在原表 raw 字段。
		t.Fatalf("raw existing value mismatch: %#v", got)
	}
	if got := newIndexTable.RawGetString("name"); !got.IsNil() {
		// raw 已有字段时不应写入 `__newindex` table。
		t.Fatalf("newindex table should not receive raw existing write: %#v", got)
	}
}

// TestTableSetWritesRawWhenNoNewIndex 验证没有 `__newindex` 时普通 set 写入原表。
//
// 该行为覆盖无元表和元表不含 `__newindex` 两类基础路径。
func TestTableSetWritesRawWhenNoNewIndex(t *testing.T) {
	table := NewTable()
	metatable := NewTable()
	table.SetMetatable(metatable)

	if err := table.Set(StringValue("name"), StringValue("raw")); err != nil {
		// 没有 `__newindex` 时写入应落在原表。
		t.Fatalf("set without newindex failed: %v", err)
	}
	if got := table.RawGetString("name"); !got.RawEqual(StringValue("raw")) {
		// 原表必须能 raw 读回写入值。
		t.Fatalf("raw write without newindex mismatch: %#v", got)
	}
}

// TestTableSetUsesTableNewIndexMetamethod 验证普通 set 会使用 table 形式 `__newindex`。
//
// `__newindex` 为 table 时，会沿 `__newindex` 链查找 raw 字段并写入。
func TestTableSetUsesTableNewIndexMetamethod(t *testing.T) {
	table := NewTable()
	metatable := NewTable()
	newIndexTable := NewTable()
	metatable.RawSetString("__newindex", ReferenceValue(KindTable, newIndexTable))
	table.SetMetatable(metatable)

	if err := table.Set(StringValue("name"), StringValue("meta")); err != nil {
		// table 形式 `__newindex` 写入必须成功。
		t.Fatalf("set table newindex failed: %v", err)
	}
	if got := table.RawGetString("name"); !got.IsNil() {
		// raw 未命中时，写入不应落在原表。
		t.Fatalf("source table should not receive newindex write: %#v", got)
	}
	if got := newIndexTable.RawGetString("name"); !got.RawEqual(StringValue("meta")) {
		// 写入必须落在 `__newindex` table。
		t.Fatalf("newindex table write mismatch: %#v", got)
	}
}

// TestTableSetFollowsNewIndexTableChain 验证普通 set 会继续跟随 `__newindex` table 链。
//
// 链上 table 没有目标字段且没有 `__newindex` 时，最终写入落在该链尾 table。
func TestTableSetFollowsNewIndexTableChain(t *testing.T) {
	table := NewTable()
	firstMetatable := NewTable()
	firstNewIndexTable := NewTable()
	secondMetatable := NewTable()
	secondNewIndexTable := NewTable()
	secondMetatable.RawSetString("__newindex", ReferenceValue(KindTable, secondNewIndexTable))
	firstNewIndexTable.SetMetatable(secondMetatable)
	firstMetatable.RawSetString("__newindex", ReferenceValue(KindTable, firstNewIndexTable))
	table.SetMetatable(firstMetatable)

	if err := table.Set(StringValue("name"), StringValue("deep")); err != nil {
		// 链式 table 型 `__newindex` 写入必须成功。
		t.Fatalf("set chained newindex failed: %v", err)
	}
	if got := secondNewIndexTable.RawGetString("name"); !got.RawEqual(StringValue("deep")) {
		// 写入应落到链尾 table。
		t.Fatalf("chained newindex write mismatch: %#v", got)
	}
}

// TestTableSetUsesFunctionNewIndexMetamethod 验证普通 set 会调用 Go function 形式 `__newindex`。
//
// `__newindex` 函数应收到当前 lookup table、key 与 value 三个参数，返回值应被忽略。
func TestTableSetUsesFunctionNewIndexMetamethod(t *testing.T) {
	table := NewTable()
	var called bool
	var gotReceiverKind ValueKind
	var gotKey Value
	var gotValue Value
	metatable := NewTable()
	newIndexMetamethod := GoFunction(func(args ...Value) (Value, error) {
		// `__newindex` 元方法接收调用者、key 与 value。
		called = true
		if len(args) != 3 {
			// Lua 调用约定要求参数长度固定为 3，长度错误直接返回 nil 触发测试。
			return NilValue(), nil
		}
		gotReceiverKind = args[0].Kind
		gotKey = args[1]
		gotValue = args[2]
		// 模拟 Go 层处理；返回值在 Lua set 路径会被忽略。
		return NilValue(), nil
	})
	metatable.RawSetString("__newindex", ReferenceValue(KindGoClosure, newIndexMetamethod))
	table.SetMetatable(metatable)

	err := table.Set(StringValue("name"), StringValue("value"))
	if err != nil {
		// `__newindex` 函数执行失败说明当前阶段函数元方法路径仍不稳定。
		t.Fatalf("unsupported newindex mismatch: %v", err)
	}
	if !called {
		// 函数形式 `__newindex` 必须被调用，raw set 不应默认为默认回退。
		t.Fatalf("newindex function should be called")
	}
	if gotReceiverKind != KindTable {
		// 第一个参数必须是 lookup table 对象。
		t.Fatalf("newindex function receiver kind mismatch: got=%v", gotReceiverKind)
	}
	if !gotKey.RawEqual(StringValue("name")) {
		// 第二参数必须是待写入 key。
		t.Fatalf("newindex function key mismatch: %#v", gotKey)
	}
	if !gotValue.RawEqual(StringValue("value")) {
		// 第三参数必须是待写入 value。
		t.Fatalf("newindex function value mismatch: %#v", gotValue)
	}
	if got := table.RawGetString("name"); !got.IsNil() {
		// 不支持的 `__newindex` 不能落盘到原表，避免错误状态污染。
		t.Fatalf("source table should remain unchanged: %#v", got)
	}
}

// TestTableRawNextIteratesStableEntries 验证 RawNext 按稳定顺序遍历 raw 内容。
//
// 当前 Go 实现为了可测试性采用数组区升序、hash 区稳定排序；Lua 规范不保证 hash 顺序。
func TestTableRawNextIteratesStableEntries(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(1, StringValue("one"))
	table.RawSetInteger(3, StringValue("three"))
	table.RawSetString("name", StringValue("lua"))
	table.RawSetString("kind", StringValue("vm"))

	var seenKeys []Value
	currentKey := NilValue()
	for {
		nextKey, nextValue, ok, err := table.RawNext(currentKey)
		if err != nil {
			// 合法迭代链不应返回错误。
			t.Fatalf("raw next failed: %v", err)
		}
		if !ok {
			// ok=false 表示迭代结束。
			break
		}
		if nextValue.IsNil() {
			// RawNext 不应返回 nil value 项。
			t.Fatalf("raw next returned nil value for key %#v", nextKey)
		}
		seenKeys = append(seenKeys, nextKey)
		currentKey = nextKey
	}

	wantKeys := []Value{IntegerValue(1), IntegerValue(3), StringValue("kind"), StringValue("name")}
	if len(seenKeys) != len(wantKeys) {
		// 遍历数量必须等于非 nil raw 项数量。
		t.Fatalf("raw next key count mismatch: got %d want %d", len(seenKeys), len(wantKeys))
	}
	for index, wantKey := range wantKeys {
		if !seenKeys[index].RawEqual(wantKey) {
			// 稳定顺序必须符合当前实现约定。
			t.Fatalf("raw next key[%d] mismatch: got %#v want %#v", index, seenKeys[index], wantKey)
		}
	}
}

// TestTableRawNextContinuesAfterKey 验证 RawNext 会从指定 key 后继续。
//
// 该行为对应 Lua `next(table, key)` 的继续迭代语义。
func TestTableRawNextContinuesAfterKey(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(1, StringValue("one"))
	table.RawSetString("name", StringValue("lua"))

	nextKey, nextValue, ok, err := table.RawNext(IntegerValue(1))
	if err != nil || !ok {
		// 从已存在 key 继续时必须找到后续 hash 项。
		t.Fatalf("raw next continue failed: ok=%v err=%v", ok, err)
	}
	if !nextKey.RawEqual(StringValue("name")) || !nextValue.RawEqual(StringValue("lua")) {
		// 返回项必须是 key=1 之后的下一项。
		t.Fatalf("raw next continue mismatch: key=%#v value=%#v", nextKey, nextValue)
	}

	_, _, ok, err = table.RawNext(StringValue("name"))
	if err != nil || ok {
		// 最后一项之后必须返回 ok=false 且无错误。
		t.Fatalf("raw next end mismatch: ok=%v err=%v", ok, err)
	}
}

// TestTableRawNextContinuesAfterDeletingCurrentKey 验证迭代中删除当前 key 后仍可继续。
//
// Lua 5.3 允许 `for k in pairs(t) do t[k] = nil end` 这类清空表写法；next(table, oldKey)
// 需要用删除前的迭代位置寻找后继，而不是因为当前 table 已缺少 oldKey 直接报错。
func TestTableRawNextContinuesAfterDeletingCurrentKey(t *testing.T) {
	table := NewTable()
	table.RawSetString("a", IntegerValue(1))
	table.RawSetString("b", IntegerValue(2))
	table.RawSetString("c", IntegerValue(3))

	currentKey, _, ok, err := table.RawNext(NilValue())
	if err != nil || !ok {
		// 首次迭代必须得到一个有效 key。
		t.Fatalf("first raw next failed: ok=%v err=%v", ok, err)
	}
	if err := table.RawSet(currentKey, NilValue()); err != nil {
		// 删除当前 key 必须是合法 raw 写入。
		t.Fatalf("delete current key failed: %v", err)
	}

	nextKey, nextValue, ok, err := table.RawNext(currentKey)
	if err != nil || !ok {
		// 删除当前 key 后继续 next 不应触发 invalid key。
		t.Fatalf("raw next after deleting current key failed: ok=%v err=%v", ok, err)
	}
	if currentValue, getErr := table.RawGet(nextKey); getErr != nil || currentValue.IsNil() || !currentValue.RawEqual(nextValue) {
		// 返回的后继项必须仍存在于当前 table。
		t.Fatalf("raw next returned stale entry: key=%#v value=%#v current=%#v err=%v", nextKey, nextValue, currentValue, getErr)
	}
}

// TestTableRawNextCacheInvalidatesOnMutation 验证 RawNext 快照缓存会随 raw 写入失效。
//
// pairs/next 热路径会复用同一版本的稳定快照；新增字段后必须重建快照，避免漏掉新 key。
func TestTableRawNextCacheInvalidatesOnMutation(t *testing.T) {
	table := NewTable()
	table.RawSetString("a", IntegerValue(1))

	firstKey, _, ok, err := table.RawNext(NilValue())
	if err != nil || !ok || !firstKey.RawEqual(StringValue("a")) {
		// 首次迭代应建立包含 key=a 的快照。
		t.Fatalf("first raw next mismatch: key=%#v ok=%v err=%v", firstKey, ok, err)
	}
	if !table.iterationCacheValid {
		// 首次 RawNext 后快照缓存必须可用。
		t.Fatalf("iteration cache should be valid after RawNext")
	}

	table.RawSetString("b", IntegerValue(2))
	if table.iterationCacheValid {
		// raw 写入必须让旧快照失效。
		t.Fatalf("iteration cache should be invalid after mutation")
	}

	var seenKeys []Value
	currentKey := NilValue()
	for {
		nextKey, _, ok, err := table.RawNext(currentKey)
		if err != nil {
			// 合法迭代不应失败。
			t.Fatalf("raw next after mutation failed: %v", err)
		}
		if !ok {
			// ok=false 表示迭代完成。
			break
		}
		seenKeys = append(seenKeys, nextKey)
		currentKey = nextKey
	}
	if len(seenKeys) != 2 || !seenKeys[0].RawEqual(StringValue("a")) || !seenKeys[1].RawEqual(StringValue("b")) {
		// 新快照必须包含 mutation 后新增的 key。
		t.Fatalf("seen keys after mutation mismatch: %#v", seenKeys)
	}
}

// TestTableRawNextSkipsNilEntries 验证 RawNext 跳过 nil 槽位和值。
//
// nil 表示 table 中没有该字段，迭代时不能返回。
func TestTableRawNextSkipsNilEntries(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(2, StringValue("two"))
	table.ensureHashStorage()
	table.hashValues[tableKey{kind: KindString, stringValue: "deleted"}] = NilValue()

	nextKey, nextValue, ok, err := table.RawNext(NilValue())
	if err != nil || !ok {
		// table 仍有 key=2，应能返回一项。
		t.Fatalf("raw next skip nil failed: ok=%v err=%v", ok, err)
	}
	if !nextKey.RawEqual(IntegerValue(2)) || !nextValue.RawEqual(StringValue("two")) {
		// 迭代必须跳过数组 key=1 nil 和 hash nil 值。
		t.Fatalf("raw next skip nil mismatch: key=%#v value=%#v", nextKey, nextValue)
	}

	_, _, ok, err = table.RawNext(nextKey)
	if err != nil || ok {
		// 跳过 nil 后没有更多元素，必须结束。
		t.Fatalf("raw next skip nil end mismatch: ok=%v err=%v", ok, err)
	}
}

// TestTableRawNextRejectsInvalidKey 验证 RawNext 对不存在的继续 key 返回错误。
//
// Lua 5.3 `next` 在收到无效 key 时会报告 invalid key to 'next'。
func TestTableRawNextRejectsInvalidKey(t *testing.T) {
	table := NewTable()
	table.RawSetString("name", StringValue("lua"))

	nextKey, nextValue, ok, err := table.RawNext(StringValue("missing"))
	if !nextKey.IsNil() || !nextValue.IsNil() || ok || !errors.Is(err, ErrInvalidTableIterationKey) {
		// 无效 key 必须返回明确错误，并且不产生迭代项。
		t.Fatalf("invalid raw next mismatch: key=%#v value=%#v ok=%v err=%v", nextKey, nextValue, ok, err)
	}
}

// TestTableRawPairsNextUsesRawNextSemantics 验证 RawPairsNext 复用 raw next 迭代语义。
//
// 当前阶段不处理 `__pairs` 元方法，pairs 的基础迭代与 next 保持一致。
func TestTableRawPairsNextUsesRawNextSemantics(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(1, StringValue("one"))
	table.RawSetString("name", StringValue("lua"))

	firstKey, firstValue, ok, err := table.RawPairsNext(NilValue())
	if err != nil || !ok {
		// nil 起始 key 必须返回第一项。
		t.Fatalf("raw pairs first failed: ok=%v err=%v", ok, err)
	}
	if !firstKey.RawEqual(IntegerValue(1)) || !firstValue.RawEqual(StringValue("one")) {
		// 当前稳定顺序下，第一项是数组区 key=1。
		t.Fatalf("raw pairs first mismatch: key=%#v value=%#v", firstKey, firstValue)
	}

	secondKey, secondValue, ok, err := table.RawPairsNext(firstKey)
	if err != nil || !ok {
		// 从第一项继续时必须返回第二项。
		t.Fatalf("raw pairs second failed: ok=%v err=%v", ok, err)
	}
	if !secondKey.RawEqual(StringValue("name")) || !secondValue.RawEqual(StringValue("lua")) {
		// 第二项必须是 hash 区 string key。
		t.Fatalf("raw pairs second mismatch: key=%#v value=%#v", secondKey, secondValue)
	}

	_, _, ok, err = table.RawPairsNext(secondKey)
	if err != nil || ok {
		// 最后一项之后必须正常结束。
		t.Fatalf("raw pairs end mismatch: ok=%v err=%v", ok, err)
	}
}

// TestTableRawIPairsNextIteratesArrayPrefix 验证 RawIPairsNext 遍历连续数组前缀。
//
// Lua 5.3 ipairs 从索引 1 开始，逐个返回正整数 key，直到遇到第一个 nil。
func TestTableRawIPairsNextIteratesArrayPrefix(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(1, StringValue("one"))
	table.RawSetInteger(2, StringValue("two"))

	firstIndex, firstValue, ok := table.RawIPairsNext(0)
	if !ok || firstIndex != 1 || !firstValue.RawEqual(StringValue("one")) {
		// 第一次迭代必须返回 key=1。
		t.Fatalf("first ipairs mismatch: index=%d value=%#v ok=%v", firstIndex, firstValue, ok)
	}

	secondIndex, secondValue, ok := table.RawIPairsNext(firstIndex)
	if !ok || secondIndex != 2 || !secondValue.RawEqual(StringValue("two")) {
		// 第二次迭代必须返回 key=2。
		t.Fatalf("second ipairs mismatch: index=%d value=%#v ok=%v", secondIndex, secondValue, ok)
	}

	_, endValue, ok := table.RawIPairsNext(secondIndex)
	if ok || !endValue.IsNil() {
		// key=3 为 nil 时必须结束。
		t.Fatalf("ipairs should end at first nil: value=%#v ok=%v", endValue, ok)
	}
}

// TestTableRawIPairsNextStopsAtHole 验证 RawIPairsNext 遇到数组空洞立即停止。
//
// 即使后续更大整数 key 有值，ipairs 也不会越过第一个 nil。
func TestTableRawIPairsNextStopsAtHole(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(2, StringValue("two"))

	index, value, ok := table.RawIPairsNext(0)
	if index != 0 || !value.IsNil() || ok {
		// key=1 为 nil 时，迭代必须立即结束。
		t.Fatalf("ipairs hole mismatch: index=%d value=%#v ok=%v", index, value, ok)
	}
}

// TestTableRawIPairsNextReadsIntegerHashKey 验证 RawIPairsNext 使用 raw integer 读取。
//
// 当前 table resize 未实现，测试直接写入 hash 区整数 key 来锁定未来 resize 后的读取语义。
func TestTableRawIPairsNextReadsIntegerHashKey(t *testing.T) {
	table := NewTable()
	table.ensureHashStorage()
	table.hashValues[tableKey{kind: KindInteger, integerValue: 1}] = StringValue("one")

	index, value, ok := table.RawIPairsNext(0)
	if !ok || index != 1 || !value.RawEqual(StringValue("one")) {
		// raw integer 读取必须能覆盖 hash 区中的正整数 key。
		t.Fatalf("ipairs integer hash mismatch: index=%d value=%#v ok=%v", index, value, ok)
	}
}

// TestTableRawIPairsNextStopsAtMaxIndex 验证 RawIPairsNext 的索引上限保护。
//
// 该保护避免 index+1 在 int64 上溢。
func TestTableRawIPairsNextStopsAtMaxIndex(t *testing.T) {
	table := NewTable()

	index, value, ok := table.RawIPairsNext(maxTableIntegerIndex)
	if index != 0 || !value.IsNil() || ok {
		// 超过可递增范围时必须直接结束。
		t.Fatalf("ipairs max index mismatch: index=%d value=%#v ok=%v", index, value, ok)
	}
}
