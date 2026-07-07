// Package table 实现 Lua 5.3 table 标准库的第一阶段能力。
//
// 本包只依赖 runtime 包，负责把 `table` 库表注册到 State 全局环境，并提供
// concat、insert、move、pack 和 remove 的 raw table 语义。
package table

import (
	"errors"
	"fmt"
	"math"
	gosort "sort"
	"strings"

	"github.com/ZingYao/go-lua-vm/runtime"
)

var (
	// ErrTableLibraryUnavailable 表示 table 库无法注册到目标 State。
	ErrTableLibraryUnavailable = errors.New("table library unavailable")
)

// sortAbort 保存 table.sort 比较过程中需要跳出 Go sort 的 Lua 错误。
type sortAbort struct {
	// err 是需要返回给 Lua 调用方的原始错误。
	err error
}

const (
	// tableUnpackStackReserve 模拟 Lua C API 在调用边界保留的额外栈槽，避免 `unpack` 返回值顶满栈上限。
	tableUnpackStackReserve = 10
	// tableSortMaxLength 对齐 Lua 5.3 ltablib.c 中 `n < INT_MAX` 的 sort 长度门槛。
	tableSortMaxLength = math.MaxInt32
)

// Open 将 Lua 5.3 table 标准库注册到 State 全局环境。
//
// state 必须非 nil 且未关闭；成功后全局 `table` 字段指向一个库表，并注册 concat、
// insert、move、pack 和 remove。当前阶段函数全部使用 raw table 访问，不触发元方法，
// 以对齐 Lua 5.3 table 库直接操作序列区的行为。
func Open(state *runtime.State) error {
	// 注册入口先校验 State 生命周期，避免在关闭后的全局表上挂载标准库。
	if state == nil {
		// nil State 没有 globals，调用方需要先创建 runtime.State。
		return fmt.Errorf("%w: %w", ErrTableLibraryUnavailable, runtime.ErrNilState)
	}
	if state.IsClosed() {
		// 已关闭 State 的 globals 已释放，不能继续注册标准库。
		return fmt.Errorf("%w: %w", ErrTableLibraryUnavailable, runtime.ErrClosedState)
	}

	library := runtime.NewTable()
	// table 库函数以 Go closure 注册，后续 VM CALL 会通过 bridge 调用。
	library.RawSetString("concat", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// concat 需要当前 State 来执行 Lua closure 形式的 __len/__index 元方法。
		return concatWithState(state, args...)
	})))
	library.RawSetString("insert", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// insert 需要当前 State 来执行 Lua closure 形式的 __len/__index/__newindex 元方法。
		return insertWithState(state, args...)
	})))
	library.RawSetString("move", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// move 需要当前 State 来执行 Lua closure 形式的 __index/__newindex 元方法。
		return moveWithState(state, args...)
	})))
	library.RawSetString("pack", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(Pack)))
	library.RawSetString("remove", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// remove 需要当前 State 来执行 Lua closure 形式的 __len/__index/__newindex 元方法。
		return removeWithState(state, args...)
	})))
	library.RawSetString("sort", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// sort 的 comparator 可能是 Lua closure，需要通过当前 State 的 Lua runner 执行。
		return sortWithState(state, args...)
	})))
	library.RawSetString("unpack", runtime.ReferenceValue(runtime.KindGoClosure, runtime.GoResultsFunction(func(args ...runtime.Value) ([]runtime.Value, error) {
		// unpack 需要当前 State 来执行 Lua closure 形式的 __len/__index 元方法。
		return unpackWithState(state, args...)
	})))
	state.SetGlobal("table", runtime.ReferenceValue(runtime.KindTable, library))
	return nil
}

// Concat 实现 Lua 5.3 `table.concat` 的基础序列拼接语义。
//
// 第一个参数必须是 table；第二个参数 sep 可选且必须是 string，默认空串；第三、四个参数
// i/j 可选且必须可转换为 integer，默认分别为 1 和 `#list`。区间内元素必须是 string 或
// number，number 按 Lua 5.3 基础 number-to-string 规则转换；遇到 nil 或其他类型会返回
// Lua error，避免把不合法序列静默拼接成调试文本。
func Concat(args ...runtime.Value) ([]runtime.Value, error) {
	// 无 State 入口保留给单测和纯 Go 调用，无法执行 Lua closure 元方法。
	return concatWithState(nil, args...)
}

