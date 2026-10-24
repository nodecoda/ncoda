// This file ports the top-level declaration productions and the type
// reference family from src/nclang/lang/parser.py (declarations & type
// references sections), returning Task 1 AST nodes. Errors follow the
// skeleton's explicit (*ParseError) return convention: each production
// returns (node, err), callers propagate, and parseProgram's declaration
// loop is the single recovery point (Python: try/except ParseError +
// _synchronize). All error messages and span assignments mirror the Python
// reference, including the optional trailing semicolons (semicolon_opt).
package parser

import (
	"fmt"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/lexer"
	"github.com/nodecoda/ncoda/pkg/token"
)

// ── Top-level declarations (parser.py: declarations section) ──

// parseConversationDecl parses "@conversation <type> <name> [= <default>] ;"
// (parser.py:_parse_conversation_decl). The default expression, when
// present, is parsed by parseExpression.
func (p *Parser) parseConversationDecl() (*ast.ConversationVarDecl, *ParseError) {
	start, err := p.consume(token.AT_CONVERSATION, "Expected '@conversation'")
	if err != nil {
		return nil, err
	}
	typeRef, err := p.parseTypeRef()
	if err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected variable name after type")
	if err != nil {
		return nil, err
	}
	var defaultExpr ast.Expr
	if _, ok := p.match(token.ASSIGN); ok {
		defaultExpr, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after conversation variable declaration"); err != nil {
		return nil, err
	}
	decl := ast.NewConversationVarDecl(nameTok.Lexeme, typeRef, defaultExpr)
	decl.SetSpan(p.spanFrom(start))
	return decl, nil
}

// parseConstDecl parses "const <name> = <literal> ;" (parser.py:
// _parse_const_decl). The value must be a literal; parseConstValue rejects
// identifiers, calls, and binary expressions.
// parseFieldList parses a record field list: { <type> <name>; ... }.
// Shared by type declarations and subflow input/output contracts.
func (p *Parser) parseFieldList(openMsg, closeMsg string) ([]*ast.FieldDecl, *ParseError) {
	if _, err := p.consume(token.LBRACE, openMsg); err != nil {
		return nil, err
	}
	var fields []*ast.FieldDecl
	for !p.check(token.RBRACE) && !p.isAtEnd() {
		fieldType, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		fieldName, err := p.consume(token.IDENTIFIER, "Expected field name")
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.SEMICOLON, "Expected ';' after field"); err != nil {
			return nil, err
		}
		fields = append(fields, ast.NewFieldDecl(fieldName.Lexeme, fieldType))
	}
	if _, err := p.consume(token.RBRACE, closeMsg); err != nil {
		return nil, err
	}
	return fields, nil
}

