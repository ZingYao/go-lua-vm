package bytecode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	// luaTypeNil 是 Lua 5.3 binary chunk 中 nil 常量的类型 tag。
	luaTypeNil byte = 0
	// luaTypeBoolean 是 Lua 5.3 binary chunk 中 boolean 常量的类型 tag。
	luaTypeBoolean byte = 1
	// luaTypeNumberFloat 是 Lua 5.3 binary chunk 中 float number 常量的类型 tag。
	luaTypeNumberFloat byte = 3
	// luaTypeNumberInteger 是 Lua 5.3 binary chunk 中 integer number 常量的类型 tag。
	luaTypeNumberInteger byte = 3 | (1 << 4)
	// luaTypeShortString 是 Lua 5.3 binary chunk 中 short string 常量的类型 tag。
	luaTypeShortString byte = 4
	// luaTypeLongString 是 Lua 5.3 binary chunk 中 long string 常量的类型 tag。
	luaTypeLongString byte = 4 | (1 << 4)
	// longStringSizeMarker 表示后续使用 size_t 读取 Lua 字符串长度。
	longStringSizeMarker byte = 0xff
)

var (
	// ErrInvalidChunkData 表示 binary chunk 头部之后的函数体数据不符合 Lua 5.3.6 格式。
	ErrInvalidChunkData = errors.New("invalid lua 5.3 chunk data")
)

// chunkReadError 将 chunk 读取失败包装为 Lua 5.3 可见的错误文本。
//
// base 必须是 ErrInvalidChunkHeader 或 ErrInvalidChunkData；EOF/UnexpectedEOF 统一输出
// truncated precompiled chunk，供 load 截断 binary chunk 时匹配官方错误片段。
func chunkReadError(base error, fieldName string, err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		// 短读说明预编译 chunk 被截断，错误文本必须包含 truncated。
		return fmt.Errorf("%w: truncated precompiled chunk: read %s: %v", base, fieldName, err)
	}
	return fmt.Errorf("%w: read %s: %v", base, fieldName, err)
}

// LoadConstants 从 reader 读取 Lua 5.3 Proto 常量表。
//
// 入参 reader 必须位于 LoadConstants 对应的常量表位置；返回值按 chunk 中的顺序保存
// 常量。该函数只读取常量表，不读取 upvalue、子 Proto 或 Debug 信息。
func LoadConstants(reader io.Reader) ([]Constant, error) {
	// 先读取常量数量，再按 Lua 5.3 LoadConstants 的顺序逐项读取 tag 与值。
	constantCount, err := readInt(reader, "constant count")
	if err != nil {
		// 常量数量缺失时，常量表无法继续解析。
		return nil, err
	}
	if constantCount < 0 {
		// 常量数量不允许为负，负值说明 chunk 数据已损坏。
		return nil, fmt.Errorf("%w: negative constant count %d", ErrInvalidChunkData, constantCount)
	}

	constants := make([]Constant, constantCount)
	for constantIndex := 0; constantIndex < constantCount; constantIndex++ {
		constant, err := loadConstant(reader)
		if err != nil {
			// 任意单项常量读取失败时，返回带索引的错误，便于定位损坏位置。
			return nil, fmt.Errorf("%w: constant[%d]: %v", ErrInvalidChunkData, constantIndex, err)
		}
		constants[constantIndex] = constant
	}

	// 所有常量均已按顺序读取完成，返回可直接挂入 Proto.Constants 的切片。
	return constants, nil
}

// AppendConstants 向目标字节切片追加 Lua 5.3 常量表编码。
//
// 该函数主要服务测试和后续 dump 实现；入参 constants 会按当前顺序写入，返回值包含
// 原有内容与追加后的常量表字节。
func AppendConstants(target []byte, constants []Constant) []byte {
	// Lua 5.3 先写入 int 常量数量，再逐项写入类型 tag 和对应值。
	target = appendInt(target, int32(len(constants)))
	for _, constant := range constants {
		switch constant.Kind {
		case ConstantNil:
			// nil 常量只写类型 tag，不写附加值。
			target = append(target, luaTypeNil)
		case ConstantBoolean:
			// boolean 常量写类型 tag 后追加 0 或 1。
			target = append(target, luaTypeBoolean)
			if constant.Bool {
				// true 在官方 chunk 中编码为非零 byte，这里固定使用 1。
				target = append(target, 1)
			} else {
				// false 在官方 chunk 中编码为 0。
				target = append(target, 0)
			}
		case ConstantInteger:
			// integer 常量写类型 tag 后追加 8 字节小端整数。
			target = append(target, luaTypeNumberInteger)
			target = appendInteger(target, constant.Integer)
		case ConstantNumber:
			// float number 常量写类型 tag 后追加 8 字节 IEEE-754 小端浮点。
			target = append(target, luaTypeNumberFloat)
			target = appendNumber(target, constant.Number)
		case ConstantString:
			// string 常量写 short string tag 后追加 Lua 字符串长度与字节内容。
			target = append(target, luaTypeShortString)
			target = appendLuaString(target, constant.String)
		default:
			// 未知常量类型无法编码为 Lua 5.3 chunk，保持 panic 让测试和开发期尽早暴露。
			panic(fmt.Sprintf("unsupported constant kind %d", constant.Kind))
		}
	}

	// 返回追加常量表后的字节切片。
	return target
}

