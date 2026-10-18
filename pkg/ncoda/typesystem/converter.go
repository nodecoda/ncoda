package typesystem

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ConversionWarning 转换警告。
type ConversionWarning struct {
	Path         string
	ExpectedType DataType
	Message      string
}

func (w *ConversionWarning) String() string {
	return "[" + w.Path + "] expected " + string(w.ExpectedType) + ": " + w.Message
}

// ConversionWarnings 警告集合。
type ConversionWarnings struct {
	Warnings []ConversionWarning
}

// NewConversionWarnings 构造空警告集合。
func NewConversionWarnings() *ConversionWarnings { return &ConversionWarnings{} }

// Merge 合并另一组警告。
func (c *ConversionWarnings) Merge(other *ConversionWarnings) *ConversionWarnings {
	if other != nil {
		c.Warnings = append(c.Warnings, other.Warnings...)
	}
	return c
}

// String 返回字符串表示。
func (c *ConversionWarnings) String() string {
	parts := make([]string, 0, len(c.Warnings))
	for _, w := range c.Warnings {
		parts = append(parts, w.String())
	}
	return strings.Join(parts, "; ")
}

// ConvertOptions 转换选项。
type ConvertOptions struct {
	CollectFileFields map[string]map[string]any // 收集文件字段
	TrimQueryFileName bool
	FailFast          bool // 遇错即返回（不收集警告）
	SkipUnknownFields bool
	SkipRequireCheck  bool
}

// DefaultConvertOptions 默认转换选项。
func DefaultConvertOptions() ConvertOptions {
	return ConvertOptions{TrimQueryFileName: true}
}

// ConvertInputs 按类型信息校验+转换输入。
func ConvertInputs(inputs map[string]any, typeInfoMap map[string]*TypeInfo, opts ConvertOptions) (map[string]any, *ConversionWarnings, error) {
	converted := map[string]any{}
	var warnings *ConversionWarnings
	if !opts.FailFast {
		warnings = NewConversionWarnings()
	}

	for name, val := range inputs {
		ti, ok := typeInfoMap[name]
		if !ok {
			if !opts.SkipUnknownFields {
				converted[name] = val
			}
			continue
		}
		result, ws, err := convertValue(val, name, ti, opts)
		if err != nil {
			return nil, warnings, err
		}
		converted[name] = result
		if ws != nil && warnings != nil {
			warnings.Merge(ws)
		}
	}

	if !opts.SkipRequireCheck {
		for name, ti := range typeInfoMap {
			if ti.Required {
				if _, present := converted[name]; !present {
					return nil, warnings, fmt.Errorf("required field '%s' is missing", name)
				}
			}
		}
	}
	return converted, warnings, nil
}

func conversionFailure(path string, expected DataType, message string, opts ConvertOptions, fallback any) (any, *ConversionWarnings, error) {
	if opts.FailFast {
		return nil, nil, fmt.Errorf("%s at %s", message, path)
	}
	return fallback, &ConversionWarnings{[]ConversionWarning{{Path: path, ExpectedType: expected, Message: message}}}, nil
}

func convertValue(val any, path string, ti *TypeInfo, opts ConvertOptions) (any, *ConversionWarnings, error) {
	if val == nil {
		return nil, nil, nil
	}
	switch ti.Type {
	case DataTypeString:
		return toString(val, path, ti, opts)
	case DataTypeInteger:
		return toInt64(val, path, ti, opts)
	case DataTypeNumber:
		return toFloat64(val, path, ti, opts)
	case DataTypeBoolean:
		return toBool(val, path, ti, opts)
	case DataTypeObject:
		return toObject(val, path, ti, opts)
	case DataTypeArray:
		return toArray(val, path, ti, opts)
	case DataTypeFile:
		return toFile(val, path, ti, opts)
	case DataTypeTime:
		return toString(val, path, ti, opts)
	}
	return val, nil, nil
}

func toString(val any, path string, ti *TypeInfo, opts ConvertOptions) (any, *ConversionWarnings, error) {
	switch v := val.(type) {
	case string:
		return v, nil, nil
	case bool:
		if v {
			return "true", nil, nil
		}
		return "false", nil, nil
	case int:
		return strconv.Itoa(v), nil, nil
	case int64:
		return strconv.FormatInt(v, 10), nil, nil
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), nil, nil
		}
		return strconv.FormatFloat(v, 'f', -1, 64), nil, nil
	case []any, map[string]any:
		b, err := json.Marshal(val)
		if err != nil {
			return conversionFailure(path, DataTypeString, err.Error(), opts, nil)
		}
		return string(b), nil, nil
	}
	return conversionFailure(path, DataTypeString, "unsupported type to convert to string: "+typeName(val), opts, nil)
}

func toInt64(val any, path string, ti *TypeInfo, opts ConvertOptions) (any, *ConversionWarnings, error) {
	switch v := val.(type) {
	case int:
		return v, nil, nil
	case int64:
		return int(v), nil, nil
	case float64:
		return int(v), nil, nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return conversionFailure(path, DataTypeInteger, err.Error(), opts, nil)
		}
		return int(n), nil, nil
	}
	return conversionFailure(path, DataTypeInteger, "unsupported type to convert to int64: "+typeName(val), opts, nil)
}

