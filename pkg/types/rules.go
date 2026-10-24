package types

// This file ports the type-compatibility rule functions from
// src/nclang/lang/types.py (is_assignable, is_comparable, is_orderable,
// join_aggregate_types, common_type, unwrap_optional_type,
// type_ref_to_descriptor, plus the internal well-formedness helpers).
// Each function header names the Python source line it mirrors.

import (
	"github.com/nodecoda/ncoda/pkg/ast"
)

// equalStrings reports whether two slices are element-wise equal (Go form of
// Python tuple equality for enum members, file constraint tuples, etc.).
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isNumeric mirrors types.py _NUMERIC_TYPES = {INT, FLOAT}.
func isNumeric(k DSLType) bool { return k == Int || k == Float }

// hasParams reports whether the descriptor carries a parameterized payload.
// It mirrors types.py's `params is not None` checks in is_assignable /
// is_comparable. File constraints are deliberately excluded: Python's
// TypeDescriptor.params never carries file constraints (they live in the
// separate file_constraints field), so a constrained file still has
// params == None.
func hasParams(d *Descriptor) bool {
	if d == nil {
		return false
	}
	return d.Elem != nil || d.Key != nil || d.Val != nil ||
		len(d.Fields) > 0 || d.EnumName != "" || len(d.EnumMembers) > 0 ||
		len(d.Params) > 0 || d.Result != nil
}

// isWellFormedEnum mirrors types.py is_well_formed_enum_type (line 264): a
// valid enum descriptor has a non-empty name and a non-empty, duplicate-free
// list of non-empty members. Python additionally requires members to be a
// tuple of str; Go []string cannot express the tuple/list or non-str member
// distinctions, so those malformed shapes have no Go representation.
func isWellFormedEnum(d *Descriptor) bool {
	if d == nil || d.Kind != Enum {
		return false
	}
	if d.EnumName == "" || len(d.EnumMembers) == 0 {
		return false
	}
	seen := make(map[string]bool, len(d.EnumMembers))
	for _, m := range d.EnumMembers {
		if m == "" {
			return false
		}
		if seen[m] {
			return false
		}
		seen[m] = true
	}
	return true
}

// aggregateTypeIsWellFormed mirrors types.py _aggregate_type_is_well_formed
// (line 287): reject malformed enum descriptors anywhere in an aggregate type.
func aggregateTypeIsWellFormed(d *Descriptor) bool {
	if d == nil {
		return true
	}
	if d.Kind == Enum {
		return isWellFormedEnum(d)
	}
	switch d.Kind {
	case Array, Optional:
		return aggregateTypeIsWellFormed(d.Elem)
	case Map:
		return aggregateTypeIsWellFormed(d.Key) && aggregateTypeIsWellFormed(d.Val)
	case Record:
		for _, f := range d.Fields {
			if !aggregateTypeIsWellFormed(f.Type) {
				return false
			}
		}
		return true
	case Function:
		for i := range d.Params {
			if !aggregateTypeIsWellFormed(&d.Params[i]) {
				return false
			}
		}
		return aggregateTypeIsWellFormed(d.Result)
	}
	return true
}

// UnwrapOptional mirrors types.py unwrap_optional_type (line 228): return the
// value descriptor beneath any well-formed optional wrappers. A malformed
// optional (no element) unwraps to itself.
func UnwrapOptional(d *Descriptor) *Descriptor {
	for d != nil && d.Kind == Optional && d.Elem != nil {
		d = d.Elem
	}
	return d
}

