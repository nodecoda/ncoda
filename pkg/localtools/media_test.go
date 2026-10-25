package localtools

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTTSSpeechSynthesisIsDeterministicAndReadable(t *testing.T) {
	m := portableManager(t, Options{})
	args := map[string]any{"text": "你好，世界", "voice_id": "v1", "speed_ratio": 1.0}

	first := callAlias(t, m, "tts/speech_synthesis", args).(map[string]any)
	second := callAlias(t, m, "tts/speech_synthesis", args).(map[string]any)
	if first["data"].(map[string]any)["link"] != second["data"].(map[string]any)["link"] {
		t.Fatalf("tts output is not deterministic: %v vs %v", first["data"], second["data"])
	}
	if first["isSuccess"] != true {
		t.Fatalf("tts isSuccess = %v", first["isSuccess"])
	}
	link := first["data"].(map[string]any)["link"].(string)
	path := strings.TrimPrefix(link, "file://")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tts artifact: %v", err)
	}
	want, format, err := audioDuration(data)
	if err != nil || format != "wav" {
		t.Fatalf("tts artifact is not a readable wav: format=%q err=%v", format, err)
	}
	if got := first["data"].(map[string]any)["duration"].(float64); got != want {
		t.Fatalf("declared duration %v != wav duration %v", got, want)
	}
	if len(first["outputList"].([]any)) != 1 {
		t.Fatalf("outputList = %v", first["outputList"])
	}
}

func TestJimengImageIsDeterministicPNG(t *testing.T) {
	m := portableManager(t, Options{})
	args := map[string]any{"prompt": "一只戴墨镜的狗", "model": "图片4.0", "ratio": "16:9"}
	a := callAlias(t, m, "jimeng_image_gen/jimeng_generate_image", args).(map[string]any)
	b := callAlias(t, m, "jimeng_image_gen/jimeng_generate_image", args).(map[string]any)
	if a["url"] != b["url"] {
		t.Fatalf("jimeng url not deterministic: %v vs %v", a["url"], b["url"])
	}
	f, err := os.Open(strings.TrimPrefix(a["url"].(string), "file://"))
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if img.Bounds().Dx() != 1280 || img.Bounds().Dy() != 720 {
		t.Fatalf("ratio 16:9 rendered %v", img.Bounds())
	}
}

func TestDrawingBoardRendersGeometryAndDisclosesLoss(t *testing.T) {
	m := portableManager(t, Options{})
	schema := `{"width":120,"height":80,"backgroundColor":"#ffffff",
	  "objects":[{"type":"Rect","left":10,"top":10,"width":50,"height":30,"fill":"#ff0000"},
	             {"type":"Textbox","left":10,"top":50,"width":80,"height":20,"fill":"#000000ff","text":"hi"}]}`
	got := callAlias(t, m, "drawing_board/render", map[string]any{"canvas_schema": schema}).(map[string]any)
	if got["data"] == "" {
		t.Fatalf("drawing_board data empty: %v", got)
	}
	if !strings.Contains(got["msg"].(string), "partial") {
		t.Fatalf("msg must disclose partial rendering: %v", got["msg"])
	}
	f, err := os.Open(strings.TrimPrefix(got["data"].(string), "file://"))
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if img.Bounds().Dx() != 120 || img.Bounds().Dy() != 80 {
		t.Fatalf("board size = %v", img.Bounds())
	}
	r, g, b, _ := img.At(20, 20).RGBA()
	if r>>8 != 0xff || g>>8 != 0 || b>>8 != 0 {
		t.Fatalf("rect fill not rendered: rgba=(%d,%d,%d)", r>>8, g>>8, b>>8)
	}

	// 坏 schema 是声明失败（data 空 + msg），不是 panic。
	bad := callAlias(t, m, "drawing_board/render", map[string]any{"canvas_schema": "{not json"}).(map[string]any)
	if bad["data"] != "" || !strings.Contains(bad["msg"].(string), "invalid canvas_schema") {
		t.Fatalf("bad schema result = %v", bad)
	}
}

func TestAudioDurationLocalAndRemote(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "tone.wav")
	if err := writeWAVTone(wav, 1.25, 440); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	m := portableManager(t, Options{})

	got := callAlias(t, m, "audio_duration/get_audio_duration", map[string]any{"mp3_url": "file://" + wav}).(map[string]any)
	if got["isSuccess"] != true {
		t.Fatalf("local wav failed: %v", got)
	}
	if d := got["duration"].(float64); d < 1.24 || d > 1.26 {
		t.Fatalf("duration = %v, want ~1.25", d)
	}

	remote := callAlias(t, m, "audio_duration/get_audio_duration", map[string]any{"mp3_url": "https://example.com/x.mp3"}).(map[string]any)
	if remote["isSuccess"] != false {
		t.Fatalf("remote url must not silently succeed: %v", remote)
	}
	if !strings.Contains(remote["errorBody"].(map[string]any)["errorCode"].(string), "REMOTE_URL_UNSUPPORTED") {
		t.Fatalf("remote error code = %v", remote["errorBody"])
	}

	missing := callAlias(t, m, "audio_duration/get_audio_duration", map[string]any{"mp3_url": filepath.Join(dir, "nope.wav")}).(map[string]any)
	if missing["isSuccess"] != false {
		t.Fatalf("missing file must be a declared failure: %v", missing)
	}
}

// 负控：媒体类库必须是 partial + 有 loss；把它们写成 real 会让 ok_real 假涨。
func TestMediaBindingsArePartialWithLoss(t *testing.T) {
	var f bindingFile
	if err := yaml.Unmarshal(bindingsYAML, &f); err != nil {
		t.Fatalf("parse bindings: %v", err)
	}
	want := map[string]bool{
		"tts/speech_synthesis":                   true,
		"jimeng_image_gen/jimeng_generate_image": true,
		"drawing_board/render":                   true,
		"audio_duration/get_audio_duration":      true,
		"timer_wait/time_wait":                   true,
	}
	seen := map[string]bool{}
	for _, b := range f.Bindings {
		key := b.Provider + "/" + b.Operation
		if want[key] {
			seen[key] = true
			if b.Implementation != "partial" || strings.TrimSpace(b.Loss) == "" {
				t.Fatalf("%s = %q loss=%q, want partial with loss", key, b.Implementation, b.Loss)
			}
		}
	}
	for key := range want {
		if !seen[key] {
			t.Fatalf("missing media binding %s", key)
		}
	}
}
