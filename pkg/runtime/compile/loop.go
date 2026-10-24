package compile

// loop.go — M1 最小 loop 驱动器（PRD M3 骨架）。
//
// 语义契约（与 build.go 的投影/coze 语义对齐）:
//   - loop 容器在 Eino 图里是一个普通节点;驱动器内部持 body 子图 Runnable
//     （递归编译 Children+InnerEdges,loop-back 边被吸收）。
//   - 每迭代 body 输入 = 转发的外层合并输入 + {loopID: {item/state 绑定}};
//     body 子节点对容器/外层生产者的引用都从该输入解析。
//   - state 更新:loop_assign 节点经 ProcessState 直写 RuntimeState.Loops
//     （M2.3 正式化,取代 M1 的 scanPort 端口扫描;entry/conv vars 保留字段）。
//   - break/continue:体节点经 ProcessState 写 RuntimeState 的 Break/Continue
//     标记（break/continue 节点仍额外接 body END,见 compile.go step 5,输出
//     端口保留但驱动器不再扫描）。
//   - ErrorHandleMode:terminate / continue-on-error / remove-abnormal-output。
//   - IsParallel:M2.3 编译期拒绝（I3.3）——v1 驱动器只做确定性串行;静默降级（已被并行驱动替代）
//     为串行会掩盖语义（并行错误处理/顺序语义）差异,fail-fast 更诚实。

