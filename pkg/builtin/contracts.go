package builtin

// contracts.go — 契约表的注册入口。
//
// 生成物按许可侧拆成两份（O2 开放内核拆分）：
//
//	contracts_dify_gen.go  开放侧（dify）—— 随开放仓发布
//	contracts_coze_gen.go  闭源侧（coze 家族）—— //go:build !dify
//
// 两份文件都在 init() 里调用 registerContracts，合并成**同一张表**：查询
// 入口（Contract / NodeOf / PortsOf / …）与语言层、各后端读到的仍是同一份
// 数据。注册期查重 —— 同一 target 出现在两侧是生成器 bug，必须当场炸掉，
// 而不是让后注册的一方静默覆盖（那会让闭源契约在开放构建里「意外存在」）。

// contracts 是按目标平台索引的内建节点结果契约（由各生成物注册）。
var contracts = map[string]map[string]NodeContract{}

// registerContracts 合并一侧生成物；重复 target ⇒ panic（生成器应已拦住）。
func registerContracts(src map[string]map[string]NodeContract) {
	for target, nodes := range src {
		if _, dup := contracts[target]; dup {
			panic("builtin: duplicate contract target " + target)
		}
		contracts[target] = nodes
	}
}
