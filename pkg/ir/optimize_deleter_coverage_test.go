package ir

// optimize_deleter_coverage_test.go — 「判死 ⇒ 必须摘得掉」的**完整性门禁**（A12-H8）。
//
// 为什么需要：`optimizeGraph` 是「重算消费视图 → 判死 → 删除」的不动点循环，它的
// 终止性依赖一条**结构性**性质 —— **节点视图看得见的聚合槽位，删除面都必须摘得掉**。
// 视图（`collectIRNodes`，沿结构树收语句上的节点）与删除面（`pruneDeadAggregates*`，
// 按语句种类下降）是两份**手工维护**的枚举，于是「新增一个聚合载体字段但没管删除面」
// = 一条**静默**的不收敛通道：该聚合每轮被判死、每轮摘不掉 ⇒ `changed` 恒真 ⇒ 挂死
// （实测历史形态：`ReturnStmt.End.FlowExitMerge` 只在视图里、不在删除面上；一手触发
// 路径见 `A12-flow-exit-multi-record` 的「同 id 双 end」）。
//
// 门禁 = **逐语句类型 × 逐槽位**的反射扫描：对 `ir` 包里每一个 `Stmt` 实现，把它
// 每个 `*Aggregate` / `*End` 字段填一个**必死**的聚合（无下游数据引用），跑
// `optimizeGraph`，断言「不挂死、不 fail loud、且该聚合从节点视图里消失」。
// 反射枚举字段 ⇒ 新增载体**字段**自动进入扫描面；类型清单与接口实现集合对账 ⇒
// 新增语句**类型**漏登记直接红。
//
// 管辖范围（诚实口径，不过claim）：本门禁管的是**声明的聚合载体**（`*Aggregate`
// 字段、`*End` 的 `FlowExitMerge`，加上 `NodeStmt.Node` 这个叶子包装位）。
// `Node` / `interface` 型字段里塞聚合是**不变量之外**的形态（`IfStmt.Relay` 那类），
// 由运行时守卫兜住（契约测试 ⑤）；视图看不见的槽位（如 `AttemptStmt.SuccessEnd`
// 的 `FlowExitMerge`）压根不会被判死，故不在本判据内 —— 扫描会逐条打印跳过原因。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// stmtKindRegistry 是「本门禁认得的所有 Stmt 实现」的**登记表**：名字 → 零值构造器。
// 它与包内真实实现的集合由下面的 AST 对账保证一致（新增 Stmt 类型不登记 ⇒ 红）。
var stmtKindRegistry = map[string]func() Stmt{
	"NodeStmt":     func() Stmt { return &NodeStmt{} },
	"IfStmt":       func() Stmt { return &IfStmt{} },
	"AttemptStmt":  func() Stmt { return &AttemptStmt{} },
	"LoopStmt":     func() Stmt { return &LoopStmt{} },
	"ParallelStmt": func() Stmt { return &ParallelStmt{} },
	"QaStmt":       func() Stmt { return &QaStmt{} },
	"SwitchStmt":   func() Stmt { return &SwitchStmt{} },
	"ReturnStmt":   func() Stmt { return &ReturnStmt{} },
	"BreakStmt":    func() Stmt { return &BreakStmt{} },
	"ContinueStmt": func() Stmt { return &ContinueStmt{} },
	"InlineStmt":   func() Stmt { return &InlineStmt{} },
}

var (
	aggregatePtrType = reflect.TypeOf((*Aggregate)(nil))
	endPtrType       = reflect.TypeOf((*End)(nil))
)

// aggregateCarrierSlots 列出语句实例上的**声明聚合槽位**：名字 → 把给定聚合填进该槽位的动作。
// 枚举走反射（字段类型是 `*Aggregate` / `*End`），不是手抄字段名 —— 新增字段自动进扫描面。
func aggregateCarrierSlots(inst Stmt) map[string]func(*Aggregate) {
	v := reflect.ValueOf(inst).Elem()
	out := map[string]func(*Aggregate){}
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		slot := v.Field(i)
		switch f.Type {
		case aggregatePtrType:
			field := slot
			out[f.Name] = func(agg *Aggregate) { field.Set(reflect.ValueOf(agg)) }
		case endPtrType:
			field := slot
			out[f.Name+".FlowExitMerge"] = func(agg *Aggregate) {
				end, ok := field.Interface().(*End)
				if !ok || end == nil {
					end = &End{ID: "900900"}
					field.Set(reflect.ValueOf(end))
				}
				end.FlowExitMerge = agg
			}
		}
	}
	return out
}

