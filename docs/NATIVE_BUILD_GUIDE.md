# Native 三平台构建

Native CGO 模式用于让 `require` 加载按 Lua 5.3 public C API 编译的 `.so`、`.dylib` 或 `.dll` 模块。

默认纯 Go 构建与 Native 构建是两条独立路径：

| 模式 | CGO | Lua C 模块 |
| --- | --- | --- |
| 纯 Go | `CGO_ENABLED=0` | 不提供内置 C 模块加载器 |
| Native | `CGO_ENABLED=1` | 自动启用 Lua 5.3 C API shim 和动态库加载 |

## 通用前置条件

- Go `1.26.4`，并确保 `go version` 使用该版本。
- Git。
- 与目标操作系统、CPU 架构匹配的 C 编译器。
- 构建命令位于仓库根目录。
- 不需要系统安装 Lua 开发包；public headers 固定在 `native/lua53/include/`。

最小 Native 构建命令：

~~~bash
CGO_ENABLED=1 go build -trimpath -o bin/glua-native ./cmd/glua
~~~

构建全部 CLI：

~~~bash
mkdir -p bin
for command in glua gluac gluals; do
  CGO_ENABLED=1 go build -trimpath \
    -o "bin/${command}" "./cmd/${command}"
done
~~~

## Linux

### 前置条件

Debian/Ubuntu：

~~~bash
sudo apt-get update
sudo apt-get install -y build-essential git
~~~

Fedora/RHEL：

~~~bash
sudo dnf install -y gcc gcc-c++ make git
~~~

确认工具链：

~~~bash
go version
gcc --version
~~~

### 本机构建

~~~bash
mkdir -p bin
CGO_ENABLED=1 CC=gcc \
  go build -trimpath \
  -o bin/glua-native ./cmd/glua
~~~

Linux 原生模块通常使用 `.so` 后缀。执行真实模块验收：

~~~bash
./scripts/test-native-real-modules.sh
~~~

### 交叉构建示例

使用 Zig 构建 Linux arm64：

~~~bash
NATIVE_CROSS_TARGETS="linux/arm64" \
NATIVE_CC_LINUX_ARM64="zig cc -target aarch64-linux-gnu" \
./scripts/check-native-cross-compile.sh
~~~

## macOS

### 前置条件

安装 Xcode Command Line Tools：

~~~bash
xcode-select --install
xcrun clang --version
~~~

### 当前架构构建

~~~bash
mkdir -p bin
CGO_ENABLED=1 CC="xcrun clang" \
  go build -trimpath \
  -o bin/glua-native ./cmd/glua
~~~

### arm64 与 amd64

Apple Silicon：

~~~bash
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
CC="xcrun clang -arch arm64" \
go build -trimpath \
  -o bin/glua-darwin-arm64 ./cmd/glua
~~~

Intel：

~~~bash
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
CC="xcrun clang -arch x86_64" \
go build -trimpath \
  -o bin/glua-darwin-amd64 ./cmd/glua
~~~

macOS 原生模块可使用 `.dylib`，Lua 生态中也常见 `.so`。本机完整验收：

~~~bash
./scripts/test-native-real-modules.sh
./scripts/check-native-lua-abi-symbols.sh
~~~

## Windows

### 前置条件

- Go `1.26.4` Windows 版本。
- Git for Windows，脚本验收需要 Git Bash。
- 支持 CGO 的 C 工具链，推荐 MSYS2 MinGW-w64 GCC；交叉编译也可使用 Zig。
- PowerShell 7 或 Windows PowerShell 5.1。

MSYS2 UCRT64 终端中安装 GCC：

~~~bash
pacman -S --needed mingw-w64-ucrt-x86_64-gcc make git
~~~

确保 `gcc.exe` 所在目录进入 `PATH`，然后在 PowerShell 构建：

~~~powershell
$env:CGO_ENABLED = "1"
$env:CC = "gcc"
go build -trimpath -o bin\glua-native.exe .\cmd\glua
~~~

Windows Lua C 模块通常使用 `.dll`，并可能链接 `lua53.dll`。仓库提供 `lua53.dll` shim、`.def` 和 import library 构建脚本。

执行 Windows 完整功能验收：

~~~powershell
.\scripts\test-native-windows-manual.ps1 -GoArch amd64 -Bash "C:\Program Files\Git\bin\bash.exe" -StrictRuntime
~~~

该脚本会检查默认纯 Go 测试、Native Go 测试、`lua53.dll`、fixture、lua-cjson、LPeg、LuaSocket 和实际 `require`。

### 从 Unix 交叉构建 Windows

~~~bash
NATIVE_CROSS_TARGETS="windows/amd64" \
NATIVE_CC_WINDOWS_AMD64="zig cc -target x86_64-windows-gnu" \
./scripts/check-native-cross-compile.sh
~~~

交叉编译只能证明链接与产物生成成功；最终仍需在 Windows 目标机执行运行期验收。

## 自动准备交叉工具链

仓库使用 `mise` 和 Zig 管理 GitHub Actions 中的多目标 C 编译器：

~~~bash
./scripts/bootstrap-native-toolchains.sh --install
eval "$(./scripts/bootstrap-native-toolchains.sh --emit-env)"
./scripts/bootstrap-native-toolchains.sh
~~~

严格检查指定目标：

~~~bash
NATIVE_CROSS_REQUIRE_ALL=1 \
NATIVE_CROSS_TARGETS="linux/amd64 windows/amd64 darwin/arm64" \
./scripts/check-native-cross-compile.sh
~~~

发布目录格式可以直接复用 Actions 脚本：

~~~bash
export NATIVE_CC_LINUX_AMD64="zig cc -target x86_64-linux-gnu"
./scripts/build-release-cli-native.sh linux-amd64 linux amd64
~~~

产物位于 `dist/linux-amd64/`，包含 `glua`、`gluac` 和 `gluals`。

## 验证构建特性

查看帮助末尾的构建特性：

~~~bash
./bin/glua-native --help
~~~

Native Go 门禁：

~~~bash
CGO_ENABLED=1 go test ./...
./scripts/check-go-gates.sh
~~~

真实模块总验收：

~~~bash
./scripts/test-native-real-modules.sh
~~~

更细的 ABI、第三方模块和平台边界见 [Native 详细构建说明](NATIVE_MODULES_BUILD.md)、[跨平台工具链](NATIVE_CROSS_TOOLCHAINS.md)和[验收记录](NATIVE_MODULES_ACCEPTANCE.md)。

## 安全边界

- Native 模块执行本机机器码，拥有 GLua 进程的权限。
- 只加载可信来源的动态库，并限制 `package.cpath`。
- 不兼容依赖 Lua 内部头文件或直接访问官方 `lua_State` 内部结构的模块。
- `lua_yieldk`、C continuation 和部分 C Debug API 尚不属于兼容承诺。
- 默认纯 Go 构建不应因为 Native 能力引入任何 CGO 依赖。
