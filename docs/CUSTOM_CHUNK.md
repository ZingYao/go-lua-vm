# 自定义加密 chunk 接入规划

本文档描述自定义加密 chunk 的规划接口、加载链路、发布链路、最小可执行 Demo 和常见坑点。该能力只接入 chunk 加载层和打包层，VM 核心仍只执行标准 Lua 5.3 Proto/bytecode。

## 是否需要同时实现 Encoder 和 Decoder

运行时执行私有加密 chunk 只需要 `ChunkDecoder`。它负责把私有格式解密或解包为标准 Lua 5.3 binary chunk 字节，再交给统一 parser 和校验流程。

如果用户还要生产私有加密 chunk，就需要同时实现 `ChunkEncoder`。它属于发布、构建或打包侧能力，负责把标准 Lua 5.3 binary chunk 包装成私有格式。

推荐职责边界：

```text
构建/发布阶段
  Lua 源码
    -> 标准 Lua 5.3 binary chunk
    -> ChunkEncoder.Encode
    -> 私有加密 chunk

运行阶段
  私有加密 chunk
    -> ChunkDecoder.Match
    -> ChunkDecoder.Decode
    -> 标准 Lua 5.3 binary chunk
    -> 统一 chunk parser 和校验
    -> VM 执行 Proto
```

## 规划接口

规划中的接口形态如下。最终实现时可以根据项目 API 风格调整命名，但职责不应改变。

```go
type ChunkDecoder interface {
    Name() string
    Match(ctx context.Context, input []byte) bool
    Decode(ctx context.Context, input []byte) ([]byte, error)
}

type ChunkEncoder interface {
    Name() string
    Encode(ctx context.Context, standardChunk []byte) ([]byte, error)
}
```

`ChunkDecoder` 约束：

- `Match` 只做快速格式识别，例如检查 magic header、版本号或封包头。
- `Decode` 必须返回标准 Lua 5.3 binary chunk 字节，不直接返回内部 `Proto`。
- `Decode` 必须响应 `context.Context` 取消。
- `Decode` 必须限制输入和输出大小，避免恶意输入造成内存消耗。
- 错误信息不得包含密钥、明文片段或宿主内部路径。

`ChunkEncoder` 约束：

- `Encode` 输入必须是标准 Lua 5.3 binary chunk 字节。
- 输出必须包含稳定 magic header 和版本号，便于 decoder 精确识别。
- 加密或封包格式需要携带完整性校验，避免随机损坏数据进入 chunk parser。
- 发布工具应记录 encoder 名称、版本和参数来源，但不能记录密钥明文。

## 最小可执行 Demo

下面是一个独立 Go 示例，用 XOR 和 magic header 演示 encoder/decoder 的最小闭环。XOR 只用于说明接口形态，不能用于生产加密。

将代码保存为 `demo_custom_chunk.go` 后运行：

```bash
go run demo_custom_chunk.go
```

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

var demoMagic = []byte{'G', 'L', 'C', 'H', 1}

type ChunkDecoder interface {
	Name() string
	Match(ctx context.Context, input []byte) bool
	Decode(ctx context.Context, input []byte) ([]byte, error)
}

type ChunkEncoder interface {
	Name() string
	Encode(ctx context.Context, standardChunk []byte) ([]byte, error)
}

type xorChunkCodec struct {
	key byte
}

func (c xorChunkCodec) Name() string {
	return "xor-demo-v1"
}

func (c xorChunkCodec) Match(ctx context.Context, input []byte) bool {
	select {
	case <-ctx.Done():
		return false
	default:
		return bytes.HasPrefix(input, demoMagic)
	}
}

func (c xorChunkCodec) Encode(ctx context.Context, standardChunk []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(standardChunk) == 0 {
		return nil, errors.New("empty standard chunk")
	}
	out := make([]byte, 0, len(demoMagic)+len(standardChunk))
	out = append(out, demoMagic...)
	for _, b := range standardChunk {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, b^c.key)
	}
	return out, nil
}

func (c xorChunkCodec) Decode(ctx context.Context, input []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(input, demoMagic) {
		return nil, errors.New("unsupported chunk format")
	}
	payload := input[len(demoMagic):]
	if len(payload) == 0 {
		return nil, errors.New("empty encrypted payload")
	}
	out := make([]byte, 0, len(payload))
	for _, b := range payload {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, b^c.key)
	}
	return out, nil
}

func main() {
	ctx := context.Background()
	codec := xorChunkCodec{key: 0x5a}

	standardChunk := []byte("\x1bLua\x53\x00demo-standard-chunk")
	encryptedChunk, err := codec.Encode(ctx, standardChunk)
	if err != nil {
		panic(err)
	}
	if !codec.Match(ctx, encryptedChunk) {
		panic("encoded chunk does not match decoder")
	}
	decodedChunk, err := codec.Decode(ctx, encryptedChunk)
	if err != nil {
		panic(err)
	}
	if !bytes.Equal(decodedChunk, standardChunk) {
		panic("decoded chunk differs from original")
	}

	fmt.Printf("%s roundtrip ok, encrypted=%d bytes, decoded=%d bytes\n", codec.Name(), len(encryptedChunk), len(decodedChunk))
}
```

真实接入项目后，`decodedChunk` 不能直接执行。它必须继续进入标准 Lua 5.3 chunk parser，完成 header、版本、整数/浮点格式、指令边界、常量表和函数嵌套深度校验后，才能生成 `Proto` 给 VM 执行。

## README 中的引用方式

README 只保留简短入口，避免把规划细节、Demo 和安全说明堆在首页：

```markdown
自定义加密 chunk 的 encoder/decoder 接入方式、最小可执行 Demo 与避坑点见 [docs/CUSTOM_CHUNK.md](docs/CUSTOM_CHUNK.md)。
```

## 常见坑点

- 不要让 decoder 直接返回内部 `Proto`。第一阶段只允许返回标准 Lua 5.3 binary chunk 字节，避免绕过统一校验。
- 不要修改 VM opcode 来承载加密逻辑。加密属于加载层和发布层能力，修改指令集会破坏 Lua 5.3 兼容性。
- 不要依赖 `luac -l` 的文本反汇编作为 encoder 输入。官方 `lua` 执行的是 binary chunk，不是文本汇编。
- 不要把密钥硬编码在 Lua 脚本里；如果密钥随包一起发布，只能增加静态分析成本，不能提供强保护。
- 不要默认加载任意外部 decoder 或动态库。Go 嵌入场景应通过显式注册接入，CLI 是否提供内置示例 decoder 需要单独评估。
- 不要忽略完整性校验。缺少校验会让随机损坏数据进入解密和 chunk parser，错误定位会非常困难。
- 不要把该能力描述为防破解。攻击者如果能调试进程，仍可能在解密后 dump 出标准 chunk 或 Proto；该设计目标是提高通用工具直接反编译的门槛。
