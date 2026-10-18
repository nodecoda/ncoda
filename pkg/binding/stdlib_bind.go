package binding

// BuildStdlibCalls: the bound-stdlib-call publication, ported from
// binder.py _build_stdlib_calls (:195-244) plus _bound_argument
// (:372-393), _reference_type (:395-418), _bound_value_ref (:430-443) and
// _bind_stdlib_expansion (:258-370).
//
// One bound record is published per CallExpr whose call target is a
// StdlibSpecId and whose argument list satisfies the manifest arity gate;
// the record carries the supplied arguments, the resolved defaults, the
// validated static (compile-time literal) arguments, a fresh result value
// id and the two-step expansion plan the lowering backend executes.
//
// ID allocation (deviation, recorded): Python mints every synthetic
// FunctionId/ScopeId/SymbolId/ValueDefId/AstNodeId from one shared builder
// counter. The Go snapshot exposes no next-id counters, so the Go builder
// mints each id from the snapshot's max+1 in that id space and increments
// within one per-program builder (calls processed in AstNodeID order). The
// minted ids never enter YAML output — lowering only uses them as internal
// BoundValues keys — so there is no byte-level parity requirement, only
// determinism and non-collision (locked by
// TestBuildStdlibCallsKeySequenceDeterministic).
//
// code_roots (binder.py _code_root_symbols :419-428) is ported in
// code_bind.go: BuildCodeSignatures → Snapshot.SetCodeSignatures runs first
// (TypeBindFull), then BuildStdlibCalls resolves ForeignCodeExpr-rooted
// arguments to their ValueDefId exactly like the reference. The ValueDefId
// branch of _reference_type resolves the code-signature port type. (The
// task-25b report records this as the completion of the pre-25 deviation.)
// _bound_default_argument (:246-256) reads stdlib template defaults, and
// stdlib templates are deferred in the Go registry (stdlib_registry.go), so
// every default is "unbound" — the guard behaves exactly like Python for v1,
// where every parameter is required and the defaults range is always empty.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/types"
)

// stdlibCallCollector collects every CallExpr node during one program walk.
type stdlibCallCollector struct {
	calls *[]ast.Node
}

func (c *stdlibCallCollector) Visit(n ast.Node) ast.Visitor {
	if call, ok := n.(*ast.CallExpr); ok {
		*c.calls = append(*c.calls, call)
	}
	return c
}

