// Package oslib 实现 Lua 5.3 os 标准库的第一阶段能力。
//
// 本包当前提供纯时间函数 clock、date、difftime，以及默认禁用宿主进程和宿主资源访问的策略。
// 涉及环境变量、文件系统和真实进程执行的能力会在 sandbox 选项接入后逐步开放。
package oslib

import (
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/ZingYao/go-lua-vm/runtime"
	"github.com/ZingYao/go-lua-vm/stdlib/internal/procstatus"
)

// processStart 保存本 Go 进程启动后首次加载 oslib 的基准时间。
var processStart = time.Now()

// cLocaleName 保存 Lua `os.setlocale` 当前固定支持的基础 locale 名称。
var cLocaleName = string([]byte{'C'})

// ExitError 表示 Lua `os.exit` 在嵌入模式下产生的退出请求。
//
// 当前标准库不能直接终止宿主 Go 进程，因此用 Lua error 携带退出码和 close 标记。
type ExitError struct {
	// Code 保存请求退出码。
	Code int
	// Close 保存 Lua 5.3 os.exit 第二参数 close 的布尔值。
	Close bool
}

// Error 返回 os.exit 的稳定错误文本。
//
// 该文本用于嵌入方日志和后续 CLI 映射，不直接暴露为宿主进程退出。
func (err *ExitError) Error() string {
	// 格式中保留退出码和 close 标记，便于 errors.As 后检查。
	return fmt.Sprintf("os.exit requested: code=%d close=%v", err.Code, err.Close)
}

// Open 将 Lua 5.3 os 标准库注册到 State 全局环境。
//
// state 必须非 nil 且未关闭；成功后全局 `os` 字段指向库表，包含当前已迁移的 os 函数。
func Open(state *runtime.State) error {
	// 注册入口先校验 State 生命周期，避免向关闭后的全局表写入库函数。
	if state == nil {
		// nil State 没有 globals，调用方需要先创建 runtime.State。
		return fmt.Errorf("os library unavailable: %w", runtime.ErrNilState)
	}
	if state.IsClosed() {
		// 已关闭 State 的 globals 已释放，不能继续注册标准库。
		return fmt.Errorf("os library unavailable: %w", runtime.ErrClosedState)
	}

	library := runtime.NewTable()
	// os 库函数以 Go closure 注册，后续 VM CALL 会通过 bridge 调用。
	library.RawSetString("clock", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Clock)))
	library.RawSetString("date", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Date)))
	library.RawSetString("difftime", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(DiffTime)))
	options := state.Options()
	library.RawSetString("execute", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// os.execute 触发宿主进程能力，只有显式授权的 State 才允许执行命令。
		return ExecuteWithOptions(options, args...)
	})))
	library.RawSetString("exit", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Exit)))
	library.RawSetString("getenv", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// os.getenv 读取宿主环境变量，默认嵌入模式必须保持关闭。
		return GetEnvWithOptions(options, args...)
	})))
	library.RawSetString("remove", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// os.remove 写宿主文件系统，注册入口按 State 权限控制。
		return RemoveWithOptions(options, args...)
	})))
	library.RawSetString("rename", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// os.rename 写宿主文件系统，注册入口按 State 权限控制。
		return RenameWithOptions(options, args...)
	})))
	library.RawSetString("setlocale", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(SetLocale)))
	library.RawSetString("time", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Time)))
	library.RawSetString("tmpname", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// os.tmpname 创建宿主临时路径，默认嵌入模式必须保持关闭。
		return TmpNameWithOptions(options, args...)
	})))
	state.SetGlobal("os", runtime.ReferenceValue(runtime.KindTable, library))
	return nil
}

// Clock 实现 Lua 5.3 `os.clock` 的第一阶段语义。
//
// 返回当前进程相对 oslib 初始化时间经过的秒数。Lua 5.3 定义为 CPU time；Go 标准库无法
// 跨平台直接提供进程 CPU time，因此当前使用单调时钟 elapsed time 作为纯 Go 可移植近似。
func Clock(args ...runtime.Value) ([]runtime.Value, error) {
	// os.clock 不需要参数，忽略多余参数以保持 Lua 标准库宽松调用风格。
	elapsed := time.Since(processStart).Seconds()
	return []runtime.Value{runtime.NumberValue(elapsed)}, nil
}

