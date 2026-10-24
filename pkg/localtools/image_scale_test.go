package localtools

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

// 判据面：image_scale.resize 的声明来自 image_scale.ncoda：
//
//	ImageScaleResizeResult { file<image> data; string msg; array<...> outputList }
//
// 只认这 3 个键；outputList 元素只认 {data,msg}。

func scaleTestPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(10 + x*5), uint8(20 + y*7), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "src.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func scaleCall(t *testing.T, m *plugin.Manager, args map[string]any) map[string]any {
	t.Helper()
	out, err := m.Call(context.Background(), "image_scale/resize", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want map", out)
	}
	return res
}

func TestImageScaleShrinksLongestSideAndIsDeterministic(t *testing.T) {
	path := scaleTestPNG(t, 40, 20)
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	a := scaleCall(t, m, map[string]any{"image_url": path, "max_length": float64(20)})
	b := scaleCall(t, m, map[string]any{"image_url": path, "max_length": float64(20)})

	if got, want := fmt.Sprint(mattingKeysOf(t, a)), "[data msg outputList]"; got != want {
		t.Fatalf("result keys = %s, want %s", got, want)
	}
	list, ok := a["outputList"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("outputList = %#v, want one element", a["outputList"])
	}
	elem := list[0].(map[string]any)
	if got := fmt.Sprint(mattingKeysOf(t, elem)); got != "[data msg]" {
		t.Fatalf("outputList[0] keys = %s", got)
	}
	if getURL(t, a, "data") != getURL(t, b, "data") {
		t.Fatalf("non-deterministic: %q vs %q", a["data"], b["data"])
	}
	if elem["data"] != a["data"] {
		t.Fatalf("outputList[0].data = %#v, want %#v", elem["data"], a["data"])
	}

	img := decodePNGAt(t, getURL(t, a, "data"))
	if got := img.Bounds(); got.Dx() != 20 || got.Dy() != 10 {
		t.Fatalf("bounds = %v, want 20x10 (aspect preserved)", got)
	}
}

func TestImageScaleNeverUpscales(t *testing.T) {
	path := scaleTestPNG(t, 10, 10)
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := scaleCall(t, m, map[string]any{"image_url": path, "max_length": float64(100)})
	if res["data"] == "" {
		t.Fatalf("unexpected failure: %v", res["msg"])
	}
	if got := decodePNGAt(t, getURL(t, res, "data")).Bounds(); got.Dx() != 10 || got.Dy() != 10 {
		t.Fatalf("bounds = %v, want 10x10 (max_length is an upper bound)", got)
	}
}

// 负控：未定义 mode / 非法 max_length / 非图片输入都必须可见地失败
// （data 空 + msg 非空），且不返回 Go error。
func TestImageScaleRejectsUndefinedInputs(t *testing.T) {
	path := scaleTestPNG(t, 8, 8)
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cases := []map[string]any{
		{"image_url": path, "max_length": float64(8), "mode": "cover"},
		{"image_url": path, "max_length": float64(0)},
		{"image_url": ""},
	}
	for i, args := range cases {
		res := scaleCall(t, m, args)
		if res["data"] != "" {
			t.Fatalf("case %d: data = %#v, want empty", i, res["data"])
		}
		if s, _ := res["msg"].(string); s == "" {
			t.Fatalf("case %d: msg is empty; failure must be visible", i)
		}
		if list, _ := res["outputList"].([]any); len(list) != 0 {
			t.Fatalf("case %d: outputList = %#v, want empty", i, res["outputList"])
		}
	}
}
