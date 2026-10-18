package parser_test

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/parser"
)

// G001: switch 语法回归测试（目标形态，先红——KW_SWITCH 未实现）。
// 与 zz_intent_parse_test.go 镜像，作为 intent → switch 迁移的验收基线。
// 实现（G002-G003）后这些测试必须全绿。

func TestParseSwitchStmtSmoke(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
function main(string input) {
    switch (input) -> r {
        "support": {
            let x = r.reason;
        }
        "billing": {
            let y = r.classificationId;
        }
        default: {
            let z = r.reason;
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

func TestParseSwitchStmtNoBindSmoke(t *testing.T) {
	src := `function main(string input) {
    switch (input) {
        "support": {
            let x = 1;
        }
        "billing": {
            let y = 2;
        }
        default: {
            let z = 3;
        }
    }
    return "ok";
}
`
	if _, errs := parser.ParseSource(src, "nobind.ncoda"); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
}

// 空 case 分支体 = 合法语法（理由见 zz_intent_parse_test.go 同名说明）。
func TestParseSwitchStmtEmptyBranchAccepted(t *testing.T) {
	src := `function main(string input) {
    switch (input) {
        "a": {
        }
    }
    return "ok";
}
`
	if _, errs := parser.ParseSource(src, "empty.ncoda"); len(errs) != 0 {
		t.Fatalf("空 case 分支体应被接受，got errors: %v", errs)
	}
}

func TestParseSwitchStmtDupLabelRejected(t *testing.T) {
	src := `function main(string input) {
    switch (input) {
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
	if _, errs := parser.ParseSource(src, "dup.ncoda"); len(errs) == 0 {
		t.Fatalf("expected duplicate-label rejection, got no errors")
	}
}

func TestParseSwitchStmtNoBranchRejected(t *testing.T) {
	src := `function main(string input) {
    switch (input) {
    }
    return "ok";
}
`
	if _, errs := parser.ParseSource(src, "nobranch.ncoda"); len(errs) == 0 {
		t.Fatalf("expected no-branch rejection, got no errors")
	}
}

// 空 default 分支体 = 合法语法（同上；`default: { }` 才是「端口有边、臂无
// 节点」的忠实读法，旧行为把它误判成悬挂）。
func TestParseSwitchStmtEmptyDefaultAccepted(t *testing.T) {
	src := `function main(string input) {
    switch (input) {
        "a": {
            let x = 1;
        }
        default: {
        }
    }
    return "ok";
}
`
	if _, errs := parser.ParseSource(src, "emptydefault.ncoda"); len(errs) != 0 {
		t.Fatalf("空 default 分支体应被接受，got errors: %v", errs)
	}
}
