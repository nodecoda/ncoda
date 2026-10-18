// Command ncoda-migrate-switch rewrites intent→switch and detects classify
// value calls in ncoda source files. Dry-run, in-place, stdout, and check
// modes mirror ncoda-migrate-llm.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/nodecoda/ncoda/pkg/migrate"
)

type options struct {
	dryRun     bool
	check      bool
	diagnostic string
	stdout     bool
	inPlace    bool
}

type fileResult struct {
	path   string
	source string
	info   os.FileInfo
	result migrate.Result
}

type pathDiagnostic struct {
	Path     string           `json:"path"`
	Line     int              `json:"line"`
	Column   int              `json:"column"`
	Code     migrate.Code     `json:"code"`
	Severity migrate.Severity `json:"severity"`
	Message  string           `json:"message"`
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var cfg options
	flags := flag.NewFlagSet("ncoda-migrate-switch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&cfg.dryRun, "dry-run", false, "suppress source writes")
	flags.BoolVar(&cfg.check, "check", false, "exit 1 when a rewrite is needed")
	flags.StringVar(&cfg.diagnostic, "diagnostics", "text", "diagnostic format: text or json")
	flags.BoolVar(&cfg.stdout, "stdout", false, "write one migrated file to stdout")
	flags.BoolVar(&cfg.inPlace, "in-place", false, "rewrite files in place")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if cfg.diagnostic != "text" && cfg.diagnostic != "json" {
		fmt.Fprintf(stderr, "ncoda-migrate-switch: invalid --diagnostics value %q\n", cfg.diagnostic)
		return 2
	}
	if cfg.stdout && cfg.inPlace {
		fmt.Fprintln(stderr, "ncoda-migrate-switch: --stdout and --in-place are mutually exclusive")
		return 2
	}
	if len(flags.Args()) == 0 {
		fmt.Fprintln(stderr, "usage: ncoda-migrate-switch [--dry-run] [--check] [--diagnostics text|json] [--stdout | --in-place] <file>...")
		return 2
	}

	paths, err := discoverFiles(flags.Args())
	if err != nil {
		fmt.Fprintf(stderr, "ncoda-migrate-switch: %v\n", err)
		return 2
	}
	if cfg.stdout && len(paths) != 1 {
		fmt.Fprintln(stderr, "ncoda-migrate-switch: --stdout requires exactly one input file")
		return 2
	}

	results := make([]fileResult, 0, len(paths))
	allDiagnostics := []pathDiagnostic{}
	ioFailed := false
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			fmt.Fprintf(stderr, "ncoda-migrate-switch: %s: %v\n", path, readErr)
			ioFailed = true
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			fmt.Fprintf(stderr, "ncoda-migrate-switch: %s: %v\n", path, statErr)
			ioFailed = true
			continue
		}
		result := migrate.RewriteIntentClassify(string(data), path)
		results = append(results, fileResult{path: path, source: string(data), info: info, result: result})
		for _, diagnostic := range result.Diagnostics {
			allDiagnostics = append(allDiagnostics, pathDiagnostic{
				Path: path, Line: diagnostic.Line, Column: diagnostic.Column,
				Code: diagnostic.Code, Severity: diagnostic.Severity, Message: diagnostic.Message,
			})
		}
	}
	if ioFailed {
		return 2
	}

	sort.SliceStable(allDiagnostics, func(i, j int) bool {
		pi, pj := allDiagnostics[i], allDiagnostics[j]
		if pi.Path != pj.Path {
			return pi.Path < pj.Path
		}
		return pi.Line < pj.Line || (pi.Line == pj.Line && pi.Column < pj.Column)
	})

	if cfg.diagnostic == "json" {
		b, _ := json.MarshalIndent(allDiagnostics, "", "  ")
		_, _ = stdout.Write(b)
		_, _ = stdout.Write([]byte{'\n'})
	} else {
		for _, d := range allDiagnostics {
			_, _ = fmt.Fprintf(stdout, "%s:%d:%d: %s: %s: %s\n", d.Path, d.Line, d.Column, d.Severity, d.Code, d.Message)
		}
	}

	needRewrite := 0
	for _, fr := range results {
		if !fr.result.Changed {
			continue
		}
		needRewrite++
		if cfg.dryRun || cfg.check {
			continue
		}
		if cfg.inPlace {
			if err := os.WriteFile(fr.path, []byte(fr.result.Source), fr.info.Mode().Perm()); err != nil {
				fmt.Fprintf(stderr, "ncoda-migrate-switch: %s: write: %v\n", fr.path, err)
				return 2
			}
		}
	}
	if cfg.stdout && needRewrite > 0 {
		_, _ = stdout.Write([]byte(results[0].result.Source))
		_, _ = stdout.Write([]byte{'\n'})
	}
	if cfg.check && needRewrite > 0 {
		fmt.Fprintf(stderr, "ncoda-migrate-switch: %d file(s) need rewrite; use --in-place or --stdout\n", needRewrite)
		return 1
	}
	return 0
}

func discoverFiles(args []string) ([]string, error) {
	var paths []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			entries, err := os.ReadDir(arg)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if !e.IsDir() && filepath.Ext(e.Name()) == ".ncoda" {
					paths = append(paths, filepath.Join(arg, e.Name()))
				}
			}
		} else {
			paths = append(paths, arg)
		}
	}
	return paths, nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