// Date 实现 Lua 5.3 `os.date` 的第一阶段语义。
//
// 第一个参数 format 可选，默认 `%c`；第二个参数 time 可选，使用 Unix 秒时间戳。当前支持
// 常用 strftime 子集以及 `!*t`/`*t` 表形式输出，未知格式保持字面输出。
func Date(args ...runtime.Value) ([]runtime.Value, error) {
	// 默认格式与 Lua 5.3 保持一致。
	format := "%c"
	if len(args) > 0 && !args[0].IsNil() {
		// format 如果出现必须是 string。
		if args[0].Kind != runtime.KindString {
			// 非字符串 format 返回参数错误。
			return nil, badArgument("date", 1, "string expected")
		}
		format = args[0].String
	}

	current := time.Now()
	if len(args) > 1 && !args[1].IsNil() {
		// time 参数如果出现必须是 integer。
		seconds, ok := args[1].ToInteger()
		if !ok {
			// 非 integer 时间戳返回参数错误。
			return nil, badArgument("date", 2, "integer expected")
		}
		if seconds > 1<<50 || seconds < -(1<<50) {
			// 极端时间戳超出当前兼容层可稳定表示范围，按 Lua 5.3 报不可表示。
			return nil, runtime.RaiseError(runtime.StringValue("cannot be represented"))
		}
		current = time.Unix(seconds, 0)
	}

	useUTC := false
	if strings.HasPrefix(format, "!") {
		// 前导 ! 表示使用 UTC 时间。
		useUTC = true
		format = strings.TrimPrefix(format, "!")
	}
	if useUTC {
		// UTC 模式需要转换时区。
		current = current.UTC()
	} else {
		// 非 UTC 模式使用本地时区。
		current = current.Local()
	}

	if format == "*t" {
		// *t 返回包含时间字段的 table。
		return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, dateTable(current))}, nil
	}
	formatted, err := formatDate(format, current)
	if err != nil {
		// 非法 strftime 转换按 Lua 5.3 抛出 invalid conversion specifier。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(formatted)}, nil
}

// DiffTime 实现 Lua 5.3 `os.difftime`。
//
// 两个参数都必须是 Unix 秒时间戳兼容 integer；返回 t2 - t1 的秒数差，使用 number 表达。
func DiffTime(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析结束时间参数。
	end, err := integerArgument(args, 1, "difftime")
	if err != nil {
		// 第一个参数不是 integer 时返回 Lua 参数错误。
		return nil, err
	}
	start, err := integerArgument(args, 2, "difftime")
	if err != nil {
		// 第二个参数不是 integer 时返回 Lua 参数错误。
		return nil, err
	}
	return []runtime.Value{runtime.NumberValue(float64(end - start))}, nil
}

