package binding

import (
	"strings"
	"testing"
)

// Agent contract constructor coverage (2026-08-20 test governance): every
// validated constructor + invariant error branch, mirroring the Python
// __post_init__ ValueError texts.

func TestNewAgentCallContract(t *testing.T) {
	if _, err := NewAgentCallContract("", "m", 1, "i", nil); err == nil {
		t.Error("empty profileAlias must error")
	}
	if _, err := NewAgentCallContract("p", "", 1, "i", nil); err == nil {
		t.Error("empty model must error")
	}
	if _, err := NewAgentCallContract("p", "m", 1, "", nil); err == nil {
		t.Error("empty instruction must error")
	}
	if _, err := NewAgentCallContract("p", "m", 1, "i", []string{"a", "a"}); err == nil {
		t.Error("duplicate tools must error")
	}
	c, err := NewAgentCallContract("p", "m", 7, "i", []string{"a", "b"})
	if err != nil {
		t.Fatalf("valid contract: %v", err)
	}
	if c.ProfileAlias != "p" || c.Model != "m" || c.Query != 7 || c.Instruction != "i" {
		t.Errorf("contract = %+v, want p/m/7/i", c)
	}
	if len(c.Tools) != 2 || c.Tools[0] != "a" || c.Tools[1] != "b" {
		t.Errorf("tools = %v, want [a b]", c.Tools)
	}
}

func TestNewAgentParameterTarget(t *testing.T) {
	if _, err := NewAgentParameterTarget("", "string", true); err == nil {
		t.Error("empty name must error")
	}
	if _, err := NewAgentParameterTarget("p", "", true); err == nil {
		t.Error("empty type must error")
	}
	p, err := NewAgentParameterTarget("query", "string", true)
	if err != nil {
		t.Fatalf("valid parameter: %v", err)
	}
	if p.Name != "query" || p.ParameterType != "string" || !p.Required {
		t.Errorf("parameter = %+v, want query/string/required", p)
	}
}

func TestNewAgentToolTargetProfile(t *testing.T) {
	if _, err := NewAgentToolTargetProfile("", "pid", "pname", "builtin", "pui", "tool", nil); err == nil {
		t.Error("empty alias must error")
	}
	if _, err := NewAgentToolTargetProfile("a", "", "pname", "builtin", "pui", "tool", nil); err == nil {
		t.Error("empty providerID must error")
	}
	if _, err := NewAgentToolTargetProfile("a", "pid", "", "builtin", "pui", "tool", nil); err == nil {
		t.Error("empty providerName must error")
	}
	if _, err := NewAgentToolTargetProfile("a", "pid", "pname", "builtin", "pui", "", nil); err == nil {
		t.Error("empty toolName must error")
	}
	dup := []AgentParameterTarget{{Name: "x", ParameterType: "string"}, {Name: "x", ParameterType: "int"}}
	if _, err := NewAgentToolTargetProfile("a", "pid", "pname", "builtin", "pui", "tool", dup); err == nil {
		t.Error("duplicate parameters must error")
	}
	tp, err := NewAgentToolTargetProfile("a", "pid", "pname", "builtin", "pui", "tool",
		[]AgentParameterTarget{{Name: "x", ParameterType: "string"}})
	if err != nil {
		t.Fatalf("valid tool profile: %v", err)
	}
	if tp.Alias != "a" || tp.ProviderID != "pid" || tp.ToolName != "tool" {
		t.Errorf("tool profile = %+v", tp)
	}
}

func TestNewAgentTargetProfile(t *testing.T) {
	if _, err := NewAgentTargetProfile("", "pn", "sn", "sl", "pui", "2", nil, nil); err == nil {
		t.Error("empty alias must error")
	}
	if _, err := NewAgentTargetProfile("a", "", "sn", "sl", "pui", "2", nil, nil); err == nil {
		t.Error("empty providerName must error")
	}
	if _, err := NewAgentTargetProfile("a", "pn", "", "sl", "pui", "2", nil, nil); err == nil {
		t.Error("empty strategyName must error")
	}
	if _, err := NewAgentTargetProfile("a", "pn", "sn", "sl", "pui", "3", nil, nil); err == nil {
		t.Error("toolNodeVersion != 2 must error")
	}
	dup := []AgentParameterTarget{{Name: "x", ParameterType: "string"}, {Name: "x", ParameterType: "int"}}
	if _, err := NewAgentTargetProfile("a", "pn", "sn", "sl", "pui", "2", dup, nil); err == nil {
		t.Error("duplicate strategy parameters must error")
	}
	tool, _ := NewAgentToolTargetProfile("t1", "pid", "pname", "builtin", "pui", "tool", nil)
	mismatched := map[string]*AgentToolTargetProfile{"wrong-key": tool}
	if _, err := NewAgentTargetProfile("a", "pn", "sn", "sl", "pui", "2", nil, mismatched); err == nil {
		t.Error("tool mapping key != tool alias must error")
	}
	params := []AgentParameterTarget{{Name: "query", ParameterType: "string"}, {Name: "max", ParameterType: "int"}}
	prof, err := NewAgentTargetProfile("a", "pn", "sn", "sl", "pui", "2", params,
		map[string]*AgentToolTargetProfile{"t1": tool})
	if err != nil {
		t.Fatalf("valid target profile: %v", err)
	}
	if prof.Alias != "a" || prof.StrategyName != "sn" || prof.ToolNodeVersion != "2" {
		t.Errorf("profile = %+v", prof)
	}
	names := prof.ParameterNames()
	if len(names) != 2 || names[0] != "query" || names[1] != "max" {
		t.Errorf("ParameterNames = %v, want [query max]", names)
	}
	allowed := prof.AllowedTools()
	if len(allowed) != 1 || allowed[0] != "t1" {
		t.Errorf("AllowedTools = %v, want [t1]", allowed)
	}
}

func TestAgentContractError(t *testing.T) {
	_, err := NewAgentCallContract("", "m", 1, "i", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "non-empty static identities") {
		t.Errorf("error message = %q, want the Python invariant text", msg)
	}
}
