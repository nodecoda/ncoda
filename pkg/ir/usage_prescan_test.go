package ir_test

// 使用集预扫（`L` 的 IR 面落点）—— SHIM-ESCAPE-CONTRACT §0.1 的 `L` 只有产物面判不了
// （coze-biz 把记录物化成 N 个口 ⇒ 「读成员」与「读整口」在 wire 上同形，2026-09-24
// 一手实测：member_only 恒 0），只能在**物化之前**由使用面判定。
//
// 本测试锁四件事（撤掉 usage_prescan.go 的任一分支即红）：
//   ① 整值读与成员读分开记（`return bag;` vs `return bag.tag;`）
//   ② 写点 / 分支内写点（φ 的信号）记账
//   ③ callee 不污染账单：用户函数名、std/json 命名空间基都不进账
//   ④ 类型引用不是读（是 TypeRef，天然不进账单）

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// prescanUsages 把源码过完整前端，返回使用面账单。
func prescanUsages(t *testing.T, src string) map[string]*ir.BindingUsage {
	t.Helper()
	res := passes.DefaultPipeline().Run(src, "usage-prescan.dc", nil)
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			t.Logf("pipeline %d %s: %s", d.Code, d.Severity, d.Message)
		}
		t.Fatalf("frontend pipeline reported errors for source:\n%s", src)
	}
	if res.Program == nil {
		t.Fatal("frontend pipeline produced no TypedBoundProgram")
	}
	return ir.PrescanBindingUsages(res.Program)
}

func usageOf(t *testing.T, usages map[string]*ir.BindingUsage, name string) *ir.BindingUsage {
	t.Helper()
	u := usages[name]
	if u == nil {
		t.Fatalf("binding %q missing from usage bill (bill = %v)", name, usageNames(usages))
	}
	return u
}

func usageNames(usages map[string]*ir.BindingUsage) []string {
	out := make([]string, 0, len(usages))
	for k := range usages {
		out = append(out, k)
	}
	return out
}

const recordHeader = `@language nodecoda/1
@mode workflow
type Bag = {
  int slot;
  string tag;
};
`

// ① 同一个记录绑定：整值读 vs 只读成员。
func TestPrescanMemberOnlyReadIsNotAWholeRead(t *testing.T) {
	memberOnly := recordHeader + `
function main(string prompt) -> string {
    Bag bag = { "slot": 1, "tag": "x" };
    return bag.tag;
}
`
	u := usageOf(t, prescanUsages(t, memberOnly), "bag")
	if u.WholeReads != 0 {
		t.Fatalf("member-only read must not count as a whole read: %+v", u)
	}
	if !u.MemberOnlyReads() {
		t.Fatalf("MemberOnlyReads() = false, want true: %+v", u)
	}
	if u.Writes != 1 || u.Declared != true {
		t.Fatalf("declaration with init must be one write point: %+v", u)
	}

	whole := recordHeader + `
function consume(Bag b) -> string {
    return b.tag;
}

function main(string prompt) -> string {
    Bag bag = { "slot": 1, "tag": "x" };
    return consume(bag);
}
`
	u2 := usageOf(t, prescanUsages(t, whole), "bag")
	if u2.WholeReads != 1 {
		t.Fatalf("passing the record as an argument is a whole read: %+v", u2)
	}
	if u2.MemberOnlyReads() {
		t.Fatalf("MemberOnlyReads() must be false when the whole value is read: %+v", u2)
	}
}

// ② 写点：声明带初始化 + 分支内赋值。
func TestPrescanCountsBranchWrites(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(int n) -> int {
    int v = 0;
    if (n == 1) {
        v = 1;
    }
    return v;
}
`
	u := usageOf(t, prescanUsages(t, src), "v")
	if u.Writes != 2 {
		t.Fatalf("want 2 write points (init decl + branch assignment), got %+v", u)
	}
	if u.BranchWrites != 1 {
		t.Fatalf("want exactly 1 branch write (a phi signal), got %+v", u)
	}
	if u.WholeReads != 1 {
		t.Fatalf("`return v;` is one whole read, got %+v", u)
	}
	if u.MemberOnlyReads() {
		t.Fatalf("a scalar whole-read binding is not member-only: %+v", u)
	}
}

// ②b 成员写：`v.f = e` 记成成员写、且目标不算读。
func TestPrescanMemberWriteTargetIsNotARead(t *testing.T) {
	src := recordHeader + `
function main(string prompt) -> string {
    Bag bag = { "slot": 1, "tag": "x" };
    bag.tag = prompt;
    return bag.tag;
}
`
	u := usageOf(t, prescanUsages(t, src), "bag")
	if u.MemberWrites != 1 {
		t.Fatalf("want 1 member write, got %+v", u)
	}
	if u.Writes != 2 {
		t.Fatalf("want 2 write points (init decl + member assignment), got %+v", u)
	}
	if u.WholeReads != 0 || u.MemberReads != 1 {
		t.Fatalf("assign target must not be a read; only `bag.tag` on return is: %+v", u)
	}
}

// ③ callee / 命名空间不进账单：用户函数名、std/json 命名空间基。
func TestPrescanExcludesCalleesAndNamespaces(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function helper(string s) -> string {
    return s;
}

function main(string prompt) -> string {
    let y = helper(prompt);
    let z = json.stringify(y);
    return z;
}
`
	usages := prescanUsages(t, src)
	for _, name := range []string{"helper", "json"} {
		if u, ok := usages[name]; ok {
			t.Fatalf("callee base %q must not enter the usage bill: %+v (bill = %v)", name, u, usageNames(usages))
		}
	}
	if u := usageOf(t, usages, "prompt"); u.WholeReads != 1 {
		t.Fatalf("an argument is a whole read of the argument name: %+v", u)
	}
	if u := usageOf(t, usages, "y"); u.WholeReads != 1 {
		t.Fatalf("`json.stringify(y)` reads y as an argument: %+v", u)
	}
}

