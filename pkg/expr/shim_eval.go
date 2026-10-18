package expr

// shim_eval.go — M4 synthetic-shim 求值器（pyast 解释,Python 子集语义）。
//
// 覆盖 IR 生成端闭合构造集:dict/list 字面量、算术/比较/逻辑、三元、
// str/len/isinstance/int/float、json.loads/json.dumps、dict.get、str.startswith/
// endswith、下标。语义对齐 Python 子集:
//   - int+int → int;任何 float 参与 → float;str+str → concat;混合 → 错误
//   - and/or 短路;not/Truthy 用 Truthy 语义
//   - str():None→"None",bool→"True"/"False",float 整值保留 ".0"（Python 形状）
//   - json.dumps 关 HTML 转义（Python json 不转义 <>&）
//
// 不支持的构造 → 错误（节点层包装 RExpressionUnsupported/RExpressionType）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/pyast"
)

// EvalShim parses and evaluates a synthetic shim against named inputs,
// returning the dict the shim returns (keys = output ports).
func EvalShim(code string, inputs map[string]any) (map[string]any, error) {
	m, err := ParseShim(code)
	if err != nil {
		return nil, err
	}
	scope := map[string]any{}
	for k, v := range inputs {
		scope[k] = v
	}
	return evalModule(m, scope)
}

func evalModule(m *pyast.Module, scope map[string]any) (map[string]any, error) {
	var last map[string]any
	for _, st := range m.Body {
		switch s := st.(type) {
		case *pyast.AssignStmt:
			v, err := evalExpr(s.Value, scope)
			if err != nil {
				return nil, err
			}
			scope[s.Name] = v
		case *pyast.ReturnStmt:
			v, err := evalExpr(s.Value, scope)
			if err != nil {
				return nil, err
			}
			d, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("shim return is %T, want dict", v)
			}
			last = d
		default:
			return nil, fmt.Errorf("shim: unsupported statement %T", st)
		}
	}
	if last == nil {
		return nil, fmt.Errorf("shim: no return statement")
	}
	return last, nil
}

func evalExpr(e pyast.Expr, scope map[string]any) (any, error) {
	switch x := e.(type) {
	case *pyast.NameExpr:
		if v, ok := scope[x.Name]; ok {
			return v, nil
		}
		if x.Name == "list" {
			return "list", nil // isinstance(x, list) marker
		}
		return nil, fmt.Errorf("shim: undefined name %q", x.Name)
	case *pyast.StringExpr:
		return x.Value, nil
	case *pyast.IntExpr:
		return x.Value, nil
	case *pyast.FloatExpr:
		return x.Value, nil
	case *pyast.BoolExpr:
		return x.Value, nil
	case *pyast.NoneExpr:
		return nil, nil
	case *pyast.ParenExpr:
		return evalExpr(x.X, scope)
	case *pyast.ListExpr:
		out := make([]any, 0, len(x.Elts))
		for _, el := range x.Elts {
			v, err := evalExpr(el, scope)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case *pyast.DictExpr:
		out := make(map[string]any, len(x.Items))
		for _, it := range x.Items {
			key, err := evalExpr(it.Key, scope)
			if err != nil {
				return nil, err
			}
			ks, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("shim: dict key is %T, want string", key)
			}
			v, err := evalExpr(it.Value, scope)
			if err != nil {
				return nil, err
			}
			out[ks] = v
		}
		return out, nil
	case *pyast.UnaryExpr:
		return evalUnary(x, scope)
	case *pyast.BinaryExpr:
		return evalBinary(x, scope)
	case *pyast.TernaryExpr:
		cond, err := evalExpr(x.Cond, scope)
		if err != nil {
			return nil, err
		}
		if Truthy(cond) {
			return evalExpr(x.Then, scope)
		}
		return evalExpr(x.Else, scope)
	case *pyast.CallExpr:
		return evalCall(x, scope)
	case *pyast.SubscriptExpr:
		obj, err := evalExpr(x.Obj, scope)
		if err != nil {
			return nil, err
		}
		idx, err := evalExpr(x.Index, scope)
		if err != nil {
			return nil, err
		}
		return subscript(obj, idx)
	case *pyast.AttributeExpr:
		// standalone attribute (json 模块名解析) — 仅在 call 中被消费;
		// 裸访问返回模块标记,调用处再分发。
		obj, err := evalExpr(x.Obj, scope)
		if err != nil {
			return nil, err
		}
		return map[string]any{"$attr": x.Attr, "$obj": obj}, nil
	case *pyast.ConcatExpr:
		var b strings.Builder
		for _, part := range x.Parts {
			v, err := evalExpr(part, scope)
			if err != nil {
				return nil, err
			}
			s, err := pyStr(v)
			if err != nil {
				return nil, err
			}
			b.WriteString(s)
		}
		return b.String(), nil
	case *pyast.ListCompExpr:
		// [{...} for i, v in enumerate(arr)] / [expr for x in xs]
		// 生成器发射形态（enumerate 计算内建,pygen.LowerBuiltin）;
		// 多子句 + if 过滤（flatten 的 isinstance 走 foreign code,
		// 不落 shim）统一按嵌套循环求值,单子句即 enumerate 形态。
		return evalListComp(x, scope)
	case *pyast.GeneratorExpr:
		// python 生成器表达式在本子集里**物化**（无惰性需求：只被 next() 消费）。
		// 一手（2026-09-28）：`pygen.LowerBuiltin("first_non_empty")` 发的就是
		// `next((str(u) for u in (X or []) if u and str(u).strip()), "")` ⇒
		// 旧注释「generator not emitted by the generator」与实现两说。
		return evalComprehension(x.Elt, x.Clauses, scope)
	case *pyast.DictCompExpr:
		return nil, fmt.Errorf("shim: comprehension unsupported (dict comprehension not emitted by the generator)")
	default:
		return nil, fmt.Errorf("shim: unsupported expression %T", e)
	}
}

