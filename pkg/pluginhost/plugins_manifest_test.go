package pluginhost

// 发布仓里 plugins/*/ncoda-plugin.yaml 的一致性判据。
//
// 静态层（不启动进程）：
//   - schema/protocol/type/fidelity/loss 由 Load 校验；
//   - id 与 `<library>.<function>` 在全仓唯一（一个符号只能有一个实现）；
//   - 每个 library 都能在 libraries/<lib>.ncoda 找到 header；
//   - command 里引用的 .py 文件真实存在；
//   - 清单数量只升不降（删掉一个插件会让此判据变红）。
//
// 握手层（有 python3 时）：
//   - 每个插件真正 initialize，声明的符号集合必须与清单完全一致。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

// 7 个真实工具插件 + corpus_stubs（未实现符号的可加载占位）。
// 只升不降：删掉一个插件会让此判据变红。
const minShippedPlugins = 8

func shippedRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "plugins")
}

func shippedHeaders(t *testing.T) HeaderIndex {
	t.Helper()
	h, err := LoadHeaders([]string{filepath.Join("..", "..", "libraries")})
	if err != nil {
		t.Fatalf("LoadHeaders: %v", err)
	}
	return h
}

// validateShippedPlugins 是纯检查，便于用坏输入证明它可判红。
func validateShippedPlugins(manifests []*Manifest) error {
	if len(manifests) < minShippedPlugins {
		return errCount(len(manifests))
	}
	ids := map[string]string{}
	symbols := map[string]string{}
	for _, m := range manifests {
		if strings.TrimSpace(m.Version) == "" {
			return errText("plugin " + m.ID + ": version is required")
		}
		if prev, dup := ids[m.ID]; dup {
			return errText("plugin id " + m.ID + " declared twice (" + prev + " and " + m.Path + ")")
		}
		ids[m.ID] = m.Path
		for _, sym := range m.Symbols() {
			if prev, dup := symbols[sym]; dup {
				return errText("symbol " + sym + " implemented by two plugins (" + prev + " and " + m.ID + ")")
			}
			symbols[sym] = m.ID
		}
	}
	return nil
}

func errText(s string) error { return fmt.Errorf("%s", s) }

func errCount(n int) error {
	return fmt.Errorf("only %d plugin manifests found, want at least %d", n, minShippedPlugins)
}

func loadShipped(t *testing.T) []*Manifest {
	t.Helper()
	paths, err := Discover(shippedRoot(t))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no shipped plugin manifests found")
	}
	hdrs := shippedHeaders(t)
	out := make([]*Manifest, 0, len(paths))
	for _, p := range paths {
		m, err := Load(p, hdrs)
		if err != nil {
			t.Fatalf("Load(%s): %v", p, err)
		}
		out = append(out, m)
	}
	return out
}

func TestShippedPluginManifestsAreConsistent(t *testing.T) {
	manifests := loadShipped(t)
	if err := validateShippedPlugins(manifests); err != nil {
		t.Fatal(err)
	}

	librariesDir := filepath.Join("..", "..", "libraries")
	for _, m := range manifests {
		// header 文件必须真的存在于公开发行目录，而不只是被某个搜索路径扫到。
		for _, lib := range m.Libraries {
			if _, err := os.Stat(filepath.Join(librariesDir, lib.Library+".ncoda")); err != nil {
				t.Fatalf("%s: library %q has no header in libraries/: %v", m.ID, lib.Library, err)
			}
		}
		// command 里以 .py 结尾的参数按清单目录解析，必须存在。
		for _, a := range m.Command[1:] {
			if !strings.HasSuffix(a, ".py") {
				continue
			}
			p := a
			if !filepath.IsAbs(p) {
				p = filepath.Join(m.Dir, p)
			}
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("%s: command argument %q: %v", m.ID, a, err)
			}
		}
	}
}

func TestShippedPluginChecksCanGoRed(t *testing.T) {
	valid := loadShipped(t)

	// 重复 id：把第二个插件的 id 改成第一个。
	dupID := make([]*Manifest, len(valid))
	for i, m := range valid {
		cp := *m
		dupID[i] = &cp
	}
	dupID[1].ID = dupID[0].ID
	if err := validateShippedPlugins(dupID); err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("duplicate id must be rejected, got %v", err)
	}

	// 重复符号：让两个插件都声明同一个 library.function。
	dupSym := make([]*Manifest, len(valid))
	for i, m := range valid {
		cp := *m
		dupSym[i] = &cp
	}
	dupSym[1].Libraries = append([]LibraryDecl{{Library: dupSym[0].Libraries[0].Library,
		Fidelity: "real", Functions: []string{dupSym[0].Libraries[0].Functions[0]}}}, dupSym[1].Libraries...)
	if err := validateShippedPlugins(dupSym); err == nil || !strings.Contains(err.Error(), "two plugins") {
		t.Fatalf("duplicate symbol must be rejected, got %v", err)
	}

	// 清单变少：负控只保留两个。
	if err := validateShippedPlugins(valid[:2]); err == nil || !strings.Contains(err.Error(), "want at least") {
		t.Fatalf("shrinking plugin set must be rejected, got %v", err)
	}
}

func TestShippedPluginHandshakesMatchManifests(t *testing.T) {
	requirePython(t)
	for _, m := range loadShipped(t) {
		t.Run(m.ID, func(t *testing.T) {
			h := New(m, Options{StartTimeout: 10 * time.Second})
			defer h.Close()
			// Register 会启动进程并逐项比对 initialize 的 libraries/functions。
			if err := h.Register(plugin.New()); err != nil {
				t.Fatalf("handshake: %v", err)
			}
		})
	}
}
