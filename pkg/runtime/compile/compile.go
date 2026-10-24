// Package compile — 运行时编译层（PRD M1）:spec.v1 → Eino compose 图。
//
// 映射规则（与 spike 实证一致）:
//   - 每个节点 = 一个 lambda:输入装配(Source ref → 端口路径 map)→ 节点实现
//     → 输出按节点 ID 命名空间包装 {nodeID: {port: value}}。命名空间化是
//     必要的:Eino 多前驱浅合并 map,同名端口(如 result)在汇聚处会撞键
//     (duplicated key found),而节点 ID 天然唯一。
//   - 触发模式:整图 AllPredecessor(工作流语义 = 等待全部输入;分支跳过
//     传播满足未走分支的依赖)。
//   - selector / attempt-op / qa 的带端口出边 → AddBranch;分支条件读取
//     节点输出的 "$branch" 标记 → 目标节点 key(端口 → 首个目标)。
//   - loop 容器 = 驱动器 lambda(内建 body 子图,递归编译);loop-back 边
//     (TargetPort==loop-function-inline-input)被吸收,永不成为 Eino 边。
//   - 数据流引用不保证直接边(实证:var d = a+b 只连 b),编译期"数据依赖
//     补边"保证每个 Source.BlockID 有 producer→node 的边。
//   - 缓存 key = sha256(规范化 spec) + 编译层版本(D1);带 GraphCompileOption
//     的编译不做缓存(选项影响图语义)。
package compile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/state"
)

// reachableFromEdges 是「从 root 沿控制边可达」的泛型查询（不进入 exclude 里的目标；
// loop-back 边被吸收）。用于门控区域计算与闭包边的防环判据。
func reachableFromEdges(s *spec.Spec, root string, exclude map[string]bool) map[string]bool {
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, e := range s.Edges {
			if e.Source == id && !exclude[e.Target] && e.TargetPort != spec.PortLoopInlineInput {
				walk(e.Target)
			}
		}
	}
	walk(root)
	return seen
}

// branchGatingAnalysis 是**分支门控分析**（spec 静态、一次性）：
//
//	portsByNode / endNodesByNode —— 分支接线（AddBranch 的端口→目标与 endNodes）
//	targetsOf / branchTargets    —— 直接分支目标（分支源的端口弧目标）
//	regions                      —— 分支源的**门控区域**（见下）
//	gatedBy                      —— 受门控节点 → 门控它的分支源（区域最小者）
//
// **门控区域**（一手修正，2026-09-24）：
//
//	region(S) = { n : n 从 S 的**恰好一个**直接目标可达 }
//
// 「可达」沿控制边（端口弧 + 普通边）走、不设截断。取「恰好一个」自动止于**汇合点**
// —— 汇合点从 S 的两个端口目标都可达（否则它不是汇合点），它下游的一切同样被排除。
//
// 旧实现只认 S 的**直接目标**，于是臂内**经普通边从臂入口到达的嵌套 selector /
// 载体**被判成「不受门控」：外层值的数据边（含闭包边）成了它们的恒存在前驱 ⇒
// 未走臂里它们照样执行，把臂内值送进汇聚（R-51 一手：`c1=false` 时读者取到 `n`）。
//
// 区域口径改一次，三处消费随之（都在 build 里）：①受门控节点的**数据边**不直连
// （否则绕过门控 ⇒ 节点因「前驱就绪」而执行）；②闭包边落点走 closureSink（最近的
// **不受门控**的祖先分支源）；③受门控节点**回声**其上下文（跨门控的值只能逐跳传递）。
//
// 同一节点被多个分支源覆盖时取**区域最小者**（= 最内层门控；区域大小是包含关系的
// 近似判据，平局按 id）—— 图构造必须是**函数**，不许依赖 map 迭代序（R-50 同纪律）。
func branchGatingAnalysis(s *spec.Spec, nodesByID map[string]*spec.NodeSpec) (portsByNode map[string]map[string]string, endNodesByNode map[string]map[string]bool, targetsOf map[string]map[string]bool, branchTargets map[string]bool, regions map[string]map[string]bool, gatedBy map[string]string) {
	portsByNode = map[string]map[string]string{}
	endNodesByNode = map[string]map[string]bool{}
	targetsOf = map[string]map[string]bool{}
	branchTargets = map[string]bool{}
	for _, e := range s.Edges {
		if e.SourcePort == "" || e.TargetPort == spec.PortLoopInlineInput {
			// M17 (46_loop_escape)：loop 体内带 SourcePort 的 loop-back 边必须
			// 声明分支 target（语义 = 迭代自然完成 → body END 汇点）。
			if e.SourcePort != "" && e.TargetPort == spec.PortLoopInlineInput {
				if _, ok := nodesByID[e.Source]; ok {
					if _, ok := portsByNode[e.Source]; !ok {
						portsByNode[e.Source] = map[string]string{}
						endNodesByNode[e.Source] = map[string]bool{}
					}
					if _, seen := portsByNode[e.Source][e.SourcePort]; !seen {
						portsByNode[e.Source][e.SourcePort] = compose.END
					}
					endNodesByNode[e.Source][compose.END] = true
				}
			}
			continue
		}
		if _, ok := nodesByID[e.Source]; !ok {
			continue
		}
		tNode, ok := nodesByID[e.Target]
		if !ok {
			continue
		}
		if _, ok := portsByNode[e.Source]; !ok {
			portsByNode[e.Source] = map[string]string{}
			endNodesByNode[e.Source] = map[string]bool{}
			targetsOf[e.Source] = map[string]bool{}
		}
		if _, seen := portsByNode[e.Source][e.SourcePort]; !seen {
			portsByNode[e.Source][e.SourcePort] = e.Target
		}
		endNodesByNode[e.Source][e.Target] = true
		if tNode.Kind == spec.KindEnd {
			continue
		}
		targetsOf[e.Source][e.Target] = true
		branchTargets[e.Target] = true
	}
	// 区域口径 = 函数头注那一条（rule 一处定义）。一手反例读数（前一版「截断在
	// 任何分支源的直接目标上」）：提升后的图是**线性链**，从臂内往里走会穿过汇合
	// 点、一路走进后继语句（下一段 `if` 的选择器）⇒ 那些选择器被判成受本条臂门控
	// ⇒ 数据边被丢 ⇒ 条件读成 nil ⇒ `$branch` 变 false（4 链夹具里第 2..4 条链全中）。
	regions = map[string]map[string]bool{}
	for srcID, targets := range targetsOf {
		reachCount := map[string]int{}
		for target := range targets {
			rs := reachableFromEdges(s, target, nil)
			rs[target] = true
			for n := range rs {
				reachCount[n]++
			}
		}
		region := map[string]bool{}
		for n, c := range reachCount {
			if c == 1 {
				region[n] = true
			}
		}
		regions[srcID] = region
	}
	gatedBy = map[string]string{}
	srcIDs := make([]string, 0, len(regions))
	for id := range regions {
		srcIDs = append(srcIDs, id)
	}
	sort.Slice(srcIDs, func(i, j int) bool {
		if len(regions[srcIDs[i]]) != len(regions[srcIDs[j]]) {
			return len(regions[srcIDs[i]]) < len(regions[srcIDs[j]])
		}
		return srcIDs[i] < srcIDs[j]
	})
	for _, srcID := range srcIDs {
		for n := range regions[srcID] {
			if _, ok := gatedBy[n]; !ok {
				gatedBy[n] = srcID
			}
		}
	}
	return portsByNode, endNodesByNode, targetsOf, branchTargets, regions, gatedBy
}

