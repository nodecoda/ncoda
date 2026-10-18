// Package importns implements the platform-qualified import name space
// (docs/PLATFORM-MECHANISM.md §4-5): an import provider may carry an
// explicit platform qualifier — "coze-biz.web_search" — so the source
// declares which platform a Tool is bound to. The qualifier selects the
// platform binding but never the source-level symbol: the capability ABI
// header (<provider>.ncoda) is platform-neutral and shared, exactly like
// Linux generic headers vs the per-arch multiarch trees.
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
	"strconv"
	"strings"
)

// Platform names are the canonical, single source of truth for the target
// platforms (also used by the foundry tool-chain member that produces
// plugin contracts). Keep in sync with the backend target slugs:
// "coze-studio" is the import name of the open-source coze target
// ("coze" slug), "coze-biz" and "dify" match their slugs.
const (
	PlatformDify       = "dify"
	PlatformCozeStudio = "coze-studio"
	PlatformCozeBiz    = "coze-biz"
)

// PlatformNames are the accepted import qualifiers, in deterministic order.
var PlatformNames = []string{PlatformDify, PlatformCozeStudio, PlatformCozeBiz}

// IsPlatformName reports whether name is a known platform qualifier.
func IsPlatformName(name string) bool {
	for _, p := range PlatformNames {
		if p == name {
			return true
		}
	}
	return false
}

// ErrEmptyProvider is returned when a platform qualifier has no provider
// ("coze-biz.").
var ErrEmptyProvider = errors.New("import provider must not be empty after platform qualifier")

// providerNameRE constrains the bare provider name (the part after a
// platform qualifier, or the whole of a bare import): lowercase slug
// charset, no path separators, no leading dot (a dot at position 0 would
// collide with the platform-qualifier separator and with hidden files).
// The bare name becomes a file name under the canonical tool root
// (capabilities-ncoda/<provider>.ncoda), so anything path-shaped (slash,
// "..", leading dot) would read outside the registry directory — a path
// traversal (review M1). Platforms whose physical provider_id contains
// slashes (dify "langgenius/perplexity/perplexity") must normalize to a
// valid slug at the collector side (see internal/foundry/collect).
var providerNameRE = regexp.MustCompile(`^[a-z0-9_][a-z0-9_.-]*$`)

// ValidProviderName reports whether name is a legal bare provider name
// (slug charset, no path separators, no leading dot, non-empty).
func ValidProviderName(name string) bool { return providerNameRE.MatchString(name) }

// ErrInvalidProvider is returned when a provider name cannot be used as a
// file name under the canonical tool root.
var ErrInvalidProvider = errors.New("invalid import provider name: must match [a-z0-9_][a-z0-9_.-]* (no path separators, no leading dot)")

// ImportPlatformError reports a qualifier that is not a known platform.
type ImportPlatformError struct {
	Provider string
	Platform string
}

func (e *ImportPlatformError) Error() string {
	return "unknown import platform " + strconv.Quote(e.Platform) + " in " + strconv.Quote(e.Provider) +
		" (allowed: " + strings.Join(PlatformNames, ", ") + ")"
}

// Split splits a provider string into an optional platform qualifier and
// the bare provider name. "coze-biz.web_search" → ("coze-biz", "web_search");
// "web_search" → ("", "web_search"). A qualifier that is not a known
// platform name is an error — silently guessing would misroute bindings.
func Split(provider string) (platform, name string, err error) {
	platform, name, found := strings.Cut(provider, ".")
	if !found {
		if !ValidProviderName(provider) {
			return "", "", fmt.Errorf("%w: %q", ErrInvalidProvider, provider)
		}
		return "", provider, nil
	}
	if name == "" {
		return "", "", ErrEmptyProvider
	}
	if !IsPlatformName(platform) {
		return "", "", &ImportPlatformError{Provider: provider, Platform: platform}
	}
	if !ValidProviderName(name) {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidProvider, name)
	}
	return platform, name, nil
}

// PlatformOf returns the platform qualifier of a provider string ("" when
// bare or unknown). Callers that need validation use Split.
func PlatformOf(provider string) string {
	platform, _, err := Split(provider)
	if err != nil {
		return ""
	}
	return platform
}
