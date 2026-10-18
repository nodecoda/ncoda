// Package pygen lowers IR builtin calls to pyast, the Python-subset AST.
// Every mapping is byte-equivalent to the string-template shim it replaces
// (calcBuiltinPython in pkg/ir/build.go): for each builtin template,
// the serialized pyast lower equals the template's output byte-for-byte
// (verified by golden tests in this package and the full probe gates).
//
// Coverage: all 16 calcBuiltinPython builtins are lowerable
// (len, trim, join, to_string, int, float, replace, first_non_empty, range,
// flatten, div, repeat, concat, enumerate, contains, split).
package pygen

import "github.com/nodecoda/ncoda/pkg/pyast"

// LowerBuiltin returns a pyast expression for a calculation builtin call.
// The caller guarantees arity; args are already-lowered pyast expressions.
func LowerBuiltin(name string, args []pyast.Expr) (pyast.Expr, bool) {
	switch name {
	case "len":
		// len(x)
		return &pyast.CallExpr{Func: "len", Args: []pyast.Expr{args[0]}}, true
	case "trim":
		// x.strip()
		return &pyast.CallExpr{Obj: args[0], Func: "strip"}, true
	case "join":
		// b.join(a)
		return &pyast.CallExpr{
			Obj:  args[1],
			Func: "join",
			Args: []pyast.Expr{args[0]},
		}, true
	case "to_string":
		// str(x)
		return &pyast.CallExpr{Func: "str", Args: []pyast.Expr{args[0]}}, true
	case "int":
		// int(x)
		return &pyast.CallExpr{Func: "int", Args: []pyast.Expr{args[0]}}, true
	case "float":
		// float(x)
		return &pyast.CallExpr{Func: "float", Args: []pyast.Expr{args[0]}}, true
	case "replace":
		// a.replace(b, c)
		return &pyast.CallExpr{
			Obj:  args[0],
			Func: "replace",
			Args: []pyast.Expr{args[1], args[2]},
		}, true
	case "first_non_empty":
		// next((str(u) for u in (arr or []) if u and str(u).strip()), "")
		return &pyast.CallExpr{
			Func: "next",
			Args: []pyast.Expr{
				&pyast.GeneratorExpr{
					Elt: &pyast.CallExpr{Func: "str", Args: []pyast.Expr{&pyast.NameExpr{Name: "u"}}},
					Clauses: []pyast.CompClause{
						{
							Vars: []string{"u"},
							Iter: &pyast.ParenExpr{X: &pyast.BinaryExpr{
								Op:    "or",
								Left:  args[0],
								Right: &pyast.ListExpr{},
							}},
							Ifs: []pyast.Expr{
								&pyast.BinaryExpr{
									Op:   "and",
									Left: &pyast.NameExpr{Name: "u"},
									Right: &pyast.CallExpr{
										Obj:  &pyast.CallExpr{Func: "str", Args: []pyast.Expr{&pyast.NameExpr{Name: "u"}}},
										Func: "strip",
									},
								},
							},
						},
					},
				},
				&pyast.StringExpr{Value: ""},
			},
		}, true
	case "range":
		// list(range(a, b))
		return &pyast.CallExpr{
			Func: "list",
			Args: []pyast.Expr{
				&pyast.CallExpr{Func: "range", Args: []pyast.Expr{args[0], args[1]}},
			},
		}, true
	case "flatten":
		// [str(x) for g in (arr or []) if isinstance(g, list) for x in g]
		return &pyast.ListCompExpr{
			Elt: &pyast.CallExpr{Func: "str", Args: []pyast.Expr{&pyast.NameExpr{Name: "x"}}},
			Clauses: []pyast.CompClause{
				{
					Vars: []string{"g"},
					Iter: &pyast.ParenExpr{X: &pyast.BinaryExpr{
						Op:    "or",
						Left:  args[0],
						Right: &pyast.ListExpr{},
					}},
					Ifs: []pyast.Expr{
						&pyast.CallExpr{
							Func: "isinstance",
							Args: []pyast.Expr{&pyast.NameExpr{Name: "g"}, &pyast.NameExpr{Name: "list"}},
						},
					},
				},
				{
					Vars: []string{"x"},
					Iter: &pyast.NameExpr{Name: "g"},
				},
			},
		}, true
	case "div":
		// (a // b)
		return &pyast.ParenExpr{
			X: &pyast.BinaryExpr{Op: "//", Left: args[0], Right: args[1]},
		}, true
	case "repeat":
		// [a] * b
		return &pyast.BinaryExpr{
			Op:    "*",
			Left:  &pyast.ListExpr{Elts: []pyast.Expr{args[0]}},
			Right: args[1],
		}, true
	case "concat":
		// (a + b)
		return &pyast.ParenExpr{
			X: &pyast.BinaryExpr{Op: "+", Left: args[0], Right: args[1]},
		}, true
	case "enumerate":
		// [{"index": i, "value": v} for i, v in enumerate(arr)]
		return &pyast.ListCompExpr{
			Elt: &pyast.DictExpr{Items: []pyast.DictItem{
				{Key: &pyast.StringExpr{Value: "index"}, Value: &pyast.NameExpr{Name: "i"}},
				{Key: &pyast.StringExpr{Value: "value"}, Value: &pyast.NameExpr{Name: "v"}},
			}},
			Clauses: []pyast.CompClause{
				{
					Vars: []string{"i", "v"},
					Iter: &pyast.CallExpr{Func: "enumerate", Args: []pyast.Expr{args[0]}},
				},
			},
		}, true
	case "contains":
		// (b in a)
		return &pyast.ParenExpr{
			X: &pyast.BinaryExpr{Op: "in", Left: args[1], Right: args[0]},
		}, true
	case "split":
		// a.split(b)
		return &pyast.CallExpr{
			Obj:  args[0],
			Func: "split",
			Args: []pyast.Expr{args[1]},
		}, true
	case "empty":
		// not x —— string/array/object 空判定统一（空串/空表/空字典均
		// falsy；None 亦 falsy → null 视为空，与平台运行期一致）
		return &pyast.UnaryExpr{Op: "not", Operand: args[0]}, true
	}
	return nil, false
}

