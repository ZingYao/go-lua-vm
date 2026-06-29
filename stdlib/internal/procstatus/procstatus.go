// Package procstatus 统一转换宿主进程退出状态到 Lua 5.3 三返回语义。
package procstatus

import (
	"os/exec"
	"syscall"

	"github.com/zing/go-lua-vm/runtime"
)

// signalWaitStatus 描述 Unix-like WaitStatus 暴露的信号退出能力。
//
// 使用接口而不是直接依赖具体 syscall.WaitStatus 方法，避免调用方关心平台实现细节。
type signalWaitStatus interface {
	// Signaled 报告进程是否因信号终止。
	Signaled() bool
	// Signal 返回终止信号编号。
	Signal() syscall.Signal
}

// ValuesFromRunError 将 exec.Command 运行结果转换为 Lua 5.3 os.execute/pclose 返回值。
//
// err 为 nil 时返回 `true, "exit", 0`；命令以非零状态退出时返回 `nil, "exit", code`；
// Unix-like 信号终止时返回 `nil, "signal", signo`。非进程退出错误返回 Lua error。
func ValuesFromRunError(err error) ([]runtime.Value, error) {
	if err == nil {
		// 正常退出按 Lua 5.3 成功三元组返回。
		return []runtime.Value{runtime.BooleanValue(true), runtime.StringValue("exit"), runtime.IntegerValue(0)}, nil
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		// 启动失败等非退出状态错误属于宿主运行异常。
		return nil, runtime.RaiseError(runtime.StringValue(err.Error()))
	}
	if status, ok := exitErr.Sys().(signalWaitStatus); ok && status.Signaled() {
		// Unix-like 平台可区分 signal 终止，官方 files.lua 会校验 HUP/KILL 编号。
		return []runtime.Value{runtime.NilValue(), runtime.StringValue("signal"), runtime.IntegerValue(int64(status.Signal()))}, nil
	}

	exitCode := exitErr.ExitCode()
	if exitCode < 0 {
		// 无法提取可移植退出码时用 1 表示失败。
		exitCode = 1
	}
	return []runtime.Value{runtime.NilValue(), runtime.StringValue("exit"), runtime.IntegerValue(int64(exitCode))}, nil
}
