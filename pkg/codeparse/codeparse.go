// Package codeparse is the single implementation of the foreign-code
// well-formedness check (P6 · SEM-CODE-001/002).
//
// 为什么只有一份：语法合法性与 `(source, language, version)` 绑定，而
// language/version 在两侧的可见性不同 —— 编译侧只知道目标声明版本，
// runtime 只知道实际解释器版本。两侧共用**同一判定实现**，只是证据等级
// 不同（policy/预测 vs wire-present/观测）。任何一侧另写一套 regex 都是
// 第二定义。
//
// 边界：只编译（compile-only），**从不执行**用户代码。Python 用
// `compile()`（不 import、不 eval），Node 用 `vm.Script` /
// `vm.SourceTextModule`（只编译不 run），并复用 Node 自己的
// `stripTypeScriptTypes` 与 stdin 的 ESM 自动检测，避免自造 TS 子集。
package codeparse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Item is one foreign-code node to check. ID is the node id used in the
// diagnostic and the trace record.
type Item struct {
	ID   string `json:"id"`
	Lang string `json:"lang"` // "python" | "javascript"
	Code string `json:"code"`
}

// Result is the per-item outcome. Err is a short, value-free message: it may
// quote compiler diagnostics (line numbers / message), never the source.
type Result struct {
	ID  string `json:"id"`
	OK  bool   `json:"ok"`
	Err string `json:"error,omitempty"`
}

// DeclaredPythonVersion is the platform's documented Python version for code
// nodes (coze docs list the 3.9 stdlib; coze-studio OSS code.go cites
// https://docs.python.org/3.9/library/). It is a policy value, not an
// observation: the runtime pin only exists to make the skew observable, never
// to reject a workflow. Empty = no declared version (no skew record).
const DeclaredPythonVersion = "3.9"

// Versions records the actual interpreter versions used for the judgement.
// It is the observable half of the version axis: without it the verdict is
// not reproducible.
type Versions struct {
	Python string
	Node   string
}

// String renders the versions for a trace `provenance` field.
func (v Versions) String() string {
	parts := make([]string, 0, 2)
	if v.Python != "" {
		parts = append(parts, "python "+v.Python)
	}
	if v.Node != "" {
		parts = append(parts, "node "+v.Node)
	}
	return strings.Join(parts, "; ")
}

// MissingInterpreterError reports that a language was requested but its
// interpreter is not on PATH. Callers map it to their own diagnostic code.
type MissingInterpreterError struct {
	Language string
	Err      error
}

func (e *MissingInterpreterError) Error() string {
	return fmt.Sprintf("%s interpreter unavailable: %v", e.Language, e.Err)
}

func (e *MissingInterpreterError) Unwrap() error { return e.Err }

// Check compiles every item without executing it. Items are grouped by
// language so one interpreter start covers the whole workflow (a per-node
// process would cost ~30ms each).
//
// A returned error means the *check itself* could not run (missing
// interpreter / unusable batch output); per-item syntax failures are in the
// results, not in the error.
func Check(items []Item) ([]Result, Versions, error) {
	var py, js []Item
	for _, it := range items {
		switch it.Lang {
		case "python":
			py = append(py, it)
		case "javascript":
			js = append(js, it)
		default:
			// Unknown language is not this package's verdict; the caller's
			// language gate owns it (RCODE_LANGUAGE_UNSUPPORTED).
			continue
		}
	}
	results := make([]Result, 0, len(py)+len(js))
	var versions Versions
	if len(py) > 0 {
		v, err := probePythonVersion()
		if err != nil {
			return nil, Versions{}, &MissingInterpreterError{Language: "python", Err: err}
		}
		versions.Python = v
		r, err := runChecker(pyScript, "python3", []string{"-I", "-c"}, py)
		if err != nil {
			return nil, versions, err
		}
		results = append(results, r...)
	}
	if len(js) > 0 {
		v, err := probeNodeVersion()
		if err != nil {
			return nil, versions, &MissingInterpreterError{Language: "javascript", Err: err}
		}
		versions.Node = v
		// The JS side must strip the coze TS signature first — the exact same
		// rule the runtime executor uses (see strip.go).
		stripped := make([]Item, len(js))
		for i, it := range js {
			stripped[i] = Item{ID: it.ID, Lang: it.Lang, Code: StripJSTypes(it.Code)}
		}
		// Node's stdin loader auto-detects ESM syntax (import/export) after the
		// script goal fails, so the checker must do the same: Script first,
		// then SourceTextModule (--experimental-vm-modules).
		r, err := runChecker(jsScript, "node", []string{"--experimental-vm-modules", "--no-warnings", "-e"}, stripped)
		if err != nil {
			return nil, versions, err
		}
		results = append(results, r...)
	}
	return results, versions, nil
}

