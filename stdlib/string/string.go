// Package stringlib 实现 Lua 5.3 string 标准库的第一阶段能力。
//
// 本包负责注册 `string` 库表，并提供 byte、char、dump、find、format、len、lower、
// rep、reverse 和 sub 的基础行为。
// Lua pattern、capture、balanced match 和 frontier pattern 会在后续专门任务中补齐。
package stringlib

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/zing/go-lua-vm/bytecode"
	"github.com/zing/go-lua-vm/runtime"
)

const (
	// maxStringRepResultBytes 表示当前纯 Go string.rep 允许构造的最大结果字节数。
	//
	// Lua 5.3 官方测试会用超过 2GB 的重复结果验证溢出保护；本实现提前拒绝这类结果，
	// 避免测试或宿主进程进入不可控的大内存分配。
	maxStringRepResultBytes int64 = 1<<31 - 1
	// maxPatternBytes 表示当前 pattern 引擎允许解析和回溯的最大 pattern 字节数。
	//
	// Lua 官方测试包含 20 万个 `.?` 组成的复杂 pattern，用于验证实现必须快速拒绝而不是
	// 进入不可控递归或回溯；该上限保留常规业务 pattern 空间，同时对压力输入返回 Lua error。
	maxPatternBytes = 10000
	// packNativeMaxAlign 表示当前纯 Go 运行时模拟的原生最大对齐字节数。
	//
	// Lua 5.3 的 `!` 不带数字时使用宿主 C ABI 的最大对齐；本项目当前固定为 8，覆盖
	// 官方测试对默认原生对齐打印与常见 64 位平台的预期。
	packNativeMaxAlign = 8
	// packMaxOptionSize 表示 Lua 5.3 官方测试允许的最大整数/对齐操作数字节数。
	packMaxOptionSize = 16
)

// stringKindUnaryMask 记录 string 一元函数允许走无 frame fast path 的参数类型集合。
var stringKindUnaryMask = runtime.UnaryKindMask(runtime.KindString)

// stringFastUnaryFunctions 保存无状态 string 一元函数的只读 fastcall 描述，避免每个 State 打开标准库时重复分配。
var stringFastUnaryFunctions = struct {
	len     *runtime.GoFastUnaryFunction
	lower   *runtime.GoFastUnaryFunction
	reverse *runtime.GoFastUnaryFunction
	upper   *runtime.GoFastUnaryFunction
}{
	len:     &runtime.GoFastUnaryFunction{Function: LenUnaryValue, AcceptedKinds: stringKindUnaryMask},
	lower:   &runtime.GoFastUnaryFunction{Function: LowerUnaryValue, AcceptedKinds: stringKindUnaryMask},
	reverse: &runtime.GoFastUnaryFunction{Function: ReverseUnaryValue, AcceptedKinds: stringKindUnaryMask},
	upper:   &runtime.GoFastUnaryFunction{Function: UpperUnaryValue, AcceptedKinds: stringKindUnaryMask},
}

// stringFixedResultsFunctions 保存无状态 string 固定返回上限函数的只读描述，供库表注册复用。
var stringFixedResultsFunctions = struct {
	byteFunction *runtime.GoFixedResultsFunction
	findFunction *runtime.GoFixedResultsFunction
	subFunction  *runtime.GoFixedResultsFunction
}{
	byteFunction: &runtime.GoFixedResultsFunction{
		MaxResults:      1,
		Function4Single: ByteFixed4Single,
		Function4:       ByteFixed4,
		Function:        ByteFixed,
		Fallback:        Byte,
	},
	findFunction: &runtime.GoFixedResultsFunction{
		MaxResults: 2,
		Function4:  FindFixed4,
		Function:   FindFixed,
		Fallback:   Find,
	},
	subFunction: &runtime.GoFixedResultsFunction{
		MaxResults:      1,
		Function4Single: SubFixed4Single,
		Function4:       SubFixed4,
		Function:        SubFixed,
		Fallback:        Sub,
	},
}

// Open 将 Lua 5.3 string 标准库注册到 State 全局环境。
//
// state 必须非 nil 且未关闭；成功后全局 `string` 字段指向库表，并注册本阶段已支持的
// string 函数。当前阶段函数按 Lua 字符串的字节语义工作，不按 Unicode rune 解释。
func Open(state *runtime.State) error {
	// 注册入口先校验 State 生命周期，避免向关闭后的全局表写入库函数。
	if state == nil {
		// nil State 没有 globals，调用方需要先创建 runtime.State。
		return fmt.Errorf("string library unavailable: %w", runtime.ErrNilState)
	}
	if state.IsClosed() {
		// 已关闭 State 的 globals 已释放，不能继续注册标准库。
		return fmt.Errorf("string library unavailable: %w", runtime.ErrClosedState)
	}

	library := runtime.NewTable()
	// string 库函数以 Go closure 注册，后续 VM CALL 会通过 bridge 调用。
	library.RawSetString("byte", runtime.ReferenceValue(runtime.KindGoClosure, stringFixedResultsFunctions.byteFunction))
	library.RawSetString("char", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Char)))
	library.RawSetString("dump", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Dump)))
	library.RawSetString("find", runtime.ReferenceValue(runtime.KindGoClosure, stringFixedResultsFunctions.findFunction))
	library.RawSetString("format", runtime.ReferenceValue(runtime.KindGoClosure, newFormatFixedResultsFunction(state)))
	library.RawSetString("gmatch", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(GMatch)))
	library.RawSetString("gsub", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// gsub 的 Lua closure 替换函数需要当前 State 提供 Lua closure runner。
		return gsubWithState(state, args...)
	})))
	library.RawSetString("len", runtime.ReferenceValue(runtime.KindGoClosure, stringFastUnaryFunctions.len))
	library.RawSetString("lower", runtime.ReferenceValue(runtime.KindGoClosure, stringFastUnaryFunctions.lower))
	library.RawSetString("match", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Match)))
	library.RawSetString("pack", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Pack)))
	library.RawSetString("packsize", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(PackSize)))
	library.RawSetString("rep", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Rep)))
	library.RawSetString("reverse", runtime.ReferenceValue(runtime.KindGoClosure, stringFastUnaryFunctions.reverse))
	library.RawSetString("sub", runtime.ReferenceValue(runtime.KindGoClosure, stringFixedResultsFunctions.subFunction))
	library.RawSetString("unpack", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Unpack)))
	library.RawSetString("upper", runtime.ReferenceValue(runtime.KindGoClosure, stringFastUnaryFunctions.upper))
	state.SetGlobal("string", runtime.ReferenceValue(runtime.KindTable, library))
	runtime.SetStringIndexTable(library)
	return nil
}

// Byte 实现 Lua 5.3 `string.byte` 的字节提取语义。
//
// 第一个参数必须是 string；i/j 可选且必须是 integer，默认 i=1、j=i。负索引按字符串长度
// 从尾部换算。返回值是区间内每个字节的 integer；区间为空或越界时返回零个结果。
func Byte(args ...runtime.Value) ([]runtime.Value, error) {
	// byte 首先解析待读取字符串。
	source, err := stringArgument(args, 1, "byte")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	startIndex := int64(1)
	if len(args) >= 2 {
		// i 参数存在时必须可转换为 integer。
		convertedIndex, ok := args[1].ToInteger()
		if !ok {
			// 非整数索引无法参与 Lua 字节区间换算。
			return nil, badArgument("byte", 2, "integer expected")
		}
		startIndex = convertedIndex
	}

	endIndex := startIndex
	if len(args) >= 3 {
		// j 参数存在时必须可转换为 integer。
		convertedIndex, ok := args[2].ToInteger()
		if !ok {
			// 非整数索引无法参与 Lua 字节区间换算。
			return nil, badArgument("byte", 3, "integer expected")
		}
		endIndex = convertedIndex
	}

	startOffset, endOffset, ok := normalizeRange(len(source), startIndex, endIndex)
	if !ok {
		// 空区间在 Lua 5.3 中返回零个结果。
		return nil, nil
	}

	results := make([]runtime.Value, 0, endOffset-startOffset)
	for index := startOffset; index < endOffset; index++ {
		// Lua string.byte 返回底层字节值，范围固定为 0..255。
		results = append(results, runtime.IntegerValue(int64(source[index])))
	}
	return results, nil
}

// ByteFixed 实现 `string.byte` 单字节固定返回快路径。
//
// dst 至少需要容纳一个返回值；仅覆盖默认单字节或 i==j 的调用形态。范围展开成多个返回值时
// 返回 handled=false 交给 Byte 完整路径，避免 MaxResults 截断 Lua 5.3 的变长返回语义。
func ByteFixed(dst []runtime.Value, args ...runtime.Value) (int, bool, error) {
	// 固定返回入口先校验结果槽，避免后续写越界。
	if len(dst) < 1 {
		// 调用方声明的 MaxResults 与结果槽不一致时不能安全处理。
		return 0, false, runtime.ErrRegisterOutOfRange
	}
	if len(args) == 0 || len(args) > 3 {
		// 缺少必选字符串或超过当前窄快路径参数数量时回退完整 Byte。
		return 0, false, nil
	}

	var arg0 runtime.Value
	var arg1 runtime.Value
	var arg2 runtime.Value
	if len(args) >= 1 {
		// 第一个参数是待读取字符串。
		arg0 = args[0]
	}
	if len(args) >= 2 {
		// 第二个参数是起始字节位置。
		arg1 = args[1]
	}
	if len(args) >= 3 {
		// 第三个参数是结束字节位置。
		arg2 = args[2]
	}

	// 复用最多四实参入口，保持直接寄存器快路径和 debug-frame 回退路径语义一致。
	return ByteFixed4(dst, arg0, arg1, arg2, runtime.NilValue(), len(args))
}

// ByteFixed4 实现 `string.byte` 最多三实参的单字节固定返回快路径。
//
// dst 至少需要容纳一个返回值；argCount 表示实际参数数量。该入口只处理返回 0 或 1 个值的
// 形态，范围返回多个字节时返回 handled=false，由通用 Byte 保留完整多返回值语义。
func ByteFixed4(dst []runtime.Value, arg0 runtime.Value, arg1 runtime.Value, arg2 runtime.Value, _ runtime.Value, argCount int) (int, bool, error) {
	// 固定返回入口只写一个槽位，调用方按 MaxResults 分配 dst。
	if len(dst) < 1 {
		// 结果槽不足时不能安全写入。
		return 0, false, runtime.ErrRegisterOutOfRange
	}
	if argCount < 1 || argCount > 3 {
		// 参数数量不在窄快路径覆盖范围时回退完整实现。
		return 0, false, nil
	}
	if arg0.Kind != runtime.KindString {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return 0, true, badArgument("byte", 1, "string expected")
	}

	startIndex := int64(1)
	if argCount >= 2 {
		// i 参数存在时必须可转换为 integer。
		convertedIndex, ok := arg1.ToInteger()
		if !ok {
			// 非整数索引无法参与 Lua 字节区间换算。
			return 0, true, badArgument("byte", 2, "integer expected")
		}
		startIndex = convertedIndex
	}

	endIndex := startIndex
	if argCount >= 3 {
		// j 参数存在时必须可转换为 integer。
		convertedIndex, ok := arg2.ToInteger()
		if !ok {
			// 非整数索引无法参与 Lua 字节区间换算。
			return 0, true, badArgument("byte", 3, "integer expected")
		}
		endIndex = convertedIndex
	}

	source := arg0.String
	startOffset, endOffset, ok := normalizeRange(len(source), startIndex, endIndex)
	if !ok {
		// 空区间在 Lua 5.3 中返回零个结果。
		return 0, true, nil
	}
	if endOffset-startOffset != 1 {
		// 多字节范围需要变长返回，不能由单槽快路径处理。
		return 0, false, nil
	}

	// 单字节命中时直接写入结果槽，避免构造临时返回切片。
	dst[0] = runtime.IntegerValue(int64(source[startOffset]))
	return 1, true, nil
}

// ByteFixed4Single 实现 `string.byte` 最多三实参的单返回无槽位快路径。
//
// argCount 表示实际参数数量。该入口只处理返回 0 或 1 个值的形态，范围返回多个字节时
// 返回 handled=false，由通用 Byte 保留完整多返回值语义。
func ByteFixed4Single(arg0 runtime.Value, arg1 runtime.Value, arg2 runtime.Value, _ runtime.Value, argCount int) (runtime.Value, int, bool, error) {
	// 单返回入口直接复用寄存器实参，不构造结果槽。
	if argCount < 1 || argCount > 3 {
		// 参数数量不在窄快路径覆盖范围时回退完整实现。
		return runtime.NilValue(), 0, false, nil
	}
	if arg0.Kind != runtime.KindString {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return runtime.NilValue(), 0, true, badArgument("byte", 1, "string expected")
	}

	startIndex := int64(1)
	if argCount >= 2 {
		// i 参数存在时必须可转换为 integer。
		convertedIndex, ok := arg1.ToInteger()
		if !ok {
			// 非整数索引无法参与 Lua 字节区间换算。
			return runtime.NilValue(), 0, true, badArgument("byte", 2, "integer expected")
		}
		startIndex = convertedIndex
	}

	endIndex := startIndex
	if argCount >= 3 {
		// j 参数存在时必须可转换为 integer。
		convertedIndex, ok := arg2.ToInteger()
		if !ok {
			// 非整数索引无法参与 Lua 字节区间换算。
			return runtime.NilValue(), 0, true, badArgument("byte", 3, "integer expected")
		}
		endIndex = convertedIndex
	}

	source := arg0.String
	startOffset, endOffset, ok := normalizeRange(len(source), startIndex, endIndex)
	if !ok {
		// 空区间在 Lua 5.3 中返回零个结果。
		return runtime.NilValue(), 0, true, nil
	}
	if endOffset-startOffset != 1 {
		// 多字节范围需要变长返回，不能由单返回快路径处理。
		return runtime.NilValue(), 0, false, nil
	}

	// 单字节命中时直接返回整数结果，避免构造临时返回槽。
	return runtime.IntegerValue(int64(source[startOffset])), 1, true, nil
}

// Char 实现 Lua 5.3 `string.char` 的字节构造语义。
//
// 所有参数都必须是 0..255 的 integer；返回值是按参数顺序拼接出的字节字符串。任一参数
// 越界或非整数都会返回 Lua error，不生成部分字符串。
func Char(args ...runtime.Value) ([]runtime.Value, error) {
	// char 先分配目标字节切片，长度等于参数数量。
	bytes := make([]byte, len(args))
	for index, value := range args {
		// 每个参数都必须是整数码点，Lua 5.3 string.char 按字节写入。
		integerValue, ok := value.ToInteger()
		if !ok {
			// 非整数值不能作为字节码点。
			return nil, badArgument("char", index+1, "integer expected")
		}
		if integerValue < 0 || integerValue > 255 {
			// 超出 byte 范围时拒绝，避免 Go byte 截断改变语义。
			return nil, badArgument("char", index+1, "value out of range")
		}
		bytes[index] = byte(integerValue)
	}

	// 返回按字节构造的 Lua string。
	return []runtime.Value{runtime.StringValue(string(bytes))}, nil
}

// Dump 实现 Lua 5.3 `string.dump` 的基础二进制 chunk 输出。
//
// 第一个参数必须是 Lua closure；第二个 strip 参数可选，按 Lua truthiness 判断是否剥离
// source、lineinfo、local var 和 upvalue 名称。Go closure 暂不支持 dump，因为没有 Lua Proto 可序列化。
func Dump(args ...runtime.Value) ([]runtime.Value, error) {
	// dump 首先检查函数参数存在且为 Lua closure。
	if len(args) == 0 || args[0].Kind != runtime.KindLuaClosure {
		// 非 Lua closure 没有 Proto，可序列化边界不成立。
		return nil, badArgument("dump", 1, "Lua function expected")
	}

	closure, ok := args[0].Ref.(*runtime.LuaClosure)
	if !ok || closure == nil || closure.Proto == nil {
		// KindLuaClosure 但引用负载损坏时按参数错误暴露。
		return nil, badArgument("dump", 1, "Lua function expected")
	}

	proto := closure.Proto
	if len(args) > 1 && args[1].Truthy() {
		// strip=true 时使用深拷贝剥离调试信息，避免污染原 closure。
		proto = bytecode.StripDebug(proto)
	}

	// 使用 bytecode 层已有 dump 实现生成 Lua 5.3 binary chunk。
	return []runtime.Value{runtime.StringValue(string(bytecode.DumpBinaryChunk(proto)))}, nil
}

// Find 实现 Lua 5.3 `string.find` 的基础查找语义。
//
// 第一个参数 source 和第二个参数 pattern 必须是 string；init 可选且必须为 integer，plain
// 可选并按 truthiness 判断。plain 为 true 时执行 literal 查找；否则复用本包 Lua pattern 引擎
// 返回完整匹配的 1-based 闭区间，并附加 capture 文本结果。
func Find(args ...runtime.Value) ([]runtime.Value, error) {
	// find 先解析源字符串和待查找文本。
	source, err := stringArgument(args, 1, "find")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	pattern, err := stringArgument(args, 2, "find")
	if err != nil {
		// 第二个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	startIndex := int64(1)
	if len(args) >= 3 {
		// init 参数存在时必须可转换为 integer。
		convertedIndex, ok := args[2].ToInteger()
		if !ok {
			// 非整数起点无法换算为字节偏移。
			return nil, badArgument("find", 3, "integer expected")
		}
		startIndex = convertedIndex
	}
	startOffset := normalizeStart(len(source), startIndex)
	if startOffset > len(source) {
		// 起点超过字符串尾部时必然找不到匹配。
		return []runtime.Value{runtime.NilValue()}, nil
	}

	plain := false
	if len(args) >= 4 {
		// 第四个参数按 Lua truthiness 决定是否禁用 pattern。
		plain = args[3].Truthy()
	}
	if !plain && isPlainPattern(pattern) {
		// 没有 Lua pattern 魔法字符时，pattern 语义等价于字面查找，可跳过递归 pattern 引擎。
		return findLiteralRange(source, pattern, startOffset), nil
	}
	if !plain {
		// 默认路径按 Lua pattern 查找，支持官方测试依赖的 `.*` 等模式。
		matchResult, ok, matchErr := findPattern(source, pattern, startOffset)
		if matchErr != nil {
			// pattern 语法或执行错误直接返回 Lua error。
			return nil, matchErr
		}
		if !ok {
			// 未命中时返回单个 nil。
			return []runtime.Value{runtime.NilValue()}, nil
		}
		results := []runtime.Value{
			runtime.IntegerValue(int64(matchResult.start + 1)),
			runtime.IntegerValue(int64(matchResult.end)),
		}
		for _, capture := range orderedCaptures(matchResult.captures) {
			// Lua string.find 在位置后追加 capture 文本或位置捕获。
			results = append(results, captureValue(source, capture))
		}
		return results, nil
	}

	return findLiteralRange(source, pattern, startOffset), nil
}

// FindFixed 实现 `string.find` 的固定上限多返回值快路径。
//
// dst 至少需要容纳两个返回值；当 plain=true 或 pattern 不含 magic 字符时，本函数直接写入
// 1-based 匹配区间并返回 handled=true。包含 Lua pattern 语义时返回 handled=false，由调用方
// 回退到 Find，避免丢失 capture 等变长返回值语义。
func FindFixed(dst []runtime.Value, args ...runtime.Value) (int, bool, error) {
	// 快路径只覆盖两个结果槽，调用方保证 MaxResults 与 dst 容量一致。
	if len(dst) < 2 {
		// 调用方提供的结果槽不足时无法安全写入固定两返回值。
		return 0, false, runtime.ErrRegisterOutOfRange
	}
	if len(args) >= 4 && args[3].Truthy() {
		// plain=true 是标准库热点，直接按字面查找处理，避免额外 pattern magic 扫描。
		resultCount, ok, err := findPlainFixedFast(dst, args)
		if ok || err != nil {
			// 参数形态完整时已处理；参数错误保持 FindFixed 原有错误语义。
			return resultCount, true, err
		}
	}
	source, err := stringArgument(args, 1, "find")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return 0, true, err
	}
	pattern, err := stringArgument(args, 2, "find")
	if err != nil {
		// 第二个参数不是 string 时直接返回 Lua 参数错误。
		return 0, true, err
	}

	startIndex := int64(1)
	if len(args) >= 3 {
		// init 参数存在时必须可转换为 integer。
		convertedIndex, ok := args[2].ToInteger()
		if !ok {
			// 非整数起点无法换算为字节偏移。
			return 0, true, badArgument("find", 3, "integer expected")
		}
		startIndex = convertedIndex
	}
	startOffset := normalizeStart(len(source), startIndex)
	if startOffset > len(source) {
		// 起点超过字符串尾部时必然找不到匹配。
		dst[0] = runtime.NilValue()
		return 1, true, nil
	}

	plain := false
	if len(args) >= 4 {
		// 第四个参数按 Lua truthiness 决定是否禁用 pattern。
		plain = args[3].Truthy()
	}
	if !plain && !isPlainPattern(pattern) {
		// 存在 Lua pattern magic 时必须回退完整引擎，避免 capture 和特殊模式被截断。
		return 0, false, nil
	}

	// 字面量路径直接写入调用方结果槽，避免构造临时 []Value。
	return findLiteralRangeInto(dst, source, pattern, startOffset), true, nil
}

