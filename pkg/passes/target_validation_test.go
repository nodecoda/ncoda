package passes

// Ports of the target-validation contract from the reference suite:
// tests/lang/unit/test_variable_assigner.py (reachable assigner assertions)
// and tests/lang/regression/test_human_input_semantics.py
// (target_validation-related assertions), plus self-built reachable-surface
// cases. Every message and span is pinned against the Python oracle
// (compile_nodecoda_result, 上游 Python 参考实现（nclang） — read-only reference):
// messages byte-for-byte, line/col identical, and zero-location diagnostics
// where the reference emits span=None (Go zero span leaves the location
// fields empty).
//
// Harness: the full pipeline (DefaultPipeline().Run) for reachable cases;
// scope+type+TargetValidationPass-only whitebox (runTargetValidationWhiteBox)
// for branches earlier passes pre-empt — the Human Input form/action
// nominal-type checks (the type pass emits its own E1003 first), the
// extract_text operand (extract_text is not a registered builtin in Go —
// Task 10 deviation — so calls short-circuit with E1001 in scope), and the
// programmatic file-extension case (the parser rejects non-dot extensions).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/target/codec"
	"github.com/nodecoda/ncoda/pkg/tool"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ── harness ────────────────────────────────────────────────────────────

// runTargetValidationWhiteBox runs scope + type (populating Bindings,
// NamedTypes, ExprTypes and VarTypes), discards their diagnostics, then runs
// ONLY the target validation pass — for branches the reference's earlier
// passes pre-empt (see the header note). Mirrors runCapabilityGateWhiteBox.
func runTargetValidationWhiteBox(t *testing.T, src string, profile *TargetProfile) (*Context, *ast.Program) {
	t.Helper()
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(profile)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	ctx.Diagnostics = nil
	(&TargetValidationPass{}).Run(prog, ctx)
	return ctx, prog
}

// assertTargetValidationError asserts exactly one error with code E1034 on
// the given line/column whose message equals wantMessage (verbatim reference
// text). A zero line/col asserts the zero-location form (Python span=None).
func assertTargetValidationError(t *testing.T, res *FrontendResult, line, col int, wantMessage string) {
	t.Helper()
	if errs := res.Errors(); len(errs) != 1 {
		t.Fatalf("want exactly one error, got %d: %v", len(errs), res.Diagnostics)
	}
	d := res.Errors()[0]
	if d.Code != diag.TARGET_NOT_LOWERABLE {
		t.Fatalf("error code = %d, want E1034(%d): %v", d.Code, diag.TARGET_NOT_LOWERABLE, d)
	}
	if d.Line != line || d.Column != col {
		t.Fatalf("error at %d:%d, want %d:%d: %v", d.Line, d.Column, line, col, d)
	}
	if d.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", d.Message, wantMessage)
	}
}

// assertTargetValidationErrors asserts exactly the given error messages in
// order (multi-diagnostic cases), each at the given zero/one location.
func assertTargetValidationErrors(t *testing.T, res *FrontendResult, want ...struct {
	line, col int
	message   string
}) {
	t.Helper()
	errs := res.Errors()
	if len(errs) != len(want) {
		t.Fatalf("want %d errors, got %d: %v", len(want), len(errs), res.Diagnostics)
	}
	for i, d := range errs {
		if d.Code != diag.TARGET_NOT_LOWERABLE {
			t.Fatalf("error %d code = %d, want E1034: %v", i, d.Code, d)
		}
		if d.Line != want[i].line || d.Column != want[i].col {
			t.Fatalf("error %d at %d:%d, want %d:%d: %v", i, d.Line, d.Column, want[i].line, want[i].col, d)
		}
		if d.Message != want[i].message {
			t.Fatalf("error %d message = %q, want %q", i, d.Message, want[i].message)
		}
	}
}

// assertNoTargetValidationErrors asserts no E1034 diagnostics (other codes
// are ignored — the whitebox harness clears earlier-pass diagnostics anyway).
func assertNoTargetValidationErrors(t *testing.T, res *FrontendResult) {
	t.Helper()
	for _, d := range res.Diagnostics {
		if d.Code == diag.TARGET_NOT_LOWERABLE && d.Severity == diag.Error {
			t.Fatalf("unexpected E1034: %v", res.Diagnostics)
		}
	}
}

// ── 1. Start input shapes (:170-196) ───────────────────────────────────

// TestStartInputShapesRejectNonRepresentable pins the unsupported map and
// record shapes. Ordinary arrays are native Dify Start inputs (input_type
// "array") and are tested separately below.
func TestStartInputShapesRejectNonRepresentable(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		message string
	}{
		{
			name: "map",
			src: `function main(map<string, int> m) -> string {
    return "x";
}
`,
			message: "map Start input is not representable by the Dify target",
		},
		{
			name: "record",
			src: `type T = { string a; }
function main(T t) -> string {
    return "x";
}
`,
			message: "record Start input is not representable by the Dify target",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertTargetValidationError(t, DefaultPipeline().Run(c.src, "t.dc", nil), 0, 0, c.message)
		})
	}
}

