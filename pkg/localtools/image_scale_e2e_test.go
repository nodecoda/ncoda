package localtools_test

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

const imageScaleE2ESrc = `@language nodecoda/1
@mode workflow
import "image_scale";

function main(file<image> input) -> file<image> {
    let r = image_scale.resize({ "image_url": file(input), "max_length": 16 }) with timeout(180s);
    return r.data;
}
`

func TestImageScaleResizeFromNcodaSource(t *testing.T) {
	const w, h = 32, 16
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{200, uint8(x * 7), uint8(y * 13), 255})
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
	res := passes.DefaultPipeline().WithToolRegistry(caps).Run(imageScaleE2ESrc, "image_scale_e2e.ncoda", runtimeProfile())
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
	scaled, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	if got := scaled.Bounds(); got.Dx() != 16 || got.Dy() != 8 {
		t.Fatalf("bounds = %v, want 16x8", got)
	}
}