// FindFixed4 实现 `string.find` 最多四实参的固定返回快路径。
//
// dst 至少需要容纳两个返回值；argCount 表示调用方实际提供的参数数量。该入口用于 VM 无 hook
// 热路径，直接接收寄存器值，避免构造参数切片和 variadic 调用。未覆盖 Lua pattern/capture 时
// 返回 handled=false，由调用方回退完整 Find 语义。
func FindFixed4(dst []runtime.Value, arg0 runtime.Value, arg1 runtime.Value, arg2 runtime.Value, arg3 runtime.Value, argCount int) (int, bool, error) {
	// 固定结果入口只写两个返回槽，调用方按 MaxResults 分配 dst。
	if len(dst) < 2 {
		// 结果槽不足时不能安全写入。
		return 0, false, runtime.ErrRegisterOutOfRange
	}
	if argCount < 2 {
		// 缺少必选参数时回退通用路径生成标准参数错误和调试帧。
		return 0, false, nil
	}
	if arg0.Kind != runtime.KindString {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return 0, true, badArgument("find", 1, "string expected")
	}
	if arg1.Kind != runtime.KindString {
		// 第二个参数不是 string 时直接返回 Lua 参数错误。
		return 0, true, badArgument("find", 2, "string expected")
	}

	startIndex := int64(1)
	if argCount >= 3 {
		// init 参数存在时必须可转换为 integer。
		convertedIndex, ok := arg2.ToInteger()
		if !ok {
			// 非整数起点无法换算为字节偏移。
			return 0, true, badArgument("find", 3, "integer expected")
		}
		startIndex = convertedIndex
	}

	source := arg0.String
	startOffset := normalizeStart(len(source), startIndex)
	if startOffset > len(source) {
		// 起点超过字符串尾部时必然找不到匹配。
		dst[0] = runtime.NilValue()
		return 1, true, nil
	}

	plain := false
	if argCount >= 4 {
		// 第四个参数按 Lua truthiness 决定是否禁用 pattern。
		plain = arg3.Truthy()
	}
	pattern := arg1.String
	if !plain && !isPlainPattern(pattern) {
		// 存在 Lua pattern magic 时必须回退完整引擎，避免 capture 和特殊模式被截断。
		return 0, false, nil
	}

	// 字面量路径直接写入调用方结果槽，避免构造临时 []Value。
	return findLiteralRangeInto(dst, source, pattern, startOffset), true, nil
}

// findPlainFixedFast 执行 string.find plain=true 的固定返回快路径。
//
// dst 至少包含两个槽位；args 必须已有第四个 truthy 参数。返回 ok=false 表示参数形态不满足
// 窄快路径，调用方继续走完整 FindFixed 校验。
func findPlainFixedFast(dst []runtime.Value, args []runtime.Value) (int, bool, error) {
	if len(args) < 2 {
		// 缺少必选参数时交回通用路径生成标准参数错误。
		return 0, false, nil
	}
	if args[0].Kind != runtime.KindString {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return 0, true, badArgument("find", 1, "string expected")
	}
	if args[1].Kind != runtime.KindString {
		// 第二个参数不是 string 时直接返回 Lua 参数错误。
		return 0, true, badArgument("find", 2, "string expected")
	}
	startIndex := int64(1)
	if len(args) >= 3 {
		// init 参数存在时必须可转换为 integer。
		convertedIndex, ok := args[2].ToInteger()
		if !ok {
			// 非整数起点无法换算为字节偏移。
			return 0, true, badArgument("find", 3, "integer expected")
		}
		startIndex = convertedIndex
	}
	source := args[0].String
	startOffset := normalizeStart(len(source), startIndex)
	if startOffset > len(source) {
		// 起点超过字符串尾部时必然找不到匹配。
		dst[0] = runtime.NilValue()
		return 1, true, nil
	}

	// plain=true 明确禁用 pattern，直接执行字面量查找。
	return findLiteralRangeInto(dst, source, args[1].String, startOffset), true, nil
}

// findLiteralRange 执行 string.find 的字面量查找并返回 Lua 1-based 闭区间。
//
// source 和 pattern 必须已经由调用方完成参数校验；startOffset 是 Go 0-based 起点。未命中时
// 返回单个 nil，命中时返回起止位置两个 integer。
func findLiteralRange(source string, pattern string, startOffset int) []runtime.Value {
	var results [2]runtime.Value
	resultCount := findLiteralRangeInto(results[:], source, pattern, startOffset)
	return append([]runtime.Value(nil), results[:resultCount]...)
}

// findLiteralRangeInto 执行 string.find 字面量查找并写入调用方提供的结果槽。
//
// dst 至少包含两个元素；未命中时写入单个 nil，命中时写入 Lua 1-based 闭区间并返回 2。
func findLiteralRangeInto(dst []runtime.Value, source string, pattern string, startOffset int) int {
	// 字面量路径直接复用 Go 标准库的线性查找。
	matchOffset := strings.Index(source[startOffset:], pattern)
	if matchOffset < 0 {
		// literal 查找失败时返回单个 nil。
		dst[0] = runtime.NilValue()
		return 1
	}

	matchStart := startOffset + matchOffset
	matchEnd := matchStart + len(pattern)
	// 返回 Lua 1-based 闭区间起止位置。
	dst[0] = runtime.IntegerValue(int64(matchStart + 1))
	dst[1] = runtime.IntegerValue(int64(matchEnd))
	return 2
}

// isPlainPattern 判断 pattern 是否不含 Lua pattern 魔法字符。
//
// 返回 true 表示 find 语义等价于字面字符串查找；`%` 本身也是 magic 字符，因此转义 pattern
// 会保留给通用 pattern 引擎解释。
func isPlainPattern(pattern string) bool {
	for index := 0; index < len(pattern); index++ {
		// 任一 magic 字节都可能改变 pattern 语义，不能使用字面量快路径。
		if isPatternMagicLiteral(pattern[index]) {
			return false
		}
	}

	// 没有 magic 字符时可以安全使用字面量查找。
	return true
}

// Format 实现 Lua 5.3 `string.format` 的基础格式化语义。
//
// 第一个参数必须是格式字符串；当前阶段支持 `%%`、`%s`、`%q`、`%d`、`%i`、`%f` 和 `%g`，
// 并支持这些格式项的常见 flag、宽度和精度子集。
func Format(args ...runtime.Value) ([]runtime.Value, error) {
	// 不带 State 的入口保留给单测和纯 Go 元方法路径使用。
	return formatWithState(nil, args...)
}

// newFormatFixedResultsFunction 构造 string.format 的固定单返回快路径描述。
//
// state 必须来自当前打开 string 库的 State；未命中 exact `%d` 热路径时，Fallback 会继续使用
// formatWithState，从而保留 `%s` 调用 Lua closure `__tostring` 的能力与完整错误语义。
func newFormatFixedResultsFunction(state *runtime.State) *runtime.GoFixedResultsFunction {
	// format 永远最多返回一个字符串，窄快路径只负责无错误 exact `%d` 成功场景。
	return &runtime.GoFixedResultsFunction{
		MaxResults:      1,
		Function4Single: FormatFixed4Single,
		Function4:       FormatFixed4,
		Function:        FormatFixed,
		Fallback: func(args ...runtime.Value) ([]runtime.Value, error) {
			// 未命中固定结果快路径时回到原始 State-aware 实现。
			return formatWithState(state, args...)
		},
	}
}

// FormatFixed 实现 string.format exact `%d` 的固定单返回快路径。
//
// dst 至少需要容纳一个返回值；仅当格式串精确等于 `%d` 且第二参数可按 Lua 整数格式转换时
// 返回 handled=true。缺参、错误参数、其它格式和需要 State 的 `%s` 都返回 handled=false，
// 由 Fallback 保留 Lua 5.3 错误、debug frame 和 `__tostring` 语义。
func FormatFixed(dst []runtime.Value, args ...runtime.Value) (int, bool, error) {
	// 固定返回入口先校验结果槽，避免后续写越界。
	if len(dst) < 1 {
		// 调用方声明的 MaxResults 与结果槽不一致时不能安全处理。
		return 0, false, runtime.ErrRegisterOutOfRange
	}
	if len(args) < 2 {
		// 缺少格式串或格式参数时交给完整实现生成 Lua 错误文本。
		return 0, false, nil
	}

	// 只需要前两个参数；exact `%d` 按 Lua 语义忽略多余实参。
	return FormatFixed4(dst, args[0], args[1], runtime.NilValue(), runtime.NilValue(), len(args))
}

// FormatFixed4 实现最多四实参入口的 string.format exact `%d` 固定单返回快路径。
//
// dst 至少需要容纳一个返回值；argCount 表示实际参数数量。该入口只写入单个字符串结果，
// 其它格式全部回退完整 Format，避免破坏宽度、精度、`%s` 元方法和错误路径。
func FormatFixed4(dst []runtime.Value, arg0 runtime.Value, arg1 runtime.Value, arg2 runtime.Value, arg3 runtime.Value, argCount int) (int, bool, error) {
	// 固定返回入口只写一个槽位，调用方按 MaxResults 分配 dst。
	if len(dst) < 1 {
		// 结果槽不足时不能安全写入。
		return 0, false, runtime.ErrRegisterOutOfRange
	}

	result, resultCount, handled, err := FormatFixed4Single(arg0, arg1, arg2, arg3, argCount)
	if err != nil || !handled {
		// 错误或未命中都交给调用方保持既有回退语义。
		return 0, handled, err
	}
	if resultCount > 0 {
		// exact `%d` 成功路径返回一个格式化字符串。
		dst[0] = result
	}
	return resultCount, true, nil
}

// FormatFixed4Single 实现 string.format("%d", value) 的单返回直接寄存器快路径。
//
// arg0 必须是精确格式串 `%d`，arg1 必须可按 string.format 的整数格式转换；其它形态返回
// handled=false，由完整 formatWithState 保留 Lua 5.3 兼容边界。
func FormatFixed4Single(arg0 runtime.Value, arg1 runtime.Value, _ runtime.Value, _ runtime.Value, argCount int) (runtime.Value, int, bool, error) {
	// exact `%d` 至少需要格式串和值两个实参，缺参交给完整实现生成 `no value`。
	if argCount < 2 {
		// 不在快路径构造错误，避免绕过 debug-frame 回退。
		return runtime.NilValue(), 0, false, nil
	}
	if arg0.Kind != runtime.KindString {
		// 第一个实参不是格式字符串时回退完整参数错误。
		return runtime.NilValue(), 0, false, nil
	}
	if arg0.String != "%d" {
		// 只覆盖官方标准库 benchmark 的 exact `%d` 热格式。
		return runtime.NilValue(), 0, false, nil
	}

	integerValue, ok := formatIntegerValue(arg1)
	if !ok {
		// 非整数参数必须回退完整 bad argument 名称重写和 traceback 语义。
		return runtime.NilValue(), 0, false, nil
	}
	return runtime.StringValue(strconv.FormatInt(integerValue, 10)), 1, true, nil
}

// formatWithState 实现 Lua 5.3 `string.format`，并在可用时让 `%s` 执行 Lua closure `__tostring`。
//
// state 可为 nil；nil 时 `%s` 只支持基础转换和 Go closure `__tostring` 元方法。
func formatWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// format 首先解析格式字符串。
	formatText, err := stringArgument(args, 1, "format")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	if formatText == "%d" {
		// 标准库混合热路径频繁调用 string.format("%d", i)，精确 %d 可跳过格式解析和 fmt.Sprintf。
		if len(args) <= 1 {
			// 缺少对应实参时保持通用路径的 Lua error 文本。
			return nil, runtime.RaiseError(runtime.StringValue("no value"))
		}
		integerValue, ok := formatIntegerValue(args[1])
		if !ok {
			// 非整数仍使用 string.format 的第二参数错误语义。
			return nil, badArgument("format", 2, "integer expected")
		}
		return []runtime.Value{runtime.StringValue(strconv.FormatInt(integerValue, 10))}, nil
	}

	var builder strings.Builder
	argumentIndex := 1
	for index := 0; index < len(formatText); index++ {
		// 普通字符直接写入输出。
		if formatText[index] != '%' {
			builder.WriteByte(formatText[index])
			continue
		}
		if index+1 >= len(formatText) {
			// 尾部单独的 % 不是合法格式。
			return nil, runtime.RaiseError(runtime.StringValue("invalid option '%' to 'format'"))
		}

		if formatText[index+1] == '%' {
			// %% 输出字面百分号，不消耗参数。
			builder.WriteByte('%')
			index++
			continue
		}
		formatOption, nextIndex, parseErr := parseFormatOption(formatText, index)
		if parseErr != nil {
			// 格式项解析失败时按 Lua error 传播。
			return nil, parseErr
		}
		index = nextIndex
		if argumentIndex >= len(args) {
			// 缺少对应实参时，格式化不能继续。
			return nil, runtime.RaiseError(runtime.StringValue("no value"))
		}

		formatted, formatErr := formatOneWithState(state, formatOption, args[argumentIndex], argumentIndex+1)
		if formatErr != nil {
			// 单个格式项失败时返回 Lua error。
			return nil, formatErr
		}
		builder.WriteString(formatted)
		argumentIndex++
	}

	// 返回完整格式化字符串。
	return []runtime.Value{runtime.StringValue(builder.String())}, nil
}

// Match 实现 Lua 5.3 `string.match` 的第一阶段 pattern 匹配语义。
//
// 第一个参数 source 和第二个参数 pattern 必须是 string；第三个参数 init 可选且必须为
// integer。当前 pattern 引擎支持 literal、`.`、常用字符类、`[]` 字符集、`* + - ?` 量词、
// capture、`%bxy` balanced match 和 `%f[]` frontier pattern。匹配失败返回单个 nil。
func Match(args ...runtime.Value) ([]runtime.Value, error) {
	// match 先解析源字符串和 pattern。
	source, err := stringArgument(args, 1, "match")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	pattern, err := stringArgument(args, 2, "match")
	if err != nil {
		// 第二个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	startIndex := int64(1)
	if len(args) >= 3 {
		// init 参数存在时必须可转换为 integer。
		convertedIndex, ok := args[2].ToInteger()
		if !ok {
			// 非整数起点无法换算为字节偏移。
			return nil, badArgument("match", 3, "integer expected")
		}
		startIndex = convertedIndex
	}

	matchResult, ok, matchErr := findPattern(source, pattern, normalizeStart(len(source), startIndex))
	if matchErr != nil {
		// pattern 语法或执行错误直接返回 Lua error。
		return nil, matchErr
	}
	if !ok {
		// 未匹配时返回单个 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	if len(matchResult.captures) == 0 {
		// 无 capture 时返回完整匹配文本。
		return []runtime.Value{runtime.StringValue(source[matchResult.start:matchResult.end])}, nil
	}

	results := make([]runtime.Value, 0, len(matchResult.captures))
	for _, capture := range orderedCaptures(matchResult.captures) {
		// capture 返回对应源字符串切片或 1-based 位置。
		results = append(results, captureValue(source, capture))
	}
	return results, nil
}

// GMatch 实现 Lua 5.3 `string.gmatch` 的第一阶段迭代器语义。
//
// 第一个参数 source 和第二个参数 pattern 必须是 string。返回值是一个 Go closure iterator；
// 每次调用返回下一次匹配的 capture 列表，或无 capture 时返回完整匹配文本。迭代结束返回零个
// 结果。当前实现复用本包第一阶段 pattern 引擎。
func GMatch(args ...runtime.Value) ([]runtime.Value, error) {
	// gmatch 先解析源字符串和 pattern。
	source, err := stringArgument(args, 1, "gmatch")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	pattern, err := stringArgument(args, 2, "gmatch")
	if err != nil {
		// 第二个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	iterator := newPatternIterator(source, pattern, 0)
	function := runtime.GoResultsFunction(func(values ...runtime.Value) ([]runtime.Value, error) {
		// gmatch iterator 不使用调用参数，只沿闭包内偏移向前推进。
		matchResult, ok, nextErr := iterator.next()
		if nextErr != nil {
			// pattern 执行错误原样返回。
			return nil, nextErr
		}
		if !ok {
			// 迭代结束时返回零个结果，Lua 泛型 for 会停止。
			return nil, nil
		}
		return patternValues(source, matchResult), nil
	})

	// 返回可直接由当前 Go closure 调用器执行的 iterator，并暴露一个匿名 debug upvalue。
	return []runtime.Value{runtime.ReferenceValue(runtime.KindGoClosure, &runtime.GoClosureWithUpvalues{
		Function: function,
		Upvalues: []runtime.Value{runtime.StringValue(source)},
	})}, nil
}

// GSub 实现 Lua 5.3 `string.gsub` 的第一阶段替换语义。
//
// 第一个参数 source、第二个参数 pattern 必须是 string；第三个 repl 当前支持 string、table 或
// Go closure；第四个 n 可选且必须是 integer。返回替换后的字符串和替换次数。string repl 支持
// `%0` 表示完整匹配、`%1..%9` 表示 capture、`%%` 表示字面 `%`；table repl 以第一 capture
// 或完整匹配作为 key 读取 string 替换值，未命中时保留原匹配文本；Go closure repl 使用
// capture 或完整匹配作为实参，首个返回值为 string/number 时作为替换文本，nil/false 保留原文。
func GSub(args ...runtime.Value) ([]runtime.Value, error) {
	// 不带 State 的入口保留给单测和纯 Go 调用；Lua closure 替换函数需经 Open 注册后使用。
	return gsubWithState(nil, args...)
}

// gsubWithState 实现 Lua 5.3 `string.gsub`，并在可用时调用 Lua closure 替换函数。
//
// state 可为 nil；nil 时只支持 string、table 和 Go closure 替换函数。非 nil 时 Lua closure
// 替换函数会通过 State 注入的 runner 执行，避免 string 包依赖 lua 包。
func gsubWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// gsub 先解析源字符串和 pattern。
	source, err := stringArgument(args, 1, "gsub")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	pattern, err := stringArgument(args, 2, "gsub")
	if err != nil {
		// 第二个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	if len(args) < 3 {
		// 缺少替换参数时无法执行替换。
		return nil, badArgument("gsub", 3, "string, function or table expected")
	}

	limit := int64(math.MaxInt64)
	if len(args) >= 4 {
		// n 参数存在时必须是 integer。
		limit, err = integerArgument(args, 4, "gsub")
		if err != nil {
			// n 类型错误时不执行替换。
			return nil, err
		}
	}
	if limit <= 0 {
		// 非正替换次数表示不替换，直接返回原字符串和 0。
		return []runtime.Value{runtime.StringValue(source), runtime.IntegerValue(0)}, nil
	}

	iterator := newPatternIterator(source, pattern, 0)
	var builder strings.Builder
	lastCopiedOffset := 0
	replacementCount := int64(0)
	for replacementCount < limit {
		// 查找下一处匹配。
		matchResult, ok, nextErr := iterator.next()
		if nextErr != nil {
			// pattern 执行错误原样返回。
			return nil, nextErr
		}
		if !ok {
			// 无更多匹配时结束替换循环。
			break
		}

		replacement, replaceErr := replacementForMatch(state, args[2], source, matchResult)
		if replaceErr != nil {
			// 替换值类型错误时返回 Lua error。
			return nil, replaceErr
		}
		builder.WriteString(source[lastCopiedOffset:matchResult.start])
		builder.WriteString(replacement)
		lastCopiedOffset = matchResult.end
		replacementCount++
	}
	builder.WriteString(source[lastCopiedOffset:])

	// 返回替换结果和替换次数。
	return []runtime.Value{runtime.StringValue(builder.String()), runtime.IntegerValue(replacementCount)}, nil
}

// Len 实现 Lua 5.3 `string.len` 的字节长度语义。
//
// 第一个参数必须是 string；返回值是底层字节数量，不按 Unicode rune 或字符宽度计算。
func Len(args ...runtime.Value) ([]runtime.Value, error) {
	// len 首先执行单返回入口，保持导出多返回签名兼容既有调用方。
	value, err := LenValue(args...)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}

	// 多返回 API 包装为单元素切片。
	return []runtime.Value{value}, nil
}