// BuildStdlibCalls mirrors _build_stdlib_calls (binder.py:195-260): walk
// the program, bind every stdlib call that passes the manifest arity and
// static-argument gates, and publish one BoundStdlibCall per call keyed by
// its AstNodeID. Calls whose target is not a StdlibSpecId, unknown stdlib
// functions, arity-mismatched calls and calls with unresolvable static
// arguments are skipped exactly like the reference.
func BuildStdlibCalls(program *ast.Program, snap *Snapshot) map[AstNodeID]BoundStdlibCall {
	result := map[AstNodeID]BoundStdlibCall{}
	if program == nil || snap == nil {
		return result
	}

	// Python iterates the frozen syntax enumeration (node_id order); Go
	// walks the live tree and sorts the collected calls by AstNodeID so the
	// processing order — and therefore the minted ids — is explicit and
	// deterministic (map iteration must never leak into output).
	var callNodes []ast.Node
	ast.Walk(&stdlibCallCollector{calls: &callNodes}, program)
	sort.Slice(callNodes, func(i, j int) bool {
		idI, _ := snap.NodeIDOf(callNodes[i])
		idJ, _ := snap.NodeIDOf(callNodes[j])
		return idI < idJ
	})
	if len(callNodes) == 0 {
		return result
	}

	// code_roots (binder.py _code_root_symbols :419-428): declaration
	// symbols initialized from a ForeignCodeExpr resolve to the code root
	// ValueDefId instead of the plain symbol id (binder.py:430-443). The
	// reference reads the already-published code_signatures; the Go form
	// requires BuildCodeSignatures → Snapshot.SetCodeSignatures to have run
	// first (TypeBindFull enforces the order). With no code expressions the
	// map is empty and behavior is unchanged from before Task 25b.
	codeRoots := codeRootSymbols(program, snap)
	mint := newStdlibIDMint(snap)
	for _, node := range callNodes {
		call := node.(*ast.CallExpr)
		callID, _ := snap.NodeIDOf(call)
		target, ok := snap.CallTargets().Get(callID)
		if !ok || target.Kind != CallStdlib {
			continue
		}
		// target.value.split(".") → [std, version, name]; malformed paths
		// fail the get_stdlib_function lookup exactly like Python's
		// KeyError/ValueError and are skipped.
		parts := strings.Split(target.Name, ".")
		if len(parts) != 3 {
			continue
		}
		spec, err := GetStdlibFunction(parts[1], parts[2])
		if err != nil {
			continue
		}
		args := call.Args()
		requiredCount := 0
		for _, parameter := range spec.Parameters {
			if parameter.Required {
				requiredCount++
			}
		}
		if !(requiredCount <= len(args) && len(args) <= len(spec.Parameters)) {
			continue
		}

		supplied := make([]BoundArgument, 0, len(args))
		for _, argument := range args {
			supplied = append(supplied, boundArgument(argument, snap, codeRoots))
		}
		// v1 has no defaults (all parameters required); the guard stays for
		// reference parity: any missing default means the call is unbound.
		defaultArgs := make([]BoundArgument, 0, len(spec.Parameters)-len(args))
		anyNilDefault := false
		for index := len(args); index < len(spec.Parameters); index++ {
			defaultArgument, ok := boundDefaultArgument(spec, index)
			if !ok {
				anyNilDefault = true
				break
			}
			defaultArgs = append(defaultArgs, defaultArgument)
		}
		if anyNilDefault {
			continue
		}

		resolved := make([]BoundArgument, 0, len(supplied)+len(defaultArgs))
		resolved = append(resolved, supplied...)
		resolved = append(resolved, defaultArgs...)

		// validated_static_args: collect the compile-time literal values of
		// every static parameter (binder.py:222-229). A static parameter
		// without a literal (e.g. a variable reference) leaves the call
		// unbound.
		staticArgs := map[string]any{}
		for index := range resolved {
			argument := &resolved[index]
			parameter := spec.Parameters[index]
			if parameter.Static && argument.Literal != nil {
				staticArgs[parameter.Name] = argument.Literal.Value
			}
		}
		missingStatic := false
		for _, parameter := range spec.Parameters {
			if parameter.Static {
				if _, ok := staticArgs[parameter.Name]; !ok {
					missingStatic = true
					break
				}
			}
		}
		if missingStatic {
			continue
		}

		resultID := mint.value()
		dynamicArgs := make([]BoundArgument, 0, len(resolved))
		for index := range resolved {
			if !spec.Parameters[index].Static {
				dynamicArgs = append(dynamicArgs, resolved[index])
			}
		}
		expansion := bindStdlibExpansion(spec, staticArgs, dynamicArgs, resultID, mint)
		result[callID] = BoundStdlibCall{
			SpecID:              spec.QualifiedName(),
			BoundArgs:           supplied,
			ResolvedDefaults:    defaultArgs,
			ValidatedStaticArgs: staticArgs,
			ResultValueID:       resultID,
			Expansion:           expansion,
		}
	}
	return result
}

// boundDefaultArgument mirrors _bound_default_argument (binder.py:248-260).
// The Go registry defers stdlib templates (stdlib_registry.go), so no
// default value can be produced and every default is "unbound"; the call
// site treats any unbound default as "skip the call". v1 has no defaults
// (every parameter is required), so this is unreachable for the v1 manifest
// and only the guard semantics are ported.
func boundDefaultArgument(spec *StdlibFunctionSpec, index int) (BoundArgument, bool) {
	_ = spec
	_ = index
	return BoundArgument{}, false
}

// boundArgument mirrors _bound_argument (binder.py:332-350): a bound value
// reference when the expression is an identifier/field chain, otherwise a
// literal argument typed by the literal kind (string/int/float/bool/null);
// any other expression binds a nil literal with the Any type.
func boundArgument(node ast.Expr, snap *Snapshot, codeRoots map[SymbolID]ValueDefID) BoundArgument {
	if reference := boundValueRef(node, snap, codeRoots); reference != nil {
		return ReferenceArgument(*reference, referenceType(reference, snap))
	}
	switch e := node.(type) {
	case *ast.StringLit:
		return LiteralArgument(BoundLiteral{Value: e.Value()}, types.StringType())
	case *ast.IntLit:
		return LiteralArgument(BoundLiteral{Value: e.Value()}, types.IntType())
	case *ast.FloatLit:
		return LiteralArgument(BoundLiteral{Value: e.Value()}, types.FloatType())
	case *ast.BoolLit:
		return LiteralArgument(BoundLiteral{Value: e.Value()}, types.BoolType())
	case *ast.NullLit:
		return LiteralArgument(BoundLiteral{Value: nil}, types.NullType())
	}
	return LiteralArgument(BoundLiteral{Value: nil}, types.AnyType())
}

