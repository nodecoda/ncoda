package decompile

// convert.go —— 图 → ncoda 结构（Phase B 转换层）。
//
// 节点 → Stmt 映射（复用 cozebiz/decompile 的 ncoda 语法树 ast4.go +
// render_ncoda.go）:
//
//	start   → main 函数参数（variables → 类型化形参）
//	llm     → let v<id> = llm(<const>, {"messages": [...], ...}); 输出 v<id>.text
//	if-else → If(条件, then, else)（sourceHandle true/false 分支）
//	answer  → return <模板值>（dify answer 聚合即流程输出）
//	end     → return <outputs>
//	code/http/tool/template/knowledge/loop/iteration → 逐步扩展
//
// 未知节点类型 → Comment("UNKNOWN") + 跳过（渐进式,覆盖清单见文档）。
//
// 引用还原：value_selector [node,key,path...] → 变量表达式;模板
// {{#node.key#}} → ${expr} 插值（TemplateString）。

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/nodecoda/ncoda/pkg/backend/llmprojection"
	cd "github.com/nodecoda/ncoda/pkg/ncoda/ast4"
	"github.com/nodecoda/ncoda/pkg/ncoda/render"
	"github.com/nodecoda/ncoda/pkg/ncoda/shim"
)

// templateRefRe 匹配 dify 模板占位符 {{#<node>.<key>#}}（key 可带路径
// 段,如 {{#node.a.b#}}）。
var templateRefRe = regexp.MustCompile(`\{\{#([^}]+)#\}\}`)

// refExpr 把 value_selector [node,key,path...] / 模板引用节点名还原为
// ncoda 表达式:
//
//	node == "sys" → IdentExpr(字段名)（dify 伪节点 sys.query/sys.user_id …
//	               = 工作流运行时内建输入 → 合成 main 参数,与黄金对照
//	               `function main(string query, ...)` 对齐;去重）
//	node == start → IdentExpr(参数名)（start 变量即函数参数,裸名可用）
//	其他         → v<node>.<key>[.path...]（member 链）
func (c *converter) refExpr(nodeID, key string, path []string) cd.Expr {
	if member, ok := c.askActionRef[nodeID]; ok {
		// ask 节点内部引用:action 分支内 __action_id → 该分支 member
		// 字面量;timeout 分支内（member==""）→ 空串（超时未点击）。
		if key == "__action_id" && member != "" {
			return &cd.LiteralExpr{Value: member}
		}
		return &cd.LiteralExpr{Value: ""}
	}
	if nodeID == "sys" || nodeID == "conversation" {
		// dify 伪节点:sys.* = 运行时系统输入;conversation.* = 会话变量。
		// 会话变量被 assigner 写 → 顶层 `var <name>;` 可变声明（LoopStatePass
		// 豁免无初值声明;@conversation 仅 advanced-chat 可用）;只读 → 合成
		// string 参数（workflow 模式近似,记入覆盖清单）。
		if nodeID == "conversation" && c.isConvVar(key) {
			return &cd.IdentExpr{Name: c.convName(key)}
		}
		c.synthParam(key)
		return &cd.IdentExpr{Name: key}
	}
	if nodeID == "env" {
		// dify 环境变量命名空间:[env, <name>] → 顶层 const <name> = <值>;
		if e := c.envIdent(key); e != nil {
			return e
		}
	}
	// 循环变量裸名引用:[loopID, <label>] → <label>;iteration 的 item/output
	// 体内 → item（当前元素）;iteration.output 体外 → 收集数组 v<iterID>。
	if labels := c.loopVarLabels[nodeID]; labels != nil {
		if labels[key] {
			return &cd.IdentExpr{Name: key}
		}
		if key == "output" && c.g.nodes[nodeID] != nil && c.g.nodes[nodeID].Type == "iteration" {
			if c.inIterationBody {
				return &cd.IdentExpr{Name: "item"}
			}
			return &cd.IdentExpr{Name: varName(nodeID)}
		}
	}
	// template-transform / list-operator 的单输出即节点值:
	// [tplID, output] / [listopID, output] → v<id>（filter 结果数组）。
	if key == "output" && c.g.nodes[nodeID] != nil &&
		(c.g.nodes[nodeID].Type == "template-transform" || c.g.nodes[nodeID].Type == "list-operator") {
		return &cd.IdentExpr{Name: varName(nodeID)}
	}
	// Reversed compiler-owned code shims are values, not physical nodes:
	// every downstream port reference aliases directly to the original
	// ncoda expression (single-expression shims only; record shims remain
	// materialized because their fields are real ports).
	if expr, ok := c.shimExprs[nodeID]; ok {
		return expr
	}
	// parameter-extractor:字段引用走 extract 的 value 信封
	// [extractor, <field>] → v<id>.value.<field>;ok/reason/usage 信封输出直访。
	if c.g.nodes[nodeID] != nil && c.g.nodes[nodeID].Type == "parameter-extractor" &&
		key != "" && key != "ok" && key != "reason" && key != "usage" {
		path = append([]string{"value"}, append([]string{key}, path...)...)
		key = ""
	}
	// 悬空引用:nodeID 不在图中（源稿删节点/笔误留下的 selector）→ 空值
	// （dify 运行时未解析模板 = 空串,忠实;不得捏造 v<id> 引发 UNDEFINED_VAR）。
	if c.g.nodes[nodeID] == nil {
		return &cd.LiteralExpr{Value: ""}
	}
	if producerID, ok := c.llmEnvelopeAdapterProducer(c.g.nodes[nodeID]); ok && key == "response" {
		return &cd.IdentExpr{Name: varName(producerID)}
	}
	if c.g.nodes[nodeID] != nil && c.g.nodes[nodeID].Type == "llm" && key == "structured_output" && len(path) > 0 && path[0] == "value" {
		// Dify's structured-output wrapper is a physical adapter. Re-enter
		// the canonical semantic envelope at text, dropping the wrapper.
		key = "text"
		path = path[1:]
	}
	var e cd.Expr
	if nodeID == c.startID {
		// start 节点选择器可能是点串伪输出（dify 导出 [<startID>, "sys.query"]
		// 形态——start 节点的 sys.* / conversation.* / env.* 伪输出）:
		// 按前缀路由到对应伪节点语义,裸键（普通 start 变量）原样。
		if sub, ok := strings.CutPrefix(key, "sys."); ok && sub != "" {
			c.synthParam(sub)
			return &cd.IdentExpr{Name: sub}
		}
		if sub, ok := strings.CutPrefix(key, "conversation."); ok && sub != "" && c.isConvVar(sub) {
			return &cd.IdentExpr{Name: c.convName(sub)}
		}
		if sub, ok := strings.CutPrefix(key, "env."); ok && sub != "" {
			if e := c.envIdent(sub); e != nil {
				return e
			}
		}
		// start 未声明的字段（表单 variables 无此名,如作者遗留的
		// [startID, "marketType"]）→ 空字面量（dify 运行时该引用为空,
		// 忠实;不得裸名引发 UNDEFINED_VAR）。
		if !c.startVars[key] {
			return &cd.LiteralExpr{Value: ""}
		}
		e = &cd.IdentExpr{Name: key}
	} else {
		e = &cd.IdentExpr{Name: varName(nodeID)}
		segs := append([]string{key}, path...)
		// variable-aggregator 组输出信封剥离:组值已还原为 dict 值/单值,
		// dify 的尾部 "output" 段（[agg, GroupX, output] / [agg, output]）
		// 是平台信封（同 cozebiz output_value 单端口包装）,剥掉——
		// 保留会生成运行时未定义的 .output 访问。
		if c.g.nodes[nodeID] != nil && c.g.nodes[nodeID].Type == "variable-aggregator" &&
			len(segs) > 0 && segs[len(segs)-1] == "output" {
			segs = segs[:len(segs)-1]
		}
		for _, seg := range segs {
			if seg == "" {
				continue
			}
			e = &cd.MemberExpr{Obj: e, Field: seg}
		}
	}
	return e
}

// synthParam 注册一个合成 main 参数（sys.* / conversation.* 字段 →
// string 参数）。start 已有同名变量时跳过——同名引用落到 start 参数,
// 避免 DUPLICATE_DEF,且类型以 start 声明的为准。
// sysFieldType dify 系统字段（sys.*）→ ncoda 类型。字段集是平台固定
// 契约（sys.query/files/conversation_id/user_id/app_id/...）;未知字段
// 默认 string（软降级,记覆盖清单）。
func sysFieldType(name string) string {
	switch name {
	case "files":
		return "array<file>"
	case "files_count", "dialogue_count":
		return "int"
	case "query", "conversation_id", "user_id", "app_id", "workflow_id",
		"workflow_run_id", "assistant_id", "message_id", "created_by",
		"conversation_mode", "file_upload":
		return "string"
	default:
		return "string"
	}
}

func (c *converter) synthParam(name string) {
	if name == "" {
		return
	}
	if c.startVars[name] {
		return
	}
	if c.paramSeen == nil {
		c.paramSeen = map[string]bool{}
	}
	if c.paramSeen[name] {
		return
	}
	c.paramSeen[name] = true
	c.extraParams = append(c.extraParams, sysFieldType(name)+" "+render.EscapeIdent(name))
}

// startVarNames 预收集 start 变量名（convert 前调用,供 synthParam 去重）。
func (c *converter) collectStartVars() {
	c.startVars = map[string]bool{}
	for _, v := range asSlice(c.g.start.Data["variables"]) {
		m := asMap(v)
		if name := asStr(m["variable"]); name != "" {
			c.startVars[name] = true
		}
	}
}

// onPath 报告 nodeID 是否在当前执行路径上（answer 聚合点判定）。
func (c *converter) onPath(nodeID string) bool {
	for _, id := range c.path {
		if id == nodeID {
			return true
		}
	}
	return false
}

// pathTerminal 当前路径的末端产值 producer（answer 的直接上游中最后一个
// 产值节点;跳过 start/answer 自身——answer 节点在 convertAnswer 之前已
// 入路径）。非产值节点（if-else 等）不产变量,不能作为替换目标。路径上
// 无产值节点 → ""（此时 answer 引用按空串处理,忠实 dify 未解析模板）。
func (c *converter) pathTerminal() string {
	for i := len(c.path) - 1; i >= 0; i-- {
		id := c.path[i]
		if id == c.g.start.ID || id == c.curNodeID {
			continue
		}
		if n := c.g.nodes[id]; n != nil && isValueProducer(n.Type) {
			return id
		}
	}
	return ""
}

// isValueProducer 节点类型是否产出可引用的变量（可作为 answer 路径末端）。
// 非产值:start/if-else/answer/end/question-classifier（控制流/聚合点）;
// loop/iteration/assigner（循环/状态写,引用走循环变量裸名,不注册 v 变量）。
func isValueProducer(typ string) bool {
	switch typ {
	case "start", "if-else", "answer", "end", "question-classifier",
		"loop", "iteration", "assigner", "loop-start", "iteration-start":
		return false
	}
	return true
}

// resolveRefNode 引用节点 → 生效节点 id（answer/end 共用）:
//
//	sys / conversation / start → 原样（合成参数 / 函数参数）
//	∈ declared（当前作用域已声明,含 parallel 透明透出）→ 原样
//	有产值末端（pathTerminal）→ 末端（共享聚合点:模板写"分支到达时输出
//	                              的值",兄弟分支 producer 在本路径未执行,
//	                              取本路径末端——黄金对照 03/10 语义）
//	否则 → ""（dify 未执行节点 = 空,忠实语义）
func (c *converter) resolveRefNode(node string) string {
	if node == "sys" || node == "conversation" || node == c.startID {
		return node
	}
	// 节点不存在（悬空引用,如 {{#context#}} 源稿笔误）→ ""（dify 未解析
	// 模板 = 空）;不得用 pathTerminal 误绑（那会凭空捏造绑定）。
	if c.g.nodes[node] == nil {
		return ""
	}
	// loop/iteration 节点:循环变量走 refExpr 裸名解析（不替换）。
	if c.loopVarLabels[node] != nil {
		return node
	}
	if c.declared[varName(node)] {
		return node
	}
	if term := c.pathTerminal(); term != "" {
		return term
	}
	return ""
}

// refExprResolved 按 resolveRefNode 解析节点名后还原表达式;解析为空串
// → 空字符串字面量。
func (c *converter) refExprResolved(node, key string, path []string) cd.Expr {
	rnode := c.resolveRefNode(node)
	if rnode == "" {
		return &cd.LiteralExpr{Value: ""}
	}
	return c.refExpr(rnode, key, path)
}

// renderAnswerTemplate 渲染 answer 模板,按当前作用域解析引用:
//
//	sys.*           → 合成参数（永不替换）
//	引用节点 ∈ 路径  → 正常解析
//	引用节点 ∉ 路径  → 绑定到路径末端 producer（共享 answer 聚合点:模板
//	                  写的是"分支到达时输出的值",兄弟分支的 producer 在本
//	                  路径未执行,应取本路径末端值——黄金对照 03/10 的
//	                  `return <分支末端>.text` 语义）
//
// 末端无对应字段（模板 .text 而末端是 code 节点等）→ 保持原 key,由前端
// FIELD_NOT_FOUND 兜底暴露（覆盖清单记录,不猜字段名）。
func (c *converter) renderAnswerTemplate(tmpl string) cd.Expr {
	if !strings.Contains(tmpl, "{{#") {
		return &cd.LiteralExpr{Value: tmpl}
	}
	ts := &cd.TemplateString{}
	last := 0
	for _, m := range templateRefRe.FindAllStringSubmatchIndex(tmpl, -1) {
		start, end := m[0], m[1]
		inner := tmpl[m[2]:m[3]]
		if start > last {
			ts.Parts = append(ts.Parts, cd.TemplatePart{Text: tmpl[last:start]})
		}
		segs := strings.Split(inner, ".")
		node := segs[0]
		key := ""
		var path []string
		if len(segs) > 1 {
			key = segs[1]
			path = segs[2:]
		}
		ts.Parts = append(ts.Parts, cd.TemplatePart{Expr: c.refExprResolved(node, key, path)})
		last = end
	}
	if last < len(tmpl) {
		ts.Parts = append(ts.Parts, cd.TemplatePart{Text: tmpl[last:]})
	}
	if len(ts.Parts) == 1 {
		if ts.Parts[0].Expr != nil {
			return ts.Parts[0].Expr
		}
		return &cd.LiteralExpr{Value: ts.Parts[0].Text}
	}
	return ts
}

// selectorExpr 把 value_selector 数组还原为表达式。
func (c *converter) selectorExpr(sel []any) cd.Expr {
	if len(sel) == 0 {
		return &cd.LiteralExpr{Value: nil}
	}
	node := asStr(sel[0])
	key := asStr(sel[1])
	var path []string
	for _, p := range sel[2:] {
		path = append(path, asStr(p))
	}
	return c.refExpr(node, key, path)
}

// renderTemplate 把 dify 模板文本中的 {{#node.key#}} 占位符替换为
// ${expr} 插值,返回 TemplateString（无插值 → 纯文本 Literal）。
func (c *converter) renderTemplate(tmpl string) cd.Expr {
	if !strings.Contains(tmpl, "{{#") {
		return &cd.LiteralExpr{Value: tmpl}
	}
	ts := &cd.TemplateString{}
	last := 0
	for _, m := range templateRefRe.FindAllStringSubmatchIndex(tmpl, -1) {
		start, end := m[0], m[1]
		inner := tmpl[m[2]:m[3]]
		if start > last {
			ts.Parts = append(ts.Parts, cd.TemplatePart{Text: tmpl[last:start]})
		}
		segs := strings.Split(inner, ".")
		node := segs[0]
		key := ""
		var path []string
		if len(segs) > 1 {
			key = segs[1]
			path = segs[2:]
		}
		ts.Parts = append(ts.Parts, cd.TemplatePart{Expr: c.refExprResolved(node, key, path)})
		last = end
	}
	if last < len(tmpl) {
		ts.Parts = append(ts.Parts, cd.TemplatePart{Text: tmpl[last:]})
	}
	if len(ts.Parts) == 1 {
		if ts.Parts[0].Expr != nil {
			return ts.Parts[0].Expr // 纯插值:{{#x.y#}} → ${expr} 直接返回 expr
		}
		return &cd.LiteralExpr{Value: ts.Parts[0].Text}
	}
	return ts
}

// converter 一次反编译的转换上下文。
// dictPut 有序字典写键值（cozebiz 的 put 未导出,本地等价）。
func dictPut(d *cd.DictExpr, key string, val cd.Expr) {
	d.Keys = append(d.Keys, key)
	d.Values = append(d.Values, val)
}

// modelConst 一个 llm 模型常量（const MODEL_<i> = "<provider>/<name>"）。
type modelConst struct {
	ConstName string
	Model     string // "provider/name"
}

type converter struct {
	g               *wfGraph
	startID         string
	models          []modelConst               // 去重的模型常量（渲染为 const 声明）
	modelBy         map[string]int             // "provider/name" → models 下标
	used            map[string]bool            // 已用变量名（防 sanitize 冲突）
	path            []string                   // 当前 DFS 执行路径（answer 聚合按路径解析）
	curNodeID       string                     // 当前转换中的节点（pathTerminal 排除自身）
	startVars       map[string]bool            // start 变量名集合（synthParam 去重）
	extraParams     []string                   // 合成 main 参数（sys.* 字段）
	paramSeen       map[string]bool            // 参数名去重（start 变量 + sys 字段）
	imports         []string                   // import 声明（tool → "dify.<tool_name>"）
	declared        map[string]bool            // 当前作用域已声明的变量（v<id>,if 分支间隔离）
	askActionRef    map[string]string          // ask 节点 id → 当前 action member（空=timeout 分支;__action_id 映射）
	loopVarLabels   map[string]map[string]bool // loop/iteration 节点 → 循环变量名集（label 裸名引用）
	inIterationBody bool                       // 正在转换 iteration 体（answer/end 跳过,归外层流程）
	convVars        []string                   // 会话变量名（assigner 写目标 ∪ YAML 声明,预扫描）
	convVarTypes    map[string]string          // 会话变量名 → dify value_type（YAML 声明为准）
	convRename      map[string]string          // 会话变量名 → 重命名（与 start 参数同名冲突时,保持两边语义）
	envVars         map[string]envVarDecl      // 环境变量（只读 const,refExpr 按需登记）
	envConsts       []envConst                 // 已引用的环境变量 const（顶层 const 声明,去重保序）
	mode            string                     // 最终 @mode（advanced-chat 或 workflow 降级）
	extraEnums      []*cd.EnumDecl             // classify 合成枚举（渲染到顶层）
	// variable-aggregator 结构（collectAggGroups 预扫描填充）:
	aggGroups     map[string][]aggGroupInfo    // 聚合器节点 → 分组（组名 + 候选 selector）
	aggHoistSrc   map[string]aggHoistTarget    // 候选节点 id → 提升目标（分支局部值 → 组变量）
	generalHoists map[string]bool              // 分支局部产值节点被外部引用 → var 提升
	nodeRefs      map[string]map[string]bool   // 节点 id → 引用者 id 集（collectRefs 预扫描;未引用产值 → 裸 ExprStmt）
	branchInside  map[string]bool              // 节点在任一分支结构内（XOR 或非独占,collectScopeFanout 标记）
	branchScopes  map[string]map[string]string // 节点 id → {if 节点 id: 分支 handle}（分支嵌套签名）
	hoistVars     []string                     // 聚合提升组变量名（顶层 `var <name>;` 声明）
	hoistTypes    map[string]string            // 提升节点 id → ncoda 类型串（`var v<id>: <type>;` 声明带类型）
	hoistZero     map[string]cd.Expr           // 提升节点 id → 类型零值表达式（dify runtime 缺省值安全网显式化,`v<id> = <zero>;`）
	extraTypes    []*cd.TypeDecl               // 合成类型声明（parameter-extractor → extract<T> 的 record 类型）
	barrier       string                       // 并行扇出链的收敛点（convertTo 遇之停止;分支转换透传）
	shimExprs     map[string]cd.Expr           // 可逆单表达式 CodeRunner shim → 原始 ncoda 值
	unknownNotes  []string                     // 类型/节点降级归因（L3-b:禁止静默 any/降级）
}

