// Package utf8lib 实现 Lua 5.3 utf8 标准库的第一阶段能力。
//
// 本包负责注册 `utf8` 库表，并提供 char、charpattern、codes、codepoint、len 和
// offset 的基础行为。所有索引均按 Lua 5.3 约定使用 1-based 字节位置，而不是 Go rune 下标。
package utf8lib

import (
	"fmt"
	"strings"
	goutf8 "unicode/utf8"

	"github.com/zing/go-lua-vm/runtime"
)

const (
	// CharPattern 是 Lua 5.3 `utf8.charpattern` 的 Lua pattern 文本。
	CharPattern = "[\x00-\x7F\xC2-\xF4][\x80-\xBF]*"
	// maxRuneCodePoint 是 Unicode 最大合法码点。
	maxRuneCodePoint = 0x10FFFF
)

// Open 将 Lua 5.3 utf8 标准库注册到 State 全局环境。
//
// state 必须非 nil 且未关闭；成功后全局 `utf8` 字段指向库表，并注册 char、charpattern、
// codes、codepoint、len 和 offset。错误语义对齐运行时生命周期错误和 Lua 标准库参数错误。
func Open(state *runtime.State) error {
	// 注册入口先校验 State 生命周期，避免向关闭后的全局表写入库函数。
	if state == nil {
		// nil State 没有 globals，调用方需要先创建 runtime.State。
		return fmt.Errorf("utf8 library unavailable: %w", runtime.ErrNilState)
	}
	if state.IsClosed() {
		// 已关闭 State 的 globals 已释放，不能继续注册标准库。
		return fmt.Errorf("utf8 library unavailable: %w", runtime.ErrClosedState)
	}

	library := runtime.NewTable()
	// utf8 库函数以 Go closure 注册，后续 VM CALL 会通过 bridge 调用。
	library.RawSetString("char", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Char)))
	library.RawSetString("charpattern", runtime.StringValue(CharPattern))
	library.RawSetString("codes", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Codes)))
	library.RawSetString("codepoint", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(CodePoint)))
	library.RawSetString("len", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Len)))
	library.RawSetString("offset", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Offset)))
	state.SetGlobal("utf8", runtime.ReferenceValue(runtime.KindTable, library))
	return nil
}

// Char 实现 Lua 5.3 `utf8.char`。
//
// 所有参数都必须可无损转换为 integer，且必须是合法 Unicode 码点。返回值是把这些码点
// 按 UTF-8 编码后拼接得到的 Lua string；无参数时返回空字符串。
func Char(args ...runtime.Value) ([]runtime.Value, error) {
	// char 按参数顺序编码每个 Unicode 码点。
	var builder strings.Builder
	for index := 1; index <= len(args); index++ {
		// 每个参数都必须是 integer 码点。
		codePoint, err := integerArgument(args, index, "char")
		if err != nil {
			// 任一参数不是 integer 时直接返回 Lua 参数错误。
			return nil, err
		}
		if !validCodePoint(codePoint) {
			// 非法 Unicode 码点不能编码为 UTF-8。
			return nil, badArgument("char", index, "value out of range")
		}
		// 写入当前码点的 UTF-8 编码。
		builder.WriteRune(rune(codePoint))
	}

	// 返回拼接后的 UTF-8 字符串。
	return []runtime.Value{runtime.StringValue(builder.String())}, nil
}

