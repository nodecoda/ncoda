package pyast

// parser.go — 反向子集递归下降 parser（2026-09-02，P3）。把编译器产出的
// python 子集模块字节还原为 pyast AST（复用前向同一 AST 源，避免双 AST
// 漂移）。文法 = docs/ideas-todo/SHIM-REVERSIBLE-DSL.md §2。互逆不变量：
// Serialize(Parse(S)) == S（字节级）。子集外语法一律报错（反向判定失败
// → 透传，封闭契约 §1.1）。

import "fmt"

// ParseModule 解析一个完整的子集模块（def main(params) -> dict: + body）。
func ParseModule(src string) (*Module, error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	m, err := p.parseModule()
	if err != nil {
		return nil, err
	}
	if p.cur().Type != TEOF {
		return nil, p.errf("trailing tokens after module")
	}
	return m, nil
}

// ParseExpr 解析单个子集表达式（不含模块头）。
func ParseExpr(src string) (Expr, error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.cur().Type != TEOF {
		return nil, p.errf("trailing tokens after expression")
	}
	return e, nil
}

type parser struct {
	toks []Token
	pos  int
}

func (p *parser) cur() Token { return p.toks[p.pos] }
func (p *parser) peek(n int) Token {
	if p.pos+n < len(p.toks) {
		return p.toks[p.pos+n]
	}
	return p.toks[len(p.toks)-1]
}
func (p *parser) advance() Token {
	t := p.toks[p.pos]
	if t.Type != TEOF {
		p.pos++
	}
	return t
}
func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("pyast: %s (offset %d)", fmt.Sprintf(format, args...), p.cur().Pos)
}
func (p *parser) isName(s string) bool {
	t := p.cur()
	return t.Type == TName && t.Text == s
}
func (p *parser) isPunct(s string) bool {
	t := p.cur()
	return t.Type == TPunct && t.Text == s
}
func (p *parser) isOp(s string) bool {
	t := p.cur()
	return t.Type == TOp && t.Text == s
}

// ── Module ───────────────────────────────────────────────────────────

