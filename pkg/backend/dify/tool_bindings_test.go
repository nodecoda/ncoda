package dify

import (
	"os"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/tool"
)

func TestNewToolBindingManifestRejectsInvalidBindings(t *testing.T) {
	binding := ToolBinding{
		Provider: "web_search", Operation: "search",
		ProviderID: "langgenius/google", ToolName: "web_search",
	}
	if _, err := NewToolBindingManifest(binding, binding); err == nil || !strings.Contains(err.Error(), "duplicate binding") {
		t.Fatalf("duplicate binding error = %v, want duplicate binding", err)
	}

	binding.ToolName = ""
	if _, err := NewToolBindingManifest(binding); err == nil || !strings.Contains(err.Error(), "requires provider ID") {
		t.Fatalf("missing physical ID error = %v, want provider/tool identity", err)
	}
}

func TestLoadToolBindingManifestYAML(t *testing.T) {
	manifest, err := LoadToolBindingManifestYAML([]byte(`version: v1
bindings:
  - provider: web_search
    operation: search
    provider_id: langgenius/google
    tool_name: web_search
    provider_type: builtin
    tool_node_version: "2"
    arguments:
      query: search_query
`))
	if err != nil {
		t.Fatalf("LoadToolBindingManifestYAML: %v", err)
	}
	call := &ir.ToolCall{ID: "tool", ProviderID: "web_search", Operation: "search"}
	binding, err := manifest.resolve(call)
	if err != nil {
		t.Fatalf("loaded manifest resolve: %v", err)
	}
	if binding.ProviderID != "langgenius/google" || binding.Arguments["query"] != "search_query" {
		t.Fatalf("loaded binding = %#v", binding)
	}
	if _, err := LoadToolBindingManifestYAML([]byte("version: v1\nbindings: []\nunknown: true\n")); err == nil {
		t.Fatal("unknown YAML field accepted")
	}
}

func TestRenderWithToolBindingsLinksPhysicalIdentityAndArguments(t *testing.T) {
	providerName := "Google"
	description := "Search the web"
	version := "2"
	argumentNames := map[string]string{"query": "search_query", "limit": "max_results"}
	bindings, err := NewToolBindingManifest(ToolBinding{
		Provider: "web_search", Operation: "search",
		ProviderID: "langgenius/google", ToolName: "web_search", ProviderType: "builtin", ToolLabel: "Web Search",
		ProviderName: &providerName, ToolDescription: &description, ToolNodeVersion: &version,
		Arguments: argumentNames,
	})
	if err != nil {
		t.Fatalf("NewToolBindingManifest: %v", err)
	}
	argumentNames["query"] = "mutated"
	providerName = "mutated"

	call := &ir.ToolCall{
		ID: "tool", ToolName: "web_search", ProviderID: "web_search", Operation: "search",
		Params: []ir.ToolParam{
			{Key: "query", Ref: &ir.Source{BlockID: "start", Name: "query"}},
			{Key: "limit", Value: int64(5)},
		},
	}
	ast, err := RenderFromIRWithToolBindings(toolGraph(call), "workflow", bindings, nil)
	if err != nil {
		t.Fatalf("RenderFromIRWithToolBindings: %v", err)
	}
	data := onlyToolData(t, ast)
	if data.ProviderID != "langgenius/google" || data.ToolName != "web_search" || data.ProviderType != "builtin" || data.ToolLabel != "Web Search" {
		t.Fatalf("tool identity = %#v, want binding identity", data)
	}
	if data.ProviderName == nil || *data.ProviderName != "Google" || data.ToolDescription == nil || *data.ToolDescription != "Search the web" || data.ToolNodeVersion == nil || *data.ToolNodeVersion != "2" {
		t.Fatalf("tool metadata = %#v, want binding metadata", data)
	}
	if got, want := data.ToolParameterOrder, []string{"search_query", "max_results"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("parameter order = %v, want %v", got, want)
	}
	if _, ok := data.ToolParameters["search_query"]; !ok {
		t.Fatalf("parameters = %#v, want bound search_query", data.ToolParameters)
	}
	if call.ProviderID != "web_search" || call.Params[0].Key != "query" {
		t.Fatalf("renderer mutated semantic IR call: %#v", call)
	}
}