// concatWithState 实现 Lua 5.3 `table.concat`，并在 State 可用时执行 Lua closure 元方法。
//
// state 可为 nil；nil 时只支持 raw/Go closure 元方法路径。args 语义同 Concat。
func concatWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// concat 先解析目标 table，后续区间读取都基于 raw integer key。
	list, err := tableArgument(args, 1, "concat")
	if err != nil {
		// 第一个参数不是 table 时直接返回 Lua 参数错误。
		return nil, err
	}

	separator := ""
	if len(args) >= 2 {
		// sep 参数存在时必须是 string，Lua 5.3 不对它做 tostring 隐式转换。
		if args[1].Kind != runtime.KindString {
			// sep 类型不匹配会使整个 concat 失败。
			return nil, badArgument("concat", 2, "string expected")
		}
		separator = args[1].String
	}

	startIndex := int64(1)
	if len(args) >= 3 {
		// i 参数存在时必须可转换为 integer。
		convertedIndex, ok := args[2].ToInteger()
		if !ok {
			// 非整数边界无法用于 raw integer 序列读取。
			return nil, badArgument("concat", 3, "integer expected")
		}
		startIndex = convertedIndex
	}

	endIndex, err := tableLength(state, list)
	if err != nil {
		// __len 元方法失败时直接传播错误。
		return nil, err
	}
	if len(args) >= 4 {
		// j 参数存在时必须可转换为 integer。
		convertedIndex, ok := args[3].ToInteger()
		if !ok {
			// 非整数边界无法用于 raw integer 序列读取。
			return nil, badArgument("concat", 4, "integer expected")
		}
		endIndex = convertedIndex
	}

	if startIndex > endIndex {
		// 空区间在 Lua 5.3 中返回空字符串，不读取任何元素。
		return []runtime.Value{runtime.StringValue("")}, nil
	}

	parts := make([]string, 0, endIndex-startIndex+1)
	for index := startIndex; index <= endIndex; index++ {
		// 每一项按普通 table 读取，允许触发 __index。
		value, getErr := tableGetInteger(state, list, index)
		if getErr != nil {
			// 元方法读取失败时直接返回错误。
			return nil, getErr
		}
		part, partErr := concatElement(value, index)
		if partErr != nil {
			// 任一元素不满足 string/number 约束时，整个拼接失败。
			return nil, partErr
		}
		parts = append(parts, part)
		if index == endIndex {
			// 已处理闭区间最后一个索引后立即退出，避免 maxinteger 自增溢出到 mininteger。
			break
		}
	}

	// 成功路径用分隔符连接已转换的字符串片段。
	return []runtime.Value{runtime.StringValue(strings.Join(parts, separator))}, nil
}

// Insert 实现 Lua 5.3 `table.insert` 的基础序列插入语义。
//
// 第一个参数必须是 table；两个参数时把第二个参数追加到 `#list + 1`；三个参数时第二个
// 参数为插入位置、第三个参数为值。位置必须落在 `[1, #list + 1]`，插入时会 raw 右移
// 现有序列槽位。函数无普通返回值，错误以 Lua error 传播。
func Insert(args ...runtime.Value) ([]runtime.Value, error) {
	// 无 State 入口保留给单测和纯 Go 调用，无法执行 Lua closure 元方法。
	return insertWithState(nil, args...)
}

// insertWithState 实现 Lua 5.3 `table.insert`，并在 State 可用时执行 Lua closure 元方法。
//
// state 可为 nil；nil 时只支持 raw/Go closure 元方法路径。args 语义同 Insert。
func insertWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// insert 先解析目标 table，并按参数数量区分 append 与指定位置插入。
	list, err := tableArgument(args, 1, "insert")
	if err != nil {
		// 第一个参数不是 table 时直接返回 Lua 参数错误。
		return nil, err
	}
	if len(args) != 2 && len(args) != 3 {
		// Lua 5.3 table.insert 只接受 value 或 pos/value 两种形态。
		return nil, runtime.RaiseError(runtime.StringValue("wrong number of arguments to 'insert'"))
	}

	lengthValue, err := tableLength(state, list)
	if err != nil {
		// __len 元方法失败时直接传播错误。
		return nil, err
	}
	length := lengthValue
	position := length + 1
	value := args[1]
	if len(args) == 3 {
		// 三参数形态下第二个参数是插入位置，第三个参数才是待插入值。
		convertedPosition, ok := args[1].ToInteger()
		if !ok {
			// 插入位置必须是整数，避免写入非序列 key。
			return nil, badArgument("insert", 2, "integer expected")
		}
		position = convertedPosition
		value = args[2]
	}
	if position < 1 || position > length+1 {
		// 位置越界时不修改 table，保持失败原子性。
		return nil, runtime.RaiseError(runtime.StringValue("position out of bounds"))
	}

	for index := length; index >= position; index-- {
		// 从右向左移动，避免覆盖尚未复制的旧元素。
		movedValue, getErr := tableGetInteger(state, list, index)
		if getErr != nil {
			// 读取待移动元素失败时停止，避免写入部分错乱数据。
			return nil, getErr
		}
		if setErr := tableSetInteger(state, list, index+1, movedValue); setErr != nil {
			// 普通写入可能触发 __newindex，错误需要传播给调用方。
			return nil, setErr
		}
	}
	// 目标位置写入新值；若 value 为 nil，则保留 Lua table 赋 nil 删除槽位语义。
	if err := tableSetInteger(state, list, position, value); err != nil {
		// 普通写入可能触发 __newindex，错误需要传播给调用方。
		return nil, err
	}
	return nil, nil
}

// Move 实现 Lua 5.3 `table.move` 的基础区间移动语义。
//
// 第一个参数必须是源 table；f/e/t 必须是整数，表示把 `[f, e]` 区间复制到从 t 开始的目标
// 区间。第五个参数可选且必须是目标 table，默认源 table。返回目标 table。
func Move(args ...runtime.Value) ([]runtime.Value, error) {
	// 无 State 入口保留给单测和纯 Go 调用，无法执行 Lua closure 元方法。
	return moveWithState(nil, args...)
}

