package spec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestExecSem105PolicyBoundary is ES-105: policy is a projection concern and
// must not leak into pkg/ir.
//
//  1. pkg/ir has no field in the policy deny-list;
//  2. pkg/ir does not import a sidecar loader or the runtime spec loader
//     (the latter is where the sidecar loader is pinned).
//
// The check parses the kernel package with go/ast. It deliberately does not
// use a hand-maintained type list: every struct in every non-test file is
// scanned.
func TestExecSem105PolicyBoundary(t *testing.T) {
	dir, err := goListDir("github.com/nodecoda/ncoda/pkg/ir")
	if err != nil {
		t.Fatalf("locate pkg/ir: %v", err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("pkg/ir dir %s unavailable: %v", dir, err)
	}

	deny := map[string]bool{
		"prefill":           true,
		"postfill":          true,
		"hostfields":        true,
		"perattempttimeout": true,
		"totalbudget":       true,
		"requireddefault":   true,
		"timeoutaxis":       true,
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	structs, files := 0, 0
	var hits []string
	var badImports []string
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			lp := strings.ToLower(p)
			if strings.Contains(lp, "sidecar") ||
				strings.HasSuffix(lp, "/runtime/spec") {
				badImports = append(badImports, name+": "+p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			structs++
			for _, field := range st.Fields.List {
				names := []string{}
				for _, id := range field.Names {
					names = append(names, id.Name)
				}
				if tag := field.Tag; tag != nil {
					if raw, err := strconv.Unquote(tag.Value); err == nil {
						names = append(names, tagValues(raw)...)
					}
				}
				for _, nm := range names {
					if nm == "" {
						continue
					}
					if deny[normalizePolicyName(nm)] {
						hits = append(hits, name+": field "+nm)
					}
				}
			}
			return true
		})
	}

	if files == 0 {
		t.Fatalf("no non-test .go files found in %s (vacuous check)", dir)
	}
	if structs == 0 {
		t.Fatalf("no struct types parsed in %s (vacuous check)", dir)
	}
	if len(hits) > 0 {
		t.Fatalf("pkg/ir carries policy fields (ES-105 deny-list): %v", hits)
	}
	if len(badImports) > 0 {
		t.Fatalf("pkg/ir imports a policy/sidecar home (language pass must not read sidecar): %v", badImports)
	}
	t.Logf("pkg/ir dir: %s", dir)
	t.Logf("scanned %d structs in %d pkg/ir files: no deny-list fields, no sidecar/commercial imports", structs, files)
}

func normalizePolicyName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

// tagValues extracts struct-tag values for json/yaml, dropping options.
func tagValues(raw string) []string {
	var out []string
	for _, part := range strings.Fields(raw) {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := kv[0]
		if key != "json" && key != "yaml" {
			continue
		}
		val, err := strconv.Unquote(kv[1])
		if err != nil {
			continue
		}
		if i := strings.IndexByte(val, ','); i >= 0 {
			val = val[:i]
		}
		if val != "" && val != "-" {
			out = append(out, val)
		}
	}
	return out
}

// goListDir resolves a package directory with the ambient module/workspace
// configuration. Tests are run with GOWORK=<stress workspace> in CI.
func goListDir(importPath string) (string, error) {
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", importPath)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", &listError{path: importPath, err: err, stderr: string(ee.Stderr)}
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type listError struct {
	path   string
	err    error
	stderr string
}

func (e *listError) Error() string {
	return "go list " + e.path + ": " + e.err.Error() + ": " + strings.TrimSpace(e.stderr)
}