// Version is the compile-layer version — part of the cache key (D1).
// Bump whenever graph-construction semantics change (U1.4 cache-key test).
const Version = "runtime-compile-v1"

// Runnable is the compiled workflow entrypoint.
type Runnable = compose.Runnable[map[string]any, map[string]any]

// chatflowRunnable 是 @mode advanced-chat 的记忆包装（M13）:图执行成功后
// 把本轮 [user query, 最终输出] 追加到会话记忆（复用 Deps.Memory;无 store
// 不包装 —— 降级为普通 workflow）。conversation 节点读历史,这里写历史,
// 共同构成 参考目标 chatflow 的"每轮对话 + 应用级记忆"语义。
type chatflowRunnable struct {
	inner Runnable
	mem   nodes.Memory
}

func (r *chatflowRunnable) Invoke(ctx context.Context, in map[string]any, opts ...compose.Option) (map[string]any, error) {
	out, err := r.inner.Invoke(ctx, in, opts...)
	if err != nil {
		return out, err
	}
	query, qok := in["query"].(string)
	if !qok || strings.TrimSpace(query) == "" {
		query, qok = in["Query"].(string)
	}
	res := UnwrapOutput(out)
	answer, aok := res["output"].(string)
	if !qok || !aok || strings.TrimSpace(answer) == "" {
		return out, nil // 非对话轮（缺 query/答案）不追加
	}
	_ = r.mem.Append(ctx, conversationID(in), []nodes.Message{
		{Role: "user", Content: query},
		{Role: "assistant", Content: answer},
	})
	return out, nil
}

// Stream/Collect/Transform forward to the inner runnable (v1: chatflow
// memory only hooks the Invoke path — the CLI runs one turn per process).
func (r *chatflowRunnable) Stream(ctx context.Context, in map[string]any, opts ...compose.Option) (*schema.StreamReader[map[string]any], error) {
	return r.inner.Stream(ctx, in, opts...)
}

func (r *chatflowRunnable) Collect(ctx context.Context, in *schema.StreamReader[map[string]any], opts ...compose.Option) (map[string]any, error) {
	return r.inner.Collect(ctx, in, opts...)
}

func (r *chatflowRunnable) Transform(ctx context.Context, in *schema.StreamReader[map[string]any], opts ...compose.Option) (*schema.StreamReader[map[string]any], error) {
	return r.inner.Transform(ctx, in, opts...)
}

// Compiler compiles spec.v1 documents into Eino graphs with a memoized
// cache keyed by normalized-spec-hash + compile version (D1). Safe for
// concurrent Compile (mutex); a single Invoke of the returned runnable is
// the v1 concurrency contract (script-driven CLI, not a concurrent service).
type Compiler struct {
	reg     *nodes.Registry
	version string
	deps    nodes.Deps
	nodeLog NodeLogger
	mu      sync.Mutex
	cache   map[string]Runnable
}

type Option func(*options)

type options struct {
	version string
	deps    nodes.Deps
	nodeLog NodeLogger
}

// NodeLog is one node invocation trace emitted through the debug logger
// (CLI --debug / runtime observability).
type NodeLog struct {
	Node  string
	Kind  string
	Input map[string]any
	Out   map[string]any
	Err   error
	Dur   time.Duration
}

// NodeLogger receives one trace per node Invoke. NodeLogger is a pure
// observation hook: it must not mutate the graph, and it is NOT part of the
// cache key (like Deps — the deterministic subset stays spec+version).
type NodeLogger func(l NodeLog)

