package astnodes

// WorkflowAST 工作流 AST（对应 ncoda_coze.ast_nodes.nodes.WorkflowAST）。
type WorkflowAST struct {
	Nodes          map[string]NodeDecl
	Connections    []Connection
	Branches       map[string][]Branch
	Hierarchy      map[string]string
	GeneratedNodes []string
	EntryKey       string
	ExitKey        string
	TerminatePlan  TerminatePlan
	SymbolTable    SymbolTable
	// PrunedNodes: 解析边界剪掉的**孤立节点**（`parser.PruneIsolatedNodes`：
	// 没有任何控制流入边、也不是 Entry/Exit、也没有被数据引用指向）。它们连
	// Nodes 都进不去 —— 没有这张表，「平台画布上有、产物里没有」在最外层就是
	// **静默**的（反编译器承诺读任意平台导出 ⇒ 丢的每一条都要有归因）。
	// 带 Type 是因为**归因要看类型**（注释框与业务节点不是同一回事：注释本来
	// 就不接线、零执行语义，平台里它们绝大多数就是浮空的）。
	// 唯一消费者 = 反编译层的节点处置 trace（`NodeTrace` → RS-TRACE-001）。
	PrunedNodes []PrunedNode
}

// PrunedNode 一个被解析边界剪掉的孤立节点（id + 平台节点类型）。
type PrunedNode struct {
	ID   string
	Type NodeType
}

// NewWorkflowAST 构造空工作流 AST。
func NewWorkflowAST() *WorkflowAST {
	return &WorkflowAST{
		Nodes:         map[string]NodeDecl{},
		Connections:   []Connection{},
		Branches:      map[string][]Branch{},
		Hierarchy:     map[string]string{},
		EntryKey:      EntryNodeKey,
		ExitKey:       ExitNodeKey,
		TerminatePlan: TerminatePlanReturnVariables,
	}
}

// GetNode 获取节点。
func (w *WorkflowAST) GetNode(key string) NodeDecl {
	return w.Nodes[key]
}

// Successors 返回后继节点 key 列表。
func (w *WorkflowAST) Successors(key string) []string {
	out := []string{}
	for _, c := range w.Connections {
		if c.FromNode == key {
			out = append(out, c.ToNode)
		}
	}
	return out
}

// Predecessors 返回前驱节点 key 列表。
func (w *WorkflowAST) Predecessors(key string) []string {
	out := []string{}
	for _, c := range w.Connections {
		if c.ToNode == key {
			out = append(out, c.FromNode)
		}
	}
	return out
}

// SameWorkflowLevel 判断两个节点是否同一工作流层级。
func (w *WorkflowAST) SameWorkflowLevel(left, right string) bool {
	return w.Hierarchy[left] == w.Hierarchy[right]
}

// CompositeChildren 返回复合节点的子节点。
func (w *WorkflowAST) CompositeChildren(parent string) []NodeDecl {
	out := []NodeDecl{}
	for key, owner := range w.Hierarchy {
		if owner == parent {
			if n, ok := w.Nodes[key]; ok {
				out = append(out, n)
			}
		}
	}
	return out
}

// SymbolTableIface 符号表接口（由 scope.SymbolTable 实现，避免包循环依赖）。
type SymbolTableIface interface {
	IsReachable(sourceNode, targetNode string) bool
	CanReference(sourceNode, targetNode string) bool
}

// SymbolTable 编译期符号表（scope 包实现；通过接口持有）。
type SymbolTable SymbolTableIface
