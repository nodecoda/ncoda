package values

import (
	"sort"
	"strconv"

	"github.com/nodecoda/ncoda/pkg/ncoda/typesystem"
)

// platform_type_table.go —— 平台类型名词表（**唯一映射处**，2026-09-21 A3 收口）。
//
// 事实：coze 平台把「文件家族」编码成 canvas `{type: "string", assistType: N}`
// （canvas **没有** file 这一 VariableType），而 yaml 通道用**类型名**表达同一族
// （`type: image` / `type: voice` / `type: txt` …）。于是「平台类型名 → 承载」这张
// 表在 ≥5 处各写了一遍（raising 入口 / 承载归一 / 绑定位 / 声明位 / lowering 的
// merge 类型同步，其中一处是字面量硬编码镜像），且名册只覆盖 6/12 —— 漏掉的名字
// 在**声明位硬崩**（`unsupported variable type`，parser/canvas.go），在**绑定位
// 静默降级 string**（本包 BlockInputFromDict）。两处两种行为、同一事实。
//
// 此处收成一张全表（12 file 族 + time），其余各处**派生**：
//   - AssistTypeForPlatformTypeName  → 族名 → canvas AssistType（raising 入口）
//   - PlatformTypeToCanvasVarType    → 族名/原生名 → canvas VariableType（承载）
//   - PlatformSpellingForFileToken / FileTokenForPlatformSpelling
//     → ncoda `file<…>` token ↔ 平台族拼写（**双向同一行**，lowering 与 raising 共用）
//
// 权威阶梯：① ncoda 语义（族 = `file<token>` 的**约束轴**，不是类型身份，见
// docs/five-step/spec/file-family-canonical-form.md A2/A5）；② 本表 = 平台**拼写**
// 的落点；③ 语料实证只决定拼写，不决定该不该有这一族。

// platformTypeEntry 平台类型名的一行。
//
// **一行装两个方向**（2026-09-21 语言级扩展 B「与平台名册同构」）：`spelling`（map 键）
// 是平台拼写，`ncodaToken` 是语言侧 `file<…>` 的 token。两列必须同行 —— 拆成两张表
// 就是「同一事实两处定义」，正是 A3 收口要消灭的形态（`document` ↔ `doc` 这条例外
// 别名尤其只许有一处）。
type platformTypeEntry struct {
	assistType int
	// ncodaToken 语言侧 `file<…>` token；**"" = 语言侧无此拼写**。三种情况共用空值：
	//   - 无族（`file` = 裸文件、`time` = 非 file 族的独立通道轴）；
	//   - 其余情况由 `file_token_roster_contract_test.go` 逐个登记。
	// 本列是**名字**（`code`），不是源码**拼写**（`$code`）：`$` 是纯词法转义
	// （`pkg/token.EscapedSpelling`），发射侧折回拼写，两侧同一处规则。
	ncodaToken string
	// carrierUnmapped 该平台类型名**在 canvas 承载闭集里没有对应物**（`time`）：
	// 平台把它建模成 DataTypeTime（另一条通道轴），而 canvas VariableType 闭集是
	// {string,integer,float,boolean,object,list} ⇒ 归一成 string 是**有损**的（轴丢）。
	// 无损耗承载的名册内拼写（12 file 族 + `file`）不得置位 —— 报它们是噪音，
	// 会把「报账」训练成背景噪声。置位的行由 PlatformTypeToCanvasVarType 返回
	// ok=false ⇒ 调用方必须报账（RS-TYP-002），与名册外拼写同一条通路。
	carrierUnmapped bool
}

