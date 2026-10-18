package render

// file 族的 ncoda 文本投影（**A2/A4 的修法实现**；规范形正本 =
// docs/five-step/spec/file-family-canonical-form.md，覆盖登记 =
// docs/coze-biz-schema-audit.md §6.9 F-AXES-1）。
//
// 规则（四句话）：
//  1. **值类型 = kind `file`**；**族 = 端口 / 参数面的约束**，在 ncoda 侧唯一的书写面
//     是 `file<token>`（语言侧族词汇表 = `pkg/passes/target_validation.go:111`
//     `supportedFileTypes`，**机械枚举出口** `passes.FileTypeTokens()`；2026-09-21
//     语言级扩展后 = 11 token，与平台名册同构）。
//  2. **raising 必须把平台声明的族投影回声明位**。不投影 = 族经一次往返即消失：
//     实测 `file<image>` → c0 `type: image` → r1 `file d` → c1 `type: file`
//     （`ARTIFACT_DRIFT/PARAM`），语料侧 `.yaml` 顶层族名 1650 行全数丢失。
//  3. **只投影语言已声明的族词汇表内的族**，判据 = `pkg/passes.FileTypeTokens()`
//     机械枚举的闭集（**闭集驱动，不是平台驱动**：平台新增一族而语言没有 ⇒ 不投影，
//     发明语言侧无定义的 token 等于自造语言）。**2026-09-21 语言级扩展（用户拍板 B
//     「与平台名册同构」+ A「`code` 补入」）**后闭集 = 12 token，平台 12 族**全部
//     可投影**（两侧只剩 `custom` 这个语言独有的半边）。
//     注意**名字 ≠ 拼写**：`code` 是 ncoda 关键字 ⇒ 发射它的源码拼写是 `$code`
//     （`token.EscapedSpelling`，`$` 是纯词法转义）。裸 `file<code>` 是**语法错误**，
//     所以「发射时忘了折拼写」的后果是产出**自己编译不过**的 ncoda —— 该陷阱由
//     `file_token_roster_contract_test.go` ④ 端到端拦下。
//  4. 本文件是族 → token 的**唯一落点**：类型文本（`fileTypeText`）、形状键
//     （`typeShapeStr`）与文本反解（`typeInfoForName`）都走它（一处定义）；
//     拼写对应本身（`doc` → `document`）取自 values 名册。
//
// 平台族名 → ncoda token 的对应**不在此文件**：唯一映射处 =
// `platforms/cozestudio/values/platform_type_table.go`（`platformTypeNames` 的
// `ncodaToken` 列，`doc` → `document` 这条例外别名只写那里）。`default`
// （= 无族）= 裸 `file`，不写轴。本文件只做「闭集校验 + 文本投影」。

import (
	"sync"

	"github.com/nodecoda/ncoda/pkg/ncoda/typesystem"
	"github.com/nodecoda/ncoda/pkg/ncoda/values"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/token"
)

// languageFileTokenSet 语言侧 `file<…>` types 轴闭集（唯一来源 = internal/passes，
// 机械枚举出口 FileTypeTokens()）。投影**只对闭集内的 token 发射** —— 平台有、
// 语言没有的族一律不投影（发明语言侧无定义的 token = 自造语言，需用户审核）。
// 求值一次即可（闭集是编译期常量，`sync.OnceValue` 与 `token.Keywords()` 同口径）。
var languageFileTokenSet = sync.OnceValue(func() map[string]bool {
	toks := passes.FileTypeTokens()
	out := make(map[string]bool, len(toks))
	for _, tok := range toks {
		out[tok] = true
	}
	return out
})

// languageFileTokens 闭集的只读视图（调用方不得修改）。
func languageFileTokens() map[string]bool { return languageFileTokenSet() }

