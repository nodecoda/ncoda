package passes

// 平台证据注记（2026-10-25）：本文引用的平台源码/产物路径是**审计追溯**（当年判据的
// 一手证据）；平台实现与证据正本已随适配器移入商业仓（internal/backend/*、platforms/*）。
// 判据读的是 TargetProfile.Carrier / capability.Table 的轴，代码不认识平台名。
//

// Immutable target-capability contracts, ported from
// src/nclang/lang/capabilities.py (347 lines) — the Phase-0 shape/state
// table the CapabilityGatePass consults (capability_gate.py:444-452).
// Reachable surface:
//
//	CapabilityState                   :30-34   CapabilityState
//	CapabilityShape                   :37-57   CapabilityShape
//	CapabilityEntry                   :61-82   CapabilityEntry
//	CapabilityManifest                :118-137 CapabilityManifest
//	CapabilityManifest.lookup         :132-134
//	DEFAULT_REJECTED_CAPABILITY       :140
//	_DEFAULT_ENTRIES + DEFAULT_...    :142-181
//
// Deviations (recorded):
//
//   - CapabilityEntry carries only State. Python's diagnostic is forced to
//     DSLDiagnosticCode.TARGET_NOT_LOWERABLE for every REJECTED entry
//     (capabilities.py:69-79) — the Go Diagnostic() method restates that
//     invariant — and evidence_ids/dify_version_range are dropped: they are
//     consumed by the capability-inventory module (17C2 target validation
//     territory), not by the gate. The test surface asserting evidence IDs
//     (test_capability_manifest.py flat/function_call/enum/memory/vision
//     cases) is therefore not ported; the states are.
//
//   - CapabilityReport (capabilities.py:84-115) is not ported: the gate
//     consumes lookup() only, and no Go consumer needs the grouped report.
//
//   - Manifest entries are read-only via a small internal table (the Go
//     analogue of MappingProxyType — Set panics), matching the frozen
//     contract. binding.RoMap is deliberately not reused: its only
//     constructor is unexported, and constructing one from the passes
//     package would force an out-of-brief binding change.
//
// The reference manifest version constant (CAPABILITY_MANIFEST_VERSION
// = "v2", capabilities.py:20) is kept for the default manifest.

import (
	"github.com/nodecoda/ncoda/pkg/diag"
)

// capabilityManifestVersion mirrors CAPABILITY_MANIFEST_VERSION
// (capabilities.py:20).
const capabilityManifestVersion = "v2"

// CapabilityState is one deterministic lowering state for a construct or
// operand shape, mirroring CapabilityState (capabilities.py:30-34).
type CapabilityState string

const (
	CapabilityStateRejected     CapabilityState = "REJECTED"
	CapabilityStateMaterialized CapabilityState = "MATERIALIZED"
	CapabilityStateEnabled      CapabilityState = "ENABLED"
)

// CapabilityShape names a closed public construct/operand shape,
// mirroring CapabilityShape (capabilities.py:37-57). All 17 shapes are
// ported (the task brief says 16; the reference defines 17 including
// CONDITION_EFFECTFUL — the reference wins).
type CapabilityShape string

const (
	CapabilityShapeConditionDirect                   CapabilityShape = "condition.direct"
	CapabilityShapeConditionCalculation              CapabilityShape = "condition.calculation"
	CapabilityShapeConditionEffectful                CapabilityShape = "condition.effectful"
	CapabilityShapeForExpressionWithYield            CapabilityShape = "for.expression.with_yield"
	CapabilityShapeForStatementPlain                 CapabilityShape = "for.statement.plain"
	CapabilityShapeForStatementWithExit              CapabilityShape = "for.statement.with_exit"
	CapabilityShapeLoopFunctionReturn                CapabilityShape = "loop.function_return"
	CapabilityShapeParallelIsolated                  CapabilityShape = "parallel.isolated"
	CapabilityShapeControlAttempt                    CapabilityShape = "control.attempt"
	CapabilityShapeStateAssignment                   CapabilityShape = "state.assignment"
	CapabilityShapeClassificationEnum                CapabilityShape = "classification.enum"
	CapabilityShapeGraphonNodeQuestionClassifierEnum CapabilityShape = "GRAPHON-NODE-QUESTION-CLASSIFIER-ENUM"
	CapabilityShapeStructuredExtractionFlat          CapabilityShape = "structured.extraction.flat"
	CapabilityShapeStructuredExtractionFunctionCall  CapabilityShape = "structured.extraction.function_call"
	CapabilityShapeStructuredExtractionEnum          CapabilityShape = "structured.extraction.enum"
	CapabilityShapeStructuredExtractionMemory        CapabilityShape = "structured.extraction.memory"
	CapabilityShapeStructuredExtractionVision        CapabilityShape = "structured.extraction.vision"
)

