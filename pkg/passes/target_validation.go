package passes

// TargetValidationPass enforces the target-specific declaration checks kept
// outside the language type system, ported from
// src/nclang/lang/passes/target_validation.py (1575 lines). This task
// (17C2a) ports the reachable surface: run's three declaration passes, the
// visit_* branches reachable without the missing faces, the shared helpers,
// and the human_input_contract port (human_input_contract.go) plus the
// Context.ValidatedHumanInputs publication.
//
// Reachable function surface (Python line refs):
//
//	run                                :161-168
//	_validate_start_input_shapes       :170-196
//	_validate_declared_file_types      :197-213
//	_validate_type_ref                 :215-224
//	_validate_file_constraints         :226-245
//	visit_var_decl_stmt                :254-260
//	visit_for_stmt                     :261-264
//	visit_call_expr (partial)          :266-293
//	visit_request_input_stmt           :396-453
//	_validate_human_input_fields       :454-551
//	_static_human_input_options        :552-566
//	_validate_human_input_actions      :567-657
//	_validate_human_input_timeout      :658-688
//	_human_input_map / _static_target_map :689-714
//	visit_if_stmt / visit_while_stmt   :1129-1140
//	visit_return_stmt / visit_yield_stmt / visit_answer_stmt / visit_output_stmt :1141-1164
//	_validate_return_expr              :1165-1173
//	_validate_generated_code_expr      :1174-1190
//	_validate_extract_text_operand     :1191-1199
//	_validate_vision_operand           :1200-1207
//	visit_field_access_expr            :1208-1221
//	visit_assign_stmt                  :1222-1294
//	_validate_conversation_declarations :1295-1322
//	_reject / _reject_at               :1323-1332
//	_assigner_rhs / _array_variable_input_supported :1333-1367
//	_descriptor_for_ref / _operation_for_call       :1368-1380
//	_is_native_selector_call / _is_native_materialization_call :1381-1396
//	_is_direct_selector_expr / _is_direct_condition_expr         :1397-1406
//	_is_compiler_generated_code_expression / _scalar_file_selectors :1407-1456
//	_map_value / _static_map_key       :1457-1471
//	_file_descriptor / _is_scalar_file / _is_selector_expr       :1491-1506
//	_extractable_file_descriptor       :1507-1518
//	_conversation_store_supported      :1519-1530
//	_is_literal / _default_supported   :1531-1575
//
// GATING (recorded per the task brief, oracle-verified): the Go binder does
// not register the extract operation builtin (Task 10 deviation —
// mainBuiltins registers llm/http/knowledge among its general builtins and
// the remaining operations register as builtins as of Tasks 25c/25d/25e/25f;
// the tool() builtin was deleted (TOOL-DIRECT-CALL-FORM decision 5)), so
// every operation call resolves at the scope phase and reaches this pass.
// The remaining call branch is therefore reachable; its helper surface:
//
//   - _validate_agent_call :295-357 (+ _validate_agent_model,
//     _validate_agent_tools) — PORTED as of Task 25d (validateAgentCall /
//     validateAgentModel / validateAgentTools; agent_profiles /
//     validated_agents / AgentCallContract land in this task).
//   - _validate_classification_call :715-852 (+ _validate_classification_
//     model / _validate_classification_config / _validate_classification_
//     labels / _validate_classification_fallback / _static_classification_
//     map) — PORTED as of Task 25e (validateClassificationCall /
//     validateClassificationModel / validateClassificationConfig /
//     validateClassificationLabels / validateClassificationFallback /
//     staticClassificationMap; validated_classifications /
//     ValidatedClassificationContract land in this task).
//   - _validate_extraction_call :854-949 (+ _validate_extraction_fields /
//     _validate_extraction_model / _validate_extraction_config /
//     _validate_extraction_descriptions / _static_extraction_map) — PORTED
//     as of Task 25f (validateExtractionCall / validateExtractionFields /
//     validateExtractionModel / validateExtractionConfig /
//     validateExtractionDescriptions / staticExtractionMap;
//     extraction_contracts / validated_extractions /
//     ExtractionContract / ValidatedExtractionContract land in this task).
//   - _unsupported_extraction_shape :1472-1490 — PORTED (unsupported
//     ExtractionShape).
//
// _validate_extract_text_operand and _validate_vision_operand ARE ported
// (whitebox-testable; extract_text/llm become reachable once the operation
// builtins register).
//
// DEVIATIONS:
//   - reportedScalarFileExprs keys on binding.AstNodeID via NodeIDOf
//     (Python: id(selector) node identity). Both are per-node-unique within
//     one compilation; recorded in the report.
//   - Go's ast.Walk child coverage is a superset of the Python base
//     visitor's (type refs, call type_args, params, const-decl values) —
//     same note as code_analysis; none of the extra children host a
//     generated-code shape, so the observable surface is identical.
//   - can_lower_list_operator is shared with the capability gate; it needs
//     the declInits map, which Run pre-enumerates (buildDeclInits) the same
//     way the gate does.

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/binding"
	"github.com/nodecoda/ncoda/pkg/diag"
	"github.com/nodecoda/ncoda/pkg/target/codec"
	"github.com/nodecoda/ncoda/pkg/types"
)

// ── module constants (target_validation.py:52-145) ────────────────────

// supportedFileTypes mirrors SUPPORTED_FILE_TYPES (:52).
//
// **2026-09-21 语言级扩展（用户拍板 B「与平台名册同构」+ A「`code` 补入」）**：
// 从 5 个 token 扩到 **12**，与平台 12 族名册**同构** —— 两侧各只剩一个已登记的
// 半边：`custom`（语言有、平台无拼写 ⇒ 放宽到裸 `file`）。
// 规则与证据 = `docs/five-step/spec/file-family-canonical-form.md` **A2**；
// 与平台名册的同构对账 = `platforms/cozebiz/decompile/file_token_roster_contract_test.go`。
//
// **闭集里存的是「名字」，不是「拼写」**（`code` 的区别所在）：`code` 是 ncoda
// 关键字（`pkg/token/token.go:180`）⇒ 源码里唯一合法拼写是 **`$code`**
// （`$` 是**纯词法**转义，`pkg/lexer/lexer.go:439`；`file<$code>` 解析出的
// lexeme 仍是 `code`，故本表按名字判）。裸 `file<code>` 是**语法错误**，不是语义
// 拒绝 —— 这条差别只影响**发射**：反编译/渲染侧必须用 `token.EscapedSpelling`
// 把名字折回合法拼写，否则会发射出**自己编译不过**的 ncoda（实测陷阱，判据
// `file_token_roster_contract_test.go` ④ 端到端拦下）。
var supportedFileTypes = map[string]bool{
	"document": true, "image": true, "audio": true, "video": true, "custom": true,
	"txt": true, "ppt": true, "excel": true, "zip": true, "svg": true, "voice": true,
	"code": true,
}

// FileTypeTokens 返回 `file<…>` types 轴闭集的**确定性快照**（升序）。
//
// 为什么要有这个出口（与 `token.Keywords()` 同一原则，2026-09-16 先例）：
// 语言面必须**可机械枚举**，否则「实现接受哪些 token」只能靠人读 map 字面量 ——
// 而平台侧名册是机械派生的，两侧对账就只能人肉。出口让「语言闭集 ↔ 平台族名册
// 同构」成为可复算判据（见 platforms/cozebiz/decompile/file_token_roster_contract_test.go）。
//
// 返回的是**名字**（如 `code`），不是源码**拼写**（`$code`）—— 两者在关键字上不同，
// 折回拼写走 `pkg/token.EscapedSpelling`（同一处规则，勿在调用点另写）。
func FileTypeTokens() []string {
	out := make([]string, 0, len(supportedFileTypes))
	for tok := range supportedFileTypes {
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

// supportedUploadMethods mirrors SUPPORTED_UPLOAD_METHODS (:53).
var supportedUploadMethods = map[string]bool{"local_file": true, "remote_url": true}

// extractableExtensions mirrors EXTRACTABLE_EXTENSIONS (:79-145): the closed
// set of file extensions with a proven Graphon extract_text legacy.
var extractableExtensions = map[string]bool{
	".txt": true, ".markdown": true, ".md": true, ".mdx": true,
	".html": true, ".htm": true, ".xml": true,
	".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cc": true,
	".cxx": true, ".c++": true,
	".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".java": true, ".php": true, ".rb": true, ".go": true, ".rs": true,
	".swift": true, ".kt": true, ".scala": true,
	".sh": true, ".bash": true, ".bat": true, ".ps1": true,
	".sql": true, ".r": true, ".m": true, ".pl": true, ".lua": true,
	".vim": true, ".asm": true, ".s": true,
	".css": true, ".scss": true, ".less": true, ".sass": true,
	".ini": true, ".cfg": true, ".conf": true, ".toml": true,
	".env": true, ".log": true, ".vtt": true,
	".json": true, ".yaml": true, ".yml": true,
	".pdf": true, ".doc": true, ".docx": true,
	".csv": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".epub": true, ".odt": true, ".eml": true, ".msg": true,
	".properties": true,
}

// assignerRHSKind is the Go form of AssignerRHSKind (:48).
type assignerRHSKind string

const (
	assignerLiteral     assignerRHSKind = "literal"
	assignerDynamic     assignerRHSKind = "dynamic"
	assignerUnsupported assignerRHSKind = "unsupported"
)

// classifyAssignerRhs mirrors classify_assigner_rhs (:149-157).
func classifyAssignerRhs(expr ast.Expr, target *types.Descriptor) assignerRHSKind {
	if !conversationStoreSupported(target) {
		return assignerUnsupported
	}
	switch expr.(type) {
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit,
		*ast.NullLit, *ast.ArrayLit, *ast.MapLit:
		return assignerLiteral
	}
	return assignerDynamic
}

// ── the pass ──────────────────────────────────────────────────────────

// TargetValidationPass validates whether typed language constructs are
// representable by Dify, mirroring target_validation.py TargetValidationPass.
type TargetValidationPass struct {
	ctx      *Context
	mode     string
	manifest *CapabilityManifest
	// declInits is the recorded Go equivalent of the Python
	// ctx.declaration_node surface needed by can_lower_list_operator
	// (capability_gate.py:69-110) — see buildDeclInits (capability_gate.go).
	declInits map[binding.SymbolID]ast.Expr
	// reportedScalarFileExprs mirrors _reported_scalar_file_exprs (:161):
	// Python stores id(selector) node identities; the Go form stores the
	// snapshot AstNodeID (NodeIDOf) — deviation recorded in the report.
	reportedScalarFileExprs map[binding.AstNodeID]bool
	// fileCapacity 当前目标的 file 契约承载能力（Run 解析一次；一处定义见
	// fileContractCapacityOf）。**两条分支共用** —— dify 也消费它。
	fileCapacity fileContractCapacity
}

// Run mirrors run (:161-168). The pass enforces the Dify target's
// representability rules; coze/coze-biz lower the same platform-neutral AST
// through their own backends and are not subject to Dify-specific shape
// constraints (e.g. scalar file field access is representable on coze-biz,
// where a file is a URL string). Gate on the profile name.
//
// 2026-09-21（Stage A）：**dify-only 的那半仍然 dify-only**（形状约束），但
// **语言级的那半对每个目标都跑** —— 合法性（`file<…>` token 的词表校验）与
// file 契约轴的降级报账（见下方非 dify 分支）。旧实现整段 return ⇒
// 同一份源在 coze-biz 上比 dify 宽松（`file<bogus>` 能过），是**目标相关的语言**。
func (p *TargetValidationPass) Run(program *ast.Program, ctx *Context) {
	p.ctx = ctx
	// 承载能力**在入口解析一次**，两条分支共用（一处定义；dify 也走这张表 ——
	// 否则「dify 三轴俱载」这条事实永远不被消费 = 空判据）。
	p.fileCapacity = fileContractCapacityOf(ctx.Profile)
	// 枚举值集的承载（R-57）与目标相关，但**判据对每个目标都跑**：dify 的
	// 「payload 面承载」这条事实否则永不被消费（= 空判据，与 file 三轴同一条纪律）。
	p.reportEnumValueSetDegradations(program)
	p.reportNullBindingDegradations(program)
	p.reportTimeoutAxisDegradations(program)
	p.reportRetryIntervalAxisDegradations(program)
	p.reportParallelForErrorModeDegradations(program)
	// 零值路径的 merge 承载（D-P18-1 ③ / D-P19-1 ④ 出口①）**已退役（2026-09-28）**：
	// 该出口要求「平台承载不了『某条路径显式写零值且要赢过非零基线』时报账」，而 lowering
	// 自 R-125 收口起**已承载全部词法可判的形状**（默认位门控以「本单元有作者写的零值」
	// 为条件 ⇒ 臂外非零基座被搬进未写臂）⇒ 该出口的定义域为空，判据退役。不变量改由
	// `pkg/ir/zero_arm_default_gate_contract_test.go`（构造期）+
	// `internal/compiler/cozebiz_zero_path_arm_gating_contract_test.go`（产物面）锁定；
	// 退役裁决与证据 = `docs/five-step/rulings/2026-09-28-ruling-sheet-r26.md`。
	if !isDifyProfile(ctx.Profile) {
		// ── 非 dify 目标（2026-09-21 Stage A）──
		//
		// 旧实现整段 return，于是「语言级的那半」也一起被跳过了：
		// `TargetValidationPass` 名义上只做 dify 形状约束，实际却成了**目标相关的
		// 语言**——同一份源 coze-biz 绿、dify 红（实测：`file<bogus>` 在 coze-biz
		// `SUCCEEDED` 并静默落成 `type: file`）。
		//
		// 现在只跳过 **dify 的形状约束**，不跳过语言级判据：把每个 `file<…>` 声明位
		// 送进 `validateTypeRef`（与 dify 路径**同一个入口** ⇒ 合法性 + 承载报账
		// 只有一份实现）。非 dify 路径不能用 pass 自身的 `Visit` 分发（那是 dify 的
		// 形状规则），故用一个只认 `*ast.FileTypeRef` 的遍历器。
		//
		// 不变量（判据 `TestFileConstraintLegalityRunsForEveryTarget`，一条判据两个
		// 目标）：每个目标的每个 `file<…>` 声明位都必须经过合法性校验。
		p.mode = program.Mode()
		p.manifest = DefaultCapabilityManifest()
		if ctx.Profile != nil && ctx.Profile.Capabilities != nil {
			p.manifest = ctx.Profile.Capabilities
		}
		ast.Walk(typeRefChecker{pass: p}, program)
		// 同一道门里的第二个缺口（R-108，2026-09-26）：**IR 消费的契约也必须
		// 每个目标都发布**。`ValidatedExtractions` / `ValidatedAgents` 由本 pass 的
		// dify 分支填充，而非 dify 目标整段 return ⇒ 两个表恒为空 ⇒ IR 拿不到
		// instruction / descriptions / strategy（一手：coze-biz 编 57 源
		// SUCCEEDED，产物 llmParam 里连 prompt 都没有 ⇒ 反编译只能还出 llm<T>）。
		// 判据 = 发布者与 dify 路径**同一个函数**（不写第二份），只跳过 dify 的
		// 形状约束。表里没有 IR 消费者的契约（classification 只被 dify lowering
		// 读）不在此列 —— 判据是「IR 读不读它」，不是节点类型清单。
		ast.Walk(languageContractChecker{pass: p}, program)
		return
	}
	p.mode = program.Mode()
	p.manifest = DefaultCapabilityManifest()
	if ctx.Profile != nil && ctx.Profile.Capabilities != nil {
		p.manifest = ctx.Profile.Capabilities
	}
	p.declInits = buildDeclInits(program, ctx)
	p.reportedScalarFileExprs = map[binding.AstNodeID]bool{}
	p.validateConversationDeclarations(program, ctx)
	p.validateDeclaredFileTypes(program)
	p.validateStartInputShapes(program, ctx)
	ast.Walk(p, program)
}

// isDifyProfile reports whether the active target profile is the dify family
// (the only family whose shape constraints this pass enforces in full).
func isDifyProfile(profile *TargetProfile) bool {
	if profile == nil {
		return true // 缺省 profile = dify（DefaultTargetProfile）
	}
	return strings.HasPrefix(profile.Name, "dify")
}

// fileContractCapacity 一个目标**物理承载** file 契约三轴的能力 —— **一处定义**。
//
// 依据 = 渲染器**实际发出的字段**（实测，不是愿望）：
//
//   - dify（`pkg/backend/dify/serialize.go`）：三个 YAML 键
//     `allowed_file_types` / `allowed_file_extensions` /
//     `allowed_file_upload_methods`，且都是**列表** ⇒ 三轴俱载、都承载集合。
//     判据 = `TestDifyFileContractAxesAreCarriedWithoutDegradation`。
//   - coze-biz：canvas `assistType` 是**单个整数**、yaml 通道 `type: <image|…>`
//     也是单值 ⇒ kind 只承载**单值**；ext / upload **无字段**
//     （覆盖登记见 `docs/coze-biz-schema-audit.md` §6.9 F-AXES-4/5/6）。
//   - coze-studio（`coze-1.0` / `coze-studio-flow-1.0`）：**实测无族承载面** ——
//     `function main(file<image> d)` 的 start 口产物是 `{type: "string"}`，
//     整份产物里 `assistType` 出现 **0 次**。故 kind 也不承载
//     （连「这是 file」都没说，更别说族）。
//   - **未知目标 ⇒ 什么都不承载**（保守 = 不发明能力）。宁可多报一条**可见**的降级
//     警告，也不要静默丢掉约束 —— ④「要么报账、要么不存在」的失败侧是**静默**。
type fileContractCapacity struct {
	kinds        bool // 有 kind 承载面
	kindsAreSets bool // 承载集合（多值）；false = 只承载单值
	exts         bool // 有扩展名承载面
	uploads      bool // 有上传方式承载面
}

// fileContractCapacityOf 目标 → 承载能力。**表外目标 = 零值（什么都不承载）**：
// 加目标要显式加一行，这是有意的 —— 不发明能力。
func fileContractCapacityOf(profile *TargetProfile) fileContractCapacity {
	switch {
	case isDifyProfile(profile):
		return fileContractCapacity{kinds: true, kindsAreSets: true, exts: true, uploads: true}
	case isCozeBizProfile(profile):
		return fileContractCapacity{kinds: true}
	}
	return fileContractCapacity{}
}

// timeoutCarrierAxis 目标上**非 http 面**的「超时」载体表达的是哪个轴 —— **一处定义**。
//
// 为什么需要它：语言裁定 `with timeout(D)` 的 D = **每次尝试**（2026-09-16 用户拍板，与
// runtime / dify 两侧一致）。平台上「超时」有两个载体，轴**不同**：
//
//   - **http 节点自带** `parameters.setting.timeout`（**秒**）：平台运行期把它装进
//     `http.Client.Timeout`，重试循环 `for i := 0; i <= retryTimes; i++ { client.Do(…) }`
//     在**循环内**每次新建 ⇒ 轴 = **每次尝试**（平台一手：
//     `oss:backend/domain/workflow/internal/nodes/httprequester/http_requester.go:293,428`）。
//   - **通用节点级** `settingOnError.timeoutMs`：deadline 在**重试循环之外**建立
//     （平台一手：`oss:backend/domain/workflow/internal/compose/node_runner.go:548`
//     的 `context.WithTimeout(ctx, timeoutMS)` 在 `newNodeRunner` 里、而重试循环在
//     `invoke` 里复用同一个 ctx）⇒ 轴 = **全部重试共享的总预算**（平台字段注释同口径：
//     `oss:backend/domain/workflow/entity/vo/canvas.go:611`「If any retry happens, ALL
//     retry attempts accumulates to the same timeout threshold」）。
//
// ⇒ 第二类载体在 `retryTimes ≥ 1` 时**把每次尝试的 D 读成总预算**。作者以为「每次最多
// D」，实际是「全部尝试合起来最多 D」：单次仍受 D 约束（收紧的是**重试**的供给，不是
// 单次的界），但后续重试可能拿不到时间片。这是**目标承载**问题的报账对象，不是语言错
// （语言裁定未变、源码合法）⇒ 处置 = 软降级 + **报账**（不许静默），与 file 三轴 /
// 枚举值集 / 显式 null 同一条纪律：**不升级成 error**（那会拒收合法源码）。
type timeoutCarrierAxis int

const (
	// timeoutAxisUnknown 该目标的载体轴**无证据** ⇒ **不发结论**（既不报账也不声称承载）。
	//
	// 与 file 三轴口径的差别是有意的：那里「表外目标 = 不承载」是**保守地多报一条可见
	// 警告**（宁可多报）；这里表外目标若照报，等于**捏造一个我们没验过的轴**（说「平台
	// 按总预算读」而其实不知道）⇒ 谎报比沉默更坏。未知面登记在
	// `docs/five-step/residuals.yaml#R-89`。
	timeoutAxisUnknown timeoutCarrierAxis = iota
	// timeoutAxisPerAttempt 载体逐次生效（与语言裁定同轴）⇒ 不报。
	timeoutAxisPerAttempt
	// timeoutAxisSharedBudget 载体是全部尝试共享的总预算 ⇒ 与语言轴**不同** ⇒ 报账。
	timeoutAxisSharedBudget
)

// nonHTTPTimeoutAxisOf 目标 → **非 http 面**的超时载体轴。
//
//   - dify：`pkg/backend/dify/render_nodes.go:288-291` 发
//     `timeout{max_connect_timeout, max_read_timeout}`（每次请求各一份）⇒ 每次尝试。
//   - coze-biz / coze-studio：非 http 节点只有 `settingOnError.timeoutMs`
//     （`internal/backend/cozebiz/render_node.go::errorPolicySetting` ·
//     `internal/backend/coze/marshal.go:833`）⇒ 总预算。平台证据同上（coze-studio 的
//     OSS 实现就是该平台的运行期实现；coze-biz 与该实现同源 —— 本仓既有口径亦如此，
//     见 `internal/backend/cozebiz/http_setting.go` 的引用）。
//   - 表外目标 ⇒ `timeoutAxisUnknown`（不发明结论）。
func nonHTTPTimeoutAxisOf(profile *TargetProfile) timeoutCarrierAxis {
	switch {
	case isDifyProfile(profile):
		return timeoutAxisPerAttempt
	case isCozeBizProfile(profile), isCozeStudioProfile(profile):
		return timeoutAxisSharedBudget
	}
	return timeoutAxisUnknown
}

// isCozeStudioProfile 目标是否 coze-studio 家族（与 coze-biz 共用 settingOnError 载体）。
func isCozeStudioProfile(profile *TargetProfile) bool {
	if profile == nil {
		return false
	}
	return strings.HasPrefix(profile.Name, "coze-studio") ||
		strings.HasPrefix(profile.Name, "coze-1.")
}

// hasPerAttemptTimeoutCarrier 操作符在**该目标**上是否有「逐次生效」的超时载体。
//
// 唯一为真的是 **http 内建**（`parameters.setting.timeout`）：实现在
// `internal/backend/cozebiz/http_setting.go::httpNodeSetting`（该文件同时钉了本判据的
// 反面：http 面若走 settingOnError 就是轴错）。其余操作符（`foreign code` / 插件 / llm）
// 都只有 `settingOnError.timeoutMs`。
//
// **这是载体选择的镜像，不是第二份定义**：两者的唯一权威 = 后端的目标剖面
// （`internal/backend/cozebiz/http_setting.go` 的表 + `errorPolicySetting` 的调用点）。
// 契约测试 `internal/compiler/cozebiz_timeout_axis_contract_test.go` 用**两态对照**
// （http 不报 / foreign code 报）把两侧钉在一起：载体剖面若改了而这里没改，该测试红。
func hasPerAttemptTimeoutCarrier(op ast.Expr) bool {
	call, ok := op.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Func().(*ast.IdentExpr)
	return ok && id.Name() == "http"
}

// timeoutAxisChecker 找**同时**写了 `retry(max: N≥1)` 与 `timeout(D)`、
// 且超时落在「总预算」载体上的策略表达式（= 轴被换的**唯一**可判形态）。
type timeoutAxisChecker struct{ pass *TargetValidationPass }

func (v timeoutAxisChecker) Visit(n ast.Node) ast.Visitor {
	pe, ok := n.(*ast.OperationPolicyExpr)
	if !ok {
		return v
	}
	if hasPerAttemptTimeoutCarrier(pe.Operation()) {
		return v
	}
	retry, timeout := 0, 0
	for _, pol := range pe.Policies() {
		switch p := pol.(type) {
		case *ast.RetryPolicy:
			retry = p.MaxRetries()
		case *ast.TimeoutPolicy:
			timeout = p.TimeoutMs()
		}
	}
	// 判据口径：`retryTimes ≥ 1` **且** `timeout > 0` 才是轴被换。
	// 只写超时（无重试）⇒ 只有一次尝试，总预算与每次尝试**同值** ⇒ 不是降级（不报）。
	if retry < 1 || timeout <= 0 {
		return v
	}
	v.pass.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning,
		fmt.Sprintf("target carries this timeout as a shared budget across attempts, not per attempt: `with retry(max: %d), timeout(%dms)` lands on settingOnError.timeoutMs, which the platform applies once for ALL retry attempts (each attempt stays bounded by %dms, but later retries may be starved of budget); the source is not rejected",
			retry, timeout, timeout), pe.Span())
	return v
}

// reportTimeoutAxisDegradations 超时轴的**载体轴**与语言轴不一致 ⇒ 软降级必须报账。
//
// 为什么不静默：作者写的 `timeout(D)` 在语言里是**每次尝试**；落到总预算载体后，
// 「最多 N 次重试」这个承诺可能不成立（前面的尝试把预算吃光）⇒ 产物行为与源码语义
// 有可观察差异，必须说出来（`失配 ≠ 静默丢弃`）。
//
// 为什么不拒收：源码合法、语言裁定未变；平台只是**装不下这个轴**。按「平台无法表达
// ⇒ 采用平台默认值（软降级）」处置，与 file 三轴 / 枚举值集 / 显式 null 同一条纪律。
func (p *TargetValidationPass) reportTimeoutAxisDegradations(program *ast.Program) {
	if nonHTTPTimeoutAxisOf(p.ctx.Profile) != timeoutAxisSharedBudget {
		return
	}
	ast.Walk(timeoutAxisChecker{pass: p}, program)
}

// ── 重试间隔轴（F3 值轴第三半的第四行）的载体表 —— **一处定义** ──
//
// 语言：`with retry(max: N, interval: D)` 的 D 是**两次尝试之间**的间隔，省略即 1s
// （正本 = `docs/five-step/spec/F3-error-policy-value-axes.md` §①/§②）。
//
// 平台承载面（一手证据；与 timeoutCarrierAxis 同一条纪律：载体表按**实测的渲染器字段**
// 写，不按愿望）：
//
//   - **dify**：`retry_interval`（毫秒，`pkg/backend/dify/render_nodes.go`）⇒ 承载。
//   - **runtime 执行目标**（`coze-runtime-1.0`）：`internal/runtime/nodes/http.go` /
//     `llm.go` 真的按 `RetryIntervalMS` sleep ⇒ 语义被消费 ⇒ 承载。
//   - **coze-biz / coze-studio**：**无载体** —— 平台的 http 重试循环里没有 sleep
//     （`oss:backend/domain/workflow/internal/nodes/httprequester/http_requester.go:428` 的
//     `for i := 0; i <= retryTimes; i++` 循环体无 sleep）；其错误策略载体只有
//     `settingOnError{retryTimes, timeoutMs}` 与 http 的 `setting{retryTimes, timeout}`
//     （表 = `internal/backend/cozebiz/http_setting.go`）⇒ 作者写的间隔整个不见。
//   - **表外目标 ⇒ 不发结论**（与超时轴同一条有意口径；理由与登记 =
//     `docs/five-step/residuals.yaml#R-89`）：说「这个目标丢了间隔」= 捏造一个我们
//     没验过的平台事实 ⇒ 谎报比沉默更坏。
type retryIntervalCarrier int

const (
	// retryIntervalUnknown 该目标的间隔载体**无证据** ⇒ 不发结论。
	retryIntervalUnknown retryIntervalCarrier = iota
	// retryIntervalCarried 目标真的消费该轴 ⇒ 不报。
	retryIntervalCarried
	// retryIntervalAbsent 目标**没有**间隔载体（立即重试）⇒ 报账。
	retryIntervalAbsent
)

// retryIntervalCarrierOf 目标 → 重试间隔的载体（一处定义；表见上方注释）。
func retryIntervalCarrierOf(profile *TargetProfile) retryIntervalCarrier {
	switch {
	case isDifyProfile(profile):
		return retryIntervalCarried
	case isCozeBizProfile(profile), isCozeStudioProfile(profile):
		return retryIntervalAbsent
	}
	if profile != nil && strings.Contains(profile.Name, "runtime") {
		return retryIntervalCarried
	}
	return retryIntervalUnknown
}

// retryIntervalChecker 找**作者显式写了** `interval:` 的策略表达式（= 无载体目标上会
// 丢的那个唯一形态）。
//
// 判据口径（两半，缺一即噪音）：
//   - `IntervalExplicit()`：`interval` 在 AST 上**恒有值**（省略即默认 1000ms）⇒ 按值
//     报账会落在**每一个** `with retry(...)` 上（含 raising 产出的、从未写间隔的那些）
//     = 报账退化成噪音（本仓措辞纪律）。升读端**从不发射** `interval:`
//     （`platforms/cozebiz/decompile/render_ncoda.go::renderOperationPolicySuffix` 只发
//     `retry(max: N)`）⇒ 这条账只会落在**作者手写**的源上。
//   - `MaxRetries() ≥ 1`：写了间隔但 `max: 0` ⇒ 根本不重试 ⇒ 间隔无意义（同超时轴的
//     「一次尝试两轴同值不报」）。
type retryIntervalChecker struct{ pass *TargetValidationPass }

func (v retryIntervalChecker) Visit(n ast.Node) ast.Visitor {
	pe, ok := n.(*ast.OperationPolicyExpr)
	if !ok {
		return v
	}
	for _, pol := range pe.Policies() {
		rp, ok := pol.(*ast.RetryPolicy)
		if !ok || !rp.IntervalExplicit() || rp.MaxRetries() < 1 {
			continue
		}
		v.pass.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning,
			fmt.Sprintf("target has no carrier for the retry interval axis: `with retry(max: %d, interval: %dms)` — the platform retries immediately (its retry loop has no sleep) and its error-policy carrier holds only retryTimes/timeout, so the interval is dropped (retries behave as back-to-back); the source is not rejected",
				rp.MaxRetries(), rp.IntervalMs()), pe.Span())
	}
	return v
}

