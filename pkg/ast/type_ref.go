package ast

// This file ports the type-reference nodes from src/nclang/lang/ast.py
// (type references section) plus the FileTypeConstraints helper carried by
// FileTypeRef (lang/types.py FileTypeConstraints).

// FileTypeConstraints carries source-level constraints on a file type
// reference: allowed types, extensions, and upload methods. Mirrors
// nclang.lang.types.FileTypeConstraints (tuples in Python, slices here).
type FileTypeConstraints struct {
	Types         []string
	Extensions    []string
	UploadMethods []string
}

// PrimTypeRef is a primitive type reference: string, int, float, bool, file,
// void, any.
type PrimTypeRef struct {
	base
	name string
}

// NewPrimTypeRef constructs a PrimTypeRef.
func NewPrimTypeRef(name string) *PrimTypeRef { return &PrimTypeRef{base: newBase(), name: name} }

// Name returns the primitive type name.
func (n *PrimTypeRef) Name() string { return n.name }

func (n *PrimTypeRef) typeRefNode()   {}
func (n *PrimTypeRef) String() string { return reprNode(n) }

// FileTypeRef is a file type with source-level type, extension, and upload
// constraints. Its compatibility name is always "file" (ast.py FileTypeRef).
type FileTypeRef struct {
	base
	constraints FileTypeConstraints
}

// NewFileTypeRef constructs a FileTypeRef.
func NewFileTypeRef(constraints FileTypeConstraints) *FileTypeRef {
	return &FileTypeRef{base: newBase(), constraints: constraints}
}

// Constraints returns the file type constraints.
func (n *FileTypeRef) Constraints() FileTypeConstraints { return n.constraints }

// Name returns the compatibility name "file".
func (n *FileTypeRef) Name() string { return "file" }

func (n *FileTypeRef) typeRefNode()   {}
func (n *FileTypeRef) String() string { return reprNode(n) }

// ArrayTypeRef is an array type reference: array[T] or T[].
type ArrayTypeRef struct {
	base
	element TypeRef
}

// NewArrayTypeRef constructs an ArrayTypeRef.
func NewArrayTypeRef(element TypeRef) *ArrayTypeRef {
	if element == nil {
		element = NewPrimTypeRef("any")
	}
	return &ArrayTypeRef{base: newBase(), element: element}
}

// Element returns the element type.
func (n *ArrayTypeRef) Element() TypeRef { return n.element }

func (n *ArrayTypeRef) typeRefNode()   {}
func (n *ArrayTypeRef) String() string { return reprNode(n) }

// MapTypeRef is a map type reference: map<K, V>.
type MapTypeRef struct {
	base
	key   TypeRef
	value TypeRef
}

// NewMapTypeRef constructs a MapTypeRef.
func NewMapTypeRef(key, value TypeRef) *MapTypeRef {
	if key == nil {
		key = NewPrimTypeRef("string")
	}
	if value == nil {
		value = NewPrimTypeRef("any")
	}
	return &MapTypeRef{base: newBase(), key: key, value: value}
}

// Key returns the key type.
func (n *MapTypeRef) Key() TypeRef { return n.key }

// Value returns the value type.
func (n *MapTypeRef) Value() TypeRef { return n.value }

func (n *MapTypeRef) typeRefNode()   {}
func (n *MapTypeRef) String() string { return reprNode(n) }

// NamedTypeRef is a user-defined type reference: SearchResult, Conversation.
type NamedTypeRef struct {
	base
	name string
}

// NewNamedTypeRef constructs a NamedTypeRef.
func NewNamedTypeRef(name string) *NamedTypeRef { return &NamedTypeRef{base: newBase(), name: name} }

// Name returns the type name.
func (n *NamedTypeRef) Name() string { return n.name }

func (n *NamedTypeRef) typeRefNode()   {}
func (n *NamedTypeRef) String() string { return reprNode(n) }

// OptionalTypeRef is an optional value type reference: T?.
type OptionalTypeRef struct {
	base
	value TypeRef
}

// NewOptionalTypeRef constructs an OptionalTypeRef.
func NewOptionalTypeRef(value TypeRef) *OptionalTypeRef {
	if value == nil {
		value = NewPrimTypeRef("any")
	}
	return &OptionalTypeRef{base: newBase(), value: value}
}

// Value returns the wrapped type.
func (n *OptionalTypeRef) Value() TypeRef { return n.value }

func (n *OptionalTypeRef) typeRefNode()   {}
func (n *OptionalTypeRef) String() string { return reprNode(n) }
