package plugin

import (
	"context"
	"strings"
	"testing"
)

func sampleHTTPPlugin() *Plugin {
	return &Plugin{
		ID:      "demo.google_search",
		Version: "1.0.0",
		Type:    PluginHTTP,
		Tools: []Tool{
			{Name: "search", Desc: "web search", Method: "GET", Path: "/search",
				Params: []ParamSchema{
					{Name: "query", Type: "string", Required: true, In: InQuery},
					{Name: "max_results", Type: "int", Default: 5, In: InQuery},
				}},
			{Name: "health", Method: "GET", Path: "/health"},
		},
	}
}

func TestRegisterAndLookup(t *testing.T) {
	m := New()
	if err := m.Register(sampleHTTPPlugin()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := m.Names(); !strings.EqualFold(strings.Join(got, ","), "demo.google_search/health,demo.google_search/search") {
		t.Fatalf("Names = %v", got)
	}
	if !m.Has("demo.google_search/search") {
		t.Fatal("Has(search) = false")
	}
	entry, ok := m.Lookup("demo.google_search/search")
	if !ok || entry.Tool.Name != "search" {
		t.Fatalf("Lookup = %v, %v", entry, ok)
	}
	// 后缀匹配
	if e, ok := m.Lookup("search"); !ok || e.Tool.Name != "search" {
		t.Fatalf("Lookup(search) = %v, %v", e, ok)
	}
}

func TestCrossPluginSameToolNameIsNamespaced(t *testing.T) {
	m := New()
	if err := m.Register(sampleHTTPPlugin()); err != nil {
		t.Fatalf("register: %v", err)
	}
	other := &Plugin{
		ID: "other.provider", Type: PluginCallable,
		Tools: []Tool{{Name: "search", Call: func(ctx context.Context, args map[string]any) (any, error) { return nil, nil }}},
	}
	// 命名空间隔离：跨插件同名工具允许注册（用完整逻辑名区分）。
	if err := m.Register(other); err != nil {
		t.Fatalf("cross-plugin same tool name should be namespaced: %v", err)
	}
	if _, ok := m.Lookup("other.provider/search"); !ok {
		t.Fatal("fully-qualified lookup failed")
	}
	// 退化名歧义 → Lookup 失败；Call 报歧义错误。
	if _, ok := m.Lookup("search"); ok {
		t.Fatal("degenerate name lookup should be ambiguous")
	}
	_, err := m.Call(context.Background(), "search", nil)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("want ambiguous error, got %v", err)
	}
	// 完整名调用仍可用。
	if _, err := m.Call(context.Background(), "other.provider/search", nil); err != nil {
		t.Fatalf("fully-qualified call failed: %v", err)
	}
}

func TestRegisterSameIDVersionConflict(t *testing.T) {
	m := New()
	if err := m.Register(sampleHTTPPlugin()); err != nil {
		t.Fatalf("register: %v", err)
	}
	up := sampleHTTPPlugin()
	up.Version = "2.0.0"
	if err := m.Register(up); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("want version conflict, got %v", err)
	}
}

func TestRegisterSameVersionIdempotentReplace(t *testing.T) {
	m := New()
	if err := m.Register(sampleHTTPPlugin()); err != nil {
		t.Fatalf("register: %v", err)
	}
	up := sampleHTTPPlugin() // same version → 幂等覆盖
	if err := m.Register(up); err != nil {
		t.Fatalf("re-register same version: %v", err)
	}
	if len(m.Names()) != 2 {
		t.Fatalf("Names after re-register = %v", m.Names())
	}
}

func TestUnregisterCascades(t *testing.T) {
	m := New()
	if err := m.Register(sampleHTTPPlugin()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := m.Unregister("demo.google_search"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if len(m.Names()) != 0 {
		t.Fatalf("Names after unregister = %v", m.Names())
	}
	if _, ok := m.Plugin("demo.google_search"); ok {
		t.Fatal("plugin still present after unregister")
	}
	if err := m.Unregister("demo.google_search"); err == nil {
		t.Fatal("unregister twice should fail")
	}
}

func TestRegisterAllRollback(t *testing.T) {
	m := New()
	a := sampleHTTPPlugin()
	bad := &Plugin{ID: "bad", Type: PluginHTTP} // 无 tools → Validate 失败
	if err := m.RegisterAll(a, bad); err == nil {
		t.Fatal("want error")
	}
	if _, ok := m.Plugin("demo.google_search"); ok {
		t.Fatal("RegisterAll should not leave partial state")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		p    *Plugin
		want string
	}{
		{"no id", &Plugin{Type: PluginHTTP, Tools: []Tool{{Name: "a", Method: "GET", Path: "/a"}}}, "id is required"},
		{"traversal id", &Plugin{ID: "../config", Type: PluginHTTP, Tools: []Tool{{Name: "a", Method: "GET", Path: "/a"}}}, "invalid id"},
		{"path id", &Plugin{ID: "a/b", Type: PluginHTTP, Tools: []Tool{{Name: "a", Method: "GET", Path: "/a"}}}, "invalid id"},
		{"no tools", &Plugin{ID: "x", Type: PluginHTTP}, "no tools"},
		{"http no path", &Plugin{ID: "x", Type: PluginHTTP, Tools: []Tool{{Name: "a", Method: "GET"}}}, "path required"},
		{"callable nil", &Plugin{ID: "x", Type: PluginCallable, Tools: []Tool{{Name: "a"}}}, "Call func"},
		{"dup tool", &Plugin{ID: "x", Type: PluginHTTP, Tools: []Tool{{Name: "a", Method: "GET", Path: "/a"}, {Name: "a", Method: "POST", Path: "/a"}}}, "duplicate tool"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.p.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate() = %v, want contains %q", err, c.want)
			}
		})
	}
}