func (p *parser) parseModule() (*Module, error) {
	m := &Module{}
	// 可选 import 行（模块级，位于 def 之前）
	for p.isName("import") {
		p.advance()
		name, err := p.expectNameStr()
		if err != nil {
			return nil, err
		}
		m.Imports = append(m.Imports, name)
		if err := p.skipNewlines(); err != nil {
			return nil, err
		}
	}
	// def main(params) -> dict:
	if err := p.expectKeyword("def"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("main"); err != nil {
		return nil, err
	}
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	params, err := p.parseParams()
	if err != nil {
		return nil, err
	}
	m.Params = params
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	if err := p.expectOp("->"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("dict"); err != nil {
		return nil, err
	}
	if err := p.expectPunct(":"); err != nil {
		return nil, err
	}
	if err := p.skipNewlines(); err != nil {
		return nil, err
	}
	// body：一个或多个语句（每行一句，缩进由行首空白表达、lexer 已吞）
	for p.cur().Type != TEOF {
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		if st != nil {
			m.Body = append(m.Body, st)
		}
		if err := p.skipNewlines(); err != nil {
			return nil, err
		}
	}
	if len(m.Body) == 0 {
		return nil, p.errf("module body is empty")
	}
	return m, nil
}

func (p *parser) parseParams() ([]Param, error) {
	var params []Param
	if p.isPunct(")") {
		return params, nil
	}
	for {
		name, err := p.expectNameStr()
		if err != nil {
			return nil, err
		}
		param := Param{Name: name}
		if p.isPunct(":") {
			p.advance()
			typ, err := p.expectNameStr()
			if err != nil {
				return nil, err
			}
			param.Type = typ
		}
		params = append(params, param)
		if p.isPunct(",") {
			p.advance()
			continue
		}
		break
	}
	return params, nil
}

func (p *parser) parseStmt() (Stmt, error) {
	switch {
	case p.isName("import"):
		return nil, p.errf("function-body import is outside the reversible subset")
	case p.isName("return"):
		p.advance()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ReturnStmt{Value: e}, nil
	case p.isName("pass"):
		p.advance()
		return &PassStmt{}, nil
	case p.isName("if"):
		p.advance()
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(":"); err != nil {
			return nil, err
		}
		if err := p.skipNewlines(); err != nil {
			return nil, err
		}
		then, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		st := &IfStmt{Cond: cond, Then: then}
		// else 分支：同一行 "else:"（子集产物不产出，宽松支持）
		if p.isName("else") {
			p.advance()
			if err := p.expectPunct(":"); err != nil {
				return nil, err
			}
			if err := p.skipNewlines(); err != nil {
				return nil, err
			}
			els, err := p.parseStmt()
			if err != nil {
				return nil, err
			}
			st.Else = els
		}
		return st, nil
	case p.isName("for"):
		p.advance()
		var vars []string
		for {
			n, err := p.expectNameStr()
			if err != nil {
				return nil, err
			}
			vars = append(vars, n)
			if p.isPunct(",") {
				p.advance()
				continue
			}
			break
		}
		if err := p.expectKeyword("in"); err != nil {
			return nil, err
		}
		iter, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(":"); err != nil {
			return nil, err
		}
		if err := p.skipNewlines(); err != nil {
			return nil, err
		}
		body, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		return &ForStmt{Var: vars[0], Iter: iter, Body: body}, nil
	}
	// assign：NAME = expr
	name, err := p.expectNameStr()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct("="); err != nil {
		return nil, err
	}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &AssignStmt{Name: name, Value: e}, nil
}

// ── 表达式（§2.2 文法，优先级从低到高）────────────────────────────────

func (p *parser) parseExpr() (Expr, error) { return p.parseTernary() }

func (p *parser) parseTernary() (Expr, error) {
	cond, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.isName("if") {
		p.advance()
		c, err := p.parseOr() // Python: cond 是 or_test
		if err != nil {
			return nil, err
		}
		if err := p.expectKeyword("else"); err != nil {
			return nil, err
		}
		els, err := p.parseTernary() // else 分支可嵌套三元
		if err != nil {
			return nil, err
		}
		return &TernaryExpr{Cond: c, Then: cond, Else: els}, nil
	}
	return cond, nil
}

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isName("or") {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isName("and") {
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "and", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.isName("not") {
		p.advance()
		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "not", Operand: operand}, nil
	}
	return p.parseComparison()
}

func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseArith()
	if err != nil {
		return nil, err
	}
	for {
		var op string
		switch {
		case p.isOp("=="):
			op = "=="
		case p.isOp("!="):
			op = "!="
		case p.isOp("<"):
			op = "<"
		case p.isOp("<="):
			op = "<="
		case p.isOp(">"):
			op = ">"
		case p.isOp(">="):
			op = ">="
		case p.isName("in"):
			op = "in"
		case p.isName("not") && p.peek(1).Type == TName && p.peek(1).Text == "in":
			op = "not in"
		case p.isName("is"):
			// `is` / `is not`（编译器 ?? shim 的 None 恒等判定；Python 关键字）。
			op = "is"
			if p.peek(1).Type == TName && p.peek(1).Text == "not" {
				op = "is not"
			}
		default:
			return left, nil
		}
		p.advance() // 吃比较符（not in / is not 时吃两个）
		if op == "not in" || op == "is not" {
			p.advance()
		}
		right, err := p.parseArith()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
}

func (p *parser) parseArith() (Expr, error) {
	first, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	type part struct {
		op   string
		expr Expr
	}
	var parts []part
	cur := first
	for p.isOp("+") || p.isOp("-") {
		op := p.advance().Text
		term, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		parts = append(parts, part{op: op, expr: term})
	}
	if len(parts) == 0 {
		return cur, nil
	}
	// `+` 链（≥2 个 +）→ flat ConcatExpr（模板串 lowering，无括号字节）；
	// 单个 + 或无括号 - → BinaryExpr 左结合（括号形态由 ParenExpr 保留）。
	allPlus := true
	for _, pt := range parts {
		if pt.op != "+" {
			allPlus = false
			break
		}
	}
	if allPlus && len(parts) >= 2 {
		elts := []Expr{cur}
		for _, pt := range parts {
			elts = append(elts, pt.expr)
		}
		return &ConcatExpr{Parts: elts}, nil
	}
	left := cur
	for _, pt := range parts {
		left = &BinaryExpr{Op: pt.op, Left: left, Right: pt.expr}
	}
	return left, nil
}

func (p *parser) parseTerm() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isOp("*") || p.isOp("/") || p.isOp("//") || p.isOp("%") {
		op := p.advance().Text
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.isOp("-") {
		p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "-", Operand: operand}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (Expr, error) {
	e, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isPunct("("):
			p.advance()
			args, kwargs, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
			// 方法调用 vs 普通调用：当前 expr 作为 Obj 时 Func=方法名；
			// 但 plain call（str(...)）的 Func 来自 NameExpr。
			if name, ok := e.(*NameExpr); ok {
				e = &CallExpr{Func: name.Name, Args: args, Kwargs: kwargs}
			} else {
				e = &CallExpr{Obj: e, Args: args, Kwargs: kwargs}
			}
		case p.isPunct("["):
			p.advance()
			idx, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct("]"); err != nil {
				return nil, err
			}
			e = &SubscriptExpr{Obj: e, Index: idx}
		case p.isPunct("."):
			p.advance()
			attr, err := p.expectNameStr()
			if err != nil {
				return nil, err
			}
			if p.isPunct("(") {
				p.advance()
				args, kwargs, err := p.parseArgs()
				if err != nil {
					return nil, err
				}
				if err := p.expectPunct(")"); err != nil {
					return nil, err
				}
				e = &CallExpr{Func: attr, Obj: e, Args: args, Kwargs: kwargs}
			} else {
				e = &AttributeExpr{Obj: e, Attr: attr}
			}
		default:
			return e, nil
		}
	}
}

func (p *parser) parseArgs() ([]Expr, []CallKwarg, error) {
	var args []Expr
	var kwargs []CallKwarg
	if p.isPunct(")") {
		return args, kwargs, nil
	}
	for {
		e, err := p.parseExpr()
		if err != nil {
			return nil, nil, err
		}
		if p.isPunct("=") {
			p.advance()
			val, err := p.parseExpr()
			if err != nil {
				return nil, nil, err
			}
			name, ok := e.(*NameExpr)
			if !ok {
				return nil, nil, p.errf("kwarg name must be a NAME")
			}
			kwargs = append(kwargs, CallKwarg{Name: name.Name, Value: val})
		} else {
			args = append(args, e)
		}
		if p.isPunct(",") {
			p.advance()
			continue
		}
		break
	}
	return args, kwargs, nil
}

func (p *parser) parseAtom() (Expr, error) {
	t := p.cur()
	switch {
	case t.Type == TNumber:
		p.advance()
		if t.IsFloat {
			return &FloatExpr{Value: t.Float}, nil
		}
		return &IntExpr{Value: t.Int}, nil
	case t.Type == TString:
		p.advance()
		return &StringExpr{Value: t.Str}, nil
	case p.isName("True"):
		p.advance()
		return &BoolExpr{Value: true}, nil
	case p.isName("False"):
		p.advance()
		return &BoolExpr{Value: false}, nil
	case p.isName("None"):
		p.advance()
		return &NoneExpr{}, nil
	case t.Type == TName:
		p.advance()
		return &NameExpr{Name: t.Text}, nil
	case p.isPunct("("):
		p.advance()
		// generator：括号内表达式后跟 `for`
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.isName("for") {
			clauses, err := p.parseCompClauses()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
			return &GeneratorExpr{Elt: e, Clauses: clauses}, nil
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		// 三元表达式序列化自带括号（`(A if C else B)`）——该括号不是
		// ParenExpr，parse 时不得再包一层（否则字节回环破坏）。
		if _, ok := e.(*TernaryExpr); ok {
			return e, nil
		}
		return &ParenExpr{X: e}, nil
	case p.isPunct("["):
		p.advance()
		if p.isPunct("]") {
			p.advance()
			return &ListExpr{}, nil
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.isName("for") {
			clauses, err := p.parseCompClauses()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct("]"); err != nil {
				return nil, err
			}
			return &ListCompExpr{Elt: e, Clauses: clauses}, nil
		}
		if p.isPunct(",") {
			// 多元素列表
			elts := []Expr{e}
			for p.isPunct(",") {
				p.advance()
				if p.isPunct("]") {
					break
				}
				el, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				elts = append(elts, el)
			}
			if err := p.expectPunct("]"); err != nil {
				return nil, err
			}
			return &ListExpr{Elts: elts}, nil
		}
		if err := p.expectPunct("]"); err != nil {
			return nil, err
		}
		return &ListExpr{Elts: []Expr{e}}, nil
	case p.isPunct("{"):
		p.advance()
		if p.isPunct("}") {
			p.advance()
			return &DictExpr{}, nil
		}
		key, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(":"); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.isName("for") {
			clauses, err := p.parseCompClauses()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct("}"); err != nil {
				return nil, err
			}
			return &DictCompExpr{Key: key, Value: val, Clauses: clauses}, nil
		}
		items := []DictItem{{Key: key, Value: val}}
		for p.isPunct(",") {
			p.advance()
			if p.isPunct("}") {
				break
			}
			k, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct(":"); err != nil {
				return nil, err
			}
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			items = append(items, DictItem{Key: k, Value: v})
		}
		if err := p.expectPunct("}"); err != nil {
			return nil, err
		}
		return &DictExpr{Items: items}, nil
	}
	return nil, p.errf("unexpected token %q", t.Text)
}

// parseCompClauses 解析 `for target in iter (if cond)*` 的重复序列。
func (p *parser) parseCompClauses() ([]CompClause, error) {
	var clauses []CompClause
	for p.isName("for") {
		p.advance()
		var vars []string
		for {
			n, err := p.expectNameStr()
			if err != nil {
				return nil, err
			}
			vars = append(vars, n)
			if p.isPunct(",") {
				p.advance()
				continue
			}
			break
		}
		if err := p.expectKeyword("in"); err != nil {
			return nil, err
		}
		// comp iter / if-cond 是 or_test（Python 文法）：不能用 parseExpr
		// （ternary），否则推导式的 `if` 会被三元解析器吞掉。
		iter, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		c := CompClause{Vars: vars, Iter: iter}
		for p.isName("if") {
			p.advance()
			cond, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			c.Ifs = append(c.Ifs, cond)
		}
		clauses = append(clauses, c)
	}
	if len(clauses) == 0 {
		return nil, p.errf("expected `for` in comprehension")
	}
	return clauses, nil
}

// ── 辅助 ─────────────────────────────────────────────────────────────

func (p *parser) skipNewlines() error {
	for p.cur().Type == TNewline {
		p.advance()
	}
	return nil
}

func (p *parser) expectNameStr() (string, error) {
	t := p.cur()
	if t.Type != TName {
		return "", p.errf("expected NAME, got %q", t.Text)
	}
	p.advance()
	return t.Text, nil
}

func (p *parser) expectKeyword(name string) error {
	if !p.isName(name) {
		return p.errf("expected %q, got %q", name, p.cur().Text)
	}
	p.advance()
	return nil
}

func (p *parser) expectPunct(s string) error {
	if !p.isPunct(s) {
		return p.errf("expected %q, got %q", s, p.cur().Text)
	}
	p.advance()
	return nil
}

func (p *parser) expectOp(s string) error {
	if !p.isOp(s) {
		return p.errf("expected %q, got %q", s, p.cur().Text)
	}
	p.advance()
	return nil
}