// IsLowerable reports whether the builtin has a byte-equivalent pyast
// lowering registered right now. All calcBuiltinPython builtins are
// lowerable; the set is kept in sync with calcBuiltinOutType.
func IsLowerable(name string) bool {
	switch name {
	case "len", "trim", "join", "to_string", "int", "float", "replace",
		"first_non_empty", "range", "flatten", "div", "repeat",
		"concat", "enumerate", "contains", "split", "empty":
		return true
	}
	return false
}

// Template renders a calculation builtin call as a Python source string,
// given already-rendered argument strings. It is the parity-compat layer of
// the generator: the string templates calcBuiltinPython (pkg/ir) and
// renderCall (pkg/backend/dify/legacy) used to carry, consolidated
// here so both legacy consumers and the IR fallback call ONE definition
// (P5.3 — parity by construction; byte equivalence with the pyast form is
// locked by TestTemplateMatchesPyast). Template's arg order follows the
// ncoda signature, identical to the historical templates.
func Template(name string, args []string) (string, bool) {
	switch name {
	case "len":
		return "len(" + args[0] + ")", true
	case "trim":
		return args[0] + ".strip()", true
	case "join":
		return args[1] + ".join(" + args[0] + ")", true
	case "to_string":
		return "str(" + args[0] + ")", true
	case "int":
		return "int(" + args[0] + ")", true
	case "float":
		return "float(" + args[0] + ")", true
	case "replace":
		return args[0] + ".replace(" + args[1] + ", " + args[2] + ")", true
	case "first_non_empty":
		return "next((str(u) for u in (" + args[0] + " or []) if u and str(u).strip()), \"\")", true
	case "range":
		return "list(range(" + args[0] + ", " + args[1] + "))", true
	case "flatten":
		return "[str(x) for g in (" + args[0] + " or []) if isinstance(g, list) for x in g]", true
	case "div":
		return "(" + args[0] + " // " + args[1] + ")", true
	case "repeat":
		return "[" + args[0] + "] * " + args[1], true
	case "concat":
		return "(" + args[0] + " + " + args[1] + ")", true
	case "enumerate":
		return "[{\"index\": i, \"value\": v} for i, v in enumerate(" + args[0] + ")]", true
	case "contains":
		return "(" + args[1] + " in " + args[0] + ")", true
	case "split":
		return args[0] + ".split(" + args[1] + ")", true
	case "empty":
		return "not (" + args[0] + ")", true
	}
	return "", false
}
