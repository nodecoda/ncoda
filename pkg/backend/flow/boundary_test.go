package flow_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// P4b boundary: flow.Project stays a deprecated wrapper and ProjectFromPlan
// stays materialization-internal. New production call sites must migrate to
// Plan.Semantics instead of adding another A-class derivation consumer. The
// allowlists below are the pre-P4b call sites; shrinking them is progress,
// growing them is an architecture regression.
func TestSemanticsMigrationBoundary(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))

	projectAllow := map[string]bool{
		"pkg/tool/registry.go":      true, // capability registry: P4b pending
		"cmd/ncoda-dualrun/main.go": true, // frozen baseline replay
		// runtime lane: 它就是 spec.Build 的服务对象（source → IR → projection →
		// spec.v1 → eino）。P4b 的落点是让 spec.Build 直接吃 Plan.Semantics；
		// 在那之前，这里是唯一的生产入口，且只有这一个。
		"pkg/runner/run.go": true,
	}
	fromPlanAllow := map[string]bool{
		"cmd/ncoda-dualrun/plan_compare.go": true,
	}

	var violations []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".omx", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "pkg/backend/flow/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(data)
		if strings.Contains(src, "flow.Project(") && !projectAllow[rel] {
			violations = append(violations, rel+" calls flow.Project")
		}
		if strings.Contains(src, "ProjectFromPlan(") && !fromPlanAllow[rel] {
			violations = append(violations, rel+" calls ProjectFromPlan")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("P4b boundary violated (migrate to Plan.Semantics; allowlist only shrinks):\n  %s",
			strings.Join(violations, "\n  "))
	}
}
