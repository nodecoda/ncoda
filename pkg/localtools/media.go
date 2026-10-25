// media.go — 媒体类库的**确定性近似实现**（partial 保真度）。
//
// 诚实边界（与 matting 同一纪律）：
//   - tts：不合成人声。输出一段确定性的 WAV（频率/时长由文本派生），
//     满足声明形状（data.duration / data.link）与下游对“可读音频文件”的依赖。
//   - jimeng_image_gen：不调用图像模型。输出确定性 PNG（prompt 哈希作种子）。
//   - drawing_board：不做完整 Fabric 渲染。只画几何（矩形/圆/椭圆）与
//     文本盒占位，不取外链图片、不做滤镜/字体栅格化。
//
// 三者都在 bindings.yaml 里写 implementation: partial + loss；任何把这些
// 产物当成“平台模型质量”的结论都是误用。
package localtools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

func registerMedia(m *plugin.Manager, dir string, opts Options) error {
	plugins := []struct {
		lib string
		p   *plugin.Plugin
	}{
		{"tts", &plugin.Plugin{
			ID: "local_tts", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "speech_synthesis", Desc: "离线确定性语音合成（正弦提示音，非人声）",
				Capabilities: []string{"tts.speech_synthesis"},
				Call:         ttsSpeech(dir),
			}},
		}},
		{"jimeng_image_gen", &plugin.Plugin{
			ID: "local_jimeng_image_gen", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "jimeng_generate_image", Desc: "离线确定性图像生成（prompt 哈希展陈图）",
				Capabilities: []string{"jimeng_image_gen.jimeng_generate_image"},
				Call:         jimengImage(dir),
			}},
		}},
		{"audio_duration", &plugin.Plugin{
			ID: "local_audio_duration", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "get_audio_duration", Desc: "本地音频时长（WAV 精确 / MP3 估算 / 不联网）",
				Capabilities: []string{"audio_duration.get_audio_duration"},
				Call:         audioDurationTool,
			}},
		}},
		{"drawing_board", &plugin.Plugin{
			ID: "local_drawing_board", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "render", Desc: "画板离线渲染（几何 + 文本盒占位；不取外链图）",
				Capabilities: []string{"drawing_board.render"},
				Call:         drawingBoard(dir),
			}},
		}},
	}
	for _, e := range plugins {
		if !opts.allows(e.lib) {
			continue
		}
		if err := m.Register(e.p); err != nil {
			return err
		}
	}
	return nil
}

// ── tts ────────────────────────────────────────────────────────────────────

func ttsSpeech(dir string) plugin.CallFunc {
	return func(_ context.Context, args map[string]any) (any, error) {
		text := ttsText(args["text"])
		if strings.TrimSpace(text) == "" {
			text = stringArg(args, "text")
		}
		voice := stringArg(args, "voice_id")
		speed := numberArg(args, "speed_ratio", 1)
		if speed <= 0 {
			speed = 1
		}
		// 语速 ~ 12 字/秒；0.5s 下限、30s 上限，保证文件不会失控。
		duration := float64(len([]rune(text))) / 12.0 / speed
		duration = math.Max(0.5, math.Min(30, duration))

		seed := sha256.Sum256([]byte(fmt.Sprintf("tts-v1|%s|%s|%.3f", text, voice, speed)))
		sum := hex.EncodeToString(seed[:8])
		freq := 180 + float64(seed[0])/255*160
		name := fmt.Sprintf("tts-%s-%.2fs.wav", sum, duration)
		path := filepath.Join(dir, name)
		if err := writeWAVTone(path, duration, freq); err != nil {
			return nil, fmt.Errorf("tts: %w", err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		link := "file://" + abs
		data := map[string]any{"duration": duration, "link": link}
		return map[string]any{
			"data":      data,
			"errorBody": map[string]any{"errorCode": "", "errorMessage": ""},
			"log_id":    "local-" + sum,
			"msg":       "local deterministic tone (not a voice model)",
			"outputList": []any{map[string]any{
				"data":   data,
				"log_id": "local-" + sum,
				"msg":    "",
				"code":   0,
			}},
			"code":      0,
			"isSuccess": true,
		}, nil
	}
}

func ttsText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, k := range []string{"text", "content", "data"} {
			if s, ok := t[k].(string); ok {
				return s
			}
		}
	}
	return ""
}

// ── jimeng_image_gen ──────────────────────────────────────────────────────

