package migrate

import (
	"strings"
	"testing"
)

// intent 语料：关键字级 1:1 迁移（含 result 绑定 / config / default / 空 default）。
const intentBody = `function main(string input) -> string {
    intent (input) -> r {
        "support": {
            return "support:" + r.reason;
        }
        "billing": {
            return "billing:" + r.classificationId;
        }
        default: {
            return "default:" + r.reason;
        }
    }
}
`

func TestRewriteIntentToSwitchKeyword(t *testing.T) {
	got := RewriteIntentClassify(intentBody, "test.ncoda")
	if strings.Contains(got.Source, "intent (") {
		t.Fatalf("intent keyword not rewritten:\n%s", got.Source)
	}
	if !strings.Contains(got.Source, "switch (input) -> r {") {
		t.Fatalf("expected switch statement:\n%s", got.Source)
	}
	// idempotent second pass
	again := RewriteIntentClassify(got.Source, "test.ncoda")
	if again.Changed {
		t.Fatalf("second pass changed source: %#v", again)
	}
}

func TestRewriteIntentWithConfig(t *testing.T) {
	src := `function main(string input) -> string {
    intent (input, { temperature: 0.5 }) -> r {
        "a": { return "A"; }
    }
}
`
	got := RewriteIntentClassify(src, "test.ncoda")
	if !strings.Contains(got.Source, "switch (input, { temperature: 0.5 }) -> r {") {
		t.Fatalf("config not preserved:\n%s", got.Source)
	}
	if strings.Contains(got.Source, "intent") {
		t.Fatalf("intent keyword remains:\n%s", got.Source)
	}
}

func TestRewriteNoIntentUntouched(t *testing.T) {
	src := `function main() -> string { return "hello"; }
`
	got := RewriteIntentClassify(src, "test.ncoda")
	if got.Changed {
		t.Fatalf("no-intent source changed: %#v", got)
	}
	if got.Source != src {
		t.Fatalf("source altered:\n%s", got.Source)
	}
}

func TestRewriteClassifyDetectedManual(t *testing.T) {
	src := `@language nodecoda/1
@mode workflow
enum Intent { unknown, support, billing }
function main(string query) -> string {
    let cls = classify<Intent>("provider/model", query, {
        instruction: "Classify the request",
        labels: { unknown: "u", support: "s", billing: "b" },
        fallback: Intent.support
    });
    return "done";
}
`
	got := RewriteIntentClassify(src, "test.ncoda")
	if len(got.Diagnostics) == 0 {
		t.Fatalf("expected classify diagnostic, got none")
	}
	found := false
	for _, d := range got.Diagnostics {
		if d.Code == CodeClassifyToSwitch && d.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected CodeClassifyToSwitch error diagnostic, got %#v", got.Diagnostics)
	}
	// classify 调用保留原样（不机械改写分支体不可知）
	if !strings.Contains(got.Source, "classify<Intent>") {
		t.Fatalf("classify call should be preserved for manual migration:\n%s", got.Source)
	}
}

func TestRewriteIdentifierNotKeyword(t *testing.T) {
	// 标识符内含 intent 不得改写（parser 拒绝保留字作标识符,但防御性保证）
	src := `function main() -> string { let switchboard = "intent"; return switchboard; }
`
	got := RewriteIntentClassify(src, "test.ncoda")
	if !strings.Contains(got.Source, `"intent"`) {
		t.Fatalf("string literal 'intent' must be preserved:\n%s", got.Source)
	}
}

func TestRewriteMixedIntentAndClassify(t *testing.T) {
	src := `function main(string q) -> string {
    intent (q) -> r {
        "a": { let x = classify<Enum>("m", q, {}); return "A"; }
    }
}
`
	got := RewriteIntentClassify(src, "test.ncoda")
	if strings.Contains(got.Source, "intent (") {
		t.Fatalf("intent not rewritten:\n%s", got.Source)
	}
	hasClassify := false
	for _, d := range got.Diagnostics {
		if d.Code == CodeClassifyToSwitch {
			hasClassify = true
		}
	}
	if !hasClassify {
		t.Fatalf("expected classify diagnostic in mixed source")
	}
}
