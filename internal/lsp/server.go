// Package lsp 实现 glua 语言服务器。
package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"

	"github.com/ZingYao/go-lua-vm/compiler/codegen"
	"github.com/ZingYao/go-lua-vm/compiler/formatter"
	"github.com/ZingYao/go-lua-vm/compiler/lexer"
	"github.com/ZingYao/go-lua-vm/compiler/parser"
	"github.com/ZingYao/go-lua-vm/extensions"
)

const (
	// textDocumentSyncFull 表示 LSP 全量文档同步模式。
	textDocumentSyncFull = 1
	// diagnosticSeverityError 表示 LSP 错误级诊断。
	diagnosticSeverityError = 1
)

var semanticTokenTypes = []string{"namespace", "type", "class", "enum", "interface", "struct", "typeParameter", "parameter", "variable", "property", "enumMember", "event", "function", "method", "macro", "keyword", "modifier", "comment", "string", "number", "regexp", "operator"}

const (
	semanticTypeVariable = 8
	semanticTypeFunction = 12
	semanticTypeKeyword  = 15
	semanticTypeComment  = 17
	semanticTypeString   = 18
	semanticTypeNumber   = 19
	semanticTypeOperator = 21
)

// Server 表示 glua language server 实例。
//
// Server 通过 stdin/stdout 风格 JSON-RPC 2.0 与编辑器通信；当前支持初始化、文档同步、
// 诊断发布和全文格式化。
type Server struct {
	// input 保存 LSP 输入流。
	input io.Reader
	// output 保存 LSP 输出流。
	output io.Writer
	// syntax 保存 parser/formatter 使用的语法扩展集合。
	syntax extensions.SyntaxSet
	// documents 保存已打开文档的最新全文。
	documents map[string]string
	// workspaceRoots 保存 initialize 提供的工作区目录，用于 require 模块解析。
	workspaceRoots []string
	// writeMu 串行化 JSON-RPC 输出，避免响应和诊断通知交错。
	writeMu sync.Mutex
	// shutdown 表示客户端已请求 shutdown。
	shutdown bool
}

// New 创建 glua language server。
//
// input/output 通常连接进程 stdin/stdout；syntax 为 0 且 syntaxSet 为 false 时使用默认扩展集合。
func New(input io.Reader, output io.Writer, syntax extensions.SyntaxSet) *Server {
	// 初始化文档表和默认语法集合，后续 initialize 可通过 initializationOptions 覆盖。
	return &Server{
		input:     input,
		output:    output,
		syntax:    syntax & extensions.Compiled(),
		documents: make(map[string]string),
	}
}

// Run 运行 LSP 主循环直到输入结束、收到 exit，或 context 取消。
//
// ctx 必须非 nil；输入流必须使用 LSP Content-Length 帧。返回错误表示协议读取、JSON 解码或
// 写入失败；客户端主动关闭输入时返回 nil。
func (server *Server) Run(ctx context.Context) error {
	// 使用 bufio.Reader 读取 header 和 body，保持协议帧边界清晰。
	if ctx == nil {
		return errors.New("nil context")
	}
	reader := bufio.NewReader(server.input)
	for {
		select {
		case <-ctx.Done():
			// 调用方取消时终止 server 主循环。
			return ctx.Err()
		default:
			// 未取消时继续读取下一帧。
		}
		body, err := readMessage(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// 客户端关闭 stdin 是正常退出路径。
				return nil
			}
			return err
		}
		if err := server.handleMessage(body); err != nil {
			if errors.Is(err, io.EOF) {
				// exit 通知表示客户端主动结束 LSP 会话。
				return nil
			}
			return err
		}
	}
}

// requestMessage 表示 JSON-RPC 请求或通知。
type requestMessage struct {
	// JSONRPC 保存协议版本。
	JSONRPC string `json:"jsonrpc"`
	// ID 保存请求 ID；通知没有 ID。
	ID json.RawMessage `json:"id,omitempty"`
	// Method 保存方法名。
	Method string `json:"method"`
	// Params 保存方法参数原始 JSON。
	Params json.RawMessage `json:"params,omitempty"`
}

// responseMessage 表示 JSON-RPC 响应。
type responseMessage struct {
	// JSONRPC 保存协议版本。
	JSONRPC string `json:"jsonrpc"`
	// ID 保存请求 ID。
	ID json.RawMessage `json:"id"`
	// Result 保存成功响应。
	Result any `json:"result"`
}

// errorResponseMessage 表示 JSON-RPC 失败响应。
type errorResponseMessage struct {
	// JSONRPC 保存协议版本。
	JSONRPC string `json:"jsonrpc"`
	// ID 保存请求 ID。
	ID json.RawMessage `json:"id"`
	// Error 保存失败响应。
	Error *responseError `json:"error"`
}

// responseError 表示 JSON-RPC 错误响应。
type responseError struct {
	// Code 保存错误码。
	Code int `json:"code"`
	// Message 保存错误文本。
	Message string `json:"message"`
}

// notificationMessage 表示 JSON-RPC 通知。
type notificationMessage struct {
	// JSONRPC 保存协议版本。
	JSONRPC string `json:"jsonrpc"`
	// Method 保存方法名。
	Method string `json:"method"`
	// Params 保存通知参数。
	Params any `json:"params,omitempty"`
}

// handleMessage 处理单个 JSON-RPC 消息。
func (server *Server) handleMessage(body []byte) error {
	// 先解出通用请求结构，再按 method 分发。
	var message requestMessage
	if err := json.Unmarshal(body, &message); err != nil {
		return err
	}
	switch message.Method {
	case "initialize":
		return server.handleInitialize(message)
	case "initialized":
		return nil
	case "shutdown":
		server.shutdown = true
		return server.writeResponse(message.ID, nil)
	case "exit":
		return io.EOF
	case "textDocument/didOpen":
		return server.handleDidOpen(message.Params)
	case "textDocument/didChange":
		return server.handleDidChange(message.Params)
	case "textDocument/didClose":
		return server.handleDidClose(message.Params)
	case "workspace/didChangeConfiguration":
		return server.handleDidChangeConfiguration(message.Params)
	case "textDocument/formatting":
		return server.handleFormatting(message)
	case "textDocument/definition":
		return server.handleDefinition(message)
	case "textDocument/hover":
		return server.handleHover(message)
	case "textDocument/completion":
		return server.handleCompletion(message)
	case "textDocument/semanticTokens/full":
		return server.handleSemanticTokensFull(message)
	default:
		if len(message.ID) == 0 {
			// 未知通知直接忽略，符合 LSP 容错习惯。
			return nil
		}
		return server.writeError(message.ID, -32601, "method not found: "+message.Method)
	}
}

// initializeParams 表示 initialize 请求参数。
type initializeParams struct {
	// InitializationOptions 保存 glua 自定义初始化参数。
	InitializationOptions json.RawMessage `json:"initializationOptions"`
	// RootURI 保存单工作区根目录 URI。
	RootURI string `json:"rootUri"`
	// WorkspaceFolders 保存多工作区根目录 URI。
	WorkspaceFolders []workspaceFolder `json:"workspaceFolders"`
}

// workspaceFolder 表示 LSP initialize 传入的工作区目录。
type workspaceFolder struct {
	// URI 保存工作区目录 URI。
	URI string `json:"uri"`
}

// gluaInitializationOptions 表示 glua LSP 支持的初始化选项。
type gluaInitializationOptions struct {
	// Syntax 保存语法模式，例如 lua53、extended、continue,switch。
	Syntax string `json:"syntax"`
	// Locale 保存用户配置的文档语言，auto 表示跟随编辑器界面语言。
	Locale string `json:"locale"`
	// ResolvedLocale 保存编辑器已经解析完成的实际文档语言。
	ResolvedLocale string `json:"resolvedLocale"`
}

// handleInitialize 处理 initialize 请求。
func (server *Server) handleInitialize(message requestMessage) error {
	// initialize 可携带 initializationOptions.syntax 覆盖默认扩展集合。
	var params initializeParams
	if len(message.Params) > 0 {
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return server.writeError(message.ID, -32602, err.Error())
		}
	}
	if len(params.InitializationOptions) > 0 && string(params.InitializationOptions) != "null" {
		var options gluaInitializationOptions
		if err := json.Unmarshal(params.InitializationOptions, &options); err != nil {
			return server.writeError(message.ID, -32602, err.Error())
		}
		if strings.TrimSpace(options.Syntax) != "" {
			syntax, err := extensions.ParseSyntaxSet(options.Syntax)
			if err != nil {
				return server.writeError(message.ID, -32602, err.Error())
			}
			server.syntax = syntax
		}
		locale := strings.TrimSpace(options.ResolvedLocale)
		if locale == "" {
			// 未提供解析语言时回退用户直接配置值。
			locale = strings.TrimSpace(options.Locale)
		}
		applyBuiltinCatalogLocale(locale)
	}
	server.workspaceRoots = workspaceRootPaths(params)
	result := map[string]any{
		"serverInfo": map[string]any{
			"name":    "gluals",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    textDocumentSyncFull,
			},
			"documentFormattingProvider": true,
			"definitionProvider":         true,
			"hoverProvider":              true,
			"completionProvider": map[string]any{
				"triggerCharacters": []string{"."},
			},
			"semanticTokensProvider": map[string]any{
				"legend": map[string]any{
					"tokenTypes":     semanticTokenTypes,
					"tokenModifiers": []string{},
				},
				"full": true,
			},
		},
	}
	return server.writeResponse(message.ID, result)
}

// textDocumentItem 表示 LSP textDocument item。
type textDocumentItem struct {
	// URI 保存文档 URI。
	URI string `json:"uri"`
	// Text 保存文档全文。
	Text string `json:"text"`
}

// versionedTextDocumentIdentifier 表示带版本的文档标识。
type versionedTextDocumentIdentifier struct {
	// URI 保存文档 URI。
	URI string `json:"uri"`
}

// didOpenParams 表示 textDocument/didOpen 参数。
type didOpenParams struct {
	// TextDocument 保存打开的文档。
	TextDocument textDocumentItem `json:"textDocument"`
}

// contentChangeEvent 表示文档变更事件。
type contentChangeEvent struct {
	// Text 保存全量文档内容。
	Text string `json:"text"`
}

