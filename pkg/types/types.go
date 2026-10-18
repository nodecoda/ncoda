package types

import "strings"

// This file ports the DSL type vocabulary and the TypeDescriptor from
// src/nclang/lang/types.py (the descriptor layer). The rule functions live in
// rules.go. TypePublicationCategory / TypeCategoryMetadata and freeze_type
// belong to the capability/binding layers and are deliberately not ported
// here (YAGNI; see task-7 brief and report).

// DSLType enumerates the 14 frontend type kinds, mirroring
// nclang.lang.types.DSLType.
type DSLType int

const (
	String DSLType = iota
	Int
	Float
	Bool
	File
	Void
	Any
	Object
	Null
	Optional
	Array
	Map
	Record
	Enum
	Function
)

// String returns the canonical type name, equal to the Python enum value
// (types.py DSLType values: "string", "int", ...).
func (t DSLType) String() string {
	switch t {
	case String:
		return "string"
	case Int:
		return "int"
	case Float:
		return "float"
	case Bool:
		return "bool"
	case File:
		return "file"
	case Void:
		return "void"
	case Any:
		return "any"
	case Object:
		return "object"
	case Null:
		return "null"
	case Optional:
		return "optional"
	case Array:
		return "array"
	case Map:
		return "map"
	case Record:
		return "record"
	case Enum:
		return "enum"
	case Function:
		return "function"
	}
	return "unknown"
}

// FileConstraints carries source-level constraints on a file type, mirroring
// nclang.lang.types.FileTypeConstraints (and pkg/ast.FileTypeConstraints,
// ported in Task 6). The task brief's "kind/pdf/local_file" phrasing is stale;
// the reference implementation uses types / extensions / upload methods.
type FileConstraints struct {
	Types         []string
	Extensions    []string
	UploadMethods []string
}

// RecordField is one named field of a record type.
type RecordField struct {
	Name string
	Type *Descriptor
}

// Descriptor is the Go form of types.py TypeDescriptor: a DSLType kind plus
// the parameterized payload for the kinds that carry one (see DESIGN.md 3.2).
// It is built once and treated as immutable after construction; the ported
// rules never mutate a descriptor.
type Descriptor struct {
	Kind DSLType
	// Elem is the element type for Array and the wrapped value for Optional.
	Elem *Descriptor
	// Key and Val are the key/value types for Map.
	Key, Val *Descriptor
	// Fields is the ordered field list for Record.
	Fields []RecordField
	// EnumName and EnumMembers carry the nominal identity for Enum.
	EnumName    string
	EnumMembers []string
	// Params and Result carry the signature for Function. Params is a value
	// slice (brief contract); Function signatures are never recursive.
	Params []Descriptor
	Result *Descriptor
	// File carries the constraints for File.
	File FileConstraints
	// JSONParseArray marks an Array descriptor produced by
	// json.parse<array<T>> (G12). Element access on such arrays supports
	// any constant index (G12-2 lowers it to an extraction shim), whereas
	// registry tool arrays (E5-b) expose only item-0 member access.
	JSONParseArray bool
}

// ── Convenience constructors (types.py TypeDescriptor static methods) ──

// StringType builds a string descriptor.
func StringType() *Descriptor { return &Descriptor{Kind: String} }

// IntType builds an int descriptor.
func IntType() *Descriptor { return &Descriptor{Kind: Int} }

// FloatType builds a float descriptor.
func FloatType() *Descriptor { return &Descriptor{Kind: Float} }

// BoolType builds a bool descriptor.
func BoolType() *Descriptor { return &Descriptor{Kind: Bool} }

// FileType builds a bare file descriptor (no constraints).
func FileType() *Descriptor { return &Descriptor{Kind: File} }

// FileTypeWithConstraints builds a file descriptor with source-level
// constraints (types.py TypeDescriptor.file(constraints)).
func FileTypeWithConstraints(c FileConstraints) *Descriptor {
	return &Descriptor{Kind: File, File: c}
}

