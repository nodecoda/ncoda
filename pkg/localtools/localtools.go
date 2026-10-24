// Package localtools — 平台工具的**本地实现包**（离线、确定性）。
//
// 定位：语料里大量 plugin 节点指向已下线/需第三方 key 的平台服务。要验证
// ncoda → runtime 这条线，先要有实现；本地实现把“平台工具”接成进程内闭包，
// 产物落在 --artifact-dir（默认 $TMPDIR/ncoda-local-artifacts）。
//
// 边界（必须诚实）：
//   - 实现的是**接口与产物契约**（参数进、声明输出出、文件落地），
//     不是平台模型的生成质量；
//   - 每个实现必须确定性（同输入同输出），否则语料校准无法复现；
//   - 平台名只出现在 bindings.yaml（绑定层），实现包本身不知道平台 provider。
package localtools

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

//go:embed bindings.yaml
var bindingsYAML []byte

// Options 控制本地实现的执行环境。
type Options struct {
	// ArtifactDir 产物目录（空 = $TMPDIR/ncoda-local-artifacts）。
	ArtifactDir string
	// DBPath 本地数据库文件（空 = 进程内 :memory:，跑完即失）。
	// 给路径 = 真实持久化：上一次 insert 的行走一次运行里的 select 能查到。
	DBPath string
}

type binding struct {
	Provider       string `yaml:"provider"`
	Operation      string `yaml:"operation"`
	Capability     string `yaml:"capability"`
	Implementation string `yaml:"implementation"`
}

type bindingFile struct {
	Bindings []binding `yaml:"bindings"`
}

var implementations = map[string]bool{"real": true, "stub": true, "partial": true}

// validateBindings is the runtime half of the binding schema. The repository
// gate (scripts/bindings_schema_check.py) is authoritative for provider names;
// this function keeps a hand-edited or generated table from silently aliasing
// an unimplemented capability at Register time.
func validateBindings(bs []binding) error {
	seen := map[string]bool{}
	for _, b := range bs {
		if b.Provider == "" || b.Operation == "" || b.Capability == "" {
			return fmt.Errorf("localtools: incomplete binding %+v", b)
		}
		if !implementations[b.Implementation] {
			return fmt.Errorf("localtools: binding %s/%s: implementation %q is not real|stub|partial",
				b.Provider, b.Operation, b.Implementation)
		}
		name := b.Provider + "/" + b.Operation
		if seen[name] {
			return fmt.Errorf("localtools: duplicate binding %q", name)
		}
		seen[name] = true
	}
	return nil
}

// Register 注册全部本地实现，并按 bindings.yaml 把语言库逻辑名绑到符号。
// 返回注册的“实现名 → 符号”列表（供日志/诊断）。
func Register(m *plugin.Manager, opts Options) ([]string, error) {
	if m == nil {
		return nil, fmt.Errorf("localtools: nil plugin manager")
	}
	dir := opts.ArtifactDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "ncoda-local-artifacts")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("localtools: artifact dir: %w", err)
	}

	img := &imageGenerator{dir: dir}
	p := &plugin.Plugin{
		ID:      "local_image_generate",
		Version: "0.1.0",
		Type:    plugin.PluginCallable,
		Tools: []plugin.Tool{{
			Name:         "generate",
			Desc:         "离线确定性图像生成：按 prompt 摘要绘制 PNG（本地实现，不调用外部模型）",
			Capabilities: []string{"image_generate.generate"},
			Call:         img.call,
		}},
	}
	if err := m.Register(p); err != nil {
		return nil, err
	}

	if err := registerDatabase(m, opts.DBPath); err != nil {
		return nil, err
	}
	if err := registerMatting(m, dir); err != nil {
		return nil, err
	}
	if err := registerImageScale(m, dir); err != nil {
		return nil, err
	}

	var binds bindingFile
	if err := yaml.Unmarshal(bindingsYAML, &binds); err != nil {
		return nil, fmt.Errorf("localtools: parse bindings: %w", err)
	}
	if err := validateBindings(binds.Bindings); err != nil {
		return nil, err
	}
	var out []string
	for _, b := range binds.Bindings {
		name := b.Provider + "/" + b.Operation
		if err := m.Alias(name, b.Capability); err != nil {
			return nil, err
		}
		out = append(out, name+" → "+b.Capability)
	}
	return out, nil
}

// ── image_generator ────────────────────────────────────────────────────────

type imageGenerator struct{ dir string }

