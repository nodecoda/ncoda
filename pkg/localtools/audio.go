// audio.go — 音频时长读取 + 确定性 WAV 生成。
//
// 边界：
//   - WAV 走 RIFF chunk，时长 = data 字节数 / byteRate，精确。
//   - MP3 逐帧精确：Layer I/II/III、MPEG1/2/2.5、VBR 逐帧累加；首帧带
//     Xing/Info 时以声明帧数为准；不足一帧的尾巴不计。
//   - OGG Vorbis 用末页 granule / 采样率；非 Vorbis 的 Ogg 显式不支持。
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
	if len(data) >= 4 && string(data[0:4]) == "OggS" {
		d, err := oggDuration(data)
		return d, "ogg", err
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

// mp3Frame 描述一个 MP3 帧头解析结果。
type mp3FrameInfo struct {
	length  int
	samples int
	rate    int
	mono    bool
	version int
}

var (
	mp3RatesV1      = [3]int{44100, 48000, 32000}
	mp3RatesV2      = [3]int{22050, 24000, 16000}
	mp3RatesV25     = [3]int{11025, 12000, 8000}
	mp3BitrateV1L1  = [16]int{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0}
	mp3BitrateV1L2  = [16]int{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0}
	mp3BitrateV1L3  = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mp3BitrateV2L1  = [16]int{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0}
	mp3BitrateV2L23 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
)

// parseMP3Frame 解析 pos 处的帧头；不是合法帧（含 free bitrate）返回 false。
func parseMP3Frame(data []byte, pos int) (mp3FrameInfo, bool) {
	if pos+4 > len(data) || data[pos] != 0xFF || data[pos+1]&0xE0 != 0xE0 {
		return mp3FrameInfo{}, false
	}
	version := int(data[pos+1]>>3) & 0x3
	layer := int(data[pos+1]>>1) & 0x3 // 1=III 2=II 3=I 0=reserved
	brIdx := int(data[pos+2] >> 4)
	rateIdx := int(data[pos+2]>>2) & 0x3
	padding := int(data[pos+2]>>1) & 0x1
	if version == 1 || layer == 0 || brIdx == 0 || brIdx == 15 || rateIdx == 3 {
		return mp3FrameInfo{}, false
	}
	var bitrate int
	switch {
	case version == 3 && layer == 3:
		bitrate = mp3BitrateV1L1[brIdx]
	case version == 3 && layer == 2:
		bitrate = mp3BitrateV1L2[brIdx]
	case version == 3:
		bitrate = mp3BitrateV1L3[brIdx]
	case layer == 3:
		bitrate = mp3BitrateV2L1[brIdx]
	default:
		bitrate = mp3BitrateV2L23[brIdx]
	}
	if bitrate == 0 {
		return mp3FrameInfo{}, false
	}
	var rate int
	switch version {
	case 3:
		rate = mp3RatesV1[rateIdx]
	case 2:
		rate = mp3RatesV2[rateIdx]
	default:
		rate = mp3RatesV25[rateIdx]
	}
	var length, samples int
	if layer == 3 { // Layer I
		length = (12*bitrate*1000/rate + padding) * 4
		samples = 384
	} else {
		coef := 144
		samples = 1152
		if version != 3 {
			coef = 72
			if layer == 1 {
				samples = 576
			}
		}
		length = coef*bitrate*1000/rate + padding
	}
	if length < 4 {
		return mp3FrameInfo{}, false
	}
	return mp3FrameInfo{length: length, samples: samples, rate: rate,
		mono: (data[pos+3]>>6)&0x3 == 3, version: version}, true
}

// mp3Duration 逐帧累加采样数：VBR/CBR 同一条路径，Xing/Info 帧数优先。
func mp3Duration(data []byte) (float64, error) {
	pos := 0
	if len(data) >= 10 && string(data[0:3]) == "ID3" {
		size := int(data[6]&0x7F)<<21 | int(data[7]&0x7F)<<14 | int(data[8]&0x7F)<<7 | int(data[9]&0x7F)
		pos = 10 + size
		if data[5]&0x10 != 0 { // ID3v2 footer present
			pos += 10
		}
	}
	first := -1
	var head mp3FrameInfo
	for ; pos+4 <= len(data); pos++ {
		if f, ok := parseMP3Frame(data, pos); ok {
			first, head = pos, f
			break
		}
	}
	if first < 0 {
		return 0, fmt.Errorf("INVALID_MP3: no frame sync")
	}
	// Xing/Info：帧数声明优先（VBR 常把真实帧数写在这里）。
	side := 32
	if head.version != 3 {
		side = 17
	}
	if head.mono {
		if head.version == 3 {
			side = 17
		} else {
			side = 9
		}
	}
	if off := first + 4 + side; off+12 <= len(data) {
		tag := string(data[off : off+4])
		if tag == "Xing" || tag == "Info" {
			if binary.BigEndian.Uint32(data[off+4:off+8])&1 != 0 {
				frames := binary.BigEndian.Uint32(data[off+8 : off+12])
				if frames > 0 {
					return float64(frames) * float64(head.samples) / float64(head.rate), nil
				}
			}
		}
	}
	var seconds float64
	p := first
	for p+4 <= len(data) {
		f, ok := parseMP3Frame(data, p)
		if !ok || p+f.length > len(data) {
			break
		}
		seconds += float64(f.samples) / float64(f.rate)
		p += f.length
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("INVALID_MP3: no complete frame")
	}
	return seconds, nil
}

// oggDuration：Vorbis 识别头取采样率，末个有效 Ogg 页的 granule 取总采样数。
func oggDuration(data []byte) (float64, error) {
	const pageHeader = 27
	if len(data) < pageHeader || string(data[0:4]) != "OggS" {
		return 0, fmt.Errorf("INVALID_OGG: no page header")
	}
	nsegs := int(data[26])
	if pageHeader+nsegs > len(data) {
		return 0, fmt.Errorf("INVALID_OGG: truncated lacing table")
	}
	body := pageHeader + nsegs
	packetLen := 0
	for i := 0; i < nsegs; i++ {
		packetLen += int(data[pageHeader+i])
		if data[pageHeader+i] < 255 {
			break
		}
	}
	if body+packetLen > len(data) || packetLen < 16 {
		return 0, fmt.Errorf("INVALID_OGG: truncated identification header")
	}
	id := data[body : body+packetLen]
	if id[0] != 0x01 || string(id[1:7]) != "vorbis" {
		return 0, fmt.Errorf("UNSUPPORTED_FORMAT: ogg codec is not vorbis")
	}
	rate := int(binary.LittleEndian.Uint32(id[12:16]))
	if rate <= 0 {
		return 0, fmt.Errorf("INVALID_OGG: bad sample rate")
	}
	var granule uint64
	found := false
	pos := 0
	for pos+pageHeader <= len(data) && string(data[pos:pos+4]) == "OggS" {
		n := int(data[pos+26])
		if pos+pageHeader+n > len(data) {
			break
		}
		size := 0
		for i := 0; i < n; i++ {
			size += int(data[pos+pageHeader+i])
		}
		if g := binary.LittleEndian.Uint64(data[pos+6 : pos+14]); g != ^uint64(0) {
			granule, found = g, true
		}
		pos += pageHeader + n + size
	}
	if !found || granule == 0 {
		return 0, fmt.Errorf("INVALID_OGG: no granule position")
	}
	return float64(granule) / float64(rate), nil
}
