package typesystem

// FileSubType 文件子类型。
type FileSubType string

const (
	FileSubTypeDefault  FileSubType = "default"
	FileSubTypeImage    FileSubType = "image"
	FileSubTypeSVG      FileSubType = "svg"
	FileSubTypeAudio    FileSubType = "audio"
	FileSubTypeVideo    FileSubType = "video"
	FileSubTypeVoice    FileSubType = "voice"
	FileSubTypeDocument FileSubType = "doc"
	FileSubTypePPT      FileSubType = "ppt"
	FileSubTypeExcel    FileSubType = "excel"
	FileSubTypeTXT      FileSubType = "txt"
	FileSubTypeCode     FileSubType = "code"
	FileSubTypeZIP      FileSubType = "zip"
)

// FileSubTypeFromString 从字符串构造；空返回 nil；未知回退 DEFAULT。
func FileSubTypeFromString(s string) *FileSubType {
	if s == "" {
		return nil
	}
	st := FileSubType(s)
	switch st {
	case FileSubTypeDefault, FileSubTypeImage, FileSubTypeSVG, FileSubTypeAudio,
		FileSubTypeVideo, FileSubTypeVoice, FileSubTypeDocument, FileSubTypePPT,
		FileSubTypeExcel, FileSubTypeTXT, FileSubTypeCode, FileSubTypeZIP:
		return &st
	default:
		d := FileSubTypeDefault
		return &d
	}
}
