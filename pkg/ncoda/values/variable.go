package values

// RefSourceType 引用来源类型（对应 ncoda_coze.values.variable.RefSourceType）。
type RefSourceType string

const (
	RefSourceBlockOutput RefSourceType = "block-output"
	RefSourceGlobalApp   RefSourceType = "global_variable_app"
	RefSourceGlobalSys   RefSourceType = "global_variable_system"
	RefSourceGlobalUser  RefSourceType = "global_variable_user"
)

// RefSourceTypeFromString 从字符串构造；空/未知回退 BLOCK_OUTPUT。
func RefSourceTypeFromString(s string) RefSourceType {
	switch RefSourceType(s) {
	case RefSourceBlockOutput, RefSourceGlobalApp, RefSourceGlobalSys, RefSourceGlobalUser:
		return RefSourceType(s)
	default:
		return RefSourceBlockOutput
	}
}

// IsGlobal 判断是否全局引用。
func (r RefSourceType) IsGlobal() bool {
	return r == RefSourceGlobalApp || r == RefSourceGlobalSys || r == RefSourceGlobalUser
}

// GlobalVarType 全局变量类型。
type GlobalVarType string

const (
	GlobalVarParentIntermediate GlobalVarType = "parent_intermediate"
	GlobalVarUser               GlobalVarType = "global_user"
	GlobalVarSystem             GlobalVarType = "global_system"
	GlobalVarApp                GlobalVarType = "global_app"
)

// GlobalVarTypeFromString 从字符串构造；空/未知返回 nil。
func GlobalVarTypeFromString(s string) *GlobalVarType {
	switch GlobalVarType(s) {
	case GlobalVarParentIntermediate, GlobalVarUser, GlobalVarSystem, GlobalVarApp:
		v := GlobalVarType(s)
		return &v
	default:
		return nil
	}
}
