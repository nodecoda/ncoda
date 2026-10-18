//go:build shelved

// 搁置面（AGENTS.md「开发主线与平台优先级」2026-09-11 拍板）：dify / coze-studio /
// python golden 语料面的测试结果**不是**主线（coze-biz）回归判据。本文件按门禁治理
// `内部治理文档（G-B 条）` G-B「快照/搁置面不阻塞默认门禁」移到默认门禁之外，
// 以免"常驻红基线"淹没主线真信号（实测：30 条常驻红里 23 条是搁置面）。
//
// 跑它：`go test -tags shelved ./...`（或 `make shelved`）；分类：`scripts/gov/triage.py`。
// 摘掉本 tag 的条件 = 该面重新成为主线判据（届时先修红再摘）。

package dify_test

// parallel_flow_end_render_test.go — P5 #4 回归锁定（dify 渲染面）。
//
// parallel 分支内 return = 流程结束（flow-end）：分支值直连 end、分支不
// 并入 parallel 栅栏、flow-end 之后的语句被投影剪掉（全 return 时）。
// 断言走 production 链路（passes → ir → flow.Project → RenderFromIR），
// 不冻结 golden（dify 形状由图不变量锁定：单 end、分支直连、无幽灵节点）。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

func renderProbe(t *testing.T, name string) (*flow.Projection, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "compiler", "testdata", "examples", "probes", name))
	if err != nil {
		t.Fatal(err)
	}
	res := passes.DefaultPipeline().Run(string(b), name, nil)
	if res.HasErrors() {
		t.Fatalf("frontend: %v", res.Errors())
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatal(err)
	}
	p, err := flow.Project(g)
	if err != nil {
		t.Fatal(err)
	}
	got, err := dify.RenderFromIR(g, "workflow")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return p, serialize(t, got)
}

// TestDifyParallelSingleReturnFlowEnd: 单 return 分支 → 值直连 end；非
// return 分支尾部仍进栅栏（after 保留，平台形状需要连通）。
func TestDifyParallelSingleReturnFlowEnd(t *testing.T) {
	p, yml := renderProbe(t, "zz_parallel_return_probe.ncoda")
	// projection: left 分支 code → 900001（flow-end），right 分支 code →
	// after text → 900001。
	var leftToEnd, leftToAfter bool
	for _, e := range p.Edges {
		if e.SourceNodeID == "100002" && e.TargetNodeID == "900001" {
			leftToEnd = true
		}
		if e.SourceNodeID == "100002" && e.TargetNodeID == "100004" {
			leftToAfter = true
		}
	}
	if !leftToEnd {
		t.Errorf("flow-end branch 100002 must wire straight to end 900001")
	}
	if leftToAfter {
		t.Errorf("flow-end branch 100002 must NOT enter the after-parallel continuation: %v", p.Edges)
	}
	// 渲染必须成功且含唯一 start/end 叶子。
	if got := countStr(yml, "type: start"); got != 1 {
		t.Errorf("start nodes = %d, want 1", got)
	}
	if got := countStr(yml, "type: end"); got != 1 {
		t.Errorf("end nodes = %d, want 1", got)
	}
}

// TestDifyParallelMultiReturnFlowEnd: 多 value-return 分支 → 各分支直连
// 同一 end（共享 900001）；after 语句被投影剪掉（流程已终止）。
func TestDifyParallelMultiReturnFlowEnd(t *testing.T) {
	p, yml := renderProbe(t, "zz_parallel_multi_return_probe.ncoda")
	var leftToEnd, rightToEnd int
	for _, e := range p.Edges {
		if e.SourceNodeID == "100002" && e.TargetNodeID == "900001" {
			leftToEnd++
		}
		if e.SourceNodeID == "100003" && e.TargetNodeID == "900001" {
			rightToEnd++
		}
	}
	if leftToEnd != 1 || rightToEnd != 1 {
		t.Errorf("each return branch must wire exactly once to end 900001 (left=%d right=%d)", leftToEnd, rightToEnd)
	}
	// after 语句（100004 text）被 dead-path 剪掉：不在 projection。
	for _, n := range p.Nodes {
		if n.NodeID() == "100004" {
			t.Errorf("dead-path after node 100004 must be pruned (flow terminated)")
		}
	}
	if got := countStr(yml, "type: start"); got != 1 {
		t.Errorf("start nodes = %d, want 1", got)
	}
	if got := countStr(yml, "type: end"); got != 1 {
		t.Errorf("end nodes = %d, want 1 (shared 900001)", got)
	}
}

func countStr(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
