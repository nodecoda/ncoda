// Package version is the single source of truth for the open-core product
// version. Release builds inject it with
//
//	-ldflags "-X github.com/nodecoda/ncoda/pkg/version.Version=vX.Y.Z \
//	          -X github.com/nodecoda/ncoda/pkg/version.Commit=<sha>"
//
// Keep this dimension separate from the wire/contract version in
// pkg/compiler (`CompilerVersion`, currently 0.4.0): the product version
// follows releases, the contract version is frozen with the JSON schema.
package version

// Version is the product version reported by `nclang-compile --version`.
// The default "dev" marks a local build that was not stamped at link time.
var Version = "dev"

// Commit is the short VCS revision stamped into release builds (optional).
var Commit = ""
