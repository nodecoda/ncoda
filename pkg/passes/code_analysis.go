// CodeAnalysisPass combines lowerability and (gated) source validation for
// Code expressions, ported 1:1 from
// src/nclang/lang/passes/code_analysis.py (316 lines). Reachable surface:
//
//	CodeAnalysisPass.Run                   :46-50
//	visit_function_def                     :53-57
//	visit_var_decl_stmt                    :59-71
//	visit_foreign_code_expr                :73-195 (lowerability :158-195,
//	                                        source visit :194)
//	visit_ident_expr                       :204-225
//	visit_field_access_expr                :227-230
//	visit_if_stmt                          :239-247
//	visit_else_if_branch                   :249-253
//	visit_for_stmt / visit_for_expr        :255-265
//	visit_while_stmt                       :267-271
//	visit_parallel_stmt                    :273-277
//	_visit_region                          :234-237
//	_field_access_has_identifier_root      :312-315
//
// Source validation (code_analysis.py:84-157) — the else-branch validates
// the embedded Python source with the standard-library ast module
// (_py_ast.parse). The Go compiler cannot run that module with zero external
// dependencies, so Task 35 ports the face with a restricted scanner
// (py_source_scan.go) over the joined literal source:
//
//   - scanner-recognized structural syntax errors → E1028
//     CODE_SOURCE_INVALID, CPython 3.12 messages verbatim (:88-95)
//   - missing `main` function → E1028 (:108-114)
//   - parameter mismatch / *args or **kwargs → E2006
//     CODE_SOURCE_CONTRACT_MISMATCH WARNING (:120-127)
//   - unprovable return keys → E2005 / mismatched keys → E2006
//     CODE_SOURCE_CONTRACT_UNVERIFIED / _MISMATCH WARNING (:138-156)
//
// The interpolation warning (:78-83) is a pure AST shape check over the
// source template parts and is implemented directly below. The declared
// contracts remain authoritative in every case, matching the reference's
// stated intent. E1028 face deviation (restricted scanner ≠ full Python
// syntax parsing) is declared in the task-35 report and PARITY.md.
//
// Traversal contract: Go's ast.Walk is exhaustive where the Python
// ProcASTVisitor base is selective; every visit_* below therefore walks its
// children manually and returns nil to prune, mirroring semantic_pass.go's
// breakContinueCheck pattern. Two parity notes:
//
// D1 (source subtree, FIXED in R1): the original brief directed the Go port
// NOT to walk the ForeignCodeExpr source subtree, reasoning that Python's
// self.visit(node.source) lands on generic_visit and never traverses
// interpolated parts. That premise is inaccurate: the base ProcASTVisitor
// defines visit_template_string (ast.py:816-820) which DOES visit
// interpolated Exprs. The reference therefore runs the IdentExpr structural
// check on an interpolated structural-code identifier inside a Code source,
// exactly like any other use. The Go port walks the source subtree
// (visitForeignCodeExpr, code_analysis.py:194) via ast.Walk's
// *TemplateString case (visitor.go:166-169), which traverses the same
// interpolated parts — full parity. This was a real parity deviation in the
// first cut (c408e0b) and is corrected here (R1); the Python oracle evidence
// is in the task-17b report.
//
// D2 (program extras): ast.Walk's *Program case also visits constDecls and
// outputs, which Python's visit_program_node (ast.py:625-638) skips. Const
// values cannot reference workflow variables (binder rejects) and outputs
// carry only type refs, so the extra IdentExpr checks are no-ops.
package passes

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/types"
)

// structuralDef records one structural Code declaration: the Code expression
// plus the region stack at the declaration point (Python tuple
// (ForeignCodeExpr, tuple[AstNodeId, ...]) in _structural_defs).
type structuralDef struct {
	code    *ast.ForeignCodeExpr
	regions []binding.AstNodeID
}

// CodeAnalysisPass implements CodeAnalysisPass.run (code_analysis.py:46-50):
// a single traversal carrying the region stack, the structural-definition
// table and the published snapshot, then ast.Walk over the whole program.
type CodeAnalysisPass struct {
	ctx            *Context                                // Python threads ctx through every visit_* call
	snap           *binding.Snapshot                       // ctx.Bindings
	exprTypes      map[binding.AstNodeID]*types.Descriptor // ctx.ExprTypes
	regions        []binding.AstNodeID
	structuralDefs map[binding.SymbolID]structuralDef
}

