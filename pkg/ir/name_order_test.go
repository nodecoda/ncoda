package ir

import (
	"fmt"
	"sort"
	"testing"
)

// naturalLess is the ordering contract of the merge-group emission order: the
// aggregated slot names carry the group's wire index, so the lowering must
// reproduce 0,1,2,…,10,11 rather than "0","1","10","11","2" (see
// platforms/cozebiz/decompile/agg_slot_emission_order_contract_test.go).
func TestNaturalLessOrdersGeneratedSlots(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"g2", "g10", true},
		{"g10", "g2", false},
		{"g9", "g10", true},
		{"g0", "g1", true},
		{"g7", "g07", true},  // digit-run tie falls back to the shorter name
		{"g07", "g7", false}, //
		{"g7", "g7", false},  // equal
		{aggName(1, 2), aggName(1, 10), true},
		{aggName(1, 10), aggName(2, 2), true}, // first difference wins
		{"a2b10", "a2b9", false},
		{"a2b10", "a10b2", true}, // earlier run decides
		{"g", "g1", true},        // prefix is smaller
		{"g1", "g", false},
	}
	for _, tc := range cases {
		if got := naturalLess(tc.a, tc.b); got != tc.want {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		// anti-symmetry
		if naturalLess(tc.a, tc.b) && naturalLess(tc.b, tc.a) {
			t.Errorf("naturalLess is not antisymmetric on (%q, %q)", tc.a, tc.b)
		}
	}
}

// aggName 构造有界名形态 `agg_v<V>_g<G>`：这是测试**输入的域**，不是对产物的观测。
func aggName(v, g int) string { return fmt.Sprintf("agg_v%d_g%d", v, g) }

func TestSortNamesNaturalMatchesSlotOrder(t *testing.T) {
	// 名字按**形态**构造：输入的域 = 有界名 `agg_v<N>_g<M>`（契约），
	// 具体实例不是契约（序号会随压实/id 位移变化）⇒ 不逐字面量罗列。
	scrambled := []int{10, 2, 11, 0, 1, 3, 4, 5, 6, 7, 8, 9}
	names := make([]string, 0, len(scrambled))
	for _, g := range scrambled {
		names = append(names, aggName(1, g))
	}
	sortNamesNatural(names)
	want := make([]string, 0, len(scrambled))
	for g := 0; g <= 11; g++ {
		want = append(want, aggName(1, g))
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("sortNamesNatural order = %v, want %v", names, want)
		}
	}
	// total order: sorting must be deterministic and idempotent
	again := append([]string{}, want...)
	sortNamesNatural(again)
	for i := range want {
		if again[i] != want[i] {
			t.Fatalf("sortNamesNatural is not idempotent: %v", again)
		}
	}
	// plain string order is what the bug looked like — keep the contrast visible
	plain := append([]string{}, names...)
	sort.Strings(plain)
	if plain[0] == names[0] && plain[11] == names[11] {
		t.Fatalf("expected plain string order to differ from natural order, got %v", plain)
	}
}