// LenValue 实现 Lua 5.3 `string.len` 的单返回热路径。
//
// 第一个参数必须是 string；返回值是底层字节数量，不按 Unicode rune 或字符宽度计算。错误语义
// 与 Len 保持一致，供 VM 内部 GoFunction 快路径避免结果切片分配。
func LenValue(args ...runtime.Value) (runtime.Value, error) {
	// len 首先解析目标字符串。
	source, err := stringArgument(args, 1, "len")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return runtime.NilValue(), err
	}

	// Lua string.len 返回字节长度。
	return runtime.IntegerValue(int64(len(source))), nil
}

// LenUnaryValue 实现 Lua 5.3 `string.len` 的单参数单返回热路径。
//
// value 是调用方已经从 Lua 寄存器读取出的第一个参数；返回语义和错误语义与 LenValue 保持一致。
func LenUnaryValue(value runtime.Value) (runtime.Value, error) {
	// len 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindString {
		// 非 string 类型不做 tostring 隐式转换。
		return runtime.NilValue(), badArgument("len", 1, "string expected")
	}

	// Lua string.len 返回字节长度。
	return runtime.IntegerValue(int64(len(value.String))), nil
}

// Lower 实现 Lua 5.3 `string.lower` 的基础 ASCII 小写转换。
//
// 第一个参数必须是 string；当前阶段只转换 ASCII `A-Z` 字节，其他字节原样保留，避免 Go
// Unicode 大小写映射改变 Lua 字节字符串长度。完整 locale 行为后续单独评估。
func Lower(args ...runtime.Value) ([]runtime.Value, error) {
	// lower 首先解析目标字符串。
	source, err := stringArgument(args, 1, "lower")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	bytes := []byte(source)
	for index, value := range bytes {
		// 只对 ASCII 大写字母做单字节转换。
		if value >= 'A' && value <= 'Z' {
			// ASCII 大小写字母相差 32，转换不会改变字节长度。
			bytes[index] = value + ('a' - 'A')
		}
	}
	return []runtime.Value{runtime.StringValue(string(bytes))}, nil
}

// LowerUnaryValue 实现 Lua 5.3 `string.lower` 的单参数单返回热路径。
//
// value 必须是 string；当前阶段只转换 ASCII `A-Z` 字节，其他字节原样保留。该入口服务
// VM CALL fast path，避免标准库热点调用构造临时参数和结果切片。
func LowerUnaryValue(value runtime.Value) (runtime.Value, error) {
	// lower 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindString {
		// 非 string 类型不做 tostring 隐式转换。
		return runtime.NilValue(), badArgument("lower", 1, "string expected")
	}

	bytes := []byte(value.String)
	for index, currentByte := range bytes {
		// 只对 ASCII 大写字母做单字节转换。
		if currentByte >= 'A' && currentByte <= 'Z' {
			// ASCII 大小写字母相差 32，转换不会改变字节长度。
			bytes[index] = currentByte + ('a' - 'A')
		}
	}
	return runtime.StringValue(string(bytes)), nil
}

// Rep 实现 Lua 5.3 `string.rep` 的基础重复语义。
//
// 第一个参数必须是 string；第二个参数 n 必须是 integer；第三个参数 sep 可选且必须是
// string。n 小于等于 0 时返回空字符串；sep 会插入在相邻重复片段之间。
func Rep(args ...runtime.Value) ([]runtime.Value, error) {
	// rep 首先解析待重复字符串。
	source, err := stringArgument(args, 1, "rep")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	repeatCount, err := integerArgument(args, 2, "rep")
	if err != nil {
		// 第二个参数必须是重复次数。
		return nil, err
	}

	separator := ""
	if len(args) >= 3 {
		// sep 参数存在时必须是 string。
		separator, err = stringArgument(args, 3, "rep")
		if err != nil {
			// sep 类型错误时不构造部分结果。
			return nil, err
		}
	}
	if repeatCount <= 0 {
		// Lua string.rep 对非正重复次数返回空字符串。
		return []runtime.Value{runtime.StringValue("")}, nil
	}

	if repeatCount > int64(int(repeatCount)) {
		// 当前宿主无法分配超过 int 上限的切片，返回明确 Lua error。
		return nil, runtime.RaiseError(runtime.StringValue("resulting string too large"))
	}
	resultLength := int64(len(source))*repeatCount + int64(len(separator))*(repeatCount-1)
	if resultLength < 0 || resultLength >= maxStringRepResultBytes {
		// 结果长度溢出或超过当前保护上限时，按 Lua 官方错误文本关键字返回。
		return nil, runtime.RaiseError(runtime.StringValue("resulting string too large"))
	}

	parts := make([]string, int(repeatCount))
	for index := range parts {
		// 每个片段都等于源字符串。
		parts[index] = source
	}
	return []runtime.Value{runtime.StringValue(strings.Join(parts, separator))}, nil
}

// Reverse 实现 Lua 5.3 `string.reverse` 的字节反转语义。
//
// 第一个参数必须是 string；返回值按底层字节反向排列，不按 Unicode rune 反转。
func Reverse(args ...runtime.Value) ([]runtime.Value, error) {
	// reverse 首先解析目标字符串。
	source, err := stringArgument(args, 1, "reverse")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	bytes := []byte(source)
	for leftIndex, rightIndex := 0, len(bytes)-1; leftIndex < rightIndex; leftIndex, rightIndex = leftIndex+1, rightIndex-1 {
		// 交换两端字节，直到左右游标相遇。
		bytes[leftIndex], bytes[rightIndex] = bytes[rightIndex], bytes[leftIndex]
	}
	return []runtime.Value{runtime.StringValue(string(bytes))}, nil
}

// ReverseUnaryValue 实现 Lua 5.3 `string.reverse` 的单参数单返回热路径。
//
// value 必须是 string；返回值按底层字节反向排列，不按 Unicode rune 反转。该入口服务
// VM CALL fast path，避免标准库热点调用构造临时参数和结果切片。
func ReverseUnaryValue(value runtime.Value) (runtime.Value, error) {
	// reverse 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindString {
		// 非 string 类型不做 tostring 隐式转换。
		return runtime.NilValue(), badArgument("reverse", 1, "string expected")
	}

	bytes := []byte(value.String)
	for leftIndex, rightIndex := 0, len(bytes)-1; leftIndex < rightIndex; leftIndex, rightIndex = leftIndex+1, rightIndex-1 {
		// 交换两端字节，直到左右游标相遇。
		bytes[leftIndex], bytes[rightIndex] = bytes[rightIndex], bytes[leftIndex]
	}
	return runtime.StringValue(string(bytes)), nil
}

// Sub 实现 Lua 5.3 `string.sub` 的字节切片语义。
//
// 第一个参数必须是 string；第二个参数 i 必须是 integer；第三个参数 j 可选，默认 -1。
// 负索引按字符串长度从尾部换算；裁剪后为空区间时返回空字符串。
func Sub(args ...runtime.Value) ([]runtime.Value, error) {
	// sub 首先解析目标字符串和起始索引。
	source, err := stringArgument(args, 1, "sub")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	startIndex, err := integerArgument(args, 2, "sub")
	if err != nil {
		// 第二个参数必须是起始索引。
		return nil, err
	}

	endIndex := int64(-1)
	if len(args) >= 3 {
		// j 参数存在时必须是 integer。
		endIndex, err = integerArgument(args, 3, "sub")
		if err != nil {
			// 终点索引类型错误时不返回部分字符串。
			return nil, err
		}
	}

	startOffset, endOffset, ok := normalizeRange(len(source), startIndex, endIndex)
	if !ok {
		// 空区间返回空字符串，而不是 nil。
		return []runtime.Value{runtime.StringValue("")}, nil
	}

	// Go 半开区间直接截取底层字节字符串。
	return []runtime.Value{runtime.StringValue(source[startOffset:endOffset])}, nil
}

// SubFixed 实现 `string.sub` 固定单返回快路径。
//
// dst 至少需要容纳一个返回值；仅覆盖二到三实参的普通 sub 调用。参数数量不匹配时返回
// handled=false 交给 Sub 生成完整 Lua 参数错误或默认值语义。
func SubFixed(dst []runtime.Value, args ...runtime.Value) (int, bool, error) {
	// 固定返回入口先校验结果槽，避免后续写越界。
	if len(dst) < 1 {
		// 调用方声明的 MaxResults 与结果槽不一致时不能安全处理。
		return 0, false, runtime.ErrRegisterOutOfRange
	}
	if len(args) < 2 || len(args) > 3 {
		// sub 至少需要 source 和 i，其他参数数量交给完整路径处理。
		return 0, false, nil
	}

	var arg0 runtime.Value
	var arg1 runtime.Value
	var arg2 runtime.Value
	if len(args) >= 1 {
		// 第一个参数是源字符串。
		arg0 = args[0]
	}
	if len(args) >= 2 {
		// 第二个参数是起始索引。
		arg1 = args[1]
	}
	if len(args) >= 3 {
		// 第三个参数是结束索引。
		arg2 = args[2]
	}

	// 复用最多四实参入口，保持直接寄存器快路径和 debug-frame 回退路径语义一致。
	return SubFixed4(dst, arg0, arg1, arg2, runtime.NilValue(), len(args))
}

// SubFixed4 实现 `string.sub` 最多三实参的固定单返回快路径。
//
// dst 至少需要容纳一个返回值；argCount 表示实际参数数量。该入口直接写出单个字符串结果，
// 与 Lua 5.3 的字节切片、负索引和空区间返回空字符串语义保持一致。
func SubFixed4(dst []runtime.Value, arg0 runtime.Value, arg1 runtime.Value, arg2 runtime.Value, _ runtime.Value, argCount int) (int, bool, error) {
	// 固定返回入口只写一个槽位，调用方按 MaxResults 分配 dst。
	if len(dst) < 1 {
		// 结果槽不足时不能安全写入。
		return 0, false, runtime.ErrRegisterOutOfRange
	}
	if argCount < 2 || argCount > 3 {
		// 参数数量不在窄快路径覆盖范围时回退完整实现。
		return 0, false, nil
	}
	if arg0.Kind != runtime.KindString {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return 0, true, badArgument("sub", 1, "string expected")
	}
	startIndex, err := integerValueArgument(arg1, 2, "sub")
	if err != nil {
		// 第二个参数必须是起始索引。
		return 0, true, err
	}

	endIndex := int64(-1)
	if argCount >= 3 {
		// j 参数存在时必须是 integer。
		convertedIndex, convertedErr := integerValueArgument(arg2, 3, "sub")
		if convertedErr != nil {
			// 终点索引类型错误时不返回部分字符串。
			return 0, true, convertedErr
		}
		endIndex = convertedIndex
	}

	source := arg0.String
	startOffset, endOffset, rangeOK := normalizeRange(len(source), startIndex, endIndex)
	if !rangeOK {
		// 空区间返回空字符串，而不是 nil。
		dst[0] = runtime.StringValue("")
		return 1, true, nil
	}

	// Go 半开区间直接截取底层字节字符串。
	dst[0] = runtime.StringValue(source[startOffset:endOffset])
	return 1, true, nil
}

// SubFixed4Single 实现 `string.sub` 最多三实参的固定单返回无槽位快路径。
//
// argCount 表示实际参数数量。该入口直接返回单个字符串结果，与 Lua 5.3 的字节切片、
// 负索引和空区间返回空字符串语义保持一致。
func SubFixed4Single(arg0 runtime.Value, arg1 runtime.Value, arg2 runtime.Value, _ runtime.Value, argCount int) (runtime.Value, int, bool, error) {
	// 单返回入口直接复用寄存器实参，不构造结果槽。
	if argCount < 2 || argCount > 3 {
		// 参数数量不在窄快路径覆盖范围时回退完整实现。
		return runtime.NilValue(), 0, false, nil
	}
	if arg0.Kind != runtime.KindString {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return runtime.NilValue(), 0, true, badArgument("sub", 1, "string expected")
	}
	startIndex, err := integerValueArgument(arg1, 2, "sub")
	if err != nil {
		// 第二个参数必须是起始索引。
		return runtime.NilValue(), 0, true, err
	}

	endIndex := int64(-1)
	if argCount >= 3 {
		// j 参数存在时必须是 integer。
		convertedIndex, convertedErr := integerValueArgument(arg2, 3, "sub")
		if convertedErr != nil {
			// 终点索引类型错误时不返回部分字符串。
			return runtime.NilValue(), 0, true, convertedErr
		}
		endIndex = convertedIndex
	}

	source := arg0.String
	startOffset, endOffset, rangeOK := normalizeRange(len(source), startIndex, endIndex)
	if !rangeOK {
		// 空区间返回空字符串，而不是 nil。
		return runtime.StringValue(""), 1, true, nil
	}

	// Go 半开区间直接截取底层字节字符串。
	return runtime.StringValue(source[startOffset:endOffset]), 1, true, nil
}

// Pack 实现 Lua 5.3 `string.pack` 的第一阶段二进制打包语义。
//
// 第一个参数必须是格式字符串；当前支持端序标记 `<`、`>`、`=`，忽略对齐控制 `!n`，并实现
// `bBhHiIlLjJTfdcNzsNxx` 的常用编码。整数按指定字节数截断为补码/无符号表示；字符串格式
// `cN` 固定写入 N 字节，`z` 写入零结尾字符串，`sN` 写入 N 字节无符号长度前缀再写内容。
func Pack(args ...runtime.Value) ([]runtime.Value, error) {
	// pack 首先解析格式字符串。
	formatText, err := stringArgument(args, 1, "pack")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	parser := newPackFormatParser(formatText)
	var builder strings.Builder
	argumentIndex := 1
	for {
		// 逐项读取格式 token，直到格式串结束。
		token, ok, tokenErr := parser.next()
		if tokenErr != nil {
			// 格式串非法时返回 Lua error。
			return nil, tokenErr
		}
		if !ok {
			// 没有更多格式项时结束打包。
			break
		}
		if token.kind == packTokenPadding {
			// x 只写入一个 NUL 字节，不消耗参数。
			builder.WriteByte(0)
			continue
		}
		if token.kind == packTokenAlign {
			// Xop 只根据当前输出长度插入对齐 NUL 字节。
			writePadding(&builder, alignmentPadding(builder.Len(), token.align))
			continue
		}
		if token.kind == packTokenControl {
			// 端序和对齐控制只影响后续 token，不消耗参数也不写入数据。
			continue
		}
		if argumentIndex >= len(args) {
			// 缺少格式项对应的值时不能继续写入。
			return nil, badArgument("pack", argumentIndex+1, "value expected")
		}

		writePadding(&builder, alignmentPadding(builder.Len(), token.align))
		if err := packOne(&builder, token, parser.order, args[argumentIndex], argumentIndex+1); err != nil {
			// 单个格式项打包失败时返回 Lua error。
			return nil, err
		}
		argumentIndex++
	}

	// 返回完整二进制字符串。
	return []runtime.Value{runtime.StringValue(builder.String())}, nil
}

// PackSize 实现 Lua 5.3 `string.packsize` 的第一阶段固定尺寸计算语义。
//
// 第一个参数必须是格式字符串；返回值是不依赖实参内容的编码字节数。`z` 和 `sN` 属于
// 可变长度格式，当前按 Lua 语义返回错误；端序标记、对齐控制和空白不计入长度。
func PackSize(args ...runtime.Value) ([]runtime.Value, error) {
	// packsize 首先解析格式字符串。
	formatText, err := stringArgument(args, 1, "packsize")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	parser := newPackFormatParser(formatText)
	var size int64
	for {
		// 逐项读取格式 token，累计固定尺寸。
		token, ok, tokenErr := parser.next()
		if tokenErr != nil {
			// 格式串非法时返回 Lua error。
			return nil, tokenErr
		}
		if !ok {
			// 格式串读取完成，返回累计尺寸。
			break
		}
		if token.kind == packTokenControl {
			// 控制 token 不贡献输出长度。
			continue
		}
		if token.kind == packTokenAlign {
			// Xop 只贡献为了到达目标对齐而插入的填充字节。
			size += int64(alignmentPadding(int(size), token.align))
			continue
		}
		if token.code == 'z' || token.code == 's' {
			// 可变长度格式无法在没有实参时确定大小。
			return nil, runtime.RaiseError(runtime.StringValue("variable-length format"))
		}
		size += int64(alignmentPadding(int(size), token.align))
		size += int64(token.size)
		if size > maxStringRepResultBytes {
			// Lua 5.3 packsize 使用有界整数，超过 2GB 级别需要报 too large。
			return nil, runtime.RaiseError(runtime.StringValue("format result too large"))
		}
	}

	// 返回固定格式总字节数。
	return []runtime.Value{runtime.IntegerValue(size)}, nil
}

