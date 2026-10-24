package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nodecoda/ncoda/pkg/importns"
)

// CapabilityManifestVersion is the only accepted identity/alias manifest
// document version. Schema (parameters/outputs/effect) remains owned by the
// capability registry; this file is only the canonical identity layer.
const CapabilityManifestVersion = "capability-manifest/v1"

// CapabilityManifestFileName is the optional canonical-root manifest file.
const CapabilityManifestFileName = "capability-manifest.yaml"

// CapabilityKind is a coarse semantic classification. It is metadata only in
// v1: lowering must not branch on it before an explicit, reviewed contract
// makes that necessary.
type CapabilityKind string

const (
	CapabilityPure   CapabilityKind = "pure"
	CapabilityEffect CapabilityKind = "effect"
	CapabilityModel  CapabilityKind = "model"
	CapabilityHuman  CapabilityKind = "human"
)

// CapabilityProviderLink points at the existing tool registry identity. It
// deliberately carries no schema: headers/DB remain the schema authority.
type CapabilityProviderLink struct {
	Provider  string `yaml:"provider"`
	Operation string `yaml:"operation"`
}

// Capability declares one stable semantic-domain capability (T2). Standard
// language capabilities (T1) are intentionally not represented here.
type Capability struct {
	Canonical string                   `yaml:"canonical"`
	Version   int                      `yaml:"version"`
	Kind      CapabilityKind           `yaml:"kind"`
	Providers []CapabilityProviderLink `yaml:"providers"`
}

// CapabilityAlias binds one legacy platform import to its canonical T2
// capability. Platform names such as coze-biz remain long-term aliases.
type CapabilityAlias struct {
	Alias     string `yaml:"alias"`
	Canonical string `yaml:"canonical"`
	Provider  string `yaml:"provider"`
	Operation string `yaml:"operation"`
}

type capabilityAliasEntry struct {
	capability *Capability
	link       CapabilityProviderLink
}

type capabilityManifestDocument struct {
	Version      string                       `yaml:"version"`
	Capabilities []Capability                 `yaml:"capabilities"`
	Aliases      map[string][]CapabilityAlias `yaml:"aliases"`
}

// CapabilityManifest is the validated, indexed identity/alias surface.
type CapabilityManifest struct {
	capabilities map[string]*Capability
	byProvider   map[string]*Capability
	aliases      map[string]map[string][]capabilityAliasEntry
}

// capabilityCanonicalRE requires domain.action (and allows deeper semantic
// domains). A bare word is either T1 syntax or an incomplete T2 identity, not
// a canonical capability.
var capabilityCanonicalRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// LoadCapabilityManifestYAML parses and validates the closed identity/alias
// manifest. Unknown fields, duplicate identities and unknown links are hard
// errors: a half-known capability map is worse than no map.
func LoadCapabilityManifestYAML(data []byte) (*CapabilityManifest, error) {
	var document capabilityManifestDocument
	if err := DecodeYAMLDocument(data, &document); err != nil {
		return nil, fmt.Errorf("capability manifest YAML: %w", err)
	}
	return NewCapabilityManifest(document.Version, document.Capabilities, document.Aliases)
}

