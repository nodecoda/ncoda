// Canonical expression production parser (Task 5), ported from
// src/nclang/lang/parser.py's expression section. Precedence chain:
// ternary → or → and → equality → comparison → addition → multiplication →
// unary → postfix → primary, plus map/array literals, template strings with
// ${expr} interpolation, lambdas, for/parallel expressions, operation
// policies (with retry/timeout/default), and foreign-code expressions.
//
// Span rules mirror parser.py exactly: only the || / && BinaryExpr layers
// and TernaryExpr carry source spans; arithmetic/comparison/equality
// BinaryExpr nodes keep the zero span (Python: span=None). UnaryExpr copies
// its operand's span; LambdaExpr falls back to its '(' when the body has no
// span. parseExpression is the single entry point (parser.py:
// _parse_expression).
package parser

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/token"
)

// parseExpression is the expression entry point (parser.py:_parse_expression).
// It is also the single recursive entry for every expression form (parens,
// calls, unary chains, ternaries, template interpolation), so the P4-4
// depth guard here covers all expression nesting in one place.
func (p *Parser) parseExpression() (ast.Expr, *ParseError) {
	if p.exprDepth >= maxExprDepth {
		return nil, p.recursionLimitError()
	}
	p.exprDepth++
	defer func() { p.exprDepth-- }()
	return p.parseTernary()
}

// maxExprDepth aligns with the reference adapter.s recursion rejection point
// (Python rejects ~89 nested parens; 88 is the deepest accepted depth).
const maxExprDepth = 89

// exprStart mirrors Python's "expr.span.start if expr.span else
// previous().location" span fallback for binary/ternary nodes.
func (p *Parser) exprStart(e ast.Expr) token.Location {
	if s := e.Span(); s.Start.Line != 0 || s.Start.Column != 0 {
		return s.Start
	}
	return p.previous().Location
}

// parseTernary parses "<or> ? <expr> : <expr>" (parser.py:_parse_ternary).
// cond 层自 route 1a 起经 parseNullCoalesce（?? 高于三目低于比较/逻辑）。
func (p *Parser) parseTernary() (ast.Expr, *ParseError) {
	expr, err := p.parseNullCoalesce()
	if err != nil {
		return nil, err
	}
	if _, ok := p.match(token.QUESTION); ok {
		thenVal, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.COLON, "Expected ':' in ternary expression"); err != nil {
			return nil, err
		}
		elseVal, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		t := ast.NewTernaryExpr(expr, thenVal, elseVal)
		t.SetSpan(ast.Span{Start: p.exprStart(expr), End: p.previous().Location})
		return t, nil
	}
	return expr, nil
}

// parseNullCoalesce parses "??" chains (route 1a, first-not-null 值选择)。
// 优先级：低于比较与 ||/&&（操作数经 parseOr 解析）、高于三目（被
// parseTernary 调用）。JS 式混用限制：?? 与 ||/&& 在同一表达式无括号
// 混用 = 语法错误（`a || b ?? c` / `a ?? b && c`）；括号分组
// `(a || b) ?? c` 合法（grouped 标记区分）。
func (p *Parser) parseNullCoalesce() (ast.Expr, *ParseError) {
	left, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	for {
		if _, ok := p.match(token.NULL_COALESCE); !ok {
			break
		}
		right, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.bareLogicalMixed(left) {
			return nil, p.coalesceMixError(left)
		}
		if p.bareLogicalMixed(right) {
			return nil, p.coalesceMixError(right)
		}
		b := ast.NewBinaryExpr("??", left, right)
		b.SetSpan(ast.Span{Start: p.exprStart(left), End: p.previous().Location})
		left = b
	}
	return left, nil
}

// bareLogicalMixed 报告表达式是否为"裸" ||/&& BinaryExpr（非括号分组）——
// 是则构成与 ?? 的无括号混用（JS 式语法错误）。
func (p *Parser) bareLogicalMixed(e ast.Expr) bool {
	if p.fromGrouped(e) {
		return false
	}
	b, ok := e.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	return b.Op() == "||" || b.Op() == "&&"
}

// coalesceMixError 报 ?? 与 ||/&& 无括号混用的语法错误（JS 式）。
// 错误在源头 append（parseBlock 只 synchronize 不收集，须与
// error()/codedError() 同构）。
func (p *Parser) coalesceMixError(e ast.Expr) *ParseError {
	tok := p.peek()
	err := &ParseError{
		Message: "'??' cannot be mixed with '||'/'&&' without parentheses",
		Span:    token.Span{Start: tok.Location, End: tok.Location},
	}
	p.errors = append(p.errors, err)
	return err
}

// parseOr parses "||" chains (parser.py:_parse_or).
func (p *Parser) parseOr() (ast.Expr, *ParseError) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		if _, ok := p.match(token.OR); !ok {
			break
		}
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		b := ast.NewBinaryExpr("||", left, right)
		b.SetSpan(ast.Span{Start: p.exprStart(left), End: p.previous().Location})
		left = b
	}
	return left, nil
}

// parseAnd parses "&&" chains (parser.py:_parse_and).
func (p *Parser) parseAnd() (ast.Expr, *ParseError) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for {
		if _, ok := p.match(token.AND); !ok {
			break
		}
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		b := ast.NewBinaryExpr("&&", left, right)
		b.SetSpan(ast.Span{Start: p.exprStart(left), End: p.previous().Location})
		left = b
	}
	return left, nil
}

