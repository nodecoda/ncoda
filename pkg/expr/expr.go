// Package expr — M2.0 公共求值层(从 compile stub 抽出的原语)。
//
// 职责:selector 条件比较、模板渲染、类型转换。节点实现与 compile 层
// 复用同一套语义(确定性、与 coze SelectorOperators 编号兼容)。纯函数,
// 不依赖 spec/compile,任何执行后端(当前 Eino 调度、未来自研 interp)
// 都直接使用。
package expr

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Compare implements the coze SelectorOperators subset:
//
//	1=`=`  2=`!=`  7=contain  8=not_contain  9=empty  10=not_empty
//	11=true  12=false  13=`>`  14=`>=`  15=`<`  16=`<=`
//
// 未知 op 一律 false(与 M1 行为一致;条件不满足 = 不选中该分支)。
func Compare(op int, lv, rv any) bool {
	switch op {
	case 1:
		return fmt.Sprint(lv) == fmt.Sprint(rv)
	case 2:
		return fmt.Sprint(lv) != fmt.Sprint(rv)
	case 7:
		return strings.Contains(fmt.Sprint(lv), fmt.Sprint(rv))
	case 8:
		return !strings.Contains(fmt.Sprint(lv), fmt.Sprint(rv))
	case 9:
		return fmt.Sprint(lv) == ""
	case 10:
		return fmt.Sprint(lv) != ""
	case 11:
		return Truthy(lv)
	case 12:
		return false
	case 13:
		l, r, ok := NumericPair(lv, rv)
		return ok && l > r
	case 14:
		l, r, ok := NumericPair(lv, rv)
		return ok && l >= r
	case 15:
		l, r, ok := NumericPair(lv, rv)
		return ok && l < r
	case 16:
		l, r, ok := NumericPair(lv, rv)
		return ok && l <= r
	}
	return false
}

// Truthy reports whether v is truthy (bool as-is; nil false; anything
// else by non-empty Sprint).
func Truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case nil:
		return false
	default:
		return fmt.Sprint(v) != ""
	}
}

// NumericPair coerces both operands to float64. A non-numeric operand
// makes the pair unusable (ok=false).
func NumericPair(lv, rv any) (float64, float64, bool) {
	l, lok := ToFloat(lv)
	r, rok := ToFloat(rv)
	return l, r, lok && rok
}

// ToFloat coerces numeric-like values to float64: float64/float32,
// int/int64, json.Number, and numeric strings (via ParseFloat).
func ToFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	}
	f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
	return f, err == nil
}

// RenderTemplate replaces {{key}} placeholders with fmt.Sprint(vars[key]).
// Missing or nil values render as "<missing>" (deterministic contract —
// callers that need strict behavior pre-validate their vars).
func RenderTemplate(tpl string, vars map[string]any) string {
	var sb strings.Builder
	for {
		idx := strings.Index(tpl, "{{")
		if idx < 0 {
			sb.WriteString(tpl)
			break
		}
		sb.WriteString(tpl[:idx])
		tpl = tpl[idx+2:]
		end := strings.Index(tpl, "}}")
		if end < 0 {
			sb.WriteString(tpl)
			break
		}
		key := strings.TrimSpace(tpl[:end])
		tpl = tpl[end+2:]
		if v, ok := vars[key]; ok && v != nil {
			sb.WriteString(FormatTemplateValue(v))
		} else {
			sb.WriteString("<missing>")
		}
	}
	return sb.String()
}

// FormatTemplateValue 把模板占位符值渲染成文本:
//   - 复合值（map/slice）→ 紧凑 JSON（注入 LLM prompt 可读、确定性;
//     knowledge outputList 之类结构体数组不再退化成 fmt.Sprint 的
//     [map[content:...]]）
//   - 标量（string/int/float/bool/其他）→ fmt.Sprint（原契约不变）
//
// JSON 序列化失败（理论不可能）回退 fmt.Sprint。nil 由调用方处理
// （RenderTemplate 渲染 <missing>）。
func FormatTemplateValue(v any) string {
	switch v.(type) {
	case map[string]any, []any:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	case string:
		return v.(string)
	}
	return fmt.Sprint(v)
}
