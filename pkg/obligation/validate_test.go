package obligation

import (
	"reflect"
	"testing"
)

type fakeView struct {
	nodes []string
	edges []EdgeView
	refs  []RefView
}

func (v fakeView) NodeIDs() []string { return v.nodes }
func (v fakeView) Edges() []EdgeView { return v.edges }
func (v fakeView) Refs() []RefView   { return v.refs }

func mapped(id string) IDMap {
	pid := id
	return IDMap{ProductID: &pid, State: IDMapped}
}

func cleanPlan() *Plan {
	return &Plan{
		Schema: PlanSchemaV2,
		Semantics: &Semantics{
			Constructs: []Construct{{
				ID:   "100012",
				Kind: "if",
				Arms: []ArmPlan{
					{Name: "then", Port: "true", Entry: strptr("100013"), Exits: []string{"100013"}, Terminal: "flow"},
					{Name: "else", Port: "false", Exits: []string{}, Empty: true, Terminal: "none"},
				},
				Merge: &MergePlan{
					ID: "100020",
					ValueClosure: map[string]ValueClosure{
						"Group1": {Kind: ClosureAlias, Candidates: []ValueCandidate{{Producer: "100013", Output: "v4"}}},
					},
				},
			}},
			Obligations: []Obligation{{
				Selector: "100012", Port: "false", Group: "Group1",
				Target: "100020", TargetKind: "merge",
				Candidates: []ValueCandidate{{Producer: "100013", Output: "v4"}},
				Status:     ObligationClosed,
			}},
			Refs: []RefFact{},
			Nodes: []NodeFact{
				{ID: "100012", Kind: "if", Reachable: true},
				{ID: "100013", Kind: "text", Reachable: true},
				{ID: "100020", Kind: "merge", Reachable: true},
				{ID: "100021", Kind: "text", Reachable: true},
			},
		},
	}
}

func cleanReport() *Report {
	return &Report{
		Schema: ReportSchemaV2,
		Target: "coze-biz-yaml",
		IDMap: map[string]IDMap{
			"100012": mapped("100012"),
			"100013": mapped("100013"),
			"100020": mapped("100020"),
			"100021": mapped("100021"),
		},
		Constructs: []ReportConstruct{{ID: "100012", MergeMode: "materialized"}},
		Nodes: []ReportNode{
			{ID: "100012", Reachable: true},
			{ID: "100020", Reachable: true, Kind: "merge"},
		},
		Edges: []EdgeObs{{
			Selector: "100012", Port: "false", Group: "Group1",
			Target: "100020", TargetKind: "merge", CanonicalLanding: "100021",
			Candidates: []ValueCandidate{{Producer: "100013", Output: "v4"}},
			EdgeCount:  1,
		}},
		LoopEdges:    []LoopEdgeObs{},
		EmittedNodes: []EmittedNode{},
		Refs:         []ReportRef{},
		Rewrites:     []Rewrite{},
		Violations:   []Violation{},
	}
}

func cleanView() GraphView {
	return fakeView{
		nodes: []string{"100012", "100013", "100020", "100021"},
		edges: []EdgeView{
			{Source: "100012", Target: "100013", SourcePort: "true"},
			{Source: "100013", Target: "100020"},
			{Source: "100020", Target: "100021"},
		},
	}
}

func strptr(s string) *string { return &s }

func hasCode(vs []Violation, code string) bool {
	for _, v := range vs {
		if v.Code == code {
			return true
		}
	}
	return false
}

func TestValidateCleanPlan(t *testing.T) {
	got := Validate(cleanView(), cleanPlan(), cleanReport())
	if len(got) != 0 {
		t.Fatalf("Validate(clean) = %#v, want no violations", got)
	}
}

func TestValidateFindsDanglingObligationAndBadIDMap(t *testing.T) {
	plan := cleanPlan()
	plan.Semantics.Obligations[0].Status = ObligationOpen
	report := cleanReport()
	report.Schema = "ncoda.obligation-report/v1"
	delete(report.IDMap, "100012")

	got := Validate(nil, plan, report)
	if !hasCode(got, CodeArmPortDangling) {
		t.Errorf("missing %s in %#v", CodeArmPortDangling, got)
	}
	if !hasCode(got, CodeIDMapInvalid) {
		t.Errorf("missing %s in %#v", CodeIDMapInvalid, got)
	}
	// Deterministic: rerun yields the identical ordered list.
	if again := Validate(nil, plan, report); !reflect.DeepEqual(got, again) {
		t.Errorf("Validate is not deterministic:\nfirst  %#v\nsecond %#v", got, again)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Code > got[i].Code {
			t.Fatalf("violations are not ordered by code: %q before %q", got[i-1].Code, got[i].Code)
		}
	}
}

func TestValidateFindsMergeAliasCycle(t *testing.T) {
	plan := cleanPlan()
	plan.Semantics.Constructs = []Construct{
		{ID: "10", Kind: "if", Merge: &MergePlan{ID: "M1", ValueClosure: map[string]ValueClosure{
			"g": {Kind: ClosureAlias, Candidates: []ValueCandidate{{Producer: "M2", Output: "g"}}},
		}}},
		{ID: "11", Kind: "if", Merge: &MergePlan{ID: "M2", ValueClosure: map[string]ValueClosure{
			"g": {Kind: ClosureAlias, Candidates: []ValueCandidate{{Producer: "M1", Output: "g"}}},
		}}},
	}
	got := Validate(nil, plan, nil)
	if !hasCode(got, CodeMergeAliasCycle) {
		t.Fatalf("missing %s in %#v", CodeMergeAliasCycle, got)
	}
}

func TestValidateFindsUnreachableDuplicateEdgeAndUnresolvedRef(t *testing.T) {
	view := fakeView{
		nodes: []string{"a", "b", "c"},
		edges: []EdgeView{
			{Source: "a", Target: "b", SourcePort: "true"},
			{Source: "a", Target: "b", SourcePort: "true"},
		},
		refs: []RefView{{Producer: "ghost", Output: "v", Consumer: "a"}},
	}
	got := Validate(view, cleanPlan(), nil)
	for _, want := range []string{CodeUnreachableNode, CodeArmPortDuplicate, CodeRefUnresolved} {
		if !hasCode(got, want) {
			t.Errorf("missing %s in %#v", want, got)
		}
	}
}

func TestValidateNilPlanIsLoud(t *testing.T) {
	got := Validate(nil, nil, nil)
	if len(got) != 1 || got[0].Code != CodePlanDiverged {
		t.Fatalf("Validate(nil plan) = %#v, want one %s", got, CodePlanDiverged)
	}
}

func TestGraphViewInterface(t *testing.T) {
	var v GraphView = fakeView{nodes: []string{"a"}}
	if got := v.NodeIDs(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("NodeIDs() = %v", got)
	}
	if v.Edges() != nil || v.Refs() != nil {
		t.Fatal("empty fakeView should expose nil edges/refs")
	}
}
