package bytecode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	// ChunkSignature 是 Lua 5.3 预编译 chunk 的完整签名。
	ChunkSignature = "\x1bLua"
	// ChunkVersion 是 Lua 5.3 的 binary chunk 版本号，等于 0x53。
	ChunkVersion byte = 0x53
	// ChunkFormat 是 Lua 5.3 官方 binary chunk 格式号。
	ChunkFormat byte = 0
	// ChunkIntSize 是 Lua 5.3 官方 64 位构建下 int 的字节数。
	ChunkIntSize byte = 4
	// ChunkSizeTSize 是 Lua 5.3 官方 64 位构建下 size_t 的字节数。
	ChunkSizeTSize byte = 8
	// ChunkInstructionSize 是 Lua 5.3 Instruction 的字节数。
	ChunkInstructionSize byte = 4
	// ChunkIntegerSize 是 Lua 5.3 默认 lua_Integer 的字节数。
	ChunkIntegerSize byte = 8
	// ChunkNumberSize 是 Lua 5.3 默认 lua_Number 的字节数。
	ChunkNumberSize byte = 8
	// ChunkCheckInteger 是 Lua 5.3 用于检测端序和整数格式的哨兵值。
	ChunkCheckInteger int64 = 0x5678
	// ChunkCheckNumber 是 Lua 5.3 用于检测浮点格式的哨兵值。
	ChunkCheckNumber float64 = 370.5
)

var (
	// ChunkData 是 Lua 5.3 用于检测转换错误的固定字节序列。
	ChunkData = []byte{0x19, 0x93, '\r', '\n', 0x1a, '\n'}
	// ErrInvalidChunkHeader 表示 binary chunk 头部不符合 Lua 5.3.6 官方格式。
	ErrInvalidChunkHeader = errors.New("invalid lua 5.3 chunk header")
)

// ChunkHeader 保存 Lua 5.3 binary chunk 头部校验后的元数据。
//
// 字段对齐 lundump.c 的 checkHeader 顺序，后续 chunk loader 可根据这些字段决定
// 是否继续读取函数原型内容。
type ChunkHeader struct {
	// Version 是 chunk 文件中的 Lua 版本字节。
	Version byte
	// Format 是 chunk 文件中的格式字节。
	Format byte
	// IntSize 是 chunk 文件中 C int 的字节数。
	IntSize byte
	// SizeTSize 是 chunk 文件中 size_t 的字节数。
	SizeTSize byte
	// InstructionSize 是 chunk 文件中 Instruction 的字节数。
	InstructionSize byte
	// IntegerSize 是 chunk 文件中 lua_Integer 的字节数。
	IntegerSize byte
	// NumberSize 是 chunk 文件中 lua_Number 的字节数。
	NumberSize byte
	// CheckInteger 是 chunk 文件中的整数格式哨兵值。
	CheckInteger int64
	// CheckNumber 是 chunk 文件中的浮点格式哨兵值。
	CheckNumber float64
}

