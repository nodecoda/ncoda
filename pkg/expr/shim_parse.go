package expr

// shim_parse.go — M4 synthetic-shim 解析器（Python 子集 → pyast）。
//
// 语法（与 IR pyast 生成端闭合对应;ParseShim 是 Serialize 的逆映射）:
//
//	module    := import* funcdef
//	import    := "import" NAME
//	funcdef   := "def" "main" "(" [NAME ("," NAME)*] ")" "->" "dict" ":" stmt+
//	stmt      := assign | return
//	assign    := NAME "=" expr
//	return    := "return" expr
//	expr      := ternary
//	ternary   := or ("if" or "else" expr)?
//	or        := and ("or" and)*
//	and       := not ("and" not)*
//	not       := "not"* comparison
//	comparison:= arith (("=="|"!="|"<"|">"|"<="|">="|"in"|"not" "in") arith)*
//	arith     := term (("+"|"-") term)*
//	term      := unary (("*"|"/"|"//"|"%") unary)*
//	unary     := ("-"|"+")* postfix
//	postfix   := primary ("(" args ")" | "." NAME | "[" expr "]")*
//	primary   := NAME | INT | FLOAT | STRING | "True" | "False" | "None"
//	           | "(" expr ")" | "[" [expr ("," expr)*] "]" | "{" dict "}"
//	args      := [expr ("," expr)*] ["," NAME "=" expr ("," NAME "=" expr)*]
//
// 只解析生成端会产出的形状;其它输入报错（RExpressionUnsupported 在节点层
// 包装）。解析错误永不 panic。

import (
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/pyast"
)

// ParseShim parses synthetic shim source into a pyast Module.
func ParseShim(src string) (*pyast.Module, error) {
	toks, err := shimLex(src)
	if err != nil {
		return nil, err
	}
	p := &shimParser{toks: toks}
	return p.parseModule()
}

type shimParser struct {
	toks []stok
	pos  int
}

func (p *shimParser) cur() stok { return p.toks[p.pos] }
func (p *shimParser) next() stok {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *shimParser) expectOp(op string) (stok, error) {
	t := p.cur()
	if t.kind != stOp || t.text != op {
		return t, tokErr(t, "expected %q, got %q", op, t.text)
	}
	p.next()
	return t, nil
}

func (p *shimParser) expectKeyword(kw string) (stok, error) {
	t := p.cur()
	if t.kind != stKeyword || t.text != kw {
		return t, tokErr(t, "expected keyword %q, got %q", kw, t.text)
	}
	p.next()
	return t, nil
}

func (p *shimParser) expectIdent() (stok, error) {
	t := p.cur()
	if t.kind != stIdent {
		return t, tokErr(t, "expected identifier, got %q", t.text)
	}
	p.next()
	return t, nil
}

func (p *shimParser) parseModule() (*pyast.Module, error) {
	m := &pyast.Module{}
	// imports
	for p.cur().kind == stKeyword && p.cur().text == "import" {
		p.next()
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		m.Imports = append(m.Imports, name.text)
	}
	// funcdef
	if _, err := p.expectKeyword("def"); err != nil {
		return nil, err
	}
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if name.text != "main" {
		return nil, tokErr(name, "shim entry must be main, got %q", name.text)
	}
	if _, err := p.expectOp("("); err != nil {
		return nil, err
	}
	for p.cur().kind != stOp || p.cur().text != ")" {
		prm, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		m.Params = append(m.Params, pyast.Param{Name: prm.text})
		if t := p.cur(); t.kind == stOp && t.text == "," {
			p.next()
			continue
		}
		break
	}
	if _, err := p.expectOp(")"); err != nil {
		return nil, err
	}
	if _, err := p.expectOp("-"); err != nil {
		return nil, err
	}
	if _, err := p.expectOp(">"); err != nil {
		return nil, err
	}
	rt, err := p.expectIdent()
	if err != nil || rt.text != "dict" {
		return nil, tokErr(rt, "shim must return dict, got %q", rt.text)
	}
	if _, err := p.expectOp(":"); err != nil {
		return nil, err
	}
	// body statements
	for p.cur().kind != stEOF {
		if p.cur().kind == stKeyword && p.cur().text == "return" {
			p.next()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			m.Body = append(m.Body, &pyast.ReturnStmt{Value: val})
			continue
		}
		if p.cur().kind == stIdent {
			lhs, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			if _, err := p.expectOp("="); err != nil {
				return nil, err
			}
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			m.Body = append(m.Body, &pyast.AssignStmt{Name: lhs.text, Value: val})
			continue
		}
		return nil, tokErr(p.cur(), "unexpected token %q in shim body", p.cur().text)
	}
	if len(m.Body) == 0 {
		return nil, tokErr(p.cur(), "shim body is empty")
	}
	return m, nil
}

