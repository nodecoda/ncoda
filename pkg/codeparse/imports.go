package codeparse

// imports.go — Python 模块策略检查（SEM-CODE-002）。
//
// 判据来源：coze 平台 code 节点的 Python 模块黑名单（coze-studio OSS
// `backend/domain/workflow/internal/nodes/code/code.go:110-137` 的同名表，
// 文档见 https://docs.coze.cn/open/docs/guides/code_node）。黑名单是
// **平台 policy**（F3），不是 artifact 值 —— 因此 trace origin=policy。
//
// 边界：只做黑名单硬判。第三方/非内置模块**不硬拒**：那需要平台侧的
// 模块白名单配置（coze-studio 从仓库配置读），本仓没有该配置，硬拒会把
// 合法语料打成红。未命中黑名单 = passed，不假装验证过 import 可用性。

import (
	"regexp"
	"strings"
)

// pythonBlacklist is the closed set of stdlib modules the platform removes
// for security reasons. Keep in sync with the OSS table; the OSS comment is
// the evidence anchor, not this file.
var pythonBlacklist = map[string]struct{}{
	"curses": {}, "dbm": {}, "ensurepip": {}, "fcntl": {}, "grp": {},
	"idlelib": {}, "lib2to3": {}, "msvcrt": {}, "pwd": {}, "resource": {},
	"syslog": {}, "termios": {}, "tkinter": {}, "turtle": {}, "turtledemo": {},
	"venv": {}, "winreg": {}, "winsound": {}, "multiprocessing": {},
	"threading": {}, "socket": {}, "pty": {}, "tty": {},
}

var (
	importLineRe     = regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_.,\s]+)`)
	fromImportLineRe = regexp.MustCompile(`^\s*from\s+([A-Za-z0-9_.]+)\s+import`)
)

// PythonImports extracts the top-level module names a snippet imports.
// Comment lines are skipped (mirrors the OSS scanner).
func PythonImports(code string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(root string) {
		if root == "" {
			return
		}
		if _, dup := seen[root]; dup {
			return
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	for _, line := range strings.Split(code, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := importLineRe.FindStringSubmatch(trimmed); m != nil {
			for _, item := range strings.Split(m[1], ",") {
				name := strings.TrimSpace(strings.Fields(item + " ")[0])
				add(strings.Split(name, ".")[0])
			}
			continue
		}
		if m := fromImportLineRe.FindStringSubmatch(trimmed); m != nil {
			add(strings.Split(m[1], ".")[0])
		}
	}
	return out
}

// BlacklistedPythonModules returns the imported modules that the platform
// removes. Empty = no policy violation (unknown/third-party modules are not
// judged here).
func BlacklistedPythonModules(code string) []string {
	var hits []string
	for _, mod := range PythonImports(code) {
		if _, bad := pythonBlacklist[mod]; bad {
			hits = append(hits, mod)
		}
	}
	return hits
}