// Codes 实现 Lua 5.3 `utf8.codes`。
//
// 第一个参数必须是 string。返回 iterator、原始字符串和初始控制变量 0；iterator 每次
// 返回下一个字符的 1-based 字节位置与码点，遇到非法 UTF-8 时返回 Lua error。
func Codes(args ...runtime.Value) ([]runtime.Value, error) {
	// codes 首先提取待遍历字符串。
	source, err := stringArgument(args, 1, "codes")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	iterator := runtime.GoResultsFunction(func(values ...runtime.Value) ([]runtime.Value, error) {
		// iterator 复用 utf8.codes 的原始字符串，并使用控制变量表示上次返回的字节位置。
		iterSource, err := stringArgument(values, 1, "codes")
		if err != nil {
			// 迭代器收到非 string 状态时返回 Lua 参数错误。
			return nil, err
		}
		position, err := integerArgument(values, 2, "codes")
		if err != nil {
			// 控制变量必须是上一次返回的 integer 字节位置。
			return nil, err
		}
		if position >= int64(len(iterSource)) {
			// 已越过最后一个字节时结束迭代。
			return nil, nil
		}
		if position < 0 {
			// 负控制变量无法映射到合法下一字节。
			return nil, badArgument("codes", 2, "position out of range")
		}

		nextByte := 0
		if position > 0 {
			// 控制变量是上次返回的 1-based 起始字节，先解码上一个字符得到下一起点。
			previousByte := int(position) - 1
			previousRune, previousSize := goutf8.DecodeRuneInString(iterSource[previousByte:])
			if previousRune == goutf8.RuneError && previousSize == 1 {
				// 上一个控制位置本身非法时，按该位置报告 UTF-8 错误。
				return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid UTF-8 code at byte %d", previousByte+1)))
			}
			nextByte = previousByte + previousSize
		}
		if nextByte >= len(iterSource) {
			// 上一个字符已经是最后一个字符，迭代结束。
			return nil, nil
		}
		codePoint, size := goutf8.DecodeRuneInString(iterSource[nextByte:])
		if codePoint == goutf8.RuneError && size == 1 {
			// DecodeRuneInString 用 RuneError/1 表示非法 UTF-8 起始字节。
			return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid UTF-8 code at byte %d", nextByte+1)))
		}

		// 返回 Lua 1-based 字节位置和当前 Unicode 码点。
		return []runtime.Value{runtime.IntegerValue(int64(nextByte + 1)), runtime.IntegerValue(int64(codePoint))}, nil
	})

	// 返回 iterator、遍历字符串和初始控制变量 0。
	return []runtime.Value{
		runtime.ReferenceValue(runtime.KindGoClosure, iterator),
		runtime.StringValue(source),
		runtime.IntegerValue(0),
	}, nil
}

// CodePoint 实现 Lua 5.3 `utf8.codepoint`。
//
// 第一个参数必须是 string；i、j 是可选 1-based 字节位置，默认 i=1、j=i，支持负索引。
// 返回 [i,j] 范围内每个 UTF-8 字符的码点；范围内出现非法 UTF-8 时返回 Lua error。
func CodePoint(args ...runtime.Value) ([]runtime.Value, error) {
	// codepoint 首先提取待读取字符串。
	source, err := stringArgument(args, 1, "codepoint")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	start, err := optionalIntegerArgument(args, 2, 1, "codepoint")
	if err != nil {
		// 第二个参数不是 integer 时直接返回 Lua 参数错误。
		return nil, err
	}
	end, err := optionalIntegerArgument(args, 3, start, "codepoint")
	if err != nil {
		// 第三个参数不是 integer 时直接返回 Lua 参数错误。
		return nil, err
	}

	startIndex := normalizeByteIndex(start, len(source))
	endIndex := normalizeByteIndex(end, len(source))
	if startIndex < 1 || startIndex > len(source)+1 {
		// 起始字节位置必须位于字符串边界内，允许空尾位置 len+1。
		return nil, badArgument("codepoint", 2, "position out of range")
	}
	if endIndex < 0 || endIndex > len(source) {
		// 结束字节位置必须落在字符串内或空范围前一位。
		return nil, badArgument("codepoint", 3, "position out of range")
	}
	if startIndex > endIndex {
		// 空范围返回零个结果。
		return nil, nil
	}

	// 按字节范围逐个解码 UTF-8 字符。
	results := make([]runtime.Value, 0)
	for offset := startIndex - 1; offset <= endIndex-1; {
		// 每次从当前字节位置解码一个完整 UTF-8 字符。
		codePoint, size := goutf8.DecodeRuneInString(source[offset:])
		if codePoint == goutf8.RuneError && size == 1 {
			// 非法 UTF-8 字节无法转换为码点。
			return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid UTF-8 code at byte %d", offset+1)))
		}
		results = append(results, runtime.IntegerValue(int64(codePoint)))
		offset += size
	}

	// 返回范围内所有码点。
	return results, nil
}

