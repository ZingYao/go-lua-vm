// Package main 演示第三方 Go 宿主通过 bridge 包向 Lua 注册延迟加载模块。
package main

import (
	"fmt"
	"log"

	"github.com/ZingYao/go-lua-vm/bridge"
	"github.com/ZingYao/go-lua-vm/lua"
)

// main 创建 GLua State，注册 Go 模块并执行使用该模块的 Lua 脚本。
//
// 示例没有命令行参数；模块注册、脚本编译或执行失败时通过 log.Fatal 退出，成功时输出 Go
// 函数返回值和 Lua 修改后的模块变量。
func main() {
	// 打开完整标准库，确保 package.preload 和 require 可供延迟模块使用。
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		// 标准库初始化失败时不能继续注册依赖 package 的模块。
		log.Fatal(err)
	}

	err := bridge.RegisterModulePreload(state, bridge.ModuleBinding{
		Name: "host.config",
		Constants: map[string]any{
			"VERSION": "1.1.0",
		},
		Variables: map[string]any{
			"enabled": true,
		},
		Functions: map[string]bridge.Function{
			"greet": func(context *bridge.Context) error {
				// 读取第一个 Lua 参数并返回由 Go 构造的问候文本。
				name, ok, convertErr := context.ToString(1)
				if convertErr != nil {
					// tostring 转换错误保留原始 Lua 错误语义。
					return convertErr
				}
				if !ok {
					// 无法转换的参数使用明确错误中止当前 Lua 调用。
					return lua.RaiseError(lua.Value{Kind: lua.KindString, String: "name must be a string"})
				}
				context.PushString("hello, " + name)
				return nil
			},
		},
		Tables: map[string]bridge.TableBinding{
			"paths": {
				Name:     "host.config.paths",
				ReadOnly: true,
				Fields: map[string]any{
					"cache":  "var/cache/glua",
					"config": "etc/glua/config.toml",
				},
			},
		},
	})
	if err != nil {
		// package.preload 不可用或绑定描述非法时终止示例。
		log.Fatal(err)
	}

	if err := lua.LoadString(state, `
local config = require("host.config")

assert(config.VERSION == "1.1.0")
local constantOK = pcall(function()
  config.VERSION = "2.0.0"
end)
assert(not constantOK)

local pathOK = pcall(function()
  config.paths.cache = "tmp"
end)
assert(not pathOK)

config.enabled = false
return config.greet("zing"), config.enabled, config.paths.config
`, "@examples/bridge_module/main.glua"); err != nil {
		// 编译错误会包含命名 chunk，便于定位示例脚本行号。
		log.Fatal(err)
	}

	results, err := lua.Call(state, state.ValueAt(-1))
	if err != nil {
		// Lua 断言、只读保护或 Go 回调错误都从受保护调用返回。
		log.Fatal(err)
	}
	if len(results) != 3 {
		// 返回数量变化表示示例与模块契约已经漂移。
		log.Fatalf("unexpected result count: %d", len(results))
	}

	fmt.Printf("greeting=%s enabled=%t config=%s\n", results[0].String, results[1].Bool, results[2].String)
}
