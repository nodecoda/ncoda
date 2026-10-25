// audio_exact_test.go — audio_duration 的**精确性**判据（先红后绿）。
//
// 不变量：
//   - MP3 时长 = 完整帧的采样数之和 / 采样率（VBR 逐帧、CBR 也逐帧）；
//   - 首帧带 Xing/Info 帧数时，以声明帧数为准；
//   - 不足一帧的尾巴不计入（truncated 不四舍五入成整帧）；
//   - OGG Vorbis 用末页 granule / 采样率；
//   - 认不出的容器仍是显式 UNSUPPORTED_FORMAT，不猜。
package localtools

import (
	"encoding/binary"
	"math"
	"testing"
)

// mp3Frame 生成一个 MPEG1 Layer III 帧（stereo，无 CRC，无 padding）。
func mp3Frame(bitrateIdx byte) []byte {
	const (
		rateIdx  = 0 // 44100
		v1l3rate = 144
	)
	bitrate := mp3V1L3KBPS[bitrateIdx] * 1000
	n := v1l3rate * bitrate / 44100
	f := make([]byte, n)
	f[0], f[1] = 0xFF, 0xFB           // MPEG1, Layer III, no CRC
	f[2] = bitrateIdx<<4 | rateIdx<<2 // bitrate / sample rate / no padding
	f[3] = 0x00                       // stereo
	return f
}

var mp3V1L3KBPS = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}

func mp3Frames(n int, idx byte) []byte {
	var out []byte
	for i := 0; i < n; i++ {
		out = append(out, mp3Frame(idx)...)
	}
	return out
}

func mp3WithXing(frames int) []byte {
	f := mp3Frame(9) // 128 kbps -> 417 bytes
	off := 4 + 32    // MPEG1 stereo side info
	copy(f[off:], "Xing")
	binary.BigEndian.PutUint32(f[off+4:], 1) // frames flag
	binary.BigEndian.PutUint32(f[off+8:], uint32(frames))
	return f
}

// oggVorbis 生成两页：BOS（Vorbis 识别头，采样率 44100）+ EOS（granule）。
func oggVorbis(rate uint32, granules uint64) []byte {
	id := []byte{0x01, 'v', 'o', 'r', 'b', 'i', 's'}
	body := make([]byte, 30) // identification header: fixed 30 bytes
	copy(body, id)
	binary.LittleEndian.PutUint32(body[7:], 0)     // vorbis_version
	body[11] = 1                                   // channels
	binary.LittleEndian.PutUint32(body[12:], rate) // sample rate
	body[29] = 1                                   // framing bit

	page := func(headerType byte, granule uint64, packet []byte) []byte {
		h := make([]byte, 27)
		copy(h, "OggS")
		h[4] = 0 // version
		h[5] = headerType
		binary.LittleEndian.PutUint64(h[6:], granule)
		binary.LittleEndian.PutUint32(h[14:], 1) // serial
		binary.LittleEndian.PutUint32(h[18:], 1) // sequence
		// h[22:26] crc = 0（时长解析不看 CRC）
		h[26] = 1 // one segment
		h = append(h, byte(len(packet)))
		return append(h, packet...)
	}
	out := page(2, 0, body)
	out = append(out, page(4, granules, nil)...)
	return out
}

func exact(t *testing.T, blob []byte) float64 {
	t.Helper()
	got, format, err := audioDuration(blob)
	if err != nil {
		t.Fatalf("audioDuration(%s): %v", format, err)
	}
	return got
}

func TestMP3CBRIsExactNotEstimated(t *testing.T) {
	blob := mp3Frames(10, 9) // 10 × 1152 samples @44100
	want := 10 * 1152.0 / 44100.0
	if got := exact(t, blob); math.Abs(got-want) > 1e-9 {
		t.Fatalf("cbr duration = %.9f, want %.9f（估算值约为 0.260625）", got, want)
	}
}

func TestMP3VBRSumsPerFrame(t *testing.T) {
	var blob []byte
	for i := 0; i < 10; i++ {
		idx := byte(9) // 128k
		if i%2 == 1 {
			idx = 11 // 192k
		}
		blob = append(blob, mp3Frame(idx)...)
	}
	want := 10 * 1152.0 / 44100.0
	if got := exact(t, blob); math.Abs(got-want) > 1e-9 {
		t.Fatalf("vbr duration = %.9f, want %.9f", got, want)
	}
}

func TestMP3XingFrameCountWins(t *testing.T) {
	blob := mp3WithXing(100)
	blob = append(blob, mp3Frame(9)...) // 物理帧数远少于声明
	want := 100 * 1152.0 / 44100.0
	if got := exact(t, blob); math.Abs(got-want) > 1e-9 {
		t.Fatalf("xing duration = %.9f, want %.9f", got, want)
	}
}

func TestMP3TruncatedTailIsNotAFrame(t *testing.T) {
	blob := mp3Frames(3, 9)
	blob = append(blob, mp3Frame(9)[:100]...) // 不足一帧
	want := 3 * 1152.0 / 44100.0
	if got := exact(t, blob); math.Abs(got-want) > 1e-9 {
		t.Fatalf("truncated duration = %.9f, want %.9f（尾巴不得凑整）", got, want)
	}
}

func TestMP3SkipsID3v2(t *testing.T) {
	id3 := make([]byte, 10)
	copy(id3, "ID3")
	id3[3], id3[4] = 4, 0
	blob := append(id3, mp3Frames(4, 9)...)
	want := 4 * 1152.0 / 44100.0
	if got := exact(t, blob); math.Abs(got-want) > 1e-9 {
		t.Fatalf("id3 duration = %.9f, want %.9f", got, want)
	}
}

func TestOggVorbisUsesGranule(t *testing.T) {
	blob := oggVorbis(44100, 3*44100)
	if got := exact(t, blob); math.Abs(got-3.0) > 1e-9 {
		t.Fatalf("ogg duration = %.9f, want 3.000", got)
	}
}

// 负控：改采样率必须改时长；否则说明没有真的读到头里。
func TestOggGranuleScaledByRate(t *testing.T) {
	blob := oggVorbis(48000, 48000)
	if got := exact(t, blob); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("ogg 48k duration = %.9f, want 1.000", got)
	}
}
