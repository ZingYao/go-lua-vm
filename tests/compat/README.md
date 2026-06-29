# Lua 5.3 兼容测试

本目录承载 Lua 5.3 官方测试套件接入、CLI 对比测试输入、stdout/stderr golden 与后续退出码 golden。

## 官方测试套件

- 官方测试源：`third_party/lua-5.3.6/testes/`
- 本地运行入口：`scripts/run-official-tests.sh`
- 默认被测二进制：`./bin/glua`
- 可通过 `GLUA_BIN=/path/to/glua` 指定被测实现。

官方测试套件中包含 C 扩展测试文件。由于本项目禁止 CGO，这类测试只作为参考资产，不接入 Go 构建链路；执行脚本会保留跳过策略入口，后续按纯 Go 能力成熟度逐步打开。

## CLI golden 对比

- 对比脚本：`scripts/compare-cli-golden.sh`
- 输入脚本目录：`tests/compat/cases/`
- stdout golden：`tests/compat/golden/stdout/`
- stderr golden：`tests/compat/golden/stderr/`
- 退出码 golden：`tests/compat/golden/exitcode/`

每个 case 使用同名文件建立 golden。例如 `tests/compat/cases/hello.lua` 对应：

- `tests/compat/golden/stdout/hello.out`
- `tests/compat/golden/stderr/hello.err`
- `tests/compat/golden/exitcode/hello.code`

`compare-cli-golden.sh` 会同时校验 stdout、stderr 与退出码。golden 文件不存在时会先用官方 `lua` 输出生成，再将本项目 `glua` 输出与 golden 对比。
