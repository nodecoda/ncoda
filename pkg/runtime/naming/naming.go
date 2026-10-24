// Package naming — 运行时侧的名字规则（库/函数/物理包名）。
//
// 规则本身是语言层的：源码只写 `库.函数`；物理包名（纯数字、plugin_<数字>）
// 不是库。具体平台的前缀由适配器在绑定表里声明，本包不拼写任何平台名。
package naming

import (
	"regexp"
	"strings"
)

// SymbolRE 是 `库.函数` 的语法。
var SymbolRE = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-zA-Z_][a-zA-Z0-9_]*$`)

// physicalPackageRE 覆盖语料里出现过的物理包名形态：plugin_<digits> / 纯数字。
// 这类名字描述的是**平台包**，不是库；它们只能出现在绑定表里。
var physicalPackageRE = regexp.MustCompile(`^(?:plugin_\d+|\d+)$`)

// IsPlatformName 报告 name 是否是物理包名（平台包 / 平台前缀形态）。
// 空串、合法库名 → false。
func IsPlatformName(name string) bool {
	return physicalPackageRE.MatchString(name)
}

// IsSymbol 报告 s 是否是合法符号（库.函数）。
// 语法合法还不够：库名不得是物理包名。
func IsSymbol(s string) bool {
	if !SymbolRE.MatchString(s) {
		return false
	}
	library := s[:strings.IndexByte(s, '.')]
	return !IsPlatformName(library)
}
