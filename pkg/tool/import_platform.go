package tool

// Platform-qualified imports (docs/PLATFORM-MECHANISM.md §4-5): re-exports
// of pkg/importns so binding resolution stays under the tool package
// surface. The qualifier selects the platform binding, never the
// source-level symbol — capability headers are platform-neutral and shared.

import "github.com/nodecoda/ncoda/pkg/importns"

// PlatformNames are the accepted import qualifiers.
var PlatformNames = importns.PlatformNames

// IsPlatformName reports whether name is a known platform namespace.
func IsPlatformName(name string) bool { return importns.IsPlatformName(name) }

// SplitImport splits a provider string into an optional platform qualifier
// and the bare provider name. See importns.Split.
func SplitImport(provider string) (platform, name string, err error) {
	return importns.Split(provider)
}

// PlatformOf returns the platform qualifier of an import provider ("" when
// bare). It never errors: callers that need validation use SplitImport.
func PlatformOf(provider string) string { return importns.PlatformOf(provider) }

// ImportPlatformError re-exports the unknown-platform error type.
type ImportPlatformError = importns.ImportPlatformError