// Unpack 实现 Lua 5.3 `string.unpack` 的第一阶段二进制解包语义。
//
// 第一个参数必须是格式字符串；第二个参数必须是数据字符串；第三个参数 pos 可选，默认 1。
// 返回值按格式项顺序排列，最后追加下一次读取的 Lua 1-based 位置。支持的格式与 Pack 对称。
func Unpack(args ...runtime.Value) ([]runtime.Value, error) {
	// unpack 首先解析格式和源数据。
	formatText, err := stringArgument(args, 1, "unpack")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	source, err := stringArgument(args, 2, "unpack")
	if err != nil {
		// 第二个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	position := int64(1)
	if len(args) >= 3 {
		// pos 参数存在时必须是 integer。
		position, err = integerArgument(args, 3, "unpack")
		if err != nil {
			// pos 类型错误时不能开始读取。
			return nil, err
		}
	}
	if position < 0 {
		// 负索引按 Lua 字符串尾部换算为 1-based 起点。
		position = int64(len(source)) + position + 1
	}
	if position < 1 || position > int64(len(source))+1 {
		// 起始位置必须落在字符串边界内或末尾后一位。
		return nil, badArgument("unpack", 3, "initial position out of string")
	}

	offset := int(position - 1)
	parser := newPackFormatParser(formatText)
	results := make([]runtime.Value, 0)
	for {
		// 逐项读取格式 token，并从当前 offset 解包。
		token, ok, tokenErr := parser.next()
		if tokenErr != nil {
			// 格式串非法时返回 Lua error。
			return nil, tokenErr
		}
		if !ok {
			// 格式串结束时跳出并追加下一位置。
			break
		}
		if token.kind == packTokenControl {
			// 控制 token 不读取数据。
			continue
		}
		if token.kind == packTokenAlign {
			// Xop 只跳过当前 offset 到目标对齐的填充字节。
			nextOffset := offset + alignmentPadding(offset, token.align)
			if nextOffset > len(source) {
				// 对齐填充越过源字符串时数据不完整。
				return nil, runtime.RaiseError(runtime.StringValue("data string too short"))
			}
			offset = nextOffset
			continue
		}
		offset += alignmentPadding(offset, token.align)

		value, nextOffset, unpackErr := unpackOne(source, offset, token, parser.order)
		if unpackErr != nil {
			// 数据不足或格式不支持时直接返回错误。
			return nil, unpackErr
		}
		offset = nextOffset
		if token.kind != packTokenPadding {
			// x padding 不产生返回值，其他格式项返回解包值。
			results = append(results, value)
		}
	}
	results = append(results, runtime.IntegerValue(int64(offset+1)))
	return results, nil
}

// Upper 实现 Lua 5.3 `string.upper` 的基础 ASCII 大写转换。
//
// 第一个参数必须是 string；当前阶段只转换 ASCII `a-z` 字节，其他字节原样保留，避免 Go
// Unicode 大小写映射改变 Lua 字节字符串长度。完整 locale 行为后续单独评估。
func Upper(args ...runtime.Value) ([]runtime.Value, error) {
	// upper 首先解析目标字符串。
	source, err := stringArgument(args, 1, "upper")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	bytes := []byte(source)
	for index, value := range bytes {
		// 只对 ASCII 小写字母做单字节转换。
		if value >= 'a' && value <= 'z' {
			// ASCII 大小写字母相差 32，转换不会改变字节长度。
			bytes[index] = value - ('a' - 'A')
		}
	}
	return []runtime.Value{runtime.StringValue(string(bytes))}, nil
}

// UpperUnaryValue 实现 Lua 5.3 `string.upper` 的单参数单返回热路径。
//
// value 必须是 string；当前阶段只转换 ASCII `a-z` 字节，其他字节原样保留。该入口服务
// VM CALL fast path，避免标准库热点调用构造临时参数和结果切片。
func UpperUnaryValue(value runtime.Value) (runtime.Value, error) {
	// upper 一元入口直接校验首参数，避免为单参数 CALL 构造临时切片。
	if value.Kind != runtime.KindString {
		// 非 string 类型不做 tostring 隐式转换。
		return runtime.NilValue(), badArgument("upper", 1, "string expected")
	}

	bytes := []byte(value.String)
	for index, currentByte := range bytes {
		// 只对 ASCII 小写字母做单字节转换。
		if currentByte >= 'a' && currentByte <= 'z' {
			// ASCII 大小写字母相差 32，转换不会改变字节长度。
			bytes[index] = currentByte - ('a' - 'A')
		}
	}
	return runtime.StringValue(string(bytes)), nil
}

// packTokenKind 表示 string.pack 格式 token 的执行类别。
//
// 普通 value token 会消耗参数或产生返回值；padding 只写入/跳过 NUL；control 只改变解析状态。
type packTokenKind uint8

const (
	// packTokenValue 表示会处理一个 Lua 值的格式项。
	packTokenValue packTokenKind = iota
	// packTokenPadding 表示 `x` 填充字节。
	packTokenPadding
	// packTokenControl 表示端序或对齐控制项。
	packTokenControl
	// packTokenAlign 表示 `Xop` 只执行对齐填充，不读写实际值。
	packTokenAlign
)

// packToken 表示解析后的单个 string.pack 格式项。
//
// code 保存格式字符；size 保存固定字节数；kind 决定该 token 是否消耗参数或产生结果。
type packToken struct {
	// code 是格式字符，例如 `i`、`s` 或 `x`。
	code byte
	// size 是该格式项的字节数；对 `z` 为 0，表示扫描到 NUL。
	size int
	// align 是写入或读取该格式项前需要满足的对齐字节数。
	align int
	// kind 表示该 token 的执行类别。
	kind packTokenKind
}

// packFormatParser 保存 string.pack 格式串解析状态。
//
// order 保存当前端序；index 是格式串当前字节偏移。
type packFormatParser struct {
	// format 是原始格式字符串。
	format string
	// index 是下一个待解析字节偏移。
	index int
	// order 是当前整数/浮点编码端序。
	order binary.ByteOrder
	// maxAlign 是 `!n` 设置的当前最大对齐字节数。
	maxAlign int
}

// newPackFormatParser 创建格式串解析器。
//
// format 是 Lua 格式字符串；默认端序使用小端，和当前 chunk 编解码保持一致。
func newPackFormatParser(format string) *packFormatParser {
	// 初始化解析器，Lua 原生端序在本项目第一阶段固定为 little endian。
	return &packFormatParser{format: format, order: binary.LittleEndian, maxAlign: 1}
}

// next 读取下一个有效格式 token。
//
// 返回 ok=false 表示格式串已结束；空白会被跳过；非法格式返回 Lua error。
func (parser *packFormatParser) next() (packToken, bool, error) {
	for parser.index < len(parser.format) {
		// 跳过 Lua pack 格式允许的空白字符。
		if parser.format[parser.index] == ' ' || parser.format[parser.index] == '\n' || parser.format[parser.index] == '\t' {
			parser.index++
			continue
		}
		break
	}
	if parser.index >= len(parser.format) {
		// 没有更多格式项。
		return packToken{}, false, nil
	}

	code := parser.format[parser.index]
	parser.index++
	switch code {
	case '<':
		// < 切换后续多字节格式为小端。
		parser.order = binary.LittleEndian
		return packToken{code: code, kind: packTokenControl}, true, nil
	case '>', '=':
		// > 使用大端；= 在当前实现中按原生小端处理。
		if code == '>' {
			// 仅大端标记需要改写当前端序。
			parser.order = binary.BigEndian
		} else {
			// 原生端序在当前纯 Go 实现中固定为小端。
			parser.order = binary.LittleEndian
		}
		return packToken{code: code, kind: packTokenControl}, true, nil
	case '!':
		// !n 更新后续格式项的最大对齐；裸 ! 使用当前模拟的原生最大对齐。
		size, hasNumber := parser.readNumber()
		if !hasNumber {
			// Lua 5.3 允许裸 !，语义是恢复原生最大对齐。
			size = packNativeMaxAlign
		}
		if !validPackOptionSize(size) {
			// 对齐字节数必须落在官方允许的 1..16 范围内。
			return packToken{}, false, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("integral size (%d) out of limits [1,16]", size)))
		}
		if !isPowerOfTwo(size) {
			// Lua 5.3 要求对齐值为 2 的幂。
			return packToken{}, false, runtime.RaiseError(runtime.StringValue("format asks for alignment not power of 2"))
		}
		parser.maxAlign = size
		return packToken{code: code, kind: packTokenControl}, true, nil
	case 'x':
		// x 写入或跳过一个填充字节。
		return packToken{code: code, size: 1, kind: packTokenPadding}, true, nil
	case 'X':
		// Xop 按后续 op 的对齐要求插入空填充，但 op 本身不读写数据。
		token, err := parser.readAlignToken()
		if err != nil {
			// X 后没有合法固定宽度格式项时返回官方兼容错误。
			return packToken{}, false, err
		}
		return token, true, nil
	case 'b', 'B':
		// b/B 是 1 字节有符号/无符号整数。
		return parser.valueToken(code, 1), true, nil
	case 'h', 'H':
		// h/H 是 2 字节有符号/无符号整数。
		return parser.valueToken(code, 2), true, nil
	case 'i', 'I':
		// i/I 可带显式字节数，默认按 4 字节 int 处理。
		size, hasNumber := parser.readNumber()
		if !hasNumber {
			// 默认 int 大小按 Lua 5.3 常见 32 位 int。
			size = 4
		}
		if !validPackOptionSize(size) {
			// Lua 5.3 官方测试要求整数宽度落在 1..16。
			return packToken{}, false, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("integral size (%d) out of limits [1,16]", size)))
		}
		if parser.alignmentForSize(size) > 1 && !isPowerOfTwo(size) {
			// 需要参与对齐的整数宽度必须是 2 的幂。
			return packToken{}, false, runtime.RaiseError(runtime.StringValue("format asks for alignment not power of 2"))
		}
		return parser.valueToken(code, size), true, nil
	case 'l', 'L', 'j', 'J', 'T':
		// long、lua_Integer、size_t 当前统一按 8 字节处理。
		return parser.valueToken(code, 8), true, nil
	case 'f':
		// f 是 4 字节 IEEE float。
		return parser.valueToken(code, 4), true, nil
	case 'd', 'n':
		// d 是 8 字节 IEEE double；n 是 Lua number，当前实现同样使用 float64。
		return parser.valueToken(code, 8), true, nil
	case 'c':
		// cN 需要显式长度。
		size, hasNumber := parser.readNumber()
		if !hasNumber {
			// c 格式缺少长度时无法确定写入字节数。
			return packToken{}, false, runtime.RaiseError(runtime.StringValue("missing size for format option 'c'"))
		}
		if size < 0 {
			// 尺寸数字溢出或无法解析时，格式串本身非法。
			return packToken{}, false, runtime.RaiseError(runtime.StringValue("invalid format"))
		}
		return packToken{code: code, size: size, align: 1, kind: packTokenValue}, true, nil
	case 's':
		// sN 使用 N 字节无符号长度前缀，默认 8 字节。
		size, hasNumber := parser.readNumber()
		if !hasNumber {
			// 默认 size_t 大小按 8 字节处理。
			size = 8
		}
		if !validPackOptionSize(size) {
			// 长度前缀同样沿用 Lua 5.3 的 1..16 限制。
			return packToken{}, false, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("integral size (%d) out of limits [1,16]", size)))
		}
		if parser.alignmentForSize(size) > 1 && !isPowerOfTwo(size) {
			// sN 的长度前缀如果参与对齐，也必须使用 2 的幂宽度。
			return packToken{}, false, runtime.RaiseError(runtime.StringValue("format asks for alignment not power of 2"))
		}
		return packToken{code: code, size: size, align: parser.alignmentForSize(size), kind: packTokenValue}, true, nil
	case 'z':
		// z 是零结尾字符串。
		return packToken{code: code, size: 0, align: 1, kind: packTokenValue}, true, nil
	default:
		// 未知格式项返回明确 Lua error。
		return packToken{}, false, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid format option '%c'", code)))
	}
}

// readNumber 从当前格式位置读取十进制数字。
//
// 返回 hasNumber=false 表示当前位置没有数字；读取成功会推进 parser.index。
func (parser *packFormatParser) readNumber() (int, bool) {
	// 记录数字起点，便于判断是否真的读取到数字。
	start := parser.index
	for parser.index < len(parser.format) && parser.format[parser.index] >= '0' && parser.format[parser.index] <= '9' {
		// 连续十进制数字属于当前格式项的尺寸参数。
		parser.index++
	}
	if start == parser.index {
		// 没有数字参数。
		return 0, false
	}

	value, err := strconv.Atoi(parser.format[start:parser.index])
	if err != nil {
		// Atoi 理论上只可能因溢出失败，使用 -1 让上层报告非法格式或越界。
		return -1, true
	}
	return value, true
}

// valueToken 构造会读写实际 Lua 值的固定宽度格式项。
//
// code 是格式字符；size 是该项的存储字节数。返回 token 会携带当前 `!n` 对齐上限。
func (parser *packFormatParser) valueToken(code byte, size int) packToken {
	// 固定宽度数值项在 Lua 5.3 中按 min(size, maxAlign) 自动对齐。
	return packToken{code: code, size: size, align: parser.alignmentForSize(size), kind: packTokenValue}
}

// alignmentForSize 返回某个固定宽度格式项当前生效的对齐字节数。
//
// size 必须为正数；结果会被 `!n` 设置的最大对齐限制截断。
func (parser *packFormatParser) alignmentForSize(size int) int {
	// 默认 maxAlign=1 时不会产生任何隐式填充。
	if size < parser.maxAlign {
		// 小尺寸项按自身大小对齐。
		return size
	}
	return parser.maxAlign
}

// readAlignToken 读取 `Xop` 的被忽略格式项并转换成只对齐 token。
//
// `X` 后必须紧跟一个固定宽度且可对齐的格式项；空白、另一个 X、字符串格式或控制项都非法。
func (parser *packFormatParser) readAlignToken() (packToken, error) {
	if parser.index >= len(parser.format) {
		// X 后没有任何格式项，官方错误为 invalid next option。
		return packToken{}, runtime.RaiseError(runtime.StringValue("invalid next option for option 'X'"))
	}
	nextCode := parser.format[parser.index]
	if nextCode == ' ' || nextCode == '\n' || nextCode == '\t' {
		// Lua 5.3 要求 X 后的 op 紧邻出现，不能隔空白。
		return packToken{}, runtime.RaiseError(runtime.StringValue("invalid next option for option 'X'"))
	}
	parser.index++

	var size int
	switch nextCode {
	case 'b', 'B':
		// b/B 以 1 字节为对齐基准。
		size = 1
	case 'h', 'H':
		// h/H 以 2 字节为对齐基准。
		size = 2
	case 'i', 'I':
		// i/I 可显式给出 1..16 字节宽度。
		readSize, hasNumber := parser.readNumber()
		if !hasNumber {
			// 默认 int 对齐按 4 字节。
			readSize = 4
		}
		if !validPackOptionSize(readSize) {
			// X 后的整数宽度错误仍报告具体越界范围。
			return packToken{}, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("integral size (%d) out of limits [1,16]", readSize)))
		}
		if parser.alignmentForSize(readSize) > 1 && !isPowerOfTwo(readSize) {
			// X 引用的整数项若参与对齐，也必须是 2 的幂宽度。
			return packToken{}, runtime.RaiseError(runtime.StringValue("format asks for alignment not power of 2"))
		}
		size = readSize
	case 'l', 'L', 'j', 'J', 'T', 'd', 'n':
		// long、lua_Integer、size_t、double、lua_Number 当前均为 8 字节对齐基准。
		size = 8
	case 'f':
		// float 以 4 字节为对齐基准。
		size = 4
	default:
		// c/s/z/x/控制项/未知项都不是 X 可引用的下一选项。
		return packToken{}, runtime.RaiseError(runtime.StringValue("invalid next option for option 'X'"))
	}
	return packToken{code: 'X', align: parser.alignmentForSize(size), kind: packTokenAlign}, nil
}

// alignmentPadding 计算当前位置到指定对齐边界需要补齐的字节数。
//
// offset 是当前已读写字节数；align<=1 表示不需要填充。
func alignmentPadding(offset int, align int) int {
	if align <= 1 {
		// 1 字节对齐不会产生额外填充。
		return 0
	}
	reminder := offset % align
	if reminder == 0 {
		// 已经位于对齐边界，无需补齐。
		return 0
	}
	return align - reminder
}

// writePadding 向 builder 写入 count 个 NUL 填充字节。
//
// count 可以为 0；此时函数不改变输出。
func writePadding(builder *strings.Builder, count int) {
	for index := 0; index < count; index++ {
		// Lua pack 对齐填充固定使用 NUL 字节。
		builder.WriteByte(0)
	}
}

// isPowerOfTwo 判断 value 是否为正的 2 的幂。
//
// Lua pack 对齐控制只接受 1、2、4、8、16 这类值。
func isPowerOfTwo(value int) bool {
	// 正数且只有一个二进制位为 1 时就是 2 的幂。
	return value > 0 && value&(value-1) == 0
}

// packOne 按单个格式 token 写入一个 Lua 值。
//
// builder 接收二进制输出；token 是解析后的格式项；order 是当前端序；position 用于错误消息。
func packOne(builder *strings.Builder, token packToken, order binary.ByteOrder, value runtime.Value, position int) error {
	// 根据格式字符选择写入策略。
	switch token.code {
	case 'b', 'h', 'i', 'l', 'j':
		// 有符号整数按补码低位写入，并按目标宽度做溢出检查。
		integerValue, ok := value.ToInteger()
		if !ok {
			// 非整数不能用于整数格式。
			return badArgument("pack", position, "integer expected")
		}
		if !signedIntegerFits(integerValue, token.size) {
			// 值超出目标有符号宽度时必须拒绝，不能静默截断。
			return runtime.RaiseError(runtime.StringValue("integer overflow"))
		}
		writeSigned(builder, order, integerValue, token.size)
		return nil
	case 'B', 'H', 'I', 'L', 'J', 'T':
		// 无符号整数按 Lua integer 位模式写入，小于 8 字节时要求非负且可表示。
		integerValue, ok := value.ToInteger()
		if !ok {
			// 非整数不能用于无符号格式。
			return badArgument("pack", position, "unsigned integer expected")
		}
		if !unsignedIntegerFits(integerValue, token.size) {
			// 小宽度无符号整数不能容纳负数或超范围正数。
			return runtime.RaiseError(runtime.StringValue("unsigned overflow"))
		}
		writeUnsigned(builder, order, uint64(integerValue), token.size)
		return nil
	case 'f', 'd', 'n':
		// 浮点格式要求 number；n 按当前 Lua number 的 float64 表示写入。
		numberValue, ok := value.ToNumber()
		if !ok {
			// 非 number 不能用于浮点格式。
			return badArgument("pack", position, "number expected")
		}
		writeFloat(builder, order, numberValue, token.size)
		return nil
	case 'c':
		// cN 写入固定长度字符串，不足补 NUL，超长按官方语义报错。
		source, err := stringValueArgument(value, "pack", position)
		if err != nil {
			// cN 需要字符串参数。
			return err
		}
		if len(source) > token.size {
			// Lua 5.3 不允许 cN 静默截断超长字符串。
			return runtime.RaiseError(runtime.StringValue("string longer than given size"))
		}
		writeFixedString(builder, source, token.size)
		return nil
	case 'z':
		// z 写入字符串内容和 NUL 终止符，字符串内部不能含 NUL。
		source, err := stringValueArgument(value, "pack", position)
		if err != nil {
			// z 需要字符串参数。
			return err
		}
		if strings.Contains(source, "\x00") {
			// NUL 会提前终止 z 字符串，必须拒绝。
			return runtime.RaiseError(runtime.StringValue("string contains zeros"))
		}
		builder.WriteString(source)
		builder.WriteByte(0)
		return nil
	case 's':
		// sN 写入长度前缀和字符串内容。
		source, err := stringValueArgument(value, "pack", position)
		if err != nil {
			// sN 需要字符串参数。
			return err
		}
		if !unsignedIntegerFits(int64(len(source)), token.size) {
			// 字符串长度无法写入前缀宽度时返回 does not fit。
			return runtime.RaiseError(runtime.StringValue("string length does not fit in given size"))
		}
		writeUnsigned(builder, order, uint64(len(source)), token.size)
		builder.WriteString(source)
		return nil
	default:
		// 未知 token 理论上已被 parser 拒绝，这里防御性报错。
		return runtime.RaiseError(runtime.StringValue("invalid pack token"))
	}
}

