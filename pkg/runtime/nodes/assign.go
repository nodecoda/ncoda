package nodes

// assign.go — assign / loop_assign 节点正式实现（从 compile stub 迁移，
// M2.1;M2.3 状态面）。两者共享同一实现（spec 用同一 AssignConfig DTO
// 解码）;端口语义:输入 "value" → 输出以 var_name 命名。
//
// 状态面:loop_assign（LoopState=true 且 LoopID 非空）同时把值写入
// RuntimeState.Loops[LoopID].Vars —— 驱动器跨迭代读取,取代 M1 对 body
// 输出端口的 scanPort 扫描。缺 RuntimeState（非图上下文）→ RNodeError
// （程序错误,诚实 fail-fast）;LoopState=false 的普通 assign 不触碰状态。
//
// 复合赋值（M14）:Operation 非空（+= -= *= /= <<）时基于当前共享值合成
// —— loop_assign 读 LoopState.Vars[VarName],conversation assign 读
// RuntimeState.ConvVars[VarName]（IR 只对 conversation 变量生成 operation;
// 数值运算宽容字符串数值,`<<` 追加数组）。此前直接写 RHS,多次复合赋值
// 互相覆盖（count+=1 两次 → 1 而非 2）。

import (
	"context"
	"math"
	"strconv"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/state"
)

// Assign writes a variable.
type Assign struct{}

func (Assign) Kind() string { return spec.KindAssign }

func (Assign) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	return buildAssign(ctx, n)
}

// LoopAssign writes a loop-state variable (same DTO semantics).
type LoopAssign struct{}

func (LoopAssign) Kind() string { return spec.KindLoopAssign }

func (LoopAssign) Build(ctx context.Context, n *spec.NodeSpec, _ Deps) (NodeImpl, error) {
	return buildAssign(ctx, n)
}

func buildAssign(ctx context.Context, n *spec.NodeSpec) (NodeImpl, error) {
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, err
	}
	return &assignNode{cfg: cfg.(*spec.AssignConfig)}, nil
}

type assignNode struct{ cfg *spec.AssignConfig }

func (a *assignNode) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	v, _ := in["value"]
	// 复合赋值:基于当前共享值合成（+= -= *= /= 数值,<< 追加数组）。
	if a.cfg.Operation != "" && a.cfg.Operation != "set" {
		old := a.currentValue(ctx)
		v = combineAssign(a.cfg.Operation, old, v)
	}
	if a.cfg.LoopState && a.cfg.LoopID != "" {
		err := compose.ProcessState[*state.RuntimeState](ctx, func(_ context.Context, rs *state.RuntimeState) error {
			if ls := rs.Loop(a.cfg.LoopID); ls != nil {
				// SetVar 同时写本迭代读视图:同一迭代内 `i = i + 1; if (i == 0)`
				// 的 if 必须读到 1,而不是迭代开始快照的 0（语句序）。
				ls.SetVar(a.cfg.VarName, v)
			}
			return nil
		})
		if err != nil {
			return nil, diag.New(diag.RNodeError, "loop_assign %s: RuntimeState missing in ctx: %v", a.cfg.VarName, err)
		}
	}
	return map[string]any{a.cfg.VarName: v}, nil
}

// currentValue 读取该变量的当前共享值（loop_assign → LoopState.Vars,
// conversation assign → ConvVars）。无状态上下文 / 变量未写入 → nil
// （复合赋值退化为直接赋值,确定性）。
func (a *assignNode) currentValue(ctx context.Context) any {
	var old any
	_ = compose.ProcessState(ctx, func(_ context.Context, rs *state.RuntimeState) error {
		if a.cfg.LoopState && a.cfg.LoopID != "" {
			if ls := rs.Loop(a.cfg.LoopID); ls != nil {
				old = ls.Vars[a.cfg.VarName]
			}
			return nil
		}
		old = rs.ConvVars[a.cfg.VarName]
		return nil
	})
	return old
}

// combineAssign 合成复合赋值结果。数值运算对字符串数值宽容
// （expr.ToFloat,与 shim 求值器一致）;非数值 → 直接覆盖（确定性）。
// `<<` 把 v 追加到旧数组（nil/非数组 → 视为空）。
func combineAssign(op string, old, v any) any {
	switch op {
	case "<<":
		out := toAnySlice(old)
		return append(out, v)
	case "+=", "-=", "*=", "/=":
		lo, lok := expr.ToFloat(old)
		ro, rok := expr.ToFloat(v)
		if !lok || !rok {
			return v
		}
		var r float64
		switch op {
		case "+=":
			r = lo + ro
		case "-=":
			r = lo - ro
		case "*=":
			r = lo * ro
		case "/=":
			r = lo / ro
		}
		if assignIntLike(old) && assignIntLike(v) {
			return int64(r)
		}
		return r
	}
	return v
}

// assignIntLike 报告值是否整数值（int 族 / 整值 float / 整数值字符串）。
func assignIntLike(v any) bool {
	switch t := v.(type) {
	case int64, int, int32:
		return true
	case float64:
		return math.Trunc(t) == t
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return err == nil && math.Trunc(f) == f
	}
	return false
}