// ── expressions ─────────────────────────────────────────────────────────

func (p *shimParser) parseExpr() (pyast.Expr, error) {
	// ternary: then if cond else else
	then, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if t := p.cur(); t.kind == stKeyword && t.text == "if" {
		p.next()
		cond, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expectKeyword("else"); err != nil {
			return nil, err
		}
		els, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &pyast.TernaryExpr{Cond: cond, Then: then, Else: els}, nil
	}
	return then, nil
}

func (p *shimParser) parseOr() (pyast.Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		if t.kind != stKeyword || t.text != "or" {
			break
		}
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &pyast.BinaryExpr{Op: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *shimParser) parseAnd() (pyast.Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		if t.kind != stKeyword || t.text != "and" {
			break
		}
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &pyast.BinaryExpr{Op: "and", Left: left, Right: right}
	}
	return left, nil
}

func (p *shimParser) parseNot() (pyast.Expr, error) {
	if t := p.cur(); t.kind == stKeyword && t.text == "not" {
		p.next()
		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &pyast.UnaryExpr{Op: "not", Operand: operand}, nil
	}
	return p.parseComparison()
}

var shimCmpOps = map[string]bool{
	"==": true, "!=": true, "<": true, ">": true, "<=": true, ">=": true, "in": true,
}

func (p *shimParser) parseComparison() (pyast.Expr, error) {
	left, err := p.parseArith()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		var op string
		if t.kind == stOp && shimCmpOps[t.text] {
			op = t.text
			p.next()
		} else if t.kind == stKeyword && t.text == "in" {
			op = "in"
			p.next()
		} else if t.kind == stKeyword && t.text == "not" && p.toks[p.pos+1].kind == stKeyword && p.toks[p.pos+1].text == "in" {
			op = "not in"
			p.next()
			p.next()
		} else {
			break
		}
		right, err := p.parseArith()
		if err != nil {
			return nil, err
		}
		left = &pyast.BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *shimParser) parseArith() (pyast.Expr, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		if t.kind != stOp || (t.text != "+" && t.text != "-") {
			break
		}
		p.next()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &pyast.BinaryExpr{Op: t.text, Left: left, Right: right}
	}
	return left, nil
}

func (p *shimParser) parseTerm() (pyast.Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		if t.kind != stOp || (t.text != "*" && t.text != "/" && t.text != "%") {
			break
		}
		p.next()
		op := t.text
		if t.text == "/" && p.cur().kind == stOp && p.cur().text == "/" {
			op = "//"
			p.next()
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &pyast.BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *shimParser) parseUnary() (pyast.Expr, error) {
	if t := p.cur(); t.kind == stOp && (t.text == "-" || t.text == "+") {
		p.next()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &pyast.UnaryExpr{Op: t.text, Operand: operand}, nil
	}
	return p.parsePostfix()
}

func (p *shimParser) parsePostfix() (pyast.Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		switch {
		case t.kind == stOp && t.text == "(":
			call, err := p.parseCall(expr)
			if err != nil {
				return nil, err
			}
			expr = call
		case t.kind == stOp && t.text == ".":
			p.next()
			attr, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			expr = &pyast.AttributeExpr{Obj: expr, Attr: attr.text}
		case t.kind == stOp && t.text == "[":
			p.next()
			idx, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expectOp("]"); err != nil {
				return nil, err
			}
			expr = &pyast.SubscriptExpr{Obj: expr, Index: idx}
		default:
			return expr, nil
		}
	}
}

