package nodes

// errpolicy.go — one place that turns a failed node attempt into what the node
// *returns*, driven by `ExceptionConfig.ProcessType` (F3 platform policy).
//
// The platform numbers the three outcomes (oss:internal/canvas/adaptor/
// to_schema.go toExceptionConfig + nodes/.../node_runner.go):
//
//	PT1 (default)  throw      — the failure aborts the workflow;
//	PT2            return     — the operation returns its declared fallback
//	                            payload (`dataOnErr`) and downstream sees a
//	                            normal value;
//	PT3            error branch — control moves to the node's failure branch
//	                            ($branch="fail-branch"), carrying the error
//	                            payload.
//
// ES-046's falsification is "merge return/error-branch ⇒ red": PT1 and PT2
// must stay distinguishable (abort vs value), and PT3 must stay
// distinguishable from both (control transfer). Before P3 the runtime only
// implemented the fail-branch half, and only for http/tool; code/llm dropped
// the policy on the floor, so `with default(...)` on a code node silently
// aborted the flow.
//
// The helper reports (payload, absorbed, err):
//
//	absorbed=true  → return payload, nil
//	absorbed=false → the caller keeps **its own** diagnostic (nothing here
//	                 rewrites execution diagnostics)
//	err != nil     → the declared policy cannot be honoured (hard error;
//	                 fabricating a fallback would turn an absorbed platform
//	                 failure into a made-up success)

import (
	"fmt"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// Process-type numbers (platform wire values; kept in step with
// cozebiz.ErrorBranchProcessType / ErrorDefaultProcessType).
const (
	processTypeThrow      = 1
	processTypeReturn     = 2
	processTypeFailBranch = 3
)

// nodeFailure is one failed attempt as the policy sees it.
type nodeFailure struct {
	Kind      string // spec.Kind*; drives the error_type classification
	Err       error
	Status    int  // http only
	HasStatus bool // whether Status is meaningful
}

// applyErrorPolicy maps a failure onto (payload, absorbed=true) when the
// declared policy absorbs it, or (nil, false, nil) when the workflow must
// fail with the caller's own diagnostic.
func applyErrorPolicy(cfg *spec.ExceptionConfig, nodeID string, f nodeFailure) (map[string]any, bool, error) {
	if cfg == nil {
		return nil, false, nil
	}
	pt := processTypeThrow
	if cfg.ProcessType != nil {
		pt = *cfg.ProcessType
	}
	if cfg.FailBranch {
		pt = processTypeFailBranch
	}
	switch pt {
	case processTypeThrow:
		return nil, false, nil
	case processTypeReturn:
		if len(cfg.DataOnErr) == 0 {
			return nil, false, diag.New(diag.RNodeError,
				"%s node %s: failure policy processType=2 has no carried payload (data_on_err); refusing to fabricate one", f.Kind, nodeID)
		}
		out := make(map[string]any, len(cfg.DataOnErr)+2)
		for _, fld := range cfg.DataOnErr {
			out[fld.Name] = fld.Value
		}
		if f.Kind == spec.KindCode {
			// Platform code nodes expose the failure to the payload itself:
			// downstream can test isSuccess and read errorBody.
			out["isSuccess"] = false
			out["errorBody"] = map[string]any{
				"errorMessage": failureMessage(f),
			}
		}
		return out, true, nil
	case processTypeFailBranch:
		msg := failureMessage(f)
		out := map[string]any{
			MarkerBranch:    "fail-branch",
			"error":         msg,
			"error_type":    f.ErrorType(),
			"error_message": msg,
		}
		if f.HasStatus {
			out["status"] = f.Status
		}
		return out, true, nil
	default:
		return nil, false, diag.New(diag.RNodeError,
			"%s node %s: unknown failure processType %d (want 1/2/3); refusing to guess", f.Kind, nodeID, pt)
	}
}

// failureMessage is the single spelling of a failure's human text.
func failureMessage(f nodeFailure) string {
	if f.Err != nil {
		return f.Err.Error()
	}
	if f.HasStatus {
		return fmt.Sprintf("status %d", f.Status)
	}
	return "node failure"
}

// ErrorType classifies the failure for the error-branch payload.
//
// http keeps the two platform-observed spellings (transport failure vs non-2xx
// response). For code/llm/tool the corpus has **zero** `error_type` literals
// (869 exports: no consumer compares it), so the string is an ncoda-side
// classification — kind-scoped and stable — not a copied platform value; the
// boundary is recorded in the ES-115 ledger rather than presented as evidence.
func (f nodeFailure) ErrorType() string {
	if f.Kind == spec.KindHTTP {
		if f.Err != nil || !f.HasStatus {
			return "network_error"
		}
		return "http_error"
	}
	switch f.Kind {
	case spec.KindCode:
		return "code_error"
	case spec.KindLLM:
		return "llm_error"
	case spec.KindTool:
		return "tool_error"
	default:
		return "node_error"
	}
}