// unpackOne 按单个格式 token 从 source 的 offset 位置读取一个值。
//
// 返回值包含 Lua 值和下一 offset；padding token 返回 nil 值但调用方不会追加结果。
func unpackOne(source string, offset int, token packToken, order binary.ByteOrder) (runtime.Value, int, error) {
	// padding 只跳过一个字节。
	if token.kind == packTokenPadding {
		if offset+1 > len(source) {
			// 数据不足时不能跳过填充字节。
			return runtime.NilValue(), offset, runtime.RaiseError(runtime.StringValue("data string too short"))
		}
		return runtime.NilValue(), offset + 1, nil
	}

	switch token.code {
	case 'b', 'h', 'i', 'l', 'j':
		// 有符号整数按指定宽度读取并符号扩展。
		raw, nextOffset, err := readSignedBits(source, offset, token.size, order)
		if err != nil {
			// 数据不足时返回读取错误。
			return runtime.NilValue(), offset, err
		}
		return runtime.IntegerValue(signExtend(raw, token.size)), nextOffset, nil
	case 'B', 'H', 'I', 'L', 'J', 'T':
		// 无符号整数当前返回 Lua integer。
		raw, nextOffset, err := readUnsigned(source, offset, token.size, order)
		if err != nil {
			// 数据不足时返回读取错误。
			return runtime.NilValue(), offset, err
		}
		return runtime.IntegerValue(int64(raw)), nextOffset, nil
	case 'f', 'd', 'n':
		// 浮点格式按 IEEE 位模式读取；n 按 Lua number 的 float64 表示读取。
		raw, nextOffset, err := readUnsigned(source, offset, token.size, order)
		if err != nil {
			// 数据不足时返回读取错误。
			return runtime.NilValue(), offset, err
		}
		if token.size == 4 {
			// 4 字节 float32 转成 Lua number。
			return runtime.NumberValue(float64(math.Float32frombits(uint32(raw)))), nextOffset, nil
		}
		// 8 字节 float64 直接转成 Lua number。
		return runtime.NumberValue(math.Float64frombits(raw)), nextOffset, nil
	case 'c':
		// cN 读取固定长度字节串。
		if offset+token.size > len(source) {
			// 数据不足时不能读取固定字符串。
			return runtime.NilValue(), offset, runtime.RaiseError(runtime.StringValue("data string too short"))
		}
		return runtime.StringValue(source[offset : offset+token.size]), offset + token.size, nil
	case 'z':
		// z 读取到第一个 NUL 之前。
		end := strings.IndexByte(source[offset:], 0)
		if end < 0 {
			// 没有 NUL 终止符时数据不完整。
			return runtime.NilValue(), offset, runtime.RaiseError(runtime.StringValue("unfinished string for format 'z'"))
		}
		return runtime.StringValue(source[offset : offset+end]), offset + end + 1, nil
	case 's':
		// sN 先读取长度前缀，再读取对应字节数。
		size, nextOffset, err := readUnsigned(source, offset, token.size, order)
		if err != nil {
			// 长度前缀不足时返回读取错误。
			return runtime.NilValue(), offset, err
		}
		if size > uint64(math.MaxInt) {
			// 当前宿主无法索引超过 int 范围的字符串长度。
			return runtime.NilValue(), offset, runtime.RaiseError(runtime.StringValue("string length does not fit"))
		}
		if size > uint64(len(source)-nextOffset) {
			// 声明长度超过剩余数据时返回错误。
			return runtime.NilValue(), offset, runtime.RaiseError(runtime.StringValue("data string too short"))
		}
		return runtime.StringValue(source[nextOffset : nextOffset+int(size)]), nextOffset + int(size), nil
	default:
		// 未知 token 理论上已被 parser 拒绝，这里防御性报错。
		return runtime.NilValue(), offset, runtime.RaiseError(runtime.StringValue("invalid unpack token"))
	}
}

// writeUnsigned 按指定端序写入低 size 字节整数。
//
// value 是待写入无符号位模式；size 可为 1..16，超过 8 字节时高位按 0 扩展。
func writeUnsigned(builder *strings.Builder, order binary.ByteOrder, value uint64, size int) {
	var buffer [8]byte
	// 按最大宽度写入缓冲，再根据 size 取对应片段。
	order.PutUint64(buffer[:], value)
	if order == binary.LittleEndian {
		// 小端低位字节在前。
		limit := size
		if limit > 8 {
			// 超过 Lua integer 宽度的高位区域用 0 扩展。
			limit = 8
		}
		builder.Write(buffer[:limit])
		writePadding(builder, size-limit)
		return
	}

	if size > 8 {
		// 大端高位区域先写 0，再写 64 位低位完整内容。
		writePadding(builder, size-8)
		builder.Write(buffer[:])
		return
	}

	// 大端低位宽度位于 8 字节缓冲的尾部。
	builder.Write(buffer[8-size:])
}

// writeSigned 按指定端序写入有符号整数补码。
//
// size 可为 1..16；超过 8 字节时高位根据符号位扩展为 0x00 或 0xff。
func writeSigned(builder *strings.Builder, order binary.ByteOrder, value int64, size int) {
	if size <= 8 {
		// 8 字节以内直接写入低位补码。
		writeUnsigned(builder, order, uint64(value), size)
		return
	}

	signFill := byte(0)
	if value < 0 {
		// 负数需要使用 0xff 扩展高位。
		signFill = 0xff
	}

	var buffer [8]byte
	order.PutUint64(buffer[:], uint64(value))
	if order == binary.LittleEndian {
		// 小端先写低 64 位，再写符号扩展字节。
		builder.Write(buffer[:])
		for index := 0; index < size-8; index++ {
			// 每个额外高位字节都必须等于符号填充值。
			builder.WriteByte(signFill)
		}
		return
	}

	for index := 0; index < size-8; index++ {
		// 大端先写符号扩展高位字节。
		builder.WriteByte(signFill)
	}
	builder.Write(buffer[:])
}

// writeFloat 按指定端序写入 float32 或 float64。
//
// size 为 4 时写 float32 位模式；size 为 8 时写 float64 位模式。
func writeFloat(builder *strings.Builder, order binary.ByteOrder, value float64, size int) {
	if size == 4 {
		// float32 需要先收窄再写入位模式。
		writeUnsigned(builder, order, uint64(math.Float32bits(float32(value))), size)
		return
	}

	// float64 直接写入位模式。
	writeUnsigned(builder, order, math.Float64bits(value), size)
}

// writeFixedString 写入固定长度字符串字段。
//
// source 超过 size 时截断，不足 size 时使用 NUL 补齐。
func writeFixedString(builder *strings.Builder, source string, size int) {
	if len(source) >= size {
		// 字符串过长时只写入前 size 个字节。
		builder.WriteString(source[:size])
		return
	}

	// 先写入原始字符串，再补齐剩余 NUL 字节。
	builder.WriteString(source)
	for index := len(source); index < size; index++ {
		// 每个缺口补一个 NUL 字节。
		builder.WriteByte(0)
	}
}

// readUnsigned 按指定端序读取 size 字节无符号整数。
//
// source 是二进制数据字符串；offset 是 Go 0-based 起点；返回下一 offset。
func readUnsigned(source string, offset int, size int, order binary.ByteOrder) (uint64, int, error) {
	if offset+size > len(source) {
		// 数据不足时返回 Lua error，避免越界读取。
		return 0, offset, runtime.RaiseError(runtime.StringValue("data string too short"))
	}

	if size > 8 {
		// Lua integer 只有 64 位；额外高位必须全部为 0，否则无法表示。
		raw, err := readLow64WithHighCheck(source[offset:offset+size], order, 0)
		if err != nil {
			// 高位非 0 表示无符号值不适配 Lua integer。
			return 0, offset, err
		}
		return raw, offset + size, nil
	}

	var buffer [8]byte
	if order == binary.LittleEndian {
		// 小端数据直接放在缓冲低位区域。
		copy(buffer[:size], source[offset:offset+size])
		return order.Uint64(buffer[:]), offset + size, nil
	}

	// 大端数据需要放在缓冲尾部，让 Uint64 得到低 size 字节位模式。
	copy(buffer[8-size:], source[offset:offset+size])
	return order.Uint64(buffer[:]), offset + size, nil
}

// readSignedBits 按指定端序读取 size 字节有符号补码的低 64 位。
//
// size 超过 8 时，额外高位必须匹配低 64 位符号扩展，否则说明值无法放入 Lua integer。
func readSignedBits(source string, offset int, size int, order binary.ByteOrder) (uint64, int, error) {
	if offset+size > len(source) {
		// 数据不足时返回 Lua error，避免越界读取。
		return 0, offset, runtime.RaiseError(runtime.StringValue("data string too short"))
	}
	if size <= 8 {
		// 8 字节以内复用无符号读取，后续由 signExtend 解释符号。
		return readUnsigned(source, offset, size, order)
	}

	chunk := source[offset : offset+size]
	low64 := extractLow64(chunk, order)
	signFill := byte(0)
	if low64&(uint64(1)<<63) != 0 {
		// 低 64 位最高位为 1 时，额外高位必须全是 0xff。
		signFill = 0xff
	}
	raw, err := readLow64WithHighCheck(chunk, order, signFill)
	if err != nil {
		// 高位不匹配符号扩展时，官方语义是 does not fit。
		return 0, offset, err
	}
	return raw, offset + size, nil
}

// extractLow64 从 1..16 字节补码片段中提取低 64 位。
//
// chunk 长度必须大于 8；order 指明低位字节所在位置。
func extractLow64(chunk string, order binary.ByteOrder) uint64 {
	var buffer [8]byte
	if order == binary.LittleEndian {
		// 小端低 64 位位于片段起始处。
		copy(buffer[:], chunk[:8])
		return binary.LittleEndian.Uint64(buffer[:])
	}

	// 大端低 64 位位于片段末尾。
	copy(buffer[:], chunk[len(chunk)-8:])
	return binary.BigEndian.Uint64(buffer[:])
}

// readLow64WithHighCheck 读取低 64 位并校验额外高位填充值。
//
// highFill 是额外高位期望字节；不匹配时返回 Lua does not fit 错误。
func readLow64WithHighCheck(chunk string, order binary.ByteOrder, highFill byte) (uint64, error) {
	if order == binary.LittleEndian {
		// 小端额外高位位于低 64 位之后。
		for index := 8; index < len(chunk); index++ {
			// 任一高位字节不匹配都表示不能放入 Lua integer。
			if chunk[index] != highFill {
				return 0, packIntegerDoesNotFitError(len(chunk))
			}
		}
		return extractLow64(chunk, order), nil
	}

	for index := 0; index < len(chunk)-8; index++ {
		// 大端额外高位位于低 64 位之前。
		if chunk[index] != highFill {
			return 0, packIntegerDoesNotFitError(len(chunk))
		}
	}
	return extractLow64(chunk, order), nil
}

// packIntegerDoesNotFitError 构造大宽度整数无法放入 Lua integer 的错误。
//
// size 会进入错误文案，既满足官方 `16-byte integer` 检查，也保留 `does not fit` 关键词。
func packIntegerDoesNotFitError(size int) error {
	// Lua 官方测试会针对 16 字节整数匹配该宽度描述。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("%d-byte integer does not fit into Lua Integer", size)))
}

// signExtend 对指定字节宽度的补码整数执行符号扩展。
//
// raw 是未解释符号的位模式；size 可为 1..16，超过 8 字节时 raw 已经是低 64 位。
func signExtend(raw uint64, size int) int64 {
	if size >= 8 {
		// 8 字节已经覆盖 int64 全宽度，直接转换。
		return int64(raw)
	}

	shift := uint((8 - size) * 8)
	// 先左移把符号位推到 int64 最高位，再算术右移恢复宽度。
	return int64(raw<<shift) >> shift
}

// stringValueArgument 从单个 Lua 值中提取 string。
//
// functionName 和 position 用于生成标准库参数错误。
func stringValueArgument(value runtime.Value, functionName string, position int) (string, error) {
	if value.Kind != runtime.KindString {
		// 非 string 类型不做 tostring 隐式转换。
		return "", badArgument(functionName, position, "string expected")
	}

	// 返回原始字节字符串。
	return value.String, nil
}

// validPackOptionSize 判断 pack 整数或对齐控制宽度是否受支持。
//
// Lua 5.3 官方测试以 1..16 为边界，0 和 17 及以上必须报错。
func validPackOptionSize(size int) bool {
	// 只接受官方测试范围内的正字节数。
	return size >= 1 && size <= packMaxOptionSize
}

// signedIntegerFits 判断 Lua integer 是否能放入指定有符号宽度。
//
// size 大于等于 8 时，int64 全量都能通过符号扩展表示。
func signedIntegerFits(value int64, size int) bool {
	if size >= 8 {
		// 8 字节及以上可完整容纳 Lua integer。
		return true
	}
	bits := uint(size * 8)
	minValue := -(int64(1) << (bits - 1))
	maxValue := (int64(1) << (bits - 1)) - 1
	return value >= minValue && value <= maxValue
}

// unsignedIntegerFits 判断 Lua integer 是否能放入指定无符号宽度。
//
// size 大于等于 8 时，Lua 5.3 允许按 64 位位模式写入，包括负数的补码表示。
func unsignedIntegerFits(value int64, size int) bool {
	if size >= 8 {
		// 8 字节及以上可表达任意 Lua integer 的低 64 位。
		return true
	}
	if value < 0 {
		// 小于 8 字节的无符号格式不能接收负数。
		return false
	}
	maxValue := (int64(1) << uint(size*8)) - 1
	return value <= maxValue
}

// patternMatch 表示一次 pattern 匹配结果。
//
// start/end 是源字符串 0-based 半开区间；captures 按左括号出现顺序保存 capture 区间。
type patternMatch struct {
	// start 是完整匹配起始字节偏移。
	start int
	// end 是完整匹配结束字节偏移。
	end int
	// captures 保存所有已完成 capture 的区间。
	captures []patternCapture
}

// patternIterator 保存 gmatch/gsub 逐次查找状态。
//
// source 是被匹配字符串；pattern 是 Lua pattern；offset 是下一次查找起始偏移。
type patternIterator struct {
	// source 保存被匹配的 Lua 字节字符串。
	source string
	// pattern 保存 Lua pattern 字节字符串。
	pattern string
	// offset 保存下一次查找起始偏移。
	offset int
	// finished 表示迭代已经结束。
	finished bool
	// skipEmptyAtOffset 表示上次是非空匹配，本次需要跳过同一 offset 的空匹配。
	skipEmptyAtOffset bool
}

// newPatternIterator 创建 pattern 迭代器。
//
// startOffset 是 Go 0-based 起始偏移。
func newPatternIterator(source string, pattern string, startOffset int) *patternIterator {
	// 初始化迭代器，offset 由调用方按 Lua init 语义换算。
	return &patternIterator{source: source, pattern: pattern, offset: startOffset}
}

// next 返回下一次 pattern 匹配。
//
// 空匹配会把下一次起点至少推进 1 个字节，避免 gmatch/gsub 在同一位置无限循环。
func (iterator *patternIterator) next() (patternMatch, bool, error) {
	if iterator.finished {
		// 已结束迭代器不再继续查找。
		return patternMatch{}, false, nil
	}
	if iterator.offset > len(iterator.source) {
		// 起点超过尾后时结束迭代。
		iterator.finished = true
		return patternMatch{}, false, nil
	}

	var result patternMatch
	var ok bool
	for {
		var err error
		result, ok, err = findPattern(iterator.source, iterator.pattern, iterator.offset)
		if err != nil {
			// pattern 执行错误直接返回。
			return patternMatch{}, false, err
		}
		if !ok {
			// 未找到更多匹配，标记迭代结束。
			iterator.finished = true
			return patternMatch{}, false, nil
		}
		if iterator.skipEmptyAtOffset && result.start == iterator.offset && result.end == result.start {
			// Lua 5.3.3 起，非空匹配结束处不能立刻再接受同位置空匹配。
			iterator.skipEmptyAtOffset = false
			iterator.offset++
			if iterator.offset > len(iterator.source) {
				// 跳过尾后空匹配后没有更多位置。
				iterator.finished = true
				return patternMatch{}, false, nil
			}
			continue
		}
		break
	}

	if result.end > result.start {
		// 非空匹配从匹配末尾继续。
		iterator.offset = result.end
		iterator.skipEmptyAtOffset = true
	} else if result.end < len(iterator.source) {
		// 空匹配至少推进一个字节，避免重复命中同一空位置。
		iterator.offset = result.end + 1
		iterator.skipEmptyAtOffset = false
	} else {
		// 尾部空匹配后没有更多位置可尝试。
		iterator.finished = true
		iterator.skipEmptyAtOffset = false
	}
	return result, true, nil
}

// patternCapture 表示一个 Lua pattern capture 区间。
//
// start/end 是源字符串 0-based 半开区间。
type patternCapture struct {
	// start 是 capture 起始字节偏移。
	start int
	// end 是 capture 结束字节偏移。
	end int
	// position 表示该 capture 是 `()` 位置捕获，返回值应为 1-based 位置而不是子串。
	position bool
	// order 表示 capture 左括号出现顺序，用于 Lua 返回值和 back reference 序号。
	order int
}

// patternOpenCapture 表示尚未闭合的 capture 起点。
type patternOpenCapture struct {
	// start 是 capture 起始字节偏移。
	start int
	// order 是该 capture 左括号出现顺序。
	order int
}

// patternState 保存 pattern 递归匹配过程中的可变状态。
//
// source 是被匹配字符串，pattern 是 Lua pattern 文本。
type patternState struct {
	// source 保存被匹配的 Lua 字节字符串。
	source string
	// pattern 保存 Lua pattern 字节字符串。
	pattern string
}

// findPattern 从指定偏移开始查找 pattern。
//
// startOffset 是 Go 0-based 起点。pattern 以 `^` 开头时只尝试起点；否则从起点到尾部逐位尝试。
func findPattern(source string, pattern string, startOffset int) (patternMatch, bool, error) {
	if len(pattern) > maxPatternBytes {
		// 超长 pattern 会导致当前递归回溯引擎不可控，按 Lua 兼容错误快速拒绝。
		return patternMatch{}, false, runtime.RaiseError(runtime.StringValue("pattern too complex"))
	}
	if result, ok, handled := findAnchoredSimpleTailPattern(source, pattern, startOffset); handled {
		// 简单锚定尾部 pattern 已由线性快路径完整处理。
		return result, ok, nil
	}

	// 创建匹配状态，后续递归共享源字符串和 pattern。
	state := patternState{source: source, pattern: pattern}
	if strings.HasPrefix(pattern, "^") {
		// 锚定 pattern 只允许从 startOffset 开始匹配。
		result, ok, err := state.matchHere(startOffset, 1, nil, nil)
		if err != nil {
			// 递归匹配错误直接上抛。
			return patternMatch{}, false, err
		}
		if !ok {
			// 锚定位置未命中即整体失败。
			return patternMatch{}, false, nil
		}
		result.start = startOffset
		return result, true, nil
	}

	for offset := startOffset; offset <= len(source); offset++ {
		// 非锚定 pattern 逐个字节位置尝试匹配，包括尾后空匹配位置。
		result, ok, err := state.matchHere(offset, 0, nil, nil)
		if err != nil {
			// 任一尝试发现 pattern 错误时直接返回。
			return patternMatch{}, false, err
		}
		if ok {
			// 首个命中位置即为 Lua string.match 返回结果。
			result.start = offset
			return result, true, nil
		}
	}

	// 所有起点都未命中。
	return patternMatch{}, false, nil
}