// Run mirrors run (code_analysis.py:46-50): initialize per-run state, then
// walk the program. State is reset so one pass instance can be reused
// across compilations.
func (p *CodeAnalysisPass) Run(program *ast.Program, ctx *Context) {
	p.ctx = ctx
	p.snap = ctx.Bindings
	p.exprTypes = ctx.ExprTypes
	p.regions = nil
	p.structuralDefs = map[binding.SymbolID]structuralDef{}
	ast.Walk(p, program)
}

// Visit mirrors the visit_* dispatch (code_analysis.py:53-277): nodes with
// a manual child walk return nil (prune); everything else returns p so the
// exhaustive Go Walk continues. The Program node is the walk root — Python's
// visit_program_node traverses the declarations, Go's Walk does the same.
func (p *CodeAnalysisPass) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.Program:
		return p

	case *ast.FunctionDef:
		// visit_function_def (code_analysis.py:53-57): push the function
		// node's ID, visit only the body (Python visits node.body), pop.
		p.pushRegion(node)
		ast.Walk(p, node.Body())
		p.popRegion()
		return nil

	case *ast.CodeFunctionDef:
		// visit_code_function_def (ast.py:643-645): visit only the body;
		// Python does not push a region for code functions (unlike
		// visit_function_def). The manual walk keeps the traversal contract
		// exact — no params/returnType drift from the exhaustive Go Walk.
		ast.Walk(p, node.Body())
		return nil

	case *ast.VarDeclStmt:
		// visit_var_decl_stmt (code_analysis.py:59-71): record the
		// structural definition when the initializer is a ForeignCodeExpr
		// with a structural output, keyed by the declared symbol
		// (ctx.stmt_symbols → DeclarationSymbols). The initializer is
		// always visited afterwards.
		if fc, ok := node.Init().(*ast.ForeignCodeExpr); ok {
			if _, isStructural := fc.Output().(*ast.CodeStructuralOutput); isStructural {
				if id, found := p.snap.NodeIDOf(node); found {
					if symID, ok := p.snap.DeclarationSymbols().Get(id); ok {
						p.structuralDefs[symID] = structuralDef{
							code:    fc,
							regions: append([]binding.AstNodeID(nil), p.regions...),
						}
					}
				}
			}
		}
		if node.Init() != nil {
			ast.Walk(p, node.Init())
		}
		return nil

	case *ast.ForeignCodeExpr:
		p.visitForeignCodeExpr(node)
		return nil

	case *ast.IdentExpr:
		// visit_ident_expr (code_analysis.py:204-225): a use of a
		// structural Code value must stay inside the region prefix where it
		// was defined, else the root materialization would escape.
		symID, ok := p.referenceSymbol(node)
		if !ok {
			return nil
		}
		def, in := p.structuralDefs[symID]
		if !in {
			return nil
		}
		use := p.regions
		// Python: use_regions[: len(definition_regions)] != definition_regions
		// (the short slice of a shorter stack is unequal by construction).
		if len(use) < len(def.regions) || !slices.Equal(use[:len(def.regions)], def.regions) {
			p.ctx.Emit(diag.CODE_INPUT_SELECTOR, diag.Error,
				fmt.Sprintf("Structural Code value '%s' cannot be materialized outside "+
					"the control-flow region where it is defined", node.Name()),
				node.Span())
			return nil
		}
		// In-region use: the whole root value is materialized (Python
		// ctx.mark_structural_code_root, bindings.py:807-812).
		if codeID, found := p.snap.NodeIDOf(def.code); found {
			p.ctx.MarkStructuralCodeRoot(codeID)
		}
		return nil

	case *ast.FieldAccessExpr:
		// visit_field_access_expr (code_analysis.py:227-230): a structural
		// field is already a physical Code output selector. Walking down to
		// its base identifier would incorrectly request root materialization.
		return nil

	case *ast.IfStmt:
		// visit_if_stmt (code_analysis.py:239-247).
		if node.Condition() != nil {
			ast.Walk(p, node.Condition())
		}
		p.visitRegion(node.ThenBody())
		for _, branch := range node.ElseIfs() {
			ast.Walk(p, branch)
		}
		p.visitRegion(node.ElseBody())
		return nil

	case *ast.ElseIfBranch:
		// visit_else_if_branch (code_analysis.py:249-253).
		if node.Condition() != nil {
			ast.Walk(p, node.Condition())
		}
		p.visitRegion(node.Body())
		return nil

	case *ast.ForStmt:
		// visit_for_stmt (code_analysis.py:255-259).
		for _, src := range node.LoopSources() {
			if src != nil {
				ast.Walk(p, src)
			}
		}
		p.visitRegion(node.Body())
		return nil

	case *ast.ForExpr:
		// visit_for_expr (code_analysis.py:261-265).
		for _, src := range node.LoopSources() {
			if src != nil {
				ast.Walk(p, src)
			}
		}
		p.visitRegion(node.Body())
		return nil

	case *ast.WhileStmt:
		// visit_while_stmt (code_analysis.py:267-271).
		if node.Condition() != nil {
			ast.Walk(p, node.Condition())
		}
		p.visitRegion(node.Body())
		return nil

	case *ast.ParallelStmt:
		// visit_parallel_stmt (code_analysis.py:273-277): each branch is
		// its own region (the branch blocks publish their symbols to the
		// enclosing scope at the join, so uses outside resolve — and then
		// fail the region prefix check).
		for _, branch := range node.Branches() {
			p.visitRegion(branch)
		}
		return nil
	}
	return p
}

