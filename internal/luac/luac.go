// Package luac 提供纯 Go 版 Lua 5.3 编译与字节码调试工具能力。
//
// 本包只编排源码解析、codegen、binary chunk 编解码和调试文本输出，不调用官方 C 版 luac，
// 也不引入 CGO。cmd/gluac 和测试失败诊断可以复用这里的稳定入口。
package luac

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zing/go-lua-vm/bytecode"
	"github.com/zing/go-lua-vm/compiler/codegen"
	"github.com/zing/go-lua-vm/compiler/lexer"
	"github.com/zing/go-lua-vm/compiler/parser"
	"github.com/zing/go-lua-vm/internal/cli"
)

const (
	// ExitOK 表示 gluac 成功退出。
	ExitOK = 0
	// ExitFailure 表示 gluac 遇到参数、读取、编译、输出或反汇编错误。
	ExitFailure = 1
	// DefaultOutputPath 是 luac 兼容的默认 binary chunk 输出路径。
	DefaultOutputPath = "luac.out"
)

// Streams 保存 gluac 与宿主标准流之间的连接。
//
// Stdout 用于版本、反汇编和 trace 文本；Stderr 用于错误输出。当前 gluac 不读取 stdin，
// 源码和 binary chunk 均通过文件路径传入。
type Streams struct {
	// Stdout 是 gluac 标准输出流。
	Stdout io.Writer
	// Stderr 是 gluac 标准错误流。
	Stderr io.Writer
}

// FailureReporter 表示测试失败时可输出最小反汇编的最小接口。
//
// testing.TB 满足该接口；自定义测试框架也可通过实现 Failed、Cleanup 和 Logf 接入。
type FailureReporter interface {
	// Failed 返回当前测试是否已经失败。
	Failed() bool
	// Cleanup 注册测试结束时执行的回调。
	Cleanup(func())
	// Logf 记录格式化测试日志。
	Logf(format string, args ...any)
}

// Options 保存 gluac 参数解析结果。
//
// 输入文件当前只支持一个；ListLevel 对应 `-l` 出现次数；StripDebug 对应 `-s`；
// ParseOnly 对应 `-p`；输出路径由 `-o` 指定，未指定时使用 luac.out。
type Options struct {
	// InputPath 是待编译源码或待反汇编 binary chunk 路径。
	InputPath string
	// OutputPath 是 binary chunk 输出路径。
	OutputPath string
	// ListLevel 是反汇编详细程度，0 表示不列出。
	ListLevel int
	// ParseOnly 表示只做解析和编译检查，不写出 chunk。
	ParseOnly bool
	// StripDebug 表示写出 chunk 前剥离调试信息。
	StripDebug bool
	// Version 表示输出版本信息。
	Version bool
	// OpcodeTrace 表示输出静态 opcode trace。
	OpcodeTrace bool
	// StepTrace 表示输出静态 VM step trace。
	StepTrace bool
	// MinimalDisassembly 表示只输出测试失败定位用的最小反汇编。
	MinimalDisassembly bool
}

