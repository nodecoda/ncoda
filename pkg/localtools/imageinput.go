// imageinput.go — 图像类参数的统一入口（matting / image_scale 共用）。
//
// 只做**读字节**这一件事：路径 / file:// / data: / 节点产物 map。
// 不联网（离线确定性优先）；不解码（解码是调用方的事，错误信息更准确）。
package localtools

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// imageInputMaxBytes 是单个输入文件的上限（64 MiB）。
const imageInputMaxBytes = 64 << 20

// readImageInput 把声明为 file/image 的参数归一成字节。返回的错误文本会原样进
// 调用方可见的 msg / errorBody.errorMessage，因此必须可读、可定位。
func readImageInput(v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return nil, fmt.Errorf("input file is missing (entry file<image> was not provided)")
	case []byte:
		return checkImageSize(x)
	case string:
		return imageBytesFromString(x)
	case map[string]any:
		for _, k := range []string{"data_url", "url", "path", "data"} {
			if got, ok := x[k]; ok && got != nil {
				if b, err := readImageInput(unwrapValue(got)); err == nil {
					return b, nil
				}
			}
		}
		return nil, fmt.Errorf("file object has none of data_url/url/path/data resolvable: keys=%v", imageInputKeys(x))
	default:
		return nil, fmt.Errorf("input has unsupported type %T (want string, []byte, or file object)", v)
	}
}

func imageInputKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out) // 错误消息也要确定性：同一输入同一文本
	return out
}

func imageBytesFromString(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("input file is empty")
	}
	if strings.HasPrefix(s, "data:") {
		i := strings.Index(s, ",")
		if i < 0 {
			return nil, fmt.Errorf("data URL has no ',' separator")
		}
		meta, payload := s[:i], s[i+1:]
		if !strings.Contains(meta, ";base64") {
			return nil, fmt.Errorf("data URL is not base64 (meta=%q)", meta)
		}
		b, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, fmt.Errorf("data URL base64: %v", err)
		}
		return checkImageSize(b)
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return nil, fmt.Errorf("remote URL is not fetched by the offline implementation: %s", s)
	}
	path := s
	if strings.HasPrefix(s, "file://") {
		path = strings.TrimPrefix(s, "file://")
		// file:///abs → /abs
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	return readImageFile(path)
}

func readImageFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", path, err)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("read %s: is a directory", path)
	}
	if st.Size() > imageInputMaxBytes {
		return nil, fmt.Errorf("read %s: %d bytes exceeds limit %d", path, st.Size(), imageInputMaxBytes)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", path, err)
	}
	return checkImageSize(b)
}

func checkImageSize(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("input is empty")
	}
	if len(b) > imageInputMaxBytes {
		return nil, fmt.Errorf("input is %d bytes; limit is %d", len(b), imageInputMaxBytes)
	}
	return b, nil
}

// writePNGAtomic 写临时文件再 rename：半截产物永远不会出现在 artifact 目录里。
func writePNGAtomic(path string, img image.Image) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".png-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := png.Encode(f, img); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