// isLiteralBinding reports whether an expression is a renderable literal
// (string/int/float/bool, or a signed numeric literal). Foreign-code inputs
// bound to literals lower to canvas literal parameters (roundtrip: coze code
// nodes accept literal-bound inputs).
func isLiteralBinding(expr ast.Expr) bool {
	switch lit := expr.(type) {
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		return true
	case *ast.UnaryExpr:
		if lit.Op() != "-" {
			return false
		}
		switch lit.Operand().(type) {
		case *ast.IntLit, *ast.FloatLit:
			return true
		}
	case *ast.ArrayLit:
		// 数组字面量（含空 []）可渲染为 literal 参数（canvas Content 是
		// JSON 数组，fieldSourceToValue literal 分支）—— coze code 输入
		// 绑定常量数组（X212/S2：array<any> caption_list = []）。元素必须
		// 递归字面量（非字面量元素 → 无法降级，显式拒绝）。
		for _, el := range lit.Elements() {
			if !isLiteralBinding(el) {
				return false
			}
		}
		return true
	}
	return false
}

// visitForeignCodeExpr mirrors visit_foreign_code_expr's lowerability half
// (code_analysis.py:73-195): the interpolation check, the source
// validation (restricted scanner), then per-input selector checks, then the
// input values are
// walked, then the source subtree is walked (code_analysis.py:194) — see
// D1 above. The output contract is not walked, matching the reference (it
// only visits node.source and the input values).
func (p *CodeAnalysisPass) visitForeignCodeExpr(node *ast.ForeignCodeExpr) {
	// Interpolation check (code_analysis.py:78-83): any interpolated part
	// makes the source contract unverifiable. Pure AST shape check — no
	// Python parsing involved.
	source := node.Source()
	if source != nil {
		interpolated := false
		for _, part := range source.Parts() {
			if part.Expr != nil {
				interpolated = true
				break
			}
		}
		if interpolated {
			p.ctx.Emit(diag.CODE_SOURCE_CONTRACT_UNVERIFIED, diag.Warning,
				"Code source contains interpolation; the declared contract remains authoritative",
				source.Span())
		} else if node.Language() == "python3" && p.visitSourceValidation(node, source) {
			// Scanner-recognized syntax error: the reference emits E1028,
			// visits the Code children and skips the per-input selector
			// checks (_visit_code_children + return None).
			return
		}
		// javascript 源码不做 Python 语法扫描：受限扫描器只对 python3 有效，
		// JS 面透传、声明契约保持权威（与 checker 放行 javascript 同一决策）。
	}

	// Lowerability checks on inputs (code_analysis.py:158-193).
	for _, binding := range node.Inputs() {
		if binding.Value() == nil {
			// Defensive parity: Python's CodeInputBinding.value defaults to
			// None (code_analysis.py:160-165) and this branch fires; the Go
			// parser (parseCodeInputs) forces a non-nil value, so the branch
			// is unreachable through the pipeline — kept for 1:1 parity.
			p.ctx.Emit(diag.CODE_INPUT_SELECTOR, diag.Error,
				fmt.Sprintf("Code input '%s' has no value to lower; bind a serializable "+
					"workflow value", binding.Name()),
				binding.Span())
			continue
		}
		var valueType *types.Descriptor
		if id, ok := p.snap.NodeIDOf(binding.Value()); ok {
			valueType = p.exprTypes[id]
		}
		// Python: isinstance(value, IdentExpr) or a field access with an
		// identifier root (_field_access_has_identifier_root, :311-315).
		isSelectorExpr := false
		switch v := binding.Value().(type) {
		case *ast.IdentExpr:
			isSelectorExpr = true
		case *ast.FieldAccessExpr:
			isSelectorExpr = fieldAccessHasIdentifierRoot(v)
		}
		// Python: LambdaExpr value, or FUNCTION/VOID-typed value → the value
		// cannot be a workflow value selector. Note: through the full
		// pipeline this branch is unreachable in BOTH Python and Go for
		// Function-typed values — the type stage emits E1021 CODE_INPUT_TYPE
		// first (type_pass.py:994-996) — but it is kept for 1:1 parity and
		// locked white-box in code_analysis_test.go.
		if _, isLambda := binding.Value().(*ast.LambdaExpr); isLambda ||
			(valueType != nil && (valueType.Kind == types.Function || valueType.Kind == types.Void)) {
			p.ctx.Emit(diag.CODE_INPUT_SELECTOR, diag.Error,
				fmt.Sprintf("Code input '%s' cannot be lowered to a workflow value "+
					"selector; bind a serializable value instead", binding.Name()),
				binding.Span())
		} else if !isSelectorExpr && !isLiteralBinding(binding.Value()) {
			// 字面量输入（string/int/float/bool，含一元负号）可渲染为
			// literal 参数（roundtrip：coze code 节点参数可绑定字面量，
			// 渲染端 fieldSourceToValue 的 literal 分支）→ 放行；其余
			// 表达式无法降级 → 显式拒绝（P4：不静默丢参）。
			p.ctx.Emit(diag.CODE_INPUT_SELECTOR, diag.Error,
				fmt.Sprintf("Code input '%s' cannot be lowered directly to a workflow "+
					"selector; assign it to a typed variable produced by a workflow node first",
					binding.Name()),
				binding.Span())
		}
		// Python visits binding.value after the checks; the walk can reach
		// IdentExpr uses of structural values here (e.g. an in-region
		// structural Code fed into another Code input).
		ast.Walk(p, binding.Value())
	}

	// Python visits node.source after the input checks (code_analysis.py:194):
	// visit_template_string (ast.py:816-820) traverses interpolated Expr
	// parts, so an interpolated structural-code identifier runs the same
	// IdentExpr region check and root materialization as any other use. Go's
	// ast.Walk *TemplateString case (visitor.go:166-169) does exactly the same.
	if node.Source() != nil {
		ast.Walk(p, node.Source())
	}
}

