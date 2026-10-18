package passes

// Boundary tests for the restricted Python source scanner
// (py_source_scan.go), the Task 35 port of code_analysis.py's
// source-validation helpers (_parameter_names / _function_returns /
// _literal_dict_keys, :282-309). Every expectation below was verified
// against the reference implementation (ast.parse + the reference helper
// logic, oracle evidence in the task-35 report); syntax-error messages are
// CPython's verbatim SyntaxError.msg.

import (
	"reflect"
	"testing"
)

func TestScanPythonSourceContractFaces(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantErr   string // scanner-recognized structural error ("" = none)
		mainFound bool
		params    []string
		vararg    bool
		kwarg     bool
		keys      [][]string // per return; nil element = unprovable; nil slice = no returns
	}{
		{
			name:      "clean direct",
			src:       "def main() -> dict:\n    return {\"result\": 1}",
			mainFound: true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "annotation and default",
			src:       "def main(value: int = 3) -> dict:\n    return {\"result\": value}",
			mainFound: true,
			params:    []string{"value"},
			keys:      [][]string{{"result"}},
		},
		{
			name:      "posonly slash and kwonly star",
			src:       "def main(a, /, b: int = 2, *, c: str = \"x\") -> dict:\n    return {\"result\": a}",
			mainFound: true,
			params:    []string{"a", "b", "c"},
			keys:      [][]string{{"result"}},
		},
		{
			name:      "vararg",
			src:       "def main(*args) -> dict:\n    return {\"result\": 1}",
			mainFound: true,
			vararg:    true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "kwarg",
			src:       "def main(**kw) -> dict:\n    return {\"result\": 1}",
			mainFound: true,
			kwarg:     true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "multiline dict",
			src:       "def main() -> dict:\n    return {\n        \"count\": 1,\n        \"normalized\": \"\"\n    }",
			mainFound: true,
			keys:      [][]string{{"count", "normalized"}},
		},
		{
			name:      "comment and string disguise",
			src:       "# def main():\n    return {\"fake\": 1}\ns = \"def main():\"\ndef main() -> dict:\n    return {\"result\": 3}",
			mainFound: true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "triple-quoted disguise",
			src:       "def main() -> dict:\n    s = \"\"\"def fake():\n    return {\"x\": 1}\"\"\"\n    return {\"result\": 1}",
			mainFound: true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "nested def return skipped",
			src:       "def main() -> dict:\n    def helper():\n        return {\"result\": 1}\n    return {\"count\": 0, \"summary\": \"\"}",
			mainFound: true,
			keys:      [][]string{{"count", "summary"}},
		},
		{
			name:      "return inside if collected",
			src:       "def main() -> dict:\n    if True:\n        return {\"result\": 1}\n    return {\"result\": 2}",
			mainFound: true,
			keys:      [][]string{{"result"}, {"result"}},
		},
		{
			name:      "dynamic return unprovable",
			src:       "def main() -> dict:\n    return make_result()",
			mainFound: true,
			keys:      [][]string{nil},
		},
		{
			name:      "empty dict provable",
			src:       "def main() -> dict:\n    return {}",
			mainFound: true,
			keys:      [][]string{{}},
		},
		{
			name:      "pass body no returns",
			src:       "def main(): pass",
			mainFound: true,
			keys:      [][]string{},
		},
		{
			name:      "f-string key unprovable",
			src:       "def main() -> dict:\n    return {f\"x{1}\": 1}",
			mainFound: true,
			keys:      [][]string{nil},
		},
		{
			name:      "bytes key unprovable",
			src:       "def main() -> dict:\n    return {b\"x\": 1}",
			mainFound: true,
			keys:      [][]string{nil},
		},
		{
			name:      "tuple return unprovable",
			src:       "def main() -> dict:\n    return (1, 2)",
			mainFound: true,
			keys:      [][]string{nil},
		},
		{
			name:      "set return unprovable",
			src:       "def main() -> dict:\n    return {\"a\", \"b\"}",
			mainFound: true,
			keys:      [][]string{nil},
		},
		{
			name:      "inline return",
			src:       "def main(): return {\"result\": 1}",
			mainFound: true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "parenthesized dict",
			src:       "def main() -> dict:\n    return ({\"result\": 1})",
			mainFound: true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "adjacent string keys concatenate",
			src:       "def main() -> dict:\n    return {\"a\" \"b\": 1}",
			mainFound: true,
			keys:      [][]string{{"ab"}},
		},
		{
			name:      "semicolon statement after dict",
			src:       "def main() -> dict:\n    return {\"result\": 1}; x = 2",
			mainFound: true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "async def main",
			src:       "async def main() -> dict:\n    return {\"result\": 1}",
			mainFound: true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "module statements before main",
			src:       "import json\ndef main() -> dict:\n    return {\"result\": 1}",
			mainFound: true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "module statements after main body",
			src:       "def main() -> dict:\n    return {\"result\": 1}\nx = 2",
			mainFound: true,
			keys:      [][]string{{"result"}},
		},
		{
			name:      "escaped quote in key",
			src:       "def main() -> dict:\n    return {\"a\\\"b\": 1}",
			mainFound: true,
			keys:      [][]string{{"a\"b"}},
		},
		{
			name:      "no main",
			src:       "x = 1",
			mainFound: false,
		},
		{
			name:      "semicolon return after assignment",
			src:       "def main() -> dict:\n    x = 1; return {\"a\": 1}\n    return {\"b\": 2}",
			mainFound: true,
			keys:      [][]string{{"a"}, {"b"}},
		},
		{
			name:      "double return one line",
			src:       "def main() -> dict:\n    return {\"a\": 1}; return {\"b\": 2}",
			mainFound: true,
			keys:      [][]string{{"a"}, {"b"}},
		},
		{
			name:      "inline suite semicolon return",
			src:       "def main() -> dict:\n    if x: y = 1; return {\"a\": 1}",
			mainFound: true,
			keys:      [][]string{{"a"}},
		},
		{
			name:      "match case return not collected",
			src:       "def main() -> dict:\n    match x:\n        case 1:\n            return {\"a\": 1}\n    return {\"b\": 2}",
			mainFound: true,
			keys:      [][]string{{"b"}},
		},
		{
			name:      "match inline case return not collected",
			src:       "def main() -> dict:\n    match x:\n        case 1: return {\"a\": 1}\n    return {\"b\": 2}",
			mainFound: true,
			keys:      [][]string{{"b"}},
		},
		{
			name:      "nested def tab separator skipped",
			src:       "def main() -> dict:\n    def\thelper():\n        return {\"a\": 1}\n    return {\"b\": 2}",
			mainFound: true,
			keys:      [][]string{{"b"}},
		},
		{
			name:      "match multiline header case return not collected",
			src:       "def main() -> dict:\n    match (\n        x\n    ):\n        case 1:\n            return {\"a\": 1}\n    return {\"b\": 2}",
			mainFound: true,
			keys:      [][]string{{"b"}},
		},
		{
			name:    "slash star needs comma",
			src:     "def main(a, / *args):\n    return 1",
			wantErr: "expected comma between / and *",
		},
		{
			name:    "slash starstar invalid",
			src:     "def main(a, / **kw):\n    return 1",
			wantErr: "invalid syntax",
		},
		{
			name:    "slash must be followed by comma or close",
			src:     "def main(a, / b):\n    return 1",
			wantErr: "invalid syntax",
		},
		{
			name:    "no indented body on line 2",
			src:     "x = 1\ndef main():\ny = 2",
			wantErr: "expected an indented block after function definition on line 2",
		},
		// scanner-recognized structural errors (CPython messages verbatim)
		{
			name:    "unclosed def paren",
			src:     "def main(",
			wantErr: "'(' was never closed",
		},
		{
			name:    "colon directly in param list",
			src:     "def main(:",
			wantErr: "invalid syntax",
		},
		{
			name:    "empty param",
			src:     "def main(,):\n    return 1",
			wantErr: "invalid syntax",
		},
		{
			name:    "non-name param",
			src:     "def main(1):\n    return 1",
			wantErr: "invalid syntax",
		},
		{
			name:    "missing colon",
			src:     "def main() -> dict\n    return 1",
			wantErr: "expected ':'",
		},
		{
			name:    "junk after close paren",
			src:     "def main() int:\n    return 1",
			wantErr: "expected ':'",
		},
		{
			name:    "bare star at end",
			src:     "def main(*) -> dict:\n    return {\"result\": 1}",
			wantErr: "named arguments must follow bare *",
		},
		{
			name:    "no indented body",
			src:     "def main():\nx = 1",
			wantErr: "expected an indented block after function definition on line 1",
		},
		{
			name:    "unclosed dict",
			src:     "def main() -> dict:\n    return {\"result\": 1",
			wantErr: "'{' was never closed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scan := scanPythonSource(tc.src)
			if scan.syntaxError != tc.wantErr {
				t.Fatalf("syntaxError = %q, want %q", scan.syntaxError, tc.wantErr)
			}
			if scan.mainFound != tc.mainFound {
				t.Fatalf("mainFound = %v, want %v", scan.mainFound, tc.mainFound)
			}
			if !reflect.DeepEqual(scan.params, tc.params) {
				t.Fatalf("params = %v, want %v", scan.params, tc.params)
			}
			if scan.vararg != tc.vararg || scan.kwarg != tc.kwarg {
				t.Fatalf("vararg/kwarg = %v/%v, want %v/%v", scan.vararg, scan.kwarg, tc.vararg, tc.kwarg)
			}
			gotKeys := make([][]string, len(scan.returns))
			for i, r := range scan.returns {
				gotKeys[i] = r.keys
			}
			if tc.keys == nil {
				if len(gotKeys) != 0 {
					t.Fatalf("returns = %v, want none", gotKeys)
				}
			} else if !reflect.DeepEqual(gotKeys, tc.keys) {
				t.Fatalf("returns = %v, want %v", gotKeys, tc.keys)
			}
		})
	}
}