// platformTypeNames 平台类型名 → 该行。键 = 平台 yaml / canvas 通道实测拼写
// （注意 `doc` 不是 `document`）。覆盖 values.go 里全部 12 个 file 族常量 + `time`
// （非 file 族，独立轴）。
var platformTypeNames = map[string]platformTypeEntry{
	"file":  {assistType: AssistTypeDefault},
	"image": {assistType: AssistTypeImage, ncodaToken: "image"},
	"doc":   {assistType: AssistTypeDoc, ncodaToken: "document"},
	"code":  {assistType: AssistTypeCode, ncodaToken: "code"},
	"ppt":   {assistType: AssistTypePPT, ncodaToken: "ppt"},
	"txt":   {assistType: AssistTypeTXT, ncodaToken: "txt"},
	"excel": {assistType: AssistTypeExcel, ncodaToken: "excel"},
	"audio": {assistType: AssistTypeAudio, ncodaToken: "audio"},
	"zip":   {assistType: AssistTypeZip, ncodaToken: "zip"},
	"video": {assistType: AssistTypeVideo, ncodaToken: "video"},
	"svg":   {assistType: AssistTypeSVG, ncodaToken: "svg"},
	"voice": {assistType: AssistTypeVoice, ncodaToken: "voice"},
	// `time`：名册内（yaml 通道确有这一拼写、canvas 有对应 AssistType），但**承载
	// 闭集里没有对应物** ⇒ 降级有损 ⇒ carrierUnmapped（F-AXES-5，2026-09-23 已裁
	// 「不升格、降级 + 报账」，2026-09-29 兑现）。
	"time": {assistType: AssistTypeTime, carrierUnmapped: true},
}

// fileSpellingByToken 上表的 ncoda token → 平台拼写**反查**（由 platformTypeNames
// 派生，不是第二张表）。构建时校验单射：两行映同一 token ⇒ 反查歧义，是表自身的
// 缺陷（不可能来自用户输入）⇒ 直接 panic，不带病运行。
var fileSpellingByToken = func() map[string]string {
	out := make(map[string]string, len(platformTypeNames))
	for spelling, e := range platformTypeNames {
		if e.ncodaToken == "" {
			continue
		}
		if prev, dup := out[e.ncodaToken]; dup {
			panic("values: platform file token " + e.ncodaToken +
				" claimed by both `" + prev + "` and `" + spelling + "`")
		}
		out[e.ncodaToken] = spelling
	}
	return out
}()

// assistSpellingByAssistType 上表的 AssistType → 平台拼写**反查**（由
// platformTypeNames 派生，不是第二张表）。构建时校验单射：两行映同一族号 ⇒
// 反查歧义 ⇒ panic（表自身缺陷，不可能来自用户输入），不带病运行。
var assistSpellingByAssistType = func() map[int]string {
	out := make(map[int]string, len(platformTypeNames))
	for spelling, e := range platformTypeNames {
		if prev, dup := out[e.assistType]; dup {
			panic("values: platform spelling for assistType " + strconv.Itoa(e.assistType) +
				" claimed by both `" + prev + "` and `" + spelling + "`")
		}
		out[e.assistType] = spelling
	}
	return out
}()

// PlatformSpellingForAssistType canvas AssistType → 平台族拼写（**raising 方向**，
// 与 AssistTypeForPlatformTypeName 互逆）。ok=false = 族号 ∉ 名册（平台新增 / 写坏）
// ⇒ 调用方不得发明拼写。
//
// 用途 = canvas 通道的声明位回读：`{type: string, assistType: 2}` ⇒ `image`
// （`type` 是 canvas 原生名、族在副类型上的形态，见 subflowInputTypeText）。
func PlatformSpellingForAssistType(n int) (string, bool) {
	spelling, ok := assistSpellingByAssistType[n]
	return spelling, ok
}

// AssistTypeForPlatformTypeName 平台类型名 → canvas AssistType（**file 族**）。
//
// `time` 不是 file 族（canvas 把它建模成 DataTypeTime，是另一条通道类型轴）⇒
// 返回 (AssistTypeNotSet, false)：调用方据此走降级路径，不得把它当 file 恢复
// （该轴的处理登记在 F-AXES-5）。
func AssistTypeForPlatformTypeName(name string) (int, bool) {
	e, ok := platformTypeNames[name]
	if !ok || e.assistType == AssistTypeTime {
		return AssistTypeNotSet, false
	}
	return e.assistType, true
}

// IsPlatformFileFamilyName 平台拼写是否是 file 族名（AssistTypeForPlatformTypeName
// 的判据面；lowering 侧的成员判断与 raising 侧同表，杜绝两处名册漂移）。
func IsPlatformFileFamilyName(name string) bool {
	_, ok := AssistTypeForPlatformTypeName(name)
	return ok
}