// VoidType builds a void descriptor.
func VoidType() *Descriptor { return &Descriptor{Kind: Void} }

// AnyType builds an any descriptor.
func AnyType() *Descriptor { return &Descriptor{Kind: Any} }

// ObjectType builds the **open-object** descriptor: a JSON object whose keys
// and value types are not statically known.
//
// 2026-09-15 用户裁定：**`object` 并入 `map<string, any>`**（同一 Descriptor）。
// 理由（实证）：语言层区分 Object 与 Map<string,any>，但 IR 立刻把两者抹成同一个
// `TObject({})`（pkg/ir/build_node.go）⇒ **没有任何一层兑现这个区分**，它只
// 制造失配：两者互不可赋值（IsAssignable 里 Map ↛ Object、Object ↛ Map），于是
// 同一份 wire 形状在「作者写 object」与「作者写 map<string,any>」两条源上给出不同
// 判定（`llm<object>` 允许、`llm<map<string,any>>` 拒绝），往返时又撞成
// `TYPE_MISMATCH expected Type(object), got Type(map, …)`。
//
// 平台侧只有**两种**容器形状（`object`+properties 有 schema / `object` 无 schema），
// 语言侧留**两个** token（命名 record = 有 schema；map<string,any> = 无 schema）即构成
// 闭集；第三个 token（object）没有对应物，故并入 map。
//
// 保留 ObjectType() 这个构造器名：源码 token `object` 仍然合法（不删语法，只归一语义）。
func ObjectType() *Descriptor { return MapType(StringType(), AnyType()) }

// NullType builds a null descriptor.
func NullType() *Descriptor { return &Descriptor{Kind: Null} }

// OptionalType wraps elem in an optional descriptor.
func OptionalType(elem *Descriptor) *Descriptor { return &Descriptor{Kind: Optional, Elem: elem} }

// ArrayType wraps elem in an array descriptor.
func ArrayType(elem *Descriptor) *Descriptor { return &Descriptor{Kind: Array, Elem: elem} }

// MapType builds a map descriptor with key k and value v.
func MapType(k, v *Descriptor) *Descriptor { return &Descriptor{Kind: Map, Key: k, Val: v} }

// RecordType builds a record descriptor. The name is accepted for API parity
// with Python (types.py RecordType) but is not stored: the descriptor shape
// carries fields only, and record compatibility in this layer is structural
// (see is_assignable: source fields must be a superset of target fields).
func RecordType(name string, fields []RecordField) *Descriptor {
	_ = name // reserved; record name is not part of this layer's rules
	return &Descriptor{Kind: Record, Fields: fields}
}

// EnumType builds a nominal enum descriptor with an ordered member list.
func EnumType(name string, members []string) *Descriptor {
	// Snapshot the member slice, mirroring Python's tuple(members) in
	// TypeDescriptor.enum: later mutation of the caller's slice must not
	// affect the descriptor (test_enum verifies this).
	return &Descriptor{Kind: Enum, EnumName: name, EnumMembers: append([]string(nil), members...)}
}

// FunctionType builds a function descriptor with an ordered parameter list and
// a result type.
func FunctionType(params []Descriptor, result *Descriptor) *Descriptor {
	return &Descriptor{Kind: Function, Params: params, Result: result}
}

