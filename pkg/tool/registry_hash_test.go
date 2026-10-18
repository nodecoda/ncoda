package tool

// Interface hash unit tests (P3.3 §16.2). The hash is the ABI fingerprint a
// binding may pin; it must be deterministic and order-sensitive across every
// ABI dimension, and stable across non-ABI metadata (description).

import (
	"strings"
	"testing"
)

func baseDef() Definition {
	return Definition{
		Provider:    "tts",
		Operation:   "speech_synthesis",
		Description: "语音合成",
		Parameters: []Parameter{
			{Name: "text", Type: "any", Required: true},
			{Name: "language", Type: "string", Required: false},
			{Name: "voice_id", Type: "string", Required: false},
		},
		Outputs: []OutputField{
			{Name: "data.url", Type: "string"},
			{Name: "status_code", Type: "float"},
			{Name: "texts", Type: "array<string>"},
		},
		SupportsRetry:         true,
		SupportsFailureBranch: true,
	}
}

func hashOf(t *testing.T, d Definition) string {
	t.Helper()
	r, err := NewRegistry(d)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	got, _ := r.Lookup(d.Provider, d.Operation)
	return got.InterfaceHash
}

// TestInterfaceHashStable locks the exact fingerprint so algorithm drift
// (ordering, field set, serialization) fails loudly. Recompute and update
// this constant only when the interface contract intentionally changes.
func TestInterfaceHashStable(t *testing.T) {
	got := hashOf(t, baseDef())
	// Pinned on 2026-08-18. Recompute only when the interface contract
	// intentionally changes (adding effect bits, renaming canonical fields).
	const want = "sha256:912075098faca0052f7efe6a08e3031102c7866d61d5142ded5c4d0e00688616"
	if got != want {
		t.Fatalf("hash = %q, want pinned %q (algorithm drift or contract change)", got, want)
	}
	// Determinism: same definition twice yields the same hash.
	if again := hashOf(t, baseDef()); again != got {
		t.Fatalf("non-deterministic hash: %q vs %q", got, again)
	}
}

// TestInterfaceHashDimensions asserts every ABI dimension moves the hash and
// non-ABI metadata (description) does not.
func TestInterfaceHashDimensions(t *testing.T) {
	base := hashOf(t, baseDef())

	cases := []struct {
		name   string
		mutate func(*Definition)
	}{
		{"provider", func(d *Definition) { d.Provider = "tts_v2" }},
		{"operation", func(d *Definition) { d.Operation = "speech_synth" }},
		{"param order", func(d *Definition) {
			d.Parameters[0], d.Parameters[2] = d.Parameters[2], d.Parameters[0]
		}},
		{"param name", func(d *Definition) { d.Parameters[0].Name = "input" }},
		{"param type", func(d *Definition) { d.Parameters[0].Type = "string" }},
		{"param required", func(d *Definition) { d.Parameters[0].Required = false }},
		{"output path", func(d *Definition) { d.Outputs[0].Name = "data.link" }},
		{"output type", func(d *Definition) { d.Outputs[0].Type = "array<string>" }},
		{"output order", func(d *Definition) {
			d.Outputs[0], d.Outputs[2] = d.Outputs[2], d.Outputs[0]
		}},
		{"retry effect", func(d *Definition) { d.SupportsRetry = false }},
		{"failure_branch effect", func(d *Definition) { d.SupportsFailureBranch = false }},
		{"timeout_ms effect", func(d *Definition) { d.TimeoutMS = 60000 }},
		{"idempotent effect", func(d *Definition) { d.Idempotent = true }},
		// Tier-2 review #11:参数约束字段进入 ABI,声明约束变化必须驱动
		// hash 漂移（否则 pin 绑定会在约束改变后仍静默通过）。
		{"param default", func(d *Definition) { d.Parameters[0].Default = "hello" }},
		{"param enum", func(d *Definition) {
			d.Parameters[1].Enum = []any{"zh", "en"}
		}},
		{"param min", func(d *Definition) { d.Parameters[2].Min = float64Ptr(0) }},
		{"param max", func(d *Definition) { d.Parameters[2].Max = float64Ptr(100) }},
	}
	for _, tc := range cases {
		d := baseDef()
		tc.mutate(&d)
		if got := hashOf(t, d); got == base {
			t.Errorf("%s: hash did not change", tc.name)
		}
	}

	// Non-ABI metadata must not move the hash.
	d := baseDef()
	d.Description = "完全不同的描述"
	if got := hashOf(t, d); got != base {
		t.Errorf("description changed hash: %q vs %q", got, base)
	}
}

