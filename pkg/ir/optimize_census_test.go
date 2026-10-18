package ir

// optimize_census_test.go — 消费普查的**完整性门禁**（2026-09-13）。
//
// 为什么需要：`collectIRConsumers` 是「谁读了谁」的唯一普查表，死节点/死汇聚
// 清理全都建立在它之上（IR 优化阶段 + `PruneDeadPureNodes`）。它按**节点类型**
// 枚举引用通道，于是「新增一种节点类型但没登记通道」= 一条**静默**的漏计：
// 被它读的生产者会被判「无人消费」而删掉，产物留下悬空 `ref_node`。
// 这不是假设——同一类漏计已经以三种形态各命中一次：
//   ① `http(...)` 的 url/body 模板槽（2026-09-13 修复）
//   ② inline 体内循环的 input/state/output sources（2026-09-13 修复）
//   ③ P1 内置节点 map 参数的值树叶子 `{path, ref_node}`（2026-09-13 修复：
//      read_book / S115_read_book_mihe 的 ARTIFACT_GATE）
//
// 门禁 = 「`ir` 包里每一个实现了 `Node` 的类型，都必须在普查的类型 switch 里
// 有明确归属（引用通道 或 显式声明的无引用通道）」。新增节点类型若漏登记，
// 这个测试直接红——把静默漏计变成编译期的强制决定。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestIRConsumerCensusCoversEveryNodeKind 核对普查 switch 与 Node 实现集合。
func TestIRConsumerCensusCoversEveryNodeKind(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	fset := token.NewFileSet()
	nodeKinds := map[string]string{} // type name → file
	var censusFile *ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if name == "optimize.go" {
			censusFile = f
		}
		// 只认**同时**实现 `Kind() Kind` 与 `NodeID() string` 的类型
		// （`Node` 接口的两个方法）——只认 NodeID 会把 `AttemptStmt` 这类
		// 语句误判成节点。
		methods := map[string]map[string]bool{}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			recv := receiverTypeName(fn.Recv.List[0].Type)
			if recv == "" || !ast.IsExported(recv) {
				continue
			}
			if methods[recv] == nil {
				methods[recv] = map[string]bool{}
			}
			methods[recv][fn.Name.Name] = true
		}
		for recv, ms := range methods {
			if ms["Kind"] && ms["NodeID"] {
				nodeKinds[recv] = name
			}
		}
	}
	// 下限防止门禁「空转」：解析失败（路径/语法变了）必须红，不能静默通过。
	if len(nodeKinds) < 15 {
		t.Fatalf("只找到 %d 个 Node 实现（预期 ≥15）——门禁空转，检查解析路径", len(nodeKinds))
	}

	if censusFile == nil {
		t.Fatal("optimize.go not found")
	}

	handled := map[string]bool{}
	for _, decl := range censusFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "collectIRConsumers" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, e := range cc.List {
				star, ok := e.(*ast.StarExpr)
				if !ok {
					continue
				}
				if id, ok := star.X.(*ast.Ident); ok {
					handled[id.Name] = true
				}
			}
			return true
		})
	}
	if len(handled) == 0 {
		t.Fatal("collectIRConsumers has no type switch — the gate would be vacuous")
	}
	t.Logf("Node 实现 %d 个 / 普查登记 %d 个", len(nodeKinds), len(handled))

	var missing []string
	for typ, file := range nodeKinds {
		if !handled[typ] {
			missing = append(missing, typ+" ("+file+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("消费普查 collectIRConsumers 漏登记 %d 个节点类型：%v\n"+
			"新增节点类型必须在普查里明确归属：有数据引用 → 逐条 addSource/addRef/addTemplate；"+
			"没有 → 显式 case 并在注释里说明（见 \">=case *Start, *HistoryCall\" 的写法）。"+
			"漏登记的后果是静默：被它读的生产者会被当成无消费者删掉。", len(missing), missing)
	}

	var bogus []string
	for typ := range handled {
		if _, ok := nodeKinds[typ]; !ok {
			bogus = append(bogus, typ)
		}
	}
	sort.Strings(bogus)
	if len(bogus) > 0 {
		t.Errorf("消费普查登记了不是 Node 的类型：%v", bogus)
	}
}

// receiverTypeName 取方法接收者里的类型名（值接收者或指针接收者）。
func receiverTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return receiverTypeName(e.X)
	}
	return ""
}

