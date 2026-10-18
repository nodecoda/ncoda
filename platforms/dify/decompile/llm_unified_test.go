package decompile

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/llmprojection"
	cd "github.com/nodecoda/ncoda/pkg/ncoda/ast4"
	"github.com/nodecoda/ncoda/pkg/ncoda/render"
)

//@feature llm-unified-envelope
//@feature open-object

func TestStructuredLLMDecompilerRestoresPayloadTypeAndTextPath(t *testing.T) {
	n := &nodeInfo{ID: "llm-1", Type: "llm", Data: map[string]any{
		"model": map[string]any{"provider": "openai", "name": "gpt"},
		"structured_output": map[string]any{
			"enabled": true,
			"schema_def": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"value": map[string]any{
					"type": "object", "additionalProperties": false,
					"properties": map[string]any{"answer": map[string]any{"type": "string"}},
					"required":   []any{"answer"},
				}}, "required": []any{"value"},
			},
		},
	}}
	c := &converter{g: &wfGraph{nodes: map[string]*nodeInfo{"llm-1": n}}, modelBy: map[string]int{}}
	stmts := c.convertLLM(n)
	rendered := render.RenderStmt(stmts[0], 0)
	if !strings.Contains(rendered, "llm<LLMllm_1Payload>") {
		t.Fatalf("structured LLM = %s", rendered)
	}
	ref := c.refExpr("llm-1", "structured_output", []string{"value", "answer"})
	if got := render.RenderExpr(ref); got != "vllm_1.text.answer" {
		t.Fatalf("canonical ref = %q", got)
	}
}

func TestDifySchemaTypePreservesOptionalEnumAndMap(t *testing.T) {
	c := &converter{}
	raw := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"values": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}},
			"verdict": map[string]any{"anyOf": []any{
				map[string]any{"type": "string", "enum": []any{"approve", "reject"}},
				map[string]any{"type": "null"},
			}},
		},
		"required": []any{"values"},
	}
	if got := c.difySchemaType(raw, "Payload"); got != "Payload" {
		t.Fatalf("type = %q", got)
	}
	if len(c.extraTypes) != 1 || len(c.extraEnums) != 1 {
		t.Fatalf("types=%#v enums=%#v", c.extraTypes, c.extraEnums)
	}
	rendered := render.RenderTypeDecl(c.extraTypes[0])
	for _, want := range []string{"map<string, int> values", "Payloadverdict? verdict"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("payload type missing %q:\n%s", want, rendered)
		}
	}
	if got := render.RenderEnumDecl(c.extraEnums[0]); !strings.Contains(got, "enum Payloadverdict { approve, reject }") {
		t.Fatalf("enum = %s", got)
	}
}

func TestDifySchemaTypeUnknownNestedIsExplicit(t *testing.T) {
	c := &converter{}
	raw := map[string]any{
		"type":       "object",
		"properties": map[string]any{"mystery": map[string]any{"type": "future"}},
		"required":   []any{"mystery"},
	}
	if got := c.difySchemaType(raw, "Payload"); got != "" {
		t.Fatalf("unknown schema guessed as %q", got)
	}
	if len(c.unknownNotes) == 0 || !strings.Contains(strings.Join(c.unknownNotes, "\n"), "unknown-llm-schema") {
		t.Fatalf("unknown notes = %#v", c.unknownNotes)
	}
}

func TestDifySchemaTypeUnknownArrayItemIsExplicit(t *testing.T) {
	c := &converter{}
	raw := map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "future"},
	}
	if got := c.difySchemaType(raw, "Payload"); got != "" {
		t.Fatalf("unknown array item schema guessed as %q", got)
	}
	if len(c.unknownNotes) == 0 || !strings.Contains(strings.Join(c.unknownNotes, "\n"), "unknown-llm-schema") {
		t.Fatalf("unknown notes = %#v", c.unknownNotes)
	}
}

func TestDifyLLMConfigCompletionParamsRestored(t *testing.T) {
	n := &nodeInfo{ID: "llm-1", Type: "llm", Data: map[string]any{
		"model": map[string]any{
			"provider": "openai", "name": "gpt-4o",
			"completion_params": map[string]any{
				"temperature": 0.8, "max_tokens": 16384,
			},
		},
		"prompt_template": []any{map[string]any{"role": "user", "text": "hi"}},
	}}
	c := &converter{g: &wfGraph{nodes: map[string]*nodeInfo{"llm-1": n}}, modelBy: map[string]int{}}
	rendered := render.RenderStmt(c.convertLLM(n)[0], 0)
	for _, want := range []string{"temperature: 0.8", "max_tokens: 16384"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("completion_params config 缺少 %q:\n%s", want, rendered)
		}
	}
}

func TestCodeInputFromLLMMetadataRemainsOptional(t *testing.T) {
	llmNode := &nodeInfo{ID: "llm-1", Type: "llm"}
	codeNode := &nodeInfo{ID: "code-1", Type: "code", Data: map[string]any{
		"code_language": "python3",
		"code":          "def main(reason):\n    return {\"result\": reason or \"\"}",
		"variables": []any{map[string]any{
			"variable": "reason", "value_type": "string",
			"value_selector": []any{"llm-1", "reasoning_content"},
		}},
		"outputs": map[string]any{"result": map[string]any{"type": "string"}},
	}}
	c := &converter{g: &wfGraph{nodes: map[string]*nodeInfo{"llm-1": llmNode, "code-1": codeNode}}}
	stmt := c.convertCode(codeNode)[0]
	decl, ok := stmt.(*cd.VarDecl)
	if !ok {
		t.Fatalf("code statement = %T, want *decompile.VarDecl", stmt)
	}
	code, ok := decl.Init.(*cd.ForeignCodeExpr)
	if !ok || len(code.Inputs) != 1 {
		t.Fatalf("code initializer = %#v, want one foreign-code input", decl.Init)
	}
	if got := code.Inputs[0].Type; got != "string?" {
		t.Fatalf("metadata input type = %q, want string?", got)
	}
}

