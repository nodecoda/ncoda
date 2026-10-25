package localtools

// capcut_video_compose.get_audio_duration —— 唯一能在开源 runtime 上实现的函数。
//
// 单位裁定（证据见 docs/LIBRARY-BINDING-SPEC.md §15 D-1）：返回**微秒整数**。
// 语料把它直接当时间线端点/关键帧 offset 用（`end: audioStartTime + duration`，
// 5s 音频配 `duration: 4884897` 量级），没有 ×1e6 的换算；同一个语料里
// `audio_duration.get_audio_duration` 返回秒并显式 `* 1000000`，两者不同单位。
//
// 保真度：partial（WAV/MP3/OGG 精确 / 不联网），loss 与 audio_duration 同源。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

func composeManager(t *testing.T) *plugin.Manager {
	t.Helper()
	return portableManager(t, Options{})
}

func TestComposeGetAudioDurationIsRegistered(t *testing.T) {
	m := composeManager(t)
	names := m.Names()
	for _, want := range []string{
		"capcut_video_compose/get_audio_duration",
		"capcut_video_compose.get_audio_duration",
	} {
		if !hasName(names, want) {
			t.Fatalf("%q missing; names=%v", want, names)
		}
	}
}

func TestComposeGetAudioDurationReturnsMicroseconds(t *testing.T) {
	m := composeManager(t)
	wav := filepath.Join(t.TempDir(), "a.wav")
	if err := writeWAVTone(wav, 1.5, 440); err != nil {
		t.Fatalf("writeWAVTone: %v", err)
	}
	got, err := m.Call(context.Background(), "capcut_video_compose/get_audio_duration",
		map[string]any{"mp3_url": wav})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	rec, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("result = %T", got)
	}
	if d, _ := rec["duration"].(float64); d != 1_500_000 {
		t.Fatalf("duration = %v, want 1500000 (microseconds)", rec["duration"])
	}
	if msg, _ := rec["message"].(string); !strings.Contains(msg, "wav") {
		t.Fatalf("message = %q", rec["message"])
	}
	list, _ := rec["outputList"].([]any)
	if len(list) != 1 {
		t.Fatalf("outputList = %v", rec["outputList"])
	}
	first, _ := list[0].(map[string]any)
	if d, _ := first["duration"].(float64); d != 1_500_000 {
		t.Fatalf("outputList[0].duration = %v", first["duration"])
	}
	// 只发 header 声明过的键（duration / message / outputList）。
	for k := range rec {
		switch k {
		case "duration", "message", "outputList":
		default:
			t.Fatalf("undeclared key %q in result", k)
		}
	}
}

func TestComposeGetAudioDurationRemoteIsError(t *testing.T) {
	m := composeManager(t)
	_, err := m.Call(context.Background(), "capcut_video_compose/get_audio_duration",
		map[string]any{"mp3_url": "https://example.com/a.mp3"})
	if err == nil {
		t.Fatal("remote URL must be an error (failure_branch), not a silent zero")
	}
	if !strings.Contains(err.Error(), "REMOTE_URL_UNSUPPORTED") {
		t.Fatalf("error = %v", err)
	}
}

func TestComposeGetAudioDurationMissingInputIsError(t *testing.T) {
	m := composeManager(t)
	_, err := m.Call(context.Background(), "capcut_video_compose/get_audio_duration", map[string]any{})
	if err == nil {
		t.Fatal("missing mp3_url must be an error")
	}
	if !strings.Contains(err.Error(), "MISSING_INPUT") {
		t.Fatalf("error = %v, want MISSING_INPUT", err)
	}
}