// SameMajorMinor compares two dotted versions by their first two components.
// The platform pin is major.minor (3.9), so patch-level drift is not skew.
func SameMajorMinor(actual, declared string) bool {
	a := strings.Split(actual, ".")
	d := strings.Split(declared, ".")
	if len(a) < 2 || len(d) < 2 {
		return actual == declared
	}
	return a[0] == d[0] && a[1] == d[1]
}

// CheckOne is the single-item convenience used by tests and the compiler.
func CheckOne(it Item) (Result, Versions, error) {
	rs, vs, err := Check([]Item{it})
	if err != nil {
		return Result{}, vs, err
	}
	if len(rs) == 0 {
		return Result{ID: it.ID, OK: true}, vs, nil
	}
	return rs[0], vs, nil
}

// runChecker feeds the items as JSON on stdin and parses the JSON verdicts.
// Sources never travel on argv (MAX_ARG_STRLEN).
func runChecker(script, bin string, flags []string, items []Item) ([]Result, error) {
	payload, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("codeparse: encode batch: %w", err)
	}
	argv := append(append([]string{}, flags...), script)
	cmd := exec.Command(bin, argv...)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codeparse: %s checker failed: %v: %s", bin, err, strings.TrimSpace(stderr.String()))
	}
	var out []Result
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("codeparse: %s checker produced unusable output: %w", bin, err)
	}
	return out, nil
}

// pyScript is compile-only: `compile()` parses and byte-compiles the module
// but never executes it (no imports, no file/network side effects).
const pyScript = `
import json, sys
items = json.load(sys.stdin)
out = []
for it in items:
    try:
        compile(it["code"], "<node:%s>" % it["id"], "exec")
        out.append({"id": it["id"], "ok": True})
    except SyntaxError as e:
        out.append({"id": it["id"], "ok": False,
                    "error": "%s: %s (line %s)" % (type(e).__name__, e.msg, e.lineno)})
    except Exception as e:
        out.append({"id": it["id"], "ok": False,
                    "error": "%s: %s" % (type(e).__name__, e)})
json.dump(out, sys.stdout)
`

// jsScript is compile-only: `new vm.Script` parses without running. Two
// runtime facts must be mirrored, or valid nodes become false positives:
//   - Node 22+ strips TypeScript types for stdin (evalTypeScript); the checker
//     therefore calls Node's own stripTypeScriptTypes, not a regex subset.
//   - ESM (import/export) is auto-detected after the script goal fails, so a
//     SourceTextModule parse is the second attempt.
//
// Neither branch executes code.
const jsScript = `
const vm = require('vm');
let raw = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', d => { raw += d; });
process.stdin.on('end', () => {
  const items = JSON.parse(raw);
  let strip = s => s;
  try {
    const mod = require('node:module');
    if (typeof mod.stripTypeScriptTypes === 'function') {
      strip = s => mod.stripTypeScriptTypes(s, {mode: 'strip'});
    }
  } catch (_) {}
  const out = items.map(it => {
    const filename = '<node:' + it.id + '>';
    let code;
    try {
      code = strip(it.code);
    } catch (e) {
      return {id: it.id, ok: false, error: String((e && e.name ? e.name + ': ' : '') + (e && e.message ? e.message : e))};
    }
    let scriptErr;
    try {
      new vm.Script(code, {filename});
      return {id: it.id, ok: true};
    } catch (e) {
      scriptErr = e;
    }
    try {
      new vm.SourceTextModule(code, {identifier: filename});
      return {id: it.id, ok: true};
    } catch (e) {
      const esm = /^\s*(import|export)\b/m.test(code);
      const err = esm ? e : scriptErr;
      return {id: it.id, ok: false, error: String((err && err.name ? err.name + ': ' : '') + (err && err.message ? err.message : err))};
    }
  });
  process.stdout.write(JSON.stringify(out));
});
`

func probePythonVersion() (string, error) {
	out, err := exec.Command("python3", "-I", "-V").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "Python ")), nil
}

func probeNodeVersion() (string, error) {
	out, err := exec.Command("node", "-v").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "v")), nil
}
