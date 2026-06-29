package parser

import (
	"fmt"
	"strings"

	"github.com/zing/go-lua-vm/compiler/lexer"
)

const (
	// maxFunctionLocals 对齐 Lua 5.3 单函数局部变量数量限制。
	maxFunctionLocals = 200
)

// ParseError 表示 parser 或语义校验阶段发现的单个错误。
//
// Position 保存错误位置；Message 保存可读错误信息；Near 保存 Lua load 错误中的 token 上下文。
type ParseError struct {
	// Position 保存错误所在源码位置。
	Position lexer.Position
	// Message 保存错误说明。
	Message string
	// Near 保存 Lua 5.3 `load` 错误中 near 后的 token 文本；为空表示该错误不携带 token 上下文。
	Near string
}

// SourceID 返回 Go API 和 CLI 错误文本中的 source 标识。
//
// sourceName 遵循 Lua chunk name 规则：`@path` 展示为 path，`=name` 展示为 name，
// 其他源码片段展示为 `[string "..."]` 摘要。文件路径不截断，方便命令行直接定位。
func SourceID(sourceName string) string {
	if strings.HasPrefix(sourceName, "@") || strings.HasPrefix(sourceName, "=") {
		// Go API 和 CLI 需要完整文件名或显式 chunk 名，避免长路径被摘要后无法打开。
		return sourceName[1:]
	}
	// 字符串 chunk 没有稳定路径，复用 Lua 摘要规则。
	return loadSourceID(sourceName)
}

// Error 返回单个 parser 错误文本。
//
// 格式保持行列前缀，便于 CLI 和测试稳定展示。
func (parseError ParseError) Error() string {
	// 错误文本使用稳定格式，避免调用方重复拼接位置。
	return fmt.Sprintf("parse error at %d:%d: %s", parseError.Position.Line, parseError.Position.Column, parseError.Message)
}

// SyntaxErrorMessage 返回 Go API 和 CLI 可直接展示的紧凑语法错误文本。
//
// sourceName 是 Lua chunk name；parseError 必须来自当前源码解析。返回值使用
// `source:line:column: syntax error near token` 形态，便于命令行和宿主日志直接定位。
func SyntaxErrorMessage(sourceName string, parseError ParseError) string {
	// 主错误优先展示 near token，具体 expected/hint 留给结构化详情。
	if parseError.Near != "" {
		return fmt.Sprintf("%s:%d:%d: syntax error near %s", SourceID(sourceName), parseError.Position.Line, parseError.Position.Column, parseError.Near)
	}
	if parseError.Message != "" {
		return fmt.Sprintf("%s:%d:%d: syntax error: %s", SourceID(sourceName), parseError.Position.Line, parseError.Position.Column, parseError.Message)
	}
	return fmt.Sprintf("%s:%d:%d: syntax error", SourceID(sourceName), parseError.Position.Line, parseError.Position.Column)
}

// ParseErrorList 表示 parser 语义阶段聚合到的一组错误。
//
// 语法错误仍按第一处失败返回；作用域和 goto/label 校验会尽量聚合多个错误。
type ParseErrorList []ParseError

// Error 返回聚合错误文本。
//
// 多个错误用换行拼接，调用方可通过 errors.As 转回 ParseErrorList 获取结构化位置。
func (parseErrors ParseErrorList) Error() string {
	// 空错误列表通常不会被返回，这里保留防御性文本。
	if len(parseErrors) == 0 {
		// 没有实际错误时返回空字符串，避免误导调用方。
		return ""
	}
	lines := make([]string, 0, len(parseErrors))
	for _, parseError := range parseErrors {
		// 每个错误复用单错误格式，保持输出稳定。
		lines = append(lines, parseError.Error())
	}

	// 使用换行聚合，便于 golden 文件逐行比较。
	return strings.Join(lines, "\n")
}