// parseEquality parses "==" / "!=" chains (parser.py:_parse_equality).
func (p *Parser) parseEquality() (ast.Expr, *ParseError) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for {
		op := ""

		if _, ok := p.match(token.EQUAL); ok {
			op = "=="
		} else if _, ok := p.match(token.NOT_EQUAL); ok {
			op = "!="
		} else {
			return left, nil
		}
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = p.binary(op, left, right)
	}
}

// parseComparison parses "<" / "<=" / ">" / ">=" / "in" chains
// (parser.py:_parse_comparison).
func (p *Parser) parseComparison() (ast.Expr, *ParseError) {
	left, err := p.parseAddition()
	if err != nil {
		return nil, err
	}
	for {
		op := ""

		if _, ok := p.match(token.LESS); ok {
			op = "<"
		} else if _, ok := p.match(token.LESS_EQUAL); ok {
			op = "<="
		} else if _, ok := p.match(token.GREATER); ok {
			op = ">"
		} else if _, ok := p.match(token.GREATER_EQUAL); ok {
			op = ">="
		} else if _, ok := p.match(token.KW_IN); ok {
			op = "in"
		} else {
			return left, nil
		}
		right, err := p.parseAddition()
		if err != nil {
			return nil, err
		}
		left = p.binary(op, left, right)
	}
}

// parseAddition parses "+" / "-" chains (parser.py:_parse_addition).
func (p *Parser) parseAddition() (ast.Expr, *ParseError) {
	left, err := p.parseMultiplication()
	if err != nil {
		return nil, err
	}
	for {
		op := ""

		if _, ok := p.match(token.PLUS); ok {
			op = "+"
		} else if _, ok := p.match(token.MINUS); ok {
			op = "-"
		} else {
			return left, nil
		}
		right, err := p.parseMultiplication()
		if err != nil {
			return nil, err
		}
		left = p.binary(op, left, right)
	}
}

// parseMultiplication parses "*" / "/" / "%" chains
// (parser.py:_parse_multiplication).
func (p *Parser) parseMultiplication() (ast.Expr, *ParseError) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		op := ""

		if _, ok := p.match(token.STAR); ok {
			op = "*"
		} else if _, ok := p.match(token.SLASH); ok {
			op = "/"
		} else if _, ok := p.match(token.PERCENT); ok {
			op = "%"
		} else {
			return left, nil
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = p.binary(op, left, right)
	}
}

// binary builds a BinaryExpr node for the equality/comparison/addition/
// multiplication layers. Python leaves these spans None — only the || / &&
// layers and TernaryExpr set spans (see parseOr/parseAnd/parseTernary).
func (p *Parser) binary(op string, left, right ast.Expr) ast.Expr {
	return ast.NewBinaryExpr(op, left, right)
}

// parseUnary parses "!" / "-" prefixes (parser.py:_parse_unary). The unary
// node's span mirrors the operand's span, as in Python.
func (p *Parser) parseUnary() (ast.Expr, *ParseError) {
	if _, ok := p.match(token.NOT); ok {
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		u := ast.NewUnaryExpr("!", operand)
		u.SetSpan(ast.Span{Start: operand.Span().Start, End: operand.Span().End})
		return u, nil
	}
	if _, ok := p.match(token.MINUS); ok {
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		u := ast.NewUnaryExpr("-", operand)
		u.SetSpan(ast.Span{Start: operand.Span().Start, End: operand.Span().End})
		return u, nil
	}
	return p.parsePostfix()
}

// parsePostfix parses field access, index access, calls, and the "with
// <policy>" suffix (parser.py:_parse_postfix).
func (p *Parser) parsePostfix() (ast.Expr, *ParseError) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		if _, ok := p.match(token.DOT); ok {
			var fieldTok token.Token
			// 关键字字段名合法：coze canvas 输出口/字段就叫 "output"（KW_OUTPUT），
			// "type"（KW_TYPE）同理。'.' 后是成员名位置，关键字无歧义
			// （点号后不可能继续表达式关键字前缀），与 yield 命名口一致。
			if p.check(token.IDENTIFIER) || token.IsKeyword(p.peek().Lexeme) {
				fieldTok = p.advance()
			} else if fieldTok, err = p.consume(token.IDENTIFIER, "Expected field name after '.'"); err != nil {
				return nil, err
			}
			f := ast.NewFieldAccessExpr(expr, fieldTok.Lexeme)
			f.SetSpan(ast.Span{Start: p.exprStart(expr), End: fieldTok.Location})
			expr = f
		} else if _, ok := p.match(token.LBRACKET); ok {
			index, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if _, err := p.consume(token.RBRACKET, "Expected ']'"); err != nil {
				return nil, err
			}
			ix := ast.NewIndexExpr(expr, index)
			ix.SetSpan(ast.Span{Start: p.exprStart(expr), End: p.previous().Location})
			expr = ix
		} else if isCallable(expr) {
			typeArgs, ok := p.parseTryCallTypeArgs()
			if !ok && !p.check(token.LPAREN) {
				break
			}
			p.advance() // consume '('
			args, err := p.parseArgumentList()
			if err != nil {
				return nil, err
			}
			if _, err := p.consume(token.RPAREN, "Expected ')' after arguments"); err != nil {
				return nil, err
			}
			c := ast.NewCallExpr(expr, typeArgs, args)
			c.SetSpan(ast.Span{Start: p.exprStart(expr), End: p.previous().Location})
			expr = c
		} else {
			break
		}
	}
	if _, ok := p.match(token.KW_WITH); ok {
		policies, err := p.parseOperationPolicies()
		if err != nil {
			return nil, err
		}
		wrapped := ast.NewOperationPolicyExpr(expr, policies)
		wrapped.SetSpan(ast.Span{Start: p.exprStart(expr), End: p.previous().Location})
		return wrapped, nil
	}
	return expr, nil
}

