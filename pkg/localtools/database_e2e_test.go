package localtools_test

// 端到端判据：一段 ncoda 源里 insert 一行、再 select 回来，值必须一致。
// 走的是完整链：frontend → IR → flow → spec.v1 → compile（tool 节点）
// → localtools 的 SQLite 实现。只测 store 层证明不了这条链是通的。

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/localtools"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/compile"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/tool"
)

const dbRoundTripSrc = `@language nodecoda/1
@mode workflow
import "database";

function main(string v) -> array<any> {
    let w = database.insert({ "tables": ["demo"], "values": [[{"name":"fieldID","input":{"value":"k"}},{"name":"fieldValue","input":{"value": v}}]] });
    let r = database.select({ "tables": ["demo"], "columns": [{"fieldID":"k"}], "rows": 10 });
    return r.outputList;
}
`

func TestDatabaseInsertThenSelectFromNcodaSource(t *testing.T) {
	caps, err := tool.LoadCapabilitySearchPaths([]string{
		filepath.Join("..", "..", "libraries"),
	})
	if err != nil {
		t.Fatalf("capability headers: %v", err)
	}
	pipeline := passes.DefaultPipeline().WithToolRegistry(caps)
	res := pipeline.Run(dbRoundTripSrc, "db_roundtrip.ncoda", nil)
	if res.HasErrors() {
		for _, d := range res.Errors() {
			t.Logf("frontend: %s", d.Message)
		}
		t.Fatal("frontend errors")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir: %v", err)
	}
	p, err := flow.Project(g)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	s, err := spec.Build(g, p)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}

	m := plugin.New()
	if _, err := localtools.Register(m, localtools.Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("localtools: %v", err)
	}
	c := compile.New(nodes.StdRegistry(), compile.WithDeps(nodes.Deps{Tools: m}))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := r.Invoke(context.Background(), map[string]any{"v": "hello"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	rows := findList(out, "output")
	if len(rows) != 1 {
		t.Fatalf("out = %#v, want one row", out)
	}
	rec, ok := rows[0].(map[string]any)
	if !ok || rec["k"] != "hello" {
		t.Fatalf("row = %#v, want k=hello", rows[0])
	}
}

func findList(m map[string]any, key string) []any {
	if v, ok := m[key].([]any); ok {
		return v
	}
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			if got := findList(sub, key); got != nil {
				return got
			}
		}
	}
	return nil
}
