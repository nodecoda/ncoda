package astnodes

import "testing"

// Coze-Biz YAML exports use the semantic artifact aliases from_json/to_json.
// They are physical aliases for the canonical AST node types and must
// normalize before typed node adaptation; otherwise reverse decompilation
// emits a platform-only bare call.
func TestParseNodeTypeArtifactAliases(t *testing.T) {
	for raw, want := range map[string]NodeType{
		"from_json": NodeTypeJsonDeserialization,
		"to_json":   NodeTypeJsonSerialization,
		"59":        NodeTypeJsonDeserialization,
		"58":        NodeTypeJsonSerialization,
	} {
		if got := ParseNodeType(raw); got != want {
			t.Fatalf("ParseNodeType(%q) = %q, want %q", raw, got, want)
		}
	}
}
