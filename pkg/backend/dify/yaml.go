package dify

// yaml.go — byte-parity port of the PyYAML 6.0.3 emitter for the
// SerializeToDict output. The reference is serializer.py serialize (:1111-1115):
//
//	rendered = pyyaml.dump(serialize_to_dict(ast), allow_unicode=True,
//	                       default_flow_style=False, sort_keys=False)
//
// PyYAML's emitter (emitter.py, installed in <reference-venv>) is
// ported directly here: 2-space indentation, block sequences whose dash
// aligns with the parent key, flow `[]`/`{}` for empty containers, pyyaml's
// scalar quoting analysis (analyze_scalar + choose_scalar_style), the
// implicit-resolver quoting check (resolver.py), 80-column wrapping
// (best_width) with pyyaml's exact column accounting, and the single/double
// quoted writers' break and escape rules. Plain map[string]any values are
// emitted with SORTED keys (Go map iteration is random — determinism hard
// constraint); this mirrors the Task 26 normalizeAny deviation.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// SerializeYAML renders the SerializeToDict output as YAML text with byte
// parity to Python serializer.py serialize (:1111-1115) for the same AST.
func SerializeYAML(om *OrderedMap) (string, error) {
	if om == nil {
		return "", fmt.Errorf("yaml: nil document")
	}
	w := &yamlWriter{bestWidth: 80, allowUnicode: true, whitespace: true, indention: true}
	if err := w.emitRoot(om); err != nil {
		return "", err
	}
	return w.b.String(), nil
}

// ── Emitter state (emitter.py Emitter) ─────────────────────────────

type yamlWriter struct {
	b            strings.Builder
	col          int  // output column in runes (pyyaml column counts codepoints)
	whitespace   bool // last written char was whitespace / line start
	indention    bool
	bestWidth    int  // pyyaml best_width (default 80)
	allowUnicode bool // pyyaml allow_unicode (default True here)
}

func (w *yamlWriter) writeLineBreak(data string) {
	if data == "" {
		data = "\n"
	}
	w.whitespace = true
	w.indention = true
	w.col = 0
	w.b.WriteString(data)
}

// writeIndent ports Emitter.write_indent (emitter.py:864-880): newline when
// needed, then spaces up to the indent column.
func (w *yamlWriter) writeIndent(indent int) {
	if !w.indention || w.col > indent || (w.col == indent && !w.whitespace) {
		w.writeLineBreak("")
	}
	if w.col < indent {
		w.whitespace = true
		w.b.WriteString(strings.Repeat(" ", indent-w.col))
		w.col = indent
	}
}

// writeIndicator ports Emitter.write_indicator (emitter.py:846-862).
func (w *yamlWriter) writeIndicator(ind string, needWhitespace bool, whitespace bool, indention bool) {
	var data string
	if w.whitespace || !needWhitespace {
		data = ind
	} else {
		data = " " + ind
	}
	w.whitespace = whitespace
	w.indention = w.indention && indention
	w.col += len(data)
	w.b.WriteString(data)
}

func (w *yamlWriter) writeRunes(rs []rune) {
	if len(rs) == 0 {
		return
	}
	w.b.WriteString(string(rs))
	w.col += len(rs)
}

// ── Node emission (recursive, mirrors expect_node/expect_block_*) ──

// emitRoot emits the document (a mapping whose keys are at column 0) plus the
// trailing newline written by expect_document_end (:468-478). A zero-key root
// follows PyYAML's check_empty_mapping (emitter.py:459-463) → flow "{}"
// (yaml.dump({}) emits "{}\n", not a bare newline).
func (w *yamlWriter) emitRoot(om *OrderedMap) error {
	if om.Len() == 0 {
		w.writeFlowEmpty("{}")
		w.writeLineBreak("")
		return nil
	}
	if err := w.emitMapping(om, 0); err != nil {
		return err
	}
	w.writeLineBreak("")
	return nil
}