// visitSourceValidation mirrors the reference's source-validation
// else-branch (code_analysis.py:84-157) using the restricted scanner. It
// emits the E1028/E2005/E2006 diagnostics and, on a scanner-recognized
// syntax error, calls visitCodeChildren and reports that the caller must
// skip the per-input selector checks (Python's `return None` after
// _visit_code_children).
func (p *CodeAnalysisPass) visitSourceValidation(node *ast.ForeignCodeExpr, source *ast.TemplateString) (skipSelectorChecks bool) {
	var b strings.Builder
	for _, part := range source.Parts() {
		if part.Expr == nil {
			b.WriteString(part.Text)
		}
	}
	scan := scanPythonSource(b.String())
	span := source.Span()
	if scan.syntaxError != "" {
		p.ctx.Emit(diag.CODE_SOURCE_INVALID, diag.Error,
			fmt.Sprintf("Invalid Python source: %s; fix the source template", scan.syntaxError), span)
		p.visitCodeChildren(node)
		return true
	}
	if !scan.mainFound {
		p.ctx.Emit(diag.CODE_SOURCE_INVALID, diag.Error,
			"Python source must define callable `main` matching the declared Code inputs", span)
		return false
	}

	// Parameter contract (code_analysis.py:120-127): *args/**kwargs make
	// the visible inputs unprovable (None); otherwise a set mismatch
	// between the visible parameters and the declared inputs warns E2006.
	declaredInputs := make([]string, 0, len(node.Inputs()))
	for _, binding := range node.Inputs() {
		declaredInputs = append(declaredInputs, binding.Name())
	}
	visible := "None"
	if !scan.vararg && !scan.kwarg {
		visible = pyListRepr(scan.params)
	}
	// coze 代码节点 args 惯例（平台统一形态）：main(args) 单参 + 代码经
	// `params['x']` / `params.get('x')` 按名取用输入。此时形参名与声明
	// 输入集合不等是惯例而非契约错误：
	//   - 声明输入 ⊆ 代码取用键 → 惯例匹配（输入确实被使用），不告警；
	//   - 声明输入 ⊄ 取用键 → 精确警告「声明但代码未取用」（比惯例误报
	//     更有诊断价值）。
	// 非 args 惯例（多参 / *args / **kwargs）保持原有 E2006 比对。
	// 惯例形态：形参名恰为 args + 非 vararg/kwarg。paramKeys 允许为空
	// （退化形态：声明无输入、代码也不取 params——契约仍吻合，消除误报）。
	// `main(other)` / `main(text)` 等具名形参是真实签名比对，不走惯例
	// （与 Python oracle 一致：形参集合必须等于声明输入集合）。
	argsConvention := len(scan.params) == 1 && scan.params[0] == "args" && !scan.vararg && !scan.kwarg
	if argsConvention {
		missing := make([]string, 0, len(declaredInputs))
		for _, d := range declaredInputs {
			if !pyListContains(scan.paramKeys, d) {
				missing = append(missing, d)
			}
		}
		if len(missing) > 0 {
			p.ctx.Emit(diag.CODE_SOURCE_CONTRACT_MISMATCH, diag.Warning,
				fmt.Sprintf("Declared Code inputs %s are not accessed via params in the source; the declaration remains authoritative",
					pyListRepr(missing)), span)
		}
	} else if scan.vararg || scan.kwarg || !pySetEqual(scan.params, declaredInputs) {
		p.ctx.Emit(diag.CODE_SOURCE_CONTRACT_MISMATCH, diag.Warning,
			fmt.Sprintf("Declared Code inputs %s do not match main parameters %s; the declaration remains authoritative",
				pyListRepr(declaredInputs), visible), span)
	}

	// Output contract (code_analysis.py:138-156): declared outputs are the
	// structural field names or ["result"] for a direct output; a missing
	// or unprovable return keys list warns E2005, a key-set mismatch warns
	// E2006 (both keep the declaration authoritative).
	declaredOutputs := []string{"result"}
	if structural, ok := node.Output().(*ast.CodeStructuralOutput); ok {
		declaredOutputs = make([]string, 0, len(structural.Fields()))
		for _, field := range structural.Fields() {
			declaredOutputs = append(declaredOutputs, field.Name())
		}
	}
	provable := len(scan.returns) > 0
	for _, r := range scan.returns {
		if r.keys == nil {
			provable = false
			break
		}
	}
	if !provable {
		p.ctx.Emit(diag.CODE_SOURCE_CONTRACT_UNVERIFIED, diag.Warning,
			"Python return keys cannot be proven statically; the declaration remains authoritative", span)
	} else {
		mismatch := false
		for _, r := range scan.returns {
			if !pySetEqual(r.keys, declaredOutputs) {
				mismatch = true
				break
			}
		}
		if mismatch {
			p.ctx.Emit(diag.CODE_SOURCE_CONTRACT_MISMATCH, diag.Warning,
				fmt.Sprintf("Declared Code outputs %s do not match visible return keys %s; the declaration remains authoritative",
					pyListRepr(declaredOutputs), pyVisibleOutputsRepr(scan.returns)), span)
		}
	}
	return false
}

