package state

// state.go — 执行期共享状态（PRD M6/D7 状态面）。
//
// RuntimeState 经 Eino WithGenLocalState 注入主图（每运行一次）,节点与
// loop 驱动器用 compose.ProcessState[*RuntimeState] 读写。**「并发安全」
// 不是免费拿到的**：eino 的并行分支会各自 Invoke 一个 loopDriver ⇒
// Loops/stack 必须自带同步（mu,见 R-129）；LoopState 的字段（Vars/Cursor…）
// 仍是「同一 loop 同时只有一条执行流」的前提（并行循环体的迭代共享一个
// LoopState,故构建期拒 state/break/continue/return）。body
// 子图不挂 stateGen —— 驱动器直接 body.Invoke(ctx,...) 时 ctx 状态链原样
// 透传,循环状态跨迭代持久（每迭代若重建状态会丢变量,故只在主图挂 gen）。
//
// v1 接线范围:LoopState（循环变量 / 游标 / break-continue 标记 / 迭代
// 进度）完整落地,取代 loop 驱动器对 body 输出的 scanPort 扫描。Entry/
// ConvVars 为 PRD 保留字段,待变量解析里程碑写入。
// M16-2 (v2):HITL-in-loop resume 需要把循环进度带过 checkpoint ——
// NextIter（下一个待执行迭代）与 IterOuts（已完成的逐迭代输出）随
// RuntimeState 进主 checkpoint（驱动器在 body 中断时经 CompositeInterrupt
// 使外层 runner 快照状态）。stack 字段不导出即不参与 gob 深拷贝。

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// init 注册序列化类型:checkpoint 深拷贝（deepCopyState）走 eino 内部
// 序列化器 + gob,interface 位置的自定义结构必须注册（否则 QA 中断恢复
// 报 "unknown type: state.RuntimeState"）。字段值的 JSON 原生类型（string/
// float64/bool/[]any/map）由 eino 内置注册覆盖;stack 未导出不参与拷贝。
func init() {
	schema.Register[RuntimeState]()
	schema.Register[LoopState]()
}

// LoopState 是单个 loop 容器在一次运行中的状态。
type LoopState struct {
	Cursor   int            // 当前迭代游标（0-based）
	Vars     map[string]any // 循环状态变量（loop_assign 写,驱动器/输出源读）
	Break    bool           // 体节点 break 标记（取代 $break 端口扫描）
	Continue bool           // 体节点 continue 标记
	// Returned / ReturnValue:M17 loop 内 return 语义。loop_return 节点
	// 写 Returned=true + ReturnValue=值 + Break=true（终止迭代）;loop
	// 驱动器读这三个字段决定短路输出（returned/return_value 端口）。
	Returned    bool // loop 是否因 return 终止
	ReturnValue any  // return 携带的值（Returned=true 时有效）
	// M16-2 (v2):resume 进度。主 checkpoint 在 body 中断瞬间快照
	// RuntimeState,resume 时驱动器从 NextIter 快进、从 IterOuts 恢复
	// 已完成迭代的输出 —— 不重放已完成迭代的陈旧中断（每迭代 QA 确认
	// 语义,见 loop.go 的 Invoke 注释与 spike per-iter 变体）。
	NextIter int              // 下一个待执行迭代下标（0-based,resume 快进）
	IterOuts map[string][]any // 已完成的逐迭代输出累积（按输出端口名）
	// ArrayInputs:by_array/含 item 绑定的循环在首轮入口解析出的输入数组。
	// 内部中断（StatefulInterrupt）默认不持久化节点输入（eino:需要
	// WithGraphInterrupt 才捕获 originalInput）,resume 时 loop 节点输入
	// 通道是零值 —— 驱动器把数组存档进 LoopState,resume 用它恢复迭代
	// 计数与 item 绑定,不依赖调用方 ctx 姿势。
	ArrayInputs [][]any // 按 cfg.Inputs 顺序的输入数组（resume 兜底）
	// curBind 是**当前迭代**的循环状态读视图（body 输入里 loopID 命名空间
	// 指向的 map）。它不是持久状态:未导出 ⇒ 不参与 checkpoint gob 拷贝,
	// 每次 body.Invoke 前由驱动器安装、返回后摘除。
	//
	// 为什么必须有:body 内对循环状态变量的引用在 spec 里编码为
	// `Source{BlockID: loopID, Name: var}`,解析时读这张 map。若它只是
	// 迭代开始时的快照,`i = i + 1; if (i == 0)` 会读到自增前的 0 ——
	// 同一迭代内「写完再读」必须读到新值（语句序）。写入口唯一:
	// loop_assign 经 SetVar 同时写 Vars 与这张视图。
	curBind map[string]any
}

