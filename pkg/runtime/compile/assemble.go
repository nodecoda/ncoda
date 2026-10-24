package compile

// assemble.go — 输入装配（PRD M1 第 2 步）:把节点配置里的 Source 列表解析
// 为节点实现的输入 map。装配在命名空间化的合并输入上进行:merged 的 key 是
// 生产者节点 ID,value 是该生产者的输出 map(端口名 → 值)。

import (
	"context"
	"sort"
	"time"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/state"
)

// wrap builds the per-node lambda: (special-case) raw input for the entry
// start node, assembled inputs for data nodes, raw merged map for loop
// drivers. Output is always namespaced {nodeID: out} (merge-safe).
func (b *buildGraph) wrap(n *spec.NodeSpec, impl nodes.NodeImpl) *compose.Lambda {
	entryID, external := b.entryID, b.external
	echo := b.branchSources[n.ID] // branch source: carry context to targets
	log := b.c.nodeLog            // observation hook (nil unless WithNodeLogger)
	kind := n.Kind
	// M11/M13: 需要会话 ID 的节点（memory agent / conversation 历史读取）——
	// 编译期判定,lambda 捕获常量（避免每轮 invoke 重复 decode config）。
	needsConvID := kind == spec.KindConversation
	if kind == spec.KindAgent {
		if cfg, err := spec.DecodeConfig(n.Kind, n.Config); err == nil {
			needsConvID = cfg.(*spec.AgentConfig).Memory
		}
	}
	// M14 conversation 变量语义:
	//   - assign/loop_assign 的目标若是 @conversation 声明 → Invoke 后
	//     把值写回 RuntimeState.ConvVars（共享状态,对齐参考目标 conversation
	//     variable;coze 的 assign 端口输出仍是局部数据流,两者并存）。
	//   - 模板类节点（output/text/llm/agent）→ Invoke 前把 ConvVars 当前
	//     值注入 inputs,键 = "global_variable_app.<name>"（与 IR 生成的
	//     占位符全文一致,expr.RenderTemplate / resolveBlockTemplate 命中）。
	convVar := ""
	tplNode := b.hasConvVarsAny()
	switch kind {
	case spec.KindAssign, spec.KindLoopAssign:
		if cfg, err := spec.DecodeConfig(n.Kind, n.Config); err == nil {
			if name := cfg.(*spec.AssignConfig).VarName; b.hasConvVar(name) {
				convVar = name
			}
		}
	case spec.KindOutput, spec.KindText, spec.KindLLM, spec.KindAgent:
		tplNode = tplNode && !external // body 内模板由驱动器转发外层值;conv 注入只在主图
	}
	switch n.Kind {
	case spec.KindLoop:
		// driver resolves loop inputs + forwarded producers itself
		return compose.InvokableLambda(func(ctx context.Context, in map[string]any) (map[string]any, error) {
			start := time.Now()
			out, err := impl.Invoke(ctx, in)
			if log != nil {
				log(NodeLog{Node: n.ID, Kind: kind, Input: in, Out: out, Err: err, Dur: time.Since(start)})
			}
			if err != nil {
				return nil, err
			}
			if echo {
				for k, v := range in {
					if _, dup := out[k]; !dup {
						out[k] = v
					}
				}
			}
			return map[string]any{n.ID: out}, nil
		})
	}
	return compose.InvokableLambda(func(ctx context.Context, in map[string]any) (map[string]any, error) {
		var inputs map[string]any
		ctxMap := in // namespaced merged view (echo source)
		if n.ID == entryID && !external {
			inputs = in // start node: raw entry params
			// the entry start's context is its own namespaced output
			ctxMap = map[string]any{n.ID: in}
		} else {
			var err error
			inputs, err = AssembleInput(n, in)
			if err != nil {
				return nil, err
			}
			// M11/M13: memory agent / conversation 节点自动注入会话 ID ——
			// entry 节点透传了原始输入（含 conversation_id / ConversationID
			// 键）,合成 source 放进节点输入;节点侧缺省 "default"。
			if needsConvID && !external && entryID != "" {
				if cm, ok := in[entryID].(map[string]any); ok {
					for _, k := range []string{"conversation_id", "ConversationID"} {
						if v, ok := cm[k]; ok {
							inputs["ConversationID"] = v
							break
						}
					}
				}
			}
		}
		// M14: 模板节点注入 conversation 变量当前值（{{global_variable_app.x}}）。
		if tplNode {
			if rs, gerr := state.Get(ctx); gerr == nil {
				for cname, cval := range rs.ConvVars {
					inputs["global_variable_app."+cname] = cval
				}
			}
		}
		start := time.Now()
		out, err := impl.Invoke(ctx, inputs)
		if log != nil {
			log(NodeLog{Node: n.ID, Kind: kind, Input: inputs, Out: out, Err: err, Dur: time.Since(start)})
		}
		if err != nil {
			return nil, err
		}
		// M14: conversation assign 写回共享状态（Invoke 成功才写）。
		if convVar != "" {
			if v, ok := out[convVar]; ok {
				_ = compose.ProcessState(ctx, func(_ context.Context, rs *state.RuntimeState) error {
					rs.ConvVars[convVar] = v
					return nil
				})
			}
		}
		if echo {
			for k, v := range ctxMap {
				if _, dup := out[k]; !dup {
					out[k] = v
				}
			}
		}
		return map[string]any{n.ID: out}, nil
	})
}

