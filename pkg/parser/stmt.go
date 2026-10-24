// This file ports the statement productions from src/nclang/lang/parser.py
// (statements section): parse_block, the _parse_statement dispatch, and the
// if/for/while/parallel/attempt/request_input/return/yield/answer/output/
// break/continue/var-decl/assignment/expression-statement productions.
// Every production mirrors parser.py method-for-method: the same token
// expectations, the same error messages, and the same span assignments.
package parser

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/token"
)

// parseBlock parses a brace-delimited statement block (parser.py:
// _parse_block): "{ <statement>; ... }" with per-statement error recovery
// (synchronize) and optional semicolons between statements. An empty block
// produces an empty statements list. Replaces the Task 3 placeholder, so
// invalid bodies now report errors instead of being silently skipped.
// maxStmtDepth guards statement-block nesting (P4-4). Calibrated to the
// reference adapter's measured nested-if rejection point: the Python oracle
// accepts 121 nested if blocks and rejects 122 (RecursionError), so Go
// matches with maxStmtDepth=122 (function body + 121 ifs = 122 blocks OK,
// 123rd block rejects). Other statement constructs (for/while/attempt)
// may deviate slightly because the reference's limit is interpreter
// frame-depth, which varies by construct.
const maxStmtDepth = 122

func (p *Parser) parseBlock() (*ast.Block, *ParseError) {
	if p.stmtDepth >= maxStmtDepth {
		return nil, p.recursionLimitError()
	}
	p.stmtDepth++
	defer func() { p.stmtDepth-- }()
	start, err := p.consume(token.LBRACE, "Expected '{'")
	if err != nil {
		return nil, err
	}
	var stmts []ast.Stmt
	p.skipSemicolons()
	for !p.check(token.RBRACE) && !p.isAtEnd() {
		stmt, err := p.parseStmt()
		if err != nil {
			if err.AdapterError {
				return nil, err // depth-limit is fatal: bubble up like Python's exception
			}
			p.synchronize()
		} else {
			stmts = append(stmts, stmt)
		}
		p.skipSemicolons()
	}
	if _, err := p.consume(token.RBRACE, "Expected '}'"); err != nil {
		return nil, err
	}
	block := ast.NewBlock(stmts)
	block.SetSpan(p.spanFrom(start))
	return block, nil
}

// parseStmt dispatches on the current token to the statement production
// (parser.py:_parse_statement). const is a top-level declaration only;
// inside blocks the dispatch matches Python exactly.
func (p *Parser) parseStmt() (ast.Stmt, *ParseError) {
	switch p.peek().Type {
	case token.KW_IF:
		return p.parseIfStmt()
	case token.KW_FOR:
		return p.parseForStmt()
	case token.KW_WHILE:
		return p.parseWhileStmt()
	case token.KW_RETURN:
		return p.parseReturnStmt()
	case token.KW_YIELD:
		return p.parseYieldStmt()
	case token.KW_PARALLEL:
		if p.checkNext(token.KW_FOR) {
			return p.parseParallelForStmt()
		}
		return p.parseParallelStmt()
	case token.KW_SWITCH, token.KW_INTENT:
		return p.parseSwitchStmt()
	case token.KW_ATTEMPT:
		return p.parseAttemptStmt()
	case token.KW_REQUEST_INPUT:
		return p.parseRequestInputStmt()
	case token.KW_BREAK:
		tok := p.advance()
		if _, err := p.consume(token.SEMICOLON, "Expected ';' after 'break'"); err != nil {
			return nil, err
		}
		s := ast.NewBreakStmt()
		s.SetSpan(ast.Span{Start: tok.Location, End: p.previous().Location})
		return s, nil
	case token.KW_CONTINUE:
		tok := p.advance()
		if _, err := p.consume(token.SEMICOLON, "Expected ';' after 'continue'"); err != nil {
			return nil, err
		}
		s := ast.NewContinueStmt()
		s.SetSpan(ast.Span{Start: tok.Location, End: p.previous().Location})
		return s, nil
	}

	// Variable declaration: <type> <name> = ...  or  var <name> = ...
	if p.check(token.KW_STRING, token.KW_INT, token.KW_FLOAT, token.KW_BOOL,
		token.KW_FILE, token.KW_OBJECT, token.KW_ANY, token.KW_MAP, token.KW_ARRAY) {
		return p.parseTypedVarDecl()
	}
	if p.check(token.KW_VAR, token.KW_LET) {
		return p.parseVarDecl()
	}
	if p.check(token.IDENTIFIER) && p.looksLikeTypedVarDecl() {
		return p.parseTypedVarDecl()
	}

	// output(key, value)
	if p.check(token.KW_OUTPUT) {
		return p.parseOutputStmt()
	}

	// Expression statement (assignment or function call).
	return p.parseExprOrAssignStmt()
}

// checkTypeKeyword reports whether the current token starts a builtin type
// reference (parser.py:_check_type_keyword).
func (p *Parser) checkTypeKeyword() bool {
	return p.check(token.KW_STRING, token.KW_INT, token.KW_FLOAT, token.KW_BOOL,
		token.KW_FILE, token.KW_OBJECT, token.KW_ANY, token.KW_MAP, token.KW_ARRAY)
}

// looksLikeTypedVarDecl reports whether the current tokens start an
// identifier-led typed declaration (parser.py:_looks_like_typed_var_decl):
// parse the type speculatively and restore cursor and diagnostics so the
// lookahead is side-effect free.
func (p *Parser) looksLikeTypedVarDecl() bool {
	// Mirrors parser.py:_looks_like_typed_var_decl: the identifier check runs
	// on the speculative cursor BEFORE the restore, so `print("x")` does not
	// look like a typed declaration.
	saved := p.current
	errorCount := len(p.errors)
	_, err := p.parseTypeRef()
	isDecl := err == nil && p.check(token.IDENTIFIER)
	p.current = saved
	p.errors = p.errors[:errorCount]
	return isDecl
}