// allCapabilityShapes is the closed 17-shape enumeration, mirroring the
// reference CapabilityShape member order (capabilities.py:37-57).
var allCapabilityShapes = []CapabilityShape{
	CapabilityShapeConditionDirect,
	CapabilityShapeConditionCalculation,
	CapabilityShapeConditionEffectful,
	CapabilityShapeForExpressionWithYield,
	CapabilityShapeForStatementPlain,
	CapabilityShapeForStatementWithExit,
	CapabilityShapeLoopFunctionReturn,
	CapabilityShapeParallelIsolated,
	CapabilityShapeControlAttempt,
	CapabilityShapeStateAssignment,
	CapabilityShapeClassificationEnum,
	CapabilityShapeGraphonNodeQuestionClassifierEnum,
	CapabilityShapeStructuredExtractionFlat,
	CapabilityShapeStructuredExtractionFunctionCall,
	CapabilityShapeStructuredExtractionEnum,
	CapabilityShapeStructuredExtractionMemory,
	CapabilityShapeStructuredExtractionVision,
}

// CapabilityEntry is one immutable state for a capability shape, mirroring
// the frozen CapabilityEntry dataclass (capabilities.py:61-82). Simplified
// to State only — see the header deviation note.
type CapabilityEntry struct {
	State CapabilityState
}

// Diagnostic mirrors the Python __post_init__ invariant
// (capabilities.py:69-79): every REJECTED entry carries
// TARGET_NOT_LOWERABLE; enabled/materialized entries carry none.
func (e CapabilityEntry) Diagnostic() diag.Code {
	if e.State == CapabilityStateRejected {
		return diag.TARGET_NOT_LOWERABLE
	}
	return 0
}

// CapabilityManifestForTarget 返回**目标平台专属**的 Phase-0 能力清单
// （D3.2 单一声明点，2026-09-13 用户拍板）：一张表 = 一处真相。缺省沿用
// DefaultCapabilityManifest（除 condition.effectful 外全 ENABLED）；目标专属
// 拒绝项在此声明，每条都对应 docs/PLATFORM-CAPABILITY.md 的一行
// （平台证据 file:line 或语料计数 + 判定点 + 用户指引 + 错误类别）。
//
// 纪律：**没有负向契约测试的能力条目不算数**（见
// internal/compiler/platform_error_class_contract_test.go）。
func CapabilityManifestForTarget(target string) *CapabilityManifest {
	switch target {
	case "coze-biz-1.0":
		// 「循环内 return」（loop.function_return）已由 IR 层物化实现
		// （pkg/ir/loop_return_materialize.go，2026-09-13 E15）：循环状态对
		// `returned:bool` / `return_value:T` + 体内写状态 + `break` + 循环后门控
		// `if (returned == true) { return <逃逸值>; }`。平台**有通道**
		// （coze-studio `variable_assign_in_loop.go` 执行即写、`loop/break/break.go`
		// 置 $break），故不属平台不可约，也不需要前端拒绝。
		//
		// batch（`parallel for`）**不是**该能力的 lowering 缺口（2026-09-14）：
		// **void** `return;` 合法（迭代体内终止整个流程，平台通道 = 容器内节点
		// 直连根 `end` 的边；真实导出 5 份在用），**带值** `return <v>` 是**语言错**
		// E1037（并发下「哪个迭代的值胜出」不确定）—— 二者都由前端
		// `parallel_iteration_local.go` 判定，不经能力门禁。
		// 残留 LoopReturnNode 由渲染器 fail loud（ErrPendingLowering）保留作兜底。
		// extract(..., {vision: image}) has no coze-biz carrier. The generic
		// ExtractionSpec keeps Vision for targets that have a native vision
		// slot, but coze-biz currently renders only the text LLM slots and
		// node_inputs. Reject this shape here so a successful compile can never
		// silently omit the image operand (C1-H5).
		return NewCapabilityManifestWithState(map[CapabilityShape]CapabilityState{
			CapabilityShapeStructuredExtractionVision: CapabilityStateRejected,
		})
	default:
		return DefaultCapabilityManifest()
	}
}

// capabilityShapeVerdict 返回一个能力形状被拒时的**错误类别**与**是否可约未实现**
// （D1/D3，2026-09-13 用户拍板）。能力标识 = 形状名本身（17 形状闭集就是能力清单
// 的单一声明点；人读版见 docs/PLATFORM-CAPABILITY.md）。
//
// 缺省 platform（**不可约**）：形状被拒 = 目标平台物理表达不了，用户必须改写法。
//
// 唯一例外 loop.function_return = internal + pending：平台**有通道**
// （循环状态变量 + break + loop 后检查）——语料实证 198 个 loop 中 36 个带
// variableParameters（循环级状态）、7 个 body 含 break、10 个把状态变量暴露成
// 循环输出，即真实导出里就存在这个模式；只是编译器尚未实现该 lowering。
// 把「我们没做」说成「平台不支持」是撒谎，也会让用户永久改掉本来没问题的代码。
func capabilityShapeVerdict(shape CapabilityShape) (class string, pending bool) {
	if shape == CapabilityShapeLoopFunctionReturn {
		return diag.ClassInternal, true
	}
	return diag.ClassPlatform, false
}

