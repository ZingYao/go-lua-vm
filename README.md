# go-lua-vm

`go-lua-vm` 是一个纯 Go Lua 5.3 虚拟机迁移项目，目标是对照 Lua 5.3 官方源码实现 compiler、bytecode、runtime、stdlib、Debug、CLI 和 Go 嵌入 API。

## Go 版本

- Go：`go1.26.4`
- `go.mod`：`go 1.26` / `toolchain go1.26.4`
- 构建与测试必须使用 `CGO_ENABLED=0`
- 默认跨平台构建不依赖 C 头文件、Lua C API 开发包、预装 `.so/.dylib/.dll` 或系统动态库链接依赖。

## 常用命令

```bash
make test
make fmt
make gate
go build -tags lua53 ./cmd/glua
go build -tags with_switch ./cmd/glua
go build -tags with_continue ./cmd/glua
```

## 目录

- `cmd/glua`：CLI 入口。
- `cmd/gluac`：Lua 5.3 `luac` 兼容字节码工具入口。
- `cmd/gluals`：glua language server 入口。
- `lua`：对外嵌入 API。
- `runtime`：VM 运行时。
- `compiler`：lexer、parser、codegen。
- `bytecode`：Lua 5.3 指令与 chunk。
- `stdlib`：Lua 标准库。
- `debug`：Debug hook 与调试信息。
- `bridge`：Go 与 Lua 双向调用。
- `docs/LUAC.md`：`gluac` / `luac` 兼容工具设计。
- `docs/BENCHMARK.md`：官方 Lua 5.3.6 与 glua/gluac 的性能基准对比。
- `docs/CUSTOM_CHUNK.md`：自定义加密 chunk 的 encoder/decoder 接入规划与最小 Demo。
- `docs/CONTROL_FLOW_EXTENSIONS.md`：`continue` 与 `switch/case/default` 语法扩展设计。
- `third_party/lua-5.3.6`：Lua 5.3.6 官方源码参考，不参与构建。

## glua / gluac 兼容口径

`glua` 目标是覆盖官方 Lua 5.3.6 `lua` 可执行文件的参数和调用方式，包括无参数、`-v`、`-E`、`-i`、`-e`、`-l`、`--`、`-`、脚本参数、stdin 管道、REPL、错误输出、Ctrl-C 和 `os.exit()`。

`gluac` 目标是覆盖官方 Lua 5.3.6 `luac` 可执行文件的参数和调用方式，包括无参数、`-v`、`-l`、`-l -l`、`-o`、`-p`、`-s`、`--`、单文件、多文件、错误参数和默认 `luac.out`。多文件输入会组合成顺序执行的 wrapper chunk，并共享 `_ENV`。

项目扩展参数不会占用官方参数空间：

- `glua` 扩展使用 `--glua-*`，例如 `--glua-syntax`、`--glua-disable-syntax`、`--glua-list-bytecode`、`--glua-format`。
- `gluac` 扩展使用 `--gluac-*`，例如 `--gluac-syntax`、`--gluac-disable-syntax`、`--gluac-opcode-trace`。
- `gluals` 扩展使用 `--gluals-*`，例如 `--gluals-syntax`。

完整兼容矩阵和 release 阻塞验收见 [docs/CLI_COMPATIBILITY.md](docs/CLI_COMPATIBILITY.md)，总体差异口径见 [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md)。

## 性能基准

官方 Lua 5.3.6、`glua`、`gluac` 的脚本运行、CLI 冷启动、编译和 Go 内部 benchmark 对比见 [docs/BENCHMARK.md](docs/BENCHMARK.md)。

## 自定义加密 chunk 接入规划

自定义加密 chunk 的 encoder/decoder 接入方式、最小可执行 Demo 与避坑点见 [docs/CUSTOM_CHUNK.md](docs/CUSTOM_CHUNK.md)。

## 动态库与 require 边界

首版不支持 `require` 直接加载普通 Lua C 模块形式的 `.so/.dylib/.dll`。普通 Lua C 模块依赖 `lua_State*` 和 Lua C ABI 兼容层；本项目默认纯 Go 构建不提供该 ABI，也不为此引入 CGO、C 头文件或系统动态库依赖。

当前已打通的是动态库 loader 接入协议：`package.searchers` 会按 `package.cpath` 展开候选路径，`package.loadlib` 或 `lua.Options.PackageDynamicLibraryLoader` 可由 Go 宿主注入实现。需要使用动态库能力时，应由 Go 宿主侧加载 `.so/.dylib/.dll`，再把能力包装成本 VM 可调用的 Go/Lua callable 暴露给 Lua。

## Go 嵌入扩展能力

Go 嵌入模式支持显式函数、模块 table、只读 table、对象代理、常量/变量注入、`package.loaded`、`package.preload` 和 Lua stub 生成。需要自动绑定时，可以显式调用 `bridge.ReflectFunction`、`bridge.ReflectedFunctions` 或 `bridge.ReflectStruct`；该能力只扫描调用方传入的函数/对象，不做包级自动扫描，也不隐式递归展开 `map`、`slice` 或循环引用对象。

VFS、动态库 loader、reflection 自动绑定和 Go 封装 API 的发布承诺与限制见 [docs/RELEASE_LIMITS.md](docs/RELEASE_LIMITS.md)，具体 Bridge API 见 [docs/BRIDGE.md](docs/BRIDGE.md)。

## 语法扩展开关

`continue` 与 `switch/case/default` 的语义、build tag 裁剪方式、glua/gluac 参数和 Go API 接入方式见 [docs/CONTROL_FLOW_EXTENSIONS.md](docs/CONTROL_FLOW_EXTENSIONS.md)。
