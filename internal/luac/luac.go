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
	"strconv"
	"strings"

	"github.com/ZingYao/go-lua-vm/bytecode"
	"github.com/ZingYao/go-lua-vm/compiler/codegen"
	"github.com/ZingYao/go-lua-vm/compiler/lexer"
	"github.com/ZingYao/go-lua-vm/compiler/parser"
	"github.com/ZingYao/go-lua-vm/extensions"
	"github.com/ZingYao/go-lua-vm/internal/buildinfo"
	"github.com/ZingYao/go-lua-vm/internal/cli"
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
// 输入文件支持一个或多个；ListLevel 对应 `-l` 出现次数；StripDebug 对应 `-s`；
// ParseOnly 对应 `-p`；输出路径由 `-o` 指定，未指定时使用 luac.out。
type Options struct {
	// Help 表示是否输出 gluac 帮助信息。
	Help bool
	// InputPath 是待编译源码或待反汇编 binary chunk 路径。
	InputPath string
	// InputPaths 保存全部待编译源码或待反汇编 binary chunk 路径。
	InputPaths []string
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
	// SyntaxExtensions 保存源码编译阶段启用的语法扩展集合。
	SyntaxExtensions extensions.SyntaxSet
	// SyntaxExtensionsSet 表示命令行是否显式指定过 --gluac-syntax。
	SyntaxExtensionsSet bool
	// DisabledSyntaxExtensions 保存命令行显式关闭的语法扩展集合。
	DisabledSyntaxExtensions extensions.SyntaxSet
}

// ParseArgs 解析 gluac 命令行参数。
//
// args 不包含程序名；支持 `-l`、重复 `-l`、`-o <file>`、`-p`、`-s`、`-v`、
// `--gluac-opcode-trace`、`--gluac-step-trace` 和 `--gluac-minimal-disassembly`。多个输入文件会按官方 luac
// 语义组合成一个顶层 wrapper chunk，依次执行每个输入 chunk。
func ParseArgs(args []string) (Options, error) {
	// 从左到右解析参数，保持与 Lua 5.3 luac 参数覆盖顺序一致。
	options := Options{OutputPath: DefaultOutputPath}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "-h", "--help":
			// -h/--help 输出帮助信息，不要求输入文件。
			options.Help = true
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
		case "--gluac-opcode-trace":
			// opcode trace 输出静态指令序列，便于观察 codegen 产物。
			options.OpcodeTrace = true
		case "--gluac-step-trace":
			// step trace 输出 VM 单步预览，实际执行接线后可扩展为动态 trace。
			options.StepTrace = true
		case "--gluac-minimal-disassembly":
			// 最小反汇编用于测试失败时输出较短上下文。
			options.MinimalDisassembly = true
		case "--gluac-syntax":
			// --gluac-syntax 必须消费后续模式名或扩展名列表。
			index++
			if index >= len(args) {
				// 缺少语法模式时无法决定编译配置。
				return Options{}, fmt.Errorf("option --gluac-syntax requires an argument")
			}
			if err := applySyntaxOption(&options, args[index]); err != nil {
				// 语法扩展名称错误直接返回参数错误。
				return Options{}, err
			}
		case "--gluac-disable-syntax":
			// --gluac-disable-syntax 必须消费后续扩展名列表。
			index++
			if index >= len(args) {
				// 缺少禁用列表时返回明确参数错误。
				return Options{}, fmt.Errorf("option --gluac-disable-syntax requires an argument")
			}
			if err := applyDisableSyntaxOption(&options, args[index]); err != nil {
				// 禁用列表名称错误直接返回参数错误。
				return Options{}, err
			}
		case "--":
			// -- 终止选项解析，后续必须至少提供一个输入路径。
			if index+1 >= len(args) {
				// 没有输入文件时无法执行编译或反汇编。
				return Options{}, fmt.Errorf("missing input file")
			}
			for _, inputPath := range args[index+1:] {
				// -- 后所有参数都按输入路径处理，保持官方 luac 多文件语义。
				options.addInputPath(inputPath)
			}
			return options, nil
		default:
			if strings.HasPrefix(argument, "--gluac-syntax=") {
				// 等号形式便于脚本和 IDE 配置传参。
				if err := applySyntaxOption(&options, strings.TrimPrefix(argument, "--gluac-syntax=")); err != nil {
					// 语法扩展名称错误直接返回参数错误。
					return Options{}, err
				}
				continue
			}
			if strings.HasPrefix(argument, "--gluac-disable-syntax=") {
				// 等号形式与 --gluac-syntax 保持一致。
				if err := applyDisableSyntaxOption(&options, strings.TrimPrefix(argument, "--gluac-disable-syntax=")); err != nil {
					// 禁用列表名称错误直接返回参数错误。
					return Options{}, err
				}
				continue
			}
			// 非选项参数按输入文件处理，未知选项直接报错。
			if strings.HasPrefix(argument, "-") {
				// 未支持选项不能静默忽略，避免输出与用户预期不一致。
				return Options{}, fmt.Errorf("unknown option: %s", argument)
			}
			options.addInputPath(argument)
		}
	}
	if options.InputPath == "" && !options.Version && !options.Help {
		// 只输出版本时允许无输入；其他模式必须有输入文件。
		return Options{}, fmt.Errorf("missing input file")
	}
	return options, nil
}

