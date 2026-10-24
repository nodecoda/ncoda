package compile

// 零迭代 loop 的**声明输出**必须存在（R-142）。
//
// 平台语义正本 = coze-studio `backend/domain/workflow/internal/nodes/loop/loop.go`：
//
//	output = make(map[string]any, len(l.outputs))
//	for k := range l.outputs { output[k] = make([]any, 0) }   // 迭代前先占位
//	...每迭代 setIthOutput 里 append...
//	return output, nil                                        // 零迭代也返回它
//
// ⇒ `by_array` 喂空数组时平台返回 `{audio_list: [], ...}`，**不是「没有这个键」**。
// 我们此前只从 `iters` 组装 ⇒ `map[]` ⇒ 下游 `resolveRef` 取不到 ⇒ 声明了的输出
// **静默消失**（撞「失配 ≠ 静默丢弃」）；它在 code 消费者面前被 `normalizeInputs`
// 的零值吸收掩盖，只在非 code 消费者（prompt / literal Text）暴露。

import (
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/spec"
)

func zeroIterCfg() *spec.LoopConfig {
	return &spec.LoopConfig{
		LoopType: "by_array",
		OutputSources: []spec.Source{
			{Path: []string{"audio_list"}, BlockID: "100027", Name: wGroup1},
			{Path: []string{"duration_list"}, BlockID: "100028", Name: "duration"},
			// loop **自身**的状态端口：Vars 语义，由 finalState 承载，不得被占位成空列表。
			{Path: []string{"returned"}, BlockID: "100022", Name: "returned"},
		},
	}
}

func TestLoopZeroIterRegistersDeclaredOutputsAsEmptyLists(t *testing.T) {
	cfg := zeroIterCfg()
	out := assembleLoopOutputs(map[string][]any{}, map[string]any{}, cfg, "100022")
	for _, k := range []string{"audio_list", "duration_list"} {
		v, ok := out[k]
		if !ok {
			t.Fatalf("零迭代后 `%s` **没有键** —— 声明了的输出静默消失（平台语义 = 空列表）", k)
		}
		l, isList := v.([]any)
		if !isList || len(l) != 0 {
			t.Fatalf("零迭代后 `%s` = %#v，期望空列表", k, v)
		}
	}
	if _, ok := out["returned"]; ok {
		t.Fatalf("loop 自身状态端口不得被占位成空列表（`returned` 是 bool）：%#v", out)
	}

	// 有迭代时：收集值胜出（占位不得覆盖真值），未收集到的口仍是空列表占位。
	out = assembleLoopOutputs(map[string][]any{"audio_list": {"a"}},
		map[string]any{"returned": true}, cfg, "100022")
	if l, ok := out["audio_list"].([]any); !ok || len(l) != 1 || l[0] != "a" {
		t.Fatalf("有迭代时 `audio_list` = %#v，期望收集值 [a]", out["audio_list"])
	}
	if out["returned"] != true {
		t.Fatalf("`returned` = %#v，期望 finalState 的 true", out["returned"])
	}
	if l, ok := out["duration_list"].([]any); !ok || len(l) != 0 {
		t.Fatalf("本轮只收集到 audio_list 时 `duration_list` 仍须占位为空列表：%#v", out["duration_list"])
	}
}

// FlattenOutput 只作用于名为 "output" 的端口（参考目标 契约）：零迭代的占位也必须是展平后的空列表。
func TestLoopZeroIterFlattenOutputPlaceholder(t *testing.T) {
	cfg := &spec.LoopConfig{
		LoopType:      "by_array",
		FlattenOutput: true,
		OutputSources: []spec.Source{{Path: []string{"output"}, BlockID: "200002", Name: "output"}},
	}
	v, ok := assembleLoopOutputs(map[string][]any{}, map[string]any{}, cfg, "200001")["output"]
	if !ok {
		t.Fatal("零迭代时 `output` 没有键")
	}
	if l, isList := v.([]any); !isList || len(l) != 0 {
		t.Fatalf("`output` = %#v，期望展平后的空列表", v)
	}
}