// emitMapping emits a block mapping whose keys start at column indent
// (expect_block_mapping :214-224; indentless sequences nest at the same
// column as their parent key).
func (w *yamlWriter) emitMapping(om *OrderedMap, indent int) error {
	for _, k := range om.Keys() {
		v, _ := om.Get(k)
		if !isSimpleKey(k) {
			// PyYAML expect_block_mapping_key complex branch: check_simple_key
			// fails for empty/multiline keys and for keys whose prepared tag +
			// scalar length reaches 128 (length < 128, emitter.py:437-455;
			// the implicit str tag prepares to "!!str" = 5 chars, so plain
			// string keys ≥ 123 runes go complex). Form: "? key" then the
			// value on its own line after ":" (non-indentless: nested seq
			// dashes at indent+2).
			w.writeIndent(indent)
			w.writeIndicator("?", true, false, true) // expect_block_mapping_key: write_indicator('?', True, indention=True)
			if err := w.writeComplexKeyScalar(k, indent); err != nil {
				return err
			}
			w.writeIndent(indent)
			w.writeIndicator(":", true, false, true) // expect_block_mapping_value
			if err := w.emitValueComplex(v, indent); err != nil {
				return err
			}
			continue
		}
		w.writeIndent(indent)
		if err := w.writeKey(k); err != nil {
			return err
		}
		w.writeIndicator(":", false, false, false) // expect_block_mapping_simple_value
		if err := w.emitValue(v, indent); err != nil {
			return err
		}
	}
	return nil
}

// emitSequence emits a block sequence whose dashes start at column indent.
func (w *yamlWriter) emitSequence(items []any, indent int) error {
	for _, item := range items {
		w.writeIndent(indent)
		w.writeIndicator("-", true, false, true)
		if err := w.emitItem(item, indent); err != nil {
			return err
		}
	}
	return nil
}

// emitValue emits a mapping VALUE in block context; indent is the parent
// mapping's key column (scalar continuation indent = indent+2, nested mapping
// keys = indent+2, nested sequence dashes = indent — indentless).
func (w *yamlWriter) emitValue(v any, indent int) error {
	switch x := v.(type) {
	case *OrderedMap:
		if x.Len() == 0 {
			w.writeFlowEmpty("{}")
			return nil
		}
		return w.emitMapping(x, indent+2)
	case []any:
		if len(x) == 0 {
			w.writeFlowEmpty("[]")
			return nil
		}
		return w.emitSequence(x, indent)
	case map[string]any:
		om := NewOrderedMap()
		for _, k := range sortedKeys(x) {
			om.Set(k, x[k])
		}
		return w.emitValue(om, indent)
	case map[string]string:
		om := NewOrderedMap()
		for _, k := range sortedKeys(x) {
			om.Set(k, x[k])
		}
		return w.emitValue(om, indent)
	case []map[string]any:
		items := make([]any, len(x))
		for i, m := range x {
			om := NewOrderedMap()
			for _, k := range sortedKeys(m) {
				om.Set(k, m[k])
			}
			items[i] = om
		}
		return w.emitValue(items, indent)
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		return w.emitValue(items, indent)
	default:
		return w.emitScalar(x, indent+2)
	}
}

// emitItem emits a SEQUENCE ITEM in block context; indent is the sequence's
// dash column (item mapping keys and nested sequence dashes = indent+2,
// scalar continuation = indent+2).
func (w *yamlWriter) emitItem(v any, indent int) error {
	switch x := v.(type) {
	case *OrderedMap:
		if x.Len() == 0 {
			w.writeFlowEmpty("{}")
			return nil
		}
		return w.emitMapping(x, indent+2)
	case []any:
		if len(x) == 0 {
			w.writeFlowEmpty("[]")
			return nil
		}
		return w.emitSequence(x, indent+2)
	case map[string]any:
		om := NewOrderedMap()
		for _, k := range sortedKeys(x) {
			om.Set(k, x[k])
		}
		return w.emitItem(om, indent)
	case map[string]string:
		om := NewOrderedMap()
		for _, k := range sortedKeys(x) {
			om.Set(k, x[k])
		}
		return w.emitItem(om, indent)
	case []map[string]any:
		items := make([]any, len(x))
		for i, m := range x {
			om := NewOrderedMap()
			for _, k := range sortedKeys(m) {
				om.Set(k, m[k])
			}
			items[i] = om
		}
		return w.emitItem(items, indent)
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		return w.emitItem(items, indent)
	default:
		return w.emitScalar(x, indent+2)
	}
}

