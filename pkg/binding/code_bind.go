package binding

// BuildCodeSignatures + BuildCodeExpressions: the bound-code publication,
// ported from bindings.py _build_code_signatures (:901-930) and binder.py
// _build_code_expressions (:149-191) plus _code_root_symbols (:419-428) and
// the code_roots branch of _bound_value_ref (:430-443). BoundCodeInput /
// BoundCodeExpr mirror bound_ir.py:59-73.
//
// Assembly order (Python publish_bindings): code_signatures are built from
// the pass-final structural_code_roots FIRST, then code expressions read
// them (code_roots maps declaration symbols to each signature's root value).
// The Go TypeBindFull enforces the same order: BuildCodeSignatures →
// Snapshot.SetCodeSignatures → BuildCodeExpressions (and BuildStdlibCalls,
// whose id minting and _bound_value_ref also read code_signatures).
//
// ID allocation (deviation, recorded): Python's _build_code_signatures mints
// ValueDefIds from a next_value_id counter starting at 1 (bindings.py:906).
// The Go snapshot publishes no ValueDefId space before this task (the value
// ids live exclusively on BoundCodeSignature), so the same "snapshot space
// max+1" convention used by stdlibIDMint yields 0+1 = 1 — identical to
// Python. The minted ids are internal BoundValues keys and never enter YAML
// output, so there is no byte-level parity requirement beyond determinism
// and non-collision (stdlibIDMint continues from the signatures' max; locked
// by TestLowerForeignCodeDeterministic).

import (
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// BoundCodeInput mirrors BoundCodeInput in bound_ir.py:59-63.
type BoundCodeInput struct {
	Name         string
	DeclaredType *types.Descriptor
	Value        BoundValueRef
}

// BoundCodeExpr mirrors BoundCodeExpr in bound_ir.py:66-73. Span is not
// ported: lowering never reads bound_code.span (the reference's
// code_translator.py raises input-level TIEs against the live
// CodeInputBinding spans, which the Go lowering reads off the AST node).
type BoundCodeExpr struct {
	NodeID   AstNodeID
	Inputs   []BoundCodeInput
	Outputs  BoundCodeSignature
	Language string
	Source   string
}

// codeExprCollector collects every ForeignCodeExpr node during one walk.
type codeExprCollector struct {
	exprs *[]ast.Node
}

func (c *codeExprCollector) Visit(n ast.Node) ast.Visitor {
	if _, ok := n.(*ast.ForeignCodeExpr); ok {
		*c.exprs = append(*c.exprs, n)
	}
	return c
}

// varDeclCollector collects every VarDeclStmt node during one walk.
type varDeclCollector struct {
	decls *[]ast.Node
}

func (c *varDeclCollector) Visit(n ast.Node) ast.Visitor {
	if _, ok := n.(*ast.VarDeclStmt); ok {
		*c.decls = append(*c.decls, n)
	}
	return c
}

// foreignCodeExprs returns every ForeignCodeExpr node in AstNodeID order
// (Python iterates the frozen syntax enumeration, which is AstNodeId order;
// Go walks the live tree and sorts by the published ids, following the
// BuildStdlibCalls pattern).
func foreignCodeExprs(program *ast.Program, snap *Snapshot) []*ast.ForeignCodeExpr {
	if program == nil {
		return nil
	}
	var nodes []ast.Node
	ast.Walk(&codeExprCollector{exprs: &nodes}, program)
	sort.Slice(nodes, func(i, j int) bool {
		idI, _ := snap.NodeIDOf(nodes[i])
		idJ, _ := snap.NodeIDOf(nodes[j])
		return idI < idJ
	})
	out := make([]*ast.ForeignCodeExpr, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.(*ast.ForeignCodeExpr))
	}
	return out
}

// snapshotDescriptor resolves a type reference through the snapshot's
// named-type environment (Python type_ref_to_descriptor with
// context.named_types; a missing named type falls back to Any inside
// TypeRefToDescriptor).
func snapshotDescriptor(ref ast.TypeRef, snap *Snapshot) *types.Descriptor {
	return types.TypeRefToDescriptor(ref, func(name string) *types.Descriptor {
		descriptor, _ := snap.NamedTypes().Get(name)
		return descriptor
	})
}

// BuildCodeSignatures mirrors _build_code_signatures (bindings.py:901-930):
// one BoundCodeSignature per ForeignCodeExpr, keyed by its AstNodeID.
// CodeDirectOutput publishes the single root port named "result" with
// NOT_NEEDED materialization; CodeStructuralOutput publishes one physical
// port per field (field_path = [name]) with REQUIRED materialization when
// the node is a structural code root and NOT_NEEDED otherwise; any other
// output contract publishes zero ports with FORBIDDEN materialization.
// structuralCodeRoots is the pass-final table (passes.Context
// .StructuralCodeRoots) and may be nil.
func BuildCodeSignatures(program *ast.Program, snap *Snapshot, structuralCodeRoots map[AstNodeID]bool) map[AstNodeID]BoundCodeSignature {
	signatures := map[AstNodeID]BoundCodeSignature{}
	if program == nil || snap == nil {
		return signatures
	}
	// ValueDefIds start at 1 (Python next_value_id); the snapshot's value
	// space is empty before this task, so max+1 = 1. See the package header.
	nextValueID := ValueDefID(1)
	for _, expr := range foreignCodeExprs(program, snap) {
		nodeID, found := snap.NodeIDOf(expr)
		if !found {
			continue
		}
		rootValue := nextValueID
		nextValueID++
		var ports []CodePort
		materialization := RootForbidden
		if direct, ok := expr.Output().(*ast.CodeDirectOutput); ok {
			ports = append(ports, CodePort{
				ValueID:  rootValue,
				PortName: "result",
				TypeInfo: snapshotDescriptor(direct.TypeRef(), snap),
			})
			materialization = RootNotNeeded
		} else if structural, ok := expr.Output().(*ast.CodeStructuralOutput); ok {
			for _, field := range structural.Fields() {
				fieldValue := nextValueID
				nextValueID++
				ports = append(ports, CodePort{
					ValueID:   fieldValue,
					PortName:  field.Name(),
					TypeInfo:  snapshotDescriptor(field.TypeRef(), snap),
					FieldPath: []string{field.Name()},
				})
			}
			if structuralCodeRoots[nodeID] {
				materialization = RootRequired
			} else {
				materialization = RootNotNeeded
			}
		}
		signatures[nodeID] = BoundCodeSignature{
			RootValue:           rootValue,
			PhysicalPorts:       ports,
			RootMaterialization: materialization,
		}
	}
	return signatures
}

