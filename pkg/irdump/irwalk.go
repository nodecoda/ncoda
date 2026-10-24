package irdump

import "github.com/nodecoda/ncoda/pkg/ir"

// WalkNodes walks every semantic node reachable from the structured IR tree.
// It is a deliberately small, local walker: the language IR keeps the
// statement tree authoritative and does not export a generic visitor yet
// (pkg/ir.walkStmts is internal). When that visitor lands this function
// becomes its thin adapter; until then the switch below must cover every
// exported ir.Stmt implementation (guarded by TestWalkNodesCoversAllStmtKinds).
func WalkNodes(g *ir.Graph, fn func(ir.Node)) {
	if g == nil {
		return
	}
	walkBlock(g.Root, fn)
}

func walkBlock(b *ir.Block, fn func(ir.Node)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmt(s, fn)
	}
}

func walkStmt(s ir.Stmt, fn func(ir.Node)) {
	switch st := s.(type) {
	case *ir.NodeStmt:
		if st.Node != nil {
			fn(st.Node)
		}
	case *ir.IfStmt:
		if st.Cond != nil {
			fn(st.Cond)
		}
		walkBlock(st.Then, fn)
		walkBlock(st.Else, fn)
		for _, arm := range st.ChainArms {
			if arm != nil {
				walkBlock(arm.Block, fn)
			}
		}
		if st.Relay != nil {
			fn(st.Relay)
		}
	case *ir.AttemptStmt:
		if st.Op != nil {
			fn(st.Op)
		}
		walkBlock(st.Success, fn)
		walkBlock(st.Failure, fn)
		if st.SuccessEnd != nil {
			fn(st.SuccessEnd)
		}
		if st.FailureEnd != nil {
			fn(st.FailureEnd)
		}
	case *ir.LoopStmt:
		if st.Container != nil {
			fn(st.Container)
		}
		walkBlock(st.Body, fn)
	case *ir.ParallelStmt:
		for _, branch := range st.Branches {
			walkBlock(branch, fn)
		}
	case *ir.QaStmt:
		if st.QA != nil {
			fn(st.QA)
		}
		for _, br := range st.Branches {
			walkBlock(br.Body, fn)
		}
	case *ir.SwitchStmt:
		if st.Switch != nil {
			fn(st.Switch)
		}
		for _, br := range st.Branches {
			walkBlock(br.Body, fn)
		}
	case *ir.ReturnStmt:
		if st.End != nil {
			fn(st.End)
		}
	case *ir.BreakStmt:
		if st.Node != nil {
			fn(st.Node)
		}
	case *ir.ContinueStmt:
		if st.Node != nil {
			fn(st.Node)
		}
	case *ir.InlineStmt:
		walkBlock(st.Body, fn)
	}
}