// reportRetryIntervalAxisDegradations 重试间隔轴在该目标上**无载体** ⇒ 软降级必须报账。
//
// 为什么不静默：作者写的 `interval: 5s` 在 coze 家族上与 `interval: 1s`、乃至省略间隔
// **逐字节同产物且零诊断**（2026-09-25 一手探针，`rulings/2026-09-25-ruling-sheet-r13.md`；
// 2026-09-30 复核三份源同 sha `26dd8725…`）⇒ 产物行为与源码语义有可观察差异
// （「两次尝试之间等 D」没有发生），必须说出来（`失配 ≠ 静默丢弃`）。
//
// 为什么不拒收：源码合法、语言裁定未变；平台只是**装不下这个轴** ⇒ 按「平台无法表达
// ⇒ 采用平台默认值（软降级）+ 报账」，与 file 三轴 / 枚举值集 / 超时轴同一条纪律。
func (p *TargetValidationPass) reportRetryIntervalAxisDegradations(program *ast.Program) {
	if retryIntervalCarrierOf(p.ctx.Profile) != retryIntervalAbsent {
		return
	}
	ast.Walk(retryIntervalChecker{pass: p}, program)
}

// parallelForErrorModeCarried 目标**是否承载 `parallel for` 的 `on_error` 轴** —— 一处定义。
//
// 承载：
//   - **dify**：`error_handle_mode`（`pkg/backend/dify/nodes.go:553/661`，
//     `serialize.go` 非默认值才写）；三值 = `terminated` / `continue-on-error` /
//     `remove-abnormal-output`（`pkg/ir/on_error_mode.go` 的落点）。
//   - **runtime 执行目标**（`coze-runtime-1.0`）：`internal/runtime/spec/configs.go`
//     把该轴送进执行器、`internal/runtime/compile/loop.go` 按它决定失败迭代的处置
//     ⇒ 语义真被消费。
//
// 不承载（**一手证据**，2026-09-29）：
//   - **coze-biz / coze-studio**：批处理容器的参数只有 `batchSize` / `concurrentSize`
//     （+ `node_inputs` / `node_outputs`）—— 语料 672 源里 **254 个 batch 节点 0 例**
//     带任何错误模式键；coze-studio OSS 同形（`oss:backend/domain/workflow/entity/vo/canvas.go:281-284`
//     的 `Batch` 只有那两个字段），且任一迭代失败即 `cancelFn(err)` **整批中止**
//     （`oss:backend/domain/workflow/internal/nodes/batch/batch.go` 的 `ithTask`）
//     ⇒ 平台**行为恒等于** `terminate`。
//   - **表外目标 = 不承载**（与 `enumValueSetCapacityOf` 同一条保守默认：宁可多报一条
//     可见的降级警告，也不要静默丢掉作者写的轴）。
//
// 为什么这是「降级报账」而不是「实现缺口」：语言三值是**作者语义**（`keep_null` =
// 失败迭代留空值继续、`remove_failed` = 压缩掉失败迭代），平台装不下 ⇒ 按
// AGENTS.md「ncoda 覆盖承诺」采用平台默认值（软降级）**并报账**（`失配 ≠ 静默丢弃`）。
// 不升级成 error：源码合法、语言裁定未变，升级会拒收合法源码。
func parallelForErrorModeCarried(profile *TargetProfile) bool {
	if profile == nil {
		return true // 缺省 profile = dify（DefaultTargetProfile）
	}
	if isDifyProfile(profile) {
		return true
	}
	return strings.Contains(profile.Name, "runtime")
}

// parallelForErrorModeChecker 找**写了非平台默认** `on_error` 的 `parallel for`。
//
// 两种拼写都要认（同一语言构造的两条语法通路，IR 侧对应 `markParallelLoop` 的两个
// 调用点）：语句形 `*ast.ForStmt`（`IsParallel()`）与值形 `*ast.ParallelForExpr`。
// `terminate` = 平台实际行为（见 `parallelForErrorModeCarried`）⇒ **不报**：报了等于
// 说「没丢东西的东西丢了」，会训练人忽略警告；空串（未走并发配置的普通 for）同样不报。
type parallelForErrorModeChecker struct{ pass *TargetValidationPass }

func (v parallelForErrorModeChecker) Visit(n ast.Node) ast.Visitor {
	switch s := n.(type) {
	case *ast.ForStmt:
		if s.IsParallel() {
			v.report(s.OnError(), s.Span())
		}
	case *ast.ParallelForExpr:
		v.report(s.OnError(), s.Span())
	}
	return v
}

func (v parallelForErrorModeChecker) report(mode string, span ast.Span) {
	if mode == "" || mode == "terminate" {
		return
	}
	v.pass.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning,
		fmt.Sprintf("target does not carry the `parallel for` on_error axis: the batch container exposes only batchSize/concurrentSize and aborts the whole run when an iteration fails, so `on_error: %s` behaves as `terminate` (failed iterations neither survive as empty slots nor are dropped); the source is not rejected", mode), span)
}

// reportParallelForErrorModeDegradations `parallel for` 的 `on_error` 轴在该目标上
// 无载体 ⇒ 软降级必须报账（2026-09-29 一手发现：`examples/nodecoda/parallel_for_keep_null.ncoda`
// 在 coze-biz 上 `SUCCEEDED` 且**零诊断**，产物与 `on_error: terminate` **逐字节相同**）。
func (p *TargetValidationPass) reportParallelForErrorModeDegradations(program *ast.Program) {
	if parallelForErrorModeCarried(p.ctx.Profile) {
		return
	}
	ast.Walk(parallelForErrorModeChecker{pass: p}, program)
}

// isCozeBizProfile 目标是否 coze-biz 家族（唯一的**单值 kind** 承载面）。
func isCozeBizProfile(profile *TargetProfile) bool {
	if profile == nil {
		return false
	}
	return strings.HasPrefix(profile.Name, "coze-biz")
}

// enumValueSetCapacityOf 目标**是否承载枚举的值集**（一处定义）。
//
// 依据 = 渲染器**实际发出的字段**（实测，不是愿望）：
//
//   - dify：**LLM payload 面**承载 —— `pkg/backend/dify/render_nodes.go:134`
//     把 `llm.Enum` 渲成 `{"type": "string", "enum": [...]}`，反编译端
//     `platforms/dify/decompile/convert.go:911` 读回 ⇒ 值集在产物里存在。
//   - coze-biz：**无承载面** —— `internal/backend/cozebiz/render_node.go::llmSchemaDecl`
//     的 `llm.KindEnum` ⇒ `"string"`（该处注释已自认 soft degradation）；非 payload
//     面更早在**共享**中性 IR 塌成 `TObject{value: string}`（`pkg/ir/build.go:1230`）。
//   - coze-studio：**无承载面** —— `internal/backend/coze/render.go::llmSchemaTypeInfo`
//     的 `llm.KindEnum` ⇒ `DataTypeString`。
//   - **表外 / 未知目标 = 不承载**（保守 = 不发明能力）：宁可多报一条**可见**的降级
//     警告，也不要静默丢掉作者声明的值域约束。
//
// 已知边界（登记，不掩盖）：dify 的**非 LLM** 面同样把值集塌成 string（中性 IR 的
// 塌缩与目标无关）——本判据只对「连 payload 面都不承载」的目标报账；dify 面属搁置面
// （AGENTS.md「开发主线与平台优先级」），不在主线动。
func enumValueSetCapacityOf(profile *TargetProfile) bool {
	return isDifyProfile(profile)
}