// parseSubflowDecl parses a top-level subworkflow declaration:
// subflow <name> = ("<workflowId>", "<version>") { input: { ... }, output: { ... } };
// The identity tuple carries the platform subworkflow id and version (both
// stable; no binding table — SUBWORKFLOW-DECLARATION). The contract block
// declares the input and output record shapes used for type checking.
func (p *Parser) parseSubflowDecl() (*ast.SubflowDecl, *ParseError) {
	start, err := p.consume(token.KW_SUBFLOW, "Expected 'subflow'")
	if err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected subflow name")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.ASSIGN, "Expected '=' after subflow name"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LPAREN, "Expected '(' after '='"); err != nil {
		return nil, err
	}
	idTok, err := p.consume(token.STRING_LITERAL, "Expected workflow id string")
	if err != nil {
		return nil, err
	}
	id := ""
	if idTok.Literal.Valid && idTok.Literal.Kind == token.LitString {
		id = idTok.Literal.S
	}
	if id == "" {
		return nil, p.error("Subflow workflow id must not be empty")
	}
	if _, err := p.consume(token.COMMA, "Expected ',' after workflow id"); err != nil {
		return nil, err
	}
	verTok, err := p.consume(token.STRING_LITERAL, "Expected workflow version string")
	if err != nil {
		return nil, err
	}
	ver := ""
	if verTok.Literal.Valid && verTok.Literal.Kind == token.LitString {
		ver = verTok.Literal.S
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after version"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LBRACE, "Expected '{' before contracts"); err != nil {
		return nil, err
	}
	var inputFields, outputFields []*ast.FieldDecl
	seen := map[string]bool{}
	for !p.check(token.RBRACE) && !p.isAtEnd() {
		var label string
		switch p.peek().Type {
		case token.IDENTIFIER:
			label = p.advance().Lexeme
		case token.KW_OUTPUT: // "output" 是保留字, 契约标签需兼容
			label = p.advance().Lexeme
		default:
			return nil, p.error("Expected 'input' or 'output' contract")
		}
		if _, err := p.consume(token.COLON, "Expected ':' after contract label"); err != nil {
			return nil, err
		}
		fields, ferr := p.parseFieldList("Expected '{' to open "+label+" contract", "Expected '}' to close "+label+" contract")
		if ferr != nil {
			return nil, ferr
		}
		switch label {
		case "input":
			if seen["input"] {
				return nil, p.error("Duplicate 'input' contract in subflow declaration")
			}
			seen["input"] = true
			inputFields = fields
		case "output":
			if seen["output"] {
				return nil, p.error("Duplicate 'output' contract in subflow declaration")
			}
			seen["output"] = true
			outputFields = fields
		default:
			return nil, p.error("Expected 'input' or 'output' contract, got '" + label + "'")
		}
		p.match(token.COMMA)
	}
	if !seen["output"] {
		return nil, p.error("Subflow declaration requires an 'output' contract")
	}
	if _, err := p.consume(token.RBRACE, "Expected '}' after subflow contracts"); err != nil {
		return nil, err
	}
	p.match(token.SEMICOLON) // optional trailing semicolon
	decl := ast.NewSubflowDecl(nameTok.Lexeme, id, ver, inputFields, outputFields)
	decl.SetSpan(p.spanFrom(start))
	return decl, nil
}

// parseImportDecl parses a capability import declaration:
// import "provider";  (P3.5). The provider slug becomes the namespace of the
// qualified capability call p.op({...}) — the only capability-call spelling
// (the legacy tool(name, action, {...}) builtin was deleted, zero compat).
func (p *Parser) parseImportDecl() (*ast.ImportDecl, *ParseError) {
	start, err := p.consume(token.KW_IMPORT, "Expected 'import'")
	if err != nil {
		return nil, err
	}
	provider, err := p.consume(token.STRING_LITERAL, "Expected provider string after 'import'")
	if err != nil {
		return nil, err
	}
	providerValue := ""
	if provider.Literal.Valid && provider.Literal.Kind == token.LitString {
		providerValue = provider.Literal.S
	}
	if providerValue == "" {
		return nil, p.error("Import provider must not be empty")
	}
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after import declaration"); err != nil {
		return nil, err
	}
	decl := ast.NewImportDecl(providerValue)
	decl.SetSpan(p.spanFrom(start))
	return decl, nil
}

func (p *Parser) parseConstDecl() (*ast.ConstDecl, *ParseError) {
	start, err := p.consume(token.KW_CONST, "Expected 'const'")
	if err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected constant name after 'const'")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.ASSIGN, "Expected '=' after constant name"); err != nil {
		return nil, err
	}
	value, err := p.parseConstValue()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.SEMICOLON, "Expected ';' after const declaration"); err != nil {
		return nil, err
	}
	decl := ast.NewConstDecl(nameTok.Lexeme, value)
	decl.SetSpan(p.spanFrom(start))
	return decl, nil
}

// parseConstValue parses a literal const value (parser.py:
// _parse_const_value). Allowed: string, int, float, bool, null, template
// string. Anything else is rejected with the Python error message.
func (p *Parser) parseConstValue() (ast.Expr, *ParseError) {
	switch p.peek().Type {
	case token.STRING_LITERAL, token.INT_LITERAL, token.FLOAT_LITERAL, token.BOOL_LITERAL, token.NULL_LITERAL:
		expr, err := p.parseLiteralPrimary()
		if err != nil {
			return nil, err
		}
		// Python's _parse_const_value builds plain literals without a span;
		// only template strings keep theirs.
		expr.SetSpan(ast.Span{})
		return expr, nil
	case token.TEMPLATE_STRING:
		tok := p.advance()
		return p.parseTemplateString(tok)
	default:
		// Python's _parse_const_value reports non-literals with its own
		// message (identifiers, calls, and binary expressions are not
		// allowed in const values).
		return nil, p.error(fmt.Sprintf(
			"Const value must be a literal (string, int, float, bool, null), got %s", p.peek().Type))
	}
}

