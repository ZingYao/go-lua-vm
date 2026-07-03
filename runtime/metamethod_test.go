package runtime

import (
	"testing"

	"github.com/zing/go-lua-vm/bytecode"
)

// TestVMArithmeticMetamethod 验证算术指令在基础数字路径失败后会调用算术元方法。
//
// Lua 5.3 对 table 等非 number 操作数会按左右操作数顺序查找对应 tag method；当前 VM
// 阶段先支持 GoFunction 形式元方法，用于后续 bridge 与完整调用栈接入前的行为闭环。
func TestVMArithmeticMetamethod(t *testing.T) {
	vm := NewVM(3)
	leftValue := tableValueWithMetamethod(metamethodAdd, func(args ...Value) (Value, error) {
		// __add 测试只关心 VM 是否把两侧原始操作数传入元方法。
		return IntegerValue(int64(len(args)) + 7), nil
	})
	if err := vm.SetRegister(1, leftValue); err != nil {
		// 测试准备阶段写入左操作数必须成功。
		t.Fatalf("set left register failed: %v", err)
	}
	if err := vm.SetRegister(2, StringValue("right")); err != nil {
		// 测试准备阶段写入右操作数必须成功。
		t.Fatalf("set right register failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpAdd, 0, 1, 2)); err != nil {
		// ADD 遇到 table/string 时应回退到 __add 元方法。
		t.Fatalf("add metamethod failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(IntegerValue(9)) {
		// 目标寄存器必须保存 __add 元方法返回值。
		t.Fatalf("add metamethod result mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestCallGoClosureResultsSupportsFixedResultsFunction 验证低层 Go closure 调用支持固定结果函数。
//
// 标准库中 string.byte/sub/find/format 会注册为 GoFixedResultsFunction；元方法或 gsub replacement
// 等间接调用场景必须能在命中快路径时返回固定结果，未命中时回退完整多返回函数。
func TestCallGoClosureResultsSupportsFixedResultsFunction(t *testing.T) {
	// fixedFunction 模拟标准库固定结果函数，integer 实参命中快路径，其他实参回退 Fallback。
	fixedFunction := &GoFixedResultsFunction{
		MaxResults: 1,
		Function: func(dst []Value, args ...Value) (int, bool, error) {
			// 结果槽由调用方按 MaxResults 预分配。
			if len(dst) < 1 {
				// 结果槽不足时返回寄存器边界错误。
				return 0, false, ErrRegisterOutOfRange
			}
			if len(args) == 1 && args[0].Kind == KindInteger {
				// 单个 integer 实参命中固定结果快路径。
				dst[0] = StringValue("fixed")
				return 1, true, nil
			}
			// 其它形态交给完整 fallback。
			return 0, false, nil
		},
		Fallback: func(args ...Value) ([]Value, error) {
			// fallback 返回可区分文本，确认调用方没有误用未命中结果槽。
			return []Value{StringValue("fallback")}, nil
		},
	}
	method := ReferenceValue(KindGoClosure, fixedFunction)

	results, err := callGoClosureResults(method, IntegerValue(1))
	if err != nil {
		// 固定结果命中不应失败。
		t.Fatalf("call fixed function failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "fixed" {
		// 命中快路径时必须返回固定结果槽写入值。
		t.Fatalf("fixed function result mismatch: %#v", results)
	}

	results, err = callGoClosureResults(method, StringValue("x"))
	if err != nil {
		// fallback 也不应失败。
		t.Fatalf("call fixed fallback failed: %v", err)
	}
	if len(results) != 1 || results[0].String != "fallback" {
		// 未命中快路径时必须执行完整 fallback。
		t.Fatalf("fixed fallback result mismatch: %#v", results)
	}
}

// TestVMBitwiseMetamethod 验证位运算指令在 integer 转换失败后会调用位运算元方法。
//
// Lua 5.3 对 `&`、`|`、`~`、移位等操作在基础整数路径失败时查找对应元方法；当前用
// `__band` 代表整组位运算回退机制。
func TestVMBitwiseMetamethod(t *testing.T) {
	vm := NewVM(3)
	leftValue := tableValueWithMetamethod(metamethodBand, func(args ...Value) (Value, error) {
		// __band 返回固定整数，便于区分基础位运算结果和元方法结果。
		return IntegerValue(0x53), nil
	})
	if err := vm.SetRegister(1, leftValue); err != nil {
		// 测试准备阶段写入左操作数必须成功。
		t.Fatalf("set left register failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(0xff)); err != nil {
		// 测试准备阶段写入右操作数必须成功。
		t.Fatalf("set right register failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpBAnd, 0, 1, 2)); err != nil {
		// BAND 遇到非 integer table 时应回退到 __band 元方法。
		t.Fatalf("band metamethod failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(IntegerValue(0x53)) {
		// 目标寄存器必须保存 __band 元方法返回值。
		t.Fatalf("band metamethod result mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMCompareMetamethods 验证 EQ、LT、LE 会在基础比较不适用时调用比较元方法。
//
// EQ 使用 `__eq` 的 truthiness；LT 使用 `__lt`；LE 优先使用 `__le`，未定义时回退到
// `not (right < left)` 的 Lua 5.3 兼容路径。
func TestVMCompareMetamethods(t *testing.T) {
	t.Run("eq", func(t *testing.T) {
		// 子用例隔离 EQ 的 skipNext 结果，避免与其他比较共享 VM 状态。
		vm := NewVM(2)
		leftValue := tableValueWithMetamethod(metamethodEq, func(args ...Value) (Value, error) {
			// __eq 返回 truthy 字符串，验证 VM 会按 Lua truthiness 解释结果。
			return StringValue("truthy"), nil
		})
		if err := vm.SetRegister(0, leftValue); err != nil {
			// 测试准备阶段写入左操作数必须成功。
			t.Fatalf("set left register failed: %v", err)
		}
		if err := vm.SetRegister(1, ReferenceValue(KindTable, NewTable())); err != nil {
			// 测试准备阶段写入右操作数必须成功。
			t.Fatalf("set right register failed: %v", err)
		}
		if err := vm.Step(bytecode.CreateABC(bytecode.OpEq, 1, 0, 1)); err != nil {
			// EQ raw 不相等时应调用 __eq 元方法。
			t.Fatalf("eq metamethod failed: %v", err)
		}
		if vm.SkipNext() {
			// A=1 表示期望比较为 true，__eq truthy 时不应跳过下一条。
			t.Fatalf("eq should not skip after truthy __eq")
		}
	})

	t.Run("lt", func(t *testing.T) {
		// 子用例隔离 LT 的 skipNext 结果。
		vm := NewVM(2)
		leftValue := tableValueWithMetamethod(metamethodLt, func(args ...Value) (Value, error) {
			// __lt 返回 true，验证基础比较失败后能完成比较。
			return BooleanValue(true), nil
		})
		if err := vm.SetRegister(0, leftValue); err != nil {
			// 测试准备阶段写入左操作数必须成功。
			t.Fatalf("set left register failed: %v", err)
		}
		if err := vm.SetRegister(1, StringValue("right")); err != nil {
			// 测试准备阶段写入右操作数必须成功。
			t.Fatalf("set right register failed: %v", err)
		}
		if err := vm.Step(bytecode.CreateABC(bytecode.OpLt, 1, 0, 1)); err != nil {
			// LT 遇到 table/string 时应调用 __lt 元方法。
			t.Fatalf("lt metamethod failed: %v", err)
		}
		if vm.SkipNext() {
			// A=1 表示期望比较为 true，__lt true 时不应跳过下一条。
			t.Fatalf("lt should not skip after true __lt")
		}
	})

	t.Run("le-fallback-lt", func(t *testing.T) {
		// 子用例覆盖 Lua 5.3 中 __le 缺失时的反向 __lt 回退。
		vm := NewVM(2)
		rightValue := tableValueWithMetamethod(metamethodLt, func(args ...Value) (Value, error) {
			// 反向 __lt 返回 false，因此 left <= right 应为 true。
			return BooleanValue(false), nil
		})
		if err := vm.SetRegister(0, StringValue("left")); err != nil {
			// 测试准备阶段写入左操作数必须成功。
			t.Fatalf("set left register failed: %v", err)
		}
		if err := vm.SetRegister(1, rightValue); err != nil {
			// 测试准备阶段写入右操作数必须成功。
			t.Fatalf("set right register failed: %v", err)
		}
		if err := vm.Step(bytecode.CreateABC(bytecode.OpLe, 1, 0, 1)); err != nil {
			// LE 在没有 __le 时应尝试 not (right < left)。
			t.Fatalf("le fallback metamethod failed: %v", err)
		}
		if vm.SkipNext() {
			// A=1 表示期望比较为 true，反向 __lt false 推导出的 LE true 不应跳过。
			t.Fatalf("le should not skip after false reverse __lt")
		}
	})
}

// TestVMLengthMetamethod 验证 table 的 LEN 指令会优先调用 `__len`。
//
// Lua 5.3 允许 table 使用 `__len` 覆盖基础边界搜索；元方法返回值不强制转换，保持
// lvm.c 中调用 tag method 后直接写回结果的语义。
func TestVMLengthMetamethod(t *testing.T) {
	vm := NewVM(2)
	tableValue := tableValueWithMetamethod(metamethodLen, func(args ...Value) (Value, error) {
		// __len 返回固定整数，便于区分 Table.Len 的基础结果。
		return IntegerValue(42), nil
	})
	if err := vm.SetRegister(1, tableValue); err != nil {
		// 测试准备阶段写入源寄存器必须成功。
		t.Fatalf("set source register failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpLen, 0, 1, 0)); err != nil {
		// LEN 遇到带 __len 的 table 时必须调用元方法。
		t.Fatalf("len metamethod failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(IntegerValue(42)) {
		// 目标寄存器必须保存 __len 元方法返回值。
		t.Fatalf("len metamethod result mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMConcatMetamethod 验证 CONCAT 在字符串转换失败后会调用 `__concat`。
//
// 当前 VM 以二元折叠方式实现连续 CONCAT；当任一对操作数不能转换为 string 时，按 Lua
// 5.3 查找 `__concat`，元方法返回值作为后续折叠的累计值。
func TestVMConcatMetamethod(t *testing.T) {
	vm := NewVM(3)
	leftValue := tableValueWithMetamethod(metamethodConcat, func(args ...Value) (Value, error) {
		// __concat 返回字符串，验证 VM 会把元方法结果写回目标寄存器。
		return StringValue("joined"), nil
	})
	if err := vm.SetRegister(1, leftValue); err != nil {
		// 测试准备阶段写入左操作数必须成功。
		t.Fatalf("set left register failed: %v", err)
	}
	if err := vm.SetRegister(2, StringValue("right")); err != nil {
		// 测试准备阶段写入右操作数必须成功。
		t.Fatalf("set right register failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpConcat, 0, 1, 2)); err != nil {
		// CONCAT 遇到 table/string 时应回退到 __concat 元方法。
		t.Fatalf("concat metamethod failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("joined")) {
		// 目标寄存器必须保存 __concat 元方法返回值。
		t.Fatalf("concat metamethod result mismatch: value=%#v ok=%v", value, ok)
	}
}

// tableValueWithMetamethod 创建带单个 Go 元方法的 table 值。
//
// name 是元方法字段名；function 是当前 VM 可直接调用的 GoFunction。返回值可直接写入
// VM 寄存器参与元方法测试。
func tableValueWithMetamethod(name string, function GoFunction) Value {
	// 构造普通 table 作为操作数 identity。
	table := NewTable()
	metatable := NewTable()
	metatable.RawSetString(name, ReferenceValue(KindGoClosure, function))
	table.SetMetatable(metatable)
	return ReferenceValue(KindTable, table)
}