// LoadChunkHeader 从 reader 读取并校验 Lua 5.3 binary chunk 头部。
//
// 入参 reader 必须位于 chunk 起始位置；返回值包含已验证的头部字段。若 reader 数据不足、
// 签名错误、版本不兼容、格式不兼容、尺寸不匹配或端序/浮点格式不一致，则返回带
// ErrInvalidChunkHeader 包装的错误。
func LoadChunkHeader(reader io.Reader) (ChunkHeader, error) {
	// 按 Lua 5.3 lundump.c 的 checkHeader 顺序逐项读取并校验头部。
	var header ChunkHeader

	signature := make([]byte, len(ChunkSignature))
	if _, err := io.ReadFull(reader, signature); err != nil {
		// 签名都无法完整读取时，chunk 必然不完整，直接返回头部错误。
		return header, chunkReadError(ErrInvalidChunkHeader, "signature", err)
	}
	if string(signature) != ChunkSignature {
		// 签名不等于 ESC Lua 时，对齐官方错误语义，说明它不是预编译 chunk。
		return header, fmt.Errorf("%w: not a precompiled chunk", ErrInvalidChunkHeader)
	}

	version, err := readByte(reader, "version")
	if err != nil {
		// 版本字节缺失时，调用方不能继续判断格式，直接返回读取错误。
		return header, err
	}
	header.Version = version
	if header.Version != ChunkVersion {
		// 版本不等于 0x53 时，说明不是 Lua 5.3 binary chunk。
		return header, fmt.Errorf("%w: version mismatch: got 0x%02x want 0x%02x", ErrInvalidChunkHeader, header.Version, ChunkVersion)
	}

	format, err := readByte(reader, "format")
	if err != nil {
		// 格式字节缺失时，调用方不能继续读取 LUAC_DATA，直接返回读取错误。
		return header, err
	}
	header.Format = format
	if header.Format != ChunkFormat {
		// Lua 5.3 官方格式号必须是 0，其他格式需要单独实现兼容层。
		return header, fmt.Errorf("%w: format mismatch: got %d want %d", ErrInvalidChunkHeader, header.Format, ChunkFormat)
	}

	chunkData := make([]byte, len(ChunkData))
	if _, err := io.ReadFull(reader, chunkData); err != nil {
		// LUAC_DATA 不完整时，chunk 头部损坏，停止读取。
		return header, chunkReadError(ErrInvalidChunkHeader, "luac data", err)
	}
	if !bytes.Equal(chunkData, ChunkData) {
		// LUAC_DATA 不匹配时，对齐官方 corrupted 错误语义。
		return header, fmt.Errorf("%w: corrupted luac data", ErrInvalidChunkHeader)
	}

	if header.IntSize, err = readExpectedSize(reader, "int", ChunkIntSize); err != nil {
		// int 尺寸不匹配会影响后续整数读取，直接返回。
		return header, err
	}
	if header.SizeTSize, err = readExpectedSize(reader, "size_t", ChunkSizeTSize); err != nil {
		// size_t 尺寸不匹配会影响字符串长度读取，直接返回。
		return header, err
	}
	if header.InstructionSize, err = readExpectedSize(reader, "Instruction", ChunkInstructionSize); err != nil {
		// Instruction 尺寸不匹配会影响字节码读取，直接返回。
		return header, err
	}
	if header.IntegerSize, err = readExpectedSize(reader, "lua_Integer", ChunkIntegerSize); err != nil {
		// lua_Integer 尺寸不匹配会影响整数常量读取，直接返回。
		return header, err
	}
	if header.NumberSize, err = readExpectedSize(reader, "lua_Number", ChunkNumberSize); err != nil {
		// lua_Number 尺寸不匹配会影响浮点常量读取，直接返回。
		return header, err
	}

	if err := binary.Read(reader, binary.LittleEndian, &header.CheckInteger); err != nil {
		// 整数哨兵读取失败时，chunk 头部不完整，直接返回。
		return header, chunkReadError(ErrInvalidChunkHeader, "check integer", err)
	}
	if header.CheckInteger != ChunkCheckInteger {
		// Lua 5.3 使用该哨兵检测端序和整数格式，值不同则拒绝加载。
		return header, fmt.Errorf("%w: endianness mismatch: got 0x%x want 0x%x", ErrInvalidChunkHeader, header.CheckInteger, ChunkCheckInteger)
	}

	if err := binary.Read(reader, binary.LittleEndian, &header.CheckNumber); err != nil {
		// 浮点哨兵读取失败时，chunk 头部不完整，直接返回。
		return header, chunkReadError(ErrInvalidChunkHeader, "check number", err)
	}
	if math.Float64bits(header.CheckNumber) != math.Float64bits(ChunkCheckNumber) {
		// 浮点哨兵不同表示 lua_Number 格式不兼容，后续浮点常量无法可靠读取。
		return header, fmt.Errorf("%w: float format mismatch: got %v want %v", ErrInvalidChunkHeader, header.CheckNumber, ChunkCheckNumber)
	}

	// 所有头部字段均已通过校验，返回可供后续 loader 使用的元数据。
	return header, nil
}

// ValidateChunkHeader 校验一段字节是否以合法 Lua 5.3 binary chunk 头部开头。
//
// 入参 chunk 可以包含完整 chunk 或仅包含头部；返回值与 LoadChunkHeader 一致。
func ValidateChunkHeader(chunk []byte) (ChunkHeader, error) {
	// 使用 bytes.Reader 复用 reader 版本校验逻辑，避免维护两套头部解析。
	return LoadChunkHeader(bytes.NewReader(chunk))
}

// LoadBinaryChunk 读取完整 Lua 5.3 binary chunk 并返回顶层函数原型。
//
// 入参 reader 必须位于 chunk 起始位置；函数会依次校验 header、读取顶层闭包 upvalue
// 数量字节，并读取顶层 Proto。当前返回值不创建运行时 closure，只保留 Proto 数据。
func LoadBinaryChunk(reader io.Reader) (*Proto, error) {
	// 先校验 header，确保后续字段尺寸、端序和浮点格式符合当前实现。
	if _, err := LoadChunkHeader(reader); err != nil {
		// header 无效时不能继续解析 Proto，直接返回原始分类错误。
		return nil, err
	}

	upvalueCount, err := readDataByte(reader, "top-level upvalue count")
	if err != nil {
		// 顶层 upvalue 数量缺失时，chunk body 不完整。
		return nil, err
	}

	proto, err := LoadProto(reader)
	if err != nil {
		// Proto 读取失败时，返回 chunk body 错误链路。
		return nil, err
	}
	if int(upvalueCount) != len(proto.Upvalues) {
		// 官方 dump 写入 sizeupvalues，Go loader 显式校验它与 Proto.upvalues 一致。
		return nil, fmt.Errorf("%w: top-level upvalue count mismatch: got %d want %d", ErrInvalidChunkData, upvalueCount, len(proto.Upvalues))
	}
	if proto.Source == "" {
		// 完整 binary chunk 的顶层 source 缺失时，Lua 5.3 使用 "=?", 子函数空 source 也继承该占位。
		fillMissingProtoSource(proto, "=?")
	}

	// header、顶层 upvalue 数量和 Proto 均已读取完成。
	return proto, nil
}