func TestManifestLoadJSONAndYAML(t *testing.T) {
	dir := t.TempDir()
	jsonP := dir + "/g.json"
	yamlP := dir + "/g.yaml"
	mustWrite(t, jsonP, `{"id":"p.json","type":"http","tools":[{"name":"a","method":"GET","path":"/a"}]}`)
	mustWrite(t, yamlP, "id: p.yaml\ntype: http\ntools:\n  - name: a\n    method: GET\n    path: /a\n")
	for _, f := range []string{jsonP, yamlP} {
		p, err := ManifestFromFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(p.Tools) != 1 || p.Tools[0].Name != "a" {
			t.Fatalf("%s: bad plugin %+v", f, p)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := writeFile(path, content); err != nil {
		t.Fatal(err)
	}
}

// Tier-1 review #3 回归:插件 ID 白名单完整边界。
// 合法 = [A-Za-z0-9_.-] 且非精确 . / ..（点保留命名空间式 ID,不产生路径
// 分量）;非法 = 空、路径分隔符、穿越序列、空白、非 ASCII、shell 元字符。
func TestValidPluginID(t *testing.T) {
	valid := []string{
		"x", "http.def", "demo.google_search", "p.one", "a-b_c.0",
		"a..b", // 内部点无害:Join(dir, "a..b"+ext) 是普通文件名
		"..a",  // 前缀点无害:Join(dir, "..a"+ext) 是普通文件名
		"-", "_",
	}
	invalid := []string{
		"",    // 空
		".",   // Join(dir, ".") → dir 本身
		"..",  // Join(dir, "..") → 父目录（穿越）
		"a/b", // 路径分隔符
		`a\b`, // Windows 分隔符
		"../config", "a/../b",
		"a b", // 空白
		"你",   // 非 ASCII
		"a<b", "a|b", "a:b", "a*b",
	}
	for _, id := range valid {
		if !validPluginID(id) {
			t.Errorf("validPluginID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if validPluginID(id) {
			t.Errorf("validPluginID(%q) = true, want false", id)
		}
	}
}

// Tier-2 review #12 回归:Tool.InterfaceHash —— 稳定 ABI 指纹,声明任意
// 字段变化驱动 hash 漂移（对齐 pkg/tool.computeInterfaceHash 的
// 长度前缀 sha256 风格,供桥接/绑定层检测声明漂移）。
func TestToolInterfaceHash(t *testing.T) {
	base := Tool{
		Name: "speak", Desc: "speak text", Method: "GET", Path: "/speak",
		Params: []ParamSchema{
			{Name: "voice", Type: "string", Desc: "voice id", Required: true,
				Default: "default_voice", Enum: []any{"a", "b"}, In: InQuery},
			{Name: "rate", Type: "number", Min: f64p(0.5), Max: f64p(2.0)},
		},
		Outputs: []OutputField{{Name: "audio", Type: "string"}},
	}
	h := base.InterfaceHash()
	if h == "" || !strings.HasPrefix(h, "sha256:") {
		t.Fatalf("hash = %q, want sha256: prefix", h)
	}
	if again := base.InterfaceHash(); again != h {
		t.Fatalf("non-deterministic hash: %q vs %q", h, again)
	}

	cases := []struct {
		name   string
		mutate func(*Tool)
	}{
		{"name", func(t *Tool) { t.Name = "sing" }},
		{"desc", func(t *Tool) { t.Desc = "different" }},
		{"method", func(t *Tool) { t.Method = "POST" }},
		{"path", func(t *Tool) { t.Path = "/other" }},
		{"param name", func(t *Tool) { t.Params[0].Name = "voice_id" }},
		{"param type", func(t *Tool) { t.Params[0].Type = "any" }},
		{"param required", func(t *Tool) { t.Params[0].Required = false }},
		{"param default", func(t *Tool) { t.Params[0].Default = "other" }},
		{"param enum", func(t *Tool) { t.Params[0].Enum = []any{"a"} }},
		{"param min", func(t *Tool) { t.Params[1].Min = f64p(0.1) }},
		{"param max", func(t *Tool) { t.Params[1].Max = nil }},
		{"param in", func(t *Tool) { t.Params[0].In = InBody }},
		{"output name", func(t *Tool) { t.Outputs[0].Name = "url" }},
		{"output type", func(t *Tool) { t.Outputs[0].Type = "any" }},
	}
	for _, tc := range cases {
		d := base
		d.Params = append([]ParamSchema(nil), base.Params...)
		d.Outputs = append([]OutputField(nil), base.Outputs...)
		tc.mutate(&d)
		if got := d.InterfaceHash(); got == h {
			t.Errorf("%s: hash did not change", tc.name)
		}
	}
}

func f64p(v float64) *float64 { return &v }

// Tier-2 #12 回归:Default/Enum 类型敏感（与 tool 侧同规则）。
// %v 会把 Default int(1) 与 float64(1) 都渲染成 "1",ABI 指纹必须区分。
func TestToolInterfaceHashDefaultTypeSensitive(t *testing.T) {
	intDefault := Tool{Name: "x", Params: []ParamSchema{{Name: "n", Type: "int", Default: 1}}}
	floatDefault := Tool{Name: "x", Params: []ParamSchema{{Name: "n", Type: "int", Default: 1.0}}}
	if intDefault.InterfaceHash() == floatDefault.InterfaceHash() {
		t.Error("hash collision: Default int(1) and float64(1) hash identically — 约束序列化必须类型敏感")
	}
}
