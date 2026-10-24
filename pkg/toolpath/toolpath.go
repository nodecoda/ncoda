// Package toolpath resolves capability-package search paths.
//
// It is the open, dependency-free piece of the CLI surface: closed tools
// (coze-decompile) and the open compiler CLI share exactly one rule for
// where `import` headers are looked up, without pulling the runtime in.
package toolpath

import (
	"os"
	"path/filepath"
)

// EnvName is the environment variable holding extra search roots
// (colon-separated, like CPATH / PYTHONPATH).
const EnvName = "NCLANG_TOOL_PATH"

// Resolve assembles the final tool search path list for capability packages
// and central binding auto-discovery, mirroring the C header-search model:
//
//  1. CLI --tool-path entries (highest priority, in order)
//  2. NCLANG_TOOL_PATH environment variable (colon-separated; empty entries
//     ignored)
//  3. `<dir of the running executable>/libraries` (the public capability
//     headers shipped next to the distribution's binaries; appended only when
//     it exists)
//  4. user default directory ~/.nodecoda/tools (appended only when it
//     exists — the "site-packages" analog)
//
// Within a layer, first-match-wins ordering is preserved; the compiler
// resolves each provider by scanning the returned list in order.
// getenv/homeDir are injected for testability.
func Resolve(flagPaths []string, getenv func(string) string, homeDir func() (string, error)) []string {
	return ResolveWith(flagPaths, getenv, homeDir, os.Executable)
}

// ResolveWith is Resolve with the executable lookup injected. Tests use it to
// prove the libraries/ layer without depending on where the test binary lives.
func ResolveWith(flagPaths []string, getenv func(string) string, homeDir func() (string, error),
	executable func() (string, error)) []string {
	paths := append([]string{}, flagPaths...)
	if env := getenv(EnvName); env != "" {
		for _, p := range filepath.SplitList(env) {
			if p != "" {
				paths = append(paths, p)
			}
		}
	}
	if exe, err := executable(); err == nil && exe != "" {
		if dir := filepath.Join(filepath.Dir(exe), "libraries"); DirExists(dir) {
			paths = append(paths, dir)
		}
	}
	if home, err := homeDir(); err == nil && home != "" {
		if userDir := filepath.Join(home, ".nodecoda", "tools"); DirExists(userDir) {
			paths = append(paths, userDir)
		}
	}
	return paths
}

// DirExists reports whether path exists and is a directory (best-effort).
func DirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
