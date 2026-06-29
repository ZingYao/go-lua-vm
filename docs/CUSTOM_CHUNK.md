# 自定义加密 chunk 接入规划

本文档描述自定义加密 chunk 的规划接口、加载链路、发布链路、最小可执行 Demo 和常见坑点。该能力只接入 chunk 加载层和打包层，VM 核心仍只执行标准 Lua 5.3 Proto/bytecode。

## 目标与非目标

自定义加密 chunk 的目标是允许 Go 嵌入用户在发布侧生成私有封包，并在运行侧通过显式注册的 decoder 解包后执行。该能力只改变 chunk 的输入形态，不改变 Lua 5.3 指令集、Proto 结构和 VM 执行模型。

设计边界：

- 发布侧可以通过 `ChunkEncoder` 把标准 Lua 5.3 binary chunk 包装成私有格式。
- 运行侧可以通过 `ChunkDecoder` 把私有格式还原为标准 Lua 5.3 binary chunk。
- VM 只接收统一 parser 校验后的标准 Proto，不感知密钥、封包格式或加密算法。
- decoder 不能直接返回 `Proto`，也不能绕过 header、版本、指令、常量表、嵌套深度等标准校验。
- 该能力不承诺防止运行时调试、内存 dump 或解密后 Proto 抽取。

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

## 加载流程

chunk 加载入口必须保持单一路径，避免标准 chunk 与私有 chunk 进入两套 parser：

1. 读取输入字节，并先检查输入大小是否超过宿主配置的最大 chunk 字节数。
2. 若输入以 Lua 5.3 官方 binary chunk signature 开头，直接进入标准 chunk parser。
3. 若不是标准 signature，则按注册顺序调用 `ChunkDecoder.Match` 做快速识别。
4. 首个 `Match` 命中的 decoder 负责执行 `Decode`，并返回标准 Lua 5.3 binary chunk 字节。
5. 解码结果再次执行最大输出字节数校验，防止小输入膨胀为超大 chunk。
6. 解码后的标准 chunk 进入同一个 header、版本、格式、指令、常量、upvalue、locvar 和嵌套 Proto 校验流程。
7. 统一 parser 产出 Proto 后，VM 才能执行。

该流程保证标准 chunk 与私有 chunk 的最终校验一致。私有 decoder 只负责“还原字节”，不负责“信任字节”。

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

对外注册入口优先放在稳定的 `lua` 嵌入 API 中，不要求用户依赖 `bytecode` 内部包。规划形态如下：

```go
state := lua.NewState(lua.Options{
    ChunkDecoders: []lua.ChunkDecoder{myDecoder},
})
```

或通过 option 风格注册：

```go
state := lua.NewState(lua.WithChunkDecoder(myDecoder))
```

API 边界：

- `ChunkDecoder` 与 `ChunkEncoder` 属于 `lua` 包的稳定嵌入 API。
- `bytecode` 包继续只处理标准 Lua 5.3 binary chunk 读写，不直接依赖私有加密格式。
- 多个 decoder 的匹配顺序由注册顺序决定。
- 标准 Lua chunk decoder 默认保留，是否允许禁用标准 chunk 需要单独配置，默认不禁用，避免破坏 Lua 5.3 兼容性。
- encoder 属于发布侧工具能力，可以由 `gluac` 后续开关或宿主构建系统调用，但不进入 VM 热路径。

## Decoder 匹配规则

decoder 匹配必须确定、可解释，并避免多个 decoder 同时争抢同一输入：

