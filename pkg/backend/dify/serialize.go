package dify

// SerializeToDict — the Go port of the authoritative reference
// 上游 Python 参考实现（nclang）/src/nclang/serializer.py serialize_to_dict
// (:1058-1109). Every output dictionary is an OrderedMap whose insertion
// order mirrors the Python dict insertion order (key-order parity), so the
// wire JSON is byte-stable and deterministic for identical inputs.
//
// Determinism: Python preserves dict insertion order for every dictionary,
// including map-backed payloads. Go maps are unordered, so map-backed
// payloads are emitted with sorted keys via normalizeAny — except code-node
// outputs and tool parameters, whose builders record the Python insertion
// order (CodeDataNode.OutputOrder / ToolDataNode.ToolParameterOrder) and the
// emitters follow it. Agent parameters, datasource parameters, schema defs
// and Extra payloads remain sorted (recorded deviation, task-26-report.md);
// structural keys (node data, edge data, base fields) keep the exact Python
// insertion order. Values are identical in all cases.
//
// Python ValueError surfaces are ported as Go errors and must never be
// swallowed (serializer.py:951-955, :664-666, :628-631).

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// ── Scalar / ordering helpers ──────────────────────────────────────

// v mirrors serializer.py _v (:86-89): integral floats become ints.
func v(val float64) any {
	if val == math.Trunc(val) {
		return int(val)
	}
	return val
}

// orEmptyStringSlice renders a []string as a JSON array, empty when nil
// (Python list defaults are never None).
func orEmptyStringSlice(s []string) []any {
	if s == nil {
		return []any{}
	}
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x
	}
	return out
}

// sortedKeys returns the sorted key list of any string-keyed map. Go maps
// are unordered; sorted iteration is the deterministic key order for
// map-backed payloads (see the package comment deviation note).

