// Command ncoda-pin-bindings 把注册表 ABI 指纹钉进平台绑定表（P3.3 §16.2，
// 修复清单 P2：binding_unpinned 1274 警告 → 0）。
//
// 与编译端同一条加载链（LoadCapabilitySearchPaths → NewRegistry →
// computeInterfaceHash）为每个 (provider, operation) 计算 interface_hash，
// 逐行写入绑定 YAML。只增 interface_hash 键，其余结构用 yaml.Node 原样
// 保序（diff 最小）；注册表不存在的行保持未钉扎并报告——无法钉 = 不假装。
//
// 已钉但值与注册表不符的行会被改写到注册表真值并报告（陈旧 pin 修复；
// 该行若在编译期出现会是硬错误，钉扎工具负责把它校正）。
//
// Usage:
//
//	ncoda-pin-bindings [--tool-path DIR]... [--bindings FILE]... [--dry-run]
//
// 默认 --tool-path examples/coze-reference（规范根，自动下钻
// capabilities-ncoda/）；默认 --bindings 为仓库内
// examples/coze-reference/bindings/<platform>/<platform>-bindings.yaml
// （coze-biz + coze-studio；平台目录存在才处理）。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/tool"
)

func main() {
	var toolPaths multiFlag
	var bindings multiFlag
	dryRun := flag.Bool("dry-run", false, "report what would change without writing")
	flag.Var(&toolPaths, "tool-path", "capability search path (repeatable; default examples/coze-reference)")
	flag.Var(&bindings, "bindings", "binding YAML to pin (repeatable; default repo bindings)")
	flag.Parse()

	paths := []string(toolPaths)
	if len(paths) == 0 {
		paths = []string{"examples/coze-reference"}
	}
	files := []string(bindings)
	if len(files) == 0 {
		for _, p := range []string{"coze-biz/coze-biz-bindings.yaml", "coze-studio/coze-studio-bindings.yaml"} {
			cand := filepath.Join("examples", "coze-reference", "bindings", p)
			if _, err := os.Stat(cand); err == nil {
				files = append(files, cand)
			}
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "ncoda-pin-bindings: no binding files to pin")
		os.Exit(2)
	}

	registry, err := tool.LoadCapabilitySearchPaths(paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ncoda-pin-bindings: loading registry:", err)
		os.Exit(1)
	}
	if registry == nil {
		fmt.Fprintln(os.Stderr, "ncoda-pin-bindings: no capability headers found in", paths)
		os.Exit(1)
	}

	anyWrite := false
	for _, file := range files {
		pinned, unpinnedKeys, changed, err := pinFile(file, registry, *dryRun)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ncoda-pin-bindings:", err)
			os.Exit(1)
		}
		state := "unchanged"
		if changed {
			state = "updated"
			anyWrite = true
		}
		fmt.Printf("%s: %s (pinned %d, unpinned %d)\n", file, state, pinned, len(unpinnedKeys))
		for _, key := range unpinnedKeys {
			fmt.Printf("  unpinned(no registry def): %s\n", key)
		}
	}
	if *dryRun {
		fmt.Println("dry-run: no files written")
		return
	}
	if anyWrite {
		fmt.Println("written")
	}
}

// multiFlag collects repeated -flag values.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// pinFile pins every binding row of one YAML file against the registry using
// text-level insertion: rows are delimited by `- provider: ` lines, and the
// hash line is inserted after the row's last field. The rest of the file
// (quoting, indentation, field order, comments) survives byte-for-byte, so
// the diff is exactly one interface_hash line per pinned row. Rows whose
// (provider, operation) is absent from the registry stay unpinned and are
// reported (cannot pin = do not fake). A row already carrying the correct
// hash is left untouched (idempotent); a stale pin is rewritten to the
// registry truth.
func pinFile(path string, registry *tool.Registry, dryRun bool) (pinned int, unpinned []string, changed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	type row struct{ start, end int } // line range [start,end) of one binding row
	var rows []row
	var cur *row
	var prov, op string
	// Collect rows; track (provider, operation) per row index.
	type keyed struct {
		r        row
		prov, op string
		hashIdx  int // line index of existing interface_hash, -1 if none
	}
	var keyedRows []keyed
	for i, ln := range lines {
		if strings.HasPrefix(ln, "- provider: ") {
			if cur != nil {
				cur.end = i
				rows = append(rows, *cur)
			}
			cur = &row{start: i}
			prov = strings.TrimSpace(strings.TrimPrefix(ln, "- provider: "))
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(ln, "  operation: ") {
			op = strings.TrimSpace(strings.TrimPrefix(ln, "  operation: "))
		}
		if strings.HasPrefix(ln, "  interface_hash: ") {
			keyedRows = append(keyedRows, keyed{r: row{cur.start, i}, prov: prov, op: op, hashIdx: i})
			cur = nil
			continue
		}
	}
	if cur != nil {
		cur.end = len(lines)
		rows = append(rows, *cur)
	}
	// Build provider/op per completed row (rows that already had a hash were
	// consumed above; recompute for the rest).
	rowMeta := make([]keyed, len(rows))
	for idx, r := range rows {
		pv, opv := "", ""
		for i := r.start; i < r.end; i++ {
			ln := lines[i]
			if strings.HasPrefix(ln, "  operation: ") {
				opv = strings.TrimSpace(strings.TrimPrefix(ln, "  operation: "))
			}
		}
		// provider is on the row start line
		ln := lines[r.start]
		pv = strings.TrimSpace(strings.TrimPrefix(ln, "- provider: "))
		rowMeta[idx] = keyed{r: r, prov: pv, op: opv, hashIdx: -1}
	}
	rowMeta = append(rowMeta, keyedRows...)
	sort.Slice(rowMeta, func(i, j int) bool { return rowMeta[i].r.start < rowMeta[j].r.start })

	var missing []string
	insertAt := make(map[int]string) // line index -> hash line to insert after
	for _, k := range rowMeta {
		if k.prov == "" || k.op == "" {
			continue
		}
		def, ok := registry.Lookup(k.prov, k.op)
		if !ok {
			missing = append(missing, tool.SymbolKey(k.prov, k.op))
			continue
		}
		if def.InterfaceHash == "" {
			missing = append(missing, tool.SymbolKey(k.prov, k.op)+" (registry hash empty)")
			continue
		}
		if k.hashIdx >= 0 {
			cur := strings.TrimPrefix(lines[k.hashIdx], "  interface_hash: ")
			if cur != def.InterfaceHash {
				// 陈旧 pin：改写为注册表真值。
				lines[k.hashIdx] = "  interface_hash: " + def.InterfaceHash
				changed = true
				fmt.Printf("  %s: stale pin corrected\n", tool.SymbolKey(k.prov, k.op))
			}
			pinned++
			continue
		}
		insertAt[k.r.end-1] = "  interface_hash: " + def.InterfaceHash
		pinned++
	}
	if len(insertAt) > 0 {
		out := make([]string, 0, len(lines)+len(insertAt))
		for i, ln := range lines {
			out = append(out, ln)
			if hash, ok := insertAt[i]; ok {
				out = append(out, hash)
			}
		}
		lines = out
		changed = true
	}
	sort.Strings(missing)
	if changed && !dryRun {
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return 0, nil, false, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return pinned, missing, changed, nil
}

func truncate(s string) string {
	if len(s) > 24 {
		return s[:24] + "…"
	}
	return s
}
