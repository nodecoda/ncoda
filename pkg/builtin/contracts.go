package builtin

// contracts.go — 契约表的注册入口。
//
// 生成物按许可侧拆开（O2 开放内核拆分）：
//
//	contracts_dify_gen.go  开放侧（dify）—— 本仓，init() 里注册
//	（闭源家族）           闭源侧 —— 商业仓 internal/builtincoze/contracts.go，
//	                       经本包的 RegisterContracts 在 init() 注册
//
// 两侧合并成**同一张表**：查询入口（Contract / NodeOf / PortsOf / …）与语言
// 层、各后端读到的仍是同一份数据。注册期查重 —— 同一 target 出现在两侧是
// 生成器 bug，必须当场炸掉，而不是让后注册的一方静默覆盖（那会让闭源契约在
// 开放构建里「意外存在」）。

// contracts 是按目标平台索引的内建节点结果契约（由各生成物注册）。
var contracts = map[string]map[string]NodeContract{}

// RegisterContracts 合并一侧生成物。开放/闭源拆仓后，闭源 target 的契约表
// 住在下游模块，经此入口在 init() 注册；重复 target 同样 panic。
func RegisterContracts(src map[string]map[string]NodeContract) { registerContracts(src) }

// registerContracts 合并一侧生成物；重复 target ⇒ panic（生成器应已拦住）。
func registerContracts(src map[string]map[string]NodeContract) {
	for target, nodes := range src {
		if _, dup := contracts[target]; dup {
			panic("builtin: duplicate contract target " + target)
		}
		contracts[target] = nodes
	}
}