// writeFlowEmpty emits an empty collection in flow style (`[]`/`{}`) —
// check_empty_sequence/check_empty_mapping force flow even with
// default_flow_style=False.
func (w *yamlWriter) writeFlowEmpty(ind string) {
	if w.whitespace {
		w.b.WriteString(ind)
		w.col += len(ind)
	} else {
		w.b.WriteString(" " + ind)
		w.col += 1 + len(ind)
	}
	w.whitespace = false
	w.indention = false
}

// ── Scalar style analysis (emitter.py analyze_scalar :517-737) ─────

type yamlScalarAnalysis struct {
	empty             bool
	multiline         bool
	allowBlockPlain   bool
	allowSingleQuoted bool
	allowDoubleQuoted bool
}

func yamlIsBreak(r rune) bool {
	return r == '\n' || r == 0x85 || r == '\u2028' || r == '\u2029'
}

func yamlIsWS(r rune) bool {
	return r == 0 || r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == 0x85 || r == '\u2028' || r == '\u2029'
}

func analyzeScalar(s string) yamlScalarAnalysis {
	rs := []rune(s)
	if len(rs) == 0 {
		return yamlScalarAnalysis{empty: true, allowBlockPlain: true, allowSingleQuoted: true, allowDoubleQuoted: true}
	}
	blockIndicators := false
	leadingSpace, leadingBreak := false, false
	trailingSpace, trailingBreak := false, false
	breakSpace, spaceBreak := false, false
	lineBreaks, specialChars := false, false

	first := rs[0]
	followedByWS := len(rs) == 1 || yamlIsWS(rs[1])
	if strings.HasPrefix(s, "---") || strings.HasPrefix(s, "...") {
		blockIndicators = true
	}
	if strings.ContainsRune("#,[]{}&*!|>'\"%@`", first) {
		blockIndicators = true
	}
	if (first == '?' || first == ':') && followedByWS {
		blockIndicators = true
	}
	if first == '-' && followedByWS {
		blockIndicators = true
	}

	precededByWS := true
	previousSpace, previousBreak := false, false
	for i, ch := range rs {
		followed := i+1 >= len(rs) || yamlIsWS(rs[i+1])
		if i > 0 {
			if ch == ':' && followed {
				blockIndicators = true
			}
			if ch == '#' && precededByWS {
				blockIndicators = true
			}
		}
		if yamlIsBreak(ch) {
			lineBreaks = true
		}
		if !(ch == '\n' || ('\x20' <= ch && ch <= '\x7E')) {
			uni := ch == 0x85 || ('\xA0' <= ch && ch <= '\uD7FF') ||
				('\uE000' <= ch && ch <= '\uFFFD') || (0x10000 <= ch && ch < 0x10FFFF)
			if !(uni && ch != '\uFEFF') {
				specialChars = true
			}
		}
		if ch == ' ' {
			if i == 0 {
				leadingSpace = true
			}
			if i == len(rs)-1 {
				trailingSpace = true
			}
			if previousBreak {
				breakSpace = true
			}
			previousSpace = true
			previousBreak = false
		} else if yamlIsBreak(ch) {
			if i == 0 {
				leadingBreak = true
			}
			if i == len(rs)-1 {
				trailingBreak = true
			}
			if previousSpace {
				spaceBreak = true
			}
			previousSpace = false
			previousBreak = true
		} else {
			previousSpace = false
			previousBreak = false
		}
		precededByWS = yamlIsWS(ch)
	}

	allowBlockPlain := true
	allowSingleQuoted := true
	allowDoubleQuoted := true
	if leadingSpace || leadingBreak || trailingSpace || trailingBreak {
		allowBlockPlain = false
	}
	if breakSpace {
		allowBlockPlain, allowSingleQuoted = false, false
	}
	if spaceBreak || specialChars {
		allowBlockPlain, allowSingleQuoted = false, false
	}
	if lineBreaks {
		allowBlockPlain = false
	}
	if blockIndicators {
		allowBlockPlain = false
	}
	return yamlScalarAnalysis{
		multiline:         lineBreaks,
		allowBlockPlain:   allowBlockPlain,
		allowSingleQuoted: allowSingleQuoted,
		allowDoubleQuoted: allowDoubleQuoted,
	}
}