// loadConstant 从 reader 读取单个 Lua 5.3 常量。
//
// 该函数对齐 lundump.c 中 LoadConstants 的 switch 分支。
func loadConstant(reader io.Reader) (Constant, error) {
	// 常量第一字节是 Lua 类型 tag，用于决定后续读取方式。
	typeTag, err := readDataByte(reader, "constant type")
	if err != nil {
		// 类型 tag 缺失时，无法判断常量类型。
		return Constant{}, err
	}

	switch typeTag {
	case luaTypeNil:
		// nil 常量没有附加负载。
		return NilConstant(), nil
	case luaTypeBoolean:
		// boolean 常量后跟一个 byte，0 表示 false，非 0 表示 true。
		value, err := readDataByte(reader, "boolean constant")
		if err != nil {
			// boolean 负载缺失时，常量表损坏。
			return Constant{}, err
		}
		return BooleanConstant(value != 0), nil
	case luaTypeNumberFloat:
		// float number 常量后跟 lua_Number 字节。
		value, err := readNumber(reader, "float constant")
		if err != nil {
			// 浮点负载缺失时，常量表损坏。
			return Constant{}, err
		}
		return NumberConstant(value), nil
	case luaTypeNumberInteger:
		// integer 常量后跟 lua_Integer 字节。
		value, err := readInteger(reader, "integer constant")
		if err != nil {
			// 整数负载缺失时，常量表损坏。
			return Constant{}, err
		}
		return IntegerConstant(value), nil
	case luaTypeShortString, luaTypeLongString:
		// short string 与 long string 的 chunk 负载格式相同，仅类型 tag 不同。
		value, err := readLuaString(reader, "string constant")
		if err != nil {
			// 字符串负载缺失或非法时，常量表损坏。
			return Constant{}, err
		}
		return StringConstant(value), nil
	default:
		// 未知类型 tag 说明 chunk 与 Lua 5.3 常量表格式不兼容。
		return Constant{}, fmt.Errorf("%w: unsupported constant tag 0x%02x", ErrInvalidChunkData, typeTag)
	}
}

// readInt 读取 Lua 5.3 chunk 中的 int 值。
//
// 当前实现支持官方 64 位构建下的 4 字节小端 int。
func readInt(reader io.Reader, fieldName string) (int, error) {
	// 使用 int32 承接 chunk int，再转换为 Go int 供切片长度使用。
	var value int32
	if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
		// 读取失败时包装字段名，便于定位损坏字段。
		return 0, chunkReadError(ErrInvalidChunkData, fieldName, err)
	}

	// 返回 Go int，后续调用方负责检查是否为负。
	return int(value), nil
}

// readInteger 读取 Lua 5.3 chunk 中的 lua_Integer 值。
//
// 当前实现支持官方默认 8 字节小端整数格式。
func readInteger(reader io.Reader, fieldName string) (int64, error) {
	// 直接读取 int64，对齐 header 中校验过的 lua_Integer size。
	var value int64
	if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
		// 读取失败时包装字段名，便于定位损坏字段。
		return 0, chunkReadError(ErrInvalidChunkData, fieldName, err)
	}

	// 返回原始整数值，不做浮点转换。
	return value, nil
}

// readNumber 读取 Lua 5.3 chunk 中的 lua_Number 值。
//
// 当前实现支持官方默认 8 字节 IEEE-754 little-endian float64。
func readNumber(reader io.Reader, fieldName string) (float64, error) {
	// 直接读取 float64，对齐 header 中校验过的 lua_Number size。
	var value float64
	if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
		// 读取失败时包装字段名，便于定位损坏字段。
		return 0, chunkReadError(ErrInvalidChunkData, fieldName, err)
	}

	// 返回浮点值，NaN 等特殊值由后续运行时语义处理。
	return value, nil
}

