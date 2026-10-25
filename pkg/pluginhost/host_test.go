package pluginhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

func headers(t *testing.T) HeaderIndex {
	t.Helper()
	h, err := LoadHeaders([]string{filepath.Join("..", "..", "libraries")})
	if err != nil {
		t.Fatalf("LoadHeaders: %v", err)
	}
	return h
}

func serverPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "server.py"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func baseManifest(t *testing.T, extra string) string {
	t.Helper()
	return fmt.Sprintf(`schema: ncoda.plugin/v1
id: test-python
version: 0.1.0
protocol: ncoda-plugin/1
command: ["python3", %q]
timeout_ms: 5000
%s
libraries:
  - library: array_count
    version: 0.1.0
    fidelity: real
    functions: [array_count]
  - library: common_code_block
    version: 0.1.0
    fidelity: real
    functions: [list_sli_trim, get_one_by_num]
`, serverPath(t), extra)
}

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
}

func TestManifestValidation(t *testing.T) {
	hdrs := headers(t)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"partial without loss", `schema: ncoda.plugin/v1
id: x
command: ["true"]
libraries:
  - library: array_count
    fidelity: partial
    functions: [array_count]
`, "requires a non-empty loss"},
		{"undeclared function", `schema: ncoda.plugin/v1
id: x
command: ["true"]
libraries:
  - library: array_count
    fidelity: real
    functions: [ghost]
`, "not declared in the header"},
		{"unknown library", `schema: ncoda.plugin/v1
id: x
command: ["true"]
libraries:
  - library: no_such_lib
    fidelity: real
    functions: [f]
`, "no capability header"},
		{"duplicate symbol", `schema: ncoda.plugin/v1
id: x
command: ["true"]
libraries:
  - library: array_count
    fidelity: real
    functions: [array_count]
  - library: array_count
    fidelity: real
    functions: [array_count]
`, "declared twice"},
		{"bad schema", "schema: other\nid: x\ncommand: [\"true\"]\nlibraries: []\n", "schema ="},
		{"bad protocol", `schema: ncoda.plugin/v1
id: x
protocol: ncoda-plugin/9
command: ["true"]
libraries:
  - library: array_count
    fidelity: real
    functions: [array_count]
`, "protocol ="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeManifest(t, tc.body)
			_, err := Load(path, hdrs)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want contains %q", err, tc.want)
			}
		})
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, ManifestName), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Discover = %v", got)
	}
}

func startTestHost(t *testing.T, body string) (*Host, *plugin.Manager) {
	t.Helper()
	requirePython(t)
	path := writeManifest(t, body)
	mf, err := Load(path, headers(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	h := New(mf, Options{ArtifactDir: t.TempDir(), RuntimeVersion: "test"})
	m := plugin.New()
	if err := h.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, m
}

func TestRegisterAndInvoke(t *testing.T) {
	_, m := startTestHost(t, baseManifest(t, ""))
	ctx := context.Background()

	got, err := m.Call(ctx, "array_count/array_count", map[string]any{"array": []any{1, 2, 3}})
	if err != nil {
		t.Fatalf("Call alias: %v", err)
	}
	if got.(map[string]any)["count"] != float64(3) {
		t.Fatalf("count = %v", got)
	}
	// 符号名与别名都必须可见：排障与绑定都靠这张表。
	names := m.Names()
	for _, want := range []string{"array_count/array_count", "array_count.array_count"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("Names() missing %q: %v", want, names)
		}
	}

	trimmed, err := m.Call(ctx, "common_code_block/list_sli_trim", map[string]any{"list": []any{" a ", "b"}})
	if err != nil {
		t.Fatalf("Call trim: %v", err)
	}
	if data := trimmed.(map[string]any)["data"].([]any); data[0] != "a" {
		t.Fatalf("trim = %v", trimmed)
	}
}

func TestInvokeErrorClass(t *testing.T) {
	_, m := startTestHost(t, baseManifest(t, ""))
	_, err := m.Call(context.Background(), "array_count/array_count", map[string]any{"__error": true})
	if err == nil || !strings.Contains(err.Error(), "test-error: boom") {
		t.Fatalf("err = %v, want class + message", err)
	}
}

func TestTimeoutKillsAndNextCallRestarts(t *testing.T) {
	_, m := startTestHost(t, baseManifest(t, ""))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.Call(ctx, "array_count/array_count", map[string]any{"__sleep_ms": 5000})
	if err == nil {
		t.Fatal("slow call must time out")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout took %v", time.Since(start))
	}
	// 进程被杀后，下一次调用自动重启。
	got, err := m.Call(context.Background(), "array_count/array_count", map[string]any{"array": []any{1}})
	if err != nil {
		t.Fatalf("restart call: %v", err)
	}
	if got.(map[string]any)["count"] != float64(1) {
		t.Fatalf("restart count = %v", got)
	}
}

func TestCrashThenRestart(t *testing.T) {
	_, m := startTestHost(t, baseManifest(t, ""))
	_, err := m.Call(context.Background(), "array_count/array_count", map[string]any{"__crash": true})
	if err == nil {
		t.Fatal("crashing call must fail")
	}
	got, err := m.Call(context.Background(), "array_count/array_count", map[string]any{"array": []any{1, 2}})
	if err != nil {
		t.Fatalf("after crash: %v", err)
	}
	if got.(map[string]any)["count"] != float64(2) {
		t.Fatalf("after crash count = %v", got)
	}
}

func TestHandshakeMismatchRejected(t *testing.T) {
	requirePython(t)
	path := writeManifest(t, baseManifest(t, "env:\n  TEST_HANDSHAKE: bad"))
	mf, err := Load(path, headers(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	h := New(mf, Options{})
	defer h.Close()
	err = h.Register(plugin.New())
	if err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("Register err = %v, want handshake protocol mismatch", err)
	}
}

// 负控：外部插件不能静默覆盖内建符号。
func TestExternalPluginCannotShadowBuiltin(t *testing.T) {
	requirePython(t)
	body := baseManifest(t, "") + `  - library: datetime
    fidelity: real
    functions: [datetime]
`
	path := writeManifest(t, body)
	mf, err := Load(path, headers(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 服务端没声明 datetime → 握手必须拒绝（不是“静默少注册”）。
	h := New(mf, Options{})
	defer h.Close()
	if err := h.Register(plugin.New()); err == nil {
		t.Fatal("handshake must reject symbols the server does not implement")
	}
}
