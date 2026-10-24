package spec

// corpus_shim_test.go — M4 永久语料回归:编译器生成的每个 synthetic code
// shim 都必须被求值器接受（ParseShim 成功）。这是生成端（IR pyast）与
// 求值端（expr）的闭合契约:生成端一旦发出求值器不认识的构造,这里立刻
// 红。语料形状计数下限防止"语料退化"导致测试静默空转。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/flow"
	"github.com/nodecoda/ncoda/pkg/expr"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
)

func TestCorpusSyntheticShimsParseable(t *testing.T) {
	corpus := "../../../examples/nodecoda"
	files, _ := filepath.Glob(filepath.Join(corpus, "*.ncoda"))
	if len(files) == 0 {
		t.Fatal("no corpus files found")
	}
	var shimCount int
	shapes := map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		// 部分语料文件是管道负例（tool 绑定等）,编译失败的跳过 —— 本测试
		// 只锁定"能编译出 shim 的语料"必须被求值器接受。
		res := passes.DefaultPipeline().Run(string(src), filepath.Base(f), nil)
		if res.HasErrors() || res.Program == nil {
			continue
		}
		irg, ierr := ir.Build(res.Program)
		if ierr != nil {
			continue
		}
		p, perr := flow.Project(irg)
		if perr != nil {
			continue
		}
		spec, serr := Build(irg, p)
		if serr != nil {
			continue
		}
		for _, n := range spec.Nodes {
			if n.Kind != KindCode {
				continue
			}
			cfg, err := DecodeConfig(n.Kind, n.Config)
			if err != nil {
				t.Fatalf("%s: decode: %v", filepath.Base(f), err)
			}
			cc := cfg.(*CodeConfig)
			if !cc.Synthetic {
				continue
			}
			shimCount++
			shapes[corpusShimShape(cc.Code)] = true
			if _, err := expr.ParseShim(cc.Code); err != nil {
				t.Fatalf("%s node %s: synthetic shim rejected by evaluator: %v\ncode:\n%s",
					filepath.Base(f), n.ID, err, cc.Code)
			}
		}
	}
	if shimCount < 10 {
		t.Fatalf("expected >=10 synthetic shims in corpus, got %d", shimCount)
	}
	if len(shapes) < 10 {
		// Lowered from 11 after the 2026-08-23 output single-value change:
		// output(key, value) key-materialization shims retired (48/56/57
		// rewritten), which removed one shim shape.
		t.Fatalf("expected >=10 distinct shim shapes, got %d", len(shapes))
	}
}

// corpusShimShape buckets a shim by its normalized one-line prefix (the
// shape-count lower bound guards against corpus degeneration).
func corpusShimShape(code string) string {
	c := code
	c = stringsReplaceNewline(c)
	if len(c) > 90 {
		c = c[:90] + "…"
	}
	return c
}

func stringsReplaceNewline(c string) string {
	out := make([]byte, 0, len(c))
	for i := 0; i < len(c); i++ {
		if c[i] == '\n' {
			out = append(out, '⏎'&0xff)
			continue
		}
		out = append(out, c[i])
	}
	return string(out)
}