// PlatformSpellingForFileToken ncoda `file<token>` → 平台族拼写（**lowering 方向**）。
// ok=false = 平台没有这一族的拼写（`custom`：语言有、平台无 ⇒ 调用方放宽到裸
// `file`，**不发明拼写**）。
func PlatformSpellingForFileToken(token string) (string, bool) {
	spelling, ok := fileSpellingByToken[token]
	return spelling, ok
}

// FileTokenForPlatformSpelling 平台族拼写 → ncoda `file<token>`（**raising 方向**）。
// ok=false = 语言侧无此 token（平台独有族 `code`，或 `file`/`time` 这类无族行）
// ⇒ 调用方不投影（保持裸 `file`）。别名 `doc` → `document` 就在本表内（**唯一**归一
// 点），两侧不再各写一遍。
func FileTokenForPlatformSpelling(spelling string) (string, bool) {
	e, ok := platformTypeNames[spelling]
	if !ok || e.ncodaToken == "" {
		return "", false
	}
	return e.ncodaToken, true
}

// FileTokenRoster 返回「平台名册里**有** ncoda 拼写的 token」快照（升序）。
//
// 它是语言闭集（`pkg/passes.FileTypeTokens()`）的**像**：语言闭集 = 本集合 ∪
// 无平台拼写的 token（`custom`）。两者同构这条事实由
// `platforms/cozebiz/decompile/file_token_roster_contract_test.go` 判据化。
func FileTokenRoster() []string {
	out := make([]string, 0, len(fileSpellingByToken))
	for tok := range fileSpellingByToken {
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

// PlatformTypeToCanvasVarType 平台类型名 → canvas VariableType（**全函数**）。
//
// 值域 ⊆ canvas 闭集 {string, integer, float, boolean, object, list}：
//   - 平台名册内**承载无损**（12 file 族 + `file`）→ ("string", true)：canvas 无 file
//     类型，族由 assistType 承载（可逆）；
//   - canvas 闭集内的原生名 → 原样，true；
//   - **名册外拼写** → ("string", false)；
//   - **名册内但承载有损**（`time`：canvas 闭集无对应物，轴丢）→ ("string", false)。
//
// 后两类**殊途同归**：降级到承载类型，**ok=false ⇒ 调用方必须报账**（RS-TYP-002）。
// 「在名册里」不等于「承载无损」—— 修前把这两件事混为一谈，`time` 因此在 322 个
// yaml 导出里静默降级（撞「失配 ≠ 静默丢弃」）。判据 =
// platforms/cozebiz/decompile/channel_type_vocabulary_contract_test.go 的名册列
// `carrierUnmapped`（两个方向都判：有损必报、无损不得报）。
//
// 为什么必须是全函数：旧实现 default 直接返回名字本身 ⇒ 名册外拼写被当成 canvas
// VariableType 塞进闭集检查 ⇒ **整份平台导出读不了**（违反反编译器「读任意平台
// 导出」的承诺）。降级是许可的（覆盖承诺：平台特性表达不了 ⇒ 采用平台默认值），
// 静默不是 —— 故 ok 必须被消费。
func PlatformTypeToCanvasVarType(name string) (canvasType string, known bool) {
	if e, ok := platformTypeNames[name]; ok {
		return string(typesystem.VariableTypeString), !e.carrierUnmapped
	}
	switch typesystem.VariableType(name) {
	case typesystem.VariableTypeString, typesystem.VariableTypeInteger,
		typesystem.VariableTypeFloat, typesystem.VariableTypeBoolean,
		typesystem.VariableTypeObject, typesystem.VariableTypeList:
		return name, true
	}
	return string(typesystem.VariableTypeString), false
}

// PlatformTypeNameRoster 返回名册的确定性快照（测试/普查用；调用方不得修改返回的 map）。
func PlatformTypeNameRoster() map[string]int {
	out := make(map[string]int, len(platformTypeNames))
	for k, e := range platformTypeNames {
		out[k] = e.assistType
	}
	return out
}
