// Package cli 实现 glua 命令行入口的第一阶段编排。
//
// 当前阶段支持 Lua 5.3 CLI 的基础选项解析、`-e` 片段加载、`-l` 模块加载、`-i` 交互模式标记、
// `-v` 版本输出、`--` 选项终止、脚本路径、脚本参数和显式 `-` stdin 脚本。
package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zing/go-lua-vm/bytecode"
	"github.com/zing/go-lua-vm/compiler/codegen"
	"github.com/zing/go-lua-vm/compiler/formatter"
	"github.com/zing/go-lua-vm/compiler/lexer"
	"github.com/zing/go-lua-vm/compiler/parser"
	"github.com/zing/go-lua-vm/extensions"
	"github.com/zing/go-lua-vm/lua"
	"github.com/zing/go-lua-vm/runtime"
	oslib "github.com/zing/go-lua-vm/stdlib/os"
)

const (
	// ExitOK 表示 CLI 成功退出。
	ExitOK = 0
	// ExitFailure 表示 CLI 遇到参数、加载或执行错误。
	ExitFailure = 1
	// ExitInterrupted 表示 CLI 被 Ctrl-C 中断。
	ExitInterrupted = 130
	// VersionText 是 Lua 5.3.6 官方兼容版本输出。
	VersionText = "Lua 5.3.6  Copyright (C) 1994-2020 Lua.org, PUC-Rio"
)

var errCLIInterrupted = errors.New("interrupted")

// Streams 保存 CLI 与宿主标准流之间的连接。
//
// Stdin 后续用于脚本 stdin 与 REPL；Stdout 用于版本、REPL 输出；Stderr 用于错误输出。
type Streams struct {
	// Stdin 是 CLI 输入流。
	Stdin io.Reader
	// Stdout 是 CLI 标准输出流。
	Stdout io.Writer
	// Stderr 是 CLI 标准错误流。
	Stderr io.Writer
}

// Options 保存 glua 参数解析结果。
//
// Expressions 对应一个或多个 `-e stat`；Libraries 对应一个或多个 `-l mod`；Interactive 对应 `-i`。
type Options struct {
	// Expressions 保存待执行的命令行 Lua 片段。
	Expressions []string
	// Libraries 保存待 require 的模块名。
	Libraries []string
	// Interactive 表示是否在执行启动项后进入交互模式。
	Interactive bool
	// Version 表示是否输出版本信息。
	Version bool
	// IgnoreEnvironment 表示是否忽略 LUA_INIT、LUA_PATH 和 LUA_CPATH 等启动环境变量。
	IgnoreEnvironment bool
	// ScriptPath 保存脚本文件路径；值为 "-" 时从 Stdin 读取脚本。
	ScriptPath string
	// ScriptArgs 保存传给脚本的参数。
	ScriptArgs []string
	// ArgPrefix 保存脚本名前的原始启动参数，用于构造 Lua CLI 的 arg 负索引。
	ArgPrefix []string
	// ListBytecodePath 保存可选 glua 反汇编输入路径；该选项不能与官方执行参数混用。
	ListBytecodePath string
	// FormatPath 保存待格式化 Lua 源码文件路径；该选项不能与官方执行参数混用。
	FormatPath string
	// FormatWrite 表示格式化结果是否写回原文件。
	FormatWrite bool
	// SyntaxExtensions 保存源码编译阶段启用的语法扩展集合。
	SyntaxExtensions extensions.SyntaxSet
	// SyntaxExtensionsSet 表示命令行是否显式指定过 --glua-syntax。
	SyntaxExtensionsSet bool
	// DisabledSyntaxExtensions 保存命令行显式关闭的语法扩展集合。
	DisabledSyntaxExtensions extensions.SyntaxSet
}

// Main 执行 glua 命令行并返回进程退出码。
//
// ctx 必须非 nil；args 不包含程序名；streams 中的输出流为 nil 时会被 io.Discard 替代。
func Main(ctx context.Context, args []string, streams Streams) int {
	// 先执行带错误返回的主流程，确保 main.go 只负责 os.Exit。
	if err := Run(ctx, args, streams); err != nil {
		if errors.Is(err, errCLIInterrupted) {
			// 交互模式 Ctrl-C 直接终止解释器，不按普通错误写 stderr。
			return ExitInterrupted
		}
		if _, ok := luaExitError(err); ok {
			// os.exit 是脚本主动请求进程退出，CLI 只映射退出码，不额外写 stderr。
			return exitCodeForError(err)
		}
		// 出错时写入 stderr 并返回非零退出码。
		stderr := safeWriter(streams.Stderr)
		if isCLISyntaxError(err) {
			// CLI 可执行文件按官方 Lua 5.3 形态输出一行语法错误，不展示 Go API 扩展指针诊断。
			_, _ = fmt.Fprintf(stderr, "%s: %s\n", os.Args[0], cliSyntaxErrorTextFromError(err))
		} else if lua.IsRuntimeError(err) {
			// Lua 运行期错误输出程序名前缀、Lua error object 和官方风格 traceback。
			_, _ = fmt.Fprintf(stderr, "%s: %s\n", os.Args[0], cliRuntimeErrorText(err))
		} else {
			// 非 Lua runtime 错误保留 Go error 文本，便于定位宿主侧失败。
			_, _ = fmt.Fprintln(stderr, err)
		}
		return exitCodeForError(err)
	}
	return ExitOK
}

// writeCLISyntaxError 输出官方 Lua CLI 风格的语法错误。
//
// err 可以是 lua.SyntaxError 或 parser 原始错误；结构化错误会降级为 `source:line: message`
// 形态，避免 CLI stderr 出现 Go API 专用列号和源码指针块。
func writeCLISyntaxError(stderr io.Writer, err error) {
	_, _ = fmt.Fprintln(stderr, cliSyntaxErrorTextFromError(err))
}

// cliSyntaxErrorTextFromError 从错误链提取官方 Lua CLI 风格语法错误文本。
func cliSyntaxErrorTextFromError(err error) string {
	var syntaxErr *lua.SyntaxError
	if errors.As(err, &syntaxErr) && syntaxErr != nil {
		// 结构化语法错误可精确降级为官方一行文本。
		return cliSyntaxErrorText(syntaxErr)
	}
	return err.Error()
}

// cliSyntaxErrorText 将结构化语法错误转换为官方 Lua CLI 的一行文本。
//
// Go API 的 SyntaxError 保留列号和详细提示；CLI 只输出 source:line:message。常见的非法语句
// 起始和孤立表达式错误按官方 `unexpected symbol` / `syntax error` 文案映射。
func cliSyntaxErrorText(syntaxErr *lua.SyntaxError) string {
	if syntaxErr == nil {
		// nil 错误没有更具体内容。
		return ""
	}
	message := parser.LoadErrorText(syntaxErr.Cause, syntaxErr.Details.SourceName)
	if strings.Contains(message, `expected operator "=" near <eof>`) {
		// 官方 `lua -e a` 报 `syntax error near <eof>`，不暴露内部 assignment 期望。
		message = strings.ReplaceAll(message, `expected operator "=" near <eof>`, "syntax error near <eof>")
	}
	if strings.Contains(message, `expected expression near <eof>`) {
		// 官方 `if` 这类非法语句尾部报 `unexpected symbol near <eof>`。
		message = strings.ReplaceAll(message, `expected expression near <eof>`, "unexpected symbol near <eof>")
	}
	return message
}

// writeSyntaxError 输出 CLI 语法错误。
//
// err 可以是 lua.SyntaxError 或 parser 原始错误；结构化错误会额外输出源码行、指针和提示，
// 原始错误只输出主消息。
func writeSyntaxError(stderr io.Writer, err error) {
	var syntaxErr *lua.SyntaxError
	if errors.As(err, &syntaxErr) && syntaxErr != nil {
		// Go API 语法错误包含可扩展详情，CLI 使用它渲染 SQL 风格源码定位。
		_, _ = fmt.Fprintln(stderr, syntaxErr.Error())
		writeSyntaxErrorDetails(stderr, syntaxErr.Details)
		return
	}
	_, _ = fmt.Fprintln(stderr, err)
}

// cliRuntimeErrorText 返回官方 Lua CLI 风格运行时错误文本。
//
// err 必须是 Lua 运行期错误；第一行是 Lua error object，后续在可用时追加 traceback。若运行时
// 尚未保存 Lua 帧，也至少返回错误主消息，避免 CLI 打印空 traceback。
func cliRuntimeErrorText(err error) string {
	message := luaErrorText(err)
	var runtimeErr *runtime.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr == nil || len(runtimeErr.TracebackFrames) == 0 {
		// 没有保存失败现场时只能输出主错误文本。
		return message
	}
	traceback := formatCLITraceback(message, runtimeErr.TracebackFrames)
	if traceback == "" {
		// 防御空 traceback，保持原有错误主消息。
		return message
	}
	return traceback
}

// formatCLITraceback 把 runtime 调用帧转换为官方 CLI 可读 traceback。
//
// frames 顺序为当前帧到最早帧；当前实现覆盖主 chunk、命名 Lua 函数和 Go/C 帧的常见 CLI
// 错误展示。无法识别的帧会退回 runtime.Traceback，保证错误仍有诊断信息。
func formatCLITraceback(message string, frames []runtime.CallFrame) string {
	var builder strings.Builder
	builder.WriteString(message)
	builder.WriteString("\nstack traceback:")
	wroteFrame := false
	for _, frame := range frames {
		// 按 Lua 5.3 traceback 顺序逐帧输出。
		line, ok := cliTracebackFrameLine(frame)
		if !ok {
			// 非 Lua 帧按 C 帧展示。
			builder.WriteString("\n\t[C]: in ?")
			wroteFrame = true
			continue
		}
		builder.WriteByte('\n')
		builder.WriteByte('\t')
		builder.WriteString(line)
		wroteFrame = true
	}
	if !wroteFrame {
		// 理论上不会出现；保留 runtime fallback 避免返回只有标题的 traceback。
		return runtime.Traceback(message, frames)
	}
	if !strings.Contains(builder.String(), "\n\t[C]: in ?") {
		// 主程序入口错误在官方 CLI 末尾会展示 C 调用边界。
		builder.WriteString("\n\t[C]: in ?")
	}
	return builder.String()
}