import (
	"context"
	"strconv"
	"sync"

	"github.com/cloudwego/eino/compose"

	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/state"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// marker ports emitted by break/continue sinks (D5) — canonical constants
// live in the nodes package (they are the node/driver contract).

// loopDriver is the loop container implementation (M1 driver, M2.3 state face).
type loopDriver struct {
	id   string
	body Runnable
	cfg  *spec.LoopConfig
	// ckpt: per-iteration checkpoint 是否启用（主图配了 checkpoint store）。
	// M16-2 (v2) 需要 —— body 内 QA 中断按迭代 id 落盘/恢复;无 store 时
	// 给 body 传 WithCheckPointID 会在 run() 顶部硬报错,故 build 期探测。
	ckpt bool
	// parallel: is_parallel 容器 —— 迭代并发执行（ParallelNums 并发上限),
	// 输出按迭代位置保序;state/break/continue/return 在构建期已拒。
	parallel bool
	// semTrace observes per-iteration collect decisions (ES-045).
	semTrace func(trace.Record)
}

// buildLoopDriver compiles the loop body subgraph and wraps it.
func (b *buildGraph) buildLoopDriver(ctx context.Context, n *spec.NodeSpec, opts []compose.GraphCompileOption) (nodes.NodeImpl, error) {
	dec, err := spec.DecodeConfig(n.Kind, n.Config)
	if err != nil {
		return nil, diag.New(diag.RSpecInvalid, "loop %s config: %v", n.ID, err)
	}
	lc, ok := dec.(*spec.LoopConfig)
	if !ok {
		return nil, diag.New(diag.RSpecInvalid, "loop %s: config is %T, want LoopConfig", n.ID, dec)
	}
	// M16-2 (v2):探测主图是否配了 checkpoint store。无 store 时 per-
	// iteration checkpoint 不可用（body runner 对 WithCheckPointID 且
	// store==nil 会在 run() 顶部立即报错,不执行任何节点）。用空图 +
	// 相同 opts 探测:有 store → 空图正常返回;无 store → 报
	// "have not set checkpoint store"。空图无业务节点、成功路径不写
	// checkpoint,探测零副作用。
	ckpt := false
	if pr, perr := compose.NewGraph[map[string]any, map[string]any]().Compile(ctx, opts...); perr == nil {
		if _, ierr := pr.Invoke(ctx, map[string]any{}, compose.WithCheckPointID("probe")); ierr == nil {
			ckpt = true
		}
	}
	if lc.IsParallel {
		// 并行循环（batch 容器,IsParallel）:并发跑迭代,输出按位置保序。
		// 仅在无法表达并行语义的特性上 fail-fast（语料 339 例全部是
		// 纯 by_array + 无 state/break/continue/return,全特性面见
		// docs/ROUNDTRIP-DIFFS.md §6.4）:
		//   - 并行 + checkpoint（I3.3 原始冲突）:per-iteration checkpoint
		//     恢复依赖串行进度（NextIter/IterOuts 快照）,并行下无定义。
		//   - 并行 + state 写 / break / continue / return:语义在并发下
		//     无定义（数据竞争 / 迭代序不保序）,不做静默串行降级。
		if ckpt {
			return nil, diag.New(diag.RParallelWithCheckpoint,
				"loop %s: is_parallel=true + per-iteration checkpoint resume is not supported (I3.3)", n.ID)
		}
		if len(lc.StateValues) > 0 {
			return nil, diag.New(diag.RKindRejected,
				"loop %s: parallel loop does not support state variables (%v)", n.ID, skeys(lc.StateValues))
		}
		if lc.ReturnValue != nil {
			return nil, diag.New(diag.RKindRejected,
				"loop %s: parallel loop does not support body return short-circuit", n.ID)
		}
		for _, cn := range lc.Children {
			switch cn.Kind {
			// continue is iteration-local: the marker lands in a per-worker
			// box (state.WithIterMarkers) and the slot is compacted out, which
			// matches the serial driver. break/return/assign still cross
			// iteration boundaries and stay rejected.
			case spec.KindLoop:
				// A nested loop keeps its progress in LoopState keyed by node
				// id — every worker of the outer parallel loop would share and
				// concurrently write it (observed wrong results / races).
				// Zero corpus workflows nest a loop inside a parallel body
				// (869 specs); fail closed instead of guessing.
				return nil, diag.New(diag.RKindRejected,
					"loop %s: parallel loop body does not support a nested loop container (shared LoopState across workers)", n.ID)
			case spec.KindBreak, spec.KindLoopReturn, spec.KindLoopAssign:
				return nil, diag.New(diag.RKindRejected,
					"loop %s: parallel loop body does not support %s node", n.ID, cn.Kind)
			}
		}
	}
	bodySpec := &spec.Spec{Version: spec.Version, Nodes: lc.Children, Edges: lc.InnerEdges}
	// 体图出口面 = 收集输出引用的体内节点（R-142）。与 external/entryID 同款
	// save-restore：嵌套 loop 的体构建会覆写该字段，外层构建必须拿回自己的。
	savedExit := b.bodyExtraExit
	b.bodyExtraExit = loopBodyExitNodes(lc)
	body, err := b.build(ctx, bodySpec, true, opts)
	b.bodyExtraExit = savedExit
	if err != nil {
		return nil, err
	}
	return &loopDriver{id: n.ID, body: body, cfg: lc, ckpt: ckpt, parallel: lc.IsParallel, semTrace: b.deps.SemTrace}, nil
}

// loopBodyExitNodes 返回 loop 的收集输出所引用的**体内节点**集合（R-142）。
//
// 平台语义（一手 = coze-studio `backend/domain/workflow/internal/nodes/loop/loop.go`）：
// loop 的输出是按**节点完成事件**收集的（`setIthOutput` 在节点完成时写
// `output[k]`），`outputs[]` 因此允许引用体内**任意**节点 —— 不限于接到循环体
// 出口的那个。我们的体图出口面却只有「END 的数据前驱」（eino 的体图返回值 =
// END 的 DataPredecessors 命名空间集合）⇒ 引用体内非尾节点时，该节点的命名空间
// 根本不在 `body.Invoke` 的返回里 ⇒ `resolveRef` 返回 nil ⇒ 声明了的输出静默
// 消失（撞 AGENTS.md「失配 ≠ 静默丢弃」）。
//
// 这里给出的集合会被体图构建期接到 END 上（compile.go 步骤 5），把出口面补齐到
// 平台口径。
func loopBodyExitNodes(lc *spec.LoopConfig) map[string]bool {
	out := map[string]bool{}
	for _, src := range lc.OutputSources {
		if src.BlockID == "" {
			continue // 字面量源：没有体内生产者
		}
		out[src.BlockID] = true
	}
	return out
}

// Invoke drives the loop iterations. Loop state lives in RuntimeState
// (Loops[loopID].Vars), written by loop_assign body nodes via ProcessState;
// the driver binds current values into the body input map each iteration
// (data contract: body refs to loop ports resolve from the forwarded map).
//
// M16-2 (v2):HITL-in-loop resume。每迭代以独立 checkpoint id
// （iter-<loopID>-<i>,仅当主图配了 checkpoint store）驱动 body —— body
// 内 QA 中断按迭代落盘/恢复,不互相串扰。body 中断 err（interruptError）
// 经 CompositeInterrupt 提升为 signal,使外层 runner 识别为可恢复中断并
// 写入主 checkpoint（含 RuntimeState 进度快照）;否则外层把中断当普通
// 节点错误,进度（NextIter/IterOuts/Vars）随运行结束丢失。
// resume 时主 checkpoint 恢复 RuntimeState:从 ls.NextIter 快进、从
// ls.IterOuts 恢复已完成迭代输出 —— 不重放已完成迭代的陈旧中断
// （每迭代 QA 确认语义:一次 resume 只放行当前迭代,下一迭代重新中断）。
func (d *loopDriver) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	cfg := d.cfg

	rs, err := state.Get(ctx)
	if err != nil {
		return nil, diag.New(diag.RNodeError, "loop %s: RuntimeState missing (graph compiled without WithGenLocalState): %v", d.id, err)
	}
	ls := rs.EnterLoop(d.id)
	defer rs.PopLoop()

	// resolve input arrays (by_array / item binding)。首轮入口存档进
	// LoopState:内部中断不持久化节点输入,resume 时 loop 节点输入通道是
	// 零值 —— 用存档恢复迭代计数与 item 绑定（见 LoopState.ArrayInputs）。
	arrays := make([][]any, 0, len(cfg.Inputs))
	for _, src := range cfg.Inputs {
		arrays = append(arrays, toSlice(resolveRef(in, src.BlockID, src.Name)))
	}
	empty := true
	for _, a := range arrays {
		if len(a) > 0 {
			empty = false
			break
		}
	}
	if !empty {
		ls.ArrayInputs = arrays
	} else if ls.ArrayInputs != nil {
		arrays = ls.ArrayInputs
	}

	// state init (literal or upstream ref) — first iteration only; Vars
	// persist across iterations once written (loop_assign owns updates).
	// resume 时 Vars 经主 checkpoint 恢复,已存在则跳过重初始化（否则
	// resume 会把状态变量重置回初值）。
	for name, src := range cfg.StateValues {
		if _, ok := ls.Vars[name]; ok {
			continue
		}
		if src.BlockID != "" {
			ls.Vars[name] = resolveRef(in, src.BlockID, src.Name)
		} else {
			ls.Vars[name] = src.Val
		}
	}

	n := d.iterationCount(arrays)
	if n <= 0 {
		n = 0
	}

	// per-iteration output values —— 累积进 LoopState（resume 恢复既有值;
	// 驱动器是唯一读写方,与局部变量等价但可跨 checkpoint 存活）。
	iters := ls.IterOuts
	if iters == nil {
		iters = map[string][]any{}
		ls.IterOuts = iters
	}
	finalState := map[string]any{} // state outputs (BlockID == loop id)

	if d.parallel {
		return d.invokeParallel(ctx, in, cfg, arrays, n, iters)
	}

	for i := ls.NextIter; i < n; i++ {
		// B 测试治理:驱动器每迭代检查 ctx 取消。body 空转/纯空图场景下
		// body.Invoke 可能不感知 cancel(空图立即返回),若缺省此检查,超长
		// 数组循环会悬挂到耗尽 —— ctx 取消是 loop 的唯一外部终止防护
		// (loop 无全局迭代上限,对齐 coze 语义)。
		if err := ctx.Err(); err != nil {
			return nil, diag.New(diag.RNodeError, "loop %s: cancelled: %v", d.id, err)
		}
		ls.Cursor = i
		ls.Break = false
		ls.Continue = false

		bodyInput := shallowClone(in)
		bind := map[string]any{}
		for k, v := range ls.Vars {
			bind[k] = v
		}
		if cfg.VarName != "" && len(arrays) > 0 && i < len(arrays[0]) {
			bind[cfg.VarName] = arrays[0][i]
		}
		bodyInput[d.id] = bind
		// 本迭代的循环状态读视图:loop_assign 经 SetVar 写穿它,使同一
		// 迭代内「写完再读」读到新值（迭代开始快照只用于第一条语句）。
		ls.BindIteration(bind)

		opts := []compose.Option{}
		if d.ckpt {
			opts = append(opts, compose.WithCheckPointID(loopIterKey(d.id, i)))
		}
		bodyOut, err := d.body.Invoke(ctx, bodyInput, opts...)
		ls.UnbindIteration()
		if err != nil {
			// HITL 中断:提升为 composite signal 交给外层 runner（写主
			// checkpoint + 快照 RuntimeState 进度）。不得被 ErrorHandleMode
			// 吞掉 —— 吞掉中断等于静默跳过确认,运行会失真。
			if _, ok := compose.ExtractInterruptInfo(err); ok {
				return nil, compose.CompositeInterrupt(ctx, nil, nil, err)
			}
			switch cfg.ErrorHandleMode {
			case "continue-on-error":
				// 留空位（保序）：失败迭代在输出列表里保留**一个 nil 槽**。
				// 正本 = `docs/five-step/spec/F3-error-policy-container-axis.md` §①
				// （`keep_null` = 该位留空、其余结果保序）；平台同一语义的出处 =
				// 参考目标 `IterationNode._run` 的 `CONTINUE_ON_ERROR: outputs[index] = None`
				// —— 它**不进入**随后的 remove 过滤。
				//
				// **不得压缩**：压缩会让 `keep_null` 与 `remove_failed` 在执行面
				// 塌成同一个行为（2026-09-29 一手发现：本文件与两个测试曾把压缩
				// 锁成「规范」，而正本 / 类型层 / 平台出处三方都说留空位）。
				appendNilIterOutputs(iters, cfg, d.id)
				continue
			case "remove-abnormal-output":
				continue
			default:
				return nil, err
			}
		}

		// markers (written by break/continue/loop_return body nodes via
		// ProcessState)。loop_return 同时置 Break 与 Returned —— 短路值
		// 由输出契约（returned/return_value 端口）携带,驱动器不吞掉
		// Returned（break 会正常跳出,returned 标记保持）。
		if ls.Returned || ls.Break {
			break
		}
		if ls.Continue {
			continue
		}
		ls.NextIter = i + 1

		// per-iteration outputs (skipped by break/continue)
		//
		// ES-045 three-state collection: the body either did not emit the port
		// (absent_skip: no element), emitted an explicit null (null_append, or
		// null_error when the declared element kind cannot hold null), or
		// emitted a zero (zero_append: the element is kept as zero). Collapsing
		// null into absent would silently drop a value the body produced.
		for _, osrc := range cfg.OutputSources {
			if osrc.BlockID == d.id {
				continue // state outputs collected after the loop
			}
			name := loopOutputKey(osrc)
			v, present := resolveRefPresent(bodyOut, osrc.BlockID, osrc.Name)
			if !present {
				d.emitCollect(name, "absent_skip")
				continue
			}
			if v == nil {
				// null is a produced element, never conflated with "the body
				// did not emit the port" (that is absent_skip above).
				//
				// ES-045 also names a `null_error` arm for element types that
				// cannot hold null (array<integer>). No such carrier exists in
				// ncoda's type system: ir.Type/spec.TypeSpec have no nullable
				// bit, so `array<null|T>` and `array<integer>` are the same
				// TypeSpec. Rejecting by kind would break ES-040/AC7 (the
				// platform accepts null elements and repairs them at the
				// downstream code-input prefill, which is corpus-observed).
				// The arm therefore stays unimplemented rather than guessed;
				// the boundary is recorded in the test-spec and EXEC-SEMANTICS.
				d.emitCollect(name, "null_append")
			} else if compileZero(v) {
				d.emitCollect(name, "zero_append")
			}
			iters[name] = append(iters[name], v)
		}
	}

	// state outputs: loop final state (Vars), also after break/continue —
	// the loop's accumulated result is part of the return contract.
	// M17:returned/return_value 是 loop 自身的输出端口（非 Vars）——returned
	// 由 loop_return 标记,return_value 携带短路值（无 return 时 nil,
	// 短路 aggregate 的 first_non_zero 会跳过 nil/零值取正常尾部；return 零值时尾部臂被门控跳过、同样为零 ⇒ 结果仍是零）。
	for _, osrc := range cfg.OutputSources {
		if osrc.BlockID != d.id {
			continue
		}
		name := loopOutputKey(osrc)
		switch name {
		case "returned":
			finalState[name] = ls.Returned
		case "return_value":
			finalState[name] = ls.ReturnValue
		default:
			finalState[name] = ls.Vars[osrc.Name]
		}
	}

	return assembleLoopOutputs(iters, finalState, cfg, d.id), nil
}