// serializeOutputDef mirrors _output_def_to_dict (code_translator.py
// :242-243): {"type": ..., "children": {...} | null}, preserving the Python
// dict insertion order at every level via ChildOrder. A leaf subtree emits
// children: null exactly like Python's "children or None".
func serializeOutputDef(od *OutputDefNode) *OrderedMap {
	m := NewOrderedMap()
	m.Set("type", od.Type)
	if len(od.Children) > 0 {
		keys := od.ChildOrder
		if len(keys) == 0 {
			keys = sortedKeys(od.Children)
		}
		children := NewOrderedMap()
		for _, k := range keys {
			if child, ok := od.Children[k]; ok {
				children.Set(k, serializeOutputDef(child))
			}
		}
		m.Set("children", children)
	} else {
		m.Set("children", nil)
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// normalizeAny recursively converts plain maps into sorted-key OrderedMaps
// and slices into normalized slices, so no output dictionary is ever built
// from raw Go map iteration. Nested OrderedMaps pass through untouched.
func normalizeAny(v any) any {
	switch x := v.(type) {
	case *OrderedMap:
		return x
	case map[string]any:
		if x == nil {
			return nil
		}
		om := NewOrderedMap()
		for _, k := range sortedKeys(x) {
			om.Set(k, normalizeAny(x[k]))
		}
		return om
	case map[string]string:
		if x == nil {
			return nil
		}
		om := NewOrderedMap()
		for _, k := range sortedKeys(x) {
			om.Set(k, x[k])
		}
		return om
	case []any:
		if x == nil {
			return nil
		}
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalizeAny(e)
		}
		return out
	default:
		return v
	}
}

func hasKey(om *OrderedMap, k string) bool {
	_, ok := om.Get(k)
	return ok
}

// canonicalizeEdgeType ports assigner_contract.py:28-31.
func canonicalizeEdgeType(edgeType string) string {
	if edgeType == "variable-assigner" {
		return "custom"
	}
	return edgeType
}

// dataNodeToType ports _DATA_NODE_TO_TYPE (serializer.py:83, the reverse of
// NODE_TYPE_MAP): the concrete data node → its Dify type string, "unknown"
// for unmodeled types.
func dataNodeToType(nd NodeData) string {
	switch nd.(type) {
	case *StartDataNode:
		return "start"
	case *OutputNode:
		return "end"
	case *AnswerNode:
		return "answer"
	case *LLMDataNode:
		return "llm"
	case *IfElseDataNode:
		return "if-else"
	case *CodeDataNode:
		return "code"
	case *HTTPRequestDataNode:
		return "http-request"
	case *ToolDataNode:
		return "tool"
	case *TemplateTransformDataNode:
		return "template-transform"
	case *KnowledgeRetrievalDataNode:
		return "knowledge-retrieval"
	case *QuestionClassifierDataNode:
		return "question-classifier"
	case *ParameterExtractorDataNode:
		return "parameter-extractor"
	case *LoopDataNode:
		return "loop"
	case *LoopStartDataNode:
		return "loop-start"
	case *LoopEndDataNode:
		return "loop-end"
	case *VariableAggregatorDataNode:
		return "variable-aggregator"
	case *AssignerDataNode:
		return "assigner"
	case *DocumentExtractorDataNode:
		return "document-extractor"
	case *ListOperatorDataNode:
		return "list-operator"
	case *AgentDataNode:
		return "agent"
	case *HumanInputDataNode:
		return "human-input"
	case *DatasourceDataNode:
		return "datasource"
	case *IterationDataNode:
		return "iteration"
	case *IterationStartDataNode:
		return "iteration-start"
	case *CustomNoteDataNode:
		return "custom-note"
	default:
		return "unknown"
	}
}

// nodeTypeName returns the short struct name of a NodeData value (Python
// type(nd).__name__ parity for the unmodeled-node error message).
func nodeTypeName(nd NodeData) string {
	if nd == nil {
		return "None"
	}
	t := reflect.TypeOf(nd)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Name()
}

// dataNodeBase returns the embedded DataNode of a concrete node. Every
// concrete node type embeds DataNode (nodes.go), so this is a compile-time
// checked projection used by the iteration_id/loop_id patch in
// serializeDataNode (serializer.py:947-952).
func dataNodeBase(nd NodeData) *DataNode {
	switch n := nd.(type) {
	case *StartDataNode:
		return &n.DataNode
	case *OutputNode:
		return &n.DataNode
	case *AnswerNode:
		return &n.DataNode
	case *LLMDataNode:
		return &n.DataNode
	case *IfElseDataNode:
		return &n.DataNode
	case *CodeDataNode:
		return &n.DataNode
	case *HTTPRequestDataNode:
		return &n.DataNode
	case *ToolDataNode:
		return &n.DataNode
	case *TemplateTransformDataNode:
		return &n.DataNode
	case *KnowledgeRetrievalDataNode:
		return &n.DataNode
	case *QuestionClassifierDataNode:
		return &n.DataNode
	case *ParameterExtractorDataNode:
		return &n.DataNode
	case *LoopDataNode:
		return &n.DataNode
	case *LoopStartDataNode:
		return &n.DataNode
	case *LoopEndDataNode:
		return &n.DataNode
	case *VariableAggregatorDataNode:
		return &n.DataNode
	case *AssignerDataNode:
		return &n.DataNode
	case *DocumentExtractorDataNode:
		return &n.DataNode
	case *ListOperatorDataNode:
		return &n.DataNode
	case *AgentDataNode:
		return &n.DataNode
	case *HumanInputDataNode:
		return &n.DataNode
	case *DatasourceDataNode:
		return &n.DataNode
	case *IterationDataNode:
		return &n.DataNode
	case *IterationStartDataNode:
		return &n.DataNode
	case *CustomNoteDataNode:
		return &n.DataNode
	}
	return nil
}

// conditionOperator ports the serializer.py:303-307 operator mapping.
func conditionOperator(op string) string {
	switch op {
	case "equals":
		return "="
	case "not_equals":
		return "≠"
	case ">=":
		return "≥"
	case "<=":
		return "≤"
	}
	return op
}

// uuid5Name builds a deterministic uuid5-style id per RFC 4122 §4.3:
// uuid5(NAMESPACE_URL, name) with SHA1 — zero dependencies, byte-stable
// across compiles. Since P3 (2026-08-15) the Python oracle emits the same
// derived uuid5 for loop and conversation variables (serializer.py
// _serialize_loop_variable / context.py register_conversation_var), so Go
// and Python produce byte-identical ids; this helper remains the shared
// deterministic implementation both sides mirror. human_input_lowerer.py:68
// was the original Python uuid5 precedent.
func uuid5Name(name string) string {
	ns := [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	h := sha1.New()
	h.Write(ns[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x50 // version 5
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// loopVariableID builds the deterministic id for a loop variable:
// uuid5(NAMESPACE_URL, "nclang://loop-variable/{loopNodeID}/{variable}").
// Python's serializer mirrors this exactly (P3); previously the Python side
// emitted a fresh random uuid4 (serializer.py:378) — a documented
// uuid4-vs-uuid5 deviation that P3 closed.
func loopVariableID(loopNodeID, variable string) string {
	return uuid5Name("nclang://loop-variable/" + loopNodeID + "/" + variable)
}

// ConversationVariableID builds the deterministic id for a conversation
// variable: uuid5(NAMESPACE_URL, "nclang://conversation-variable/{name}").
// Python's register_conversation_var mirrors this exactly (P3); previously
// it minted str(uuid.uuid4()) (context.py:913-928) — a documented
// uuid4-vs-uuid5 deviation that P3 closed.
func ConversationVariableID(name string) string {
	return uuid5Name("nclang://conversation-variable/" + name)
}

// HumanInputDeliveryMethodID builds the deterministic webapp
// delivery-method id Go substitutes for the Python uuid5
// (human_input_lowerer.py:64-68):
// uuid5(NAMESPACE_URL, "nclang://human-input/{nodeID}/webapp").
func HumanInputDeliveryMethodID(nodeID string) string {
	return uuid5Name("nclang://human-input/" + nodeID + "/webapp")
}

// ── Variable value coercion (serializer.py:92-154) ─────────────────

func isIntKind(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

func isListOrMap(v any) bool {
	switch v.(type) {
	case []any, []string, map[string]any:
		return true
	}
	return false
}

// pythonStr mimics Python str() for the values _coerce_variable_value can
// receive: integral floats keep their ".0" suffix (str(1.0) == "1.0", but
// Go %v prints "1").
func pythonStr(v any) string {
	switch x := v.(type) {
	case float64:
		sv := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(sv, ".eE") {
			sv += ".0"
		}
		return sv
	case float32:
		sv := strconv.FormatFloat(float64(x), 'g', -1, 32)
		if !strings.ContainsAny(sv, ".eE") {
			sv += ".0"
		}
		return sv
	default:
		return fmt.Sprintf("%v", x)
	}
}

// pythonLiteral is the best-effort fallback for Python single-quoted
// literals ("['a', 'b']", "{'a': 1}") — serializer.py:107-112. Single quotes
// become double quotes and True/False/None tokens are rewritten to JSON only
// when they appear OUTSIDE string literals (so the value 'True story' or a
// key like 'TrueData' is never corrupted).
func pythonLiteral(s string) (any, bool) {
	var b strings.Builder
	inStr := false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\':
			b.WriteByte(c)
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
			i++
		case c == '\'':
			b.WriteByte('"')
			inStr = !inStr
			i++
		case !inStr && (c == 'T' || c == 'F' || c == 'N'):
			if idx, ok := matchPyKeyword(s, i); ok {
				b.WriteString(pyKeywordJSON(s[i : i+idx]))
				i += idx
				continue
			}
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	var v any
	if json.Unmarshal([]byte(b.String()), &v) == nil {
		return v, true
	}
	return nil, false
}

// matchPyKeyword reports whether s[i:] begins with a standalone
// True/False/None token (word boundaries on both sides).
func matchPyKeyword(s string, i int) (int, bool) {
	for _, kw := range []struct{ py string }{{"True"}, {"False"}, {"None"}} {
		if strings.HasPrefix(s[i:], kw.py) {
			end := i + len(kw.py)
			beforeOK := i == 0 || !isPyWordChar(s[i-1])
			afterOK := end >= len(s) || !isPyWordChar(s[end])
			if beforeOK && afterOK {
				return len(kw.py), true
			}
		}
	}
	return 0, false
}

func isPyWordChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func pyKeywordJSON(tok string) string {
	switch tok {
	case "True":
		return "true"
	case "False":
		return "false"
	default:
		return "null"
	}
}

// valueOrEmpty implements Python `value if value else ""` truthiness for the
// scalar branch of _coerce_variable_value (:151-154).
func valueOrEmpty(v any) any {
	switch x := v.(type) {
	case string:
		if x == "" {
			return ""
		}
		return x
	case bool:
		if !x {
			return ""
		}
		return x
	case float64:
		if x == 0 {
			return ""
		}
		return x
	case float32:
		if x == 0 {
			return ""
		}
		return x
	case []any:
		if len(x) == 0 {
			return ""
		}
		return x
	case map[string]any:
		if len(x) == 0 {
			return ""
		}
		return x
	default:
		if isIntKind(v) {
			rv := reflect.ValueOf(v)
			if rv.Kind() >= reflect.Uint && rv.Kind() <= reflect.Uint64 {
				if rv.Uint() == 0 {
					return ""
				}
			} else if rv.Int() == 0 {
				return ""
			}
		}
		return v
	}
}

// coerceVariableValue ports serializer.py _coerce_variable_value (:92-154).
func coerceVariableValue(value any, valueType string) any {
	if value == nil {
		return ""
	}
	switch valueType {
	case "number", "integer":
		switch v := value.(type) {
		case bool:
			// Python float(True) == 1.0 -> int 1 (serializer.py:98-104).
			if v {
				return 1
			}
			return 0
		case string:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return v
			}
			if f == math.Trunc(f) {
				return int(f)
			}
			return f
		case float64:
			if v == math.Trunc(v) {
				return int(v)
			}
			return v
		case float32:
			f := float64(v)
			if f == math.Trunc(f) {
				return int(f)
			}
			return f
		default:
			if isIntKind(value) {
				return value
			}
			s := fmt.Sprintf("%v", value)
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return value
			}
			if f == math.Trunc(f) {
				return int(f)
			}
			return f
		}
	case "boolean":
		// Python str(value).lower() — Go's %v prints float64(1.0) as "1",
		// Python prints "1.0"; pythonStr restores the Python shape so
		// 1.0 is NOT mistaken for the boolean token "1" (serializer.py:117-122).
		s := strings.ToLower(pythonStr(value))
		if s == "true" || s == "1" {
			return true
		}
		if s == "false" || s == "0" {
			return false
		}
		return value
	default:
		if strings.HasPrefix(valueType, "array") || valueType == "object" {
			if s, ok := value.(string); ok && (strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{")) {
				var parsed any
				if json.Unmarshal([]byte(s), &parsed) == nil {
					return parsed
				}
				if lit, ok := pythonLiteral(s); ok {
					return lit
				}
			}
			if isListOrMap(value) {
				return value
			}
			return value
		}
		// string / text-input / file / select / paragraph
		return valueOrEmpty(value)
	}
}

// normalizeValueType ports serializer.py _normalize_value_type (:156-165).
func normalizeValueType(valueType string, value any) string {
	if valueType == "number" {
		if c := coerceVariableValue(value, "number"); isIntKind(c) {
			return "integer"
		}
	}
	return valueType
}

// ── Shared component serializers (serializer.py:86-411) ────────────

// serializeVariableSelector ports _serialize_variable_selector (:91-99):
// dict form when variable/value_type are set, else the plain path list.
func serializeVariableSelector(vs *VariableSelectorNode) any {
	if vs.Variable != "" || vs.ValueType != "" {
		d := NewOrderedMap()
		d.Set("value_selector", orEmptyStringSlice(vs.Path))
		if vs.ValueType != "" {
			d.Set("value_type", vs.ValueType)
		}
		if vs.Variable != "" {
			d.Set("variable", vs.Variable)
		}
		return d
	}
	return orEmptyStringSlice(vs.Path)
}

// serializeCompletionParams ports _serialize_completion_params (:73-89).
func serializeCompletionParams(cp CompletionParams) *OrderedMap {
	d := NewOrderedMap()
	if cp.Temperature != 1.0 {
		d.Set("temperature", cp.Temperature)
	}
	if cp.MaxTokens != nil {
		d.Set("max_tokens", *cp.MaxTokens)
	}
	if cp.TopP != 1.0 {
		d.Set("top_p", cp.TopP)
	}
	if cp.TopK != nil {
		d.Set("top_k", *cp.TopK)
	}
	if cp.FrequencyPenalty != 0.0 {
		d.Set("frequency_penalty", cp.FrequencyPenalty)
	}
	if cp.PresencePenalty != 0.0 {
		d.Set("presence_penalty", cp.PresencePenalty)
	}
	if cp.ResponseFormat != "" {
		d.Set("response_format", cp.ResponseFormat)
	}
	if len(cp.Stop) > 0 {
		d.Set("stop", orEmptyStringSlice(cp.Stop))
	}
	for _, k := range sortedKeys(cp.Extra) {
		d.Set(k, normalizeAny(cp.Extra[k]))
	}
	return d
}

// hasMeaningfulCompletionParams ports _has_meaningful_completion_params
// (:230-246).
func hasMeaningfulCompletionParams(cp CompletionParams) bool {
	return cp.Temperature != 1.0 ||
		cp.MaxTokens != nil ||
		cp.TopP != 1.0 ||
		cp.TopK != nil ||
		cp.FrequencyPenalty != 0.0 ||
		cp.PresencePenalty != 0.0 ||
		cp.ResponseFormat != "" ||
		len(cp.Stop) > 0 ||
		len(cp.Extra) > 0
}

// serializeModelConfig ports _serialize_model_config (:214-228).
func serializeModelConfig(m *ModelConfigNode) *OrderedMap {
	d := NewOrderedMap()
	d.Set("provider", m.Provider)
	d.Set("name", m.Name)
	mode := m.Mode
	if mode == "" {
		mode = "chat"
	}
	d.Set("mode", mode)
	if hasMeaningfulCompletionParams(m.CompletionParams) {
		d.Set("completion_params", serializeCompletionParams(m.CompletionParams))
	}
	return d
}

// serializeRetryConfig ports _serialize_retry_config (:190-198).
func serializeRetryConfig(c *RetryConfig) *OrderedMap {
	d := NewOrderedMap()
	d.Set("max_retries", c.MaxRetries)
	d.Set("retry_interval", c.RetryInterval)
	if c.RetryEnabled {
		d.Set("retry_enabled", true)
	}
	return d
}

// serializeCondition ports _serialize_condition (:301-309).
func serializeCondition(c ConditionNode) *OrderedMap {
	d := NewOrderedMap()
	d.Set("variable_selector", serializeVariableSelector(&c.VariableSelector))
	d.Set("comparison_operator", conditionOperator(c.ComparisonOperator))
	d.Set("value", c.Value)
	return d
}

// serializeCase ports _serialize_case (:311-318).
func serializeCase(c CaseNode) *OrderedMap {
	d := NewOrderedMap()
	d.Set("case_id", c.CaseID)
	d.Set("logical_operator", c.LogicalOperator)
	if len(c.Conditions) > 0 {
		conds := make([]any, len(c.Conditions))
		for i, cond := range c.Conditions {
			conds[i] = serializeCondition(cond)
		}
		d.Set("conditions", conds)
	}
	return d
}

// serializePromptMessage ports _serialize_prompt_message (:320-325).
func serializePromptMessage(pm PromptMessageNode) *OrderedMap {
	d := NewOrderedMap()
	d.Set("role", pm.Role)
	d.Set("text", pm.Text)
	if pm.ID != "" {
		d.Set("id", pm.ID)
	}
	return d
}

// serializeVariableDef ports _serialize_variable_def (:327-352).
func serializeVariableDef(v VariableDef) *OrderedMap {
	d := NewOrderedMap()
	d.Set("label", v.Label)
	d.Set("type", v.Type)
	d.Set("variable", v.Variable)
	if v.Required {
		d.Set("required", true)
	} else {
		if v.Default != nil {
			d.Set("default", normalizeAny(v.Default))
		} else {
			d.Set("default", "")
		}
	}
	if v.Type == "file" || v.Type == "file-list" {
		d.Set("allowed_file_types", orEmptyStringSlice(v.AllowedFileTypes))
		d.Set("allowed_file_extensions", orEmptyStringSlice(v.AllowedFileExtensions))
		d.Set("allowed_file_upload_methods", orEmptyStringSlice(v.AllowedFileUploadMethods))
	}
	return d
}

// serializeOutputMapping ports _serialize_output_mapping (:354-360).
func serializeOutputMapping(o OutputMapping) *OrderedMap {
	d := NewOrderedMap()
	d.Set("variable", o.Variable)
	if o.ValueType != "" {
		d.Set("value_type", o.ValueType)
	}
	if len(o.ValueSelector) > 0 {
		d.Set("value_selector", orEmptyStringSlice(o.ValueSelector))
	}
	return d
}

// serializeMemoryConfig ports _serialize_memory_config (:362-379).
func serializeMemoryConfig(m *MemoryConfig) *OrderedMap {
	d := NewOrderedMap()
	d.Set("enabled", m.Enabled)
	if m.QueryPromptTemplate != "" {
		d.Set("query_prompt_template", m.QueryPromptTemplate)
	}
	if mw, ok := m.Window.(*MemoryWindowConfig); ok {
		window := NewOrderedMap()
		window.Set("enabled", mw.Enabled)
		if mw.Size != nil {
			window.Set("size", *mw.Size)
		}
		d.Set("window", window)
	} else if mw, ok := m.Window.(MemoryWindowConfig); ok {
		window := NewOrderedMap()
		window.Set("enabled", mw.Enabled)
		if mw.Size != nil {
			window.Set("size", *mw.Size)
		}
		d.Set("window", window)
	} else if m.Window != nil {
		d.Set("window", normalizeAny(m.Window))
	}
	if len(m.RolePrefix) > 0 {
		d.Set("role_prefix", normalizeAny(m.RolePrefix))
	}
	return d
}

// serializeContextConfig ports _serialize_context_config (:381-382).
func serializeContextConfig(c *ContextConfig) *OrderedMap {
	d := NewOrderedMap()
	d.Set("enabled", c.Enabled)
	d.Set("variable_selector", orEmptyStringSlice(c.VariableSelector))
	return d
}

// serializeVisionConfig ports _serialize_vision_config (:384-387).
func serializeVisionConfig(v *VisionConfig) *OrderedMap {
	d := NewOrderedMap()
	d.Set("enabled", v.Enabled)
	if v.Configs != nil && v.Configs.Len() > 0 {
		d.Set("configs", normalizeAny(v.Configs))
	}
	return d
}

// serializeStructuredOutput ports _serialize_structured_output (:389-392).
func serializeStructuredOutput(s *StructuredOutputConfig) *OrderedMap {
	d := NewOrderedMap()
	d.Set("enabled", s.Enabled)
	for _, k := range sortedKeys(s.SchemaDef) {
		d.Set(k, normalizeAny(s.SchemaDef[k]))
	}
	return d
}

// serializeClassDef ports _serialize_class_def (:394-396).
func serializeClassDef(c ClassDef) *OrderedMap {
	d := NewOrderedMap()
	d.Set("id", c.ID)
	d.Set("name", c.Name)
	d.Set("label", c.Label)
	return d
}

// serializeLoopVariable ports _serialize_loop_variable (:369-385). The
// random uuid4 id is replaced by a deterministic uuid5-style id (see
// loopVariableID and task-26-report.md).
func serializeLoopVariable(lv LoopVariable, loopNodeID string) *OrderedMap {
	d := NewOrderedMap()
	d.Set("label", lv.Variable)
	valueType := lv.ValueType
	if valueType == "" {
		valueType = "constant"
	}
	d.Set("value_type", valueType)
	varType := lv.VarType
	if varType == "" {
		varType = "string"
	}
	d.Set("var_type", varType)
	if lv.ID != "" {
		d.Set("id", lv.ID)
	} else {
		d.Set("id", loopVariableID(loopNodeID, lv.Variable))
	}
	if lv.Value != nil {
		d.Set("value", normalizeAny(lv.Value))
	}
	return d
}

// serializeListFilterConfig ports _serialize_list_filter_config (:387-391).
func serializeListFilterConfig(c ListFilterConfig) *OrderedMap {
	d := NewOrderedMap()
	d.Set("enabled", c.Enabled)
	items := make([]any, len(c.Conditions))
	for i, item := range c.Conditions {
		it := NewOrderedMap()
		it.Set("key", item.Key)
		it.Set("comparison_operator", item.ComparisonOperator)
		if item.Value != nil {
			it.Set("value", normalizeAny(item.Value))
		}
		items[i] = it
	}
	d.Set("conditions", items)
	return d
}

// serializeListExtractConfig ports _serialize_list_extract_config (:393-395).
func serializeListExtractConfig(c ListExtractConfig) *OrderedMap {
	d := NewOrderedMap()
	d.Set("enabled", c.Enabled)
	d.Set("serial", c.Serial)
	return d
}

// serializeListOrderConfig ports _serialize_list_order_config (:397-399).
func serializeListOrderConfig(c ListOrderConfig) *OrderedMap {
	d := NewOrderedMap()
	d.Set("enabled", c.Enabled)
	d.Set("key", c.Key)
	d.Set("value", c.Value)
	return d
}

// serializeListLimitConfig ports _serialize_list_limit_config (:401-403).
func serializeListLimitConfig(c ListLimitConfig) *OrderedMap {
	d := NewOrderedMap()
	d.Set("enabled", c.Enabled)
	d.Set("size", c.Size)
	return d
}

// serializeAssignmentItem ports _serialize_assignment_item (:405-414).
func serializeAssignmentItem(a AssignmentItem) *OrderedMap {
	d := NewOrderedMap()
	d.Set("variable_selector", serializeVariableSelector(&a.VariableSelector))
	d.Set("operation", a.Operation)
	d.Set("input_type", a.InputType)
	switch v := a.Value.(type) {
	case *VariableSelectorNode:
		d.Set("value", serializeVariableSelector(v))
	case VariableSelectorNode:
		d.Set("value", serializeVariableSelector(&v))
	default:
		d.Set("value", normalizeAny(a.Value))
	}
	return d
}

// serializeParameterDef ports _serialize_parameter_def (:416-424).
func serializeParameterDef(p ParameterDef) *OrderedMap {
	d := NewOrderedMap()
	d.Set("name", p.Name)
	d.Set("type", p.Type)
	d.Set("description", p.Description)
	d.Set("required", p.Required)
	if p.Options != nil {
		d.Set("options", orEmptyStringSlice(*p.Options))
	}
	return d
}

// serializeStringSource ports _serialize_string_source (:426-428).
func serializeStringSource(s *StringSourceNode) *OrderedMap {
	d := NewOrderedMap()
	d.Set("type", s.Type)
	d.Set("selector", orEmptyStringSlice(s.Selector))
	d.Set("value", s.Value)
	return d
}

// serializeHumanInputField ports _serialize_human_input_field (:430-451).
func serializeHumanInputField(h HumanInputField) *OrderedMap {
	d := NewOrderedMap()
	switch f := h.(type) {
	case ParagraphHumanInput:
		d.Set("type", f.Type)
		d.Set("output_variable_name", f.OutputVariableName)
		if f.Default != nil {
			d.Set("default", serializeStringSource(f.Default))
		}
	case *ParagraphHumanInput:
		d.Set("type", f.Type)
		d.Set("output_variable_name", f.OutputVariableName)
		if f.Default != nil {
			d.Set("default", serializeStringSource(f.Default))
		}
	case SelectHumanInput:
		d.Set("type", f.Type)
		d.Set("output_variable_name", f.OutputVariableName)
		os := NewOrderedMap()
		os.Set("type", f.OptionSource.Type)
		os.Set("selector", orEmptyStringSlice(f.OptionSource.Selector))
		os.Set("value", orEmptyStringSlice(f.OptionSource.Value))
		d.Set("option_source", os)
	case *SelectHumanInput:
		d.Set("type", f.Type)
		d.Set("output_variable_name", f.OutputVariableName)
		os := NewOrderedMap()
		os.Set("type", f.OptionSource.Type)
		os.Set("selector", orEmptyStringSlice(f.OptionSource.Selector))
		os.Set("value", orEmptyStringSlice(f.OptionSource.Value))
		d.Set("option_source", os)
	case FileHumanInput:
		d.Set("type", f.Type)
		d.Set("output_variable_name", f.OutputVariableName)
		d.Set("allowed_file_types", orEmptyStringSlice(f.AllowedFileTypes))
		d.Set("allowed_file_extensions", orEmptyStringSlice(f.AllowedFileExtensions))
		d.Set("allowed_file_upload_methods", orEmptyStringSlice(f.AllowedFileUploadMethods))
	case *FileHumanInput:
		d.Set("type", f.Type)
		d.Set("output_variable_name", f.OutputVariableName)
		d.Set("allowed_file_types", orEmptyStringSlice(f.AllowedFileTypes))
		d.Set("allowed_file_extensions", orEmptyStringSlice(f.AllowedFileExtensions))
		d.Set("allowed_file_upload_methods", orEmptyStringSlice(f.AllowedFileUploadMethods))
	case FileListHumanInput:
		d.Set("type", f.Type)
		d.Set("output_variable_name", f.OutputVariableName)
		d.Set("allowed_file_types", orEmptyStringSlice(f.AllowedFileTypes))
		d.Set("allowed_file_extensions", orEmptyStringSlice(f.AllowedFileExtensions))
		d.Set("allowed_file_upload_methods", orEmptyStringSlice(f.AllowedFileUploadMethods))
		d.Set("number_limits", f.NumberLimits)
	case *FileListHumanInput:
		d.Set("type", f.Type)
		d.Set("output_variable_name", f.OutputVariableName)
		d.Set("allowed_file_types", orEmptyStringSlice(f.AllowedFileTypes))
		d.Set("allowed_file_extensions", orEmptyStringSlice(f.AllowedFileExtensions))
		d.Set("allowed_file_upload_methods", orEmptyStringSlice(f.AllowedFileUploadMethods))
		d.Set("number_limits", f.NumberLimits)
	default:
		// unreachable: the four concrete human-input shapes are modeled above
	}
	return d
}

// serializeDeliveryMethod renders one delivery-method dict with the Python
// dict insertion order (id, type, enabled — human_input_lowerer.py:64-68).
// Python passes delivery_methods through verbatim (serializer.py:797-798),
// so an unexpected element type is a programming error, not a wire case:
// panic loudly instead of serializing a misleading {} (task-38 review
// MINOR 2).
func serializeDeliveryMethod(m any) *OrderedMap {
	dm, ok := m.(map[string]any)
	if !ok {
		panic("dify: delivery_methods element must be map[string]any, got " + fmt.Sprintf("%T", m))
	}
	d := NewOrderedMap()
	for _, key := range []string{"id", "type", "enabled"} {
		d.Set(key, dm[key])
	}
	return d
}

// serializeUserAction ports _serialize_user_action (:453-458).
func serializeUserAction(ua UserAction) *OrderedMap {
	d := NewOrderedMap()
	d.Set("id", ua.ID)
	d.Set("title", ua.Title)
	style := ua.ButtonStyle
	if style == "" {
		style = "default"
	}
	d.Set("button_style", style)
	return d
}

// serializeDatasourceInput ports _serialize_datasource_input (:830-833).
func serializeDatasourceInput(v DatasourceInputNode) *OrderedMap {
	d := NewOrderedMap()
	d.Set("value", normalizeAny(v.Value))
	if v.Type != nil {
		d.Set("type", *v.Type)
	}
	return d
}

// ── DataNode base (serializer.py:206-226) ──────────────────────────

// serializeBase ports _serialize_base (:206-226): type, title, iteration_id,
// loop_id, isInIteration, isInLoop, error_strategy, default_value.
func serializeBase(nd *DataNode, nodeType string) *OrderedMap {
	d := NewOrderedMap()
	d.Set("type", nodeType)
	if nd.Title != "" {
		d.Set("title", nd.Title)
	}
	if nd.IterationID != "" {
		d.Set("iteration_id", nd.IterationID)
	}
	if nd.LoopID != "" {
		d.Set("loop_id", nd.LoopID)
	}
	if nd.IsInIteration {
		d.Set("isInIteration", true)
	}
	if nd.IsInLoop {
		d.Set("isInLoop", true)
	}
	if nd.ErrorStrategy != nil {
		d.Set("error_strategy", *nd.ErrorStrategy)
	}
	if nd.DefaultValue != nil {
		d.Set("default_value", normalizeAny(*nd.DefaultValue))
	}
	return d
}

// ── Per-type node serializers (serializer.py:472-885) ──────────────

func serializeStart(nd *StartDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "start")
	vars := make([]any, len(nd.Variables))
	for i, v := range nd.Variables {
		vars[i] = serializeVariableDef(v)
	}
	d.Set("variables", vars)
	return d
}

func serializeOutputEnd(nd *OutputNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "end")
	outs := make([]any, len(nd.Outputs))
	for i, o := range nd.Outputs {
		outs[i] = serializeOutputMapping(o)
	}
	d.Set("outputs", outs)
	return d
}

func serializeAnswer(nd *AnswerNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "answer")
	if nd.Answer != "" {
		d.Set("answer", nd.Answer)
	}
	if nd.Variables != nil {
		vars := make([]any, len(nd.Variables))
		for i, v := range nd.Variables {
			vars[i] = serializeVariableSelector(&v)
		}
		d.Set("variables", vars)
	} else {
		d.Set("variables", []any{})
	}
	return d
}

func serializeLLM(nd *LLMDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "llm")
	if nd.Model != nil {
		d.Set("model", serializeModelConfig(nd.Model))
	}
	if len(nd.PromptTemplate) > 0 {
		msgs := make([]any, len(nd.PromptTemplate))
		for i, m := range nd.PromptTemplate {
			msgs[i] = serializePromptMessage(m)
		}
		d.Set("prompt_template", msgs)
	}
	if nd.Memory != nil {
		d.Set("memory", serializeMemoryConfig(nd.Memory))
	}
	if nd.Context != nil {
		d.Set("context", serializeContextConfig(nd.Context))
	}
	if nd.Vision != nil {
		d.Set("vision", serializeVisionConfig(nd.Vision))
	}
	if len(nd.Variables) > 0 {
		vars := make([]any, len(nd.Variables))
		for i, v := range nd.Variables {
			vars[i] = serializeVariableSelector(&v)
		}
		d.Set("variables", vars)
	}
	if nd.StructuredOutput != nil {
		d.Set("structured_output", serializeStructuredOutput(nd.StructuredOutput))
	}
	if nd.ReasoningFormat != nil {
		d.Set("reasoning_format", *nd.ReasoningFormat)
	}
	if nd.RetryConfig != nil {
		d.Set("retry_config", serializeRetryConfig(nd.RetryConfig))
	}
	return d
}

func serializeIfElse(nd *IfElseDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "if-else")
	if nd.Cases != nil {
		cases := make([]any, len(nd.Cases))
		for i, c := range nd.Cases {
			cases[i] = serializeCase(c)
		}
		d.Set("cases", cases)
	} else {
		d.Set("cases", []any{})
	}
	return d
}

