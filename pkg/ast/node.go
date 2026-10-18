// Package ast defines the SourceAST (syntax tree) for the NodeCoda DSL.
//
// This is a direct port of the procedural AST node definitions in
// src/nclang/lang/ast.py (read-only oracle). It is the Layer-1
// representation of the three-layer design (SourceAST -> TypedBoundProgram
// -> WorkflowAST): a pure syntax tree, independent of the Dify graph AST.
//
// Immutability is by convention: node fields are unexported, values are
// assigned through constructors, and read back through getters. There is no
// physical freeze (Go has none); the parser assigns the source span right
// after construction, mirroring `node.span = self._span_from(start)` in
// parser.py. Every node carries a NodeID assigned from a package-level
// incrementing counter, mirroring the Python ProcNode identity semantics.
//
// Slice-aliasing caveat: getters for slice fields (e.g. Block.Stmts,
// Program.Functions) return the underlying slice, sharing its backing array
// with the node. This aliasing is intentional (zero-copy binding between
// layers); treat getter results as read-only and copy when you need to
// mutate them.
package ast

import (
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/nodecoda/ncoda/pkg/token"
)

// NodecodaLanguageIdentity is the language identity emitted by the
// compiler. Mirrors NODECODA_LANGUAGE_IDENTITY in lang/ast.py.
const NodecodaLanguageIdentity = "nodecoda/1"

// NodeID uniquely identifies a constructed AST node. IDs are assigned from
// a package-level incrementing counter at construction time and are never
// reused within one compiler process.
type NodeID int

// Span is the source region a node was parsed from. A zero Span means the
// node was constructed programmatically (e.g. a default) or the span has
// not been assigned yet.
type Span struct {
	Start token.Location
	End   token.Location
}

// Node is implemented by every AST node. NodeID and Span are set at
// construction; SetSpan lets the parser attach source locations after
// construction (Python: `node.span = ...`).
type Node interface {
	NodeID() NodeID
	Span() Span
	SetSpan(Span)
}

// Expr is implemented by every expression node.
type Expr interface {
	Node
	exprNode()
}

// Stmt is implemented by every statement node.
type Stmt interface {
	Node
	stmtNode()
}

// TypeRef is implemented by every type-reference node.
type TypeRef interface {
	Node
	typeRefNode()
}

// base carries the shared identity/span state of every node. It is embedded
// in every concrete node type so NodeID/Span/SetSpan are promoted.
type base struct {
	id   NodeID
	span Span
}

func (b *base) NodeID() NodeID { return b.id }
func (b *base) Span() Span     { return b.span }
func (b *base) SetSpan(s Span) { b.span = s }

// nextID returns a fresh, strictly increasing NodeID. The counter is
// package-level, mirroring the Python ProcNode identity assignment.
var idCounter atomic.Int64

func nextID() NodeID {
	return NodeID(idCounter.Add(1))
}

func newBase() base { return base{id: nextID()} }

// reprNode renders a Python-style repr: TypeName(field=value, ...). It is
// the Go equivalent of ProcNode.__repr__ in lang/ast.py: it enumerates the
// concrete node's own fields, skipping the embedded identity base. Only used
// for diagnostics; never for serialization.
func reprNode(n Node) string {
	v := reflect.ValueOf(n)
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "nil"
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return fmt.Sprintf("%v", v.Interface())
	}
	t := v.Type()
	var parts []string
	for i := 0; i < v.NumField(); i++ {
		f := t.Field(i)
		if f.Name == "base" { // embedded identity state, not a syntax field
			continue
		}
		if f.Anonymous {
			// Embedded node value, e.g. ForExpr inside ParallelForExpr.
			parts = append(parts, formatNodeValue(v.Field(i)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", f.Name, formatNodeValue(v.Field(i))))
	}
	return fmt.Sprintf("%s(%s)", t.Name(), strings.Join(parts, ", "))
}

// formatNodeValue renders one field value for reprNode without descending
// into the whole subtree. Node pointers and interfaces render through their
// String() method (one level of Python-like nesting); slices/maps render as
// element counts.
// usableValue lifts the read-only flag from a value obtained through an
// unexported struct field so Interface() and Stringer lookups work. The
// re-wrapped value is addressable because reprNode dereferences a node
// pointer; this is diagnostics-only and never mutates the node.
func usableValue(fv reflect.Value) reflect.Value {
	if fv.CanInterface() {
		return fv
	}
	if fv.CanAddr() {
		return reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem()
	}
	return reflect.Value{}
}

func formatNodeValue(fv reflect.Value) string {
	if !fv.CanInterface() {
		// Unexported field: re-wrap via its address to render it (the
		// struct is addressable since reprNode dereferences a pointer).
		fv = usableValue(fv)
		if !fv.IsValid() {
			return "<unexported>"
		}
	}
	if fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return "<nil>"
		}
		if s, ok := fv.Interface().(fmt.Stringer); ok {
			return s.String()
		}
		return fmt.Sprintf("*%s", fv.Type().Elem().Name())
	}
	switch fv.Kind() {
	case reflect.Slice, reflect.Map:
		if fv.IsNil() {
			return "[]"
		}
		return fmt.Sprintf("[%d items]", fv.Len())
	case reflect.Struct:
		if fv.CanAddr() {
			if s, ok := fv.Addr().Interface().(fmt.Stringer); ok {
				return s.String()
			}
		}
		return fmt.Sprintf("%v", fv.Interface())
	case reflect.Interface:
		if fv.IsNil() {
			return "<nil>"
		}
		if s, ok := fv.Interface().(fmt.Stringer); ok {
			return s.String()
		}
		return fmt.Sprintf("%v", fv.Interface())
	default:
		return fmt.Sprintf("%v", fv.Interface())
	}
}
