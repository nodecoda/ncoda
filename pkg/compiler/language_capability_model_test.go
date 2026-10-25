package compiler

// language_capability_model_test.go — 语言 lane 不得携带目标能力模型。
//
// neutralLanguageProfile 必须装空 capability manifest（NoCapabilityModel），
// 且 for / loop / attempt 在 `--target __none__` 下照常产出 IR。这两条一起
// 锁住“平台能力不泄进语言前端”。

import (
	"testing"
)

const languageCapabilityNeutralSrc = `@language nodecoda/1
@mode workflow

function main(string[] xs) -> string {
    for (x in xs) {
        let y = x;
    }
    attempt http("GET", "https://httpbin.org/status/503", {}) as response {
        success { return "ok"; }
        failure(error) { return "fail:" + error.error_type; }
    }
    return "done";
}
`

func TestLanguageLaneInstallsNoCapabilityModel(t *testing.T) {
	p := neutralLanguageProfile()
	if p.Capabilities == nil {
		t.Fatal("language-only profile has a nil capability manifest; want an explicit empty model")
	}
	if p.Capabilities.Len() != 0 {
		t.Fatalf("language-only profile declares %d capability shapes; want an empty model (no platform facts in the language lane)", p.Capabilities.Len())
	}

	res := Compile(languageCapabilityNeutralSrc, Options{
		Target:   TargetLanguageOnly,
		Filename: "language.ncoda",
		EmitIR:   true,
	})
	if !res.OK {
		t.Fatalf("language lane rejected for/attempt under the target-free profile: %+v", res.Diagnostics)
	}
	if res.IR == nil {
		t.Fatal("language lane produced no IR")
	}
}