// loopOutputKey 是一个 output source 在 loop 输出表里的键。
// 与推断端同解：平台 Build 期直接要求 `len(info.Path) == 1` 并取 `info.Path[0]`
// （coze-studio `nodes/loop/loop.go::Build`），所以末段即键。
func loopOutputKey(src spec.Source) string {
	if name := lastPathSeg(src.Path); name != "" {
		return name
	}
	return src.Name
}

// appendNilIterOutputs 给每个**逐迭代**输出各追加一个 nil 槽（留空位语义）。
//
// 与 invokeParallel 的预分配槽位同一条语义（串行/并行两驱动必须同解）：失败迭代
// 在结果里**保留位置**、值为 nil。只处理 `BlockID != loopID` 的逐迭代输出 ——
// loop 自身的状态端口（returned / return_value / 状态 var）由 finalState 承载。
func appendNilIterOutputs(iters map[string][]any, cfg *spec.LoopConfig, loopID string) {
	for _, src := range cfg.OutputSources {
		if src.BlockID == loopID {
			continue
		}
		name := loopOutputKey(src)
		if name == "" {
			continue
		}
		iters[name] = append(iters[name], nil)
	}
}

// preRegisterLoopOutputs 把 loop 的**逐迭代声明输出**先按**空列表**放进输出表。
//
// 为什么必须预注册（平台语义，一手 = coze-studio `nodes/loop/loop.go`）：
//
//	output = make(map[string]any, len(l.outputs))
//	for k := range l.outputs { output[k] = make([]any, 0) }   // ← 迭代前先占位
//	...每迭代 setIthOutput 里 append...
//	return output, nil                                        // ← 零迭代也返回它
//
// ⇒ **零迭代**（`by_array` 喂空数组）时平台返回的是 `{k: []}`，**不是「没有这个键」**。
// 我们此前只从 `iters` 组装 ⇒ 零迭代 ⇒ `map[]` ⇒ 下游 `resolveRef` 取不到 ⇒ 声明了的
// 输出**静默消失**（撞 AGENTS.md「失配 ≠ 静默丢弃」）；它在 code 消费者面前被
// `normalizeInputs` 的零值吸收掩盖，只在非 code 消费者（prompt / literal Text）暴露。
//
// 只预注册**逐迭代**输出（`BlockID != loopID`）：loop 自身的状态端口
// （`returned` / `return_value` / 状态 var）是 Vars 语义，由 `finalState` 承载 ——
// 把它们塞成空列表会改掉真假值（`returned` 应为 bool）。
func preRegisterLoopOutputs(out map[string]any, cfg *spec.LoopConfig, loopID string) {
	for _, src := range cfg.OutputSources {
		if src.BlockID == loopID {
			continue // 状态端口：finalState 承载
		}
		name := loopOutputKey(src)
		if name == "" {
			continue
		}
		if _, ok := out[name]; ok {
			continue
		}
		if cfg.FlattenOutput && name == "output" {
			out[name] = flattenList(nil)
			continue
		}
		out[name] = []any{}
	}
}

