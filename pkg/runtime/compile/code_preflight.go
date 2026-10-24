package compile

// code_preflight.go — load 期 foreign-code 预检（P6 · SEM-CODE-001/002/003）。
//
// 为什么在 load：用户代码是**逃逸出类型化 IR 的不透明载荷**。语义模型对它
// 唯一能断言的事实是「良构」，而良构是 (source, language, version) 的函数，
// 其中 version 是环境事实（F6）——只有 runtime 拿得到实际解释器版本。
// 因此权威判定在这里，编译侧只做同规则的派生投影（CODE_SYNTAX 诊断）。
//
// 为什么在节点实例化之前、且不按需：`buildGraph.build` 一次性看到整张
// （子）图的节点表，判定是**分支无关**的。懒加载会让未达分支里的语法错
// 永远不暴露——那正是本切片要修的缺陷。
//
// 边界：compile-only。不 import、不执行、不触碰网络/文件。

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/codeparse"
	"github.com/nodecoda/ncoda/pkg/runtime/diag"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/runtime/trace"
)

// preflightCode compiles every user-written code node in this (sub)graph.
// Synthetic shims are compiler-generated and already parsed by ParseShim at
// Build, so they are out of scope here.
func (b *buildGraph) preflightCode(s *spec.Spec) error {
	type nodeItem struct {
		id   string
		lang string
		code string
	}
	var nodes []nodeItem
	for i := range s.Nodes {
		n := &s.Nodes[i]
		if n.Kind != spec.KindCode {
			continue
		}
		cfg, err := spec.DecodeConfig(n.Kind, n.Config)
		if err != nil {
			continue // Build reports malformed config with its own code.
		}
		cc, ok := cfg.(*spec.CodeConfig)
		if !ok || cc.Synthetic {
			continue
		}
		lang := normalizeCodeLang(cc.Language)
		if lang == "" {
			continue // language gate owns the verdict (RCODE_LANGUAGE_UNSUPPORTED).
		}
		if strings.TrimSpace(cc.Code) == "" {
			// An empty body is a source-contract question for the language
			// gate, not a parse question: compile("") is legal Python.
			continue
		}
		nodes = append(nodes, nodeItem{id: n.ID, lang: lang, code: cc.Code})
	}
	if len(nodes) == 0 {
		return nil
	}

	items := make([]codeparse.Item, len(nodes))
	for i, n := range nodes {
		items[i] = codeparse.Item{ID: n.id, Lang: n.lang, Code: n.code}
	}
	results, versions, err := codeparse.Check(items)
	if err != nil {
		if miss, ok := err.(*codeparse.MissingInterpreterError); ok {
			return diag.New(diag.RCodeLanguageUnsupported, "code preflight: %v", miss)
		}
		return diag.New(diag.RCodeSyntax, "code preflight: %v", err)
	}
	byID := make(map[string]codeparse.Result, len(results))
	for _, r := range results {
		byID[r.ID] = r
	}

	// SEM-CODE-003: the interpreter actually used is an environment fact. When
	// a declared platform version exists and its major.minor differs, record
	// the skew once per graph — never as a syntax error.
	if versions.Python != "" && codeparse.DeclaredPythonVersion != "" &&
		!codeparse.SameMajorMinor(versions.Python, codeparse.DeclaredPythonVersion) {
		b.emitCodeRecord(trace.Record{
			Stage: "load", RuleID: trace.RuleCodeVersion, Origin: trace.OriginPolicy,
			Evidence: "hand:docs/five-step/platform_model.yaml:1", CheckRule: "version",
			Input: "declared=" + codeparse.DeclaredPythonVersion, Output: "actual=" + versions.Python,
			Decision: "gap", Provenance: versions.String(),
		})
	}

	var firstErr error
	for _, n := range nodes {
		r, ok := byID[n.id]
		if !ok {
			continue
		}
		decision, out := "passed", "ok"
		if !r.OK {
			decision, out = "error", "error"
			if firstErr == nil {
				firstErr = diag.New(diag.RCodeSyntax,
					"node %s: %s source does not parse: %s", n.id, n.lang, r.Err)
			}
		}
		b.emitCodeRecord(trace.Record{
			Stage: "load", RuleID: trace.RuleCodeSyntax, Origin: trace.OriginWirePresent,
			Evidence: "observed:artifact:node:" + n.id, CheckRule: "syntax",
			Node: n.id, Kind: spec.KindCode, Input: "source", Output: out,
			Decision: decision, Provenance: versions.String(),
		})
		if n.lang != "python" {
			continue
		}
		black := codeparse.BlacklistedPythonModules(n.code)
		impDecision, impOut := "passed", "ok"
		if len(black) > 0 {
			impDecision, impOut = "error", "error"
			if firstErr == nil {
				firstErr = diag.New(diag.RCodeSyntax,
					"node %s: python imports module(s) the platform removes: %s", n.id, strings.Join(black, ", "))
			}
		}
		b.emitCodeRecord(trace.Record{
			Stage: "load", RuleID: trace.RuleCodeImport, Origin: trace.OriginPolicy,
			Evidence: "hand:docs/five-step/platform_model.yaml:1", CheckRule: "import",
			Node: n.id, Kind: spec.KindCode, Input: "imports", Output: impOut,
			Decision: impDecision, Provenance: versions.String(),
		})
	}
	return firstErr
}

// emitCodeRecord writes one full P6 record. Every record carries attempt/axis
// and a check_rule, so ValidateFull holds without inventing a fill_rule.
func (b *buildGraph) emitCodeRecord(r trace.Record) {
	if b.deps.SemTrace == nil {
		return
	}
	if r.Attempt == 0 {
		r.Attempt = 1
	}
	if r.Axis == "" {
		r.Axis = "per_attempt"
	}
	if r.DeadlineMS < 0 {
		r.DeadlineMS = 0
	}
	b.deps.SemTrace(r)
}

func normalizeCodeLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "python", "python3":
		return "python"
	case "javascript", "js", "nodejs":
		return "javascript"
	}
	return ""
}
