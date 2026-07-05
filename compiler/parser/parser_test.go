package parser

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zing/go-lua-vm/compiler/lexer"
	"github.com/zing/go-lua-vm/extensions"
)

// TestParserMarkResetRestoresTokenStream 验证 parser 局部试探可恢复前瞻 token 和后续 token 流。
//
// 该能力是 compile_3000_functions 紧凑函数体试探解析的前置门禁；失败回退必须保持普通 parser 行为。
func TestParserMarkResetRestoresTokenStream(t *testing.T) {
	parser := New("return x + 7 end")

	firstToken := parser.current
	mark := parser.mark()
	parser.advance()
	secondToken := parser.current
	parser.advance()
	thirdToken := parser.current

	parser.reset(mark)
	assertParserTokenEqual(t, parser.current, firstToken)
	parser.advance()
	assertParserTokenEqual(t, parser.current, secondToken)
	parser.advance()
	assertParserTokenEqual(t, parser.current, thirdToken)
}

// assertParserTokenEqual 校验两个 token 的词法内容和源码位置一致。
func assertParserTokenEqual(t *testing.T, got lexer.Token, want lexer.Token) {
	// 标记 helper 后，失败行会指向调用方 token 序列断言。
	t.Helper()
	if got.Kind != want.Kind || got.Text != want.Text || got.Literal != want.Literal || got.Number != want.Number || got.Position != want.Position {
		// token 内容不同说明 reset 后的前瞻或 lexer 后继序列已经偏移。
		t.Fatalf("token mismatch got=%+v want=%+v", got, want)
	}
	if (got.Err == nil) != (want.Err == nil) {
		// 错误 token 的有无也属于词法流语义，必须保持一致。
		t.Fatalf("token error mismatch got=%v want=%v", got.Err, want.Err)
	}
	if got.Err != nil && want.Err != nil && got.Err.Error() != want.Err.Error() {
		// 错误文本不同会影响 parser 兼容错误输出。
		t.Fatalf("token error text mismatch got=%v want=%v", got.Err, want.Err)
	}
}

// TestParserParseChunkAndBlock 验证 chunk 和 block 会保留顺序语句。
//
// 输入包含空语句、local 赋值和普通赋值，用于覆盖当前 parser 的最小语句集合。
func TestParserParseChunkAndBlock(t *testing.T) {
	parser := New("; local a, b = 1, 'x' a, b = b, a _G._ARG = arg msgs[#msgs+1] = item do local scoped = 1 end")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 当前输入只包含已支持语法，不应解析失败。
		t.Fatalf("parse chunk failed: %v", err)
	}
	if chunk.Block == nil {
		// chunk 必须包含顶层 block。
		t.Fatalf("chunk block is nil")
	}
	if len(chunk.Block.Statements) != 6 {
		// block 应按顺序保留六条语句。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}
	if _, ok := chunk.Block.Statements[0].(*EmptyStatement); !ok {
		// 第一条语句应为分号空语句。
		t.Fatalf("first statement should be empty")
	}

	localStatement, ok := chunk.Block.Statements[1].(*LocalAssignmentStatement)
	if !ok {
		// 第二条语句应为 local 赋值。
		t.Fatalf("second statement should be local assignment")
	}
	if len(localStatement.Names) != 2 || localStatement.Names[0] != "a" || localStatement.Names[1] != "b" {
		// local 名称列表必须按源码顺序保存。
		t.Fatalf("unexpected local names=%v", localStatement.Names)
	}
	if len(localStatement.Values) != 2 {
		// local 初始化表达式列表必须包含两个值。
		t.Fatalf("unexpected local value count=%d", len(localStatement.Values))
	}

	assignmentStatement, ok := chunk.Block.Statements[2].(*AssignmentStatement)
	if !ok {
		// 第三条语句应为普通赋值。
		t.Fatalf("third statement should be assignment")
	}
	if len(assignmentStatement.Left) != 2 || len(assignmentStatement.Right) != 2 {
		// 普通赋值左右表达式列表都应保留两个元素。
		t.Fatalf("unexpected assignment sizes left=%d right=%d", len(assignmentStatement.Left), len(assignmentStatement.Right))
	}

	fieldAssignmentStatement, ok := chunk.Block.Statements[3].(*AssignmentStatement)
	if !ok {
		// 第四条语句应为字段赋值。
		t.Fatalf("fourth statement should be field assignment")
	}
	fieldExpression, ok := fieldAssignmentStatement.Left[0].(*FieldAccessExpression)
	if !ok || fieldExpression.Field != "_ARG" {
		// `_G._ARG` 应解析为字段访问左值。
		t.Fatalf("field assignment should keep _ARG target")
	}

	indexAssignmentStatement, ok := chunk.Block.Statements[4].(*AssignmentStatement)
	if !ok {
		// 第五条语句应为索引赋值。
		t.Fatalf("fifth statement should be index assignment")
	}
	indexExpression, ok := indexAssignmentStatement.Left[0].(*IndexExpression)
	if !ok {
		// `msgs[#msgs+1]` 应解析为索引访问左值。
		t.Fatalf("index assignment should keep index target")
	}
	if _, ok := indexExpression.Index.(*BinaryExpression); !ok {
		// 索引表达式应保留 `#msgs+1` 的二元表达式结构。
		t.Fatalf("index target should keep binary index expression")
	}

	doStatement, ok := chunk.Block.Statements[5].(*DoStatement)
	if !ok {
		// 第六条语句应为 do-end 显式 block。
		t.Fatalf("sixth statement should be do statement")
	}
	if len(doStatement.Body.Statements) != 1 {
		// do block 应保留内部 local 语句。
		t.Fatalf("unexpected do body statement count=%d", len(doStatement.Body.Statements))
	}
}

// TestParserPreallocatesLargeTopLevelFunctionBlock 验证大量顶层函数声明会预留 block 语句容量。
//
// 该优化只影响 Statement slice 的底层容量，不改变 AST 语句数量、顺序或函数体解析结果。
func TestParserPreallocatesLargeTopLevelFunctionBlock(t *testing.T) {
	// 构造超过容量 hint 阈值的顶层函数声明，覆盖 compile_3000_functions 的源码形态。
	var builder strings.Builder
	const functionCount = 300
	for functionIndex := 0; functionIndex < functionCount; functionIndex++ {
		// 每行以 function 开头，容量预估可以在不消费 token 的情况下计入语句下界。
		builder.WriteString("function f")
		builder.WriteString(strconv.Itoa(functionIndex))
		builder.WriteString("(x) return x + 1 end\n")
	}
	builder.WriteString("return f299(1)\n")
	source := builder.String()

	parser := New(source)
	chunk, err := parser.ParseChunk()
	if err != nil {
		// 合法的大量函数声明样例必须能正常解析。
		t.Fatalf("ParseChunk failed: %v", err)
	}
	if len(chunk.Block.Statements) != functionCount {
		// 容量预估不能改变实际语句数量。
		t.Fatalf("unexpected statement count: %d", len(chunk.Block.Statements))
	}
	if cap(chunk.Block.Statements) < functionCount {
		// 顶层函数声明容量应按行首声明数量预留。
		t.Fatalf("expected preallocated top-level statement capacity, cap=%d", cap(chunk.Block.Statements))
	}

	compactParser := NewCompactWithSyntax(source, extensions.Default())
	compactChunk, err := compactParser.ParseChunk()
	if err != nil {
		// compact 编译入口必须同样保持合法源码可解析。
		t.Fatalf("compact ParseChunk failed: %v", err)
	}
	if len(compactChunk.Block.Statements) != functionCount {
		// compact 节点预留不能改变语句数量。
		t.Fatalf("unexpected compact statement count: %d", len(compactChunk.Block.Statements))
	}
	if len(compactParser.compactFunctionStatementPage) != functionCount || cap(compactParser.compactFunctionStatementPage) < functionCount {
		// compact function 节点应复用顶层容量预估，避免 3000 函数场景反复分配 256 大小页。
		t.Fatalf("unexpected compact arena len/cap=%d/%d", len(compactParser.compactFunctionStatementPage), cap(compactParser.compactFunctionStatementPage))
	}
}

