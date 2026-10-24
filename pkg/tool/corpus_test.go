// Package tool defines platform-neutral external Tool capability contracts.
package tool

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCapabilityHeaderRealCorpus is the public-library regression: it loads
// the open tree's libraries/ directory and asserts the registry did not
// silently shrink, plus spot-checks key shapes. Skipped when the headers are
// absent (e.g. a source tarball without libraries/).
func TestCapabilityHeaderRealCorpus(t *testing.T) {
	corpus := filepath.Join("..", "..", "libraries")
	if _, err := os.Stat(corpus); err != nil {
		t.Skipf("corpus not present: %s", corpus)
	}
	registry, err := LoadCapabilityDir(corpus)
	if err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	// **下界，不是精确值**（2026-09-30 改；改因是一手红，不是口味）。
	//
	// 本断言守的**唯一缺陷方向** = 「注册表静默缩水」（加载器漏文件 / 解析失败被吞）。
	// 精确钉死把正常增长也变成红 ⇒ 退化成**指纹型判据**（改实现 ⇒ 判据过期，修法只是
	// 重钉数字；同类已于 2026-09-19 按 D1 退役）。实证：`d33f1671` 补
	// `capabilities-ncoda/database.ncoda`（+1 头文件 / +4 op）时没人重钉本值
	// ⇒ 基线写 293/152，事实 297/153，**红了两天无人看见**（2026-09-30 复核时才发现）。
	// 假红比不判更坏：它会训练人绕过判据。下界棘轮「只增不减」，与仓内其它棘轮同形。
	//
	// 锚计数来由（保留原作者的点名）：2026-09-01 盲测补 plugin_<id> 9 头文件 +
	// video_compose_toolkit 补 2 op + 增量 7 + video_audio_extractor + S6b
	// bing_search/video_url_get；2026-09-18 语料闭集补齐 plugin_7513599695369945107 的 3 op
	// —— 该 pluginID 不在 slug 表，兜底域名不带身份时语料整源 DECOMPILE_FAIL/RS-REG-002。
	// 当前事实（2026-09-30 实测）：154 头文件 / 299 定义。
	// 2026-10-25：头文件随公共库迁入开源仓 `libraries/`，实测 174 文件 / 339 定义。
	if len(definitions) < 339 {
		t.Errorf("definition count = %d, want >= 339 (下界棘轮：注册表不得静默缩水；锚计数与来由见上)", len(definitions))
	}
	checks := map[string]bool{
		"tts/speech_synthesis":                            false,
		"feishu_docs/create_document":                     false,
		"xhs_article_extract/extract_xhs_article_webinfo": false,
	}
	renamedLeaf := false
	for _, d := range definitions {
		key := d.Provider + "/" + d.Operation
		if _, ok := checks[key]; ok {
			checks[key] = true
		}
		for _, out := range d.Outputs {
			if out.Name == "data.type" {
				renamedLeaf = true
			}
		}
	}
	for key, found := range checks {
		if !found {
			t.Errorf("corpus missing definition %s", key)
		}
	}
	if !renamedLeaf {
		t.Error("corpus missing physical reserved-word leaf data.type ($ 转义在文本面，IR 路径即物理名)")
	}
	// P3.3 §16.5: the extracted @capability annotations land on the real
	// corpus — every definition carries one of the three policy bits, and
	// the timeout ceiling of the capcut family is locked.
	annotated := 0
	withTimeout := 0
	for _, d := range definitions {
		if d.SupportsRetry || d.SupportsFailureBranch || d.TimeoutMS > 0 {
			annotated++
		}
		if d.TimeoutMS > 0 {
			withTimeout++
		}
	}
	// 180 → 181（2026-10-17）：image_generate 的内置节点能力头补
	// `@capability retry=true, failure_branch=true` —— 平台语料里同型号
	// 节点实际带 settingOnError processType 3/1（新建语料批次
	// corpus-inbox/2026-10-17-github 的 7576926169774653449 节点 1175150/155753），
	// 此前缺注释导致反编译出的 `attempt` 被 OPERATION_POLICY 拒绝。
	if annotated != 181 {
		t.Errorf("annotated definitions = %d/293, want 181 (cs 版采集缺口: 新补 plugin 未带 @capability, 待采集补全; P3.3 §16.5 —— 180→181 = 2026-10-17 补 image_generate 的 retry/failure_branch)", annotated)
	}
	if withTimeout == 0 {
		t.Error("no definition carries a timeout_ms ceiling")
	}
	capcut, ok := registry.Lookup("capcut_data", "audio_infos")
	if !ok || capcut.TimeoutMS != 180000 || !capcut.SupportsFailureBranch {
		t.Errorf("capcut_data/audio_infos policies = %+v, want failure_branch + timeout_ms=180000", capcut)
	}
	// R-29（2026-09-24 用户裁定「收紧」）：仓内 293 个 op **全部**声明了输出表
	// —— 要么 `-> T`（契约记录），要么 `-> void`（显式空表，如定时器）。缺输出
	// 表 = 调用点拒收（CAPABILITY_OUTPUTS_UNDECLARED）⇒ 人口普查必须是 0，
	// 否则「头文件是权威」这条在真实数据上就不成立。
	undeclared := 0
	for _, d := range definitions {
		if d.OutputsUndeclared {
			t.Errorf("capability %s.%s declares no output table; write `-> T` or `-> void` (R-29)",
				d.Provider, d.Operation)
			undeclared++
		}
	}
	if undeclared != 0 {
		t.Errorf("undeclared output tables = %d, want 0", undeclared)
	}
	t.Logf("loaded %d definitions from %d header files; %d annotated; %d undeclared output tables (want 0)",
		len(definitions), countHeaderFiles(corpus), annotated, undeclared)
}

func countHeaderFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > len(CapabilityHeaderExt) &&
			e.Name()[len(e.Name())-len(CapabilityHeaderExt):] == CapabilityHeaderExt {
			n++
		}
	}
	return n
}
