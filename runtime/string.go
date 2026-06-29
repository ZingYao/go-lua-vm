package runtime

import "math"

const (
	// stringHashLimit 对齐 Lua 5.3 LUAI_HASHLIMIT，控制长字符串 hash 采样步长。
	stringHashLimit = 5
	// maxShortStringLen 对齐 Lua 5.3 LUAI_MAXSHORTLEN，限制短字符串驻留的最大字节数。
	maxShortStringLen = 40
	// defaultStringHashSeed 是当前阶段的固定字符串 hash 种子。
	defaultStringHashSeed uint32 = 0
)

const (
	// StringStorageShortInterned 表示字符串按短字符串策略驻留。
	StringStorageShortInterned StringStorageKind = "short_interned"
	// StringStorageLongOwned 表示字符串按长字符串策略由 Value 自身持有。
	StringStorageLongOwned StringStorageKind = "long_owned"
)

// StringStorageKind 表示字符串存储策略。
//
// 短字符串进入驻留池复用；长字符串不进入驻留池，只在 Value 中保存 Go string 内容。
type StringStorageKind string

// StringStorage 描述一次字符串创建后的存储结果。
//
// Kind 表示短字符串或长字符串路径；Value 是可直接参与 Lua 运算的 string 值；Hash 保存
// Lua 5.3 字符串 hash，ByteLen 保存 Lua 字节长度，Interned 表示是否进入短字符串池。
type StringStorage struct {
	// Kind 表示字符串存储策略。
	Kind StringStorageKind
	// Value 保存 Lua string 值。
	Value Value
	// Hash 保存按 Lua 5.3 算法计算出的字符串 hash。
	Hash uint32
	// ByteLen 保存字符串字节长度。
	ByteLen int
	// Interned 表示当前字符串是否进入短字符串驻留池。
	Interned bool
}

// StringPool 保存短字符串驻留表。
//
// Lua 5.3 会驻留短字符串以复用相同字节序列；当前实现只对长度不超过
// maxShortStringLen 的字符串建立池，长字符串保持普通值并由 Go GC 管理。
type StringPool struct {
	// values 保存已驻留短字符串，key 是原始字节序列。
	values map[string]Value
}

// NewStringPool 创建空短字符串池。
//
// 返回的池只用于短字符串驻留；长字符串不会写入池。调用方通常应把该池挂到 State 或全局
// 运行时对象，后续接入 State 随机 hash seed 后可在此扩展。
func NewStringPool() *StringPool {
	// 初始化 map，避免首次 Intern 时出现 nil map 分支。
	return &StringPool{values: make(map[string]Value)}
}

// IsShortString 判断 text 是否属于短字符串。
//
// Lua 5.3 默认短字符串上限是 40 字节；这里按字节长度判断，不按 UTF-8 rune 数判断。
func IsShortString(text string) bool {
	// Go len 返回字节数，直接对齐 Lua string 字节长度语义。
	return len(text) <= maxShortStringLen
}

// Intern 返回 text 对应的 Lua string 值。
//
// text 长度不超过 maxShortStringLen 时会驻留到池中，相同字节序列重复 Intern 会复用同一个
// Value；长字符串不会进入池，直接返回普通 StringValue。
func (pool *StringPool) Intern(text string) Value {
	if !IsShortString(text) {
		// 长字符串不驻留，避免池无限增长；后续长字符串生命周期由 Go GC 管理。
		return StringValue(text)
	}
	if pool.values == nil {
		// 防御零值 StringPool，允许调用方直接声明后使用。
		pool.values = make(map[string]Value)
	}
	if value, ok := pool.values[text]; ok {
		// 已驻留短字符串直接复用，保持相同字节序列只有一个池条目。
		return value
	}

	// 首次出现的短字符串写入驻留表。
	value := StringValue(text)
	pool.values[text] = value
	return value
}

