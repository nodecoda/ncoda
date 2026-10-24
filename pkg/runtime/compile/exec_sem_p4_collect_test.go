package compile

// exec_sem_p4_collect_test.go — P4 gate evidence for ES-045 (loop collection).
//
// Three-state contract for one loop iteration's output:
//
//	absent_skip — the body did not emit the port ⇒ no element
//	null_append — the body emitted an explicit null and the declared element
//	              kind can hold null ⇒ a null element
//	zero_append — the body emitted the type zero ("" / 0 / false / empty
//	              container) ⇒ the zero element is kept
//	null_error  — explicit null into a scalar element kind ⇒
//	              RNULL_IN_NON_NULLABLE + collect_rule=null_error
//
// The harness runs the real loop driver against a stubbed tool body, so the
// decision is made on the production collection path.

import (
	"context"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// p4CollectSpec builds the loop: body = one stub tool whose returned map
// depends on the bound item.
func p4CollectSpec(elemKind string) *spec.Spec {
	children := []spec.NodeSpec{
		nodeSpec("100003", spec.KindTool, &spec.ToolConfig{
			ToolName: "emit", ProviderID: "p",
			Params: []spec.ToolParam{{Key: "item", Ref: &spec.Source{BlockID: "100002", Name: "item"}}},
		}),
	}
	inner := []spec.EdgeSpec{
		{Source: "100002", Target: "100003", SourcePort: wLoopFunctionInlineOutput},
		{Source: "100003", Target: "100002", TargetPort: wLoopFunctionInlineInput},
	}
	loopCfg := &spec.LoopConfig{
		VarName:  "item",
		LoopType: "by_array",
		Inputs:   []spec.Source{{Path: []string{"items"}, BlockID: "100001", Name: "items"}},
		OutTypes: map[string]*spec.TypeSpec{
			"obj_list": {Kind: "list", Elem: &spec.TypeSpec{Kind: elemKind}},
			"s_list":   {Kind: "list", Elem: &spec.TypeSpec{Kind: "string"}},
			"n_list":   {Kind: "list", Elem: &spec.TypeSpec{Kind: "integer"}},
		},
		OutputSources: []spec.Source{
			{Path: []string{"obj_list"}, BlockID: "100003", Name: "obj"},
			{Path: []string{"s_list"}, BlockID: "100003", Name: "s"},
			{Path: []string{"n_list"}, BlockID: "100003", Name: "n"},
		},
		Children:   children,
		InnerEdges: inner,
	}
	return &spec.Spec{
		Version: spec.Version,
		Entry: &spec.EntrySpec{ID: "100001",
			Params: []spec.ParamSpec{{Name: "items", Type: &spec.TypeSpec{Kind: "list"}}}},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("100002", spec.KindLoop, loopCfg),
			nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Outputs: []spec.EndOutput{
				{Name: "obj_list", Ref: spec.Ref{Producer: "100002", Output: "obj_list"}},
				{Name: "s_list", Ref: spec.Ref{Producer: "100002", Output: "s_list"}},
				{Name: "n_list", Ref: spec.Ref{Producer: "100002", Output: "n_list"}},
			}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "100002"},
			{Source: "100002", Target: "900001"},
		},
	}
}

