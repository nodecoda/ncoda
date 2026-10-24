package localtools_test

// 端到端判据：ncoda 源 `matting.cutout({url: file(input)})` 必须走完
// frontend → IR → flow → spec.v1 → compile（tool 节点）→ localtools。
// 只测 store 层证明不了这条链是通的（database 那轮同样理由）。

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/localtools"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/runtime/compile"
	"github.com/nodecoda/ncoda/pkg/runtime/nodes"
	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
	"github.com/nodecoda/ncoda/pkg/runtime/spec"
	"github.com/nodecoda/ncoda/pkg/tool"
)

const mattingE2ESrc = `@language nodecoda/1
@mode workflow
import "matting";

function main(file<image> input) -> file<image> {
    let r = matting.cutout({ "url": file(input), "only_mask": 0, "output_mode": 0 }) with timeout(180s);
    return r.data;
}
`

// runtimeProfile 与 internal/cli/run.go 的同名函数保持一致：file<...> 的
// 访问只在 runtime 目标上合法，声明了类型限制的适配器目标会先红（那是目标差异，不是本判据）。
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

func TestMattingCutoutFromNcodaSource(t *testing.T) {
	// 输入图：纯蓝背景 + 中央白块。
	const w, h = 24, 24
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{20, 40, 220, 255}
			if x >= 8 && x < 16 && y >= 8 && y < 16 {
				c = color.RGBA{245, 245, 245, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(t.TempDir(), "in.png")
	if err := os.WriteFile(srcPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	caps, err := tool.LoadCapabilitySearchPaths([]string{
		filepath.Join("..", "..", "libraries"),
	})
	if err != nil {
		t.Fatalf("capability headers: %v", err)
	}
	res := passes.DefaultPipeline().WithToolRegistry(caps).Run(mattingE2ESrc, "matting_e2e.ncoda", runtimeProfile())
	if res.HasErrors() {
		for _, d := range res.Errors() {
			t.Logf("frontend: %s", d.Message)
		}
		t.Fatal("frontend errors")
	}
	g, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("ir: %v", err)
	}
	p, err := flow.Project(g)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	s, err := spec.Build(g, p, spec.BuildOptions{TimeoutAxes: spec.DeriveTimeoutAxes(p)})
	if err != nil {
		t.Fatalf("spec: %v", err)
	}

	m := plugin.New()
	if _, err := localtools.Register(m, localtools.Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("localtools: %v", err)
	}
	c := compile.New(nodes.StdRegistry(), compile.WithDeps(nodes.Deps{Tools: m}))
	r, err := c.Compile(context.Background(), s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := r.Invoke(context.Background(), map[string]any{"input": srcPath})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	dataURL := findOutputString(out, "output")
	if !strings.HasPrefix(dataURL, "file://") {
		t.Fatalf("output = %#v, want file:// artifact URL", out)
	}
	b, err := os.ReadFile(strings.TrimPrefix(dataURL, "file://"))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	cut, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	if _, _, _, a := cut.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("corner alpha = %d, want 0", a>>8)
	}
	if _, _, _, a := cut.At(12, 12).RGBA(); a != 0xffff {
		t.Fatalf("center alpha = %d, want 255", a>>8)
	}
}

func findOutputString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			if got := findOutputString(sub, key); got != "" {
				return got
			}
		}
	}
	return ""
}