func evalUnary(x *pyast.UnaryExpr, scope map[string]any) (any, error) {
	v, err := evalExpr(x.Operand, scope)
	if err != nil {
		return nil, err
	}
	switch x.Op {
	case "-":
		switch n := v.(type) {
		case int64:
			return -n, nil
		case float64:
			return -n, nil
		}
		return nil, fmt.Errorf("shim: unary - on %T", v)
	case "not":
		return !Truthy(v), nil
	}
	return nil, fmt.Errorf("shim: unsupported unary %q", x.Op)
}

func evalBinary(x *pyast.BinaryExpr, scope map[string]any) (any, error) {
	// and/or 短路:右侧仅在需要时求值
	if x.Op == "and" || x.Op == "or" {
		l, err := evalExpr(x.Left, scope)
		if err != nil {
			return nil, err
		}
		if x.Op == "and" {
			if !Truthy(l) {
				return l, nil
			}
			return evalExpr(x.Right, scope)
		}
		if Truthy(l) {
			return l, nil
		}
		return evalExpr(x.Right, scope)
	}
	l, err := evalExpr(x.Left, scope)
	if err != nil {
		return nil, err
	}
	r, err := evalExpr(x.Right, scope)
	if err != nil {
		return nil, err
	}
	// list * int / int * list：Python 的序列重复（`pygen` 的 `repeat` 内建
	// 就发 `[v] * n` —— 2026-09-28 两端同解判据一手：求值器只认数值乘法，
	// 于是这个内建的 shim 跑不起来）。字符串重复同形，一并覆盖。
	if x.Op == "*" {
		if rep, ok := repeatSeq(l, r); ok {
			return rep, nil
		}
		if rep, ok := repeatSeq(r, l); ok {
			return rep, nil
		}
	}
	switch x.Op {
	case "+":
		ls, lok := l.(string)
		rs, rok := r.(string)
		if lok && rok {
			return ls + rs, nil
		}
		ln, lok2 := toNum(l)
		rn, rok2 := toNum(r)
		if lok2 && rok2 {
			if isIntVal(l) && isIntVal(r) {
				return int64(ln) + int64(rn), nil
			}
			return ln + rn, nil
		}
		return nil, fmt.Errorf("shim: + on %T and %T", l, r)
	case "-", "*", "/", "//", "%":
		ln, lok2 := toNum(l)
		rn, rok2 := toNum(r)
		if !lok2 || !rok2 {
			return nil, fmt.Errorf("shim: %s on %T and %T", x.Op, l, r)
		}
		switch x.Op {
		case "-":
			if isIntVal(l) && isIntVal(r) {
				return int64(ln) - int64(rn), nil
			}
			return ln - rn, nil
		case "*":
			if isIntVal(l) && isIntVal(r) {
				return int64(ln) * int64(rn), nil
			}
			return ln * rn, nil
		case "/":
			return ln / rn, nil
		case "//":
			return int64(ln) / int64(rn), nil
		case "%":
			if isIntVal(l) && isIntVal(r) {
				return int64(ln) % int64(rn), nil
			}
			return float64(int64(ln) % int64(rn)), nil
		}
	case "==":
		return shimEq(l, r), nil
	case "!=":
		return !shimEq(l, r), nil
	case "<", ">", "<=", ">=":
		return shimCmp(x.Op, l, r)
	case "in", "not in":
		has, err := shimIn(l, r)
		if err != nil {
			return nil, err
		}
		if x.Op == "not in" {
			return !has, nil
		}
		return has, nil
	}
	return nil, fmt.Errorf("shim: unsupported operator %q", x.Op)
}

