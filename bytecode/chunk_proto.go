package bytecode

import (
	"encoding/binary"
	"fmt"
	"io"
)

// LoadProto 从 reader 读取一个 Lua 5.3 函数原型。
//
// 入参 reader 必须位于 LoadFunction 起始位置；返回值包含 source、行号、参数、
// 指令、常量、upvalue、子 Proto 和 Debug 信息。该函数不读取 chunk header，也不读取
// 顶层闭包的 upvalue 数量字节。
func LoadProto(reader io.Reader) (*Proto, error) {
	// 顶层 Proto 没有父 source，委托内部递归函数完成 Lua 5.3 LoadFunction 顺序。
	return loadProto(reader, "")
}

// AppendProto 向目标字节切片追加 Lua 5.3 函数原型编码。
//
// 该函数主要服务测试和后续 dump 实现；它只写入 Proto 内容，不写 chunk header 或顶层
// 闭包 upvalue 数量。
func AppendProto(target []byte, proto *Proto) []byte {
	// 顶层 Proto 没有父 source，委托内部递归函数处理 source 继承压缩。
	return appendProtoWithParentSource(target, proto, "")
}

// appendProtoWithParentSource 向目标字节切片追加 Proto，并压缩重复 source。
//
// parentSource 是父 Proto 的 source；当当前 Proto source 与父级相同且父级非空时，按 Lua 5.3
// dump 规则写入 NULL source，加载时由 loadProto 继承父 source。
func appendProtoWithParentSource(target []byte, proto *Proto, parentSource string) []byte {
	// 按 Lua 5.3 LoadFunction 的反向顺序写入 source、基础元数据、code、constants、upvalues、children 和 debug。
	if parentSource != "" && proto.Source == parentSource {
		// 子函数 source 与父函数相同，写 NULL 避免在 binary chunk 中重复长 source。
		target = appendNullableLuaString(target, "")
	} else {
		// 顶层或不同 source 必须写出真实 source。
		target = appendNullableLuaString(target, proto.Source)
	}
	target = appendInt(target, int32(proto.LineDefined))
	target = appendInt(target, int32(proto.LastLineDefined))
	target = append(target, proto.NumParams)
	if proto.IsVararg {
		// Lua 5.3 chunk 中 is_vararg 使用 byte 表示，非零即 true。
		target = append(target, 1)
	} else {
		// false 固定写为 0，便于 roundtrip 测试稳定。
		target = append(target, 0)
	}
	target = append(target, proto.MaxStackSize)

	target = appendInstructions(target, proto.Code)
	target = AppendConstants(target, proto.Constants)
	target = appendUpvalues(target, proto.Upvalues)
	target = appendChildProtos(target, proto.Protos, proto.Source)
	target = appendDebug(target, proto)

	// 返回追加完整 Proto 后的字节切片。
	return target
}

// loadProto 按 Lua 5.3 LoadFunction 顺序读取函数原型。
//
// parentSource 是父函数 source；当前函数 source 为空时复用父 source。
func loadProto(reader io.Reader, parentSource string) (*Proto, error) {
	// 先读取 source，Lua 5.3 允许子函数 source 为空并继承父函数 source。
	source, hasSource, err := readNullableLuaString(reader, "proto source")
	if err != nil {
		// source 字段损坏时，后续字段位置无法可靠判断。
		return nil, err
	}
	if !hasSource {
		// source 为空表示复用父 source，顶层没有父 source 时保持空字符串。
		source = parentSource
	}

	proto := NewProto(source)
	if proto.LineDefined, err = readInt(reader, "line defined"); err != nil {
		// 起始行号缺失时，Proto 基础调试信息不完整。
		return nil, err
	}
	if proto.LastLineDefined, err = readInt(reader, "last line defined"); err != nil {
		// 结束行号缺失时，Proto 基础调试信息不完整。
		return nil, err
	}

	numParams, err := readDataByte(reader, "num params")
	if err != nil {
		// 参数数量缺失时，调用帧无法正确布局。
		return nil, err
	}
	proto.NumParams = numParams

	isVararg, err := readDataByte(reader, "is vararg")
	if err != nil {
		// vararg 标记缺失时，函数参数语义无法判断。
		return nil, err
	}
	proto.IsVararg = isVararg != 0

	maxStackSize, err := readDataByte(reader, "max stack size")
	if err != nil {
		// 最大栈大小缺失时，VM 无法创建寄存器窗口。
		return nil, err
	}
	proto.MaxStackSize = maxStackSize

	if proto.Code, err = loadInstructions(reader); err != nil {
		// 指令数组损坏时，函数主体无法执行。
		return nil, err
	}
	if proto.Constants, err = LoadConstants(reader); err != nil {
		// 常量表损坏时，LOADK 等指令无法解析。
		return nil, err
	}
	if proto.Upvalues, err = loadUpvalues(reader); err != nil {
		// upvalue 描述损坏时，闭包捕获语义无法建立。
		return nil, err
	}
	if proto.Protos, err = loadChildProtos(reader, proto.Source); err != nil {
		// 子函数原型损坏时，CLOSURE 指令无法创建嵌套闭包。
		return nil, err
	}
	if err := loadDebug(reader, proto); err != nil {
		// Debug 信息损坏时，traceback、local 和 upvalue 名称不可用。
		return nil, err
	}

	// Proto 已按 Lua 5.3 顺序完整读取。
	return proto, nil
}

