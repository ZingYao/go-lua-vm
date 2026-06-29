package runtime

import (
	"strings"
	"testing"
)

// TestStringPoolInternsShortStrings 验证短字符串会进入驻留池。
//
// 相同字节序列重复驻留必须复用池条目，避免短字符串重复分配。
func TestStringPoolInternsShortStrings(t *testing.T) {
	pool := NewStringPool()
	first := pool.Intern("lua")
	second := pool.Intern("lua")

	if !first.RawEqual(second) {
		// 相同短字符串驻留后必须得到相同 Lua 值语义。
		t.Fatalf("interned short strings should raw equal")
	}
	if pool.Len() != 1 {
		// 重复驻留同一短字符串不能增加池条目数量。
		t.Fatalf("string pool length mismatch after duplicate intern: got %d", pool.Len())
	}

	_ = pool.Intern("go")
	if pool.Len() != 2 {
		// 不同短字符串必须分别占用驻留池条目。
		t.Fatalf("string pool length mismatch after new intern: got %d", pool.Len())
	}
}

// TestStringPoolSkipsLongStrings 验证长字符串不会进入短字符串池。
//
// 长字符串生命周期由 Go GC 管理，当前阶段不做驻留以避免池无限增长。
func TestStringPoolSkipsLongStrings(t *testing.T) {
	pool := NewStringPool()
	longText := strings.Repeat("x", maxShortStringLen+1)
	first := pool.Intern(longText)
	second := pool.Intern(longText)

	if !first.RawEqual(second) {
		// 长字符串虽然不驻留，但相同字节序列的 Lua string 仍按值相等。
		t.Fatalf("long strings should raw equal by value")
	}
	if pool.Len() != 0 {
		// 长字符串不能写入短字符串驻留池。
		t.Fatalf("long string should not be interned: got %d", pool.Len())
	}
}

// TestStringPoolStoreShortString 验证 Store 对短字符串使用驻留策略。
//
// Store 会返回字符串存储元信息，供后续 Table 和 GC 路径识别短字符串驻留状态。
func TestStringPoolStoreShortString(t *testing.T) {
	pool := NewStringPool()
	storage := pool.Store("short")

	if storage.Kind != StringStorageShortInterned {
		// 短字符串必须标记为驻留策略。
		t.Fatalf("short string storage kind mismatch: %s", storage.Kind)
	}
	if !storage.Interned {
		// 短字符串必须进入驻留池。
		t.Fatalf("short string should be interned")
	}
	if !storage.Value.RawEqual(StringValue("short")) {
		// Store 返回的 Lua 值必须保留原始字符串内容。
		t.Fatalf("short string storage value mismatch: %#v", storage.Value)
	}
	if storage.ByteLen != len("short") {
		// Store 必须记录 Lua 字节长度。
		t.Fatalf("short string byte length mismatch: %d", storage.ByteLen)
	}
	if storage.Hash != DefaultStringHash("short") {
		// Store 必须记录 Lua 5.3 字符串 hash。
		t.Fatalf("short string hash mismatch: %d", storage.Hash)
	}
	if pool.Len() != 1 {
		// 短字符串 Store 必须写入驻留池。
		t.Fatalf("short string pool length mismatch: %d", pool.Len())
	}
}

// TestStringPoolStoreLongString 验证 Store 对长字符串使用非驻留策略。
//
// 长字符串只由返回的 Value 持有内容，不写入短字符串池，生命周期交给 Go GC。
func TestStringPoolStoreLongString(t *testing.T) {
	pool := NewStringPool()
	longText := strings.Repeat("x", maxShortStringLen+1)
	storage := pool.Store(longText)

	if storage.Kind != StringStorageLongOwned {
		// 长字符串必须标记为非驻留持有策略。
		t.Fatalf("long string storage kind mismatch: %s", storage.Kind)
	}
	if storage.Interned {
		// 长字符串不得进入短字符串驻留池。
		t.Fatalf("long string should not be interned")
	}
	if !storage.Value.RawEqual(StringValue(longText)) {
		// Store 返回的 Lua 值必须保留完整长字符串内容。
		t.Fatalf("long string storage value mismatch: %#v", storage.Value)
	}
	if storage.ByteLen != len(longText) {
		// Store 必须记录长字符串字节长度。
		t.Fatalf("long string byte length mismatch: %d", storage.ByteLen)
	}
	if storage.Hash != DefaultStringHash(longText) {
		// Store 必须记录长字符串 hash，后续 table key 可复用该值。
		t.Fatalf("long string hash mismatch: %d", storage.Hash)
	}
	if pool.Len() != 0 {
		// 长字符串 Store 不得写入短字符串池。
		t.Fatalf("long string pool length mismatch: %d", pool.Len())
	}
}