// parseTypedVarDecl parses "<type> <name> [= <expr>] ;"
// (parser.py:_parse_typed_var_decl). Typed declarations are mutable.
func (p *Parser) parseTypedVarDecl() (*ast.VarDeclStmt, *ParseError) {
	start := p.peek()
	typeRef, err := p.parseTypeRef()
	if err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected variable name")
	if err != nil {
		return nil, err
	}
	var init ast.Expr
	if _, ok := p.match(token.ASSIGN); ok {
		init, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after variable declaration"); err != nil {
		return nil, err
	}
	s := ast.NewVarDeclStmt(nameTok.Lexeme, typeRef, init, true)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// parseVarDecl parses "var <name> = <expr>; or let <name> = <expr>;"
// (parser.py:_parse_var_decl). 'var' is mutable, 'let' is immutable.
func (p *Parser) parseVarDecl() (*ast.VarDeclStmt, *ParseError) {
	kwTok := p.advance() // consume 'var' or 'let'
	isMutable := kwTok.Type == token.KW_VAR
	nameTok, err := p.consume(token.IDENTIFIER, "Expected variable name")
	if err != nil {
		return nil, err
	}
	var init ast.Expr
	if _, ok := p.match(token.ASSIGN); ok {
		init, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after variable declaration"); err != nil {
		return nil, err
	}
	s := ast.NewVarDeclStmt(nameTok.Lexeme, nil, init, isMutable)
	s.SetSpan(p.spanFrom(kwTok))
	return s, nil
}

// parseExprOrAssignStmt parses an expression statement or an assignment
// (parser.py:_parse_expr_or_assign_stmt). Compound assignments desugar the
// value to a BinaryExpr with a deepcopy of the target on the left.
func (p *Parser) parseExprOrAssignStmt() (ast.Stmt, *ParseError) {
	start := p.peek()
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	// Assignment: <expr> = <expr>;
	if _, ok := p.match(token.ASSIGN); ok {
		value, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.SEMICOLON, "Expected ';' after assignment"); err != nil {
			return nil, err
		}
		s := ast.NewAssignStmt(expr, value, "set")
		s.SetSpan(p.spanFrom(start))
		return s, nil
	}

	// Compound assignments: +=, -=, *=, /= desugar to BinaryExpr values.
	for _, op := range []struct {
		tokType token.Type
		op      string // BinaryExpr op
		assign  string // AssignStmt operation
	}{
		{token.PLUS_ASSIGN, "+", "+="},
		{token.MINUS_ASSIGN, "-", "-="},
		{token.STAR_ASSIGN, "*", "*="},
		{token.SLASH_ASSIGN, "/", "/="},
	} {
		if _, ok := p.match(op.tokType); ok {
			value, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if _, err := p.consume(token.SEMICOLON, "Expected ';' after assignment"); err != nil {
				return nil, err
			}
			// Python leaves the desugared BinaryExpr span None.
			desugared := ast.NewBinaryExpr(op.op, deepCopyExpr(expr), value)
			s := ast.NewAssignStmt(expr, desugared, op.assign)
			s.SetSpan(p.spanFrom(start))
			return s, nil
		}
	}

	// Append: <expr> << <expr>;
	if _, ok := p.match(token.LESS_LESS); ok {
		value, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.SEMICOLON, "Expected ';' after append"); err != nil {
			return nil, err
		}
		s := ast.NewAssignStmt(expr, value, "<<")
		s.SetSpan(p.spanFrom(start))
		return s, nil
	}

	// Expression statement.
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after expression"); err != nil {
		return nil, err
	}
	s := ast.NewExprStmt(expr)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// deepCopyExpr builds a full, independent copy of an expression tree with
// fresh NodeIDs and preserved spans (Python: copy.deepcopy(expr) in
// _parse_expr_or_assign_stmt). The copy is complete — every reachable child
// (including the blocks/statements/type refs nested inside for, parallel,
// and foreign-code expressions) is rebuilt, so NodeID-keyed walkers never
// observe a DAG from compound-assignment desugaring.
func deepCopyExpr(e ast.Expr) ast.Expr {
	if e == nil {
		return nil
	}
	return deepCopyNode(e).(ast.Expr)
}

