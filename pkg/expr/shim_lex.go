package expr

// shim_lex.go — M4 synthetic-shim 词法（Python 子集）。
//
// 只覆盖 IR 生成的 shim 封闭构造集:标识符/关键字、字符串（双引号 +
// \uXXXX 转义,与 pyast.quoteString 逆对应）、整数/浮点、运算符与括号。
// 语法错误在 Parse 期以错误返回（不 panic）;token 位置仅用于错误消息。

import (
	"fmt"
	"strconv"
	"strings"
)

type stokKind int

const (
	stEOF stokKind = iota
	stIdent
	stKeyword
	stString
	stInt
	stFloat
	stOp // single-char operator / punctuation
)

// stok is one shim token.
type stok struct {
	kind stokKind
	text string // keyword/op text; for stString the DECODED value
	pos  int
}

// stKeywordSet is the closed keyword set the shims may use.
var stKeywordSet = map[string]bool{
	"def": true, "return": true, "import": true, "if": true, "else": true,
	"and": true, "or": true, "not": true, "in": true, "for": true, "True": true,
	"False": true, "None": true, "is": true,
}

func shimLex(src string) ([]stok, error) {
	var out []stok
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '#': // line comment
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			word := src[i:j]
			if stKeywordSet[word] {
				out = append(out, stok{kind: stKeyword, text: word, pos: i})
			} else {
				out = append(out, stok{kind: stIdent, text: word, pos: i})
			}
			i = j
		case c == '"':
			val, j, err := shimScanString(src, i)
			if err != nil {
				return nil, err
			}
			out = append(out, stok{kind: stString, text: val, pos: i})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			tok := src[i:j]
			if strings.Contains(tok, ".") {
				out = append(out, stok{kind: stFloat, text: tok, pos: i})
			} else {
				out = append(out, stok{kind: stInt, text: tok, pos: i})
			}
			i = j
		default:
			// multi-char operators first (== != <= >=), then single chars
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "==", "!=", "<=", ">=":
				out = append(out, stok{kind: stOp, text: two, pos: i})
				i += 2
				continue
			}
			out = append(out, stok{kind: stOp, text: string(c), pos: i})
			i++
		}
	}
	out = append(out, stok{kind: stEOF, pos: len(src)})
	return out, nil
}

// shimScanString decodes a double-quoted string literal starting at i.
// Escapes mirror pyast.quoteString: \" \\ \b \f \n \r \t \uXXXX (surrogate
// pairs supported). Raw newlines inside the literal are an error.
func shimScanString(src string, i int) (string, int, error) {
	var b strings.Builder
	j := i + 1
	for j < len(src) {
		c := src[j]
		switch c {
		case '"':
			return b.String(), j + 1, nil
		case '\\':
			if j+1 >= len(src) {
				return "", 0, fmt.Errorf("shim: unterminated escape at %d", i)
			}
			e := src[j+1]
			switch e {
			case '"', '\\', '\'', '/':
				b.WriteByte(e)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				if j+5 >= len(src) {
					return "", 0, fmt.Errorf("shim: bad \\u escape at %d", j)
				}
				hex := src[j+2 : j+6]
				n, err := strconv.ParseUint(hex, 16, 32)
				if err != nil {
					return "", 0, fmt.Errorf("shim: bad \\u escape %q at %d", hex, j)
				}
				r := rune(n)
				if utf16IsHighSurrogate(r) {
					// expect a following \uDC00-\uDFFF low surrogate
					if j+11 >= len(src) || src[j+6] != '\\' || src[j+7] != 'u' {
						return "", 0, fmt.Errorf("shim: lone high surrogate at %d", j)
					}
					loHex := src[j+8 : j+12]
					lo, err := strconv.ParseUint(loHex, 16, 32)
					if err != nil || !utf16IsLowSurrogate(rune(lo)) {
						return "", 0, fmt.Errorf("shim: bad surrogate pair at %d", j)
					}
					cp := 0x10000 + (int64(r)-0xD800)<<10 + (int64(lo) - 0xDC00)
					b.WriteRune(rune(cp))
					j += 10
				} else {
					b.WriteRune(r)
					j += 4
				}
			default:
				return "", 0, fmt.Errorf("shim: unknown escape \\%c at %d", e, j)
			}
			j += 2
		case '\n':
			return "", 0, fmt.Errorf("shim: unterminated string at %d", i)
		default:
			b.WriteByte(c)
			j++
		}
	}
	return "", 0, fmt.Errorf("shim: unterminated string at %d", i)
}

func utf16IsHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }
func utf16IsLowSurrogate(r rune) bool  { return r >= 0xDC00 && r <= 0xDFFF }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// tokErr builds a positioned parse error.
func tokErr(tok stok, format string, args ...any) error {
	return fmt.Errorf("shim:%d: %s", tok.pos, fmt.Sprintf(format, args...))
}
