package passes_test

// Full-frontend fuzz: DefaultPipeline().Run drives lexer → parser →
// desugar/normalize → binding → all semantic passes. It must yield a result
// (with diagnostics) or panic-free behavior on any input. Run:
//
//	go test ./internal/passes -fuzz=FuzzPipeline -fuzztime=60s

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/passes"
)

func FuzzPipeline(f *testing.F) {
	seeds := []string{
		"",
		"@language nodecoda/1\n@mode workflow\nfunction main() {\n    return \"hi\";\n}\n",
		"@mode advanced-chat\n@conversation string name\nfunction main(string q) {\n    output(\"answer\", q);\n}\n",
		"function f(int a) -> int { return a; }\nfunction main() { let x = f(1); return x; }",
		"type T = { string a; int b; }\nfunction main() -> T { let t = { a: \"x\", b: 1 }; return t; }",
		"function main() { let x = foreign code python3( int v = 1 ) -> { r: int; } { source `def main(v): return {\"r\": v}`; }; return x.r; }",
		"function main() { let m = { \"k\": [1, 2, 3] }; return m.k; }",
		"parallel { branch { let x = 1; } branch { let y = 2; } }",
		"attempt { let r = http(\"GET\", \"https://example.com\"); } recover { output(\"e\", r); }",
		"@conversation string name\nfunction main() { output(\"n\", name); }",
		"request_input { title \"Q\" fields { name: string } }\nfunction main() { let v = request_input(\"Q\"); return v; }",
		"function main() {",
		"@",
		"let x = ;",
		"type T = {",
		"function main() { for (let i = 0; i < n; i += 1) { } }",
		"function main() { while (x) { break; } }",
		// ── compound assignment / deepCopyNode surface ──
		"function main() { let x = 1; x += 1; x -= 2; x *= 3; x /= 4; return x; }",
		"function main() { let m = { \"a\": 1 }; m.a += 1; m[\"b\"] -= 2; return m; }",
		"function main() { let a = [1, 2]; a[0] *= 3; a[1] /= 2; return a; }",
		"function main() { let s = \"x\"; s += \"y\"; return s; }",
		"function main() { let t = { a: { b: { c: 1 } } }; t.a.b.c += 1; }",
		// ── while / loop state (valid, invalid, nested, compound in body) ──
		"function main() { let x = 0; while (x < 3) { x = x + 1; } return x; }",
		"function main() { let x = 0; while (x < 3) { x += 1; } return x; }",
		"function main() { let x = 0; while (x < 2) { let y = 0; while (y < 2) { y = y + 1; x = x + y; } } }",
		"function main() { while (true) { let y = 0; while (y < 1) { y += 1; } } }",
		"function main() { let x = 1; while (x > 0) { x = x - 1; } return x; }",
		// ── hostile bytes / unicode / deep nesting / long idents ──
		"function main() { let s = \"\\x00\\x01\\xff\\xfe\\ud800\\udc00\\x7f\"; return s; }",
		"function main() { let s = \"中文测试日本語한국어🚀emoji😀\"; return s; }",
		"function main() { let x = ((((((((((((((((((((1))))))))))))))))))))); }",
		"function main() { let x = 99999999999999999999999999999999999999; }",
		"function main() { let f = 1.7976931348623157e308; let g = 5e-324; }",
		"function main() { let s = \"\\x00\" + \"\\xff\"; return s; }",
		"@mode workflow\n@conversation string q\nfunction main(string q) { output(\"a\", q); }",
		"@mode workflow\nfunction main() { let x = 0; while (x < 1000000) { x += 1; } return x; }",
		"enum E { a }\nfunction main() -> E { return E.b; }",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		res := passes.DefaultPipeline().Run(string(input), "fuzz.ncoda", nil)
		_ = res // must not panic; diagnostics may be empty or full
	})
}