func serializeCode(nd *CodeDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "code")
	d.Set("code_language", nd.CodeLanguage)
	d.Set("code", nd.Code)
	if nd.Variables != nil {
		vars := make([]any, len(nd.Variables))
		for i, v := range nd.Variables {
			vars[i] = serializeVariableSelector(&v)
		}
		d.Set("variables", vars)
	} else {
		d.Set("variables", []any{})
	}
	if len(nd.Outputs) > 0 {
		outs := NewOrderedMap()
		keys := nd.OutputOrder // Python dict insertion order (declaration order)
		if len(keys) == 0 {
			keys = sortedKeys(nd.Outputs)
		}
		for _, k := range keys {
			od, ok := nd.Outputs[k]
			if !ok {
				continue // defensive: order must stay a subset of Outputs
			}
			outs.Set(k, serializeOutputDef(&od))
		}
		d.Set("outputs", outs)
	}
	if len(nd.Dependencies) > 0 {
		d.Set("dependencies", orEmptyStringSlice(nd.Dependencies))
	}
	return d
}

// httpParamsOrEmpty ports `nd.params or ""` (serializer.py:553): a falsy
// value (nil / "" / empty map) becomes the empty string.
func httpParamsOrEmpty(p any) any {
	switch v := p.(type) {
	case nil:
		return ""
	case string:
		if v == "" {
			return ""
		}
		return v
	case map[string]any:
		if len(v) == 0 {
			return ""
		}
		return normalizeAny(v)
	default:
		return normalizeAny(p)
	}
}

