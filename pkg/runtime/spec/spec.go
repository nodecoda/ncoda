// Package spec — 执行面子集（PRD D1）。
//
// spec.v1 is the explicit, roundtrip-checkable execution subset of a
// workflow: nodes (kind + per-kind config), edges (ports), loop containers
// (nested children + inner edges) and entry/conv-var declarations. It is
// derived from the IR+Projection at build time and is the ONLY input the
// runtime compile layer may read — any field not present in a config DTO
// is invisible to execution by construction (D1 "已消费字段" registry).
//
// Guarantees:
//   - Normalize: decode every config by kind and re-marshal → canonical
//     bytes (key order stable, unknown fields dropped, schema validated).
//   - Roundtrip gate: Marshal(Normalize(Marshal(s))) == Marshal(Normalize(s)).
//   - SpecHash: sha256 of the canonical normalized bytes (cache key input).
package spec

import "encoding/json"

// Version is the compiler-emitted spec schema version. VersionV1 remains a
// readable legacy input; Normalize converts both versions to the same v2
// execution plan.
// CanonicalExitID 是语言投影的**流程出口 End** 固定 ID（语言仓
// `ir.ExitNodeKey = "900001"`）。只有它的出边才是「return 之后的不可达代码」
// 投影残留；其余 End 可能是 attempt / QA 臂端（`SuccessEnd`/`FailureEnd`），
// 是控制流载体 —— 它们的出边是「本臂结束 → 继续执行」的活边。
const CanonicalExitID = "900001"

const (
	VersionV1 = "v1"
	VersionV2 = "v2"
	Version   = VersionV2
)

// Spec is the serializable execution subset of one workflow.
type Spec struct {
	Version string `json:"version"`
	// SemanticsVersion is the execution-semantics projection version (PRD
	// §4.7). It is independent of the spec schema version above and is
	// carried from the artifact sidecar; Normalize and DecodeJSON must copy
	// it explicitly or the roundtrip silently drops the version.
	SemanticsVersion string `json:"semantics_version,omitempty"`
	// Provenance records, per (node, field), where a carried fact came from
	// (PRD §4.3 origin: wire-present|wire-absent|policy|harness) plus a
	// resolvable evidence pointer. It is a derived projection, never a
	// second definition of platform policy.
	Provenance []ProvenanceRow `json:"provenance,omitempty"`
	Mode       string          `json:"mode,omitempty"` // @mode 顶层形态（workflow/advanced-chat/agent;M13 起 runtime 语义）
	Entry      *EntrySpec      `json:"entry,omitempty"`
	ConvVars   []ConvVarSpec   `json:"conversation_vars,omitempty"`
	Nodes      []NodeSpec      `json:"nodes"`
	Edges      []EdgeSpec      `json:"edges"`
}

// EntrySpec is the workflow entry: input parameter declarations.
type EntrySpec struct {
	ID     string      `json:"id"`
	Params []ParamSpec `json:"params"`
}

// ConvVarSpec is one @conversation declaration (initial value binding).
type ConvVarSpec struct {
	Name    string    `json:"name"`
	Type    *TypeSpec `json:"type,omitempty"`
	Default any       `json:"default"` // no omitempty: ""/0/false are legal
}

// ParamSpec is one entry input parameter.
type ParamSpec struct {
	Name     string    `json:"name"`
	Type     *TypeSpec `json:"type,omitempty"`
	Optional bool      `json:"optional"`
	Default  any       `json:"default"` // no omitempty
}

// TypeSpec is the execution-relevant port type. File-upload constraints
// (AllowedFileTypes/...) are reference-target frontend concerns — excluded (D1 subset).
type TypeSpec struct {
	Kind     string               `json:"kind"`
	Elem     *TypeSpec            `json:"elem,omitempty"`
	Props    map[string]*TypeSpec `json:"props,omitempty"`
	Required bool                 `json:"required"`
}

// NodeSpec is one graph node: id, kind and its per-kind config payload.
// Config is raw JSON; Normalize re-encodes it canonically per kind.
type NodeSpec struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Config json.RawMessage `json:"config"`
}

// EdgeSpec is one control-flow arc. SourcePort carries the branch/port
// literal ("true"/"false"/"fail-branch"/"source"/"loop-function-inline-output");
// TargetPort carries "loop-function-inline-input" for loop-back arcs
// (absorbed by the loop driver at compile time).
type EdgeSpec struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	SourcePort string `json:"source_port,omitempty"`
	TargetPort string `json:"target_port,omitempty"`
}

// Port literals (mirror flow.Project derivation, verbatim).
const (
	PortTrue             = "true"
	PortFalse            = "false"
	PortFailBranch       = "fail-branch"
	PortSource           = "source" // attempt success branch
	PortLoopInlineOutput = "loop-function-inline-output"
	PortLoopInlineInput  = "loop-function-inline-input"
)