// TestParserLocalAssignmentWithoutValues 验证 local 声明可以没有初始化表达式。
//
// Lua 允许 `local a, b`，未初始化变量后续 codegen 会按 nil 处理。
func TestParserLocalAssignmentWithoutValues(t *testing.T) {
	parser := New("local a, b")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 合法 local 声明不应解析失败。
		t.Fatalf("parse local failed: %v", err)
	}
	localStatement, ok := chunk.Block.Statements[0].(*LocalAssignmentStatement)
	if !ok {
		// 唯一语句应为 local 赋值。
		t.Fatalf("statement should be local assignment")
	}
	if len(localStatement.Values) != 0 {
		// 没有等号时 Values 必须为空。
		t.Fatalf("unexpected values=%v", localStatement.Values)
	}
}

// TestParserRejectsUnsupportedStatement 验证尚未实现语句会返回明确错误。
//
// 当前阶段非法顶层关键字不能静默跳过。
func TestParserRejectsUnsupportedStatement(t *testing.T) {
	parser := New("and")

	_, err := parser.ParseChunk()
	if err == nil {
		// 未实现语句必须返回错误。
		t.Fatalf("expected unsupported statement error")
	}
}

// TestParserIllegalStringReportsNearToken 验证非法字符串 token 使用 Lua 风格 near 错误片段。
//
// 官方 literals.lua 的 lexerror 断言会匹配 `near ...` 后的非法 escape 或 `<eof>`，因此
// parser 需要保留 lexer 的非法 token 文本，而不是只返回 expected expression。
func TestParserIllegalStringReportsNearToken(t *testing.T) {
	for _, testCase := range []struct {
		name string
		src  string
		want string
	}{
		{name: "invalid escape", src: `return "abc\x"`, want: `near '"abc\x"'`},
		{name: "unfinished", src: `return 'alo`, want: `near <eof>`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			parser := New(testCase.src)

			_, err := parser.ParseChunk()
			if err == nil {
				// 非法字符串必须返回语法错误。
				t.Fatalf("expected parse error")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				// 错误文本必须包含 Lua 风格 near 片段，供官方 scanner 测试匹配。
				t.Fatalf("error %q missing %q", err.Error(), testCase.want)
			}
		})
	}
}

// TestParserFunctionStatements 验证 local function 和普通 function 语句。
//
// 当前阶段函数名只支持简单标识符，函数体支持普通参数列表和已支持语句。
func TestParserFunctionStatements(t *testing.T) {
	parser := New("local function add(a, b) result = a end function main() value = result end showmem = function () return result end")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 函数语句应能成功解析。
		t.Fatalf("parse functions failed: %v", err)
	}
	if len(chunk.Block.Statements) != 3 {
		// 顶层应包含两条函数语句和一条匿名函数赋值。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}

	localFunction, ok := chunk.Block.Statements[0].(*LocalFunctionStatement)
	if !ok {
		// 第一条语句应为 local function。
		t.Fatalf("first statement should be local function")
	}
	if localFunction.Name != "add" || len(localFunction.Body.Params) != 2 {
		// local function 应保留函数名和两个参数。
		t.Fatalf("unexpected local function=%+v", localFunction)
	}
	if localFunction.Body != &localFunction.inlineBody {
		// local function 的 Body 应指向语句自身内嵌函数体。
		t.Fatalf("local function body should use inline function body")
	}

	functionStatement, ok := chunk.Block.Statements[1].(*FunctionStatement)
	if !ok {
		// 第二条语句应为普通 function。
		t.Fatalf("second statement should be function")
	}
	if functionStatement.Name != "main" || len(functionStatement.Body.Body.Statements) != 1 {
		// 普通 function 应保留名称和函数体语句。
		t.Fatalf("unexpected function statement=%+v", functionStatement)
	}
	if functionStatement.Body != &functionStatement.inlineBody {
		// 普通 function 的 Body 应指向语句自身内嵌函数体。
		t.Fatalf("function statement body should use inline function body")
	}

	assignmentStatement, ok := chunk.Block.Statements[2].(*AssignmentStatement)
	if !ok {
		// 第三条语句应为普通赋值。
		t.Fatalf("third statement should be assignment")
	}
	functionExpression, ok := assignmentStatement.Right[0].(*FunctionExpression)
	if !ok {
		// 赋值右侧应解析为匿名函数表达式。
		t.Fatalf("assignment right side should be function expression")
	}
	if functionExpression.Body == nil || len(functionExpression.Body.Params) != 0 {
		// 匿名函数体应保留空参数列表。
		t.Fatalf("unexpected function expression=%+v", functionExpression)
	}
	if functionExpression.Body != &functionExpression.inlineBody {
		// 匿名函数表达式的 Body 应指向表达式自身内嵌函数体。
		t.Fatalf("function expression body should use inline function body")
	}
}

// TestParserFieldAndMethodFunctionStatements 验证字段函数和方法函数定义。
//
// 字段函数会降级为赋值语句；冒号方法还会在函数体参数列表前补入 self。
func TestParserFieldAndMethodFunctionStatements(t *testing.T) {
	parser := New("function mod.add(x) return x end function mod:inc(y) return y end")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 字段函数和方法函数都属于 Lua 5.3 普通函数定义语法。
		t.Fatalf("parse field and method functions failed: %v", err)
	}
	if len(chunk.Block.Statements) != 2 {
		// 输入应解析出两个函数定义赋值语句。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}

	fieldAssignment, ok := chunk.Block.Statements[0].(*AssignmentStatement)
	if !ok {
		// 字段函数定义应降级为字段赋值。
		t.Fatalf("field function should become assignment")
	}
	fieldTarget, ok := fieldAssignment.Left[0].(*FieldAccessExpression)
	if !ok || fieldTarget.Field != "add" {
		// 赋值左侧应保留 mod.add 字段访问。
		t.Fatalf("unexpected field function target=%+v", fieldAssignment.Left[0])
	}
	fieldReceiver, ok := fieldTarget.Receiver.(*NameExpression)
	if !ok || fieldReceiver.Name != "mod" {
		// 字段访问接收者应保留 mod 名称。
		t.Fatalf("unexpected field receiver=%+v", fieldTarget.Receiver)
	}
	fieldFunction, ok := fieldAssignment.Right[0].(*FunctionExpression)
	if !ok || len(fieldFunction.Body.Params) != 1 || fieldFunction.Body.Params[0] != "x" {
		// 字段函数右侧应是保留原参数的匿名函数表达式。
		t.Fatalf("unexpected field function body=%+v", fieldAssignment.Right[0])
	}
	if fieldFunction.Body != &fieldFunction.inlineBody {
		// 字段函数降级后的匿名函数体应复用表达式内嵌槽。
		t.Fatalf("field function body should use inline function body")
	}

	methodAssignment, ok := chunk.Block.Statements[1].(*AssignmentStatement)
	if !ok {
		// 方法函数定义同样应降级为字段赋值。
		t.Fatalf("method function should become assignment")
	}
	methodFunction, ok := methodAssignment.Right[0].(*FunctionExpression)
	if !ok || len(methodFunction.Body.Params) != 2 || methodFunction.Body.Params[0] != "self" || methodFunction.Body.Params[1] != "y" {
		// 冒号方法必须在参数列表前注入 self。
		t.Fatalf("unexpected method function body=%+v", methodAssignment.Right[0])
	}
	if methodFunction.Body != &methodFunction.inlineBody {
		// 方法函数降级后的匿名函数体应复用表达式内嵌槽。
		t.Fatalf("method function body should use inline function body")
	}
}