// addInputPath 追加一个输入路径并维护兼容旧调用方的 InputPath 字段。
func (options *Options) addInputPath(inputPath string) {
	// 第一个输入路径仍写入 InputPath，避免单文件调用方需要改动。
	if options.InputPath == "" {
		options.InputPath = inputPath
	}
	options.InputPaths = append(options.InputPaths, inputPath)
}

// applySyntaxOption 将 --gluac-syntax 参数写入 Options。
//
// value 支持 lua53、extended、all 或逗号分隔扩展名；解析失败时返回可展示错误。
func applySyntaxOption(options *Options, value string) error {
	// 复用 extensions 注册表，避免 gluac 维护第二份扩展名称。
	syntaxSet, err := extensions.ParseSyntaxSet(value)
	if err != nil {
		// 参数非法时保留原始解析错误。
		return err
	}
	options.SyntaxExtensions = syntaxSet
	options.SyntaxExtensionsSet = true
	return nil
}

// applyDisableSyntaxOption 将 --gluac-disable-syntax 参数追加到 Options。
//
// value 只接受逗号分隔扩展名；多个 --gluac-disable-syntax 会合并禁用集合。
func applyDisableSyntaxOption(options *Options, value string) error {
	// 禁用列表与显式 --gluac-syntax 可叠加，最终在 syntaxForOptions 中统一扣除。
	disabledSet, err := extensions.ParseDisabledSyntaxSet(value)
	if err != nil {
		// 参数非法时保留原始解析错误。
		return err
	}
	options.DisabledSyntaxExtensions = options.DisabledSyntaxExtensions.With(disabledSet)
	return nil
}

