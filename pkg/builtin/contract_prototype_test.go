// contract_prototype_test.go —— 内建契约表的**语言半边**门禁。
//
// # 它回答什么问题
//
// 契约表（examples/builtins/*.yaml → contracts_dify_gen.go）声明的是
// **平台物理口 ↔ 语言字段名**的对应；语言字段的**类型**权威在语言结果原型
// （pkg/binding/symbol.go）。两个权威必须描述同一个语言类型，否则
// 反编译会发出「语言上不存在的字段」或「与原型不符的类型」——
// 前者再编译 FIELD_NOT_FOUND、后者 TYPE_MISMATCH（A12-H13 一手实测）。
//
// 判据（与 langtype.go 的 ResultRecordAgrees / LangTypeOf 同一谓词）：
//
//	对每个 (target, nodeType)：声明了合同的键**必须**能在语言原型里找到同名
//	内建函数；其结果若为 record，字段集必须与该合同声明的语言字段集**相等**；
//	结果若不是 record（当前 = knowledge 的数组结果），必须在
//	nonRecordAcknowledged 里**显式承认**（默认拒绝，且承认表过期同样报红）。
//
// 负向自检见 TestContractPrototypeGateSelfTest：注入一处不一致必须报红
// （撤判据即失效）。
package builtin

import (
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/types"
)

// nonRecordAcknowledged = 语言原型结果**不是 record**、因而无字段集可对账的
// 合同键（显式承认，不是静默跳过）。理由写在值里。
//
// 加新键 = 必须一并想清楚「该节点的语言类型到底是什么」；键被修好后
// 必须从本表删除（表过期同样报红）。
var nonRecordAcknowledged = map[string]string{
	"coze-biz-1.0/knowledge": "语言原型 `knowledge()` 的结果是 array<SearchResult>（符号链），" +
		"而合同声明的语言字段是 `result` —— 两者不是同一个语言类型（`result` 属 OperationResult 形态）。" +
		"在裁定「coze-biz knowledge 节点的语言类型」之前不咨询原型（default deny），" +
		"调用方保留既有路径；本表即该项的显式承认。",
}

// checkContractPrototype 是判据本体：返回问题列表（空 = 通过）。
func checkContractPrototype() []string {
	var problems []string
	targets := append([]string(nil), DeclaredTargets()...)
	sort.Strings(targets)
	seenNonRecord := map[string]bool{}
	for _, target := range targets {
		keys := make([]string, 0)
		for k := range Contract(target) {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			full := target + "/" + key
			ports := PortsOf(target, key)
			if len(ports) == 0 {
				problems = append(problems, full+": 合同没有声明任何端口")
				continue
			}
			for _, p := range ports {
				if p.Port == "" || p.Lang == "" || p.Type == "" {
					problems = append(problems, full+": 端口声明不完整 "+p.Port+"/"+p.Lang+"/"+p.Type)
				}
				// 物理口必须映射回语言字段（raising 方向的可查性）。
				if lang, ok := LangOf(target, key, p.Port); !ok || lang != p.Lang {
					problems = append(problems, full+": 物理口 "+p.Port+" 未映射到语言字段 "+p.Lang)
				}
			}
			// 语言原型必须是**同名内建函数**（合同键与函数名同拼写）。
			res, ok := ResultType(key)
			if !ok {
				problems = append(problems, full+": 语言原型里没有同名内建函数 "+key)
				continue
			}
			if res.Kind != types.Record {
				seenNonRecord[full] = true
				if _, ack := nonRecordAcknowledged[full]; !ack {
					problems = append(problems, full+": 语言原型结果不是 record ⇒ 无字段集可对账；"+
						"要么让它与合同一致，要么在 nonRecordAcknowledged 里显式承认")
				}
				continue
			}
			// 两个权威描述同一个语言类型：字段集**相等**。
			if _, ok := ResultRecordAgrees(target, key); !ok {
				problems = append(problems, full+": 合同的语言字段集 "+strings.Join(LangFields(target, key), ",")+
					" 与语言原型 record 的字段集不一致 ⇒ 两处声明不是同一个语言类型")
			}
			for _, p := range ports {
				if _, ok := LangTypeOf(target, key, p.Port); !ok {
					problems = append(problems, full+": 端口 "+p.Port+"（语言字段 "+p.Lang+
						"）在语言原型里查不到类型（raising 定型会缺权威）")
				}
			}
		}
	}
	// 承认表过期同样报红（欠账只减不增）。但 target 不在本构建（O2 开放/闭源
	// 拆分：开放仓只带 dify 契约）时，该条目不适用，不算过期。
	declaredTargets := map[string]bool{}
	for _, t := range DeclaredTargets() {
		declaredTargets[t] = true
	}
	for full := range nonRecordAcknowledged {
		target := strings.SplitN(full, "/", 2)[0]
		if !declaredTargets[target] {
			continue
		}
		if !seenNonRecord[full] {
			problems = append(problems, full+": nonRecordAcknowledged 条目已过期（该键的语言原型现在可对账了）")
		}
	}
	return problems
}

func TestContractPrototypeConsistency(t *testing.T) {
	if problems := checkContractPrototype(); len(problems) > 0 {
		for _, p := range problems {
			t.Errorf("%s", p)
		}
	}
}

// TestContractPrototypeGateSelfTest 负向自检：判据必须是活的 ——
// 注入一处「合同声明了语言原型里没有的字段」必须报红。
func TestContractPrototypeGateSelfTest(t *testing.T) {
	if problems := checkContractPrototype(); len(problems) > 0 {
		t.Fatalf("基线不干净，自检无意义：%v", problems)
	}
	// 任取一个本构建里存在的 (target, key)：自检不能依赖某个具体平台
	// （开放仓只有 dify；闭源仓多 target）。
	targets := DeclaredTargets()
	sort.Strings(targets)
	if len(targets) == 0 {
		t.Fatal("合同里没有任何 target")
	}
	target := targets[0]
	keys := make([]string, 0, len(Contract(target)))
	for k := range Contract(target) {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		t.Fatalf("合同 %s 里没有任何节点键", target)
	}
	key := keys[0]
	orig, ok := NodeOf(target, key)
	if !ok {
		t.Fatalf("合同里没有 %s/%s", target, key)
	}
	damaged := orig
	damaged.Ports = append(append([]Port(nil), orig.Ports...), Port{Port: "nope", Lang: "nonexistent", Type: "string"})
	contracts[target][key] = damaged
	defer func() { contracts[target][key] = orig }()

	problems := checkContractPrototype()
	if len(problems) == 0 {
		t.Fatalf("注入「语言原型里不存在的字段」后判据仍绿 ⇒ 判据是死的")
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "不一致") && !strings.Contains(joined, "查不到类型") {
		t.Fatalf("报红位置不对（应落在字段集/类型查询上）：\n%s", joined)
	}
	// 撤掉注入即恢复绿（自检不得污染后续用例）。
	contracts[target][key] = orig
	if problems := checkContractPrototype(); len(problems) > 0 {
		t.Fatalf("恢复后仍红：%v", problems)
	}
}
