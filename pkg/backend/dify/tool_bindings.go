package dify

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// ToolBinding maps one logical Tool operation to its Dify node identity.
// Arguments maps semantic parameter names to Dify tool parameter names; an
// omitted entry keeps the semantic name unchanged. Outputs maps semantic
// result names to Dify output-schema field names (P3.3 §16.4, symmetric
// with the Coze binding); an omitted entry keeps the semantic name.
type ToolBinding struct {
	Provider        string            `yaml:"provider"`
	Operation       string            `yaml:"operation"`
	ProviderID      string            `yaml:"provider_id"`
	ToolName        string            `yaml:"tool_name"`
	ProviderType    string            `yaml:"provider_type"`
	ToolLabel       string            `yaml:"tool_label"`
	ProviderName    *string           `yaml:"provider_name"`
	ToolDescription *string           `yaml:"tool_description"`
	ToolNodeVersion *string           `yaml:"tool_node_version"`
	Arguments       map[string]string `yaml:"arguments"`
	Outputs         map[string]string `yaml:"outputs"`
	// InterfaceHash pins the registry ABI this binding was written against
	// (P3.3 §16.2). Empty = unpinned (informational); a mismatched pin is a
	// hard failure at render time.
	InterfaceHash string `yaml:"interface_hash"`
	// CredentialRef is a logical credential reference (env://VAR or
	// secret://NAME) — never a raw value (P3.3 §16.3). The platform supplies
	// the real secret at runtime; this field is metadata + format validation.
	CredentialRef string `yaml:"credential_ref"`
}

// ToolBindingManifest is a closed set of Dify Tool bindings. When supplied
// to the renderer, every Tool call must resolve through it.
type ToolBindingManifest struct {
	bindings map[string]ToolBinding
}

// NewToolBindingManifest validates and indexes Dify Tool bindings.
func NewToolBindingManifest(bindings ...ToolBinding) (*ToolBindingManifest, error) {
	manifest := &ToolBindingManifest{bindings: make(map[string]ToolBinding, len(bindings))}
	for _, binding := range bindings {
		key := tool.SymbolKey(binding.Provider, binding.Operation)
		if binding.Provider == "" || binding.Operation == "" {
			return nil, fmt.Errorf("dify tool bindings: provider and operation must be non-empty: %q", key)
		}
		if _, exists := manifest.bindings[key]; exists {
			return nil, fmt.Errorf("dify tool bindings: duplicate binding %q", key)
		}
		if binding.ProviderID == "" || binding.ToolName == "" {
			return nil, fmt.Errorf("dify tool bindings: %q requires provider ID and tool name", key)
		}

		arguments := make(map[string]string, len(binding.Arguments))
		targetNames := make(map[string]bool, len(binding.Arguments))
		for semanticName, targetName := range binding.Arguments {
			if semanticName == "" || targetName == "" {
				return nil, fmt.Errorf("dify tool bindings: %q has an empty argument mapping", key)
			}
			if targetNames[targetName] {
				return nil, fmt.Errorf("dify tool bindings: %q maps multiple arguments to %q", key, targetName)
			}
			arguments[semanticName] = targetName
			targetNames[targetName] = true
		}
		outputs := make(map[string]string, len(binding.Outputs))
		outTargets := make(map[string]bool, len(binding.Outputs))
		for semanticName, targetName := range binding.Outputs {
			if semanticName == "" || targetName == "" {
				return nil, fmt.Errorf("dify tool bindings: %q has an empty output mapping", key)
			}
			if outTargets[targetName] {
				return nil, fmt.Errorf("dify tool bindings: %q maps multiple outputs to %q", key, targetName)
			}
			outputs[semanticName] = targetName
			outTargets[targetName] = true
		}
		binding.Outputs = outputs
		binding.Arguments = arguments
		if err := tool.ValidateCredentialRef(binding.CredentialRef); err != nil {
			return nil, fmt.Errorf("dify tool bindings: %q: %w", key, err)
		}
		binding.ProviderName = copyString(binding.ProviderName)
		binding.ToolDescription = copyString(binding.ToolDescription)
		binding.ToolNodeVersion = copyString(binding.ToolNodeVersion)
		manifest.bindings[key] = binding
	}
	return manifest, nil
}

