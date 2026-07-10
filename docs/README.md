# GLua

GLua 是以官方 Lua 5.3.6 行为为兼容基线、使用 Go 实现的 Lua 虚拟机与工具链。项目既可以作为 Go 库嵌入服务，也可以构建为 `glua`、`gluac` 和 `gluals` 命令行程序。

<div class="glua-capabilities">
  <div class="glua-capability"><strong>纯 Go 核心</strong>默认构建使用 <code>CGO_ENABLED=0</code>，VM、编译器、标准库和 Debug 不依赖系统 Lua，也不支持 Native C 模块。</div>
  <div class="glua-capability"><strong>Lua 5.3.6 兼容</strong>通过官方源码映射、可执行文件差分、Golden 和官方测试验证行为。</div>
  <div class="glua-capability"><strong>完整工具链</strong>提供脚本执行、字节码编译/反汇编、格式化、静态分析、DAP 与编辑器扩展。</div>
  <div class="glua-capability"><strong>可控扩展</strong>提供语法糖、Event、序列化、Hash、Regex、UUID、ZIP、Schema 和 Path。</div>
</div>

## 与官方 Lua 的关系

GLua 不修改 Lua 5.3 的核心语言目标。默认模式保留 Lua 5.3.6 语义，同时提供可以在编译期和运行时关闭的扩展：

- `continue`、`switch/case/default`、`const` 语法糖。
- `glua.event` 文件与函数执行事件。
- JSON、YAML、XML、TOML 编解码。
- Codec、Hash、Regex、UUID、内存 ZIP、Schema 和纯词法 Path。
- VSCode 与 JetBrains 的补全、跳转、静态扫描、格式化和 DAP 调试。
- 显式 `native_modules` 构建下的 Lua 5.3 C 模块加载。

需要完全按 Lua 5.3 语法运行时，可以使用：

~~~bash
glua --glua-syntax=lua53 script.lua
gluac --gluac-syntax=lua53 -o script.luac script.lua
~~~

## 环境要求

- Go `1.26.4`。
- Git。
- 默认纯 Go 构建不需要 C 编译器、Lua SDK 或 CGO，也不支持 Native C 模块。
- Native 构建必须显式使用 `CGO_ENABLED=1` 和 `native_modules` build tag，并需要当前平台可用的 C 工具链，参见 [Native 三平台构建](NATIVE_BUILD_GUIDE.md)。

## 快速开始

构建默认纯 Go 命令行工具：

~~~bash
mkdir -p bin
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac
CGO_ENABLED=0 go build -o bin/gluals ./cmd/gluals
~~~

运行脚本：

~~~lua
-- hello.glua
const project = "GLua"

for index = 1, 5 do
  if index == 2 then
    continue
  end
  print(project, index)
end
~~~

~~~bash
./bin/glua hello.glua
~~~

## Go 嵌入

~~~go
package main

import (
    "log"

    "github.com/ZingYao/go-lua-vm/lua"
)

func main() {
    state := lua.NewState()
    defer state.Close()

    if err := lua.OpenLibs(state); err != nil {
        log.Fatal(err)
    }
    if err := lua.DoString(state, `print(glua.json.encode({ ready = true }))`); err != nil {
        log.Fatal(err)
    }
}
~~~

## 下一步

- 查看 [效率对比](PERFORMANCE.md)，了解与官方 Lua 5.3.6 的同机测试结果。
- 查看 [Native 三平台构建](NATIVE_BUILD_GUIDE.md)，启用 Lua C 原生模块。
- 查看 [语法糖](SYNTAX_EXTENSIONS.md)，了解编译与运行期开关。
- 查看 [扩展方法总览](EXTENSION_APIS.md)，按命名空间查找 API 和示例。
- 查看 [Go 嵌入 API](API.md) 与 [Bridge](BRIDGE.md)，把 GLua 集成到 Go 服务。

<div class="glua-note">
Native 模块会执行本机机器码并继承当前进程权限。生产环境应限制动态库来源、package.cpath 和可写目录。
</div>
