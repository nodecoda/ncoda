// five-step-map-key-count —— 从 parser AST 量 ncoda 源里**映射字面量的最大键数**。
//
// 为什么用 AST 不用正则：嵌套字面量 / 模板串 / 数组元素里的 `"k":` 会被文本扫描混入；
// AST 里映射字面量是单个 `*ast.MapLit`，其 `Entries()` 长度即顶层键数，可靠。
//
// 用法：go run ./tools/five-step-map-key-count <file.ncoda> [more...]   （--json 出机器读数）
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/parser"
)

type mv struct {
	max *int
}

func (v *mv) Visit(n ast.Node) ast.Visitor {
	if m, ok := n.(*ast.MapLit); ok {
		if l := len(m.Entries()); l > *v.max {
			*v.max = l
		}
	}
	return v
}

func keysOf(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	prog, errs := parser.ParseSource(string(raw), path)
	if len(errs) > 0 {
		return 0, fmt.Errorf("%d parse errors (first: %v)", len(errs), errs[0])
	}
	max := 0
	ast.Walk(&mv{max: &max}, prog)
	return max, nil
}

func main() {
	jsonOut := flag.Bool("json", false, "")
	flag.Parse()
	type row struct {
		File string `json:"file"`
		Keys int    `json:"keys"`
		Err  string `json:"err,omitempty"`
	}
	var rows []row
	gmax := 0
	for _, p := range flag.Args() {
		k, err := keysOf(p)
		r := row{File: p, Keys: k}
		if err != nil {
			r.Err = err.Error()
		} else if k > gmax {
			gmax = k
		}
		rows = append(rows, r)
		if !*jsonOut {
			status := fmt.Sprintf("keys=%d", k)
			if err != nil {
				status = "ERR " + err.Error()
			}
			fmt.Printf("%s\t%s\n", p, status)
		}
	}
	if *jsonOut {
		out := map[string]any{"files": len(rows), "max_map_keys": gmax, "rows": rows}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("max_map_keys=%d over %d files\n", gmax, len(rows))
}
