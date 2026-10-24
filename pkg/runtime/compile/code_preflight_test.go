package compile

// code_preflight_test.go — P6 runtime load 期 foreign-code 预检。
//
// 锁三件事：
//  1. 判定是 load 期、分支无关的：未走分支里的语法错也必须让 Compile 失败；
//  2. trace 记录满足 full-record 契约（check_rule 分支，不编造 fill_rule）；
//  3. synthetic shim 不进入该检查（它由 ParseShim 在 Build 期负责）。

import (
	"context"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

func p6Compiler(recs *[]trace.Record) *Compiler {
	return New(testRegistry(), WithDeps(nodes.Deps{
		SemTrace: func(r trace.Record) { *recs = append(*recs, r) },
	}))
}

// selector(true→text, false→broken code): flag=true never runs the broken
// arm, but load-time well-formedness must still fail.
func TestP6PreflightRejectsUnreachedBranch(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001", Params: []spec.ParamSpec{{Name: "flag", Type: &spec.TypeSpec{Kind: "boolean"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("sel", spec.KindSelector, &spec.SelectorConfig{Conditions: []spec.Condition{
				{Left: spec.Operand{Ref: &spec.Ref{Producer: "100001", Output: "flag"}}, Operator: 1, Right: spec.Operand{Val: true}},
			}, Logic: 1}),
			nodeSpec("ok", spec.KindText, &spec.TextConfig{Method: "const", Template: "true-arm"}),
			nodeSpec("bad", spec.KindCode, &spec.CodeConfig{
				Code:     "async def main(args):\n    ret = {\"a\": 1 b}\n    return ret",
				Language: "python",
				OutOrder: []string{"a"},
			}),
			nodeSpec("agg", spec.KindAggregate, &spec.AggregateConfig{Refs: []spec.Ref{
				{Producer: "ok", Output: "output"}, {Producer: "bad", Output: "a"},
			}}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "agg", Output: wGroup1}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "sel"},
			{Source: "sel", Target: "ok", SourcePort: "true"},
			{Source: "sel", Target: "bad", SourcePort: "false"},
			{Source: "ok", Target: "agg"},
			{Source: "bad", Target: "agg"},
			{Source: "agg", Target: "e1"},
		},
	}
	var recs []trace.Record
	_, err := p6Compiler(&recs).Compile(context.Background(), s)
	if err == nil {
		t.Fatal("Compile accepted a broken code node on an unreached branch")
	}
	d, ok := err.(*diag.Error)
	if !ok || d.Code != diag.RCodeSyntax {
		t.Fatalf("error = %v, want RCODE_SYNTAX", err)
	}
	if len(recs) == 0 {
		t.Fatal("no semantic trace emitted")
	}
	var sawError bool
	for _, r := range recs {
		if err := r.ValidateFull(); err != nil {
			t.Fatalf("trace record does not satisfy full contract: %v (%+v)", err, r)
		}
		if r.RuleID == trace.RuleCodeSyntax && r.Node == "bad" {
			if r.Stage != "load" || r.Decision != "error" || r.CheckRule != "syntax" {
				t.Fatalf("syntax record = %+v, want stage=load decision=error check_rule=syntax", r)
			}
			if r.Provenance == "" {
				t.Fatal("syntax record missing observed interpreter version")
			}
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("no SEM-CODE-001 error record: %+v", recs)
	}
}

func TestP6PreflightPassesValidCodeAndSkipsShims(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("c1", spec.KindCode, &spec.CodeConfig{
				Code:     "async def main(args):\n    return {\"a\": 1}",
				Language: "python",
				OutOrder: []string{"a"},
			}),
			// Synthetic shim: parsed by ParseShim at Build, not here.
			nodeSpec("shim", spec.KindCode, &spec.CodeConfig{
				Code: "def main() -> dict:\n    return {\"result\": 1}", Language: "python", Synthetic: true, OutOrder: []string{"result"},
			}),
			nodeSpec("e1", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "c1", Output: "a"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "c1"},
			{Source: "c1", Target: "e1"},
			{Source: "100001", Target: "shim"},
			{Source: "shim", Target: "e1"},
		},
	}
	var recs []trace.Record
	if _, err := p6Compiler(&recs).Compile(context.Background(), s); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var syntaxPass, importPass, shim bool
	for _, r := range recs {
		if err := r.ValidateFull(); err != nil {
			t.Fatalf("trace record does not satisfy full contract: %v (%+v)", err, r)
		}
		switch {
		case r.RuleID == trace.RuleCodeSyntax && r.Node == "c1" && r.Decision == "passed":
			syntaxPass = true
		case r.RuleID == trace.RuleCodeImport && r.Node == "c1" && r.Decision == "passed":
			importPass = true
		case r.Node == "shim":
			shim = true
		}
	}
	if !syntaxPass || !importPass {
		t.Fatalf("missing pass records: syntax=%v import=%v (%+v)", syntaxPass, importPass, recs)
	}
	if shim {
		t.Fatal("synthetic shim must not enter the foreign-code preflight")
	}
}
