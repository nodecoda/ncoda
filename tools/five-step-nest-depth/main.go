// five-step-nest-depth —— 从**parser AST** 量 ncoda 源的控制块最大嵌套深度。
//
// 为什么不用文本缩进：`foreign code … { source `…` }` 的宿主语言是**模板串 token**，
// 缩进/正则扫描会把内嵌 JS/Python 的 `if/for` 当成 ncoda 控制块 ⇒ 深度虚高。
// AST 里宿主语言整体是**一个 TemplateString**，天然不进控制块计数，故可靠。
//
// 用法：go run ./tools/five-step-nest-depth <file.ncoda> [more...]   （--json 出机器读数）
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/parser"
)

type dv struct {
	depth int
	max   *int
}

func blockBearing(n ast.Node) bool {
	switch n.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.WhileStmt, *ast.SwitchStmt,
		*ast.ParallelStmt, *ast.AttemptStmt, *ast.RequestInputStmt,
		*ast.ForExpr, *ast.ParallelForExpr:
		return true
	}
	return false
}

func (v *dv) Visit(n ast.Node) ast.Visitor {
	d := v.depth
	if blockBearing(n) {
		d++
		if d > *v.max {
			*v.max = d
		}
	}
	return &dv{depth: d, max: v.max}
}

func depthOf(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	prog, errs := parser.ParseSource(string(raw), path)
	if len(errs) > 0 {
		return 0, fmt.Errorf("%d parse errors (first: %v)", len(errs), errs[0])
	}
	max := 0
	ast.Walk(&dv{max: &max}, prog)
	return max, nil
}

func main() {
	jsonOut := flag.Bool("json", false, "")
	flag.Parse()
	type row struct {
		File  string `json:"file"`
		Depth int    `json:"depth"`
		Err   string `json:"err,omitempty"`
	}
	var rows []row
	gmax := 0
	for _, p := range flag.Args() {
		d, err := depthOf(p)
		r := row{File: p, Depth: d}
		if err != nil {
			r.Err = err.Error()
		} else if d > gmax {
			gmax = d
		}
		rows = append(rows, r)
		if !*jsonOut {
			status := fmt.Sprintf("depth=%d", d)
			if err != nil {
				status = "ERR: " + err.Error()
			}
			fmt.Printf("%s\t%s\n", status, p)
		}
	}
	if *jsonOut {
		b, _ := json.MarshalIndent(map[string]any{"max": gmax, "files": rows}, "", "  ")
		fmt.Println(string(b))
	}
}