// ── 字段级门禁（2026-09-13 加固，B1 教训）──────────────────────────────
//
// 上面的类型级门禁只回答「这个节点类型有没有被提起」，回答不了「它的
// **每一个引用字段**有没有被读」。B1 就是字段级的洞：`*Switch` 在普查里
// 有 case（登记了 SystemPrompt 模板槽），但它的 `Query Source` 字段没读 ⇒
// `switch (query)` 的 query 被判无消费者 ⇒ 归并节点被删、产物悬空
// （6 个最小源码 SUCCEEDED → ARTIFACT_GATE）。类型级门禁对此**全绿**。
//
// 字段级门禁：Node 类型上每一个「可能携带数据引用」的字段
// （Source / *Source / []Source / Ref / *Ref / []Ref，以及模板槽字符串
// Template/URL/Body/SystemPrompt）都必须在普查里有 v.<Field> 的读取，或在
// censusFieldExemptions 里显式豁免并给出理由。豁免表必须「用得上」——
// 出现了却没对应的字段 = 过期豁免，同样红（防止门禁被豁免表蚕食）。

type nodeFieldInfo struct {
	Type     string
	Field    string
	TypeText string
	File     string
}

// censusRefShaped 判据：字段类型是否是数据引用载体。
func censusRefShaped(text string) bool {
	switch text {
	case "Source", "*Source", "[]Source", "Ref", "*Ref", "[]Ref":
		return true
	}
	return false
}

// censusTemplateSlot 判据：字符串字段是否是「原样进产物的模板槽」。
// 模板槽里的 {{block_output_<id>.<port>}} 是渲染期才解析的数据引用，
// 漏登记同样删活值（http url/body 第一手）。
func censusTemplateSlot(name string) bool {
	switch name {
	case "Template", "URL", "Body", "SystemPrompt":
		return true
	}
	return false
}

// censusFieldExemptions: "Type.Field" → 理由。只在**确实不是引用通道**时
// 才允许登记（例：字段由别处的语句遍历统计）。
var censusFieldExemptions = map[string]string{
	// 两种编码互斥：`Refs` 是 legacy 单组（Group1）编码，`Groups` 是多端口
	// phi 编码；builder 从不同时填充（见 build_stmt/build_condition/
	// build_return 的 Aggregate 构造点，`Groups` 非空时 `Refs` 恒空），
	// 渲染器与 `AllRefs()` 也一致地「Groups 非空 ⇒ 只读 Groups」。
	// 所以 `AllRefs()` 对本字段是**无损**读取，不是漏读。
	"Aggregate.Refs": "AllRefs() 覆盖（Groups/Refs 两种编码互斥，Groups 非空时只读 Groups）",
}

