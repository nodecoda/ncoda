// Command nclang-compile (DIFY-ONLY build) is the JSON contract CLI for the
// NodeCoda→Dify compiler: reads source from stdin, writes one versioned
// JSON result to stdout. Exit code 0 means a result was produced (including
// COMPILE_ERROR); non-zero means the CLI itself failed.
//
// This variant is produced by `go build -tags dify` for the open/free local
// distribution (the nclang binary consumed by @nodecoda/skill): it excludes
// Coze / Coze-biz / runtime code entirely. The implementation lives in
// pkg/cli.CompileRun (open side); the complete multi-target variant lives in
// main.go (//go:build !dify).
package main

import (
	"io"
	"os"

	"github.com/nodecoda/ncoda/pkg/cli"
)

func runCLI(stdin io.Reader, stdout io.Writer, args []string) int {
	return cli.CompileRun(stdin, stdout, args)
}

func main() {
	os.Exit(runCLI(os.Stdin, os.Stdout, os.Args[1:]))
}
