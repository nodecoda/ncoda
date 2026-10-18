package passes

// ast_rebuild_form_preservation_contract_test.go —— AST 重写面的**多形保真**判据
// （R-146 家族，2026-09-29）。
//
// 规则一句话：**「一个意思两种拼写」的那些字段是节点身份的一部分，任何重写都必须
// 逐字保形**。这些字段的闭集（一处定义 = `internal/ast` 的多形构造器；本文件只做
// 投影，不新增枚举）：
//
//	*ast.YieldStmt    → Port()              裸 `yield v` = 默认口 ""（"output"）
//	*ast.ParallelStmt → BranchNames()       与 Branches 按索引对齐；"" = 无名分支
//	*ast.ForStmt      → LoopVars/LoopSources/IsParallel/Concurrency/OnError   zip 多形
//	*ast.ForExpr      → 同上
//	*ast.ParallelForExpr → 同上 + Concurrency/OnError
//	*ast.ReturnStmt   → Streaming()         尾置投递开关
//	*ast.OutputStmt   → Streaming()         同上
//	*ast.VarDeclStmt  → Name()/IsMutable()
//
// 为什么必须机器挡（一手，2026-09-29）：`FoldConstCalls` 重建 `yield` 时用了单形
// 构造器 `ast.NewYieldStmt` ⇒ `yield img = x; yield audio = x;` 被压成默认口
// "output" ⇒ 多口聚合退化成单口 ⇒ `agg.audio` 静默变 any ⇒ 下游 TYPE_MISMATCH
// （现场 = `TestHoistedAggregatePortIdentityIndependentOfBindingForm`）。
// 同族（同一次普查发现，同一次修）：`const_substitution` 的 yield 口、
// `const_substitution` / `normalize` / `desugar` 的 parallel 分支名。
//
// 判据形态 = **投影不变式**（不是逐点断言）：同一份源码 parse 两次 —— 一份过真重写面
// （`SubstituteConstants` + `FoldConstCalls`，即 `runPipeline` 顶部的两步），另一份
// 不动 —— 两棵树的**多形投影**必须逐行相同。投影走 `ast.Walk` **全树**，所以新节点
// 类型上的字段丢失也会被看见（闭集不靠人列容器）。
//
// 撤实现必红（一手实测）：任一处的多形构造器换回单形 ⇒ 投影行不同 ⇒ 红。

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/parser"
)

// formSrc 是**多形夹具**：一份源码同时含全部多形字段（且带 const 与可折叠调用，
// 保证两道重写都真的跑起来）。
const formSrc = `@language nodecoda/1
@mode workflow
const K = "0";
function main(array<int> xs) -> string {
    int acc = 0;
    var mut = 1;
    for (a, b in zip(xs, xs)) {
        acc = acc + int(K);
    }
    let agg = for (x in xs) {
        yield img = x;
        yield audio = x;
    };
    parallel {
        left: { acc = acc + len(agg.audio); }
        right: { mut = mut + 1; }
    }
    output("mid") streaming;
    return template "{{agg.img}}" streaming;
}
`

// formLine 把节点的**多形字段**投影成一行（不在闭集里的节点 → ""，不参与比对：
// 常量代入 / 折叠会合法地改动它们）。
func formLine(n ast.Node) string {
	switch v := n.(type) {
	case *ast.YieldStmt:
		return "yield port=" + v.Port()
	case *ast.ParallelStmt:
		return "parallel names=" + strings.Join(v.BranchNames(), ",")
	case *ast.ForStmt:
		return fmt.Sprintf("for vars=%s srcs=%d parallel=%v conc=%d onerr=%s",
			strings.Join(v.LoopVars(), ","), len(v.LoopSources()), v.IsParallel(), v.Concurrency(), v.OnError())
	case *ast.ForExpr:
		return fmt.Sprintf("forexpr vars=%s srcs=%d", strings.Join(v.LoopVars(), ","), len(v.LoopSources()))
	case *ast.ParallelForExpr:
		return fmt.Sprintf("pforexpr vars=%s srcs=%d conc=%d onerr=%s",
			strings.Join(v.LoopVars(), ","), len(v.LoopSources()), v.Concurrency(), v.OnError())
	case *ast.ReturnStmt:
		return fmt.Sprintf("return streaming=%v", v.Streaming())
	case *ast.OutputStmt:
		return fmt.Sprintf("output streaming=%v", v.Streaming())
	case *ast.VarDeclStmt:
		return fmt.Sprintf("vardecl name=%s mutable=%v", v.Name(), v.IsMutable())
	}
	return ""
}

type formProjector struct{ lines []string }

func (p *formProjector) Visit(n ast.Node) ast.Visitor {
	if line := formLine(n); line != "" {
		p.lines = append(p.lines, line)
	}
	return p
}

func formProject(prog *ast.Program) []string {
	p := &formProjector{}
	ast.Walk(p, prog)
	return p.lines
}

// TestAstRebuildPreservesMultiFormFields —— 投影不变式本体 + 自检（非空转）。
func TestAstRebuildPreservesMultiFormFields(t *testing.T) {
	refProg, perrs := parser.ParseSource(formSrc, "form_ref.ncoda")
	if len(perrs) != 0 {
		t.Fatalf("夹具解析失败（判据自身要先站得住）：%v", perrs)
	}
	want := formProject(refProg)

	// 判据自检：投影必须真的看见每一类多形字段，否则下面的比对是空转。
	for _, marker := range []string{
		"yield port=img", "yield port=audio", "parallel names=left,right",
		"for vars=a,b srcs=2", "return streaming=true", "output streaming=true",
		"vardecl name=mut mutable=true",
	} {
		found := false
		for _, l := range want {
			if strings.Contains(l, marker) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("判据空转：投影里没有 %q（夹具没覆盖这条多形字段）\n投影：%v", marker, want)
		}
	}

	// 真重写面：runPipeline 顶部的两步（常量代入 + 常量调用折叠）。
	gotProg, perrs := parser.ParseSource(formSrc, "form_ref.ncoda")
	if len(perrs) != 0 {
		t.Fatalf("夹具解析失败：%v", perrs)
	}
	SubstituteConstants(gotProg)
	FoldConstCalls(gotProg)
	got := formProject(gotProg)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("重写改动了多形字段（一个意思两种拼写 ⇒ 静默丢口/丢标签）：\n--- 重写前 ---\n%s\n--- 重写后 ---\n%s",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

// TestAstRebuildPreservesMultiFormFieldsThroughPipeline —— 同一投影不变式，但把
// **全默认管线**（含 desugar / normalize / binder / 各 pass）也纳入：这三处同样是重写面。
func TestAstRebuildPreservesMultiFormFieldsThroughPipeline(t *testing.T) {
	refProg, perrs := parser.ParseSource(formSrc, "form_ref.ncoda")
	if len(perrs) != 0 {
		t.Fatalf("夹具解析失败：%v", perrs)
	}
	want := formProject(refProg)

	res := DefaultPipeline().Run(formSrc, "form_pipeline.ncoda", nil)
	if res.HasErrors() {
		t.Fatalf("夹具必须干净编译（否则投影比对没意义）：%v", res.Diagnostics)
	}
	got := formProject(res.Program.Program)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("全管线改动了多形字段：\n--- 重写前 ---\n%s\n--- 重写后 ---\n%s",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}