// aggGroupInfo 一个 variable-aggregator 分组（组名 + 候选 selector 列表）。
// hoistVar 非空 = 候选全在 if 分支内（不可见）→ 组值提升为单变量,分支内赋值。
type aggGroupInfo struct {
	name     string  // 组名（"Group1"…）;简单形态为空串
	sels     [][]any // 候选 selector:[nodeID, key, path...]
	hoistVar string  // 提升组变量名（仅分支局部候选时）
}

// aggHoistTarget 一个候选节点的提升目标（候选 var 声明后追加赋值）。
type aggHoistTarget struct {
	Var string // 组变量名
	Sel []any  // 原始 selector（赋值的 ref 表达式）
}

// modelConstRef 返回 llm 的 model const 名（provider/name → MODEL_<i>）。
func (c *converter) modelConstRef(provider, name string) string {
	key := provider + "/" + name
	if i, ok := c.modelBy[key]; ok {
		return c.models[i].ConstName
	}
	cn := fmt.Sprintf("MODEL_%d", len(c.models)+1)
	c.models = append(c.models, modelConst{ConstName: cn, Model: key})
	if c.modelBy == nil {
		c.modelBy = map[string]int{}
	}
	c.modelBy[key] = len(c.models) - 1
	return cn
}

// startParams 生成 main 形参（"string query, int num"）;start 变量的
// default 映射为参数默认值（黄金对照 `string language = "中文"`）。
func (c *converter) startParams() []string {
	var out []string
	for _, v := range asSlice(c.g.start.Data["variables"]) {
		m := asMap(v)
		name := asStr(m["variable"])
		if name == "" {
			continue
		}
		if c.paramSeen == nil {
			c.paramSeen = map[string]bool{}
		}
		c.paramSeen[name] = true
		typ := c.difyTypeToNcoda(asStr(m["type"]))
		p := typ + " " + render.EscapeIdent(name)
		if dv, ok := m["default"]; ok && dv != nil {
			if rd, ok := c.renderDefaultFor(asStr(m["type"]), dv); ok {
				p += " = " + rd
			}
		}
		out = append(out, p)
	}
	return out
}

// renderDefaultFor 按 start 变量类型渲染 default 值。返回 (渲染, 是否
// 渲染)。dify 的 default 常是空串（= 无默认）;file 类型 default 无意义
// → 不渲染默认值（避免 `file doc = ""` 类型错误）。number 的空串默认
// 同样跳过;非空数字串 → int 字面量。
func (c *converter) renderDefaultFor(typ string, v any) (string, bool) {
	switch t := c.difyTypeToNcoda(typ); t {
	case "file":
		return "", false
	case "int", "bool":
		switch x := v.(type) {
		case string:
			if x == "" {
				return "", false
			}
			if t == "int" {
				if iv, err := strconv.Atoi(x); err == nil {
					return strconv.Itoa(iv), true
				}
			}
			return x, true // bool "true"/"false" 字符串 → 裸值
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64), true
		case bool:
			if x {
				return "true", true
			}
			return "false", true
		}
		return "", false
	default: // string 族
		if s, ok := v.(string); ok && s == "" {
			return "", false
		}
		return render.RenderNCodaExpr(&cd.LiteralExpr{Value: v}), true
	}
}

// noteUnknown 记录一次 UNKNOWN 降级归因（产物尾部 // UNKNOWN: 注释块按
// 原因聚合）。open 字典 any（map<string,any> 合法兜底）不走 note。
func (c *converter) noteUnknown(reason string) {
	c.unknownNotes = append(c.unknownNotes, reason)
}

// difyTypeToNcoda dify start 表单类型 → ncoda 类型。
// 未知类型降级 string 并记录 UNKNOWN 归因（L3-b:禁止静默降级——产物尾部
// // UNKNOWN: 注释块按原因聚合,与 coze-biz 反编译同契约）。
func (c *converter) difyTypeToNcoda(t string) string {
	switch t {
	case "string":
		return "string"
	case "number":
		return "int"
	case "file":
		return "file"
	case "file-list":
		return "array<file>"
	case "select", "text-input", "paragraph", "email", "phone", "url":
		return "string"
	case "boolean":
		return "bool"
	case "list", "array":
		return "array<string>"
	default:
		c.noteUnknown("unknown-start-type:" + t)
		return "string"
	}
}

// convert 从 start 沿边生成语句序列（线性链;if-else 分支递归;同端口
// 多后继 = 并行扇出 → parallel { ... } 透明屏障,收敛点之后继续）。
func (c *converter) convert(id string) []cd.Stmt {
	return c.convertTo(id)
}

// convertTo 转换一个节点链,遇 c.barrier（并行扇出收敛点）停止。barrier
// 自身由外层继续转换,避免共享 end/answer/聚合器重复转换。barrier 是
// converter 字段而非参数——if-else/loop 分支转换（branchStmts）也必须
// 透传收敛边界,否则并行分支内的节点会越过收敛点重复转换。
func (c *converter) convertTo(id string) []cd.Stmt {
	var out []cd.Stmt
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		if c.barrier != "" && id == c.barrier {
			return // 收敛点:由外层继续
		}
		seen[id] = true
		n := c.g.nodes[id]
		if n == nil {
			return
		}
		c.path = append(c.path, id)
		defer func() { c.path = c.path[:len(c.path)-1] }()
		saveCur := c.curNodeID
		c.curNodeID = id
		defer func() { c.curNodeID = saveCur }()
		if isValueProducer(n.Type) {
			if c.declared == nil {
				c.declared = map[string]bool{}
			}
			c.declared[varName(id)] = true
		}
		if c.inIterationBody {
			// iteration 体边界:answer/end 属于外层流程,跳过（不产出 return）。
			if n.Type == "answer" || n.Type == "end" {
				return
			}
		}
		switch n.Type {
		case "start", "loop-start", "iteration-start":
			// 参数由入口收集;入口节点透传;后继续走
		case "note":
			// dify 画布注释节点（data.type 空串归一化）:无执行语义,跳过
		case "llm":
			out = append(out, c.convertLLM(n)...)
		case "if-else":
			out = append(out, c.convertIfElse(n)...)
		case "code":
			if _, generated := c.llmEnvelopeAdapterProducer(n); !generated {
				out = append(out, c.convertCode(n)...)
			}
		case "tool":
			out = append(out, c.convertTool(n)...)
		case "loop":
			out = append(out, c.convertLoop(n)...)
			return // 循环节点终结点:after-loop 延续在 convertLoop 内部
		case "iteration":
			out = append(out, c.convertIteration(n)...)
			return // 同上（iteration 的 source 后继 = after-loop,内部处理）
		case "assigner":
			out = append(out, c.convertAssigner(n)...)
		case "template-transform":
			out = append(out, c.convertTemplateTransform(n)...)
		case "list-operator":
			out = append(out, c.convertListOp(n)...)
		case "http-request":
			out = append(out, c.convertHTTP(n)...)
		case "knowledge-retrieval":
			out = append(out, c.convertKnowledge(n)...)
		case "document-extractor":
			out = append(out, c.convertDocumentExtractor(n)...)
		case "variable-aggregator":
			out = append(out, c.convertAggregator(n)...)
		case "parameter-extractor":
			out = append(out, c.convertParameterExtractor(n)...)
		case "agent":
			out = append(out, c.convertAgent(n)...)
		case "human-input":
			out = append(out, c.convertHumanInput(n)...)
		case "question-classifier":
			out = append(out, c.convertQuestionClassifier(n)...)
		case "answer":
			// convertAnswer 决定单条 answer 是 output 还是 return。
			// 有后继时这里必须继续走控制流；无后继时 walk 自然停止。
			out = append(out, c.convertAnswer(n)...)
		case "end":
			out = append(out, c.convertEnd(n)...)
			return
		default:
			c.noteUnknown("unknown-node:" + n.Type)
			out = append(out, &cd.Comment{Text: fmt.Sprintf("UNKNOWN dify node %q (%s) — not yet covered", n.ID, n.Type)})
			return
		}
		// 泛化 var 提升:分支局部产值节点的 let 声明 → 赋值（prelude 已声明
		// `var vC;`）。分支内赋值,外部引用解析到 vC（可选分支未执行 = null）。
		if c.generalHoists[id] {
			for i := range out {
				if vd, ok := out[i].(*cd.VarDecl); ok && vd.Name == varName(id) && vd.Init != nil {
					out[i] = &cd.AssignStmt{Target: vd.Name, Value: vd.Init}
				}
			}
		}
		// 聚合提升:候选节点（分支局部值）声明后,把值写进组变量。赋值紧跟
		// VarDecl,与声明同作用域（if 分支内赋值 → 分支互斥写,汇聚后可见）。
		if h, ok := c.aggHoistSrc[id]; ok && len(h.Sel) >= 2 {
			out = append(out, &cd.AssignStmt{Target: h.Var, Value: c.refExpr(asStr(h.Sel[0]), asStr(h.Sel[1]), pathOf(h.Sel[2:]))})
		}
		// 丢弃:产值节点输出未被任何下游引用（nodeRefs 无引用者）且未被提升
		// → 裸表达式语句（保留副作用调用,不声明变量）。否则前端 UNUSED_VAR
		// Warning 升级 ANALYSIS_ERROR（03 实证:vllm_en 声明未使用——false 分支
		// llm 输出未被 answer 模板引用）。提升/聚合候选已有变量语义,不适用。
		if c.nodeRefs != nil && !c.generalHoists[id] && len(c.nodeRefs[id]) == 0 {
			if _, isAgg := c.aggHoistSrc[id]; !isAgg {
				for i := range out {
					if vd, ok := out[i].(*cd.VarDecl); ok && vd.Name == varName(id) && vd.Init != nil {
						out[i] = &cd.ExprStmt{Expr: vd.Init}
					}
				}
			}
		}
		succs := c.g.succ(id, "source")
		if len(succs) <= 1 {
			for _, t := range succs {
				walk(t)
			}
			return
		}
		// 并行扇出（dify 同 handle 多出边 = 分支并行执行,全部完成才继续）:
		// branchStmts 内 parallel 块 + 收敛点之后转换一次。
		out = append(out, c.branchStmts(succs)...)
	}
	walk(id)
	return out
}

// convergence 找扇出链的共同收敛节点（从各后继可达,且在其 walk 终止点
// 之前——即 end/answer/后向汇入）。全链无共同收敛 → ""（dangling）。
// 实现:对每个后继 BFS 到终止点（answer/end/无后继）,取交集,返回最先
// 遇到者（按第一个后继的 walk 序）。
func (c *converter) convergence(succs []string) string {
	var common map[string]bool
	for _, t := range succs {
		reach := c.reachable(t, map[string]bool{})
		if common == nil {
			common = reach
		} else {
			for id := range common {
				if !reach[id] {
					delete(common, id)
				}
			}
		}
		if len(common) == 0 {
			return ""
		}
	}
	// 按第一后继的 walk 序选第一个公共节点:优先全图拓扑序（g.order,
	// 从 start 沿 source 边 BFS）;iteration 容器内部节点不在 g.order
	// （容器图经 start_node_id 锚定,外部无边可达）,回退到从第一后继
	// 出发的局部可达序（与 reachable 同遍历,记录访问顺序）。
	for _, id := range c.g.order {
		if common[id] {
			return id
		}
	}
	for _, id := range c.reachOrder(succs[0]) {
		if common[id] {
			return id
		}
	}
	return ""
}

// reachOrder 返回从 id 出发沿边可达的访问顺序（与 reachable 相同的
// 遍历规则:source 线性边 + if-else 分支边;answer/end 为终点）。
func (c *converter) reachOrder(id string) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
		n := c.g.nodes[id]
		if n == nil {
			return
		}
		switch n.Type {
		case "answer", "end":
			return
		}
		for _, t := range c.g.succ(id, "source") {
			walk(t)
		}
		if n.Type == "if-else" {
			for _, cs := range c.ifCases(n) {
				for _, t := range c.g.succ(id, cs.handle) {
					walk(t)
				}
			}
			for _, t := range c.g.succ(id, "false") {
				walk(t)
			}
		}
		if n.Type == "question-classifier" {
			for _, rc := range asSlice(n.Data["classes"]) {
				cm := asMap(rc)
				for _, t := range c.g.succ(id, asStr(cm["id"])) {
					walk(t)
				}
			}
		}
	}
	walk(id)
	return out
}

// reachable 从 id 沿边可达且未被终止的节点集（answer/end 为终点）。
// 除 source 线性边外,还穿透 if-else 的 true/false 分支边——并行扇出
// 的收敛点判定必须能看到分支内的节点（dual_switch: 分支内节点 → 聚合器）。
func (c *converter) reachable(id string, acc map[string]bool) map[string]bool {
	if acc[id] {
		return acc
	}
	acc[id] = true
	n := c.g.nodes[id]
	if n == nil {
		return acc
	}
	switch n.Type {
	case "answer", "end":
		return acc
	}
	for _, t := range c.g.succ(id, "source") {
		c.reachable(t, acc)
	}
	switch n.Type {
	case "if-else":
		for _, t := range c.g.succ(id, "true") {
			c.reachable(t, acc)
		}
		for _, t := range c.g.succ(id, "false") {
			c.reachable(t, acc)
		}
	case "question-classifier":
		for _, rc := range asSlice(n.Data["classes"]) {
			cm := asMap(rc)
			for _, t := range c.g.succ(id, asStr(cm["id"])) {
				c.reachable(t, acc)
			}
		}
	}
	return acc
}

// reachableHas 报告 aid 是否在从 id 出发的可达集内（候选是否在聚合器上游）。
func (c *converter) reachableHas(id, aid string) bool {
	return c.reachable(id, map[string]bool{})[aid]
}

// convertLLM: let v<id> = llm(MODEL_<i>, {"messages": [...], "temperature": t, "max_tokens": n});
func (c *converter) convertLLM(n *nodeInfo) []cd.Stmt {
	data := n.Data
	model := asMap(data["model"])
	cn := c.modelConstRef(asStr(model["provider"]), asStr(model["name"]))
	messages := &cd.ListExpr{}
	for _, pm := range asSlice(data["prompt_template"]) {
		pm := asMap(pm)
		messages.Items = append(messages.Items, &cd.DictExpr{
			Keys:   []string{"role", "content"},
			Values: []cd.Expr{&cd.LiteralExpr{Value: asStr(pm["role"])}, c.renderTemplate(asStr(pm["text"]))},
		})
	}
	call := &cd.CallExpr{Callee: "llm", Args: []cd.Expr{&cd.IdentExpr{Name: cn}}}
	if schema := asMap(asMap(data["structured_output"])["schema_def"]); len(schema) > 0 {
		if value, ok := asMap(schema["properties"])["value"]; ok {
			name := "LLM" + sanitizeID(n.ID) + "Payload"
			typeMark, enumMark := len(c.extraTypes), len(c.extraEnums)
			if typ := c.difySchemaType(value, name); typ != "" {
				call.TypeArgs = []string{typ}
			} else {
				c.extraTypes = c.extraTypes[:typeMark]
				c.extraEnums = c.extraEnums[:enumMark]
			}
		}
	}
	cfg := &cd.DictExpr{}
	if len(messages.Items) > 0 {
		dictPut(cfg, "messages", messages)
	}
	// Dify carries LLM completion params in model.completion_params on the
	// current export shape; some fixtures still use the legacy top-level
	// fields. Both are the same platform config surface, so normalize them
	// into the one ncoda config dictionary.
	if v := llmCompletionParam(data, "temperature"); v != nil {
		dictPut(cfg, "temperature", &cd.LiteralExpr{Value: asFloat(v)})
	}
	if v := llmCompletionParam(data, "max_tokens"); v != nil {
		dictPut(cfg, "max_tokens", &cd.LiteralExpr{Value: int(asFloat(v))})
	}
	call.Args = append(call.Args, cfg)
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: call}}
}

// llmCompletionParam reads the Dify LLM config parameter from its current
// model.completion_params shape, falling back only to the known legacy
// top-level shape. It returns nil when the optional key is absent.
func llmCompletionParam(data map[string]any, key string) any {
	if v := asMap(asMap(data["model"])["completion_params"])[key]; v != nil {
		return v
	}
	return data[key]
}

