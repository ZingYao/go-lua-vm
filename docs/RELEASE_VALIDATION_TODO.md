# 发布增强验证 TODO

本文跟踪第 29 节发布增强的可执行验证清单。每个条目必须在实现或文档变更后保持命令可复跑、夹具可定位、期望结果可判定、失败诊断口径明确。

## 执行约定

- 所有 Go 命令默认在仓库根目录执行，且 PATH 上的 `go` 必须是 `go1.26.4`。
- 默认构建、测试、benchmark 均使用 `CGO_ENABLED=0`，除非条目明确标注为宿主可选动态库 loader 验证。
- 涉及 Go 代码理解、符号引用或诊断时，优先使用 gopls MCP；若 MCP 不可用，使用命令行 `gopls check` 并在交付说明记录命令和结果。
- 平台列中 `all` 表示 Linux、macOS、Windows 都需要执行；`host` 表示当前开发机执行即可；`matrix` 表示至少覆盖 `scripts/release-glua.sh` 内声明的目标平台构建。

## 基线门禁

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| GATE-01 | Go 版本、无 CGO、未跟踪 Go 文件和全量单测 | `./scripts/check-go-gates.sh` | 全仓 Go 文件 | 命令零退出，且 `CGO_ENABLED=0 go test ./...` 全部通过 | 先看 Go 版本是否为 `go1.26.4`，再看 `import "C"` 命中、未跟踪 Go 文件、具体失败包 | host |
| GATE-02 | 全仓 gopls 诊断 | `git ls-files '*.go' \| xargs gopls check` | 全仓 Go 文件 | 命令零退出，无新增诊断 | 若失败，按文件路径和诊断行定位；若与 `go test` 冲突，记录两者差异并以可复现编译结果为准 | host |
| GATE-03 | 未跟踪 Go 文件检查 | `git ls-files --others --exclude-standard \| rg '(_test)?\.go$'` | Git 工作树 | 无输出，命令非零退出也可接受 | 若有输出，必须 `git add` 对应 Go 文件或说明为何不纳入交付 | host |
| GATE-04 | 全仓 benchmark smoke | `CGO_ENABLED=0 go test -bench=. ./...` | 现有 benchmark | 命令零退出，结果用于观察退化，不要求固定数值 | 若超时或退化，先拆到 `./runtime`、`./bridge` 定位，再对比 `docs/BENCHMARK.md` 基线 | host |
| GATE-05 | benchmark 文档回归 | `CGO_ENABLED=0 go test ./runtime -run=^$ -bench=. -benchtime=100ms` | `runtime/benchmark_test.go` | 输出包含 VM dispatch、Table、函数调用、字符串拼接、Go/Lua 回调基准 | 若新增热点未覆盖，需要先补 benchmark 或在发布文档中说明风险 | host |

## Go 嵌入 API

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| API-01 | State 生命周期、OpenLibs、DoString、DoFile、LoadBinary | `CGO_ENABLED=0 go test ./lua -run Test` | `lua/api_test.go` | 单测零退出，无泄漏到宿主权限默认开启路径 | 先区分 parser/codegen/load/runtime 错误；确认 context 和权限选项是否按测试初始化 | host |
| API-02 | Go 函数注册和 Lua 调 Go | `CGO_ENABLED=0 go test ./lua ./bridge -run Test` | `lua/api_test.go`、`bridge/bridge_test.go` | Lua 可调用 Go 函数，多返回值、error、panic 边界稳定 | 若 Lua error 文本变化，检查 bridge 错误包装和 traceback 是否仍可判断 | host |
| API-03 | Go 调 Lua 与嵌套回调 | `CGO_ENABLED=0 go test ./bridge -run Test` | `bridge/bridge_test.go` | Go -> Lua -> Go 与 Lua -> Go -> Lua 均成功或按预期返回错误 | 先看调用帧恢复、栈顶数量、context 取消和 panic recover 边界 | host |

