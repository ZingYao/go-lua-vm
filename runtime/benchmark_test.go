package runtime

import (
	"testing"

	"github.com/zing/go-lua-vm/bytecode"
)

// benchmarkValueSink 保存基准测试读取到的值，避免编译器完全消除循环体。
var benchmarkValueSink Value

// benchmarkResultsSink 保存基准测试函数调用结果，避免返回值路径被优化掉。
var benchmarkResultsSink []Value

// BenchmarkVMDispatch 度量最小 VM 单条指令分发开销。
//
// 该基准使用 MOVE 指令避免常量表和元方法干扰，后续 VM dispatch 优化可用该基线对比。
func BenchmarkVMDispatch(b *testing.B) {
	// 初始化固定寄存器窗口，保证循环内只测 Step 分发路径。
	vm := NewVM(2)
	if err := vm.SetRegister(0, IntegerValue(53)); err != nil {
		// 基准准备失败说明 VM 初始化或寄存器写入语义异常。
		b.Fatalf("set source register failed: %v", err)
	}
	instruction := bytecode.CreateABC(bytecode.OpMove, 1, 0, 0)

	b.ReportAllocs()
	b.ResetTimer()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		if err := vm.Step(instruction); err != nil {
			// MOVE 是已支持基础指令，循环中失败必须终止基准。
			b.Fatalf("dispatch move failed: %v", err)
		}
	}
}

// BenchmarkTableReadWrite 度量 Table raw integer key 读写开销。
//
// 该基准分离写入和读取子项，便于后续数组区/hash 区优化分别观察影响。
func BenchmarkTableReadWrite(b *testing.B) {
	b.Run("raw_set_integer", func(b *testing.B) {
		// 写入路径使用正整数 key，主要覆盖数组区扩容和赋值。
		table := NewTable()
		b.ReportAllocs()
		for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
			table.RawSetInteger(int64(benchmarkIndex%1024)+1, IntegerValue(int64(benchmarkIndex)))
		}
	})

	b.Run("raw_get_integer", func(b *testing.B) {
		// 预填固定数组区，循环内只测 raw get 读取路径。
		table := NewTable()
		for valueIndex := 1; valueIndex <= 1024; valueIndex++ {
			table.RawSetInteger(int64(valueIndex), IntegerValue(int64(valueIndex)))
		}
		b.ReportAllocs()
		b.ResetTimer()
		for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
			benchmarkValueSink = table.RawGetInteger(int64(benchmarkIndex%1024) + 1)
		}
	})
}

// BenchmarkGoFunctionCall 度量当前 Go closure 调用适配层开销。
//
// 该基准覆盖 Lua 调 Go 的最小桥接通道，后续 bridge 完整实现可继续复用该基线。
func BenchmarkGoFunctionCall(b *testing.B) {
	// 构造单返回值 GoFunction，避免被测路径包含多返回值分配之外的业务逻辑。
	function := ReferenceValue(KindGoClosure, GoFunction(func(args ...Value) (Value, error) {
		if len(args) == 0 {
			// 无实参时返回 nil，防御基准调用方误用。
			return NilValue(), nil
		}

		// 正常路径返回第一个实参，保持调用开销可观测。
		return args[0], nil
	}))
	argument := IntegerValue(53)

	b.ReportAllocs()
	b.ResetTimer()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		results, err := callGoClosureResults(function, argument)
		if err != nil {
			// Go closure 适配层不应在合法函数和参数下失败。
			b.Fatalf("go function call failed: %v", err)
		}
		benchmarkResultsSink = results
	}
}

// BenchmarkStringConcat 度量 Lua string 拼接 helper 的基础开销。
//
// 该基准覆盖当前 VM CONCAT 指令的底层字符串拼接路径，后续字符串驻留或 buffer 优化可复用该基线。
func BenchmarkStringConcat(b *testing.B) {
	// 固定四段短字符串，避免基准结果被输入构造成本主导。
	values := []Value{
		StringValue("lua"),
		StringValue("-"),
		StringValue("5.3"),
		StringValue("-vm"),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		value, ok := ConcatStrings(values...)
		if !ok {
			// 全部输入都是 string，拼接失败说明底层 helper 语义异常。
			b.Fatalf("string concat failed")
		}
		benchmarkValueSink = value
	}
}

