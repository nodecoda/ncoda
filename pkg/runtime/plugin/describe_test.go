package plugin

import (
	"context"
	"testing"
)

func TestDescribeToolSchema(t *testing.T) {
	m := New()
	p := &Plugin{
		ID: "math", Type: PluginCallable,
		Tools: []Tool{{
			Name: "sum", Desc: "add two ints",
			Call: func(ctx context.Context, args map[string]any) (any, error) { return nil, nil },
			Params: []ParamSchema{
				{Name: "a", Type: "int", Required: true},
				{Name: "b", Type: "int", Required: true},
				{Name: "label", Type: "string"},
			},
		}},
	}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	spec, found := m.Describe(context.Background(), "math/sum")
	if !found {
		t.Fatal("Describe not found")
	}
	if spec.Name != "sum" || spec.Description != "add two ints" {
		t.Fatalf("spec = %+v", spec)
	}
	params, _ := spec.Parameters["properties"].(map[string]any)
	if len(params) != 3 {
		t.Fatalf("properties = %v", params)
	}
	if params["a"].(map[string]any)["type"] != "integer" {
		t.Fatalf("a = %v", params["a"])
	}
	req, _ := spec.Parameters["required"].([]string)
	if len(req) != 2 {
		t.Fatalf("required = %v", req)
	}
}

func TestDescribeUnknownTool(t *testing.T) {
	m := New()
	if _, ok := m.Describe(context.Background(), "nope/x"); ok {
		t.Fatal("want not found")
	}
}

func TestDescribeDegenerateName(t *testing.T) {
	m := New()
	p := &Plugin{ID: "p1", Type: PluginCallable,
		Tools: []Tool{{Name: "t", Call: func(ctx context.Context, args map[string]any) (any, error) { return nil, nil }}}}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Describe(context.Background(), "t"); !ok {
		t.Fatal("degenerate name describe failed")
	}
}

// TestDescribeParameterRules:parameter_rules（enum/min/max）投影进 JSON
// Schema —— agent 模型可见取值白名单与数值边界（对齐参考目标 options/min/max）。
func TestDescribeParameterRules(t *testing.T) {
	m := New()
	fmin := 0.0
	fmax := 10.0
	p := &Plugin{
		ID: "geo", Type: PluginCallable,
		Tools: []Tool{{
			Name: "weather", Desc: "query weather",
			Call: func(ctx context.Context, args map[string]any) (any, error) { return nil, nil },
			Params: []ParamSchema{
				{Name: "unit", Type: "string", Required: true,
					Enum: []any{"celsius", "fahrenheit"}},
				{Name: "days", Type: "int", Min: &fmin, Max: &fmax},
			},
		}},
	}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	spec, found := m.Describe(context.Background(), "geo/weather")
	if !found {
		t.Fatal("Describe not found")
	}
	params, _ := spec.Parameters["properties"].(map[string]any)

	unit, _ := params["unit"].(map[string]any)
	enum, _ := unit["enum"].([]any)
	if len(enum) != 2 || enum[0] != "celsius" || enum[1] != "fahrenheit" {
		t.Fatalf("unit enum = %v, want [celsius fahrenheit]", enum)
	}

	days, _ := params["days"].(map[string]any)
	if days["minimum"] != 0.0 || days["maximum"] != 10.0 {
		t.Fatalf("days bounds = %v / %v, want 0 / 10", days["minimum"], days["maximum"])
	}

	// 无约束参数不出现 enum/minimum/maximum 键。
	plain := &Plugin{ID: "plain", Type: PluginCallable,
		Tools: []Tool{{Name: "echo", Call: func(ctx context.Context, args map[string]any) (any, error) { return nil, nil },
			Params: []ParamSchema{{Name: "x", Type: "string"}}}},
	}
	if err := m.Register(plain); err != nil {
		t.Fatal(err)
	}
	ps, _ := m.Describe(context.Background(), "plain/echo")
	pp, _ := ps.Parameters["properties"].(map[string]any)
	x, _ := pp["x"].(map[string]any)
	if _, has := x["enum"]; has {
		t.Fatalf("unconstrained param must not carry enum: %v", x)
	}
	if _, has := x["minimum"]; has {
		t.Fatalf("unconstrained param must not carry minimum: %v", x)
	}
}