func (p *shimParser) parseCall(obj pyast.Expr) (pyast.Expr, error) {
	// obj may be a plain name (func) or an AttributeExpr (json.loads /
	// obj.method). pyast CallExpr keeps Func string + Obj for method calls;
	// a dotted func name (json.loads) collapses into Func="json.loads".
	p.next() // consume "("
	var args []pyast.Expr
	var kwargs []pyast.CallKwarg
	seenKw := false
	for {
		t := p.cur()
		if t.kind == stOp && t.text == ")" {
			p.next()
			break
		}
		// keyword argument: NAME "=" expr
		if !seenKw && t.kind == stIdent && p.toks[p.pos+1].kind == stOp && p.toks[p.pos+1].text == "=" {
			p.next()
			p.next()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			kwargs = append(kwargs, pyast.CallKwarg{Name: t.text, Value: val})
			seenKw = true
		} else {
			arg, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
		}
		if t2 := p.cur(); t2.kind == stOp && t2.text == "," {
			p.next()
			continue
		}
		if p.cur().kind != stOp || p.cur().text != ")" {
			return nil, tokErr(p.cur(), "expected , or ) in call, got %q", p.cur().text)
		}
	}
	// method call: obj.method(args)
	if attr, ok := obj.(*pyast.AttributeExpr); ok {
		return &pyast.CallExpr{Obj: attr.Obj, Func: attr.Attr, Args: args, Kwargs: kwargs}, nil
	}
	name, ok := obj.(*pyast.NameExpr)
	if !ok {
		return nil, tokErr(p.cur(), "call target is not a name")
	}
	return &pyast.CallExpr{Func: name.Name, Args: args, Kwargs: kwargs}, nil
}

func (p *shimParser) parsePrimary() (pyast.Expr, error) {
	t := p.cur()
	switch t.kind {
	case stString:
		p.next()
		return &pyast.StringExpr{Value: t.text}, nil
	case stInt:
		p.next()
		n, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return nil, tokErr(t, "bad integer %q", t.text)
		}
		return &pyast.IntExpr{Value: n}, nil
	case stFloat:
		p.next()
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, tokErr(t, "bad float %q", t.text)
		}
		return &pyast.FloatExpr{Value: f}, nil
	case stIdent:
		p.next()
		return &pyast.NameExpr{Name: t.text}, nil
	case stKeyword:
		switch t.text {
		case "True":
			p.next()
			return &pyast.BoolExpr{Value: true}, nil
		case "False":
			p.next()
			return &pyast.BoolExpr{Value: false}, nil
		case "None":
			p.next()
			return &pyast.NoneExpr{}, nil
		}
		return nil, tokErr(t, "unexpected keyword %q in expression", t.text)
	case stOp:
		switch t.text {
		case "(":
			p.next()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			// 括号生成器表达式 `(elt for x in xs if ...)`（pygen 的
			// first_non_empty 形态：`next((str(u) for u in ...), "")`）。
			if p.cur().kind == stKeyword && p.cur().text == "for" {
				clauses, err := p.parseCompClauses()
				if err != nil {
					return nil, err
				}
				if _, err := p.expectOp(")"); err != nil {
					return nil, err
				}
				return &pyast.GeneratorExpr{Elt: e, Clauses: clauses}, nil
			}
			if _, err := p.expectOp(")"); err != nil {
				return nil, err
			}
			return collapseParens(e), nil
		case "[":
			return p.parseList()
		case "{":
			return p.parseDict()
		}
	}
	return nil, tokErr(t, "unexpected token %q in expression", t.text)
}

