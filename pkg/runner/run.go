package runner

// run.go — 开放发行版的**运行时 lane**：一条 .ncoda 源码从语言前端
// 一路跑到执行结果，不依赖任何平台后端。
//
// 链路：passes（语言前端）→ ir.Build → flow.Project → spec.Build →
// runtime/compile → Invoke。
//
// 退出码（与 compile_language.go 同一张契约表，增加"运行期失败"）：
//
//	0 = 跑通，结果 JSON 在 stdout
//	1 = 语言判红或运行期失败（诊断/错误在 stderr，stdout 无结果）
//	2 = CLI 用法错（参数、IO、未知 target）
//
// 默认模型后端是 echo（离线确定性）。真模型属于适配器/宿主能力，不在语言内核里。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/localtools"
	"github.com/nodecoda/ncoda/pkg/passes"
	rtcompile "github.com/nodecoda/ncoda/pkg/runtime/compile"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/tool"
	"github.com/nodecoda/ncoda/pkg/toolbridge"
	"github.com/nodecoda/ncoda/pkg/toolpath"
	"github.com/nodecoda/ncoda/pkg/version"
)

// runtimeProfile is the execution lane's target profile: the runtime runs
// language nodes directly, so it does not enforce a platform's lowerability
// shapes (NodeGraphShapes=false) and declares the axes it really carries.
// Mirrors the commercial runtime lane; the language frontend keeps its own
// stricter profile.
func runtimeProfile() *passes.TargetProfile {
	base := passes.DefaultTargetProfile()
	return &passes.TargetProfile{
		Name:                                 "runtime",
		Operations:                           base.Operations,
		MaxParallelism:                       base.MaxParallelism,
		Capabilities:                         passes.DefaultCapabilityManifest(),
		AgentProfiles:                        base.AgentProfiles,
		ParameterExtractorFunctionCallModels: base.ParameterExtractorFunctionCallModels,
		ParameterExtractorMemoryMaxTurns:     base.ParameterExtractorMemoryMaxTurns,
		Codec:                                base.Codec,
		Carrier:                              passes.CarrierProfile{Declared: true, RetryInterval: passes.RetryIntervalCarried, ParallelOnError: true},
	}
}

// echoModel 是离线确定性的模型后端：llm 返回固定文本，agent 不发工具调用。
type echoModel struct{}

func (echoModel) Chat(_ context.Context, model string, _ []nodes.Message) (string, error) {
	return "echo:" + model, nil
}

func (echoModel) ChatWithTools(_ context.Context, model string, _ []nodes.Message, _ []nodes.ToolSpec) (nodes.ChatResult, error) {
	return nodes.ChatResult{Content: "echo:" + model}, nil
}

