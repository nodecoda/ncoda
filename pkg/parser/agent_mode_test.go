package parser

import (
	"strings"
	"testing"
)

// ── @mode agent + @agent declaration ─────────────────────────────────────

const agentModeSrc = `@language nodecoda/1
@mode agent

@agent {
    model: "gpt-5.6-sol",
    instruction: "你是天气助手",
    strategy: "function-calling",
    max_iteration: 10,
    tools: ["weather", "clock"]
}

function main(string query) -> string {
    return run(query);
}
`

func TestAgentModeParse(t *testing.T) {
	prog := conformOK(t, agentModeSrc)
	if got := prog.Mode(); got != "agent" {
		t.Fatalf("Mode() = %q, want agent", got)
	}
	d := prog.AgentDecl()
	if d == nil {
		t.Fatal("AgentDecl() = nil, want declaration")
	}
	if d.Model() != "gpt-5.6-sol" {
		t.Errorf("Model() = %q", d.Model())
	}
	if d.Instruction() != "你是天气助手" {
		t.Errorf("Instruction() = %q", d.Instruction())
	}
	if d.Strategy() != "function-calling" {
		t.Errorf("Strategy() = %q, want function-calling", d.Strategy())
	}
	if d.MaxIteration() != 10 {
		t.Errorf("MaxIteration() = %d, want 10", d.MaxIteration())
	}
	tools := d.Tools()
	if len(tools) != 2 || tools[0] != "weather" || tools[1] != "clock" {
		t.Errorf("Tools() = %v, want [weather clock]", tools)
	}
}

func TestAgentModeMinimal(t *testing.T) {
	// model 唯一必填字段;缺省 max_iteration/tools 合法。
	prog := conformOK(t, "@language nodecoda/1\n@mode agent\n@agent { model: \"m\" }\nfunction main(string q) -> string { return run(q); }\n")
	d := prog.AgentDecl()
	if d == nil || d.Model() != "m" {
		t.Fatalf("AgentDecl = %+v, want model m", d)
	}
	if d.Strategy() != "" || d.MaxIteration() != 0 || len(d.Tools()) != 0 {
		t.Fatalf("defaults: strategy=%q max_iteration=%d tools=%v, want empty/0/nil", d.Strategy(), d.MaxIteration(), d.Tools())
	}
}

func TestAgentModeErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"missing-model", "@mode agent\n@agent { instruction: \"i\" }\nfunction main(string q) -> string { return run(q); }", "Missing required @agent field: model"},
		{"unknown-field", "@mode agent\n@agent { model: \"m\", bogus: 1 }\nfunction main(string q) -> string { return run(q); }", "Unknown @agent field"},
		{"not-agent-mode", "@mode workflow\n@agent { model: \"m\" }\nfunction main() {}", "requires @mode agent"},
		{"duplicate", "@mode agent\n@agent { model: \"m\" }\n@agent { model: \"n\" }\nfunction main() {}", "Duplicate @agent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := ParseSource(c.src, "<agent>")
			if len(errs) == 0 || !strings.Contains(errs[0].Message, c.want) {
				t.Fatalf("errs = %v, want containing %q", errs, c.want)
			}
		})
	}
}

func TestAgentModeMemoryParse(t *testing.T) {
	prog := conformOK(t, `@language nodecoda/1
@mode agent

@agent {
    model: "m",
    instruction: "i",
    memory: true,
    memory_window: 8
}

function main(string query) -> string {
    return run(query);
}
`)
	d := prog.AgentDecl()
	if d == nil {
		t.Fatal("AgentDecl() = nil")
	}
	if !d.Memory() {
		t.Errorf("Memory() = false, want true")
	}
	if d.MemoryWindow() != 8 {
		t.Errorf("MemoryWindow() = %d, want 8", d.MemoryWindow())
	}
}

func TestAgentModeMemoryDefaults(t *testing.T) {
	// memory 缺省 false、memory_window 缺省 0（= 不限）。
	prog := conformOK(t, "@language nodecoda/1\n@mode agent\n@agent { model: \"m\", memory: false }\nfunction main(string q) -> string { return run(q); }\n")
	d := prog.AgentDecl()
	if d.Memory() || d.MemoryWindow() != 0 {
		t.Fatalf("defaults: memory=%v window=%d, want false/0", d.Memory(), d.MemoryWindow())
	}
}

func TestAgentModeMemoryErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"bad-type", "@mode agent\n@agent { model: \"m\", memory: \"yes\" }\nfunction main(string q) -> string { return run(q); }", "Expected boolean for memory"},
		{"bad-window", "@mode agent\n@agent { model: \"m\", memory: true, memory_window: \"10\" }\nfunction main(string q) -> string { return run(q); }", "Expected integer for memory_window"},
		// 负数在 map literal 值位不被词法支持 → 按类型错误报出（>=0 由解析保证）。
		{"neg-window", "@mode agent\n@agent { model: \"m\", memory: true, memory_window: -1 }\nfunction main(string q) -> string { return run(q); }", "Expected integer for memory_window"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := ParseSource(c.src, "<agent>")
			if len(errs) == 0 || !strings.Contains(errs[0].Message, c.want) {
				t.Fatalf("errs = %v, want containing %q", errs, c.want)
			}
		})
	}
}

// TestAgentModeKnowledgeParse:@agent 块 knowledge 配置解析
// （dataset_ids 白名单 + top_k;空 dataset_ids 视为未配置）。
func TestAgentModeKnowledgeParse(t *testing.T) {
	src := `@language nodecoda/1
@mode agent

@agent {
    model: "m",
    instruction: "用知识库回答",
    knowledge: {
        dataset_ids: ["kb1", "kb2"],
        top_k: 4
    }
}

function main(string query) -> string {
    return run(query);
}
`
	prog := conformOK(t, src)
	d := prog.AgentDecl()
	if d == nil {
		t.Fatal("AgentDecl missing")
	}
	if got := d.KnowledgeDatasetIDs(); len(got) != 2 || got[0] != "kb1" || got[1] != "kb2" {
		t.Fatalf("KnowledgeDatasetIDs() = %v, want [kb1 kb2]", got)
	}
	if d.KnowledgeTopK() != 4 {
		t.Fatalf("KnowledgeTopK() = %d, want 4", d.KnowledgeTopK())
	}
}

// TestAgentModeKnowledgeDefaults:无 knowledge 字段 → 未配置（nil/0）。
func TestAgentModeKnowledgeDefaults(t *testing.T) {
	prog := conformOK(t, agentModeSrc)
	d := prog.AgentDecl()
	if d.KnowledgeDatasetIDs() != nil || d.KnowledgeTopK() != 0 {
		t.Fatalf("defaults: ids=%v topK=%d, want nil/0", d.KnowledgeDatasetIDs(), d.KnowledgeTopK())
	}
}

// TestAgentModeKnowledgeBadShape:knowledge 类型/字段错误 → 解析期报错。
func TestAgentModeKnowledgeBadShape(t *testing.T) {
	cases := []struct{ name, src, wantErr string }{
		{"not-map", "@mode agent\n@agent { model: \"m\", knowledge: 42 }\nfunction main(string q) -> string { return run(q); }", "Expected map for knowledge"},
		{"bad-ds-type", "@mode agent\n@agent { model: \"m\", knowledge: { dataset_ids: 1 } }\nfunction main(string q) -> string { return run(q); }", "Expected list for dataset_ids"},
		{"bad-topk", "@mode agent\n@agent { model: \"m\", knowledge: { dataset_ids: [\"a\"], top_k: \"4\" } }\nfunction main(string q) -> string { return run(q); }", "Expected integer for top_k"},
		{"neg-topk", "@mode agent\n@agent { model: \"m\", knowledge: { dataset_ids: [\"a\"], top_k: -1 } }\nfunction main(string q) -> string { return run(q); }", "Expected integer for top_k"},
		{"unknown-field", "@mode agent\n@agent { model: \"m\", knowledge: { nope: 1 } }\nfunction main(string q) -> string { return run(q); }", "Unknown knowledge field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := ParseSource(c.src, "<agent-mode>")
			if len(errs) == 0 {
				t.Fatalf("want error containing %q, got none", c.wantErr)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), c.wantErr) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("errors = %v, want one containing %q", errs, c.wantErr)
			}
		})
	}
}
