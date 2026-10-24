package spec

// exec_sem_p1a1_test.go — P1a-1 gate evidence for slice E.
//
// Covers:
//   ES-020  declared settingOnError.timeoutMs=60000 → provenance wire-present
//   ES-021  no timeout vs explicit 60000 → wire-absent vs wire-present
//   ES-032a code OutTypes carrier (every declared output port survives)
//   ES-033  fact-carrier roundtrip (Build → Normalize → JSON → DecodeJSON)
//
// ES-060a (minimal trace) lives in internal/runtime/trace/trace_test.go
// because it drives a real node. ES-103a (sidecar loader on the production
// DecodeJSON path) lives in sidecar_test.go.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

const p1a1Head = "@language nodecoda/1\n@mode workflow\n"

// p1a1Graph runs the production frontend → IR → Project chain.
func p1a1Graph(t *testing.T, name, src string) (*ir.Graph, *flow.Projection) {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, name, nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatalf("frontend: %v", res.Errors())
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir-build: %v", err)
	}
	p, err := flow.Project(g)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return g, p
}

func p1a1Build(t *testing.T, name, src string, opts ...BuildOptions) *Spec {
	t.Helper()
	g, p := p1a1Graph(t, name, src)
	s, err := Build(g, p, opts...)
	if err != nil {
		t.Fatalf("spec-build: %v", err)
	}
	return s
}

// p1a1CodeConfigs decodes every code node config in spec order.
func p1a1CodeConfigs(t *testing.T, s *Spec) []*CodeConfig {
	t.Helper()
	var out []*CodeConfig
	for i := range s.Nodes {
		if s.Nodes[i].Kind != KindCode {
			continue
		}
		var cc CodeConfig
		if err := json.Unmarshal(s.Nodes[i].Config, &cc); err != nil {
			t.Fatalf("decode CodeConfig %s: %v", s.Nodes[i].ID, err)
		}
		out = append(out, &cc)
	}
	return out
}

// p1a1TwoOutSource declares (and returns) two output ports on a foreign code
// node. policy is appended verbatim after the block (` with timeout(60s)`).
func p1a1TwoOutSource(policy string) string {
	return p1a1Head + "function main(string x) -> string {\n" +
		"    let c = foreign code python3(string v = x) -> { count: int; value: string; } { source `def main(args):\n" +
		"    return {\"count\": 1, \"value\": args.params[\"v\"]}\n" +
		"`; }" + policy + ";\n" +
		"    return c.value;\n" +
		"}\n"
}

func p1a1ProvenanceRow(t *testing.T, s *Spec, suffix string) *ProvenanceRow {
	t.Helper()
	for i := range s.Provenance {
		if strings.HasSuffix(s.Provenance[i].Field, suffix) {
			return &s.Provenance[i]
		}
	}
	return nil
}

