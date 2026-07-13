package lsp

import (
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/ZingYao/go-lua-vm/compiler/lexer"
	"github.com/ZingYao/go-lua-vm/extensions"
)

// BrowserDiagnostic 表示浏览器编辑器可直接消费的一条 GLua 诊断。
type BrowserDiagnostic struct {
	// Range 保存零基 UTF-16 行列范围。
	Range lspRange `json:"range"`
	// Severity 保存 LSP 兼容诊断等级。
	Severity int `json:"severity"`
	// Source 保存诊断来源。
	Source string `json:"source"`
	// Message 保存用户可见错误信息。
	Message string `json:"message"`
}

// BrowserCompletion 表示浏览器编辑器补齐候选。
type BrowserCompletion struct {
	// Label 保存候选展示和默认插入名称。
	Label string `json:"label"`
	// Kind 保存 LSP CompletionItemKind 兼容类型。
	Kind int `json:"kind"`
	// Detail 保存签名或符号类型摘要。
	Detail string `json:"detail,omitempty"`
	// Documentation 保存 Markdown 文档。
	Documentation string `json:"documentation,omitempty"`
	// InsertText 保存实际插入文本；函数候选使用 Monaco 兼容 snippet 占位符。
	InsertText string `json:"insertText,omitempty"`
	// InsertTextFormat 保存 LSP 插入格式，1 表示纯文本，2 表示 snippet。
	InsertTextFormat int `json:"insertTextFormat,omitempty"`
	// Range 保存应被候选替换的当前前缀范围。
	Range lspRange `json:"range"`
	// SortText 保存编辑器候选的稳定排序键。
	SortText string `json:"sortText,omitempty"`
}

// BrowserHover 表示浏览器编辑器 hover 内容。
type BrowserHover struct {
	// Markdown 保存 hover Markdown 正文。
	Markdown string `json:"markdown"`
}

// BrowserDefinition 表示浏览器编辑器当前文件内的定义位置。
type BrowserDefinition struct {
	// Path 保存跨文件定义所在的工作区相对路径；空字符串表示当前文件。
	Path string `json:"path,omitempty"`
	// Range 保存定义标识符范围。
	Range lspRange `json:"range"`
}

// BrowserDiagnostics 使用桌面 LSP 同一分析器生成浏览器诊断。
//
// source 必须是完整 GLua chunk；返回范围使用零基 UTF-16 行列，空切片表示源码通过语法与基础语义检查。
func BrowserDiagnostics(source string) []BrowserDiagnostic {
	// 复用默认扩展语法的 parser 与 codegen 诊断，避免 Web 与桌面规则漂移。
	diagnostics := analyzeDiagnostics(source, extensions.Default())
	result := make([]BrowserDiagnostic, 0, len(diagnostics))
	for _, item := range diagnostics {
		// 只转换协议外壳，保留现有诊断范围、等级和文本。
		result = append(result, BrowserDiagnostic{
			Range:    item.Range,
			Severity: item.Severity,
			Source:   item.Source,
			Message:  item.Message,
		})
	}
	return result
}

// BrowserSemanticTokens 使用桌面 LSP 同一规则生成 delta 编码语义 token。
//
// source 必须是完整文档；返回数据遵循 LSP SemanticTokens 的五整数分组，供 Monaco provider 原样消费。
func BrowserSemanticTokens(source string) []int {
	// 直接复用 LSP delta 编码，Monaco 支持相同 token 数据模型。
	return semanticTokens(source, extensions.Default())
}