// isCallable reports whether the expression can be a call target (Python:
// isinstance(expr, (IdentExpr, FieldAccessExpr))).
func isCallable(e ast.Expr) bool {
	switch e.(type) {
	case *ast.IdentExpr, *ast.FieldAccessExpr:
		return true
	}
	return false
}

// parseTryCallTypeArgs speculatively parses "<T, ...>" call type arguments,
// restoring cursor and diagnostics when the following tokens do not form a
// call with type arguments (parser.py:_try_parse_call_type_args).
func (p *Parser) parseTryCallTypeArgs() ([]ast.TypeRef, bool) {
	if !p.check(token.LESS) {
		return nil, false
	}
	saved := p.current
	errorCount := len(p.errors)
	p.advance()
	var typeArgs []ast.TypeRef
	first, err := p.parseTypeRef()
	if err != nil {
		p.current = saved
		p.errors = p.errors[:errorCount]
		return nil, false
	}
	typeArgs = append(typeArgs, first)
	for {
		if _, ok := p.match(token.COMMA); !ok {
			break
		}
		next, err := p.parseTypeRef()
		if err != nil {
			p.current = saved
			p.errors = p.errors[:errorCount]
			return nil, false
		}
		typeArgs = append(typeArgs, next)
	}
	if _, err := p.consume(token.GREATER, "Expected '>' after call type arguments"); err != nil {
		p.current = saved
		p.errors = p.errors[:errorCount]
		return nil, false
	}
	if !p.check(token.LPAREN) {
		p.current = saved
		p.errors = p.errors[:errorCount]
		return nil, false
	}
	return typeArgs, true
}

// parseOperationPolicies parses the "with <policy>, <policy>..." suffix
// (parser.py:_parse_postfix policy loop).
func (p *Parser) parseOperationPolicies() ([]ast.OperationPolicy, *ParseError) {
	first, err := p.parseOperationPolicy()
	if err != nil {
		return nil, err
	}
	policies := []ast.OperationPolicy{first}
	for {
		if _, ok := p.match(token.COMMA); !ok {
			break
		}
		next, err := p.parseOperationPolicy()
		if err != nil {
			return nil, err
		}
		policies = append(policies, next)
	}
	return policies, nil
}

// parseOperationPolicy parses one retry/timeout/default policy
// (parser.py:_parse_operation_policy).
func (p *Parser) parseOperationPolicy() (ast.OperationPolicy, *ParseError) {
	start := p.peek()
	var policy ast.OperationPolicy
	if _, ok := p.match(token.KW_RETRY); ok {
		if _, err := p.consume(token.LPAREN, "Expected '(' after retry"); err != nil {
			return nil, err
		}
		maxKey, err := p.consume(token.IDENTIFIER, "Expected retry max")
		if err != nil {
			return nil, err
		}
		if maxKey.Lexeme != "max" {
			return nil, p.error("Expected retry 'max' option")
		}
		if _, err := p.consume(token.COLON, "Expected ':' after retry max"); err != nil {
			return nil, err
		}
		retries, err := p.consume(token.INT_LITERAL, "Expected integer retry max")
		if err != nil {
			return nil, err
		}
		intervalMs := 1000
		// intervalExplicit 记录「源里写了 interval:」这件事（provenance，不是取值）：
		// 省略 ⇒ 语言默认 1000ms（与 `interval: 1s` 同产物），但两者在「目标平台
		// 有没有间隔载体」这条报账上必须可分辨（residuals R-90 / F3 值轴 §③）。
		intervalExplicit := false
		if _, ok := p.match(token.COMMA); ok {
			intervalKey, err := p.consume(token.IDENTIFIER, "Expected retry interval")
			if err != nil {
				return nil, err
			}
			if intervalKey.Lexeme != "interval" {
				return nil, p.error("Expected retry 'interval' option")
			}
			if _, err := p.consume(token.COLON, "Expected ':' after retry interval"); err != nil {
				return nil, err
			}
			intervalMs, err = p.parseDurationMs()
			if err != nil {
				return nil, err
			}
			intervalExplicit = true
		}
		if _, err := p.consume(token.RPAREN, "Expected ')' after retry policy"); err != nil {
			return nil, err
		}
		maxRetries := 0
		if retries.Literal.Valid && retries.Literal.Kind == token.LitInt {
			maxRetries = int(retries.Literal.I)
		}
		rp := ast.NewRetryPolicy(maxRetries, intervalMs)
		rp.SetIntervalExplicit(intervalExplicit)
		policy = rp
	} else if _, ok := p.match(token.KW_TIMEOUT); ok {
		if _, err := p.consume(token.LPAREN, "Expected '(' after timeout"); err != nil {
			return nil, err
		}
		timeoutMs, err := p.parseDurationMs()
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.RPAREN, "Expected ')' after timeout policy"); err != nil {
			return nil, err
		}
		policy = ast.NewTimeoutPolicy(timeoutMs)
	} else if _, ok := p.match(token.KW_DEFAULT); ok {
		if _, err := p.consume(token.LPAREN, "Expected '(' after default"); err != nil {
			return nil, err
		}
		value, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.RPAREN, "Expected ')' after default policy"); err != nil {
			return nil, err
		}
		policy = ast.NewDefaultPolicy(value)
	} else {
		return nil, p.error("Expected retry, timeout, or default policy")
	}
	policy.SetSpan(p.spanFrom(start))
	return policy, nil
}

