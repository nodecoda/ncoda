//@feature parallel-fence — `parallel { A } { B }` 的语义 = 顺序组合 + 并发交错

package passes

// 语言判定（A 级，2026-09-19 用户拍板）：
//
//	**`parallel { A } { B }` 与 `A; B` 的差异只有「并发交错」** —— 那是 runtime 关心
//	的事，不改变程序语义。由此三条推论，且**只有**这三条：
//
//	① 分支内的写是**普通写**：写「体外声明的绑定」与写「分支内声明的绑定」都合法，
//	   栅栏之后可读（分支内声明的名字经透明屏障逃逸到外层，下游按名字直接访问）。
//	② 唯一的语言级错误 = **同一绑定被两个分支触碰**（写/写、读/写）——那是交错引入
//	   的不确定性，顺序执行下同样的两句是确定的（`x = 1; let y = x;`）。
//	③ 「兄弟分支互不可见」由**名字解析**保证（分支 2 引用不了分支 1 声明的名字
//	   ⇒ UNDEFINED_VAR），不靠额外判据。
//
// 反例（本节记录的改判）：曾把 ① 判成错（「分支写体外绑定 = E1036」）——**实测被推翻**：
// coze-biz 语料的规范形恰恰是「体外声明 + 零值载体 + 分支内写 + 栅栏后读」，
// 672 份源里 **92 份**含带赋值的 parallel 块（例：7531650036067188778_X152_Pcut_video_img_1
// `T v1; v1 = <零值>; … parallel { { v1 = …; } { v2 = …; } { v3 = …; } } return { "data":
// v3.data, … }`）；该判据会把它们整体打红（一手实测：收窄前后 A/B 为
// SUCCEEDED → 2×E1036）。

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

// TestParallelBranchWritesAreOrdinaryWrites 钉住 ①：分支内写体外绑定 / 分支内局部
// 都合法（含用户给的最小例与语料规范形）。
func TestParallelBranchWritesAreOrdinaryWrites(t *testing.T) {
	cases := []struct{ name, src string }{
		{"outer_binding_bare_decl", `
function main(string u) -> string {
    string a;
    a = "";
    string b;
    b = "";
    parallel {
        { a = "A"; }
        { b = "B"; }
    }
    return a + b;
}`},
		{"outer_binding_decl_with_init", `
function main(string u) -> string {
    var a = "";
    var b = "";
    parallel {
        { a = "A"; }
        { b = "B"; }
    }
    return a + b;
}`},
		{"branch_local_write", `
function main() -> int {
    parallel {
        { var y = 2; y = 3; }
        { var z = 4; }
    }
    return 0;
}`},
		{"user_minimal_example", `
function main() -> int {
    parallel {
        { var a; a = 10; }
        { var b; b = 11; }
    }
    let c = a + b;
    return c;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runParallelPipeline(t, tc.src)
			if codes := errCodes(result); len(codes) != 0 {
				t.Fatalf("branch writes are ordinary writes (only interleaving is new): %v\n%v",
					codes, result.Diagnostics)
			}
		})
	}
}

// TestParallelCrossBranchConflictIsAConflict 钉住 ②：交错带来的不确定性仍是错误
// （写/写 与 读/写 两种）；文案必须是并行 hazard 文本（不是 loop-state 文本）。
func TestParallelCrossBranchConflictIsAConflict(t *testing.T) {
	cases := []struct{ name, src string }{
		{"write_write", `
function main() -> int {
    var x = 0;
    parallel {
        { x = 1; }
        { x = 2; }
    }
    return x;
}`},
		{"write_read", `
function main() -> int {
    var x = 0;
    parallel {
        { x = 1; }
        { let y = x; }
    }
    return x;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runParallelPipeline(t, tc.src)
			got := errCodes(result)
			if !reflect.DeepEqual(got, []diag.Code{diag.PARALLEL_ACCESS_HAZARD}) {
				t.Fatalf("error codes = %v, want [PARALLEL_ACCESS_HAZARD]", got)
			}
			msg := result.Errors()[0].Message
			if strings.Contains(msg, "owning while loop") || strings.Contains(msg, "workflow state") {
				t.Fatalf("a cross-branch hazard must not borrow the loop-state text: %q", msg)
			}
			if !strings.Contains(msg, "hazard") {
				t.Fatalf("message = %q, want the cross-branch hazard text", msg)
			}
		})
	}
}