// BrowserCompletions 返回指定光标位置的内置和当前文件符号补齐。
//
// line 与 character 均为零基 UTF-16 位置；越界位置由现有 LSP 前缀分析按行首或行尾夹紧。
func BrowserCompletions(source string, line int, character int) []BrowserCompletion {
	// 先按桌面 LSP 规则解析限定前缀与内置命名空间别名。
	position := lspPosition{Line: line, Character: character}
	prefix := completionPrefixAtPosition(source, position)
	builtinPrefix := resolveBuiltinQualifiedAlias(source, prefix, position)
	items := builtinCompletionItems(builtinPrefix)
	items = append(items, documentCompletionItems(source, position, prefix)...)
	result := make([]BrowserCompletion, 0, len(items))
	for _, item := range items {
		// 桌面 LSP 候选统一转换为带 snippet 与精确替换范围的浏览器候选。
		result = append(result, makeBrowserCompletion(item, position, prefix))
	}
	sort.SliceStable(result, func(left int, right int) bool {
		// 稳定按标签排序，确保浏览器和桌面端候选顺序可重复验证。
		return result[left].Label < result[right].Label
	})
	return result
}

// BrowserWorkspaceCompletions 返回指定光标位置的单文件与虚拟工作区模块补齐。
//
// documentPath 是当前工作区相对路径；files 的 key 必须使用正斜杠相对路径，value 是未保存内容优先的完整源码。
func BrowserWorkspaceCompletions(source string, documentPath string, files map[string]string, line int, character int) []BrowserCompletion {
	// 先保留内置与当前文件候选，再附加 require 返回表成员并去重。
	result := BrowserCompletions(source, line, character)
	position := lspPosition{Line: line, Character: character}
	linePrefix := completionPrefixAtPosition(source, position)
	separatorIndex := strings.LastIndex(linePrefix, ".")
	separator := "."
	if separatorIndex < 0 {
		// 点号不存在时检查 Lua 方法调用的冒号上下文。
		separatorIndex = strings.LastIndex(linePrefix, ":")
		separator = ":"
	}
	if separatorIndex < 0 {
		// 顶层候选没有模块接收者，直接返回单文件结果。
		return result
	}
	receiver := linePrefix[:separatorIndex]
	memberPrefix := linePrefix[separatorIndex+1:]
	modulePath := browserRequireBindings(source, documentPath, files)[receiver]
	if modulePath == "" {
		// 接收者没有静态 require 绑定时不猜测动态模块。
		return result
	}
	seen := make(map[string]struct{}, len(result))
	for _, item := range result {
		// 已有标签用于抑制工作区候选重复项。
		seen[item.Label] = struct{}{}
	}
	for _, member := range moduleExportMembersFromSource(files[modulePath]) {
		// 成员前缀和点号/冒号调用方式必须同时匹配。
		if !strings.HasPrefix(member.Name, memberPrefix) || (separator == "." && member.ColonOnly) || (separator == ":" && !member.ColonOnly) {
			continue
		}
		if _, exists := seen[member.Name]; exists {
			// 同名候选保留先到的当前文件或内置描述。
			continue
		}
		seen[member.Name] = struct{}{}
		item := completionItem{Label: member.Name, Kind: member.Kind, Detail: member.Detail}
		result = append(result, makeBrowserCompletion(item, position, linePrefix))
	}
	sort.SliceStable(result, func(left int, right int) bool {
		// 跨文件候选仍保持确定性的字典顺序。
		return result[left].Label < result[right].Label
	})
	return result
}

// makeBrowserCompletion 把共享 LSP 候选转换为 Monaco 可直接消费的浏览器候选。
//
// item 是桌面端候选；position 与 prefix 表示当前光标和限定前缀。返回结果保留文档、snippet 与精确替换范围。
func makeBrowserCompletion(item completionItem, position lspPosition, prefix string) BrowserCompletion {
	// MarkupContent 在浏览器协议中压平为 Markdown 字符串。
	documentation := ""
	if item.Documentation != nil {
		// 非空文档保留完整 Markdown，供 Monaco suggest 直接展示。
		documentation = item.Documentation.Value
	}
	memberPrefix := completionMemberPrefix(prefix)
	startCharacter := position.Character - len([]rune(memberPrefix))
	if startCharacter < 0 {
		// 非法或截断的客户端位置回退到行首，避免构造负范围。
		startCharacter = 0
	}
	insertText := item.Label
	insertTextFormat := 1
	if item.Kind == 3 && strings.Contains(item.Detail, "(") {
		// 函数与方法候选按扩展相同规则生成参数占位 snippet。
		insertText = browserCompletionSnippet(item.Label, item.Detail)
		insertTextFormat = 2
	}
	return BrowserCompletion{
		Label:            item.Label,
		Kind:             item.Kind,
		Detail:           item.Detail,
		Documentation:    documentation,
		InsertText:       insertText,
		InsertTextFormat: insertTextFormat,
		Range:            lspRange{Start: lspPosition{Line: position.Line, Character: startCharacter}, End: position},
		SortText:         browserCompletionSortText(item),
	}
}

