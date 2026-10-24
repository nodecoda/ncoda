package ir

// HTTPConfigKeys is the declared vocabulary of the `http()` config map — the
// **language-side** option set every target honors.
//
// It is the single declaration point: the lowering dispatch in buildHTTP
// switches over exactly these keys, and a key outside the list is rejected
// (never silently ignored — an unhandled key used to fall through to the
// generic ref collector, whose Sources land in HTTPCall.Inputs, a carrier only
// the reference renderer reads ⇒ on the coze targets the entry AND its data
// reference vanished while the compile reported SUCCEEDED; C2-H3 一手实测).
//
// `params`（查询参数）于 **2026-10-22 由用户裁定进入词表**（PRD
// `.omx/plans/prd-ncoda-exec-semantics.md` v1.8 的 P2 切片：
// `HTTPCall.Params` + `HTTPConfig.Headers/Params`）。证据分两层，必须分清：
//
//   - **行形状 = 代码一手**：平台读 `inputs.Params []*vo.Param`
//     （`oss:backend/domain/workflow/internal/nodes/httprequester/http_requester.go:428`），
//     与 `inputs.Headers` 同一 `{name, input:{type,value}}` 条目；本仓 parser
//     已按该形状把查询参数登记为 `__params_<md5>` 输入源
//     （`platforms/cozestudio/parser/adaptorhelpers.go:357`）。
//   - **语料先例 = 0**：869 份导出里 http 节点的 `parameters.params` 全部为空数组
//     ⇒ 非空行的**语料**先例不存在。这不是造形状（形状取自代码），但也不是
//     语料实证；ES-115 台账须保留这一诚实边界。
//
// 否决 `params` 的旧理由（“平台物理通道不是语言选项”）已不成立：query 是
// 请求语义的一部分，ncoda 若不承载它，反编译就只能把查询条件冻进 URL 字符串、
// 让数据引用无处可去（ES-031 的「只保留字面量」红灯）。
var HTTPConfigKeys = []string{"body", "headers", "params"}