// readLuaString 读取 Lua 5.3 chunk 中的字符串负载。
//
// Lua 字符串长度字段包含结尾 NUL；返回的 Go 字符串不包含该结尾 NUL。
func readLuaString(reader io.Reader, fieldName string) (string, error) {
	// 字符串先读取 1 字节长度；0 表示 NULL，0xff 表示后续使用 size_t。
	sizeByte, err := readDataByte(reader, fieldName+" size")
	if err != nil {
		// 长度字节缺失时，字符串无法继续解析。
		return "", err
	}

	stringSize := uint64(sizeByte)
	if sizeByte == longStringSizeMarker {
		// 0xff 表示长长度编码，后续读取 8 字节 size_t。
		if err := binary.Read(reader, binary.LittleEndian, &stringSize); err != nil {
			// size_t 读取失败时，字符串负载不完整。
			return "", chunkReadError(ErrInvalidChunkData, fieldName+" long size", err)
		}
	}
	if stringSize == 0 {
		// 常量表字符串不应为 NULL；Debug 名称可为空，但该函数用于常量读取。
		return "", fmt.Errorf("%w: %s is null", ErrInvalidChunkData, fieldName)
	}

	payloadSize := stringSize - 1
	if payloadSize > uint64(int(^uint(0)>>1)) {
		// Go 切片长度不能超过 int 最大值，超限说明 chunk 不适合当前进程加载。
		return "", fmt.Errorf("%w: %s too large: %d", ErrInvalidChunkData, fieldName, payloadSize)
	}

	buffer := make([]byte, int(payloadSize))
	if _, err := io.ReadFull(reader, buffer); err != nil {
		// 字符串字节不足时，常量表损坏。
		return "", chunkReadError(ErrInvalidChunkData, fieldName+" payload", err)
	}

	// 返回不含结尾 NUL 的字符串内容，保持 Lua LoadString 的 --size 语义。
	return string(buffer), nil
}

// readDataByte 从 reader 读取一个 chunk body 字节。
//
// fieldName 用于构造错误上下文；错误统一包装 ErrInvalidChunkData，避免与 header
// 校验错误混淆。
func readDataByte(reader io.Reader, fieldName string) (byte, error) {
	// 使用固定 1 字节缓冲读取，保持 body 解析逻辑简单可控。
	var buffer [1]byte
	if _, err := io.ReadFull(reader, buffer[:]); err != nil {
		// body 字节读取失败时包装为 chunk data 错误，便于上层分类处理。
		return 0, chunkReadError(ErrInvalidChunkData, fieldName, err)
	}

	// 读取成功后返回唯一字节。
	return buffer[0], nil
}

// appendInt 按 Lua 5.3 int 格式追加 4 字节小端整数。
//
// 该函数服务测试和后续 dump 实现，入参 value 必须已在 int32 范围内。
func appendInt(target []byte, value int32) []byte {
	// 使用固定 4 字节缓冲，保持与 ChunkIntSize 一致。
	var buffer [ChunkIntSize]byte
	binary.LittleEndian.PutUint32(buffer[:], uint32(value))
	return append(target, buffer[:]...)
}

// appendInteger 按 Lua 5.3 lua_Integer 格式追加 8 字节小端整数。
//
// 该函数服务测试和后续 dump 实现。
func appendInteger(target []byte, value int64) []byte {
	// 使用固定 8 字节缓冲，保持与 ChunkIntegerSize 一致。
	var buffer [ChunkIntegerSize]byte
	binary.LittleEndian.PutUint64(buffer[:], uint64(value))
	return append(target, buffer[:]...)
}

// appendNumber 按 Lua 5.3 lua_Number 格式追加 8 字节小端浮点数。
//
// 该函数服务测试和后续 dump 实现。
func appendNumber(target []byte, value float64) []byte {
	// 使用 Float64bits 保留浮点位模式，再按小端写入。
	var buffer [ChunkNumberSize]byte
	binary.LittleEndian.PutUint64(buffer[:], math.Float64bits(value))
	return append(target, buffer[:]...)
}

// appendLuaString 按 Lua 5.3 chunk 字符串格式追加字符串。
//
// Lua chunk 中字符串长度包含结尾 NUL，但实际负载只写入字符串字节本身。
func appendLuaString(target []byte, value string) []byte {
	stringSize := len(value) + 1
	if stringSize < int(longStringSizeMarker) {
		// 短长度直接使用 1 字节长度字段。
		target = append(target, byte(stringSize))
	} else {
		// 长长度使用 0xff 标记并追加 8 字节 size_t。
		target = append(target, longStringSizeMarker)
		var buffer [ChunkSizeTSize]byte
		binary.LittleEndian.PutUint64(buffer[:], uint64(stringSize))
		target = append(target, buffer[:]...)
	}

	// Lua 5.3 dump 不写入结尾 NUL，只写入长度减一后的字符串字节。
	return append(target, value...)
}