// cliTracebackFrameLine 返回单个调用帧的官方 CLI traceback 行。
//
// 返回 false 表示该帧不是 Lua closure，调用方应按 C 帧处理。source 会去掉 `@` 前缀，行号缺失时
// 只展示 source。
func cliTracebackFrameLine(frame runtime.CallFrame) (string, bool) {
	if frame.Function.Kind != runtime.KindLuaClosure {
		// Go closure 对应官方 traceback 中的 C 帧。
		if frame.Name != "" {
			// Lua CLI 对 C 函数帧统一展示 `function`，即使 debug name 来源是 global/field。
			return fmt.Sprintf("[C]: in function '%s'", frame.Name), true
		}
		return "[C]: in ?", true
	}
	closure, ok := frame.Function.Ref.(*runtime.LuaClosure)
	if !ok || closure == nil || closure.Proto == nil {
		// 损坏 Lua closure 无法可靠展示源码位置。
		return "", false
	}
	source := strings.TrimPrefix(closure.Proto.Source, "@")
	if source == "" {
		// stripped 或缺失 source 时使用官方占位。
		source = "=?"
	}
	line := -1
	if frame.CurrentPC >= 0 && frame.CurrentPC < len(closure.Proto.LineInfo) {
		// 行号表存在时使用当前 pc 的源码行号。
		line = closure.Proto.LineInfo[frame.CurrentPC]
	}
	location := source
	if line > 0 {
		// 有行号时展示 source:line。
		location = fmt.Sprintf("%s:%d", source, line)
	}
	if frame.Name != "" {
		// 命名调用帧展示函数名来源。
		nameWhat := frame.NameWhat
		if nameWhat == "" {
			// 缺失来源时按普通 function 展示。
			nameWhat = "function"
		}
		return fmt.Sprintf("%s: in %s '%s'", location, nameWhat, frame.Name), true
	}
	if closure.Proto.LineDefined == 0 {
		// 顶层 chunk 对齐官方 `in main chunk`。
		return fmt.Sprintf("%s: in main chunk", location), true
	}
	return fmt.Sprintf("%s: in function <%s:%d>", location, source, closure.Proto.LineDefined), true
}

// writeSyntaxErrorDetails 输出语法错误的源码行与指针详情。
//
// details.LineText 为空时跳过扩展展示；Column 使用 1 起始，EOF 可指向行尾后一列。
func writeSyntaxErrorDetails(stderr io.Writer, details lua.SyntaxErrorDetails) {
	if details.LineText == "" {
		// 没有源码行时只保留主错误，避免输出空指针块。
		return
	}
	linePrefix := fmt.Sprintf("%4d | ", details.Line)
	_, _ = fmt.Fprintf(stderr, "%s%s\n", linePrefix, details.LineText)
	caretColumn := details.Column
	if caretColumn < 1 {
		// 防御非法列号，至少指向行首。
		caretColumn = 1
	}
	padding := strings.Repeat(" ", len(linePrefix)) + "| " + strings.Repeat(" ", caretColumn-1)
	hint := details.Hint
	if hint == "" {
		// 缺少用户提示时回退到 parser 原始 expected 文本。
		hint = details.Expected
	}
	if hint != "" {
		// 有提示时把提示放在 caret 后，方便直接阅读。
		_, _ = fmt.Fprintf(stderr, "%s^ %s\n", padding, hint)
		return
	}
	_, _ = fmt.Fprintf(stderr, "%s^\n", padding)
}

// Run 解析参数并执行当前阶段已支持的 CLI 行为。
//
// ctx 必须非 nil；当前执行顺序为版本输出、打开标准库、加载 `-l` 模块、执行 `-e` 片段、
// 执行脚本或 stdin、处理 `-i` 标记；无参数且 stdin 为终端时兼容官方 lua 进入 REPL。
func Run(ctx context.Context, args []string, streams Streams) error {
	if ctx == nil {
		// nil context 无法表达取消语义，调用方应传入 context.Background 或可取消上下文。
		return lua.ErrNilContext
	}
	options, err := ParseArgs(args)
	if err != nil {
		// 参数解析错误直接返回，避免创建 State 后再失败。
		return err
	}
	if options.Version {
		// -v 只写 stdout，不阻止后续 -l/-e/script 执行。
		stdout := safeWriter(streams.Stdout)
		_, _ = fmt.Fprintln(stdout, VersionText)
	}
	if options.ListBytecodePath != "" {
		// 可选反汇编模式只做调试输出，不创建 State，也不执行脚本。
		return runBytecodeList(options.ListBytecodePath, streams.Stdout, syntaxForOptions(options))
	}
	if options.FormatPath != "" {
		// 格式化模式只读取并输出/写回源码，不执行脚本。
		return runFormat(options.FormatPath, options.FormatWrite, streams.Stdout, syntaxForOptions(options))
	}

	// 创建带 context 的 State，保证后续加载和调用可观察取消。
	stateOptions := lua.DefaultOptions()
	stateOptions = lua.WithSyntaxExtensions(stateOptions, syntaxForOptions(options))
	stateOptions.AllowHostFilesystem = true
	stateOptions.AllowProcess = true
	if !options.IgnoreEnvironment {
		// 普通 CLI 模式兼容官方 lua，允许 os.getenv 和 package 初始化读取宿主环境。
		stateOptions.AllowEnvironment = true
	}
	state, err := lua.NewStateWithContext(ctx, stateOptions)
	if err != nil {
		// State 创建失败说明运行环境不可用，直接返回。
		return err
	}
	defer state.Close()
	stopSignals := enableSignalInterrupts(state)
	defer stopSignals()
	openLibs := func() error {
		// 标准库打开阶段会初始化 package.path/cpath，因此 -E 需要在这里屏蔽环境变量。
		return lua.OpenLibs(state)
	}
	if options.IgnoreEnvironment {
		// -E 要求忽略影响启动和 package 路径的宿主环境变量。
		err = withIgnoredLuaEnvironment(openLibs)
	} else {
		// 普通模式下按 Lua 5.3 规则读取宿主环境变量。
		err = openLibs()
	}
	if err != nil {
		// 标准库打开失败时无法支持 -l/require，直接返回。
		return err
	}

	if err := setArgTable(state, options.ScriptPath, options.ScriptArgs, options.ArgPrefix); err != nil {
		// arg 表必须在 LUA_INIT、-e 和 -l 前写入，保持官方 CLI 启动顺序。
		return err
	}
	if !options.IgnoreEnvironment {
		// -E 模式跳过 LUA_INIT；普通模式执行启动初始化。
		if err := executeStartupInit(state); err != nil {
			// LUA_INIT 执行失败时停止后续启动项。
			return err
		}
	}
	if err := loadLibraries(state, options.Libraries); err != nil {
		// 任一 -l 模块加载失败时停止执行后续片段。
		return err
	}
	if err := executeExpressions(state, options.Expressions); err != nil {
		// 任一 -e 片段执行失败时停止后续交互。
		return err
	}
	scriptPath := options.ScriptPath
	if shouldExecuteImplicitStdin(options, streams.Stdin) {
		// 无脚本且 stdin 明确来自管道或文件时，兼容 Lua CLI 把 stdin 当作脚本执行。
		scriptPath = "-"
	}
	if err := executeScript(state, scriptPath, options.ScriptArgs, streams.Stdin); err != nil {
		// 脚本或 stdin 执行失败时停止后续交互。
		return err
	}
	enterInteractive := options.Interactive || shouldRunImplicitREPL(options, streams.Stdin)
	if enterInteractive {
		if !options.Version {
			// 进入交互解释器前输出版本 banner，对齐官方 lua 裸启动和 -i 行为。
			stdout := safeWriter(streams.Stdout)
			_, _ = fmt.Fprintln(stdout, VersionText)
		}
		// -i 或裸终端启动进入 REPL；REPL 内部错误写 stderr 并继续读取下一条输入。
		return runREPL(state, streams)
	}
	return nil
}

// enableSignalInterrupts 将宿主 SIGINT 转换为 Lua 级一次性中断。
//
// state 必须是当前 CLI 执行使用的 State；返回函数必须在 Run 退出时调用，以释放 signal.Notify
// 注册。第一次 Ctrl-C 不取消 context，因为 Lua 5.3 允许 pcall 捕获一次 Ctrl-C 错误后继续执行
// 后续语句；短时间内的后续 Ctrl-C 直接结束进程，用于打断长时间停留在 Go/C 函数内部、无法回到
// VM 检查点的执行。
func enableSignalInterrupts(state *lua.State) func() {
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt)
	done := make(chan struct{})
	stopped := make(chan struct{})
	var pendingSignals atomic.Int32
	go func() {
		defer close(stopped)
		for {
			// 信号 goroutine 只负责把 OS 信号转为 State 中断请求，不直接修改 VM 栈。
			select {
			case <-done:
				// Run 退出后停止监听，避免测试或多次 CLI 调用泄漏 goroutine。
				return
			case <-signalChannel:
				if pendingSignals.Add(1) > 1 {
					// 第二次 Ctrl-C 说明脚本可能卡在无法检查中断的 Go/C 风格长调用中，直接退出进程。
					os.Exit(ExitInterrupted)
				}
				// Ctrl-C 对齐 Lua 5.3 行为，下一次 VM 检查点抛出 interrupted!。
				state.RequestInterrupt()
				go func() {
					// 若首次中断已被 Lua 代码捕获并继续运行，稍后允许新的 Ctrl-C 再次走可捕获路径。
					timer := time.NewTimer(2 * time.Second)
					defer timer.Stop()
					select {
					case <-done:
						// Run 退出后无需重置中断窗口。
						return
					case <-timer.C:
						// 超出短窗口后恢复第一次 Ctrl-C 的 Lua 级可捕获语义。
						pendingSignals.Store(0)
					}
				}()
			}
		}
	}()
	return func() {
		// stopSignal 必须先注销 signal.Notify，再关闭 done，避免迟到信号阻塞发送。
		signal.Stop(signalChannel)
		close(done)
		<-stopped
	}
}