func (c *converter) difySchemaType(raw any, name string) string {
	m := asMap(raw)
	if variants := asSlice(m["anyOf"]); len(variants) > 0 {
		var value any
		nulls := 0
		for _, variant := range variants {
			if asStr(asMap(variant)["type"]) == "null" {
				nulls++
				continue
			}
			if value != nil {
				c.noteUnknown("unknown-llm-schema:unsupported-anyOf")
				return ""
			}
			value = variant
		}
		if nulls != 1 || value == nil {
			c.noteUnknown("unknown-llm-schema:malformed-nullable")
			return ""
		}
		inner := c.difySchemaType(value, name)
		if inner == "" {
			return ""
		}
		return strings.TrimSuffix(inner, "?") + "?"
	}
	switch asStr(m["type"]) {
	case "string":
		if vals := asSlice(m["enum"]); len(vals) > 0 {
			members := make([]string, 0, len(vals))
			for _, value := range vals {
				member := asStr(value)
				if member == "" || !isDifySchemaIdent(member) {
					c.noteUnknown("unknown-llm-schema:enum-wire-value")
					return ""
				}
				members = append(members, member)
			}
			c.extraEnums = append(c.extraEnums, &cd.EnumDecl{Name: name, Members: members})
			return name
		}
		return "string"
	case "boolean":
		return "bool"
	case "integer":
		return "int"
	case "number":
		return "float"
	case "array":
		item := c.difySchemaType(m["items"], name+"Item")
		if item == "" {
			return ""
		}
		return "array<" + item + ">"
	case "object":
		props := asMap(m["properties"])
		if len(props) == 0 {
			if value := asMap(m["additionalProperties"]); len(value) > 0 {
				vt := c.difySchemaType(value, name+"Value")
				if vt == "" {
					return ""
				}
				return "map<string, " + vt + ">"
			}
			// 空 object → 开放对象 object（LLM-OPEN-OBJECT.md §3.4）：
			// 保留审计归因，产物是合法可编译的 object（而非 "" 退化 string）。
			c.noteUnknown("unknown-llm-schema:open-object")
			return "object"
		}
		fields := asSlice(m["required"])
		required := map[string]bool{}
		for _, f := range fields {
			required[asStr(f)] = true
		}
		td := &cd.TypeDecl{Name: name}
		for _, field := range sortedKeys(props) {
			ft := c.difySchemaType(props[field], name+sanitizeID(field))
			if ft == "" {
				return ""
			}
			if !required[field] && !strings.HasSuffix(ft, "?") {
				ft += "?"
			}
			td.Fields = append(td.Fields, cd.TypeField{Type: ft, Name: render.EscapeIdent(field)})
		}
		c.addExtraType(td)
		return name
	default:
		c.noteUnknown("unknown-llm-schema:type-" + asStr(m["type"]))
		return ""
	}
}

func isDifySchemaIdent(value string) bool {
	for i, r := range value {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
		} else if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return value != ""
}

// ifCase 一个 if-else 分支（handle = 出边端口;cond nil = 无条件兜底）。
type ifCase struct {
	handle string
	cond   cd.Expr
}

// convertIfElse: dify 多 case → ncoda if / else-if / else 链。每个 case
// 一个出边端口（新版 = case_id,老版 = true/false）;显式 false 端口 =
// 全部 case 不满足的兜底。分支内同端口多后继 = 并行扇出（branchStmts）。
// ifConv 计算 if-else 的共同收敛点（分支入口 reachable 交集,同 convertIfElse
// 内部逻辑）。collectGeneralHoists 复用:fan 收敛点是终端（answer/单值 end）
// → 分支内节点不提升（分支内各自 return,answer 引用走 pathTerminal 解析本
// 路径 producer,无需跨分支提升变量）。
func (c *converter) ifConv(n *nodeInfo) string {
	var branchEnts []string
	for _, cs := range c.ifCases(n) {
		branchEnts = append(branchEnts, c.g.succ(n.ID, cs.handle)...)
	}
	branchEnts = append(branchEnts, c.g.succ(n.ID, "false")...)
	if len(branchEnts) < 2 {
		return ""
	}
	return c.convergence(branchEnts)
}

// isTerminalNode 节点是否是流程终点（answer 恒真;end 仅单输出——多输出 end
// 是 dict 精确引用,pathTerminal 重定向会错,不能按终端收敛不提升）。
func (c *converter) isTerminalNode(id string) bool {
	n := c.g.nodes[id]
	if n == nil {
		return false
	}
	if n.Type == "answer" {
		return true
	}
	if n.Type == "end" {
		return len(asSlice(n.Data["outputs"])) == 1
	}
	return false
}

