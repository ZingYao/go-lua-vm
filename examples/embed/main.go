// Package main 展示 go-lua-vm 作为 Go 库嵌入宿主程序的基础用法。
//
// 示例只依赖当前已经完成的 Go closure 注册与调用能力，不依赖尚未接线的 Lua Proto 执行器。
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zing/go-lua-vm/lua"
)

// main 创建 Lua State、注册 Go 函数并通过 lua.Call 调用。
//
// 入参来自进程启动环境；示例没有命令行参数。错误会通过 log.Fatal 退出进程，正常路径输出
// Go 回调返回的 Lua integer 值。
func main() {
	// 创建带 context 和默认资源限制的 State，便于宿主统一控制取消与超时。
	state, err := lua.NewStateWithContext(context.Background(), lua.DefaultOptions())
	if err != nil {
		// State 创建失败表示宿主传参或资源配置非法，示例直接退出。
		log.Fatal(err)
	}
	defer state.Close()

	if err := lua.Register(state, "add", func(args ...lua.Value) ([]lua.Value, error) {
		// 示例函数要求两个参数都能按 Lua 5.3 number-to-integer 规则转换为 integer。
		left, leftOK := args[0].ToInteger()
		if !leftOK {
			// 参数类型不符合预期时抛出 Lua error object。
			return nil, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "left integer expected"})
		}
		right, rightOK := args[1].ToInteger()
		if !rightOK {
			// 参数类型不符合预期时抛出 Lua error object。
			return nil, lua.RaiseError(lua.Value{Kind: lua.KindString, String: "right integer expected"})
		}
		return []lua.Value{lua.Value{Kind: lua.KindInteger, Integer: left + right}}, nil
	}); err != nil {
		// 注册失败通常表示 State 无效或函数为空。
		log.Fatal(err)
	}

	addFunction, err := lua.GetGlobal(state, "add")
	if err != nil {
		// 读取全局函数失败表示 State 生命周期异常。
		log.Fatal(err)
	}
	results, err := lua.Call(
		state,
		addFunction,
		lua.Value{Kind: lua.KindInteger, Integer: 20},
		lua.Value{Kind: lua.KindInteger, Integer: 22},
	)
	if err != nil {
		// 调用失败时把 Lua error object 输出出来，便于宿主定位脚本侧错误。
		log.Fatalf("call failed: %v object=%s", err, lua.ErrorObject(err).DebugString())
	}
	if len(results) != 1 {
		// 示例函数只承诺单返回值，返回数量不匹配表示注册函数实现错误。
		log.Fatalf("unexpected result count: %d", len(results))
	}

	fmt.Println(results[0].Integer)
}
