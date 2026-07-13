//go:build js && wasm

// Package main 提供 GLua 文档 Playground 的 WebAssembly 入口。
//
// 该入口运行在 Web Worker 中，通过 JavaScript 消息桥接执行、标准输入和调试控制；浏览器 UI
// 由 docs/assets/playground.js 维护，核心会话逻辑由 internal/playground 提供。
package main

import (
	"context"
	"encoding/json"
	"sync"
	"syscall/js"

	"github.com/ZingYao/go-lua-vm/compiler/formatter"
	"github.com/ZingYao/go-lua-vm/extensions"
	"github.com/ZingYao/go-lua-vm/internal/lsp"
	"github.com/ZingYao/go-lua-vm/internal/playground"
)

var (
	// sessionMu 保护 JavaScript 回调和执行 goroutine共享的当前会话指针。
	sessionMu sync.Mutex
	// currentSession 保存当前 Worker 内唯一的执行会话。
	currentSession *playground.Session
	// dispatchFunction 保持 JavaScript 回调存活到 Worker 被页面销毁。
	dispatchFunction js.Func
)

// main 注册浏览器消息入口并保持 Go WebAssembly 运行时存活。
func main() {
	// 全局函数由 Worker 包装成 postMessage 协议，避免 Go 代码依赖 DOM。
	dispatchFunction = js.FuncOf(dispatch)
	js.Global().Set("gluaPlaygroundDispatch", dispatchFunction)
	postRaw(map[string]any{"type": "ready"})
	select {}
}

// dispatch 处理 Worker 转发的单条浏览器请求。
//
// 第一个参数必须是普通 JavaScript object；支持 run、format、language、input、closeInput、breakpoints 和 debugCommand。
// 返回 undefined，执行结果通过异步 postMessage 事件发送。
func dispatch(_ js.Value, args []js.Value) any {
	// 缺少请求对象时忽略，防止页面初始化竞态导致 panic。
	if len(args) == 0 || args[0].Type() != js.TypeObject {
		return nil
	}
	request := args[0]
	action := request.Get("action").String()
	switch action {
	case "run":
		// run 在独立 goroutine 执行，使 JavaScript 回调仍能提交输入和调试命令。
		workspace, err := jsStringMapProperty(request, "workspace")
		if err != nil {
			// 工作区快照非法时拒绝执行，避免 require 看到不完整文件集合。
			postEvent(playground.Event{Type: "error", Stream: "stderr", Text: "invalid run workspace: " + err.Error() + "\n"})
			break
		}
		startRun(request.Get("source").String(), jsStringProperty(request, "path"), workspace, request.Get("debug").Bool(), jsIntSlice(request.Get("breakpoints")))
	case "format":
		// format 复用项目 formatter，并把成功结果或语法错误返回编辑器。
		formatSource(request.Get("source").String())
	case "language":
		// language 复用桌面 LSP 内核并按 requestId 返回异步编辑器结果。
		handleLanguageRequest(request)
	case "input":
		// 用户输入直接写入当前会话的阻塞 stdin 队列。
		withSession(func(session *playground.Session) {
			session.SendInput(request.Get("text").String())
		})
	case "closeInput":
		// 关闭输入会让等待中的 io.read 收到 EOF。
		withSession(func(session *playground.Session) {
			session.CloseInput()
		})
	case "breakpoints":
		// 断点采用整表替换语义，与编辑器 gutter 当前状态保持一致。
		withSession(func(session *playground.Session) {
			session.SetBreakpoints(jsIntSlice(request.Get("lines")))
		})
	case "debugCommand":
		// 调试命令错误作为 stderr 状态返回，不终止 Worker。
		withSession(func(session *playground.Session) {
			if err := session.DebugCommand(request.Get("command").String()); err != nil {
				postEvent(playground.Event{Type: "error", Stream: "stderr", Text: err.Error() + "\n"})
			}
		})
	default:
		// 未知 action 返回可见错误，帮助定位前后端协议版本不一致。
		postEvent(playground.Event{Type: "error", Stream: "stderr", Text: "unsupported playground action: " + action + "\n"})
	}
	return nil
}

// handleLanguageRequest 处理浏览器 Monaco 发起的一次语言服务请求。
//
// request 必须包含 operation、source 和 requestId；completion、hover、definition 还使用零基 line/character。
// 未知 operation 返回 languageResult/error，不影响执行会话。
func handleLanguageRequest(request js.Value) {
	// 读取公共请求字段后按操作分派到 internal/lsp 的浏览器 facade。
	operation := request.Get("operation").String()
	source := request.Get("source").String()
	requestID := jsIntProperty(request, "requestId")
	line := jsIntProperty(request, "line")
	character := jsIntProperty(request, "character")
	documentPath := jsStringProperty(request, "path")
	workspace, workspaceError := jsStringMapProperty(request, "workspace")
	if workspaceError != nil {
		// 无法解析的工作区快照会导致错误跨文件结果，因此拒绝本次请求。
		postRaw(map[string]any{"type": "languageResult", "requestId": requestID, "operation": operation, "error": "invalid language workspace: " + workspaceError.Error()})
		return
	}
	var result any
	switch operation {
	case "diagnostics":
		// diagnostics 返回 parser 与 codegen 的完整错误列表。
		result = lsp.BrowserDiagnostics(source)
	case "semanticTokens":
		// semanticTokens 返回 LSP 五元组 delta 编码。
		result = lsp.BrowserSemanticTokens(source)
	case "completion":
		// completion 返回内置目录、当前文件符号与静态 require 模块成员候选。
		result = lsp.BrowserWorkspaceCompletions(source, documentPath, workspace, line, character)
	case "hover":
		// hover 返回 Markdown 文档或 nil。
		result = lsp.BrowserHoverAt(source, line, character)
	case "definition":
		// definition 返回当前文件或虚拟工作区模块定义范围。
		result = lsp.BrowserWorkspaceDefinitionAt(source, documentPath, workspace, line, character)
	default:
		// 未知操作显式返回错误，帮助发现前后端协议漂移。
		postRaw(map[string]any{"type": "languageResult", "requestId": requestID, "operation": operation, "error": "unsupported language operation: " + operation})
		return
	}
	postRaw(map[string]any{"type": "languageResult", "requestId": requestID, "operation": operation, "result": result})
}

