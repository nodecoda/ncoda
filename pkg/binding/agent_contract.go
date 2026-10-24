package binding

// Immutable frontend contracts for target-pinned Agent calls, ported 1:1
// from src/nclang/lang/agent_call_contract.py (25 lines) and
// src/nclang/lang/agent_contract.py (75 lines). The Python frozen
// dataclasses with __post_init__ invariants become Go structs built through
// validated constructors (the human_input_contract.go pattern); the same
// invariant texts are reused for the error messages.
//
// Mapping notes (recorded in task-25d-report.md):
//   - Python stores tools as a tuple and agent_profiles as a Mapping; the
//     Go contract keeps an ordered slice (Tools) and the profile keeps a
//     map (Tools) plus the declared parameter order (Parameters slice) —
//     ParameterNames()/AllowedTools() reconstruct the Python tuple forms.
//   - Python validates identities with truthiness; Go checks empty strings
//     with the same messages.

import "sort"

// AgentCallContract mirrors AgentCallContract (agent_call_contract.py:11-25):
// one statically closed Agent invocation resolved against a target profile.
// Query is the AstNodeID of the call's query argument (Python AstNodeId).
type AgentCallContract struct {
	ProfileAlias string
	Model        string
	Query        AstNodeID
	Instruction  string
	Tools        []string
}

// NewAgentCallContract validates and builds one contract, mirroring
// AgentCallContract.__post_init__ (agent_call_contract.py:19-24). It
// returns an error carrying the Python ValueError message text when the
// shape is invalid; Tools is copied so the contract stays immutable.
func NewAgentCallContract(profileAlias, model string, query AstNodeID, instruction string, tools []string) (*AgentCallContract, error) {
	if profileAlias == "" || model == "" || instruction == "" {
		return nil, errAgentContract("Agent call contract requires non-empty static identities")
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool] {
			return nil, errAgentContract("Agent call tools must not contain duplicates")
		}
		seen[tool] = true
	}
	return &AgentCallContract{
		ProfileAlias: profileAlias,
		Model:        model,
		Query:        query,
		Instruction:  instruction,
		Tools:        append([]string(nil), tools...),
	}, nil
}

// AgentParameterTarget mirrors AgentParameterTarget (agent_contract.py:9-18):
// one exact installed strategy or tool parameter contract.
type AgentParameterTarget struct {
	Name          string
	ParameterType string
	Required      bool
}

// NewAgentParameterTarget validates and builds one parameter contract,
// mirroring AgentParameterTarget.__post_init__ (agent_contract.py:14-16).
func NewAgentParameterTarget(name, parameterType string, required bool) (*AgentParameterTarget, error) {
	if name == "" || parameterType == "" {
		return nil, errAgentContract("agent parameter identities must be non-empty")
	}
	return &AgentParameterTarget{Name: name, ParameterType: parameterType, Required: required}, nil
}

// AgentToolTargetProfile mirrors AgentToolTargetProfile
// (agent_contract.py:21-33): one stable NodeCoda tool alias pinned to an
// exact the reference target tool identity. ProviderType is the Literal["builtin"] value.
type AgentToolTargetProfile struct {
	Alias                  string
	ProviderID             string
	ProviderName           string
	ProviderType           string
	PluginUniqueIdentifier string
	ToolName               string
	Parameters             []AgentParameterTarget
}

// NewAgentToolTargetProfile validates and builds one tool profile,
// mirroring AgentToolTargetProfile.__post_init__ (agent_contract.py:27-32).
func NewAgentToolTargetProfile(alias, providerID, providerName, providerType, pluginUniqueIdentifier, toolName string, parameters []AgentParameterTarget) (*AgentToolTargetProfile, error) {
	if alias == "" || providerID == "" || providerName == "" || toolName == "" {
		return nil, errAgentContract("agent tool target identities must be non-empty")
	}
	seen := map[string]bool{}
	for _, parameter := range parameters {
		if seen[parameter.Name] {
			return nil, errAgentContract("agent tool parameters must not contain duplicates")
		}
		seen[parameter.Name] = true
	}
	return &AgentToolTargetProfile{
		Alias:                  alias,
		ProviderID:             providerID,
		ProviderName:           providerName,
		ProviderType:           providerType,
		PluginUniqueIdentifier: pluginUniqueIdentifier,
		ToolName:               toolName,
		Parameters:             append([]AgentParameterTarget(nil), parameters...),
	}, nil
}

