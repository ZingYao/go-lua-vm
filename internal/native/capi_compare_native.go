//go:build native_modules

package native

/*
typedef struct lua_State lua_State;
*/
import "C"

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/zing/go-lua-vm/runtime"
)

const (
	nativeLuaOpEq = 0
	nativeLuaOpLt = 1
	nativeLuaOpLe = 2
)

// nativeLuaCompare 实现 Lua 5.3 C API 的基础比较入口。
func nativeLuaCompare(luaState unsafe.Pointer, leftIndex int, rightIndex int, operation int) int {
	// 比较先按 C API 视角读取两个栈值；任一索引无效时直接返回 false。
	left, leftOK := nativeLuaValueAt(luaState, leftIndex)
	right, rightOK := nativeLuaValueAt(luaState, rightIndex)
	if !leftOK || !rightOK {
		// Lua C API 对无效索引返回 0，不触发错误。
		return 0
	}
	var result bool
	var comparable bool
	switch operation {
	case nativeLuaOpEq:
		// 当前 native shim 的 equality 先覆盖 raw equality；__eq 元方法后续随完整比较阶段补齐。
		result = left.RawEqual(right)
		comparable = true
	case nativeLuaOpLt:
		// 小于比较覆盖 number 和 string 的基础 Lua 5.3 语义。
		result, comparable = nativeLuaCompareLessThan(left, right)
	case nativeLuaOpLe:
		// 小于等于比较覆盖 number 和 string 的基础 Lua 5.3 语义。
		result, comparable = nativeLuaCompareLessEqual(left, right)
	default:
		// 未知 op 在 Lua C API 中属于 api_check 范畴；当前 shim 记录错误后返回 0。
		message := fmt.Sprintf("invalid lua_compare operation %d", operation)
		_ = setNativeStatePendingError(luaState, runtime.StringValue(message))
		return 0
	}
	if !comparable {
		// 不可比较类型记录 pending error，等待 C function 返回边界传播。
		_ = setNativeStatePendingError(luaState, runtime.StringValue(nativeLuaCompareError(left, right)))
		return 0
	}
	if result {
		// Lua C API 使用 int 表示 boolean，非 0 为 true。
		return 1
	}
	return 0
}

// nativeLuaCompareLessThan 执行基础 Lua 小于比较。
func nativeLuaCompareLessThan(left runtime.Value, right runtime.Value) (bool, bool) {
	// number 与 number 按 Lua 5.3 数值语义比较，保留 integer/float 边界精度。
	if left.Kind == runtime.KindInteger && right.Kind == runtime.KindInteger {
		return left.Integer < right.Integer, true
	}
	if left.Kind == runtime.KindInteger && right.Kind == runtime.KindNumber {
		return nativeLuaIntegerLessThanFloat(left.Integer, right.Number), true
	}
	if left.Kind == runtime.KindNumber && right.Kind == runtime.KindInteger {
		return nativeLuaFloatLessThanInteger(left.Number, right.Integer), true
	}
	if left.IsNumber() && right.IsNumber() {
		// 同为 float number 时 Go 的 < 与 Lua 基础比较一致，NaN 会返回 false。
		leftNumber, _ := left.ToNumber()
		rightNumber, _ := right.ToNumber()
		return leftNumber < rightNumber, true
	}
	if left.Kind == runtime.KindString && right.Kind == runtime.KindString {
		// Lua 5.3 基础 string 比较按字节字典序执行。
		return left.String < right.String, true
	}
	return false, false
}

// nativeLuaCompareLessEqual 执行基础 Lua 小于等于比较。
func nativeLuaCompareLessEqual(left runtime.Value, right runtime.Value) (bool, bool) {
	// number 与 number 的 <= 可由 raw equality 和严格小于组合，保持 NaN 为 false。
	if left.IsNumber() && right.IsNumber() {
		if left.RawEqual(right) {
			return true, true
		}
		return nativeLuaCompareLessThan(left, right)
	}
	if left.Kind == runtime.KindString && right.Kind == runtime.KindString {
		// Lua 5.3 基础 string 比较按字节字典序执行。
		return left.String <= right.String, true
	}
	return false, false
}

// nativeLuaCompareError 生成基础比较失败时的 Lua 风格错误文本。
func nativeLuaCompareError(left runtime.Value, right runtime.Value) string {
	// 同类型不可比较时使用 two <type> values，混合类型时说明左右类型。
	leftTypeName := nativeLuaTypeName(nativeLuaTypeCode(left, false))
	rightTypeName := nativeLuaTypeName(nativeLuaTypeCode(right, false))
	if leftTypeName == rightTypeName {
		return fmt.Sprintf("attempt to compare two %s values", leftTypeName)
	}
	return fmt.Sprintf("attempt to compare %s with %s", leftTypeName, rightTypeName)
}

// nativeLuaIntegerLessThanFloat 比较 Lua integer 是否小于 Lua float number。
func nativeLuaIntegerLessThanFloat(integerValue int64, numberValue float64) bool {
	// NaN 与任何数字的有序比较都为 false。
	if math.IsNaN(numberValue) {
		return false
	}
	if numberValue <= float64(math.MinInt64) {
		// 小于或等于最小 integer 的 float 不大于任何 Lua integer。
		return false
	}
	if numberValue >= -float64(math.MinInt64) {
		// 大于等于 2^63 的 float 大于所有 Lua integer。
		return true
	}
	floorValue := math.Floor(numberValue)
	floorInteger := int64(floorValue)
	if floorValue == numberValue {
		// float 本身是整数值时执行严格整数比较。
		return integerValue < floorInteger
	}
	return integerValue <= floorInteger
}

// nativeLuaFloatLessThanInteger 比较 Lua float number 是否小于 Lua integer。
func nativeLuaFloatLessThanInteger(numberValue float64, integerValue int64) bool {
	// NaN 与任何数字的有序比较都为 false。
	if math.IsNaN(numberValue) {
		return false
	}
	if numberValue < float64(math.MinInt64) {
		// 小于最小 integer 的 float 小于所有 Lua integer。
		return true
	}
	if numberValue >= -float64(math.MinInt64) {
		// 大于等于 2^63 的 float 不小于任何 Lua integer。
		return false
	}
	ceilValue := math.Ceil(numberValue)
	ceilInteger := int64(ceilValue)
	if ceilValue == numberValue {
		// float 本身是整数值时执行严格整数比较。
		return ceilInteger < integerValue
	}
	return ceilInteger <= integerValue
}

// lua_compare 导出 Lua 5.3 C API 比较入口。
//
//export lua_compare
func lua_compare(luaState *C.lua_State, leftIndex C.int, rightIndex C.int, operation C.int) C.int {
	// C API 入口只做类型转换；具体比较规则由 Go helper 维护。
	return C.int(nativeLuaCompare(unsafe.Pointer(luaState), int(leftIndex), int(rightIndex), int(operation)))
}
