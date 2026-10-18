package graphfacts

// Semantic-graph facts: compiler-only semantic proofs consumed by the
// post-lowering verifier, ported field-for-field from the authoritative
// reference 上游 Python 参考实现（nclang）/src/nclang/lang/backends/dify/graph_facts.py
// (225 lines, read-only oracle).
//
// Python frozen dataclasses become plain value structs; tuple fields become
// slices; set fields become map[K]bool; the frozenset[str] parallel-join
// group set becomes map[string]bool keyed by a canonical sorted-member
// join. Fields are exported and treated as immutable after construction
// (the pkg/types convention).

import (
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ConsumedExitProof is one exact control exit and the boundary that
// consumed it, mirroring graph_facts.py:22-29.
type ConsumedExitProof struct {
	Kind             binding.ExitKind
	TargetScopeID    binding.ScopeID
	TailNodeID       *string
	TailSourceHandle string
	ConsumerNodeID   *string
	OwnerContainerID *string
	Terminal         bool
}

// ExtractionFieldFingerprint is one immutable target-validated extraction
// field shape, mirroring graph_facts.py:33-38.
type ExtractionFieldFingerprint struct {
	Name         string
	GraphonType  string
	NodecodaType *types.Descriptor
	Options      []string
}

// ExtractionContractFingerprint is independent extraction authority
// retained after graph emission, mirroring graph_facts.py:42-45.
type ExtractionContractFingerprint struct {
	ProducerNodeID string
	Fields         []ExtractionFieldFingerprint
}

// ClassificationClassFingerprint is one independently retained class branch
// and its physical materializer, mirroring graph_facts.py:49-55.
type ClassificationClassFingerprint struct {
	ClassID       string
	Name          string
	Label         string
	WireValue     string
	AdapterNodeID string
}

// ClassificationContractFingerprint is target-validated classifier
// authority retained after graph emission, mirroring graph_facts.py:59-65.
type ClassificationContractFingerprint struct {
	EnumType            *types.Descriptor
	Classes             []ClassificationClassFingerprint
	AggregatorID        string
	AggregatorSelectors [][]string
	OutputType          string
}

// HumanInputFieldFingerprint mirrors graph_facts.py:69-75.
type HumanInputFieldFingerprint struct {
	Name           string
	Kind           string
	Options        []string
	SourceType     string
	SourceSelector []string
	// SourceValue is the Python union str | tuple[str, ...]; Go: any.
	SourceValue any
}

// HumanInputActionFingerprint mirrors graph_facts.py:77-81.
type HumanInputActionFingerprint struct {
	Member string
	Title  string
	Style  string
}

// HumanInputBranchFingerprint mirrors graph_facts.py:83-88.
type HumanInputBranchFingerprint struct {
	Handle             string
	NodeIDs            []string
	Edges              [][3]string
	SubmittedSelectors [][]string
	// FallthroughTails is the Python tuple[tuple[str | None, str], ...];
	// Go: any for the nullable tail node id.
	FallthroughTails [][2]any
}

// NodeOwnershipFingerprint mirrors graph_facts.py:90-97.
type NodeOwnershipFingerprint struct {
	ParentID      *string
	ZIndex        *int
	IsInIteration bool
	IterationID   string
	IsInLoop      bool
	LoopID        string
}

// HumanInputContinuationPayloadFingerprint mirrors graph_facts.py:99-128.
type HumanInputContinuationPayloadFingerprint struct {
	NodeType           string
	Title              string
	Description        *string
	DataSelected       bool
	RawUsedEmpty       bool
	RawEmpty           bool
	ErrorStrategy      *string
	DefaultValueIsNone bool
	Code               string
	CodeLanguage       string
	// Outputs is the Python tuple[(name, type, has_children), ...].
	Outputs [][3]any
	// Variables is the Python tuple[(variable, path, value_type), ...].
	Variables [][3]any
	// Dependencies is the Python tuple[str, ...].
	Dependencies []string
	// DeclaredOutputs is the Python tuple[(name, TypeDescriptor), ...].
	DeclaredOutputs [][2]any
	// Position is (x, y); PositionAbsolute is (x, y) or nil.
	Position         [2]float64
	Width            *float64
	Height           *float64
	SourcePosition   string
	TargetPosition   string
	PositionAbsolute *[2]float64
	NodeSelected     bool
	Extent           *string
	Draggable        *bool
	Selectable       *bool
	Dragging         *bool
}

// HumanInputContinuationPayload projects only the continuation fields that
// form its canonical wire shape, mirroring graph_facts.py:130-172. Returns
// nil when the node is not a code node (Python: not isinstance(data,
// CodeDataNode)).
func HumanInputContinuationPayload(node *dify.NodeDecl) *HumanInputContinuationPayloadFingerprint {
	data, ok := node.Data.(*dify.CodeDataNode)
	if !ok {
		return nil
	}
	fp := &HumanInputContinuationPayloadFingerprint{
		NodeType:           node.NodeType,
		Title:              data.Title,
		Description:        data.Desc,
		DataSelected:       data.Selected,
		RawUsedEmpty:       data.RawUsed == nil || len(data.RawUsed) == 0,
		RawEmpty:           data.Raw == nil || len(data.Raw) == 0,
		ErrorStrategy:      data.ErrorStrategy,
		DefaultValueIsNone: data.DefaultValue == nil,
		Code:               data.Code,
		CodeLanguage:       data.CodeLanguage,
		Dependencies:       append([]string(nil), data.Dependencies...),
		Position:           [2]float64{node.Position.X, node.Position.Y},
		Width:              node.Width,
		Height:             node.Height,
		SourcePosition:     node.SourcePosition,
		TargetPosition:     node.TargetPosition,
		NodeSelected:       node.Selected,
		Extent:             node.Extent,
		Draggable:          node.Draggable,
		Selectable:         node.Selectable,
		Dragging:           node.Dragging,
	}
	for name, output := range data.Outputs {
		fp.Outputs = append(fp.Outputs, [3]any{name, output.Type, len(output.Children) > 0})
	}
	sort.Slice(fp.Outputs, func(i, j int) bool {
		return fp.Outputs[i][0].(string) < fp.Outputs[j][0].(string)
	})
	for _, variable := range data.Variables {
		fp.Variables = append(fp.Variables, [3]any{variable.Variable, append([]string(nil), variable.Path...), variable.ValueType})
	}
	for name, descriptor := range data.DeclaredOutputContract {
		fp.DeclaredOutputs = append(fp.DeclaredOutputs, [2]any{name, descriptor})
	}
	sort.Slice(fp.DeclaredOutputs, func(i, j int) bool {
		return fp.DeclaredOutputs[i][0].(string) < fp.DeclaredOutputs[j][0].(string)
	})
	if node.PositionAbsolute != nil {
		abs := [2]float64{node.PositionAbsolute.X, node.PositionAbsolute.Y}
		fp.PositionAbsolute = &abs
	}
	return fp
}

// HumanInputContractFingerprint mirrors graph_facts.py:174-182.
type HumanInputContractFingerprint struct {
	Content               string
	Fields                []HumanInputFieldFingerprint
	Actions               []HumanInputActionFingerprint
	TimeoutValue          int
	TimeoutUnit           string
	DeliveryMethods       []any
	Branches              []HumanInputBranchFingerprint
	ContinuationNodeID    *string
	ContinuationPayload   *HumanInputContinuationPayloadFingerprint
	ContinuationOwnership *NodeOwnershipFingerprint
}

// SemanticGraphFacts holds target-independent identities paired with their
// emitted graph witnesses, mirroring graph_facts.py:184-203.
type SemanticGraphFacts struct {
	ContainerScopes                      map[string]binding.ScopeID
	FunctionScopes                       map[binding.ScopeID]bool
	UserFunctionNames                    map[string]bool
	CompilerGeneratedCodeNodes           map[string]bool
	CompilerGeneratedQuestionClassifiers map[string]bool
	CodeNodeProvenance                   map[string]string
	ExtractionContracts                  map[string]ExtractionContractFingerprint
	ClassificationContracts              map[string]ClassificationContractFingerprint
	CompilerGeneratedHumanInputs         map[string]bool
	HumanInputContracts                  map[string]HumanInputContractFingerprint
	ParallelJoinGroups                   map[string]bool
	ConsumedExits                        []ConsumedExitProof
}

// NewSemanticGraphFacts returns empty facts with every container
// initialized, mirroring the Python field(default_factory=...) defaults
// (graph_facts.py:184-203).
func NewSemanticGraphFacts() *SemanticGraphFacts {
	return &SemanticGraphFacts{
		ContainerScopes:                      map[string]binding.ScopeID{},
		FunctionScopes:                       map[binding.ScopeID]bool{},
		UserFunctionNames:                    map[string]bool{},
		CompilerGeneratedCodeNodes:           map[string]bool{},
		CompilerGeneratedQuestionClassifiers: map[string]bool{},
		CodeNodeProvenance:                   map[string]string{},
		ExtractionContracts:                  map[string]ExtractionContractFingerprint{},
		ClassificationContracts:              map[string]ClassificationContractFingerprint{},
		CompilerGeneratedHumanInputs:         map[string]bool{},
		HumanInputContracts:                  map[string]HumanInputContractFingerprint{},
		ParallelJoinGroups:                   map[string]bool{},
		ConsumedExits:                        []ConsumedExitProof{},
	}
}

// JoinGroupKey canonicalizes a parallel join group's member ids into a
// stable map key: the Go analogue of Python's frozenset[str] set members
// (graph_facts.py:201). The sorted member join makes membership and
// equality order-insensitive like the frozen set.
func JoinGroupKey(members []string) string {
	sorted := append([]string(nil), members...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\x00")
}

// SealHumanInputContinuationOwnership freezes final container ownership
// before post-lowering verification, mirroring graph_facts.py:205-225.
// Python rebuilds the frozen contract with dataclasses.replace; the Go
// port mutates the value copy and stores it back — HumanInputContractFingerprint
// is a plain value type, so the map is updated wholesale (no shared-pointer
// aliasing trap).
func (f *SemanticGraphFacts) SealHumanInputContinuationOwnership(graph *dify.GraphNode) {
	nodesByID := make(map[string]*dify.NodeDecl, len(graph.Nodes))
	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		nodesByID[n.ID] = n
	}
	for humanInputID, contract := range f.HumanInputContracts {
		if contract.ContinuationOwnership != nil {
			continue
		}
		var continuation *dify.NodeDecl
		if contract.ContinuationNodeID != nil {
			continuation = nodesByID[*contract.ContinuationNodeID]
		}
		if continuation == nil {
			continue
		}
		contract.ContinuationOwnership = &NodeOwnershipFingerprint{
			ParentID:      continuation.ParentID,
			ZIndex:        continuation.ZIndex,
			IsInIteration: containerFlags(continuation.Data, "IsInIteration") == "true",
			IterationID:   containerFlags(continuation.Data, "IterationID"),
			IsInLoop:      containerFlags(continuation.Data, "IsInLoop") == "true",
			LoopID:        containerFlags(continuation.Data, "LoopID"),
		}
		f.HumanInputContracts[humanInputID] = contract
	}
}

// containerFlags reads one DataNode base container-ownership field via the
// promoted DataNode.ContainerFlags accessor (pkg/backend/dify/nodes.go). Every
// concrete data node embeds DataNode, so the type assertion always resolves;
// a non-data node yields "". The Python source reads continuation.data.<field>
// directly (ast_nodes.py:399-411); this Go helper keeps that read without
// depending on the lowering package's reflection getters.
func containerFlags(data dify.NodeData, field string) string {
	cf, ok := data.(interface {
		ContainerFlags() (bool, string, bool, string)
	})
	if !ok {
		return ""
	}
	isInIteration, iterationID, isInLoop, loopID := cf.ContainerFlags()
	switch field {
	case "IsInIteration":
		if isInIteration {
			return "true"
		}
		return "false"
	case "IterationID":
		return iterationID
	case "IsInLoop":
		if isInLoop {
			return "true"
		}
		return "false"
	case "LoopID":
		return loopID
	}
	return ""
}
