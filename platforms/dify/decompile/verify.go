package decompile

// verify.go — 反编译产物的前端验证（自验证 + 软降级）。
//
// DecompileYAML 产出 ncoda 后跑一遍前端管线（与测试 helper frontendRun
// 同一管线）:若唯一的错误族是 RETURN_TYPE（dify 多 answer/end 出口值
// 类型异构,ncoda 要求一致）→ 重试 `-> any` 声明;重试仍缺返回路径
// （MISSING_RETURN,dify 允许部分路径无 answer）→ 追加 `return "";`
// 兜底。软降级均记入 docs/DIFY-DECOMPILE.md。

import (
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/passes"
)

// difyRuntimeProfile 反编译产物的目标运行时画像（dify 默认）。
func difyRuntimeProfile() *passes.TargetProfile {
	p := passes.DefaultTargetProfile()
	return p
}

// noopTargetValidation 反编译产物的目标验证 = 语法 + 类型 + 语义正确性
// （compile_ok）。Dify 目标层拒绝 array start 输入等是平台真实缺口
// （dify 工作流无数组入口）,不是反编译错误——跳过 TargetValidation。
type noopTargetValidation struct{}

func (noopTargetValidation) Run(*ast.Program, *passes.Context) {}

// verifyNcoda 用 fixture 的 tool 注册表 + 无目标验证管线跑前端（限定名
// 工具调用是闭集契约,注册表 = dify YAML 就地取材的能力契约,见
// tool_registry.go）。
func verifyNcoda(nc, name, yamlText string) *passes.FrontendResult {
	pipeline := passes.New()
	register := func(stage passes.Stage, pass passes.Pass) {
		if err := pipeline.Register(stage, pass); err != nil {
			panic("decompile pipeline wiring: " + err.Error())
		}
	}
	register(passes.StageDesugar, &passes.DesugarPass{})
	register(passes.StageNormalize, &passes.NormalizePass{})
	register(passes.StageDefiniteInit, &passes.DefiniteInitPass{})
	register(passes.StageEffect, &passes.EffectPass{})
	register(passes.StageType, &passes.TypePass{})
	register(passes.StageParallel, &passes.ParallelAnalysisPass{})
	register(passes.StageLoopState, &passes.LoopStatePass{})
	register(passes.StageYieldContract, &passes.YieldContractPass{})
	register(passes.StageResponseContract, &passes.ResponseContractPass{})
	register(passes.StageCodeAnalysis, &passes.CodeAnalysisPass{})
	register(passes.StageSyntax, &passes.SyntaxPass{})
	register(passes.StageSemantic, &passes.SemanticPass{})
	register(passes.StageTargetValidation, noopTargetValidation{})
	register(passes.StageCapabilityGate, &passes.CapabilityGatePass{})
	if reg, err := ToolRegistryForYAML(yamlText); err == nil {
		pipeline = pipeline.WithToolRegistry(reg)
	}
	return pipeline.Run(nc, name, difyRuntimeProfile())
}

// countErrCodes 统计错误诊断数（severity == Error）。
func countErrCodes(res *passes.FrontendResult) int {
	if res == nil {
		return 0
	}
	return len(res.Errors())
}

// hasErrCode 报告结果中是否含某错误码。
func hasErrCode(res *passes.FrontendResult, code diag.Code) bool {
	if res == nil {
		return false
	}
	for _, d := range res.Errors() {
		if d.Code == code {
			return true
		}
	}
	return false
}

// softenMixedReturns 自验证软降级:RETURN_TYPE（多出口类型异构）→
// main 声明 `-> any`;仍 MISSING_RETURN（部分路径无 answer）→ 追加
// `return "";` 兜底。返回（ncoda, 是否改动）。
func softenMixedReturns(nc, yamlText string) (string, bool) {
	res := verifyNcoda(nc, "decompile", yamlText)
	if !hasErrCode(res, diag.RETURN_TYPE) {
		return nc, false
	}
	// 有 RETURN_TYPE → 重试 `-> any`。
	retry := addReturnAny(nc)
	res2 := verifyNcoda(retry, "decompile", yamlText)
	if countErrCodes(res2) >= countErrCodes(res) {
		return nc, false // 重试没改善 → 保留原样（错误如实暴露）
	}
	if hasErrCode(res2, diag.MISSING_RETURN) {
		// `-> any` 触发显式返回类型下的全路径返回检查（E1010）——dify
		// 允许部分路径无 answer,追加 `return "";` 兜底（软降级）。
		retry2 := appendReturnEmpty(retry)
		res3 := verifyNcoda(retry2, "decompile", yamlText)
		if countErrCodes(res3) <= countErrCodes(res2) {
			retry = retry2
		}
	}
	return retry, true
}

// trimReturnEmpty 去掉追加的 `return "";`（兜底未生效时）。

// addReturnAny 把 main 的 `{` 前插入 ` -> any`（首个 function main 行）。
func addReturnAny(nc string) string {
	idx := strings.Index(nc, "\nfunction main(")
	if idx < 0 {
		return nc
	}
	brace := strings.Index(nc[idx:], " {")
	if brace < 0 {
		return nc
	}
	pos := idx + brace
	// 已在 `-> any`（防御）→ 原样
	if strings.HasSuffix(strings.TrimSpace(nc[idx:pos]), "-> any") {
		return nc
	}
	return nc[:pos] + " -> any" + nc[pos:]
}

// appendReturnEmpty 在 main 体末尾（最后一个 `}` 前）追加 `return "";`。
func appendReturnEmpty(nc string) string {
	idx := strings.LastIndex(nc, "\n}")
	if idx < 0 {
		return nc
	}
	return nc[:idx] + "\n    return \"\";" + nc[idx:]
}

// trimReturnEmpty 去掉追加的 `return "";`（兜底未生效时）。
func trimReturnEmpty(nc string, res *passes.FrontendResult) string {
	if hasErrCode(res, diag.MISSING_RETURN) {
		return nc // 仍缺返回 → 保留兜底（少错优先）
	}
	return strings.Replace(nc, "\n    return \"\";", "", 1)
}
