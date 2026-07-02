package runtime

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
)

var (
	// ErrNilTableKey 表示尝试使用 nil 作为 table key。
	ErrNilTableKey = errors.New("table index is nil")
	// ErrNaNTableKey 表示尝试使用 NaN 作为 table key。
	ErrNaNTableKey = errors.New("table index is NaN")
	// ErrProtectedMetatable 表示尝试修改带 `__metatable` 保护字段的元表。
	ErrProtectedMetatable = errors.New("cannot change a protected metatable")
	// ErrUnsupportedIndexMetamethod 表示 `__index` 元方法返回了当前实现未支持的类型。
	ErrUnsupportedIndexMetamethod = errors.New("unsupported __index metamethod")
	// ErrUnsupportedNewIndexMetamethod 表示 `__newindex` 元方法返回了当前实现未支持的类型。
	ErrUnsupportedNewIndexMetamethod = errors.New("unsupported __newindex metamethod")
	// ErrMetamethodChainTooLong 表示元方法查询链超过 VM 防护上限。
	ErrMetamethodChainTooLong = errors.New("metamethod chain too long")
	// ErrInvalidTableIterationKey 表示 next 使用了当前 table 中不存在的 key。
	ErrInvalidTableIterationKey = errors.New("invalid key to next")
)

const (
	// maxTableIntegerIndex 表示当前 table 长度搜索可处理的最大正整数 key。
	maxTableIntegerIndex = int64(^uint64(0) >> 1)
	// maxTableArrayIndex 表示正整数 key 进入数组区的最大索引，超过该值的稀疏 key
	// 写入 hash 区，避免官方测试中的大整数索引触发巨量 slice 扩容。
	maxTableArrayIndex = int64(1 << 20)
	// maxTableIndexChainDepth 表示 table 形式 `__index` 链的最大跟随深度。
	maxTableIndexChainDepth = 100
	// metatableProtectionKey 表示 Lua 5.3 用于保护元表公开访问的字段名。
	metatableProtectionKey = "__metatable"
	// tableIndexMetamethodKey 表示 Lua 5.3 table 读取时使用的 `__index` 元方法字段名。
	tableIndexMetamethodKey = "__index"
	// tableNewIndexMetamethodKey 表示 Lua 5.3 table 写入时使用的 `__newindex` 元方法字段名。
	tableNewIndexMetamethodKey = "__newindex"
	// tableWeakModeKey 表示 Lua 5.3 弱表元表中的 `__mode` 字段名。
	tableWeakModeKey = "__mode"
	// tableArrayDoublingLimit 表示数组区使用 2 倍扩容的上限；超过后改用 1.5 倍减少大表多余扫描。
	tableArrayDoublingLimit = 1 << 16
)

// Table 表示 Lua table 的第一阶段实现。
//
// 当前结构提供数组区和 hash 区的 raw get/set 能力；元表、next 迭代和弱表语义会在
// 后续 Table 阶段补齐。
type Table struct {
	// arrayValues 保存正整数 key 的数组区，Lua key 从 1 开始映射到 index 0。
	arrayValues []Value
	// hashValues 保存非数组区 key 到 Lua 值的映射。
	hashValues map[tableKey]Value
	// hashKeys 保存 hash 区 tableKey 对应的原始 Lua key，用于 next 返回引用 identity key。
	hashKeys map[tableKey]Value
	// metatable 保存当前 table 的元表；nil 表示未设置元表。
	metatable *Table
	// lengthCache 保存最近一次 Lua `#table` 计算出的边界，用于密集数组追加热路径。
	lengthCache int64
	// lengthCacheValid 表示 lengthCache 是否仍可复用。
	lengthCacheValid bool
	// mutationVersion 保存 raw 写入版本，供上层缓存按 table 变更失效。
	mutationVersion uint64
	// iterationCache 保存当前版本的 raw next 稳定迭代快照。
	iterationCache []tableEntry
	// iterationCacheVersion 保存 iterationCache 对应的 table raw 写入版本。
	iterationCacheVersion uint64
	// iterationCacheValid 表示 iterationCache 是否可复用。
	iterationCacheValid bool
	// staleIterationCache 保存最近一次失效前的 raw next 快照，用于兼容迭代中删除当前 key 后继续 next。
	staleIterationCache []tableEntry
	// staleIterationCacheValid 表示 staleIterationCache 可作为已删除当前 key 的续迭代兜底。
	staleIterationCacheValid bool
}

// tableKey 表示可放入 hash 区的 Lua key。
//
// 该结构只包含可比较字段，避免直接把 Value 中的 any 引用放入 map key。
type tableKey struct {
	// kind 保存 key 的 Lua 类型。
	kind ValueKind
	// boolValue 保存 boolean key。
	boolValue bool
	// integerValue 保存 integer key 或可整数化 number key。
	integerValue int64
	// numberValue 保存非整数 float number key。
	numberValue float64
	// stringValue 保存 string key。
	stringValue string
	// referenceValue 保存 table、closure、userdata、thread 等引用 key 的 identity。
	referenceValue uintptr
}

// tableEntry 表示一次 table raw 迭代快照中的键值对。
//
// 该结构只服务 RawNext 的稳定遍历，不暴露给 VM 外部。
type tableEntry struct {
	// key 保存当前迭代项的 Lua key。
	key Value
	// value 保存当前迭代项的 Lua value。
	value Value
}

// NewTable 创建一个空 Lua table。
//
// 返回的 table 不设置元表，数组区与 hash 区都按写入需求延迟分配。
func NewTable() *Table {
	// 空表延迟分配数组区和 hash 区，纯数组表可避免无用 map 分配。
	return &Table{}
}

// ensureHashStorage 确保 hash 区 map 已初始化。
//
// hashValues 保存实际 table 值；hashKeys 仅在无法从 tableKey 无损还原原始 Lua key 时按需创建。
func (table *Table) ensureHashStorage() {
	if table.hashValues != nil {
		// hash 区已经初始化时无需重复分配。
		return
	}

	// 首次 hash 写入时再创建 map，避免纯数组 table 和空 table 承担 hash 分配成本。
	table.hashValues = make(map[tableKey]Value)
}

// ensureHashKeyStorage 确保 hash 区原始 key 镜像 map 已初始化。
//
// 该 map 只服务 table/function/userdata/thread 等引用 key 以及需要保留原始 Lua key 形态的路径；
// string、boolean、number、integer 这类基础 key 可由 tableKey 直接还原，不需要提前分配。
func (table *Table) ensureHashKeyStorage() {
	if table.hashKeys != nil {
		// 原始 key 镜像已经初始化时无需重复分配。
		return
	}

	// 首次需要保留原始 Lua key 时再创建 map，避免纯 string-key table 承担额外分配。
	table.hashKeys = make(map[tableKey]Value)
}

