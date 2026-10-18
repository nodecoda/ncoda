package tool

// Canonical-root layout constants (docs/TOOL-REGISTRY-LAYOUT.md). A single
// tool root carries the capability registry and the platform binding
// tables:
//
//	<root>/capabilities-ncoda/<provider>.ncoda
//	<root>/bindings/<platform>/<platform>-bindings.yaml
//
// The compiler resolves headers and auto-discovers bindings through these
// paths; the foundry tool-chain member (internal/foundry) *produces* them.
// One source of truth so producer and consumer cannot drift.
const (
	// BindingsDir is the canonical subdirectory holding per-platform
	// binding tables under a tool root.
	BindingsDir = "bindings"
)

// CentralBindingFileName returns the central binding manifest file name
// for a platform (e.g. "coze-biz" → "coze-biz-bindings.yaml").
func CentralBindingFileName(platform string) string {
	return platform + "-bindings.yaml"
}
