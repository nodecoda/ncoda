package importns

import (
	"strings"
	"testing"
)

// providerNameRE 的契约测试：裸 provider 名必须是小写 slug（可含 . -），
// 不允许路径分隔符/前导点/大写——裸名会被拼进规范工具根下的文件路径
// （capabilities-ncoda/<provider>.ncoda），路径形态的名字=路径穿越。
func TestSplitRejectsPathShapedProvider(t *testing.T) {
	cases := []string{
		"../victimfile", // 穿越出注册表目录（M1 实测向量）
		"a/b",           // 子目录
		"a\\b",          // Windows 分隔符
		".hidden",       // 前导点：与平台限定词分隔符冲突
		"..",            // 双点
		"coze-biz..x",   // 限定词后空名被 strings.Cut 吃掉？→ 空名分支
		"a b",           // 空格
		"UPPER",         // 大写不在 slug 字符集
	}
	for _, provider := range cases {
		if _, name, err := Split(provider); err == nil {
			t.Errorf("Split(%q) = name %q, want error", provider, name)
		}
	}
}

func TestSplitRejectsQualifiedPathShapedProvider(t *testing.T) {
	// 平台限定词合法，但裸名含路径形态 —— 必须报错而不是放行到
	// filepath.Join（capability_header.go 的 <root>/capabilities-ncoda/<name>.ncoda）。
	cases := []string{
		"coze-biz../victimfile",
		"coze-biz.a/b",
		"coze-biz..hidden",
		"dify.langgenius/perplexity/perplexity", // dify 物理 provider_id 未规范化前
	}
	for _, provider := range cases {
		if _, name, err := Split(provider); err == nil {
			t.Errorf("Split(%q) = name %q, want error", provider, name)
		} else if !strings.Contains(err.Error(), "invalid import provider") {
			t.Errorf("Split(%q) error = %v, want invalid-provider error", provider, err)
		}
	}
}

func TestSplitAcceptsValidProviders(t *testing.T) {
	cases := []struct{ provider, platform, name string }{
		{"web_search", "", "web_search"},
		{"coze-biz.web_search", "coze-biz", "web_search"},
		{"coze-studio.tts", "coze-studio", "tts"},
		{"dify.time", "dify", "time"},
		{"dify.langgenius_perplexity_perplexity", "dify", "langgenius_perplexity_perplexity"},
	}
	for _, c := range cases {
		platform, name, err := Split(c.provider)
		if err != nil {
			t.Errorf("Split(%q) unexpected error: %v", c.provider, err)
			continue
		}
		if platform != c.platform || name != c.name {
			t.Errorf("Split(%q) = (%q, %q), want (%q, %q)", c.provider, platform, name, c.platform, c.name)
		}
	}
}

// The kernel validates qualifier *shape* only: an import may name a platform
// the open distribution does not ship, because target support is an adapter
// question (ASSET-BOUNDARY D8) and must not surface as a language error.
func TestSplitAcceptsShapeValidQualifier(t *testing.T) {
	cases := []struct{ provider, platform, name string }{
		{"acme.web_search", "acme", "web_search"},
		{"acme-2.tts", "acme-2", "tts"},
	}
	for _, c := range cases {
		platform, name, err := Split(c.provider)
		if err != nil {
			t.Fatalf("Split(%q): %v", c.provider, err)
		}
		if platform != c.platform || name != c.name {
			t.Fatalf("Split(%q) = (%q, %q), want (%q, %q)", c.provider, platform, name, c.platform, c.name)
		}
	}
}

func TestSplitRejectsMalformedQualifier(t *testing.T) {
	cases := []string{".web_search", "Acme.web_search", "a_b.web_search", "-acme.web_search"}
	for _, provider := range cases {
		if _, _, err := Split(provider); err == nil {
			t.Fatalf("Split(%q): want malformed-qualifier error", provider)
		}
	}
}

func TestValidProviderName(t *testing.T) {
	for _, ok := range []string{"tts", "web_search", "a.b", "a-b", "x1"} {
		if !ValidProviderName(ok) {
			t.Errorf("ValidProviderName(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "a/b", "..", ".x", "A", "a b", "a\nb"} {
		if ValidProviderName(bad) {
			t.Errorf("ValidProviderName(%q) = true, want false", bad)
		}
	}
}
