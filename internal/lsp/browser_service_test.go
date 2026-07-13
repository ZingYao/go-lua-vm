package lsp

import "testing"

// TestBrowserDiagnostics 验证浏览器诊断复用 GLua parser 与 codegen。
func TestBrowserDiagnostics(t *testing.T) {
	// 同时覆盖合法源码与缺失 end 的语法错误。
	if diagnostics := BrowserDiagnostics("local value = 1\n"); len(diagnostics) != 0 {
		// 合法源码出现诊断说明浏览器与桌面语法边界发生漂移。
		t.Fatalf("valid diagnostics = %#v", diagnostics)
	}
	diagnostics := BrowserDiagnostics("if true then\n  print('x')\n")
	if len(diagnostics) == 0 {
		// 缺失 end 必须生成至少一条错误，供 Monaco 显示红线。
		t.Fatal("missing syntax diagnostic")
	}
}

// TestBrowserCompletions 验证浏览器补齐包含内置和当前文件符号。
func TestBrowserCompletions(t *testing.T) {
	// 在第二行光标处请求 pri 前缀，预期同时能复用桌面补齐目录。
	source := "local privateValue = 1\npri"
	completions := BrowserCompletions(source, 1, 3)
	labels := make(map[string]bool, len(completions))
	var printCompletion BrowserCompletion
	for _, completion := range completions {
		// 收集标签后统一断言，避免依赖候选数量和后续目录扩展。
		labels[completion.Label] = true
		if completion.Label == "print" {
			// 保存函数候选，继续验证 snippet 和替换范围。
			printCompletion = completion
		}
	}
	if !labels["print"] || !labels["privateValue"] {
		// 内置 print 与光标前 local 都应进入同一次补齐结果。
		t.Fatalf("completion labels = %#v", labels)
	}
	if printCompletion.InsertTextFormat != 2 || printCompletion.InsertText != "print(${1:...})" {
		// Web 端必须像桌面扩展一样插入函数括号和参数占位符。
		t.Fatalf("print completion = %#v", printCompletion)
	}
	if printCompletion.Range.Start.Line != 1 || printCompletion.Range.Start.Character != 0 || printCompletion.Range.End.Character != 3 {
		// 已输入 pri 应被完整且只被当前候选替换。
		t.Fatalf("print replacement range = %#v", printCompletion.Range)
	}
}

// TestBrowserHoverAndDefinition 验证浏览器 hover 与当前文件定义跳转。
func TestBrowserHoverAndDefinition(t *testing.T) {
	// 内置 print 应返回完整 Markdown，局部变量使用桌面最近定义规则。
	if hover := BrowserHoverAt("print('x')", 0, 1); hover == nil || hover.Markdown == "" {
		// 缺失内置 hover 会让 Web 与桌面文档体验不一致。
		t.Fatalf("print hover = %#v", hover)
	}
	source := "local answer = 42\nprint(answer)\n"
	definition := BrowserDefinitionAt(source, 1, 8)
	if definition == nil || definition.Range.Start.Line != 0 {
		// 第二行 answer 必须跳到第一行 local 定义。
		t.Fatalf("answer definition = %#v", definition)
	}
}

// TestBrowserSemanticTokens 验证浏览器语义 token 使用合法五元组编码。
func TestBrowserSemanticTokens(t *testing.T) {
	// GLua const 和字符串应产生可被 Monaco 消费的非空 token 数据。
	tokens := BrowserSemanticTokens("const name = 'glua'\n")
	if len(tokens) == 0 || len(tokens)%5 != 0 {
		// LSP semantic token 数据必须按五整数分组。
		t.Fatalf("semantic tokens = %#v", tokens)
	}
}