// RawSet 使用任意 Lua key 写入 Lua 值。
//
// nil key 和 NaN key 会返回错误；value 为 nil 时删除 key。
func (table *Table) RawSet(key Value, value Value) error {
	// nil key 在 Lua 中非法，必须返回错误。
	if key.IsNil() {
		return ErrNilTableKey
	}
	if key.Kind == KindNumber && math.IsNaN(key.Number) {
		// NaN key 在 Lua 中非法，必须返回错误。
		return ErrNaNTableKey
	}

	if integerKey, ok := key.ToInteger(); ok && integerKey > 0 {
		// 正整数 key 进入数组区，符合 Lua table 的数组区基础语义。
		table.RawSetInteger(integerKey, value)
		return nil
	}

	hashKey, err := makeTableKey(key)
	if err != nil {
		// key 无法编码到当前 hash 区时，返回明确错误。
		return err
	}
	if value.IsNil() {
		// nil 值表示删除 hash 区 key。
		delete(table.hashValues, hashKey)
		delete(table.hashKeys, hashKey)
		table.noteMutation()
		return nil
	}

	// 非 nil 值直接写入 hash 区。
	table.ensureHashStorage()
	table.ensureHashKeyStorage()
	table.hashValues[hashKey] = value
	table.hashKeys[hashKey] = key
	table.noteMutation()
	return nil
}

// RawGet 使用任意 Lua key 读取 Lua 值。
//
// nil key 和 NaN key 会返回错误；key 不存在时返回 Lua nil。
func (table *Table) RawGet(key Value) (Value, error) {
	// nil key 在 Lua get 路径中返回 nil，不触发查找。
	if key.IsNil() {
		return NilValue(), nil
	}
	if key.Kind == KindNumber && math.IsNaN(key.Number) {
		// NaN key 不可能命中 table，按 Lua get 语义返回 nil。
		return NilValue(), nil
	}

	if integerKey, ok := key.ToInteger(); ok && integerKey > 0 {
		// 正整数 key 先查数组区。
		return table.RawGetInteger(integerKey), nil
	}

	hashKey, err := makeTableKey(key)
	if err != nil {
		// 当前阶段不支持的 key 类型返回错误，后续 userdata/thread 可扩展 identity key。
		return NilValue(), err
	}
	if value, ok := table.hashValues[hashKey]; ok {
		// 命中 hash 区时返回存储值。
		return value, nil
	}

	// 未命中时按 Lua 语义返回 nil。
	return NilValue(), nil
}

// RawSetString 使用 string key 写入 Lua 值。
//
// value 为 nil 时删除 key，模拟 Lua table 中赋 nil 删除字段的基础行为。
func (table *Table) RawSetString(key string, value Value) {
	// nil 值表示删除字段，不在 table 中保留 nil 负载。
	if value.IsNil() {
		delete(table.hashValues, tableKey{kind: KindString, stringValue: key})
		delete(table.hashKeys, tableKey{kind: KindString, stringValue: key})
		table.noteMutation()
		return
	}

	// 非 nil 值直接写入 hash 区。
	table.ensureHashStorage()
	table.hashValues[tableKey{kind: KindString, stringValue: key}] = value
	table.noteMutation()
}

// RawGetString 使用 string key 读取 Lua 值。
//
// key 不存在时返回 Lua nil。
func (table *Table) RawGetString(key string) Value {
	// 命中 hash 区 string key 时直接返回存储值。
	if value, ok := table.hashValues[tableKey{kind: KindString, stringValue: key}]; ok {
		return value
	}

	// 未命中时按 Lua 语义返回 nil。
	return NilValue()
}

// RawSetInteger 使用 integer key 写入 Lua 值。
//
// value 为 nil 时删除 key，模拟 Lua table 中赋 nil 删除字段的基础行为。
func (table *Table) RawSetInteger(key int64, value Value) {
	if key > 0 {
		// 正整数写入可能影响 Lua 长度边界，需要先维护或失效长度缓存。
		table.updateLengthCacheForIntegerSet(key, value)
	}
	if key > 0 && key <= maxTableArrayIndex {
		// 正整数 key 进入数组区，Lua key 从 1 开始。
		table.ensureArraySize(int(key))
		if value.IsNil() {
			// nil 值删除数组区元素。
			table.arrayValues[key-1] = NilValue()
			table.noteMutation()
			return
		}
		// 非 nil 值写入数组区对应槽位。
		table.arrayValues[key-1] = value
		table.noteMutation()
		return
	}

	// nil 值表示删除字段，不在 table 中保留 nil 负载。
	if value.IsNil() {
		delete(table.hashValues, tableKey{kind: KindInteger, integerValue: key})
		delete(table.hashKeys, tableKey{kind: KindInteger, integerValue: key})
		table.noteMutation()
		return
	}

	// 非正整数 key 或超大稀疏正整数 key 写入 hash 区。
	table.ensureHashStorage()
	table.ensureHashKeyStorage()
	table.hashValues[tableKey{kind: KindInteger, integerValue: key}] = value
	table.hashKeys[tableKey{kind: KindInteger, integerValue: key}] = IntegerValue(key)
	table.noteMutation()
}

// RawSetPositiveIntegerNonNil 使用正整数 key 写入非 nil Lua 值。
//
// key 必须大于 0，value 必须不是 nil；不满足时回退 RawSetInteger 保持语义。该入口服务
// VM 中数值 for 数组写入热路径，避免重复执行 nil/delete 分支。
func (table *Table) RawSetPositiveIntegerNonNil(key int64, value Value) {
	if key <= 0 || value.IsNil() {
		// 调用方条件不满足时回退完整整数写入语义。
		table.RawSetInteger(key, value)
		return
	}
	if table.lengthCacheValid {
		// 正整数非 nil 写入只需要维护追加、覆盖和稀疏失效三种状态。
		if key == table.lengthCache+1 {
			// 连续追加一个非 nil 元素，边界向后移动一位。
			table.lengthCache = key
		} else if key > table.lengthCache {
			// 跳跃写入可能形成新的可选边界，保守失效。
			table.invalidateLengthCache()
		}
	}
	if key <= maxTableArrayIndex {
		// 正整数 key 进入数组区，Lua key 从 1 开始。
		arrayIndex := int(key)
		if arrayIndex == len(table.arrayValues)+1 && len(table.arrayValues) < cap(table.arrayValues) {
			// 连续追加且已有预留容量时直接扩展一格，避免热循环每次进入 ensureArraySize。
			table.arrayValues = append(table.arrayValues, value)
			table.noteMutation()
			return
		}
		table.ensureArraySize(arrayIndex)
		table.arrayValues[key-1] = value
		table.noteMutation()
		return
	}

	// 超大稀疏正整数 key 写入 hash 区。
	table.ensureHashStorage()
	table.ensureHashKeyStorage()
	table.hashValues[tableKey{kind: KindInteger, integerValue: key}] = value
	table.hashKeys[tableKey{kind: KindInteger, integerValue: key}] = IntegerValue(key)
	table.noteMutation()
}