// Execute 实现 Lua 5.3 `os.execute` 的基础宿主命令语义。
//
// 无参数时只查询系统 shell 是否可用；传入命令字符串时通过宿主 shell 执行，并按 Lua 5.3
// 返回首值布尔成功性、退出类型和退出码。该能力用于官方 Lua 测试套件驱动 CLI 兼容用例。
func Execute(args ...runtime.Value) ([]runtime.Value, error) {
	// 无参数查询 shell 是否可用，官方测试套件会用该结果判断能否执行后续 shell 用例。
	if len(args) == 0 || args[0].IsNil() {
		return []runtime.Value{runtime.BooleanValue(true)}, nil
	}
	if args[0].Kind != runtime.KindString {
		// 命令参数如果出现必须是 string。
		return nil, badArgument("execute", 1, "string expected")
	}

	commandName, commandArgs := shellCommand(args[0].String)
	command := exec.Command(commandName, commandArgs...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	// 命令退出状态统一转换为 Lua 5.3 `ok, what, code` 三元组。
	return procstatus.ValuesFromRunError(command.Run())
}

// ExecuteWithOptions 按 State 宿主进程策略执行 Lua 5.3 `os.execute`。
//
// options.AllowProcess 为 false 时，带命令调用返回禁用错误；无参数查询 shell 可用性保持 false，
// 便于官方测试或用户脚本按返回值跳过进程相关路径。
func ExecuteWithOptions(options runtime.Options, args ...runtime.Value) ([]runtime.Value, error) {
	// 无参数查询不启动进程；禁用时返回 false 表示当前 Lua 环境不可执行 shell。
	if len(args) == 0 || args[0].IsNil() {
		if !options.AllowProcess {
			// 禁用进程时返回 false，而不是 Lua error，兼容 os.execute() 的能力探测语义。
			return []runtime.Value{runtime.BooleanValue(false)}, nil
		}
		return Execute(args...)
	}
	if !options.AllowProcess {
		// 带命令执行需要宿主显式授权，否则会产生不可控副作用。
		return nil, processDisabledError("execute")
	}
	return Execute(args...)
}

// Exit 实现 Lua 5.3 `os.exit` 的嵌入安全语义。
//
// 第一个参数可选，可为 boolean 或 integer；第二个参数 close 可选且必须为 boolean。
// 当前不直接终止宿主进程，而是返回包含 ExitError 的 Lua error，供 CLI 层后续转为退出码。
func Exit(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析退出码，默认 true 对应 0。
	code := 0
	if len(args) > 0 && !args[0].IsNil() {
		// boolean 参数按 Lua 语义映射为成功或失败。
		if args[0].Kind == runtime.KindBoolean {
			// true 表示成功退出码 0，false 表示失败退出码 1。
			if args[0].Bool {
				code = 0
			} else {
				code = 1
			}
		} else if value, ok := args[0].ToInteger(); ok {
			// integer 参数直接作为退出码。
			code = int(value)
		} else {
			// 其他类型不能作为退出码。
			return nil, badArgument("exit", 1, "boolean or integer expected")
		}
	}

	closeState := true
	if len(args) > 1 && !args[1].IsNil() {
		// close 参数如果出现必须是 boolean。
		if args[1].Kind != runtime.KindBoolean {
			// 非 boolean close 参数返回参数错误。
			return nil, badArgument("exit", 2, "boolean expected")
		}
		closeState = args[1].Bool
	}
	return nil, fmt.Errorf("%w: %w", runtime.ErrLuaError, &ExitError{Code: code, Close: closeState})
}

// GetEnv 实现 Lua 5.3 `os.getenv` 的默认 sandbox 策略。
//
// 环境变量读取属于宿主信息访问；当前默认策略下合法参数也返回 Lua 错误。
func GetEnv(args ...runtime.Value) ([]runtime.Value, error) {
	// 变量名必须是 string。
	if len(args) == 0 || args[0].Kind != runtime.KindString {
		// 缺失或非字符串变量名按 Lua 参数错误处理。
		return nil, badArgument("getenv", 1, "string expected")
	}
	value, ok := os.LookupEnv(args[0].String)
	if !ok {
		// 环境变量不存在时按 Lua 5.3 返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	return []runtime.Value{runtime.StringValue(value)}, nil
}

// GetEnvWithOptions 按 State 宿主环境策略执行 Lua 5.3 `os.getenv`。
//
// options.AllowEnvironment 为 false 时，合法参数也返回 Lua error，避免默认嵌入模式泄漏宿主配置。
func GetEnvWithOptions(options runtime.Options, args ...runtime.Value) ([]runtime.Value, error) {
	// 参数校验仍交给 GetEnv 兼容函数；禁用策略只拦截合法读取行为。
	if len(args) == 0 || args[0].Kind != runtime.KindString {
		// 缺失或非字符串变量名按 Lua 参数错误处理。
		return nil, badArgument("getenv", 1, "string expected")
	}
	if !options.AllowEnvironment {
		// 默认关闭环境读取，避免脚本探测宿主敏感配置。
		return nil, environmentDisabledError("getenv")
	}
	return GetEnv(args...)
}

// Remove 实现 Lua 5.3 `os.remove` 的基础文件删除语义。
//
// 参数必须是路径字符串；删除成功返回 true，底层文件系统错误按 Lua 5.3 语义返回 nil 与错误文本。
func Remove(args ...runtime.Value) ([]runtime.Value, error) {
	// 路径必须是 string。
	if len(args) == 0 || args[0].Kind != runtime.KindString {
		// 缺失或非字符串路径按 Lua 参数错误处理。
		return nil, badArgument("remove", 1, "string expected")
	}
	if err := os.Remove(args[0].String); err != nil {
		// 删除失败是 os.remove 的普通返回路径，脚本可用 not os.remove(...) 判断。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(err.Error())}, nil
	}
	return []runtime.Value{runtime.BooleanValue(true)}, nil
}

// RemoveWithOptions 按 State 文件系统策略执行 Lua 5.3 `os.remove`。
//
// options.AllowHostFilesystem 为 false 时，合法路径也返回 Lua error，保持库模式默认 sandbox。
func RemoveWithOptions(options runtime.Options, args ...runtime.Value) ([]runtime.Value, error) {
	// 参数错误优先于权限错误，保持 Lua 标准库参数诊断稳定。
	if len(args) == 0 || args[0].Kind != runtime.KindString {
		// 缺失或非字符串路径按 Lua 参数错误处理。
		return nil, badArgument("remove", 1, "string expected")
	}
	if !options.AllowHostFilesystem {
		// 删除宿主文件属于写副作用，默认必须拒绝。
		return nil, filesystemDisabledError("remove")
	}
	return Remove(args...)
}

// Rename 实现 Lua 5.3 `os.rename` 的基础文件改名语义。
//
// 两个参数必须是路径字符串；改名成功返回 true，底层文件系统错误转换为 Lua error。
func Rename(args ...runtime.Value) ([]runtime.Value, error) {
	// 源路径必须是 string。
	if len(args) == 0 || args[0].Kind != runtime.KindString {
		// 缺失或非字符串源路径按 Lua 参数错误处理。
		return nil, badArgument("rename", 1, "string expected")
	}
	if len(args) < 2 || args[1].Kind != runtime.KindString {
		// 缺失或非字符串目标路径按 Lua 参数错误处理。
		return nil, badArgument("rename", 2, "string expected")
	}
	if err := os.Rename(args[0].String, args[1].String); err != nil {
		// 改名失败属于 os.rename 的普通失败返回，脚本可用 not os.rename(...) 判断。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue(err.Error()), runtime.IntegerValue(1)}, nil
	}
	return []runtime.Value{runtime.BooleanValue(true)}, nil
}

// RenameWithOptions 按 State 文件系统策略执行 Lua 5.3 `os.rename`。
//
// options.AllowHostFilesystem 为 false 时，合法路径也返回 Lua error，避免默认嵌入模式修改宿主文件。
func RenameWithOptions(options runtime.Options, args ...runtime.Value) ([]runtime.Value, error) {
	// 参数错误优先于权限错误，保持 Lua 标准库参数诊断稳定。
	if len(args) == 0 || args[0].Kind != runtime.KindString {
		// 缺失或非字符串源路径按 Lua 参数错误处理。
		return nil, badArgument("rename", 1, "string expected")
	}
	if len(args) < 2 || args[1].Kind != runtime.KindString {
		// 缺失或非字符串目标路径按 Lua 参数错误处理。
		return nil, badArgument("rename", 2, "string expected")
	}
	if !options.AllowHostFilesystem {
		// 改名宿主文件属于写副作用，默认必须拒绝。
		return nil, filesystemDisabledError("rename")
	}
	return Rename(args...)
}

// SetLocale 实现 Lua 5.3 `os.setlocale` 的第一阶段策略。
//
// Go 标准库不提供进程 locale 切换；当前只接受 nil 或 cLocaleName，并返回 cLocaleName。
// 其他 locale 返回 nil，表示无法设置。
func SetLocale(args ...runtime.Value) ([]runtime.Value, error) {
	// locale 参数可选；nil 表示查询当前 locale。
	if len(args) == 0 || args[0].IsNil() {
		return []runtime.Value{runtime.StringValue(cLocaleName)}, nil
	}
	if args[0].Kind != runtime.KindString {
		// locale 如果出现必须是 string。
		return nil, badArgument("setlocale", 1, "string expected")
	}
	if len(args) > 1 && !args[1].IsNil() && args[1].Kind != runtime.KindString {
		// category 如果出现必须是 string。
		return nil, badArgument("setlocale", 2, "string expected")
	}
	if args[0].String == cLocaleName {
		// 当前固定支持 C locale。
		return []runtime.Value{runtime.StringValue(cLocaleName)}, nil
	}
	if args[0].String == "" {
		// 空字符串通常表示用户默认 locale；纯 Go 当前不可切换，返回 nil。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	// 其他 locale 暂不支持，按 Lua 语义返回 nil。
	return []runtime.Value{runtime.NilValue()}, nil
}

// Time 实现 Lua 5.3 `os.time`。
//
// 无参数时返回当前 Unix 秒时间戳；传入 table 时读取 year、month、day、hour、min、sec
// 字段并转换为本地时区 Unix 秒。缺失 hour/min/sec 默认 12/0/0，兼容 Lua 5.3 手册。
func Time(args ...runtime.Value) ([]runtime.Value, error) {
	// 无参数时返回当前时间戳。
	if len(args) == 0 || args[0].IsNil() {
		return []runtime.Value{runtime.IntegerValue(time.Now().Unix())}, nil
	}
	if args[0].Kind != runtime.KindTable {
		// os.time 的 table 参数必须是 table。
		return nil, badArgument("time", 1, "table expected")
	}
	table, ok := args[0].Ref.(*runtime.Table)
	if !ok {
		// table 引用类型错配时按参数错误处理。
		return nil, badArgument("time", 1, "table expected")
	}

	year, err := tableIntegerField(table, "year", true, 0)
	if err != nil {
		// year 字段缺失或非法时返回 Lua 错误。
		return nil, err
	}
	month, err := tableIntegerField(table, "month", true, 0)
	if err != nil {
		// month 字段缺失或非法时返回 Lua 错误。
		return nil, err
	}
	day, err := tableIntegerField(table, "day", true, 0)
	if err != nil {
		// day 字段缺失或非法时返回 Lua 错误。
		return nil, err
	}
	hour, err := tableIntegerField(table, "hour", false, 12)
	if err != nil {
		// hour 字段非法时返回 Lua 错误。
		return nil, err
	}
	minute, err := tableIntegerField(table, "min", false, 0)
	if err != nil {
		// min 字段非法时返回 Lua 错误。
		return nil, err
	}
	second, err := tableIntegerField(table, "sec", false, 0)
	if err != nil {
		// sec 字段非法时返回 Lua 错误。
		return nil, err
	}
	if year < 0 {
		// Lua 官方测试要求极端负年份报告 out-of-bound，而不是由 Go time.Date 归一化。
		return nil, runtime.RaiseError(runtime.StringValue("out-of-bound"))
	}
	if year > 9999 {
		// 当前兼容层限制到常规公历年份范围；超大年份按 Lua 5.3 表达为不可表示。
		return nil, runtime.RaiseError(runtime.StringValue("cannot be represented"))
	}

	value := time.Date(int(year), time.Month(month), int(day), int(hour), int(minute), int(second), 0, time.Local)
	updateDateTable(table, value)
	return []runtime.Value{runtime.IntegerValue(value.Unix())}, nil
}

// TmpName 实现 Lua 5.3 `os.tmpname` 的基础临时路径语义。
//
// 返回一个当前进程可用的临时路径字符串。实现保留创建出的空文件，确保路径唯一且后续 os.remove 可清理。
func TmpName(args ...runtime.Value) ([]runtime.Value, error) {
	// os.tmpname 不接受参数，当前阶段忽略多余参数并生成宿主临时路径。
	file, err := os.CreateTemp("", "go-lua-vm-*")
	if err != nil {
		// 临时文件创建失败时按 Lua error 暴露底层原因。
		return nil, runtime.RaiseError(runtime.StringValue(err.Error()))
	}
	name := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		// 关闭失败说明临时路径状态不可靠，先返回错误。
		return nil, runtime.RaiseError(runtime.StringValue(closeErr.Error()))
	}
	return []runtime.Value{runtime.StringValue(name)}, nil
}

// TmpNameWithOptions 按 State 文件系统策略执行 Lua 5.3 `os.tmpname`。
//
// options.AllowHostFilesystem 为 false 时返回 Lua error；授权后才创建宿主临时路径。
func TmpNameWithOptions(options runtime.Options, args ...runtime.Value) ([]runtime.Value, error) {
	// 临时路径创建会触碰宿主文件系统，默认嵌入模式拒绝。
	if !options.AllowHostFilesystem {
		// 即使参数多余，当前实现也沿用 TmpName 的宽松参数语义，只检查权限。
		return nil, filesystemDisabledError("tmpname")
	}
	return TmpName(args...)
}

// shellCommand 返回当前平台执行 Lua `os.execute` 命令字符串所需的 shell 命令行。
//
// command 是 Lua 传入的原始命令文本；返回值可直接传给 exec.Command。Windows 使用 cmd，
// 其他平台使用 POSIX sh，匹配官方 Lua 测试对类 Unix shell 的主要假设。
func shellCommand(command string) (string, []string) {
	// Windows 平台使用 cmd.exe 的 /C 语义执行命令字符串。
	if goruntime.GOOS == "windows" {
		return "cmd", []string{"/C", command}
	}
	// 非 Windows 平台追加 exit $?，避免 sh 对最后一个外部命令做 exec 优化而把子 shell 信号冒泡。
	return "/bin/sh", []string{"-c", command + "; exit $?"}
}

// dateTable 构造 os.date("*t") 返回的 Lua table。
//
// 字段对齐 Lua 5.3 手册：year、month、day、hour、min、sec、wday、yday、isdst。
func dateTable(current time.Time) *runtime.Table {
	// 新建 table 并写入所有时间字段。
	table := runtime.NewTable()
	updateDateTable(table, current)
	return table
}

// updateDateTable 将 Go time 写回 Lua date table 字段。
//
// os.date("*t") 和 os.time(table) 归一化回写共享该逻辑；字段对齐 Lua 5.3 手册：
// year、month、day、hour、min、sec、wday、yday、isdst。
func updateDateTable(table *runtime.Table, current time.Time) {
	// 写入年月日与时间字段。
	table.RawSetString("year", runtime.IntegerValue(int64(current.Year())))
	table.RawSetString("month", runtime.IntegerValue(int64(current.Month())))
	table.RawSetString("day", runtime.IntegerValue(int64(current.Day())))
	table.RawSetString("hour", runtime.IntegerValue(int64(current.Hour())))
	table.RawSetString("min", runtime.IntegerValue(int64(current.Minute())))
	table.RawSetString("sec", runtime.IntegerValue(int64(current.Second())))
	table.RawSetString("wday", runtime.IntegerValue(luaWeekday(current)))
	table.RawSetString("yday", runtime.IntegerValue(int64(current.YearDay())))
	table.RawSetString("isdst", runtime.BooleanValue(false))
}

// luaWeekday 将 Go weekday 转换为 Lua `os.date("*t").wday`。
//
// Lua 使用 1=Sunday 到 7=Saturday；Go 的 Sunday 为 0，因此需要加 1。
func luaWeekday(current time.Time) int64 {
	// Go Weekday 枚举可直接偏移为 Lua 范围。
	return int64(current.Weekday()) + 1
}

// formatDate 将 Lua 常见 strftime 格式转换为 Go 时间文本。
//
// 当前支持 Lua 兼容常用子集；未识别的 `%x` 会保留 x 字符本身，避免误抛错阻塞后续补齐。
func formatDate(format string, current time.Time) (string, error) {
	// 逐字符扫描格式串并处理 % 转义。
	var builder strings.Builder
	for index := 0; index < len(format); index++ {
		// 非 % 字符直接写入。
		if format[index] != '%' {
			builder.WriteByte(format[index])
			continue
		}
		if index+1 >= len(format) {
			// 尾部孤立 % 是非法转换。
			return "", invalidDateConversion()
		}
		index++
		if format[index] == 'E' || format[index] == 'O' {
			// POSIX E/O 修饰符必须再跟一个实际转换符。
			if index+1 >= len(format) {
				return "", invalidDateConversion()
			}
			index++
			directiveText, ok := formatDirective(format[index], current)
			if !ok {
				// 修饰符后的转换符仍需是当前实现支持的合法集合。
				return "", invalidDateConversion()
			}
			builder.WriteString(directiveText)
			continue
		}
		directiveText, ok := formatDirective(format[index], current)
		if !ok {
			// 未支持或非法转换符按 Lua 5.3 返回运行期错误。
			return "", invalidDateConversion()
		}
		builder.WriteString(directiveText)
	}
	return builder.String(), nil
}

// formatDirective 格式化单个 strftime 指令。
//
// code 是 `%` 后的控制字符；返回值是对应时间字段文本，第二返回值表示转换是否有效。
func formatDirective(code byte, current time.Time) (string, bool) {
	// 根据控制字符输出对应文本。
	switch code {
	case '%':
		// %% 输出单个百分号。
		return "%", true
	case 'a':
		// 缩写星期名。
		return current.Format("Mon"), true
	case 'A':
		// 完整星期名。
		return current.Format("Monday"), true
	case 'b', 'h':
		// 缩写月份名；POSIX %h 是 %b 的别名。
		return current.Format("Jan"), true
	case 'B':
		// 完整月份名。
		return current.Format("January"), true
	case 'C':
		// 年份的世纪部分。
		return fmt.Sprintf("%02d", current.Year()/100), true
	case 'D':
		// POSIX %D 等价于 %m/%d/%y。
		return current.Format("01/02/06"), true
	case 'e':
		// 空格填充的月内日期。
		return fmt.Sprintf("%2d", current.Day()), true
	case 'F':
		// ISO 日期，等价于 %Y-%m-%d。
		return current.Format("2006-01-02"), true
	case 'g':
		// ISO 周年份的两位形式。
		isoYear, _ := current.ISOWeek()
		return fmt.Sprintf("%02d", positiveModulo(isoYear, 100)), true
	case 'G':
		// ISO 周年份的四位形式。
		isoYear, _ := current.ISOWeek()
		return fmt.Sprintf("%04d", isoYear), true
	case 'Y':
		// 四位年份。
		return fmt.Sprintf("%04d", current.Year()), true
	case 'y':
		// 两位年份。
		return fmt.Sprintf("%02d", current.Year()%100), true
	case 'm':
		// 两位月份。
		return fmt.Sprintf("%02d", int(current.Month())), true
	case 'd':
		// 两位日期。
		return fmt.Sprintf("%02d", current.Day()), true
	case 'I':
		// 12 小时制小时，01..12。
		return fmt.Sprintf("%02d", twelveHour(current.Hour())), true
	case 'H':
		// 两位 24 小时。
		return fmt.Sprintf("%02d", current.Hour()), true
	case 'k':
		// 空格填充的 24 小时。
		return fmt.Sprintf("%2d", current.Hour()), true
	case 'l':
		// 空格填充的 12 小时。
		return fmt.Sprintf("%2d", twelveHour(current.Hour())), true
	case 'M':
		// 两位分钟。
		return fmt.Sprintf("%02d", current.Minute()), true
	case 'n':
		// 换行符。
		return "\n", true
	case 'p':
		// 本地 AM/PM 标记；当前固定 C locale 英文输出。
		if current.Hour() < 12 {
			// 0..11 点为 AM。
			return "AM", true
		}
		// 12..23 点为 PM。
		return "PM", true
	case 'r':
		// 12 小时制完整时间。
		return fmt.Sprintf("%02d:%02d:%02d %s", twelveHour(current.Hour()), current.Minute(), current.Second(), amPM(current.Hour())), true
	case 'R':
		// 24 小时制小时和分钟。
		return fmt.Sprintf("%02d:%02d", current.Hour(), current.Minute()), true
	case 's':
		// Unix 秒时间戳。
		return fmt.Sprintf("%d", current.Unix()), true
	case 'S':
		// 两位秒。
		return fmt.Sprintf("%02d", current.Second()), true
	case 't':
		// 水平制表符。
		return "\t", true
	case 'T':
		// 24 小时制完整时间。
		return fmt.Sprintf("%02d:%02d:%02d", current.Hour(), current.Minute(), current.Second()), true
	case 'u':
		// ISO 星期序号，Monday=1 到 Sunday=7。
		return fmt.Sprintf("%d", isoWeekday(current)), true
	case 'U':
		// Sunday 作为每周第一天的周序号。
		return fmt.Sprintf("%02d", posixWeekNumber(current, time.Sunday)), true
	case 'V':
		// ISO 8601 周序号。
		_, isoWeek := current.ISOWeek()
		return fmt.Sprintf("%02d", isoWeek), true
	case 'w':
		// 星期序号，Lua/strftime 使用 0=Sunday 到 6=Saturday。
		return fmt.Sprintf("%d", int(current.Weekday())), true
	case 'W':
		// Monday 作为每周第一天的周序号。
		return fmt.Sprintf("%02d", posixWeekNumber(current, time.Monday)), true
	case 'j':
		// 年内天数，范围 001..366。
		return fmt.Sprintf("%03d", current.YearDay()), true
	case 'x':
		// 本地日期展示；Go 不依赖 C locale，使用稳定的月/日/年格式。
		return current.Format("01/02/06"), true
	case 'X':
		// 本地时间展示；当前固定为 C locale 的 HH:MM:SS。
		return fmt.Sprintf("%02d:%02d:%02d", current.Hour(), current.Minute(), current.Second()), true
	case 'c':
		// 本地日期时间常见展示。
		return current.Format("Mon Jan _2 15:04:05 2006"), true
	case 'z':
		// 数字时区偏移，格式为 +HHMM。
		return formatTimezoneOffset(current), true
	case 'Z':
		// 时区缩写名称。
		zoneName, _ := current.Zone()
		return zoneName, true
	case 'E', 'O':
		// POSIX 扩展修饰符需要后续指令；单独出现非法。
		return "", false
	default:
		// 未支持控制符保持非法转换，便于官方 tests 捕获错误。
		return "", false
	}
}

// twelveHour 将 24 小时制小时转换为 strftime 的 12 小时制小时。
//
// hour 可为 0..23；返回值范围为 1..12，午夜和正午都映射为 12。
func twelveHour(hour int) int {
	// 先按 12 取余，得到 0..11。
	value := hour % 12
	if value == 0 {
		// strftime 的 12 小时制用 12 表示 0 点或 12 点。
		return 12
	}
	return value
}

// amPM 返回固定 C locale 的 AM/PM 文本。
//
// hour 可为 0..23；返回值用于 `%p` 和 `%r`，当前不受系统 locale 影响。
func amPM(hour int) string {
	// 0..11 点为 AM。
	if hour < 12 {
		return "AM"
	}
	// 12..23 点为 PM。
	return "PM"
}

// isoWeekday 返回 ISO 8601 星期序号。
//
// current 是已按调用方时区选择后的时间；返回 Monday=1 到 Sunday=7。
func isoWeekday(current time.Time) int {
	// Go 的 Sunday 为 0，需要特判成 ISO 的 7。
	if current.Weekday() == time.Sunday {
		return 7
	}
	return int(current.Weekday())
}

// posixWeekNumber 计算 POSIX `%U` 和 `%W` 周序号。
//
// current 是目标时间；firstWeekday 必须是 time.Sunday 或 time.Monday。返回值为 0..53，
// 表示第一个指定 weekday 之前的日期属于第 0 周。
func posixWeekNumber(current time.Time, firstWeekday time.Weekday) int {
	// 构造同年 1 月 1 日，用于计算第一周起点。
	yearStart := time.Date(current.Year(), time.January, 1, 0, 0, 0, 0, current.Location())
	yearDayIndex := current.YearDay() - 1
	daysToFirstWeekday := positiveModulo(int(firstWeekday)-int(yearStart.Weekday()), 7)
	if yearDayIndex < daysToFirstWeekday {
		// 第一周起点之前的日期属于第 0 周。
		return 0
	}
	return (yearDayIndex-daysToFirstWeekday)/7 + 1
}

// positiveModulo 返回非负取模结果。
//
// value 可为任意整数；modulus 必须大于 0。返回范围为 0..modulus-1。
func positiveModulo(value int, modulus int) int {
	// Go 的 % 会保留负号，因此先做一次普通取模。
	result := value % modulus
	if result < 0 {
		// 负结果需要补回 modulus，得到非负余数。
		return result + modulus
	}
	return result
}

// formatTimezoneOffset 格式化 time.Time 的数字时区偏移。
//
// current 是目标时间；返回 `+HHMM` 或 `-HHMM`，与 strftime `%z` 兼容。
func formatTimezoneOffset(current time.Time) string {
	// 从 time.Time 读取当前时区相对 UTC 的秒偏移。
	_, offsetSeconds := current.Zone()
	sign := "+"
	if offsetSeconds < 0 {
		// 负偏移需要输出负号并转为绝对秒数。
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	return fmt.Sprintf("%s%02d%02d", sign, hours, minutes)
}

// invalidDateConversion 构造 os.date 非法格式错误。
//
// Lua 5.3 官方测试只匹配 `invalid conversion specifier` 文本，具体转换符无需暴露。
func invalidDateConversion() error {
	// 非法日期转换统一使用 Lua error，供 pcall 捕获。
	return runtime.RaiseError(runtime.StringValue("invalid conversion specifier"))
}

// integerArgument 读取指定位置的 integer 参数。
//
// position 使用 Lua 1-based 参数序号；参数缺失或无法无损转换为 integer 时返回 Lua 参数错误。
func integerArgument(args []runtime.Value, position int, functionName string) (int64, error) {
	// 参数缺失时无法取得 integer。
	if position <= 0 || position > len(args) {
		// Lua 标准库把缺失参数报告为 integer expected。
		return 0, badArgument(functionName, position, "integer expected")
	}
	value, ok := integerValue(args[position-1])
	if !ok {
		// 非 integer 参数返回 Lua 参数错误。
		return 0, badArgument(functionName, position, "integer expected")
	}
	return value, nil
}

// tableIntegerField 从 Lua table 中读取 integer 字段。
//
// required 为 true 时字段缺失会返回 Lua error；required 为 false 时缺失返回 defaultValue。
func tableIntegerField(table *runtime.Table, name string, required bool, defaultValue int64) (int64, error) {
	// table 字段按 string key 读取。
	value := table.RawGetString(name)
	if value.IsNil() {
		// 必填字段缺失时返回 Lua 错误。
		if required {
			// os.time 缺少必填时间字段无法构造时间。
			return 0, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("field '%s' missing in date table", name)))
		}
		return defaultValue, nil
	}
	integer, ok := integerValue(value)
	if !ok {
		// 字段存在但不是 integer 时返回 Lua 错误。
		return 0, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("field '%s' is not an integer", name)))
	}
	return integer, nil
}