func TestStartInputFileArrayIsLegal(t *testing.T) {
	src := `function main(array<file> files) -> string {
    return "x";
}
`
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

func TestStartInputStringArrayIsLegal(t *testing.T) {
	src := `function main(array<string> items) -> string {
    return items[0];
}
`
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

// ── 2. File constraints (:215-245) ─────────────────────────────────────

func TestFileTypeConstraintUnsupported(t *testing.T) {
	src := `function main(file<document, bogus> f) -> string {
    return "x";
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 0, 0,
		"file type constraint is unsupported: bogus")
}

func TestFileUploadMethodConstraintUnsupported(t *testing.T) {
	src := `function main(file<document; .pdf; local_file, ftp> f) -> string {
    return "x";
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 0, 0,
		"file upload method constraint is unsupported: ftp")
}

// TestFileExtensionConstraintUnsupported is a whitebox case: the reference
// parser rejects a non-dot extension with E1000 before target validation
// ("Expected '.' before file extension"), so the bad-extension branch of
// _validate_file_constraints is exercised through the programmatic AST
// constructor path (Python oracle: TargetValidationPass on a constructed
// FileTypeRef(extensions=("bad",)) emits the same message with span=None).
func TestFileExtensionConstraintUnsupported(t *testing.T) {
	entry := ast.NewFunctionDef("main", []*ast.ParamDecl{
		ast.NewParamDecl("f", ast.NewFileTypeRef(ast.FileTypeConstraints{Extensions: []string{"bad"}}), nil),
	}, ast.NewPrimTypeRef("string"), ast.NewBlock([]ast.Stmt{ast.NewReturnStmt(ast.NewStringLit("x"))}))
	prog := ast.NewProgram(ast.WithMode("workflow"), ast.WithEntry(entry))
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	ctx.Diagnostics = nil
	(&TargetValidationPass{}).Run(prog, ctx)
	errs := ctx.Errors()
	if len(errs) != 1 {
		t.Fatalf("want exactly one error, got %d: %v", len(errs), ctx.Diagnostics)
	}
	if errs[0].Code != diag.TARGET_NOT_LOWERABLE || errs[0].Message != "file extension constraint is unsupported: bad" {
		t.Fatalf("error = %v, want E1034 'file extension constraint is unsupported: bad'", errs[0])
	}
	if errs[0].Line != 0 || errs[0].Column != 0 {
		t.Fatalf("error location = %d:%d, want zero (Python span=None)", errs[0].Line, errs[0].Column)
	}
}

func TestFileConstraintsSupportedAreAccepted(t *testing.T) {
	src := `function main(file<document; .txt; local_file> f) -> string {
    return "x";
}
`
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

// ── 3. Conversation declarations (:1292-1314) ──────────────────────────

func TestConversationVariablesRequireAdvancedChatMode(t *testing.T) {
	src := `@mode workflow
@conversation string name = "default";
function main(string query) {
    name = "updated";
    return name;
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 2, 1,
		"Conversation variables (@conversation) require @mode advanced-chat")
}

func TestConversationVariableTypeUnsupported(t *testing.T) {
	src := `@mode advanced-chat
@conversation file f;
function main(string query) {
    output(query);
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 2, 1,
		"conversation variable type is not supported by Dify")
}

// TestConversationVariableNonLiteralDefault pins the optional+null case:
// the store-type rejection and the non-literal-default rejection both fire
// (Python oracle: two E1034 at 2:1 and 2:30).
func TestConversationVariableNonLiteralDefault(t *testing.T) {
	src := `@mode advanced-chat
@conversation string? name = null;
function main(string query) {
    output(query);
}
`
	res := DefaultPipeline().Run(src, "t.dc", nil)
	assertTargetValidationErrors(t, res,
		struct {
			line, col int
			message   string
		}{2, 1, "conversation variable type is not supported by Dify"},
		struct {
			line, col int
			message   string
		}{2, 30, "conversation variable defaults must be representable literals"},
	)
}

func TestConversationVariableLiteralDefaultAccepted(t *testing.T) {
	src := `@mode advanced-chat
@conversation string name = "default";
function main(string query) {
    output(query);
}
`
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

// ── 4. Assigner (visit_assign_stmt :1213-1290) ─────────────────────────

func TestAssignerLiteralSetAccepted(t *testing.T) {
	src := `@mode advanced-chat
@conversation string name = "default";
function main(string query) {
    name = "updated";
    output(name);
}
`
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

func TestAssignerSetDirectSelectorAccepted(t *testing.T) {
	src := `@mode advanced-chat
@conversation string visitor = "anon";
function main(string query) {
    visitor = query;
    output(visitor);
}
`
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

func TestAssignerSetArrayLiteralRejected(t *testing.T) {
	// array<string> is a supported conversation store, but the set operation
	// only supports scalar literals (Python oracle message + span).
	src := `@mode advanced-chat
@conversation array<string> tags = [];
function main(string query) {
    tags = ["a"];
    output(tags);
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 4, 12,
		"conversation variable set operation only supports scalar literal values")
}

func TestAssignerAppendSelectorAccepted(t *testing.T) {
	src := `@mode advanced-chat
@conversation array<string> tags = [];
function main(string query) {
    let item = "x";
    tags << item;
    output(tags);
}
`
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

func TestAssignerAppendLiteralRejected(t *testing.T) {
	src := `@mode advanced-chat
@conversation array<string> tags = [];
function main(string query) {
    tags << "x";
    output(tags);
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 4, 13,
		"conversation variable append requires a selector-backed variable input; literal append is not representable by Graphon v2")
}

func TestAssignerCompoundNumericAccepted(t *testing.T) {
	src := `@mode advanced-chat
@conversation int score = 0;
function main(string query) {
    score += 10;
    output(score);
}
`
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

func TestAssignerCompoundNonNumericRejected(t *testing.T) {
	src := `@mode advanced-chat
@conversation string text = "";
function main(string query) {
    text += "b";
    output(text);
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 4, 13,
		"conversation variable compound assignment requires a numeric literal for a numeric target")
}

// TestAssignerStateAssignmentRejectedManifest pins the capability gate
// branch: a profile whose manifest flips STATE_ASSIGNMENT to REJECTED yields
// the certified-target message before any assigner shape check (Python
// oracle: E1034 'state.assignment is not certified for the selected target'
// at 4:5).
func TestAssignerStateAssignmentRejectedManifest(t *testing.T) {
	src := `@mode advanced-chat
@conversation string name = "default";
function main(string query) {
    name = "updated";
    output(name);
}
`
	profile := rejectedProfile(CapabilityShapeStateAssignment)
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", profile), 4, 5,
		"state.assignment is not certified for the selected target")
}

// ── 5. Field access (visit_field_access_expr :1201-1211) ───────────────

func TestSingleFileFieldAccessRejected(t *testing.T) {
	src := `function main(file<document> f) -> string {
    let s = f.name;
    return s;
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 2, 13,
		"single file field access is not representable by the Dify target")
}

// TestFileFieldAccessResultRejected covers the result-is-file branch: a
// record field access whose RESULT is a file also rejects (Python oracle:
// 'single file field access is not representable by the Dify target').
func TestFileFieldAccessResultRejected(t *testing.T) {
	src := `type F = { file<document> doc; }
function main(F f) -> string {
    let d = f.doc;
    return "x";
}
`
	res := DefaultPipeline().Run(src, "t.dc", nil)
	// The record Start input rejection fires first (param F), then the field
	// access rejection at 3:13.
	errs := res.Errors()
	var messages []string
	for _, d := range errs {
		if d.Code == diag.TARGET_NOT_LOWERABLE {
			messages = append(messages, d.Message)
		}
	}
	found := false
	for _, m := range messages {
		if m == "single file field access is not representable by the Dify target" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing field-access rejection, got %v", res.Diagnostics)
	}
}

// ── 6. llm vision operand (:1194-1199, visit_call_expr :281-289) ───────

func TestLLMVisionSelectorAccepted(t *testing.T) {
	src := `@mode workflow
function main(file<image> img) -> string {
    let r = llm("gpt-4o", { "vision": img });
    return r.text;
}
`
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

// TestLLMVisionNonSelectorRejected pins the ternary-operand case: a file
// value that is not an IdentExpr/FieldAccessExpr selector fails the vision
// contract (Python oracle: E1034 at 3:39, the ternary start).
func TestLLMVisionNonSelectorRejected(t *testing.T) {
	src := `@mode workflow
function main(file<image> a, file<image> b) -> string {
    let r = llm("gpt-4o", { "vision": true ? a : b });
    return r.text;
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 3, 39,
		"vision requires a selector-backed scalar file for the Dify target")
}

// ── 7. extract_text operand (:1186-1192) — whitebox ────────────────────

// TestExtractTextOperandNonExtractable is a whitebox case: extract_text is
// not a registered builtin in Go (Task 10 deviation), so a source call
// short-circuits in scope with E1001. The ported operand validator is
// exercised directly (Python oracle on the same helper: 'extract_text
// requires a selector-backed scalar file with a declared extractable
// extension' — for a file<document> selector, which has no declared
// extensions). The Go source places the selector at 3:13.
func TestExtractTextOperandNonExtractable(t *testing.T) {
	src := "@mode workflow\nfunction main(file<document> f) -> string {\n    let g = f;\n    return \"x\";\n}\n"
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	ctx.Diagnostics = nil
	c := &identCollector{}
	ast.Walk(c, prog)
	fIdent := lastNamedIdent(c.out, "f")
	(&TargetValidationPass{ctx: ctx}).validateExtractTextOperand(fIdent, ctx)
	errs := ctx.Errors()
	if len(errs) != 1 {
		t.Fatalf("want exactly one error, got %d: %v", len(errs), ctx.Diagnostics)
	}
	if errs[0].Code != diag.TARGET_NOT_LOWERABLE || errs[0].Message != "extract_text requires a selector-backed scalar file with a declared extractable extension" {
		t.Fatalf("error = %v, want E1034 extract_text operand rejection", errs[0])
	}
	if errs[0].Line != 3 || errs[0].Column != 13 {
		t.Fatalf("error at %d:%d, want 3:13 (the selector position)", errs[0].Line, errs[0].Column)
	}
}

// TestExtractTextOperandExtractableAccepted is the whitebox control: a
// file<document; .txt> selector satisfies the extractable contract.
func TestExtractTextOperandExtractableAccepted(t *testing.T) {
	src := "@mode workflow\nfunction main(file<document; .txt> f) -> string {\n    let g = f;\n    return \"x\";\n}\n"
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	ctx.Diagnostics = nil
	c := &identCollector{}
	ast.Walk(c, prog)
	fIdent := lastNamedIdent(c.out, "f")
	(&TargetValidationPass{ctx: ctx}).validateExtractTextOperand(fIdent, ctx)
	if errs := ctx.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
}

// ── 8. Generated code expressions (:1143-1184) ─────────────────────────

// TestGeneratedCodeReturnMapRejected pins the return-map path: a nested map
// value that is not a direct selector is routed through the generated-code
// check, whose scalar-file scan finds the file selector (Python oracle:
// E1034 at 3:33, the selector position).
func TestGeneratedCodeReturnMapRejected(t *testing.T) {
	src := `@mode workflow
function main(file<document; .txt> f) -> map<string, map<string, file>> {
    return { "doc": { "nested": f } };
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 3, 33,
		"single-file Code input is not JSON-safe for the Dify target")
}

// TestGeneratedCodeAnswerMapRejected pins the answer-stmt path (Python
// oracle: E1034 at 3:21).
func TestGeneratedCodeAnswerMapRejected(t *testing.T) {
	src := `@mode advanced-chat
function main(file<document; .txt> query) {
    output({ "doc": query });
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 3, 21,
		"single-file Code input is not JSON-safe for the Dify target")
}

// TestGeneratedCodeReturnBinaryRejected pins the non-map return path (Python
// oracle: E1034 at 3:18, the file selector inside the binary expression).
func TestGeneratedCodeReturnBinaryRejected(t *testing.T) {
	src := `@mode workflow
function main(file<document; .txt> a, file<document; .txt> b) -> string {
    return "x" + a;
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 3, 18,
		"single-file Code input is not JSON-safe for the Dify target")
}

// ── 9. Human Input (visit_request_input_stmt :404-690) ─────────────────

const humanInputSource = `
enum ReviewAction { approve, reject }
enum Priority { low, high }

type ReviewForm = {
    string comment;
    Priority priority;
}

function main() -> string {
    request_input<ReviewForm, ReviewAction>({
        content: "Review this request",
        fields: {
            comment: { kind: "paragraph" },
            priority: { kind: "select" }
        },
        actions: {
            approve: { title: "Approve", style: "accent" },
            reject: { title: "Reject", style: "ghost" }
        },
        timeout: { value: 3, unit: "day" }
    }) as review {
        action ReviewAction.approve { return review.comment; }
        action ReviewAction.reject { return review.priority.value; }
        timeout { return "timed out"; }
    }
}
`

// TestRequestInputPublishesClosedContract ports
// test_human_input_semantics.py::test_request_input_publishes_immutable_closed_contract
// through the whitebox harness (the pipeline result does not expose the
// Context). The published contract must carry the form/action descriptors,
// content, ordered field/action contracts and the timeout pair.
func TestRequestInputPublishesClosedContract(t *testing.T) {
	ctx, prog := runTargetValidationWhiteBox(t, humanInputSource, nil)
	if errs := ctx.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
	if len(ctx.ValidatedHumanInputs) != 1 {
		t.Fatalf("ValidatedHumanInputs = %d entries, want 1", len(ctx.ValidatedHumanInputs))
	}
	var ri *ast.RequestInputStmt
	ast.Walk(&requestInputCollector{out: &ri}, prog)
	nodeID, ok := ctx.Bindings.NodeIDOf(ri)
	if !ok {
		t.Fatalf("request_input node not enumerated")
	}
	contract := ctx.ValidatedHumanInputs[nodeID]
	if contract == nil {
		t.Fatalf("no contract published for the request_input node")
	}
	if contract.FormType.Kind != types.Record || contract.ActionType.Kind != types.Enum {
		t.Fatalf("form/action kinds = %v/%v, want record/enum", contract.FormType.Kind, contract.ActionType.Kind)
	}
	if contract.Content != "Review this request" {
		t.Fatalf("content = %q, want %q", contract.Content, "Review this request")
	}
	if len(contract.Fields) != 2 {
		t.Fatalf("fields = %d, want 2", len(contract.Fields))
	}
	if contract.Fields[0].Name != "comment" || contract.Fields[0].Kind != binding.HumanInputFieldKindParagraph {
		t.Fatalf("field[0] = %+v, want comment/paragraph", contract.Fields[0])
	}
	if contract.Fields[1].Name != "priority" || contract.Fields[1].Kind != binding.HumanInputFieldKindSelect {
		t.Fatalf("field[1] = %+v, want priority/select", contract.Fields[1])
	}
	if !binding.StringSlicesEqual(contract.Fields[1].Options, []string{"low", "high"}) {
		t.Fatalf("priority options = %v, want [low high]", contract.Fields[1].Options)
	}
	if len(contract.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(contract.Actions))
	}
	if contract.Actions[0].Member != "approve" || contract.Actions[0].Title != "Approve" || contract.Actions[0].Style != binding.HumanInputActionStyleAccent {
		t.Fatalf("action[0] = %+v, want approve/Approve/accent", contract.Actions[0])
	}
	if contract.Actions[1].Member != "reject" || contract.Actions[1].Title != "Reject" || contract.Actions[1].Style != binding.HumanInputActionStyleGhost {
		t.Fatalf("action[1] = %+v, want reject/Reject/ghost", contract.Actions[1])
	}
	if contract.TimeoutValue != 3 || contract.TimeoutUnit != binding.HumanInputTimeoutUnitDay {
		t.Fatalf("timeout = %d %s, want 3 day", contract.TimeoutValue, contract.TimeoutUnit)
	}
}

// requestInputCollector stores the (last) request_input statement.
type requestInputCollector struct {
	out **ast.RequestInputStmt
}

func (c *requestInputCollector) Visit(n ast.Node) ast.Visitor {
	if ri, ok := n.(*ast.RequestInputStmt); ok {
		*c.out = ri
	}
	return c
}

// astWalker invokes onRequestInput for every request_input statement while
// walking a program (used by the stale-contract rerun test to mutate the
// config in place, mirroring the reference test's style.value mutation).
type astWalker struct {
	onRequestInput func(*ast.RequestInputStmt)
}

func (w *astWalker) Visit(n ast.Node) ast.Visitor {
	if ri, ok := n.(*ast.RequestInputStmt); ok {
		w.onRequestInput(ri)
	}
	return w
}

// mutateHumanInputStyle rewrites the style entry of one action config in the
// program's request_input statement (the reference test mutates style.value
// in place before re-running the pass).
func mutateHumanInputStyle(prog *ast.Program, action, style string) {
	ast.Walk(&astWalker{onRequestInput: func(ri *ast.RequestInputStmt) {
		cfg := ri.Config()
		for i := range cfg.Entries() {
			key, ok := staticMapKey(cfg.Entries()[i].Key)
			if !ok || key != "actions" {
				continue
			}
			actions, ok := cfg.Entries()[i].Value.(*ast.MapLit)
			if !ok {
				continue
			}
			for j := range actions.Entries() {
				key, ok := staticMapKey(actions.Entries()[j].Key)
				if !ok || key != action {
					continue
				}
				approve, ok := actions.Entries()[j].Value.(*ast.MapLit)
				if !ok {
					continue
				}
				for k := range approve.Entries() {
					key, ok := staticMapKey(approve.Entries()[k].Key)
					if !ok || key != "style" {
						continue
					}
					approve.Entries()[k].Value = ast.NewStringLit(style)
					return
				}
			}
		}
	}}, prog)
}

// TestRequestInputConfigContractRejections pins the config-level messages
// and spans (Python oracle per case: line/col verified against
// compile_nodecoda_result on the same source).
func TestRequestInputConfigContractRejections(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		line    int
		col     int
		message string
	}{
		{
			name: "missing-content",
			src:  replace(humanInputSource, "        content: \"Review this request\",\n", ""),
			line: 11, col: 45,
			message: "Human Input config is missing required key 'content'",
		},
		{
			name: "extra-key",
			src:  replace(humanInputSource, "        timeout: { value: 3, unit: \"day\" }", "        timeout: { value: 3, unit: \"day\" }, x: 1"),
			line: 11, col: 45,
			message: "Human Input config has extra key 'x'",
		},
		{
			name: "delivery",
			src:  replace(humanInputSource, "        content: \"Review this request\",", "        content: \"Review this request\", delivery: \"email\","),
			line: 12, col: 51,
			message: "Human Input delivery is runtime invocation configuration, not NodeCoda node config",
		},
		{
			name: "dynamic-content",
			src:  replace(replace(humanInputSource, "function main() -> string", "function main(string prompt) -> string"), "content: \"Review this request\"", "content: prompt"),
			line: 12, col: 18,
			message: "Human Input content must be a non-empty static string",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertTargetValidationError(t, DefaultPipeline().Run(c.src, "t.dc", nil), c.line, c.col, c.message)
		})
	}
}

func TestRequestInputFieldContractRejections(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		line    int
		col     int
		message string
	}{
		{
			name: "missing-field",
			src:  replace(humanInputSource, "            comment: { kind: \"paragraph\" },\n", ""),
			line: 13, col: 17,
			message: "Human Input fields are missing form field 'comment'",
		},
		{
			name: "extra-field",
			src:  replace(humanInputSource, "            comment: { kind: \"paragraph\" },", "            extra: { kind: \"paragraph\" }, comment: { kind: \"paragraph\" },"),
			line: 13, col: 17,
			message: "Human Input fields contain extra form field 'extra'",
		},
		{
			name: "bad-kind",
			src:  replace(humanInputSource, "comment: { kind: \"paragraph\" }", "comment: { kind: \"select\" }"),
			line: 14, col: 30,
			message: "Human Input field 'comment' requires kind 'paragraph', got 'select'",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertTargetValidationError(t, DefaultPipeline().Run(c.src, "t.dc", nil), c.line, c.col, c.message)
		})
	}
}

func TestRequestInputActionContractRejections(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		line    int
		col     int
		message string
	}{
		{
			name: "bad-style",
			src:  replace(humanInputSource, "style: \"accent\"", "style: \"danger\""),
			line: 18, col: 49,
			message: "Human Input action 'approve' style must be primary, default, accent, or ghost",
		},
		{
			name: "dynamic-title",
			src:  replace(replace(humanInputSource, "function main() -> string", "function main(string title) -> string"), "title: \"Approve\"", "title: title"),
			line: 18, col: 31,
			message: "Human Input action 'approve' title must be a non-empty static string",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertTargetValidationError(t, DefaultPipeline().Run(c.src, "t.dc", nil), c.line, c.col, c.message)
		})
	}
}

func TestRequestInputTimeoutContractRejections(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		line    int
		col     int
		message string
	}{
		{
			name: "bad-unit",
			src:  replace(humanInputSource, "unit: \"day\"", "unit: \"minute\""),
			line: 21, col: 36,
			message: "Human Input timeout unit must be hour or day",
		},
		{
			name: "non-positive-value",
			src:  replace(humanInputSource, "value: 3", "value: 0"),
			line: 21, col: 27,
			message: "Human Input timeout value must be a positive static integer",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertTargetValidationError(t, DefaultPipeline().Run(c.src, "t.dc", nil), c.line, c.col, c.message)
		})
	}
}

