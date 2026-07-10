package lua

import (
	"fmt"
	"path/filepath"

	"github.com/ZingYao/go-lua-vm/runtime"
)

// registerGluaPathGlobals 注册不访问宿主文件系统的路径运算命名空间。
//
// state 必须持有有效全局表；函数没有返回值。全部方法遵循当前宿主平台的 filepath 语义，
// 只处理字符串，不读取文件、目录、环境变量或进程工作目录。
func registerGluaPathGlobals(state *State) {
	// 注册过程只创建函数表和平台分隔符常量。
	if state == nil || state.Globals() == nil {
		// 无效 State 没有可写入的全局环境。
		return
	}
	gluaTable := gluaNamespaceTable(state.Globals())
	if gluaTable == nil {
		// 宿主占用非 table 的 glua 全局时保持原值。
		return
	}
	pathTable := runtime.NewTable()
	pathTable.RawSetString("join", gluaGoFunction(gluaPathJoin))
	pathTable.RawSetString("clean", gluaGoFunction(gluaPathClean))
	pathTable.RawSetString("base", gluaGoFunction(gluaPathBase))
	pathTable.RawSetString("dir", gluaGoFunction(gluaPathDir))
	pathTable.RawSetString("ext", gluaGoFunction(gluaPathExt))
	pathTable.RawSetString("isAbs", gluaGoFunction(gluaPathIsAbs))
	pathTable.RawSetString("rel", gluaGoFunction(gluaPathRel))
	pathTable.RawSetString("split", gluaGoFunction(gluaPathSplit))
	pathTable.RawSetString("volume", gluaGoFunction(gluaPathVolume))
	pathTable.RawSetString("toSlash", gluaGoFunction(gluaPathToSlash))
	pathTable.RawSetString("fromSlash", gluaGoFunction(gluaPathFromSlash))
	pathTable.RawSetString("match", gluaGoFunction(gluaPathMatch))
	pathTable.RawSetString("separator", runtime.StringValue(string(filepath.Separator)))
	pathTable.RawSetString("listSeparator", runtime.StringValue(string(filepath.ListSeparator)))
	gluaTable.RawSetString("path", runtime.ReferenceValue(runtime.KindTable, pathTable))
}

