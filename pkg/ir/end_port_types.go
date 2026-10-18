package ir

// end_port_types.go — CB-10：端点绑定的**被引用值类型**，建图收尾**一处**盖章。
//
// 为什么不是在各构造点写：End 的两条值通道（`Inputs` / `Outputs`）与答复名额表
// （`Answer.Bindings`）分别由 7 处构造 / 改写（`ensureEnd` / `resolvePendingElse` /
// `materializeFlowExitPhi` / `canonicalizeFlowExitGroupName` …）。在每个构造点各自
// 解析一遍类型 = 「同一规则 N 处实现」——漏一处就是静默丢类型（CB-10 的形态）。
// 故：**建图收尾走一遍**，只认此刻还在树上的引用，类型全部取自唯一权威
// `portTypeOf`（生产者口类型）。
//
// 为什么必须与 `canonicalizeFlowExitGroupName` 同序之后：出口汇聚单元的组名会被
// 重排（R-85），而类型是按「Ref（含 Output 段名）」取的 ⇒ 必须在名字定稿之后盖。
//
// 语义（一手，coze-studio OSS）：平台 `end.node_inputs[*].input.type` 被运行期读
// （`SetInputsForNodeSchema` → `NodeSchema.InputTypes` → `preTypeConverter`：缺失 /
// 空值按声明类型填零、字符串按声明类型归一为数组）⇒ 它是**语义**，不是保真注解。
// ⇒ 我们按语言类型投影发射；取不到 ⇒ **不发射**，绝不虚构 `string`。
func (g *Builder) stampEndPortTypes() {
	for _, n := range g.nodes {
		e, ok := n.(*End)
		if !ok || e == nil {
			continue
		}
		g.stampOneEnd(e)
	}
}

// stampOneEnd 给单个端点盖类型。幂等：已填过的槽不重算（同一 Ref 只会得同一个
// 权威答案，重算只会浪费 portTypeOf 的聚合 / 点号解析路径）。
func (g *Builder) stampOneEnd(e *End) {
	if e.PortTypes == nil {
		e.PortTypes = make(map[Ref]*Type, len(e.Inputs)+len(e.Outputs))
	}
	for _, r := range e.Inputs {
		if _, done := e.PortTypes[r]; !done {
			e.PortTypes[r] = g.portTypeOf(r.Producer, r.Output)
		}
	}
	for _, o := range e.Outputs {
		if _, done := e.PortTypes[o.Ref]; !done {
			e.PortTypes[o.Ref] = g.portTypeOf(o.Ref.Producer, o.Ref.Output)
		}
	}
	if e.Answer == nil {
		return
	}
	for i := range e.Answer.Bindings {
		b := &e.Answer.Bindings[i]
		if b.Type == nil {
			b.Type = g.portTypeOf(b.Ref.Producer, b.Ref.Output)
		}
	}
}