// capabilityEntryTable is the read-only manifest entry table (the Go
// analogue of the Python MappingProxyType entries field). Lookups and
// iteration only; Set panics so any mutation attempt after construction is
// loud. The wrapped map is owned exclusively by the manifest (construction
// copies), so no mutation path survives.
type capabilityEntryTable struct {
	m map[CapabilityShape]CapabilityEntry
}

// newCapabilityEntryTable defensively copies entries.
func newCapabilityEntryTable(entries map[CapabilityShape]CapabilityEntry) *capabilityEntryTable {
	cp := make(map[CapabilityShape]CapabilityEntry, len(entries))
	for k, v := range entries {
		cp[k] = v
	}
	return &capabilityEntryTable{m: cp}
}

// Get returns the entry bound to shape.
func (t *capabilityEntryTable) Get(shape CapabilityShape) (CapabilityEntry, bool) {
	e, found := t.m[shape]
	return e, found
}

// Len reports the number of declared entries.
func (t *capabilityEntryTable) Len() int { return len(t.m) }

// Set mutates the table; it always panics because manifest entries are
// frozen after construction (Python MappingProxyType raises TypeError on
// item assignment).
func (t *capabilityEntryTable) Set(CapabilityShape, CapabilityEntry) {
	panic("passes: capability manifest entries are read-only after construction")
}

// CapabilityManifest is a versioned immutable table with a closed
// default-rejected lookup, mirroring CapabilityManifest
// (capabilities.py:118-137). Entries are read-only (Set panics).
type CapabilityManifest struct {
	Version string
	entries *capabilityEntryTable
}

// NewCapabilityManifest constructs a manifest, defensively copying entries
// (the Go analogue of the __post_init__ MappingProxyType wrap). A nil
// entries map yields an empty closed-world manifest whose every lookup is
// the default REJECTED entry.
func NewCapabilityManifest(version string, entries map[CapabilityShape]CapabilityEntry) *CapabilityManifest {
	if entries == nil {
		entries = map[CapabilityShape]CapabilityEntry{}
	}
	return &CapabilityManifest{Version: version, entries: newCapabilityEntryTable(entries)}
}

// Lookup returns the declared entry or the immutable closed-world rejection,
// mirroring CapabilityManifest.lookup (capabilities.py:132-134).
func (m *CapabilityManifest) Lookup(shape CapabilityShape) CapabilityEntry {
	if m == nil || m.entries == nil {
		return defaultRejectedCapability
	}
	entry, found := m.entries.Get(shape)
	if !found {
		return defaultRejectedCapability
	}
	return entry
}

// Entries returns the read-only entry table (capabilities.py entries field).
func (m *CapabilityManifest) Entries() *capabilityEntryTable {
	return m.entries
}

// Len reports the number of declared entries.
func (m *CapabilityManifest) Len() int {
	if m == nil || m.entries == nil {
		return 0
	}
	return m.entries.Len()
}

// defaultRejectedCapability mirrors DEFAULT_REJECTED_CAPABILITY
// (capabilities.py:140).
var defaultRejectedCapability = CapabilityEntry{State: CapabilityStateRejected}

// DefaultCapabilityManifest mirrors DEFAULT_CAPABILITY_MANIFEST
// (capabilities.py:142-181): every phase-0 shape is ENABLED except
// CONDITION_EFFECTFUL, the manifest's only default REJECTED entry.
func DefaultCapabilityManifest() *CapabilityManifest {
	entries := make(map[CapabilityShape]CapabilityEntry, len(allCapabilityShapes))
	enabled := CapabilityEntry{State: CapabilityStateEnabled}
	for _, shape := range allCapabilityShapes {
		entries[shape] = enabled
	}
	entries[CapabilityShapeConditionEffectful] = CapabilityEntry{State: CapabilityStateRejected}
	return NewCapabilityManifest(capabilityManifestVersion, entries)
}

// NewCapabilityManifestWithState builds a manifest from the default entries
// with the given per-shape state overrides applied. It is the Go analogue
// of the reference's rejected-profile recipe (dataclasses.replace with a
// fresh CapabilityManifest — test_operation_failure_contract.py:101-116),
// and is the test injection surface the gate tests use.
func NewCapabilityManifestWithState(overrides map[CapabilityShape]CapabilityState) *CapabilityManifest {
	m := DefaultCapabilityManifest()
	entries := make(map[CapabilityShape]CapabilityEntry, len(allCapabilityShapes))
	for _, shape := range allCapabilityShapes {
		entries[shape] = m.Lookup(shape)
	}
	for shape, state := range overrides {
		entries[shape] = CapabilityEntry{State: state}
	}
	return NewCapabilityManifest(capabilityManifestVersion, entries)
}