## CLI

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| CLI-01 | 构建 `glua` | `./scripts/build-glua.sh bin/glua` | `cmd/glua`、`internal/cli` | 输出 `built bin/glua`，产物可执行 | 若失败，先确认 Go 版本、CGO 关闭、CLI 包编译错误 | host |
| CLI-02 | CLI golden | `./scripts/compare-cli-golden.sh` | `tests/golden` 与临时 CLI 脚本 | stdout、stderr、退出码与 golden 对齐 | 按 stdout/stderr/exit code 三类拆分；错误文本变化需判断兼容性或更新 golden | host |
| CLI-03 | `glua` 常用参数 | `CGO_ENABLED=0 go test ./internal/cli -run Test` | `internal/cli/cli_test.go` | `-e`、`-l`、`-i`、`-v`、脚本参数和 `-E` 权限语义稳定 | 若失败，先看参数解析、权限选项、stdin/stdout/stderr 注入 | host |
| CLI-04 | `gluac` 常用参数 | `CGO_ENABLED=0 go test ./internal/luac -run Test` | `internal/luac/luac_test.go` | `-l`、`-o`、`-p`、`-s`、`-v` 行为稳定 | 若失败，区分源码编译、binary chunk load/dump、反汇编输出变化 | host |

## VFS

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| VFS-01 | `lua.Options` VFS API 设计验收 | `go test ./lua -run TestVFSOptions` | 后续新增 `lua/vfs_test.go` | Options 清晰表达只读/可写边界、宿主优先级和路径清洗策略 | 若 API 泄露 runtime 内部类型或无法表达权限边界，先回退设计 | host |
| VFS-02 | `loadfile`/`dofile` 读取虚拟文件 | `CGO_ENABLED=0 go test ./lua -run TestVFSLoadFileDoFile` | `fstest.MapFS` Lua 文件 | 宿主文件系统关闭时仍可从 VFS 读取并执行；错误文本含虚拟路径 | 若失败，定位 base `loadfile`、State 文件读取抽象和权限选项传递 | host |
| VFS-03 | `require` Lua 文件 loader 读取虚拟模块 | `CGO_ENABLED=0 go test ./lua ./stdlib/package -run TestVFSRequire` | `fstest.MapFS` 模块树 | `require("a.b")` 命中 VFS，`package.loaded` 缓存稳定，子目录模块可加载 | 先看 `package.path` 展开、路径清洗、模块名到路径映射和 loader vararg | host |
| VFS-04 | 只读 `io.open`/`io.lines`/`file:read` | `CGO_ENABLED=0 go test ./stdlib/io ./lua -run TestVFSReadOnlyIO` | `fstest.MapFS` 文本与二进制文件 | 只读读取成功，写入和追加返回明确权限错误三返回 | 区分 io 库 mode 校验、虚拟文件 reader、文件 userdata close 状态 | host |
| VFS-05 | VFS 安全边界 | `CGO_ENABLED=0 go test ./lua ./stdlib/package ./stdlib/io -run TestVFSPathTraversal` | 包含 `..`、绝对路径、空路径、重复分隔符的用例 | 路径穿越被拒绝，错误文本稳定，不回退到宿主文件系统 | 若失败，检查路径 clean 后是否仍在 FS 根内，确认 Windows 分隔符处理 | all |

## require 与 package

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| REQ-01 | `package.preload` 与 Lua closure loader | `CGO_ENABLED=0 go test ./lua -run TestPackagePreloadLuaClosureLoader` | `lua/api_test.go` | loader 接收模块名，返回值写入 `package.loaded`，重复 require 走缓存 | 先看 searcher 返回值、loader caller、loaded sentinel 和错误包装 | host |
| REQ-02 | Lua 文件 loader 错误聚合 | `CGO_ENABLED=0 go test ./stdlib/package ./lua -run Test` | `stdlib/package/package_test.go` | 未找到模块时聚合候选路径，语法错误不被吞掉 | 检查 searcher 顺序、`package.path` 展开和 chunk name/source 行号 | host |
| REQ-03 | VFS 与宿主优先级 | `CGO_ENABLED=0 go test ./lua -run TestRequireVFSHostPriority` | 同名 VFS 文件与宿主临时文件 | 按设计口径优先命中 VFS 或宿主，并在文档中固定 | 若行为摇摆，先补 `docs/API.md` 和测试命名说明优先级 | host |

