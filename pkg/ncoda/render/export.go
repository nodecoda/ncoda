package render

import "github.com/nodecoda/ncoda/pkg/ncoda/typesystem"

// 这些 helper 原先只在 package decompile 内可见；渲染器移入开放层后，
// 闭源侧仍有调用点，故在此导出（实现不变）。

func NcodaTypeName(ti *typesystem.TypeInfo) string { return ncodaTypeName(ti) }

func NcodaTypeNameWith(ti *typesystem.TypeInfo, reg func(*typesystem.TypeInfo) (string, bool)) string {
	return ncodaTypeNameWith(ti, reg)
}

func IsValidTypeFieldName(name string) bool { return isValidTypeFieldName(name) }

func RenderTemplateClause(text string, bindings []TemplateSlot) string {
	return renderTemplateClause(text, bindings)
}

func IsEmptyListExpr(e Expr) bool { return isEmptyListExpr(e) }

func RenderParallelForConfig(concurrency int, onError string) string {
	return renderParallelForConfig(concurrency, onError)
}

func QuoteNCoda(s string) string { return quoteNCoda(s) }

func RenderUnknownNotes(notes []string) string { return renderUnknownNotes(notes) }

func RenderMergeNotes(notes []string) string { return renderMergeNotes(notes) }

func RenderOperationPolicySuffix(v *OperationPolicyExpr) string {
	return renderOperationPolicySuffix(v)
}

func FileTypeText(ti *typesystem.TypeInfo) string { return fileTypeText(ti) }

func FileFamilyNCodaToken(ti *typesystem.TypeInfo) string { return fileFamilyNCodaToken(ti) }

func FileSubTypeFromNCodaToken(text string) (typesystem.FileSubType, bool) {
	return fileSubTypeFromNCodaToken(text)
}

func FileSubTypeOf(ti *typesystem.TypeInfo) typesystem.FileSubType { return fileSubTypeOf(ti) }

func FileTokenOfFamily(ft typesystem.FileSubType) string { return fileTokenOfFamily(ft) }