// AssembleInput resolves a node's dataflow sources against the merged,
// namespaced input map into the node's port-path keyed input map.
//
//   - literal source (BlockID==""): value = Val (""/0/false are legal).
//   - ref source: value = dotted-path lookup into the producer's output map
//     ("r.name" → out["r"]["name"], falling back to the exact key).
//   - missing producer (skipped branch / not among predecessors): nil
//     (aggregate first_non_zero treats nil as zero, like an unexecuted arm).
//
// Sources are placed at Source.Path into the output map (nested paths
// create nested maps).
func AssembleInput(n *spec.NodeSpec, merged map[string]any) (map[string]any, error) {
	srcs, err := spec.Sources(n)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, src := range srcs {
		var val any
		if src.BlockID == "" {
			val = src.Val
		} else {
			val = resolveRef(merged, src.BlockID, src.Name)
		}
		putPath(out, src.Path, val)
	}
	return out, nil
}

// resolveRef looks up producer→dotted port in a namespaced map.
//
// Fallback (coze context semantics): when the producer is not a direct
// predecessor (branch targets — their data arrives via the branch
// source's context echo), scan the branch-source echoes for the producer
// namespace. The echo is the branch source's merged input (keyed by
// producer ID), so the producer data is found nested inside it.
func resolveRef(merged map[string]any, producerID, port string) any {
	if pm, ok := merged[producerID]; ok {
		return lookupDotted(pm, port)
	}
	// deterministic first-hit: scan top-level keys in sorted order, then
	// descend into nested echoes (deep branch chains).
	for _, key := range sortedKeys(merged) {
		v := merged[key]
		if m, ok := v.(map[string]any); ok {
			if pm, ok2 := m[producerID]; ok2 {
				return lookupDotted(pm, port)
			}
			if val, found := scanNestedEcho(m, producerID, port); found {
				return val
			}
		}
	}
	return nil
}

// resolveRefPresent is resolveRef with the absent/null distinction preserved
// (ES-045): present=true means the port exists, and v==nil then means an
// **explicit null** — not "the producer did not emit this port". The loop
// collector needs that difference to choose between absent_skip and
// null_append/null_error.
//
// Context-echo hits use the same key-witness rule: the echo nests the
// producer namespace as a map, so an explicit null member is present. A
// missing producer/port inside the echo stays absent.
func resolveRefPresent(merged map[string]any, producerID, port string) (any, bool) {
	if pm, ok := merged[producerID]; ok {
		return lookupDottedOK(pm, port)
	}
	for _, key := range sortedKeys(merged) {
		v := merged[key]
		if m, ok := v.(map[string]any); ok {
			if pm, ok2 := m[producerID]; ok2 {
				return lookupDottedOK(pm, port)
			}
			if val, found := scanNestedEchoPresent(m, producerID, port); found {
				return val, true
			}
		}
	}
	return nil, false
}