// parseTypeDecl parses "type <name> = { <type> <field>; ... } [;]"
// (parser.py:_parse_type_decl). The trailing semicolon after '}' is
// optional (semicolon_opt).
func (p *Parser) parseTypeDecl() (*ast.TypeDecl, *ParseError) {
	start, err := p.consume(token.KW_TYPE, "Expected 'type'")
	if err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected type name")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.ASSIGN, "Expected '=' after type name"); err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LBRACE, "Expected '{'"); err != nil {
		return nil, err
	}
	var fields []*ast.FieldDecl
	for !p.check(token.RBRACE) && !p.isAtEnd() {
		fieldType, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		fieldName, err := p.consume(token.IDENTIFIER, "Expected field name")
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.SEMICOLON, "Expected ';' after field"); err != nil {
			return nil, err
		}
		fields = append(fields, ast.NewFieldDecl(fieldName.Lexeme, fieldType))
	}
	if _, err := p.consume(token.RBRACE, "Expected '}' after type fields"); err != nil {
		return nil, err
	}
	p.match(token.SEMICOLON) // optional trailing semicolon
	decl := ast.NewTypeDecl(nameTok.Lexeme, fields)
	decl.SetSpan(p.spanFrom(start))
	return decl, nil
}

// parseEnumDecl parses "enum <name> { <member> [, <member> ... [,]] } [;]"
// (parser.py:_parse_enum_decl). An empty member list is rejected; a
// trailing comma before '}' is allowed.
func (p *Parser) parseEnumDecl() (*ast.EnumDecl, *ParseError) {
	start, err := p.consume(token.KW_ENUM, "Expected 'enum'")
	if err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected enum name")
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.LBRACE, "Expected '{' after enum name"); err != nil {
		return nil, err
	}
	if p.check(token.RBRACE) {
		return nil, p.error("Enum declaration requires at least one member")
	}
	var members []*ast.EnumMemberDecl
	for {
		memberTok, err := p.consume(token.IDENTIFIER, "Expected enum member name")
		if err != nil {
			return nil, err
		}
		member := ast.NewEnumMemberDecl(memberTok.Lexeme)
		member.SetSpan(ast.Span{Start: memberTok.Location, End: memberTok.Location})
		members = append(members, member)

		if _, ok := p.match(token.COMMA); !ok {
			break
		}
		if p.check(token.RBRACE) {
			break
		}
	}
	if _, err := p.consume(token.RBRACE, "Expected '}' after enum members"); err != nil {
		return nil, err
	}
	p.match(token.SEMICOLON) // optional trailing semicolon
	decl := ast.NewEnumDecl(nameTok.Lexeme, members)
	decl.SetSpan(p.spanFrom(start))
	return decl, nil
}