// ParseArgs 解析 gluac 命令行参数。
//
// args 不包含程序名；支持 `-l`、重复 `-l`、`-o <file>`、`-p`、`-s`、`-v`、
// `--opcode-trace`、`--step-trace` 和 `--minimal-disassembly`。当前只接受一个输入文件。
func ParseArgs(args []string) (Options, error) {
	// 从左到右解析参数，保持与 Lua 5.3 luac 参数覆盖顺序一致。
	options := Options{OutputPath: DefaultOutputPath}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "-l":
			// -l 每出现一次提升详细级别，-l -l 会额外输出 debug dump。
			options.ListLevel++
		case "-o":
			// -o 必须消费后续路径作为输出文件。
			index++
			if index >= len(args) {
				// 缺少输出路径时无法安全写文件，直接返回参数错误。
				return Options{}, fmt.Errorf("option -o requires an argument")
			}
			options.OutputPath = args[index]
		case "-p":
			// -p 只检查编译，不输出 binary chunk。
			options.ParseOnly = true
		case "-s":
			// -s 写出 chunk 前剥离调试信息。
			options.StripDebug = true
		case "-v":
			// -v 输出 Lua 5.3 兼容版本文本。
			options.Version = true
		case "--opcode-trace":
			// opcode trace 输出静态指令序列，便于观察 codegen 产物。
			options.OpcodeTrace = true
		case "--step-trace":
			// step trace 输出 VM 单步预览，实际执行接线后可扩展为动态 trace。
			options.StepTrace = true
		case "--minimal-disassembly":
			// 最小反汇编用于测试失败时输出较短上下文。
			options.MinimalDisassembly = true
		case "--":
			// -- 终止选项解析，后续必须正好提供一个输入路径。
			if index+1 >= len(args) {
				// 没有输入文件时无法执行编译或反汇编。
				return Options{}, fmt.Errorf("missing input file")
			}
			if index+2 < len(args) {
				// 首版 gluac 只支持单输入文件，避免多文件合并语义含糊。
				return Options{}, fmt.Errorf("multiple input files are not supported")
			}
			options.InputPath = args[index+1]
			return options, nil
		default:
			// 非选项参数按输入文件处理，未知选项直接报错。
			if strings.HasPrefix(argument, "-") {
				// 未支持选项不能静默忽略，避免输出与用户预期不一致。
				return Options{}, fmt.Errorf("unknown option: %s", argument)
			}
			if options.InputPath != "" {
				// 多输入文件当前没有合并 Proto 语义，直接拒绝。
				return Options{}, fmt.Errorf("multiple input files are not supported")
			}
			options.InputPath = argument
		}
	}
	if options.InputPath == "" && !options.Version {
		// 只输出版本时允许无输入；其他模式必须有输入文件。
		return Options{}, fmt.Errorf("missing input file")
	}
	return options, nil
}

// Main 执行 gluac 命令并返回进程退出码。
//
// args 不包含程序名；streams 的输出流为 nil 时会被 io.Discard 替代。错误会写入 stderr，
// 返回 ExitFailure，便于 cmd/gluac 保持极薄入口。
func Main(args []string, streams Streams) int {
	// 将错误返回流程集中在 Run，main.go 只负责 os.Exit。
	if err := Run(args, streams); err != nil {
		// 出错时向 stderr 输出一行稳定错误文本。
		_, _ = fmt.Fprintln(safeWriter(streams.Stderr), err)
		return ExitFailure
	}
	return ExitOK
}

// Run 解析并执行 gluac 命令。
//
// 当前执行顺序为版本输出、读取输入、加载或编译 Proto、按选项输出调试文本、按选项写出
// binary chunk。binary chunk 输入在不写输出时可直接用于反汇编和 trace。
func Run(args []string, streams Streams) error {
	// 先解析参数，参数错误无需读取文件。
	options, err := ParseArgs(args)
	if err != nil {
		// 参数解析失败时直接返回，避免产生任何输出文件。
		return err
	}
	stdout := safeWriter(streams.Stdout)
	if options.Version {
		// 版本文本复用 glua 的 Lua 5.3 兼容版本输出。
		_, _ = fmt.Fprintln(stdout, cli.VersionText)
		if options.InputPath == "" {
			// 仅请求版本时已经完成全部工作。
			return nil
		}
	}

	inputBytes, err := os.ReadFile(options.InputPath)
	if err != nil {
		// 文件读取失败保留 os.PathError，调用方可识别权限或不存在。
		return err
	}
	proto, sourceInput, err := ProtoFromBytes(inputBytes, "@"+options.InputPath)
	if err != nil {
		// 源码编译或 binary chunk 加载失败时不写出文件。
		return err
	}

	if err := writeRequestedReports(stdout, proto, sourceInput, inputBytes, "@"+options.InputPath, options); err != nil {
		// 报告输出当前只可能来自 writer 错误，返回给调用方处理。
		return err
	}
	if shouldWriteChunk(options) {
		// 写出前按 -s 需求选择是否复制并剥离调试信息。
		outputProto := proto
		if options.StripDebug {
			// strip 不能修改原始 Proto，避免后续同轮报告看到被剥离的数据。
			outputProto = StripDebug(proto)
		}
		if err := os.WriteFile(options.OutputPath, bytecode.DumpBinaryChunk(outputProto), 0o644); err != nil {
			// 输出文件写入失败时返回底层路径错误。
			return err
		}
	}
	return nil
}

