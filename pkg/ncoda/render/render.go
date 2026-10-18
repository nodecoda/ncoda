package render

// render_ncoda.go —— AST4 → ncoda 源码序列化（Phase 5 step 3）。
// AST4 保持语言无关；ncoda 差异（头 @language/@mode、function main 签名、
// let 声明、for (x in y) 语法、字符串引用、类型名映射）全部在序列化层补齐。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/migrate"
	"unicode"

	"github.com/nodecoda/ncoda/pkg/ncoda/astnodes"
	"github.com/nodecoda/ncoda/pkg/ncoda/astwalk"
	"github.com/nodecoda/ncoda/pkg/ncoda/typesystem"
	"github.com/nodecoda/ncoda/pkg/token"
)

// NcodaParam main 函数参数。
type NcodaParam struct {
	Name     string
	Type     string
	Required bool
	// TypeInfo = 该参数的平台类型（start 节点的输出口类型）。渲染**默认值**需要它：
	// file 族无字面量（file-family-canonical-form §①），值必须写成标注
	// `file(<string>)`，而「这口是不是 file 族」只能由类型本身判定
	// （见 renderEntryDefaultValue）。typNames 覆盖只服务 object → 命名 record。
	TypeInfo *typesystem.TypeInfo
}

// EntryParams 从 Entry 节点输出类型推导 main 参数（start 节点的 data.outputs
// 即工作流输入参数，buildEntryNode 将其映射为 Entry 的 OutputTypes）。
//
// typNames = 调用方（Generator）**在 Generate 之前**用同一把 registrar 解析出的
// 类型文本（键 = 参数名）。为什么不由本函数自己解析：object 型参数经 registrar 会
// 登记命名 record 类型，而该声明必须随 typeDecls 前置 —— 渲染时现算就错过了前置
// 时机（CB-5 的类型半）。缺项 ⇒ 回退 `ncodaTypeName`（无 registrar 的历史口径）。
func EntryParams(entry astnodes.NodeDecl, typNames map[string]string) []NcodaParam {
	outs := entry.GetOutputTypes()
	names := make([]string, 0, len(outs))
	for name := range outs {
		names = append(names, name)
	}
	sort.Strings(names)
	params := make([]NcodaParam, 0, len(names))
	for _, name := range names {
		ti := outs[name]
		p := NcodaParam{Name: name, Type: "string", Required: true, TypeInfo: ti}
		if ti != nil {
			p.Type = ncodaTypeName(ti)
			p.Required = ti.Required
		}
		if t, ok := typNames[name]; ok && t != "" {
			p.Type = t
		}
		// 关键字参数名不做 name_ 后缀（type_/file_ = <关键字>_ 兜底，2026-09-09
		// 已判删除）：保留物理名，main 签名渲染经 escapeFieldName 写 $file，
		// body 引用（IdentExpr 渲染同样转义）→ compile 词法剥 $ 还原物理名，
		// A0 键 file == B1 键 file（NAME-CONVERSION-SPEC 物理名不变量）。
		params = append(params, p)
	}
	return params
}

// renderEntryDefaultValue 按**声明类型**渲染入口参数的默认值。
//
// 与 body 值树同一渲染器，**只差 file 族这一处**：file 是类型类别、**无字面量**
// （file-family-canonical-form §①），file 值的规范形是标注 cast `file(<string>)`
// （string→file，编译期零开销；语料既有 298 处 `file(""`）。平台把 file 口的
// default_value 写成 URL 字符串，直接按字面量渲染 ⇒ `file<audio> bgm = "https://…"`
// ⇒ 正编译 `TYPE_MISMATCH … expected Type(file), got Type(string)`（`IsAssignable`
// 只放宽 `file → string`，反向不成立）⇒ **自家反编译产物自家编译不了**
// （一手 2026-10-01：全语料 9 源 COMPILE_FAIL@r1，棘轮 3 → 12）。
//
// 数组同理逐元素（`array<file<image>>` 的默认值是 URL 列表）。其余类型原样走
// 字面量渲染器（string / int / float / bool / 数组 / 映射）。
func renderEntryDefaultValue(v any, ti *typesystem.TypeInfo) string {
	if ti != nil {
		switch ti.Type {
		case typesystem.DataTypeFile:
			if s, ok := v.(string); ok {
				return "file(" + RenderNCodaExpr(&LiteralExpr{Value: s}) + ")"
			}
		case typesystem.DataTypeArray:
			if elem := ti.ElemTypeInfo; elem != nil && elem.Type == typesystem.DataTypeFile {
				if elems, ok := v.([]any); ok {
					parts := make([]string, 0, len(elems))
					for _, e := range elems {
						parts = append(parts, renderEntryDefaultValue(e, elem))
					}
					return "[" + strings.Join(parts, ", ") + "]"
				}
			}
		}
	}
	return RenderNCodaExpr(&LiteralExpr{Value: v})
}

// objectTypeRegistrar 把 object schema 登记为命名 record 类型（返回类型名）。
// 返回 ok=false 表示不可登记（无 schema / 字段名非法），调用方回退 "any"。
type objectTypeRegistrar func(ti *typesystem.TypeInfo) (name string, ok bool)

// ncodaTypeName typesystem.DataType → ncoda 类型名（integer→int、number→float、
// boolean→bool、time→string、object→any；list→array[T] 沿 ElemTypeInfo 递归）。
// reg 非 nil 时，带字段 schema 的 object 登记为命名 record 类型（roundtrip
// 类型保真：LLM 结构化数组元素、foreign code 输出契约），否则回退 "any"。
func ncodaTypeName(ti *typesystem.TypeInfo) string {
	return ncodaTypeNameWith(ti, nil)
}

func ncodaTypeNameWith(ti *typesystem.TypeInfo, reg objectTypeRegistrar) string {
	if ti == nil {
		return "any"
	}
	var base string
	switch ti.Type {
	case typesystem.DataTypeString, typesystem.DataTypeTime:
		base = "string"
	case typesystem.DataTypeInteger:
		base = "int"
	case typesystem.DataTypeNumber:
		base = "float"
	case typesystem.DataTypeBoolean:
		base = "bool"
	case typesystem.DataTypeFile:
		// 族 = 端口 / 参数面的事实 ⇒ 声明位必须带上（file_family_text.go）。
		base = fileTypeText(ti)
	case typesystem.DataTypeArray:
		elem := "any"
		if ti.ElemTypeInfo != nil {
			elem = ncodaTypeNameWith(ti.ElemTypeInfo, reg)
		}
		base = "array<" + elem + ">"
	case typesystem.DataTypeObject:
		if reg != nil {
			if name, ok := reg(ti); ok {
				base = name
			} else if len(ti.Properties) == 0 {
				base = "object"
			}
		}
		if base == "" {
			base = "any"
		}
	default:
		base = "any"
	}
	// TypeInfo.Required is the platform's field-level presence contract.
	// Callers that have not passed through Coze artifact normalization may
	// still use this helper for explicit nullable semantic types.
	if ti.RequiredExplicit && !ti.Required {
		return base + "?"
	}
	return base
}

