package typesystem

import "fmt"

// DataType 节点 I/O schema 类型（对应 ncoda_coze.type_system.datatype.DataType）。
type DataType string

const (
	DataTypeString  DataType = "string"
	DataTypeInteger DataType = "integer"
	DataTypeNumber  DataType = "number"
	DataTypeBoolean DataType = "boolean"
	DataTypeTime    DataType = "time"
	DataTypeObject  DataType = "object"
	DataTypeArray   DataType = "list"
	DataTypeFile    DataType = "file"
)

// DataTypeFromString 容错解析：兼容 "array" / "float" / "int" / "bool" 等变体。
func DataTypeFromString(s string) (DataType, error) {
	switch s {
	case "array", "list":
		return DataTypeArray, nil
	case "float", "number", "double":
		return DataTypeNumber, nil
	case "int", "integer", "long":
		return DataTypeInteger, nil
	case "bool", "boolean":
		return DataTypeBoolean, nil
	}
	switch DataType(s) {
	case DataTypeString, DataTypeTime, DataTypeObject, DataTypeFile:
		return DataType(s), nil
	}
	return "", fmt.Errorf("unknown DataType: %q", s)
}

// VariableType Canvas BlockInput 类型。
type VariableType string

const (
	VariableTypeString  VariableType = "string"
	VariableTypeInteger VariableType = "integer"
	VariableTypeFloat   VariableType = "float"
	VariableTypeBoolean VariableType = "boolean"
	VariableTypeObject  VariableType = "object"
	VariableTypeList    VariableType = "list"
)

// ToDataType 是 VariableType -> DataType 转换。
func (v VariableType) ToDataType() DataType {
	switch v {
	case VariableTypeString:
		return DataTypeString
	case VariableTypeInteger:
		return DataTypeInteger
	case VariableTypeFloat:
		return DataTypeNumber
	case VariableTypeBoolean:
		return DataTypeBoolean
	case VariableTypeObject:
		return DataTypeObject
	case VariableTypeList:
		return DataTypeArray
	}
	return DataTypeString
}
