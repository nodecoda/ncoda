package token

import "testing"

// `$` 转义拼写的**唯一规则**（发射侧反向折回）：判据要能分辨「恒等返回」这种
// 退化解 —— 若 EscapedSpelling 退化成只搬名字，关键字族的发射会产出**自己解析
// 不了**的 ncoda（实测：裸 `file<code>` 是语法错误）。
func TestEscapedSpellingFoldsKeywords(t *testing.T) {
	cases := []struct{ in, want string }{
		{"code", "$code"}, // 保留字 ⇒ 折回 $ 拼写
		{"txt", "txt"},    // 非保留字 ⇒ 原样
		{"document", "document"},
		{"file", "$file"}, // 类型名也是保留字
		{"type", "$type"},
		{"image", "image"},
	}
	for _, tc := range cases {
		if got := EscapedSpelling(tc.in); got != tc.want {
			t.Errorf("EscapedSpelling(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
	if EscapedSpelling("code") == "code" {
		t.Error("EscapedSpelling 退化成恒等 —— 关键字族的发射会产出不可编译的 ncoda")
	}
}

func TestNameFromSpellingStripsEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"$code", "code"},
		{"code", "code"}, // 幂等
		{"$txt", "txt"},
		{"$", ""}, // 退化输入：剥掉即空（词法层本身拒绝裸 `$`）
		{"", ""},
	}
	for _, tc := range cases {
		if got := NameFromSpelling(tc.in); got != tc.want {
			t.Errorf("NameFromSpelling(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// 两个方向必须互逆**在关键字与非关键字上都成立**（一处规则，两处调用点）。
func TestSpellingRoundTripsBothWays(t *testing.T) {
	for _, name := range append(Keywords(), "code", "txt", "custom") {
		if got := NameFromSpelling(EscapedSpelling(name)); got != name {
			t.Errorf("名字 %q：NameFromSpelling(EscapedSpelling(..)) = %q，不闭合", name, got)
		}
	}
}
