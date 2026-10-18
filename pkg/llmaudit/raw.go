package llmaudit

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// InspectRawWorkflowLLM positively identifies LLM nodes in a raw platform
// workflow without depending on decompilation succeeding.
func InspectRawWorkflowLLM(data []byte, format string) (bool, error) {
	var document any
	var err error
	switch strings.ToLower(format) {
	case "json":
		err = json.Unmarshal(data, &document)
	case "yaml", "yml":
		err = yaml.Unmarshal(data, &document)
	default:
		return false, fmt.Errorf("unsupported workflow format %q", format)
	}
	if err != nil {
		return false, err
	}
	return containsRawLLMNode(document), nil
}

func containsRawLLMNode(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		if _, nodeLike := v["id"]; nodeLike {
			for _, key := range []string{"type", "node_type", "nodeType"} {
				if rawLLMType(v[key]) {
					return true
				}
			}
		}
		for _, child := range v {
			if containsRawLLMNode(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsRawLLMNode(child) {
				return true
			}
		}
	}
	return false
}

func rawLLMType(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.EqualFold(v, "llm") || v == "3"
	case float64:
		return v == 3
	case int:
		return v == 3
	case uint64:
		return v == 3
	}
	return false
}
