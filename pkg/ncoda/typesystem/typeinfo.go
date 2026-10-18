package typesystem

import "sort"

// TypeInfo 递归类型描述符（对应 ncoda_coze.type_system.typeinfo.TypeInfo）。
type TypeInfo struct {
	Type         DataType
	ElemTypeInfo *TypeInfo
	FileType     *FileSubType
	Required     bool
	// RequiredExplicit distinguishes an explicit platform required:false from
	// the zero value used by synthetic/inferred descriptors.
	RequiredExplicit bool
	Desc             string
	Properties       map[string]*TypeInfo
}

// NewTypeInfo 构造基础 TypeInfo。
func NewTypeInfo(t DataType) *TypeInfo {
	return &TypeInfo{Type: t}
}

// TypeInfoString 构造 string 类型。
func TypeInfoString(required bool, desc string) *TypeInfo {
	return &TypeInfo{Type: DataTypeString, Required: required, Desc: desc}
}

// TypeInfoInteger 构造 integer 类型。
func TypeInfoInteger(required bool, desc string) *TypeInfo {
	return &TypeInfo{Type: DataTypeInteger, Required: required, Desc: desc}
}

// TypeInfoNumber 构造 number 类型。
func TypeInfoNumber(required bool, desc string) *TypeInfo {
	return &TypeInfo{Type: DataTypeNumber, Required: required, Desc: desc}
}

// TypeInfoBoolean 构造 boolean 类型。
func TypeInfoBoolean(required bool, desc string) *TypeInfo {
	return &TypeInfo{Type: DataTypeBoolean, Required: required, Desc: desc}
}

// TypeInfoArray 构造数组类型。
func TypeInfoArray(elem *TypeInfo, required bool, desc string) *TypeInfo {
	return &TypeInfo{Type: DataTypeArray, ElemTypeInfo: elem, Required: required, Desc: desc}
}

// TypeInfoObject 构造对象类型。
func TypeInfoObject(properties map[string]*TypeInfo, required bool, desc string) *TypeInfo {
	return &TypeInfo{Type: DataTypeObject, Properties: properties, Required: required, Desc: desc}
}

// TypeInfoFile 构造文件类型。
func TypeInfoFile(fileType FileSubType, required bool, desc string) *TypeInfo {
	return &TypeInfo{Type: DataTypeFile, FileType: &fileType, Required: required, Desc: desc}
}

// TypeInfoFromDict 从 coze JSON 结构反序列化 TypeInfo。
func TypeInfoFromDict(d map[string]any) *TypeInfo {
	typeStr, _ := d["type"].(string)
	if typeStr == "" {
		typeStr = "string"
	}
	t, err := DataTypeFromString(typeStr)
	if err != nil {
		t = DataTypeString
	}
	ti := &TypeInfo{Type: t}
	if req, ok := d["required"].(bool); ok {
		ti.Required = req
		ti.RequiredExplicit = true
	}
	if desc, ok := d["desc"].(string); ok {
		ti.Desc = desc
	}
	if raw, ok := d["elem_type_info"].(map[string]any); ok && len(raw) > 0 {
		ti.ElemTypeInfo = TypeInfoFromDict(raw)
	}
	if raw, ok := d["file_type"].(string); ok {
		ft := FileSubTypeFromString(raw)
		if ft != nil {
			ti.FileType = ft
		}
	}
	if raw, ok := d["properties"].(map[string]any); ok && len(raw) > 0 {
		ti.Properties = make(map[string]*TypeInfo, len(raw))
		for k, v := range raw {
			if vm, ok := v.(map[string]any); ok {
				ti.Properties[k] = TypeInfoFromDict(vm)
			}
		}
	}
	return ti
}

// TypeInfoFromVariableType 是 VariableType -> TypeInfo 简单转换。
func TypeInfoFromVariableType(vt VariableType) *TypeInfo {
	return &TypeInfo{Type: vt.ToDataType()}
}

// ToDict 序列化为 dict。
func (ti *TypeInfo) ToDict() map[string]any {
	d := map[string]any{
		"type":     string(ti.Type),
		"required": ti.Required,
	}
	if ti.Desc != "" {
		d["desc"] = ti.Desc
	}
	if ti.ElemTypeInfo != nil {
		d["elem_type_info"] = ti.ElemTypeInfo.ToDict()
	}
	if ti.FileType != nil {
		d["file_type"] = string(*ti.FileType)
	}
	if len(ti.Properties) > 0 {
		props := make(map[string]any, len(ti.Properties))
		for k, v := range ti.Properties {
			props[k] = v.ToDict()
		}
		d["properties"] = props
	}
	return d
}

// IsSimple 判断简单类型（无递归结构）。
func (ti *TypeInfo) IsSimple() bool {
	switch ti.Type {
	case DataTypeString, DataTypeInteger, DataTypeNumber, DataTypeBoolean, DataTypeTime:
		return true
	}
	return false
}

// IsArray 判断数组类型。
func (ti *TypeInfo) IsArray() bool { return ti.Type == DataTypeArray }

// IsObject 判断对象类型。
func (ti *TypeInfo) IsObject() bool { return ti.Type == DataTypeObject }

// IsFile 判断文件类型。
func (ti *TypeInfo) IsFile() bool { return ti.Type == DataTypeFile }

// Equals 深度类型相等比较。
func (ti *TypeInfo) Equals(other *TypeInfo) bool {
	if ti == nil || other == nil {
		return ti == nil && other == nil
	}
	if ti.Type != other.Type || ti.Required != other.Required || ti.Desc != other.Desc {
		return false
	}
	if (ti.FileType == nil) != (other.FileType == nil) {
		return false
	}
	if ti.FileType != nil && *ti.FileType != *other.FileType {
		return false
	}
	if (ti.ElemTypeInfo == nil) != (other.ElemTypeInfo == nil) {
		return false
	}
	if ti.ElemTypeInfo != nil && !ti.ElemTypeInfo.Equals(other.ElemTypeInfo) {
		return false
	}
	if len(ti.Properties) != len(other.Properties) {
		return false
	}
	for k, v := range ti.Properties {
		ov, ok := other.Properties[k]
		if !ok || !v.Equals(ov) {
			return false
		}
	}
	return true
}

// PropertyKeys 返回有序属性键（稳定遍历用）。
func (ti *TypeInfo) PropertyKeys() []string {
	keys := make([]string, 0, len(ti.Properties))
	for k := range ti.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// String 返回开发者可读表示。
func (ti *TypeInfo) String() string {
	if ti == nil {
		return "<nil>"
	}
	if ti.IsArray() && ti.ElemTypeInfo != nil {
		return "Array<" + ti.ElemTypeInfo.String() + ">"
	}
	if ti.IsObject() {
		return "Object"
	}
	if ti.IsFile() && ti.FileType != nil {
		return "File<" + string(*ti.FileType) + ">"
	}
	return string(ti.Type)
}
