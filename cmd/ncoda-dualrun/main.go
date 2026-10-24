// Command ncoda-dualrun is the P1 dual-run evidence tool for the graph
// obligation kernel (docs/GRAPH-OBLIGATION-KERNEL.md v2 §7).
//
// Snapshot mode (built at the pre-solver baseline, e.g. commit 51b4b71)
// freezes the projection of every corpus file into a canonical digest:
//
//	ncoda-dualrun -corpus DIR -out snapshot.json
//
// Compare mode (built at HEAD with -tags plan) recompiles the corpus and
// checks that both the frozen old path (flow.Project) and the new plan path
// (flow.Plan -> flow.ProjectFromPlan) reproduce every frozen digest:
//
//	ncoda-dualrun -corpus DIR -compare snapshot.json
//
// Files that fail to compile are recorded as errors and fail the run; a
// partial snapshot is never written.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/tool"
)

const snapshotVersion = "ncoda.dualrun-snapshot/v1"

type fileRecord struct {
	File   string `json:"file"`
	Nodes  int    `json:"nodes"`
	Edges  int    `json:"edges"`
	Digest string `json:"digest"`
}

type snapshot struct {
	Version string       `json:"version"`
	Corpus  string       `json:"corpus"`
	Files   int          `json:"files"`
	Nodes   int          `json:"nodes"`
	Edges   int          `json:"edges"`
	Digest  string       `json:"digest"`
	Records []fileRecord `json:"records"`
}

type pathsFlag []string

func (p *pathsFlag) String() string { return strings.Join(*p, string(os.PathListSeparator)) }
func (p *pathsFlag) Set(v string) error {
	for _, part := range strings.Split(v, string(os.PathListSeparator)) {
		if part != "" {
			*p = append(*p, part)
		}
	}
	return nil
}

func main() {
	var toolPaths pathsFlag
	corpus := flag.String("corpus", os.Getenv("NCODA_CORPUS"), "corpus root containing *.ncoda files (default: $NCODA_CORPUS; the corpus lives in its own repo)")
	out := flag.String("out", "", "snapshot output path (snapshot mode)")
	compare := flag.String("compare", "", "snapshot path to verify (compare mode)")
	limit := flag.Int("limit", 0, "optional file limit for debugging")
	flag.Var(&toolPaths, "tool-path", "capability search path (repeatable; ncoda path-list separators allowed)")
	flag.Parse()

	if (*out == "") == (*compare == "") {
		fatalf("exactly one of -out or -compare is required")
	}
	registry, err := loadRegistry(toolPaths)
	if err != nil {
		fatalf("tool paths: %v", err)
	}
	files, err := corpusFiles(*corpus, *limit)
	if err != nil {
		fatalf("%v", err)
	}
	if *out != "" {
		if err := writeSnapshot(*corpus, files, registry, *out); err != nil {
			fatalf("%v", err)
		}
		return
	}
	if err := compareSnapshot(*corpus, files, registry, *compare); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ncoda-dualrun: "+format+"\n", args...)
	os.Exit(1)
}

func loadRegistry(paths []string) (*tool.Registry, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	return tool.LoadCapabilitySearchPaths(paths)
}

func corpusFiles(root string, limit int) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("corpus %s: %w", root, err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ncoda") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("corpus %s: no .ncoda files", root)
	}
	return files, nil
}

func writeSnapshot(corpus string, files []string, registry *tool.Registry, out string) error {
	snap := snapshot{Version: snapshotVersion, Corpus: corpus, Records: make([]fileRecord, 0, len(files))}
	agg := sha256.New()
	for _, name := range files {
		proj, err := projectFile(filepath.Join(corpus, name), registry)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		digest, nodes, edges := projectionDigest(proj)
		snap.Records = append(snap.Records, fileRecord{File: name, Nodes: nodes, Edges: edges, Digest: digest})
		fmt.Fprintf(agg, "%s\t%s\n", name, digest)
		snap.Nodes += nodes
		snap.Edges += edges
	}
	snap.Files = len(snap.Records)
	snap.Digest = hex.EncodeToString(agg.Sum(nil))
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ncoda-dualrun: snapshot files=%d nodes=%d edges=%d digest=%s\n",
		snap.Files, snap.Nodes, snap.Edges, snap.Digest)
	return nil
}

