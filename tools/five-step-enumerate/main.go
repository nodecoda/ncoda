// Command five-step-enumerate 枚举 ncoda 语言与 IR 的**闭集**（五步法第 2 步的输入）。
//
// 只读源码结构（stdlib go/parser + go/ast），不执行、不反射、不引入第三方依赖。
// 纪律：闭集必须"由代码结构给出、不由人列举"——所以这里只做机械枚举，
// 任何推断不出来的地方都进 issues（fail loud），不静默跳过。
//
// 输出：JSON 到 stdout。
//
//	go run ./tools/five-step-enumerate --repo . --grammar <spec>/grammar.ebnf
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type nodeType struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
}

type fieldRef struct {
	Struct string `json:"struct"`
	Field  string `json:"field"`
	Type   string `json:"type"`
}

type out struct {
	Grammar          []string    `json:"grammar"`
	GrammarAvailable bool        `json:"grammar_available"`
	GrammarSource    string      `json:"grammar_source"`
	GrammarSHA256    string      `json:"grammar_sha256"`
	AST              [][2]string `json:"ast"` // [family, Type]
	IRKinds          []string    `json:"ir_kinds"`
	IRNodeTypes      []nodeType  `json:"ir_node_types"`
	SourceFields     []fieldRef  `json:"source_fields"`
	Issues           []string    `json:"issues"`
}

func main() {
	repo := flag.String("repo", ".", "仓库根")
	grammarPath := flag.String("grammar", os.Getenv("NCODA_SPEC_GRAMMAR"), "语言标准仓的 grammar.ebnf")
	flag.Parse()

	res := out{}
	var issues []string
	add := func(f string, a ...any) { issues = append(issues, fmt.Sprintf(f, a...)) }

	// ── 闭集 1：语法产生式（语言标准仓，不在隔离范围内）──
	// 不可达不是"问题"而是环境事实：报 grammar_available=false 并交出 sha256 契约，
	// 由门禁决定如何处理（绝不静默缩小定义域）。
	res.GrammarSource = *grammarPath
	if raw, err := os.ReadFile(*grammarPath); err == nil {
		sum := sha256.Sum256(raw)
		res.GrammarSHA256 = hex.EncodeToString(sum[:])
		res.GrammarAvailable = true
		g, gi, err := grammarProductions(*grammarPath)
		if err != nil {
			add("grammar 不可读：%v", err)
		}
		issues = append(issues, gi...)
		res.Grammar = g
	}

	// ── 闭集 2：AST 节点类型（pkg/ast 的标记接口）──
	astFiles, err := parsePkg(filepath.Join(*repo, "pkg", "ast"))
	if err != nil {
		add("pkg/ast 解析失败：%v", err)
	}
	families, members, ai := astFamilies(astFiles)
	issues = append(issues, ai...)
	_ = families
	for _, m := range members {
		res.AST = append(res.AST, [2]string{m[0], m[1]})
	}

	// ── 闭集 3/4：IR 节点 kind + 数据依赖槽 ──
	irFiles, err := parsePkg(filepath.Join(*repo, "pkg", "ir"))
	if err != nil {
		add("pkg/ir 解析失败：%v", err)
	}
	kinds, kindNames, irTypes, ii := irInventory(irFiles)
	issues = append(issues, ii...)
	res.IRKinds = kinds
	res.IRNodeTypes = irTypes
	res.SourceFields = sourceFields(irFiles)

	// ── 自洽检查：kind 常量 ↔ 实现类型（1:1；缺一侧即 fail loud）──
	seen := map[string]int{}
	for _, t := range res.IRNodeTypes {
		seen[t.Kind]++
	}
	for _, k := range res.IRKinds {
		if seen[k] == 0 {
			add("IR kind `%s` 没有任何实现类型（Node 接口）", k)
		}
	}
	for _, t := range res.IRNodeTypes {
		if t.Kind != "" && !kindNames[t.Kind] {
			add("IR 类型 %s 返回未声明的 kind 常量 `%s`", t.Type, t.Kind)
		}
	}
	for k, n := range seen {
		if n > 1 {
			add("IR kind `%s` 有 %d 个实现类型（期望 1）", k, n)
		}
	}

	sort.Strings(res.Grammar)
	sort.Slice(res.AST, func(i, j int) bool {
		if res.AST[i][0] != res.AST[j][0] {
			return res.AST[i][0] < res.AST[j][0]
		}
		return res.AST[i][1] < res.AST[j][1]
	})
	sort.Sort(byKind(res.IRNodeTypes))
	sort.Slice(res.SourceFields, func(i, j int) bool {
		if res.SourceFields[i].Struct != res.SourceFields[j].Struct {
			return res.SourceFields[i].Struct < res.SourceFields[j].Struct
		}
		return res.SourceFields[i].Field < res.SourceFields[j].Field
	})
	sort.Strings(issues)
	res.Issues = issues

	// JSON 形状稳定：nil 一律出空数组（下游不必处理 null）
	if res.Grammar == nil {
		res.Grammar = []string{}
	}
	if res.AST == nil {
		res.AST = [][2]string{}
	}
	if res.IRKinds == nil {
		res.IRKinds = []string{}
	}
	if res.IRNodeTypes == nil {
		res.IRNodeTypes = []nodeType{}
	}
	if res.SourceFields == nil {
		res.SourceFields = []fieldRef{}
	}
	if res.Issues == nil {
		res.Issues = []string{}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(issues) > 0 {
		os.Exit(1) // fail loud：闭集自洽性有问题时不得当作成功
	}
}

type byKind []nodeType

func (b byKind) Len() int           { return len(b) }
func (b byKind) Less(i, j int) bool { return b[i].Kind < b[j].Kind }
func (b byKind) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }

func parsePkg(dir string) ([]*ast.File, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// grammarProductions 抽取 EBNF 左部产生式名（跳过 (* ... *) 注释）。
func grammarProductions(path string) ([]string, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var prods []string
	seen := map[string]bool{}
	inComment := false
	for _, line := range strings.Split(string(raw), "\n") {
		t := line
		if inComment {
			if i := strings.Index(t, "*)"); i >= 0 {
				t, inComment = t[i+2:], false
			} else {
				continue
			}
		}
		for {
			i := strings.Index(t, "(*")
			if i < 0 {
				break
			}
			if j := strings.Index(t[i:], "*)"); j >= 0 {
				t = t[:i] + t[i+j+2:]
			} else {
				t, inComment = t[:i], true
				break
			}
		}
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		eq := strings.Index(t, "=")
		if eq <= 0 {
			continue
		}
		lhs := strings.TrimSpace(t[:eq])
		if !validProduction(lhs) || seen[lhs] {
			continue
		}
		seen[lhs] = true
		prods = append(prods, lhs)
	}
	return prods, nil, nil
}

func validProduction(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// astFamilies 发现 pkg/ast 里的标记接口族与实现类型。
// 判据：接口中含「零参、未导出、无返回值」的方法 = 标记方法（stmtNode/exprNode/...）。
func astFamilies(files []*ast.File) (families []string, members [][2]string, issues []string) {
	markers := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, sp := range gd.Specs {
				ts, ok := sp.(*ast.TypeSpec)
				if !ok {
					continue
				}
				it, ok := ts.Type.(*ast.InterfaceType)
				if !ok || it.Methods == nil {
					continue
				}
				for _, m := range it.Methods.List {
					ft, ok := m.Type.(*ast.FuncType)
					if !ok || len(m.Names) == 0 {
						continue
					}
					name := m.Names[0].Name
					if isMarker(name, ft) {
						markers[name] = true
					}
				}
			}
		}
	}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 {
				continue
			}
			if !markers[fd.Name.Name] {
				continue
			}
			if fd.Type.Params != nil && len(fd.Type.Params.List) > 0 {
				continue
			}
			members = append(members, [2]string{fd.Name.Name, recvTypeName(fd.Recv.List[0].Type)})
		}
	}
	for m := range markers {
		families = append(families, m)
	}
	sort.Strings(families)
	if len(families) == 0 {
		issues = append(issues, "pkg/ast 未发现任何标记接口族（闭集为空 = 判据失效）")
	}
	return families, members, issues
}

