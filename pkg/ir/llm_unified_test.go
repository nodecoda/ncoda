package ir_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

func TestLLMIRCarriesSemanticResultContract(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type Answer = { string answer; int score; };
function main(string prompt) -> string {
  let r = llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]});
  return r.text.answer;
}`
	r := passes.DefaultPipeline().Run(src, "llm-ir.ncoda", nil)
	if r.HasErrors() {
		t.Fatalf("pipeline: %v", r.Diagnostics)
	}
	g, err := ir.Build(r.Program)
	if err != nil {
		t.Fatal(err)
	}
	var call *ir.LLMCall
	var walk func(*ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, st := range b.Stmts {
			switch x := st.(type) {
			case *ir.NodeStmt:
				if c, ok := x.Node.(*ir.LLMCall); ok {
					call = c
				}
			case *ir.IfStmt:
				walk(x.Then)
				walk(x.Else)
			case *ir.AttemptStmt:
				walk(x.Success)
				walk(x.Failure)
			case *ir.LoopStmt:
				walk(x.Body)
			case *ir.ParallelStmt:
				for _, br := range x.Branches {
					walk(br)
				}
			}
		}
	}
	walk(g.Root)
	if call == nil || call.Result == nil || call.Result.PayloadSchema == nil {
		t.Fatalf("missing semantic result contract: %#v", call)
	}
	if call.Result.WholeEnvelopeConsumed {
		t.Fatal("member-only access must not mark the whole LLM envelope consumed")
	}
	if call.Result.PayloadSchema.Kind != "record" || len(call.Result.PayloadSchema.Fields) != 2 {
		t.Fatalf("payload schema = %#v", call.Result.PayloadSchema)
	}
	if _, ok := call.OutTypes["answer"]; ok {
		t.Fatalf("record field leaked into physical OutTypes: %#v", call.OutTypes)
	}
	if call.OutTypes["text"] == nil {
		t.Fatalf("missing transitional text output: %#v", call.OutTypes)
	}
	end := g.Root.Stmts[len(g.Root.Stmts)-1].(*ir.ReturnStmt).End
	if got := end.Inputs[0].Output; got != "text.answer" {
		t.Fatalf("semantic result ref = %q, want text.answer", got)
	}
}

func TestLLMIRDistinguishesWholeEnvelopeFromText(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type Answer = { string answer; };
function main(string prompt) {
  let r = llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]});
  return r;
}`
	r := passes.DefaultPipeline().Run(src, "llm-whole-envelope-ir.ncoda", nil)
	if r.HasErrors() {
		t.Fatalf("pipeline: %v", r.Diagnostics)
	}
	g, err := ir.Build(r.Program)
	if err != nil {
		t.Fatal(err)
	}
	end := g.Root.Stmts[len(g.Root.Stmts)-1].(*ir.ReturnStmt).End
	if got := end.Inputs[0].Output; got != "response" {
		t.Fatalf("whole envelope ref = %q, want response (distinct from text)", got)
	}
}

func TestLLMIRPolicyCallKeepsWholeEnvelopeBinding(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type Answer = { string answer; };
function main(string prompt) {
  let r = llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]}) with retry(max: 1, interval: 1s);
  return r;
}`
	r := passes.DefaultPipeline().Run(src, "llm-policy-whole-envelope-ir.ncoda", nil)
	if r.HasErrors() {
		t.Fatalf("pipeline: %v", r.Diagnostics)
	}
	g, err := ir.Build(r.Program)
	if err != nil {
		t.Fatal(err)
	}
	end := g.Root.Stmts[len(g.Root.Stmts)-1].(*ir.ReturnStmt).End
	if got := end.Inputs[0].Output; got != ir.LLMEnvelopeOutput {
		t.Fatalf("policy-wrapped whole envelope ref = %q, want %q", got, ir.LLMEnvelopeOutput)
	}
}

func TestLLMIRAttemptCarriesSemanticResultContract(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type Answer = { string answer; };
function main(string prompt) -> string {
  attempt llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]}) as r {
    success { return r.text.answer; }
    failure(err) { return "failed"; }
  }
}`
	r := passes.DefaultPipeline().Run(src, "llm-attempt-ir.ncoda", nil)
	if r.HasErrors() {
		t.Fatalf("pipeline: %v", r.Diagnostics)
	}
	g, err := ir.Build(r.Program)
	if err != nil {
		t.Fatal(err)
	}
	stmt, ok := g.Root.Stmts[0].(*ir.AttemptStmt)
	if !ok {
		t.Fatalf("root stmt = %T, want *ir.AttemptStmt", g.Root.Stmts[0])
	}
	call, ok := stmt.Op.(*ir.LLMCall)
	if !ok {
		t.Fatalf("attempt op = %T, want *ir.LLMCall", stmt.Op)
	}
	if call.Result == nil || call.Result.PayloadSchema == nil {
		t.Fatalf("missing semantic result contract: %#v", call)
	}
	if call.Result.PayloadSchema.Kind != "record" || len(call.Result.PayloadSchema.Fields) != 1 {
		t.Fatalf("payload schema = %#v", call.Result.PayloadSchema)
	}
	if call.OutTypes["text"] == nil {
		t.Fatalf("missing transitional text output: %#v", call.OutTypes)
	}
}

func TestLLMMetadataMaterializerKeepsPhysicalScalarTypes(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type Answer = { string answer; };
function main(string prompt) -> string {
  let r = llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]});
  return r.reasoning_content ?? r.text.answer;
}`
	r := passes.DefaultPipeline().Run(src, "llm-metadata-ir.ncoda", nil)
	if r.HasErrors() {
		t.Fatalf("pipeline: %v", r.Diagnostics)
	}
	g, err := ir.Build(r.Program)
	if err != nil {
		t.Fatal(err)
	}
	var materializer *ir.CodeRun
	for _, st := range g.Root.Stmts {
		if ns, ok := st.(*ir.NodeStmt); ok {
			if code, ok := ns.Node.(*ir.CodeRun); ok {
				materializer = code
			}
		}
	}
	if materializer == nil {
		t.Fatal("missing metadata coalesce materializer")
	}
	inputType := func(output string) *ir.Type {
		for _, input := range materializer.Inputs {
			if input.Name == output && len(input.Path) > 0 {
				return materializer.Types[input.Path[0]]
			}
		}
		return nil
	}
	if got := inputType("reasoning_content"); got == nil || got.Kind != "string" {
		t.Fatalf("reasoning_content input type = %#v, want string (inputs=%#v types=%#v)", got, materializer.Inputs, materializer.Types)
	}
	if got := inputType("text.answer"); got == nil || got.Kind != "string" {
		t.Fatalf("text.answer input type = %#v, want string (inputs=%#v types=%#v)", got, materializer.Inputs, materializer.Types)
	}
}