// moveWithState 实现 Lua 5.3 `table.move`，并在 State 可用时执行 Lua closure 元方法。
//
// state 可为 nil；nil 时只支持 raw/Go closure 元方法路径。args 语义同 Move。实现按 Lua
// 5.3 `ltablib.c:tmove` 迁移：先检查元素数量和目标尾端回绕，再根据同表重叠关系选择
// 前向或后向逐槽复制，避免极端大区间为了重叠安全而整段缓存。
func moveWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// move 先解析源 table 与三个整数边界。
	source, err := tableArgument(args, 1, "move")
	if err != nil {
		// 第一个参数不是 table 时直接返回 Lua 参数错误。
		return nil, err
	}
	firstIndex, err := integerArgument(args, 2, "move")
	if err != nil {
		// f 不是整数时无法定位源区间。
		return nil, err
	}
	lastIndex, err := integerArgument(args, 3, "move")
	if err != nil {
		// e 不是整数时无法定位源区间。
		return nil, err
	}
	targetStart, err := integerArgument(args, 4, "move")
	if err != nil {
		// t 不是整数时无法定位目标区间。
		return nil, err
	}

	target := source
	targetValue := args[0]
	if len(args) >= 5 && !args[4].IsNil() {
		// 第五个参数存在时指定目标 table。
		target, err = tableArgument(args, 5, "move")
		if err != nil {
			// 目标不是 table 时不执行任何写入。
			return nil, err
		}
		targetValue = args[4]
	}

	if firstIndex > lastIndex {
		// 空区间不写入任何槽位，但仍按 Lua 5.3 返回目标 table。
		return []runtime.Value{targetValue}, nil
	}
	if firstIndex <= 0 && lastIndex >= math.MaxInt64+firstIndex {
		// 区间元素数量超过 lua_Integer 可表达范围时，必须在移动前按官方文本报错。
		return nil, runtime.RaiseError(runtime.StringValue("too many elements to move"))
	}

	moveCount := lastIndex - firstIndex + 1
	if targetStart > math.MaxInt64-moveCount+1 {
		// 目标区间尾端会超过 maxinteger 时，官方 table.move 返回 destination wrap around。
		return nil, runtime.RaiseError(runtime.StringValue("destination wrap around"))
	}

	copyForward := target != source || targetStart > lastIndex || targetStart <= firstIndex
	if copyForward {
		// 非重叠或目标表不同的场景从左到右复制，匹配官方实现并利于 table 递增写入。
		for offset := int64(0); offset < moveCount; offset++ {
			sourceIndex := firstIndex + offset
			targetIndex := targetStart + offset
			value, getErr := tableGetInteger(state, source, sourceIndex)
			if getErr != nil {
				// 读取源槽位失败时直接传播错误，目标只可能已有前序官方同序写入。
				return nil, getErr
			}
			if setErr := tableSetInteger(state, target, targetIndex, value); setErr != nil {
				// 写入目标槽位失败时直接传播错误，后续槽位不再处理。
				return nil, setErr
			}
		}
	} else {
		// 同表且目标落在源区间内部时必须倒序复制，避免前序写入污染尚未读取的源值。
		for offset := moveCount - 1; offset >= 0; offset-- {
			sourceIndex := firstIndex + offset
			targetIndex := targetStart + offset
			value, getErr := tableGetInteger(state, source, sourceIndex)
			if getErr != nil {
				// 读取源槽位失败时直接传播错误，目标只可能已有前序官方同序写入。
				return nil, getErr
			}
			if setErr := tableSetInteger(state, target, targetIndex, value); setErr != nil {
				// 写入目标槽位失败时直接传播错误，后续槽位不再处理。
				return nil, setErr
			}
		}
	}

	// Lua table.move 返回目标 table，便于链式调用。
	return []runtime.Value{targetValue}, nil
}

// Pack 实现 Lua 5.3 `table.pack` 的基础打包语义。
//
// 所有入参会按 1-based 正整数 key 写入新 table，并额外写入 string key `n` 表示原始入参
// 数量。nil 入参仍计入 n，但 raw 写入 nil 会删除对应槽位，这与 Lua table 的存储语义一致。
func Pack(args ...runtime.Value) ([]runtime.Value, error) {
	// pack 始终创建新 table，不读取外部状态。
	result := runtime.NewTable()
	for index, value := range args {
		// Lua 序列区从 1 开始保存传入参数。
		result.RawSetInteger(int64(index+1), value)
	}
	// n 字段记录原始参数数量，用于保留尾部 nil 的调用语义。
	result.RawSetString("n", runtime.IntegerValue(int64(len(args))))
	return []runtime.Value{runtime.ReferenceValue(runtime.KindTable, result)}, nil
}

// Remove 实现 Lua 5.3 `table.remove` 的基础序列删除语义。
//
// 第一个参数必须是 table；第二个参数 pos 可选且必须是 integer，默认 `#list`。当 `#list==0`
// 时，Lua 5.3 允许默认索引 0 与显式索引 0/1；其他显式 pos 必须落在 `[1, #list]`。
// 删除后会 raw 左移后续元素，并清空原末尾槽位。
func Remove(args ...runtime.Value) ([]runtime.Value, error) {
	// 无 State 入口保留给单测和纯 Go 调用，无法执行 Lua closure 元方法。
	return removeWithState(nil, args...)
}

