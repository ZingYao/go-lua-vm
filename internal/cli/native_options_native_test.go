//go:build native_modules

package cli

import (
	"testing"

	"github.com/ZingYao/go-lua-vm/lua"
	"github.com/ZingYao/go-lua-vm/runtime"
)

// TestApplyNativeModuleOptionsInjectsLoaders 验证 native_modules 构建会启用 CLI native loader。
func TestApplyNativeModuleOptionsInjectsLoaders(t *testing.T) {
	// native CLI 构建需要自动接入 package.loadlib/searcher 使用的 loader。
	options := applyNativeModuleOptions(lua.DefaultOptions())
	if options.PackageDynamicLibraryLoader == nil {
		// 无状态 loader 缺失会让 package.loadlib 直接回到禁用策略。
		t.Fatalf("native PackageDynamicLibraryLoader is nil")
	}
	if options.PackageDynamicLibraryLoaderForState == nil {
		// 状态感知 loader 缺失会让 luaopen_* 无法绑定当前 State。
		t.Fatalf("native PackageDynamicLibraryLoaderForState is nil")
	}
}

// TestApplyNativeModuleOptionsKeepsExistingLoaders 验证 native helper 不覆盖调用方已设置的 loader。
func TestApplyNativeModuleOptionsKeepsExistingLoaders(t *testing.T) {
	// 保留已有 loader 便于后续 CLI 测试或嵌入式入口复用该 helper。
	stateless := func(filename string, symbol string) (runtime.Value, error) {
		// 测试只比较函数指针是否被保留，不实际打开动态库。
		return runtime.NilValue(), nil
	}
	stateful := func(state *runtime.State) func(filename string, symbol string) (runtime.Value, error) {
		// 测试只比较函数指针是否被保留，不实际创建 State。
		return stateless
	}
	options := applyNativeModuleOptions(lua.Options{
		PackageDynamicLibraryLoader:         stateless,
		PackageDynamicLibraryLoaderForState: stateful,
	})
	_, err := options.PackageDynamicLibraryLoader("", "")
	if err != nil {
		// 已设置无状态 loader 不应被 native.Loader 覆盖，否则空参数会返回校验错误。
		t.Fatalf("native helper replaced existing stateless loader: %v", err)
	}
	loader := options.PackageDynamicLibraryLoaderForState(nil)
	_, err = loader("", "")
	if err != nil {
		// 已设置状态感知 loader 不应被 native.LoaderForState 覆盖。
		t.Fatalf("native helper replaced existing stateful loader: %v", err)
	}
}
