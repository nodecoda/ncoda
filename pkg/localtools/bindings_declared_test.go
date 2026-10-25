package localtools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 这条判据补的是本轮真发生的漂移：L1' 把 image_generate 的函数名从
// image_generator 改成 generate，绑定表没跟，唯一的端到端路径静默断掉
// （RBINDING_MISSING）。Alias 只校验「目标已注册」，不校验「名字是不是
// 头文件里声明的库/函数」——所以需要这一层。
//
// 判据：bindings.yaml 里每个 provider 都必须是 capability 头的 library 名，
// operation 必须是该头声明过的 function。平台 id 形态（plugin_<id>、裸数字）
// 不得出现在语言层文件；它们只属于 platform.RegisterPlatformAliases。

var (
	reLibrary  = regexp.MustCompile(`(?m)^library\s+([A-Za-z_][A-Za-z0-9_]*)\s*@`)
	reFunction = regexp.MustCompile(`(?m)^function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

func declaredFunctions(t *testing.T, dir string) map[string]map[string]bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read capability headers: %v", err)
	}
	out := map[string]map[string]bool{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ncoda") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		m := reLibrary.FindSubmatch(b)
		if m == nil {
			continue
		}
		fns := map[string]bool{}
		for _, f := range reFunction.FindAllSubmatch(b, -1) {
			fns[string(f[1])] = true
		}
		out[string(m[1])] = fns
	}
	if len(out) == 0 {
		t.Fatalf("no capability headers parsed under %s", dir)
	}
	return out
}

func undeclaredBindings(bs []binding, libs map[string]map[string]bool) []string {
	var bad []string
	for _, b := range bs {
		fns, ok := libs[b.Provider]
		if !ok {
			bad = append(bad, b.Provider+"/"+b.Operation+" (provider not declared)")
			continue
		}
		if !fns[b.Operation] {
			bad = append(bad, b.Provider+"/"+b.Operation)
		}
	}
	return bad
}

func TestBindingsNameDeclaredFunctions(t *testing.T) {
	var f bindingFile
	if err := yaml.Unmarshal(bindingsYAML, &f); err != nil {
		t.Fatalf("parse bindings.yaml: %v", err)
	}
	libs := declaredFunctions(t, filepath.Join("..", "..", "libraries"))

	if bad := undeclaredBindings(f.Bindings, libs); len(bad) > 0 {
		t.Fatalf("bindings name functions no header declares: %v", bad)
	}

	// 非空转证明：至少一条绑定真的落在已解析的库上。
	checked := 0
	for _, b := range f.Bindings {
		if _, ok := libs[b.Provider]; ok {
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no binding was checked against a declared library — the gate is vacuous")
	}

	// 负控：本轮漂移的那个组合必须被判出来。
	drift := []binding{{Provider: "image_generate", Operation: "image_generator"}}
	if bad := undeclaredBindings(drift, libs); len(bad) != 1 {
		t.Fatalf("negative control: want 1 undeclared binding, got %v", bad)
	}

	// 负控：平台物理 id 混进语言层也必须红。
	platformID := []binding{{Provider: "plugin_7547770968681447458", Operation: "image_generator"}}
	if bad := undeclaredBindings(platformID, libs); len(bad) != 1 {
		t.Fatalf("negative control: platform id accepted in language bindings: %v", bad)
	}
}

func TestBindingsHaveImplementationAndNoDuplicates(t *testing.T) {
	var f bindingFile
	if err := yaml.Unmarshal(bindingsYAML, &f); err != nil {
		t.Fatalf("parse bindings.yaml: %v", err)
	}
	if err := validateBindings(f.Bindings); err != nil {
		t.Fatalf("bindings schema: %v", err)
	}
	// v2：real 之外允许 partial/stub，但必须显式声明 loss —— 否则“近似实现”
	// 会被下游当成 real 消费。real 绑定不需要 loss。
	realCount, partialCount := 0, 0
	for _, b := range f.Bindings {
		switch b.Implementation {
		case "real":
			realCount++
		case "partial", "stub":
			partialCount++
			if strings.TrimSpace(b.Loss) == "" {
				t.Fatalf("%s/%s implementation = %q without loss declaration", b.Provider, b.Operation, b.Implementation)
			}
		default:
			t.Fatalf("%s/%s implementation = %q, want real|partial|stub", b.Provider, b.Operation, b.Implementation)
		}
	}
	if realCount == 0 || partialCount == 0 {
		t.Fatalf("binding mix = %d real / %d partial+stub; want both classes represented", realCount, partialCount)
	}
	// L1' 兼容入口必须留在语言层：移走会让 complete 86 → 83。
	found := false
	for _, b := range f.Bindings {
		if b.Provider == "image_generate_fast" && b.Operation == "image_generator" {
			found = true
		}
	}
	if !found {
		t.Fatal("image_generate_fast/image_generator missing from language bindings")
	}

	if err := validateBindings([]binding{{Provider: "x", Operation: "y", Capability: "z"}}); err == nil {
		t.Fatal("negative control: missing implementation accepted")
	}
	if err := validateBindings([]binding{{Provider: "x", Operation: "y", Capability: "z", Implementation: "fake"}}); err == nil {
		t.Fatal("negative control: unknown implementation accepted")
	}
	if err := validateBindings([]binding{{Provider: "x", Operation: "y", Capability: "z", Implementation: "partial"}}); err == nil {
		t.Fatal("negative control: partial binding without loss accepted")
	}
	if err := validateBindings([]binding{{Provider: "x", Operation: "y", Capability: "z", Implementation: "stub", Loss: "shape only"}}); err != nil {
		t.Fatalf("stub with loss must be accepted: %v", err)
	}
	dup := []binding{
		{Provider: "x", Operation: "y", Capability: "z", Implementation: "real"},
		{Provider: "x", Operation: "y", Capability: "z", Implementation: "real"},
	}
	if err := validateBindings(dup); err == nil {
		t.Fatal("negative control: duplicate provider/operation accepted")
	}
}
