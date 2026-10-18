package dify

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/tool"
)

type toolBindingsYAML struct {
	Version  string        `yaml:"version"`
	Bindings []ToolBinding `yaml:"bindings"`
}

// LoadToolBindingManifestYAML parses a versioned Dify Tool binding file.
func LoadToolBindingManifestYAML(data []byte) (*ToolBindingManifest, error) {
	var document toolBindingsYAML
	if err := tool.DecodeYAMLDocument(data, &document); err != nil {
		return nil, fmt.Errorf("dify tool bindings YAML: %w", err)
	}
	if document.Version != "v1" {
		return nil, fmt.Errorf("dify tool bindings YAML: version must be \"v1\", got %q", document.Version)
	}
	manifest, err := NewToolBindingManifest(document.Bindings...)
	if err != nil {
		return nil, err
	}
	return manifest, nil
}
