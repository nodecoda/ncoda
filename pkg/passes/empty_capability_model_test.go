package passes

// empty_capability_model_test.go — 语言 lane 的「无目标能力模型」契约。
//
// 目标无关的语言前端不声明任何平台能力：profile 装一张**空** capability
// manifest 时，for / attempt / multi-branch parallel 这些语言合法构造不得因
// 「表里没写 ENABLED」而被判 TARGET_NOT_LOWERABLE。空表 = 没有目标模型，
// 不是「目标拒绝一切」。
//
// 负控：非空 manifest 里显式 REJECTED 的形状仍必须红 —— “无模型”不等于
// “忽略声明”。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/diag"
)

const emptyCapabilityLanguageSrc = `@language nodecoda/1
@mode workflow

function main(string[] xs) -> string {
    for (x in xs) {
        let y = x;
    }
    parallel {
        { let a = 1; }
        { let b = 2; }
    }
    attempt http("GET", "https://httpbin.org/status/503", {}) as response {
        success { return "ok"; }
        failure(error) { return "fail:" + error.error_type; }
    }
    return "done";
}
`

func languageOnlyProfile() *TargetProfile {
	p := DefaultTargetProfile()
	p.Name = "__none__"
	p.Capabilities = NewCapabilityManifest("language-only", nil)
	p.Carrier = LanguageCarrier()
	return p
}

func TestEmptyCapabilityManifestDoesNotRejectLanguageConstructs(t *testing.T) {
	res := DefaultPipeline().Run(emptyCapabilityLanguageSrc, "lang.ncoda", languageOnlyProfile())
	if res.Program == nil {
		t.Fatalf("language-only profile produced no program: %v", res.Diagnostics)
	}
	for _, d := range res.Diagnostics {
		if d.Code == diag.TARGET_NOT_LOWERABLE {
			t.Fatalf("empty capability manifest rejected a language construct: %v", d)
		}
	}
	if res.HasErrors() {
		t.Fatalf("language-only profile rejected a valid program: %v", res.Diagnostics)
	}
}

// 负控：显式声明 REJECTED 的形状仍然红。没有这条，上一条可以用“忽略所有
// capability 判定”蒙混过去。
func TestDeclaredCapabilityManifestStillRejects(t *testing.T) {
	p := DefaultTargetProfile()
	p.Capabilities = NewCapabilityManifestWithState(map[CapabilityShape]CapabilityState{
		CapabilityShapeForStatementPlain: CapabilityStateRejected,
	})
	res := DefaultPipeline().Run(emptyCapabilityLanguageSrc, "target.ncoda", p)
	found := false
	for _, d := range res.Errors() {
		if d.Code == diag.TARGET_NOT_LOWERABLE {
			found = true
		}
	}
	if !found {
		t.Fatalf("declared REJECTED shape did not produce TARGET_NOT_LOWERABLE: %v", res.Diagnostics)
	}
}
