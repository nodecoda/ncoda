// audio.go — 音频时长读取 + 确定性 WAV 生成。
//
// 边界：
//   - WAV 走 RIFF chunk，时长 = data 字节数 / byteRate，精确。
//   - MP3 只做 CBR 估算（首个帧头 bitrate + 字节数），VBR/Xing 不解析
//     ⇒ bindings.yaml 里 audio_duration 是 partial，loss 写明。
//   - http(s) 不联网：返回 REMOTE_URL_UNSUPPORTED，由调用方的 failure 分支处理。
package localtools

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	wavSampleRate = 16000
	wavChannels   = 1
)

// writeWAVTone 生成确定性单声道 16-bit PCM：频率/时长由调用方给定，
// 首尾各 20ms 淡入淡出，避免拼接时爆音。
func writeWAVTone(path string, seconds, freq float64) error {
	if seconds <= 0 {
		seconds = 0.05
	}
	if freq <= 0 {
		freq = 220
	}
	n := int(seconds * wavSampleRate)
	if n < 1 {
		n = 1
	}
	dataSize := n * 2
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(wavChannels))
	binary.Write(&buf, binary.LittleEndian, uint32(wavSampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(wavSampleRate*wavChannels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(wavChannels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataSize))

	fade := wavSampleRate / 50 // 20ms
	for i := 0; i < n; i++ {
		t := float64(i) / wavSampleRate
		v := math.Sin(2*math.Pi*freq*t) * 0.25
		// 二次谐波让音色不至于太“电子”；仍是确定性的。
		v += 0.08 * math.Sin(2*math.Pi*freq*2*t)
		gain := 1.0
		if i < fade {
			gain = float64(i) / float64(fade)
		} else if i > n-fade {
			gain = float64(n-i) / float64(fade)
		}
		sample := int16(v * gain * 32767)
		binary.Write(&buf, binary.LittleEndian, sample)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// audioBytes 解析输入形态：本地路径 / file:// / data:audio/...;base64。
// 节点产物 map（{url|path|data|data_url}）也接受，与 matting 同构。
func audioBytes(raw any) ([]byte, error) {
	s := ""
	switch v := raw.(type) {
	case string:
		s = v
	case map[string]any:
		for _, k := range []string{"url", "path", "data", "data_url", "link", "src"} {
			if candidate, ok := v[k].(string); ok && candidate != "" {
				s = candidate
				break
			}
		}
	case []any:
		if len(v) > 0 {
			return audioBytes(v[0])
		}
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty audio input")
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return nil, fmt.Errorf("REMOTE_URL_UNSUPPORTED: offline implementation does not fetch %s", s)
	case strings.HasPrefix(lower, "file://"):
		u, err := url.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("bad file url: %w", err)
		}
		return os.ReadFile(u.Path)
	case strings.HasPrefix(lower, "data:"):
		comma := strings.IndexByte(s, ',')
		if comma < 0 {
			return nil, fmt.Errorf("bad data url")
		}
		payload := s[comma+1:]
		if strings.Contains(s[:comma], ";base64") {
			return base64.StdEncoding.DecodeString(payload)
		}
		return []byte(payload), nil
	default:
		return os.ReadFile(filepath.Clean(s))
	}
}

// audioDuration 返回 (秒, 容器格式)。只认 WAV 精确、MP3 估算。
func audioDuration(data []byte) (float64, string, error) {
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		d, err := wavDuration(data)
		return d, "wav", err
	}
	if len(data) >= 4 && (string(data[0:4]) == "OggS") {
		return 0, "ogg", fmt.Errorf("UNSUPPORTED_FORMAT: ogg duration not decoded")
	}
	if len(data) >= 4 && (string(data[0:3]) == "ID3" || (data[0] == 0xFF && data[1]&0xE0 == 0xE0)) {
		d, err := mp3Duration(data)
		return d, "mp3", err
	}
	return 0, "unknown", fmt.Errorf("UNSUPPORTED_FORMAT: not wav/mp3")
}

func wavDuration(data []byte) (float64, error) {
	var byteRate uint32
	var dataSize uint32
	haveFmt, haveData := false, false
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		body := off + 8
		if body > len(data) {
			break
		}
		end := body + size
		if end > len(data) {
			end = len(data)
		}
		switch id {
		case "fmt ":
			if size >= 12 && body+12 <= len(data) {
				byteRate = binary.LittleEndian.Uint32(data[body+8 : body+12])
				haveFmt = true
			}
		case "data":
			dataSize = uint32(end - body)
			haveData = true
		}
		off = body + size
		if size%2 == 1 {
			off++ // RIFF padding
		}
	}
	if !haveFmt || !haveData || byteRate == 0 {
		return 0, fmt.Errorf("INVALID_WAV: missing fmt/data chunk")
	}
	return float64(dataSize) / float64(byteRate), nil
}

// mp3Duration：跳过 ID3v2 与尾部 ID3v1，按首个帧头的 bitrate 估算 CBR 时长。
func mp3Duration(data []byte) (float64, error) {
	off := 0
	if len(data) >= 10 && string(data[0:3]) == "ID3" {
		size := int(data[6]&0x7F)<<21 | int(data[7]&0x7F)<<14 | int(data[8]&0x7F)<<7 | int(data[9]&0x7F)
		off = 10 + size
	}
	for off+4 <= len(data) {
		if data[off] == 0xFF && data[off+1]&0xE0 == 0xE0 {
			break
		}
		off++
	}
	if off+4 > len(data) {
		return 0, fmt.Errorf("INVALID_MP3: no frame sync")
	}
	h := data[off : off+4]
	version := (h[1] >> 3) & 0x3
	layer := (h[1] >> 1) & 0x3
	bitrateIdx := int(h[2] >> 4)
	sampleIdx := int((h[2] >> 2) & 0x3)
	if bitrateIdx == 0 || bitrateIdx == 15 || sampleIdx == 3 {
		return 0, fmt.Errorf("INVALID_MP3: bad frame header")
	}
	var bitrates [16]int
	switch {
	case version == 3 && layer == 1: // MPEG1 Layer III
		bitrates = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	case version == 3: // MPEG1 Layer II
		bitrates = [16]int{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0}
	default: // MPEG2/2.5 Layer III
		bitrates = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	}
	kbps := bitrates[bitrateIdx]
	if kbps == 0 {
		return 0, fmt.Errorf("INVALID_MP3: free bitrate")
	}
	size := len(data) - off
	if size > 128 && string(data[len(data)-128:len(data)-125]) == "TAG" {
		size -= 128
	}
	if size < 0 {
		size = 0
	}
	return float64(size) * 8 / (float64(kbps) * 1000), nil
}