func evalCall(x *pyast.CallExpr, scope map[string]any) (any, error) {
	// json 模块调用:json.loads / json.dumps
	if nm, ok := x.Obj.(*pyast.NameExpr); ok && nm.Name == "json" {
		args := make([]any, 0, len(x.Args))
		for _, a := range x.Args {
			v, err := evalExpr(a, scope)
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		}
		switch x.Func {
		case "loads":
			if len(args) != 1 {
				return nil, fmt.Errorf("json.loads wants 1 arg, got %d", len(args))
			}
			s, ok := args[0].(string)
			if !ok {
				return nil, fmt.Errorf("json.loads input is %T, want string", args[0])
			}
			var out any
			if err := json.Unmarshal([]byte(s), &out); err != nil {
				return nil, fmt.Errorf("json.loads: %v", err)
			}
			return out, nil
		case "dumps":
			if len(args) < 1 {
				return nil, fmt.Errorf("json.dumps wants >= 1 arg")
			}
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(args[0]); err != nil {
				return nil, fmt.Errorf("json.dumps: %v", err)
			}
			return strings.TrimSuffix(buf.String(), "\n"), nil
		}
		return nil, fmt.Errorf("shim: unknown json function %q", x.Func)
	}
	// 方法调用:obj.method(args)
	if x.Obj != nil {
		obj, err := evalExpr(x.Obj, scope)
		if err != nil {
			return nil, err
		}
		args := make([]any, 0, len(x.Args))
		for _, a := range x.Args {
			v, err := evalExpr(a, scope)
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		}
		return callMethod(obj, x.Func, args)
	}
	// 裸内置
	args := make([]any, 0, len(x.Args))
	for _, a := range x.Args {
		v, err := evalExpr(a, scope)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	switch x.Func {
	case "str":
		if len(args) != 1 {
			return nil, fmt.Errorf("str wants 1 arg")
		}
		return pyStr(args[0])
	case "int":
		if len(args) != 1 {
			return nil, fmt.Errorf("int wants 1 arg")
		}
		return shimInt(args[0])
	case "float":
		if len(args) != 1 {
			return nil, fmt.Errorf("float wants 1 arg")
		}
		return shimFloat(args[0])
	case "len":
		if len(args) != 1 {
			return nil, fmt.Errorf("len wants 1 arg")
		}
		return shimLen(args[0])
	case "isinstance":
		if len(args) != 2 {
			return nil, fmt.Errorf("isinstance wants 2 args")
		}
		_, isList := args[1].(string) // "list" marker from NameExpr
		if isList {
			_, ok := args[0].([]any)
			return ok, nil
		}
		return false, nil
	case "list":
		// list(x) → x 原样返回（range/推导产物已是 []any）;list() → 空数组。
		if len(args) == 0 {
			return []any{}, nil
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("list wants 0..1 args")
		}
		return args[0], nil

	case "next":
		// next(it) / next(it, default)：本子集里 it 是**物化**的 []any。
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("next wants 1..2 args")
		}
		items, ok := args[0].([]any)
		if !ok {
			return nil, fmt.Errorf("next arg is %T, want list", args[0])
		}
		if len(items) > 0 {
			return items[0], nil
		}
		if len(args) == 2 {
			return args[1], nil
		}
		return nil, fmt.Errorf("shim: next() on empty iterator (StopIteration)")

	case "range":
		// range(stop) / range(start, stop) / range(start, stop, step) →
		// []any of int64（list(range(...)) 形态的 shim 求值）。
		if len(args) < 1 || len(args) > 3 {
			return nil, fmt.Errorf("range wants 1..3 args")
		}
		toI := func(v any) (int64, error) {
			switch n := v.(type) {
			case int64:
				return n, nil
			case float64:
				return int64(n), nil
			}
			return 0, fmt.Errorf("range arg is %T, want number", v)
		}
		var start, stop, step int64
		step = 1
		switch len(args) {
		case 1:
			v, err := toI(args[0])
			if err != nil {
				return nil, err
			}
			stop = v
		case 2:
			v0, err := toI(args[0])
			if err != nil {
				return nil, err
			}
			v1, err := toI(args[1])
			if err != nil {
				return nil, err
			}
			start, stop = v0, v1
		case 3:
			v0, err := toI(args[0])
			if err != nil {
				return nil, err
			}
			v1, err := toI(args[1])
			if err != nil {
				return nil, err
			}
			v2, err := toI(args[2])
			if err != nil {
				return nil, err
			}
			start, stop, step = v0, v1, v2
		}
		if step == 0 {
			return nil, fmt.Errorf("range step must not be 0")
		}
		var out []any
		if step > 0 {
			for i := start; i < stop; i += step {
				out = append(out, i)
			}
		} else {
			for i := start; i > stop; i += step {
				out = append(out, i)
			}
		}
		return out, nil

	case "enumerate":
		// enumerate(arr) → [(0, a0), (1, a1), ...] —— 元组以 []any{idx, item}
		// 表示,列表推导的 for i, v in ... 子句按位置解包。
		if len(args) != 1 {
			return nil, fmt.Errorf("enumerate wants 1 arg")
		}
		arr, ok := args[0].([]any)
		if !ok {
			return nil, fmt.Errorf("enumerate input is %T, want list", args[0])
		}
		out := make([]any, 0, len(arr))
		for i, v := range arr {
			out = append(out, []any{int64(i), v})
		}
		return out, nil
	}
	return nil, fmt.Errorf("shim: unknown function %q", x.Func)
}