// didChangeParams 表示 textDocument/didChange 参数。
type didChangeParams struct {
	// TextDocument 保存文档标识。
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
	// ContentChanges 保存变更列表；当前 server 使用 full sync，读取最后一次全文。
	ContentChanges []contentChangeEvent `json:"contentChanges"`
}

// didCloseParams 表示 textDocument/didClose 参数。
type didCloseParams struct {
	// TextDocument 保存关闭的文档标识。
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
}

// handleDidOpen 处理文档打开通知。
func (server *Server) handleDidOpen(params json.RawMessage) error {
	// 保存打开文档全文并立即发布诊断。
	var didOpen didOpenParams
	if err := json.Unmarshal(params, &didOpen); err != nil {
		return err
	}
	server.documents[didOpen.TextDocument.URI] = didOpen.TextDocument.Text
	return server.publishDiagnostics(didOpen.TextDocument.URI, didOpen.TextDocument.Text)
}

// handleDidChange 处理文档全量变更通知。
func (server *Server) handleDidChange(params json.RawMessage) error {
	// 使用最后一个 contentChanges 作为当前全文。
	var didChange didChangeParams
	if err := json.Unmarshal(params, &didChange); err != nil {
		return err
	}
	if len(didChange.ContentChanges) == 0 {
		return nil
	}
	text := didChange.ContentChanges[len(didChange.ContentChanges)-1].Text
	server.documents[didChange.TextDocument.URI] = text
	return server.publishDiagnostics(didChange.TextDocument.URI, text)
}

// handleDidClose 处理文档关闭通知。
func (server *Server) handleDidClose(params json.RawMessage) error {
	// 移除缓存并发布空诊断，清理编辑器红线。
	var didClose didCloseParams
	if err := json.Unmarshal(params, &didClose); err != nil {
		return err
	}
	delete(server.documents, didClose.TextDocument.URI)
	return server.publishDiagnostics(didClose.TextDocument.URI, "")
}

// didChangeConfigurationParams 表示工作区配置变更通知。
type didChangeConfigurationParams struct {
	// Settings 保存客户端发送的设置根对象。
	Settings struct {
		// Glua 保存 GLua 扩展设置。
		Glua struct {
			// DocLanguage 保存用户配置的文档语言。
			DocLanguage string `json:"docLanguage"`
			// ResolvedDocLanguage 保存客户端按界面语言解析后的实际语言。
			ResolvedDocLanguage string `json:"resolvedDocLanguage"`
		} `json:"glua"`
	} `json:"settings"`
}

// handleDidChangeConfiguration 处理文档语言动态变更。
func (server *Server) handleDidChangeConfiguration(params json.RawMessage) error {
	// 配置通知只更新内置文档语言，不重启语言服务器或重新索引文件。
	var changed didChangeConfigurationParams
	if len(params) == 0 {
		// 空通知没有可应用配置。
		return nil
	}
	if err := json.Unmarshal(params, &changed); err != nil {
		// JSON 结构错误交给 LSP 主循环记录。
		return err
	}
	locale := strings.TrimSpace(changed.Settings.Glua.ResolvedDocLanguage)
	if locale == "" {
		// 旧客户端未发送 resolvedDocLanguage 时使用直接配置值。
		locale = strings.TrimSpace(changed.Settings.Glua.DocLanguage)
	}
	applyBuiltinCatalogLocale(locale)
	return nil
}

// formattingParams 表示 textDocument/formatting 参数。
type formattingParams struct {
	// TextDocument 保存待格式化文档标识。
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
}

// textEdit 表示 LSP TextEdit。
type textEdit struct {
	// Range 保存替换范围。
	Range lspRange `json:"range"`
	// NewText 保存替换后的文本。
	NewText string `json:"newText"`
}

// handleFormatting 处理全文格式化请求。
func (server *Server) handleFormatting(message requestMessage) error {
	// 从已打开文档缓存读取全文，调用 glua formatter 后返回单个全量替换 edit。
	var params formattingParams
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return server.writeError(message.ID, -32602, err.Error())
	}
	text, ok := server.documents[params.TextDocument.URI]
	if !ok {
		return server.writeError(message.ID, -32602, "document is not open")
	}
	formatted, err := formatter.Format(text, server.syntax)
	if err != nil {
		return server.writeError(message.ID, -32603, err.Error())
	}
	edit := textEdit{
		Range:   fullDocumentRange(text),
		NewText: formatted,
	}
	return server.writeResponse(message.ID, []textEdit{edit})
}

// textDocumentPositionParams 表示带文档位置的 LSP 请求参数。
type textDocumentPositionParams struct {
	// TextDocument 保存文档标识。
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
	// Position 保存请求位置。
	Position lspPosition `json:"position"`
}

// location 表示 LSP Location。
type location struct {
	// URI 保存目标文档 URI。
	URI string `json:"uri"`
	// Range 保存目标范围。
	Range lspRange `json:"range"`
}

// handleDefinition 处理跳转到定义请求。
func (server *Server) handleDefinition(message requestMessage) error {
	// 从当前文档中查找光标处标识符，再返回最近可见定义位置。
	var params textDocumentPositionParams
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return server.writeError(message.ID, -32602, err.Error())
	}
	text, ok := server.documents[params.TextDocument.URI]
	if !ok {
		return server.writeError(message.ID, -32602, "document is not open")
	}
	targetName := qualifiedIdentifierAtPosition(text, params.Position)
	builtinTargetName := resolveBuiltinQualifiedAlias(text, targetName, params.Position)
	if moduleLocation, ok := server.requiredModuleLocation(text, params.TextDocument.URI, params.Position); ok {
		// 光标位于 require 字符串时优先跳转到解析出的模块文件。
		return server.writeResponse(message.ID, []location{moduleLocation})
	}
	if moduleLocation, ok := server.requiredMemberLocation(text, params.TextDocument.URI, params.Position); ok {
		// require 返回表成员跳转到模块内的导出定义。
		return server.writeResponse(message.ID, []location{moduleLocation})
	}
	if targetName == "" {
		return server.writeResponse(message.ID, nil)
	}
	definition, ok := findDefinition(text, targetName, params.Position)
	if !ok {
		if builtinURI, builtinRange, ok := builtinFunctionLocation(builtinTargetName); ok {
			return server.writeResponse(message.ID, []location{{URI: builtinURI, Range: builtinRange}})
		}
		return server.writeResponse(message.ID, nil)
	}
	return server.writeResponse(message.ID, []location{{URI: params.TextDocument.URI, Range: definition}})
}

// hoverResult 表示 LSP Hover 响应。
type hoverResult struct {
	// Contents 保存 hover 展示内容。
	Contents markupContent `json:"contents"`
	// Range 保存 hover 关联范围。
	Range lspRange `json:"range,omitempty"`
}

// markupContent 表示 LSP MarkupContent。
type markupContent struct {
	// Kind 保存 markup 类型。
	Kind string `json:"kind"`
	// Value 保存展示文本。
	Value string `json:"value"`
}

// completionItem 表示 LSP 补全候选。
type completionItem struct {
	// Label 保存编辑器展示和默认插入的名称。
	Label string `json:"label"`
	// Kind 保存 LSP CompletionItemKind。
	Kind int `json:"kind"`
	// Detail 保存签名或符号类型摘要。
	Detail string `json:"detail,omitempty"`
	// Documentation 保存候选的 Markdown 文档。
	Documentation *markupContent `json:"documentation,omitempty"`
	// InsertText 保存接受函数候选时插入的 snippet。
	InsertText string `json:"insertText,omitempty"`
	// InsertTextFormat=2 表示 InsertText 使用 LSP snippet 语法。
	InsertTextFormat int `json:"insertTextFormat,omitempty"`
}

// handleCompletion 处理补全请求。
//
// 当前优先统一内置函数和常量的补全模型；普通文件符号仍由迁移期间的插件回退分析器提供。
func (server *Server) handleCompletion(message requestMessage) error {
	// 读取光标前的限定名称，并从 Go 侧 builtin 目录生成候选。
	var params textDocumentPositionParams
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return server.writeError(message.ID, -32602, err.Error())
	}
	text, ok := server.documents[params.TextDocument.URI]
	if !ok {
		return server.writeError(message.ID, -32602, "document is not open")
	}
	prefix := completionPrefixAtPosition(text, params.Position)
	builtinPrefix := resolveBuiltinQualifiedAlias(text, prefix, params.Position)
	items := builtinCompletionItems(builtinPrefix)
	items = append(items, server.moduleCompletionItems(text, params.TextDocument.URI, params.Position, prefix)...)
	items = append(items, documentCompletionItems(text, params.Position, prefix)...)
	sort.Slice(items, func(left int, right int) bool {
		// 稳定排序让不同编辑器和测试获得一致候选顺序。
		return items[left].Label < items[right].Label
	})
	return server.writeResponse(message.ID, items)
}

// handleHover 处理 hover 请求。
func (server *Server) handleHover(message requestMessage) error {
	// hover 复用 definition 查找逻辑，展示符号和定义位置，便于确认 gluals 正在接管文件。
	var params textDocumentPositionParams
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return server.writeError(message.ID, -32602, err.Error())
	}
	text, ok := server.documents[params.TextDocument.URI]
	if !ok {
		return server.writeError(message.ID, -32602, "document is not open")
	}
	targetName := qualifiedIdentifierAtPosition(text, params.Position)
	builtinTargetName := resolveBuiltinQualifiedAlias(text, targetName, params.Position)
	if targetName == "" {
		return server.writeResponse(message.ID, nil)
	}
	definition, ok := findDefinition(text, targetName, params.Position)
	if !ok {
		if info, builtinOk := builtinFunctionInfo(builtinTargetName); builtinOk {
			return server.writeResponse(message.ID, hoverResult{
				Contents: markupContent{Kind: "markdown", Value: formatBuiltinHover(builtinTargetName, info)},
			})
		}
		return server.writeResponse(message.ID, hoverResult{
			Contents: markupContent{Kind: "markdown", Value: "`" + targetName + "`"},
		})
	}
	value := fmt.Sprintf("`%s`\n\nDefined at line %d, column %d.", targetName, definition.Start.Line+1, definition.Start.Character+1)
	return server.writeResponse(message.ID, hoverResult{
		Contents: markupContent{Kind: "markdown", Value: value},
		Range:    definition,
	})
}

// semanticTokensParams 表示 semanticTokens/full 请求参数。
type semanticTokensParams struct {
	// TextDocument 保存文档标识。
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
}