// TestBrowserSemanticTokensMatchExtensionScopes 验证浏览器语义高亮对齐扩展的命名空间、方法、事件和属性分类。
func TestBrowserSemanticTokensMatchExtensionScopes(t *testing.T) {
	// 每个名称放在独立行，便于直接读取五元组中的 token 类型。
	source := "glua.event.setProgress()\nglua.event.progress_start\nvalue.field\nprint(value)\n"
	tokens := BrowserSemanticTokens(source)
	typesByLine := make(map[int][]int)
	line := 0
	for index := 0; index+4 < len(tokens); index += 5 {
		// delta line 需要累计后才能还原原始行号。
		line += tokens[index]
		typesByLine[line] = append(typesByLine[line], tokens[index+3])
	}
	if !containsSemanticType(typesByLine[0], semanticTypeNamespace) || !containsSemanticType(typesByLine[0], semanticTypeMethod) {
		// glua 根对象与 setProgress 调用必须分别显示命名空间和方法颜色。
		t.Fatalf("line 0 semantic types = %#v", typesByLine[0])
	}
	if !containsSemanticType(typesByLine[1], semanticTypeEvent) {
		// progress_start 应使用事件常量颜色。
		t.Fatalf("line 1 semantic types = %#v", typesByLine[1])
	}
	if !containsSemanticType(typesByLine[2], semanticTypeProperty) || !containsSemanticType(typesByLine[3], semanticTypeFunction) {
		// 普通成员和普通函数调用也必须与扩展 scope 对齐。
		t.Fatalf("property/function semantic types = %#v / %#v", typesByLine[2], typesByLine[3])
	}
}

// containsSemanticType 判断一行语义 token 类型中是否包含目标类型。
//
// types 是解码后的类型列表，target 是 semanticTokenTypes 下标；返回值仅供定向测试断言。
func containsSemanticType(types []int, target int) bool {
	// 线性扫描足以覆盖单行少量 token。
	for _, tokenType := range types {
		if tokenType == target {
			// 命中目标类型即可提前结束。
			return true
		}
	}
	return false
}

// TestBrowserWorkspaceLanguage 验证虚拟工作区 require 成员补齐与跨文件跳转。
func TestBrowserWorkspaceLanguage(t *testing.T) {
	// 使用未落盘源码模拟 OPFS 与 File System Access 工作区。
	files := map[string]string{
		"main.glua":      "local tools = require(\"lib.tools\")\ntools.gr",
		"lib/tools.glua": "local module = {}\nfunction module.greet(name)\n  return name\nend\nreturn module\n",
	}
	completions := BrowserWorkspaceCompletions(files["main.glua"], "main.glua", files, 1, 8)
	foundGreet := false
	for _, completion := range completions {
		// 工作区模块应暴露匹配前缀的导出函数。
		if completion.Label == "greet" {
			foundGreet = true
		}
	}
	if !foundGreet {
		// 缺少模块成员说明浏览器与桌面 LSP 的导出分析发生漂移。
		t.Fatalf("workspace completions = %#v, want greet", completions)
	}

	requireDefinition := BrowserWorkspaceDefinitionAt(files["main.glua"], "main.glua", files, 0, 25)
	if requireDefinition == nil || requireDefinition.Path != "lib/tools.glua" {
		// require 字符串必须跳到解析后的模块入口。
		t.Fatalf("require definition = %#v, want lib/tools.glua", requireDefinition)
	}
	memberSource := "local tools = require(\"lib.tools\")\ntools.greet()"
	memberDefinition := BrowserWorkspaceDefinitionAt(memberSource, "main.glua", files, 1, 8)
	if memberDefinition == nil || memberDefinition.Path != "lib/tools.glua" || memberDefinition.Range.Start.Line != 1 {
		// 模块成员必须跳到导出函数名称所在行。
		t.Fatalf("member definition = %#v, want lib/tools.glua line 1", memberDefinition)
	}
}

// TestResolveBrowserModuleRejectsTraversal 验证虚拟文件系统模块解析不会逃逸工作区。
func TestResolveBrowserModuleRejectsTraversal(t *testing.T) {
	// 即使 files 中构造了相似键，父目录模块名也必须被拒绝。
	files := map[string]string{"secret.glua": "return {}"}
	if resolved := resolveBrowserModule("../secret", "main.glua", files); resolved != "" {
		// 路径穿越会突破用户授权目录边界，因此测试立即失败。
		t.Fatalf("resolved traversal module to %q", resolved)
	}
}