- 标准 Lua 5.3 binary chunk 优先按官方 signature 识别，默认不进入私有 decoder 链。
- 私有 decoder 按注册顺序调用 `Match`，首个返回 `true` 的 decoder 独占本次解码。
- 命中后不再调用后续 decoder，避免同一输入被多种格式重复尝试导致错误语义不稳定。
- `Match` 必须是轻量、无副作用、可重复调用的方法，只允许检查 magic、版本、头部长度等小范围字节。
- `Match` 不得消耗 reader 状态、不得修改输入切片、不得记录明文或密钥相关日志。
- 若多个 decoder 需要兼容同一 magic 的不同版本，应由同一个 decoder 在 `Decode` 内按版本分发，或由宿主按更具体版本优先注册。
- 禁用标准 Lua chunk 的能力不作为默认行为；如果未来提供 `DisableStandardChunk`，必须在 API 文档和 CLI 帮助中明确这会破坏 Lua 5.3 默认兼容性。

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

推荐封包头最少包含：

- 固定 magic：区分标准 Lua chunk 与私有格式。
- 格式版本：用于 decoder 做兼容分支。
- encoder 名称或编号：用于诊断和灰度迁移。
- payload 长度：用于提前拒绝截断或膨胀输入。
- 完整性校验：例如 MAC、签名或强校验和；生产环境不应只依赖弱 checksum。

encoder 输出不能伪装成官方 Lua binary chunk signature。私有 chunk 必须先由私有 decoder 识别和还原，再进入标准 parser。

## 安全边界

该能力运行在宿主进程内，decoder/encoder 代码与宿主同权限执行，因此默认策略必须保守：

- 所有 `Match`、`Decode`、`Encode` 都必须接收 `context.Context`，并在循环、I/O、解密和大内存分配前检查取消信号。
- 加载入口必须同时限制原始输入大小和解码后标准 chunk 大小，防止压缩炸弹或恶意膨胀。
- decoder 不应访问网络、环境变量、进程命令或任意外部文件；如宿主确需这样做，应由宿主自己的 decoder 明确承担风险。
- 错误信息不得包含密钥、解密后明文片段、完整输入路径、宿主内部目录结构或加密参数细节。
- 日志只记录 decoder 名称、格式版本、输入/输出字节数、错误分类和 request/correlation id 等非敏感信息。
- 密钥来源由宿主管理，核心库不提供全局密钥变量，也不在 CLI 默认参数中接收明文密钥。
- decoder 返回的字节必须复制或视为只读，不能依赖后续调用仍持有可变输入缓冲。

推荐默认限制由 `lua.Options` 承载，例如最大输入 chunk 字节数、最大解码输出字节数和是否允许私有 decoder。具体字段名可在实现阶段按现有 Options 风格确定。

## 异常语义

错误分类需要让 CLI、Go 嵌入调用和测试能稳定判断失败阶段：

- 输入既不是标准 Lua chunk，也未命中任何 decoder：返回“未知 chunk 格式”类错误。
- decoder `Match` 命中但 `Decode` 失败：返回“chunk 解码失败”类错误，并附带 decoder 名称，不透出敏感底层细节。
- decoder 返回空字节、超大字节或不以标准 Lua signature 开头：返回“非法解码结果”类错误。
- 解码结果进入标准 parser 后失败：统一返回 bytecode 校验错误，例如 header、版本、格式、截断、指令或常量表错误。
- `context.Context` 取消或超时：优先返回 context 原始错误，方便宿主用 `errors.Is(err, context.Canceled)` 或 `errors.Is(err, context.DeadlineExceeded)` 判断。
- 多 decoder 均未命中时不应把每个 decoder 的内部细节拼接到用户错误中，避免泄露格式探测策略。

Go API 层应保留可 `errors.Is` / `errors.As` 判断的结构化错误；CLI 层再把它格式化为 Lua 风格加载错误。

## CLI 策略

`glua` 默认只保证标准 Lua 5.3 chunk 与源码执行兼容，不自动加载任意外部 decoder。私有加密 chunk 的首选接入方式是 Go 嵌入 API 显式注册。

CLI 策略建议：

