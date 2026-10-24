// image_scale.go — `image_scale` 库的本地实现（确定性缩放）。
//
// 声明（唯一权威）：libraries/image_scale.ncoda
//
//	resize(string image_url, int max_length, string mode) -> ImageScaleResizeResult
//	ImageScaleResizeResult { file<image> data; string msg; array<...> outputList }
//	outputList 元素 { file<image> data; string msg }
//
// 语义（本项目定义；语料只用到 max_length，mode 未出现）：
//
//	max_length  **最长边**的上限。只在超限时缩小，**不放大**——"max" 是上界，
//	            不是目标尺寸；放大只会浪费字节，不会多出信息。
//	mode        "" / "fit" = 等比缩放到最长边 = max_length。其他值显式拒绝
//	            （data 为空 + msg 说明），不猜平台语义。
//	输出        PNG（保留 alpha）。文件名含输入哈希 + 目标尺寸 + 算法版本，
//	            同输入同参数 ⇒ 同路径。
//
// 采样：本项目自己实现的双线性（premultiplied alpha），因为离线环境不引入
// 新依赖。算法身份写在 imageScaleAlgorithmVersion 里：改公式必须改它。
//
// 失败形态：头文件**没有** isSuccess/errorBody，所以失败只能靠 `data == ""`
// 表达，原因放 `msg`。这里不返回 Go error（那会让归档类别落到 other），
// 也不假装成功。
package localtools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"math"
	"path/filepath"
	"strings"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

const imageScaleAlgorithmVersion = "bilinear-premult-v1"

type imageScaleTool struct{ dir string }

func registerImageScale(m *plugin.Manager, dir string) error {
	t := &imageScaleTool{dir: dir}
	p := &plugin.Plugin{
		ID:      "local_image_scale",
		Version: "0.1.0",
		Type:    plugin.PluginCallable,
		Tools: []plugin.Tool{{
			Name:         "resize",
			Desc:         "本地确定性缩放：等比缩到最长边≤max_length（不放大），双线性",
			Capabilities: []string{"image_scale.resize"},
			Call:         t.resize,
		}},
	}
	return m.Register(p)
}

func (t *imageScaleTool) resize(_ context.Context, args map[string]any) (any, error) {
	raw := unwrapValue(args["image_url"])
	maxLen := intArg(args, "max_length", 0)
	mode := strings.TrimSpace(stringArg(args, "mode"))

	if mode != "" && !strings.EqualFold(mode, "fit") {
		return imageScaleFailure(fmt.Sprintf("mode=%q is not defined by this implementation (want \"\" or \"fit\")", mode)), nil
	}
	if maxLen <= 0 {
		return imageScaleFailure(fmt.Sprintf("max_length=%d is invalid (want > 0)", maxLen)), nil
	}

	data, err := readImageInput(raw)
	if err != nil {
		return imageScaleFailure(err.Error()), nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return imageScaleFailure(fmt.Sprintf("input is not a decodable image (png/jpeg/gif): %v", err)), nil
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return imageScaleFailure("image has empty bounds"), nil
	}

	w, h := scaledSize(b.Dx(), b.Dy(), maxLen)
	var out image.Image
	if w == b.Dx() && h == b.Dy() {
		out = copyRGBA(src) // 不放大：尺寸不变也要落一个可复算的 PNG
	} else {
		out = resizeBilinear(src, w, h)
	}

	sum := sha256.Sum256(append([]byte(fmt.Sprintf("%s|%d|%s|", imageScaleAlgorithmVersion, maxLen, mode)), data...))
	name := fmt.Sprintf("scaled-%s-%dx%d.png", hex.EncodeToString(sum[:8]), w, h)
	path := filepath.Join(t.dir, name)
	if err := writePNGAtomic(path, out); err != nil {
		return imageScaleFailure("artifact write failed: " + err.Error()), nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	url := "file://" + abs
	msg := fmt.Sprintf("local deterministic bilinear resize: %dx%d -> %dx%d (max_length=%d)", b.Dx(), b.Dy(), w, h, maxLen)
	return map[string]any{
		"data":       url,
		"msg":        msg,
		"outputList": []any{map[string]any{"data": url, "msg": msg}},
	}, nil
}

// imageScaleFailure：声明形状里没有 isSuccess，失败 = data 空 + msg 说原因。
func imageScaleFailure(message string) map[string]any {
	return map[string]any{
		"data":       "",
		"msg":        message,
		"outputList": []any{},
	}
}

// scaledSize 返回等比缩放后的尺寸；最长边不超过 maxLen，且从不放大。
func scaledSize(w, h, maxLen int) (int, int) {
	longest := w
	if h > longest {
		longest = h
	}
	if longest <= maxLen {
		return w, h
	}
	scale := float64(maxLen) / float64(longest)
	nw := int(math.Round(float64(w) * scale))
	nh := int(math.Round(float64(h) * scale))
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return nw, nh
}

func copyRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// resizeBilinear：像素中心对齐 (x+0.5)*src/dst-0.5；采样在 premultiplied
// alpha 空间做，避免透明边缘出现黑边。
func resizeBilinear(src image.Image, dw, dh int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	scaleX := float64(sw) / float64(dw)
	scaleY := float64(sh) / float64(dh)

	sample := func(ix, iy int) (pr, pg, pb, pa float64) {
		ix = clampInt(ix, 0, sw-1)
		iy = clampInt(iy, 0, sh-1)
		r, g, b, a := src.At(sb.Min.X+ix, sb.Min.Y+iy).RGBA()
		af := float64(a>>8) / 255.0
		return float64(r>>8) * af, float64(g>>8) * af, float64(b>>8) * af, af
	}

	for y := 0; y < dh; y++ {
		fy := (float64(y)+0.5)*scaleY - 0.5
		y0 := int(math.Floor(fy))
		wy := fy - float64(y0)
		for x := 0; x < dw; x++ {
			fx := (float64(x)+0.5)*scaleX - 0.5
			x0 := int(math.Floor(fx))
			wx := fx - float64(x0)

			r00, g00, b00, a00 := sample(x0, y0)
			r10, g10, b10, a10 := sample(x0+1, y0)
			r01, g01, b01, a01 := sample(x0, y0+1)
			r11, g11, b11, a11 := sample(x0+1, y0+1)

			mix := func(v00, v10, v01, v11 float64) float64 {
				top := v00*(1-wx) + v10*wx
				bottom := v01*(1-wx) + v11*wx
				return top*(1-wy) + bottom*wy
			}
			pr := mix(r00, r10, r01, r11)
			pg := mix(g00, g10, g01, g11)
			pb := mix(b00, b10, b01, b11)
			pa := mix(a00, a10, a01, a11)

			var r8, g8, b8 uint8
			if pa > 0 {
				r8 = uint8(math.Round(clampF(pr/pa, 0, 255)))
				g8 = uint8(math.Round(clampF(pg/pa, 0, 255)))
				b8 = uint8(math.Round(clampF(pb/pa, 0, 255)))
			}
			a8 := uint8(math.Round(clampF(pa, 0, 1) * 255))
			dst.SetRGBA(x, y, color.RGBA{r8, g8, b8, a8})
		}
	}
	return dst
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