func (m *ToolBindingManifest) resolve(call *ir.ToolCall) (*ToolBinding, error) {
	if m == nil {
		return nil, nil
	}
	key := tool.SymbolKey(call.ProviderID, call.Operation)
	binding, ok := m.bindings[key]
	if !ok {
		return nil, fmt.Errorf("dify tool bindings: node %s has no binding for %q", call.ID, key)
	}
	return &binding, nil
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// Links projects the manifest into registry-validatable binding links
// (P3.3 §16.2). The map projections keep semantic-name keys, which are
// exactly what the registry checks.
func (m *ToolBindingManifest) Links() []tool.BindingLink {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m.bindings))
	for key := range m.bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	links := make([]tool.BindingLink, 0, len(keys))
	for _, key := range keys {
		b := m.bindings[key]
		links = append(links, tool.BindingLink{
			Provider:      b.Provider,
			Operation:     b.Operation,
			Arguments:     b.Arguments,
			Outputs:       b.Outputs,
			InterfaceHash: b.InterfaceHash,
		})
	}
	return links
}

// ValidateAgainst performs the two-layer binding↔registry check (P3.3
// §16.2) over every binding in the manifest. Returns the unpinned binding
// keys (informational) or a hard error for collection mismatches and
// explicit pin violations. A nil registry is a no-op.
func (m *ToolBindingManifest) ValidateAgainst(r *tool.Registry) ([]string, error) {
	if m == nil {
		return nil, nil
	}
	return r.ValidateBindings(m.Links())
}

// All returns a copy of the indexed bindings.
func (m *ToolBindingManifest) All() []ToolBinding {
	if m == nil {
		return nil
	}
	out := make([]ToolBinding, 0, len(m.bindings))
	for _, b := range m.bindings {
		out = append(out, b)
	}
	return out
}

// MergePackageBindings merges per-package binding files into the central
// manifest (P3.5 §5 绑定随包). Every binding in "<provider>.<target>.yaml"
// must belong to that provider, and its operation must be declared by the
// registry when one is supplied. A (provider, operation) key declared both
// centrally and in a package file is a hard error — silent overrides are
// never allowed. A nil central manifest (no --dify-bindings) is fine: the
// package files then form the whole manifest.
func MergePackageBindings(central *ToolBindingManifest, packageFiles []string, target string, registry *tool.Registry) (*ToolBindingManifest, error) {
	if len(packageFiles) == 0 {
		return central, nil
	}
	merged := make(map[string]ToolBinding, 8)
	if central != nil {
		for _, b := range central.All() {
			merged[tool.SymbolKey(b.Provider, b.Operation)] = b
		}
	}
	for _, path := range packageFiles {
		provider := providerFromBindingFile(path, target)
		if provider == "" {
			return nil, fmt.Errorf("dify package bindings: cannot derive provider from file name %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("dify package bindings: reading %s: %w", path, err)
		}
		manifest, err := LoadToolBindingManifestYAML(data)
		if err != nil {
			return nil, fmt.Errorf("dify package bindings: %s: %w", path, err)
		}
		for _, b := range manifest.All() {
			if b.Provider != provider {
				return nil, fmt.Errorf("dify package bindings: %s declares provider %q (expected %q)", path, b.Provider, provider)
			}
			if registry != nil {
				if _, found := registry.Lookup(b.Provider, b.Operation); !found {
					return nil, fmt.Errorf("dify package bindings: %s: operation %q is not declared by provider %q", path, b.Operation, b.Provider)
				}
			}
			key := tool.SymbolKey(b.Provider, b.Operation)
			if _, exists := merged[key]; exists {
				return nil, fmt.Errorf("dify package bindings: conflict on %q: declared both centrally and in %s", key, path)
			}
			merged[key] = b
		}
	}
	bindings := make([]ToolBinding, 0, len(merged))
	for _, b := range merged {
		bindings = append(bindings, b)
	}
	return NewToolBindingManifest(bindings...)
}

// providerFromBindingFile extracts the provider slug from a package binding
// file name "<provider>.<target>.yaml".
func providerFromBindingFile(path, target string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".yaml")
	return strings.TrimSuffix(base, "."+target)
}
