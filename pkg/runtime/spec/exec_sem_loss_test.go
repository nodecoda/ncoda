package spec

// exec_sem_loss_test.go —— ES-070 / ES-091 seeded spec/IR loss negative
// controls.
//
// These tests are **negative controls**: they pass when the fact ledger
// correctly detects a seeded loss (verdict=red) at a precise node/port.
// They must not be satisfied by editing a checker copy: every candidate is
// fed through the production loader `DecodeJSON → Normalize → Marshal`.
//
// Fixtures are valid spec.v2 documents under
// testdata/exec-semantics/spec/. The `with_*` fixture declares the fact; the
// `missing_*` fixture is the same node with the fact deleted (the seed).
// `out_types` on a code node and `headers` on an http node are IR-side
// declarations that HEAD's spec DTO does not carry (PRD C11/C3) — the ledger
// reports those losses on the production path even when the fixture still
// declares them. `loop.out_types` and `exception` are carried today, so for
// those the seed is what makes the loss appear.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// fact is one declared execution fact. Key identity is node+field+port; the
// value is the declared payload (for diagnostics only).
type fact struct {
	Node  string
	Field string // out_types | headers | exception
	Port  string // out_types key, header name, or "exception"
	Value string
}

func (f fact) key() string {
	return f.Node + "." + f.Field + "[" + f.Port + "]"
}

