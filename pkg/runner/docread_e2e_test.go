package runner

// docread_e2e_test.go — 本地文档读取/计时器从 CLI 到字段的端到端判据。
// 边界：不联网；远程 URL 必须 rc=1 且带 REMOTE_URL_UNSUPPORTED，不许空值兜底。

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeXLSXFixture(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z := zip.NewWriter(f)
	defer z.Close()
	ss, err := z.Create("xl/sharedStrings.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ss.Write([]byte(`<sst><si><t>input</t></si><si><t>output</t></si></sst>`))
	sh, err := z.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = sh.Write([]byte(`<worksheet><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
<row r="2"><c r="A2" t="inlineStr"><is><t>q1</t></is></c><c r="B2" t="inlineStr"><is><t>a1</t></is></c></row>
</sheetData></worksheet>`))
}

func TestDocReadAndTimerEndToEnd(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "wenan.txt")
	if err := os.WriteFile(txt, []byte("正文内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	xlsx := filepath.Join(dir, "data.xlsx")
	writeXLSXFixture(t, xlsx)

	src := `@language nodecoda/1
@mode workflow
import "file_read";
import "text_file_reader";
import "document_to_text";
import "document_reader";
import "timer";
import "wait_cat_timer";

function main() -> any {
    let f = file_read.read({ "url": "` + txt + `" });
    let t = text_file_reader.read_txt({ "txt_url": "` + txt + `" });
    let d = document_to_text.convert_file({ "file_url": "` + txt + `" });
    let x = document_reader.xlsx_parse({ "url": "` + xlsx + `" });
    timer.time_wait({ "seconds": 0.001 });
    let w = wait_cat_timer.time_wait({ "seconds": 0.001 });
    return { "data": f.data, "code": f.$code, "text": t.text,
             "extracted": d.extracted_text, "rows": x.message, "value": w.value };
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
		Data      string  `json:"data"`
		Code      float64 `json:"code"`
		Text      string  `json:"text"`
		Extracted string  `json:"extracted"`
		Rows      string  `json:"rows"`
		Value     float64 `json:"value"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if got.Data != "正文内容" || got.Text != "正文内容" || got.Extracted != "正文内容" {
		t.Fatalf("text fields = %+v", got)
	}
	if got.Code != 0 {
		t.Fatalf("file_read.$code = %v", got.Code)
	}
	if !strings.Contains(got.Rows, `"input":"q1"`) || !strings.Contains(got.Rows, `"output":"a1"`) {
		t.Fatalf("xlsx message = %s", got.Rows)
	}
	if got.Value > 0.5 {
		t.Fatalf("timer value = %v (MaxSleep default 1s, waited 0.001s)", got.Value)
	}
}

func TestDocReadRemoteStaysLoud(t *testing.T) {
	dir := t.TempDir()
	src := `@language nodecoda/1
@mode workflow
import "file_read";

function main() -> string {
    let f = file_read.read({ "url": "https://example.com/a.txt" });
    return f.data;
}
`
	srcPath := filepath.Join(dir, "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--local-plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 1 {
		t.Fatalf("rc=%d, want 1; stdout=%s stderr=%s", rc, out, errs)
	}
	if !strings.Contains(errs, "REMOTE_URL_UNSUPPORTED") {
		t.Fatalf("stderr = %s", errs)
	}
}

func TestLocalPluginsSelectiveRegistration(t *testing.T) {
	dir := t.TempDir()
	src := `@language nodecoda/1
@mode workflow
import "document_reader";
import "file_read";

function main() -> string {
    let f = file_read.read({ "url": "x.txt" });
    return f.data;
}
`
	srcPath := filepath.Join(dir, "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--local-plugins=document_reader", "--tool-path", "../../libraries", srcPath)
	if rc != 1 {
		t.Fatalf("rc=%d, want 1; stdout=%s stderr=%s", rc, out, errs)
	}
	if !strings.Contains(errs, "RBINDING_MISSING") || !strings.Contains(errs, "file_read") {
		t.Fatalf("stderr = %s", errs)
	}
}