// loadInstructions 读取 Lua 5.3 Proto.code 指令数组。
//
// 指令数量使用 chunk int 编码，每条指令使用 4 字节小端 Instruction。
func loadInstructions(reader io.Reader) ([]Instruction, error) {
	// 先读取指令数量，再逐项读取 uint32 指令字。
	instructionCount, err := readInt(reader, "instruction count")
	if err != nil {
		// 指令数量缺失时，无法确定后续 code 数组长度。
		return nil, err
	}
	if instructionCount < 0 {
		// 指令数量为负说明 chunk 数据损坏。
		return nil, fmt.Errorf("%w: negative instruction count %d", ErrInvalidChunkData, instructionCount)
	}

	instructions := make([]Instruction, instructionCount)
	for instructionIndex := 0; instructionIndex < instructionCount; instructionIndex++ {
		var rawInstruction uint32
		if err := binary.Read(reader, binary.LittleEndian, &rawInstruction); err != nil {
			// 单条指令读取失败时，返回带 pc 的错误方便定位。
			return nil, chunkReadError(ErrInvalidChunkData, fmt.Sprintf("instruction[%d]", instructionIndex), err)
		}
		instructions[instructionIndex] = Instruction(rawInstruction)
	}

	// 返回按 pc 顺序排列的指令数组。
	return instructions, nil
}

// loadUpvalues 读取 Lua 5.3 Proto.upvalues 的捕获描述。
//
// 名称不在此阶段读取，Lua 5.3 会在 LoadDebug 阶段回填 upvalue name。
func loadUpvalues(reader io.Reader) ([]UpvalueDesc, error) {
	// 先读取 upvalue 数量，再读取 instack 与 idx 两个字节字段。
	upvalueCount, err := readInt(reader, "upvalue count")
	if err != nil {
		// upvalue 数量缺失时，闭包描述无法继续解析。
		return nil, err
	}
	if upvalueCount < 0 {
		// upvalue 数量为负说明 chunk 数据损坏。
		return nil, fmt.Errorf("%w: negative upvalue count %d", ErrInvalidChunkData, upvalueCount)
	}

	upvalues := make([]UpvalueDesc, upvalueCount)
	for upvalueIndex := 0; upvalueIndex < upvalueCount; upvalueIndex++ {
		inStack, err := readDataByte(reader, fmt.Sprintf("upvalue[%d].instack", upvalueIndex))
		if err != nil {
			// instack 缺失时，该 upvalue 捕获来源无法判断。
			return nil, err
		}
		index, err := readDataByte(reader, fmt.Sprintf("upvalue[%d].idx", upvalueIndex))
		if err != nil {
			// idx 缺失时，该 upvalue 捕获位置无法判断。
			return nil, err
		}
		upvalues[upvalueIndex] = UpvalueDesc{InStack: inStack != 0, Index: index}
	}

	// 返回未带名称的 upvalue 描述，名称由 loadDebug 回填。
	return upvalues, nil
}

// loadChildProtos 递归读取 Lua 5.3 Proto.p 子函数原型数组。
//
// parentSource 用于子 Proto 缺省 source 时继承父函数 source。
func loadChildProtos(reader io.Reader, parentSource string) ([]*Proto, error) {
	// 先读取子函数数量，再按顺序递归读取每个子 Proto。
	childCount, err := readInt(reader, "child proto count")
	if err != nil {
		// 子函数数量缺失时，无法继续解析嵌套函数。
		return nil, err
	}
	if childCount < 0 {
		// 子函数数量为负说明 chunk 数据损坏。
		return nil, fmt.Errorf("%w: negative child proto count %d", ErrInvalidChunkData, childCount)
	}

	children := make([]*Proto, childCount)
	for childIndex := 0; childIndex < childCount; childIndex++ {
		child, err := loadProto(reader, parentSource)
		if err != nil {
			// 子 Proto 读取失败时，返回带索引的错误方便定位。
			return nil, fmt.Errorf("%w: child proto[%d]: %v", ErrInvalidChunkData, childIndex, err)
		}
		children[childIndex] = child
	}

	// 返回按 CLOSURE Bx 索引顺序排列的子 Proto。
	return children, nil
}

