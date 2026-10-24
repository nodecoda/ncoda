// matting.go — `matting` 库的本地实现（确定性抠图）。
//
// 诚实边界（必须说清楚）：
//   - 这不是**模型抠图**。平台 replace_background / removebg 类服务的语义来自
//     训练出来的分割模型，本包没有那个模型，也不假装有。
//   - 本实现是**边界背景 keying**：用图像外圈像素估计背景色，按 RGB 距离生成
//     alpha。它对纯色/近纯色背景（商品图、证件照、白底图）有效，对复杂背景
//     （树林、人群、渐变光）会失败。失败是**可观察的**：alpha 不干净，调用方
//     看到的就是不干净。
//   - 算法与阈值是本项目**自己定义**的（mattingAlgorithmVersion），因此产物
//     命名把它算进去：换算法必须换名字，旧产物不会被误认成新算法结果。
//
// 声明（唯一权威）：libraries/matting.ncoda
//
//	cutout(int only_mask, int output_mode, string prompt, file url) -> MattingCutoutResult
//	MattingCutoutResult { file<image> data; MattingCutoutErrorbody errorBody;
//	                      bool isSuccess; string mask; string msg }
//
// 参数语义（本项目定义，语料里出现过的值都覆盖）：
//
//	url          本地路径 / file:// / data:image/...;base64 / 节点产物 map
//	             （{url|path|data_url|data}）。http(s) **不联网**：离线确定性
//	             优先，返回 isSuccess=false + REMOTE_URL_UNSUPPORTED。
//	only_mask    1 = 只出蒙版（data 为空）；其余值 = 出抠图。语料里有 3 且
//	             紧接着消费 data，所以 3 不能当"只出蒙版"。
//	output_mode  0 = RGBA PNG（透明背景）。未定义值返回 UNSUPPORTED_OUTPUT_MODE，
//	             不静默降级——否则调用方会拿到一个"看起来对"的产物。
//	prompt       平台模型用的提示词；边界 keying 不需要它，接受但忽略。
//
// 失败不 panic、不返回 Go error：失败是**声明的结果**（isSuccess=false +
// errorBody），因为头文件把 failure_branch 写进了契约。
package localtools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"math"
	"path/filepath"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

// mattingAlgorithmVersion 是算法身份。改阈值/公式必须同步改它，
// 产物文件名才会变（同输入同算法 ⇒ 同路径；否则就是自欺的确定性）。
const mattingAlgorithmVersion = "border-key-v1"

// 背景判定的两个端点：dist <= bgTolerance ⇒ 全透明；
// dist >= fgDistance ⇒ 全不透明；两者之间做 smoothstep（抗锯齿边缘）。
const (
	mattingBGTolerance = 24.0
	mattingFGDist      = 96.0
	mattingMaxSide     = 4096
)

type mattingTool struct{ dir string }

// registerMatting 注册 matting.cutout。库名/函数名 → 实现的绑定仍走
// bindings.yaml（与 database 同一个机制，不另开一条路）。
func registerMatting(m *plugin.Manager, dir string) error {
	t := &mattingTool{dir: dir}
	p := &plugin.Plugin{
		ID:      "local_matting",
		Version: "0.1.0",
		Type:    plugin.PluginCallable,
		Tools: []plugin.Tool{{
			Name:         "cutout",
			Desc:         "本地确定性抠图：边界背景 keying（不是分割模型）",
			Capabilities: []string{"matting.cutout"},
			Call:         t.cutout,
		}},
	}
	return m.Register(p)
}

// ── 入口 ───────────────────────────────────────────────────────────────────

func (t *mattingTool) cutout(_ context.Context, args map[string]any) (any, error) {
	// 先解平台参数壳：ref/literal 两种形态在 tool 节点已经归一，但经由
	// 平台 artifact 回来的参数仍可能带 {value:…} 壳（与 database 同因）。
	raw := unwrapValue(args["url"])
	onlyMask := intArg(args, "only_mask", 0)
	outputMode := intArg(args, "output_mode", 0)

	if outputMode != 0 {
		return mattingFailure("UNSUPPORTED_OUTPUT_MODE",
			fmt.Sprintf("output_mode=%d is not defined by this implementation (only 0 = RGBA cutout)", outputMode)), nil
	}

	data, err := readImageInput(raw)
	if err != nil {
		return mattingFailure("INVALID_INPUT", err.Error()), nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return mattingFailure("DECODE_FAILED", fmt.Sprintf("input is not a decodable image (png/jpeg/gif): %v", err)), nil
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return mattingFailure("DECODE_FAILED", "image has empty bounds"), nil
	}
	if b.Dx() > mattingMaxSide || b.Dy() > mattingMaxSide {
		return mattingFailure("IMAGE_TOO_LARGE",
			fmt.Sprintf("image is %dx%d; max side is %d", b.Dx(), b.Dy(), mattingMaxSide)), nil
	}

	cut, mask := mattingRender(img)
	sum := sha256.Sum256(append([]byte(mattingAlgorithmVersion+"|"), data...))
	stem := hex.EncodeToString(sum[:8])
	base := fmt.Sprintf("matting-%s-%dx%d", stem, b.Dx(), b.Dy())

	maskURL, err := t.writeArtifact("matting-mask-"+stem+fmt.Sprintf("-%dx%d.png", b.Dx(), b.Dy()), mask)
	if err != nil {
		return mattingFailure("ARTIFACT_WRITE_FAILED", err.Error()), nil
	}

	msg := "local deterministic background keying (not a segmentation model)"
	if onlyMask == 1 {
		return mattingSuccess("", maskURL, msg+"; only_mask=1 ⇒ data is empty"), nil
	}
	dataURL, err := t.writeArtifact(base+".png", cut)
	if err != nil {
		return mattingFailure("ARTIFACT_WRITE_FAILED", err.Error()), nil
	}
	return mattingSuccess(dataURL, maskURL, msg), nil
}