// IsAssignable mirrors types.py is_assignable (line 306): can source be
// assigned to target? ANY is an explicit target, never an implicit source
// conversion; same base type is assignable; INT → FLOAT widens; NULL only
// goes to OPTIONAL; arrays/maps recurse; records require the source to have a
// superset of the target's fields; enums are nominal and must be well formed.
func IsAssignable(src, dst *Descriptor) bool {
	if dst.Kind == Any {
		return true
	}
	if src.Kind == Any {
		return dst.Kind == Any
	}
	// coze-biz 平台 file 本质是 URL 字符串（image/audio/video 输入在
	// 平台侧按 string 传参）: file → string 兼容, 与 isCodePortType
	// 对 code 节点 file 端口的处理同理。参考目标等目标需要 file 真类型时
	// 由后端映射还原（upload-config 重建, 不受此处放宽影响）。
	if src.Kind == File && dst.Kind == String {
		return true
	}
	if dst.Kind == Optional {
		if dst.Elem == nil {
			return false
		}
		if src.Kind == Null {
			return true
		}
		if src.Kind == Optional {
			return src.Elem != nil && IsAssignable(src.Elem, dst.Elem)
		}
		return IsAssignable(src, dst.Elem)
	}
	if src.Kind == Null || src.Kind == Optional {
		return false
	}
	// record → map 赋值放宽：record 是"固定键 map"，传给期望
	// map<string,T> 的参数时逐字段检查（dst.Val 为 any 时恒兼容）。
	// 无期望 dict → record 推断（X212 异构字段保类型）后，数据构造
	// dict 传给期望 map 的工具参数不再误报。
	// record -> object 赋值放宽已随「object 并入 map<string,any>」（2026-09-15
	// 裁定）由下面的 record -> map 规则覆盖：合并后不存在 Kind == Object 的描述符。
	if src.Kind == Record && dst.Kind == Map {
		if dst.Key != nil && dst.Key.Kind != String {
			return false
		}
		if dst.Val == nil || dst.Val.Kind == Any {
			return true
		}
		for _, f := range src.Fields {
			if !IsAssignable(f.Type, dst.Val) {
				return false
			}
		}
		return true
	}
	if src.Kind == dst.Kind {
		if src.Kind == Enum {
			return isWellFormedEnum(src) && isWellFormedEnum(dst) &&
				src.EnumName == dst.EnumName && equalStrings(src.EnumMembers, dst.EnumMembers)
		}
		if !hasParams(src) && !hasParams(dst) {
			return true
		}
		if src.Kind == Array {
			if src.Elem == nil || dst.Elem == nil {
				return false
			}
			return IsAssignable(src.Elem, dst.Elem)
		}
		if src.Kind == Map {
			if src.Key == nil || dst.Key == nil || src.Val == nil || dst.Val == nil {
				return false
			}
			return IsAssignable(src.Key, dst.Key) && IsAssignable(src.Val, dst.Val)
		}
		if src.Kind == Record {
			// Note: Python rejects a fielded source against a bare record
			// target (target.params must be RecordType). The flat Go struct
			// cannot distinguish a zero-field RecordType from a bare record,
			// and no constructor path produces a bare record target, so the
			// superset check below treats an empty target as satisfied.
			if len(src.Fields) == 0 && len(dst.Fields) == 0 {
				return true
			}
			dstByName := make(map[string]*Descriptor, len(dst.Fields))
			for _, f := range dst.Fields {
				dstByName[f.Name] = f.Type
			}
			srcByName := make(map[string]*Descriptor, len(src.Fields))
			for _, f := range src.Fields {
				srcByName[f.Name] = f.Type
			}
			for fname, ftype := range dstByName {
				srcType, ok := srcByName[fname]
				if !ok {
					return false
				}
				if !IsAssignable(srcType, ftype) {
					return false
				}
			}
			return true
		}
		return true
	}
	// INT → FLOAT widening.
	return src.Kind == Int && dst.Kind == Float
}