func TestRenderWithToolBindingsRejectsMissingNestedBinding(t *testing.T) {
	bindings, err := NewToolBindingManifest()
	if err != nil {
		t.Fatalf("NewToolBindingManifest: %v", err)
	}
	call := &ir.ToolCall{ID: "tool", ToolName: "web_search", ProviderID: "web_search", Operation: "search"}
	loop := &ir.Loop{
		ID: "loop", LoopType: "by_array",
		Inputs: []ir.Source{{Path: []string{"items"}, BlockID: "start", Name: "items"}},
	}
	graph := &ir.Graph{
		Entry: &ir.Start{ID: "start", Params: []ir.Param{{Name: "items", Type: ir.TList(ir.T("string"))}}},
		Root: &ir.Block{Stmts: []ir.Stmt{&ir.LoopStmt{
			ID: loop.ID, Container: loop, LoopType: loop.LoopType, Inputs: loop.Inputs,
			Body: &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: call}}},
		}}},
	}
	if _, err := RenderFromIRWithToolBindings(graph, "workflow", bindings, nil); err == nil || !strings.Contains(err.Error(), `no binding for "web_search/search"`) {
		t.Fatalf("nested missing binding error = %v, want explicit link failure", err)
	}
}

func toolGraph(call *ir.ToolCall) *ir.Graph {
	return &ir.Graph{
		Entry: &ir.Start{ID: "start", Params: []ir.Param{{Name: "query", Type: ir.T("string")}}},
		Root:  &ir.Block{Stmts: []ir.Stmt{&ir.NodeStmt{Node: call}}},
	}
}

func onlyToolData(t *testing.T, ast *WorkflowAST) *ToolDataNode {
	t.Helper()
	for _, node := range ast.Graph.Nodes {
		if data, ok := node.Data.(*ToolDataNode); ok {
			return data
		}
	}
	t.Fatal("workflow has no ToolDataNode")
	return nil
}

func TestToolBindingOutputsValidation(t *testing.T) {
	// P3.3 §16.4: Dify output mapping mirrors Coze — semantic names must be
	// unique and target names may not collide.
	base := ToolBinding{
		Provider: "web_search", Operation: "search",
		ProviderID: "langgenius/google", ToolName: "web_search",
		Outputs: map[string]string{"title": "title", "link": "url"},
	}
	if _, err := NewToolBindingManifest(base); err != nil {
		t.Fatalf("valid outputs rejected: %v", err)
	}
	dup := base
	dup.Outputs = map[string]string{"title": "url", "link": "url"}
	if _, err := NewToolBindingManifest(dup); err == nil || !strings.Contains(err.Error(), "maps multiple outputs to") {
		t.Fatalf("duplicate target error = %v, want multiple-outputs rejection", err)
	}
	empty := base
	empty.Outputs = map[string]string{"": "url"}
	if _, err := NewToolBindingManifest(empty); err == nil || !strings.Contains(err.Error(), "empty output mapping") {
		t.Fatalf("empty semantic name error = %v, want empty output mapping", err)
	}
}

func TestRenameOutputSchema(t *testing.T) {
	// P3.3 §16.4: output_schema keys follow binding.Outputs renames; unmapped
	// keys stay; nil/empty maps are no-ops.
	binding := &ToolBinding{Outputs: map[string]string{"title": "title_value", "type": "type_"}}
	schema := map[string]any{
		"title":     map[string]any{"type": "string"},
		"type":      map[string]any{"type": "string"},
		"untouched": map[string]any{"type": "int"},
	}
	got := renameOutputSchema(binding, schema)
	if _, ok := got["title"]; ok {
		t.Error("semantic key 'title' survived rename")
	}
	if got["title_value"] == nil {
		t.Error("mapped key 'title_value' missing")
	}
	if got["type_"] == nil {
		t.Error("mapped key 'type_' missing")
	}
	if got["untouched"] == nil {
		t.Error("unmapped key 'untouched' missing")
	}
	// The caller owns the original map: the input must not be mutated.
	if schema["title"] == nil {
		t.Error("input schema mutated")
	}
	if got2 := renameOutputSchema(nil, schema); got2 == nil || len(got2) != 3 {
		t.Error("nil binding must be a no-op")
	}
}

// ---- P3.5 §5 绑定随包: MergePackageBindings ----

const difyPackageBindingTTS = `version: v1
bindings:
  - provider: tts
    operation: speech_synthesis
    provider_id: "langgenius/tts"
    tool_name: "speech_synthesis"
    provider_type: "builtin"
    arguments:
      text: text
      language: language
`

func writeDifyPackageBindingFile(t *testing.T, dir, provider, body string) string {
	t.Helper()
	path := dir + "/" + provider + ".dify.yaml"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	return path
}

func loadDifyTTSPackageRegistry(t *testing.T, dir string) *tool.Registry {
	t.Helper()
	header := dir + "/tts.ncoda"
	body := `@language nodecoda/1
type TtsResult = { string url; };
function speech_synthesis(string text, string language = "zh") -> TtsResult {}
`
	if err := os.WriteFile(header, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", header, err)
	}
	registry, err := tool.LoadCapabilityPackages([]string{dir}, []string{"tts"})
	if err != nil {
		t.Fatalf("LoadCapabilityPackages: %v", err)
	}
	return registry
}