// Len 实现 Lua 5.3 `utf8.len`。
//
// 第一个参数必须是 string；i、j 是可选 1-based 字节位置，默认 i=1、j=-1，支持负索引。
// 成功时返回 UTF-8 字符数量；遇到非法 UTF-8 时返回 nil 和首个非法字节位置。
func Len(args ...runtime.Value) ([]runtime.Value, error) {
	// len 首先提取待统计字符串。
	source, err := stringArgument(args, 1, "len")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}

	start, err := optionalIntegerArgument(args, 2, 1, "len")
	if err != nil {
		// 第二个参数不是 integer 时直接返回 Lua 参数错误。
		return nil, err
	}
	end, err := optionalIntegerArgument(args, 3, -1, "len")
	if err != nil {
		// 第三个参数不是 integer 时直接返回 Lua 参数错误。
		return nil, err
	}

	startIndex := normalizeByteIndex(start, len(source))
	endIndex := normalizeByteIndex(end, len(source))
	if startIndex < 1 || startIndex > len(source)+1 {
		// 起始字节位置必须位于字符串边界内，允许空尾位置 len+1。
		return nil, badArgument("len", 2, "position out of range")
	}
	if endIndex < 0 || endIndex > len(source) {
		// 结束字节位置必须落在字符串内或空范围前一位。
		return nil, badArgument("len", 3, "position out of range")
	}
	if startIndex > endIndex {
		// 空范围长度为 0。
		return []runtime.Value{runtime.IntegerValue(0)}, nil
	}

	count := int64(0)
	for offset := startIndex - 1; offset <= endIndex-1; {
		// 每次从当前字节位置解码一个完整 UTF-8 字符。
		codePoint, size := goutf8.DecodeRuneInString(source[offset:])
		if codePoint == goutf8.RuneError && size == 1 {
			// Lua 5.3 utf8.len 遇到非法 UTF-8 时返回 nil 和首个非法字节位置。
			return []runtime.Value{runtime.NilValue(), runtime.IntegerValue(int64(offset + 1))}, nil
		}
		count++
		offset += size
	}

	// 返回统计得到的 UTF-8 字符数量。
	return []runtime.Value{runtime.IntegerValue(count)}, nil
}

// Offset 实现 Lua 5.3 `utf8.offset`。
//
// 第一个参数必须是 string，第二个参数 n 必须是 integer，第三个参数 i 可选。n>0 时从
// i 起向后找第 n 个字符起始字节；n<0 时从 i 前方向前找字符起始字节；n==0 时返回包含
// i 的字符起始字节。未找到时返回 nil，非法 UTF-8 起始位置返回 Lua error。
func Offset(args ...runtime.Value) ([]runtime.Value, error) {
	// offset 首先提取待定位字符串。
	source, err := stringArgument(args, 1, "offset")
	if err != nil {
		// 第一个参数不是 string 时直接返回 Lua 参数错误。
		return nil, err
	}
	n, err := integerArgument(args, 2, "offset")
	if err != nil {
		// 第二个参数不是 integer 时直接返回 Lua 参数错误。
		return nil, err
	}

	defaultIndex := int64(1)
	if n < 0 {
		// 负向搜索默认从字符串尾后位置开始，便于查找倒数第 n 个字符。
		defaultIndex = int64(len(source) + 1)
	}
	position, err := optionalIntegerArgument(args, 3, defaultIndex, "offset")
	if err != nil {
		// 第三个参数不是 integer 时直接返回 Lua 参数错误。
		return nil, err
	}

	index := normalizeByteIndex(position, len(source))
	if index < 1 || index > len(source)+1 {
		// offset 允许尾后位置 len+1，但不允许越过两端。
		return nil, badArgument("offset", 3, "position out of range")
	}

	if n == 0 {
		// n==0 返回包含 index 的 UTF-8 字符起始字节。
		return offsetContainingByte(source, index)
	}
	if n > 0 {
		// n>0 从 index 起向后查找第 n 个 UTF-8 字符起始字节。
		return offsetForward(source, index, n)
	}

	// n<0 从 index 前方向前查找第 -n 个 UTF-8 字符起始字节。
	return offsetBackward(source, index, -n)
}