// IsComparable mirrors types.py is_comparable (line 376): can left and right
// be compared with == / !=? Same type is comparable, numeric types compare
// with each other, NULL compares only with NULL or OPTIONAL, aggregates recurse,
// enums are nominal and must be well formed.
func IsComparable(a, b *Descriptor) bool {
	if a.Kind == Any || b.Kind == Any {
		return true
	}
	if a.Kind == Null || b.Kind == Null {
		other := b
		if a.Kind != Null {
			other = a
		}
		return other.Kind == Null || other.Kind == Optional
	}
	if a.Kind == Optional || b.Kind == Optional {
		// types.py requires both sides to carry OptionalType params: an
		// optional is never comparable to a non-optional parameterized type
		// (e.g. array), even when the element types would match.
		if a.Kind != Optional || b.Kind != Optional || a.Elem == nil || b.Elem == nil {
			return false
		}
		return IsComparable(a.Elem, b.Elem)
	}
	if isNumeric(a.Kind) && isNumeric(b.Kind) {
		return true
	}
	// coze-biz 平台 file 本质是 URL 字符串（IsAssignable 已放行
	// file→string 赋值）: file 与 string 可 == / != 比较（实证: S11
	// 即梦图片 url(file) 做非空条件 `image.url != ""`）。
	if (a.Kind == File && b.Kind == String) || (a.Kind == String && b.Kind == File) {
		return true
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case Enum:
		return isWellFormedEnum(a) && isWellFormedEnum(b) &&
			a.EnumName == b.EnumName && equalStrings(a.EnumMembers, b.EnumMembers)
	case Array:
		if a.Elem == nil || b.Elem == nil {
			return false
		}
		return IsComparable(a.Elem, b.Elem)
	case Map:
		if a.Key == nil || b.Key == nil || a.Val == nil || b.Val == nil {
			return false
		}
		return IsComparable(a.Key, b.Key) && IsComparable(a.Val, b.Val)
	case Record:
		if len(a.Fields) != len(b.Fields) {
			return false
		}
		// Python compares field key sets (order-insensitive) then per-name
		// types; port the same semantics with a by-name lookup.
		bByName := make(map[string]*Descriptor, len(b.Fields))
		for _, f := range b.Fields {
			bByName[f.Name] = f.Type
		}
		for _, f := range a.Fields {
			bt, ok := bByName[f.Name]
			if !ok {
				return false
			}
			if !IsComparable(f.Type, bt) {
				return false
			}
		}
		return true
	}
	if hasParams(a) || hasParams(b) {
		return a.Equal(b)
	}
	return true
}

// IsOrderable mirrors types.py is_orderable (line 427): ordering is defined
// for numeric pairs (including INT/FLOAT widening) and STRING/STRING. ANY is
// accepted only to avoid cascading diagnostics after an earlier unresolved
// type.
func IsOrderable(a, b *Descriptor) bool {
	if a.Kind == Any || b.Kind == Any {
		return true
	}
	if isNumeric(a.Kind) && isNumeric(b.Kind) {
		return true
	}
	return a.Kind == String && b.Kind == String
}

// JoinAggregateTypes mirrors types.py join_aggregate_types (line 441): join
// homogeneous aggregate members without applying operator coercions. Same
// descriptors join to themselves unless the descriptor contains a malformed
// enum anywhere (fail closed).
func JoinAggregateTypes(a, b *Descriptor) *Descriptor {
	if a == nil || b == nil {
		return nil
	}
	if a.Equal(b) {
		if !aggregateTypeIsWellFormed(a) {
			return nil
		}
		return a
	}
	if a.Kind == Any || b.Kind == Any {
		return AnyType()
	}
	if a.Kind == Null {
		return OptionalType(b)
	}
	if b.Kind == Null {
		return OptionalType(a)
	}
	if a.Kind == Optional && a.Elem != nil {
		joined := JoinAggregateTypes(a.Elem, b)
		if joined == nil {
			return nil
		}
		if joined.Kind == Optional {
			return joined
		}
		return OptionalType(joined)
	}
	if b.Kind == Optional && b.Elem != nil {
		return JoinAggregateTypes(b, a)
	}
	if isNumeric(a.Kind) && isNumeric(b.Kind) {
		return FloatType()
	}
	// file 本质是 URL 字符串（IsAssignable 已放行 file→string）：公共父
	// 类型取 string。X174 聚合消解分支赋值 img(file) / v165070.data(string)
	// 流类型 join 成 string，避免裸声明聚合变量类型冲突回退。
	if (a.Kind == File && b.Kind == String) || (a.Kind == String && b.Kind == File) {
		return StringType()
	}
	if a.Kind == Array && b.Kind == Array {
		if a.Elem == nil || b.Elem == nil {
			return nil
		}
		elem := JoinAggregateTypes(a.Elem, b.Elem)
		if elem == nil {
			return nil
		}
		return ArrayType(elem)
	}
	if a.Kind == Map && b.Kind == Map {
		if a.Key == nil || b.Key == nil || a.Val == nil || b.Val == nil {
			return nil
		}
		key := JoinAggregateTypes(a.Key, b.Key)
		val := JoinAggregateTypes(a.Val, b.Val)
		if key == nil || val == nil {
			return nil
		}
		return MapType(key, val)
	}
	if a.Kind == Record && b.Kind == Record {
		// 字面量推断 record（无期望 dict 静态键）字段级 join：同序字段
		// 逐项 join（int+float→float），字段集不一致 → 不兼容。
		if len(a.Fields) != len(b.Fields) {
			return nil
		}
		joined := make([]RecordField, 0, len(a.Fields))
		for i := range a.Fields {
			if a.Fields[i].Name != b.Fields[i].Name {
				return nil
			}
			f := JoinAggregateTypes(a.Fields[i].Type, b.Fields[i].Type)
			if f == nil {
				return nil
			}
			joined = append(joined, RecordField{Name: a.Fields[i].Name, Type: f})
		}
		return RecordType("", joined)
	}
	return nil
}

