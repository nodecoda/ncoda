package capability

import "testing"

func TestForCoze(t *testing.T) {
	tb := For("coze-studio-flow-1.0")
	if tb == nil || !tb.Condition.RefRef || !tb.Condition.RefLiteral || !tb.JSONSerial || !tb.TextConcat || !tb.TextSplit {
		t.Fatalf("coze table = %+v, want ref×ref+ref×literal native, JSONSerial+TextConcat+TextSplit true", tb)
	}
}

func TestForUnknownConservative(t *testing.T) {
	tb := For("future-platform-9")
	if tb == nil || !tb.Condition.RefLiteral || tb.Condition.RefRef || tb.JSONSerial {
		t.Fatalf("unknown target must degrade to the conservative surface: %+v", tb)
	}
}