// TestParserFunctionBodyInlineSingleParam 验证单参数函数复用函数体内嵌参数槽。
//
// 对外 Params 仍保持普通切片语义；多参数函数继续使用普通切片保存完整参数顺序。
func TestParserFunctionBodyInlineSingleParam(t *testing.T) {
	parser := New("function one(x) return x end function pair(a, b) return a end")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 两个普通函数都应成功解析。
		t.Fatalf("parse functions failed: %v", err)
	}
	oneFunction, ok := chunk.Block.Statements[0].(*FunctionStatement)
	if !ok {
		// 第一条语句应为简单函数定义。
		t.Fatalf("first statement should be function")
	}
	if len(oneFunction.Body.Params) != 1 || oneFunction.Body.Params[0] != "x" {
		// 单参数函数应保留原始参数名。
		t.Fatalf("unexpected one params=%+v", oneFunction.Body.Params)
	}
	if oneFunction.Body != &oneFunction.inlineBody {
		// 简单函数语句的 Body 应指向语句自身内嵌函数体。
		t.Fatalf("one function body should use inline function body")
	}
	if &oneFunction.Body.Params[0] != &oneFunction.Body.inlineParams[0] {
		// 单参数函数的 Params 应指向函数体内嵌槽。
		t.Fatalf("single param should use inline slot")
	}
	if oneFunction.Body.Body != &oneFunction.Body.inlineBody {
		// 函数体 block 应指向函数体内嵌 block 槽。
		t.Fatalf("function body should use inline block")
	}
	if oneFunction.Body.Body.Scope != &oneFunction.Body.Body.inlineScope {
		// 函数体 block 的作用域应指向 block 内嵌 scope 槽。
		t.Fatalf("function body scope should use inline scope")
	}

	pairFunction, ok := chunk.Block.Statements[1].(*FunctionStatement)
	if !ok {
		// 第二条语句应为简单函数定义。
		t.Fatalf("second statement should be function")
	}
	if len(pairFunction.Body.Params) != 2 || pairFunction.Body.Params[0] != "a" || pairFunction.Body.Params[1] != "b" {
		// 多参数函数必须保持源码参数顺序。
		t.Fatalf("unexpected pair params=%+v", pairFunction.Body.Params)
	}
	if pairFunction.Body.Body != &pairFunction.Body.inlineBody {
		// 多参数函数同样应复用函数体内嵌 block。
		t.Fatalf("pair function body should use inline block")
	}
	if pairFunction.Body.Body.Scope != &pairFunction.Body.Body.inlineScope {
		// 多参数函数体 block 同样应复用内嵌 scope。
		t.Fatalf("pair function body scope should use inline scope")
	}
}

// TestParserReturnStatementInlineSingleValue 验证单返回值复用 return 节点内嵌表达式槽。
//
// 对外 Values 仍保持普通切片语义；多返回值继续使用普通切片保存完整表达式顺序。
func TestParserReturnStatementInlineSingleValue(t *testing.T) {
	parser := New("function one(x) return x end function pair(x) return x, x end")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 两个普通函数都应成功解析。
		t.Fatalf("parse functions failed: %v", err)
	}
	oneFunction, ok := chunk.Block.Statements[0].(*FunctionStatement)
	if !ok {
		// 第一条语句应为简单函数定义。
		t.Fatalf("first statement should be function")
	}
	oneReturn := oneFunction.Body.Body.Return
	if oneReturn == nil || len(oneReturn.Values) != 1 {
		// 单返回值函数应保留一个返回表达式。
		t.Fatalf("unexpected one return=%+v", oneReturn)
	}
	if oneReturn != &oneFunction.Body.Body.inlineReturn {
		// block 内唯一 return 应复用 block 内嵌 return 节点。
		t.Fatalf("single return statement should use block inline slot")
	}
	if &oneReturn.Values[0] != &oneReturn.inlineValues[0] {
		// 单返回值的 Values 应指向 return 节点内嵌槽。
		t.Fatalf("single return value should use inline slot")
	}

	pairFunction, ok := chunk.Block.Statements[1].(*FunctionStatement)
	if !ok {
		// 第二条语句应为简单函数定义。
		t.Fatalf("second statement should be function")
	}
	pairReturn := pairFunction.Body.Body.Return
	if pairReturn == nil || len(pairReturn.Values) != 2 {
		// 多返回值函数必须保持源码返回值顺序。
		t.Fatalf("unexpected pair return=%+v", pairReturn)
	}
	if pairReturn != &pairFunction.Body.Body.inlineReturn {
		// 多返回值同样只有一个 return 语句，应复用 block 内嵌节点。
		t.Fatalf("multi return statement should use block inline slot")
	}
}