func compareSnapshot(corpus string, files []string, registry *tool.Registry, snapshotPath string) error {
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return err
	}
	var want snapshot
	if err := json.Unmarshal(data, &want); err != nil {
		return fmt.Errorf("snapshot %s: %w", snapshotPath, err)
	}
	if want.Version != snapshotVersion {
		return fmt.Errorf("snapshot %s: version %q, want %q", snapshotPath, want.Version, snapshotVersion)
	}
	byFile := make(map[string]fileRecord, len(want.Records))
	for _, rec := range want.Records {
		byFile[rec.File] = rec
	}
	mismatches := 0
	nodes, edges := 0, 0
	for _, name := range files {
		wantRec, ok := byFile[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "MISSING snapshot record: %s\n", name)
			mismatches++
			continue
		}
		graph, err := buildGraph(filepath.Join(corpus, name), registry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR %s: %v\n", name, err)
			mismatches++
			continue
		}
		oldPath, err := flow.Project(graph)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR %s: Project: %v\n", name, err)
			mismatches++
			continue
		}
		newPath, err := planProjection(graph)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR %s: Plan: %v\n", name, err)
			mismatches++
			continue
		}
		oldDigest, _, _ := projectionDigest(oldPath)
		newDigest, newNodes, newEdges := projectionDigest(newPath)
		if oldDigest != wantRec.Digest {
			fmt.Fprintf(os.Stderr, "OLD-PATH DRIFT %s: snapshot=%s replayed=%s\n", name, wantRec.Digest, oldDigest)
			mismatches++
			continue
		}
		if newDigest != wantRec.Digest {
			fmt.Fprintf(os.Stderr, "PLAN-PATH DRIFT %s: snapshot=%s plan=%s (nodes %d/%d, edges %d/%d)\n",
				name, wantRec.Digest, newDigest, wantRec.Nodes, newNodes, wantRec.Edges, newEdges)
			mismatches++
			continue
		}
		nodes += newNodes
		edges += newEdges
	}
	if len(want.Records) != len(files) {
		fmt.Fprintf(os.Stderr, "FILE SET DRIFT: snapshot=%d corpus=%d\n", len(want.Records), len(files))
		mismatches++
	}
	if mismatches != 0 {
		return fmt.Errorf("dual-run failed: %d/%d files differ from %s", mismatches, len(files), snapshotPath)
	}
	fmt.Fprintf(os.Stderr, "ncoda-dualrun: dual-run OK files=%d nodes=%d edges=%d snapshot=%s\n",
		len(files), nodes, edges, want.Digest)
	return nil
}

func projectFile(path string, registry *tool.Registry) (*flow.Projection, error) {
	graph, err := buildGraph(path, registry)
	if err != nil {
		return nil, err
	}
	return flow.Project(graph)
}

func buildGraph(path string, registry *tool.Registry) (*ir.Graph, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pipeline := passes.DefaultPipeline()
	if registry != nil {
		pipeline = pipeline.WithToolRegistry(registry)
	}
	front := pipeline.Run(string(src), filepath.Base(path), runtimeProfile())
	if front.HasErrors() {
		errs := front.Errors()
		return nil, fmt.Errorf("frontend: %s: %s", errs[0].Code, errs[0].Message)
	}
	if front.Program == nil {
		return nil, fmt.Errorf("frontend returned no program")
	}
	graph, err := ir.Build(front.Program)
	if err != nil {
		return nil, fmt.Errorf("ir: %w", err)
	}
	return graph, nil
}

// runtimeProfile mirrors ncoda run's frontend profile: the corpus is
// decompiled workflow source, and runtime semantics (URL/data-URL file
// carriers) must not be rejected by reference-only shape gates.
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
	}
}

// projectionDigest canonicalizes nodes (id, kind) and edges (source, target,
// source port, target port) in projection order. Order is semantic: the
// dual-run gate compares sequences, not sets.
func projectionDigest(p *flow.Projection) (string, int, int) {
	h := sha256.New()
	nodes := 0
	for _, n := range p.Nodes {
		if n == nil {
			h.Write([]byte("N\t<nil>\n"))
			continue
		}
		fmt.Fprintf(h, "N\t%s\t%s\n", n.NodeID(), n.Kind())
		nodes++
	}
	edges := 0
	for _, e := range p.Edges {
		fmt.Fprintf(h, "E\t%s\t%s\t%s\t%s\n", e.SourceNodeID, e.TargetNodeID, e.SourcePortID, e.TargetPortID)
		edges++
	}
	return hex.EncodeToString(h.Sum(nil)), nodes, edges
}