// removeWithState 实现 Lua 5.3 `table.remove`，并在 State 可用时执行 Lua closure 元方法。
//
// state 可为 nil；nil 时只支持 raw/Go closure 元方法路径。args 语义同 Remove。
func removeWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// remove 先解析目标 table，并读取当前序列长度。
	list, err := tableArgument(args, 1, "remove")
	if err != nil {
		// 第一个参数不是 table 时直接返回 Lua 参数错误。
		return nil, err
	}

	length, err := tableLength(state, list)
	if err != nil {
		// __len 元方法失败时直接传播错误。
		return nil, err
	}
	position := length
	if len(args) >= 2 {
		// pos 参数存在时必须是整数。
		convertedPosition, ok := args[1].ToInteger()
		if !ok {
			// 非整数位置不能用于序列删除。
			return nil, badArgument("remove", 2, "integer expected")
		}
		position = convertedPosition
	}
	if length == 0 {
		if position != 0 && position != 1 {
			// 空序列只允许默认/显式 0 或显式 1，其他位置仍越界。
			return nil, runtime.RaiseError(runtime.StringValue("position out of bounds"))
		}
		// Lua 5.3 对空序列仍会读取 pos，并清空索引 0。
		removedValue, getErr := tableGetInteger(state, list, position)
		if getErr != nil {
			// 读取删除值失败时不继续清空槽位。
			return nil, getErr
		}
		if setErr := tableSetInteger(state, list, 0, runtime.NilValue()); setErr != nil {
			// 清空默认索引 0 可能触发 __newindex。
			return nil, setErr
		}
		if position == 1 {
			// 显式 pos=1 时也清空该槽位，避免残留非序列元素。
			if setErr := tableSetInteger(state, list, 1, runtime.NilValue()); setErr != nil {
				// 清空显式索引失败时返回错误。
				return nil, setErr
			}
		}
		return []runtime.Value{removedValue}, nil
	}
	if position == length+1 {
		// Lua 5.3 允许删除末尾后一位；该位置没有元素，返回 nil 且不修改 table。
		return []runtime.Value{runtime.NilValue()}, nil
	}
	if position < 1 || position > length {
		// 删除位置越界时不修改 table，保持失败原子性。
		return nil, runtime.RaiseError(runtime.StringValue("position out of bounds"))
	}

	removedValue, err := tableGetInteger(state, list, position)
	if err != nil {
		// 读取删除值失败时不继续移动元素。
		return nil, err
	}
	for index := position; index < length; index++ {
		// 从左向右覆盖，完成删除点之后元素的前移。
		movedValue, getErr := tableGetInteger(state, list, index+1)
		if getErr != nil {
			// 读取后续元素失败时停止移动。
			return nil, getErr
		}
		if setErr := tableSetInteger(state, list, index, movedValue); setErr != nil {
			// 普通写入可能触发 __newindex。
			return nil, setErr
		}
	}
	// 清空旧末尾，避免 table 中残留重复元素。
	if err := tableSetInteger(state, list, length, runtime.NilValue()); err != nil {
		// 清空旧末尾失败时返回错误。
		return nil, err
	}
	return []runtime.Value{removedValue}, nil
}

// Sort 实现 Lua 5.3 `table.sort` 的基础序列排序语义。
//
// 第一个参数必须是 table；第二个参数 comp 可选，支持 Go closure comparator；Open 注册
// 的有状态入口还支持 Lua closure comparator。未传 comp 时使用 Lua 5.3 基础 `<` 语义：number 与
// number 数值比较、string 与 string 字节序比较，其他组合返回 Lua error。函数会原地重写
// `1..#list` 序列区且无普通返回值；comparator 抛错时保留已发生的局部交换状态。
func Sort(args ...runtime.Value) ([]runtime.Value, error) {
	// 无 State 的入口保留给单测和 Go comparator；Lua comparator 由 Open 注册的闭包处理。
	return sortWithState(nil, args...)
}

// sortWithState 实现 Lua 5.3 `table.sort`，并在可用时执行 Lua closure comparator。
//
// state 可为 nil；nil 时仅支持 Go closure comparator。额外参数按 Lua 5.3 语义忽略。
func sortWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// sort 先解析目标 table，并把连续序列区缓存到 Go 切片中排序。
	list, err := tableArgument(args, 1, "sort")
	if err != nil {
		// 第一个参数不是 table 时直接返回 Lua 参数错误。
		return nil, err
	}

	length, err := tableLength(state, list)
	if err != nil {
		// __len 元方法失败时直接传播错误。
		return nil, err
	}
	if length >= tableSortMaxLength {
		// Lua 5.3 table.sort 使用 C int 索引，长度达到 INT_MAX 时直接拒绝。
		return nil, runtime.RaiseError(runtime.StringValue("array too big"))
	}
	if length <= 1 {
		// 长度为 0、1 或负数时没有需要比较的元素，官方 sort.lua 期望直接成功。
		return nil, nil
	}

	comparator := runtime.NilValue()
	if len(args) >= 2 && !args[1].IsNil() {
		// comp 参数存在时必须是当前运行期可执行的函数；额外参数由 Lua 5.3 忽略。
		if args[1].Kind != runtime.KindGoClosure && args[1].Kind != runtime.KindLuaClosure {
			// 非函数 comparator 不能参与排序；该检查只在非平凡区间执行。
			return nil, badArgument("sort", 2, "function expected")
		}
		comparator = args[1]
	}
	values := make([]runtime.Value, 0, length)
	for index := int64(1); index <= length; index++ {
		// 排序处理 1..#list，允许 __index 提供虚拟元素。
		value, getErr := tableGetInteger(state, list, index)
		if getErr != nil {
			// 读取待排序元素失败时停止排序。
			return nil, getErr
		}
		values = append(values, value)
	}

	if err := sortValues(state, values, comparator); err != nil {
		// comparator、基础比较或 invalid order 错误会中止排序并返回给 Lua。
		return nil, err
	}

	for index, value := range values {
		// 排序完成后按 1-based 序列槽位写回 table，允许 __newindex 接管。
		if err := tableSetInteger(state, list, int64(index+1), value); err != nil {
			// 写回失败时返回错误。
			return nil, err
		}
	}
	return nil, nil
}