// jsStringProperty 读取 JavaScript object 的字符串属性。
//
// object 必须是可读 JavaScript object；属性缺失或不是 string 时返回空字符串。
func jsStringProperty(object js.Value, name string) string {
	// 先检查属性类型，避免把 undefined 转成具有误导性的文本。
	value := object.Get(name)
	if value.Type() != js.TypeString {
		// 缺失路径或快照采用空值语义。
		return ""
	}
	return value.String()
}

// jsStringMapProperty 读取 JSON 编码的字符串映射属性。
//
// object 的属性缺失或空字符串时返回空映射；JSON 非法或 value 不是字符串时返回错误。
func jsStringMapProperty(object js.Value, name string) (map[string]string, error) {
	// 浏览器使用 JSON 传递虚拟工作区，避免 syscall/js 逐键跨边界读取。
	payload := jsStringProperty(object, name)
	if payload == "" {
		// 没有工作区时仍返回可写空映射，统一下游调用。
		return map[string]string{}, nil
	}
	result := make(map[string]string)
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		// 非法快照必须显式返回错误，不能静默退化为错误的跨文件分析。
		return nil, err
	}
	return result, nil
}

// jsIntProperty 读取 JavaScript object 的整数属性。
//
// object 必须是可读 JavaScript object；属性缺失或不是 number 时返回 0，适合语言请求的默认位置与编号。
func jsIntProperty(object js.Value, name string) int {
	// 先检查属性类型，避免对 undefined/null 调用 Int 导致 syscall/js panic。
	value := object.Get(name)
	if value.Type() != js.TypeNumber {
		// 缺失或非数字属性统一回退为零，不影响其他请求字段。
		return 0
	}
	return value.Int()
}

// formatSource 使用 GLua 默认语法扩展格式化 Playground 源码。
//
// source 必须是完整 GLua chunk；成功时返回 formatResult/source，语法错误时返回
// formatResult/error，调用方据此保留原始代码并把错误写入 stderr。
func formatSource(source string) {
	// 直接复用 CLI 与 LSP 的 formatter，保证浏览器格式化规则与桌面编辑器一致。
	formatted, err := formatter.Format(source, extensions.Default())
	if err != nil {
		// 格式化失败不修改源码，只把可读错误交给页面展示。
		postRaw(map[string]any{"type": "formatResult", "error": err.Error()})
		return
	}
	postRaw(map[string]any{"type": "formatResult", "source": formatted})
}

// startRun 创建当前 Worker 唯一会话并异步执行源码。
func startRun(source string, entryPath string, workspace map[string]string, debug bool, breakpoints []int) {
	// 每个新 Worker 只运行一次；防御重复 run 时关闭旧输入并替换指针。
	session := playground.NewSession(postEvent)
	session.SetBreakpoints(breakpoints)
	sessionMu.Lock()
	if currentSession != nil {
		// 重复 run 时先关闭旧输入，避免旧 goroutine永久等待。
		currentSession.CloseInput()
	}
	currentSession = session
	sessionMu.Unlock()
	go func() {
		// Worker 生命周期由页面控制，因此使用 Background 作为会话上下文。
		_ = session.RunWorkspace(context.Background(), source, entryPath, workspace, debug)
	}()
}

// withSession 在当前会话存在时执行一次控制操作。
func withSession(action func(*playground.Session)) {
	// 只在锁内复制指针，避免阻塞输入或命令发送长期占用互斥锁。
	sessionMu.Lock()
	session := currentSession
	sessionMu.Unlock()
	if session == nil || action == nil {
		// Worker 尚未收到 run 时忽略迟到或提前控制消息。
		return
	}
	action(session)
}

// jsIntSlice 将 JavaScript number 数组转换为 Go int 切片。
func jsIntSlice(value js.Value) []int {
	// undefined、null 或非 object 输入表示空集合。
	if value.Type() != js.TypeObject {
		return nil
	}
	lengthValue := value.Get("length")
	if lengthValue.Type() != js.TypeNumber {
		// 普通 object 没有数组 length 时按空集合处理。
		return nil
	}
	length := lengthValue.Int()
	lines := make([]int, 0, length)
	for index := 0; index < length; index++ {
		// JavaScript number 按一基源码行整数读取。
		lines = append(lines, value.Index(index).Int())
	}
	return lines
}

// postEvent 将强类型 Playground 事件发送给 Worker 宿主页。
func postEvent(event playground.Event) {
	// 统一走 JSON 序列化，确保嵌套局部变量树可安全传给 JavaScript。
	postJSON(event)
}

// postRaw 发送不属于 Playground Event 结构的初始化消息。
func postRaw(value map[string]any) {
	// 初始化 ready 消息和普通事件共享同一 JSON 转换路径。
	postJSON(value)
}

// postJSON 把 Go 值编码为 JavaScript object 并调用 Worker postMessage。
func postJSON(value any) {
	// 内部事件结构均应可编码；失败时无法可靠通知页面，只能忽略该条事件。
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	parsed := js.Global().Get("JSON").Call("parse", string(payload))
	js.Global().Call("postMessage", parsed)
}