// assembleLoopOutputs 组装 loop 输出:迭代输出（保序列表,并行/串行共用）
// + state 输出。FlattenOutput 只作用于名为 "output" 的端口（参考目标 契约）。
func assembleLoopOutputs(iters map[string][]any, finalState map[string]any, cfg *spec.LoopConfig, loopID string) map[string]any {
	out := map[string]any{}
	preRegisterLoopOutputs(out, cfg, loopID)
	for name, vals := range iters {
		out[name] = vals
		if cfg.FlattenOutput && name == "output" {
			out[name] = flattenList(vals)
		}
	}
	for name, v := range finalState {
		out[name] = v
	}
	return out
}

// invokeParallel 驱动并行循环（is_parallel 容器）:
//   - 最多 ParallelNums 个 worker 并发跑迭代（默认 10,参考目标 契约）;
//   - 输出按迭代位置写预分配槽位 —— 并发执行不改变输出序（位置保序,
//     与串行驱动输出一致,round3 语义不动点成立的前提）;
//   - error_handle_mode=terminated（默认）:首个错误取消其余 worker,返回错误;
//     continue-on-error:吞错 + 失败迭代槽位**留 nil**（保序）;
//     remove-abnormal-output:吞错 + 失败迭代**压缩掉**（结果变短）。
//   - state/break/return 在构建期已拒（跨迭代语义无定义）；continue 是
//     迭代本地的（每 worker 标记盒 + 跳过槽位压缩）。
func (d *loopDriver) invokeParallel(ctx context.Context, in map[string]any, cfg *spec.LoopConfig, arrays [][]any, n int, iters map[string][]any) (map[string]any, error) {
	// 预分配输出槽位（位置保序）。resume 恢复的既有值（串行路径写入的
	// IterOuts）不缩容 —— 并行 + ckpt 构建期已拒,理论不可达,防御性保留。
	for _, osrc := range cfg.OutputSources {
		if osrc.BlockID == d.id {
			continue
		}
		name := lastPathSeg(osrc.Path)
		if name == "" {
			name = osrc.Name
		}
		if len(iters[name]) < n {
			iters[name] = append(iters[name], make([]any, n-len(iters[name]))...)
		}
	}

	workers := cfg.ParallelNums
	if workers <= 0 {
		workers = 10 // 参考目标 默认 parallel_nums
	}
	if workers > n {
		workers = n
	}
	if workers < 1 {
		workers = 1
	}

	// written[name][i]:第 i 迭代是否成功写槽 —— **remove-abnormal-output
	// 压缩**失败迭代的依据（nil 是合法值,不能靠值判空洞;`continue-on-error`
	// 不压缩，槽位保持 nil 即留空位）。
	written := map[string][]bool{}
	continued := make([]bool, n)
	for _, osrc := range cfg.OutputSources {
		if osrc.BlockID == d.id {
			continue
		}
		name := lastPathSeg(osrc.Path)
		if name == "" {
			name = osrc.Name
		}
		written[name] = make([]bool, n)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	idx := make(chan int, n)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				if ctx.Err() != nil {
					return
				}
				if cont, err := d.runParallelIter(ctx, i, in, cfg, arrays, iters, written); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					if cfg.ErrorHandleMode == "" || cfg.ErrorHandleMode == "terminated" {
						cancel() // 快速失败:取消其余迭代
					}
				} else if cont {
					mu.Lock()
					continued[i] = true
					mu.Unlock()
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		idx <- i
	}
	close(idx)
	wg.Wait()
	// terminate（默认）:首个错误即失败。
	if firstErr != nil && (cfg.ErrorHandleMode == "" || cfg.ErrorHandleMode == "terminated") {
		return nil, firstErr
	}
	// 失败迭代的**删 / 留 = 轴本身**（正本 §①）：
	//   · `remove-abnormal-output` ⇒ **压缩**（失败位置从结果里去掉）；
	//   · `continue-on-error`     ⇒ **留空位**（位置保留、值为 nil，见上面的
	//     预分配槽位）—— 与函数头注「失败迭代槽位留 nil」一致。
	// 两者必须**可区分**，否则 `keep_null` 与 `remove_failed` 在 ncoda 自己的
	// 执行面是同一个行为（2026-09-29 一手发现并修：原先两条拼写共用压缩）。
	compact := cfg.ErrorHandleMode == "remove-abnormal-output"
	anyContinued := false
	for _, c := range continued {
		if c {
			anyContinued = true
			break
		}
	}
	if compact || anyContinued {
		for name, wm := range written {
			compacted := make([]any, 0, n)
			for i := 0; i < n; i++ {
				if continued[i] {
					continue // continue: iteration produced no slot (serial parity)
				}
				if compact && !wm[i] {
					continue // failed iteration: remove-abnormal-output
				}
				if i < len(iters[name]) {
					compacted = append(compacted, iters[name][i])
				}
			}
			iters[name] = compacted
		}
	}
	return assembleLoopOutputs(iters, map[string]any{}, cfg, d.id), nil
}