// gluaPathJoin 连接任意数量的路径片段并清理结果。
//
// args 可以为空，也可以包含任意数量 string；返回宿主平台格式的路径。非 string 参数返回 Lua error。
func gluaPathJoin(args ...runtime.Value) ([]runtime.Value, error) {
	// 先把 Lua 字符串转换为 Go 路径片段，再交给 filepath.Join。
	parts, err := gluaPathStringArgs("glua.path.join", args, -1)
	if err != nil {
		// 参数类型错误时不返回部分路径。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(filepath.Join(parts...))}, nil
}

// gluaPathClean 返回等价路径的最短词法表示。
//
// args 必须是单个 string；返回宿主平台格式的清理结果，不检查路径是否存在。
func gluaPathClean(args ...runtime.Value) ([]runtime.Value, error) {
	// 单路径方法复用严格参数解析。
	pathValue, err := gluaPathSingleString("glua.path.clean", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(filepath.Clean(pathValue))}, nil
}

// gluaPathBase 返回路径最后一个元素。
//
// args 必须是单个 string；返回 filepath.Base 结果，不访问文件系统。
func gluaPathBase(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析路径后提取末尾元素。
	pathValue, err := gluaPathSingleString("glua.path.base", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(filepath.Base(pathValue))}, nil
}

// gluaPathDir 返回路径除最后一个元素外的目录部分。
//
// args 必须是单个 string；返回清理后的 filepath.Dir 结果，不检查目录是否存在。
func gluaPathDir(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析路径后提取目录部分。
	pathValue, err := gluaPathSingleString("glua.path.dir", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(filepath.Dir(pathValue))}, nil
}

// gluaPathExt 返回路径最后一个元素的扩展名。
//
// args 必须是单个 string；返回包含前导点的扩展名，没有扩展名时返回空字符串。
func gluaPathExt(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析路径后提取扩展名。
	pathValue, err := gluaPathSingleString("glua.path.ext", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(filepath.Ext(pathValue))}, nil
}

// gluaPathIsAbs 判断路径是否为宿主平台绝对路径。
//
// args 必须是单个 string；返回 boolean，不解析符号链接也不检查目标。
func gluaPathIsAbs(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析路径后执行纯词法绝对路径判断。
	pathValue, err := gluaPathSingleString("glua.path.isAbs", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.BooleanValue(filepath.IsAbs(pathValue))}, nil
}

// gluaPathRel 返回 target 相对于 base 的词法路径。
//
// args 必须是 base、target 两个 string；返回相对路径。不同卷等无法表达的情况返回 Lua error。
func gluaPathRel(args ...runtime.Value) ([]runtime.Value, error) {
	// 相对路径运算严格要求两个参数。
	paths, err := gluaPathStringArgs("glua.path.rel", args, 2)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	relative, err := filepath.Rel(paths[0], paths[1])
	if err != nil {
		// filepath 无法表达结果时保留平台错误信息。
		return nil, runtime.RaiseError(runtime.StringValue("glua.path.rel: " + err.Error()))
	}
	return []runtime.Value{runtime.StringValue(relative)}, nil
}

// gluaPathSplit 把路径拆成目录和最后一个元素。
//
// args 必须是单个 string；返回 dir、file 两个字符串，dir 保留尾部分隔符以匹配 filepath.Split。
func gluaPathSplit(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析路径后返回两个稳定位置的结果。
	pathValue, err := gluaPathSingleString("glua.path.split", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	directory, fileName := filepath.Split(pathValue)
	return []runtime.Value{runtime.StringValue(directory), runtime.StringValue(fileName)}, nil
}

// gluaPathVolume 返回路径的宿主平台卷名前缀。
//
// args 必须是单个 string；Windows 可返回驱动器或 UNC 卷名，其他平台通常返回空字符串。
func gluaPathVolume(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析路径后提取平台卷名。
	pathValue, err := gluaPathSingleString("glua.path.volume", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(filepath.VolumeName(pathValue))}, nil
}

// gluaPathToSlash 把宿主平台分隔符转换为正斜杠。
//
// args 必须是单个 string；返回 filepath.ToSlash 结果，不清理路径。
func gluaPathToSlash(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析路径后只替换平台分隔符。
	pathValue, err := gluaPathSingleString("glua.path.toSlash", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(filepath.ToSlash(pathValue))}, nil
}

// gluaPathFromSlash 把正斜杠转换为宿主平台分隔符。
//
// args 必须是单个 string；返回 filepath.FromSlash 结果，不清理路径。
func gluaPathFromSlash(args ...runtime.Value) ([]runtime.Value, error) {
	// 解析路径后只替换正斜杠。
	pathValue, err := gluaPathSingleString("glua.path.fromSlash", args)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	return []runtime.Value{runtime.StringValue(filepath.FromSlash(pathValue))}, nil
}

// gluaPathMatch 判断名称是否匹配宿主平台路径模式。
//
// args 必须是 pattern、name 两个 string；返回 boolean。格式错误的模式返回 Lua error。
func gluaPathMatch(args ...runtime.Value) ([]runtime.Value, error) {
	// 模式匹配严格要求两个字符串。
	values, err := gluaPathStringArgs("glua.path.match", args, 2)
	if err != nil {
		// 参数错误直接返回。
		return nil, err
	}
	matched, err := filepath.Match(values[0], values[1])
	if err != nil {
		// 非法字符类等模式错误不降级为未匹配。
		return nil, runtime.RaiseError(runtime.StringValue("glua.path.match: " + err.Error()))
	}
	return []runtime.Value{runtime.BooleanValue(matched)}, nil
}

// gluaPathSingleString 解析单个路径字符串参数。
//
// apiName 用于错误消息，args 必须只含一个 string；返回字符串或 Lua error。
func gluaPathSingleString(apiName string, args []runtime.Value) (string, error) {
	// 复用通用多字符串解析并读取唯一结果。
	values, err := gluaPathStringArgs(apiName, args, 1)
	if err != nil {
		// 参数错误原样返回。
		return "", err
	}
	return values[0], nil
}

// gluaPathStringArgs 解析路径 API 的字符串参数。
//
// apiName 用于错误消息；expected 小于零表示接受任意数量，否则必须精确匹配。返回独立字符串切片。
func gluaPathStringArgs(apiName string, args []runtime.Value, expected int) ([]string, error) {
	// 先校验数量，再逐项校验类型，避免部分转换。
	if expected >= 0 && len(args) != expected {
		// 固定参数 API 不接受缺失或多余参数。
		return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("%s expects %d string argument(s)", apiName, expected)))
	}
	values := make([]string, len(args))
	for index, value := range args {
		// Lua 路径 API 只接受 string，不执行隐式 tostring。
		if value.Kind != runtime.KindString {
			// 报告 1-based 参数位置，保持 Lua 错误习惯。
			return nil, runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (string expected)", index+1, apiName)))
		}
		values[index] = value.String
	}
	return values, nil
}
