package lexer

import "testing"

func TestSmokeRealExamples(t *testing.T) {
	samples := []string{
		"../../examples/nodecoda/00_minimal_start_llm_end.ncoda",
		"../../examples/nodecoda/03_conditional_branch.ncoda",
		"../../examples/nodecoda/05_http_request.ncoda",
		"../../examples/nodecoda/09_iteration.ncoda",
		"../../examples/nodecoda/12_loop_with_break.ncoda",
		"../../examples/nodecoda/19_http_request_json_tool.ncoda",
	}
	for _, path := range samples {
		src := readFile(t, path)
		lx := New(src, path)
		lx.Tokenize()
		if len(lx.Errors()) > 0 {
			t.Errorf("%s: %d lexer errors (first: %v)", path, len(lx.Errors()), lx.Errors()[0])
		}
	}
}
