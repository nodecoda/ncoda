package tool

// P3.5 §5 绑定随包: PackageBindingFiles resolves import declarations against
// search paths and returns the sibling binding file "<provider>.<target>.yaml"
// for each resolved package. Resolution order is identical to
// LoadCapabilityPackages (first search path containing the header wins), and
// a package without a binding file is skipped.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeBinding writes "<provider>.<target>.yaml" into dir.
func writeBinding(t *testing.T, dir, provider, target, body string) {
	t.Helper()
	path := filepath.Join(dir, provider+"."+target+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func TestPackageBindingFilesResolvesSiblingBindings(t *testing.T) {
	dir := t.TempDir()
	writeCapability(t, dir, "tts", simpleHeader)
	writeBinding(t, dir, "tts", "coze", "version: v1\nbindings: []\n")
	writeCapability(t, dir, "video_parse", "function parse(any url) -> { string title; } {}\n")
	writeBinding(t, dir, "video_parse", "acme", "version: v1\nbindings: []\n")

	files, err := PackageBindingFiles([]string{dir}, []string{"tts", "video_parse"}, "coze")
	if err != nil {
		t.Fatalf("PackageBindingFiles: %v", err)
	}
	want := []string{filepath.Join(dir, "tts.coze.yaml")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v (the reference target binding must be skipped for target coze)", files, want)
	}
}

func TestPackageBindingFilesSkipsMissingBindingOrHeader(t *testing.T) {
	dir := t.TempDir()
	// header without a sibling binding
	writeCapability(t, dir, "tts", simpleHeader)
	// binding without a header
	writeBinding(t, dir, "orphan", "coze", "version: v1\nbindings: []\n")

	files, err := PackageBindingFiles([]string{dir}, []string{"tts", "orphan"}, "coze")
	if err != nil {
		t.Fatalf("PackageBindingFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("files = %v, want none (tts has no binding, orphan has no header)", files)
	}
}

func TestPackageBindingFilesFirstSearchPathWins(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeCapability(t, first, "tts", simpleHeader)
	writeBinding(t, first, "tts", "coze", "version: v1\nbindings: []\n")
	writeCapability(t, second, "tts", simpleHeader)
	writeBinding(t, second, "tts", "coze", "version: v1\nbindings: []\n")

	files, err := PackageBindingFiles([]string{first, second}, []string{"tts"}, "coze")
	if err != nil {
		t.Fatalf("PackageBindingFiles: %v", err)
	}
	want := []string{filepath.Join(first, "tts.coze.yaml")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v (first search path must win)", files, want)
	}
}

func TestPackageBindingFilesDedupesImports(t *testing.T) {
	dir := t.TempDir()
	writeCapability(t, dir, "tts", simpleHeader)
	writeBinding(t, dir, "tts", "coze", "version: v1\nbindings: []\n")

	files, err := PackageBindingFiles([]string{dir}, []string{"tts", "tts"}, "coze")
	if err != nil {
		t.Fatalf("PackageBindingFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %v, want exactly one entry (duplicate imports deduped)", files)
	}
}

func TestPackageBindingFilesEmptyInputs(t *testing.T) {
	dir := t.TempDir()
	writeCapability(t, dir, "tts", simpleHeader)
	writeBinding(t, dir, "tts", "coze", "version: v1\nbindings: []\n")

	for _, tc := range []struct {
		name   string
		paths  []string
		imps   []string
		target string
	}{
		{"no search paths", nil, []string{"tts"}, "coze"},
		{"no imports", []string{dir}, nil, "coze"},
		{"empty target", []string{dir}, []string{"tts"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, err := PackageBindingFiles(tc.paths, tc.imps, tc.target)
			if err != nil {
				t.Fatalf("PackageBindingFiles: %v", err)
			}
			if len(files) != 0 {
				t.Fatalf("files = %v, want none for empty input", files)
			}
		})
	}
}

// TestPackageBindingFilesCanonicalRootFirst 锁定 M6：规范根布局
// （capabilities-ncoda/ 子目录）下兄弟绑定必须被解析——此前 PackageBindingFiles
// 只认平铺 header，规范根下误判"header 不存在"跳过整个 searchPath，
// 与 loadCapabilityPackage 的规范根优先不一致。
func TestPackageBindingFilesCanonicalRootFirst(t *testing.T) {
	dir := t.TempDir()
	capsDir := filepath.Join(dir, CapabilityHeadersDir)
	if err := os.MkdirAll(capsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 规范根：header 在 capabilities-ncoda/，绑定文件仍在根下（绑定随包）
	writeCapabilityAt(t, filepath.Join(capsDir, "tts.ncoda"), simpleHeader)
	writeBinding(t, dir, "tts", "coze", "version: v1\nbindings: []\n")

	files, err := PackageBindingFiles([]string{dir}, []string{"tts"}, "coze")
	if err != nil {
		t.Fatalf("PackageBindingFiles: %v", err)
	}
	want := []string{filepath.Join(dir, "tts.coze.yaml")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v (canonical-root header must resolve sibling binding)", files, want)
	}
}

func writeCapabilityAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}