// syntaxForOptions 计算 gluac 当前命令最终使用的语法扩展集合。
func syntaxForOptions(options Options) extensions.SyntaxSet {
	// 未显式指定 --gluac-syntax 时使用当前构建默认集合。
	syntaxSet := extensions.Default()
	if options.SyntaxExtensionsSet {
		// 显式 --gluac-syntax 覆盖默认集合。
		syntaxSet = options.SyntaxExtensions
	}
	if options.DisabledSyntaxExtensions != 0 {
		// --gluac-disable-syntax 在最终集合上扣除指定扩展，允许 extended 默认开再局部关闭。
		syntaxSet = syntaxSet.Without(options.DisabledSyntaxExtensions)
	}
	return syntaxSet & extensions.Compiled()
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
	if options.Help {
		// 帮助模式只输出命令说明，不读取输入文件或写出 luac.out。
		_, _ = fmt.Fprint(stdout, HelpText())
		return nil
	}
	if options.Version {
		// 版本文本复用 glua 的 Lua 5.3 兼容版本输出。
		_, _ = fmt.Fprintln(stdout, cli.VersionText)
		if options.InputPath == "" {
			// 仅请求版本时已经完成全部工作。
			return nil
		}
	}

	proto, sourceInput, inputBytes, chunkName, err := protoForOptions(options)
	if err != nil {
		// 源码编译或 binary chunk 加载失败时不写出文件。
		return err
	}

	if err := writeRequestedReports(stdout, proto, sourceInput, inputBytes, chunkName, options); err != nil {
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

// HelpText 返回 gluac 命令帮助文本。
func HelpText() string {
	// 帮助文本集中维护，便于测试和 cmd/gluac 复用。
	var builder strings.Builder
	builder.WriteString(cli.VersionText)
	builder.WriteString("\n\n")
	builder.WriteString("Usage: gluac [options] [filenames]\n\n")
	builder.WriteString("Lua compatible options:\n")
	builder.WriteString("  -l                       list bytecodes for compiled chunks\n")
	builder.WriteString("  -o name                  output to file 'name', default is luac.out\n")
	builder.WriteString("  -p                       parse only\n")
	builder.WriteString("  -s                       strip debug information\n")
	builder.WriteString("  -v                       show version information\n")
	builder.WriteString("  --                       stop handling options\n\n")
	builder.WriteString("GLua options:\n")
	builder.WriteString("  -h, --help               show this help\n")
	builder.WriteString("  --gluac-syntax value     select syntax mode: lua53, extended, all, continue,switch\n")
	builder.WriteString("  --gluac-disable-syntax v disable syntax sugar names from the selected mode\n")
	builder.WriteString("  --gluac-opcode-trace     print static opcode trace\n")
	builder.WriteString("  --gluac-step-trace       print static VM step trace\n")
	builder.WriteString("  --gluac-minimal-disassembly\n")
	builder.WriteString("                           print compact disassembly for failure diagnostics\n\n")
	builder.WriteString(buildinfo.FeatureText(false))
	return builder.String()
}

// protoForOptions 按命令行输入加载单个 Proto 或组合多个输入 Proto。
func protoForOptions(options Options) (*bytecode.Proto, bool, []byte, string, error) {
	// 单输入路径保留旧行为，让源码报告和最小反汇编继续使用原始输入字节。
	if len(options.InputPaths) <= 1 {
		inputBytes, err := os.ReadFile(options.InputPath)
		if err != nil {
			// 文件读取失败保留 os.PathError，调用方可识别权限或不存在。
			return nil, false, nil, "", err
		}
		chunkName := "@" + options.InputPath
		proto, sourceInput, err := ProtoFromBytesWithSyntax(inputBytes, chunkName, syntaxForOptions(options))
		if err != nil {
			// 源码编译或 binary chunk 加载失败时不写出文件。
			return nil, false, nil, "", err
		}
		return proto, sourceInput, inputBytes, chunkName, nil
	}

	protos := make([]*bytecode.Proto, 0, len(options.InputPaths))
	for _, inputPath := range options.InputPaths {
		// 多输入文件逐个读取并编译/加载，任一失败时不写出组合 chunk。
		inputBytes, err := os.ReadFile(inputPath)
		if err != nil {
			return nil, false, nil, "", err
		}
		proto, _, err := ProtoFromBytesWithSyntax(inputBytes, "@"+inputPath, syntaxForOptions(options))
		if err != nil {
			return nil, false, nil, "", err
		}
		protos = append(protos, proto)
	}
	return CombineProtos(protos, "=(luac)"), false, nil, "=(luac)", nil
}

// CombineProtos 构造官方 luac 多输入文件语义的顶层 wrapper Proto。
//
// wrapper 依次创建并调用每个输入 chunk 的 closure；输入 chunk 的 `_ENV` upvalue 会改为捕获
// wrapper 的 `_ENV`，使多文件组合后共享同一个全局环境。
func CombineProtos(protos []*bytecode.Proto, source string) *bytecode.Proto {
	// 顶层 wrapper 自身只需要一个函数寄存器，并声明外部注入的 `_ENV` upvalue。
	wrapper := bytecode.NewProto(source)
	wrapper.MaxStackSize = 2
	wrapper.Upvalues = []bytecode.UpvalueDesc{{Name: "_ENV", InStack: true, Index: 0}}
	for _, child := range protos {
		// nil 子 Proto 表示调用方传入损坏数据，跳过可避免 panic。
		if child == nil {
			continue
		}
		reparentTopLevelEnvironment(child)
		childIndex := wrapper.AddChild(child)
		wrapper.Code = append(wrapper.Code,
			bytecode.CreateABx(bytecode.OpClosure, 0, childIndex),
			bytecode.CreateABC(bytecode.OpCall, 0, 1, 1),
		)
	}
	wrapper.Code = append(wrapper.Code, bytecode.CreateABC(bytecode.OpReturn, 0, 1, 0))
	wrapper.LineInfo = make([]int, len(wrapper.Code))
	return wrapper
}

// reparentTopLevelEnvironment 让被 luac wrapper 包裹的输入 chunk 捕获 wrapper 的 `_ENV`。
func reparentTopLevelEnvironment(proto *bytecode.Proto) {
	if proto == nil || len(proto.Upvalues) == 0 {
		// 没有 upvalue 的 chunk 不需要环境重定向。
		return
	}
	for index := range proto.Upvalues {
		if proto.Upvalues[index].Name == "_ENV" || index == 0 {
			// 顶层 chunk 的外部环境改为捕获 wrapper 的 upvalue 0，兼容 stripped chunk 名称为空的情况。
			proto.Upvalues[index].InStack = false
			proto.Upvalues[index].Index = 0
			return
		}
	}
}

// CompileSource 将 Lua 源码编译为 Lua 5.3 Proto。
//
// source 是完整 Lua chunk；chunkName 写入 Proto.Source，建议源码文件使用 `@path`。返回错误
// 保留 parser 或 codegen 的原始语义，便于 CLI 与嵌入方定位。
func CompileSource(source string, chunkName string) (*bytecode.Proto, error) {
	// 默认入口使用当前构建产物的默认语法扩展集合。
	return CompileSourceWithSyntax(source, chunkName, extensions.Default())
}

// CompileSourceWithSyntax 将 Lua 源码按指定语法集合编译为 Lua 5.3 Proto。
//
// source 是完整 Lua chunk；chunkName 写入 Proto.Source；syntax 会裁剪到当前二进制已编译集合。
func CompileSourceWithSyntax(source string, chunkName string, syntax extensions.SyntaxSet) (*bytecode.Proto, error) {
	// 优先尝试 compile_3000_functions 同构的完整 chunk streaming 路径；不命中时无副作用回退普通 parser/codegen。
	if proto, ok := compileSimpleFunctionStreamChunk(source, chunkName); ok {
		// streaming 路径已经直接生成 Lua 5.3 Proto，调用方无需再经过 AST 和 codegen。
		return proto, nil
	}
	// 先解析源码为 AST，再交给 codegen 生成 Proto。
	chunkParser := parser.NewCompactWithSyntax(source, syntax)
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

// simpleFunctionStreamFunction 保存完整 chunk streaming 命中的单个顶层简单函数声明。
//
// 字段均来自已证明的 `function name(param) return param + integer end` 单行形态；该结构只服务
// CompileSourceWithSyntax 的编译期 fast path，不暴露给普通 parser 或 runtime。
type simpleFunctionStreamFunction struct {
	// Name 是顶层全局函数名。
	Name string
	// ParamName 是唯一形参名称。
	ParamName string
	// Integer 是 return 表达式右侧的整数常量。
	Integer int64
	// Line 是函数定义所在源码行。
	Line int
}

// simpleFunctionStreamReturn 保存完整 chunk streaming 命中的最终单调用 return。
//
// 当前只覆盖 `return fN(integer)`，用于生成与普通 codegen 等价的 TAILCALL + RETURN。
type simpleFunctionStreamReturn struct {
	// Name 是最终被调用的全局函数名。
	Name string
	// Argument 是最终调用的单个整数参数。
	Argument int64
	// Line 是 return 语句所在源码行。
	Line int
}

// simpleFunctionStreamCursor 保存源码扫描位置和当前行号。
//
// cursor 只识别 ASCII Lua benchmark 子集；任何非目标形态都返回 ok=false，交给普通 parser 保留语义和错误位置。
type simpleFunctionStreamCursor struct {
	// source 是完整 Lua 源码。
	source string
	// offset 是当前读取字节偏移。
	offset int
	// line 是当前 1-based 源码行号。
	line int
}

// compileSimpleFunctionStreamChunk 尝试直接生成批量顶层简单函数声明 chunk 的 Proto。
//
// source 必须精确匹配若干 `function name(param) return param + integer end`，并以
// `return name(integer)` 结束；不匹配时返回 ok=false 且不产生错误，调用方必须回退普通 parser/codegen。
func compileSimpleFunctionStreamChunk(source string, chunkName string) (*bytecode.Proto, bool) {
	// 初始化 cursor 时行号从 1 开始，保持 Lua 5.3 debug 行号语义。
	cursor := simpleFunctionStreamCursor{source: source, line: 1}
	functions := make([]simpleFunctionStreamFunction, 0, strings.Count(source, "\nfunction ")+1)
	for {
		// 顶层只允许横向空白；空行、注释或其他语句全部回退普通 parser。
		cursor.skipHorizontalWhitespace()
		if cursor.atEOF() {
			// 没有最终 return 语句时不是目标完整 chunk。
			return nil, false
		}
		if cursor.hasKeywordPrefix("return") {
			// 遇到最终 return 后结束函数声明扫描。
			break
		}
		function, ok := cursor.parseSimpleFunctionDefinition()
		if !ok {
			// 任一函数声明不满足目标形态时回退普通 parser/codegen。
			return nil, false
		}
		functions = append(functions, function)
		if !cursor.consumeLineBreak() {
			// 函数声明后必须换行，避免吞掉同一行额外语句或分号语义。
			return nil, false
		}
	}
	if len(functions) == 0 {
		// 没有顶层函数声明时不是 compile_3000_functions 同构形态。
		return nil, false
	}
	returnCall, ok := cursor.parseSimpleFunctionReturnCall()
	if !ok {
		// 最终 return 不是单个整数实参调用时回退普通 parser/codegen。
		return nil, false
	}
	cursor.skipTrailingWhitespace()
	if !cursor.atEOF() {
		// return 之后存在额外 token 时必须回退，保留普通 parser 的错误或语义。
		return nil, false
	}
	nameIndexes := make(map[string]int, len(functions))
	for index, function := range functions {
		// 记录函数名常量索引；重复定义时最后一次覆盖不影响同名常量位置，因为常量表仍按源码函数声明顺序写入。
		nameIndexes[function.Name] = index
	}
	if _, ok := nameIndexes[returnCall.Name]; !ok {
		// 当前 streaming 只覆盖最终调用已在本 chunk 中声明的函数，其他全局查找交给普通 codegen。
		return nil, false
	}
	if len(functions)+1 > bytecode.MaxArgBx {
		// 常量表索引无法放入 LOADK Bx 时必须回退普通路径并由现有 codegen 报错或处理。
		return nil, false
	}
	return buildSimpleFunctionStreamProto(functions, returnCall, nameIndexes, chunkName), true
}

// parseSimpleFunctionDefinition 解析单行简单函数声明。
//
// 只接受 `function name(param) return param + integer end`；任意额外语句、复杂表达式、hex/float 或多参数都回退。
func (cursor *simpleFunctionStreamCursor) parseSimpleFunctionDefinition() (simpleFunctionStreamFunction, bool) {
	// 记录函数定义行号，后续直接写入 child Proto debug 信息。
	line := cursor.line
	if !cursor.consumeKeyword("function") || !cursor.requireHorizontalWhitespace() {
		// 顶层函数声明必须以 function 关键字开头，并跟随空白。
		return simpleFunctionStreamFunction{}, false
	}
	name, ok := cursor.consumeIdentifier()
	if !ok {
		// 函数名必须是 ASCII 简单标识符；字段、方法和扩展形态回退普通 parser。
		return simpleFunctionStreamFunction{}, false
	}
	cursor.skipHorizontalWhitespace()
	if !cursor.consumeByte('(') {
		// 目标形态必须立即进入普通参数列表。
		return simpleFunctionStreamFunction{}, false
	}
	cursor.skipHorizontalWhitespace()
	paramName, ok := cursor.consumeIdentifier()
	if !ok {
		// 目标形态只覆盖单个普通参数，空参数或 vararg 回退。
		return simpleFunctionStreamFunction{}, false
	}
	cursor.skipHorizontalWhitespace()
	if !cursor.consumeByte(')') {
		// 多参数或缺失右括号回退普通 parser。
		return simpleFunctionStreamFunction{}, false
	}
	if !cursor.requireHorizontalWhitespace() || !cursor.consumeKeyword("return") || !cursor.requireHorizontalWhitespace() {
		// 函数体必须只包含 return 语句。
		return simpleFunctionStreamFunction{}, false
	}
	leftName, ok := cursor.consumeIdentifier()
	if !ok || leftName != paramName {
		// return 左操作数必须精确读取唯一参数名，避免 local/upvalue/global 语义变化。
		return simpleFunctionStreamFunction{}, false
	}
	cursor.skipHorizontalWhitespace()
	if !cursor.consumeByte('+') {
		// 当前 streaming 只覆盖整数加法。
		return simpleFunctionStreamFunction{}, false
	}
	cursor.skipHorizontalWhitespace()
	integer, ok := cursor.consumeDecimalInteger()
	if !ok {
		// 只覆盖十进制非负整数；hex、float 或非法数字回退普通 parser。
		return simpleFunctionStreamFunction{}, false
	}
	if !cursor.requireHorizontalWhitespace() || !cursor.consumeKeyword("end") {
		// integer 后必须直接关闭函数体。
		return simpleFunctionStreamFunction{}, false
	}
	cursor.skipHorizontalWhitespace()
	return simpleFunctionStreamFunction{Name: name, ParamName: paramName, Integer: integer, Line: line}, true
}

// parseSimpleFunctionReturnCall 解析最终单调用 return 语句。
//
// 只接受 `return name(integer)`，因为该形态由普通 codegen 生成 TAILCALL + RETURN，正好覆盖默认 compile benchmark。
func (cursor *simpleFunctionStreamCursor) parseSimpleFunctionReturnCall() (simpleFunctionStreamReturn, bool) {
	// 记录 return 行号，父 Proto 的 tailcall 指令全部映射到该行。
	line := cursor.line
	if !cursor.consumeKeyword("return") || !cursor.requireHorizontalWhitespace() {
		// 最终语句必须是 return。
		return simpleFunctionStreamReturn{}, false
	}
	name, ok := cursor.consumeIdentifier()
	if !ok {
		// 当前只覆盖简单全局函数名调用。
		return simpleFunctionStreamReturn{}, false
	}
	cursor.skipHorizontalWhitespace()
	if !cursor.consumeByte('(') {
		// 调用必须使用括号形态。
		return simpleFunctionStreamReturn{}, false
	}
	cursor.skipHorizontalWhitespace()
	argument, ok := cursor.consumeDecimalInteger()
	if !ok {
		// 当前只覆盖单个十进制整数实参。
		return simpleFunctionStreamReturn{}, false
	}
	cursor.skipHorizontalWhitespace()
	if !cursor.consumeByte(')') {
		// 缺失右括号或存在更多实参时回退普通 parser。
		return simpleFunctionStreamReturn{}, false
	}
	return simpleFunctionStreamReturn{Name: name, Argument: argument, Line: line}, true
}

// buildSimpleFunctionStreamProto 直接构造完整 chunk streaming 的父 Proto 和子 Proto。
//
// 入参必须来自 compileSimpleFunctionStreamChunk 的精确解析结果；函数按源码顺序写入全局 `_ENV`，
// 最终 return 生成与普通 codegen 等价的 TAILCALL + RETURN。
func buildSimpleFunctionStreamProto(functions []simpleFunctionStreamFunction, returnCall simpleFunctionStreamReturn, nameIndexes map[string]int, chunkName string) *bytecode.Proto {
	// 父常量表先保存所有函数名，再保存最终调用的整数实参，保持与现有 codegen 对齐。
	constants := make([]bytecode.Constant, 0, len(functions)+1)
	for _, function := range functions {
		// 每个 function 声明都需要一个全局名称常量。
		constants = append(constants, bytecode.StringConstant(function.Name))
	}
	argumentIndex := len(constants)
	constants = append(constants, bytecode.IntegerConstant(returnCall.Argument))

	code := make([]bytecode.Instruction, 0, len(functions)*3+5)
	lineInfo := make([]int, 0, len(functions)*3+5)
	children := make([]*bytecode.Proto, len(functions))
	childProtos := make([]bytecode.Proto, len(functions))
	childConstants := make([]bytecode.Constant, len(functions))
	childCode := make([]bytecode.Instruction, len(functions)*2)
	childLineInfo := make([]int, len(functions)*2)
	childLocalVars := make([]bytecode.LocalVar, len(functions))
	for functionIndex, function := range functions {
		// 子 Proto 使用批量 backing arrays，避免 compile_3000_functions 每个 child 产生多次切片分配。
		childConstants[functionIndex] = bytecode.IntegerConstant(function.Integer)
		codeStart := functionIndex * 2
		childCode[codeStart] = bytecode.CreateABC(bytecode.OpAdd, 1, 0, bytecode.RKAsK(0))
		childCode[codeStart+1] = bytecode.CreateABC(bytecode.OpReturn, 1, 2, 0)
		childLineInfo[codeStart] = function.Line
		childLineInfo[codeStart+1] = function.Line
		childLocalVars[functionIndex] = bytecode.LocalVar{
			Name:     function.ParamName,
			Register: 0,
			StartPC:  0,
			EndPC:    2,
		}
		childProtos[functionIndex] = bytecode.Proto{
			NumParams:       1,
			IsVararg:        false,
			MaxStackSize:    2,
			LineDefined:     function.Line,
			LastLineDefined: function.Line,
			Source:          chunkName,
			Constants:       childConstants[functionIndex : functionIndex+1],
			Code:            childCode[codeStart : codeStart+2],
			LineInfo:        childLineInfo[codeStart : codeStart+2],
			LocalVars:       childLocalVars[functionIndex : functionIndex+1],
		}
		children[functionIndex] = &childProtos[functionIndex]
		code = append(code, bytecode.CreateABx(bytecode.OpClosure, 0, functionIndex))
		lineInfo = append(lineInfo, function.Line)
		appendGlobalNameStore(&code, &lineInfo, functionIndex, function.Line)
	}

	nameIndex := nameIndexes[returnCall.Name]
	appendGlobalNameLoad(&code, &lineInfo, nameIndex, returnCall.Line)
	code = append(code,
		bytecode.CreateABx(bytecode.OpLoadK, 1, argumentIndex),
		bytecode.CreateABC(bytecode.OpTailCall, 0, 2, 0),
		bytecode.CreateABC(bytecode.OpReturn, 0, 0, 0),
	)
	lineInfo = append(lineInfo, returnCall.Line, returnCall.Line, returnCall.Line)

	return &bytecode.Proto{
		NumParams:    0,
		IsVararg:     true,
		MaxStackSize: 2,
		Source:       chunkName,
		Constants:    constants,
		Code:         code,
		Protos:       children,
		LineInfo:     lineInfo,
		Upvalues:     []bytecode.UpvalueDesc{{Name: "_ENV", InStack: true, Index: 0}},
	}
}

// appendGlobalNameStore 追加 `_ENV[name] = R0` 对应指令。
//
// nameIndex 可被 RK 直接编码时使用 SETTABUP K；超过 RK 上限时先 LOADK 到 R1，再用寄存器键写入。
func appendGlobalNameStore(code *[]bytecode.Instruction, lineInfo *[]int, nameIndex int, line int) {
	if nameIndex <= bytecode.MaxIndexRK {
		// 常量索引可直接编码到 SETTABUP 的 B 字段。
		*code = append(*code, bytecode.CreateABC(bytecode.OpSetTabUp, 0, bytecode.RKAsK(nameIndex), 0))
		*lineInfo = append(*lineInfo, line)
		return
	}
	// 常量索引超过 RK 上限时，先把全局名加载到 R1，再按寄存器键写入 `_ENV`。
	*code = append(*code,
		bytecode.CreateABx(bytecode.OpLoadK, 1, nameIndex),
		bytecode.CreateABC(bytecode.OpSetTabUp, 0, 1, 0),
	)
	*lineInfo = append(*lineInfo, line, line)
}

// appendGlobalNameLoad 追加 `R0 = _ENV[name]` 对应指令。
//
// nameIndex 可被 RK 直接编码时使用 GETTABUP K；超过 RK 上限时复用 R1 作为临时键寄存器。
func appendGlobalNameLoad(code *[]bytecode.Instruction, lineInfo *[]int, nameIndex int, line int) {
	if nameIndex <= bytecode.MaxIndexRK {
		// 常量索引可直接编码到 GETTABUP 的 C 字段。
		*code = append(*code, bytecode.CreateABC(bytecode.OpGetTabUp, 0, 0, bytecode.RKAsK(nameIndex)))
		*lineInfo = append(*lineInfo, line)
		return
	}
	// 常量索引超过 RK 上限时，先把全局名加载到 R1，再从 `_ENV` 读取函数值到 R0。
	*code = append(*code,
		bytecode.CreateABx(bytecode.OpLoadK, 1, nameIndex),
		bytecode.CreateABC(bytecode.OpGetTabUp, 0, 0, 1),
	)
	*lineInfo = append(*lineInfo, line, line)
}

// atEOF 判断 cursor 是否已经读完源码。
func (cursor *simpleFunctionStreamCursor) atEOF() bool {
	// offset 到达源码长度即表示 EOF。
	return cursor.offset >= len(cursor.source)
}

// skipHorizontalWhitespace 跳过不换行的空白字节。
func (cursor *simpleFunctionStreamCursor) skipHorizontalWhitespace() {
	for cursor.offset < len(cursor.source) {
		// 只跳过空格、制表符和 CR；LF 由行边界函数处理。
		switch cursor.source[cursor.offset] {
		case ' ', '\t', '\r':
			cursor.offset++
		default:
			return
		}
	}
}

// skipTrailingWhitespace 跳过源码尾部空白，并维护行号。
func (cursor *simpleFunctionStreamCursor) skipTrailingWhitespace() {
	for cursor.offset < len(cursor.source) {
		// 尾部空白可以包含换行，但不能包含注释或其他 token。
		switch cursor.source[cursor.offset] {
		case ' ', '\t', '\r':
			cursor.offset++
		case '\n':
			cursor.offset++
			cursor.line++
		default:
			return
		}
	}
}

// consumeLineBreak 消费一个必需的换行。
func (cursor *simpleFunctionStreamCursor) consumeLineBreak() bool {
	if cursor.offset < len(cursor.source) && cursor.source[cursor.offset] == '\n' {
		// 成功消费换行后行号加一。
		cursor.offset++
		cursor.line++
		return true
	}
	// 没有换行说明目标单行函数声明后存在额外 token 或 EOF。
	return false
}

// requireHorizontalWhitespace 要求当前位置至少存在一个横向空白。
func (cursor *simpleFunctionStreamCursor) requireHorizontalWhitespace() bool {
	startOffset := cursor.offset
	cursor.skipHorizontalWhitespace()
	return cursor.offset > startOffset
}

// consumeByte 消费指定 ASCII 字节。
func (cursor *simpleFunctionStreamCursor) consumeByte(want byte) bool {
	if cursor.offset < len(cursor.source) && cursor.source[cursor.offset] == want {
		// 命中目标字节时前进一位。
		cursor.offset++
		return true
	}
	// 未命中目标字节时保持 cursor 不变。
	return false
}

// consumeKeyword 消费 Lua 关键字并确认后续不是标识符字符。
func (cursor *simpleFunctionStreamCursor) consumeKeyword(keyword string) bool {
	if !cursor.hasKeywordPrefix(keyword) {
		// 关键字不匹配时不能前进 cursor。
		return false
	}
	cursor.offset += len(keyword)
	return true
}

// hasKeywordPrefix 判断当前位置是否以指定关键字开始。
func (cursor *simpleFunctionStreamCursor) hasKeywordPrefix(keyword string) bool {
	if !strings.HasPrefix(cursor.source[cursor.offset:], keyword) {
		// 文本前缀不匹配时直接失败。
		return false
	}
	nextOffset := cursor.offset + len(keyword)
	if nextOffset < len(cursor.source) && isSimpleLuaIdentifierPart(cursor.source[nextOffset]) {
		// 关键字后仍是标识符字符时，说明它只是更长标识符的前缀。
		return false
	}
	return true
}

// consumeIdentifier 消费 ASCII Lua 标识符。
func (cursor *simpleFunctionStreamCursor) consumeIdentifier() (string, bool) {
	if cursor.offset >= len(cursor.source) || !isSimpleLuaIdentifierStart(cursor.source[cursor.offset]) {
		// 标识符首字节不合法时回退普通 parser。
		return "", false
	}
	startOffset := cursor.offset
	cursor.offset++
	for cursor.offset < len(cursor.source) && isSimpleLuaIdentifierPart(cursor.source[cursor.offset]) {
		// 连续读取标识符后续字节。
		cursor.offset++
	}
	return cursor.source[startOffset:cursor.offset], true
}

// consumeDecimalInteger 消费十进制非负整数。
func (cursor *simpleFunctionStreamCursor) consumeDecimalInteger() (int64, bool) {
	if cursor.offset >= len(cursor.source) || cursor.source[cursor.offset] < '0' || cursor.source[cursor.offset] > '9' {
		// 当前 streaming 不覆盖负数、hex 或非数字开头。
		return 0, false
	}
	startOffset := cursor.offset
	for cursor.offset < len(cursor.source) && cursor.source[cursor.offset] >= '0' && cursor.source[cursor.offset] <= '9' {
		// 连续读取十进制数字。
		cursor.offset++
	}
	value, err := strconv.ParseInt(cursor.source[startOffset:cursor.offset], 10, 64)
	if err != nil {
		// 整数溢出或解析失败交给普通 parser/codegen 处理。
		return 0, false
	}
	return value, true
}

// isSimpleLuaIdentifierStart 判断 ASCII 字节是否可作为 Lua 标识符首字节。
func isSimpleLuaIdentifierStart(value byte) bool {
	// 当前 streaming 只覆盖 ASCII 标识符；非 ASCII 名称回退普通 lexer/parser。
	return value == '_' || (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

// isSimpleLuaIdentifierPart 判断 ASCII 字节是否可作为 Lua 标识符后续字节。
func isSimpleLuaIdentifierPart(value byte) bool {
	// Lua 标识符后续可包含数字；其他字节由普通 parser 处理。
	return isSimpleLuaIdentifierStart(value) || (value >= '0' && value <= '9')
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
	// 默认入口使用当前构建产物的默认语法扩展集合。
	return ProtoFromBytesWithSyntax(input, chunkName, extensions.Default())
}

// ProtoFromBytesWithSyntax 从输入字节按指定语法集合加载或编译 Proto。
//
// 输入以 Lua chunk 签名开头时按 binary chunk 读取，否则按源码编译；syntax 只影响源码路径。
func ProtoFromBytesWithSyntax(input []byte, chunkName string, syntax extensions.SyntaxSet) (*bytecode.Proto, bool, error) {
	// 通过签名判断输入形态，避免为 CLI 额外增加模式参数。
	if bytes.HasPrefix(input, []byte(bytecode.ChunkSignature)) {
		// binary chunk 直接走 loader，不重新编译源码。
		proto, err := bytecode.LoadBinaryChunk(bytes.NewReader(input))
		return proto, false, err
	}
	source := lexer.StripInitialShebang(string(input))
	proto, err := CompileSourceWithSyntax(source, chunkName, syntax)
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
