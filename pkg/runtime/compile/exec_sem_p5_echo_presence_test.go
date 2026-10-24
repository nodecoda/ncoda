package compile

// exec_sem_p5_echo_presence_test.go — ES-045 regression.
//
// A context echo nests the producer namespace as a map, so an explicit null
// member is *present*. Before P5 the nested lookup treated `val == nil` as
// absent, which silently converted an explicit null into absent_skip in the
// loop collector.

import "testing"

func TestResolveRefPresentNestedEchoExplicitNull(t *testing.T) {
	merged := map[string]any{
		"branchSource": map[string]any{
			"inner": map[string]any{
				"producer": map[string]any{"out": nil},
			},
		},
	}
	val, ok := resolveRefPresent(merged, "producer", "out")
	if !ok {
		t.Fatal("nested echo explicit null reported as absent")
	}
	if val != nil {
		t.Fatalf("value = %#v, want nil", val)
	}
	// A port that the producer really does not carry stays absent.
	if _, ok := resolveRefPresent(merged, "producer", "missing"); ok {
		t.Fatal("absent nested port reported as present")
	}
	// An unknown producer stays absent.
	if _, ok := resolveRefPresent(merged, "other", "out"); ok {
		t.Fatal("unknown producer reported as present")
	}
}

func TestResolveRefPresentDirectProducerExplicitNull(t *testing.T) {
	merged := map[string]any{"producer": map[string]any{"out": nil}}
	if _, ok := resolveRefPresent(merged, "producer", "out"); !ok {
		t.Fatal("direct explicit null reported as absent")
	}
	// resolveRef (value-only) keeps returning nil for both cases.
	if got := resolveRef(merged, "producer", "missing"); got != nil {
		t.Fatalf("resolveRef missing = %#v, want nil", got)
	}
}

// resolveRef keeps first-producer-wins: when the first (sorted) echo carries
// the producer namespace but not the port, it must not fall through to a later
// echo that happens to have a value.
func TestResolveRefFirstProducerWinsAcrossEchoes(t *testing.T) {
	merged := map[string]any{
		"a_echo": map[string]any{"producer": map[string]any{"other": 1}},
		"b_echo": map[string]any{"producer": map[string]any{"out": 2}},
	}
	if got := resolveRef(merged, "producer", "out"); got != nil {
		t.Fatalf("resolveRef fell through to a later echo: %#v", got)
	}
	if _, ok := resolveRefPresent(merged, "producer", "out"); ok {
		t.Fatal("presence scan fell through to a later echo")
	}
}