// semanticTokensResult 表示 semantic token 响应。
type semanticTokensResult struct {
	// Data 保存 LSP delta 编码后的 token 数据。
	Data []int `json:"data"`
}

// handleSemanticTokensFull 处理全文语义高亮请求。
func (server *Server) handleSemanticTokensFull(message requestMessage) error {
	// 从已打开文档缓存读取全文并生成 LSP semantic token delta 数据。
	var params semanticTokensParams
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return server.writeError(message.ID, -32602, err.Error())
	}
	text, ok := server.documents[params.TextDocument.URI]
	if !ok {
		return server.writeError(message.ID, -32602, "document is not open")
	}
	return server.writeResponse(message.ID, semanticTokensResult{Data: semanticTokens(text, server.syntax)})
}

// diagnostic 表示 LSP Diagnostic。
type diagnostic struct {
	// Range 保存诊断范围。
	Range lspRange `json:"range"`
	// Severity 保存诊断级别。
	Severity int `json:"severity"`
	// Source 保存诊断来源。
	Source string `json:"source"`
	// Message 保存诊断消息。
	Message string `json:"message"`
}

// lspRange 表示 LSP Range。
type lspRange struct {
	// Start 保存起始位置。
	Start lspPosition `json:"start"`
	// End 保存结束位置。
	End lspPosition `json:"end"`
}

// lspPosition 表示 LSP Position，行列均从 0 开始。
type lspPosition struct {
	// Line 保存 0 基行号。
	Line int `json:"line"`
	// Character 保存 0 基字符列。
	Character int `json:"character"`
}

// publishDiagnostics 发布指定文档的诊断。
func (server *Server) publishDiagnostics(uri string, text string) error {
	// didClose 传入空 text 时发布空诊断；其他路径使用 parser 诊断当前全文。
	diagnostics := analyzeDiagnostics(text, server.syntax)
	params := map[string]any{
		"uri":         uri,
		"diagnostics": diagnostics,
	}
	return server.writeNotification("textDocument/publishDiagnostics", params)
}

// analyzeDiagnostics 使用 glua parser 生成 LSP 诊断。
//
// text 是完整文档内容；syntax 控制项目语法糖开关。返回值为空表示没有语法错误。
func analyzeDiagnostics(text string, syntax extensions.SyntaxSet) []diagnostic {
	// 空文档也需要经过 parser；空 chunk 合法时返回无诊断。
	chunk, err := parser.NewWithSyntax(text, syntax).ParseChunk()
	if err != nil {
		parseErrors := collectParseErrors(err)
		diagnostics := make([]diagnostic, 0, len(parseErrors))
		for _, parseError := range parseErrors {
			diagnostics = append(diagnostics, diagnosticFromParseError(parseError))
		}
		return diagnostics
	}
	if _, err := codegen.CompileChunk(chunk, "lsp"); err != nil {
		// parser 通过后继续执行 codegen 语义诊断，覆盖 const 重新赋值等非纯语法错误。
		return []diagnostic{diagnosticFromCompileError(err)}
	}
	return make([]diagnostic, 0)
}

// diagnosticFromCompileError 将 codegen 语义错误转换为 LSP 诊断。
func diagnosticFromCompileError(err error) diagnostic {
	// codegen 错误当前采用 `line N: message` 文本，先从文本中恢复行号，后续可替换为结构化错误。
	message := err.Error()
	startLine := 0
	if strings.HasPrefix(message, "line ") {
		rest := strings.TrimPrefix(message, "line ")
		colonIndex := strings.Index(rest, ":")
		if colonIndex > 0 {
			if parsedLine, parseErr := strconv.Atoi(rest[:colonIndex]); parseErr == nil && parsedLine > 0 {
				startLine = parsedLine - 1
				message = strings.TrimSpace(rest[colonIndex+1:])
			}
		}
	}
	return diagnostic{
		Range: lspRange{
			Start: lspPosition{Line: startLine, Character: 0},
			End:   lspPosition{Line: startLine, Character: 1},
		},
		Severity: diagnosticSeverityError,
		Source:   "glua",
		Message:  message,
	}
}

// collectParseErrors 从错误链中提取 parser 错误列表。
func collectParseErrors(err error) []parser.ParseError {
	// 优先保留聚合语义错误的多条诊断。
	var parseErrors parser.ParseErrorList
	if errors.As(err, &parseErrors) {
		return []parser.ParseError(parseErrors)
	}
	var parseError parser.ParseError
	if errors.As(err, &parseError) {
		return []parser.ParseError{parseError}
	}
	return []parser.ParseError{{
		Message: err.Error(),
	}}
}

// diagnosticFromParseError 将 parser 错误转换为 LSP 诊断。
func diagnosticFromParseError(parseError parser.ParseError) diagnostic {
	// parser 行列从 1 开始，LSP 行列从 0 开始。
	startLine := parseError.Position.Line - 1
	if startLine < 0 {
		startLine = 0
	}
	startColumn := parseError.Position.Column - 1
	if startColumn < 0 {
		startColumn = 0
	}
	message := parseError.Message
	if parseError.Near != "" {
		message = "syntax error near " + parseError.Near
		if parseError.Message != "" {
			message += ": " + parseError.Message
		}
	}
	return diagnostic{
		Range: lspRange{
			Start: lspPosition{Line: startLine, Character: startColumn},
			End:   lspPosition{Line: startLine, Character: startColumn + 1},
		},
		Severity: diagnosticSeverityError,
		Source:   "glua",
		Message:  message,
	}
}

// fullDocumentRange 返回覆盖整个文档的 LSP Range。
func fullDocumentRange(text string) lspRange {
	// 计算最后一行和最后一列，供格式化 edit 全量替换。
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return lspRange{}
	}
	lastLine := len(lines) - 1
	lastCharacter := len(lines[lastLine])
	return lspRange{
		Start: lspPosition{Line: 0, Character: 0},
		End:   lspPosition{Line: lastLine, Character: lastCharacter},
	}
}

// tokenInfo 保存单个源码 token 的 LSP 位置和文本。
type tokenInfo struct {
	// Text 保存 token 文本。
	Text string
	// Kind 保存 lexer token 类型。
	Kind lexer.TokenKind
	// Range 保存 token 覆盖范围。
	Range lspRange
}

// identifierAtPosition 返回指定位置覆盖的标识符。
func identifierAtPosition(text string, position lspPosition) string {
	// 遍历 token，找到包含当前位置的 identifier。
	for _, token := range scanTokens(text) {
		if token.Kind != lexer.TokenIdentifier && token.Kind != lexer.TokenKeyword {
			// 非标识符和上下文关键字不参与跳转。
			continue
		}
		if positionInRange(position, token.Range) {
			return token.Text
		}
	}
	return ""
}

// qualifiedIdentifierAtPosition 返回指定位置覆盖的点号限定标识符。
//
// 对 `events.function_error` 返回完整名称；普通局部变量仍返回单段名称。
func qualifiedIdentifierAtPosition(text string, position lspPosition) string {
	// 先定位光标覆盖 token，再向左拼接连续的 `identifier . identifier` 链。
	tokens := scanTokens(text)
	for index, token := range tokens {
		if token.Kind != lexer.TokenIdentifier && token.Kind != lexer.TokenKeyword {
			// 非标识符不能作为限定名称末段。
			continue
		}
		if !positionInRange(position, token.Range) {
			// 未覆盖光标的 token 继续扫描。
			continue
		}
		start := index
		for start >= 2 && tokens[start-1].Text == "." && tokens[start-2].Kind == lexer.TokenIdentifier {
			start -= 2
		}
		parts := make([]string, 0, index-start+1)
		for partIndex := start; partIndex <= index; partIndex++ {
			parts = append(parts, tokens[partIndex].Text)
		}
		return strings.Join(parts, "")
	}
	return ""
}

// completionPrefixAtPosition 返回光标前可参与 builtin 补全的限定前缀。
func completionPrefixAtPosition(text string, position lspPosition) string {
	// 只截取当前行光标前文本，避免跨行把无关表达式拼进前缀。
	lines := strings.Split(text, "\n")
	if position.Line < 0 || position.Line >= len(lines) {
		// 非法行号没有可用补全上下文。
		return ""
	}
	line := []rune(lines[position.Line])
	character := position.Character
	if character < 0 {
		// 负列号按行首处理。
		character = 0
	}
	if character > len(line) {
		// 超过行尾时按行尾处理。
		character = len(line)
	}
	start := character
	for start > 0 && (isIdentifierRune(line[start-1]) || line[start-1] == '.' || line[start-1] == ':') {
		start--
	}
	return string(line[start:character])
}

// resolveBuiltinQualifiedAlias 展开限定名称首段的当前可见内置命名空间别名。
//
// 仅解析 `local/const alias = builtin.namespace` 形式的简单点号链；普通单段变量保持原名，
// 以便点击 alias 自身时仍跳转到局部声明。
func resolveBuiltinQualifiedAlias(text string, qualifiedName string, position lspPosition) string {
	// 没有成员访问的名称不展开，保留局部变量 definition 语义。
	separatorIndex := strings.IndexAny(qualifiedName, ".:")
	if separatorIndex <= 0 {
		// 空名称、单段名称或非法首段都直接返回。
		return qualifiedName
	}
	rootName := qualifiedName[:separatorIndex]
	aliases := builtinNamespaceAliases(text, position)
	resolvedRoot, ok := aliases[rootName]
	if !ok {
		// 首段不是当前可见别名时保持原限定名。
		return qualifiedName
	}
	return resolvedRoot + qualifiedName[separatorIndex:]
}