// TestP1a1ES020DeclaredTimeoutProvenance locks origin=wire-present + evidence
// for a declared 60000ms node timeout. Treating it as absent must turn this
// red (the row would be wire-absent).
func TestP1a1ES020DeclaredTimeoutProvenance(t *testing.T) {
	s := p1a1Build(t, "es020.ncoda", p1a1TwoOutSource(" with timeout(60s)"))
	cc := p1a1CodeConfigs(t, s)
	if len(cc) == 0 {
		t.Fatal("no code node in spec")
	}
	var found *CodeConfig
	for _, c := range cc {
		if c.Exception != nil && c.Exception.TimeoutMS == 60000 {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatalf("declared timeout 60000 did not reach CodeConfig.Exception: %+v", cc)
	}

	row := p1a1ProvenanceRow(t, s, ".config.exception.timeout_ms")
	if row == nil {
		t.Fatalf("spec.Provenance has no timeout_ms row: %+v", s.Provenance)
	}
	if row.Origin != OriginWirePresent {
		t.Fatalf("timeout provenance origin = %q, want %q", row.Origin, OriginWirePresent)
	}
	if row.Evidence == "" {
		t.Fatal("timeout provenance has no evidence pointer")
	}

	// The version carrier supplied via BuildOptions must also survive Build.
	if s.SemanticsVersion != "" {
		t.Fatalf("unexpected implicit semantics version %q", s.SemanticsVersion)
	}
}

// TestP1a1ES021WireAbsentVsPresent requires the two nodes to report distinct
// origins (absent vs present). Reading both as policy or both as absent is red.
func TestP1a1ES021WireAbsentVsPresent(t *testing.T) {
	src := p1a1Head + "function main(string x) -> string {\n" +
		"    let a = foreign code python3(string v = x) -> { value: string; } { source `def main(args):\n" +
		"    return {\"value\": args.params[\"v\"]}\n" +
		"`; };\n" +
		"    let b = foreign code python3(string v = a.value) -> { value: string; } { source `def main(args):\n" +
		"    return {\"value\": args.params[\"v\"]}\n" +
		"`; } with timeout(60s);\n" +
		"    return b.value;\n" +
		"}\n"
	s := p1a1Build(t, "es021.ncoda", src)

	var absent, present int
	for _, r := range s.Provenance {
		if !strings.HasSuffix(r.Field, ".config.exception.timeout_ms") {
			continue
		}
		switch r.Origin {
		case OriginWireAbsent:
			absent++
		case OriginWirePresent:
			present++
		default:
			t.Fatalf("unexpected timeout origin %q", r.Origin)
		}
	}
	if absent != 1 || present != 1 {
		t.Fatalf("timeout origins: absent=%d present=%d, want 1/1 (%+v)", absent, present, s.Provenance)
	}
}

// TestP1a1ES032aOutTypesCarrier requires every declared output port (name +
// type) to survive IR→spec. Deleting one port in Build must turn this red.
func TestP1a1ES032aOutTypesCarrier(t *testing.T) {
	s := p1a1Build(t, "es032a.ncoda", p1a1TwoOutSource(""))
	var carrier *CodeConfig
	for _, c := range p1a1CodeConfigs(t, s) {
		if len(c.OutTypes) == 2 {
			carrier = c
			break
		}
	}
	if carrier == nil {
		t.Fatalf("no code node carries 2 OutTypes: %+v", p1a1CodeConfigs(t, s))
	}
	want := map[string]string{"count": "integer", "value": "string"}
	if len(carrier.OutTypes) != len(want) {
		t.Fatalf("OutTypes has %d ports, want %d: %+v", len(carrier.OutTypes), len(want), carrier.OutTypes)
	}
	for name, kind := range want {
		ts, ok := carrier.OutTypes[name]
		if !ok || ts == nil {
			t.Fatalf("OutTypes missing port %q: %+v", name, carrier.OutTypes)
		}
		if ts.Kind != kind {
			t.Fatalf("OutTypes[%q].Kind = %q, want %q", name, ts.Kind, kind)
		}
	}

	// Normalize (the only canonicalizing step) must not drop either port.
	norm, err := s.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	for _, c := range p1a1CodeConfigs(t, norm) {
		if len(c.OutTypes) != 2 {
			continue
		}
		if !reflect.DeepEqual(c.OutTypes, carrier.OutTypes) {
			t.Fatalf("Normalize changed OutTypes: got %+v want %+v", c.OutTypes, carrier.OutTypes)
		}
		return
	}
	t.Fatal("Normalize dropped the two-port OutTypes carrier")
}

// TestP1a1ES033CarrierRoundtrip covers the fact-carrier roundtrip:
// Build → Normalize → Marshal → DecodeJSON must preserve SemanticsVersion,
// Provenance and OutTypes. Dropping any copy in Normalize/DecodeJSON is red.
func TestP1a1ES033CarrierRoundtrip(t *testing.T) {
	explicit := ProvenanceRow{
		Node:     "workflow",
		Field:    "semantics_version",
		Origin:   OriginPolicy,
		Evidence: "hand:docs/five-step/EXEC-SEMANTICS.md:70",
	}
	s := p1a1Build(t, "es033.ncoda", p1a1TwoOutSource(" with timeout(60s)"),
		BuildOptions{SemanticsVersion: "v1", Provenance: []ProvenanceRow{explicit}})
	if s.SemanticsVersion != "v1" {
		t.Fatalf("Build dropped SemanticsVersion: %q", s.SemanticsVersion)
	}

	raw, err := s.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	decoded, err := DecodeJSON(raw)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if decoded.SemanticsVersion != "v1" {
		t.Fatalf("DecodeJSON/Normalize dropped SemanticsVersion: %q", decoded.SemanticsVersion)
	}

	wantRow := p1a1ProvenanceRow(t, s, "semantics_version")
	gotRow := p1a1ProvenanceRow(t, decoded, "semantics_version")
	if wantRow == nil || gotRow == nil {
		t.Fatalf("semantics_version provenance row lost: before=%+v after=%+v", s.Provenance, decoded.Provenance)
	}
	if !reflect.DeepEqual(*wantRow, *gotRow) {
		t.Fatalf("provenance changed across roundtrip: got %+v want %+v", *gotRow, *wantRow)
	}

	var before, after *CodeConfig
	for _, c := range p1a1CodeConfigs(t, s) {
		if len(c.OutTypes) == 2 {
			before = c
		}
	}
	for _, c := range p1a1CodeConfigs(t, decoded) {
		if len(c.OutTypes) == 2 {
			after = c
		}
	}
	if before == nil || after == nil {
		t.Fatal("OutTypes carrier lost across roundtrip")
	}
	if !reflect.DeepEqual(before.OutTypes, after.OutTypes) {
		t.Fatalf("OutTypes changed across roundtrip: got %+v want %+v", after.OutTypes, before.OutTypes)
	}
}

// TestP1a1BuildOptionsTimeoutAxesCarried proves the variadic BuildOptions
// accepts the G4 axis map **and** that P3 consumes it: the injected axis lands
// on the node it names. The key is the node's real id (an unknown id is a
// build error — see validateTimeoutAxes), so the test derives it from the
// projection instead of hard-coding a placeholder.
func TestP1a1BuildOptionsTimeoutAxesCarried(t *testing.T) {
	g, p := p1a1Graph(t, "axes.ncoda", p1a1TwoOutSource(" with timeout(5s)"))
	id := ""
	for _, n := range p.Nodes {
		if n.Kind() == ir.KindCode {
			id = n.NodeID()
			break
		}
	}
	if id == "" {
		t.Fatal("fixture has no code node")
	}
	s, err := Build(g, p, BuildOptions{
		TimeoutAxes: map[string]ExceptionTimeoutAxis{id: TimeoutAxisTotal},
	})
	if err != nil {
		t.Fatalf("Build with TimeoutAxes: %v", err)
	}
	if s == nil || len(s.Nodes) == 0 {
		t.Fatal("Build returned an empty spec")
	}
	var node *NodeSpec
	for i := range s.Nodes {
		if s.Nodes[i].ID == id {
			node = &s.Nodes[i]
			break
		}
	}
	if node == nil {
		t.Fatalf("spec has no node %q", id)
	}
	cfg, derr := DecodeConfig(node.Kind, node.Config)
	if derr != nil {
		t.Fatalf("decode %s: %v", id, derr)
	}
	cc, ok := cfg.(*CodeConfig)
	if !ok || cc.Exception == nil {
		t.Fatalf("code config/exception missing: %#v", cfg)
	}
	if cc.Exception.TimeoutAxis != string(TimeoutAxisTotal) {
		t.Fatalf("TimeoutAxis = %q, want %q (P3 consumer must project the axis map)",
			cc.Exception.TimeoutAxis, TimeoutAxisTotal)
	}
	// The axis is a projection of the caller's map, not a hard-coded kind
	// constant: injecting per_attempt must flip the same node.
	s2, err := Build(g, p, BuildOptions{
		TimeoutAxes: map[string]ExceptionTimeoutAxis{id: TimeoutAxisPerAttempt},
	})
	if err != nil {
		t.Fatalf("Build with per_attempt: %v", err)
	}
	for i := range s2.Nodes {
		if s2.Nodes[i].ID != id {
			continue
		}
		c2, derr := DecodeConfig(s2.Nodes[i].Kind, s2.Nodes[i].Config)
		if derr != nil {
			t.Fatalf("decode %s: %v", id, derr)
		}
		if got := c2.(*CodeConfig).Exception.TimeoutAxis; got != string(TimeoutAxisPerAttempt) {
			t.Fatalf("TimeoutAxis = %q, want %q", got, TimeoutAxisPerAttempt)
		}
	}
}