// collapseParens removes redundant explicit-paren wrappers around
// self-delimiting expressions. The serializer ALWAYS emits parens for
// ternary/unary nodes and for its own ParenExpr nodes, so a parser-side
// ParenExpr around them would double the parens on every round-trip
// (((a if b else c)) grows one layer per pass). BinaryExpr and atoms are
// kept: legacy shim bytes parenthesize binary chains explicitly
// ((a + b) + c) and `(value + 1)` in `((value + 1) > 2)` must round-trip.
func collapseParens(e pyast.Expr) pyast.Expr {
	for {
		switch x := e.(type) {
		case *pyast.ParenExpr:
			e = x.X
		case *pyast.TernaryExpr, *pyast.UnaryExpr:
			return e
		default:
			return e
		}
	}
}

// parseCompClauses 解析推导子句序列 `for … [if …] [for … [if …]] …`
// （list comp 与括号生成器表达式共用同一段）。
//
// **iterable 必须用 parseOr 而不是 parseExpr**（一手，2026-09-28）：Python
// 语法里 for 后面的 iterable 是 `or_test`，不是完整 expression —— 用 parseExpr
// 会把尾随的 `if` 当成三元运算符吞掉，于是
//
//	`[str(x) for g in (xs or []) if isinstance(g, list) for x in g]`（pygen 的 flatten）
//
// 在 `(xs or []) if …` 处报 `expected keyword "else", got "for"`。
func (p *shimParser) parseCompClauses() ([]pyast.CompClause, error) {
	var out []pyast.CompClause
	for p.cur().kind == stKeyword && p.cur().text == "for" {
		p.next() // consume "for"
		clause := pyast.CompClause{}
		// 目标变量:单 ident 或逗号分隔多 ident（tuple-unpack）
		for {
			id, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			clause.Vars = append(clause.Vars, id.text)
			if t := p.cur(); t.kind == stOp && t.text == "," {
				p.next()
				continue
			}
			break
		}
		if _, err := p.expectKeyword("in"); err != nil {
			return nil, err
		}
		iter, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		clause.Iter = iter
		for p.cur().kind == stKeyword && p.cur().text == "if" {
			p.next()
			f, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			clause.Ifs = append(clause.Ifs, f)
		}
		out = append(out, clause)
	}
	return out, nil
}

func (p *shimParser) parseList() (pyast.Expr, error) {
	p.next() // consume "["
	list := &pyast.ListExpr{}
	for p.cur().kind != stOp || p.cur().text != "]" {
		el, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		list.Elts = append(list.Elts, el)
		if t := p.cur(); t.kind == stOp && t.text == "," {
			p.next()
			continue
		}
		break
	}
	// 列表推导（生成器发射的 enumerate 形态）:
	//   [{...} for i, v in enumerate(arr)]  /  [expr for x in xs]
	// `for` 出现在闭括号之前,必须先判推导再 expect "]"。解析为
	// ListCompExpr（generator 只发射这两种形态;带 if 过滤的推导——
	// flatten 的 isinstance——走 foreign code,不落 shim）。
	if p.cur().kind == stKeyword && p.cur().text == "for" {
		lc := &pyast.ListCompExpr{Elt: list.Elts[0]}
		clauses, err := p.parseCompClauses()
		if err != nil {
			return nil, err
		}
		lc.Clauses = clauses
		if _, err := p.expectOp("]"); err != nil {
			return nil, err
		}
		return lc, nil
	}
	if _, err := p.expectOp("]"); err != nil {
		return nil, err
	}
	return list, nil
}

func (p *shimParser) parseDict() (pyast.Expr, error) {
	p.next() // consume "{"
	dict := &pyast.DictExpr{}
	for p.cur().kind != stOp || p.cur().text != "}" {
		key, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expectOp(":"); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		dict.Items = append(dict.Items, pyast.DictItem{Key: key, Value: val})
		if t := p.cur(); t.kind == stOp && t.text == "," {
			p.next()
			continue
		}
		break
	}
	if _, err := p.expectOp("}"); err != nil {
		return nil, err
	}
	return dict, nil
}

// ParseShimRoundTrip re-parses the serialized form (test helper contract:
// Parse(Serialize(Parse(x))) is stable).
func ParseShimRoundTrip(src string) (string, error) {
	m, err := ParseShim(src)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(pyast.Serialize(m), "\n"), nil
}
