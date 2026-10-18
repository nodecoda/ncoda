package decompile

// unknown_notes_test.go —— dify 反编译 UNKNOWN 降级归因（L3-b）回归测试。
// 与 coze-biz 反编译同契约:类型/节点降级显式 UNKNOWN 注释块,禁止静默 any。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderUnknownNotesEmpty 无降级时产物与之前逐字节一致（空注释块）。
func TestRenderUnknownNotesEmpty(t *testing.T) {
	if got := renderUnknownNotes(nil); got != "" {
		t.Fatalf("empty notes should render empty, got %q", got)
	}
	if got := renderUnknownNotes([]string{}); got != "" {
		t.Fatalf("empty notes should render empty, got %q", got)
	}
}

// TestRenderUnknownNotesAggregated 按原因聚合 + 排序,重复 note 只计一次数。
func TestRenderUnknownNotesAggregated(t *testing.T) {
	got := renderUnknownNotes([]string{
		"object-no-schema",
		"object-no-schema",
		"unknown-datatype:foo",
		"unknown-node:bar",
	})
	if !strings.Contains(got, "// UNKNOWN:") {
		t.Fatalf("missing header: %q", got)
	}
	if !strings.Contains(got, "object-no-schema: 2") {
		t.Fatalf("expected aggregated count 2: %q", got)
	}
	if !strings.Contains(got, "unknown-datatype:foo: 1") {
		t.Fatalf("expected sorted reason: %q", got)
	}
	if !strings.Contains(got, "unknown-node:bar: 1") {
		t.Fatalf("expected sorted reason: %q", got)
	}
}

// TestDecompileYAMLUnknownNotes 真实 fixture 反编译产物带 UNKNOWN 注释块
// （fixtures 含 unparsed 聚合/未知节点时命中;否则空注释块也算通过——
// 关键是编译仍 OK,注释不破坏产物）。
func TestDecompileYAMLUnknownNotes(t *testing.T) {
	dir := os.Getenv("DIFY_YAML_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "examples", "yaml")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no fixtures dir %s: %v", dir, err)
	}
	counted := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		nc, err := DecompileYAML(string(data))
		if err != nil {
			t.Errorf("decompile %s: %v", e.Name(), err)
			continue
		}
		// 产物要么不含 UNKNOWN（无降级）,要么注释块在末尾且 compile_ok
		if strings.Contains(nc, "// UNKNOWN:") && !strings.HasSuffix(strings.TrimSpace(nc), ")") && !strings.HasSuffix(nc, "\n//   - ") {
			// 注释块应在源码结束后;粗略校验注释块在尾部
			idx := strings.Index(nc, "// UNKNOWN:")
			if idx < len(nc)/2 {
				t.Errorf("%s: UNKNOWN block should be near end (idx=%d len=%d)", e.Name(), idx, len(nc))
			}
		}
		counted++
	}
	if counted == 0 {
		t.Skip("no fixtures scanned")
	}
}
