package ir_test

// D2 family D：值循环 yield 字面量（数组/空数组）与多口聚合 selector。
// 见 docs/ideas-todo/2026-09-08-d2-family-C-loopagg-member-chain.md §4
// （X212×2 / S2xuankushidan caption_list 端口 string↔list<string> 逐轮翻转）。
//@feature for-expr — 值循环（yield 字面量绑定类型化 result 端口 + 多口 selector）

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

func loopByID(t *testing.T, g *ir.Graph, id string) *ir.LoopStmt {
	t.Helper()
	var found *ir.LoopStmt
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil || found != nil {
			return
		}
		for _, s := range b.Stmts {
			if ls, ok := s.(*ir.LoopStmt); ok {
				if ls.ID == id {
					found = ls
					return
				}
				walk(ls.Body)
			}
			switch st := s.(type) {
			case *ir.IfStmt:
				walk(st.Then)
				walk(st.Else)
			case *ir.AttemptStmt:
				walk(st.Success)
				walk(st.Failure)
			case *ir.LoopStmt:
				walk(st.Body)
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					walk(br)
				}
			}
		}
	}
	walk(g.Root)
	if found == nil {
		t.Fatalf("loop %s not found", id)
	}
	return found
}

// TestLoopYieldLiteralBindsTypedResultPort：yield 数组字面量（[item] / []）
// 的循环输出必须绑定物化 CodeRun 的类型化 "result" 端口（值位置与 let
// 绑定一致）——绑 "output" 标量口会让端口 items 退化成 string（X212
// caption_list B1 侧），逐轮 string↔list<string> 翻转。
func TestLoopYieldLiteralBindsTypedResultPort(t *testing.T) {
	src := workflowHeader + `
function main(array<string> items) -> string {
    let agg = for (item in items) {
        yield audio_list = [item];
        yield caption_list = [];
    };
    return "x";
}
`
	g := buildIR(t, src)
	loopID := findLoopID(t, g)
	loop := loopByID(t, g, loopID)
	t.Logf("OutTypes: %v", loop.OutTypes)
	for _, s := range loop.OutputSources {
		t.Logf("OutputSource Path=%v Name=%q BlockID=%q", s.Path, s.Name, s.BlockID)
	}
	srcByName := map[string][]string{}
	for _, s := range loop.OutputSources {
		srcByName[s.Path[0]] = append(srcByName[s.Path[0]], s.Name)
	}
	for _, port := range []string{"audio_list", "caption_list"} {
		names := srcByName[port]
		ok := false
		for _, n := range names {
			if n == "result" {
				ok = true
			}
		}
		if !ok {
			t.Errorf("loop output %s source name = %v, want result (typed array port; got scalar output port)", port, names)
		}
		if ot := loop.OutTypes[port]; ot == nil || ot.Kind != "list" || ot.Elem == nil || ot.Elem.Kind != "list" {
			t.Errorf("loop output %s type = %v, want list<list<string>>", port, ot)
		}
	}
}

// TestLoopAggMultiPortSelectorContract：多口值循环聚合第一段 = 端口
// selector，必须解析到对应声明端口（不依赖端口名排序）；selector 上再带
// 成员链 = 该端口上的 dotted path。空字面量/数组字面量端口 schema 稳定
// 后（同族 yield-literal 修复）启用——曾因 unmask string↔list<string>
// 翻转而跳过（family C 记录），根因已归位到 yield-literal 绑错端口。
func TestLoopAggMultiPortSelectorContract(t *testing.T) {
	src := workflowHeader + `
function main(array<string> items) -> string {
    let a = for (it in items) {
        let v = { "data": { "link": it } };
        yield outputList = v;
        yield duration_list = [it];
    };
    let m1 = { "u": a.duration_list };
    let m2 = { "u": a.outputList.data.link };
    return "x";
}
`
	g := buildIR(t, src)
	loopID := findLoopID(t, g)
	names := refNamesTo(g, loopID)
	if !hasName(names, "duration_list") {
		t.Errorf("multi-port selector a.duration_list: got %v, want duration_list", names)
	}
	if !hasName(names, "outputList.data.link") {
		t.Errorf("multi-port selector a.outputList.data.link: got %v, want outputList.data.link", names)
	}
}