// TestParserOrdinarySimpleFunctionKeepsFullAST 验证普通 parser 入口不会生成编译专用 compact summary。
//
// 未来 compile-only streaming 路径可以跳过部分 AST 构造，但 parser.New 必须继续返回完整
// FunctionStatement、ReturnStatement 和 BinaryExpression，供格式化、诊断、工具和宿主直接读取。
func TestParserOrdinarySimpleFunctionKeepsFullAST(t *testing.T) {
	source := "function f0(x) return x + 7 end\nreturn f0(1)\n"

	ordinaryParser := New(source)
	ordinaryChunk, err := ordinaryParser.ParseChunk()
	if err != nil {
		// 普通入口必须能解析官方 benchmark 同构的简单函数声明。
		t.Fatalf("ordinary ParseChunk failed: %v", err)
	}
	ordinaryFunction, ok := ordinaryChunk.Block.Statements[0].(*FunctionStatement)
	if !ok {
		// 顶层第一条语句必须继续以公开 FunctionStatement 暴露。
		t.Fatalf("ordinary parser should expose FunctionStatement, got %T", ordinaryChunk.Block.Statements[0])
	}
	if _, _, _, _, _, ok := ordinaryFunction.Body.CompactSimpleAddInteger(); ok || ordinaryFunction.Body.compactSimple != nil {
		// 普通 parser 不允许携带编译专用 summary，避免宿主误依赖内部 fast path。
		t.Fatalf("ordinary parser should not expose compact summary")
	}
	if ordinaryFunction.Body.Body == nil || ordinaryFunction.Body.Body.Return == nil {
		// 普通 AST 必须保留函数体 block 和 return 节点。
		t.Fatalf("ordinary function should keep full return AST: %+v", ordinaryFunction.Body)
	}
	returnStatement := ordinaryFunction.Body.Body.Return
	if len(returnStatement.Values) != 1 {
		// `return x + 7` 必须解析为单返回值表达式。
		t.Fatalf("unexpected return values=%d", len(returnStatement.Values))
	}
	binaryExpression, ok := returnStatement.Values[0].(*BinaryExpression)
	if !ok || binaryExpression.Operator != "+" {
		// 返回值必须保留二元加法表达式，供错误位置和工具分析使用。
		t.Fatalf("return value should be binary + expression, got %#v", returnStatement.Values[0])
	}
	leftName, ok := binaryExpression.Left.(*NameExpression)
	if !ok || leftName.Name != "x" {
		// 左操作数必须保留参数名读取。
		t.Fatalf("unexpected left expression=%#v", binaryExpression.Left)
	}
	rightLiteral, ok := binaryExpression.Right.(*LiteralExpression)
	if !ok || rightLiteral.Number.Integer != 7 {
		// 右操作数必须保留整数 literal 节点。
		t.Fatalf("unexpected right expression=%#v", binaryExpression.Right)
	}

	compactParser := NewCompactWithSyntax(source, extensions.Default())
	compactChunk, err := compactParser.ParseChunk()
	if err != nil {
		// 编译专用入口也必须成功解析同一源码。
		t.Fatalf("compact ParseChunk failed: %v", err)
	}
	compactFunction, ok := compactChunk.Block.Statements[0].(*CompactFunctionStatement)
	if !ok {
		// 编译专用入口允许精确目标形态使用轻量语句节点，codegen 必须等价生成 Proto。
		t.Fatalf("compact parser should expose CompactFunctionStatement, got %T", compactChunk.Block.Statements[0])
	}
	if compactFunction.Name != "f0" || compactFunction.ParamName != "x" || compactFunction.Integer != 7 {
		// 编译专用入口应只在精确目标形态上生成 summary。
		t.Fatalf("unexpected compact statement=%+v", compactFunction)
	}
	if compactFunction.LineDefined != 1 || compactFunction.LastLineDefined != 1 || compactFunction.ReturnLine != 1 ||
		compactFunction.OperatorLine != 1 || compactFunction.Pos().Line != 1 {
		// compact 语句必须保留 codegen 所需关键行号，供 debug 行号使用。
		t.Fatalf("unexpected compact positions=%+v", compactFunction)
	}
}

// TestParserIfWhileRepeatStatements 验证 if/elseif/else、while 和 repeat-until 语句。
//
// 条件表达式当前使用基础表达式，block 内使用已实现赋值语句。
func TestParserIfWhileRepeatStatements(t *testing.T) {
	parser := New("if true then a = 1 elseif false then a = 2 else a = 3 end while flag do flag = false end repeat a = a until true")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 控制流语句应能成功解析。
		t.Fatalf("parse control flow failed: %v", err)
	}
	if len(chunk.Block.Statements) != 3 {
		// 顶层应包含 if、while、repeat 三条语句。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}

	ifStatement, ok := chunk.Block.Statements[0].(*IfStatement)
	if !ok {
		// 第一条语句应为 if。
		t.Fatalf("first statement should be if")
	}
	if len(ifStatement.Clauses) != 2 || ifStatement.ElseBlock == nil {
		// if 应包含一个 if 分支、一个 elseif 分支和一个 else block。
		t.Fatalf("unexpected if statement=%+v", ifStatement)
	}

	whileStatement, ok := chunk.Block.Statements[1].(*WhileStatement)
	if !ok {
		// 第二条语句应为 while。
		t.Fatalf("second statement should be while")
	}
	if len(whileStatement.Body.Statements) != 1 {
		// while block 应包含一条赋值语句。
		t.Fatalf("unexpected while body=%+v", whileStatement.Body)
	}

	repeatStatement, ok := chunk.Block.Statements[2].(*RepeatUntilStatement)
	if !ok {
		// 第三条语句应为 repeat-until。
		t.Fatalf("third statement should be repeat-until")
	}
	if len(repeatStatement.Body.Statements) != 1 {
		// repeat block 应包含一条赋值语句。
		t.Fatalf("unexpected repeat body=%+v", repeatStatement.Body)
	}
}

// TestParserReturnSemicolonBeforeBlockEnd 验证无返回值 return 可携带结尾分号。
//
// Lua 5.3 允许 `return; end`，分号应归属于 return 语句自身，不能被解析成 return 后的空语句。
func TestParserReturnSemicolonBeforeBlockEnd(t *testing.T) {
	parser := New("function f(i) while 1 do if i > 0 then i = i - 1 else return; end; end; end")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 官方 constructs.lua 使用 `else return; end`，该语法必须可解析。
		t.Fatalf("parse return semicolon before end failed: %v", err)
	}
	if len(chunk.Block.Statements) != 1 {
		// 输入只包含一个函数定义语句。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}
}

// TestParserForBreakGotoAndLabelStatements 验证 for、break、goto 和 label 语句。
//
// 该用例覆盖 numeric for、generic for、break、goto 和 `::label::` 五类语句节点。
func TestParserForBreakGotoAndLabelStatements(t *testing.T) {
	parser := New("for i = 1, 3, 1 do break end for k, v in iter, state do goto done end ::done::")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 当前输入只包含本轮支持的语句，不应解析失败。
		t.Fatalf("parse for/goto/label failed: %v", err)
	}
	if len(chunk.Block.Statements) != 3 {
		// 顶层应包含 numeric for、generic for 和 label 三条语句。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}

	numericFor, ok := chunk.Block.Statements[0].(*NumericForStatement)
	if !ok {
		// 第一条语句应为 numeric for。
		t.Fatalf("first statement should be numeric for")
	}
	if numericFor.Name != "i" || numericFor.Step == nil || len(numericFor.Body.Statements) != 1 {
		// numeric for 应保留循环变量、步长和 body。
		t.Fatalf("unexpected numeric for=%+v", numericFor)
	}
	if _, ok := numericFor.Body.Statements[0].(*BreakStatement); !ok {
		// numeric for body 中应解析出 break。
		t.Fatalf("numeric for body should contain break")
	}

	genericFor, ok := chunk.Block.Statements[1].(*GenericForStatement)
	if !ok {
		// 第二条语句应为 generic for。
		t.Fatalf("second statement should be generic for")
	}
	if len(genericFor.Names) != 2 || len(genericFor.Iterators) != 2 || len(genericFor.Body.Statements) != 1 {
		// generic for 应保留名称列表、迭代表达式列表和 body。
		t.Fatalf("unexpected generic for=%+v", genericFor)
	}
	gotoStatement, ok := genericFor.Body.Statements[0].(*GotoStatement)
	if !ok {
		// generic for body 中应解析出 goto。
		t.Fatalf("generic for body should contain goto")
	}
	if gotoStatement.Label != "done" {
		// goto 应保留目标标签名。
		t.Fatalf("unexpected goto label=%q", gotoStatement.Label)
	}

	labelStatement, ok := chunk.Block.Statements[2].(*LabelStatement)
	if !ok {
		// 第三条语句应为 label。
		t.Fatalf("third statement should be label")
	}
	if labelStatement.Name != "done" {
		// label 应保留标签名。
		t.Fatalf("unexpected label name=%q", labelStatement.Name)
	}
}