// parseDurationMs parses "<int> <ms|s>" (parser.py:_parse_duration_ms).
func (p *Parser) parseDurationMs() (int, *ParseError) {
	amount, err := p.consume(token.INT_LITERAL, "Expected duration amount")
	if err != nil {
		return 0, err
	}
	unit, err := p.consume(token.IDENTIFIER, "Expected duration unit 'ms' or 's'")
	if err != nil {
		return 0, err
	}
	if unit.Lexeme != "ms" && unit.Lexeme != "s" {
		return 0, p.error("Duration unit must be 'ms' or 's'")
	}
	multiplier := 1000
	if unit.Lexeme == "ms" {
		multiplier = 1
	}
	if !amount.Literal.Valid || amount.Literal.Kind != token.LitInt {
		return 0, nil
	}
	return int(amount.Literal.I) * multiplier, nil
}

// parseArgumentList parses the comma-separated call arguments
// (parser.py:_parse_argument_list).
func (p *Parser) parseArgumentList() ([]ast.Expr, *ParseError) {
	var args []ast.Expr
	if !p.check(token.RPAREN) {
		first, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		args = append(args, first)
		for {
			if _, ok := p.match(token.COMMA); !ok {
				break
			}
			next, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			args = append(args, next)
		}
	}
	return args, nil
}

// looksLikeLambda reports whether the tokens at the cursor start a lambda
// parameter list "(<ident>[, ...]) ->" (parser.py:_looks_like_lambda). Pure
// lookahead over the token slice; does not consume or record errors.
func (p *Parser) looksLikeLambda() bool {
	if !p.check(token.LPAREN) {
		return false
	}
	// Clamp index like Python's list indexing (EOF token always present).
	idx := p.current + 1
	if idx >= len(p.tokens) {
		idx = len(p.tokens) - 1
	}
	if p.tokens[idx].Type == token.RPAREN {
		n := idx + 1
		if n >= len(p.tokens) {
			n = len(p.tokens) - 1
		}
		return p.tokens[n].Type == token.ARROW
	}
	for p.tokens[idx].Type == token.IDENTIFIER {
		idx++
		if idx >= len(p.tokens) {
			idx = len(p.tokens) - 1
		}
		if p.tokens[idx].Type == token.RPAREN {
			n := idx + 1
			if n >= len(p.tokens) {
				n = len(p.tokens) - 1
			}
			return p.tokens[n].Type == token.ARROW
		}
		if p.tokens[idx].Type != token.COMMA {
			return false
		}
		idx++
		if idx >= len(p.tokens) {
			idx = len(p.tokens) - 1
		}
	}
	return false
}

// parseLambdaExpr parses "(<param>, ...) -> <body>" (parser.py:
// _parse_lambda_expr). Interim port for statement-position lambdas (e.g.
// filter callbacks); Task 5 formalizes the production and its test suite.
func (p *Parser) parseLambdaExpr() (*ast.LambdaExpr, *ParseError) {
	start, err := p.consume(token.LPAREN, "Expected '(' before lambda parameters")
	if err != nil {
		return nil, err
	}
	var params []string
	if !p.check(token.RPAREN) {
		first, err := p.consume(token.IDENTIFIER, "Expected lambda parameter")
		if err != nil {
			return nil, err
		}
		params = append(params, first.Lexeme)
		for {
			if _, ok := p.match(token.COMMA); !ok {
				break
			}
			next, err := p.consume(token.IDENTIFIER, "Expected lambda parameter")
			if err != nil {
				return nil, err
			}
			params = append(params, next.Lexeme)
		}
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after lambda parameters"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.ARROW, "Expected '->' after lambda parameters"); err != nil {
		return nil, err
	}
	body, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	// Python: SourceSpan(start.location, body.span.end if body.span else
	// start.location) — an arithmetic body has no span, so both ends are the
	// lambda's '('.
	end := start.Location
	if s := body.Span(); s.Start.Line != 0 || s.Start.Column != 0 {
		end = s.End
	}
	expr := ast.NewLambdaExpr(params, body)
	expr.SetSpan(ast.Span{Start: start.Location, End: end})
	return expr, nil
}

