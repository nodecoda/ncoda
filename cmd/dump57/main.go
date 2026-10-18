package main

import (
	"fmt"
	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/parser"
	"os"
)

func dump(e ast.Expr, d int) {
	if e == nil {
		fmt.Println("nil")
		return
	}
	switch v := e.(type) {
	case *ast.MapLit:
		fmt.Printf("%*sMapLit\n", d*2, "")
		for _, en := range v.Entries() {
			fmt.Printf("%*skey: ", d*2+2, "")
			dump(en.Key, d+1)
			fmt.Printf("%*sval: ", d*2+2, "")
			dump(en.Value, d+1)
		}
	case *ast.StringLit:
		fmt.Printf("%*sString(%q)\n", d*2, "", v.Value())
	case *ast.IdentExpr:
		fmt.Printf("%*sIdent(%s)\n", d*2, "", v.Name())
	case *ast.CallExpr:
		fmt.Printf("%*sCall(\n", d*2, "")
		fmt.Printf("%*s  callee: ", d*2+2, "")
		dump(v.Func(), d+1)
		fmt.Printf("%*s  typeArgs:\n", d*2+2, "")
		for _, ta := range v.TypeArgs() {
			fmt.Printf("%*s    %s\n", d*2+2, "", typeRefName(ta))
		}
		for i, a := range v.Args() {
			fmt.Printf("%*s  arg%d: ", d*2+2, "", i)
			dump(a, d+1)
		}
		fmt.Printf("%*s)\n", d*2, "")
	case *ast.UnaryExpr:
		fmt.Printf("%*sUnary(%v)\n", d*2, "", v.Op())
		dump(v.Operand(), d+1)
	case *ast.FieldAccessExpr:
		fmt.Printf("%*sField(%s).", d*2, "", v.FieldName())
		dump(v.Obj(), d)
	default:
		fmt.Printf("%*s%T\n", d*2, "", v)
	}
}
func main() {
	src, _ := os.ReadFile(os.Args[1])
	prog, errs := parser.ParseSource(string(src), os.Args[1])
	if len(errs) > 0 {
		fmt.Println("parse errors:", errs)
		os.Exit(1)
	}
	fn := prog.Entry()
	fmt.Println("== fn", fn.Name(), "params:")
	for _, p := range fn.Params() {
		fmt.Println("   param", p.Name(), typeRefName(p.TypeRef()))
	}
	for _, st := range fn.Body().Stmts() {
		fmt.Printf("stmt %T\n", st)
		switch s := st.(type) {
		case *ast.VarDeclStmt:
			fmt.Println("  name:", s.Name())
			dump(s.Init(), 1)
		case *ast.IfStmt:
			dump(s.Condition(), 1)
			fmt.Println("  then:")
			walkStmts(s.ThenBody().Stmts(), 2)
			if s.ElseBody() != nil {
				fmt.Println("  else:")
				walkStmts(s.ElseBody().Stmts(), 2)
			}
		case *ast.OutputStmt:
			fmt.Printf("  output value=")
			dump(s.Value(), 2)
		case *ast.ReturnStmt:
			dump(s.Value(), 2)
		}
	}
	fmt.Println("== types ==")
	for _, td := range prog.TypeDecls() {
		fmt.Println("type", td.Name())
		for _, f := range td.Fields() {
			fmt.Println("   field", f.Name(), typeRefName(f.TypeRef()))
		}
	}
	fmt.Println("== consts ==")
	for _, cd := range prog.ConstDecls() {
		fmt.Println("const", cd.Name())
	}
}
func walkStmts(stmts []ast.Stmt, d int) {
	for _, st := range stmts {
		fmt.Printf("%*sstmt %T\n", d*2, "", st)
		switch s := st.(type) {
		case *ast.OutputStmt:
			fmt.Printf("%*svalue=", d*2+2, "")
			dump(s.Value(), d+2)
		case *ast.ReturnStmt:
			dump(s.Value(), d+1)
		}
	}
}
func typeRefName(t ast.TypeRef) string {
	switch v := t.(type) {
	case *ast.PrimTypeRef:
		return v.Name()
	case *ast.NamedTypeRef:
		return v.Name()
	}
	return "?"
}