func (c *converter) convertIfElse(n *nodeInfo) []cd.Stmt {
	cases := c.ifCases(n)
	// 多分支共同汇入点（aggregator/end/answer）→ 分支转换在收敛点前停,
	// if-else 之后转换一次——否则共享聚合器/终点被每个分支重复转换
	// （let 遮蔽在分支内,外层引用未定义;与 parallel branchStmts 同构）。
	// 仅单分支（无 else）时线性走到底（原行为）,不设 barrier。
	var branchEnts []string
	for _, cs := range cases {
		branchEnts = append(branchEnts, c.g.succ(n.ID, cs.handle)...)
	}
	branchEnts = append(branchEnts, c.g.succ(n.ID, "false")...)
	conv := ""
	if len(branchEnts) >= 2 {
		conv = c.convergence(branchEnts)
	}
	save := c.barrier
	// conv 非空 → 分支收敛点（分支转换停在此,共享聚合器终点之后转一次）。
	// conv 是终端（answer/end）→ 不设 barrier,分支内自然走到终端各自 return
	// （golden 03/10 形态:if-else 收敛到 answer → 分支内 let llm + return）。
	// conv 为空 → 保留外层 barrier（并行扇出收敛点,如 answer）:分支走到外层
	// 收敛点即停、不重复转换（test_complex_branch 实证:if-else false 分支 →
	// answer 恰是外层 parallel 收敛点,若被内层覆盖成空,answer 在分支内重复
	// 转换 → 分支读兄弟分支提升变量 → P5 读写竞争 E1036）。
	isTerminal := conv != "" && c.isTerminalNode(conv)
	if conv != "" && !isTerminal {
		c.barrier = conv
	}
	snap := c.snapshotDeclared()
	var head, tail *cd.If
	for _, cs := range cases {
		blk := &cd.Block{Stmts: c.branchStmts(c.g.succ(n.ID, cs.handle))}
		c.restoreDeclared(snap)
		if cs.cond == nil {
			// 无条件 case（dify 兜底,case_id "true" 且无 conditions）。
			if tail != nil {
				tail.Else = blk
			}
			continue
		}
		it := &cd.If{Cond: cs.cond, Then: blk}
		if head == nil {
			head, tail = it, it
		} else {
			tail.Else = &cd.Block{Stmts: []cd.Stmt{it}}
			tail = it
		}
	}
	// false 端口 = 全部条件不满足的最终兜底（老版端口;新版无 case 覆盖时
	// 也存在）。无条件 case 已吃掉该路径时 succ 为空,自然跳过。
	falseB := c.branchStmts(c.g.succ(n.ID, "false"))
	c.restoreDeclared(snap)
	c.barrier = save
	if len(falseB) > 0 {
		if tail != nil {
			tail.Else = &cd.Block{Stmts: falseB}
		} else if len(cases) == 0 {
			head = &cd.If{Cond: &cd.LiteralExpr{Value: true}, Then: &cd.Block{Stmts: falseB}}
		}
	}
	var out []cd.Stmt
	if head != nil {
		out = append(out, head)
	}
	if conv != "" && conv != n.ID && !isTerminal {
		out = append(out, c.convert(conv)...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// convertChain 转换一组后继节点（线性链,忽略分支内部细节）。
func (c *converter) convertChain(ids []string) []cd.Stmt {
	return c.branchStmts(ids)
}

// branchStmts 转换一组后继:单后继线性走;多后继 = 并行扇出 → parallel
// 透明屏障（块内变量透出,P5）,共同收敛点（end/answer）之后转换一次。
func (c *converter) branchStmts(succs []string) []cd.Stmt {
	if len(succs) <= 1 {
		var out []cd.Stmt
		for _, id := range succs {
			out = append(out, c.convert(id)...)
		}
		return out
	}
	conv := c.convergence(succs)
	ps := &cd.ParallelStmtStmt{}
	// 收敛边界透传:分支转换（含 if-else 递归）必须在 conv 前停止——否则
	// 共享聚合器/end 会被每个分支重复转换（dual_switch 实证 4× UNKNOWN）。
	save := c.barrier
	c.barrier = conv
	for _, t := range succs {
		ps.Blocks = append(ps.Blocks, &cd.Block{Stmts: c.convertTo(t)})
	}
	c.barrier = save
	out := []cd.Stmt{ps}
	if conv != "" {
		out = append(out, c.convert(conv)...)
	}
	return out
}

// ifCases 提取 if-else 全部分支（新版 cases[] / 旧版 conditions）。
// 新版每个 case 一个出边端口（case_id）;case 无 conditions（case_id 为
// "true" 的兜底形态）→ 无条件分支（cond nil）。旧版 conditions 结构 →
// 单 case（handle "true"）。
func (c *converter) ifCases(n *nodeInfo) []ifCase {
	data := n.Data
	cases := asSlice(data["cases"])
	if len(cases) == 0 {
		if old := asMap(data["conditions"]); len(old) > 0 {
			return []ifCase{{handle: "true", cond: c.condGroup(asStr(old["logical_operator"]), asSlice(old["conditions"]))}}
		}
		return []ifCase{{handle: "true", cond: &cd.LiteralExpr{Value: true}}}
	}
	var out []ifCase
	for _, rc := range cases {
		cm := asMap(rc)
		handle := asStr(cm["case_id"])
		if handle == "" {
			handle = "true"
		}
		conds := asSlice(cm["conditions"])
		if len(conds) == 0 {
			// 无条件兜底 case（dify 导出 case_id "true" 空条件 = else）。
			out = append(out, ifCase{handle: handle})
			continue
		}
		out = append(out, ifCase{handle: handle, cond: c.condGroup(asStr(cm["logical_operator"]), conds)})
	}
	return out
}

// condGroup 一组条件（and/or 组合）。
func (c *converter) condGroup(op string, conds []any) cd.Expr {
	var e cd.Expr
	for i, rc := range conds {
		rc := asMap(rc)
		sel := asSlice(rc["variable_selector"])
		lhs := c.selectorExpr(sel)
		cmp := c.compareExpr(rc, lhs)
		if i == 0 {
			e = cmp
			continue
		}
		join := "&&"
		if op == "or" {
			join = "||"
		}
		e = &cd.BinaryExpr{Op: join, Left: e, Right: cmp}
	}
	if e == nil {
		return &cd.LiteralExpr{Value: true}
	}
	return e
}

// switchConv question-classifier 分支汇聚点（全部 class 分支入口的交集
// 收敛,同 ifConv 语义;单分支/无分支 → ""）。
func (c *converter) switchConv(n *nodeInfo) string {
	var branchEnts []string
	for _, rc := range asSlice(n.Data["classes"]) {
		cm := asMap(rc)
		branchEnts = append(branchEnts, c.g.succ(n.ID, asStr(cm["id"]))...)
	}
	if len(branchEnts) < 2 {
		return ""
	}
	return c.convergence(branchEnts)
}

// isArrayVarType dify varType 是否数组（"array[file]"/"array[string]"...）。
func isArrayVarType(t string) bool {
	return strings.HasPrefix(t, "array")
}

// compareExpr 单个条件的比较表达式（顶层条件与 sub_variable_condition
// 子条件共用;lhs 由调用方构造——顶层 = selectorExpr,sub = item[.<key>]）。
// 操作符覆盖 dify if-else 全集:equals/is/=/not equals/is not/≠/contains/
// not contains/greater than/less than/empty/not empty/in/not in,以及
// contains/not contains/all of + sub_variable_condition 数组元素谓词
// （len(filter(x, item -> sub)) > 0 / == 0 / == len(x)）。
func (c *converter) compareExpr(rc map[string]any, lhs cd.Expr) cd.Expr {
	varType := asStr(rc["varType"])
	// sub_variable_condition 容器（dify 新版数组元素条件）。
	if sub, ok := rc["sub_variable_condition"].(map[string]any); ok {
		lams := c.subCondLambdas(sub)
		if len(lams) == 0 {
			return &cd.LiteralExpr{Value: true}
		}
		// and 组合 → 嵌套 filter（每条件单谓词,正编译 listFilterItem 只认
		// 单条件;&& 链撞 E1034 filter 不可安全降级——图文智控链实证）。
		// or 组合无法嵌套（filter 是 AND 语义）→ len(filter)>0 || ... 合成。
		f := &cd.CallExpr{Callee: "filter", Args: []cd.Expr{lhs, lams[0]}}
		if asStr(sub["logical_operator"]) == "and" {
			for i := 1; i < len(lams); i++ {
				f = &cd.CallExpr{Callee: "filter", Args: []cd.Expr{f, lams[i]}}
			}
		} else {
			var body cd.Expr
			for i, lam := range lams {
				lamFn := &cd.CallExpr{Callee: "filter", Args: []cd.Expr{lhs, lam}}
				lenF := &cd.CallExpr{Callee: "len", Args: []cd.Expr{lamFn}}
				gt0 := &cd.BinaryExpr{Op: ">", Left: lenF, Right: &cd.LiteralExpr{Value: 0}}
				if i == 0 {
					body = gt0
				} else {
					body = &cd.BinaryExpr{Op: "||", Left: body, Right: gt0}
				}
			}
			return body
		}
		lenF := &cd.CallExpr{Callee: "len", Args: []cd.Expr{f}}
		switch asStr(rc["comparison_operator"]) {
		case "contains", "not contains":
			op := ">"
			if asStr(rc["comparison_operator"]) == "not contains" {
				op = "=="
			}
			return &cd.BinaryExpr{Op: op, Left: lenF, Right: &cd.LiteralExpr{Value: 0}}
		case "all of":
			return &cd.BinaryExpr{Op: "==", Left: lenF,
				Right: &cd.CallExpr{Callee: "len", Args: []cd.Expr{lhs}}}
		}
		// 其他操作符带 sub → 无法表达,记 UNKNOWN 语义降级为恒真（调用方
		// 已入覆盖清单路径）。
		return &cd.LiteralExpr{Value: true}
	}
	rhs := &cd.LiteralExpr{Value: renderDefaultFor2(varType, rc["value"])}
	switch asStr(rc["comparison_operator"]) {
	case "equals", "is", "=":
		return &cd.BinaryExpr{Op: "==", Left: lhs, Right: rhs}
	case "not equals", "is not", "≠":
		return &cd.BinaryExpr{Op: "!=", Left: lhs, Right: rhs}
	case "contains":
		return &cd.CallExpr{Callee: "contains", Args: []cd.Expr{lhs, rhs}}
	case "not contains":
		return &cd.CallExpr{Callee: "!contains", Args: []cd.Expr{lhs, rhs}}
	case "greater than":
		return &cd.BinaryExpr{Op: ">", Left: lhs, Right: rhs}
	case "less than":
		return &cd.BinaryExpr{Op: "<", Left: lhs, Right: rhs}
	case "greater than or equal", "not less than":
		return &cd.BinaryExpr{Op: ">=", Left: lhs, Right: rhs}
	case "less than or equal", "not greater than":
		return &cd.BinaryExpr{Op: "<=", Left: lhs, Right: rhs}
	case "empty", "not empty":
		op := "=="
		if asStr(rc["comparison_operator"]) == "not empty" {
			op = "!="
		}
		if isArrayVarType(varType) {
			// 数组判空 = 长度;string 判空 = 空串。
			return &cd.BinaryExpr{Op: op, Left: &cd.CallExpr{Callee: "len", Args: []cd.Expr{lhs}},
				Right: &cd.LiteralExpr{Value: 0}}
		}
		return &cd.BinaryExpr{Op: op, Left: lhs, Right: &cd.LiteralExpr{Value: ""}}
	case "is empty":
		return &cd.BinaryExpr{Op: "==", Left: lhs, Right: &cd.LiteralExpr{Value: ""}}
	case "is not empty":
		return &cd.BinaryExpr{Op: "!=", Left: lhs, Right: &cd.LiteralExpr{Value: ""}}
	case "in", "not in":
		vals := make([]cd.Expr, 0)
		for _, it := range asSlice(rc["value"]) {
			vals = append(vals, &cd.LiteralExpr{Value: renderDefaultFor2("", it)})
		}
		if asStr(rc["comparison_operator"]) == "not in" {
			// ncoda 无 not in:单元素 → !=;多元素 → && 链（德摩根）。
			if len(vals) == 1 {
				return &cd.BinaryExpr{Op: "!=", Left: lhs, Right: vals[0]}
			}
			var e cd.Expr
			for i, v := range vals {
				cmp := &cd.BinaryExpr{Op: "!=", Left: lhs, Right: v}
				if i == 0 {
					e = cmp
					continue
				}
				e = &cd.BinaryExpr{Op: "&&", Left: e, Right: cmp}
			}
			return e
		}
		if len(vals) == 1 {
			return &cd.BinaryExpr{Op: "in", Left: lhs, Right: &cd.ListExpr{Items: vals}}
		}
		return &cd.BinaryExpr{Op: "in", Left: lhs, Right: &cd.ListExpr{Items: vals}}
	default:
		return &cd.BinaryExpr{Op: "==", Left: lhs, Right: rhs}
	}
}

// subCondLambdas 把 sub_variable_condition 条件组渲染为 filter 谓词列表
// （每条件一个单谓词 lambda:item.<key> <op> <value>）。and 组合由调用方
// 嵌套 filter（正编译 listFilterItem 只认单条件谓词,&& 链撞 1034）;or
// 组合由调用方合成 len(filter)>0 || len(filter)>0。
func (c *converter) subCondLambdas(sub map[string]any) []cd.Expr {
	var out []cd.Expr
	for _, rc := range asSlice(sub["conditions"]) {
		rc := asMap(rc)
		key := asStr(rc["key"])
		var lhs cd.Expr = &cd.IdentExpr{Name: "item"}
		if key != "" {
			lhs = &cd.MemberExpr{Obj: lhs, Field: key}
		}
		// contains 子条件必须方法形态 item.<key>.contains(<val>)——前端
		// IsNativeContains 只认 FieldAccessExpr 调用;裸函数 contains(x, v)
		// 在 filter 谓词里不匹配（1034）。
		cmp := c.compareExpr(rc, lhs)
		if op := asStr(rc["comparison_operator"]); op == "contains" {
			cmp = &cd.CallExpr{Callee: "contains", Receiver: lhs,
				Args: []cd.Expr{&cd.LiteralExpr{Value: renderDefaultFor2("", rc["value"])}}}
		}
		if cmp == nil {
			continue
		}
		out = append(out, &cd.LambdaExpr{Params: []string{"item"}, Body: cmp})
	}
	return out
}

// convertAnswer: dify answer 是流式消息发射。有控制流后继时翻译为
// output(<值>)并继续;无后继时才是 return(<值>)。链式 answer 因此保留
// 全部中间消息和其上游产值绑定，而不是把首个 answer 当流程终点。
func (c *converter) convertAnswer(n *nodeInfo) []cd.Stmt {
	value := c.renderAnswerTemplate(asStr(n.Data["answer"]))
	if len(c.g.succ(n.ID, "source")) > 0 {
		return []cd.Stmt{&cd.OutputStmt{Value: value}}
	}
	return []cd.Stmt{&cd.Return{Value: value}}
}

// convertEnd: end.outputs → return（单 output 直返;多 output 组 dict）。
// value_selector 引用按当前作用域解析（resolveRefNode）——引用未执行
// 的兄弟路径 producer → 空串（dify 未运行节点输出为空,忠实语义）。
func (c *converter) convertEnd(n *nodeInfo) []cd.Stmt {
	outs := asSlice(n.Data["outputs"])
	if len(outs) == 0 {
		// dify end 空输出 = 流程结束无返回值;ncoda 多出口要求各出口类型
		// 一致——空输出以 "" 落型（与同流程字符串返回出口同型）。若整个
		// 流程都是空输出,function 返回类型隐式为 string。
		return []cd.Stmt{&cd.Return{Value: &cd.LiteralExpr{Value: ""}}}
	}
	if len(outs) == 1 {
		o := asMap(outs[0])
		if sel := asSlice(o["value_selector"]); len(sel) > 0 {
			return []cd.Stmt{&cd.Return{Value: c.endValueExpr(sel)}}
		}
		return []cd.Stmt{&cd.Return{Value: &cd.LiteralExpr{Value: o["value"]}}}
	}
	d := &cd.DictExpr{}
	seen := map[string]bool{}
	for _, ro := range outs {
		o := asMap(ro)
		name := asStr(o["variable"])
		if name == "" {
			name = asStr(o["value_selector"])
		}
		// 同名输出去重（语料病态:同一 end 3× variable:text）:平台 dict 亦
		// 不能有重复键,保留首个（其余丢弃,记覆盖清单）。
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		dictPut(d, name, c.endValueExpr(asSlice(o["value_selector"])))
	}
	return []cd.Stmt{&cd.Return{Value: d}}
}

// endValueExpr end 输出 value_selector → 表达式（作用域解析,同 answer）。
func (c *converter) endValueExpr(sel []any) cd.Expr {
	if len(sel) < 2 {
		return &cd.LiteralExpr{Value: nil}
	}
	return c.refExprResolved(asStr(sel[0]), asStr(sel[1]), pathOf(sel[2:]))
}

// pathOf value_selector 尾段 → 路径段。
func pathOf(rest []any) []string {
	var out []string
	for _, p := range rest {
		out = append(out, asStr(p))
	}
	return out
}

// 占位（渐进实现）:ncoda 数值字面量 float 需要规范化格式。
var _ = strconv.FormatFloat

// ── code 节点（dify Code → ncoda foreign code）──

// convertCode: let v<id> = foreign code <lang>(<typed inputs>) -> { <outputs> } { source `...` };
// dify code 节点 data: {code, language, variables[], outputs{name:{type}}, ...}。
// 输入绑定取自 variables（value_selector → 表达式,value_type → ncoda 类型）;
// 输出契约 = outputs 命名 record（确定性排序）。单输出也用 record 形态——
// dify 引用恒为 node.<output> 键式访问,直出（Direct）会丢输出名。
func (c *converter) convertCode(n *nodeInfo) []cd.Stmt {
	data := n.Data
	lang := asStr(data["code_language"])
	if lang == "" {
		lang = asStr(data["language"]) // 老导出字段名（兼容）
	}
	if lang == "" {
		lang = "python3" // dify 缺省 python3
	}
	fc := &cd.ForeignCodeExpr{Language: lang}
	for _, v := range asSlice(data["variables"]) {
		m := asMap(v)
		name := asStr(m["variable"])
		if name == "" {
			continue
		}
		typeName := c.difyCodeTypeToNcoda(asStr(m["value_type"]))
		selector := asSlice(m["value_selector"])
		if c.isOptionalLLMMetadataSelector(selector) && !strings.HasSuffix(typeName, "?") {
			typeName += "?"
		}
		fc.Inputs = append(fc.Inputs, cd.CodeInputBinding{
			Type: typeName,
			Name: name,
			Expr: c.selectorExpr(selector),
		})
	}
	for _, k := range sortedKeys(asMap(data["outputs"])) {
		om := asMap(asMap(data["outputs"])[k])
		fc.Output = append(fc.Output, cd.ForeignCodeOutputField{
			Name: k,
			Type: c.difyCodeTypeToNcoda(asStr(om["type"])),
		})
	}
	// Compiler-owned Python shims have a closed reverse contract. Reuse the
	// shared reverse engine instead of treating them as user foreign code.
	bindings := make(map[string]cd.Expr, len(fc.Inputs))
	for _, in := range fc.Inputs {
		bindings[in.Name] = in.Expr
	}
	if rev, ok := shim.New(nil, nil).ReverseShim(asStr(data["code"]), nil, bindings); ok {
		if _, isRecord := rev.(*cd.DictExpr); !isRecord {
			if c.shimExprs == nil {
				c.shimExprs = map[string]cd.Expr{}
			}
			c.shimExprs[n.ID] = rev
			return nil
		}
	}
	fc.Source = &cd.TemplateString{Parts: []cd.TemplatePart{{Text: asStr(data["code"])}}}
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: fc}}
}

func (c *converter) llmEnvelopeAdapterProducer(n *nodeInfo) (string, bool) {
	if n == nil || n.Type != "code" || !strings.Contains(asStr(n.Data["code"]), llmprojection.AssemblyMarker) {
		return "", false
	}
	for _, value := range asSlice(n.Data["variables"]) {
		selector := asSlice(asMap(value)["value_selector"])
		if len(selector) < 2 {
			continue
		}
		producer := asStr(selector[0])
		physical := asStr(selector[1])
		if source := c.g.nodes[producer]; source != nil && source.Type == "llm" && (physical == "text" || physical == "structured_output") {
			return producer, true
		}
	}
	return "", false
}

func (c *converter) isOptionalLLMMetadataSelector(selector []any) bool {
	if len(selector) < 2 {
		return false
	}
	producer := c.g.nodes[asStr(selector[0])]
	if producer == nil || producer.Type != "llm" {
		return false
	}
	switch asStr(selector[1]) {
	case "reasoning_content", "usage", "finish_reason":
		return true
	default:
		return false
	}
}

// difyCodeTypeToNcoda dify 类型字符串 → ncoda 类型（code outputs/inputs）。
// number → int（dify number 语义整数为主,float 场景记覆盖清单）;
// object → any（ncoda 无裸 object 基元,记 object-no-schema 归因）;
// array[T] → array<T>;未知 → any（不捏造,记 unknown-datatype 归因）。
// 记录 UNKNOWN（L3-b:禁止静默 any,与 coze-biz 反编译同契约）。
func (c *converter) difyCodeTypeToNcoda(t string) string {
	if strings.HasPrefix(t, "array[") && strings.HasSuffix(t, "]") {
		return "array<" + c.difyCodeTypeToNcoda(t[6:len(t)-1]) + ">"
	}
	switch t {
	case "string", "text":
		return "string"
	case "number", "integer":
		return "int"
	case "boolean", "bool":
		return "bool"
	case "file":
		return "file"
	case "object", "any", "":
		c.noteUnknown("object-no-schema")
		return "any"
	default:
		c.noteUnknown("unknown-datatype:" + t)
		return "any"
	}
}

// ── tool 节点（dify Tool → 限定名工具调用）──

// convertTool: import "dify.<tool_name>"; let v<id> = <tool_name>.<tool_name>({params});
// dify tool 节点 data: {tool_name, tool_parameters{...}, ...}。tool_parameters
// 的模板值 {{#node.key#}} 还原为 ${expr} 插值,普通值按字面量。
func (c *converter) convertTool(n *nodeInfo) []cd.Stmt {
	data := n.Data
	toolName := toolSymbolName(asStr(data["tool_name"]))
	if toolName == "" {
		toolName = toolSymbolName(asStr(data["provider_name"]))
	}
	if toolName == "" {
		return []cd.Stmt{&cd.Comment{Text: fmt.Sprintf("UNKNOWN dify tool node %q (missing tool_name) — not yet covered", n.ID)}}
	}
	c.addImport("dify." + toolName)
	cfg := &cd.DictExpr{}
	var out []cd.Stmt
	for _, k := range sortedKeys(asMap(data["tool_parameters"])) {
		value := c.toolParamExpr(asMap(data["tool_parameters"])[k])
		// Dify tool parameters accept a literal or a direct variable reference
		// at the ncoda tool boundary. A template containing a live reference is
		// still valid ncoda, but must be materialized first so the target sees a
		// normal variable input rather than a complex expression.
		if _, ok := value.(*cd.TemplateString); ok {
			name := c.freshTempName("__dify_param_" + sanitizeID(n.ID) + "_" + sanitizeID(k))
			out = append(out, &cd.VarDecl{Name: name, Init: value})
			value = &cd.IdentExpr{Name: name}
		}
		dictPut(cfg, k, value)
	}
	out = append(out, &cd.VarDecl{Name: varName(n.ID), Init: &cd.CallExpr{
		Callee:   toolName,
		Receiver: &cd.IdentExpr{Name: toolName},
		Args:     []cd.Expr{cfg},
	}})
	return out
}

// toolParamExpr tool 参数值 → ncoda 表达式。dify 参数值常用 "mixed" 信封
// {type, value}:展开取 value（模板插值 / 字面量）——信封本身是平台参数
// 元数据,不是数据。普通值:含 {{#...#}} 模板 → ${expr} 插值;否则字面量。
func (c *converter) toolParamExpr(v any) cd.Expr {
	if m, ok := v.(map[string]any); ok {
		if t := asStr(m["type"]); t != "" {
			if _, hasVal := m["value"]; hasVal {
				v = m["value"]
			}
		}
	}
	if s, ok := v.(string); ok && strings.Contains(s, "{{#") {
		return c.renderTemplate(s)
	}
	if s, ok := v.(string); ok {
		return &cd.LiteralExpr{Value: s}
	}
	return &cd.LiteralExpr{Value: v}
}

// freshTempName returns a deterministic, compiler-generated name that cannot
// collide with another materialized tool parameter in this conversion.
func (c *converter) freshTempName(base string) string {
	if base == "" {
		base = "__dify_param"
	}
	if c.used == nil {
		c.used = map[string]bool{}
	}
	name := base
	for i := 2; c.used[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	c.used[name] = true
	return name
}

// addImport 注册去重 import（渲染时提升到 @mode 之后）。
func (c *converter) addImport(path string) {
	for _, p := range c.imports {
		if p == path {
			return
		}
	}
	c.imports = append(c.imports, path)
}

// snapshotDeclared / restoreDeclared 拷贝/恢复已声明变量集（if 分支隔离:
// then 分支的声明不影响 else 分支的引用判定）。
func (c *converter) snapshotDeclared() map[string]bool {
	snap := make(map[string]bool, len(c.declared))
	for k := range c.declared {
		snap[k] = true
	}
	return snap
}

func (c *converter) restoreDeclared(snap map[string]bool) {
	c.declared = snap
}

// ── loop 节点（dify Loop → ncoda while + 可变状态）──

// convertLoop: dify loop（loop_count + loop_variables + break_conditions）
//
//	var <label> = <init>;              // loop_variables（常量初始值）
//	while ( <继续条件> ) limit <loop_count> {
//	    <body: start_node_id 链>
//	}
//
// 继续条件 = break 条件的否定（break 命中则停;多条件按 logical_operator
// 合取/析取后整体否定——比较符翻转,AND↔OR）。body 内 answer = 流程终止
// （dify answer 语义,忠实）。可变状态由前端 LoopStatePass 校验。
func (c *converter) convertLoop(n *nodeInfo) []cd.Stmt {
	data := n.Data
	var out []cd.Stmt
	for _, lv := range asSlice(data["loop_variables"]) {
		m := asMap(lv)
		label := asStr(m["label"])
		if label == "" {
			continue
		}
		c.registerLoopVar(n.ID, label)
		var init cd.Expr
		if asStr(m["value_type"]) == "variable" {
			if sel := asSlice(m["value"]); len(sel) >= 2 {
				init = c.refExpr(asStr(sel[0]), asStr(sel[1]), pathOf(sel[2:]))
			} else {
				init = &cd.LiteralExpr{Value: nil}
			}
		} else {
			init = &cd.LiteralExpr{Value: renderLoopVarInit(asStr(m["var_type"]), m["value"])}
		}
		out = append(out, &cd.VarDecl{Name: label, Mutable: true, Init: init})
	}
	cond := c.whileCond(data)
	bodyStart := asStr(data["start_node_id"])
	if bodyStart == "" {
		return append(out, &cd.Comment{Text: fmt.Sprintf("UNKNOWN dify loop node %q (missing start_node_id) — not yet covered", n.ID)})
	}
	out = append(out, &cd.While{Cond: cond, Limit: int(asFloat(data["loop_count"])), Body: &cd.Block{Stmts: c.convert(bodyStart)}})
	// after-loop 延续:loop 自身的 source 后继（节点已从 path 弹出,循环变量
	// 仍在作用域——ncoda while 变量透出）。
	out = append(out, c.branchStmts(c.g.succ(n.ID, "source"))...)
	return out
}

// whileCond 构建 while 继续条件:break 条件的否定（比较符翻转,AND↔OR）。
// 无 break_conditions → 恒真（limit 兜底）。
func (c *converter) whileCond(data map[string]any) cd.Expr {
	bcs := asSlice(data["break_conditions"])
	if len(bcs) == 0 {
		return &cd.LiteralExpr{Value: true}
	}
	op := asStr(data["logical_operator"])
	if op == "" {
		op = "and"
	}
	var e cd.Expr
	for i, bc := range bcs {
		m := asMap(bc)
		sel := asSlice(m["variable_selector"])
		lhs := c.selectorExpr(sel)
		rhs := &cd.LiteralExpr{Value: renderDefaultFor2(c.breakConditionVarType(data, m), m["value"])}
		var neg cd.Expr
		switch asStr(m["comparison_operator"]) {
		case "≥", ">=", "is greater than":
			neg = &cd.BinaryExpr{Op: "<", Left: lhs, Right: rhs}
		case "≤", "<=", "is less than":
			neg = &cd.BinaryExpr{Op: ">", Left: lhs, Right: rhs}
		case ">":
			neg = &cd.BinaryExpr{Op: "<=", Left: lhs, Right: rhs}
		case "<":
			neg = &cd.BinaryExpr{Op: ">=", Left: lhs, Right: rhs}
		case "==", "=":
			neg = &cd.BinaryExpr{Op: "!=", Left: lhs, Right: rhs}
		case "!=", "≠":
			neg = &cd.BinaryExpr{Op: "==", Left: lhs, Right: rhs}
		case "contains":
			neg = &cd.CallExpr{Callee: "!contains", Args: []cd.Expr{lhs, rhs}}
		case "not contains":
			neg = &cd.CallExpr{Callee: "contains", Args: []cd.Expr{lhs, rhs}}
		case "is empty":
			neg = &cd.BinaryExpr{Op: "!=", Left: lhs, Right: &cd.LiteralExpr{Value: ""}}
		case "is not empty":
			neg = &cd.BinaryExpr{Op: "==", Left: lhs, Right: &cd.LiteralExpr{Value: ""}}
		default:
			neg = &cd.BinaryExpr{Op: "!=", Left: lhs, Right: rhs}
		}
		if i == 0 {
			e = neg
			continue
		}
		join := "&&"
		if op == "or" {
			join = "||"
		}
		e = &cd.BinaryExpr{Op: join, Left: e, Right: neg}
	}
	return e
}

// registerLoopVar 注册循环变量名（[loopID,label] 裸名引用）。
func (c *converter) registerLoopVar(nodeID, label string) {
	if c.loopVarLabels == nil {
		c.loopVarLabels = map[string]map[string]bool{}
	}
	if c.loopVarLabels[nodeID] == nil {
		c.loopVarLabels[nodeID] = map[string]bool{}
	}
	c.loopVarLabels[nodeID][label] = true
}

// ── iteration 节点（dify Iteration → for 值循环）──

// convertIteration: dify iteration（iterator_selector + start_node_id +
// output_selector）
//
//	let v<id> = for (item in <iterator_selector 表达式>) {
//	    <body: start_node_id 链（answer/end 属外层,跳过）>
//	    yield <output_selector 表达式>;      // 收集（output_selector 存在时）
//	};
//
// 迭代变量固定 "item"（dify 约定）;体内 [iter,item]/[iter,output] → item,
// 体外 [iter,output] → v<id>（收集数组）。iterator_selector 缺省（退化
// 画布）→ 迭代 start 的 list 变量,且不收集（语句 for,自然透传）。
func (c *converter) convertIteration(n *nodeInfo) []cd.Stmt {
	data := n.Data
	var out []cd.Stmt
	c.registerLoopVar(n.ID, "item")
	iterSel := asSlice(data["iterator_selector"])
	var iterable cd.Expr
	if len(iterSel) >= 2 {
		iterable = c.selectorExpr(iterSel)
	} else {
		// 退化画布（如 09_iteration:iteration 无 data）:迭代 start 的首个
		// list 变量（黄金对照 `for (item in items)`）。
		iterable = c.startListParam()
	}
	outSel := asSlice(data["output_selector"])
	collectOutput := len(outSel) >= 2 && c.iterationOutputUsed(n.ID)
	if len(outSel) >= 2 && !collectOutput {
		// output_selector is an internal edge from the body producer to the
		// iteration container. If the container output is dead, it must not
		// keep that producer alive as a declared-but-unused value.
		c.removeNodeRef(asStr(outSel[0]), n.ID)
	}
	bodyStart := asStr(data["start_node_id"])
	var bodyStmts []cd.Stmt
	if bodyStart != "" {
		save := c.inIterationBody
		c.inIterationBody = true
		bodyStmts = c.convert(bodyStart)
		c.inIterationBody = save
	} else {
		// 退化画布:body = iteration 的 source 后继（非 start_node_id 形态）。
		succs := c.g.succ(n.ID, "source")
		if len(succs) > 0 {
			save := c.inIterationBody
			c.inIterationBody = true
			bodyStmts = c.branchStmts(succs)
			c.inIterationBody = save
		}
	}
	// output_selector 有外部消费者 → 收集（ForExpr）;否则语句 for（保留
	// body 的副作用,不制造无用的 iteration 结果变量）。
	hasStart := bodyStart != ""
	if collectOutput {
		bodyStmts = append(bodyStmts, &cd.Yield{Value: c.refExpr(asStr(outSel[0]), asStr(outSel[1]), pathOf(outSel[2:]))})
		out = []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: &cd.ForExpr{
			Var: "item", Iterable: iterable, Body: &cd.Block{Stmts: bodyStmts},
		}}}
	} else if iterable == nil {
		return []cd.Stmt{&cd.Comment{Text: fmt.Sprintf("UNKNOWN dify iteration node %q (no iterator_selector, no start list var) — not yet covered", n.ID)}}
	} else {
		out = []cd.Stmt{&cd.For{Var: "item", Iterable: iterable, Body: &cd.Block{Stmts: bodyStmts}}}
	}
	// after-loop 延续:仅真实 iteration（start_node_id 存在）——退化画布
	// （无 start_node_id）的 source 后继就是 body,已转换,不得重复。
	if hasStart {
		out = append(out, c.branchStmts(c.g.succ(n.ID, "source"))...)
	}
	return out
}

