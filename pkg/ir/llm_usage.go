package ir

import "github.com/nodecoda/ncoda/pkg/llm"

// markLLMEnvelopeUsage derives access-driven envelope assembly requirements
// from semantic refs. It does not add nodes, ports, or target projection data.
func markLLMEnvelopeUsage(graph *Graph, nodes []Node) {
	if graph == nil {
		return
	}
	for _, node := range nodes {
		call, ok := node.(*LLMCall)
		if !ok || call.Result == nil {
			continue
		}
		call.Result.WholeEnvelopeConsumed = blockUsesSemanticRef(graph.Root, call.ID, LLMEnvelopeOutput)
	}
}

// FirstStructuredLLM returns the first LLM call in structured source order
// whose payload requires a closed field-level structured-output proof (any
// non-string, non-open-object type). External profiles use it to fail closed
// until their integrated generation+validation+retry proof is available; the
// local runtime does not. Open-object (object / array<object>) payloads are
// exempt: they only require "is a JSON object" validation, which any target
// can satisfy without a structured-generation capability.
func FirstStructuredLLM(graph *Graph) *LLMCall {
	if graph == nil {
		return nil
	}
	return firstStructuredLLMInBlock(graph.Root)
}

func firstStructuredLLMInBlock(block *Block) *LLMCall {
	if block == nil {
		return nil
	}
	check := func(node Node) *LLMCall {
		call, ok := node.(*LLMCall)
		if !ok || call.Result == nil || call.Result.PayloadSchema == nil {
			return nil
		}
		// String and open-object payloads emit trivially-validatable schemas
		// and therefore need no integrated structured-generation proof
		// (docs/ideas-todo/LLM-OPEN-OBJECT.md §3).
		if call.Result.PayloadSchema.Kind == llm.KindString || call.Result.PayloadSchema.IsOpenObject() {
			return nil
		}
		return call
	}
	for _, stmt := range block.Stmts {
		switch value := stmt.(type) {
		case *NodeStmt:
			if call := check(value.Node); call != nil {
				return call
			}
		case *InlineStmt:
			if call := firstStructuredLLMInBlock(value.Body); call != nil {
				return call
			}
		case *IfStmt:
			if call := firstStructuredLLMInBlock(value.Then); call != nil {
				return call
			}
			if call := firstStructuredLLMInBlock(value.Else); call != nil {
				return call
			}
		case *AttemptStmt:
			if call := check(value.Op); call != nil {
				return call
			}
			if call := firstStructuredLLMInBlock(value.Success); call != nil {
				return call
			}
			if call := firstStructuredLLMInBlock(value.Failure); call != nil {
				return call
			}
		case *LoopStmt:
			if call := firstStructuredLLMInBlock(value.Body); call != nil {
				return call
			}
		case *ParallelStmt:
			for _, branch := range value.Branches {
				if call := firstStructuredLLMInBlock(branch); call != nil {
					return call
				}
			}
		case *QaStmt:
			for _, branch := range value.Branches {
				if call := firstStructuredLLMInBlock(branch.Body); call != nil {
					return call
				}
			}
		case *SwitchStmt:
			for _, branch := range value.Branches {
				if call := firstStructuredLLMInBlock(branch.Body); call != nil {
					return call
				}
			}
		}
	}
	return nil
}

func blockUsesSemanticRef(block *Block, producer, output string) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Stmts {
		switch value := stmt.(type) {
		case *NodeStmt:
			if nodeUsesSemanticRef(value.Node, producer, output) {
				return true
			}
		case *InlineStmt:
			if blockUsesSemanticRef(value.Body, producer, output) {
				return true
			}
		case *ReturnStmt:
			if value.Value != nil && value.Value.nodeID == producer && value.Value.output == output {
				return true
			}
			if value.End != nil && refsUseSemanticRef(value.End.Inputs, producer, output) {
				return true
			}
		case *IfStmt:
			if nodeUsesSemanticRef(value.Cond, producer, output) || blockUsesSemanticRef(value.Then, producer, output) || blockUsesSemanticRef(value.Else, producer, output) {
				return true
			}
		case *AttemptStmt:
			if nodeUsesSemanticRef(value.Op, producer, output) || blockUsesSemanticRef(value.Success, producer, output) || blockUsesSemanticRef(value.Failure, producer, output) {
				return true
			}
		case *ParallelStmt:
			for _, branch := range value.Branches {
				if blockUsesSemanticRef(branch, producer, output) {
					return true
				}
			}
		case *LoopStmt:
			if nodeUsesSemanticRef(value.Container, producer, output) || blockUsesSemanticRef(value.Body, producer, output) {
				return true
			}
		case *SwitchStmt:
			if nodeUsesSemanticRef(value.Switch, producer, output) {
				return true
			}
			for _, branch := range value.Branches {
				if blockUsesSemanticRef(branch.Body, producer, output) {
					return true
				}
			}
		}
	}
	return false
}

func nodeUsesSemanticRef(node Node, producer, output string) bool {
	usesSources := func(sources []Source) bool { return sourcesUseSemanticRef(sources, producer, output) }
	switch value := node.(type) {
	case *End:
		if refsUseSemanticRef(value.Inputs, producer, output) {
			return true
		}
		for _, result := range value.Outputs {
			if result.Ref.Producer == producer && result.Ref.Output == output {
				return true
			}
		}
	case *LLMCall:
		return usesSources(value.Inputs)
	case *HTTPCall:
		return usesSources(value.Inputs)
	case *ToolCall:
		for _, param := range value.Params {
			if param.Ref != nil && param.Ref.BlockID == producer && param.Ref.Name == output {
				return true
			}
		}
	case *KnowledgeCall:
		return usesSources(value.Inputs)
	case *AgentCall:
		return value.Query != nil && value.Query.BlockID == producer && value.Query.Name == output
	case *TextProcess:
		return usesSources(value.Inputs)
	case *CodeRun:
		return usesSources(value.Inputs)
	case *ListOp:
		return usesSources(value.Inputs)
	case *Selector:
		for _, condition := range value.Conditions {
			if operandUsesSemanticRef(condition.Left, producer, output) || operandUsesSemanticRef(condition.Right, producer, output) {
				return true
			}
		}
	case *Aggregate:
		return refsUseSemanticRef(value.AllRefs(), producer, output)
	case *Assign:
		return value.Value.BlockID == producer && value.Value.Name == output
	case *QA:
		return usesSources(value.Inputs)
	case *Loop:
		if usesSources(value.Inputs) || usesSources(value.OutputSources) {
			return true
		}
		for _, source := range value.StateValues {
			if source.BlockID == producer && source.Name == output {
				return true
			}
		}
	case *LoopReturnNode:
		return value.Value.BlockID == producer && value.Value.Name == output
	case *Switch:
		return value.Query.BlockID == producer && value.Query.Name == output
	}
	return false
}

func sourcesUseSemanticRef(values []Source, producer, output string) bool {
	for _, value := range values {
		if value.BlockID == producer && value.Name == output {
			return true
		}
	}
	return false
}

func refsUseSemanticRef(values []Ref, producer, output string) bool {
	for _, value := range values {
		if value.Producer == producer && value.Output == output {
			return true
		}
	}
	return false
}

func operandUsesSemanticRef(value Operand, producer, output string) bool {
	return value.Ref != nil && value.Ref.Producer == producer && value.Ref.Output == output
}