// visitCodeChildren mirrors _visit_code_children (code_analysis.py:195-200):
// visit the binding values and the source template for lowerability when
// source validation already emitted an error — the per-input selector
// checks are skipped.
func (p *CodeAnalysisPass) visitCodeChildren(node *ast.ForeignCodeExpr) {
	for _, binding := range node.Inputs() {
		if binding.Value() != nil {
			ast.Walk(p, binding.Value())
		}
	}
	if node.Source() != nil {
		ast.Walk(p, node.Source())
	}
}

// pyStrRepr renders a Go string the way CPython repr() renders a str:
// single quotes by default, double quotes only when the value contains a
// single quote and no double quote; backslash, newline, tab and carriage
// return get short escapes; other control characters and non-printable
// chars below U+0100 render as \xNN; non-printable non-ASCII renders as
// \uNNNN / \UNNNNNNNN; printable non-ASCII stays literal. Mirrors the
// lowering package's pythonRepr implementation (which cannot be imported
// here — lowering depends on passes), which was itself locked empirically
// against .venv/bin/python repr() (task-23a-report "R1 review fixes").
// The E2006/E2005 message contract uses these reprs verbatim.
func pyStrRepr(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = '"'
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte(quote)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			switch {
			case r == rune(quote):
				b.WriteByte('\\')
				b.WriteRune(r)
			case r < 0x20 || r == 0x7f:
				fmt.Fprintf(&b, `\x%02x`, r)
			case !unicode.IsPrint(r) && r < 0x100:
				fmt.Fprintf(&b, `\x%02x`, r)
			case !unicode.IsPrint(r) && r < 0x10000:
				fmt.Fprintf(&b, `\u%04x`, r)
			case !unicode.IsPrint(r):
				fmt.Fprintf(&b, `\U%08x`, r)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// pyListRepr renders []string as a CPython list repr: ['a', 'b'].
// pyListContains 报告 item 是否在 items 中（线性查找，列表小）。
func pyListContains(items []string, item string) bool {
	for _, it := range items {
		if it == item {
			return true
		}
	}
	return false
}

func pyListRepr(items []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(pyStrRepr(it))
	}
	b.WriteByte(']')
	return b.String()
}

// pyVisibleOutputsRepr renders the per-return key classification the way
// CPython repr() renders the reference's visible_outputs list (a list of
// list[str] | None): [['result'], None].
func pyVisibleOutputsRepr(returns []pyReturn) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, r := range returns {
		if i > 0 {
			b.WriteString(", ")
		}
		if r.keys == nil {
			b.WriteString("None")
		} else {
			b.WriteString(pyListRepr(r.keys))
		}
	}
	b.WriteByte(']')
	return b.String()
}

