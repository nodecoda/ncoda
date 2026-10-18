package ir_test

//@feature function-inline — inline 边界 = 容器上下文屏障：return 归属由**词法
// 容器所有权**决定，函数定义体不属于调用点的任何容器（P13，2026-09-11）。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// barrierErrs 只取**错误**级诊断（未使用变量等 warning 与本族无关）。
func barrierErrs(t *testing.T, src string) []string {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, "t.dc", nil)
	var msgs []string
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Error {
			msgs = append(msgs, d.Message)
		}
	}
	if res.Program != nil {
		if _, err := ir.Build(res.Program); err != nil {
			msgs = append(msgs, err.Error())
		}
	}
	return msgs
}

const barrierSeed = `function build_url(string base) -> string {
    return base + "/api";
}

`

// inlineReturnShapes 收集每个 inline 助手函数体内 ReturnStmt 的形状三元组。
func inlineReturnShapes(t *testing.T, src string) []string {
	t.Helper()
	g := buildIR(t, workflowHeader+barrierSeed+src)
	var out []string
	var walk func(b *ir.Block)
	walk = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.InlineStmt:
				var inner func(x *ir.Block)
				inner = func(x *ir.Block) {
					if x == nil {
						return
					}
					for _, is := range x.Stmts {
						if rs, ok := is.(*ir.ReturnStmt); ok {
							out = append(out, fmtShape(rs))
						}
					}
				}
				inner(st.Body)
			case *ir.IfStmt:
				walk(st.Then)
				walk(st.Else)
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					walk(br)
				}
			case *ir.LoopStmt:
				walk(st.Body)
			case *ir.SwitchStmt:
				for _, br := range st.Branches {
					walk(br.Body)
				}
			case *ir.AttemptStmt:
				walk(st.Success)
				walk(st.Failure)
			}
		}
	}
	walk(g.Root)
	return out
}