// deepCopyNode copies any parser-produced node reachable from an expression:
// expression nodes, operation policies, code contracts, blocks, statements,
// and type references. Constructors assign fresh NodeIDs; spans are copied
// back onto the clone (copy.deepcopy preserves the span attribute).
func deepCopyNode(n ast.Node) ast.Node {
	if n == nil {
		return nil
	}
	switch t := n.(type) {
	// ── literals / identifiers (leaf) ──
	case *ast.IntLit:
		c := ast.NewIntLit(t.Value())
		c.SetSpan(t.Span())
		return c
	case *ast.FloatLit:
		c := ast.NewFloatLit(t.Value())
		c.SetSpan(t.Span())
		return c
	case *ast.StringLit:
		c := ast.NewStringLit(t.Value())
		c.SetSpan(t.Span())
		return c
	case *ast.BoolLit:
		c := ast.NewBoolLit(t.Value())
		c.SetSpan(t.Span())
		return c
	case *ast.NullLit:
		c := ast.NewNullLit()
		c.SetSpan(t.Span())
		return c
	case *ast.IdentExpr:
		c := ast.NewIdentExpr(t.Name())
		c.SetEscaped(t.Escaped()) // 转义标记随拷贝传播（binder 拒绝 $file(...) 需要它）
		c.SetSpan(t.Span())
		return c

	// ── literals with children ──
	case *ast.TemplateString:
		parts := make([]ast.TemplatePart, len(t.Parts()))
		for i, part := range t.Parts() {
			parts[i] = ast.TemplatePart{Text: part.Text, Expr: deepCopyExpr(part.Expr)}
		}
		c := ast.NewTemplateString(parts)
		c.SetSpan(t.Span())
		return c
	case *ast.ArrayLit:
		c := ast.NewArrayLit(deepCopyExprs(t.Elements()))
		c.SetSpan(t.Span())
		return c
	case *ast.MapLit:
		entries := make([]ast.MapEntry, len(t.Entries()))
		for i, en := range t.Entries() {
			entries[i] = ast.MapEntry{Key: deepCopyExpr(en.Key), Value: deepCopyExpr(en.Value)}
		}
		c := ast.NewMapLit(entries)
		c.SetSpan(t.Span())
		return c

	// ── access / calls ──
	case *ast.FieldAccessExpr:
		c := ast.NewFieldAccessExpr(deepCopyExpr(t.Obj()), t.FieldName())
		c.SetSpan(t.Span())
		return c
	case *ast.IndexExpr:
		c := ast.NewIndexExpr(deepCopyExpr(t.Obj()), deepCopyExpr(t.Index()))
		c.SetSpan(t.Span())
		return c
	case *ast.CallExpr:
		c := ast.NewCallExpr(deepCopyExpr(t.Func()), deepCopyTypeRefs(t.TypeArgs()), deepCopyExprs(t.Args()))
		c.SetSpan(t.Span())
		return c

	// ── operation policies ──
	case *ast.RetryPolicy:
		c := ast.NewRetryPolicy(t.MaxRetries(), t.IntervalMs())
		c.SetIntervalExplicit(t.IntervalExplicit()) // provenance 必须随深拷贝一起走
		c.SetSpan(t.Span())
		return c
	case *ast.TimeoutPolicy:
		c := ast.NewTimeoutPolicy(t.TimeoutMs())
		c.SetSpan(t.Span())
		return c
	case *ast.DefaultPolicy:
		c := ast.NewDefaultPolicy(deepCopyExpr(t.Value()))
		c.SetSpan(t.Span())
		return c
	case *ast.OperationPolicyExpr:
		policies := make([]ast.OperationPolicy, len(t.Policies()))
		for i, pol := range t.Policies() {
			policies[i] = deepCopyNode(pol).(ast.OperationPolicy)
		}
		c := ast.NewOperationPolicyExpr(deepCopyExpr(t.Operation()), policies)
		c.SetSpan(t.Span())
		return c

	// ── unary / binary / ternary / lambda ──
	case *ast.UnaryExpr:
		c := ast.NewUnaryExpr(t.Op(), deepCopyExpr(t.Operand()))
		c.SetSpan(t.Span())
		return c
	case *ast.BinaryExpr:
		c := ast.NewBinaryExpr(t.Op(), deepCopyExpr(t.Left()), deepCopyExpr(t.Right()))
		c.SetSpan(t.Span())
		return c
	case *ast.TernaryExpr:
		c := ast.NewTernaryExpr(deepCopyExpr(t.Condition()), deepCopyExpr(t.ThenVal()), deepCopyExpr(t.ElseVal()))
		c.SetSpan(t.Span())
		return c
	case *ast.LambdaExpr:
		c := ast.NewLambdaExpr(append([]string(nil), t.Params()...), deepCopyExpr(t.Body()))
		c.SetSpan(t.Span())
		return c

	// ── for / parallel / foreign code ──
	case *ast.ForExpr:
		if t.IsZip() {
			c := ast.NewZipForExpr(loopVars(t), loopTypes(t), loopSources(t), deepCopyBlock(t.Body()))
			c.SetSpan(t.Span())
			return c
		}
		c := ast.NewForExpr(t.VarName(), deepCopyTypeRef(t.VarType()), deepCopyExpr(t.Iterable()), deepCopyBlock(t.Body()))
		c.SetSpan(t.Span())
		return c
	case *ast.ParallelForExpr:
		if t.IsZip() {
			c := ast.NewZipParallelForExpr(loopVars(&t.ForExpr), loopTypes(&t.ForExpr), loopSources(&t.ForExpr), deepCopyBlock(t.Body()), t.Concurrency(), t.OnError())
			c.SetSpan(t.Span())
			return c
		}
		c := ast.NewParallelForExpr(t.VarName(), deepCopyTypeRef(t.VarType()), deepCopyExpr(t.Iterable()), deepCopyBlock(t.Body()), t.Concurrency(), t.OnError())
		c.SetSpan(t.Span())
		return c

	case *ast.CodeInputBinding:
		c := ast.NewCodeInputBinding(t.Name(), deepCopyTypeRef(t.TypeRef()), deepCopyExpr(t.Value()))
		c.SetSpan(t.Span())
		return c
	case *ast.CodeDirectOutput:
		c := ast.NewCodeDirectOutput(deepCopyTypeRef(t.TypeRef()))
		c.SetSpan(t.Span())
		return c
	case *ast.CodeOutputField:
		c := ast.NewCodeOutputField(t.Name(), deepCopyTypeRef(t.TypeRef()))
		c.SetSpan(t.Span())
		return c
	case *ast.CodeStructuralOutput:
		fields := make([]*ast.CodeOutputField, len(t.Fields()))
		for i, f := range t.Fields() {
			fields[i] = deepCopyNode(f).(*ast.CodeOutputField)
		}
		c := ast.NewCodeStructuralOutput(fields)
		c.SetSpan(t.Span())
		return c
	case *ast.ForeignCodeExpr:
		inputs := make([]*ast.CodeInputBinding, len(t.Inputs()))
		for i, in := range t.Inputs() {
			inputs[i] = deepCopyNode(in).(*ast.CodeInputBinding)
		}
		output := deepCopyNode(t.Output())
		var out ast.CodeOutputContract
		if output != nil {
			out = output.(ast.CodeOutputContract)
		}
		source := deepCopyNode(t.Source())
		var src *ast.TemplateString
		if source != nil {
			src = source.(*ast.TemplateString)
		}
		c := ast.NewForeignCodeExpr(t.Language(), inputs, out, src)
		c.SetSpan(t.Span())
		return c

	// ── blocks / statements (reachable from expression bodies) ──
	case *ast.Block:
		return deepCopyBlock(t)
	case *ast.VarDeclStmt:
		c := ast.NewVarDeclStmt(t.Name(), deepCopyTypeRef(t.TypeRef()), deepCopyExpr(t.Init()), t.IsMutable())
		c.SetSpan(t.Span())
		return c
	case *ast.AssignStmt:
		c := ast.NewAssignStmt(deepCopyExpr(t.Target()), deepCopyExpr(t.Value()), t.Operation())
		c.SetSpan(t.Span())
		return c
	case *ast.ExprStmt:
		c := ast.NewExprStmt(deepCopyExpr(t.Expr()))
		c.SetSpan(t.Span())
		return c
	case *ast.IfStmt:
		elseIfs := make([]*ast.ElseIfBranch, len(t.ElseIfs()))
		for i, ei := range t.ElseIfs() {
			elseIfs[i] = deepCopyNode(ei).(*ast.ElseIfBranch)
		}
		c := ast.NewIfStmt(deepCopyExpr(t.Condition()), deepCopyBlock(t.ThenBody()), elseIfs, deepCopyBlock(t.ElseBody()))
		c.SetSpan(t.Span())
		return c
	case *ast.ElseIfBranch:
		c := ast.NewElseIfBranch(deepCopyExpr(t.Condition()), deepCopyBlock(t.Body()))
		c.SetSpan(t.Span())
		return c
	case *ast.ForStmt:
		loops, loopt := copyLoopParts(t)
		if t.IsParallel() {
			if t.IsZip() {
				c := ast.NewZipParallelForStmt(loops, loopt, loopSources(t), deepCopyBlock(t.Body()), t.Concurrency(), t.OnError())
				c.SetSpan(t.Span())
				return c
			}
			c := ast.NewParallelForStmt(t.VarName(), deepCopyTypeRef(t.VarType()), deepCopyExpr(t.Iterable()), deepCopyBlock(t.Body()), t.Concurrency(), t.OnError())
			c.SetSpan(t.Span())
			return c
		}
		if t.IsZip() {
			c := ast.NewZipForStmt(loops, loopt, loopSources(t), deepCopyBlock(t.Body()))
			c.SetSpan(t.Span())
			return c
		}
		c := ast.NewForStmt(t.VarName(), deepCopyTypeRef(t.VarType()), deepCopyExpr(t.Iterable()), deepCopyBlock(t.Body()))
		c.SetSpan(t.Span())
		return c
	case *ast.WhileStmt:
		c := ast.NewWhileStmt(deepCopyExpr(t.Condition()), t.Limit(), deepCopyBlock(t.Body()))
		c.SetSpan(t.Span())
		return c
	case *ast.ReturnStmt:
		c := ast.NewReturnStmt(deepCopyExpr(t.Value()), t.Streaming())
		c.SetSpan(t.Span())
		return c
	case *ast.YieldStmt:
		var c *ast.YieldStmt
		if t.Port() != "" {
			c = ast.NewNamedYieldStmt(t.Port(), deepCopyExpr(t.Value()))
		} else {
			c = ast.NewYieldStmt(deepCopyExpr(t.Value()))
		}
		c.SetSpan(t.Span())
		return c
	case *ast.OutputStmt:
		c := ast.NewOutputStmt(deepCopyExpr(t.Value()), t.Streaming())
		c.SetSpan(t.Span())
		return c
	case *ast.ParallelStmt:
		branches := make([]*ast.Block, len(t.Branches()))
		for i, b := range t.Branches() {
			branches[i] = deepCopyBlock(b)
		}
		c := ast.NewParallelStmt(branches, t.BranchNames()...)
		c.SetSpan(t.Span())
		return c
	case *ast.AttemptStmt:
		c := ast.NewAttemptStmt(deepCopyExpr(t.Operation()), deepCopyNode(t.ResultBinding()).(*ast.VarDeclStmt), deepCopyBlock(t.SuccessBody()), deepCopyNode(t.ErrorBinding()).(*ast.VarDeclStmt), deepCopyBlock(t.FailureBody()))
		c.SetSpan(t.Span())
		return c
	case *ast.HumanActionBranch:
		c := ast.NewHumanActionBranch(deepCopyExpr(t.ActionExpr()), deepCopyBlock(t.Body()))
		c.SetSpan(t.Span())
		return c
	case *ast.RequestInputStmt:
		branches := make([]*ast.HumanActionBranch, len(t.ActionBranches()))
		for i, br := range t.ActionBranches() {
			branches[i] = deepCopyNode(br).(*ast.HumanActionBranch)
		}
		var config *ast.MapLit
		if t.Config() != nil {
			config = deepCopyNode(t.Config()).(*ast.MapLit)
		}
		c := ast.NewRequestInputStmt(deepCopyTypeRef(t.FormType()), deepCopyTypeRef(t.ActionType()), config, deepCopyNode(t.ResultBinding()).(*ast.VarDeclStmt), branches, deepCopyBlock(t.TimeoutBody()))
		c.SetSpan(t.Span())
		return c
	case *ast.SwitchBranch:
		c := ast.NewSwitchBranch(t.Label(), deepCopyBlock(t.Body()))
		c.SetSpan(t.Span())
		return c
	case *ast.SwitchStmt:
		branches := make([]*ast.SwitchBranch, len(t.Branches()))
		for i, br := range t.Branches() {
			branches[i] = deepCopyNode(br).(*ast.SwitchBranch)
		}
		var config *ast.MapLit
		if t.Config() != nil {
			config = deepCopyNode(t.Config()).(*ast.MapLit)
		}
		var defBody *ast.Block
		if t.DefaultBody() != nil {
			defBody = deepCopyBlock(t.DefaultBody())
		}
		c := ast.NewSwitchStmt(deepCopyExpr(t.Query()), config, branches, defBody, deepCopyNode(t.Result()).(*ast.VarDeclStmt))
		c.SetSpan(t.Span())
		return c
	case *ast.BreakStmt:
		c := ast.NewBreakStmt()
		c.SetSpan(t.Span())
		return c
	case *ast.ContinueStmt:
		c := ast.NewContinueStmt()
		c.SetSpan(t.Span())
		return c

	// ── type references ──
	case *ast.PrimTypeRef:
		c := ast.NewPrimTypeRef(t.Name())
		c.SetSpan(t.Span())
		return c
	case *ast.FileTypeRef:
		c := ast.NewFileTypeRef(t.Constraints())
		c.SetSpan(t.Span())
		return c
	case *ast.ArrayTypeRef:
		c := ast.NewArrayTypeRef(deepCopyTypeRef(t.Element()))
		c.SetSpan(t.Span())
		return c
	case *ast.MapTypeRef:
		c := ast.NewMapTypeRef(deepCopyTypeRef(t.Key()), deepCopyTypeRef(t.Value()))
		c.SetSpan(t.Span())
		return c
	case *ast.NamedTypeRef:
		c := ast.NewNamedTypeRef(t.Name())
		c.SetSpan(t.Span())
		return c
	case *ast.OptionalTypeRef:
		c := ast.NewOptionalTypeRef(deepCopyTypeRef(t.Value()))
		c.SetSpan(t.Span())
		return c
	}
	// Fail loudly instead of returning a nil node: a nil here would later
	// panic as an interface conversion in deepCopyExpr's .(ast.Expr) cast,
	// obscuring the real bug. New expression node types (e.g. a future
	// PipeExpr for `|>`) must be added here in the same commit.
	panic(fmt.Sprintf("deepCopyNode: unhandled node type %T", n))
}

