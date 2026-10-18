package ir_test

// end_regress_test.go — End 终点合成回归。
//
// output(expr) 降级为 OutputEmitter（ir.Output,中间消息,非终止）,不再产生
// continuing End（emitOutputEnd 已删除,2026-08-23）。流程中 output 之后的
// 语句照常执行,最终由 ensureEnd 合成唯一 900001。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
)

// endIDs lowers src and returns the projected End node ids (in order).
func endIDs(t *testing.T, src string) []string {
	t.Helper()
	g := buildIR(t, src)
	p, err := flow.Project(g)
	if err != nil {
		t.Fatalf("flow.Project: %v", err)
	}
	var ids []string
	for _, n := range p.Nodes {
		if n.Kind() == ir.KindEnd {
			ids = append(ids, n.NodeID())
		}
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// output(expr) 不再产生 continuing End:单个 output 后由 ensureEnd 合成唯一
// 900001,且产物中有一个 OutputEmitter。
func TestOutputSingleImplicitEnd(t *testing.T) {
	src := workflowHeader + `
function main() {
    output("x");
}
`
	ids := endIDs(t, src)
	if len(ids) != 1 || ids[0] != "900001" {
		t.Fatalf("end nodes = %v, want [900001] (output emits no End)", ids)
	}
	p, err := flow.Project(buildIR(t, src))
	if err != nil {
		t.Fatalf("flow.Project: %v", err)
	}
	hasOut := false
	for _, n := range p.Nodes {
		if n.Kind() == ir.KindOutput {
			hasOut = true
		}
	}
	if !hasOut {
		t.Fatal("output(expr) must lower to an OutputEmitter node")
	}
}

// output(expr) 之后紧跟 return:输出节点不终止流程,return 收敛到 900001。
func TestOutputThenReturnCreatesImplicitEnd(t *testing.T) {
	src := workflowHeader + `
function main(string value = "probe") -> string {
    output(value);
    return value + ":final";
}
`
	ids := endIDs(t, src)
	if len(ids) != 1 || ids[0] != "900001" {
		t.Fatalf("end nodes = %v, want [900001]", ids)
	}
}

// 多 output + return 累积（48 完整形态）:2 个 OutputEmitter 按序 + 唯一 900001。
func TestMultipleOutputThenReturn(t *testing.T) {
	src := workflowHeader + `
function main(string value = "probe") -> string {
    output(value);
    output(value + ":second");
    return value + ":final";
}
`
	ids := endIDs(t, src)
	if len(ids) != 1 || ids[0] != "900001" {
		t.Fatalf("end nodes = %v, want [900001]", ids)
	}
	p, err := flow.Project(buildIR(t, src))
	if err != nil {
		t.Fatalf("flow.Project: %v", err)
	}
	outCount := 0
	for _, n := range p.Nodes {
		if n.Kind() == ir.KindOutput {
			outCount++
		}
	}
	if outCount != 2 {
		t.Fatalf("output emitters = %d, want 2", outCount)
	}
}

// 基线:普通 return 仍走单一 900001（不回归既有语义）。
func TestPlainReturnSingleImplicitEnd(t *testing.T) {
	src := workflowHeader + `
function main() -> string {
    return "ok";
}
`
	ids := endIDs(t, src)
	if len(ids) != 1 || ids[0] != "900001" {
		t.Fatalf("end nodes = %v, want [900001]", ids)
	}
}

// 基线:bare return（void）同样落在 900001。
func TestVoidReturnSingleImplicitEnd(t *testing.T) {
	src := workflowHeader + `
function main() {
    return;
}
`
	ids := endIDs(t, src)
	if len(ids) != 1 || ids[0] != "900001" {
		t.Fatalf("end nodes = %v, want [900001]", ids)
	}
}