// findAnchoredSimpleTailPattern 线性匹配官方大字符串用到的简单锚定 pattern。
//
// 当前支持形态为 `^x*.?$`、`^x-.?$`、`^x*.?y$` 和 `^x-.?y$`；这些 pattern 无 capture、
// 无转义和字符集，语言等价于“从 startOffset 到结尾，除最后可选通配字节和可选尾字节外均为 x”。
func findAnchoredSimpleTailPattern(source string, pattern string, startOffset int) (patternMatch, bool, bool) {
	if len(pattern) != 6 && len(pattern) != 7 {
		// 其他长度不属于该快路径负责的 pattern 形态。
		return patternMatch{}, false, false
	}
	if pattern[0] != '^' || (pattern[2] != '*' && pattern[2] != '-') || pattern[3] != '.' || pattern[4] != '?' || pattern[len(pattern)-1] != '$' {
		// 结构不匹配时交回通用 pattern 引擎处理。
		return patternMatch{}, false, false
	}
	repeated := pattern[1]
	if isPatternMagicLiteral(repeated) {
		// 魔法字符需要通用转义语义，不能按普通字面量快路径处理。
		return patternMatch{}, false, false
	}
	if startOffset < 0 || startOffset > len(source) {
		// init 归一化后理论上不会越界，防御异常调用直接按未命中处理。
		return patternMatch{}, false, true
	}

	endBeforeTail := len(source)
	if len(pattern) == 7 {
		// 带尾部字面量时，最后一个源字节必须等于该尾字节。
		tail := pattern[5]
		if isPatternMagicLiteral(tail) {
			// 尾部魔法字符需要通用 pattern 解释。
			return patternMatch{}, false, false
		}
		if startOffset >= len(source) || source[len(source)-1] != tail {
			// 尾字节不匹配时整体不匹配，避免通用引擎在大输入上贪婪回溯。
			return patternMatch{}, false, true
		}
		endBeforeTail = len(source) - 1
	}

	literalEnd := endBeforeTail
	if literalEnd > startOffset {
		// `.?` 可在尾锚前消耗一个任意字节，因此最后一个非尾字节可不等于 repeated。
		literalEnd--
	}
	for index := startOffset; index < literalEnd; index++ {
		if source[index] != repeated {
			// 重复字面量区域出现其他字节时该简单 pattern 不匹配。
			return patternMatch{}, false, true
		}
	}

	// 简单锚定 pattern 命中时总是覆盖 startOffset 到源字符串结尾。
	return patternMatch{start: startOffset, end: len(source)}, true, true
}

// isPatternMagicLiteral 判断字节是否是 Lua pattern 魔法字符。
//
// 快路径只处理普通字面量；魔法字符必须保留给通用 parser 解释，避免改变语义。
func isPatternMagicLiteral(value byte) bool {
	// Lua pattern 魔法字符集合：`^$()%.[]*+-?`。
	return strings.ContainsRune("^$()%.[]*+-?", rune(value))
}

// matchHere 从指定 source/pattern 偏移执行递归匹配。
//
// sourceOffset 是当前源字节偏移；patternOffset 是当前 pattern 字节偏移；captures 保存已完成
// capture，openCaptures 保存尚未闭合 capture 的起始位置。
func (state patternState) matchHere(sourceOffset int, patternOffset int, captures []patternCapture, openCaptures []patternOpenCapture) (patternMatch, bool, error) {
	// pattern 结束时，只有没有未闭合 capture 才算匹配成功。
	if patternOffset >= len(state.pattern) {
		if len(openCaptures) != 0 {
			// capture 未闭合属于 pattern 错误。
			return patternMatch{}, false, runtime.RaiseError(runtime.StringValue("unfinished capture"))
		}
		return patternMatch{end: sourceOffset, captures: captures}, true, nil
	}
	if state.pattern[patternOffset] == '$' && patternOffset == len(state.pattern)-1 {
		// 末尾 $ 只在源字符串也到达尾部时匹配。
		if sourceOffset == len(state.source) {
			// 结束锚命中，不消耗源字节。
			return patternMatch{end: sourceOffset, captures: captures}, true, nil
		}
		return patternMatch{}, false, nil
	}
	if state.pattern[patternOffset] == '(' {
		if patternOffset+1 < len(state.pattern) && state.pattern[patternOffset+1] == ')' {
			// 空 capture 是位置捕获，记录当前源位置的 1-based Lua 索引。
			nextCaptures := append(append([]patternCapture(nil), captures...), patternCapture{start: sourceOffset, end: sourceOffset, position: true, order: len(captures) + len(openCaptures)})
			return state.matchHere(sourceOffset, patternOffset+2, nextCaptures, openCaptures)
		}

		// 左括号开始一个 capture，不消耗源字节。
		openCapture := patternOpenCapture{start: sourceOffset, order: len(captures) + len(openCaptures)}
		return state.matchHere(sourceOffset, patternOffset+1, captures, append(openCaptures, openCapture))
	}
	if state.pattern[patternOffset] == ')' {
		// 右括号闭合最近一个 capture。
		if len(openCaptures) == 0 {
			// 没有对应左括号时 pattern 非法。
			return patternMatch{}, false, runtime.RaiseError(runtime.StringValue("invalid pattern capture"))
		}
		openCapture := openCaptures[len(openCaptures)-1]
		nextOpen := append([]patternOpenCapture(nil), openCaptures[:len(openCaptures)-1]...)
		nextCaptures := append(append([]patternCapture(nil), captures...), patternCapture{start: openCapture.start, end: sourceOffset, order: openCapture.order})
		return state.matchHere(sourceOffset, patternOffset+1, nextCaptures, nextOpen)
	}

	token, nextPatternOffset, err := state.parsePatternToken(patternOffset)
	if err != nil {
		// token 解析错误直接返回。
		return patternMatch{}, false, err
	}
	quantifier := byte(0)
	if nextPatternOffset < len(state.pattern) && isPatternQuantifier(state.pattern[nextPatternOffset]) {
		// 量词修饰前一个 token。
		quantifier = state.pattern[nextPatternOffset]
		nextPatternOffset++
	}
	return state.matchQuantified(token, quantifier, sourceOffset, nextPatternOffset, captures, openCaptures)
}

// patternToken 表示一个已解析的 Lua pattern 原子。
//
// kind 决定匹配规则；literal、class、set、open/close 等字段按 kind 使用。
type patternToken struct {
	// kind 是 token 类型。
	kind string
	// literal 保存 literal token 的字节。
	literal byte
	// class 保存 `%a`、`%d` 等字符类代码。
	class byte
	// set 保存 `[]` 字符集内容，不包含外层中括号。
	set string
	// balancedOpen 保存 `%bxy` 的起始字节。
	balancedOpen byte
	// balancedClose 保存 `%bxy` 的结束字节。
	balancedClose byte
	// frontierSet 保存 `%f[]` 的字符集内容。
	frontierSet string
	// captureIndex 保存 `%1..%9` 引用的 0-based capture 序号。
	captureIndex int
}

// parsePatternToken 从指定 pattern 偏移解析一个 pattern 原子。
//
// 返回 nextOffset 指向 token 后的第一个 pattern 字节。
func (state patternState) parsePatternToken(offset int) (patternToken, int, error) {
	if offset >= len(state.pattern) {
		// 调用方不应在 pattern 尾后解析 token。
		return patternToken{}, offset, runtime.RaiseError(runtime.StringValue("malformed pattern"))
	}

	current := state.pattern[offset]
	if current == '.' {
		// . 匹配任意单字节。
		return patternToken{kind: "any"}, offset + 1, nil
	}
	if current == '[' {
		// 方括号字符集读取到闭合 ]；开头的 ] 或 ^] 按 Lua 规则属于集合内容。
		setEnd := findPatternSetClose(state.pattern, offset)
		if setEnd < 0 {
			// 字符集没有闭合时 pattern 非法。
			return patternToken{}, offset, runtime.RaiseError(runtime.StringValue("malformed pattern set"))
		}
		return patternToken{kind: "set", set: state.pattern[offset+1 : setEnd]}, setEnd + 1, nil
	}
	if current == '%' {
		// % 引入转义字符类或特殊 pattern。
		if offset+1 >= len(state.pattern) {
			// 尾部单独 % 非法。
			return patternToken{}, offset, runtime.RaiseError(runtime.StringValue("malformed pattern escape"))
		}
		escaped := state.pattern[offset+1]
		if escaped == 'b' {
			// %bxy 需要两个额外分隔字符。
			if offset+3 >= len(state.pattern) {
				// 缺少 x 或 y 时 pattern 非法。
				return patternToken{}, offset, runtime.RaiseError(runtime.StringValue("malformed balanced pattern"))
			}
			return patternToken{kind: "balanced", balancedOpen: state.pattern[offset+2], balancedClose: state.pattern[offset+3]}, offset + 4, nil
		}
		if escaped == 'f' {
			// %f[] 需要紧跟字符集。
			if offset+2 >= len(state.pattern) || state.pattern[offset+2] != '[' {
				// frontier 后没有字符集时 pattern 非法。
				return patternToken{}, offset, runtime.RaiseError(runtime.StringValue("missing '[' after '%f' in pattern"))
			}
			end := strings.IndexByte(state.pattern[offset+3:], ']')
			if end < 0 {
				// frontier 字符集没有闭合时 pattern 非法。
				return patternToken{}, offset, runtime.RaiseError(runtime.StringValue("malformed frontier pattern"))
			}
			setEnd := offset + 3 + end
			return patternToken{kind: "frontier", frontierSet: state.pattern[offset+3 : setEnd]}, setEnd + 1, nil
		}
		if escaped == '0' {
			// Lua pattern 的 back reference 只允许 `%1` 到 `%9`。
			return patternToken{}, offset, invalidCaptureIndexError(escaped)
		}
		if escaped >= '1' && escaped <= '9' {
			// %1..%9 是 back reference，匹配对应 capture 的文本。
			return patternToken{kind: "capture", captureIndex: int(escaped - '1')}, offset + 2, nil
		}
		if isLuaPatternClass(escaped) {
			// 常用 Lua 字符类由 matchClass 处理。
			return patternToken{kind: "class", class: escaped}, offset + 2, nil
		}
		// 其他转义表示匹配该字面字符。
		return patternToken{kind: "literal", literal: escaped}, offset + 2, nil
	}

	// 普通字符按字面匹配。
	return patternToken{kind: "literal", literal: current}, offset + 1, nil
}

// matchQuantified 执行带可选量词的 token 匹配。
//
// quantifier 为 0 表示单次匹配；`*`、`+` 是贪婪，`-` 是非贪婪，`?` 是可选。
func (state patternState) matchQuantified(token patternToken, quantifier byte, sourceOffset int, nextPatternOffset int, captures []patternCapture, openCaptures []patternOpenCapture) (patternMatch, bool, error) {
	if token.kind == "frontier" {
		// frontier 是零宽断言，不接受量词。
		if state.matchFrontier(token, sourceOffset) {
			// frontier 命中后继续匹配后续 pattern。
			return state.matchHere(sourceOffset, nextPatternOffset, captures, openCaptures)
		}
		return patternMatch{}, false, nil
	}
	if token.kind == "balanced" {
		// balanced match 是变长原子，本阶段不支持再叠加量词。
		nextOffset, ok := state.matchBalanced(token, sourceOffset)
		if !ok {
			// 起点不满足 balanced 结构。
			return patternMatch{}, false, nil
		}
		return state.matchHere(nextOffset, nextPatternOffset, captures, openCaptures)
	}
	if token.kind == "capture" {
		// %n back reference 匹配已捕获文本本身。
		nextOffset, ok, err := state.matchCaptureReference(token.captureIndex, sourceOffset, captures)
		if err != nil || !ok {
			// capture 引用非法或当前位置文本不相等时失败。
			return patternMatch{}, false, err
		}
		return state.matchHere(nextOffset, nextPatternOffset, captures, openCaptures)
	}

	maxOffsets := []int{sourceOffset}
	currentOffset := sourceOffset
	for {
		// 逐次消费 token，记录每个可回溯位置。
		nextOffset, ok := state.matchSingle(token, currentOffset)
		if !ok {
			// 当前 token 无法继续消费。
			break
		}
		if nextOffset == currentOffset {
			// 防御零宽 token 导致死循环。
			break
		}
		maxOffsets = append(maxOffsets, nextOffset)
		currentOffset = nextOffset
	}

	switch quantifier {
	case 0:
		// 无量词时必须恰好匹配一次。
		if len(maxOffsets) < 2 {
			// 一次都未命中则失败。
			return patternMatch{}, false, nil
		}
		return state.matchHere(maxOffsets[1], nextPatternOffset, captures, openCaptures)
	case '?':
		// ? 优先使用一次匹配，再回退到零次。
		for index := minInt(1, len(maxOffsets)-1); index >= 0; index-- {
			result, ok, err := state.matchHere(maxOffsets[index], nextPatternOffset, captures, openCaptures)
			if err != nil || ok {
				// 后续匹配成功或出错时立即返回。
				return result, ok, err
			}
		}
	case '+':
		// + 至少匹配一次，并从最长结果回溯。
		if len(maxOffsets) < 2 {
			// 一次都未命中则失败。
			return patternMatch{}, false, nil
		}
		for index := len(maxOffsets) - 1; index >= 1; index-- {
			result, ok, err := state.matchHere(maxOffsets[index], nextPatternOffset, captures, openCaptures)
			if err != nil || ok {
				// 后续匹配成功或出错时立即返回。
				return result, ok, err
			}
		}
	case '*':
		// * 允许零次，并从最长结果回溯。
		for index := len(maxOffsets) - 1; index >= 0; index-- {
			result, ok, err := state.matchHere(maxOffsets[index], nextPatternOffset, captures, openCaptures)
			if err != nil || ok {
				// 后续匹配成功或出错时立即返回。
				return result, ok, err
			}
		}
	case '-':
		// - 允许零次，并从最短结果开始尝试。
		for index := 0; index < len(maxOffsets); index++ {
			result, ok, err := state.matchHere(maxOffsets[index], nextPatternOffset, captures, openCaptures)
			if err != nil || ok {
				// 后续匹配成功或出错时立即返回。
				return result, ok, err
			}
		}
	}

	// 所有回溯路径都失败。
	return patternMatch{}, false, nil
}

// findPatternSetClose 查找 Lua pattern 字符集的闭合 `]`。
//
// openOffset 指向 `[`；若集合以 `]` 或 `^]` 开头，该 `]` 是集合成员而不是闭合符。
func findPatternSetClose(pattern string, openOffset int) int {
	index := openOffset + 1
	if index < len(pattern) && pattern[index] == '^' {
		// 取反标记不属于闭合判断范围，后续第一个 ] 仍可作为集合成员。
		index++
	}
	if index < len(pattern) && pattern[index] == ']' {
		// Lua 允许 ] 作为字符集第一个普通字符。
		index++
	}
	for index < len(pattern) {
		if pattern[index] == '%' && index+1 < len(pattern) {
			// 字符集内的转义字符不能作为闭合符，例如 `%]` 表示字面右括号。
			index += 2
			continue
		}
		if pattern[index] == ']' {
			// 找到真正闭合字符集的右括号。
			return index
		}
		index++
	}

	// 没有闭合右括号。
	return -1
}

// matchCaptureReference 匹配 `%n` back reference。
//
// captureIndex 是 0-based capture 序号；位置捕获不能作为文本引用，引用不存在时返回 Lua error。
func (state patternState) matchCaptureReference(captureIndex int, sourceOffset int, captures []patternCapture) (int, bool, error) {
	ordered := orderedCaptures(captures)
	if captureIndex < 0 || captureIndex >= len(ordered) {
		// Lua 对不存在的 capture 引用报告 invalid capture index。
		return sourceOffset, false, invalidCaptureIndexError(byte(captureIndex) + '1')
	}
	capture := ordered[captureIndex]
	if capture.position {
		// 位置捕获没有可回放文本，不能作为 back reference。
		return sourceOffset, false, invalidCaptureIndexError(byte(captureIndex) + '1')
	}
	text := state.source[capture.start:capture.end]
	if strings.HasPrefix(state.source[sourceOffset:], text) {
		// 当前位置文本等于 capture 内容，消费对应字节数。
		return sourceOffset + len(text), true, nil
	}

	// 文本不相等时当前匹配路径失败。
	return sourceOffset, false, nil
}

// matchSingle 尝试让一个单字节 token 消费 source 的当前位置。
//
// 返回 nextOffset 表示消费后的源偏移。
func (state patternState) matchSingle(token patternToken, sourceOffset int) (int, bool) {
	if sourceOffset >= len(state.source) {
		// 到达源字符串末尾时无法消费单字节 token。
		return sourceOffset, false
	}

	value := state.source[sourceOffset]
	switch token.kind {
	case "any":
		// . 消费任意一个字节。
		return sourceOffset + 1, true
	case "literal":
		// literal 必须字节相等。
		return sourceOffset + 1, value == token.literal
	case "class":
		// 字符类按 Lua 字节类判断。
		return sourceOffset + 1, matchLuaClass(value, token.class)
	case "set":
		// 字符集按 [] 内部规则判断。
		return sourceOffset + 1, matchPatternSet(value, token.set)
	default:
		// 其他 token 不能作为单字节 token 使用。
		return sourceOffset, false
	}
}

// matchBalanced 尝试匹配 `%bxy` balanced pattern。
//
// token 必须是 balanced；sourceOffset 必须位于起始分隔符处。
func (state patternState) matchBalanced(token patternToken, sourceOffset int) (int, bool) {
	if sourceOffset >= len(state.source) || state.source[sourceOffset] != token.balancedOpen {
		// balanced 必须从起始分隔符开始。
		return sourceOffset, false
	}
	if token.balancedOpen == token.balancedClose {
		// 同字符开闭分隔符不能用嵌套深度抵消，需匹配下一次出现的同一字节。
		for index := sourceOffset + 1; index < len(state.source); index++ {
			if state.source[index] == token.balancedClose {
				// 找到闭合分隔符，返回闭合符后一位。
				return index + 1, true
			}
		}
		// 没有第二个分隔符时匹配失败。
		return sourceOffset, false
	}

	depth := 0
	for index := sourceOffset; index < len(state.source); index++ {
		if state.source[index] == token.balancedOpen {
			// 遇到起始分隔符时嵌套深度增加。
			depth++
		}
		if state.source[index] == token.balancedClose {
			// 遇到结束分隔符时嵌套深度减少。
			depth--
			if depth == 0 {
				// 回到 0 表示 balanced 区间闭合，返回结束后一位。
				return index + 1, true
			}
		}
	}

	// 扫描到末尾仍未闭合则匹配失败。
	return sourceOffset, false
}

// matchFrontier 判断 `%f[set]` 零宽 frontier 是否成立。
//
// frontier 要求前一个字节不在 set 中，当前位置字节在 set 中；字符串边界按 NUL 外部字节处理。
func (state patternState) matchFrontier(token patternToken, sourceOffset int) bool {
	previousByte := byte(0)
	if sourceOffset > 0 {
		// 起点前有字节时使用真实前一字节；字符串开头外侧按 Lua 规则视为 NUL。
		previousByte = state.source[sourceOffset-1]
	}
	previousInSet := matchPatternSet(previousByte, token.frontierSet)
	currentByte := byte(0)
	if sourceOffset < len(state.source) {
		// 当前位置有字节时使用真实当前字节；字符串末尾外侧按 Lua 规则视为 NUL。
		currentByte = state.source[sourceOffset]
	}
	currentInSet := matchPatternSet(currentByte, token.frontierSet)

	// Lua frontier 在“不在集合 -> 在集合”的边界成立。
	return !previousInSet && currentInSet
}

