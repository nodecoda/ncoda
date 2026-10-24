package tool

// Platform-qualified imports: re-exports of pkg/importns so binding
// resolution stays under the tool package surface. The qualifier selects the
// platform binding, never the source-level symbol — capability headers are
// platform-neutral and shared.
//
// The qualifier is validated by shape only. Which platforms exist is an
// adapter question (ASSET-BOUNDARY D8), answered at build time.

import "github.com/nodecoda/ncoda/pkg/importns"

// SplitImport splits a provider string into an optional platform qualifier
// and the bare provider name. See importns.Split.
func SplitImport(provider string) (platform, name string, err error) {
	return importns.Split(provider)
}

// PlatformOf returns the platform qualifier of an import provider ("" when
// bare). It never errors: callers that need validation use SplitImport.
func PlatformOf(provider string) string { return importns.PlatformOf(provider) }

// ValidPlatformQualifier reports whether name is a legal qualifier (shape
// only).
func ValidPlatformQualifier(name string) bool { return importns.ValidPlatformQualifier(name) }