// ── Implicit resolver (resolver.py) — plain style is forbidden when the
// value resolves to a non-str tag (bool/int/float/null/timestamp/merge/value).

var (
	yamlBoolRe  = regexp.MustCompile(`^(?:yes|Yes|YES|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF)$`)
	yamlFloatRe = regexp.MustCompile(`^(?:[-+]?(?:[0-9][0-9_]*)\.[0-9_]*(?:[eE][-+][0-9]+)?|\.[0-9][0-9_]*(?:[eE][-+][0-9]+)?|[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+\.[0-9_]*|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)
	yamlIntRe   = regexp.MustCompile(`^(?:[-+]?0b[0-1_]+|[-+]?0[0-7_]+|[-+]?(?:0|[1-9][0-9_]*)|[-+]?0x[0-9a-fA-F_]+|[-+]?[1-9][0-9_]*(?::[0-5]?[0-9])+)$`)
	yamlNullRe  = regexp.MustCompile(`^(?:~|null|Null|NULL| )$`)
	yamlTSRe    = regexp.MustCompile(`^(?:[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]|[0-9][0-9][0-9][0-9]-[0-9][0-9]?-[0-9][0-9]?(?:[Tt]|[ \t]+)[0-9][0-9]?:[0-9][0-9]:[0-9][0-9](?:\.[0-9]*)?(?:[ \t]*(?:Z|[-+][0-9][0-9]?(?::[0-9][0-9])?))?)$`)
	yamlMergeRe = regexp.MustCompile(`^(?:<<)$`)
	yamlValueRe = regexp.MustCompile(`^(?:=)$`)
)

// yamlResolvesToStr mirrors Resolver.resolve(ScalarNode, value, (True, False)):
// the per-first-character resolver groups are consulted in add order (float,
// int, timestamp for digits; bool/null/... for their first chars) and the
// first regex match decides the tag. True means the value resolves to a
// string (plain style allowed for strings).
func yamlResolvesToStr(s string) bool {
	if s == "" {
		return false // '' group → null tag
	}
	switch s[0] {
	case 'y', 'Y', 't', 'T', 'f', 'F', 'o', 'O':
		return !yamlBoolRe.MatchString(s)
	case 'n', 'N':
		if yamlBoolRe.MatchString(s) {
			return false
		}
		return !yamlNullRe.MatchString(s)
	case '~':
		return !yamlNullRe.MatchString(s)
	case '-', '+', '.', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if yamlFloatRe.MatchString(s) {
			return false
		}
		if yamlIntRe.MatchString(s) {
			return false
		}
		if s[0] >= '0' && s[0] <= '9' {
			return !yamlTSRe.MatchString(s)
		}
		return true
	case '<':
		return !yamlMergeRe.MatchString(s)
	case '=':
		return !yamlValueRe.MatchString(s)
	default:
		return true
	}
}

// ── Scalar writers (emitter.py choose_scalar_style + writers) ──────

// chooseStringStyle ports choose_scalar_style (:494-517) for a block-context
// string scalar: plain when allowed and the implicit resolver keeps it a
// string; otherwise single-quoted; otherwise double-quoted.
func (w *yamlWriter) chooseStringStyle(s string, isKey bool) (byte, error) {
	a := analyzeScalar(s)
	if isKey && (a.empty || a.multiline) {
		return 0, fmt.Errorf("yaml: cannot emit %q as a simple mapping key", s)
	}
	if a.allowBlockPlain && yamlResolvesToStr(s) {
		return 'p', nil
	}
	if a.allowSingleQuoted {
		return '\'', nil
	}
	return '"', nil
}

// isSimpleKey ports Emitter.check_simple_key (emitter.py:437-455) for our
// string keys: simple iff prepared length (tag "!!str" = 5 + scalar) < 128 and
// the scalar is non-empty and single-line.
func isSimpleKey(s string) bool {
	rs := []rune(s)
	if len(rs) >= 123 { // 5 + len < 128
		return false
	}
	a := analyzeScalar(s)
	return !a.empty && !a.multiline
}

// writeComplexKeyScalar writes the key of a complex `? key` form mapping
// entry. The key is emitted with simple_key_context=False (PyYAML
// expect_node(mapping=True)), so normal block scalar style rules apply:
// plain when allowed, else single- or double-quoted; split=True with wrap
// continuation at indent+2 (the increased indent during expect_scalar).
func (w *yamlWriter) writeComplexKeyScalar(s string, indent int) error {
	a := analyzeScalar(s)
	style := byte('p')
	if !(a.allowBlockPlain && yamlResolvesToStr(s)) {
		if a.allowSingleQuoted {
			style = '\''
		} else {
			style = '"'
		}
	}
	switch style {
	case 'p':
		w.writePlain(s, true, indent+2)
	case '\'':
		w.writeSingleQuoted(s, true, indent+2)
	case '"':
		w.writeDoubleQuoted(s, true, indent+2)
	}
	return nil
}

// emitValueComplex emits a block-mapping VALUE in the complex `? key` form.
// Identical to emitValue except the block sequence dashes are NOT indentless
// (expect_block_mapping_value leaves indention=True, so indentless=False in
// expect_sequence): dashes sit at indent+2 rather than the parent key column.
func (w *yamlWriter) emitValueComplex(v any, indent int) error {
	switch x := v.(type) {
	case *OrderedMap:
		if x.Len() == 0 {
			w.writeFlowEmpty("{}")
			return nil
		}
		return w.emitMapping(x, indent+2)
	case []any:
		if len(x) == 0 {
			w.writeFlowEmpty("[]")
			return nil
		}
		return w.emitSequence(x, indent+2)
	case map[string]any:
		om := NewOrderedMap()
		for _, k := range sortedKeys(x) {
			om.Set(k, x[k])
		}
		return w.emitValueComplex(om, indent)
	case map[string]string:
		om := NewOrderedMap()
		for _, k := range sortedKeys(x) {
			om.Set(k, x[k])
		}
		return w.emitValueComplex(om, indent)
	case []map[string]any:
		items := make([]any, len(x))
		for i, m := range x {
			om := NewOrderedMap()
			for _, k := range sortedKeys(m) {
				om.Set(k, m[k])
			}
			items[i] = om
		}
		return w.emitValueComplex(items, indent)
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		return w.emitValueComplex(items, indent)
	default:
		return w.emitScalar(x, indent+2)
	}
}

func (w *yamlWriter) writeKey(s string) error {
	style, err := w.chooseStringStyle(s, true)
	if err != nil {
		return err
	}
	// Simple keys never wrap (split=False) and never have a prefix space
	// (writeIndent left whitespace=true).
	switch style {
	case 'p':
		w.writePlain(s, false, 0)
	case '\'':
		w.writeSingleQuoted(s, false, 0)
	case '"':
		w.writeDoubleQuoted(s, false, 0)
	}
	return nil
}

func (w *yamlWriter) emitScalar(v any, valueIndent int) error {
	switch x := v.(type) {
	case nil:
		w.writePlain("null", true, valueIndent)
	case bool:
		if x {
			w.writePlain("true", true, valueIndent)
		} else {
			w.writePlain("false", true, valueIndent)
		}
	case string:
		style, err := w.chooseStringStyle(x, false)
		if err != nil {
			return err
		}
		switch style {
		case 'p':
			w.writePlain(x, true, valueIndent)
		case '\'':
			w.writeSingleQuoted(x, true, valueIndent)
		case '"':
			w.writeDoubleQuoted(x, true, valueIndent)
		}
	case int:
		w.writePlain(strconv.Itoa(x), true, valueIndent)
	case int8:
		w.writePlain(strconv.FormatInt(int64(x), 10), true, valueIndent)
	case int16:
		w.writePlain(strconv.FormatInt(int64(x), 10), true, valueIndent)
	case int32:
		w.writePlain(strconv.FormatInt(int64(x), 10), true, valueIndent)
	case int64:
		w.writePlain(strconv.FormatInt(x, 10), true, valueIndent)
	case uint:
		w.writePlain(strconv.FormatUint(uint64(x), 10), true, valueIndent)
	case uint8:
		w.writePlain(strconv.FormatUint(uint64(x), 10), true, valueIndent)
	case uint16:
		w.writePlain(strconv.FormatUint(uint64(x), 10), true, valueIndent)
	case uint32:
		w.writePlain(strconv.FormatUint(uint64(x), 10), true, valueIndent)
	case uint64:
		w.writePlain(strconv.FormatUint(x, 10), true, valueIndent)
	case float64:
		w.writePlain(pythonFloatRepr(x), true, valueIndent)
	case float32:
		w.writePlain(pythonFloatRepr(float64(x)), true, valueIndent)
	default:
		return fmt.Errorf("yaml: unsupported scalar type %T", v)
	}
	return nil
}

// writePlain ports Emitter.write_plain (emitter.py:936-971) including the
// 80-column single-space wrapping (write_indent continuation).
func (w *yamlWriter) writePlain(text string, split bool, indent int) {
	if !w.whitespace {
		w.col++
		w.b.WriteByte(' ')
	}
	w.whitespace = false
	w.indention = false
	rs := []rune(text)
	spaces, breaks := false, false
	start, end := 0, 0
	for end <= len(rs) {
		var ch rune = -1
		if end < len(rs) {
			ch = rs[end]
		}
		if spaces {
			if ch != ' ' {
				if start+1 == end && w.col > w.bestWidth && split {
					w.writeIndent(indent)
					w.whitespace = false
					w.indention = false
				} else {
					w.writeRunes(rs[start:end])
				}
				start = end
			}
		} else if breaks {
			if ch != '\n' && ch != 0x85 && ch != '\u2028' && ch != '\u2029' {
				if rs[start] == '\n' {
					w.writeLineBreak("")
				}
				for _, br := range rs[start:end] {
					if br == '\n' {
						w.writeLineBreak("")
					} else {
						w.writeLineBreak(string(br))
					}
				}
				w.writeIndent(indent)
				w.whitespace = false
				w.indention = false
				start = end
			}
		} else {
			if ch == -1 || ch == ' ' || yamlIsBreak(ch) {
				w.writeRunes(rs[start:end])
				start = end
			}
		}
		if ch != -1 {
			spaces = (ch == ' ')
			breaks = yamlIsBreak(ch)
		}
		end++
	}
}

// writeSingleQuoted ports Emitter.write_single_quoted (emitter.py:822-853):
// embedded ' doubled (”), breaks emitted as N+1 newlines then the full
// continuation indent, raw U+2028/U+2029/NEL kept as their own line breaks.
func (w *yamlWriter) writeSingleQuoted(text string, split bool, indent int) {
	w.writeIndicator("'", true, false, false)
	rs := []rune(text)
	spaces, breaks := false, false
	start, end := 0, 0
	for end <= len(rs) {
		var ch rune = -1
		if end < len(rs) {
			ch = rs[end]
		}
		if spaces {
			if ch == -1 || ch != ' ' {
				if start+1 == end && w.col > w.bestWidth && split && start != 0 && end != len(rs) {
					w.writeIndent(indent)
				} else {
					w.writeRunes(rs[start:end])
				}
				start = end
			}
		} else if breaks {
			if ch == -1 || (ch != '\n' && ch != 0x85 && ch != '\u2028' && ch != '\u2029') {
				if rs[start] == '\n' {
					w.writeLineBreak("")
				}
				for _, br := range rs[start:end] {
					if br == '\n' {
						w.writeLineBreak("")
					} else {
						w.writeLineBreak(string(br))
					}
				}
				w.writeIndent(indent)
				start = end
			}
		} else {
			if ch == -1 || ch == ' ' || yamlIsBreak(ch) || ch == '\'' {
				if start < end {
					w.writeRunes(rs[start:end])
					start = end
				}
			}
			if ch == '\'' {
				w.b.WriteString("''")
				w.col += 2
				start = end + 1
			}
		}
		if ch != -1 {
			spaces = (ch == ' ')
			breaks = yamlIsBreak(ch)
		}
		end++
	}
	w.writeIndicator("'", false, false, false)
}

// yamlDoubleEscapes mirrors Emitter.ESCAPE_REPLACEMENTS (emitter.py:871-886).
var yamlDoubleEscapes = map[rune]string{
	0:        "0",
	0x07:     "a",
	0x08:     "b",
	0x09:     "t",
	0x0A:     "n",
	0x0B:     "v",
	0x0C:     "f",
	0x0D:     "r",
	0x1B:     "e",
	'"':      `"`,
	'\\':     `\`,
	0x85:     "N",
	0xA0:     "_",
	'\u2028': "L",
	'\u2029': "P",
}

// writeDoubleQuoted ports Emitter.write_double_quoted (emitter.py:887-914):
// used only when the analyzer forbids plain and single-quoted styles
// (control characters such as tab, or space-before-break runs).
func (w *yamlWriter) writeDoubleQuoted(text string, split bool, indent int) {
	w.writeIndicator("\"", true, false, false)
	rs := []rune(text)
	start, end := 0, 0
	for end <= len(rs) {
		var ch rune = -1
		if end < len(rs) {
			ch = rs[end]
		}
		if ch == -1 || ch == '"' || ch == '\\' || ch == 0x85 || ch == '\u2028' || ch == '\u2029' || ch == '\uFEFF' ||
			!((0x20 <= ch && ch <= 0x7E) || (w.allowUnicode && ((0xA0 <= ch && ch <= 0xD7FF) || (0xE000 <= ch && ch <= 0xFFFD)))) {
			if start < end {
				w.writeRunes(rs[start:end])
				start = end
			}
			if ch != -1 {
				var esc string
				if rep, ok := yamlDoubleEscapes[ch]; ok {
					esc = "\\" + rep
				} else if ch <= 0xFF {
					esc = fmt.Sprintf("\\x%02X", ch)
				} else if ch <= 0xFFFF {
					esc = fmt.Sprintf("\\u%04X", ch)
				} else {
					esc = fmt.Sprintf("\\U%08X", ch)
				}
				w.col += len(esc)
				w.b.WriteString(esc)
				start = end + 1
			}
		}
		if 0 < end && end < len(rs)-1 && (ch == ' ' || start >= end) && w.col+(end-start) > w.bestWidth && split {
			// Python slices clamp: text[start:end] with start > end (after an
			// escape write) yields ""; Go slices panic, so clamp the window.
			lo := start
			if lo > end {
				lo = end
			}
			data := string(rs[lo:end]) + "\\"
			if start < end {
				start = end
			}
			w.col += len(data)
			w.b.WriteString(data)
			w.writeIndent(indent)
			w.whitespace = false
			w.indention = false
			if start < len(rs) && rs[start] == ' ' {
				w.b.WriteByte('\\')
				w.col++
			}
		}
		end++
	}
	w.writeIndicator("\"", false, false, false)
}

// pythonFloatRepr ports PyYAML 6.0.3 Representer.represent_float
// (representer.py): repr(data).lower() with PyYAML's '.0e' insertion for
// integral mantissas in scientific form (repr(1e17)='1e+17' → '1.0e+17').
// Go's strconv 'e' shortest produces the same shortest digits as Python's
// repr; the %e/%f split mirrors Python (scientific when the exponent is
// < -4 or >= 16).
func pythonFloatRepr(f float64) string {
	if f != f {
		return ".nan"
	}
	if f == math.Inf(1) {
		return ".inf"
	}
	if f == math.Inf(-1) {
		return "-.inf"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64) // "1e+17", "9.99e+15", "-0e+00"
	eIdx := strings.IndexByte(s, 'e')
	mant := s[:eIdx]
	expStr := s[eIdx+1:]
	exp, _ := strconv.Atoi(expStr)
	if exp < -4 || exp >= 16 {
		val := mant + "e" + expStr
		if !strings.Contains(val, ".") && strings.Contains(val, "e") {
			val = strings.Replace(val, "e", ".0e", 1)
		}
		return val
	}
	sign := ""
	m := mant
	if m[0] == '-' || m[0] == '+' {
		if m[0] == '-' {
			sign = "-"
		}
		m = m[1:]
	}
	digits := strings.Replace(m, ".", "", 1)
	pointPos := exp + 1
	var val string
	switch {
	case pointPos <= 0:
		val = "0." + strings.Repeat("0", -pointPos) + digits
	case pointPos >= len(digits):
		val = digits + strings.Repeat("0", pointPos-len(digits))
	default:
		val = digits[:pointPos] + "." + digits[pointPos:]
	}
	if !strings.Contains(val, ".") {
		val += ".0"
	}
	return sign + val
}