func serializeHTTPRequest(nd *HTTPRequestDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "http-request")
	d.Set("method", nd.Method)
	d.Set("url", nd.URL)
	d.Set("headers", nd.Headers)
	d.Set("params", httpParamsOrEmpty(nd.Params))
	if nd.Body != nil {
		body := NewOrderedMap()
		body.Set("type", nd.Body.Type)
		body.Set("data", nd.Body.Data)
		d.Set("body", body)
	} else {
		body := NewOrderedMap()
		body.Set("type", "none")
		body.Set("data", "")
		d.Set("body", body)
	}
	if nd.Timeout != nil {
		t := NewOrderedMap()
		t.Set("connect", nd.Timeout.MaxConnectTimeout)
		t.Set("read", nd.Timeout.MaxReadTimeout)
		// Python quirk kept verbatim (serializer.py:565): write = max_read.
		t.Set("write", nd.Timeout.MaxReadTimeout)
		d.Set("timeout", t)
	}
	if nd.Authorization != nil {
		auth := NewOrderedMap()
		auth.Set("type", nd.Authorization.Type)
		if nd.Authorization.Config != nil {
			cfg := NewOrderedMap()
			cfg.Set("type", nd.Authorization.Config.Type)
			cfg.Set("api_key", nd.Authorization.Config.APIKey)
			cfg.Set("header", nd.Authorization.Config.Header)
			auth.Set("config", cfg)
		}
		d.Set("authorization", auth)
	}
	if nd.RetryConfig != nil {
		d.Set("retry_config", serializeRetryConfig(nd.RetryConfig))
	}
	vars := make([]any, len(nd.Variables))
	for i, v := range nd.Variables {
		vars[i] = serializeVariableSelector(&v)
	}
	d.Set("variables", vars)
	return d
}