func isMarker(name string, ft *ast.FuncType) bool {
	if name == "" || ast.IsExported(name) {
		return false
	}
	if ft.Params != nil && len(ft.Params.List) > 0 {
		return false
	}
	if ft.Results != nil && len(ft.Results.List) > 0 {
		return false
	}
	return true
}

func recvTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	case *ast.IndexListExpr:
		return recvTypeName(t.X)
	}
	return types.ExprString(e)
}

// irInventory 抽取 Kind 常量、实现 Node 的类型及其 kind。
func irInventory(files []*ast.File) (kinds []string, kindNames map[string]bool, nodes []nodeType, issues []string) {
	kindNames = map[string]bool{}
	kindOfConst := map[string]string{} // 常量名 → kind 字符串
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			declaredKind := false
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if vs.Type != nil {
					declaredKind = types.ExprString(vs.Type) == "Kind"
				}
				if !declaredKind {
					continue
				}
				for i, n := range vs.Names {
					kindOfConst[n.Name] = ""
					if i < len(vs.Values) {
						if bl, ok := vs.Values[i].(*ast.BasicLit); ok {
							kindOfConst[n.Name] = strings.Trim(bl.Value, `"`)
						} else {
							issues = append(issues, fmt.Sprintf("Kind 常量 %s 的值不是字面量（无法机械读取）", n.Name))
						}
					} else {
						issues = append(issues, fmt.Sprintf("Kind 常量 %s 没有显式值（无法机械读取）", n.Name))
					}
				}
			}
		}
	}
	for name, v := range kindOfConst {
		if v == "" {
			issues = append(issues, fmt.Sprintf("Kind 常量 %s 值为空", name))
			continue
		}
		kindNames[v] = true
		kinds = append(kinds, v)
	}

	// 实现类型：方法 Kind() Kind
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "Kind" || fd.Recv == nil || len(fd.Recv.List) == 0 {
				continue
			}
			// 只认 `Kind() Kind`（ir.Node 接口）；`Kind() string` 是别的接口（如 build 期 helper）
			if fd.Type.Results == nil || len(fd.Type.Results.List) != 1 ||
				types.ExprString(fd.Type.Results.List[0].Type) != "Kind" {
				continue
			}
			tn := recvTypeName(fd.Recv.List[0].Type)
			kind := ""
			if fd.Body != nil && len(fd.Body.List) == 1 {
				if ret, ok := fd.Body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
					switch e := ret.Results[0].(type) {
					case *ast.Ident:
						kind = kindOfConst[e.Name]
					case *ast.BasicLit:
						kind = strings.Trim(e.Value, `"`)
					case *ast.SelectorExpr:
						kind = kindOfConst[e.Sel.Name]
					}
				}
			}
			if kind == "" {
				issues = append(issues, fmt.Sprintf("IR 类型 %s 的 Kind() 无法机械读取（形态：%s）", tn,
					types.ExprString(fd.Type)))
			}
			nodes = append(nodes, nodeType{Type: tn, Kind: kind})
		}
	}
	sort.Strings(kinds)
	return kinds, kindNames, nodes, issues
}

// sourceFields 抽取 IR 里承载数据依赖的槽位（Source / Ref 家族字段）。
func sourceFields(files []*ast.File) []fieldRef {
	var out []fieldRef
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, sp := range gd.Specs {
				ts, ok := sp.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				for _, fld := range st.Fields.List {
					base := baseTypeName(types.ExprString(fld.Type))
					if base != "Source" && base != "Ref" {
						continue
					}
					for _, n := range fld.Names {
						out = append(out, fieldRef{Struct: ts.Name.Name, Field: n.Name,
							Type: types.ExprString(fld.Type)})
					}
				}
			}
		}
	}
	return out
}

func baseTypeName(t string) string {
	t = strings.TrimPrefix(t, "[]")
	t = strings.TrimPrefix(t, "*")
	t = strings.TrimPrefix(t, "[]")
	t = strings.TrimPrefix(t, "*")
	return t
}
