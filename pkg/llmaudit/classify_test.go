package llmaudit

import (
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		input    Input
		category string
		reason   string
	}{
		{
			name: "llm decompile failure is unclassified",
			input: Input{
				DecompileErr:   "broken graph",
				RawLLMKnown:    true,
				RawWorkflowLLM: true,
			},
			category: CategoryFailure,
			reason:   "unclassified-llm-decompile-failure",
		},
		{
			name: "non llm decompile failure is positively not applicable",
			input: Input{
				DecompileErr: "broken graph",
				RawLLMKnown:  true,
			},
			category: CategoryNotApplicable,
			reason:   "non-llm-decompile-failure",
		},
		{
			name: "explicit unknown schema",
			input: Input{
				Source:      "type RT = { array<any> timelines; };\nfunction main() { let result = llm<RT>(\"model\", \"prompt\"); return result.text; }\n// UNKNOWN: array element schema missing",
				Status:      "COMPILE_ERROR",
				DiagCode:    "TYPE_MISMATCH",
				DiagMessage: `field "timelines": llm payload type any is not admitted`,
			},
			category: CategoryUnknown,
			reason:   "llm-schema-unknown",
		},
		{
			name: "known corpus contract mismatch",
			input: Input{
				Source:      "type Calendar = { string date1; string date2; };\nfunction main() { let result = llm<Calendar>(\"model\", \"prompt\"); let sink = foreign code javascript(string content = result.text) -> { output: string; } { source `return {};`; }; return sink.output; }",
				Status:      "COMPILE_ERROR",
				DiagCode:    "CODE_INPUT_TYPE",
				DiagMessage: "Code input 'content' expected Type(string), got Type(record, RecordType(name='', fields={'date1': Type(string), 'date2': Type(string)}))",
			},
			category: CategoryPreExisting,
			reason:   "structured-llm-payload-to-string-code-input",
		},
		{
			name: "legacy external structured llm rejection text still classifies (historical audit data)",
			input: Input{
				Source:      "type Answer = { string title; };\nfunction main() { let result = llm<Answer>(\"model\", \"prompt\"); return result.text; }",
				Status:      "COMPILE_ERROR",
				DiagCode:    "ADAPTER_ERROR",
				DiagMessage: "target profile coze-biz-1.0 has no integrated structured generation/validation/retry proof for non-string llm<T> (node 100001)",
			},
			category: CategoryAdapterRejected,
			reason:   "structured-llm-profile-not-proven",
		},
		{
			name: "subworkflow is named non llm gap",
			input: Input{
				Source:      "function main() { return SubWorkflow(); }",
				Status:      "COMPILE_ERROR",
				DiagCode:    "UNDEFINED_VAR",
				DiagMessage: "Undefined variable: 'SubWorkflow'",
			},
			category: CategoryPreExisting,
			reason:   "subworkflow-language-coverage",
		},
		{
			name: "stable llm semantics with global projection diff",
			input: Input{
				Source:       "function main() { let result = llm<string>(\"model\", \"prompt\"); return result.text; }",
				Round2Source: "function main() { let v100001 = llm<string>(\"model\", \"prompt\"); return v100001.text; }",
				Status:       "SUCCEEDED",
				Round2:       "diff",
			},
			category: CategoryProjectionOnly,
			reason:   "llm-semantics-stable",
		},
		{
			name: "legacy access normalized once",
			input: Input{
				Source:       "type Answer = { string title; };\nfunction main() { let result = llm<Answer>(\"model\", \"prompt\"); return result.title; }",
				Round2Source: "type Answer = { string title; };\nfunction main() { let v100001 = llm<Answer>(\"model\", \"prompt\"); return v100001.text.title; }",
				Status:       "SUCCEEDED",
				Round2:       "diff",
			},
			category: CategoryMigrationNormalized,
			reason:   "legacy-llm-access-normalized",
		},
		{
			name: "semantic llm path change is unclassified failure",
			input: Input{
				Source:       "function main() { let result = llm<string>(\"model\", \"prompt\"); return result.text; }",
				Round2Source: "function main() { let v100001 = llm<string>(\"model\", \"prompt\"); return v100001.reasoning_content; }",
				Status:       "SUCCEEDED",
				Round2:       "diff",
			},
			category: CategoryFailure,
			reason:   "unclassified-llm-path-difference",
		},
		{
			name: "downstream projection drops an llm consumer",
			input: Input{
				Source:       "function main() { let result = llm<string>(\"model\", \"prompt\"); let sink = foreign code javascript(string content = result.text) -> { output: string; } { source `return {};`; }; return sink.output; }",
				Round2Source: "function main() { let v100001 = llm<string>(\"model\", \"prompt\"); return null; }",
				Status:       "SUCCEEDED",
				Round2:       "diff",
			},
			category: CategoryPreExisting,
			reason:   "downstream-non-llm-reference-projection-drift",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.input)
			if got.Category != tt.category || got.Reason != tt.reason {
				t.Fatalf("classification = %#v, want category=%q reason=%q", got, tt.category, tt.reason)
			}
			if strings.Contains(got.Reason, "100001") || strings.Contains(got.Detail, "100001") {
				t.Fatalf("classification leaks node id: %#v", got)
			}
		})
	}
}

func TestInspectRawWorkflowLLM(t *testing.T) {
	for _, tc := range []struct {
		name, format, raw string
		want              bool
	}{
		{name: "canvas llm", format: "json", raw: `{"nodes":[{"id":"n","type":"3","data":{}}]}`, want: true},
		{name: "biz llm", format: "yaml", raw: "nodes:\n  - id: n\n    type: llm\n    parameters: {}\n", want: true},
		{name: "schema type three is not a node", format: "json", raw: `{"schema":{"type":3}}`},
		{name: "non llm", format: "yaml", raw: "nodes:\n  - id: n\n    type: code\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := InspectRawWorkflowLLM([]byte(tc.raw), tc.format)
			if err != nil || got != tc.want {
				t.Fatalf("got=%v err=%v, want %v", got, err, tc.want)
			}
		})
	}
}

func TestSummarizeCountsOnlyUnclassifiedReasons(t *testing.T) {
	summary := Summarize([]Classification{
		{Category: CategoryUnknown, Reason: "llm-schema-unknown"},
		{Category: CategoryPreExisting, Reason: "subworkflow-language-coverage"},
		{Category: CategoryAdapterRejected, Reason: "structured-llm-profile-not-proven"},
		{Category: CategoryProjectionOnly, Reason: "llm-semantics-stable"},
		{Category: CategoryFailure, Reason: "known-llm-runtime-failure"},
		{Category: CategoryFailure, Reason: "unclassified-llm-path-difference"},
	})

	if summary.Unclassified != 1 {
		t.Fatalf("unclassified = %d, want 1", summary.Unclassified)
	}
	if summary.Counts[CategoryUnknown] != 1 || summary.Counts[CategoryPreExisting] != 1 || summary.Counts[CategoryAdapterRejected] != 1 || summary.Counts[CategoryProjectionOnly] != 1 || summary.Counts[CategoryFailure] != 2 {
		t.Fatalf("unexpected counts: %#v", summary.Counts)
	}
}
