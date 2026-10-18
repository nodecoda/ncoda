package decompile

// decompile.go —— dify 反编译器入口（dify YAML → ncoda 源码）。

import (
	"fmt"
	"sort"
	"strings"

	cd "github.com/nodecoda/ncoda/pkg/ncoda/ast4"
	"github.com/nodecoda/ncoda/pkg/ncoda/render"
)

// DecompileYAML 反编译 dify 工作流 YAML → ncoda 源码文本。
//
// 产出形态:
//
//	@language nodecoda/1
//	@mode workflow
//
//	const MODEL_1 = "openai/gpt-4o";
//
//	function main(string query) {
//	    let vllm_1 = llm(MODEL_1, {"messages": [...], ...});
//	    return vllm_1.text;
//	}
//
// 覆盖（47 语料全过,UNKNOWN 节点仅剩 human-input 与无实例类型,显式
// 标记绝不静默丢弃——与 coze-biz 反编译同契约;完整清单见
// docs/DIFY-DECOMPILE.md）:start/llm/if-else/code/tool/answer/end/
// template-transform/loop/iteration/assigner/parallel 扇出/http-request/
// knowledge-retrieval/variable-aggregator/parameter-extractor。
func DecompileYAML(text string) (string, error) {
	g, err := parseYAML(text)
	if err != nil {
		return "", err
	}
	mode := g.mode
	if mode == "" {
		mode = "workflow"
	}
	c := &converter{g: g, startID: g.start.ID, used: map[string]bool{}}
	c.collectStartVars()
	c.collectEnvVars()
	c.collectConvVars()
	// 引用预扫描（collectGeneralHoists 用 nodeRefs 判提升;未引用产值 → 裸语句）。
	c.collectRefs()
	c.collectAggGroups()
	// 转换主体先跑:发现全部合成参数（sys.* 引用 → extraParams）后,再按
	// @mode advanced-chat 的 CHATFLOW_ENTRY 契约定最终 mode——入口首参
	// 必须叫 query（语义 pass M13）。dify advanced-chat 的 start 变量/
	// sys 字段若首个不是 query（自定义表单字段/sys.dialogue_count 等）,
	// 降级为 @mode workflow——会话变量随之退化为 var <name>; 近似
	// （记覆盖清单,见 docs/DIFY-DECOMPILE.md）。
	body := c.convert(g.start.ID)
	ps := append(c.extraParams, c.startParams()...)
	if mode == "advanced-chat" {
		first := ""
		if len(ps) > 0 {
			first = strings.TrimSpace(strings.SplitN(ps[0], " ", 2)[1])
		}
		if first != "" && first != "query" {
			mode = "workflow"
		}
	}
	c.mode = mode
	stmts := append(c.convVarStmts(), c.hoistVarStmts()...)
	stmts = append(stmts, body...)

	var b strings.Builder
	b.WriteString("@language nodecoda/1\n")
	fmt.Fprintf(&b, "\n@mode %s\n", mode)
	// @conversation 声明紧跟 @mode（parser 顶层顺序:@language/@mode →
	// @conversation → import/const/type/enum/function）。
	for _, decl := range c.conversationDecls() {
		fmt.Fprintf(&b, "\n%s\n", decl)
	}
	for _, imp := range c.imports {
		fmt.Fprintf(&b, "\nimport %q;\n", imp)
	}
	for _, mc := range c.models {
		fmt.Fprintf(&b, "\nconst %s = %q;\n", mc.ConstName, mc.Model)
	}
	// 环境变量常量（dify environment_variables → 只读 const;值字面量
	// 渲染与模型 const 同层,名字 $ 转义由 EscapeIdent 处理——关键字名
	// （如 env 变量叫 type）写 $type）。
	for _, ec := range c.envConsts {
		fmt.Fprintf(&b, "\nconst %s = %s;\n", render.EscapeIdent(ec.Name), render.RenderNCodaExpr(&cd.LiteralExpr{Value: ec.Value}))
	}
	// 合成 record 类型声明（parameter-extractor → extract<T> 的 T）。
	for _, td := range c.extraTypes {
		b.WriteString("\n")
		b.WriteString(render.RenderTypeDecl(td))
		b.WriteString("\n")
	}
	// 合成枚举声明（question-classifier → classify<E> 的 E）。
	for _, ed := range c.extraEnums {
		b.WriteString("\n")
		b.WriteString(render.RenderEnumDecl(ed))
		b.WriteString("\n")
	}
	// 合成 sys.* 参数在前（黄金对照 query 首参约定）,start 变量随后。
	params := append(c.extraParams, c.startParams()...)
	paramStr := "(" + strings.Join(params, ", ") + ")"
	fmt.Fprintf(&b, "\nfunction main%s {\n", paramStr)
	// RenderNCodaStmt 自带 pad（indent=2 → 函数体 8 空格,嵌套 if 12/16…）。
	// 不能用额外行级 indent() 包装——那会把 foreign code 模板字面量的
	// 内容也缩进,破坏 Python 源码的列 0 main 检测（CODE_SOURCE_INVALID）。
	for _, st := range stmts {
		b.WriteString(render.RenderNCodaStmt(st, 2))
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	nc := b.String()
	// UNKNOWN 注释块:类型/节点降级归因（L3-b 禁止静默 any/降级,与 coze-biz
	// 反编译同契约）。加在源码末尾,注释零语义影响,compile_ok 门禁不受影响。
	nc += renderUnknownNotes(c.unknownNotes)
	// 自验证软降级:RETURN_TYPE（多出口类型异构）→ `-> any`;
	// 仍 MISSING_RETURN → 追加 `return "";`。见 verify.go。
	if softened, ok := softenMixedReturns(nc, text); ok {
		nc = softened
	}
	return nc, nil
}

// renderUnknownNotes 渲染 UNKNOWN 注释块（反编译类型/节点降级归因,L3-b）。
// 与 coze-biz 反编译 renderUnknownNotes 同契约:按原因聚合,产物尾部显式
// 列出,禁止静默降级。无降级时返回空串（产物与之前逐字节一致）。
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
