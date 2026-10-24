package spec

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/llm"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// ── D1 consumed-fields registry parity ───────────────────────────────────

func TestConsumedFieldsRegistryParity(t *testing.T) {
	kinds := []string{
		string(ir.KindStart), string(ir.KindEnd), string(ir.KindLLM), string(ir.KindHTTP),
		string(ir.KindTool), string(ir.KindKnowledge), string(ir.KindAgent), string(ir.KindConversation), string(ir.KindText),
		string(ir.KindCode), string(ir.KindListOp), string(ir.KindSelector), string(ir.KindAggregate),
		string(ir.KindAssign), KindLoopAssign, string(ir.KindQA), string(ir.KindOutput),
		string(ir.KindLoop), string(ir.KindBreak), string(ir.KindContinue),
	}
	if len(ConsumedFields) != len(kinds) {
		t.Fatalf("ConsumedFields has %d kinds, want %d", len(ConsumedFields), len(kinds))
	}
	for _, k := range kinds {
		cfg := populatedConfig(t, k)
		b, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("marshal %s: %v", k, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal %s: %v", k, err)
		}
		keys := []string{}
		for key := range m {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		reg := append([]string{}, ConsumedFields[k]...)
		sort.Strings(reg)
		if !reflect.DeepEqual(keys, reg) {
			t.Errorf("kind %s: populated DTO fields %v != registry %v", k, keys, reg)
		}
	}
}

// populatedConfig returns a config DTO with every consumable field set to a
// non-zero value, so the registry parity check sees the full field set
// (omitempty must not hide declared fields from the registry contract).
func populatedConfig(t *testing.T, kind string) any {
	t.Helper()
	one := 1
	switch kind {
	case string(ir.KindStart):
		return &StartConfig{}
	case string(ir.KindEnd):
		return &EndConfig{Inputs: []Ref{{Producer: "a", Output: "b"}}, Outputs: []EndOutput{{Name: "n", Ref: Ref{Producer: "a", Output: "b"}}}, NamedReturn: true, Answer: &EndAnswerConfig{Template: "t", Bindings: []EndOutput{{Name: "n", Ref: Ref{Producer: "a", Output: "b"}}}}}
	case string(ir.KindLLM):
		return &LLMConfig{Model: "m", Config: []ConfigPair{{Key: "k", Value: "v"}}, Inputs: []Source{{BlockID: "a", Name: "b"}}, OutputFormat: "text", ResponseFormat: 1, Exception: &ExceptionConfig{MaxRetry: 1, FailBranch: true}, Result: &LLMResultConfig{PayloadSchema: llm.String(), Validation: LLMValidationConfig{Mode: "none", ClosedRecords: true, DuplicateKeys: "last-value-wins", NonFiniteNums: "reject", AttemptScope: "model-only"}}}
	case string(ir.KindHTTP):
		return &HTTPConfig{Method: "GET", URL: "u", Inputs: []Source{{BlockID: "a"}}, Body: "b", Headers: []HTTPKV{{Name: "h", Value: "v"}}, Params: []HTTPKV{{Name: "p", Value: "q"}}, SettingTimeoutSec: 3, Exception: &ExceptionConfig{MaxRetry: 1, TimeoutAxis: "per_attempt"}}
	case string(ir.KindTool):
		return &ToolConfig{ToolName: "t", ProviderID: "p", Operation: "o", Params: []ToolParam{{Key: "k", Value: "v"}}, Exception: &ExceptionConfig{}}
	case string(ir.KindKnowledge):
		return &KnowledgeConfig{DatasetIDs: []string{"d"}, DatasetNames: []string{"n"}, Inputs: []Source{{BlockID: "a"}}, QueryName: "q"}
	case string(ir.KindAgent):
		return &AgentConfig{Model: "m", Instruction: "i", Query: &Source{BlockID: "a"}, MaxIteration: 1, Strategy: "function-calling", Memory: true, MemoryWindow: 8, Exception: &ExceptionConfig{}, Tools: []AgentToolWire{{Alias: "al"}}}
	case string(ir.KindConversation):
		return &ConversationConfig{}
	case string(ir.KindText):
		return &TextConfig{Method: "concat", Template: "t", ConcatChar: "|", Separators: []string{","}, Inputs: []Source{{BlockID: "a"}}, Answer: true}
	case string(ir.KindCode):
		return &CodeConfig{Code: "c", Language: "python", Inputs: []Source{{BlockID: "a"}}, OutOrder: []string{"o"}, Condition: true, Synthetic: true, KeepWhole: true}
	case string(ir.KindListOp):
		return &ListOpConfig{Operation: "filter", Inputs: []Source{{BlockID: "a"}}, FilterKey: "fk", FilterOp: "contains", FilterVal: "v", OrderKey: "ok", OrderVal: "asc", Limit: 3}
	case string(ir.KindSelector):
		return &SelectorConfig{Conditions: []Condition{{Left: Operand{Val: "a"}, Operator: 1, Right: Operand{Val: "b"}}}, Logic: 2}
	case string(ir.KindAggregate):
		return &AggregateConfig{
			Refs: []Ref{{Producer: "a", Output: "b"}},
			Groups: []AggregateGroup{{
				Name: "field",
				Refs: []Ref{{Producer: "a", Output: "field"}},
				Type: &TypeSpec{Kind: "string"},
			}},
		}
	case string(ir.KindAssign), KindLoopAssign:
		return &AssignConfig{VarName: "v", Value: Source{Val: "x"}, Operation: "set", LoopState: true, LoopID: "l"}
	case string(ir.KindQA):
		return &QaConfig{Choices: []string{"a"}, Inputs: []Source{{BlockID: "b"}}, Template: "t", Fields: []QaField{{Name: "f", Kind: "select", Options: []string{"o"}}}, Timeout: 5}
	case string(ir.KindOutput):
		return &OutputConfig{Template: "t"}
	case string(ir.KindLoop):
		return &LoopConfig{VarName: "v", LoopType: "by_array", LoopCount: 3, InputArrays: []string{"a"}, Inputs: []Source{{BlockID: "b"}}, Types: map[string]*TypeSpec{"t": {Kind: "list"}}, StateTypes: map[string]*TypeSpec{"s": {Kind: "string"}}, StateValues: map[string]Source{"s": {Val: "x"}}, OutTypes: map[string]*TypeSpec{"o": {Kind: "string"}}, OutputSources: []Source{{BlockID: "c"}}, FlattenOutput: true, IsParallel: true, ParallelNums: 2, ErrorHandleMode: "continue", Children: []NodeSpec{{ID: "c1", Kind: string(ir.KindText), Config: json.RawMessage(`{}`)}}, InnerEdges: []EdgeSpec{{Source: "c1", Target: "c1"}}}
	case string(ir.KindBreak):
		return &BreakConfig{}
	case string(ir.KindContinue):
		return &ContinueConfig{}
	}
	_ = one
	t.Fatalf("no populated config for kind %s", kind)
	return nil
}

// ── roundtrip gate + normalization ───────────────────────────────────────

func TestRoundTripGate(t *testing.T) {
	s := sampleSpec(t)
	b1, err := s.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var s2 Spec
	if err := json.Unmarshal(b1, &s2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	b2, err := s2.Marshal()
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("roundtrip gate failed: marshal→unmarshal→marshal not byte-stable\nfirst:  %s\nsecond: %s", b1, b2)
	}
	// normalize idempotence
	n1, err := s2.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	n2, err := n1.Normalize()
	if err != nil {
		t.Fatalf("normalize²: %v", err)
	}
	bn1, _ := json.Marshal(n1)
	bn2, _ := json.Marshal(n2)
	if !bytes.Equal(bn1, bn2) {
		t.Fatalf("normalize not idempotent")
	}
}

// Normalize drops unknown fields (execution-subset enforcement).
func TestNormalizeDropsUnknownFields(t *testing.T) {
	s := sampleSpec(t)
	b, _ := s.Marshal()
	// inject an unknown field into the text node config
	var raw struct {
		Version string     `json:"version"`
		Nodes   []NodeSpec `json:"nodes"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for i := range raw.Nodes {
		if raw.Nodes[i].Kind == string(ir.KindText) {
			var m map[string]any
			_ = json.Unmarshal(raw.Nodes[i].Config, &m)
			m["secret_compile_time_field"] = "must not survive"
			inj, _ := json.Marshal(m)
			raw.Nodes[i].Config = inj
		}
	}
	raw.Version = Version
	injSpec, _ := json.Marshal(raw)
	var s2 Spec
	if err := json.Unmarshal(injSpec, &s2); err != nil {
		t.Fatal(err)
	}
	norm, err := s2.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range norm.Nodes {
		if n.Kind == string(ir.KindText) {
			if bytes.Contains(n.Config, []byte("secret_compile_time_field")) {
				t.Fatalf("unknown field survived normalization: %s", n.Config)
			}
		}
	}
}

func TestSpecHashStableAndCanonical(t *testing.T) {
	s := sampleSpec(t)
	h1, err := s.SpecHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// re-marshal+unmarshal → same hash
	b, _ := s.Marshal()
	var s2 Spec
	_ = json.Unmarshal(b, &s2)
	h2, _ := s2.SpecHash()
	if h1 != h2 {
		t.Fatalf("hash not stable across roundtrip: %x vs %x", h1, h2)
	}
	// semantic change → different hash
	for i := range s2.Nodes {
		if s2.Nodes[i].Kind == string(ir.KindText) {
			s2.Nodes[i].Config = json.RawMessage(`{"method":"concat","template":"Changed","inputs":[{"path":["name"],"block_id":"start","name":"name"}]}`)
		}
	}
	h3, _ := s2.SpecHash()
	if h1 == h3 {
		t.Fatalf("hash did not change on semantic edit")
	}
}

func TestUnknownKindRejected(t *testing.T) {
	s := &Spec{Version: Version, Nodes: []NodeSpec{{ID: "n1", Kind: "warp_drive", Config: json.RawMessage(`{}`)}}}
	if _, err := s.Normalize(); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("expected unknown-kind error, got %v", err)
	}
}

func TestVersionGate(t *testing.T) {
	s := &Spec{Version: "v9", Nodes: nil}
	if _, err := s.Normalize(); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("expected version error, got %v", err)
	}
}

func TestDecodeJSONVersionDispatchAndV1LLMRefNormalization(t *testing.T) {
	v1 := []byte(`{
		"version":"v1",
		"nodes":[
			{"id":"l","kind":"llm","config":{"model":"m"}},
			{"id":"e","kind":"end","config":{"inputs":[{"producer":"l","output":"output"}],"outputs":[],"named_return":false}}
		],
		"edges":[{"source":"l","target":"e"}]
	}`)
	norm, err := DecodeJSON(v1)
	if err != nil {
		t.Fatal(err)
	}
	if norm.Version != VersionV2 {
		t.Fatalf("version = %q", norm.Version)
	}
	llmCfg, _ := DecodeConfig(KindLLM, norm.Nodes[0].Config)
	if llmCfg.(*LLMConfig).Result == nil || llmCfg.(*LLMConfig).Result.PayloadSchema.Kind != llm.KindString {
		t.Fatalf("v1 llm result = %#v", llmCfg)
	}
	endCfg, _ := DecodeConfig(KindEnd, norm.Nodes[1].Config)
	if got := endCfg.(*EndConfig).Inputs[0].Output; got != "text" {
		t.Fatalf("v1 llm output ref = %q, want text", got)
	}
}

func TestDecodeJSONRejectsMissingMalformedAndFutureVersions(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing", raw: `{}`, want: "missing version"},
		{name: "null", raw: `{"version":null}`, want: "missing version"},
		{name: "malformed", raw: `{"version":2}`, want: "malformed version"},
		{name: "future", raw: `{"version":"v3"}`, want: "unsupported version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeJSON([]byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDecodeJSONRejectsV2LLMWithoutValidSchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result string
	}{
		{name: "missing-result", result: ""},
		{name: "missing-payload-schema", result: `,"result":{"metadata":{},"validation":{}}`},
		{name: "malformed-array-schema", result: `,"result":{"payload_schema":{"kind":"array"},"metadata":{},"validation":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"version":"v2","nodes":[{"id":"l","kind":"llm","config":{"model":"m"` + tc.result + `}}]}`
			if _, err := DecodeJSON([]byte(raw)); err == nil || !strings.Contains(err.Error(), "payload schema") {
				t.Fatalf("err = %v, want payload schema rejection", err)
			}
		})
	}
}

// ── Build from real IR + Projection ──────────────────────────────────────

func TestBuildFromProjection(t *testing.T) {
	g := buildCorpusGraph(t, "09_iteration.ncoda")
	p, err := flow.Project(g)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	s, err := Build(g, p)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var sawLoop bool
	for _, n := range s.Nodes {
		if n.Kind == string(ir.KindLoop) {
			sawLoop = true
			var lc LoopConfig
			if err := json.Unmarshal(n.Config, &lc); err != nil {
				t.Fatalf("loop config: %v", err)
			}
			if len(lc.Children) != 1 {
				t.Fatalf("loop children = %d, want 1", len(lc.Children))
			}
			if lc.InputArrays[0] != "items" {
				t.Fatalf("input array = %v", lc.InputArrays)
			}
			foundLoopBack := false
			for _, e := range lc.InnerEdges {
				if e.TargetPort == PortLoopInlineInput {
					foundLoopBack = true
				}
			}
			if !foundLoopBack {
				t.Fatalf("loop inner edges missing loop-back arc")
			}
		}
	}
	if !sawLoop {
		t.Fatalf("no loop node in spec")
	}
	// deterministic: two builds → identical bytes
	s2, err := Build(g, p)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := s.Marshal()
	b2, _ := s2.Marshal()
	if !bytes.Equal(b1, b2) {
		t.Fatalf("Build not deterministic")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

// sampleSpec is a hand-written start→text→end spec (the U1.1 shape).
func sampleSpec(t *testing.T) *Spec {
	t.Helper()
	text := `{"method":"concat","template":"Hello {{name}}","inputs":[{"path":["name"],"block_id":"start","name":"name"}]}`
	return &Spec{
		Version: Version,
		Entry:   &EntrySpec{ID: "start", Params: []ParamSpec{{Name: "name", Type: &TypeSpec{Kind: "string"}, Optional: false, Default: nil}}},
		Nodes: []NodeSpec{
			{ID: "start", Kind: string(ir.KindStart), Config: json.RawMessage(`{}`)},
			{ID: "t1", Kind: string(ir.KindText), Config: json.RawMessage(text)},
			{ID: "e1", Kind: string(ir.KindEnd), Config: json.RawMessage(`{"inputs":[{"producer":"t1","output":"output"}],"outputs":null,"named_return":false}`)},
		},
		Edges: []EdgeSpec{
			{Source: "start", Target: "t1"},
			{Source: "t1", Target: "e1"},
		},
	}
}

func buildCorpusGraph(t *testing.T, name string) *ir.Graph {
	t.Helper()
	src, err := readTestCorpus(name)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	res := passes.DefaultPipeline().Run(src, name, nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatalf("frontend errors: %v", res.Errors())
	}
	g, berr := ir.Build(res.Program)
	if berr != nil {
		t.Fatalf("ir-build: %v", berr)
	}
	if g.Root == nil {
		t.Fatalf("no structured root")
	}
	return g
}

func readTestCorpus(name string) (string, error) {
	// the public corpus lives next to the kernel module root
	b, err := osReadFile("../../../examples/nodecoda/" + name)
	if err != nil {
		return "", errNoFile(name)
	}
	return string(b), nil
}