// iterationOutputUsed reports whether a Dify iteration's collected output is
// selected by any node outside the iteration itself. The body output selector
// is not an external use; it only describes how Dify would build the discarded
// container value.
func (c *converter) iterationOutputUsed(iterationID string) bool {
	for consumer := range c.nodeRefs[iterationID] {
		if consumer != iterationID {
			return true
		}
	}
	return false
}

// removeNodeRef removes one synthetic/internal consumer from the pre-scan
// reference set while preserving all genuine downstream references.
func (c *converter) removeNodeRef(producerID, consumerID string) {
	if refs := c.nodeRefs[producerID]; refs != nil {
		delete(refs, consumerID)
	}
}

// startListParam start 变量中第一个 list 类型变量（退化 iteration 的
// 迭代源回退）。无 → nil。
func (c *converter) startListParam() cd.Expr {
	for _, v := range asSlice(c.g.start.Data["variables"]) {
		m := asMap(v)
		t := asStr(m["type"])
		if t == "list" || t == "array" || strings.HasPrefix(t, "array[") {
			name := asStr(m["variable"])
			if name != "" {
				return &cd.IdentExpr{Name: name}
			}
		}
	}
	return nil
}

// ── assigner 节点（dify Variable Assigner → 赋值语句）──

// convertAssigner: dify assigner items → AssignStmt。目标解析:
//
//	[loop, <label>]       → 循环变量裸名（i += 1 → i = i + 1）
//	[conversation, <name>]→ 会话变量 → 合成可变参数近似（answer = value）
//	[start, <name>]       → 函数参数
//	其他                  → UNKNOWN 注释
//
// 操作映射:over-write/=/→ 直赋;+=/-=/*=//= 复合;append/extend → 拼接;
// clear → 清空（""）。
func (c *converter) convertAssigner(n *nodeInfo) []cd.Stmt {
	var out []cd.Stmt
	for _, it := range asSlice(n.Data["items"]) {
		m := asMap(it)
		sel := asSlice(m["variable_selector"])
		if len(sel) < 2 {
			continue
		}
		targetNode, targetKey := asStr(sel[0]), asStr(sel[1])
		var target string
		switch {
		case targetNode == "conversation":
			target = c.convName(targetKey) // var <name>; 顶层声明（collectConvVars）
		case targetNode == c.startID:
			target = targetKey
		case c.loopVarLabels[targetNode] != nil && c.loopVarLabels[targetNode][targetKey]:
			target = targetKey
		default:
			out = append(out, &cd.Comment{Text: fmt.Sprintf("UNKNOWN dify assigner target %q (%s.%s) — not yet covered", n.ID, targetNode, targetKey)})
			continue
		}
		val := c.assignerValue(m)
		op := asStr(m["operation"])
		switch op {
		case "+=", "-=", "*=", "/=":
			binOp := strings.TrimSuffix(op, "=")
			out = append(out, &cd.AssignStmt{Target: target, Value: &cd.BinaryExpr{
				Op: binOp, Left: &cd.IdentExpr{Name: target}, Right: val}})
		case "over-write", "set", "=", "":
			out = append(out, &cd.AssignStmt{Target: target, Value: val})
		case "append", "extend":
			// dify append 语义按目标类型分派:array 会话变量 → ncoda `<<`
			// 追加操作符（`+` 对 array 无定义,旅行Demo History 实证）;
			// 其他（循环字符串累加器等）→ `+` 拼接（search_dify 实证）。
			if c.convVarIsArray(target) {
				out = append(out, &cd.AssignStmt{Target: target, Op: "<<", Value: val})
			} else {
				out = append(out, &cd.AssignStmt{Target: target, Value: &cd.BinaryExpr{
					Op: "+", Left: &cd.IdentExpr{Name: target}, Right: val}})
			}
		case "clear":
			out = append(out, &cd.AssignStmt{Target: target, Value: c.convClearExpr(target)})
		default:
			out = append(out, &cd.Comment{Text: fmt.Sprintf("UNKNOWN dify assigner operation %q (%s) — not yet covered", op, n.ID)})
		}
	}
	return out
}

// convClearExpr 会话变量 clear 的空值表达式:array → [];number → 0;
// 其余 → ""（dify clear 语义 = 还原到类型空值）。
func (c *converter) convClearExpr(name string) cd.Expr {
	t := convVarNcodaType(c.convVarTypes[name])
	switch t {
	case "array":
		return &cd.ListExpr{}
	case "int":
		return &cd.LiteralExpr{Value: 0}
	default:
		return &cd.LiteralExpr{Value: ""}
	}
}

// assignerValue 赋值值:input_type variable → selector;constant → 字面量。
func (c *converter) assignerValue(m map[string]any) cd.Expr {
	if asStr(m["input_type"]) == "variable" {
		if sel := asSlice(m["value"]); len(sel) >= 2 {
			return c.refExpr(asStr(sel[0]), asStr(sel[1]), pathOf(sel[2:]))
		}
	}
	return &cd.LiteralExpr{Value: m["value"]}
}

// renderLoopVarInit 按 var_type 渲染循环变量初始值（dify 导出 value 常为
// 字符串 "0"/"1",var_type number → int 字面量）。
func renderLoopVarInit(varType string, v any) any {
	if varType == "number" || varType == "int" {
		switch x := v.(type) {
		case string:
			if iv, err := strconv.Atoi(x); err == nil {
				return iv
			}
		case float64:
			return int(x)
		}
	}
	return v
}

// ── list-operator 节点（dify 列表运算 → ncoda filter/sort_by/take 组合）──

// convertListOp: dify list-operator → let v<id> = <嵌套列表表达式>;。
// 输入 = variable [nodeID, key]（sys.files / 上游节点输出）;单节点内
// filter → order_by → limit 依次应用,嵌套为
// take(sort_by(filter(<input>, (item) -> <cond>), key, asc), N)。
// filter 谓词复用 ncoda 既有 lambda 语法（前端 listFilterItem 支持
// contains/in/is/is not/比较运算符）;不支持的比较运算符（start with/
// end with/empty 等,语料 0 例）→ UNKNOWN 降级,记覆盖清单。
func (c *converter) convertListOp(n *nodeInfo) []cd.Stmt {
	data := n.Data
	var e cd.Expr
	// 输入数组引用（dify variable 恒为 [node, key] 二元组）。
	if sel := asSlice(data["variable"]); len(sel) >= 2 {
		e = c.refExpr(asStr(sel[0]), asStr(sel[1]), nil)
	} else {
		e = &cd.IdentExpr{Name: "[]"}
	}
	if e == nil {
		e = &cd.IdentExpr{Name: "[]"}
	}
	// filter_by:单条件（语料实测全单条件;多条件需并列 contains 链,记 TODO）。
	if fb := asMap(data["filter_by"]); asBool(fb["enabled"]) {
		conds := asSlice(fb["conditions"])
		if len(conds) >= 1 {
			cond := asMap(conds[0])
			lam := c.listFilterLambda(cond)
			if lam != nil {
				e = &cd.CallExpr{Callee: "filter", Args: []cd.Expr{e, lam}}
			} else {
				return []cd.Stmt{&cd.Comment{Text: fmt.Sprintf("UNKNOWN dify node %q (list-operator) — unsupported filter operator, not yet covered", n.ID)}}
			}
		}
	}
	// order_by:key 空 → sort(items, "asc");key 非空 → sort_by(items, key, "asc")。
	if ob := asMap(data["order_by"]); asBool(ob["enabled"]) {
		order := asStr(ob["value"])
		if order == "" {
			order = "asc"
		}
		if k := asStr(ob["key"]); k != "" {
			e = &cd.CallExpr{Callee: "sort_by", Args: []cd.Expr{e, &cd.LiteralExpr{Value: k}, &cd.LiteralExpr{Value: order}}}
		} else {
			e = &cd.CallExpr{Callee: "sort", Args: []cd.Expr{e, &cd.LiteralExpr{Value: order}}}
		}
	}
	// limit → take(<inner>, N)。
	if lm := asMap(data["limit"]); asBool(lm["enabled"]) {
		size := asInt(lm["size"])
		if size <= 0 {
			size = 10
		}
		e = &cd.CallExpr{Callee: "take", Args: []cd.Expr{e, &cd.LiteralExpr{Value: size}}}
	}
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: e}}
}

// numericLiteral 尝试把值解析为数字（int/float 优先 int）;纯数字字符串
// （"1"/"1.5"）返回数字,否则 ok=false（保持字符串语义）。
func numericLiteral(v any) (any, bool) {
	switch x := v.(type) {
	case float64:
		if x == float64(int(x)) {
			return int(x), true
		}
		return x, true
	case int:
		return x, true
	case string:
		if x == "" {
			return nil, false
		}
		if iv, err := strconv.Atoi(x); err == nil {
			return iv, true
		}
		if fv, err := strconv.ParseFloat(x, 64); err == nil {
			return fv, true
		}
	}
	return nil, false
}

// listFilterLambda 构造 filter 谓词 `(item) -> <cond>`。item 裸元素（key
// 空）或 item.<key>。支持 contains/in/is/is not/比较运算符,与前端
// listFilterItem 对齐;不支持的运算符返回 nil（调用方降级 UNKNOWN）。
func (c *converter) listFilterLambda(cond map[string]any) cd.Expr {
	key := asStr(cond["key"])
	item := func() cd.Expr {
		if key == "" {
			return &cd.IdentExpr{Name: "item"}
		}
		return &cd.MemberExpr{Obj: &cd.IdentExpr{Name: "item"}, Field: key}
	}
	val := &cd.LiteralExpr{Value: renderDefaultFor2("", cond["value"])}
	op := asStr(cond["comparison_operator"])
	var body cd.Expr
	switch op {
	case "contains":
		// 方法形态 item.<key>.contains(<val>)——前端 IsNativeContains 要求
		// FieldAccessExpr 调用;裸函数 contains(x, v) 在 filter lambda 里
		// 不匹配（1034）。
		body = &cd.CallExpr{Callee: "contains", Receiver: item(), Args: []cd.Expr{val}}
	case "not contains":
		// 前端无 not-contains 方法形态（!contains 是裸函数 if 条件专用）;
		// filter 谓词不支持 → 降级 UNKNOWN（语料 0 例）。
		return nil
	case "in", "not in":
		// dify value 恒为数组 → ncoda membership `in [...]`（file 枚举字段
		// type/transfer_method 必须走 in,capability gate fileEnumKeys 拒绝
		// ==;实测 `item.$type in ["image"]` 全管线通过）。not in → 单元素
		// 退化为 !=（ncoda 无 not in 二元运算,语料 not in 均单元素）。
		items := asSlice(cond["value"])
		vals := make([]cd.Expr, 0, len(items))
		for _, it := range items {
			vals = append(vals, &cd.LiteralExpr{Value: renderDefaultFor2("", it)})
		}
		if op == "not in" && len(vals) == 1 {
			body = &cd.BinaryExpr{Op: "!=", Left: item(), Right: vals[0]}
		} else if op == "not in" && len(vals) > 1 {
			return nil
		} else if len(vals) == 1 {
			// 单元素 in → == 等价但 file 枚举字段（type）被 capability gate
			// 拒绝 == → 恒走 in 数组（1 元素数组语义一致）。
			body = &cd.BinaryExpr{Op: "in", Left: item(), Right: &cd.ListExpr{Items: vals}}
		} else {
			body = &cd.BinaryExpr{Op: "in", Left: item(), Right: &cd.ListExpr{Items: vals}}
		}
	case "is":
		body = &cd.BinaryExpr{Op: "==", Left: item(), Right: val}
	case "is not":
		body = &cd.BinaryExpr{Op: "!=", Left: item(), Right: val}
	case ">", "<", ">=", "<=":
		// 数字字段（file.size 等）的值在 dify 导出是字符串（"1"）,
		// 反编译须还原为数字字面量,否则 `item.size < "1"` 1012 类型错误。
		if n, ok := numericLiteral(cond["value"]); ok {
			val = &cd.LiteralExpr{Value: n}
		}
		body = &cd.BinaryExpr{Op: op, Left: item(), Right: val}
	default:
		return nil
	}
	return &cd.LambdaExpr{Params: []string{"item"}, Body: body}
}

// breakConditionVarType resolves a loop break operand type. The per-condition
// varType echo is optional in current exports; loop_variables is the declared
// authority for labels selected from the loop state.
func (c *converter) breakConditionVarType(loopData, condition map[string]any) string {
	if t := asStr(condition["varType"]); t != "" {
		return t
	}
	sel := asSlice(condition["variable_selector"])
	if len(sel) < 2 {
		return ""
	}
	label := asStr(sel[1])
	for _, rv := range asSlice(loopData["loop_variables"]) {
		m := asMap(rv)
		if asStr(m["label"]) == label {
			return asStr(m["var_type"])
		}
	}
	return ""
}

