package spec

// Kind is the execution-kind spelling. It aliases string so the untyped
// constants below and the runtime switch stay byte-identical; the alias also
// gives adapter-side census tables a named type to target.
type Kind = string

// Kind constants (alias the IR kind strings — the closed semantic set).
const (
	KindStart        = "start"
	KindEnd          = "end"
	KindLLM          = "llm"
	KindHTTP         = "http"
	KindTool         = "tool"
	KindAgent        = "agent"
	KindConversation = "conversation" // @mode advanced-chat 会话历史读取（M13）
	KindKnowledge    = "knowledge"
	KindText         = "text"
	KindListOp       = "list_op"
	KindCode         = "code"
	KindSelector     = "selector"
	KindAggregate    = "aggregate"
	KindAssign       = "assign"
	KindLoopAssign   = "loop_assign"
	KindQA           = "question_answer"
	KindSwitch       = "switch"
	KindOutput       = "output"
	KindLoop         = "loop"
	KindBreak        = "break"
	KindContinue     = "continue"
	KindLoopReturn   = "loop_return"
	KindSubWorkflow  = "sub_workflow"
)

// specKindValues is the closed Kind set in declaration order. It is the
// authority adapter tables and census totals compare against.
var specKindValues = []Kind{
	KindStart, KindEnd, KindLLM, KindHTTP, KindTool, KindAgent, KindConversation,
	KindKnowledge, KindText, KindListOp, KindCode, KindSelector, KindAggregate,
	KindAssign, KindLoopAssign, KindQA, KindSwitch, KindOutput, KindLoop,
	KindBreak, KindContinue, KindLoopReturn, KindSubWorkflow,
}

// AllKinds returns the closed Kind set in declaration order. Callers must not
// mutate the result.
func AllKinds() []Kind {
	out := make([]Kind, len(specKindValues))
	copy(out, specKindValues)
	return out
}