// WithVersion overrides the compile-layer version (cache-key tests, U1.4).
func WithVersion(v string) Option {
	return func(o *options) { o.version = v }
}

// WithDeps injects execution-side dependencies (HTTP client, model factory,
// tool registry, state) for I/O nodes (M2.2). Deps are part of the graph's
// semantics — they are NOT part of the cache key (deterministic subset
// remains spec+version); recompiling with different deps yields the same
// graph shape over different backends, which is the intended contract.
func WithDeps(d nodes.Deps) Option {
	return func(o *options) { o.deps = d }
}

// WithNodeLogger attaches a per-node invocation trace hook (--debug). Like
// Deps, the logger is observation-only and stays out of the cache key.
func WithNodeLogger(l NodeLogger) Option {
	return func(o *options) { o.nodeLog = l }
}

// New builds a Compiler over a node registry.
func New(reg *nodes.Registry, opts ...Option) *Compiler {
	c := &Compiler{reg: reg, version: Version, cache: map[string]Runnable{}}
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	if o.version != "" {
		c.version = o.version
	}
	c.deps = o.deps
	c.nodeLog = o.nodeLog
	return c
}

// Compile normalizes the spec (canonical bytes), checks the memoized cache
// and builds the Eino graph. opts are forwarded to the Eino graph compile
// (checkpoint store etc.); when non-empty the cache is bypassed (options
// change graph semantics).
func (c *Compiler) Compile(ctx context.Context, s *spec.Spec, opts ...compose.GraphCompileOption) (Runnable, error) {
	norm, err := s.Normalize()
	if err != nil {
		return nil, diag.New(diag.RSpecInvalid, "%v", err)
	}
	if len(opts) > 0 {
		r, err := c.build(ctx, norm, opts)
		if err != nil {
			return nil, err
		}
		return c.maybeWrapChatflow(norm, r), nil
	}
	h, err := norm.SpecHash()
	if err != nil {
		return nil, diag.New(diag.RSpecInvalid, "hash: %v", err)
	}
	key := fmt.Sprintf("%x|%s", h, c.version)
	c.mu.Lock()
	if r, ok := c.cache[key]; ok {
		c.mu.Unlock()
		return r, nil
	}
	c.mu.Unlock()
	r, err := c.build(ctx, norm, nil)
	if err != nil {
		return nil, err
	}
	r = c.maybeWrapChatflow(norm, r)
	c.mu.Lock()
	c.cache[key] = r
	c.mu.Unlock()
	return r, nil
}

// maybeWrapChatflow 包一层会话记忆追加（@mode advanced-chat + 有 Memory
// store 时）。norm 是规范化后的 spec（Mode 字段随 Normalize 保留）。
func (c *Compiler) maybeWrapChatflow(norm *spec.Spec, r Runnable) Runnable {
	if norm.Mode != "advanced-chat" || c.deps.Memory == nil {
		return r
	}
	return &chatflowRunnable{inner: r, mem: c.deps.Memory}
}

// conversationID 提取会话 ID（input map 键,缺省 "default"）。
func conversationID(in map[string]any) string {
	for _, key := range []string{"conversation_id", "ConversationID"} {
		if v, ok := in[key]; ok && v != nil && fmt.Sprint(v) != "" {
			return fmt.Sprint(v)
		}
	}
	return "default"
}

// CacheSize returns the number of cached runnables (test observation).
func (c *Compiler) CacheSize() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cache)
}

// buildGraph owns one compile: it holds the registry/deps and the
// allowExternalRefs flag (false at top level, true inside loop bodies).
type buildGraph struct {
	c        *Compiler
	deps     nodes.Deps
	external bool // loop-body subgraph: refs to unknown producers are forwarded
	entryID  string
	// convVars: @conversation 声明的变量（name → 初始值）。M14 起成为
	// 运行时共享状态:assign 写回 RuntimeState.ConvVars,模板节点从其中
	// 解析 {{global_variable_app.<name>}} 引用（对齐参考目标 conversation
	// variable 语义）。主图编译时收集;body 子图（external）不覆盖。
	convVars map[string]any
	// branchSources: node IDs whose output must echo their merged input
	// context (the branch gate delivers the echo to the selected target,
	// which resolves outside refs from it — coze context semantics).
	branchSources map[string]bool
	// bodyExtraExit: loop 体的**收集输出**所引用的体内节点（只在外层
	// buildLoopDriver 设置，body 子图构建期有效）。体图出口面 = eino END 的
	// **数据前驱**集合（`dagChannel.reportValues` 只收 DataPredecessors）
	// ⇒ 不在这里点名的体内节点，其命名空间不进 `body.Invoke` 的返回
	// ⇒ 驱动器 `resolveRef` 取不到 ⇒ 声明了的 loop 输出静默变 nil。
	// 平台语义相反：`outputs[]` 允许引用体内**任意**节点（按节点完成事件
	// 收集），不限于接到循环体出口的那个（R-142）。
	bodyExtraExit map[string]bool
}

// hasConvVar reports whether name is a declared @conversation variable.
func (b *buildGraph) hasConvVar(name string) bool {
	_, ok := b.convVars[name]
	return ok
}

// hasConvVarsAny reports whether the graph declares any @conversation var.
func (b *buildGraph) hasConvVarsAny() bool { return len(b.convVars) > 0 }

