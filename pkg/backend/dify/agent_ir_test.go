package dify

// Agent IR-path coverage (2026-08-20, legacy-retirement prep): agent(...)
// lowering now lives on the production IR path (ir.AgentCall →
// RenderFromIR → AgentDataNode v2). This test locks the IR build + render
// independently of the frozen legacy oracle: the wire payload must match
// the pinned function-calling-v1 profile (strategy/provider/label/plugin +
// per-tool wire identity), and result.text must resolve onto the agent
// node's "text" port. The legacy-only serialize fixtures keep their oracle
// locks (serialize_test.go); this suite covers the production path shape.

import (
	"reflect"
	"testing"

	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

const agentIRFixture = `@language nodecoda/1
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

func agentIRProfile(t *testing.T) *passes.TargetProfile {
	t.Helper()
	param := func(name, typ string, required bool) *binding.AgentParameterTarget {
		p, err := binding.NewAgentParameterTarget(name, typ, required)
		if err != nil {
			t.Fatalf("NewAgentParameterTarget: %v", err)
		}
		return p
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
		[]binding.AgentParameterTarget{
			*param("model", "model-selector", true),
			*param("query", "string", true),
			*param("instruction", "string", false),
			*param("tools", "tools-selector", true),
		},
		map[string]*binding.AgentToolTargetProfile{"clock": clock},
	)
	if err != nil {
		t.Fatalf("NewAgentTargetProfile: %v", err)
	}
	tp := passes.DefaultTargetProfile()
	tp.AgentProfiles = map[string]*binding.AgentTargetProfile{"function-calling-v1": profile}
	return tp
}

func TestAgentIRBuild(t *testing.T) {
	res := passes.DefaultPipeline().Run(agentIRFixture, "agent_ir.ncoda", agentIRProfile(t))
	if res.HasErrors() {
		t.Fatalf("frontend errors: %v", res.Diagnostics)
	}
	if res.Program == nil {
		t.Fatal("no typed program")
	}
	graph, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	var call *ir.AgentCall
	for _, stmt := range graph.Root.Stmts {
		ns, ok := stmt.(*ir.NodeStmt)
		if !ok {
			continue
		}
		if ac, ok := ns.Node.(*ir.AgentCall); ok {
			call = ac
			break
		}
	}
	if call == nil {
		t.Fatalf("no AgentCall node in IR (stmts=%d)", len(graph.Root.Stmts))
	}
	if call.Model != "provider/model" {
		t.Errorf("Model = %q, want provider/model", call.Model)
	}
	if call.Instruction != "Answer the user" {
		t.Errorf("Instruction = %q, want Answer the user", call.Instruction)
	}
	if call.StrategyName != "function_calling" || call.StrategyProviderName != "langgenius/agent" ||
		call.StrategyLabel != "Function Calling" || call.PluginUniqueID != "langgenius/agent:1.0.0@sha256:agent" {
		t.Errorf("wire identity = %+v, want pinned function-calling-v1", call)
	}
	if len(call.Tools) != 1 {
		t.Fatalf("Tools = %+v, want 1 pinned tool", call.Tools)
	}
	tool := call.Tools[0]
	if tool.Alias != "clock" || tool.ProviderName != "time" || tool.ToolName != "current_time" ||
		tool.ProviderType != "builtin" || tool.PluginUniqueID != "langgenius/time:1.0.0@sha256:time" {
		t.Errorf("tool wire = %+v, want pinned clock", tool)
	}
	if call.Query == nil || call.Query.BlockID == "" {
		t.Errorf("Query = %+v, want start-param ref", call.Query)
	}
}

func TestAgentIRRender(t *testing.T) {
	res := passes.DefaultPipeline().Run(agentIRFixture, "agent_ir.ncoda", agentIRProfile(t))
	if res.HasErrors() {
		t.Fatalf("frontend errors: %v", res.Diagnostics)
	}
	graph, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir.Build: %v", err)
	}
	astOut, err := RenderFromIR(graph, res.Program.Program.Mode())
	if err != nil {
		t.Fatalf("RenderFromIR: %v", err)
	}
	var agentData *AgentDataNode
	var agentID string
	var endOutput *OutputNode
	for _, node := range astOut.Graph.Nodes {
		switch data := node.Data.(type) {
		case *AgentDataNode:
			agentData = data
			agentID = node.ID
		case *OutputNode:
			endOutput = data
		}
	}
	if agentData == nil {
		t.Fatal("no agent node rendered")
	}
	if agentData.AgentStrategyName != "function_calling" ||
		agentData.AgentStrategyProviderName != "langgenius/agent" ||
		agentData.AgentStrategyLabel != "Function Calling" ||
		agentData.PluginUniqueIdentifier != "langgenius/agent:1.0.0@sha256:agent" {
		t.Errorf("agent node wire identity = %+v", agentData)
	}
	if agentData.ToolNodeVersion == nil || *agentData.ToolNodeVersion != "2" {
		t.Errorf("ToolNodeVersion = %v, want 2", agentData.ToolNodeVersion)
	}
	params := agentData.AgentParameters
	query, ok := params["query"]
	if !ok {
		t.Fatal("agent_parameters missing query")
	}
	if qs, ok := query.Value.(string); !ok || qs == "" {
		t.Errorf("query value = %#v, want start-param interpolation", query.Value)
	}
	tools, ok := params["tools"]
	if !ok {
		t.Fatal("agent_parameters missing tools")
	}
	toolList, ok := tools.Value.([]map[string]any)
	if !ok || len(toolList) != 1 {
		t.Fatalf("tools value = %#v, want 1 pinned tool", tools.Value)
	}
	tm := toolList[0]
	if tm["tool_name"] != "current_time" || tm["provider_name"] != "time" || tm["type"] != "builtin" {
		t.Errorf("tool wire = %v, want pinned clock", tm)
	}
	if tm["plugin_unique_identifier"] != "langgenius/time:1.0.0@sha256:time" {
		t.Errorf("tool plugin id = %v, want pinned", tm["plugin_unique_identifier"])
	}
	// result.text resolves onto the agent node's "text" port (oracle:
	// value_selector ["agent_2", "text"]).
	if endOutput == nil {
		t.Fatal("no end node rendered")
	}
	var found bool
	for _, o := range endOutput.Outputs {
		if reflect.DeepEqual(o.ValueSelector, []string{agentID, "text"}) {
			found = true
		}
	}
	if !found {
		t.Errorf("end outputs = %+v, want a selector onto agent text port", endOutput.Outputs)
	}
}