// parseFunctionDef parses "function <name>(<params>) [-> <type>] <block>"
// (parser.py:_parse_function_def). The return type is optional.
func (p *Parser) parseFunctionDef() (*ast.FunctionDef, *ParseError) {
	start, err := p.consume(token.KW_FUNCTION, "Expected 'function'")
	if err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected function name")
	if err != nil {
		return nil, err
	}
	params, err := p.parseParamList()
	if err != nil {
		return nil, err
	}
	var returnType ast.TypeRef
	if _, ok := p.match(token.ARROW); ok {
		returnType, err = p.parseTypeRef()
		if err != nil {
			return nil, err
		}
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	fn := ast.NewFunctionDef(nameTok.Lexeme, params, returnType, body)
	fn.SetSpan(p.spanFrom(start))
	return fn, nil
}

// parseCodeFunctionDef parses "code <name>(<params>) -> <type> <block>"
// (parser.py:_parse_code_function_def). Unlike functions, the return type
// is required.
func (p *Parser) parseCodeFunctionDef() (*ast.CodeFunctionDef, *ParseError) {
	start, err := p.consume(token.KW_CODE, "Expected 'code'")
	if err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected Code function name")
	if err != nil {
		return nil, err
	}
	params, err := p.parseParamList()
	if err != nil {
		return nil, err
	}
	if _, err := p.consume(token.ARROW, "Expected '->' before Code function return type"); err != nil {
		return nil, err
	}
	returnType, err := p.parseTypeRef()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	fn := ast.NewCodeFunctionDef(nameTok.Lexeme, params, returnType, body)
	fn.SetSpan(p.spanFrom(start))
	return fn, nil
}

// parseParamList parses "(<param>, ...)" (parser.py:_parse_param_list).
func (p *Parser) parseParamList() ([]*ast.ParamDecl, *ParseError) {
	if _, err := p.consume(token.LPAREN, "Expected '('"); err != nil {
		return nil, err
	}
	var params []*ast.ParamDecl
	if !p.check(token.RPAREN) {
		first, err := p.parseParam()
		if err != nil {
			return nil, err
		}
		params = append(params, first)
		for {
			if _, ok := p.match(token.COMMA); !ok {
				break
			}
			next, err := p.parseParam()
			if err != nil {
				return nil, err
			}
			params = append(params, next)
		}
	}
	if _, err := p.consume(token.RPAREN, "Expected ')' after parameters"); err != nil {
		return nil, err
	}
	return params, nil
}

// parseParam parses "<type> <name> [= <default>]" (parser.py:_parse_param).
func (p *Parser) parseParam() (*ast.ParamDecl, *ParseError) {
	typeRef, err := p.parseTypeRef()
	if err != nil {
		return nil, err
	}
	nameTok, err := p.consume(token.IDENTIFIER, "Expected parameter name")
	if err != nil {
		return nil, err
	}
	var defaultExpr ast.Expr
	if _, ok := p.match(token.ASSIGN); ok {
		defaultExpr, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	return ast.NewParamDecl(nameTok.Lexeme, typeRef, defaultExpr), nil
}

// ── Type references (parser.py: type references section) ──

// parseTypeRef parses a type reference (parser.py:_parse_type_ref):
// primitives -> PrimTypeRef, file (with optional <constraints>) ->
// FileTypeRef, map<K,V> -> MapTypeRef, array<T>/T[] -> ArrayTypeRef,
// TypeName -> NamedTypeRef, plus the optional T?/T[] suffix loop.
func (p *Parser) parseTypeRef() (ast.TypeRef, *ParseError) {
	tok := p.peek()
	var ref ast.TypeRef

	if p.check(token.KW_STRING, token.KW_INT, token.KW_FLOAT, token.KW_BOOL, token.KW_VOID, token.KW_OBJECT, token.KW_ANY) {
		p.advance()
		ref = ast.NewPrimTypeRef(tok.Lexeme)
		ref.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
	} else if _, ok := p.match(token.KW_FILE); ok {
		constraints := ast.FileTypeConstraints{}
		if _, ok := p.match(token.LESS); ok {
			fileTypes, err := p.parseFileConstraintList("Expected file type")
			if err != nil {
				return nil, err
			}
			var fileExts, fileUploads []string
			if _, ok := p.match(token.SEMICOLON); ok {
				fileExts, err = p.parseFileExtensions()
				if err != nil {
					return nil, err
				}
				if _, ok := p.match(token.SEMICOLON); ok {
					fileUploads, err = p.parseFileConstraintList("Expected upload method")
					if err != nil {
						return nil, err
					}
				}
			}
			if _, err := p.consume(token.GREATER, "Expected '>' to close file type parameters"); err != nil {
				return nil, err
			}
			constraints = ast.FileTypeConstraints{Types: fileTypes, Extensions: fileExts, UploadMethods: fileUploads}
		}
		ref = ast.NewFileTypeRef(constraints)
		ref.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
	} else if _, ok := p.match(token.KW_MAP); ok {
		if _, err := p.consume(token.LESS, "Expected '<' after 'map'"); err != nil {
			return nil, err
		}
		keyTok, err := p.consume(token.KW_STRING, "map keys must be string")
		if err != nil {
			return nil, err
		}
		keyRef := ast.NewPrimTypeRef("string")
		keyRef.SetSpan(ast.Span{Start: keyTok.Location, End: keyTok.Location})
		if _, err := p.consume(token.COMMA, "Expected ',' in map type"); err != nil {
			return nil, err
		}
		valRef, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.GREATER, "Expected '>' to close map type"); err != nil {
			return nil, err
		}
		ref = ast.NewMapTypeRef(keyRef, valRef)
		ref.SetSpan(p.spanFrom(tok))
	} else if _, ok := p.match(token.KW_ARRAY); ok {
		if _, err := p.consume(token.LESS, "Expected '<' after 'array'"); err != nil {
			return nil, err
		}
		elemRef, err := p.parseTypeRef()
		if err != nil {
			return nil, err
		}
		if _, err := p.consume(token.GREATER, "Expected '>' to close array type"); err != nil {
			return nil, err
		}
		ref = ast.NewArrayTypeRef(elemRef)
		ref.SetSpan(p.spanFrom(tok))
	} else if _, ok := p.match(token.IDENTIFIER); ok {
		ref = ast.NewNamedTypeRef(tok.Lexeme)
		ref.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
	} else {
		return nil, p.error("Expected type name")
	}

	// Suffix loop: T? (optional) and T[] (array), applied left to right.
	for {
		if _, ok := p.match(token.QUESTION); ok {
			optionalRef := ast.NewOptionalTypeRef(ref)
			optionalRef.SetSpan(p.spanFrom(tok))
			ref = optionalRef
		} else if _, ok := p.match(token.LBRACKET); ok {
			if _, err := p.consume(token.RBRACKET, "Expected ']' in array type"); err != nil {
				return nil, err
			}
			arrayRef := ast.NewArrayTypeRef(ref)
			arrayRef.SetSpan(p.spanFrom(tok))
			ref = arrayRef
		} else {
			break
		}
	}
	return ref, nil
}

// parseFileConstraintList parses a comma-separated identifier list for file
// type constraints (parser.py:_parse_file_constraint_list).
func (p *Parser) parseFileConstraintList(message string) ([]string, *ParseError) {
	var values []string
	if p.check(token.SEMICOLON, token.GREATER) {
		return values, nil
	}
	first, err := p.consume(token.IDENTIFIER, message)
	if err != nil {
		return nil, err
	}
	values = append(values, first.Lexeme)
	for {
		if _, ok := p.match(token.COMMA); !ok {
			break
		}
		next, err := p.consume(token.IDENTIFIER, message)
		if err != nil {
			return nil, err
		}
		values = append(values, next.Lexeme)
	}
	return values, nil
}

// parseFileExtensions parses ".ext, .ext, ..." file extensions, normalizing
// each to a dot-prefixed string (parser.py:_parse_file_extensions).
func (p *Parser) parseFileExtensions() ([]string, *ParseError) {
	var values []string
	if p.check(token.SEMICOLON, token.GREATER) {
		return values, nil
	}
	for {
		if _, err := p.consume(token.DOT, "Expected '.' before file extension"); err != nil {
			return nil, err
		}
		ext, err := p.consume(token.IDENTIFIER, "Expected extension name after '.'")
		if err != nil {
			return nil, err
		}
		values = append(values, "."+ext.Lexeme)
		if _, ok := p.match(token.COMMA); !ok {
			break
		}
	}
	return values, nil
}

// parseLiteralPrimary parses a string/int/float/bool/null literal token into
// the corresponding literal expression node. Shared by parseConstValue and
// the interim expression parser (expr.go); error messages mirror parser.py.
func (p *Parser) parseLiteralPrimary() (ast.Expr, *ParseError) {
	tok := p.peek()
	var expr ast.Expr
	switch tok.Type {
	case token.STRING_LITERAL:
		p.advance()
		if !tok.Literal.Valid || tok.Literal.Kind != token.LitString {
			return nil, p.error("Invalid string literal token")
		}
		expr = ast.NewStringLit(tok.Literal.S)
	case token.INT_LITERAL:
		p.advance()
		if !tok.Literal.Valid || tok.Literal.Kind != token.LitInt {
			return nil, p.error("Invalid integer literal token")
		}
		expr = ast.NewIntLit(tok.Literal.I)
	case token.FLOAT_LITERAL:
		p.advance()
		if !tok.Literal.Valid || tok.Literal.Kind != token.LitFloat {
			return nil, p.error("Invalid float literal token")
		}
		expr = ast.NewFloatLit(tok.Literal.F)
	case token.BOOL_LITERAL:
		p.advance()
		if !tok.Literal.Valid || tok.Literal.Kind != token.LitBool {
			return nil, p.error("Invalid boolean literal token")
		}
		expr = ast.NewBoolLit(tok.Literal.B)
	case token.NULL_LITERAL:
		p.advance()
		expr = ast.NewNullLit()
	default:
		return nil, p.error("Expected expression")
	}
	expr.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
	return expr, nil
}

// parseTemplateString parses a TEMPLATE_STRING token into a TemplateString
// node, splitting ${expr} interpolations (parser.py:_parse_template_string).
// Parts always start and end with a literal string, mirroring ast.py.
func (p *Parser) parseTemplateString(tok token.Token) (*ast.TemplateString, *ParseError) {
	raw := ""
	if tok.Literal.Valid && tok.Literal.Kind == token.LitString {
		raw = tok.Literal.S
	}
	var parts []ast.TemplatePart
	pos := 0
	for pos < len(raw) {
		idx := strings.Index(raw[pos:], "${")
		if idx == -1 {
			parts = append(parts, ast.TemplatePart{Text: raw[pos:]})
			break
		}
		idx += pos
		if idx > pos {
			parts = append(parts, ast.TemplatePart{Text: raw[pos:idx]})
		}
		depth := 1
		end := idx + 2
		for end < len(raw) && depth > 0 {
			switch raw[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
			end++
		}
		if depth != 0 {
			p.error("Unterminated template expression")
			parts = append(parts, ast.TemplatePart{Text: raw[idx+2:]})
			break
		}
		expr := p.parseTemplateExpr(raw[idx+2:end-1], tok.Location)
		parts = append(parts, ast.TemplatePart{Expr: expr})
		pos = end
	}
	if len(parts) == 0 || parts[0].Expr != nil {
		parts = append([]ast.TemplatePart{{Text: ""}}, parts...)
	}
	if last := parts[len(parts)-1]; last.Expr != nil {
		parts = append(parts, ast.TemplatePart{Text: ""})
	}
	e := ast.NewTemplateString(parts)
	e.SetSpan(ast.Span{Start: tok.Location, End: tok.Location})
	return e, nil
}

// parseTemplateExpr parses one ${...} expression string with a sub-parser
// (parser.py:_parse_template_expr). Failures are recorded on the main
// parser and fall back to an empty string literal, matching Python.
func (p *Parser) parseTemplateExpr(source string, baseLoc token.Location) ast.Expr {
	subTokens := lexer.New(source, "").Tokenize()
	if len(subTokens) == 0 {
		p.error("Empty template expression")
		e := ast.NewStringLit("")
		e.SetSpan(ast.Span{Start: baseLoc, End: baseLoc})
		return e
	}
	sub := New(subTokens)
	result, err := sub.parseExpression()
	if err != nil {
		p.errors = append(p.errors, err)
		e := ast.NewStringLit("")
		e.SetSpan(ast.Span{Start: baseLoc, End: baseLoc})
		return e
	}
	p.errors = append(p.errors, sub.Errors()...)
	// Deliberately NO cursor re-check after the sub-expression (unlike a
	// strict-EOF parse): parser.py's _parse_template_expr returns after
	// _parse_expression without confirming sub.is_at_end(), so trailing
	// tokens inside ${...} are silently dropped (e.g. `${1 2}` yields parts
	// ["", IntLit(1), ""] with no error). This is oracle-verified byte
	// parity — do not "fix" it by adding a sub.isAtEnd() error without
	// re-diffing against parser.py (see TestTemplateTrailingTokensMatchPython
	// and the Task 5 report, handoff item 3).
	return result
}

// parseAgentDecl parses the @mode agent application declaration:
//
//	@agent {
//	    model: "provider/model",
//	    instruction: "system persona",
//	    max_iteration: 10,
//	    tools: ["weather"]
//	}
//
// The block body is a map literal (parser.py map-literal production); the
// four fields are extracted here so semantic errors surface at parse time.
// Unknown keys are rejected; tools entries must be string literals.
func (p *Parser) parseAgentDecl() (*ast.AgentDecl, *ParseError) {
	start, err := p.consume(token.AT_AGENT, "Expected '@agent'")
	if err != nil {
		return nil, err
	}
	ml, perr := p.parseMapLiteral()
	if perr != nil {
		return nil, perr
	}
	var model, instruction, strategy string
	maxIteration := 0
	memory := false
	memoryWindow := 0
	var tools []string
	var knowledgeDatasetIDs []string
	knowledgeTopK := 0
	seen := map[string]bool{}
	for _, e := range ml.Entries() {
		keyName, ok := agentMapKeyName(e.Key)
		if !ok {
			return nil, p.error("Expected identifier or string key in @agent block")
		}
		switch keyName {
		case "model":
			sl, ok := e.Value.(*ast.StringLit)
			if !ok {
				return nil, p.error("Expected string for model")
			}
			model = sl.Value()
		case "instruction":
			sl, ok := e.Value.(*ast.StringLit)
			if !ok {
				return nil, p.error("Expected string for instruction")
			}
			instruction = sl.Value()
		case "strategy":
			sl, ok := e.Value.(*ast.StringLit)
			if !ok {
				return nil, p.error("Expected string for strategy")
			}
			strategy = sl.Value()
		case "max_iteration":
			il, ok := e.Value.(*ast.IntLit)
			if !ok {
				return nil, p.error("Expected integer for max_iteration")
			}
			maxIteration = int(il.Value())
		case "memory":
			bl, ok := e.Value.(*ast.BoolLit)
			if !ok {
				return nil, p.error("Expected boolean for memory")
			}
			memory = bl.Value()
		case "memory_window":
			il, ok := e.Value.(*ast.IntLit)
			if !ok {
				return nil, p.error("Expected integer for memory_window")
			}
			if il.Value() < 0 {
				return nil, p.error("memory_window must be >= 0")
			}
			memoryWindow = int(il.Value())
		case "tools":
			al, ok := e.Value.(*ast.ArrayLit)
			if !ok {
				return nil, p.error("Expected list for tools")
			}
			for _, elem := range al.Elements() {
				sl, ok := elem.(*ast.StringLit)
				if !ok {
					return nil, p.error("Expected string tool alias in tools list")
				}
				tools = append(tools, sl.Value())
			}
		case "knowledge":
			// 对齐参考目标 agent-chat 数据集检索:knowledge: {dataset_ids: [...],
			// top_k: n}。空 dataset_ids 视为未配置。
			kl, ok := e.Value.(*ast.MapLit)
			if !ok {
				return nil, p.error("Expected map for knowledge")
			}
			var ds []string
			topK := 0
			for _, e2 := range kl.Entries() {
				k2, ok := agentMapKeyName(e2.Key)
				if !ok {
					return nil, p.error("Expected identifier or string key in knowledge block")
				}
				switch k2 {
				case "dataset_ids":
					al, ok := e2.Value.(*ast.ArrayLit)
					if !ok {
						return nil, p.error("Expected list for dataset_ids")
					}
					for _, elem := range al.Elements() {
						sl, ok := elem.(*ast.StringLit)
						if !ok {
							return nil, p.error("Expected string dataset id in dataset_ids")
						}
						ds = append(ds, sl.Value())
					}
				case "top_k":
					il, ok := e2.Value.(*ast.IntLit)
					if !ok {
						return nil, p.error("Expected integer for top_k")
					}
					if il.Value() < 0 {
						return nil, p.error("top_k must be >= 0")
					}
					topK = int(il.Value())
				default:
					return nil, p.error(fmt.Sprintf("Unknown knowledge field %q (want dataset_ids, top_k)", k2))
				}
			}
			if len(ds) > 0 {
				knowledgeDatasetIDs = ds
				knowledgeTopK = topK
			}
		default:
			return nil, p.error(fmt.Sprintf("Unknown @agent field %q (want model, instruction, strategy, max_iteration, memory, memory_window, tools, knowledge)", keyName))
		}
		seen[keyName] = true
	}
	if !seen["model"] {
		return nil, p.error("Missing required @agent field: model")
	}
	decl := ast.NewAgentDecl(model, instruction, strategy, maxIteration, memory, memoryWindow, tools)
	if len(knowledgeDatasetIDs) > 0 {
		decl.SetKnowledge(knowledgeDatasetIDs, knowledgeTopK)
	}
	decl.SetSpan(p.spanFrom(start))
	return decl, nil
}

// agentMapKeyName returns the key name of an @agent field entry: an
// identifier (model:) or a string literal ("model":).
func agentMapKeyName(k ast.Expr) (string, bool) {
	switch kk := k.(type) {
	case *ast.IdentExpr:
		return kk.Name(), true
	case *ast.StringLit:
		return kk.Value(), true
	}
	return "", false
}