// builtinNamespaceAliases 收集光标前可见的简单内置命名空间别名。
func builtinNamespaceAliases(text string, position lspPosition) map[string]string {
	// 按源码顺序处理声明，使后续同名声明覆盖或清除前序别名。
	tokens := scanTokens(text)
	aliases := make(map[string]string)
	for index := 0; index+3 < len(tokens); index++ {
		// 只读取光标前已经完成的声明。
		if !rangeBeforeOrEqual(tokens[index].Range, position) {
			// 后续 token 不参与当前可见别名解析。
			break
		}
		if tokens[index].Text != "local" && tokens[index].Text != "const" {
			// 普通赋值可能改变运行期对象，不作为静态命名空间别名。
			continue
		}
		aliasToken := tokens[index+1]
		if aliasToken.Kind != lexer.TokenIdentifier || tokens[index+2].Text != "=" {
			// 多变量声明和非简单赋值不在当前保守解析范围内。
			continue
		}
		rightStart := index + 3
		if tokens[rightStart].Kind != lexer.TokenIdentifier && tokens[rightStart].Kind != lexer.TokenKeyword {
			// 右值不是名称链时清除同名旧别名。
			delete(aliases, aliasToken.Text)
			continue
		}
		rightParts := []string{tokens[rightStart].Text}
		rightEnd := rightStart
		for rightEnd+2 < len(tokens) && tokens[rightEnd+1].Text == "." && (tokens[rightEnd+2].Kind == lexer.TokenIdentifier || tokens[rightEnd+2].Kind == lexer.TokenKeyword) {
			// 连续点号成员组成可静态展开的命名空间链。
			rightParts = append(rightParts, ".", tokens[rightEnd+2].Text)
			rightEnd += 2
		}
		rightName := strings.Join(rightParts, "")
		if dotIndex := strings.Index(rightName, "."); dotIndex > 0 {
			// 链式别名先展开右值首段，例如 events = event.events。
			if resolvedRoot, found := aliases[rightName[:dotIndex]]; found {
				rightName = resolvedRoot + rightName[dotIndex:]
			}
		}
		if !isBuiltinNamespace(rightName) {
			// 右值无法解析到内置目录时清除同名旧别名，避免错误跳转。
			delete(aliases, aliasToken.Text)
			continue
		}
		aliases[aliasToken.Text] = rightName
		index = rightEnd
	}
	return aliases
}

// isBuiltinNamespace 判断名称是否是内置条目或内置条目的命名空间前缀。
func isBuiltinNamespace(name string) bool {
	// 完整函数或常量名称本身也可以作为后续链式别名的右值。
	if _, ok := builtinFunctionInfo(name); ok {
		// 已知内置条目直接命中。
		return true
	}
	prefix := name + "."
	for builtinName := range builtinFunctionDocs {
		// 任一内置函数位于该前缀下即可确认命名空间。
		if strings.HasPrefix(builtinName, prefix) {
			return true
		}
	}
	for _, constantName := range builtinConstantNames {
		// 事件常量目录同样参与命名空间判断。
		if strings.HasPrefix(constantName, prefix) {
			return true
		}
	}
	return false
}

// findDefinition 查找指定名称在文档内的定义位置。
func findDefinition(text string, name string, position lspPosition) (lspRange, bool) {
	// 收集光标前的所有定义，返回最近的一个；这是局部变量和函数定义的实用近似。
	tokens := scanTokens(text)
	var best lspRange
	found := false
	for index := range tokens {
		if !rangeBeforeOrEqual(tokens[index].Range, position) {
			// 后续定义不应影响当前位置的跳转。
			continue
		}
		if isDefinitionAt(tokens, index, name) {
			best = tokens[index].Range
			found = true
		}
	}
	if found {
		return best, true
	}
	for index := range tokens {
		if isDefinitionAt(tokens, index, name) {
			// 找不到前置定义时回退到全文第一个定义，支持顶层函数互相调用。
			return tokens[index].Range, true
		}
	}
	return lspRange{}, false
}

// isDefinitionAt 判断 tokens[index] 是否是指定名称的定义点。
func isDefinitionAt(tokens []tokenInfo, index int, name string) bool {
	// 定义点覆盖 local name、function name 和 label name。
	if index < 0 || index >= len(tokens) || tokens[index].Text != name {
		return false
	}
	if index > 0 && tokens[index-1].Text == "local" {
		return true
	}
	if index > 0 && tokens[index-1].Text == "const" {
		// glua const 声明与 local 一样是名称定义点。
		return true
	}
	if index > 0 && tokens[index-1].Text == "function" {
		return true
	}
	if index > 1 && tokens[index-2].Text == "local" && tokens[index-1].Text == "function" {
		return true
	}
	if index > 0 && index+1 < len(tokens) && tokens[index-1].Text == "::" && tokens[index+1].Text == "::" {
		return true
	}
	return false
}

// semanticTokens 生成 LSP semantic token delta 编码。
func semanticTokens(text string, syntax extensions.SyntaxSet) []int {
	// 逐 token 分类并按 LSP semantic token 5 元组 delta 编码输出。
	tokens := scanTokens(text)
	data := make([]int, 0, len(tokens)*5)
	lastLine := 0
	lastStart := 0
	for _, token := range tokens {
		tokenType, ok := semanticTypeForToken(token, syntax)
		if !ok {
			// 不具备语义分类的 token 继续由 TextMate grammar 着色。
			continue
		}
		if token.Range.Start.Line != token.Range.End.Line {
			// LSP semantic token 不能跨行，多行长字符串交给 TextMate grammar 处理。
			continue
		}
		line := token.Range.Start.Line
		start := token.Range.Start.Character
		deltaLine := line - lastLine
		deltaStart := start
		if deltaLine == 0 {
			deltaStart = start - lastStart
		}
		length := token.Range.End.Character - token.Range.Start.Character
		if length <= 0 {
			continue
		}
		data = append(data, deltaLine, deltaStart, length, tokenType, 0)
		lastLine = line
		lastStart = start
	}
	return data
}

// semanticTypeForToken 返回 token 的 semantic token 类型。
func semanticTypeForToken(token tokenInfo, syntax extensions.SyntaxSet) (int, bool) {
	// 按 glua 词法类型和关键字集合映射 VS Code 可识别的 token 类型。
	switch token.Kind {
	case lexer.TokenKeyword:
		return semanticTypeKeyword, true
	case lexer.TokenString:
		return semanticTypeString, true
	case lexer.TokenNumber:
		return semanticTypeNumber, true
	case lexer.TokenOperator:
		return semanticTypeOperator, true
	case lexer.TokenIdentifier:
		if isContextKeyword(token.Text, syntax) {
			return semanticTypeKeyword, true
		}
		if isBuiltinFunction(token.Text) {
			return semanticTypeFunction, true
		}
		return semanticTypeVariable, true
	default:
		return 0, false
	}
}

// scanTokens 扫描完整文档中的 token。
func scanTokens(text string) []tokenInfo {
	// 使用项目 lexer 保证 LSP 看到的 token 与 parser 一致。
	tokenLexer := lexer.New(text)
	tokens := make([]tokenInfo, 0, 64)
	for {
		token := tokenLexer.NextToken()
		if token.Kind == lexer.TokenEOF {
			// EOF 只终止扫描，不生成可见 token。
			break
		}
		if token.Kind == lexer.TokenIllegal {
			// 非法 token 已由 diagnostics 报告，不参与语义高亮。
			continue
		}
		tokens = append(tokens, tokenInfo{
			Text:  token.Text,
			Kind:  token.Kind,
			Range: semanticTokenSourceRange(text, token),
		})
	}
	return tokens
}

// semanticTokenSourceRange 按源码原始字面量计算 token 的 UTF-16 LSP 范围。
//
// text 必须是 lexer 使用的同一份源码；token.Position.Offset 是 UTF-8 字节偏移。字符串 token 的 Text
// 已去除引号并完成解码，因此不能直接用 Text 长度推导高亮范围。
func semanticTokenSourceRange(text string, token lexer.Token) lspRange {
	// 先约束起始字节偏移，避免损坏输入导致切片越界。
	startOffset := token.Position.Offset
	if startOffset < 0 {
		// 负偏移回退到文档开头，仅影响当前异常 token。
		startOffset = 0
	} else if startOffset > len(text) {
		// 超过文档末尾时夹紧到 EOF，保证 LSP server 不 panic。
		startOffset = len(text)
	}
	endOffset := startOffset + len(token.Text)
	if token.Kind == lexer.TokenString {
		// 字符串必须按原始引号、转义和长括号分隔符计算实际源码长度。
		endOffset = sourceStringTokenEnd(text, startOffset)
	}
	if endOffset < startOffset {
		// 反向范围没有 LSP 语义，退化为空范围。
		endOffset = startOffset
	} else if endOffset > len(text) {
		// 原始范围超过 EOF 时夹紧，避免读取文档外字节。
		endOffset = len(text)
	}

	startLine := token.Position.Line - 1
	if startLine < 0 {
		// lexer 行号异常时回退到第一行。
		startLine = 0
	}
	lineStartOffset := strings.LastIndex(text[:startOffset], "\n") + 1
	startCharacter := utf16CodeUnitCount(text[lineStartOffset:startOffset])
	rawToken := text[startOffset:endOffset]
	newlineCount := strings.Count(rawToken, "\n")
	if newlineCount == 0 {
		// 单行 token 的结束列可直接累加原始源码 UTF-16 长度。
		return lspRange{
			Start: lspPosition{Line: startLine, Character: startCharacter},
			End:   lspPosition{Line: startLine, Character: startCharacter + utf16CodeUnitCount(rawToken)},
		}
	}
	lastNewlineOffset := strings.LastIndex(rawToken, "\n")
	return lspRange{
		Start: lspPosition{Line: startLine, Character: startCharacter},
		End: lspPosition{
			Line:      startLine + newlineCount,
			Character: utf16CodeUnitCount(rawToken[lastNewlineOffset+1:]),
		},
	}
}

// sourceStringTokenEnd 返回字符串 token 在原始源码中的结束字节偏移。
//
// startOffset 必须指向短字符串引号或长括号起始符；输入不完整时返回文档末尾，让 diagnostics 负责报告错误。
func sourceStringTokenEnd(text string, startOffset int) int {
	// 先拒绝越界起点，避免后续读取首字节失败。
	if startOffset < 0 || startOffset >= len(text) {
		// 无可读字符串时返回夹紧后的 EOF。
		return len(text)
	}
	opening := text[startOffset]
	if opening == '\'' || opening == '"' {
		// 短字符串按反斜杠转义跳过下一字节，直到找到未转义的同类引号。
		for offset := startOffset + 1; offset < len(text); offset++ {
			if text[offset] == '\\' {
				// 转义符和后续字节属于同一原始字符串；末尾孤立转义会自然落到 EOF。
				offset++
				continue
			}
			if text[offset] == opening {
				// 返回闭合引号后一字节，确保 semantic token 覆盖完整字面量。
				return offset + 1
			}
		}
		return len(text)
	}
	if opening != '[' {
		// 非字符串起始符按 token.Text 的调用方回退语义返回原位置。
		return startOffset
	}
	equalEnd := startOffset + 1
	for equalEnd < len(text) && text[equalEnd] == '=' {
		// 长括号允许任意数量的等号，逐字节定位第二个左方括号。
		equalEnd++
	}
	if equalEnd >= len(text) || text[equalEnd] != '[' {
		// 不完整长括号交给 lexer diagnostics，范围延伸到 EOF。
		return len(text)
	}
	equalCount := equalEnd - startOffset - 1
	closingDelimiter := "]" + strings.Repeat("=", equalCount) + "]"
	closingRelativeOffset := strings.Index(text[equalEnd+1:], closingDelimiter)
	if closingRelativeOffset < 0 {
		// 找不到闭合分隔符时覆盖剩余文档，与未完成长字符串诊断一致。
		return len(text)
	}
	return equalEnd + 1 + closingRelativeOffset + len(closingDelimiter)
}