func (g *imageGenerator) call(_ context.Context, args map[string]any) (any, error) {
	// 参数形态由 L1' 头文件决定：generate(inputs, model_setting, prompt_template,
	// references)。这里按**声明的契约**取值，而不是猜平台私有的扁平键名。
	prompt := imagePrompt(args)
	if strings.TrimSpace(prompt) == "" {
		prompt = "untitled"
	}
	ratio := mapArg(args, "model_setting", "custom_ratio")
	w := clampInt(intArg(ratio, "width", intArg(args, "width", 1024)), 16, 2048)
	h := clampInt(intArg(ratio, "height", intArg(args, "height", 1024)), 16, 2048)

	seed := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%s", prompt, w, h, stringArg(args, "model"))))
	sum := hex.EncodeToString(seed[:8])

	name := fmt.Sprintf("image-%s-%dx%d.png", sum, w, h)
	path := filepath.Join(g.dir, name)
	if err := writePNG(path, seed, w, h); err != nil {
		return nil, fmt.Errorf("image_generator: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// 返回类型来自头文件：ImageGenerateGenerateResult { file<image> data; string msg }。
	// 只发声明过的字段——多发的键会变成调用方可见的隐式契约。
	return map[string]any{
		"data": "file://" + abs,
		"msg":  "local deterministic implementation (no external model); log_id=local-" + sum,
	}, nil
}

// imagePrompt 从声明的参数形态里取提示词：prompt_template.prompt（含
// {{output}} 槽位，用 inputs.output 填充）优先，其次 inputs 本身。
func imagePrompt(args map[string]any) string {
	tmpl := mapArg(args, "prompt_template")
	if p := stringArg(tmpl, "prompt"); p != "" {
		if v := stringArg(args, "inputs"); v != "" {
			return strings.ReplaceAll(p, "{{output}}", v)
		}
		if out := stringArg(mapArg(args, "inputs"), "output"); out != "" {
			return strings.ReplaceAll(p, "{{output}}", out)
		}
		return p
	}
	if v := stringArg(args, "inputs"); v != "" {
		return v
	}
	if out := stringArg(mapArg(args, "inputs"), "output"); out != "" {
		return out
	}
	return stringArg(args, "prompt")
}

func writePNG(path string, seed [32]byte, w, h int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, render(seed, w, h))
}

// render 画一张确定性“展陈”图：渐变背景 + 画框 + 展台 + 主体装置。
// 只用标准库，保证离线与可复现。
func render(seed [32]byte, w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	hue := float64(seed[0]) / 255.0
	top := hsv(hue, 0.55, 0.18)
	bottom := hsv(math.Mod(hue+0.08, 1), 0.60, 0.42)
	accent := hsv(math.Mod(hue+0.55, 1), 0.62, 0.92)
	wall := hsv(math.Mod(hue+0.02, 1), 0.18, 0.72)

	for y := 0; y < h; y++ {
		t := float64(y) / float64(maxInt(h-1, 1))
		for x := 0; x < w; x++ {
			c := lerp(top, bottom, t)
			// 展墙与地面
			if float64(y)/float64(h) > 0.66 {
				c = lerp(c, wall, 0.55)
			}
			// 斜向光带（3 条，确定性偏移）
			band := (x + 2*y + int(seed[1])) % maxInt(w/3, 1)
			if band < maxInt(w/120, 1) {
				c = lerp(c, accent, 0.07)
			}
			img.SetRGBA(x, y, c)
		}
	}

	// 展台
	ped := rect{w / 4, h * 62 / 100, w * 3 / 4, h * 84 / 100}
	fillRect(img, ped, hsv(math.Mod(hue+0.03, 1), 0.25, 0.55))

	// 主体装置：大圆 + 小圆（人物），位置由 seed 微调
	cx := w/2 + (int(seed[2])%21-10)*w/200
	cy := h*44/100 + (int(seed[3])%21-10)*h/300
	circle(img, cx, cy, minInt(w, h)*22/100, accent)
	circle(img, cx-minInt(w, h)*9/100, cy+minInt(w, h)*13/100, minInt(w, h)*5/100, lerp(accent, color.RGBA{255, 255, 255, 255}, 0.45))

	// 画框
	frame := maxInt(minInt(w, h)*3/200, 2)
	drawFrame(img, frame, color.RGBA{245, 245, 240, 255})
	return img
}

type rect struct{ x0, y0, x1, y1 int }

func fillRect(img *image.RGBA, r rect, c color.RGBA) {
	for y := r.y0; y < r.y1; y++ {
		for x := r.x0; x < r.x1; x++ {
			if image.Pt(x, y).In(img.Bounds()) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func circle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r*r && image.Pt(x, y).In(img.Bounds()) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func drawFrame(img *image.RGBA, f int, c color.RGBA) {
	b := img.Bounds()
	for y := 0; y < b.Max.Y; y++ {
		for x := 0; x < b.Max.X; x++ {
			if x < f || y < f || x >= b.Max.X-f || y >= b.Max.Y-f {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func hsv(hue, sat, val float64) color.RGBA {
	h := hue * 6
	i := math.Floor(h)
	f := h - i
	p := val * (1 - sat)
	q := val * (1 - sat*f)
	t := val * (1 - sat*(1-f))
	var r, g, b float64
	switch int(i) % 6 {
	case 0:
		r, g, b = val, t, p
	case 1:
		r, g, b = q, val, p
	case 2:
		r, g, b = p, val, t
	case 3:
		r, g, b = p, q, val
	case 4:
		r, g, b = t, p, val
	default:
		r, g, b = val, p, q
	}
	return color.RGBA{uint8(r * 255), uint8(g * 255), uint8(b * 255), 255}
}

func lerp(a, b color.RGBA, t float64) color.RGBA {
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
}

// mapArg 沿路径取嵌套对象；任一段不是对象则返回空 map（调用方不必判空）。
func mapArg(args map[string]any, path ...string) map[string]any {
	cur := args
	for _, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func stringArg(args map[string]any, key string) string {
	switch v := args[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func intArg(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