// ParseArgs 解析 glua 命令行参数。
//
// args 不包含程序名；支持 `-e stat`、`-estat`、`-l mod`、`-lmod`、`-i`、`-v`、`--`、脚本路径和脚本参数。
func ParseArgs(args []string) (Options, error) {
	// 从左到右解析选项，保持 Lua CLI 多个 -e/-l 的执行顺序。
	var options Options
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "-i" {
			// -i 只设置交互模式标记，不消耗额外参数。
			options.Interactive = true
			continue
		}
		if argument == "-v" {
			// -v 输出版本信息，不消耗额外参数。
			options.Version = true
			continue
		}
		if argument == "-E" {
			// -E 忽略 Lua 启动环境变量。
			options.IgnoreEnvironment = true
			continue
		}
		if argument == "--" {
			// -- 终止选项解析，后续第一个参数按脚本路径处理。
			if index+1 < len(args) {
				options.ScriptPath = args[index+1]
				options.ScriptArgs = append([]string(nil), args[index+2:]...)
				options.ArgPrefix = append([]string(nil), args[:index+1]...)
			}
			if err := validateListBytecodeOptions(options); err != nil {
				// 反汇编模式不能与 -- 后的脚本执行混用。
				return Options{}, err
			}
			return options, nil
		}
		if argument == "-e" {
			// 独立 -e 必须消耗下一个参数作为 Lua 片段。
			index++
			if index >= len(args) {
				// 缺少片段时返回明确参数错误。
				return Options{}, fmt.Errorf("'-e' needs argument")
			}
			options.Expressions = append(options.Expressions, args[index])
			continue
		}
		if len(argument) > 2 && argument[:2] == "-e" {
			// 紧凑 -estat 形式直接截取后缀作为 Lua 片段。
			options.Expressions = append(options.Expressions, argument[2:])
			continue
		}
		if argument == "-l" {
			// 独立 -l 必须消耗下一个参数作为模块名。
			index++
			if index >= len(args) {
				// 缺少模块名时返回明确参数错误。
				return Options{}, fmt.Errorf("'-l' needs argument")
			}
			options.Libraries = append(options.Libraries, args[index])
			continue
		}
		if len(argument) > 2 && argument[:2] == "-l" {
			// 紧凑 -lmod 形式直接截取后缀作为模块名。
			options.Libraries = append(options.Libraries, argument[2:])
			continue
		}
		if argument == "--glua-list-bytecode" {
			// glua 的反汇编能力使用项目命名空间长选项，避免抢占官方 lua 参数语义。
			index++
			if index >= len(args) {
				// 缺少输入路径时无法进行反汇编。
				return Options{}, fmt.Errorf("option --glua-list-bytecode requires an argument")
			}
			options.ListBytecodePath = args[index]
			continue
		}
		if strings.HasPrefix(argument, "--glua-list-bytecode=") {
			// 等号形式便于脚本调用，同样不影响官方 -l。
			options.ListBytecodePath = strings.TrimPrefix(argument, "--glua-list-bytecode=")
			if options.ListBytecodePath == "" {
				// 空路径没有可读取目标，直接返回参数错误。
				return Options{}, fmt.Errorf("option --glua-list-bytecode requires an argument")
			}
			continue
		}
		if argument == "--glua-format" {
			// --glua-format 支持 `--glua-format file` 与 `--glua-format -w file` 两种形式。
			consumed, err := applyFormatOption(&options, args[index+1:])
			if err != nil {
				// 缺少路径或重复模式时返回明确参数错误。
				return Options{}, err
			}
			index += consumed
			continue
		}
		if strings.HasPrefix(argument, "--glua-format=") {
			// 等号形式只指定输出到 stdout 的格式化目标。
			formatPath := strings.TrimPrefix(argument, "--glua-format=")
			if formatPath == "" {
				// 空路径没有可读取目标，直接返回参数错误。
				return Options{}, fmt.Errorf("option --glua-format requires an argument")
			}
			if options.FormatPath != "" {
				// 单次命令只允许一个格式化目标。
				return Options{}, fmt.Errorf("--glua-format can only be specified once")
			}
			options.FormatPath = formatPath
			continue
		}
		if argument == "--glua-syntax" {
			// --glua-syntax 必须消费后续模式名或扩展名列表。
			index++
			if index >= len(args) {
				// 缺少语法模式时无法决定编译配置。
				return Options{}, fmt.Errorf("option --glua-syntax requires an argument")
			}
			if err := applySyntaxOption(&options, args[index]); err != nil {
				// 语法扩展名称错误直接返回参数错误。
				return Options{}, err
			}
			continue
		}
		if strings.HasPrefix(argument, "--glua-syntax=") {
			// 等号形式便于脚本和 IDE 配置传参。
			if err := applySyntaxOption(&options, strings.TrimPrefix(argument, "--glua-syntax=")); err != nil {
				// 语法扩展名称错误直接返回参数错误。
				return Options{}, err
			}
			continue
		}
		if argument == "--glua-disable-syntax" {
			// --glua-disable-syntax 必须消费后续扩展名列表。
			index++
			if index >= len(args) {
				// 缺少禁用列表时返回明确参数错误。
				return Options{}, fmt.Errorf("option --glua-disable-syntax requires an argument")
			}
			if err := applyDisableSyntaxOption(&options, args[index]); err != nil {
				// 禁用列表名称错误直接返回参数错误。
				return Options{}, err
			}
			continue
		}
		if strings.HasPrefix(argument, "--glua-disable-syntax=") {
			// 等号形式与 --glua-syntax 保持一致。
			if err := applyDisableSyntaxOption(&options, strings.TrimPrefix(argument, "--glua-disable-syntax=")); err != nil {
				// 禁用列表名称错误直接返回参数错误。
				return Options{}, err
			}
			continue
		}
		if argument == "-" {
			// 单独 - 表示从 stdin 读取脚本，后续参数写入 arg 表。
			options.ScriptPath = "-"
			options.ScriptArgs = append([]string(nil), args[index+1:]...)
			options.ArgPrefix = append([]string(nil), args[:index+1]...)
			if err := validateListBytecodeOptions(options); err != nil {
				// 反汇编模式不能与 stdin 脚本执行混用。
				return Options{}, err
			}
			return options, nil
		}
		if len(argument) > 0 && argument[0] == '-' {
			// 当前阶段只支持已实现选项，其他选项留到后续 TODO。
			return Options{}, fmt.Errorf("unrecognized option '%s'", argument)
		}

		// 第一个非选项参数作为脚本路径，剩余参数作为脚本 arg。
		options.ScriptPath = argument
		options.ScriptArgs = append([]string(nil), args[index+1:]...)
		options.ArgPrefix = append([]string(nil), args[:index]...)
		if err := validateListBytecodeOptions(options); err != nil {
			// 反汇编模式不能与脚本执行混用。
			return Options{}, err
		}
		return options, nil
	}
	if err := validateListBytecodeOptions(options); err != nil {
		// 反汇编模式与执行模式冲突时返回明确参数错误。
		return Options{}, err
	}
	return options, nil
}

// applySyntaxOption 将 --glua-syntax 参数写入 Options。
//
// value 支持 lua53、extended、all 或逗号分隔扩展名；解析失败时返回可展示错误。
func applySyntaxOption(options *Options, value string) error {
	// 复用 extensions 注册表，避免 CLI 维护第二份扩展名称。
	syntaxSet, err := extensions.ParseSyntaxSet(value)
	if err != nil {
		// 参数非法时保留原始解析错误。
		return err
	}
	options.SyntaxExtensions = syntaxSet
	options.SyntaxExtensionsSet = true
	return nil
}

// applyDisableSyntaxOption 将 --glua-disable-syntax 参数追加到 Options。
//
// value 只接受逗号分隔扩展名；多个 --glua-disable-syntax 会合并禁用集合。
func applyDisableSyntaxOption(options *Options, value string) error {
	// 禁用列表与显式 --glua-syntax 可叠加，最终在 syntaxForOptions 中统一扣除。
	disabledSet, err := extensions.ParseDisabledSyntaxSet(value)
	if err != nil {
		// 参数非法时保留原始解析错误。
		return err
	}
	options.DisabledSyntaxExtensions = options.DisabledSyntaxExtensions.With(disabledSet)
	return nil
}

// applyFormatOption 解析 --glua-format 后续参数。
//
// args 是 --glua-format 之后尚未消费的参数；返回值表示消费了几个后续参数。
func applyFormatOption(options *Options, args []string) (int, error) {
	if options.FormatPath != "" {
		// 单次命令只允许一个格式化目标，避免多个输出顺序不明确。
		return 0, fmt.Errorf("--glua-format can only be specified once")
	}
	if len(args) == 0 {
		// 缺少文件路径时无法执行格式化。
		return 0, fmt.Errorf("option --glua-format requires an argument")
	}
	if args[0] == "-w" {
		// `--glua-format -w file` 表示原地写回。
		if len(args) < 2 {
			// -w 后必须跟待格式化文件路径。
			return 0, fmt.Errorf("option --glua-format -w requires a file")
		}
		options.FormatWrite = true
		options.FormatPath = args[1]
		return 2, nil
	}
	if strings.HasPrefix(args[0], "-") {
		// --glua-format 后除 -w 外不接受其他选项，避免把路径缺失误判为脚本参数。
		return 0, fmt.Errorf("option --glua-format requires a file")
	}
	options.FormatPath = args[0]
	return 1, nil
}

// syntaxForOptions 计算 glua 当前命令最终使用的语法扩展集合。
func syntaxForOptions(options Options) extensions.SyntaxSet {
	// 未显式指定 --glua-syntax 时使用当前构建默认集合。
	syntaxSet := extensions.Default()
	if options.SyntaxExtensionsSet {
		// 显式 --glua-syntax 覆盖默认集合。
		syntaxSet = options.SyntaxExtensions
	}
	if options.DisabledSyntaxExtensions != 0 {
		// --glua-disable-syntax 在最终集合上扣除指定扩展，允许 extended 默认开再局部关闭。
		syntaxSet = syntaxSet.Without(options.DisabledSyntaxExtensions)
	}
	return syntaxSet & extensions.Compiled()
}