func callMethod(obj any, name string, args []any) (any, error) {
	switch name {
	case "get":
		m, ok := obj.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(".get on %T, want dict", obj)
		}
		key, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf(".get key is %T, want string", args[0])
		}
		if v, found := m[key]; found {
			return v, nil
		}
		if len(args) > 1 {
			return args[1], nil
		}
		return nil, nil
	case "startswith":
		s, ok := obj.(string)
		if !ok {
			return nil, fmt.Errorf(".startswith on %T", obj)
		}
		return strings.HasPrefix(s, pyStrOr(args[0])), nil
	case "strip":
		// str.strip() / str.strip(chars)（pygen 的 `trim` → `x.strip()`）
		s, ok := obj.(string)
		if !ok {
			return nil, fmt.Errorf(".strip on %T", obj)
		}
		if len(args) == 0 {
			return strings.TrimSpace(s), nil
		}
		if len(args) != 1 {
			return nil, fmt.Errorf(".strip wants 0..1 args")
		}
		return strings.Trim(s, pyStrOr(args[0])), nil
	case "split":
		// str.split(sep)（pygen 的 `split`）——Python 语义：不给 sep 时按空白
		// 且丢空段；本表只覆盖 pygen 的发形态（1 个 sep 实参）。
		s, ok := obj.(string)
		if !ok {
			return nil, fmt.Errorf(".split on %T", obj)
		}
		if len(args) == 0 {
			out := []any{}
			for _, f := range strings.Fields(s) {
				out = append(out, f)
			}
			return out, nil
		}
		if len(args) != 1 {
			return nil, fmt.Errorf(".split wants 0..1 args")
		}
		sep, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf(".split sep is %T, want string", args[0])
		}
		if sep == "" {
			return nil, fmt.Errorf("shim: empty separator")
		}
		parts := strings.Split(s, sep)
		out := make([]any, len(parts))
		for i, v := range parts {
			out[i] = v
		}
		return out, nil
	case "join":
		// sep.join(items)（pygen 的 `join` → `sep.join(items)`）。Python 要求
		// 元素全是 str（TypeError 否则）——**不宽容**：宽容会让 rt-l3 绿而平台红。
		sep, ok := obj.(string)
		if !ok {
			return nil, fmt.Errorf(".join on %T, want str separator", obj)
		}
		if len(args) != 1 {
			return nil, fmt.Errorf(".join wants 1 arg")
		}
		items, ok := args[0].([]any)
		if !ok {
			return nil, fmt.Errorf(".join arg is %T, want list", args[0])
		}
		parts := make([]string, len(items))
		for i, v := range items {
			sv, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("shim: sequence item %d: expected str instance, %T found", i, v)
			}
			parts[i] = sv
		}
		return strings.Join(parts, sep), nil
	case "replace":
		// str.replace(old, new)（pygen 的 `replace`）——Python 替换全部出现。
		s, ok := obj.(string)
		if !ok {
			return nil, fmt.Errorf(".replace on %T", obj)
		}
		if len(args) != 2 {
			return nil, fmt.Errorf(".replace wants 2 args")
		}
		return strings.ReplaceAll(s, pyStrOr(args[0]), pyStrOr(args[1])), nil
	case "endswith":
		s, ok := obj.(string)
		if !ok {
			return nil, fmt.Errorf(".endswith on %T", obj)
		}
		return strings.HasSuffix(s, pyStrOr(args[0])), nil
	}
	return nil, fmt.Errorf("shim: unknown method %q", name)
}

