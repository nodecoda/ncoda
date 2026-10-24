package nodes

// aggregate.go — aggregate 节点正式实现（从 compile stub 迁移，M2.1）。
//
// P4/D3 merge 语义 = **first_non_zero**（2026-09-09 语言裁定，正本
// `docs/AGGREGATION-HOISTING-ALGORITHM.md:63`）：按候选序（r0..rn）取第一个
// **非 zero** 的整值；候选全部 zero（含执行臂产出空串/0/false/空容器）⇒ zero。
// 未执行臂在平台上贡献的是**类型 zero**，不是 nil/“无值”——所以零值候选被
// 跳过、空串**不短路**。旧实现 `first non-nil` 把空串当成合法取值（ES-102 的
// 已知缺陷：`["", "x"]` 会返回 `""`，而平台返回 `"x"`）。
//
// 值域只有 absent | null | value(T)：nil 与容器 zero 同权（都是 zero），因此
// 运行期不需要类型表就能判 zero。

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// Aggregate merges branch values into Group1 or named groups (first_non_zero).
type Aggregate struct{}

func (Aggregate) Kind() string { return spec.KindAggregate }

func (Aggregate) Build(_ context.Context, n *spec.NodeSpec, deps Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &aggregateNode{cfg: cfg.(*spec.AggregateConfig), id: n.ID, semTrace: deps.SemTrace}, nil
}

type aggregateNode struct {
	cfg      *spec.AggregateConfig
	id       string
	semTrace func(trace.Record)
}

func (a *aggregateNode) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	if len(a.cfg.Groups) > 0 {
		out := make(map[string]any, len(a.cfg.Groups))
		for _, group := range a.cfg.Groups {
			var groupInput map[string]any
			if v, ok := in[group.Name].(map[string]any); ok {
				groupInput = v
			}
			out[group.Name] = a.pick(group.Name, len(group.Refs), groupInput, group.Refs)
		}
		return out, nil
	}
	// 单组口名 = 汇聚单元身份（spec 由 IR 的 SingleGroupName 落值；空 = 平台的
	// 「无名组」拼写）。声明与消费同判据，否则下游读不到这个口。
	legacy := a.cfg.LegacyGroupName
	if legacy == "" {
		legacy = "Group1"
	}
	return map[string]any{legacy: a.pick(legacy, len(a.cfg.Refs), in, a.cfg.Refs)}, nil
}

// pick returns the group's first_non_zero value. Candidates are read in arm
// order (r0..rn) from the group's input map; a candidate whose key is absent
// counts as zero (the raising side is responsible for materialising declared
// candidates, ES-111 — the runtime never renumbers the remaining arms).
//
// When every candidate is zero the result is that zero ("" / 0 / false / null):
// an all-zero merge is still a value, not an absence.
func (a *aggregateNode) pick(group string, n int, in map[string]any, refs []spec.Ref) any {
	// zeroVal keeps the first **typed** zero seen, so [absent, ""] yields ""
	// (the declared zero) rather than nil. Only an all-absent group returns nil.
	var zeroVal any
	var skipped []string
	for i := 0; i < n; i++ {
		v, ok := in[fmt.Sprintf("r%d", i)]
		if !ok {
			v = nil
		}
		if mergeZero(v) {
			if zeroVal == nil {
				zeroVal = v
			}
			skipped = append(skipped, mergeCandidateName(refs, i))
			continue
		}
		a.emitMergeTrace(group, refs, i)
		return v
	}
	if len(skipped) > 0 {
		a.emitMergeTrace(group, refs, -1)
	}
	return zeroVal
}

// emitMergeTrace records a zero-skip decision (only when zero candidates were
// actually skipped; a clean first-arm win is not a fill decision). `winner` is
// the 0-based candidate index that won, or -1 when all candidates were zero.
// The candidate order is carried so a reviewer can reconstruct the arm order;
// values themselves are never recorded (ES-064 redaction).
func (a *aggregateNode) emitMergeTrace(group string, refs []spec.Ref, winner int) {
	if a.semTrace == nil {
		return
	}
	names := make([]string, len(refs))
	for i := range refs {
		names[i] = mergeCandidateName(refs, i)
	}
	decision := "all_zero"
	if winner >= 0 {
		decision = fmt.Sprintf("pick_r%d", winner)
	}
	a.semTrace(trace.Record{
		Stage:    "fill",
		RuleID:   trace.RuleMerge,
		Origin:   trace.OriginPolicy,
		Evidence: "hand:docs/AGGREGATION-HOISTING-ALGORITHM.md:63",
		Node:     a.id,
		Kind:     spec.KindAggregate,
		FillRule: "merge_zero",
		Attempt:  1,
		Axis:     "per_attempt",
		Input:    group + ":" + strings.Join(names, ","),
		Output:   "<redacted>",
		Decision: decision,
	})
}

func mergeCandidateName(refs []spec.Ref, i int) string {
	if i >= 0 && i < len(refs) {
		r := refs[i]
		if r.Output != "" {
			return r.Producer + "." + r.Output
		}
		return r.Producer
	}
	return fmt.Sprintf("r%d", i)
}

// mergeZero reports whether v is the type zero a merge candidate contributes
// when its arm did not execute: nil/null, "", 0, false, or an empty container.
// Containers are zero at zero length; a non-empty value of any kind is non-zero.
func mergeZero(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return x == ""
	case bool:
		return !x
	case int:
		return x == 0
	case int8:
		return x == 0
	case int16:
		return x == 0
	case int32:
		return x == 0
	case int64:
		return x == 0
	case uint:
		return x == 0
	case uint8:
		return x == 0
	case uint16:
		return x == 0
	case uint32:
		return x == 0
	case uint64:
		return x == 0
	case float32:
		return x == 0
	case float64:
		return x == 0
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Slice, reflect.Map:
		return reflect.ValueOf(v).Len() == 0
	}
	return false
}