// runParallelIter 执行第 i 次迭代:绑定迭代变量 → body.Invoke → 按位置
// 写输出槽位。body 内无 state/break/continue/return（构建期已拒）,对
// RuntimeState 零写,多个 worker 共享编译后的 body Runnable 安全。
func (d *loopDriver) runParallelIter(ctx context.Context, i int, in map[string]any, cfg *spec.LoopConfig, arrays [][]any, iters map[string][]any, written map[string][]bool) (bool, error) {
	bodyInput := shallowClone(in)
	bind := map[string]any{}
	if cfg.VarName != "" && len(arrays) > 0 && i < len(arrays[0]) {
		bind[cfg.VarName] = arrays[0][i]
	}
	bodyInput[d.id] = bind

	iterCtx, markers := state.WithIterMarkers(ctx)
	bodyOut, err := d.body.Invoke(iterCtx, bodyInput)
	if err != nil {
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			return false, err // HITL 中断:提升给外层 runner（并行 + ckpt 已拒,防御）
		}
		return false, err
	}
	if markers.Continue {
		return true, nil // iteration skipped: no output slot
	}
	for _, osrc := range cfg.OutputSources {
		if osrc.BlockID == d.id {
			continue
		}
		name := lastPathSeg(osrc.Path)
		if name == "" {
			name = osrc.Name
		}
		v, present := resolveRefPresent(bodyOut, osrc.BlockID, osrc.Name)
		// Parallel loops are positional by contract (slot i belongs to
		// iteration i), so absent_skip is not available here: an absent value
		// leaves the reserved slot null rather than renumbering later
		// iterations. The nullability rule still applies (ES-045 null_error).
		if present && v == nil {
			d.emitCollect(name, "null_append")
		}
		if i < len(iters[name]) {
			iters[name][i] = v
			if wm := written[name]; wm != nil && i < len(wm) {
				wm[i] = true
			}
		}
	}
	return false, nil
}

