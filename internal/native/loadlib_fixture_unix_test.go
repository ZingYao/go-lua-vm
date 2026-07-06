//go:build native_modules && (linux || darwin)

package native

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	luaruntime "github.com/zing/go-lua-vm/runtime"
	packagelib "github.com/zing/go-lua-vm/stdlib/package"
)

// TestUnixPackageLoadLibResolvesNativeFixture 验证 package.loadlib 可解析真实 Lua C 模块入口。
func TestUnixPackageLoadLibResolvesNativeFixture(t *testing.T) {
	// 构建最小 Lua C 模块 fixture，只导出 luaopen_*，不调用任何 Lua C API。
	fixturePath := buildUnixNativeFixture(t)
	environment := packagelib.NewEnvironmentWithLoaders(nil, Loader())

	values, err := environment.LoadLib(
		luaruntime.StringValue(fixturePath),
		luaruntime.StringValue("luaopen_glua_native_smoke"),
	)
	if err != nil {
		// 合法 loadlib 参数和 native loader 失败边界必须通过三返回表达，不应抛 Go error。
		t.Fatalf("LoadLib returned error: %v", err)
	}
	if len(values) != 3 || !values[0].IsNil() || values[2].String != "init" {
		// 当前尚未实现 C API shim，因此解析成功后应停在 init 分类，而不是返回 callable。
		t.Fatalf("LoadLib fixture values = %#v, want nil,message,init", values)
	}
	if !strings.Contains(values[1].String, "shim is not implemented") {
		// 错误文本必须明确已经越过动态库解析，阻塞点是 Lua C API shim。
		t.Fatalf("LoadLib fixture message = %q, want shim boundary", values[1].String)
	}
}

