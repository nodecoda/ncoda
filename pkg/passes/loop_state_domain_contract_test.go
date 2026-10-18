//@feature var-let — 可变态量声明的两种拼写等价（`T v = init;` ≡ `T v; v = init;`）与 E1058 的域

package passes

// 契约（A 级，2026-09-19 用户拍板）：
//
//  ① **语言判定：`T v = <init>;` 与 `T v; v = <init>;` 等价**——本语言不区分构造与
//     赋值（不像 C++）。带初值的可变声明因此也是**提升声明**：其后的写走同一条
//     可寻址物化路径，并同样成为交汇点的 phi 成员（junction phi 的成员门含
//     `hoistedVars` 一关，登记缺失 ⇒ 臂内写被静默丢弃——见 IR 侧契约测试）。
//  ② **E1058 的域 = 「被某个 while 拥有的可变绑定」**。判据原先把选择谓词写成
//     「可变 ∧ 声明处有字面量初值」，比规则域宽：没有 while 的程序也开火，文案却
//     断言 "before its owning while loop" —— 指向一个**不存在的** while（把必要条件
//     写成了存在性断言）。实测：`string v = ""; v = "A"; return v;`（无 if 无 while）
//     也报 E1058。收窄后：无 owning while ⇒ 本规则不表态（普通可变本地由 IR 的通用
//     规则处理）。
//
// parallel 的语义（不在本文件）见 parallel_scope_contract_test.go。

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

// TestLoopStateContractDoesNotFireOutsideItsDomain 钉住判据域的外侧：没有任何
// owning while 的可变本地写，一条都不许报 E1058。
func TestLoopStateContractDoesNotFireOutsideItsDomain(t *testing.T) {
	cases := []struct{ name, src string }{
		{"decl_init_plain_write", `
function main(string u) -> string {
    string v = "";
    v = "A";
    return v;
}`},
		{"decl_init_if_write", `
function main(string u) -> string {
    string v = "";
    if (u == "a") { v = "A"; }
    return v;
}`},
		{"decl_init_if_else_write", `
function main(string u) -> string {
    string v = "";
    if (u == "a") { v = "A"; } else { v = "B"; }
    return v;
}`},
		{"int_decl_init_compound_write", `
function main() -> int {
    var value = 0;
    value += 1;
    return value;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runParallelPipeline(t, tc.src)
			if codes := errCodes(result); len(codes) != 0 {
				t.Fatalf("E1058 fired with no owning while (outside its domain): %v\n%v",
					codes, result.Diagnostics)
			}
		})
	}
}

// TestLoopStateContractStillFiresWithinItsDomain 钉住判据域的内侧（判据本体一字未改）：
// 确有 owning while 且体外还有写 ⇒ 仍 E1058，且文案里的 while 这次**真的存在**。
func TestLoopStateContractStillFiresWithinItsDomain(t *testing.T) {
	src := `
function main(string u) -> int {
    var i = 0;
    if (u == "a") { i = 1; }
    while (i < 3) limit 3 {
        i += 1;
    }
    return i;
}`
	result := runParallelPipeline(t, src)
	got := errCodes(result)
	if !reflect.DeepEqual(got, []diag.Code{diag.INVALID_STATE}) {
		t.Fatalf("error codes = %v, want [INVALID_STATE]", got)
	}
	if msg := result.Errors()[0].Message; !strings.Contains(msg, "owning while loop") {
		t.Fatalf("message = %q, want the loop-state contract text (an owning while does exist here)", msg)
	}
}
