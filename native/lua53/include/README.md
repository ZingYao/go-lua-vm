# Lua 5.3.6 公共头文件

本目录保存 `native_modules` 可选构建使用的 Lua 5.3.6 public C API 头文件。

来源：

- `third_party/lua-5.3.6/lua.h`
- `third_party/lua-5.3.6/luaconf.h`
- `third_party/lua-5.3.6/lauxlib.h`
- `third_party/lua-5.3.6/lualib.h`

使用边界：

- 这些头文件只用于编译 Lua 5.3 C 扩展 fixture、native shim 和宿主侧兼容入口。
- 默认无 CGO 构建不依赖本目录。
- 本目录不保存 `lstate.h`、`lobject.h`、`lapi.h` 等 Lua 内部头文件；依赖内部结构的 C 模块不属于第一阶段兼容目标。
- Go VM 仍由本项目实现，不链接 Lua 官方 C VM。
- 为通过仓库 `git diff --check` 门禁，复制后删除了少量文件末尾多余空白行；public API 声明不变。