// ── 产物 ───────────────────────────────────────────────────────────────────

func (t *mattingTool) writeArtifact(name string, img image.Image) (string, error) {
	path := filepath.Join(t.dir, name)
	if err := writePNGAtomic(path, img); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return "file://" + abs, nil
}

// ── 算法 ───────────────────────────────────────────────────────────────────

// mattingRender 返回抠图（RGBA，背景 alpha=0）与蒙版（白=前景，黑=背景）。
func mattingRender(src image.Image) (*image.RGBA, *image.RGBA) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	cut := image.NewRGBA(image.Rect(0, 0, w, h))
	mask := image.NewRGBA(image.Rect(0, 0, w, h))

	// 已经带透明度的输入（例如上游出的是抠好的 PNG）：保留 alpha，不再
	// 二次 keying。二次 keying 会把半透明边缘吃成硬边。
	if hasAlpha(src) {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
				cut.SetRGBA(x, y, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)})
				mask.SetRGBA(x, y, color.RGBA{uint8(a >> 8), uint8(a >> 8), uint8(a >> 8), 255})
			}
		}
		return cut, mask
	}

	bg := estimateBackground(src)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			dr := float64(r>>8) - float64(bg.R)
			dg := float64(g>>8) - float64(bg.G)
			db := float64(bl>>8) - float64(bg.B)
			dist := math.Sqrt(dr*dr + dg*dg + db*db)
			a := mattingAlpha(dist)
			au := uint8(math.Round(a * 255))
			cut.SetRGBA(x, y, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), au})
			mask.SetRGBA(x, y, color.RGBA{au, au, au, 255})
		}
	}
	return cut, mask
}

func mattingAlpha(dist float64) float64 {
	if dist <= mattingBGTolerance {
		return 0
	}
	if dist >= mattingFGDist {
		return 1
	}
	t := (dist - mattingBGTolerance) / (mattingFGDist - mattingBGTolerance)
	return t * t * (3 - 2*t) // smoothstep
}

func hasAlpha(src image.Image) bool {
	switch src.(type) {
	case *image.RGBA, *image.NRGBA, *image.RGBA64, *image.NRGBA64:
	default:
		return false
	}
	b := src.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := src.At(x, y).RGBA(); a < 0xFA00 { // < 250/255
				return true
			}
		}
	}
	return false
}

// estimateBackground：外圈 1px 像素按每通道 16 级量化取众数桶，再取该桶均值。
// 外圈在多数商品图/人像图里背景占优；故意不做全图统计，避免主体被当成背景。
func estimateBackground(src image.Image) color.RGBA {
	b := src.Bounds()
	type bucket struct{ r, g, b, n uint64 }
	counts := map[[3]uint8]*bucket{}
	add := func(x, y int) {
		r, g, bl, _ := src.At(x, y).RGBA()
		r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(bl>>8)
		k := [3]uint8{r8 / 16, g8 / 16, b8 / 16}
		e := counts[k]
		if e == nil {
			e = &bucket{}
			counts[k] = e
		}
		e.r += uint64(r8)
		e.g += uint64(g8)
		e.b += uint64(b8)
		e.n++
	}
	for x := b.Min.X; x < b.Max.X; x++ {
		add(x, b.Min.Y)
		if b.Dy() > 1 {
			add(x, b.Max.Y-1)
		}
	}
	for y := b.Min.Y + 1; y < b.Max.Y-1; y++ {
		add(b.Min.X, y)
		if b.Dx() > 1 {
			add(b.Max.X-1, y)
		}
	}
	var best *bucket
	for _, e := range counts {
		if best == nil || e.n > best.n {
			best = e
		}
	}
	if best == nil || best.n == 0 {
		return color.RGBA{0, 0, 0, 255}
	}
	return color.RGBA{uint8(best.r / best.n), uint8(best.g / best.n), uint8(best.b / best.n), 255}
}

// ── 结果 ───────────────────────────────────────────────────────────────────

func mattingSuccess(data, mask, msg string) map[string]any {
	return map[string]any{
		"data":      data,
		"errorBody": map[string]any{"errorCode": "", "errorMessage": ""},
		"isSuccess": true,
		"mask":      mask,
		"msg":       msg,
	}
}

func mattingFailure(code, message string) map[string]any {
	return map[string]any{
		"data":      "",
		"errorBody": map[string]any{"errorCode": code, "errorMessage": message},
		"isSuccess": false,
		"mask":      "",
		"msg":       message,
	}
}
