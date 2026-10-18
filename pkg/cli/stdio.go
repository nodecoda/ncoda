package cli

// stdio.go — 全局 stdout/stderr writer 间接层（开放面）。默认指向
// os.Stdout / os.Stderr，统一 CLI 入口通过 SetStdio 重定向（避免把
// io.Writer 传遍每个 helper）。

import (
	"fmt"
	"io"
	"os"
)

// Stdout / Stderr are the process-wide writers used by the CLI helpers.
var (
	Stdout io.Writer = os.Stdout
	Stderr io.Writer = os.Stderr
)

// SetStdio 重定向全局 stdout/stderr（nil = 保留当前值）。
func SetStdio(stdout, stderr io.Writer) {
	if stdout != nil {
		Stdout = stdout
	}
	if stderr != nil {
		Stderr = stderr
	}
}

// Out writes to the global stdout indirection.
func Out(format string, args ...any) { fmt.Fprintf(Stdout, format, args...) }

// Outln writes a line to the global stdout indirection.
func Outln(args ...any) { fmt.Fprintln(Stdout, args...) }