// codeRootSymbols mirrors _code_root_symbols (binder.py:419-428): the
// declaration symbols whose initializer is a ForeignCodeExpr, mapped to the
// signature's root value. BuildCodeExpressions and BuildStdlibCalls consume
// it through _bound_value_ref (a code root reference resolves to the
// ValueDefId instead of the plain symbol id).
func codeRootSymbols(program *ast.Program, snap *Snapshot) map[SymbolID]ValueDefID {
	roots := map[SymbolID]ValueDefID{}
	if program == nil || snap == nil {
		return roots
	}
	var decls []ast.Node
	ast.Walk(&varDeclCollector{decls: &decls}, program)
	for _, n := range decls {
		stmt := n.(*ast.VarDeclStmt)
		fc, ok := stmt.Init().(*ast.ForeignCodeExpr)
		if !ok {
			continue
		}
		stmtID, found := snap.NodeIDOf(stmt)
		if !found {
			continue
		}
		symbolID, found := snap.DeclarationSymbols().Get(stmtID)
		if !found {
			continue
		}
		exprID, found := snap.NodeIDOf(fc)
		if !found {
			continue
		}
		signature, found := snap.CodeSignatures().Get(exprID)
		if !found {
			continue
		}
		roots[symbolID] = signature.RootValue
	}
	return roots
}

// BuildCodeExpressions mirrors _build_code_expressions (binder.py:149-191):
// one BoundCodeExpr per ForeignCodeExpr carrying the resolved input
// references (code roots substituted for code-root symbols), the published
// signature, language and the source's literal text. Python's
// self._binding.code_signatures[node_id] direct index is complete by
// construction (every ForeignCodeExpr got a signature above); a Go miss is
// skipped as a programmer-error guard, mirroring the RoMap convention.
func BuildCodeExpressions(program *ast.Program, snap *Snapshot) map[AstNodeID]*BoundCodeExpr {
	result := map[AstNodeID]*BoundCodeExpr{}
	if program == nil || snap == nil {
		return result
	}
	codeRoots := codeRootSymbols(program, snap)
	for _, expr := range foreignCodeExprs(program, snap) {
		nodeID, found := snap.NodeIDOf(expr)
		if !found {
			continue
		}
		var inputs []BoundCodeInput
		for _, codeInput := range expr.Inputs() {
			if codeInput.Value() == nil {
				continue
			}
			reference := boundValueRef(codeInput.Value(), snap, codeRoots)
			if reference == nil {
				continue
			}
			inputs = append(inputs, BoundCodeInput{
				Name:         codeInput.Name(),
				DeclaredType: snapshotDescriptor(codeInput.TypeRef(), snap),
				Value:        *reference,
			})
		}
		signature, found := snap.CodeSignatures().Get(nodeID)
		if !found {
			continue
		}
		result[nodeID] = &BoundCodeExpr{
			NodeID:   nodeID,
			Inputs:   inputs,
			Outputs:  signature,
			Language: expr.Language(),
			Source:   templateLiteralText(expr.Source()),
		}
	}
	return result
}

// templateLiteralText joins the literal text parts of a template string,
// mirroring binder.py: "".join(part for part in source.parts if isinstance
// (part, str)). Expression parts carry no text and empty literal parts
// contribute nothing, so collecting part.Text over every part is equivalent.
func templateLiteralText(ts *ast.TemplateString) string {
	if ts == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range ts.Parts() {
		builder.WriteString(part.Text)
	}
	return builder.String()
}

// boundValueRef mirrors _bound_value_ref (binder.py:430-443): walk the
// FieldAccessExpr chain collecting field names, require an IdentExpr root
// that resolves through reference_symbols, and return the reversed-path
// reference. A symbol that is a code root (code_roots) resolves to the root
// ValueDefId, exactly like Python's code_roots.get(symbol_id, symbol_id).
// code_roots may be nil (untyped or empty program).
func boundValueRef(node ast.Expr, snap *Snapshot, codeRoots map[SymbolID]ValueDefID) *BoundValueRef {
	var fields []string
	current := node
	for {
		access, ok := current.(*ast.FieldAccessExpr)
		if !ok {
			break
		}
		fields = append(fields, access.FieldName())
		current = access.Obj()
	}
	ident, ok := current.(*ast.IdentExpr)
	if !ok {
		return nil
	}
	identID, found := snap.NodeIDOf(ident)
	if !found {
		return nil
	}
	symbolID, found := snap.ReferenceSymbols().Get(identID)
	if !found {
		return nil
	}
	reversed := make([]string, len(fields))
	for index, name := range fields {
		reversed[len(fields)-1-index] = name
	}
	value := SymbolBoundValue(symbolID)
	if root, ok := codeRoots[symbolID]; ok {
		value = ValueDefBoundValue(root)
	}
	return &BoundValueRef{Value: value, FieldPath: reversed}
}
