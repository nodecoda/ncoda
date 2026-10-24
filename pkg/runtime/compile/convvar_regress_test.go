package compile

// convvar_regress_test.go — M14 回归:conversation 变量共享状态语义。
//
// 修复:@conversation 变量此前在 runtime 无解析路径,answer(username)
// 恒 <missing>。现在 assign 写回 RuntimeState.ConvVars,模板节点把
// {{global_variable_app.<name>}} 注入 inputs（对齐参考目标 conversation
// variable）。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

// convVarSpec 构造一个 @conversation 声明 + start→assign→output→end 的
// 图;assign 把字面量写入 conv 变量,output 模板引用 {{global_variable_app.x}}。
func convVarSpec(varName, defaultValue, assigned, outTpl string, withAssign bool) *spec.Spec {
	nodes := []spec.NodeSpec{
		nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
	}
	edges := []spec.EdgeSpec{{Source: "100001", Target: "100002"}}
	if withAssign {
		nodes = append(nodes, nodeSpec("100002", spec.KindAssign, &spec.AssignConfig{
			VarName: varName,
			Value:   spec.Source{Path: []string{varName}, Val: assigned},
		}))
		nodes = append(nodes, nodeSpec("100003", spec.KindOutput, &spec.OutputConfig{Template: outTpl}))
		edges = append(edges, spec.EdgeSpec{Source: "100002", Target: "100003"})
		edges = append(edges, spec.EdgeSpec{Source: "100003", Target: "900001"})
	} else {
		nodes = append(nodes, nodeSpec("100002", spec.KindOutput, &spec.OutputConfig{Template: outTpl}))
		edges = append(edges, spec.EdgeSpec{Source: "100002", Target: "900001"})
	}
	nodes = append(nodes, nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: lastNodeID(withAssign), Output: "output"}}}))
	return &spec.Spec{
		Version:  spec.Version,
		Entry:    &spec.EntrySpec{ID: "100001"},
		ConvVars: []spec.ConvVarSpec{{Name: varName, Type: &spec.TypeSpec{Kind: "string"}, Default: defaultValue}},
		Nodes:    nodes,
		Edges:    edges,
	}
}

func lastNodeID(withAssign bool) string {
	if withAssign {
		return "100003"
	}
	return "100002"
}

// M14:assign 写回 ConvVars → output 引用取到赋值后的值（53/54/55 形态）。
func TestConvVarAssignWriteBack(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), convVarSpec("username", "guest", "updated", "{{global_variable_app.username}}", true))
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if out["output"] != "updated" {
		t.Fatalf("output = %v, want updated (assign 写回后引用)", out["output"])
	}
}

// M14:未赋值时引用 conversation 变量 → 初始值（@conversation default）。
func TestConvVarDefaultWhenUnassigned(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), convVarSpec("username", "guest", "", "{{global_variable_app.username}}", false))
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if out["output"] != "guest" {
		t.Fatalf("output = %v, want guest (未赋值取默认)", out["output"])
	}
}

// M14:无 @conversation 声明时,{{global_variable_app.x}} 保持 <missing>
// （确定性契约,不因注入机制泄漏）。
func TestConvVarTemplateMissingWithoutDecl(t *testing.T) {
	s := convVarSpec("username", "guest", "", "{{global_variable_app.username}}", false)
	s.ConvVars = nil // 无声明
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if out["output"] != "<missing>" {
		t.Fatalf("output = %v, want <missing> (未声明变量)", out["output"])
	}
}

// M14:多变量共写（53:username/score/tags 三个 conv 变量顺序写回）。
func TestConvVarMultipleWrites(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		ConvVars: []spec.ConvVarSpec{
			{Name: "a", Type: &spec.TypeSpec{Kind: "string"}, Default: ""},
			{Name: "b", Type: &spec.TypeSpec{Kind: "integer"}, Default: 0},
		},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("100002", spec.KindAssign, &spec.AssignConfig{VarName: "a", Value: spec.Source{Path: []string{"a"}, Val: "x"}}),
			nodeSpec("100003", spec.KindAssign, &spec.AssignConfig{VarName: "b", Value: spec.Source{Path: []string{"b"}, Val: 7}}),
			nodeSpec("100004", spec.KindOutput, &spec.OutputConfig{Template: "{{global_variable_app.a}}/{{global_variable_app.b}}"}), // 需要先修再注入
			nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "100004", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "100002"},
			{Source: "100002", Target: "100003"},
			{Source: "100003", Target: "100004"},
			{Source: "100004", Target: "900001"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	if out["output"] != "x/7" {
		t.Fatalf("output = %v, want x/7", out["output"])
	}
}

// M14:复合赋值（+= / <<）必须基于当前共享值累加 —— 而非只写 RHS。
// （count+=1 两次 → 2;tags<<a <<b → [a b]。此前 assign 直接写 RHS,
// 多次复合赋值互相覆盖,暴露为 count+=1;count+=1 → 1。）
func TestConvVarCompoundAssign(t *testing.T) {
	s := &spec.Spec{
		Version: spec.Version,
		Entry:   &spec.EntrySpec{ID: "100001"},
		ConvVars: []spec.ConvVarSpec{
			{Name: "count", Type: &spec.TypeSpec{Kind: "integer"}, Default: 0},
			{Name: "tags", Type: &spec.TypeSpec{Kind: "array"}, Default: []any{}},
		},
		Nodes: []spec.NodeSpec{
			nodeSpec("100001", spec.KindStart, &spec.StartConfig{}),
			nodeSpec("100002", spec.KindAssign, &spec.AssignConfig{VarName: "count", Value: spec.Source{Path: []string{"count"}, Val: 1}, Operation: "+="}),
			nodeSpec("100003", spec.KindAssign, &spec.AssignConfig{VarName: "count", Value: spec.Source{Path: []string{"count"}, Val: 1}, Operation: "+="}),
			nodeSpec("100004", spec.KindAssign, &spec.AssignConfig{VarName: "tags", Value: spec.Source{Path: []string{"tags"}, Val: "a"}, Operation: "<<"}),
			nodeSpec("100005", spec.KindAssign, &spec.AssignConfig{VarName: "tags", Value: spec.Source{Path: []string{"tags"}, Val: "b"}, Operation: "<<"}),
			nodeSpec("100006", spec.KindOutput, &spec.OutputConfig{Template: "{{global_variable_app.count}}/{{global_variable_app.tags}}"}),
			nodeSpec("900001", spec.KindEnd, &spec.EndConfig{Inputs: []spec.Ref{{Producer: "100006", Output: "output"}}}),
		},
		Edges: []spec.EdgeSpec{
			{Source: "100001", Target: "100002"},
			{Source: "100002", Target: "100003"},
			{Source: "100003", Target: "100004"},
			{Source: "100004", Target: "100005"},
			{Source: "100005", Target: "100006"},
			{Source: "100006", Target: "900001"},
		},
	}
	r := mustCompile(t, New(testRegistry()), s)
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{}))
	// M16: 复合值模板渲染走 JSON（数组 [a b] → ["a","b"],注入 prompt 可读）。
	if out["output"] != `2/["a","b"]` {
		t.Fatalf("output = %v, want 2/[\"a\",\"b\"] (复合赋值需累加/追加;复合值 JSON 渲染)", out["output"])
	}
}