// RawGetInteger 使用 integer key 读取 Lua 值。
//
// key 不存在时返回 Lua nil。
func (table *Table) RawGetInteger(key int64) Value {
	if key > 0 && key <= int64(len(table.arrayValues)) {
		// 正整数 key 优先读取数组区。
		value := table.arrayValues[key-1]
		if !value.IsNil() {
			// 数组槽位非 nil 时直接返回。
			return value
		}
	}

	// 命中 hash 区 integer key 时直接返回存储值。
	if value, ok := table.hashValues[tableKey{kind: KindInteger, integerValue: key}]; ok {
		return value
	}

	// 未命中时按 Lua 语义返回 nil。
	return NilValue()
}

// Get 使用 Lua 普通 table 读取语义读取 key。
//
// 该方法先执行 raw get；raw 未命中时沿元表中的 table 或 Go function 形式 `__index`
// 链继续查找。Go function 形式会直接以 `(调用者, key)` 调用，并返回第一返回值。
func (table *Table) Get(key Value) (Value, error) {
	// 无 Lua runner 时只支持原有 Go closure 元方法路径。
	return table.GetWithRunner(key, nil)
}

// GetWithRunner 使用 Lua 普通 table 读取语义读取 key。
//
// runner 可为 nil；非 nil 时 `__index` 为 Lua closure 也可执行，并会以 `__index` 作为
// debug 元方法名。该入口服务 VM 指令路径，保持不带 State 的 Table.Get 向后兼容。
func (table *Table) GetWithRunner(key Value, runner LuaMetamethodRunner) (Value, error) {
	// 普通读取必须先走 raw get，以保证已有字段不会触发元方法。
	value, err := table.RawGet(key)
	if err != nil {
		// raw get 的 key 编码错误需要直接返回，避免错误地进入元方法链。
		return NilValue(), err
	}
	if !value.IsNil() {
		// raw 命中时直接返回，符合 Lua 5.3 luaV_gettable 的优先级。
		return value, nil
	}

	// raw 未命中时继续尝试 `__index` 链。
	return table.getThroughIndexChainWithRunner(key, runner)
}

// Set 使用 Lua 普通 table 写入语义写入 key。
//
// 该方法先检查 raw 是否已有字段；已有字段直接 raw set。raw 未命中时沿元表中的
// table 或 Go function 形式 `__newindex` 链继续写入。Go function 形式会以
// `(调用者, key, value)` 调用，并忽略返回值。
func (table *Table) Set(key Value, value Value) error {
	// 旧入口不带 Lua runner，只支持 Go closure 形式 `__newindex`。
	return table.SetWithRunner(key, value, nil)
}

// SetWithRunner 使用 Lua 普通 table 写入语义写入 key。
//
// runner 可为 nil；非 nil 时 `__newindex` 为 Lua closure 也可执行，并会以 `__newindex` 作为
// debug 元方法名。已有 raw 字段仍直接写入，不触发元方法。
func (table *Table) SetWithRunner(key Value, value Value, runner LuaMetamethodRunner) error {
	// 普通写入必须先检查 raw 字段，已有字段不触发 `__newindex`。
	existingValue, err := table.RawGet(key)
	if err != nil {
		// key 非法或暂不支持时直接返回 raw get 错误，避免进入元方法链。
		return err
	}
	if !existingValue.IsNil() {
		// raw 已有字段时按普通 table 写入覆盖或删除该字段。
		return table.RawSet(key, value)
	}

	// raw 未命中时继续尝试 `__newindex` 链。
	return table.setThroughNewIndexChainWithRunner(key, value, runner)
}

// RawNext 返回当前 key 之后的下一个 raw 迭代项。
//
// key 为 nil 时返回第一个非 nil 项；返回 ok=false 表示迭代结束。当前实现按数组区正整数
// 升序、hash 区稳定排序输出，顺序不承诺等同 Lua C 实现内部 hash 顺序。
func (table *Table) RawNext(key Value) (Value, Value, bool, error) {
	// 先构建稳定迭代快照，避免 Go map 遍历顺序影响测试与 VM 行为复现。
	entries := table.rawIterationEntries()
	if len(entries) == 0 {
		// 空 table 没有可迭代项，直接结束。
		return NilValue(), NilValue(), false, nil
	}
	if key.IsNil() {
		// nil 起始 key 表示从第一个迭代项开始。
		return entries[0].key, entries[0].value, true, nil
	}

	for index, entry := range entries {
		if entry.key.RawEqual(key) {
			// 找到当前 key 后，返回它后面的第一个元素。
			return table.rawNextFromEntries(entries, index+1)
		}
	}
	if table.staleIterationCacheValid {
		// Lua 允许在遍历时删除当前 key；删除会重建当前快照，但继续 key 仍可在旧快照中定位后继。
		for index, entry := range table.staleIterationCache {
			if entry.key.RawEqual(key) {
				// 从旧快照后继继续，并跳过已经被删除或改为 nil 的项。
				return table.rawNextFromEntries(table.staleIterationCache, index+1)
			}
		}
	}

	// key 不属于当前迭代快照时，按 Lua next 错误边界返回明确错误。
	return NilValue(), NilValue(), false, ErrInvalidTableIterationKey
}

// rawNextFromEntries 从指定快照位置开始返回仍存在的下一项。
//
// entries 必须来自当前或最近一次 raw next 快照；startIndex 是候选后继起点。若候选项已被删除
// 或写成 nil，会继续向后查找，保证删除当前 key 的 pairs 循环可以自然前进。
func (table *Table) rawNextFromEntries(entries []tableEntry, startIndex int) (Value, Value, bool, error) {
	for nextIndex := startIndex; nextIndex < len(entries); nextIndex++ {
		// 候选 key 必须仍然存在于当前 table；旧快照中的已删除项需要跳过。
		currentValue, err := table.RawGet(entries[nextIndex].key)
		if err != nil {
			// 快照 key 理论上都可 raw get；若失败，保留底层错误便于暴露损坏状态。
			return NilValue(), NilValue(), false, err
		}
		if currentValue.IsNil() {
			// 该候选项已被删除，继续寻找下一个仍有效项。
			continue
		}
		// 返回当前 table 中的最新 value，兼容遍历期间覆盖后续 key 的值。
		return entries[nextIndex].key, currentValue, true, nil
	}

	// 后续没有仍存在的项时迭代结束。
	return NilValue(), NilValue(), false, nil
}

