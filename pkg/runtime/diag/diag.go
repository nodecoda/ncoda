// Package diag defines the runtime diagnostic vocabulary (PRD D4).
//
// Runtime codes live in their own table (3xxx) and are deliberately
// separate from the compiler's pkg/diag 1xxx/2xxx enum: the
// compiler-side parity gate (TestCodeParity) counts exactly 60 entries and
// must never absorb runtime codes. The runtime table has its own parity
// test (TestRuntimeCodeParity).
package diag

import (
	"errors"
	"fmt"
)

// Code is a runtime diagnostic code. Errors are 3xxx.
type Code int

const (
	// Spec / graph construction errors (compile time of the runtime graph).
	RSpecInvalid            Code = 3001 // spec failed normalization / schema violation
	RUnknownKind            Code = 3002 // node kind not in the IR kind set
	RDanglingRef            Code = 3003 // dataflow ref to an unknown producer node
	RNodeNotRegistered      Code = 3004 // kind has no node builder in the registry
	RKindRejected           Code = 3005 // kind deliberately rejected in v1 (knowledge, user python code)
	RLoopNoExit             Code = 3006 // loop body has no exit junction (cannot wire body END)
	RBranchNoTarget         Code = 3007 // selector/branch port has no declared target
	RUnreachableNode        Code = 3008 // node not reachable from START in the effective graph
	RLoopBackAtTop          Code = 3009 // loop-function-inline-input edge outside a loop container
	RInvalidTrigger         Code = 3010 // trigger-mode conflict (reserved)
	RParallelWithCheckpoint Code = 3011 // parallel loop + checkpoint compile-time rejection (I3.3)
	RExternalMissing        Code = 3012 // referenced external subworkflow cannot be resolved at load time

	// Execution-time errors (2xxx-adjacent range reserved; these mirror
	// compiler semantics where a semantic twin exists).
	RInputMissing            Code = 3020 // a required node input resolved to nil
	RExpressionUnsupported   Code = 3021 // expression construct outside the evaluator subset (M4)
	RExpressionType          Code = 3022 // expression type mismatch (M4)
	RCodeLanguageUnsupported Code = 3023 // user python foreign code — mirrors pkg/diag CODE_LANGUAGE_UNSUPPORTED (1027) semantics
	RBindingMissing          Code = 3024 // tool binding manifest entry missing (M2)
	RInterruptNotResumable   Code = 3025 // resume attempted on a non-resumable run (D2)
	RNodeError               Code = 3026 // node implementation failure (wrapped)
	RCodeRuntimeError        Code = 3027 // user python execution failed (exception / non-zero exit, M17)
	RCodeTimeout             Code = 3028 // user python execution exceeded the timeout (M17)
	RCodeModuleUnresolved    Code = 3029 // user javascript imports a module the runtime does not provide (load-time)
	RCodeSyntax              Code = 3031 // user foreign code does not parse under the declared language (load-time, compile-only)

	// Version carriers (PRD §4.7): the artifact sidecar declares an execution
	// semantics major this runtime does not implement (or a malformed string).
	// The name is the CLI-visible token RSEMANTICS_VERSION_UNSUPPORTED.
	RSemanticsVersionUnsupported Code = 3030
)

// names mirrors compiler-side diag names extraction; the parity test
// asserts every const above is present exactly once with a 3xxx value.
var names = map[Code]string{
	RSpecInvalid: "RSPEC_INVALID", RUnknownKind: "RUNKNOWN_KIND",
	RDanglingRef: "RDANGLING_REF", RNodeNotRegistered: "RNODE_NOT_REGISTERED",
	RKindRejected: "RKIND_REJECTED", RLoopNoExit: "RLOOP_NO_EXIT",
	RBranchNoTarget: "RBRANCH_NO_TARGET", RUnreachableNode: "RUNREACHABLE_NODE",
	RLoopBackAtTop: "RLOOP_BACK_AT_TOP", RInvalidTrigger: "RINVALID_TRIGGER",
	RParallelWithCheckpoint: "RPARALLEL_WITH_CHECKPOINT",
	RExternalMissing:        "REXTERNAL_MISSING",
	RInputMissing:           "RINPUT_MISSING", RExpressionUnsupported: "REXPRESSION_UNSUPPORTED",
	RExpressionType: "REXPRESSION_TYPE", RCodeLanguageUnsupported: "RCODE_LANGUAGE_UNSUPPORTED",
	RBindingMissing: "RBINDING_MISSING", RInterruptNotResumable: "RINTERRUPT_NOT_RESUMABLE",
	RNodeError:        "RNODE_ERROR",
	RCodeRuntimeError: "RCODE_RUNTIME_ERROR", RCodeTimeout: "RCODE_TIMEOUT",
	RCodeModuleUnresolved:        "RCODE_MODULE_UNRESOLVED",
	RCodeSyntax:                  "RCODE_SYNTAX",
	RSemanticsVersionUnsupported: "RSEMANTICS_VERSION_UNSUPPORTED",
}

// String returns the symbolic name of the code.
func (c Code) String() string {
	if s, ok := names[c]; ok {
		return s
	}
	return fmt.Sprintf("RUNTIME_CODE_%d", int(c))
}

// IsError reports whether the code is an error-severity diagnostic
// (runtime codes are errors only; reserved ranges may add warnings later).
func (c Code) IsError() bool { return c >= 3000 && c < 4000 }

// Error is a runtime diagnostic: code + message.
type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New builds a runtime diagnostic error.
func New(code Code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf extracts the runtime Code from an error (0 when not a runtime error).
// errors.As unwraps %w chains (Registry.Build wraps builder errors), so
// callers get the coded diagnostic through any wrapping.
func CodeOf(err error) Code {
	if err == nil {
		return 0
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}
