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
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/zing/go-lua-vm/compiler/formatter"
	"github.com/zing/go-lua-vm/compiler/lexer"
	"github.com/zing/go-lua-vm/compiler/parser"
	"github.com/zing/go-lua-vm/extensions"
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
	Result any `json:"result,omitempty"`
	// Error 保存失败响应。
	Error *responseError `json:"error,omitempty"`
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
	case "textDocument/formatting":
		return server.handleFormatting(message)
	case "textDocument/definition":
		return server.handleDefinition(message)
	case "textDocument/hover":
		return server.handleHover(message)
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
}

// gluaInitializationOptions 表示 glua LSP 支持的初始化选项。
type gluaInitializationOptions struct {
	// Syntax 保存语法模式，例如 lua53、extended、continue,switch。
	Syntax string `json:"syntax"`
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
	}
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
	targetName := identifierAtPosition(text, params.Position)
	if targetName == "" {
		return server.writeResponse(message.ID, nil)
	}
	definition, ok := findDefinition(text, targetName, params.Position)
	if !ok {
		if builtinURI, builtinRange, ok := builtinFunctionLocation(targetName); ok {
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
	targetName := identifierAtPosition(text, params.Position)
	if targetName == "" {
		return server.writeResponse(message.ID, nil)
	}
	definition, ok := findDefinition(text, targetName, params.Position)
	if !ok {
		if info, builtinOk := builtinFunctionInfo(targetName); builtinOk {
			return server.writeResponse(message.ID, hoverResult{
				Contents: markupContent{Kind: "markdown", Value: formatBuiltinHover(targetName, info)},
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
	return server.writeResponse(message.ID, semanticTokensResult{Data: semanticTokens(text)})
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
	if _, err := parser.NewWithSyntax(text, syntax).ParseChunk(); err != nil {
		parseErrors := collectParseErrors(err)
		diagnostics := make([]diagnostic, 0, len(parseErrors))
		for _, parseError := range parseErrors {
			diagnostics = append(diagnostics, diagnosticFromParseError(parseError))
		}
		return diagnostics
	}
	return nil
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
func semanticTokens(text string) []int {
	// 逐 token 分类并按 LSP semantic token 5 元组 delta 编码输出。
	tokens := scanTokens(text)
	data := make([]int, 0, len(tokens)*5)
	lastLine := 0
	lastStart := 0
	for _, token := range tokens {
		tokenType, ok := semanticTypeForToken(token)
		if !ok {
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
func semanticTypeForToken(token tokenInfo) (int, bool) {
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
		if isContextKeyword(token.Text) {
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
			break
		}
		if token.Kind == lexer.TokenIllegal {
			continue
		}
		start := lspPosition{Line: token.Position.Line - 1, Character: token.Position.Column - 1}
		if start.Line < 0 {
			start.Line = 0
		}
		if start.Character < 0 {
			start.Character = 0
		}
		length := utf8.RuneCountInString(token.Text)
		if length <= 0 {
			length = 1
		}
		tokens = append(tokens, tokenInfo{
			Text: token.Text,
			Kind: token.Kind,
			Range: lspRange{
				Start: start,
				End:   lspPosition{Line: start.Line, Character: start.Character + length},
			},
		})
	}
	return tokens
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
func isContextKeyword(text string) bool {
	// switch/case/default/continue 在 lexer 中可能仍是 identifier，需要语义高亮为关键字。
	switch text {
	case "switch", "case", "default", "continue":
		return true
	default:
		return false
	}
}

type builtinFunctionDoc struct {
	// Signature 保存函数签名，参数部分保持与官方文档一致。
	Signature string
	// Returns 保存函数返回说明。
	Returns string
	// Parameters 保存参数说明列表。
	Parameters []string
	// Description 保存函数功能说明。
	Description string
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
}

func builtinFunctionInfo(name string) (builtinFunctionDoc, bool) {
	// 查找 base 标准库方法签名与说明，供 hover/definition 使用。
	info, ok := builtinFunctionDocs[name]
	return info, ok
}

func builtinFunctionLocation(name string) (string, lspRange, bool) {
	// 用自定义 URI 承载内置函数跳转目标，避免污染真实用户工程路径。
	if _, ok := builtinFunctionDocs[name]; !ok {
		return "", lspRange{}, false
	}
	return "glua-builtin:///" + name + ".lua", lspRange{
		Start: lspPosition{},
		End:   lspPosition{},
	}, true
}

func formatBuiltinHover(name string, info builtinFunctionDoc) string {
	// 拼接 hover 文档，包含签名、参数、返回值与功能说明。
	lines := make([]string, 0, 18)
	lines = append(lines, "```lua", info.Signature, "```", "", "**Description**", info.Description)
	lines = append(lines, "", "**Parameters**")
	for _, parameter := range info.Parameters {
		lines = append(lines, "- `"+parameter+"`")
	}
	lines = append(lines, "", "**Returns**", "- "+info.Returns)
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
	return server.writeJSON(responseMessage{JSONRPC: "2.0", ID: id, Error: &responseError{Code: code, Message: message}})
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
