package nodes

// jsimports_test.go — 锁 JS 依赖扫描的两条面：
//   - 语法面：import / export-from / require / 动态 import 都能识别；注释、
//     字符串、模板字面量里的「import」不算依赖；内置与相对路径放行。
//   - 契约面：Build（装载期）对未解析的裸说明符报 RCODE_MODULE_UNRESOLVED，
//     而不是等到节点执行才炸。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestScanJSBareImports(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"default", `import dayjs from 'dayjs';`, []string{"dayjs"}},
		{"named", `import { isObject } from "lodash";`, []string{"lodash"}},
		{"side-effect", `import 'polyfill';`, []string{"polyfill"}},
		{"scoped", `import { x } from '@scope/pkg';`, []string{"@scope/pkg"}},
		{"require", `const _ = require('lodash');`, []string{"lodash"}},
		{"dynamic", `const m = await import('dayjs');`, []string{"dayjs"}},
		{"export-from", `export { x } from 'mod';`, []string{"mod"}},
		{"dedupe", "import a from 'dayjs';\nimport b from 'dayjs';", []string{"dayjs"}},
		{"builtins-and-local", `import fs from 'node:fs';
import path from 'path';
import a from './a';
import b from '../b';
import c from '/abs/c';
const d = require('fs/promises');`, nil},
		{"import-meta", `const u = import.meta.url;`, nil},
		{"comments-strings", `// import a from 'a'
/* require('b') */
const s = "import c from 'c'";
const q = 'require("d")';`, nil},
		{"template", "const t = `import a from 'a'`;\nconst u = `x ${1 + 2}`;", nil},
		{"nested-template", "const t = `a ${`inner ${x}`}`; import real from 'real';", []string{"real"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scanJSBareImports(tc.src); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("scanJSBareImports = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestCodeBuildRejectsUnresolvedJSModule pins the load-time contract: a bare
// import is a dependency error before the node (or the flow) runs.
func TestCodeBuildRejectsUnresolvedJSModule(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node not available; the language gate fires first")
	}
	cfg := &spec.CodeConfig{
		Language: "javascript",
		Code:     "import dayjs from 'dayjs';\nasync function main({ params }) { return { out: 1 }; }",
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: "js1", Kind: spec.KindCode, Config: b}, Deps{})
	if err == nil || diag.CodeOf(err) != diag.RCodeModuleUnresolved {
		t.Fatalf("err = %v, want RCODE_MODULE_UNRESOLVED", err)
	}
	if !strings.Contains(err.Error(), "dayjs") {
		t.Fatalf("err = %v, want the offending module named", err)
	}
}

// TestCodeBuildAllowsBuiltinJSImports is the reverse anchor: builtins and
// relative imports stay legal, so the scan does not over-reject.
func TestCodeBuildAllowsBuiltinJSImports(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node not available")
	}
	cfg := &spec.CodeConfig{
		Language: "javascript",
		Code:     "import fs from 'node:fs';\nconst local = require('./local');\nasync function main({ params }) { return { out: typeof fs.readFileSync }; }",
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: "js2", Kind: spec.KindCode, Config: b}, Deps{}); err != nil {
		t.Fatalf("builtin/relative imports must build: %v", err)
	}
}

func writeFakeModule(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "node_modules", filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+name+`","version":"1.0.0","main":"index.js"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeNodeRootAndMissing(t *testing.T) {
	root := t.TempDir()
	writeFakeModule(t, root, "dayjs", "module.exports = () => 'ok';")
	writeFakeModule(t, root, "@scope/pkg", "module.exports = {};")

	if got := normalizeNodeRoot(filepath.Join(root, "node_modules")); got != root {
		t.Fatalf("normalizeNodeRoot(node_modules) = %q, want %q", got, root)
	}
	if got := normalizeNodeRoot(root); got != root {
		t.Fatalf("normalizeNodeRoot(root) = %q, want %q", got, root)
	}
	if got := missingNodeModules(root, []string{"dayjs", "lodash", "@scope/pkg", "lodash/fp"}); !reflect.DeepEqual(got, []string{"lodash", "lodash/fp"}) {
		t.Fatalf("missingNodeModules = %#v", got)
	}
	if got := missingNodeModules("", []string{"dayjs"}); !reflect.DeepEqual(got, []string{"dayjs"}) {
		t.Fatalf("empty root must report every module, got %#v", got)
	}
}

// TestCodeNodeRunsImportedJSPackage is the end-to-end proof for
// --node-modules: a bare import resolves from the module root and the node
// executes (no real npm install needed).
func TestCodeNodeRunsImportedJSPackage(t *testing.T) {
	if !nodeAvailable() {
		t.Skip("node not available")
	}
	root := t.TempDir()
	writeFakeModule(t, root, "dayjs", "module.exports = () => 'dayjs-ok';")

	cfg := &spec.CodeConfig{
		Language: "javascript",
		Code: `import dayjs from 'dayjs';
async function main({ params }) { return { out: dayjs() }; }`,
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	impl, err := StdRegistry().Build(context.Background(), &spec.NodeSpec{ID: "js3", Kind: spec.KindCode, Config: b}, Deps{NodeModules: root})
	if err != nil {
		t.Fatalf("Build with module root: %v", err)
	}
	out, err := impl.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got, _ := out["out"].(string); got != "dayjs-ok" {
		t.Fatalf("out = %#v, want out=dayjs-ok", out)
	}
}