// reportEnumValueSetDegradations 枚举的**值集**在该目标上承载不了 ⇒ 软降级必须报账
// （R-57，2026-09-25 用户已给默认）。
//
// 报什么：`enum E { a, b }` 声明的值域 `{a, b}` 在该目标的产物里**不存在**（类型塌成
// string）。**不拒收**（warning，不是 error）——升级成 error 会拒收合法源码，方向与
// 「平台无法表达 ⇒ 采用平台默认值（软降级）」相反（与 file 三轴报账同一条纪律）。
//
// 报在哪里：**声明位**，一条枚举一条警告。理由 = 逐使用点报只会重复同一句事实
// （噪音），而声明位是值域约束的**书写处**。措辞用「not carried」而不是「dropped」：
// 声明了但没被引用的枚举什么也没丢，措辞不得撒谎。
//
// **豁免：`ask` 的 action 枚举**（一手 2026-09-26）。原措辞称「LLM payload 面与值位置
// 面都塌成 string ⇒ 任何使用面都不承载值集」—— 这句话对 `ask<Form, E>` 的 `E` 是**假**的：
// 每个目标的 QA 载体都把 action 成员**逐条**发进选项表（`docs/ASK-SYNTAX.md` §4：
// dify `human_input.user_actions[]` / coze-studio `QuestionAnswerNode.options` /
// coze-biz `question` 的选项表），而编译器对「枚举成员 ↔ action 臂」强制穷尽
// （缺臂 = `TYPE_MISMATCH` "Human Input is missing action branches"）⇒ 每个成员都必然
// 出现在选项表里，值集可精确还原。一手：`examples/nodecoda/61_request_input.ncoda`
// 往返 r1 还原 `enum RT_action_approve_reject { approve, reject };`（枚举**名** = 形状锚，
// B-H1 收口 2026-10-16 前是携带平台 node id 的 `QaAction<id>`）。
// ⇒ 这类声明**不报账**（多报 = 噪音，会训练人忽略警告；与「假红比不判更坏」同一纪律）。
// 承载不了的**其余**枚举（只作字段类型 / 只作值位置）照旧逐条报。
// 表外目标 = 不承载（与 `enumValueSetCapacityOf` 同一条**保守默认**：不发明能力）。
// explicitNullCarried 该目标是否**不报**显式 null 初值的降级账。
//
// 注意函数名的口径比证据强，**证据等级写在下面**（2026-09-25 补，避免把「未验」读成「已验」）：
//
//	· coze-studio / coze-biz = **已验不承载**：`AGENTS.md`「null / undefined 语义契约」把
//	  coze 平台定义为不区分 nil/null/zero，coze-biz 更被显式定义为**不支持 nil/null** 的平台
//	  （2026-09-08 A 级定义）⇒ 报账。
//	· dify = **格式层已验能承载、绑定层不可观测**：dify 的序列化面能写 null
//	  （`pkg/backend/dify/yaml.go` 的 `yamlNullRe` / `writePlain("null")`、
//	  `render_nodes.go` 的 `{"type":"null"}` anyOf 一手证据）⇒ 不出降级账；
//	  但「dify 把 `let x = null;` 渲成什么样」**今天观测不到** —— 读 null 型值在三目标都
//	  fail loud（`residuals.yaml#R-65`）⇒ 无消费点可看产物。**不声称**它承载，只是不报账
//	  （不撒谎的底线：没有证据就不发结论，也不发反向结论）。
func explicitNullCarried(profile *TargetProfile) bool { return isDifyProfile(profile) }

// nullBindingChecker 找**声明位**的显式 null 初值（`let x = null;`）。
type nullBindingChecker struct{ pass *TargetValidationPass }

func (v nullBindingChecker) Visit(n ast.Node) ast.Visitor {
	if d, ok := n.(*ast.VarDeclStmt); ok {
		if _, isNull := d.Init().(*ast.NullLit); isNull {
			v.pass.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning,
				fmt.Sprintf("target has no null carrier: the explicit null initializer of %s is not carried by this target's product (a nil-less platform has no null value; the read side keeps its own fail-loud) — the source is not rejected",
					d.Name()), d.Span())
		}
	}
	return v
}

// reportNullBindingDegradations 显式 `null` 初值在该目标上**没有承载面** ⇒ 软降级必须报账。
//
// 用户 2026-09-25 裁决（甲）：显式 null **不得拒收** —— 它只在作者显式书写处出现，
// 是合法 ncoda；「平台无法表达 ⇒ 采用平台默认值（软降级）」（与 file 三轴 / enum 值集
// 报账同一条纪律：升级成 error 会拒收合法源码，方向相反）。
//
// 处置 = 与 **hoisted 路径同一把尺子**（`pkg/ir/build_call.go`：null 初值
// **不物化 producer**，变量保持无绑定；消费点由平台 FillZero 兜零值）。
// 措辞不得撒谎：`let x = null;` 的推导类型是 `null`，**没有可物化的零值** ⇒ 这里是
// 「该值在平台上不存在」，不是「被替换成某个零值」。
//
// 报账位置 = **声明位**（一条声明一条警告），理由同 `reportEnumValueSetDegradations`。
func (p *TargetValidationPass) reportNullBindingDegradations(program *ast.Program) {
	if explicitNullCarried(p.ctx.Profile) {
		return
	}
	ast.Walk(nullBindingChecker{pass: p}, program)
}

func (p *TargetValidationPass) reportEnumValueSetDegradations(program *ast.Program) {
	if enumValueSetCapacityOf(p.ctx.Profile) {
		return
	}
	// 值集被 QA 选项表承载的那一类（`ask<Form, E>` 的 `E`）不报 —— 判据与理由见函数
	// 头注的「豁免」段。表外目标不豁免（保守默认 = 不发明能力）。
	var qaCarried map[string]bool
	if qaActionCarriesEnumValueSet(p.ctx.Profile) {
		qaCarried = askActionEnumNames(program)
	}
	for _, decl := range program.EnumDecls() {
		if qaCarried[decl.Name()] {
			continue
		}
		members := make([]string, 0, len(decl.Members()))
		for _, m := range decl.Members() {
			members = append(members, m.Name())
		}
		p.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning,
			fmt.Sprintf("target has no enum carrier: the value set of enum %s {%s} is not carried by this target's product (the type collapses to string); the value-domain constraint is lost, the source is not rejected",
				decl.Name(), strings.Join(members, ", ")), decl.Span())
	}
}

// qaActionCarriesEnumValueSet 该目标是否把 `ask` 的 action 枚举的**值集**发进产物
// （= 存在 QA 选项表载体）。已知目标逐条列举（`docs/ASK-SYNTAX.md` §4 的落地列），
// 表外 = false（保守 = 不发明能力）。
func qaActionCarriesEnumValueSet(profile *TargetProfile) bool {
	switch {
	case profile == nil:
		return false
	case isDifyProfile(profile) || isCozeStudioProfile(profile) || isCozeBizProfile(profile):
		return true
	default:
		return false
	}
}

// askActionEnumNames 收集全部 `ask<Form, Action>` 的 **Action 具名类型**（值集被 QA
// 选项表承载的枚举名集合）。非具名（`ask<F, any>` 等）不收集。
func askActionEnumNames(program *ast.Program) map[string]bool {
	names := map[string]bool{}
	ast.Walk(askActionEnumCollector{names: names}, program)
	return names
}

type askActionEnumCollector struct{ names map[string]bool }

func (c askActionEnumCollector) Visit(n ast.Node) ast.Visitor {
	if req, ok := n.(*ast.RequestInputStmt); ok {
		if named, ok := req.ActionType().(*ast.NamedTypeRef); ok {
			c.names[named.Name()] = true
		}
	}
	return c
}

// typeRefChecker 非 dify 目标上的**语言级类型引用检查器**：把每个 `file<…>` 声明位
// 送进 `validateTypeRef`（与 dify 路径**同一个入口** ⇒ 合法性 + 承载报账只有一份实现）。
// 非 dify 路径不做 dify 的形状约束，所以不能直接复用 pass 自身的 `Visit` 分发。
type typeRefChecker struct {
	pass *TargetValidationPass
}

func (v typeRefChecker) Visit(n ast.Node) ast.Visitor {
	if ref, ok := n.(*ast.FileTypeRef); ok {
		v.pass.validateTypeRef(ref, ref.Span())
	}
	return v
}

// languageContractChecker 在非 dify 目标上发布 IR 消费的**语言级契约**：
// `ValidatedExtractions`（extract<T>）与 `ValidatedAgents`（agent）。
// 它只认这两个操作性分支，且调用的校验器与 dify 路径**同一个函数** ——
// 判据一次、发布一处，避免「同一份契约在非 dify 上静默为空」这类半接线。
type languageContractChecker struct {
	pass *TargetValidationPass
}

func (v languageContractChecker) Visit(n ast.Node) ast.Visitor {
	// `ask` 的语言级契约与 extract/agent 同一条纪律：非 dify 目标也必须发布
	// （IR 无条件读它）。校验函数与 dify 路径同一个（`validateHumanInputStmt`），
	// 子节点仍由本访问器继续下潜（不借用 pass 的 dify 形状规则）。
	if req, ok := n.(*ast.RequestInputStmt); ok {
		v.pass.validateHumanInputStmt(req, v)
		return nil
	}
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return v
	}
	operation := operationForCall(call, v.pass.ctx)
	if operation == nil {
		return v
	}
	switch {
	case operation.ResultKind == "schema-extraction":
		v.pass.validateExtractionCall(call, v.pass.ctx)
	case operation.Name == "agent":
		v.pass.validateAgentCall(call, v.pass.ctx)
	}
	return v
}

// reportFileContractDegradations 报告该目标承载不了的约束轴（**软降级必须报账**）。
//
// 方向纪律（2026-09-21 用户裁定）：**只许放宽，不许收紧**。多值的并集在单值目标上
// 落成「无约束」（`yamlFileKindName` 的放宽），不是「取首」—— 取首会拒收作者允许的
// 输入，方向与「平台无法表达 ⇒ 采用平台默认值」相反。
func (p *TargetValidationPass) reportFileContractDegradations(c ast.FileTypeConstraints, span ast.Span, capacity fileContractCapacity) {
	// 判「多值」用**去重后的集合**，不是书写的元素个数：`types` 轴的语义是集合，
	// `file<image, image>` 与 `file<image>` 是同一个约束 ⇒ 单值承载面装得下它，
	// 不构成降级。这条与渲染器的规范形（`canonicalFileKindSet` = 去重 + 排序）是
	// **同一条规则**：两边都必须是集合语义，否则「报账说降级了、产物却没降级」
	// （实测过：`file<image, image>` 曾被报成放宽）。
	kinds := dedupeSorted(c.Types)
	switch {
	case len(kinds) > 0 && !capacity.kinds:
		p.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning,
			fmt.Sprintf("target has no file-kind field: the kind constraint [%s] is dropped",
				strings.Join(kinds, ", ")), span)
	case len(kinds) > 1 && !capacity.kindsAreSets:
		p.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning,
			fmt.Sprintf("target carries a single file kind: the set [%s] is widened to the unconstrained file kind (narrowing to one member would reject files the author allows)",
				strings.Join(kinds, ", ")), span)
	}
	if len(c.Extensions) > 0 && !capacity.exts {
		p.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning,
			fmt.Sprintf("target has no file-extension field: the extension constraint [%s] is dropped",
				strings.Join(dedupeSorted(c.Extensions), ", ")), span)
	}
	if len(c.UploadMethods) > 0 && !capacity.uploads {
		p.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning,
			fmt.Sprintf("target has no upload-method field: the upload-method constraint [%s] is dropped",
				strings.Join(dedupeSorted(c.UploadMethods), ", ")), span)
	}
}

// Visit mirrors the visit_* dispatch (target_validation.py:254-1322).
func (p *TargetValidationPass) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.VarDeclStmt:
		return p.visitVarDeclStmt(node)
	case *ast.ForStmt:
		return p.visitForStmt(node)
	case *ast.CallExpr:
		return p.visitCallExpr(node)
	case *ast.RequestInputStmt:
		return p.visitRequestInputStmt(node)
	case *ast.IfStmt:
		return p.visitIfStmt(node)
	case *ast.WhileStmt:
		return p.visitWhileStmt(node)
	case *ast.ReturnStmt:
		return p.visitReturnStmt(node)
	case *ast.YieldStmt:
		return p.visitYieldStmt(node)
	case *ast.OutputStmt:
		return p.visitOutputStmt(node)
	case *ast.FieldAccessExpr:
		return p.visitFieldAccessExpr(node)
	case *ast.AssignStmt:
		return p.visitAssignStmt(node)
	}
	return p
}

// validateStartInputShapes mirrors _validate_start_input_shapes (:170-196).
func (p *TargetValidationPass) validateStartInputShapes(program *ast.Program, ctx *Context) {
	if program.Entry() == nil {
		return
	}
	for _, param := range program.Entry().Params() {
		descriptor := types.UnwrapOptional(descriptorForRef(param.TypeRef(), ctx))
		switch descriptor.Kind {
		case types.Array:
			if descriptor.Elem == nil {
				p.rejectAt("array Start input has no element type", param.Span())
			}
		case types.Map:
			p.rejectAt("map Start input is not representable by the Dify target", param.Span())
		case types.Record:
			p.rejectAt("record Start input is not representable by the Dify target", param.Span())
		}
	}
}

// validateDeclaredFileTypes mirrors _validate_declared_file_types (:197-213).
func (p *TargetValidationPass) validateDeclaredFileTypes(program *ast.Program) {
	type refAndSpan struct {
		ref  ast.TypeRef
		span ast.Span
	}
	var declarations []refAndSpan
	for _, typeDecl := range program.TypeDecls() {
		for _, field := range typeDecl.Fields() {
			declarations = append(declarations, refAndSpan{field.TypeRef(), field.Span()})
		}
	}
	for _, decl := range program.ConversationDecls() {
		declarations = append(declarations, refAndSpan{decl.TypeRef(), decl.Span()})
	}
	for _, output := range program.Outputs() {
		declarations = append(declarations, refAndSpan{output.TypeRef(), output.Span()})
	}
	var functions []*ast.FunctionDef
	functions = append(functions, program.Functions()...)
	for _, codeFn := range program.CodeFunctions() {
		functions = append(functions, &codeFn.FunctionDef)
	}
	if program.Entry() != nil {
		functions = append(functions, program.Entry())
	}
	for _, fn := range functions {
		for _, param := range fn.Params() {
			declarations = append(declarations, refAndSpan{param.TypeRef(), param.Span()})
		}
		if fn.ReturnType() != nil {
			declarations = append(declarations, refAndSpan{fn.ReturnType(), fn.Span()})
		}
	}
	for _, d := range declarations {
		p.validateTypeRef(d.ref, d.span)
	}
}

// validateTypeRef mirrors _validate_type_ref (:215-224).
func (p *TargetValidationPass) validateTypeRef(ref ast.TypeRef, span ast.Span) {
	switch r := ref.(type) {
	case *ast.FileTypeRef:
		// 两件事都在这一个入口（每个目标的每个 `file<…>` 声明位都经过它）：
		// ① 合法性 —— 语言级，目标无关；② 承载报账 —— 目标级，语义 = 该目标
		// 物理装不下的轴必须说出来（`p.fileCapacity` 由 Run 解析，一处定义）。
		p.validateFileConstraints(r.Constraints(), span)
		p.reportFileContractDegradations(r.Constraints(), span, p.fileCapacity)
	case *ast.ArrayTypeRef:
		p.validateTypeRef(r.Element(), span)
	case *ast.MapTypeRef:
		p.validateTypeRef(r.Key(), span)
		p.validateTypeRef(r.Value(), span)
	case *ast.OptionalTypeRef:
		p.validateTypeRef(r.Value(), span)
	}
}

// validateFileConstraints mirrors _validate_file_constraints (:226-245).
// The Python side builds sets (set(constraints.types) - SUPPORTED_FILE_TYPES
// and friends), which dedupes repeated values before the sorted join; the Go
// accumulators dedupe the same way (R1 I1: file<document, bogus, bogus> must
// report "bogus", not "bogus, bogus").
func (p *TargetValidationPass) validateFileConstraints(constraints ast.FileTypeConstraints, span ast.Span) {
	unknownTypes := dedupeStrings(constraints.Types, func(t string) bool { return !supportedFileTypes[t] })
	unknownUploads := dedupeStrings(constraints.UploadMethods, func(u string) bool { return !supportedUploadMethods[u] })
	badExtensions := dedupeStrings(constraints.Extensions, func(ext string) bool {
		return ext != strings.TrimSpace(ext) || !strings.HasPrefix(ext, ".") || len(ext) == 1
	})
	sort.Strings(unknownTypes)
	sort.Strings(unknownUploads)
	sort.Strings(badExtensions)
	if len(unknownTypes) > 0 {
		p.rejectAt("file type constraint is unsupported: "+strings.Join(unknownTypes, ", "), span)
	}
	if len(unknownUploads) > 0 {
		p.rejectAt("file upload method constraint is unsupported: "+strings.Join(unknownUploads, ", "), span)
	}
	if len(badExtensions) > 0 {
		p.rejectAt("file extension constraint is unsupported: "+strings.Join(badExtensions, ", "), span)
	}
}

// visitVarDeclStmt mirrors visit_var_decl_stmt (:254-260). The Python base
// visitor walks type_ref then init; the manual walk keeps that coverage.
func (p *TargetValidationPass) visitVarDeclStmt(node *ast.VarDeclStmt) ast.Visitor {
	if node.TypeRef() != nil {
		p.validateTypeRef(node.TypeRef(), node.Span())
	}
	if node.Init() != nil {
		p.validateGeneratedCodeExpr(node.Init(), p.ctx)
	}
	ast.Walk(p, node.TypeRef())
	ast.Walk(p, node.Init())
	return nil
}

// visitForStmt mirrors visit_for_stmt (:261-264). The base visitor walks
// var_type, iterable and body; the manual walk keeps that coverage.
func (p *TargetValidationPass) visitForStmt(node *ast.ForStmt) ast.Visitor {
	if node.VarType() != nil {
		p.validateTypeRef(node.VarType(), node.Span())
	}
	ast.Walk(p, node.VarType())
	ast.Walk(p, node.Iterable())
	ast.Walk(p, node.Body())
	return nil
}

// visitCallExpr mirrors visit_call_expr (:266-293) with the GATED branches
// elided (see the header note). The base visitor walks func then args; the
// manual walk keeps that coverage.
func (p *TargetValidationPass) visitCallExpr(node *ast.CallExpr) ast.Visitor {
	operation := operationForCall(node, p.ctx)
	// (:267-270): the agent branch validates the closed Agent subset, then
	// super().visit_call_expr continues the walk over func and args — the
	// manual walk below keeps that coverage.
	if operation != nil && operation.Name == "agent" {
		p.validateAgentCall(node, p.ctx)
		ast.Walk(p, node.Func())
		for _, arg := range node.Args() {
			ast.Walk(p, arg)
		}
		return nil
	}
	// (:275-278): the classification branch validates the closed
	// classification subset, then super().visit_call_expr continues the
	// walk over func and args — the manual walk below keeps that coverage.
	if operation != nil && operation.ResultKind == "classification" {
		p.validateClassificationCall(node, p.ctx)
		ast.Walk(p, node.Func())
		for _, arg := range node.Args() {
			ast.Walk(p, arg)
		}
		return nil
	}
	// (:271-274): the schema-extraction branch validates the closed
	// extract subset via _validate_extraction_call, then the manual walk
	// below keeps the super().visit_call_expr coverage of func and args.
	if operation != nil && operation.ResultKind == "schema-extraction" {
		p.validateExtractionCall(node, p.ctx)
		ast.Walk(p, node.Func())
		for _, arg := range node.Args() {
			ast.Walk(p, arg)
		}
		return nil
	}
	if operation != nil && operation.Name == "extract_text" && len(node.Args()) > 0 {
		p.validateExtractTextOperand(node.Args()[0], p.ctx)
	} else if operation != nil && operation.Name == "llm" && len(node.Args()) > 1 {
		if config, ok := node.Args()[1].(*ast.MapLit); ok {
			if vision := mapValue(config, "vision"); vision != nil {
				p.validateVisionOperand(vision, p.ctx)
			}
		}
	} else if !isNativeMaterializationCall(node, p.ctx, p.declInits) {
		p.validateGeneratedCodeExpr(node, p.ctx)
	}
	ast.Walk(p, node.Func())
	for _, arg := range node.Args() {
		ast.Walk(p, arg)
	}
	return nil
}

