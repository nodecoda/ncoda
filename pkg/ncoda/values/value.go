package values

import (
	"github.com/nodecoda/ncoda/pkg/ncoda/typesystem"
)

// BlockInputValueType 值类型（对应 ncoda_coze.values.value.BlockInputValueType）。
type BlockInputValueType string

const (
	BlockInputValueLiteral   BlockInputValueType = "literal"
	BlockInputValueRef       BlockInputValueType = "ref"
	BlockInputValueObjectRef BlockInputValueType = "object_ref"
)

// BlockInputValueTypeFromString 从字符串构造；空/未知回退 LITERAL。
func BlockInputValueTypeFromString(s string) BlockInputValueType {
	switch BlockInputValueType(s) {
	case BlockInputValueLiteral, BlockInputValueRef, BlockInputValueObjectRef:
		return BlockInputValueType(s)
	default:
		return BlockInputValueLiteral
	}
}

// BlockInputValue 输入值（字面量或引用）。
type BlockInputValue struct {
	Type    BlockInputValueType
	Content any
	RawMeta any
}

// BlockInputValueFromDict 从 dict 构造。
func BlockInputValueFromDict(d map[string]any) *BlockInputValue {
	v := &BlockInputValue{
		Type:    BlockInputValueTypeFromString(asString(d["type"])),
		RawMeta: d["rawMeta"],
	}
	content := d["content"]
	if v.Type == BlockInputValueRef {
		if cm, ok := content.(map[string]any); ok {
			v.Content = BlockInputReferenceFromDict(cm)
		} else {
			v.Content = content
		}
	} else {
		v.Content = content
	}
	return v
}

// IsRef 判断是否引用。
func (v *BlockInputValue) IsRef() bool { return v.Type == BlockInputValueRef }

// IsLiteral 判断是否字面量。
func (v *BlockInputValue) IsLiteral() bool { return v.Type == BlockInputValueLiteral }

// GetReference 获取引用（非 ref 时返回 nil）。
func (v *BlockInputValue) GetReference() *BlockInputReference {
	if v.IsRef() {
		if r, ok := v.Content.(*BlockInputReference); ok {
			return r
		}
	}
	return nil
}

// AssistType 辅助类型常量。
const (
	AssistTypeNotSet  = 0
	AssistTypeDefault = 1
	AssistTypeImage   = 2
	AssistTypeDoc     = 3
	AssistTypeCode    = 4
	AssistTypePPT     = 5
	AssistTypeTXT     = 6
	AssistTypeExcel   = 7
	AssistTypeAudio   = 8
	AssistTypeZip     = 9
	AssistTypeVideo   = 10
	AssistTypeSVG     = 11
	AssistTypeVoice   = 12
	AssistTypeTime    = 10000
)

// BlockInput 节点输入块（对应 ncoda_coze.values.value.BlockInput）。
type BlockInput struct {
	Type       typesystem.VariableType
	AssistType int
	Schema     any
	Value      *BlockInputValue
	// TypeExplicit 类型是否由平台显式声明（dict 携带非空 type 键）。
	// yaml 简化引用 {path, ref_node} 无 type → false（默认 string 占位）；
	// json inputParameters / yaml 带 type 的输入 → true。供反编译阶段区分
	// 「声明类型」（平台契约，回填不得覆盖）与「默认占位」（以上游 schema
	// 回填推断）。
	TypeExplicit bool
}

// BlockInputFromDict 从 dict 构造。
func BlockInputFromDict(d map[string]any) *BlockInput {
	bi := &BlockInput{}
	typeStr := asString(d["type"])
	if typeStr == "" {
		typeStr = "string"
	} else {
		bi.TypeExplicit = true
	}
	switch typesystem.VariableType(typeStr) {
	case typesystem.VariableTypeString, typesystem.VariableTypeInteger,
		typesystem.VariableTypeFloat, typesystem.VariableTypeBoolean,
		typesystem.VariableTypeObject, typesystem.VariableTypeList:
		bi.Type = typesystem.VariableType(typeStr)
	default:
		bi.Type = typesystem.VariableTypeString
	}
	if v, ok := d["assistType"].(int); ok {
		bi.AssistType = v
	}
	bi.Schema = d["schema"]
	if raw, ok := d["value"]; ok && raw != nil {
		if vm, ok := raw.(map[string]any); ok {
			bi.Value = BlockInputValueFromDict(vm)
		} else {
			// 真实商业导出可能携带裸标量字面量（如 HTTP header 输入），
			// 包装为 LITERAL 以保留值（含 falsy 0 / 空串）。
			bi.Value = &BlockInputValue{
				Type:    BlockInputValueLiteral,
				Content: raw,
			}
		}
	}
	return bi
}
