package stdlib_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zing/go-lua-vm/lua"
	"github.com/zing/go-lua-vm/runtime"
)

// TestStandardLibraryExportsGolden 验证标准库关键导出面与 golden 保持一致。
//
// 该 golden 只检查 OpenLibs 后关键全局、库表和函数类型，不依赖 Lua closure 执行器，适合作为
// 当前阶段标准库注册面的稳定验收。
func TestStandardLibraryExportsGolden(t *testing.T) {
	// 创建 State 并打开全部标准库，随后生成导出摘要。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库打开失败说明注册入口不可用。
		t.Fatalf("OpenLibs failed: %v", err)
	}

	got := standardLibraryGolden(state)
	goldenPath := filepath.Join("..", "tests", "golden", "stdlib_exports.golden")
	expectedBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		// golden 文件缺失会让标准库导出面失去稳定基线。
		t.Fatalf("read stdlib golden failed: %v", err)
	}
	expected := strings.TrimRight(string(expectedBytes), "\n")
	if got != expected {
		// 标准库导出面变化必须经过兼容性确认后更新 golden。
		t.Fatalf("stdlib exports golden mismatch:\n got:\n%s\nwant:\n%s", got, expected)
	}
}

// standardLibraryGolden 返回当前 State 标准库导出摘要。
//
// state 必须已调用 lua.OpenLibs；返回文本按固定库和字段顺序输出，避免 map 迭代顺序影响 golden。
func standardLibraryGolden(state *lua.State) string {
	// 使用 strings.Builder 累积稳定文本。
	var builder strings.Builder
	appendGlobalGolden(&builder, state, []string{
		"_G", "_VERSION", "assert", "pcall", "xpcall", "type", "tostring", "tonumber",
		"coroutine", "table", "string", "math", "io", "os", "package", "utf8", "debug",
	})
	appendLibraryGolden(&builder, state, "coroutine", []string{"create", "resume", "running", "status", "wrap", "yield"})
	appendLibraryGolden(&builder, state, "table", []string{"concat", "insert", "move", "pack", "remove", "sort", "unpack"})
	appendLibraryGolden(&builder, state, "string", []string{"byte", "char", "dump", "find", "format", "gmatch", "gsub", "len", "lower", "match", "pack", "packsize", "rep", "reverse", "sub", "unpack", "upper"})
	appendLibraryGolden(&builder, state, "math", []string{"abs", "acos", "asin", "atan", "ceil", "cos", "deg", "exp", "floor", "fmod", "huge", "log", "max", "maxinteger", "min", "mininteger", "modf", "pi", "rad", "random", "randomseed", "sin", "sqrt", "tan", "tointeger", "type", "ult"})
	appendLibraryGolden(&builder, state, "io", []string{"close", "flush", "input", "lines", "open", "output", "popen", "read", "tmpfile", "type", "write", "stdin", "stdout", "stderr"})
	appendLibraryGolden(&builder, state, "os", []string{"clock", "date", "difftime", "execute", "exit", "getenv", "remove", "rename", "setlocale", "time", "tmpname"})
	appendLibraryGolden(&builder, state, "package", []string{"config", "cpath", "loaded", "loadlib", "path", "preload", "searchers", "searchpath"})
	appendLibraryGolden(&builder, state, "utf8", []string{"char", "charpattern", "codes", "codepoint", "len", "offset"})
	appendLibraryGolden(&builder, state, "debug", []string{"debug", "gethook", "getinfo", "getlocal", "getmetatable", "getregistry", "getupvalue", "getuservalue", "sethook", "setlocal", "setmetatable", "setupvalue", "setuservalue", "traceback", "upvalueid", "upvaluejoin"})
	return strings.TrimRight(builder.String(), "\n")
}

// appendGlobalGolden 追加全局变量类型摘要。
//
// names 必须按 golden 期望顺序传入；缺失字段会输出 nil，便于定位注册缺口。
func appendGlobalGolden(builder *strings.Builder, state *lua.State, names []string) {
	for _, name := range names {
		// 每个全局都按固定顺序读取。
		value, err := lua.GetGlobal(state, name)
		if err != nil {
			// GetGlobal 理论上不会失败；若失败则写入 error 便于 golden 暴露。
			builder.WriteString("global." + name + "=error\n")
			continue
		}
		builder.WriteString("global." + name + "=" + valueKindName(value) + "\n")
	}
}

// appendLibraryGolden 追加库表字段类型摘要。
//
// libraryName 必须是全局库表；fields 按固定顺序输出，缺失字段会输出 nil。
func appendLibraryGolden(builder *strings.Builder, state *lua.State, libraryName string, fields []string) {
	// 先读取库表，库表不存在时输出缺失并跳过字段展开。
	libraryValue, err := lua.GetGlobal(state, libraryName)
	if err != nil {
		// GetGlobal 失败说明 State 不可用，输出 error 便于定位。
		builder.WriteString(libraryName + "=error\n")
		return
	}
	libraryTable, ok := libraryValue.Ref.(*runtime.Table)
	if libraryValue.Kind != runtime.KindTable || !ok {
		// 非 table 说明 OpenLibs 注册面不符合标准库约定。
		builder.WriteString(libraryName + "=" + valueKindName(libraryValue) + "\n")
		return
	}
	for _, field := range fields {
		// 每个字段按固定顺序 raw 读取，避免元方法影响 golden。
		value := libraryTable.RawGetString(field)
		builder.WriteString(libraryName + "." + field + "=" + valueKindName(value) + "\n")
	}
}

// valueKindName 返回 golden 使用的 Lua 值类型名称。
//
// 返回值只描述基础类型，不展开具体函数指针、table identity 或 userdata identity，保证文本稳定。
func valueKindName(value runtime.Value) string {
	// 根据 ValueKind 映射到稳定文本。
	switch value.Kind {
	case runtime.KindNil:
		// nil 表示字段不存在或显式 nil。
		return "nil"
	case runtime.KindBoolean:
		// boolean 只记录类型，不记录具体 true/false。
		return "boolean"
	case runtime.KindInteger:
		// integer 与 number 分开，保留 Lua 5.3 双数字模型。
		return "integer"
	case runtime.KindNumber:
		// number 表示浮点 lua_Number。
		return "number"
	case runtime.KindString:
		// string 不记录具体内容，避免路径等环境差异。
		return "string"
	case runtime.KindTable:
		// table 只记录基础类型。
		return "table"
	case runtime.KindLuaClosure:
		// Lua closure 当前主要由编译器产生，标准库导出面通常不应出现。
		return "luaclosure"
	case runtime.KindGoClosure:
		// Go closure 表示标准库函数。
		return "gofunction"
	case runtime.KindUserdata:
		// userdata 用于 io file 等宿主对象。
		return "userdata"
	case runtime.KindThread:
		// thread 表示 coroutine 对象。
		return "thread"
	default:
		// 未知类型说明 runtime 扩展后 golden helper 尚未更新。
		return "unknown"
	}
}
