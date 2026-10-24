// Command nclang-compile is the language-lane CLI of the open ncoda
// distribution: stdin source → ncoda-ir/v1 on stdout.
//
// It links no platform backend on purpose (ASSET-BOUNDARY): the targets
// (dify / coze / coze-biz) are commercial adapters. `--target` therefore
// accepts only `__none__`; anything else is a usage error (rc=2).
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
