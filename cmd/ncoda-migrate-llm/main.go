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

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

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
	start    int
	end      int
}

func run(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	var cfg options
	flags := flag.NewFlagSet("ncoda-migrate-llm", flag.ContinueOnError)
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
		fmt.Fprintf(stderr, "ncoda-migrate-llm: invalid --diagnostics value %q (want text or json)\n", cfg.diagnostic)
		return 2
	}
	if cfg.stdout && cfg.inPlace {
		fmt.Fprintln(stderr, "ncoda-migrate-llm: --stdout and --in-place are mutually exclusive")
		return 2
	}
	if len(flags.Args()) == 0 {
		fmt.Fprintln(stderr, "usage: ncoda-migrate-llm [--dry-run] [--check] [--diagnostics text|json] [--stdout | --in-place] <file-or-directory>...")
		return 2
	}

	paths, err := discoverFiles(flags.Args())
	if err != nil {
		fmt.Fprintf(stderr, "ncoda-migrate-llm: %v\n", err)
		return 2
	}
	if cfg.stdout && len(paths) != 1 {
		fmt.Fprintln(stderr, "ncoda-migrate-llm: --stdout requires exactly one input file")
		return 2
	}

	results := make([]fileResult, 0, len(paths))
	allDiagnostics := []pathDiagnostic{}
	ioFailed := false
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			fmt.Fprintf(stderr, "ncoda-migrate-llm: %s: %v\n", path, readErr)
			ioFailed = true
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			fmt.Fprintf(stderr, "ncoda-migrate-llm: %s: %v\n", path, statErr)
			ioFailed = true
			continue
		}
		result := migrate.Rewrite(string(data), path)
		results = append(results, fileResult{path: path, source: string(data), info: info, result: result})
		for _, diagnostic := range result.Diagnostics {
			allDiagnostics = append(allDiagnostics, pathDiagnostic{
				Path: path, Line: diagnostic.Line, Column: diagnostic.Column,
				Code: diagnostic.Code, Severity: diagnostic.Severity, Message: diagnostic.Message,
				start: diagnostic.Start, end: diagnostic.End,
			})
		}
	}
	if ioFailed {
		return 2
	}

	sort.SliceStable(allDiagnostics, func(i, j int) bool {
		if allDiagnostics[i].Path != allDiagnostics[j].Path {
			return allDiagnostics[i].Path < allDiagnostics[j].Path
		}
		if allDiagnostics[i].start != allDiagnostics[j].start {
			return allDiagnostics[i].start < allDiagnostics[j].start
		}
		if allDiagnostics[i].end != allDiagnostics[j].end {
			return allDiagnostics[i].end < allDiagnostics[j].end
		}
		return allDiagnostics[i].Code < allDiagnostics[j].Code
	})
	emitDiagnostics(allDiagnostics, cfg.diagnostic, stdout, stderr)

	rewriteNeeded := false
	for _, item := range results {
		if len(item.result.Diagnostics) > 0 {
			continue
		}
		rewriteNeeded = rewriteNeeded || item.result.Changed
		if cfg.stdout {
			if _, err := io.WriteString(stdout, item.result.Source); err != nil {
				fmt.Fprintf(stderr, "ncoda-migrate-llm: stdout: %v\n", err)
				return 2
			}
			continue
		}
		if cfg.inPlace && !cfg.dryRun && !cfg.check && item.result.Changed {
			if err := atomicRewrite(item.path, []byte(item.result.Source), item.info.Mode()); err != nil {
				fmt.Fprintf(stderr, "ncoda-migrate-llm: %s: %v\n", item.path, err)
				return 2
			}
		}
	}
	if len(allDiagnostics) > 0 || (cfg.check && rewriteNeeded) {
		return 1
	}
	return 0
}

func discoverFiles(inputs []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, input := range inputs {
		info, err := os.Stat(input)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if filepath.Ext(input) != ".ncoda" {
				return nil, fmt.Errorf("%s: input file must have .ncoda extension", input)
			}
			clean := filepath.Clean(input)
			if !seen[clean] {
				seen[clean] = true
				files = append(files, clean)
			}
			continue
		}
		err = filepath.WalkDir(input, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".ncoda" {
				return nil
			}
			clean := filepath.Clean(path)
			if !seen[clean] {
				seen[clean] = true
				files = append(files, clean)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func emitDiagnostics(diagnostics []pathDiagnostic, format string, stdout, stderr io.Writer) {
	for _, diagnostic := range diagnostics {
		if format == "json" {
			data, _ := json.Marshal(diagnostic)
			fmt.Fprintln(stdout, string(data))
			continue
		}
		fmt.Fprintf(stderr, "%s:%d:%d: %s: %s\n",
			diagnostic.Path, diagnostic.Line, diagnostic.Column,
			diagnostic.Code, diagnostic.Message)
	}
}

func atomicRewrite(path string, data []byte, mode os.FileMode) (returnErr error) {
	directory, base := filepath.Dir(path), filepath.Base(path)
	temp, err := os.CreateTemp(directory, "."+base+".llm-migrate-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			if closeErr := temp.Close(); returnErr == nil && closeErr != nil {
				returnErr = closeErr
			}
		}
		if !renamed {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	err = temp.Close()
	closed = true
	if err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	renamed = true
	return nil
}