// Run parses args, runs one source end to end on the language runtime, and
// writes the result JSON to stdout.
func Run(stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("ncoda-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	inputJSON := fs.String("input", "", "Entry input as a JSON object (@file reads it from a file)")
	pretty := fs.Bool("pretty", false, "Pretty-print the result JSON")
	raw := fs.Bool("raw", false, `Print the raw value when the result is exactly one string "output" key`)
	emitSpec := fs.String("emit-spec", "", "Do not run: write the compiled spec.v1 JSON to this path and exit")
	modelFactory := fs.String("model-factory", "echo", "Model backend for llm/agent nodes (the open distribution ships echo)")
	localPlugins := fs.Bool("local-plugins", false, "Register the built-in offline local libraries (deterministic; platform tools are bound by symbol)")
	artifactDir := fs.String("artifact-dir", "", "Directory for artifacts written by --local-plugins (default: $TMPDIR/ncoda-local-artifacts)")
	localDB := fs.String("local-db", "", "SQLite file backing the local database library (--local-plugins); empty = in-process :memory:")
	var toolPaths []string
	fs.Func("tool-path", "Search path for capability headers imported by the source (repeatable; default: <exe>/libraries, $NCLANG_TOOL_PATH, ~/.nodecoda/tools)", func(v string) error {
		if v != "" {
			toolPaths = append(toolPaths, v)
		}
		return nil
	})
	versionFlag := fs.Bool("version", false, "Print the runtime version and exit")
	var params multiFlag
	fs.Func("p", "Entry input as key=value (repeatable; coerced to JSON scalars)", func(v string) error {
		params = append(params, v)
		return nil
	})
	flagArgs, posArgs := splitFlagArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *versionFlag {
		fmt.Fprintf(stdout, "ncoda-run %s\n", version.Version)
		return 0
	}
	if *modelFactory != "echo" {
		fmt.Fprintf(stderr, "ncoda-run: --model-factory %q is not available in the open distribution (only echo); real model backends are host/adapter capabilities.\n", *modelFactory)
		return 2
	}

	source, name, err := readRunSource(stdin, posArgs)
	if err != nil {
		fmt.Fprintln(stderr, "ncoda-run:", err)
		return 2
	}
	input, err := runInput(*inputJSON, params)
	if err != nil {
		fmt.Fprintln(stderr, "ncoda-run:", err)
		return 2
	}

	// Capability headers: the source's import declarations resolve against the
	// same search-path model as nclang-compile (flag > env > <exe>/libraries >
	// user default). Missing directories are skipped (CPATH semantics).
	pipeline := passes.DefaultPipeline()
	var semDefs []tool.Definition
	capReg, cerr := tool.LoadCapabilitySearchPaths(toolpath.Resolve(toolPaths, os.Getenv, os.UserHomeDir))
	if cerr != nil {
		fmt.Fprintln(stderr, "ncoda-run: capability tool paths:", cerr)
		return 1
	}
	if capReg != nil {
		semDefs = append(semDefs, capReg.Definitions()...)
	}

	// Local libraries: the open distribution's deterministic implementations
	// (image/database/matting/scale). Platform physical ids are adapter facts
	// and never registered here.
	var pm *plugin.Manager
	if *localPlugins {
		pm = plugin.New()
		if _, lerr := localtools.Register(pm, localtools.Options{ArtifactDir: *artifactDir, DBPath: *localDB}); lerr != nil {
			fmt.Fprintln(stderr, "ncoda-run: local plugins:", lerr)
			return 1
		}
		tr, terr := toolbridge.FromPluginManager(pm)
		if terr != nil {
			fmt.Fprintln(stderr, "ncoda-run: semantic registry:", terr)
			return 1
		}
		semDefs = append(tr.Definitions(), semDefs...)
	}
	if len(semDefs) > 0 {
		merged, merr := tool.NewRegistry(toolbridge.MergeDefinitions(semDefs)...)
		if merr != nil {
			fmt.Fprintln(stderr, "ncoda-run: semantic registry:", merr)
			return 1
		}
		pipeline = pipeline.WithToolRegistry(merged)
	}

	res := pipeline.Run(source, name, runtimeProfile())
	if res.HasErrors() || res.Program == nil {
		for _, d := range res.Errors() {
			fmt.Fprintf(stderr, "%s: %s\n", d.Code.String(), d.Message)
		}
		return 1
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		fmt.Fprintln(stderr, "ncoda-run: ir:", err)
		return 1
	}
	proj, err := flow.Project(g)
	if err != nil {
		fmt.Fprintln(stderr, "ncoda-run: project:", err)
		return 1
	}
	s, err := spec.Build(g, proj)
	if err != nil {
		fmt.Fprintln(stderr, "ncoda-run: spec:", err)
		return 1
	}
	if *emitSpec != "" {
		raw, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, "ncoda-run: emit-spec:", err)
			return 1
		}
		if err := os.WriteFile(*emitSpec, append(raw, '\n'), 0o644); err != nil {
			fmt.Fprintln(stderr, "ncoda-run: emit-spec:", err)
			return 1
		}
		return 0
	}

	deps := nodes.Deps{Models: echoModel{}}
	if pm != nil {
		deps.Tools = pm
	}
	c := rtcompile.New(nodes.StdRegistry(), rtcompile.WithDeps(deps))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		fmt.Fprintln(stderr, "ncoda-run: compile:", err)
		return 1
	}
	out, err := r.Invoke(context.Background(), input)
	if err != nil {
		fmt.Fprintln(stderr, "ncoda-run: run:", err)
		return 1
	}
	result := rtcompile.UnwrapOutput(out)
	if *raw {
		if len(result) == 1 {
			if v, ok := result["output"].(string); ok {
				fmt.Fprintln(stdout, v)
				return 0
			}
		}
	}
	var buf []byte
	if *pretty {
		buf, err = json.MarshalIndent(result, "", "  ")
	} else {
		buf, err = json.Marshal(result)
	}
	if err != nil {
		fmt.Fprintln(stderr, "ncoda-run: marshal:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(buf))
	return 0
}

// readRunSource returns the source text: a positional path, else stdin.
func readRunSource(stdin io.Reader, args []string) (string, string, error) {
	if len(args) > 1 {
		return "", "", fmt.Errorf("accepts at most one source path (got %d)", len(args))
	}
	if len(args) == 1 {
		b, err := os.ReadFile(args[0])
		if err != nil {
			return "", "", err
		}
		return string(b), args[0], nil
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return "", "", err
	}
	return string(b), "<stdin>", nil
}

// runInput merges --input with -p pairs; -p wins on conflict.
func runInput(inputJSON string, params []string) (map[string]any, error) {
	out := map[string]any{}
	if inputJSON != "" {
		body := inputJSON
		if strings.HasPrefix(body, "@") {
			b, err := os.ReadFile(strings.TrimPrefix(body, "@"))
			if err != nil {
				return nil, err
			}
			body = string(b)
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			return nil, fmt.Errorf("--input: %w", err)
		}
	}
	for _, p := range params {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("-p %q: want key=value", p)
		}
		out[k] = coerceInputValue(v)
	}
	return out, nil
}

// coerceInputValue reads a scalar as JSON when it parses, else keeps the text.
func coerceInputValue(v string) any {
	var parsed any
	if err := json.Unmarshal([]byte(v), &parsed); err == nil {
		return parsed
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n
	}
	return v
}

// splitFlagArgs lets the source path appear before or after flags: Go's flag
// package stops at the first positional, which would silently drop `-p` after
// a path. Boolean flags never consume the next token.
func splitFlagArgs(args []string) (flags, pos []string) {
	boolFlag := map[string]bool{"pretty": true, "raw": true, "version": true, "local-plugins": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return flags, append(pos, args[i+1:]...)
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") || boolFlag[name] {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return flags, pos
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