// TestRequestInputFormActionNominalTypes is a whitebox case: the type pass
// pre-empts both nominal-type rejections in the full pipeline with its own
// E1003 ("Human Input form/action type must be a nominal record/enum"), so
// the target-validation branch is pinned with the earlier passes' diagnostics
// discarded (Python oracle: TargetValidationPass-only run emits the E1034 at
// 11:5 for both sources).
func TestRequestInputFormActionNominalTypes(t *testing.T) {
	cases := []struct {
		name    string
		replace string
		message string
	}{
		{
			name:    "form-string",
			replace: "request_input<ReviewForm, ReviewAction>",
			message: "Human Input form must be a nominal record",
		},
		{
			name:    "action-string",
			replace: "request_input<ReviewForm, ReviewAction>",
			message: "Human Input action type must be a nominal enum",
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := humanInputSource
			if i == 0 {
				src = replace(src, c.replace, "request_input<string, ReviewAction>")
			} else {
				src = replace(src, c.replace, "request_input<ReviewForm, string>")
			}
			ctx, _ := runTargetValidationWhiteBox(t, src, nil)
			errs := ctx.Errors()
			if len(errs) != 1 {
				t.Fatalf("want exactly one error, got %d: %v", len(errs), ctx.Diagnostics)
			}
			if errs[0].Code != diag.TARGET_NOT_LOWERABLE || errs[0].Message != c.message {
				t.Fatalf("error = %v, want %q", errs[0], c.message)
			}
			if errs[0].Line != 11 || errs[0].Column != 5 {
				t.Fatalf("error at %d:%d, want 11:5", errs[0].Line, errs[0].Column)
			}
		})
	}
}

// ── 9b. human_input_contract.go (108-line module) ──────────────────────

// TestIsCanonicalHumanInputActionID ports the canonical-ID wire contract
// (human_input_contract.py:25-27): [A-Za-z_][A-Za-z0-9_]* and at most 20
// characters.
func TestIsCanonicalHumanInputActionID(t *testing.T) {
	valid := []string{"approve", "reject", "_ok", "A1_b2", "x"}
	for _, v := range valid {
		if !binding.IsCanonicalHumanInputActionID(v) {
			t.Fatalf("binding.IsCanonicalHumanInputActionID(%q) = false, want true", v)
		}
	}
	invalid := []string{"", "bad-id", "1abc", "with space", "a.b", "this_action_id_is_way_too_long"}
	for _, v := range invalid {
		if binding.IsCanonicalHumanInputActionID(v) {
			t.Fatalf("binding.IsCanonicalHumanInputActionID(%q) = true, want false", v)
		}
	}
}

func TestHumanInputFieldContractInvariants(t *testing.T) {
	enum := types.EnumType("Priority", []string{"low", "high"})
	if _, err := binding.NewHumanInputFieldContract("", binding.HumanInputFieldKindParagraph, types.StringType(), nil); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := binding.NewHumanInputFieldContract("f", binding.HumanInputFieldKindParagraph, types.IntType(), nil); err == nil {
		t.Fatal("paragraph over int accepted")
	}
	if _, err := binding.NewHumanInputFieldContract("f", binding.HumanInputFieldKindParagraph, types.StringType(), []string{"x"}); err == nil {
		t.Fatal("paragraph with options accepted")
	}
	if _, err := binding.NewHumanInputFieldContract("f", binding.HumanInputFieldKindSelect, types.StringType(), nil); err == nil {
		t.Fatal("select over string accepted")
	}
	if _, err := binding.NewHumanInputFieldContract("f", binding.HumanInputFieldKindSelect, enum, []string{"high", "low"}); err == nil {
		t.Fatal("select with reordered options accepted")
	}
	contract, err := binding.NewHumanInputFieldContract("f", binding.HumanInputFieldKindSelect, enum, []string{"low", "high"})
	if err != nil || contract == nil {
		t.Fatalf("valid select contract rejected: %v", err)
	}
}

func TestHumanInputActionContractInvariants(t *testing.T) {
	if _, err := binding.NewHumanInputActionContract("bad-id", "Title", binding.HumanInputActionStylePrimary); err == nil {
		t.Fatal("non-canonical member accepted")
	}
	if _, err := binding.NewHumanInputActionContract("ok", "", binding.HumanInputActionStylePrimary); err == nil {
		t.Fatal("empty title accepted")
	}
	if _, err := binding.NewHumanInputActionContract("ok", "Title", binding.HumanInputActionStyle("danger")); err == nil {
		t.Fatal("unknown style accepted")
	}
	// M1: the title limit counts code points (Python len()), so 60 CJK
	// characters (180 UTF-8 bytes) are legal at the contract level too.
	cjk60 := ""
	for i := 0; i < 60; i++ {
		cjk60 += "汉"
	}
	cjkContract, err := binding.NewHumanInputActionContract("approve", cjk60, binding.HumanInputActionStyleAccent)
	if err != nil || cjkContract == nil {
		t.Fatalf("60-CJK-character title rejected (byte vs rune count): %v", err)
	}
	if _, err := binding.NewHumanInputActionContract("ok", strings.Repeat("A", 101), binding.HumanInputActionStylePrimary); err == nil {
		t.Fatal("101-ASCII-character title accepted")
	}
	contract, err := binding.NewHumanInputActionContract("approve", "Approve", binding.HumanInputActionStyleAccent)
	if err != nil || contract == nil {
		t.Fatalf("valid action contract rejected: %v", err)
	}
}

func TestValidatedHumanInputContractInvariants(t *testing.T) {
	actionEnum := types.EnumType("ReviewAction", []string{"approve", "reject"})
	formRecord := types.RecordType("ReviewForm", []types.RecordField{
		{Name: "comment", Type: types.StringType()},
		{Name: "priority", Type: types.EnumType("Priority", []string{"low", "high"})},
	})
	fields := []binding.HumanInputFieldContract{
		{Name: "comment", Kind: binding.HumanInputFieldKindParagraph, TypeInfo: types.StringType()},
		{Name: "priority", Kind: binding.HumanInputFieldKindSelect, TypeInfo: types.EnumType("Priority", []string{"low", "high"}), Options: []string{"low", "high"}},
	}
	actions := []binding.HumanInputActionContract{
		{Member: "approve", Title: "Approve", Style: binding.HumanInputActionStyleAccent},
		{Member: "reject", Title: "Reject", Style: binding.HumanInputActionStyleGhost},
	}
	if _, err := binding.NewValidatedHumanInputContract(types.StringType(), actionEnum, "c", fields, actions, 3, binding.HumanInputTimeoutUnitDay, 0, false, nil); err == nil {
		t.Fatal("non-record form accepted")
	}
	if _, err := binding.NewValidatedHumanInputContract(formRecord, types.StringType(), "c", fields, actions, 3, binding.HumanInputTimeoutUnitDay, 0, false, nil); err == nil {
		t.Fatal("non-enum action type accepted")
	}
	if _, err := binding.NewValidatedHumanInputContract(formRecord, actionEnum, "", fields, actions, 3, binding.HumanInputTimeoutUnitDay, 0, false, nil); err == nil {
		t.Fatal("empty content accepted")
	}
	partialFields := fields[:1]
	if _, err := binding.NewValidatedHumanInputContract(formRecord, actionEnum, "c", partialFields, actions, 3, binding.HumanInputTimeoutUnitDay, 0, false, nil); err == nil {
		t.Fatal("non-exhaustive fields accepted")
	}
	if _, err := binding.NewValidatedHumanInputContract(formRecord, actionEnum, "c", fields, actions, 0, binding.HumanInputTimeoutUnitDay, 0, false, nil); err == nil {
		t.Fatal("non-positive timeout accepted")
	}
	if _, err := binding.NewValidatedHumanInputContract(formRecord, actionEnum, "c", fields, actions, 3, binding.HumanInputTimeoutUnit("minute"), 0, false, nil); err == nil {
		t.Fatal("unknown timeout unit accepted")
	}
	contract, err := binding.NewValidatedHumanInputContract(formRecord, actionEnum, "c", fields, actions, 3, binding.HumanInputTimeoutUnitDay, 0, false, nil)
	if err != nil || contract == nil {
		t.Fatalf("valid contract rejected: %v", err)
	}
}