// RawPairsNext 返回 pairs 辅助迭代的下一个 raw 项。
//
// key 为 nil 时返回第一个非 nil 项；返回 ok=false 表示迭代结束。当前方法复用 RawNext，
// 不触发 `__pairs` 元方法，标准库 `pairs` 的元方法兼容策略会在 stdlib 阶段补齐。
func (table *Table) RawPairsNext(key Value) (Value, Value, bool, error) {
	// pairs 的基础迭代语义与 next 一致，当前阶段直接复用 RawNext。
	return table.RawNext(key)
}

// RawIPairsNext 返回 ipairs 辅助迭代的下一个正整数项。
//
// index 表示上一次返回的正整数索引，通常从 0 开始；返回 ok=false 表示 index+1 对应值
// 为 nil，迭代结束。该方法只使用 raw integer 读取，不触发 `__index` 或 `__ipairs`。
func (table *Table) RawIPairsNext(index int64) (int64, Value, bool) {
	if index >= maxTableIntegerIndex {
		// 索引到达 int64 上限时无法安全递增，直接结束迭代。
		return 0, NilValue(), false
	}

	nextIndex := index + 1
	value := table.RawGetInteger(nextIndex)
	if value.IsNil() {
		// ipairs 在遇到第一个 nil 正整数槽位时结束。
		return 0, NilValue(), false
	}

	// 返回下一个正整数索引和值。
	return nextIndex, value, true
}

// SweepWeakEntries 按当前 table 元表中的 `__mode` 清理基础弱 key/value 项。
//
// 当前方法只处理 table/function/userdata/thread 这类引用值；字符串按当前运行时值语义保留。
// 返回值表示是否删除过条目，供 GC 兼容层测试和诊断使用。
func (table *Table) SweepWeakEntries(strongRefs map[tableKey]bool) bool {
	// 读取弱模式；非弱表没有清理动作。
	weakKeys, weakValues := table.weakMode()
	if !weakKeys && !weakValues {
		// 没有 k/v 标记时直接返回，避免误删普通 table。
		return false
	}

	removed := false
	if weakValues {
		// 数组区只有 value 可能为弱引用；正整数 key 本身不可收集。
		for index, value := range table.arrayValues {
			if isWeakCollectableValue(value) && !isStrongReference(value, strongRefs) {
				// 弱 value 在完整 GC 后不可由当前 table 保留，写回 nil 删除该数组项。
				table.arrayValues[index] = NilValue()
				removed = true
			}
		}
	}

	for hashKey, value := range table.hashValues {
		luaKey, ok := table.hashKeys[hashKey]
		if !ok {
			// 兼容直接写 hashValues 的旧测试路径，基础 key 可由 tableKey 还原。
			luaKey = hashKey.toValue()
		}
		if shouldRemoveWeakEntry(luaKey, value, weakKeys, weakValues, strongRefs) {
			// 弱 key/value 任一侧满足清理条件时，删除 hash 值和原始 key 缓存。
			delete(table.hashValues, hashKey)
			delete(table.hashKeys, hashKey)
			removed = true
		}
	}
	if removed {
		// 弱表 sweep 直接修改底层存储，必须失效长度和迭代缓存。
		table.invalidateLengthCache()
		table.noteMutation()
	}

	// 返回是否发生过删除，便于上层判断 sweep 是否产生效果。
	return removed
}

// SweepWeakValueEntries 清理 weak value-only 表中不可达的弱 value。
//
// 调用方必须只在 `__mode` 包含 v 且不包含 k 时调用；该方法不会移除 weak key，服务
// finalizer 前的阶段性清理顺序。
func (table *Table) SweepWeakValueEntries(strongRefs map[tableKey]bool) bool {
	if table == nil {
		// nil table 没有可清理内容。
		return false
	}
	weakKeys, weakValues := table.weakMode()
	if weakKeys || !weakValues {
		// 只处理 weak value-only 表，其他弱模式留给完整 sweep。
		return false
	}

	removed := false
	for index, value := range table.arrayValues {
		if isWeakCollectableValue(value) && (value.Kind == KindLuaClosure || !isStrongReference(value, strongRefs)) {
			// 数组区弱 value 不强可达时可提前删除；Lua closure 临时寄存器在该阶段不保活弱值。
			table.arrayValues[index] = NilValue()
			removed = true
		}
	}
	for hashKey, value := range table.hashValues {
		luaKey, ok := table.hashKeys[hashKey]
		if !ok {
			// 兼容直接写 hashValues 的旧测试路径，基础 key 可由 tableKey 还原。
			luaKey = hashKey.toValue()
		}
		if isWeakCollectableValue(value) && (value.Kind == KindLuaClosure || !isStrongReference(value, strongRefs)) && !luaKey.RawEqual(value) {
			// 弱 value 不强可达且不是 key==value 自保项时可以提前删除；Lua closure 临时寄存器不保活弱值。
			delete(table.hashValues, hashKey)
			delete(table.hashKeys, hashKey)
			removed = true
		}
	}
	if removed {
		// 弱 value 清理直接修改底层存储，必须让 RawNext 快照缓存失效。
		table.invalidateLengthCache()
		table.noteMutation()
	}
	return removed
}

// SweepWeakKVEntriesBeforeFinalizers 清理 weak key/value 表中 finalizer 前应消失的弱项。
//
// strongRefs 保留用于与弱表清理入口保持一致；weak key/value 同时启用时，Lua 5.3 在运行
// table `__gc` 前会先清理普通弱 key 与弱 value。preserveWeakKeys 中的 key 表示待终结对象
// 自身，当前兼容层保留这些 key 供后续扩展 userdata/table finalizer 顺序。
func (table *Table) SweepWeakKVEntriesBeforeFinalizers(strongRefs map[tableKey]bool, preserveWeakKeys map[tableKey]bool) bool {
	if table == nil {
		// nil table 没有可清理内容。
		return false
	}
	weakKeys, weakValues := table.weakMode()
	if !weakKeys || !weakValues {
		// 只处理 key/value 同时为弱引用的表，其他模式沿用既有路径。
		return false
	}

	removed := false
	for hashKey, value := range table.hashValues {
		luaKey, ok := table.hashKeys[hashKey]
		if !ok {
			// 兼容直接写 hashValues 的旧测试路径，基础 key 可由 tableKey 还原。
			luaKey = hashKey.toValue()
		}
		keyShouldRemove := isWeakCollectableValue(luaKey) && !preserveWeakKeys[hashKey]
		valueShouldRemove := isWeakCollectableValue(value) && !luaKey.RawEqual(value)
		if keyShouldRemove || valueShouldRemove {
			// weak kv 表在 finalizer 前移除普通可收集 key/value，避免 stale 寄存器保活临时对象。
			delete(table.hashValues, hashKey)
			delete(table.hashKeys, hashKey)
			removed = true
		}
	}
	for index, value := range table.arrayValues {
		if isWeakCollectableValue(value) {
			// 数组区没有弱 key，只需按 weak value 规则清理。
			table.arrayValues[index] = NilValue()
			removed = true
		}
	}
	if removed {
		// weak kv 清理后 table 结构已变化，后续迭代必须重建快照。
		table.invalidateLengthCache()
		table.noteMutation()
	}
	_ = strongRefs
	return removed
}