- `glua script.lua`、`glua chunk.luac` 保持现有标准 Lua 行为。
- 私有 chunk 默认不通过文件扩展名自动识别，也不扫描系统目录加载插件。
- 若未来提供 CLI 示例 decoder，应通过显式开关开启，例如 `--chunk-decoder=xor-demo`，并标注只用于演示。
- CLI 不接受裸明文密钥作为推荐参数；如确需示例，优先使用环境变量、key file 或宿主回调，但这些都不作为首版默认能力。
- `gluac` 可以优先支持输出标准 binary chunk；私有 encoder 作为发布侧后续扩展，不影响 VM 和标准 chunk 兼容。
- CLI 帮助中必须明确私有加密 chunk 是“封包/混淆接入点”，不是安全沙箱或防破解承诺。

## 能力边界

该方案只提升通用工具直接读取和反编译发布物的门槛，不提供强 DRM 或可信执行环境：

- 攻击者若能调试进程，可能在 decoder 返回标准 chunk 后 dump 字节。
- 攻击者若能修改宿主程序，可能替换 decoder、hook VM 加载入口或拦截 Proto。
- 如果密钥随二进制或资源包发布，密钥仍可能被静态分析或运行时提取。
- 完整性校验只能发现损坏或篡改，不能单独隐藏内容；隐藏内容必须依赖加密算法和密钥管理。
- VM 层仍按标准 Lua 5.3 Proto 执行，因此 debug、traceback、hook、string.dump 等能力是否开放，需要由宿主权限策略另行控制。
- 本能力不替代代码授权、许可证校验、远程 attestation、操作系统级沙箱或硬件安全模块。

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

## 测试计划

实现阶段至少补齐以下测试，确保私有 chunk 接入不会破坏标准 Lua 5.3 chunk 行为：

| 测试场景 | 目标 | 建议位置 |
| --- | --- | --- |
| 标准源码和标准 binary chunk | 未注册私有 decoder 时，标准源码、`string.dump`、`gluac` 输出仍可加载执行 | `lua` API 测试与 CLI golden |
| 加密 chunk roundtrip | 标准 chunk 经 demo encoder 封包后，注册 decoder 可还原并执行，stdout/返回值与标准 chunk 一致 | `lua` API 集成测试 |
| encoder/decoder roundtrip | `Encode` 后 `Match` 命中，`Decode` 输出与原始标准 chunk 字节一致 | `lua` 或独立 codec 单测 |
| 未知格式 | 非标准 signature 且无 decoder 命中时，返回未知 chunk 格式错误 | `lua` API 单测 |
| decoder 错误 | `Match` 命中但 `Decode` 返回错误时，错误分类为解码失败并包含 decoder 名称 | `lua` API 单测 |
| 非法解密结果 | decoder 返回空字节、随机字节、非 Lua signature 或截断 chunk 时，统一进入非法解码结果或 bytecode 校验错误 | `bytecode`/`lua` 边界测试 |
| 超大解密结果 | 小输入解码为超过限制的大输出时，被最大输出字节数拦截，不进入 parser | `lua` API 单测 |
| context 取消 | `Match`、`Decode` 或 `Encode` 遇到取消时返回 context 错误，调用方可用 `errors.Is` 判断 | `lua` API 单测 |
| 多 decoder 顺序 | 多个 decoder 注册时按顺序匹配，首个命中后不调用后续 decoder | `lua` API 单测 |
| 标准 chunk 优先级 | 标准 Lua signature 默认不进入私有 decoder 链，避免私有 decoder 抢占官方 chunk | `lua` API 单测 |
| 错误脱敏 | decoder 失败时不泄露密钥、明文片段、宿主内部路径或加密参数 | `lua` API 单测 |
| CLI 默认策略 | `glua` 默认不自动加载私有 decoder；标准脚本和标准 chunk 行为不变 | CLI golden |

测试数据建议使用极小 Lua 脚本生成标准 chunk，例如 `return 1 + 2`，再由 demo encoder 生成私有封包。测试不应引入真实密钥、网络访问、外部服务或 CGO。

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