// CommonType mirrors types.py common_type (line 478): find the common type
// for a binary operation. Same type returns that type; INT + FLOAT → FLOAT;
// STRING + anything → STRING only for "+" (concatenation); ANY + T → ANY;
// NULL promotes the other side to OPTIONAL; otherwise nil (incompatible).
func CommonType(a, b *Descriptor, op string) *Descriptor {
	if a.Kind == Enum || b.Kind == Enum {
		return nil
	}
	if a.Kind == b.Kind {
		return a
	}
	if a.Kind == Any || b.Kind == Any {
		return AnyType()
	}
	if a.Kind == Null {
		return OptionalType(b)
	}
	if b.Kind == Null {
		return OptionalType(a)
	}
	// String concatenation is only valid for the + operator.
	if op == "+" && (a.Kind == String || b.Kind == String) {
		return StringType()
	}
	if isNumeric(a.Kind) && isNumeric(b.Kind) {
		return FloatType()
	}
	// file 本质是 URL 字符串（IsAssignable 已放行 file→string）：公共父
	// 类型取 string。X174 聚合消解分支赋值 img(file) / v165070.data(string)
	// 流类型 join 成 string，避免裸声明聚合变量类型冲突回退。
	if (a.Kind == File && b.Kind == String) || (a.Kind == String && b.Kind == File) {
		return StringType()
	}
	return nil
}

// cloneStrings copies a string slice so converted descriptors do not alias
// the source AST's backing arrays (Python tuples are immutable; slices are).
func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// TypeRefToDescriptor mirrors types.py type_ref_to_descriptor (line 513):
// convert an AST TypeRef to a Descriptor. The lookup callback resolves named
// types; a nil lookup or an unknown name recovers to Any (mirroring the
// Python behavior of silently returning ANY when diagnostics is None).
func TypeRefToDescriptor(ref ast.TypeRef, lookup func(string) *Descriptor) *Descriptor {
	switch r := ref.(type) {
	case *ast.FileTypeRef:
		c := r.Constraints()
		return FileTypeWithConstraints(FileConstraints{
			Types:         cloneStrings(c.Types),
			Extensions:    cloneStrings(c.Extensions),
			UploadMethods: cloneStrings(c.UploadMethods),
		})
	case *ast.PrimTypeRef:
		switch r.Name() {
		case "string":
			return StringType()
		case "int":
			return IntType()
		case "float":
			return FloatType()
		case "bool":
			return BoolType()
		case "file":
			return FileType()
		case "void":
			return VoidType()
		case "any":
			return AnyType()
		case "object":
			return ObjectType()
		default:
			return AnyType()
		}
	case *ast.ArrayTypeRef:
		return ArrayType(TypeRefToDescriptor(r.Element(), lookup))
	case *ast.MapTypeRef:
		return MapType(TypeRefToDescriptor(r.Key(), lookup), TypeRefToDescriptor(r.Value(), lookup))
	case *ast.OptionalTypeRef:
		return OptionalType(TypeRefToDescriptor(r.Value(), lookup))
	case *ast.NamedTypeRef:
		if lookup != nil {
			if d := lookup(r.Name()); d != nil {
				return d
			}
		}
		return AnyType()
	default:
		return AnyType()
	}
}
