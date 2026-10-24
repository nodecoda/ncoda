// Package irdump owns the canonical ncoda-ir/v1 projection — the language's
// own audit artifact, emitted by `nclang-compile --emit-ir`.
//
// 2026-10-25 (ASSET-BOUNDARY P2b): moved here from the commercial
// internal/libaudit. The dump is platform-neutral by construction (no
// plugin_id / api_id / package field), so it belongs to the language repo;
// the commercial repo now re-exports it through libaudit for its scanners.
package irdump

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/tool"
)

// IRDumpSchema is the canonical dump schema identifier. Scanners must reject
// dumps that do not carry it (LL-065: the scan reads the dump, never source).
const IRDumpSchema = "ncoda-ir/v1"

// IRLibrary is one imported library pin. Name is the semantic library name;
// Version and ABI are empty (and reported) when no declaration/lock pins the
// library — an unpinned call is a diagnostic, never a silent default.
type IRLibrary struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	ABI     string `json:"abi"`
}

// IRParam is one ordered tool-call parameter.
type IRParam struct {
	Key   string `json:"key"`
	Value any    `json:"value,omitempty"`
	Ref   string `json:"ref,omitempty"` // "<producer>.<port>" when the value is dataflow
}

// IRToolCall is the platform-neutral projection of one capability call.
// There is deliberately no plugin_id / api_id / package field: physical
// identity belongs to the link phase, not the IR.
type IRToolCall struct {
	NodeID   string    `json:"node_id"`
	Library  string    `json:"library"`
	Function string    `json:"function"`
	Version  string    `json:"version"`
	ABI      string    `json:"abi"`
	Params   []IRParam `json:"params"`
}

// IRDump is the canonical, deterministic IR projection emitted by
// `nclang-compile --emit-ir`.
type IRDump struct {
	Schema          string       `json:"schema"`
	CompilerVersion string       `json:"compiler_version"`
	Source          string       `json:"source"`
	Mode            string       `json:"mode"`
	Libraries       []IRLibrary  `json:"libraries"`
	ToolCalls       []IRToolCall `json:"tool_calls"`
	Diagnostics     []string     `json:"diagnostics,omitempty"`
}

// LibraryABI computes the v2 library ABI fingerprint from the declared
// function table: domain label + sorted (operation, function ABI) pairs.
// The hash domain is fixed ("ncoda-lib-v1"); function hashes come from the
// capability registry (interface hash, currently scheme v1).
func LibraryABI(defs []tool.Definition) string {
	sorted := make([]tool.Definition, len(defs))
	copy(sorted, defs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Operation < sorted[j].Operation })
	h := sha256.New()
	write := func(v string) { _, _ = fmt.Fprintf(h, "%d:%s", len(v), v) }
	write("ncoda-lib-v1")
	for _, d := range sorted {
		write(d.Operation)
		write(d.InterfaceHash)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// BuildIRDump projects a built graph into the canonical dump. registry may be
// nil (open world): calls then stay unpinned and are recorded in Diagnostics.
func BuildIRDump(g *ir.Graph, registry *tool.Registry, imports []string, source, compilerVersion string) *IRDump {
	if g == nil {
		return nil
	}
	dump := &IRDump{
		Schema:          IRDumpSchema,
		CompilerVersion: compilerVersion,
		Source:          source,
		Mode:            g.Mode,
		Libraries:       []IRLibrary{},
		ToolCalls:       []IRToolCall{},
	}
	// Library pins: group registry definitions by provider to compute the
	// library ABI once per imported library.
	defsByProvider := map[string][]tool.Definition{}
	if registry != nil {
		for _, d := range registry.Definitions() {
			defsByProvider[d.Provider] = append(defsByProvider[d.Provider], d)
		}
	}
	seen := map[string]bool{}
	for _, raw := range imports {
		name := raw
		if _, bare, err := tool.SplitImport(raw); err == nil && bare != "" {
			name = bare
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		lib := IRLibrary{Name: name, Version: "0.1.0"}
		if defs := defsByProvider[name]; len(defs) > 0 {
			lib.ABI = LibraryABI(defs)
		} else {
			lib.Version = ""
			dump.Diagnostics = append(dump.Diagnostics, "IR_LIBRARY_UNPINNED: "+name)
		}
		dump.Libraries = append(dump.Libraries, lib)
	}
	sort.Slice(dump.Libraries, func(i, j int) bool { return dump.Libraries[i].Name < dump.Libraries[j].Name })

	libByProvider := map[string]IRLibrary{}
	for _, lib := range dump.Libraries {
		libByProvider[lib.Name] = lib
	}
	WalkNodes(g, func(n ir.Node) {
		tc, ok := n.(*ir.ToolCall)
		if !ok {
			return
		}
		lib := libByProvider[tc.ProviderID]
		if lib.Name == "" {
			lib = IRLibrary{Name: tc.ProviderID}
			if defs := defsByProvider[tc.ProviderID]; len(defs) > 0 {
				lib.Version, lib.ABI = "0.1.0", LibraryABI(defs)
			}
		}
		call := IRToolCall{
			NodeID:   tc.ID,
			Library:  lib.Name,
			Function: tc.Operation,
			Version:  lib.Version,
			ABI:      lib.ABI,
			Params:   make([]IRParam, 0, len(tc.Params)),
		}
		for _, p := range tc.Params {
			param := IRParam{Key: p.Key}
			if p.Ref != nil {
				param.Ref = p.Ref.BlockID + "." + p.Ref.Name
			} else {
				param.Value = p.Value
			}
			call.Params = append(call.Params, param)
		}
		if call.Version == "" || call.ABI == "" {
			dump.Diagnostics = append(dump.Diagnostics, "IR_LIBRARY_UNPINNED: call "+tc.ID+" "+call.Library+"."+call.Function)
		}
		dump.ToolCalls = append(dump.ToolCalls, call)
	})
	sort.Slice(dump.ToolCalls, func(i, j int) bool {
		if dump.ToolCalls[i].NodeID == dump.ToolCalls[j].NodeID {
			return dump.ToolCalls[i].Function < dump.ToolCalls[j].Function
		}
		return dump.ToolCalls[i].NodeID < dump.ToolCalls[j].NodeID
	})
	sort.Strings(dump.Diagnostics)
	dump.Diagnostics = dedupe(dump.Diagnostics)
	return dump
}

// Marshal canonicalizes the dump (sorted keys via encoding/json, trailing
// newline). Deterministic: same graph + registry => same bytes.
func (d *IRDump) Marshal() ([]byte, error) {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func dedupe(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
