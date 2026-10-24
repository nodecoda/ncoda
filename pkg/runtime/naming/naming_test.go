package naming

import "testing"

func TestIsPlatformName(t *testing.T) {
	for _, s := range []string{"plugin_7547770968681447458", "7547770968681447458"} {
		if !IsPlatformName(s) {
			t.Errorf("IsPlatformName(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"", "image_generate", "capcut", "local_image_generate", "feishu_docs", "my_lib2",
	} {
		if IsPlatformName(s) {
			t.Errorf("IsPlatformName(%q) = true, want false", s)
		}
	}
}

func TestIsSymbol(t *testing.T) {
	for _, s := range []string{"image_generate.image_generator", "capcut.create_draft", "a.b"} {
		if !IsSymbol(s) {
			t.Errorf("IsSymbol(%q) = false", s)
		}
	}
	for _, s := range []string{"image_generate", "Image.Generate", "image-generate.f", "a.b.c", "plugin_1.f"} {
		if IsSymbol(s) {
			t.Errorf("IsSymbol(%q) = true", s)
		}
	}
}

// 语法作业：物理包名永远不是符号；任何大小写/分隔符越界都被拒绝。
func TestSymbolGrammarIsClosed(t *testing.T) {
	for _, s := range []string{"Library.f", "lib.Func.X", "lib.f-g", "lib..f", ".f", "lib."} {
		if IsSymbol(s) {
			t.Errorf("IsSymbol(%q) = true, want false", s)
		}
	}
}