func serializeTool(nd *ToolDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "tool")
	d.Set("tool_name", nd.ToolName)
	if nd.ToolLabel != "" {
		d.Set("tool_label", nd.ToolLabel)
	}
	if nd.ProviderID != "" {
		d.Set("provider_id", nd.ProviderID)
	}
	if nd.ProviderType != "" {
		d.Set("provider_type", nd.ProviderType)
	}
	if nd.ToolParameters != nil {
		if len(nd.ToolParameterOrder) > 0 {
			params := NewOrderedMap()
			for _, k := range nd.ToolParameterOrder {
				if v, ok := nd.ToolParameters[k]; ok {
					params.Set(k, normalizeAny(v))
				}
			}
			d.Set("tool_parameters", params)
		} else {
			d.Set("tool_parameters", normalizeAny(nd.ToolParameters))
		}
	}
	if nd.ToolConfiguration != nil {
		d.Set("tool_configuration", normalizeAny(nd.ToolConfiguration))
	}
	if nd.ProviderName != nil {
		d.Set("provider_name", *nd.ProviderName)
	}
	if nd.ToolConfigurations != nil {
		d.Set("tool_configurations", normalizeAny(nd.ToolConfigurations))
	}
	if nd.ToolDescription != nil && *nd.ToolDescription != "" {
		d.Set("tool_description", *nd.ToolDescription)
	}
	if nd.ToolNodeVersion != nil && *nd.ToolNodeVersion != "" {
		d.Set("tool_node_version", *nd.ToolNodeVersion)
	}
	if nd.OutputSchema != nil {
		d.Set("output_schema", normalizeAny(nd.OutputSchema))
	}
	if nd.ParamSchemas != nil {
		ps := make([]any, len(nd.ParamSchemas))
		for i, m := range nd.ParamSchemas {
			ps[i] = normalizeAny(m)
		}
		d.Set("paramSchemas", ps)
	}
	if nd.Params != nil {
		d.Set("params", normalizeAny(nd.Params))
	}
	if nd.RetryConfig != nil {
		d.Set("retry_config", serializeRetryConfig(nd.RetryConfig))
	}
	return d
}

func serializeTemplateTransform(nd *TemplateTransformDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "template-transform")
	d.Set("template", nd.Template)
	if nd.Variables != nil {
		vars := make([]any, len(nd.Variables))
		for i, v := range nd.Variables {
			vars[i] = serializeVariableSelector(&v)
		}
		d.Set("variables", vars)
	} else {
		d.Set("variables", []any{})
	}
	return d
}

func serializeKnowledgeRetrieval(nd *KnowledgeRetrievalDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "knowledge-retrieval")
	d.Set("dataset_ids", orEmptyStringSlice(nd.DatasetIDs))
	if nd.QueryVariableSelector != nil {
		d.Set("query_variable_selector", serializeVariableSelector(nd.QueryVariableSelector))
	} else {
		d.Set("query_variable_selector", []any{"sys", "query"})
	}
	query := ""
	if nd.Query != nil {
		query = *nd.Query
	}
	d.Set("query", query)
	mode := "single"
	if nd.RetrievalMode != nil && *nd.RetrievalMode != "" {
		mode = *nd.RetrievalMode
	}
	d.Set("retrieval_mode", mode)
	if len(nd.MultipleRetrievalConfig) > 0 {
		d.Set("multiple_retrieval_config", normalizeAny(nd.MultipleRetrievalConfig))
	}
	if len(nd.SingleRetrievalConfig) > 0 {
		d.Set("single_retrieval_config", normalizeAny(nd.SingleRetrievalConfig))
	} else {
		model := NewOrderedMap()
		model.Set("completion_params", NewOrderedMap())
		model.Set("mode", "chat")
		model.Set("name", "")
		model.Set("provider", "")
		single := NewOrderedMap()
		single.Set("model", model)
		d.Set("single_retrieval_config", single)
	}
	if len(nd.RetrievalModel) > 0 {
		d.Set("retrieval_model", normalizeAny(nd.RetrievalModel))
	}
	return d
}

func serializeQuestionClassifier(nd *QuestionClassifierDataNode) (*OrderedMap, error) {
	if nd.QueryVariableSelector == nil {
		return nil, fmt.Errorf("question-classifier export requires query_variable_selector")
	}
	if nd.Model == nil {
		return nil, fmt.Errorf("question-classifier export requires model")
	}
	d := serializeBase(&nd.DataNode, "question-classifier")
	d.Set("query_variable_selector", serializeVariableSelector(nd.QueryVariableSelector))
	d.Set("model", serializeModelConfig(nd.Model))
	classes := make([]any, len(nd.Classes))
	for i, c := range nd.Classes {
		classes[i] = serializeClassDef(c)
	}
	d.Set("classes", classes)
	if nd.Instruction != nil && *nd.Instruction != "" {
		d.Set("instruction", *nd.Instruction)
	}
	if nd.Memory != nil {
		d.Set("memory", serializeMemoryConfig(nd.Memory))
	}
	if nd.Vision != nil {
		d.Set("vision", serializeVisionConfig(nd.Vision))
	}
	return d, nil
}

