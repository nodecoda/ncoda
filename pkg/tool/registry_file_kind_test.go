package tool

// file 族**载体**判据（F-AXES-7 的锚，2026-09-21）。
//
// 背景（一手实证）：能力头文件的输出字段写 `file<image> data;`，但
// `typeRefToTypeString` 曾把它压成裸 `"file"` ⇒ **族在注册表这一层就丢了**
// （把头文件改成 `file<image>` 后产物逐字节不变）。注册表串是 carrier，不是标签。
//
// 判据三段：
//  1. `FileKindTokens` 从 `file<…>` 取约束 token；裸 `file` / 非 file 串 ⇒ nil；
//  2. `IsFileType` = 裸 `file` ∪ `file<…>`（旧消费者据此保住既有 file 行为，
//     不落 string 默认）；
//  3. **端到端载体**：头文件 `file<image> data;` → 注册表 `Outputs[].Type`
//     逐字是 `file<image>`（撤 `fileRegistryTypeString` ⇒ 红）；
//     关键字族 `file<$code>` 同路（撤「发射折拼写」⇒ 红）。

import "testing"

func TestFileKindTokensParseClosedVocabulary(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"file<image>", []string{"image"}},
		{"file<document>", []string{"document"}},
		{"file<document; .pdf; local_file>", []string{"document", ".pdf", "local_file"}},
		{"file<image,audio>", []string{"image", "audio"}},
		// `code` 是 ncoda 关键字 ⇒ 载体里的拼写带 `$`；回读只给名字（`$` 是纯词法转义）
		{"file<$code>", []string{"code"}},
		{"file<$code, image>", []string{"code", "image"}},
		{"file", nil},
		{"file<>", nil},
		{"string", nil},
		{"array<file>", nil},
	}
	for _, tc := range cases {
		got := FileKindTokens(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("FileKindTokens(%q) = %v，期望 %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("FileKindTokens(%q)[%d] = %q，期望 %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestIsFileTypeAcceptsConstrainedForm(t *testing.T) {
	for _, s := range []string{"file", "file<image>", "file<document; .pdf>"} {
		if !IsFileType(s) {
			t.Errorf("IsFileType(%q) = false，期望 true（旧消费者靠它保住 file 行为）", s)
		}
	}
	for _, s := range []string{"string", "any", "array<file>"} {
		if IsFileType(s) {
			t.Errorf("IsFileType(%q) = true，期望 false", s)
		}
	}
}

// TestCapabilityHeaderCarriesFileFamilyIntoRegistry 是载体判据：
// 头文件里的 file 族必须逐字进注册表（不是被压成 `file`）。
func TestCapabilityHeaderCarriesFileFamilyIntoRegistry(t *testing.T) {
	dir := t.TempDir()
	writeHeader(t, dir, "matting.ncoda", `type MattingCutoutResult = {
  file<image> data;

  string msg;
};

@capability retry=true
function cutout(file url = 0) -> MattingCutoutResult {}
`)
	writeHeader(t, dir, "plain.ncoda", `type PlainResult = {
  file data;

  string msg;
};

@capability retry=true
function fetch(file url = 0) -> PlainResult {}
`)

	writeHeader(t, dir, "runcode.ncoda", `type RunCodeResult = {
  file<$code> data;

  string msg;
};

@capability retry=true
function run(file url = 0) -> RunCodeResult {}
`)
	reg, err := LoadCapabilityDir(dir)
	if err != nil {
		t.Fatalf("LoadCapabilityDir: %v", err)
	}
	def, ok := reg.Lookup("matting", "cutout")
	if !ok {
		t.Fatal("missing matting/cutout")
	}
	var got string
	for _, out := range def.Outputs {
		if out.Name == "data" {
			got = out.Type
		}
	}
	if got != "file<image>" {
		t.Errorf("Outputs[data].Type = %q，期望 %q —— 族在注册表（carrier）这一层丢了",
			got, "file<image>")
	}

	code, ok := reg.Lookup("runcode", "run")
	if !ok {
		t.Fatal("missing runcode/run")
	}
	var gotCode string
	for _, out := range code.Outputs {
		if out.Name == "data" {
			gotCode = out.Type
		}
	}
	// 关键字族名必须**折回源码拼写**发射：注册表串会被写回头文件/类型文本，
	// 发裸 `file<code>` 等于产出**自己解析不了**的 ncoda。
	if gotCode != "file<$code>" {
		t.Errorf("Outputs[data].Type = %q，期望 %q —— 关键字族名必须折回 `$` 拼写",
			gotCode, "file<$code>")
	}

	plain, ok := reg.Lookup("plain", "fetch")
	if !ok {
		t.Fatal("missing plain/fetch")
	}
	for _, out := range plain.Outputs {
		if out.Name == "data" && out.Type != "file" {
			t.Errorf("裸 file 声明 = %q，期望 %q（不得发明族）", out.Type, "file")
		}
	}
}
