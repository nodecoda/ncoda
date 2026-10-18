package shim

import "github.com/nodecoda/ncoda/pkg/ncoda/typesystem"

// paramCastScalarTable / paramCastContainerTable 是**参数边界类型归一**的闭集
// （一处定义，R-128 第二刀）。
//
// 规则的一句话：**目标参数声明的类型是权威；流过来的值的类型与它不同且两者之间
// 没有隐式可赋值关系时，raising 必须在值边界写一次显式转换**，把平台弱类型事实
// （DB/语料 schema 只是"声明"，运行期由 JS 语义兜住）固化成合法 ncoda；不写转换
// 的结果不是"少个注解"，是**产物在正编译时被强校验拒收**（TYPE_MISMATCH），即真实
// 平台导出失去产物。
//
// 表里**只列不可赋值**的组合（可赋值的组合在 types.IsAssignable 里已经放行，
// 列进来会让今天通过的源多出一个载体 ⇒ 产物变化）：
//
//	float → int   不可（收窄）      int → float 可（加宽，故不列）
//	file  → string 可（平台 file 即 URL 串，故不列）     string → file 由 fileParams 处理
//
// 表外的组合（`any` 源、`array<T> ← string` 等方向可疑者）**不猜**：原样返回，
// 由正编译的强校验点名 —— 「失配 ≠ 静默丢弃」，也 ≠ 猜一个转换。
var ParamCastScalarTable = map[string]map[string]string{
	// 标量源 → 目标 string：to_string(x)（pygen lowering = str(x)，与 JS String(x) 同解）
	"string": {"int": "to_string", "float": "to_string", "bool": "to_string"},
	// 容器源 → 目标 string：json.stringify(x)（平台槽位要的就是文本）
	"__container_to_string": {"array": "json.stringify", "map": "json.stringify", "record": "json.stringify"},
	// 目标 int：float → int(x)（收窄）；string → int(x)（平台存字符串数字）
	"int": {"float": "int", "string": "int"},
	// 目标 float：string → float(x)；平台也存字符串数字（G14 新增 cast）
	"float": {"string": "float", "bool": "float"},
}

// paramSrcKind 把 raising 侧解析出的值类型归到**闭集小类**（string/int/float/bool/
// file/array/map/record/any）。刻意不复用 ncodaTypeName：那个函数在 object 分支需要
// registrar 才能给出名字，且会登记命名 record / 记 UNKNOWN 归因（有副作用），
// 这里只做分类。不可推断 → "any"（调用方据此不猜）。
func ParamSrcKind(ti *typesystem.TypeInfo) string {
	if ti == nil {
		return "any"
	}
	switch ti.Type {
	case typesystem.DataTypeString, typesystem.DataTypeTime:
		return "string"
	case typesystem.DataTypeInteger:
		return "int"
	case typesystem.DataTypeNumber:
		return "float"
	case typesystem.DataTypeBoolean:
		return "bool"
	case typesystem.DataTypeFile:
		return "file"
	case typesystem.DataTypeArray:
		return "array"
	case typesystem.DataTypeObject:
		if len(ti.Properties) > 0 {
			return "record"
		}
		return "map"
	}
	return "any"
}