// TestParserLua53SyntaxKeepsExtensionWordsAsIdentifiers 验证关闭扩展后词法兼容 Lua 5.3。
//
// continue 和 switch 在扩展关闭时必须仍可作为普通变量名，避免语法糖污染 Lua 5.3 保留字表。
func TestParserLua53SyntaxKeepsExtensionWordsAsIdentifiers(t *testing.T) {
	parser := NewWithSyntax("local continue = 1 local switch = continue + 1", extensions.None())

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 关闭扩展后同名变量仍是合法 Lua 5.3 代码。
		t.Fatalf("parse lua53 identifiers failed: %v", err)
	}
	if len(chunk.Block.Statements) != 2 {
		// 两条 local 语句必须都被保留下来。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}

	parser = NewWithSyntax("continue", extensions.None())
	if _, err := parser.ParseChunk(); err == nil || !strings.Contains(err.Error(), "expected operator \"=\"") {
		// 关闭扩展后裸 continue 应按普通标识符语句失败，而不是解析成 continue 控制流。
		t.Fatalf("expected identifier statement error, got %v", err)
	}
}

// TestParserReturnFunctionBodyVarargTableAndPrefix 验证 return、函数体、vararg、table 和 prefix expression。
//
// 该用例覆盖本轮新增的 5 个 parser TODO，表达式仍限制在当前基础表达式集合内。
func TestParserReturnFunctionBodyVarargTableAndPrefix(t *testing.T) {
	parser := New("function pack(a, ...) local t = {a, ..., (a), ___Glob = 1, tostring = true} return t, ... end")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 当前输入只包含已支持语法，不应解析失败。
		t.Fatalf("parse function body and return failed: %v", err)
	}
	functionStatement, ok := chunk.Block.Statements[0].(*FunctionStatement)
	if !ok {
		// 顶层语句应为普通 function。
		t.Fatalf("statement should be function")
	}
	if len(functionStatement.Body.Params) != 1 || !functionStatement.Body.Vararg {
		// 函数体应保留一个普通参数和 vararg 标记。
		t.Fatalf("unexpected function body=%+v", functionStatement.Body)
	}
	if len(functionStatement.Body.Body.Statements) != 1 {
		// 函数体普通语句区应包含 local table 赋值。
		t.Fatalf("unexpected function statement count=%d", len(functionStatement.Body.Body.Statements))
	}

	localStatement, ok := functionStatement.Body.Body.Statements[0].(*LocalAssignmentStatement)
	if !ok {
		// 函数体第一条语句应为 local 赋值。
		t.Fatalf("function body statement should be local assignment")
	}
	if len(localStatement.Values) != 1 {
		// local t 应有一个 table constructor 初始化表达式。
		t.Fatalf("unexpected local values=%d", len(localStatement.Values))
	}
	tableExpression, ok := localStatement.Values[0].(*TableConstructorExpression)
	if !ok {
		// local 初始化值应为 table constructor。
		t.Fatalf("local value should be table constructor")
	}
	if len(tableExpression.Fields) != 3 {
		// table constructor 应保存 a、...、(a) 三个数组字段。
		t.Fatalf("unexpected table field count=%d", len(tableExpression.Fields))
	}
	if len(tableExpression.RecordFields) != 2 {
		// table constructor 应保存两个名称键值字段。
		t.Fatalf("unexpected table record field count=%d", len(tableExpression.RecordFields))
	}
	if tableExpression.RecordFields[0].Name != "___Glob" || tableExpression.RecordFields[1].Name != "tostring" {
		// 记录字段应保留字段名字符串 key。
		t.Fatalf("unexpected table record fields=%+v", tableExpression.RecordFields)
	}
	if _, ok := tableExpression.Fields[1].(*VarargExpression); !ok {
		// 第二个字段应为 vararg 表达式。
		t.Fatalf("second table field should be vararg")
	}
	if _, ok := tableExpression.Fields[2].(*PrefixExpression); !ok {
		// 第三个字段应为括号 prefix expression。
		t.Fatalf("third table field should be prefix expression")
	}

	returnStatement := functionStatement.Body.Body.Return
	if returnStatement == nil {
		// 函数体 block 应保留 return 语句。
		t.Fatalf("function body should have return")
	}
	if len(returnStatement.Values) != 2 {
		// return t, ... 应保留两个返回表达式。
		t.Fatalf("unexpected return value count=%d", len(returnStatement.Values))
	}
	if _, ok := returnStatement.Values[1].(*VarargExpression); !ok {
		// 第二个返回值应为 vararg 表达式。
		t.Fatalf("second return value should be vararg")
	}
}

// TestParserTableFieldAcceptsFunctionCall 验证 table 数组字段支持 identifier 开头的完整表达式。
//
// Lua 5.3 官方 gc.lua 使用 `{setmetatable({}, {__gc = function () end})}`，解析器不能把
// `setmetatable` 提前截断为裸名称字段。
func TestParserTableFieldAcceptsFunctionCall(t *testing.T) {
	parser := New("local u = {setmetatable({}, {__gc = function () end})}")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 嵌套 table constructor 中的函数调用字段必须可解析。
		t.Fatalf("parse function call table field failed: %v", err)
	}
	localStatement, ok := chunk.Block.Statements[0].(*LocalAssignmentStatement)
	if !ok {
		// 顶层语句应为 local 赋值。
		t.Fatalf("statement should be local assignment")
	}
	tableExpression, ok := localStatement.Values[0].(*TableConstructorExpression)
	if !ok {
		// local 初始化值应是 table constructor。
		t.Fatalf("local value should be table constructor")
	}
	if len(tableExpression.Fields) != 1 {
		// 外层 table 应只有一个数组字段。
		t.Fatalf("unexpected table field count=%d", len(tableExpression.Fields))
	}
	if _, ok := tableExpression.Fields[0].(*FunctionCallExpression); !ok {
		// identifier 开头的函数调用必须作为完整数组字段保留。
		t.Fatalf("table field should be function call, got %T", tableExpression.Fields[0])
	}
}