## 动态库 loader

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| DYN-01 | 默认无 CGO 不启用动态库 | `CGO_ENABLED=0 go test ./lua ./stdlib/package -run Test` | `lua/api_test.go`、`stdlib/package/package_test.go` | 默认 `package.loadlib`、C searcher、C root searcher 返回明确不支持，不打开真实动态库 | 若误打开动态库或依赖 CGO，立即回滚默认路径并补 no-CGO 检查 | all |
| DYN-02 | 宿主覆盖 `package.loadlib` | `CGO_ENABLED=0 go test ./lua -run TestPackageLoadLibCanBeOverriddenByHostLoader` | `lua/api_test.go` | 覆盖后的 loader 可被 Lua 调用一次，返回宿主模拟模块值 | 若失败，检查全局 `package.loadlib` 覆盖、Go closure equality 和 loader 返回值调用 | host |
| DYN-03 | 可选动态库 loader 接入点设计 | `go test ./lua -run TestDynamicLoaderOptions` | 后续新增设计测试 | 默认零值禁用；显式注入 loader 才按 filename/symbol 返回 Lua callable | 若 API 需要宿主插件，测试必须不破坏默认 no-CGO 路径 | host |
| DYN-04 | 分平台候选扩展名和诊断文本 | `CGO_ENABLED=0 GOOS=<os> GOARCH=amd64 go test ./stdlib/package -run TestDynamicLibraryCandidates` | Linux/macOS/Windows 候选表 | Linux 包含 `.so`，macOS 包含 `.dylib`，Windows 运行期包含 `.dll` 且 `.lib` 只作为不支持边界说明 | 若交叉测试受限，至少把候选生成逻辑做成纯函数并按 GOOS 覆盖 | all |
| DYN-05 | CGO 与宿主适配层验证 | `CGO_ENABLED=1 go test ./...` | native loader 与后续可选宿主包 | CGO 构建自动启用平台 loader，宿主适配仍需显式注入 | 若引入外部依赖，必须在文档中标明其构建与发布边界 | matrix |

## reflection 自动绑定

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| REF-01 | 自动绑定方案文档 | `rg -n -e reflection -e 反射 -e 可见性 -e tag -e panic docs/BRIDGE.md docs/API.md` | 设计文档 | 可见性、命名、tag、receiver、字段权限、错误语义和性能边界均有说明 | 若设计缺项，禁止进入实现；先补文档和测试名 | host |
| REF-02 | 导出函数自动转 Lua callable | `CGO_ENABLED=0 go test ./bridge ./lua -run TestReflectFunctionBinding` | 后续新增反射函数夹具 | 参数转换、多返回值、`error` 返回、panic recover 均符合设计 | 若失败，先看 reflect 类型转换、Lua nil/number/integer 规则和错误包装 | host |
| REF-03 | struct 字段与方法自动绑定 | `CGO_ENABLED=0 go test ./bridge ./lua -run TestReflectStructBinding` | 后续新增 struct 夹具 | 导出字段读写、导出方法调用、指针和值 receiver、嵌入字段、tag 重命名可用 | 若失败，检查不可导出字段拒绝、nil receiver、地址可取性和 method set | host |
| REF-04 | 自动绑定安全边界 | `CGO_ENABLED=0 go test ./bridge ./lua -run TestReflectBindingSafety` | 不可导出字段、循环引用、nil receiver、panic 夹具 | 不可导出成员不可见，循环引用不死循环，nil receiver 返回明确错误 | 若失败，先回退默认禁用自动绑定，保持显式绑定能力不受影响 | host |

## Go table/object 封装与常量变量注入

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| WRAP-01 | 统一注册 API 设计 | `rg -n -e RegisterModule -e ObjectBinding -e 常量 -e 变量 -e 只读 docs/BRIDGE.md docs/API.md` | 设计文档 | 注册函数、table、object、常量、变量和覆盖策略均有明确入口 | 若 API 分散或命名冲突，先收敛到 `lua` 稳定入口再实现 | host |
| WRAP-02 | Go 函数注册到全局、模块、`package.loaded`、`package.preload` | `CGO_ENABLED=0 go test ./bridge ./lua -run TestRegisterFunctionDestinations` | 后续新增注册夹具 | 四种目的地均可被 Lua 调用，require 返回模块表 | 先看 package 库是否已打开、模块名冲突和覆盖策略 | host |
| WRAP-03 | Go table 封装 | `CGO_ENABLED=0 go test ./bridge ./lua -run TestGoTableBinding` | 嵌套 table、方法、metatable、只读字段夹具 | Lua 可读写允许字段，调用方法，禁止写只读 table | 若失败，检查 table 构造顺序、metatable `__index/__newindex` 和错误文本 | host |
| WRAP-04 | Go object proxy | `CGO_ENABLED=0 go test ./bridge ./lua -run TestObjectProxy` | `bridge/bridge_test.go` 和后续 object 夹具 | 冒号调用、字段访问、生命周期关闭、错误传播稳定 | 若失败，检查 userdata identity、方法 receiver、State 关闭 finalizer 和栈恢复 | host |
| WRAP-05 | 常量与变量注入 | `CGO_ENABLED=0 go test ./bridge ./lua -run TestInjectConstantsVariables` | string、bool、integer、number、nil、table、function、userdata 夹具 | 只读常量不可改，可变变量可更新，类型转换符合 Lua 5.3 语义 | 若失败，先看 raw set 路径、metatable 保护和 Go/Lua 值转换 | host |