// pySetEqual compares two string lists as sets, matching Python's
// set(visible) != set(declared) semantics (duplicates collapse: Python
// `def main(a, a)` parses and set(['a','a']) == {'a'} — distinct from
// target_validation's len-sensitive sameStringSet).
func pySetEqual(a, b []string) bool {
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, x := range a {
		seen[x] = struct{}{}
	}
	for _, y := range b {
		if _, ok := seen[y]; !ok {
			return false
		}
	}
	return len(seen) == len(b)
}

// referenceSymbol resolves the snapshot symbol bound to an expression node
// (Python ctx.expr_symbols / ctx.symbol_id, code_analysis.py:205-206).
func (p *CodeAnalysisPass) referenceSymbol(n ast.Node) (binding.SymbolID, bool) {
	id, ok := p.snap.NodeIDOf(n)
	if !ok {
		return 0, false
	}
	return p.snap.ReferenceSymbols().Get(id)
}

// pushRegion appends n's AstNodeID to the region stack (Python
// self._regions.append(ctx.ast_id(...))). Binder enumeration is total over
// FunctionDef/CodeFunctionDef/Block/If/For/While/Parallel (binder.go:145-216),
// so the lookup always succeeds; a failure here would silently skip a frame
// while popRegion still pops, corrupting the region stack — the guard is
// defensive only.
func (p *CodeAnalysisPass) pushRegion(n ast.Node) {
	if id, ok := p.snap.NodeIDOf(n); ok {
		p.regions = append(p.regions, id)
	}
}

// popRegion pops the region pushed by the matching pushRegion.
func (p *CodeAnalysisPass) popRegion() {
	if len(p.regions) > 0 {
		p.regions = p.regions[:len(p.regions)-1]
	}
}

// visitRegion mirrors _visit_region (code_analysis.py:234-237): push the
// block's ID, walk the block, pop.
func (p *CodeAnalysisPass) visitRegion(block *ast.Block) {
	if block == nil {
		return
	}
	p.pushRegion(block)
	ast.Walk(p, block)
	p.popRegion()
}

// fieldAccessHasIdentifierRoot mirrors _field_access_has_identifier_root
// (code_analysis.py:312-315): whether the base chain of a field access
// bottoms out in an identifier.
func fieldAccessHasIdentifierRoot(node *ast.FieldAccessExpr) bool {
	current := node.Obj()
	for {
		switch v := current.(type) {
		case *ast.FieldAccessExpr:
			current = v.Obj()
		case *ast.IndexExpr:
			// 数组索引不打断引用链：v.node.arr[0].field 的 root 仍是
			// workflow 节点输出标识符（X90 等 14 例：code 输入绑定
			// all_timelines[0].end 被误判非 selector → CODE_INPUT_SELECTOR）。
			current = v.Obj()
		default:
			goto done
		}
	}
done:
	_, ok := current.(*ast.IdentExpr)
	return ok
}
