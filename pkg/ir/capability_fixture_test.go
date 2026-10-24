package ir

// capability_fixture_test.go — a *declaring* target for IR tests.
//
// IR tests need a table that declares native nodes to exercise the native
// paths (text concat, json serialize, ref×ref conditions). The fixture is
// deliberately platform-agnostic: the kernel must not know which platform
// happens to own such nodes (ASSET-BOUNDARY D8) — adapters supply that.

import "github.com/nodecoda/ncoda/pkg/capability"

// nativeCapabilityFixture declares every axis native.
func nativeCapabilityFixture(name string) *capability.Table {
	return &capability.Table{
		Target:       name,
		Condition:    capability.Condition{RefLiteral: true, RefRef: true},
		TextConcat:   true,
		TextSplit:    true,
		JSONSerial:   true,
		JSONDeserial: true,
	}
}