// validateAgentCall mirrors _validate_agent_call (:295-357): validate the
// closed Agent subset before any target lowering can occur, then publish
// the AgentCallContract onto ctx.ValidatedAgents when no new diagnostic
// appeared. The check order is byte-exact with the reference (multi-
// diagnostic cases emit in the same order — oracle /tmp/oracle25d_tie.py).
// Spans: the reference uses the profile/instruction expression span when
// present and the config map span otherwise; Go mirrors that.
func (p *TargetValidationPass) validateAgentCall(node *ast.CallExpr, ctx *Context) {
	nodeID, hasID := snapshotID(ctx, node)
	if hasID {
		delete(ctx.ValidatedAgents, nodeID)
	}
	before := len(ctx.Diagnostics)
	if len(node.Args()) != 3 {
		p.rejectAt("Agent requires exactly three value arguments", node.Span())
		return
	}

	modelExpr, queryExpr, configExpr := node.Args()[0], node.Args()[1], node.Args()[2]
	model := p.validateAgentModel(modelExpr)
	if model == "" {
		return
	}
	configMap, ok := configExpr.(*ast.MapLit)
	if !ok {
		p.rejectAt("Agent config must be a static map literal", configExpr.Span())
		return
	}
	values := p.staticTargetMap(configMap, "Agent", "config")
	required := map[string]bool{"profile": true, "instruction": true, "tools": true}
	for _, key := range sortedMissingKeys(values, required) {
		p.rejectAt(fmt.Sprintf("Agent config is missing required key '%s'", key), configMap.Span())
	}
	for _, key := range sortedExtraKeys(values, required) {
		p.rejectAt(
			fmt.Sprintf("Agent config has extra key '%s'; supported keys: profile, instruction, tools", key),
			configMap.Span(),
		)
	}

	// Profile identity (:321-336): a static string alias resolves the
	// installed profile; the reference renders the missing/non-string alias
	// with Python repr semantics — 'alias' when resolved, None otherwise
	// (oracle: "Agent profile None is not installed in target ...").
	profileExpr := values["profile"]
	profileAlias := ""
	aliasResolved := false
	if sl, ok := profileExpr.(*ast.StringLit); ok {
		profileAlias = sl.Value()
		aliasResolved = true
	}
	if profileExpr != nil && !aliasResolved {
		p.rejectAt("Agent profile must be a static string", profileExpr.Span())
	}
	var profile *binding.AgentTargetProfile
	if aliasResolved {
		profile = ctx.Profile.AgentProfiles[profileAlias]
	}
	if profile == nil {
		span := configMap.Span()
		if profileExpr != nil {
			span = profileExpr.Span()
		}
		p.rejectAt(
			fmt.Sprintf("Agent profile %s is not installed in target '%s'", renderAgentAlias(profileAlias, aliasResolved), ctx.Profile.Name),
			span,
		)
	} else if !sameStringSet(profile.ParameterNames(), []string{"model", "query", "instruction", "tools"}) {
		span := configMap.Span()
		if profileExpr != nil {
			span = profileExpr.Span()
		}
		p.rejectAt(
			fmt.Sprintf("Agent profile '%s' has an unsupported parameter contract", profile.Alias),
			span,
		)
	}

	// Instruction (:338-344): must be present, a static string and non-blank
	// (Python not instruction.strip()).
	instructionExpr := values["instruction"]
	instruction := ""
	instructionOK := false
	if sl, ok := instructionExpr.(*ast.StringLit); ok {
		instruction = sl.Value()
		instructionOK = strings.TrimSpace(instruction) != ""
	}
	if instructionExpr == nil || !instructionOK {
		span := configMap.Span()
		if instructionExpr != nil {
			span = instructionExpr.Span()
		}
		p.rejectAt("Agent instruction must be a non-empty static string", span)
	}

	toolsExpr := values["tools"]
	tools := p.validateAgentTools(toolsExpr, profile)
	if len(ctx.Diagnostics) != before || profile == nil || !aliasResolved {
		return
	}
	// The pass-side checks above guarantee NewAgentCallContract cannot fail:
	// aliasResolved proves a non-empty profile_alias, validateAgentModel
	// returns a non-empty model, the instruction guard enforces a
	// non-empty instruction and validateAgentTools rejects duplicates.
	// Python raises ValueError in __post_init__ on a contract violation
	// (a crash face); the Go form skips publication defensively — the
	// branch is unreachable (same convention as the Human Input contract).
	queryID, queryOK := snapshotID(ctx, queryExpr)
	if !queryOK {
		return
	}
	if contract, err := binding.NewAgentCallContract(profileAlias, model, queryID, instruction, tools); err == nil {
		ctx.ValidatedAgents[nodeID] = contract
	}
}

// validateAgentModel mirrors _validate_agent_model (:359-371): the model
// must be a static string with a non-empty 'provider/model' split.
func (p *TargetValidationPass) validateAgentModel(model ast.Expr) string {
	sl, ok := model.(*ast.StringLit)
	if !ok {
		p.rejectAt("Agent model must be a static string", model.Span())
		return ""
	}
	provider, name, hasSep := strings.Cut(sl.Value(), "/")
	if !hasSep || strings.TrimSpace(provider) == "" || strings.TrimSpace(name) == "" {
		p.rejectAt("Agent model must be a static 'provider/model' string with non-empty parts", model.Span())
		return ""
	}
	return sl.Value()
}

