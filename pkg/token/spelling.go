package token

import "strings"

// spelling.go —— 「名字 → 源码拼写」的唯一落点（`$` 转义的**逆**）。
//
// 事实：词法层把 `$` 当**纯转义**——`$code` 与 `code` 产出**同一个** IDENTIFIER
// （lexeme = `code`，`pkg/lexer/lexer.go:439` scanDollarEscape）。于是「一个
// 名字」在源码里有**两种拼写**，而**哪一个合法取决于这个名字是不是保留字**：
//
//	名字      合法源码拼写
//	txt       `txt`（也是 `$txt`，但那是噪音）
//	code      **只有** `$code` —— 裸 `code` 词法还原成 KW_CODE，任何按 IDENTIFIER
//	          读的位置都读不到它
//
// 反编译/发射侧拿到的是**名字**（族名、端口名、注册表 token），不是拼写；它必须
// 用本函数把名字折回**唯一合法拼写**，否则会发射出**自己都编译不过**的 ncoda
// （实测：raising 发射裸 `file<code>` ⇒ 该文本再编译 SYNTAX_ERROR）。
//
// 为什么放在 token 包：这条规则只依赖「是不是保留字」，是**词法**事实；放在
// passes/tool/各平台里各写一遍就是同一规则的多份定义（本仓「一处定义」）。

// EscapedSpelling 返回名字 `name` 在 ncoda 源码里的**唯一合法拼写**：
// `name` 是保留字 ⇒ `"$" + name`；否则原样返回。
//
// 例：EscapedSpelling("code") == "$code"（code 是关键字）；
//
//	EscapedSpelling("txt")  == "txt"。
func EscapedSpelling(name string) string {
	if IsKeyword(name) {
		return "$" + name
	}
	return name
}

// NameFromSpelling 是 EscapedSpelling 的逆：剥掉源码拼写的前导 `$`。
// 非转义拼写原样返回（幂等）。
//
// 例：NameFromSpelling("$code") == "code"；NameFromSpelling("txt") == "txt"。
func NameFromSpelling(spelling string) string {
	return strings.TrimPrefix(spelling, "$")
}
