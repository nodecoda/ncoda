package parser

// Pins LANGUAGE-REFERENCE.md §4.9 / §10「转换拼写」：类型关键字转换**只有**
// int(...) / float(...) / file(...)；其余类型关键字作函数名 = 语法错误。
// R-SEM SEM-S10-18 的机器判据（2026-10-13「收编标准」后：标准 §4.9/§10/§11 已补 float）。

import "testing"

func TestCastSpellingKeywordConversionsAccepted(t *testing.T) {
	for _, src := range []string{
		`function main() { let n = int("42"); }`,
		`function main() { let f = float("2.5"); }`,
		`function main() { let u = file(url); }`,
		`function main() { let n = int(float("1.5")); }`,
	} {
		conformOK(t, src)
	}
}

func TestCastSpellingOtherTypeKeywordsRejected(t *testing.T) {
	for _, src := range []string{
		`function main() { let s = string(1); }`,
		`function main() { let b = bool(1); }`,
	} {
		prog, errs := ParseSource(src, "<cast-spelling>")
		if prog != nil && len(errs) == 0 {
			t.Errorf("ParseSource(%q): expected a syntax error for a non-cast type keyword used as a callee, got none", src)
		}
	}
}