// sortValues 使用 Go 标准库排序 Lua 序列值。
//
// state 用于执行 Lua comparator；values 是待排序快照；comparator 为 nil 时使用基础 `<`。
// 比较过程中的 Lua error 通过受控 panic 跳出 sort.SliceStable，再还原为普通 error。
func sortValues(state *runtime.State, values []runtime.Value, comparator runtime.Value) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// 只吞掉本函数主动抛出的 sortAbort；其他 panic 继续向外传播，避免掩盖内部 bug。
			if abort, ok := recovered.(sortAbort); ok {
				err = abort.err
				return
			}
			panic(recovered)
		}
	}()

	gosort.SliceStable(values, func(leftIndex int, rightIndex int) bool {
		// Go sort 要求 bool comparator；Lua 错误通过 sortAbort 短路出去。
		left := values[leftIndex]
		right := values[rightIndex]
		less, compareErr := lessForSort(state, left, right, comparator)
		if compareErr != nil {
			// comparator 或基础比较失败时立即终止排序。
			panic(sortAbort{err: compareErr})
		}
		if less {
			// a<b 与 b<a 同时为真违反严格弱序，按 Lua 5.3 报 invalid order function。
			reverseLess, reverseErr := lessForSort(state, right, left, comparator)
			if reverseErr != nil {
				// 反向比较失败同样来自 comparator 或基础比较。
				panic(sortAbort{err: reverseErr})
			}
			if reverseLess {
				// 非法比较函数必须返回官方错误文本。
				panic(sortAbort{err: runtime.RaiseError(runtime.StringValue("invalid order function for sorting"))})
			}
		}
		return less
	})
	return nil
}

// Unpack 实现 Lua 5.3 `table.unpack` 的基础返回语义。
//
// 第一个参数必须是 table；第二、三个参数 i/j 可选且必须为 integer，默认分别为 1 和
// `#list`。返回 `[i, j]` 区间内 raw integer key 对应的值，nil 槽位会作为 nil 返回值保留。
func Unpack(args ...runtime.Value) ([]runtime.Value, error) {
	// 无 State 入口保留给单测和纯 Go 调用，无法执行 Lua closure 元方法。
	return unpackWithState(nil, args...)
}

// unpackWithState 实现 Lua 5.3 `table.unpack`，并在 State 可用时执行 Lua closure 元方法。
//
// state 可为 nil；nil 时只支持 raw/Go closure 元方法路径。args 语义同 Unpack。
func unpackWithState(state *runtime.State, args ...runtime.Value) ([]runtime.Value, error) {
	// unpack 先解析目标 table，后续按 raw integer key 顺序读取返回值。
	list, err := tableArgument(args, 1, "unpack")
	if err != nil {
		// 第一个参数不是 table 时直接返回 Lua 参数错误。
		return nil, err
	}

	startIndex := int64(1)
	if len(args) >= 2 && !args[1].IsNil() {
		// i 参数存在且非 nil 时必须可转换为 integer；nil 等价于省略，沿用默认 1。
		convertedIndex, ok := args[1].ToInteger()
		if !ok {
			// 非整数起点不能用于 raw 序列读取。
			return nil, badArgument("unpack", 2, "integer expected")
		}
		startIndex = convertedIndex
	}

	endIndex, err := tableLength(state, list)
	if err != nil {
		// __len 元方法失败时直接传播错误。
		return nil, err
	}
	if len(args) >= 3 && !args[2].IsNil() {
		// j 参数存在且非 nil 时必须可转换为 integer；nil 等价于省略，沿用 table 长度。
		convertedIndex, ok := args[2].ToInteger()
		if !ok {
			// 非整数终点不能用于 raw 序列读取。
			return nil, badArgument("unpack", 3, "integer expected")
		}
		endIndex = convertedIndex
	}

	if startIndex > endIndex {
		// 空区间返回零个结果，而不是返回 nil。
		return nil, nil
	}

	if startIndex <= 0 && endIndex > math.MaxInt64+startIndex-1 {
		// 区间长度超过 int64 可表达范围时必然不可能返回，必须在减法溢出前失败。
		return nil, runtime.RaiseError(runtime.StringValue("too many results to unpack"))
	}

	resultCount := endIndex - startIndex + 1
	// Lua 5.3 的 unpack 会通过 lua_checkstack 预留返回槽；调用参数和额外栈槽也要计入预算。
	options := runtime.NormalizeOptions(runtime.Options{})
	if state != nil {
		// State 可用时使用嵌入方配置的栈上限。
		options = runtime.NormalizeOptions(state.Options())
	}
	stackAllowance := int64(options.MaxStackDepth) - int64(len(args)) - tableUnpackStackReserve
	if resultCount > stackAllowance {
		// 返回值数量超过栈预算时，必须在分配巨大结果切片前失败。
		if options.MaxStackDepth == runtime.DefaultMaxStackDepth {
			// 官方 errors.lua 对默认栈上限附近的 table.unpack 巨大区间期望 too many results。
			return nil, runtime.RaiseError(runtime.StringValue("too many results to unpack"))
		}
		return nil, options.CheckStackDepth(options.MaxStackDepth + 1)
	}

	results := make([]runtime.Value, 0, resultCount)
	for index := startIndex; index <= endIndex; index++ {
		// 普通读取会把 sparse 洞作为 nil 返回值保留，同时允许 __index。
		value, getErr := tableGetInteger(state, list, index)
		if getErr != nil {
			// 元方法读取失败时直接返回错误。
			return nil, getErr
		}
		results = append(results, value)
		if index == endIndex {
			// 已处理闭区间最后一个索引后立即退出，避免 maxinteger 自增溢出到 mininteger。
			break
		}
	}
	return results, nil
}

