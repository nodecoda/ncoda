package codeparse

// strip.go — coze JavaScript/TypeScript code 节点的签名层 TS 剥离。
//
// 从 internal/runtime/nodes/jrun.go 迁出：runtime 执行器与编译期/加载期
// 语法预检必须用**同一份**剥离规则，否则预检会对 `{ params }: Args` 这类
// coze 模板全量假阳性（语料 1538 个 JS 节点里 92% 是同一模板）。

import "strings"

// StripJSTypes 轻量 TS 剥离：只处理 main 签名层的类型注解（coze 模板
// `{ params }: Args` / `): Promise<Output>`），不碰函数体与字符串内容。
// 不匹配的形态原样保留（node 语法错误 → run-err，语料占比 <3%）。
func StripJSTypes(code string) string {
	// 1. main 参数列表: 每个 `形参: 类型` → `形参`（形参 = 标识符或 {解构}）。
	//    深度扫描:注解冒号只作用于顶层参数（brace/bracket/angle 深度为 0）,
	//    类型消费到同层 `,` / `)`——`Record<string, any>` 等含逗号的泛型
	//    不会被截断（re1 的 `[^,)]+` 正是断在这里）。
	code = stripParamTypes(code)
	// 2. 返回类型剥离: `): Type {` → `) {`。Type 可含嵌套泛型与对象字面量
	//    （`Promise<{ image_url: string }>` / `Promise<Record<string, any>>`
	//    等,coze 语料实证）。平衡扫描:在参数列表 `)` 之后遇到 `:` 即进入
	//    返回类型区,消费到 angleDepth==0 && braceDepth==0 处的 `{`（函数体
	//    起始）。正则的 `[^>]*` / `Index(m, "{")` 无法处理嵌套 `>` 与泛型内
	//    的 `{`,统一用带深度的扫描器。
	code = stripReturnType(code)
	return code
}

// stripParamTypes 扫描 main(...) 参数列表,剥离每个顶层 `: 类型` 注解。
// 只处理 main 签名层,不触碰函数体/字符串/模板字面量/注释。
func stripParamTypes(code string) string {
	i := strings.Index(code, "main(")
	if i < 0 {
		return code
	}
	start := i + len("main(")
	// 状态机扫描到参数列表右括号(paren==0)。期间剥离注解:
	// paren==1 且 angle/brace/bracket 全 0 时,`:` 起始一个注解,
	// 消费到同层 `,` / `)`(含嵌套 <...>/[...]/{...}/字符串)。
	var out []byte
	out = append(out, code[:start]...)
	paren, angle, brace, bracket := 1, 0, 0, 0
	inStr, inTpl, inLineCmt, inBlkCmt, esc := false, false, false, false, false
	annot := false // 当前处于注解消费区
	j := start
	for j < len(code) {
		c := code[j]
		// 注解消费区:直接前进到同层逗号/右括号(深度全 0)。
		if annot {
			switch c {
			case '"':
				inStr = !inStr
			case '\\':
				// 字符串内转义跳过
			case ',':
				if !inStr && angle == 0 && brace == 0 && bracket == 0 {
					annot = false
					out = append(out, ',')
					j++
					continue
				}
			case ')':
				if !inStr && angle == 0 && brace == 0 && bracket == 0 {
					annot = false
					out = append(out, ')')
					paren--
					j++
					if paren == 0 {
						out = append(out, code[j:]...)
						return string(out)
					}
					continue
				}
			case '<':
				if !inStr {
					angle++
				}
			case '>':
				if !inStr && angle > 0 {
					angle--
				}
			case '{':
				if !inStr {
					brace++
				}
			case '}':
				if !inStr && brace > 0 {
					brace--
				}
			case '[':
				if !inStr {
					bracket++
				}
			case ']':
				if !inStr && bracket > 0 {
					bracket--
				}
			}
			j++
			continue
		}
		// 正常区:处理注释/字符串/模板/深度/括号闭合。
		switch {
		case inLineCmt:
			if c == '\n' {
				inLineCmt = false
				out = append(out, c)
			}
		case inBlkCmt:
			if c == '*' && j+1 < len(code) && code[j+1] == '/' {
				inBlkCmt = false
				out = append(out, c, '/')
				j++
			}
		case inTpl:
			if c == '\\' {
				esc = !esc
			} else if c == '`' && !esc {
				inTpl = false
			} else {
				esc = false
			}
			out = append(out, c)
		case inStr:
			if c == '\\' {
				esc = !esc
			} else if c == '"' && !esc {
				inStr = false
			} else {
				esc = false
			}
			out = append(out, c)
		case c == '/' && j+1 < len(code) && code[j+1] == '/':
			inLineCmt = true
			out = append(out, c)
		case c == '/' && j+1 < len(code) && code[j+1] == '*':
			inBlkCmt = true
			out = append(out, c)
		case c == '`':
			inTpl = true
			out = append(out, c)
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == ':':
			// 顶层注解冒号(paren 1 且无嵌套):进入注解消费区。
			if paren == 1 && angle == 0 && brace == 0 && bracket == 0 {
				annot = true
			} else {
				out = append(out, c)
			}
		case c == '<':
			angle++
			out = append(out, c)
		case c == '>':
			if angle > 0 {
				angle--
			}
			out = append(out, c)
		case c == '{':
			brace++
			out = append(out, c)
		case c == '}':
			if brace > 0 {
				brace--
			}
			out = append(out, c)
		case c == '[':
			bracket++
			out = append(out, c)
		case c == ']':
			if bracket > 0 {
				bracket--
			}
			out = append(out, c)
		case c == '(':
			paren++
			out = append(out, c)
		case c == ')':
			paren--
			out = append(out, c)
			if paren == 0 {
				out = append(out, code[j+1:]...)
				return string(out)
			}
		default:
			out = append(out, c)
		}
		j++
	}
	return code
}

