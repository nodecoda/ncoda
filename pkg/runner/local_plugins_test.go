package runner

// `--local-plugins=<lib,…>` 的 CLI 判据：
//   - 子集只注册选中的库；
//   - 未知库名 rc=2（用法错，不是运行失败）；
//   - 裸 flag 仍是全开；
//   - 子集 + 进程外插件可以共存（这正是该功能的用途）。

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLocalPluginsFlagParsing(t *testing.T) {
	for _, tc := range []struct {
		in      string
		enabled bool
		libs    []string
	}{
		{"true", true, nil},
		{"", true, nil},
		{"all", true, nil},
		{"false", false, nil},
		{"database", true, []string{"database"}},
		{"database,tts", true, []string{"database", "tts"}},
		{" database , tts ", true, []string{"database", "tts"}},
	} {
		f := &localPluginsFlag{}
		if err := f.Set(tc.in); err != nil {
			t.Fatalf("Set(%q): %v", tc.in, err)
		}
		if f.enabled != tc.enabled {
			t.Fatalf("Set(%q).enabled = %v, want %v", tc.in, f.enabled, tc.enabled)
		}
		if strings.Join(f.libs, ",") != strings.Join(tc.libs, ",") {
			t.Fatalf("Set(%q).libs = %v, want %v", tc.in, f.libs, tc.libs)
		}
	}
	if err := (&localPluginsFlag{}).Set(" , "); err == nil {
		t.Fatal("empty library list must be rejected")
	}
}

func symbolListing(t *testing.T, args ...string) []string {
	t.Helper()
	rc, out, errs := runCLI(t, "", append(args, "--list-symbols")...)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got struct {
		PluginHosts int      `json:"plugin_hosts"`
		Symbols     []string `json:"symbols"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	return got.Symbols
}

func TestLocalPluginsSubsetRegistersOnlyThatLibrary(t *testing.T) {
	symbols := symbolListing(t, "--local-plugins=database", "--tool-path", "../../libraries")
	if len(symbols) == 0 {
		t.Fatal("no symbols")
	}
	for _, s := range symbols {
		if !strings.HasPrefix(s, "database/") && !strings.HasPrefix(s, "database.") &&
			!strings.HasPrefix(s, "local_database/") {
			t.Fatalf("unexpected symbol %q in a database-only listing: %v", s, symbols)
		}
	}
}

func TestLocalPluginsUnknownLibraryIsUsageError(t *testing.T) {
	rc, _, errs := runCLI(t, "", "--local-plugins=bogus", "--tool-path", "../../libraries", "--list-symbols")
	if rc != 2 {
		t.Fatalf("rc=%d, want 2; stderr=%s", rc, errs)
	}
	if !strings.Contains(errs, "unknown library") {
		t.Fatalf("stderr = %s", errs)
	}
}

func TestLocalPluginsBareFlagRegistersEverything(t *testing.T) {
	symbols := symbolListing(t, "--local-plugins", "--tool-path", "../../libraries")
	want := map[string]bool{"database/insert": false, "tts/speech_synthesis": false}
	for _, s := range symbols {
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("%q missing from bare --local-plugins listing", name)
		}
	}
}

func TestLocalPluginsSubsetCoexistsWithOutOfProcessPlugin(t *testing.T) {
	mf := pythonPlugin(t) // 声明 array_count + common_code_block
	symbols := symbolListing(t, "--local-plugins=database", "--plugin", mf,
		"--tool-path", "../../libraries")
	want := map[string]bool{
		"database/insert":                         false, // 内建选中的库
		"array_count/array_count":                 false, // 插件接管
		"ext_test-python_array_count/array_count": false,
	}
	for _, s := range symbols {
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("%q missing when subset (database) coexists with plugin; symbols=%v", name, symbols)
		}
	}
}