func resolveDifyBinding(t *testing.T, m *ToolBindingManifest, provider, operation string) *ToolBinding {
	t.Helper()
	binding, err := m.resolve(&ir.ToolCall{ID: "probe", ProviderID: provider, Operation: operation})
	if err != nil {
		t.Fatalf("resolve(%s/%s): %v", provider, operation, err)
	}
	return binding
}

func TestMergePackageBindingsCentralPlusPackage(t *testing.T) {
	dir := t.TempDir()
	central, err := NewToolBindingManifest(ToolBinding{
		Provider: "web_search", Operation: "search",
		ProviderID: "langgenius/google", ToolName: "web_search",
	})
	if err != nil {
		t.Fatalf("NewToolBindingManifest: %v", err)
	}
	pkgFile := writeDifyPackageBindingFile(t, dir, "tts", difyPackageBindingTTS)

	merged, err := MergePackageBindings(central, []string{pkgFile}, "dify", nil)
	if err != nil {
		t.Fatalf("MergePackageBindings: %v", err)
	}
	if resolveDifyBinding(t, merged, "web_search", "search") == nil {
		t.Fatal("central binding lost after merge")
	}
	binding := resolveDifyBinding(t, merged, "tts", "speech_synthesis")
	if binding.ProviderID != "langgenius/tts" || binding.Arguments["language"] != "language" {
		t.Fatalf("package binding = %#v, want bound provider id + argument mapping", binding)
	}
}

func TestMergePackageBindingsNilCentralUsesPackagesAlone(t *testing.T) {
	dir := t.TempDir()
	pkgFile := writeDifyPackageBindingFile(t, dir, "tts", difyPackageBindingTTS)

	merged, err := MergePackageBindings(nil, []string{pkgFile}, "dify", nil)
	if err != nil {
		t.Fatalf("MergePackageBindings: %v", err)
	}
	if merged == nil {
		t.Fatal("nil central with package files must produce a manifest")
	}
	if resolveDifyBinding(t, merged, "tts", "speech_synthesis") == nil {
		t.Fatal("package-only manifest missing tts/speech_synthesis")
	}
}

func TestMergePackageBindingsProviderMismatch(t *testing.T) {
	dir := t.TempDir()
	// File is named video_parse.dify.yaml but declares provider tts.
	mismatch := `version: v1
bindings:
  - provider: tts
    operation: speech_synthesis
    provider_id: "langgenius/tts"
    tool_name: "speech_synthesis"
`
	pkgFile := writeDifyPackageBindingFile(t, dir, "video_parse", mismatch)
	_, err := MergePackageBindings(nil, []string{pkgFile}, "dify", nil)
	if err == nil || !strings.Contains(err.Error(), "video_parse.dify.yaml declares provider \"tts\" (expected \"video_parse\")") {
		t.Fatalf("provider mismatch error = %v, want file/provider consistency check", err)
	}
}

func TestMergePackageBindingsUndeclaredOperation(t *testing.T) {
	dir := t.TempDir()
	registry := loadDifyTTSPackageRegistry(t, dir)
	bad := `version: v1
bindings:
  - provider: tts
    operation: no_such_op
    provider_id: "langgenius/tts"
    tool_name: "no_such_op"
`
	pkgFile := writeDifyPackageBindingFile(t, dir, "tts", bad)
	_, err := MergePackageBindings(nil, []string{pkgFile}, "dify", registry)
	if err == nil || !strings.Contains(err.Error(), `operation "no_such_op" is not declared by provider "tts"`) {
		t.Fatalf("undeclared operation error = %v, want registry check", err)
	}
}

func TestMergePackageBindingsCentralConflictIsHardError(t *testing.T) {
	dir := t.TempDir()
	central, err := NewToolBindingManifest(ToolBinding{
		Provider: "tts", Operation: "speech_synthesis",
		ProviderID: "central", ToolName: "central",
	})
	if err != nil {
		t.Fatalf("NewToolBindingManifest: %v", err)
	}
	pkgFile := writeDifyPackageBindingFile(t, dir, "tts", difyPackageBindingTTS)
	_, err = MergePackageBindings(central, []string{pkgFile}, "dify", nil)
	if err == nil || !strings.Contains(err.Error(), `conflict on "tts/speech_synthesis"`) {
		t.Fatalf("conflict error = %v, want explicit central/package conflict", err)
	}
}

func TestMergePackageBindingsNoPackageFilesPassesCentralThrough(t *testing.T) {
	central, err := NewToolBindingManifest(ToolBinding{
		Provider: "web_search", Operation: "search",
		ProviderID: "langgenius/google", ToolName: "web_search",
	})
	if err != nil {
		t.Fatalf("NewToolBindingManifest: %v", err)
	}
	merged, err := MergePackageBindings(central, nil, "dify", nil)
	if err != nil {
		t.Fatalf("MergePackageBindings: %v", err)
	}
	if merged != central {
		t.Fatal("no package files must return the central manifest unchanged")
	}
}
