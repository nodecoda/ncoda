package pyast

import "testing"

// TestSerialize_exprs: golden tests for the expression serializer.
func TestSerializeExpr(t *testing.T) {
	cases := []struct {
		name string
		in   Expr
		want string
	}{
		{
			name: "name",
			in:   &NameExpr{Name: "input"},
			want: "input",
		},
		{
			name: "string",
			in:   &StringExpr{Value: "hello"},
			want: `"hello"`,
		},
		{
			name: "string-escape",
			in:   &StringExpr{Value: "a\nb\t\"c\\"},
			want: `"a\nb\t\"c\\"`,
		},
		{
			name: "int",
			in:   &IntExpr{Value: 42},
			want: "42",
		},
		{
			name: "float",
			in:   &FloatExpr{Value: 3.5},
			want: "3.5",
		},
		{
			name: "bool-true",
			in:   &BoolExpr{Value: true},
			want: "True",
		},
		{
			name: "bool-false",
			in:   &BoolExpr{Value: false},
			want: "False",
		},
		{
			name: "none",
			in:   &NoneExpr{},
			want: "None",
		},
		{
			name: "call-len",
			in:   &CallExpr{Func: "len", Args: []Expr{&NameExpr{Name: "x"}}},
			want: "len(x)",
		},
		{
			name: "method-strip",
			in:   &CallExpr{Obj: &NameExpr{Name: "s"}, Func: "strip", Args: nil},
			want: "s.strip()",
		},
		{
			name: "binary-add",
			in:   &BinaryExpr{Op: "+", Left: &NameExpr{Name: "a"}, Right: &NameExpr{Name: "b"}},
			want: "a + b",
		},
		{
			name: "binary-nested-paren",
			in: &BinaryExpr{
				Op:    "*",
				Left:  &NameExpr{Name: "a"},
				Right: &BinaryExpr{Op: "+", Left: &NameExpr{Name: "b"}, Right: &NameExpr{Name: "c"}},
			},
			want: "a * (b + c)",
		},
		{
			name: "unary-not",
			in:   &UnaryExpr{Op: "not", Operand: &NameExpr{Name: "x"}},
			want: "(not x)",
		},
		{
			name: "unary-minus",
			in:   &UnaryExpr{Op: "-", Operand: &NameExpr{Name: "x"}},
			want: "(-x)",
		},
		{
			name: "subscript",
			in:   &SubscriptExpr{Obj: &NameExpr{Name: "xs"}, Index: &IntExpr{Value: 0}},
			want: "xs[0]",
		},
		{
			name: "attribute",
			in:   &AttributeExpr{Obj: &NameExpr{Name: "obj"}, Attr: "value"},
			want: "obj.value",
		},
		{
			name: "ternary",
			in: &TernaryExpr{
				Cond: &NameExpr{Name: "c"},
				Then: &StringExpr{Value: "yes"},
				Else: &StringExpr{Value: "no"},
			},
			want: `("yes" if c else "no")`,
		},
		{
			name: "list-empty",
			in:   &ListExpr{},
			want: "[]",
		},
		{
			name: "list",
			in:   &ListExpr{Elts: []Expr{&IntExpr{Value: 1}, &IntExpr{Value: 2}}},
			want: "[1, 2]",
		},
		{
			name: "dict-empty",
			in:   &DictExpr{},
			want: "{}",
		},
		{
			name: "dict",
			in: &DictExpr{Items: []DictItem{
				{Key: &StringExpr{Value: "a"}, Value: &IntExpr{Value: 1}},
				{Key: &StringExpr{Value: "b"}, Value: &IntExpr{Value: 2}},
			}},
			want: `{"a": 1, "b": 2}`,
		},
		{
			name: "dict-sorted-keys",
			in: &DictExpr{Items: []DictItem{
				{Key: &StringExpr{Value: "b"}, Value: &IntExpr{Value: 2}},
				{Key: &StringExpr{Value: "a"}, Value: &IntExpr{Value: 1}},
			}},
			want: `{"a": 1, "b": 2}`,
		},
		{
			name: "listcomp",
			in: &ListCompExpr{
				Elt: &NameExpr{Name: "x"},
				Clauses: []CompClause{
					{Vars: []string{"x"}, Iter: &NameExpr{Name: "xs"}},
				},
			},
			want: "[x for x in xs]",
		},
		{
			name: "listcomp-with-if",
			in: &ListCompExpr{
				Elt: &NameExpr{Name: "x"},
				Clauses: []CompClause{
					{Vars: []string{"x"}, Iter: &NameExpr{Name: "xs"}, Ifs: []Expr{&NameExpr{Name: "p"}}},
				},
			},
			want: "[x for x in xs if p]",
		},
		{
			name: "listcomp-multi-clause-tuple",
			in: &ListCompExpr{
				Elt: &CallExpr{Func: "str", Args: []Expr{&NameExpr{Name: "x"}}},
				Clauses: []CompClause{
					{Vars: []string{"g"}, Iter: &NameExpr{Name: "arr"}},
					{Vars: []string{"x"}, Iter: &NameExpr{Name: "g"}},
				},
			},
			want: "[str(x) for g in arr for x in g]",
		},
		{
			name: "listcomp-tuple-unpack",
			in: &ListCompExpr{
				Elt: &DictExpr{Items: []DictItem{
					{Key: &StringExpr{Value: "index"}, Value: &NameExpr{Name: "i"}},
					{Key: &StringExpr{Value: "value"}, Value: &NameExpr{Name: "v"}},
				}},
				Clauses: []CompClause{
					{Vars: []string{"i", "v"}, Iter: &CallExpr{Func: "enumerate", Args: []Expr{&NameExpr{Name: "xs"}}}},
				},
			},
			want: `[{"index": i, "value": v} for i, v in enumerate(xs)]`,
		},
		{
			name: "dictcomp",
			in: &DictCompExpr{
				Key:   &NameExpr{Name: "x"},
				Value: &NameExpr{Name: "x"},
				Clauses: []CompClause{
					{Vars: []string{"x"}, Iter: &NameExpr{Name: "xs"}},
				},
			},
			want: "{x: x for x in xs}",
		},
		{
			name: "generator-next",
			in: &GeneratorExpr{
				Elt: &CallExpr{Func: "str", Args: []Expr{&NameExpr{Name: "u"}}},
				Clauses: []CompClause{
					{Vars: []string{"u"}, Iter: &NameExpr{Name: "urls"}},
				},
			},
			want: "(str(u) for u in urls)",
		},
		{
			name: "paren-expr",
			in:   &ParenExpr{X: &BinaryExpr{Op: "+", Left: &NameExpr{Name: "a"}, Right: &NameExpr{Name: "b"}}},
			want: "(a + b)",
		},
		{
			name: "concat-flat",
			in: &ConcatExpr{Parts: []Expr{
				&StringExpr{Value: "a"},
				&CallExpr{Func: "str", Args: []Expr{&NameExpr{Name: "x"}}},
				&StringExpr{Value: "b"},
			}},
			want: `"a" + str(x) + "b"`,
		},
		{
			name: "concat-single",
			in:   &ConcatExpr{Parts: []Expr{&StringExpr{Value: "solo"}}},
			want: `"solo"`,
		},
		{
			name: "call-multi-args",
			in:   &CallExpr{Func: "range", Args: []Expr{&IntExpr{Value: 1}, &IntExpr{Value: 10}}},
			want: "range(1, 10)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SerializeExpr(c.in)
			if got != c.want {
				t.Errorf("SerializeExpr(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

// TestSerialize_module: golden tests for the module serializer.
func TestSerializeModule(t *testing.T) {
	m := &Module{
		Params: []Param{{Name: "input"}},
		Body: []Stmt{&ReturnStmt{
			Value: &CallExpr{
				Func: "len",
				Args: []Expr{&AttributeExpr{
					Obj:  &NameExpr{Name: "input"},
					Attr: "items",
				}},
			},
		}},
	}
	want := `def main(input) -> dict:
    return len(input.items)
`
	got := Serialize(m)
	if got != want {
		t.Errorf("Serialize module mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}

	// Typed params emit the annotation verbatim (emitMapOp: items: list).
	m2 := &Module{
		Params: []Param{{Name: "items", Type: "list"}},
		Body: []Stmt{&ReturnStmt{
			Value: &ListCompExpr{
				Elt:     &NameExpr{Name: "item"},
				Clauses: []CompClause{{Vars: []string{"item"}, Iter: &NameExpr{Name: "items"}}},
			},
		}},
	}
	want2 := `def main(items: list) -> dict:
    return [item for item in items]
`
	if got2 := Serialize(m2); got2 != want2 {
		t.Errorf("typed module mismatch:\ngot:\n%s\nwant:\n%s", got2, want2)
	}
}

func TestQuoteString(t *testing.T) {
	got := quoteString("a\nb")
	if got != `"a\nb"` {
		t.Errorf("quoteString = %q, want %q", got, `"a\nb"`)
	}
}