// stringArgument 按 Lua 标准库参数规则提取 string。
//
// args 使用 0-based Go 切片；position 使用 Lua 1-based 参数序号。非 string 返回 Lua
// 参数错误，不执行其他类型到字符串的隐式转换。
func stringArgument(args []runtime.Value, position int, functionName string) (string, error) {
	// 先检查参数是否存在。
	if len(args) < position {
		// 缺失参数按 string expected 报错。
		return "", badArgument(functionName, position, "string expected")
	}
	if args[position-1].Kind != runtime.KindString {
		// 非 string 类型不做隐式转换。
		return "", badArgument(functionName, position, "string expected")
	}

	// 返回原始 Lua string 字节内容。
	return args[position-1].String, nil
}

// integerArgument 按 Lua 标准库参数规则提取 integer。
//
// args 使用 0-based Go 切片；position 使用 Lua 1-based 参数序号。integer 与可无损转换的
// float number 都会被接受，其他类型返回 Lua 参数错误。
func integerArgument(args []runtime.Value, position int, functionName string) (int64, error) {
	// 先检查参数是否存在。
	if len(args) < position {
		// 缺失整数参数无法提供默认值。
		return 0, badArgument(functionName, position, "integer expected")
	}

	integerValue, ok := args[position-1].ToInteger()
	if !ok {
		// 非整数值不能作为 utf8 标准库整数参数。
		return 0, badArgument(functionName, position, "integer expected")
	}

	// 返回已转换的 int64 Lua integer。
	return integerValue, nil
}

// optionalIntegerArgument 读取可选 integer 参数。
//
// 参数缺失或显式 nil 时返回 defaultValue；参数存在时必须可无损转换为 integer。
func optionalIntegerArgument(args []runtime.Value, position int, defaultValue int64, functionName string) (int64, error) {
	// 参数缺失时使用调用方提供的默认值。
	if len(args) < position || args[position-1].Kind == runtime.KindNil {
		return defaultValue, nil
	}

	// 参数存在时按 integer 规则解析。
	return integerArgument(args, position, functionName)
}

// normalizeByteIndex 将 Lua 1-based 字节索引规范化为正索引。
//
// index 为负数时从字符串末尾反向计算；length 是字符串字节长度。返回值仍为 Lua 1-based
// 字节位置，调用方负责判断是否落在具体函数允许的范围内。
func normalizeByteIndex(index int64, length int) int {
	// 负数索引按 Lua 字符串约定从末尾反向定位。
	if index < 0 {
		return length + int(index) + 1
	}

	// 非负索引直接转为 int。
	return int(index)
}

// validCodePoint 判断整数是否是可编码 UTF-8 码点。
//
// Lua 5.3 utf8.char 接受 0 到 0x10FFFF 内且不是 surrogate 的 Unicode 码点。
func validCodePoint(codePoint int64) bool {
	// 码点必须落在 Unicode 最大范围内。
	if codePoint < 0 || codePoint > maxRuneCodePoint {
		return false
	}
	if codePoint >= 0xD800 && codePoint <= 0xDFFF {
		// surrogate 半区不是合法 Unicode scalar value。
		return false
	}

	// 通过范围检查后可编码为 UTF-8。
	return true
}

// offsetContainingByte 返回包含指定 1-based 字节位置的 UTF-8 字符起始位置。
//
// source 必须是 Lua string 字节内容；index 必须已通过范围校验。尾后位置没有包含字符，
// 返回 nil；若向前回溯得到的字符非法，则返回 Lua error。
func offsetContainingByte(source string, index int) ([]runtime.Value, error) {
	if len(source) == 0 && index == 1 {
		// 空字符串的唯一合法边界同时是起点和尾后位置，Lua 5.3 返回 1。
		return []runtime.Value{runtime.IntegerValue(1)}, nil
	}
	// 尾后位置不属于任何 UTF-8 字符。
	if index == len(source)+1 {
		return []runtime.Value{runtime.NilValue()}, nil
	}

	offset := index - 1
	for offset > 0 && isContinuationByte(source[offset]) {
		// continuation byte 属于前一个 UTF-8 字符，继续向前找起始字节。
		offset--
	}
	if err := validateRuneAt(source, offset, "offset"); err != nil {
		// 找到的起始字节非法时直接返回 Lua error。
		return nil, err
	}

	// 返回包含 index 的字符起始位置。
	return []runtime.Value{runtime.IntegerValue(int64(offset + 1))}, nil
}