// utf16CodeUnitCount 返回字符串在 LSP position 中占用的 UTF-16 code unit 数量。
func utf16CodeUnitCount(text string) int {
	// LSP Character 使用 UTF-16 编码宽度，补充平面字符需要计为两个 code unit。
	count := 0
	for _, character := range text {
		count += utf16.RuneLen(character)
	}
	return count
}

// positionInRange 判断 position 是否落在 range 内。
func positionInRange(position lspPosition, tokenRange lspRange) bool {
	// 当前 token 都是单行范围，按起止列判断即可。
	if position.Line != tokenRange.Start.Line {
		return false
	}
	return position.Character >= tokenRange.Start.Character && position.Character <= tokenRange.End.Character
}

// rangeBeforeOrEqual 判断 tokenRange 是否不晚于 position。
func rangeBeforeOrEqual(tokenRange lspRange, position lspPosition) bool {
	// 定义查找只考虑光标之前的 token。
	if tokenRange.Start.Line < position.Line {
		return true
	}
	if tokenRange.Start.Line == position.Line && tokenRange.Start.Character <= position.Character {
		return true
	}
	return false
}

// isContextKeyword 判断标识符是否是 glua 上下文关键字。
func isContextKeyword(text string, syntax extensions.SyntaxSet) bool {
	// glua 扩展关键字在 lexer 中可能仍是 identifier，需要语义高亮为关键字。
	switch text {
	case "switch", "case", "default":
		return syntax.Has(extensions.SyntaxSwitch)
	case "continue":
		return syntax.Has(extensions.SyntaxContinue)
	case "const":
		return syntax.Has(extensions.SyntaxConst)
	default:
		return false
	}
}

// isAnyContextKeyword 判断文本是否是 glua 已知上下文关键字。
func isAnyContextKeyword(text string) bool {
	// 不带 syntax 的场景只用于候选识别，不代表当前配置启用。
	switch text {
	case "switch", "case", "default", "continue", "const":
		return true
	default:
		return false
	}
}

type builtinFunctionDoc struct {
	// Kind 保存 function 或 constant，用于补全和伪定义目标统一分类。
	Kind string
	// Signature 保存函数签名，参数部分保持与官方文档一致。
	Signature string
	// Returns 保存函数返回说明。
	Returns string
	// Parameters 保存参数说明列表。
	Parameters []string
	// Description 保存函数功能说明。
	Description string
	// Example 保存可直接运行的示例代码。
	Example string
	// Locale 保存当前文档实际使用的语言。
	Locale string
}

// builtinCatalogFile 表示插件随包提供的 builtin JSON 文档。
type builtinCatalogFile struct {
	// Functions 保存按完整名称索引的 builtin 条目。
	Functions map[string]builtinCatalogEntry `json:"functions"`
}

// builtinCatalogEntry 表示单个 builtin JSON 条目。
type builtinCatalogEntry struct {
	// Signature 保存按语言区分的签名。
	Signature map[string]string `json:"signature"`
	// Returns 保存按语言区分的返回说明。
	Returns map[string]string `json:"returns"`
	// Parameters 保存按语言区分的参数说明。
	Parameters map[string][]string `json:"params"`
	// Description 保存按语言区分的功能说明。
	Description map[string]string `json:"description"`
	// Example 保存按语言区分的示例代码。
	Example map[string]string `json:"example"`
}

var (
	// builtinCatalogEntries 保存尚未本地化的插件内置目录，初始化和配置变更时可重新选择语言。
	builtinCatalogEntries = make(map[string]builtinCatalogEntry)
	// activeBuiltinCatalogLocale 保存当前 LSP 进程使用的文档语言。
	activeBuiltinCatalogLocale = "en"
)

// LoadBuiltinCatalogFiles 加载插件随包的 builtin JSON 目录。
//
// files 必须是本地常规文件路径；不可读取的文件返回错误，成功条目会覆盖同名内置文档。
func LoadBuiltinCatalogFiles(files []string) error {
	// 逐个解析 catalog，并把无参数签名条目登记为 Constant。
	for _, file := range files {
		if strings.TrimSpace(file) == "" {
			// 空路径由调用方忽略，便于插件拼接可选配置。
			continue
		}
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var catalog builtinCatalogFile
		if err := json.Unmarshal(content, &catalog); err != nil {
			return err
		}
		for name, entry := range catalog.Functions {
			if catalogText(entry.Signature, "en") == "" {
				// 缺少签名的无效条目不参与语言服务。
				continue
			}
			builtinCatalogEntries[name] = entry
		}
	}
	applyBuiltinCatalogLocale(activeBuiltinCatalogLocale)
	return nil
}

// catalogText 按指定语言选择 catalog 文本，缺失时回退英文和任意可用语言。
func catalogText(values map[string]string, locale string) string {
	// 空 map 时返回空文本。
	if values == nil {
		return ""
	}
	locale = normalizeBuiltinCatalogLocale(locale)
	if value := strings.TrimSpace(values[locale]); value != "" {
		// 精确语言命中时直接返回。
		return value
	}
	if value := strings.TrimSpace(values["en"]); value != "" {
		return value
	}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// catalogParameters 按指定语言选择参数列表，缺失时回退英文和任意可用语言。
func catalogParameters(values map[string][]string, locale string) []string {
	// 空 map 时返回 nil。
	if values == nil {
		return nil
	}
	locale = normalizeBuiltinCatalogLocale(locale)
	if value, ok := values[locale]; ok {
		// 精确语言命中时复制列表，避免目录被调用方修改。
		return append([]string(nil), value...)
	}
	if value, ok := values["en"]; ok {
		return append([]string(nil), value...)
	}
	for _, value := range values {
		return value
	}
	return nil
}

// normalizeBuiltinCatalogLocale 把编辑器语言规范为当前内置目录支持的中英文标签。
func normalizeBuiltinCatalogLocale(locale string) string {
	// auto 已由客户端解析；缺失或其他语言统一回退英文。
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(locale, "_", "-")))
	if strings.HasPrefix(normalized, "zh") {
		// 简体、繁体界面当前统一使用项目提供的中文文档。
		return "zh-CN"
	}
	return "en"
}

// applyBuiltinCatalogLocale 使用指定语言重新生成可展示的内置目录。
func applyBuiltinCatalogLocale(locale string) {
	// 每次应用都从未本地化目录重建插件条目，避免语言切换残留旧文本。
	activeBuiltinCatalogLocale = normalizeBuiltinCatalogLocale(locale)
	for name, entry := range builtinCatalogEntries {
		// 每个条目独立选择当前语言并覆盖默认文档。
		signature := catalogText(entry.Signature, activeBuiltinCatalogLocale)
		if signature == "" {
			// 缺少签名的损坏条目不参与展示。
			continue
		}
		kind := "function"
		if !strings.Contains(signature, "(") {
			// 无调用括号的条目作为常量展示。
			kind = "constant"
			appendBuiltinConstant(name)
		}
		builtinFunctionDocs[name] = builtinFunctionDoc{
			Kind:        kind,
			Signature:   signature,
			Returns:     catalogText(entry.Returns, activeBuiltinCatalogLocale),
			Parameters:  catalogParameters(entry.Parameters, activeBuiltinCatalogLocale),
			Description: catalogText(entry.Description, activeBuiltinCatalogLocale),
			Example:     catalogText(entry.Example, activeBuiltinCatalogLocale),
			Locale:      activeBuiltinCatalogLocale,
		}
	}
}