// parsePrimary parses the expression primaries (parser.py:_parse_primary).
func (p *Parser) parsePrimary() (ast.Expr, *ParseError) {
	tok := p.peek()

	if p.check(token.KW_FOREIGN) {
		return p.parseForeignCodeExpr()
	}

	// `template "<逐字文本>" [using {…}]` 作为**表达式**（R-68 甲，用户 2026-09-25
	// 批准）：出现在值位 —— 今天的落点是 llm message content（平台 prompt 槽只有
	// 一个字符串，W2 §1.1）。与语句位**同一条前瞻规则**（`template` 紧跟字符串
	// 字面量才是标记）⇒ `template` 作为普通标识符的既有写法不受影响；无承载面的
	// 值位由 IR fail loud（不是静默求值）。
	if p.checkTemplateMarker() {
		tr, err := p.parseTemplateBody()
		if err != nil {
			return nil, err
		}
		return tr, nil
	}

	if p.check(token.TEMPLATE_STRING) {
		tok := p.advance()
		return p.parseTemplateString(tok)
	}
	if p.check(token.STRING_LITERAL, token.INT_LITERAL, token.FLOAT_LITERAL,
		token.BOOL_LITERAL, token.NULL_LITERAL) {
		return p.parseLiteralPrimary()
	}
	// G13: `int(...)` numeric cast — the int type keyword reads as a
	// contextual function callee only when immediately followed by '('
	// (type positions never reach parsePrimary, so no ambiguity).
	if p.check(token.KW_INT) && p.checkNext(token.LPAREN) {
		p.advance()
		ident := ast.NewIdentExpr("int")
		ident.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
		return ident, nil
	}
	// G14: `float(...)` numeric cast —— 与 `int(...)` 同一条上下文规则：
	// float 类型关键字仅当紧跟 '(' 时读作函数名（类型位置不经过
	// parsePrimary, 无歧义）。用途 = 平台参数边界的显式数值归一（R-128）。
	if p.check(token.KW_FLOAT) && p.checkNext(token.LPAREN) {
		p.advance()
		ident := ast.NewIdentExpr("float")
		ident.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
		return ident, nil
	}
	// file(x) — string→file 显式标注 cast: 与 int(...) 同一上下文规则,
	// file 类型关键字仅当紧跟 '(' 时读作函数名（类型位置不经过
	// parsePrimary, 无歧义）。
	if p.check(token.KW_FILE) && p.checkNext(token.LPAREN) {
		p.advance()
		ident := ast.NewIdentExpr("file")
		ident.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
		return ident, nil
	}
	if p.check(token.IDENTIFIER) {
		tok := p.advance()
		ident := ast.NewIdentExpr(tok.Lexeme)
		// NCODA-DOLLAR-ESCAPE-SPEC: IDENTIFIER token with keyword-spelling lexeme
		// can only come from '$'-escape (plain keyword → KW_* token, never IDENTIFIER).
		// Mark Escaped so the binder rejects escaped keyword-builtin calls
		// (e.g. $file(...) must not silently alias the file() cast).
		if token.IsKeyword(tok.Lexeme) {
			ident.SetEscaped(true)
		}
		ident.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
		return ident, nil
	}

	// Lambda: (params) -> body (parser.py checks this before grouping).
	if p.looksLikeLambda() {
		return p.parseLambdaExpr()
	}

	// Grouped: (expr)
	if _, ok := p.match(token.LPAREN); ok {
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.RPAREN, "Expected ')'"); err != nil {
			return nil, err
		}
		// route 1a：记录括号分组（?? 与 ||/&& 混用限制需要区分裸表达式）。
		p.markGrouped(expr)
		return expr, nil
	}

	// Array literal: [expr, expr, ...]
	if _, ok := p.match(token.LBRACKET); ok {
		var elements []ast.Expr
		if !p.check(token.RBRACKET) {
			first, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			elements = append(elements, first)
			for {
				if _, ok := p.match(token.COMMA); !ok {
					break
				}
				next, err := p.parseExpression()
				if err != nil {
					return nil, err
				}
				elements = append(elements, next)
			}
		}
		if _, err := p.consume(token.RBRACKET, "Expected ']'"); err != nil {
			return nil, err
		}
		a := ast.NewArrayLit(elements)
		a.SetSpan(ast.Span{Start: tok.Location, End: p.previous().Location})
		return a, nil
	}

	// Parallel-for expressions (parser.py:_parse_primary dispatch). A bare
	// `parallel { ... }` is a STATEMENT (parseParallelStmt); the value
	// expression form was removed: parallel branches are a transparent
	// barrier with no collection value (arrays are authored explicitly).
	if p.check(token.KW_PARALLEL) && p.checkNext(token.KW_FOR) {
		return p.parseParallelForExpr()
	}
	if p.check(token.KW_FOR) {
		return p.parseForExpr()
	}

	// Map literal: { key: value, ... }
	if p.check(token.LBRACE) {
		return p.parseMapLiteral()
	}

	return nil, p.error("Expected expression")
}

// parseForExpr parses "for ( [var|<type>] <var> in <iterable> ) <block>"
// as an expression (parser.py:_parse_for_expr).
func (p *Parser) parseForExpr() (*ast.ForExpr, *ParseError) {
	start, err := p.consume(token.KW_FOR, "Expected 'for'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LPAREN, "Expected '(' after 'for'"); err != nil {
		return nil, err
	}
	vars, types, sources, err := p.parseForHead()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after for clause"); err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	var e *ast.ForExpr
	if len(sources) > 1 {
		e = ast.NewZipForExpr(vars, types, sources, body)
	} else {
		e = ast.NewForExpr(vars[0], types[0], sources[0], body)
	}
	e.SetSpan(p.spanFrom(start))
	return e, nil
}