func serializeParameterExtractor(nd *ParameterExtractorDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "parameter-extractor")
	if nd.Model != nil {
		d.Set("model", serializeModelConfig(nd.Model))
	}
	if len(nd.Parameters) > 0 {
		params := make([]any, len(nd.Parameters))
		for i, p := range nd.Parameters {
			params[i] = serializeParameterDef(p)
		}
		d.Set("parameters", params)
	}
	if len(nd.Query) > 0 {
		d.Set("query", orEmptyStringSlice(nd.Query))
	}
	if nd.Instruction != "" {
		d.Set("instruction", nd.Instruction)
	}
	if nd.Memory != nil {
		// serializer.py:637-647: a memory whose only setting is a window is
		// emitted in the simplified {window: {...}} shape.
		if mw, ok := nd.Memory.Window.(*MemoryWindowConfig); ok &&
			nd.Memory.QueryPromptTemplate == "" && len(nd.Memory.RolePrefix) == 0 {
			win := NewOrderedMap()
			win.Set("enabled", mw.Enabled)
			if mw.Size != nil {
				win.Set("size", *mw.Size)
			}
			simplified := NewOrderedMap()
			simplified.Set("window", win)
			d.Set("memory", simplified)
		} else if mw, ok := nd.Memory.Window.(MemoryWindowConfig); ok &&
			nd.Memory.QueryPromptTemplate == "" && len(nd.Memory.RolePrefix) == 0 {
			win := NewOrderedMap()
			win.Set("enabled", mw.Enabled)
			if mw.Size != nil {
				win.Set("size", *mw.Size)
			}
			simplified := NewOrderedMap()
			simplified.Set("window", win)
			d.Set("memory", simplified)
		} else {
			d.Set("memory", serializeMemoryConfig(nd.Memory))
		}
	}
	if nd.ReasoningMode != nil && *nd.ReasoningMode != "" {
		d.Set("reasoning_mode", *nd.ReasoningMode)
	}
	if nd.Vision != nil {
		d.Set("vision", serializeVisionConfig(nd.Vision))
	}
	return d
}

func serializeLoop(nd *LoopDataNode, nodeID string) *OrderedMap {
	d := serializeBase(&nd.DataNode, "loop")
	if len(nd.LoopVariables) > 0 {
		lvs := make([]any, len(nd.LoopVariables))
		for i, lv := range nd.LoopVariables {
			lvs[i] = serializeLoopVariable(lv, nodeID)
		}
		d.Set("loop_variables", lvs)
	}
	if len(nd.BreakConditions) > 0 {
		conds := make([]any, len(nd.BreakConditions))
		for i, c := range nd.BreakConditions {
			conds[i] = serializeCondition(c)
		}
		d.Set("break_conditions", conds)
	}
	// Python dataclass default loop_count=100 (ast_nodes.py:538) applies when
	// the Go value is 0. NOTE the masking: Go lowering overwrites LoopCount
	// from the statement limit (flow_loop.go:68, ln.LoopCount =
	// statement.Limit()), so a parseable `limit 0` would be masked to 100
	// here. That is safe because the frontend verifier rejects loop_count <= 0
	// (verifier.py:511-517, LOWERING_INVARIANT "Loop '{id}' has non-positive
	// loop_count ..."), so every verifier-accepted program either carries a
	// positive limit or reaches this serializer only via direct construction.
	loopCount := nd.LoopCount
	if loopCount == 0 {
		loopCount = 100
	}
	d.Set("loop_count", loopCount)
	if nd.ErrorHandleMode != "" && nd.ErrorHandleMode != "terminated" {
		d.Set("error_handle_mode", nd.ErrorHandleMode)
	}
	logicalOperator := nd.LogicalOperator
	if logicalOperator == "" {
		logicalOperator = "and"
	}
	d.Set("logical_operator", logicalOperator)
	if nd.Metadata.StartNodeID != "" {
		d.Set("start_node_id", nd.Metadata.StartNodeID)
	}
	return d
}

func serializeLoopStart(nd *LoopStartDataNode) *OrderedMap {
	d := NewOrderedMap()
	d.Set("type", "loop-start")
	d.Set("selected", false)
	title := nd.Title
	if title == "" {
		title = "Loop Start"
	}
	d.Set("title", title)
	desc := ""
	if nd.Desc != nil {
		desc = *nd.Desc
	}
	d.Set("desc", desc)
	if nd.IterationID != "" {
		d.Set("iteration_id", nd.IterationID)
	}
	if nd.LoopID != "" {
		d.Set("loop_id", nd.LoopID)
	}
	if nd.IsInLoop {
		d.Set("isInLoop", true)
	}
	return d
}

func serializeLoopEnd(nd *LoopEndDataNode) *OrderedMap {
	return serializeBase(&nd.DataNode, "loop-end")
}

func serializeVariableAggregator(nd *VariableAggregatorDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "variable-aggregator")
	vars := make([]any, len(nd.Variables))
	for i, v := range nd.Variables {
		vars[i] = serializeVariableSelector(&v)
	}
	d.Set("variables", vars)
	if nd.OutputType != nil && *nd.OutputType != "" {
		d.Set("output_type", *nd.OutputType)
	}
	if nd.AdvancedSettings != nil {
		d.Set("advanced_settings", normalizeAny(nd.AdvancedSettings))
	}
	return d
}

func serializeAssigner(nd *AssignerDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "assigner")
	items := make([]any, len(nd.Items))
	for i, item := range nd.Items {
		items[i] = serializeAssignmentItem(item)
	}
	d.Set("items", items)
	d.Set("version", "2")
	return d
}

func serializeDocumentExtractor(nd *DocumentExtractorDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "document-extractor")
	if nd.VariableSelector != nil {
		d.Set("variable_selector", serializeVariableSelector(nd.VariableSelector))
	}
	if nd.IsArrayFile {
		d.Set("is_array_file", true)
	}
	return d
}

func serializeListOperator(nd *ListOperatorDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "list-operator")
	d.Set("variable", orEmptyStringSlice(nd.Variable))
	d.Set("filter_by", serializeListFilterConfig(nd.FilterBy))
	d.Set("extract_by", serializeListExtractConfig(nd.ExtractBy))
	d.Set("order_by", serializeListOrderConfig(nd.OrderBy))
	d.Set("limit", serializeListLimitConfig(nd.Limit))
	if nd.VarType != "" {
		d.Set("var_type", nd.VarType)
	}
	if nd.ItemVarType != "" {
		d.Set("item_var_type", nd.ItemVarType)
	}
	return d
}

func serializeAgent(nd *AgentDataNode) (*OrderedMap, error) {
	if nd.ToolNodeVersion == nil || *nd.ToolNodeVersion != "2" {
		return nil, fmt.Errorf("agent export requires explicit tool_node_version '2'")
	}
	d := serializeBase(&nd.DataNode, "agent")
	d.Set("output_schema", normalizeAny(nd.OutputSchema))
	d.Set("agent_strategy_name", nd.AgentStrategyName)
	d.Set("agent_strategy_provider_name", nd.AgentStrategyProviderName)
	d.Set("agent_strategy_label", nd.AgentStrategyLabel)
	params := NewOrderedMap()
	for _, k := range sortedKeys(nd.AgentParameters) {
		p := nd.AgentParameters[k]
		pv := NewOrderedMap()
		pv.Set("value", normalizeAny(p.Value))
		pv.Set("type", p.Type)
		params.Set(k, pv)
	}
	d.Set("agent_parameters", params)
	if nd.Model != nil {
		d.Set("model", serializeModelConfig(nd.Model))
	}
	if nd.Memory != nil {
		d.Set("memory", serializeMemoryConfig(nd.Memory))
	}
	if nd.PluginUniqueIdentifier != "" {
		d.Set("plugin_unique_identifier", nd.PluginUniqueIdentifier)
	}
	d.Set("tool_node_version", *nd.ToolNodeVersion)
	return d, nil
}

func serializeHumanInput(nd *HumanInputDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "human-input")
	d.Set("form_content", nd.FormContent)
	inputs := make([]any, len(nd.Inputs))
	for i, in := range nd.Inputs {
		inputs[i] = serializeHumanInputField(in)
	}
	d.Set("inputs", inputs)
	actions := make([]any, len(nd.UserActions))
	for i, ua := range nd.UserActions {
		actions[i] = serializeUserAction(ua)
	}
	d.Set("user_actions", actions)
	d.Set("timeout", nd.Timeout)
	d.Set("timeout_unit", nd.TimeoutUnit)
	if len(nd.DeliveryMethods) > 0 {
		methods := make([]any, len(nd.DeliveryMethods))
		for i, m := range nd.DeliveryMethods {
			methods[i] = serializeDeliveryMethod(m)
		}
		d.Set("delivery_methods", methods)
	}
	return d
}

func serializeDatasource(nd *DatasourceDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "datasource")
	d.Set("plugin_id", nd.PluginID)
	d.Set("provider_name", nd.ProviderName)
	d.Set("provider_type", nd.ProviderType)
	d.Set("datasource_name", nd.DatasourceName)
	if nd.DatasourceConfigurations != nil {
		d.Set("datasource_configurations", normalizeAny(nd.DatasourceConfigurations))
	}
	if nd.PluginUniqueIdentifier != nil {
		d.Set("plugin_unique_identifier", *nd.PluginUniqueIdentifier)
	}
	if nd.DatasourceParameters != nil {
		params := NewOrderedMap()
		for _, k := range sortedKeys(nd.DatasourceParameters) {
			params.Set(k, serializeDatasourceInput(nd.DatasourceParameters[k]))
		}
		d.Set("datasource_parameters", params)
	}
	return d
}

