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
- `lua`：对外嵌入 API。
- `runtime`：VM 运行时。
- `compiler`：lexer、parser、codegen。
- `bytecode`：Lua 5.3 指令与 chunk。
- `stdlib`：Lua 标准库。
- `debug`：Debug hook 与调试信息。
- `bridge`：Go 与 Lua 双向调用。
- `docs/LUAC.md`：`gluac` / `luac` 兼容工具设计。
- `docs/CUSTOM_CHUNK.md`：自定义加密 chunk 的 encoder/decoder 接入规划与最小 Demo。
- `docs/CONTROL_FLOW_EXTENSIONS.md`：`continue` 与 `switch/case/default` 语法扩展设计。
- `third_party/lua-5.3.6`：Lua 5.3.6 官方源码参考，不参与构建。

## 自定义加密 chunk 接入规划

自定义加密 chunk 的 encoder/decoder 接入方式、最小可执行 Demo 与避坑点见 [docs/CUSTOM_CHUNK.md](docs/CUSTOM_CHUNK.md)。

## 动态库与 require 边界

首版不支持 `require` 直接加载普通 Lua C 模块形式的 `.so/.dylib/.dll`。普通 Lua C 模块依赖 `lua_State*` 和 Lua C ABI 兼容层；本项目默认纯 Go 构建不提供该 ABI，也不为此引入 CGO、C 头文件或系统动态库依赖。

当前已打通的是动态库 loader 接入协议：`package.searchers` 会按 `package.cpath` 展开候选路径，`package.loadlib` 或 `lua.Options.PackageDynamicLibraryLoader` 可由 Go 宿主注入实现。需要使用动态库能力时，应由 Go 宿主侧加载 `.so/.dylib/.dll`，再把能力包装成本 VM 可调用的 Go/Lua callable 暴露给 Lua。

## 语法扩展开关

`continue` 与 `switch/case/default` 的语义、build tag 裁剪方式、glua/gluac 参数和 Go API 接入方式见 [docs/CONTROL_FLOW_EXTENSIONS.md](docs/CONTROL_FLOW_EXTENSIONS.md)。
