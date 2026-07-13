// Package playground 提供浏览器文档中心使用的 GLua 沙箱执行会话。
//
// 本包负责隔离脚本权限、转接标准输入输出和连接指令级调试观察器；平台相关的 JavaScript
// 消息协议由 cmd/glua-wasm 维护，避免浏览器细节泄露到稳定 lua API。
package playground

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/ZingYao/go-lua-vm/lua"
	"github.com/ZingYao/go-lua-vm/runtime"
	baselib "github.com/ZingYao/go-lua-vm/stdlib/base"
	iolib "github.com/ZingYao/go-lua-vm/stdlib/io"
)

const playgroundChunkName = "@playground.lua"

// Event 表示 Playground 向浏览器发送的一次执行或调试事件。
type Event struct {
	// Type 表示 ready、output、paused、finished 或 error。
	Type string `json:"type"`
	// Stream 表示 output 事件来自 stdout 还是 stderr。
	Stream string `json:"stream,omitempty"`
	// Text 保存输出文本或状态说明。
	Text string `json:"text,omitempty"`
	// Source 保存当前暂停源码名称。
	Source string `json:"source,omitempty"`
	// Line 保存当前暂停的一基源码行号。
	Line int `json:"line,omitempty"`
	// Reason 保存 entry、breakpoint、pause 或 step 暂停原因。
	Reason string `json:"reason,omitempty"`
	// Depth 保存暂停点的 Lua/Go 调用帧深度。
	Depth int `json:"depth,omitempty"`
	// Locals 保存当前 Lua 帧可见的局部变量。
	Locals []Variable `json:"locals,omitempty"`
}

// Variable 表示调试面板中的一个 Lua 值。
type Variable struct {
	// Name 保存局部变量名或 table 键名。
	Name string `json:"name"`
	// Type 保存 Lua 基础类型名称。
	Type string `json:"type"`
	// Value 保存稳定调试文本。
	Value string `json:"value"`
	// Const 表示局部绑定是否只读。
	Const bool `json:"const,omitempty"`
	// Children 保存 table 的有限深度子项。
	Children []Variable `json:"children,omitempty"`
}

// Sink 接收会话产生的流式事件。
type Sink func(Event)

// Session 保存一次浏览器脚本执行所需的输入、输出和调试状态。
type Session struct {
	sink     Sink
	input    *InputReader
	debugger *Debugger
}

// NewSession 创建尚未执行脚本的 Playground 会话。
//
// sink 允许为 nil，此时事件会被丢弃；每个 Session 拥有独立输入队列和调试控制器。
func NewSession(sink Sink) *Session {
	// 初始化独立组件，避免不同浏览器 Worker 之间共享控制 channel。
	session := &Session{sink: sink, input: NewInputReader()}
	session.debugger = NewDebugger(session.emit)
	return session
}

// Run 编译并执行一段 Playground 源码。
//
// ctx 必须非 nil；debug 为 true 时首条可见源码行会暂停。宿主文件、环境变量、进程与动态模块
// 权限保持关闭。返回值保留 Lua 编译或运行错误，错误同时以 stderr 事件发送。
func (session *Session) Run(ctx context.Context, source string, debug bool) error {
	// 单文件入口委托统一执行路径，不注册虚拟工作区模块。
	return session.run(ctx, source, playgroundChunkName, nil, debug)
}

// RunWorkspace 编译并执行带虚拟文件系统模块的 Playground 源码。
//
// entryPath 必须是工作区相对 Lua/GLua 路径；files 的 key 也必须是规范相对路径。静态模块会注册到 package.preload，
// 不访问宿主文件系统；任一模块编译失败会在主入口运行前返回错误，以避免缓存损坏的 loader。
func (session *Session) RunWorkspace(ctx context.Context, source string, entryPath string, files map[string]string, debug bool) error {
	// 入口 chunk 使用真实工作区路径，使调试暂停位置和错误信息可以映射回 Monaco Model。
	entryPath = normalizeWorkspacePath(entryPath)
	if entryPath == "" {
		// 非法或空入口回退稳定的 Playground 名称，仍允许临时文档执行。
		entryPath = strings.TrimPrefix(playgroundChunkName, "@")
	}
	return session.run(ctx, source, "@"+entryPath, files, debug)
}