// deepCopyExprs copies a slice of expressions (nil element stays nil).
func deepCopyExprs(es []ast.Expr) []ast.Expr {
	if es == nil {
		return nil
	}
	out := make([]ast.Expr, len(es))
	for i, e := range es {
		out[i] = deepCopyExpr(e)
	}
	return out
}

// deepCopyBlock copies a block's statement list (nil stays nil).
func deepCopyBlock(b *ast.Block) *ast.Block {
	if b == nil {
		return nil
	}
	stmts := make([]ast.Stmt, len(b.Stmts()))
	for i, s := range b.Stmts() {
		stmts[i] = deepCopyNode(s).(ast.Stmt)
	}
	c := ast.NewBlock(stmts)
	c.SetSpan(b.Span())
	return c
}

// deepCopyTypeRef copies one type reference (nil stays nil).
func deepCopyTypeRef(tr ast.TypeRef) ast.TypeRef {
	if tr == nil {
		return nil
	}
	return deepCopyNode(tr).(ast.TypeRef)
}

// deepCopyTypeRefs copies a slice of type references (nil stays nil).
func deepCopyTypeRefs(trs []ast.TypeRef) []ast.TypeRef {
	if trs == nil {
		return nil
	}
	out := make([]ast.TypeRef, len(trs))
	for i, tr := range trs {
		out[i] = deepCopyTypeRef(tr)
	}
	return out
}

