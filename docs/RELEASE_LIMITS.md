# 首版发布限制

本文记录首个 release 的能力承诺与已知限制。结论基于当前代码实现、测试结果和发布前确认口径。

## 能力承诺

- Lua 5.3 官方测试套件已通过，`_soft=true all.lua` 已输出 `final OK !!!`。
- `CGO_ENABLED=0 go test ./...`、`gopls check`、项目 Go 门禁脚本和未跟踪 Go 文件检查已通过。
- `glua` 支持 Lua 5.3 CLI 常用执行路径；`gluac` 支持标准 binary chunk 编译、输出和调试工具路径。
- binary chunk 首版承诺本项目 load/dump roundtrip、当前平台标准 chunk 读写和 Lua 5.3 语义字段兼容。
- Go 嵌入 API 支持显式注册 Go 函数、模块 table、`package.loaded`、`package.preload`、显式 table 构造、常量/变量注入、只读 table、显式对象代理、userdata 生命周期关闭、reflection 自动绑定和 Lua stub/代理代码生成。
- 库模式默认关闭宿主文件系统、环境变量和进程访问；CLI 普通模式按官方 Lua 使用习惯开启宿主访问，`-E` 继续屏蔽环境变量读取。
- Go `fs.FS` 只读虚拟文件系统可通过 `lua.Options.VirtualFilesystem` 接入，覆盖 `loadfile`、`dofile`、`require` Lua 文件 loader、只读 `io.open/io.lines` 以及对应 `file:read/file:lines`；默认 VFS 优先，`PreferHostFilesystem` 可在宿主文件系统授权后切换宿主优先。
- 项目核心与默认构建保持纯 Go、无 CGO，目标是避免跨系统编译困难；这不禁止宿主程序或可选扩展调用外部 `lib`、`.so`、`.dylib` 或 Windows `.dll`。嵌入方可以通过 `lua.Options.PackageDynamicLibraryLoader`、`lua.Options.PackageDynamicLibraryLoaderForState` 或 `stdlib/package` 环境注入可选 loader，也可以继续覆盖 `package.loadlib`、写入 `package.preload` 或替换 `package.searchers` 接入。
- 默认跨平台编译不需要额外 C 头文件、预装 `.so/.dylib/.dll`、Lua C API 开发包或系统动态库依赖；`package.cpath` 和动态库 loader 接入点只是运行期扩展协议，不进入默认构建链路。
- 已补充无 CGO 测试，验证宿主覆盖 `package.loadlib`、通过 `lua.Options` 注入动态库 loader，以及 C searcher 按 `package.cpath` 候选返回 Lua 可调用 loader 的链路。
- `native_modules` 是显式可选构建能力，需要 `CGO_ENABLED=1` 和 `-tags native_modules`；该构建当前已在 macOS arm64、Linux arm64、Windows amd64 与 Android arm64 上用仓库内 fixture、lua-cjson、LPeg 和 LuaSocket 完成目标平台源码构建与运行期验收。macOS 覆盖 `.so` / `.dylib`，Linux 覆盖 `.so`，Windows 覆盖 `.dll` 与 `lua53.dll` runtime shim/import library，Android 覆盖设备侧 `.so` 真实模块主路径。

## 已知限制

- 不承诺官方 Lua 5.3 binary chunk 跨端序、跨字长完全互通；跨架构互通需要独立测试矩阵后再承诺。
- Go `fs.FS` 虚拟文件系统首版仅承诺只读路径；写入、删除、重命名、临时文件、进程管道和环境变量仍由宿主权限开关控制，不映射到 VFS。
- 默认构建不内置 C 动态库加载器；未注入 loader 时，`package.loadlib`、C searcher 和 C root searcher 按纯 Go、无 CGO 策略返回明确不支持。注入 loader 后，Linux/macOS 候选覆盖 `.so`/`.dylib`，Windows 候选限定为运行期 `.dll`；`.lib`/import library 属于链接期产物，不作为 `require` 运行期候选。
- `native_modules` 只面向按 Lua 5.3 public C API 编写并导出 `luaopen_*` 的 Lua C 模块，不是任意动态库 FFI；依赖 `lstate.h`、`lobject.h` 等 Lua 内部头文件或访问 `lua_State` 内部布局的模块不属于兼容承诺。
- `native_modules` 已在 macOS arm64、Linux arm64 与 Windows amd64 上通过仓库内真实模块源码构建与运行期验收；其他 OS/架构组合、Android 真实第三方模块全量验收，以及未经源码固定的现成第三方二进制模块仍需独立矩阵确认。
- 当前 native C API 覆盖以 `lua-cjson` 和仓库 fixture 为验收基线；`lua_yieldk`、C continuation、debug hook C API、C 源码文件名/行号、动态库内部 C 调用栈、`lua_gc`、`lua_dump`、`lua_load` 等仍不作为发布承诺。
- Go reflection 自动绑定必须显式启用，不做默认隐式扫描；当前支持导出函数、导出字段、导出方法和基础类型/对象代理转换，不承诺 map/table 深拷贝或未导出成员访问。
- 只读 table 与常量字段通过 Lua 元方法保护普通写入；首版不承诺阻止宿主或 Lua debug/raw API 路径绕过元方法进行 raw 写入。
- Lua stub 生成不是 Go 源码到 Lua 源码的语义翻译；它只生成 Lua 侧代理层。
- 自定义加密 chunk 只提高通用工具直接反编译门槛，不承诺强 DRM、防破解、运行时防 dump 或可信执行。

## native_modules 安全边界

- 原生模块执行本机机器码，拥有与 `glua` 进程相同的文件、网络、环境变量和系统调用能力；本项目不把 native module 放入沙箱。
- 加载不可信 `.so/.dylib/.dll` 等同于执行不可信程序代码。生产环境应限制 `package.cpath`，优先使用可信绝对路径，避免让用户输入直接影响动态库搜索目录。
- C 模块中的未定义行为、越界写、全局状态竞争、线程安全问题或崩溃可能直接终止进程；Go VM 的 `pcall` 只能捕获通过 Lua C API 抛出的 Lua error，不能隔离 C 级内存破坏或进程崩溃。
- `native_modules` 构建允许 CGO 只服务 Lua C 模块加载；默认发布、默认测试和默认嵌入 API 仍以 `CGO_ENABLED=0` 为稳定路径。
- 真实第三方模块验收必须使用仓库内固定源码、许可证记录和仓库内 Lua 5.3 public headers，禁止测试时联网下载或依赖系统 Lua 开发包来掩盖可移植性问题。

## 后续增强建议

- 补充 VFS 使用文档和示例，说明路径清洗、宿主/VFS 优先级、只读边界和错误文本。
- 建立跨平台 binary chunk 互通测试矩阵，再决定是否提升兼容承诺。
- 补充官方文档级示例，展示宿主程序如何通过 `PackageDynamicLibraryLoaderForState` 注册 state-aware native loader 或替换 `package.loadlib`，同时保持默认构建 no-CGO。
- 补充 Go 封装 API 的端到端示例，展示 `RegisterModulePreload`、只读 table、object proxy、常量/变量注入和错误传播的推荐用法。
- 评估是否提供公开 native loader 适配包：要求默认 `CGO_ENABLED=0` 跨平台构建不受影响，平台相关实现必须隔离在显式 build tag、插件或宿主适配层中。
- 为 reflection 自动绑定补充更多官方文档级示例，说明字段/方法可见性、tag 命名、错误语义和性能边界。
