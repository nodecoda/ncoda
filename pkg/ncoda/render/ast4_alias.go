package render

import "github.com/nodecoda/ncoda/pkg/ncoda/ast4"

// ast4 类型别名；方法集与未导出标记方法仍归 pkg/ncoda/ast4。
type Stmt = ast4.Stmt
type Block = ast4.Block
type VarDecl = ast4.VarDecl
type ExprStmt = ast4.ExprStmt
type If = ast4.If
type While = ast4.While
type ParallelStmtStmt = ast4.ParallelStmtStmt
type For = ast4.For
type ForExpr = ast4.ForExpr
type Yield = ast4.Yield
type OutputStmt = ast4.OutputStmt
type Return = ast4.Return
type Break = ast4.Break
type Continue = ast4.Continue
type Comment = ast4.Comment
type ImportStmt = ast4.ImportStmt
type Expr = ast4.Expr
type IdentExpr = ast4.IdentExpr
type LiteralExpr = ast4.LiteralExpr
type CallExpr = ast4.CallExpr
type OperationPolicyExpr = ast4.OperationPolicyExpr
type LambdaExpr = ast4.LambdaExpr
type MemberExpr = ast4.MemberExpr
type DictExpr = ast4.DictExpr
type ListExpr = ast4.ListExpr
type BinaryExpr = ast4.BinaryExpr
type UnaryExpr = ast4.UnaryExpr
type TernaryExpr = ast4.TernaryExpr
type IndexExpr = ast4.IndexExpr
type TemplateString = ast4.TemplateString
type TemplateValue = ast4.TemplateValue
type TemplatePart = ast4.TemplatePart
type ForeignCodeOutputField = ast4.ForeignCodeOutputField
type ForeignCodeExpr = ast4.ForeignCodeExpr
type CodeInputBinding = ast4.CodeInputBinding
type AssignStmt = ast4.AssignStmt
type TypeDecl = ast4.TypeDecl
type SubflowDecl = ast4.SubflowDecl
type TemplateSlot = ast4.TemplateSlot
type EnumDecl = ast4.EnumDecl
type TypeField = ast4.TypeField
type Switch = ast4.Switch
type SwitchBranch = ast4.SwitchBranch
type AttemptStmt = ast4.AttemptStmt
type RequestInputStmt = ast4.RequestInputStmt
type RequestInputLLM = ast4.RequestInputLLM
type RequestInputField = ast4.RequestInputField
type RequestInputAction = ast4.RequestInputAction
type RequestInputBranch = ast4.RequestInputBranch

// FlattenValueLoop 原定义在 ast4；保留渲染器内的裸名调用。
func FlattenValueLoop(init Expr) bool { return ast4.FlattenValueLoop(init) }