// weakMode 读取 table 元表的 `__mode` 字段。
//
// 返回值分别表示 key 弱引用和值弱引用；只有 string 型 `__mode` 会生效。
func (table *Table) weakMode() (bool, bool) {
	if table == nil || table.metatable == nil {
		// nil table 或无元表时不是弱表。
		return false, false
	}
	modeValue := table.metatable.RawGetString(tableWeakModeKey)
	if modeValue.Kind != KindString {
		// Lua 5.3 只接受 string 型 __mode；其他类型视为无弱模式。
		return false, false
	}

	weakKeys := false
	weakValues := false
	for _, modeRune := range modeValue.String {
		switch modeRune {
		case 'k':
			// 字符 k 表示弱 key。
			weakKeys = true
		case 'v':
			// 字符 v 表示弱 value。
			weakValues = true
		default:
			// 其他字符不影响弱表模式。
			continue
		}
	}
	return weakKeys, weakValues
}

// shouldRemoveWeakEntry 判断弱表中的一个 hash 项是否应在完整 GC 后删除。
//
// weakKeys/weakValues 来自 `__mode`；key==value 的自引用项在基础兼容层中保留，用于模拟
// Lua 弱表中强侧仍可保留对象的常见场景。
func shouldRemoveWeakEntry(key Value, value Value, weakKeys bool, weakValues bool, strongRefs map[tableKey]bool) bool {
	if weakKeys && weakValues && key.RawEqual(value) && isWeakCollectableValue(key) {
		// key/value 同时为弱引用时，自引用不能保活自身，必须删除。
		return true
	}
	if weakKeys && isWeakCollectableValue(key) && !isStrongReference(key, strongRefs) {
		// 弱 key 且 key 没有被同项 value 保留时删除该项。
		return true
	}
	if weakValues && isWeakCollectableValue(value) && !isStrongReference(value, strongRefs) && !(weakValues && !weakKeys && key.RawEqual(value)) {
		// 弱 value 且 value 没有被同项 key 保留时删除该项。
		return true
	}

	// 其他组合当前保留。
	return false
}

// isStrongReference 判断引用值是否出现在本轮 GC 强根集合中。
//
// strongRefs 为空时表示没有额外强根；非引用或无法编码的值都视为非强引用。
func isStrongReference(value Value, strongRefs map[tableKey]bool) bool {
	if len(strongRefs) == 0 || !isWeakCollectableValue(value) {
		// 无强根集合或非弱表可收集值时，不能作为保留依据。
		return false
	}
	key, err := makeTableKey(value)
	if err != nil {
		// 无法编码的引用值不在强根集合中。
		return false
	}

	// 命中强根集合表示本轮 GC 不能清理该对象所在弱表项。
	return strongRefs[key]
}

// isWeakCollectableValue 判断值是否按当前兼容层视为弱表可收集对象。
//
// Lua 5.3 中 table/function/userdata/thread 属于可收集引用；字符串在本项目当前值系统中按值
// 保存，为匹配官方 weak table 样例暂不作为弱表清理目标。
func isWeakCollectableValue(value Value) bool {
	switch value.Kind {
	case KindTable, KindLuaClosure, KindGoClosure, KindUserdata, KindThread:
		// 引用对象可能只被弱表持有，完整 GC 可以清理对应项。
		return value.Ref != nil
	default:
		// nil、boolean、number、string 在当前兼容层不作为弱表可收集对象。
		return false
	}
}

// ArraySize 返回当前数组区槽位数量。
//
// 该方法用于测试与后续 resize 策略验证，不代表 Lua 长度运算结果。
func (table *Table) ArraySize() int {
	// 直接返回数组区底层长度。
	return len(table.arrayValues)
}

// HashSize 返回当前 hash 区非 nil key 数量。
//
// 该方法用于测试与后续 resize 策略验证，不代表 Lua table 长度。
func (table *Table) HashSize() int {
	// 直接返回 hash map 元素数量。
	return len(table.hashValues)
}

// Len 返回 Lua table 的基础长度边界。
//
// 当前实现不处理 `__len` 元方法，只按 Lua 5.3 luaH_getn 的边界定义查找：
// t[i] 非 nil 且 t[i+1] 为 nil，若 t[1] 为 nil 则长度为 0。
func (table *Table) Len() int64 {
	if table.lengthCacheValid {
		// 连续数组追加场景可直接复用缓存，避免每次 #t 都做边界搜索。
		return table.lengthCache
	}

	// 数组区为空时，直接从 0 开始在 hash 区查找边界。
	if len(table.arrayValues) == 0 {
		length := table.unboundSearch(0)
		table.setLengthCache(length)
		return length
	}

	lastArrayIndex := len(table.arrayValues)
	if table.arrayValues[lastArrayIndex-1].IsNil() {
		// 数组区末尾为 nil，说明边界在数组区内，使用二分查找。
		lowIndex := 0
		highIndex := lastArrayIndex
		for highIndex-lowIndex > 1 {
			middleIndex := (lowIndex + highIndex) / 2
			if table.arrayValues[middleIndex-1].IsNil() {
				// 中点为 nil 时，边界在左侧。
				highIndex = middleIndex
			} else {
				// 中点非 nil 时，边界在右侧。
				lowIndex = middleIndex
			}
		}
		length := int64(lowIndex)
		table.setLengthCache(length)
		return length
	}

	// 数组区末尾非 nil，边界可能在 hash 区。
	length := table.unboundSearch(int64(lastArrayIndex))
	table.setLengthCache(length)
	return length
}

// setLengthCache 写入 table 长度缓存。
//
// length 必须来自 Len 的边界搜索或连续追加维护；该缓存只优化 raw 长度查询，不改变
// Lua 5.3 对稀疏表长度边界可任选的语义。
func (table *Table) setLengthCache(length int64) {
	// 保存边界并标记可用，供下一次 Len 直接读取。
	table.lengthCache = length
	table.lengthCacheValid = true
}

// invalidateLengthCache 失效 table 长度缓存。
//
// 当正整数 key 写入无法通过连续追加规则安全维护时，下一次 Len 会回退到原有边界搜索。
func (table *Table) invalidateLengthCache() {
	// 清除可用标记即可，旧 lengthCache 值不会被读取。
	table.lengthCacheValid = false
}

// MutationVersion 返回 table 最近一次 raw 写入后的版本号。
//
// 返回值只用于缓存失效判断；调用方不得把具体数值暴露为 Lua 语义。
func (table *Table) MutationVersion() uint64 {
	if table == nil {
		// nil table 没有可观察版本，返回 0 作为稳定占位。
		return 0
	}

	// 返回当前 raw 写入版本。
	return table.mutationVersion
}

