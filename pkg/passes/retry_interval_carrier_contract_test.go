package passes

// retry_interval_carrier_contract_test.go —— **重试间隔轴的载体表**判据（residuals R-90）。
//
// 缺陷面（2026-09-25 一手探针，2026-09-30 复核）：作者写 `with retry(max: 2, interval: 5s)`
// 在没有间隔载体的目标上与 `interval: 1s`、乃至省略间隔**逐字节同产物且零诊断** ⇒ 一个
// **语义轴**被静默丢掉（撞 AGENTS.md 不变量「失配 ≠ 静默丢弃」）。
//
// 处置 = 软降级 + 报账（**不拒收**，源码合法、语言裁定未变），实现唯一处 =
// `target_validation.go::reportRetryIntervalAxisDegradations`（表 = `retryIntervalCarrierOf`，
// 值来自 `carrier.go` 的 CarrierProfile）。
//
// 为什么这张表要单独锁（不只是端到端那条契约测试的附庸）：
//   · 端到端只跑得通**已声明**的目标；「未声明目标不发结论」这条**姿态**（R-89 同款）
//     经公开 API 到不了（未知目标名在 `compiler.Compile` 就 UNKNOWN_TARGET 拒了）
//     ⇒ 只能在这里锁，否则它是一句没人判的散文。
//   · 表是**一处定义**：谁改了渲染器载体而没改这张表，本判据 + 端到端契约测试同时红。

import "testing"

func TestRetryIntervalCarrierTable(t *testing.T) {
	cases := []struct {
		name    string
		profile *TargetProfile
		want    RetryIntervalCarrier
	}{
		// 适配器声明什么就是什么（三档全覆盖）。
		{"declared carried", &TargetProfile{Name: "adapter-a", Carrier: CarrierProfile{Declared: true, RetryInterval: RetryIntervalCarried}}, RetryIntervalCarried},
		{"declared absent", &TargetProfile{Name: "adapter-b", Carrier: CarrierProfile{Declared: true, RetryInterval: RetryIntervalAbsent}}, RetryIntervalAbsent},
		{"declared unknown", &TargetProfile{Name: "adapter-c", Carrier: CarrierProfile{Declared: true}}, RetryIntervalUnknown},
		// 缺省 profile = 语言参考载体（DefaultTargetProfile）。
		{"nil profile = language reference carrier", nil, RetryIntervalCarried},
		// 未声明：**不发结论**（既不报也不声称承载）—— 与超时轴同一条有意口径，登记 R-89。
		{"undeclared target", &TargetProfile{Name: "future-target-9.9"}, RetryIntervalUnknown},
		// 过渡桥（P2d 发版批次删除）：未声明 Carrier 的旧 profile 仍按历史名取事实。
		{"legacy name bridge: dify family", &TargetProfile{Name: "ncoda-native-1.0"}, RetryIntervalCarried},
		{"legacy name bridge: biz family", &TargetProfile{Name: "coze-biz-1.0"}, RetryIntervalAbsent},
		{"legacy name bridge: execution family", &TargetProfile{Name: "coze-runtime-1.0"}, RetryIntervalCarried},
	}
	for _, tc := range cases {
		if got := retryIntervalCarrierOf(tc.profile); got != tc.want {
			t.Errorf("%s: retryIntervalCarrierOf = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestDeclaredCarrierWinsOverLegacyName 注入点优先于过渡桥：适配器一旦声明，
// 历史目标名不再参与判定（这是 P2d 删掉 carrierFromName 的前提）。
func TestDeclaredCarrierWinsOverLegacyName(t *testing.T) {
	p := &TargetProfile{
		Name:    "ncoda-native-1.0",
		Carrier: CarrierProfile{Declared: true, RetryInterval: RetryIntervalAbsent},
	}
	if got := retryIntervalCarrierOf(p); got != RetryIntervalAbsent {
		t.Fatalf("declared carrier must win over the legacy name bridge: got %d", got)
	}
}

// TestRetryIntervalReportGuardIsInertWithoutCarrierEvidence 判据自检的一半：
// 报账函数只在 `RetryIntervalAbsent` 上通电 —— 未声明目标（unknown）与承载目标都**不得**
// 让 `retryIntervalChecker` 跑起来（unknown 报了 = 捏造平台事实）。
func TestRetryIntervalReportGuardIsInertWithoutCarrierEvidence(t *testing.T) {
	for _, profile := range []*TargetProfile{
		{Name: "adapter-a", Carrier: CarrierProfile{Declared: true, RetryInterval: RetryIntervalCarried}},
		{Name: "adapter-b", Carrier: CarrierProfile{Declared: true}},
		{Name: "future-target-9.9"},
		nil,
	} {
		if retryIntervalCarrierOf(profile) == RetryIntervalAbsent {
			t.Errorf("目标 %v 被判为无载体 ⇒ 会报账，但它不该在这张表的这一档", profile)
		}
	}
}