// repeatSeq 实现 Python 的 `seq * n`（list/string × int）：n<=0 ⇒ 空。
// 返回 ok=false 表示两边不是「序列 × 整数」这一形态（交回数值路径）。
func repeatSeq(seq, n any) (any, bool) {
	cnt, ok := toInt(n)
	if !ok || cnt < 0 {
		return nil, false
	}
	switch s := seq.(type) {
	case []any:
		out := make([]any, 0, len(s)*cnt)
		for i := 0; i < cnt; i++ {
			out = append(out, s...)
		}
		return out, true
	case string:
		return strings.Repeat(s, cnt), true
	}
	return nil, false
}

func subscript(obj, idx any) (any, error) {
	switch o := obj.(type) {
	case []any:
		i, ok := toInt(idx)
		if !ok {
			return nil, fmt.Errorf("list index is %T", idx)
		}
		if i < 0 || i >= len(o) {
			return nil, fmt.Errorf("list index %d out of range (len %d)", i, len(o))
		}
		return o[i], nil
	case string:
		i, ok := toInt(idx)
		if !ok {
			return nil, fmt.Errorf("string index is %T", idx)
		}
		runes := []rune(o)
		if i < 0 || i >= len(runes) {
			return nil, fmt.Errorf("string index %d out of range", i)
		}
		return string(runes[i]), nil
	case map[string]any:
		key, ok := idx.(string)
		if !ok {
			return nil, fmt.Errorf("dict index is %T, want string", idx)
		}
		return o[key], nil
	}
	return nil, fmt.Errorf("subscript on %T", obj)
}

// ── helpers ─────────────────────────────────────────────────────────────

// shimEq compares two values (Python-ish: numeric across int/float; string
// equality; bool identity; nil == nil; mixed types → false).
func shimEq(l, r any) bool {
	if l == nil || r == nil {
		return l == nil && r == nil
	}
	if isNum(l) && isNum(r) {
		ln, _ := toNum(l)
		rn, _ := toNum(r)
		return ln == rn
	}
	return fmt.Sprint(l) == fmt.Sprint(r)
}

func shimCmp(op string, l, r any) (bool, error) {
	if ls, ok := l.(string); ok {
		if rs, ok2 := r.(string); ok2 {
			switch op {
			case "<":
				return ls < rs, nil
			case ">":
				return ls > rs, nil
			case "<=":
				return ls <= rs, nil
			case ">=":
				return ls >= rs, nil
			}
		}
	}
	ln, lok := toNum(l)
	rn, rok := toNum(r)
	if !lok || !rok {
		return false, fmt.Errorf("shim: %s on %T and %T", op, l, r)
	}
	switch op {
	case "<":
		return ln < rn, nil
	case ">":
		return ln > rn, nil
	case "<=":
		return ln <= rn, nil
	case ">=":
		return ln >= rn, nil
	}
	return false, fmt.Errorf("shim: unsupported compare %q", op)
}

func shimIn(needle, haystack any) (bool, error) {
	switch h := haystack.(type) {
	case []any:
		for _, el := range h {
			if shimEq(el, needle) {
				return true, nil
			}
		}
		return false, nil
	case string:
		return strings.Contains(h, pyStrOr(needle)), nil
	case map[string]any:
		key, ok := needle.(string)
		if !ok {
			return false, nil
		}
		_, found := h[key]
		return found, nil
	}
	return false, fmt.Errorf("shim: in on %T", haystack)
}

// shimInt 是 Python int(x) 在**本子集取值域**上的语义（R-128 第二刀补的
// 两端同解：`pkg/pygen.LowerBuiltin` 能发 `int(x)`，求值器就必须能算）。
//
// 语义（对齐 Python，不宽容）：int→本身；bool→0/1；float→**向零截断**
// （`int(-1.9) == -1`）；str→十进制整数（允许首尾空白与正负号，`"1.5"` 是
// ValueError）。**为什么不在字符串上宽容**：shim 是**要跑在平台 CodeRunner
// （真 Python）里**的产物 —— 求值器比 Python 宽 = rt-l3 绿而平台红（假绿）。
func shimInt(v any) (any, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	case float64:
		return int64(math.Trunc(x)), nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("shim: int(%q): invalid literal for int()", x)
		}
		return n, nil
	}
	return nil, fmt.Errorf("shim: int() on %T", v)
}