// noteMutation 标记 table 发生一次 raw 写入变化。
//
// 该版本只用于上层缓存失效；即使写入同值也递增，避免为了比较复杂 Lua 值引入额外成本。
func (table *Table) noteMutation() {
	// raw set/delete 完成后递增版本，缓存读取方可用版本不一致触发重建。
	if table.iterationCacheValid {
		// 保留失效前快照，允许 next(table, deletedCurrentKey) 按旧位置找到后继。
		table.staleIterationCache = append(table.staleIterationCache[:0], table.iterationCache...)
		table.staleIterationCacheValid = true
	}
	table.mutationVersion++
	table.iterationCacheValid = false
}

// updateLengthCacheForIntegerSet 根据正整数 key 写入维护长度缓存。
//
// 连续追加 `t[#t + 1] = value` 是官方测试热路径，可以 O(1) 推进缓存；其他插入、删除或
// 稀疏写入保守失效，继续由 Len 的边界搜索保证行为。
func (table *Table) updateLengthCacheForIntegerSet(key int64, value Value) {
	if !table.lengthCacheValid {
		// 缓存尚不可用时无法增量维护，等待下一次 Len 重建。
		return
	}
	if value.IsNil() {
		if key <= table.lengthCache {
			// 删除当前边界内元素可能改变 #t，必须失效。
			table.invalidateLengthCache()
		}
		return
	}
	if key == table.lengthCache+1 {
		// 连续追加一个非 nil 元素，边界向后移动一位。
		table.lengthCache = key
		return
	}
	if key <= table.lengthCache {
		// 边界内覆盖非 nil 不改变当前缓存。
		return
	}

	// 跳跃写入可能形成新的可选边界，保守失效。
	table.invalidateLengthCache()
}

// SetMetatable 设置当前 table 的元表。
//
// 入参 metatable 为 nil 时表示移除元表；该方法只写入 raw 元表引用，不处理
// `__metatable` 保护字段，保护语义会在公开 API 层补齐以对齐 Lua 5.3。
func (table *Table) SetMetatable(metatable *Table) {
	// 直接替换元表引用，调用方负责决定是否允许覆盖受保护元表。
	table.metatable = metatable
}

// GetMetatable 返回当前 table 的 raw 元表。
//
// 返回 nil 表示当前 table 没有元表；该方法不读取 `__metatable` 保护字段，供 VM 内部
// 元方法查找使用。
func (table *Table) GetMetatable() *Table {
	// 直接返回内部 raw 元表，后续公开 API 会包装受保护元表语义。
	return table.metatable
}

// MetatableValue 返回当前 table 对外可见的元表值。
//
// 若当前 table 没有元表则返回 nil；若元表包含 `__metatable` 字段，则返回该字段值；
// 否则返回 table 元表引用值，以对齐 Lua 5.3 `getmetatable` 的保护语义。
func (table *Table) MetatableValue() Value {
	// 没有元表时，对外读取结果为 nil。
	if table.metatable == nil {
		return NilValue()
	}

	protectedValue := table.metatable.RawGetString(metatableProtectionKey)
	if !protectedValue.IsNil() {
		// `__metatable` 非 nil 时，对外隐藏真实元表并返回保护值。
		return protectedValue
	}

	// 未受保护时，对外返回真实元表引用。
	return ReferenceValue(KindTable, table.metatable)
}

// SetMetatableChecked 在遵守 `__metatable` 保护语义的前提下设置元表。
//
// 入参 metatable 为 nil 时表示移除元表；如果当前元表存在非 nil `__metatable` 字段，
// 返回 ErrProtectedMetatable 且不修改当前元表。
func (table *Table) SetMetatableChecked(metatable *Table) error {
	// 当前元表未设置时，可以直接写入新元表。
	if table.metatable == nil {
		table.metatable = metatable
		return nil
	}

	protectedValue := table.metatable.RawGetString(metatableProtectionKey)
	if !protectedValue.IsNil() {
		// 受保护元表不能通过公开 setmetatable 语义替换或移除。
		return ErrProtectedMetatable
	}

	// 当前元表未受保护，可以替换或移除。
	table.metatable = metatable
	return nil
}

// getThroughIndexChain 沿 `__index` 元方法链查找 key。
//
// 入参 key 已经在起始 table 上 raw 未命中；返回值为链上首次可返回的值，未命中返回 nil。
// 遇到函数形式 `__index`，会以 `(调用者, key)` 调用 Go 函数并返回第一返回值。
// 遇到既不是 table 也不是 Go function 的元方法类型时返回 ErrUnsupportedIndexMetamethod。
func (table *Table) getThroughIndexChain(key Value) (Value, error) {
	// 旧入口不带 Lua runner，只支持 Go closure 形式 `__index`。
	return table.getThroughIndexChainWithRunner(key, nil)
}

// getThroughIndexChainWithRunner 沿 `__index` 元方法链查找 key。
//
// runner 可为 nil；非 nil 时函数形式 `__index` 可为 Lua closure，并按 metamethod 调试语义执行。
func (table *Table) getThroughIndexChainWithRunner(key Value, runner LuaMetamethodRunner) (Value, error) {
	// 从当前 table 开始，每轮读取该 table 元表中的 `__index` 字段。
	currentTable := table
	for depth := 0; depth < maxTableIndexChainDepth; depth++ {
		if currentTable.metatable == nil {
			// 没有元表时，普通读取最终结果为 nil。
			return NilValue(), nil
		}

		indexValue := currentTable.metatable.RawGetString(tableIndexMetamethodKey)
		if indexValue.IsNil() {
			// 元表没有 `__index` 字段时，普通读取最终结果为 nil。
			return NilValue(), nil
		}
		if indexValue.Kind != KindTable {
			// table 以外先尝试按函数元方法语义执行。
			// currentTable 是发生当前元方法查找的 table（含链式上可能变化），函数接收它作为第一参数。
			result, callErr := callMetamethodValue(runner, indexValue, tableIndexMetamethodKey, ReferenceValue(KindTable, currentTable), key)
			if callErr != nil {
				// 元方法类型不支持或函数执行失败时返回错误，禁止静默降级。
				return NilValue(), callErr
			}

			// 函数形式 `__index` 直接返回第一值，不继续追踪元表链。
			return result, nil
		}

		indexTable, ok := indexValue.Ref.(*Table)
		if !ok || indexTable == nil {
			// table 类型引用负载不合法时，按内部不支持元方法错误返回。
			return NilValue(), ErrUnsupportedIndexMetamethod
		}

		value, err := indexTable.RawGet(key)
		if err != nil {
			// 链上 raw get 出错时直接返回，避免继续跟随错误的元方法链。
			return NilValue(), err
		}
		if !value.IsNil() {
			// 链上 raw 命中时返回该值。
			return value, nil
		}

		// 当前 `__index` table 未命中，下一轮检查它自己的元表。
		currentTable = indexTable
	}

	// 超出最大深度表示元方法链可能循环，返回明确错误避免死循环。
	return NilValue(), ErrMetamethodChainTooLong
}