// Store 按 Lua 5.3 字符串策略保存 text。
//
// 短字符串会进入驻留池并返回 StringStorageShortInterned；长字符串不进入池，返回
// StringStorageLongOwned，并保留 hash 与字节长度供 Table、GC 和调试路径复用。
func (pool *StringPool) Store(text string) StringStorage {
	if IsShortString(text) {
		// 短字符串走驻留路径，相同字节序列复用池内 Value。
		value := pool.Intern(text)
		return StringStorage{
			Kind:     StringStorageShortInterned,
			Value:    value,
			Hash:     DefaultStringHash(text),
			ByteLen:  len(text),
			Interned: true,
		}
	}

	// 长字符串只构造普通 string 值，不写入短字符串池，生命周期交给 Go GC。
	return StringStorage{
		Kind:     StringStorageLongOwned,
		Value:    StringValue(text),
		Hash:     DefaultStringHash(text),
		ByteLen:  len(text),
		Interned: false,
	}
}

// Len 返回当前短字符串池中的驻留条目数量。
//
// 该方法用于测试和运行时观测；长字符串不会计入数量。
func (pool *StringPool) Len() int {
	if pool.values == nil {
		// 零值池还没有初始化 map，驻留数量为 0。
		return 0
	}

	// map 长度即已驻留短字符串数量。
	return len(pool.values)
}

// StringLen 返回 Lua string 的字节长度。
//
// Lua 5.3 的字符串长度按字节计算，不按 Unicode rune 或 UTF-8 字符数计算。
func StringLen(value Value) (int, bool) {
	// 只有 Lua string 值支持基础字符串长度计算。
	if value.Kind != KindString {
		return 0, false
	}

	// Go string 的 len 返回字节数，正好对齐 Lua string 长度语义。
	return len(value.String), true
}

// StringHash 使用 Lua 5.3 字符串 hash 采样算法计算 hash。
//
// seed 对齐 Lua 全局状态中的 hash seed；当前项目默认可使用 DefaultStringHash。
func StringHash(text string, seed uint32) uint32 {
	// Lua 5.3 使用 seed xor 字符串长度作为初始 hash。
	hashValue := seed ^ uint32(len(text))
	step := (len(text) >> stringHashLimit) + 1
	for remaining := len(text); remaining >= step; remaining -= step {
		// 按 Lua 5.3 luaS_hash 公式从后往前采样字符。
		hashValue ^= (hashValue << 5) + (hashValue >> 2) + uint32(text[remaining-1])
	}

	// 返回 32 位 hash 值，后续字符串池可直接复用。
	return hashValue
}

// DefaultStringHash 使用当前阶段默认 seed 计算 Lua string hash。
//
// 后续接入 State 随机种子后，可改由 State 持有 seed 并调用 StringHash。
func DefaultStringHash(text string) uint32 {
	// 使用固定 seed 便于当前阶段单测稳定。
	return StringHash(text, defaultStringHashSeed)
}

// StringEqual 判断两个 Lua string 值是否按字节相等。
//
// 两个入参都必须是 string；任一非 string 时返回 false。
func StringEqual(left Value, right Value) bool {
	// 任一值不是 string 时，不能进行字符串相等比较。
	if left.Kind != KindString || right.Kind != KindString {
		return false
	}

	// Go string == 按字节序列比较，符合 Lua string equality 语义。
	return left.String == right.String
}

// ConcatStrings 拼接一组 Lua string 值。
//
// 入参必须全部为 string；任一非 string 或拼接长度溢出 int 时返回 false。
func ConcatStrings(values ...Value) (Value, bool) {
	// 空输入在当前 helper 中定义为无法拼接，VM 的 CONCAT 指令会保证至少两个操作数。
	if len(values) == 0 {
		return NilValue(), false
	}

	totalLength := 0
	for _, value := range values {
		// CONCAT 基础路径只接受已转换为 string 的值；数字转字符串由 VM/tostring 路径处理。
		if value.Kind != KindString {
			return NilValue(), false
		}
		if len(value.String) > math.MaxInt-totalLength {
			// 长度超过 Go int 可表达范围时，拒绝构造结果，避免分配溢出。
			return NilValue(), false
		}
		totalLength += len(value.String)
	}

	result := make([]byte, 0, totalLength)
	for _, value := range values {
		// 按输入顺序追加字节，保持 Lua concat 的左到右结果。
		result = append(result, value.String...)
	}

	// 返回拼接后的 Lua string 值。
	return StringValue(string(result)), true
}
