package parser

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ast"
)

// Subflow 顶层声明语法：subflow <name> = ("id", "version") { input/output 契约 }。
func TestParseSubflowDecl(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow

type 歌词回调 = { string text; };

subflow 歌词时间线 = ("7567215314397528118", "v0.0.1") {
  input:  { string 歌词; int 时间; },
  output: { string text; 歌词回调 回调; }
};

function main(string 歌词) -> string {
  let v = 歌词时间线({ 歌词: 歌词 });
  return v.text;
}`
	prog := conformOK(t, src)
	decls := prog.SubflowDecls()
	if len(decls) != 1 {
		t.Fatalf("want 1 subflow decl, got %d", len(decls))
	}
	d := decls[0]
	if d.Name() != "歌词时间线" {
		t.Errorf("name = %q, want 歌词时间线", d.Name())
	}
	if d.WorkflowID() != "7567215314397528118" {
		t.Errorf("workflow id = %q", d.WorkflowID())
	}
	if d.WorkflowVersion() != "v0.0.1" {
		t.Errorf("version = %q", d.WorkflowVersion())
	}
	if len(d.InputFields()) != 2 {
		t.Fatalf("input fields = %d, want 2", len(d.InputFields()))
	}
	if d.InputFields()[0].Name() != "歌词" {
		t.Errorf("input[0] name = %q", d.InputFields()[0].Name())
	}
	if len(d.OutputFields()) != 2 {
		t.Fatalf("output fields = %d, want 2", len(d.OutputFields()))
	}
	if d.OutputFields()[1].Name() != "回调" {
		t.Errorf("output[1] name = %q", d.OutputFields()[1].Name())
	}
}

// 缺少 output / 空 id / 未知标签 / 重复 input = 语法错误。
func TestParseSubflowDeclErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"missing output", `subflow s = ("1", "v1") { input: { string a; } };`},
		{"empty id", `subflow s = ("", "v1") { input: { string a; }, output: { string b; } };`},
		{"unknown label", `subflow s = ("1", "v1") { foo: { string a; }, output: { string b; } };`},
		{"dup input", `subflow s = ("1", "v1") { input: { string a; }, input: { string b; }, output: { string c; } };`},
		{"dup name", "subflow s = (\"1\", \"v1\") { input: { string a; }, output: { string b; } };\nsubflow s = (\"2\", \"v1\") { input: { string a; }, output: { string b; } };"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conformErr(t, tc.src, 1)
		})
	}
}

// Program 收集 + 访问器。
func TestProgramSubflowDecls(t *testing.T) {
	src := `subflow a = ("1", "v1") { input: { string x; }, output: { string y; } };
subflow b = ("2", "v2") { input: { int z; }, output: { int w; } };`
	prog := conformOK(t, src)
	if got := len(prog.SubflowDecls()); got != 2 {
		t.Fatalf("SubflowDecls() = %d, want 2", got)
	}
	if prog.SubflowDecls()[1].Name() != "b" {
		t.Errorf("decls[1].Name() = %q", prog.SubflowDecls()[1].Name())
	}
}

var _ = ast.SubflowDecl{}