// semanticAnalyzer 负责给 AST 标注作用域并执行基础语义校验。
//
// 该结构只在单次 ParseChunk 内使用，不暴露给包外调用方。
type semanticAnalyzer struct {
	// nextScopeID 保存下一个可分配的作用域编号。
	nextScopeID int
	// loopDepth 保存当前函数内嵌套循环深度，用于校验 continue 只能出现在循环内部。
	loopDepth int
	// errors 保存语义阶段聚合到的错误。
	errors ParseErrorList
}

// functionNamespace 保存单个 Lua 函数内的 label/goto 命名空间。
//
// Lua goto 不能跨函数跳转，因此每个函数体使用独立 namespace 校验。
type functionNamespace struct {
	// labels 按名称保存当前函数内已经声明的 label 列表。
	labels map[string][]labelRecord
	// gotos 保存当前函数内出现的 goto。
	gotos []gotoRecord
	// scopes 按 ID 保存当前函数内的 block 作用域，用于判断 label 可见性。
	scopes map[int]*ScopeInfo
}

// labelRecord 保存 label 及其所在 block。
//
// block 用于判断 goto 是否跳入更深层作用域。
type labelRecord struct {
	// block 保存 label 所在 block。
	block *Block
	// scope 保存 label 所在作用域。
	scope *ScopeInfo
	// label 保存 label 元信息。
	label LabelInfo
}

// gotoRecord 保存 goto 及其所在 block。
//
// block 用于后续与目标 label 计算作用域穿越关系。
type gotoRecord struct {
	// block 保存 goto 所在 block。
	block *Block
	// scope 保存 goto 所在作用域。
	scope *ScopeInfo
	// gotoInfo 保存 goto 元信息。
	gotoInfo GotoInfo
}

// localDeclaration 表示进入 block 前需要预声明的局部变量。
//
// 函数参数和 for 循环变量通过该结构注入对应 body 作用域。
type localDeclaration struct {
	// name 保存局部变量名。
	name string
	// position 保存局部变量声明位置。
	position lexer.Position
}

// analyzeChunk 标注 chunk 作用域并执行基础语义校验。
//
// chunk 必须已经通过语法解析；返回错误表示作用域、局部变量或 goto/label 规则失败。
func (analyzer *semanticAnalyzer) analyzeChunk(chunk *Chunk) error {
	// 顶层 chunk 使用一个独立函数命名空间，后续函数体会递归创建新命名空间。
	namespace := newFunctionNamespace()
	analyzer.analyzeBlock(chunk.Block, nil, -1, 0, nil, false, namespace)
	analyzer.validateGotos(namespace)
	if len(analyzer.errors) > 0 {
		// 语义错误可能有多个，一次性返回给调用方。
		return analyzer.errors
	}

	// 没有语义错误时返回 nil。
	return nil
}

// newFunctionNamespace 创建新的函数级 label/goto 命名空间。
//
// Lua label 和 goto 只在单个函数内互相可见。
func newFunctionNamespace() *functionNamespace {
	// 初始化 label 与 scope 索引，避免声明和校验时反复判空。
	return &functionNamespace{labels: make(map[string][]labelRecord), scopes: make(map[int]*ScopeInfo)}
}

// analyzeBlock 标注 block 作用域并递归处理嵌套结构。
//
// parent 为空表示当前 block 是函数或 chunk 顶层；parentStatementIndex 表示当前 block 所属语句在父
// block 中的位置；predeclared 会作为当前作用域起始局部变量。
func (analyzer *semanticAnalyzer) analyzeBlock(block *Block, parent *ScopeInfo, parentStatementIndex int, depth int, predeclared []localDeclaration, trailingCondition bool, namespace *functionNamespace) {
	parentID := -1
	if parent != nil {
		// 嵌套 block 记录父作用域编号。
		parentID = parent.ID
	}
	endStatement := len(block.Statements)
	scope := &ScopeInfo{ID: analyzer.nextScopeID, ParentID: parentID, ParentStatementIndex: parentStatementIndex, Depth: depth, StatementCount: endStatement, TrailingCondition: trailingCondition}
	analyzer.nextScopeID++
	block.Scope = scope
	if namespace != nil {
		// 当前函数命名空间记录所有 block 作用域，供 goto 查找可见 label。
		namespace.scopes[scope.ID] = scope
	}
	for _, declaration := range predeclared {
		if len(scope.Locals) >= maxFunctionLocals {
			// 参数或循环变量超过单函数局部变量上限时记录 Lua 5.3 兼容错误。
			analyzer.addError(declaration.position, fmt.Sprintf("line %d: too many local variables", declaration.position.Line))
			continue
		}
		// 参数和 for 循环变量在 block 开始处可见，生命周期延续到 block 结束。
		scope.Locals = append(scope.Locals, LocalInfo{Name: declaration.name, StartStatement: 0, EndStatement: endStatement, Position: declaration.position})
	}
	for statementIndex, statement := range block.Statements {
		// 按语句顺序记录当前 block 直接声明的局部变量、label 和 goto。
		analyzer.analyzeStatement(block, scope, depth, statementIndex, statement, namespace)
	}
}

