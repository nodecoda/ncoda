package ir_test

// 生成器集上的使用面普查（`L` 的事前面）——env 门控的一手读数，不是常驻判据。
//
// 为什么在生成器集上量：产物面已证伪（SHIM-ESCAPE-CONTRACT §4.2 读数边界 —— coze-biz 把
// 记录的成员物化成独立口 ⇒「读成员」与「读整口」在 wire 上同形 ⇒ `member_only` 恒 0）。
// 语言级的「整值读 / 成员读」只在 IR 面（物化之前）存在，所以这个数只能用本预扫量。
//
//   PRESCAN_GEN_DIR=<生成器源目录> go test ./internal/ir/ -run TestPrescanGeneratorCensus -v
//
// 目录可用 `scripts/five-step/gen_run.py` 物化（`--out <dir>`）。未设该环境变量时跳过。
// `usage_foldable` 那一列是**使用面上界**（P1∧P2，不看类型）⇒ 含数组下标读这类非记录形态，**不是**判决。
// 判据只有一条（撤预扫的「成员读 / 整值读」区分即红）：file_family 家族的 `r1`
// **必须**被看见「只读成员」（`whole = 0`）。其余是读数打印（生成器按形状刻意留下大量
// 未读绑定 ⇒ `dead` 那一列含生成器噪音，**不是**判决）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/parser"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/tool"
)

func TestPrescanGeneratorCensus(t *testing.T) {
	dir := os.Getenv("PRESCAN_GEN_DIR")
	if dir == "" {
		t.Skip("PRESCAN_GEN_DIR not set (generator sources; see scripts/five-step/gen_run.py)")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.ncoda"))
	if len(files) == 0 {
		t.Skipf("no *.ncoda under %s", dir)
	}
	base := passes.DefaultTargetProfile()
	profile := &passes.TargetProfile{
		Name:                                 "coze-biz-1.0",
		Operations:                           base.Operations,
		MaxParallelism:                       base.MaxParallelism,
		Capabilities:                         passes.CapabilityManifestForTarget("coze-biz-1.0"),
		AgentProfiles:                        base.AgentProfiles,
		ParameterExtractorFunctionCallModels: base.ParameterExtractorFunctionCallModels,
		ParameterExtractorMemoryMaxTurns:     base.ParameterExtractorMemoryMaxTurns,
		Codec:                                base.Codec,
	}
	toolPaths := []string{os.Getenv("PRESCAN_TOOL_PATH")}
	var (
		nFiles, nBind, nMemberOnly, nDead, nBranch, nMemberOnlyR1 int
		nFileFamily                                               int
		nUsageFoldable                                            int
	)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var reg *tool.Registry
		if prog, perrs := parser.ParseSource(string(src), f); len(perrs) == 0 && len(prog.Imports()) > 0 {
			imports := make([]string, 0, len(prog.Imports()))
			for _, imp := range prog.Imports() {
				imports = append(imports, imp.Provider())
			}
			if toolPaths[0] == "" {
				continue // 有 import 但没给 --tool-path 等价物 ⇒ 该源不可编译，跳过
			}
			reg, err = tool.LoadCapabilityPackages(toolPaths, imports)
			if err != nil {
				t.Fatalf("%s: load capability packages: %v", filepath.Base(f), err)
			}
		}
		res := passes.DefaultPipeline().WithToolRegistry(reg).Run(string(src), f, profile)
		if res.HasErrors() || res.Program == nil {
			t.Fatalf("%s: pipeline reported errors", filepath.Base(f))
		}
		nFiles++
		usages := ir.PrescanBindingUsages(res.Program)
		for _, u := range usages {
			if u.Writes == 0 {
				continue
			}
			nBind++
			if u.MemberOnlyReads() {
				nMemberOnly++
			}
			if u.UsageAllowsWholeValueFold() {
				nUsageFoldable++
			}
			if u.TotalReads() == 0 {
				nDead++
			}
			if u.BranchWrites > 0 {
				nBranch++
			}
		}
		if strings.Contains(filepath.Base(f), "file_family") {
			nFileFamily++
			if u := usages["r1"]; u != nil && u.MemberOnlyReads() {
				nMemberOnlyR1++
			}
		}
	}
	t.Logf("CENSUS files=%d written_bindings=%d member_only=%d no_read_at_all=%d branch_written=%d usage_foldable=%d (file_family r1 member-only: %d/%d)",
		nFiles, nBind, nMemberOnly, nDead, nBranch, nUsageFoldable, nMemberOnlyR1, nFileFamily)
	if nFileFamily > 0 && nMemberOnlyR1 != nFileFamily {
		t.Fatalf("file_family r1 must be member-only in every source (E1/E2 lock): %d/%d", nMemberOnlyR1, nFileFamily)
	}
}