// offsetForward 从指定 1-based 字节位置向后查找第 n 个 UTF-8 字符起始位置。
//
// index 必须已通过范围校验，n 必须大于 0。若 index 在 continuation byte 上或遍历遇到
// 非法 UTF-8，返回 Lua error；若字符数量不足，返回 nil。
func offsetForward(source string, index int, n int64) ([]runtime.Value, error) {
	// 尾后位置之后没有可查找字符。
	if index == len(source)+1 {
		return []runtime.Value{runtime.NilValue()}, nil
	}

	offset := index - 1
	if isContinuationByte(source[offset]) {
		// Lua 5.3 要求正向起点不能位于 continuation byte。
		return nil, badArgument("offset", 3, "initial position is a continuation byte")
	}

	for count := int64(1); ; count++ {
		if offset == len(source) {
			if count == n {
				// 从最后一个字符继续查找下一个字符时，Lua 5.3 返回尾后位置。
				return []runtime.Value{runtime.IntegerValue(int64(len(source) + 1))}, nil
			}
			// 尾后之后没有更多字符起始位置。
			return []runtime.Value{runtime.NilValue()}, nil
		}
		// 每次 offset 都应指向一个合法 UTF-8 字符起始字节。
		codePoint, size := goutf8.DecodeRuneInString(source[offset:])
		if codePoint == goutf8.RuneError && size == 1 {
			// 非法 UTF-8 起始字节无法继续定位。
			return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid UTF-8 code at byte %d", offset+1)))
		}
		if count == n {
			// 找到第 n 个字符起始字节。
			return []runtime.Value{runtime.IntegerValue(int64(offset + 1))}, nil
		}
		offset += size
	}
}

// offsetBackward 从指定 1-based 字节位置前方向前查找第 n 个 UTF-8 字符起始位置。
//
// index 必须已通过范围校验，n 必须大于 0。若遍历遇到非法 UTF-8，返回 Lua error；若字符
// 数量不足，返回 nil。
func offsetBackward(source string, index int, n int64) ([]runtime.Value, error) {
	// 从 index 前一个字节开始回溯，index=len+1 时自然从最后一个字节开始。
	offset := index - 2
	for count := int64(0); offset >= 0; offset-- {
		// 跳过 continuation byte，直到找到一个候选起始字节。
		if isContinuationByte(source[offset]) {
			continue
		}
		if err := validateRuneAt(source, offset, "offset"); err != nil {
			// 候选起始字节非法时直接返回 Lua error。
			return nil, err
		}
		count++
		if count == n {
			// 找到第 n 个反向字符起始字节。
			return []runtime.Value{runtime.IntegerValue(int64(offset + 1))}, nil
		}
	}

	// 字符数量不足时返回 nil。
	return []runtime.Value{runtime.NilValue()}, nil
}

// validateRuneAt 校验 source[offset:] 是否从合法 UTF-8 字符起始字节开始。
//
// functionName 用于生成 Lua 错误消息；offset 是 0-based 字节位置。
func validateRuneAt(source string, offset int, functionName string) error {
	// 起始位置必须落在字符串范围内。
	if offset < 0 || offset >= len(source) {
		return badArgument(functionName, 3, "position out of range")
	}
	codePoint, size := goutf8.DecodeRuneInString(source[offset:])
	if codePoint == goutf8.RuneError && size == 1 {
		// RuneError/1 表示非法 UTF-8 起始字节。
		return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid UTF-8 code at byte %d", offset+1)))
	}

	// 解码成功表示 offset 处是合法 UTF-8 起始字节。
	return nil
}

// isContinuationByte 判断字节是否是 UTF-8 continuation byte。
//
// continuation byte 的二进制形态为 10xxxxxx，也就是取值区间 0x80 到 0xBF。
func isContinuationByte(value byte) bool {
	// 使用位掩码检查高两位是否为 10。
	return value&0xC0 == 0x80
}

// badArgument 构造 Lua 标准库参数错误。
//
// functionName 是标准库函数名，position 是 Lua 1-based 参数序号，detail 是期望或错误原因。
func badArgument(functionName string, position int, detail string) error {
	// 标准库参数错误以 Lua string error object 传播。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (%s)", position, functionName, detail)))
}
