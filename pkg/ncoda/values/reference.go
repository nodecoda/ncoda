package values

// BlockInputReference Canvas 层引用（对应 ncoda_coze.values.reference.BlockInputReference）。
type BlockInputReference struct {
	BlockID string
	Name    string
	Path    []string
	Source  RefSourceType
}

// BlockInputReferenceFromDict 从 dict 构造。
func BlockInputReferenceFromDict(d map[string]any) *BlockInputReference {
	r := &BlockInputReference{Source: RefSourceBlockOutput}
	if v, ok := d["blockID"].(string); ok {
		r.BlockID = v
	}
	if v, ok := d["name"].(string); ok {
		r.Name = v
	}
	if raw, ok := d["path"].([]any); ok {
		for _, p := range raw {
			if s, ok := p.(string); ok {
				r.Path = append(r.Path, s)
			}
		}
	}
	r.Source = RefSourceTypeFromString(asString(d["source"]))
	return r
}

// ToDict 转换为 dict。
func (r *BlockInputReference) ToDict() map[string]any {
	return map[string]any{
		"blockID": r.BlockID,
		"name":    r.Name,
		"path":    r.Path,
		"source":  string(r.Source),
	}
}

// FileExtra 文件额外信息。
type FileExtra struct {
	FileName  string
	FileNames []string
}

// FileExtraFromDict 从 dict 构造。
func FileExtraFromDict(d map[string]any) *FileExtra {
	fe := &FileExtra{}
	if v, ok := d["file_name"].(string); ok {
		fe.FileName = v
	}
	if raw, ok := d["file_names"].([]any); ok {
		for _, p := range raw {
			if s, ok := p.(string); ok {
				fe.FileNames = append(fe.FileNames, s)
			}
		}
	}
	return fe
}

// ToDict 转换为 dict。
func (fe *FileExtra) ToDict() map[string]any {
	return map[string]any{
		"file_name":  fe.FileName,
		"file_names": fe.FileNames,
	}
}

// Reference 编译期引用（对应 ncoda_coze.values.reference.Reference）。
type Reference struct {
	FromNodeKey  string
	FromPath     []string
	VariableType *GlobalVarType
}

// IsGlobalVariable 判断是否全局变量引用。
func (r *Reference) IsGlobalVariable() bool { return r.VariableType != nil }

// IsParentIntermediate 判断是否父工作流中间变量。
func (r *Reference) IsParentIntermediate() bool {
	return r.VariableType != nil && *r.VariableType == GlobalVarParentIntermediate
}

// ToDict 转换为 dict。
func (r *Reference) ToDict() map[string]any {
	result := map[string]any{
		"from_node_key": r.FromNodeKey,
		"from_path":     r.FromPath,
	}
	if r.VariableType != nil {
		result["variable_type"] = string(*r.VariableType)
	}
	return result
}

// ReferenceFromDict 从 dict 构造。
func ReferenceFromDict(d map[string]any) *Reference {
	r := &Reference{}
	if v, ok := d["from_node_key"].(string); ok {
		r.FromNodeKey = v
	}
	if raw, ok := d["from_path"].([]any); ok {
		for _, p := range raw {
			if s, ok := p.(string); ok {
				r.FromPath = append(r.FromPath, s)
			}
		}
	}
	r.VariableType = GlobalVarTypeFromString(asString(d["variable_type"]))
	return r
}

// FieldSource 字段值来源（三选一：ref / val / file_extra）。
type FieldSource struct {
	Ref       *Reference
	Val       any
	FileExtra *FileExtra
}

// IsRef 判断是否引用。
func (fs *FieldSource) IsRef() bool { return fs.Ref != nil }

// IsLiteral 判断是否字面量。
func (fs *FieldSource) IsLiteral() bool { return fs.Ref == nil && fs.Val != nil }

// IsFile 判断是否文件。
func (fs *FieldSource) IsFile() bool { return fs.FileExtra != nil }

// ToDict 转换为 dict。
func (fs *FieldSource) ToDict() map[string]any {
	result := map[string]any{}
	if fs.Ref != nil {
		result["ref"] = fs.Ref.ToDict()
	} else {
		result["val"] = fs.Val
	}
	if fs.FileExtra != nil {
		result["file_extra"] = fs.FileExtra.ToDict()
	}
	return result
}

// FieldSourceFromDict 从 dict 构造。
func FieldSourceFromDict(d map[string]any) *FieldSource {
	fs := &FieldSource{}
	if raw, ok := d["ref"].(map[string]any); ok && len(raw) > 0 {
		fs.Ref = ReferenceFromDict(raw)
	}
	if _, ok := d["val"]; ok {
		fs.Val = d["val"]
	}
	if raw, ok := d["file_extra"].(map[string]any); ok && len(raw) > 0 {
		fs.FileExtra = FileExtraFromDict(raw)
	}
	return fs
}

// FieldInfo 字段信息：值放到哪个路径(path)，值从哪来(source)。
type FieldInfo struct {
	Path   []string
	Source FieldSource
	// TypeExplicit 来源输入块是否显式声明类型（见 BlockInput.TypeExplicit）。
	TypeExplicit bool
}

// FieldInfoFromDict 从 dict 构造。
func FieldInfoFromDict(d map[string]any) *FieldInfo {
	fi := &FieldInfo{}
	if raw, ok := d["path"].([]any); ok {
		for _, p := range raw {
			if s, ok := p.(string); ok {
				fi.Path = append(fi.Path, s)
			}
		}
	}
	if raw, ok := d["source"].(map[string]any); ok {
		fi.Source = *FieldSourceFromDict(raw)
	}
	return fi
}

// IsRefGlobalVariable 判断是否引用全局变量（GLOBAL_USER/SYSTEM/APP）。
func (fi *FieldInfo) IsRefGlobalVariable() bool {
	if fi.Source.Ref == nil || fi.Source.Ref.VariableType == nil {
		return false
	}
	switch *fi.Source.Ref.VariableType {
	case GlobalVarUser, GlobalVarSystem, GlobalVarApp:
		return true
	}
	return false
}

// ToDict 转换为 dict。
func (fi *FieldInfo) ToDict() map[string]any {
	return map[string]any{
		"path":   fi.Path,
		"source": fi.Source.ToDict(),
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