// CompileSource 将 Lua 源码编译为 Lua 5.3 Proto。
//
// source 是完整 Lua chunk；chunkName 写入 Proto.Source，建议源码文件使用 `@path`。返回错误
// 保留 parser 或 codegen 的原始语义，便于 CLI 与嵌入方定位。
func CompileSource(source string, chunkName string) (*bytecode.Proto, error) {
	// 先解析源码为 AST，再交给 codegen 生成 Proto。
	chunkParser := parser.New(source)
	chunk, err := chunkParser.ParseChunk()
	if err != nil {
		// 语法或基础语义错误直接返回，不进入 codegen。
		return nil, err
	}
	proto, err := codegen.CompileChunk(chunk, chunkName)
	if err != nil {
		// codegen 错误说明当前 AST 不可生成有效字节码。
		return nil, err
	}
	return proto, nil
}

// DumpSource 将 Lua 源码编译为 Lua 5.3 binary chunk。
//
// stripDebug 为 true 时会剥离 lineinfo、local var 和 upvalue 名称；返回字节可直接写入
// `.luac` 文件或交给 LoadBinaryChunk 重新读取。
func DumpSource(source string, chunkName string, stripDebug bool) ([]byte, error) {
	// 先编译出 Proto，确保 dump 只处理有效编译产物。
	proto, err := CompileSource(source, chunkName)
	if err != nil {
		// 编译失败时不产生部分 chunk。
		return nil, err
	}
	if stripDebug {
		// strip 模式使用深拷贝，避免调用方后续继续使用原始调试信息时被污染。
		proto = StripDebug(proto)
	}
	return bytecode.DumpBinaryChunk(proto), nil
}

// ProtoFromBytes 从输入字节加载或编译 Proto。
//
// 输入以 Lua chunk 签名开头时按 binary chunk 读取，否则按源码编译。返回值 sourceInput
// 表示是否按源码处理，调用方可据此决定最小反汇编是否需要重新展示源码编译错误。
func ProtoFromBytes(input []byte, chunkName string) (*bytecode.Proto, bool, error) {
	// 通过签名判断输入形态，避免为 CLI 额外增加模式参数。
	if bytes.HasPrefix(input, []byte(bytecode.ChunkSignature)) {
		// binary chunk 直接走 loader，不重新编译源码。
		proto, err := bytecode.LoadBinaryChunk(bytes.NewReader(input))
		return proto, false, err
	}
	source := lexer.StripInitialShebang(string(input))
	proto, err := CompileSource(source, chunkName)
	return proto, true, err
}

// DisassembleChunk 反汇编 Lua 5.3 binary chunk。
//
// input 必须是完整 binary chunk；返回文本使用项目内稳定格式，不承诺逐字节对齐官方 luac -l。
func DisassembleChunk(input []byte) (string, error) {
	// 先加载 Proto，确保坏 header 或坏 body 能明确返回错误。
	proto, err := bytecode.LoadBinaryChunk(bytes.NewReader(input))
	if err != nil {
		// chunk 无效时不能输出误导性的部分反汇编。
		return "", err
	}
	return bytecode.DisassembleProto(proto), nil
}

// DebugDumpProto 返回 Proto 调试信息文本。
//
// 输出包含顶层和子 Proto 的调试信息摘要，重点展示 lineinfo、local var 与 upvalue 名称，
// 用于 `-l -l` 和开发期定位。
func DebugDumpProto(proto *bytecode.Proto) string {
	// 复用 debug dump 递归输出，保持顶层和子函数格式一致。
	var builder strings.Builder
	writeDebugDump(&builder, proto, 0, "main")
	return builder.String()
}

