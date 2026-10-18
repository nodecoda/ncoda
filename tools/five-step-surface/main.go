// 语言面（词法层）枚举器 —— 把「实现接受哪些符号」变成可枚举的闭集。
//
// 方法 = **真跑词法器**（不是读代码猜）：对候选符号串逐一送入 lexer，能整体
// 词法化为**恰好一个** token 的候选即「实现承认的符号」。这样 maximal-munch
// 的边界（`?` vs `??`、`<` vs `<<`）由实现给出，而不是由注释给出。
//
// 用法：
//
//	go run ./tools/five-step-surface            # JSON
//	go run ./tools/five-step-surface --max-len 2
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/nodecoda/ncoda/pkg/lexer"
	"github.com/nodecoda/ncoda/pkg/token"
)

const alphabet = "+-*/%=<>!&|?^~:;,.@$()[]{}"

type symbol struct {
	Lexeme string `json:"lexeme"`
	Token  string `json:"token"`
}

type report struct {
	Keywords []string `json:"keywords"`
	Symbols  []symbol `json:"symbols"`
	Alphabet string   `json:"alphabet"`
	MaxLen   int      `json:"max_len"`
}

// lexOne reports the single token a candidate lexes to（整体化为 1 个 token）.
func lexOne(src string) (token.Type, bool) {
	toks := lexer.New(src, "<surface>").Tokenize()
	var real []token.Token
	for _, t := range toks {
		if t.Type == token.EOF {
			continue
		}
		if t.Type == token.ERROR {
			return 0, false
		}
		real = append(real, t)
	}
	if len(real) != 1 {
		return 0, false
	}
	return real[0].Type, true
}

func main() {
	maxLen := flag.Int("max-len", 3, "候选符号串最大长度")
	probes := flag.String("probe", "", "额外探针（换行分隔；输出各探针的 token 流）")
	flag.Parse()

	if *probes != "" {
		for _, src := range strings.Split(*probes, "\n") {
			type tok struct {
				Type   string `json:"type"`
				Lexeme string `json:"lexeme"`
			}
			var ts []tok
			for _, t := range lexer.New(src, "<probe>").Tokenize() {
				if t.Type == token.EOF {
					continue
				}
				ts = append(ts, tok{Type: t.Type.String(), Lexeme: t.Lexeme})
			}
			enc := json.NewEncoder(os.Stdout)
			enc.Encode(map[string]any{"probe": src, "tokens": ts})
		}
		return
	}

	rep := report{Keywords: token.Keywords(), Alphabet: alphabet, MaxLen: *maxLen}
	var walk func(cur string)
	walk = func(cur string) {
		// 注释起始（`//` / `/*`）会让「后面全被吃掉」，于是 `+//` 也会"整体化为 1 个
		// token"—— 那是假命中。含注释起始的候选一律不参与。
		if strings.Contains(cur, "//") || strings.Contains(cur, "/*") {
			return
		}
		if len(cur) > 0 {
			if typ, ok := lexOne(cur); ok {
				rep.Symbols = append(rep.Symbols, symbol{Lexeme: cur, Token: typ.String()})
			}
		}
		if len(cur) >= *maxLen {
			return
		}
		for _, c := range alphabet {
			walk(cur + string(c))
		}
	}
	for _, c := range alphabet {
		walk(string(c))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(2)
	}
}
