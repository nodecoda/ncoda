package pluginhost

// 函数级 fidelity（清单 v1 扩展）判据：
//   - 字符串与映射两种 functions 元素可混用；
//   - 有效保真度 = 函数级覆盖 > 库级；未写则继承；
//   - partial/stub 的 loss 可以写在函数上，也可以写在库上；
//   - 头文件没有的函数、重复声明、无 name 的映射、非法 fidelity 都必须拒绝。

import (
	"strings"
	"testing"
)

func loadBody(t *testing.T, body string) (*Manifest, error) {
	t.Helper()
	return Load(writeManifest(t, body), headers(t))
}

func policyManifest(functions string, libExtra string) string {
	return `schema: ncoda.plugin/v1
id: policy-test
version: 0.1.0
protocol: ncoda-plugin/1
command: ["python3", "-c", "pass"]
libraries:
  - library: capcut_data
    version: 0.1.0
` + libExtra + `    functions:
` + functions
}

func TestFunctionLevelFidelityParses(t *testing.T) {
	m, err := loadBody(t, policyManifest(`      - get_url
      - timelines
      - name: wenan_split
        fidelity: partial
        loss: "切分策略未证实"
`, "    fidelity: real\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lib := m.Libraries[0]
	if got := strings.Join(lib.Functions, ","); got != "get_url,timelines,wenan_split" {
		t.Fatalf("functions = %q", got)
	}
	if fid, loss := lib.FidelityFor("get_url"); fid != "real" || loss != "" {
		t.Fatalf("get_url = (%q,%q), want (real,\"\")", fid, loss)
	}
	if fid, loss := lib.FidelityFor("wenan_split"); fid != "partial" || loss != "切分策略未证实" {
		t.Fatalf("wenan_split = (%q,%q)", fid, loss)
	}
	// 符号面不变：保真度不产生新符号。
	if symbols := m.Symbols(); len(symbols) != 3 {
		t.Fatalf("symbols = %v", symbols)
	}
}

func TestFunctionFidelityInheritsLibraryLoss(t *testing.T) {
	m, err := loadBody(t, policyManifest(`      - name: wenan_split
        fidelity: partial
      - get_url
`, "    fidelity: partial\n    loss: \"库级 loss 覆盖全部函数\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lib := m.Libraries[0]
	fid, loss := lib.FidelityFor("wenan_split")
	if fid != "partial" || loss != "库级 loss 覆盖全部函数" {
		t.Fatalf("wenan_split = (%q,%q), want inherited loss", fid, loss)
	}
	if fid, loss := lib.FidelityFor("get_url"); fid != "partial" || loss == "" {
		t.Fatalf("get_url = (%q,%q), want library-level partial with loss", fid, loss)
	}
}

func TestFunctionPartialWithoutLossIsRejected(t *testing.T) {
	_, err := loadBody(t, policyManifest(`      - name: wenan_split
        fidelity: partial
`, "    fidelity: real\n"))
	if err == nil {
		t.Fatal("partial without loss must be rejected")
	}
	if !strings.Contains(err.Error(), "requires a non-empty loss") {
		t.Fatalf("error = %v", err)
	}
}

func TestFunctionUndeclaredInHeaderIsRejected(t *testing.T) {
	_, err := loadBody(t, policyManifest(`      - name: ghost_fn
        fidelity: real
`, "    fidelity: real\n"))
	if err == nil {
		t.Fatal("header-undeclared function must be rejected")
	}
	if !strings.Contains(err.Error(), "not declared in the header") {
		t.Fatalf("error = %v", err)
	}
}

func TestFunctionDeclaredTwiceIsRejected(t *testing.T) {
	_, err := loadBody(t, policyManifest(`      - get_url
      - name: get_url
        fidelity: real
`, "    fidelity: real\n"))
	if err == nil {
		t.Fatal("duplicate function declaration must be rejected")
	}
	if !strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("error = %v", err)
	}
}

func TestFunctionEntryWithoutNameIsRejected(t *testing.T) {
	_, err := loadBody(t, policyManifest(`      - fidelity: real
`, "    fidelity: real\n"))
	if err == nil {
		t.Fatal("mapping entry without name must be rejected")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestFunctionFidelityValueIsValidated(t *testing.T) {
	_, err := loadBody(t, policyManifest(`      - name: get_url
        fidelity: mostly
`, "    fidelity: real\n"))
	if err == nil {
		t.Fatal("unknown fidelity must be rejected")
	}
	if !strings.Contains(err.Error(), "want real|partial|stub") {
		t.Fatalf("error = %v", err)
	}
}

func TestFunctionFidelityDoesNotChangeHandshakeSymbols(t *testing.T) {
	// 回归：函数级保真度只影响登记/诊断，握手仍按符号集合精确匹配。
	requirePython(t)
	body := baseManifest(t, "") // 全是库级 real
	m, err := Load(writeManifest(t, body), headers(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(m.Symbols()); got != 3 {
		t.Fatalf("symbols = %d, want 3", got)
	}
	for _, fn := range m.Libraries[0].Functions {
		if fid, _ := m.Libraries[0].FidelityFor(fn); fid != "real" {
			t.Fatalf("%s = %q, want real", fn, fid)
		}
	}
}
