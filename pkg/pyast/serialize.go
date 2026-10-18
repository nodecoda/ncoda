package pyast

// Serialize renders a pyast Module to a deterministic Python source string.
// The output is byte-equivalent to the existing calcBuiltinPython /
// renderConditionPython / renderLiteralPython shims for identical inputs.
// Indentation is 4 spaces; dict keys are sorted; output is minimal but
// readable (no unnecessary parens beyond what the AST requires).

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Serialize renders a Module to Python source.
func Serialize(m *Module) string {
	var b strings.Builder
	// Module-level imports come first (self-sufficiency contract, PYTHON-SUBSET-GENERATOR.md §18):
	// every generated shim must be independently executable, so stdlib
	// dependencies (json) are imported at the top of the module — never
	// after the def main header (that position would be a syntax error).
	for _, imp := range m.Imports {
		b.WriteString("import ")
		b.WriteString(imp)
		b.WriteString("\n")
	}
	b.WriteString("def main(")
	for i, p := range m.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
		if p.Type != "" {
			b.WriteString(": ")
			b.WriteString(p.Type)
		}
	}
	b.WriteString(") -> dict:\n")
	for _, st := range m.Body {
		writeStmt(&b, st, 1)
	}
	return b.String()
}

// SerializeExpr renders a single pyast expression to a Python source string.
// This is the expression-level equivalent of Serialize, useful for inlining
// pyast fragments into existing generated code.
func SerializeExpr(e Expr) string {
	var b strings.Builder
	writeExpr(&b, e, 0)
	return b.String()
}

func writeStmt(b *strings.Builder, s Stmt, indent int) {
	pad := strings.Repeat("    ", indent)
	switch st := s.(type) {
	case *ReturnStmt:
		b.WriteString(pad)
		b.WriteString("return ")
		writeExpr(b, st.Value, 0)
		b.WriteString("\n")
	case *AssignStmt:
		b.WriteString(pad)
		b.WriteString(st.Name)
		b.WriteString(" = ")
		writeExpr(b, st.Value, 0)
		b.WriteString("\n")
	case *IfStmt:
		b.WriteString(pad)
		b.WriteString("if ")
		writeExpr(b, st.Cond, 0)
		b.WriteString(":\n")
		writeStmt(b, st.Then, indent+1)
		if st.Else != nil {
			b.WriteString(pad)
			b.WriteString("else:\n")
			writeStmt(b, st.Else, indent+1)
		}
	case *ForStmt:
		b.WriteString(pad)
		b.WriteString("for ")
		b.WriteString(st.Var)
		b.WriteString(" in ")
		writeExpr(b, st.Iter, 0)
		b.WriteString(":\n")
		writeStmt(b, st.Body, indent+1)
	case *PassStmt:
		b.WriteString(pad)
		b.WriteString("pass\n")
	}
}

// FloatLiteralText 渲染 python 子集里的浮点字面量：**取整值必须显式补 `.0`**，
// 否则 `1.0` 写成 `1` ⇒ 反向词法读成 IntExpr（`pkg/pyast/lexer.go` 的
// IsFloat）⇒ shim 正文不再逐字可逆、且 `float` 语义退化成 `int`
// （R-98 的另一面；与反编译端 `render_ncoda.go::ncodaFloatText` 同一条规则、
// 同一理由 —— 两侧都是「拼写即类型载体」的位置）。
//
// 本函数是 python 侧该规则的**唯一落点**：`internal/ir` 里所有把 ncoda 浮点
// 字面量写进 python 正文的地方都走它。
func FloatLiteralText(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if strings.Contains(s, ".") {
		return s
	}
	return s + ".0"
}