// stripReturnType 扫描 main(...) 参数列表右括号之后的返回类型注解。
// 只处理返回类型区（`): Type {` 与 `): Type` 变体）,不触碰函数体。
// skipBalancedBrace 返回从 open（指向 `{`）起配对 `}` 之后的下标。
// 只跟踪字符串边界:类型字面量里的 `{}` 嵌套按深度消费。
func skipBalancedBrace(code string, open int) (int, bool) {
	depth, inStr, esc := 0, false, false
	for i := open; i < len(code); i++ {
		c := code[i]
		if inStr {
			if c == '\\' {
				esc = !esc
			} else if c == '"' && !esc {
				inStr = false
			} else if c != '\\' {
				esc = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func stripReturnType(code string) string {
	i := strings.Index(code, "main(")
	if i < 0 {
		return code
	}
	// 定位参数列表右括号:从 main( 起扫描,跟踪括号深度与字符串/模板字面量。
	j := i + len("main(")
	paren, inStr, inTpl, esc, inLineCmt, inBlkCmt := 1, false, false, false, false, false
	for ; j < len(code); j++ {
		c := code[j]
		switch {
		case inLineCmt:
			if c == '\n' {
				inLineCmt = false
			}
		case inBlkCmt:
			if c == '*' && j+1 < len(code) && code[j+1] == '/' {
				inBlkCmt = false
				j++
			}
		case inTpl:
			if c == '\\' {
				esc = true
			} else if c == '`' && !esc {
				inTpl = false
			} else {
				esc = false
			}
		case inStr:
			if c == '\\' {
				esc = true
			} else if c == '"' && !esc {
				inStr = false
			} else {
				esc = false
			}
		case c == '/' && j+1 < len(code) && code[j+1] == '/':
			inLineCmt = true
			j++
		case c == '/' && j+1 < len(code) && code[j+1] == '*':
			inBlkCmt = true
			j++
		case c == '`':
			inTpl = true
		case c == '"':
			inStr = true
		case c == '(':
			paren++
		case c == ')':
			paren--
			if paren == 0 {
				goto foundClose
			}
		}
	}
foundClose:
	// j 停在参数列表 `)`。之后若为 `: 类型`,剥离到函数体 `{`。
	k := j + 1
	// 跳过空白与注释
	for k < len(code) {
		c := code[k]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			k++
			continue
		}
		if c == '/' && k+1 < len(code) && code[k+1] == '/' {
			for k < len(code) && code[k] != '\n' {
				k++
			}
			continue
		}
		if c == '/' && k+1 < len(code) && code[k+1] == '*' {
			k += 2
			for k+1 < len(code) && !(code[k] == '*' && code[k+1] == '/') {
				k++
			}
			k += 2
			continue
		}
		break
	}
	if k >= len(code) || code[k] != ':' {
		return code // 无返回类型注解
	}
	// 进入类型区:消费到函数体的 `{`。
	// 注意：返回类型可以以对象字面量开头（`): { a: string } { ... }`），
	// 那个 `{` 属于类型，不是函数体。判据 = 前一个非空白字符：`{` 紧跟
	// `:`/`|`/`&` 时是类型字面量（跳过配对的 `}`），否则才是函数体。
	angle, brace, bstr, bstrEsc := 0, 0, false, false
	prevSig := byte(':')
	for k < len(code) {
		c := code[k]
		if bstr {
			if c == '\\' {
				bstrEsc = !bstrEsc
			} else if c == '"' && !bstrEsc {
				bstr = false
			} else if c != '\\' {
				bstrEsc = false
			}
			k++
			continue
		}
		if c == '{' && angle == 0 && brace == 0 {
			if prevSig == ':' || prevSig == '|' || prevSig == '&' {
				nk, ok := skipBalancedBrace(code, k)
				if !ok {
					return code // 类型字面量未闭合:不冒险改写
				}
				k = nk
				prevSig = '}'
				continue
			}
			break // 函数体起始
		}
		switch c {
		case '"':
			bstr = true
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '{':
			brace++
		case '}':
			if brace > 0 {
				brace--
			}
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			prevSig = c
		}
		k++
	}
	if k < len(code) {
		// 删除 `): Type` 段（含中间空白）,保留 `)` 与函数体 `{`。
		code = code[:j+1] + code[k:]
	}
	return code
}
