# 首版发布限制

本文记录首个 release 的能力承诺与已知限制。结论基于当前代码实现、测试结果和发布前确认口径。

## 能力承诺

- Lua 5.3 官方测试套件已通过，`_soft=true all.lua` 已输出 `final OK !!!`。
- `CGO_ENABLED=0 go test ./...`、`gopls check`、项目 Go 门禁脚本和未跟踪 Go 文件检查已通过。
- `glua` 支持 Lua 5.3 CLI 常用执行路径；`gluac` 支持标准 binary chunk 编译、输出和调试工具路径。
- binary chunk 首版承诺本项目 load/dump roundtrip、当前平台标准 chunk 读写和 Lua 5.3 语义字段兼容。
- Go 嵌入 API 支持显式注册 Go 函数、显式对象代理和 Lua stub/代理代码生成。
- 库模式默认关闭宿主文件系统、环境变量和进程访问；CLI 普通模式按官方 Lua 使用习惯开启宿主访问，`-E` 继续屏蔽环境变量读取。
- Go `fs.FS` 只读虚拟文件系统可通过 `lua.Options.VirtualFilesystem` 接入，覆盖 `loadfile`、`dofile`、`require` Lua 文件 loader、只读 `io.open/io.lines` 以及对应 `file:read/file:lines`；默认 VFS 优先，`PreferHostFilesystem` 可在宿主文件系统授权后切换宿主优先。
- 项目核心与默认构建保持纯 Go、无 CGO，目标是避免跨系统编译困难；这不禁止宿主程序或可选扩展调用外部 `lib`、`.so`、`.dylib`。嵌入方可以在自己的宿主程序中用 CGO、插件、系统动态库机制或后续可选 loader 实现自定义 C loader，并通过覆盖 `package.loadlib`、写入 `package.preload` 或替换 `package.searchers` 接入。
- 已补充无 CGO 测试，验证宿主覆盖 `package.loadlib` 后 Lua 侧可以获取并执行第三方 C 动态库 loader 形态的入口函数。

## 已知限制

- 不承诺官方 Lua 5.3 binary chunk 跨端序、跨字长完全互通；跨架构互通需要独立测试矩阵后再承诺。
- Go `fs.FS` 虚拟文件系统首版仅承诺只读路径；写入、删除、重命名、临时文件、进程管道和环境变量仍由宿主权限开关控制，不映射到 VFS。
- 首版默认不内置 C 动态库加载器；默认 `package.loadlib`、C searcher 和 C root searcher 按纯 Go、无 CGO 策略返回明确不支持，但这不限制用户在宿主程序侧或可选扩展中自行接入外部动态库加载能力。
- 不支持 Go reflection 自动绑定；首版只支持显式注册 Go 函数、显式对象代理和基于绑定信息的 Lua stub 生成。
- Lua stub 生成不是 Go 源码到 Lua 源码的语义翻译；它只生成 Lua 侧代理层。
- 自定义加密 chunk 只提高通用工具直接反编译门槛，不承诺强 DRM、防破解、运行时防 dump 或可信执行。

## 后续增强建议

- 补充 VFS 使用文档和示例，说明路径清洗、宿主/VFS 优先级、只读边界和错误文本。
- 建立跨平台 binary chunk 互通测试矩阵，再决定是否提升兼容承诺。
- 补充官方文档级示例，展示宿主程序如何注册自定义 C loader 或替换 `package.loadlib`，同时保持本仓库 no-CGO。
- 评估后续可选动态库 loader：要求默认 `CGO_ENABLED=0` 跨平台构建不受影响，平台相关实现必须隔离在显式 build tag、插件或宿主适配层中。
- 如需 reflection 自动绑定，先设计字段/方法可见性、权限、命名、错误语义和性能边界。