func TestHoistedLLMUsesCanonicalOptionalEnvelope(t *testing.T) {
	c := &converter{}
	typeName := c.nodeHoistType(&nodeInfo{ID: "llm-1", Type: "llm"})
	if typeName != "Hoistllm_1" {
		t.Fatalf("hoist type = %q", typeName)
	}
	if len(c.extraTypes) != 2 {
		t.Fatalf("extra types = %d, want usage and envelope", len(c.extraTypes))
	}
	usage := render.RenderTypeDecl(c.extraTypes[0])
	for _, field := range []string{"int? prompt_tokens", "int? completion_tokens", "int? total_tokens"} {
		if !strings.Contains(usage, field) {
			t.Errorf("usage type missing %q:\n%s", field, usage)
		}
	}
	envelope := render.RenderTypeDecl(c.extraTypes[1])
	for _, field := range []string{
		"string text", "string? reasoning_content", "LLMUsage? usage", "string? finish_reason",
	} {
		if !strings.Contains(envelope, field) {
			t.Errorf("envelope type missing %q:\n%s", field, envelope)
		}
	}
	if zero := render.RenderNCodaExpr(c.typeZeroValue(typeName)); zero != `{ "text": "", "reasoning_content": null, "usage": null, "finish_reason": null }` {
		t.Fatalf("hoist zero = %s", zero)
	}
}

func TestGeneratedLLMEnvelopeAdapterNormalizesToBareLLMRef(t *testing.T) {
	llmNode := &nodeInfo{ID: "llm-1", Type: "llm"}
	adapter := &nodeInfo{ID: "adapter-1", Type: "code", Data: map[string]any{
		"code": "def main(text):\n    " + llmprojection.AssemblyMarker + " = {\"text\": text}\n    return {\"response\": " + llmprojection.AssemblyMarker + "}",
		"variables": []any{map[string]any{
			"variable": "text", "value_selector": []any{"llm-1", "text"},
		}},
	}}
	c := &converter{g: &wfGraph{nodes: map[string]*nodeInfo{"llm-1": llmNode, "adapter-1": adapter}}}
	if producer, ok := c.llmEnvelopeAdapterProducer(adapter); !ok || producer != "llm-1" {
		t.Fatalf("generated adapter producer = %q, ok=%v", producer, ok)
	}
	if got := render.RenderExpr(c.refExpr("adapter-1", "response", nil)); got != "vllm_1" {
		t.Fatalf("adapter response normalized to %q, want bare LLM variable", got)
	}

	userCode := &nodeInfo{ID: "user-code", Type: "code", Data: map[string]any{
		"code":      "def main(text):\n    return {\"response\": {\"text\": text}}",
		"variables": adapter.Data["variables"],
	}}
	if producer, ok := c.llmEnvelopeAdapterProducer(userCode); ok || producer != "" {
		t.Fatalf("source-authored code misclassified as generated adapter: producer=%q", producer)
	}
}

// ANY-SHRINK L1-a 反编译侧锁定：dify LLM 节点的 temperature/max_tokens
// 已知键必须恢复进 ncoda config 字典（convert.go convertLLM 恢复逻辑）。
// 可选性：无这些键时 config 不包含对应条目（平台默认由编译侧兜底）。
func TestDifyLLMConfigKnownKeysRestored(t *testing.T) {
	n := &nodeInfo{ID: "llm-1", Type: "llm", Data: map[string]any{
		"model": map[string]any{"provider": "openai", "name": "gpt-4o"},
		"prompt_template": []any{
			map[string]any{"role": "user", "text": "hi"},
		},
		"temperature": 0.8,
		"max_tokens":  16384,
	}}
	c := &converter{g: &wfGraph{nodes: map[string]*nodeInfo{"llm-1": n}}, modelBy: map[string]int{}}
	stmts := c.convertLLM(n)
	rendered := render.RenderStmt(stmts[0], 0)
	for _, want := range []string{"temperature: 0.8", "max_tokens: 16384"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("dify LLM config 缺少 %q：\n%s", want, rendered)
		}
	}
	// 可选键缺省：config 不含 temperature/max_tokens 条目。
	n2 := &nodeInfo{ID: "llm-2", Type: "llm", Data: map[string]any{
		"model":           map[string]any{"provider": "openai", "name": "gpt-4o"},
		"prompt_template": []any{map[string]any{"role": "user", "text": "hi"}},
	}}
	c2 := &converter{g: &wfGraph{nodes: map[string]*nodeInfo{"llm-2": n2}}, modelBy: map[string]int{}}
	rendered2 := render.RenderStmt(c2.convertLLM(n2)[0], 0)
	if strings.Contains(rendered2, "temperature") || strings.Contains(rendered2, "max_tokens") {
		t.Errorf("可选键缺省不应出现在 config：\n%s", rendered2)
	}
}