// validateListBytecodeOptions 校验 glua 可选反汇编模式不破坏官方 lua 参数语义。
//
// `--glua-list-bytecode` 只能与 `-v` 共存；不能与官方 `-l` 模块加载、`-e`、脚本路径或 `-i`
// 混用，避免用户把 Lua CLI 入口误当成 luac 入口。
func validateListBytecodeOptions(options Options) error {
	// 未启用反汇编模式时没有额外冲突。
	if options.ListBytecodePath == "" && options.FormatPath == "" {
		// 空路径表示普通 glua 执行路径。
		return nil
	}
	if options.ListBytecodePath != "" && options.FormatPath != "" {
		// 反汇编和格式化都是独立文件模式，不能同时执行。
		return fmt.Errorf("--glua-list-bytecode cannot be combined with --glua-format")
	}
	if len(options.Libraries) > 0 || len(options.Expressions) > 0 || options.ScriptPath != "" || options.Interactive {
		// 调试文件模式与官方执行参数混用会造成执行和查看的顺序歧义，直接拒绝。
		if options.FormatPath != "" {
			return fmt.Errorf("--glua-format cannot be combined with -l, -e, -i, or script execution")
		}
		return fmt.Errorf("--glua-list-bytecode cannot be combined with -l, -e, -i, or script execution")
	}
	return nil
}

// runBytecodeList 执行 glua 可选反汇编输出。
//
// path 可指向 Lua 源码或 Lua 5.3 binary chunk；输出使用 bytecode.DisassembleProto 的项目内
// 稳定格式，不承诺与官方 luac -l 完全一致。
func runBytecodeList(path string, stdout io.Writer, syntax extensions.SyntaxSet) error {
	// 先读取输入文件，再根据 Lua chunk 签名决定加载或编译。
	inputBytes, err := os.ReadFile(path)
	if err != nil {
		// 文件读取失败保留 os.PathError，便于调用方定位权限和路径问题。
		return err
	}
	proto, err := protoFromListInput(inputBytes, "@"+path, syntax)
	if err != nil {
		// 编译或加载失败时不输出部分反汇编。
		return err
	}
	_, err = fmt.Fprint(safeWriter(stdout), bytecode.DisassembleProto(proto))
	if err != nil {
		// stdout 写入失败时向上传播，命令入口会返回失败退出码。
		return err
	}
	return nil
}

// runFormat 格式化指定 Lua 源码文件。
//
// writeBack 为 true 时把结果写回原文件；否则写入 stdout。源码会先按 glua 当前语法扩展集合
// 编译校验，确保 switch/continue 等扩展语法与实际运行模式一致。
func runFormat(path string, writeBack bool, stdout io.Writer, syntax extensions.SyntaxSet) error {
	inputBytes, err := os.ReadFile(path)
	if err != nil {
		// 文件读取失败保留 os.PathError，便于调用方定位权限和路径问题。
		return err
	}
	source := lexer.StripInitialShebang(string(inputBytes))
	options := lua.WithSyntaxExtensions(lua.DefaultOptions(), syntax)
	state := lua.NewStateWithOptions(options)
	defer state.Close()
	if err := lua.LoadString(state, source, "@"+path); err != nil {
		// 语法错误使用 lua.SyntaxError 结构返回，CLI 会输出源码指针诊断。
		return err
	}
	formatted, err := formatter.Format(source, syntax)
	if err != nil {
		// 理论上前置校验已覆盖 parser 错误；这里保留防御式返回。
		return err
	}
	if writeBack {
		// 写回时尽量保留原文件权限。
		info, statErr := os.Stat(path)
		if statErr != nil {
			// 无法读取权限时返回原始文件状态错误。
			return statErr
		}
		return os.WriteFile(path, []byte(formatted), info.Mode().Perm())
	}
	_, err = fmt.Fprint(safeWriter(stdout), formatted)
	if err != nil {
		// stdout 写入失败时向上传播，命令入口会返回失败退出码。
		return err
	}
	return nil
}

// protoFromListInput 从 glua 反汇编输入中取得 Proto。
//
// input 以 Lua binary chunk 签名开头时使用 bytecode loader；否则按 Lua 源码解析和 codegen。
func protoFromListInput(input []byte, chunkName string, syntax extensions.SyntaxSet) (*bytecode.Proto, error) {
	// 通过固定签名区分 binary chunk 和源码，避免新增用户模式参数。
	if bytes.HasPrefix(input, []byte(bytecode.ChunkSignature)) {
		// binary chunk 直接加载 Proto。
		return bytecode.LoadBinaryChunk(bytes.NewReader(input))
	}
	chunkParser := parser.NewWithSyntax(string(input), syntax)
	chunk, err := chunkParser.ParseChunk()
	if err != nil {
		// 源码解析失败时返回 parser 错误。
		return nil, err
	}
	proto, err := codegen.CompileChunk(chunk, chunkName)
	if err != nil {
		// codegen 失败时返回原始错误。
		return nil, err
	}
	return proto, nil
}

// loadLibraries 按顺序执行 `-l mod` 模块加载。
//
// state 必须非 nil；modules 中每个名称会通过全局 require 调用加载，返回错误时停止。
func loadLibraries(state *lua.State, modules []string) error {
	if state == nil {
		// nil State 无法调用 require。
		return lua.ErrNilState
	}
	for _, moduleName := range modules {
		// 每个 -l 都通过 require 加载，保持 Lua CLI 的模块加载入口一致。
		if moduleName == "" {
			// 空模块名没有稳定 require key，返回明确错误。
			return fmt.Errorf("module name is empty")
		}
		requireValue, err := lua.GetGlobal(state, "require")
		if err != nil {
			// require 读取失败时返回底层错误。
			return err
		}
		results, err := lua.Call(state, requireValue, runtime.StringValue(moduleName))
		if err != nil {
			// require 执行失败时保留 Lua runtime error。
			return err
		}
		if len(results) > 0 {
			// Lua 5.3 CLI 的 -l 会把 require 返回值写入同名全局，供后续脚本按模块名读取。
			if err := lua.SetGlobal(state, moduleName, results[0]); err != nil {
				// 全局写入失败说明 State 已不可用，停止继续加载后续模块。
				return err
			}
		}
	}
	return nil
}

// executeExpressions 按顺序执行 `-e stat` 片段。
//
// state 必须非 nil；当前 Lua closure 执行器尚未完整接入，因此合法片段会返回阶段性执行错误。
func executeExpressions(state *lua.State, expressions []string) error {
	if state == nil {
		// nil State 无法加载或执行片段。
		return lua.ErrNilState
	}
	for _, expression := range expressions {
		// 每个 -e 都作为独立 chunk 执行，保持错误定位和 Lua CLI 行为接近。
		if err := lua.DoString(state, expression); err != nil {
			// 完整执行器未接入时允许 ErrExecutionUnavailable 透出，避免误报执行成功。
			return err
		}
	}
	return nil
}

// executeStartupInit 执行 Lua 5.3 启动环境变量 LUA_INIT。
//
// LUA_INIT_5_3 优先于 LUA_INIT；值以 `@` 开头时按文件路径执行，否则按 Lua 源码片段执行。
// 空字符串表示显式禁用初始化，不执行任何 chunk。
func executeStartupInit(state *lua.State) error {
	if state == nil {
		// nil State 无法执行初始化脚本。
		return lua.ErrNilState
	}
	initChunk, ok := os.LookupEnv("LUA_INIT_5_3")
	if !ok {
		// 版本专用变量不存在时回退到通用 LUA_INIT。
		initChunk, ok = os.LookupEnv("LUA_INIT")
	}
	if !ok || initChunk == "" {
		// 未设置或显式空值都表示不执行启动初始化。
		return nil
	}
	if strings.HasPrefix(initChunk, "@") {
		// @file 形式按文件加载，保留官方 Lua 启动初始化文件语义。
		return lua.DoFile(state, strings.TrimPrefix(initChunk, "@"))
	}
	if err := lua.DoString(state, initChunk); err != nil {
		// 字符串形式 LUA_INIT 的运行错误需要带固定 chunk 名称和行号，匹配官方 CLI 诊断。
		errorText := strings.TrimPrefix(luaErrorText(err), "(string):1: ")
		return fmt.Errorf("LUA_INIT:1: %s", errorText)
	}
	return nil
}

// luaErrorText 提取 Lua error 对象的可读文本。
//
// Lua runtime 错误通常携带 ErrorObject；若没有字符串对象，则退回 Go error 文本，避免丢失
// 宿主错误上下文。
func luaErrorText(err error) string {
	if err == nil {
		// nil 错误没有可读文本。
		return ""
	}
	errorObject := runtime.ErrorObject(err)
	if errorObject.Kind == runtime.KindString {
		// 字符串错误对象是 Lua error 最常见路径。
		return errorObject.String
	}
	if errorObject.Kind == runtime.KindTable && !hasToStringMetamethod(errorObject) {
		// Lua CLI 对没有可用 __tostring 的 table error object 使用固定诊断文本。
		return "error object is a table value"
	}
	if converted, convertErr := runtime.ToString(errorObject); convertErr == nil && converted.Kind == runtime.KindString {
		// 非字符串 Lua error object 需要按 tostring 语义展示，保留 __tostring 元方法兼容性。
		return converted.String
	}
	return err.Error()
}

// hasToStringMetamethod 判断 table error object 是否声明了 `__tostring`。
//
// value 必须是 table 才可能返回 true；该 helper 只做 raw 元表读取，不调用元方法，避免错误展示路径
// 产生额外副作用。
func hasToStringMetamethod(value runtime.Value) bool {
	if value.Kind != runtime.KindTable {
		// 非 table 不走 table error object 特殊文案。
		return false
	}
	table, ok := value.Ref.(*runtime.Table)
	if !ok || table == nil {
		// 损坏引用不能安全读取元表。
		return false
	}
	metatable := table.GetMetatable()
	if metatable == nil {
		// 没有元表就没有 __tostring。
		return false
	}
	method := metatable.RawGetString("__tostring")
	return method.Kind == runtime.KindGoClosure || method.Kind == runtime.KindLuaClosure
}

// normalizeLuaErrorObject 在 State 存活期间把 Lua error object 转成 CLI 可打印错误。
//
// state 必须非 nil；err 为 nil 时直接返回 nil。非 string 错误对象若带 table `__tostring`
// 元方法，会在当前 State 上调用该元方法并把返回字符串重新包装为 Lua error object；转换失败时保留
// 原错误，避免错误展示路径掩盖真实运行时失败。
func normalizeLuaErrorObject(state *lua.State, err error) error {
	if err == nil {
		// 没有错误时不需要转换。
		return nil
	}
	errorObject := runtime.ErrorObject(err)
	if errorObject.Kind == runtime.KindString {
		// 字符串错误对象已是 CLI 可打印文本。
		return err
	}
	textValue, ok := luaErrorObjectToString(state, errorObject)
	if !ok {
		// 无法安全转换时保留原始错误对象，避免改变错误传播语义。
		return err
	}
	return lua.RaiseError(textValue)
}