// fileTokenOfFamily 族（平台拼写）→ ncoda token；返回 "" = 不投影。
//
// 映射 = **恒等**，两个例外：
//   - `doc`（平台拼写）→ `document`（ncoda 单一拼写，别名归一）；
//   - `default` = 无族 ⇒ 裸 `file`（不写轴）。
//
// 其余族是否投影**由语言闭集决定**，不由平台决定：闭集外 ⇒ ""（发明语言侧无定义的
// token = 自造语言）。2026-09-21 语言级扩展（B 同构 + A 补 `code`）后闭集 = 12 个
// token，平台 12 族**全部可投影**（`doc`→`document` 归一；`code` 的源码拼写是
// `$code`，见 `fileTypeText`）。
func fileTokenOfFamily(ft typesystem.FileSubType) string {
	if ft == typesystem.FileSubTypeDefault {
		return ""
	}
	// 平台拼写 → ncoda token 的**唯一**映射处 = values 名册（`doc` → `document`
	// 这条例外别名就在那张表里，不再在 raising/lowering 各写一遍）。
	tok, ok := values.FileTokenForPlatformSpelling(string(ft))
	if !ok {
		return "" // 平台独有族（`code`）：语言侧无此 token
	}
	if !languageFileTokens()[tok] {
		// 防御：名册与语言闭集脱钩时不得发射（同构判据会先红）。发射闭集外 token
		// = 产出**不可编译**的 ncoda（自造语言），比丢族严重。
		return ""
	}
	return tok
}

// fileFamilyNCodaToken 平台 file 族 → ncoda `file<…>` token；返回 "" = 不投影。
func fileFamilyNCodaToken(ti *typesystem.TypeInfo) string {
	if ti == nil || ti.FileType == nil {
		return ""
	}
	return fileTokenOfFamily(*ti.FileType)
}

// fileSubTypeFromNCodaToken ncoda token（名字或源码拼写）→ 平台族
// （`fileFamilyNCodaToken` 的逆）。返回 ok=false 的场景**两种**，调用方一律回退
// 「裸 file」，不发明族：
//   - 名字 ∉ 语言闭集（判据面 = 语言，不是平台）；
//   - 名字 ∈ 语言闭集但平台无此族拼写（`custom`：语言有、平台没有 ⇒ 放宽，不发明平台拼写）。
func fileSubTypeFromNCodaToken(text string) (typesystem.FileSubType, bool) {
	// 入参可能是**源码拼写**（`$code`，来自类型文本）而不是名字 —— 词法把 `$` 当纯
	// 转义，所以先折回名字再查表（`$` 规则只写在 `internal/token` 一处）。
	name := token.NameFromSpelling(text)
	if !languageFileTokens()[name] {
		return "", false
	}
	spelling, ok := values.PlatformSpellingForFileToken(name)
	if !ok {
		return "", false // 平台无此族拼写（custom）：放宽到裸 file，不发明拼写
	}
	return typesystem.FileSubType(spelling), true
}

// fileSubTypeOf 取族的平台值；无族（nil / 未声明）⇒ FileSubTypeDefault。
// 用途 = 需要「原样搬家族」的**声明位**改写点（如混合 file/string 汇聚的聚合类型
// 统一）——回落 Default 是「不发明族」的兜底，不是权威。
func fileSubTypeOf(ti *typesystem.TypeInfo) typesystem.FileSubType {
	if ti != nil && ti.FileType != nil {
		return *ti.FileType
	}
	return typesystem.FileSubTypeDefault
}

// fileTypeText file 类型的 ncoda 文本：有族 ⇒ `file<token>`，无族 / 表外族 ⇒ `file`。
//
// 发射的是**源码拼写**（`token.EscapedSpelling`）：`code` 是关键字 ⇒ 发射 `file<$code>`
// （裸 `file<code>` 语法错误，发出去的作品自己编译不过）。非关键字族名（`txt`…）原样。
func fileTypeText(ti *typesystem.TypeInfo) string {
	if tok := fileFamilyNCodaToken(ti); tok != "" {
		return "file<" + token.EscapedSpelling(tok) + ">"
	}
	return "file"
}
