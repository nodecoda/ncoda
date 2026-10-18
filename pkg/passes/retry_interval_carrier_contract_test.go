package passes

// retry_interval_carrier_contract_test.go —— **重试间隔轴的载体表**判据（residuals R-90）。
//
// 缺陷面（2026-09-25 一手探针，2026-09-30 复核）：作者写 `with retry(max: 2, interval: 5s)`
// 在 coze 家族上与 `interval: 1s`、乃至省略间隔**逐字节同产物且零诊断** ⇒ 一个**语义轴**
// 被静默丢掉（撞 AGENTS.md 不变量「失配 ≠ 静默丢弃」）。
//
// 处置 = 软降级 + 报账（**不拒收**，源码合法、语言裁定未变），实现唯一处 =
// `target_validation.go::reportRetryIntervalAxisDegradations`（表 = `retryIntervalCarrierOf`）。
//
// 为什么这张表要单独锁（不只是端到端那条契约测试的附庸）：
//   · 端到端只跑得通**三个**真实目标名；「表外目标不发结论」这条**姿态**（R-89 同款）
//     经公开 API 到不了（未知目标名在 `compiler.Compile` 就 UNKNOWN_TARGET 拒了）
//     ⇒ 只能在这里锁，否则它是一句没人判的散文。
//   · 表是**一处定义**：谁改了渲染器载体而没改这张表，本判据 + 端到端契约测试同时红。

import "testing"

func TestRetryIntervalCarrierTable(t *testing.T) {
	cases := []struct {
		name    string
		profile *TargetProfile
		want    retryIntervalCarrier
	}{
		// 承载：载体真的消费该轴（dify `retry_interval` / runtime 的执行器 sleep）。
		{"dify", &TargetProfile{Name: "dify-1.16-graphon-0.6"}, retryIntervalCarried},
		{"nil profile = dify 缺省", nil, retryIntervalCarried},
		{"runtime 执行目标", &TargetProfile{Name: "coze-runtime-1.0"}, retryIntervalCarried},
		// 无载体：平台立即重试（重试循环内无 sleep）⇒ 报账。
		{"coze-biz", &TargetProfile{Name: "coze-biz-1.0"}, retryIntervalAbsent},
		{"coze-studio flow", &TargetProfile{Name: "coze-studio-flow-1.0"}, retryIntervalAbsent},
		{"coze-1.x", &TargetProfile{Name: "coze-1.0"}, retryIntervalAbsent},
		// 表外：**不发结论**（既不报也不声称承载）—— 与超时轴同一条有意口径，登记 R-89。
		{"表外目标", &TargetProfile{Name: "future-target-9.9"}, retryIntervalUnknown},
	}
	for _, tc := range cases {
		if got := retryIntervalCarrierOf(tc.profile); got != tc.want {
			t.Errorf("%s: retryIntervalCarrierOf = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestRetryIntervalReportGuardIsInertWithoutCarrierEvidence 判据自检的一半：
// 报账函数只在 `retryIntervalAbsent` 上通电 —— 表外目标（unknown）与承载目标都**不得**
// 让 `retryIntervalChecker` 跑起来（unknown 报了 = 捏造平台事实）。
func TestRetryIntervalReportGuardIsInertWithoutCarrierEvidence(t *testing.T) {
	for _, profile := range []*TargetProfile{
		{Name: "dify-1.16-graphon-0.6"},
		{Name: "coze-runtime-1.0"},
		{Name: "future-target-9.9"},
		nil,
	} {
		if retryIntervalCarrierOf(profile) == retryIntervalAbsent {
			t.Errorf("目标 %v 被判为无载体 ⇒ 会报账，但它不该在这张表的这一档", profile)
		}
	}
}