// BenchmarkVMConcatInstruction 度量 VM OP_CONCAT 在纯字符串寄存器上的执行成本。
//
// 该基准隔离 DoString、循环和 GC，只观察 CONCAT 指令自身的寄存器读取、快路径分派和字符串分配。
func BenchmarkVMConcatInstruction(b *testing.B) {
	b.Run("binary_string", func(b *testing.B) {
		// 二元短字符串拼接覆盖 `s = s .. "x"` codegen 后的核心 OP_CONCAT 形态。
		vm := NewVM(3)
		if err := vm.SetRegister(0, StringValue("prefix")); err != nil {
			// 基准准备阶段必须能写入目标寄存器。
			b.Fatalf("set left register failed: %v", err)
		}
		if err := vm.SetRegister(1, StringValue("x")); err != nil {
			// 基准准备阶段必须能写入右操作数寄存器。
			b.Fatalf("set right register failed: %v", err)
		}
		instruction := bytecode.CreateABC(bytecode.OpConcat, 2, 0, 1)

		b.ReportAllocs()
		b.ResetTimer()
		for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
			if err := vm.Step(instruction); err != nil {
				// 纯字符串 CONCAT 不应失败。
				b.Fatalf("concat instruction failed: %v", err)
			}
		}
		benchmarkValueSink, _ = vm.Register(2)
	})

	b.Run("empty_right", func(b *testing.B) {
		// 右操作数为空字符串时，Lua 5.3 直接保留左操作数。
		vm := NewVM(3)
		if err := vm.SetRegister(0, StringValue("prefix")); err != nil {
			b.Fatalf("set left register failed: %v", err)
		}
		if err := vm.SetRegister(1, StringValue("")); err != nil {
			b.Fatalf("set right register failed: %v", err)
		}
		instruction := bytecode.CreateABC(bytecode.OpConcat, 2, 0, 1)

		b.ReportAllocs()
		b.ResetTimer()
		for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
			if err := vm.Step(instruction); err != nil {
				// 空字符串快路径不应改变 CONCAT 成功语义。
				b.Fatalf("concat instruction failed: %v", err)
			}
		}
		benchmarkValueSink, _ = vm.Register(2)
	})

	b.Run("empty_left", func(b *testing.B) {
		// 左操作数为空字符串时，Lua 5.3 直接使用右操作数。
		vm := NewVM(3)
		if err := vm.SetRegister(0, StringValue("")); err != nil {
			b.Fatalf("set left register failed: %v", err)
		}
		if err := vm.SetRegister(1, StringValue("suffix")); err != nil {
			b.Fatalf("set right register failed: %v", err)
		}
		instruction := bytecode.CreateABC(bytecode.OpConcat, 2, 0, 1)

		b.ReportAllocs()
		b.ResetTimer()
		for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
			if err := vm.Step(instruction); err != nil {
				// 空字符串快路径不应改变 CONCAT 成功语义。
				b.Fatalf("concat instruction failed: %v", err)
			}
		}
		benchmarkValueSink, _ = vm.Register(2)
	})

	b.Run("four_strings", func(b *testing.B) {
		// 多段字符串拼接覆盖 `a .. b .. c .. d` 这种寄存器范围快路径。
		vm := NewVM(5)
		parts := []string{"lua", "-", "5.3", "-vm"}
		for partIndex, part := range parts {
			// 将各片段预置到连续寄存器，循环内只测 CONCAT。
			if err := vm.SetRegister(partIndex, StringValue(part)); err != nil {
				b.Fatalf("set part register failed: %v", err)
			}
		}
		instruction := bytecode.CreateABC(bytecode.OpConcat, 4, 0, 3)

		b.ReportAllocs()
		b.ResetTimer()
		for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
			if err := vm.Step(instruction); err != nil {
				// 纯字符串 CONCAT 不应失败。
				b.Fatalf("concat instruction failed: %v", err)
			}
		}
		benchmarkValueSink, _ = vm.Register(4)
	})
}

// BenchmarkGoLuaCallback 度量当前 Go/Lua 回调边界的最小往返成本。
//
// 当前阶段尚未接入完整 Lua closure 执行循环，因此用 Thread.Resume 执行 Go closure 模拟 Lua 入口调 Go 回调。
func BenchmarkGoLuaCallback(b *testing.B) {
	// 创建独立 State，避免基准循环内反复分配运行时根对象。
	state := NewState()
	function := ReferenceValue(KindGoClosure, GoResultsFunction(func(args ...Value) ([]Value, error) {
		if len(args) == 0 {
			// 无参数调用时返回空列表，防御基准入口被误改。
			return nil, nil
		}

		// 正常路径回传参数，模拟 Lua 调 Go 后把结果交还 Lua 栈。
		return []Value{args[0]}, nil
	}))
	argument := IntegerValue(53)

	b.ReportAllocs()
	b.ResetTimer()
	for benchmarkIndex := 0; benchmarkIndex < b.N; benchmarkIndex++ {
		// 每轮创建新协程以保持 Resume 可执行；dead 协程按 Lua 语义不能重复恢复。
		thread := state.NewThread(function)
		results, err := thread.Resume(argument)
		if err != nil {
			// 合法 Go closure 入口不应在回调边界失败。
			b.Fatalf("go/lua callback failed: %v", err)
		}
		benchmarkResultsSink = results
	}
}