func fmtShape(rs *ir.ReturnStmt) string {
	return "flowend=" + boolStr(rs.FlowEnd) + " end=" + boolStr(rs.End != nil) + " val=" + boolStr(rs.Value != nil)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestInlineReturnStaysFunctionOwnedInEveryContainer 是 P13 的归属矩阵：
// 「函数体内的 return = 该函数的值回流」必须对**任意宿主容器**成立——顶层、
// if 两臂、value-producing for 体、while 体、语句 for 体、parallel 分支、
// attempt 分支、循环内 if、深度 2（for 体内 while 体内）。
//
// 修复前（S2-gate 实测）：循环体内 ⇒ 被夺权为调用方循环的 M17 loop return
// （`function "build_url" produces no value`，fail loud）；parallel 分支内 ⇒
// 被判成「终止整个流程」（`flowend=true`，**无诊断**，运行期从助手函数里把
// 整个工作流提前结束）。同一条规则在两种宿主下产生两种错，故断言必须落在
// **图形状**上，而不是「有/无诊断」。
func TestInlineReturnStaysFunctionOwnedInEveryContainer(t *testing.T) {
	cases := []struct{ name, src string }{
		{"top", `function main(string u) -> string {
    let t = build_url(u);
    return t;
}
`},
		{"if-then", `function main(string u) -> string {
    var s;
    if (u != "") { s = build_url(u); } else { s = "x"; }
    return s;
}
`},
		{"if-else", `function main(string u) -> string {
    var s;
    if (u == "") { s = "x"; } else { s = build_url(u); }
    return s;
}
`},
		{"for-expr-body", `function main(string u) -> string {
    array<string> xs = ["a"];
    let ys = for (x in xs) {
        let s = build_url(x);
        yield s;
    };
    return ys[0];
}
`},
		{"while-body", `function main(string u) -> string {
    int i = 0;
    string acc = "";
    while (i < 2) limit 5 {
        let t = build_url(u);
        acc = t;
        i += 1;
    }
    return acc;
}
`},
		{"for-stmt-body", `function main(string u) -> string {
    array<string> xs = ["a"];
    for (x in xs) {
        let t = build_url(x);
    }
    return "x";
}
`},
		{"parallel-branch", `function main(string u) -> string {
    parallel {
        r1: { let t = build_url(u); }
    }
    return t;
}
`},
		{"attempt-branch", `function main(string u) -> string {
    attempt http("GET", "https://x/", {}) as response {
        success { let t = build_url(u); return t; }
        failure(error) { return "e"; }
    }
}
`},
		{"loop-nested-if", `function main(string u) -> string {
    array<string> xs = ["a"];
    let ys = for (x in xs) {
        var s;
        if (x != "") { s = build_url(x); } else { s = x; }
        yield s;
    };
    return ys[0];
}
`},
		{"depth2-while-in-for", `function main(string u) -> string {
    array<string> xs = ["a"];
    let ys = for (x in xs) {
        int i = 0;
        string acc = "";
        while (i < 2) limit 5 {
            let t = build_url(x);
            acc = t;
            i += 1;
        }
        yield acc;
    };
    return ys[0];
}
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if msgs := barrierErrs(t, workflowHeader+barrierSeed+c.src); len(msgs) > 0 {
				t.Fatalf("diagnostics: %v", msgs)
			}
			shapes := inlineReturnShapes(t, c.src)
			if len(shapes) == 0 {
				t.Fatal("no inline body ReturnStmt found")
			}
			for _, s := range shapes {
				if s != "flowend=false end=false val=true" {
					t.Errorf("inline return shape = %q, want the function-owned shape "+
						"(flowend=false end=false val=true) — a loop/parallel host must not "+
						"take ownership of the helper's return", s)
				}
			}
		})
	}
}

// TestInlineInsideParallelBranchIsNotFlowEnd 锁定 S2-gate 抓到的**静默**面：
// 助手函数在 parallel 分支内被 inline 时，其 return 曾被写成 flow-end
// （直连唯一 end 900001）——编译零诊断，运行期等于把整个工作流提前结束。
func TestInlineInsideParallelBranchIsNotFlowEnd(t *testing.T) {
	src := `function main(string u) -> string {
    parallel {
        r1: { let t = build_url(u); }
    }
    return t;
}
`
	shapes := inlineReturnShapes(t, src)
	if len(shapes) != 1 || shapes[0] != "flowend=false end=false val=true" {
		t.Fatalf("inline return inside parallel branch = %v, want the function-owned shape "+
			"(a flow-end here silently terminates the whole workflow at runtime)", shapes)
	}
}

// TestInlineFlagsDoNotLeakIntoCallerBranch 锁定 S2-gate 抓到的第二个静默面：
// 助手函数内的 return 曾把 `branchFlowReturns` 置位并**泄漏给调用方分支**，
// 使调用方 then 分支被误判为 flow-return ⇒ 多出一个 phi 汇聚（`IF merge`）。
// 判据：then 臂只有 parallel + inline、无臂内赋值时，调用方 if **不得**有 merge。
func TestInlineFlagsDoNotLeakIntoCallerBranch(t *testing.T) {
	src := `function main(string u) -> string {
    if (u != "") {
        parallel {
            r1: { let t = build_url(u); }
        }
    }
    return "x";
}
`
	g := buildIR(t, workflowHeader+barrierSeed+src)
	if merges := nodesOf(g)[ir.KindAggregate]; len(merges) != 0 {
		t.Fatalf("caller if gained %d phi merge(s) from the inlined helper's return; "+
			"the helper's branch-flow flag leaked into the caller branch", len(merges))
	}
}

// TestInlineDoesNotBorrowCallerLoopState 锁定最隐蔽的静默面：助手函数体内的
// 赋值曾被重定向成**调用方循环**的状态赋值（`Assign{LoopState:true,
// LoopID:<调用方循环>}`），只要变量与调用方循环状态**同名**即触发。判据：
// (1) inline 助手函数体内不得出现 LoopState 赋值；(2) 调用方循环不得因助手
// 函数内的 return 而获得 returnValue。
func TestInlineDoesNotBorrowCallerLoopState(t *testing.T) {
	seed := `function bump(string base) -> string {
    var i;
    i = base;
    return i;
}
`
	src := `function main(string u) -> string {
    int i = 0;
    string acc = "";
    while (i < 2) limit 5 {
        let t = bump(u);
        acc = t;
        i += 1;
    }
    return acc;
}
`
	if msgs := barrierErrs(t, workflowHeader+seed+src); len(msgs) > 0 {
		t.Fatalf("diagnostics: %v", msgs)
	}
	g := buildIR(t, workflowHeader+seed+src)
	var check func(b *ir.Block)
	check = func(b *ir.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			switch st := s.(type) {
			case *ir.InlineStmt:
				var inner func(x *ir.Block)
				inner = func(x *ir.Block) {
					if x == nil {
						return
					}
					for _, is := range x.Stmts {
						if ns, ok := is.(*ir.NodeStmt); ok {
							if as, ok2 := ns.Node.(*ir.Assign); ok2 && as.LoopState {
								t.Errorf("inline body wrote caller loop state %q (loopID=%s)", as.VarName, as.LoopID)
							}
						}
					}
				}
				inner(st.Body)
			case *ir.IfStmt:
				check(st.Then)
				check(st.Else)
			case *ir.LoopStmt:
				if st.Container.ReturnValue != nil {
					t.Errorf("caller loop %s got a returnValue from the inlined helper's return", st.Container.NodeID())
				}
				check(st.Body)
			case *ir.ParallelStmt:
				for _, br := range st.Branches {
					check(br)
				}
			}
		}
	}
	check(g.Root)
}

// TestInlineFunctionOwnLoopKeepsLoopReturnInBranchArm 锁定第四个静默面：
// 助手函数**自带循环**时，其循环内的 return 仍是该循环的 M17 loop return
// （值经 loop 输出口传出）。曾被调用方臂的 branchValueDepth/switchBranchDepth
// 抑制 ⇒ 静默丢掉 returnValue/`loop_return` 节点。
func TestInlineFunctionOwnLoopKeepsLoopReturnInBranchArm(t *testing.T) {
	seed := `function pick(string base) -> string {
    int j = 0;
    var out = "";
    while (j < 2) limit 3 {
        j += 1;
        return base + "/hit";
    }
    return out;
}
`
	cases := []struct{ name, src string }{
		{"switch-arm", `function main(string u) -> string {
    switch (u) {
        "yes": { let t = pick(u); return t; }
        default: { return "?"; }
    }
}
`},
		{"attempt-arm", `function main(string u) -> string {
    attempt http("GET", "https://x/", {}) as response {
        success { let t = pick(u); return t; }
        failure(error) { return "e"; }
    }
}
`},
		{"top-control", `function main(string u) -> string {
    let t = pick(u);
    return t;
}
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if msgs := barrierErrs(t, workflowHeader+seed+c.src); len(msgs) > 0 {
				t.Fatalf("diagnostics: %v", msgs)
			}
			g := buildIR(t, workflowHeader+seed+c.src)
			loops := nodesOf(g)[ir.KindLoop]
			if len(loops) == 0 {
				t.Fatal("no loop node (the helper's own while loop vanished)")
			}
			// 助手函数自带循环的 returnValue 必须装配（M17）。
			var hasReturnValue, hasLoopReturn bool
			for _, l := range loops {
				if l.(*ir.Loop).ReturnValue != nil {
					hasReturnValue = true
				}
			}
			if len(nodesOf(g)[ir.KindLoopReturn]) > 0 {
				hasLoopReturn = true
			}
			if !hasReturnValue || !hasLoopReturn {
				t.Errorf("helper's own loop lost its M17 return (returnValue=%v loop_return=%v) — "+
					"the caller arm's branch container suppressed it", hasReturnValue, hasLoopReturn)
			}
		})
	}
}

// TestInlineBoundaryIsIdempotentAcrossHosts 是递归自洽的「×N 不膨胀」判据：
// 同一个助手函数在**不同宿主容器**里 inline，必须产生**同一份** inline 值形状
// （否则同一源码在不同位置得不同 IR ⇒ 多轮 roundtrip 必漂）。
func TestInlineBoundaryIsIdempotentAcrossHosts(t *testing.T) {
	hosts := map[string]string{
		"top": `function main(string u) -> string {
    let t = build_url(u);
    return t;
}
`,
		"forexpr": `function main(string u) -> string {
    array<string> xs = ["a"];
    let ys = for (x in xs) { let s = build_url(x); yield s; };
    return ys[0];
}
`,
		"parallel": `function main(string u) -> string {
    parallel { r1: { let t = build_url(u); } }
    return t;
}
`,
	}
	var want []string
	for name, src := range hosts {
		shapes := inlineReturnShapes(t, src)
		joined := strings.Join(shapes, "|")
		if want == nil {
			want = shapes
			continue
		}
		if joined != strings.Join(want, "|") {
			t.Errorf("host %q inline shape = %v, want %v (same helper, same shape)", name, shapes, want)
		}
	}
}
