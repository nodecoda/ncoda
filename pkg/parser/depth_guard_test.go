package parser_test

// P4-4 depth-guard parity regression: the Go parser must reject deep
// recursion with the same ADAPTER_ERROR boundary as the Python reference
// (RecursionError catch). Measured on 2026-08-16 against the Python oracle:
//
//	parens:  88 accepted / 89 rejected  (dp_88 / dp_89)
//	calls:   88 accepted / 89 rejected  (deep_calls_88 / deep_calls_89)
//	if:     121 accepted / 122 rejected (ifp_121 / ifp_122)
//
// These tests lock the boundaries so a future refactor cannot silently widen
// the accepted depth (Go accepting what Python rejects breaks parity).

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/parser"
)

func hasAdapterError(errs []*parser.ParseError) bool {
	for _, e := range errs {
		if e.AdapterError {
			return true
		}
	}
	return false
}

func parseAdapterStatus(t *testing.T, src string) (ok bool, adapter bool) {
	t.Helper()
	prog, errs := parser.ParseSource(src, "<test>")
	if prog == nil {
		if len(errs) == 0 {
			t.Fatal("ParseSource returned nil program with no errors")
		}
		return false, hasAdapterError(errs)
	}
	if len(errs) > 0 {
		return false, hasAdapterError(errs)
	}
	return true, false
}

func TestDepthGuardParensBoundary(t *testing.T) {
	for _, tc := range []struct {
		n           int
		wantAdapter bool
	}{
		{80, false}, {87, false}, {88, false}, {89, true}, {90, true}, {200, true},
	} {
		src := "@mode workflow\nfunction main() -> int { return " + strings.Repeat("(", tc.n) + "1" + strings.Repeat(")", tc.n) + "; }\n"
		_, adapter := parseAdapterStatus(t, src)
		if adapter != tc.wantAdapter {
			t.Errorf("parens depth %d: adapter=%v, want %v", tc.n, adapter, tc.wantAdapter)
		}
	}
}

func TestDepthGuardCallBoundary(t *testing.T) {
	for _, tc := range []struct {
		n           int
		wantAdapter bool
	}{
		{80, false}, {87, false}, {88, false}, {89, true}, {90, true}, {200, true},
	} {
		inner := "1"
		for i := 0; i < tc.n; i++ {
			inner = "f(" + inner + ")"
		}
		src := "@mode workflow\nfunction main() -> int { let x = " + inner + "; return x; }\n"
		_, adapter := parseAdapterStatus(t, src)
		if adapter != tc.wantAdapter {
			t.Errorf("call depth %d: adapter=%v, want %v", tc.n, adapter, tc.wantAdapter)
		}
	}
}

func TestDepthGuardIfBoundary(t *testing.T) {
	build := func(n int) string {
		var b strings.Builder
		b.WriteString("@mode workflow\nfunction main() -> int {\n")
		for i := 0; i < n; i++ {
			b.WriteString(strings.Repeat("    ", i+1) + "if (true) {\n")
		}
		b.WriteString(strings.Repeat("    ", n+1) + "return 1;\n")
		for i := n - 1; i >= 0; i-- {
			b.WriteString(strings.Repeat("    ", i+1) + "}\n")
		}
		b.WriteString("}\n")
		return b.String()
	}
	for _, tc := range []struct {
		n           int
		wantAdapter bool
	}{
		{100, false}, {120, false}, {121, false}, {122, true}, {123, true}, {150, true},
	} {
		_, adapter := parseAdapterStatus(t, build(tc.n))
		if adapter != tc.wantAdapter {
			t.Errorf("if depth %d: adapter=%v, want %v", tc.n, adapter, tc.wantAdapter)
		}
	}
}
