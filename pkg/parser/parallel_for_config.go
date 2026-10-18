package parser

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/token"
)

// parallel_for_config.go —— `parallel for` 配置子句的**单一定义处**。
//
// 两条语法通路（语句形 `parseParallelForStmt`（stmt.go）、值形
// `parseParallelForExpr`（expr.go））共用本解析器。2026-09-29 用户批准的语言级
// 放宽（正本 = `docs/five-step/spec/F3-error-policy-container-axis.md` §②）：
//
//	parallel for (x in items) { … }
//	parallel for (x in items, concurrency: 3) { … }
//	parallel for (x in items, on_error: keep_null) { … }
//	parallel for (x in items, concurrency: 3, on_error: keep_null) { … }
//	parallel for (x in items, on_error: keep_null, concurrency: 3) { … }   // 键序任意
//
// 三条判据（放宽的边界，不许再宽）：
//   - **子句整体可省**（先前两个键都必填、顺序固定 —— 与正本 §② 自己写的
//     「`terminate` 是缺省、与不写同产物」矛盾：源语法根本不许不写）；
//   - **只认已给的键**：未知键 / 重复键 ⇒ 拒收（不猜、不静默忽略）；
//   - **显式写的值仍要合法**：`concurrency` 必须正整数（0 与「未设」在 AST 上
//     不可分辨 ⇒ 显式 0 必须在**语法层**拦下，否则作者写 0 会静默拿到缺省）。
//
// **缺省不在本层落地**：省略 ⇒ 返回 (0, "")，即「未设」；由 lowering 按目标缺省
// 物化（唯一取值处 = `pkg/ir/parallel_for_defaults.go`）。语言层不把缺省写死
// 进 AST —— 否则「写了缺省」与「没写」不可分辨，规范形（缺省不写）就没有判据。
//
// 规范序 = `concurrency` 在前、`on_error` 在后（反编译写侧照此输出）。
//
// 与相邻规则的关系：`on_error` 的**语言闭集**（作者能写的三个名字）在
// `on_error.go`（同包，唯一实现）；本文件只负责**子句形态**，值合法性交回它。
func (p *Parser) parseParallelForConfig() (int, string, *ParseError) {
	if !p.check(token.COMMA) {
		return 0, "", nil // 子句整体可省
	}
	concurrency, mode := 0, ""
	seenConcurrency, seenOnError := false, false
	for p.check(token.COMMA) {
		if _, err := p.consume(token.COMMA, "Expected ',' before parallel-for option"); err != nil {
			return 0, "", err
		}
		key, err := p.consume(token.IDENTIFIER, "Expected 'concurrency' or 'on_error' parallel-for option")
		if err != nil {
			return 0, "", err
		}
		switch key.Lexeme {
		case "concurrency":
			if seenConcurrency {
				return 0, "", p.errorAt(key, "Duplicate 'concurrency' parallel-for option")
			}
			seenConcurrency = true
			if _, err := p.consume(token.COLON, "Expected ':' after concurrency"); err != nil {
				return 0, "", err
			}
			tok, err := p.consume(token.INT_LITERAL, "Expected integer concurrency")
			if err != nil {
				return 0, "", err
			}
			if tok.Literal.Valid && tok.Literal.Kind == token.LitInt {
				concurrency = int(tok.Literal.I)
			}
			if concurrency <= 0 {
				// 0 与「未设」在 AST 上同形 ⇒ 只能在语法层拦（正本 §②「只认已给的键」）。
				return 0, "", p.errorAt(tok, "Parallel-for concurrency must be a positive integer")
			}
		case "on_error":
			if seenOnError {
				return 0, "", p.errorAt(key, "Duplicate 'on_error' parallel-for option")
			}
			seenOnError = true
			if _, err := p.consume(token.COLON, "Expected ':' after on_error"); err != nil {
				return 0, "", err
			}
			errorMode, err := p.consume(token.IDENTIFIER, "Expected parallel-for error mode")
			if err != nil {
				return 0, "", err
			}
			if !isNcodaOnErrorMode(errorMode.Lexeme) {
				return 0, "", p.errorAt(errorMode, onErrorModeExpected)
			}
			mode = errorMode.Lexeme
		default:
			return 0, "", p.errorAt(key, "Expected 'concurrency' or 'on_error' parallel-for option")
		}
	}
	return concurrency, mode, nil
}

// errorAt records a parse error anchored at tok. `p.error` anchors at the
// *current* token, which points one past the culprit whenever the caller has
// already consumed it (all config-clause value checks are of that shape).
func (p *Parser) errorAt(tok token.Token, message string) *ParseError {
	err := &ParseError{
		Message: fmt.Sprintf("%s, got %s ('%s')", message, tok.Type, tok.Lexeme),
		Span:    token.Span{Start: tok.Location, End: tok.Location},
	}
	p.errors = append(p.errors, err)
	return err
}