// NewCapabilityManifest validates and indexes the manifest's identity links.
func NewCapabilityManifest(
	version string,
	capabilities []Capability,
	aliases map[string][]CapabilityAlias,
) (*CapabilityManifest, error) {
	if version != CapabilityManifestVersion {
		return nil, fmt.Errorf("capability manifest: version must be %q, got %q",
			CapabilityManifestVersion, version)
	}

	manifest := &CapabilityManifest{
		capabilities: make(map[string]*Capability, len(capabilities)),
		byProvider:   make(map[string]*Capability, len(capabilities)),
		aliases:      make(map[string]map[string][]capabilityAliasEntry, len(aliases)),
	}
	for i, capability := range capabilities {
		if err := validateCapabilityIdentity(capability); err != nil {
			return nil, fmt.Errorf("capability manifest: capabilities[%d]: %w", i, err)
		}
		if _, exists := manifest.capabilities[capability.Canonical]; exists {
			return nil, fmt.Errorf("capability manifest: duplicate capability %q", capability.Canonical)
		}
		stored := capability
		manifest.capabilities[capability.Canonical] = &stored
		for _, provider := range capability.Providers {
			key := SymbolKey(provider.Provider, provider.Operation)
			if _, exists := manifest.byProvider[key]; exists {
				return nil, fmt.Errorf("capability manifest: duplicate provider link %q", key)
			}
			manifest.byProvider[key] = &stored
		}
	}

	for platform, platformAliases := range aliases {
		if !ValidPlatformQualifier(platform) {
			return nil, fmt.Errorf("capability manifest: invalid alias platform qualifier %q (must match [a-z0-9][a-z0-9-]*)", platform)
		}
		manifest.aliases[platform] = make(map[string][]capabilityAliasEntry, len(platformAliases))
		for i, alias := range platformAliases {
			capability, ok := manifest.capabilities[alias.Canonical]
			if !ok {
				return nil, fmt.Errorf("capability manifest: aliases[%s][%d]: alias %q maps to unknown capability %q",
					platform, i, alias.Alias, alias.Canonical)
			}
			if !capability.hasProvider(alias.Provider, alias.Operation) {
				return nil, fmt.Errorf("capability manifest: aliases[%s][%d]: alias %q links %s/%s outside capability %q",
					platform, i, alias.Alias, alias.Provider, alias.Operation, alias.Canonical)
			}
			if want := platform + "." + alias.Provider; alias.Alias != want {
				return nil, fmt.Errorf("capability manifest: aliases[%s][%d]: alias %q must be %q",
					platform, i, alias.Alias, want)
			}
			for _, existing := range manifest.aliases[platform][alias.Alias] {
				if existing.link.Operation == alias.Operation {
					return nil, fmt.Errorf("capability manifest: duplicate alias+operation %q/%q", alias.Alias, alias.Operation)
				}
			}
			manifest.aliases[platform][alias.Alias] = append(manifest.aliases[platform][alias.Alias], capabilityAliasEntry{
				capability: capability,
				link:       CapabilityProviderLink{Provider: alias.Provider, Operation: alias.Operation},
			})
		}
	}
	return manifest, nil
}

// LookupCanonical resolves the exact stable T2 semantic identity.
func (m *CapabilityManifest) LookupCanonical(canonical string) (*Capability, bool) {
	if m == nil {
		return nil, false
	}
	capability, ok := m.capabilities[canonical]
	return capability, ok
}

// LookupAliasLink resolves a platform alias together with the exact
// provider/operation it claims. Callers that need to compare the semantic
// alias against a physical binding should use this method; LookupAlias alone
// only returns the canonical capability.
func (m *CapabilityManifest) LookupAliasLink(platform, alias string) (CapabilityProviderLink, string, bool) {
	if m == nil {
		return CapabilityProviderLink{}, "", false
	}
	entries, ok := m.aliases[platform][alias]
	if !ok || len(entries) != 1 {
		// A provider with multiple operations is intentionally ambiguous at
		// alias-only granularity; callers must resolve alias+operation.
		return CapabilityProviderLink{}, "", false
	}
	entry := entries[0]
	return entry.link, entry.capability.Canonical, true
}

// LookupAliasOperation resolves a provider-level import together with the
// requested operation. Plugin providers frequently expose multiple operations
// through the same platform import, so alias identity alone cannot disambiguate.
func (m *CapabilityManifest) LookupAliasOperation(platform, alias, operation string) (CapabilityProviderLink, string, bool) {
	if m == nil {
		return CapabilityProviderLink{}, "", false
	}
	for _, entry := range m.aliases[platform][alias] {
		if entry.link.Operation == operation {
			return entry.link, entry.capability.Canonical, true
		}
	}
	return CapabilityProviderLink{}, "", false
}

// LookupAliasOperations resolves every operation registered under a
// provider-level alias. It exists for ordinary plugin imports where one
// provider exposes several operations; callers must still compare the exact
// requested operation against the returned links.
func (m *CapabilityManifest) LookupAliasOperations(platform, alias string) ([]CapabilityProviderLink, []string, bool) {
	if m == nil {
		return nil, nil, false
	}
	entries, ok := m.aliases[platform][alias]
	if !ok || len(entries) == 0 {
		return nil, nil, false
	}
	links := make([]CapabilityProviderLink, 0, len(entries))
	canonicals := make([]string, 0, len(entries))
	for _, entry := range entries {
		links = append(links, entry.link)
		canonicals = append(canonicals, entry.capability.Canonical)
	}
	return links, canonicals, true
}