// loadDebug 读取 Lua 5.3 Proto 的行号、局部变量和 upvalue 名称调试信息。
//
// 该函数对齐 lundump.c 的 LoadDebug 顺序。
func loadDebug(reader io.Reader, proto *Proto) error {
	// 先读取 lineinfo，再读取 locvars，最后按数量回填 upvalue 名称。
	lineInfo, err := loadIntSlice(reader, "line info")
	if err != nil {
		// 行号表损坏会影响 traceback 和 debug.getinfo。
		return err
	}
	proto.LineInfo = lineInfo

	localVars, err := loadLocalVars(reader, proto.Code)
	if err != nil {
		// 局部变量表损坏会影响 debug.getlocal。
		return err
	}
	proto.LocalVars = localVars

	upvalueNameCount, err := readInt(reader, "upvalue name count")
	if err != nil {
		// upvalue 名称数量缺失时，无法完成 Debug 名称回填。
		return err
	}
	if upvalueNameCount < 0 {
		// upvalue 名称数量为负说明 chunk 数据损坏。
		return fmt.Errorf("%w: negative upvalue name count %d", ErrInvalidChunkData, upvalueNameCount)
	}
	if upvalueNameCount > len(proto.Upvalues) {
		// 官方实现假设数量匹配；Go loader 显式拒绝越界回填。
		return fmt.Errorf("%w: upvalue name count %d exceeds upvalue count %d", ErrInvalidChunkData, upvalueNameCount, len(proto.Upvalues))
	}
	for upvalueIndex := 0; upvalueIndex < upvalueNameCount; upvalueIndex++ {
		name, _, err := readNullableLuaString(reader, fmt.Sprintf("upvalue[%d].name", upvalueIndex))
		if err != nil {
			// 单个 upvalue 名称损坏时，返回带索引的错误方便定位。
			return err
		}
		proto.Upvalues[upvalueIndex].Name = name
	}

	// Debug 信息已全部回填到 Proto。
	return nil
}

// loadIntSlice 读取 Lua 5.3 chunk 中的 int 数组。
//
// fieldName 用于错误上下文，当前主要服务 lineinfo。
func loadIntSlice(reader io.Reader, fieldName string) ([]int, error) {
	// 先读取数组长度，再逐项读取 int。
	valueCount, err := readInt(reader, fieldName+" count")
	if err != nil {
		// 数量字段缺失时，无法确定数组长度。
		return nil, err
	}
	if valueCount < 0 {
		// 数量为负说明 chunk 数据损坏。
		return nil, fmt.Errorf("%w: negative %s count %d", ErrInvalidChunkData, fieldName, valueCount)
	}

	values := make([]int, valueCount)
	for valueIndex := 0; valueIndex < valueCount; valueIndex++ {
		value, err := readInt(reader, fmt.Sprintf("%s[%d]", fieldName, valueIndex))
		if err != nil {
			// 单项 int 读取失败时，返回带索引的错误方便定位。
			return nil, err
		}
		values[valueIndex] = value
	}

	// 返回按 chunk 顺序读取的 int 数组。
	return values, nil
}

// loadLocalVars 读取 Lua 5.3 Proto.locvars 局部变量调试表。
//
// 每个局部变量包含名称、startpc 和 endpc。
func loadLocalVars(reader io.Reader, code []Instruction) ([]LocalVar, error) {
	// 先读取局部变量数量，再逐项读取名称和生命周期范围。
	localVarCount, err := readInt(reader, "local var count")
	if err != nil {
		// 数量字段缺失时，无法确定 locvars 长度。
		return nil, err
	}
	if localVarCount < 0 {
		// 数量为负说明 chunk 数据损坏。
		return nil, fmt.Errorf("%w: negative local var count %d", ErrInvalidChunkData, localVarCount)
	}

	localVars := make([]LocalVar, localVarCount)
	for localVarIndex := 0; localVarIndex < localVarCount; localVarIndex++ {
		name, _, err := readNullableLuaString(reader, fmt.Sprintf("local var[%d].name", localVarIndex))
		if err != nil {
			// 名称读取失败时，该局部变量 Debug 信息不可用。
			return nil, err
		}
		startPC, err := readInt(reader, fmt.Sprintf("local var[%d].startpc", localVarIndex))
		if err != nil {
			// startpc 缺失时，该局部变量生命周期不完整。
			return nil, err
		}
		endPC, err := readInt(reader, fmt.Sprintf("local var[%d].endpc", localVarIndex))
		if err != nil {
			// endpc 缺失时，该局部变量生命周期不完整。
			return nil, err
		}
		localVars[localVarIndex] = LocalVar{Name: name, StartPC: startPC, EndPC: endPC}
	}

	assignLoadedLocalRegisters(localVars, code)

	// 返回按 Debug 表顺序排列的局部变量。
	return localVars, nil
}