// BindIteration 安装本迭代的循环状态读视图（驱动器在 body.Invoke 前调用）。
func (ls *LoopState) BindIteration(bind map[string]any) { ls.curBind = bind }

// UnbindIteration 摘除读视图（body.Invoke 返回后立即调用,含错误/中断路径）。
func (ls *LoopState) UnbindIteration() { ls.curBind = nil }

// SetVar 写循环状态变量,并同步到本迭代读视图（若已安装）——保证同一迭代
// 内后续语句读到刚写入的值,而不是迭代开始快照。
func (ls *LoopState) SetVar(name string, v any) {
	ls.Vars[name] = v
	if ls.curBind != nil {
		ls.curBind[name] = v
	}
}

// RuntimeState 是整图共享执行状态。
type RuntimeState struct {
	Entry    map[string]any        // entry 参数快照（v1 保留字段）
	ConvVars map[string]any        // 对话/工作流级变量（v1 保留字段）
	Loops    map[string]*LoopState // loopID → 循环状态（嵌套循环共存）
	stack    []string              // 激活 loop 栈（驱动器进入/退出,标记定位）
	// mu 保护 Loops / stack。**为什么必须有**（R-129，2026-09-28 一手）：
	// loop 驱动器**可以并发**——eino 的并行分支各自 Invoke 一个 loopDriver
	// ⇒ 两个 loop 同时 EnterLoop；未同步时命中 `fatal error: concurrent map
	// writes`（并发的 `s.Loops[id] = ls`），整趟 rt-l3 跑批被内核 abort
	// （一手：672 源跑批 RC=2，栈顶 = state.go EnterLoop ← loop.go Invoke）。
	// 本字段不导出 ⇒ 与 stack 一样不参与 checkpoint 的 gob 深拷贝。
	mu sync.Mutex
}

// New builds an empty RuntimeState（主图 stateGen 用）。
func New() *RuntimeState {
	return &RuntimeState{
		Entry:    map[string]any{},
		ConvVars: map[string]any{},
		Loops:    map[string]*LoopState{},
	}
}

// EnterLoop 进入 loop id 的执行上下文（一次 per loop 运行,defer PopLoop）。
// 返回该 loop 的状态;Vars 在迭代间持久。已存在时复用（嵌套 loop 各自
// 独立 key,互不覆盖）。
func (s *RuntimeState) EnterLoop(id string) *LoopState {
	s.mu.Lock()
	defer s.mu.Unlock()
	ls := s.Loops[id]
	if ls == nil {
		ls = &LoopState{Vars: map[string]any{}}
		s.Loops[id] = ls
	}
	s.stack = append(s.stack, id)
	return ls
}

// PopLoop 退出最内层 loop 上下文（驱动器 defer 调用）。
func (s *RuntimeState) PopLoop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.stack) > 0 {
		s.stack = s.stack[:len(s.stack)-1]
	}
}

// Loop 返回指定 loop 的状态（未运行过 → nil）。
func (s *RuntimeState) Loop(id string) *LoopState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Loops[id]
}

// TopLoop 返回最内层激活 loop 的状态（break/continue 标记定位目标）。
func (s *RuntimeState) TopLoop() *LoopState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.stack) == 0 {
		return nil
	}
	return s.Loops[s.stack[len(s.stack)-1]]
}

// Get 从 ctx 取 RuntimeState（无状态 → 错误;节点在非图上下文调用时缺失）。
func Get(ctx context.Context) (*RuntimeState, error) {
	var rs *RuntimeState
	err := compose.ProcessState(ctx, func(_ context.Context, s *RuntimeState) error {
		rs = s
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rs, nil
}

// IterMarkers 是并行迭代的**本地**控制标记。
//
// 并行 worker 不能写共享 LoopState（数据竞争，且一个迭代的 continue 会
// 泄漏到其它迭代）。break/continue 节点在存在该盒子时只写盒子，驱动器在
// body 返回后读取自己那一份。loop_return 在并行体构建期已被拒绝，故盒子
// 不携带 return 状态。
type IterMarkers struct {
	Break    bool
	Continue bool
}

type iterMarkersKey struct{}

// WithIterMarkers returns a context carrying a fresh per-iteration box.
func WithIterMarkers(ctx context.Context) (context.Context, *IterMarkers) {
	m := &IterMarkers{}
	return context.WithValue(ctx, iterMarkersKey{}, m), m
}

// IterMarkersFrom returns the per-iteration box, or nil outside a parallel
// iteration.
func IterMarkersFrom(ctx context.Context) *IterMarkers {
	m, _ := ctx.Value(iterMarkersKey{}).(*IterMarkers)
	return m
}