// analyzeStatement 标注单条语句产生的作用域信息。
//
// block 和 scope 表示语句所在 block；statementIndex 是该语句在 block 内的下标。
func (analyzer *semanticAnalyzer) analyzeStatement(block *Block, scope *ScopeInfo, depth int, statementIndex int, statement Statement, namespace *functionNamespace) {
	switch typedStatement := statement.(type) {
	case *DoStatement:
		// do 语句创建显式子作用域，但共享当前函数级 label/goto 命名空间。
		analyzer.analyzeBlock(typedStatement.Body, scope, statementIndex, depth+1, nil, false, namespace)
	case *LocalAssignmentStatement:
		// local 变量从声明语句开始可见，生命周期延续到当前 block 结束。
		analyzer.addLocalNames(scope, typedStatement.Names, statementIndex, typedStatement.Position)
	case *LocalFunctionStatement:
		// local function 先在外层声明函数名，再用独立函数命名空间分析函数体。
		analyzer.addLocalNames(scope, []string{typedStatement.Name}, statementIndex, typedStatement.Position)
		analyzer.analyzeFunctionBody(typedStatement.Body)
	case *FunctionStatement:
		// 普通 function 语句不声明 local，但函数体内部需要独立作用域和 label/goto 命名空间。
		analyzer.analyzeFunctionBody(typedStatement.Body)
	case *IfStatement:
		// if/elseif/else 每个分支 block 都创建子作用域。
		analyzer.analyzeIfStatement(scope, depth, statementIndex, typedStatement, namespace)
	case *WhileStatement:
		// while body 创建子作用域，循环条件表达式不声明局部变量。
		analyzer.loopDepth++
		analyzer.analyzeBlock(typedStatement.Body, scope, statementIndex, depth+1, nil, false, namespace)
		analyzer.loopDepth--
	case *RepeatUntilStatement:
		// repeat body 创建子作用域；until 条件的局部可见性后续 codegen 阶段再细化。
		analyzer.loopDepth++
		analyzer.analyzeBlock(typedStatement.Body, scope, statementIndex, depth+1, nil, true, namespace)
		analyzer.loopDepth--
	case *NumericForStatement:
		// numeric for 循环变量在循环 body 作用域内可见。
		declarations := []localDeclaration{{name: typedStatement.Name, position: typedStatement.Position}}
		analyzer.loopDepth++
		analyzer.analyzeBlock(typedStatement.Body, scope, statementIndex, depth+1, declarations, false, namespace)
		analyzer.loopDepth--
	case *GenericForStatement:
		// generic for 的名称列表都在循环 body 作用域内可见。
		declarations := analyzer.declarationsFromNames(typedStatement.Names, typedStatement.Position)
		analyzer.loopDepth++
		analyzer.analyzeBlock(typedStatement.Body, scope, statementIndex, depth+1, declarations, false, namespace)
		analyzer.loopDepth--
	case *LabelStatement:
		// label 声明记录到当前函数命名空间，并检查重复声明。
		analyzer.addLabel(block, scope, statementIndex, typedStatement, namespace)
	case *GotoStatement:
		// goto 先记录，待当前函数所有 label 收集完后统一校验。
		gotoInfo := GotoInfo{Name: typedStatement.Label, StatementIndex: statementIndex, Position: typedStatement.Position}
		scope.Gotos = append(scope.Gotos, gotoInfo)
		namespace.gotos = append(namespace.gotos, gotoRecord{block: block, scope: scope, gotoInfo: gotoInfo})
	default:
		if analyzer.analyzeExtensionStatement(block, scope, depth, statementIndex, statement, namespace) {
			// 当前语句已由编译进来的扩展语义分析器处理。
			return
		}
		// 其他语句当前不会声明局部变量或 label/goto，无需处理。
		return
	}
}

