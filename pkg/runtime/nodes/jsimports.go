package nodes

// jsimports.go — 用户 JavaScript code 节点的**静态依赖面**扫描。
//
// 为什么要有这一层（契约，不是优化）：
//   - code 节点的 JS 源码是一个自包含编译单元；裸说明符（`dayjs` /
//     `lodash`）只有在执行环境提供模块根时才能解析。`node -` 没有
//     node_modules，缺模块的失败此前发生在**节点执行中**（RCODE_RUNTIME_ERROR
//     + node 栈），而且只在该节点真的被执行时才暴露 —— 未命中的分支会把
//     缺依赖藏起来（语料一手：`7508594537250766889_W0022` 的 lodash 节点在
//     未执行分支里，压测报 OK）。
//   - 装载期（Build）扫描 import/require/export-from/动态 import，把
//     「运行时无此模块」变成一条**开跑前**的 RCODE_MODULE_UNRESOLVED 诊断，
//     与子流程 Preflight 同一模型：依赖先行，执行在后。
//
// 只认 **裸说明符**：node 内置（`node:` 前缀或稳定内置表）与相对/绝对路径
// 不在这里判定（后者的存在性由运行期解析，属于另一条轴）。

import (
	"os"
	"path/filepath"
	"strings"
)

// jsBuiltinModules 是 Node 的稳定内置模块表（裸名；含常用子路径）。
// `node:` 前缀的形式由 isJSBuiltin 直接放行，不依赖本表。
var jsBuiltinModules = map[string]bool{
	"assert": true, "assert/strict": true, "async_hooks": true, "buffer": true,
	"child_process": true, "cluster": true, "console": true, "constants": true,
	"crypto": true, "dgram": true, "diagnostics_channel": true, "dns": true,
	"dns/promises": true, "domain": true, "events": true, "fs": true,
	"fs/promises": true, "http": true, "http2": true, "https": true,
	"inspector": true, "module": true, "net": true, "os": true, "path": true,
	"path/posix": true, "path/win32": true, "perf_hooks": true, "process": true,
	"punycode": true, "querystring": true, "readline": true,
	"readline/promises": true, "repl": true, "stream": true,
	"stream/consumers": true, "stream/promises": true, "stream/web": true,
	"string_decoder": true, "sys": true, "timers": true, "timers/promises": true,
	"tls": true, "trace_events": true, "tty": true, "url": true, "util": true,
	"util/types": true, "v8": true, "vm": true, "wasi": true,
	"worker_threads": true, "zlib": true,
}

// scanJSBareImports returns the bare (non-builtin, non-relative) module
// specifiers referenced by import / export-from / require / dynamic import,
// in first-appearance order. Strings, comments and template literals are
// skipped, so `// import x from "y"` is not a dependency.
func scanJSBareImports(src string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(spec string) {
		if spec == "" || seen[spec] || isJSBuiltin(spec) || isLocalSpecifier(spec) {
			return
		}
		seen[spec] = true
		out = append(out, spec)
	}

	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			i = skipJSLineComment(src, i)
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i = skipJSBlockComment(src, i)
		case c == '"' || c == '\'':
			_, i = readJSString(src, i)
		case c == '`':
			i = skipJSTemplate(src, i)
		case isJSIdentStart(c):
			start := i
			for i < len(src) && isJSIdentPart(src[i]) {
				i++
			}
			switch src[start:i] {
			case "import":
				j := skipJSSpace(src, i)
				if j < len(src) && src[j] == '.' { // import.meta
					i = j + 1
					continue
				}
				if j < len(src) && src[j] == '(' { // dynamic import("x")
					j = skipJSSpace(src, j+1)
					if j < len(src) && (src[j] == '"' || src[j] == '\'') {
						spec, next := readJSString(src, j)
						add(spec)
						i = next
					} else {
						i = j
					}
					continue
				}
				if j < len(src) && (src[j] == '"' || src[j] == '\'') { // side-effect import
					spec, next := readJSString(src, j)
					add(spec)
					i = next
					continue
				}
				i = scanJSFromClause(src, j, add)
			case "export":
				i = scanJSFromClause(src, i, add)
			case "require":
				j := skipJSSpace(src, i)
				if j < len(src) && src[j] == '(' {
					j = skipJSSpace(src, j+1)
					if j < len(src) && (src[j] == '"' || src[j] == '\'') {
						spec, next := readJSString(src, j)
						add(spec)
						i = next
					} else {
						i = j
					}
				}
			}
		default:
			i++
		}
	}
	return out
}