// ledgerFacts reads the raw JSON fact surface. It intentionally sees fields
// that the spec DTO drops, because the source side of the ledger is the
// declaration surface (IR/spec node), not the normalized execution subset.
func ledgerFacts(t *testing.T, raw []byte) map[string]fact {
	t.Helper()
	var doc struct {
		Version string `json:"version"`
		Nodes   []struct {
			ID     string          `json:"id"`
			Kind   string          `json:"kind"`
			Config json.RawMessage `json:"config"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("ledger: decode spec: %v", err)
	}
	out := map[string]fact{}
	for _, n := range doc.Nodes {
		var cfg map[string]any
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			t.Fatalf("ledger: node %s config: %v", n.ID, err)
		}
		// out_types: one fact per declared output port.
		if rawOT, ok := cfg["out_types"]; ok && rawOT != nil {
			ot, ok := rawOT.(map[string]any)
			if !ok {
				t.Fatalf("ledger: node %s out_types is not an object: %T", n.ID, rawOT)
			}
			for port, typ := range ot {
				f := fact{Node: n.ID, Field: "out_types", Port: port, Value: typeKind(typ)}
				out[f.key()] = f
			}
		}
		// headers / params: object form {name: value} or array form
		// [{name, ...}] (the canonical spec DTO is the ordered array).
		for _, field := range []string{"headers", "params"} {
			rawKV, ok := cfg[field]
			if !ok || rawKV == nil {
				continue
			}
			switch h := rawKV.(type) {
			case map[string]any:
				for name, val := range h {
					f := fact{Node: n.ID, Field: field, Port: name, Value: fmt.Sprint(val)}
					out[f.key()] = f
				}
			case []any:
				for _, item := range h {
					m, _ := item.(map[string]any)
					if m == nil {
						continue
					}
					name, _ := m["name"].(string)
					if name == "" {
						continue
					}
					f := fact{Node: n.ID, Field: field, Port: name, Value: fmt.Sprint(m["value"])}
					out[f.key()] = f
				}
			default:
				t.Fatalf("ledger: node %s %s has unsupported shape %T", n.ID, field, rawKV)
			}
		}
		// exception: presence is the fact (the DTO carries the payload).
		if rawE, ok := cfg["exception"]; ok && rawE != nil {
			f := fact{Node: n.ID, Field: "exception", Port: "exception"}
			out[f.key()] = f
		}
	}
	return out
}

func typeKind(v any) string {
	if m, ok := v.(map[string]any); ok {
		if k, ok := m["kind"].(string); ok {
			return k
		}
	}
	return ""
}

// ledgerLoss returns the source facts absent from target, sorted. A non-empty
// result is a red verdict for the seeded-loss control.
func ledgerLoss(source, target map[string]fact) []string {
	var lost []string
	for k := range source {
		if _, ok := target[k]; !ok {
			lost = append(lost, k)
		}
	}
	sort.Strings(lost)
	return lost
}

func readSpecFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "exec-semantics", "spec", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return raw
}

// productionNormalize runs the production loader path and returns canonical
// normalized bytes. A failure here is a real production failure, not a
// fixture-only failure.
func productionNormalize(t *testing.T, raw []byte) []byte {
	t.Helper()
	s, err := DecodeJSON(raw)
	if err != nil {
		t.Fatalf("production DecodeJSON: %v", err)
	}
	b, err := s.Marshal()
	if err != nil {
		t.Fatalf("production Marshal: %v", err)
	}
	return b
}

type lossCase struct {
	name          string
	source        string
	missing       string
	wantLoss      []string
	carriedBySpec bool // true: production retains the fact when declared
}

func runLossCase(t *testing.T, tc lossCase) {
	t.Helper()
	sourceRaw := readSpecFixture(t, tc.source)
	missingRaw := readSpecFixture(t, tc.missing)

	source := ledgerFacts(t, sourceRaw)
	missingDeclared := ledgerFacts(t, missingRaw)

	// The seed must really be absent from the checked-in fixture.
	for _, k := range tc.wantLoss {
		if _, ok := missingDeclared[k]; ok {
			t.Fatalf("%s: fixture %s still declares seeded-loss fact %s", tc.name, tc.missing, k)
		}
	}

	// Production-path observation on the unmodified declaration.
	fromSource := ledgerFacts(t, productionNormalize(t, sourceRaw))
	sourceLoss := ledgerLoss(source, fromSource)

	// Production-path observation on the seeded fixture.
	fromMissing := ledgerFacts(t, productionNormalize(t, missingRaw))
	seededLoss := ledgerLoss(source, fromMissing)

	sort.Strings(tc.wantLoss)
	if tc.carriedBySpec {
		if len(sourceLoss) != 0 {
			t.Fatalf("%s: production lost %v even without the seed; the fixture is not a clean positive control", tc.name, sourceLoss)
		}
	} else {
		// IR-side facts (code out_types, http headers) are not carried by
		// HEAD's spec DTO. The production path itself must show the loss.
		if !sameStrings(sourceLoss, tc.wantLoss) {
			t.Fatalf("%s: production-path loss = %v, want %v (IR fact not carried)", tc.name, sourceLoss, tc.wantLoss)
		}
	}
	if !sameStrings(seededLoss, tc.wantLoss) {
		t.Fatalf("%s: seeded-loss ledger = %v, want exactly %v (node/port precision)", tc.name, seededLoss, tc.wantLoss)
	}
	if len(seededLoss) == 0 {
		t.Fatalf("%s: verdict still green after seed; negative control failed", tc.name)
	}
	t.Logf("%s: verdict=red loss=%v", tc.name, seededLoss)
}

// TestExecSem070SeededSpecLoss covers facts spec carries today: a seeded
// deletion must be visible at node/port granularity.
func TestExecSem070SeededSpecLoss(t *testing.T) {
	// Fixture uses a loop input of ["items"]; the declared output ports are
	// acc/idx. Deleting out_types must report both ports on loop_100010.
	runLossCase(t, lossCase{
		name:          "loop_out_types",
		source:        "loop_with_out_types.json",
		missing:       "loop_missing_out_types.json",
		wantLoss:      []string{"loop_100010.out_types[acc]", "loop_100010.out_types[idx]"},
		carriedBySpec: true,
	})
	// Deleting the code node's exception stanza must report code_100001.
	runLossCase(t, lossCase{
		name:          "code_exception",
		source:        "code_with_exception.json",
		missing:       "code_missing_exception.json",
		wantLoss:      []string{"code_100001.exception[exception]"},
		carriedBySpec: true,
	})
}

// TestExecSem091SeededIRLoss covers the IR-side facts named by ES-091:
// code out_types and http headers/query params. P1a-1 added the
// `CodeConfig.OutTypes` carrier and P2 added `HTTPConfig.Headers/Params`, so
// both families are now retained by the production path; the seeded fixture
// still proves node/port precision (a deleted declaration must re-appear as a
// named loss, never as silence).
func TestExecSem091SeededIRLoss(t *testing.T) {
	runLossCase(t, lossCase{
		name:          "code_out_types",
		source:        "code_with_out_types.json",
		missing:       "code_missing_out_types.json",
		wantLoss:      []string{"code_100001.out_types[count]", "code_100001.out_types[value]"},
		carriedBySpec: true,
	})
	runLossCase(t, lossCase{
		name:    "http_headers",
		source:  "http_with_headers.json",
		missing: "http_missing_headers.json",
		wantLoss: []string{
			"http_100020.headers[Content-Type]",
			"http_100020.headers[X-Trace-Id]",
			"http_100020.params[key]",
			"http_100020.params[location]",
		},
		carriedBySpec: true,
	})
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