// scanNestedEcho searches for a producer namespace inside a context echo
// (branch-source chains nest one level per branch crossing). It reports the
// namespace hit, not the port presence: resolveRef is value-only and keeps
// first-producer-wins even when that producer lacks the port.
func scanNestedEcho(echo map[string]any, producerID, port string) (any, bool) {
	for _, key := range sortedKeys(echo) {
		m, ok := echo[key].(map[string]any)
		if !ok {
			continue
		}
		if pm, ok2 := m[producerID]; ok2 {
			return lookupDotted(pm, port), true
		}
	}
	for _, key := range sortedKeys(echo) {
		if m, ok := echo[key].(map[string]any); ok {
			if val, found := scanNestedEchoPresent(m, producerID, port); found {
				return val, true
			}
		}
	}
	return nil, false
}

// scanNestedEchoPresent is the presence-aware variant for resolveRefPresent:
// a producer namespace that exists but lacks the port is (nil,false), while an
// explicit null member is (nil,true).
func scanNestedEchoPresent(echo map[string]any, producerID, port string) (any, bool) {
	for _, key := range sortedKeys(echo) {
		m, ok := echo[key].(map[string]any)
		if !ok {
			continue
		}
		if pm, ok2 := m[producerID]; ok2 {
			return lookupDottedOK(pm, port)
		}
	}
	for _, key := range sortedKeys(echo) {
		if m, ok := echo[key].(map[string]any); ok {
			if val, found := scanNestedEchoPresent(m, producerID, port); found {
				return val, true
			}
		}
	}
	return nil, false
}

// sortedKeys returns the map keys in ascending order (deterministic
// first-hit resolution for the context-echo scan).
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// lookupDotted walks a dotted port name ("value.city") through nested maps.
// Falls back to the exact joined key (producers may emit flat dotted keys).
func lookupDotted(producer any, port string) any {
	v, _ := lookupDottedOK(producer, port)
	return v
}

// lookupDottedOK is lookupDotted with presence: (nil, true) is an explicit
// null member, (nil, false) is an absent member.
func lookupDottedOK(producer any, port string) (any, bool) {
	if port == "" {
		return producer, true
	}
	segs := splitDots(port)
	cur := producer
	for _, seg := range segs {
		ok := false
		cur, ok = lookupDottedSeg(cur, seg, port)
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// lookupDottedSeg is one segment step of lookupDottedOK. It keeps the legacy
// fallbacks (record-array item 0, flat dotted key) and reports whether the
// member exists.
func lookupDottedSeg(cur any, seg, port string) (any, bool) {
	m, ok := cur.(map[string]any)
	if !ok {
		// E5-b: a record-array member chain (result[0].output →
		// "outputList.output") — the constant-0 index was dropped at
		// IR, so an array at a segment position descends via item 0
		// (empty array → missing). Mirrors the reference-target item-0 member
		// access rendering.
		if arr, isArr := cur.([]any); isArr {
			if len(arr) == 0 {
				return nil, false
			}
			if m0, ok2 := arr[0].(map[string]any); ok2 {
				if v, ok3 := m0[seg]; ok3 {
					return v, true
				}
				if vv, ok3 := m0[port]; ok3 {
					return vv, true
				}
			}
		}
		if arr, isArr := cur.([]map[string]any); isArr {
			if len(arr) == 0 {
				return nil, false
			}
			if v, ok2 := arr[0][seg]; ok2 {
				return v, true
			}
			if vv, ok3 := arr[0][port]; ok3 {
				return vv, true
			}
		}
		// fallback: try the full remaining key on the top-level map
		if mm, ok2 := cur.(map[string]any); ok2 {
			if v, ok3 := mm[port]; ok3 {
				return v, true
			}
		}
		return nil, false
	}
	v, found := m[seg]
	if !found {
		// dotted-member fallback on this level (e.g. "r.name" as a
		// literal key emitted by some producers)
		if vv, ok2 := m[port]; ok2 {
			return vv, true
		}
		return nil, false
	}
	return v, true
}

func splitDots(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// putPath writes val at path into m, creating nested maps as needed.
func putPath(m map[string]any, path []string, val any) {
	if len(path) == 0 {
		return
	}
	cur := m
	for i, seg := range path {
		if i == len(path)-1 {
			cur[seg] = val
			return
		}
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
}