// boundValueRef lives in code_bind.go (boundValueRef mirrors _bound_value_ref
// binder.py:430-443, including the code_roots ValueDefId branch).

// referenceType mirrors _reference_type (binder.py:352-373): a SymbolId
// reference starts from the declared symbol type; a ValueDefId reference
// starts from the matching code-signature port type (Any when the id is a
// root value, which has no physical port — the Python next(...) default).
// Both branches then walk the reference's field path (ENUM .value → string,
// non-record → unchanged, record → field type with an Any default for
// missing fields).
func referenceType(reference *BoundValueRef, snap *Snapshot) *types.Descriptor {
	var result *types.Descriptor
	if reference.Value.Kind == BoundValueIDDef {
		result = types.AnyType()
		snap.CodeSignatures().Range(func(_ AstNodeID, signature BoundCodeSignature) bool {
			for _, port := range signature.PhysicalPorts {
				if port.ValueID == reference.Value.ValueDef {
					result = port.TypeInfo
					return false
				}
			}
			return true
		})
	} else {
		decl, found := snap.Declarations().Get(reference.Value.SymbolID)
		if !found {
			// Python raises KeyError (symbol_declarations[value], binder.py:397);
			// the Go analogue is a panic, following the RoMap.Set convention
			// for Python hard-error parity. Unreachable for a validated v1
			// program: every bound reference resolves through
			// reference_symbols, and the scope pass always declares the
			// referenced symbol before it can be referenced.
			panic(fmt.Sprintf("binding: no declaration for bound stdlib reference symbol %d", reference.Value.SymbolID))
		}
		result = decl.Type
	}
	for _, fieldName := range reference.FieldPath {
		if result.Kind == types.Enum && fieldName == "value" {
			result = types.StringType()
			continue
		}
		if result.Kind != types.Record {
			return result
		}
		result = recordFieldOrDefault(result, fieldName)
	}
	return result
}

// recordFieldOrDefault resolves one record field, defaulting to Any when the
// field is absent (Python: params.fields.get(field_name, Any)).
func recordFieldOrDefault(d *types.Descriptor, name string) *types.Descriptor {
	for _, field := range d.Fields {
		if field.Name == name {
			return field.Type
		}
	}
	return types.AnyType()
}

// stdlibIDMint mints per-program synthetic ids above the snapshot maxima in
// each id space, so bound-stdlib records never collide with published ids
// and two runs over the same input mint identical sequences.
type stdlibIDMint struct {
	nextSymbol SymbolID
	nextValue  ValueDefID
	nextAST    AstNodeID
}

func newStdlibIDMint(snap *Snapshot) *stdlibIDMint {
	mint := &stdlibIDMint{}
	snap.Declarations().Range(func(id SymbolID, _ SymbolDecl) bool {
		if id > mint.nextSymbol {
			mint.nextSymbol = id
		}
		return true
	})
	snap.DeclarationSymbols().Range(func(_ AstNodeID, id SymbolID) bool {
		if id > mint.nextSymbol {
			mint.nextSymbol = id
		}
		return true
	})
	snap.ReferenceSymbols().Range(func(_ AstNodeID, id SymbolID) bool {
		if id > mint.nextSymbol {
			mint.nextSymbol = id
		}
		return true
	})
	snap.ConversationVarIDs().Range(func(id SymbolID, _ bool) bool {
		if id > mint.nextSymbol {
			mint.nextSymbol = id
		}
		return true
	})
	snap.CodeSignatures().Range(func(_ AstNodeID, signature BoundCodeSignature) bool {
		if signature.RootValue > mint.nextValue {
			mint.nextValue = signature.RootValue
		}
		for _, port := range signature.PhysicalPorts {
			if port.ValueID > mint.nextValue {
				mint.nextValue = port.ValueID
			}
		}
		return true
	})
	// Minted AstNodeIDs mirror Python's _next_ast; nothing keys the
	// snapshot by them, but keeping them above the enumeration max keeps
	// every id space collision-free by construction.
	snap.AstNodes().Range(func(id AstNodeID, _ NodeInfo) bool {
		if id > mint.nextAST {
			mint.nextAST = id
		}
		return true
	})
	mint.nextSymbol++
	mint.nextValue++
	mint.nextAST++
	return mint
}

