# IO 与 OS 标准库宿主访问策略

## 默认策略

`io`、`os` 和 `package` 标准库默认不允许访问宿主文件系统、读取宿主环境变量或创建宿主进程。这个默认值用于库模式嵌入场景，避免脚本在调用方没有显式授权时读取、写入或删除宿主资源。

CLI 普通模式会显式开启 `AllowHostFilesystem`、`AllowProcess` 和 `AllowEnvironment`，用于兼容官方 Lua 5.3 测试套件及常规命令行行为；CLI `-E` 模式会继续屏蔽环境变量读取。标准流属于进程已经打开的句柄，不等价于允许任意文件系统访问。

## Sandbox 选项设计

`lua.Options` 与底层 `runtime.Options` 当前承载以下已实现策略字段：

- `AllowHostFilesystem bool`：是否允许 `io.open`、`io.input`、`io.output`、`io.lines`、`io.tmpfile`、`os.remove`、`os.rename`、`os.tmpname` 等访问宿主文件系统。
- `AllowProcess bool`：是否允许 `io.popen`、`os.execute` 等启动宿主进程。
- `AllowEnvironment bool`：是否允许 `os.getenv` 读取宿主环境变量。

后续仍需补齐以下更细粒度策略字段：

- `AllowedRoots []string`：允许访问的根目录集合；为空且启用文件系统时表示由宿主自行承担全局授权风险。
- `StandardStreams bool`：是否注册 stdin/stdout/stderr；默认启用，关闭后 `io` 库只注册纯函数和后续明确授权的文件句柄。

所有路径型 API 必须先规范化路径，再检查是否落在允许根目录内。进程型 API 默认关闭，启用时必须保留命令、参数和工作目录的审计入口。

## 当前实现边界

- `io.close(file)` 只关闭显式传入的 file userdata；标准流关闭为 no-op，避免关闭宿主进程 stdio。
- `io.close()` 按当前默认输出处理；默认输出为标准流时保持 no-op。
- `io.flush(file)` 优先调用 file userdata 的 flush 能力；标准流当前 flush 为 no-op，避免对管道或终端执行不稳定的底层同步。
- `io.flush()` 刷新当前默认输出；默认输出为标准流时保持 no-op。
- `io.input(file)` 与 `io.output(file)` 已支持在 file userdata 之间切换默认输入输出；传入字符串路径时受 `AllowHostFilesystem` 控制。
- `io.lines()` 已支持从当前默认输入逐行迭代，支持 `io.lines(nil, formats...)` 保留格式参数；传入字符串路径时受 `AllowHostFilesystem` 控制，路径型 iterator 在 EOF 后自动关闭并在再次调用时报 closed file 错误。
- `io.open(...)` 已支持 Lua 5.3 参数校验和宿主文件打开；路径访问受 `AllowHostFilesystem` 控制，失败返回 `nil, message, code`。
- `io.popen(...)` 当前只做 Lua 5.3 参数校验；宿主进程访问受 `AllowProcess` 控制。
- `io.read(...)` 已支持当前默认输入上的 `*l`、`*L`/`L`、`*a`、`*n`/`n` 和非负整数 byte count；数字读取按 Lua 5.3 扫描终止符，超长数字失败时保留剩余行，EOF 上 `read(0)` 返回 nil。
- `io.write(...)` 已支持向当前默认输出写入 string、integer 和 number，成功时返回当前输出 file。
- `io.tmpfile()` 已支持创建宿主临时文件；访问受 `AllowHostFilesystem` 控制。
- `io.type(value)` 已支持识别 file 与 closed file。
- file `:close` 已提供方法入口；普通 file 显式重复 close 返回 closed file 错误，底层 Close 仍保持幂等供 GC/finalizer 使用。
- file `:flush`、`:lines`、`:read`、`:seek`、`:setvbuf` 已提供方法入口；`:lines` 支持多格式读取与 250 参数上限，`:seek("cur")` 会扣除行缓冲预读字节，`:setvbuf` 当前只做 mode/size 参数校验并返回成功，不改变 Go 层缓冲策略。
- file `:write` 已支持向目标 file 写入 string、integer 和 number，成功时返回 file 自身；不可写但未关闭的 file 按普通 I/O 失败返回 `nil, message, code`。
- `os.clock` 使用纯 Go 单调 elapsed time 近似 Lua 5.3 CPU time。
- `os.date` 已支持常用 strftime 子集和 `*t` table 输出，`!` 前缀使用 UTC。
- `os.difftime` 已支持两个 Unix 秒时间戳的差值计算。
- `os.execute` 当前默认禁用宿主进程；无参数查询 shell 可用性在禁用时返回 false，传入命令在禁用时返回进程禁用错误。
- `os.exit` 在嵌入模式下不直接终止宿主进程，而是返回携带退出码的 Lua error；后续 CLI 层负责映射为真实进程退出。
- `os.getenv` 默认禁用宿主环境变量访问；启用 `AllowEnvironment` 后读取 `os.LookupEnv`。
- `os.remove`、`os.rename` 默认禁用宿主文件系统写入；启用 `AllowHostFilesystem` 后执行宿主文件操作，普通失败返回 `nil, message, code`。
- `os.setlocale` 当前固定为 C locale；设置其他 locale 返回 nil。
- `os.time` 已支持当前 Unix 秒和 date table 转 Unix 秒。
- `os.tmpname` 默认禁用宿主临时路径访问；启用 `AllowHostFilesystem` 后返回宿主临时路径。
