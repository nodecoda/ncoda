package ir_test

// Loop 聚合变量成员链 lowering 契约（D2 family C：S13yangsheng agg_2.link 漂移）。
// 值循环聚合变量 agg 上的字段链 = 逐元素投影，compile 必须组合成
// <端口>.<成员...> dotted path，不得吞掉任何一段。

import (
	"fmt"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

func collectRefInputs(g *ir.Graph, loopID string) []string {
	var got []string
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.NodeStmt:
				for _, src := range nodeSources(st.Node) {
					if src.BlockID == loopID {
						got = append(got, fmt.Sprintf("%s(%s).%s -> %v", st.Node.Kind(), st.Node.NodeID(), src.Name, src.Path))
					}
				}
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
	return got
}

func nodeSources(n ir.Node) []ir.Source {
	switch nd := n.(type) {
	case *ir.CodeRun:
		return nd.Inputs
	case *ir.LLMCall:
		return nd.Inputs
	case *ir.HTTPCall:
		return nd.Inputs
	}
	return nil
}
func findLoopID(t *testing.T, g *ir.Graph) string {
	t.Helper()
	var loopID string
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil || loopID != "" {
			return
		}
		for _, s := range b.Stmts {
			if ls, ok := s.(*ir.LoopStmt); ok {
				loopID = ls.ID
				return
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
	if loopID == "" {
		t.Fatal("no loop found")
	}
	return loopID
}

// refNamesTo returns every dataflow source Name targeting loopID (dotted
// producer path). Assertion target: the loop-aggregate member chain must
// survive IR build as a full dotted path.
func refNamesTo(g *ir.Graph, loopID string) []string {
	var got []string
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.NodeStmt:
				for _, src := range nodeSources(st.Node) {
					if src.BlockID == loopID {
						got = append(got, src.Name)
					}
				}
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
	return got
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestLoopAggMemberChainNamedPortContract：命名单口循环（yield outputList =
// v），聚合变量成员链 a.data.link 必须组合成 outputList.data.link
// （曾丢 data → outputList.link，S13yangsheng B1 逐字节复现）。
func TestLoopAggMemberChainNamedPortContract(t *testing.T) {
	src := workflowHeader + `
function main(array<string> items) -> string {
    let a = for (it in items) {
        let v = { "data": { "link": it } };
        yield outputList = v;
    };
    let m = { "u": a.data.link };
    return "x";
}
`
	g := buildIR(t, src)
	loopID := findLoopID(t, g)
	names := refNamesTo(g, loopID)
	if !hasName(names, "outputList.data.link") {
		t.Fatalf("loop-aggregate member chain a.data.link: got %v, want outputList.data.link", names)
	}
}

// TestLoopAggMemberChainDefaultPortContract：默认口值循环（yield v），聚合
// 变量成员链同样必须组合成 output.data.link。
func TestLoopAggMemberChainDefaultPortContract(t *testing.T) {
	src := workflowHeader + `
function main(array<string> items) -> string {
    let a = for (it in items) {
        let v = { "data": { "link": it } };
        yield v;
    };
    let m = { "u": a.data.link };
    return "x";
}
`
	g := buildIR(t, src)
	loopID := findLoopID(t, g)
	names := refNamesTo(g, loopID)
	if !hasName(names, "output.data.link") {
		t.Fatalf("default-port loop-aggregate member chain: got %v, want output.data.link", names)
	}
}

// TestLoopAggNestedContainerContract：递归自洽——外层容器体内再嵌套值循环，
// 内层聚合变量成员链在嵌套深度同样组合完整 dotted path（不依赖容器层数）。
func TestLoopAggNestedContainerContract(t *testing.T) {
	src := workflowHeader + `
function main(array<string> items) -> string {
    let outer = for (it in items) {
        let inner = for (x in [it]) {
            let v = { "data": { "link": x } };
            yield outputList = v;
        };
        let m = { "u": inner.data.link };
        yield result = m;
    };
    return "x";
}
`
	g := buildIR(t, src)
	// inner loop id: the loop whose OutTypes contains outputList of records
	// with data.link. outer loop's OutTypes element is the record m.
	var innerID string
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil || innerID != "" {
			return
		}
		for _, s := range b.Stmts {
			if ls, ok := s.(*ir.LoopStmt); ok {
				if _, has := ls.OutTypes["outputList"]; has {
					if _, hasResult := ls.OutTypes["result"]; !hasResult {
						innerID = ls.ID
						return
					}
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
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					walk(br)
				}
			}
		}
	}
	walk(g.Root)
	if innerID == "" {
		t.Fatal("no inner loop found")
	}
	names := refNamesTo(g, innerID)
	if !hasName(names, "outputList.data.link") {
		t.Fatalf("nested inner loop-aggregate member chain: got %v, want outputList.data.link", names)
	}
}