// isLoopChild reports whether producerID is a node inside loop node n's
// body subgraph (children). Body-internal producers are validated by the
// recursive body build; the outer graph must not treat them as dangling.
func isLoopChild(n *spec.NodeSpec, producerID string) bool {
	if n.Kind != spec.KindLoop {
		return false
	}
	cfg, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return false
	}
	for _, c := range cfg.(*spec.LoopConfig).Children {
		if c.ID == producerID {
			return true
		}
	}
	return false
}

func (c *Compiler) build(ctx context.Context, s *spec.Spec, opts []compose.GraphCompileOption) (Runnable, error) {
	return (&buildGraph{c: c, deps: c.deps}).build(ctx, s, false, opts)
}

// build compiles one spec (sub)graph into a runnable.
func (b *buildGraph) build(ctx context.Context, s *spec.Spec, external bool, opts []compose.GraphCompileOption) (Runnable, error) {
	// P6: foreign code is an opaque payload; its well-formedness is a
	// load-time invariant, not a runtime property. Check the whole node table
	// before instantiating anything, so an unreached branch cannot hide a
	// syntax error.
	if err := b.preflightCode(s); err != nil {
		return nil, err
	}
	b.external = external
	// M14: @conversation 变量是整图共享状态 —— 仅主图收集（body 子图
	// spec 不带 ConvVars,若覆盖会把主图声明清空）。
	if !external {
		b.convVars = map[string]any{}
		for _, cv := range s.ConvVars {
			b.convVars[cv.Name] = cv.Default
		}
	}
	// 主图挂 RuntimeState stateGen（PRD M6/D7）:节点/驱动器用
	// compose.ProcessState[*state.RuntimeState] 读写。body 子图不挂 gen ——
	// 直接 Invoke 时 ctx 状态链透传,循环状态跨迭代持久（见 state 包 doc）。
	// convVars 默认值在 gen 时灌入,assign 写回覆盖（M14 conversation 语义）。
	var g *compose.Graph[map[string]any, map[string]any]
	if !external {
		convInit := b.convVars
		g = compose.NewGraph[map[string]any, map[string]any](
			compose.WithGenLocalState(func(_ context.Context) *state.RuntimeState {
				rs := state.New()
				for k, v := range convInit {
					rs.ConvVars[k] = v
				}
				return rs
			}),
		)
	} else {
		g = compose.NewGraph[map[string]any, map[string]any]()
	}

	nodesByID := map[string]*spec.NodeSpec{}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		if _, dup := nodesByID[n.ID]; dup {
			return nil, diag.New(diag.RSpecInvalid, "duplicate node id %q", n.ID)
		}
		nodesByID[n.ID] = n
	}
	b.entryID = ""
	if !external && s.Entry != nil {
		b.entryID = s.Entry.ID
	}

	// 死代码闭包（N-RET-1）：规范出口 900001 的出边是「return 之后的不可达
	// 语句」的投影残留。它们不执行（出边被丢），也不得被「无入边」判成 spec
	// 错误 —— 只有**唯一**经由这些边可达的节点才是死代码；同时从入口可达的
	// 节点不受影响（共享汇合点仍然活）。
	deadAfterReturn := map[string]bool{}
	if !external && b.entryID != "" {
		closure := map[string]bool{}
		stack := make([]string, 0, 4)
		for _, e := range s.Edges {
			if e.Source == spec.CanonicalExitID && e.TargetPort != spec.PortLoopInlineInput {
				stack = append(stack, e.Target)
			}
		}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if closure[id] {
				continue
			}
			closure[id] = true
			for _, e := range s.Edges {
				if e.Source == id {
					stack = append(stack, e.Target)
				}
			}
		}
		live := map[string]bool{}
		stack = append(stack[:0], b.entryID)
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if live[id] {
				continue
			}
			live[id] = true
			for _, e := range s.Edges {
				if e.Source != id {
					continue
				}
				if e.Source == spec.CanonicalExitID && e.TargetPort != spec.PortLoopInlineInput {
					continue // 死代码边不传播活性
				}
				stack = append(stack, e.Target)
			}
		}
		for id := range closure {
			if !live[id] {
				deadAfterReturn[id] = true
			}
		}
	}

	// ── 1. instantiate + register every node lambda ─────────────────────
	impls := map[string]nodes.NodeImpl{}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		// synthesize the start node config from Spec.Entry (single source
		// of truth for entry params).
		if !external && b.entryID != "" && n.ID == b.entryID && n.Kind == spec.KindStart && s.Entry != nil {
			raw, merr := json.Marshal(spec.StartConfig{Params: s.Entry.Params})
			if merr != nil {
				return nil, diag.New(diag.RSpecInvalid, "start config: %v", merr)
			}
			n.Config = raw
		}
		var impl nodes.NodeImpl
		var err error
		if n.Kind == spec.KindLoop {
			// loop containers are compile-built drivers (body subgraph),
			// never registry nodes. The recursive body build mutates
			// buildGraph state (external/entryID) — save/restore so the
			// outer frame keeps its own values for subsequent wiring.
			savedExternal, savedEntry := b.external, b.entryID
			impl, err = b.buildLoopDriver(ctx, n, opts)
			b.external, b.entryID = savedExternal, savedEntry
		} else {
			impl, err = b.c.reg.Build(ctx, n, b.deps)
			if err != nil {
				// 透传已编码的 runtime 诊断（code/knowledge REJECT 等）;
				// 未注册 kind（ErrKindNotRegistered）包装为 RNodeNotRegistered。
				if d, ok := err.(*diag.Error); ok {
					return nil, d
				}
				var derr *diag.Error
				if errors.As(err, &derr) {
					return nil, derr
				}
				return nil, diag.New(diag.RNodeNotRegistered, "node %s (%s): %v", n.ID, n.Kind, err)
			}
		}
		if err != nil {
			return nil, err
		}
		impls[n.ID] = impl
	}
	// restore entryID after recursive loop-driver builds (they share this
	// buildGraph instance and clobber the field with external=true).
	if !external && s.Entry != nil {
		b.entryID = s.Entry.ID
	}
	// branch sources echo their context — computed from the spec BEFORE
	// any recursive body build mutates b.external (the loop driver wraps
	// are created during instantiation, above, with their own values).
	b.branchSources = map[string]bool{}
	for _, e := range s.Edges {
		if e.SourcePort != "" && e.TargetPort != spec.PortLoopInlineInput {
			if _, ok := nodesByID[e.Source]; ok {
				b.branchSources[e.Source] = true
			}
		}
	}
	// 门控分析在**注册 lambda 之前**算完：`wrap` 在构造时就绑定「是否回声」，
	// 而受门控节点也必须回声（R-51，见下）。
	portsByNode, endNodesByNode, targetsOf, branchTargets, regions, gatedBy := branchGatingAnalysis(s, nodesByID)
	// 受门控节点同样回声：跨门控搬运的值只能靠**回声逐跳**传递 —— 数据边会
	// 绕过门控（见 3 节），于是区域内的中间节点若把上下文吞掉，链下游的读者
	// 就取不到值（一手 R-51：臂入口的零值载体之后，读者取到 nil ⇒ 汇聚取零）。
	for id := range gatedBy {
		b.branchSources[id] = true
	}

	for i := range s.Nodes {
		n := &s.Nodes[i]
		if err := g.AddLambdaNode(n.ID, b.wrap(n, impls[n.ID])); err != nil {
			return nil, diag.New(diag.RSpecInvalid, "add node %s: %v", n.ID, err)
		}
	}

	// ── 2. branch-arc analysis (before edges: gating decisions depend on
	//    the branch topology). ──────────────────────────────────────────
	// branch arcs = port-carrying edges. Eino routes them exclusively
	// through AddBranch (endNodes declaration) — they are NEVER data edges
	// (spike-verified: a data edge into a branch target bypasses the gate
	// and triggers the unselected target).
	// 分支接线 / 门控区域 / 闭包落点 = 一处分析，已在注册 lambda 之前算完
	// （`portsByNode` … `gatedBy`；头注含 R-51 的一手证据）。

	// ── 3. edges: projection edges (loop-back absorbed) + data deps ─────
	// Direct branch targets are DATA-ISOLATED from outside their branch:
	// any data edge into a target from a non-gated producer bypasses the
	// branch gate (Eino empirical contract). Intra-branch edges (source is
	// another target of the same branch) are kept — the target then reads
	// gated data. Data the target needs from outside is resolved at
	// assembly time from the branch source's context echo (resolveRef
	// falls back to scanning the echo) — see assemble.go.
	edgeKey := map[string]bool{}
	addEdge := func(src, dst string) error {
		if src == dst {
			return nil
		}
		k := src + "\x00" + dst
		if edgeKey[k] {
			return nil
		}
		edgeKey[k] = true
		return g.AddEdge(src, dst)
	}
	// closure: outside producers referenced by branch targets must reach
	// the branch source's context (P→S edge) so the echo carries them.
	// Cycle-guarded: P must not already be reachable from S.
	// closureSink 决定「外层值的闭包边落在哪个分支源上」。
	//
	// 判据（一手实测，2026-09-24）：闭包边是**数据边**，而数据边进入一个「本身
	// 也是分支目标」的节点时会**绕过它的外层门控**（本文件 2 节头注原话：
	// 「a data edge into a branch target bypasses the gate and triggers the
	// unselected target」）⇒ 未走臂里的嵌套 selector 照样执行、它自己的分支再
	// 触发下游 ⇒ **未执行臂上取到非零值**。
	//
	//	string v; v = "";
	//	if (c1) { v = x; if (c2) { v = "z"; } }   // x = 入口参数（start 无条件产出）
	//	return v;
	//	实测：`!c1` 应为 ""，闭包边 `start.x → 内层 selector` 令内层照样执行 ⇒ "XX"。
	//
	// 语义正确的落点 = 该嵌套分支源**最近的那条不受门控的祖先分支源**：值经
	// 祖先的 echo 逐层嵌套传下去（`resolveRef` 的 `scanNestedEcho` 递归读回），
	// 而每一次跨分支都由**它自己的**门控决定是否执行 ⇒ 未走臂不再被触发。
	closureSink := func(s string) string {
		for guard := 0; guard < 64; guard++ {
			up, ok := gatedBy[s]
			if !ok {
				return s
			}
			s = up
		}
		return s
	}
	closureAdded := map[string]bool{} // for incoming accounting
	sortedSources := make([]string, 0, len(targetsOf))
	for srcID := range targetsOf {
		sortedSources = append(sortedSources, srcID)
	}
	sort.Strings(sortedSources)
	for _, srcID := range sortedSources {
		targets := targetsOf[srcID]
		sink := closureSink(srcID)
		sortedTargets := make([]string, 0, len(targets))
		for target := range targets {
			sortedTargets = append(sortedTargets, target)
		}
		sort.Strings(sortedTargets)
		for _, target := range sortedTargets {
			n := nodesByID[target]
			nodeSrcs, serr := spec.Sources(n)
			if serr != nil {
				return nil, diag.New(diag.RSpecInvalid, "node %s sources: %v", target, serr)
			}
			for _, src := range nodeSrcs {
				if src.BlockID == "" || src.BlockID == target {
					continue
				}
				producer, ok := nodesByID[src.BlockID]
				if !ok {
					continue // external producer — forwarded at runtime
				}
				if producer.Kind == spec.KindEnd {
					continue
				}
				if targets[src.BlockID] {
					continue // intra-branch: normal gated data edge
				}
				// P must be in the branch source's context. If P is not
				// reachable from S (cycle guard), add P→S so the echo
				// carries it to the selected target.
				downstream := reachableFromEdges(s, sink, nil)
				if downstream[src.BlockID] {
					continue // P is inside the branch region — cycle guard
				}
				if err := addEdge(src.BlockID, sink); err != nil {
					return nil, diag.New(diag.RSpecInvalid, "branch closure edge %s→%s: %v", src.BlockID, sink, err)
				}
				closureAdded[src.BlockID+"\x00"+sink] = true
			}
		}
	}
	incoming := map[string]int{}
	loopBackSources := map[string]bool{}
	for _, e := range s.Edges {
		if e.TargetPort == spec.PortLoopInlineInput {
			loopBackSources[e.Source] = true
			continue // absorbed by the loop driver
		}
		if _, ok := nodesByID[e.Source]; !ok {
			if external {
				continue // external producer (forwarded via body input map)
			}
			if e.Source == compose.START {
				continue
			}
			return nil, diag.New(diag.RSpecInvalid, "edge from unknown node %q", e.Source)
		}
		if _, ok := nodesByID[e.Target]; !ok {
			if external {
				continue
			}
			if e.Target == compose.END {
				continue
			}
			return nil, diag.New(diag.RSpecInvalid, "edge to unknown node %q", e.Target)
		}
		// 只有**规范流程出口** End（900001）的出边是投影残留（return 之后的
		// 死代码：`return "ok"; output("never")` ⇒ `900001 → dead`）。
		// 非规范 End 是 attempt / QA 臂端（SuccessEnd / FailureEnd）——控制流
		// 载体：`success { return "ok"; }` 之后的下一条语句靠 `臂端 → 后继`
		// 这条活边继续（语言仓 pkg/backend/flow/project.go:703 的「臂端 End
		// 是控制流载体」）。把它们一并丢掉会让后继变成无入边孤儿
		// （RUNREACHABLE_NODE，P5 §5.8 subscope return）。
		if nodesByID[e.Source].Kind == spec.KindEnd && e.Source == spec.CanonicalExitID {
			continue
		}
		// 死代码节点的边一律不物化：它们的源永不执行，若把边接到活节点上，
		// 活节点会永远等一个不会完成的前驱（Eino：no tasks to execute）。
		if deadAfterReturn[e.Source] || deadAfterReturn[e.Target] {
			continue
		}
		// port-carrying edges are branch arcs — AddBranch only.
		if e.SourcePort != "" {
			continue
		}
		// data edges INTO a direct branch target from a node that is not
		// itself a target of the same branch bypass the gate — drop.
		if branchTargets[e.Target] {
			intra := false
			for srcID := range targetsOf {
				if targetsOf[srcID][e.Source] && targetsOf[srcID][e.Target] {
					intra = true
					break
				}
			}
			if !intra {
				// else-less if 汇合豁免（X245）:else-less if 中段续接时,分支尾
				// （then 链深处,非 S 直接 target）以普通边进入续节点 T,同时
				// S 的 false 端口边也进入 T —— T 是汇合点（pendingIf
				// junction）,两条路径都必须可达。直接丢弃会把 true 路径
				// dead-end（"no tasks to execute after <branch tail>"）。
				// 保留条件:e.Source 从某分支源 S（其 target 含 T）可达
				// 且不经 T（防环）、且 e.Source 不是 S 的直接 target。
				kept := false
				for srcID := range targetsOf {
					if !targetsOf[srcID][e.Target] {
						continue
					}
					if targetsOf[srcID][e.Source] {
						continue // S 的两个直接 target 互连——仍丢弃
					}
					if reachableFromEdges(s, srcID, map[string]bool{e.Target: true})[e.Source] {
						kept = true
						break
					}
				}
				if !kept {
					continue
				}
			}
		}
		if err := addEdge(e.Source, e.Target); err != nil {
			return nil, diag.New(diag.RSpecInvalid, "edge %s→%s: %v", e.Source, e.Target, err)
		}
		incoming[e.Target]++
	}
	// data-dependency edges (a ref's producer must be edge-connected),
	// except into direct branch targets (resolved via the context echo).
	for i := range s.Nodes {
		n := &s.Nodes[i]
		srcs, err := spec.Sources(n)
		if err != nil {
			return nil, diag.New(diag.RSpecInvalid, "node %s sources: %v", n.ID, err)
		}
		for _, src := range srcs {
			if src.BlockID == "" || src.BlockID == n.ID {
				continue
			}
			producer, ok := nodesByID[src.BlockID]
			if !ok {
				if external {
					continue // forwarded via the body input map
				}
				// loop 节点的 Sources 递归包含 body 子图内部引用（child 间
				// 数据流,如 assign→code）;这些 producer 不在主图 nodesByID,
				// 由 body 子图编译时自检 —— 主图不得误报 dangling。
				if isLoopChild(n, src.BlockID) {
					continue
				}
				return nil, diag.New(diag.RDanglingRef, "node %s references unknown producer %q", n.ID, src.BlockID)
			}
			if producer.Kind == spec.KindEnd {
				continue
			}
			// 受门控节点 = 直接分支目标 **∪** 臂内链路上的节点（gatedBy 的
			// 区域口径，见 branchGatingAnalysis）。从门控区域**外**的 producer
			// 直连进来的数据边会**绕过门控**：eino 的 AllPredecessor 只要前驱
			// 集合里有一个恒存在的外部前驱，节点就会执行 —— 即使它所在的那条臂
			// 被跳过（一手：R-51，`c1=false` 时臂内嵌套 selector 照样跑，把臂内
			// 值送进汇聚）。⇒ 丢弃，值从门控源的**上下文回声**里逐层取
			// （assemble.go resolveRef / scanNestedEcho）。区域**内**的 producer
			// 是同样受门控的数据流 —— 保留普通边。
			if g, gated := gatedBy[n.ID]; gated && !regions[g][src.BlockID] {
				continue
			}
			if err := addEdge(src.BlockID, n.ID); err != nil {
				return nil, diag.New(diag.RSpecInvalid, "data edge %s→%s: %v", src.BlockID, n.ID, err)
			}
			incoming[n.ID]++
		}
	}
	// closure edges count as incoming for the branch source (reachability).
	for k := range closureAdded {
		parts := splitKey(k)
		incoming[parts[1]]++
	}

	// ── 4. branch wiring: port-carrying outgoing edges → AddBranch ──────
	for nodeID, portTarget := range portsByNode {
		branch := compose.NewGraphBranch[map[string]any](
			func(ctx context.Context, in map[string]any) (string, error) {
				return branchKeyOf(nodeID, in, portTarget)
			}, endNodesByNode[nodeID])
		if err := g.AddBranch(nodeID, branch); err != nil {
			return nil, diag.New(diag.RSpecInvalid, "node %s add branch: %v", nodeID, err)
		}
	}

	// ── 4. START wiring ──────────────────────────────────────────────────
	if !external && b.entryID != "" {
		if b.entryID == compose.START || b.entryID == compose.END {
			return nil, diag.New(diag.RSpecInvalid, "entry node id %q is reserved by the graph runtime", b.entryID)
		}
		if _, ok := nodesByID[b.entryID]; !ok {
			return nil, diag.New(diag.RSpecInvalid, "entry node %q missing from nodes", b.entryID)
		}
	}
	if external {
		// body subgraph: START delivers the driver's forwarded input map to
		// entry nodes (zero-incoming) and to any node referencing external
		// producers (its refs resolve from the forwarded map).
		hasExternalRef := map[string]bool{}
		for i := range s.Nodes {
			n := &s.Nodes[i]
			srcs, err := spec.Sources(n)
			if err != nil {
				return nil, err
			}
			for _, src := range srcs {
				if src.BlockID == "" || src.BlockID == n.ID {
					continue
				}
				if _, ok := nodesByID[src.BlockID]; !ok {
					hasExternalRef[n.ID] = true
				}
			}
		}
		for i := range s.Nodes {
			n := &s.Nodes[i]
			if branchTargets[n.ID] {
				continue // data arrives via the branch source echo
			}
			// 受门控节点不得被 START 无条件触发（N-LOOP-2）。body 里引用
			// 外部（循环容器）数据的节点如果一律接 START，就会在 `while`
			// 守卫为假的那一轮照样执行 —— `while` 在 IR 里投影为
			// `if (cond) { body } else { break }`，守卫区域内的 if/比较节点
			// 只读循环状态、没有其它控制前驱，于是成了「每次迭代都跑」的
			// 根节点。实测：`i=0; while (i<3) { i=i+1; if (i==3) out=out+1; }`
			// 修复前 out=2（多跑一轮），正确 = 1。门控节点的输入由分支源的
			// context echo 逐跳携带（见 assemble.go resolveRef 的嵌套回声），
			// 不依赖 START；零入边的入口节点仍然接 START。
			if incoming[n.ID] == 0 || (hasExternalRef[n.ID] && gatedBy[n.ID] == "") {
				if err := g.AddEdge(compose.START, n.ID); err != nil {
					return nil, diag.New(diag.RSpecInvalid, "body start edge %s: %v", n.ID, err)
				}
			}
		}
	} else {
		if b.entryID == "" {
			return nil, diag.New(diag.RSpecInvalid, "spec has no entry node")
		}
		if err := g.AddEdge(compose.START, b.entryID); err != nil {
			return nil, diag.New(diag.RSpecInvalid, "start edge: %v", err)
		}
		for i := range s.Nodes {
			n := &s.Nodes[i]
			if n.ID == b.entryID {
				continue
			}
			if incoming[n.ID] == 0 && !branchTargets[n.ID] && !deadAfterReturn[n.ID] {
				return nil, diag.New(diag.RUnreachableNode, "node %s (%s) has no incoming edges", n.ID, n.Kind)
			}
		}
	}

	// ── 5. END wiring ────────────────────────────────────────────────────
	// top level: every terminal End node → END; a question_answer with no
	// outgoing edge is terminal too (coze QA answers end the workflow).
	outgoing := map[string]int{}
	outgoingControl := map[string]int{} // 排除 loop-back（被驱动器吸收）
	for _, e := range s.Edges {
		outgoing[e.Source]++
		if e.TargetPort != spec.PortLoopInlineInput {
			outgoingControl[e.Source]++
		}
	}
	// body: loop-back sources + break/continue sinks → END (driver reads
	// markers/state from the merged body output).
	endWired := false
	endFrom := map[string]bool{}
	for i := range s.Nodes {
		n := &s.Nodes[i]
		what := ""
		switch {
		case deadAfterReturn[n.ID]:
			continue // 死代码节点不得成为图出口
		case n.Kind == spec.KindEnd && (n.ID == spec.CanonicalExitID || outgoingControl[n.ID] == 0):
			// 只有**规范出口**或**没有活出边**的 End 才是流程终止点。
			// 有出边的非规范 End = attempt/QA 臂端（控制流载体）：它把控制权
			// 交给后继，自己不得进 END —— 否则臂的值会作为图输出与真正的
			// 出口值争同一个 "output" 键（UnwrapOutput 合并顶层命名空间）。
			what = "end edge"
		case !external && n.Kind == spec.KindQA && outgoing[n.ID] == 0:
			what = "qa end edge"
		case external && (n.Kind == spec.KindBreak || n.Kind == spec.KindContinue):
			what = "marker edge"
		case external && loopBackSources[n.ID]:
			what = "exit edge"
		}
		if what == "" {
			continue
		}
		if err := g.AddEdge(n.ID, compose.END); err != nil {
			return nil, diag.New(diag.RSpecInvalid, "%s %s: %v", what, n.ID, err)
		}
		endFrom[n.ID] = true
		endWired = true
	}
	// 体图出口面补齐（R-142）：loop 的收集输出引用的**体内非尾节点**也必须进
	// 出口面 —— eino 的体图返回 = END 的**数据前驱**命名空间集合
	// （`dagChannel.reportValues` 只收 `DataPredecessors`），而平台的收集面是
	// 「体内任意节点」（按节点完成事件收集）。缺这条 ⇒ 引用体内中间节点时
	// 驱动器 `resolveRef` 取不到 ⇒ 声明了的输出静默变 nil（撞「失配 ≠ 静默丢弃」）。
	// 被跳过的节点由 eino 的 skip 级联（`channelManager.reportBranch` → 后继
	// `reportSkip`）标记就绪，不会把 END 挂死。
	if external && len(b.bodyExtraExit) > 0 {
		extra := make([]string, 0, len(b.bodyExtraExit))
		for id := range b.bodyExtraExit {
			if _, ok := nodesByID[id]; !ok {
				continue // 引用的是外层生产者：由驱动器转发，不在体内
			}
			if endFrom[id] {
				continue
			}
			extra = append(extra, id)
		}
		sort.Strings(extra)
		for _, id := range extra {
			if err := g.AddEdge(id, compose.END); err != nil {
				return nil, diag.New(diag.RSpecInvalid, "collect edge %s: %v", id, err)
			}
			endWired = true
		}
	}
	if !external && !endWired {
		return nil, diag.New(diag.RSpecInvalid, "spec has no terminal end node")
	}
	if external && !endWired {
		return nil, diag.New(diag.RLoopNoExit, "loop body %s has no exit junction", "")
	}

	// ── 6. compile ───────────────────────────────────────────────────────
	compileOpts := append([]compose.GraphCompileOption{compose.WithNodeTriggerMode(compose.AllPredecessor)}, opts...)
	r, err := g.Compile(ctx, compileOpts...)
	if err != nil {
		return nil, diag.New(diag.RSpecInvalid, "eino compile: %v", err)
	}
	return r, nil
}

