package llmprojection

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/llm"
)

const AssemblyMarker = "__ncoda_llm_envelope__"

type MetadataPolicy struct {
	ReasoningContent bool
	Usage            bool
	FinishReason     bool
}

type Plan struct {
	Projection *flow.Projection
	Adapters   map[string]string // semantic LLM producer -> generated adapter
}

func Lower(projection *flow.Projection, policy MetadataPolicy) *Plan {
	plan := &Plan{Projection: projection, Adapters: map[string]string{}}
	if projection == nil {
		return plan
	}
	adapters := map[string]*ir.CodeRun{}
	for _, node := range projection.Nodes {
		call, ok := node.(*ir.LLMCall)
		if !ok || call.Result == nil {
			continue
		}
		if !call.Result.WholeEnvelopeConsumed && !projectionUsesEnvelope(projection, call.ID) {
			continue
		}
		adapter := NewAssemblyAdapter(call, policy)
		adapters[call.ID] = adapter
		plan.Adapters[call.ID] = adapter.ID
	}
	if len(adapters) == 0 {
		return plan
	}
	nodes := make([]ir.Node, 0, len(projection.Nodes)+len(adapters))
	for _, node := range projection.Nodes {
		nodes = append(nodes, node)
		if adapter := adapters[node.NodeID()]; adapter != nil {
			nodes = append(nodes, adapter)
		}
	}
	edges := make([]flow.Edge, 0, len(projection.Edges)+len(adapters))
	inserted := map[string]bool{}
	for _, edge := range projection.Edges {
		if adapter := adapters[edge.SourceNodeID]; adapter != nil && edge.SourcePortID == "" {
			if !inserted[edge.SourceNodeID] {
				edges = append(edges, flow.Edge{SourceNodeID: edge.SourceNodeID, TargetNodeID: adapter.ID})
				inserted[edge.SourceNodeID] = true
			}
			edge.SourceNodeID = adapter.ID
		}
		edges = append(edges, edge)
	}
	producers := make([]string, 0, len(adapters))
	for producer := range adapters {
		producers = append(producers, producer)
	}
	sort.Strings(producers)
	for _, producer := range producers {
		adapter := adapters[producer]
		if !inserted[producer] {
			edges = append(edges, flow.Edge{SourceNodeID: producer, TargetNodeID: adapter.ID})
		}
	}
	plan.Projection = &flow.Projection{Nodes: nodes, Edges: edges}
	return plan
}

func AdapterID(producer string) string { return producer + "__llm_envelope" }

// UsesEnvelope reports whether a semantic tree contains a whole-response ref
// for producer. It is used by Coze-Biz's approved single-pass lowering path.
func UsesEnvelope(root any, producer string) bool {
	return containsEnvelopeRef(reflect.ValueOf(root), producer, map[uintptr]bool{})
}

func NewAssemblyAdapter(call *ir.LLMCall, policy MetadataPolicy) *ir.CodeRun {
	inputs := []ir.Source{{Path: []string{"text"}, BlockID: call.ID, Name: "text"}}
	types := map[string]*ir.Type{"text": schemaType(call.Result.PayloadSchema)}
	params := []string{"text"}
	fields := []string{`"text": text`}
	addMetadata := func(name string, native bool, typ *ir.Type) {
		if native {
			inputs = append(inputs, ir.Source{Path: []string{name}, BlockID: call.ID, Name: name})
			types[name] = typ
			params = append(params, name)
			fields = append(fields, fmt.Sprintf("%q: %s", name, name))
			return
		}
		fields = append(fields, fmt.Sprintf("%q: None", name))
	}
	addMetadata("reasoning_content", policy.ReasoningContent, ir.T("string"))
	addMetadata("usage", policy.Usage, usageType())
	addMetadata("finish_reason", policy.FinishReason, ir.T("string"))
	code := "def main(" + strings.Join(params, ", ") + "):\n" +
		"    " + AssemblyMarker + " = {" + strings.Join(fields, ", ") + "}\n" +
		"    return {\"response\": " + AssemblyMarker + "}"
	return &ir.CodeRun{
		ID:        AdapterID(call.ID),
		Code:      code,
		Language:  "python3",
		Inputs:    inputs,
		Types:     types,
		OutTypes:  map[string]*ir.Type{"response": envelopeType(call.Result.PayloadSchema)},
		OutOrder:  []string{"response"},
		Synthetic: true,
	}
}

func envelopeType(payload *llm.LLMSchema) *ir.Type {
	return ir.TObject(map[string]*ir.Type{
		"text":              schemaType(payload),
		"reasoning_content": ir.T("string"),
		"usage":             usageType(),
		"finish_reason":     ir.T("string"),
	})
}

func usageType() *ir.Type {
	return ir.TObject(map[string]*ir.Type{
		"prompt_tokens":     ir.T("integer"),
		"completion_tokens": ir.T("integer"),
		"total_tokens":      ir.T("integer"),
	})
}

func schemaType(schema *llm.LLMSchema) *ir.Type {
	if schema == nil {
		return ir.T("string")
	}
	switch schema.Kind {
	case llm.KindBool:
		return ir.T("boolean")
	case llm.KindInt64:
		return ir.T("integer")
	case llm.KindFloat64:
		return ir.T("number")
	case llm.KindArray:
		return ir.TList(schemaType(schema.Elem))
	case llm.KindMap:
		return ir.TObject(nil)
	case llm.KindObject:
		// open object: map-shaped without closed fields.
		return ir.TObject(nil)
	case llm.KindOptional:
		return schemaType(schema.Value)
	case llm.KindRecord:
		props := make(map[string]*ir.Type, len(schema.Fields))
		for _, field := range schema.Fields {
			props[field.Name] = schemaType(field.Schema)
		}
		return ir.TObject(props)
	default:
		return ir.T("string")
	}
}

func projectionUsesEnvelope(projection *flow.Projection, producer string) bool {
	for _, node := range projection.Nodes {
		if containsEnvelopeRef(reflect.ValueOf(node), producer, map[uintptr]bool{}) {
			return true
		}
	}
	return false
}

var (
	refType    = reflect.TypeOf(ir.Ref{})
	sourceType = reflect.TypeOf(ir.Source{})
)

func containsEnvelopeRef(value reflect.Value, producer string, seen map[uintptr]bool) bool {
	if !value.IsValid() {
		return false
	}
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		ptr := value.Pointer()
		if seen[ptr] {
			return false
		}
		seen[ptr] = true
		return containsEnvelopeRef(value.Elem(), producer, seen)
	}
	if value.Type() == refType {
		return value.FieldByName("Producer").String() == producer && value.FieldByName("Output").String() == ir.LLMEnvelopeOutput
	}
	if value.Type() == sourceType {
		return value.FieldByName("BlockID").String() == producer && value.FieldByName("Name").String() == ir.LLMEnvelopeOutput
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if containsEnvelopeRef(value.Field(i), producer, seen) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if containsEnvelopeRef(value.Index(i), producer, seen) {
				return true
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if containsEnvelopeRef(iter.Value(), producer, seen) {
				return true
			}
		}
	}
	return false
}
