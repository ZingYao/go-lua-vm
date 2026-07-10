# go-lua-vm

`go-lua-vm` 是一个纯 Go 实现的 Lua 5.3 虚拟机项目，迁移基线锁定为官方 Lua 5.3.6。项目覆盖 compiler、bytecode、runtime、stdlib、Debug、CLI、`luac` 兼容工具以及 Go 嵌入 API，默认构建不依赖 CGO、Lua C API 或系统动态库。

[在线文档](https://zingyao.github.io/go-lua-vm/) · [文档构建状态](https://github.com/ZingYao/go-lua-vm/actions/workflows/docs.yml)

## 项目定位

- **Lua 5.3.6 行为兼容**：以官方 C 源码和官方可执行文件为基线，覆盖语法、字节码、VM、标准库、Debug 和 CLI 行为。
- **纯 Go 运行时**：核心 VM、编译器、标准库和工具链均使用 Go 实现，默认 `CGO_ENABLED=0`。
- **双交付形态**：既可作为 Go 库嵌入宿主程序，也可构建为 `glua`、`gluac`、`gluals` 命令行工具。
- **可审计扩展点**：支持 Go/Lua 双向调用、模块注册、对象代理、只读 VFS、动态库 loader 注入和可选语法扩展。

## 当前能力

| 能力 | 说明 |
| --- | --- |
| `glua` | 对齐官方 `lua` 的脚本执行、`-e`、`-l`、`-i`、stdin、REPL、错误输出和退出码。 |
| `gluac` | 对齐官方 `luac` 的编译、`-l` / `-l -l` 反汇编、`-o`、`-p`、`-s`、多文件和默认输出。 |
| Go 嵌入 API | 创建 State、加载源码/文件/chunk、打开标准库、注册 Go 函数并执行 Lua。 |
| Bridge | 显式注册 Go 函数、模块 table、对象代理、reflection 绑定和 Lua stub 生成。 |
| 标准库 | 覆盖 base、coroutine、table、string、utf8、math、io、os、package、debug 的主要 Lua 5.3 语义。 |
| Debug | 支持 traceback、hook、局部变量、upvalue、registry 与 stripped chunk 相关边界。 |
| 性能 | 默认 benchmark 中多数用例已低于官方 Lua 5.3.6，最终结果见 [docs/BENCHMARK.md](docs/BENCHMARK.md)。 |

## 环境要求

- Go：`go1.26.4`
- `go.mod`：`go 1.26` / `toolchain go1.26.4`
- 构建与测试：默认使用 `CGO_ENABLED=0`
- 默认构建：不需要 C 头文件、Lua C API 开发包、预装 `.so/.dylib/.dll` 或系统动态库链接依赖

## 快速开始

构建全部命令行工具：

```bash
make build
```

运行 Lua 脚本：

```bash
./bin/glua -v
./bin/glua script.lua arg1 arg2
printf 'print(_VERSION)' | ./bin/glua -
```

编译或反汇编 chunk：

```bash
./bin/gluac -p script.lua
./bin/gluac -o out.luac script.lua
./bin/gluac -l -l script.lua
```

跨平台构建发布产物：

```bash
make dist
```

清理本地产物：

```bash
make clean
```

## Go 嵌入示例

```go
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
	if err := lua.DoString(state, `print(_VERSION)`); err != nil {
		log.Fatal(err)
	}
}
```

更完整的嵌入 API、VFS、动态库 loader、Bridge 和对象代理说明见 [docs/API.md](docs/API.md)、[docs/BRIDGE.md](docs/BRIDGE.md) 与 [docs/RELEASE_LIMITS.md](docs/RELEASE_LIMITS.md)。

## CLI 兼容口径

`glua` 目标是覆盖官方 Lua 5.3.6 `lua` 可执行文件的参数和调用方式，包括无参数、`-v`、`-E`、`-i`、`-e`、`-l`、`--`、`-`、脚本参数、stdin 管道、REPL、错误输出、Ctrl-C 和 `os.exit()`。

`gluac` 目标是覆盖官方 Lua 5.3.6 `luac` 可执行文件的参数和调用方式，包括无参数、`-v`、`-l`、`-l -l`、`-o`、`-p`、`-s`、`--`、单文件、多文件、错误参数和默认 `luac.out`。多文件输入会组合成顺序执行的 wrapper chunk，并共享 `_ENV`。

项目扩展参数不会占用官方参数空间：

- `glua` 扩展使用 `--glua-*`，例如 `--glua-syntax`、`--glua-disable-syntax`、`--glua-list-bytecode`、`--glua-format`。
- `gluac` 扩展使用 `--gluac-*`，例如 `--gluac-syntax`、`--gluac-disable-syntax`、`--gluac-opcode-trace`。
- `gluals` 扩展使用 `--gluals-*`，例如 `--gluals-syntax`。

完整兼容矩阵见 [docs/CLI_COMPATIBILITY.md](docs/CLI_COMPATIBILITY.md)，总体差异口径见 [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md)。

## 语法扩展

项目在 Lua 5.3 基线上提供可选的 `continue`、`switch/case/default` 与 `const` 语法扩展。扩展只改变 lexer/parser/codegen 层，最终仍生成标准 Lua 5.3 VM 指令。

```bash
go build -tags lua53 ./cmd/glua
go build -tags with_continue ./cmd/glua
go build -tags with_switch ./cmd/glua
go build -tags with_const ./cmd/glua
go build -tags with_all ./cmd/glua
```

运行时可通过 `--glua-syntax`、`--gluac-syntax` 或 Go API 选择 Lua 5.3 兼容模式或扩展模式。详细语义和示例见[在线语法糖文档](https://zingyao.github.io/go-lua-vm/#/SYNTAX_EXTENSIONS)。

## 动态库与 require 边界

默认构建不支持 `require` 直接加载普通 Lua C 模块形式的 `.so/.dylib/.dll`。默认路径保持纯 Go、无 CGO，不提供 Lua C ABI，也不要求系统安装 Lua C 开发包。

动态库 loader 接入协议始终是显式 opt-in：`package.searchers` 会按 `package.cpath` 展开候选路径，`package.loadlib`、`lua.Options.PackageDynamicLibraryLoader` 或 `lua.Options.PackageDynamicLibraryLoaderForState` 可由 Go 宿主注入实现。普通动态库不是 Lua C 模块；只有导出 `luaopen_*` 并使用 Lua 5.3 public C API 的模块才能按 `require` 语义加载。

`native_modules` 是可选 CGO 构建能力：

```bash
CGO_ENABLED=1 go build -tags native_modules -o bin/glua-native ./cmd/glua
```

该构建在 CLI 中注入 State-aware native loader，用仓库内 Lua 5.3 public headers 和 native shim 支持 Lua C 模块。macOS arm64、Linux arm64 和 Windows amd64 已覆盖仓库内真实模块源码构建及运行期验收，包含 lua-cjson、LPeg 和 LuaSocket；其他系统与架构仍需目标平台独立验收。native 模块执行本机机器码，拥有进程权限；生产环境需要限制动态库来源和 `package.cpath`。三平台前置条件和命令见[在线 Native 构建文档](https://zingyao.github.io/go-lua-vm/#/NATIVE_BUILD_GUIDE)。

## 验证命令

日常开发门禁：

```bash
CGO_ENABLED=0 go test ./...
./scripts/check-go-gates.sh
git ls-files --others --exclude-standard | rg '\.go$|_test\.go$'
```

涉及 CLI、bytecode、VM、stdlib、compiler 或官方兼容行为时，还应重建工具并对比官方 Lua 5.3.6：

```bash
CGO_ENABLED=0 go build -o bin/glua ./cmd/glua
CGO_ENABLED=0 go build -o bin/gluac ./cmd/gluac
LUA_BIN=/path/to/lua-5.3.6 \
LUAC_BIN=/path/to/luac-5.3.6 \
./scripts/compare-cli-golden.sh
LUA_BIN=/path/to/lua-5.3.6 \
LUAC_BIN=/path/to/luac-5.3.6 \
./scripts/compare-official-executables.sh
LUA_BIN=/path/to/lua-5.3.6 \
LUAC_BIN=/path/to/luac-5.3.6 \
./scripts/run-official-tests.sh
```

性能对比：

```bash
LUA_BIN=/path/to/lua-5.3.6 \
LUAC_BIN=/path/to/luac-5.3.6 \
GLUA_BIN=./bin/glua \
GLUAC_BIN=./bin/gluac \
./scripts/benchmark-official.sh
```

## 仓库结构

| 路径 | 职责 |
| --- | --- |
| `cmd/glua` | `lua` 兼容 CLI 入口。 |
| `cmd/gluac` | `luac` 兼容字节码工具入口。 |
| `cmd/gluals` | glua language server 入口。 |
| `lua` | 对外嵌入 API。 |
| `runtime` | VM 运行时、State、栈、值、表、闭包、协程、错误恢复。 |
| `compiler` | lexer、parser、codegen。 |
| `bytecode` | Lua 5.3 指令、Proto、binary chunk load/dump 和反汇编。 |
| `stdlib` | Lua 标准库实现。 |
| `debug` | Debug hook、栈帧、局部变量、upvalue 和 traceback。 |
| `bridge` | Go 与 Lua 双向调用、对象代理和 Lua stub 生成。 |
| `docs` | 设计、兼容、发布边界、性能与使用说明文档。 |
| `third_party/lua-5.3.6` | Lua 5.3.6 官方源码参考，不参与 Go 构建。 |

## 对外文档

### 项目与兼容口径

- [docs/PLAN.md](docs/PLAN.md)：Lua 5.3 迁移目标、边界、交付形态和总体路线。
- [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md)：Lua 5.3.6 行为兼容目标、允许差异和验收方式。
- [docs/CLI_COMPATIBILITY.md](docs/CLI_COMPATIBILITY.md)：`glua` / `gluac` 对官方 `lua` / `luac` 的 CLI 兼容矩阵。
- [docs/RELEASE_LIMITS.md](docs/RELEASE_LIMITS.md)：首版发布能力承诺、默认纯 Go 边界和已知限制。
- [docs/LUA53_MAPPING.md](docs/LUA53_MAPPING.md)：Lua 5.3.6 官方 C 源码到本项目 Go 包的迁移映射。

### 使用与集成

- [docs/API.md](docs/API.md)：Go 嵌入 API 设计、状态机生命周期、加载执行和宿主选项。
- [docs/BRIDGE.md](docs/BRIDGE.md)：Go/Lua 双向调用、模块注册、对象代理和 Lua stub 生成。
- [docs/LUAC.md](docs/LUAC.md)：`gluac` 兼容工具设计、参数策略、反汇编和 binary chunk 输出。
- [docs/CONTROL_FLOW_EXTENSIONS.md](docs/CONTROL_FLOW_EXTENSIONS.md)：`continue`、`switch/case/default` 语法扩展开关与语义。
- [docs/CUSTOM_CHUNK.md](docs/CUSTOM_CHUNK.md)：自定义加密 chunk 的 encoder/decoder 接入规划与最小 Demo。
- [docs/NATIVE_MODULES_PLAN.md](docs/NATIVE_MODULES_PLAN.md)：Lua C 原生模块加载方案、目标、非目标、架构和分期。
- [docs/NATIVE_MODULES_BUILD.md](docs/NATIVE_MODULES_BUILD.md)：`native_modules` 构建方式、平台前置条件、验收命令和当前 API 覆盖边界。
- [docs/NATIVE_CROSS_TOOLCHAINS.md](docs/NATIVE_CROSS_TOOLCHAINS.md)：`native_modules` 跨系统交叉编译矩阵、mise/Zig 工具链和 CI 验证入口。
- [docs/NATIVE_MODULES_SOURCE_INVENTORY.md](docs/NATIVE_MODULES_SOURCE_INVENTORY.md)：native shim、Lua public headers、fixture、真实模块源码和脚本的自包含清单。

### 运行时与标准库语义

- [docs/DEBUG.md](docs/DEBUG.md)：Debug hook、traceback、局部变量、upvalue 和 `debug` 标准库范围。
- [docs/GC.md](docs/GC.md)：Go GC 与 Lua 对象生命周期边界、root 策略、finalizer 和弱表限制。
- [docs/TABLE.md](docs/TABLE.md)：Table 迭代稳定性、raw next/ipairs、resize 与 weak table 策略。
- [docs/IO_OS.md](docs/IO_OS.md)：`io` / `os` / `package` 标准库的宿主访问策略和 sandbox 选项。

### 性能结果

- [docs/BENCHMARK.md](docs/BENCHMARK.md)：官方 Lua 5.3.6 与 `glua` / `gluac` 的最终 benchmark 对比。
- [docs/PERFORMANCE_CLOSURE_REPORT.md](docs/PERFORMANCE_CLOSURE_REPORT.md)：性能收敛详情、中文用例对照、优化差异和语义门禁说明。

内部推进用的 `*_TODO.md` 与阶段性 perf plan 文档不作为对外入口维护；需要追溯优化过程时优先阅读性能收敛报告。
