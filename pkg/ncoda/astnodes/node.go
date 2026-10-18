package astnodes

import (
	"reflect"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ncoda/typesystem"
	"github.com/nodecoda/ncoda/pkg/ncoda/values"
)

// NodeDecl 是所有节点类型的接口。
type NodeDecl interface {
	// 公共字段访问器
	GetKey() string
	GetNodeType() NodeType
	GetName() string
	GetInputTypes() map[string]*typesystem.TypeInfo
	GetOutputTypes() map[string]*typesystem.TypeInfo
	GetInputSources() []values.FieldInfo
	GetOutputSources() []values.FieldInfo
	GetExceptionConfig() *ExceptionConfig
	GetParentKey() string
	// 序列化
	Kind() string
	ToConfigDict() map[string]any
}

// NodeBase 是所有节点共享的公共字段（内嵌到各节点结构体）。
type NodeBase struct {
	Key             string
	NodeType        NodeType
	Name            string
	InputTypes      map[string]*typesystem.TypeInfo
	OutputTypes     map[string]*typesystem.TypeInfo
	InputSources    []values.FieldInfo
	OutputSources   []values.FieldInfo
	ExceptionConfig *ExceptionConfig
	ParentKey       string
}

func (b *NodeBase) GetKey() string        { return b.Key }
func (b *NodeBase) GetNodeType() NodeType { return b.NodeType }
func (b *NodeBase) GetName() string       { return b.Name }
func (b *NodeBase) GetInputTypes() map[string]*typesystem.TypeInfo {
	return b.InputTypes
}
func (b *NodeBase) GetOutputTypes() map[string]*typesystem.TypeInfo {
	return b.OutputTypes
}
func (b *NodeBase) GetInputSources() []values.FieldInfo  { return b.InputSources }
func (b *NodeBase) GetOutputSources() []values.FieldInfo { return b.OutputSources }
func (b *NodeBase) GetExceptionConfig() *ExceptionConfig { return b.ExceptionConfig }
func (b *NodeBase) GetParentKey() string                 { return b.ParentKey }

// OutputTypeAt 沿路径取输出类型。
func (b *NodeBase) OutputTypeAt(path []string) *typesystem.TypeInfo {
	if len(path) == 0 {
		return nil
	}
	current, ok := b.OutputTypes[path[0]]
	if !ok {
		return nil
	}
	for _, part := range path[1:] {
		if current == nil {
			return nil
		}
		if current.IsArray() {
			current = current.ElemTypeInfo
			if current == nil {
				return nil
			}
			if part >= "0" && part <= "999999999" {
				continue
			}
		}
		if !current.IsObject() {
			return nil
		}
		current = current.Properties[part]
	}
	return current
}

// ChildVisibleTypeAt 复合节点暴露给子节点的类型。
func (b *NodeBase) ChildVisibleTypeAt(path []string) *typesystem.TypeInfo {
	if len(path) == 0 || !b.NodeType.IsComposite() {
		return nil
	}
	var current *typesystem.TypeInfo
	if path[0] == "index" {
		current = typesystem.TypeInfoInteger(false, "")
	} else {
		current = b.InputTypes[path[0]]
		intermediateNames := map[string]struct{}{}
		if iv, ok := any(b).(interface {
			GetIntermediateTypes() map[string]*typesystem.TypeInfo
		}); ok {
			for k := range iv.GetIntermediateTypes() {
				intermediateNames[k] = struct{}{}
			}
		}
		if _, isInter := intermediateNames[path[0]]; !isInter {
			if current == nil || !current.IsArray() {
				return nil
			}
			current = current.ElemTypeInfo
		}
	}
	for _, part := range path[1:] {
		if current == nil || !current.IsObject() {
			return nil
		}
		current = current.Properties[part]
	}
	return current
}

// ToConfigDict 将节点专有字段序列化为 IR config dict。
// 通过反射遍历内嵌 NodeBase 之外的所有导出字段，递归归一化。
func (b *NodeBase) ToConfigDict(node any) map[string]any {
	result := map[string]any{}
	rv := reflect.ValueOf(node)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.Anonymous {
			continue // 跳过内嵌 NodeBase
		}
		result[ConfigKey(field.Name)] = normalizeConfigValue(rv.Field(i).Interface())
	}
	if k, ok := node.(interface{ Kind() string }); ok {
		result["kind"] = k.Kind()
	}
	return result
}

// normalizeConfigValue 递归归一化：struct -> dict（TypeInfo 特判）、slice -> list。
func normalizeConfigValue(value any) any {
	switch v := value.(type) {
	case *typesystem.TypeInfo:
		if v == nil {
			return nil
		}
		return v.ToDict()
	case typesystem.TypeInfo:
		t := v
		return (&t).ToDict()
	case *ExceptionConfig:
		if v == nil {
			return nil
		}
		return map[string]any{
			"timeout_ms":   v.TimeoutMS,
			"max_retry":    v.MaxRetry,
			"data_on_err":  v.DataOnErr,
			"process_type": v.ProcessType,
		}
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Struct:
		out := map[string]any{}
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" {
				continue // 未导出
			}
			out[f.Name] = normalizeConfigValue(rv.Field(i).Interface())
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, normalizeConfigValue(rv.Index(i).Interface()))
		}
		return out
	case reflect.Map:
		out := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			k := iter.Key()
			ks, ok := k.Interface().(string)
			if !ok {
				ks = k.String()
			}
			out[ks] = normalizeConfigValue(iter.Value().Interface())
		}
		return out
	case reflect.Ptr:
		if rv.IsNil() {
			return nil
		}
		return normalizeConfigValue(rv.Elem().Interface())
	case reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return normalizeConfigValue(rv.Elem().Interface())
	}
	return value
}

// configAcronyms 是 Go 字段名中的缩写 -> 单词拆分表（对齐 Python 字段命名）。
var configAcronyms = []struct{ from, to string }{
	{"NL2SQL", "Nl2sql"},
	{"JSON", "Json"},
	{"HTTP", "Http"},
	{"URL", "Url"},
	{"SQL", "Sql"},
	{"OCR", "Ocr"},
	{"IDs", "Ids"},
	{"ID", "Id"},
	{"MS", "Ms"},
}

// ConfigKey 将 Go 导出字段名转为 IR config 的 snake_case key。
// 例: ModelID -> model_id, URLTemplate -> url_template, EnableNL2SQL -> enable_nl2sql。
func ConfigKey(fieldName string) string {
	name := fieldName
	for _, a := range configAcronyms {
		name = strings.ReplaceAll(name, a.from, a.to)
	}
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