// shimFloat 是 Python float(x) 的同一子集语义（G14 的 `float(x)` cast
// 在求值侧的另一半 —— 2026-09-28 一手：`unknown function "float"` 10 处）。
func shimFloat(v any) (any, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case int:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case bool:
		if x {
			return 1.0, nil
		}
		return 0.0, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return nil, fmt.Errorf("shim: float(%q): could not convert string to float", x)
		}
		return f, nil
	}
	return nil, fmt.Errorf("shim: float() on %T", v)
}

func shimLen(v any) (int64, error) {
	switch x := v.(type) {
	case []any:
		return int64(len(x)), nil
	case string:
		return int64(len([]rune(x))), nil
	case map[string]any:
		return int64(len(x)), nil
	}
	return 0, fmt.Errorf("shim: len on %T", v)
}

// pyStr renders Python str(x) shapes.
func pyStr(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "None", nil
	case string:
		return x, nil
	case bool:
		if x {
			return "True", nil
		}
		return "False", nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s, nil
	case []any:
		// Python str(list) — JSON-ish for v1 (matches the generator's
		// str() usage on string operands only; kept for safety).
		b, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return fmt.Sprint(v), nil
	}
}

func pyStrOr(v any) string {
	s, _ := pyStr(v)
	return s
}

func isNum(v any) bool {
	switch v.(type) {
	case int64, float64, int, int32:
		return true
	}
	return false
}

func isIntVal(v any) bool {
	switch t := v.(type) {
	case int64, int, int32:
		return true
	case float64:
		return math.Trunc(t) == t
	case string:
		// 数值字符串（loop state / coze 输入的 JSON 形态,如 "1"）在算术中
		// 视为整数值 —— 与 expr.ToFloat / Compare 的宽容语义一致（M14）。
		f, err := strconv.ParseFloat(t, 64)
		return err == nil && math.Trunc(f) == f
	}
	return false
}

func toNum(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		// 数值字符串（loop state / coze 输入）→ 数值;非数值字符串不在此
		// 分支成功（ParseFloat 失败 → ok=false,保持原有 str 语义）。
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

// evalListComp evaluates a list comprehension as nested loops over its
// clauses. Multi-var clauses unpack the iterated item positionally
// (enumerate pairs arrive as []any{index, item}).
func evalListComp(x *pyast.ListCompExpr, scope map[string]any) ([]any, error) {
	return evalComprehension(x.Elt, x.Clauses, scope)
}

// evalComprehension 把「for 子句 + if 过滤」的推导物化成 []any（list comp 与
// generator expr 共用同一段求值 —— 本子集无惰性需求）。
func evalComprehension(elt pyast.Expr, clauses []pyast.CompClause, scope map[string]any) ([]any, error) {
	results := []any{}
	var walk func(clauseIdx int, sc map[string]any) error
	walk = func(clauseIdx int, sc map[string]any) error {
		if clauseIdx == len(clauses) {
			v, err := evalExpr(elt, sc)
			if err != nil {
				return err
			}
			results = append(results, v)
			return nil
		}
		cl := clauses[clauseIdx]
		iter, err := evalExpr(cl.Iter, sc)
		if err != nil {
			return err
		}
		items, ok := iter.([]any)
		if !ok {
			return fmt.Errorf("shim: comprehension iterable is %T, want list", iter)
		}
		for _, item := range items {
			child := make(map[string]any, len(sc)+len(cl.Vars))
			for k, v := range sc {
				child[k] = v
			}
			if len(cl.Vars) == 1 {
				child[cl.Vars[0]] = item
			} else {
				tup, ok := item.([]any)
				if !ok || len(tup) < len(cl.Vars) {
					return fmt.Errorf("shim: comprehension unpack %d vars from %T", len(cl.Vars), item)
				}
				for i, v := range cl.Vars {
					child[v] = tup[i]
				}
			}
			skip := false
			for _, f := range cl.Ifs {
				fv, err := evalExpr(f, child)
				if err != nil {
					return err
				}
				if !Truthy(fv) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			if err := walk(clauseIdx+1, child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(0, scope); err != nil {
		return nil, err
	}
	return results, nil
}
