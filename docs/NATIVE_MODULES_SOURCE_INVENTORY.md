# native_modules C 源码自包含清单

本文记录 `native_modules` 构建、测试和最终验收允许依赖的仓库内源码。目标是确保 Lua C 原生模块能力可以在 Linux、macOS、Windows 上用仓库内容复现，不依赖系统 Lua 开发包、LuaRocks、Homebrew Lua、临时下载源码或未记录的本机动态库。

## 入仓原则

- Lua 5.3 public headers、项目侧 C ABI/shim、fixture、真实第三方模块验收源码和构建脚本都必须有仓库内固定来源。
- 构建脚本只能引用 `native/lua53/include/` 下的 Lua 5.3 public headers，不能读取 `/usr/include/lua*`、Homebrew Lua、LuaRocks 安装目录或用户自定义 Lua SDK。
- 自编 fixture 只作为 loader 和 shim smoke；真实兼容验收必须使用仓库内固定的第三方 C 模块源码。
- 现成二进制模块验收可以作为附加 ABI 验证，但不能替代仓库内源码编译验收。

## 已入仓清单

| 类别 | 路径 | 当前用途 | 状态 |
| --- | --- | --- | --- |
| Lua public header | `native/lua53/include/lua.h` | 编译 Lua 5.3 public C API fixture 和后续真实模块源码 | 已固定 |
| Lua public header | `native/lua53/include/luaconf.h` | 提供 Lua 5.3 public ABI 类型配置 | 已固定 |
| Lua public header | `native/lua53/include/lauxlib.h` | 提供 `luaL_*` public API 声明和宏 | 已固定 |
| Lua public header | `native/lua53/include/lualib.h` | 提供标准库 public 声明 | 已固定 |
| Header 来源说明 | `native/lua53/include/README.md` | 记录 public headers 来源和禁止使用内部头文件的边界 | 已固定 |
| Go/CGO shim | `internal/native/capi_*_native.go` | 导出 `lua_*` / `luaL_*` 符号并映射到 Go VM | 已固定 |
| Go/CGO loader | `internal/native/dlopen_unix.go` | Unix `dlopen` / `dlsym` / `dlclose` 封装 | 已固定 |
| Go/CGO loader | `internal/native/dlopen_windows.go` | Windows `LoadLibraryW` / `GetProcAddress` / `FreeLibrary` 封装 | 已固定 |
| Fixture C source | `tests/native_modules/fixtures/glua_native_smoke.c` | 构建 `glua_native_smoke` / `glua_native_failopen`，覆盖 `luaopen_*`、C function、userdata、metatable、registry 和错误 smoke | 已固定 |
| Fixture Lua 脚本 | `tests/native_modules/fixtures/glua_native_smoke.lua` | 复用 `require("glua_native_smoke")`、C function、userdata、错误传播、traceback 和 `package.loaded` smoke 验收 | 已固定 |
| Fixture 构建脚本 | `scripts/build-native-fixtures.sh` | 使用仓库内 Lua public headers 和 fixture C 源码构建当前平台动态库；Linux 产出 `.so`，macOS 同时产出 `.dylib` 与 `.so`，并输出 `GOOS`、`GOARCH`、`CC`、`CGO_ENABLED` 与产物路径 | 已固定 |
| Fixture 测试脚本 | `scripts/test-native-modules.sh` | 构建 native tag `glua`，调用 fixture 构建脚本，并按当前平台后缀执行成功 require 与 luaopen 初始化失败两条 CLI smoke；macOS 覆盖 `.dylib` 与 `.so` 两类候选 | 已固定 |
| 交叉编译脚本 | `scripts/check-native-cross-compile.sh` | 编译 `internal/native` 测试二进制和 `cmd/glua` native 产物，显式输出目标平台、`CC`、产物路径和 skip 原因 | 已固定 |
| Skip 原因检查脚本 | `scripts/check-native-skip-reasons.sh` | 验证 Windows shim 未落地、LuaSocket/真实模块总验收 Windows runtime 暂不可用和缺失 cross C compiler 等场景必须输出明确 `skip:` 原因，防止平台不可用被静默视为通过 | 已固定 |
| Fixture Go harness | `internal/native/loadlib_fixture_unix_test.go` | 编译仓库内 fixture C 文件，并验证 `package.loadlib`、`require`、错误传播和 userdata 状态 | 已固定 |
| 真实模块源码 | `third_party/lua-cjson/` | 第一真实模块验收源码，固定 upstream `mpx/lua-cjson` tag `2.1.0` / commit `4bc5e917c8cd5fc2f6b217512ef530007529322f`，许可证见目录内 `LICENSE` | 已固定 |
| 真实模块构建脚本 | `scripts/build-native-cjson.sh` | 使用仓库内 Lua 5.3 public headers 和固定 `third_party/lua-cjson/` 源码编译当前平台 `cjson` 动态模块，显式输出目标平台、`CC`、源码路径和产物路径 | 已固定 |
| 真实模块运行期脚本 | `scripts/test-native-cjson.sh` | 构建 native tag `glua` 与 `cjson` 动态模块，验证 `lua_*` / `luaL_*` 未解析 ABI 符号由 native `glua` shim 覆盖，并执行 `require("cjson")`、`encode/decode`、`cjson.null`、非法 JSON `pcall` 和不可序列化 function `pcall` 验收；macOS 分别覆盖 `.so` 与 `.dylib` 后缀 | 已固定 |
| 真实模块源码 | `third_party/lpeg/` | 第二真实模块验收源码，固定 LPeg 1.1.0 官方源码包，用于后续覆盖复杂 userdata、metatable、registry 和 C function 行为；许可证和来源见目录内 `GLUA_VENDOR.md` 与 `lpeg.html` | 已固定 |
| 真实模块构建脚本 | `scripts/build-native-lpeg.sh` | 使用仓库内 Lua 5.3 public headers 和固定 `third_party/lpeg/` 源码编译当前平台 `lpeg` 动态模块，显式输出目标平台、`CC`、源码路径和产物路径；Windows 在 `lua53.dll` shim/import library 落地前明确 skip | 已固定 |
| LPeg 运行期脚本 | `scripts/test-native-lpeg.sh` | 使用仓库内 `third_party/lpeg/` 和 Lua 5.3 public headers 编译并运行第二真实模块验收；macOS 覆盖 `.so` 与 `.dylib` 后缀 | 已固定 |
| 真实模块源码 | `third_party/luasocket/` | 网络库验收源码，固定 `lunarmodules/luasocket` tag `v3.1.0` / commit `95b7efa9da506ef968c1347edf3fc56370f0deed`，用于后续覆盖 `socket.core`、`mime.core`、系统 socket 依赖和平台网络行为；许可证和来源见目录内 `LICENSE` 与 `GLUA_VENDOR.md` | 已固定 |
| 真实模块构建脚本 | `scripts/build-native-luasocket.sh` | 使用仓库内 Lua 5.3 public headers 和固定 `third_party/luasocket/` 源码编译当前平台 `socket/core` 与 `mime/core` 动态模块，显式输出目标平台、`CC`、源码路径和产物路径；Windows 在 `lua53.dll` shim/import library 落地前明确 skip | 已固定 |
| 真实模块运行期脚本 | `scripts/test-native-luasocket.sh` | 构建 native tag `glua` 与 LuaSocket 动态模块，按当前平台后缀执行 `require("mime")`、MIME 编解码、`require("socket")`、TCP loopback 和 UDP loopback 运行期验收；Windows 在 `lua53.dll` shim/import library 落地前明确 skip | 已固定 |
| 真实模块总验收脚本 | `scripts/test-native-real-modules.sh` | 串联 fixture、lua-cjson、LPeg 和 LuaSocket 当前平台运行期验收，统一输出宿主平台、目标平台和 CGO 状态；Windows 目标和其他异平台目标会明确 skip，Linux/Windows 仍由各子脚本和目标平台闭环单独确认 | 已固定 |
| 真实模块验收记录 | `docs/NATIVE_MODULES_ACCEPTANCE.md` | 记录当前 macOS arm64 已通过的真实模块验收、Linux/Windows 未闭环状态和不可夸大的兼容边界 | 已固定 |