// TestParserCallUnaryBinaryAndPowerExpressions 验证调用、一元、二元优先级和右结合幂运算。
//
// 该用例覆盖 function call、method call、unary expression、binary expression precedence 和 right-associative power。
func TestParserCallUnaryBinaryAndPowerExpressions(t *testing.T) {
	parser := New("call(1, 2 + 3 * 4) io.stderr:write(-value, not flag) os.setlocale\"C\" local x = 2 ^ 3 ^ 4 local y = -a ^ b")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 当前输入只包含已支持语法，不应解析失败。
		t.Fatalf("parse call/unary/binary failed: %v", err)
	}
	if len(chunk.Block.Statements) != 5 {
		// 顶层应包含普通调用、方法调用、调用简写和两个 local 赋值。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}

	callStatement, ok := chunk.Block.Statements[0].(*FunctionCallStatement)
	if !ok {
		// 第一条语句应为普通函数调用语句。
		t.Fatalf("first statement should be function call")
	}
	callExpression, ok := callStatement.Call.(*FunctionCallExpression)
	if !ok {
		// 调用语句内部应保存普通函数调用表达式。
		t.Fatalf("call statement should contain function call expression")
	}
	if len(callExpression.Arguments) != 2 {
		// call 应保留两个显式参数。
		t.Fatalf("unexpected call argument count=%d", len(callExpression.Arguments))
	}
	plusExpression, ok := callExpression.Arguments[1].(*BinaryExpression)
	if !ok || plusExpression.Operator != "+" {
		// 第二个参数最外层应先按低优先级归约为加法。
		t.Fatalf("second call argument should be plus expression")
	}
	multiplyExpression, ok := plusExpression.Right.(*BinaryExpression)
	if !ok || multiplyExpression.Operator != "*" {
		// 乘法优先级高于加法，因此应位于加法右侧子树。
		t.Fatalf("plus right side should be multiply expression")
	}

	methodStatement, ok := chunk.Block.Statements[1].(*FunctionCallStatement)
	if !ok {
		// 第二条语句应为方法调用语句。
		t.Fatalf("second statement should be method call statement")
	}
	methodExpression, ok := methodStatement.Call.(*MethodCallExpression)
	if !ok {
		// 方法调用语句内部应保存 method call expression。
		t.Fatalf("method statement should contain method call expression")
	}
	if methodExpression.Method != "write" || len(methodExpression.Arguments) != 2 {
		// method call 应保留方法名和两个显式参数。
		t.Fatalf("unexpected method expression=%+v", methodExpression)
	}
	fieldExpression, ok := methodExpression.Receiver.(*FieldAccessExpression)
	if !ok || fieldExpression.Field != "stderr" {
		// method call 的接收者应允许点号字段访问链。
		t.Fatalf("method receiver should be field access")
	}
	nameExpression, ok := fieldExpression.Receiver.(*NameExpression)
	if !ok || nameExpression.Name != "io" {
		// 字段访问左侧应保留原始 `io` 名称表达式。
		t.Fatalf("field receiver should be io name")
	}
	if unaryExpression, ok := methodExpression.Arguments[0].(*UnaryExpression); !ok || unaryExpression.Operator != "-" {
		// 第一个方法参数应是一元取负表达式。
		t.Fatalf("first method argument should be unary minus")
	}
	if unaryExpression, ok := methodExpression.Arguments[1].(*UnaryExpression); !ok || unaryExpression.Operator != "not" {
		// 第二个方法参数应是一元 not 表达式。
		t.Fatalf("second method argument should be unary not")
	}

	shortcutCallStatement, ok := chunk.Block.Statements[2].(*FunctionCallStatement)
	if !ok {
		// 第三条语句应为字符串参数调用简写。
		t.Fatalf("third statement should be shortcut call statement")
	}
	shortcutCallExpression, ok := shortcutCallStatement.Call.(*FunctionCallExpression)
	if !ok || len(shortcutCallExpression.Arguments) != 1 {
		// 调用简写应归约为普通函数调用表达式并保留一个参数。
		t.Fatalf("shortcut call should contain one function call argument")
	}
	expectedLocaleName := string([]byte{67})
	if literalExpression, ok := shortcutCallExpression.Arguments[0].(*LiteralExpression); !ok || literalExpression.Value != expectedLocaleName {
		// 字符串简写参数应保留解码后的字符串值。
		t.Fatalf("shortcut call argument should be locale name")
	}

	powerLocal, ok := chunk.Block.Statements[3].(*LocalAssignmentStatement)
	if !ok {
		// 第四条语句应为 local x。
		t.Fatalf("fourth statement should be local assignment")
	}
	powerExpression, ok := powerLocal.Values[0].(*BinaryExpression)
	if !ok || powerExpression.Operator != "^" {
		// 2 ^ 3 ^ 4 最外层应是幂运算。
		t.Fatalf("local x should be power expression")
	}
	if rightPower, ok := powerExpression.Right.(*BinaryExpression); !ok || rightPower.Operator != "^" {
		// 幂运算右结合，因此右侧还应是一个幂运算。
		t.Fatalf("power expression should be right associative")
	}

	unaryPowerLocal, ok := chunk.Block.Statements[4].(*LocalAssignmentStatement)
	if !ok {
		// 第五条语句应为 local y。
		t.Fatalf("fifth statement should be local assignment")
	}
	unaryPowerExpression, ok := unaryPowerLocal.Values[0].(*UnaryExpression)
	if !ok || unaryPowerExpression.Operator != "-" {
		// -a ^ b 应先解析幂运算再套一元取负。
		t.Fatalf("local y should be unary minus")
	}
	if innerPower, ok := unaryPowerExpression.Operand.(*BinaryExpression); !ok || innerPower.Operator != "^" {
		// 一元取负的操作数应是 a ^ b。
		t.Fatalf("unary minus operand should be power expression")
	}
}

// TestParserDoesNotCallBareTableConstructorAcrossLine 验证 table constructor 不会粘连下一行调用。
//
// Lua 5.3 的调用后缀只能接在 prefixexp 后；`local t = {}` 后面的 `(function...)(1)`
// 必须作为独立 IIFE 语句解析，不能被误归约为调用 `{}`。
func TestParserDoesNotCallBareTableConstructorAcrossLine(t *testing.T) {
	parser := New("local t = {}\n(function (a) t[a] = 10 end)(1)\n")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 官方 attrib.lua 中的 IIFE 形态必须可解析。
		t.Fatalf("parse table constructor followed by IIFE failed: %v", err)
	}
	if len(chunk.Block.Statements) != 2 {
		// local 声明和 IIFE 必须是两条独立语句。
		t.Fatalf("unexpected statement count=%d", len(chunk.Block.Statements))
	}
	localStatement, ok := chunk.Block.Statements[0].(*LocalAssignmentStatement)
	if !ok {
		// 第一条语句必须保持 local t。
		t.Fatalf("first statement should be local assignment")
	}
	if _, ok := localStatement.Values[0].(*TableConstructorExpression); !ok {
		// local t 的初始化值必须是裸 table constructor，而不是 function call。
		t.Fatalf("local initializer should be table constructor, got %T", localStatement.Values[0])
	}
	callStatement, ok := chunk.Block.Statements[1].(*FunctionCallStatement)
	if !ok {
		// 第二条语句必须是 IIFE 调用。
		t.Fatalf("second statement should be function call")
	}
	callExpression, ok := callStatement.Call.(*FunctionCallExpression)
	if !ok {
		// IIFE 应归约为普通 FunctionCallExpression。
		t.Fatalf("IIFE should be function call expression")
	}
	if _, ok := callExpression.Function.(*PrefixExpression); !ok {
		// 被调用函数必须来自括号表达式，保留 Lua prefixexp 语义。
		t.Fatalf("IIFE function should be prefix expression, got %T", callExpression.Function)
	}
}