// luaErrorObjectToString 调用 error object 的 `__tostring` 元方法。
//
// state 必须非 nil；object 当前只处理 table error object，因为官方 suite 的 CLI 错误兼容点依赖
// `error(table)` 与 table 元表。元方法必须返回 string，否则视为不可转换并交回原错误路径。
func luaErrorObjectToString(state *lua.State, object runtime.Value) (runtime.Value, bool) {
	if state == nil {
		// nil State 不能执行 Lua closure 元方法。
		return runtime.NilValue(), false
	}
	if object.Kind != runtime.KindTable {
		// 当前只处理 table error object，其他类型交由既有 luaErrorText 兜底。
		return runtime.NilValue(), false
	}
	errorTable, ok := object.Ref.(*runtime.Table)
	if !ok || errorTable == nil {
		// 损坏的 table 引用不能安全读取元表。
		return runtime.NilValue(), false
	}
	metatable := errorTable.GetMetatable()
	if metatable == nil {
		// 没有元表就没有 `__tostring` 转换入口。
		return runtime.NilValue(), false
	}
	method := metatable.RawGetString("__tostring")
	switch method.Kind {
	case runtime.KindGoClosure, runtime.KindLuaClosure:
		syntheticFrameCount := pushLuaErrorToStringFrames(state)
		// Go/Lua 元方法都通过 lua.Call 进入统一调用边界，便于支持 Lua closure。
		results, err := lua.Call(state, method, object)
		if err != nil {
			// 元方法自身失败时不覆盖原始错误对象。
			return runtime.NilValue(), false
		}
		popSyntheticFrames(state, syntheticFrameCount)
		if len(results) == 0 || results[0].Kind != runtime.KindString {
			// Lua 5.3 要求 __tostring 返回字符串；不满足时保留原错误。
			return runtime.NilValue(), false
		}
		return results[0], true
	default:
		// 非 callable 元方法不能作为错误文本转换函数。
		return runtime.NilValue(), false
	}
}

// pushLuaErrorToStringFrames 为 CLI 错误对象 tostring 临时补齐错误处理调用层。
//
// Lua 5.3 官方 CLI 在打印非字符串 error object 时会通过内部错误处理路径调用 `__tostring`；
// 当前 Go CLI 直接在 protected call 返回前转换错误对象，因此需要补一个 Go 帧，让
// `debug.getinfo(4)` 这类官方测试能落到原脚本出错帧。返回值表示成功压入的帧数。
func pushLuaErrorToStringFrames(state *lua.State) int {
	if state == nil {
		// nil State 不能维护调用栈。
		return 0
	}
	if state.CallDepth() == 0 {
		// 没有原始错误帧时无需合成中间错误处理层。
		return 0
	}
	syntheticFrame := runtime.NewGoCallFrame(runtime.ReferenceValue(runtime.KindGoClosure, "cli error handler"), state.StackTop()+1, 0)
	pushedCount := 0
	for pushedCount < 1 {
		// 合成一个 Go 帧，对齐官方错误展示时 __tostring 到出错 chunk 之间的层级距离。
		if err := state.PushCallFrame(syntheticFrame); err != nil {
			// 调用深度耗尽时停止补帧，后续转换仍可按现有栈尽力执行。
			break
		}
		pushedCount++
	}
	return pushedCount
}

// popSyntheticFrames 弹出 CLI 错误对象 tostring 成功路径补入的临时帧。
//
// count 来自 pushLuaErrorToStringFrames；调用方只在 `__tostring` 成功返回后使用。若弹出失败，
// 说明调用栈已被更内层错误打断，保护边界会在外层统一恢复。
func popSyntheticFrames(state *lua.State, count int) {
	if state == nil {
		// nil State 无需恢复。
		return
	}
	for remainingCount := count; remainingCount > 0; remainingCount-- {
		// 按 LIFO 弹出临时帧，恢复到调用 __tostring 前的栈深度。
		if _, err := state.PopCallFrame(); err != nil {
			// 栈已不可恢复时停止，避免继续弹出真实业务帧。
			return
		}
	}
}

// withIgnoredLuaEnvironment 临时屏蔽 Lua 启动相关环境变量并执行 action。
//
// 该 helper 用于 CLI `-E` 模式；它只清理 Lua 5.3 会读取的 INIT/PATH/CPATH 变量，并在
// action 结束后恢复原值，避免污染宿主进程或其他测试。
func withIgnoredLuaEnvironment(action func() error) error {
	if action == nil {
		// nil action 没有可执行内容，直接成功返回。
		return nil
	}
	luaEnvironmentNames := []string{
		"LUA_INIT_5_3",
		"LUA_INIT",
		"LUA_PATH_5_3",
		"LUA_PATH",
		"LUA_CPATH_5_3",
		"LUA_CPATH",
	}
	type environmentValue struct {
		// value 保存变量原值。
		value string
		// exists 标记变量是否原本存在。
		exists bool
	}
	savedValues := make(map[string]environmentValue, len(luaEnvironmentNames))
	for _, name := range luaEnvironmentNames {
		// 逐项保存并删除，确保 package.Open 和 LUA_INIT 都看不到这些变量。
		value, exists := os.LookupEnv(name)
		savedValues[name] = environmentValue{value: value, exists: exists}
		_ = os.Unsetenv(name)
	}
	defer func() {
		for _, name := range luaEnvironmentNames {
			// action 结束后恢复原始环境，避免影响同进程后续测试。
			saved := savedValues[name]
			if saved.exists {
				// 原本存在的变量恢复原值。
				_ = os.Setenv(name, saved.value)
			} else {
				// 原本不存在的变量保持不存在。
				_ = os.Unsetenv(name)
			}
		}
	}()
	return action()
}

// executeScript 执行脚本文件或显式 stdin 脚本。
//
// scriptPath 为空时不执行；scriptPath 为 "-" 时从 stdin 读取源码，其余值按文件路径加载执行。
// scriptArgs 会作为脚本 chunk 的 vararg `...` 传入。
func executeScript(state *lua.State, scriptPath string, scriptArgs []string, stdin io.Reader) error {
	if state == nil {
		// nil State 无法执行脚本。
		return lua.ErrNilState
	}
	if scriptPath == "" {
		// 没有脚本路径时保持空操作，允许纯 -v/-i/-e 调用。
		return nil
	}
	if scriptPath == "-" {
		// 显式 - 从 stdin 读取完整 chunk，避免无参数时阻塞真实终端。
		sourceBytes, err := io.ReadAll(safeReader(stdin))
		if err != nil {
			// stdin 读取错误直接返回。
			return err
		}
		return executeLoadedScript(state, func() error {
			// stdin chunk 使用稳定名称，保持与 DoString 接近的诊断语义。
			return lua.LoadString(state, string(sourceBytes), "=stdin")
		}, scriptArgs)
	}

	// 普通脚本路径走 LoadFile，保留 @path chunk 名称和文件读取错误，同时传入脚本参数。
	return executeLoadedScript(state, func() error {
		return lua.LoadFile(state, scriptPath)
	}, scriptArgs)
}

// executeLoadedScript 在 protected call 边界内执行已加载脚本。
//
// loader 必须把 Lua closure 压入 state 栈顶；scriptArgs 会转换为 Lua string vararg 传给 chunk。
func executeLoadedScript(state *lua.State, loader func() error, scriptArgs []string) error {
	return lua.ProtectedCall(state, func(callState *lua.State) error {
		// 先加载 chunk，确保读取、解析和 codegen 错误仍位于 protected call 边界内。
		if err := loader(); err != nil {
			return err
		}
		closureValue, err := callState.Pop()
		if err != nil {
			// loader 成功后栈顶必须是 Lua closure。
			return err
		}
		arguments, err := scriptArgumentValues(callState, scriptArgs)
		if err != nil {
			// arg 表损坏时按 Lua CLI 启动错误终止脚本。
			return err
		}
		_, err = lua.Call(callState, closureValue, arguments...)
		if err != nil {
			// 脚本错误对象需要在 State 关闭前完成 __tostring 转换，供外层 CLI 稳定打印。
			return normalizeLuaErrorObject(callState, err)
		}
		return nil
	})
}

// scriptArgumentValues 将 CLI 脚本参数转换为 Lua vararg 值。
//
// 优先读取当前全局 arg 表的正整数项，保留 -e 或 LUA_INIT 对 arg 的修改；arg 表不可用时使用
// 解析阶段保存的脚本参数字符串作为兜底。
func scriptArgumentValues(state *lua.State, scriptArgs []string) ([]runtime.Value, error) {
	argValue := state.GetGlobal("arg")
	if argValue.Kind == runtime.KindTable {
		// arg 表存在时，按正整数连续区间生成脚本 chunk 的 vararg。
		if argTable, ok := argValue.Ref.(*runtime.Table); ok && argTable != nil {
			values := make([]runtime.Value, 0)
			for index := int64(1); ; index++ {
				// 遇到 nil 表示连续脚本参数结束。
				value := argTable.RawGetInteger(index)
				if value.IsNil() {
					break
				}
				values = append(values, value)
			}
			return values, nil
		}
		// 损坏的 table 引用不能继续作为 arg 表使用。
		return nil, lua.RaiseError(runtime.StringValue("'arg' is not a table"))
	}
	if !argValue.IsNil() {
		// Lua 5.3 CLI 在脚本执行前要求 arg 仍是 table。
		return nil, lua.RaiseError(runtime.StringValue("'arg' is not a table"))
	}

	values := make([]runtime.Value, 0, len(scriptArgs))
	for _, argument := range scriptArgs {
		// CLI 参数不做类型推断，全部保持 string。
		values = append(values, runtime.StringValue(argument))
	}
	return values, nil
}

