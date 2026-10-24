package compiler

// code_syntax.go — 编译期 foreign-code 语法投影（P6）。
//
// 这是 runtime load 期权威判定（SEM-CODE-001）的派生投影，不是第二定义：
// 两边共用 pkg/codeparse 的同一判定实现。差别只有证据等级 —— 这里用本机
// 解释器（预测/尽早反馈），runtime 用实际执行解释器（观测）。
//
// 2026-10-25（ASSET-BOUNDARY）：自商业 internal/compiler 迁入。它是语言级
// 事实（用户写的代码在真实解释器上良构吗），不是平台事实；此前只有商业的
// 语言 lane 跑它，导致开源 lane 对同一语料多判 2 例通过（869 vs 867）。
//
// 与 CODE_SOURCE_INVALID 的分工（两个不同事实，不得合并）：
//   - CODE_SOURCE_INVALID：pkg/passes/code_analysis.go 的受限扫描器，服务
//     对象是 python-code-shim 的源码模板（python3 only，JS 明确透传）。
//   - CODE_SYNTAX（本文件）：对用户手写 foreign code 的真实 CPython / Node
//     解析，两种语言都覆盖。

import (
	"fmt"
	"strings"

	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/codeparse"
)

// CodeSyntaxDiags checks every user-written foreign code expression with the
// real language parser. It returns diagnostics only; authority stays at
// runtime load.
func CodeSyntaxDiags(prog *binding.TypedBoundProgram) []DiagJSON {
	if prog == nil || len(prog.CodeExpressions) == 0 {
		return nil
	}

	items := make([]codeparse.Item, 0, len(prog.CodeExpressions))
	for id, ce := range prog.CodeExpressions {
		if ce == nil || strings.TrimSpace(ce.Source) == "" {
			continue
		}
		lang := normalizeCompilerCodeLang(ce.Language)
		if lang == "" {
			continue
		}
		items = append(items, codeparse.Item{ID: fmt.Sprint(int(id)), Lang: lang, Code: ce.Source})
	}
	if len(items) == 0 {
		return nil
	}

	results, _, err := codeparse.Check(items)
	if err != nil {
		// The check itself could not run (no interpreter). That is not a
		// source verdict: report the gap honestly and never fake a pass.
		return []DiagJSON{{
			Code:     DiagCodeCodeSyntaxUnverified,
			Severity: "warning",
			Message:  fmt.Sprintf("code syntax check unavailable: %v", err),
		}}
	}

	var out []DiagJSON
	for _, r := range results {
		if r.OK {
			continue
		}
		out = append(out, DiagJSON{
			Code:     DiagCodeCodeSyntax,
			Severity: "error",
			Message:  fmt.Sprintf("Invalid code source (node %s): %s", r.ID, r.Err),
		})
	}
	// Version skew is an environment fact, not a source defect, and it is
	// emitted once per workflow by the runtime load preflight (SEM-CODE-003).
	return out
}

func normalizeCompilerCodeLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "python", "python3":
		return "python"
	case "javascript", "js", "nodejs":
		return "javascript"
	}
	return ""
}
