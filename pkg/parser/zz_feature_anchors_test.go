package parser

// 语言特性矩阵锚点声明（docs/LANGUAGE-FEATURE-MATRIX.md）——IR 层 $ 转义特性。
//
//@feature dollar-escape — $ 关键字转义词法/解析：词法剥 '$' 还原为普通标识符，
// parsePrimary 打 Escaped 标记，binder 拒绝 escaped 关键字解析到语言内建
// （$file(...) 静默别名漏洞）。实际断言见 zz_dollar_min_test.go
// TestDollarEscapeMarksEscapedIdent 与 internal/compiler/zz_dollar_escape_test.go。
//
// 本文件是特性锚点声明（空测试），锚点语义由引用测试承载。
import "testing"

func TestDollarEscapeFeatureAnchor(t *testing.T) {}
