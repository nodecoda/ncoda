// Package pyast implements a typed AST for the normalized Python subset
// (docs/ideas-todo/PYTHON-SUBSET-GENERATOR.md). The AST mirrors the
// platform backend pattern: typed nodes + deterministic serializer.
// Backends (IR builder, the reference target legacy) consume this AST instead of
// hand-written string templates, guaranteeing byte-identical output.
package pyast

// Node is the interface for all Python-subset AST nodes.
type Node interface{ pyastNode() }

// Stmt is a Python statement.
type Stmt interface {
	Node
	stmtNode()
}

// Expr is a Python expression.
type Expr interface {
	Node
	exprNode()
}

// ── Module (top-level) ─────────────────────────────────────────────────

// Module is a single-function Python module: def main(params) -> dict: {body}
type Module struct {
	// Imports are emitted as `import <name>` lines before def main. A shim
	// that references a module-level name (json.loads, json.dumps) must
	// declare it here: platform code sandboxes (the reference target/Coze) make stdlib
	// modules importable but do NOT auto-bind them — missing import is a
	// runtime NameError no static gate can catch.
	Imports []string
	Params  []Param
	Body    []Stmt
}

// Param is a named function parameter with optional type hint.
type Param struct {
	Name string
	// Type is the exact annotation text emitted after ": " in the
	// signature (e.g. "list", "str", "int"). "" emits no annotation.
	// Only valid Python annotations are allowed — the serializer emits
	// this verbatim.
	Type string
}

func (m *Module) pyastNode() {}

// ── Statements ─────────────────────────────────────────────────────────

type ReturnStmt struct{ Value Expr }
type AssignStmt struct {
	Name  string
	Value Expr
}
type IfStmt struct {
	Cond       Expr
	Then, Else Stmt
}
type ForStmt struct {
	Var  string
	Iter Expr
	Body Stmt
}
type PassStmt struct{}

func (s *ReturnStmt) pyastNode() {}
func (s *ReturnStmt) stmtNode()  {}
func (s *AssignStmt) pyastNode() {}
func (s *AssignStmt) stmtNode()  {}
func (s *IfStmt) pyastNode()     {}
func (s *IfStmt) stmtNode()      {}
func (s *ForStmt) pyastNode()    {}
func (s *ForStmt) stmtNode()     {}
func (s *PassStmt) pyastNode()   {}
func (s *PassStmt) stmtNode()    {}

// ── Expressions ────────────────────────────────────────────────────────

type CallExpr struct {
	Func string // function or method name
	Args []Expr
	Obj  Expr // nil for plain calls; set for method calls (obj.method(args))
	// Kwargs is the keyword-argument list (name=value), rendered after the
	// positional args. Runtime shim evaluators may ignore it or use it
	// (json.dumps(..., ensure_ascii=False)); the serializer round-trips it.
	Kwargs []CallKwarg
}

// CallKwarg is one keyword argument (name=value) of a call.
type CallKwarg struct {
	Name  string
	Value Expr
}

func (e *CallExpr) pyastNode() {}
func (e *CallExpr) exprNode()  {}

type NameExpr struct{ Name string }

func (e *NameExpr) pyastNode() {}
func (e *NameExpr) exprNode()  {}

type StringExpr struct{ Value string }

func (e *StringExpr) pyastNode() {}
func (e *StringExpr) exprNode()  {}

type IntExpr struct{ Value int64 }

func (e *IntExpr) pyastNode() {}
func (e *IntExpr) exprNode()  {}

type FloatExpr struct{ Value float64 }

func (e *FloatExpr) pyastNode() {}
func (e *FloatExpr) exprNode()  {}

type BoolExpr struct{ Value bool }

func (e *BoolExpr) pyastNode() {}
func (e *BoolExpr) exprNode()  {}

type NoneExpr struct{}

func (e *NoneExpr) pyastNode() {}
func (e *NoneExpr) exprNode()  {}

type BinaryExpr struct {
	Op    string // +, -, *, /, //, %, ==, !=, <, >, <=, >=, and, or, in, not in
	Left  Expr
	Right Expr
}

func (e *BinaryExpr) pyastNode() {}
func (e *BinaryExpr) exprNode()  {}

type UnaryExpr struct {
	Op      string // -, not
	Operand Expr
}

func (e *UnaryExpr) pyastNode() {}
func (e *UnaryExpr) exprNode()  {}

type SubscriptExpr struct {
	Obj   Expr
	Index Expr
}

func (e *SubscriptExpr) pyastNode() {}
func (e *SubscriptExpr) exprNode()  {}

// ParenExpr wraps an expression in parentheses for explicit grouping.
// The serializer always emits the parens regardless of precedence.
// Needed for byte-equivalent lowerings where the legacy template emits
// outer parens (div/concat/contains).
type ParenExpr struct{ X Expr }

func (e *ParenExpr) pyastNode() {}
func (e *ParenExpr) exprNode()  {}

type AttributeExpr struct {
	Obj  Expr
	Attr string
}

func (e *AttributeExpr) pyastNode() {}
func (e *AttributeExpr) exprNode()  {}

type TernaryExpr struct {
	Cond, Then, Else Expr
}

func (e *TernaryExpr) pyastNode() {}
func (e *TernaryExpr) exprNode()  {}

// CompClause is one "for ... in ... if ..." segment of a comprehension.
// Vars holds one or more loop target names; len(Vars) > 1 renders as a
// tuple-unpacking target "(a, b)".
type CompClause struct {
	Vars []string
	Iter Expr
	Ifs  []Expr
}

type ListCompExpr struct {
	Elt     Expr
	Clauses []CompClause
}

func (e *ListCompExpr) pyastNode() {}
func (e *ListCompExpr) exprNode()  {}

type DictCompExpr struct {
	Key, Value Expr
	Clauses    []CompClause
}

func (e *DictCompExpr) pyastNode() {}
func (e *DictCompExpr) exprNode()  {}

type GeneratorExpr struct {
	Elt     Expr
	Clauses []CompClause
}

func (e *GeneratorExpr) pyastNode() {}
func (e *GeneratorExpr) exprNode()  {}

type DictExpr struct{ Items []DictItem }
type DictItem struct{ Key, Value Expr }

func (e *DictExpr) pyastNode() {}
func (e *DictExpr) exprNode()  {}

type ListExpr struct{ Elts []Expr }

func (e *ListExpr) pyastNode() {}
func (e *ListExpr) exprNode()  {}

// ConcatExpr is a flat `a + b + c` string-concat chain (template-string
// lowering). The legacy renderer joins template parts with " + ", so parts
// are never nested (a nested template becomes a str(...) call argument).
type ConcatExpr struct{ Parts []Expr }

func (e *ConcatExpr) pyastNode() {}
func (e *ConcatExpr) exprNode()  {}

// StripParens removes all explicit ParenExpr wrappers (recursively) so two
// structurally identical expressions compare equal regardless of redundant
// parenthesization (compiler `??` shim arms are emitted by both the pyast
// serializer and the legacy string renderer with different paren counts).
func StripParens(e Expr) Expr {
	for {
		pe, ok := e.(*ParenExpr)
		if !ok {
			return e
		}
		e = pe.X
	}
}

// SameExpr reports structural equality after stripping explicit parens.
// Used by the decompile shim reverse to fold `(a) if (a) is not None else b`
// back into ncoda `a ?? b` regardless of which compiler path emitted the shim.
func SameExpr(a, b Expr) bool {
	return exprString(StripParens(a)) == exprString(StripParens(b))
}