// OpcodeTraceProto 返回 Proto 的静态 opcode trace。
//
// 返回值按 pc 顺序列出 opcode 名称、原始指令字和行号；该模式不执行 VM，因此不会产生副作用。
func OpcodeTraceProto(proto *bytecode.Proto) string {
	// 静态 trace 只读取 Proto.Code，适合 codegen 和 binary chunk 调试。
	var builder strings.Builder
	writeOpcodeTrace(&builder, proto, 0, "main")
	return builder.String()
}

// StepTraceProto 返回 Proto 的静态 VM step trace 预览。
//
// 当前 VM 动态 trace 尚未接入 CLI，本函数按 pc 顺序输出每一步将执行的指令和寄存器窗口摘要，
// 为后续动态执行 trace 保留稳定文本入口。
func StepTraceProto(proto *bytecode.Proto) string {
	// 静态 step trace 以 Proto 的 maxstack 和 pc 顺序模拟“将要执行”的单步视图。
	var builder strings.Builder
	writeStepTrace(&builder, proto, 0, "main")
	return builder.String()
}

// MinimalDisassembly 返回测试失败定位用的最小反汇编。
//
// source 是完整 Lua chunk；chunkName 写入 Proto.Source。编译失败时返回错误文本；成功时只输出
// pc、行号和 opcode，避免测试日志被常量表和调试表淹没。
func MinimalDisassembly(source string, chunkName string) string {
	// 先尝试编译源码，成功后输出紧凑指令列表。
	proto, err := CompileSource(source, chunkName)
	if err != nil {
		// 编译失败时返回错误文本，测试日志仍可定位失败阶段。
		return fmt.Sprintf("compile error: %v\n", err)
	}
	return MinimalDisassemblyProto(proto)
}

// MinimalDisassemblyProto 返回 Proto 的最小反汇编。
//
// 输出仅包含函数标签、pc、line 和 opcode，适合在测试失败时作为短诊断附加信息。
func MinimalDisassemblyProto(proto *bytecode.Proto) string {
	// 使用递归写出子 Proto，确保闭包相关失败也能看到子函数字节码。
	var builder strings.Builder
	writeMinimalDisassembly(&builder, proto, 0, "main")
	return builder.String()
}

// AttachMinimalDisassemblyOnFailure 在测试失败时输出源码的最小反汇编。
//
// reporter 通常传入 `t`；source 是完整 Lua chunk；chunkName 写入 Proto.Source。测试成功时不输出；
// 测试失败时通过 Cleanup 延迟输出，避免正常用例日志噪声。
func AttachMinimalDisassemblyOnFailure(reporter FailureReporter, source string, chunkName string) {
	// 通过 Cleanup 在测试最终状态确定后再决定是否输出诊断。
	reporter.Cleanup(func() {
		// 只有测试失败时才输出最小反汇编，避免污染成功日志。
		if !reporter.Failed() {
			// 成功测试不需要任何诊断输出。
			return
		}
		reporter.Logf("minimal lua disassembly for %s:\n%s", chunkName, MinimalDisassembly(source, chunkName))
	})
}

// AttachMinimalProtoDisassemblyOnFailure 在测试失败时输出 Proto 的最小反汇编。
//
// reporter 通常传入 `t`；proto 必须非 nil。测试成功时不输出；测试失败时输出当前 Proto 的
// pc、line 和 opcode，便于定位 codegen 或 VM 断言失败。
func AttachMinimalProtoDisassemblyOnFailure(reporter FailureReporter, proto *bytecode.Proto) {
	// 通过 Cleanup 在测试最终状态确定后再决定是否输出诊断。
	reporter.Cleanup(func() {
		// 只有测试失败时才输出最小反汇编，避免污染成功日志。
		if !reporter.Failed() {
			// 成功测试不需要任何诊断输出。
			return
		}
		reporter.Logf("minimal lua proto disassembly:\n%s", MinimalDisassemblyProto(proto))
	})
}