// assignLoadedLocalRegisters 根据 locvar 生命周期重建局部变量寄存器槽位。
//
// Lua 5.3 binary chunk 的 locvar 只保存 name/startpc/endpc，不保存寄存器号；官方调试表顺序
// 按局部变量入栈顺序排列。加载外部 chunk 时需要按生命周期复用已结束局部变量的低槽位，
// 让 debug.getinfo("n") 和 debug.getlocal 能在 dump/load 后继续反查 local 名称。
func assignLoadedLocalRegisters(localVars []LocalVar, code []Instruction) {
	// activeEndByRegister 记录每个重建寄存器当前占用到哪条 PC。
	activeEndByRegister := make(map[int]int)
	for localVarIndex := range localVars {
		startPC := localVars[localVarIndex].StartPC
		for register, endPC := range activeEndByRegister {
			if endPC <= startPC {
				// 生命周期已结束的局部变量释放对应寄存器槽位，允许后续 local 复用。
				delete(activeEndByRegister, register)
			}
		}

		if register, ok := loadedLocalRegisterFromStartInstruction(localVars[localVarIndex], code); ok {
			// local function 的 CLOSURE A 会显式暴露真实局部寄存器，优先保留该槽位以维持 Debug 名称反查。
			localVars[localVarIndex].Register = register
			activeEndByRegister[register] = localVars[localVarIndex].EndPC
			continue
		}

		register := 0
		for {
			if _, occupied := activeEndByRegister[register]; !occupied {
				// 选择当前最小空闲槽位，匹配 Lua 栈局部变量的低位优先分配。
				break
			}
			register++
		}
		localVars[localVarIndex].Register = register
		activeEndByRegister[register] = localVars[localVarIndex].EndPC
	}
}

// loadedLocalRegisterFromStartInstruction 尝试从局部变量起始指令恢复真实寄存器。
//
// Lua 5.3 binary chunk 的 locvar 不保存寄存器号，但 local function 声明通常在 StartPC
// 处通过 CLOSURE A 创建闭包；`local f = function()` 赋值式闭包则常在 StartPC-1 创建。
// 此时 A 就是真实 local 槽位。优先使用该槽位可避免 dump/load 后同作用域多个局部函数
// 被低槽重建，导致 debug.getinfo("n") 调用名错配。
func loadedLocalRegisterFromStartInstruction(localVar LocalVar, code []Instruction) (int, bool) {
	// 先尝试 StartPC，再尝试 StartPC-1，覆盖 local function 与赋值式闭包两类调试生命周期边界。
	for _, candidatePC := range []int{localVar.StartPC, localVar.StartPC - 1} {
		if candidatePC < 0 || candidatePC >= len(code) {
			// 越界候选不能参与指令反推，继续检查下一个候选。
			continue
		}
		instruction := code[candidatePC]
		if instruction.OpCode() != OpClosure {
			// 只有 CLOSURE A 能稳定表达 local closure 的真实目标寄存器。
			continue
		}
		// 返回 CLOSURE 的 A 字段作为局部闭包所在寄存器。
		return instruction.A(), true
	}

	// 未找到 CLOSURE 候选时交回生命周期算法处理。
	return 0, false
}