// analyzeFunctionBody 使用独立命名空间分析函数体。
//
// 函数参数会作为函数 body 作用域起始局部变量。
func (analyzer *semanticAnalyzer) analyzeFunctionBody(body *FunctionBody) {
	namespace := newFunctionNamespace()
	declarations := analyzer.declarationsFromNames(body.Params, body.Position)
	previousLoopDepth := analyzer.loopDepth
	analyzer.loopDepth = 0
	analyzer.analyzeBlock(body.Body, nil, -1, 0, declarations, false, namespace)
	analyzer.loopDepth = previousLoopDepth
	analyzer.validateGotos(namespace)
}

// analyzeIfStatement 分析 if/elseif/else 的子 block。
//
// 每个分支拥有独立子作用域，但共享同一个函数级 label/goto 命名空间。
func (analyzer *semanticAnalyzer) analyzeIfStatement(parent *ScopeInfo, depth int, parentStatementIndex int, statement *IfStatement, namespace *functionNamespace) {
	for clauseIndex := range statement.Clauses {
		// 每个 if/elseif 分支都按源码顺序创建独立子作用域。
		analyzer.analyzeBlock(statement.Clauses[clauseIndex].Block, parent, parentStatementIndex, depth+1, nil, false, namespace)
	}
	if statement.ElseBlock != nil {
		// else 分支存在时同样创建独立子作用域。
		analyzer.analyzeBlock(statement.ElseBlock, parent, parentStatementIndex, depth+1, nil, false, namespace)
	}
}

// addLocalNames 将名称列表登记为当前作用域局部变量。
//
// startStatement 是声明所在语句下标；局部变量结束位置统一为当前 block 的普通语句数量。
func (analyzer *semanticAnalyzer) addLocalNames(scope *ScopeInfo, names []string, startStatement int, position lexer.Position) {
	for _, name := range names {
		if len(scope.Locals) >= maxFunctionLocals {
			// 超出 Lua 5.3 单函数局部变量上限时记录错误，并停止追加后续局部。
			analyzer.addError(position, fmt.Sprintf("line %d: too many local variables", position.Line))
			return
		}
		// 同名 local 在 Lua 中允许遮蔽，因此这里不报重复错误。
		scope.Locals = append(scope.Locals, LocalInfo{Name: name, StartStatement: startStatement, EndStatement: scope.StatementCount, Position: position})
	}
}

// addLabel 登记 label 并检查同一 block 内重复 label。
//
// Lua 5.3 允许不同 block 中存在同名 label，但同一 block 内同名 label 会让目标不唯一。
func (analyzer *semanticAnalyzer) addLabel(block *Block, scope *ScopeInfo, statementIndex int, statement *LabelStatement, namespace *functionNamespace) {
	labelInfo := LabelInfo{Name: statement.Name, StatementIndex: statementIndex, Position: statement.Position}
	scope.Labels = append(scope.Labels, labelInfo)
	for _, existingLabel := range namespace.labels[statement.Name] {
		if existingLabel.block == block {
			// 同一 block 内重复 label 会导致 goto 目标不唯一，必须报错。
			analyzer.addError(statement.Position, fmt.Sprintf("duplicate label '%s'", statement.Name))
			return
		}
	}
	namespace.labels[statement.Name] = append(namespace.labels[statement.Name], labelRecord{block: block, scope: scope, label: labelInfo})
}

