# 首版发布限制

本文记录首个 release 的能力承诺与已知限制。结论基于当前代码实现、测试结果和发布前确认口径。

## 能力承诺

- Lua 5.3 官方测试套件已通过，`_soft=true all.lua` 已输出 `final OK !!!`。
- `CGO_ENABLED=0 go test ./...`、`gopls check`、项目 Go 门禁脚本和未跟踪 Go 文件检查已通过。
- `glua` 支持 Lua 5.3 CLI 常用执行路径；`gluac` 支持标准 binary chunk 编译、输出和调试工具路径。
- binary chunk 首版承诺本项目 load/dump roundtrip、当前平台标准 chunk 读写和 Lua 5.3 语义字段兼容。
- Go 嵌入 API 支持显式注册 Go 函数、显式对象代理、reflection 自动绑定和 Lua stub/代理代码生成。
- 库模式默认关闭宿主文件系统、环境变量和进程访问；CLI 普通模式按官方 Lua 使用习惯开启宿主访问，`-E` 继续屏蔽环境变量读取。
- 项目核心与默认构建保持纯 Go、无 CGO，目标是避免跨系统编译困难；这不禁止宿主程序或可选扩展调用外部 `lib`、`.so`、`.dylib` 或 Windows `.dll`。嵌入方可以通过 `lua.Options.PackageDynamicLibraryLoader` 或 `stdlib/package` 环境注入可选 loader，也可以继续覆盖 `package.loadlib`、写入 `package.preload` 或替换 `package.searchers` 接入。
- 已补充无 CGO 测试，验证宿主覆盖 `package.loadlib`、通过 `lua.Options` 注入动态库 loader，以及 C searcher 按 `package.cpath` 候选返回 Lua 可调用 loader 的链路。

## 已知限制

- 不承诺官方 Lua 5.3 binary chunk 跨端序、跨字长完全互通；跨架构互通需要独立测试矩阵后再承诺。
- 当前尚未完整支持 Go `fs.FS` 虚拟文件系统；已有宿主文件系统权限开关和 `package` Lua 文件 loader 扩展点，但不能宣称首版已支持完整 VFS。
- 首版默认不内置 C 动态库加载器；未注入 loader 时，`package.loadlib`、C searcher 和 C root searcher 按纯 Go、无 CGO 策略返回明确不支持。注入 loader 后，Linux/macOS 候选覆盖 `.so`/`.dylib`，Windows 候选限定为运行期 `.dll`；`.lib`/import library 属于链接期产物，不作为 `require` 运行期候选。
- Go reflection 自动绑定必须显式启用，不做默认隐式扫描；当前支持导出函数、导出字段、导出方法和基础类型/对象代理转换，不承诺 map/table 深拷贝或未导出成员访问。
- Lua stub 生成不是 Go 源码到 Lua 源码的语义翻译；它只生成 Lua 侧代理层。
- 自定义加密 chunk 只提高通用工具直接反编译门槛，不承诺强 DRM、防破解、运行时防 dump 或可信执行。

## 后续增强建议

- 为 Go `fs.FS` 设计只读虚拟文件系统入口，优先覆盖 `loadfile`、`dofile`、`require` 和只读 `io.open/io.lines`。
- 建立跨平台 binary chunk 互通测试矩阵，再决定是否提升兼容承诺。
- 补充官方文档级示例，展示宿主程序如何通过 `PackageDynamicLibraryLoader` 注册自定义 C loader 或替换 `package.loadlib`，同时保持本仓库 no-CGO。
- 评估平台动态库 loader 示例包：要求默认 `CGO_ENABLED=0` 跨平台构建不受影响，平台相关实现必须隔离在显式 build tag、插件或宿主适配层中。
- 为 reflection 自动绑定补充更多官方文档级示例，说明字段/方法可见性、tag 命名、错误语义和性能边界。