// TestOptimizeDeleterCoversEveryDeclaredAggregateCarrier 是门禁本体。
func TestOptimizeDeleterCoversEveryDeclaredAggregateCarrier(t *testing.T) {
	exercised, skipped := 0, 0
	names := make([]string, 0, len(stmtKindRegistry))
	for name := range stmtKindRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ctor := stmtKindRegistry[name]
		// 槽位名先在一具探针实例上枚举；**真正写入必须在被测实例自身上**（反射的
		// 槽位 setter 绑定的是它枚举时那一具实例 —— 用探针实例写、把另一具实例塞进
		// 图里，扫描会静默地一个槽位都判不到，这正是本门禁要防的「空转」）。
		slotNames := make([]string, 0, 4)
		for s := range aggregateCarrierSlots(ctor()) {
			slotNames = append(slotNames, s)
		}
		// `NodeStmt` 的载体声明为 `Node`（叶子包装位）：聚合在那里是**树上的节点**，
		// 删除面摘它的方式是「连同语句一起丢掉」。它是视图的合法入口之一，
		// 所以显式纳入扫描（其余 `Node` 型字段不在本门禁管辖内，见文件头）。
		if name == "NodeStmt" {
			slotNames = append(slotNames, "Node")
		}
		sort.Strings(slotNames)
		for _, slot := range slotNames {
			inst := ctor()
			apply := aggregateCarrierSlots(inst)[slot]
			if apply == nil && slot == "Node" {
				leaf := inst.(*NodeStmt)
				apply = func(agg *Aggregate) { leaf.Node = agg }
			}
			agg := oneCandidatePhi("100777", "100004")
			apply(agg)
			g := &Graph{Root: &Block{Stmts: []Stmt{inst}}}
			if _, visible := collectIRNodes(g)[agg.ID]; !visible {
				// 视图看不见 ⇒ 不会被判死 ⇒ 与「判死 ⇒ 摘得掉」无关（例：
				// AttemptStmt.SuccessEnd 的 FlowExitMerge 今天不进节点视图）。
				t.Logf("跳过 %s.%s：该槽位的聚合不进节点视图（判死判据不覆盖它）", name, slot)
				skipped++
				continue
			}
			if msg := runOptimize(t, g); msg != "" {
				t.Errorf("%s.%s：可摘的死聚合不得 fail loud（视图/删除面在此槽位上应当一致）：%s", name, slot, msg)
				continue
			}
			if still, ok := collectIRNodes(g)[agg.ID]; ok {
				t.Errorf("%s.%s：判死的聚合仍在节点视图里（%T）⇒ changed 恒真 ⇒ 不动点不收敛；"+
					"删除面必须覆盖视图声明过的每一个聚合槽位", name, slot, still)
				continue
			}
			exercised++
		}
	}
	// 下限防止门禁「空转」：反射/构造一旦失效（字段改名、类型改名）必须红，
	// 不能静默地「一个槽位都没扫到还全绿」。
	if exercised < 5 || skipped < 1 {
		t.Fatalf("门禁空转：实际判定 %d 个槽位、跳过 %d 个（预期判定 ≥5、跳过 ≥1）"+
			"——检查 aggregateCarrierSlots 的枚举面", exercised, skipped)
	}
	t.Logf("判死覆盖：判定 %d 个槽位 · 跳过 %d 个（视图看不见 ⇒ 不在判据内）", exercised, skipped)
}

// TestOptimizeDeleterRegistryCoversEveryStmtKind 把登记表与包内真实的 `Stmt` 实现集合对账
// （门禁的类型级半边：新增语句类型不登记 ⇒ 红；登记表里出现已删除的类型 ⇒ 红）。
func TestOptimizeDeleterRegistryCoversEveryStmtKind(t *testing.T) {
	impls, err := stmtImplementationsInPackage()
	if err != nil {
		t.Fatalf("扫描包内 Stmt 实现失败：%v", err)
	}
	if len(impls) < 10 {
		t.Fatalf("只找到 %d 个 Stmt 实现（预期 ≥10）——门禁空转，检查解析路径", len(impls))
	}
	var missing, stale []string
	for name := range impls {
		if _, ok := stmtKindRegistry[name]; !ok {
			missing = append(missing, name+" ("+impls[name]+")")
		}
	}
	for name := range stmtKindRegistry {
		if _, ok := impls[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("聚合删除面门禁漏登记 %d 个 Stmt 实现：%v\n"+
			"新增语句类型必须进 stmtKindRegistry（并让 aggregateCarrierSlots 扫到它的聚合槽位）",
			len(missing), missing)
	}
	if len(stale) > 0 {
		t.Errorf("stmtKindRegistry 里有 %d 个已不存在的 Stmt 实现：%v", len(stale), stale)
	}
}

// stmtImplementationsInPackage 用 AST 找出本包内**同时**声明 `stmtNode() Node` 与
// `stmtID() string` 的类型名（= `Stmt` 接口的实现），返回 名字 → 文件名。
func stmtImplementationsInPackage() (map[string]string, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			return nil, err
		}
		methods := map[string]map[string]bool{}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			recv := receiverTypeName(fn.Recv.List[0].Type)
			if recv == "" {
				continue
			}
			if methods[recv] == nil {
				methods[recv] = map[string]bool{}
			}
			methods[recv][fn.Name.Name] = true
		}
		for recv, ms := range methods {
			if ms["stmtNode"] && ms["stmtID"] {
				out[recv] = name
			}
		}
	}
	return out, nil
}
