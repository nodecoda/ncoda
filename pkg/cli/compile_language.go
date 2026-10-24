package cli

// compile_language.go — 开放发行版的 CLI 实现：**语言 lane**。
//
// 读 stdin 源码，跑语言前端 + IR 构建，把 `ncoda-ir/v1` 写到 stdout。
// 退出码（与 docs/language/CLI-CONTRACT.md 一致）：
//
//	0 = 产出合法 IR
//	1 = 语言判红（语法/类型/P6/IR 构建失败）；stdout 没有 IR，诊断在 stderr
//	2 = CLI 用法错（参数/IO/未知平台 target）
//
// 这里**没有平台后端**：`--target` 只接受 `__none__`。平台 target 属于
// 商业仓的编译器（ASSET-BOUNDARY：适配器只在商业仓）；传了就是用法错，
// 不做静默降级、不假装渲染。
//
// 2026-10-25（ASSET-BOUNDARY P2c）：本文件原先叫 compile_dify.go，是 Dify
// 编译器的入口。Dify 管线已搬入商业仓，开放发行版的唯一产物是语言 IR。

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nodecoda/ncoda/pkg/compiler"
	"github.com/nodecoda/ncoda/pkg/toolpath"
	"github.com/nodecoda/ncoda/pkg/version"
)

// CompileRun parses args, compiles stdin, and writes the ncoda-ir/v1 dump.
func CompileRun(stdin io.Reader, stdout io.Writer, args []string) int {
	fs := flag.NewFlagSet("nclang-compile", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "Accepted for CLI-shape parity; the output is always JSON")
	target := fs.String("target", compiler.TargetLanguageOnly, "Target profile ID (the open distribution supports only "+compiler.TargetLanguageOnly+")")
	filename := fs.String("filename", "<input>", "Source filename label")
	var toolPaths []string
	fs.Func("tool-path", "Search path for capability packages (repeatable; resolved by source import declarations)", func(value string) error {
		if value != "" {
			toolPaths = append(toolPaths, value)
		}
		return nil
	})
	emitIR := fs.Bool("emit-ir", true, "Emit the canonical ncoda-ir/v1 dump (the language lane's product)")
	strictFlag := fs.Bool("strict", false, "Strict mode: keep warning-severity diagnostics (default suppresses them)")
	versionFlag := fs.Bool("version", false, "Print the compiler version and exit")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if *versionFlag {
		fmt.Fprintf(stdout, "nclang-compile %s (contract %s", version.Version, compiler.CompilerVersion())
		if version.Commit != "" {
			fmt.Fprintf(stdout, ", commit %s", version.Commit)
		}
		fmt.Fprintln(stdout, ")")
		return 0
	}
	_ = *jsonFlag
	if *target != compiler.TargetLanguageOnly {
		fmt.Fprintf(os.Stderr, "nclang-compile: target %q is not available: the open distribution ships the language lane only (--target %s). Platform adapters live in the commercial compiler.\n", *target, compiler.TargetLanguageOnly)
		return 2
	}

	source, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nclang-compile: reading stdin:", err)
		return 2
	}
	opts := compiler.Options{
		Target:   *target,
		Filename: *filename,
		Strict:   *strictFlag,
		EmitIR:   *emitIR,
	}
	opts.ToolPaths = ResolveToolPaths(toolPaths)

	result := compiler.Compile(string(source), opts)
	if !result.OK {
		fmt.Fprintf(os.Stderr, "nclang-compile: %s\n", result.Status)
		for _, d := range result.Diagnostics {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", d.Code, d.Message)
		}
		return 1
	}
	if *emitIR {
		if result.IR == nil {
			fmt.Fprintln(os.Stderr, "nclang-compile: --emit-ir: no IR produced")
			return 1
		}
		data, merr := result.IR.Marshal()
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
	data, merr := compiler.MarshalContractJSON(resultToWire(result))
	if merr != nil {
		fmt.Fprintln(os.Stderr, "nclang-compile: encoding result:", merr)
		return 2
	}
	if _, werr := stdout.Write(data); werr != nil {
		fmt.Fprintln(os.Stderr, "nclang-compile: writing stdout:", werr)
		return 2
	}
	return 0
}

// resultToWire is the minimal contract shape for `--emit-ir=false`: status,
// ok, diagnostics. The IR is the language lane's real product; this exists so
// a caller can still read why a program was rejected without parsing stderr.
func resultToWire(r *compiler.Result) map[string]any {
	diags := make([]any, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		m := map[string]any{
			"code":     d.Code,
			"severity": d.Severity,
			"message":  d.Message,
		}
		if d.Location != "" {
			m["location"] = d.Location
		}
		diags = append(diags, m)
	}
	return map[string]any{
		"target":           r.Target,
		"compiler_version": r.CompilerVersion,
		"status":           r.Status,
		"ok":               r.OK,
		"diagnostics":      diags,
	}
}

// ResolveToolPaths assembles the tool search path list from CLI entries,
// NCLANG_TOOL_PATH and ~/.nodecoda/tools (the open, runtime-free rule).
func ResolveToolPaths(flagPaths []string) []string {
	return toolpath.Resolve(flagPaths, os.Getenv, os.UserHomeDir)
}