func (m *stdlibIDMint) symbol() SymbolID {
	id := m.nextSymbol
	m.nextSymbol++
	return id
}

func (m *stdlibIDMint) value() ValueDefID {
	id := m.nextValue
	m.nextValue++
	return id
}

func (m *stdlibIDMint) astNode() AstNodeID {
	id := m.nextAST
	m.nextAST++
	return id
}

// bindStdlibExpansion mirrors _bind_stdlib_expansion (binder.py:258-370):
// mint dynamic parameter symbols, build the two-step expansion for the two
// v1 stdlib functions (rag_answer → knowledge + llm; fetch_and_summarize →
// http + llm), and return the expansion whose result is the response step's
// result symbol.
func bindStdlibExpansion(spec *StdlibFunctionSpec, staticArgs map[string]any, dynamicArgs []BoundArgument, resultID ValueDefID, mint *stdlibIDMint) *StdlibExpansion {
	dynamicParameters := make([]StdlibParameterSpec, 0, len(spec.Parameters))
	for _, parameter := range spec.Parameters {
		if !parameter.Static {
			dynamicParameters = append(dynamicParameters, parameter)
		}
	}
	parameters := make([]BoundParameter, 0, len(dynamicParameters))
	parameterRefs := make(map[string]BoundValueRef, len(dynamicParameters))
	for _, parameter := range dynamicParameters {
		symbolID := mint.symbol()
		parameters = append(parameters, BoundParameter{
			Name:     parameter.Name,
			TypeInfo: stdlibDescriptorForName(parameter.TypeName),
			SymbolID: symbolID,
		})
		parameterRefs[parameter.Name] = BoundValueRef{Value: SymbolBoundValue(symbolID)}
	}

	step := func(kind string, resultType *types.Descriptor, config map[string]string, inputs map[string]BoundValueRef, messages []StdlibMessage) BoundStdlibStep {
		return BoundStdlibStep{
			NodeID:       mint.astNode(),
			Kind:         kind,
			ResultSymbol: mint.symbol(),
			ResultValue:  mint.value(),
			ResultType:   resultType,
			Config:       config,
			Inputs:       inputs,
			Messages:     messages,
		}
	}

	var context, response BoundStdlibStep
	switch spec.Name {
	case "rag_answer":
		context = step(
			"knowledge",
			types.ArrayType(types.MapType(types.StringType(), types.AnyType())),
			map[string]string{"dataset_ids": fmt.Sprintf("%v", staticArgs["dataset_ids"])},
			map[string]BoundValueRef{"query": parameterRefs["query"]},
			nil,
		)
		response = step(
			"llm",
			types.StringType(),
			map[string]string{"model": fmt.Sprintf("%v", staticArgs["model"])},
			nil,
			[]StdlibMessage{
				{Role: "system", Parts: []StdlibMessagePart{{Text: "Answer using the retrieved context."}}},
				{Role: "user", Parts: []StdlibMessagePart{
					{Text: "Context:\n"},
					{Ref: &BoundValueRef{Value: SymbolBoundValue(context.ResultSymbol)}},
					{Text: "\n\nQuestion:\n"},
					{Ref: &BoundValueRef{Value: parameterRefs["query"].Value}},
				}},
			},
		)
	case "fetch_and_summarize":
		context = step(
			"http",
			types.StringType(),
			map[string]string{"method": "GET", "url": fmt.Sprintf("%v", staticArgs["url"])},
			nil,
			nil,
		)
		response = step(
			"llm",
			types.StringType(),
			map[string]string{"model": fmt.Sprintf("%v", staticArgs["model"])},
			nil,
			[]StdlibMessage{
				{Role: "user", Parts: []StdlibMessagePart{
					{Text: "Summarize this content:\n"},
					{Ref: &BoundValueRef{Value: SymbolBoundValue(context.ResultSymbol)}},
				}},
			},
		)
	default:
		// Unreachable for the v1 manifest (it only carries rag_answer and
		// fetch_and_summarize); mirrors binder.py:327's ValueError hard
		// error exactly like the reference.
		panic(fmt.Sprintf("No bound stdlib expansion for %s", spec.QualifiedName()))
	}

	return &StdlibExpansion{
		Name:       fmt.Sprintf("std_%s_%s", spec.Version, spec.Name),
		Parameters: parameters,
		Arguments:  dynamicArgs,
		ReturnType: spec.ReturnType(),
		Steps:      []BoundStdlibStep{context, response},
		Result:     BoundValueRef{Value: SymbolBoundValue(response.ResultSymbol)},
	}
}