// tableArgument 按 Lua 标准库参数规则提取 table。
//
// args 使用 0-based Go 切片；position 使用 Lua 1-based 参数序号。返回错误时会携带 Lua
// error object，便于 pcall/xpcall 获取标准库参数错误。
func tableArgument(args []runtime.Value, position int, functionName string) (*runtime.Table, error) {
	// 先检查参数是否存在。
	if len(args) < position {
		// 缺失参数按 nil 处理，并报告 table expected。
		return nil, badArgument(functionName, position, "table expected")
	}
	if args[position-1].Kind != runtime.KindTable {
		// 非 table 类型不允许进入 raw table 标准库操作。
		return nil, badArgument(functionName, position, "table expected")
	}

	tableValue, ok := args[position-1].Ref.(*runtime.Table)
	if !ok || tableValue == nil {
		// KindTable 但引用负载非法属于运行期内部不一致，仍按参数错误暴露给 Lua。
		return nil, badArgument(functionName, position, "table expected")
	}

	// 返回强类型 table，调用方继续执行具体标准库逻辑。
	return tableValue, nil
}

// tableLength 按 Lua `#` 语义获取 table 长度。
//
// state 可为 nil；nil 时无法执行 Lua closure `__len`，会回退到底层 raw 长度。
func tableLength(state *runtime.State, tableValue *runtime.Table) (int64, error) {
	// 有 State 时优先调用 State 注入的 Lua closure 元方法 runner。
	if state != nil {
		value := runtime.ReferenceValue(runtime.KindTable, tableValue)
		metatable := tableValue.GetMetatable()
		if metatable != nil {
			method := metatable.RawGetString("__len")
			if method.IsNil() {
				// 元表未定义 __len 时继续使用 raw 长度。
				return tableValue.Len(), nil
			}
			results, err := callTableMetamethod(state, method, "__len", value)
			if err != nil {
				// __len 元方法错误按 Lua 调用错误传播。
				return 0, err
			}
			if len(results) == 0 {
				// 无返回值不能作为长度。
				return 0, runtime.RaiseError(runtime.StringValue("object length is not an integer"))
			}
			length, ok := results[0].ToInteger()
			if !ok {
				// table 库需要整数长度才能定位序列区。
				return 0, runtime.RaiseError(runtime.StringValue("object length is not an integer"))
			}
			return length, nil
		}
	}

	// 没有可执行 __len 时使用 raw 长度边界。
	return tableValue.Len(), nil
}

// tableGetInteger 按 Lua 普通 table 读取语义读取整数 key。
//
// 该 helper 用于 table 库需要遵守 `__index` 的位置，例如 insert/remove 的移动过程。
func tableGetInteger(state *runtime.State, tableValue *runtime.Table, key int64) (runtime.Value, error) {
	if state != nil {
		// 有 State 时允许执行 Lua closure 形式的 __index。
		return tableValue.GetWithRunner(runtime.IntegerValue(key), stateLuaMetamethodRunner(state))
	}
	// 整数 key 通过 runtime.Value 包装后进入普通 Get，允许触发 __index 链。
	return tableValue.Get(runtime.IntegerValue(key))
}

// tableSetInteger 按 Lua 普通 table 写入语义写入整数 key。
//
// 该 helper 用于 table 库需要遵守 `__newindex` 的位置，例如 insert/remove 的移动过程。
func tableSetInteger(state *runtime.State, tableValue *runtime.Table, key int64, value runtime.Value) error {
	if state != nil {
		// 有 State 时允许执行 Lua closure 形式的 __newindex。
		return tableValue.SetWithRunner(runtime.IntegerValue(key), value, stateLuaMetamethodRunner(state))
	}
	// 整数 key 通过 runtime.Value 包装后进入普通 Set，允许触发 __newindex 链。
	return tableValue.Set(runtime.IntegerValue(key), value)
}

