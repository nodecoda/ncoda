package passes

import (
	"strings"
	"testing"
)

func TestLLMUnifiedEnvelopeType(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type Answer = { string answer; int score; };
function main(string prompt) -> string {
  let r = llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]});
  return r.text.answer;
}`
	r := DefaultPipeline().Run(src, "llm-unified.ncoda", nil)
	if r.HasErrors() {
		t.Fatalf("unified envelope rejected: %v", r.Diagnostics)
	}
}

func TestLLMLegacyRecordFieldGetsMigrationDiagnostic(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
type Answer = { string answer; };
function main(string prompt) -> string {
  let r = llm<Answer>("model", {"messages": [{"role":"user", "content":prompt}]});
  return r.answer;
}`
	r := DefaultPipeline().Run(src, "llm-legacy.ncoda", nil)
	if !r.HasErrors() {
		t.Fatal("legacy record-expanded access unexpectedly accepted")
	}
	found := false
	for _, d := range r.Diagnostics {
		if strings.Contains(d.Message, "use '.text.answer'") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing migration guidance: %v", r.Diagnostics)
	}
}
