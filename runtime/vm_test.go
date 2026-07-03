package runtime

import (
	"errors"
	"math"
	"testing"

	"github.com/zing/go-lua-vm/bytecode"
)

// TestVMMoveCopiesRegister 验证 MOVE 会复制源寄存器到目标寄存器。
//
// MOVE 是 Lua 5.3 最基础的数据搬运指令，语义为 R(A) := R(B)。
func TestVMMoveCopiesRegister(t *testing.T) {
	vm := NewVM(3)
	if err := vm.SetRegister(1, StringValue("lua")); err != nil {
		// 测试准备阶段写入源寄存器必须成功。
		t.Fatalf("set source register failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpMove, 0, 1, 0))
	if err != nil {
		// MOVE 指令在合法寄存器范围内必须执行成功。
		t.Fatalf("move step failed: %v", err)
	}

	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("lua")) {
		// 目标寄存器必须获得源寄存器原值。
		t.Fatalf("move target mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMMovePreservesReferenceIdentity 验证 MOVE 复制引用值时保留 identity。
//
// Table、closure、userdata 和 thread 后续都依赖引用值复制不改变 Ref identity。
func TestVMMovePreservesReferenceIdentity(t *testing.T) {
	vm := NewVM(2)
	table := NewTable()
	value := ReferenceValue(KindTable, table)
	if err := vm.SetRegister(1, value); err != nil {
		// 测试准备阶段写入引用值必须成功。
		t.Fatalf("set reference register failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpMove, 0, 1, 0)); err != nil {
		// MOVE 复制引用值必须成功。
		t.Fatalf("move reference failed: %v", err)
	}
	movedValue, ok := vm.Register(0)
	if !ok || movedValue.Kind != KindTable || movedValue.Ref != table {
		// 复制后的引用值必须指向同一个 table identity。
		t.Fatalf("moved reference mismatch: value=%#v ok=%v", movedValue, ok)
	}
}

// TestVMMoveOutOfRange 验证 MOVE 遇到寄存器越界时返回错误且不覆盖目标。
//
// 损坏 chunk 或编译器错误可能产生越界寄存器，VM 必须拒绝并保持已有寄存器值。
func TestVMMoveOutOfRange(t *testing.T) {
	vm := NewVM(1)
	if err := vm.SetRegister(0, StringValue("keep")); err != nil {
		// 测试准备阶段写入目标寄存器必须成功。
		t.Fatalf("set target register failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpMove, 0, 2, 0))
	if !errors.Is(err, ErrRegisterOutOfRange) {
		// 源寄存器越界必须返回 ErrRegisterOutOfRange。
		t.Fatalf("move out of range error mismatch: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("keep")) {
		// 越界失败不能覆盖目标寄存器。
		t.Fatalf("target register should remain unchanged: value=%#v ok=%v", value, ok)
	}
}

// TestVMSetVarargUpdatesVarargInstructionSource 验证 SetVararg 会更新 OP_VARARG 的读取源。
//
// debug.setlocal 负索引会通过该入口改写活动 VM 的 vararg；后续 `...` 展开必须读取新值。
func TestVMSetVarargUpdatesVarargInstructionSource(t *testing.T) {
	// 创建带两个 vararg 的 VM，并改写第二个 vararg。
	vm := NewVMWithPrototypeData(2, nil, nil, nil, []Value{StringValue("old1"), StringValue("old2")})
	if !vm.SetVararg(1, StringValue("new2")) {
		// 合法 vararg 下标必须写入成功。
		t.Fatalf("SetVararg should succeed")
	}
	if vm.SetVararg(2, StringValue("overflow")) {
		// 越界 vararg 下标不能写入成功。
		t.Fatalf("SetVararg overflow should fail")
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpVararg, 0, 3, 0)); err != nil {
		// VARARG 固定展开两个值应执行成功。
		t.Fatalf("VARARG step failed: %v", err)
	}
	first, firstOK := vm.Register(0)
	second, secondOK := vm.Register(1)
	if !firstOK || !secondOK || first.String != "old1" || second.String != "new2" {
		// 展开结果必须包含改写后的 vararg 值。
		t.Fatalf("vararg registers mismatch: first=%#v second=%#v", first, second)
	}
}

// TestVMLoadKLoadsConstants 验证 LOADK 会把常量表值写入目标寄存器。
//
// LOADK 是常量加载基础指令，后续表达式、函数和 table 构造都会依赖该路径。
func TestVMLoadKLoadsConstants(t *testing.T) {
	vm := NewVMWithConstants(5, []bytecode.Constant{
		bytecode.NilConstant(),
		bytecode.BooleanConstant(true),
		bytecode.IntegerConstant(53),
		bytecode.NumberConstant(5.3),
		bytecode.StringConstant("lua"),
	})

	tests := []struct {
		name          string
		register      int
		constant      int
		expectedValue Value
	}{
		{name: "nil", register: 0, constant: 0, expectedValue: NilValue()},
		{name: "boolean", register: 1, constant: 1, expectedValue: BooleanValue(true)},
		{name: "integer", register: 2, constant: 2, expectedValue: IntegerValue(53)},
		{name: "number", register: 3, constant: 3, expectedValue: NumberValue(5.3)},
		{name: "string", register: 4, constant: 4, expectedValue: StringValue("lua")},
	}
	for _, testCase := range tests {
		// 每个常量类型都需要独立验证，避免转换时混淆 integer/number/string。
		instruction := bytecode.CreateABx(bytecode.OpLoadK, testCase.register, testCase.constant)
		if err := vm.Step(instruction); err != nil {
			// 合法 LOADK 必须执行成功。
			t.Fatalf("%s loadk failed: %v", testCase.name, err)
		}
		value, ok := vm.Register(testCase.register)
		if !ok || !value.RawEqual(testCase.expectedValue) {
			// 目标寄存器必须得到常量转换后的运行时值。
			t.Fatalf("%s loadk value mismatch: value=%#v ok=%v", testCase.name, value, ok)
		}
	}
}

// TestVMLoadKCopiesConstantSlice 验证 VM 创建时会复制常量表切片。
//
// 调用方后续修改原始常量表不应影响已创建 VM 的 Proto 视图。
func TestVMLoadKCopiesConstantSlice(t *testing.T) {
	constants := []bytecode.Constant{bytecode.StringConstant("before")}
	vm := NewVMWithConstants(1, constants)
	constants[0] = bytecode.StringConstant("after")

	err := vm.Step(bytecode.CreateABx(bytecode.OpLoadK, 0, 0))
	if err != nil {
		// LOADK 使用 VM 内部复制的常量表，应执行成功。
		t.Fatalf("loadk copied constant failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("before")) {
		// VM 必须读取创建时复制的常量，而不是外部被修改后的切片。
		t.Fatalf("copied constant mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMBorrowedPrototypeDataReusesConstantSlice 验证执行期 VM 会借用 Proto 常量表。
//
// Lua closure 执行路径传入的 constants/protos 来自不可变 Proto；借用切片可以避免每次函数调用复制
// Proto 数据。该测试只覆盖专用构造函数，公开 NewVMWithConstants 仍保持复制语义。
func TestVMBorrowedPrototypeDataReusesConstantSlice(t *testing.T) {
	constants := []bytecode.Constant{bytecode.StringConstant("before")}
	vm := NewVMWithBorrowedPrototypeData(1, constants, nil, nil, nil)
	constants[0] = bytecode.StringConstant("after")

	if err := vm.Step(bytecode.CreateABx(bytecode.OpLoadK, 0, 0)); err != nil {
		// 借用常量表的 LOADK 仍应执行成功。
		t.Fatalf("loadk borrowed constant failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("after")) {
		// 专用构造函数应读取借用切片的最新值，用于证明没有复制 Proto 常量表。
		t.Fatalf("borrowed constant mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMLoadKOutOfRange 验证 LOADK 常量或寄存器越界会返回明确错误。
//
// 损坏 chunk 可能引用不存在的常量表索引，VM 必须拒绝执行并保留目标寄存器。
func TestVMLoadKOutOfRange(t *testing.T) {
	vm := NewVMWithConstants(1, []bytecode.Constant{bytecode.StringConstant("keep")})
	if err := vm.SetRegister(0, StringValue("original")); err != nil {
		// 测试准备阶段写入目标寄存器必须成功。
		t.Fatalf("set original register failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABx(bytecode.OpLoadK, 0, 9))
	if !errors.Is(err, ErrConstantOutOfRange) {
		// 常量表索引越界必须返回 ErrConstantOutOfRange。
		t.Fatalf("loadk constant out of range error mismatch: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("original")) {
		// 常量越界失败不能覆盖目标寄存器。
		t.Fatalf("target register should remain after constant error: value=%#v ok=%v", value, ok)
	}

	err = vm.Step(bytecode.CreateABx(bytecode.OpLoadK, 2, 0))
	if !errors.Is(err, ErrRegisterOutOfRange) {
		// 目标寄存器越界必须返回 ErrRegisterOutOfRange。
		t.Fatalf("loadk register out of range error mismatch: %v", err)
	}
}

// TestVMLoadKXWithExtraArg 验证 LOADKX 会使用紧随其后的 EXTRAARG 加载常量。
//
// Lua 5.3 使用 LOADKX + EXTRAARG 表示超过 Bx 可直接编码范围的常量索引。
func TestVMLoadKXWithExtraArg(t *testing.T) {
	vm := NewVMWithConstants(1, []bytecode.Constant{bytecode.StringConstant("wide")})

	if err := vm.Step(bytecode.CreateABx(bytecode.OpLoadKX, 0, 0)); err != nil {
		// 合法 LOADKX 必须先记录目标寄存器。
		t.Fatalf("loadkx step failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.IsNil() {
		// EXTRAARG 执行前不能提前写入目标寄存器。
		t.Fatalf("loadkx should wait for extraarg: value=%#v ok=%v", value, ok)
	}

	if err := vm.Step(bytecode.CreateAx(bytecode.OpExtraArg, 0)); err != nil {
		// EXTRAARG 必须完成前置 LOADKX 的常量加载。
		t.Fatalf("extraarg step failed: %v", err)
	}
	value, ok = vm.Register(0)
	if !ok || !value.RawEqual(StringValue("wide")) {
		// 目标寄存器必须获得 EXTRAARG 指定的常量值。
		t.Fatalf("loadkx target mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMLoadKXRequiresExtraArg 验证 LOADKX 后必须紧跟 EXTRAARG。
//
// 损坏 chunk 若在 LOADKX 后接其他指令，VM 必须返回明确错误而不是继续执行。
func TestVMLoadKXRequiresExtraArg(t *testing.T) {
	vm := NewVMWithConstants(1, []bytecode.Constant{bytecode.StringConstant("wide")})
	if err := vm.Step(bytecode.CreateABx(bytecode.OpLoadKX, 0, 0)); err != nil {
		// 第一步 LOADKX 必须记录等待状态。
		t.Fatalf("loadkx setup failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpMove, 0, 0, 0))
	if !errors.Is(err, ErrExpectedExtraArg) {
		// LOADKX 后接非 EXTRAARG 必须返回 ErrExpectedExtraArg。
		t.Fatalf("expected extraarg error mismatch: %v", err)
	}
}

// TestVMUnexpectedExtraArg 验证没有前置 LOADKX 的 EXTRAARG 会返回错误。
//
// EXTRAARG 是扩展参数指令，不能独立执行。
func TestVMUnexpectedExtraArg(t *testing.T) {
	vm := NewVMWithConstants(1, []bytecode.Constant{bytecode.StringConstant("wide")})
	err := vm.Step(bytecode.CreateAx(bytecode.OpExtraArg, 0))
	if !errors.Is(err, ErrUnexpectedExtraArg) {
		// 独立 EXTRAARG 必须返回 ErrUnexpectedExtraArg。
		t.Fatalf("unexpected extraarg error mismatch: %v", err)
	}
}

// TestVMLoadBool 验证 LOADBOOL 写入 boolean 并记录跳过下一条指令的标记。
//
// Lua 5.3 中 B 为 0 表示 false，非 0 表示 true；C 非 0 表示跳过下一条指令。
func TestVMLoadBool(t *testing.T) {
	vm := NewVM(2)
	if err := vm.Step(bytecode.CreateABC(bytecode.OpLoadBool, 0, 1, 1)); err != nil {
		// LOADBOOL 写 true 并设置跳转标记必须成功。
		t.Fatalf("loadbool true failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(BooleanValue(true)) {
		// B 非 0 时目标寄存器必须为 true。
		t.Fatalf("loadbool true mismatch: value=%#v ok=%v", value, ok)
	}
	if !vm.SkipNext() {
		// C 非 0 时必须要求跳过下一条指令。
		t.Fatalf("loadbool should set skip next")
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpLoadBool, 1, 0, 0)); err != nil {
		// LOADBOOL 写 false 且不设置跳转标记必须成功。
		t.Fatalf("loadbool false failed: %v", err)
	}
	value, ok = vm.Register(1)
	if !ok || !value.RawEqual(BooleanValue(false)) {
		// B 为 0 时目标寄存器必须为 false。
		t.Fatalf("loadbool false mismatch: value=%#v ok=%v", value, ok)
	}
	if vm.SkipNext() {
		// C 为 0 时不得要求跳过下一条指令。
		t.Fatalf("loadbool should not set skip next")
	}
}

// TestVMLoadNil 验证 LOADNIL 会清空闭区间寄存器。
//
// 指令语义为 R(A)..R(A+B) := nil，包含起止两个寄存器。
func TestVMLoadNil(t *testing.T) {
	vm := NewVM(4)
	for registerIndex := 0; registerIndex < 4; registerIndex++ {
		// 测试准备阶段将全部寄存器写成非 nil。
		if err := vm.SetRegister(registerIndex, StringValue("value")); err != nil {
			t.Fatalf("set register %d failed: %v", registerIndex, err)
		}
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpLoadNil, 1, 2, 0)); err != nil {
		// LOADNIL 清空 R(1)..R(3) 必须成功。
		t.Fatalf("loadnil failed: %v", err)
	}
	for registerIndex := 1; registerIndex <= 3; registerIndex++ {
		// 被 LOADNIL 覆盖的寄存器必须为 nil。
		value, ok := vm.Register(registerIndex)
		if !ok || !value.IsNil() {
			t.Fatalf("loadnil register %d mismatch: value=%#v ok=%v", registerIndex, value, ok)
		}
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("value")) {
		// 区间外寄存器不能被 LOADNIL 修改。
		t.Fatalf("loadnil should not touch register 0: value=%#v ok=%v", value, ok)
	}
}

// TestVMLoadNilOutOfRange 验证 LOADNIL 区间越界时不修改任何寄存器。
//
// 损坏 chunk 可能产生越界清空区间，VM 必须保持寄存器窗口原样。
func TestVMLoadNilOutOfRange(t *testing.T) {
	vm := NewVM(2)
	_ = vm.SetRegister(0, StringValue("keep0"))
	_ = vm.SetRegister(1, StringValue("keep1"))

	err := vm.Step(bytecode.CreateABC(bytecode.OpLoadNil, 1, 2, 0))
	if !errors.Is(err, ErrRegisterOutOfRange) {
		// 清空区间超过寄存器窗口必须返回 ErrRegisterOutOfRange。
		t.Fatalf("loadnil out of range error mismatch: %v", err)
	}
	firstValue, firstOK := vm.Register(0)
	secondValue, secondOK := vm.Register(1)
	if !firstOK || !secondOK || !firstValue.RawEqual(StringValue("keep0")) || !secondValue.RawEqual(StringValue("keep1")) {
		// 越界失败不能修改任何寄存器。
		t.Fatalf("loadnil should keep registers: first=%#v ok=%v second=%#v ok=%v", firstValue, firstOK, secondValue, secondOK)
	}
}

// TestVMGetAndSetUpvalue 验证 GETUPVAL 与 SETUPVAL 会在寄存器和 upvalue 之间搬运值。
//
// upvalue 是闭包语义的基础，当前最小 VM 先验证值读取和写回边界，开放 upvalue 生命周期
// 会在调用帧和闭包阶段继续接入。
func TestVMGetAndSetUpvalue(t *testing.T) {
	vm := NewVMWithConstantsAndUpvalues(2, nil, []Value{StringValue("up")})

	if err := vm.Step(bytecode.CreateABC(bytecode.OpGetUpval, 0, 0, 0)); err != nil {
		// GETUPVAL 读取合法 upvalue 必须成功。
		t.Fatalf("getupval failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("up")) {
		// 目标寄存器必须获得 upvalue 当前值。
		t.Fatalf("getupval value mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.SetRegister(1, IntegerValue(53)); err != nil {
		// 测试准备阶段写入源寄存器必须成功。
		t.Fatalf("set source register failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpSetupVal, 1, 0, 0)); err != nil {
		// SETUPVAL 写入合法 upvalue 必须成功。
		t.Fatalf("setupval failed: %v", err)
	}
	upvalue, ok := vm.Upvalue(0)
	if !ok || !upvalue.RawEqual(IntegerValue(53)) {
		// upvalue 必须获得源寄存器当前值。
		t.Fatalf("setupval value mismatch: value=%#v ok=%v", upvalue, ok)
	}
}

// TestVMUpvalueCellWithoutSnapshot 验证执行期 VM 可只通过共享 cell 访问 upvalue。
//
// Lua closure 执行器会在存在 UpvalueCell 时跳过 Upvalues 快照复制；GETUPVAL、SETTABUP 和
// 捕获外层 upvalue 都必须继续以 cell 作为真实 upvalue 来源。
func TestVMUpvalueCellWithoutSnapshot(t *testing.T) {
	table := NewTable()
	table.RawSetString("name", StringValue("lua"))
	cell := NewClosedUpvalueCell(ReferenceValue(KindTable, table))
	vm := NewVMWithConstantsAndUpvalues(2, []bytecode.Constant{bytecode.StringConstant("name"), bytecode.StringConstant("version")}, nil)
	vm.BindBorrowedUpvalueCells([]*UpvalueCell{cell})

	if err := vm.Step(bytecode.CreateABC(bytecode.OpGetUpval, 0, 0, 0)); err != nil {
		// 仅有 cell 时 GETUPVAL 仍必须能读取 upvalue。
		t.Fatalf("get upvalue from cell failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || value.Kind != KindTable || value.Ref != table {
		// GETUPVAL 结果必须来自共享 cell 保存的 table。
		t.Fatalf("get upvalue from cell mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.SetRegister(1, IntegerValue(53)); err != nil {
		// 测试准备阶段写入 SETTABUP value 寄存器必须成功。
		t.Fatalf("set value register failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpSetTabUp, 0, bytecode.RKAsK(1), 1)); err != nil {
		// 仅有 cell 时 SETTABUP 必须能通过 upvalue table 写字段。
		t.Fatalf("set tabup from cell failed: %v", err)
	}
	if got := table.RawGetString("version"); !got.RawEqual(IntegerValue(53)) {
		// SETTABUP 应写入 cell 中 table 的 hash 字段。
		t.Fatalf("set tabup value mismatch: %#v", got)
	}

	childProto := bytecode.NewProto("child")
	childProto.Upvalues = []bytecode.UpvalueDesc{{Name: "env", InStack: false, Index: 0}}
	vm.protos = []*bytecode.Proto{childProto}
	if err := vm.Step(bytecode.CreateABx(bytecode.OpClosure, 0, 0)); err != nil {
		// 捕获外层 upvalue 时必须能复用已有共享 cell。
		t.Fatalf("closure capture from cell failed: %v", err)
	}
	closureValue, ok := vm.Register(0)
	closure, closureOK := closureValue.Ref.(*LuaClosure)
	if !ok || closureValue.Kind != KindLuaClosure || !closureOK || len(closure.UpvalueCells) != 1 || closure.UpvalueCells[0] != cell {
		// 子闭包必须复用同一个 cell，保证多层闭包共享 upvalue。
		t.Fatalf("closure capture cell mismatch: value=%#v closure=%#v", closureValue, closure)
	}
}

// TestUpvalueCellCloseKeepsStableValue 验证开放 upvalue 闭合后不再受原寄存器复用影响。
//
// Lua 局部变量离开作用域后，捕获它的 closure 必须继续看到闭合时的值；后续寄存器复用不能污染
// 已闭合 upvalue。该测试覆盖 UpvalueCell 内嵌闭合槽的关键语义边界。
func TestUpvalueCellCloseKeepsStableValue(t *testing.T) {
	// 模拟活动寄存器被闭包捕获。
	register := IntegerValue(53)
	cell := NewOpenUpvalueCell(&register)
	cell.Close()

	// 复用原寄存器槽不能影响已闭合 upvalue。
	register = IntegerValue(99)
	if value := cell.Value(); !value.RawEqual(IntegerValue(53)) {
		// 闭合值必须停留在 Close 时刻的寄存器值。
		t.Fatalf("closed upvalue value mismatch: %#v", value)
	}
	cell.Set(StringValue("closed"))
	if value := cell.Value(); !value.RawEqual(StringValue("closed")) {
		// 闭合后的 SETUPVAL 仍应写入同一个共享 cell。
		t.Fatalf("closed upvalue set mismatch: %#v", value)
	}
	if !register.RawEqual(IntegerValue(99)) {
		// 写入闭合 upvalue 不能回写已经离开作用域的旧寄存器。
		t.Fatalf("closed upvalue should not mutate old register: %#v", register)
	}
}

// TestVMUpvalueOutOfRange 验证 upvalue 越界时返回明确错误且不覆盖已有值。
//
// 损坏 chunk 或闭包原型不匹配可能访问不存在的 upvalue，VM 必须拒绝并保持状态。
func TestVMUpvalueOutOfRange(t *testing.T) {
	vm := NewVMWithConstantsAndUpvalues(1, nil, []Value{StringValue("keep")})
	if err := vm.SetRegister(0, StringValue("target")); err != nil {
		// 测试准备阶段写入目标寄存器必须成功。
		t.Fatalf("set target register failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpGetUpval, 0, 3, 0))
	if !errors.Is(err, ErrUpvalueOutOfRange) {
		// GETUPVAL 访问不存在 upvalue 必须返回 ErrUpvalueOutOfRange。
		t.Fatalf("getupval out of range error mismatch: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("target")) {
		// GETUPVAL 失败不能覆盖目标寄存器。
		t.Fatalf("getupval should keep target: value=%#v ok=%v", value, ok)
	}

	err = vm.Step(bytecode.CreateABC(bytecode.OpSetupVal, 0, 3, 0))
	if !errors.Is(err, ErrUpvalueOutOfRange) {
		// SETUPVAL 访问不存在 upvalue 必须返回 ErrUpvalueOutOfRange。
		t.Fatalf("setupval out of range error mismatch: %v", err)
	}
	upvalue, ok := vm.Upvalue(0)
	if !ok || !upvalue.RawEqual(StringValue("keep")) {
		// SETUPVAL 失败不能覆盖已有 upvalue。
		t.Fatalf("setupval should keep upvalue: value=%#v ok=%v", upvalue, ok)
	}
}

// TestVMGetTable 验证 GETTABLE 支持 RK 常量 key 和寄存器 key。
//
// 当前 table 指令先复用 Table.Get 的普通读取语义，后续函数形式元方法会在调用能力具备后接入。
func TestVMGetTable(t *testing.T) {
	table := NewTable()
	table.RawSetString("name", StringValue("lua"))
	table.RawSetInteger(1, StringValue("first"))
	vm := NewVMWithConstants(3, []bytecode.Constant{bytecode.StringConstant("name")})
	if err := vm.SetRegister(0, ReferenceValue(KindTable, table)); err != nil {
		// 测试准备阶段写入 table 寄存器必须成功。
		t.Fatalf("set table register failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(1)); err != nil {
		// 测试准备阶段写入寄存器 key 必须成功。
		t.Fatalf("set key register failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpGetTable, 1, 0, bytecode.RKAsK(0))); err != nil {
		// GETTABLE 使用常量 key 读取必须成功。
		t.Fatalf("gettable constant key failed: %v", err)
	}
	value, ok := vm.Register(1)
	if !ok || !value.RawEqual(StringValue("lua")) {
		// 常量 key 路径必须读取到 string 字段。
		t.Fatalf("gettable constant key mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpGetTable, 1, 0, 2)); err != nil {
		// GETTABLE 使用寄存器 key 读取必须成功。
		t.Fatalf("gettable register key failed: %v", err)
	}
	value, ok = vm.Register(1)
	if !ok || !value.RawEqual(StringValue("first")) {
		// 寄存器 key 路径必须读取到数组区字段。
		t.Fatalf("gettable register key mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMGetTableUsesUserdataIndexMetatable 验证 GETTABLE 可通过 userdata `__index` 读取方法。
//
// Lua 5.3 file handle 与 Go 对象代理依赖 userdata 元表暴露方法；该测试只覆盖读取路径，
// 调用语义仍由 CALL 和 Go closure 适配层负责。
func TestVMGetTableUsesUserdataIndexMetatable(t *testing.T) {
	methodValue := StringValue("method")
	methods := NewTable()
	methods.RawSetString("write", methodValue)
	metatable := NewTable()
	metatable.RawSetString(tableIndexMetamethodKey, ReferenceValue(KindTable, methods))
	userdata := NewUserdata("file-like")
	if err := userdata.SetMetatable(metatable); err != nil {
		// 测试准备阶段绑定 userdata 元表必须成功。
		t.Fatalf("set userdata metatable failed: %v", err)
	}

	vm := NewVMWithConstants(2, []bytecode.Constant{bytecode.StringConstant("write")})
	if err := vm.SetRegister(0, userdata.Value()); err != nil {
		// 测试准备阶段写入 userdata 寄存器必须成功。
		t.Fatalf("set userdata register failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpGetTable, 1, 0, bytecode.RKAsK(0))); err != nil {
		// GETTABLE 通过 userdata __index table 读取方法必须成功。
		t.Fatalf("gettable userdata index failed: %v", err)
	}

	value, ok := vm.Register(1)
	if !ok || !value.RawEqual(methodValue) {
		// 目标寄存器必须获得 __index 方法表中的方法值。
		t.Fatalf("userdata gettable mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMSetTable 验证 SETTABLE 支持 RK 常量 key/value 和寄存器 value。
//
// SETTABLE 是 table 构造、字段赋值和对象代理的基础指令，当前阶段覆盖 raw 写入成功路径。
func TestVMSetTable(t *testing.T) {
	table := NewTable()
	vm := NewVMWithConstants(3, []bytecode.Constant{
		bytecode.StringConstant("name"),
		bytecode.StringConstant("lua"),
	})
	if err := vm.SetRegister(0, ReferenceValue(KindTable, table)); err != nil {
		// 测试准备阶段写入 table 寄存器必须成功。
		t.Fatalf("set table register failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpSetTable, 0, bytecode.RKAsK(0), bytecode.RKAsK(1))); err != nil {
		// SETTABLE 使用常量 key/value 写入必须成功。
		t.Fatalf("settable constant key value failed: %v", err)
	}
	value := table.RawGetString("name")
	if !value.RawEqual(StringValue("lua")) {
		// table 必须保存常量 value。
		t.Fatalf("settable constant value mismatch: value=%#v", value)
	}

	if err := vm.SetRegister(1, StringValue("version")); err != nil {
		// 测试准备阶段写入寄存器 key 必须成功。
		t.Fatalf("set key register failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(53)); err != nil {
		// 测试准备阶段写入寄存器 value 必须成功。
		t.Fatalf("set value register failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpSetTable, 0, 1, 2)); err != nil {
		// SETTABLE 使用寄存器 key/value 写入必须成功。
		t.Fatalf("settable register key value failed: %v", err)
	}
	value = table.RawGetString("version")
	if !value.RawEqual(IntegerValue(53)) {
		// table 必须保存寄存器 value。
		t.Fatalf("settable register value mismatch: value=%#v", value)
	}

	if err := vm.SetRegister(1, IntegerValue(1)); err != nil {
		// 测试准备阶段写入整数寄存器 key 必须成功。
		t.Fatalf("set integer key register failed: %v", err)
	}
	if err := vm.SetRegister(2, StringValue("first")); err != nil {
		// 测试准备阶段写入数组区 value 必须成功。
		t.Fatalf("set array value register failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpSetTable, 0, 1, 2)); err != nil {
		// SETTABLE 使用整数寄存器 key 写入数组区必须成功。
		t.Fatalf("settable integer register key failed: %v", err)
	}
	value = table.RawGetInteger(1)
	if !value.RawEqual(StringValue("first")) {
		// table 必须保存数组区寄存器 value。
		t.Fatalf("settable integer register value mismatch: value=%#v", value)
	}
}

// TestNewLuaClosureCachesDirectCallSafe 验证 Lua closure 创建时缓存 direct CALL 属性。
//
// 叶子函数可走 direct CALL；包含 CALL 的函数必须保留完整调用路径，避免裁剪 debug/coroutine 现场。
func TestNewLuaClosureCachesDirectCallSafe(t *testing.T) {
	leafProto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpReturn, 0, 1, 0),
		},
	}
	leafClosure := NewLuaClosure(leafProto, nil, nil)
	if !leafClosure.DirectCallSafe {
		// 只有 RETURN 的叶子函数应被标记为 direct CALL safe。
		t.Fatalf("leaf closure should be direct-call safe")
	}
	if leafClosure.LeafAddReturn != nil {
		// 只有 RETURN 的叶子函数不是 ADD;RETURN 形态。
		t.Fatalf("return-only closure should not cache leaf add")
	}

	callingProto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpCall, 0, 1, 1),
			bytecode.CreateABC(bytecode.OpReturn, 0, 1, 0),
		},
	}
	callingClosure := NewLuaClosure(callingProto, nil, nil)
	if callingClosure.DirectCallSafe {
		// 含 CALL 的函数不能进入 leaf direct CALL 路径。
		t.Fatalf("calling closure should not be direct-call safe")
	}
	if callingClosure.LeafAddReturn != nil {
		// 含 CALL 的函数不能缓存 ADD;RETURN 形态。
		t.Fatalf("calling closure should not cache leaf add")
	}

	addProto := &bytecode.Proto{
		Constants: []bytecode.Constant{bytecode.IntegerConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpAdd, 1, 0, bytecode.RKAsK(0)),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
	addClosure := NewLuaClosure(addProto, nil, nil)
	if addClosure.LeafAddReturn == nil || addClosure.LeafAddReturn.AddInstruction.OpCode() != bytecode.OpAdd {
		// ADD;RETURN 形态应在 closure 创建时缓存，避免调用热路径重复扫描 Proto。
		t.Fatalf("add closure should cache leaf add")
	}
	if addClosure.LeafAddReturn.LeftOperand.Constant || addClosure.LeafAddReturn.LeftOperand.RegisterIndex != 0 {
		// 左操作数 R0 应缓存为寄存器操作数。
		t.Fatalf("add closure left operand metadata mismatch: %+v", addClosure.LeafAddReturn.LeftOperand)
	}
	if !addClosure.LeafAddReturn.RightOperand.Constant || !addClosure.LeafAddReturn.RightOperand.ConstantValue.RawEqual(IntegerValue(1)) {
		// 右操作数 K1 应在创建时转换为 runtime integer 值。
		t.Fatalf("add closure right operand metadata mismatch: %+v", addClosure.LeafAddReturn.RightOperand)
	}
	if !addClosure.LeafAddReturn.HasRegisterIntegerConstant || addClosure.LeafAddReturn.IntegerRegisterIndex != 0 || addClosure.LeafAddReturn.IntegerConstant != 1 {
		// R0 + K1 应额外缓存为寄存器加 integer 常量专用形态。
		t.Fatalf("add closure integer constant metadata mismatch: %+v", addClosure.LeafAddReturn)
	}

	upvalueAddProto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpGetUpval, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpAdd, 1, 0, 1),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
	upvalueAddClosure := NewLuaClosure(upvalueAddProto, nil, nil)
	if upvalueAddClosure.LeafAddReturn == nil || !upvalueAddClosure.LeafAddReturn.HasUpvalueRegister || upvalueAddClosure.LeafAddReturn.UpvalueIndex != 0 {
		// GETUPVAL;ADD;RETURN 形态也应缓存 upvalue 元数据。
		t.Fatalf("upvalue add closure should cache leaf add metadata")
	}
	if !upvalueAddClosure.LeafAddReturn.HasRegisterUpvalueAdd || upvalueAddClosure.LeafAddReturn.UpvalueAddRegisterIndex != 0 {
		// R0 + upvalue 应额外缓存为实参加 upvalue 专用形态。
		t.Fatalf("upvalue add closure should cache register upvalue metadata: %+v", upvalueAddClosure.LeafAddReturn)
	}

	upvalueIntegerProto := &bytecode.Proto{
		Constants: []bytecode.Constant{bytecode.IntegerConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpGetUpval, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpAdd, 1, 1, bytecode.RKAsK(0)),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
	upvalueIntegerClosure := NewLuaClosure(upvalueIntegerProto, nil, nil)
	if upvalueIntegerClosure.LeafAddReturn == nil || !upvalueIntegerClosure.LeafAddReturn.HasRegisterIntegerConstant || upvalueIntegerClosure.LeafAddReturn.IntegerRegisterIndex != 1 || upvalueIntegerClosure.LeafAddReturn.IntegerConstant != 1 {
		// upvalue + K1 也应缓存为整数常量专用形态。
		t.Fatalf("upvalue integer add metadata mismatch: %+v", upvalueIntegerClosure.LeafAddReturn)
	}

	upvalueAddSetProto := &bytecode.Proto{
		Constants: []bytecode.Constant{bytecode.IntegerConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpGetUpval, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpAdd, 0, 0, bytecode.RKAsK(0)),
			bytecode.CreateABC(bytecode.OpSetupVal, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpGetUpval, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpReturn, 0, 2, 0),
		},
	}
	upvalueAddSetClosure := NewLuaClosure(upvalueAddSetProto, nil, nil)
	if upvalueAddSetClosure.LeafUpvalueAddSetReturn == nil || upvalueAddSetClosure.LeafUpvalueAddSetReturn.UpvalueIndex != 0 || upvalueAddSetClosure.LeafUpvalueAddSetReturn.IntegerConstant != 1 {
		// upvalue 自增并返回同一 upvalue 的闭包叶子函数应缓存专用元数据。
		t.Fatalf("upvalue add-set metadata mismatch: %+v", upvalueAddSetClosure.LeafUpvalueAddSetReturn)
	}

	fibClosure := NewLuaClosure(testSelfRecursiveIntegerFibProto(), nil, nil)
	if fibClosure.DirectCallSafe {
		// 自递归 fib 含 CALL，不能被误判为叶子 direct CALL。
		t.Fatalf("self-recursive fib should not be direct-call safe")
	}
	if !fibClosure.SelfRecursiveIntegerFib {
		// 精确 fib 形态应在 closure 创建时缓存，供 caller-side 固定签名路径使用。
		t.Fatalf("self-recursive fib should cache fixed-signature metadata")
	}
}

// TestVMTryExecuteSelfRecursiveIntegerFibInCaller 验证固定签名自递归 fib caller-side 快路径。
//
// 该路径只处理官方 recursion benchmark 的 integer 小输入；非自引用、非 integer 或大输入必须
// 回退普通 VM，以保留 debug、traceback、调用深度和算术转换语义。
func TestVMTryExecuteSelfRecursiveIntegerFibInCaller(t *testing.T) {
	proto := testSelfRecursiveIntegerFibProto()
	selfCell := NewClosedUpvalueCell(NilValue())
	closure := NewLuaClosure(proto, nil, []*UpvalueCell{selfCell})
	selfCell.Set(ReferenceValue(KindLuaClosure, closure))
	vm := NewVM(2)
	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 测试准备阶段必须能写入函数槽。
		t.Fatalf("set function register failed: %v", err)
	}
	if err := vm.SetRegister(1, IntegerValue(15)); err != nil {
		// 测试准备阶段必须能写入唯一实参。
		t.Fatalf("set argument register failed: %v", err)
	}
	request := &CallRequest{FunctionIndex: 0, ArgumentCount: 1, ReturnCount: 1}
	handled, err := vm.TryExecuteSelfRecursiveIntegerFibInCaller(closure, request)
	if err != nil {
		// 合法小整数 fib 不应失败。
		t.Fatalf("self-recursive fib fast path failed: %v", err)
	}
	if !handled {
		// 目标形态必须由 fast path 完成。
		t.Fatalf("self-recursive fib should be handled")
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(IntegerValue(610)) {
		// fib(15) 的结果必须与普通递归一致。
		t.Fatalf("self-recursive fib result mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 回退场景也需要恢复函数槽。
		t.Fatalf("reset function register failed: %v", err)
	}
	if err := vm.SetRegister(1, StringValue("15")); err != nil {
		// 字符串数字在普通 VM 中涉及比较和算术转换，fast path 必须回退。
		t.Fatalf("set string argument failed: %v", err)
	}
	handled, err = vm.TryExecuteSelfRecursiveIntegerFibInCaller(closure, request)
	if err != nil || handled {
		// 非 integer 参数不能被 caller-side 路径吞掉。
		t.Fatalf("string argument should fallback: handled=%v err=%v", handled, err)
	}

	if err := vm.SetRegister(1, IntegerValue(selfRecursiveIntegerFibFastMaxArgument+1)); err != nil {
		// 大输入用于验证调用深度和栈溢出边界回退。
		t.Fatalf("set large argument failed: %v", err)
	}
	handled, err = vm.TryExecuteSelfRecursiveIntegerFibInCaller(closure, request)
	if err != nil || handled {
		// 超出小输入范围时必须保留普通递归调用栈。
		t.Fatalf("large argument should fallback: handled=%v err=%v", handled, err)
	}

	nonSelfCell := NewClosedUpvalueCell(NilValue())
	nonSelfClosure := NewLuaClosure(proto, nil, []*UpvalueCell{nonSelfCell})
	nonSelfCell.Set(ReferenceValue(KindLuaClosure, closure))
	if err := vm.SetRegister(1, IntegerValue(5)); err != nil {
		// 自引用 guard 测试仍需要合法 integer 参数。
		t.Fatalf("set non-self argument failed: %v", err)
	}
	handled, err = vm.TryExecuteSelfRecursiveIntegerFibInCaller(nonSelfClosure, request)
	if err != nil || handled {
		// upvalue 0 没有指回当前 closure 时不能当作自递归。
		t.Fatalf("non-self closure should fallback: handled=%v err=%v", handled, err)
	}
}

// testSelfRecursiveIntegerFibProto 构造官方 recursion benchmark 中 fib 子函数的精确 Proto。
func testSelfRecursiveIntegerFibProto() *bytecode.Proto {
	// 指令形态对应：if n < 2 then return n end; return fib(n-1)+fib(n-2)。
	return &bytecode.Proto{
		NumParams:    1,
		MaxStackSize: 4,
		Constants: []bytecode.Constant{
			bytecode.IntegerConstant(2),
			bytecode.IntegerConstant(1),
		},
		Upvalues: []bytecode.UpvalueDesc{{Name: "fib", InStack: true, Index: 0}},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpLt, 0, 0, bytecode.RKAsK(0)),
			bytecode.CreateAsBx(bytecode.OpJmp, 0, 1),
			bytecode.CreateABC(bytecode.OpReturn, 0, 2, 0),
			bytecode.CreateABC(bytecode.OpGetUpval, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpSub, 2, 0, bytecode.RKAsK(1)),
			bytecode.CreateABC(bytecode.OpCall, 1, 2, 2),
			bytecode.CreateABC(bytecode.OpGetUpval, 2, 0, 0),
			bytecode.CreateABC(bytecode.OpSub, 3, 0, bytecode.RKAsK(0)),
			bytecode.CreateABC(bytecode.OpCall, 2, 2, 2),
			bytecode.CreateABC(bytecode.OpAdd, 1, 1, 2),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
}

// TestVMTryExecuteLeafAddReturn 验证 `ADD; RETURN` 叶子函数快路径。
//
// 该快路径服务 Lua direct CALL 热点，必须只在单值返回形态命中，并保持普通 ADD/RETURN 语义。
func TestVMTryExecuteLeafAddReturn(t *testing.T) {
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{bytecode.IntegerConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpAdd, 1, 0, bytecode.RKAsK(0)),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
	vm := NewVMWithConstants(2, proto.Constants)
	vm.BindPrototype(proto)
	if err := vm.SetRegister(0, IntegerValue(41)); err != nil {
		// 测试准备阶段必须能写入参数寄存器。
		t.Fatalf("set argument register failed: %v", err)
	}
	results, _, handled, err := vm.TryExecuteLeafAddReturn(proto)
	if err != nil {
		// 合法 ADD/RETURN 快路径不应失败。
		t.Fatalf("leaf add return failed: %v", err)
	}
	if !handled {
		// 目标字节码形态必须被快路径识别。
		t.Fatalf("leaf add return should be handled")
	}
	if len(results) != 1 || !results[0].RawEqual(IntegerValue(42)) {
		// 快路径必须返回 ADD 结果。
		t.Fatalf("leaf add return result mismatch: %#v", results)
	}

	otherProto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpMove, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
	if _, _, handled, err := vm.TryExecuteLeafAddReturn(otherProto); err != nil || handled {
		// 非 ADD/RETURN 形态必须回退通用执行器。
		t.Fatalf("unexpected non-add handling: handled=%v err=%v", handled, err)
	}
}

// TestVMTryExecuteLeafAddReturnInCallerTwoArguments 验证 caller-side 二实参加法快路径。
//
// `local function add(a,b) return a+b end` 是函数调用热路径的典型形态；快路径必须只在两个
// 实参都真实存在且为原生数值时写回，缺参场景要回退完整 VM 以保留 nil 算术错误语义。
func TestVMTryExecuteLeafAddReturnInCallerTwoArguments(t *testing.T) {
	proto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpAdd, 0, 0, 1),
			bytecode.CreateABC(bytecode.OpReturn, 0, 2, 0),
		},
	}
	closure := NewLuaClosure(proto, nil, nil)
	if closure.LeafAddReturn == nil {
		// 二寄存器 ADD;RETURN 必须预解析为叶子加法形态。
		t.Fatalf("expected leaf add metadata")
	}
	if !closure.LeafAddReturn.HasRegisterRegisterAdd {
		// 双实参寄存器形态应命中专用缓存，避免热路径重复解析操作数。
		t.Fatalf("expected register-register leaf add metadata")
	}

	vm := NewVM(4)
	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 调用函数槽写入必须成功。
		t.Fatalf("set function register failed: %v", err)
	}
	if err := vm.SetRegister(1, IntegerValue(40)); err != nil {
		// 第一个实参写入必须成功。
		t.Fatalf("set first argument failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(2)); err != nil {
		// 第二个实参写入必须成功。
		t.Fatalf("set second argument failed: %v", err)
	}
	handled, err := vm.TryExecuteLeafAddReturnInCaller(closure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 2,
		ReturnCount:   1,
	})
	if err != nil {
		// 合法二实参加法不应失败。
		t.Fatalf("caller leaf add failed: %v", err)
	}
	if !handled {
		// 二实参 ADD;RETURN 应在 caller 侧完成。
		t.Fatalf("caller leaf add should be handled")
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(IntegerValue(42)) {
		// 结果必须直接写回函数槽。
		t.Fatalf("caller leaf add result mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 重新写回函数槽用于 number 混合路径验证。
		t.Fatalf("reset function register for number path failed: %v", err)
	}
	if err := vm.SetRegister(1, NumberValue(40.5)); err != nil {
		// 第一个 number 实参写入必须成功。
		t.Fatalf("set number argument failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(1)); err != nil {
		// 第二个 integer 实参写入必须成功。
		t.Fatalf("set integer argument failed: %v", err)
	}
	handled, err = vm.TryExecuteLeafAddReturnInCaller(closure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 2,
		ReturnCount:   1,
	})
	if err != nil || !handled {
		// 原生 number/integer 混合加法也应由双寄存器快路径覆盖。
		t.Fatalf("number caller leaf add mismatch: handled=%v err=%v", handled, err)
	}
	value, ok = vm.Register(0)
	if !ok || !value.RawEqual(NumberValue(41.5)) {
		// number 混合路径必须写回浮点结果。
		t.Fatalf("number caller leaf add result mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 重新写回函数槽用于缺参回退验证。
		t.Fatalf("reset function register failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(99)); err != nil {
		// 相邻寄存器放置哨兵，确保缺参不会被错误读取。
		t.Fatalf("set sentinel register failed: %v", err)
	}
	handled, err = vm.TryExecuteLeafAddReturnInCaller(closure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 1,
		ReturnCount:   1,
	})
	if err != nil || handled {
		// 第二个实参缺失时必须回退完整 VM，而不是读取 R2 哨兵。
		t.Fatalf("missing argument should fallback: handled=%v err=%v", handled, err)
	}
}

// TestVMTryExecuteLeafAddReturnInCallerFirstArgumentConstant 验证单实参加常量叶子快路径。
//
// `local function inc(x) return x + 1 end` 是函数调用循环的高频形态；caller 侧只能在第一个
// 实参真实存在且为原生数值时写回，缺参或非数值必须回退完整 VM。
func TestVMTryExecuteLeafAddReturnInCallerFirstArgumentConstant(t *testing.T) {
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{bytecode.IntegerConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpAdd, 1, 0, bytecode.RKAsK(0)),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
	closure := NewLuaClosure(proto, nil, nil)
	if closure.LeafAddReturn == nil || !closure.LeafAddReturn.HasRegisterIntegerConstant {
		// x + 1 必须预解析为寄存器加 integer 常量形态。
		t.Fatalf("expected register integer leaf metadata: %+v", closure.LeafAddReturn)
	}

	vm := NewVMWithConstants(3, proto.Constants)
	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 调用函数槽写入必须成功。
		t.Fatalf("set function register failed: %v", err)
	}
	if err := vm.SetRegister(1, IntegerValue(41)); err != nil {
		// 第一个实参写入必须成功。
		t.Fatalf("set integer argument failed: %v", err)
	}
	handled, err := vm.TryExecuteLeafAddReturnInCaller(closure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 1,
		ReturnCount:   1,
	})
	if err != nil || !handled {
		// integer + 常量应在 caller 侧完成。
		t.Fatalf("integer constant leaf mismatch: handled=%v err=%v", handled, err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(IntegerValue(42)) {
		// 结果必须直接写回函数槽。
		t.Fatalf("integer result mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 重新写回函数槽用于 number 路径验证。
		t.Fatalf("reset function register failed: %v", err)
	}
	if err := vm.SetRegister(1, NumberValue(40.5)); err != nil {
		// number 实参写入必须成功。
		t.Fatalf("set number argument failed: %v", err)
	}
	handled, err = vm.TryExecuteLeafAddReturnInCaller(closure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 1,
		ReturnCount:   1,
	})
	if err != nil || !handled {
		// number + 常量同样应在 caller 侧完成。
		t.Fatalf("number constant leaf mismatch: handled=%v err=%v", handled, err)
	}
	value, ok = vm.Register(0)
	if !ok || !value.RawEqual(NumberValue(41.5)) {
		// number 路径必须写回浮点结果。
		t.Fatalf("number result mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 重新写回函数槽用于缺参回退验证。
		t.Fatalf("reset function for missing argument failed: %v", err)
	}
	if err := vm.SetRegister(1, IntegerValue(99)); err != nil {
		// 相邻寄存器放置哨兵，缺参时不能读取该值。
		t.Fatalf("set sentinel failed: %v", err)
	}
	handled, err = vm.TryExecuteLeafAddReturnInCaller(closure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 0,
		ReturnCount:   1,
	})
	if err != nil || handled {
		// 缺参必须回退完整 VM，避免把旧 R1 当作参数。
		t.Fatalf("missing argument should fallback: handled=%v err=%v", handled, err)
	}
}

// TestVMTryExecuteLeafUpvalueAddSetReturnInCaller 验证 upvalue 自增闭包 caller-side 快路径。
//
// `local function inc() x = x + 1; return x end` 是 closure_upvalue benchmark 的热点形态；
// 快路径必须同步写回共享 upvalue cell，并且只覆盖 integer upvalue，其他类型回退完整 VM。
func TestVMTryExecuteLeafUpvalueAddSetReturnInCaller(t *testing.T) {
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{bytecode.IntegerConstant(1)},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpGetUpval, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpAdd, 0, 0, bytecode.RKAsK(0)),
			bytecode.CreateABC(bytecode.OpSetupVal, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpGetUpval, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpReturn, 0, 2, 0),
		},
	}
	cell := NewClosedUpvalueCell(IntegerValue(41))
	closure := NewLuaClosure(proto, []Value{IntegerValue(41)}, []*UpvalueCell{cell})
	if closure.LeafUpvalueAddSetReturn == nil {
		// 目标闭包必须预解析为 upvalue 自增写回形态。
		t.Fatalf("expected upvalue add-set metadata")
	}

	vm := NewVM(1)
	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 调用函数槽写入必须成功。
		t.Fatalf("set function register failed: %v", err)
	}
	handled, err := vm.TryExecuteLeafUpvalueAddSetReturnInCaller(closure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 0,
		ReturnCount:   1,
	})
	if err != nil || !handled {
		// integer upvalue 自增应在 caller 侧完成。
		t.Fatalf("upvalue add-set mismatch: handled=%v err=%v", handled, err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(IntegerValue(42)) {
		// 返回值必须直接写回函数槽。
		t.Fatalf("upvalue add-set result mismatch: value=%#v ok=%v", value, ok)
	}
	if !cell.Value().RawEqual(IntegerValue(42)) || !closure.Upvalues[0].RawEqual(IntegerValue(42)) {
		// 共享 cell 和 upvalue 快照都应同步更新，避免后续读取旧值。
		t.Fatalf("upvalue add-set did not update cell/snapshot: cell=%#v snapshot=%#v", cell.Value(), closure.Upvalues[0])
	}

	cell.Set(StringValue("41"))
	closure.Upvalues[0] = StringValue("41")
	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 重新写回函数槽用于非 integer 回退验证。
		t.Fatalf("reset function register failed: %v", err)
	}
	handled, err = vm.TryExecuteLeafUpvalueAddSetReturnInCaller(closure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 0,
		ReturnCount:   1,
	})
	if err != nil || handled {
		// 字符串数字必须回退完整 VM，保留 Lua 算术转换和错误语义。
		t.Fatalf("string upvalue should fallback: handled=%v err=%v", handled, err)
	}

	paramProto := &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpGetUpval, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpAdd, 1, 1, 0),
			bytecode.CreateABC(bytecode.OpSetupVal, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpGetUpval, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
	paramCell := NewClosedUpvalueCell(IntegerValue(10))
	paramClosure := NewLuaClosure(paramProto, []Value{IntegerValue(10)}, []*UpvalueCell{paramCell})
	if paramClosure.LeafUpvalueAddSetReturn == nil || !paramClosure.LeafUpvalueAddSetReturn.HasRegisterOperand {
		// `x = x + v; return x` 必须预解析为参数寄存器形态。
		t.Fatalf("expected upvalue add-set register metadata")
	}
	paramVM := NewVM(2)
	if err := paramVM.SetRegister(0, ReferenceValue(KindLuaClosure, paramClosure)); err != nil {
		// 调用函数槽写入必须成功。
		t.Fatalf("set param function register failed: %v", err)
	}
	if err := paramVM.SetRegister(1, IntegerValue(5)); err != nil {
		// 参数槽写入必须成功。
		t.Fatalf("set param register failed: %v", err)
	}
	handled, err = paramVM.TryExecuteLeafUpvalueAddSetReturnInCaller(paramClosure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 1,
		ReturnCount:   1,
	})
	if err != nil || !handled {
		// integer upvalue 加 integer 参数应在 caller 侧完成。
		t.Fatalf("upvalue param add-set mismatch: handled=%v err=%v", handled, err)
	}
	value, ok = paramVM.Register(0)
	if !ok || !value.RawEqual(IntegerValue(15)) {
		// 参数形态返回值必须直接写回函数槽。
		t.Fatalf("upvalue param add-set result mismatch: value=%#v ok=%v", value, ok)
	}
	if !paramCell.Value().RawEqual(IntegerValue(15)) || !paramClosure.Upvalues[0].RawEqual(IntegerValue(15)) {
		// 参数形态也必须同步 cell 和快照。
		t.Fatalf("upvalue param add-set did not update cell/snapshot: cell=%#v snapshot=%#v", paramCell.Value(), paramClosure.Upvalues[0])
	}

	if err := paramVM.SetRegister(0, ReferenceValue(KindLuaClosure, paramClosure)); err != nil {
		// 重新写回函数槽用于字符串参数回退验证。
		t.Fatalf("reset param function register failed: %v", err)
	}
	if err := paramVM.SetRegister(1, StringValue("5")); err != nil {
		// 字符串参数槽写入必须成功。
		t.Fatalf("reset param argument failed: %v", err)
	}
	handled, err = paramVM.TryExecuteLeafUpvalueAddSetReturnInCaller(paramClosure, &CallRequest{
		FunctionIndex: 0,
		ArgumentCount: 1,
		ReturnCount:   1,
	})
	if err != nil || handled {
		// 字符串数字参数必须回退完整 VM，保留 Lua 算术转换和元方法语义。
		t.Fatalf("string param should fallback: handled=%v err=%v", handled, err)
	}
}

// TestVMNewTable 验证 NEWTABLE 会创建新的 table 引用。
//
// Lua 5.3 NEWTABLE 的 B/C 预分配 hint 暂未生效，但创建空 table 的可观察语义必须正确。
func TestVMNewTable(t *testing.T) {
	vm := NewVM(1)
	if err := vm.Step(bytecode.CreateABC(bytecode.OpNewTable, 0, 1, 1)); err != nil {
		// NEWTABLE 写入合法目标寄存器必须成功。
		t.Fatalf("newtable failed: %v", err)
	}

	value, ok := vm.Register(0)
	if !ok || value.Kind != KindTable {
		// 目标寄存器必须保存 table 引用值。
		t.Fatalf("newtable value mismatch: value=%#v ok=%v", value, ok)
	}
	table, ok := value.Ref.(*Table)
	if !ok || table == nil {
		// table 引用负载必须是可用的 *Table。
		t.Fatalf("newtable ref mismatch: ref=%#v ok=%v", value.Ref, ok)
	}
	if table.ArraySize() != 0 || table.HashSize() != 0 {
		// 当前阶段 NEWTABLE 创建空 table，容量 hint 尚不产生可观察元素。
		t.Fatalf("newtable should be empty: array=%d hash=%d", table.ArraySize(), table.HashSize())
	}
}

// TestVMNewTablePreallocatesNumericForArray 验证强约束 numeric-for 数组写入会预留 table 数组容量。
//
// 该优化只改变 table 数组区底层 cap，不改变 NEWTABLE 后的空表可见语义；后续 SETTABLE 仍逐条执行。
func TestVMNewTablePreallocatesNumericForArray(t *testing.T) {
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{
			bytecode.IntegerConstant(1),
			bytecode.IntegerConstant(200000),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpNewTable, 0, 0, 0),
			bytecode.CreateABx(bytecode.OpLoadK, 1, 0),
			bytecode.CreateABx(bytecode.OpLoadK, 2, 1),
			bytecode.CreateABx(bytecode.OpLoadK, 3, 0),
			bytecode.CreateAsBx(bytecode.OpForPrep, 1, 1),
			bytecode.CreateABC(bytecode.OpSetTable, 0, 4, 4),
			bytecode.CreateAsBx(bytecode.OpForLoop, 1, -2),
		},
	}
	vm := NewVM(5)
	vm.BindPrototype(proto)
	vm.SetCurrentPC(0)
	if err := vm.Step(proto.Code[0]); err != nil {
		// NEWTABLE 写入合法目标寄存器必须成功。
		t.Fatalf("newtable failed: %v", err)
	}

	value, ok := vm.Register(0)
	if !ok || value.Kind != KindTable {
		// 目标寄存器必须保存 table 引用值。
		t.Fatalf("newtable value mismatch: value=%#v ok=%v", value, ok)
	}
	table, ok := value.Ref.(*Table)
	if !ok || table == nil {
		// table 引用负载必须是可用的 *Table。
		t.Fatalf("newtable ref mismatch: ref=%#v ok=%v", value.Ref, ok)
	}
	if table.ArraySize() != 0 || table.HashSize() != 0 {
		// 预留容量不能暴露为可见数组元素或 hash 元素。
		t.Fatalf("preallocated table should still be empty: array=%d hash=%d", table.ArraySize(), table.HashSize())
	}
	if cap(table.arrayValues) != 200000 {
		// 精确 t[i]=i 形态应按循环上界预留数组区容量。
		t.Fatalf("array capacity mismatch: got %d", cap(table.arrayValues))
	}
	if got := table.RawGetInteger(1); !got.IsNil() {
		// 预留槽位在写入前必须仍按 nil 读取。
		t.Fatalf("reserved slot should read nil: %#v", got)
	}
}

// TestVMNewTablePreallocRejectsNonIdentityValue 验证非 t[i]=i 形态不会触发表数组预留。
//
// value 表达式不是循环变量时，后续 SETTABLE 可能包含更复杂数据流；当前优化必须保守回退。
func TestVMNewTablePreallocRejectsNonIdentityValue(t *testing.T) {
	proto := &bytecode.Proto{
		Constants: []bytecode.Constant{
			bytecode.IntegerConstant(1),
			bytecode.IntegerConstant(200000),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpNewTable, 0, 0, 0),
			bytecode.CreateABx(bytecode.OpLoadK, 1, 0),
			bytecode.CreateABx(bytecode.OpLoadK, 2, 1),
			bytecode.CreateABx(bytecode.OpLoadK, 3, 0),
			bytecode.CreateAsBx(bytecode.OpForPrep, 1, 1),
			bytecode.CreateABC(bytecode.OpSetTable, 0, 4, 1),
			bytecode.CreateAsBx(bytecode.OpForLoop, 1, -2),
		},
	}
	vm := NewVM(5)
	vm.BindPrototype(proto)
	vm.SetCurrentPC(0)
	if err := vm.Step(proto.Code[0]); err != nil {
		// NEWTABLE 写入合法目标寄存器必须成功。
		t.Fatalf("newtable failed: %v", err)
	}

	value, ok := vm.Register(0)
	if !ok || value.Kind != KindTable {
		// 目标寄存器必须保存 table 引用值。
		t.Fatalf("newtable value mismatch: value=%#v ok=%v", value, ok)
	}
	table, ok := value.Ref.(*Table)
	if !ok || table == nil {
		// table 引用负载必须是可用的 *Table。
		t.Fatalf("newtable ref mismatch: ref=%#v ok=%v", value.Ref, ok)
	}
	if cap(table.arrayValues) != 0 {
		// 非精确 t[i]=i 数据流不能提前预留容量，避免误覆盖更复杂语义。
		t.Fatalf("unexpected array capacity: got %d", cap(table.arrayValues))
	}
}

// TestVMTableInstructionErrors 验证 table 指令的主要错误边界。
//
// 错误路径需要保持寄存器状态稳定，避免损坏字节码导致部分写入。
func TestVMTableInstructionErrors(t *testing.T) {
	vm := NewVMWithConstants(2, []bytecode.Constant{bytecode.StringConstant("key")})
	if err := vm.SetRegister(0, StringValue("not-table")); err != nil {
		// 测试准备阶段写入非 table 值必须成功。
		t.Fatalf("set non-table register failed: %v", err)
	}
	if err := vm.SetRegister(1, StringValue("keep")); err != nil {
		// 测试准备阶段写入目标寄存器必须成功。
		t.Fatalf("set target register failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpGetTable, 1, 0, bytecode.RKAsK(0)))
	if !errors.Is(err, ErrExpectedTable) {
		// 非 table 值执行 GETTABLE 必须返回 ErrExpectedTable。
		t.Fatalf("gettable non-table error mismatch: %v", err)
	}
	value, ok := vm.Register(1)
	if !ok || !value.RawEqual(StringValue("keep")) {
		// GETTABLE 失败不能覆盖目标寄存器。
		t.Fatalf("gettable should keep target: value=%#v ok=%v", value, ok)
	}

	err = vm.Step(bytecode.CreateABC(bytecode.OpSetTable, 0, bytecode.RKAsK(0), bytecode.RKAsK(0)))
	if !errors.Is(err, ErrExpectedTable) {
		// 非 table 值执行 SETTABLE 必须返回 ErrExpectedTable。
		t.Fatalf("settable non-table error mismatch: %v", err)
	}

	table := NewTable()
	if err := vm.SetRegister(0, ReferenceValue(KindTable, table)); err != nil {
		// 测试准备阶段恢复 table 寄存器必须成功。
		t.Fatalf("set table register failed: %v", err)
	}
	err = vm.Step(bytecode.CreateABC(bytecode.OpGetTable, 1, 0, bytecode.RKAsK(7)))
	if !errors.Is(err, ErrConstantOutOfRange) {
		// RK 常量索引越界必须返回 ErrConstantOutOfRange。
		t.Fatalf("gettable constant out of range error mismatch: %v", err)
	}
	value, ok = vm.Register(1)
	if !ok || !value.RawEqual(StringValue("keep")) {
		// RK 读取失败不能覆盖目标寄存器。
		t.Fatalf("gettable bad rk should keep target: value=%#v ok=%v", value, ok)
	}
}

// TestVMGetTabUpAndSetTabUp 验证 GETTABUP 与 SETTABUP 会访问 upvalue table。
//
// Lua 5.3 使用 GETTABUP/SETTABUP 访问 `_ENV` 等 upvalue table，当前最小 VM 先覆盖
// table 型 upvalue 的普通读写语义。
func TestVMGetTabUpAndSetTabUp(t *testing.T) {
	envTable := NewTable()
	envTable.RawSetString("name", StringValue("lua"))
	vm := NewVMWithConstantsAndUpvalues(2, []bytecode.Constant{
		bytecode.StringConstant("name"),
		bytecode.StringConstant("version"),
		bytecode.IntegerConstant(53),
	}, []Value{ReferenceValue(KindTable, envTable)})

	if err := vm.Step(bytecode.CreateABC(bytecode.OpGetTabUp, 0, 0, bytecode.RKAsK(0))); err != nil {
		// GETTABUP 使用常量 key 读取 upvalue table 必须成功。
		t.Fatalf("gettabup failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("lua")) {
		// 目标寄存器必须获得 upvalue table 中的字段值。
		t.Fatalf("gettabup value mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpSetTabUp, 0, bytecode.RKAsK(1), bytecode.RKAsK(2))); err != nil {
		// SETTABUP 使用常量 key/value 写入 upvalue table 必须成功。
		t.Fatalf("settabup failed: %v", err)
	}
	value = envTable.RawGetString("version")
	if !value.RawEqual(IntegerValue(53)) {
		// upvalue table 必须保存 SETTABUP 写入的值。
		t.Fatalf("settabup value mismatch: value=%#v", value)
	}
	if err := vm.SetRegister(1, IntegerValue(54)); err != nil {
		// 测试寄存器 value 写入路径前必须先准备源寄存器。
		t.Fatalf("set source register failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpSetTabUp, 0, bytecode.RKAsK(1), 1)); err != nil {
		// SETTABUP 使用常量 key 和寄存器 value 写入 upvalue table 必须成功。
		t.Fatalf("settabup register value failed: %v", err)
	}
	value = envTable.RawGetString("version")
	if !value.RawEqual(IntegerValue(54)) {
		// upvalue table 必须保存寄存器 value 写入的值。
		t.Fatalf("settabup register value mismatch: value=%#v", value)
	}
}

// TestVMGetTabUpAndSetTabUpErrors 验证 upvalue table 指令的主要错误边界。
//
// 损坏 chunk 可能访问不存在的 upvalue 或把非 table upvalue 当作环境表，VM 必须明确拒绝。
func TestVMGetTabUpAndSetTabUpErrors(t *testing.T) {
	vm := NewVMWithConstantsAndUpvalues(1, []bytecode.Constant{bytecode.StringConstant("key")}, []Value{StringValue("not-table")})
	if err := vm.SetRegister(0, StringValue("keep")); err != nil {
		// 测试准备阶段写入目标寄存器必须成功。
		t.Fatalf("set target register failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpGetTabUp, 0, 3, bytecode.RKAsK(0)))
	if !errors.Is(err, ErrUpvalueOutOfRange) {
		// GETTABUP 访问不存在 upvalue 必须返回 ErrUpvalueOutOfRange。
		t.Fatalf("gettabup upvalue out of range error mismatch: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("keep")) {
		// GETTABUP 失败不能覆盖目标寄存器。
		t.Fatalf("gettabup should keep target: value=%#v ok=%v", value, ok)
	}

	err = vm.Step(bytecode.CreateABC(bytecode.OpGetTabUp, 0, 0, bytecode.RKAsK(0)))
	if !errors.Is(err, ErrExpectedTable) {
		// 非 table upvalue 执行 GETTABUP 必须返回 ErrExpectedTable。
		t.Fatalf("gettabup non-table error mismatch: %v", err)
	}

	err = vm.Step(bytecode.CreateABC(bytecode.OpSetTabUp, 0, bytecode.RKAsK(0), bytecode.RKAsK(0)))
	if !errors.Is(err, ErrExpectedTable) {
		// 非 table upvalue 执行 SETTABUP 必须返回 ErrExpectedTable。
		t.Fatalf("settabup non-table error mismatch: %v", err)
	}
}

// TestVMSelf 验证 SELF 会同时写入方法和接收者。
//
// Lua 冒号调用 `obj:method()` 依赖 SELF 把 method 放入 R(A)，把接收者放入 R(A+1)。
func TestVMSelf(t *testing.T) {
	receiver := NewTable()
	methodValue := StringValue("method")
	receiver.RawSetString("call", methodValue)
	vm := NewVMWithConstants(3, []bytecode.Constant{bytecode.StringConstant("call")})
	receiverValue := ReferenceValue(KindTable, receiver)
	if err := vm.SetRegister(2, receiverValue); err != nil {
		// 测试准备阶段写入接收者寄存器必须成功。
		t.Fatalf("set receiver register failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpSelf, 0, 2, bytecode.RKAsK(0))); err != nil {
		// SELF 使用常量 method key 必须成功。
		t.Fatalf("self failed: %v", err)
	}
	methodRegister, methodOK := vm.Register(0)
	receiverRegister, receiverOK := vm.Register(1)
	if !methodOK || !methodRegister.RawEqual(methodValue) {
		// R(A) 必须保存从接收者 table 读取到的方法值。
		t.Fatalf("self method mismatch: value=%#v ok=%v", methodRegister, methodOK)
	}
	if !receiverOK || receiverRegister.Kind != KindTable || receiverRegister.Ref != receiver {
		// R(A+1) 必须保存原接收者 identity。
		t.Fatalf("self receiver mismatch: value=%#v ok=%v", receiverRegister, receiverOK)
	}
}

// TestVMSelfUsesUserdataIndexMetatable 验证 SELF 可通过 userdata `__index` 准备冒号调用。
//
// 官方测试中的 `io.stderr:write(...)` 会先对 file userdata 执行 SELF；该路径必须同时取到
// 方法值并把原 userdata identity 放入 self 寄存器。
func TestVMSelfUsesUserdataIndexMetatable(t *testing.T) {
	methodValue := StringValue("method")
	methods := NewTable()
	methods.RawSetString("write", methodValue)
	metatable := NewTable()
	metatable.RawSetString(tableIndexMetamethodKey, ReferenceValue(KindTable, methods))
	userdata := NewUserdata("stderr")
	if err := userdata.SetMetatable(metatable); err != nil {
		// 测试准备阶段绑定 userdata 元表必须成功。
		t.Fatalf("set userdata metatable failed: %v", err)
	}
	receiverValue := userdata.Value()

	vm := NewVMWithConstants(3, []bytecode.Constant{bytecode.StringConstant("write")})
	if err := vm.SetRegister(2, receiverValue); err != nil {
		// 测试准备阶段写入 userdata 接收者必须成功。
		t.Fatalf("set userdata receiver failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpSelf, 0, 2, bytecode.RKAsK(0))); err != nil {
		// SELF 通过 userdata __index table 读取方法必须成功。
		t.Fatalf("self userdata index failed: %v", err)
	}

	methodRegister, methodOK := vm.Register(0)
	receiverRegister, receiverOK := vm.Register(1)
	if !methodOK || !methodRegister.RawEqual(methodValue) {
		// R(A) 必须保存从 userdata 元表读取到的方法。
		t.Fatalf("userdata self method mismatch: value=%#v ok=%v", methodRegister, methodOK)
	}
	if !receiverOK || receiverRegister.Kind != KindUserdata || receiverRegister.Ref != userdata {
		// R(A+1) 必须保存原 userdata identity。
		t.Fatalf("userdata self receiver mismatch: value=%#v ok=%v", receiverRegister, receiverOK)
	}
}

// TestVMSelfErrors 验证 SELF 错误路径不会覆盖目标寄存器。
//
// SELF 同时写两个寄存器，因此必须先完成所有校验和方法读取，再写入目标。
func TestVMSelfErrors(t *testing.T) {
	vm := NewVMWithConstants(2, []bytecode.Constant{bytecode.StringConstant("call")})
	if err := vm.SetRegister(0, StringValue("keep-method")); err != nil {
		// 测试准备阶段写入方法目标寄存器必须成功。
		t.Fatalf("set method target failed: %v", err)
	}
	if err := vm.SetRegister(1, StringValue("keep-receiver")); err != nil {
		// 测试准备阶段写入接收者目标寄存器必须成功。
		t.Fatalf("set receiver target failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpSelf, 0, 1, bytecode.RKAsK(0)))
	if !errors.Is(err, ErrExpectedTable) {
		// 非 table 接收者执行 SELF 必须返回 ErrExpectedTable。
		t.Fatalf("self non-table error mismatch: %v", err)
	}
	methodValue, methodOK := vm.Register(0)
	receiverValue, receiverOK := vm.Register(1)
	if !methodOK || !receiverOK || !methodValue.RawEqual(StringValue("keep-method")) || !receiverValue.RawEqual(StringValue("keep-receiver")) {
		// SELF 失败不能覆盖 R(A) 或 R(A+1)。
		t.Fatalf("self should keep targets: method=%#v ok=%v receiver=%#v ok=%v", methodValue, methodOK, receiverValue, receiverOK)
	}

	err = vm.Step(bytecode.CreateABC(bytecode.OpSelf, 1, 1, bytecode.RKAsK(0)))
	if !errors.Is(err, ErrRegisterOutOfRange) {
		// A+1 超出寄存器窗口必须返回 ErrRegisterOutOfRange。
		t.Fatalf("self target out of range error mismatch: %v", err)
	}
}

// TestVMBinaryArithmeticInstructions 验证二元算术指令的基础语义。
//
// 本测试覆盖 ADD/SUB/MUL/MOD/POW/DIV/IDIV，包含 integer 快速路径、float 路径和字符串
// 数字转换路径。
func TestVMBinaryArithmeticInstructions(t *testing.T) {
	tests := []struct {
		name          string
		opCode        bytecode.OpCode
		constants     []bytecode.Constant
		expectedValue Value
	}{
		{name: "add integer", opCode: bytecode.OpAdd, constants: []bytecode.Constant{bytecode.IntegerConstant(7), bytecode.IntegerConstant(5)}, expectedValue: IntegerValue(12)},
		{name: "sub integer", opCode: bytecode.OpSub, constants: []bytecode.Constant{bytecode.IntegerConstant(7), bytecode.IntegerConstant(5)}, expectedValue: IntegerValue(2)},
		{name: "mul integer", opCode: bytecode.OpMul, constants: []bytecode.Constant{bytecode.IntegerConstant(7), bytecode.IntegerConstant(5)}, expectedValue: IntegerValue(35)},
		{name: "mod integer", opCode: bytecode.OpMod, constants: []bytecode.Constant{bytecode.IntegerConstant(-7), bytecode.IntegerConstant(5)}, expectedValue: IntegerValue(3)},
		{name: "mod min integer by minus one", opCode: bytecode.OpMod, constants: []bytecode.Constant{bytecode.IntegerConstant(math.MinInt64), bytecode.IntegerConstant(-1)}, expectedValue: IntegerValue(0)},
		{name: "mod positive by positive infinity", opCode: bytecode.OpMod, constants: []bytecode.Constant{bytecode.NumberConstant(1), bytecode.NumberConstant(math.Inf(1))}, expectedValue: NumberValue(1)},
		{name: "mod positive by negative infinity", opCode: bytecode.OpMod, constants: []bytecode.Constant{bytecode.NumberConstant(1), bytecode.NumberConstant(math.Inf(-1))}, expectedValue: NumberValue(math.Inf(-1))},
		{name: "mod negative by positive infinity", opCode: bytecode.OpMod, constants: []bytecode.Constant{bytecode.NumberConstant(-1), bytecode.NumberConstant(math.Inf(1))}, expectedValue: NumberValue(math.Inf(1))},
		{name: "mod negative by negative infinity", opCode: bytecode.OpMod, constants: []bytecode.Constant{bytecode.NumberConstant(-1), bytecode.NumberConstant(math.Inf(-1))}, expectedValue: NumberValue(-1)},
		{name: "pow number", opCode: bytecode.OpPow, constants: []bytecode.Constant{bytecode.IntegerConstant(2), bytecode.IntegerConstant(3)}, expectedValue: NumberValue(8)},
		{name: "div number", opCode: bytecode.OpDiv, constants: []bytecode.Constant{bytecode.IntegerConstant(7), bytecode.IntegerConstant(2)}, expectedValue: NumberValue(3.5)},
		{name: "div zero number", opCode: bytecode.OpDiv, constants: []bytecode.Constant{bytecode.IntegerConstant(1), bytecode.IntegerConstant(0)}, expectedValue: NumberValue(math.Inf(1))},
		{name: "idiv integer", opCode: bytecode.OpIDiv, constants: []bytecode.Constant{bytecode.IntegerConstant(-7), bytecode.IntegerConstant(2)}, expectedValue: IntegerValue(-4)},
		{name: "idiv min integer by minus one", opCode: bytecode.OpIDiv, constants: []bytecode.Constant{bytecode.IntegerConstant(math.MinInt64), bytecode.IntegerConstant(-1)}, expectedValue: IntegerValue(math.MinInt64)},
		{name: "add string number", opCode: bytecode.OpAdd, constants: []bytecode.Constant{bytecode.StringConstant("1.5"), bytecode.StringConstant("2.25")}, expectedValue: NumberValue(3.75)},
	}

	for _, testCase := range tests {
		// 每个算术 opcode 独立构造 VM，避免寄存器状态在用例间相互污染。
		vm := NewVMWithConstants(1, testCase.constants)
		instruction := bytecode.CreateABC(testCase.opCode, 0, bytecode.RKAsK(0), bytecode.RKAsK(1))
		if err := vm.Step(instruction); err != nil {
			// 合法算术指令必须执行成功。
			t.Fatalf("%s failed: %v", testCase.name, err)
		}
		value, ok := vm.Register(0)
		if !ok || !value.RawEqual(testCase.expectedValue) {
			// 目标寄存器必须保存该算术 opcode 的预期结果。
			t.Fatalf("%s value mismatch: value=%#v ok=%v", testCase.name, value, ok)
		}
	}
}

// TestVMIntegerModIDivCache 验证 MOD/IDIV 的 integer inline cache。
//
// 缓存命中必须保持 Lua 5.3 的 floor-mod/floor-division 语义；运行期除数变为 0 时仍返回
// 原始 Lua 错误并保持目标寄存器不被覆盖。
func TestVMIntegerModIDivCache(t *testing.T) {
	tests := []struct {
		name           string
		opCode         bytecode.OpCode
		left           int64
		right          int64
		expectedFirst  Value
		expectedSecond Value
	}{
		{
			name:           "mod cached",
			opCode:         bytecode.OpMod,
			left:           -7,
			right:          5,
			expectedFirst:  IntegerValue(3),
			expectedSecond: IntegerValue(1),
		},
		{
			name:           "idiv cached",
			opCode:         bytecode.OpIDiv,
			left:           -7,
			right:          2,
			expectedFirst:  IntegerValue(-4),
			expectedSecond: IntegerValue(-5),
		},
	}

	for _, testCase := range tests {
		// 每个 opcode 使用独立 Proto，确保 PC 缓存只观察当前指令。
		proto := &bytecode.Proto{Code: []bytecode.Instruction{bytecode.CreateABC(testCase.opCode, 0, 1, 2)}}
		vm := NewVM(3)
		vm.BindPrototype(proto)
		if err := vm.SetRegister(1, IntegerValue(testCase.left)); err != nil {
			// 测试准备阶段必须能写入左操作数。
			t.Fatalf("%s set left failed: %v", testCase.name, err)
		}
		if err := vm.SetRegister(2, IntegerValue(testCase.right)); err != nil {
			// 测试准备阶段必须能写入右操作数。
			t.Fatalf("%s set right failed: %v", testCase.name, err)
		}
		vm.currentPC = 0
		if err := vm.Step(proto.Code[0]); err != nil {
			// 首次执行会建立 integer 缓存，不应失败。
			t.Fatalf("%s first step failed: %v", testCase.name, err)
		}
		if value, ok := vm.Register(0); !ok || !value.RawEqual(testCase.expectedFirst) {
			// 首次结果必须符合 Lua 5.3 整数语义。
			t.Fatalf("%s first value=%#v ok=%v", testCase.name, value, ok)
		}

		if err := vm.SetRegister(1, IntegerValue(testCase.left-2)); err != nil {
			// 第二轮更新左操作数，用于确认缓存读取的是当前寄存器值。
			t.Fatalf("%s update left failed: %v", testCase.name, err)
		}
		vm.currentPC = 0
		if err := vm.Step(proto.Code[0]); err != nil {
			// 第二次同 PC 执行应命中缓存并保持语义。
			t.Fatalf("%s cached step failed: %v", testCase.name, err)
		}
		if value, ok := vm.Register(0); !ok || !value.RawEqual(testCase.expectedSecond) {
			// 缓存命中不能复用旧值，必须读取当前寄存器。
			t.Fatalf("%s cached value=%#v ok=%v", testCase.name, value, ok)
		}

		if err := vm.SetRegister(0, StringValue("keep")); err != nil {
			// 零除前设置哨兵值，用于确认错误路径不覆盖目标寄存器。
			t.Fatalf("%s set sentinel failed: %v", testCase.name, err)
		}
		if err := vm.SetRegister(2, IntegerValue(0)); err != nil {
			// 更新右操作数为 0 以覆盖缓存命中错误路径。
			t.Fatalf("%s set zero divisor failed: %v", testCase.name, err)
		}
		vm.currentPC = 0
		if err := vm.Step(proto.Code[0]); !errors.Is(err, ErrDivisionByZero) {
			// 缓存命中也必须保留 Lua 的零除错误。
			t.Fatalf("%s zero divisor error=%v", testCase.name, err)
		}
		if value, ok := vm.Register(0); !ok || !value.RawEqual(StringValue("keep")) {
			// 零除错误路径不能写入部分结果。
			t.Fatalf("%s zero divisor value=%#v ok=%v", testCase.name, value, ok)
		}
	}
}

// TestVMMulNumberConstantFastPath 验证 MUL 的 number 常量窄快路径。
//
// 该快路径服务混合算术循环中的 `number * Knum` 形态；必须支持常量左右两侧，并在字符串数字
// 场景回退完整 Lua 算术转换路径。
func TestVMMulNumberConstantFastPath(t *testing.T) {
	tests := []struct {
		name          string
		instruction   bytecode.Instruction
		registerValue Value
		expectedValue Value
	}{
		{
			name:          "register times number constant",
			instruction:   bytecode.CreateABC(bytecode.OpMul, 0, 1, bytecode.RKAsK(0)),
			registerValue: NumberValue(2.5),
			expectedValue: NumberValue(10),
		},
		{
			name:          "number constant times register",
			instruction:   bytecode.CreateABC(bytecode.OpMul, 0, bytecode.RKAsK(0), 1),
			registerValue: IntegerValue(3),
			expectedValue: NumberValue(12),
		},
		{
			name:          "string number falls back",
			instruction:   bytecode.CreateABC(bytecode.OpMul, 0, 1, bytecode.RKAsK(0)),
			registerValue: StringValue("3"),
			expectedValue: NumberValue(12),
		},
	}

	for _, testCase := range tests {
		// 每个形态使用独立 VM，避免寄存器和值类型缓存影响后续断言。
		vm := NewVMWithConstants(2, []bytecode.Constant{bytecode.NumberConstant(4)})
		if err := vm.SetRegister(1, testCase.registerValue); err != nil {
			// 测试准备阶段必须能写入待乘寄存器。
			t.Fatalf("%s set register failed: %v", testCase.name, err)
		}
		if err := vm.Step(testCase.instruction); err != nil {
			// 合法 number 常量乘法不应失败。
			t.Fatalf("%s failed: %v", testCase.name, err)
		}
		value, ok := vm.Register(0)
		if !ok || !value.RawEqual(testCase.expectedValue) {
			// 目标寄存器必须保存乘法结果。
			t.Fatalf("%s value mismatch: value=%#v ok=%v", testCase.name, value, ok)
		}
	}
}

// TestVMAddNativeNumberFastPath 验证 ADD 的原生 number 窄快路径。
//
// 快路径只覆盖至少一侧为真实 number 的加法；双 integer 结果仍应保持 integer，字符串数字
// 继续回退完整算术转换路径。
func TestVMAddNativeNumberFastPath(t *testing.T) {
	tests := []struct {
		name          string
		leftValue     Value
		rightValue    Value
		expectedValue Value
	}{
		{
			name:          "number plus integer",
			leftValue:     NumberValue(1.5),
			rightValue:    IntegerValue(2),
			expectedValue: NumberValue(3.5),
		},
		{
			name:          "number plus number",
			leftValue:     NumberValue(1.25),
			rightValue:    NumberValue(2.75),
			expectedValue: NumberValue(4),
		},
		{
			name:          "integer plus integer keeps integer",
			leftValue:     IntegerValue(1),
			rightValue:    IntegerValue(2),
			expectedValue: IntegerValue(3),
		},
		{
			name:          "string number falls back",
			leftValue:     StringValue("1.5"),
			rightValue:    StringValue("2.25"),
			expectedValue: NumberValue(3.75),
		},
	}

	for _, testCase := range tests {
		// 每个形态独立构造寄存器窗口，避免整数缓存影响后续用例。
		vm := NewVM(3)
		if err := vm.SetRegister(1, testCase.leftValue); err != nil {
			// 测试准备阶段必须能写入左操作数。
			t.Fatalf("%s set left register failed: %v", testCase.name, err)
		}
		if err := vm.SetRegister(2, testCase.rightValue); err != nil {
			// 测试准备阶段必须能写入右操作数。
			t.Fatalf("%s set right register failed: %v", testCase.name, err)
		}
		if err := vm.Step(bytecode.CreateABC(bytecode.OpAdd, 0, 1, 2)); err != nil {
			// 合法加法不应失败。
			t.Fatalf("%s failed: %v", testCase.name, err)
		}
		value, ok := vm.Register(0)
		if !ok || !value.RawEqual(testCase.expectedValue) {
			// 目标寄存器必须保存加法结果。
			t.Fatalf("%s value mismatch: value=%#v ok=%v", testCase.name, value, ok)
		}
	}
}

// TestVMAddNativeNumberCacheFallback 验证寄存器 number ADD 缓存命中与类型变化回退。
//
// 同一 PC 首次执行 number+number 会建立缓存；第二次应命中缓存并继续返回 number。若后续
// 操作数变为双 integer，缓存必须失效并回到 integer ADD 语义。
func TestVMAddNativeNumberCacheFallback(t *testing.T) {
	// 使用带 Proto 的 VM 启用按 PC 的算术缓存。
	proto := bytecode.NewProto("number-add-cache")
	proto.Code = []bytecode.Instruction{bytecode.CreateABC(bytecode.OpAdd, 0, 1, 2)}
	vm := NewVMWithPrototypeData(3, nil, nil, nil, nil)
	vm.BindPrototype(proto)
	vm.SetCurrentPC(0)
	if err := vm.SetRegister(1, NumberValue(1.25)); err != nil {
		// 测试准备阶段必须能写入左 number 操作数。
		t.Fatalf("set first left failed: %v", err)
	}
	if err := vm.SetRegister(2, NumberValue(2.75)); err != nil {
		// 测试准备阶段必须能写入右 number 操作数。
		t.Fatalf("set first right failed: %v", err)
	}
	if err := vm.Step(proto.Code[0]); err != nil {
		// 首次 number ADD 不应失败。
		t.Fatalf("first add failed: %v", err)
	}
	firstValue, firstOK := vm.Register(0)
	if !firstOK || !firstValue.RawEqual(NumberValue(4)) {
		// 首次执行必须得到 number 结果。
		t.Fatalf("first value mismatch: value=%#v ok=%v", firstValue, firstOK)
	}

	vm.SetCurrentPC(0)
	if err := vm.SetRegister(1, NumberValue(10.5)); err != nil {
		// 第二次执行复用同一 PC，左操作数仍为 number。
		t.Fatalf("set second left failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(2)); err != nil {
		// 第二次执行右操作数允许是 integer，结果仍应为 number。
		t.Fatalf("set second right failed: %v", err)
	}
	if err := vm.Step(proto.Code[0]); err != nil {
		// 缓存命中路径不应失败。
		t.Fatalf("second add failed: %v", err)
	}
	secondValue, secondOK := vm.Register(0)
	if !secondOK || !secondValue.RawEqual(NumberValue(12.5)) {
		// number cache 命中后必须保留 Lua number 结果。
		t.Fatalf("second value mismatch: value=%#v ok=%v", secondValue, secondOK)
	}

	vm.SetCurrentPC(0)
	if err := vm.SetRegister(1, IntegerValue(3)); err != nil {
		// 类型变化为双 integer 后必须允许缓存失效。
		t.Fatalf("set third left failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(4)); err != nil {
		// 右操作数同样切换为 integer。
		t.Fatalf("set third right failed: %v", err)
	}
	if err := vm.Step(proto.Code[0]); err != nil {
		// 双 integer 回退路径不应失败。
		t.Fatalf("third add failed: %v", err)
	}
	thirdValue, thirdOK := vm.Register(0)
	if !thirdOK || !thirdValue.RawEqual(IntegerValue(7)) {
		// 双 integer 必须回到 integer ADD 结果，而不是 number 结果。
		t.Fatalf("third value mismatch: value=%#v ok=%v", thirdValue, thirdOK)
	}
}

// TestVMDivNativeNumberFastPath 验证 DIV 的原生 number/integer 窄快路径。
//
// Lua 5.3 的 `/` 总是返回 float number；快路径必须覆盖原生数值，并让字符串数字继续走
// 完整算术转换路径。
func TestVMDivNativeNumberFastPath(t *testing.T) {
	tests := []struct {
		name          string
		leftValue     Value
		rightValue    Value
		expectedValue Value
	}{
		{
			name:          "integer division returns number",
			leftValue:     IntegerValue(7),
			rightValue:    IntegerValue(2),
			expectedValue: NumberValue(3.5),
		},
		{
			name:          "number division",
			leftValue:     NumberValue(9),
			rightValue:    NumberValue(4.5),
			expectedValue: NumberValue(2),
		},
		{
			name:          "string number falls back",
			leftValue:     StringValue("8"),
			rightValue:    StringValue("2"),
			expectedValue: NumberValue(4),
		},
	}

	for _, testCase := range tests {
		// 每个形态独立构造寄存器窗口，避免前一个用例的目标寄存器影响结果。
		vm := NewVM(3)
		if err := vm.SetRegister(1, testCase.leftValue); err != nil {
			// 测试准备阶段必须能写入左操作数。
			t.Fatalf("%s set left register failed: %v", testCase.name, err)
		}
		if err := vm.SetRegister(2, testCase.rightValue); err != nil {
			// 测试准备阶段必须能写入右操作数。
			t.Fatalf("%s set right register failed: %v", testCase.name, err)
		}
		if err := vm.Step(bytecode.CreateABC(bytecode.OpDiv, 0, 1, 2)); err != nil {
			// 合法除法不应失败。
			t.Fatalf("%s failed: %v", testCase.name, err)
		}
		value, ok := vm.Register(0)
		if !ok || !value.RawEqual(testCase.expectedValue) {
			// 目标寄存器必须保存除法结果。
			t.Fatalf("%s value mismatch: value=%#v ok=%v", testCase.name, value, ok)
		}
	}
}

// TestVMDivNativeNumberCacheFallback 验证寄存器原生数值 DIV 缓存命中与回退。
//
// 同一 PC 首次执行 integer/integer DIV 会建立缓存；第二次应命中缓存并继续返回 number。若后续
// 操作数变为字符串数字，缓存必须失效并回到完整 Lua 数字字符串转换语义。
func TestVMDivNativeNumberCacheFallback(t *testing.T) {
	// 使用带 Proto 的 VM 启用按 PC 的算术缓存。
	proto := bytecode.NewProto("number-div-cache")
	proto.Code = []bytecode.Instruction{bytecode.CreateABC(bytecode.OpDiv, 0, 1, 2)}
	vm := NewVMWithPrototypeData(3, nil, nil, nil, nil)
	vm.BindPrototype(proto)
	vm.SetCurrentPC(0)
	if err := vm.SetRegister(1, IntegerValue(7)); err != nil {
		// 测试准备阶段必须能写入左 integer 操作数。
		t.Fatalf("set first left failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(2)); err != nil {
		// 测试准备阶段必须能写入右 integer 操作数。
		t.Fatalf("set first right failed: %v", err)
	}
	if err := vm.Step(proto.Code[0]); err != nil {
		// 首次 DIV 不应失败。
		t.Fatalf("first div failed: %v", err)
	}
	firstValue, firstOK := vm.Register(0)
	if !firstOK || !firstValue.RawEqual(NumberValue(3.5)) {
		// integer / integer 也必须得到 Lua number 结果。
		t.Fatalf("first value mismatch: value=%#v ok=%v", firstValue, firstOK)
	}

	vm.SetCurrentPC(0)
	if err := vm.SetRegister(1, NumberValue(9)); err != nil {
		// 第二次执行复用同一 PC，左操作数切换为 number。
		t.Fatalf("set second left failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(3)); err != nil {
		// 右操作数保持 integer。
		t.Fatalf("set second right failed: %v", err)
	}
	if err := vm.Step(proto.Code[0]); err != nil {
		// 缓存命中路径不应失败。
		t.Fatalf("second div failed: %v", err)
	}
	secondValue, secondOK := vm.Register(0)
	if !secondOK || !secondValue.RawEqual(NumberValue(3)) {
		// DIV cache 命中后必须保留 Lua number 结果。
		t.Fatalf("second value mismatch: value=%#v ok=%v", secondValue, secondOK)
	}

	vm.SetCurrentPC(0)
	if err := vm.SetRegister(1, StringValue("8")); err != nil {
		// 字符串数字需要触发缓存回退。
		t.Fatalf("set third left failed: %v", err)
	}
	if err := vm.SetRegister(2, StringValue("2")); err != nil {
		// 右操作数同样使用字符串数字。
		t.Fatalf("set third right failed: %v", err)
	}
	if err := vm.Step(proto.Code[0]); err != nil {
		// 完整路径应支持字符串数字转换。
		t.Fatalf("third div failed: %v", err)
	}
	thirdValue, thirdOK := vm.Register(0)
	if !thirdOK || !thirdValue.RawEqual(NumberValue(4)) {
		// 字符串数字回退路径必须保持 Lua 5.3 算术转换语义。
		t.Fatalf("third value mismatch: value=%#v ok=%v", thirdValue, thirdOK)
	}
}

// TestVMDecodedInstructionCacheFollowsBoundProto 验证预解码缓存跟随当前绑定 Proto。
//
// VM 池复用时可能先后执行不同 Proto；预解码数组必须按 Proto 边界重建，避免相同 PC 误读旧
// 指令字段或 RK 常量形态。nil Proto 绑定必须清理缓存，保持手写 VM 单测兼容。
func TestVMDecodedInstructionCacheFollowsBoundProto(t *testing.T) {
	// 构造带 integer 常量和 FORLOOP 的 Proto，覆盖 RK、A/B/C、Bx、sBx 和 Ax 字段预解码。
	proto := bytecode.NewProto("decoded")
	proto.Constants = []bytecode.Constant{bytecode.IntegerConstant(3)}
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpAdd, 2, 1, bytecode.RKAsK(0)),
		bytecode.CreateAsBx(bytecode.OpForLoop, 0, -1),
		bytecode.CreateABC(bytecode.OpAdd, 0, 9, 0),
	}
	vm := NewVMWithPrototypeData(4, proto.Constants, nil, nil, nil)
	vm.BindPrototype(proto)

	decodedAdd, ok := vm.decodedInstructionAt(0)
	if !ok {
		// 当前 Proto 的第一条指令必须能完成预解码。
		t.Fatalf("decoded add missing")
	}
	if decodedAdd.Instruction != proto.Code[0] || decodedAdd.OpCode != bytecode.OpAdd || decodedAdd.A != 2 || decodedAdd.B != 1 || decodedAdd.C != bytecode.RKAsK(0) {
		// 预解码字段必须与原始 Instruction 位字段一致。
		t.Fatalf("decoded add fields mismatch: %+v", decodedAdd)
	}
	if decodedAdd.BOperand.Constant || decodedAdd.BOperand.Index != 1 {
		// B 字段是寄存器操作数，只应记录寄存器下标。
		t.Fatalf("decoded B operand mismatch: %+v", decodedAdd.BOperand)
	}
	if !decodedAdd.COperand.Constant || decodedAdd.COperand.Index != 0 || !decodedAdd.COperand.IntegerConstantOK || decodedAdd.COperand.IntegerConstant != 3 {
		// C 字段是 integer 常量，必须缓存常量下标和值。
		t.Fatalf("decoded C operand mismatch: %+v", decodedAdd.COperand)
	}

	decodedForLoop, ok := vm.decodedInstructionAt(1)
	if !ok {
		// 第二条 FORLOOP 指令也应位于预解码数组中。
		t.Fatalf("decoded forloop missing")
	}
	if decodedForLoop.OpCode != bytecode.OpForLoop || decodedForLoop.SBx != -1 {
		// sBx 必须保留有符号跳转偏移，供后续 superinstruction guard 使用。
		t.Fatalf("decoded forloop mismatch: %+v", decodedForLoop)
	}
	decodedWideRegister, ok := vm.decodedInstructionAt(2)
	if !ok {
		// 第三条宽寄存器指令也应位于预解码数组中。
		t.Fatalf("decoded wide register add missing")
	}
	if decodedWideRegister.BOperand.Constant || decodedWideRegister.BOperand.Index != 9 {
		// 预解码阶段不能根据当前 VM 寄存器窗口提前拒绝寄存器下标，只记录形态供执行期检查。
		t.Fatalf("decoded wide register operand mismatch: %+v", decodedWideRegister.BOperand)
	}

	otherProto := bytecode.NewProto("decoded-other")
	otherProto.Code = []bytecode.Instruction{bytecode.CreateABC(bytecode.OpMove, 0, 1, 0)}
	vm.BindPrototype(otherProto)
	decodedMove, ok := vm.decodedInstructionAt(0)
	if !ok {
		// 切换 Proto 后应为新 Proto 重新构建预解码。
		t.Fatalf("decoded move missing")
	}
	if vm.decodedInstructionProto != otherProto || decodedMove.OpCode != bytecode.OpMove || decodedMove.Instruction != otherProto.Code[0] {
		// 预解码缓存必须绑定新 Proto，不能继续引用旧 Proto 数据。
		t.Fatalf("decoded proto switch mismatch: proto=%p decoded=%+v", vm.decodedInstructionProto, decodedMove)
	}

	vm.BindPrototype(nil)
	if decoded, ok := vm.decodedInstructionAt(0); ok || vm.decodedInstructionProto != nil || vm.decodedInstructions != nil {
		// nil Proto 绑定必须清空缓存，确保手工 Step 路径没有陈旧预解码状态。
		t.Fatalf("decoded cache should be cleared: decoded=%+v ok=%v proto=%p len=%d", decoded, ok, vm.decodedInstructionProto, len(vm.decodedInstructions))
	}
}

// TestVMDecodedInstructionHandlesStrippedAndInvalidRK 验证 stripped Proto 和异常 RK 的预解码安全性。
//
// stripped chunk 仍保留 Code/Constants 但缺少调试信息；预解码不得依赖 LineInfo 或 LocalVars。
// 越界常量和非 integer 常量只能标记形态，不能伪造 integer fast path 命中。
func TestVMDecodedInstructionHandlesStrippedAndInvalidRK(t *testing.T) {
	// 构造 stripped Proto 与异常 RK，确认预解码只缓存可证明安全的 integer 常量值。
	proto := bytecode.NewProto("@decoded.lua")
	proto.Constants = []bytecode.Constant{bytecode.StringConstant("not integer")}
	proto.Code = []bytecode.Instruction{bytecode.CreateABC(bytecode.OpAdd, 0, bytecode.RKAsK(9), bytecode.RKAsK(0))}
	stripped := bytecode.StripDebug(proto)
	vm := NewVMWithPrototypeData(1, stripped.Constants, nil, nil, nil)
	vm.BindPrototype(stripped)

	decodedAdd, ok := vm.decodedInstructionAt(0)
	if !ok {
		// stripped Proto 的 code 仍必须支持预解码。
		t.Fatalf("decoded stripped add missing")
	}
	if !decodedAdd.BOperand.Constant || decodedAdd.BOperand.Index != 9 || decodedAdd.BOperand.IntegerConstantOK {
		// 越界常量只记录常量形态，不能设置 integer 常量命中。
		t.Fatalf("decoded invalid B operand mismatch: %+v", decodedAdd.BOperand)
	}
	if !decodedAdd.COperand.Constant || decodedAdd.COperand.Index != 0 || decodedAdd.COperand.IntegerConstantOK {
		// 非 integer 常量同样不能设置 integer 常量命中。
		t.Fatalf("decoded string C operand mismatch: %+v", decodedAdd.COperand)
	}
}

// TestVMTryExecuteAddForLoop 验证 `ADD; FORLOOP` superinstruction 的 integer 热路径。
//
// 该 fast path 只在 guard 全部命中时提交寄存器写入；循环继续和退出都必须与普通 ADD 后接
// FORLOOP 的 PC 与寄存器语义一致。
func TestVMTryExecuteAddForLoop(t *testing.T) {
	// 构造 `sum = sum + i; FORLOOP` 形态，R1..R4 是 numeric-for 控制槽。
	proto := bytecode.NewProto("add-forloop")
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpAdd, 0, 0, 4),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -2),
	}
	vm := NewVMWithPrototypeData(5, nil, nil, nil, nil)
	vm.BindPrototype(proto)
	vm.PrepareAddForLoopSuperInstructions()
	initialRegisters := []Value{IntegerValue(0), IntegerValue(1), IntegerValue(3), IntegerValue(1), IntegerValue(1)}
	for registerIndex, value := range initialRegisters {
		// 初始化 sum 与 FORLOOP 控制寄存器，模拟 FORPREP 后进入循环体的状态。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set register %d failed: %v", registerIndex, err)
		}
	}

	nextPC, handled := vm.TryExecuteAddForLoop(0)
	if !handled || nextPC != 0 {
		// 第一次执行后循环仍应跳回 ADD。
		t.Fatalf("first fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(1)) {
		// sum 必须写入 ADD 结果。
		t.Fatalf("first sum mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(1); !ok || !value.RawEqual(IntegerValue(2)) {
		// FORLOOP 继续时内部 index 必须步进。
		t.Fatalf("first index mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(4); !ok || !value.RawEqual(IntegerValue(2)) {
		// FORLOOP 继续时外部可见循环变量必须同步。
		t.Fatalf("first control variable mismatch: value=%#v ok=%v", value, ok)
	}

	nextPC, handled = vm.TryExecuteAddForLoop(0)
	if !handled || nextPC != 0 {
		// 第二次执行后仍应继续循环。
		t.Fatalf("second fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	nextPC, handled = vm.TryExecuteAddForLoop(0)
	if !handled || nextPC != 2 {
		// 第三次执行后越过 limit，应落到 FORLOOP 后一条指令。
		t.Fatalf("third fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(6)) {
		// 三轮累加结果必须等于 1+2+3。
		t.Fatalf("final sum mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(1); !ok || !value.RawEqual(IntegerValue(3)) {
		// 循环退出时普通 FORLOOP 不写回越界后的内部 index。
		t.Fatalf("final index mismatch: value=%#v ok=%v", value, ok)
	}
	if vm.currentPC != 1 || vm.pcOffset != 0 {
		// fast path 后 VM 状态应等价于刚执行完 FORLOOP。
		t.Fatalf("final pc state mismatch: currentPC=%d pcOffset=%d", vm.currentPC, vm.pcOffset)
	}
}

// TestVMTryExecuteTableWriteForLoopBatch 验证 `SETTABLE; FORLOOP` batch 可连续写入数组区。
func TestVMTryExecuteTableWriteForLoopBatch(t *testing.T) {
	proto := testTableWriteForLoopProto()
	vm := NewVMWithPrototypeData(5, nil, nil, nil, nil)
	vm.BindPrototype(proto)
	if !vm.PrepareTableWriteForLoopSuperInstructions() {
		// 官方 table_rw 写入循环形态应能预构建 superinstruction。
		t.Fatalf("expected table write for-loop superinstruction")
	}
	table := NewTable()
	initialRegisters := []Value{
		ReferenceValue(KindTable, table),
		IntegerValue(1),
		IntegerValue(3),
		IntegerValue(1),
		IntegerValue(1),
	}
	for registerIndex, value := range initialRegisters {
		// 初始化 table 与 FORLOOP 控制寄存器，模拟 FORPREP 后进入循环体的状态。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set register %d failed: %v", registerIndex, err)
		}
	}

	batch, ok := vm.PrepareTableWriteForLoopBatch(0)
	if !ok {
		// 精确 `t[i] = i` 写入循环应能准备 batch。
		t.Fatalf("expected prepared table write batch")
	}
	nextPC, iterations, handled := vm.TryExecuteTableWriteForLoopBatch(batch, 2)
	if !handled || iterations != 2 || nextPC != 0 {
		// 最多提交两轮时循环仍应跳回 SETTABLE。
		t.Fatalf("partial batch mismatch: handled=%v iterations=%d nextPC=%d", handled, iterations, nextPC)
	}
	if value := table.RawGetInteger(1); !value.RawEqual(IntegerValue(1)) {
		// 第一轮应写入 t[1] = 1。
		t.Fatalf("first table value mismatch: %#v", value)
	}
	if value := table.RawGetInteger(2); !value.RawEqual(IntegerValue(2)) {
		// 第二轮应写入 t[2] = 2。
		t.Fatalf("second table value mismatch: %#v", value)
	}
	if value, ok := vm.Register(1); !ok || !value.RawEqual(IntegerValue(3)) {
		// FORLOOP 继续时内部 index 必须推进到第三轮。
		t.Fatalf("partial index mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(4); !ok || !value.RawEqual(IntegerValue(3)) {
		// 外部可见循环变量必须同步到第三轮。
		t.Fatalf("partial visible index mismatch: value=%#v ok=%v", value, ok)
	}

	nextPC, iterations, handled = vm.TryExecuteTableWriteForLoopBatch(batch, 10)
	if !handled || iterations != 1 || nextPC != 2 {
		// 剩余一轮后达到 limit 并退出循环。
		t.Fatalf("final batch mismatch: handled=%v iterations=%d nextPC=%d", handled, iterations, nextPC)
	}
	if value := table.RawGetInteger(3); !value.RawEqual(IntegerValue(3)) {
		// 第三轮应写入 t[3] = 3。
		t.Fatalf("third table value mismatch: %#v", value)
	}
	if value, ok := vm.Register(1); !ok || !value.RawEqual(IntegerValue(3)) {
		// 循环退出时普通 FORLOOP 不写回越界后的内部 index。
		t.Fatalf("final index mismatch: value=%#v ok=%v", value, ok)
	}
	if vm.currentPC != 1 || vm.pcOffset != 0 {
		// batch 后 VM 状态应等价于刚执行完 FORLOOP。
		t.Fatalf("final pc state mismatch: currentPC=%d pcOffset=%d", vm.currentPC, vm.pcOffset)
	}
}

// TestVMTryExecuteTableWriteForLoopFallback 验证 table 写入 batch 动态 guard 失败无副作用。
func TestVMTryExecuteTableWriteForLoopFallback(t *testing.T) {
	proto := testTableWriteForLoopProto()
	vm := NewVMWithPrototypeData(5, nil, nil, nil, nil)
	vm.BindPrototype(proto)
	if !vm.PrepareTableWriteForLoopSuperInstructions() {
		// 静态字节码形态应能命中；元表属于执行期 guard。
		t.Fatalf("expected table write for-loop superinstruction")
	}
	table := NewTable()
	table.SetMetatable(NewTable())
	initialRegisters := []Value{
		ReferenceValue(KindTable, table),
		IntegerValue(1),
		IntegerValue(3),
		IntegerValue(1),
		IntegerValue(1),
	}
	for registerIndex, value := range initialRegisters {
		// 初始化 table 与 FORLOOP 控制寄存器，便于验证 guard 失败不产生写入。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set register %d failed: %v", registerIndex, err)
		}
	}

	batch, ok := vm.PrepareTableWriteForLoopBatch(0)
	if !ok {
		// 元表不是静态 guard，batch 准备应成功并在执行期回退。
		t.Fatalf("expected prepared table write batch")
	}
	nextPC, iterations, handled := vm.TryExecuteTableWriteForLoopBatch(batch, 8)
	if handled || iterations != 0 || nextPC != 0 {
		// 带元表 table 必须回退普通 SETTABLE，保留 __newindex 语义。
		t.Fatalf("fallback mismatch: handled=%v iterations=%d nextPC=%d", handled, iterations, nextPC)
	}
	if value := table.RawGetInteger(1); !value.IsNil() {
		// guard 失败不能提前写入 table。
		t.Fatalf("table should stay empty: %#v", value)
	}
	if value, ok := vm.Register(1); !ok || !value.RawEqual(IntegerValue(1)) {
		// guard 失败不能推进 FORLOOP 控制槽。
		t.Fatalf("index should be unchanged: value=%#v ok=%v", value, ok)
	}
}

// TestVMTryExecuteAddForLoopBatch 验证 `ADD; FORLOOP` batch 可连续提交多轮。
func TestVMTryExecuteAddForLoopBatch(t *testing.T) {
	// 构造官方 arith_add_loop 的 `sum = sum + i; FORLOOP` 形态。
	proto := bytecode.NewProto("add-forloop-batch")
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpAdd, 0, 0, 4),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -2),
	}
	vm := NewVMWithPrototypeData(5, nil, nil, nil, nil)
	vm.BindPrototype(proto)
	if !vm.PrepareAddForLoopSuperInstructions() {
		// ADD;FORLOOP 完整循环体应能预构建 superinstruction。
		t.Fatalf("expected add for-loop superinstruction")
	}
	initialRegisters := []Value{IntegerValue(0), IntegerValue(1), IntegerValue(3), IntegerValue(1), IntegerValue(1)}
	for registerIndex, value := range initialRegisters {
		// 初始化 sum 与 FORLOOP 控制寄存器，模拟 FORPREP 后进入循环体的状态。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set register %d failed: %v", registerIndex, err)
		}
	}

	batch, ok := vm.PrepareAddForLoopBatch(0)
	if !ok {
		// 官方加法循环形态应能准备 batch。
		t.Fatalf("expected prepared add for-loop batch")
	}
	nextPC, iterations, handled := vm.TryExecuteAddForLoopBatch(batch, 2)
	if !handled || iterations != 2 || nextPC != 0 {
		// 最多提交两轮时循环仍应跳回 ADD。
		t.Fatalf("partial batch mismatch: handled=%v iterations=%d nextPC=%d", handled, iterations, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(3)) {
		// sum 必须等于 1+2。
		t.Fatalf("partial sum mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(1); !ok || !value.RawEqual(IntegerValue(3)) {
		// FORLOOP 继续时内部 index 必须推进到第三轮。
		t.Fatalf("partial index mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(4); !ok || !value.RawEqual(IntegerValue(3)) {
		// 外部可见循环变量必须同步到第三轮。
		t.Fatalf("partial visible index mismatch: value=%#v ok=%v", value, ok)
	}

	nextPC, iterations, handled = vm.TryExecuteAddForLoopBatch(batch, 10)
	if !handled || iterations != 1 || nextPC != 2 {
		// 剩余一轮后达到 limit 并退出循环。
		t.Fatalf("final batch mismatch: handled=%v iterations=%d nextPC=%d", handled, iterations, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(6)) {
		// 三轮累加结果必须等于 1+2+3。
		t.Fatalf("final sum mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(1); !ok || !value.RawEqual(IntegerValue(3)) {
		// 循环退出时普通 FORLOOP 不写回越界后的内部 index。
		t.Fatalf("final index mismatch: value=%#v ok=%v", value, ok)
	}
	if vm.currentPC != 1 || vm.pcOffset != 0 {
		// batch 后 VM 状态应等价于刚执行完 FORLOOP。
		t.Fatalf("final pc state mismatch: currentPC=%d pcOffset=%d", vm.currentPC, vm.pcOffset)
	}
}

// TestVMTryExecuteAddForLoopConstantOperandAndFallback 验证常量操作数和 guard 回退。
//
// integer 常量 ADD 可以直接命中；当任一操作数或 FORLOOP 控制槽不是 integer 时，fast path 必须
// 返回 handled=false 且不修改寄存器，交由普通 VM 保留完整 Lua 转换、元方法和错误语义。
func TestVMTryExecuteAddForLoopConstantOperandAndFallback(t *testing.T) {
	// 先验证 `sum = sum + Kint; FORLOOP` 可以使用预解码 integer 常量。
	proto := bytecode.NewProto("add-forloop-constant")
	proto.Constants = []bytecode.Constant{bytecode.IntegerConstant(2)}
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpAdd, 0, 0, bytecode.RKAsK(0)),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -2),
	}
	vm := NewVMWithPrototypeData(5, proto.Constants, nil, nil, nil)
	vm.BindPrototype(proto)
	vm.PrepareAddForLoopSuperInstructions()
	for registerIndex, value := range []Value{IntegerValue(10), IntegerValue(1), IntegerValue(1), IntegerValue(1), IntegerValue(1)} {
		// 初始化单轮循环，确认常量 ADD 与 FORLOOP 退出同时成立。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set constant register %d failed: %v", registerIndex, err)
		}
	}
	nextPC, handled := vm.TryExecuteAddForLoop(0)
	if !handled || nextPC != 2 {
		// 单轮循环应执行 ADD 后直接退出。
		t.Fatalf("constant fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(12)) {
		// integer 常量必须参与 ADD 写回。
		t.Fatalf("constant sum mismatch: value=%#v ok=%v", value, ok)
	}

	// 再验证非 integer 操作数会无副作用回退。
	if err := vm.SetRegister(0, StringValue("keep")); err != nil {
		// 测试准备阶段必须能写入目标寄存器。
		t.Fatalf("set fallback target failed: %v", err)
	}
	if err := vm.SetRegister(1, IntegerValue(1)); err != nil {
		// 恢复 FORLOOP 控制槽，避免回退原因来自循环控制。
		t.Fatalf("set fallback index failed: %v", err)
	}
	if err := vm.SetRegister(4, StringValue("not integer")); err != nil {
		// ADD 右操作数变为字符串，fast path 必须拒绝处理。
		t.Fatalf("set fallback operand failed: %v", err)
	}
	nextPC, handled = vm.TryExecuteAddForLoop(0)
	if handled || nextPC != 0 {
		// guard 不满足时必须回退普通 VM。
		t.Fatalf("fallback mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(StringValue("keep")) {
		// 回退前不能覆盖目标寄存器，确保普通 VM 仍能产生原始错误或元方法语义。
		t.Fatalf("fallback target mutated: value=%#v ok=%v", value, ok)
	}
}

// TestVMTryExecuteAddForLoopRejectsNonEntryAdd 验证混合循环末尾 ADD 不会误命中。
//
// `arith_mix_loop` 这类循环中也可能出现相邻 `ADD; FORLOOP`，但 FORLOOP 跳回的是更早的循环体入口。
// 该形态必须留给后续完整 superinstruction，不能用 arith_add_loop 的窄路径处理。
func TestVMTryExecuteAddForLoopRejectsNonEntryAdd(t *testing.T) {
	// 构造 `MUL; ADD; FORLOOP`，其中 FORLOOP 回跳到 MUL 而不是 ADD。
	proto := bytecode.NewProto("add-forloop-non-entry")
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpMul, 5, 4, bytecode.RKAsK(0)),
		bytecode.CreateABC(bytecode.OpAdd, 0, 0, 5),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -3),
	}
	vm := NewVMWithPrototypeData(6, []bytecode.Constant{bytecode.IntegerConstant(3)}, nil, nil, nil)
	vm.BindPrototype(proto)
	if vm.PrepareAddForLoopSuperInstructions() {
		// 只有 FORLOOP 回跳当前 ADD 时才应启用 ADD;FORLOOP fast path。
		t.Fatalf("non-entry ADD;FORLOOP should not prepare superinstruction")
	}
	if nextPC, handled := vm.TryExecuteAddForLoop(1); handled || nextPC != 0 {
		// 误命中会破坏混合循环前置 MUL/SUB/IDIV/MOD 的执行顺序。
		t.Fatalf("non-entry fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
}

// TestVMTryExecuteMulAddSubForLoop 验证 `MUL; ADD; SUB; FORLOOP` superinstruction 的 integer 热路径。
//
// 该形态对应 `sum = sum + i * 3 - 7`，fast path 必须按普通逐指令顺序提交临时寄存器和
// numeric-for 控制槽更新。
func TestVMTryExecuteMulAddSubForLoop(t *testing.T) {
	// 构造 `tmp = i * 3; tmp = sum + tmp; sum = tmp - 7; FORLOOP` 形态。
	proto := bytecode.NewProto("mul-add-sub-forloop")
	proto.Constants = []bytecode.Constant{bytecode.IntegerConstant(3), bytecode.IntegerConstant(7)}
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpMul, 5, 4, bytecode.RKAsK(0)),
		bytecode.CreateABC(bytecode.OpAdd, 5, 0, 5),
		bytecode.CreateABC(bytecode.OpSub, 0, 5, bytecode.RKAsK(1)),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -4),
	}
	vm := NewVMWithPrototypeData(6, proto.Constants, nil, nil, nil)
	vm.BindPrototype(proto)
	if !vm.PrepareMulAddSubForLoopSuperInstructions() {
		// 完整循环体回跳到 MUL 时应能准备算术链 superinstruction。
		t.Fatalf("expected MUL;ADD;SUB;FORLOOP superinstruction")
	}
	initialRegisters := []Value{IntegerValue(0), IntegerValue(1), IntegerValue(3), IntegerValue(1), IntegerValue(1), NilValue()}
	for registerIndex, value := range initialRegisters {
		// 初始化 sum 与 FORLOOP 控制寄存器，模拟 FORPREP 后进入循环体的状态。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set register %d failed: %v", registerIndex, err)
		}
	}

	nextPC, handled := vm.TryExecuteMulAddSubForLoop(0)
	if !handled || nextPC != 0 {
		// 第一轮执行后循环仍应跳回 MUL。
		t.Fatalf("first fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(-4)) {
		// 第一轮结果为 0 + 1*3 - 7。
		t.Fatalf("first sum mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(5); !ok || !value.RawEqual(IntegerValue(3)) {
		// 临时寄存器应保留普通 ADD 写回的中间结果，SUB 写回 sum 不会覆盖 tmp。
		t.Fatalf("first temp mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(4); !ok || !value.RawEqual(IntegerValue(2)) {
		// FORLOOP 继续时外部可见循环变量必须同步到下一轮。
		t.Fatalf("first control variable mismatch: value=%#v ok=%v", value, ok)
	}

	nextPC, handled = vm.TryExecuteMulAddSubForLoop(0)
	if !handled || nextPC != 0 {
		// 第二轮执行后仍应继续循环。
		t.Fatalf("second fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	nextPC, handled = vm.TryExecuteMulAddSubForLoop(0)
	if !handled || nextPC != 4 {
		// 第三轮执行后越过 limit，应落到 FORLOOP 后一条指令。
		t.Fatalf("third fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(-3)) {
		// 三轮链式算术结果必须等于 (((0+3-7)+6-7)+9-7)。
		t.Fatalf("final sum mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(1); !ok || !value.RawEqual(IntegerValue(3)) {
		// 循环退出时普通 FORLOOP 不写回越界后的内部 index。
		t.Fatalf("final index mismatch: value=%#v ok=%v", value, ok)
	}
	if vm.currentPC != 3 || vm.pcOffset != 0 {
		// fast path 后 VM 状态应等价于刚执行完 FORLOOP。
		t.Fatalf("final pc state mismatch: currentPC=%d pcOffset=%d", vm.currentPC, vm.pcOffset)
	}
}

// TestVMTryExecuteMulAddSubForLoopFallback 验证算术链 guard 失败时无副作用回退。
//
// 任一操作数不是 integer 时，fast path 必须返回 handled=false，不提前覆盖 sum 或临时寄存器。
func TestVMTryExecuteMulAddSubForLoopFallback(t *testing.T) {
	// 构造与官方 arith_chain_temp 同形态的四指令循环体。
	proto := bytecode.NewProto("mul-add-sub-forloop-fallback")
	proto.Constants = []bytecode.Constant{bytecode.IntegerConstant(3), bytecode.IntegerConstant(7)}
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpMul, 5, 4, bytecode.RKAsK(0)),
		bytecode.CreateABC(bytecode.OpAdd, 5, 0, 5),
		bytecode.CreateABC(bytecode.OpSub, 0, 5, bytecode.RKAsK(1)),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -4),
	}
	vm := NewVMWithPrototypeData(6, proto.Constants, nil, nil, nil)
	vm.BindPrototype(proto)
	vm.PrepareMulAddSubForLoopSuperInstructions()
	initialRegisters := []Value{IntegerValue(11), IntegerValue(1), IntegerValue(1), IntegerValue(1), StringValue("not integer"), IntegerValue(99)}
	for registerIndex, value := range initialRegisters {
		// 初始化一个 MUL 左操作数不满足 integer guard 的场景。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set fallback register %d failed: %v", registerIndex, err)
		}
	}

	nextPC, handled := vm.TryExecuteMulAddSubForLoop(0)
	if handled || nextPC != 0 {
		// guard 不满足时必须回退普通 VM。
		t.Fatalf("fallback mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(11)) {
		// 回退前不能覆盖 sum，确保普通 VM 仍能产生原始错误或元方法语义。
		t.Fatalf("fallback sum mutated: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(5); !ok || !value.RawEqual(IntegerValue(99)) {
		// 回退前不能覆盖临时寄存器。
		t.Fatalf("fallback temp mutated: value=%#v ok=%v", value, ok)
	}
}

// TestVMTryExecuteMulAddSubForLoopRejectsNonEntryLoop 验证非完整循环体不会误命中。
//
// 如果 FORLOOP 不回跳当前 MUL，说明四条相邻指令不是完整循环体，fast path 不能启用。
func TestVMTryExecuteMulAddSubForLoopRejectsNonEntryLoop(t *testing.T) {
	// 构造 `MUL;ADD;SUB;FORLOOP`，但 FORLOOP 回跳到 ADD 而不是 MUL。
	proto := bytecode.NewProto("mul-add-sub-forloop-non-entry")
	proto.Constants = []bytecode.Constant{bytecode.IntegerConstant(3), bytecode.IntegerConstant(7)}
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpMul, 5, 4, bytecode.RKAsK(0)),
		bytecode.CreateABC(bytecode.OpAdd, 5, 0, 5),
		bytecode.CreateABC(bytecode.OpSub, 0, 5, bytecode.RKAsK(1)),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -3),
	}
	vm := NewVMWithPrototypeData(6, proto.Constants, nil, nil, nil)
	vm.BindPrototype(proto)
	if vm.PrepareMulAddSubForLoopSuperInstructions() {
		// 只有 FORLOOP 回跳当前 MUL 时才应启用算术链 fast path。
		t.Fatalf("non-entry MUL;ADD;SUB;FORLOOP should not prepare superinstruction")
	}
	if nextPC, handled := vm.TryExecuteMulAddSubForLoop(0); handled || nextPC != 0 {
		// 误命中会破坏循环体前序指令执行顺序。
		t.Fatalf("non-entry fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
}

// TestVMTryExecuteMixArithmeticForLoop 验证 `arith_mix_loop` 完整 superinstruction 热路径。
//
// 该形态对应 `sum = sum + i * 3 - 7; sum = sum // 2 + i % 5`，fast path 必须按普通逐指令
// 顺序处理 IDIV、MOD 和末尾 ADD，并保持 numeric-for 控制槽更新一致。
func TestVMTryExecuteMixArithmeticForLoop(t *testing.T) {
	// 构造官方 arith_mix_loop 的完整循环体。
	proto := bytecode.NewProto("mix-arithmetic-forloop")
	proto.Constants = []bytecode.Constant{
		bytecode.IntegerConstant(3),
		bytecode.IntegerConstant(7),
		bytecode.IntegerConstant(2),
		bytecode.IntegerConstant(5),
	}
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpMul, 5, 4, bytecode.RKAsK(0)),
		bytecode.CreateABC(bytecode.OpAdd, 5, 0, 5),
		bytecode.CreateABC(bytecode.OpSub, 0, 5, bytecode.RKAsK(1)),
		bytecode.CreateABC(bytecode.OpIDiv, 5, 0, bytecode.RKAsK(2)),
		bytecode.CreateABC(bytecode.OpMod, 6, 4, bytecode.RKAsK(3)),
		bytecode.CreateABC(bytecode.OpAdd, 0, 5, 6),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -7),
	}
	vm := NewVMWithPrototypeData(7, proto.Constants, nil, nil, nil)
	vm.BindPrototype(proto)
	if !vm.PrepareMixArithmeticForLoopSuperInstructions() {
		// 完整循环体回跳到 MUL 时应能准备混合算术 superinstruction。
		t.Fatalf("expected mix arithmetic superinstruction")
	}
	initialRegisters := []Value{IntegerValue(0), IntegerValue(1), IntegerValue(3), IntegerValue(1), IntegerValue(1), NilValue(), NilValue()}
	for registerIndex, value := range initialRegisters {
		// 初始化 sum 与 FORLOOP 控制寄存器，模拟 FORPREP 后进入循环体的状态。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set register %d failed: %v", registerIndex, err)
		}
	}

	nextPC, handled := vm.TryExecuteMixArithmeticForLoop(0)
	if !handled || nextPC != 0 {
		// 第一轮执行后循环仍应跳回 MUL。
		t.Fatalf("first fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(-1)) {
		// 第一轮结果为 (0 + 1*3 - 7)//2 + 1%5。
		t.Fatalf("first sum mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(5); !ok || !value.RawEqual(IntegerValue(-2)) {
		// R5 最终应保存 IDIV 结果。
		t.Fatalf("first idiv temp mismatch: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(6); !ok || !value.RawEqual(IntegerValue(1)) {
		// R6 最终应保存 MOD 结果。
		t.Fatalf("first mod temp mismatch: value=%#v ok=%v", value, ok)
	}

	nextPC, handled = vm.TryExecuteMixArithmeticForLoop(0)
	if !handled || nextPC != 0 {
		// 第二轮执行后仍应继续循环。
		t.Fatalf("second fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	nextPC, handled = vm.TryExecuteMixArithmeticForLoop(0)
	if !handled || nextPC != 7 {
		// 第三轮执行后越过 limit，应落到 FORLOOP 后一条指令。
		t.Fatalf("third fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(4)) {
		// 三轮混合算术结果必须与普通 VM 一致。
		t.Fatalf("final sum mismatch: value=%#v ok=%v", value, ok)
	}
	if vm.currentPC != 6 || vm.pcOffset != 0 {
		// fast path 后 VM 状态应等价于刚执行完 FORLOOP。
		t.Fatalf("final pc state mismatch: currentPC=%d pcOffset=%d", vm.currentPC, vm.pcOffset)
	}
}

// TestVMTryExecuteMixArithmeticForLoopFallback 验证混合算术 guard 失败时无副作用回退。
//
// 除数为 0 或操作数类型不满足 integer guard 时，fast path 必须回退普通 VM，让普通路径保留
// 前序写回和零除错误语义。
func TestVMTryExecuteMixArithmeticForLoopFallback(t *testing.T) {
	// 构造 IDIV 除数为 0 的形态，fast path 必须拒绝处理。
	proto := bytecode.NewProto("mix-arithmetic-forloop-fallback")
	proto.Constants = []bytecode.Constant{
		bytecode.IntegerConstant(3),
		bytecode.IntegerConstant(7),
		bytecode.IntegerConstant(0),
		bytecode.IntegerConstant(5),
	}
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpMul, 5, 4, bytecode.RKAsK(0)),
		bytecode.CreateABC(bytecode.OpAdd, 5, 0, 5),
		bytecode.CreateABC(bytecode.OpSub, 0, 5, bytecode.RKAsK(1)),
		bytecode.CreateABC(bytecode.OpIDiv, 5, 0, bytecode.RKAsK(2)),
		bytecode.CreateABC(bytecode.OpMod, 6, 4, bytecode.RKAsK(3)),
		bytecode.CreateABC(bytecode.OpAdd, 0, 5, 6),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -7),
	}
	vm := NewVMWithPrototypeData(7, proto.Constants, nil, nil, nil)
	vm.BindPrototype(proto)
	vm.PrepareMixArithmeticForLoopSuperInstructions()
	initialRegisters := []Value{IntegerValue(11), IntegerValue(1), IntegerValue(1), IntegerValue(1), IntegerValue(1), IntegerValue(99), IntegerValue(88)}
	for registerIndex, value := range initialRegisters {
		// 初始化可执行到 IDIV 零除 guard 的寄存器状态。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set fallback register %d failed: %v", registerIndex, err)
		}
	}

	nextPC, handled := vm.TryExecuteMixArithmeticForLoop(0)
	if handled || nextPC != 0 {
		// 零除必须回退普通 VM，不能在 fast path 内吞掉错误路径。
		t.Fatalf("fallback mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, ok := vm.Register(0); !ok || !value.RawEqual(IntegerValue(11)) {
		// 回退前不能提前覆盖 sum。
		t.Fatalf("fallback sum mutated: value=%#v ok=%v", value, ok)
	}
	if value, ok := vm.Register(5); !ok || !value.RawEqual(IntegerValue(99)) {
		// 回退前不能提前覆盖临时寄存器。
		t.Fatalf("fallback temp mutated: value=%#v ok=%v", value, ok)
	}
}

// TestVMTryExecuteMixArithmeticForLoopRejectsNonEntryLoop 验证非完整混合循环不会误命中。
//
// 如果 FORLOOP 不回跳当前 MUL，说明七条相邻指令不是完整循环体，fast path 不能启用。
func TestVMTryExecuteMixArithmeticForLoopRejectsNonEntryLoop(t *testing.T) {
	// 构造完整 opcode 序列，但 FORLOOP 回跳到 ADD 而不是 MUL。
	proto := bytecode.NewProto("mix-arithmetic-forloop-non-entry")
	proto.Constants = []bytecode.Constant{
		bytecode.IntegerConstant(3),
		bytecode.IntegerConstant(7),
		bytecode.IntegerConstant(2),
		bytecode.IntegerConstant(5),
	}
	proto.Code = []bytecode.Instruction{
		bytecode.CreateABC(bytecode.OpMul, 5, 4, bytecode.RKAsK(0)),
		bytecode.CreateABC(bytecode.OpAdd, 5, 0, 5),
		bytecode.CreateABC(bytecode.OpSub, 0, 5, bytecode.RKAsK(1)),
		bytecode.CreateABC(bytecode.OpIDiv, 5, 0, bytecode.RKAsK(2)),
		bytecode.CreateABC(bytecode.OpMod, 6, 4, bytecode.RKAsK(3)),
		bytecode.CreateABC(bytecode.OpAdd, 0, 5, 6),
		bytecode.CreateAsBx(bytecode.OpForLoop, 1, -6),
	}
	vm := NewVMWithPrototypeData(7, proto.Constants, nil, nil, nil)
	vm.BindPrototype(proto)
	if vm.PrepareMixArithmeticForLoopSuperInstructions() {
		// 只有 FORLOOP 回跳当前 MUL 时才应启用混合算术 fast path。
		t.Fatalf("non-entry mix arithmetic should not prepare superinstruction")
	}
	if nextPC, handled := vm.TryExecuteMixArithmeticForLoop(0); handled || nextPC != 0 {
		// 误命中会破坏循环体前序指令执行顺序。
		t.Fatalf("non-entry fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
}

// TestVMTryExecuteFunctionCallAddForLoop 验证 function_call 完整循环体 fast path。
func TestVMTryExecuteFunctionCallAddForLoop(t *testing.T) {
	proto := testFunctionCallAddForLoopProto()
	vm := NewVMWithConstants(9, proto.Constants)
	vm.BindPrototype(proto)
	if !vm.PrepareFunctionCallAddForLoopSuperInstructions() {
		// 官方 function_call 字节码形态应能预构建 superinstruction。
		t.Fatalf("expected function_call superinstruction")
	}
	closure := NewLuaClosure(testLeafAddReturnProto(), nil, nil)
	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 被调函数局部必须能写入寄存器。
		t.Fatalf("set function failed: %v", err)
	}
	if err := vm.SetRegister(1, IntegerValue(10)); err != nil {
		// sum 初值必须能写入寄存器。
		t.Fatalf("set sum failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(1)); err != nil {
		// FORLOOP 内部 index 必须能写入寄存器。
		t.Fatalf("set index failed: %v", err)
	}
	if err := vm.SetRegister(3, IntegerValue(3)); err != nil {
		// FORLOOP limit 必须能写入寄存器。
		t.Fatalf("set limit failed: %v", err)
	}
	if err := vm.SetRegister(4, IntegerValue(1)); err != nil {
		// FORLOOP step 必须能写入寄存器。
		t.Fatalf("set step failed: %v", err)
	}
	if err := vm.SetRegister(5, IntegerValue(1)); err != nil {
		// 外部可见循环变量必须能写入寄存器。
		t.Fatalf("set visible index failed: %v", err)
	}

	nextPC, handled := vm.TryExecuteFunctionCallAddForLoop(0)
	if !handled || nextPC != 0 {
		// 第一轮循环未结束时必须跳回循环体入口。
		t.Fatalf("function_call fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, _ := vm.Register(1); !value.RawEqual(IntegerValue(12)) {
		// sum = 10 + add(1, 1)。
		t.Fatalf("sum mismatch: %#v", value)
	}
	if value, _ := vm.Register(6); !value.RawEqual(IntegerValue(2)) {
		// CALL 函数槽在单返回后保存 add 的结果。
		t.Fatalf("call result mismatch: %#v", value)
	}
	if value, _ := vm.Register(7); !value.RawEqual(IntegerValue(1)) {
		// 第一实参槽保留 MOVE 后的循环变量值。
		t.Fatalf("first argument mismatch: %#v", value)
	}
	if value, _ := vm.Register(8); !value.RawEqual(IntegerValue(1)) {
		// 第二实参槽保留 LOADK 后的 integer 常量。
		t.Fatalf("second argument mismatch: %#v", value)
	}
	if value, _ := vm.Register(2); !value.RawEqual(IntegerValue(2)) {
		// FORLOOP 继续时更新内部 index。
		t.Fatalf("for index mismatch: %#v", value)
	}
	if value, _ := vm.Register(5); !value.RawEqual(IntegerValue(2)) {
		// FORLOOP 继续时更新外部可见循环变量。
		t.Fatalf("visible index mismatch: %#v", value)
	}
}

// TestVMTryExecuteFunctionCallAddForLoopFallback 验证 function_call fast path guard 失败无副作用。
func TestVMTryExecuteFunctionCallAddForLoopFallback(t *testing.T) {
	proto := testFunctionCallAddForLoopProto()
	vm := NewVMWithConstants(9, proto.Constants)
	vm.BindPrototype(proto)
	if !vm.PrepareFunctionCallAddForLoopSuperInstructions() {
		// 官方 function_call 字节码形态应能预构建 superinstruction。
		t.Fatalf("expected function_call superinstruction")
	}
	if err := vm.SetRegister(0, StringValue("not a closure")); err != nil {
		// 非 closure 被调值用于验证回退。
		t.Fatalf("set function failed: %v", err)
	}
	if err := vm.SetRegister(1, IntegerValue(10)); err != nil {
		// sum 初值必须能写入寄存器。
		t.Fatalf("set sum failed: %v", err)
	}
	if err := vm.SetRegister(5, IntegerValue(1)); err != nil {
		// 外部循环变量必须能写入寄存器。
		t.Fatalf("set visible index failed: %v", err)
	}

	nextPC, handled := vm.TryExecuteFunctionCallAddForLoop(0)
	if handled || nextPC != 0 {
		// 非 closure 不应被 fast path 消费。
		t.Fatalf("fallback mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, _ := vm.Register(1); !value.RawEqual(IntegerValue(10)) {
		// guard 失败不能提前修改 sum。
		t.Fatalf("sum should be unchanged: %#v", value)
	}
	if value, _ := vm.Register(6); !value.IsNil() {
		// guard 失败不能执行第一条 MOVE。
		t.Fatalf("temporary slot should stay nil: %#v", value)
	}
}

// TestVMTryExecuteFunctionCallAddForLoopBatch 验证已准备的 function_call batch 可连续复用。
func TestVMTryExecuteFunctionCallAddForLoopBatch(t *testing.T) {
	proto := testFunctionCallAddForLoopProto()
	vm := NewVMWithConstants(9, proto.Constants)
	vm.BindPrototype(proto)
	if !vm.PrepareFunctionCallAddForLoopSuperInstructions() {
		// 官方 function_call 字节码形态应能预构建 superinstruction。
		t.Fatalf("expected function_call superinstruction")
	}
	closure := NewLuaClosure(testLeafAddReturnProto(), nil, nil)
	for _, entry := range []struct {
		register int
		value    Value
	}{
		{register: 0, value: ReferenceValue(KindLuaClosure, closure)},
		{register: 1, value: IntegerValue(10)},
		{register: 2, value: IntegerValue(1)},
		{register: 3, value: IntegerValue(3)},
		{register: 4, value: IntegerValue(1)},
		{register: 5, value: IntegerValue(1)},
	} {
		if err := vm.SetRegister(entry.register, entry.value); err != nil {
			// 测试夹具寄存器必须能成功初始化。
			t.Fatalf("set register %d failed: %v", entry.register, err)
		}
	}

	batch, ok := vm.PrepareFunctionCallAddForLoopBatch(0)
	if !ok {
		// batch 准备应复用已验证的静态形态和 callee metadata。
		t.Fatalf("expected prepared batch")
	}
	nextPC, iterations, handled, err := vm.TryExecuteFunctionCallAddForLoopBatch(batch, 8, NewState())
	if err != nil || !handled || iterations != 3 || nextPC != 6 {
		// 三轮后达到 limit 并退出循环。
		t.Fatalf("batch mismatch: handled=%v iterations=%d nextPC=%d err=%v", handled, iterations, nextPC, err)
	}
	if value, _ := vm.Register(1); !value.RawEqual(IntegerValue(19)) {
		// sum = 10 + add(1,1) + add(2,1) + add(3,1)。
		t.Fatalf("sum mismatch: %#v", value)
	}
	if value, _ := vm.Register(2); !value.RawEqual(IntegerValue(3)) {
		// 循环退出时 FORLOOP 不写入越界后的内部 index。
		t.Fatalf("for index mismatch: %#v", value)
	}
	if value, _ := vm.Register(5); !value.RawEqual(IntegerValue(3)) {
		// 循环退出时外部可见循环变量保持最后一次有效值。
		t.Fatalf("visible index mismatch: %#v", value)
	}
}

// TestVMTryExecutePreparedFunctionCallAddForLoopFallback 验证 prepared batch 动态 guard 失败无副作用。
func TestVMTryExecutePreparedFunctionCallAddForLoopFallback(t *testing.T) {
	proto := testFunctionCallAddForLoopProto()
	vm := NewVMWithConstants(9, proto.Constants)
	vm.BindPrototype(proto)
	if !vm.PrepareFunctionCallAddForLoopSuperInstructions() {
		// 官方 function_call 字节码形态应能预构建 superinstruction。
		t.Fatalf("expected function_call superinstruction")
	}
	closure := NewLuaClosure(testLeafAddReturnProto(), nil, nil)
	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 被调函数局部必须能写入寄存器。
		t.Fatalf("set function failed: %v", err)
	}
	if err := vm.SetRegister(1, StringValue("not integer sum")); err != nil {
		// 非 integer sum 用于验证动态 guard 失败。
		t.Fatalf("set sum failed: %v", err)
	}
	if err := vm.SetRegister(5, IntegerValue(1)); err != nil {
		// 外部循环变量必须能写入寄存器。
		t.Fatalf("set visible index failed: %v", err)
	}

	batch, ok := vm.PrepareFunctionCallAddForLoopBatch(0)
	if !ok {
		// sum 类型不是 batch 静态 guard；应延迟到执行期回退。
		t.Fatalf("expected prepared batch")
	}
	nextPC, handled := vm.TryExecutePreparedFunctionCallAddForLoop(batch)
	if handled || nextPC != 0 {
		// 动态 guard 失败不应被 fast path 消费。
		t.Fatalf("fallback mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, _ := vm.Register(1); !value.RawEqual(StringValue("not integer sum")) {
		// guard 失败不能修改 sum。
		t.Fatalf("sum should be unchanged: %#v", value)
	}
	if value, _ := vm.Register(6); !value.IsNil() {
		// guard 失败不能执行第一条 MOVE。
		t.Fatalf("temporary slot should stay nil: %#v", value)
	}
}

// TestVMTryExecuteFunctionCallAssignForLoopBatch 验证官方 function_call 赋值循环 batch。
func TestVMTryExecuteFunctionCallAssignForLoopBatch(t *testing.T) {
	proto := testFunctionCallAssignForLoopProto()
	vm := NewVMWithConstants(9, nil)
	vm.BindPrototype(proto)
	if !vm.PrepareFunctionCallAssignForLoopSuperInstructions() {
		// 官方 function_call 赋值字节码形态应能预构建 superinstruction。
		t.Fatalf("expected function_call assign superinstruction")
	}
	closure := NewLuaClosure(testLeafAddReturnProto(), nil, nil)
	for _, entry := range []struct {
		register int
		value    Value
	}{
		{register: 0, value: ReferenceValue(KindLuaClosure, closure)},
		{register: 1, value: IntegerValue(10)},
		{register: 2, value: IntegerValue(1)},
		{register: 3, value: IntegerValue(3)},
		{register: 4, value: IntegerValue(1)},
		{register: 5, value: IntegerValue(1)},
	} {
		if err := vm.SetRegister(entry.register, entry.value); err != nil {
			// 测试夹具寄存器必须能成功初始化。
			t.Fatalf("set register %d failed: %v", entry.register, err)
		}
	}

	batch, ok := vm.PrepareFunctionCallAssignForLoopBatch(0)
	if !ok {
		// batch 准备应复用已验证的静态形态和 callee metadata。
		t.Fatalf("expected prepared assign batch")
	}
	nextPC, iterations, handled, err := vm.TryExecuteFunctionCallAssignForLoopBatch(batch, 8, NewState())
	if err != nil || !handled || iterations != 3 || nextPC != 6 {
		// 三轮后达到 limit 并退出循环。
		t.Fatalf("assign batch mismatch: handled=%v iterations=%d nextPC=%d err=%v", handled, iterations, nextPC, err)
	}
	if value, _ := vm.Register(1); !value.RawEqual(IntegerValue(16)) {
		// sum = add(add(add(10, 1), 2), 3)。
		t.Fatalf("sum mismatch: %#v", value)
	}
	if value, _ := vm.Register(6); !value.RawEqual(IntegerValue(16)) {
		// CALL 函数槽在单返回后保存最后一轮 add 的结果。
		t.Fatalf("call result mismatch: %#v", value)
	}
	if value, _ := vm.Register(7); !value.RawEqual(IntegerValue(13)) {
		// 第一实参槽保留最后一轮 MOVE 后的旧 sum。
		t.Fatalf("first argument mismatch: %#v", value)
	}
	if value, _ := vm.Register(8); !value.RawEqual(IntegerValue(3)) {
		// 第二实参槽保留最后一轮 MOVE 后的循环变量值。
		t.Fatalf("second argument mismatch: %#v", value)
	}
	if value, _ := vm.Register(2); !value.RawEqual(IntegerValue(3)) {
		// 循环退出时 FORLOOP 不写入越界后的内部 index。
		t.Fatalf("for index mismatch: %#v", value)
	}
	if value, _ := vm.Register(5); !value.RawEqual(IntegerValue(3)) {
		// 循环退出时外部可见循环变量保持最后一次有效值。
		t.Fatalf("visible index mismatch: %#v", value)
	}
}

// TestVMTryExecuteFunctionCallAssignForLoopFallback 验证官方 function_call batch 动态 guard 失败无副作用。
func TestVMTryExecuteFunctionCallAssignForLoopFallback(t *testing.T) {
	proto := testFunctionCallAssignForLoopProto()
	vm := NewVMWithConstants(9, nil)
	vm.BindPrototype(proto)
	if !vm.PrepareFunctionCallAssignForLoopSuperInstructions() {
		// 官方 function_call 赋值字节码形态应能预构建 superinstruction。
		t.Fatalf("expected function_call assign superinstruction")
	}
	closure := NewLuaClosure(testLeafAddReturnProto(), nil, nil)
	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 被调函数局部必须能写入寄存器。
		t.Fatalf("set function failed: %v", err)
	}
	if err := vm.SetRegister(1, StringValue("not integer sum")); err != nil {
		// 非 integer sum 用于验证动态 guard 失败。
		t.Fatalf("set sum failed: %v", err)
	}
	if err := vm.SetRegister(5, IntegerValue(1)); err != nil {
		// 外部循环变量必须能写入寄存器。
		t.Fatalf("set visible index failed: %v", err)
	}

	batch, ok := vm.PrepareFunctionCallAssignForLoopBatch(0)
	if !ok {
		// sum 类型不是 batch 静态 guard；应延迟到执行期回退。
		t.Fatalf("expected prepared assign batch")
	}
	nextPC, iterations, handled, err := vm.TryExecuteFunctionCallAssignForLoopBatch(batch, 8, NewState())
	if err != nil || handled || iterations != 0 || nextPC != 0 {
		// 动态 guard 失败不应被 fast path 消费。
		t.Fatalf("fallback mismatch: handled=%v iterations=%d nextPC=%d err=%v", handled, iterations, nextPC, err)
	}
	if value, _ := vm.Register(1); !value.RawEqual(StringValue("not integer sum")) {
		// guard 失败不能修改 sum。
		t.Fatalf("sum should be unchanged: %#v", value)
	}
	if value, _ := vm.Register(6); !value.IsNil() {
		// guard 失败不能执行第一条 MOVE。
		t.Fatalf("temporary slot should stay nil: %#v", value)
	}
}

// TestVMTryExecuteClosureUpvalueForLoopBatch 验证官方 closure_upvalue 循环 batch。
func TestVMTryExecuteClosureUpvalueForLoopBatch(t *testing.T) {
	proto := testClosureUpvalueForLoopProto()
	vm := NewVMWithConstants(8, nil)
	vm.BindPrototype(proto)
	if !vm.PrepareClosureUpvalueForLoopSuperInstructions() {
		// 官方 closure_upvalue 字节码形态应能预构建 superinstruction。
		t.Fatalf("expected closure_upvalue superinstruction")
	}
	cell := NewClosedUpvalueCell(IntegerValue(0))
	closure := NewLuaClosure(testLeafUpvalueAddSetReturnParamProto(), []Value{IntegerValue(0)}, []*UpvalueCell{cell})
	if closure.LeafUpvalueAddSetReturn == nil || !closure.LeafUpvalueAddSetReturn.HasRegisterOperand {
		// 被调闭包必须命中 upvalue 加参数后写回的叶子形态。
		t.Fatalf("expected upvalue add-set register metadata")
	}
	for _, entry := range []struct {
		register int
		value    Value
	}{
		{register: 0, value: ReferenceValue(KindLuaClosure, closure)},
		{register: 1, value: IntegerValue(0)},
		{register: 2, value: IntegerValue(1)},
		{register: 3, value: IntegerValue(3)},
		{register: 4, value: IntegerValue(1)},
		{register: 5, value: IntegerValue(1)},
	} {
		if err := vm.SetRegister(entry.register, entry.value); err != nil {
			// 测试夹具寄存器必须能成功初始化。
			t.Fatalf("set register %d failed: %v", entry.register, err)
		}
	}

	batch, ok := vm.PrepareClosureUpvalueForLoopBatch(0)
	if !ok {
		// batch 准备应复用已验证的静态形态和 callee metadata。
		t.Fatalf("expected prepared closure_upvalue batch")
	}
	nextPC, iterations, handled, err := vm.TryExecuteClosureUpvalueForLoopBatch(batch, 8, NewState())
	if err != nil || !handled || iterations != 3 || nextPC != 5 {
		// 三轮后达到 limit 并退出循环。
		t.Fatalf("closure_upvalue batch mismatch: handled=%v iterations=%d nextPC=%d err=%v", handled, iterations, nextPC, err)
	}
	if value, _ := vm.Register(1); !value.RawEqual(IntegerValue(3)) {
		// sum 保存最后一次 inc(1) 返回值。
		t.Fatalf("sum mismatch: %#v", value)
	}
	if value, _ := vm.Register(6); !value.RawEqual(IntegerValue(3)) {
		// CALL 函数槽在单返回后保存最后一轮 inc 的结果。
		t.Fatalf("call result mismatch: %#v", value)
	}
	if value, _ := vm.Register(7); !value.RawEqual(IntegerValue(1)) {
		// 实参槽保留最后一轮 LOADK 后的常量 1。
		t.Fatalf("argument mismatch: %#v", value)
	}
	if !cell.Value().RawEqual(IntegerValue(3)) || !closure.Upvalues[0].RawEqual(IntegerValue(3)) {
		// upvalue cell 和 closure 快照都必须同步到最后结果。
		t.Fatalf("upvalue mismatch: cell=%#v snapshot=%#v", cell.Value(), closure.Upvalues[0])
	}
	if value, _ := vm.Register(2); !value.RawEqual(IntegerValue(3)) {
		// 循环退出时 FORLOOP 不写入越界后的内部 index。
		t.Fatalf("for index mismatch: %#v", value)
	}
	if value, _ := vm.Register(5); !value.RawEqual(IntegerValue(3)) {
		// 循环退出时外部可见循环变量保持最后一次有效值。
		t.Fatalf("visible index mismatch: %#v", value)
	}
}

// TestVMTryExecuteClosureUpvalueForLoopFallback 验证 closure_upvalue batch 动态 guard 失败无副作用。
func TestVMTryExecuteClosureUpvalueForLoopFallback(t *testing.T) {
	proto := testClosureUpvalueForLoopProto()
	vm := NewVMWithConstants(8, nil)
	vm.BindPrototype(proto)
	if !vm.PrepareClosureUpvalueForLoopSuperInstructions() {
		// 官方 closure_upvalue 字节码形态应能预构建 superinstruction。
		t.Fatalf("expected closure_upvalue superinstruction")
	}
	cell := NewClosedUpvalueCell(StringValue("not integer"))
	closure := NewLuaClosure(testLeafUpvalueAddSetReturnParamProto(), []Value{StringValue("not integer")}, []*UpvalueCell{cell})
	if err := vm.SetRegister(0, ReferenceValue(KindLuaClosure, closure)); err != nil {
		// 被调函数局部必须能写入寄存器。
		t.Fatalf("set function failed: %v", err)
	}
	if err := vm.SetRegister(1, IntegerValue(99)); err != nil {
		// sum 初值必须能写入寄存器。
		t.Fatalf("set sum failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(1)); err != nil {
		// FORLOOP 内部 index 必须能写入寄存器。
		t.Fatalf("set for index failed: %v", err)
	}
	if err := vm.SetRegister(3, IntegerValue(3)); err != nil {
		// FORLOOP limit 必须能写入寄存器。
		t.Fatalf("set for limit failed: %v", err)
	}
	if err := vm.SetRegister(4, IntegerValue(1)); err != nil {
		// FORLOOP step 必须能写入寄存器。
		t.Fatalf("set for step failed: %v", err)
	}

	batch, ok := vm.PrepareClosureUpvalueForLoopBatch(0)
	if !ok {
		// upvalue 类型不是 batch 静态 guard；应延迟到执行期回退。
		t.Fatalf("expected prepared closure_upvalue batch")
	}
	nextPC, iterations, handled, err := vm.TryExecuteClosureUpvalueForLoopBatch(batch, 8, NewState())
	if err != nil || handled || iterations != 0 || nextPC != 0 {
		// 动态 guard 失败不应被 fast path 消费。
		t.Fatalf("fallback mismatch: handled=%v iterations=%d nextPC=%d err=%v", handled, iterations, nextPC, err)
	}
	if value, _ := vm.Register(1); !value.RawEqual(IntegerValue(99)) {
		// guard 失败不能修改 sum。
		t.Fatalf("sum should be unchanged: %#v", value)
	}
	if value, _ := vm.Register(6); !value.IsNil() {
		// guard 失败不能执行第一条 MOVE。
		t.Fatalf("temporary slot should stay nil: %#v", value)
	}
}

// TestVMTryExecuteFormatLenAddForLoop 验证 `#string.format("%d", i)` 长度消费 fast path。
//
// 该路径必须在不构造格式化字符串的情况下，产生与 CALL、LEN、ADD、FORLOOP 后一致的寄存器和 PC 状态。
func TestVMTryExecuteFormatLenAddForLoop(t *testing.T) {
	proto := testFormatLenAddForLoopProto()
	globals := testGlobalsWithFormatFixedFunction(true)
	vm := NewVMWithConstantsAndUpvalues(9, proto.Constants, []Value{ReferenceValue(KindTable, globals)})
	vm.BindPrototype(proto)
	if !vm.PrepareFormatLenAddForLoopSuperInstructions() {
		// 官方 stdlib_math_string 尾部字节码形态应能预构建 superinstruction。
		t.Fatalf("expected format length superinstruction")
	}
	registers := map[int]Value{
		0: IntegerValue(0),
		1: IntegerValue(1),
		2: IntegerValue(3),
		3: IntegerValue(1),
		4: IntegerValue(12),
		5: IntegerValue(100),
	}
	for registerIndex, value := range registers {
		// 初始化 sum、FORLOOP 控制槽、当前格式化参数和前序累加临时值。
		if err := vm.SetRegister(registerIndex, value); err != nil {
			t.Fatalf("set register %d failed: %v", registerIndex, err)
		}
	}

	nextPC, handled := vm.TryExecuteFormatLenAddForLoop(8)
	if !handled || nextPC != 0 {
		// 第一轮循环未结束时必须跳回完整循环体入口。
		t.Fatalf("format length fast path mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, _ := vm.Register(0); !value.RawEqual(IntegerValue(102)) {
		// sum = 前序累加临时值 100 + len("12")。
		t.Fatalf("sum mismatch: %#v", value)
	}
	if value, _ := vm.Register(6); !value.RawEqual(IntegerValue(2)) {
		// CALL/LEN 结果槽最终应保存字符串长度。
		t.Fatalf("length slot mismatch: %#v", value)
	}
	if value, _ := vm.Register(7); !value.RawEqual(StringValue("%d")) {
		// 格式串实参槽应保持 LOADK 后的 `%d` 常量。
		t.Fatalf("format argument mismatch: %#v", value)
	}
	if value, _ := vm.Register(8); !value.RawEqual(IntegerValue(12)) {
		// 值实参槽应保持 MOVE 后的当前循环变量旧值。
		t.Fatalf("value argument mismatch: %#v", value)
	}
	if value, _ := vm.Register(1); !value.RawEqual(IntegerValue(2)) {
		// FORLOOP 继续时更新内部 index。
		t.Fatalf("for index mismatch: %#v", value)
	}
	if value, _ := vm.Register(4); !value.RawEqual(IntegerValue(2)) {
		// FORLOOP 继续时更新外部可见循环变量。
		t.Fatalf("visible index mismatch: %#v", value)
	}
	if vm.currentPC != 15 || vm.pcOffset != -16 {
		// fast path 后 VM 状态应等价于刚执行完 FORLOOP。
		t.Fatalf("pc state mismatch: currentPC=%d pcOffset=%d", vm.currentPC, vm.pcOffset)
	}
}

// TestVMTryExecuteFormatLenAddForLoopFallback 验证 format 长度消费 guard 失败无副作用。
func TestVMTryExecuteFormatLenAddForLoopFallback(t *testing.T) {
	proto := testFormatLenAddForLoopProto()
	globals := testGlobalsWithFormatFixedFunction(false)
	vm := NewVMWithConstantsAndUpvalues(9, proto.Constants, []Value{ReferenceValue(KindTable, globals)})
	vm.BindPrototype(proto)
	if !vm.PrepareFormatLenAddForLoopSuperInstructions() {
		// 字节码形态仍应能预构建，执行期由函数身份 guard 决定是否命中。
		t.Fatalf("expected format length superinstruction")
	}
	if err := vm.SetRegister(0, IntegerValue(10)); err != nil {
		// sum 初值必须能写入寄存器。
		t.Fatalf("set sum failed: %v", err)
	}
	if err := vm.SetRegister(4, IntegerValue(12)); err != nil {
		// 外部循环变量必须能写入寄存器。
		t.Fatalf("set visible index failed: %v", err)
	}
	if err := vm.SetRegister(5, IntegerValue(100)); err != nil {
		// 前序累加临时值必须能写入寄存器。
		t.Fatalf("set accumulator failed: %v", err)
	}

	nextPC, handled := vm.TryExecuteFormatLenAddForLoop(8)
	if handled || nextPC != 0 {
		// 未带标准库 fast-path 标记的 Go closure 不应被表达式级消费。
		t.Fatalf("fallback mismatch: handled=%v nextPC=%d", handled, nextPC)
	}
	if value, _ := vm.Register(0); !value.RawEqual(IntegerValue(10)) {
		// guard 失败不能提前修改 sum。
		t.Fatalf("sum should be unchanged: %#v", value)
	}
	if value, _ := vm.Register(6); !value.IsNil() {
		// guard 失败不能执行 GETTABUP。
		t.Fatalf("temporary slot should stay nil: %#v", value)
	}
}

// testFunctionCallAddForLoopProto 构造官方 function_call benchmark 的循环体 Proto。
func testFunctionCallAddForLoopProto() *bytecode.Proto {
	return &bytecode.Proto{
		Constants: []bytecode.Constant{
			bytecode.IntegerConstant(1),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpMove, 6, 0, 0),
			bytecode.CreateABC(bytecode.OpMove, 7, 5, 0),
			bytecode.CreateABx(bytecode.OpLoadK, 8, 0),
			bytecode.CreateABC(bytecode.OpCall, 6, 3, 2),
			bytecode.CreateABC(bytecode.OpAdd, 1, 1, 6),
			bytecode.CreateAsBx(bytecode.OpForLoop, 2, -6),
		},
	}
}

// testTableWriteForLoopProto 构造官方 table_rw 写入段的 `t[i] = i` 循环体 Proto。
func testTableWriteForLoopProto() *bytecode.Proto {
	return &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpSetTable, 0, 4, 4),
			bytecode.CreateAsBx(bytecode.OpForLoop, 1, -2),
		},
	}
}

// testFunctionCallAssignForLoopProto 构造官方完整 benchmark 的 `sum = add(sum, i)` 循环体 Proto。
func testFunctionCallAssignForLoopProto() *bytecode.Proto {
	return &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpMove, 6, 0, 0),
			bytecode.CreateABC(bytecode.OpMove, 7, 1, 0),
			bytecode.CreateABC(bytecode.OpMove, 8, 5, 0),
			bytecode.CreateABC(bytecode.OpCall, 6, 3, 2),
			bytecode.CreateABC(bytecode.OpMove, 1, 6, 0),
			bytecode.CreateAsBx(bytecode.OpForLoop, 2, -6),
		},
	}
}

// testClosureUpvalueForLoopProto 构造官方 closure_upvalue benchmark 的 `sum = inc(1)` 循环体 Proto。
func testClosureUpvalueForLoopProto() *bytecode.Proto {
	return &bytecode.Proto{
		Constants: []bytecode.Constant{
			bytecode.IntegerConstant(1),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpMove, 6, 0, 0),
			bytecode.CreateABx(bytecode.OpLoadK, 7, 0),
			bytecode.CreateABC(bytecode.OpCall, 6, 2, 2),
			bytecode.CreateABC(bytecode.OpMove, 1, 6, 0),
			bytecode.CreateAsBx(bytecode.OpForLoop, 2, -5),
		},
	}
}

// testLeafUpvalueAddSetReturnParamProto 构造 `x = x + v; return x` 叶子闭包 Proto。
func testLeafUpvalueAddSetReturnParamProto() *bytecode.Proto {
	return &bytecode.Proto{
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpGetUpval, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpAdd, 1, 1, 0),
			bytecode.CreateABC(bytecode.OpSetupVal, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpGetUpval, 1, 0, 0),
			bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0),
		},
	}
}

// testFormatLenAddForLoopProto 构造官方 stdlib_math_string 的 format/LEN 循环尾部 Proto。
func testFormatLenAddForLoopProto() *bytecode.Proto {
	return &bytecode.Proto{
		Constants: []bytecode.Constant{
			bytecode.StringConstant("string"),
			bytecode.StringConstant("format"),
			bytecode.StringConstant("%d"),
		},
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpMove, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpMove, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpMove, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpMove, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpMove, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpMove, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpMove, 0, 0, 0),
			bytecode.CreateABC(bytecode.OpAdd, 5, 0, 5),
			bytecode.CreateABC(bytecode.OpGetTabUp, 6, 0, bytecode.RKAsK(0)),
			bytecode.CreateABC(bytecode.OpGetTable, 6, 6, bytecode.RKAsK(1)),
			bytecode.CreateABx(bytecode.OpLoadK, 7, 2),
			bytecode.CreateABC(bytecode.OpMove, 8, 4, 0),
			bytecode.CreateABC(bytecode.OpCall, 6, 3, 2),
			bytecode.CreateABC(bytecode.OpLen, 6, 6, 0),
			bytecode.CreateABC(bytecode.OpAdd, 0, 5, 6),
			bytecode.CreateAsBx(bytecode.OpForLoop, 1, -16),
		},
	}
}

// testGlobalsWithFormatFixedFunction 构造包含 string.format 的最小全局表。
func testGlobalsWithFormatFixedFunction(marked bool) *Table {
	stringTable := NewTable()
	fastPathID := GoFixedResultsFastPathNone
	if marked {
		// 标记为标准库 exact `%d` 固定结果函数，允许表达式级长度消费。
		fastPathID = GoFixedResultsFastPathStringFormatDecimal
	}
	stringTable.RawSetString("format", ReferenceValue(KindGoClosure, &GoFixedResultsFunction{MaxResults: 1, FastPathID: fastPathID}))
	globals := NewTable()
	globals.RawSetString("string", ReferenceValue(KindTable, stringTable))
	return globals
}

// testLeafAddReturnProto 构造 `return a + b` 叶子函数 Proto。
func testLeafAddReturnProto() *bytecode.Proto {
	return &bytecode.Proto{
		NumParams:    2,
		MaxStackSize: 3,
		Code: []bytecode.Instruction{
			bytecode.CreateABC(bytecode.OpAdd, 2, 0, 1),
			bytecode.CreateABC(bytecode.OpReturn, 2, 2, 0),
		},
	}
}

// TestVMBinaryArithmeticErrors 验证二元算术指令的错误边界。
//
// 算术错误必须返回明确错误，并保持目标寄存器原值。
func TestVMBinaryArithmeticErrors(t *testing.T) {
	tests := []struct {
		name          string
		opCode        bytecode.OpCode
		constants     []bytecode.Constant
		expectedError error
	}{
		{name: "invalid operand", opCode: bytecode.OpAdd, constants: []bytecode.Constant{bytecode.StringConstant("bad"), bytecode.IntegerConstant(1)}, expectedError: ErrArithmeticOperand},
		{name: "mod zero", opCode: bytecode.OpMod, constants: []bytecode.Constant{bytecode.IntegerConstant(1), bytecode.IntegerConstant(0)}, expectedError: ErrDivisionByZero},
		{name: "idiv zero", opCode: bytecode.OpIDiv, constants: []bytecode.Constant{bytecode.IntegerConstant(1), bytecode.IntegerConstant(0)}, expectedError: ErrDivisionByZero},
	}

	for _, testCase := range tests {
		// 每个错误用例独立构造 VM，确保目标寄存器原值可验证。
		vm := NewVMWithConstants(1, testCase.constants)
		if err := vm.SetRegister(0, StringValue("keep")); err != nil {
			// 测试准备阶段写入目标寄存器必须成功。
			t.Fatalf("%s set target failed: %v", testCase.name, err)
		}
		err := vm.Step(bytecode.CreateABC(testCase.opCode, 0, bytecode.RKAsK(0), bytecode.RKAsK(1)))
		if !errors.Is(err, testCase.expectedError) {
			// 算术错误必须匹配预期错误类型。
			t.Fatalf("%s error mismatch: %v", testCase.name, err)
		}
		value, ok := vm.Register(0)
		if !ok || !value.RawEqual(StringValue("keep")) {
			// 算术失败不能覆盖目标寄存器。
			t.Fatalf("%s should keep target: value=%#v ok=%v", testCase.name, value, ok)
		}
	}
}

// TestVMBinaryBitwiseInstructions 验证二元位运算指令的基础语义。
//
// 本测试覆盖 BAND/BOR/BXOR/SHL/SHR，所有操作都按 64 位补码位模式执行。
func TestVMBinaryBitwiseInstructions(t *testing.T) {
	tests := []struct {
		name          string
		opCode        bytecode.OpCode
		constants     []bytecode.Constant
		expectedValue Value
	}{
		{name: "band", opCode: bytecode.OpBAnd, constants: []bytecode.Constant{bytecode.IntegerConstant(12), bytecode.IntegerConstant(10)}, expectedValue: IntegerValue(8)},
		{name: "bor", opCode: bytecode.OpBOr, constants: []bytecode.Constant{bytecode.IntegerConstant(12), bytecode.IntegerConstant(10)}, expectedValue: IntegerValue(14)},
		{name: "bxor", opCode: bytecode.OpBXor, constants: []bytecode.Constant{bytecode.IntegerConstant(12), bytecode.IntegerConstant(10)}, expectedValue: IntegerValue(6)},
		{name: "shl", opCode: bytecode.OpShl, constants: []bytecode.Constant{bytecode.IntegerConstant(1), bytecode.IntegerConstant(3)}, expectedValue: IntegerValue(8)},
		{name: "shr", opCode: bytecode.OpShr, constants: []bytecode.Constant{bytecode.IntegerConstant(8), bytecode.IntegerConstant(2)}, expectedValue: IntegerValue(2)},
		{name: "negative shl becomes shr", opCode: bytecode.OpShl, constants: []bytecode.Constant{bytecode.IntegerConstant(8), bytecode.IntegerConstant(-1)}, expectedValue: IntegerValue(4)},
		{name: "hex float bitwise coercion", opCode: bytecode.OpBOr, constants: []bytecode.Constant{bytecode.NumberConstant(0xF0), bytecode.StringConstant("0xAA.0")}, expectedValue: IntegerValue(250)},
	}

	for _, testCase := range tests {
		// 每个 bitwise opcode 独立构造 VM，避免寄存器状态在用例间相互污染。
		vm := NewVMWithConstants(1, testCase.constants)
		instruction := bytecode.CreateABC(testCase.opCode, 0, bytecode.RKAsK(0), bytecode.RKAsK(1))
		if err := vm.Step(instruction); err != nil {
			// 合法位运算指令必须执行成功。
			t.Fatalf("%s failed: %v", testCase.name, err)
		}
		value, ok := vm.Register(0)
		if !ok || !value.RawEqual(testCase.expectedValue) {
			// 目标寄存器必须保存该位运算 opcode 的预期结果。
			t.Fatalf("%s value mismatch: value=%#v ok=%v", testCase.name, value, ok)
		}
	}
}

// TestVMBinaryBitwiseErrors 验证位运算遇到非 integer 操作数时返回错误。
//
// Lua 5.3 位运算要求操作数可转换为 integer，不能把任意 float 或 table 当作位模式。
func TestVMBinaryBitwiseErrors(t *testing.T) {
	vm := NewVMWithConstants(1, []bytecode.Constant{
		bytecode.NumberConstant(1.5),
		bytecode.IntegerConstant(1),
	})
	if err := vm.SetRegister(0, StringValue("keep")); err != nil {
		// 测试准备阶段写入目标寄存器必须成功。
		t.Fatalf("set target failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpBAnd, 0, bytecode.RKAsK(0), bytecode.RKAsK(1)))
	if !errors.Is(err, ErrIntegerOperand) {
		// 不可整数化的 float 必须返回 ErrIntegerOperand。
		t.Fatalf("bitwise error mismatch: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("keep")) {
		// 位运算失败不能覆盖目标寄存器。
		t.Fatalf("bitwise should keep target: value=%#v ok=%v", value, ok)
	}

	overflowVM := NewVMWithConstants(1, []bytecode.Constant{
		bytecode.NumberConstant(-float64(math.MinInt64)),
		bytecode.IntegerConstant(1),
	})
	err = overflowVM.Step(bytecode.CreateABC(bytecode.OpBAnd, 0, bytecode.RKAsK(0), bytecode.RKAsK(1)))
	if !errors.Is(err, ErrIntegerOperand) {
		// 2^63 超出 Lua integer 数学范围，位运算必须拒绝。
		t.Fatalf("bitwise overflow error mismatch: %v", err)
	}
}

// TestVMUnaryInstructions 验证 UNM、BNOT 和 NOT 的基础语义。
//
// 一元指令只读取 B 寄存器并写入 A 寄存器，当前测试覆盖 integer、bitwise 和 truthy 语义。
func TestVMUnaryInstructions(t *testing.T) {
	vm := NewVM(5)
	if err := vm.SetRegister(1, IntegerValue(7)); err != nil {
		// 测试准备阶段写入整数寄存器必须成功。
		t.Fatalf("set integer failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(0)); err != nil {
		// 测试准备阶段写入零值寄存器必须成功。
		t.Fatalf("set zero failed: %v", err)
	}
	if err := vm.SetRegister(3, BooleanValue(false)); err != nil {
		// 测试准备阶段写入 false 寄存器必须成功。
		t.Fatalf("set false failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpUnm, 0, 1, 0)); err != nil {
		// UNM 作用于 integer 必须成功。
		t.Fatalf("unm failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpBNot, 4, 2, 0)); err != nil {
		// BNOT 作用于 integer 必须成功。
		t.Fatalf("bnot failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpNot, 2, 3, 0)); err != nil {
		// NOT 作用于 false 必须成功。
		t.Fatalf("not failed: %v", err)
	}

	negativeValue, negativeOK := vm.Register(0)
	bitwiseValue, bitwiseOK := vm.Register(4)
	notValue, notOK := vm.Register(2)
	if !negativeOK || !negativeValue.RawEqual(IntegerValue(-7)) {
		// UNM 必须得到整数负值。
		t.Fatalf("unm value mismatch: value=%#v ok=%v", negativeValue, negativeOK)
	}
	if !bitwiseOK || !bitwiseValue.RawEqual(IntegerValue(-1)) {
		// BNOT 作用于 0 必须翻转为所有 bit 为 1，即 int64(-1)。
		t.Fatalf("bnot value mismatch: value=%#v ok=%v", bitwiseValue, bitwiseOK)
	}
	if !notOK || !notValue.RawEqual(BooleanValue(true)) {
		// NOT 作用于 false 必须得到 true。
		t.Fatalf("not value mismatch: value=%#v ok=%v", notValue, notOK)
	}
}

// TestVMUnaryMinusPreservesNegativeZero 验证 OP_UNM 对 float 零保留 IEEE-754 负零。
//
// Lua 5.3 官方 strings.lua 会通过 string.format("%a", -0.0) 观察负零符号；若一元负号先走
// integer 转换路径，结果会错误变成整数 0。
func TestVMUnaryMinusPreservesNegativeZero(t *testing.T) {
	vm := NewVM(2)
	if err := vm.SetRegister(1, NumberValue(0.0)); err != nil {
		// 测试准备阶段写入 float 零必须成功。
		t.Fatalf("set float zero failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpUnm, 0, 1, 0)); err != nil {
		// UNM 作用于 float 零必须成功。
		t.Fatalf("unm float zero failed: %v", err)
	}

	negativeZero, ok := vm.Register(0)
	if !ok || negativeZero.Kind != KindNumber || !math.Signbit(negativeZero.Number) || negativeZero.Number != 0 {
		// 结果必须仍是 number，且保留负零符号位。
		t.Fatalf("unm float zero mismatch: value=%#v ok=%v", negativeZero, ok)
	}
}

// TestVMUnaryErrors 验证一元指令错误路径保持目标寄存器不变。
//
// UNM 与 BNOT 的转换错误不能覆盖目标寄存器，源寄存器越界也必须明确返回寄存器错误。
func TestVMUnaryErrors(t *testing.T) {
	vm := NewVM(2)
	if err := vm.SetRegister(0, StringValue("keep")); err != nil {
		// 测试准备阶段写入目标寄存器必须成功。
		t.Fatalf("set target failed: %v", err)
	}
	if err := vm.SetRegister(1, StringValue("bad")); err != nil {
		// 测试准备阶段写入非法操作数必须成功。
		t.Fatalf("set operand failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpUnm, 0, 1, 0))
	if !errors.Is(err, ErrArithmeticOperand) {
		// UNM 遇到不可转换 number 的 string 必须返回 ErrArithmeticOperand。
		t.Fatalf("unm error mismatch: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("keep")) {
		// UNM 失败不能覆盖目标寄存器。
		t.Fatalf("unm should keep target: value=%#v ok=%v", value, ok)
	}

	err = vm.Step(bytecode.CreateABC(bytecode.OpBNot, 0, 1, 0))
	if !errors.Is(err, ErrIntegerOperand) {
		// BNOT 遇到不可转换 integer 的 string 必须返回 ErrIntegerOperand。
		t.Fatalf("bnot error mismatch: %v", err)
	}
	value, ok = vm.Register(0)
	if !ok || !value.RawEqual(StringValue("keep")) {
		// BNOT 失败不能覆盖目标寄存器。
		t.Fatalf("bnot should keep target: value=%#v ok=%v", value, ok)
	}
}

// TestVMLength 验证 LEN 支持 string 字节长度和 table 长度。
//
// 当前阶段不触发 `__len` 元方法，只覆盖 Lua 5.3 基础 string/table 长度路径。
func TestVMLength(t *testing.T) {
	table := NewTable()
	table.RawSetInteger(1, StringValue("a"))
	table.RawSetInteger(2, StringValue("b"))
	vm := NewVM(3)
	if err := vm.SetRegister(1, StringValue("lua")); err != nil {
		// 测试准备阶段写入 string 源寄存器必须成功。
		t.Fatalf("set string source failed: %v", err)
	}
	if err := vm.SetRegister(2, ReferenceValue(KindTable, table)); err != nil {
		// 测试准备阶段写入 table 源寄存器必须成功。
		t.Fatalf("set table source failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpLen, 0, 1, 0)); err != nil {
		// LEN 作用于 string 必须成功。
		t.Fatalf("len string failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(IntegerValue(3)) {
		// string 长度必须按字节数写入目标寄存器。
		t.Fatalf("len string mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpLen, 0, 2, 0)); err != nil {
		// LEN 作用于 table 必须成功。
		t.Fatalf("len table failed: %v", err)
	}
	value, ok = vm.Register(0)
	if !ok || !value.RawEqual(IntegerValue(2)) {
		// table 长度必须使用当前 Table.Len 边界。
		t.Fatalf("len table mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMLengthErrors 验证 LEN 错误路径保持目标寄存器不变。
//
// 非 string/table 类型当前没有基础长度语义，必须返回 ErrLengthOperand。
func TestVMLengthErrors(t *testing.T) {
	vm := NewVM(2)
	if err := vm.SetRegister(0, StringValue("keep")); err != nil {
		// 测试准备阶段写入目标寄存器必须成功。
		t.Fatalf("set target failed: %v", err)
	}
	if err := vm.SetRegister(1, BooleanValue(true)); err != nil {
		// 测试准备阶段写入非法操作数必须成功。
		t.Fatalf("set source failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpLen, 0, 1, 0))
	if !errors.Is(err, ErrLengthOperand) {
		// boolean 当前不能执行 LEN，必须返回 ErrLengthOperand。
		t.Fatalf("len error mismatch: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("keep")) {
		// LEN 失败不能覆盖目标寄存器。
		t.Fatalf("len should keep target: value=%#v ok=%v", value, ok)
	}
}

// TestVMConcat 验证 CONCAT 会按寄存器顺序拼接 string 和 number。
//
// Lua 5.3 CONCAT 支持 string/number 基础路径，其他类型需要后续 `__concat` 元方法回退。
func TestVMConcat(t *testing.T) {
	vm := NewVM(6)
	if err := vm.SetRegister(1, StringValue("lua")); err != nil {
		// 测试准备阶段写入第一个片段必须成功。
		t.Fatalf("set first part failed: %v", err)
	}
	if err := vm.SetRegister(2, IntegerValue(5)); err != nil {
		// 测试准备阶段写入第二个片段必须成功。
		t.Fatalf("set second part failed: %v", err)
	}
	if err := vm.SetRegister(3, NumberValue(3.0)); err != nil {
		// 测试准备阶段写入第三个片段必须成功。
		t.Fatalf("set third part failed: %v", err)
	}
	if err := vm.SetRegister(4, StringValue("-")); err != nil {
		// 测试准备阶段写入第四个片段必须成功。
		t.Fatalf("set fourth part failed: %v", err)
	}
	if err := vm.SetRegister(5, StringValue("vm")); err != nil {
		// 测试准备阶段写入第五个片段必须成功。
		t.Fatalf("set fifth part failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpConcat, 0, 1, 5)); err != nil {
		// CONCAT 作用于 string/number 区间必须成功。
		t.Fatalf("concat failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("lua53.0-vm")) {
		// CONCAT 必须按 R(B)..R(C) 顺序写入拼接结果。
		t.Fatalf("concat value mismatch: value=%#v ok=%v", value, ok)
	}
}

// TestVMConcatErrors 验证 CONCAT 错误路径保持目标寄存器不变。
//
// 拼接区间中任一值不能转换为 string 时，目标寄存器不能被部分结果覆盖。
func TestVMConcatErrors(t *testing.T) {
	vm := NewVM(3)
	if err := vm.SetRegister(0, StringValue("keep")); err != nil {
		// 测试准备阶段写入目标寄存器必须成功。
		t.Fatalf("set target failed: %v", err)
	}
	if err := vm.SetRegister(1, StringValue("a")); err != nil {
		// 测试准备阶段写入合法片段必须成功。
		t.Fatalf("set first part failed: %v", err)
	}
	if err := vm.SetRegister(2, BooleanValue(true)); err != nil {
		// 测试准备阶段写入非法片段必须成功。
		t.Fatalf("set invalid part failed: %v", err)
	}

	err := vm.Step(bytecode.CreateABC(bytecode.OpConcat, 0, 1, 2))
	if !errors.Is(err, ErrConcatOperand) {
		// boolean 当前不能参与 CONCAT，必须返回 ErrConcatOperand。
		t.Fatalf("concat error mismatch: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("keep")) {
		// CONCAT 失败不能覆盖目标寄存器。
		t.Fatalf("concat should keep target: value=%#v ok=%v", value, ok)
	}
}

// TestVMComparisonInstructions 验证 EQ、LT、LE 会按 Lua 5.3 测试指令语义设置 skipNext。
//
// 当前最小 VM 没有完整 pc，测试指令通过 SkipNext 暴露是否跳过下一条指令。
func TestVMComparisonInstructions(t *testing.T) {
	tests := []struct {
		name     string
		opCode   bytecode.OpCode
		a        int
		left     bytecode.Constant
		right    bytecode.Constant
		skipNext bool
	}{
		{name: "eq expected true matched", opCode: bytecode.OpEq, a: 1, left: bytecode.IntegerConstant(1), right: bytecode.IntegerConstant(1), skipNext: false},
		{name: "eq expected false mismatched", opCode: bytecode.OpEq, a: 0, left: bytecode.IntegerConstant(1), right: bytecode.IntegerConstant(2), skipNext: false},
		{name: "eq expected true mismatched", opCode: bytecode.OpEq, a: 1, left: bytecode.IntegerConstant(1), right: bytecode.IntegerConstant(2), skipNext: true},
		{name: "lt number", opCode: bytecode.OpLt, a: 1, left: bytecode.IntegerConstant(1), right: bytecode.NumberConstant(2.5), skipNext: false},
		{name: "lt min integer precision", opCode: bytecode.OpLt, a: 1, left: bytecode.IntegerConstant(math.MinInt64), right: bytecode.IntegerConstant(math.MinInt64 + 1), skipNext: false},
		{name: "lt max integer against float boundary", opCode: bytecode.OpLt, a: 1, left: bytecode.IntegerConstant(math.MaxInt64), right: bytecode.NumberConstant(-float64(math.MinInt64)), skipNext: false},
		{name: "lt integer against nearby fractional float", opCode: bytecode.OpLt, a: 1, left: bytecode.IntegerConstant(-1), right: bytecode.NumberConstant(-0.9), skipNext: false},
		{name: "le float against nearby integer", opCode: bytecode.OpLe, a: 1, left: bytecode.NumberConstant(1.1), right: bytecode.IntegerConstant(1), skipNext: true},
		{name: "le string", opCode: bytecode.OpLe, a: 1, left: bytecode.StringConstant("a"), right: bytecode.StringConstant("b"), skipNext: false},
	}

	for _, testCase := range tests {
		// 每个比较用例独立构造 VM，避免 skipNext 状态相互影响。
		vm := NewVMWithConstants(1, []bytecode.Constant{testCase.left, testCase.right})
		instruction := bytecode.CreateABC(testCase.opCode, testCase.a, bytecode.RKAsK(0), bytecode.RKAsK(1))
		if err := vm.Step(instruction); err != nil {
			// 合法比较指令必须执行成功。
			t.Fatalf("%s failed: %v", testCase.name, err)
		}
		if vm.SkipNext() != testCase.skipNext {
			// skipNext 必须反映 Lua 测试指令的 pc++ 条件。
			t.Fatalf("%s skip mismatch: got=%v want=%v", testCase.name, vm.SkipNext(), testCase.skipNext)
		}
	}
}

// TestVMComparisonRegisterIntegerConstantLT 验证寄存器 integer 小于 integer 常量的测试语义。
//
// 该形态覆盖递归边界判断 `n < constant` 的热路径，同时要求 A 字段仍按 Lua 5.3 测试指令规则控制 skipNext。
func TestVMComparisonRegisterIntegerConstantLT(t *testing.T) {
	tests := []struct {
		name     string
		a        int
		left     Value
		right    bytecode.Constant
		skipNext bool
	}{
		{name: "expected false matched", a: 0, left: IntegerValue(1), right: bytecode.IntegerConstant(2), skipNext: true},
		{name: "expected true matched", a: 1, left: IntegerValue(1), right: bytecode.IntegerConstant(2), skipNext: false},
		{name: "expected false mismatched", a: 0, left: IntegerValue(3), right: bytecode.IntegerConstant(2), skipNext: false},
		{name: "float fallback", a: 1, left: NumberValue(1.5), right: bytecode.IntegerConstant(2), skipNext: false},
	}

	for _, testCase := range tests {
		// 每个子用例独立构造 VM，避免寄存器和 skipNext 状态互相影响。
		vm := NewVMWithConstants(1, []bytecode.Constant{testCase.right})
		if err := vm.SetRegister(0, testCase.left); err != nil {
			// 测试寄存器初始化失败表示 VM 构造不符合用例预期。
			t.Fatalf("%s set register failed: %v", testCase.name, err)
		}
		if err := vm.Step(bytecode.CreateABC(bytecode.OpLt, testCase.a, 0, bytecode.RKAsK(0))); err != nil {
			// 合法 LT 指令必须执行成功。
			t.Fatalf("%s failed: %v", testCase.name, err)
		}
		if vm.SkipNext() != testCase.skipNext {
			// skipNext 必须与 Lua 5.3 `comparison ~= A` 的跳转语义一致。
			t.Fatalf("%s skip mismatch: got=%v want=%v", testCase.name, vm.SkipNext(), testCase.skipNext)
		}
	}
}

// TestVMComparisonErrors 验证 LT/LE 遇到不可比较类型时返回错误。
//
// 当前阶段不触发 `__lt` 或 `__le` 元方法，因此非 number/string 有序比较必须失败。
func TestVMComparisonErrors(t *testing.T) {
	vm := NewVMWithConstants(1, []bytecode.Constant{
		bytecode.BooleanConstant(true),
		bytecode.IntegerConstant(1),
	})

	err := vm.Step(bytecode.CreateABC(bytecode.OpLt, 1, bytecode.RKAsK(0), bytecode.RKAsK(1)))
	if !errors.Is(err, ErrCompareOperand) {
		// boolean 与 integer 当前不能执行 LT，必须返回 ErrCompareOperand。
		t.Fatalf("lt compare error mismatch: %v", err)
	}
}

// TestVMJump 验证 JMP 会记录 pc 偏移和 upvalue 关闭起点。
//
// 当前最小 VM 不直接维护 pc，只通过 PCOffset 与 CloseFrom 暴露执行循环需要消费的请求。
func TestVMJump(t *testing.T) {
	vm := NewVM(1)
	if err := vm.Step(bytecode.CreateAsBx(bytecode.OpJmp, 2, -3)); err != nil {
		// JMP 不访问寄存器窗口，合法指令必须成功。
		t.Fatalf("jmp failed: %v", err)
	}
	if vm.PCOffset() != -3 {
		// JMP 必须记录 sBx 偏移。
		t.Fatalf("jmp offset mismatch: %d", vm.PCOffset())
	}
	closeFrom, ok := vm.CloseFrom()
	if !ok || closeFrom != 1 {
		// A 非 0 时必须记录 A-1 作为 upvalue 关闭起点。
		t.Fatalf("jmp close mismatch: closeFrom=%d ok=%v", closeFrom, ok)
	}
}

// TestVMTestAndTestSet 验证 TEST 与 TESTSET 的 truthy 分支语义。
//
// TEST 只设置 skipNext；TESTSET 在条件满足时复制值，条件不满足时跳过下一条。
func TestVMTestAndTestSet(t *testing.T) {
	vm := NewVM(3)
	if err := vm.SetRegister(1, StringValue("truthy")); err != nil {
		// 测试准备阶段写入 truthy 值必须成功。
		t.Fatalf("set truthy failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpTest, 1, 0, 1)); err != nil {
		// TEST 期望 true 且源值 truthy 时必须成功。
		t.Fatalf("test failed: %v", err)
	}
	if vm.SkipNext() {
		// 条件满足时 TEST 不应跳过下一条。
		t.Fatalf("test should not skip")
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpTestSet, 0, 1, 1)); err != nil {
		// TESTSET 条件满足时必须复制源寄存器。
		t.Fatalf("testset copy failed: %v", err)
	}
	value, ok := vm.Register(0)
	if !ok || !value.RawEqual(StringValue("truthy")) {
		// 目标寄存器必须获得源值。
		t.Fatalf("testset copy mismatch: value=%#v ok=%v", value, ok)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpTestSet, 2, 1, 0)); err != nil {
		// TESTSET 条件不满足时也应执行成功，并只设置 skipNext。
		t.Fatalf("testset skip failed: %v", err)
	}
	if !vm.SkipNext() {
		// 条件不满足时 TESTSET 必须跳过下一条。
		t.Fatalf("testset should skip")
	}
}

// TestVMCallTailCallAndTForCall 验证调用类指令会生成调用请求。
//
// 当前最小 VM 不直接进入调用帧，只记录调用执行循环需要的函数寄存器、参数数量和返回数量。
func TestVMCallTailCallAndTForCall(t *testing.T) {
	vm := NewVM(8)
	if err := vm.SetRegister(1, ReferenceValue(KindGoClosure, GoFunction(func(args ...Value) (Value, error) {
		// CALL 测试只验证请求生成，不实际执行 Go closure。
		return NilValue(), nil
	}))); err != nil {
		// 测试准备阶段写入普通调用函数槽必须成功。
		t.Fatalf("set call function failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpCall, 1, 3, 2)); err != nil {
		// CALL 固定两个参数和一个返回值必须生成请求。
		t.Fatalf("call failed: %v", err)
	}
	request := vm.LastCallRequest()
	if request == nil || request.FunctionIndex != 1 || request.ArgumentCount != 2 || request.ReturnCount != 1 || request.Tail {
		// CALL 请求字段必须对齐 B/C 的减一编码。
		t.Fatalf("call request mismatch: %#v", request)
	}

	if err := vm.SetRegister(2, ReferenceValue(KindGoClosure, GoFunction(func(args ...Value) (Value, error) {
		// TAILCALL 测试只验证请求生成，不实际执行 Go closure。
		return NilValue(), nil
	}))); err != nil {
		// 测试准备阶段写入尾调用函数槽必须成功。
		t.Fatalf("set tailcall function failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpTailCall, 2, 2, 0)); err != nil {
		// TAILCALL 固定一个参数和开放返回必须生成尾调用请求。
		t.Fatalf("tailcall failed: %v", err)
	}
	request = vm.LastCallRequest()
	if request == nil || request.FunctionIndex != 2 || request.ArgumentCount != 1 || request.ReturnCount != -1 || !request.Tail {
		// TAILCALL 请求必须标记 Tail。
		t.Fatalf("tailcall request mismatch: %#v", request)
	}

	if err := vm.SetRegister(0, ReferenceValue(KindGoClosure, GoFunction(func(args ...Value) (Value, error) {
		// TFORCALL 测试只验证请求生成，不实际执行 Go closure。
		return NilValue(), nil
	}))); err != nil {
		// 测试准备阶段写入泛型 for 迭代器函数槽必须成功。
		t.Fatalf("set tforcall function failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpTForCall, 0, 0, 2)); err != nil {
		// TFORCALL 请求两个迭代结果必须成功。
		t.Fatalf("tforcall failed: %v", err)
	}
	request = vm.LastCallRequest()
	if request == nil || !request.GenericFor || request.FunctionIndex != 0 || request.ArgumentCount != 2 || request.ReturnCount != 2 || request.ResultIndex != 3 {
		// TFORCALL 请求必须标记泛型 for 调用和结果起点。
		t.Fatalf("tforcall request mismatch: %#v", request)
	}
}

// TestVMOpenVarargCall 验证 VARARG B=0 后的 CALL B=0 会按开放栈顶确定参数数量。
//
// Lua 5.3 在 `f(a, ...)` 这类调用中要求最后一个 vararg 展开为全部剩余参数；最小 VM 通过
// openTop 记录 VARARG 写入上界，并在 CALL 阶段折算为固定参数数供执行循环消费。
func TestVMOpenVarargCall(t *testing.T) {
	vm := NewVMWithPrototypeData(5, nil, nil, nil, []Value{StringValue("in"), StringValue("out")})
	if err := vm.SetRegister(0, ReferenceValue(KindGoClosure, GoFunction(func(args ...Value) (Value, error) {
		// CALL 测试只验证请求生成，不实际执行 Go closure。
		return NilValue(), nil
	}))); err != nil {
		// 测试准备阶段写入函数槽必须成功。
		t.Fatalf("set function failed: %v", err)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpVararg, 1, 0, 0)); err != nil {
		// VARARG B=0 应把全部 vararg 写入 R(1).. 并记录开放上界。
		t.Fatalf("open vararg failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpCall, 0, 0, 2)); err != nil {
		// CALL B=0 应消费前置开放上界并生成固定参数请求。
		t.Fatalf("open call failed: %v", err)
	}
	request := vm.LastCallRequest()
	if request == nil || request.FunctionIndex != 0 || request.ArgumentCount != 2 || request.ReturnCount != 1 {
		// 两个 vararg 值必须全部传入，且 C=2 表示一个返回值。
		t.Fatalf("open call request mismatch: %#v", request)
	}
}

// TestVMReturn 验证 RETURN 会收集返回值快照。
//
// RETURN 不直接弹出调用帧，当前最小 VM 先记录返回值供后续执行循环消费。
func TestVMReturn(t *testing.T) {
	vm := NewVM(4)
	_ = vm.SetRegister(1, StringValue("a"))
	_ = vm.SetRegister(2, IntegerValue(2))

	if err := vm.Step(bytecode.CreateABC(bytecode.OpReturn, 1, 3, 0)); err != nil {
		// RETURN 从 R(1) 返回两个值必须成功。
		t.Fatalf("return failed: %v", err)
	}
	values := vm.ReturnValues()
	if len(values) != 2 || !values[0].RawEqual(StringValue("a")) || !values[1].RawEqual(IntegerValue(2)) {
		// 返回值快照必须匹配 R(A)..R(A+B-2)。
		t.Fatalf("return values mismatch: %#v", values)
	}
}

// TestVMReturnNoValues 验证裸 RETURN 会被识别为已返回但没有返回值。
//
// Lua 5.3 的 `return` 使用 B=1 表示 0 个返回值；执行层需要区分它和“上一条指令不是 RETURN”，
// 否则裸 return 的 debug return hook 会被跳过。
func TestVMReturnNoValues(t *testing.T) {
	vm := NewVM(2)

	if err := vm.Step(bytecode.CreateABC(bytecode.OpReturn, 0, 1, 0)); err != nil {
		// 裸 RETURN 不读取返回值区间，应成功记录返回状态。
		t.Fatalf("empty return failed: %v", err)
	}
	values := vm.ReturnValues()
	if values == nil || len(values) != 0 {
		// 空但非 nil 的返回值切片表示已执行 RETURN 且返回 0 个值。
		t.Fatalf("empty return values mismatch: %#v", values)
	}
	if err := vm.Step(bytecode.CreateABC(bytecode.OpLoadNil, 0, 0, 0)); err != nil {
		// 后续普通指令不应失败。
		t.Fatalf("load nil failed: %v", err)
	}
	if values := vm.ReturnValues(); values != nil {
		// 下一条非 RETURN 指令必须清除上一条返回状态。
		t.Fatalf("return state should be cleared, got %#v", values)
	}
}

// TestVMForPrepAndForLoop 验证数值 for 的初始化和步进语义。
//
// FORPREP 先预减 index 并跳转；FORLOOP 步进后未越界则更新外部变量并跳转。
func TestVMForPrepAndForLoop(t *testing.T) {
	vm := NewVM(4)
	_ = vm.SetRegister(0, IntegerValue(1))
	_ = vm.SetRegister(1, IntegerValue(3))
	_ = vm.SetRegister(2, IntegerValue(1))

	if err := vm.Step(bytecode.CreateAsBx(bytecode.OpForPrep, 0, 4)); err != nil {
		// FORPREP 初始化 integer for 必须成功。
		t.Fatalf("forprep failed: %v", err)
	}
	value, _ := vm.Register(0)
	if !value.RawEqual(IntegerValue(0)) || vm.PCOffset() != 4 {
		// FORPREP 必须执行 init-step 并记录跳转。
		t.Fatalf("forprep mismatch: value=%#v offset=%d", value, vm.PCOffset())
	}

	if err := vm.Step(bytecode.CreateAsBx(bytecode.OpForLoop, 0, -2)); err != nil {
		// FORLOOP 第一次步进必须继续循环。
		t.Fatalf("forloop failed: %v", err)
	}
	indexValue, _ := vm.Register(0)
	externalValue, _ := vm.Register(3)
	if !indexValue.RawEqual(IntegerValue(1)) || !externalValue.RawEqual(IntegerValue(1)) || vm.PCOffset() != -2 {
		// FORLOOP 继续时必须更新内部 index、外部变量并记录跳转。
		t.Fatalf("forloop mismatch: index=%#v external=%#v offset=%d", indexValue, externalValue, vm.PCOffset())
	}
}

// TestVMForLoopMaxIntegerNegativeStep 验证 integer for 在最大整数负步长边界上的补码语义。
//
// Lua 5.3 的 integer for 控制寄存器必须保留 int64 精度；`math.maxinteger` 经 float64 会丢精度，
// 进而导致 `for i=max,max-10,-1` 错误地跳过循环。
func TestVMForLoopMaxIntegerNegativeStep(t *testing.T) {
	vm := NewVM(4)
	_ = vm.SetRegister(0, IntegerValue(math.MaxInt64))
	_ = vm.SetRegister(1, IntegerValue(math.MaxInt64-10))
	_ = vm.SetRegister(2, IntegerValue(-1))

	if err := vm.Step(bytecode.CreateAsBx(bytecode.OpForPrep, 0, 1)); err != nil {
		// FORPREP 必须允许 init-step 在 int64 补码语义下从 MaxInt64 溢出。
		t.Fatalf("forprep max integer negative step failed: %v", err)
	}
	preparedValue, _ := vm.Register(0)
	if !preparedValue.RawEqual(IntegerValue(math.MinInt64)) || vm.PCOffset() != 1 {
		// MaxInt64 - (-1) 会按 Lua integer 补码语义预减到 MinInt64。
		t.Fatalf("forprep max integer mismatch: value=%#v offset=%d", preparedValue, vm.PCOffset())
	}

	if err := vm.Step(bytecode.CreateAsBx(bytecode.OpForLoop, 0, -1)); err != nil {
		// FORLOOP 第一次步进必须恢复到 MaxInt64 并进入循环。
		t.Fatalf("forloop max integer negative step failed: %v", err)
	}
	indexValue, _ := vm.Register(0)
	externalValue, _ := vm.Register(3)
	if !indexValue.RawEqual(IntegerValue(math.MaxInt64)) || !externalValue.RawEqual(IntegerValue(math.MaxInt64)) || vm.PCOffset() != -1 {
		// 第一次循环体可见值必须是原始 MaxInt64，且继续跳转到循环体。
		t.Fatalf("forloop max integer mismatch: index=%#v external=%#v offset=%d", indexValue, externalValue, vm.PCOffset())
	}
}

// TestVMForLoopIntegerModeWithFloatLimit 验证 integer for 会按步长方向折算 float 边界。
//
// Lua 5.3 在初值和步长为 integer 时，会把 float limit 按正步长 floor、负步长 ceil 后继续
// 使用 integer 控制变量，`nextvar.lua` 依赖该语义检查 `math.type(i)`。
func TestVMForLoopIntegerModeWithFloatLimit(t *testing.T) {
	cases := []struct {
		name          string
		initialValue  Value
		limitValue    Value
		stepValue     Value
		preparedValue Value
		firstValue    Value
	}{
		{
			name:          "positive step floors float limit",
			initialValue:  IntegerValue(1),
			limitValue:    NumberValue(10.9),
			stepValue:     IntegerValue(1),
			preparedValue: IntegerValue(0),
			firstValue:    IntegerValue(1),
		},
		{
			name:          "negative step ceils float limit",
			initialValue:  IntegerValue(10),
			limitValue:    NumberValue(0.001),
			stepValue:     IntegerValue(-1),
			preparedValue: IntegerValue(11),
			firstValue:    IntegerValue(10),
		},
		{
			name:          "positive step accepts positive infinity",
			initialValue:  IntegerValue(1),
			limitValue:    NumberValue(math.Inf(1)),
			stepValue:     IntegerValue(1),
			preparedValue: IntegerValue(0),
			firstValue:    IntegerValue(1),
		},
	}

	for _, tc := range cases {
		// 每个边界组合使用独立 VM，避免控制寄存器互相污染。
		t.Run(tc.name, func(t *testing.T) {
			vm := NewVM(4)
			_ = vm.SetRegister(0, tc.initialValue)
			_ = vm.SetRegister(1, tc.limitValue)
			_ = vm.SetRegister(2, tc.stepValue)

			if err := vm.Step(bytecode.CreateAsBx(bytecode.OpForPrep, 0, 1)); err != nil {
				// FORPREP 必须接受可折算的 float 边界。
				t.Fatalf("forprep failed: %v", err)
			}
			preparedValue, _ := vm.Register(0)
			if !preparedValue.RawEqual(tc.preparedValue) {
				// 预减后的内部 index 必须保持 integer 类型。
				t.Fatalf("prepared value mismatch: got %#v want %#v", preparedValue, tc.preparedValue)
			}

			if err := vm.Step(bytecode.CreateAsBx(bytecode.OpForLoop, 0, -1)); err != nil {
				// FORLOOP 第一次步进必须继续，并写出 integer 控制变量。
				t.Fatalf("forloop failed: %v", err)
			}
			externalValue, _ := vm.Register(3)
			if !externalValue.RawEqual(tc.firstValue) || externalValue.Kind != KindInteger {
				// 外部可见控制变量必须是 integer，而不是退化成 float。
				t.Fatalf("external value mismatch: got %#v want %#v", externalValue, tc.firstValue)
			}
		})
	}
}

// TestVMTForLoop 验证泛型 for 循环结果判空语义。
//
// R(A+1) 非 nil 时，TFORLOOP 把它复制到 R(A) 并跳转。
func TestVMTForLoop(t *testing.T) {
	vm := NewVM(2)
	_ = vm.SetRegister(1, StringValue("key"))

	if err := vm.Step(bytecode.CreateAsBx(bytecode.OpTForLoop, 0, -1)); err != nil {
		// TFORLOOP 遇到非 nil 迭代结果必须继续。
		t.Fatalf("tforloop failed: %v", err)
	}
	value, _ := vm.Register(0)
	if !value.RawEqual(StringValue("key")) || vm.PCOffset() != -1 {
		// TFORLOOP 继续时必须保存控制变量并记录跳转。
		t.Fatalf("tforloop mismatch: value=%#v offset=%d", value, vm.PCOffset())
	}
}

// TestVMSetList 验证 SETLIST 会批量写入 table 数组区。
//
// 当前覆盖 C 非 0 的直接批次写入和 C 为 0 的 EXTRAARG 扩展批次写入。
func TestVMSetList(t *testing.T) {
	table := NewTable()
	vm := NewVM(4)
	_ = vm.SetRegister(0, ReferenceValue(KindTable, table))
	_ = vm.SetRegister(1, StringValue("a"))
	_ = vm.SetRegister(2, StringValue("b"))

	if err := vm.Step(bytecode.CreateABC(bytecode.OpSetList, 0, 2, 1)); err != nil {
		// SETLIST 第一批写入两个值必须成功。
		t.Fatalf("setlist failed: %v", err)
	}
	if !table.RawGetInteger(1).RawEqual(StringValue("a")) || !table.RawGetInteger(2).RawEqual(StringValue("b")) {
		// 第一批写入必须落到数组索引 1 和 2。
		t.Fatalf("setlist values mismatch")
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpSetList, 0, 1, 0)); err != nil {
		// C 为 0 时 SETLIST 必须等待 EXTRAARG。
		t.Fatalf("setlist pending failed: %v", err)
	}
	if err := vm.Step(bytecode.CreateAx(bytecode.OpExtraArg, 2)); err != nil {
		// EXTRAARG 批次 2 应把 R(1) 写入索引 51。
		t.Fatalf("setlist extraarg failed: %v", err)
	}
	if !table.RawGetInteger(51).RawEqual(StringValue("a")) {
		// 第二批第一项必须写入 (2-1)*50+1。
		t.Fatalf("setlist extraarg value mismatch: %#v", table.RawGetInteger(51))
	}

	openTable := NewTable()
	openVM := NewVMWithPrototypeData(6, nil, nil, nil, []Value{StringValue("x"), StringValue("y"), StringValue("z")})
	_ = openVM.SetRegister(0, ReferenceValue(KindTable, openTable))
	if err := openVM.Step(bytecode.CreateABC(bytecode.OpVararg, 1, 0, 0)); err != nil {
		// 开放 VARARG 应把全部 vararg 写入 R1.. 并记录 openTop。
		t.Fatalf("open vararg failed: %v", err)
	}
	if err := openVM.Step(bytecode.CreateABC(bytecode.OpSetList, 0, 0, 1)); err != nil {
		// SETLIST B=0 应按 openTop 写入真实 vararg 数量。
		t.Fatalf("open setlist failed: %v", err)
	}
	if !openTable.RawGetInteger(1).RawEqual(StringValue("x")) || !openTable.RawGetInteger(3).RawEqual(StringValue("z")) {
		// 开放列表必须按顺序写入全部 vararg。
		t.Fatalf("open setlist values mismatch")
	}
	if !openTable.RawGetInteger(4).IsNil() {
		// openTop 后的未使用寄存器不能被 SETLIST B=0 写入 table。
		t.Fatalf("open setlist wrote beyond openTop: %#v", openTable.RawGetInteger(4))
	}

	emptyOpenTable := NewTable()
	emptyOpenVM := NewVMWithPrototypeData(4, nil, nil, nil, nil)
	_ = emptyOpenVM.SetRegister(0, ReferenceValue(KindTable, emptyOpenTable))
	_ = emptyOpenVM.SetRegister(1, StringValue("stale"))
	if err := emptyOpenVM.Step(bytecode.CreateABC(bytecode.OpVararg, 1, 0, 0)); err != nil {
		// 空开放 VARARG 也必须记录 openTop，供 SETLIST 区分空列表和未知边界。
		t.Fatalf("empty open vararg failed: %v", err)
	}
	if err := emptyOpenVM.Step(bytecode.CreateABC(bytecode.OpSetList, 0, 0, 1)); err != nil {
		// SETLIST B=0 遇到空开放列表时应成功且不写入旧寄存器值。
		t.Fatalf("empty open setlist failed: %v", err)
	}
	if !emptyOpenTable.RawGetInteger(1).IsNil() {
		// 空开放列表不能把 R1 中的历史值误写入 table。
		t.Fatalf("empty open setlist wrote stale register: %#v", emptyOpenTable.RawGetInteger(1))
	}
}

// TestVMClosureAndVararg 验证 CLOSURE 与 VARARG 的基础写寄存器语义。
//
// CLOSURE 根据子 Proto 捕获 upvalue；VARARG 把当前函数 vararg 写入目标寄存器区间。
func TestVMClosureAndVararg(t *testing.T) {
	childProto := bytecode.NewProto("child")
	childProto.Upvalues = []bytecode.UpvalueDesc{{Name: "x", InStack: true, Index: 1}}
	vm := NewVMWithPrototypeData(4, nil, nil, []*bytecode.Proto{childProto}, []Value{StringValue("v1"), IntegerValue(2)})
	_ = vm.SetRegister(1, StringValue("captured"))

	if err := vm.Step(bytecode.CreateABx(bytecode.OpClosure, 0, 0)); err != nil {
		// CLOSURE 创建子闭包必须成功。
		t.Fatalf("closure failed: %v", err)
	}
	value, ok := vm.Register(0)
	closure, closureOK := value.Ref.(*LuaClosure)
	if !ok || value.Kind != KindLuaClosure || !closureOK || closure.Proto != childProto || len(closure.Upvalues) != 1 || !closure.Upvalues[0].RawEqual(StringValue("captured")) {
		// 闭包值必须引用子 Proto，并捕获声明的 upvalue。
		t.Fatalf("closure mismatch: value=%#v closure=%#v", value, closure)
	}

	if err := vm.Step(bytecode.CreateABC(bytecode.OpVararg, 2, 3, 0)); err != nil {
		// VARARG 固定读取两个 vararg 必须成功。
		t.Fatalf("vararg failed: %v", err)
	}
	firstValue, _ := vm.Register(2)
	secondValue, _ := vm.Register(3)
	if !firstValue.RawEqual(StringValue("v1")) || !secondValue.RawEqual(IntegerValue(2)) {
		// VARARG 必须按顺序写入 vararg 值。
		t.Fatalf("vararg mismatch: first=%#v second=%#v", firstValue, secondValue)
	}
}

// TestVMUnsupportedInstruction 验证未实现 opcode 返回明确错误。
//
// 该行为用于在逐步实现 VM 指令时避免静默跳过未知指令。
func TestVMUnsupportedInstruction(t *testing.T) {
	vm := NewVM(1)
	err := vm.Step(bytecode.Instruction(bytecode.NumOpCodes))
	if !errors.Is(err, ErrUnsupportedInstruction) {
		// 非法或未知 opcode 必须返回 ErrUnsupportedInstruction。
		t.Fatalf("unsupported instruction error mismatch: %v", err)
	}
}