func jimengImage(dir string) plugin.CallFunc {
	return func(_ context.Context, args map[string]any) (any, error) {
		prompt := stringArg(args, "prompt")
		if strings.TrimSpace(prompt) == "" {
			prompt = fmt.Sprint(args["image_urls"])
		}
		w, h := ratioSize(stringArg(args, "ratio"))
		seed := sha256.Sum256([]byte(fmt.Sprintf("jimeng-v1|%s|%s|%dx%d", prompt, stringArg(args, "model"), w, h)))
		sum := hex.EncodeToString(seed[:8])
		path := filepath.Join(dir, fmt.Sprintf("jimeng-%s-%dx%d.png", sum, w, h))
		if err := writePNG(path, seed, w, h); err != nil {
			return nil, fmt.Errorf("jimeng_image_gen: %w", err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		return map[string]any{
			"message": "",
			"task_id": "local-" + sum,
			"url":     "file://" + abs,
		}, nil
	}
}

func ratioSize(ratio string) (int, int) {
	switch strings.TrimSpace(ratio) {
	case "16:9":
		return 1280, 720
	case "9:16":
		return 720, 1280
	case "4:3":
		return 1152, 864
	case "3:4":
		return 864, 1152
	case "3:2":
		return 1200, 800
	case "2:3":
		return 800, 1200
	default:
		return 1024, 1024
	}
}

// ── drawing_board ─────────────────────────────────────────────────────────

func drawingBoard(dir string) plugin.CallFunc {
	return func(_ context.Context, args map[string]any) (any, error) {
		schema, err := decodeCanvas(args["canvas_schema"])
		if err != nil {
			return map[string]any{"data": "", "msg": "invalid canvas_schema: " + err.Error()}, nil
		}
		w := clampInt(int(numField(schema, "width", 1080)), 16, 4096)
		h := clampInt(int(numField(schema, "height", 1920)), 16, 4096)
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		bg, ok := parseColor(strField(schema, "backgroundColor"))
		if !ok {
			bg, _ = parseColor(strField(schema, "background"))
		}
		if !ok {
			bg = color.RGBA{255, 255, 255, 255}
		}
		fillRect(img, rect{0, 0, w, h}, bg)

		objects := 0
		if objs, ok := schema["objects"].([]any); ok {
			for _, obj := range objs {
				if m, ok := obj.(map[string]any); ok {
					drawBoardObject(img, m, 0)
					objects++
				}
			}
		}

		sum := sha256.Sum256([]byte(fmt.Sprintf("board-v1|%dx%d|%d", w, h, objects)))
		path := filepath.Join(dir, fmt.Sprintf("board-%s-%dx%d.png", hex.EncodeToString(sum[:8]), w, h))
		if err := writePNGAtomic(path, img); err != nil {
			return map[string]any{"data": "", "msg": "artifact write failed: " + err.Error()}, nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		return map[string]any{
			"data": "file://" + abs,
			"msg":  fmt.Sprintf("local partial render: %d top-level objects, geometry only (no glyphs/external images/filters)", objects),
		}, nil
	}
}

func decodeCanvas(v any) (map[string]any, error) {
	switch t := v.(type) {
	case map[string]any:
		return t, nil
	case string:
		var out map[string]any
		if err := json.Unmarshal([]byte(t), &out); err != nil {
			return nil, err
		}
		return out, nil
	case nil:
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("want object or JSON string, got %T", v)
	}
}

func drawBoardObject(img *image.RGBA, obj map[string]any, depth int) {
	if depth > 12 {
		return
	}
	if visible, ok := obj["visible"].(bool); ok && !visible {
		return
	}
	typ := strField(obj, "type")
	left := numField(obj, "left", 0)
	top := numField(obj, "top", 0)
	w := numField(obj, "width", 0) * scaleOf(obj, "scaleX")
	h := numField(obj, "height", 0) * scaleOf(obj, "scaleY")
	x0, y0 := int(left), int(top)
	x1, y1 := int(left+w), int(top+h)
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}

	switch typ {
	case "Group":
		if kids, ok := obj["objects"].([]any); ok {
			for _, k := range kids {
				if km, ok := k.(map[string]any); ok {
					drawBoardObject(img, km, depth+1)
				}
			}
		}
		return
	case "Image":
		// 外链图不取；画一个带边框的占位块，声明“这里原本是图片”。
		drawBorder(img, rect{x0, y0, x1, y1}, color.RGBA{180, 180, 180, 255}, 2)
		return
	}

	bbox := rect{x0, y0, x1, y1}
	if c, ok := parseColor(strField(obj, "fill")); ok {
		switch typ {
		case "Circle", "Ellipse":
			drawEllipse(img, bbox, c)
		default:
			fillRect(img, bbox, c)
		}
	}
	if c, ok := parseColor(strField(obj, "stroke")); ok {
		sw := int(numField(obj, "strokeWidth", 0))
		if sw > 0 {
			drawBorder(img, bbox, c, sw)
		}
	}
	switch typ {
	case "Textbox", "IText", "Text", "TextboxGroup":
		// 文本不栅格化：用 3 条横线表示文本占位，避免把空白图当“渲染成功”。
		line := color.RGBA{90, 90, 90, 255}
		for i := 1; i <= 3; i++ {
			y := y0 + (y1-y0)*i/4
			fillRect(img, rect{x0 + 2, y, x1 - 2, y + 1}, line)
		}
	}
}

func scaleOf(obj map[string]any, key string) float64 {
	s := numField(obj, key, 1)
	if s == 0 {
		return 1
	}
	if s < 0 {
		return -s
	}
	return s
}

func drawEllipse(img *image.RGBA, r rect, c color.RGBA) {
	if r.x1 <= r.x0 || r.y1 <= r.y0 {
		return
	}
	cx := float64(r.x0+r.x1) / 2
	cy := float64(r.y0+r.y1) / 2
	rx := float64(r.x1-r.x0) / 2
	ry := float64(r.y1-r.y0) / 2
	for y := r.y0; y < r.y1; y++ {
		for x := r.x0; x < r.x1; x++ {
			dx := (float64(x) - cx) / rx
			dy := (float64(y) - cy) / ry
			if dx*dx+dy*dy <= 1 {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func drawBorder(img *image.RGBA, r rect, c color.RGBA, width int) {
	if width < 1 {
		width = 1
	}
	fillRect(img, rect{r.x0, r.y0, r.x1, r.y0 + width}, c)
	fillRect(img, rect{r.x0, r.y1 - width, r.x1, r.y1}, c)
	fillRect(img, rect{r.x0, r.y0, r.x0 + width, r.y1}, c)
	fillRect(img, rect{r.x1 - width, r.y0, r.x1, r.y1}, c)
}

func numField(m map[string]any, key string, def float64) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

func strField(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// parseColor 支持 #RGB/#RRGGBB/#RRGGBBAA 与 rgb(r,g,b)/rgba(r,g,b,a)。
func parseColor(s string) (color.RGBA, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "transparent" || s == "none" {
		return color.RGBA{}, false
	}
	if strings.HasPrefix(s, "#") {
		h := strings.TrimPrefix(s, "#")
		switch len(h) {
		case 3:
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
			fallthrough
		case 6:
			h += "ff"
			fallthrough
		case 8:
			n, err := strconv.ParseUint(h, 16, 64)
			if err != nil {
				return color.RGBA{}, false
			}
			return color.RGBA{uint8(n >> 24), uint8(n >> 16), uint8(n >> 8), uint8(n)}, true
		}
		return color.RGBA{}, false
	}
	if strings.HasPrefix(s, "rgb") {
		body := s[strings.IndexByte(s, '(')+1:]
		body = strings.TrimSuffix(body, ")")
		parts := strings.Split(body, ",")
		if len(parts) < 3 {
			return color.RGBA{}, false
		}
		vals := make([]uint8, 0, 4)
		vals = append(vals, 255)
		for i := 0; i < len(parts) && i < 4; i++ {
			f, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
			if err != nil {
				return color.RGBA{}, false
			}
			if i == 3 && f <= 1 {
				f *= 255
			}
			vals[i] = uint8(math.Max(0, math.Min(255, f)))
		}
		return color.RGBA{vals[0], vals[1], vals[2], vals[3]}, true
	}
	return color.RGBA{}, false
}

// ── audio_duration ────────────────────────────────────────────────────────

func audioDurationTool(_ context.Context, args map[string]any) (any, error) {
	raw := args["audio_url"]
	if raw == nil || fmt.Sprint(raw) == "" {
		raw = args["mp3_url"]
	}
	data, err := audioBytes(raw)
	if err != nil {
		return audioDurationFailure(err), nil
	}
	duration, format, err := audioDuration(data)
	if err != nil {
		return audioDurationFailure(err), nil
	}
	msg := fmt.Sprintf("local %s: %.3fs", format, duration)
	return map[string]any{
		"duration":   duration,
		"errorBody":  map[string]any{"errorCode": "", "errorMessage": ""},
		"isSuccess":  true,
		"message":    msg,
		"outputList": []any{map[string]any{"duration": duration, "message": msg}},
		"error":      "",
	}, nil
}

// audioDurationFailure 把失败写成**声明形状**（header 有 isSuccess/errorBody），
// 不是 Go error：调用方的 default/failure 分支才能按契约接住。
func audioDurationFailure(err error) map[string]any {
	code := "LOCAL_AUDIO_ERROR"
	msg := err.Error()
	for _, prefix := range []string{"REMOTE_URL_UNSUPPORTED", "UNSUPPORTED_FORMAT", "INVALID_WAV", "INVALID_MP3"} {
		if strings.HasPrefix(msg, prefix) {
			code = prefix
			break
		}
	}
	return map[string]any{
		"duration":   0.0,
		"errorBody":  map[string]any{"errorCode": code, "errorMessage": msg},
		"isSuccess":  false,
		"message":    msg,
		"outputList": []any{},
		"error":      msg,
	}
}
