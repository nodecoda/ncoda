package astnodes

// Connection 是强类型 AST 的控制流连接（对应 ncoda_coze.ast_nodes.connection.Connection）。
type Connection struct {
	FromNode string
	ToNode   string
	FromPort string
	ToPort   string
}

// Identity 返回连接身份：有端口时含端口，否则仅节点对。
func (c Connection) Identity() string {
	if c.FromPort != "" {
		return c.FromNode + ":" + c.ToNode + ":" + c.FromPort
	}
	return c.FromNode + ":" + c.ToNode
}

// Branch 是选择器某一输出端口对应的下游分支。
type Branch struct {
	Port    string
	Targets []string
}