// TestParserScopeLocalLifetimeAndGotoValidation 验证作用域、局部变量生命周期和 goto/label 校验。
//
// 该用例覆盖 Parser 剩余的作用域栈、local 生命周期和 goto/label 合法性任务。
func TestParserScopeLocalLifetimeAndGotoValidation(t *testing.T) {
	parser := New("local a = 1 ::again:: a = a + 1 goto again")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// 合法 label 和向后 goto 不应触发语义错误。
		t.Fatalf("parse scope failed: %v", err)
	}
	if chunk.Block.Scope == nil {
		// 顶层 block 必须挂载作用域信息。
		t.Fatalf("top scope is nil")
	}
	if chunk.Block.Scope != &chunk.Block.inlineScope {
		// 顶层 block 的作用域应复用 block 内嵌 scope 槽。
		t.Fatalf("top scope should use inline slot")
	}
	if chunk.Block.Scope.ID != 0 || chunk.Block.Scope.ParentID != -1 || chunk.Block.Scope.Depth != 0 {
		// 顶层作用域应使用稳定的根作用域元信息。
		t.Fatalf("unexpected top scope=%+v", chunk.Block.Scope)
	}
	if len(chunk.Block.Scope.Locals) != 1 {
		// 顶层应记录 local a。
		t.Fatalf("unexpected local count=%d", len(chunk.Block.Scope.Locals))
	}
	localInfo := chunk.Block.Scope.Locals[0]
	if localInfo.Name != "a" || localInfo.StartStatement != 0 || localInfo.EndStatement != len(chunk.Block.Statements) {
		// local a 生命周期应从声明语句持续到 block 结束。
		t.Fatalf("unexpected local info=%+v", localInfo)
	}
	if len(chunk.Block.Scope.Labels) != 1 || chunk.Block.Scope.Labels[0].Name != "again" {
		// label again 应登记到当前作用域。
		t.Fatalf("unexpected labels=%+v", chunk.Block.Scope.Labels)
	}
	if len(chunk.Block.Scope.Gotos) != 1 || chunk.Block.Scope.Gotos[0].Name != "again" {
		// goto again 应登记到当前作用域。
		t.Fatalf("unexpected gotos=%+v", chunk.Block.Scope.Gotos)
	}
}

// TestFunctionNamespaceUsesInlineScopeStack 验证函数命名空间优先复用内嵌作用域栈。
//
// 普通函数常见只有一个顶层 block；超过一层时仍必须能按普通切片语义扩展。
func TestFunctionNamespaceUsesInlineScopeStack(t *testing.T) {
	namespace := newFunctionNamespace()
	firstScope := &ScopeInfo{ID: 1}
	secondScope := &ScopeInfo{ID: 2}

	namespace.pushScope(firstScope)
	if len(namespace.scopeStack) != 1 || namespace.scopeStack[0] != firstScope {
		// 首个作用域必须成功压入命名空间栈。
		t.Fatalf("unexpected first scope stack=%+v", namespace.scopeStack)
	}
	if &namespace.scopeStack[0] != &namespace.inlineScopeStack[0] {
		// 单层作用域应复用结构体内嵌槽，避免普通函数额外分配底层数组。
		t.Fatalf("first scope should use inline slot")
	}

	namespace.pushScope(secondScope)
	if len(namespace.scopeStack) != 2 || namespace.scopeStack[0] != firstScope || namespace.scopeStack[1] != secondScope {
		// 多层作用域仍必须保持普通栈顺序。
		t.Fatalf("unexpected expanded scope stack=%+v", namespace.scopeStack)
	}
	namespace.popScope()
	namespace.popScope()
	if len(namespace.scopeStack) != 0 {
		// 弹空后栈长度必须回到零，便于后续复用同一命名空间。
		t.Fatalf("scope stack should be empty, got=%+v", namespace.scopeStack)
	}
}

// TestSemanticAnalyzerBorrowsInlineFunctionNamespaces 验证语义分析器复用常见函数命名空间槽。
//
// 顶层 chunk 和普通子函数应使用 analyzer 内嵌槽；深层嵌套函数仍允许回退普通临时命名空间。
func TestSemanticAnalyzerBorrowsInlineFunctionNamespaces(t *testing.T) {
	analyzer := &semanticAnalyzer{}

	topNamespace := analyzer.borrowFunctionNamespace()
	if topNamespace != &analyzer.inlineFunctionNamespaces[0] {
		// 顶层 chunk 应复用第一个内嵌命名空间槽。
		t.Fatalf("top namespace should use first inline slot")
	}
	childNamespace := analyzer.borrowFunctionNamespace()
	if childNamespace != &analyzer.inlineFunctionNamespaces[1] {
		// 一层子函数应复用第二个内嵌命名空间槽。
		t.Fatalf("child namespace should use second inline slot")
	}
	deepNamespace := analyzer.borrowFunctionNamespace()
	if deepNamespace == &analyzer.inlineFunctionNamespaces[0] || deepNamespace == &analyzer.inlineFunctionNamespaces[1] {
		// 更深嵌套必须使用独立命名空间，避免覆盖仍在使用的父函数槽。
		t.Fatalf("deep namespace should not reuse active inline slots")
	}

	deepNamespace.gotos = append(deepNamespace.gotos, gotoRecord{})
	analyzer.releaseFunctionNamespace(deepNamespace)
	childNamespace.labels = map[string][]labelRecord{"x": nil}
	analyzer.releaseFunctionNamespace(childNamespace)
	if analyzer.inlineFunctionNamespaces[1].labels != nil {
		// 归还时必须清空内部引用，避免下一函数观察到旧 label。
		t.Fatalf("child namespace should be cleared after release")
	}
	analyzer.releaseFunctionNamespace(topNamespace)
	if analyzer.functionNamespaceDepth != 0 {
		// 所有命名空间归还后深度必须回到零。
		t.Fatalf("unexpected namespace depth=%d", analyzer.functionNamespaceDepth)
	}

	reusedNamespace := analyzer.borrowFunctionNamespace()
	if reusedNamespace != &analyzer.inlineFunctionNamespaces[0] {
		// 新一轮分析应从顶层槽重新开始复用。
		t.Fatalf("reused namespace should use first inline slot")
	}
	analyzer.releaseFunctionNamespace(reusedNamespace)
}

// TestParserAggregatesSemanticErrors 验证 parser 语义错误聚合策略。
//
// 当前错误恢复策略针对作用域语义阶段：尽量收集多个 goto/label 错误并一次返回。
func TestParserAggregatesSemanticErrors(t *testing.T) {
	parser := New("goto missing goto other")

	_, err := parser.ParseChunk()
	if err == nil {
		// 未定义 label 必须报错。
		t.Fatalf("expected semantic errors")
	}
	parseErrors, ok := err.(ParseErrorList)
	if !ok {
		// 语义阶段应返回结构化聚合错误。
		t.Fatalf("expected ParseErrorList, got %T", err)
	}
	if len(parseErrors) != 2 {
		// 两个未定义 goto 目标应一次性返回。
		t.Fatalf("unexpected semantic error count=%d", len(parseErrors))
	}
	if !strings.Contains(parseErrors.Error(), "undefined label 'missing'") || !strings.Contains(parseErrors.Error(), "undefined label 'other'") {
		// 聚合错误文本应包含两个目标 label 名称。
		t.Fatalf("unexpected semantic error text=%q", parseErrors.Error())
	}
}