// parseIfStmt parses "if ( <cond> ) <block> [else if ...] [else <block>]"
// (parser.py:_parse_if_stmt). The else-if chain becomes ElseIfBranch nodes;
// the trailing else is an optional block.
func (p *Parser) parseIfStmt() (*ast.IfStmt, *ParseError) {
	start, err := p.consume(token.KW_IF, "Expected 'if'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LPAREN, "Expected '(' after 'if'"); err != nil {
		return nil, err
	}
	condition, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after if condition"); err != nil {
		return nil, err
	}
	thenBody, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	var elseIfs []*ast.ElseIfBranch
	var elseBody *ast.Block
	for {
		if _, ok := p.match(token.KW_ELSE); !ok {
			break
		}
		if p.check(token.KW_IF) {
			// else if
			p.advance()
			if _, err := p.consume(token.LPAREN, "Expected '(' after 'else if'"); err != nil {
				return nil, err
			}
			eiCond, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if _, err := p.consume(token.RPAREN, "Expected ')' after condition"); err != nil {
				return nil, err
			}
			eiBody, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			branch := ast.NewElseIfBranch(eiCond, eiBody)
			branch.SetSpan(ast.Span{Start: p.previous().Location, End: p.previous().Location})
			elseIfs = append(elseIfs, branch)
		} else {
			elseBody, err = p.parseBlock()
			if err != nil {
				return nil, err
			}
			break
		}
	}

	s := ast.NewIfStmt(condition, thenBody, elseIfs, elseBody)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// parseForStmt parses "for ( [var|let|<type>] <var> in <iterable> ) <block>"
