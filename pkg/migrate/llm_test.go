package migrate

import (
	"strings"
	"testing"
)

const recordPreamble = `@language nodecoda/1
@mode workflow
type Answer = { string answer; string summary; };
`

func rewriteOK(t *testing.T, source string) Result {
	t.Helper()
	got := Rewrite(source, "test.ncoda")
	if len(got.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %#v", got.Diagnostics)
	}
	return got
}

func TestRewriteLLMProvenanceMatrix(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "direct-record",
			body: `function main() -> string { let r = llm<Answer>("m", {}); return r.answer; }`,
			want: `function main() -> string { let r = llm<Answer>("m", {}); return r.text.answer; }`,
		},
		{
			name: "alias",
			body: `function main() -> string { let r = llm<Answer>("m", {}); let alias = r; return alias.answer; }`,
			want: `function main() -> string { let r = llm<Answer>("m", {}); let alias = r; return alias.text.answer; }`,
		},
		{
			name: "nested-expression-right-to-left",
			body: `function main() -> string { let r = llm<Answer>("m", {}); return r.answer + r.summary; }`,
			want: `function main() -> string { let r = llm<Answer>("m", {}); return r.text.answer + r.text.summary; }`,
		},
		{
			name: "template",
			body: "function main() -> string { let r = llm<Answer>(\"m\", {}); return `answer=${r.answer}`; }",
			want: "function main() -> string { let r = llm<Answer>(\"m\", {}); return `answer=${r.text.answer}`; }",
		},
		{
			name: "template-multiple-non-ascii",
			body: "function main() -> string { let r = llm<Answer>(\"m\", {}); return `结果：${r.answer}/${r.summary}`; }",
			want: "function main() -> string { let r = llm<Answer>(\"m\", {}); return `结果：${r.text.answer}/${r.text.summary}`; }",
		},
		{
			name: "content-alias",
			body: `function main() -> string { let r = llm("m", {}); return r.content; }`,
			want: `function main() -> string { let r = llm("m", {}); return r.text; }`,
		},
		{
			name: "generated-decompiler-source",
			body: `const MODEL_1 = "coze:1706077826"; function main() -> string { let v100002 = llm<Answer>(MODEL_1, {}); return v100002.answer; }`,
			want: `const MODEL_1 = "coze:1706077826"; function main() -> string { let v100002 = llm<Answer>(MODEL_1, {}); return v100002.text.answer; }`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteOK(t, recordPreamble+tc.body)
			if got.Source != recordPreamble+tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got.Source, recordPreamble+tc.want)
			}
			second := rewriteOK(t, got.Source)
			if second.Changed || second.Source != got.Source {
				t.Fatalf("second pass changed source: %#v", second)
			}
		})
	}
}

func TestRewriteBatchInnerOutputRequiresExplicitCallOrigin(t *testing.T) {
	for _, name := range []string{"v100002_inner", "renamed_batch_value"} {
		t.Run(name, func(t *testing.T) {
			body := `function main() -> string { let ` + name + ` = llm<Answer>("m", {}); return ` + name + `.$output; }`
			source := recordPreamble + body
			untagged := rewriteOK(t, source)
			if untagged.Changed || untagged.Source != source {
				t.Fatalf("untagged source changed:\n%s", untagged.Source)
			}

			callOffset := strings.Index(source, `llm<Answer>`)
			tagged := Rewrite(source, "test.ncoda", OriginTag{CallOffset: callOffset, Kind: OriginBatchInnerLLM})
			if len(tagged.Diagnostics) != 0 {
				t.Fatalf("diagnostics: %#v", tagged.Diagnostics)
			}
			want := strings.Replace(source, name+`.$output`, name+`.text`, 1)
			if !tagged.Changed || tagged.Source != want {
				t.Fatalf("tagged source:\n%s\nwant:\n%s", tagged.Source, want)
			}
		})
	}
}

func TestRewriteBatchInnerMarkerRequiresLexicalAdjacency(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "inside-string",
			body: `function main() -> string { let marker = "` + BatchInnerSourceMarker + `"; let r = llm<Answer>("m", {}); return r.$output; }`,
		},
		{
			name: "inside-line-comment",
			body: "function main() -> string { // " + BatchInnerSourceMarker + "\n let r = llm<Answer>(\"m\", {}); return r.$output; }",
		},
		{
			name: "detached-from-call",
			body: `function main() -> string { ` + BatchInnerSourceMarker + ` let x = 1; let r = llm<Answer>("m", {}); return r.$output; }`,
		},
		{
			name: "followed-by-another-comment",
			body: `function main() -> string { ` + BatchInnerSourceMarker + ` /* unrelated */ let r = llm<Answer>("m", {}); return r.$output; }`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := recordPreamble + tc.body
			got := rewriteOK(t, source)
			if got.Changed || got.Source != source {
				t.Fatalf("marker-looking text retargeted a later call:\n%s", got.Source)
			}
		})
	}
}