// LookupProvider resolves a registry identity back to its canonical capability.
func (m *CapabilityManifest) LookupProvider(provider, operation string) (*Capability, bool) {
	if m == nil {
		return nil, false
	}
	capability, ok := m.byProvider[SymbolKey(provider, operation)]
	return capability, ok
}

// LookupAlias resolves a long-term platform import alias. It returns the
// canonical capability and canonical identity so callers do not have to parse
// the alias string or guess a semantic name.
func (m *CapabilityManifest) LookupAlias(platform, alias string) (*Capability, string, bool) {
	if m == nil {
		return nil, "", false
	}
	entries, ok := m.aliases[platform][alias]
	if !ok || len(entries) != 1 {
		return nil, "", false
	}
	entry := entries[0]
	return entry.capability, entry.capability.Canonical, true
}

// ValidateAgainst checks that every provider/operation identity exists in the
// capability registry. A nil registry is a no-op, mirroring binding
// validation: schema checks belong to the registry consumer.
func (m *CapabilityManifest) ValidateAgainst(registry *Registry) error {
	if m == nil || registry == nil {
		return nil
	}
	for _, capability := range m.capabilities {
		for _, provider := range capability.Providers {
			if _, ok := registry.Lookup(provider.Provider, provider.Operation); !ok {
				return fmt.Errorf("capability manifest: registry missing capability %q",
					SymbolKey(provider.Provider, provider.Operation))
			}
		}
	}
	return nil
}

func (c *Capability) hasProvider(provider, operation string) bool {
	for _, link := range c.Providers {
		if link.Provider == provider && link.Operation == operation {
			return true
		}
	}
	return false
}

func validateCapabilityIdentity(capability Capability) error {
	if !capabilityCanonicalRE.MatchString(capability.Canonical) {
		return fmt.Errorf("invalid canonical %q: must be domain.action (lowercase)", capability.Canonical)
	}
	if capability.Version <= 0 {
		return fmt.Errorf("canonical %q: version must be positive", capability.Canonical)
	}
	switch capability.Kind {
	case CapabilityPure, CapabilityEffect, CapabilityModel, CapabilityHuman:
	default:
		return fmt.Errorf("canonical %q: unknown capability kind %q", capability.Canonical, capability.Kind)
	}
	if len(capability.Providers) == 0 {
		return fmt.Errorf("canonical %q: must declare at least one provider link", capability.Canonical)
	}
	for _, provider := range capability.Providers {
		if provider.Provider == "" || provider.Operation == "" {
			return fmt.Errorf("canonical %q: provider and operation must be non-empty", capability.Canonical)
		}
		if !importns.ValidProviderName(provider.Provider) {
			return fmt.Errorf("canonical %q: invalid provider name %q", capability.Canonical, provider.Provider)
		}
		if strings.ContainsAny(provider.Operation, " \t\r\n") {
			return fmt.Errorf("canonical %q: invalid operation name %q", capability.Canonical, provider.Operation)
		}
	}
	return nil
}

// DiscoverCapabilityManifest scans canonical tool roots for the optional
// capability manifest. A path may be either a canonical root or its
// capabilities-ncoda subdirectory. Missing manifests are not errors; a broken
// or invalid manifest is.
func DiscoverCapabilityManifest(searchPaths []string) (*CapabilityManifest, bool, error) {
	for _, searchPath := range searchPaths {
		root := filepath.Clean(searchPath)
		if filepath.Base(root) == CapabilityHeadersDir {
			root = filepath.Dir(root)
		}
		path := filepath.Join(root, CapabilityManifestFileName)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, true, fmt.Errorf("reading capability manifest %s: %w", path, err)
		}
		manifest, err := LoadCapabilityManifestYAML(data)
		if err != nil {
			return nil, true, fmt.Errorf("loading capability manifest %s: %w", path, err)
		}
		return manifest, true, nil
	}
	return nil, false, nil
}
