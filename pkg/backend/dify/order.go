package dify

import (
	"bytes"
	"encoding/json"
)

// OrderedMap is an insertion-ordered string-keyed map that marshals to JSON
// in insertion order. Every dictionary emitted by SerializeToDict
// (serialize.go) is an OrderedMap so that the wire key order exactly matches
// the Python serializer's dict insertion order (serializer.py). Plain Go
// maps are never iterated into the output; when a value is a plain map it is
// marshaled by encoding/json, which sorts keys deterministically (see
// serialize.go for the deviation note).
//
// HTML escaping is disabled (SetEscapeHTML(false)) to match Python's
// json.dumps(ensure_ascii=False) byte-for-byte: Go's default json.Marshal
// would emit \u003c/\u003e/\u0026 for < > &.
//
// Task 26+ note: SerializeToDict stores shared value pointers (e.g. node
// data) inside OrderedMap values. A future serialization-pass (Task 26
// follow-up) that deep-copies or freezes these values is evaluated there;
// the current build-phase AST is never mutated after lowering, so sharing is
// safe today.
type OrderedMap struct {
	keys []string
	m    map[string]any
}

// NewOrderedMap builds an empty insertion-ordered map.
func NewOrderedMap() *OrderedMap {
	return &OrderedMap{m: map[string]any{}}
}

// Set inserts or replaces key k. A duplicate Set keeps the original
// insertion position (Python dict semantics).
func (om *OrderedMap) Set(k string, v any) {
	if om.m == nil {
		om.m = map[string]any{}
	}
	if _, ok := om.m[k]; !ok {
		om.keys = append(om.keys, k)
	}
	om.m[k] = v
}

// Get returns the value for key k and whether it was present.
func (om *OrderedMap) Get(k string) (any, bool) {
	v, ok := om.m[k]
	return v, ok
}

// Keys returns the insertion-ordered key list.
func (om *OrderedMap) Keys() []string { return om.keys }

// Values returns the insertion-ordered value list.
func (om *OrderedMap) Values() []any {
	out := make([]any, 0, len(om.keys))
	for _, k := range om.keys {
		out = append(out, om.m[k])
	}
	return out
}

// Len returns the number of keys.
func (om *OrderedMap) Len() int { return len(om.keys) }

// noEscapeMarshal serializes v with Python-style escaping (no HTML escapes,
// no ASCII-only escaping) — json.dumps(ensure_ascii=False) parity. Go's
// encoding/json always escapes U+2028/U+2029 even with SetEscapeHTML(false)
// (they are valid JSON but not valid JavaScript); Python emits them raw, so
// the encoded output is post-processed back to UTF-8 (unescapeLineSeps).
func noEscapeMarshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return unescapeLineSeps(out), nil
}

// unescapeLineSeps rewrites Go's always-escaped \u2028/\u2029 sequences
// back to the raw UTF-8 characters, matching json.dumps(ensure_ascii=False).
// The scan is escape-aware: encoding/json doubles every literal backslash
// (\\), so only a single backslash followed by u2028/u2029 is a real
// U+2028/U+2029 escape; doubled backslashes pass through untouched and the
// literal 5-char text "u2028" stays literal.
func unescapeLineSeps(out []byte) []byte {
	res := make([]byte, 0, len(out))
	for i := 0; i < len(out); {
		if out[i] == '\\' && i+1 < len(out) {
			if out[i+1] == '\\' {
				// Escaped literal backslash — copy both and keep scanning.
				res = append(res, out[i], out[i+1])
				i += 2
				continue
			}
			if i+5 < len(out) && out[i+1] == 'u' &&
				out[i+2] == '2' && out[i+3] == '0' && out[i+4] == '2' &&
				(out[i+5] == '8' || out[i+5] == '9') {
				// Real U+2028/U+2029 escape: emit the raw UTF-8 bytes
				// (E2 80 A8 / E2 80 A9).
				res = append(res, 0xE2, 0x80, out[i+5]&0x01|0xA8)
				i += 6
				continue
			}
		}
		res = append(res, out[i])
		i++
	}
	return res
}

// MarshalJSON emits the map as a JSON object with keys in insertion order.
// Nested OrderedMap values marshal recursively (encoding/json honors the
// json.Marshaler interface); plain map values are marshaled by encoding/json
// with its deterministic sorted-key order.
func (om *OrderedMap) MarshalJSON() ([]byte, error) {
	if om == nil {
		return []byte("null"), nil
	}
	buf := make([]byte, 0, 64)
	buf = append(buf, '{')
	for i, k := range om.keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		kb, err := noEscapeMarshal(k)
		if err != nil {
			return nil, err
		}
		buf = append(buf, kb...)
		buf = append(buf, ':')
		vb, err := noEscapeMarshal(om.m[k])
		if err != nil {
			return nil, err
		}
		buf = append(buf, vb...)
	}
	buf = append(buf, '}')
	return buf, nil
}
