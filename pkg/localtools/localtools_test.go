package localtools

import (
	"context"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

// 语言层符号（image_generate/generate、image_generate_fast/image_generator）
// 必须经绑定层接到本地实现；平台物理 id 必须留给 adapter lane（L1-AC7）。
func TestRegisterBindsPlatformNameToSymbol(t *testing.T) {
	m := plugin.New()
	binds, err := Register(m, Options{ArtifactDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(binds) == 0 {
		t.Fatal("no bindings registered")
	}
	const platformAlias = "plugin_7547770968681447458/image_generator"
	if _, ok := m.Lookup(platformAlias); ok {
		t.Fatalf("P0 register must not resolve platform alias %q; names=%v", platformAlias, m.Names())
	}
	if _, ok := m.Lookup("image_generate_fast/image_generator"); !ok {
		t.Fatalf("language alias lookup failed; names=%v", m.Names())
	}
	// L1' 语言层符号：源码写 image_generate.generate(...)，运行时按库/函数定名。
	if _, ok := m.Lookup("image_generate/generate"); !ok {
		t.Fatalf("language-level symbol lookup failed; names=%v", m.Names())
	}
	if _, ok := m.Lookup("image_generate.generate"); !ok {
		t.Fatalf("capability symbol lookup failed; names=%v", m.Names())
	}
}

func TestImageGeneratorWritesDeterministicPNG(t *testing.T) {
	dir := t.TempDir()
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: dir}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	args := map[string]any{"prompt": "艺术展览", "width": float64(64), "height": float64(32)}
	first, err := m.Call(context.Background(), "image_generate/generate", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	second, err := m.Call(context.Background(), "image_generate_fast/image_generator", args)
	if err != nil {
		t.Fatalf("Call(alias): %v", err)
	}
	u1 := first.(map[string]any)["data"].(string)
	u2 := second.(map[string]any)["data"].(string)
	if u1 != u2 {
		t.Fatalf("non-deterministic: %q vs %q", u1, u2)
	}
	path := strings.TrimPrefix(u1, "file://")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 32 {
		t.Fatalf("bounds = %v, want 64x32", b)
	}
}

// 语料用声明的参数形态调用：generate({inputs:{output}}, {model_setting:{custom_ratio}},
// {prompt_template:{prompt:"{{output}}"}})。实现必须按契约取值并只发声明过的字段
// （ImageGenerateGenerateResult { file<image> data; string msg }）。
func TestImageGeneratorHonoursDeclaredSignature(t *testing.T) {
	dir := t.TempDir()
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: dir}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	args := map[string]any{
		"inputs":          map[string]any{"output": "艺术展览"},
		"model_setting":   map[string]any{"custom_ratio": map[string]any{"width": float64(96), "height": float64(48)}},
		"prompt_template": map[string]any{"prompt": "{{output}} 海报", "negative_prompt": ""},
	}
	got, err := m.Call(context.Background(), "image_generate/generate", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", got)
	}
	for k := range obj {
		if k != "data" && k != "msg" {
			t.Errorf("undeclared result field %q (declared: data, msg)", k)
		}
	}
	url, _ := obj["data"].(string)
	if !strings.HasPrefix(url, "file://") {
		t.Fatalf("data = %v, want file:// URL", obj["data"])
	}
	f, err := os.Open(strings.TrimPrefix(url, "file://"))
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 96 || b.Dy() != 48 {
		t.Fatalf("bounds = %v, want 96x48 from custom_ratio", b)
	}
}