func TestRewriteBatchInnerMarkerAcceptsOnlyCompleteAdjacentCall(t *testing.T) {
	source := recordPreamble + `function main() -> string {
    let r = ` + BatchInnerSourceMarker + `
        llm<Answer>("m", {});
    return r.$output;
}`
	got := rewriteOK(t, source)
	want := strings.Replace(source, "r.$output", "r.text", 1)
	if got.Source != want {
		t.Fatalf("adjacent marker was not applied:\n%s\nwant:\n%s", got.Source, want)
	}
}

func TestRewriteBranchHoistedSameProducer(t *testing.T) {
	source := recordPreamble + `function main(bool choose) -> string {
    let base = llm<Answer>("m", {});
    var result;
    if (choose) { result = base; } else { result = base; }
    return result.answer;
}`
	got := rewriteOK(t, source)
	if !strings.Contains(got.Source, "return result.text.answer;") {
		t.Fatalf("same-producer merge not rewritten:\n%s", got.Source)
	}
}

func TestRewriteBranchHoistedMultipleProducersIsAmbiguous(t *testing.T) {
	source := recordPreamble + `function main(bool choose) -> string {
    let first = llm<Answer>("m", {});
    let second = llm<Answer>("m", {});
    var result;
    if (choose) { result = first; } else { result = second; }
    return result.answer;
}`
	got := Rewrite(source, "test.ncoda")
	if got.Changed || got.Source != source {
		t.Fatalf("ambiguous source changed: %#v", got)
	}
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != CodeAmbiguousProducer {
		t.Fatalf("diagnostics = %#v, want %s", got.Diagnostics, CodeAmbiguousProducer)
	}
}

func TestRewriteDynamicIndexIsDiagnostic(t *testing.T) {
	source := recordPreamble + `function main(string key) -> string { let r = llm<Answer>("m", {}); return r[key]; }`
	got := Rewrite(source, "test.ncoda")
	if got.Changed || got.Source != source {
		t.Fatalf("dynamic index source changed: %#v", got)
	}
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != CodeDynamicIndex {
		t.Fatalf("diagnostics = %#v, want %s", got.Diagnostics, CodeDynamicIndex)
	}
}

func TestRewriteStaticStringIndex(t *testing.T) {
	source := recordPreamble + `function main() -> string { let r = llm<Answer>("m", {}); return r["answer"]; }`
	got := rewriteOK(t, source)
	want := strings.Replace(source, `r["answer"]`, `r.text["answer"]`, 1)
	if got.Source != want {
		t.Fatalf("got %q want %q", got.Source, want)
	}
}

func TestRewriteDoesNotTouchNonLLMFields(t *testing.T) {
	source := `@language nodecoda/1
type UserRecord = { string text; string $output; string content; };
function user_record() -> UserRecord { return {text: "t", $output: "o", content: "c"}; }
function main() -> string {
    let user = user_record();
    let runner = foreign code python3() -> { text: string; $output: string; content: string; } {
        source ` + "`def main(): return {\"text\": \"t\", \"output\": \"o\", \"content\": \"c\"}`" + `;
    };
    return user.content + runner.$output + runner.text;
}`
	got := rewriteOK(t, source)
	if got.Changed || got.Source != source {
		t.Fatalf("non-LLM fields changed:\n%s", got.Source)
	}
}

func TestRewriteRequiresLegacyRecordOrBatchOrigin(t *testing.T) {
	tests := []string{
		`function main() -> string { let r = llm("m", {}); return r.answer; }`,
		`function main() -> string { let r = llm<int>("m", {}); return r.output; }`,
		`function main() -> string { let r = llm<Answer>("m", {}); return r.$output; }`,
	}
	for _, body := range tests {
		source := recordPreamble + body
		got := rewriteOK(t, source)
		if got.Changed || got.Source != source {
			t.Fatalf("non-legacy shape changed:\n%s", got.Source)
		}
	}
}

func TestRewriteUserFunctionResultIsNotBuiltinLLM(t *testing.T) {
	source := `@language nodecoda/1
type Answer = { string answer; };
function model_call(string model, map<string, any> config) -> Answer { return {answer: "x"}; }
function main() -> string { let r = model_call("m", {}); return r.answer; }`
	got := rewriteOK(t, source)
	if got.Changed || got.Source != source {
		t.Fatalf("shadowed llm changed:\n%s", got.Source)
	}
}

func TestRewriteParseDiagnosticHasStableLocation(t *testing.T) {
	got := Rewrite("function main( {", "broken.ncoda")
	if len(got.Diagnostics) == 0 {
		t.Fatal("expected parse diagnostic")
	}
	d := got.Diagnostics[0]
	if d.Code != CodeParse || d.Severity != SeverityError || d.Line < 1 || d.Column < 1 {
		t.Fatalf("diagnostic = %#v", d)
	}
}