// parseParallelForExpr parses
//
//	"parallel for ( <var> in <iterable> [, concurrency: <int>]
//	                [, on_error: <mode>] ) <block>"
//
// (parser.py:_parse_parallel_for_expr). The configuration clause is optional
// and key-order free as of the 2026-09-29 language relaxation; see
// parallel_for_config.go for the accepted shapes and the omission semantics.
func (p *Parser) parseParallelForExpr() (*ast.ParallelForExpr, *ParseError) {
	start, err := p.consume(token.KW_PARALLEL, "Expected 'parallel'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.KW_FOR, "Expected 'for' after 'parallel'"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LPAREN, "Expected '(' after 'parallel for'"); err != nil {
		return nil, err
	}
	vars, types, sources, err := p.parseForHead()
	if err != nil {
		return nil, err
	}
	concurrency, errorMode, err := p.parseParallelForConfig()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after parallel-for configuration"); err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	var e *ast.ParallelForExpr
	if len(sources) > 1 {
		e = ast.NewZipParallelForExpr(vars, types, sources, body, concurrency, errorMode)
	} else {
		e = ast.NewParallelForExpr(vars[0], types[0], sources[0], body, concurrency, errorMode)
	}
	e.SetSpan(p.spanFrom(start))
	return e, nil
}

// parseMapLiteral parses "{ <key>: <expr>, ... }" (parser.py:
// _parse_map_literal).
func (p *Parser) parseMapLiteral() (*ast.MapLit, *ParseError) {
	start, err := p.consume(token.LBRACE, "Expected map literal")
	if err != nil {
		return nil, err
	}
	var entries []ast.MapEntry
	if !p.check(token.RBRACE) {
		key, err := p.parseMapKey()
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.COLON, "Expected ':' after map key"); err != nil {
			return nil, err
		}
		value, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		entries = append(entries, ast.MapEntry{Key: key, Value: value})
		for {
			if _, ok := p.match(token.COMMA); !ok {
				break
			}
			// trailing comma before '}' is allowed (consistent with
			// parseEnumDecl): `{ a: 1, b: 2, }`.
			if p.check(token.RBRACE) {
				break
			}
			key, err := p.parseMapKey()
			if err != nil {
				return nil, err
			}
			if _, err := p.consume(token.COLON, "Expected ':' after map key"); err != nil {
				return nil, err
			}
			value, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			entries = append(entries, ast.MapEntry{Key: key, Value: value})
		}
	}
	if _, err := p.consume(token.RBRACE, "Expected '}'"); err != nil {
		return nil, err
	}
	lit := ast.NewMapLit(entries)
	lit.SetSpan(p.spanFrom(start))
	return lit, nil
}

// parseMapKey parses a map key, treating the reserved 'timeout' keyword as
// an identifier (parser.py:_parse_map_key).
//
// `limit` 同款（2026-10-10，r57 ask 参数面）：`ask` 的 config 有一个键就叫
// `limit`（平台 `question.limit`，与既有 `timeout` 键同族）—— 而 `limit` 是
// while 子句的保留字（`while cond limit N {}`）⇒ 与 `timeout` 面临同一个歧义。
// **词法零改动、零新保留字**：这里只是让保留字在 **map 键位置**回落成标识符
// （与 timeout 的处理逐字同形，不是新形态）。除 map 键位置外，二者仍是保留字。
func (p *Parser) parseMapKey() (ast.Expr, *ParseError) {
	for _, kw := range []token.Type{token.KW_TIMEOUT, token.KW_LIMIT} {
		if tok, ok := p.match(kw); ok {
			key := ast.NewIdentExpr(tok.Lexeme)
			key.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
			return key, nil
		}
	}
	return p.parseExpression()
}

// ── Foreign code expressions (parser.py: foreign code section) ──

// parseForeignCodeExpr parses "foreign code <lang>(<inputs>) -> <output>
// { source <template>; }" (parser.py:_parse_foreign_code_expr). On failure
// the cursor is recovered to the enclosing statement terminator before the
// error propagates to the block-level synchronize.
func (p *Parser) parseForeignCodeExpr() (*ast.ForeignCodeExpr, *ParseError) {
	startIndex := p.current
	start, err := p.consume(token.KW_FOREIGN, "Expected 'foreign'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.KW_CODE, "Expected 'code' after 'foreign'"); err != nil {
		return nil, err
	}
	expr, err := p.parseForeignCodeExprTail(start)
	if err != nil {
		p.recoverCodeExpr(startIndex)
		return nil, err
	}
	return expr, nil
}