// completionMemberPrefix 返回限定补全中真正需要替换的成员前缀。
//
// prefix 可以是顶层名称、点号成员或冒号方法；返回值不包含接收者与访问符。
func completionMemberPrefix(prefix string) string {
	// 点号与冒号都使用最后一个访问符之后的文本作为替换范围。
	dotIndex := strings.LastIndex(prefix, ".")
	colonIndex := strings.LastIndex(prefix, ":")
	separatorIndex := dotIndex
	if colonIndex > separatorIndex {
		// 冒号比点号更靠后时当前处于方法调用补全。
		separatorIndex = colonIndex
	}
	if separatorIndex < 0 {
		// 顶层补全直接替换完整前缀。
		return prefix
	}
	return prefix[separatorIndex+1:]
}

// browserCompletionSnippet 按扩展规则从函数签名生成 Monaco/LSP snippet。
//
// name 是待插入名称，signature 是展示签名；无法提取参数时仍插入一对调用括号。
func browserCompletionSnippet(name string, signature string) string {
	// 只读取最外层第一对签名括号，保持与现有扩展的轻量规则一致。
	openIndex := strings.Index(signature, "(")
	closeIndex := strings.LastIndex(signature, ")")
	if openIndex < 0 || closeIndex <= openIndex {
		// 损坏签名回退为普通名称，避免生成不完整 snippet。
		return browserSnippetEscape(name)
	}
	rawParameters := strings.Split(signature[openIndex+1:closeIndex], ",")
	parameters := make([]string, 0, len(rawParameters))
	for _, rawParameter := range rawParameters {
		// 可选参数方括号和默认值仅用于文档，不进入占位符文本。
		parameter := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(rawParameter, "[", ""), "]", ""))
		if equalsIndex := strings.Index(parameter, "="); equalsIndex >= 0 {
			// 默认值从等号开始移除，光标只选中参数名称。
			parameter = strings.TrimSpace(parameter[:equalsIndex])
		}
		if parameter == "" {
			// 空参数片段不会生成无意义占位符。
			continue
		}
		parameters = append(parameters, parameter)
	}
	if len(parameters) == 0 {
		// 无参函数仍补齐调用括号。
		return browserSnippetEscape(name) + "()"
	}
	placeholders := make([]string, 0, len(parameters))
	for index, parameter := range parameters {
		// 占位序号从 1 开始，符合 LSP snippet 规范。
		placeholders = append(placeholders, "${"+strconv.Itoa(index+1)+":"+browserSnippetEscape(parameter)+"}")
	}
	return browserSnippetEscape(name) + "(" + strings.Join(placeholders, ", ") + ")"
}

// browserSnippetEscape 转义 LSP snippet 占位文本中的控制字符。
//
// value 是候选名称或参数文本；返回值可以安全嵌入 ${n:value}。
func browserSnippetEscape(value string) string {
	// 反斜线必须最先转义，随后保护美元符号和右花括号。
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "$", "\\$")
	return strings.ReplaceAll(value, "}", "\\}")
}

// browserCompletionSortText 返回 Monaco 使用的稳定候选排序键。
//
// item 是共享补全候选；局部变量优先于函数，常量排在其后，同类按标签排序。
func browserCompletionSortText(item completionItem) string {
	// LSP kind 6 是变量、3 是函数、21 是常量。
	prefix := "3"
	switch item.Kind {
	case 6:
		// 当前文件变量优先展示。
		prefix = "0"
	case 3:
		// 函数和方法紧随变量。
		prefix = "1"
	case 21:
		// 常量排在函数之后。
		prefix = "2"
	default:
		// 未识别类型保持稳定的末尾分组。
	}
	return prefix + item.Label
}

