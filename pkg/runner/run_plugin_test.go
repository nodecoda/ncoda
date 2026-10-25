package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/pluginhost"
)

// 进程外插件从 CLI 到符号注册面的回归判据：
//   - 正控：清单合法 ⇒ --list-symbols 能看到 ext_ 符号与 lib/fn 别名；
//   - 正控：源码真的调用到插件进程（rc=0 且值来自 Python 实现）；
//   - 负控：清单声明 header 没有的函数 ⇒ rc=2（用法错，不是运行失败）。

func pythonPlugin(t *testing.T) (manifest string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	server, err := filepath.Abs("../pluginhost/testdata/server.py")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(server); err != nil {
		t.Fatalf("reference server: %v", err)
	}
	dir := t.TempDir()
	manifest = filepath.Join(dir, "ncoda-plugin.yaml")
	body := fmt.Sprintf(`schema: ncoda.plugin/v1
id: test-python
version: 0.1.0
protocol: ncoda-plugin/1
command: ["python3", %q]
libraries:
  - library: array_count
    version: 0.1.0
    fidelity: real
    functions: [array_count]
  - library: common_code_block
    version: 0.1.0
    fidelity: real
    functions: [list_sli_trim, get_one_by_num]
`, server)
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func runCLI(t *testing.T, stdin string, args ...string) (rc int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	rc = Run(strings.NewReader(stdin), &out, &errb, args)
	return rc, out.String(), errb.String()
}

func TestPluginListSymbols(t *testing.T) {
	mf := pythonPlugin(t)
	rc, out, errs := runCLI(t, "", "--plugin", mf, "--tool-path", "../../libraries", "--list-symbols")
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got struct {
		PluginHosts int      `json:"plugin_hosts"`
		Symbols     []string `json:"symbols"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if got.PluginHosts != 1 {
		t.Fatalf("plugin_hosts = %d, want 1", got.PluginHosts)
	}
	for _, needle := range []string{
		"array_count/array_count",
		"array_count.array_count",
		"common_code_block/get_one_by_num",
		"common_code_block/list_sli_trim",
		"ext_test-python_array_count/array_count",
	} {
		found := false
		for _, s := range got.Symbols {
			if s == needle {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("symbol %q missing from %v", needle, got.Symbols)
		}
	}
}

func TestPluginExecutesEndToEnd(t *testing.T) {
	mf := pythonPlugin(t)
	src := `@language nodecoda/1
@mode workflow
import "array_count";
import "common_code_block";

function main() -> any {
    let a = array_count.array_count({ "array": ["a", "b", "c"] });
    let b = common_code_block.get_one_by_num({ "list": ["x", "y", "z"], "end": true });
    return { "count": a.count, "last": b.item };
}
`
	srcPath := filepath.Join(t.TempDir(), "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--plugin", mf, "--tool-path", "../../libraries", srcPath)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if got["count"] != float64(3) || got["last"] != "z" {
		t.Fatalf("result = %v, want count=3 last=z", got)
	}
}

func TestPluginRejectsUndeclaredFunction(t *testing.T) {
	mf := pythonPlugin(t)
	body, err := os.ReadFile(mf)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(body), "functions: [array_count]", "functions: [array_count, no_such_fn]", 1)
	badPath := filepath.Join(filepath.Dir(mf), "bad.yaml")
	if err := os.WriteFile(badPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, _, errs := runCLI(t, "", "--plugin", badPath, "--tool-path", "../../libraries", "--list-symbols")
	if rc != 2 {
		t.Fatalf("rc=%d, want 2 (usage); stderr=%s", rc, errs)
	}
	if !strings.Contains(errs, "not declared in the header") {
		t.Fatalf("stderr = %s", errs)
	}
}

// capcut_data 试点：仓库内 plugins/ 目录必须能直接驱动真实语言链。
// 正控覆盖清单里声明为 real 的 7 个函数；负控证明未实现的函数确实 unbound
// （不许插件用空值假装实现）。
func TestCapcutDataPluginEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	src := `@language nodecoda/1
@mode workflow
import "capcut_data";

function main() -> any {
    let t = capcut_data.timelines({ "duration": 5000000, "num": 3 });
    let u = capcut_data.get_url({ "output": "https://example.com/a.png" });
    let direct = u.$output;
    let one = u.outputList;
    let us = [one];
    let l = capcut_data.objs_to_str_list({ "outputs": us });
    let s = capcut_data.str_to_list({ "obj": "x" });
    let m = capcut_data.wenan_links_merge({ "link1": ["a1", "a2"], "link2": ["b1"] });
    let p = capcut_data.array_pair({ "items": ["f0", "e0", "f1", "e1"] });
    let f = capcut_data.extract_first_end_frame({ "items": ["f0", "e0"] });
    return { "direct": direct, "timelines": t.timelines, "all": t.all_timelines, "urls": l.infos,
             "single": s.infos, "links": m.links_list, "groups": p.total_groups,
             "first": f.first_item, "end": f.end_item };
}
`
	srcPath := filepath.Join(t.TempDir(), "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--plugin-dir", "../../plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got struct {
		Direct    string                       `json:"direct"`
		All       []struct{ Start, End int64 } `json:"all"`
		Timelines []struct{ Start, End int64 } `json:"timelines"`
		Urls      []string                     `json:"urls"`
		Single    []string                     `json:"single"`
		Links     []struct{ Links []string }   `json:"links"`
		Groups    int                          `json:"groups"`
		First     string                       `json:"first"`
		End       string                       `json:"end"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if got.Direct != "https://example.com/a.png" {
		t.Fatalf("u.$output = %q (wire key must be the port name, not \"$output\")", got.Direct)
	}
	if len(got.All) != 1 || got.All[0].Start != 0 || got.All[0].End != 5000000 {
		t.Fatalf("all_timelines = %+v", got.All)
	}
	if len(got.Timelines) != 3 || got.Timelines[2].End != 5000000 {
		t.Fatalf("timelines = %+v", got.Timelines)
	}
	if len(got.Urls) != 1 || got.Urls[0] != "https://example.com/a.png" {
		t.Fatalf("urls = %v", got.Urls)
	}
	if len(got.Single) != 1 || got.Single[0] != "x" {
		t.Fatalf("single = %v", got.Single)
	}
	if len(got.Links) != 2 || len(got.Links[0].Links) != 2 || len(got.Links[1].Links) != 1 {
		t.Fatalf("links = %+v", got.Links)
	}
	if got.Groups != 2 || got.First != "f0" || got.End != "e0" {
		t.Fatalf("groups=%d first=%q end=%q", got.Groups, got.First, got.End)
	}
}

func TestCapcutDataUnboundFunctionStaysRed(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	src := `@language nodecoda/1
@mode workflow
import "capcut_data";

function main() -> string {
    let v = capcut_data.caption_infos({ "texts": ["a"], "font_size": 8 });
    return v.infos;
}
`
	srcPath := filepath.Join(t.TempDir(), "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--plugin-dir", "../../plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 1 {
		t.Fatalf("rc=%d, want 1 (unbound symbol); stdout=%s stderr=%s", rc, out, errs)
	}
	if !strings.Contains(errs, "RBINDING_MISSING") {
		t.Fatalf("stderr = %s", errs)
	}
}

// text_toolkit：7 个纯文本/数组库必须能从仓库内 plugins/ 直接驱动。
// 正控覆盖每个库至少一条调用；负控证明契约错误以 rc=1 暴露，而不是静默空值。
func TestTextToolkitPluginEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	src := `@language nodecoda/1
@mode workflow
import "string_to_array";
import "link_extract";
import "url_get";
import "text_split_master";
import "text_split_tool";
import "null_fill";
import "array_split_merge";

function main() -> any {
    let s = string_to_array.string_2_array({ "content": "a\nb\nc" });
    let l = link_extract.extract_all_links({ "text": "看 https://a.com/x，还有 http://b.cn/y。" });
    let f = link_extract.extract_first_link({ "text": "前 https://a.com/p 后" });
    let u = url_get.get_url({ "content": "https://a.com/i.png" });
    let m = text_split_master.TextSplittingMaster({ "input": "第一段。\n\n第二段。" });
    let chunks = m.$output;
    let ok = s.$code == 0;
    let p = text_split_tool.Paragraph_splitting({ "text": "甲\n\n乙" });
    let n = null_fill.null_value_filling({ "array": ["a"] });
    let g = array_split_merge.Str_list({ "Str_list": ["aa", "bb", "cc"], "num": 4 });
    let j = array_split_merge.Str_merge({ "Str_list1": ["a1", "a2"], "Str_list2": ["b1"] });
    return { "ok": ok, "data": s.data, "links": l.data, "first": f.data, "url": u.url,
             "chunks": chunks, "segments": p.segments, "filled": n.filled_data,
             "g1": g.Str_list1, "g2": g.Str_list2, "all": j.All_Str_list, "groups": j.Split_str };
}
`
	srcPath := filepath.Join(t.TempDir(), "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--plugin-dir", "../../plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got struct {
		OK       bool     `json:"ok"`
		Data     []string `json:"data"`
		Links    []string `json:"links"`
		First    string   `json:"first"`
		URL      string   `json:"url"`
		Chunks   []string `json:"chunks"`
		Segments []string `json:"segments"`
		Filled   []string `json:"filled"`
		G1       []string `json:"g1"`
		G2       []string `json:"g2"`
		All      []string `json:"all"`
		Groups   []struct {
			StrList []string `json:"Str_list"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if !got.OK {
		t.Fatalf("$code != 0: %s", out)
	}
	if len(got.Data) != 3 || got.Data[2] != "c" {
		t.Fatalf("data = %v", got.Data)
	}
	if len(got.Links) != 2 || got.Links[0] != "https://a.com/x" || got.Links[1] != "http://b.cn/y" {
		t.Fatalf("links = %v", got.Links)
	}
	if got.First != "https://a.com/p" || got.URL != "https://a.com/i.png" {
		t.Fatalf("first=%q url=%q", got.First, got.URL)
	}
	if len(got.Chunks) != 2 {
		t.Fatalf("chunks = %v", got.Chunks)
	}
	if len(got.Segments) != 2 || got.Segments[1] != "乙" {
		t.Fatalf("segments = %v", got.Segments)
	}
	if len(got.Filled) != 1 || got.Filled[0] != "a" {
		t.Fatalf("filled = %v", got.Filled)
	}
	if len(got.G1) != 2 || len(got.G2) != 1 || got.G2[0] != "cc" {
		t.Fatalf("g1=%v g2=%v", got.G1, got.G2)
	}
	if len(got.All) != 3 || got.All[2] != "b1" {
		t.Fatalf("all = %v", got.All)
	}
	if len(got.Groups) != 2 || len(got.Groups[0].StrList) != 2 {
		t.Fatalf("groups = %+v", got.Groups)
	}
}

func TestTextToolkitInvalidArgsStaysRed(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	src := `@language nodecoda/1
@mode workflow
import "null_fill";

function main() -> any {
    let v = null_fill.null_value_filling({ "array": "not-an-array" });
    return v.filled_data;
}
`
	srcPath := filepath.Join(t.TempDir(), "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--plugin-dir", "../../plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 1 {
		t.Fatalf("rc=%d, want 1; stdout=%s stderr=%s", rc, out, errs)
	}
	// 静态类型检查先于插件调用：错类型必须是 rc=1 的 TYPE_MISMATCH，
	// 不能被插件用空值兜底成 rc=0。
	if !strings.Contains(errs, "TYPE_MISMATCH") || !strings.Contains(errs, "null_fill") {
		t.Fatalf("stderr = %s", errs)
	}
}

// compose_toolkit：剪映/速创时间轴纯函数必须能由仓库内插件驱动。
// 注意：多字段 record 不能直接进数组字面量（N-LANG-5），这里先绑变量。
func TestComposeToolkitPluginEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	src := `@language nodecoda/1
@mode workflow
import "capcut_data";
import "capcut_helper";
import "suchuang_video_compose";

function main() -> any {
    let t = capcut_helper.text_splitter({ "text": "a。b。" });
    let segs = t.segments;
    // 时间轴记录数组只能来自库调用（N-LANG-5：record 不能进数组字面量）。
    let pt = capcut_data.timelines({ "duration": 100, "num": 1 });
    let pre = pt.timelines;
    let mt0 = capcut_data.timelines({ "duration": 50, "num": 1 });
    let main_tl = mt0.timelines;
    let m = capcut_helper.timeline_merge({ "pre_timeline": pre, "main_timeline": main_tl, "gap_us": 10.0 });
    let mts = m.main_timelines;
    let last = m.last_end_us;
    let w = capcut_helper.wenan_timeline_range({ "timelines": mts, "wenan": ["x"] });
    let wt = w.wenanTimeline;
    let lc = capcut_helper.audio_link_collector({ "outputList": ["a.mp3", "b.mp3"] });
    let links = lc.links;
    let r = capcut_helper.rolling_effect({ "duration_list": [100, 200], "str_list": ["s"] });
    let rt = r.timelines;

    let st = suchuang_video_compose.text_splitter({ "text": "x。y。" });
    let ss = st.segments;
    let so = st.outputList;
    let te = suchuang_video_compose.text_empty_removal({ "text": ["a", "", "b"] });
    let tw = te.wenan;
    let al = suchuang_video_compose.add_list({ "add_list": ["a"] });
    let list = al.list;
    let one = capcut_data.timelines({ "duration": 100, "num": 1 });
    let mt = suchuang_video_compose.media_tile({ "all_timeline": one.all_timelines, "media_url": "u", "duration": 50.0 });
    let mtl = mt.timelines;
    let mu = mt.urls;
    let tg = suchuang_video_compose.timeline_generator({ "duration": "1", "items": ["a", "b"] });
    let tt = tg.timelines;
    let ds = suchuang_video_compose.dialog_split({ "roles": "Q|A", "dialogs": ["Q: q\nA: a"] });
    let rd = ds.roleDialogs;
    let dm = suchuang_video_compose.dialog_merge({ "audioLinks": { "Q": ["q.mp3"], "A": ["a.mp3"] }, "roleDialogs": rd });
    let ml = dm.mergedAudioLinks;
    let dw = capcut_data.timelines({ "duration": 100, "num": 2 });
    let dt = suchuang_video_compose.dialog_wenan_timeline({ "roleDialogs": rd, "timelines": dw.timelines });
    let rwt = dt.roleWenanTimeline;
    return { "segs": segs, "mts": mts, "last": last, "wt": wt, "links": links,
             "rt": rt, "ss": ss, "so": so, "tw": tw, "list": list,
             "mtl": mtl, "mu": mu, "tt": tt, "roles": rd, "ml": ml, "rwt": rwt };
}
`
	srcPath := filepath.Join(t.TempDir(), "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--plugin-dir", "../../plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got struct {
		Segs []string                     `json:"segs"`
		Mts  []struct{ Start, End int64 } `json:"mts"`
		Last int64                        `json:"last"`
		WT   []struct {
			Content    string
			Start, End int64
		} `json:"wt"`
		Links []string                     `json:"links"`
		RT    []struct{ Start, End int64 } `json:"rt"`
		SS    []string                     `json:"ss"`
		SO    []string                     `json:"so"`
		TW    []string                     `json:"tw"`
		List  []string                     `json:"list"`
		MTL   []struct{ Start, End int64 } `json:"mtl"`
		MU    []string                     `json:"mu"`
		TT    []struct{ Start, End int64 } `json:"tt"`
		Roles []struct {
			Role    string `json:"role"`
			Dialogs []struct {
				Content string `json:"content"`
				Idx     int64  `json:"idx"`
			} `json:"dialogs"`
		} `json:"roles"`
		ML  []string `json:"ml"`
		RWT []struct {
			Role     string                       `json:"role"`
			Wenan    []string                     `json:"wenan"`
			Timeline []struct{ Start, End int64 } `json:"timelines"`
		} `json:"rwt"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if len(got.Segs) != 2 {
		t.Fatalf("segs = %v", got.Segs)
	}
	if len(got.Mts) != 1 || got.Mts[0].Start != 110 || got.Mts[0].End != 160 {
		t.Fatalf("main_timelines = %+v", got.Mts)
	}
	if got.Last != 160 {
		t.Fatalf("last_end_us = %d", got.Last)
	}
	if len(got.WT) != 1 || got.WT[0].Content != "x" || got.WT[0].Start != 110 {
		t.Fatalf("wenanTimeline = %+v", got.WT)
	}
	if len(got.Links) != 2 || got.Links[1] != "b.mp3" {
		t.Fatalf("links = %v", got.Links)
	}
	if len(got.RT) != 2 || got.RT[1].End != 300 {
		t.Fatalf("rolling timelines = %+v", got.RT)
	}
	if len(got.SS) != 2 || len(got.SO) != 2 {
		t.Fatalf("suchuang segments=%v outputList=%v", got.SS, got.SO)
	}
	if len(got.TW) != 2 || got.TW[0] != "a" {
		t.Fatalf("text_empty_removal = %v", got.TW)
	}
	if len(got.List) != 1 || got.List[0] != "a" {
		t.Fatalf("add_list = %v", got.List)
	}
	if len(got.MTL) != 2 || len(got.MU) != 2 {
		t.Fatalf("media_tile timelines=%v urls=%v", got.MTL, got.MU)
	}
	if len(got.TT) != 2 || got.TT[1].End != 2_000_000 {
		t.Fatalf("timeline_generator = %+v", got.TT)
	}
	if len(got.Roles) != 2 || len(got.Roles[0].Dialogs) != 1 || got.Roles[0].Dialogs[0].Idx != 0 {
		t.Fatalf("roleDialogs = %+v", got.Roles)
	}
	if len(got.ML) != 2 || got.ML[0] != "q.mp3" || got.ML[1] != "a.mp3" {
		t.Fatalf("mergedAudioLinks = %v", got.ML)
	}
	if len(got.RWT) != 2 || len(got.RWT[0].Wenan) != 1 || len(got.RWT[0].Timeline) != 1 {
		t.Fatalf("roleWenanTimeline = %+v", got.RWT)
	}
}

// 草稿状态机/音频探测保持 unbound：插件不许用空值假装实现。
func TestComposeToolkitStateMachineStaysRed(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	src := `@language nodecoda/1
@mode workflow
import "suchuang_video_compose";

function main() -> any {
    let v = suchuang_video_compose.audio_timelines({ "links": ["a.mp3"] });
    return v.timelines;
}
`
	srcPath := filepath.Join(t.TempDir(), "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--plugin-dir", "../../plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 1 {
		t.Fatalf("rc=%d, want 1; stdout=%s stderr=%s", rc, out, errs)
	}
	if !strings.Contains(errs, "RBINDING_MISSING") || !strings.Contains(errs, "audio_timelines") {
		t.Fatalf("stderr = %s", errs)
	}
}

// 第二轮补齐的宿主判据：codel_toolkit 的纯列表函数从 CLI 真的走到子进程。
func TestCodelToolkitPluginEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	src := `@language nodecoda/1
@mode workflow
import "codel_video";

function main() -> any {
    let s = codel_video.split_list({ "size": 2, "text_list": ["a", "b", "c"] });
    let m = codel_video.merge_list({ "audio_list1": ["x"], "audio_list3": ["y"] });
    return { "first": s.result.text_list1, "second": s.result.text_list2, "all": m.all_list };
}
`
	srcPath := filepath.Join(dir, "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--plugin-dir", "../../plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got struct {
		First  []string `json:"first"`
		Second []string `json:"second"`
		All    []string `json:"all"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if strings.Join(got.First, ",") != "a,b" || strings.Join(got.Second, ",") != "c" {
		t.Fatalf("split_list = %+v", got)
	}
	if strings.Join(got.All, ",") != "x,y" {
		t.Fatalf("merge_list = %+v", got)
	}
}

// capcut_data.timelines_format 与 suchuang_video_compose.timeline_align 的宿主判据。
// 输入时间轴来自库调用（避开 N-LANG-5：多字段 record 不能直接进数组字面量）。
func TestTimelineFormatAndAlignPluginEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	src := `@language nodecoda/1
@mode workflow
import "capcut_data";
import "suchuang_video_compose";

function main() -> any {
    let base = capcut_data.timelines({ "duration": 1000000, "num": 2 });
    let f = capcut_data.timelines_format({ "timelines": base.timelines });
    let a = suchuang_video_compose.timeline_align({ "align": "end", "timelines": base.timelines });
    return { "texts": f.all_formatted_texts, "formatted": f.all_timelines,
             "aligned": a.align_timelines, "all": a.all_timeline };
}
`
	srcPath := filepath.Join(dir, "app.ncoda")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runCLI(t, "", "--plugin-dir", "../../plugins", "--tool-path", "../../libraries", srcPath)
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got struct {
		Texts     []string                     `json:"texts"`
		Formatted []struct{ Start, End int64 } `json:"formatted"`
		Aligned   []struct{ Start, End int64 } `json:"aligned"`
		All       []struct{ Start, End int64 } `json:"all"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if len(got.Texts) != 2 || len(got.Formatted) != 2 || got.Formatted[1].End != 1000000 {
		t.Fatalf("timelines_format = %+v", got)
	}
	if len(got.Aligned) != 2 || got.Aligned[0].End != 500000 || got.Aligned[1].End != 1000000 {
		t.Fatalf("timeline_align = %+v", got)
	}
	if len(got.All) != 1 || got.All[0].Start != 0 || got.All[0].End != 1000000 {
		t.Fatalf("all_timeline = %+v", got.All)
	}
}

// AC3/AC7 的符号面判据：发行仓里每个插件清单声明的符号，都必须真的出现在
// --list-symbols 里（lib/fn 别名 + lib.fn 本名），且 host 数与清单数一致。
func TestShippedPluginsExposeAllDeclaredSymbols(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	hdrs, err := pluginhost.LoadHeaders([]string{"../../libraries"})
	if err != nil {
		t.Fatalf("LoadHeaders: %v", err)
	}
	paths, err := pluginhost.Discover("../../plugins")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no shipped plugin manifests")
	}
	var want []string
	for _, path := range paths {
		m, err := pluginhost.Load(path, hdrs)
		if err != nil {
			t.Fatalf("Load(%s): %v", path, err)
		}
		want = append(want, m.Symbols()...)
	}
	rc, out, errs := runCLI(t, "", "--plugin-dir", "../../plugins", "--tool-path", "../../libraries", "--list-symbols")
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errs)
	}
	var got struct {
		PluginHosts int      `json:"plugin_hosts"`
		Symbols     []string `json:"symbols"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out)
	}
	if got.PluginHosts != len(paths) {
		t.Fatalf("plugin_hosts = %d, want %d", got.PluginHosts, len(paths))
	}
	set := map[string]bool{}
	for _, s := range got.Symbols {
		set[s] = true
	}
	for _, sym := range want {
		lib, fn, ok := strings.Cut(sym, ".")
		if !ok {
			t.Fatalf("bad manifest symbol %q", sym)
		}
		if !set[lib+"/"+fn] || !set[sym] {
			t.Fatalf("declared symbol %q missing from --list-symbols", sym)
		}
	}
}
