package compiler

// language.go — the target-free language lane (L1).
//
// `--target __none__ --emit-ir` runs lexer → parser → binding/name
// resolution → type check → IR build and stops there. No capability model,
// no platform profile, no backend rendering: the only product is the
// canonical ncoda-ir/v1 dump.
//
// 2026-10-25 (ASSET-BOUNDARY P2b): the lane previously existed only in the
// commercial internal/compiler. It is the open repo's own answer to "does
// this program mean something in the language?", so it moved here before
// the reference target adapter leaves.

import (
	"github.com/nodecoda/ncoda/pkg/passes"
)

// TargetLanguageOnly is the target-free language lane. It is a reserved
// name, not a platform: it installs LanguageCarrier (no optional axis is
// claimed carried), so only the language-level passes
// (syntax/semantic/type) judge the source.
const TargetLanguageOnly = "__none__"

// neutralLanguageProfile is the frontend profile for TargetLanguageOnly.
// Capabilities are an empty closed world because the language lane must not
// consult any platform model; a program that needs a capability verdict
// belongs to a target lane.
func neutralLanguageProfile() *passes.TargetProfile {
	p := passes.DefaultTargetProfile()
	p.Name = TargetLanguageOnly
	p.Capabilities = passes.DefaultCapabilityManifest()
	// Nothing target-specific is carried: the language lane answers "does
	// this program mean something?", never "can a product hold it?".
	p.Carrier = passes.LanguageCarrier()
	return p
}
