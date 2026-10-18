package ir

// canonical_zero_carrier_order.go — IR 优化阶段：**注入零载体在块内的相对序 =
// 规范序**（R-42 收口，2026-09-24；正本 = J §40）。
//
// 现象（X4 一手，J §39.1）：同一个意思的两份源，两条**注入零载体**（零字符串
// 载体 `return {"result": ""}` / 记录零载体 `return {"output": ""}`）的**链序
// 翻转** ⇒ 产物节点序、id、边序跟着翻 ⇒ `canon(c0) != canon(c1)`
// （`ARTIFACT_DRIFT/PARAM@r2`）。
//
// 根因（J §39.3）：反编译侧把 φ 单元的零值声明**整体前置**（`generate.go` 的
// `hoisted = append(aggHoists, hoisted...)`），而编译侧**按源语句序物化载体**
// （P3：边集是 IR 结构树的投影）⇒ 链序跟着**源声明序**走；而「平台导出的别名序」
// 与「我们自产形态的 φ 序」在这一点上不同 ⇒ 一轮瞬态。
//
// 规则（一条，无宿主/形态枚举）：
//
//	同一 Block 内**相邻的**注入零载体（`isZeroCarrierNode`）按**身份键**排序 ——
//	序键 = `zeroCarrierPoolKey`（Code 逐字 + Language + 输出端口声明序），
//	即 P28 用来判定「同键只物化一个节点」的**同一把尺**：键即身份，身份序即规范序。
//
// 为什么这是等价变换（不是近似）：
//
//	· 零载体是**编译器自建的纯常量发射器**（Synthetic ∧ shim ∧ `ZeroOf ≠ nil`
//	  ∧ 无输入）—— 它不携载任何臂值、不捕获任何臂内数据、没有副作用 ⇒ 两条这样
//	  的语句**互换位置**不改变任何值、不改变任何控制流依赖（P1/P2 逐条成立：
//	  位置集合不变 ⇒ 每个载体的生产者仍在它全部消费者的上游）。
//	· 序键是**载体自身的内容键**，不是源声明序、不是名字序、不是 id 序 ⇒ 任何
//	  源序（平台导出 / 自产 / 手写）编译出**同一条链序**。
//	· 与「整块后置」的区别（那个是**反例**，J §39.5 两头否决）：本 pass 只排
//	  **零载体之间**的相对序，不动「派生声明 vs 其余语句」的边界，不碰任何别的
//	  语句的位置。
//
// 域收窄（登记，不掩盖）：本刀只排**相邻**的零载体段（不越过任何非零载体语句）
// —— 这是**可证安全**的最小置换。不相邻的零载体（中间夹着真实语句）尚无一手
// 证据，且越过真实语句需要额外的支配域证明，故不在本刀范围（J §40.3）。
//
// 与 P23 / P27 / P28 同层同尺（都在 coze-biz lowering 入口的 IR 优化阶段收尾）。

// CanonicalZeroCarrierOrder 把每个 Block 内相邻的注入零载体按身份键排序。
// 返回是否发生改动。
func CanonicalZeroCarrierOrder(g *Graph) bool {
	if g == nil || g.Root == nil {
		return false
	}
	changed := false
	forEachBlock(g.Root, func(b *Block) {
		if canonicalizeBlockZeroCarrierRuns(b) {
			changed = true
		}
	})
	return changed
}

// canonicalizeBlockZeroCarrierRuns 处理一个块内的**每一个**极大连续零载体段。
func canonicalizeBlockZeroCarrierRuns(b *Block) bool {
	changed := false
	for i := 0; i < len(b.Stmts); {
		if !isZeroCarrierStmt(b.Stmts[i]) {
			i++
			continue
		}
		j := i
		for j < len(b.Stmts) && isZeroCarrierStmt(b.Stmts[j]) {
			j++
		}
		if j-i >= 2 {
			run := make([]Stmt, j-i)
			copy(run, b.Stmts[i:j])
			keys := make([]string, len(run))
			for k, s := range run {
				keys[k] = zeroCarrierStmtKey(s)
			}
			// 稳定插入排序（段短；不引入 sort 包依赖，也不改变同键相对序）。
			for k := 1; k < len(run); k++ {
				for m := k; m > 0 && keys[m] < keys[m-1]; m-- {
					run[m], run[m-1] = run[m-1], run[m]
					keys[m], keys[m-1] = keys[m-1], keys[m]
					changed = true
				}
			}
			copy(b.Stmts[i:j], run)
		}
		i = j
	}
	return changed
}

// isZeroCarrierStmt 该语句是「一个注入零载体」吗 —— 判据与 P27/P28 逐字同尺
// （`isZeroCarrierNode`）。
func isZeroCarrierStmt(s Stmt) bool {
	ns, ok := s.(*NodeStmt)
	if !ok || ns == nil || ns.Node == nil {
		return false
	}
	return isZeroCarrierNode(ns.Node)
}

// zeroCarrierStmtKey 序键 = 既有池键（`zeroCarrierPoolKey`）—— 一处定义，
// 不另写一份「排序用」的键（否则排序序与池身份两把尺会漂移）。
func zeroCarrierStmtKey(s Stmt) string {
	ns, _ := s.(*NodeStmt)
	if ns == nil {
		return ""
	}
	if cr, ok := ns.Node.(*CodeRun); ok {
		return zeroCarrierPoolKey(cr)
	}
	return ""
}
