package shim

import (
	"github.com/nodecoda/ncoda/pkg/ncoda/astnodes"
	"github.com/nodecoda/ncoda/pkg/ncoda/typesystem"
)

// TypeResolver / NodeLookup 是闭源 Generator 需要实现的最小接口面。
// 开放侧只依赖这两问：表达式类型、节点声明。
type TypeResolver interface {
	ExprTypeInfo(Expr) *typesystem.TypeInfo
}

type NodeLookup interface {
	Node(string) astnodes.NodeDecl
}

// Reverser 是 shim 反演的无状态入口（params 为按节点的解析缓存）。
type Reverser struct {
	Types  TypeResolver
	Nodes  NodeLookup
	params map[string]map[string]string
}

func New(types TypeResolver, nodes NodeLookup) *Reverser {
	return &Reverser{Types: types, Nodes: nodes}
}

func (r *Reverser) ExprTypeInfo(e Expr) *typesystem.TypeInfo {
	if r == nil || r.Types == nil {
		return nil
	}
	return r.Types.ExprTypeInfo(e)
}

func (r *Reverser) node(id string) astnodes.NodeDecl {
	if r == nil || r.Nodes == nil {
		return nil
	}
	return r.Nodes.Node(id)
}

// 以下为闭源侧仍按历史裸名调用的入口（实现不变）。

func (r *Reverser) ShimDictParams(nodeID string) map[string]string {
	return r.shimDictParams(nodeID)
}

func (r *Reverser) ShimRecordFileCast(d *DictExpr, recTI *typesystem.TypeInfo) {
	r.shimRecordFileCast(d, recTI)
}

func (r *Reverser) RecordShimNullableLifts(code string, nullableInputs map[string]bool) map[string]bool {
	return r.recordShimNullableLifts(code, nullableInputs)
}