// setThroughNewIndexChain 沿 `__newindex` 元方法链写入 key。
//
// 入参 key 已经在起始 table 上 raw 未命中；该方法会把写入落到第一个没有 `__newindex`
// 的 table，或落到链上已有 raw 字段的 table。
func (table *Table) setThroughNewIndexChain(key Value, value Value) error {
	// 旧入口不带 Lua runner，只支持 Go closure 形式 `__newindex`。
	return table.setThroughNewIndexChainWithRunner(key, value, nil)
}

// setThroughNewIndexChainWithRunner 沿 `__newindex` 元方法链写入 key。
//
// runner 可为 nil；非 nil 时函数形式 `__newindex` 可为 Lua closure，并按 metamethod 调试语义执行。
func (table *Table) setThroughNewIndexChainWithRunner(key Value, value Value, runner LuaMetamethodRunner) error {
	// 从当前 table 开始，每轮读取该 table 元表中的 `__newindex` 字段。
	currentTable := table
	for depth := 0; depth < maxTableIndexChainDepth; depth++ {
		if currentTable.metatable == nil {
			// 没有元表时，写入落在当前 table。
			return currentTable.RawSet(key, value)
		}

		newIndexValue := currentTable.metatable.RawGetString(tableNewIndexMetamethodKey)
		if newIndexValue.IsNil() {
			// 元表没有 `__newindex` 字段时，写入落在当前 table。
			return currentTable.RawSet(key, value)
		}
		if newIndexValue.Kind != KindTable {
			// table 以外先尝试按函数元方法语义执行。
			// 当前链上的 table 作为 `__newindex` 调用的接收者参数。
			_, callErr := callMetamethodValue(runner, newIndexValue, tableNewIndexMetamethodKey, ReferenceValue(KindTable, currentTable), key, value)
			return callErr
		}

		newIndexTable, ok := newIndexValue.Ref.(*Table)
		if !ok || newIndexTable == nil {
			// table 类型引用负载不合法时，按内部不支持元方法错误返回。
			return ErrUnsupportedNewIndexMetamethod
		}

		existingValue, err := newIndexTable.RawGet(key)
		if err != nil {
			// 链上 raw get 出错时直接返回，避免继续跟随错误的元方法链。
			return err
		}
		if !existingValue.IsNil() {
			// 链上 table 已有 raw 字段时，普通写入覆盖该字段。
			return newIndexTable.RawSet(key, value)
		}

		// 当前 `__newindex` table 未命中，下一轮检查它自己的元表。
		currentTable = newIndexTable
	}

	// 超出最大深度表示元方法链可能循环，返回明确错误避免死循环。
	return ErrMetamethodChainTooLong
}

// rawIterationEntries 构建 table 当前 raw 内容的稳定迭代快照。
//
// 数组区按正整数 key 升序输出，hash 区按类型和值排序输出；nil 值会被跳过。
func (table *Table) rawIterationEntries() []tableEntry {
	if table.iterationCacheValid && table.iterationCacheVersion == table.mutationVersion {
		// 当前 table 未发生 raw 写入时复用上一轮快照，避免 pairs/next 每步重复排序 hash 区。
		return table.iterationCache
	}
	// 预估容量仅用于减少分配，不影响迭代语义。
	entries := make([]tableEntry, 0, len(table.arrayValues)+len(table.hashValues))
	for index, value := range table.arrayValues {
		if value.IsNil() {
			// 数组区 nil 槽位不参与 next 迭代。
			continue
		}
		// Lua 数组区 key 从 1 开始，对应 Go slice index+1。
		entries = append(entries, tableEntry{
			key:   IntegerValue(int64(index + 1)),
			value: value,
		})
	}

	hashKeys := make([]tableKey, 0, len(table.hashValues))
	for key, value := range table.hashValues {
		if value.IsNil() {
			// hash 区 nil 值不参与 next 迭代，兼容直接构造 map 的测试路径。
			continue
		}
		// 收集 hash key 后统一排序，避免 Go map 随机顺序。
		hashKeys = append(hashKeys, key)
	}
	sort.Slice(hashKeys, func(leftIndex int, rightIndex int) bool {
		// 使用稳定的类型和值排序保证迭代快照可复现。
		return tableKeyLess(hashKeys[leftIndex], hashKeys[rightIndex])
	})
	for _, key := range hashKeys {
		luaKey, ok := table.hashKeys[key]
		if !ok {
			// 兼容测试或旧路径直接写 hashValues 的场景，基础 key 可从 tableKey 还原。
			luaKey = key.toValue()
		}
		// 排序后的 hash key 按顺序转换回 Lua Value。
		entries = append(entries, tableEntry{
			key:   luaKey,
			value: table.hashValues[key],
		})
	}

	// 缓存数组区和 hash 区合并后的稳定快照，供同一版本后续 next 继续迭代复用。
	table.iterationCache = entries
	table.iterationCacheVersion = table.mutationVersion
	table.iterationCacheValid = true

	// 返回数组区和 hash 区合并后的稳定快照。
	return entries
}

// toValue 把 hash 区 tableKey 还原为 Lua Value。
//
// 该方法只处理 makeTableKey 支持写入 hash 区的基础 key 类型。
func (key tableKey) toValue() Value {
	// 根据 key 类型还原对应 Lua 值。
	switch key.kind {
	case KindBoolean:
		// boolean key 使用保存的布尔负载。
		return BooleanValue(key.boolValue)
	case KindInteger:
		// integer key 使用保存的整数负载。
		return IntegerValue(key.integerValue)
	case KindNumber:
		// number key 使用保存的浮点负载。
		return NumberValue(key.numberValue)
	case KindString:
		// string key 使用保存的字节字符串负载。
		return StringValue(key.stringValue)
	default:
		// 不支持的 key 类型正常不会出现，返回 nil 让调用方测试能暴露异常。
		return NilValue()
	}
}