// BrowserHoverAt 返回指定光标位置的内置文档或当前文件定义摘要。
//
// line 与 character 均为零基 UTF-16 位置；无可展示符号时返回 nil。
func BrowserHoverAt(source string, line int, character int) *BrowserHover {
	// 先尝试内置函数与常量文档，再回退到当前文件定义位置。
	position := lspPosition{Line: line, Character: character}
	targetName := qualifiedIdentifierAtPosition(source, position)
	if targetName == "" {
		// 光标不覆盖名称时没有 hover 内容。
		return nil
	}
	builtinTargetName := resolveBuiltinQualifiedAlias(source, targetName, position)
	if info, ok := builtinFunctionInfo(builtinTargetName); ok {
		// 内置目录提供完整签名、参数、返回值和示例 Markdown。
		return &BrowserHover{Markdown: formatBuiltinHover(builtinTargetName, info)}
	}
	if definition, ok := findDefinition(source, targetName, position); ok {
		// 当前文件符号至少展示名称与定义行，保持轻量回退可用。
		return &BrowserHover{Markdown: "`" + targetName + "`\n\n定义于当前文件第 " + itoa(definition.Start.Line+1) + " 行。"}
	}
	return &BrowserHover{Markdown: "`" + targetName + "`"}
}

// BrowserDefinitionAt 返回指定光标位置在当前文件中的定义范围。
//
// line 与 character 均为零基 UTF-16 位置；内置伪文件和跨文件模块定义由后续工作区接口处理。
func BrowserDefinitionAt(source string, line int, character int) *BrowserDefinition {
	// 使用桌面 LSP 的限定名称与最近可见定义规则定位符号。
	position := lspPosition{Line: line, Character: character}
	targetName := qualifiedIdentifierAtPosition(source, position)
	if targetName == "" {
		// 光标不覆盖名称时没有定义位置。
		return nil
	}
	definition, ok := findDefinition(source, targetName, position)
	if !ok {
		// 当前文件不存在定义时返回 nil，避免 Monaco 跳到错误位置。
		return nil
	}
	return &BrowserDefinition{Range: definition}
}

// BrowserWorkspaceDefinitionAt 返回当前文件或虚拟工作区模块中的定义位置。
//
// documentPath 和 files 使用工作区相对路径；静态 require 字符串与 require 返回表成员均支持跨文件跳转。
func BrowserWorkspaceDefinitionAt(source string, documentPath string, files map[string]string, line int, character int) *BrowserDefinition {
	// 跨文件 require 目标优先于当前文件普通符号，保持与桌面 LSP 的跳转顺序一致。
	position := lspPosition{Line: line, Character: character}
	tokens := scanTokens(source)
	for index, token := range tokens {
		// 仅处理光标覆盖的字符串 token。
		if token.Kind != lexer.TokenString || !positionInRange(position, token.Range) {
			continue
		}
		moduleName, ok := requireModuleNameAt(tokens, index)
		if !ok {
			// 普通字符串不是模块跳转目标。
			continue
		}
		modulePath := resolveBrowserModule(moduleName, documentPath, files)
		if modulePath == "" {
			// 工作区不存在静态模块时不给出错误位置。
			return nil
		}
		return &BrowserDefinition{Path: modulePath, Range: lspRange{End: lspPosition{Character: 1}}}
	}
	bindings := browserRequireBindings(source, documentPath, files)
	for index := 2; index < len(tokens); index++ {
		// 识别 receiver.member 与 receiver:member 的成员 token。
		if tokens[index].Kind != lexer.TokenIdentifier || !positionInRange(position, tokens[index].Range) {
			continue
		}
		if tokens[index-1].Text != "." && tokens[index-1].Text != ":" {
			// 非成员访问继续扫描可能覆盖光标的其他 token。
			continue
		}
		modulePath := bindings[tokens[index-2].Text]
		if modulePath == "" {
			// 接收者不是静态 require 绑定，回退当前文件查找。
			break
		}
		for _, member := range moduleExportMembersFromSource(files[modulePath]) {
			// 成员名称相同即返回模块源码中的精确范围。
			if member.Name == tokens[index].Text {
				return &BrowserDefinition{Path: modulePath, Range: member.Range}
			}
		}
		break
	}
	return BrowserDefinitionAt(source, line, character)
}