// TestParallelSiblingDeclarationsAreInvisible 钉住 ③：交错安全的名字解析面
// ——分支 2 引用不了分支 1 声明的名字（否则会读到「另一个分支是否已跑完」的未定义值）。
func TestParallelSiblingDeclarationsAreInvisible(t *testing.T) {
	src := `
function main() -> int {
    parallel {
        { var a = 1; }
        { let b = a; }
    }
    return 0;
}`
	result := runParallelPipeline(t, src)
	got := errCodes(result)
	if !reflect.DeepEqual(got, []diag.Code{diag.UNDEFINED_VAR}) {
		t.Fatalf("error codes = %v, want [UNDEFINED_VAR] (siblings are not visible to each other)", got)
	}
}

// TestParallelArmScopeIsNotClosed 钉住「**臂块透明 ≠ 臂内任意深度透明**」：
//
// 逃逸判据 = **definite assignment** —— 一个声明能否逃出栅栏（join），取决于到达
// join 的**每一条**路径上该声明是否都执行过。既不是「容器种类」白名单
// （`while`/`attempt` 之类的枚举必然漏），也不是「有没有顺序」（臂内 `if` 明明
// 有顺序，它的声明却仍不逃逸）。
//
//   - 臂块本身：所有臂都执行（AND 汇合）⇒ 臂块内**直接**写的声明 definite ⇒ 逃逸；
//   - 臂内**嵌套块**（if / while）：存在「不执行」的路径 ⇒ 其中的声明不 definite
//     ⇒ 不逃逸（栅栏之后引用 = UNDEFINED_VAR）；
//   - 对照：`if` 的臂互斥 ⇒ 臂内声明同样不逃逸 —— 跨臂传值只能由作者写
//     「外层声明 + 臂内赋值」（= 物化一次 Φ），不是让编译器猜。
//
// 权威：P5 parallel 语义契约（`docs/BACKEND-ARCHITECTURE.md`，2026-09-20 收口
// 「不封闭的作用域 / 逃逸判据 = definite assignment」）。本条判据**不选边**：
// 它锁的就是这个边界，若哪天要改成「臂内任意深度透明」，那是语言语义变更
// （须用户审核），本测试会先红。
func TestParallelArmScopeIsNotClosed(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []diag.Code // 空切片 = 期望零错误
	}{
		{"arm_block_decl_escapes", `
function main() -> string {
    parallel {
        { var a = "A"; }
        { var b = "B"; }
    }
    return a + b;
}`, []diag.Code{}},
		{"arm_nested_if_decl_does_not_escape", `
function main(string u) -> string {
    parallel {
        { if (u != "") { var e = "E"; } }
        { var z = "Z"; }
    }
    return e;
}`, []diag.Code{diag.UNDEFINED_VAR}},
		{"arm_nested_while_decl_does_not_escape", `
function main(string u) -> string {
    parallel {
        { while (u != "") limit 5 { var c = "C"; } }
        { var z = "Z"; }
    }
    return c;
}`, []diag.Code{diag.UNDEFINED_VAR}},
		{"outer_decl_assign_in_arm_nested_block_is_readable", `
function main(string u) -> string {
    var e = "";
    parallel {
        { if (u != "") { e = "E"; } }
        { var z = "Z"; }
    }
    return e;
}`, []diag.Code{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := errCodes(runParallelPipeline(t, tc.src))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("error codes = %v, want %v", got, tc.want)
			}
		})
	}
}
