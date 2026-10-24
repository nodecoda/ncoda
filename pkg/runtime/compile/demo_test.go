package compile

// demo_test.go — 简单端到端演示:ncoda 源码 → IR → Project → spec → Eino 图
// → 运行。展示分支门语义:每次运行只有选中的分支目标执行。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func TestDemoScoreLevel(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(int score) {
    var level = score >= 60 ? "pass" : "fail";
    return level;
}
`
	// 1) 前端解析
	res := passes.DefaultPipeline().Run(src, "demo.ncoda", nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatalf("frontend errors: %v", res.Errors())
	}
	// 2) IR 构建 + 投影
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir-build: %v", err)
	}
	p, err := flow.Project(g)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	// 3) spec.v1 文档
	s, err := spec.Build(g, p)
	if err != nil {
		t.Fatalf("spec build: %v", err)
	}
	// 4) 编译成 Eino 可运行图
	r := mustCompile(t, New(testRegistry()), s)

	// 5) 运行两个场景
	pass := UnwrapOutput(mustInvoke(t, r, map[string]any{"score": float64(85)}))
	fail := UnwrapOutput(mustInvoke(t, r, map[string]any{"score": float64(40)}))
	t.Logf("score=85 → %q", pass["output"])
	t.Logf("score=40 → %q", fail["output"])

	if pass["output"] != "pass" {
		t.Fatalf("score=85 → %q, want pass", pass["output"])
	}
	if fail["output"] != "fail" {
		t.Fatalf("score=40 → %q, want fail", fail["output"])
	}
	// 分支门:85 时只有 pass 分支执行,fail 分支零执行;反之亦然
	if n := textRunCount("fail"); n != 1 {
		t.Fatalf("fail 分支执行 %d 次,期望 1(仅 score=40 时)", n)
	}
	t.Logf("分支门验证:pass 分支执行 %d 次(仅 score=85),fail 分支执行 %d 次(仅 score=40)",
		textRunCount("pass"), textRunCount("fail"))
}