// TestInterfaceHashEmptyTables ensures definitions with no parameters or no
// outputs still hash deterministically (open-world leaves are the common
// fallback shape).
func TestInterfaceHashEmptyTables(t *testing.T) {
	d := Definition{Provider: "p", Operation: "op"}
	got := hashOf(t, d)
	if got == "" {
		t.Fatal("empty definition has no hash")
	}
	if again := hashOf(t, Definition{Provider: "p", Operation: "op"}); again != got {
		t.Fatalf("empty definition hash not deterministic: %q vs %q", got, again)
	}
}

// TestValidateBindingsTwoLayer locks the P3.3 §16.2 binding↔registry check:
// collection mismatches and pin violations are hard errors; absent pins are
// reported as unpinned, never errors.
func TestValidateBindingsTwoLayer(t *testing.T) {
	r, err := NewRegistry(Definition{
		Provider:  "tts",
		Operation: "speech_synthesis",
		Parameters: []Parameter{
			{Name: "text", Type: "any", Required: true},
			{Name: "voice_id", Type: "string", Required: false},
		},
		Outputs: []OutputField{
			{Name: "data.url", Type: "string"},
			{Name: "status_code", Type: "float"},
		},
		SupportsRetry: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	def, _ := r.Lookup("tts", "speech_synthesis")
	hash := def.InterfaceHash
	if hash == "" {
		t.Fatal("registry definition has no hash")
	}

	// 1. Consistent, unpinned → no error, reported as unpinned.
	unpinned, err := r.ValidateBindings([]BindingLink{{
		Provider:  "tts",
		Operation: "speech_synthesis",
		Arguments: map[string]string{"text": "text"},
		Outputs:   map[string]string{"data.url": "data.url"},
	}})
	if err != nil {
		t.Fatalf("consistent unpinned binding rejected: %v", err)
	}
	if len(unpinned) != 1 || unpinned[0] != "tts/speech_synthesis" {
		t.Fatalf("unpinned = %v, want [tts/speech_synthesis]", unpinned)
	}

	// 2. Consistent, correctly pinned → no error, not unpinned.
	unpinned, err = r.ValidateBindings([]BindingLink{{
		Provider: "tts", Operation: "speech_synthesis",
		Arguments:     map[string]string{"text": "text"},
		Outputs:       map[string]string{"status_code": "code"},
		InterfaceHash: hash,
	}})
	if err != nil {
		t.Fatalf("pinned binding rejected: %v", err)
	}
	if len(unpinned) != 0 {
		t.Fatalf("pinned binding reported unpinned: %v", unpinned)
	}

	// 3. Stale pin → hard error.
	_, err = r.ValidateBindings([]BindingLink{{
		Provider: "tts", Operation: "speech_synthesis",
		Arguments:     map[string]string{"text": "text"},
		InterfaceHash: "sha256:deadbeef",
	}})
	if err == nil || !strings.Contains(err.Error(), "interface mismatch") {
		t.Fatalf("stale pin error = %v, want interface mismatch", err)
	}

	// 4. Unknown capability → skipped (P3.5: the registry may be a subset
	// of the binding manifest when imports load only declared packages; a
	// binding for an out-of-scope capability is not exercised).
	unpinned, err = r.ValidateBindings([]BindingLink{{Provider: "nope", Operation: "nope"}})
	if err != nil {
		t.Fatalf("out-of-scope binding should be skipped: %v", err)
	}
	if len(unpinned) != 0 {
		t.Fatalf("out-of-scope binding reported unpinned: %v", unpinned)
	}

	// 5. Collection mismatch (argument not in registry) → hard error with diff.
	_, err = r.ValidateBindings([]BindingLink{{
		Provider: "tts", Operation: "speech_synthesis",
		Arguments: map[string]string{"typo_param": "x"},
	}})
	if err == nil || !strings.Contains(err.Error(), `declares argument "typo_param" not in registry`) {
		t.Fatalf("argument mismatch error = %v", err)
	}

	// 6. Output path not in registry → hard error with diff.
	_, err = r.ValidateBindings([]BindingLink{{
		Provider: "tts", Operation: "speech_synthesis",
		Outputs: map[string]string{"data.missing": "x"},
	}})
	if err == nil || !strings.Contains(err.Error(), `declares output "data.missing" not in registry`) {
		t.Fatalf("output mismatch error = %v", err)
	}

	// 7. Nil registry is a no-op (open-world).
	if _, err := (*Registry)(nil).ValidateBindings([]BindingLink{{Provider: "x", Operation: "y"}}); err != nil {
		t.Fatalf("nil registry should be a no-op: %v", err)
	}
}

// float64Ptr 返回指向 v 的指针（测试辅助,用于 Min/Max 维度用例）。
func float64Ptr(v float64) *float64 { return &v }

// Tier-2 #11 回归:Default/Enum 的类型敏感性。
// computeInterfaceHash 用 %v 渲染 Default,导致 int(1) 与 float64(1) 碰撞
// （%v 都渲染成 "1"）——JSON Schema 语义里 1 与 1.0 是不同的默认值,ABI
// 指纹必须能区分。int(1) 与 float64(1) 的 hash 必须不同。
func TestInterfaceHashDefaultTypeSensitive(t *testing.T) {
	intDefault := hashOf(t, Definition{Provider: "p", Operation: "op",
		Parameters: []Parameter{{Name: "n", Type: "int", Default: 1}}})
	floatDefault := hashOf(t, Definition{Provider: "p", Operation: "op",
		Parameters: []Parameter{{Name: "n", Type: "int", Default: 1.0}}})
	if intDefault == floatDefault {
		t.Errorf("hash collision: Default int(1) and float64(1) hash identically %q — %%v 渲染碰撞,约束序列化必须类型敏感", intDefault)
	}
}

// Tier-2 #11 端到端:约束字段进 ABI 后,pin 绑定能感知约束变化。
// 同一 identity,registry 声明带约束（Default "zh"）而 pin 指向"无约束"
// 声明的 hash → 必须 pin 失配硬错（此前约束不进 hash,pin 对约束变化盲）。
func TestValidateBindingsConstraintPinSensitive(t *testing.T) {
	// registry:带约束的声明
	r, err := NewRegistry(Definition{
		Provider: "tts", Operation: "speak",
		Parameters: []Parameter{
			{Name: "language", Type: "string", Required: true, Default: "zh", Enum: []any{"zh", "en"}},
		},
		Outputs: []OutputField{{Name: "audio", Type: "string"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	def, _ := r.Lookup("tts", "speak")

	// 1. 正确 pin（带约束声明的 hash）→ 无错误。
	if _, err := r.ValidateBindings([]BindingLink{{
		Provider: "tts", Operation: "speak",
		Arguments:     map[string]string{"language": "language"},
		Outputs:       map[string]string{"audio": "audio"},
		InterfaceHash: def.InterfaceHash,
	}}); err != nil {
		t.Fatalf("correctly pinned binding rejected: %v", err)
	}

	// 2. pin 指向无约束声明的 hash → 失配硬错（约束变化驱动 pin 拒绝）。
	plain, err := NewRegistry(Definition{
		Provider: "tts", Operation: "speak",
		Parameters: []Parameter{{Name: "language", Type: "string", Required: true}},
		Outputs:    []OutputField{{Name: "audio", Type: "string"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plainDef, _ := plain.Lookup("tts", "speak")
	if plainDef.InterfaceHash == def.InterfaceHash {
		t.Fatal("constraint fields do not drive hash — pin cannot detect constraint change")
	}
	if _, err := r.ValidateBindings([]BindingLink{{
		Provider: "tts", Operation: "speak",
		Arguments:     map[string]string{"language": "language"},
		Outputs:       map[string]string{"audio": "audio"},
		InterfaceHash: plainDef.InterfaceHash, // 无约束声明的 hash,与带约束声明失配
	}}); err == nil {
		t.Error("expected pin violation: constraint-bearing declaration vs constraint-free pin")
	}
}