// readNullableLuaString 读取 Lua 5.3 chunk 字符串，并允许 NULL 字符串。
//
// 返回值 hasString 为 false 时表示 chunk 中长度为 0，调用方应按上下文决定是否继承父值。
func readNullableLuaString(reader io.Reader, fieldName string) (value string, hasString bool, err error) {
	// 先读取长度字节；0 表示 NULL，0xff 表示后续使用 size_t。
	sizeByte, err := readDataByte(reader, fieldName+" size")
	if err != nil {
		// 长度字段缺失时，字符串无法解析。
		return "", false, err
	}
	if sizeByte == 0 {
		// NULL 字符串没有负载，常用于缺省 source 或 stripped debug name。
		return "", false, nil
	}

	stringSize := uint64(sizeByte)
	if sizeByte == longStringSizeMarker {
		// 长字符串长度使用 8 字节 size_t 编码。
		if err := binary.Read(reader, binary.LittleEndian, &stringSize); err != nil {
			// size_t 缺失时，字符串负载不完整。
			return "", false, chunkReadError(ErrInvalidChunkData, fieldName+" long size", err)
		}
	}
	if stringSize == 0 {
		// 防御性检查：size_t 形式也不允许再次编码为 0。
		return "", false, nil
	}

	payloadSize := stringSize - 1
	if payloadSize > uint64(int(^uint(0)>>1)) {
		// Go 进程无法分配超过 int 最大值的字节切片。
		return "", false, fmt.Errorf("%w: %s too large: %d", ErrInvalidChunkData, fieldName, payloadSize)
	}

	buffer := make([]byte, int(payloadSize))
	if _, err := io.ReadFull(reader, buffer); err != nil {
		// 字符串负载不足时，chunk 数据损坏。
		return "", false, chunkReadError(ErrInvalidChunkData, fieldName+" payload", err)
	}

	// 返回不含结尾 NUL 的字符串内容。
	return string(buffer), true, nil
}

// appendInstructions 按 Lua 5.3 code 数组格式追加指令。
//
// 先写入指令数量，再按 4 字节小端写入每条 Instruction。
func appendInstructions(target []byte, instructions []Instruction) []byte {
	// Lua 5.3 code 数组长度使用 int 编码。
	target = appendInt(target, int32(len(instructions)))
	for _, instruction := range instructions {
		var buffer [ChunkInstructionSize]byte
		binary.LittleEndian.PutUint32(buffer[:], uint32(instruction))
		target = append(target, buffer[:]...)
	}

	// 返回追加指令数组后的字节切片。
	return target
}

// appendUpvalues 按 Lua 5.3 upvalue 捕获描述格式追加数据。
//
// 名称不在此处写入，名称属于 Debug 段。
func appendUpvalues(target []byte, upvalues []UpvalueDesc) []byte {
	// Lua 5.3 先写入 upvalue 数量，再逐项写入 instack 与 idx。
	target = appendInt(target, int32(len(upvalues)))
	for _, upvalue := range upvalues {
		if upvalue.InStack {
			// instack 为 true 时写入 1。
			target = append(target, 1)
		} else {
			// instack 为 false 时写入 0。
			target = append(target, 0)
		}
		target = append(target, upvalue.Index)
	}

	// 返回追加 upvalue 捕获描述后的字节切片。
	return target
}

// appendChildProtos 按 Lua 5.3 子 Proto 数组格式追加数据。
//
// 每个子 Proto 递归使用 AppendProto 写入。
func appendChildProtos(target []byte, children []*Proto, parentSource string) []byte {
	// Lua 5.3 先写入子 Proto 数量，再逐项写入完整函数原型。
	target = appendInt(target, int32(len(children)))
	for _, child := range children {
		target = appendProtoWithParentSource(target, child, parentSource)
	}

	// 返回追加子 Proto 后的字节切片。
	return target
}

// appendDebug 按 Lua 5.3 Debug 段格式追加行号、局部变量和 upvalue 名称。
//
// Debug 段即使为空也必须写入三个数量字段。
func appendDebug(target []byte, proto *Proto) []byte {
	// lineinfo 先写数量，再写每条指令对应行号。
	target = appendInt(target, int32(len(proto.LineInfo)))
	for _, line := range proto.LineInfo {
		target = appendInt(target, int32(line))
	}

	// locvars 先写数量，再逐项写名称、startpc 和 endpc。
	target = appendInt(target, int32(len(proto.LocalVars)))
	for _, localVar := range proto.LocalVars {
		target = appendNullableLuaString(target, localVar.Name)
		target = appendInt(target, int32(localVar.StartPC))
		target = appendInt(target, int32(localVar.EndPC))
	}

	// upvalue 名称数量按 upvalue 数量写入，名称为空时编码为 NULL 字符串。
	target = appendInt(target, int32(len(proto.Upvalues)))
	for _, upvalue := range proto.Upvalues {
		target = appendNullableLuaString(target, upvalue.Name)
	}

	// 返回追加 Debug 段后的字节切片。
	return target
}

// appendNullableLuaString 按 Lua 5.3 chunk 字符串格式追加可空字符串。
//
// 空字符串在 Debug/source 上编码为 NULL 字符串；非空字符串使用普通 Lua 字符串格式。
func appendNullableLuaString(target []byte, value string) []byte {
	// 空字符串表示 NULL 字符串，写入单字节 0。
	if value == "" {
		return append(target, 0)
	}

	// 非空字符串复用普通 Lua 字符串编码。
	return appendLuaString(target, value)
}