// ④ 类型引用不是读：`Bag bag = …` 里的 `Bag` 不该出现在账单里。
func TestPrescanIgnoresTypeReferences(t *testing.T) {
	src := recordHeader + `
function main(string prompt) -> string {
    Bag bag = { "slot": 1, "tag": "x" };
    return bag.tag;
}
`
	usages := prescanUsages(t, src)
	if u, ok := usages["Bag"]; ok {
		t.Fatalf("a TypeRef is not a value read: %+v", u)
	}
}

// ⑥ 写点按值形态三分类 + §42.3 A 轴的可折判据（P1 ∧ P2）。
//
// 判据三格：① 写点分类是划分（`Writes == literal + member + other`）；
// ② 记录字面量写 + 只读成员 ⇒ 可折；③ 任一写点不是字面量/成员写 ⇒ 不可折（fail-safe）。
func TestPrescanWriteKindsAndFoldability(t *testing.T) {
	literalOnly := recordHeader + `
function main(string prompt) -> string {
    Bag bag = { "slot": 1, "tag": "x" };
    return bag.tag;
}
`
	u := usageOf(t, prescanUsages(t, literalOnly), "bag")
	assertWritePartition(t, u)
	if u.LiteralWrites != 1 || u.MemberWrites != 0 || u.OtherWrites != 0 {
		t.Fatalf("record literal write must be classified as literal: %+v", u)
	}
	if !u.UsageAllowsWholeValueFold() {
		t.Fatalf("literal-only writes + member-only reads must be foldable: %+v", u)
	}

	// 成员写仍是 P2 允许的形态（§42.3 A 轴 P2 原文：记录字面量**或成员写**）。
	memberWrite := recordHeader + `
function main(string prompt) -> string {
    Bag bag = { "slot": 1, "tag": "x" };
    bag.tag = prompt;
    return bag.tag;
}
`
	u2 := usageOf(t, prescanUsages(t, memberWrite), "bag")
	assertWritePartition(t, u2)
	if u2.LiteralWrites != 1 || u2.MemberWrites != 1 {
		t.Fatalf("want one literal write + one member write: %+v", u2)
	}
	if !u2.UsageAllowsWholeValueFold() {
		t.Fatalf("member writes are an allowed P2 form: %+v", u2)
	}

	// 任意表达式写 ⇒ P2 不成立 ⇒ 保持整值记录（fail-safe）。
	otherWrite := recordHeader + `
function main(string prompt) -> string {
    Bag bag = { "slot": 1, "tag": "x" };
    Bag other = { "slot": 2, "tag": "y" };
    bag = other;
    return bag.tag;
}
`
	u3 := usageOf(t, prescanUsages(t, otherWrite), "bag")
	assertWritePartition(t, u3)
	if u3.OtherWrites != 1 {
		t.Fatalf("assigning a binding must be classified as other: %+v", u3)
	}
	if u3.UsageAllowsWholeValueFold() {
		t.Fatalf("a non-literal write must keep the whole-value record: %+v", u3)
	}
	if u3.WholeValueObservable() {
		t.Fatalf("member-only reads must not report the whole value as observable: %+v", u3)
	}
}

// ⑥b 整值可观察的两种形态：整值读（有消费者）与一次都没读（死载体，属 ¬L 分支）。
func TestPrescanWholeValueObservable(t *testing.T) {
	whole := recordHeader + `
function consume(Bag b) -> string {
    return b.tag;
}

function main(string prompt) -> string {
    Bag bag = { "slot": 1, "tag": "x" };
    return consume(bag);
}
`
	if u := usageOf(t, prescanUsages(t, whole), "bag"); !u.WholeValueObservable() {
		t.Fatalf("a whole-value read makes the whole value observable: %+v", u)
	}

	unread := recordHeader + `
function main(string prompt) -> string {
    Bag bag = { "slot": 1, "tag": "x" };
    return prompt;
}
`
	u := usageOf(t, prescanUsages(t, unread), "bag")
	if !u.WholeValueObservable() {
		t.Fatalf("a never-read binding is the dead-carrier branch, not foldable: %+v", u)
	}
	if u.UsageAllowsWholeValueFold() {
		t.Fatalf("a never-read binding has no read point to fold: %+v", u)
	}
}

// assertWritePartition 锁账单的内部一致性：写点分类必须是一个划分。
func assertWritePartition(t *testing.T, u *ir.BindingUsage) {
	t.Helper()
	if got := u.LiteralWrites + u.MemberWrites + u.OtherWrites; got != u.Writes {
		t.Fatalf("write kinds must partition Writes: %d + %d + %d != %d (%+v)",
			u.LiteralWrites, u.MemberWrites, u.OtherWrites, u.Writes, u)
	}
}

// ⑤ 循环变量只记 Declared（口径边界，写在文件头注里）。
func TestPrescanMarksLoopVarDeclared(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(array<string> xs) -> int {
    int n = 0;
    for (item in xs) {
        n = n + 1;
    }
    return n;
}
`
	usages := prescanUsages(t, src)
	u := usageOf(t, usages, "item")
	if !u.Declared {
		t.Fatalf("loop variable must be recorded as declared: %+v", u)
	}
	if u.Writes != 0 {
		t.Fatalf("a loop variable has no statement write point: %+v", u)
	}
}
