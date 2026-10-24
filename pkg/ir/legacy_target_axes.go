package ir

// legacy_target_axes.go —— **过渡桥（P2d 发版批次删除）**。
//
// 商业仓 v0.6.0 的 capability.Table 还不带 OnErrorWire / LoopReturnShape /
// LLMEnvelopePlacement；在适配器显式声明之前，内核按历史目标名给出旧值，
// 保证产物逐字节不变。
//
// 边界：这些是**字符串字面量**（不是标识符），且只在本文件出现；
// scripts/audit_split.py S6 因此不把它们当依赖。内核打 tag、商业仓 bump、
// 适配器显式声明之后，本文件整段删除 —— 那时内核不认识任何平台名。
//
// 台账：docs/PLATFORM-IDENTITY-RESIDUALS.yaml 的 transitional_name_gates。

import "github.com/nodecoda/ncoda/pkg/capability"

// legacyOnErrorWire returns the pre-declaration wire spelling for a target.
func legacyOnErrorWire(target, mode string) (string, bool) {
	switch {
	case isNodeGraphTarget(target):
		wire, ok := nodeGraphOnErrorWire[mode]
		return wire, ok
	}
	return "", false
}

// nodeGraphOnErrorWire 是节点图载体在适配器声明前使用的线拼写表。
// **唯一实现处**：判据见 on_error_mode_single_implementation_test.go。
var nodeGraphOnErrorWire = map[string]string{
	"terminate":     "terminated",
	"keep_null":     "continue-on-error",
	"remove_failed": "remove-abnormal-output",
}

// legacyLoopReturnStateGate reports whether the target needs the state-gate
// loop-return shape before the adapter declares LoopReturnShape.
func legacyLoopReturnStateGate(target string) bool {
	return target == "coze-biz-1.0"
}

// legacyEnvelopePlacement returns the pre-declaration envelope placement.
func legacyEnvelopePlacement(target string) capability.EnvelopePlacement {
	switch target {
	case "dify-1.16-graphon-0.6", "coze-studio-flow-1.0", "coze-1.0", "coze-biz-1.0":
		return capability.EnvelopeShim
	case "coze-runtime-1.0":
		return capability.EnvelopeNative
	}
	return capability.EnvelopeUnspecified
}

// isNodeGraphTarget names the families whose products are node graphs (the
// reference carrier and the coze families). Transitional only.
func isNodeGraphTarget(target string) bool {
	switch {
	case target == "dify-1.16-graphon-0.6":
		return true
	case len(target) >= 4 && target[:4] == "coze":
		return true
	}
	return false
}
