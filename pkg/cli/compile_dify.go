package cli

// compile_dify.go — 开放发行版的 JSON 契约 CLI（dify-only）：读 stdin 源码，
// 写一份 versioned JSON 结果到 stdout。退出码 0 = 产出了结果（含
// COMPILE_ERROR）；非零 = CLI 自身失败。
//
// 这是 `go build -tags dify ./cmd/nclang-compile` 的实现体，与闭源全量
// CLI（internal/cli.CompileRun，支持 coze / coze-biz 等 target）共享同一
// 份 JSON 线格式（pkg/compiler）。

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/compiler"
	"github.com/nodecoda/ncoda/pkg/irdump"
	"github.com/nodecoda/ncoda/pkg/target/codec"
	"github.com/nodecoda/ncoda/pkg/toolpath"
	"github.com/nodecoda/ncoda/pkg/version"
)

// CompileRun 解析参数、从 stdin 读源码，把 JSON 契约写到 stdout。
// 参数形状与闭源全量 CLI 的 dify 子集一致（coze-* flag 不存在 ⇒ 传了就是
// 用法错，退出码 2）。
func CompileRun(stdin io.Reader, stdout io.Writer, args []string) int {
	fs := flag.NewFlagSet("nclang-compile", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "Output JSON result (accepted; output is always JSON, mirroring compile_cli.py main)")
	target := fs.String("target", codec.TargetName, "Target profile ID")
	filename := fs.String("filename", "<input>", "Source filename label")
	var toolPaths []string
	fs.Func("tool-path", "Search path for capability packages (repeatable; resolved by source import declarations)", func(value string) error {
		if value != "" {
			toolPaths = append(toolPaths, value)
		}
		return nil
	})
	difyBindingsPath := fs.String("dify-bindings", "", "Path to Dify Tool binding YAML")
	placementReport := fs.Bool("placement-report", false, "Emit native-vs-materialize lowering decision log (placement)")
	strictFlag := fs.Bool("strict", false, "Strict mode: keep warning-severity diagnostics (2xxx) in the result (audit mode; default suppresses them)")
	versionFlag := fs.Bool("version", false, "Print the compiler version and exit")
	emitIR := fs.Bool("emit-ir", false, "Emit the canonical ncoda-ir/v1 dump (language lane; requires --target __none__)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if *versionFlag {
		// Two dimensions on purpose: product version (release) and contract
		// version (frozen wire schema). `dev` = build not stamped at link time.
		fmt.Fprintf(stdout, "nclang-compile %s (contract %s", version.Version, compiler.CompilerVersion())
		if version.Commit != "" {
			fmt.Fprintf(stdout, ", commit %s", version.Commit)
		}
		fmt.Fprintln(stdout, ")")
		return 0
	}
	// Python's main never reads args.json — JSON output is unconditional
	// (compile_cli.py:156-175); the flag exists for CLI-shape parity only.
	_ = *jsonFlag
	// 语言 lane（L1）：`--target __none__` 只回答「这门程序在语言里有没有
	// 意义」，产物只有 ncoda-ir/v1。它不做平台承载判定，因此必须与
	// --emit-ir 成对出现（docs/language/CLI-CONTRACT.md）。
	if *target == compiler.TargetLanguageOnly && !*emitIR {
		fmt.Fprintf(os.Stderr, "nclang-compile: --target %s is the language lane and requires --emit-ir\n", compiler.TargetLanguageOnly)
		return 2
	}
	if *emitIR && *target != compiler.TargetLanguageOnly {
		fmt.Fprintf(os.Stderr, "nclang-compile: --emit-ir requires --target %s\n", compiler.TargetLanguageOnly)
		return 2
	}
	source, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nclang-compile: reading stdin:", err)
		return 1
	}
	opts := compiler.Options{Target: *target, Filename: *filename, ShowPlacements: *placementReport, Strict: *strictFlag, EmitIR: *emitIR}
	opts.ToolPaths = ResolveToolPaths(toolPaths)
	if *difyBindingsPath != "" {
		if *target != codec.TargetName {
			fmt.Fprintf(os.Stderr, "nclang-compile: --dify-bindings requires --target %s\n", codec.TargetName)
			return 2
		}
		data, err := os.ReadFile(*difyBindingsPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nclang-compile: reading --dify-bindings:", err)
			return 2
		}
		opts.DifyToolBindings, err = dify.LoadToolBindingManifestYAML(data)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nclang-compile: loading --dify-bindings:", err)
			return 2
		}
	}
	result := compiler.CompileToJSONWithOptions(string(source), opts)
	if *emitIR {
		irValue, ok := result["ir"].(*irdump.IRDump)
		if !ok || irValue == nil {
			status, _ := result["status"].(string)
			fmt.Fprintf(os.Stderr, "nclang-compile: --emit-ir: no IR produced (status %s)\n", status)
			if diags, ok := result["diagnostics"].([]any); ok {
				for _, d := range diags {
					if m, ok := d.(map[string]any); ok {
						fmt.Fprintf(os.Stderr, "  %v: %v\n", m["code"], m["message"])
					}
				}
			}
			return 1
		}
		data, merr := irValue.Marshal()
		if merr != nil {
			fmt.Fprintln(os.Stderr, "nclang-compile: encoding IR:", merr)
			return 2
		}
		if _, werr := stdout.Write(data); werr != nil {
			fmt.Fprintln(os.Stderr, "nclang-compile: writing stdout:", werr)
			return 2
		}
		return 0
	}
	data, err := compiler.MarshalContractJSON(result)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nclang-compile: encoding result:", err)
		return 1
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(os.Stderr, "nclang-compile: writing stdout:", err)
		return 1
	}
	return 0
}

// ResolveToolPaths assembles the tool search path list from CLI entries,
// NCLANG_TOOL_PATH and ~/.nodecoda/tools (the open, runtime-free rule).
func ResolveToolPaths(flagPaths []string) []string {
	return toolpath.Resolve(flagPaths, os.Getenv, os.UserHomeDir)
}