// Equal reports whether d and other are structurally equal, mirroring the
// Python dataclass equality (types.py TypeDescriptor.__eq__): enum identity is
// nominal (name plus ordered members), function params compare in order, file
// constraints compare structurally. A nil descriptor is equal only to nil.
//
// Record deviation (Minor A, brief contract): Python RecordType.fields is a
// dict whose equality is order-insensitive; the Go Fields slice compares in
// order ("RecordType 字段序敏感" per the task brief). Field-name identity is
// enforced by field 0..n positional comparison.
func (d *Descriptor) Equal(other *Descriptor) bool {
	if d == nil || other == nil {
		return d == other
	}
	if d.Kind != other.Kind {
		return false
	}
	switch d.Kind {
	case Optional, Array:
		return d.Elem.Equal(other.Elem)
	case Map:
		return d.Key.Equal(other.Key) && d.Val.Equal(other.Val)
	case Record:
		if len(d.Fields) != len(other.Fields) {
			return false
		}
		for i := range d.Fields {
			if d.Fields[i].Name != other.Fields[i].Name {
				return false
			}
			if !d.Fields[i].Type.Equal(other.Fields[i].Type) {
				return false
			}
		}
		return true
	case Enum:
		return d.EnumName == other.EnumName && equalStrings(d.EnumMembers, other.EnumMembers)
	case Function:
		if len(d.Params) != len(other.Params) {
			return false
		}
		for i := range d.Params {
			if !d.Params[i].Equal(&other.Params[i]) {
				return false
			}
		}
		return d.Result.Equal(other.Result)
	case File:
		return equalStrings(d.File.Types, other.File.Types) &&
			equalStrings(d.File.Extensions, other.File.Extensions) &&
			equalStrings(d.File.UploadMethods, other.File.UploadMethods)
	default:
		// Primitives and void: kind equality is enough.
		return true
	}
}

// IsWellFormedEnum mirrors types.py is_well_formed_enum_type (line 264); the
// rule lives in rules.go (isWellFormedEnum) — this is the exported surface
// the passes consume (type_pass.py visit_field_access_expr /
// visit_request_input_stmt).
func IsWellFormedEnum(d *Descriptor) bool { return isWellFormedEnum(d) }

// String renders the Python TypeDescriptor repr (types.py __repr__, line
// 222): "Type(<base>)" for unparameterized kinds, "Type(<base>,
// <Params>)" for parameterized ones, with the dataclass reprs
// ArrayType(element=...)/MapType(key=..., value=...)/OptionalType(value=...)
// /RecordType(name=..., fields={...})/EnumType(name=..., members=(...))
// /FunctionType(params=(...), return_type=...). The type pass embeds this
// text in its diagnostics (type_pass.py f-strings), so the exact shape is
// part of the parity contract. Two recorded deviations from Python:
//
//   - record names are dropped by the Task 7 record rule, so the name
//     renders as the empty string (Python shows the declared name);
//   - nil descriptors render as Type(any) (Python never formats None).
func (d *Descriptor) String() string {
	if d == nil {
		return "Type(any)"
	}
	name := func(s string) string { return "'" + s + "'" }
	switch d.Kind {
	case Array:
		return "Type(array, ArrayType(element=" + d.Elem.String() + "))"
	case Map:
		return "Type(map, MapType(key=" + d.Key.String() + ", value=" + d.Val.String() + "))"
	case Optional:
		return "Type(optional, OptionalType(value=" + d.Elem.String() + "))"
	case Record:
		var sb strings.Builder
		sb.WriteString("Type(record, RecordType(name='', fields={")
		for i, f := range d.Fields {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(name(f.Name) + ": " + f.Type.String())
		}
		sb.WriteString("}))")
		return sb.String()
	case Enum:
		members := ""
		for i, m := range d.EnumMembers {
			if i > 0 {
				members += ", "
			}
			members += "'" + m + "'"
		}
		if len(d.EnumMembers) == 1 {
			members += ","
		}
		return "Type(enum, EnumType(name='" + d.EnumName + "', members=(" + members + ")))"
	case Function:
		var sb strings.Builder
		sb.WriteString("Type(function, FunctionType(params=(")
		for i := range d.Params {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(d.Params[i].String())
		}
		if len(d.Params) == 1 {
			sb.WriteString(",")
		}
		sb.WriteString("), return_type=")
		if d.Result != nil {
			sb.WriteString(d.Result.String())
		} else {
			sb.WriteString("Type(any)")
		}
		sb.WriteString("))")
		return sb.String()
	default:
		return "Type(" + d.Kind.String() + ")"
	}
}
