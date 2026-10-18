package pyast

// lexer.go — 反向子集 lexer（2026-09-02，P3）。与序列化器 quoteString /
// serialize.go 严格镜像：把编译器产出的 python 子集字节（pkg/ir/
// testdata/golden/*.py = 契约字节）还原为 token 流。字符串转义族解码
// （\" \\ \b \f \n \r \t \uXXXX + surrogate pair）与 quoteString 一一对应；
// 未知转义 / 非法字节一律报错（子集外 = 不反向、透传，封闭契约 §1.1）。

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TokenType 是词法单元类型。
type TokenType int

const (
	TEOF TokenType = iota
	TName
	TNumber
	TString
	TNewline
	TOp    // + - * / // % == != < <= > >= 与 ->（: 单独）
	TPunct // ( ) [ ] { } , : .
	TArrow // ->（由 OP_ 组合,这里单独出）
)

// Token 是一个词法单元。
type Token struct {
	Type    TokenType
	Text    string  // 原始文本（name / op / punct）
	Str     string  // TString：解码后的 Go 字符串值
	Int     int64   // TNumber：整数值（IsFloat=false）
	Float   float64 // TNumber：浮点值
	IsFloat bool    // TNumber：是否浮点
	Pos     int     // 字节偏移（错误定位）
}

// Lex 把 python 子集源码切分为 token 流（不含换行 token；parser 按行结构
// 处理缩进——子集模块 body 缩进固定 4 空格，由 parser 负责）。
func Lex(src string) ([]Token, error) {
	var toks []Token
	i := 0
	n := len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '\n':
			toks = append(toks, Token{Type: TNewline, Text: "\n", Pos: i})
			i++
		case c == '#':
			// 注释到行尾（marker 行 # ncoda:shim 1 在模块头；正文无注释）
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'':
			s, ni, err := lexString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, Token{Type: TString, Str: s, Pos: i})
			i = ni
		case c >= '0' && c <= '9':
			tok, ni, err := lexNumber(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tok)
			i = ni
		case isNameStart(c):
			j := i + 1
			for j < n && isNamePart(src[j]) {
				j++
			}
			toks = append(toks, Token{Type: TName, Text: src[i:j], Pos: i})
			i = j
		default:
			tok, ni, ok := lexOp(src, i)
			if !ok {
				return nil, fmt.Errorf("pyast: unexpected byte %q at offset %d", c, i)
			}
			toks = append(toks, tok)
			i = ni
		}
	}
	toks = append(toks, Token{Type: TEOF, Pos: n})
	return toks, nil
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNamePart(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9')
}

func lexOp(src string, i int) (Token, int, bool) {
	two := ""
	if i+1 < len(src) {
		two = src[i : i+2]
	}
	switch two {
	case "//", "==", "!=", "<=", ">=", "->":
		return Token{Type: TOp, Text: two, Pos: i}, i + 2, true
	}
	c := src[i]
	switch c {
	case '+', '-', '*', '/', '%', '<', '>':
		return Token{Type: TOp, Text: string(c), Pos: i}, i + 1, true
	case '(', ')', '[', ']', '{', '}', ',', ':', '.', '=':
		return Token{Type: TPunct, Text: string(c), Pos: i}, i + 1, true
	}
	return Token{}, i, false
}

func lexNumber(src string, i int) (Token, int, error) {
	j := i
	for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '_') {
		j++
	}
	isFloat := false
	if j < len(src) && src[j] == '.' {
		isFloat = true
		j++
		for j < len(src) && (src[j] >= '0' && src[j] <= '9') {
			j++
		}
	}
	if j < len(src) && (src[j] == 'e' || src[j] == 'E') {
		isFloat = true
		j++
		if j < len(src) && (src[j] == '+' || src[j] == '-') {
			j++
		}
		for j < len(src) && src[j] >= '0' && src[j] <= '9' {
			j++
		}
	}
	text := src[i:j]
	text = strings.ReplaceAll(text, "_", "")
	tok := Token{Type: TNumber, Text: text, IsFloat: isFloat, Pos: i}
	if isFloat {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return Token{}, i, fmt.Errorf("pyast: bad float %q: %v", text, err)
		}
		tok.Float = f
	} else {
		v, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return Token{}, i, fmt.Errorf("pyast: bad int %q: %v", text, err)
		}
		tok.Int = v
	}
	return tok, j, nil
}

// lexString 解码一个 python 字符串字面量（双引号或单引号）。转义族与
// quoteString 镜像：\" \\ \b \f \n \r \t \uXXXX（含 surrogate pair 组合）。
// 未知转义报错（子集外）。
func lexString(src string, i int) (string, int, error) {
	quote := src[i]
	j := i + 1
	var b strings.Builder
	for j < len(src) {
		c := src[j]
		if c == quote {
			return b.String(), j + 1, nil
		}
		if c == '\\' {
			if j+1 >= len(src) {
				return "", i, fmt.Errorf("pyast: unterminated string at offset %d", i)
			}
			e := src[j+1]
			switch e {
			case '"':
				b.WriteByte('"')
				j += 2
			case '\'':
				b.WriteByte('\'')
				j += 2
			case '\\':
				b.WriteByte('\\')
				j += 2
			case 'b':
				b.WriteByte('\b')
				j += 2
			case 'f':
				b.WriteByte('\f')
				j += 2
			case 'n':
				b.WriteByte('\n')
				j += 2
			case 'r':
				b.WriteByte('\r')
				j += 2
			case 't':
				b.WriteByte('\t')
				j += 2
			case 'u':
				r, nj, err := lexUnicodeEscape(src, j+2)
				if err != nil {
					return "", i, err
				}
				// surrogate pair 组合：\uD800-\uDBFF 后紧跟 \uDC00-\uDFFF
				if utf16High(r) && strings.HasPrefix(src[nj:], "\\u") {
					r2, nj2, err := lexUnicodeEscape(src, nj+2)
					if err == nil && utf16Low(r2) {
						b.WriteRune(utf16Decode(r, r2))
						j = nj2
						continue
					}
				}
				b.WriteRune(r)
				j = nj
			default:
				return "", i, fmt.Errorf("pyast: unsupported escape \\%c at offset %d", e, j)
			}
			continue
		}
		if c == '\n' {
			return "", i, fmt.Errorf("pyast: newline in string at offset %d", i)
		}
		b.WriteByte(c)
		j++
	}
	return "", i, fmt.Errorf("pyast: unterminated string at offset %d", i)
}

func lexUnicodeEscape(src string, i int) (rune, int, error) {
	if i+4 > len(src) {
		return 0, i, fmt.Errorf("pyast: truncated \\u escape at offset %d", i)
	}
	v, err := strconv.ParseUint(src[i:i+4], 16, 32)
	if err != nil {
		return 0, i, fmt.Errorf("pyast: bad \\u escape at offset %d", i)
	}
	return rune(v), i + 4, nil
}

func utf16High(r rune) bool { return r >= 0xd800 && r <= 0xdbff }
func utf16Low(r rune) bool  { return r >= 0xdc00 && r <= 0xdfff }

func utf16Decode(hi, lo rune) rune {
	return 0x10000 + (hi-0xd800)<<10 + (lo - 0xdc00)
}

var _ = utf8.RuneLen // keep import if unused in future edits
