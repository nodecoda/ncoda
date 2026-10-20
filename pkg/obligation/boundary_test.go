package obligation_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestLeafPackageBoundary enforces the obligation ← flow import direction:
// pkg/obligation must never depend on pkg/backend/flow (docs v2 §5.4).
func TestLeafPackageBoundary(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasSuffix(dep, "/pkg/backend/flow") || strings.Contains(dep, "/pkg/backend/") {
			t.Fatalf("pkg/obligation must stay a leaf package, found dependency %q", dep)
		}
	}
}
