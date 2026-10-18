package decompile

// parse_test.go — dify YAML → WorkflowAST 解析（Phase A）。

import (
	"os"
	"testing"
)

// TestParseDifyOfficialFixture:解析 dify 官方测试语料（各节点类型面）。
// 用环境变量 DIFY_FIXTURES 指向语料目录;未设置时跳过（CI 无外部依赖）。
func TestParseDifyOfficialFixture(t *testing.T) {
	dir := os.Getenv("DIFY_FIXTURES")
	if dir == "" {
		t.Skip("set DIFY_FIXTURES=<dify>/api/tests/fixtures/workflow to run")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	parsed := 0
	for _, e := range entries {
		if e.IsDir() || !hasYAMLExt(e.Name()) {
			continue
		}
		b, err := os.ReadFile(dir + "/" + e.Name())
		if err != nil {
			t.Fatalf("%s: read: %v", e.Name(), err)
		}
		ast, err := ParseWorkflow(string(b))
		if err != nil {
			t.Errorf("%s: parse: %v", e.Name(), err)
			continue
		}
		if len(ast.Graph.Nodes) == 0 {
			t.Errorf("%s: no nodes", e.Name())
		}
		parsed++
	}
	if parsed == 0 {
		t.Fatalf("no yaml fixtures found in %s", dir)
	}
	t.Logf("parsed %d fixtures", parsed)
}

func hasYAMLExt(name string) bool {
	return len(name) > 4 && (name[len(name)-4:] == ".yml" || name[len(name)-4:] == ".yaml")
}
