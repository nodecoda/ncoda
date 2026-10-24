package nodes

// std.go — 正式实现的注册入口（M2.1）。
//
// StdRegistry 返回注册了全部正式节点的 registry（compile 层与 CLI 直接
// 用）。M2.2 起含 I/O 节点 http/llm/tool/qa —— 它们的 Build 需要 Deps
// （HTTP/Models/Tools）;缺依赖时编译报 RNodeError。不含 loop（compile
// 内建驱动器）。

// StdRegistry builds a registry with every formal pure-function node.
func StdRegistry() *Registry {
	r := NewRegistry()
	for _, b := range StdBuilders() {
		if err := r.Register(b); err != nil {
			panic(err) // static set — programmer error if a kind collides
		}
	}
	return r
}

// StdBuilders lists the formal node builders (M2.3 set: 含 agent)。
func StdBuilders() []NodeBuilder {
	return []NodeBuilder{
		Start{}, End{}, Text{}, Aggregate{}, Selector{},
		Assign{}, LoopAssign{}, ListOp{}, Output{},
		Code{}, Knowledge{}, Break{}, Continue{}, LoopReturn{},
		HTTP{}, LLM{}, Tool{}, QA{}, Switch{}, Agent{}, Conversation{},
		SubWorkflow{},
	}
}