// StripDebug 深拷贝 Proto 并剥离调试信息。
//
// 返回值会保留 code、constant、child proto 和 upvalue 捕获位置，但清空 lineinfo、local var
// 和 upvalue 名称，语义对齐 luac -s 的首版能力。
func StripDebug(proto *bytecode.Proto) *bytecode.Proto {
	// 统一委托 bytecode 层，保证 luac -s 与 string.dump(fn, true) 使用同一剥离语义。
	return bytecode.StripDebug(proto)
}

// shouldWriteChunk 判断当前选项是否需要写出 binary chunk。
//
// 只解析、只列出和 trace 模式默认不写输出；显式 `-o` 后续可扩展为强制写出，目前保持首版简单语义。
func shouldWriteChunk(options Options) bool {
	// 调试输出模式默认不生成文件，避免用户只查看反汇编时意外覆盖 luac.out。
	return !options.ParseOnly && options.ListLevel == 0 && !options.OpcodeTrace && !options.StepTrace && !options.MinimalDisassembly
}

// writeRequestedReports 按选项写出反汇编、debug dump 和 trace 报告。
//
// writer 必须非 nil；sourceInput 表示 inputBytes 是否为源码。返回错误只来自底层 writer。
func writeRequestedReports(writer io.Writer, proto *bytecode.Proto, sourceInput bool, inputBytes []byte, chunkName string, options Options) error {
	// 按选项顺序输出报告，保持同一命令的文本稳定。
	if options.ListLevel > 0 {
		// -l 输出完整反汇编。
		if _, err := fmt.Fprint(writer, bytecode.DisassembleProto(proto)); err != nil {
			// 输出失败时停止后续报告，避免交错写入。
			return err
		}
	}
	if options.ListLevel > 1 {
		// -l -l 额外输出 debug dump。
		if _, err := fmt.Fprint(writer, DebugDumpProto(proto)); err != nil {
			// 输出失败时停止后续报告，避免调用方收到不完整组合文本。
			return err
		}
	}
	if options.OpcodeTrace {
		// opcode trace 输出静态 opcode 序列。
		if _, err := fmt.Fprint(writer, OpcodeTraceProto(proto)); err != nil {
			// 输出失败时直接返回。
			return err
		}
	}
	if options.StepTrace {
		// step trace 输出静态 VM 单步预览。
		if _, err := fmt.Fprint(writer, StepTraceProto(proto)); err != nil {
			// 输出失败时直接返回。
			return err
		}
	}
	if options.MinimalDisassembly {
		// 最小反汇编优先复用已加载 Proto，源码输入也可重新编译以暴露编译错误文本。
		text := MinimalDisassemblyProto(proto)
		if sourceInput {
			// 源码输入时复用 public helper，确保测试失败 helper 与 CLI 语义一致。
			text = MinimalDisassembly(string(inputBytes), chunkName)
		}
		if _, err := fmt.Fprint(writer, text); err != nil {
			// 输出失败时直接返回。
			return err
		}
	}
	return nil
}

// writeDebugDump 写入单个 Proto 及其子 Proto 的调试信息。
//
// depth 控制缩进层级；label 标识当前 Proto 在父结构中的位置。
func writeDebugDump(builder *strings.Builder, proto *bytecode.Proto, depth int, label string) {
	// 先写当前 Proto 摘要，再分别写 lineinfo、local 和 upvalue 调试表。
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(builder, "%sdebug %s source=%q lines=%d..%d code=%d constants=%d children=%d\n", indent, label, proto.Source, proto.LineDefined, proto.LastLineDefined, len(proto.Code), len(proto.Constants), len(proto.Protos))
	fmt.Fprintf(builder, "%s  lineinfo=%v\n", indent, proto.LineInfo)
	for index, local := range proto.LocalVars {
		// 局部变量按 Proto.LocalVars 顺序输出，便于定位生命周期。
		fmt.Fprintf(builder, "%s  local[%d] name=%q pc=[%d,%d)\n", indent, index, local.Name, local.StartPC, local.EndPC)
	}
	for index, upvalue := range proto.Upvalues {
		// upvalue 输出捕获位置和调试名称，便于排查闭包捕获。
		fmt.Fprintf(builder, "%s  upvalue[%d] name=%q instack=%v index=%d\n", indent, index, upvalue.Name, upvalue.InStack, upvalue.Index)
	}
	for index, child := range proto.Protos {
		// 子 Proto 递归输出，保持父子层级可读。
		writeDebugDump(builder, child, depth+1, fmt.Sprintf("child[%d]", index))
	}
}