// parseForeignCodeExprTail parses the language, inputs, output contract, and
// source of a foreign code expression (parser.py:_parse_foreign_code_expr_tail).
func (p *Parser) parseForeignCodeExprTail(start token.Token) (*ast.ForeignCodeExpr, *ParseError) {
	if p.check(token.LPAREN) {
		return nil, p.codedError(diag.CODE_LANGUAGE_UNSUPPORTED,
			"Foreign Code requires a language name after `foreign code`", &start)
	}

	languageTok := p.peek()
	if _, ok := p.match(token.IDENTIFIER, token.STRING_LITERAL); !ok {
		return nil, p.codedError(diag.CODE_LANGUAGE_UNSUPPORTED,
			"Unsupported Code language; use `python3` or \"python3\"", &languageTok)
	}
	language := languageTok.Lexeme
	if languageTok.Literal.Valid && languageTok.Literal.Kind == token.LitString {
		language = languageTok.Literal.S
	}
	switch language {
	case "python", "python3":
		language = "python3"
	case "javascript", "js", "nodejs":
		language = "javascript"
	default:
		return nil, p.codedError(diag.CODE_LANGUAGE_UNSUPPORTED,
			"Unsupported Code language '"+language+"'; use `python3` or `javascript`", &languageTok)
	}

	if _, err := p.expectCoded(token.LPAREN, diag.CODE_OUTPUT_INVALID, "Expected '(' after Code language"); err != nil {
		return nil, err
	}
	inputs, err := p.parseCodeInputs()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectCoded(token.RPAREN, diag.CODE_OUTPUT_INVALID, "Expected ')' after Code inputs"); err != nil {
		return nil, err
	}
	if _, err := p.expectCoded(token.ARROW, diag.CODE_OUTPUT_INVALID, "Expected '->' before Code output contract"); err != nil {
		return nil, err
	}
	output, err := p.parseCodeOutputContract()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectCoded(token.LBRACE, diag.CODE_SOURCE_REQUIRED, "Expected Code body containing one source"); err != nil {
		return nil, err
	}
	source, err := p.parseCodeSource()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectCoded(token.RBRACE, diag.CODE_SOURCE_REQUIRED, "Expected '}' after Code source"); err != nil {
		return nil, err
	}

	// 语言必须透传：javascript 是合法语言面（coze CodeRunner language=5），
	// 硬编码 python3 会让后续 pass/runtime 误按 Python 处理 JS 源码。
	expr := ast.NewForeignCodeExpr(language, inputs, output, source)
	expr.SetSpan(p.spanFrom(start))
	return expr, nil
}

// recoverCodeExpr positions the cursor at the enclosing statement terminator
// (parser.py:_recover_code_expr), so the block-level synchronize consumes
// the ';' and stops instead of skipping through nested braces.
func (p *Parser) recoverCodeExpr(startIndex int) {
	p.current = startIndex + 1
	parenDepth := 0
	braceDepth := 0
	for !p.isAtEnd() {
		switch p.peek().Type {
		case token.LPAREN:
			parenDepth++
		case token.RPAREN:
			if parenDepth > 0 {
				parenDepth--
			}
		case token.LBRACE:
			braceDepth++
		case token.RBRACE:
			if braceDepth > 0 {
				braceDepth--
			}
		case token.SEMICOLON:
			if parenDepth == 0 && braceDepth == 0 {
				return
			}
		}
		p.advance()
	}
}

// parseCodeInputs parses the typed input bindings of a foreign code
// expression (parser.py:_parse_code_inputs).
func (p *Parser) parseCodeInputs() ([]*ast.CodeInputBinding, *ParseError) {
	var inputs []*ast.CodeInputBinding
	names := map[string]bool{}
	for !p.check(token.RPAREN) && !p.isAtEnd() {
		start := p.peek()
		typeRef, err := p.parseCodeTypeRef()
		if err != nil {
			return nil, err
		}
		if err := p.rejectCodeFileType(typeRef, "input"); err != nil {
			return nil, err
		}
		nameTok, err := p.parseCodePortName("input")
		if err != nil {
			return nil, err
		}
		if names[nameTok.Lexeme] {
			return nil, p.codedError(diag.CODE_DUPLICATE_INPUT,
				"Duplicate Code input '"+nameTok.Lexeme+"'; use a unique input name", &nameTok)
		}
		names[nameTok.Lexeme] = true
		if _, err := p.expectCoded(token.ASSIGN, diag.CODE_OUTPUT_INVALID, "Expected '=' in Code input binding"); err != nil {
			return nil, err
		}
		value, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		binding := ast.NewCodeInputBinding(nameTok.Lexeme, typeRef, value)
		binding.SetSpan(p.spanFrom(start))
		inputs = append(inputs, binding)
		if _, ok := p.match(token.COMMA); !ok {
			break
		}
		if p.check(token.RPAREN) {
			break
		}
	}
	return inputs, nil
}

// parseCodeOutputContract parses the direct or structural output contract
// (parser.py:_parse_code_output_contract). Structural outputs require at
// least two fields.
func (p *Parser) parseCodeOutputContract() (ast.CodeOutputContract, *ParseError) {
	if _, ok := p.match(token.LBRACE); !ok {
		typeRef, err := p.parseCodeTypeRef()
		if err != nil {
			return nil, err
		}
		if err := p.rejectCodeFileType(typeRef, "output"); err != nil {
			return nil, err
		}
		output := ast.NewCodeDirectOutput(typeRef)
		output.SetSpan(typeRef.Span())
		return output, nil
	}

	start := p.previous()
	var fields []*ast.CodeOutputField
	names := map[string]bool{}
	for !p.check(token.RBRACE) && !p.isAtEnd() {
		fieldStart := p.peek()
		nameTok, err := p.parseCodePortName("output")
		if err != nil {
			return nil, err
		}
		if names[nameTok.Lexeme] {
			return nil, p.codedError(diag.CODE_DUPLICATE_OUTPUT,
				"Duplicate Code output '"+nameTok.Lexeme+"'; use a unique output name", &nameTok)
		}
		names[nameTok.Lexeme] = true
		if _, err := p.expectCoded(token.COLON, diag.CODE_OUTPUT_INVALID, "Expected ':' after Code output name"); err != nil {
			return nil, err
		}
		typeRef, err := p.parseCodeTypeRef()
		if err != nil {
			return nil, err
		}
		if err := p.rejectCodeFileType(typeRef, "output"); err != nil {
			return nil, err
		}
		if _, err := p.expectCoded(token.SEMICOLON, diag.CODE_OUTPUT_INVALID, "Expected ';' after Code output field"); err != nil {
			return nil, err
		}
		field := ast.NewCodeOutputField(nameTok.Lexeme, typeRef)
		field.SetSpan(p.spanFrom(fieldStart))
		fields = append(fields, field)
	}
	if _, err := p.expectCoded(token.RBRACE, diag.CODE_OUTPUT_INVALID, "Expected '}' after Code outputs"); err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, p.codedError(diag.CODE_OUTPUT_INVALID,
			"Structural Code output is invalid; declare at least one field", &start)
	}
	output := ast.NewCodeStructuralOutput(fields)
	output.SetSpan(p.spanFrom(start))
	return output, nil
}