// TestStringPoolShortStringBoundary 验证短字符串字节长度边界。
//
// Lua 5.3 默认 40 字节以内属于短字符串，超过 40 字节属于长字符串。
func TestStringPoolShortStringBoundary(t *testing.T) {
	if !IsShortString(strings.Repeat("a", maxShortStringLen)) {
		// 等于短字符串上限时仍应视为短字符串。
		t.Fatalf("max short string length should be short")
	}
	if IsShortString(strings.Repeat("a", maxShortStringLen+1)) {
		// 超过短字符串上限时必须视为长字符串。
		t.Fatalf("string longer than max should be long")
	}
}

// TestZeroValueStringPool 验证零值 StringPool 可直接使用。
//
// 该能力便于 State 后续内嵌 StringPool 时不用额外处理 map 初始化顺序。
func TestZeroValueStringPool(t *testing.T) {
	var pool StringPool
	value := pool.Intern("zero")

	if !value.RawEqual(StringValue("zero")) {
		// 零值池驻留后必须返回正确的 Lua string 值。
		t.Fatalf("zero value string pool intern mismatch: %#v", value)
	}
	if pool.Len() != 1 {
		// 零值池首次 Intern 后必须完成 map 初始化并记录条目。
		t.Fatalf("zero value string pool length mismatch: got %d", pool.Len())
	}
}

// TestStringLen 验证 Lua string 长度按字节计算。
//
// UTF-8 多字节字符在 Lua 中仍按底层字节数计长。
func TestStringLen(t *testing.T) {
	length, ok := StringLen(StringValue("好"))
	if !ok || length != len("好") {
		// 中文字符应返回 UTF-8 字节长度，而不是 rune 数量。
		t.Fatalf("string byte length mismatch: length=%d ok=%v", length, ok)
	}

	if _, ok := StringLen(IntegerValue(1)); ok {
		// 非 string 值不能走字符串长度 helper。
		t.Fatalf("non-string should not have string length")
	}
}

// TestStringHash 验证字符串 hash 与 Lua 5.3 采样算法的稳定结果。
//
// 这里锁定短字符串和长字符串各一个结果，避免后续字符串池实现改坏基础算法。
func TestStringHash(t *testing.T) {
	if got := StringHash("lua", 0); got != 201586 {
		// 短字符串 hash 应对齐当前 Go 实现的 Lua 5.3 公式。
		t.Fatalf("short string hash mismatch: got %d", got)
	}

	longText := "abcdefghijklmnopqrstuvwxyz0123456789"
	if got := StringHash(longText, 0); got != 2731011768 {
		// 长字符串 hash 应覆盖 step > 1 的采样路径。
		t.Fatalf("long string hash mismatch: got %d", got)
	}
}

// TestStringEqual 验证 Lua string 按字节比较。
//
// 字符串比较不做 Unicode 归一化，也不触发元方法。
func TestStringEqual(t *testing.T) {
	if !StringEqual(StringValue("a\x00b"), StringValue("a\x00b")) {
		// 相同字节序列必须相等。
		t.Fatalf("same byte string should equal")
	}
	if StringEqual(StringValue("a"), StringValue("A")) {
		// 不同字节序列必须不相等。
		t.Fatalf("different byte string should not equal")
	}
	if StringEqual(StringValue("1"), IntegerValue(1)) {
		// 非 string 值不能被 StringEqual 视为相等。
		t.Fatalf("string and integer should not string-equal")
	}
}

// TestConcatStrings 验证基础字符串拼接。
//
// 当前 helper 只拼接已是 string 的值，数字转字符串由 VM 的 tostring 路径处理。
func TestConcatStrings(t *testing.T) {
	result, ok := ConcatStrings(StringValue("go"), StringValue("-"), StringValue("lua"))
	if !ok || !result.RawEqual(StringValue("go-lua")) {
		// 多段字符串必须按输入顺序拼接。
		t.Fatalf("concat mismatch: result=%#v ok=%v", result, ok)
	}

	if _, ok := ConcatStrings(StringValue("x"), IntegerValue(1)); ok {
		// 非 string 操作数必须拒绝，后续 VM 可选择尝试元方法。
		t.Fatalf("concat with non-string should fail")
	}
}