// shouldExecuteImplicitStdin 判断无脚本参数时是否应把 stdin 当作 Lua chunk 执行。
//
// 仅在没有显式脚本、没有 -e/-l、没有 -i，且 stdin 不是终端时返回 true；这样兼容
// `echo "print(1)" | lua`，同时避免裸 `glua` 在交互终端中意外阻塞等待 EOF。
func shouldExecuteImplicitStdin(options Options, stdin io.Reader) bool {
	if options.ScriptPath != "" || options.Interactive || len(options.Expressions) > 0 || len(options.Libraries) > 0 {
		// 已有显式执行目标或交互模式时，stdin 不再隐式作为脚本。
		return false
	}
	if stdin == nil {
		// nil stdin 表示调用方未提供输入流，按无输入处理。
		return false
	}
	file, ok := stdin.(*os.File)
	if !ok {
		// 测试 reader、bytes.Buffer、strings.Reader 等非 os.File 输入视为可直接读取的管道数据。
		return true
	}
	stat, err := file.Stat()
	if err != nil {
		// 无法判断文件类型时保持保守，避免在未知终端上阻塞。
		return false
	}
	if stat.Mode()&os.ModeCharDevice != 0 {
		// 字符设备通常是交互终端，裸命令不隐式读取。
		return false
	}
	return true
}

// shouldRunImplicitREPL 判断无执行目标且 stdin 为终端时是否应进入 REPL。
//
// Lua 5.3 官方 CLI 在裸 `lua` 连接交互终端时进入交互解释器；但 `-v`、`-e`、`-l`、脚本、
// 反汇编和管道 stdin 都有明确执行语义，不能被隐式 REPL 改写。
func shouldRunImplicitREPL(options Options, stdin io.Reader) bool {
	if options.ScriptPath != "" || options.Interactive || options.Version || options.ListBytecodePath != "" {
		// 已有脚本、显式交互、版本专用输出或反汇编模式时，不启用裸命令 REPL。
		return false
	}
	if len(options.Expressions) > 0 || len(options.Libraries) > 0 {
		// -e 和 -l 代表已有启动执行目标，官方语义下不会因为终端 stdin 自动追加 REPL。
		return false
	}
	file, ok := stdin.(*os.File)
	if !ok {
		// 非 os.File reader 通常是测试或管道输入，应交给隐式 stdin 脚本逻辑处理。
		return false
	}
	stat, err := file.Stat()
	if err != nil {
		// 无法判断文件类型时保持非交互，避免在未知输入上阻塞。
		return false
	}
	if stat.Mode()&os.ModeCharDevice == 0 {
		// 普通文件或管道 stdin 应按脚本执行，不进入交互提示。
		return false
	}
	return true
}

// runREPL 执行第一阶段交互式读取循环。
//
// stdin 按行读取；真实终端会启用最小行编辑以支持左右方向键；stdout 输出 `> ` 和 `>> ` 提示；
// stderr 输出语法或运行错误并继续下一条输入。
func runREPL(state *lua.State, streams Streams) error {
	if state == nil {
		// nil State 无法执行 REPL 输入。
		return lua.ErrNilState
	}

	// REPL 只依赖传入流，便于测试 stdout/stderr 分离。
	stdout := safeWriter(streams.Stdout)
	stderr := safeWriter(streams.Stderr)
	lineReader, restoreTerminal := newREPLLineReader(streams.Stdin, stdout)
	defer restoreTerminal()
	var pendingLines []string
	for {
		var prompt string
		if len(pendingLines) == 0 {
			// 新 chunk 使用主提示符。
			prompt = replPrompt(state, "_PROMPT", "> ")
		} else {
			// 已有未完成 chunk 时使用续行提示符。
			prompt = replPrompt(state, "_PROMPT2", ">> ")
		}
		line, ok, err := lineReader.readLine(prompt)
		if err != nil {
			// 底层输入流错误需要返回给 Main 转换退出码。
			return err
		}
		if !ok {
			// 输入结束时退出 REPL，保留已经输出的最后一个提示符。
			break
		}

		// 追加当前行并判断是否需要继续补全多行块。
		pendingLines = append(pendingLines, line)
		source := strings.Join(pendingLines, "\n")
		if isIncompleteREPLSource(source) {
			// 当前 chunk 仍有未闭合块，继续读取下一行。
			continue
		}

		if err := executeREPLChunk(state, source); err != nil {
			if _, ok := luaExitError(err); ok {
				// os.exit 是用户明确请求结束解释器，交给 Main 映射退出码，不能被 REPL 错误恢复吞掉。
				return err
			}
			// REPL 错误写 stderr 后恢复下一条输入，不中断进程。
			_, _ = fmt.Fprintln(stderr, replErrorText(state, err))
		}
		pendingLines = nil
	}
	if len(pendingLines) > 0 {
		// EOF 时仍有未完成 chunk，按语法错误输出并成功结束 REPL。
		_, _ = fmt.Fprintln(stderr, "incomplete input")
	}
	return nil
}

// replErrorText 返回 REPL 模式下的错误展示文本。
//
// Lua 5.3 交互解释器对运行时错误会打印 traceback；语法错误和普通宿主错误仍只打印主错误文本。
func replErrorText(state *lua.State, err error) string {
	message := luaErrorText(err)
	if !lua.IsRuntimeError(err) {
		// 非运行时错误不拼接 traceback，避免语法错误输出过度噪声。
		return message
	}
	location := replErrorLocation(message)
	if strings.HasPrefix(location, "stdin:") {
		// 交互顶层 chunk 需要贴近官方 lua 文案，不能使用 runtime 的内部帧类型展示。
		return fmt.Sprintf("%s\nstack traceback:\n\t%s: in main chunk\n\t[C]: in ?", message, location)
	}
	frames := state.TracebackFrames()
	if len(frames) > 0 {
		// 调用帧仍可见时使用 runtime 统一 traceback 格式。
		return runtime.Traceback(message, frames)
	}
	// REPL 顶层 chunk 错误在返回到交互循环时通常已回滚调用帧，需合成官方最小 traceback。
	return fmt.Sprintf("%s\nstack traceback:\n\t%s: in main chunk\n\t[C]: in ?", message, location)
}

// replErrorLocation 从错误消息中提取 REPL 顶层 chunk 位置。
//
// Lua 运行时错误通常以 `stdin:line:` 开头；提取失败时回退到官方交互模式的 stdin:1。
func replErrorLocation(message string) string {
	firstColon := strings.Index(message, ":")
	if firstColon <= 0 {
		// 缺少 source 前缀时只能回退到交互输入首行。
		return "stdin:1"
	}
	rest := message[firstColon+1:]
	secondColon := strings.Index(rest, ":")
	if secondColon <= 0 {
		// 缺少 line 前缀时只能回退到交互输入首行。
		return "stdin:1"
	}
	lineText := rest[:secondColon]
	for _, lineRune := range lineText {
		if lineRune < '0' || lineRune > '9' {
			// 非数字行号不是 Lua source:line 前缀。
			return "stdin:1"
		}
	}
	return message[:firstColon+1+secondColon]
}

// replLineReader 抽象 REPL 单行读取能力。
//
// scannerREPLLineReader 用于管道、测试 reader 和 raw mode 不可用的终端；terminalREPLLineReader 用于
// 支持 ANSI 控制序列的真实终端输入。
type replLineReader interface {
	readLine(prompt string) (string, bool, error)
}

// newREPLLineReader 根据输入流选择 REPL 行读取器。
//
// stdin/stdout 都是终端且 raw mode 可用时启用最小行编辑；否则回退到 Scanner，保持非终端输入兼容。
func newREPLLineReader(stdin io.Reader, stdout io.Writer) (replLineReader, func()) {
	if file, ok := stdin.(*os.File); ok {
		// 只有真实文件描述符才可能切换终端 raw mode。
		restore, rawOK := makeRawTerminal(file)
		if rawOK {
			// raw mode 成功后按字节处理方向键、退格和插入。
			return &terminalREPLLineReader{
				reader: bufio.NewReader(file),
				stdout: stdout,
			}, restore
		}
	}
	return &scannerREPLLineReader{
		scanner: bufio.NewScanner(safeReader(stdin)),
		stdout:  stdout,
	}, func() {}
}

// scannerREPLLineReader 使用标准 Scanner 读取一行。
//
// 该路径用于非终端输入，保留原有测试和管道执行行为。
type scannerREPLLineReader struct {
	scanner *bufio.Scanner
	stdout  io.Writer
}

// readLine 输出提示符并读取下一行。
//
// 返回 ok=false 表示 EOF；Scanner 内部错误通过 error 返回。
func (reader *scannerREPLLineReader) readLine(prompt string) (string, bool, error) {
	_, _ = fmt.Fprint(reader.stdout, prompt)
	if !reader.scanner.Scan() {
		// Scanner 到达 EOF 或遇到读取错误时结束当前 REPL。
		if err := reader.scanner.Err(); err != nil {
			// 读取错误需要返回给上层转成 CLI 失败。
			return "", false, err
		}
		return "", false, nil
	}
	return reader.scanner.Text(), true, nil
}

// terminalREPLLineReader 在 raw mode 终端上实现最小行编辑。
//
// 当前支持左右方向键、Home/End、Delete、Backspace、Ctrl-D 和中间插入，覆盖官方 lua 常见交互输入。
type terminalREPLLineReader struct {
	reader *bufio.Reader
	stdout io.Writer
}

// readLine 输出提示符并在 raw mode 中读取一行。
//
// 返回 ok=false 表示用户在空行按 Ctrl-D 或输入 EOF；错误仅表示底层读写失败。
func (reader *terminalREPLLineReader) readLine(prompt string) (string, bool, error) {
	_, _ = fmt.Fprint(reader.stdout, prompt)
	var buffer []rune
	cursor := 0
	for {
		inputRune, _, err := reader.reader.ReadRune()
		if err != nil {
			// EOF 代表终端输入结束；其他错误交给上层处理。
			if errors.Is(err, io.EOF) {
				return "", false, nil
			}
			return "", false, err
		}
		switch inputRune {
		case '\r', '\n':
			// 回车提交当前行，并显式换行以补偿 raw mode 不自动回显。
			_, _ = fmt.Fprint(reader.stdout, "\r\n")
			return string(buffer), true, nil
		case '\x04':
			// Ctrl-D 在空行退出 REPL；非空行保留当前编辑内容。
			if len(buffer) == 0 {
				return "", false, nil
			}
		case '\x03':
			// Ctrl-C 在交互输入阶段直接停止 REPL，匹配官方解释器的中断体验。
			_, _ = fmt.Fprint(reader.stdout, "\r\n")
			return "", false, errCLIInterrupted
		case '\x1b':
			// ANSI escape 序列承载方向键等编辑命令。
			nextCursor, nextBuffer, err := reader.consumeEscape(prompt, buffer, cursor)
			if err != nil {
				// escape 读取失败说明底层输入不可用。
				return "", false, err
			}
			cursor = nextCursor
			buffer = nextBuffer
		case '\b', '\x7f':
			if cursor > 0 {
				// 删除光标左侧字符后重绘当前行。
				copy(buffer[cursor-1:], buffer[cursor:])
				buffer = buffer[:len(buffer)-1]
				cursor--
				if err := redrawREPLLine(reader.stdout, prompt, buffer, cursor); err != nil {
					// 输出失败时停止 REPL。
					return "", false, err
				}
			}
		default:
			if inputRune >= ' ' {
				// 普通可打印字符插入到当前光标位置。
				buffer = append(buffer, 0)
				copy(buffer[cursor+1:], buffer[cursor:])
				buffer[cursor] = inputRune
				cursor++
				if err := redrawREPLLine(reader.stdout, prompt, buffer, cursor); err != nil {
					// 输出失败时停止 REPL。
					return "", false, err
				}
			}
		}
	}
}