// run 创建沙箱 State、注册工作区模块并执行入口 chunk。
//
// ctx 必须非 nil；chunkName 必须是 Lua 调试可见名称；files 为 nil 表示单文件模式。
func (session *Session) run(ctx context.Context, source string, chunkName string, files map[string]string, debug bool) error {
	// 先校验上下文，避免创建无法停止的运行状态。
	if ctx == nil {
		// nil 上下文无法表达 Worker 生命周期，返回稳定参数错误。
		return lua.ErrNilContext
	}
	workspaceFiles := workspaceSourceSnapshot(files, strings.TrimPrefix(chunkName, "@"), source)
	options := lua.DefaultOptions()
	options.VirtualFilesystem = newWorkspaceFilesystem(workspaceFiles)
	if debug {
		// Debug 模式接入逐指令观察器并直接运行，只在断点、主动暂停或单步时停止。
		options.DebugObserver = session.debugger
		session.debugger.Reset(false)
	} else {
		// 普通运行仍清理旧断点暂停状态，确保 Session 可在测试中复用。
		session.debugger.Reset(false)
	}
	state, err := lua.NewStateWithContext(ctx, options)
	if err != nil {
		// State 创建失败时尚无脚本可执行，直接报告宿主错误。
		session.emitError(err)
		return err
	}
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库注册失败意味着 print/io/require 均不可用，停止本次会话。
		session.emitError(err)
		return err
	}
	if err := session.connectStreams(state); err != nil {
		// 流连接失败时不能保证网页看到正确输出，停止执行。
		session.emitError(err)
		return err
	}
	session.emit(Event{Type: "running"})
	if err := lua.LoadString(state, source, chunkName); err != nil {
		// 编译错误属于 stderr，并保留原始 Lua 诊断文本。
		session.emitError(err)
		return err
	}
	closure, err := state.Pop()
	if err != nil {
		// LoadString 成功后必须有 closure，栈异常按内部错误返回。
		session.emitError(err)
		return err
	}
	if _, err := lua.Call(state, closure); err != nil {
		// 运行期错误提取 Lua error object 与 traceback，避免只显示笼统的 lua error。
		err = runtime.WithTracebackFrames(err, runtimeStateTraceback(state))
		session.emitError(err)
		return err
	}
	session.emit(Event{Type: "finished"})
	return nil
}

// runtimeStateTraceback 返回 Lua State 当前失败现场的调用帧。
//
// state 必须非 nil；返回顺序从当前帧到最早帧，供 runtime.Traceback 直接格式化。
func runtimeStateTraceback(state *lua.State) []runtime.CallFrame {
	// lua.State 是 runtime.State 的稳定别名，转换后读取错误返回前尚未弹出的帧。
	if state == nil {
		return nil
	}
	return (*runtime.State)(state).TracebackFrames()
}

// workspaceSourceSnapshot 创建当前执行使用的独立工作区文件快照。
//
// files 可以为 nil；entryPath 必须是工作区相对路径。合法入口会使用编辑器当前 source 覆盖持久化版本。
func workspaceSourceSnapshot(files map[string]string, entryPath string, source string) map[string]string {
	// 复制输入映射，避免执行准备阶段修改页面语言服务共享的快照。
	snapshot := make(map[string]string, len(files)+1)
	for filePath, fileSource := range files {
		// 原始路径由 VFS 构造器统一规范化和过滤。
		snapshot[filePath] = fileSource
	}
	entryPath = normalizeWorkspacePath(entryPath)
	if entryPath != "" {
		// 当前 Monaco Model 可能尚未保存，执行必须优先使用编辑器内容。
		snapshot[entryPath] = source
	}
	return snapshot
}

// emitError 把 Go/Lua 错误转换为 Playground 可读 stderr 事件。
//
// err 必须非 nil；Lua RuntimeError 会展示原始 error object 和失败现场 traceback，普通 Go error 保留原文本。
func (session *Session) emitError(err error) {
	// nil 错误不产生误导性的 stderr 事件。
	if err == nil {
		return
	}
	session.emit(Event{Type: "error", Stream: "stderr", Text: playgroundErrorText(err) + "\n"})
}

