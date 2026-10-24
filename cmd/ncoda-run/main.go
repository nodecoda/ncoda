// Command ncoda-run is the runtime lane of the open ncoda distribution:
// .ncoda source (or a file path) in, execution result JSON out — no platform
// backend, no network, no database.
package main

import (
	"io"
	"os"

	"github.com/nodecoda/ncoda/pkg/runner"
)

func runCLI(stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	return runner.Run(stdin, stdout, stderr, args)
}

func main() {
	os.Exit(runCLI(os.Stdin, os.Stdout, os.Stderr, os.Args[1:]))
}