// writeOpcodeTrace 写入单个 Proto 的 opcode trace。
//
// depth 控制缩进层级；label 标识当前 Proto 在父结构中的位置。
func writeOpcodeTrace(builder *strings.Builder, proto *bytecode.Proto, depth int, label string) {
	// 按 pc 顺序输出每条指令的 opcode 和原始 32 位指令字。
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(builder, "%sopcode-trace %s code=%d\n", indent, label, len(proto.Code))
	for pc, instruction := range proto.Code {
		// lineinfo 缺失时输出 -1，避免 trace 列数不稳定。
		line := -1
		if pc < len(proto.LineInfo) {
			// 有行号信息时写入源码行号。
			line = proto.LineInfo[pc]
		}
		fmt.Fprintf(builder, "%s  pc=%04d line=%d op=%s raw=0x%08x\n", indent, pc, line, instruction.OpCode().Name(), uint32(instruction))
	}
	for index, child := range proto.Protos {
		// 子 Proto 保持同样格式递归输出。
		writeOpcodeTrace(builder, child, depth+1, fmt.Sprintf("child[%d]", index))
	}
}

// writeStepTrace 写入单个 Proto 的静态 VM step trace 预览。
//
// depth 控制缩进层级；label 标识当前 Proto 在父结构中的位置。
func writeStepTrace(builder *strings.Builder, proto *bytecode.Proto, depth int, label string) {
	// 当前阶段只做静态预览，不执行 VM，避免 trace 本身改变运行时状态。
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(builder, "%sstep-trace %s maxstack=%d code=%d\n", indent, label, proto.MaxStackSize, len(proto.Code))
	for pc, instruction := range proto.Code {
		// 每行展示 step 序号、pc、opcode 和字段，便于人工对照 VM Step。
		fmt.Fprintf(builder, "%s  step=%04d pc=%04d op=%s A=%d B=%d C=%d Bx=%d sBx=%d\n", indent, pc, pc, instruction.OpCode().Name(), instruction.A(), instruction.B(), instruction.C(), instruction.Bx(), instruction.SBx())
	}
	for index, child := range proto.Protos {
		// 子 Proto 递归输出静态 step，覆盖闭包字节码。
		writeStepTrace(builder, child, depth+1, fmt.Sprintf("child[%d]", index))
	}
}

// writeMinimalDisassembly 写入单个 Proto 的最小反汇编。
//
// depth 控制缩进层级；label 标识当前 Proto 在父结构中的位置。
func writeMinimalDisassembly(builder *strings.Builder, proto *bytecode.Proto, depth int, label string) {
	// 只输出最短必要字段，适合失败测试日志。
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(builder, "%s%s code=%d\n", indent, label, len(proto.Code))
	for pc, instruction := range proto.Code {
		// lineinfo 缺失时使用 -，保持输出紧凑。
		line := "-"
		if pc < len(proto.LineInfo) {
			// 有行号时输出十进制行号。
			line = fmt.Sprintf("%d", proto.LineInfo[pc])
		}
		fmt.Fprintf(builder, "%s  [%04d] line=%s %s\n", indent, pc, line, instruction.OpCode().Name())
	}
	for index, child := range proto.Protos {
		// 子 Proto 递归输出，避免遗漏闭包失败上下文。
		writeMinimalDisassembly(builder, child, depth+1, fmt.Sprintf("child[%d]", index))
	}
}

// safeWriter 返回可安全写入的 writer。
//
// writer 为 nil 时返回 io.Discard，避免 CLI 单测或嵌入调用发生 nil 指针。
func safeWriter(writer io.Writer) io.Writer {
	// nil writer 代表调用方不关心该输出流。
	if writer == nil {
		// 丢弃输出比 panic 更符合命令入口的健壮性。
		return io.Discard
	}
	return writer
}