// (parser.py:_parse_for_stmt). Statement-level 'for' always produces a
// ForStmt; the value-producing ForExpr / parallel traversal forms are
// expression productions (parseForExpr / parseParallelForExpr) reached from
// expression positions, matching parser.py's dispatch.
func (p *Parser) parseForStmt() (*ast.ForStmt, *ParseError) {
	start, err := p.consume(token.KW_FOR, "Expected 'for'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LPAREN, "Expected '(' after 'for'"); err != nil {
		return nil, err
	}

	// Parse the loop head: optional type/var per variable, 'in', then
	// either a single iterable expression or the zip(...) source list.
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

	var s *ast.ForStmt
	if len(sources) > 1 {
		s = ast.NewZipForStmt(vars, types, sources, body)
	} else {
		s = ast.NewForStmt(vars[0], types[0], sources[0], body)
	}
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// parseForHead 解析 "for (" 子句头：变量列表 + 'in' + 源。
// 单变量 → 普通迭代表达式（zip(...) 保持函数调用语义，向后兼容）；
// 多变量 → 必须 zip 形式 "for (a, b in zip(x, y))"，变量数 = 源数。
// 消费到源列表结束，不消费 RPAREN（parallel for 的配置子句由调用方处理）。
func (p *Parser) parseForHead() ([]string, []ast.TypeRef, []ast.Expr, *ParseError) {
	var vars []string
	var types []ast.TypeRef
	for {
		var varType ast.TypeRef
		if p.check(token.KW_VAR, token.KW_LET) {
			p.advance()
		} else if p.checkTypeKeyword() {
			var err *ParseError
			varType, err = p.parseTypeRef()
			if err != nil {
				return nil, nil, nil, err
			}
		}
		varTok, err := p.consume(token.IDENTIFIER, "Expected loop variable name")
		if err != nil {
			return nil, nil, nil, err
		}
		vars = append(vars, varTok.Lexeme)
		types = append(types, varType)
		if p.check(token.COMMA) {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.consume(token.KW_IN, "Expected 'in' after loop variable"); err != nil {
		return nil, nil, nil, err
	}
	if len(vars) > 1 {
		// zip 形式（上下文关键字：仅多变量 for 头里特殊）。
		if !(p.check(token.IDENTIFIER) && p.peek().Lexeme == "zip" && p.checkNext(token.LPAREN)) {
			return nil, nil, nil, p.error("Multi-variable for loop requires zip(...) source list")
		}
		p.advance() // zip
		p.advance() // (
		var sources []ast.Expr
		for {
			expr, err := p.parseExpression()
			if err != nil {
				return nil, nil, nil, err
			}
			sources = append(sources, expr)
			if p.check(token.COMMA) {
				p.advance()
				continue
			}
			break
		}
		if _, err := p.consume(token.RPAREN, "Expected ')' after zip sources"); err != nil {
			return nil, nil, nil, err
		}
		if len(sources) != len(vars) {
			return nil, nil, nil, p.error(fmt.Sprintf("zip arity mismatch: %d variables, %d sources", len(vars), len(sources)))
		}
		return vars, types, sources, nil
	}
	iterable, err := p.parseExpression()
	if err != nil {
		return nil, nil, nil, err
	}
	return vars, types, []ast.Expr{iterable}, nil
}

// parseWhileStmt parses "while ( <cond> ) limit <int> <block>"
// (parser.py:_parse_while_stmt). The static limit clause is required.
func (p *Parser) parseWhileStmt() (*ast.WhileStmt, *ParseError) {
	start, err := p.consume(token.KW_WHILE, "Expected 'while'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LPAREN, "Expected '(' after 'while'"); err != nil {
		return nil, err
	}
	condition, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after while condition"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.KW_LIMIT, "Expected static 'limit' after while condition"); err != nil {
		return nil, err
	}
	limitTok, err := p.consume(token.INT_LITERAL, "Expected integer while limit")
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	limit := 0
	if limitTok.Literal.Valid && limitTok.Literal.Kind == token.LitInt {
		limit = int(limitTok.Literal.I)
	}
	s := ast.NewWhileStmt(condition, limit, body)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// parseParallelStmt parses "parallel { <block>; <block>; ... }"
// (parser.py:_parse_parallel_stmt). Branches are Blocks (ParallelStmt);
// the named-branch form ("name: { ... }") is also statement-level — the
// value-producing expression form was removed (parallel never yields a
// collection; see docs/COZE-EXPRESSIVENESS.md).
// parseParallelForStmt parses a statement-level parallel for:
//
//	parallel for ( [var|let|<type>] <var> in <iterable>
//	                [, concurrency: <int>] [, on_error: <terminate|keep_null|remove_failed>] ) <block>
//
// The statement form mirrors the expression-level parseParallelForExpr
// configuration clause (shared parser = parallel_for_config.go: optional and
// key-order free since the 2026-09-29 relaxation); the body is a plain block
// (no yield — side-effect loops). Renders as a commercial batch container
// (IsParallel).
func (p *Parser) parseParallelForStmt() (*ast.ForStmt, *ParseError) {
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
	var s *ast.ForStmt
	if len(sources) > 1 {
		s = ast.NewZipParallelForStmt(vars, types, sources, body, concurrency, errorMode)
	} else {
		s = ast.NewParallelForStmt(vars[0], types[0], sources[0], body, concurrency, errorMode)
	}
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

func (p *Parser) parseParallelStmt() (*ast.ParallelStmt, *ParseError) {
	start, err := p.consume(token.KW_PARALLEL, "Expected 'parallel'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LBRACE, "Expected '{' after 'parallel'"); err != nil {
		return nil, err
	}
	var branches []*ast.Block
	var names []string
	seen := map[string]bool{}
	p.skipSemicolons()
	for !p.check(token.RBRACE) && !p.isAtEnd() {
		name := ""
		// Optional per-branch label: `parallel { r1: {...} }`. The label is
		// source-level only — branch values are reached downstream through
		// the transparent scope, never by branch name.
		if p.check(token.IDENTIFIER) && p.checkNext(token.COLON) {
			nameTok, err := p.consume(token.IDENTIFIER, "Expected parallel branch name")
			if err != nil {
				return nil, err
			}
			name = nameTok.Lexeme
			if seen[name] {
				return nil, p.error("Duplicate parallel branch name '" + name + "'")
			}
			seen[name] = true
			if _, err := p.consume(token.COLON, "Expected ':' after parallel branch name"); err != nil {
				return nil, err
			}
		}
		branch, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		branches = append(branches, branch)
		names = append(names, name)
		p.skipSemicolons()
	}
	if len(branches) == 0 {
		return nil, p.error("Parallel statement requires at least one branch")
	}
	if _, err := p.consume(token.RBRACE, "Expected '}' after parallel branches"); err != nil {
		return nil, err
	}
	s := ast.NewParallelStmt(branches, names...)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// parseAttemptStmt parses:
//
//	attempt <expr> as <result> { success <block> failure ( <err> ) <block> }
//
// (parser.py:_parse_attempt_stmt). Result and error bindings are immutable
// VarDeclStmts with point spans at their names.
func (p *Parser) parseAttemptStmt() (*ast.AttemptStmt, *ParseError) {
	start, err := p.consume(token.KW_ATTEMPT, "Expected 'attempt'")
	if err != nil {
		return nil, err
	}
	operation, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.KW_AS, "Expected 'as' after attempted operation"); err != nil {
		return nil, err
	}
	result, err := p.consume(token.IDENTIFIER, "Expected success result name")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LBRACE, "Expected '{' before attempt branches"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.KW_SUCCESS, "Expected 'success' branch"); err != nil {
		return nil, err
	}
	successBody, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.KW_FAILURE, "Expected 'failure' branch"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LPAREN, "Expected '(' after failure"); err != nil {
		return nil, err
	}
	errorTok, err := p.consume(token.IDENTIFIER, "Expected failure error name")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after failure error name"); err != nil {
		return nil, err
	}
	failureBody, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.RBRACE, "Expected '}' after attempt branches"); err != nil {
		return nil, err
	}

	resultBinding := ast.NewVarDeclStmt(result.Lexeme, nil, nil, false)
	resultBinding.SetSpan(ast.Span{Start: result.Location, End: result.Location})
	errorBinding := ast.NewVarDeclStmt(errorTok.Lexeme, nil, nil, false)
	errorBinding.SetSpan(ast.Span{Start: errorTok.Location, End: errorTok.Location})

	s := ast.NewAttemptStmt(operation, resultBinding, successBody, errorBinding, failureBody)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// parseSwitchStmt parses the classification multi-exit branch statement
// (switch keyword; intent retained as a deprecated alias):
//
//	switch ( <query> [, <config-map> ]) [-> <result>] {
//	    "<label>": { <block> }
//	    ...
//	    [default: { <block> }]
//	}
//
// The intent list is extracted from the case labels (order = case order =
// platform branch_0..N); duplicate labels are rejected.
//
// 空分支体 = **合法语法**（用户拍板 08-27 重申 + 2026-09-13 A 级：
// 「空分支」的精确含义 = 分支**无后续连线（端口悬空）**，有明确连线的分支
// 是正常语义；两臂都空也「语义上无错误，语法上无错误」——编译期临时框架
// 是合法写法）。空体臂的端口由 lowering 的义务模型闭到收敛点（P4
// `pendingPorts`），**真悬挂**（声明了分支却没有端口边）由 wire 层
// `Dangling → FAILED(SWITCH-DANGLING)` 承担——parser 看不到端口连线，
// 在这里的文本层代理检查既拦不住真悬挂、又误杀合法空臂（K/R_ask 实证：
// 反编译产物里的合法空臂被自家 parser 拒收）。
func (p *Parser) parseSwitchStmt() (*ast.SwitchStmt, *ParseError) {
	var start token.Token
	if p.check(token.KW_SWITCH, token.KW_INTENT) {
		start = p.advance()
	} else {
		return nil, p.error("Expected 'switch' or 'intent'")
	}
	if _, err := p.consume(token.LPAREN, "Expected '(' after 'switch'"); err != nil {
		return nil, err
	}
	query, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	var config *ast.MapLit
	if _, ok := p.match(token.COMMA); ok {
		config, err = p.parseMapLiteral()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after switch query"); err != nil {
		return nil, err
	}
	var result *ast.VarDeclStmt
	if _, ok := p.match(token.ARROW); ok {
		nameTok, err := p.consume(token.IDENTIFIER, "Expected switch result name after '->'")
		if err != nil {
			return nil, err
		}
		result = ast.NewVarDeclStmt(nameTok.Lexeme, nil, nil, false)
	}
	if _, err := p.consume(token.LBRACE, "Expected '{' before switch branches"); err != nil {
		return nil, err
	}

	var branches []*ast.SwitchBranch
	var defBody *ast.Block
	seen := map[string]struct{}{}
	for {
		if p.check(token.STRING_LITERAL) {
			labelTok := p.advance()
			if _, err := p.consume(token.COLON, "Expected ':' after switch case label"); err != nil {
				return nil, err
			}
			label := labelTok.Literal.S
			if label == "" {
				label = labelTok.Lexeme
			}
			body, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			if _, dup := seen[label]; dup {
				return nil, p.error(fmt.Sprintf("switch case label %q 重复", label))
			}
			seen[label] = struct{}{}
			branch := ast.NewSwitchBranch(label, body)
			branch.SetSpan(ast.Span{Start: labelTok.Location, End: p.previous().Location})
			branches = append(branches, branch)
			continue
		}
		if p.check(token.KW_DEFAULT) {
			p.advance()
			if _, err := p.consume(token.COLON, "Expected ':' after 'default'"); err != nil {
				return nil, err
			}
			defBody, err = p.parseBlock()
			if err != nil {
				return nil, err
			}
			break
		}
		break
	}
	if len(branches) == 0 {
		return nil, p.error("switch 需要至少一个 case 分支")
	}
	if _, err := p.consume(token.RBRACE, "Expected '}' after switch branches"); err != nil {
		return nil, err
	}
	s := ast.NewSwitchStmt(query, config, branches, defBody, result)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// parseRequestInputStmt parses:
//
//	ask<Form, Action>({config}) as <result> {
//	  action <expr> <block> ... [timeout <block>]
//	}
//	ask<Form>({config}) as <result>;          // 无动作形态（S1）
//
// 'request_input' 保留为兼容别名（旧语料继续可解析）。timeout 分支可选
// （0 或 1 个）——平台未声明超时边时允许缺省。
//
// **第二类型参数缺席 = 无动作形态**（用户 2026-10-01 批准「甲 扩语言」）：
// 平台 `answer_type: text` 的问答节点就是这一格 —— 无选项、无分支口、**单条
// 无标签推进口**（coze-studio `nodes/qa/question_answer.go::ExpectPorts`:
// `AnswerType != option ⇒ 不声明任何分支口`；本仓同解处 =
// `platforms/cozestudio/checker/validator.go::expectedPorts` 与
// `internal/backend/cozebiz/（商业仓）verify.go::branchHostPorts`）。没有臂就没有块：
// 无动作形态**只有一种拼写**（`;` 结尾）；写出分支体时本刀只接受**空体**，
// 任何 action / timeout 臂都是错（`docs/ASK-SYNTAX.md` §2；J §33）。
//
// (parser.py:_parse_request_input_stmt). Parsed fully here; semantic
// rejection for advanced-chat-only surfaces lands in later passes.
func (p *Parser) parseRequestInputStmt() (*ast.RequestInputStmt, *ParseError) {
	start, err := p.consume(token.KW_REQUEST_INPUT, "Expected 'ask'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LESS, "Expected '<' after 'ask'"); err != nil {
		return nil, err
	}
	formType, err := p.parseTypeRef()
	if err != nil {
		return nil, err
	}
	// 第二类型参数可选：缺席 = 无动作形态（Action 类型的 nil 即该形态的判据，
	// 与 ast 构造器「nil 保持 nil」一致 —— 一处定义）。
	var actionType ast.TypeRef
	if _, ok := p.match(token.COMMA); ok {
		actionType, err = p.parseTypeRef()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.consume(token.GREATER, "Expected '>' after Human Input types"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LPAREN, "Expected '(' before Human Input config"); err != nil {
		return nil, err
	}
	config, err := p.parseMapLiteral()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after Human Input config"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.KW_AS, "Expected 'as' after Human Input config"); err != nil {
		return nil, err
	}
	bindingTok, err := p.consume(token.IDENTIFIER, "Expected Human Input result name")
	if err != nil {
		return nil, err
	}
	// 分支体可选：无动作形态下「没有臂 ⇒ 不应有块」（块的唯一身份是臂的容器）。
	var actionBranches []*ast.HumanActionBranch
	var timeoutBody *ast.Block
	if p.check(token.LBRACE) {
		p.advance()
		for {
			if _, ok := p.match(token.KW_ACTION); !ok {
				break
			}
			actionStart := p.previous()
			actionExpr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			body, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			branch := ast.NewHumanActionBranch(actionExpr, body)
			branch.SetSpan(ast.Span{Start: actionStart.Location, End: p.previous().Location})
			actionBranches = append(actionBranches, branch)
		}
		// timeout 分支可选（0 或 1 个）。
		if _, ok := p.match(token.KW_TIMEOUT); ok {
			timeoutBody, err = p.parseBlock()
			if err != nil {
				return nil, err
			}
		}
		if _, err := p.consume(token.RBRACE, "Expected '}' after Human Input branches"); err != nil {
			return nil, err
		}
	} else if _, err := p.consume(token.SEMICOLON, "Expected ';' after Human Input statement"); err != nil {
		return nil, err
	}
	if actionType == nil {
		// 无动作形态：两条臂载体都必须缺席（`ask<Form>` 的语义就是「不分支」）。
		if len(actionBranches) > 0 {
			return nil, p.error("Human Input without an action type cannot declare action branches")
		}
		if timeoutBody != nil {
			return nil, p.error("Human Input without an action type cannot declare a timeout branch")
		}
	} else if len(actionBranches) == 0 {
		return nil, p.error("Human Input requires at least one action branch")
	}

	resultBinding := ast.NewVarDeclStmt(bindingTok.Lexeme, nil, nil, false)
	resultBinding.SetSpan(ast.Span{Start: bindingTok.Location, End: bindingTok.Location})

	s := ast.NewRequestInputStmt(formType, actionType, config, resultBinding, actionBranches, timeoutBody)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// parseReturnStmt parses "return [<expr>] ;" (parser.py:_parse_return_stmt)，
// 以及流程答复端点的**模板返回**（W2）：
//
//	return template "<逐字模板文本>" [ using { name: <值>, ... } ] ;
//
// `template` / `using` 是**上下文关键字**（不是保留字，1 token 前瞻）：
// `template` 只在 `return` 之后紧跟字符串字面量时才是标记（`return template;`
// 这类写法仍按普通标识符解析 ⇒ 不引入新的保留字、不动既有源码）。
// 模板文本**逐字承载、零解析**（用户 2026-09-17 拍板：编译器不读模板字符串）。
func (p *Parser) parseReturnStmt() (*ast.ReturnStmt, *ParseError) {
	start, err := p.consume(token.KW_RETURN, "Expected 'return'")
	if err != nil {
		return nil, err
	}
	// `return template "…" [using {…}];` 的表层拼写不变：`return` 后面本来就是
	// **表达式**（`parseExpression` → `parsePrimary` 的模板分支）。删掉的只是这条
	// **多余的语句位特判**（否则同一个构造有两条语法路径，改一处忘一处即半接线）。
	var value ast.Expr
	if !p.check(token.SEMICOLON) {
		value, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	// 尾置**投递开关**（`return template "…" [using {…}] streaming;`，用户
	// 2026-09-27 裁决 甲；标准 template_return 行）。只在值是**模板值**时收：
	// 开关说的是「答复端点怎么投递」，对 `return x;` 没有承载面 —— 那种写法
	// 留在流里 ⇒ 下面 consume(';') 当场报语法错（不静默接受、也不静默丢弃）。
	streaming := false
	if _, isTemplate := value.(*ast.TemplateValue); isTemplate && p.checkContextualWord("streaming") {
		p.advance()
		streaming = true
	}
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after return"); err != nil {
		return nil, err
	}
	s := ast.NewReturnStmt(value, streaming)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// checkTemplateMarker 报告下一个 token 是否为**上下文关键字** `template`
// 且其后再接一个字符串字面量（1 token 前瞻，无歧义：`template` 后紧跟
// STRING_LITERAL 时不可能构成合法表达式 —— 既非调用也非下标）。
func (p *Parser) checkTemplateMarker() bool {
	t := p.peek()
	return t.Type == token.IDENTIFIER && t.Lexeme == "template" && p.checkNext(token.STRING_LITERAL)
}

// checkContextualWord 报告下一个 token 是否为上下文关键字 word（只按拼写，
// 不占保留字位）。
func (p *Parser) checkContextualWord(word string) bool {
	t := p.peek()
	return t.Type == token.IDENTIFIER && t.Lexeme == word
}

// parseTemplateBody parses `template "<逐字文本>" [ using { name: <值路径>, … } ]`
// —— `template` 标记必须**在当前位置**（调用方已做 1 token 前瞻，见
// checkTemplateMarker）。**三个位点共用同一个体**：`return template`（答复端点，
// W2）· `output template`（中间消息，R-67 甲）· **表达式位**（llm message
// content，R-68 甲）。不消费结尾 `;`（由调用方按自己的语句形态收）。
func (p *Parser) parseTemplateBody() (*ast.TemplateValue, *ParseError) {
	start := p.advance() // template 标记
	tmplTok, err := p.consume(token.STRING_LITERAL, "Expected template string after 'template'")
	if err != nil {
		return nil, err
	}
	if !tmplTok.Literal.Valid || tmplTok.Literal.Kind != token.LitString {
		return nil, p.error("Invalid template string literal")
	}
	var bindings []*ast.TemplateBinding
	if p.checkContextualWord("using") {
		p.advance()
		if _, err := p.consume(token.LBRACE, "Expected '{' after 'using'"); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for !p.check(token.RBRACE) {
			if p.isAtEnd() {
				return nil, p.error("Unterminated template binding list (expected '}')")
			}
			// 名额名是**目标平台的名字**（wire `node_inputs[].name`），与
			// `yield <port> = …` 的口名、`.` 后成员同一类（标准 `member_name`
			// = name | KEYWORD）：关键字占该位合法，因为拼写来自平台而非作者
			// （语料实证：名额名 `output` 是默认出口键，最常见）。
			// 前瞻无歧义：名字后必跟 ':'。
			peek := p.peek()
			if peek.Type != token.IDENTIFIER && !token.IsKeyword(peek.Lexeme) {
				return nil, p.error("Expected template slot name")
			}
			nameTok := p.advance()
			if seen[nameTok.Lexeme] {
				return nil, p.error(fmt.Sprintf("Duplicate template slot name %q", nameTok.Lexeme))
			}
			seen[nameTok.Lexeme] = true
			if _, err := p.consume(token.COLON, "Expected ':' after template slot name"); err != nil {
				return nil, err
			}
			val, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, &ast.TemplateBinding{Name: nameTok.Lexeme, Value: val})
			if p.check(token.COMMA) {
				p.advance()
				continue
			}
			break
		}
		if _, err := p.consume(token.RBRACE, "Expected '}' after template binding list"); err != nil {
			return nil, err
		}
	}
	tr := ast.NewTemplateValue(tmplTok.Literal.S, bindings)
	tr.SetSpan(p.spanFrom(start))
	return tr, nil
}

// parseYieldStmt parses "yield [<ident> =] <expr> ;" — 命名口形态
// `yield <port> = <expr>`（coze loop/batch 输出口逐口一行），裸 `yield <expr>`
// 发布到默认口 "output"。同名口重复由 IR 层报错。
func (p *Parser) parseYieldStmt() (*ast.YieldStmt, *ParseError) {
	start, err := p.consume(token.KW_YIELD, "Expected 'yield'")
	if err != nil {
		return nil, err
	}
	var value ast.Expr
	port := ""
	if !p.check(token.SEMICOLON) {
		// 前瞻：(IDENT | 关键字) '=' → 命名口（与裸 yield 表达式区分开）。
		// 关键字口名是真实需求：coze loop/batch 的默认输出口就叫 "output"
		// （KW_OUTPUT），关键字后跟 '=' 不可能构成合法裸表达式，无歧义。
		peek := p.peek()
		if (peek.Type == token.IDENTIFIER || token.IsKeyword(peek.Lexeme)) && p.checkNext(token.ASSIGN) {
			nameTok := p.advance() // ident / 关键字口名
			if _, err := p.consume(token.ASSIGN, "Expected '=' after yield port name"); err != nil {
				return nil, err
			}
			port = nameTok.Lexeme
		}
		value, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after yield"); err != nil {
		return nil, err
	}
	var s *ast.YieldStmt
	if port != "" {
		s = ast.NewNamedYieldStmt(port, value)
	} else {
		s = ast.NewYieldStmt(value)
	}
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// parseOutputStmt parses "output ( <expr> ) ;" — one intermediate message
// published to the response stream; non-terminating.
func (p *Parser) parseOutputStmt() (*ast.OutputStmt, *ParseError) {
	start, err := p.consume(token.KW_OUTPUT, "Expected 'output'")
	if err != nil {
		return nil, err
	}
	// **只有一种语句拼写**：`output(<expr>);`。模板串形态由**表达式位**承载
	// （`parsePrimary` 的 `checkTemplateMarker` 分支，2026-09-25 用户拍板归一）——
	// 作者写 `output(template "…" using {…});`。
	//
	// 曾经存在无括号的 `output template "…" using {…};`（R-67 甲首版）；归一删掉它，
	// 因为「`template … using {…}` 是表达式」这一条一旦成立，括号里那份写法**必然**
	// 可解析 —— 要禁它就只能加一条「参数是不是模板值」的**形态守卫**，而本仓禁止
	// 形态守卫；⇒ 无括号那份才是多余的那个（同一 wire 两种拼写 = 规范型破裂）。
	if _, err := p.consume(token.LPAREN, "Expected '(' after 'output'"); err != nil {
		return nil, err
	}
	valueExpr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after output value"); err != nil {
		return nil, err
	}
	// 尾置投递开关（`output(<expr>) streaming;`，用户 2026-09-27 裁决 甲；标准
	// output_stmt 行）。上下文词：`streaming` 仍是普通标识符，只在 `)` 之后、
	// `;` 之前这一格被读成开关（它在那里不可能构成合法表达式 ⇒ 无歧义）。
	streaming := false
	if p.checkContextualWord("streaming") {
		p.advance()
		streaming = true
	}
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after output()"); err != nil {
		return nil, err
	}
	s := ast.NewOutputStmt(valueExpr, streaming)
	s.SetSpan(p.spanFrom(start))
	return s, nil
}

// loopVars 返回循环全变量名列表（单变量时 [varName]）。
func loopVars(t interface{ LoopVars() []string }) []string { return t.LoopVars() }

// loopTypes 返回循环全变量类型注解列表。
func loopTypes(t interface{ LoopTypes() []ast.TypeRef }) []ast.TypeRef { return t.LoopTypes() }

// loopSources 返回循环全源列表。
func loopSources(t interface{ LoopSources() []ast.Expr }) []ast.Expr { return t.LoopSources() }

// copyLoopParts 返回循环全变量名与类型注解的深拷贝（ForStmt/ForExpr 共用）。
func copyLoopParts(t interface {
	LoopVars() []string
	LoopTypes() []ast.TypeRef
}) ([]string, []ast.TypeRef) {
	vars := t.LoopVars()
	types := t.LoopTypes()
	cv := make([]string, len(vars))
	copy(cv, vars)
	ct := make([]ast.TypeRef, len(types))
	for i, tr := range types {
		ct[i] = deepCopyTypeRef(tr)
	}
	return cv, ct
}
