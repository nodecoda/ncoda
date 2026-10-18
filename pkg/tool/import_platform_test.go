package tool

// Platform-qualified imports (docs/PLATFORM-MECHANISM.md §4-5): SplitImport
// splits "platform.provider" while keeping bare providers untouched; unknown
// qualifiers are a hard error so bindings never silently misroute.

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
		{"dify.google_search", "dify", "google_search"},
		{"coze-studio.feishu_bitable", "coze-studio", "feishu_bitable"},
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

func TestSplitImportRejectsUnknownPlatform(t *testing.T) {
	_, _, err := SplitImport("windows.web_search")
	if err == nil {
		t.Fatal("SplitImport: want error for unknown platform")
	}
	pe, ok := err.(*ImportPlatformError)
	if !ok {
		t.Fatalf("error type = %T, want *ImportPlatformError", err)
	}
	if pe.Platform != "windows" || pe.Provider != "windows.web_search" {
		t.Fatalf("ImportPlatformError = %#v", pe)
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
		{"dify.google_search", "dify"},
		{"windows.web_search", ""}, // unknown qualifier is not a platform
	}
	for _, tc := range cases {
		if got := PlatformOf(tc.provider); got != tc.want {
			t.Fatalf("PlatformOf(%q) = %q, want %q", tc.provider, got, tc.want)
		}
	}
}

func TestIsPlatformName(t *testing.T) {
	for _, p := range PlatformNames {
		if !IsPlatformName(p) {
			t.Fatalf("IsPlatformName(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"windows", "coze", ""} {
		if IsPlatformName(p) {
			t.Fatalf("IsPlatformName(%q) = true, want false", p)
		}
	}
}
