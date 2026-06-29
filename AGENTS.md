# go-lua-vm 项目级规范

## 项目目标

本项目使用 Go 实现 Lua 5.3 虚拟机，迁移 Lua 5.3 官方源码行为，覆盖编译器、字节码、VM、标准库、Debug、CLI 与 Go 嵌入调用能力。

实现必须同时支持：

- 作为 Go 库嵌入其他程序调用。
- 编译为类似 `lua` 的命令行产物。
- Go 与 Lua 双向回调。
- 将 Go 暴露能力生成 Lua stub/代理代码。

## Go 版本与本机路径

- Go 稳定版本基线：`go1.26.4`。
- `go.mod` 必须声明 `go 1.26` 与 `toolchain go1.26.4`。
- 本机 Go SDK 路径：`/Users/zing/sdk/go/go1.26.4`。
- 项目脚本不得硬编码 SDK 绝对路径；执行命令前应确保 PATH 上的 `go` 指向 `go1.26.4`。
- 禁止通过临时设置 `GOROOT`、`GOPATH`、`GOTOOLCHAIN` 或绝对路径切换 Go 版本来绕过项目门禁。

## CGO 规则

- 禁止引入 CGO。
- 禁止任何 Go 文件出现 `import "C"`。
- 构建、测试、基准测试必须使用 `CGO_ENABLED=0`。
- 不得通过 C 动态库、Lua C API 或宿主 C 扩展实现核心功能。
- 允许在 `third_party/` 存放 Lua 5.3 官方 C 源码，仅作为对照迁移参考，不参与 Go 构建。

## 注释规则

- 所有包级导出类型、导出函数、导出方法、未导出业务函数和未导出方法都必须提供中文注释。
- 方法或函数注释必须说明功能目标、入参约束、出参语义、错误语义和兼容 Lua 5.3 的关键点。
- 方法体首行必须是中文流程注释，说明该方法的主要执行路径。
- 所有 `if`、`else if`、`else`、`switch`、`case`、`default` 分支必须有中文注释。
- 分支内出现 `return`、`continue`、`break`、`panic` 时，注释必须说明提前退出原因与影响范围。
- 并发、栈操作、寄存器操作、upvalue、GC、Debug hook、错误恢复和跨 Go/Lua 回调必须补充更详细的中文说明。
- 禁止使用无语义变量名，例如 `tmp`、`obj`、`data`；确需临时变量时必须在声明前说明用途。

## 架构规则

- 核心 VM 必须使用纯 Go 实现。
- Lua 5.3 官方源码迁移必须先建立源码文件到 Go 包的映射，再逐模块实现。
- `lua/` 目录只暴露稳定嵌入 API，不泄露内部 VM 细节。
- `runtime/` 目录承载 State、栈、值、表、闭包、协程、GC 和错误恢复。
- `compiler/` 目录承载 lexer、parser 和 codegen。
- `bytecode/` 目录承载 Lua 5.3 opcode、Proto 与 binary chunk 编解码。
- `stdlib/` 目录承载 Lua 标准库。
- `debug/` 目录承载 Debug hook、栈帧、局部变量、upvalue 和 traceback。
- `bridge/` 目录承载 Go 与 Lua 双向调用、userdata 代理和 Lua stub 生成。

## 开发门禁

- 修改 Go 代码前必须优先使用 gopls 做符号、引用、诊断和影响面分析。
- gopls MCP 可用时优先使用 MCP；gopls MCP 不可用时，允许直接调用 PATH 上的 `gopls` 可执行程序作为替代，不需要启动 MCP。
- 使用命令行 `gopls` 时，必须直接运行诊断、定义、引用等所需子命令或 LSP 兼容命令，并在交付说明中记录使用的命令与结果。
- 如果 gopls MCP 与命令行 `gopls` 都不可用，交付说明必须明确不可用原因、替代校验手段和剩余风险。
- 新建或修改 Go 文件后必须执行 `gofmt`。
- 新建或修改 Go 文件后必须立即执行 `git add <file>`。
- 交付前必须执行 `./scripts/check-go-gates.sh`。
- 交付前必须执行 `git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'`，若存在未跟踪 Go 文件必须先处理。
- 受保护分支 `main`、`master`、`test` 上禁止提交；需要提交前必须先切换到业务分支。

## 测试规则

- Go 单测使用标准库 `testing`。
- 修改 VM、compiler、stdlib、debug、bridge 任一模块时必须同步补测试。
- 兼容行为必须尽量用 Lua 5.3 官方行为或 golden 对比验证。
- 测试不得依赖真实时间漂移、随机顺序、外部服务或跨用例污染。
- 不得为了测试在正式代码中加入测试专用分支、测试入口或可变函数 hook。

## 第三方源码规则

- 允许在 `third_party/lua-5.3.6/` clone 或保存 Lua 5.3 官方源码。
- 第三方源码只作为阅读、映射和 golden 对照使用。
- 禁止把第三方 C 源码接入 Go 构建链路。
- 禁止修改第三方源码后声称仍是官方原始基线；如需 patch，必须另建目录并记录差异。