// consumeEscape 处理 ANSI escape 编辑序列。
//
// 只识别 REPL 需要的方向键和删除键；未知序列会被忽略，避免污染 Lua 源码输入。
func (reader *terminalREPLLineReader) consumeEscape(prompt string, buffer []rune, cursor int) (int, []rune, error) {
	prefix, _, err := reader.reader.ReadRune()
	if err != nil {
		// escape 后续字节读取失败时把错误返回给上层。
		return cursor, buffer, err
	}
	if prefix != '[' {
		// 非 CSI 序列不是当前行编辑支持范围，直接忽略。
		return cursor, buffer, nil
	}
	command, _, err := reader.reader.ReadRune()
	if err != nil {
		// CSI 命令读取失败时把错误返回给上层。
		return cursor, buffer, err
	}
	switch command {
	case 'D':
		if cursor > 0 {
			// 左方向键只移动光标，不改变缓冲区。
			cursor--
			_, _ = fmt.Fprint(reader.stdout, "\x1b[D")
		}
	case 'C':
		if cursor < len(buffer) {
			// 右方向键只移动光标，不改变缓冲区。
			cursor++
			_, _ = fmt.Fprint(reader.stdout, "\x1b[C")
		}
	case 'H':
		// Home 移动到行首，便于快速编辑当前 chunk。
		cursor = 0
		if err := redrawREPLLine(reader.stdout, prompt, buffer, cursor); err != nil {
			// 重绘失败时返回错误。
			return cursor, buffer, err
		}
	case 'F':
		// End 移动到行尾，便于继续追加输入。
		cursor = len(buffer)
		if err := redrawREPLLine(reader.stdout, prompt, buffer, cursor); err != nil {
			// 重绘失败时返回错误。
			return cursor, buffer, err
		}
	case '3':
		trailing, _, err := reader.reader.ReadRune()
		if err != nil {
			// Delete 序列未完整读取时返回底层错误。
			return cursor, buffer, err
		}
		if trailing == '~' && cursor < len(buffer) {
			// Delete 删除光标位置字符，并重绘当前行。
			copy(buffer[cursor:], buffer[cursor+1:])
			buffer = buffer[:len(buffer)-1]
			if err := redrawREPLLine(reader.stdout, prompt, buffer, cursor); err != nil {
				// 重绘失败时返回错误。
				return cursor, buffer, err
			}
		}
	default:
		// 未识别 CSI 命令不改变输入缓冲。
	}
	return cursor, buffer, nil
}

// redrawREPLLine 重绘当前 REPL 输入行并恢复光标位置。
//
// raw mode 下终端不负责行编辑，因此中间插入、删除、Home/End 后需要显式刷新整行。
func redrawREPLLine(stdout io.Writer, prompt string, buffer []rune, cursor int) error {
	if _, err := fmt.Fprintf(stdout, "\r%s%s\x1b[K", prompt, string(buffer)); err != nil {
		// 输出失败时上层应终止 REPL。
		return err
	}
	rightDistance := len(buffer) - cursor
	if rightDistance > 0 {
		// 从行尾向左移动到逻辑光标位置。
		_, err := fmt.Fprintf(stdout, "\x1b[%dD", rightDistance)
		if err != nil {
			// 光标移动失败时上层应终止 REPL。
			return err
		}
	}
	return nil
}

// executeREPLChunk 执行单个 REPL chunk。
//
// source 会先尝试作为表达式回显；表达式路径成功时通过全局 print 输出返回值。表达式编译失败后
// 再按普通语句 chunk 执行，保持 Lua 5.3 交互解释器的基本行为。
func executeREPLChunk(state *lua.State, source string) error {
	if state == nil {
		// nil State 无法执行 REPL chunk。
		return lua.ErrNilState
	}
	trimmedSource := strings.TrimSpace(source)
	if strings.HasPrefix(trimmedSource, "=") {
		// 交互模式兼容 Lua 的 `=expr` 快捷写法，等价于 `return expr` 并回显结果。
		return executeREPLExpression(state, strings.TrimSpace(strings.TrimPrefix(trimmedSource, "=")))
	}
	if replLooksLikeCallStatement(source) {
		// 单独函数调用在交互模式下按语句执行，不回显返回值。
		return executeREPLStatementOnly(state, source)
	}
	return executeREPLExpression(state, source)
}

// executeREPLExpression 先按表达式回显执行 REPL 输入，失败时回退为语句 chunk。
//
// source 必须是用户输入的完整 chunk；表达式路径通过 `return` 包装实现，语句路径保持原源码。
func executeREPLExpression(state *lua.State, source string) error {
	expressionSource := "return " + source
	if err := lua.LoadString(state, expressionSource, "=stdin"); err == nil {
		// 表达式编译成功时弹出 closure 并执行，随后用 print 回显结果。
		closureValue, popErr := state.Pop()
		if popErr != nil {
			// LoadString 成功后栈顶必须是 closure。
			return popErr
		}
		results, callErr := lua.Call(state, closureValue)
		if callErr != nil {
			// 表达式执行错误直接返回给 REPL 错误恢复逻辑。
			return callErr
		}
		return printREPLResults(state, results)
	}
	return executeREPLStatementChunk(state, source)
}

// executeREPLStatementChunk 按普通 chunk 执行 REPL 输入并回显返回值。
//
// source 已经无法作为简单表达式编译；这里保留显式 `return` 或 `do return ... end` 的返回值，
// 用当前全局 print 输出，匹配 Lua 5.3 交互解释器对多行 chunk 的展示行为。
func executeREPLStatementChunk(state *lua.State, source string) error {
	if err := lua.LoadString(state, source, "=stdin"); err != nil {
		// 语句 chunk 编译失败时把 parser/codegen 错误交给 REPL 错误恢复逻辑。
		return err
	}
	closureValue, err := state.Pop()
	if err != nil {
		// LoadString 成功后栈顶必须是 closure。
		return err
	}
	results, err := lua.Call(state, closureValue)
	if err != nil {
		// 运行期错误直接返回给 REPL 错误恢复逻辑。
		return err
	}
	return printREPLResults(state, results)
}

// executeREPLStatementOnly 按普通语句执行 REPL 输入并丢弃返回值。
//
// 该路径用于 `assert(...)`、`print(...)` 等函数调用语句；Lua 交互模式不会像表达式那样自动回显
// 它们的返回值。
func executeREPLStatementOnly(state *lua.State, source string) error {
	if err := lua.LoadString(state, source, "=stdin"); err != nil {
		// 编译失败时交给 REPL 错误恢复逻辑。
		return err
	}
	closureValue, err := state.Pop()
	if err != nil {
		// LoadString 成功后栈顶必须是 closure。
		return err
	}
	_, err = lua.Call(state, closureValue)
	return err
}

// replPrompt 读取 REPL 提示符全局变量。
//
// name 为 `_PROMPT` 或 `_PROMPT2`；全局值是字符串时使用该值，否则回退到 Lua 5.3 默认提示符。
func replPrompt(state *lua.State, name string, fallback string) string {
	if state == nil {
		// nil State 无法读取全局提示符，使用默认值。
		return fallback
	}
	value := state.GetGlobal(name)
	if value.Kind == runtime.KindString {
		// Lua 5.3 允许用户通过 _PROMPT/_PROMPT2 自定义提示符。
		return value.String
	}
	return fallback
}

// printREPLResults 使用当前全局 print 回显 REPL 表达式结果。
//
// results 为空时不输出；print 不可调用或调用失败时返回包含 `error calling 'print'` 的 Lua 错误，
// 兼容官方 main.lua 对交互模式错误文本的检查。
func printREPLResults(state *lua.State, results []runtime.Value) error {
	if len(results) == 0 {
		// 无表达式结果时不调用 print。
		return nil
	}
	printValue, err := lua.GetGlobal(state, "print")
	if err != nil {
		// 全局读取失败通常表示 State 生命周期异常。
		return err
	}
	if _, err := lua.Call(state, printValue, results...); err != nil {
		// REPL 回显失败必须指向 print 调用，官方测试依赖该错误文本。
		return lua.RaiseError(runtime.StringValue("error calling 'print'"))
	}
	return nil
}

// isIncompleteREPLSource 判断 REPL 当前输入是否需要继续补全。
//
// 当前启发式只处理常见块关键字平衡；不尝试解析字符串和注释，完整 Lua 提示策略后续可细化。
func isIncompleteREPLSource(source string) bool {
	if replHasOpenLongString(source) {
		// 长字符串未闭合时继续读取，兼容交互模式中跨行输入 `[[...]]`。
		return true
	}
	if replHasOpenDelimiters(source) {
		// 括号或方括号表达式未闭合时继续读取，兼容官方把空格替换成换行的交互测试。
		return true
	}
	trimmedLine := replLastCodeLine(source)
	if strings.HasSuffix(trimmedLine, "=") && !strings.HasPrefix(trimmedLine, "local ") {
		// 普通赋值右侧缺失时继续读下一行，兼容 `a =` 后换行输入表达式。
		return true
	}
	// 先按关键字平衡判断常见多行块。
	balance := replBlockBalance(source)
	if balance > 0 {
		// 开启块多于关闭块时需要继续读取。
		return true
	}
	if replLooksLikeIncompleteSyntax(source, trimmedLine) {
		// parser 明确缺少表达式或名称时，当前输入仍可能是合法 chunk 前缀。
		return true
	}
	return false
}

