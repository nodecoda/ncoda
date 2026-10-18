package ir

// HTTPConfigKeys is the declared vocabulary of the `http()` config map — the
// **language-side** option set every target honors.
//
// It is the single declaration point: the lowering dispatch in buildHTTP
// switches over exactly these keys, and a key outside the list is rejected
// (never silently ignored — an unhandled key used to fall through to the
// generic ref collector, whose Sources land in HTTPCall.Inputs, a carrier only
// the dify renderer reads ⇒ on the coze targets the entry AND its data
// reference vanished while the compile reported SUCCEEDED; C2-H3 一手实测).
//
// 为什么是删表外键而不是补 `params`：`params` 是平台自己的查询参数通道
// （coze-studio `http_requester.go` 读 `inputs.Params []*vo.Param`，与
// `inputs.Headers` 同一机制、同一 `{name, input:{type,value}}` 条目形状），
// 但 ncoda 把查询参数写在 url 模板里 ⇒ 它是平台物理通道，不是语言选项；
// 且**语料 3/3 `params: []`** ⇒ 非空参数行的 wire 形状无一手先例，
// 凭空造形状是本仓明令禁止的。`params` 是否进入语言词表 = 待用户裁定
// （docs/five-step/rules/C2-http-call.yaml 的 C2-H3）。
//
// 本文件与 build_call.go 分开是**判据要求**，不是风格：repair 记录的 undo
// 只退回 build_call.go（撤掉「表外键报错」这一条规则），本声明仍在 ⇒
// 契约测试仍可编译 ⇒ 撤实现后的 RED 精确落在规则面上，而不是编译失败。
var HTTPConfigKeys = []string{"body", "headers"}
