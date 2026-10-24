package nodes

// listop.go — list_op 节点正式实现（M2.1，PRD U2.5）。
//
// 内存实现 filter / sort / sort_by / take:
//   - filter:FilterKey（""=裸元素）取字段值,FilterOp 比较
//     （contains / is / is not / in / > / >= / < / <=）
//   - sort / sort_by:OrderKey 字段（sort 为裸值）+ OrderVal（asc/desc）
//   - take:Limit 前 N 个
//
// 输入端口 "input"（AssembleInput 路径）,输出端口 "output"（IR 契约）。

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// ListOp applies an in-memory list operator.
type ListOp struct{}

func (ListOp) Kind() string { return spec.KindListOp }

func (ListOp) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &listOpNode{cfg: cfg.(*spec.ListOpConfig)}, nil
}

type listOpNode struct{ cfg *spec.ListOpConfig }

func (l *listOpNode) Invoke(_ context.Context, in map[string]any) (map[string]any, error) {
	input := toAnySlice(in["input"])
	var out []any
	switch l.cfg.Operation {
	case "filter":
		out = l.filter(input)
	case "take":
		n := l.cfg.Limit
		if n > len(input) {
			n = len(input)
		}
		if n < 0 {
			n = 0
		}
		out = input[:n]
	case "sort":
		out = l.sort(input, "")
	case "sort_by":
		out = l.sort(input, l.cfg.OrderKey)
	default:
		out = input
	}
	return map[string]any{"output": out}, nil
}

func (l *listOpNode) filter(input []any) []any {
	out := make([]any, 0, len(input))
	for _, el := range input {
		if l.match(el) {
			out = append(out, el)
		}
	}
	return out
}

func (l *listOpNode) match(el any) bool {
	lv := el
	if l.cfg.FilterKey != "" {
		m, ok := el.(map[string]any)
		if !ok {
			return false
		}
		lv = m[l.cfg.FilterKey]
	}
	rv := l.cfg.FilterVal
	switch l.cfg.FilterOp {
	case "":
		return true // 无 op（保守 passthrough;IR filter 恒带 op）
	case "is", "==", "=":
		return fmt.Sprint(lv) == fmt.Sprint(rv)
	case "is not", "!=":
		return fmt.Sprint(lv) != fmt.Sprint(rv)
	case "contains":
		return strings.Contains(fmt.Sprint(lv), fmt.Sprint(rv))
	case "in":
		for _, v := range toAnySlice(rv) {
			if fmt.Sprint(v) == fmt.Sprint(lv) {
				return true
			}
		}
		return false
	case ">", ">=", "<", "<=":
		op := map[string]int{">": 13, ">=": 14, "<": 15, "<=": 16}[l.cfg.FilterOp]
		return expr.Compare(op, lv, rv)
	}
	return false
}

func (l *listOpNode) sort(input []any, key string) []any {
	out := append([]any(nil), input...)
	asc := l.cfg.OrderVal != "desc"
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if key != "" {
			am, aok := a.(map[string]any)
			bm, bok := b.(map[string]any)
			if aok && bok {
				a, b = am[key], bm[key]
			}
		}
		if af, aok := expr.ToFloat(a); aok {
			if bf, bok := expr.ToFloat(b); bok {
				if asc {
					return af < bf
				}
				return af > bf
			}
		}
		as, bs := fmt.Sprint(a), fmt.Sprint(b)
		if asc {
			return as < bs
		}
		return as > bs
	})
	return out
}

// toAnySlice normalizes any list-ish value to []any (nil → nil).
func toAnySlice(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	case []string:
		out := make([]any, 0, len(t))
		for _, s := range t {
			out = append(out, s)
		}
		return out
	default:
		return []any{v}
	}
}
