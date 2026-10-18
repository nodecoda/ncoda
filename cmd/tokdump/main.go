package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/nodecoda/ncoda/pkg/lexer"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var src string
	for sc.Scan() {
		src += sc.Text() + "\n"
	}
	lx := lexer.New(src, "<stdin>")
	toks := lx.Tokenize()
	for _, tok := range toks {
		lit := ""
		switch {
		case tok.Literal.Valid && tok.Literal.Kind == 1:
			lit = tok.Literal.S
		case tok.Literal.Valid && tok.Literal.Kind == 2:
			lit = fmt.Sprintf("%d", tok.Literal.I)
		case tok.Literal.Valid && tok.Literal.Kind == 3:
			lit = fmt.Sprintf("%v", tok.Literal.F)
		case tok.Literal.Valid && tok.Literal.Kind == 4:
			lit = fmt.Sprintf("%v", tok.Literal.B)
		}
		fmt.Printf("%s|%s|%s\n", tok.Type, tok.Lexeme, lit)
	}
	for _, e := range lx.Errors() {
		fmt.Printf("ERROR|%s\n", e.Message)
	}
}
