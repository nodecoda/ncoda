package localtools

// docread.go — 本地文档读取（宿主环境输入类）。
//
// 边界（显式）：
//   - 只读**本地路径**与 `file://`；http(s) 不联网，返回 REMOTE_URL_UNSUPPORTED；
//   - 只接受文本类文件（file_read / text_file_reader / document_to_text）；
//     二进制（PDF/图片）没有 OCR/解析器，返回 UNSUPPORTED_FILE_TYPE，不返回空串；
//   - xlsx 只读第一个 worksheet、首行表头；共享字符串与内联字符串；
//     合并单元格/公式/多 sheet 未实现（partial，见 bindings.yaml）。
//
// 这些是「环境输入」而不是纯函数：文件由宿主路径决定，故走内建而非进程外插件。

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

const maxDocBytes = 32 << 20 // 32 MiB：超过就报错，不静默截断

func registerDocRead(m *plugin.Manager, opts Options) error {
	plugins := []struct {
		lib string
		p   *plugin.Plugin
	}{
		{"file_read", &plugin.Plugin{
			ID: "local_file_read", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "read", Desc: "读取本地文本文件 {data,log_id,msg,code}",
				Capabilities: []string{"file_read.read"},
				Call:         fileRead,
			}},
		}},
		{"text_file_reader", &plugin.Plugin{
			ID: "local_text_file_reader", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "read_txt", Desc: "读取本地 txt {message,text}",
				Capabilities: []string{"text_file_reader.read_txt"},
				Call:         readTxt,
			}},
		}},
		{"document_to_text", &plugin.Plugin{
			ID: "local_document_to_text", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "convert_file", Desc: "本地文本文件转文本 {detected_type,error,extracted_text,file_name,file_size}",
				Capabilities: []string{"document_to_text.convert_file"},
				Call:         convertFile,
			}},
		}},
		{"document_reader", &plugin.Plugin{
			ID: "local_document_reader", Version: "0.1.0", Type: plugin.PluginCallable,
			Tools: []plugin.Tool{{
				Name: "xlsx_parse", Desc: "本地 xlsx → JSON 行数组字符串 {message}",
				Capabilities: []string{"document_reader.xlsx_parse"},
				Call:         xlsxParse,
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

// resolveLocalPath 把 file<...> 值解析成本地路径；远程 URL 直接判错。
func resolveLocalPath(raw string) (string, error) {
	if raw == "" || raw == "0" {
		return "", fmt.Errorf("MISSING_INPUT: file path is empty")
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("REMOTE_URL_UNSUPPORTED: %q (local runtime does not fetch network files)", raw)
	}
	if strings.HasPrefix(lower, "file://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("INVALID_PATH: %v", err)
		}
		return u.Path, nil
	}
	return raw, nil
}

func readLocalText(raw string) (data []byte, name string, err error) {
	path, err := resolveLocalPath(raw)
	if err != nil {
		return nil, "", err
	}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("FILE_NOT_FOUND: %s", path)
		}
		return nil, "", fmt.Errorf("FILE_UNREADABLE: %v", err)
	}
	if st.IsDir() {
		return nil, "", fmt.Errorf("UNSUPPORTED_FILE_TYPE: %s is a directory", path)
	}
	if st.Size() > maxDocBytes {
		return nil, "", fmt.Errorf("FILE_TOO_LARGE: %s is %d bytes (max %d)", path, st.Size(), maxDocBytes)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("FILE_UNREADABLE: %v", err)
	}
	if looksBinary(b) {
		return nil, "", fmt.Errorf("UNSUPPORTED_FILE_TYPE: %s (no OCR/PDF parser in the local runtime)", filepath.Base(path))
	}
	return b, filepath.Base(path), nil
}

// looksBinary：前 8KB 出现 NUL 视为二进制（文本文件不会有）。
func looksBinary(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	for _, c := range b[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}

func fileRead(_ context.Context, args map[string]any) (any, error) {
	b, name, err := readLocalText(stringArg(args, "url"))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"data":   string(b),
		"log_id": "local:" + name,
		"msg":    "ok",
		"code":   float64(0),
	}, nil
}

func readTxt(_ context.Context, args map[string]any) (any, error) {
	b, _, err := readLocalText(stringArg(args, "txt_url"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"message": "ok", "text": string(b)}, nil
}

func convertFile(_ context.Context, args map[string]any) (any, error) {
	raw := stringArg(args, "file_url")
	b, name, err := readLocalText(raw)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	detected := ext
	if detected == "" {
		detected = "txt"
	}
	return map[string]any{
		"detected_type":  detected,
		"error":          "",
		"extracted_text": string(b),
		"file_name":      name,
		"file_size":      int64(len(b)),
	}, nil
}

// ── xlsx ────────────────────────────────────────────────────────────────────

type xlsxSharedStrings struct {
	Items []struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

type xlsxCell struct {
	Ref  string `xml:"r,attr"`
	Type string `xml:"t,attr"`
	V    string `xml:"v"`
	IS   struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}

type xlsxRow struct {
	Cells []xlsxCell `xml:"c"`
}

type xlsxSheet struct {
	Rows []xlsxRow `xml:"sheetData>row"`
}

func colIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
	}
	return n - 1
}

func xlsxParse(_ context.Context, args map[string]any) (any, error) {
	raw := stringArg(args, "url")
	path, err := resolveLocalPath(raw)
	if err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("UNSUPPORTED_FILE_TYPE: %s is not a readable .xlsx (%v)", filepath.Base(path), err)
	}
	defer zr.Close()

	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	var shared []string
	if f, ok := files["xl/sharedStrings.xml"]; ok {
		var ss xlsxSharedStrings
		if err := decodeZipXML(f, &ss); err != nil {
			return nil, err
		}
		for _, si := range ss.Items {
			t := si.Text
			if t == "" {
				var b strings.Builder
				for _, r := range si.Runs {
					b.WriteString(r.Text)
				}
				t = b.String()
			}
			shared = append(shared, t)
		}
	}
	var sheet *zip.File
	if f, ok := files["xl/worksheets/sheet1.xml"]; ok {
		sheet = f
	} else {
		for name, f := range files {
			if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") {
				sheet = f
				break
			}
		}
	}
	if sheet == nil {
		return nil, fmt.Errorf("XLSX_EMPTY: %s has no worksheet", filepath.Base(path))
	}
	var sh xlsxSheet
	if err := decodeZipXML(sheet, &sh); err != nil {
		return nil, err
	}
	if len(sh.Rows) == 0 {
		return nil, fmt.Errorf("XLSX_EMPTY: %s has no rows", filepath.Base(path))
	}
	headers := map[int]string{}
	maxCol := -1
	for i, c := range sh.Rows[0].Cells {
		idx := colIndex(c.Ref)
		if idx < 0 {
			idx = i
		}
		headers[idx] = xlsxCellValue(c, shared)
		if idx > maxCol {
			maxCol = idx
		}
	}
	rows := make([]map[string]string, 0, len(sh.Rows)-1)
	for _, r := range sh.Rows[1:] {
		row := map[string]string{}
		empty := true
		for i, c := range r.Cells {
			idx := colIndex(c.Ref)
			if idx < 0 {
				idx = i
			}
			name, ok := headers[idx]
			if !ok || name == "" {
				continue
			}
			val := xlsxCellValue(c, shared)
			if val != "" {
				empty = false
			}
			row[name] = val
		}
		if !empty {
			rows = append(rows, row)
		}
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("XLSX_PARSE: %v", err)
	}
	return map[string]any{"message": string(b)}, nil
}

func xlsxCellValue(c xlsxCell, shared []string) string {
	switch c.Type {
	case "s":
		idx := 0
		_, _ = fmt.Sscanf(strings.TrimSpace(c.V), "%d", &idx)
		if idx >= 0 && idx < len(shared) {
			return shared[idx]
		}
		return ""
	case "inlineStr":
		if c.IS.Text != "" {
			return c.IS.Text
		}
		var b strings.Builder
		for _, r := range c.IS.Runs {
			b.WriteString(r.Text)
		}
		return b.String()
	default:
		return strings.TrimSpace(c.V)
	}
}

func decodeZipXML(f *zip.File, out any) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("XLSX_PARSE: %v", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxDocBytes))
	if err != nil {
		return fmt.Errorf("XLSX_PARSE: %v", err)
	}
	if err := xml.Unmarshal(b, out); err != nil {
		return fmt.Errorf("XLSX_PARSE: %v", err)
	}
	return nil
}