var builtinFunctionDocs = map[string]builtinFunctionDoc{
	"assert": {
		Signature:   "assert(v [, message])",
		Returns:     "returns: the original `v` and all following arguments unchanged; when `v` is false/nil, throws an error.",
		Parameters:  []string{"v: any value to test", "message (optional): custom error value"},
		Description: "检查第一个参数是否为真值。若为真则原样返回全部参数，若为假则抛出错误。",
	},
	"collectgarbage": {
		Signature: "collectgarbage(opt [, arg])",
		Returns:   "returns: command result, depends on `opt`.",
		Parameters: []string{
			"opt (optional): command string, defaults to \"collect\"",
			"arg (optional): command argument",
		},
		Description: "执行或控制垃圾回收接口。当前实现兼容 Lua 5.3 常见命令语义。",
	},
	"dofile": {
		Signature:   "dofile(filename)",
		Returns:     "returns: the chunk return values when execution is available.",
		Parameters:  []string{"filename: file path string"},
		Description: "读取并执行一个 Lua 文件。当前阶段保留加载与错误边界，文件执行能力可按运行时阶段逐步完善。",
	},
	"error": {
		Signature: "error(object [, level])",
		Returns:   "returns: never returns normally; throws an error object.",
		Parameters: []string{
			"object: error object",
			"level (optional): stack level",
		},
		Description: "显式抛出错误，通常会附带堆栈信息。",
	},
	"getmetatable": {
		Signature:   "getmetatable(object)",
		Returns:     "returns: object's metatable or nil.",
		Parameters:  []string{"object: Lua value"},
		Description: "返回对象的元表；若元表被锁定，则返回其保护后的元表。",
	},
	"ipairs": {
		Signature:   "ipairs(t)",
		Returns:     "returns: iterator function, initial index (0), table.",
		Parameters:  []string{"t: table-like value"},
		Description: "返回用于数组风格遍历的三元组。",
	},
	"load": {
		Signature: "load(chunk [, chunkname [, mode [, env]]])",
		Returns:   "returns: compiled function, or nil plus error message.",
		Parameters: []string{
			"chunk: string/reader",
			"chunkname (optional): chunk source name",
			"mode (optional): load mode",
			"env (optional): environment table",
		},
		Description: "将代码片段编译为匿名函数，不会立即执行。",
	},
	"loadfile": {
		Signature: "loadfile([filename [, mode [, env]]])",
		Returns:   "returns: compiled function, or nil plus error message.",
		Parameters: []string{
			"filename (optional): file path",
			"mode (optional): load mode",
			"env (optional): environment table",
		},
		Description: "从文件中加载并编译 chunk，默认返回匿名函数。",
	},
	"next": {
		Signature:   "next(table [, index])",
		Returns:     "returns: next key and value, or nil.",
		Parameters:  []string{"table: table", "index (optional): previous key"},
		Description: "按内部遍历顺序返回 table 的下一组键值对。",
	},
	"pairs": {
		Signature:   "pairs(t)",
		Returns:     "returns: iterator, state, initial value.",
		Parameters:  []string{"t: table or userdata"},
		Description: "返回泛型遍历所需的迭代器三元组。",
	},
	"pcall": {
		Signature:   "pcall(f, arg1, ...)",
		Returns:     "returns: boolean status, followed by results or error.",
		Parameters:  []string{"f: callable", "arg1, ...: function arguments"},
		Description: "保护调用函数，捕获错误并返回状态码与返回值。",
	},
	"print": {
		Signature:   "print(...)",
		Returns:     "returns: no return values.",
		Parameters:  []string{"...: values to print"},
		Description: "把参数转换为字符串并输出到标准输出。",
	},
	"require": {
		Signature:   "require(modname)",
		Returns:     "returns: module value from cache or newly loaded value.",
		Parameters:  []string{"modname: module name"},
		Description: "按 package 加载规则加载模块并返回结果。",
	},
	"rawequal": {
		Signature:   "rawequal(v1, v2)",
		Returns:     "returns: boolean.",
		Parameters:  []string{"v1: value", "v2: value"},
		Description: "不触发元方法，进行直接比较。",
	},
	"rawget": {
		Signature:   "rawget(table, index)",
		Returns:     "returns: raw value at key.",
		Parameters:  []string{"table: table", "index: key"},
		Description: "直接读取表字段，不触发 `__index`。",
	},
	"rawlen": {
		Signature:   "rawlen(v)",
		Returns:     "returns: integer length.",
		Parameters:  []string{"v: table or string"},
		Description: "返回数组/字符串在不触发元方法情况下的长度。",
	},
	"rawset": {
		Signature:   "rawset(table, index, value)",
		Returns:     "returns: table.",
		Parameters:  []string{"table: table", "index: key", "value: value"},
		Description: "直接设置表字段，不触发 `__newindex`。",
	},
	"select": {
		Signature:   "select(index, ...)",
		Returns:     "returns: vararg slice or count.",
		Parameters:  []string{"index: integer or '#'", "...: candidate values"},
		Description: "按索引选择变长参数，或返回参数个数。",
	},
	"setmetatable": {
		Signature:   "setmetatable(object, metatable)",
		Returns:     "returns: object.",
		Parameters:  []string{"object: table", "metatable: metatable or nil"},
		Description: "给 table 设置元表，当前实现保持 Lua 语义约束。",
	},
	"tonumber": {
		Signature:   "tonumber(e [, base])",
		Returns:     "returns: number or nil.",
		Parameters:  []string{"e: value to convert", "base (optional): numeric base"},
		Description: "将字符串转换为数字；转换失败返回 nil。",
	},
	"tostring": {
		Signature:   "tostring(v)",
		Returns:     "returns: string.",
		Parameters:  []string{"v: value"},
		Description: "将值转换为字符串，并触发可用的 `__tostring` 元方法。",
	},
	"type": {
		Signature:   "type(v)",
		Returns:     "returns: string type name.",
		Parameters:  []string{"v: value"},
		Description: "返回 Lua 类型名，如 `number`、`table` 等。",
	},
	"xpcall": {
		Signature:   "xpcall(f, err, ...)",
		Returns:     "returns: boolean status, followed by function results or error handler result.",
		Parameters:  []string{"f: callable", "err: error handler", "...: function arguments"},
		Description: "带错误处理器的保护调用。",
	},
	"glua.event.setProgress": {
		Signature:   "glua.event.setProgress(event, callback [, config])",
		Returns:     "returns: event id.",
		Parameters:  []string{"event: preset or custom event name", "callback: function(ctx)", "config (optional): filters and reliability options"},
		Description: "注册当前源码文件内同步执行的进度事件回调；支持限次、优先级、节流、采样及传播、忽略、静音、删除错误策略。",
	},
	"glua.event.setProgressAsync": {
		Signature:   "glua.event.setProgressAsync(event, callback [, config])",
		Returns:     "returns: event id.",
		Parameters:  []string{"event: preset or custom event name", "callback: function(ctx)", "config (optional): filters, reliability, debounce, and queue options"},
		Description: "注册在后续 VM 安全点执行的进度事件回调；额外支持无后台线程的防抖合并和队列背压。",
	},
	"glua.event.callProgress": {
		Signature:   "glua.event.callProgress(event [, payload])",
		Returns:     "returns: nil.",
		Parameters:  []string{"event: custom event name", "payload (optional): callback payload"},
		Description: "同步触发当前源码文件的自定义事件。",
	},
	"glua.event.callProgressAsync": {
		Signature:   "glua.event.callProgressAsync(event [, payload])",
		Returns:     "returns: nil.",
		Parameters:  []string{"event: custom event name", "payload (optional): callback payload"},
		Description: "异步排队当前源码文件的自定义事件。",
	},
	"glua.event.remove": {
		Signature:   "glua.event.remove(id)",
		Returns:     "returns: true when removed.",
		Parameters:  []string{"id: event id"},
		Description: "删除一个事件注册。",
	},
	"glua.event.setMuted": {
		Signature:   "glua.event.setMuted(id, muted)",
		Returns:     "returns: true when updated.",
		Parameters:  []string{"id: event id", "muted: boolean"},
		Description: "临时启用或静音一个事件注册。",
	},
	"glua.event.setCallback": {
		Signature:   "glua.event.setCallback(id, callback)",
		Returns:     "returns: true when updated.",
		Parameters:  []string{"id: event id", "callback: replacement function(ctx)"},
		Description: "替换监听器回调函数，保留事件 ID、配置和统计。",
	},
	"glua.event.setConfig": {
		Signature:   "glua.event.setConfig(id, config)",
		Returns:     "returns: true when updated.",
		Parameters:  []string{"id: event id", "config: filter table"},
		Description: "替换事件的函数过滤配置。",
	},
	"glua.event.getConfig": {
		Signature:   "glua.event.getConfig(id)",
		Returns:     "returns: current config table or nil.",
		Parameters:  []string{"id: event id"},
		Description: "返回事件注册关联的配置表。",
	},
	"glua.event.eventList": {
		Signature:   "glua.event.eventList()",
		Returns:     "returns: current source event listener summary table.",
		Parameters:  []string{},
		Description: "返回当前源码文件已注册事件及同步、异步、活跃和静音监听器统计。",
	},
	"glua.event.get": {
		Signature:   "glua.event.get(id)",
		Returns:     "returns: listener snapshot or nil.",
		Parameters:  []string{"id: event id"},
		Description: "返回监听器回调、配置、状态与累计统计快照。",
	},
	"glua.event.clear": {
		Signature:   "glua.event.clear([event])",
		Returns:     "returns: removed listener count.",
		Parameters:  []string{"event (optional): event name"},
		Description: "清理当前源码文件全部监听器或指定事件监听器。",
	},
	"glua.event.setGroupMuted": {
		Signature:   "glua.event.setGroupMuted(group, muted)",
		Returns:     "returns: updated listener count.",
		Parameters:  []string{"group: listener group", "muted: boolean"},
		Description: "批量静音或启用当前源码文件中的监听器分组。",
	},
	"glua.event.removeGroup": {
		Signature:   "glua.event.removeGroup(group)",
		Returns:     "returns: removed listener count.",
		Parameters:  []string{"group: listener group"},
		Description: "删除当前源码文件指定分组的监听器。",
	},
	"glua.event.flush": {
		Signature:   "glua.event.flush()",
		Returns:     "returns: executed async task count.",
		Parameters:  []string{},
		Description: "立即消费当前 State 的异步事件队列。",
	},
	"glua.event.stats": {
		Signature:   "glua.event.stats()",
		Returns:     "returns: current source listener and queue statistics.",
		Parameters:  []string{},
		Description: "返回当前源码文件监听器、队列、丢弃任务和回调错误统计。",
	},
	"glua.json.encode": {
		Signature:   "glua.json.encode(value [, prettyOrOptions])",
		Returns:     "returns: JSON string.",
		Parameters:  []string{"value: serializable Lua value", "pretty (optional): boolean"},
		Description: "把 Lua 标量、对象或连续数组编码为 JSON；循环、稀疏和混合键 table 会返回错误。",
	},
	"glua.json.decode": {
		Signature:   "glua.json.decode(text [, options])",
		Returns:     "returns: decoded Lua value.",
		Parameters:  []string{"text: one JSON document"},
		Description: "解码单个 JSON 文档，null 映射为只读 glua.null。",
	},
	"glua.yaml.encode": {
		Signature:   "glua.yaml.encode(value [, options])",
		Returns:     "returns: YAML string.",
		Parameters:  []string{"value: serializable Lua value"},
		Description: "使用与 JSON 一致的 table 形状规则编码单个 YAML 文档。",
	},
	"glua.yaml.decode": {
		Signature:   "glua.yaml.decode(text [, options])",
		Returns:     "returns: decoded Lua value.",
		Parameters:  []string{"text: one YAML document"},
		Description: "解码单个 YAML 文档，仅接受字符串映射键，null 映射为 glua.null。",
	},
	"glua.xml.encode": {
		Signature:   "glua.xml.encode(value [, options])",
		Returns:     "returns: XML string.",
		Parameters:  []string{"value: serializable Lua value", "options (optional): root and pretty"},
		Description: "把对象键映射为元素、数组映射为 item、_attr 映射为属性、_text 映射为文本。",
	},
	"glua.xml.decode": {
		Signature:   "glua.xml.decode(text [, options])",
		Returns:     "returns: decoded Lua value.",
		Parameters:  []string{"text: one-root XML document", "options (optional): inferTypes"},
		Description: "解码 XML table 映射；inferTypes 可控制布尔和数字类型推断。",
	},
}

