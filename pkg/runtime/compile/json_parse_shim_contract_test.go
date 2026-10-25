package compile

// json.parse<object> 的 shim 契约。
//
// 语料里 SW_* 一族的工作流都是这个形状：
//   llm<T>  →  text.<field>（模型给的 JSON 字符串）  →  json.parse<object>(…)
// 离线 echo 对 string 字段发的是 "x"，于是 json.loads("x") 失败、被记成
// REXPRESSION_TYPE。要证明那 7 条是**输入值形状**问题而不是语义 bug，就得把
// shim 自己的行为钉住：合法 JSON 串必须解析成对象，非法串必须报错，且诊断码
// 不得漂移。

import (
	"context"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// 支持形态：绑定到 let（语料里的实际形态）。
const jsonParseShimBoundSrc = `@language nodecoda/1
@mode workflow

function main(string payload) -> object {
    let v = json.parse<object>(payload);
    return { "jd_profile": v };
}
`

// 直接写在 return 里：R-216 收口后，return 位置与 let 绑定必须同权。
const jsonParseShimReturnSrc = `@language nodecoda/1
@mode workflow

function main(string payload) -> object {
    return json.parse<object>(payload);
}
`

func TestJsonParseShimContract(t *testing.T) {
	r := mustCompile(t, New(testRegistry()), qaLoopFromSource(t, jsonParseShimBoundSrc))

	// 正侧：模型真会给的形状（一个 JSON 文档字符串）必须解析成对象。
	out, err := r.Invoke(context.Background(), map[string]any{"payload": `{"jd_profile":{"role_title":"x"}}`})
	if err != nil {
		t.Fatalf("valid JSON rejected: %v", err)
	}
	// end 节点把返回值挂在以节点 id 为键的嵌套 map 里；不写死 id。
	outer := findMapWithKey(out, "jd_profile")
	if outer == nil {
		t.Fatalf("out = %#v, want parsed object under jd_profile", out)
	}
	obj, ok := outer["jd_profile"].(map[string]any)
	if !ok {
		t.Fatalf("jd_profile = %#v, want parsed object", outer["jd_profile"])
	}
	if _, ok := obj["jd_profile"]; !ok {
		t.Fatalf("parsed object lost the payload: %#v", obj)
	}

	// 负侧：echo 的默认 string 标量（"x"）不是 JSON —— 必须报错，且是
	// REXPRESSION_TYPE。这条同时是分诊用的判别位：语料里那 7 条命中的就是它。
	if _, err := r.Invoke(context.Background(), map[string]any{"payload": "x"}); err == nil {
		t.Fatal("invalid JSON accepted")
	} else if !strings.Contains(err.Error(), "REXPRESSION_TYPE") {
		t.Fatalf("diagnostic code drifted: %v", err)
	}
}

// TestJsonParseInReturnPositionIsSupported 锁定 R-216 的收口：
// 同一个原语，绑到 let、写在 return、作为实参，三种位置必须同权。
// findMapWithKey 在输出 map（可能被 end 节点 id 包一层）里找第一个含 key 的 map。
func findMapWithKey(m map[string]any, key string) map[string]any {
	if _, ok := m[key]; ok {
		return m
	}
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			if hit := findMapWithKey(sub, key); hit != nil {
				return hit
			}
		}
	}
	return nil
}

func TestJsonParseInReturnPositionIsSupported(t *testing.T) {
	res := passes.DefaultPipeline().Run(jsonParseShimReturnSrc, "repro.ncoda", nil)
	if res.HasErrors() {
		for _, d := range res.Errors() {
			t.Logf("frontend: %s", d.Message)
		}
		t.Fatalf("frontend rejected the source")
	}
	if _, err := ir.Build(res.Program); err != nil {
		t.Fatalf("return-position json.parse did not lower: %v", err)
	}

	r := mustCompile(t, New(testRegistry()), qaLoopFromSource(t, jsonParseShimReturnSrc))
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"payload": `{"jd_profile":{"role_title":"x"}}`}))
	outer := findMapWithKey(out, "jd_profile")
	if outer == nil {
		t.Fatalf("out = %#v, want the parsed object under jd_profile", out)
	}
	if _, ok := outer["jd_profile"].(map[string]any); !ok {
		t.Fatalf("jd_profile = %#v, want a parsed object", outer["jd_profile"])
	}
}

// 实参位置：json.parse<T> 直接作为用户函数调用的参数，必须与 let / return
// 同权（R-216 的第二种位置）。
const jsonParseShimArgSrc = `@language nodecoda/1
@mode workflow

type Resp = { string name; };

function pick(Resp r) -> string {
    return r.name;
}

function main(string payload) -> string {
    return pick(json.parse<Resp>(payload));
}
`

func TestJsonParseInArgumentPositionIsSupported(t *testing.T) {
	res := passes.DefaultPipeline().Run(jsonParseShimArgSrc, "repro.ncoda", nil)
	if res.HasErrors() {
		for _, d := range res.Errors() {
			t.Logf("frontend: %s", d.Message)
		}
		t.Fatalf("frontend rejected the source")
	}
	if _, err := ir.Build(res.Program); err != nil {
		t.Fatalf("argument-position json.parse did not lower: %v", err)
	}

	r := mustCompile(t, New(testRegistry()), qaLoopFromSource(t, jsonParseShimArgSrc))
	out := UnwrapOutput(mustInvoke(t, r, map[string]any{"payload": `{"name":"A"}`}))
	if got, _ := out["output"].(string); got != "A" {
		t.Fatalf("pick(json.parse<Resp>(...)) = %#v, want \"A\"", out["output"])
	}
}