// browserRequireBindings 收集虚拟工作区中的静态 require 绑定。
//
// source 是当前文档源码；无法解析或无法在 files 中定位的动态 require 会被忽略。
func browserRequireBindings(source string, documentPath string, files map[string]string) map[string]string {
	// 复用桌面端 token 形态，支持 local name = require("module") 与无括号调用。
	tokens := scanTokens(source)
	bindings := make(map[string]string)
	for index, token := range tokens {
		nameIndex := index
		if token.Text == "local" {
			// local 后一个 token 才是绑定名称。
			nameIndex++
		}
		if nameIndex+2 >= len(tokens) || tokens[nameIndex].Kind != lexer.TokenIdentifier || tokens[nameIndex+1].Text != "=" || tokens[nameIndex+2].Text != "require" {
			continue
		}
		stringIndex := nameIndex + 3
		if stringIndex < len(tokens) && tokens[stringIndex].Text == "(" {
			// 有括号调用时跳过左括号定位字符串。
			stringIndex++
		}
		if stringIndex >= len(tokens) {
			// 不完整调用没有模块名可解析。
			continue
		}
		moduleName, ok := requireModuleNameAt(tokens, stringIndex)
		if !ok {
			// 动态 require 或非字符串参数不进入静态绑定。
			continue
		}
		modulePath := resolveBrowserModule(moduleName, documentPath, files)
		if modulePath != "" {
			// 只有工作区确实存在的模块才建立绑定。
			bindings[tokens[nameIndex].Text] = modulePath
		}
	}
	return bindings
}

// resolveBrowserModule 按 Lua 模块名在虚拟工作区寻找源码文件。
//
// moduleName 支持点号或斜杠分隔；返回规范化相对路径，非法穿越路径或未找到时返回空字符串。
func resolveBrowserModule(moduleName string, documentPath string, files map[string]string) string {
	// 点号转换为目录分隔符，并拒绝逃逸工作区的名称。
	relative := path.Clean(strings.ReplaceAll(moduleName, ".", "/"))
	if relative == "." || relative == ".." || strings.HasPrefix(relative, "../") || path.IsAbs(relative) {
		// 非法模块名不能访问工作区边界之外。
		return ""
	}
	documentPath = strings.TrimPrefix(path.Clean(strings.TrimPrefix(documentPath, "@/")), "/")
	roots := []string{path.Dir(documentPath), "", "lua", "src"}
	seen := make(map[string]struct{})
	for _, root := range roots {
		for _, suffix := range []string{relative + ".glua", relative + ".lua", path.Join(relative, "init.glua"), path.Join(relative, "init.lua")} {
			candidate := strings.TrimPrefix(path.Clean(path.Join(root, suffix)), "./")
			if _, exists := seen[candidate]; exists {
				// 相同根目录候选只检查一次。
				continue
			}
			seen[candidate] = struct{}{}
			if _, exists := files[candidate]; exists {
				// 按当前文档目录、工作区根、lua、src 的顺序返回首个模块。
				return candidate
			}
		}
	}
	return ""
}

// itoa 把正整数转换为十进制文本，用于浏览器 hover 行号。
//
// value 可以是任意整数；返回值不包含额外格式信息。
func itoa(value int) string {
	// 使用小范围无分配特化没有收益，统一复用标准十进制转换。
	return strconv.Itoa(value)
}