// TestParserSemanticErrorsMatchGolden 验证 parser 语义错误位置与 golden 保持一致。
//
// 该测试把未定义 goto label 的错误文本固定下来，用于后续迁移 Lua 5.3 parser 时观察位置语义漂移。
func TestParserSemanticErrorsMatchGolden(t *testing.T) {
	// 使用两个未定义 goto 目标触发语义阶段聚合错误。
	parser := New("goto missing goto other")

	_, err := parser.ParseChunk()
	if err == nil {
		// 未定义 label 必须产生错误，否则 golden 对比没有意义。
		t.Fatalf("expected semantic errors")
	}
	goldenBytes, readErr := os.ReadFile(filepath.Join("..", "..", "tests", "golden", "parser_error_positions.golden"))
	if readErr != nil {
		// golden 文件是测试基线，读取失败说明仓库测试资产不完整。
		t.Fatalf("read parser golden failed: %v", readErr)
	}

	expectedText := strings.TrimRight(string(goldenBytes), "\n")
	actualText := strings.TrimRight(err.Error(), "\n")
	if actualText != expectedText {
		// 错误位置或文本变化时必须显式更新迁移基线。
		t.Fatalf("parser semantic errors mismatch:\n got:\n%s\nwant:\n%s", actualText, expectedText)
	}
}

// TestParserRejectsGotoIntoLocalScope 验证 goto 不能向前跳入 local 生命周期。
//
// Lua 5.3 禁止 goto 越过局部变量声明到达后续 label。
func TestParserRejectsGotoIntoLocalScope(t *testing.T) {
	parser := New("goto done local secret = 1 ::done:: print(secret)")

	_, err := parser.ParseChunk()
	if err == nil {
		// goto 跳入 local secret 的作用域必须报错。
		t.Fatalf("expected goto local scope error")
	}
	if !strings.Contains(err.Error(), "jumps into scope of local 'secret'") {
		// 错误文本应明确指出被跳入生命周期的 local 名称。
		t.Fatalf("unexpected error=%v", err)
	}
}

// TestParserAllowsGotoToLabelAfterClosedLocalScope 验证 label 位于已关闭 local 作用域之后的场景。
//
// Lua 5.3 的 label 是空语句；当 local 后存在一个回跳到 local 声明前的 goto 时，后续 label
// 不再处于该 local 生命周期内。官方 closure.lua 的 `l4a/l4/l4b` 小节依赖该规则。
func TestParserAllowsGotoToLabelAfterClosedLocalScope(t *testing.T) {
	source := `
do
  local t
  goto l4
  ::l4a:: t = 1; goto l4b
  ::l4::
  local y = 2
  t = function () return y end
  goto l4a
  ::l4b::
end`
	parser := New(source)
	if _, err := parser.ParseChunk(); err != nil {
		// 官方 closure.lua 同形态结构必须通过语义校验。
		t.Fatalf("parse closure goto pattern failed: %v", err)
	}
}

// TestParserGolden 验证 parser AST 和作用域摘要的稳定 golden 输出。
//
// golden 覆盖基础 AST 节点、scope locals、label 和 goto 元信息。
func TestParserGolden(t *testing.T) {
	parser := New("local x = 1\n::done::\ncall(x)\n")

	chunk, err := parser.ParseChunk()
	if err != nil {
		// golden 输入必须是合法 Lua 子集。
		t.Fatalf("parse golden input failed: %v", err)
	}
	got := parserGoldenText(chunk)
	goldenPath := filepath.Join("..", "..", "tests", "golden", "parser_ast.golden")
	expectedBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		// golden 文件缺失表示测试资产不完整。
		t.Fatalf("read parser golden failed: %v", err)
	}
	if got != string(expectedBytes) {
		// AST 摘要必须与 golden 完全一致。
		t.Fatalf("parser golden mismatch:\n got:\n%s\nwant:\n%s", got, string(expectedBytes))
	}
}

// parserGoldenText 返回 parser AST 的稳定文本摘要。
//
// 输出只覆盖当前 parser 已承诺的节点和作用域字段，避免未来未实现字段造成噪音。
func parserGoldenText(chunk *Chunk) string {
	var builder strings.Builder
	builder.WriteString("chunk@")
	builder.WriteString(positionText(chunk.Position))
	builder.WriteString("\n")
	appendBlockGolden(&builder, chunk.Block, "")

	// 返回完整 golden 文本。
	return builder.String()
}

// appendBlockGolden 追加 block 的 golden 摘要。
//
// indent 用于展示嵌套结构；当前 golden 只使用顶层 block，但 helper 保持递归扩展能力。
func appendBlockGolden(builder *strings.Builder, block *Block, indent string) {
	builder.WriteString(indent)
	builder.WriteString("block scope=")
	builder.WriteString(intToString(block.Scope.ID))
	builder.WriteString(" parent=")
	builder.WriteString(intToString(block.Scope.ParentID))
	builder.WriteString(" depth=")
	builder.WriteString(intToString(block.Scope.Depth))
	builder.WriteString(" statements=")
	builder.WriteString(intToString(block.Scope.StatementCount))
	builder.WriteString("\n")
	for _, localInfo := range block.Scope.Locals {
		// 局部变量输出名称和生命周期区间。
		builder.WriteString(indent)
		builder.WriteString("local ")
		builder.WriteString(localInfo.Name)
		builder.WriteString(" [")
		builder.WriteString(intToString(localInfo.StartStatement))
		builder.WriteString(",")
		builder.WriteString(intToString(localInfo.EndStatement))
		builder.WriteString("]\n")
	}
	for _, labelInfo := range block.Scope.Labels {
		// label 输出名称和语句下标。
		builder.WriteString(indent)
		builder.WriteString("label ")
		builder.WriteString(labelInfo.Name)
		builder.WriteString(" @")
		builder.WriteString(intToString(labelInfo.StatementIndex))
		builder.WriteString("\n")
	}
	for _, gotoInfo := range block.Scope.Gotos {
		// goto 输出目标名称和语句下标。
		builder.WriteString(indent)
		builder.WriteString("goto ")
		builder.WriteString(gotoInfo.Name)
		builder.WriteString(" @")
		builder.WriteString(intToString(gotoInfo.StatementIndex))
		builder.WriteString("\n")
	}
	for statementIndex, statement := range block.Statements {
		// 语句输出保持源码顺序。
		builder.WriteString(indent)
		builder.WriteString("stmt")
		builder.WriteString(intToString(statementIndex))
		builder.WriteString(" ")
		builder.WriteString(statementGoldenName(statement))
		builder.WriteString("\n")
	}
}

// statementGoldenName 返回语句节点的稳定名称。
//
// 未覆盖的节点统一返回 unknown，提醒测试扩展时补充 golden 映射。
func statementGoldenName(statement Statement) string {
	switch typedStatement := statement.(type) {
	case *LocalAssignmentStatement:
		// local 赋值展示声明名称列表。
		return "local " + strings.Join(typedStatement.Names, ",")
	case *LabelStatement:
		// label 展示标签名。
		return "label " + typedStatement.Name
	case *FunctionCallStatement:
		// 函数调用语句展示通用 call 名称。
		return "call"
	default:
		return "unknown"
	}
}

// positionText 返回源码位置的稳定展示。
//
// golden 只输出行列，避免字节偏移受 UTF-8 输入细节影响阅读。
func positionText(position lexer.Position) string {
	// 使用十进制行列格式对齐 parser 错误输出。
	return intToString(position.Line) + ":" + intToString(position.Column)
}

// intToString 返回整数十进制文本。
//
// 测试 golden 统一通过该 helper 格式化整数，避免散落 strconv 调用。
func intToString(value int) string {
	// strconv.Itoa 返回稳定十进制表示。
	return strconv.Itoa(value)
}