func writeExpr(b *strings.Builder, e Expr, depth int) {
	switch ex := e.(type) {
	case *NoneExpr:
		b.WriteString("None")
	case *BoolExpr:
		if ex.Value {
			b.WriteString("True")
		} else {
			b.WriteString("False")
		}
	case *IntExpr:
		fmt.Fprint(b, ex.Value)
	case *FloatExpr:
		b.WriteString(FloatLiteralText(ex.Value))
	case *StringExpr:
		b.WriteString(quoteString(ex.Value))
	case *NameExpr:
		b.WriteString(ex.Name)
	case *CallExpr:
		if ex.Obj != nil {
			writeExpr(b, ex.Obj, depth)
			b.WriteString(".")
		}
		b.WriteString(ex.Func)
		b.WriteString("(")
		for i, a := range ex.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			writeExpr(b, a, depth)
		}
		for i, kw := range ex.Kwargs {
			if len(ex.Args) > 0 || i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(kw.Name)
			b.WriteString("=")
			writeExpr(b, kw.Value, depth)
		}
		b.WriteString(")")
	case *BinaryExpr:
		left := parenNeeded(ex.Left, ex, true)
		right := parenNeeded(ex.Right, ex, false)
		if left {
			b.WriteString("(")
			writeExpr(b, ex.Left, depth+1)
			b.WriteString(")")
		} else {
			writeExpr(b, ex.Left, depth+1)
		}
		b.WriteString(" ")
		b.WriteString(ex.Op)
		b.WriteString(" ")
		if right {
			b.WriteString("(")
			writeExpr(b, ex.Right, depth+1)
			b.WriteString(")")
		} else {
			writeExpr(b, ex.Right, depth+1)
		}
	case *UnaryExpr:
		b.WriteString("(")
		b.WriteString(ex.Op)
		if ex.Op != "-" {
			// `not` renders `(not x)`; unary minus renders `(-x)` with no
			// space, mirroring the legacy renderers.
			b.WriteString(" ")
		}
		writeExpr(b, ex.Operand, depth+1)
		b.WriteString(")")
	case *ParenExpr:
		b.WriteString("(")
		writeExpr(b, ex.X, depth+1)
		b.WriteString(")")
	case *SubscriptExpr:
		writeExpr(b, ex.Obj, depth+1)
		b.WriteString("[")
		writeExpr(b, ex.Index, depth+1)
		b.WriteString("]")
	case *AttributeExpr:
		writeExpr(b, ex.Obj, depth+1)
		b.WriteString(".")
		b.WriteString(ex.Attr)
	case *TernaryExpr:
		b.WriteString("(")
		writeExpr(b, ex.Then, depth+1)
		b.WriteString(" if ")
		writeExpr(b, ex.Cond, depth+1)
		b.WriteString(" else ")
		writeExpr(b, ex.Else, depth+1)
		b.WriteString(")")
	case *ListCompExpr:
		b.WriteString("[")
		writeExpr(b, ex.Elt, depth+1)
		writeCompClauses(b, ex.Clauses, depth)
		b.WriteString("]")
	case *DictCompExpr:
		b.WriteString("{")
		writeExpr(b, ex.Key, depth+1)
		b.WriteString(": ")
		writeExpr(b, ex.Value, depth+1)
		writeCompClauses(b, ex.Clauses, depth)
		b.WriteString("}")
	case *GeneratorExpr:
		b.WriteString("(")
		writeExpr(b, ex.Elt, depth+1)
		writeCompClauses(b, ex.Clauses, depth)
		b.WriteString(")")
	case *DictExpr:
		if len(ex.Items) == 0 {
			b.WriteString("{}")
			return
		}
		// Sort keys for deterministic output
		sorted := make([]DictItem, len(ex.Items))
		copy(sorted, ex.Items)
		sort.Slice(sorted, func(i, j int) bool {
			return compareExpr(sorted[i].Key, sorted[j].Key) < 0
		})
		b.WriteString("{")
		for i, item := range sorted {
			if i > 0 {
				b.WriteString(", ")
			}
			writeExpr(b, item.Key, depth+1)
			b.WriteString(": ")
			writeExpr(b, item.Value, depth+1)
		}
		b.WriteString("}")
	case *ListExpr:
		if len(ex.Elts) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[")
		for i, el := range ex.Elts {
			if i > 0 {
				b.WriteString(", ")
			}
			writeExpr(b, el, depth+1)
		}
		b.WriteString("]")
	case *ConcatExpr:
		// Flat `a + b + c` join — no parens, mirroring the legacy template
		// renderer (renderConditionPython joins parts with " + ").
		for i, part := range ex.Parts {
			if i > 0 {
				b.WriteString(" + ")
			}
			writeExpr(b, part, depth+1)
		}
	}
}

// parenNeeded reports whether the child expression needs parentheses when
// placed as a child of the given parent expression.
func writeCompClauses(b *strings.Builder, clauses []CompClause, depth int) {
	for _, c := range clauses {
		b.WriteString(" for ")
		for i, v := range c.Vars {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(v)
		}
		b.WriteString(" in ")
		writeExpr(b, c.Iter, depth)
		for _, f := range c.Ifs {
			b.WriteString(" if ")
			writeExpr(b, f, depth)
		}
	}
}

func parenNeeded(child Expr, parent Expr, asLeft bool) bool {
	// Only BinaryExpr parents ask (the serializer calls this only from the
	// BinaryExpr case). Legacy renderConditionPython parenthesizes EVERY
	// binary node, so a binary child of a binary gets parens — this
	// reproduces the legacy bytes for same-precedence chains
	// ((a + b) + c, not a + b + c). Every other child is either an atom or
	// self-delimiting (unary/ternary/ParenExpr emit their own parens; call/
	// subscript/attribute/dict/list/concat parse unambiguously as children),
	// so adding parens here would double them.
	_, childIsBinary := child.(*BinaryExpr)
	return childIsBinary
}

// compareExpr compares two expressions for deterministic ordering (used for
// dict keys). Returns -1, 0, or 1.
func compareExpr(a, b Expr) int {
	sa := exprString(a)
	sb := exprString(b)
	if sa < sb {
		return -1
	}
	if sa > sb {
		return 1
	}
	return 0
}

func exprString(e Expr) string {
	var b strings.Builder
	writeExpr(&b, e, 0)
	return b.String()
}

// quoteString produces a Python string literal (double-quoted, with
// standard escapes). Byte-equivalent to pythonJSONString in pkg/ir/build.go:
// ASCII printable (0x20-0x7e) via literal, control chars via \uXXXX, surrogates
// via \uXXXX\uXXXX. This ensures pyast serialization and the legacy string-template
// path produce identical output for any string value.
func quoteString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\b':
			b.WriteString("\\b")
		case '\f':
			b.WriteString("\\f")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			switch {
			case r >= 0x20 && r <= 0x7e:
				b.WriteRune(r)
			case r <= 0xffff:
				fmt.Fprintf(&b, "\\u%04x", r)
			default:
				r -= 0x10000
				fmt.Fprintf(&b, "\\u%04x\\u%04x", 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