// declarationsFromNames 将名称列表转换为预声明局部变量列表。
//
// position 当前使用语法结构起始位置，后续 token span 细化后可保存逐个名称位置。
func (analyzer *semanticAnalyzer) declarationsFromNames(names []string, position lexer.Position) []localDeclaration {
	declarations := make([]localDeclaration, 0, len(names))
	for _, name := range names {
		// 每个名称都作为当前 block 起始局部变量。
		declarations = append(declarations, localDeclaration{name: name, position: position})
	}

	// 返回完整预声明列表。
	return declarations
}

// validateGotos 校验当前函数命名空间中的 goto。
//
// 当前阶段覆盖未定义 label、跳入内层 block 和同 block 向前跳过 local 声明三类关键错误。
func (analyzer *semanticAnalyzer) validateGotos(namespace *functionNamespace) {
	for _, gotoRecord := range namespace.gotos {
		labelRecord, exists := analyzer.resolveVisibleLabel(gotoRecord, namespace)
		if !exists {
			// goto 目标必须在当前函数内存在。
			analyzer.addError(gotoRecord.gotoInfo.Position, fmt.Sprintf("undefined label '%s'", gotoRecord.gotoInfo.Name))
			continue
		}
		if analyzer.jumpsIntoInnerBlock(gotoRecord.block, labelRecord.block) {
			// 从外层 block 跳入内层 block 会越过内层局部变量作用域入口。
			analyzer.addError(gotoRecord.gotoInfo.Position, fmt.Sprintf("goto '%s' jumps into inner scope", gotoRecord.gotoInfo.Name))
			continue
		}
		if gotoRecord.block == labelRecord.block && labelRecord.label.StatementIndex > gotoRecord.gotoInfo.StatementIndex {
			// 同一 block 向前跳转时，不能越过后续 local 声明进入其生命周期。
			analyzer.validateForwardGotoLocals(gotoRecord, labelRecord, namespace)
		} else if gotoRecord.block != labelRecord.block {
			// 从内层 block 跳到外层后方 label 时，也不能越过外层 block 的 local 声明。
			analyzer.validateOuterForwardGotoLocals(gotoRecord, labelRecord, namespace)
		}
	}
}

// resolveVisibleLabel 查找 goto 可见的目标 label。
//
// Lua label 对同一 block 及其内层 block 可见，但不能从外层或兄弟 block 跳入 label 所在 block；
// 因此解析时优先选择同 block label，其次选择最近的祖先 block label。
func (analyzer *semanticAnalyzer) resolveVisibleLabel(gotoRecord gotoRecord, namespace *functionNamespace) (labelRecord, bool) {
	labels := namespace.labels[gotoRecord.gotoInfo.Name]
	var best labelRecord
	found := false
	for _, candidate := range labels {
		if !analyzer.scopeContains(candidate.scope, gotoRecord.scope, namespace) {
			// 目标 label 所在作用域不是 goto 作用域的祖先，说明不可见。
			continue
		}
		if !found || candidate.scope.Depth > best.scope.Depth {
			// 越深的祖先作用域越接近 goto，按 Lua 可见性选择最近 label。
			best = candidate
			found = true
		}
	}
	return best, found
}

// validateForwardGotoLocals 检查同一 block 内向前 goto 是否跳过 local 声明。
//
// Lua 5.3 禁止 goto 进入尚未开始生命周期的局部变量作用域。
func (analyzer *semanticAnalyzer) validateForwardGotoLocals(gotoRecord gotoRecord, labelRecord labelRecord, namespace *functionNamespace) {
	for _, localInfo := range gotoRecord.scope.Locals {
		// 只检查 goto 和 label 之间新声明的 local。
		if localInfo.StartStatement > gotoRecord.gotoInfo.StatementIndex && localInfo.StartStatement <= labelRecord.label.StatementIndex {
			if analyzer.labelAtBlockTail(gotoRecord.block, labelRecord.label.StatementIndex) {
				// 跳到 block 尾部 label 时，Lua 5.3 认为前面的 local 生命周期已结束。
				continue
			}
			if analyzer.hasScopeClosingBackwardGoto(gotoRecord.block, localInfo.StartStatement, labelRecord.label.StatementIndex, namespace) {
				// local 后存在跳回 local 前 label 的 goto，Lua 会在该跳转处关闭局部作用域。
				continue
			}
			// 跳过该 local 会让目标位置看到尚未初始化的局部变量。
			analyzer.addError(gotoRecord.gotoInfo.Position, fmt.Sprintf("goto '%s' jumps into scope of local '%s'", gotoRecord.gotoInfo.Name, localInfo.Name))
			return
		}
	}
}

