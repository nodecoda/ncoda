package localtools

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

// 判据面：matting.cutout 的声明来自
// libraries/matting.ncoda：
//
//	MattingCutoutResult { file<image> data; MattingCutoutErrorbody errorBody;
//	                      bool isSuccess; string mask; string msg }
//
// 这里只认这 5 个键。多发的键会变成调用方可见的隐式契约——database 那轮
// 已经踩过一次，这里用键集比较把它钉住。

func mattingTestPNG(t *testing.T) (string, []byte) {
	t.Helper()
	const w, h = 32, 32
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{220, 30, 40, 255} // 纯色背景
			if x >= 10 && x < 22 && y >= 10 && y < 22 {
				c = color.RGBA{250, 250, 250, 255} // 主体
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	path := filepath.Join(t.TempDir(), "src.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write test png: %v", err)
	}
	return path, buf.Bytes()
}

func mattingCall(t *testing.T, m *plugin.Manager, args map[string]any) map[string]any {
	t.Helper()
	out, err := m.Call(context.Background(), "matting/cutout", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want map", out)
	}
	return res
}

func mattingKeysOf(t *testing.T, m map[string]any) []string {
	t.Helper()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func getURL(t *testing.T, res map[string]any, key string) string {
	t.Helper()
	s, ok := res[key].(string)
	if !ok {
		t.Fatalf("%s = %#v, want string", key, res[key])
	}
	return s
}

func decodePNGAt(t *testing.T, url string) image.Image {
	t.Helper()
	if !strings.HasPrefix(url, "file://") {
		t.Fatalf("url %q is not a file:// URL", url)
	}
	b, err := os.ReadFile(strings.TrimPrefix(url, "file://"))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode artifact png: %v", err)
	}
	return img
}

func TestMattingCutoutShapeAndDeterminism(t *testing.T) {
	path, _ := mattingTestPNG(t)
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	first := mattingCall(t, m, map[string]any{
		"url": path, "only_mask": float64(0), "output_mode": float64(0),
	})
	second := mattingCall(t, m, map[string]any{
		"url": path, "only_mask": float64(0), "output_mode": float64(0),
	})

	// 形状：只发声明过的 5 个字段；errorBody 只发声明过的 2 个字段。
	if got, want := fmt.Sprint(mattingKeysOf(t, first)), "[data errorBody isSuccess mask msg]"; got != want {
		t.Fatalf("result keys = %s, want %s", got, want)
	}
	eb, ok := first["errorBody"].(map[string]any)
	if !ok {
		t.Fatalf("errorBody = %#v, want map", first["errorBody"])
	}
	if got := fmt.Sprint(mattingKeysOf(t, eb)); got != "[errorCode errorMessage]" {
		t.Fatalf("errorBody keys = %s", got)
	}

	if first["isSuccess"] != true {
		t.Fatalf("isSuccess = %#v (errorBody=%v)", first["isSuccess"], first["errorBody"])
	}
	if u1, u2 := getURL(t, first, "data"), getURL(t, second, "data"); u1 != u2 {
		t.Fatalf("non-deterministic data: %q vs %q", u1, u2)
	}
	if u1, u2 := getURL(t, first, "mask"), getURL(t, second, "mask"); u1 != u2 {
		t.Fatalf("non-deterministic mask: %q vs %q", u1, u2)
	}

	cut := decodePNGAt(t, getURL(t, first, "data"))
	// 背景（角落）透明、主体（中心）不透明 —— 算法真的做了 alpha。
	if _, _, _, a := cut.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("corner alpha = %d, want 0 (background should be cut)", a>>8)
	}
	if _, _, _, a := cut.At(16, 16).RGBA(); a != 0xffff {
		t.Fatalf("center alpha = %d, want 255 (subject should stay)", a>>8)
	}

	mask := decodePNGAt(t, getURL(t, first, "mask"))
	if r, _, _, _ := mask.At(0, 0).RGBA(); r != 0 {
		t.Fatalf("mask corner = %d, want 0", r>>8)
	}
	if r, _, _, _ := mask.At(16, 16).RGBA(); r != 0xffff {
		t.Fatalf("mask center = %d, want 255", r>>8)
	}
}

func TestMattingOnlyMaskLeavesDataEmpty(t *testing.T) {
	path, _ := mattingTestPNG(t)
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := mattingCall(t, m, map[string]any{
		"url": path, "only_mask": float64(1), "output_mode": float64(0),
	})
	if res["isSuccess"] != true {
		t.Fatalf("isSuccess = %#v", res["isSuccess"])
	}
	if got := getURL(t, res, "data"); got != "" {
		t.Fatalf("data = %q, want empty under only_mask=1", got)
	}
	if decodePNGAt(t, getURL(t, res, "mask")) == nil {
		t.Fatal("mask artifact missing")
	}
}

// 负控 1：非图片输入必须变成声明的失败结果，而不是 panic / rc=1。
func TestMattingRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-an-image.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := mattingCall(t, m, map[string]any{"url": path, "only_mask": float64(0), "output_mode": float64(0)})
	if res["isSuccess"] != false {
		t.Fatalf("isSuccess = %#v, want false", res["isSuccess"])
	}
	eb := res["errorBody"].(map[string]any)
	if eb["errorCode"] == "" {
		t.Fatalf("errorCode is empty: %#v", eb)
	}
	if getURL(t, res, "data") != "" || getURL(t, res, "mask") != "" {
		t.Fatalf("failed result carries artifacts: %#v", res)
	}
}

// 负控 2：缺 url / 未定义的 output_mode 不许静默降级。
func TestMattingRejectsMissingInputAndUnknownMode(t *testing.T) {
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := mattingCall(t, m, map[string]any{"only_mask": float64(0), "output_mode": float64(0)})
	if res["isSuccess"] != false {
		t.Fatalf("missing url: isSuccess = %#v, want false", res["isSuccess"])
	}
	if got := res["errorBody"].(map[string]any)["errorCode"]; got != "INVALID_INPUT" {
		t.Fatalf("missing url: errorCode = %#v", got)
	}

	path, _ := mattingTestPNG(t)
	res = mattingCall(t, m, map[string]any{"url": path, "only_mask": float64(0), "output_mode": float64(9)})
	if res["isSuccess"] != false {
		t.Fatalf("unknown mode: isSuccess = %#v, want false", res["isSuccess"])
	}
	if got := res["errorBody"].(map[string]any)["errorCode"]; got != "UNSUPPORTED_OUTPUT_MODE" {
		t.Fatalf("unknown mode: errorCode = %#v", got)
	}
}

// 正向覆盖：file:// 与 data: URL 两条输入形态；map 形态（节点产物）也吃。
func TestMattingInputForms(t *testing.T) {
	path, raw := mattingTestPNG(t)
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
	cases := map[string]any{
		"file:// URL": "file://" + path,
		"data URL":    encoded,
		"object url":  map[string]any{"url": "file://" + path},
	}
	for name, v := range cases {
		res := mattingCall(t, m, map[string]any{"url": v, "only_mask": float64(0), "output_mode": float64(0)})
		if res["isSuccess"] != true {
			t.Fatalf("%s: isSuccess = %#v (errorBody=%v)", name, res["isSuccess"], res["errorBody"])
		}
		if decodePNGAt(t, getURL(t, res, "data")) == nil {
			t.Fatalf("%s: no data artifact", name)
		}
	}
}
