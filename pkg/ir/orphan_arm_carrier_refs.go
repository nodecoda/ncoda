package ir

// dropOrphanArmCarrierRefs 清掉**尝试臂终端 End**（`AttemptStmt.SuccessEnd` /
// `FailureEnd`）里**没有接收方**的引用。
//
// 一条引用的两头 = 生产者 + 接收方。臂终端 End 的 `Inputs` 语义是「这条臂逃出的
// 值」，它在 IR 里的**唯一接收方通道**是该语句的汇聚单元（`AttemptStmt.Exit` 的
// 候选槽 / 逐变量 phi 组）。值整体无人消费时汇聚单元被判死摘掉（`optimizeGraph`），
// 这条引用就**没有接收方**了；渲染器也不发射它 —— 臂终端在产物里是**端口边**的
// 终点（P4 / P15），不是数据绑定。
//
// 为什么必须清掉、而不是留给每个下游各自「打折」：树级读数（反射遍历整棵 IR，
// 例 `llmprojection.UsesEnvelope`）会把「没有接收方的引用」算成消费者。
//
// 一手（2026-09-23，R-04）：生成器 `gen_parallel_d3_s3_seed0` 的臂尾是 llm 的整值
// 信封（`let v123 = llm(…)` 无人读）⇒ 信封被判「被读」⇒ 渲染器合成出一个
// **没人读**的信封 code 节点（`<llm>__llm_envelope`）⇒ `compile(r0)` 84 节点、
// `compile(decompile(compile(r0)))` 83 节点（裁掉的是它）⇒
// `ARTIFACT_DRIFT/TOPOLOGY@r2`。最小形 6 → 5 节点，见同族判据
// `internal/compiler/cozebiz_arm_carrier_liveness_contract_test.go` 与
// `platforms/cozebiz/decompile/arm_carrier_liveness_contract_test.go`。
//
// 判据复用**既有那把尺**，不另写一份：「引用普查」（`collectIRConsumers`，臂终端
// Inputs 计入）减去「臂终端载体站点」（`armCarrierSites`）= **真实读者**；差值为 0
// ⇒ 该生产者的这条引用没有接收方 ⇒ 去掉。
//
// 同族既有三次落地（同一把尺、方向一致，都是在下游「打折」而不是去掉引用）：
// `prune_dead_pure_nodes.go::armCarrierSites` · `hoist_zero_arm_carriers.go::nonCarrierSites`
// · `whole_value_refs.go::attemptScaffoldEndIDs`。本函数是第一次**把引用本身去掉**，
// 于是包括反射遍历在内的所有下游读数自动一致（一处事实，一处承载）。
func dropOrphanArmCarrierRefs(g *Graph) {
	if g == nil || g.Root == nil {
		return
	}
	nodes := collectIRNodes(g)
	consumers := collectIRConsumers(g, nodes)
	carriers := armCarrierSites(g)
	forEachStmt(g.Root, func(s Stmt) {
		a, ok := s.(*AttemptStmt)
		if !ok {
			return
		}
		for _, e := range []*End{a.SuccessEnd, a.FailureEnd} {
			if e == nil || len(e.Inputs) == 0 {
				continue
			}
			kept := make([]Ref, 0, len(e.Inputs))
			for _, r := range e.Inputs {
				if r.Producer != "" && nonCarrierSites(consumers, carriers, r.Producer) == 0 {
					continue
				}
				kept = append(kept, r)
			}
			if len(kept) == 0 {
				kept = nil
			}
			e.Inputs = kept
		}
	})
}