// validateOuterForwardGotoLocals 检查从内层 block 跳到外层 label 时是否越过外层 local。
//
// 典型非法形态是 `do goto L end; local x; ::L:: use(x)`：goto 离开内层 block 后进入外层
// local x 的生命周期，Lua 5.3 必须拒绝。
func (analyzer *semanticAnalyzer) validateOuterForwardGotoLocals(gotoRecord gotoRecord, labelRecord labelRecord, namespace *functionNamespace) {
	exitStatement, ok := analyzer.childStatementIndexOnPath(labelRecord.scope, gotoRecord.scope, namespace)
	if !ok {
		// 目标 label 不是 goto 的祖先作用域时，前置可见性校验已经处理，这里无需重复报错。
		return
	}
	if labelRecord.label.StatementIndex <= exitStatement {
		// 跳到外层同一语句之前或当前位置，不会越过后续 local 声明。
		return
	}
	for _, localInfo := range labelRecord.scope.Locals {
		// 只检查内层 block 所在语句之后、目标 label 之前声明的外层 local。
		if localInfo.StartStatement > exitStatement && localInfo.StartStatement <= labelRecord.label.StatementIndex {
			if analyzer.labelAtBlockTail(labelRecord.block, labelRecord.label.StatementIndex) {
				// 目标 label 位于 block 尾部时，该 local 生命周期已经结束。
				continue
			}
			// 跳过该外层 local 会让目标位置看到尚未初始化的局部变量。
			analyzer.addError(gotoRecord.gotoInfo.Position, fmt.Sprintf("goto '%s' jumps into scope of local '%s'", gotoRecord.gotoInfo.Name, localInfo.Name))
			return
		}
	}
}

// childStatementIndexOnPath 返回 ancestor 的直接子作用域在 ancestor block 中的语句下标。
//
// descendant 必须位于 ancestor 内部；返回值用于判断 goto 离开内层 block 后，会从外层 block
// 的哪条语句之后继续向目标 label 前进。
func (analyzer *semanticAnalyzer) childStatementIndexOnPath(ancestor *ScopeInfo, descendant *ScopeInfo, namespace *functionNamespace) (int, bool) {
	if ancestor == nil || descendant == nil || namespace == nil {
		// 缺少作用域信息时无法计算路径。
		return -1, false
	}
	current := descendant
	for current != nil && current.ParentID >= 0 {
		parent := namespace.scopes[current.ParentID]
		if parent == nil {
			// 父作用域索引缺失时无法计算路径。
			return -1, false
		}
		if parent.ID == ancestor.ID {
			// current 是 ancestor 的直接子作用域，ParentStatementIndex 即其所属语句位置。
			return current.ParentStatementIndex, true
		}
		current = parent
	}

	// descendant 不在 ancestor 的子树内。
	return -1, false
}

// labelAtBlockTail 判断 label 后是否只剩 label 或空语句。
//
// Lua label 是空语句；目标 label 位于 block 尾部时，前面 local 的生命周期已经结束，goto 可以
// 跳过这些 local 声明抵达 block 结束位置。
func (analyzer *semanticAnalyzer) labelAtBlockTail(block *Block, labelIndex int) bool {
	if block == nil {
		// 缺少 block 时无法证明位于尾部。
		return false
	}
	if block.Scope != nil && block.Scope.TrailingCondition {
		// repeat-until 的条件表达式位于语法 block 尾部之后，且仍可见 repeat body 的 local。
		return false
	}
	for statementIndex := labelIndex + 1; statementIndex < len(block.Statements); statementIndex++ {
		switch block.Statements[statementIndex].(type) {
		case *LabelStatement, *EmptyStatement:
			// 连续 label 和空语句不延长前面 local 的可见生命周期。
			continue
		default:
			// 后面仍有普通语句，跳到该 label 会进入 local 可见范围。
			return false
		}
	}

	// label 后没有普通语句，视为 block 尾部。
	return true
}