// p4CollectBody is the stubbed body: the emitted map is the whole scenario.
func p4CollectBody(t *testing.T) nodes.Deps {
	t.Helper()
	pm := plugin.New()
	if err := pm.Register(&plugin.Plugin{
		ID: "p", Type: plugin.PluginCallable,
		Tools: []plugin.Tool{{Name: "emit", Call: func(_ context.Context, args map[string]any) (any, error) {
			switch args["item"] {
			case "absent":
				return map[string]any{}, nil // port not emitted at all
			case "null":
				return map[string]any{"obj": nil}, nil // explicit null
			case "zero":
				return map[string]any{"s": ""}, nil // explicit zero
			default:
				return map[string]any{"obj": map[string]any{"k": 1}, "n": 5}, nil
			}
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	return nodes.Deps{Tools: pm}
}

func p4CollectTrace(t *testing.T) (func(trace.Record), *[]trace.Record) {
	t.Helper()
	var recs []trace.Record
	return func(r trace.Record) {
		if err := r.Validate(); err != nil {
			t.Fatalf("invalid trace record: %v (%+v)", err, r)
		}
		recs = append(recs, r)
	}, &recs
}

func TestExecSemP4ES045LoopCollectThreeStates(t *testing.T) {
	deps := p4CollectBody(t)
	emit, recs := p4CollectTrace(t)
	deps.SemTrace = emit
	r := mustCompile(t, New(nodes.StdRegistry(), WithDeps(deps)), p4CollectSpec("object"))
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{
		"items": []any{"absent", "null", "zero", "value"},
	}))

	// absent item contributes nothing; the explicit null is an element.
	obj, _ := out["obj_list"].([]any)
	if len(obj) != 2 || obj[0] != nil {
		t.Fatalf("obj_list = %#v, want [nil, {k:1}] (absent skipped, null appended)", out["obj_list"])
	}
	if m, ok := obj[1].(map[string]any); !ok || m["k"] != 1 {
		t.Fatalf("obj_list[1] = %#v, want {k:1}", obj[1])
	}
	// The explicit zero is kept (not skipped) because it was produced.
	s, _ := out["s_list"].([]any)
	if len(s) != 1 || s[0] != "" {
		t.Fatalf("s_list = %#v, want [\"\"] (zero appended)", out["s_list"])
	}
	// Only the value item emitted n.
	n, _ := out["n_list"].([]any)
	if len(n) != 1 || n[0] != 5 {
		t.Fatalf("n_list = %#v, want [5] (absent iterations skipped)", out["n_list"])
	}

	rules := map[string]int{}
	for _, rec := range *recs {
		if rec.RuleID != trace.RuleCollect {
			continue
		}
		if rec.Node != "100002" || rec.Kind != spec.KindLoop {
			t.Fatalf("collect trace node/kind = %s/%s, want 100002/%s", rec.Node, rec.Kind, spec.KindLoop)
		}
		rules[rec.CollectRule]++
	}
	for _, want := range []string{"absent_skip", "null_append", "zero_append"} {
		if rules[want] == 0 {
			t.Fatalf("collect_rule %q never recorded: %v", want, rules)
		}
	}
	if rules["null_error"] != 0 {
		t.Fatalf("unexpected null_error for a null-permitting element kind: %v", rules)
	}
}

// TestExecSemP4ES045NullIsNeverConflatedWithAbsent is the boundary test for
// the one arm ES-045 cannot express: `null_error` for `array<integer>`.
//
// ncoda's type system has no nullability carrier (ir.Type / spec.TypeSpec carry
// kind + required only), so `array<null|T>` and `array<integer>` produce the
// same TypeSpec. A kind-based rejection would also break ES-040/AC7: the
// platform lets a null element through the loop and repairs it at the
// downstream code-input prefill (`PreFillZero` on `array<int>`), which is
// corpus-observed. The collector therefore appends the null and records
// `null_append`; the unimplemented arm is documented, not guessed.
func TestExecSemP4ES045NullIsNeverConflatedWithAbsent(t *testing.T) {
	deps := p4CollectBody(t)
	emit, recs := p4CollectTrace(t)
	deps.SemTrace = emit
	r := mustCompile(t, New(nodes.StdRegistry(), WithDeps(deps)), p4CollectSpec("integer"))

	out := UnwrapOutput(mustInvoke(t, r, map[string]any{
		"items": []any{"absent", "null"},
	}))
	obj, _ := out["obj_list"].([]any)
	// absent ⇒ no element; null ⇒ one null element (even for integer elem).
	if len(obj) != 1 || obj[0] != nil {
		t.Fatalf("obj_list = %#v, want [nil] (absent skipped, explicit null appended)", out["obj_list"])
	}
	var sawAbsent, sawNull bool
	for _, rec := range *recs {
		if rec.RuleID != trace.RuleCollect {
			continue
		}
		switch rec.CollectRule {
		case "absent_skip":
			sawAbsent = true
		case "null_append":
			sawNull = true
		case "null_error":
			t.Fatalf("collector guessed a nullability rule the type system cannot express: %+v", rec)
		}
	}
	if !sawAbsent || !sawNull {
		t.Fatalf("collect rules absent/null not distinguished: absent=%v null=%v", sawAbsent, sawNull)
	}
}
