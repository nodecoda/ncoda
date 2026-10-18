package pygen

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/pyast"
)

// byteEquivTest records a calcBuiltinPython shim template and the
// pyast lowering that must produce byte-identical output.
type byteEquivTest struct {
	name    string
	builtin string
	// args are the pyast.Expr arguments to pass to LowerBuiltin.
	args []pyast.Expr
	// want is the expected output from the current calcBuiltinPython shim.
	// Each caller must copy-paste the shim template verbatim, with the
	// same argument placeholders used in args.
	want string
}

func byteEquivCases() []byteEquivTest {
	return []byteEquivTest{
		{
			name:    "len",
			builtin: "len",
			args:    []pyast.Expr{name("x")},
			want:    "len(x)",
		},
		{
			name:    "trim",
			builtin: "trim",
			args:    []pyast.Expr{name("s")},
			want:    "s.strip()",
		},
		{
			name:    "join",
			builtin: "join",
			args:    []pyast.Expr{name("items"), name("sep")},
			want:    "sep.join(items)",
		},
		{
			name:    "to_string",
			builtin: "to_string",
			args:    []pyast.Expr{name("x")},
			want:    "str(x)",
		},
		{
			name:    "int",
			builtin: "int",
			args:    []pyast.Expr{name("x")},
			want:    "int(x)",
		},
		{
			name:    "float",
			builtin: "float",
			args:    []pyast.Expr{name("x")},
			want:    "float(x)",
		},
		{
			name:    "replace",
			builtin: "replace",
			args:    []pyast.Expr{name("s"), name("old"), name("new")},
			want:    "s.replace(old, new)",
		},
		{
			name:    "split",
			builtin: "split",
			args:    []pyast.Expr{name("s"), name("delim")},
			want:    "s.split(delim)",
		},
		{
			name:    "repeat",
			builtin: "repeat",
			args:    []pyast.Expr{name("v"), name("n")},
			want:    "[v] * n",
		},
		{
			name:    "div",
			builtin: "div",
			args:    []pyast.Expr{name("a"), name("b")},
			want:    "(a // b)",
		},
		{
			name:    "concat",
			builtin: "concat",
			args:    []pyast.Expr{name("a"), name("b")},
			want:    "(a + b)",
		},
		{
			name:    "contains",
			builtin: "contains",
			args:    []pyast.Expr{name("xs"), name("x")},
			want:    "(x in xs)",
		},
		{
			name:    "range",
			builtin: "range",
			args:    []pyast.Expr{name("a"), name("b")},
			want:    "list(range(a, b))",
		},
		{
			name:    "enumerate",
			builtin: "enumerate",
			args:    []pyast.Expr{name("xs")},
			want:    `[{"index": i, "value": v} for i, v in enumerate(xs)]`,
		},
		{
			name:    "flatten",
			builtin: "flatten",
			args:    []pyast.Expr{name("xs")},
			want:    `[str(x) for g in (xs or []) if isinstance(g, list) for x in g]`,
		},
		{
			name:    "first_non_empty",
			builtin: "first_non_empty",
			args:    []pyast.Expr{name("xs")},
			want:    `next((str(u) for u in (xs or []) if u and str(u).strip()), "")`,
		},
	}

}

func TestLowerBuiltin_byteEquivalent(t *testing.T) {
	for _, c := range byteEquivCases() {
		t.Run(c.name, func(t *testing.T) {
			got, ok := LowerBuiltin(c.builtin, c.args)
			if !ok {
				t.Fatalf("LowerBuiltin(%q, ...) returned false, want true", c.builtin)
			}
			serialized := pyast.SerializeExpr(got)
			if serialized != c.want {
				t.Errorf("LowerBuiltin(%q) serialize = %q, want %q (byte equivalent to shim template)",
					c.builtin, serialized, c.want)
			}
		})
	}
}

func TestIsLowerable(t *testing.T) {
	all := []string{"len", "trim", "join", "to_string", "int", "float", "replace",
		"first_non_empty", "range", "flatten", "div", "repeat",
		"concat", "enumerate", "contains", "split"}
	for _, name := range all {
		if !IsLowerable(name) {
			t.Errorf("IsLowerable(%q) = false, want true", name)
		}
	}
}

func name(s string) pyast.Expr {
	return &pyast.NameExpr{Name: s}
}

// TestAllLoweringsCompile verifies every pyast lowering is valid Python via
// `python3 -c compile()`. This is the py_compile CI gate (P5 §6.4): the
// serializer must never emit syntactically-invalid Python.
func TestAllLoweringsCompile(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	all := byteEquivCases()
	if len(all) == 0 {
		t.Fatal("no lowerings to verify")
	}
	var lines []string
	for _, c := range all {
		got, ok := LowerBuiltin(c.builtin, c.args)
		if !ok {
			t.Fatalf("LowerBuiltin(%q) = false, want true", c.builtin)
		}
		lines = append(lines, pyast.SerializeExpr(got))
	}
	script := strings.Join(lines, "\n")
	cmd := exec.Command("python3", "-c", "import sys; compile(sys.stdin.read(), '<pygen>', 'exec')")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(cmd.Environ(), "PYTHONIOENCODING=utf-8")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python compile failed for %d lowerings:\n%s\nscript:\n%s", len(all), out, script)
	}
}

// TestTemplateMatchesPyast locks the P5.3 parity contract: the string
// template form (Template, consumed by the IR fallback and the dify legacy
// renderer) must stay byte-identical to the pyast form (LowerBuiltin) for
// every builtin. A builtin change must touch both forms in lockstep; this
// test fails otherwise (parity by construction).
func TestTemplateMatchesPyast(t *testing.T) {
	for _, c := range byteEquivCases() {
		t.Run(c.name, func(t *testing.T) {
			strArgs := make([]string, 0, len(c.args))
			for _, a := range c.args {
				strArgs = append(strArgs, pyast.SerializeExpr(a))
			}
			got, ok := Template(c.builtin, strArgs)
			if !ok {
				t.Fatalf("Template(%q, ...) returned false, want true", c.builtin)
			}
			if got != c.want {
				t.Errorf("Template(%q) = %q, want %q (byte equivalent to pyast form)", c.builtin, got, c.want)
			}
		})
	}
}