func serializeIteration(nd *IterationDataNode) *OrderedMap {
	d := serializeBase(&nd.DataNode, "iteration")
	if len(nd.IteratorSelector) > 0 {
		d.Set("iterator_selector", orEmptyStringSlice(nd.IteratorSelector))
	} else {
		d.Set("iterator_selector", []any{})
	}
	if len(nd.OutputSelector) > 0 {
		d.Set("output_selector", orEmptyStringSlice(nd.OutputSelector))
	} else {
		d.Set("output_selector", []any{})
	}
	outType := nd.OutputType
	if outType == "" {
		outType = ""
	}
	d.Set("output_type", outType)
	if nd.IteratorInputType != "" {
		d.Set("iterator_input_type", nd.IteratorInputType)
	}
	if nd.StartNodeID != "" {
		d.Set("start_node_id", nd.StartNodeID)
	}
	if nd.FlattenOutput != nil {
		d.Set("flatten_output", *nd.FlattenOutput)
	}
	if nd.ErrorHandleMode != "" && nd.ErrorHandleMode != "terminated" {
		d.Set("error_handle_mode", nd.ErrorHandleMode)
	}
	if nd.IsParallel {
		d.Set("is_parallel", true)
	}
	if nd.ParallelNums != 0 && nd.ParallelNums != 10 {
		d.Set("parallel_nums", nd.ParallelNums)
	}
	if nd.Height != 0 {
		d.Set("height", nd.Height)
	}
	if nd.Width != 0 {
		d.Set("width", nd.Width)
	}
	return d
}

func serializeIterationStart(nd *IterationStartDataNode) *OrderedMap {
	d := NewOrderedMap()
	d.Set("type", "iteration-start")
	d.Set("selected", false)
	title := nd.Title
	if title == "" {
		title = "Iteration Start"
	}
	d.Set("title", title)
	desc := ""
	if nd.Desc != nil {
		desc = *nd.Desc
	}
	d.Set("desc", desc)
	if nd.IsInIteration {
		d.Set("isInIteration", true)
	}
	if nd.IterationID != "" {
		d.Set("iteration_id", nd.IterationID)
	}
	if nd.LoopID != "" {
		d.Set("loop_id", nd.LoopID)
	}
	return d
}

func serializeCustomNote(nd *CustomNoteDataNode) *OrderedMap {
	d := NewOrderedMap()
	d.Set("type", "")
	d.Set("selected", false)
	text := nd.Text
	if text == "" {
		text = ""
	}
	d.Set("text", text)
	author := nd.Author
	if author == "" {
		author = ""
	}
	d.Set("author", author)
	// Python default show_author=True (ast_nodes.py:758); nil means unset.
	showAuthor := true
	if nd.ShowAuthor != nil {
		showAuthor = *nd.ShowAuthor
	}
	d.Set("showAuthor", showAuthor)
	theme := nd.Theme
	if theme == "" {
		theme = ""
	}
	d.Set("theme", theme)
	d.Set("height", nd.NoteHeight)
	d.Set("width", nd.NoteWidth)
	return d
}

// ── Dispatch (serializer.py:879-955) ───────────────────────────────

// serializeDataNode ports _serialize_data_node (:940-955): dispatch on the
// concrete node type, then patch missing iteration_id/loop_id from the base
// fields (:947-952). Unknown node types raise an error — no silent fallback.
func serializeDataNode(nd NodeData, nodeID string) (*OrderedMap, error) {
	var result *OrderedMap
	switch n := nd.(type) {
	case *StartDataNode:
		result = serializeStart(n)
	case *OutputNode:
		result = serializeOutputEnd(n)
	case *AnswerNode:
		result = serializeAnswer(n)
	case *LLMDataNode:
		result = serializeLLM(n)
	case *IfElseDataNode:
		result = serializeIfElse(n)
	case *CodeDataNode:
		result = serializeCode(n)
	case *HTTPRequestDataNode:
		result = serializeHTTPRequest(n)
	case *ToolDataNode:
		result = serializeTool(n)
	case *TemplateTransformDataNode:
		result = serializeTemplateTransform(n)
	case *KnowledgeRetrievalDataNode:
		result = serializeKnowledgeRetrieval(n)
	case *QuestionClassifierDataNode:
		var err error
		result, err = serializeQuestionClassifier(n)
		if err != nil {
			return nil, err
		}
	case *ParameterExtractorDataNode:
		result = serializeParameterExtractor(n)
	case *LoopDataNode:
		result = serializeLoop(n, nodeID)
	case *LoopStartDataNode:
		result = serializeLoopStart(n)
	case *LoopEndDataNode:
		result = serializeLoopEnd(n)
	case *VariableAggregatorDataNode:
		result = serializeVariableAggregator(n)
	case *AssignerDataNode:
		result = serializeAssigner(n)
	case *DocumentExtractorDataNode:
		result = serializeDocumentExtractor(n)
	case *ListOperatorDataNode:
		result = serializeListOperator(n)
	case *AgentDataNode:
		var err error
		result, err = serializeAgent(n)
		if err != nil {
			return nil, err
		}
	case *HumanInputDataNode:
		result = serializeHumanInput(n)
	case *DatasourceDataNode:
		result = serializeDatasource(n)
	case *IterationDataNode:
		result = serializeIteration(n)
	case *IterationStartDataNode:
		result = serializeIterationStart(n)
	case *CustomNoteDataNode:
		result = serializeCustomNote(n)
	default:
		return nil, fmt.Errorf(
			"Cannot serialize unmodeled node type '%s'. Add a dedicated serializer to _DATA_NODE_SERIALIZERS.",
			nodeTypeName(nd),
		)
	}
	base := dataNodeBase(nd)
	if base != nil && base.IterationID != "" && !hasKey(result, "iteration_id") {
		result.Set("iteration_id", base.IterationID)
	}
	if base != nil && base.LoopID != "" && !hasKey(result, "loop_id") {
		result.Set("loop_id", base.LoopID)
	}
	return result, nil
}

// serializeNode ports _serialize_node (:918-936): id, type, position (only
// when x/y non-zero), data, parentId, zIndex, selected, draggable, selectable.
func serializeNode(node *NodeDecl) (*OrderedMap, error) {
	outerType := "custom"
	switch node.Data.(type) {
	case *IterationStartDataNode:
		outerType = "custom-iteration-start"
	case *LoopStartDataNode:
		outerType = "custom-loop-start"
	case *LoopEndDataNode:
		outerType = "custom-simple"
	case *CustomNoteDataNode:
		outerType = "custom-note"
	}
	d := NewOrderedMap()
	d.Set("id", node.ID)
	d.Set("type", outerType)
	if node.Position.X != 0 || node.Position.Y != 0 {
		pos := NewOrderedMap()
		pos.Set("x", v(node.Position.X))
		pos.Set("y", v(node.Position.Y))
		d.Set("position", pos)
	}
	data, err := serializeDataNode(node.Data, node.ID)
	if err != nil {
		return nil, err
	}
	d.Set("data", data)
	if node.ParentID != nil && *node.ParentID != "" {
		d.Set("parentId", *node.ParentID)
	}
	if node.ZIndex != nil {
		d.Set("zIndex", *node.ZIndex)
	}
	if node.Selected {
		d.Set("selected", true)
	}
	if node.Draggable != nil {
		d.Set("draggable", *node.Draggable)
	}
	if node.Selectable != nil {
		d.Set("selectable", *node.Selectable)
	}
	return d, nil
}

// serializeEdge ports _serialize_edge (:957-985).
func serializeEdge(edge *EdgeDecl, nodeTypes map[string]string) *OrderedMap {
	d := NewOrderedMap()
	d.Set("id", edge.ID)
	d.Set("source", edge.Source)
	d.Set("sourceHandle", edge.SourceHandle)
	d.Set("target", edge.Target)
	targetHandle := "target"
	if edge.TargetHandle != nil && *edge.TargetHandle != "" {
		targetHandle = *edge.TargetHandle
	}
	d.Set("targetHandle", targetHandle)
	d.Set("type", canonicalizeEdgeType(edge.Type))
	data := NewOrderedMap()
	inIteration := false
	if edge.IsInIteration != nil {
		inIteration = *edge.IsInIteration
	}
	inLoop := false
	if edge.IsInLoop != nil {
		inLoop = *edge.IsInLoop
	}
	data.Set("isInIteration", inIteration)
	data.Set("isInLoop", inLoop)
	data.Set("sourceType", nodeTypes[edge.Source])
	data.Set("targetType", nodeTypes[edge.Target])
	if inIteration && edge.IterationID != "" {
		data.Set("iteration_id", edge.IterationID)
	}
	if inLoop && edge.LoopID != "" {
		data.Set("loop_id", edge.LoopID)
	}
	d.Set("data", data)
	if edge.ZIndex != 0 {
		d.Set("zIndex", edge.ZIndex)
	}
	return d
}

// ── Features (serializer.py:913-1056) ──────────────────────────────

func isDefaultFileUpload(fu *FileUploadConfig) bool {
	return !fu.Enabled &&
		len(fu.AllowedFileExtensions) == 0 &&
		len(fu.AllowedFileTypes) == 0 &&
		len(fu.AllowedFileUploadMethods) == 0 &&
		fu.NumberLimits == 3 &&
		fu.Image == nil &&
		fu.FileLimits == nil
}