// builtinConstantNames 保存由 glua 运行时公开的命名空间和常量。
var builtinConstantNames = []string{
	"glua",
	"glua.null",
	"glua.json",
	"glua.json.null",
	"glua.yaml",
	"glua.yaml.null",
	"glua.xml",
	"glua.xml.null",
	"glua.event",
	"glua.event.events",
	"glua.event.events.progress_line",
	"glua.event.events.progress_start",
	"glua.event.events.progress_end",
	"glua.event.events.progress_error",
	"glua.event.events.progress_exit",
	"glua.event.events.progress_function_call",
	"glua.event.events.progress_function_return",
	"glua.event.events.progress_function_error",
	"glua.event.events.progress_function_exit",
	"events.function_call",
	"events.function_return",
	"events.function_error",
	"events.function_exit",
	"events.progress_line",
	"events.progress_start",
	"events.progress_end",
	"events.progress_error",
	"events.progress_exit",
}

// appendBuiltinConstant 将 catalog 常量加入补全目录且保持名称唯一。
func appendBuiltinConstant(name string) {
	// 内置列表较小，线性查找可避免引入第二份可变索引。
	for _, current := range builtinConstantNames {
		if current == name {
			return
		}
	}
	builtinConstantNames = append(builtinConstantNames, name)
}

// builtinCompletionItems 根据限定前缀生成内置候选。
func builtinCompletionItems(prefix string) []completionItem {
	// 函数和常量共同从 Go 侧目录产生，保证两个编辑器看到相同类型。
	items := make([]completionItem, 0, len(builtinFunctionDocs)+len(builtinConstantNames))
	for name, info := range builtinFunctionDocs {
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			// 非匹配函数不加入当前补全结果。
			continue
		}
		documentation := markupContent{Kind: "markdown", Value: formatBuiltinHover(name, info)}
		label := strings.TrimPrefix(name, namespacePrefix(prefix))
		items = append(items, completionItem{Label: label, Kind: 3, Detail: info.Signature, Documentation: &documentation, InsertText: completionSnippet(label, info.Signature), InsertTextFormat: 2})
	}
	for _, name := range builtinConstantNames {
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			// 非匹配常量不加入当前补全结果。
			continue
		}
		items = append(items, completionItem{Label: strings.TrimPrefix(name, namespacePrefix(prefix)), Kind: 21, Detail: name + " const"})
	}
	return items
}

// completionSnippet 将函数签名转换为编辑器可定位参数的 LSP snippet。
func completionSnippet(name string, signature string) string {
	// 参数名来自 catalog 签名，接受候选后首个参数被选中；无参数函数光标停在右括号后。
	opening := strings.Index(signature, "(")
	closing := strings.LastIndex(signature, ")")
	if opening < 0 || closing < opening {
		return name
	}
	parameters := make([]string, 0, 4)
	for _, raw := range strings.Split(signature[opening+1:closing], ",") {
		parameter := strings.TrimSpace(strings.NewReplacer("[", "", "]", "").Replace(raw))
		if assignment := strings.Index(parameter, "="); assignment >= 0 {
			parameter = strings.TrimSpace(parameter[:assignment])
		}
		if parameter != "" {
			parameters = append(parameters, parameter)
		}
	}
	if len(parameters) == 0 {
		return name + "()"
	}
	placeholders := make([]string, 0, len(parameters))
	for index, parameter := range parameters {
		escaped := strings.NewReplacer("\\", "\\\\", "$", "\\$", "}", "\\}").Replace(parameter)
		placeholders = append(placeholders, fmt.Sprintf("${%d:%s}", index+1, escaped))
	}
	return name + "(" + strings.Join(placeholders, ", ") + ")"
}

// documentCompletionItems 返回光标前可见的简单文件符号候选。
func documentCompletionItems(text string, position lspPosition, prefix string) []completionItem {
	// 从 token 定义点提取名称，重点保留 const 类型；复杂作用域将在后续模块分析迁移中补齐。
	tokens := scanTokens(text)
	memberPrefix := strings.TrimPrefix(prefix, namespacePrefix(prefix))
	itemsByName := make(map[string]completionItem)
	for index, token := range tokens {
		if !rangeBeforeOrEqual(token.Range, position) {
			// 光标后的声明当前不可见。
			continue
		}
		if token.Kind != lexer.TokenIdentifier || index == 0 {
			// 非标识符或无前置 token 时不可能是当前支持的定义点。
			continue
		}
		if memberPrefix != "" && !strings.HasPrefix(token.Text, memberPrefix) {
			// 已输入前缀不匹配的符号不加入结果。
			continue
		}
		previous := tokens[index-1].Text
		switch previous {
		case "const":
			itemsByName[token.Text] = completionItem{Label: token.Text, Kind: 21, Detail: token.Text + " const"}
		case "local":
			itemsByName[token.Text] = completionItem{Label: token.Text, Kind: 6, Detail: token.Text + " local"}
		case "function":
			itemsByName[token.Text] = completionItem{Label: token.Text, Kind: 3, Detail: token.Text + " function"}
		default:
			// 其他表达式 token 不是简单声明点。
		}
	}
	items := make([]completionItem, 0, len(itemsByName))
	for _, item := range itemsByName {
		items = append(items, item)
	}
	return items
}

// workspaceRootPaths 将 initialize 参数转换为去重后的本地工作区目录。
func workspaceRootPaths(params initializeParams) []string {
	// workspaceFolders 优先支持多根目录；rootUri 作为旧客户端兼容回退。
	roots := make([]string, 0, len(params.WorkspaceFolders)+1)
	seen := make(map[string]struct{})
	appendRoot := func(uri string) {
		path := filePathFromURI(uri)
		if path == "" {
			// 非本地 URI 不能用于文件模块解析。
			return
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			// 重复根目录只保留一次。
			return
		}
		seen[path] = struct{}{}
		roots = append(roots, path)
	}
	for _, folder := range params.WorkspaceFolders {
		appendRoot(folder.URI)
	}
	appendRoot(params.RootURI)
	return roots
}

// filePathFromURI 将 file URI 转换为本地路径。
func filePathFromURI(uri string) string {
	// 仅支持 file URI，避免 language server 读取远程或虚拟协议路径。
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}
	path := filepath.FromSlash(parsed.Path)
	if path == "" {
		return ""
	}
	return path
}