// hasScopeClosingBackwardGoto 判断 local 与目标 label 之间是否存在关闭该 local 作用域的回跳。
//
// Lua 5.3 的 label 属于空语句；当 local 后面的 goto 跳回到 local 声明之前的 label 时，后续
// label 可位于该 local 生命周期之外。closure.lua 中 `goto l4a ... ::l4b::` 依赖该规则。
func (analyzer *semanticAnalyzer) hasScopeClosingBackwardGoto(block *Block, localStart int, targetLabel int, namespace *functionNamespace) bool {
	for statementIndex := localStart + 1; statementIndex < targetLabel && statementIndex < len(block.Statements); statementIndex++ {
		statement, ok := block.Statements[statementIndex].(*GotoStatement)
		if !ok {
			// 非 goto 语句不会关闭 local 作用域。
			continue
		}
		labelRecord, exists := analyzer.resolveVisibleLabel(gotoRecord{block: block, scope: block.Scope, gotoInfo: GotoInfo{Name: statement.Label, StatementIndex: statementIndex, Position: statement.Position}}, namespace)
		if !exists || labelRecord.block != block {
			// 未解析或跨 block 的 label 不用于这个窄规则。
			continue
		}
		if labelRecord.label.StatementIndex < localStart {
			// 回跳到 local 声明前，后续 label 位于该 local 生命周期之外。
			return true
		}
	}

	// 没有发现关闭作用域的回跳。
	return false
}

// jumpsIntoInnerBlock 判断 goto 是否从外层跳入目标 label 所在的内层 block。
//
// 返回 true 表示目标 block 是 goto block 的后代且不是同一个 block。
func (analyzer *semanticAnalyzer) jumpsIntoInnerBlock(gotoBlock *Block, labelBlock *Block) bool {
	if gotoBlock == labelBlock {
		// 同一 block 不属于跳入内层作用域。
		return false
	}
	if gotoBlock.Scope == nil || labelBlock.Scope == nil {
		// 缺少作用域信息时不做内层判断，避免空指针掩盖原始错误。
		return false
	}
	if labelBlock.Scope.Depth <= gotoBlock.Scope.Depth {
		// 目标作用域不比 goto 更深时，不是跳入内层。
		return false
	}

	// 当前没有保存 block 父指针，先用深度作为保守判定；后续 scope tree 完整后可精确化。
	return true
}

// scopeContains 判断 outer 是否为 inner 的同一作用域或祖先作用域。
//
// namespace.scopes 保存当前函数内所有作用域；若链条缺失，则保守返回 false，避免错误允许跨 block 跳转。
func (analyzer *semanticAnalyzer) scopeContains(outer *ScopeInfo, inner *ScopeInfo, namespace *functionNamespace) bool {
	if outer == nil || inner == nil || namespace == nil {
		// 缺少作用域信息时无法证明可见，按不可见处理。
		return false
	}
	for current := inner; current != nil; current = namespace.scopes[current.ParentID] {
		if current.ID == outer.ID {
			// 在父链上命中 outer，说明 outer label 对 inner goto 可见。
			return true
		}
		if current.ParentID < 0 {
			// 到达函数顶层仍未命中 outer，说明不是祖先。
			return false
		}
	}

	// 父链索引缺失时按不可见处理。
	return false
}

// addError 追加一个语义错误。
//
// position 保存错误位置，message 保存不含位置前缀的错误说明。
func (analyzer *semanticAnalyzer) addError(position lexer.Position, message string) {
	// 语义错误聚合后统一返回，帮助调用方一次看到多个 label/goto 问题。
	analyzer.errors = append(analyzer.errors, ParseError{Position: position, Message: message})
}