// scanJSFromClause scans an import/export clause for `from "<specifier>"` and
// returns the position after the clause (or after `;` when no from is found).
func scanJSFromClause(src string, i int, add func(string)) int {
	for i < len(src) {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			i = skipJSLineComment(src, i)
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i = skipJSBlockComment(src, i)
		case c == '"' || c == '\'':
			_, i = readJSString(src, i)
		case c == '`':
			i = skipJSTemplate(src, i)
		case c == ';':
			return i + 1
		case isJSIdentStart(c):
			start := i
			for i < len(src) && isJSIdentPart(src[i]) {
				i++
			}
			if src[start:i] != "from" {
				continue
			}
			j := skipJSSpace(src, i)
			if j < len(src) && (src[j] == '"' || src[j] == '\'') {
				spec, next := readJSString(src, j)
				add(spec)
				return next
			}
			return i
		default:
			i++
		}
	}
	return i
}

// normalizeNodeRoot resolves a --node-modules argument to the directory that
// contains node_modules. It accepts either the module root or the
// node_modules directory itself.
func normalizeNodeRoot(dir string) string {
	if dir == "" {
		return ""
	}
	clean := filepath.Clean(dir)
	if filepath.Base(clean) == "node_modules" {
		return filepath.Dir(clean)
	}
	return clean
}

// validateNodeRoot checks that a configured module root is a real directory.
// An invalid root is a load-time configuration error, not a silent no-op.
func validateNodeRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "stat", Path: root, Err: os.ErrInvalid}
	}
	return nil
}

// missingNodeModules returns the subset of specifiers that do not resolve to
// a package under root/node_modules. An empty root means "no modules at all".
func missingNodeModules(root string, mods []string) []string {
	if len(mods) == 0 {
		return nil
	}
	if root == "" {
		return mods
	}
	missing := make([]string, 0, len(mods))
	for _, spec := range mods {
		if !nodeModuleAvailable(root, spec) {
			missing = append(missing, spec)
		}
	}
	return missing
}

// nodeModuleAvailable reports whether the package owning spec exists under
// root/node_modules (subpaths resolve against their package root:
// `lodash/fp` → `lodash`, `@scope/pkg/sub` → `@scope/pkg`).
func nodeModuleAvailable(root, spec string) bool {
	pkg := jsPackageRoot(spec)
	if pkg == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(root, "node_modules", filepath.FromSlash(pkg)))
	return err == nil && info.IsDir()
}

func jsPackageRoot(spec string) string {
	parts := strings.Split(spec, "/")
	if strings.HasPrefix(spec, "@") {
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1]
		}
		return ""
	}
	return parts[0]
}

func isJSBuiltin(spec string) bool {
	return strings.HasPrefix(spec, "node:") || jsBuiltinModules[spec]
}

func isLocalSpecifier(spec string) bool {
	return spec == "." || spec == ".." ||
		strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/")
}

func isJSIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isJSIdentPart(c byte) bool {
	return isJSIdentStart(c) || (c >= '0' && c <= '9')
}

func skipJSSpace(src string, i int) int {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
		i++
	}
	return i
}

func skipJSLineComment(src string, i int) int {
	for i < len(src) && src[i] != '\n' {
		i++
	}
	return i
}

func skipJSBlockComment(src string, i int) int {
	i += 2
	for i+1 < len(src) {
		if src[i] == '*' && src[i+1] == '/' {
			return i + 2
		}
		i++
	}
	return len(src)
}

// readJSString reads a single- or double-quoted string starting at i and
// returns its (unescaped) value plus the position after the closing quote.
func readJSString(src string, i int) (string, int) {
	quote := src[i]
	i++
	var b strings.Builder
	for i < len(src) {
		c := src[i]
		if c == '\\' && i+1 < len(src) {
			b.WriteByte(src[i+1])
			i += 2
			continue
		}
		if c == quote {
			return b.String(), i + 1
		}
		if c == '\n' {
			return b.String(), i // unterminated: stop at the newline
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), i
}

// skipJSTemplate skips a template literal, including nested `${...}` scope
// (so strings/templates inside the substitution are skipped correctly).
func skipJSTemplate(src string, i int) int {
	i++ // opening backtick
	depth := 0
	for i < len(src) {
		switch src[i] {
		case '\\':
			i += 2
		case '`':
			if depth == 0 {
				return i + 1
			}
			i = skipJSTemplate(src, i)
		case '"', '\'':
			if depth > 0 {
				var next int
				_, next = readJSString(src, i)
				i = next
			} else {
				i++
			}
		case '$':
			if i+1 < len(src) && src[i+1] == '{' {
				depth++
				i += 2
			} else {
				i++
			}
		case '}':
			if depth > 0 {
				depth--
			}
			i++
		default:
			i++
		}
	}
	return len(src)
}
