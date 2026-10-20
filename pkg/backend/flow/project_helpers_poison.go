//go:build poison

// project_helpers_poison.go — poison build (TC-28 C2).
//
// Every legacy emitter helper the new solver must not touch is replaced by a
// panicking stub. Building this file with -tags poison and running the
// semantics tests proves independence: Semantics never panics, while the
// legacy Project path does.

package flow

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/ir"
)

func (d *deriveCtx) owePort(sel, port string) {
	panic(fmt.Sprintf("poisoned helper owePort(%q,%q): the new solver must not call the legacy emitter", sel, port))
}

func (d *deriveCtx) drainPorts(dst string) {
	panic(fmt.Sprintf("poisoned helper drainPorts(%q): the new solver must not call the legacy emitter", dst))
}

func (d *deriveCtx) drainPortsFrom(mark int, dst, dstPort string) {
	panic(fmt.Sprintf("poisoned helper drainPortsFrom(%d,%q,%q): the new solver must not call the legacy emitter", mark, dst, dstPort))
}

func (d *deriveCtx) recordSkippedMerge(m *ir.Aggregate) {
	panic(fmt.Sprintf("poisoned helper recordSkippedMerge(%v): the new solver must not call the legacy emitter", m))
}