// stateLuaMetamethodRunner 返回标准库可使用的 State 元方法执行器。
//
// name 是元方法名；args 按 Lua 元方法调用顺序传入。
func stateLuaMetamethodRunner(state *runtime.State) runtime.LuaMetamethodRunner {
	return func(method runtime.Value, name string, args ...runtime.Value) ([]runtime.Value, error) {
		// State.CallLuaClosure 复用上层注入的完整 Lua closure runner。
		return state.CallLuaClosure(method, args...)
	}
}

// callTableMetamethod 调用 table 标准库需要的 Go/Lua 元方法。
//
// method 可以是 Go closure 或 Lua closure；name 仅用于保持调用路径语义清晰。
func callTableMetamethod(state *runtime.State, method runtime.Value, name string, args ...runtime.Value) ([]runtime.Value, error) {
	if method.Kind == runtime.KindGoClosure {
		// Go closure 元方法直接执行并返回所有结果。
		if function, ok := method.Ref.(runtime.GoResultsFunction); ok {
			// GoResultsFunction 可返回多个值。
			return function(args...)
		}
		if function, ok := method.Ref.(runtime.GoFunction); ok {
			// GoFunction 只返回单个值。
			value, err := function(args...)
			if err != nil {
				// Go 元方法错误原样传播。
				return nil, err
			}
			return []runtime.Value{value}, nil
		}
	}
	if method.Kind == runtime.KindLuaClosure {
		// Lua closure 元方法通过 State runner 执行。
		return stateLuaMetamethodRunner(state)(method, name, args...)
	}
	return nil, runtime.ErrExpectedCallable
}

// integerArgument 按 Lua 标准库参数规则提取 integer。
//
// args 使用 0-based Go 切片；position 使用 Lua 1-based 参数序号。integer 与可无损转换的
// float number 都会被接受，其他类型返回 Lua 参数错误。
func integerArgument(args []runtime.Value, position int, functionName string) (int64, error) {
	// 先检查参数是否存在。
	if len(args) < position {
		// 缺失整数边界无法提供默认值，由调用方决定哪些参数可选。
		return 0, badArgument(functionName, position, "integer expected")
	}

	integerValue, ok := args[position-1].ToInteger()
	if !ok {
		// 非整数值不能作为 table 序列边界。
		return 0, badArgument(functionName, position, "integer expected")
	}

	// 返回已转换的 int64 Lua integer。
	return integerValue, nil
}

// concatElement 把 table.concat 区间内的单个元素转换为字符串片段。
//
// value 必须是 string、integer 或 number；index 用于错误消息定位。返回错误时以 Lua error
// 传播，避免调用方获得不完整拼接结果。
func concatElement(value runtime.Value, index int64) (string, error) {
	// 根据 Lua 5.3 table.concat 约束，只接受 string 和 number。
	switch value.Kind {
	case runtime.KindString:
		// string 元素原样参与拼接。
		return value.String, nil
	case runtime.KindInteger, runtime.KindNumber:
		// number 元素按 Lua 基础 number-to-string 规则转换。
		converted, ok := value.NumberToString()
		if !ok {
			// 理论上 number 分支必须可转换，失败时按非法值报错。
			return "", invalidConcatValue(index)
		}
		return converted, nil
	default:
		// nil、table、function 等值均不能参与 table.concat。
		return "", invalidConcatValue(index)
	}
}

// lessForSort 执行 table.sort 单次小于比较。
//
// comparator 为 nil 时使用 Lua 基础 `<`；comparator 为 Go 或 Lua closure 时调用该函数并按第一
// 返回值 truthiness 判断结果。错误会直接返回给 sort 调用方。
func lessForSort(state *runtime.State, left runtime.Value, right runtime.Value, comparator runtime.Value) (bool, error) {
	// 未传 comparator 时使用基础比较。
	if comparator.IsNil() {
		// number 与 number 支持 integer/float 混合比较。
		if left.IsNumber() && right.IsNumber() {
			// ToNumber 对 number 值必然成功，忽略 ok 只保留比较语义。
			leftNumber, _ := left.ToNumber()
			rightNumber, _ := right.ToNumber()
			return leftNumber < rightNumber, nil
		}
		if left.Kind == runtime.KindString && right.Kind == runtime.KindString {
			// string 基础排序按字节字典序执行。
			return left.String < right.String, nil
		}
		if result, found, err := lessMetamethodForSort(state, left, right); found || err != nil {
			// table/userdata 等复合值可通过 __lt 元方法参与默认排序。
			return result, err
		}

		// 其他类型组合在没有 comparator 时不可比较。
		return false, runtime.RaiseError(runtime.StringValue("invalid order function for sorting"))
	}

	results, err := callComparator(state, comparator, left, right)
	if err != nil {
		// comparator 自身错误必须原样传播，满足 comparator error 边界。
		return false, err
	}
	if len(results) == 0 {
		// 无返回值按 Lua 第一返回值 nil 处理，即 false。
		return false, nil
	}

	// Lua truthiness 决定 comparator 是否认为 left < right。
	return results[0].Truthy(), nil
}