// playgroundErrorText 返回适合浏览器输出的具体错误文本。
//
// err 必须非 nil；Lua string error object 去掉调试引号，并在 RuntimeError 保存调用帧时附加 Lua 风格 traceback。
func playgroundErrorText(err error) string {
	// 优先读取 Lua error object，因为 RuntimeError.Error 可能只返回 lua error 分类名称。
	errorObject := runtime.ErrorObject(err)
	message := errorObject.DebugString()
	if errorObject.Kind == runtime.KindString {
		// 字符串错误直接展示内容，避免 stdout 出现额外引号。
		message = errorObject.String
	}
	if message == "" {
		// nil 或空错误对象回退 Go 错误文本，确保页面始终有可诊断信息。
		message = err.Error()
	}
	var runtimeError *runtime.RuntimeError
	if errors.As(err, &runtimeError) && runtimeError != nil && len(runtimeError.TracebackFrames) > 0 {
		// 使用错误发生现场而不是 Call 返回后的空栈生成 traceback。
		return runtime.Traceback(message, runtimeError.TracebackFrames)
	}
	return message
}

// normalizeWorkspacePath 规范化浏览器工作区相对路径。
//
// 输入可以带 `@/` 前缀；绝对路径、父目录穿越和当前目录返回空字符串。
func normalizeWorkspacePath(filePath string) string {
	// 统一移除调试 chunk 前缀并使用 slash 语义清理。
	normalized := path.Clean(strings.TrimPrefix(strings.TrimPrefix(filePath, "@/"), "/"))
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || path.IsAbs(normalized) {
		// 非法路径不能突破浏览器授权的虚拟工作区边界。
		return ""
	}
	return normalized
}

// SendInput 将用户输入追加到当前脚本 stdin。
//
// text 按一次终端提交处理；缺少末尾换行时自动补充换行，满足 io.read 默认按行读取语义。
func (session *Session) SendInput(text string) {
	// 输入格式由 InputReader 统一规范化，浏览器无需自行拼接换行。
	session.input.Send(text)
}

// SetBreakpoints 替换当前 Playground 源码的断点集合。
//
// lines 使用一基行号；非正行会被忽略，重复行会自动去重。
func (session *Session) SetBreakpoints(lines []int) {
	// 断点替换语义委托给 Debugger，避免 UI 删除断点后遗留旧状态。
	session.debugger.SetBreakpoints(lines)
}

// DebugCommand 向暂停或运行中的调试器发送控制命令。
//
// 支持 pause、continue、stepInto、stepOver 和 stepOut；未知命令返回错误且不改变状态。
func (session *Session) DebugCommand(command string) error {
	// Debugger 负责并发唤醒与步进状态更新。
	return session.debugger.Command(command)
}

// CloseInput 关闭 stdin，使等待输入的 io.read 收到 EOF。
func (session *Session) CloseInput() {
	// 关闭操作保持幂等，便于 Worker 销毁路径重复调用。
	session.input.Close()
}

// connectStreams 把 Lua 标准输入输出连接到浏览器事件和输入队列。
func (session *Session) connectStreams(state *lua.State) error {
	// 为 stdout/stderr 分别创建 writer，保证 UI 可以独立着色和筛选。
	stdoutWriter := eventWriter{stream: "stdout", emit: session.emit}
	stderrWriter := eventWriter{stream: "stderr", emit: session.emit}
	inputFile := iolib.NewStandardFile("stdin", session.input, nil)
	outputFile := iolib.NewStandardFile("stdout", nil, stdoutWriter)
	errorFile := iolib.NewStandardFile("stderr", nil, stderrWriter)
	runtimeState := (*runtime.State)(state)
	inputValue := iolib.NewFileValue(runtimeState, inputFile)
	outputValue := iolib.NewFileValue(runtimeState, outputFile)
	errorValue := iolib.NewFileValue(runtimeState, errorFile)
	if _, err := iolib.Input(inputValue); err != nil {
		// stdin 切换失败会让交互脚本错误读取宿主输入，必须停止。
		return fmt.Errorf("connect playground stdin: %w", err)
	}
	if _, err := iolib.Output(outputValue); err != nil {
		// stdout 切换失败会丢失 io.write 输出，必须停止。
		return fmt.Errorf("connect playground stdout: %w", err)
	}
	ioValue, err := lua.GetGlobal(state, "io")
	if err != nil {
		// OpenLibs 后 io 必须存在，读取失败按初始化错误返回。
		return fmt.Errorf("read playground io table: %w", err)
	}
	ioTable, ok := ioValue.Ref.(*runtime.Table)
	if !ok || ioTable == nil {
		// 非 table 的 io 全局无法承载标准流句柄。
		return errors.New("playground io library is unavailable")
	}
	ioTable.RawSetString("stdin", inputValue)
	ioTable.RawSetString("stdout", outputValue)
	ioTable.RawSetString("stderr", errorValue)
	if err := lua.Register(state, "print", func(args ...lua.Value) ([]lua.Value, error) {
		// print 使用专用 stdout writer，保持与 io.stderr 分流。
		return baselib.PrintTo(stdoutWriter, args...)
	}); err != nil {
		// print 覆盖失败会让输出落到宿主控制台，必须停止。
		return fmt.Errorf("connect playground print: %w", err)
	}
	return nil
}

