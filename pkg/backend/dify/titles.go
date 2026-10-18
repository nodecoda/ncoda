package dify

import (
	"fmt"
	"strings"
)

// DefaultTitles maps Dify node types onto their i18n titles (ported from
// context.py:186-206; shared by the legacy lowering and the IR render).
var DefaultTitles = map[string]string{
	"start":               "User Input",
	"end":                 "Output",
	"answer":              "Answer",
	"llm":                 "LLM",
	"knowledge-retrieval": "Knowledge Retrieval",
	"if-else":             "IF/ELSE",
	"code":                "Code",
	"template-transform":  "Template",
	"http-request":        "HTTP Request",
	"variable-aggregator": "Variable Aggregator",
	"tool":                "Tool",
	"iteration":           "Iteration",
	"iteration-start":     "Iteration Start",
	"loop":                "Loop",
	"loop-start":          "Loop Start",
	"loop-end":            "Exit Loop",
	"parameter-extractor": "Parameter Extractor",
	"question-classifier": "Question Classifier",
	"human-input":         "Human Input",
}

// RenderFormContent mirrors _render_form_content
// (human_input_lowerer.py:214-220): every field placeholder is placed where
// the Dify Console renders its live control.
func RenderFormContent(content string, fieldNames []string) string {
	blocks := make([]string, 0, len(fieldNames)+1)
	blocks = append(blocks, content)
	for _, name := range fieldNames {
		blocks = append(blocks, fmt.Sprintf("**%s**\n\n{{#$output.%s#}}", name, name))
	}
	return strings.Join(blocks, "\n\n")
}