// templateExprRe 匹配 template-transform 的两种平台模板引用形态:
// 局部变量 {{ var }} 与节点引用 {{#node.key#}}（含 iteration item）。
var templateExprRe = regexp.MustCompile(`\{\{#([^}]+)#\}\}|\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// convertTemplateTransform: let v<id> = <模板串>;。variables 把局部变量名
// 映射到 value_selector 表达式;{{ var }} → ${expr};未知变量保留原文
// （模板文本,诚实）。节点单输出（output）即整个值,refExpr 已映射。
func (c *converter) convertTemplateTransform(n *nodeInfo) []cd.Stmt {
	data := n.Data
	tmpl := asStr(data["template"])
	if !strings.Contains(tmpl, "{{") {
		return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: &cd.LiteralExpr{Value: tmpl}}}
	}
	// 变量名 → 表达式映射（dify 局部名,如 arg1）。
	locals := map[string]cd.Expr{}
	for _, v := range asSlice(data["variables"]) {
		m := asMap(v)
		name := asStr(m["variable"])
		if name == "" {
			continue
		}
		locals[name] = c.selectorExpr(asSlice(m["value_selector"]))
	}
	ts := &cd.TemplateString{}
	last := 0
	for _, m := range templateExprRe.FindAllStringSubmatchIndex(tmpl, -1) {
		start, end := m[0], m[1]
		if start > last {
			ts.Parts = append(ts.Parts, cd.TemplatePart{Text: tmpl[last:start]})
		}
		if m[2] >= 0 {
			inner := tmpl[m[2]:m[3]]
			segs := strings.Split(inner, ".")
			node := segs[0]
			key := ""
			var path []string
			if len(segs) > 1 {
				key = segs[1]
				path = segs[2:]
			}
			ts.Parts = append(ts.Parts, cd.TemplatePart{Expr: c.refExprResolved(node, key, path)})
		} else if m[4] >= 0 {
			name := tmpl[m[4]:m[5]]
			if expr, ok := locals[name]; ok {
				ts.Parts = append(ts.Parts, cd.TemplatePart{Expr: expr})
			} else {
				ts.Parts = append(ts.Parts, cd.TemplatePart{Text: "{{" + name + "}}"})
			}
		}
		last = end
	}
	if last < len(tmpl) {
		ts.Parts = append(ts.Parts, cd.TemplatePart{Text: tmpl[last:]})
	}
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: ts}}
}

// isConvVar 报告 name 是否为已收集的会话变量（assigner 写目标）。
func (c *converter) isConvVar(name string) bool {
	for _, n := range c.convVars {
		if n == name {
			return true
		}
	}
	return false
}

// envConst 一个被引用的环境变量常量声明。
type envConst struct {
	Name  string
	Value any
}

// collectEnvVars 预收集环境变量名 → 声明（convert 前调用）。
func (c *converter) collectEnvVars() {
	c.envVars = map[string]envVarDecl{}
	for name, d := range c.g.envVars {
		c.envVars[name] = d
	}
}

// renameConvVarCollisions 会话变量名与 start 参数同名时改名（如 start 的
// file text 与 conversation 的 text）——ncoda 一个作用域不能重名,两个
// 实体语义不同,保留 start 参数原名、会话变量改名 <name>_cv（重名概率
// 低;改名破坏 conversation_variables 名字契约,记入覆盖清单）。类型
// 查询仍按原名（convVarTypes/convVarIsArray 键不变）。
func (c *converter) renameConvVarCollisions() {
	c.convRename = map[string]string{}
	for _, name := range c.convVars {
		if !c.startVars[name] {
			continue
		}
		renamed := name + "_cv"
		for c.startVars[renamed] || c.paramSeen[renamed] {
			renamed += "2"
		}
		c.convRename[name] = renamed
	}
}

// convName 会话变量渲染名（改名映射;无冲突 → 原名）。
func (c *converter) convName(name string) string {
	if r, ok := c.convRename[name]; ok {
		return r
	}
	return name
}

// envIdent 环境变量引用 → 顶层 const 声明 + 裸名标识符（只读,与模型
// const 同形态;const 值 = dify 值字面量,名字 $ 转义由渲染层处理）。
func (c *converter) envIdent(name string) *cd.IdentExpr {
	if _, ok := c.envVars[name]; !ok {
		return nil // 悬空 env 引用（YAML 未声明）→ 不捏造,走原 fallback
	}
	for _, ec := range c.envConsts {
		if ec.Name == name {
			return &cd.IdentExpr{Name: name}
		}
	}
	c.envConsts = append(c.envConsts, envConst{Name: name, Value: c.envVars[name].Value})
	return &cd.IdentExpr{Name: name}
}

// collectConvVars 预扫描会话变量:YAML 声明（conversation_variables 块,带
// 类型/初值）+ assigner 写目标（convert 前调用,供 refExpr/声明判定）。
// 最终渲染形态由 DecompileYAML 在 convert 后按 c.mode 决定:advanced-chat
// → @conversation 声明;workflow 降级 → 顶层 var <name>;（无类型,
// 记覆盖清单）。
func (c *converter) collectConvVars() {
	c.convVarTypes = map[string]string{}
	seen := map[string]bool{}
	for name, d := range c.g.convVars {
		if !seen[name] {
			seen[name] = true
			c.convVars = append(c.convVars, name)
		}
		c.convVarTypes[name] = d.Type
	}
	c.renameConvVarCollisions()
	for _, n := range c.g.nodes {
		if n.Type != "assigner" {
			continue
		}
		for _, it := range asSlice(n.Data["items"]) {
			m := asMap(it)
			sel := asSlice(m["variable_selector"])
			if len(sel) >= 2 && asStr(sel[0]) == "conversation" {
				name := asStr(sel[1])
				if name == "" {
					continue
				}
				if !seen[name] {
					seen[name] = true
					c.convVars = append(c.convVars, name)
				}
				if c.convVarTypes[name] == "" {
					c.convVarTypes[name] = asStr(m["var_type"])
				}
			}
		}
	}
}

// convVarNcodaType dify 会话变量 value_type → ncoda 类型字符串。
// number → int;boolean → bool;array[T] → array<T>;string/paragraph → string;
// file → file;object/未知 → any（不捏造,记覆盖清单）。
func convVarNcodaType(t string) string {
	if strings.HasPrefix(t, "array[") && strings.HasSuffix(t, "]") {
		return "array<" + convVarNcodaType(t[6:len(t)-1]) + ">"
	}
	switch t {
	case "number", "integer", "int":
		return "int"
	case "boolean", "bool":
		return "bool"
	case "string", "text", "paragraph":
		return "string"
	case "file":
		return "file"
	default:
		return "any"
	}
}

// convVarKind dify 会话变量 value_type → 语义种类（array/int/bool/string/
// file/any）,供初值/标量判定（convVarNcodaType 返回完整 ncoda 类型串）。
func convVarKind(t string) string {
	if strings.HasPrefix(t, "array") {
		return "array"
	}
	switch t {
	case "number", "integer", "int":
		return "int"
	case "boolean", "bool":
		return "bool"
	case "file":
		return "file"
	case "string", "text", "paragraph":
		return "string"
	default:
		return "any"
	}
}

// convVarInitExpr 会话变量初值（dify value）→ ncoda 表达式。number → int
// 字面量;boolean → bool;array → 列表字面量;object → 字典字面量;其余字符串。
func convVarInitExpr(t string, v any) cd.Expr {
	switch convVarKind(t) {
	case "int":
		switch x := v.(type) {
		case string:
			if iv, err := strconv.Atoi(x); err == nil {
				return &cd.LiteralExpr{Value: iv}
			}
		case float64:
			return &cd.LiteralExpr{Value: int(x)}
		case int:
			return &cd.LiteralExpr{Value: x}
		case int64:
			return &cd.LiteralExpr{Value: int(x)}
		}
		return &cd.LiteralExpr{Value: 0}
	case "bool":
		switch x := v.(type) {
		case bool:
			return &cd.LiteralExpr{Value: x}
		case string:
			return &cd.LiteralExpr{Value: x == "true"}
		}
		return &cd.LiteralExpr{Value: false}
	case "array":
		return &cd.ListExpr{}
	case "file":
		return &cd.LiteralExpr{Value: ""}
	default:
		if s, ok := v.(string); ok {
			return &cd.LiteralExpr{Value: s}
		}
		return &cd.LiteralExpr{Value: ""}
	}
}

// convVarStmts 会话变量顶层声明（函数体内）。advanced-chat 模式走
// @conversation 顶层声明（DecompileYAML 输出,本函数返回空）;workflow
// 模式退化为 `var <name>;`（无初值,可变;LoopStatePass 豁免 Init==nil
// 的可变声明——dify workflow 模式无会话变量语义,记覆盖清单）。
func (c *converter) convVarStmts() []cd.Stmt {
	if c.mode == "advanced-chat" {
		return nil
	}
	var out []cd.Stmt
	for _, name := range c.convVars {
		// 带类型可变声明（`string text_cv;`）——无类型 var = any,
		// llm 模板引用 text_cv 时 1003（expected string got any）。
		out = append(out, &cd.VarDecl{Name: c.convName(name), Type: convVarNcodaType(c.convVarTypes[name]), Mutable: true})
	}
	return out
}

// conversationDecls 渲染 @conversation 顶层声明（advanced-chat 模式）。
// `@conversation <ncoda类型> <name> = <初值>;`——初值来自 dify
// conversation_variables.value,语义 = 会话起始值（ncoda 原生会话变量,
// 类型/初值经 TypePass 校验,写操作豁免 LoopStatePass 状态检查）。
func (c *converter) conversationDecls() []string {
	if c.mode != "advanced-chat" {
		return nil
	}
	var out []string
	for _, name := range c.convVars {
		t := c.convVarTypes[name]
		init := convVarInitExpr(t, c.g.convVars[name].Value)
		// 无初值声明的会话变量（YAML 未给 value）→ 省略默认值
		// （@conversation <type> <name>;,允许）。
		if c.g.convVars[name].Value == nil {
			out = append(out, fmt.Sprintf("@conversation %s %s;", convVarNcodaType(t), render.EscapeIdent(c.convName(name))))
			continue
		}
		out = append(out, fmt.Sprintf("@conversation %s %s = %s;",
			convVarNcodaType(t), render.EscapeIdent(c.convName(name)), render.RenderNCodaExpr(init)))
	}
	return out
}

// renderDefaultFor2 按 varType 渲染比较值（number → int 字面量;dify 导出
// 常为字符串 "2"）。
func renderDefaultFor2(varType string, v any) any {
	if varType == "number" || varType == "int" {
		switch x := v.(type) {
		case string:
			if iv, err := strconv.Atoi(x); err == nil {
				return iv
			}
		case float64:
			return int(x)
		}
	}
	return v
}

// ── variable-aggregator（dify 分支值汇聚 → ncoda 合并）──

// convertAggregator: dify variable-aggregator → 组 dict 或单值。
//
//	dify 简单形态（variables 模板/选择器列表）→ 单组,值 = 候选 ref
//	  或 || 合并链（互斥分支 first_not_null,cozebiz 同语义）;
//	dify 分组形态（advanced_settings.groups）→ 组名 → 组值 dict。
//	分支局部候选（在 if 分支内声明）→ 组值提升为顶层可变变量,候选
//	声明后写回（aggHoistSrc 挂载赋值）——ncoda 无 merge 节点,互斥
//	分支汇聚 = 变量 + 分支赋值（var 提升路径,与 cozebiz 同构）。
func (c *converter) convertAggregator(n *nodeInfo) []cd.Stmt {
	groups := c.aggGroups[n.ID]
	if len(groups) == 0 {
		c.noteUnknown("unknown-node:variable-aggregator-unparsed")
		return []cd.Stmt{&cd.Comment{Text: fmt.Sprintf("UNKNOWN dify node %q (variable-aggregator) — unparsed groups, not yet covered", n.ID)}}
	}
	if len(groups) == 1 && groups[0].name == "" {
		// 简单形态:单值（非组 dict）——dify 聚合输出即值本身。
		return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: c.aggGroupValue(groups[0])}}
	}
	d := &cd.DictExpr{}
	for _, g := range groups {
		if g.name == "" {
			continue
		}
		dictPut(d, g.name, c.aggGroupValue(g))
	}
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: d}}
}

// aggGroupValue 一个组的汇聚值:提升变量（分支互斥写）或 || 链（全部可见）。
func (c *converter) aggGroupValue(g aggGroupInfo) cd.Expr {
	if g.hoistVar != "" {
		return &cd.IdentExpr{Name: g.hoistVar}
	}
	var e cd.Expr
	for _, sel := range g.sels {
		if len(sel) < 2 {
			continue
		}
		x := c.refExpr(asStr(sel[0]), asStr(sel[1]), pathOf(sel[2:]))
		if e == nil {
			e = x
			continue
		}
		e = &cd.BinaryExpr{Op: "||", Left: e, Right: x}
	}
	if e == nil {
		return &cd.LiteralExpr{Value: ""}
	}
	return e
}

// collectAggGroups 预扫描全部 variable-aggregator 节点:解析分组 + 判定
// 候选可见性 + 挂载提升。convert 前调用（collectConvVars 之后）。
func (c *converter) collectAggGroups() {
	c.aggGroups = map[string][]aggGroupInfo{}
	c.aggHoistSrc = map[string]aggHoistTarget{}
	c.generalHoists = map[string]bool{}
	c.computeBranchScopes()
	for _, n := range c.g.nodes {
		if n.Type != "variable-aggregator" {
			continue
		}
		groups := parseAggGroups(n)
		if len(groups) == 0 {
			continue
		}
		for gi := range groups {
			g := &groups[gi]
			needHoist := false
			for _, sel := range g.sels {
				if len(sel) < 2 {
					continue
				}
				cid := asStr(sel[0])
				if c.g.nodes[cid] == nil {
					continue
				}
				if !c.aggVisible(cid, n.ID) {
					needHoist = true
					break
				}
			}
			if !needHoist {
				continue
			}
			// 组内任一候选分支局部 → 整组提升（组 = 互斥分支候选,单变量汇聚）。
			g.hoistVar = aggHoistName(n.ID, g.name, gi)
			c.hoistVars = append(c.hoistVars, g.hoistVar)
			for _, sel := range g.sels {
				if len(sel) < 2 {
					continue
				}
				cid := asStr(sel[0])
				if c.g.nodes[cid] == nil {
					continue
				}
				c.aggHoistSrc[cid] = aggHoistTarget{Var: g.hoistVar, Sel: sel}
			}
		}
		c.aggGroups[n.ID] = groups
	}
	// 泛化 var 提升（聚合器候选已挂载组变量,排除）。
	c.collectGeneralHoists()
}

// aggHoistName 提升组变量名（g<aggID>_<组名或序号>,标识符合法）。
func aggHoistName(aggID, groupName string, idx int) string {
	if groupName != "" {
		return "g" + sanitizeID(aggID) + "_" + sanitizeID(groupName)
	}
	return fmt.Sprintf("g%s_g%d", sanitizeID(aggID), idx)
}

// computeBranchScopes 计算分支嵌套签名:对每个 if-else 与 parallel 扇出
// （同 source handle 多出边）,仅从该分支可达（XOR）的节点标记
// (ifID, handle)。聚合器候选可见性判定依据:候选的分支签名 ⊆ 聚合器位置
// 的分支签名（候选所在分支若已收敛,其声明已被 restore——不可见）。
// parallel 分支同样记录（P5 透明屏障:分支局部值被分支后引用 → 提升;
// 兄弟分支互引用 → 提升后编译器 E1036 精确诊断竞态,而非误导性未定义）。
func (c *converter) computeBranchScopes() {
	c.branchScopes = map[string]map[string]string{}
	c.branchInside = map[string]bool{}
	for _, n := range c.g.nodes {
		if n.Type == "if-else" {
			var branches [][]string
			handles := []string{}
			for _, cs := range c.ifCases(n) {
				handles = append(handles, cs.handle)
				branches = append(branches, c.g.succ(n.ID, cs.handle))
			}
			if len(c.g.succ(n.ID, "false")) > 0 {
				handles = append(handles, "false")
				branches = append(branches, c.g.succ(n.ID, "false"))
			}
			c.collectScopeFanout(n.ID, handles, branches, "")
			continue
		}
		// question-classifier 多出口:每 class 一条分支边（sourceHandle =
		// class id,互斥 OR 分类）→ 记录 branchScope 供提升判定（分支局部
		// 值被分支后引用 → var 提升;跨分支引用 → 提升后编译器精确诊断）。
		if n.Type == "question-classifier" {
			var branches [][]string
			handles := []string{}
			for _, rc := range asSlice(n.Data["classes"]) {
				cm := asMap(rc)
				id := asStr(cm["id"])
				if id == "" {
					continue
				}
				handles = append(handles, id)
				branches = append(branches, c.g.succ(n.ID, id))
			}
			conv := c.switchConv(n)
			c.collectScopeFanout(n.ID, handles, branches, conv)
			continue
		}
		// parallel 扇出:同 source handle 多出边（源节点本身非 if-else）。
		// 分支互斥（AND 栅栏各分支独占）——记录 branchScope 供提升判定。
		succs := c.g.succ(n.ID, "source")
		if len(succs) <= 1 {
			continue
		}
		handles := make([]string, 0, len(succs))
		branches := make([][]string, 0, len(succs))
		for i := range succs {
			handles = append(handles, fmt.Sprintf("p%d", i))
			branches = append(branches, []string{succs[i]})
		}
		c.collectScopeFanout(n.ID, handles, branches, c.convergence(succs))
	}
}

// collectScopeFanout 对一组互斥分支（if-else cases / parallel 扇出）:
// 各分支可达集（到 barrier 为止）,XOR 独占节点记 branchScope (fanID,
// handle)。branches[i] 是分支 i 的入口后继列表;barrier = 并行收敛点
// （parallel 时）;if-else 无 barrier（分支内可达直到 answer/end/后向
// 汇入,与旧 reachable 一致）。
func (c *converter) collectScopeFanout(fanID string, handles []string, branches [][]string, barrier string) {
	reachSets := map[string]map[string]bool{}
	for i, h := range handles {
		reachSets[h] = map[string]bool{}
		for _, t := range branches[i] {
			c.reachableTo(t, barrier, reachSets[h])
		}
	}
	// 所有可达节点（含非独占）记 branchInside——"在任一分支结构内"的判定
	// 依据。非独占节点（多分支都出现,如两分支共用同一 code 节点）虽无
	// XOR 独占签名,仍属分支内声明:若被分支外引用必须提升（conditional_
	// parallel 实证:v11100 在 then/else 两分支 parallel 内都 let,if 外
	// return 引用 → 无 branchScope 导致未提升 → UNDEFINED_VAR）。
	for _, h1 := range handles {
		for id := range reachSets[h1] {
			c.branchInside[id] = true
		}
	}
	// 节点只在其可达集合中出现（其他分支集合均不含）→ 该分支局部,
	// 记 branchScope（聚合提升 / 泛化提升判定依据）。
	for _, h1 := range handles {
		for id := range reachSets[h1] {
			exclusive := true
			for _, h2 := range handles {
				if h1 == h2 {
					continue
				}
				if reachSets[h2][id] {
					exclusive = false
					break
				}
			}
			if exclusive {
				c.scopeAdd(id, fanID, h1)
			}
		}
	}
}

// reachableTo 可达集（到 barrier 为止;barrier 本身不计入,由外层继续）。
func (c *converter) reachableTo(id, barrier string, acc map[string]bool) map[string]bool {
	if acc[id] {
		return acc
	}
	acc[id] = true
	if id == barrier || id == "" {
		return acc
	}
	n := c.g.nodes[id]
	if n == nil {
		return acc
	}
	switch n.Type {
	case "answer", "end":
		return acc
	}
	for _, t := range c.g.succ(id, "source") {
		c.reachableTo(t, barrier, acc)
	}
	if n.Type == "if-else" {
		for _, cs := range c.ifCases(n) {
			for _, t := range c.g.succ(id, cs.handle) {
				c.reachableTo(t, barrier, acc)
			}
		}
		for _, t := range c.g.succ(id, "false") {
			c.reachableTo(t, barrier, acc)
		}
	}
	if n.Type == "question-classifier" {
		for _, rc := range asSlice(n.Data["classes"]) {
			cm := asMap(rc)
			for _, t := range c.g.succ(id, asStr(cm["id"])) {
				c.reachableTo(t, barrier, acc)
			}
		}
	}
	return acc
}

func (c *converter) scopeAdd(nodeID, ifID, handle string) {
	if c.branchScopes[nodeID] == nil {
		c.branchScopes[nodeID] = map[string]string{}
	}
	c.branchScopes[nodeID][ifID] = handle
}

// aggVisible 候选节点在聚合器位置是否可见:候选的分支嵌套签名必须是
// 聚合器位置签名的子集（无未收敛分支遮挡）,且候选在聚合器上游。
func (c *converter) aggVisible(cid, aggID string) bool {
	if len(c.branchScopes) == 0 {
		return true
	}
	cand := c.branchScopes[cid]
	at := c.branchScopes[aggID]
	for ifID, handle := range cand {
		if at[ifID] != handle {
			return false
		}
	}
	return c.reachableHas(cid, aggID)
}

// parseAggGroups 解析聚合器分组:分组形态（advanced_settings.groups）
// 优先;否则简单形态（variables 模板串 / 选择器列表 → 单组）。
func parseAggGroups(n *nodeInfo) []aggGroupInfo {
	data := n.Data
	groups := asSlice(asMap(data["advanced_settings"])["groups"])
	if len(groups) > 0 {
		var out []aggGroupInfo
		for _, rg := range groups {
			gm := asMap(rg)
			g := aggGroupInfo{name: asStr(gm["group_name"])}
			for _, rv := range asSlice(gm["variables"]) {
				if sel := asSlice(rv); len(sel) >= 2 {
					g.sels = append(g.sels, sel)
				}
			}
			if len(g.sels) > 0 {
				out = append(out, g)
			}
		}
		return out
	}
	var g aggGroupInfo
	switch v := data["variables"].(type) {
	case string:
		// 模板串形态:"{{#llm-summary.text#}}" → 候选 [llm-summary, text]。
		for _, m := range templateRefRe.FindAllStringSubmatch(v, -1) {
			segs := strings.Split(m[1], ".")
			sel := []any{segs[0]}
			if len(segs) > 1 {
				sel = append(sel, segs[1])
			}
			for _, p := range segs[2:] {
				sel = append(sel, p)
			}
			g.sels = append(g.sels, sel)
		}
	case []any:
		for _, rv := range asSlice(data["variables"]) {
			if sel := asSlice(rv); len(sel) >= 2 {
				g.sels = append(g.sels, sel)
			}
		}
	}
	if len(g.sels) == 0 {
		return nil
	}
	return []aggGroupInfo{g}
}

// hoistVarStmts 聚合提升组变量的顶层声明（var <name>;,无初值可变——
// 与会话变量同形,LoopStatePass 豁免无初值声明）。
func (c *converter) hoistVarStmts() []cd.Stmt {
	var out []cd.Stmt
	for _, name := range c.hoistVars {
		vd := &cd.VarDecl{Name: name, Mutable: true}
		var zeroExpr cd.Expr
		// 提升节点带输出形状（nodeHoistType）→ 声明带类型,下游字段访问
		// 可解析（完蛋语料 v<id>.text 提升后 1040 → 通过）。
		for id, t := range c.hoistTypes {
			if varName(id) == name {
				vd.Type = t
				zeroExpr = c.hoistZero[id]
				break
			}
		}
		out = append(out, vd)
		// 类型零值初始（dify runtime 缺省值安全网显式化）:
		// `var v: T;` 带类型声明 definite-init 视为未初始化,紧跟
		// `v = <zero>;` 使其确定初始化,且分支内赋值覆盖零值——语义对齐
		// dify runtime"未执行路径字段 = null"。无法生成零值的提升变量
		// （any/file/未知）不带类型（裸 `var v;`,definite-init 豁免）。
		if zeroExpr != nil {
			out = append(out, &cd.AssignStmt{Target: name, Value: zeroExpr})
		}
	}
	return out
}

// ── http-request（dify HTTP → ncoda 内建 http()）──

// convertHTTP: let v<id> = http(<method>, <url>, {headers, params, body});
// url/headers/params/body 的 {{#node.key#}} 占位符还原为 ${expr} 插值。
// 输出端口（body/status_code/headers）与内建 http 结果契约一致,下游
// ref 可解析（05 实证:{{#http.body#}} → v<id>.body）。
func (c *converter) convertHTTP(n *nodeInfo) []cd.Stmt {
	data := n.Data
	args := []cd.Expr{
		&cd.LiteralExpr{Value: asStr(data["method"])},
		c.renderTemplate(asStr(data["url"])),
	}
	opts := &cd.DictExpr{}
	if h := asStr(data["headers"]); h != "" {
		dictPut(opts, "headers", c.renderTemplate(h))
	}
	if p := asStr(data["params"]); p != "" {
		dictPut(opts, "params", c.renderTemplate(p))
	}
	if body := asMap(data["body"]); body != nil {
		if bd := asStr(body["data"]); bd != "" {
			dictPut(opts, "body", c.renderTemplate(bd))
		}
	}
	if len(opts.Keys) > 0 {
		args = append(args, opts)
	}
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: &cd.CallExpr{Callee: "http", Args: args}}}
}

// ── knowledge-retrieval（dify 知识库检索 → 内建 knowledge()）──

// convertDocumentExtractor: let v<id> = extract_text(<file>);
// dify document-extractor 从文件提取文本（输出端口 text:string,契约见
// analyzer type_system.go）。variable_selector → 文件表达式。is_array_file
// （数组文件）dify 逐文件提取后 text 仍为 string（本仓库 dify 契约）,
// ncoda 无数组批量提取内建 → 单文件 extract_text 覆盖;数组场景软降级为
// extract_text(<数组>)（覆盖清单 §document-extractor）,下游 .text 引用
// 保持可解析。
func (c *converter) convertDocumentExtractor(n *nodeInfo) []cd.Stmt {
	data := n.Data
	expr := c.selectorExpr(asSlice(data["variable_selector"]))
	if expr == nil {
		expr = &cd.LiteralExpr{Value: ""}
	}
	call := &cd.CallExpr{Callee: "extract_text", Args: []cd.Expr{expr}}
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: call}}
}

// convertKnowledge: let v<id> = knowledge("<ds1>,<ds2>", <query>);
// dataset_ids 逗号连接为 datasets 参（正编译 buildKnowledge 同形态）;
// query 模板 {{#sys.query#}} → 合成参数。输出端口 result（array）与
// 内建 knowledge 结果契约一致。
func (c *converter) convertKnowledge(n *nodeInfo) []cd.Stmt {
	data := n.Data
	ds := make([]string, 0, 4)
	for _, r := range asSlice(data["dataset_ids"]) {
		if s := asStr(r); s != "" {
			ds = append(ds, s)
		}
	}
	// Current Dify exports bind the query through query_variable_selector.
	// The top-level query template is the registered legacy shape and is
	// used only when the selector is absent.
	query := c.renderTemplate(asStr(data["query"]))
	if sel := asSlice(data["query_variable_selector"]); len(sel) > 0 {
		query = c.selectorExpr(sel)
	}
	args := []cd.Expr{
		&cd.LiteralExpr{Value: strings.Join(ds, ",")},
		query,
	}
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: &cd.CallExpr{Callee: "knowledge", Args: args}}}
}

// ── parameter-extractor（dify 结构化抽取 → 内建 extract<T>()）──

// convertParameterExtractor: 合成 record 类型 + extract<T>(MODEL, query, {})。
//
//	type Extract<id> = { <type> <field>; ... };
//	let v<id> = extract<Extract<id>>(MODEL_<i>, <query>, {});
//
// parameters[].type → ncoda 字段类型（select→string,number→int,...
// 见 difyParamTypeToNcoda）;memory/reasoning_mode 无源码级对应 → 软降级
// （覆盖清单记录）。结果访问 v<id>.value.<field>（extract 的 value 信封）。
func (c *converter) convertParameterExtractor(n *nodeInfo) []cd.Stmt {
	data := n.Data
	model := asMap(data["model"])
	cn := c.modelConstRef(asStr(model["provider"]), asStr(model["name"]))
	query := c.selectorExpr(asSlice(data["query"]))
	if query == nil {
		query = &cd.LiteralExpr{Value: ""}
	}
	typeName := "Extract" + sanitizeID(n.ID)
	td := &cd.TypeDecl{Name: typeName}
	for _, rp := range asSlice(data["parameters"]) {
		pm := asMap(rp)
		name := asStr(pm["name"])
		if name == "" {
			continue
		}
		td.Fields = append(td.Fields, cd.TypeField{Type: difyParamTypeToNcoda(asStr(pm["type"])), Name: name})
	}
	// parameter-extractor 信封字段（正编译 emitExtraction 的适配器输出同
	// 契约）:__is_success(number→int,语料条件 == 0)/__reason(string)/
	// __usage(object→any)。不声明则下游 v.value.__is_success 引用报
	// "Field not found in struct"（PG 语料实证）。
	td.Fields = append(td.Fields,
		cd.TypeField{Type: "int", Name: "__is_success"},
		cd.TypeField{Type: "string", Name: "__reason"},
		cd.TypeField{Type: "any", Name: "__usage"},
	)
	c.addExtraType(td)
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: &cd.CallExpr{
		Callee:   "extract",
		TypeArgs: []string{typeName},
		Args:     []cd.Expr{&cd.IdentExpr{Name: cn}, query, &cd.DictExpr{}},
	}}}
}

// addExtraType 登记合成 record 类型声明（按名去重——共享节点被多路径
// 转换时 extraTypes 会重复,1038 实证）。
func (c *converter) addExtraType(td *cd.TypeDecl) {
	for _, t := range c.extraTypes {
		if t.Name == td.Name {
			return
		}
	}
	c.extraTypes = append(c.extraTypes, td)
}

// difyParamTypeToNcoda parameter-extractor 参数类型 → ncoda 字段类型。
// select/paragraph/email/phone/url → string;number → int;boolean → bool;
// array[T] → array<T>;未知 → string（保守,不捏造 object）。
func difyParamTypeToNcoda(t string) string {
	if strings.HasPrefix(t, "array[") && strings.HasSuffix(t, "]") {
		return "array<" + difyParamTypeToNcoda(t[6:len(t)-1]) + ">"
	}
	switch t {
	case "select", "paragraph", "email", "phone", "url", "string", "text":
		return "string"
	case "number", "integer":
		return "int"
	case "boolean", "bool":
		return "bool"
	case "file":
		return "file"
	case "object", "any":
		return "any"
	default:
		return "string"
	}
}

// ── 泛化 var 提升（分支局部值被外部引用 → 顶层可变变量 + 分支赋值）──

// collectGeneralHoists 预扫描:if 分支内声明的产值节点若被分支外引用
// （作用域签名不包含候选的分支嵌套）→ 提升:prelude 声明 `var vC;`,
// 分支内 `let vC = ...` 改为赋值 `vC = ...`,外部引用解析到 vC。
// 覆盖 dify "可选分支未执行 = null" 语义（ncoda 未初始化可变变量）。
// 聚合器候选已在 collectAggGroups 处理（组变量）,排除避免双重提升。
func (c *converter) collectRefs() {
	c.nodeRefs = map[string]map[string]bool{} // 被引用节点 id → 引用位置节点 id 集
	for _, n := range c.g.nodes {
		if n == nil || n.Data == nil {
			continue
		}
		collectRefsInData(n.ID, n.Data, c.nodeRefs)
	}
	c.collectTerminalRefs()
}

// collectTerminalRefs 补充 answer/end 的隐式引用——answer/end 的模板引用
// 在转换期经 resolveRefNode → pathTerminal 重定向:字面引用的节点 X 未在
// 当前路径声明（X 在兄弟分支）时,回退到"当前路径末端产值 producer"。
// collectRefsInData 只收字面引用,漏掉这些重定向目标 → 兄弟分支 producer
// 被判未引用 → 降级为裸调用（不声明变量）→ 分支内 return 引用未声明
// 变量 → UNDEFINED_VAR（03/10 实证:false 分支 llm 输出未被 answer 模板
// 字面引用,但 false 路径 answer 经 pathTerminal 引用它）。
//
// 算法:对每个 answer/end 节点 A——
//  1. A 的字面 producer 引用集 R（触发 pathTerminal 的候选;sys/start/
//     悬空引用/循环变量不触发,排除）。
//  2. 沿 A 的入边回溯,找每条路径的末端产值 producer（距 A 最近的产值
//     节点 = pathTerminal 的静态等价;非产值节点继续回溯）。
//  3. 末端 producer P 被 A 引用 ⇔ 存在一条 start→P 路径避开某个 X∈R
//     （该路径上 X 未声明 → pathTerminal 重定向到 P）。P 自身在 R 中则
//     已有字面引用,无需补充。
func (c *converter) collectTerminalRefs() {
	for _, a := range c.g.nodes {
		if a == nil || (a.Type != "answer" && a.Type != "end") {
			continue
		}
		// 1. A 字面引用的产值节点（pathTerminal 触发候选）。
		refs := map[string]bool{}
		for id, posSet := range c.nodeRefs {
			if !posSet[a.ID] {
				continue
			}
			rn := c.g.nodes[id]
			if rn == nil || !isValueProducer(rn.Type) {
				continue
			}
			refs[id] = true
		}
		if len(refs) == 0 {
			continue // 无产值引用 → pathTerminal 永不触发,无隐式引用
		}
		// 2. 沿入边回溯各路径末端 producer。
		terminals := map[string]bool{}
		seen := map[string]bool{}
		var walk func(id string)
		walk = func(id string) {
			if seen[id] || id == c.startID {
				return
			}
			seen[id] = true
			rn := c.g.nodes[id]
			if rn == nil {
				return
			}
			if isValueProducer(rn.Type) {
				terminals[id] = true
				return // 距 A 最近的产值节点即该路径末端;更上游非末端
			}
			for _, e := range c.g.edges {
				if e.Target == id {
					walk(e.Source)
				}
			}
		}
		for _, e := range c.g.edges {
			if e.Target == a.ID {
				walk(e.Source)
			}
		}
		// 3. 判定每个末端 producer 是否被 A 经 pathTerminal 隐式引用。
		for p := range terminals {
			if refs[p] {
				continue // 已有字面引用,不补充
			}
			refBy := false
			for x := range refs {
				if x == p {
					continue
				}
				if c.reachableAvoiding(c.startID, p, x) {
					refBy = true
					break
				}
			}
			if refBy {
				if c.nodeRefs[p] == nil {
					c.nodeRefs[p] = map[string]bool{}
				}
				c.nodeRefs[p][a.ID] = true
			}
		}
	}
}

// reachableAvoiding 从 from 出发沿控制流边是否可达 to（避开 avoid 节点）。
// 遍历规则与 reachableTo 一致:source 线性边 + if-else 分支边;avoid 节点
// 及其子树不可达（该路径上 avoid 未声明 → pathTerminal 重定向）。
func (c *converter) reachableAvoiding(from, to, avoid string) bool {
	if from == to {
		return true
	}
	seen := map[string]bool{}
	queue := []string{from}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		n := c.g.nodes[id]
		if n == nil {
			continue
		}
		var next []string
		next = append(next, c.g.succ(id, "source")...)
		if n.Type == "if-else" {
			for _, cs := range c.ifCases(n) {
				next = append(next, c.g.succ(id, cs.handle)...)
			}
			next = append(next, c.g.succ(id, "false")...)
		}
		for _, t := range next {
			if t == avoid {
				continue
			}
			if t == to {
				return true
			}
			queue = append(queue, t)
		}
	}
	return false
}

func (c *converter) collectGeneralHoists() {
	if len(c.branchScopes) == 0 {
		return
	}
	for _, n := range c.g.nodes {
		if n == nil || !isValueProducer(n.Type) || n.Type == "variable-aggregator" {
			continue
		}
		if _, aggCand := c.aggHoistSrc[n.ID]; aggCand {
			continue // 聚合器候选走组变量
		}
		if !c.branchInside[n.ID] {
			continue // 不在任何分支内 → 全局可见,无需提升
		}
		// fan 收敛终端（answer/单值 end）→ 分支内节点不提升:分支内各自
		// return,answer 引用经 pathTerminal 解析本路径 producer（golden 03/10
		// 形态）,无需跨分支提升变量;提升反而造成 false 分支引用未初始化
		// 提升变量（E1048 语义 bug,03/10 实证）。多输出 end（dict 精确引用,
		// conditional_parallel 实证）→ pathTerminal 重定向会错,保持提升。
		// 仅 parallel 分支（P5 透明屏障,分支内变量透出外层）→ 不提升,分支内
		// let 声明 + 外层透明透出引用即可（binder visitParallelStmt 在 join
		// 点发布到外层 scope）。if-else 分支（互斥块）→ 需提升。
		if len(c.branchScopes[n.ID]) > 0 {
			allParallel := true
			terminalFan := false
			for fanID := range c.branchScopes[n.ID] {
				node := c.g.nodes[fanID]
				if node != nil && node.Type == "if-else" {
					allParallel = false
					conv := c.ifConv(node)
					if conv != "" && c.isTerminalNode(conv) {
						terminalFan = true
					}
					break
				}
				// question-classifier 同 if-else：互斥 OR 分类块（非 parallel
				// 透明屏障），分支局部值被分支后引用需提升。
				if node != nil && node.Type == "question-classifier" {
					allParallel = false
					conv := c.switchConv(node)
					if conv != "" && c.isTerminalNode(conv) {
						terminalFan = true
					}
					break
				}
			}
			if allParallel {
				continue // 仅 parallel → P5 透明透出,不提升
			}
			if terminalFan {
				continue // fan 收敛终端 → 分支内 return,不提升
			}
		}
		// 谁引用了 n（引用位置集）:若存在引用者不在 n 的分支嵌套内
		// （跨分支引用,如分支外/另一分支引用）→ n 必须提升为顶层 var
		// + 分支内赋值,否则引用处 Undefined variable。
		// 特殊:节点在分支内但非 XOR 独占（多分支都出现,无 branchScope）,
		// 如 conditional_parallel 的 v11100（then/else 两分支都 let）→
		// 分支内声明 let 仅局部,被外部引用必须提升（branchInside 捕获）。
		for refPos := range c.nodeRefs[n.ID] {
			if refPos == n.ID {
				continue
			}
			// 节点无独占签名（非 XOR）→ 分支内非独占声明,任何外部引用均需提升。
			// 节点有独占签名 → 检查引用者签名是否包含节点签名（branchScopeContains）。
			if len(c.branchScopes[n.ID]) == 0 || !branchScopeContains(c.branchScopes[refPos], c.branchScopes[n.ID]) {
				c.generalHoists[n.ID] = true
				break
			}
		}
	}
	for id := range c.generalHoists {
		c.hoistVars = append(c.hoistVars, varName(id))
		if n := c.g.nodes[id]; n != nil {
			if t := c.nodeHoistType(n); t != "" {
				if c.hoistTypes == nil {
					c.hoistTypes = map[string]string{}
				}
				c.hoistTypes[id] = t
				// dify runtime 缺省值安全网显式化（DIFY-DECOMPILE §0）:
				// 提升变量可能"本路径未执行"（conditional_parallel）,反编译
				// 产物补类型零值初始,使 definite-init 通过且语义对齐 dify
				// runtime 的 null 兜底。无法生成零值（any/file/未知）→ 保持
				// 无初值（var v; 裸声明 definite-init 豁免）。
				if zv := c.typeZeroValue(t); zv != nil {
					if c.hoistZero == nil {
						c.hoistZero = map[string]cd.Expr{}
					}
					c.hoistZero[id] = zv
				}
			}
		}
	}
	// 提升变量是函数级作用域（顶层 `var vC;` / 组变量声明）——注册进 declared
	// 持久基集,使分支之后（answer/end/聚合器）的引用可解析。convertIfElse 的
	// snapshot/restore 快照在基集之后取,分支隔离不会抹掉提升变量（03 实证:
	// answer 引用分支内节点 llm-zh,若不注册基集 → 引用解析空 → return ""）。
	c.seedHoistDeclared()
}

// seedHoistDeclared 把提升变量注册进 declared 基集（hoistVarStmts 发出的
// 顶层 var 声明,函数任意位置可见）。
func (c *converter) seedHoistDeclared() {
	if len(c.hoistVars) == 0 {
		return
	}
	if c.declared == nil {
		c.declared = map[string]bool{}
	}
	for _, name := range c.hoistVars {
		c.declared[name] = true
	}
}

// nodeHoistType 提升节点 → ncoda 类型串。var v<id>: <type>; 声明带类型,
// 否则提升后丢失输出形状,下游字段访问报 "Field access requires a proven
// record shape, not any"（完蛋语料实证）。推导自节点输出契约:
//   - record 形状（llm/code/http/agent/tool/parameter-extractor）合成命名
//     类型 Hoist<id> 加入 extraTypes（ncoda 变量声明不支持内联 record,
//     类型必须命名后前置引用）;usage 嵌套 record 共享命名类型 LLMUsage
//     （addExtraType 按名去重,多 llm 提升不重复声明）。
//   - template-transform 单输出 string;list-operator → array<any>
//     （filter 结果,元素形状不跨分支断言）。
//   - 未覆盖节点（document-extractor 等）→ ""（保持无类型 var v;）。
//
// typeZeroValue 类型串 → 零值表达式（dify runtime 缺省值安全网显式化,见
// docs/DIFY-DECOMPILE.md §0）。递归构造:内建类型直接返回,命名 record 类型
// 从 extraTypes 查字段递归;array → 空列表,map → 空字典。无法生成零值
// （any/file/未知类型）→ nil（该提升变量保持无初值,原行为）。
func (c *converter) typeZeroValue(t string) cd.Expr {
	if strings.HasSuffix(t, "?") {
		return &cd.LiteralExpr{Value: nil}
	}
	switch t {
	case "string":
		return &cd.LiteralExpr{Value: ""}
	case "int":
		return &cd.LiteralExpr{Value: 0}
	case "bool":
		return &cd.LiteralExpr{Value: false}
	case "any":
		return nil // 无法生成零值,保持无初值
	case "map<string, any>":
		return &cd.DictExpr{}
	default:
		if strings.HasPrefix(t, "array<") {
			return &cd.ListExpr{}
		}
		// 命名 record 类型 → 查 extraTypes 字段递归。
		for _, td := range c.extraTypes {
			if td.Name == t {
				d := &cd.DictExpr{}
				for _, f := range td.Fields {
					fv := c.typeZeroValue(f.Type)
					if fv == nil {
						return nil
					}
					// 字段名经 EscapeIdent 转义（关键字 → $type）;DictExpr
					// 键是原始契约名（词法剥 '$' 还原）,剥离前缀还原。
					d.Keys = append(d.Keys, strings.TrimPrefix(f.Name, "$"))
					d.Values = append(d.Values, fv)
				}
				return d
			}
		}
		return nil
	}
}

func (c *converter) nodeHoistType(n *nodeInfo) string {
	switch n.Type {
	case "llm":
		c.addExtraType(&cd.TypeDecl{Name: "LLMUsage", Fields: []cd.TypeField{
			{Type: "int?", Name: "prompt_tokens"},
			{Type: "int?", Name: "completion_tokens"},
			{Type: "int?", Name: "total_tokens"},
		}})
		name := "Hoist" + sanitizeID(n.ID)
		c.addExtraType(&cd.TypeDecl{Name: name, Fields: []cd.TypeField{
			{Type: "string", Name: "text"},
			{Type: "string?", Name: "reasoning_content"},
			{Type: "LLMUsage?", Name: "usage"},
			{Type: "string?", Name: "finish_reason"},
		}})
		return name
	case "code":
		outs := asMap(n.Data["outputs"])
		if len(outs) == 0 {
			return "any"
		}
		name := "Hoist" + sanitizeID(n.ID)
		td := &cd.TypeDecl{Name: name}
		for _, k := range sortedKeys(outs) {
			om := asMap(outs[k])
			td.Fields = append(td.Fields, cd.TypeField{Type: c.difyCodeTypeToNcoda(asStr(om["type"])), Name: render.EscapeIdent(k)})
		}
		c.addExtraType(td)
		return name
	case "parameter-extractor":
		// extract<Extract<id>> 结果信封（extraction_contract.go 对齐）:
		// { value: T; ok: bool; reason: string; usage: map<string,any> }。
		// 字段访问走 v<id>.value.<field>,必须带 value record 形状。
		// Extract<id> 先注册（Hoist 引用它,TypeDecl 渲染按注册序）。
		extractName := "Extract" + sanitizeID(n.ID)
		etd := &cd.TypeDecl{Name: extractName}
		for _, rp := range asSlice(n.Data["parameters"]) {
			pm := asMap(rp)
			nm := asStr(pm["name"])
			if nm == "" {
				continue
			}
			etd.Fields = append(etd.Fields, cd.TypeField{Type: difyParamTypeToNcoda(asStr(pm["type"])), Name: nm})
		}
		etd.Fields = append(etd.Fields,
			cd.TypeField{Type: "int", Name: "__is_success"},
			cd.TypeField{Type: "string", Name: "__reason"},
			cd.TypeField{Type: "any", Name: "__usage"},
		)
		c.addExtraType(etd)
		name := "Hoist" + sanitizeID(n.ID)
		c.addExtraType(&cd.TypeDecl{Name: name, Fields: []cd.TypeField{
			{Type: extractName, Name: "value"},
			{Type: "bool", Name: "ok"},
			{Type: "string", Name: "reason"},
			{Type: "map<string, any>", Name: "usage"},
		}})
		return name
	case "http":
		name := "Hoist" + sanitizeID(n.ID)
		c.addExtraType(&cd.TypeDecl{Name: name, Fields: []cd.TypeField{
			{Type: "string", Name: "body"},
			{Type: "int", Name: "status_code"},
			{Type: "map<string, any>", Name: "headers"},
			{Type: "array<file>", Name: "files"},
		}})
		return name
	case "agent":
		name := "Hoist" + sanitizeID(n.ID)
		c.addExtraType(&cd.TypeDecl{Name: name, Fields: []cd.TypeField{
			{Type: "string", Name: "text"},
		}})
		return name
	case "template-transform":
		return "string"
	case "list-operator":
		return "array<any>"
	case "tool":
		// tool 输出 record 来自白名单（tool_registry knownToolOutputs）;
		// 字段未知 → 该字段 any,record 形状保证下游 .field 可解析。
		m := knownToolOutputs[asStr(n.Data["tool_name"])]
		if len(m) == 0 {
			return "any"
		}
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		name := "Hoist" + sanitizeID(n.ID)
		td := &cd.TypeDecl{Name: name}
		for _, k := range keys {
			td.Fields = append(td.Fields, cd.TypeField{Type: m[k], Name: render.EscapeIdent(k)})
		}
		c.addExtraType(td)
		return name
	default:
		return ""
	}
}

// branchScopeContains outer 是否覆盖 inner 的全部分支嵌套（inner ⊆ outer）。
func branchScopeContains(outer, inner map[string]string) bool {
	for ifID, handle := range inner {
		if outer[ifID] != handle {
			return false
		}
	}
	return true
}

// collectRefsInData 递归收集节点 data 里的引用:selector 数组 [nodeID, key]
// 与模板串 {{#nodeID.key#}}。引用位置 = 数据所属节点 id。
func collectRefsInData(posID string, v any, refs map[string]map[string]bool) {
	switch x := v.(type) {
	case []any:
		if len(x) >= 1 {
			if id, ok := x[0].(string); ok {
				if refs[id] == nil {
					refs[id] = map[string]bool{}
				}
				refs[id][posID] = true
			}
		}
		for _, item := range x {
			collectRefsInData(posID, item, refs)
		}
	case map[string]any:
		for _, item := range x {
			collectRefsInData(posID, item, refs)
		}
	case string:
		for _, m := range templateRefRe.FindAllStringSubmatch(x, -1) {
			segs := strings.Split(m[1], ".")
			if len(segs) == 0 || segs[0] == "" {
				continue
			}
			if refs[segs[0]] == nil {
				refs[segs[0]] = map[string]bool{}
			}
			refs[segs[0]][posID] = true
		}
	}
}

// ── agent（dify Agent 节点 → 内建 agent()）──

// convertAgent: let v<id> = agent(MODEL_<i>, <query>, {instruction, tools});
// agent_parameters 各键是 dify "constant" 信封 {type, value}:展开取 value。
// query 模板 {{#sys.query#}} → 合成参数;tools → {provider_name, tool_name}
// 最小列表（config 是 any map,不捏造 schema）。输出端口 text 与内建
// agent 结果契约一致（下游 v<id>.text 可解析）。
func (c *converter) convertAgent(n *nodeInfo) []cd.Stmt {
	data := n.Data
	ap := asMap(data["agent_parameters"])
	mm := asMap(unwrapParam(ap["model"]))
	cn := c.modelConstRef(asStr(mm["provider"]), asStr(mm["name"]))
	query := c.paramValueExpr(ap["query"])
	cfg := &cd.DictExpr{}
	if inst, ok := unwrapParam(ap["instruction"]).(string); ok && inst != "" {
		dictPut(cfg, "instruction", &cd.LiteralExpr{Value: inst})
	}
	if tools := asSlice(unwrapParam(ap["tools"])); len(tools) > 0 {
		tl := &cd.ListExpr{}
		for _, rt := range tools {
			tm := asMap(rt)
			toolsDict := &cd.DictExpr{}
			if pn := asStr(tm["provider_name"]); pn != "" {
				dictPut(toolsDict, "provider_name", &cd.LiteralExpr{Value: pn})
			}
			if tn := asStr(tm["tool_name"]); tn != "" {
				dictPut(toolsDict, "tool_name", &cd.LiteralExpr{Value: tn})
			}
			tl.Items = append(tl.Items, toolsDict)
		}
		dictPut(cfg, "tools", tl)
	}
	return []cd.Stmt{&cd.VarDecl{Name: varName(n.ID), Init: &cd.CallExpr{
		Callee: "agent",
		Args:   []cd.Expr{&cd.IdentExpr{Name: cn}, query, cfg},
	}}}
}

// ── question-classifier（dify 问题分类 → ncoda switch 多出口）──

// convertQuestionClassifier: question-classifier（多出口）→ ncoda switch 语句。
// 形状契约（与 oracle27 classify 一致，G005 正编译的逆投影）：
//
//	switch ( <query> ) {
//	    "<class_id>": { ...分支体... }
//	    ...
//	}
//
// 每个 class 一条分支边（sourceHandle = class id，P4 端口显式）;分支体从
// 分支入口转换到汇聚点（variable-aggregator / end）之前;汇聚点之后由外层
// 继续转换（与 convertIfElse 同构）。不再输出 classify<QClass> 值形式
// （classify 值语义已删除，G003）。
func (c *converter) convertQuestionClassifier(n *nodeInfo) []cd.Stmt {
	data := n.Data
	query := c.selectorExpr(asSlice(data["query_variable_selector"]))
	if query == nil {
		query = &cd.LiteralExpr{Value: ""}
	}
	classes := asSlice(data["classes"])
	// 分支入口 + 汇聚点。
	type branch struct {
		label string
		ents  []string
	}
	var branchEnts []string
	var branches []branch
	for _, rc := range classes {
		cm := asMap(rc)
		id := asStr(cm["id"])
		if id == "" {
			continue
		}
		ents := c.g.succ(n.ID, id)
		if len(ents) == 0 {
			// 悬挂分支（canvas 声明 class 但无对应分支边）→ FAILED 标记。
			branches = append(branches, branch{label: id})
			continue
		}
		branchEnts = append(branchEnts, ents...)
		branches = append(branches, branch{label: id, ents: ents})
	}
	conv := ""
	if len(branchEnts) >= 2 {
		conv = c.convergence(branchEnts)
	}
	save := c.barrier
	isTerminal := conv != "" && c.isTerminalNode(conv)
	if conv != "" && !isTerminal {
		c.barrier = conv
	}
	sw := &cd.Switch{Query: query}
	snap := c.snapshotDeclared()
	for _, br := range branches {
		if len(br.ents) == 0 {
			sw.Branches = append(sw.Branches, cd.SwitchBranch{Label: br.label, Dangling: true})
			continue
		}
		blk := &cd.Block{Stmts: c.branchStmts(br.ents)}
		c.restoreDeclared(snap)
		sw.Branches = append(sw.Branches, cd.SwitchBranch{Label: br.label, Body: blk})
	}
	c.barrier = save
	var out []cd.Stmt
	out = append(out, sw)
	if conv != "" && conv != n.ID && !isTerminal {
		out = append(out, c.convert(conv)...)
	}
	return out
}

// isIdentSafe 报告 s 是否为合法 ncoda 标识符（[a-zA-Z_][a-zA-Z0-9_]*）。

// ---- human-input (dify HITL -> ncoda ask) ----

// convertHumanInput: human-input -> ncoda ask stmt.
// data: form_content, inputs[], user_actions[], timeout, timeout_unit.
// branch edges: action_N -> branch body; __timeout -> timeout body (optional).
// dangling action (no outgoing edge) -> FAILED marker (same as switch Dangling).
func (c *converter) convertHumanInput(n *nodeInfo) []cd.Stmt {
	data := n.Data
	// 1. content
	content := asStr(data["form_content"])
	useDefaultContent := false
	if content == "" {
		content = n.Title
		if content == "" {
			content = "ask"
		}
		useDefaultContent = true
	}
	// 2. inputs -> fields
	rawInputs := asSlice(data["inputs"])
	fields := make([]cd.RequestInputField, 0, len(rawInputs))
	for _, ri := range rawInputs {
		im := asMap(ri)
		fname := asStr(im["output_variable_name"])
		if fname == "" {
			continue
		}
		ftype := asStr(im["type"])
		kind := "paragraph"
		if ftype == "select" {
			kind = "select"
		}
		opts := asSlice(im["options"])
		options := make([]string, 0, len(opts))
		for _, o := range opts {
			if s, ok := o.(string); ok {
				options = append(options, s)
			}
		}
		fields = append(fields, cd.RequestInputField{Name: fname, Kind: kind, Options: options})
	}
	// 3. user_actions -> actions
	rawActions := asSlice(data["user_actions"])
	actions := make([]cd.RequestInputAction, 0, len(rawActions))
	for _, ra := range rawActions {
		am := asMap(ra)
		mid := asStr(am["id"])
		if mid == "" {
			continue
		}
		title := asStr(am["title"])
		style := asStr(am["button_style"])
		actions = append(actions, cd.RequestInputAction{Member: mid, Title: title, Style: style})
	}
	// 4. timeout
	timeoutVal := 0
	timeoutUnit := ""
	if tv, ok := data["timeout"].(int); ok && tv > 0 {
		timeoutVal = tv
		timeoutUnit = asStr(data["timeout_unit"])
	}
	// 5. type names
	shortID := n.ID
	formTypeName := "HitlForm" + shortID
	actionTypeName := "HitlAction" + shortID
	// 6. FormType record decl
	ft := &cd.TypeDecl{
		Name:   formTypeName,
		Fields: make([]cd.TypeField, 0, len(fields)),
	}
	for _, f := range fields {
		ftype := "string"
		if f.Kind == "select" && len(f.Options) > 0 {
			optEnumName := formTypeName + "_" + f.Name + "Option"
			members := make([]string, len(f.Options))
			for i, opt := range f.Options {
				members[i] = opt
			}
			c.extraEnums = append(c.extraEnums, &cd.EnumDecl{Name: optEnumName, Members: members})
			ftype = optEnumName
		}
		ft.Fields = append(ft.Fields, cd.TypeField{Name: f.Name, Type: ftype})
	}
	c.extraTypes = append(c.extraTypes, ft)
	// 7. ActionType enum decl
	actionMembers := make([]string, 0, len(actions))
	for _, a := range actions {
		actionMembers = append(actionMembers, a.Member)
	}
	c.extraEnums = append(c.extraEnums, &cd.EnumDecl{Name: actionTypeName, Members: actionMembers})
	// 8. result binding name
	resultBinding := varName(n.ID)
	// 9. branch infos
	type branchInfo struct {
		member string
		ents   []string
	}
	var branchInfos []branchInfo
	var timeoutEnts []string
	for _, a := range actions {
		ents := c.g.succ(n.ID, a.Member)
		if len(ents) == 0 {
			branchInfos = append(branchInfos, branchInfo{member: a.Member})
			c.noteUnknown("human-input-dangling:" + a.Member)
			continue
		}
		branchInfos = append(branchInfos, branchInfo{member: a.Member, ents: ents})
	}
	timeoutEnts = c.g.succ(n.ID, "__timeout")
	// convergence
	var allBranchEnts []string
	for _, bi := range branchInfos {
		allBranchEnts = append(allBranchEnts, bi.ents...)
	}
	allBranchEnts = append(allBranchEnts, timeoutEnts...)
	conv := ""
	if len(allBranchEnts) >= 2 {
		conv = c.convergence(allBranchEnts)
	}
	save := c.barrier
	isTerminal := conv != "" && c.isTerminalNode(conv)
	if conv != "" && !isTerminal {
		c.barrier = conv
	}
	ask := &cd.RequestInputStmt{
		FormType:      formTypeName,
		ActionType:    actionTypeName,
		Content:       &cd.LiteralExpr{Value: content},
		Fields:        fields,
		Actions:       actions,
		TimeoutValue:  timeoutVal,
		TimeoutUnit:   timeoutUnit,
		ResultBinding: resultBinding,
	}
	snap := c.snapshotDeclared()
	for _, bi := range branchInfos {
		blk := &cd.Block{}
		if len(bi.ents) == 0 {
			// 悬挂 action:无出边（dify 点击后流程终止）→ return;（void 终止）
			blk.Stmts = []cd.Stmt{&cd.Return{Value: nil}}
		} else {
			if c.askActionRef == nil {
				c.askActionRef = map[string]string{}
			}
			c.askActionRef[n.ID] = bi.member
			blk.Stmts = c.branchStmts(bi.ents)
			delete(c.askActionRef, n.ID)
		}
		c.restoreDeclared(snap)
		ask.ActionBranches = append(ask.ActionBranches, cd.RequestInputBranch{Member: bi.member, Body: blk})
	}
	if len(timeoutEnts) > 0 {
		if c.askActionRef == nil {
			c.askActionRef = map[string]string{}
		}
		// timeout 分支:ask 结果（表单/__action_id）全部为空（超时无提交）
		c.askActionRef[n.ID] = ""
		ask.TimeoutBody = &cd.Block{Stmts: c.branchStmts(timeoutEnts)}
		delete(c.askActionRef, n.ID)
	}
	c.restoreDeclared(snap)
	c.barrier = save
	var out []cd.Stmt
	if useDefaultContent {
		c.noteUnknown("human-input-empty-content:form_content empty, use node title as content")
	}
	out = append(out, ask)
	if conv != "" && conv != n.ID && !isTerminal {
		out = append(out, c.convert(conv)...)
	}
	return out
}

func isIdentSafe(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') {
				return false
			}
			continue
		}
		if r != '_' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// unwrapParam 展开 dify 参数信封 {type, value} → value（保持其他值原样）。
func unwrapParam(v any) any {
	if m, ok := v.(map[string]any); ok {
		if t := asStr(m["type"]); t != "" {
			if _, hasVal := m["value"]; hasVal {
				return m["value"]
			}
		}
	}
	return v
}

// paramValueExpr 参数信封值 → ncoda 表达式（模板插值 / 字面量）。
func (c *converter) paramValueExpr(v any) cd.Expr {
	v = unwrapParam(v)
	if s, ok := v.(string); ok {
		if strings.Contains(s, "{{#") {
			return c.renderParamTemplate(s)
		}
		return &cd.LiteralExpr{Value: s}
	}
	return &cd.LiteralExpr{Value: v}
}

// renderParamTemplate 参数模板:纯插值 {{#conversation.X#}} 且 X 为
// 非标量会话变量（array/object）→ 保留 ${expr} 模板串形态（运行时字符串
// 化,dify 模板渲染语义——agent 的 query 字段把数组历史转成文本）;
// 其余走 renderTemplate 通用路径（纯标量插值 → 裸表达式）。
func (c *converter) renderParamTemplate(tmpl string) cd.Expr {
	if m := templateRefRe.FindStringSubmatch(tmpl); m != nil && templateRefRe.FindStringIndex(tmpl)[1] == len(tmpl) {
		segs := strings.Split(m[1], ".")
		if len(segs) == 2 && segs[0] == "conversation" && c.convVarIsScalar(segs[1]) == false {
			ts := &cd.TemplateString{}
			ts.Parts = append(ts.Parts, cd.TemplatePart{Expr: c.refExpr("conversation", segs[1], nil)})
			return ts
		}
	}
	return c.renderTemplate(tmpl)
}

// convVarIsArray 会话变量是否为数组类型（append → << 分派）。
// 非会话变量（循环变量等）→ false（走 + 拼接路径）。
func (c *converter) convVarIsArray(name string) bool {
	return convVarKind(c.convVarTypes[name]) == "array"
}

// convVarIsScalar 会话变量是否为标量（string/number/bool/file）。
// 非会话变量 → 返回 true（走裸表达式路径,不额外包裹）。
func (c *converter) convVarIsScalar(name string) bool {
	switch convVarKind(c.convVarTypes[name]) {
	case "array", "any":
		return false
	}
	return true
}