func toFloat64(val any, path string, ti *TypeInfo, opts ConvertOptions) (any, *ConversionWarnings, error) {
	switch v := val.(type) {
	case int:
		return float64(v), nil, nil
	case int64:
		return float64(v), nil, nil
	case float64:
		return v, nil, nil
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return conversionFailure(path, DataTypeNumber, err.Error(), opts, nil)
		}
		return f, nil, nil
	}
	return conversionFailure(path, DataTypeNumber, "unsupported type to convert to float64: "+typeName(val), opts, nil)
}

func toBool(val any, path string, ti *TypeInfo, opts ConvertOptions) (any, *ConversionWarnings, error) {
	switch v := val.(type) {
	case bool:
		return v, nil, nil
	case string:
		switch v {
		case "1", "t", "T", "true", "TRUE", "True":
			return true, nil, nil
		case "0", "f", "F", "false", "FALSE", "False":
			return false, nil, nil
		}
		return conversionFailure(path, DataTypeBoolean, fmt.Sprintf("invalid boolean syntax: %q", v), opts, nil)
	}
	return conversionFailure(path, DataTypeBoolean, "unsupported type to convert to bool: "+typeName(val), opts, nil)
}

func toObject(val any, path string, ti *TypeInfo, opts ConvertOptions) (any, *ConversionWarnings, error) {
	var warnings *ConversionWarnings
	if !opts.FailFast {
		warnings = NewConversionWarnings()
	}
	if s, ok := val.(string); ok {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return conversionFailure(path, DataTypeObject, err.Error(), opts, nil)
		}
		val = parsed
	}
	m, ok := val.(map[string]any)
	if !ok {
		return conversionFailure(path, DataTypeObject, "unsupported type to convert to object: "+typeName(val), opts, nil)
	}
	result := map[string]any{}
	for propName, propTI := range ti.Properties {
		if pv, present := m[propName]; present {
			subPath := path + "." + propName
			converted, ws, err := convertValue(pv, subPath, propTI, opts)
			if err != nil {
				return nil, warnings, err
			}
			result[propName] = converted
			if ws != nil && warnings != nil {
				warnings.Merge(ws)
			}
		} else if propTI.Required && !opts.SkipRequireCheck {
			if opts.FailFast {
				return nil, nil, fmt.Errorf("required property '%s' missing at %s", propName, path)
			}
			if warnings != nil {
				warnings.Warnings = append(warnings.Warnings, ConversionWarning{
					Path: path + "." + propName, ExpectedType: propTI.Type,
					Message: "required property is missing",
				})
			}
		}
	}
	if !opts.SkipUnknownFields {
		for k, v := range m {
			if _, known := ti.Properties[k]; !known {
				result[k] = v
			}
		}
	}
	return result, warnings, nil
}

func toArray(val any, path string, ti *TypeInfo, opts ConvertOptions) (any, *ConversionWarnings, error) {
	var warnings *ConversionWarnings
	if !opts.FailFast {
		warnings = NewConversionWarnings()
	}
	if s, ok := val.(string); ok {
		var parsed []any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return conversionFailure(path, DataTypeArray, err.Error(), opts, []any{})
		}
		val = parsed
	}
	list, ok := val.([]any)
	if !ok {
		return conversionFailure(path, DataTypeArray, "unsupported type to convert to array: "+typeName(val), opts, []any{})
	}
	elemTI := ti.ElemTypeInfo
	if elemTI == nil {
		elemTI = TypeInfoString(false, "")
	}
	result := []any{}
	for i, item := range list {
		subPath := fmt.Sprintf("%s[%d]", path, i)
		converted, ws, err := convertValue(item, subPath, elemTI, opts)
		if err != nil {
			return nil, warnings, err
		}
		if ws == nil || elemTI.Type == DataTypeObject {
			result = append(result, converted)
		}
		if ws != nil && warnings != nil {
			warnings.Merge(ws)
		}
	}
	return result, warnings, nil
}

func toFile(val any, path string, ti *TypeInfo, opts ConvertOptions) (any, *ConversionWarnings, error) {
	converted, warnings, err := toString(val, path, ti, opts)
	if err != nil || warnings != nil {
		return converted, warnings, err
	}
	s, ok := converted.(string)
	if !ok {
		return converted, warnings, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return converted, warnings, nil
	}
	q := u.Query()
	fileName := q.Get("x-wf-file_name")
	q.Del("x-wf-file_name")
	u.RawQuery = q.Encode()
	cleaned := u.String()
	fileType := "default"
	if ti.FileType != nil {
		fileType = string(*ti.FileType)
	}
	if opts.CollectFileFields != nil {
		opts.CollectFileFields[cleaned] = map[string]any{
			"file_name": fileName,
			"file_type": fileType,
		}
	}
	if opts.TrimQueryFileName {
		return cleaned, nil, nil
	}
	return s, nil, nil
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float64:
		return "float64"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	}
	return fmt.Sprintf("%T", v)
}