// tableKeyLess 按稳定规则比较两个 hash 区 key。
//
// 排序只用于 Go 实现的可复现 raw next 顺序，不代表 Lua 5.3 对 hash 迭代顺序作出保证。
func tableKeyLess(left tableKey, right tableKey) bool {
	if left.kind != right.kind {
		// 不同类型按 ValueKind 枚举顺序排列。
		return left.kind < right.kind
	}

	// 同类型 key 按各自负载排序。
	switch left.kind {
	case KindBoolean:
		// false 排在 true 前。
		return !left.boolValue && right.boolValue
	case KindInteger:
		// integer 按数值升序排列。
		return left.integerValue < right.integerValue
	case KindNumber:
		// number 按浮点数值升序排列，NaN key 已在写入阶段禁止。
		return left.numberValue < right.numberValue
	case KindString:
		// string 按字节字典序排列。
		return left.stringValue < right.stringValue
	case KindTable, KindLuaClosure, KindGoClosure, KindUserdata, KindThread:
		// 引用 key 按 identity 排序，保证每次 RawNext 重建快照时顺序稳定。
		return left.referenceValue < right.referenceValue
	default:
		// 未知类型保持原有相对顺序。
		return false
	}
}

// ensureArraySize 确保数组区至少包含 size 个槽位。
//
// size 使用 Lua 正整数 key，对应 Go slice 长度。
func (table *Table) ensureArraySize(size int) {
	if len(table.arrayValues) >= size {
		// 当前数组区已经足够大，无需扩展。
		return
	}

	if cap(table.arrayValues) >= size {
		// 容量已预留时只扩展可见长度，避免连续数组写入反复分配。
		table.arrayValues = table.arrayValues[:size]
	} else {
		// 按几何增长预留容量，但 len 仍保持到目标 size，避免预留槽位暴露给 Lua 语义。
		grownCapacity := nextTableArrayCapacity(cap(table.arrayValues), size)
		grownValues := make([]Value, size, grownCapacity)
		copy(grownValues, table.arrayValues)
		table.arrayValues = grownValues
	}
	// Value 的零值就是 KindNil；Go 扩容和 make 已经保证新增槽位为 Lua nil。
}

// nextTableArrayCapacity 计算数组区下一次预留容量。
func nextTableArrayCapacity(currentCapacity int, requiredSize int) int {
	if currentCapacity <= 0 {
		// 空数组区从 8 个槽位开始，减少连续整数写入热路径的早期扩容与拷贝。
		currentCapacity = 8
	}
	for currentCapacity < requiredSize {
		if currentCapacity < tableArrayDoublingLimit {
			// 中小数组区按 2 倍增长，保持连续整数写入的摊还 O(1) 扩容成本。
			currentCapacity *= 2
			continue
		}
		// 大数组区改用 1.5 倍增长，减少超过实际长度的 Value 槽位和 GC 扫描压力。
		currentCapacity += currentCapacity / 2
	}
	return currentCapacity
}

// unboundSearch 从已知 presentIndex 开始向后查找 table 长度边界。
//
// presentIndex 为 0 或一个已知非 nil 的整数 key；该方法先指数扩张找到 nil 上界，
// 再二分搜索边界。
func (table *Table) unboundSearch(presentIndex int64) int64 {
	// 如果 key 1 为 nil，则长度边界为 0。
	if presentIndex == 0 && table.RawGetInteger(1).IsNil() {
		return 0
	}

	lowIndex := presentIndex
	highIndex := presentIndex + 1
	for !table.RawGetInteger(highIndex).IsNil() {
		// highIndex 仍非 nil，继续指数扩张上界。
		lowIndex = highIndex
		if highIndex > maxTableIntegerIndex/2 {
			// 接近 int64 上限时退化为线性查找，避免乘 2 溢出。
			return table.linearBoundarySearch(highIndex)
		}
		highIndex *= 2
	}

	for highIndex-lowIndex > 1 {
		middleIndex := (lowIndex + highIndex) / 2
		if table.RawGetInteger(middleIndex).IsNil() {
			// 中点为 nil 时，边界在左侧。
			highIndex = middleIndex
		} else {
			// 中点非 nil 时，边界在右侧。
			lowIndex = middleIndex
		}
	}

	// lowIndex 是最后一个非 nil 的整数 key。
	return lowIndex
}

// linearBoundarySearch 从 startIndex 开始线性查找第一个 nil 前的边界。
//
// 该路径只在指数扩张接近 int64 溢出时使用。
func (table *Table) linearBoundarySearch(startIndex int64) int64 {
	// 从 startIndex 开始找第一个 nil，返回其前一个索引。
	currentIndex := startIndex
	for !table.RawGetInteger(currentIndex).IsNil() {
		currentIndex++
	}
	return currentIndex - 1
}

// makeTableKey 把 Lua Value 转换为 hash 区 key。
//
// 当前阶段支持 boolean、integer、number、string 和引用对象 identity；引用对象按 kind 与引用地址共同区分。
func makeTableKey(value Value) (tableKey, error) {
	// 根据 Lua 值类型构造可比较的 hash key。
	switch value.Kind {
	case KindBoolean:
		// boolean key 使用布尔负载。
		return tableKey{kind: KindBoolean, boolValue: value.Bool}, nil
	case KindInteger:
		// integer key 使用整数负载。
		return tableKey{kind: KindInteger, integerValue: value.Integer}, nil
	case KindNumber:
		if integerValue, ok := value.ToInteger(); ok {
			// 可整数化 float key 与 integer key 共用整数 key，符合 Lua 5.3 get 语义。
			return tableKey{kind: KindInteger, integerValue: integerValue}, nil
		}
		// 非整数 float key 使用浮点负载。
		return tableKey{kind: KindNumber, numberValue: value.Number}, nil
	case KindString:
		// string key 使用字节字符串负载。
		return tableKey{kind: KindString, stringValue: value.String}, nil
	case KindTable, KindLuaClosure, KindGoClosure, KindUserdata, KindThread:
		// 引用类型按对象 identity 做 key，符合 Lua table/function/userdata/thread 可作为 key 的语义。
		referenceValue, ok := referenceIdentity(value.Ref)
		if !ok {
			// 无法取得稳定 identity 时拒绝写入，避免产生不可重复读取的 key。
			return tableKey{}, fmt.Errorf("unsupported table key reference kind %d", value.Kind)
		}
		return tableKey{kind: value.Kind, referenceValue: referenceValue}, nil
	default:
		// 当前阶段暂不支持复杂对象作为 hash key。
		return tableKey{}, fmt.Errorf("unsupported table key kind %d", value.Kind)
	}
}

// referenceIdentity 提取引用对象可比较 identity。
//
// ref 应为 Go 指针、函数、map、slice、chan 或 unsafe pointer；返回值只用于 table key hash。
func referenceIdentity(ref any) (uintptr, bool) {
	if ref == nil {
		// nil 引用没有稳定 Lua 对象 identity。
		return 0, false
	}
	value := reflect.ValueOf(ref)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		// 这些引用类型都能提供底层指针；函数使用代码指针满足 Go closure key 的阶段性 identity。
		pointerValue := value.Pointer()
		if pointerValue == 0 {
			// nil 引用类型不能作为有效对象 identity。
			return 0, false
		}
		return pointerValue, true
	default:
		// 其他类型没有可用的引用 identity。
		return 0, false
	}
}
