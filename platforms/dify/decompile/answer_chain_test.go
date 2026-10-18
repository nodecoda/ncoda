package decompile

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Dify answer nodes are stream emitters, not automatic flow terminators.
// A chained answer must lower to the existing ncoda `output(expr)` statement;
// only the terminal answer (no control-flow successor) is `return(expr)`.
// This also preserves the upstream LLM binding consumed by the final answer.
func TestChainedAnswerUsesOutputAndTerminalReturn(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "yaml", "test-answer-order.yml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	nc, err := DecompileYAML(string(data))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}

	if !strings.Contains(nc, `output("--- answer 1 ---\n\nfoo\n")`) {
		t.Fatalf("first chained answer must be an output emitter, got:\n%s", nc)
	}
	if strings.Contains(nc, `return "--- answer 1 ---\n\nfoo\n"`) {
		t.Fatalf("first chained answer must not terminate the flow, got:\n%s", nc)
	}

	// The second answer consumes the upstream LLM's canonical text port.
	re := regexp.MustCompile(`let (v\d+) = llm\(`)
	m := re.FindStringSubmatch(nc)
	if m == nil {
		t.Fatalf("upstream llm binding missing:\n%s", nc)
	}
	if !strings.Contains(nc, "return `--- answer 2 ---") ||
		!strings.Contains(nc, "${"+m[1]+".text}") {
		t.Fatalf("terminal answer must return the final template and reference %s, got:\n%s", m[1], nc)
	}
	if strings.Index(nc, "output(") > strings.Index(nc, "return `--- answer 2 ---") {
		t.Fatalf("chained output must precede terminal return, got:\n%s", nc)
	}
	if strings.Index(nc, "output(") > strings.Index(nc, "return `--- answer 2 ---") {
		t.Fatalf("chained output must precede terminal return, got:\n%s", nc)
	}
}