// branchKeyOf maps the "$branch" marker in a branch source's namespaced
// output to the port's target node key.
func branchKeyOf(nodeID string, in map[string]any, portTarget map[string]string) (string, error) {
	ns, ok := in[nodeID].(map[string]any)
	if !ok {
		return "", diag.New(diag.RSpecInvalid, "branch source %s: missing namespaced output", nodeID)
	}
	key, _ := ns["$branch"].(string)
	if key == "" {
		return "", diag.New(diag.RSpecInvalid, "branch source %s: node did not emit a $branch key", nodeID)
	}
	target, ok := portTarget[key]
	if !ok {
		return "", diag.New(diag.RSpecInvalid, "branch source %s: $branch %q has no declared target (have %v)", nodeID, key, keysOf(portTarget))
	}
	return target, nil
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// splitKey splits an edge key built by addEdge (src\x00dst).
func splitKey(k string) []string {
	sep := "\x00"
	for i := 0; i+len(sep) <= len(k); i++ {
		if k[i:i+len(sep)] == sep {
			return []string{k[:i], k[i+len(sep):]}
		}
	}
	return []string{k, ""}
}

// UnwrapOutput flattens the raw graph output ({endID: {name: value}, ...})
// into a single result map ({name: value}), merging all terminal ends.
//
// null 规则（N-NULL-1）：未执行分支的终端 End 也会作为 END 的数据前驱出现，
// 其值为 nil —— 它**不得覆盖**已由活臂产出的值。否则「同一个 output 键」
// 的归属取决于命名空间遍历顺序（一手：attempt 尾语句失败臂，活臂 "err" 被
// 未走 success 臂的 nil 吃掉，实测 300/300 为 nil）。显式 null 全为 nil 时
// 结果仍是 nil，语义不变。
func UnwrapOutput(out map[string]any) map[string]any {
	res := map[string]any{}
	for _, v := range out {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for k, val := range m {
			if val == nil {
				if _, seen := res[k]; seen {
					continue // 未执行分支的零值不得覆盖活值
				}
			}
			res[k] = val
		}
	}
	return res
}
