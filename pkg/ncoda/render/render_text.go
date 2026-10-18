package render

// render.go —— 语句树 → 可读文本（调试/探针用，对应 ana2 ast4/codegen 的简化形态）。

import (
	"fmt"
	"strings"
)

// RenderStmt 渲染单条语句为文本。
func RenderStmt(s Stmt, indent int) string {
	pad := strings.Repeat("  ", indent)
	switch v := s.(type) {
	case *Block:
		return ""
	case *VarDecl:
		if v.Init == nil {
			return fmt.Sprintf("%svar %s", pad, v.Name)
		}
		init := " = " + RenderExpr(v.Init)
		return fmt.Sprintf("%svar %s%s", pad, v.Name, init)
	case *AssignStmt:
		return fmt.Sprintf("%s%s = %s", pad, v.Target, RenderExpr(v.Value))
	case *ExprStmt:
		return pad + RenderExpr(v.Expr)
	case *OutputStmt:
		return pad + "output(" + RenderExpr(v.Value) + ")"
	case *If:
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%sif %s {\n", pad, RenderExpr(v.Cond)))
		for _, inner := range v.Then.Stmts {
			b.WriteString(RenderStmt(inner, indent+1) + "\n")
		}
		if v.Else != nil {
			b.WriteString(pad + "} else {\n")
			for _, inner := range v.Else.Stmts {
				b.WriteString(RenderStmt(inner, indent+1) + "\n")
			}
		}
		b.WriteString(pad + "}")
		return b.String()
	case *For:
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%sfor %s in %s {\n", pad, v.Var, RenderExpr(v.Iterable)))
		for _, inner := range v.Body.Stmts {
			b.WriteString(RenderStmt(inner, indent+1) + "\n")
		}
		b.WriteString(pad + "}")
		return b.String()
	case *AttemptStmt:
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%sattempt %s as %s {\n", pad, RenderExpr(v.Operation), v.Result))
		b.WriteString(pad + "  success {\n")
		for _, inner := range v.Success.Stmts {
			b.WriteString(RenderStmt(inner, indent+2) + "\n")
		}
		b.WriteString(pad + "  }\n" + pad + "  failure(" + v.Error + ") {\n")
		for _, inner := range v.Failure.Stmts {
			b.WriteString(RenderStmt(inner, indent+2) + "\n")
		}
		b.WriteString(pad + "  }\n" + pad + "}")
		return b.String()
	case *Return:
		return pad + "return " + RenderExpr(v.Value)
	case *Break:
		return pad + "break"
	case *Continue:
		return pad + "continue"
	case *Comment:
		return pad + "// " + v.Text
	default:
		return pad + fmt.Sprintf("<stmt %T>", s)
	}
}

// RenderExpr 渲染表达式为文本。
func RenderExpr(e Expr) string {
	switch v := e.(type) {
	case *IdentExpr:
		return v.Name
	case *LiteralExpr:
		return fmt.Sprintf("%v", v.Value)
	case *CallExpr:
		args := make([]string, 0, len(v.Args))
		for _, a := range v.Args {
			args = append(args, RenderExpr(a))
		}
		callee := v.Callee
		if v.Receiver != nil {
			callee = RenderExpr(v.Receiver) + "." + callee
		}
		typeArgs := ""
		if len(v.TypeArgs) > 0 {
			typeArgs = "<" + strings.Join(v.TypeArgs, ", ") + ">"
		}
		return callee + typeArgs + "(" + strings.Join(args, ", ") + ")"
	case *OperationPolicyExpr:
		return RenderExpr(v.Base) + renderOperationPolicySuffix(v)
	case *MemberExpr:
		return RenderExpr(v.Obj) + "." + escapeFieldName(v.Field)
	case *DictExpr:
		pairs := make([]string, 0, len(v.Keys))
		for i, k := range v.Keys {
			pairs = append(pairs, fmt.Sprintf("%s: %s", k, RenderExpr(v.Values[i])))
		}
		return "{" + strings.Join(pairs, ", ") + "}"
	case *ListExpr:
		items := make([]string, 0, len(v.Items))
		for _, item := range v.Items {
			items = append(items, RenderExpr(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case *TemplateValue:
		return renderTemplateClause(v.Text, v.Bindings)
	case *TemplateString:
		var sb strings.Builder
		sb.WriteString("`")
		for _, part := range v.Parts {
			sb.WriteString(part.Text)
			if part.Expr != nil {
				sb.WriteString("${")
				sb.WriteString(RenderExpr(part.Expr))
				sb.WriteString("}")
			}
		}
		sb.WriteString("`")
		return sb.String()
	default:
		return fmt.Sprintf("<expr %T>", e)
	}
}

// RenderStmts 渲染语句列表。
func RenderStmts(stmts []Stmt) string {
	var b strings.Builder
	for _, s := range stmts {
		line := RenderStmt(s, 0)
		if line != "" {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// CountStmts 统计语句类型数量（探针用）。
func CountStmts(stmts []Stmt) map[string]int {
	counts := map[string]int{}
	var walk func([]Stmt)
	walk = func(items []Stmt) {
		for _, s := range items {
			switch v := s.(type) {
			case *VarDecl:
				counts["var"]++
			case *ExprStmt:
				counts["expr"]++
			case *OutputStmt:
				counts["output"]++
			case *If:
				counts["if"]++
				walk(v.Then.Stmts)
				if v.Else != nil {
					walk(v.Else.Stmts)
				}
			case *For:
				counts["for"]++
				walk(v.Body.Stmts)
			case *Return:
				counts["return"]++
			case *Break:
				counts["break"]++
			case *Continue:
				counts["continue"]++
			case *Comment:
				counts["comment"]++
			case *ImportStmt:
				counts["import"]++
			case *AttemptStmt:
				counts["attempt"]++
				walk(v.Success.Stmts)
				walk(v.Failure.Stmts)
			}
		}
	}
	walk(stmts)
	return counts
}