// matchPatternSet 判断字节是否属于 `[]` 字符集。
//
// set 不包含外层中括号；首字符 `^` 表示取反；支持范围 `a-z` 和 `%` 字符类。
func matchPatternSet(value byte, set string) bool {
	negated := false
	if strings.HasPrefix(set, "^") {
		// ^ 开头表示字符集取反。
		negated = true
		set = set[1:]
	}

	matched := false
	for index := 0; index < len(set); index++ {
		current := set[index]
		if current == '%' && index+1 < len(set) {
			index++
			escaped := set[index]
			if isLuaPatternClass(escaped) {
				// 字符集内的 Lua 字符类。
				if matchLuaClass(value, escaped) {
					// 任一类匹配即可。
					matched = true
				}
				continue
			}
			if value == escaped {
				// 字符集内非字符类转义按字面字符匹配，例如 `%]`、`%-`。
				matched = true
			}
			continue
		}
		if index+2 < len(set) && set[index+1] == '-' {
			// a-z 范围匹配。
			end := set[index+2]
			if value >= current && value <= end {
				// 字节落在闭区间内。
				matched = true
			}
			index += 2
			continue
		}
		if value == current {
			// 普通字面字符匹配。
			matched = true
		}
	}

	if negated {
		// 取反集合返回相反结果。
		return !matched
	}
	return matched
}

// matchLuaClass 判断字节是否匹配 Lua `%x` 字符类。
//
// 支持常用类：a/c/d/g/l/p/s/u/w/x/z 及其大写取反形式。
func matchLuaClass(value byte, class byte) bool {
	// 大写字符类表示对应小写类取反。
	if class >= 'A' && class <= 'Z' {
		// 转为小写后递归判断，再取反。
		return !matchLuaClass(value, class+('a'-'A'))
	}

	switch class {
	case 'a':
		// alphabetic ASCII 字母。
		return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
	case 'c':
		// control 字符。
		return value < 32 || value == 127
	case 'd':
		// 十进制数字。
		return value >= '0' && value <= '9'
	case 'g':
		// graphic 字符，排除空白控制。
		return value > 32 && value < 127
	case 'l':
		// 小写 ASCII 字母。
		return value >= 'a' && value <= 'z'
	case 'p':
		// ASCII 标点。
		return strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(value))
	case 's':
		// 常见空白字符。
		return value == ' ' || value == '\f' || value == '\n' || value == '\r' || value == '\t' || value == '\v'
	case 'u':
		// 大写 ASCII 字母。
		return value >= 'A' && value <= 'Z'
	case 'w':
		// alphanumeric ASCII。
		return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9')
	case 'x':
		// 十六进制数字。
		return (value >= '0' && value <= '9') || (value >= 'A' && value <= 'F') || (value >= 'a' && value <= 'f')
	case 'z':
		// NUL 字节。
		return value == 0
	default:
		// 非字符类按字面字符匹配。
		return value == class
	}
}

// isLuaPatternClass 判断 `%` 后的字节是否属于当前支持的字符类。
//
// 大小写类都允许；特殊 pattern `%b`、`%f` 由 parsePatternToken 单独处理。
func isLuaPatternClass(value byte) bool {
	// 判断是否是 Lua 常用字符类代码。
	return strings.ContainsRune("aAcCdDgGlLpPsSuUwWxXzZ", rune(value))
}

// isPatternQuantifier 判断字节是否为 Lua pattern 量词。
//
// 支持 `*`、`+`、`-` 和 `?`。
func isPatternQuantifier(value byte) bool {
	// 这些字符只在 token 后作为量词处理。
	return value == '*' || value == '+' || value == '-' || value == '?'
}

// minInt 返回两个 int 中较小的一个。
//
// 该 helper 用于可选量词回溯起点计算。
func minInt(left int, right int) int {
	if left < right {
		// left 更小时返回 left。
		return left
	}
	// right 小于等于 left 时返回 right。
	return right
}

// patternValues 把 patternMatch 转换为 Lua 返回值。
//
// 无 capture 时返回完整匹配文本；有 capture 时按 capture 顺序返回。
func patternValues(source string, matchResult patternMatch) []runtime.Value {
	if len(matchResult.captures) == 0 {
		// 无 capture 返回完整匹配。
		return []runtime.Value{runtime.StringValue(source[matchResult.start:matchResult.end])}
	}

	results := make([]runtime.Value, 0, len(matchResult.captures))
	for _, capture := range orderedCaptures(matchResult.captures) {
		// capture 转换为 Lua 返回值。
		results = append(results, captureValue(source, capture))
	}
	return results
}

// captureValue 把一次 capture 转换成 Lua 可见返回值。
//
// 普通 capture 返回字符串切片；`()` 位置捕获返回 1-based 字节位置。
func captureValue(source string, capture patternCapture) runtime.Value {
	if capture.position {
		// 位置捕获返回 Lua 1-based 字节位置。
		return runtime.IntegerValue(int64(capture.start + 1))
	}

	// 普通捕获返回源字符串对应区间。
	return runtime.StringValue(source[capture.start:capture.end])
}

// orderedCaptures 返回按 Lua 左括号出现顺序排列的 capture 副本。
//
// 递归匹配会按闭合顺序生成 capture；Lua 返回值和 `%n` 引用必须按打开顺序解释。
func orderedCaptures(captures []patternCapture) []patternCapture {
	ordered := append([]patternCapture(nil), captures...)
	sort.SliceStable(ordered, func(leftIndex int, rightIndex int) bool {
		// order 越小表示左括号越早出现。
		return ordered[leftIndex].order < ordered[rightIndex].order
	})
	return ordered
}

// replacementForMatch 计算一次 gsub 替换文本。
//
// replacement 支持 string、table 或 Go closure；source 和 matchResult 用于展开 `%0` 与 capture。
func replacementForMatch(state *runtime.State, replacement runtime.Value, source string, matchResult patternMatch) (string, error) {
	switch replacement.Kind {
	case runtime.KindString:
		// 字符串替换模板需要展开 `%` 转义。
		return expandReplacementString(replacement.String, source, matchResult)
	case runtime.KindTable:
		// table 替换按第一 capture 或完整匹配作为 key。
		tableValue, ok := replacement.Ref.(*runtime.Table)
		if !ok || tableValue == nil {
			// table 引用负载损坏时返回参数错误。
			return "", badArgument("gsub", 3, "table expected")
		}
		key := replacementKey(source, matchResult)
		value, err := tableReplacementValue(state, tableValue, key)
		if err != nil {
			// table 普通读取或 `__index` 元方法错误需要中止 gsub。
			return "", err
		}
		if value.IsNil() {
			// table 未命中时保留原始匹配文本。
			return source[matchResult.start:matchResult.end], nil
		}
		if value.Kind == runtime.KindBoolean && !value.Bool {
			// table replacement 中 false 与 nil 一样表示不替换当前匹配。
			return source[matchResult.start:matchResult.end], nil
		}
		if value.Kind != runtime.KindString {
			// 当前阶段 table repl 只接受 string 结果。
			return "", invalidReplacementValue(value)
		}
		return value.String, nil
	case runtime.KindGoClosure:
		// Go closure 替换按 Lua 规则接收所有 capture；没有 capture 时接收完整匹配。
		results, err := callGSubGoReplacement(replacement, patternValues(source, matchResult))
		if err != nil {
			// 替换函数错误需要中止 gsub 并向上传播。
			return "", err
		}
		return gsubReplacementResult(results, source, matchResult)
	case runtime.KindLuaClosure:
		// Lua closure 替换需要 State runner；只有通过 Open 注册到 State 的 gsub 才具备该能力。
		if state == nil {
			// 纯 Go GSub 入口没有 Lua 调度器，按参数类型错误处理。
			return "", badArgument("gsub", 3, "string, function or table expected")
		}
		exitBoundary := state.EnterGoCallbackBoundary()
		defer exitBoundary()
		results, err := state.CallLuaClosure(replacement, patternValues(source, matchResult)...)
		if err != nil {
			// Lua 替换函数错误需要中止 gsub 并向上传播。
			return "", err
		}
		return gsubReplacementResult(results, source, matchResult)
	default:
		// 其他替换类型需要完整 Lua callable 后支持。
		return "", badArgument("gsub", 3, "string, function or table expected")
	}
}

// tableReplacementValue 按 Lua 普通 table 读取语义取得 gsub table replacement 值。
//
// state 非 nil 时允许 `__index` 为 Lua closure；state 为 nil 时仍支持 raw/table/Go closure `__index`。
func tableReplacementValue(state *runtime.State, tableValue *runtime.Table, key runtime.Value) (runtime.Value, error) {
	if state == nil {
		// 纯 Go GSub 入口没有 Lua runner，只能使用 Table.Get 的既有 Go 元方法能力。
		return tableValue.Get(key)
	}

	// 带 State 的 gsub 可执行 Lua closure 型 `__index`，对齐官方 string.gsub table replacement。
	return tableValue.GetWithRunner(key, func(method runtime.Value, _ string, args ...runtime.Value) ([]runtime.Value, error) {
		exitBoundary := state.EnterGoCallbackBoundary()
		defer exitBoundary()
		return state.CallLuaClosure(method, args...)
	})
}

// callGSubGoReplacement 调用 gsub 的 Go closure 替换函数。
//
// replacement 必须是 KindGoClosure；arguments 已按 Lua pattern capture 规则排列。
// GoResultsFunction 保留多返回值，GoFunction 和一元函数被适配成单返回值。
func callGSubGoReplacement(replacement runtime.Value, arguments []runtime.Value) ([]runtime.Value, error) {
	// 只有 Go closure 可在 string 库无 State 的上下文中直接执行。
	if replacement.Kind != runtime.KindGoClosure {
		// 调用方传入非 Go closure 表示内部类型分派错误。
		return nil, runtime.ErrExpectedCallable
	}

	switch function := replacement.Ref.(type) {
	case runtime.GoResultsFunction:
		// GoResultsFunction 直接按多返回值约定调用。
		if function == nil {
			// nil 函数负载不可执行。
			return nil, runtime.ErrExpectedCallable
		}
		return function(arguments...)
	case runtime.GoFunction:
		// GoFunction 只有单返回值，需要适配为结果列表。
		if function == nil {
			// nil 函数负载不可执行。
			return nil, runtime.ErrExpectedCallable
		}
		result, err := function(arguments...)
		if err != nil {
			// 替换函数错误直接返回给 gsub。
			return nil, err
		}
		return []runtime.Value{result}, nil
	case runtime.GoUnaryFunction:
		// 一元函数只消费首个 capture；Lua 调用允许替换函数忽略多余参数。
		if function == nil {
			// nil 函数负载不可执行。
			return nil, runtime.ErrExpectedCallable
		}
		result, err := function(firstGSubReplacementArgument(arguments))
		if err != nil {
			// 替换函数错误直接返回给 gsub。
			return nil, err
		}
		return []runtime.Value{result}, nil
	case *runtime.GoFastUnaryFunction:
		// 标准库一元 fastcall 描述在 gsub 替换函数位置仍按普通一元函数执行。
		if function == nil || function.Function == nil {
			// 损坏注册不能进入 nil 函数调用。
			return nil, runtime.ErrExpectedCallable
		}
		result, err := function.Function(firstGSubReplacementArgument(arguments))
		if err != nil {
			// 替换函数错误直接返回给 gsub。
			return nil, err
		}
		return []runtime.Value{result}, nil
	case *runtime.GoFixedResultsFunction:
		// 固定结果函数复用声明的结果上限；未命中时回退完整函数，保持变长语义。
		if function == nil || function.Function == nil {
			// 损坏注册不能进入 nil 函数调用。
			return nil, runtime.ErrExpectedCallable
		}
		results := make([]runtime.Value, function.MaxResults)
		resultCount, handled, err := function.Function(results, arguments...)
		if err != nil {
			// 替换函数错误直接返回给 gsub。
			return nil, err
		}
		if handled {
			// 命中固定结果快路径时只返回实际写入的前缀。
			return results[:resultCount], nil
		}
		if function.Fallback == nil {
			// 没有回退函数时表示注册不完整。
			return nil, runtime.ErrExpectedCallable
		}
		return function.Fallback(arguments...)
	case *runtime.GoClosureWithUpvalues:
		// 带 debug upvalue 元数据的 Go closure 仍通过内部 Function 执行。
		if function == nil || function.Function == nil {
			// 损坏注册不能进入 nil 函数调用。
			return nil, runtime.ErrExpectedCallable
		}
		return function.Function(arguments...)
	default:
		// Go closure 引用负载损坏时按不可调用处理。
		return nil, runtime.ErrExpectedCallable
	}
}

// firstGSubReplacementArgument 返回 gsub 替换函数的一元调用实参。
//
// arguments 已经由 patternValues 按 Lua 规则构造；空结果只可能来自内部异常路径，此时用 nil
// 进入一元函数，让其按自身参数错误语义返回。
func firstGSubReplacementArgument(arguments []runtime.Value) runtime.Value {
	// 没有 capture 或完整匹配值时使用 nil 触发一元函数自己的参数检查。
	if len(arguments) == 0 {
		// nil 实参保持 Lua 缺参调用的错误边界。
		return runtime.NilValue()
	}
	return arguments[0]
}

// gsubReplacementResult 把替换函数返回值转换为最终替换文本。
//
// 空返回、nil 和 false 表示保留原始匹配；string、integer、number 会转换为替换文本；
// 其他类型按 Lua 5.3 返回无效替换值错误。
func gsubReplacementResult(results []runtime.Value, source string, matchResult patternMatch) (string, error) {
	// 没有返回值时等价 nil，保留原始匹配文本。
	if len(results) == 0 || results[0].IsNil() {
		// Lua 5.3 规定 nil 替换结果不替换当前匹配。
		return source[matchResult.start:matchResult.end], nil
	}

	result := results[0]
	switch result.Kind {
	case runtime.KindBoolean:
		// false 表示保留原始匹配；true 不是合法替换文本。
		if !result.Bool {
			// false 与 nil 一样表示不替换。
			return source[matchResult.start:matchResult.end], nil
		}
		return "", invalidReplacementValue(result)
	case runtime.KindString:
		// string 结果直接作为替换文本。
		return result.String, nil
	case runtime.KindInteger:
		// integer 结果按 Lua tostring 的十进制形式作为替换文本。
		return strconv.FormatInt(result.Integer, 10), nil
	case runtime.KindNumber:
		// number 结果按 Go 的紧凑浮点格式作为阶段性 tostring 结果。
		return strconv.FormatFloat(result.Number, 'g', -1, 64), nil
	default:
		// table、function、thread 等不能作为替换文本。
		return "", invalidReplacementValue(result)
	}
}

// invalidReplacementValue 构造 Lua 5.3 兼容的 gsub 非法替换值错误。
//
// value 是替换表或替换函数产出的非法 Lua 值，错误文本需要包含 Lua 类型名供官方测试匹配。
func invalidReplacementValue(value runtime.Value) error {
	// 错误文本对齐 Lua 5.3 lstrlib.c 的 `invalid replacement value (a %s)` 形式。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid replacement value (a %s)", replacementTypeName(value))))
}

// replacementTypeName 返回 gsub replacement 错误消息中的 Lua 类型名。
//
// 该 helper 只服务错误文本，不触发 `__name` 或 `__tostring` 元方法。
func replacementTypeName(value runtime.Value) string {
	switch value.Kind {
	case runtime.KindNil:
		// nil 类型名固定为 nil。
		return "nil"
	case runtime.KindBoolean:
		// boolean 类型名固定为 boolean。
		return "boolean"
	case runtime.KindInteger, runtime.KindNumber:
		// Lua 整数和浮点在类型名中都属于 number。
		return "number"
	case runtime.KindString:
		// string 类型名固定为 string。
		return "string"
	case runtime.KindTable:
		// table 类型名固定为 table。
		return "table"
	case runtime.KindLuaClosure, runtime.KindGoClosure:
		// Lua 和 Go closure 对 Lua 侧都表现为 function。
		return "function"
	case runtime.KindUserdata:
		// userdata 类型名固定为 userdata。
		return "userdata"
	case runtime.KindThread:
		// thread 类型名固定为 thread。
		return "thread"
	default:
		// 未知内部类型按 value 兜底，便于暴露异常状态。
		return "value"
	}
}

// replacementKey 返回 table gsub 使用的查找 key。
//
// 有 capture 时使用第一 capture；否则使用完整匹配。
func replacementKey(source string, matchResult patternMatch) runtime.Value {
	if len(matchResult.captures) > 0 {
		// 第一 capture 优先作为 table key。
		capture := orderedCaptures(matchResult.captures)[0]
		if capture.position {
			// 位置捕获作为替换 key 时保留 Lua integer 语义。
			return runtime.IntegerValue(int64(capture.start + 1))
		}
		return runtime.StringValue(source[capture.start:capture.end])
	}

	// 无 capture 时使用完整匹配文本。
	return runtime.StringValue(source[matchResult.start:matchResult.end])
}

// expandReplacementString 展开 gsub 字符串替换模板。
//
// `%0` 表示完整匹配，`%1..%9` 表示 capture，`%%` 表示字面百分号。
func expandReplacementString(template string, source string, matchResult patternMatch) (string, error) {
	var builder strings.Builder
	for index := 0; index < len(template); index++ {
		// 普通字符直接写入。
		if template[index] != '%' {
			builder.WriteByte(template[index])
			continue
		}
		if index+1 >= len(template) {
			// 尾部单独 % 非法。
			return "", runtime.RaiseError(runtime.StringValue("invalid use of '%' in replacement string"))
		}
		index++
		next := template[index]
		switch {
		case next == '%':
			// %% 展开为字面百分号。
			builder.WriteByte('%')
		case next == '0':
			// %0 展开为完整匹配。
			builder.WriteString(source[matchResult.start:matchResult.end])
		case next >= '1' && next <= '9':
			// %1..%9 展开为对应 capture。
			captureIndex := int(next - '1')
			ordered := orderedCaptures(matchResult.captures)
			if len(ordered) == 0 && captureIndex == 0 {
				// Lua gsub 在 pattern 没有 capture 时，把完整匹配视为第一个 capture。
				builder.WriteString(source[matchResult.start:matchResult.end])
				continue
			}
			if captureIndex >= len(ordered) {
				// 引用不存在的 capture 是替换模板错误。
				return "", invalidCaptureIndexError(next)
			}
			capture := ordered[captureIndex]
			if capture.position {
				// 位置捕获在替换模板中展开为 1-based 十进制位置。
				builder.WriteString(strconv.Itoa(capture.start + 1))
				continue
			}
			builder.WriteString(source[capture.start:capture.end])
		default:
			// 其他 %x 当前按 Lua 语义视为非法替换转义。
			return "", runtime.RaiseError(runtime.StringValue("invalid use of '%' in replacement string"))
		}
	}

	// 返回完整替换文本。
	return builder.String(), nil
}

// invalidCaptureIndexError 构造 Lua 5.3 兼容的 capture 序号错误。
//
// digit 是 pattern 或 replacement 中 `%` 后面的数字字符，错误文本需要保留 `%n`。
func invalidCaptureIndexError(digit byte) error {
	// 官方测试会匹配具体 capture 序号，不能只返回通用错误。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid capture index %%%c", digit)))
}

// stringArgument 按 Lua 标准库参数规则提取 string。
//
// args 使用 0-based Go 切片；position 使用 Lua 1-based 参数序号。返回错误时会携带 Lua
// error object，便于 pcall/xpcall 获取标准库参数错误。
func stringArgument(args []runtime.Value, position int, functionName string) (string, error) {
	// 先检查参数是否存在。
	if len(args) < position {
		// 缺失参数按 nil 处理，并报告 string expected。
		return "", badArgument(functionName, position, "string expected")
	}
	if args[position-1].Kind != runtime.KindString {
		// 非 string 类型不做 tostring 隐式转换。
		return "", badArgument(functionName, position, "string expected")
	}

	// 返回底层字节字符串。
	return args[position-1].String, nil
}