// TestIRConsumerCensusCoversEveryRefField 字段级普查门禁。
func TestIRConsumerCensusCoversEveryRefField(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	fset := token.NewFileSet()
	nodeKinds := map[string]string{}
	var files []*ast.File
	var censusFile *ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
		if name == "optimize.go" {
			censusFile = f
		}
		methods := map[string]map[string]bool{}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			recv := receiverTypeName(fn.Recv.List[0].Type)
			if recv == "" || !ast.IsExported(recv) {
				continue
			}
			if methods[recv] == nil {
				methods[recv] = map[string]bool{}
			}
			methods[recv][fn.Name.Name] = true
		}
		for recv, ms := range methods {
			if ms["Kind"] && ms["NodeID"] {
				nodeKinds[recv] = name
			}
		}
	}
	if censusFile == nil {
		t.Fatal("optimize.go not found")
	}

	// 收集 Node 类型上「可能携带数据引用」的字段。
	var fields []nodeFieldInfo
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name == nil || !ast.IsExported(ts.Name.Name) {
					continue
				}
				if _, isNode := nodeKinds[ts.Name.Name]; !isNode {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				for _, fl := range st.Fields.List {
					typeText := exprText(fl.Type)
					for _, nm := range fl.Names {
						if censusRefShaped(typeText) || (typeText == "string" && censusTemplateSlot(nm.Name)) {
							fields = append(fields, nodeFieldInfo{
								Type: ts.Name.Name, Field: nm.Name,
								TypeText: typeText, File: f.Name.Name,
							})
						}
					}
				}
			}
		}
	}
	if len(fields) < 10 {
		t.Fatalf("只找到 %d 个引用字段（预期 ≥10）——门禁空转，检查 Node 类型识别", len(fields))
	}

	// 普查里每个 case 分支读了哪些字段（v.<Field>）。
	read := map[string]map[string]bool{} // type → field set
	for _, decl := range censusFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "collectIRConsumers" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSwitchStmt)
			if !ok {
				return true
			}
			ident, ok := ts.Assign.(*ast.AssignStmt)
			if !ok || len(ident.Lhs) != 1 || len(ident.Rhs) != 1 {
				return true
			}
			lhs, ok := ident.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			v := lhs.Name
			for _, c := range ts.Body.List {
				cc, ok := c.(*ast.CaseClause)
				if !ok {
					continue
				}
				var names []string
				for _, e := range cc.List {
					if id, ok := starIdent(e); ok {
						names = append(names, id)
					}
				}
				if len(names) == 0 {
					continue
				}
				used := map[string]bool{}
				for _, stmt := range cc.Body {
					ast.Inspect(stmt, func(n ast.Node) bool {
						sel, ok := n.(*ast.SelectorExpr)
						if !ok {
							return true
						}
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == v {
							used[sel.Sel.Name] = true
						}
						return true
					})
				}
				for _, nm := range names {
					if read[nm] == nil {
						read[nm] = map[string]bool{}
					}
					for f := range used {
						read[nm][f] = true
					}
				}
			}
			return true
		})
	}

	usedExemption := map[string]bool{}
	var missing []string
	for _, fi := range fields {
		key := fi.Type + "." + fi.Field
		if read[fi.Type][fi.Field] {
			continue
		}
		if reason, ok := censusFieldExemptions[key]; ok && reason != "" {
			usedExemption[key] = true
			continue
		}
		missing = append(missing, key+" ("+fi.TypeText+", "+fi.File+")")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("消费普查漏读 %d 个引用字段：%v\n"+
			"每个 Source/Ref 字段与模板槽字符串都是潜在数据依赖：在 collectIRConsumers 的"+
			"对应 case 里读取它（addSource/addSources/addRef/addRefs/addTemplate），"+
			"或在本文件的 censusFieldExemptions 里给出「它不是引用通道 / 别处统计」的理由。",
			len(missing), missing)
	}
	var stale []string
	for key, reason := range censusFieldExemptions {
		if reason == "" || !usedExemption[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("censusFieldExemptions 里有 %d 条过期豁免（字段已不缺失或已被读取）：%v", len(stale), stale)
	}
}

// starIdent 取 `*T` 里的 T（同时接受 `T` 本身，兼容未来非指针 case）。
func starIdent(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.StarExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name, true
		}
	case *ast.Ident:
		return x.Name, true
	}
	return "", false
}

// exprText 渲染类型表达式的源码文本（用于比对字段类型）。
func exprText(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return "*" + exprText(x.X)
	case *ast.ArrayType:
		return "[]" + exprText(x.Elt)
	}
	return ""
}
