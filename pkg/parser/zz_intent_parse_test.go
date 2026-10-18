package parser_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/parser"
)

func TestParseIntentStmtSmoke(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string input) {
    intent (input) -> it {
        "图片地址": {
            let x = it.reason;
        }
        "null": {
            let y = it.classificationId;
        }
        default: {
            let z = it.reason;
        }
    }
    return "ok";
}
`
	prog, errs := parser.ParseSource(src, "smoke.ncoda")
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if prog == nil || prog.Entry() == nil {
		t.Fatalf("no program/entry")
	}
	_ = prog
}

// 空分支体 = **合法语法**（08-27 用户拍板重申 + 2026-09-13 A 级裁定：
// 「空分支」的精确含义 = 分支**无后续连线（端口悬空）**，有明确连线的分支是
// 正常语义；两臂都空也「语义上无错误，语法上无错误」——开发期临时框架是合法
// 写法）。空体臂的端口由 lowering 的义务模型闭到收敛点；**真悬挂**（声明了
// 分支却没有端口边）由 wire 层 `FAILED(SWITCH-DANGLING)` 承担——parser 看不到
// 端口连线，文本层的代理检查既拦不住真悬挂、又误杀合法空臂（K/CV/R_ask
// 实证：反编译产物里的合法空臂曾被自家 parser 拒收）。
func TestParseIntentStmtEmptyBranchAccepted(t *testing.T) {
	src := `function main(string input) {
    intent (input) {
        "a": {
        }
    }
    return "ok";
}
`
	if _, errs := parser.ParseSource(src, "empty.ncoda"); len(errs) != 0 {
		t.Fatalf("空分支体应被接受（端口义务由 lowering 闭到收敛点），got errors: %v", errs)
	}
}

func TestParseIntentStmtDupLabelRejected(t *testing.T) {
	src := `function main(string input) {
    intent (input) {
        "a": {
            let x = 1;
        }
        "a": {
            let y = 2;
        }
    }
    return "ok";
}
`
	_, errs := parser.ParseSource(src, "dup.ncoda")
	if len(errs) == 0 {
		t.Fatalf("expected duplicate-label rejection, got no errors")
	}
}

func TestParseIntentStmtNoBranchRejected(t *testing.T) {
	src := `function main(string input) {
    intent (input) {
    }
    return "ok";
}
`
	_, errs := parser.ParseSource(src, "nobranch.ncoda")
	if len(errs) == 0 {
		t.Fatalf("expected no-branch rejection, got no errors")
	}
}
