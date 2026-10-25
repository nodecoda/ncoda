package localtools

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

func writeText(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeXLSX(t *testing.T, path string, rows string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z := zip.NewWriter(f)
	defer z.Close()
	shared, err := z.Create("xl/sharedStrings.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = shared.Write([]byte(`<?xml version="1.0"?><sst>
<si><t>input</t></si><si><t>output</t></si>
</sst>`))
	sheet, err := z.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = sheet.Write([]byte(`<?xml version="1.0"?><worksheet><sheetData>` + rows + `</sheetData></worksheet>`))
}

func callErr(t *testing.T, m *plugin.Manager, name string, args map[string]any) string {
	t.Helper()
	_, err := m.Call(context.Background(), name, args)
	if err == nil {
		t.Fatalf("Call(%q) must fail", name)
	}
	return err.Error()
}

func TestFileReadLocalText(t *testing.T) {
	m := portableManager(t, Options{})
	path := writeText(t, "a.txt", "hello\nworld")
	got := callAlias(t, m, "file_read/read", map[string]any{"url": path}).(map[string]any)
	if got["data"] != "hello\nworld" || got["msg"] != "ok" {
		t.Fatalf("file_read = %v", got)
	}
	if got["code"] != float64(0) {
		t.Fatalf("code = %v", got["code"])
	}
	// file:// 也接受。
	uri := "file://" + path
	if got := callAlias(t, m, "file_read/read", map[string]any{"url": uri}).(map[string]any); got["data"] != "hello\nworld" {
		t.Fatalf("file:// = %v", got)
	}
}

func TestFileReadRemoteAndBinaryStayLoud(t *testing.T) {
	m := portableManager(t, Options{})
	if msg := callErr(t, m, "file_read/read", map[string]any{"url": "https://example.com/a.txt"}); !strings.Contains(msg, "REMOTE_URL_UNSUPPORTED") {
		t.Fatalf("remote error = %s", msg)
	}
	if msg := callErr(t, m, "file_read/read", map[string]any{"url": "/nope/missing.txt"}); !strings.Contains(msg, "FILE_NOT_FOUND") {
		t.Fatalf("missing error = %s", msg)
	}
	bin := writeText(t, "a.pdf", "\x00\x01\x02")
	if msg := callErr(t, m, "file_read/read", map[string]any{"url": bin}); !strings.Contains(msg, "UNSUPPORTED_FILE_TYPE") {
		t.Fatalf("binary error = %s", msg)
	}
	if msg := callErr(t, m, "file_read/read", map[string]any{}); !strings.Contains(msg, "MISSING_INPUT") {
		t.Fatalf("empty error = %s", msg)
	}
}

func TestTextFileReaderAndConvertFile(t *testing.T) {
	m := portableManager(t, Options{})
	path := writeText(t, "wenan.md", "正文")
	got := callAlias(t, m, "text_file_reader/read_txt", map[string]any{"txt_url": path}).(map[string]any)
	if got["text"] != "正文" {
		t.Fatalf("read_txt = %v", got)
	}
	conv := callAlias(t, m, "document_to_text/convert_file", map[string]any{"file_url": path}).(map[string]any)
	if conv["extracted_text"] != "正文" || conv["file_name"] != "wenan.md" || conv["detected_type"] != "md" {
		t.Fatalf("convert_file = %v", conv)
	}
	if conv["file_size"] != int64(len("正文")) {
		t.Fatalf("file_size = %v", conv["file_size"])
	}
}

func TestXLSXParseRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.xlsx")
	writeXLSX(t, path, `
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
<row r="2"><c r="A2" t="inlineStr"><is><t>q1</t></is></c><c r="B2"><v>7</v></c></row>
<row r="3"><c r="A3" t="inlineStr"><is><t>q2</t></is></c><c r="B3" t="inlineStr"><is><t>a2</t></is></c></row>`)
	m := portableManager(t, Options{})
	got := callAlias(t, m, "document_reader/xlsx_parse", map[string]any{"url": path}).(map[string]any)
	var rows []map[string]string
	if err := json.Unmarshal([]byte(got["message"].(string)), &rows); err != nil {
		t.Fatalf("message is not JSON: %v (%v)", err, got["message"])
	}
	if len(rows) != 2 || rows[0]["input"] != "q1" || rows[0]["output"] != "7" || rows[1]["output"] != "a2" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestXLSXErrorsAreLoud(t *testing.T) {
	m := portableManager(t, Options{})
	if msg := callErr(t, m, "document_reader/xlsx_parse", map[string]any{"url": "https://example.com/a.xlsx"}); !strings.Contains(msg, "REMOTE_URL_UNSUPPORTED") {
		t.Fatalf("remote = %s", msg)
	}
	notzip := writeText(t, "a.xlsx", "not a zip")
	if msg := callErr(t, m, "document_reader/xlsx_parse", map[string]any{"url": notzip}); !strings.Contains(msg, "UNSUPPORTED_FILE_TYPE") {
		t.Fatalf("notzip = %s", msg)
	}
}

func TestDocReadHonorsOnly(t *testing.T) {
	m := registerOnly(t, []string{"document_reader"})
	names := m.Names()
	if !hasName(names, "document_reader/xlsx_parse") {
		t.Fatalf("selected missing: %v", names)
	}
	for _, absent := range []string{"file_read/read", "timer/time_wait"} {
		if hasName(names, absent) {
			t.Fatalf("%q must not be registered: %v", absent, names)
		}
	}
}