// ── 10. Regression: pipeline stage wiring ──────────────────────────────

// TestTargetValidationRegisteredInPipeline locks the pipeline wiring: the
// target-validation stage runs between semantic and capability_gate (17C1),
// and the earlier probe surface (semantic + gate suites) stays green — the
// full package suite is the regression gate (brief item 10).
func TestTargetValidationRegisteredInPipeline(t *testing.T) {
	p := DefaultPipeline()
	if _, ok := p.registry[StageTargetValidation]; !ok {
		t.Fatal("StageTargetValidation is not registered in DefaultPipeline")
	}
	// Order: semantic (13) < target_validation (14) < capability_gate (15).
	if StageSemantic >= StageTargetValidation || StageTargetValidation >= StageCapabilityGate {
		t.Fatalf("stage order wrong: semantic=%d target_validation=%d capability_gate=%d",
			StageSemantic, StageTargetValidation, StageCapabilityGate)
	}
}

// lastNamedIdent returns the last collected identifier with the given name,
// or nil.
func lastNamedIdent(idents []*ast.IdentExpr, name string) *ast.IdentExpr {
	for i := len(idents) - 1; i >= 0; i-- {
		if idents[i].Name() == name {
			return idents[i]
		}
	}
	return nil
}

// replace substitutes the first occurrence of old with new (test sources
// mirror the reference suite's str.replace edits).
func replace(s, old, new string) string {
	idx := indexOf(s, old)
	if idx < 0 {
		panic("not found: " + old)
	}
	return s[:idx] + new + s[idx+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ── R1 regression: I1 dedupe + I2 sorted missing keys + M1 rune count ──

// TestFileTypeConstraintDuplicateValuesDeduped pins the I1 fix: Python
// dedupes via set(constraints.types) before the sorted join
// (_validate_file_constraints :226-245), so a duplicated unknown type must
// appear once (oracle: "file type constraint is unsupported: bogus" for
// file<document, bogus, bogus>).
func TestFileTypeConstraintDuplicateValuesDeduped(t *testing.T) {
	src := `@mode workflow
function main(file<document, bogus, bogus> f) -> string {
    return "x";
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 0, 0,
		"file type constraint is unsupported: bogus")
}

// TestFileUploadMethodConstraintDuplicateValuesDeduped pins the I1 upload
// branch: duplicate unknown upload methods collapse to one entry (oracle:
// "file upload method constraint is unsupported: ftp").
func TestFileUploadMethodConstraintDuplicateValuesDeduped(t *testing.T) {
	src := `@mode workflow
function main(file<document; .pdf; local_file, ftp, ftp> f) -> string {
    return "x";
}
`
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 0, 0,
		"file upload method constraint is unsupported: ftp")
}

// TestFileExtensionConstraintDuplicateValuesDeduped pins the I1 extension
// branch through the programmatic AST (the parser rejects non-dot
// extensions, so this is whitebox like the single-value sibling).
func TestFileExtensionConstraintDuplicateValuesDeduped(t *testing.T) {
	entry := ast.NewFunctionDef("main", []*ast.ParamDecl{
		ast.NewParamDecl("f", ast.NewFileTypeRef(ast.FileTypeConstraints{Extensions: []string{"bad", "bad"}}), nil),
	}, ast.NewPrimTypeRef("string"), ast.NewBlock([]ast.Stmt{ast.NewReturnStmt(ast.NewStringLit("x"))}))
	prog := ast.NewProgram(ast.WithMode("workflow"), ast.WithEntry(entry))
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	ctx.Diagnostics = nil
	(&TargetValidationPass{}).Run(prog, ctx)
	errs := ctx.Errors()
	if len(errs) != 1 {
		t.Fatalf("want exactly one error, got %d: %v", len(errs), ctx.Diagnostics)
	}
	if errs[0].Code != diag.TARGET_NOT_LOWERABLE || errs[0].Message != "file extension constraint is unsupported: bad" {
		t.Fatalf("error = %v, want E1034 'file extension constraint is unsupported: bad'", errs[0])
	}
}

// TestRequestInputConfigMissingAllKeysSorted pins the I2 config fix: Python
// emits sorted(required - config.keys()) (visit_request_input_stmt :404),
// so with all four keys missing the order is actions, content, fields,
// timeout (oracle verified). The span is the config map span (11:45).
func TestRequestInputConfigMissingAllKeysSorted(t *testing.T) {
	src := replace(humanInputSource,
		`    request_input<ReviewForm, ReviewAction>({
        content: "Review this request",
        fields: {
            comment: { kind: "paragraph" },
            priority: { kind: "select" }
        },
        actions: {
            approve: { title: "Approve", style: "accent" },
            reject: { title: "Reject", style: "ghost" }
        },
        timeout: { value: 3, unit: "day" }
    }) as review {`,
		`    request_input<ReviewForm, ReviewAction>({}) as review {`,
	)
	assertTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil),
		struct {
			line, col int
			message   string
		}{11, 45, "Human Input config is missing required key 'actions'"},
		struct {
			line, col int
			message   string
		}{11, 45, "Human Input config is missing required key 'content'"},
		struct {
			line, col int
			message   string
		}{11, 45, "Human Input config is missing required key 'fields'"},
	)
}

// TestRequestInputTimeoutMissingBothKeysSorted pins the I2 timeout fix:
// Python emits sorted({"value","unit"} - keys) (_validate_human_input_timeout
// :669), so both missing yields unit before value (oracle verified). The
// span is the timeout map span (21:18 for this source shape).
func TestRequestInputTimeoutMissingBothKeysSorted(t *testing.T) {
	src := replace(humanInputSource, "timeout: { value: 3, unit: \"day\" }", "timeout: {}")
	assertTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil),
		struct {
			line, col int
			message   string
		}{21, 18, "Human Input timeout is missing required key 'unit'"},
		struct {
			line, col int
			message   string
		}{21, 18, "Human Input timeout is missing required key 'value'"},
	)
}

// TestRequestInputActionTitleCJKAccepted pins the M1 fix: Python len() on a
// title counts code points, so a 60-CJK-character title is within the 100
// limit (oracle: zero diagnostics). Go must not count UTF-8 bytes.
func TestRequestInputActionTitleCJKAccepted(t *testing.T) {
	cjk60 := ""
	for i := 0; i < 60; i++ {
		cjk60 += "汉"
	}
	src := replace(humanInputSource, `title: "Approve"`, `title: "`+cjk60+`"`)
	assertNoTargetValidationErrors(t, DefaultPipeline().Run(src, "t.dc", nil))
}

// ── R1 M3: Python-suite branch assertions (oracle pins) ────────────────

// TestRequestInputDynamicTargetConfigurations pins the pass-level
// diagnostics for the four dynamic-config branches of
// test_request_input_rejects_dynamic_target_configuration: dynamic field
// kind, dynamic action style, dynamic timeout value, and non-literal
// options (each message+span oracle-verified on the same source shape).
func TestRequestInputDynamicTargetConfigurations(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		line    int
		col     int
		message string
	}{
		{
			name: "dynamic-kind",
			src: replace(replace(humanInputSource,
				"function main() -> string", "function main(string fieldKind) -> string"),
				`comment: { kind: "paragraph" }`, "comment: { kind: fieldKind }"),
			line: 14, col: 30,
			message: "Human Input field 'comment' kind must be a static string",
		},
		{
			name: "dynamic-style",
			src: replace(replace(humanInputSource,
				"function main() -> string", "function main(string style) -> string"),
				`style: "accent"`, "style: style"),
			line: 18, col: 49,
			message: "Human Input action 'approve' style must be a static string",
		},
		{
			name: "dynamic-timeout-value",
			src: replace(replace(humanInputSource,
				"function main() -> string", "function main(int duration) -> string"),
				"value: 3", "value: duration"),
			line: 21, col: 27,
			message: "Human Input timeout value must be a positive static integer",
		},
		{
			name: "dynamic-options",
			src: replace(replace(humanInputSource,
				"function main() -> string", "function main(string[] choices) -> string"),
				`priority: { kind: "select" }`,
				`priority: { kind: "select", options: choices }`),
			// The string[] start param adds its own E1034 first (oracle:
			// both diagnostics appear, as in the reference suite's
			// _assert_error presence check); the options diagnostic is the
			// one asserted below.
			line: 15, col: 50,
			message: "Human Input field 'priority' options must be a static string array",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := DefaultPipeline().Run(c.src, "t.dc", nil)
			for _, d := range res.Errors() {
				if d.Code == diag.TARGET_NOT_LOWERABLE && d.Line == c.line && d.Column == c.col && d.Message == c.message {
					return
				}
			}
			t.Fatalf("want E1034 %q at %d:%d, got %v", c.message, c.line, c.col, res.Diagnostics)
		})
	}
}

// TestRequestInputOptionsOrderRejected pins the pass-level options-order
// diagnostic (test_request_input_accepts_explicit_options_only_in_enum_order:
// reversed options are rejected with TARGET_NOT_LOWERABLE containing "order").
func TestRequestInputOptionsOrderRejected(t *testing.T) {
	src := replace(humanInputSource,
		`priority: { kind: "select" }`,
		`priority: { kind: "select", options: ["high", "low"] }`)
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 15, 50,
		"Human Input field 'priority' options must match enum declaration order")
}

// TestRequestInputOptionsInEnumOrderAccepted is the control: explicit options
// in declaration order stay legal and reach the published contract.
func TestRequestInputOptionsInEnumOrderAccepted(t *testing.T) {
	src := replace(humanInputSource,
		`priority: { kind: "select" }`,
		`priority: { kind: "select", options: ["low", "high"] }`)
	ctx, _ := runTargetValidationWhiteBox(t, src, nil)
	if errs := ctx.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", ctx.Diagnostics)
	}
	if len(ctx.ValidatedHumanInputs) != 1 {
		t.Fatalf("ValidatedHumanInputs = %d entries, want 1", len(ctx.ValidatedHumanInputs))
	}
}

// TestRequestInputUnsupportedFieldType pins the unsupported-field-type branch
// (test_request_input_rejects_field_config_mismatch_and_unsupported_field_type):
// an int comment field is rejected at the field config span with the
// descriptor rendered like Python ("Type(int)").
func TestRequestInputUnsupportedFieldType(t *testing.T) {
	src := replace(replace(humanInputSource, "string comment;", "int comment;"),
		"return review.comment;", `return "reviewed";`)
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 14, 22,
		"Human Input field 'comment' has unsupported type Type(int)")
}

// TestRequestInputActionsMissingMember pins the missing-enum-member branch
// (the config-map variant; branch-level non-exhaustive actions are pre-empted
// by the type pass with E1003 in the full pipeline — oracle: E1034 at 17:18
// for the config map).
func TestRequestInputActionsMissingMember(t *testing.T) {
	src := replace(humanInputSource,
		"            approve: { title: \"Approve\", style: \"accent\" },\n            reject: { title: \"Reject\", style: \"ghost\" }\n",
		"            approve: { title: \"Approve\", style: \"accent\" }\n")
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 17, 18,
		"Human Input actions are missing enum member 'reject'")
}

// TestRequestInputActionsExtraMember pins the extra-enum-member branch
// (oracle: E1034 at 17:18 for the config map).
func TestRequestInputActionsExtraMember(t *testing.T) {
	src := replace(humanInputSource,
		"        actions: {",
		"        actions: { extra_action: { title: \"X\", style: \"primary\" },")
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 17, 18,
		"Human Input actions contain extra enum member 'extra_action'")
}

// TestRequestInputMissingStaticStyle pins the missing-style branch
// (test_request_input_rejects_noncanonical_action_payloads: style removed
// → "style" in message).
func TestRequestInputMissingStaticStyle(t *testing.T) {
	src := replace(humanInputSource, ", style: \"accent\"", "")
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 18, 22,
		"Human Input action 'approve' is missing required static style")
}

// TestRequestInputTitleTooLong pins the >100-character title branch.
func TestRequestInputTitleTooLong(t *testing.T) {
	src := replace(humanInputSource, `title: "Approve"`, `title: "`+strings.Repeat("A", 101)+`"`)
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 18, 31,
		"Human Input action 'approve' title exceeds the target maximum of 100 characters")
}

// TestRequestInputActionMemberTooLong pins the >20-character member branch.
func TestRequestInputActionMemberTooLong(t *testing.T) {
	src := replace(replace(replace(humanInputSource,
		"approve, reject", "approve_action_member_too_long, reject"),
		"approve: {", "approve_action_member_too_long: {"),
		"ReviewAction.approve", "ReviewAction.approve_action_member_too_long")
	assertTargetValidationError(t, DefaultPipeline().Run(src, "t.dc", nil), 17, 18,
		"Human Input action ID 'approve_action_member_too_long' must match [A-Za-z_][A-Za-z0-9_]* and contain at most 20 characters")
}

// TestRequestInputSubtreeErrorPreventsContractPublication ports
// test_request_input_target_subtree_error_prevents_contract_publication: a
// scalar-file field access inside the action (or timeout) branch emits E1034
// during the subtree walk, so the request_input must not publish a contract.
func TestRequestInputSubtreeErrorPreventsContractPublication(t *testing.T) {
	for _, branch := range []string{"action", "timeout"} {
		t.Run(branch, func(t *testing.T) {
			src := replace(humanInputSource,
				"function main() -> string", "function main(file<document> upload) -> string")
			if branch == "action" {
				src = replace(src, "return review.comment;", "return upload.name;")
			} else {
				src = replace(src, `return "timed out";`, "return upload.name;")
			}
			ctx, _ := runTargetValidationWhiteBox(t, src, nil)
			found := false
			for _, d := range ctx.Diagnostics {
				if d.Code == diag.TARGET_NOT_LOWERABLE && d.Message == "single file field access is not representable by the Dify target" {
					found = true
				}
			}
			if !found {
				t.Fatalf("want scalar-file field-access E1034, got %v", ctx.Diagnostics)
			}
			if len(ctx.ValidatedHumanInputs) != 0 {
				t.Fatalf("ValidatedHumanInputs = %d entries, want 0 (subtree error must prevent publication)", len(ctx.ValidatedHumanInputs))
			}
		})
	}
}

// TestRequestInputRerunRemovesStaleContract ports
// test_request_input_revalidation_removes_stale_contract_from_same_context:
// a second pass run over the same context/program, now with an invalid
// style, must drop the previously published contract (the pass deletes the
// stale entry at visit start and only republishes on a clean validation).
func TestRequestInputRerunRemovesStaleContract(t *testing.T) {
	prog := parseSourceOrFail(t, humanInputSource)
	ctx := NewContext(nil)
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	ctx.Diagnostics = nil
	(&TargetValidationPass{}).Run(prog, ctx)
	if len(ctx.ValidatedHumanInputs) != 1 {
		t.Fatalf("first run: ValidatedHumanInputs = %d, want 1", len(ctx.ValidatedHumanInputs))
	}
	// Mutate the approve style to an invalid value, as the reference test
	// mutates style.value in place.
	mutateHumanInputStyle(prog, "approve", "danger")
	ctx.Diagnostics = nil
	(&TargetValidationPass{}).Run(prog, ctx)
	found := false
	for _, d := range ctx.Diagnostics {
		if d.Code == diag.TARGET_NOT_LOWERABLE && d.Message == "Human Input action 'approve' style must be primary, default, accent, or ghost" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want bad-style E1034 on rerun, got %v", ctx.Diagnostics)
	}
	if len(ctx.ValidatedHumanInputs) != 0 {
		t.Fatalf("ValidatedHumanInputs = %d entries, want 0 (stale contract must be dropped)", len(ctx.ValidatedHumanInputs))
	}
}

// ── Agent validation (Task 25d, _validate_agent_call :295-357) ─────

// agentOracleFixture is the brief's agent fixture with the oracle source
// layout (lines/columns in the assertions match /tmp/oracle25d_tie.py).
const agentOracleFixture = `@language nodecoda/1
@mode workflow
function main(string query) -> string {
    let result = agent("provider/model", query, {
        profile: "function-calling-v1",
        instruction: "Answer the user",
        tools: ["clock"]
    });
    return result.text;
}
`

// agentTargetForTest returns the default target with the pinned
// function-calling-v1 agent profile installed (test_agent_translation.py
// _target()).
func agentTargetForTest(t *testing.T) *TargetProfile {
	t.Helper()
	params := []binding.AgentParameterTarget{
		mustAgentParam(t, "model", "model-selector", true),
		mustAgentParam(t, "query", "string", true),
		mustAgentParam(t, "instruction", "string", false),
		mustAgentParam(t, "tools", "tools-selector", true),
	}
	clock, err := binding.NewAgentToolTargetProfile(
		"clock", "builtin-time", "time", "builtin",
		"langgenius/time:1.0.0@sha256:time", "current_time", nil,
	)
	if err != nil {
		t.Fatalf("NewAgentToolTargetProfile: %v", err)
	}
	profile, err := binding.NewAgentTargetProfile(
		"function-calling-v1", "langgenius/agent", "function_calling", "Function Calling",
		"langgenius/agent:1.0.0@sha256:agent", "2",
		params, map[string]*binding.AgentToolTargetProfile{"clock": clock},
	)
	if err != nil {
		t.Fatalf("NewAgentTargetProfile: %v", err)
	}
	tp := DefaultTargetProfile()
	tp.AgentProfiles = map[string]*binding.AgentTargetProfile{"function-calling-v1": profile}
	return tp
}

func mustAgentParam(t *testing.T, name, parameterType string, required bool) binding.AgentParameterTarget {
	t.Helper()
	p, err := binding.NewAgentParameterTarget(name, parameterType, required)
	if err != nil {
		t.Fatalf("NewAgentParameterTarget: %v", err)
	}
	return *p
}

func TestAgentValidation_DefaultTargetRejected(t *testing.T) {
	// Oracle (default target, no agent_profiles): profile-not-installed at
	// 5:18 (the profile string) then tools-not-permitted at 7:16 — the
	// uninstalled profile leaves the allowed-tool set empty, so the pinned
	// clock alias is unknown.
	res := DefaultPipeline().Run(agentOracleFixture, "agent-oracle.ncoda", nil)
	assertTargetValidationErrors(t, res,
		struct {
			line, col int
			message   string
		}{5, 18, "Agent profile 'function-calling-v1' is not installed in target 'dify-1.16-graphon-0.6'"},
		struct {
			line, col int
			message   string
		}{7, 16, "Agent tools are not permitted by the pinned profile: clock"},
	)
}

func TestAgentValidation_ModelNoProvider(t *testing.T) {
	// Oracle: "Agent model must be a static 'provider/model' string with
	// non-empty parts" at 4:24 (the model string literal).
	src := strings.Replace(agentOracleFixture, `agent("provider/model", query`, `agent("model", query`, 1)
	res := DefaultPipeline().Run(src, "agent-oracle.ncoda", agentTargetForTest(t))
	assertTargetValidationError(t, res, 4, 24,
		"Agent model must be a static 'provider/model' string with non-empty parts")
}

func TestAgentValidation_ToolsDuplicate(t *testing.T) {
	// Oracle: "Agent tools must not contain duplicates" at 7:16.
	src := strings.Replace(agentOracleFixture, `tools: ["clock"]`, `tools: ["clock", "clock"]`, 1)
	res := DefaultPipeline().Run(src, "agent-oracle.ncoda", agentTargetForTest(t))
	assertTargetValidationError(t, res, 7, 16, "Agent tools must not contain duplicates")
}

func TestAgentValidation_ToolsUnknown(t *testing.T) {
	// Oracle: "Agent tools are not permitted by the pinned profile: wrench"
	// at 7:16.
	src := strings.Replace(agentOracleFixture, `tools: ["clock"]`, `tools: ["wrench"]`, 1)
	res := DefaultPipeline().Run(src, "agent-oracle.ncoda", agentTargetForTest(t))
	assertTargetValidationError(t, res, 7, 16,
		"Agent tools are not permitted by the pinned profile: wrench")
}

func TestAgentValidation_InstructionEmpty(t *testing.T) {
	// Oracle: "Agent instruction must be a non-empty static string" at 6:22
	// (the instruction string literal).
	src := strings.Replace(agentOracleFixture, `"Answer the user"`, `""`, 1)
	res := DefaultPipeline().Run(src, "agent-oracle.ncoda", agentTargetForTest(t))
	assertTargetValidationError(t, res, 6, 22,
		"Agent instruction must be a non-empty static string")
}

func TestAgentValidation_MissingInstructionKey(t *testing.T) {
	// Oracle: missing-key at 4:49 (config map span) + instruction at 4:49
	// (no instruction expr → config span).
	src := strings.Replace(agentOracleFixture, `        instruction: "Answer the user",
`, "", 1)
	res := DefaultPipeline().Run(src, "agent-oracle.ncoda", agentTargetForTest(t))
	assertTargetValidationErrors(t, res,
		struct {
			line, col int
			message   string
		}{4, 49, "Agent config is missing required key 'instruction'"},
		struct {
			line, col int
			message   string
		}{4, 49, "Agent instruction must be a non-empty static string"},
	)
}

func TestAgentValidation_ExtraKey(t *testing.T) {
	// Oracle: extra-key message at 4:49 (config map span).
	src := strings.Replace(agentOracleFixture, `        tools: ["clock"]`, `        tools: ["clock"],
        extra: 1`, 1)
	res := DefaultPipeline().Run(src, "agent-oracle.ncoda", agentTargetForTest(t))
	assertTargetValidationError(t, res, 4, 49,
		"Agent config has extra key 'extra'; supported keys: profile, instruction, tools")
}

func TestAgentValidation_MissingProfileKey(t *testing.T) {
	// Oracle: three diagnostics — missing-key at 4:49, "Agent profile None
	// is not installed in target ..." at 4:49 (repr of None, config span),
	// tools-not-permitted at 6:16 (the tools array moves up a line).
	src := strings.Replace(agentOracleFixture, `        profile: "function-calling-v1",
`, "", 1)
	res := DefaultPipeline().Run(src, "agent-oracle.ncoda", agentTargetForTest(t))
	assertTargetValidationErrors(t, res,
		struct {
			line, col int
			message   string
		}{4, 49, "Agent config is missing required key 'profile'"},
		struct {
			line, col int
			message   string
		}{4, 49, "Agent profile None is not installed in target 'dify-1.16-graphon-0.6'"},
		struct {
			line, col int
			message   string
		}{6, 16, "Agent tools are not permitted by the pinned profile: clock"},
	)
}

func TestAgentValidation_ProfileNonString(t *testing.T) {
	// Oracle: "Agent profile must be a static string" + "Agent profile None
	// is not installed in target ..." both at 5:18 (the profile expr span),
	// then the tools-not-permitted rejection at 7:16 — the unresolved alias
	// leaves the allowed-tool set empty (3 diagnostics, oracle re-run
	// 2026-08-14 — the first capture showed 2, the stable re-run is 3).
	src := strings.Replace(agentOracleFixture, `profile: "function-calling-v1"`, `profile: 42`, 1)
	res := DefaultPipeline().Run(src, "agent-oracle.ncoda", agentTargetForTest(t))
	assertTargetValidationErrors(t, res,
		struct {
			line, col int
			message   string
		}{5, 18, "Agent profile must be a static string"},
		struct {
			line, col int
			message   string
		}{5, 18, "Agent profile None is not installed in target 'dify-1.16-graphon-0.6'"},
		struct {
			line, col int
			message   string
		}{7, 16, "Agent tools are not permitted by the pinned profile: clock"},
	)
}

func TestAgentValidation_WhiteBoxArity(t *testing.T) {
	// Pre-empted by the type pass E1004 in the full pipeline (oracle
	// arity_two); the whitebox harness reaches the branch. Span is the
	// call expr (4:18).
	src := strings.Replace(agentOracleFixture, `, query, {
        profile: "function-calling-v1",
        instruction: "Answer the user",
        tools: ["clock"]`, `, {
        profile: "function-calling-v1",
        instruction: "Answer the user",
        tools: ["clock"]`, 1)
	ctx, _ := runTargetValidationWhiteBox(t, src, agentTargetForTest(t))
	res := &FrontendResult{Diagnostics: ctx.Diagnostics}
	assertTargetValidationError(t, res, 4, 18, "Agent requires exactly three value arguments")
}

func TestAgentValidation_WhiteBoxConfigNonMap(t *testing.T) {
	// Pre-empted by the type pass E1003 in the full pipeline (oracle
	// config_nonmap); whitebox reaches "Agent config must be a static map
	// literal" at 4:49.
	src := strings.Replace(agentOracleFixture, `{
        profile: "function-calling-v1",
        instruction: "Answer the user",
        tools: ["clock"]
    }`, `"not a map"`, 1)
	ctx, _ := runTargetValidationWhiteBox(t, src, agentTargetForTest(t))
	res := &FrontendResult{Diagnostics: ctx.Diagnostics}
	assertTargetValidationError(t, res, 4, 49, "Agent config must be a static map literal")
}

func TestAgentValidation_WhiteBoxModelNonString(t *testing.T) {
	// Pre-empted by the type pass E1003 in the full pipeline (oracle
	// model_nonstring); whitebox reaches "Agent model must be a static
	// string" at 4:24.
	src := strings.Replace(agentOracleFixture, `agent("provider/model", query`, `agent(42, query`, 1)
	ctx, _ := runTargetValidationWhiteBox(t, src, agentTargetForTest(t))
	res := &FrontendResult{Diagnostics: ctx.Diagnostics}
	assertTargetValidationError(t, res, 4, 24, "Agent model must be a static string")
}

// ── classification call validation (target_validation.py:715-852) ────

// classifySource mirrors the oracle fixture layout exactly (line/col spans
// below are oracle-measured from /tmp/oracle25e_diag.py): the labels map
// block sits on lines 7-11, fallback on 12.
const classifySource = `@language nodecoda/1
@mode workflow
enum Intent { unknown, support, billing }
function main(string query) -> Intent {
    let clasif = classify<Intent>("provider/model", query, {
        instruction: "Classify the request",
        labels: {
            unknown: "Cannot determine intent",
            support: "Technical support request",
            billing: "Billing request"
        },
        fallback: Intent.support
    });
    return clasif;
}
`

// TestClassificationCallValidation locks the target-validation E1034
// branches with oracle-measured spans (each fixture keeps the labels map on
// lines 7-11 so the fallback line stays 12).
func TestClassificationCallValidation(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		line    int
		col     int
		message string
	}{
		{
			name: "model-no-provider",
			src:  replace(classifySource, `classify<Intent>("provider/model"`, `classify<Intent>("model"`),
			line: 5, col: 35,
			message: "Classification model must be a static 'provider/model' string",
		},
		{
			name: "config-non-map",
			src: `@language nodecoda/1
@mode workflow
enum Intent { unknown, support, billing }
function main(string query) -> Intent {
    let clasif = classify<Intent>("provider/model", query, "nope");
    return clasif;
}
`,
			line: 5, col: 60,
			message: "Classification config must be a static map literal",
		},
		{
			name: "config-missing-instruction",
			src:  replace(classifySource, "        instruction: \"Classify the request\",\n", ""),
			line: 5, col: 60,
			message: "Classification config is missing required key 'instruction'",
		},
		{
			name: "config-missing-fallback",
			src: replace(
				replace(classifySource, "        fallback: Intent.support\n", ""),
				"        },\n    });",
				"        }\n    });"),
			line: 5, col: 60,
			message: "Classification config is missing required key 'fallback'",
		},
		{
			name: "instruction-non-string",
			src:  replace(classifySource, `instruction: "Classify the request"`, `instruction: 42`),
			line: 6, col: 22,
			message: "Classification instruction must be a static string",
		},
		{
			name: "labels-non-map",
			src: `@language nodecoda/1
@mode workflow
enum Intent { unknown, support, billing }
function main(string query) -> Intent {
    let clasif = classify<Intent>("provider/model", query, {
        instruction: "Classify the request",
        labels: query,
        fallback: Intent.support
    });
    return clasif;
}
`,
			line: 7, col: 17,
			message: "Classification labels must be a static map literal",
		},
		{
			name: "labels-missing-member",
			src:  replace(classifySource, "            unknown: \"Cannot determine intent\",\n", ""),
			line: 7, col: 17,
			message: "Classification labels are missing enum member 'unknown'",
		},
		{
			name: "labels-extra-member",
			src:  replace(classifySource, `            billing: "Billing request"`, "            billing: \"Billing request\",\n            other: \"Other\""),
			line: 7, col: 17,
			message: "Classification labels contain extra member 'other'",
		},
		{
			name: "label-value-non-string",
			src:  replace(classifySource, `support: "Technical support request"`, `support: 42`),
			line: 9, col: 22,
			message: "Classification label for 'support' must be a static string",
		},
		{
			name: "label-duplicate-description",
			src:  replace(classifySource, `support: "Technical support request"`, `support: "Cannot determine intent"`),
			line: 9, col: 22,
			message: "Classification label description for 'support' duplicates enum member 'unknown'",
		},
		{
			name: "fallback-non-member",
			src:  replace(classifySource, `fallback: Intent.support`, `fallback: 42`),
			line: 12, col: 19,
			message: "Classification fallback must be an explicit member of the same enum",
		},
		{
			name: "fallback-wrong-enum",
			src: replace(
				replace(classifySource,
					"enum Intent { unknown, support, billing }",
					"enum Intent { unknown, support, billing }\nenum Other { x }"),
				"fallback: Intent.support",
				"fallback: Other.x"),
			line: 13, col: 19,
			message: "Classification fallback must be an explicit member of the same enum",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, _ := runTargetValidationWhiteBox(t, c.src, nil)
			for _, d := range ctx.Diagnostics {
				if d.Code == diag.TARGET_NOT_LOWERABLE && d.Line == c.line && d.Column == c.col && d.Message == c.message {
					return
				}
			}
			t.Fatalf("want E1034 %q at %d:%d, got %v", c.message, c.line, c.col, ctx.Diagnostics)
		})
	}
}

// TestClassificationCallPublishesContract locks the positive path: a clean
// classify call publishes exactly one ValidatedClassificationContract on the
// context with the oracle identities (query = args[1] id, fallback first in
// ordered_classes).
func TestClassificationCallPublishesContract(t *testing.T) {
	ctx, prog := runTargetValidationWhiteBox(t, classifySource, nil)
	if len(ctx.Diagnostics) != 0 {
		t.Fatalf("clean classify call produced diagnostics: %v", ctx.Diagnostics)
	}
	if len(ctx.ValidatedClassifications) != 1 {
		t.Fatalf("validated_classifications = %d, want 1", len(ctx.ValidatedClassifications))
	}
	var contract *binding.ValidatedClassificationContract
	for _, c := range ctx.ValidatedClassifications {
		contract = c
	}
	if contract == nil {
		t.Fatal("no published contract")
	}
	if contract.Model != "provider/model" {
		t.Errorf("contract.model = %q, want provider/model", contract.Model)
	}
	if contract.Instruction != "Classify the request" {
		t.Errorf("contract.instruction = %q, want Classify the request", contract.Instruction)
	}
	if contract.FallbackMember != "support" {
		t.Errorf("contract.fallback_member = %q, want support", contract.FallbackMember)
	}
	if contract.EnumType == nil || contract.EnumType.Kind != types.Enum || contract.EnumType.EnumName != "Intent" {
		t.Errorf("contract.enum_type = %v, want Intent enum", contract.EnumType)
	}
	// classes in enum declaration order (unknown/support/billing).
	wantMembers := []string{"unknown", "support", "billing"}
	if len(contract.Classes) != 3 {
		t.Fatalf("contract.classes = %d, want 3", len(contract.Classes))
	}
	for i, want := range wantMembers {
		if contract.Classes[i].Member != want {
			t.Errorf("contract.classes[%d].member = %q, want %q", i, contract.Classes[i].Member, want)
		}
	}
	if contract.Classes[1].Label != "Technical support request" {
		t.Errorf("contract.classes[1].label = %q, want Technical support request", contract.Classes[1].Label)
	}
	// ordered classes put the fallback first (support, unknown, billing).
	ordered := contract.OrderedClasses()
	wantOrdered := []string{"support", "unknown", "billing"}
	if len(ordered) != 3 {
		t.Fatalf("ordered_classes = %d, want 3", len(ordered))
	}
	for i, want := range wantOrdered {
		if ordered[i].Member != want {
			t.Errorf("ordered_classes[%d].member = %q, want %q", i, ordered[i].Member, want)
		}
	}
	// query identity: the call's args[1] (the query ident).
	call := findClassifyCall(t, prog)
	queryID, ok := snapshotID(ctx, call.Args()[1])
	if !ok {
		t.Fatal("query arg has no snapshot id")
	}
	if contract.Query != queryID {
		t.Errorf("contract.query = %d, want %d (args[1])", contract.Query, queryID)
	}
}

// findClassifyCall walks the program for the classify<Intent> call.
func findClassifyCall(t *testing.T, prog *ast.Program) *ast.CallExpr {
	t.Helper()
	var found *ast.CallExpr
	ast.Walk(visitorFunc(func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if ident, ok := call.Func().(*ast.IdentExpr); ok && ident.Name() == "classify" {
				found = call
				return false
			}
		}
		return true
	}), prog)
	if found == nil {
		t.Fatal("no classify call found in program")
	}
	return found
}

// visitorFunc adapts a function to the ast.Visitor interface.
type visitorFunc func(ast.Node) bool

func (f visitorFunc) Visit(node ast.Node) ast.Visitor {
	if f(node) {
		return f
	}
	return nil
}

// ── classification type-pass branches (type_pass.py:1169-1174) ───────

// TestClassificationTypePassBranches locks the type-pass E1003/E1004
// classification branches with oracle-measured spans (type argument at
// 5:27, call at 5:18, query argument at 5:53).
func TestClassificationTypePassBranches(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		code    diag.Code
		line    int
		col     int
		message string
	}{
		{
			name: "type-arg-not-nominal",
			src:  replace(classifySource, `classify<Intent>`, `classify<string>`),
			code: diag.TYPE_MISMATCH,
			line: 5, col: 27,
			message: "classification type argument must be a nominal enum type",
		},
		{
			name: "type-arg-unknown",
			src:  replace(classifySource, `classify<Intent>`, `classify<Missing>`),
			code: diag.TYPE_MISMATCH,
			line: 5, col: 27,
			message: "unknown classification enum 'Missing'",
		},
		{
			name: "arity-zero",
			src: `@language nodecoda/1
@mode workflow
enum Intent { unknown, support, billing }
function main(string query) -> Intent {
    let clasif = classify<Intent>();
    return clasif;
}
`,
			code: diag.ARG_COUNT,
			line: 5, col: 18,
			message: "Operation 'classify' expects 3 to 3 arguments, got 0",
		},
		{
			name: "query-wrong-type",
			src:  replace(classifySource, `function main(string query)`, `function main(int query)`),
			code: diag.TYPE_MISMATCH,
			line: 5, col: 53,
			message: "Operation parameter 'query' expected Type(string), got Type(int)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := runTypePass(t, c.src)
			for _, d := range ctx.Diagnostics {
				if d.Code == c.code && d.Line == c.line && d.Column == c.col && d.Message == c.message {
					return
				}
			}
			t.Fatalf("want %s %q at %d:%d, got %v", c.code.String(), c.message, c.line, c.col, ctx.Diagnostics)
		})
	}
}

// ── extraction (schema-extraction) ────────────────────────────────────

// extractionSource mirrors the oracle fixture layout exactly (line/col
// spans below are oracle-measured on this layout): the extract call opens
// on line 11 with the config map literal from column 68; the descriptions
// map block sits on lines 13-17, strategy on 19.
const extractionSource = `@language nodecoda/1
@mode workflow
type TravelRequest = {
    string city;
    float days;
    bool flexible;
    string[] tags;
}

function main(string text) {
    let extracted = extract<TravelRequest>("provider/model", text, {
        instruction: "Extract a travel request",
        descriptions: {
            city: "Destination city",
            days: "Trip duration in days",
            flexible: "Whether travel dates are flexible",
            tags: "Travel preferences"
        },
        strategy: "prompt"
    });
    output(extracted.value.city);
}
`

// TestExtractionTypePassBranches locks the type-pass schema-extraction
// E1015/E1004 branches with oracle-measured spans (type argument at 11:29
// on the base fixture, call at 11:21).
func TestExtractionTypePassBranches(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		code    diag.Code
		line    int
		col     int
		message string
	}{
		{
			name: "schema-is-enum",
			src: replace(
				replace(extractionSource,
					"type TravelRequest = {",
					"enum TravelMode { air, rail }\ntype TravelRequest = {"),
				`extract<TravelRequest>`, `extract<TravelMode>`),
			code: diag.UNKNOWN_TYPE,
			line: 12, col: 29,
			message: "extraction schema 'TravelMode' must be a record",
		},
		{
			name: "schema-not-nominal",
			src: `@language nodecoda/1
@mode workflow
type TravelRequest = { string city; }
function main(string text) {
    let extracted = extract<string>("provider/model", text, {
        instruction: "Extract",
        descriptions: {city: "City"},
        strategy: "prompt"
    });
    output(extracted.value.city);
}
`,
			code: diag.UNKNOWN_TYPE,
			line: 5, col: 29,
			message: "extraction schema must be a named record type",
		},
		{
			name: "schema-unknown",
			src:  replace(extractionSource, `extract<TravelRequest>`, `extract<Missing>`),
			code: diag.UNKNOWN_TYPE,
			line: 11, col: 29,
			message: "unknown extraction schema 'Missing'",
		},
		{
			name: "schema-empty-record",
			src: replace(extractionSource,
				"string city;\n    float days;\n    bool flexible;\n    string[] tags;", ""),
			code: diag.UNKNOWN_TYPE,
			line: 8, col: 29,
			message: "extraction schema 'TravelRequest' must contain at least one field",
		},
		{
			name: "arity-two-typeargs",
			src:  replace(extractionSource, `extract<TravelRequest>`, `extract<TravelRequest, Missing>`),
			code: diag.ARG_COUNT,
			line: 11, col: 21,
			message: "Operation 'extract' expects exactly one schema type argument, got 2",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := runTypePass(t, c.src)
			for _, d := range ctx.Diagnostics {
				if d.Code == c.code && d.Line == c.line && d.Column == c.col && d.Message == c.message {
					return
				}
			}
			t.Fatalf("want %s %q at %d:%d, got %v", c.code.String(), c.message, c.line, c.col, ctx.Diagnostics)
		})
	}
}

// TestExtractionCallValidation locks the target-validation E1034 branches
// with oracle-measured messages + spans (each fixture keeps the extract
// call on line 11, the config map opening at column 68, the descriptions
// map on lines 13-17 and strategy on 19).
func TestExtractionCallValidation(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		line    int
		col     int
		message string
	}{
		{
			name: "model-bare",
			src:  replace(extractionSource, `"provider/model"`, `"model"`),
			line: 11, col: 44,
			message: "Extraction model must be a static 'provider/model' string with non-empty parts",
		},
		{
			name: "config-non-map",
			src: `@language nodecoda/1
@mode workflow
type TravelRequest = { string city; }
function main(string text) {
    let extracted = extract<TravelRequest>("provider/model", text, "nope");
    output(extracted.value.city);
}
`,
			line: 5, col: 68,
			message: "Extraction config must be one static map literal",
		},
		{
			name: "config-missing-instruction",
			src:  replace(extractionSource, "        instruction: \"Extract a travel request\",\n", ""),
			line: 11, col: 68,
			message: "Extraction config is missing required config key 'instruction'",
		},
		{
			name: "config-missing-descriptions",
			src: replace(extractionSource,
				"        descriptions: {\n            city: \"Destination city\",\n            days: \"Trip duration in days\",\n            flexible: \"Whether travel dates are flexible\",\n            tags: \"Travel preferences\"\n        },\n", ""),
			line: 11, col: 68,
			message: "Extraction config is missing required config key 'descriptions'",
		},
		{
			name: "config-missing-strategy",
			src: replace(
				replace(extractionSource, "        strategy: \"prompt\"\n", ""),
				"        },\n    });",
				"        }\n    });"),
			line: 11, col: 68,
			message: "Extraction config is missing required config key 'strategy'",
		},
		{
			name: "config-unknown-key",
			src: replace(extractionSource, `        strategy: "prompt"`, `        strategy: "prompt",
        bogus: 1`),
			line: 11, col: 68,
			message: "Extraction config has unknown config key 'bogus'; supported keys: instruction, descriptions, strategy, memory, vision",
		},
		{
			name: "instruction-non-string",
			src:  replace(extractionSource, `"Extract a travel request"`, `42`),
			line: 12, col: 22,
			message: "Extraction instruction must be a static string",
		},
		{
			name: "strategy-invalid",
			src:  replace(extractionSource, `"prompt"`, `"bad"`),
			line: 19, col: 19,
			message: "Extraction strategy must be the static string 'prompt' or 'function_call'",
		},
		{
			name: "descriptions-missing-field",
			src:  replace(extractionSource, "            city: \"Destination city\",\n", ""),
			line: 13, col: 23,
			message: "Extraction schema is missing description for field 'city'",
		},
		{
			name: "descriptions-extra-field",
			src: replace(extractionSource, `            tags: "Travel preferences"`, `            tags: "Travel preferences",
            bogus: "x"`),
			line: 13, col: 23,
			message: "Extraction config has extra description for field 'bogus'",
		},
		{
			name: "descriptions-empty",
			src:  replace(extractionSource, `"Destination city"`, `""`),
			line: 14, col: 19,
			message: "Extraction description for field 'city' must be a static non-empty string",
		},
		{
			name: "memory-workflow",
			src: replace(extractionSource, `        strategy: "prompt"`, `        strategy: "prompt",
        memory: 3`),
			line: 11, col: 21,
			message: "Extraction memory requires @mode advanced-chat",
		},
		{
			name: "memory-non-positive",
			src: replace(extractionSource, `        strategy: "prompt"`, `        strategy: "prompt",
        memory: 0`),
			line: 20, col: 17,
			message: "Extraction memory must be a positive integer literal",
		},
		{
			name: "reserved-field",
			src: replace(
				replace(
					replace(extractionSource,
						"string city;",
						"string __is_success;"),
					"            city: \"Destination city\",",
					"            __is_success: \"Status\","),
				"output(extracted.value.city);",
				"output(extracted.value.__is_success);"),
			line: 11, col: 21,
			message: "Extraction field '__is_success' is reserved by the Graphon status contract",
		},
		{
			name: "unsupported-field",
			src: replace(
				replace(
					replace(extractionSource,
						"string city;",
						"map<string, any> extras;"),
					"            city: \"Destination city\",",
					"            extras: \"Extras\","),
				"output(extracted.value.city);",
				"output(extracted.value.extras);"),
			line: 11, col: 21,
			message: "Extraction field 'extras' has unsupported scalar map NodeCoda type for target 'dify-1.16-graphon-0.6'; supported field types: string, float, bool, string[], float[], bool[], map<string, any>[], and required scalar enum fields",
		},
		{
			name: "function-call-uncertified",
			src: replace(
				replace(extractionSource, `"provider/model"`, `"openai/gpt-4o"`),
				`"prompt"`, `"function_call"`),
			line: 11, col: 21,
			message: "Extraction function_call model 'openai/gpt-4o' has no certified tool-call evidence for target 'dify-1.16-graphon-0.6'",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, _ := runTargetValidationWhiteBox(t, c.src, nil)
			for _, d := range ctx.Diagnostics {
				if d.Code == diag.TARGET_NOT_LOWERABLE && d.Line == c.line && d.Column == c.col && d.Message == c.message {
					return
				}
			}
			t.Fatalf("want E1034 %q at %d:%d, got %v", c.message, c.line, c.col, ctx.Diagnostics)
		})
	}
}

// TestExtractionVisionOperand locks the vision operand check (already
// ported for llm/extract_text) in the extraction branch: a non-file vision
// selector is rejected with the shared message (oracle span 9:17 on the
// header-included ImageFacts layout).
func TestExtractionVisionOperand(t *testing.T) {
	ctx, _ := runTargetValidationWhiteBox(t, `@language nodecoda/1
@mode workflow
type ImageFacts = { string label; }
function main(string text) {
    let extracted = extract<ImageFacts>("openai_api_compatible/gpt-5.4", text, {
        instruction: "Read the image and extract its label",
        descriptions: {label: "The exact visible label"},
        strategy: "prompt",
        vision: text
    });
    output(extracted.value.label);
}
`, nil)
	for _, d := range ctx.Diagnostics {
		if d.Code == diag.TARGET_NOT_LOWERABLE && d.Line == 9 && d.Column == 17 && d.Message == "vision requires a selector-backed scalar file for the Dify target" {
			return
		}
	}
	t.Fatalf("want E1034 vision rejection at 9:17, got %v", ctx.Diagnostics)
}

// TestExtractionMemoryBranches locks the memory branches reachable under
// @mode advanced-chat (the Go lowering rejects advanced-chat later, but the
// target-validation memory gates fire here exactly like the reference;
// oracle-measured messages + spans on this layout: extract call at 5:21,
// config map at 5:68).
func TestExtractionMemoryBranches(t *testing.T) {
	base := `@language nodecoda/1
@mode advanced-chat
type TravelRequest = { string city; }
function main(string text) -> string {
    let extracted = extract<TravelRequest>("provider/model", text, {
        instruction: "Extract a travel request",
        descriptions: {city: "Destination city"},
        strategy: "prompt",
        memory: 3
    });
    return extracted.value.city;
}
`
	cases := []struct {
		name    string
		src     string
		line    int
		col     int
		message string
	}{
		{
			name: "limit-exceeded",
			src:  replace(base, "memory: 3", "memory: 100"),
			line: 5, col: 21,
			message: "Extraction memory value 100 exceeds target 'dify-1.16-graphon-0.6' limit 99",
		},
		{
			name: "non-prompt-strategy",
			src: replace(
				replace(base, `"provider/model"`, `"deepseek/deepseek-chat"`),
				`"prompt"`, `"function_call"`),
			line: 5, col: 21,
			message: "Extraction memory currently requires strategy \"prompt\"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, _ := runTargetValidationWhiteBox(t, c.src, nil)
			for _, d := range ctx.Diagnostics {
				if d.Code == diag.TARGET_NOT_LOWERABLE && d.Line == c.line && d.Column == c.col && d.Message == c.message {
					return
				}
			}
			t.Fatalf("want E1034 %q at %d:%d, got %v", c.message, c.line, c.col, ctx.Diagnostics)
		})
	}
}

// TestExtractionCallPublishesContract locks the positive path: a clean
// extract call publishes exactly one ValidatedExtractionContract on the
// context with the oracle identities (schema TravelRequest, strategy
// prompt, 4 certified fields in declaration order, no memory, no vision).
func TestExtractionCallPublishesContract(t *testing.T) {
	ctx, _ := runTargetValidationWhiteBox(t, extractionSource, nil)
	if len(ctx.Diagnostics) != 0 {
		t.Fatalf("clean extract call produced diagnostics: %v", ctx.Diagnostics)
	}
	if len(ctx.ValidatedExtractions) != 1 {
		t.Fatalf("validated_extractions = %d, want 1", len(ctx.ValidatedExtractions))
	}
	var contract *binding.ValidatedExtractionContract
	for _, c := range ctx.ValidatedExtractions {
		contract = c
	}
	if contract == nil {
		t.Fatal("no published contract")
	}
	if contract.Schema == nil || contract.Schema.SchemaName != "TravelRequest" {
		t.Errorf("contract.schema = %+v, want TravelRequest", contract.Schema)
	}
	if contract.Model != "provider/model" {
		t.Errorf("contract.model = %q, want provider/model", contract.Model)
	}
	if contract.Instruction != "Extract a travel request" {
		t.Errorf("contract.instruction = %q, want Extract a travel request", contract.Instruction)
	}
	if contract.Strategy != "prompt" {
		t.Errorf("contract.strategy = %q, want prompt", contract.Strategy)
	}
	if contract.MemoryTurns != nil {
		t.Errorf("contract.memory_turns = %v, want nil", contract.MemoryTurns)
	}
	if contract.Vision {
		t.Errorf("contract.vision = true, want false")
	}
	wantFields := []struct {
		name        string
		graphonType string
		description string
		options     []string
	}{
		{"city", "string", "Destination city", nil},
		{"days", "number", "Trip duration in days", nil},
		{"flexible", "boolean", "Whether travel dates are flexible", nil},
		{"tags", "array[string]", "Travel preferences", nil},
	}
	if len(contract.Fields) != 4 {
		t.Fatalf("contract.fields = %d, want 4", len(contract.Fields))
	}
	for i, want := range wantFields {
		f := contract.Fields[i]
		if f.Name != want.name || f.GraphonType != want.graphonType || f.Description != want.description || !reflect.DeepEqual(f.Options, want.options) {
			t.Errorf("contract.fields[%d] = %+v, want %+v", i, f, want)
		}
	}
	// result type is the closed Extraction[TravelRequest] record.
	if contract.Schema.ResultType.Kind != types.Record || len(contract.Schema.ResultType.Fields) != 4 {
		t.Errorf("result_type = %v, want 4-field Extraction record", contract.Schema.ResultType)
	}
}

// TestExtractionStaticMapBranches locks the staticExtractionMap duplicate
// and non-static-key branches with oracle-measured messages + spans
// (duplicate key at the second key's span; the int-literal key at 19:9 on
// the base fixture, followed by the missing-strategy diagnostic — Python
// emits both in order).
func TestExtractionStaticMapBranches(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		line    int
		col     int
		message string
	}{
		{
			name: "config-duplicate-key",
			src: replace(extractionSource, `        strategy: "prompt"`, `        strategy: "prompt",
        strategy: "prompt"`),
			line: 20, col: 9,
			message: "Extraction config has duplicate config key 'strategy'",
		},
		{
			name: "descriptions-duplicate-field",
			src: replace(extractionSource, `            city: "Destination city",`, `            city: "Destination city",
            city: "Duplicate city",`),
			line: 15, col: 13,
			message: "Extraction config has duplicate description for field 'city'",
		},
		{
			name: "config-non-static-key",
			src:  replace(extractionSource, `        strategy: "prompt"`, `        1: "prompt"`),
			line: 19, col: 9,
			message: "Extraction config keys must be static identifiers or strings",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, _ := runTargetValidationWhiteBox(t, c.src, nil)
			for _, d := range ctx.Diagnostics {
				if d.Code == diag.TARGET_NOT_LOWERABLE && d.Line == c.line && d.Column == c.col && d.Message == c.message {
					return
				}
			}
			t.Fatalf("want E1034 %q at %d:%d, got %v", c.message, c.line, c.col, ctx.Diagnostics)
		})
	}
}

// ── I3: target codec face through the profile ─────────────────────────

// The extraction EXACT-loss certification reads the target codec through
// TargetProfile.Codec (the shared internal/target layer), not a backend
// package default. DefaultTargetProfile wires the real dify codec; a
// hand-built profile with a stub codec flips the certification outcome,
// proving the pass consults the profile's face.
func TestDefaultTargetProfileCarriesCodecFace(t *testing.T) {
	profile := DefaultTargetProfile()
	if profile.Codec == nil {
		t.Fatal("DefaultTargetProfile must carry the target codec face (I3)")
	}
	shape := profile.Codec.ToParameterExtractorField(types.StringType())
	if shape.Loss != codec.LossExact {
		t.Fatalf("string field loss = %v, want exact", shape.Loss)
	}
}

// stubUnsupportedCodec implements codec.Interface with an all-unsupported
// mapping: the profile-injected face must change the certification outcome.
type stubUnsupportedCodec struct{}

func (stubUnsupportedCodec) ToParameterExtractorField(d *types.Descriptor) *codec.Shape {
	return &codec.Shape{Loss: codec.LossUnsupported}
}

func TestExtractionValidationReadsProfileCodecFace(t *testing.T) {
	profile := DefaultTargetProfile()
	profile.Codec = stubUnsupportedCodec{}
	ctx, _ := runTargetValidationWhiteBox(t, extractionSource, profile)
	found := false
	for _, d := range ctx.Diagnostics {
		if d.Code == diag.TARGET_NOT_LOWERABLE && strings.Contains(d.Message, "has unsupported") {
			found = true
		}
	}
	if !found {
		t.Fatalf("stub codec must reject fields the real codec certifies, got %v", ctx.Diagnostics)
	}
}

// ── capability 直调参数透传 file（coze-biz TOOL-DIRECT-CALL-FORM）─────────
//
// Z34_yj_BQB 驱动: 原始工作流把 start.cankao(file) 直接透传给
// capcut.str_to_list 的 obj 参数。工具调用有独立的 tool-lowering 与平台
// file 契约（coze-biz file == URL 字符串），参数是透传而非编译器生成的
// Code 表达式——scalarFileSelectors 不应把工具参数里的 file 标识符当
// "single-file Code input" 拒绝（E1034）。Dify 参考无 capability import，
// 该分支在参考表面不可达（parity 不受影响）。
func TestCapabilityCallFileParamIsPassThrough(t *testing.T) {
	reg, err := tool.NewRegistry(tool.Definition{
		Provider:   "capcut",
		Operation:  "str_to_list",
		Parameters: []tool.Parameter{{Name: "obj", Type: "string", Required: true}},
		Outputs:    []tool.OutputField{{Name: "infos", Type: "array<string>"}},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	src := `@language nodecoda/1
@mode workflow
import "coze-biz.capcut";

function main(file cankao, string fengge) -> any {
    let infos = capcut.str_to_list({ "obj": cankao });
    return { "fengge": fengge, "n": infos.infos };
}
`
	prog := parseSourceOrFail(t, src)
	ctx := NewContext(DefaultTargetProfile())
	ctx.ToolRegistry = reg
	ScopePass{}.Run(prog, ctx)
	(&TypePass{}).Run(prog, ctx)
	ctx.Diagnostics = nil
	(&TargetValidationPass{}).Run(prog, ctx)
	assertNoTargetValidationErrors(t, &FrontendResult{Diagnostics: ctx.Diagnostics})
}
