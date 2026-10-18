package astnodes

import "testing"

func TestConfigKey(t *testing.T) {
	cases := map[string]string{
		"ModelID": "model_id", "URLTemplate": "url_template", "TimeoutMS": "timeout_ms",
		"EnableNL2SQL": "enable_nl2sql", "DatabaseInfoIDs": "database_info_ids",
		"SQLTemplate": "sql_template", "ImageOCR": "image_ocr", "ConcatChar": "concat_char",
		"DefaultValues": "default_values", "TopK": "top_k", "BackupModelID": "backup_model_id",
	}
	for in, want := range cases {
		if got := ConfigKey(in); got != want {
			t.Errorf("ConfigKey(%q) = %q, want %q", in, got, want)
		}
	}
}