// lessMetamethodForSort 尝试通过 `__lt` 元方法完成 table.sort 默认比较。
//
// state 用于执行 Lua closure 元方法；left/right 按 Lua 二元元方法规则先查左侧，再查右侧。
// found=false 表示两侧均未定义 `__lt`，调用方应返回不可比较错误。
func lessMetamethodForSort(state *runtime.State, left runtime.Value, right runtime.Value) (bool, bool, error) {
	method, found := sortLookupLtMetamethod(left)
	if !found {
		// 左侧没有 __lt 时尝试右侧元表。
		method, found = sortLookupLtMetamethod(right)
	}
	if !found {
		// 两侧都没有 __lt，调用方继续走基础不可比较错误。
		return false, false, nil
	}
	results, err := callTableMetamethod(state, method, "__lt", left, right)
	if err != nil {
		// 元方法执行错误直接传播给 table.sort。
		return false, true, err
	}
	if len(results) == 0 {
		// 无返回值按 nil 处理，即 false。
		return false, true, nil
	}
	return results[0].Truthy(), true, nil
}

// sortLookupLtMetamethod 查找 table.sort 默认比较可用的 `__lt` 元方法。
//
// 当前覆盖 table 与 userdata 的 raw 元表；基础类型已有直接比较路径或共享元表路径不属于官方
// sort.lua 断点，后续如需可扩展到 runtime.BasicTypeMetatable。
func sortLookupLtMetamethod(value runtime.Value) (runtime.Value, bool) {
	var metatable *runtime.Table
	switch value.Kind {
	case runtime.KindTable:
		// table 值从自身元表查找 __lt。
		tableValue, ok := value.Ref.(*runtime.Table)
		if !ok || tableValue == nil {
			// 损坏 table 引用不参与元方法查找。
			return runtime.NilValue(), false
		}
		metatable = tableValue.GetMetatable()
	case runtime.KindUserdata:
		// userdata 值从自身元表查找 __lt。
		userdata, ok := value.Ref.(*runtime.Userdata)
		if !ok || userdata == nil {
			// 损坏 userdata 引用不参与元方法查找。
			return runtime.NilValue(), false
		}
		metatable = userdata.GetMetatable()
	default:
		// 其他类型暂不在 table.sort 默认比较中查元方法。
		return runtime.NilValue(), false
	}
	if metatable == nil {
		// 没有元表时不存在 __lt。
		return runtime.NilValue(), false
	}
	method := metatable.RawGetString("__lt")
	if method.IsNil() {
		// __lt 缺失或显式 nil 时视为未定义。
		return runtime.NilValue(), false
	}
	return method, true
}

// callComparator 调用 table.sort 支持的 comparator。
//
// comparator 可为 Go closure 或 Lua closure；Lua closure 需要 state 上注入 Lua runner。
// 返回值保持多返回值布局，调用方只读取第一返回值。
func callComparator(state *runtime.State, comparator runtime.Value, left runtime.Value, right runtime.Value) ([]runtime.Value, error) {
	if state != nil {
		// table.sort comparator 对齐 Lua C 边界：比较函数内部不能跨该边界 yield。
		leaveBoundary := state.EnterGoCallbackBoundary()
		defer leaveBoundary()
	}
	switch function := comparator.Ref.(type) {
	case runtime.GoFunction:
		if comparator.Kind != runtime.KindGoClosure {
			// Lua closure 不能按 GoFunction 负载执行。
			return nil, badArgument("sort", 2, "function expected")
		}
		// 单返回值 GoFunction 适配为多返回值切片。
		if function == nil {
			// nil 函数负载表示 Go closure 损坏。
			return nil, badArgument("sort", 2, "function expected")
		}
		result, err := function(left, right)
		if err != nil {
			// comparator 错误原样返回，供 pcall/xpcall 捕获。
			return nil, err
		}
		return []runtime.Value{result}, nil
	case runtime.GoResultsFunction:
		if comparator.Kind != runtime.KindGoClosure {
			// Lua closure 不能按 GoResultsFunction 负载执行。
			return nil, badArgument("sort", 2, "function expected")
		}
		// 多返回值 GoResultsFunction 直接执行。
		if function == nil {
			// nil 函数负载表示 Go closure 损坏。
			return nil, badArgument("sort", 2, "function expected")
		}
		return function(left, right)
	default:
		if comparator.Kind == runtime.KindLuaClosure && state != nil {
			// Lua comparator 通过 State 注入的完整 Lua closure runner 执行。
			return state.CallLuaClosure(comparator, left, right)
		}
		// 未知 Ref 类型或缺少 State runner 表示当前 comparator 不可调用。
		return nil, badArgument("sort", 2, "function expected")
	}
}

// invalidConcatValue 构造 table.concat 元素类型错误。
//
// index 表示发生错误的 Lua 1-based 序列索引；错误对象保持稳定文本，后续可与 Lua 5.3
// golden 行为继续收敛。
func invalidConcatValue(index int64) error {
	// 生成 Lua error，调用方不应继续拼接剩余元素。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("invalid value (%s) at index %d in table for 'concat'", "nil", index)))
}

// badArgument 构造 Lua 标准库参数错误。
//
// functionName 是标准库函数名，position 是 Lua 1-based 参数序号，detail 是期望或错误原因。
func badArgument(functionName string, position int, detail string) error {
	// 标准库参数错误以 Lua string error object 传播。
	return runtime.RaiseError(runtime.StringValue(fmt.Sprintf("bad argument #%d to '%s' (%s)", position, functionName, detail)))
}