// replLastCodeLine 返回 REPL 输入最后一行去除短注释后的代码文本。
//
// 当前用于续行启发式，避免 `(expr) -- ===` 这类注释尾部被误判成赋值未完成。
func replLastCodeLine(source string) string {
	lastLine := source
	if newlineIndex := strings.LastIndex(source, "\n"); newlineIndex >= 0 {
		// 续行判断只关心最后一行是否仍缺少右侧表达式。
		lastLine = source[newlineIndex+1:]
	}
	if commentIndex := strings.Index(lastLine, "--"); commentIndex >= 0 {
		// Lua 短注释后的内容不参与续行判断。
		lastLine = lastLine[:commentIndex]
	}
	return strings.TrimSpace(lastLine)
}

// replHasOpenLongString 判断 REPL 输入中是否存在未闭合的简式长字符串。
//
// 当前覆盖官方测试使用的 `[[...]]` 形式；带等号的长括号后续可扩展为完整 lexer 级判断。
func replHasOpenLongString(source string) bool {
	openCount := strings.Count(source, "[[")
	closeCount := strings.Count(source, "]]")
	if openCount > closeCount {
		// 开启分隔符多于关闭分隔符时需要继续读取。
		return true
	}
	return false
}

// replHasOpenDelimiters 判断表达式括号是否仍未闭合。
//
// 当前用于 REPL 续行启发式，只扫描短注释前的源码文本，覆盖官方 main.lua 中 `return(`、
// `f(` 和跨行参数列表。字符串内括号的完整处理后续可下沉到 lexer。
func replHasOpenDelimiters(source string) bool {
	parenBalance := 0
	bracketBalance := 0
	for _, line := range strings.Split(source, "\n") {
		codeLine := line
		if commentIndex := strings.Index(codeLine, "--"); commentIndex >= 0 {
			// 短注释后的括号不参与续行判断。
			codeLine = codeLine[:commentIndex]
		}
		for _, currentRune := range codeLine {
			switch currentRune {
			case '(':
				// 左圆括号开启表达式或调用参数列表。
				parenBalance++
			case ')':
				// 右圆括号关闭参数列表；多余关闭括号交给 parser 报错。
				parenBalance--
			case '[':
				// 简式长字符串已在 replHasOpenLongString 处理，这里覆盖普通索引表达式。
				bracketBalance++
			case ']':
				// 右方括号关闭索引表达式。
				bracketBalance--
			}
		}
	}
	return parenBalance > 0 || bracketBalance > 0
}

// replLooksLikeIncompleteSyntax 通过 parser 错误文本识别可继续补全的前缀。
//
// source 是当前累计输入；lastCodeLine 是最后一行去掉短注释后的文本。`local =` 这类明确非法输入
// 仍应立即报错，避免把用户错误吞成无限续行。
func replLooksLikeIncompleteSyntax(source string, lastCodeLine string) bool {
	if lastCodeLine == "" {
		// 空行或纯注释不需要续行。
		return false
	}
	if strings.HasPrefix(lastCodeLine, "local =") {
		// 官方测试要求非法 local 语句直接报错。
		return false
	}
	chunkParser := parser.New(source)
	if _, err := chunkParser.ParseChunk(); err == nil {
		// 已经是完整 chunk 时不需要继续读取。
		return false
	} else {
		// 仅把缺少名称或表达式的错误视为可补全前缀。
		errText := err.Error()
		return strings.Contains(errText, "expected expression") || strings.Contains(errText, "expected identifier")
	}
}

// replLooksLikeCallStatement 判断当前输入是否是单条函数调用语句。
//
// 使用 parser 解析完整 chunk，避免把普通表达式误判成语句；只有顶层恰好一个 FunctionCallStatement
// 且没有 return 时才返回 true。
func replLooksLikeCallStatement(source string) bool {
	chunkParser := parser.New(source)
	chunk, err := chunkParser.ParseChunk()
	if err != nil {
		// 无法解析时不能安全判定为函数调用语句。
		return false
	}
	if chunk == nil || chunk.Block == nil || chunk.Block.Return != nil {
		// return chunk 需要回显返回值，不走语句丢弃路径。
		return false
	}
	if len(chunk.Block.Statements) != 1 {
		// 多语句 chunk 保持普通执行路径。
		return false
	}
	_, ok := chunk.Block.Statements[0].(*parser.FunctionCallStatement)
	return ok
}

// replBlockBalance 计算 REPL 输入中的 Lua 块关键字平衡。
//
// 返回值大于 0 表示存在未闭合 block；小于等于 0 表示当前阶段可尝试编译执行。
func replBlockBalance(source string) int {
	// 使用 Fields 做保守 token 化，足以覆盖首版 function/end、if/end 等测试路径。
	fields := strings.Fields(source)
	balance := 0
	for _, field := range fields {
		// 清理常见标点，避免 `then` 或 `do` 后带分隔符时漏判。
		token := strings.Trim(field, " \t\r\n;(){}[]")
		switch token {
		case "function", "then", "do", "repeat":
			// 这些 token 会开启需要后续关闭的块。
			balance++
		case "end", "until":
			// end/until 关闭一个块；允许多余关闭词让 parser 报错。
			balance--
		}
	}
	return balance
}

// setArgTable 写入 Lua CLI 兼容的 arg 表。
//
// scriptPath 为空时不写入；arg[0] 为脚本路径，arg[i] 为第 i 个脚本参数；arg[-1] 起倒序保存
// 脚本名前的原始启动参数，最后再保存解释器路径。
func setArgTable(state *lua.State, scriptPath string, scriptArgs []string, argPrefix []string) error {
	if state == nil {
		// nil State 无法写入全局 arg 表。
		return lua.ErrNilState
	}
	if scriptPath == "" {
		// 没有脚本时不写入 arg，避免纯 -e/-i 场景误导脚本环境。
		return nil
	}

	// 构建 Lua arg 表，正整数参数从 1 开始，arg[0] 是脚本路径。
	argTable := runtime.NewTable()
	for prefixIndex := len(argPrefix) - 1; prefixIndex >= 0; prefixIndex-- {
		// 负索引从最靠近脚本名的启动参数开始，符合 Lua CLI 的 arg 表布局。
		negativeIndex := int64(-(len(argPrefix) - prefixIndex))
		argTable.RawSetInteger(negativeIndex, runtime.StringValue(argPrefix[prefixIndex]))
	}
	if executablePath, err := os.Executable(); err == nil && executablePath != "" {
		// 解释器路径位于所有启动参数之前，索引继续向负方向扩展。
		argTable.RawSetInteger(int64(-(len(argPrefix) + 1)), runtime.StringValue(executablePath))
	}
	argTable.RawSetInteger(0, runtime.StringValue(scriptPath))
	for index, argument := range scriptArgs {
		// Lua 脚本参数从 arg[1] 开始。
		argTable.RawSetInteger(int64(index+1), runtime.StringValue(argument))
	}
	return lua.SetGlobal(state, "arg", runtime.ReferenceValue(runtime.KindTable, argTable))
}

// safeReader 返回可安全读取的输入流。
//
// reader 为 nil 时返回空 reader，避免显式 stdin 测试路径 panic。
func safeReader(reader io.Reader) io.Reader {
	if reader == nil {
		// nil stdin 按空输入处理。
		return strings.NewReader("")
	}
	return reader
}

// safeWriter 返回可安全写入的输出流。
//
// writer 为 nil 时返回 io.Discard，避免 CLI 错误路径因空 writer panic。
func safeWriter(writer io.Writer) io.Writer {
	if writer == nil {
		// nil 输出流按丢弃处理，便于测试只关注退出码。
		return io.Discard
	}
	return writer
}

// exitCodeForError 将 CLI 错误映射为进程退出码。
//
// 当前 Lua 5.3 兼容策略使用 0 表示成功，其他参数、语法、运行时和资源错误统一返回 1。
func exitCodeForError(err error) int {
	if err == nil {
		// nil 错误表示成功。
		return ExitOK
	}
	if errors.Is(err, errCLIInterrupted) {
		// Ctrl-C 按常见 shell 约定映射为 130。
		return ExitInterrupted
	}
	if exitErr, ok := luaExitError(err); ok {
		// os.exit 携带的退出码优先级最高，保持官方 CLI 不打印错误文本的语义。
		return exitErr.Code
	}
	if parser.IsSyntaxError(err) {
		// 语法错误按 Lua CLI 非零失败退出。
		return ExitFailure
	}
	if IsExecutionUnavailable(err) {
		// 当前阶段执行器未接入也属于运行失败。
		return ExitFailure
	}
	return ExitFailure
}

// isCLISyntaxError 判断错误是否应按 CLI 语法错误展示。
//
// err 可来自 parser 直接返回，也可经过加载执行路径包装；命中时 stderr 需要包含官方 "syntax error" 文本。
func isCLISyntaxError(err error) bool {
	if parser.IsSyntaxError(err) {
		// parser 结构化错误是最可靠的语法错误信号。
		return true
	}
	if err != nil && strings.Contains(err.Error(), "parse error") {
		// 部分加载路径当前只保留文本，先按官方 CLI 展示为 syntax error。
		return true
	}
	return false
}

// luaExitError 从错误链中提取 Lua `os.exit` 请求。
//
// err 可为 nil、Lua runtime 包装错误或普通 Go error；命中时返回 ExitError 与 true。
func luaExitError(err error) (*oslib.ExitError, bool) {
	// 通过 errors.As 保留 Lua error 与 Go 包装链路的兼容性。
	var exitErr *oslib.ExitError
	if errors.As(err, &exitErr) {
		// os.exit 命中时由 CLI 主流程决定是否打印与退出码。
		return exitErr, true
	}
	return nil, false
}

// IsExecutionUnavailable 判断错误是否为当前阶段 Lua closure 执行器未接入。
//
// 该 helper 供 CLI 测试和后续退出码分流复用，普通语法错误不会命中。
func IsExecutionUnavailable(err error) bool {
	// 使用 errors.Is 保留 RuntimeError 包装链路判断能力。
	return errors.Is(err, lua.ErrExecutionUnavailable)
}
