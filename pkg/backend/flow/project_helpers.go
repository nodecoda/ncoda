//go:build !poison

// project_helpers.go — legacy emitter helpers behind a build tag so the
// poison build can replace them with panicking stubs and thereby prove the
// new solver never depends on them (TC-28 C2).

package flow

import "github.com/nodecoda/ncoda/pkg/ir"

// owePort 登记一条分支端口义务（渲染不出节点的臂 ⇒ selector 欠该臂端口一条边）。
func (d *deriveCtx) owePort(sel, port string) {
	d.pendingPorts = append(d.pendingPorts, portObligation{Selector: sel, Port: port})
}

// drainPorts 把所有未落地的端口义务接进 dst 并清空 —— 这是**唯一** drain 形状
// （协作渲染器的 drainPorts 同形：续接语句 / 分支汇聚 / 容器尾 / 文档尾四个落点）。
// 义务边是**分支端口边**（P4），不是普通控制边：每个端口各自显式。
func (d *deriveCtx) drainPorts(dst string) {
	d.drainPortsFrom(0, dst, "")
}

// drainPortsFrom 接的是 [mark:] 之后登记的义务（dstPort = 容器口时用），保留
// [0:mark) 的 —— 那些属于**外层** junction，落点不是本容器。
func (d *deriveCtx) drainPortsFrom(mark int, dst, dstPort string) {
	if dst == "" || mark >= len(d.pendingPorts) {
		return
	}
	rest := append([]portObligation{}, d.pendingPorts[mark:]...)
	d.pendingPorts = d.pendingPorts[:mark]
	for _, ob := range rest {
		if ob.Selector == "" || ob.Selector == dst {
			continue
		}
		d.edges = append(d.edges, Edge{SourceNodeID: ob.Selector, TargetNodeID: dst, TargetPortID: dstPort, SourcePortID: ob.Port})
	}
}

// recordSkippedMerge 记住被 pass-through 跳过的 merge 的原始 Refs
// （值语义:Group1 = 首非 nil ref）。
func (d *deriveCtx) recordSkippedMerge(m *ir.Aggregate) {
	if m == nil {
		return
	}
	if d.skippedMerge == nil {
		d.skippedMerge = map[string]map[string][]ir.Ref{}
	}
	groups := map[string][]ir.Ref{}
	if len(m.Groups) > 0 {
		for _, group := range m.Groups {
			refs := make([]ir.Ref, len(group.Refs))
			copy(refs, group.Refs)
			groups[group.Name] = refs
		}
	} else {
		refs := make([]ir.Ref, len(m.Refs))
		copy(refs, m.Refs)
		groups["Group1"] = refs
	}
	d.skippedMerge[m.ID] = groups
}