func serializeFeatures(features *WorkflowFeatures) *OrderedMap {
	result := NewOrderedMap()
	if features.FileUpload != nil && !isDefaultFileUpload(features.FileUpload) {
		fu := NewOrderedMap()
		if len(features.FileUpload.AllowedFileExtensions) > 0 {
			fu.Set("allowed_file_extensions", orEmptyStringSlice(features.FileUpload.AllowedFileExtensions))
		}
		if len(features.FileUpload.AllowedFileTypes) > 0 {
			fu.Set("allowed_file_types", orEmptyStringSlice(features.FileUpload.AllowedFileTypes))
		}
		if len(features.FileUpload.AllowedFileUploadMethods) > 0 {
			fu.Set("allowed_file_upload_methods", orEmptyStringSlice(features.FileUpload.AllowedFileUploadMethods))
		}
		fu.Set("enabled", features.FileUpload.Enabled)
		if features.FileUpload.FileLimits != nil {
			fsc := features.FileUpload.FileLimits
			cfg := NewOrderedMap()
			cfg.Set("attachment_image_file_size_limit", fsc.AttachmentImageFileSizeLimit)
			cfg.Set("audio_file_size_limit", fsc.AudioFileSizeLimit)
			cfg.Set("batch_count_limit", fsc.BatchCountLimit)
			cfg.Set("file_size_limit", fsc.FileSizeLimit)
			cfg.Set("file_upload_limit", fsc.FileUploadLimit)
			cfg.Set("image_file_batch_limit", fsc.ImageFileBatchLimit)
			cfg.Set("image_file_size_limit", fsc.ImageFileSizeLimit)
			cfg.Set("single_chunk_attachment_limit", fsc.SingleChunkAttachmentLimit)
			cfg.Set("video_file_size_limit", fsc.VideoFileSizeLimit)
			cfg.Set("workflow_file_upload_limit", fsc.WorkflowFileUploadLimit)
			fu.Set("fileUploadConfig", cfg)
		}
		if features.FileUpload.Image != nil {
			img := features.FileUpload.Image
			image := NewOrderedMap()
			image.Set("enabled", img.Enabled)
			image.Set("number_limits", img.NumberLimits)
			image.Set("transfer_methods", orEmptyStringSlice(img.TransferMethods))
			fu.Set("image", image)
		}
		fu.Set("number_limits", features.FileUpload.NumberLimits)
		result.Set("file_upload", fu)
	} else {
		result.Set("file_upload", NewOrderedMap())
	}
	swa := NewOrderedMap()
	swa.Set("enabled", features.SensitiveWordAvoidance.Enabled)
	result.Set("sensitive_word_avoidance", swa)
	if features.TextToSpeech != nil {
		tts := NewOrderedMap()
		tts.Set("enabled", features.TextToSpeech.Enabled)
		tts.Set("language", features.TextToSpeech.Language)
		tts.Set("voice", features.TextToSpeech.Voice)
		result.Set("text_to_speech", tts)
	} else {
		tts := NewOrderedMap()
		tts.Set("enabled", false)
		tts.Set("language", "")
		tts.Set("voice", "")
		result.Set("text_to_speech", tts)
	}
	result.Set("opening_statement", features.OpeningStatement)
	rr := NewOrderedMap()
	rr.Set("enabled", features.RetrieverResource.Enabled)
	result.Set("retriever_resource", rr)
	stt := NewOrderedMap()
	stt.Set("enabled", features.SpeechToText.Enabled)
	result.Set("speech_to_text", stt)
	result.Set("suggested_questions", orEmptyStringSlice(features.SuggestedQuestions))
	sqaa := NewOrderedMap()
	sqaa.Set("enabled", features.SuggestedQuestionsAfterAnswer.Enabled)
	result.Set("suggested_questions_after_answer", sqaa)
	return result
}

// ── Top level (serializer.py:1058-1109) ────────────────────────────

func serializeConversationVariable(cv ConversationVariable) *OrderedMap {
	d := NewOrderedMap()
	d.Set("id", cv.ID)
	d.Set("name", cv.Name)
	vt := cv.ValueType
	if vt == "" {
		vt = "string"
	}
	d.Set("value_type", normalizeValueType(vt, cv.Value))
	d.Set("value", coerceVariableValue(cv.Value, cv.ValueType))
	d.Set("description", cv.Description)
	if len(cv.Selector) > 0 {
		d.Set("selector", orEmptyStringSlice(cv.Selector))
	} else {
		d.Set("selector", []any{"conversation", cv.Name})
	}
	for _, k := range sortedKeys(cv.Extra) {
		d.Set(k, normalizeAny(cv.Extra[k]))
	}
	return d
}

func serializeEnvironmentVariable(ev EnvironmentVariable) *OrderedMap {
	d := NewOrderedMap()
	d.Set("id", ev.ID)
	d.Set("name", ev.Name)
	vt := ev.ValueType
	if vt == "" {
		vt = "string"
	}
	d.Set("value_type", normalizeValueType(vt, ev.Value))
	d.Set("value", coerceVariableValue(ev.Value, ev.ValueType))
	d.Set("description", ev.Description)
	d.Set("selector", []any{"environment", ev.Name})
	for _, k := range sortedKeys(ev.Extra) {
		d.Set(k, normalizeAny(ev.Extra[k]))
	}
	return d
}

// SerializeToDict ports serializer.py serialize_to_dict (:1058-1109). Top
// level key order: app, kind, version, workflow, dependencies.
func SerializeToDict(ast *WorkflowAST) (*OrderedMap, error) {
	top := NewOrderedMap()
	app := NewOrderedMap()
	if ast.Envelope.App.Name != "" {
		app.Set("description", ast.Envelope.App.Description)
		app.Set("icon", ast.Envelope.App.Icon)
		iconBackground := ast.Envelope.App.IconBackground
		if iconBackground == "" {
			iconBackground = "#FFFFFF"
		}
		app.Set("icon_background", iconBackground)
		iconType := ast.Envelope.App.IconType
		if iconType == "" {
			iconType = "emoji"
		}
		app.Set("icon_type", iconType)
		app.Set("mode", ast.Envelope.App.Mode)
		app.Set("name", ast.Envelope.App.Name)
		app.Set("use_icon_as_answer_icon", ast.Envelope.App.UseIconAsAnswerIcon)
	} else {
		app.Set("mode", ast.Envelope.App.Mode)
		app.Set("name", "")
	}
	top.Set("app", app)
	top.Set("kind", ast.Envelope.Kind)
	top.Set("version", "0.6.0")

	// node_type_lookup for edge data (serializer.py:1065-1067).
	nodeTypeLookup := make(map[string]string, len(ast.Graph.Nodes))
	for i := range ast.Graph.Nodes {
		n := &ast.Graph.Nodes[i]
		nodeTypeLookup[n.ID] = dataNodeToType(n.Data)
	}

	nodes := make([]any, len(ast.Graph.Nodes))
	for i := range ast.Graph.Nodes {
		n, err := serializeNode(&ast.Graph.Nodes[i])
		if err != nil {
			return nil, err
		}
		nodes[i] = n
	}
	edges := make([]any, len(ast.Graph.Edges))
	for i := range ast.Graph.Edges {
		edges[i] = serializeEdge(&ast.Graph.Edges[i], nodeTypeLookup)
	}
	graph := NewOrderedMap()
	graph.Set("nodes", nodes)
	graph.Set("edges", edges)
	if ast.Graph.Viewport != nil {
		vp := NewOrderedMap()
		vp.Set("x", v(ast.Graph.Viewport.X))
		vp.Set("y", v(ast.Graph.Viewport.Y))
		vp.Set("zoom", v(ast.Graph.Viewport.Zoom))
		graph.Set("viewport", vp)
	} else {
		vp := NewOrderedMap()
		vp.Set("x", 0)
		vp.Set("y", 0)
		vp.Set("zoom", 1)
		graph.Set("viewport", vp)
	}

	wf := NewOrderedMap()
	convs := make([]any, len(ast.ConversationVariables))
	for i, cv := range ast.ConversationVariables {
		convs[i] = serializeConversationVariable(cv)
	}
	wf.Set("conversation_variables", convs)
	envs := make([]any, len(ast.EnvironmentVariables))
	for i, ev := range ast.EnvironmentVariables {
		envs[i] = serializeEnvironmentVariable(ev)
	}
	wf.Set("environment_variables", envs)
	wf.Set("rag_pipeline_variables", orEmptyStringSlice(ast.RagPipelineVariables))
	wf.Set("features", serializeFeatures(&ast.Features))
	wf.Set("graph", graph)
	top.Set("workflow", wf)

	if len(ast.Dependencies) > 0 {
		deps := make([]any, len(ast.Dependencies))
		for i, dep := range ast.Dependencies {
			deps[i] = normalizeAny(dep)
		}
		top.Set("dependencies", deps)
	} else {
		top.Set("dependencies", []any{})
	}
	return top, nil
}