// escapeFieldName 把外部契约字段名转义为 ncoda 标识符文本（NCODA-DOLLAR-
// ESCAPE-SPEC）：ncoda 关键字字段名（type/output）写作 '$'+关键字（词法剥
// '$' 后还原为原始标识符，正编译零改动，roundtrip 天然闭合）。非关键字原样
// 返回（$ 前缀只服务关键字）。返回的文本可直接写入 record 字段声明/成员
// 访问；内部 IR/ref/类型名一律使用原始契约名（不转义）。
// EscapeIdent 导出关键字 $ 转义（供反编译器转换层在生成参数名/声明名时
// 使用;NCODA-DOLLAR-ESCAPE-SPEC:type → $type）。
func EscapeIdent(name string) string { return escapeFieldName(name) }

func escapeFieldName(name string) string {
	if token.IsKeyword(name) {
		return "$" + name
	}
	return name
}

// isValidTypeFieldName 字段名可作为 record 字段标识符（首字符字母/下划线/$
// （NCODA-DOLLAR-ESCAPE-SPEC 转义形态，如 $type）、其余字母/数字/下划线，
// 且非关键字——转义形态由 escapeFieldName 生成，此处接受 '$' 起始）。
func isValidTypeFieldName(name string) bool {
	if name == "" || token.IsKeyword(name) {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !(r == '_' || r == '$' || unicode.IsLetter(r)) {
				return false
			}
		} else if !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

// RenderNCodaProgram 渲染完整 ncoda 程序（头 + function main + 语句体）。
func RenderNCodaProgram(stmts []Stmt, entry astnodes.NodeDecl) string {
	return RenderNCodaProgramWithEntryTypes(stmts, entry, nil, "")
}

// RenderNCodaProgramWithEntryTypes 同上，但由调用方给出**主签名的类型文本**：
//
//	· entryTypes    —— 入口参数类型（键 = 参数名；见 EntryParams 的说明）；
//	· returnTypeText —— 返回类型（空串 = 不发射 `-> T`；见 Generator.deriveReturnTypeText）。
//
// 生产入口用这个，测试夹具用两参形式。
func RenderNCodaProgramWithEntryTypes(stmts []Stmt, entry astnodes.NodeDecl, entryTypes map[string]string, returnTypeText string) string {
	var b strings.Builder
	b.WriteString("@language nodecoda/1\n")
	b.WriteString("@mode workflow\n")
	// import 提升到函数声明之前（递归收集所有分支，去重保序）。
	//
	// 子块递归走**唯一实现** `stmtSubtreeBlocks`（容器块 ∪ 值位附体块，同一个
	// 闭集），不再手写 case 枚举 —— 手写版曾缺 `While` / `AttemptStmt`（以及
	// 值位上的 `ForExpr`）：那里的 import 会被整体丢掉，正是 X9/read_book 那一族
	// （import 丢失 ⇒ 下一轮 UNDEFINED_VAR）的形态。判据 =
	// stmt_walker_census_test.go（G2：本函数不再自带 switch ⇒ 无重复遍历面）。
	seen := map[string]bool{}
	var collectImports func([]Stmt)
	collectImports = func(items []Stmt) {
		for _, st := range items {
			if im, ok := st.(*ImportStmt); ok {
				if !seen[im.Path] {
					seen[im.Path] = true
					b.WriteString("import \"" + im.Path + "\";\n")
				}
				continue
			}
			for _, blk := range astwalk.StmtSubtreeBlocks(st) {
				if blk != nil {
					collectImports(blk.Stmts)
				}
			}
		}
	}
	collectImports(stmts)
	if len(seen) > 0 {
		b.WriteString("\n")
	}
	// 命名 record 类型声明提升到 function main 之前（保持 imports 之后）。
	for _, s := range stmts {
		if td, ok := s.(*TypeDecl); ok {
			b.WriteString(RenderTypeDecl(td))
			b.WriteString("\n")
		}
	}
	// 命名枚举声明提升（ask ActionType / classify EnumT），type 之后、
	// subflow 之前（与 type 同级的文件头声明段）。
	for _, s := range stmts {
		if ed, ok := s.(*EnumDecl); ok {
			b.WriteString(RenderEnumDecl(ed))
			b.WriteString("\n")
		}
	}
	// subflow 声明提升（SUBWORKFLOW-DECLARATION）：身份 + 输入/输出契约，
	// 与 type 平级放在文件头声明段（type 之后、function main 之前）。
	for _, s := range stmts {
		if sd, ok := s.(*SubflowDecl); ok {
			b.WriteString(RenderSubflowDecl(sd))
			b.WriteString("\n")
		}
	}
	b.WriteString("function main(")
	params := EntryParams(entry, entryTypes)
	// 入口参数默认值（L28）：`start.node_outputs.<口>.default_value` 经
	// buildEntryNode → EntryNode.DefaultValues 到达此处；ncoda 的载体 = 参数默认值
	// 语法 `<type> <name> = <lit>`（parseParam）。漏写 = 平台默认值静默消失
	// （一手 2026-10-01：`default_value: 藏红花` ⇒ `function main(string yaoming)`，rc=0）。
	// 值已由 ConvertInputs 按声明类型规范化，按字面量渲染即可（与 body 值树同一渲染器）。
	var entryDefaults map[string]any
	if e, ok := entry.(*astnodes.EntryNode); ok {
		entryDefaults = e.DefaultValues
	}
	for i, p := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Type + " " + escapeFieldName(p.Name))
		if v, ok := entryDefaults[p.Name]; ok && v != nil {
			b.WriteString(" = " + renderEntryDefaultValue(v, p.TypeInfo))
		}
	}
	b.WriteString(")")
	// 返回类型：**能取到就写、取不到整段不发射**（绝不虚构）—— 与端点名额
	// `input.type` 同一条第二半（CB-10）。取到而丢掉 = 作者声明的出口契约静默消失
	// （CB-6），且下一轮的类型检查随之关闭（`type_pass` 无声明走「首个 return 锚定
	// 隐式类型」通路）。
	if returnTypeText != "" {
		b.WriteString(" -> " + returnTypeText)
	}
	b.WriteString(" {\n")
	for _, s := range stmts {
		if _, ok := s.(*TypeDecl); ok {
			continue // 已提升到 function main 之前
		}
		if _, ok := s.(*EnumDecl); ok {
			continue // 已提升到 function main 之前
		}
		if _, ok := s.(*SubflowDecl); ok {
			continue // 已提升到 function main 之前
		}
		b.WriteString(RenderNCodaStmt(s, 1))
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// renderTemplateClause 渲染**模板串**本身（不含宿主语句的 `return` / `output` 前缀、
// 不含结尾 `;`）—— 一句话：`template "<逐字文本>" [using { name: <值路径>, … }]`。
//
// **一处定义、三处复用**：`return template …;`（答复端点）· `output(template …);`
// （中间消息）· 值位的 `template …`（llm message content）。三者只是宿主不同，
// 载体逐字相同（正本 = docs/five-step/spec/TEMPLATE-STRING-CANONICAL-FORM.md §4）。
//
// 文本**逐字**（零解析）：只做 ncoda 字符串字面量的必要转义，不改动任何其它字节；
// `using` 只在有名额时发射；`template` 标记恒发射（判别标记，见 W2 §1.2）。
func renderTemplateClause(text string, bindings []TemplateSlot) string {
	var b strings.Builder
	b.WriteString("template ")
	b.WriteString(quoteNCodaString(text))
	if len(bindings) > 0 {
		b.WriteString(" using {")
		for i, slot := range bindings {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(" ")
			b.WriteString(escapeFieldName(slot.Name))
			b.WriteString(": ")
			b.WriteString(RenderNCodaExpr(slot.Value))
		}
		b.WriteString(" }")
	}
	return b.String()
}

// quoteNCodaString 把任意 Go 字符串渲染成 ncoda 双引号字符串字面量。转义集合
// **只含** lexer 能解回的序列（\\n \\r \\t \\\\ \\"）；其余字符（含非 ASCII、
// 引号外的空白）原样保留——`\uXXXX` 之类 lexer 会报 Unknown escape sequence。
func quoteNCodaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// RenderTypeDecl 渲染命名 record 类型声明（type X = { <type> <field>; ... };）。
func RenderTypeDecl(td *TypeDecl) string {
	var b strings.Builder
	b.WriteString("type " + td.Name + " = {")
	if len(td.Fields) == 0 {
		b.WriteString(" }")
		return b.String()
	}
	b.WriteString("\n")
	for i, f := range td.Fields {
		b.WriteString("  " + f.Type + " " + escapeFieldName(f.Name) + ";")
		if i < len(td.Fields)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n};")
	return b.String()
}

// RenderSubflowDecl 渲染 subflow 声明（SUBWORKFLOW-DECLARATION）：
//
//	subflow <name> = ("<workflowId>", "<version>") {
//	    input:  { <type> <field>; ... },
//	    output: { <type> <field>; ... }
//	};
func RenderSubflowDecl(sd *SubflowDecl) string {
	var b strings.Builder
	b.WriteString("subflow " + sd.Name + " = (\"" + sd.WorkflowID + "\", \"" + sd.WorkflowVersion + "\") {\n")
	b.WriteString("  input: {")
	if len(sd.InputFields) > 0 {
		b.WriteString(" ")
		for i, f := range sd.InputFields {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(f.Type + " " + escapeFieldName(f.Name) + ";")
		}
		b.WriteString(" ")
	}
	b.WriteString("},\n")
	b.WriteString("  output: {")
	if len(sd.OutputFields) > 0 {
		b.WriteString(" ")
		for i, f := range sd.OutputFields {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(f.Type + " " + escapeFieldName(f.Name) + ";")
		}
		b.WriteString(" ")
	}
	b.WriteString("}\n")
	b.WriteString("};")
	return b.String()
}

// RenderEnumDecl 渲染命名枚举类型声明（enum Name { m1, m2 };）。
func RenderEnumDecl(ed *EnumDecl) string {
	if ed == nil {
		return ""
	}
	members := make([]string, 0, len(ed.Members))
	for _, m := range ed.Members {
		members = append(members, escapeFieldName(m))
	}
	return fmt.Sprintf("enum %s { %s };", ed.Name, strings.Join(members, ", "))
}

// RenderNCodaStmt 渲染单条语句。
// streamingSuffix 渲染投递开关的**唯一拼写**（尾置上下文词，用户 2026-09-27
// 裁决 甲）：true ⇒ ` streaming`，false ⇒ 空串（= 平台默认值，缺省即默认，
// 不写出来）。两个位点（中间消息 / 答复端点）共用，一处定义。
func streamingSuffix(on bool) string {
	if on {
		return " streaming"
	}
	return ""
}

func RenderNCodaStmt(s Stmt, indent int) string {
	pad := strings.Repeat("    ", indent)
	switch v := s.(type) {
	case *Block:
		return ""
	case *VarDecl:
		// An empty list has no element type of its own.  Most initialized
		// declarations deliberately omit the recovered canvas type in the
		// surface syntax (`let v = call(...)`), but that omission is not safe
		// for `[]`: the compiler cannot infer an element type and reports
		// EMPTY_AGGREGATE_TYPE on the next compile.  Keep the recovered type
		// only at this boundary, where it is semantically required, so a
		// reversed compiler-owned shim such as `array<string> v = []` remains
		// compilable without changing the rendering of ordinary declarations.
		if v.Type != "" && v.SharedStorage && v.Init != nil {
			// 共享载体（wire 扇出 ≥2）的声明保留类型：`let` 是**值绑定**，
			// 纯常量在再编译时被折叠进消费者、节点消失 ⇒ 产物漂移
			// （C §6.1 A9 / J §8 U11）。与下面的空列表同性质：类型只在这个
			// 语义必需的边界保留。
			return fmt.Sprintf("%s%s %s = %s;", pad, v.Type, v.Name, RenderNCodaExpr(v.Init))
		}
		if v.Type != "" && isEmptyListExpr(v.Init) {
			return fmt.Sprintf("%s%s %s = %s;", pad, v.Type, v.Name, RenderNCodaExpr(v.Init))
		}
		if v.Init == nil {
			// 分支变量作用域提升的裸声明（var v;）/ 带类型聚合变量
			// （string agg_..._<group>;，ncoda typed var decl 类型前置，
			// 分支聚合消解 X212 模式）。
			if v.Type != "" {
				return fmt.Sprintf("%s%s %s;", pad, v.Type, v.Name)
			}
			return fmt.Sprintf("%svar %s;", pad, v.Name)
		}
		origin := ""
		if v.MigrationOrigin == string(migrate.OriginBatchInnerLLM) {
			origin = migrate.BatchInnerSourceMarker + " "
		}
		if v.Mutable || FlattenValueLoop(v.Init) {
			// FlattenValueLoop：展平值循环的语义**只在 `var` 拼写里**（编译端
			// `flattenOutput = s.IsMutable()`）⇒ 还原垫片后必须用 `var` 回写，
			// 否则重编译不再生展平（语义静默丢失）。
			return fmt.Sprintf("%svar %s = %s%s;", pad, v.Name, origin, RenderNCodaExpr(v.Init))
		}
		return fmt.Sprintf("%slet %s = %s%s;", pad, v.Name, origin, RenderNCodaExpr(v.Init))
	case *AssignStmt:
		op := "="
		if v.Op != "" {
			op = v.Op
		}
		return fmt.Sprintf("%s%s %s %s;", pad, escapeFieldName(v.Target), op, RenderNCodaExpr(v.Value))
	case *ExprStmt:
		return pad + RenderNCodaExpr(v.Expr) + ";"
	case *If:
		return renderIfStmt(v, indent, "if")
	case *For:
		return pad + renderForExprFlags(v.Var, v.Iterable, v.Body, indent, v.Parallel, v.Concurrency, v.OnError, v.LoopVars(), v.LoopSources())
	case *While:
		var wb strings.Builder
		wb.WriteString(fmt.Sprintf("%swhile (%s) limit %d {\n", pad, RenderNCodaExpr(v.Cond), v.Limit))
		for _, inner := range v.Body.Stmts {
			wb.WriteString(RenderNCodaStmt(inner, indent+1) + "\n")
		}
		wb.WriteString(pad + "}")
		return wb.String()
	case *ParallelStmtStmt:
		var pb strings.Builder
		pb.WriteString(pad + "parallel {\n")
		for _, blk := range v.Blocks {
			// 每条并行链必须是独立块（parseParallelStmt 逐分支 parseBlock）。
			pb.WriteString(strings.Repeat("    ", indent+1) + "{\n")
			for _, inner := range blk.Stmts {
				pb.WriteString(RenderNCodaStmt(inner, indent+2) + "\n")
			}
			pb.WriteString(strings.Repeat("    ", indent+1) + "}\n")
		}
		pb.WriteString(pad + "}")
		return pb.String()
	case *Switch:
		var ib strings.Builder
		head := "switch (" + RenderNCodaExpr(v.Query)
		if v.Config != nil && len(v.Config.Keys) > 0 {
			head += ", " + RenderNCodaExpr(v.Config)
		}
		head += ")"
		if v.Result != "" {
			head += " -> " + escapeFieldName(v.Result)
		}
		ib.WriteString(pad + head + " {\n")
		for _, br := range v.Branches {
			if br.Dangling {
				// **真悬挂**（端口无边）：分支声明了却没有任何 branch 边 →
				// 显式 FAILED 标记（工作契约），case 不生成。
				//
				// 措辞按 2026-09-13 裁定收紧：判据是「**端口悬空**」——有连线
				// 的空臂是合法语法（`{ }` / 直通），不走这条分支（P25）。
				ib.WriteString(strings.Repeat("    ", indent+1) +
					"// FAILED(SWITCH-DANGLING): 分支 \"" + br.Label + "\" 无对应 branch 边（端口悬空 = 病态，拒绝）\n")
				continue
			}
			ib.WriteString(strings.Repeat("    ", indent+1) + "\"" + br.Label + "\": {\n")
			for _, inner := range br.Body.Stmts {
				ib.WriteString(RenderNCodaStmt(inner, indent+2) + "\n")
			}
			ib.WriteString(strings.Repeat("    ", indent+1) + "}\n")
		}
		if v.HasDefault {
			ib.WriteString(strings.Repeat("    ", indent+1) + "default: {\n")
			if v.DefaultBody != nil {
				for _, inner := range v.DefaultBody.Stmts {
					ib.WriteString(RenderNCodaStmt(inner, indent+2) + "\n")
				}
			}
			ib.WriteString(strings.Repeat("    ", indent+1) + "}\n")
		}
		ib.WriteString(pad + "}")
		return ib.String()

	case *AttemptStmt:
		return renderAttemptStmt(v, pad, indent)
	case *RequestInputStmt:
		return renderRequestInputStmt(v, pad, indent)
	case *OutputStmt:
		// 归一到 `output(<expr>) [streaming];`（2026-09-25 用户拍板；开关 =
		// 2026-09-27 甲）：`template "…" using {…}` 是**表达式**
		// （`RenderNCodaExpr` 的值位分支已认识它）⇒ 中间消息**没有**第二种语句
		// 拼写。同一条规则也覆盖 `return`：那个 case 本来就没有特判。
		return pad + "output(" + RenderNCodaExpr(v.Value) + ")" + streamingSuffix(v.Streaming) + ";"
	case *Return:
		if v.Value == nil {
			// void return（主流程分支直达 End，S188 intent default→End）：
			// `return;` 表达流程终止。
			return pad + "return;"
		}
		return pad + "return " + RenderNCodaExpr(v.Value) + streamingSuffix(v.Streaming) + ";"
	case *Break:
		return pad + "break;"
	case *Continue:
		return pad + "continue;"
	case *Yield:
		if v.Port != "" {
			return pad + "yield " + v.Port + " = " + RenderNCodaExpr(v.Value) + ";"
		}
		return pad + "yield " + RenderNCodaExpr(v.Value) + ";"
	case *ImportStmt:
		// 已由 RenderNCodaProgram 提升到 @mode 之后，函数体内跳过。
		return ""
	case *Comment:
		return pad + "// " + v.Text
	default:
		return pad + fmt.Sprintf("/* <stmt %T> */", s)
	}
}

// isEmptyListExpr reports the AST empty-list literal, not [] text embedded in
// foreign-code source strings.  The distinction matters because foreign code
// is an opaque user contract and must not be rewritten by the ncoda renderer.
func isEmptyListExpr(e Expr) bool {
	l, ok := e.(*ListExpr)
	return ok && len(l.Items) == 0
}

// renderForExpr 渲染 for 循环（语句 For 与表达式 ForExpr 共用）。
func renderForExpr(varName string, iterable Expr, body *Block, indent int) string {
	return renderForExprFlags(varName, iterable, body, indent, false, 0, "", nil, nil)
}

// renderForExprFlags 渲染 for/parallel for 循环。Parallel=true 时输出
// `parallel for (x in items[, concurrency: N][, on_error: mode])`（coze Batch）——
// **配置子句按规范形写：等于缺省 ⇒ 不写**（见 renderParallelForConfig）。
// zip 多源（vars/sources len>1）：`for (a, b in zip(x, y))`（变量名即端口名）。
// renderIfStmt renders an If, and canonicalizes a nested If that is the sole
// statement of its Else block into else-if syntax. This is surface syntax
// only: recognition keeps ordered exclusive branches as recursive If nodes.
func renderIfStmt(v *If, indent int, prefix string) string {
	pad := strings.Repeat("    ", indent)
	lead := pad
	if prefix != "if" {
		lead = ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s%s (%s) {\n", lead, prefix, RenderNCodaExpr(v.Cond)))
	for _, inner := range v.Then.Stmts {
		b.WriteString(RenderNCodaStmt(inner, indent+1) + "\n")
	}
	if v.Else != nil && len(v.Else.Stmts) > 0 {
		if len(v.Else.Stmts) == 1 {
			if nested, ok := v.Else.Stmts[0].(*If); ok {
				return b.String() + pad + "} " + renderIfStmt(nested, indent, "else if")
			}
		}
		b.WriteString(pad + "} else {\n")
		for _, inner := range v.Else.Stmts {
			b.WriteString(RenderNCodaStmt(inner, indent+1) + "\n")
		}
	}
	b.WriteString(pad + "}")
	return b.String()
}

func renderForExprFlags(varName string, iterable Expr, body *Block, indent int, parallel bool, concurrency int, onError string, zipVars []string, zipSources []Expr) string {
	var b strings.Builder
	isZip := len(zipSources) > 1
	if parallel {
		head := "parallel for (" + escapeFieldName(varName) + " in " + RenderNCodaExpr(iterable)
		if isZip {
			head = "parallel for (" + zipClause(zipVars, zipSources)
		}
		if cfg := renderParallelForConfig(concurrency, onError); cfg != "" {
			head += ", " + cfg
		}
		b.WriteString(head + ") {\n")
	} else if isZip {
		b.WriteString("for (" + zipClause(zipVars, zipSources) + ") {\n")
	} else {
		b.WriteString(fmt.Sprintf("for (%s in %s) {\n", escapeFieldName(varName), RenderNCodaExpr(iterable)))
	}
	for _, inner := range body.Stmts {
		b.WriteString(RenderNCodaStmt(inner, indent+1) + "\n")
	}
	b.WriteString(strings.Repeat("    ", indent) + "}")
	return b.String()
}

// renderParallelForConfig 渲染 `parallel for` 配置子句的**规范形**：
// **等于语言缺省 ⇒ 不写**（2026-09-29 语言级放宽；正本 =
// docs/five-step/spec/F3-error-policy-container-axis.md §②）。
//
// 缺省的取值**不在这里**（唯一处 = pkg/ir/parallel_for_defaults.go）——
// 本函数只判「等于缺省吗」，不重写数值；否则同一份缺省会漂成两处。
//
// 键序 = 规范序（`concurrency` 在前、`on_error` 在后），与 parser 的接受面无关
// （parser 键序任意）：**规范形唯一** 才让「同一语义两种写法」有判据。
func renderParallelForConfig(concurrency int, onError string) string {
	parts := make([]string, 0, 2)
	if concurrency > 0 && concurrency != ir.ParallelForDefaultConcurrency {
		parts = append(parts, fmt.Sprintf("concurrency: %d", concurrency))
	}
	if onError != "" && onError != ir.ParallelForDefaultOnError {
		parts = append(parts, "on_error: "+onError)
	}
	return strings.Join(parts, ", ")
}

// zipClause 渲染 "a, b in zip(x, y)"（变量名即端口名，逗号分隔）。
func zipClause(vars []string, sources []Expr) string {
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		names = append(names, escapeFieldName(v))
	}
	srcs := make([]string, 0, len(sources))
	for _, src := range sources {
		srcs = append(srcs, RenderNCodaExpr(src))
	}
	return strings.Join(names, ", ") + " in zip(" + strings.Join(srcs, ", ") + ")"
}

// ncodaFloatText 渲染标量浮点字面量。
//
// ncoda lexer 的浮点字面量形状 = `digits '.' digits`（无科学计数法、无 'f'
// 后缀）⇒ **取整数值必须显式补 `.0`**：`strconv.FormatFloat(1.0,'f',-1,64)` = `"1"`，
// 直接落地会被词法读成 INT_LITERAL ⇒ 值类型丢失（R-98：`{type: float, value: 1}`
// → `1` → `{type: integer, value: 1}`，c0 ≠ c1）。
//
// spellType = true 表示该标量的**拼写就是它的类型载体**（见 LiteralExpr.SpellType）：
// 取整值补 `.0`，让读回仍是浮点。false 表示类型由别处承载（JSON 值树成员 = JSON
// 只有一种 number；具名 schema 键）⇒ 取整值写整数，与既有文本 / 编译器再序列化
// （`encoding/json` 把 `float64(1)` 写成 `1`）逐字一致。
func ncodaFloatText(f float64, bitSize int, spellType bool) string {
	s := strconv.FormatFloat(f, 'f', -1, bitSize)
	if !spellType || strings.Contains(s, ".") {
		return s
	}
	return s + ".0"
}

// RenderNCodaExpr 渲染表达式（ncoda 方言：字符串加引号、dict 键加引号）。
// ncodaBinaryLevel 是渲染侧要用的**运算符优先级**（数字越小结合越松），逐层对应
// pkg/parser 的下降链：parseTernary > parseNullCoalesce(??) > parseOr(||) >
// parseAnd(&&) > parseEquality(==/!=) > parseComparison > parseAddition >
// parseMultiplication。
var ncodaBinaryLevel = map[string]int{
	"??": 1, "||": 2, "&&": 3, "==": 4, "!=": 4,
	"<": 5, ">": 5, "<=": 5, ">=": 5, "in": 5, "not in": 5,
	"+": 6, "-": 6, "*": 7, "/": 7, "%": 7,
}

// renderBinaryExpr 拼二元表达式，并在**优先级/结合性要求处补括号**。不补就变义
// （一手，2026-09-30）：我们自己的布尔 shim `((n == 2) or (m == 3)) and (k == 5)`
// 读回成 `n == 2 || m == 3 && k == 5` —— `&&` 结合更紧 ⇒ 实际是
// `n == 2 || (m == 3 && k == 5)`，与产物语义不同。shim 契约的一半就是「我们造的
// shim 精确互逆」，所以括号是**语义**，不是排版偏好。
func renderBinaryExpr(v *BinaryExpr) string {
	left := RenderNCodaExpr(v.Left)
	right := RenderNCodaExpr(v.Right)
	level, known := ncodaBinaryLevel[v.Op]
	if !known {
		return left + " " + v.Op + " " + right
	}
	wrap := func(expr Expr, rendered string, isRight bool) string {
		child, ok := expr.(*BinaryExpr)
		if !ok {
			return rendered
		}
		if v.Op == "??" || child.Op == "??" {
			// `??` 与 `||`/`&&` 无括号混用是**语法错误**（parser.coalesceMixError）
			// ⇒ 两侧只要有二元子式就加括号。
			return "(" + rendered + ")"
		}
		cl, ok := ncodaBinaryLevel[child.Op]
		if !ok {
			return rendered
		}
		if cl < level {
			return "(" + rendered + ")"
		}
		// 左子式同层不必加括号（左结合解析回来就是同一棵树）；**右子式**同层只有
		// 真正可结合的算子对能省（`a - (b - c)` / `a / (b / c)` 省了就变义）。
		if isRight && cl == level && !associativeBinaryPair(v.Op, child.Op) {
			return "(" + rendered + ")"
		}
		return rendered
	}
	return wrap(v.Left, left, false) + " " + v.Op + " " + wrap(v.Right, right, true)
}

// associativeBinaryPair 报告 `a OP (b OP c)` 与 `(a OP b) OP c` 同义（同形算子且
// 该算子可结合）。只有这四个能省同层右括号。
func associativeBinaryPair(parent, child string) bool {
	if parent != child {
		return false
	}
	switch parent {
	case "&&", "||", "+", "*":
		return true
	}
	return false
}

func RenderNCodaExpr(e Expr) string {
	switch v := e.(type) {
	case *ForExpr:
		return renderForExprFlags(v.Var, v.Iterable, v.Body, 0, v.Parallel, v.Concurrency, v.OnError, v.LoopVars(), v.LoopSources())
	case *IdentExpr:
		// 标识符关键字碰撞 → $ 转义（NCODA-DOLLAR-ESCAPE-SPEC）:合成参数/
		// 会话变量/循环变量等外部命名可能是 ncoda 关键字（file/type/var），
		// 词法层 $type → IDENTIFIER("type")，roundtrip 零改写。
		return escapeFieldName(v.Name)
	case *LiteralExpr:
		switch val := v.Value.(type) {
		case nil:
			return "null"
		case string:
			return quoteNCoda(val)
		case bool:
			if val {
				return "true"
			}
			return "false"
		case float64:
			// ncoda lexer 只支持 digits[.digits]，无科学计数法；'f' 保证无指数。
			return ncodaFloatText(val, 64, v.SpellType)
		case map[string]any:
			// 字面量 object（来自节点输入 val）→ 有序 dict 字面量。
			keys := make([]string, 0, len(val))
			for k := range val {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			pairs := make([]string, 0, len(keys))
			for _, k := range keys {
				pairs = append(pairs, fmt.Sprintf("%s: %s", strconv.Quote(k),
					RenderNCodaExpr(&LiteralExpr{Value: val[k]})))
			}
			return "{ " + strings.Join(pairs, ", ") + " }"
		case []any:
			items := make([]string, 0, len(val))
			for _, item := range val {
				items = append(items, RenderNCodaExpr(&LiteralExpr{Value: item}))
			}
			return "[" + strings.Join(items, ", ") + "]"
		case float32:
			return ncodaFloatText(float64(val), 32, v.SpellType)
		case int:
			return strconv.Itoa(val)
		case int64:
			return strconv.FormatInt(val, 10)
		default:
			return fmt.Sprintf("%v", v.Value)
		}
	case *CallExpr:
		args := make([]string, 0, len(v.Args))
		for _, a := range v.Args {
			args = append(args, RenderNCodaExpr(a))
		}
		// 转义边界（NCODA-DOLLAR-ESCAPE-SPEC）：$ 只服务"工作流标识符"（字段/
		// 参数/工具操作名），不服务语言内建调用。无 Receiver = 语言内建调用
		// （file(...) cast / output(...) 消息发射器），callee 保持关键字原样；
		// 有 Receiver = 外部工具操作名 p.op(...)，操作名撞 ncoda 关键字时需
		// $ 转义（词法剥 $ 还原为原始契约名，roundtrip 闭合）。
		callee := v.Callee
		if v.Receiver != nil {
			callee = escapeFieldName(v.Callee)
			callee = RenderNCodaExpr(v.Receiver) + "." + callee
		}
		typeArgs := ""
		if len(v.TypeArgs) > 0 {
			typeArgs = "<" + strings.Join(v.TypeArgs, ", ") + ">"
		}
		return callee + typeArgs + "(" + strings.Join(args, ", ") + ")"
	case *OperationPolicyExpr:
		// 策略后缀是**表达式**的一部分（parser 在 postfix 之后吃 `with`，
		// 见 pkg/parser/expr.go），所以包装层直接拼在调用后面。
		return RenderNCodaExpr(v.Base) + renderOperationPolicySuffix(v)
	case *ForeignCodeExpr:
		return renderForeignCode(v)
	case *TemplateValue:
		// 具名名额形态（值位）：`template "…{{k}}…" using { k: v.x }`。
		return renderTemplateClause(v.Text, v.Bindings)
	case *TemplateString:
		// 反引号模板串；字面段含反引号时 ncoda lexer 无转义 → 降级为纯字面量，
		// 且每个插值被写成 `{{ref}}` —— **占位符名字被销毁、绑定随之丢失**。
		// 这是**已登记残余**（规则表 C1-llm-call 的 C1-H1），不是正确行为：
		// 早先这句注释写的「语料实测 0 例」**已被实测证伪**（现网产物里已有含
		// `{{ref}}` 的文件 ⇒ 该退化路径在语料上有真实命中）。修它需要渲染期
		// 拿到「插值 ↔ 绑定名」的对应表，属单独立项。
		needsPlain := false
		for _, part := range v.Parts {
			if strings.Contains(part.Text, "`") {
				needsPlain = true
				break
			}
		}
		if needsPlain {
			var plain strings.Builder
			for _, part := range v.Parts {
				plain.WriteString(part.Text)
				if part.Expr != nil {
					plain.WriteString("{{ref}}")
				}
			}
			return quoteNCoda(plain.String())
		}
		var sb strings.Builder
		sb.WriteString("`")
		for _, part := range v.Parts {
			sb.WriteString(part.Text)
			if part.Expr != nil {
				sb.WriteString("${")
				sb.WriteString(RenderNCodaExpr(part.Expr))
				sb.WriteString("}")
			}
		}
		sb.WriteString("`")
		return sb.String()
	case *LambdaExpr:
		// ncoda 既有 lambda 语法 `(params) -> body`（filter/take/sort 谓词）。
		return "(" + strings.Join(v.Params, ", ") + ") -> " + RenderNCodaExpr(v.Body)
	case *UnaryExpr:
		return v.Op + RenderNCodaExpr(v.Operand)
	case *BinaryExpr:
		return renderBinaryExpr(v)
	case *TernaryExpr:
		// 条件统一带括号：忠实 Python 序列化器对 BinaryExpr 条件的强制
		// ParenExpr（pyast_lower lowerCondChild），且嵌套三元作条件时
		// 无歧义（ncoda parseTernary 的条件只解析到 or 层）。
		return "(" + RenderNCodaExpr(v.Cond) + ") ? " + RenderNCodaExpr(v.Then) + " : " + RenderNCodaExpr(v.Else)
	case *IndexExpr:
		return RenderNCodaExpr(v.Obj) + "[" + RenderNCodaExpr(v.Index) + "]"
	case *MemberExpr:
		obj := RenderNCodaExpr(v.Obj)
		// 关键字字段名（type/output）经 escapeFieldName 写为 $type/$output
		// （词法剥 '$' 还原为原始标识符，roundtrip 闭合）；非关键字原样。
		// 点号形态反编译后是 FieldAccessExpr（code_analysis 认可的
		// selector 形态）；方括号 IndexExpr 会被 code input selector
		// 检查拒绝（CODE_INPUT_SELECTOR）。
		return obj + "." + escapeFieldName(v.Field)
	case *DictExpr:
		if len(v.Keys) == 0 {
			return "{}"
		}
		pairs := make([]string, 0, len(v.Keys))
		for i, k := range v.Keys {
			pairs = append(pairs, fmt.Sprintf("%s: %s", strconv.Quote(k), RenderNCodaExpr(v.Values[i])))
		}
		return "{ " + strings.Join(pairs, ", ") + " }"
	case *ListExpr:
		items := make([]string, 0, len(v.Items))
		for _, item := range v.Items {
			items = append(items, RenderNCodaExpr(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	default:
		return fmt.Sprintf("<expr %T>", e)
	}
}

// quoteNCoda 按 ncoda lexer 支持的子集转义字符串字面量：
// \n \t \r \\ \' \" 用转义，其余字符（含 U+200B 等控制字符、CJK、emoji）
// 原样输出 UTF-8——ncoda lexer 不支持 Go 的 \uXXXX 转义（escapeChar 仅 n/t/r/\\/'/"）。
// 与 coze-studio 一致：systemPrompt 是原始 UTF-8 字符串，不做 Go 风格二次转义。

// renderAttemptStmt 渲染 attempt <op> as <r> { success {…} failure(<e>) {…} }。
// 两臂都用花括号块（ncoda 语法要求 success / failure(<e>) 均在场，空臂 = 空块）。
func renderAttemptStmt(v *AttemptStmt, pad string, indent int) string {
	ind := func(n int) string { return strings.Repeat("    ", n) }
	var b strings.Builder
	b.WriteString(pad + "attempt " + RenderNCodaExpr(v.Operation) + " as " + escapeFieldName(v.Result) + " {\n")
	b.WriteString(ind(indent+1) + "success {\n")
	if v.Success != nil {
		for _, inner := range v.Success.Stmts {
			b.WriteString(RenderNCodaStmt(inner, indent+2) + "\n")
		}
	}
	b.WriteString(ind(indent+1) + "}\n")
	b.WriteString(ind(indent+1) + "failure(" + escapeFieldName(v.Error) + ") {\n")
	if v.Failure != nil {
		for _, inner := range v.Failure.Stmts {
			b.WriteString(RenderNCodaStmt(inner, indent+2) + "\n")
		}
	}
	b.WriteString(ind(indent+1) + "}\n")
	b.WriteString(pad + "}")
	return b.String()
}

// renderRequestInputStmt 渲染两种 `ask` 形态（规范形 —— 一语义一拼写）：
//
//	ask<Form, Action>({content, fields, actions[, timeout]}) as r { action … }  // 有动作
//	ask<Form>({content, fields}) as r;                                        // 无动作（S1）
//
// **无动作形态**（`ActionType == ""`，用户 2026-10-01 批准）：平台
// `answer_type: text` 的问答节点 —— 无选项、无分支口、单条无标签推进口
// ⇒ 没有臂就没有块（`;` 结尾），config 里也不写 `actions`/`timeout`
// （两个键在无动作形态下没有载体；语言侧同解，见 target_validation）。
func renderRequestInputStmt(v *RequestInputStmt, pad string, indent int) string {
	noAction := v.ActionType == "" && len(v.ActionBranches) == 0 && v.TimeoutBody == nil
	var b strings.Builder
	ind := func(n int) string { return strings.Repeat("    ", n) }
	if noAction {
		b.WriteString(pad + "ask<" + v.FormType + ">({\n")
	} else {
		b.WriteString(pad + "ask<" + v.FormType + ", " + v.ActionType + ">({\n")
	}
	// content
	b.WriteString(ind(indent+1) + "content: " + RenderNCodaExpr(v.Content) + ",\n")
	// fields
	b.WriteString(ind(indent+1) + "fields: {\n")
	for i, f := range v.Fields {
		b.WriteString(ind(indent+2) + escapeFieldName(f.Name) + ": { kind: " + strconv.Quote(f.Kind) + " }")
		if len(f.Options) > 0 {
			opts := make([]string, len(f.Options))
			for j, o := range f.Options {
				opts[j] = strconv.Quote(o)
			}
			b.WriteString(", options: [" + strings.Join(opts, ", ") + "]")
		}
		if i < len(v.Fields)-1 {
			b.WriteString(",\n")
		} else {
			b.WriteString("\n")
		}
	}
	b.WriteString(ind(indent+1) + "},\n")
	// ── question 参数面 ②③（r57，2026-10-10 用户批准「丙·扩语言」）──────────
	// 规范形键序（I1 逐字节判据依赖它）：content → fields → limit → extra_output
	// → llm → dynamic_options → actions → timeout。**只在有值时写**（键存在性
	// = 平台导出写没写 ⇒ 缺席就是不写）。
	if v.Limit > 0 {
		b.WriteString(ind(indent+1) + "limit: " + strconv.Itoa(v.Limit) + ",\n")
	}
	if v.ExtraOutput {
		b.WriteString(ind(indent+1) + "extra_output: true,\n")
	}
	if v.LLM != nil {
		b.WriteString(ind(indent+1) + "llm: {")
		parts := make([]string, 0, 8)
		if v.LLM.Model != nil {
			parts = append(parts, "model: "+strconv.Quote(*v.LLM.Model))
		}
		if v.LLM.ModelName != nil {
			parts = append(parts, "model_name: "+strconv.Quote(*v.LLM.ModelName))
		}
		if v.LLM.Temperature != nil {
			parts = append(parts, "temperature: "+formatNCodaFloat(*v.LLM.Temperature))
		}
		if v.LLM.MaxTokens != nil {
			parts = append(parts, "max_tokens: "+strconv.Itoa(*v.LLM.MaxTokens))
		}
		if v.LLM.TopP != nil {
			parts = append(parts, "top_p: "+formatNCodaFloat(*v.LLM.TopP))
		}
		if v.LLM.ResponseFormat != nil {
			parts = append(parts, "response_format: "+strconv.Itoa(*v.LLM.ResponseFormat))
		}
		if v.LLM.GenerationDiversity != nil {
			parts = append(parts, "generation_diversity: "+strconv.Quote(*v.LLM.GenerationDiversity))
		}
		if v.LLM.SystemPrompt != nil {
			parts = append(parts, "system_prompt: "+strconv.Quote(*v.LLM.SystemPrompt))
		}
		b.WriteString(strings.Join(parts, ", ") + " },\n")
	}
	if v.DynamicOptions != nil {
		b.WriteString(ind(indent+1) + "dynamic_options: " + RenderNCodaExpr(v.DynamicOptions) + ",\n")
	}
	if noAction {
		// 无臂 ⇒ 无块（`;` 结尾）；config 不带 actions/timeout。
		b.WriteString(pad + "}) as " + escapeFieldName(v.ResultBinding) + ";")
		return b.String()
	}
	// actions
	b.WriteString(ind(indent+1) + "actions: {\n")
	for i, a := range v.Actions {
		b.WriteString(ind(indent+2) + escapeFieldName(a.Member) + ": { title: " + strconv.Quote(a.Title) + ", style: " + strconv.Quote(a.Style) + " }")
		if i < len(v.Actions)-1 {
			b.WriteString(",\n")
		} else {
			b.WriteString("\n")
		}
	}
	b.WriteString(ind(indent+1) + "},\n")
	// timeout config (可选)
	if v.TimeoutValue > 0 && v.TimeoutUnit != "" {
		b.WriteString(ind(indent+1) + "timeout: { value: " + strconv.Itoa(v.TimeoutValue) + ", unit: " + strconv.Quote(v.TimeoutUnit) + " },\n")
	}
	b.WriteString(pad + "}) as " + escapeFieldName(v.ResultBinding) + " {\n")
	// action branches
	for _, br := range v.ActionBranches {
		b.WriteString(ind(indent+1) + "action " + v.ActionType + "." + escapeFieldName(br.Member) + " {\n")
		if br.Body != nil {
			for _, inner := range br.Body.Stmts {
				b.WriteString(RenderNCodaStmt(inner, indent+2) + "\n")
			}
		}
		b.WriteString(ind(indent+1) + "}\n")
	}
	// timeout branch (可选)
	if v.TimeoutBody != nil {
		b.WriteString(ind(indent+1) + "timeout {\n")
		for _, inner := range v.TimeoutBody.Stmts {
			b.WriteString(RenderNCodaStmt(inner, indent+2) + "\n")
		}
		b.WriteString(ind(indent+1) + "}\n")
	}
	b.WriteString(pad + "}")
	return b.String()
}

// formatNCodaFloat 渲染浮点字面量：整数值**不带小数点**（`1` 而不是 `1.0`），
// 与解析端「int 可拓宽成 float」的既有口径一致 —— 往返不引入 `1` ↔ `1.0` 的
// 拼写抖动（纯函数、确定）。
func formatNCodaFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func quoteNCoda(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\'':
			b.WriteString("\\'")
		case '\n':
			b.WriteString("\\n")
		case '\t':
			b.WriteString("\\t")
		case '\r':
			b.WriteString("\\r")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// renderForeignCode 渲染 foreign code 表达：
//
//	foreign code <lang>(<type> <name> = <expr>, ...) -> <契约> { source `<原文>`; }
//
// 输出契约：Direct（单 result 端口）→ `-> <type>`；structural → `-> { name: type, ... }`
// （单字段也走 structural，保端口名 roundtrip——parser 已放行 ≥1 字段）。
// escapeCodeSource 转义 foreign code source 文本，使其可作为 ncoda 模板串字面
// 逐字保留（透传语义不变）：
//
//	`  → \`   （模板串定界符转义）
//	\ → \\  （反斜杠转义，防 \`/\$ 组合歧义）
//	${ → \${  （禁 ncoda 插值；JS 模板字面量原文保留）
//
// ncoda lexer 已支持反斜杠转义反引号/反斜杠/美元（见 lexer.scanBacktickString），
// parser 解析后 TemplatePart.Text 还原为原始代码，正编译 templateText 拼接
// 得到与 canvas 完全一致的字节。
func escapeCodeSource(code string) string {
	var sb strings.Builder
	for i := 0; i < len(code); i++ {
		ch := code[i]
		switch ch {
		case '`':
			sb.WriteString("\\`")
		case '\\':
			sb.WriteString("\\\\")
		case '$':
			if i+1 < len(code) && code[i+1] == '{' {
				sb.WriteString("\\${")
				i++ // 消费 '{'（已随转义写入）
			} else {
				sb.WriteByte(ch)
			}
		default:
			sb.WriteByte(ch)
		}
	}
	return sb.String()
}

// renderUnknownNotes 渲染 UNKNOWN 注释块（反编译类型降级归因，L3-b）。
// 产物尾部输出，按原因聚合计数；无降级返回空串（产物与 L3-b 前逐字节
// 一致）。注释不参与 ncoda 语义（语法自检忽略），roundtrip 零影响。
func renderUnknownNotes(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, n := range notes {
		counts[n]++
	}
	reasons := make([]string, 0, len(counts))
	for r := range counts {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	var b strings.Builder
	b.WriteString("\n// UNKNOWN: 类型信息缺失降级为 any（非显式声明；L3-b 归因）\n")
	for _, r := range reasons {
		fmt.Fprintf(&b, "//   - %s: %d\n", r, counts[r])
	}
	return b.String()
}

// renderMergeNotes 渲染 MERGE 修正注释块：非互斥位置的 variable_merge 一律
// 按用户拍板（2026-09-02）自作主张修正（merge 只用于互斥分支收敛），每种
// 改写形态（or 多候选→|| / alias 单候选→直引 / default 空组→类型默认值 /
// collect 标量收集→数组）都记录归因，产物尾部按节点聚合输出——可 grep 可
// 统计（口子开大：不限于多候选 || 链）。注释不参与 ncoda 语义（语法自检
// 忽略），roundtrip 零影响——与 renderUnknownNotes 同契约。
func renderMergeNotes(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, n := range notes {
		counts[n]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("\n// MERGE 修正（非互斥 variable_merge 改写归因：alias=单候选直引 / default=空组默认值 / collect=标量收集为数组；多候选无互斥证据 = UNKNOWN fail loud，不留 ??（coze-biz nil-less zero 语义，2026-09-09））\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "//   - 节点 %s: %d\n", k, counts[k])
	}
	return b.String()
}

func renderForeignCode(f *ForeignCodeExpr) string {
	var sb strings.Builder
	sb.WriteString("foreign code ")
	sb.WriteString(f.Language)
	sb.WriteString("(")
	for i, in := range f.Inputs {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(in.Type)
		sb.WriteString(" ")
		sb.WriteString(escapeFieldName(in.Name))
		sb.WriteString(" = ")
		sb.WriteString(RenderNCodaExpr(in.Expr))
	}
	sb.WriteString(") -> ")
	if f.Direct {
		sb.WriteString(f.DirectType)
	} else {
		sb.WriteString("{ ")
		for i, o := range f.Output {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(escapeFieldName(o.Name))
			sb.WriteString(": ")
			sb.WriteString(o.Type)
			sb.WriteString(";")
		}
		sb.WriteString(" }")
	}
	sb.WriteString(" { source ")
	sb.WriteString(renderCodeSourceTemplate(f.Source))
	sb.WriteString("; }")
	return sb.String()
}

// renderCodeSourceTemplate 渲染 foreign code source 模板串：对每个字面段做
// 模板串转义（反引号/反斜杠/${），保留 ${expr} 插值部分不变（反编译的
// foreign code source 无插值——代码原文透传；这里仅为完整性支持插值段）。
func renderCodeSourceTemplate(ts *TemplateString) string {
	if ts == nil {
		return "``"
	}
	var sb strings.Builder
	sb.WriteString("`")
	for _, part := range ts.Parts {
		sb.WriteString(escapeCodeSource(part.Text))
		if part.Expr != nil {
			sb.WriteString("${")
			sb.WriteString(RenderNCodaExpr(part.Expr))
			sb.WriteString("}")
		}
	}
	sb.WriteString("`")
	return sb.String()
}

// renderOperationPolicySuffix 渲染 `with retry(max:N), timeout(<D>s)`。
//
// 顺序固定 retry → timeout（parser 按逗号顺序读，语义无关但文本必须稳定，
// 否则死代码守卫的"渲染前后是否有变化"判据会每轮翻新）。timeout 用整秒，
// 因为编译端要求 TimeoutMs % 1000 == 0（type_infer 的 Timeout policy 校验）；
// 非整秒的值不得进这里（构造处已拦）。
func renderOperationPolicySuffix(v *OperationPolicyExpr) string {
	parts := make([]string, 0, 3)
	if v.Retry > 0 {
		parts = append(parts, fmt.Sprintf("retry(max: %d)", v.Retry))
	}
	if v.Timeout > 0 {
		parts = append(parts, fmt.Sprintf("timeout(%ds)", v.Timeout/1000))
	}
	if v.Default != nil {
		parts = append(parts, "default("+RenderNCodaExpr(v.Default)+")")
	}
	if len(parts) == 0 {
		return ""
	}
	return " with " + strings.Join(parts, ", ")
}
