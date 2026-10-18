package main

// Task 4 (TOOL-DIRECT-CALL-FORM): the codemod rewrites legacy
// tool("p","op",{...}) call sites to the qualified namespace-call form
// p.op({...}). These tests are the TDD surface: RED before the codemod
// exists (rewrite is undefined), GREEN after. The codemod is span surgery
// over the ORIGINAL source bytes — the parser/linter spans are rune
// offsets (Python str-indexing semantics), so every edit position is
// converted to byte offsets before slicing, which matters for sources
// whose comments contain multi-byte characters (CJK) before the call site
// (X13/06 corpus shape).

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/parser"
)

func TestRewriteToolCall(t *testing.T) {
	in := `function main() {
    let a = tool("tts", "speech_synthesis", { "text": "hi" });
    return a;
}`
	want := `function main() {
    let a = tts.speech_synthesis({ "text": "hi" });
    return a;
}`
	got, err := rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewriteToolCallConstProvider(t *testing.T) {
	// rewrite() folds the const via the const table but keeps the const
	// declaration (deletion is the separate file-level transform).
	in := `const FEIYING = "feiyin_digital_human";
function main(string t) {
    let job = tool(FEIYING, "create_lipsync_video", { "text": t });
    return job;
}`
	want := `const FEIYING = "feiyin_digital_human";
function main(string t) {
    let job = feiyin_digital_human.create_lipsync_video({ "text": t });
    return job;
}`
	got, err := rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMigrateSourceDeletesUsedProviderConst(t *testing.T) {
	// File-level transform: once every tool() site that used the const as
	// its provider is rewritten, the const declaration line is removed
	// (decision 3: const providers are not supported; namespaces are
	// literal identifiers).
	in := `@language nodecoda/1
@mode workflow
import "feiyin_digital_human";
const FEIYING = "feiyin_digital_human";

function main(string text) {
    let job = tool(FEIYING, "create_lipsync_video", { "text": text });
    let status = tool(FEIYING, "inspect_video_creation_status", { "job_id": job.job_id });
    return status;
}`
	want := `@language nodecoda/1
@mode workflow
import "feiyin_digital_human";

function main(string text) {
    let job = feiyin_digital_human.create_lipsync_video({ "text": text });
    let status = feiyin_digital_human.inspect_video_creation_status({ "job_id": job.job_id });
    return status;
}`
	got, err := migrateSource(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMigrateSourceKeepsConstUsedElsewhere(t *testing.T) {
	// A const used outside tool() provider position must NOT be deleted —
	// the transform is safe only for provider-only constants.
	in := `@language nodecoda/1
@mode workflow
import "tts";
const VOICE = "zh";

function main(string text) {
    let audio = tool("tts", "speech_synthesis", { "text": text, "voice_id": VOICE });
    return audio;
}`
	want := `@language nodecoda/1
@mode workflow
import "tts";
const VOICE = "zh";

function main(string text) {
    let audio = tts.speech_synthesis({ "text": text, "voice_id": VOICE });
    return audio;
}`
	got, err := migrateSource(in)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Review R1: the whole-line const deletion must be reparse-verified.
// migrateSource() now re-parses after deleteConstLines internally; this
// test pins the invariant explicitly so removing the internal check (or
// breaking the deletion) fails loudly instead of silently corrupting the
// output.
func TestMigrateSourceOutputReparses(t *testing.T) {
	in := `@language nodecoda/1
@mode workflow
import "feiyin_digital_human";
const FEIYING = "feiyin_digital_human";

function main(string text) {
    let job = tool(FEIYING, "create_lipsync_video", { "text": text });
    return job;
}`
	got, err := migrateSource(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "FEIYING") {
		t.Fatalf("provider const line not deleted:\n%s", got)
	}
	if _, perrs := parser.ParseSource(got, "t.dc"); len(perrs) > 0 {
		t.Fatalf("migrated output failed to reparse: %s (output:\n%s)", perrs[0].Message, got)
	}
}

// Review R1: a provider const that shares its line with another top-level
// declaration must error loudly — the whole-line deletion would otherwise
// silently remove the sibling statement. The error must name the const.
// (`let` is not valid at top level, so the realistic siblings are
// import/const/function declarations.)
func TestMigrateSourceSharedLineConstErrors(t *testing.T) {
	in := `@language nodecoda/1
@mode workflow
import "feiyin_digital_human";
const FEIYING = "feiyin_digital_human"; import "other";

function main(string text) {
    let job = tool(FEIYING, "create_lipsync_video", { "text": text });
    return job;
}`
	_, err := migrateSource(in)
	if err == nil {
		t.Fatal("shared-line provider const must error, got nil")
	}
	if !strings.Contains(err.Error(), "FEIYING") {
		t.Fatalf("error = %q, want it to name the const", err)
	}
}

// Review R1: a trailing line comment on the const's line is trivia — the
// whole-line deletion may take the comment with it.
func TestMigrateSourceTrailingCommentConstDeletes(t *testing.T) {
	in := `@language nodecoda/1
@mode workflow
import "feiyin_digital_human";
const FEIYING = "feiyin_digital_human"; // provider slug

function main(string text) {
    let job = tool(FEIYING, "create_lipsync_video", { "text": text });
    return job;
}`
	got, err := migrateSource(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "FEIYING") {
		t.Fatalf("provider const line not deleted:\n%s", got)
	}
	if _, perrs := parser.ParseSource(got, "t.dc"); len(perrs) > 0 {
		t.Fatalf("migrated output failed to reparse: %s (output:\n%s)", perrs[0].Message, got)
	}
}

// Review R1: a sibling declaration BEFORE the const on the same line is
// also unsafe to delete — error loudly too.
func TestMigrateSourceLeadingSiblingConstErrors(t *testing.T) {
	in := `@language nodecoda/1
@mode workflow
import "feiyin_digital_human";
import "other"; const FEIYING = "feiyin_digital_human";

function main(string text) {
    let job = tool(FEIYING, "create_lipsync_video", { "text": text });
    return job;
}`
	_, err := migrateSource(in)
	if err == nil {
		t.Fatal("const after a sibling declaration on the same line must error, got nil")
	}
}

func TestRewriteTwoArgToolErrors(t *testing.T) {
	in := `function main() {
    let r = tool("tts", { "text": "hi" });
    return r;
}`
	_, err := rewrite(in, nil)
	if err == nil || !strings.Contains(err.Error(), "two-arg") {
		t.Fatalf("err = %v, want two-arg error", err)
	}
}

func TestRewriteNonLiteralProviderErrors(t *testing.T) {
	in := `function main(string query) {
    let r = tool(query, "search", { "q": "hi" });
    return r;
}`
	_, err := rewrite(in, nil)
	if err == nil || !strings.Contains(err.Error(), "string literal or const") {
		t.Fatalf("err = %v, want provider shape error", err)
	}
}

func TestRewriteNonLiteralOperationErrors(t *testing.T) {
	in := `function main() {
    let r = tool("tts", OP, { "text": "hi" });
    return r;
}`
	_, err := rewrite(in, nil)
	if err == nil || !strings.Contains(err.Error(), "operation") {
		t.Fatalf("err = %v, want operation shape error", err)
	}
}

func TestRewriteMultilineParamsPreservesFormatting(t *testing.T) {
	in := `function main(string text) {
    let job = tool("feiyin_digital_human", "create_lipsync_video", {
        "hifly_id": text,
        "speaker_id": "x"
    });
    return job;
}`
	want := `function main(string text) {
    let job = feiyin_digital_human.create_lipsync_video({
        "hifly_id": text,
        "speaker_id": "x"
    });
    return job;
}`
	got, err := rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewriteStatementPositionToolCall(t *testing.T) {
	in := `function main(string err) {
    if (err != "") {
        tool("throw_error", "throw_error", {"error_message": err});
    }
    return "ok";
}`
	want := `function main(string err) {
    if (err != "") {
        throw_error.throw_error({"error_message": err});
    }
    return "ok";
}`
	got, err := rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewritePolicyWrappedCall(t *testing.T) {
	// attempt tool(...) with retry(...): the OperationPolicyExpr wraps the
	// CallExpr; the surgery touches only the call region, the policy suffix
	// survives untouched.
	in := `function main(string query) {
    attempt tool("web_search", "search", {"query": query, "limit": 5}) with retry(max: 2, interval: 1s) as result {
        success { return "ok"; }
        failure (error) { return "failed:" + error.error_message; }
    }
}`
	want := `function main(string query) {
    attempt web_search.search({"query": query, "limit": 5}) with retry(max: 2, interval: 1s) as result {
        success { return "ok"; }
        failure (error) { return "failed:" + error.error_message; }
    }
}`
	got, err := rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewritePolicySuffixCall(t *testing.T) {
	in := `function main() {
    let result = tool("time", "current_time", {"timezone": "UTC"}) with retry(max: 2, interval: 1s);
    return "ok";
}`
	want := `function main() {
    let result = time.current_time({"timezone": "UTC"}) with retry(max: 2, interval: 1s);
    return "ok";
}`
	got, err := rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewriteIdempotent(t *testing.T) {
	in := `function main() {
    let a = tool("tts", "speech_synthesis", { "text": "hi" });
    return a;
}`
	once, err := rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := rewrite(once, nil)
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Fatalf("rewrite is not idempotent: once=%q twice=%q", once, twice)
	}
}

func TestRewriteCJKRuneOffsets(t *testing.T) {
	// CJK comment bytes before the call site shift byte offsets away from
	// rune offsets; the surgery must slice on byte offsets or it corrupts
	// the source (X13 corpus shape).
	in := `@language nodecoda/1
// X13 飞影数字人工作流（provider slug: feiyin_digital_human）
@mode workflow
import "feiyin_digital_human";
const FEIYING = "feiyin_digital_human";
function main(string text) {
    let job = tool(FEIYING, "create_lipsync_video", { "text": text });
    return job;
}`
	want := `@language nodecoda/1
// X13 飞影数字人工作流（provider slug: feiyin_digital_human）
@mode workflow
import "feiyin_digital_human";
const FEIYING = "feiyin_digital_human";
function main(string text) {
    let job = feiyin_digital_human.create_lipsync_video({ "text": text });
    return job;
}`
	got, err := rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewriteNestedLoopBody(t *testing.T) {
	in := `function main(string id) {
    for (round in rounds) {
        let profile = tool("dy", "profile", {"id": id});
        tool("dy", "save", {"records": rec});
    }
    return "ok";
}`
	want := `function main(string id) {
    for (round in rounds) {
        let profile = dy.profile({"id": id});
        dy.save({"records": rec});
    }
    return "ok";
}`
	got, err := rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFindRemainingToolCalls(t *testing.T) {
	// check mode must detect any remaining tool( call, including shapes the
	// codemod refuses (two-arg) and shapes it never touches (comments).
	src := `// tool("a", "b", {}) stays in a comment
function main() {
    let r = tool("tts", { "text": "hi" });
    return r;
}`
	rem := findRemainingToolCalls(src)
	if len(rem) != 1 {
		t.Fatalf("remaining tool( calls = %d, want 1 (comment is not a call): %#v", len(rem), rem)
	}
	clean := `let a = tts.speech_synthesis({ "text": text });`
	if rem := findRemainingToolCalls(clean); len(rem) != 0 {
		t.Fatalf("clean source reports %d remaining calls", len(rem))
	}
}