## 尚未入仓清单

| 类别 | 目标路径 | 用途 | 阻塞影响 |
| --- | --- | --- | --- |
| Windows shim 产物源码 | 待定 `native/lua53/windows/` 或 `tests/native_modules/windows/` | 提供 `lua53.dll` shim 或等价 import library 验证入口 | Windows 现成 Lua 5.3 ABI 模块验收暂缺 |

## 当前可验证边界

- 默认构建仍以 `CGO_ENABLED=0 go test ./...` 和 `./scripts/check-go-gates.sh` 作为必过门禁。
- `native_modules` 构建当前可通过 `CGO_ENABLED=1 go test -tags native_modules ./...` 验证仓库内 Go/CGO shim、平台 loader 和 Unix 内嵌 fixture。
- `lua-cjson` 真实模块源码可通过 `CGO_ENABLED=1 scripts/build-native-cjson.sh` 做源码编译级验收，也可通过 `scripts/test-native-cjson.sh` 做 ABI 符号和运行期验收；运行期脚本覆盖 `require("cjson")`、`encode/decode`、`cjson.null` identity、非法 JSON `pcall` 和不可序列化 function `pcall`，并在 macOS 上分别验证 `.so` 与 `.dylib` 两类候选。
- `third_party/lpeg/` 当前可通过 `CGO_ENABLED=1 scripts/build-native-lpeg.sh` 做源码编译级验收，也可通过 `scripts/test-native-lpeg.sh` 做运行期验收；macOS arm64 `.so` 与 `.dylib` 后缀已通过基础 smoke 和完整官方 `test.lua`。
- `third_party/luasocket/` 当前可通过 `CGO_ENABLED=1 scripts/build-native-luasocket.sh` 做 `socket.core` / `mime.core` 源码编译级验收，也可通过 `scripts/test-native-luasocket.sh` 做当前平台运行期验收；脚本覆盖 `require("socket")`、`require("mime")`、MIME 编解码和可控本机 TCP/UDP loopback。
- `scripts/test-native-real-modules.sh` 可作为当前平台真实模块总验收入口，串联 fixture、lua-cjson、LPeg 和 LuaSocket 运行期脚本；该入口只聚合宿主同平台结果，Windows 目标和其他异平台目标会在执行子验收前明确 skip，不把缺失目标平台的 skip 解释为 Linux/Windows 已闭环。
- 现阶段不得把内嵌 smoke fixture 的通过结果解释为“任意第三方 Lua C 模块兼容”；它只证明项目侧 loader、opaque `lua_State*`、基础 C API shim 和 require 链路已经贯通。

## 后续维护规则

- 每引入一个新的 C 源文件、第三方模块源码、构建脚本或平台 shim，都必须更新本文。
- 每个第三方模块目录必须记录来源、版本、许可证和本项目是否修改过源码。
- 若某个平台缺少 C toolchain，验证脚本必须显式输出 skip 原因，不能静默成功。