// emit 安全发送一次会话事件。
func (session *Session) emit(event Event) {
	// nil sink 表示调用方只关心 Run 返回值。
	if session == nil || session.sink == nil {
		return
	}
	session.sink(event)
}

// eventWriter 将 Lua 字节输出转换为流式 Playground 事件。
type eventWriter struct {
	stream string
	emit   Sink
}

// Write 实现 io.Writer，并保持输入字节内容不变。
func (writer eventWriter) Write(content []byte) (int, error) {
	// 空写入不产生冗余 UI 事件，但仍按 io.Writer 语义成功。
	if len(content) == 0 {
		return 0, nil
	}
	if writer.emit != nil {
		// 复制为 string 后事件不再借用调用方缓冲区。
		writer.emit(Event{Type: "output", Stream: writer.stream, Text: string(content)})
	}
	return len(content), nil
}

// InputReader 是支持浏览器异步提交文本的阻塞输入流。
type InputReader struct {
	mu      sync.Mutex
	pending bytes.Buffer
	chunks  chan string
	closed  chan struct{}
	once    sync.Once
}

// NewInputReader 创建空的可交互输入流。
func NewInputReader() *InputReader {
	// 缓冲 channel 允许用户在脚本开始读取前预先提交少量输入。
	return &InputReader{chunks: make(chan string, 64), closed: make(chan struct{})}
}

// Read 实现 io.Reader，并在没有输入时等待浏览器提交或关闭。
func (reader *InputReader) Read(target []byte) (int, error) {
	// 零长度读取按 io.Reader 约定立即成功。
	if len(target) == 0 {
		return 0, nil
	}
	for {
		reader.mu.Lock()
		if reader.pending.Len() > 0 {
			// 已缓存输入时优先复制，保留未消费尾部供下一次读取。
			count, err := reader.pending.Read(target)
			reader.mu.Unlock()
			return count, err
		}
		reader.mu.Unlock()
		select {
		case chunk := <-reader.chunks:
			// 新输入先写入内部缓冲，再由下一轮统一读取。
			reader.mu.Lock()
			_, _ = reader.pending.WriteString(chunk)
			reader.mu.Unlock()
		case <-reader.closed:
			// 关闭且没有缓存内容时返回 EOF，解除 io.read 等待。
			return 0, io.EOF
		}
	}
}

// Send 向输入流提交一条终端文本。
func (reader *InputReader) Send(text string) {
	// 行输入缺少换行时自动补齐，避免默认 io.read 一直等待行结束。
	if len(text) == 0 || text[len(text)-1] != '\n' {
		text += "\n"
	}
	select {
	case <-reader.closed:
		// 已关闭输入不再接受迟到消息。
		return
	case reader.chunks <- text:
		// 成功排队后由阻塞 Read 消费。
		return
	}
}

// Close 关闭输入流并唤醒等待者。
func (reader *InputReader) Close() {
	// sync.Once 保证多条 Worker 清理路径不会重复关闭 channel。
	reader.once.Do(func() {
		close(reader.closed)
	})
}