// AgentTargetProfile mirrors AgentTargetProfile (agent_contract.py:36-75):
// one Agent strategy and its closed set of permitted tools. ToolNodeVersion
// is the Literal["2"] value.
type AgentTargetProfile struct {
	Alias                  string
	ProviderName           string
	StrategyName           string
	StrategyLabel          string
	PluginUniqueIdentifier string
	ToolNodeVersion        string
	Parameters             []AgentParameterTarget
	Tools                  map[string]*AgentToolTargetProfile
}

// NewAgentTargetProfile validates and builds one target profile, mirroring
// AgentTargetProfile.__post_init__ (agent_contract.py:45-57). The Tools
// map is copied and every key must match its profile's alias
// (agent_contract.py:54-56).
func NewAgentTargetProfile(alias, providerName, strategyName, strategyLabel, pluginUniqueIdentifier, toolNodeVersion string, parameters []AgentParameterTarget, tools map[string]*AgentToolTargetProfile) (*AgentTargetProfile, error) {
	if alias == "" || providerName == "" || strategyName == "" {
		return nil, errAgentContract("agent target profile identities must be non-empty")
	}
	if toolNodeVersion != "2" {
		return nil, errAgentContract("agent target profiles require tool_node_version '2'")
	}
	seen := map[string]bool{}
	for _, parameter := range parameters {
		if seen[parameter.Name] {
			return nil, errAgentContract("agent strategy parameters must not contain duplicates")
		}
		seen[parameter.Name] = true
	}
	toolsCopy := make(map[string]*AgentToolTargetProfile, len(tools))
	for aliasKey, tool := range tools {
		if aliasKey != tool.Alias {
			return nil, errAgentContract("agent tool mapping keys must match tool aliases")
		}
		toolsCopy[aliasKey] = tool
	}
	return &AgentTargetProfile{
		Alias:                  alias,
		ProviderName:           providerName,
		StrategyName:           strategyName,
		StrategyLabel:          strategyLabel,
		PluginUniqueIdentifier: pluginUniqueIdentifier,
		ToolNodeVersion:        toolNodeVersion,
		Parameters:             append([]AgentParameterTarget(nil), parameters...),
		Tools:                  toolsCopy,
	}, nil
}

// ParameterNames returns the exact strategy parameter names in declared
// order, mirroring AgentTargetProfile.parameter_names (agent_contract.py
// :60-62).
func (p *AgentTargetProfile) ParameterNames() []string {
	names := make([]string, 0, len(p.Parameters))
	for _, parameter := range p.Parameters {
		names = append(names, parameter.Name)
	}
	return names
}

// AllowedTools returns the permitted tool aliases, mirroring
// AgentTargetProfile.allowed_tools (agent_contract.py:64-66). The Go Tools
// map has no insertion order (Python preserves mapping order), so the
// aliases are returned sorted — callers only use this face as a set
// (target_validation.py set arithmetic is order-independent).
func (p *AgentTargetProfile) AllowedTools() []string {
	aliases := make([]string, 0, len(p.Tools))
	for alias := range p.Tools {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

// errAgentContract wraps one contract invariant as an error (the Go
// analogue of the Python ValueError raised in __post_init__).
func errAgentContract(message string) error {
	return &agentContractError{message: message}
}

type agentContractError struct{ message string }

func (e *agentContractError) Error() string { return e.message }
