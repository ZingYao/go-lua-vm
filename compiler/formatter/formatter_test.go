package formatter

import (
	"testing"

	"github.com/zing/go-lua-vm/extensions"
)

// TestFormatSwitchContinueExtensions 验证 formatter 支持项目扩展语法。
func TestFormatSwitchContinueExtensions(t *testing.T) {
	if !extensions.Default().Has(extensions.SyntaxContinue | extensions.SyntaxSwitch) {
		// lua53 构建不包含扩展语法，扩展 formatter 用例在该模式下不执行。
		t.Skip("syntax extensions are not compiled")
	}
	source := "local a={1,2}\nwhile true do\nif a[1]==1 then\ncontinue\nend\nswitch a[1] do\ncase 1,2\nprint('one')\ndefault\nprint(\"other\")\nend\nend\n"
	got, err := Format(source, extensions.Default())
	if err != nil {
		// 默认扩展语法下 switch/continue 应可格式化。
		t.Fatalf("Format failed: %v", err)
	}
	want := "local a = {1, 2}\n" +
		"while true do\n" +
		"  if a[1] == 1 then\n" +
		"    continue\n" +
		"  end\n" +
		"  switch a[1] do\n" +
		"    case 1, 2\n" +
		"      print('one')\n" +
		"    default\n" +
		"      print(\"other\")\n" +
		"  end\n" +
		"end\n"
	if got != want {
		// 格式化输出必须稳定，便于 CLI 和编辑器集成。
		t.Fatalf("formatted mismatch:\n%s\nwant:\n%s", got, want)
	}
}

// TestFormatRejectsDisabledExtensions 验证关闭扩展后 formatter 会拒绝语法糖。
func TestFormatRejectsDisabledExtensions(t *testing.T) {
	if _, err := Format("while true do continue end", extensions.None()); err == nil {
		// lua53 模式下 continue 不能被格式化吞掉。
		t.Fatalf("Format should reject disabled continue syntax")
	}
}

// TestFormatPreservesCommentsAndStrings 验证 formatter 不破坏短注释与字符串字面量。
func TestFormatPreservesCommentsAndStrings(t *testing.T) {
	source := "local s='a--b'-- comment\nprint(s)\n"
	got, err := Format(source, extensions.Default())
	if err != nil {
		// 合法源码不应格式化失败。
		t.Fatalf("Format failed: %v", err)
	}
	want := "local s = 'a--b' -- comment\nprint(s)\n"
	if got != want {
		// 字符串内部的 -- 不能被误判为注释。
		t.Fatalf("formatted mismatch: %q want %q", got, want)
	}
}

// TestFormatUnaryLengthOperatorNoSpace 验证 # 运算符格式化后不与操作数留空格。
func TestFormatUnaryLengthOperatorNoSpace(t *testing.T) {
	source := "print(# a)\nlocal b = # c\n"
	got, err := Format(source, extensions.Default())
	if err != nil {
		// 长度运算应兼容标准语法并可被 formatter 处理。
		t.Fatalf("Format failed: %v", err)
	}
	want := "print(#a)\nlocal b = #c\n"
	if got != want {
		// 一元长度符号是紧贴操作数的风格。
		t.Fatalf("formatted mismatch: %q want %q", got, want)
	}
}