// loopIterKey 每迭代唯一的 checkpoint id（嵌套 loop 各节点 id 唯一,
// 外层 body 迭代与内层 loop 迭代互不串扰）。
func loopIterKey(loopID string, i int) string {
	return "iter-" + loopID + "-" + strconv.Itoa(i)
}

func (d *loopDriver) iterationCount(arrays [][]any) int {
	switch d.cfg.LoopType {
	case "by_iteration":
		return d.cfg.LoopCount
	case "by_array":
		if len(arrays) > 0 {
			return len(arrays[0])
		}
		return 0
	case "infinite":
		if d.cfg.LoopCount > 0 {
			return d.cfg.LoopCount
		}
		return 10
	default:
		return 0
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

// toSlice normalizes any list-ish value to []any (nil → nil).
func toSlice(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	case []string:
		out := make([]any, 0, len(t))
		for _, s := range t {
			out = append(out, s)
		}
		return out
	default:
		return []any{v}
	}
}

// shallowClone copies the outer namespaced map (inner producer maps are
// shared read-only).
func shallowClone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func lastPathSeg(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

// flattenList flattens one level of a list-of-lists (FlattenOutput).
func flattenList(vals []any) any {
	out := []any{}
	for _, v := range vals {
		if list, ok := v.([]any); ok {
			out = append(out, list...)
		} else {
			out = append(out, v)
		}
	}
	return out
}

// skeys 返回 map 键（错误消息里列出不支持的 state 变量名）。
func skeys(m map[string]spec.Source) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// emitCollect records one ES-045 collect decision. Values are never recorded
// (only the output port name), so the trace cannot leak payload data.
func (d *loopDriver) emitCollect(name, rule string) {
	if d.semTrace == nil {
		return
	}
	d.semTrace(trace.Record{
		Stage:       "fill",
		RuleID:      trace.RuleCollect,
		Origin:      trace.OriginPolicy,
		Evidence:    "hand:docs/AGGREGATION-HOISTING-ALGORITHM.md:63",
		Node:        d.id,
		Kind:        spec.KindLoop,
		CollectRule: rule,
		Attempt:     1,
		Axis:        "per_attempt",
		Input:       name,
		Output:      "<redacted>",
		Decision:    "passed",
	})
}

// compileZero mirrors the merge zero rule for loop element collection:
// nil (handled separately), "", 0, false and empty containers are zero.
func compileZero(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return x == ""
	case bool:
		return !x
	case int:
		return x == 0
	case int64:
		return x == 0
	case float64:
		return x == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}
