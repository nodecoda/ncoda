//@feature aggregate-projection — hoisted 聚合的端口身份与**绑定形态**无关

package passes

import "testing"

// 一手缺陷（2026-09-22，语料源 7535404609906491419_X38_V19_shudan_video）：
// raising 的变量提升把 `let agg = for(…) { yield … }` 改写成
// `var agg; … agg = for(…) { yield … };` —— **同一语义、两种绑定形态**。
// 而 `TypePass` 的循环端口表只在**声明位**登记（`visitVarDeclStmt`）⇒ 赋值位不登记
// ⇒ `agg.audio` 这类端口引用静默退化成 `any` ⇒ `ANY_NOT_NARROWED`（消费方若接受 any
// 则侥幸通过：同批语料 13 源里 6 源如此）。
//
// 规则（② 规范形，C §6.1 B10）：**端口的身份由表达式决定，与绑定形态（声明位 /
// 赋值位）无关** —— 两处必须同解（`bindForPorts`，`type_pass.go`）。
//
// 判据（两侧锚点都必须有，否则「一律 any」也能让正向变绿）：
//
//	① 赋值位形态（提升产物）⇒ 干净编译；
//	② 声明位形态（作者直写）⇒ 干净编译，且与 ① **同解**（诊断集合相等）；
//	③ 负向：同一形态引用**不存在的端口** ⇒ 必须报错（端口身份不是「什么都收」）。
func TestHoistedAggregatePortIdentityIndependentOfBindingForm(t *testing.T) {
	const head = `@mode workflow

code f(array<int> xs) -> int {
    return 1;
}

`
	const aggExpr = `for (x in enumerate(v2)) {
        yield img = x.value;
        yield audio = x.index;
    }`
	// ① 赋值位（raising 提升后的形态）
	assignForm := head + `function main(int n = 3) {
    var agg_1;
    array<int> v2 = [1, 2, 3];
    agg_1 = ` + aggExpr + `;
    let v10 = f(agg_1.audio);
    return v10;
}
`
	// ② 声明位（作者直写；与 ① 同一语义的另一种拼写）
	declForm := head + `function main(int n = 3) {
    array<int> v2 = [1, 2, 3];
    let agg_2 = ` + aggExpr + `;
    let v10 = f(agg_2.audio);
    return v10;
}
`
	// ③ 负向：端口名不在 yield 集合里
	badPort := head + `function main(int n = 3) {
    var agg_1;
    array<int> v2 = [1, 2, 3];
    agg_1 = ` + aggExpr + `;
    let v10 = f(agg_1.bogus);
    return v10;
}
`
	resAssign := runTypePipeline(t, assignForm)
	resDecl := runTypePipeline(t, declForm)
	if resAssign.HasErrors() {
		t.Fatalf("赋值位形态（提升产物）必须干净编译，实测 %v", resAssign.Errors())
	}
	if resDecl.HasErrors() {
		t.Fatalf("声明位形态必须干净编译，实测 %v", resDecl.Errors())
	}
	// 同解：两种拼写的诊断集合必须相同（形态不得影响语义）。
	if got, want := codesOf(resAssign.Diagnostics), codesOf(resDecl.Diagnostics); !codesEqual(got, want...) {
		t.Fatalf("两形态诊断集合不同：赋值位 %v vs 声明位 %v", got, want)
	}
	// ③ 端口身份不是「什么都收」：不存在的端口必须报错。
	if resBad := runTypePipeline(t, badPort); !resBad.HasErrors() {
		t.Fatal("引用不存在的端口必须编译报错（端口身份必须是表达式决定的闭集）")
	}
}