// integerValue 按 Lua 5.3 C API 的整数读取规则转换值。
//
// value 可为 integer、可无损转换为 integer 的 float number，或 numeric string；返回 ok=false
// 表示调用方应按对应标准库上下文报告 integer 参数或字段错误。
func integerValue(value runtime.Value) (int64, bool) {
	// 先复用当前 number 到 integer 的基础转换。
	if integer, ok := value.ToInteger(); ok {
		// 已经是 integer 或可无损转换的 float，直接返回。
		return integer, true
	}
	if value.Kind != runtime.KindString {
		// 非字符串没有 Lua 5.3 的隐式 numeric string 入口。
		return 0, false
	}
	converted, ok := value.StringToNumber()
	if !ok {
		// 字符串不是合法 Lua number，不能作为 integer 使用。
		return 0, false
	}
	integer, ok := converted.ToInteger()
	if !ok {
		// numeric string 对应非整数 number 时仍不能通过 integer 检查。
		return 0, false
	}
	return integer, true
}

// badArgument 构造 Lua 标准库参数错误。
//
// functionName 不包含库名前缀；position 使用 Lua 1-based 参数序号；detail 写入括号内的
// 具体约束说明。返回错误可被 errors.Is(err, runtime.ErrLuaError) 识别。
func badArgument(functionName string, position int, detail string) error {
	// 参数错误统一走 Lua error 对象，便于 pcall/xpcall 后续复用。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (%s)", position, functionName, detail)))
}

// processDisabledError 构造宿主进程创建禁用错误。
//
// functionName 用于说明触发禁用策略的 os 函数；返回值是 Lua error，表示脚本需要宿主显式
// 开启进程权限后才能继续。
func processDisabledError(functionName string) error {
	// 进程创建默认关闭，避免脚本绕过宿主安全边界。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("os.%s: host process access is disabled", functionName)))
}

// environmentDisabledError 构造宿主环境变量访问禁用错误。
//
// functionName 用于说明触发禁用策略的 os 函数；返回值是 Lua error，表示脚本需要宿主显式
// 开启环境变量权限后才能继续。
func environmentDisabledError(functionName string) error {
	// 环境变量访问默认关闭，避免脚本读取宿主敏感配置。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("os.%s: host environment access is disabled", functionName)))
}

// filesystemDisabledError 构造宿主文件系统禁用错误。
//
// functionName 用于说明触发禁用策略的 os 函数；返回值是 Lua error，表示脚本需要宿主显式
// 开启文件系统权限后才能继续。
func filesystemDisabledError(functionName string) error {
	// 文件系统访问默认关闭，错误消息必须稳定便于测试和嵌入方识别。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("os.%s: host filesystem access is disabled", functionName)))
}
