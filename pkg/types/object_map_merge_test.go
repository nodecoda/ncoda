//@feature open-object — `object` 与 `map<string, any>` 是同一个 open-object Descriptor（双向可赋值）

package types

// object_map_merge_test.go —— 「`object` 并入 `map<string, any>`」的契约（撤实现必红）。
//
// 2026-09-15 用户裁定：两者是**同一个 Descriptor**。判据（正负两侧都锁）：
//
//	正面：ObjectType() 的结构 == map<string, any>（Kind/Key/Val 逐项）；
//	正面：两个方向都可赋值（合并前 Map ↛ Object、Object ↛ Map —— 同一个值
//	      写 `object` 能过、写 `map<string,any>` 报 TYPE_MISMATCH）。
//
// 撤掉实现（ObjectType 回到 `&Descriptor{Kind: Object}`）⇒ 本文件两条断言同时 RED。

import "testing"

func TestObjectTypeIsMapStringAny(t *testing.T) {
	o, m := ObjectType(), MapType(StringType(), AnyType())
	if o.Kind != Map || o.Kind != m.Kind {
		t.Fatalf("ObjectType().Kind = %v, want %v (object 并入 map)", o.Kind, Map)
	}
	if o.Key == nil || o.Key.Kind != String {
		t.Fatalf("ObjectType().Key = %v, want string", o.Key)
	}
	if o.Val == nil || o.Val.Kind != Any {
		t.Fatalf("ObjectType().Val = %v, want any", o.Val)
	}
}

func TestObjectAndMapAreMutuallyAssignable(t *testing.T) {
	o, m := ObjectType(), MapType(StringType(), AnyType())
	if !IsAssignable(o, m) {
		t.Error("object -> map<string,any> 不可赋值（合并后必须可赋值）")
	}
	if !IsAssignable(m, o) {
		t.Error("map<string,any> -> object 不可赋值（合并后必须可赋值）")
	}
}
