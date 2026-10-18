package ir

import "strconv"

// alloc_node_id.go — **IR 优化阶段**（构造之后）铸新 node id 的唯一入口。
//
// 为什么 pass 也可以铸 id：id 的**值**不是契约（AGENTS.md「产物 id 不是契约」），
// 契约是「同一个语义 ⇒ 同一套 id」——由产物出口那条对删除/新增路径**无知**的规则
// 保证（`internal/backend/cozebiz/compact_pruned_ids.go`：分配集合 − 落地集合）。
// 于是新增分配只要**登记**进 `AllocatedNodeIDs` 就自动闭合，不需要逐 pass 自报足迹
// ——这正是那条规则从「单 pass 足迹」推广出来的理由（逐 pass 闭合必然漏）。
//
// id 段与构造期同源（`100000+n`），并**避开** 900001（End 段，平台约定）。
// 取号 = 分配集合 ∪ 图上现存节点的数值最大者 + 1 ⇒ 与既有节点不撞，且同一语义
// 两轮得到同一套号（分配集合是构造期 + pass 的**确定**函数）。
func (g *Graph) AllocNodeID() string {
	if g == nil {
		return ""
	}
	max := 100000
	bump := func(id string) {
		if n, err := strconv.Atoi(id); err == nil && n > max && n < 900001 {
			max = n
		}
	}
	for _, id := range g.AllocatedNodeIDs {
		bump(id)
	}
	for id := range collectIRNodes(g) {
		bump(id)
	}
	id := strconv.Itoa(max + 1)
	g.AllocatedNodeIDs = append(g.AllocatedNodeIDs, id)
	return id
}
