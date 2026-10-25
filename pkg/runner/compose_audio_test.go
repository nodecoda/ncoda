package runner

// capcut_video_compose.get_audio_duration 的 CLI 级判据（D-1 裁定的唯一实现面）：
//   - 微秒整数契约：0.5s WAV → 500000，而不是 0.5 或 500；
//   - 服务端草稿状态机（create_draft 等）必须保持 unbound（RBINDING_MISSING），
//     不许用本地文件冒充 draft_url。

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func silentWAV(samples int) []byte {
	data := make([]byte, samples*2)
	buf := make([]byte, 0, 44+len(data))
	le := func(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
	le16 := func(v uint16) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }
	buf = append(buf, []byte("RIFF")...)
	buf = append(buf, le(uint32(36+len(data)))...)
	buf = append(buf, []byte("WAVEfmt ")...)
	buf = append(buf, le(16)...)
	buf = append(buf, le16(1)...)   // PCM
	buf = append(buf, le16(1)...)   // mono
	buf = append(buf, le(16000)...) // sample rate
	buf = append(buf, le(32000)...) // byte rate
	buf = append(buf, le16(2)...)   // block align
	buf = append(buf, le16(16)...)  // bits
	buf = append(buf, []byte("data")...)
	buf = append(buf, le(uint32(len(data)))...)
	return append(buf, data...)
}

func TestComposeAudioDurationIsMicroseconds(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "half.wav")
	if err := os.WriteFile(wav, silentWAV(8000), 0o644); err != nil { // 0.5s
		t.Fatal(err)
	}
	src := `@language nodecoda/1
@mode workflow
import "capcut_video_compose";

function main() -> any {
    let d = capcut_video_compose.get_audio_duration({ "mp3_url": "` + wav + `" });
    return { "duration": d.duration, "msg": d.message };
}
`
	srcPath := filepath.Join(dir, "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--local-plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got struct {
		Duration float64 `json:"duration"`
		Msg      string  `json:"msg"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if got.Duration != 500000 {
		t.Fatalf("duration = %v, want 500000 microseconds", got.Duration)
	}
	if !strings.Contains(got.Msg, "wav") {
		t.Fatalf("msg = %q", got.Msg)
	}
}

func TestComposeDraftStateMachineStaysUnbound(t *testing.T) {
	dir := t.TempDir()
	src := `@language nodecoda/1
@mode workflow
import "capcut_video_compose";

function main() -> any {
    let d = capcut_video_compose.create_draft({ "width": 1080, "height": 1920 });
    return d.draft_url;
}
`
	srcPath := filepath.Join(dir, "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, _, errs := runCLI(t, "", "--local-plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 1 {
		t.Fatalf("rc=%d, want 1 (unbound draft state machine); stderr=%s", rc, errs)
	}
	if !strings.Contains(errs, "RBINDING_MISSING") || !strings.Contains(errs, "create_draft") {
		t.Fatalf("stderr = %s", errs)
	}
}
