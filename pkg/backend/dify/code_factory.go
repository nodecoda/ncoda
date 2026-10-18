package dify

import (
	"fmt"
	"strings"
)

// makeCodeNode mirrors node_factory.py make_code_node (:318-328).
func MakeCodeNode(nodeID, code string, outputs map[string]OutputDefNode, variables []VariableSelectorNode) NodeDecl {
	if variables == nil {
		variables = []VariableSelectorNode{}
	}
	data := &CodeDataNode{
		Code:         code,
		CodeLanguage: "python3",
		Variables:    variables,
		Outputs:      outputs,
	}
	return MakeNodeDecl(nodeID, "code", data)
}

// JSONDumpsString ports Python json.dumps(s, ensure_ascii=True), the
// string rendering the code materializer embeds into generated code
// (lowering/code_materializer.go:684-718).
func JSONDumpsString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			switch {
			case r >= 0x20 && r <= 0x7e:
				b.WriteRune(r)
			case r <= 0xFFFF:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
