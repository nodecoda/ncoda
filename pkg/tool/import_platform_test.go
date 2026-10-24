package tool

// Platform-qualified imports: SplitImport splits "qualifier.provider" while
// keeping bare providers untouched. The qualifier is validated by shape, not
// against a platform list — the open kernel does not know which platforms
// exist (ASSET-BOUNDARY D8); adapters reject qualifiers they do not serve at
// build time (UNSUPPORTED_BY_TARGET), never as a language error.

import "testing"

func TestSplitImportBareProvider(t *testing.T) {
	platform, name, err := SplitImport("web_search")
	if err != nil {
		t.Fatalf("SplitImport: %v", err)
	}
	if platform != "" || name != "web_search" {
		t.Fatalf("platform=%q name=%q, want (\"\", web_search)", platform, name)
	}
}

func TestSplitImportPlatformQualified(t *testing.T) {
	cases := []struct {
		provider string
		platform string
		name     string
	}{
		{"coze-biz.web_search", "coze-biz", "web_search"},
		{"acme.google_search", "acme", "google_search"},
		{"coze-studio.feishu_bitable", "coze-studio", "feishu_bitable"},
		// A qualifier the open repo does not serve is still a legal import:
		// whether the target can satisfy it is an adapter decision.
		{"windows.web_search", "windows", "web_search"},
	}
	for _, tc := range cases {
		platform, name, err := SplitImport(tc.provider)
		if err != nil {
			t.Fatalf("SplitImport(%q): %v", tc.provider, err)
		}
		if platform != tc.platform || name != tc.name {
			t.Fatalf("SplitImport(%q) = (%q, %q), want (%q, %q)", tc.provider, platform, name, tc.platform, tc.name)
		}
	}
}

func TestSplitImportRejectsMalformedQualifier(t *testing.T) {
	cases := []string{
		".web_search",     // empty qualifier
		"Acme.web_search", // uppercase qualifier
		"a_b.web_search",  // underscore is not a qualifier character
	}
	for _, provider := range cases {
		if _, _, err := SplitImport(provider); err == nil {
			t.Fatalf("SplitImport(%q): want error for malformed qualifier", provider)
		}
	}
}

func TestSplitImportRejectsEmptyProvider(t *testing.T) {
	if _, _, err := SplitImport("coze-biz."); err == nil {
		t.Fatal("SplitImport: want error for empty provider after qualifier")
	}
}

func TestPlatformOf(t *testing.T) {
	cases := []struct {
		provider string
		want     string
	}{
		{"web_search", ""},
		{"coze-biz.web_search", "coze-biz"},
		{"acme.google_search", "acme"},
		{"windows.web_search", "windows"}, // shape-valid ⇒ a qualifier
		{"a/b.web_search", ""},            // malformed ⇒ no qualifier
	}
	for _, tc := range cases {
		if got := PlatformOf(tc.provider); got != tc.want {
			t.Fatalf("PlatformOf(%q) = %q, want %q", tc.provider, got, tc.want)
		}
	}
}

func TestValidPlatformQualifier(t *testing.T) {
	for _, ok := range []string{"acme", "coze-biz", "coze-studio", "acme2", "x"} {
		if !ValidPlatformQualifier(ok) {
			t.Fatalf("ValidPlatformQualifier(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "-acme", ".acme", "acme.x", "a/b", "ACME", "a_b"} {
		if ValidPlatformQualifier(bad) {
			t.Fatalf("ValidPlatformQualifier(%q) = true, want false", bad)
		}
	}
}
