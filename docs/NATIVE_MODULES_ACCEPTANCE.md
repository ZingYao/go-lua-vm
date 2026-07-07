# native_modules 验收记录

本文记录 `native_modules` 可选构建的真实模块验收状态。它是当前验收台账，不等同于全平台最终完成声明；Linux 与 Windows 仍需按 TODO 继续闭环。

## 当前记录

- 日期：2026-07-07
- 分支：`quanquan/feature/glua-native-module-loader`
- 默认构建边界：默认无 build tag、`CGO_ENABLED=0` 路径不启用 native loader；标准库语义修复仍必须通过默认 no-CGO 门禁。
- native 构建边界：`CGO_ENABLED=1 -tags native_modules`，只承诺按 Lua 5.3 public C API 编写并导出 `luaopen_*` 的 C 模块。

## macOS arm64

当前 macOS arm64 已完成源码自包含构建与运行期验收：

| 模块 | 脚本 | 后缀 | 验收点 |
| --- | --- | --- | --- |
| fixture | `scripts/test-native-modules.sh` | `.so`、`.dylib` | `require` 成功路径、`luaopen_*` 初始化失败、userdata/metatable/registry/error smoke |
| lua-cjson | `scripts/test-native-cjson.sh` | `.so`、`.dylib` | ABI 符号由 native `glua` shim 覆盖、`require("cjson")`、`encode/decode`、错误输入 `pcall` |
| LPeg | `scripts/test-native-lpeg.sh` | `.so`、`.dylib` | `require("lpeg")`、基础 pattern/match；完整 `third_party/lpeg/test.lua` 已在排查闭环中通过 |
| LuaSocket | `scripts/test-native-luasocket.sh` | `.so`、`.dylib` | `require("mime")`、MIME 编解码、`require("socket")`、TCP echo loopback、UDP sendto/receivefrom loopback |
| 当前平台总验收 | `scripts/test-native-real-modules.sh` | `.so`、`.dylib` | 串联 fixture、lua-cjson、LPeg 和 LuaSocket 运行期验收，用于本机或 CI 一次性回归 |

最近一次本机真实模块总验收：

```bash
source ~/.zshrc && CGO_ENABLED=1 ./scripts/test-native-real-modules.sh
```

结果：2026-07-07 自动轮次复跑通过；macOS arm64 `.so` 与 `.dylib` 均通过 fixture、lua-cjson、LPeg 和 LuaSocket runtime acceptance。

额外诊断样本：

- 2026-07-07 使用外部临时副本 `/tmp/glua-lua-sandbox-extensions` 运行 LPeg 日志解析 suite，覆盖 `common_log_format`、`date_time`、`logfmt`、`lpeg_heka`、`mysql`、`phabricator`、`postfix`、`printf`、`uri` 等上层 Lua grammar 对 native LPeg 的压力路径。
- 该样本需要临时 Lua 5.1/旧 LPeg 兼容垫片（`setfenv`、`_G.lpeg` 别名、`TZ=UTC`、纳秒 double 期望容差和测试路径补齐），因此不作为仓库内正式验收门禁；它用于证明本轮修复后的 native LPeg C frame、`os.time` / `os.date` 标准库语义可支撑真实日志 grammar。

最近一次 native Go 门禁：

```bash
CGO_ENABLED=1 go test -tags native_modules ./...
```

结果：通过。

## Linux

Linux `.so` 是目标支持面，但当前记录尚未声明 Linux 实机运行期通过：

- `scripts/build-native-cjson.sh`、`scripts/build-native-lpeg.sh`、`scripts/build-native-luasocket.sh` 均设计为 Linux 输出 `.so`。
- `scripts/test-native-cjson.sh`、`scripts/test-native-lpeg.sh`、`scripts/test-native-luasocket.sh` 均包含 Linux `.so` 运行期入口。
- 仍需在 Linux 主机或等价 CI 环境执行真实运行期验收，并记录结果。

## Windows

Windows `.dll` 是目标支持面，但当前记录尚未声明 Windows 运行期通过：

- `LoadLibraryW` / `GetProcAddress` loader 代码路径已存在。
- 真实模块构建与运行期脚本在 `lua53.dll` shim 或等价 import library 落地前明确 `skip`。
- `scripts/check-native-skip-reasons.sh` 已覆盖 Windows fixture、cjson、LPeg、LuaSocket build/runtime 和真实模块总验收入口的 skip 文本，防止不可用平台静默通过。

## 不可夸大的结论

- 通过上述验收不代表任意动态库都能被 `require`；模块必须是 Lua 5.3 public C API 模块并导出 `luaopen_*`。
- 不承诺依赖 Lua 内部头文件或访问 `lua_State` 内部结构的模块兼容。
- 不承诺完整 Lua 5.3 C API 已覆盖；兼容范围以 `docs/NATIVE_MODULES_PLAN.md` 和现有 shim 实现为准。
- 默认 no-CGO 构建仍必须独立通过 `CGO_ENABLED=0 go test ./...` 与 `./scripts/check-go-gates.sh`，native 验收不能替代默认构建门禁。
