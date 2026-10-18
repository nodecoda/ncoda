package astnodes

// SELECTOR_OPERATORS 是 Selector 算子的 Canvas 数字 ID -> 运行时算子字符串映射。
var SelectorOperators = map[int]string{
	1:  "=",
	2:  "!=",
	3:  "len >",
	4:  "len >=",
	5:  "len <",
	6:  "len <=",
	7:  "contain",
	8:  "not_contain",
	9:  "empty",
	10: "not_empty",
	11: "true",
	12: "false",
	13: ">",
	14: ">=",
	15: "<",
	16: "<=",
}

// CompositePorts 是复合节点端口集合：parser normalize_ports 清除这些端口，exporter 需重建。
var CompositePorts = map[string]struct{}{
	"loop-function-inline-output":  {},
	"loop-output":                  {},
	"batch-function-inline-output": {},
	"batch-output":                 {},
}

// IsCompositePort 判断端口是否属于复合节点合成端口。
func IsCompositePort(port string) bool {
	_, ok := CompositePorts[port]
	return ok
}