// validateAgentTools mirrors _validate_agent_tools (:372-394): the tools
// value must be a static string array without duplicates, and every alias
// must be permitted by the pinned profile (unknown aliases reported sorted,
// Python set arithmetic).
func (p *TargetValidationPass) validateAgentTools(toolsExpr ast.Expr, profile *binding.AgentTargetProfile) []string {
	if toolsExpr == nil {
		return nil
	}
	arr, ok := toolsExpr.(*ast.ArrayLit)
	if !ok {
		p.rejectAt("Agent tools must be a static string array", toolsExpr.Span())
		return nil
	}
	for _, item := range arr.Elements() {
		if _, ok := item.(*ast.StringLit); !ok {
			p.rejectAt("Agent tools must be a static string array", toolsExpr.Span())
			return nil
		}
	}
	tools := make([]string, 0, len(arr.Elements()))
	seen := map[string]bool{}
	for _, item := range arr.Elements() {
		if sl, ok := item.(*ast.StringLit); ok {
			tools = append(tools, sl.Value())
			seen[sl.Value()] = true
		}
	}
	if len(tools) != len(seen) {
		p.rejectAt("Agent tools must not contain duplicates", toolsExpr.Span())
	}
	allowed := map[string]bool{}
	if profile != nil {
		for _, alias := range profile.AllowedTools() {
			allowed[alias] = true
		}
	}
	var unknown []string
	for tool := range seen {
		if !allowed[tool] {
			unknown = append(unknown, tool)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		p.rejectAt("Agent tools are not permitted by the pinned profile: "+strings.Join(unknown, ", "), toolsExpr.Span())
	}
	return tools
}

// ── classification (target_validation.py:715-852) ─────────────────────

// classificationConfig carries the three validated config faces back from
// validateClassificationConfig (the Go analogue of the Python
// (instruction, classes, fallback_member) triple; nil result on rejection).
type classificationConfig struct {
	instruction    string
	classes        []binding.ClassificationClass
	fallbackMember string
}

// validateClassificationCall mirrors _validate_classification_call
// (:715-740): validate the closed classification subset before any target
// lowering can occur, then publish the ValidatedClassificationContract onto
// ctx.ValidatedClassifications when no new diagnostic appeared. The check
// order is byte-exact with the reference (multi-diagnostic cases emit in
// the same order — oracle /tmp/oracle25e_diag.py).
func (p *TargetValidationPass) validateClassificationCall(node *ast.CallExpr, ctx *Context) {
	nodeID, hasID := snapshotID(ctx, node)
	if hasID {
		delete(ctx.ValidatedClassifications, nodeID)
	}
	enumType := exprTypeOf(ctx, node)
	if enumType == nil || enumType.Kind != types.Enum {
		p.rejectAt("Classification requires one resolved nominal enum type", node.Span())
		return
	}
	if len(node.Args()) != 3 {
		p.rejectAt("Classification requires exactly three value arguments", node.Span())
		return
	}

	model := p.validateClassificationModel(node.Args()[0])
	config := p.validateClassificationConfig(node.Args()[2], enumType, ctx)
	if model == "" || config == nil {
		return
	}
	queryID, queryOK := snapshotID(ctx, node.Args()[1])
	if !queryOK {
		// Python publishes the contract with ctx.ast_id(node.args[1])
		// unconditionally (an unbound query arg would crash the reference);
		// the Go form skips publication defensively when the arg has no
		// snapshot id. The branch is unreachable through the pipeline — a
		// validated classify call's query arg always carries a snapshot id
		// — and mirrors the validateAgentCall:508 precedent.
		return
	}
	// The pass-side checks above guarantee NewValidatedClassificationContract
	// cannot fail: the enum_type is a resolved well-formed enum (type-pass
	// published), validateClassificationLabels returns classes covering the
	// enum exactly in declaration order with unique labels, and
	// validateClassificationFallback returns a member of the same enum.
	// Python raises ValueError in __post_init__ on a contract violation (a
	// crash face); the Go form skips publication defensively — the branch
	// is unreachable (same convention as the Agent/Human Input contracts).
	if contract, err := binding.NewValidatedClassificationContract(enumType, model, queryID, config.instruction, config.classes, config.fallbackMember); err == nil {
		ctx.ValidatedClassifications[nodeID] = contract
	}
}

// validateClassificationModel mirrors _validate_classification_model
// (:742-753): the model must be a static string with a non-empty
// 'provider/model' split.
func (p *TargetValidationPass) validateClassificationModel(model ast.Expr) string {
	sl, ok := model.(*ast.StringLit)
	if !ok {
		p.rejectAt("Classification model must be a static string", model.Span())
		return ""
	}
	provider, name, hasSep := strings.Cut(sl.Value(), "/")
	if !hasSep || strings.TrimSpace(provider) == "" || strings.TrimSpace(name) == "" {
		p.rejectAt("Classification model must be a static 'provider/model' string", model.Span())
		return ""
	}
	return sl.Value()
}

// validateClassificationConfig mirrors _validate_classification_config
// (:755-785): the config must be a static map literal carrying
// instruction/labels/fallback; any new diagnostic (or a non-string
// instruction, incomplete labels or missing fallback) rejects the whole
// config. The instruction check only fires when the key is present with a
// non-string value, exactly like the reference.
func (p *TargetValidationPass) validateClassificationConfig(config ast.Expr, enumType *types.Descriptor, ctx *Context) *classificationConfig {
	if _, ok := config.(*ast.MapLit); !ok {
		p.rejectAt("Classification config must be a static map literal", config.Span())
		return nil
	}
	configMap := config.(*ast.MapLit)
	before := len(ctx.Diagnostics)
	values := p.staticClassificationMap(configMap, "config")
	required := map[string]bool{"instruction": true, "labels": true, "fallback": true}
	for _, key := range sortedMissingKeys(values, required) {
		p.rejectAt(fmt.Sprintf("Classification config is missing required key '%s'", key), configMap.Span())
	}
	for _, key := range sortedExtraKeys(values, required) {
		p.rejectAt(fmt.Sprintf("Classification config has extra key '%s'", key), configMap.Span())
	}

	instructionExpr := values["instruction"]
	instruction := ""
	instructionOK := false
	if sl, ok := instructionExpr.(*ast.StringLit); ok {
		instruction = sl.Value()
		instructionOK = true
	}
	if instructionExpr != nil && !instructionOK {
		// The reference distinguishes a missing key (nil expr, no
		// diagnostic here) from a present non-string value (diagnostic at
		// the value span, :770-775). A present empty-string literal is a
		// valid instruction — the check is only about the static-string
		// shape, not its content.
		p.rejectAt("Classification instruction must be a static string", instructionExpr.Span())
	}

	classes := p.validateClassificationLabels(values["labels"], enumType)
	fallbackMember := p.validateClassificationFallback(values["fallback"], enumType, ctx)
	if len(ctx.Diagnostics) != before || !instructionOK || classes == nil || fallbackMember == "" {
		return nil
	}
	return &classificationConfig{
		instruction:    instruction,
		classes:        classes,
		fallbackMember: fallbackMember,
	}
}

// validateClassificationLabels mirrors _validate_classification_labels
// (:787-830): the labels value must be a static map literal whose keys
// cover the enum exactly (missing/extra members reported sorted, Python set
// arithmetic) and whose values are distinct static strings. On success the
// classes are returned in enum declaration order, exactly like the
// reference's final tuple comprehension.
func (p *TargetValidationPass) validateClassificationLabels(labels ast.Expr, enumType *types.Descriptor) []binding.ClassificationClass {
	if labels == nil {
		return nil
	}
	labelsMap, ok := labels.(*ast.MapLit)
	if !ok {
		p.rejectAt("Classification labels must be a static map literal", labels.Span())
		return nil
	}
	before := len(p.ctx.Diagnostics)
	values := p.staticClassificationMap(labelsMap, "label")
	expected := map[string]bool{}
	for _, member := range enumType.EnumMembers {
		expected[member] = true
	}
	for _, member := range sortedMissingKeys(values, expected) {
		p.rejectAt(fmt.Sprintf("Classification labels are missing enum member '%s'", member), labelsMap.Span())
	}
	for _, member := range sortedExtraKeys(values, expected) {
		p.rejectAt(fmt.Sprintf("Classification labels contain extra member '%s'", member), labelsMap.Span())
	}
	// Value-shape checks iterate the map literal in source order exactly
	// like Python's `for member, value in values.items()` (a dict keeps
	// insertion order); Go's map has no order, so the ordered entry walk
	// below mirrors it. Rejected entries (non-static keys, duplicates) were
	// already skipped by staticClassificationMap with their own
	// diagnostics, so they are skipped here too.
	result := map[string]string{}
	descriptionMembers := map[string]string{}
	seen := map[string]bool{}
	for _, entry := range labelsMap.Entries() {
		name, keyOK := staticMapKey(entry.Key)
		if !keyOK || seen[name] {
			continue
		}
		seen[name] = true
		value := entry.Value
		if _, isString := value.(*ast.StringLit); !isString {
			p.rejectAt(fmt.Sprintf("Classification label for '%s' must be a static string", name), value.Span())
			continue
		}
		text := value.(*ast.StringLit).Value()
		if previous, dup := descriptionMembers[text]; dup {
			p.rejectAt(fmt.Sprintf("Classification label description for '%s' duplicates enum member '%s'", name, previous), value.Span())
			continue
		}
		descriptionMembers[text] = name
		result[name] = text
	}
	if len(p.ctx.Diagnostics) != before {
		return nil
	}
	classes := make([]binding.ClassificationClass, 0, len(enumType.EnumMembers))
	for _, member := range enumType.EnumMembers {
		classes = append(classes, binding.ClassificationClass{Member: member, Label: result[member]})
	}
	return classes
}

// validateClassificationFallback mirrors _validate_classification_fallback
// (:832-843): the fallback value must resolve to an explicit member fact of
// the very same enum (structural comparison, Python frozen-dataclass
// equality). A missing fact, a foreign enum or a non-member expression
// rejects with the same message at the fallback expression's span.
func (p *TargetValidationPass) validateClassificationFallback(fallback ast.Expr, enumType *types.Descriptor, ctx *Context) string {
	if fallback == nil {
		return ""
	}
	fallbackID, idOK := snapshotID(ctx, fallback)
	fact, found := ctx.EnumMembers[fallbackID]
	if !idOK || !found || !enumType.Equal(fact.EnumType) {
		p.rejectAt("Classification fallback must be an explicit member of the same enum", fallback.Span())
		return ""
	}
	return fact.Member
}

// staticClassificationMap mirrors _static_classification_map (:845-847):
// the classification reuse of the agent static-map helper with the
// "Classification" owner prefix.
func (p *TargetValidationPass) staticClassificationMap(node *ast.MapLit, label string) map[string]ast.Expr {
	return p.staticTargetMap(node, "Classification", label)
}

// ── extraction (schema-extraction, target_validation.py:854-1127) ─────

// validateExtractionCall mirrors _validate_extraction_call (:854-949): the
// schema contract must resolve (the type pass publishes it), the arity must
// be exactly three, every field must map exactly onto the target's
// parameter shapes, the model and config must be static, memory requires
// @mode advanced-chat with strategy "prompt" under the target's turn limit
// (with the memory capability enabled), vision requires the enabled
// capability plus a validated operand, strategy "function_call" requires a
// certified model, and the flat/function_call capability must be enabled.
// On success the ValidatedExtractionContract is published. The contract is
// a plain frozen dataclass in Python (no __post_init__ invariants), so the
// Go publication is direct.
func (p *TargetValidationPass) validateExtractionCall(node *ast.CallExpr, ctx *Context) {
	nodeID, hasID := snapshotID(ctx, node)
	if hasID {
		delete(ctx.ValidatedExtractions, nodeID)
	}
	schema := ctx.ExtractionContracts[nodeID]
	if schema == nil {
		p.rejectAt("schema extraction requires a resolved language extraction contract", node.Span())
		return
	}
	if len(node.Args()) != 3 {
		p.rejectAt("schema extraction requires exactly three value arguments", node.Span())
		return
	}

	before := len(ctx.Diagnostics)
	fields := p.validateExtractionFields(schema, ctx, node)
	model := p.validateExtractionModel(node.Args()[0])
	metadata := p.validateExtractionConfig(node.Args()[2], schema)
	if len(ctx.Diagnostics) != before || model == "" || metadata == nil {
		return
	}
	instruction, strategy, descriptions, memoryTurns, visionExpr :=
		metadata.instruction, metadata.strategy, metadata.descriptions, metadata.memoryTurns, metadata.vision
	if memoryTurns != nil {
		if p.mode != "advanced-chat" {
			p.rejectAt("Extraction memory requires @mode advanced-chat", node.Span())
			return
		}
		if strategy != "prompt" {
			p.rejectAt("Extraction memory currently requires strategy \"prompt\"", node.Span())
			return
		}
		if *memoryTurns > p.ctx.Profile.ParameterExtractorMemoryMaxTurns {
			p.rejectAt(
				fmt.Sprintf("Extraction memory value %d exceeds target '%s' limit %d",
					*memoryTurns, p.ctx.Profile.Name, p.ctx.Profile.ParameterExtractorMemoryMaxTurns),
				node.Span(),
			)
			return
		}
		memoryCapability := p.ctx.Profile.Capabilities.Lookup(CapabilityShapeStructuredExtractionMemory)
		if memoryCapability.State != CapabilityStateEnabled {
			p.rejectAt(
				fmt.Sprintf("%s is not lowerable by target '%s'",
					CapabilityShapeStructuredExtractionMemory, p.ctx.Profile.Name),
				node.Span(),
			)
			return
		}
	}
	if visionExpr != nil {
		visionCapability := p.ctx.Profile.Capabilities.Lookup(CapabilityShapeStructuredExtractionVision)
		if visionCapability.State != CapabilityStateEnabled {
			p.rejectAt(
				fmt.Sprintf("%s is not lowerable by target '%s'",
					CapabilityShapeStructuredExtractionVision, p.ctx.Profile.Name),
				visionExpr.Span(),
			)
			return
		}
		p.validateVisionOperand(visionExpr, ctx)
		if len(ctx.Diagnostics) != before {
			return
		}
	}
	if strategy == "function_call" && !p.ctx.Profile.ParameterExtractorFunctionCallModels[model] {
		p.rejectAt(
			fmt.Sprintf("Extraction function_call model '%s' has no certified tool-call evidence for target '%s'",
				model, p.ctx.Profile.Name),
			node.Span(),
		)
		return
	}
	capabilityShape := CapabilityShapeStructuredExtractionFlat
	if strategy == "function_call" {
		capabilityShape = CapabilityShapeStructuredExtractionFunctionCall
	}
	capability := p.ctx.Profile.Capabilities.Lookup(capabilityShape)
	if capability.State != CapabilityStateEnabled {
		p.rejectAt(
			fmt.Sprintf("%s is not lowerable by target '%s'", capabilityShape, p.ctx.Profile.Name),
			node.Span(),
		)
		return
	}
	validated := make([]binding.ValidatedExtractionField, 0, len(fields))
	for _, pair := range fields {
		options := []string(nil)
		if pair.field.TypeInfo.Kind == types.Enum && types.IsWellFormedEnum(pair.field.TypeInfo) {
			options = append([]string(nil), pair.field.TypeInfo.EnumMembers...)
		}
		validated = append(validated, binding.ValidatedExtractionField{
			Name:         pair.field.Name,
			NodecodaType: pair.field.TypeInfo,
			GraphonType:  pair.graphonType,
			Description:  descriptions[pair.field.Name],
			Options:      options,
		})
	}
	ctx.ValidatedExtractions[nodeID] = &binding.ValidatedExtractionContract{
		Schema:      schema,
		Model:       model,
		Instruction: instruction,
		Strategy:    strategy,
		Fields:      validated,
		MemoryTurns: memoryTurns,
		Vision:      visionExpr != nil,
	}
}

// extractionFieldPair is one validated schema field with its Graphon
// parameter value type (the Python list[tuple[ExtractionField, str]]).
type extractionFieldPair struct {
	field       binding.ExtractionField
	graphonType string
}

// validateExtractionFields mirrors _validate_extraction_fields (:961-993):
// every schema field must survive the target codec's parameter mapping
// (EXACT loss) and avoid the Graphon status reserved names; each rejection
// reports the unsupported shape text. Enum fields with well-formed members
// are certified (options list). The graphon shape's ValueType is asserted
// non-empty for EXACT shapes in the reference (assert graphon_shape
// .value_type is not None); the Go form skips defensively.
func (p *TargetValidationPass) validateExtractionFields(schema *binding.ExtractionContract, ctx *Context, node *ast.CallExpr) []extractionFieldPair {
	var validated []extractionFieldPair
	reserved := map[string]bool{"__is_success": true, "__reason": true, "__usage": true}
	const supported = "string, float, bool, string[], float[], bool[], map<string, any>[], and required scalar enum fields"
	for _, field := range schema.Fields {
		if reserved[field.Name] {
			p.rejectAt(
				fmt.Sprintf("Extraction field '%s' is reserved by the Graphon status contract", field.Name),
				node.Span(),
			)
			continue
		}
		// I3: the target codec is read through the profile's codec face
		// (TargetProfile.Codec — the internal/target shared layer), not a
		// backend package default. DefaultTargetProfile wires the
		// DIFY_116_GRAPHON_06 codec; the nil fallback keeps hand-built
		// profiles working (the Capabilities nil-guard precedent).
		typeCodec := ctx.Profile.Codec
		if typeCodec == nil {
			typeCodec = codec.Default()
		}
		graphonShape := typeCodec.ToParameterExtractorField(field.TypeInfo)
		if graphonShape.Loss != codec.LossExact {
			shape := unsupportedExtractionShape(field.TypeInfo)
			p.rejectAt(
				fmt.Sprintf("Extraction field '%s' has unsupported %s NodeCoda type for target '%s'; supported field types: %s",
					field.Name, shape, ctx.Profile.Name, supported),
				node.Span(),
			)
			continue
		}
		if graphonShape.ValueType == "" {
			continue
		}
		validated = append(validated, extractionFieldPair{field: field, graphonType: string(graphonShape.ValueType)})
	}
	return validated
}

// validateExtractionModel mirrors _validate_extraction_model (:995-1007).
func (p *TargetValidationPass) validateExtractionModel(model ast.Expr) string {
	sl, ok := model.(*ast.StringLit)
	if !ok {
		p.rejectAt("Extraction model must be a static string", model.Span())
		return ""
	}
	provider, name, hasSep := strings.Cut(sl.Value(), "/")
	if !hasSep || strings.TrimSpace(provider) == "" || strings.TrimSpace(name) == "" {
		p.rejectAt("Extraction model must be a static 'provider/model' string with non-empty parts", model.Span())
		return ""
	}
	return sl.Value()
}

// extractionConfig is the parsed extraction config metadata (the Python
// tuple instruction, strategy, descriptions, memory_turns, vision).
type extractionConfig struct {
	instruction  string
	strategy     string
	descriptions map[string]string
	memoryTurns  *int
	vision       ast.Expr
}

// validateExtractionConfig mirrors _validate_extraction_config (:1009-1062):
// the config must be a static map literal carrying instruction/
// descriptions/strategy (memory/vision optional); any new diagnostic (or a
// missing instruction/strategy/descriptions) rejects the whole config.
func (p *TargetValidationPass) validateExtractionConfig(config ast.Expr, schema *binding.ExtractionContract) *extractionConfig {
	configMap, ok := config.(*ast.MapLit)
	if !ok {
		p.rejectAt("Extraction config must be one static map literal", config.Span())
		return nil
	}
	before := len(p.ctx.Diagnostics)
	values := p.staticExtractionMap(configMap, "config")
	required := map[string]bool{"instruction": true, "descriptions": true, "strategy": true}
	for _, key := range sortedMissingKeys(values, required) {
		p.rejectAt(fmt.Sprintf("Extraction config is missing required config key '%s'", key), configMap.Span())
	}
	allowed := map[string]bool{"instruction": true, "descriptions": true, "strategy": true, "memory": true, "vision": true}
	for _, key := range sortedExtraKeys(values, allowed) {
		p.rejectAt(
			fmt.Sprintf("Extraction config has unknown config key '%s'; supported keys: instruction, descriptions, strategy, memory, vision", key),
			configMap.Span(),
		)
	}

	instructionExpr := values["instruction"]
	instruction := ""
	if sl, ok := instructionExpr.(*ast.StringLit); ok {
		instruction = sl.Value()
	}
	if instructionExpr != nil && instruction == "" {
		p.rejectAt("Extraction instruction must be a static string", instructionExpr.Span())
	}

	strategyExpr := values["strategy"]
	strategy := ""
	if sl, ok := strategyExpr.(*ast.StringLit); ok {
		strategy = sl.Value()
	}
	if strategyExpr != nil && strategy != "prompt" && strategy != "function_call" {
		p.rejectAt("Extraction strategy must be the static string 'prompt' or 'function_call'", strategyExpr.Span())
	}

	descriptions := p.validateExtractionDescriptions(values["descriptions"], schema)
	memoryExpr := values["memory"]
	visionExpr := values["vision"]
	var memoryTurns *int
	if memoryExpr != nil {
		if il, ok := memoryExpr.(*ast.IntLit); ok && il.Value() > 0 {
			turns := int(il.Value())
			memoryTurns = &turns
		} else {
			p.rejectAt("Extraction memory must be a positive integer literal", memoryExpr.Span())
		}
	}
	if len(p.ctx.Diagnostics) != before || instruction == "" || strategy == "" || descriptions == nil {
		return nil
	}
	return &extractionConfig{
		instruction:  instruction,
		strategy:     strategy,
		descriptions: descriptions,
		memoryTurns:  memoryTurns,
		vision:       visionExpr,
	}
}

// validateExtractionDescriptions mirrors _validate_extraction_descriptions
// (:1064-1097): the descriptions map must be static, cover the schema
// fields exactly, and carry distinct non-empty string values. The value
// check iterates the deduplicated static map in source order — the Go
// analogue of the Python dict iteration over _static_extraction_map's
// insertion-ordered values (each duplicate key was rejected above and is
// skipped here).
func (p *TargetValidationPass) validateExtractionDescriptions(descriptions ast.Expr, schema *binding.ExtractionContract) map[string]string {
	if descriptions == nil {
		return nil
	}
	descriptionsMap, ok := descriptions.(*ast.MapLit)
	if !ok {
		p.rejectAt("Extraction descriptions must be a static map literal", descriptions.Span())
		return nil
	}
	before := len(p.ctx.Diagnostics)
	values := p.staticExtractionMap(descriptionsMap, "description")
	schemaNames := map[string]bool{}
	for _, field := range schema.Fields {
		schemaNames[field.Name] = true
	}
	for _, name := range sortedMissingKeys(values, schemaNames) {
		p.rejectAt(fmt.Sprintf("Extraction schema is missing description for field '%s'", name), descriptionsMap.Span())
	}
	for _, name := range sortedExtraKeys(values, schemaNames) {
		p.rejectAt(fmt.Sprintf("Extraction config has extra description for field '%s'", name), descriptionsMap.Span())
	}
	result := map[string]string{}
	seen := map[string]bool{}
	for _, entry := range descriptionsMap.Entries() {
		name, ok := staticMapKey(entry.Key)
		if !ok || !schemaNames[name] || seen[name] {
			continue
		}
		seen[name] = true
		value, isString := entry.Value.(*ast.StringLit)
		if !isString || strings.TrimSpace(value.Value()) == "" {
			p.rejectAt(
				fmt.Sprintf("Extraction description for field '%s' must be a static non-empty string", name),
				entry.Value.Span(),
			)
			continue
		}
		result[name] = value.Value()
	}
	if len(p.ctx.Diagnostics) != before {
		return nil
	}
	return result
}

// staticExtractionMap mirrors _static_extraction_map (:1099-1110): parse
// one static extraction map with the label-specific duplicate message
// (Python "duplicate config key 'x'" vs "duplicate description for field
// 'x'").
func (p *TargetValidationPass) staticExtractionMap(node *ast.MapLit, label string) map[string]ast.Expr {
	values := map[string]ast.Expr{}
	for _, entry := range node.Entries() {
		name, ok := staticMapKey(entry.Key)
		if !ok {
			p.rejectAt(fmt.Sprintf("Extraction %s keys must be static identifiers or strings", label), entry.Key.Span())
			continue
		}
		if _, dup := values[name]; dup {
			duplicate := fmt.Sprintf("duplicate config key '%s'", name)
			if label == "description" {
				duplicate = fmt.Sprintf("duplicate description for field '%s'", name)
			}
			p.rejectAt(fmt.Sprintf("Extraction config has %s", duplicate), entry.Key.Span())
			continue
		}
		values[name] = entry.Value
	}
	return values
}

// unsupportedExtractionShape mirrors _unsupported_extraction_shape
// (:1472-1490): the human-readable NodeCoda shape label for a rejected
// extraction field type (Python str(descriptor.base.value) fallback — the
// Go DSLType.String() canonical name).
func unsupportedExtractionShape(descriptor *types.Descriptor) string {
	switch descriptor.Kind {
	case types.Optional:
		return "optional"
	case types.Map:
		return "scalar map"
	case types.File:
		return "file"
	case types.Any:
		return "any"
	case types.Record:
		return "nested record"
	case types.Array:
		if descriptor.Elem.Kind == types.Array {
			return "nested array"
		}
		if descriptor.Elem.Kind == types.Record {
			return "named-record array"
		}
	}
	return descriptor.Kind.String()
}

// renderAgentAlias renders the profile alias with Python repr semantics for
// the "not installed in target" message: 'alias' when the static string was
// resolved, None otherwise (Python {profile_alias!r} — repr of None).
func renderAgentAlias(alias string, resolved bool) string {
	if !resolved {
		return "None"
	}
	return "'" + alias + "'"
}

// sortedMissingKeys returns the sorted keys of required not present in
// values (Python `sorted(required - values.keys())`).
func sortedMissingKeys(values map[string]ast.Expr, required map[string]bool) []string {
	var missing []string
	for key := range required {
		if _, ok := values[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

// sameStringSet reports whether a and want hold the same set of strings
// (Python `set(a) != set(want)` — order-independent).
func sameStringSet(a, want []string) bool {
	if len(a) != len(want) {
		return false
	}
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	for _, s := range want {
		if !set[s] {
			return false
		}
	}
	return true
}

// visitRequestInputStmt mirrors visit_request_input_stmt (:404-455). On the
// form/action-type rejections the Python branch returns without calling
// super() (children pruned); the Go form returns nil for the same pruning.
func (p *TargetValidationPass) visitRequestInputStmt(node *ast.RequestInputStmt) ast.Visitor {
	// dify 路径：子节点用 **pass 自己**做访问器（子节点也过 dify 形状规则）。
	p.validateHumanInputStmt(node, p)
	return nil
}

// validateHumanInputStmt 校验 + 发布 `ask` 的语言级契约（**一条实现，两条目标
// 路径共用**：dify 的 `visitRequestInputStmt` 与非 dify 的 `languageContractChecker`）。
//
// 为什么非 dify 也要跑（R-108 同类，2026-09-30 一手）：`pkg/ir/build_condition.go`
// 的 `emitRequestInput` **无条件**读 `prog.ValidatedHumanInputs`（判据 = 「IR 读不读它」），
// 而旧实现只在 dify 路径填充该表 ⇒ coze-biz / coze-studio 上 `qa.Fields/Actions/Timeout`
// 恒为空 = 作者写的 `fields[].kind/options`、`actions[].title/style`、`timeout` 全部
// 静默丢（`failNames` 之外一个都没落地）。发布点与 dify 路径**同一个函数**，
// 避免「同一份契约在非 dify 上静默为空」这类半接线。
func (p *TargetValidationPass) validateHumanInputStmt(node *ast.RequestInputStmt, child ast.Visitor) *binding.ValidatedHumanInputContract {
	nodeID, hasID := snapshotID(p.ctx, node)
	if hasID {
		delete(p.ctx.ValidatedHumanInputs, nodeID)
	}
	before := len(p.ctx.Diagnostics)
	formType := descriptorForRef(node.FormType(), p.ctx)
	// ActionType == nil = **无动作形态**（S1，`ask<Form>({content, fields}) as r;`）：
	// 平台 `answer_type: text` 的问答节点这一格。无动作 ⇒ 无 action 类型、
	// 无臂、无超时（用户 2026-10-01 批准「甲 扩语言」；正本 docs/ASK-SYNTAX.md §2）。
	noAction := node.ActionType() == nil
	actionType := descriptorForRef(node.ActionType(), p.ctx)
	if noAction {
		// **无动作形态**的契约里 action 类型必须是 **nil**（构造器的无动作分支，
		// `human_input_contract.go::NewValidatedHumanInputContract`）—— 而
		// `descriptorForRef(nil, …)` 给出的是一个**非枚举描述符**，会让构造器走
		// 「action type must be a nominal enum」拒收 ⇒ 契约静默不发布。
		// 一手 2026-10-10：`ask<Form>({content, fields, limit, extra_output, llm}) as r;`
		// 的新键因此整批落不了地（渲染端读到全空）。修复 = 传 nil（形态本来就是
		// 「没有动作类型」）。
		actionType = nil
	}
	if noAction {
		// **无动作形态的载体表外 ⇒ 拒收并点名**（不是「语法错」，也不是静默降级）。
		// 依据 = r38 §六.2（用户 2026-10-01 批准）：dify / coze-studio 侧按平台优先级
		// 搁置 ⇒ 最低限度 = 不产错产物。旧行为（一手实测）不是拒收而是**产坏产物**：
		// dify ⇒ `human_input` 之后没有 `end`、`inputs: []`（表单字段与尾巴一起丢，
		// 仍 SUCCEEDED）；coze-studio ⇒ `answer_type: option` + 只连 1 条边。
		axes := askTargetAxesOf(p.ctx.Profile)
		if axes.known && !axes.carriesActionless {
			name := "dify"
			if p.ctx.Profile != nil && p.ctx.Profile.Name != "" {
				name = p.ctx.Profile.Name
			}
			p.rejectAt(fmt.Sprintf(
				"Human Input without an action type (ask<Form>({...}) as r;, the platform's answer_type: text) "+
					"has no carrier on target %s yet — the target's question widget is not wired for the actionless form; "+
					"supported target so far: coze-biz-1.0 (the source is not silently degraded)", name), node.Span())
			return nil
		}
	}
	if formType.Kind != types.Record {
		p.rejectAt("Human Input form must be a nominal record", node.Span())
		return nil
	}
	if !noAction && actionType.Kind != types.Enum {
		p.rejectAt("Human Input action type must be a nominal enum", node.Span())
		return nil
	}

	config := p.humanInputMap(node.Config(), "config")
	required := []string{"content", "fields"}
	if !noAction {
		required = append(required, "actions")
	}
	missingRequired := make([]string, 0, len(required))
	for _, key := range required {
		if _, ok := config[key]; !ok {
			missingRequired = append(missingRequired, key)
		}
	}
	sort.Strings(missingRequired)
	for _, key := range missingRequired {
		p.rejectAt(fmt.Sprintf("Human Input config is missing required key '%s'", key), node.Config().Span())
	}
	if delivery, ok := config["delivery"]; ok {
		p.rejectAt(
			"Human Input delivery is runtime invocation configuration, not NodeCoda node config",
			delivery.Span(),
		)
	}
	allowedKeys := map[string]bool{
		"content": true, "fields": true, "actions": true, "timeout": true, "delivery": true,
		// coze-biz `question` 参数面 ②③（2026-10-10 用户批准「丙·扩语言」，
		// 设计说明 = docs/five-step/rulings/2026-10-10-ruling-sheet-r57.md）：
		// `limit` / `extra_output` 是平台行为旋钮，`llm` 是节点自带模型配置，
		// `dynamic_options` 是动态选项来源（option_type: dynamic）。
		"limit": true, "extra_output": true, "llm": true, "dynamic_options": true,
	}
	for _, key := range sortedExtraKeys(config, allowedKeys) {
		p.rejectAt(fmt.Sprintf("Human Input config has extra key '%s'", key), node.Config().Span())
	}
	if noAction {
		// 无动作形态**没有超时载体**：本刀只落地「只问不选」这一格（用户
		// 2026-10-01 批准），`无动作 + 超时` 需平台另有一格承载（dify 装得下、
		// coze-biz 装不下）⇒ 现在**拒收并点名**，不静默丢超时（残余已登记）。
		if _, ok := config["timeout"]; ok {
			p.rejectAt(
				"Human Input without an action type has no timeout carrier (unsupported in this spelling)",
				node.Config().Span(),
			)
		}
		if _, ok := config["actions"]; ok {
			p.rejectAt(
				"Human Input without an action type must not declare 'actions'",
				node.Config().Span(),
			)
		}
	}

	// ── coze-biz `question` 参数面 ②③ 的四个新键（2026-10-10）──────────────
	// 目标能力：新键在 dify / coze-studio / runtime 上**没有载体**（那些 question
	// widget 不持有这些参数）⇒ 与「无动作形态」同一条处置：**fail loud 点名**，
	// 不静默降级（r38 §六.2 先例）。
	cozeAxes := askTargetAxesOf(p.ctx.Profile)
	if cozeAxes.known && !cozeAxes.carriesQuestionParams {
		for _, key := range []string{"limit", "extra_output", "llm", "dynamic_options"} {
			if _, present := config[key]; !present {
				continue
			}
			name := "dify"
			if p.ctx.Profile != nil && p.ctx.Profile.Name != "" {
				name = p.ctx.Profile.Name
			}
			p.rejectAt(fmt.Sprintf(
				"Human Input config key %q has no carrier on target %s yet — that target's question widget holds no such parameter; "+
					"supported target so far: coze-biz-1.0 (the source is not silently degraded)", key, name), node.Config().Span())
		}
	}
	maxAnswerCount := 0
	if expr, present := config["limit"]; present {
		if il, isInt := expr.(*ast.IntLit); isInt && il.Value() >= 0 {
			maxAnswerCount = int(il.Value())
		} else {
			p.rejectAt("Human Input limit must be a non-negative integer literal", expr.Span())
		}
	}
	extractFromAnswer := false
	if expr, present := config["extra_output"]; present {
		if bl, isBool := expr.(*ast.BoolLit); isBool {
			extractFromAnswer = bl.Value()
		} else {
			p.rejectAt("Human Input extra_output must be a boolean literal", expr.Span())
		}
	}
	llm := p.validateHumanInputLLM(config["llm"])
	// 平台自己的不变量：抽取需要模型（coze-studio `Adapt`: `ExtractOutput &&
	// llmParams == nil` ⇒ 报错）⇒ 语言侧同款 fail loud。
	if extractFromAnswer && !llm.HasAny() && len(p.ctx.Diagnostics) == before {
		p.rejectAt("Human Input with extra_output requires an 'llm' config — the platform needs the node's model to extract from the answer", node.Config().Span())
	}
	dynamicOptions := false
	if expr, present := config["dynamic_options"]; present {
		// 动态选项（平台 `option_type: dynamic` + `dynamic_option`）：选项表在
		// **运行期**由上游数据给出 ⇒ 静态 Action 枚举无法枚举它们。
		// 规范形（r57 §二②）：`dynamic_options` 存在 ⇒ 仍是有动作形态，但
		// **Action 必须恰 1 个成员**（该成员 = 「任一动态选项被选中」的单一臂；
		// 平台端口 = branch_0 [+ default]，`ExpectPorts` 的 dynamic 分支）。
		dynamicOptions = true
		if noAction {
			p.rejectAt("Human Input dynamic_options requires an action type (the platform declares one branch port for dynamic choices)", expr.Span())
		} else if actionType.Kind == types.Enum && len(actionType.EnumMembers) != 1 {
			p.rejectAt("Human Input dynamic_options requires an action enum with exactly one member (the single branch port the platform declares for dynamic choices)", expr.Span())
		}
	}
	if dynamicOptions && !noAction {
		// 静态选项槽（`options[].name` = 动作成员名）在动态态**没有载体**
		// （平台只在 `option_type: static` 时读它）⇒ 不写 `actions` 之外的决定，
		// 由渲染端按 `dynamic_options` 择路（renderQa）。
		_ = dynamicOptions
	}
	contentExpr := config["content"]
	content := ""
	switch ce := contentExpr.(type) {
	case *ast.StringLit:
		content = ce.Value()
	case *ast.TemplateValue:
		// 作者名额表形态（`content: template "…" using {k: v}`）：**文本**逐字承载
		// （零解析，与答复端点 / llm message 同一载体纪律）；**名额表**由 lowering
		// （build_condition.go::emitRequestInput）转成生产者引用，落到产物的
		// `node_inputs` —— 平台 `question` 文本里的 `{{name}}` 正是按它解析的。
		//
		// dify 目标这一格**没有名额载体**（本拼写装不下）⇒ 拒收并点名，不静默丢
		// ——与同文件「无动作形态 + timeout 无载体」同一条处置。
		content = ce.Text()
		if isDifyProfile(p.ctx.Profile) && len(ce.Bindings()) > 0 {
			p.rejectAt(
				"Human Input content template has no slot carrier on this target (unsupported in this spelling)",
				contentExpr.Span(),
			)
		}
	}
	if contentExpr != nil && content == "" {
		p.rejectAt("Human Input content must be a non-empty static string", contentExpr.Span())
	}

	fieldContracts := p.validateHumanInputFields(config["fields"], formType)
	var actionContracts []binding.HumanInputActionContract
	if !noAction {
		actionContracts = p.validateHumanInputActions(config["actions"], actionType)
	}
	timeoutValue, timeoutUnit, timeoutOK := p.validateHumanInputTimeout(config["timeout"])
	var contract *binding.ValidatedHumanInputContract
	actionsOK := actionContracts != nil
	if noAction {
		actionsOK = true
	}
	if len(p.ctx.Diagnostics) == before && content != "" &&
		fieldContracts != nil && actionsOK && timeoutOK {
		c, err := binding.NewValidatedHumanInputContract(
			formType, actionType, content, fieldContracts, actionContracts,
			timeoutValue, timeoutUnit,
			maxAnswerCount, extractFromAnswer, llm,
		)
		// The pass-side checks above (nominal form/action, non-empty static
		// content, exhaustive field/action contracts, positive timeout pair)
		// guarantee this path constructs only valid contracts, so a
		// __post_init__ failure is unreachable here (Python would raise).
		// No diagnostic is added: the diagnostics face must stay identical.
		if err == nil {
			contract = c
		}
	}

	// Python super().visit_request_input_stmt walks form_type, action_type,
	// config, result_binding, action branches and timeout body; the manual walk
	// keeps that coverage. 子访问器由调用方给（dify = pass 自己；非 dify =
	// languageContractChecker）—— 形状规则分路径，语言校验只有一份。
	ast.Walk(child, node.FormType())
	ast.Walk(child, node.ActionType()) // nil-safe（Walk 对 typed-nil 早退）
	ast.Walk(child, node.Config())
	ast.Walk(child, node.ResultBinding())
	for _, branch := range node.ActionBranches() {
		ast.Walk(child, branch)
	}
	ast.Walk(child, node.TimeoutBody())

	// 子节点里的错误同样阻断发布（判据 TestRequestInputSubtreeErrorPrevents
	// ContractPublication：契约与错误的源必须同生共死）。
	if contract != nil && len(p.ctx.Diagnostics) == before && hasID {
		p.ctx.ValidatedHumanInputs[nodeID] = contract
	}
	// 校验通过（含子树）⇒ 产物会出厂 ⇒ 装不下的 F 轴必须报账（`失配 ≠ 静默丢弃`）。
	// 校验未过时不报：那种源本来就不出产物（错误面已由语言判据给出）。
	if contract != nil && len(p.ctx.Diagnostics) == before {
		p.reportAskFidelityLosses(contract, node)
	}
	return contract
}

// ── ask 节点的保真（F）轴载体表 —— **一处定义** ──
//
// 语言面（`ask<Form, Action>` 的 config）有三个**保真**轴在 coze 家族上无处安放：
//
//   - `actions[m].title`（按钮显示文案）：平台的每个选项只有**一个**字符串
//     （`options[].name`），而规范形把**成员名**放进去（成员名同时是分支身份 +
//     反编译侧枚举成员的唯一来源）⇒ 标题一旦 ≠ 成员名就丢。
//   - `actions[m].style`（primary/default/accent/ghost）：平台 QA 结构里没有样式槽
//     （`oss:backend/domain/workflow/entity/vo/canvas.go::QA` = answer_type/limit/
//     extra_output/option_type/options[{name}]/question/dynamic_option，逐字段核过）。
//   - `timeout: {value, unit}`：同结构无超时时长槽 —— 平台只有 `default` **端口**
//     （超时臂的落点），没有时长配置。
//
// 逐目标列举（**表外 ⇒ 不发结论**：说「这个目标丢了 X」= 捏造一个我们没验过的
// 平台事实 ⇒ 谎报比沉默更坏，同 `retryIntervalCarrierOf` / `timeoutCarrierAxis`）：
//
//   - **dify**：三轴全承（`pkg/backend/dify/nodes.go::UserAction{Title, Label,
//     ButtonStyle}` + `HumanInputDataNode{Timeout, TimeoutUnit}`）。
//   - **coze-biz / coze-studio**：三轴俱无（上面那条 QA 结构；语料
//     `7574994076144779304_calendar_helper/workflow.yaml` 的 2/2 例也只有
//     question/options/limit/extra_output/llmParam 键）。
//   - **runtime / 表外**：不发结论（`internal/runtime/spec/configs.go::QaConfig` 有
//     `timeout` 值槽但**无单位槽** —— 半载属另一条轴的账，不在本表裁）。
//
// askTargetAxes = ask 的**目标轴表**：三根保真（F）轴的载体 + 该目标是否施加
// 严格的 action ID 线格式。
//
//   - requiresActionID：`IsCanonicalHumanInputActionID` 的注释自己写着它是
//     **「Dify's exact action ID wire contract」**（≤20 字符的 ASCII 标识符）——
//     那是 dify `user_actions[].member` 的线格式，不是语言级规则。coze 家族把成员
//     写进 `options[].name`（**自由文本槽**：平台会把它 TemplateRender 给用户看，
//     见 `nodes/qa/question_answer.go`），分支身份由**端口序号**承载 ⇒ 该契约不适用
//     （2026-09-30 一手：语料 `7574994076144779304_calendar_helper` 的 question 节点
//     选项名是任意文本，套用 dify 契约会让真实平台导出读不回来）。
//     语言级只剩「非空」——作者写的成员本来就是枚举成员 token，标识符规则由
//     解析器保证。
type askTargetAxes struct {
	known            bool
	carriesTitle     bool
	carriesStyle     bool
	carriesTimeout   bool
	requiresActionID bool
	// carriesQuestionParams：该目标**已落地** `question` 参数面 ②③ 的载体
	// （`limit` / `extra_output` / `llm` / `dynamic_options`，2026-10-10 用户批准
	// 「丙·扩语言」）。同 carriesActionless —— 这是**本仓实施现状**而不是平台事实：
	// 其余目标（dify / coze-studio / runtime）的 question widget 不持有这些参数
	// ⇒ 接了也是死载体 ⇒ fail loud（r38 §六.2 处置）。
	carriesQuestionParams bool

	// carriesActionless：该目标**已落地**「无动作形态」（S1，`ask<Form>({…}) as r;`
	// —— 平台 `answer_type: text` 的只问不选）的载体。
	//
	// **与上面三根 F 轴不同**：那三根说的是「平台装不下」（平台事实）；这一根说的是
	// **本仓的实施现状**（我们还没把载体接上去）——故它的 false 不是报账，而是
	// **fail loud**：接不上的目标要是静默放行，产物会丢尾巴/丢字段还标 SUCCEEDED
	// （一手 2026-10-01：dify ⇒ `human_input` 后无 `end`、`inputs` 空；coze-studio ⇒
	// `answer_type: option` 且只连 1 条边）。
	//
	// 依据（用户 2026-10-01 批准的 r38 §六.2）：dify / coze-studio 按平台优先级**搁置**
	// ⇒ 只做「不产错产物的最低限度」= 点名拒收 + 登记，不在本刀改这两个后端。
	// coze-biz = true（本刀主线，读写两侧均已落地，判据 =
	// `internal/compiler/cozebiz_qa_text_form_contract_test.go`）。
	carriesActionless bool
}

// askTargetAxesOf 目标 → ask 的目标轴（一处定义；表见上方注释）。
func askTargetAxesOf(profile *TargetProfile) askTargetAxes {
	switch {
	case isDifyProfile(profile):
		return askTargetAxes{known: true, carriesTitle: true, carriesStyle: true, carriesTimeout: true, requiresActionID: true}
	case isCozeStudioProfile(profile):
		// 三根 F 轴俱无（同 coze-biz）；无动作形态**本刀未落地**（搁置面）。
		return askTargetAxes{known: true}
	case isCozeBizProfile(profile):
		// 三根 F 轴俱无；无动作形态**已落地**（`render_loop.go::renderQa` 的
		// `qa.TextForm` 分支 + `emitRequestInput` 的无臂直通）；`question` 参数面
		// ②③（limit/extra_output/llm/dynamic_options）**已落地**（r57）。
		return askTargetAxes{known: true, carriesActionless: true, carriesQuestionParams: true}
	}
	return askTargetAxes{}
}

// reportAskFidelityLosses 该目标装不下的 F 轴 ⇒ **一条声明一条账**（与枚举值集 /
// 超时轴 / 重试间隔同一条纪律：丢可以，静默不可以）。
//
// 只列**非缺省**值：`title == member`、`style: "default"`、未写 timeout 与「平台
// 装不下」在产物上不可观察地等价 ⇒ 报了就是噪音（同 `IntervalExplicit()` 的口径）。
func (p *TargetValidationPass) reportAskFidelityLosses(c *binding.ValidatedHumanInputContract, node *ast.RequestInputStmt) {
	carrier := askTargetAxesOf(p.ctx.Profile)
	if !carrier.known {
		return
	}
	lost := []string{}
	if !carrier.carriesTitle {
		for _, a := range c.Actions {
			if a.Title == a.Member {
				continue
			}
			lost = append(lost, fmt.Sprintf(`action title %s→%q`, a.Member, a.Title))
		}
	}
	if !carrier.carriesStyle {
		for _, a := range c.Actions {
			if a.Style == binding.HumanInputActionStyleDefault {
				continue
			}
			lost = append(lost, fmt.Sprintf("action style %s:%s", a.Member, a.Style))
		}
	}
	if !carrier.carriesTimeout && c.TimeoutValue > 0 {
		lost = append(lost, fmt.Sprintf("timeout {%d %s}", c.TimeoutValue, c.TimeoutUnit))
	}
	if len(lost) == 0 {
		return
	}
	p.ctx.Emit(diag.TARGET_CONTRACT_DEGRADED, diag.Warning, fmt.Sprintf(
		"target has no carrier for the ask fidelity axes: %s — the platform question node holds only answer_type/option_type/options[{name}]/question (one string per option = the member name), so a distinct title/style/timeout has nowhere to go; the source is not rejected",
		strings.Join(lost, ", ")), node.Config().Span())
}

// validateHumanInputFields mirrors _validate_human_input_fields (:457-521).
func (p *TargetValidationPass) validateHumanInputFields(fieldsExpr ast.Expr, formType *types.Descriptor) []binding.HumanInputFieldContract {
	if fieldsExpr == nil {
		return nil
	}
	fieldsMap, ok := fieldsExpr.(*ast.MapLit)
	if !ok {
		p.rejectAt("Human Input fields must be a static map literal", fieldsExpr.Span())
		return nil
	}
	before := len(p.ctx.Diagnostics)
	configs := p.humanInputMap(fieldsMap, "field")
	expectedNames := map[string]bool{}
	for _, field := range formType.Fields {
		expectedNames[field.Name] = true
	}
	var missing, extra []string
	for _, field := range formType.Fields {
		if _, ok := configs[field.Name]; !ok {
			missing = append(missing, field.Name)
		}
	}
	for name := range configs {
		if !expectedNames[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	for _, name := range missing {
		p.rejectAt(fmt.Sprintf("Human Input fields are missing form field '%s'", name), fieldsMap.Span())
	}
	for _, name := range extra {
		p.rejectAt(fmt.Sprintf("Human Input fields contain extra form field '%s'", name), fieldsMap.Span())
	}

	var contracts []binding.HumanInputFieldContract
	for _, field := range formType.Fields {
		name := field.Name
		configExpr := configs[name]
		if configExpr == nil {
			continue
		}
		configMap, ok := configExpr.(*ast.MapLit)
		if !ok {
			p.rejectAt(fmt.Sprintf("Human Input field '%s' config must be a static map literal", name), configExpr.Span())
			continue
		}
		values := p.humanInputMap(configMap, fmt.Sprintf("field '%s' config", name))
		for _, key := range sortedExtraKeys(values, map[string]bool{"kind": true, "options": true}) {
			p.rejectAt(fmt.Sprintf("Human Input field '%s' config has extra key '%s'", name, key), configMap.Span())
		}
		kindExpr := values["kind"]
		if kindExpr == nil {
			p.rejectAt(fmt.Sprintf("Human Input field '%s' is missing required key 'kind'", name), configMap.Span())
			continue
		}
		kind, ok := kindExpr.(*ast.StringLit)
		if !ok {
			p.rejectAt(fmt.Sprintf("Human Input field '%s' kind must be a static string", name), kindExpr.Span())
			continue
		}

		var expectedKind string
		var options []string
		switch {
		case field.Type.Kind == types.String:
			expectedKind = string(binding.HumanInputFieldKindParagraph)
			if opts, ok := values["options"]; ok {
				p.rejectAt(fmt.Sprintf("Human Input paragraph field '%s' cannot declare options", name), opts.Span())
			}
		case field.Type.Kind == types.Enum:
			expectedKind = string(binding.HumanInputFieldKindSelect)
			options = field.Type.EnumMembers
			if explicit := values["options"]; explicit != nil {
				parsed := p.staticHumanInputOptions(explicit, name)
				if parsed != nil && !binding.StringSlicesEqual(parsed, options) {
					p.rejectAt(
						fmt.Sprintf("Human Input field '%s' options must match enum declaration order", name),
						explicit.Span(),
					)
				}
			}
		default:
			p.rejectAt(
				fmt.Sprintf("Human Input field '%s' has unsupported type %s", name, field.Type.String()),
				configMap.Span(),
			)
			continue
		}
		if kind.Value() != expectedKind {
			p.rejectAt(
				fmt.Sprintf("Human Input field '%s' requires kind '%s', got '%s'", name, expectedKind, kind.Value()),
				kindExpr.Span(),
			)
			continue
		}
		contract, err := binding.NewHumanInputFieldContract(name, binding.HumanInputFieldKind(expectedKind), field.Type, options)
		if err != nil {
			// Defensive: the kind/type/options checks above guarantee this
			// path constructs only valid contracts; a __post_init__ failure
			// is unreachable (Python would raise). No diagnostic is added —
			// the diagnostics face must stay identical.
			continue
		}
		contracts = append(contracts, *contract)
	}
	if len(p.ctx.Diagnostics) != before {
		return nil
	}
	return contracts
}

// staticHumanInputOptions mirrors _static_human_input_options (:523-533).
func (p *TargetValidationPass) staticHumanInputOptions(expr ast.Expr, fieldName string) []string {
	arr, ok := expr.(*ast.ArrayLit)
	if !ok {
		p.rejectAt(fmt.Sprintf("Human Input field '%s' options must be a static string array", fieldName), expr.Span())
		return nil
	}
	var out []string
	for _, item := range arr.Elements() {
		s, ok := item.(*ast.StringLit)
		if !ok {
			p.rejectAt(fmt.Sprintf("Human Input field '%s' options must be a static string array", fieldName), expr.Span())
			return nil
		}
		out = append(out, s.Value())
	}
	return out
}

// validateHumanInputActions mirrors _validate_human_input_actions (:535-622).
func (p *TargetValidationPass) validateHumanInputActions(actionsExpr ast.Expr, actionType *types.Descriptor) []binding.HumanInputActionContract {
	if actionsExpr == nil {
		return nil
	}
	actionsMap, ok := actionsExpr.(*ast.MapLit)
	if !ok {
		p.rejectAt("Human Input actions must be a static map literal", actionsExpr.Span())
		return nil
	}
	before := len(p.ctx.Diagnostics)
	configs := p.humanInputMap(actionsMap, "action")
	expected := map[string]bool{}
	for _, member := range actionType.EnumMembers {
		expected[member] = true
	}
	var missing, extra []string
	for _, member := range actionType.EnumMembers {
		if _, ok := configs[member]; !ok {
			missing = append(missing, member)
		}
	}
	for name := range configs {
		if !expected[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	for _, member := range missing {
		p.rejectAt(fmt.Sprintf("Human Input actions are missing enum member '%s'", member), actionsMap.Span())
	}
	for _, member := range extra {
		p.rejectAt(fmt.Sprintf("Human Input actions contain extra enum member '%s'", member), actionsMap.Span())
	}

	var contracts []binding.HumanInputActionContract
	axes := askTargetAxesOf(p.ctx.Profile)
	for _, member := range actionType.EnumMembers {
		if axes.requiresActionID && !binding.IsCanonicalHumanInputActionID(member) {
			p.rejectAt(
				fmt.Sprintf("Human Input action ID '%s' must match [A-Za-z_][A-Za-z0-9_]* and contain at most 20 characters", member),
				actionsMap.Span(),
			)
			continue
		}
		if !axes.requiresActionID && member == "" {
			p.rejectAt("Human Input action member must not be empty", actionsMap.Span())
			continue
		}
		configExpr := configs[member]
		if configExpr == nil {
			continue
		}
		configMap, ok := configExpr.(*ast.MapLit)
		if !ok {
			p.rejectAt(fmt.Sprintf("Human Input action '%s' config must be a static map literal", member), configExpr.Span())
			continue
		}
		values := p.humanInputMap(configMap, fmt.Sprintf("action '%s' config", member))
		for _, key := range sortedExtraKeys(values, map[string]bool{"title": true, "style": true}) {
			p.rejectAt(fmt.Sprintf("Human Input action '%s' config has extra key '%s'", member, key), configMap.Span())
		}
		titleExpr := values["title"]
		title := ""
		if sl, ok := titleExpr.(*ast.StringLit); ok {
			title = sl.Value()
		}
		if titleExpr == nil || title == "" {
			span := configMap.Span()
			if titleExpr != nil {
				span = titleExpr.Span()
			}
			p.rejectAt(fmt.Sprintf("Human Input action '%s' title must be a non-empty static string", member), span)
			continue
		}
		// Python len(title) counts code points (the title limit is in
		// characters), so Go must count runes, not UTF-8 bytes (R1 M1: a
		// 60-CJK-character title is legal).
		if utf8.RuneCountInString(title) > 100 {
			p.rejectAt(fmt.Sprintf("Human Input action '%s' title exceeds the target maximum of 100 characters", member), titleExpr.Span())
			continue
		}
		// `style` 的**必填性**是目标约束，不是语言级必填：ASK-SYNTAX.md §1 把
		// `user_actions[]: {member, title, style}` 写在「Dify Human-Input 节点」的
		// 输入形状里（dify 的按钮需要样式槽）；coze 家族（QA / QuestionAnswerNode）
		// 的结构里没有样式槽（见 askTargetAxesOf 表）⇒ 要求作者写一个必然被
		// 丢弃的字段没有意义。缺席 = 缺省 `default`，与产物侧「缺省不报账」同口径。
		style := binding.HumanInputActionStyleDefault
		styleExpr := values["style"]
		if styleExpr == nil {
			if axes.carriesStyle {
				p.rejectAt(fmt.Sprintf("Human Input action '%s' is missing required static style", member), configMap.Span())
				continue
			}
		} else {
			sl, ok := styleExpr.(*ast.StringLit)
			if !ok {
				p.rejectAt(fmt.Sprintf("Human Input action '%s' style must be a static string", member), styleExpr.Span())
				continue
			}
			if !binding.ValidActionStyle(binding.HumanInputActionStyle(sl.Value())) {
				p.rejectAt(fmt.Sprintf("Human Input action '%s' style must be primary, default, accent, or ghost", member), styleExpr.Span())
				continue
			}
			style = binding.HumanInputActionStyle(sl.Value())
		}
		if !axes.requiresActionID {
			// coze 家族：成员是自由文本槽 `options[].name` 的值 ⇒ 只受语言级
			// 约束（非空；标识符性由解析器保证），不套 dify 的 ≤20/ASCII 线格式。
			// 上面已完成 title/style 的全部检查，故可直接构造。
			contracts = append(contracts, binding.HumanInputActionContract{Member: member, Title: title, Style: style})
			continue
		}
		contract, err := binding.NewHumanInputActionContract(member, title, style)
		if err != nil {
			// Defensive: the member/title/style checks above guarantee this
			// path constructs only valid contracts; a __post_init__ failure
			// is unreachable (Python would raise). No diagnostic is added —
			// the diagnostics face must stay identical.
			continue
		}
		contracts = append(contracts, *contract)
	}
	if len(p.ctx.Diagnostics) != before {
		return nil
	}
	return contracts
}

// validateHumanInputTimeout mirrors _validate_human_input_timeout (:624-662).
// The bool reports whether a validated (value, unit) pair was produced.
// validateHumanInputLLM 解析 `llm: {…}` 子表 —— ask 宿主节点**自带**的模型配置
// （平台 coze-biz `question` 的 `llmParam`，扁平标量表）。键词汇 = `llm()` 的旋钮
// 词汇（**一处定义**：`docs/UNIFIED-LLM-SEMANTICS.md` §1 / R-70）。
//
// **表示法 = 指针**（presence）：语言面写了这个键 ⇒ 发射；没写 ⇒ 不发。平台导出
// 会写 `systemPrompt: ""` 这种「写了、值空」的键（一手）⇒ 值类型分不出「缺席」与
// 「空值」，必须用 presence（同 `OutputEmitterNode.CallTransferVoice` 的理由）。
//
// 返回 nil = 该键缺席；解析失败同样返回 nil（诊断已发 ⇒ 契约不发布，
// 不会静默半接线）。
func (p *TargetValidationPass) validateHumanInputLLM(expr ast.Expr) *binding.HumanInputLLM {
	if expr == nil {
		return nil
	}
	m, ok := expr.(*ast.MapLit)
	if !ok {
		p.rejectAt("Human Input llm must be a static map literal", expr.Span())
		return nil
	}
	before := len(p.ctx.Diagnostics)
	values := p.humanInputMap(m, "llm config")
	allowed := map[string]bool{
		"model": true, "model_name": true, "temperature": true, "max_tokens": true,
		"top_p": true, "response_format": true, "generation_diversity": true, "system_prompt": true,
	}
	for _, key := range sortedExtraKeys(values, allowed) {
		p.rejectAt(fmt.Sprintf("Human Input llm config has extra key '%s'", key), m.Span())
	}
	llm := &binding.HumanInputLLM{}
	// strPtr：写了 ⇒ 指向字面量（含空串）；非字面量 ⇒ 拒。
	strPtr := func(key string, dst **string) {
		v, ok := values[key]
		if !ok {
			return
		}
		sl, isStr := v.(*ast.StringLit)
		if !isStr {
			p.rejectAt(fmt.Sprintf("Human Input llm.%s must be a string literal", key), v.Span())
			return
		}
		val := sl.Value()
		*dst = &val
	}
	// numPtr：写了 ⇒ 指向该数（int 拓宽成 float）。temperature / top_p 含 int
	// 拓宽（语料实证 0.8 / 1 都合法，同 `llmConfigKnownKeyTypes`）。
	numPtr := func(key string) *float64 {
		v, ok := values[key]
		if !ok {
			return nil
		}
		switch n := v.(type) {
		case *ast.IntLit:
			f := float64(n.Value())
			return &f
		case *ast.FloatLit:
			f := n.Value()
			return &f
		}
		p.rejectAt(fmt.Sprintf("Human Input llm.%s must be a number literal", key), v.Span())
		return nil
	}
	// intPtr：写了 ⇒ 指向该整数。`max_tokens: 0` / `response_format: 0` 是**写了零**，
	// 与缺席不同（平台侧 `SimpleLLMParam` 无 omitempty，写了就落键）。
	intPtr := func(key string, dst **int) {
		v, ok := values[key]
		if !ok {
			return
		}
		if il, isInt := v.(*ast.IntLit); isInt {
			n := int(il.Value())
			*dst = &n
			return
		}
		p.rejectAt(fmt.Sprintf("Human Input llm.%s must be an integer literal", key), v.Span())
	}
	strPtr("model", &llm.Model)
	strPtr("model_name", &llm.ModelName)
	strPtr("generation_diversity", &llm.GenerationDiversity)
	strPtr("system_prompt", &llm.SystemPrompt)
	llm.Temperature = numPtr("temperature")
	llm.TopP = numPtr("top_p")
	intPtr("max_tokens", &llm.MaxTokens)
	intPtr("response_format", &llm.ResponseFormat)
	// `model` 的载体是平台 `modelType`（**整数**）⇒ 语言串必须是 `coze:<n>` 协议
	// 串（与 `llm()` 同一个协议；非 coze: 前缀在本目标装不下 = 不静默降级）。
	if llm.Model != nil && !isCozeModelProtocolString(*llm.Model) {
		p.rejectAt(fmt.Sprintf(
			"Human Input llm.model must be a \"coze:<digits>\" protocol string (the target carries the model as an integer modelType), got %q",
			*llm.Model), values["model"].Span())
	}
	if len(p.ctx.Diagnostics) != before {
		return nil
	}
	if !llm.HasAny() {
		// 空子表 = 作者写了一个**什么都不发**的配置（静默无操作）⇒ fail loud。
		// 判据与 `binding.NewValidatedHumanInputContract` 的 `HasAny` 同源。
		p.rejectAt("Human Input llm config must declare at least one key", m.Span())
		return nil
	}
	return llm
}

// isCozeModelProtocolString 报「该串是 `coze:<digits>` 协议串」——ask 的
// `llm.model` 载体是平台 `modelType`（整数），非该协议串在本目标装不下
// （`llm()` 的 model 位置参数同协议；此处只做**可承载性**校验，不做注册表查询）。
func isCozeModelProtocolString(model string) bool {
	const prefix = "coze:"
	if !strings.HasPrefix(model, prefix) {
		return false
	}
	digits := model[len(prefix):]
	if digits == "" {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}

func (p *TargetValidationPass) validateHumanInputTimeout(timeoutExpr ast.Expr) (int, binding.HumanInputTimeoutUnit, bool) {
	if timeoutExpr == nil {
		return 0, "", true // 无超时配置，合法
	}
	timeoutMap, ok := timeoutExpr.(*ast.MapLit)
	if !ok {
		p.rejectAt("Human Input timeout must be a static map literal", timeoutExpr.Span())
		return 0, "", false
	}
	before := len(p.ctx.Diagnostics)
	values := p.humanInputMap(timeoutMap, "timeout config")
	var missingKeys []string
	for _, key := range []string{"value", "unit"} {
		if _, ok := values[key]; !ok {
			missingKeys = append(missingKeys, key)
		}
	}
	sort.Strings(missingKeys)
	for _, key := range missingKeys {
		p.rejectAt(fmt.Sprintf("Human Input timeout is missing required key '%s'", key), timeoutMap.Span())
	}
	for _, key := range sortedExtraKeys(values, map[string]bool{"value": true, "unit": true}) {
		p.rejectAt(fmt.Sprintf("Human Input timeout has extra key '%s'", key), timeoutMap.Span())
	}
	valueExpr := values["value"]
	value := int64(0)
	valueIsInt := false
	if il, ok := valueExpr.(*ast.IntLit); ok {
		value = il.Value()
		valueIsInt = true
	}
	if valueExpr != nil && (!valueIsInt || value <= 0) {
		p.rejectAt("Human Input timeout value must be a positive static integer", valueExpr.Span())
	}
	unitExpr := values["unit"]
	unit := ""
	unitIsString := false
	if sl, ok := unitExpr.(*ast.StringLit); ok {
		unit = sl.Value()
		unitIsString = true
	}
	if unitExpr != nil && unit != "hour" && unit != "day" {
		p.rejectAt("Human Input timeout unit must be hour or day", unitExpr.Span())
	}
	if len(p.ctx.Diagnostics) != before || !valueIsInt || !unitIsString {
		return 0, "", false
	}
	return int(value), binding.HumanInputTimeoutUnit(unit), true
}

// humanInputMap mirrors _human_input_map (:664-666).
func (p *TargetValidationPass) humanInputMap(node *ast.MapLit, label string) map[string]ast.Expr {
	return p.staticTargetMap(node, "Human Input", label)
}

// staticTargetMap mirrors _static_target_map (:668-690): parse one static
// target config map with the shared duplicate-key handling.
func (p *TargetValidationPass) staticTargetMap(node *ast.MapLit, owner, label string) map[string]ast.Expr {
	values := map[string]ast.Expr{}
	for _, entry := range node.Entries() {
		name, ok := staticMapKey(entry.Key)
		if !ok {
			p.rejectAt(fmt.Sprintf("%s %s keys must be static identifiers or strings", owner, label), entry.Key.Span())
			continue
		}
		if _, dup := values[name]; dup {
			p.rejectAt(fmt.Sprintf("%s has duplicate %s key '%s'", owner, label, name), entry.Key.Span())
			continue
		}
		values[name] = entry.Value
	}
	return values
}

// visitIfStmt mirrors visit_if_stmt (:1129-1134).
func (p *TargetValidationPass) visitIfStmt(node *ast.IfStmt) ast.Visitor {
	if node.Condition() != nil && !isDirectConditionExpr(node.Condition(), p.ctx, p.declInits) {
		p.validateGeneratedCodeExpr(node.Condition(), p.ctx)
	}
	ast.Walk(p, node.Condition())
	ast.Walk(p, node.ThenBody())
	for _, branch := range node.ElseIfs() {
		ast.Walk(p, branch)
	}
	ast.Walk(p, node.ElseBody())
	return nil
}

// visitWhileStmt mirrors visit_while_stmt (:1136-1141).
func (p *TargetValidationPass) visitWhileStmt(node *ast.WhileStmt) ast.Visitor {
	if node.Condition() != nil && !isDirectConditionExpr(node.Condition(), p.ctx, p.declInits) {
		p.validateGeneratedCodeExpr(node.Condition(), p.ctx)
	}
	ast.Walk(p, node.Condition())
	ast.Walk(p, node.Body())
	return nil
}

// visitReturnStmt mirrors visit_return_stmt (:1143-1147).
func (p *TargetValidationPass) visitReturnStmt(node *ast.ReturnStmt) ast.Visitor {
	if node.Value() != nil {
		p.validateReturnExpr(node.Value(), p.ctx)
	}
	ast.Walk(p, node.Value())
	return nil
}

// visitYieldStmt mirrors visit_yield_stmt (:1149-1153).
func (p *TargetValidationPass) visitYieldStmt(node *ast.YieldStmt) ast.Visitor {
	if node.Value() != nil && !isDirectSelectorExpr(node.Value()) {
		p.validateGeneratedCodeExpr(node.Value(), p.ctx)
	}
	ast.Walk(p, node.Value())
	return nil
}

// visitOutputStmt mirrors visit_output_stmt (:1161-1165): the message
// expression must be directly selectable (or a literal) — code generation
// for intermediate output is not supported.
func (p *TargetValidationPass) visitOutputStmt(node *ast.OutputStmt) ast.Visitor {
	if node.Value() != nil && !isDirectSelectorExpr(node.Value()) {
		p.validateGeneratedCodeExpr(node.Value(), p.ctx)
	}
	ast.Walk(p, node.Value())
	return nil
}

// validateReturnExpr mirrors _validate_return_expr (:1167-1172).
func (p *TargetValidationPass) validateReturnExpr(expr ast.Expr, ctx *Context) {
	if m, ok := expr.(*ast.MapLit); ok {
		// 键必须**静态**（标识符名或字符串值，ast.StaticMapKey）——原文只认
		// 字符串字面量，导致 `return { a: 1 }` 整块跳过值校验（校验面漏口）。
		allStaticKeys := true
		for _, entry := range m.Entries() {
			if _, ok := ast.StaticMapKey(entry.Key); !ok {
				allStaticKeys = false
				break
			}
		}
		if allStaticKeys {
			for _, entry := range m.Entries() {
				if !isDirectSelectorExpr(entry.Value) {
					p.validateGeneratedCodeExpr(entry.Value, p.ctx)
				}
			}
			return
		}
	}
	if _, isTemplate := expr.(*ast.TemplateString); !isTemplate && !isDirectSelectorExpr(expr) {
		p.validateGeneratedCodeExpr(expr, p.ctx)
	}
}

// validateGeneratedCodeExpr mirrors _validate_generated_code_expr
// (:1174-1184).
func (p *TargetValidationPass) validateGeneratedCodeExpr(expr ast.Expr, ctx *Context) {
	if !isCompilerGeneratedCodeExpression(expr, ctx, p.declInits) {
		return
	}
	selectors := scalarFileSelectors(expr, ctx, p.declInits)
	var unreported []ast.Expr
	for _, selector := range selectors {
		id, ok := snapshotID(ctx, selector)
		if !ok {
			continue // not in the snapshot (programmatic node): nothing to report
		}
		if !p.reportedScalarFileExprs[id] {
			unreported = append(unreported, selector)
		}
	}
	if len(unreported) == 0 {
		return
	}
	for _, selector := range selectors {
		if id, ok := snapshotID(ctx, selector); ok {
			p.reportedScalarFileExprs[id] = true
		}
	}
	p.rejectAt("single-file Code input is not JSON-safe for the Dify target", unreported[0].Span())
}

// validateExtractTextOperand mirrors _validate_extract_text_operand
// (:1186-1192). Reachable once extract_text registers; whitebox-testable now.
func (p *TargetValidationPass) validateExtractTextOperand(expr ast.Expr, ctx *Context) {
	descriptor := exprTypeOf(ctx, expr)
	if !isSelectorExpr(expr) || !extractableFileDescriptor(descriptor) {
		p.rejectAt(
			"extract_text requires a selector-backed scalar file with a declared extractable extension",
			expr.Span(),
		)
	}
}

// validateVisionOperand mirrors _validate_vision_operand (:1194-1199).
func (p *TargetValidationPass) validateVisionOperand(expr ast.Expr, ctx *Context) {
	descriptor := exprTypeOf(ctx, expr)
	if !isSelectorExpr(expr) || !isScalarFile(descriptor) {
		p.rejectAt(
			"vision requires a selector-backed scalar file for the Dify target",
			expr.Span(),
		)
	}
}

// visitFieldAccessExpr mirrors visit_field_access_expr (:1201-1211).
func (p *TargetValidationPass) visitFieldAccessExpr(node *ast.FieldAccessExpr) ast.Visitor {
	var objType *types.Descriptor
	if node.Obj() != nil {
		objType = exprTypeOf(p.ctx, node.Obj())
	}
	resultType := exprTypeOf(p.ctx, node)
	objIsFile := objType != nil && types.UnwrapOptional(objType).Kind == types.File
	resultIsFile := resultType != nil && types.UnwrapOptional(resultType).Kind == types.File
	if objIsFile || resultIsFile {
		p.rejectAt("single file field access is not representable by the Dify target", node.Span())
	}
	ast.Walk(p, node.Obj())
	return nil
}

// visitAssignStmt mirrors visit_assign_stmt (:1213-1290). The Python early
// returns without super() prune the children; the Go returns of nil keep the
// same pruning.
func (p *TargetValidationPass) visitAssignStmt(node *ast.AssignStmt) ast.Visitor {
	walkChildren := func() {
		ast.Walk(p, node.Target())
		ast.Walk(p, node.Value())
	}
	if _, ok := node.Target().(*ast.IdentExpr); !ok {
		walkChildren()
		return nil
	}
	_, symID, ok := symbolDecl(p.ctx, node.Target())
	if !ok {
		walkChildren()
		return nil
	}
	isConversationState := p.ctx.ConversationVarIDs[symID]
	isLoopState := false
	if nodeID, ok := snapshotID(p.ctx, node); ok {
		for _, facts := range p.ctx.LoopStates {
			for _, fact := range facts {
				if fact.SymbolID == symID && containsNodeID(fact.WriteSites, nodeID) {
					isLoopState = true
				}
			}
		}
	}
	if !isConversationState && !isLoopState {
		walkChildren()
		return nil
	}
	if p.manifest.Lookup(CapabilityShapeStateAssignment).State != CapabilityStateEnabled {
		p.reject("state.assignment is not certified for the selected target", node)
		return nil
	}
	if isLoopState {
		walkChildren()
		return nil
	}

	target := exprTypeOf(p.ctx, node.Target())
	rhs := assignerRhs(node)
	if target == nil || rhs == nil {
		p.reject("conversation variable assigner input is unsupported", node)
		return nil
	}
	kind := classifyAssignerRhs(rhs, target)
	switch node.Operation() {
	case "<<":
		if kind != assignerDynamic || !arrayVariableInputSupported(target, rhs, p.ctx) {
			p.reject(
				"conversation variable append requires a selector-backed variable input; "+
					"literal append is not representable by Graphon v2",
				rhs,
			)
		}
		return nil
	case "+=", "-=", "*=", "/=":
		if (target.Kind != types.Int && target.Kind != types.Float) ||
			kind != assignerLiteral || !isNumericLiteral(rhs) {
			p.reject(
				"conversation variable compound assignment requires a numeric literal for a numeric target",
				rhs,
			)
		}
		return nil
	}
	// Graphon/Dify supports overwriting a scalar conversation variable from a
	// directly selected variable. Keep the target boundary strict: complex
	// expressions still require a materialization node and array/map values are
	// not accepted by this assigner shape.
	scalarTarget := target.Kind == types.String || target.Kind == types.Int ||
		target.Kind == types.Float || target.Kind == types.Bool
	directSelector := kind == assignerDynamic && scalarTarget && isDirectSelectorExpr(rhs)
	literalScalar := kind == assignerLiteral && scalarTarget && isScalarLiteral(rhs)
	if node.Operation() != "set" || (!literalScalar && !directSelector) {
		p.reject("conversation variable set operation only supports scalar literal values", rhs)
	}
	return nil
}

// validateConversationDeclarations mirrors
// _validate_conversation_declarations (:1292-1314).
func (p *TargetValidationPass) validateConversationDeclarations(program *ast.Program, ctx *Context) {
	for _, decl := range program.ConversationDecls() {
		descriptor := descriptorForRef(decl.TypeRef(), ctx)
		if program.Mode() != "advanced-chat" {
			p.reject("Conversation variables (@conversation) require @mode advanced-chat", decl)
		}
		if !conversationStoreSupported(descriptor) {
			p.reject("conversation variable type is not supported by Dify", decl)
		}
		if def := decl.Default(); def != nil {
			if !isLiteral(def) {
				p.reject("conversation variable defaults must be representable literals", def)
			} else if !defaultSupported(descriptor, def, ctx) {
				p.reject("conversation variable default cannot be represented by Dify", def)
			}
		}
	}
}

// reject mirrors _reject (:1316-1318): the node span (zero when unset).
func (p *TargetValidationPass) reject(message string, node ast.Node) {
	p.rejectAt(message, node.Span())
}

// rejectAt mirrors _reject_at (:1320-1322): emit TARGET_NOT_LOWERABLE with
// the span; a zero span leaves the location fields empty (Python None).
func (p *TargetValidationPass) rejectAt(message string, span ast.Span) {
	p.ctx.Emit(diag.TARGET_NOT_LOWERABLE, diag.Error, message, span)
}

// ── module helpers ────────────────────────────────────────────────────

// assignerRhs mirrors _assigner_rhs (:1324-1330).
func assignerRhs(node *ast.AssignStmt) ast.Expr {
	if node.Value() == nil {
		return nil
	}
	switch node.Operation() {
	case "+=", "-=", "*=", "/=":
		if binary, ok := node.Value().(*ast.BinaryExpr); ok {
			return binary.Right()
		}
	}
	return node.Value()
}

// arrayVariableInputSupported mirrors _array_variable_input_supported
// (:1332-1354).
func arrayVariableInputSupported(target *types.Descriptor, rhs ast.Expr, ctx *Context) bool {
	if target.Kind != types.Array || target.Elem == nil {
		return false
	}
	switch target.Elem.Kind {
	case types.String, types.Int, types.Float, types.Bool:
	default:
		return false
	}
	switch rhs.(type) {
	case *ast.IdentExpr, *ast.FieldAccessExpr:
	default:
		return false
	}
	root := rhs
	for {
		fa, ok := root.(*ast.FieldAccessExpr)
		if !ok {
			break
		}
		if fa.Obj() == nil {
			return false
		}
		root = fa.Obj()
	}
	rootIdent, ok := root.(*ast.IdentExpr)
	if !ok {
		return false
	}
	_, _, found := symbolDecl(ctx, rootIdent)
	return found
}

// descriptorForRef mirrors _descriptor_for_ref (:1356-1359):
// types.TypeRefToDescriptor against ctx.NamedTypes.
func descriptorForRef(ref ast.TypeRef, ctx *Context) *types.Descriptor {
	return types.TypeRefToDescriptor(ref, func(name string) *types.Descriptor {
		return ctx.NamedTypes[name]
	})
}

// operationForCall mirrors _operation_for_call (:1361-1365): a CallBuiltin
// target resolved through the target profile's operation registry.
func operationForCall(node *ast.CallExpr, ctx *Context) *OperationSpec {
	target, ok := callTarget(node, ctx)
	if !ok || target.Kind != binding.CallBuiltin {
		return nil
	}
	if ctx.Profile == nil || ctx.Profile.Operations == nil {
		return nil
	}
	return ctx.Profile.Operations.Lookup(target.Name)
}

// callTarget resolves the call's published binding target (Python
// ctx.call_targets.get(ctx.ast_id(node))).
func callTarget(node *ast.CallExpr, ctx *Context) (binding.CallTarget, bool) {
	id, ok := snapshotID(ctx, node)
	if !ok {
		return binding.CallTarget{}, false
	}
	return ctx.Bindings.CallTargets().Get(id)
}

// isNativeSelectorCall mirrors _is_native_selector_call (:1367-1371), with a
// coze-biz extension: a qualified capability call p.op({...})
// (TOOL-DIRECT-CALL-FORM, type_infer.go visitQualifiedCapabilityCall) is a
// native selector too — it lowers through the tool path with its own
// platform file contract, so a scalar File value inside its params map is
// legal (coze-biz file == URL string), never compiler-generated Code. The
// Dify reference has no capability imports, so this branch is unreachable
// there and the reference-surface parity is untouched.
func isNativeSelectorCall(node *ast.CallExpr, ctx *Context) bool {
	if target, ok := callTarget(node, ctx); ok && target.Kind == binding.CallStdlib {
		return true
	}
	if operationForCall(node, ctx) != nil {
		return true
	}
	return isQualifiedCapabilityCall(node, ctx)
}

// isQualifiedCapabilityCall mirrors the qualified-capability-call
// interception in the type pass (type_infer.go visitQualifiedCapabilityCall):
// p.op({...}) where p resolves to an import namespace symbol (capability
// provider). Builtin namespaces (json, std) fall through to normal function
// application (NamespaceBuiltin discriminant, I2).
func isQualifiedCapabilityCall(node *ast.CallExpr, ctx *Context) bool {
	fa, ok := node.Func().(*ast.FieldAccessExpr)
	if !ok {
		return false
	}
	obj, ok := fa.Obj().(*ast.IdentExpr)
	if !ok {
		return false
	}
	decl, _, ok := symbolDecl(ctx, obj)
	if !ok || decl.Kind != binding.SymbolNamespace {
		return false
	}
	return decl.NS == binding.NamespaceImport
}

// isNativeMaterializationCall mirrors _is_native_materialization_call
// (:1373-1378): a call has a non-Code lowering with its own file contract
// when it is a native selector call or a proven list-operator call.
func isNativeMaterializationCall(node *ast.CallExpr, ctx *Context, declInits map[binding.SymbolID]ast.Expr) bool {
	if isNativeSelectorCall(node, ctx) {
		return true
	}
	if _, ok := node.Func().(*ast.IdentExpr); ok {
		return canLowerListOperator(node, ctx, declInits)
	}
	return false
}

// isDirectSelectorExpr mirrors _is_direct_selector_expr (:1380-1381).
func isDirectSelectorExpr(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.IdentExpr, *ast.FieldAccessExpr:
		return true
	}
	return false
}

// isDirectConditionExpr mirrors _is_direct_condition_expr (:1383-1385).
func isDirectConditionExpr(expr ast.Expr, ctx *Context, declInits map[binding.SymbolID]ast.Expr) bool {
	if isDirectSelectorExpr(expr) {
		return true
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		return isNativeMaterializationCall(call, ctx, declInits)
	}
	return false
}

// isCompilerGeneratedCodeExpression mirrors
// _is_compiler_generated_code_expression (:1387-1392): expressions routed
// through compiler-owned Code materialization.
func isCompilerGeneratedCodeExpression(expr ast.Expr, ctx *Context, declInits map[binding.SymbolID]ast.Expr) bool {
	switch expr.(type) {
	case *ast.TemplateString, *ast.ForeignCodeExpr, *ast.OperationPolicyExpr:
		return false
	case *ast.CallExpr:
		return !isNativeMaterializationCall(expr.(*ast.CallExpr), ctx, declInits)
	}
	return true
}

// scalarFileSelectors mirrors _scalar_file_selectors (:1394-1424): find the
// scalar-file identifiers in a generated calculation expression. The Go walk
// mirrors the Python recursive walk: TemplateString prunes, CallExpr walks
// only its args (native-materialization calls prune entirely), a
// FieldAccessExpr over a file object prunes (the dedicated field-access
// validator owns that diagnostic), a FieldAccessExpr over a non-file object
// recurses into the obj chain (R1 M2; Python's is_dataclass fall-through),
// and non-expression dataclass fields are never entered (mirroring
// `isinstance(value, Expr)` gating).
func scalarFileSelectors(expr ast.Expr, ctx *Context, declInits map[binding.SymbolID]ast.Expr) []ast.Expr {
	var found []ast.Expr
	seen := map[ast.NodeID]bool{}
	var walk func(value ast.Expr)
	walk = func(value ast.Expr) {
		switch v := value.(type) {
		case *ast.TemplateString:
			return
		case *ast.CallExpr:
			if isNativeMaterializationCall(v, ctx, declInits) {
				return
			}
			for _, arg := range v.Args() {
				walk(arg)
			}
			return
		case *ast.FieldAccessExpr:
			var objType *types.Descriptor
			if v.Obj() != nil {
				objType = exprTypeOf(ctx, v.Obj())
			}
			if objType != nil && types.UnwrapOptional(objType).Kind == types.File {
				return
			}
			walkScalarFileExprChildren(v, walk)
			return
		case *ast.IdentExpr:
			descriptor := exprTypeOf(ctx, v)
			if descriptor != nil && types.UnwrapOptional(descriptor).Kind == types.File {
				if !seen[v.NodeID()] {
					seen[v.NodeID()] = true
					found = append(found, v)
				}
			}
			return
		default:
			walkScalarFileExprChildren(v, walk)
		}
	}
	walk(expr)
	return found
}

// walkScalarFileExprChildren recurses into the Expr children of one
// expression node, mirroring the Python `is_dataclass` field walk restricted
// to Expr values (and slices of them). Expression-only child shapes are
// enumerated; statement/type-ref/leaf children are never entered.
//
// R1 M2: FieldAccessExpr recurses into the obj side. Python's walk checks a
// FieldAccessExpr's obj type (FILE obj → return, owned by the dedicated
// field-access validator) and then, for a non-file obj, falls through to the
// is_dataclass walk of obj (the field name is a plain string, not an Expr).
// The Go top-level walk performs the same obj-type check before reaching
// here; this case reproduces the fall-through so a nested obj chain (e.g.
// rec.inner.field) is inspected for scalar-file leaves.

func walkScalarFileExprChildren(value ast.Expr, walk func(ast.Expr)) {
	switch v := value.(type) {
	case *ast.ArrayLit:
		for _, item := range v.Elements() {
			walk(item)
		}
	case *ast.MapLit:
		for _, entry := range v.Entries() {
			walk(entry.Key)
			walk(entry.Value)
		}
	case *ast.IndexExpr:
		walk(v.Obj())
		walk(v.Index())
	case *ast.OperationPolicyExpr:
		// Python walks only the operation field: the policies are plain
		// dataclasses (not Expr values), so their values are never entered.
		walk(v.Operation())
	case *ast.UnaryExpr:
		walk(v.Operand())
	case *ast.BinaryExpr:
		walk(v.Left())
		walk(v.Right())
	case *ast.TernaryExpr:
		walk(v.Condition())
		walk(v.ThenVal())
		walk(v.ElseVal())
	case *ast.LambdaExpr:
		walk(v.Body())
	case *ast.ForExpr, *ast.ParallelForExpr:
		// Python walks only the iterable (var_type is a TypeRef, body is a
		// Block — neither is an Expr, so they are never entered).
		switch f := v.(type) {
		case *ast.ForExpr:
			walk(f.Iterable())
		case *ast.ParallelForExpr:
			walk(f.Iterable())
		}
	case *ast.FieldAccessExpr:
		if v.Obj() != nil {
			walk(v.Obj())
		}
	case *ast.ForeignCodeExpr:
		// Parallel branches and Code input bindings are not Expr values in
		// Python; nothing is entered.
	default:
		// leaf expressions (literals, identifiers, templates, ...)
	}
}

// mapValue mirrors _map_value (:1426-1430): string-literal keyed lookup.
func mapValue(node *ast.MapLit, key string) ast.Expr {
	for _, entry := range node.Entries() {
		// 静态键 = 标识符名 或 字符串值（唯一判据 ast.StaticMapKey，2026-09-14）：
		// 只认字符串会让 `{ vision: true }` 静默查不到，只认标识符会让反编译
		// 产物形态（字符串键）静默查不到——两个方向都是静默丢配置。
		if name, ok := ast.StaticMapKey(entry.Key); ok && name == key {
			return entry.Value
		}
	}
	return nil
}

// staticMapKey mirrors _static_map_key (:1432-1435): an identifier or string
// literal key; the bool reports whether the key is static. The rule itself has
// exactly one implementation, ast.StaticMapKey (2026-09-14) — this is a local
// alias so the pass reads like its Python counterpart, not a second rule.
func staticMapKey(expr ast.Expr) (string, bool) {
	return ast.StaticMapKey(expr)
}

// fileDescriptor mirrors _file_descriptor (:1453-1457).
func fileDescriptor(d *types.Descriptor) *types.Descriptor {
	if d == nil || d.Kind != types.File {
		return nil
	}
	return d
}

// isScalarFile mirrors _is_scalar_file (:1459-1460).
func isScalarFile(d *types.Descriptor) bool {
	return fileDescriptor(d) != nil
}

// isSelectorExpr mirrors _is_selector_expr (:1462-1463).
func isSelectorExpr(expr ast.Expr) bool {
	return isDirectSelectorExpr(expr)
}

// extractableFileDescriptor mirrors _extractable_file_descriptor
// (:1465-1474). Go's FileConstraints is a value struct, so the Python
// "constraints is None" branch collapses into the empty-extensions check —
// the observable result is identical (bare `file` has no extensions).
func extractableFileDescriptor(d *types.Descriptor) bool {
	fd := fileDescriptor(d)
	if fd == nil {
		return false
	}
	if len(fd.File.Extensions) == 0 {
		return false
	}
	for _, ext := range fd.File.Extensions {
		if !extractableExtensions[ext] {
			return false
		}
	}
	for _, t := range fd.File.Types {
		if t == "image" || t == "audio" || t == "video" {
			return false
		}
	}
	return true
}

// conversationStoreSupported mirrors _conversation_store_supported
// (:1476-1484).
func conversationStoreSupported(d *types.Descriptor) bool {
	if d == nil {
		return false
	}
	switch d.Kind {
	case types.String, types.Int, types.Float, types.Bool:
		return true
	case types.Array:
		return d.Elem != nil && conversationStoreSupported(d.Elem)
	case types.Map:
		return d.Key != nil && d.Key.Kind == types.String && d.Val != nil && conversationStoreSupported(d.Val)
	}
	return false
}

// isLiteral mirrors _is_literal (:1486-1493). NullLit is deliberately not a
// literal (Python omits it from the tuple).
func isLiteral(value ast.Expr) bool {
	switch v := value.(type) {
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		return true
	case *ast.ArrayLit:
		for _, item := range v.Elements() {
			if !isLiteral(item) {
				return false
			}
		}
		return true
	case *ast.MapLit:
		for _, entry := range v.Entries() {
			if !isLiteral(entry.Key) || !isLiteral(entry.Value) {
				return false
			}
		}
		return true
	}
	return false
}

// defaultSupported mirrors _default_supported (:1495-1516).
func defaultSupported(descriptor *types.Descriptor, value ast.Expr, ctx *Context) bool {
	actual := exprTypeOf(ctx, value)
	if actual == nil || !types.IsAssignable(actual, descriptor) {
		return false
	}
	switch descriptor.Kind {
	case types.String:
		_, ok := value.(*ast.StringLit)
		return ok
	case types.Int:
		_, ok := value.(*ast.IntLit)
		return ok
	case types.Float:
		_, ok := value.(*ast.FloatLit)
		return ok
	case types.Bool:
		_, ok := value.(*ast.BoolLit)
		return ok
	case types.Array:
		if descriptor.Elem == nil {
			return false
		}
		arr, ok := value.(*ast.ArrayLit)
		if !ok {
			return false
		}
		for _, item := range arr.Elements() {
			if !defaultSupported(descriptor.Elem, item, ctx) {
				return false
			}
		}
		return true
	case types.Map:
		if descriptor.Key == nil || descriptor.Val == nil {
			return false
		}
		m, ok := value.(*ast.MapLit)
		if !ok {
			return false
		}
		for _, entry := range m.Entries() {
			if !defaultSupported(descriptor.Key, entry.Key, ctx) ||
				!defaultSupported(descriptor.Val, entry.Value, ctx) {
				return false
			}
		}
		return true
	}
	return false
}

// ── small shared helpers ───────────────────────────────────────────────

// exprTypeOf resolves an expression's inferred type (Python
// ctx.expr_types.get(ctx.ast_id(n))).
func exprTypeOf(ctx *Context, n ast.Node) *types.Descriptor {
	id, ok := snapshotID(ctx, n)
	if !ok {
		return nil
	}
	return ctx.ExprTypes[id]
}

// isNumericLiteral reports whether expr is an int or float literal.
func isNumericLiteral(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.IntLit, *ast.FloatLit:
		return true
	}
	return false
}

// isScalarLiteral reports whether expr is one of the scalar literal kinds
// (Python isinstance(rhs, (StringLit, IntLit, FloatLit, BoolLit))).
func isScalarLiteral(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		return true
	}
	return false
}

// containsNodeID reports whether ids contains id.
func containsNodeID(ids []binding.AstNodeID, id binding.AstNodeID) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

// dedupeStrings returns the strings in values that satisfy keep, without
// duplicates (the Go form of the Python set difference used by
// _validate_file_constraints :226-245).
func dedupeStrings(values []string, keep func(string) bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		if keep(v) && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// sortedExtraKeys returns the sorted keys of values not allowed by the
// allowed set (Python `sorted(values.keys() - allowed)`).
func sortedExtraKeys(values map[string]ast.Expr, allowed map[string]bool) []string {
	var extra []string
	for key := range values {
		if !allowed[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	return extra
}