## 官方兼容回归

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| COMPAT-01 | 构建官方回归用 `glua` | `./scripts/build-glua.sh bin/glua` | `cmd/glua` | `bin/glua` 存在且可执行 | 若失败，不进入官方套件；先修复 CLI 构建 | host |
| COMPAT-02 | 官方 Lua 5.3 套件全量回归 | `GLUA_BIN=./bin/glua ./scripts/run-official-tests.sh` | `third_party/lua-5.3.6/testes/all.lua` | 零退出；软模式按既有记录应输出 `final OK !!!` | 若失败，先单文件复跑断点脚本，再按 TODO 中官方套件历史断点定位 | host |
| COMPAT-03 | 官方套件重点脚本单文件回归 | `GLUA_BIN=./bin/glua ./scripts/run-official-tests.sh files.lua db.lua gc.lua attrib.lua locals.lua constructs.lua strings.lua math.lua bitwise.lua` | 官方测试套件重点脚本 | 每个脚本零退出并输出既有 `OK` 或等价通过标志 | 按脚本名定位到 stdlib、debug、compiler、runtime 对应模块 | host |
| COMPAT-04 | binary chunk roundtrip 与反汇编 | `CGO_ENABLED=0 go test ./bytecode ./internal/luac -run Test` | `tests/golden/bytecode_roundtrip.txt`、luac 测试夹具 | chunk load/dump、strip、list、反汇编稳定 | 若失败，区分 header、常量、Proto、调试信息和 CLI 展示变化 | host |

## 跨平台构建

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| XPLAT-01 | release 矩阵构建 | `GLUA_RELEASE_VERSION=validation ./scripts/release-glua.sh dist/validation` | `scripts/release-glua.sh` | 生成 darwin/amd64、darwin/arm64、linux/amd64、linux/arm64、windows/amd64 的压缩包 | 若失败，先看 GOOS/GOARCH 编译错误、路径分隔符、Windows `.exe` 命名 | matrix |
| XPLAT-02 | no-CGO 交叉编译单包 smoke | `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go test ./...` | 全仓 Go 包 | 支持 `go test` 的目标平台零退出；不支持执行测试的平台至少完成 `go test -run=^$` 编译检查 | 若执行受宿主限制，记录限制并补 `go test -run=^$` 或 `go test -c` 编译验证 | matrix |
| XPLAT-03 | 平台路径与权限差异 | `CGO_ENABLED=0 go test ./stdlib/io ./stdlib/os ./stdlib/package -run Test` | 平台路径、权限、环境变量夹具 | Unix 与 Windows 路径、环境和进程权限错误文本在设计范围内 | 若失败，确认是否是平台真实差异；必要时用 GOOS 纯函数测试补齐 | all |

## 发布文档

| ID | 验证项 | 命令 | 输入夹具 | 期望输出 | 失败诊断口径 | 平台 |
| --- | --- | --- | --- | --- | --- | --- |
| DOC-01 | 发布限制同步 | `rg -n -e VFS -e 动态库 -e reflection -e 反射 -e stub -e CGO -e 'binary chunk' docs/RELEASE_LIMITS.md docs/PLAN.md README.md` | 发布文档 | 能力承诺和已知限制与实现状态一致，无过度承诺 | 若文档宣称未实现能力，必须改为限制或补实现测试 | host |
| DOC-02 | API 与 Bridge 示例同步 | `rg -n -e OpenLibs -e Register -e RegisterModule -e ObjectBinding -e GenerateLuaStub -e 'fs\.FS' -e loadlib docs/API.md docs/BRIDGE.md` | API 与 Bridge 文档 | 示例能对应真实 API 或明确为规划草案 | 若示例不可编译，改为规划描述或补 examples 编译测试 | host |
| DOC-03 | benchmark 结论同步 | `rg -n -e Benchmark -e 基线 -e glua -e luac -e '官方 Lua' docs/BENCHMARK.md` | benchmark 文档 | 记录命令、环境、结果和结论；新增性能风险有说明 | 若 benchmark 命令变化，必须同步 `Makefile` 或文档命令 | host |
| DOC-04 | TODO 状态同步 | `sed -n '/^## 29/,$p' TODO.md` | `TODO.md` | 第 29 节状态只标记已验证完成的条目，未实现能力保持未勾选 | 若 TODO 与代码状态不一致，优先回滚 TODO 勾选并补说明 | host |