// fileURIFromPath 将本地文件路径转换为 LSP file URI。
func fileURIFromPath(path string) string {
	// 使用 url.URL 处理空格等转义字符，保证客户端能重新打开目标文件。
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// modulePathCandidates 返回 require 名称的本地 Lua 候选路径。
func (server *Server) modulePathCandidates(moduleName string, documentURI string) []string {
	// 依次尝试当前文件目录、工作区根目录及其 lua/src 子目录。
	relative := filepath.FromSlash(strings.ReplaceAll(strings.TrimSpace(moduleName), ".", "/"))
	if relative == "" {
		return nil
	}
	roots := make([]string, 0, len(server.workspaceRoots)+1)
	if documentPath := filePathFromURI(documentURI); documentPath != "" {
		roots = append(roots, filepath.Dir(documentPath))
	}
	roots = append(roots, server.workspaceRoots...)
	candidates := make([]string, 0, len(roots)*12)
	seen := make(map[string]struct{})
	appendCandidates := func(root string, prefixes []string) {
		for _, prefix := range prefixes {
			base := root
			if prefix != "" {
				base = filepath.Join(base, prefix)
			}
			for _, candidate := range []string{
				filepath.Join(base, relative+".glua"),
				filepath.Join(base, relative+".lua"),
				filepath.Join(base, relative, "init.glua"),
				filepath.Join(base, relative, "init.lua"),
			} {
				candidate = filepath.Clean(candidate)
				if _, ok := seen[candidate]; ok {
					// 重复候选无需再次探测文件系统。
					continue
				}
				seen[candidate] = struct{}{}
				candidates = append(candidates, candidate)
			}
		}
	}
	if len(roots) > 0 {
		appendCandidates(roots[0], []string{""})
	}
	for index := 1; index < len(roots); index++ {
		appendCandidates(roots[index], []string{"", "lua", "src"})
	}
	return candidates
}

// resolveRequiredModuleFile 返回 require 名称对应的首个 Lua 源文件。
func (server *Server) resolveRequiredModuleFile(moduleName string, documentURI string) string {
	// 按 Lua 模块查找顺序检查普通文件和目录 init 文件。
	for _, candidate := range server.modulePathCandidates(moduleName, documentURI) {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

// requiredModuleLocation 返回光标位于 require 字符串时的模块定义位置。
func (server *Server) requiredModuleLocation(text string, documentURI string, position lspPosition) (location, bool) {
	// 识别 require "name" 和 require("name") 两种静态写法。
	tokens := scanTokens(text)
	for index, token := range tokens {
		if token.Kind != lexer.TokenString || !positionInRange(position, token.Range) {
			continue
		}
		moduleName, ok := requireModuleNameAt(tokens, index)
		if !ok {
			return location{}, false
		}
		path := server.resolveRequiredModuleFile(moduleName, documentURI)
		if path == "" {
			return location{}, false
		}
		return location{URI: fileURIFromPath(path), Range: lspRange{End: lspPosition{Character: 1}}}, true
	}
	return location{}, false
}

// requireModuleNameAt 返回 require 调用中字符串 token 对应的模块名。
func requireModuleNameAt(tokens []tokenInfo, stringIndex int) (string, bool) {
	// 向左跳过可选左括号，确认字符串前是 require 标识符。
	if stringIndex < 1 || tokens[stringIndex].Kind != lexer.TokenString {
		return "", false
	}
	requireIndex := stringIndex - 1
	if tokens[requireIndex].Text == "(" {
		requireIndex--
	}
	if requireIndex < 0 || tokens[requireIndex].Text != "require" {
		return "", false
	}
	moduleName := strings.Trim(tokens[stringIndex].Text, "\"'")
	return moduleName, moduleName != ""
}

// requireBindings 收集当前文档中的静态 require 返回值绑定。
func (server *Server) requireBindings(text string, documentURI string) map[string]string {
	// 支持 local name = require("module") 和 name = require "module"。
	tokens := scanTokens(text)
	bindings := make(map[string]string)
	for index, token := range tokens {
		nameIndex := index
		if token.Text == "local" {
			nameIndex++
		}
		if nameIndex+2 >= len(tokens) || tokens[nameIndex].Kind != lexer.TokenIdentifier || tokens[nameIndex+1].Text != "=" || tokens[nameIndex+2].Text != "require" {
			continue
		}
		stringIndex := nameIndex + 3
		if stringIndex < len(tokens) && tokens[stringIndex].Text == "(" {
			stringIndex++
		}
		if stringIndex >= len(tokens) {
			continue
		}
		moduleName, ok := requireModuleNameAt(tokens, stringIndex)
		if !ok {
			continue
		}
		if modulePath := server.resolveRequiredModuleFile(moduleName, documentURI); modulePath != "" {
			bindings[tokens[nameIndex].Text] = modulePath
		}
	}
	return bindings
}

// exportedMember 表示 require 返回表中可跳转或补全的成员。
type exportedMember struct {
	// Name 保存成员名。
	Name string
	// Range 保存成员定义范围。
	Range lspRange
	// Kind 保存 LSP CompletionItemKind。
	Kind int
	// Detail 保存成员类型说明。
	Detail string
	// ColonOnly 表示成员仅适用于冒号调用。
	ColonOnly bool
}

// moduleExportMembers 读取模块并提取 return 表的直接成员定义。
func moduleExportMembers(path string) []exportedMember {
	// 先读取模块源码，再用项目 lexer 提取 return 表名及其成员定义。
	text, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	tokens := scanTokens(string(text))
	exportedTables := make(map[string]struct{})
	for index, token := range tokens {
		if token.Text == "return" && index+1 < len(tokens) && tokens[index+1].Kind == lexer.TokenIdentifier {
			exportedTables[tokens[index+1].Text] = struct{}{}
		}
	}
	members := make([]exportedMember, 0, 8)
	seen := make(map[string]struct{})
	addMember := func(nameToken tokenInfo, kind int, detail string, colonOnly bool) {
		if _, ok := seen[nameToken.Text]; ok {
			return
		}
		seen[nameToken.Text] = struct{}{}
		members = append(members, exportedMember{Name: nameToken.Text, Range: nameToken.Range, Kind: kind, Detail: detail, ColonOnly: colonOnly})
	}
	for index := 0; index+2 < len(tokens); index++ {
		receiver := tokens[index]
		if _, ok := exportedTables[receiver.Text]; !ok {
			continue
		}
		separator := tokens[index+1].Text
		member := tokens[index+2]
		if member.Kind != lexer.TokenIdentifier || (separator != "." && separator != ":") {
			continue
		}
		if index > 0 && tokens[index-1].Text == "function" {
			addMember(member, 3, member.Text+" function", separator == ":")
			continue
		}
		if index+4 < len(tokens) && tokens[index+3].Text == "=" && tokens[index+4].Text == "function" {
			addMember(member, 3, member.Text+" function", false)
			continue
		}
		if index+3 < len(tokens) && tokens[index+3].Text == "=" && member.Text != "_glua_const" {
			addMember(member, 6, member.Text+" table field", false)
		}
	}
	return members
}

// requiredMemberLocation 返回 require 绑定成员的模块定义位置。
func (server *Server) requiredMemberLocation(text string, documentURI string, position lspPosition) (location, bool) {
	// 识别 receiver.member 和 receiver:member，并在模块导出表内查找同名成员。
	tokens := scanTokens(text)
	for index := 2; index < len(tokens); index++ {
		if !positionInRange(position, tokens[index].Range) || tokens[index].Kind != lexer.TokenIdentifier {
			continue
		}
		if tokens[index-1].Text != "." && tokens[index-1].Text != ":" {
			continue
		}
		modulePath := server.requireBindings(text, documentURI)[tokens[index-2].Text]
		if modulePath == "" {
			return location{}, false
		}
		for _, member := range moduleExportMembers(modulePath) {
			if member.Name == tokens[index].Text {
				return location{URI: fileURIFromPath(modulePath), Range: member.Range}, true
			}
		}
	}
	return location{}, false
}

// moduleCompletionItems 返回 require 返回表的成员补全候选。
func (server *Server) moduleCompletionItems(text string, documentURI string, position lspPosition, prefix string) []completionItem {
	// 补全仅在点号或冒号成员上下文启用，避免把模块成员污染全局候选。
	linePrefix := completionPrefixAtPosition(text, position)
	separatorIndex := strings.LastIndex(linePrefix, ".")
	separator := "."
	if separatorIndex < 0 {
		separatorIndex = strings.LastIndex(linePrefix, ":")
		separator = ":"
	}
	if separatorIndex < 0 {
		return nil
	}
	receiver := linePrefix[:separatorIndex]
	memberPrefix := linePrefix[separatorIndex+1:]
	modulePath := server.requireBindings(text, documentURI)[receiver]
	if modulePath == "" {
		return nil
	}
	items := make([]completionItem, 0, 8)
	for _, member := range moduleExportMembers(modulePath) {
		if !strings.HasPrefix(member.Name, memberPrefix) || (separator == "." && member.ColonOnly) || (separator == ":" && !member.ColonOnly) {
			continue
		}
		items = append(items, completionItem{Label: member.Name, Kind: member.Kind, Detail: member.Detail})
	}
	return items
}

// namespacePrefix 返回点号补全时应从候选标签移除的命名空间部分。
func namespacePrefix(prefix string) string {
	// 最后一个点号之前属于已输入命名空间，之后属于成员过滤文本。
	index := strings.LastIndex(prefix, ".")
	if index < 0 {
		// 顶层补全不移除任何前缀。
		return ""
	}
	return prefix[:index+1]
}

func builtinFunctionInfo(name string) (builtinFunctionDoc, bool) {
	// 查找 base 标准库方法签名与说明，供 hover/definition 使用。
	info, ok := builtinFunctionDocs[name]
	if ok {
		// 已命中函数文档时直接返回。
		return info, true
	}
	for _, constantName := range builtinConstantNames {
		if constantName == name {
			// 常量使用无参数签名，避免伪定义被错误渲染成函数。
			description := "GLua predefined event constant."
			returns := "constant value"
			if activeBuiltinCatalogLocale == "zh-CN" {
				// 中文目录使用中文常量兜底说明。
				description = "GLua 预定义事件常量。"
				returns = "常量值"
			}
			return builtinFunctionDoc{Kind: "constant", Signature: name, Description: description, Returns: returns, Locale: activeBuiltinCatalogLocale}, true
		}
	}
	return info, ok
}

func builtinFunctionLocation(name string) (string, lspRange, bool) {
	// 用自定义 URI 承载内置函数跳转目标，避免污染真实用户工程路径。
	info, ok := builtinFunctionInfo(name)
	if !ok {
		return "", lspRange{}, false
	}
	uriPrefix := "glua-builtin:///"
	if info.Kind == "constant" {
		// 常量 URI 明确携带类型，客户端生成虚拟文档时不会再创建 function stub。
		uriPrefix += "constant/"
	}
	return uriPrefix + name + ".lua", lspRange{
		Start: lspPosition{},
		End:   lspPosition{},
	}, true
}

func formatBuiltinHover(name string, info builtinFunctionDoc) string {
	// 拼接 hover 文档，包含签名、参数、返回值与功能说明。
	labels := map[string]string{"description": "Description", "parameters": "Parameters", "returns": "Returns", "example": "Example"}
	if normalizeBuiltinCatalogLocale(info.Locale) == "zh-CN" {
		// 中文文档同步本地化 Markdown 区块标题。
		labels = map[string]string{"description": "说明", "parameters": "参数", "returns": "返回值", "example": "示例"}
	}
	lines := make([]string, 0, 24)
	lines = append(lines, "```lua", info.Signature, "```", "", "**"+labels["description"]+"**", info.Description)
	if info.Kind == "constant" {
		// 常量没有参数列表，展示类型和值语义即可。
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "", "**"+labels["parameters"]+"**")
	for _, parameter := range info.Parameters {
		lines = append(lines, "- `"+parameter+"`")
	}
	lines = append(lines, "", "**"+labels["returns"]+"**", "- "+info.Returns)
	if strings.TrimSpace(info.Example) != "" {
		// 示例使用 Lua 代码块，Hover 中可直接复制运行。
		lines = append(lines, "", "**"+labels["example"]+"**", "```lua", info.Example, "```")
	}
	return strings.Join(lines, "\n")
}

// isBuiltinFunction 判断标识符是否是常见 Lua 内置函数。
func isBuiltinFunction(text string) bool {
	// 内置函数标成 function，提升无外部语法插件时的基础高亮体验。
	_, ok := builtinFunctionInfo(text)
	return ok
}

// isIdentifierRune 判断 rune 是否可作为标识符字符。
func isIdentifierRune(value rune) bool {
	// 保留给后续补充手写位置映射使用；当前 token 路径仍用 lexer 为准。
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

// readMessage 读取一个 LSP Content-Length 帧。
func readMessage(reader *bufio.Reader) ([]byte, error) {
	// 先读取 header，直到空行，再按 Content-Length 读取 body。
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid LSP header: %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			parsedLength, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			contentLength = parsedLength
		}
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

// writeResponse 写出 JSON-RPC 成功响应。
func (server *Server) writeResponse(id json.RawMessage, result any) error {
	// JSON-RPC 响应必须复用请求 ID。
	if len(id) == 0 {
		return nil
	}
	return server.writeJSON(responseMessage{JSONRPC: "2.0", ID: id, Result: result})
}

// writeError 写出 JSON-RPC 错误响应。
func (server *Server) writeError(id json.RawMessage, code int, message string) error {
	// 通知没有 ID，无法响应错误。
	if len(id) == 0 {
		return nil
	}
	return server.writeJSON(errorResponseMessage{JSONRPC: "2.0", ID: id, Error: &responseError{Code: code, Message: message}})
}

// writeNotification 写出 JSON-RPC 通知。
func (server *Server) writeNotification(method string, params any) error {
	// 通知不携带 ID。
	return server.writeJSON(notificationMessage{JSONRPC: "2.0", Method: method, Params: params})
}

// writeJSON 以 LSP Content-Length 帧写出 JSON 消息。
func (server *Server) writeJSON(value any) error {
	// 输出必须串行化，避免并发诊断和响应交错。
	server.writeMu.Lock()
	defer server.writeMu.Unlock()
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var frame bytes.Buffer
	_, _ = fmt.Fprintf(&frame, "Content-Length: %d\r\n\r\n", len(payload))
	frame.Write(payload)
	_, err = server.output.Write(frame.Bytes())
	return err
}
