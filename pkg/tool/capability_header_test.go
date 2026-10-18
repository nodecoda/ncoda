package tool

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
)

const ttsHeader = `@language nodecoda/1

type TTSData = {
  float duration;
  float start_time;
  string url;
};

type TTSResult = {
  TTSData data;
  float status_code;
  array<string> texts;
  map<string,any> extra;
};

function speech_synthesis(any text, string language, any voice_id, any speed_ratio, string speaker_id = "default") -> TTSResult {}
`

func writeHeader(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func TestLoadCapabilityDir(t *testing.T) {
	dir := t.TempDir()
	writeHeader(t, dir, "tts.ncoda", ttsHeader)
	writeHeader(t, dir, "video_parse.ncoda", `type VPResult = {
  string title;
  string cover_url;
};

function parse(any url) -> VPResult {}
`)
	writeHeader(t, dir, "README.txt", "not a header")

	registry, err := LoadCapabilityDir(dir)
	if err != nil {
		t.Fatalf("LoadCapabilityDir: %v", err)
	}

	tts, ok := registry.Lookup("tts", "speech_synthesis")
	if !ok {
		t.Fatal("missing tts/speech_synthesis")
	}
	if tts.Description != "" {
		t.Errorf("Description = %q, want empty", tts.Description)
	}
	wantParams := []Parameter{
		{Name: "text", Type: "any", Required: true},
		{Name: "language", Type: "string", Required: true},
		{Name: "voice_id", Type: "any", Required: true},
		{Name: "speed_ratio", Type: "any", Required: true},
		{Name: "speaker_id", Type: "string"}, // has default -> not required
	}
	if len(tts.Parameters) != len(wantParams) {
		t.Fatalf("Parameters = %+v, want %+v", tts.Parameters, wantParams)
	}
	for i, want := range wantParams {
		// Parameter 含 []any（Enum）,不可直接比较（Tier-2 #11 加字段后）。
		if !reflect.DeepEqual(tts.Parameters[i], want) {
			t.Errorf("Parameters[%d] = %+v, want %+v", i, tts.Parameters[i], want)
		}
	}
	wantOutputs := []OutputField{
		{Name: "data.duration", Type: "float"},
		{Name: "data.start_time", Type: "float"},
		{Name: "data.url", Type: "string"},
		{Name: "status_code", Type: "float"},
		{Name: "texts", Type: "array<string>"},
		{Name: "extra", Type: "map<string,any>"},
	}
	if len(tts.Outputs) != len(wantOutputs) {
		t.Fatalf("Outputs = %+v, want %+v", tts.Outputs, wantOutputs)
	}
	for i, want := range wantOutputs {
		if tts.Outputs[i] != want {
			t.Errorf("Outputs[%d] = %+v, want %+v", i, tts.Outputs[i], want)
		}
	}

	parse, ok := registry.Lookup("video_parse", "parse")
	if !ok || len(parse.Outputs) != 2 {
		t.Fatalf("video_parse/parse = %+v, want 2 outputs", parse)
	}
}

func TestLoadCapabilityDirRejectsInvalidHeaders(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "non-empty body",
			body: `function f(any x) -> R {} 
type R = { string s; };
function g(any y) { var n = 1; }
`,
			want: "empty body",
		},
		{
			name: "main function",
			body: `function main() { }`,
			want: "must not declare a main function",
		},
		{
			name: "const not allowed",
			body: `const X = 1;
type R = { string s; };
function f(any x) -> R {}`,
			want: "allows only `type` and empty-body `function`",
		},
		{
			name: "recursive type",
			body: `type A = { B b; };
type B = { A a; };
function f(any x) -> A {}`,
			want: "recursive type",
		},
		{
			name: "undefined type",
			body: `function f(any x) -> Missing {}`,
			want: `undefined type "Missing"`,
		},
		{
			name: "optional param type",
			body: `type R = { string s; };
function f(string? x) -> R {}`,
			want: "optional type is not a registry type",
		},
		{
			name: "named param type",
			body: `type P = { string s; };
type R = { string s; };
function f(P x) -> R {}`,
			want: "only valid as a struct output",
		},
		{
			name: "primitive return type",
			body: `function f(any x) -> string {}`,
			want: "must be a named struct type",
		},
		{
			name: "parse error",
			body: `function f(any x -> R {}`,
			want: "parse error",
		},
		{
			name: "no apis",
			body: `type R = { string s; };`,
			want: "no api declarations found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeHeader(t, dir, "tts.ncoda", tt.body)
			_, err := LoadCapabilityDir(dir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadCapabilityDir error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadCapabilityDirEmptyAndDuplicate(t *testing.T) {
	empty := t.TempDir()
	if _, err := LoadCapabilityDir(empty); err == nil || !errors.Is(err, ErrNoCapabilityHeaders) {
		t.Fatalf("LoadCapabilityDir(empty) error = %v, want ErrNoCapabilityHeaders", err)
	}
	dup := t.TempDir()
	writeHeader(t, dup, "tts.ncoda", `type R = { string s; };
function f(any x) -> R {}
function f(any y) -> R {}
`)
	if _, err := LoadCapabilityDir(dup); err == nil || !strings.Contains(err.Error(), `duplicate definition "tts/f"`) {
		t.Fatalf("LoadCapabilityDir duplicate error = %v, want duplicate definition", err)
	}
}

func TestLoadCapabilityDirCapabilityAnnotations(t *testing.T) {
	dir := t.TempDir()
	writeHeader(t, dir, "web_search.ncoda", `type R = { string title; };

@capability retry=true, failure_branch=true
function search(string query) -> R {}

@capability retry=true
function search_async(string query) -> R {}
`)
	registry, err := LoadCapabilityDir(dir)
	if err != nil {
		t.Fatalf("LoadCapabilityDir: %v", err)
	}
	search, _ := registry.Lookup("web_search", "search")
	if !search.SupportsRetry || !search.SupportsFailureBranch {
		t.Errorf("search policies = retry:%v failure_branch:%v, want true/true",
			search.SupportsRetry, search.SupportsFailureBranch)
	}
	async, _ := registry.Lookup("web_search", "search_async")
	if !async.SupportsRetry || async.SupportsFailureBranch {
		t.Errorf("search_async policies = retry:%v failure_branch:%v, want true/false",
			async.SupportsRetry, async.SupportsFailureBranch)
	}
	plain, _ := registry.Lookup("web_search", "search_async")
	_ = plain
}

func TestLoadCapabilityDirRejectsBadAnnotations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown attribute",
			body: `type R = { string s; };
@capability timeout=true
function f(any x) -> R {}`,
			want: `unknown attribute "timeout"`,
		},
		{
			name: "bad value",
			body: `type R = { string s; };
@capability retry=yes
function f(any x) -> R {}`,
			want: "expected true or false",
		},
		{
			name: "missing equals",
			body: `type R = { string s; };
@capability retry
function f(any x) -> R {}`,
			want: "expected key=true",
		},
		{
			name: "not followed by function",
			body: `@capability retry=true
type R = { string s; };`,
			want: "not followed by a function declaration",
		},
		{
			name: "duplicate annotation",
			body: `type R = { string s; };
@capability retry=true
@capability retry=false
function f(any x) -> R {}`,
			want: "duplicate capability annotation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeHeader(t, dir, "tts.ncoda", tt.body)
			_, err := LoadCapabilityDir(dir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadCapabilityDir error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadCapabilityDirArrayOfRecordElementFields(t *testing.T) {
	// E5-b: array<T> where T is a named record emits the array leaf plus the
	// element record's fields as descendant dotted paths.
	dir := t.TempDir()
	writeHeader(t, dir, "search.ncoda", `type SearchDoc = {
  string title;
  string url;
};

type SearchResult = {
  SearchDoc data;
  array<SearchDoc> doc_results;
  int status_code;
};

function search(string query) -> SearchResult {}
`)
	registry, err := LoadCapabilityDir(dir)
	if err != nil {
		t.Fatalf("LoadCapabilityDir: %v", err)
	}
	search, ok := registry.Lookup("search", "search")
	if !ok {
		t.Fatal("missing search/search")
	}
	wantOutputs := []OutputField{
		{Name: "data.title", Type: "string"},
		{Name: "data.url", Type: "string"},
		{Name: "doc_results", Type: "array<record:SearchDoc>"},
		{Name: "doc_results.title", Type: "string"},
		{Name: "doc_results.url", Type: "string"},
		{Name: "status_code", Type: "int"},
	}
	if len(search.Outputs) != len(wantOutputs) {
		t.Fatalf("Outputs = %+v, want %+v", search.Outputs, wantOutputs)
	}
	for i, want := range wantOutputs {
		if search.Outputs[i] != want {
			t.Errorf("Outputs[%d] = %+v, want %+v", i, search.Outputs[i], want)
		}
	}
}

func TestLoadCapabilityDirRejectsRecursiveArrayElement(t *testing.T) {
	dir := t.TempDir()
	writeHeader(t, dir, "a.ncoda", `type A = { array<A> items; };
function f(any x) -> A {}`)
	if _, err := LoadCapabilityDir(dir); err == nil || !strings.Contains(err.Error(), "recursive type") {
		t.Fatalf("LoadCapabilityDir error = %v, want recursive type", err)
	}
}

func TestLoadCapabilityDirEffectExtendedAttributes(t *testing.T) {
	// P3.3 §16.3: timeout_ms (int ceiling) and idempotent parse from the
	// @capability annotation and land on the Definition.
	dir := t.TempDir()
	writeHeader(t, dir, "capcut_data.ncoda", `type R = { string infos; };

@capability retry=true, failure_branch=true, timeout_ms=180000, idempotent=true
function audio_infos(any mp3_urls, any timelines) -> R {}
`)
	registry, err := LoadCapabilityDir(dir)
	if err != nil {
		t.Fatalf("LoadCapabilityDir: %v", err)
	}
	d, _ := registry.Lookup("capcut_data", "audio_infos")
	if !d.SupportsRetry || !d.SupportsFailureBranch {
		t.Errorf("effect bits = retry:%v fb:%v, want true/true", d.SupportsRetry, d.SupportsFailureBranch)
	}
	if d.TimeoutMS != 180000 {
		t.Errorf("TimeoutMS = %d, want 180000", d.TimeoutMS)
	}
	if !d.Idempotent {
		t.Error("Idempotent = false, want true")
	}
	// The extended effect bits participate in the interface hash.
	if d.InterfaceHash == "" {
		t.Error("InterfaceHash empty")
	}
}

func TestLoadCapabilityDirRejectsBadTimeoutMS(t *testing.T) {
	dir := t.TempDir()
	writeHeader(t, dir, "tts.ncoda", `type R = { string s; };
@capability timeout_ms=-1
function f(any x) -> R {}`)
	_, err := LoadCapabilityDir(dir)
	if err == nil || !strings.Contains(err.Error(), "non-negative integer") {
		t.Fatalf("error = %v, want non-negative integer rejection", err)
	}
}

// 调用点 timeout 高于头文件标称值必须**放行**（2026-09-29，R-151）。
//
// 一手依据：coze-biz 语料 322 份 yaml 里 15 个 op 的节点 timeoutMs=600000
// 而头文件标称 180000（平台接受）⇒ 标称是「该 op 的默认超时（数据）」，
// 不是上限。旧实现按上限硬拒 ⇒ raising 产出的 ncoda 过不了 compile
// （实测 34/322 源，10.5% 语料结构上无法往返）。
func TestValidateCallTimeoutAboveNominalIsAccepted(t *testing.T) {
	r, err := NewRegistry(Definition{
		Provider:   "capcut_data",
		Operation:  "audio_infos",
		Parameters: []Parameter{{Name: "mp3_urls", Type: "any", Required: true}},
		TimeoutMS:  180000,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := &ir.ToolCall{
		ID:         "100001",
		ProviderID: "capcut_data",
		Operation:  "audio_infos",
		Params:     []ir.ToolParam{{Key: "mp3_urls", Value: ir.Source{BlockID: "100000", Name: "x"}}},
		Exception:  &ir.ExceptionConfig{TimeoutMS: 300000},
	}
	if err := r.validateCall(call); err != nil {
		t.Fatalf("高于标称的调用点 timeout 必须放行（标称不是上限）: %v", err)
	}
	call.Exception.TimeoutMS = 60000
	if err := r.validateCall(call); err != nil {
		t.Fatalf("低于标称的调用点 timeout 被拒: %v", err)
	}
	call.Exception = nil
	if err := r.validateCall(call); err != nil {
		t.Fatalf("no-exception call rejected: %v", err)
	}
}

// TestLoadCapabilityPackageCanonicalRoot：规范工具根布局
// （<root>/capabilities-ncoda/<provider>.ncoda）必须可被 import 解析——
// 外部工具（ncoda-foundry export）按该布局产出，编译器直接导入。
func TestLoadCapabilityPackageCanonicalRoot(t *testing.T) {
	root := t.TempDir()
	caps := filepath.Join(root, "capabilities-ncoda")
	if err := os.MkdirAll(caps, 0o755); err != nil {
		t.Fatal(err)
	}
	header := `// datetime.ncoda — capability header
// desc: 日期时间
@language nodecoda/1

type DatetimeDatetimeResult = {
  float date;
};

@capability failure_branch=true, timeout_ms=180000
function datetime(string datetime = "") -> DatetimeDatetimeResult {}
`
	if err := os.WriteFile(filepath.Join(caps, "datetime.ncoda"), []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadCapabilityPackages([]string{root}, []string{"datetime"})
	if err != nil {
		t.Fatalf("canonical root load: %v", err)
	}
	def, ok := reg.Lookup("datetime", "datetime")
	if !ok {
		t.Fatal("datetime not registered")
	}
	if !def.SupportsFailureBranch || def.TimeoutMS != 180000 {
		t.Errorf("policies lost: %+v", def)
	}
}

// TestLoadCapabilityPackageFlatFallback：旧平铺布局（<dir>/<provider>.ncoda）
// 必须继续工作（向后兼容）。
func TestLoadCapabilityPackageFlatFallback(t *testing.T) {
	dir := t.TempDir()
	header := `@language nodecoda/1

type XResult = {
  string msg;
};

function probe(string input) -> XResult {}
`
	if err := os.WriteFile(filepath.Join(dir, "probe.ncoda"), []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCapabilityPackages([]string{dir}, []string{"probe"}); err != nil {
		t.Fatalf("flat fallback: %v", err)
	}
}

// TestLoadCapabilityPackagesRejectsTraversalProvider 锁住 M1 的消费侧防线：
// 路径形态的 provider 名（../ 穿越）在 SplitImport 阶段就被拒绝，绝不进入
// capabilities-ncoda/ 的 filepath.Join。
func TestLoadCapabilityPackagesRejectsTraversalProvider(t *testing.T) {
	root := t.TempDir()
	caps := filepath.Join(root, "capabilities-ncoda")
	if err := os.MkdirAll(caps, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caps, "victimfile.ncoda"), []byte("@language nodecoda/1\nfunction f() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCapabilityPackages([]string{root}, []string{"coze-biz../victimfile"})
	if err == nil {
		t.Fatal("LoadCapabilityPackages accepted traversal provider, want error")
	}
	if !strings.Contains(err.Error(), "invalid import provider") {
		t.Fatalf("error = %v, want invalid-provider error", err)
	}
}

// TestLoadCapabilitySearchPaths 验证 C 头文件搜索模型：路径按优先级
// first-wins shadowing、缺失目录静默跳过、非能力目录静默跳过、损坏目录硬报错、
// 整条链为空时返回 (nil, nil)。
func TestLoadCapabilitySearchPaths(t *testing.T) {
	// 高优先级目录：tts 用 v2 参数集（覆盖低优先级）
	high := t.TempDir()
	writeHeader(t, high, "tts.ncoda", `type TTSData = { float duration; string url; };
type TTSResult = { TTSData data; float status_code; };
function speech_synthesis(any text, string language, any voice_id) -> TTSResult {}
`)
	writeHeader(t, high, "only_high.ncoda", `type OnlyResult = { string s; };
function high_only(any x) -> OnlyResult {}
`)

	// 低优先级目录：tts 旧参数集（应被覆盖），video_parse 补集
	low := t.TempDir()
	writeHeader(t, low, "tts.ncoda", `type TTSData = { float duration; string url; };
type TTSResult = { TTSData data; float status_code; };
function speech_synthesis(any text, string language, any voice_id, any speed_ratio) -> TTSResult {}
`)
	writeHeader(t, low, "video_parse.ncoda", `type VPResult = { string title; };
function parse(any url) -> VPResult {}
`)

	// 缺失目录 + 空目录（非能力目录）：静默跳过
	missing := filepath.Join(t.TempDir(), "not-exist")
	empty := t.TempDir()

	reg, err := LoadCapabilitySearchPaths([]string{missing, empty, high, low})
	if err != nil {
		t.Fatalf("LoadCapabilitySearchPaths: %v", err)
	}
	if reg == nil {
		t.Fatal("registry nil, want non-nil")
	}

	// first-wins：tts 来自 high（3 参数），不是 low（4 参数）
	tts, ok := reg.Lookup("tts", "speech_synthesis")
	if !ok {
		t.Fatal("missing tts/speech_synthesis")
	}
	if len(tts.Parameters) != 3 {
		t.Errorf("tts.Parameters = %d, want 3 (first-wins from high)", len(tts.Parameters))
	}
	// 补集来自 low
	if _, ok := reg.Lookup("video_parse", "parse"); !ok {
		t.Error("missing video_parse/parse from low")
	}
	if _, ok := reg.Lookup("only_high", "high_only"); !ok {
		t.Error("missing only_high/high_only from high")
	}
}

func TestLoadCapabilitySearchPathsBrokenDirErrors(t *testing.T) {
	good := t.TempDir()
	writeHeader(t, good, "tts.ncoda", ttsHeader)
	broken := t.TempDir()
	writeHeader(t, broken, "bad.ncoda", "function f(any x { broken syntax")

	if _, err := LoadCapabilitySearchPaths([]string{broken, good}); err == nil {
		t.Fatal("broken capability dir must error (fail loud), got nil")
	}
}

func TestLoadCapabilitySearchPathsEmptyChain(t *testing.T) {
	reg, err := LoadCapabilitySearchPaths([]string{t.TempDir(), filepath.Join(t.TempDir(), "nope")})
	if err != nil {
		t.Fatalf("LoadCapabilitySearchPaths: %v", err)
	}
	if reg != nil {
		t.Fatalf("registry = %+v, want nil (no capability sources)", reg)
	}
}

// ── R-29（2026-09-24 用户裁定「收紧」）：输出表是头文件的义务 ──────────
//
// 加载器必须把「**没写**输出表」与「**写了**空输出表（`-> void`）」分开：
// 前者是声明方的错误（调用点 fail loud，`CAPABILITY_OUTPUTS_UNDECLARED`），
// 后者是合法的「本次调用没有结果值」。区分载体 = Definition.OutputsUndeclared
// （零值 = 已声明）。
func TestCapabilityHeaderVoidReturnDeclaresEmptyOutputTable(t *testing.T) {
	dir := t.TempDir()
	writeHeader(t, dir, "timer.ncoda", `@language nodecoda/1

function wait(int delay_ms) -> void {}
`)
	registry, err := LoadCapabilityDir(dir)
	if err != nil {
		t.Fatalf("LoadCapabilityDir: %v", err)
	}
	def, ok := registry.Lookup("timer", "wait")
	if !ok {
		t.Fatal("missing timer/wait: `-> void` must be a legal capability declaration")
	}
	if def.OutputsUndeclared {
		t.Error("`-> void` = 显式声明空输出表；不得判为「未声明」")
	}
	if len(def.Outputs) != 0 {
		t.Errorf("`-> void` outputs = %+v, want empty", def.Outputs)
	}
}

func TestCapabilityHeaderMissingReturnTypeIsUndeclared(t *testing.T) {
	dir := t.TempDir()
	writeHeader(t, dir, "silent.ncoda", `@language nodecoda/1

function silent(any q) {}
`)
	registry, err := LoadCapabilityDir(dir)
	if err != nil {
		t.Fatalf("LoadCapabilityDir: %v", err)
	}
	def, ok := registry.Lookup("silent", "silent")
	if !ok {
		t.Fatal("missing silent/silent")
	}
	if !def.OutputsUndeclared {
		t.Error("头文件没写 `-> T` ⇒ OutputsUndeclared 必须为 true（调用点据此拒收）")
	}
}