// integerArgument 按 Lua 标准库参数规则提取 integer。
//
// args 使用 0-based Go 切片；position 使用 Lua 1-based 参数序号。integer 与可无损转换的
// float number 都会被接受，其他类型返回 Lua 参数错误。
func integerArgument(args []runtime.Value, position int, functionName string) (int64, error) {
	// 先检查参数是否存在。
	if len(args) < position {
		// 缺失整数参数无法提供默认值，由调用方决定哪些参数可选。
		return 0, badArgument(functionName, position, "integer expected")
	}

	integerValue, ok := args[position-1].ToInteger()
	if !ok {
		if args[position-1].Kind == runtime.KindNumber {
			// float number 但无法无损表示为 Lua integer 时，官方错误文本强调整数表示失败。
			return 0, badArgument(functionName, position, "number has no integer representation")
		}
		// 非整数值不能作为 string 标准库的索引或次数。
		return 0, badArgument(functionName, position, "integer expected")
	}

	// 返回已转换的 int64 Lua integer。
	return integerValue, nil
}

// integerValueArgument 按 Lua 标准库参数规则从单个 Value 提取 integer。
//
// value 是已经由调用方读取出的实参；position 使用 Lua 1-based 参数序号。该 helper 服务
// 固定寄存器快路径，必须与 integerArgument 的错误文本保持一致。
func integerValueArgument(value runtime.Value, position int, functionName string) (int64, error) {
	integerValue, ok := value.ToInteger()
	if !ok {
		if value.Kind == runtime.KindNumber {
			// float number 但无法无损表示为 Lua integer 时，官方错误文本强调整数表示失败。
			return 0, badArgument(functionName, position, "number has no integer representation")
		}
		// 非整数值不能作为 string 标准库的索引或次数。
		return 0, badArgument(functionName, position, "integer expected")
	}

	// 返回已转换的 int64 Lua integer。
	return integerValue, nil
}

// normalizeRange 将 Lua 1-based 闭区间换算为 Go 0-based 半开区间。
//
// length 是字符串字节长度；startIndex/endIndex 可为负数。返回 ok=false 表示裁剪后为空区间。
func normalizeRange(length int, startIndex int64, endIndex int64) (int, int, bool) {
	// 先把 Lua 索引转换为 1-based 正向位置。
	start := normalizeLuaIndex(length, startIndex)
	end := normalizeLuaIndex(length, endIndex)
	if start < 1 {
		// 起点小于 1 时裁剪到首字节。
		start = 1
	}
	if end > int64(length) {
		// 终点超过长度时裁剪到尾字节。
		end = int64(length)
	}
	if start > end || length == 0 {
		// 裁剪后没有可返回字节。
		return 0, 0, false
	}

	// Go 半开区间左边界为 Lua 起点减 1，右边界为 Lua 终点本身。
	return int(start - 1), int(end), true
}

// normalizeStart 将 Lua init 参数换算为 Go 0-based 起始偏移。
//
// length 是字符串字节长度；index 可为负数。返回值会被裁剪到 `[0, length+1]` 区间。
func normalizeStart(length int, index int64) int {
	// 先转换为 Lua 1-based 正向位置。
	normalized := normalizeLuaIndex(length, index)
	if normalized < 1 {
		// 小于首位时从字符串开头查找。
		return 0
	}
	if normalized > int64(length)+1 {
		// 允许返回 length+1 作为空尾位置，再由调用方判断是否可匹配。
		return length + 1
	}

	// 正常位置换算为 Go 0-based 偏移。
	return int(normalized - 1)
}

// normalizeLuaIndex 将 Lua 字符串索引换算为 1-based 正向索引。
//
// index 为负数时从字符串尾部倒数；index 为 0 时保持 0，便于调用方执行裁剪。
func normalizeLuaIndex(length int, index int64) int64 {
	if index < 0 {
		// 负索引按 length + index + 1 换算，例如 -1 指向最后一个字节。
		return int64(length) + index + 1
	}

	// 非负索引直接返回，调用方负责边界裁剪。
	return index
}

// parseFormatOption 从 `%` 起点解析一个 Lua string.format 格式项。
//
// 返回值 formatOption 包含起始 `%` 和最终格式字符；nextIndex 指向格式字符所在偏移。
// 当前解析 flag、宽度和精度，不支持长度修饰符，因为 Lua 5.3 已禁止其直接暴露给脚本。
func parseFormatOption(formatText string, percentIndex int) (string, int, error) {
	// 格式项必须至少包含 `%` 后的一个字符。
	optionIndex := percentIndex + 1
	seenFlags := map[byte]bool{}
	for optionIndex < len(formatText) && strings.ContainsRune("-+ #0", rune(formatText[optionIndex])) {
		if seenFlags[formatText[optionIndex]] {
			// Lua 5.3 对重复 flag 返回 repeated flags，避免交给宿主 printf 产生差异。
			return "", 0, runtime.RaiseError(runtime.StringValue("repeated flags"))
		}
		seenFlags[formatText[optionIndex]] = true
		// 跳过 printf flag 字符。
		optionIndex++
	}
	widthStart := optionIndex
	for optionIndex < len(formatText) && formatText[optionIndex] >= '0' && formatText[optionIndex] <= '9' {
		// 跳过十进制宽度。
		optionIndex++
	}
	if isFormatNumberTooLong(formatText[widthStart:optionIndex]) {
		// Lua 5.3 限制单个格式项宽度，过大时直接报 too long。
		return "", 0, runtime.RaiseError(runtime.StringValue("too long"))
	}
	if optionIndex < len(formatText) && formatText[optionIndex] == '.' {
		// 精度由点号和可选十进制数字组成。
		optionIndex++
		precisionStart := optionIndex
		for optionIndex < len(formatText) && formatText[optionIndex] >= '0' && formatText[optionIndex] <= '9' {
			// 跳过精度数字。
			optionIndex++
		}
		if isFormatNumberTooLong(formatText[precisionStart:optionIndex]) {
			// Lua 5.3 限制单个格式项精度，过大时直接报 too long。
			return "", 0, runtime.RaiseError(runtime.StringValue("too long"))
		}
	}
	if optionIndex >= len(formatText) {
		// 格式项未提供最终格式字符。
		return "", 0, runtime.RaiseError(runtime.StringValue("invalid option '%' to 'format'"))
	}
	verb := formatText[optionIndex]
	switch verb {
	case 's', 'q', 'c', 'd', 'i', 'o', 'u', 'x', 'X', 'f', 'g', 'a', 'A':
		// 当前支持的格式字符可以继续交给单项格式化逻辑。
		return formatText[percentIndex : optionIndex+1], optionIndex, nil
	default:
		// 未支持的格式字符返回明确 Lua error，避免静默产生错误文本。
		return "", 0, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid option '%s' to 'format'", formatText[percentIndex:optionIndex+1])))
	}
}

// isFormatNumberTooLong 判断格式项宽度或精度是否超过 Lua 兼容上限。
//
// numberText 只包含十进制数字；空串表示该部分缺省。当前按官方测试约束拒绝三位及以上，
// 或解析后大于等于 100 的宽度/精度，避免生成超长中间字符串。
func isFormatNumberTooLong(numberText string) bool {
	if numberText == "" {
		// 缺省宽度或精度不参与上限检查。
		return false
	}
	if len(numberText) >= 3 {
		// 三位及以上数字在官方 strings.lua 中应报 too long。
		return true
	}
	numberValue, err := strconv.Atoi(numberText)
	if err != nil {
		// 理论上不会失败；失败时按过长处理更保守。
		return true
	}
	return numberValue >= 100
}

// formatOne 格式化单个 string.format 格式项。
//
// formatOption 包含 `%` 和最终格式字符；value 是对应 Lua 参数；position 是 Lua 1-based 参数序号。
func formatOne(formatOption string, value runtime.Value, position int) (string, error) {
	// 不带 State 的入口保留给单测和旧调用方。
	return formatOneWithState(nil, formatOption, value, position)
}

// formatOneWithState 格式化单个 string.format 格式项，并支持 State 绑定的 `%s` 转换。
//
// state 可为 nil；仅 `%s` 会使用 state 执行 Lua closure `__tostring` 元方法，其他格式保持
// 参数自身的数字、字节或 literal 转换规则。
func formatOneWithState(state *runtime.State, formatOption string, value runtime.Value, position int) (string, error) {
	// 格式项解析阶段已保证存在最终格式字符。
	verb := formatOption[len(formatOption)-1]
	// 根据当前支持的格式字符执行转换。
	switch verb {
	case 's':
		// %s 复用 runtime.ToString，允许 number、boolean、nil 和 string 基础转换。
		converted, err := runtime.ToStringWithState(state, value)
		if err != nil {
			// tostring 元方法失败时返回原始错误。
			return "", err
		}
		if strings.Contains(converted.String, "\x00") && formatOption != "%s" {
			// Lua 5.3 禁止带修饰的 %s 格式化含 NUL 的字符串，避免 C printf 截断语义。
			return "", runtime.RaiseError(runtime.StringValue("string contains zeros"))
		}
		return fmt.Sprintf(formatOption, converted.String), nil
	case 'q':
		// %q 只接受可表示为 Lua 字面量的 string/number，并保持 number 类型可回读。
		return formatQuoteValue(value, position)
	case 'c':
		// %c 需要 byte 范围内的 integer，并按 Lua 字节字符串输出。
		integerValue, ok := formatIntegerValue(value)
		if !ok {
			// 非整数不能按字符格式化。
			return "", badArgument("format", position, "integer expected")
		}
		if integerValue < 0 || integerValue > 255 {
			// Lua 5.3 string.format("%c") 只接受可写入单字节字符串的值。
			return "", badArgument("format", position, "value out of range")
		}
		return string([]byte{byte(integerValue)}), nil
	case 'd', 'i':
		// %d/%i 需要 integer 参数。
		integerValue, ok := formatIntegerValue(value)
		if !ok {
			// 非整数不能按十进制整数格式化。
			return "", badArgument("format", position, "integer expected")
		}
		if verb == 'i' {
			// Go fmt 没有 %i，Lua 的 %i 与 %d 等价，最终格式字符需替换为 d。
			formatOption = formatOption[:len(formatOption)-1] + "d"
		}
		return fmt.Sprintf(formatOption, integerValue), nil
	case 'o', 'u', 'x', 'X':
		// 无符号整数格式使用 uint64 视图，匹配 Lua/C printf 对负整数的补码展示。
		integerValue, ok := formatIntegerValue(value)
		if !ok {
			// 非整数不能按无符号整数格式化。
			return "", badArgument("format", position, "integer expected")
		}
		if verb == 'u' {
			// Go fmt 没有 %u，使用 %d 格式化 uint64。
			formatOption = formatOption[:len(formatOption)-1] + "d"
		}
		return fmt.Sprintf(formatOption, uint64(integerValue)), nil
	case 'f', 'a', 'A':
		// %f 需要 number 参数，默认保留 6 位小数以贴近 printf。
		numberValue, ok := value.ToNumber()
		if !ok {
			// 非 number 不能按浮点数格式化。
			return "", badArgument("format", position, "number expected")
		}
		if verb == 'a' || verb == 'A' {
			// Lua 5.3 对 %a/%A 的 inf/nan 文本使用大小写固定形式，不带正号。
			if special, specialOK := formatHexFloatSpecial(numberValue, verb); specialOK {
				return special, nil
			}
		}
		if verb == 'a' {
			// Go fmt 使用 %x 表达 C99/Lua 的十六进制浮点 `%a`。
			formatOption = formatOption[:len(formatOption)-1] + "x"
		}
		if verb == 'A' {
			// Go fmt 使用 %X 表达 C99/Lua 的大写十六进制浮点 `%A`。
			formatOption = formatOption[:len(formatOption)-1] + "X"
		}
		return normalizeHexFloatExponent(fmt.Sprintf(formatOption, numberValue)), nil
	case 'g':
		// %g 需要 number 参数，使用紧凑格式。
		numberValue, ok := value.ToNumber()
		if !ok {
			// 非 number 不能按浮点数格式化。
			return "", badArgument("format", position, "number expected")
		}
		return fmt.Sprintf(formatOption, numberValue), nil
	default:
		// 未支持的格式字符返回明确 Lua error，避免静默产生错误文本。
		return "", runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid option '%%%c' to 'format'", verb)))
	}
}

// formatHexFloatSpecial 格式化 `%a/%A` 的 inf、-inf 和 nan 特殊值。
//
// numberValue 必须是待格式化浮点数；verb 为 a 时返回小写文本，verb 为 A 时返回大写文本。
// 第二返回值表示是否命中特殊值，未命中时由普通十六进制浮点路径继续处理。
func formatHexFloatSpecial(numberValue float64, verb byte) (string, bool) {
	if math.IsInf(numberValue, 1) {
		// Lua 5.3 官方测试期望正无穷不带加号。
		if verb == 'A' {
			return "INF", true
		}
		return "inf", true
	}
	if math.IsInf(numberValue, -1) {
		// 负无穷保留负号，并按格式字符决定大小写。
		if verb == 'A' {
			return "-INF", true
		}
		return "-inf", true
	}
	if math.IsNaN(numberValue) {
		// NaN 符号不可移植，当前按无符号 Lua 文本输出。
		if verb == 'A' {
			return "NAN", true
		}
		return "nan", true
	}
	return "", false
}

// formatQuoteValue 格式化 Lua 5.3 string.format("%q") 的单个参数。
//
// value 只能是 string 或 number；number 直接输出数字字面量，string 使用 Lua quote 规则。
// 其他类型没有 Lua 字面量表示，返回包含 no literal 的参数错误。
func formatQuoteValue(value runtime.Value, position int) (string, error) {
	switch value.Kind {
	case runtime.KindNil:
		// nil 是 Lua 基础字面量，可直接被 load 回读。
		return "nil", nil
	case runtime.KindBoolean:
		// boolean 是 Lua 基础字面量，可直接被 load 回读。
		if value.Bool {
			return "true", nil
		}
		return "false", nil
	case runtime.KindString:
		// string 字面量需要加双引号并转义控制字节。
		return quoteLuaString(value.String), nil
	case runtime.KindInteger, runtime.KindNumber:
		// number 直接使用 Lua 数字转字符串结果，保证 load 后仍是 number。
		if value.Kind == runtime.KindInteger && value.Integer == math.MinInt64 {
			// 当前 parser 不能把十进制 9223372036854775808 作为一元负号操作数回读，使用等价十六进制。
			return "-0x8000000000000000", nil
		}
		converted, ok := value.NumberToString()
		if !ok {
			// NaN/Inf 等无法作为 Lua 数字字面量时没有可回读 literal。
			return "", badArgument("format", position, "no literal")
		}
		return converted, nil
	default:
		// table/function/userdata/thread/nil/boolean 没有 Lua 5.3 %q 字面量表示。
		return "", badArgument("format", position, "no literal")
	}
}

// normalizeHexFloatExponent 归一化十六进制浮点指数中的前导零。
//
// Go fmt 为 `%x/%X` 输出 `p+00`，Lua 5.3 官方格式更接近 C printf 的 `p+0`。该函数只处理
// 已经格式化出的有限十六进制浮点文本，inf/nan 或不含指数的文本会原样返回。
func normalizeHexFloatExponent(formatted string) string {
	// 先定位 p/P 指数分隔符；没有分隔符时无需归一化。
	exponentIndex := strings.LastIndexAny(formatted, "pP")
	if exponentIndex < 0 || exponentIndex+1 >= len(formatted) {
		// inf/nan 等特殊值没有指数，直接返回。
		return formatted
	}

	signIndex := exponentIndex + 1
	digitsIndex := signIndex
	if formatted[signIndex] == '+' || formatted[signIndex] == '-' {
		// 指数符号保留，后续只裁剪数字部分。
		digitsIndex++
	}
	if digitsIndex >= len(formatted) {
		// 损坏格式没有数字，保守返回原文。
		return formatted
	}

	firstSignificantIndex := digitsIndex
	for firstSignificantIndex+1 < len(formatted) && formatted[firstSignificantIndex] == '0' {
		// 去掉指数数字前导零，但至少保留最后一个数字。
		firstSignificantIndex++
	}
	if firstSignificantIndex == digitsIndex {
		// 没有前导零时无需重新分配字符串。
		return formatted
	}
	return formatted[:digitsIndex] + formatted[firstSignificantIndex:]
}

// quoteLuaString 按 Lua 5.3 string.format("%q") 规则转义字符串。
//
// source 按 Lua 字节字符串处理；结果包含外层双引号。换行使用反斜杠加真实换行，NUL 在不
// 与后续数字粘连时使用 `\0`，其余控制字符使用三位十进制转义，保证 load 可回读。
func quoteLuaString(source string) string {
	// 预留外层引号和常见转义空间，减少小字符串格式化时的扩容。
	var builder strings.Builder
	builder.Grow(len(source) + 2)
	builder.WriteByte('"')
	for index := 0; index < len(source); index++ {
		// 按字节处理，避免 Go rune 解码改变 Lua 原始字符串内容。
		currentByte := source[index]
		switch currentByte {
		case '"', '\\':
			// 双引号和反斜杠必须加反斜杠，保证结果仍是合法短字符串。
			builder.WriteByte('\\')
			builder.WriteByte(currentByte)
		case '\n':
			// Lua 5.3 的 %q 对换行输出反斜杠加真实换行。
			builder.WriteByte('\\')
			builder.WriteByte('\n')
		case 0:
			if index+1 < len(source) && source[index+1] >= '0' && source[index+1] <= '9' {
				// NUL 后接数字时使用三位转义，避免和后续数字组成不同字节值。
				builder.WriteString(`\000`)
			} else {
				// 单独 NUL 使用官方测试期望的短转义。
				builder.WriteString(`\0`)
			}
		case 255:
			// 0xff 在源码中容易被宿主文本解码替换，使用十进制转义保证 load 可回读。
			builder.WriteString(`\255`)
		default:
			if currentByte < 32 {
				// 其他控制字符使用固定三位十进制转义，便于 load 精确回读。
				builder.WriteString(fmt.Sprintf(`\%03d`, currentByte))
				continue
			}
			// 普通字节原样输出，包括非 ASCII 字节。
			builder.WriteByte(currentByte)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

// formatIntegerValue 按 Lua string.format 的整数格式转换参数。
//
// value 可以是 integer、可无损转整数的 number，或内容为十进制整数的 string；返回 false 表示不能转换。
func formatIntegerValue(value runtime.Value) (int64, bool) {
	if integerValue, ok := value.ToInteger(); ok {
		// runtime.Value 自带整数转换优先覆盖 integer/number。
		return integerValue, true
	}
	if value.Kind != runtime.KindString {
		// 非字符串且不能 ToInteger 的值不能用于 %d/%i。
		return 0, false
	}
	trimmedValue := strings.TrimSpace(value.String)
	if trimmedValue == "" {
		// 空字符串不能转换为整数。
		return 0, false
	}
	integerValue, err := strconv.ParseInt(trimmedValue, 10, 64)
	if err != nil {
		// 非十进制整数字符串不能转换。
		return 0, false
	}
	return integerValue, true
}

// badArgument 构造 Lua 标准库参数错误。
//
// functionName 是标准库函数名，position 是 Lua 1-based 参数序号，detail 是期望或错误原因。
func badArgument(functionName string, position int, detail string) error {
	// 标准库参数错误以 Lua string error object 传播。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (%s)", position, functionName, detail)))
}