// buildUnixNativeFixture 构建只导出 luaopen_glua_native_smoke 的最小 Lua C 动态库。
func buildUnixNativeFixture(t *testing.T) string {
	// native_modules 测试依赖 C 编译器；若环境没有可用编译器则明确跳过。
	t.Helper()
	cc := os.Getenv("CC")
	if cc == "" {
		// 未显式指定 CC 时使用系统默认 cc。
		cc = "cc"
	}
	if _, err := exec.LookPath(cc); err != nil {
		// 没有编译器无法构建 fixture，属于环境缺失而不是 Go 逻辑失败。
		t.Skipf("C compiler %q not found: %v", cc, err)
	}

	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "glua_native_smoke.c")
	source := `
#include "lua.h"

int luaopen_glua_native_smoke(lua_State *L) {
	(void)L;
	return 0;
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		// 写入临时 C 源码失败说明测试环境不可用。
		t.Fatalf("write native fixture source failed: %v", err)
	}

	includeDir := filepath.Join(repoRootFromTest(t), "native", "lua53", "include")
	outputPath := filepath.Join(tempDir, "glua_native_smoke"+dynamicLibraryExtension())
	args := nativeFixtureCompileArgs(includeDir, outputPath, sourcePath)
	command := exec.Command(cc, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		// fixture 编译失败需要暴露完整命令和输出，便于区分编译器差异与头文件路径错误。
		t.Fatalf("compile native fixture failed: %v\n%s %s\n%s", err, cc, strings.Join(args, " "), string(output))
	}
	return outputPath
}

// repoRootFromTest 返回当前测试文件所在仓库根目录。
func repoRootFromTest(t *testing.T) string {
	// 使用 runtime.Caller 定位源码文件，避免依赖 go test 的当前工作目录。
	t.Helper()
	_, filename, _, ok := goruntime.Caller(0)
	if !ok {
		// 无法定位测试文件时无法稳定找到 native/lua53/include。
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

// nativeFixtureCompileArgs 返回当前 Unix 平台构建动态库 fixture 的编译参数。
func nativeFixtureCompileArgs(includeDir string, outputPath string, sourcePath string) []string {
	// 根据平台选择动态库输出参数，保持 fixture 只导出 luaopen_*。
	args := []string{"-I", includeDir, "-o", outputPath}
	switch goruntime.GOOS {
	case "darwin":
		// macOS 使用 dynamiclib，当前 fixture 不依赖外部 Lua 符号。
		args = append(args, "-dynamiclib")
	default:
		// Linux 使用 shared + fPIC，满足 dlopen 对共享对象的要求。
		args = append(args, "-shared", "-fPIC")
	}
	return append(args, sourcePath)
}

// dynamicLibraryExtension 返回当前 Unix 平台测试 fixture 的动态库后缀。
func dynamicLibraryExtension() string {
	// 后缀与平台 loader 预期一致，后续 cpath fixture 可复用。
	if goruntime.GOOS == "darwin" {
		// macOS 首轮 fixture 使用 dylib；后续单独覆盖 so 后缀候选。
		return ".dylib"
	}
	return ".so"
}

// TestUnixPackageLoadLibMissingFixtureSymbol 验证真实 fixture 缺失符号仍保持 init 分类。
func TestUnixPackageLoadLibMissingFixtureSymbol(t *testing.T) {
	// 使用真实动态库验证 package.loadlib 的错误三返回，而不只测试底层 dlsym。
	fixturePath := buildUnixNativeFixture(t)
	environment := packagelib.NewEnvironmentWithLoaders(nil, Loader())

	values, err := environment.LoadLib(
		luaruntime.StringValue(fixturePath),
		luaruntime.StringValue("luaopen_glua_native_missing"),
	)
	if err != nil {
		// 动态库符号缺失必须通过 loadlib 三返回表达。
		t.Fatalf("LoadLib missing symbol returned error: %v", err)
	}
	if len(values) != 3 || !values[0].IsNil() || values[2].String != "init" {
		// 缺失 luaopen_* 符号属于初始化失败分类。
		t.Fatalf("LoadLib missing symbol values = %#v, want nil,message,init", values)
	}
	if !strings.Contains(values[1].String, "luaopen_glua_native_missing") {
		// 错误文本应包含缺失符号名，方便 require 诊断。
		t.Fatalf("LoadLib missing symbol message = %q", values[1].String)
	}
	_, loaderErr := Loader()(fixturePath, "luaopen_glua_native_missing")
	var dynamicErr packagelib.DynamicLibraryError
	if !errors.As(loaderErr, &dynamicErr) {
		// 直接 loader 路径同样要保持 DynamicLibraryError 分类，避免 package.loadlib 只能归类 open。
		t.Fatalf("direct Loader missing symbol did not return DynamicLibraryError")
	}
	if dynamicErr.Category != "init" {
		// 直接 loader 与 package.loadlib 分类必须一致。
		t.Fatalf("direct Loader missing symbol category = %q, want init", dynamicErr.Category)
	}
}

// TestUnixNativeLuaPushCClosureCallsResolvedFixture 验证解析出的 C 函数可包装为 Go closure。
func TestUnixNativeLuaPushCClosureCallsResolvedFixture(t *testing.T) {
	// fixture 的 luaopen_* 入口是符合 lua_CFunction ABI 的真实动态库符号，当前返回 0 个结果。
	fixturePath := buildUnixNativeFixture(t)
	library, err := openDynamicLibrary(fixturePath)
	if err != nil {
		// fixture 已构建成功，打开失败说明动态库 loader 退化。
		t.Fatalf("open native fixture failed: %v", err)
	}
	defer func() {
		if closeErr := library.close(); closeErr != nil {
			// 动态库关闭失败需要暴露，避免隐藏句柄生命周期问题。
			t.Fatalf("close native fixture failed: %v", closeErr)
		}
	}()
	symbol, err := library.lookupSymbol("luaopen_glua_native_smoke")
	if err != nil {
		// 符号缺失会使 C function wrapper 无法验收。
		t.Fatalf("lookup native fixture symbol failed: %v", err)
	}

	state := luaruntime.NewState()
	defer state.Close()
	handle, err := newNativeStateHandle(state)
	if err != nil {
		// handle 创建失败说明 State 映射不可用，无法验证 C function wrapper。
		t.Fatalf("newNativeStateHandle failed: %v", err)
	}
	defer handle.close()
	luaState := handle.pointer()

	nativeLuaPushCClosure(luaState, symbol, 0)
	closureValue := state.ValueAt(-1)
	if closureValue.Kind != luaruntime.KindGoClosure {
		// C function 必须被包装成当前 VM 可调用的 Go closure。
		t.Fatalf("native C closure value = %#v, want Go closure", closureValue)
	}
	closure, ok := closureValue.Ref.(luaruntime.GoResultsFunction)
	if !ok || closure == nil {
		// 当前 wrapper 使用 GoResultsFunction 承接多返回值语义。
		t.Fatalf("native C closure payload = %#v, want GoResultsFunction", closureValue.Ref)
	}
	results, err := closure(luaruntime.IntegerValue(1), luaruntime.StringValue("arg"))
	if err != nil {
		// fixture 返回 0，调用不应产生错误。
		t.Fatalf("native C closure call failed: %v", err)
	}
	if len(results) != 0 {
		// fixture luaopen_* 返回 0 个结果，wrapper 应保持空结果。
		t.Fatalf("native C closure results = %#v, want empty", results)
	}
	if got := nativeLuaStackTop(luaState); got != 1 {
		// 调用期间临时压入的参数必须在返回后恢复，只保留原本压入的 closure。
		t.Fatalf("native C closure top after call = %d, want 1", got)
	}
}

// TestDynamicLibraryExtensionDocumentsFixtureSuffix 验证 fixture 后缀选择保持平台可读。
func TestDynamicLibraryExtensionDocumentsFixtureSuffix(t *testing.T) {
	// 该测试锁定后缀策略，避免后续 cpath fixture 与构建脚本产生分歧。
	extension := dynamicLibraryExtension()
	if extension != ".so" && extension != ".dylib" {
		// Unix 平台只应出现 .so 或 .dylib。
		t.Fatalf("dynamic library extension = %q", extension)
	}
	if goruntime.GOOS == "darwin" && extension != ".dylib" {
		// macOS 首轮 fixture 使用 dylib。
		t.Fatalf("darwin extension = %q, want .dylib", extension)
	}
	if goruntime.GOOS != "darwin" && extension != ".so" {
		// Linux 首轮 fixture 使用 so。
		t.Fatalf("%s extension = %q, want .so", goruntime.GOOS, extension)
	}
}
