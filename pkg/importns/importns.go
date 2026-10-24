// Package importns implements the platform-qualified import name space: an
// import provider may carry an explicit platform qualifier —
// "acme.web_search" — so the source declares which platform binding a Tool
// belongs to. The qualifier selects the binding but never the source-level
// symbol: the capability ABI header (<provider>.ncoda) is platform-neutral
// and shared.
//
// Boundary (ASSET-BOUNDARY D8): this package validates the *shape* of a
// qualifier, never a list of platforms. A closed platform whitelist here
// would make the open language reject real-world source the day it mentions
// a platform the open distribution does not ship — i.e. "the platform is
// unsupported" would surface as a language error, which the project contract
// forbids (target support is UNSUPPORTED_BY_TARGET, rc=3). Whether a
// qualifier names a platform the current target can satisfy is the target
// adapter's question, answered at build time (`--target`), not here.
//
// This package must stay dependency-free (imports only stdlib) so every
// consumer — tool (binding resolution), binding (namespace registration),
// ir (namespace lowering) — shares one source of truth without import
// cycles.
package importns

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrEmptyProvider is returned when a platform qualifier has no provider
// ("acme.").
var ErrEmptyProvider = errors.New("import provider must not be empty after platform qualifier")

// ErrInvalidPlatformQualifier is returned when the qualifier itself is not a
// slug (uppercase, path separators, leading dash/dot).
var ErrInvalidPlatformQualifier = errors.New("invalid import platform qualifier: must match [a-z0-9][a-z0-9-]*")

// providerNameRE constrains the bare provider name (the part after a
// platform qualifier, or the whole of a bare import): lowercase slug
// charset, no path separators, no leading dot (a dot at position 0 would
// collide with the platform-qualifier separator and with hidden files).
// The bare name becomes a file name under the canonical tool root
// (capabilities-ncoda/<provider>.ncoda), so anything path-shaped (slash,
// "..", leading dot) would read outside the registry directory — a path
// traversal (review M1). Platform provider_ids that contain slashes must be
// normalized to a valid slug at the collector side.
var providerNameRE = regexp.MustCompile(`^[a-z0-9_][a-z0-9_.-]*$`)

// platformQualifierRE constrains the platform qualifier: a lowercase slug
// with dashes, no dots (the first dot is the separator), no path separators.
var platformQualifierRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidProviderName reports whether name is a legal bare provider name
// (slug charset, no path separators, no leading dot, non-empty).
func ValidProviderName(name string) bool { return providerNameRE.MatchString(name) }

// ValidPlatformQualifier reports whether name is a legal platform qualifier
// (shape only — this package does not know which platforms exist).
func ValidPlatformQualifier(name string) bool { return platformQualifierRE.MatchString(name) }

// ErrInvalidProvider is returned when a provider name cannot be used as a
// file name under the canonical tool root.
var ErrInvalidProvider = errors.New("invalid import provider name: must match [a-z0-9_][a-z0-9_.-]* (no path separators, no leading dot)")

// Split splits a provider string into an optional platform qualifier and
// the bare provider name. "acme.web_search" → ("acme", "web_search");
// "web_search" → ("", "web_search").
//
// Both parts are validated by shape. The qualifier is returned verbatim and
// is *not* checked against a platform registry: adapters decide at build
// time whether they serve it.
func Split(provider string) (platform, name string, err error) {
	platform, name, found := strings.Cut(provider, ".")
	if !found {
		if !ValidProviderName(provider) {
			return "", "", fmt.Errorf("%w: %q", ErrInvalidProvider, provider)
		}
		return "", provider, nil
	}
	if platform == "" {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidPlatformQualifier, provider)
	}
	if !ValidPlatformQualifier(platform) {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidPlatformQualifier, platform)
	}
	if name == "" {
		return "", "", ErrEmptyProvider
	}
	if !ValidProviderName(name) {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidProvider, name)
	}
	return platform, name, nil
}

// PlatformOf returns the platform qualifier of a provider string ("" when
// bare or malformed). Callers that need validation use Split.
func PlatformOf(provider string) string {
	platform, _, err := Split(provider)
	if err != nil {
		return ""
	}
	return platform
}

// Qualified reports whether a provider string carries a well-formed
// platform qualifier.
func Qualified(provider string) bool { return PlatformOf(provider) != "" }