// parseCodeSource parses the single `source` template-string member of a
// Code body (parser.py:_parse_code_source).
func (p *Parser) parseCodeSource() (*ast.TemplateString, *ParseError) {
	sourceKw := p.peek()
	if _, ok := p.match(token.KW_SOURCE); !ok {
		return nil, p.codedError(diag.CODE_SOURCE_REQUIRED,
			"Code body requires exactly one `source` template-string member", &sourceKw)
	}
	sourceTok := p.peek()
	var source *ast.TemplateString
	if _, ok := p.match(token.TEMPLATE_STRING); ok {
		var err *ParseError
		source, err = p.parseTemplateString(sourceTok)
		if err != nil {
			return nil, err
		}
	} else if p.check(token.STRING_LITERAL) && strings.HasPrefix(sourceTok.Lexeme, "`") {
		p.advance()
		text := ""
		if sourceTok.Literal.Valid && sourceTok.Literal.Kind == token.LitString {
			text = sourceTok.Literal.S
		}
		source = ast.NewTemplateString([]ast.TemplatePart{{Text: text}})
		source.SetSpan(ast.Span{Start: sourceTok.Location, End: sourceTok.Location})
	} else {
		return nil, p.codedError(diag.CODE_SOURCE_REQUIRED,
			"Code `source` must be a backtick template string", &sourceTok)
	}
	if _, err := p.expectCoded(token.SEMICOLON, diag.CODE_SOURCE_REQUIRED, "Expected ';' after Code source"); err != nil {
		return nil, err
	}
	if !p.check(token.RBRACE) {
		return nil, p.codedError(diag.CODE_SOURCE_REQUIRED,
			"Code body must contain exactly one `source` member")
	}
	return source, nil
}

// pythonKeywords mirrors Python's keyword.iskeyword set used by
// _parse_code_port_name to reject reserved names.
var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

// parseCodePortName parses a Code input/output port name, rejecting reserved
// Python keywords and "python3" (parser.py:_parse_code_port_name).
func (p *Parser) parseCodePortName(kind string) (token.Token, *ParseError) {
	tok := p.peek()
	// 端口名允许 ncoda 关键字（output/array/any/input/type...）——真实语料
	// canvas 端口名就叫这些（output:output 31 例、input:input 115 例），
	// 语法必须放行以保端口名 roundtrip；上下文（后随 ':' 或 '='）无歧义。
	// 仍拒绝 python 关键字（Python 代码里端口名不能是 python 保留字）。
	if tok.Type != token.IDENTIFIER && !token.IsKeyword(tok.Lexeme) {
		return token.Token{}, p.codedError(diag.CODE_PORT_INVALID,
			"Invalid or reserved Code "+kind+" name '"+tok.Lexeme+"'; use a valid identifier", &tok)
	}
	if pythonKeywords[tok.Lexeme] || tok.Lexeme == "python3" {
		return token.Token{}, p.codedError(diag.CODE_PORT_INVALID,
			"Invalid or reserved Code "+kind+" name '"+tok.Lexeme+"'; use a valid identifier", &tok)
	}
	p.advance()
	return tok, nil
}

// parseCodeTypeRef parses a Code type contract, wrapping type-ref failures
// in the Code-specific diagnostic (parser.py:_parse_code_type_ref).
func (p *Parser) parseCodeTypeRef() (ast.TypeRef, *ParseError) {
	typeRef, err := p.parseTypeRef()
	if err != nil {
		if len(p.errors) > 0 && p.errors[len(p.errors)-1] == err {
			p.errors = p.errors[:len(p.errors)-1]
		}
		return nil, p.codedError(diag.CODE_OUTPUT_INVALID,
			"Invalid Code type contract; use a primitive, array, map, or named type")
	}
	return typeRef, nil
}

// rejectCodeFileType reports the unsupported `file` Code type
// (parser.py:_reject_code_file_type).
func (p *Parser) rejectCodeFileType(typeRef ast.TypeRef, kind string) *ParseError {
	if p.codeTypeContainsFile(typeRef) {
		prev := p.previous()
		return p.codedError(diag.CODE_OUTPUT_INVALID,
			"Code "+kind+" type `file` is unsupported; use a serializable value type", &prev)
	}
	return nil
}

// codeTypeContainsFile walks a type reference for a `file` component
// (parser.py:_code_type_contains_file).
func (p *Parser) codeTypeContainsFile(typeRef ast.TypeRef) bool {
	switch t := typeRef.(type) {
	case *ast.PrimTypeRef:
		return t.Name() == "file"
	case *ast.ArrayTypeRef:
		return p.codeTypeContainsFile(t.Element())
	case *ast.MapTypeRef:
		return p.codeTypeContainsFile(t.Key()) || p.codeTypeContainsFile(t.Value())
	}
	return false
}