// fillMissingProtoSource 递归填充 binary chunk 中缺失的 source。
//
// proto 必须来自完整 binary chunk loader；source 是父级可见源码名。该 helper 只处理 stripped
// chunk 的空 source，占位值对齐 Lua 5.3 `=?`，不会覆盖已有真实 source。
func fillMissingProtoSource(proto *Proto, source string) {
	if proto == nil {
		// nil Proto 没有可填充字段。
		return
	}
	if proto.Source == "" {
		// 缺失 source 时继承父级占位或真实 source。
		proto.Source = source
	} else {
		// 子函数继续继承当前函数的真实 source。
		source = proto.Source
	}
	for index := range proto.Protos {
		// stripped 子 Proto 需要与顶层保持同一占位 source。
		fillMissingProtoSource(proto.Protos[index], source)
	}
}

// DumpBinaryChunk 将顶层 Proto 编码为完整 Lua 5.3 binary chunk。
//
// 返回字节包含 header、顶层 upvalue 数量和 Proto body；当前实现保留 Debug 信息，不执行
// Lua 官方 dump 的 strip 分支。
func DumpBinaryChunk(proto *Proto) []byte {
	// 先写 header，再写顶层 closure 的 upvalue 数量，最后写函数原型。
	chunk := AppendChunkHeader(nil)
	chunk = append(chunk, byte(len(proto.Upvalues)))
	chunk = DumpProto(chunk, proto)
	return chunk
}

// DumpProto 向目标字节切片追加 Lua 5.3 函数原型编码。
//
// 该函数是 AppendProto 的公开语义入口，后续 dump、测试和工具代码应优先调用它。
func DumpProto(target []byte, proto *Proto) []byte {
	// 当前阶段复用 AppendProto 的编码顺序，保持与 ldump.c DumpFunction 对齐。
	return AppendProto(target, proto)
}

// AppendChunkHeader 向目标字节切片追加 Lua 5.3 官方 binary chunk 头部。
//
// 该函数服务测试、dump 和 golden 生成；返回的新切片包含原有内容和追加的头部字节。
func AppendChunkHeader(target []byte) []byte {
	// 先按官方顺序追加签名、版本、格式和 LUAC_DATA。
	target = append(target, ChunkSignature...)
	target = append(target, ChunkVersion, ChunkFormat)
	target = append(target, ChunkData...)
	target = append(target, ChunkIntSize, ChunkSizeTSize, ChunkInstructionSize, ChunkIntegerSize, ChunkNumberSize)

	integerBytes := make([]byte, ChunkIntegerSize)
	binary.LittleEndian.PutUint64(integerBytes, uint64(ChunkCheckInteger))
	target = append(target, integerBytes...)

	numberBytes := make([]byte, ChunkNumberSize)
	binary.LittleEndian.PutUint64(numberBytes, math.Float64bits(ChunkCheckNumber))
	target = append(target, numberBytes...)

	// 返回包含完整 Lua 5.3 header 的新切片。
	return target
}

// readByte 从 reader 读取一个 chunk 头部字节。
//
// fieldName 用于构造错误上下文，便于调用方定位损坏字段。
func readByte(reader io.Reader, fieldName string) (byte, error) {
	// 使用固定 1 字节缓冲读取，避免 binary.Read 对 byte 的反射开销。
	var buffer [1]byte
	if _, err := io.ReadFull(reader, buffer[:]); err != nil {
		// 读取失败时包装为 chunk header 错误，保留原始 IO 错误文本。
		return 0, chunkReadError(ErrInvalidChunkHeader, fieldName, err)
	}

	// 读取成功后返回唯一字节。
	return buffer[0], nil
}

// readExpectedSize 读取并校验 Lua 5.3 chunk 头部中的类型尺寸字段。
//
// fieldName 用于错误信息，expected 是当前纯 Go 实现支持的官方 64 位尺寸。
func readExpectedSize(reader io.Reader, fieldName string, expected byte) (byte, error) {
	// 先读取实际尺寸字节，再与当前实现支持的尺寸比较。
	actual, err := readByte(reader, fieldName+" size")
	if err != nil {
		// 尺寸字段缺失时，直接返回读取错误。
		return 0, err
	}
	if actual != expected {
		// 尺寸不匹配时拒绝继续读取，避免后续字段错位。
		return actual, fmt.Errorf("%w: %s size mismatch: got %d want %d", ErrInvalidChunkHeader, fieldName, actual, expected)
	}

	// 尺寸匹配时返回实际值，便于 header 记录完整元数据。
	return actual, nil
}
