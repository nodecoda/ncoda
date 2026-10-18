package typesystem

import "encoding/json"

// KeyIsFinished 流式输出中追加的标记（对齐 nodes/stream.go）。
const KeyIsFinished = "\x1FKey is finished\x1F"

// zeroValue 对齐 vo.TypeInfo.Zero()。
func zeroValue(ti *TypeInfo) any {
	switch ti.Type {
	case DataTypeString:
		return ""
	case DataTypeInteger:
		return 0
	case DataTypeNumber:
		return 0.0
	case DataTypeBoolean:
		return false
	case DataTypeTime:
		return ""
	case DataTypeFile:
		return ""
	case DataTypeObject:
		return nil
	case DataTypeArray:
		return nil
	}
	return nil
}

var missing = &struct{}{}

// FillIfNotRequired 对齐 FillIfNotRequired。
// strategy: "zero" (FillZero) 或 "nil" (FillNil)。
func FillIfNotRequired(ti *TypeInfo, container map[string]any, key, strategy string, isInsideObject bool) error {
	val, present := container[key]

	if present {
		// Key present
		if len(ti.Properties) == 0 {
			// Scalar or array without element properties
			if val == nil && strategy == "zero" {
				if !isInsideObject {
					container[key] = zeroValue(ti)
				}
				// else: leave as nil
			} else if val != nil && ti.Type == DataTypeArray {
				return coerceArray(ti, container, key, strategy)
			}
			return nil
		}
		// Object with properties
		if val == nil {
			return nil // leave as nil
		}
		m, ok := val.(map[string]any)
		if !ok {
			if s, isStr := val.(string); isStr {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(s), &parsed); err != nil {
					return err
				}
				m = parsed
				container[key] = m
			} else {
				return errNotMap(key)
			}
		}
		newSub := map[string]any{}
		for k, v := range m {
			newSub[k] = v
		}
		for subK, subTI := range ti.Properties {
			if err := FillIfNotRequired(subTI, newSub, subK, strategy, true); err != nil {
				return err
			}
		}
		container[key] = newSub
		return nil
	}

	// Key not present
	if ti.Required {
		return errRequired(key)
	}
	var z any
	if strategy == "zero" && !isInsideObject {
		z = zeroValue(ti)
	}
	container[key] = z
	if len(ti.Properties) > 0 {
		z = map[string]any{}
		container[key] = z
		for subK, subTI := range ti.Properties {
			if err := FillIfNotRequired(subTI, z.(map[string]any), subK, strategy, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// FillZero 对齐 FillZero（PreFillZero）。
func FillZero(ti *TypeInfo, container map[string]any, key string) error {
	return FillIfNotRequired(ti, container, key, "zero", false)
}

// FillNil 对齐 FillNil（PostFillNil）。
func FillNil(ti *TypeInfo, container map[string]any, key string) error {
	return FillIfNotRequired(ti, container, key, "nil", false)
}

func coerceArray(ti *TypeInfo, container map[string]any, key, strategy string) error {
	val := container[key]
	elemTI := ti.ElemTypeInfo
	if elemTI == nil {
		return nil
	}
	// 元素是对象时对每个元素应用 fill 策略
	switch items := val.(type) {
	case []any:
		for i, item := range items {
			if m, ok := item.(map[string]any); ok {
				newItem := map[string]any{}
				for k, v := range m {
					newItem[k] = v
				}
				for subK, subTI := range elemTI.Properties {
					if err := FillIfNotRequired(subTI, newItem, subK, strategy, true); err != nil {
						return err
					}
				}
				items[i] = newItem
			}
		}
		container[key] = items
	}
	return nil
}

func errNotMap(key string) error {
	return &fillError{"layer field " + key + " is not a map[string]any or string"}
}

func errRequired(key string) error {
	return &fillError{"output field " + key + " is required but not present"}
}

type fillError struct{ msg string }

func (e *fillError) Error() string { return e.msg }
